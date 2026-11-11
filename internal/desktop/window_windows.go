//go:build windows

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	assets "aioverlay/web"
	webview "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
)

var user32 = syscall.NewLazyDLL("user32.dll")
var showWindow = user32.NewProc("ShowWindow")
var foreground = user32.NewProc("SetForegroundWindow")
var getVisible = user32.NewProc("IsWindowVisible")
var isIconic = user32.NewProc("IsIconic")
var messageBox = user32.NewProc("MessageBoxW")
var setWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
var callWindowProc = user32.NewProc("CallWindowProcW")
var defWindowProc = user32.NewProc("DefWindowProcW")
var getSystemMetrics = user32.NewProc("GetSystemMetrics")
var getWindowRect = user32.NewProc("GetWindowRect")
var getDpiForSystem = user32.NewProc("GetDpiForSystem")
var systemParametersInfo = user32.NewProc("SystemParametersInfoW")

const (
	gwlpWndProc        = -4
	wmCloseDesktop     = 0x0010
	wmNCDestroyDesktop = 0x0082
	smRemoteSession    = 0x1000
)

// isRemoteSession is diagnostic metadata only; it does not alter browser
// rendering, graphics flags or Studio capture-affinity policy.
func isRemoteSession() bool {
	m, _, _ := getSystemMetrics.Call(smRemoteSession)
	return m != 0
}

var desktopWindowMap sync.Map
var desktopSubclassProc = syscall.NewCallback(desktopSubclassWndProc)

type desktopWindow struct {
	mu           sync.Mutex
	view         webview.WebView
	options      Options
	ctx          context.Context
	captureMu    sync.Mutex
	origWndProc  uintptr
	appClosing   atomic.Bool
	diag         *studioDiag
	diagSessions atomic.Uint64
	// rendererReady is set by the first bridge call of the current session.
	// Until then no page exists that could hold an unsaved draft or answer a
	// close request, so the native close button must not depend on it.
	rendererReady atomic.Bool
	openCh        chan struct{}
	closeCh       chan struct{}
}

// studioDiag keeps a timestamped trace of Studio window creation next to
// config.json. A blank window on a user machine is environment-dependent and
// cannot be reproduced remotely, so the build reports what WebView2 actually
// did instead of guessing. Events carry no keys, prompts or request bodies.
type studioDiag struct {
	mu      sync.Mutex
	path    string
	started time.Time
	events  []map[string]any
}

func newStudioDiag(configPath string) *studioDiag {
	dir := filepath.Dir(configPath)
	if dir == "" || dir == "." {
		dir, _ = os.Getwd()
	}
	d := &studioDiag{path: filepath.Join(dir, "studio-diagnostics.json"), started: time.Now()}
	return d
}

// Path returns the diagnostics file location for user-visible messages.
func (d *studioDiag) Path() string {
	if d == nil {
		return "studio-diagnostics.json"
	}
	return d.path
}

func (d *studioDiag) record(kind string, data map[string]any) {
	if d == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	d.mu.Lock()
	d.events = append(d.events, map[string]any{"at": time.Now().Format(time.RFC3339Nano), "kind": kind, "data": data})
	if n := len(d.events); n > 300 {
		d.events = d.events[n-300:]
	}
	d.mu.Unlock()
	d.writeNow()
}

func (d *studioDiag) writeNow() {
	if d == nil {
		return
	}
	d.mu.Lock()
	raw, err := json.MarshalIndent(map[string]any{
		"started": d.started.Format(time.RFC3339),
		"pid":     os.Getpid(),
		"events":  d.events,
	}, "", "  ")
	d.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(d.path, raw, 0o600)
}

type diagRect struct{ Left, Top, Right, Bottom int32 }

func windowRectForDiag(v webview.WebView) any {
	var r diagRect
	ok, _, _ := getWindowRect.Call(uintptr(v.Window()), uintptr(unsafe.Pointer(&r)))
	if ok == 0 {
		return "unavailable"
	}
	return []int{int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)}
}

func New(o Options) Window {
	return &desktopWindow{options: o, openCh: make(chan struct{}, 1), closeCh: make(chan struct{}, 1)}
}

// Run owns only the Studio session manager. Closing a WebView2 session does
// not end AIOverlay; another Show request creates a fresh session on demand.
func (w *desktopWindow) Run(ctx context.Context) error {
	w.mu.Lock()
	w.ctx = ctx
	initiallyVisible := w.options.Visible
	if w.diag == nil {
		w.diag = newStudioDiag(w.options.ConfigPath)
	}
	diag := w.diag
	w.mu.Unlock()
	start := map[string]any{
		"autoOpen":      initiallyVisible,
		"remoteSession": isRemoteSession(),
		"browserArgs":   os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"),
	}
	if version, err := webviewloader.GetInstalledVersion(); err != nil {
		start["webview2Error"] = err.Error()
	} else {
		start["webview2Version"] = version
	}
	diag.record("process_start", start)
	defer diag.writeNow()
	go sweepStaleProfiles()

	var life sessionLifecycle
	var sessionDone chan error
	var sessionCancel context.CancelFunc

	startSession := func() {
		sessionCtx, cancel := context.WithCancel(ctx)
		sessionCancel = cancel
		sessionDone = make(chan error, 1)
		done := sessionDone
		go func() { done <- w.runSession(sessionCtx) }()
	}
	if initiallyVisible {
		life.running = true
		startSession()
	}

	for {
		select {
		case <-ctx.Done():
			w.appClosing.Store(true)
			if life.running && sessionCancel != nil {
				sessionCancel()
				select {
				case <-sessionDone:
				case <-time.After(3 * time.Second):
				}
			}
			return nil
		case <-w.openCh:
			start, show := life.requestOpen()
			if start {
				startSession()
			} else if show {
				w.showActive()
			}
		case <-w.closeCh:
			if life.requestClose() && sessionCancel != nil {
				sessionCancel()
			}
		case err := <-sessionDone:
			sessionCancel = nil
			sessionDone = nil
			restart := life.sessionEnded()
			end := map[string]any{}
			if err != nil {
				end["error"] = err.Error()
			}
			w.diag.record("session_end", end)
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				w.diag.record("session_failed", map[string]any{"error": err.Error()})
				ShowError("AIOverlay · Pipeline Studio", err.Error())
			}
			if restart && ctx.Err() == nil {
				startSession()
			}
		}
	}
}

func (w *desktopWindow) runSession(ctx context.Context) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	uninit, err := initializeSTA()
	if err != nil {
		return err
	}
	defer uninit()
	ctx, endSession := context.WithCancel(ctx)
	defer endSession()
	defer func() {
		if w.options.HotkeyEditor != nil {
			w.options.HotkeyEditor(false)
		}
	}()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := webviewloader.GetInstalledVersion(); err != nil {
		w.diag.record("webview2_missing", map[string]any{"error": err.Error()})
		return fmt.Errorf("для встроенного интерфейса нужен Microsoft Edge WebView2 Runtime. Запустите Install-WebView2.ps1 из папки приложения, затем снова откройте Studio: %w", err)
	}
	// Each lazy session gets an isolated temporary profile. Destroying the
	// session releases WebView2 and deletes its profile; portable state remains
	// in config.json and Go-owned trace state.
	profile, err := os.MkdirTemp("", studioProfilePrefix+"*")
	if err != nil {
		return err
	}
	defer cleanupProfile(profile)
	html, err := assets.DesktopHTML()
	if err != nil {
		return err
	}
	w.mu.Lock()
	options := w.options
	w.mu.Unlock()
	w.rendererReady.Store(false)
	geometry, placed := studioWindowGeometry()
	w.diag.record("session_begin", map[string]any{
		"index":       w.diagSessions.Add(1),
		"document":    "in-process-document",
		"remote":      isRemoteSession(),
		"browserArgs": os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"),
		"htmlBytes":   len(html),
		"geometry":    geometry,
	})
	window := webview.WindowOptions{Title: options.Title, Width: studioWidthDIP, Height: studioHeightDIP}
	if placed {
		window.Width, window.Height = uint(geometry.Width), uint(geometry.Height)
		window.Positioned, window.Left, window.Top = true, geometry.X, geometry.Y
	}
	// The window is created visible, before the controller, which is the
	// standard WebView2 hosting order. Every later hide/show (capture, minimize,
	// reopen) is forwarded to the controller by the vendored webview, so the
	// page never keeps presenting only the default background (the white window).
	v := webview.NewWithOptions(webview.WebViewOptions{Debug: false, DataPath: profile, AutoFocus: true, WindowOptions: window, Background: &studioBackground})
	if v == nil {
		w.diag.record("view_create_failed", map[string]any{"browserArgs": os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS")})
		return fmt.Errorf("не удалось создать WebView2. Установите Microsoft Edge WebView2 Runtime через Install-WebView2.ps1 и повторите открытие Studio")
	}
	w.diag.record("view_created", map[string]any{"hwnd": fmt.Sprintf("0x%x", uintptr(v.Window()))})
	w.mu.Lock()
	w.view = v
	w.origWndProc = 0
	w.mu.Unlock()
	w.installCloseToUnload(v)
	watchDone := make(chan struct{})
	var requests sync.WaitGroup
	var firstRequest sync.Once
	defer func() {
		endSession()
		close(watchDone)
		requests.Wait()
		w.uninstallCloseSubclass(v)
		w.mu.Lock()
		if w.view == v {
			w.view = nil
		}
		w.origWndProc = 0
		w.mu.Unlock()
		v.Destroy()
	}()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if placed {
		v.SetSize(geometry.MinWidth, geometry.MinHeight, webview.HintMin)
	} else {
		v.SetSize(studioMinWidthDIP, studioMinHeightDIP, webview.HintMin)
	}
	w.applyIdentity(v)
	applyDarkCaption(v)
	// Studio is deliberately never hidden from screen capture: it is a config
	// editor the user opens, edits and closes, and WDA_EXCLUDEFROMCAPTURE on a
	// WebView2 window can blank presentation on some drivers. Stealth applies
	// to the overlay only.
	semaphore := make(chan struct{}, 8)
	err = v.Bind("studioDispatch", func(r Request) error {
		// Bind callbacks execute on the UI thread. All potentially slow work is
		// scheduled in Go and delivered with Dispatch; never block the message loop.
		firstRequest.Do(func() {
			w.rendererReady.Store(true)
			w.diag.record("renderer_ready", nil)
		})
		select {
		case semaphore <- struct{}{}:
		default:
			return fmt.Errorf("too many outstanding desktop requests")
		}
		requests.Add(1)
		go func() {
			defer requests.Done()
			defer func() { <-semaphore }()
			closeAfterReply := false
			var reply Reply
			switch r.Path {
			case "/desktop/close", "/desktop/hide": // hide kept for older embedded UI bundles
				closeAfterReply = true
				reply = Reply{OK: true, Data: json.RawMessage(`{}`)}
			case "/desktop/quit":
				if w.options.Quit != nil {
					w.options.Quit()
				}
				reply = Reply{OK: true, Data: json.RawMessage(`{}`)}
			case "/desktop/export":
				reply = w.exportConfig(ctx, v)
			default:
				reply = Call(ctx, w.options.Handler, r)
			}
			encoded, _ := json.Marshal(reply)
			id, _ := json.Marshal(r.ID)
			w.dispatchSession(ctx, v, func() { v.Eval("window.__studioReply(" + string(id) + "," + string(encoded) + ")") })
			if closeAfterReply && ctx.Err() == nil {
				w.Close()
			}
		}()
		return nil
	})
	if err != nil {
		return err
	}
	v.OnNavigationCompleted(func(ok bool, status int32) {
		w.diag.record("navigation_completed", map[string]any{"success": ok, "webErrorStatus": status})
	})
	v.SetHtml(html)
	w.diag.record("document_set", map[string]any{"bytes": len(html)})
	foreground.Call(uintptr(v.Window()))
	vis, _, _ := getVisible.Call(uintptr(v.Window()))
	w.diag.record("window_shown", map[string]any{"visible": vis != 0, "rect": windowRectForDiag(v)})
	go func() {
		select {
		case <-ctx.Done():
			v.Dispatch(func() { v.Destroy() })
		case <-watchDone:
		}
	}()
	v.Run()

	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func desktopSubclassWndProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	value, ok := desktopWindowMap.Load(hwnd)
	if !ok {
		r, _, _ := defWindowProc.Call(hwnd, uintptr(message), wparam, lparam)
		return r
	}
	w := value.(*desktopWindow)
	if message == wmCloseDesktop && !w.appClosing.Load() {
		if !w.rendererReady.Load() {
			// The document never reached the bridge (failed load, missing
			// runtime feature): there is no draft to protect and nobody to
			// answer, so close the session instead of ignoring the button.
			w.Close()
			return 0
		}
		// Ask the page to save a valid draft first. It will call /desktop/close,
		// which tears down this WebView2 session without exiting AIOverlay.
		go w.requestCloseFromPage()
		return 0
	}
	w.mu.Lock()
	orig := w.origWndProc
	w.mu.Unlock()
	if message == wmNCDestroyDesktop {
		desktopWindowMap.Delete(hwnd)
	}
	if orig != 0 {
		r, _, _ := callWindowProc.Call(orig, hwnd, uintptr(message), wparam, lparam)
		return r
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(message), wparam, lparam)
	return r
}

func signedPtr(v int32) uintptr { return uintptr(int64(v)) }
func (w *desktopWindow) installCloseToUnload(v webview.WebView) {
	hwnd := uintptr(v.Window())
	desktopWindowMap.Store(hwnd, w)
	orig, _, _ := setWindowLongPtr.Call(hwnd, signedPtr(gwlpWndProc), desktopSubclassProc)
	w.mu.Lock()
	w.origWndProc = orig
	w.mu.Unlock()
}
func (w *desktopWindow) uninstallCloseSubclass(v webview.WebView) {
	hwnd := uintptr(v.Window())
	w.mu.Lock()
	orig := w.origWndProc
	w.mu.Unlock()
	if orig != 0 {
		setWindowLongPtr.Call(hwnd, signedPtr(gwlpWndProc), orig)
	}
	desktopWindowMap.Delete(hwnd)
}

func (w *desktopWindow) dispatchSession(ctx context.Context, v webview.WebView, fn func()) {
	if ctx.Err() != nil {
		return
	}
	v.Dispatch(func() {
		w.mu.Lock()
		valid := w.view == v
		w.mu.Unlock()
		if valid && ctx.Err() == nil {
			fn()
		}
	})
}
func (w *desktopWindow) dispatch(fn func(webview.WebView)) {
	w.mu.Lock()
	v := w.view
	w.mu.Unlock()
	if v != nil {
		v.Dispatch(func() {
			w.mu.Lock()
			valid := w.view == v
			w.mu.Unlock()
			if valid {
				fn(v)
			}
		})
	}
}
func (w *desktopWindow) requestCloseFromPage() {
	w.dispatch(func(v webview.WebView) {
		v.Eval("window.__studioNativeClose && window.__studioNativeClose()")
	})
}
func (w *desktopWindow) showActive() {
	w.dispatch(func(v webview.WebView) {
		showWindow.Call(uintptr(v.Window()), 9)
		foreground.Call(uintptr(v.Window()))
	})
}
func (w *desktopWindow) Show() {
	select {
	case w.openCh <- struct{}{}:
	default:
	}
}
func (w *desktopWindow) Close() {
	select {
	case w.closeCh <- struct{}{}:
	default:
	}
}
func (w *desktopWindow) Diagnostics() any {
	w.mu.Lock()
	defer w.mu.Unlock()
	return map[string]any{"loaded": w.view != nil}
}

func (w *desktopWindow) Capture(fn func() ([]byte, error)) ([]byte, error) {
	w.captureMu.Lock()
	defer w.captureMu.Unlock()
	w.mu.Lock()
	v := w.view
	w.mu.Unlock()
	if v == nil {
		return fn()
	}
	done := make(chan bool, 1)
	requestCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w.dispatchSession(requestCtx, v, func() {
		visible, _, _ := getVisible.Call(uintptr(v.Window()))
		minimized, _, _ := isIconic.Call(uintptr(v.Window()))
		hide := hideForCapture(visible != 0, minimized != 0)
		if hide {
			showWindow.Call(uintptr(v.Window()), 0)
		}
		done <- hide
	})
	var visible bool
	select {
	case visible = <-done:
	case <-requestCtx.Done():
		w.mu.Lock()
		stillActive := w.view == v
		w.mu.Unlock()
		if stillActive {
			return nil, fmt.Errorf("Studio window did not acknowledge capture request")
		}
		return fn()
	}
	defer func() {
		if visible {
			w.mu.Lock()
			stillActive := w.view == v
			w.mu.Unlock()
			if stillActive {
				v.Dispatch(func() { showWindow.Call(uintptr(v.Window()), 8) })
			}
		}
	}()
	if visible {
		time.Sleep(120 * time.Millisecond)
	}
	return fn()
}

// studioBackground matches the Studio theme (--bg) so the frames before the
// first paint are dark instead of the WebView2 default white.
var studioBackground = webview.Color{R: 0x11, G: 0x15, B: 0x1d}

// studioWindowGeometry keeps the complete Studio window reachable on small
// monitors and scales it on high-DPI desktops (the process is per-monitor v2
// aware, so Win32 sizes are physical pixels).
func studioWindowGeometry() (studioGeometry, bool) {
	var r struct{ Left, Top, Right, Bottom int32 }
	if ok, _, _ := systemParametersInfo.Call(0x30, 0, uintptr(unsafe.Pointer(&r)), 0); ok == 0 { // SPI_GETWORKAREA
		return studioGeometry{}, false
	}
	dpi := 96
	if getDpiForSystem.Find() == nil {
		if v, _, _ := getDpiForSystem.Call(); v != 0 {
			dpi = int(v)
		}
	}
	return studioGeometryFor(workArea{Left: int(r.Left), Top: int(r.Top), Right: int(r.Right), Bottom: int(r.Bottom)}, dpi)
}

func (w *desktopWindow) applyIdentity(v webview.WebView) {
	k := syscall.NewLazyDLL("kernel32.dll")
	h, _, _ := k.NewProc("GetModuleHandleW").Call(0)
	name, _ := syscall.UTF16PtrFromString("APP")
	for i, size := range []uintptr{16, 32} {
		ico, _, _ := user32.NewProc("LoadImageW").Call(h, uintptr(unsafe.Pointer(name)), 1, size, size, 0x8000)
		if ico != 0 {
			user32.NewProc("SendMessageW").Call(uintptr(v.Window()), 0x80, uintptr(i), ico)
		}
	}
}

// applyDarkCaption puts the non-client area (title bar) into the same dark mode as the page.
// Studio has no light theme - the page background is always dark - so a system-light caption is
// always wrong here, and Windows draws it white by default. 20 is DWMWA_USE_IMMERSIVE_DARK_MODE;
// 19 is the same attribute under its pre-20H1 number. A failure would be cosmetic only, but trying
// both makes one call enough on every supported Windows.
func applyDarkCaption(v webview.WebView) {
	set := syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	enabled := int32(1)
	for _, attribute := range []uintptr{20, 19} {
		if hr, _, _ := set.Call(uintptr(v.Window()), attribute, uintptr(unsafe.Pointer(&enabled)), unsafe.Sizeof(enabled)); int32(hr) >= 0 {
			return
		}
	}
}

// A save dialog can only export the current config; the bridge does not expose
// arbitrary filesystem reads or writes. File picker runs on the owning thread.
func (w *desktopWindow) exportConfig(ctx context.Context, v webview.WebView) Reply {
	result := make(chan Reply, 1)
	w.dispatchSession(ctx, v, func() {
		path, err := saveDialog(uintptr(v.Window()), filepath.Dir(w.options.ConfigPath))
		if err != nil {
			result <- Reply{Error: err.Error()}
			return
		}
		if path == "" {
			result <- Reply{OK: true, Data: json.RawMessage(`{"cancelled":true}`)}
			return
		}
		resp := Call(ctx, w.options.Handler, Request{Path: "/api/config", Method: "GET"})
		if !resp.OK {
			result <- resp
			return
		}
		if err := os.WriteFile(path, resp.Data, 0600); err != nil {
			result <- Reply{Error: err.Error()}
			return
		}
		result <- Reply{OK: true, Data: json.RawMessage(`{"saved":true}`)}
	})
	select {
	case r := <-result:
		return r
	case <-ctx.Done():
		return Reply{Error: "application closed"}
	}
}

type openFileName struct {
	Size            uint32
	Owner           uintptr
	Instance        uintptr
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustomFilter uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	Extension       uint16
	DefaultExt      *uint16
	CustomData      uintptr
	Hook            uintptr
	Template        *uint16
	Reserved        uintptr
	ReservedValue   uint32
	FlagsEx         uint32
}

func saveDialog(owner uintptr, dir string) (string, error) {
	buf := make([]uint16, 32768)
	name, _ := syscall.UTF16FromString("AIOverlay-config.json")
	copy(buf, name)
	// Filter pairs are NUL-separated and the list ends with a double NUL.
	filter, _ := syscall.UTF16FromString("JSON configuration")
	pattern, _ := syscall.UTF16FromString("*.json")
	filter = append(append(filter, pattern...), 0)
	title, _ := syscall.UTF16PtrFromString("Экспорт конфигурации с API-ключами")
	ext, _ := syscall.UTF16PtrFromString("json")
	initial, _ := syscall.UTF16PtrFromString(dir)
	o := openFileName{Size: uint32(unsafe.Sizeof(openFileName{})), Owner: owner, Filter: &filter[0], FilterIndex: 1, File: &buf[0], MaxFile: uint32(len(buf)), InitialDir: initial, Title: title, DefaultExt: ext, Flags: 0x80000 | 0x2 | 0x800 | 0x8}
	dll := syscall.NewLazyDLL("comdlg32.dll")
	ok, _, _ := dll.NewProc("GetSaveFileNameW").Call(uintptr(unsafe.Pointer(&o)))
	if ok == 0 {
		code, _, _ := dll.NewProc("CommDlgExtendedError").Call()
		if code != 0 {
			return "", fmt.Errorf("save dialog: 0x%x", code)
		}
		return "", nil
	}
	return syscall.UTF16ToString(buf), nil
}

func initializeSTA() (func(), error) {
	ole := syscall.NewLazyDLL("ole32.dll")
	hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, 2)
	if int32(hr) < 0 {
		return nil, fmt.Errorf("Studio COM initialization failed: 0x%08x", uint32(hr))
	}
	return func() {
		// A failed constructor may destroy HWND before Run consumes WM_QUIT. Do not
		// leave it on a Go thread that can later host another Studio session.
		var msg struct {
			Hwnd    uintptr
			Message uint32
			WParam  uintptr
			LParam  uintptr
			Time    uint32
			X, Y    int32
			Private uint32
		}
		peek := user32.NewProc("PeekMessageW")
		for {
			ok, _, _ := peek.Call(uintptr(unsafe.Pointer(&msg)), 0, 0x12, 0x12, 1)
			if ok == 0 {
				break
			}
		}
		ole.NewProc("CoUninitialize").Call()
	}, nil
}

const studioProfilePrefix = "aioverlay-studio-"

// cleanupProfile removes the per-session WebView2 profile. The browser
// processes exit asynchronously after the controller closes and keep files
// locked for a while, so a short synchronous attempt is followed by a longer
// background retry instead of leaking tens of megabytes per Studio session.
func cleanupProfile(path string) {
	for i := 0; i < 10; i++ {
		if os.RemoveAll(path) == nil {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	go func() {
		var err error
		for i := 0; i < 60; i++ {
			time.Sleep(time.Second)
			if err = os.RemoveAll(path); err == nil {
				return
			}
		}
		log.Printf("Studio profile cleanup pending: %v", err)
	}()
}

// sweepStaleProfiles deletes Studio profiles left behind by a previous crash or
// forced exit. Only day-old directories are touched, so a profile in use by a
// concurrently running instance is never removed.
func sweepStaleProfiles() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), studioProfilePrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		_ = os.RemoveAll(filepath.Join(os.TempDir(), entry.Name()))
	}
}

// CaptureBackground never blinks Studio. If an open visible Studio cannot be
// confidently excluded from capture, the automatic source is paused instead of
// risking screenshots of settings or secrets. Manual Capture keeps the existing
// hide/restore path because it is an explicit user action. Studio no longer
// uses window affinity at all, so any visible Studio window pauses background
// screen capture.
func (w *desktopWindow) CaptureBackground(fn func() ([]byte, error)) ([]byte, error) {
	w.captureMu.Lock()
	defer w.captureMu.Unlock()
	w.mu.Lock()
	v := w.view
	w.mu.Unlock()
	if v == nil {
		return fn()
	}
	result := make(chan struct{ visible, minimized bool }, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	w.dispatchSession(ctx, v, func() {
		vis, _, _ := getVisible.Call(uintptr(v.Window()))
		minimized, _, _ := isIconic.Call(uintptr(v.Window()))
		result <- struct{ visible, minimized bool }{vis != 0, minimized != 0}
	})
	select {
	case state := <-result:
		if state.visible && !state.minimized {
			return nil, suppressBackgroundCapture("studio_visible_without_capture_exclusion", "Studio is visible and not capture-excluded by design; background screen capture paused")
		}
		return fn()
	case <-ctx.Done():
		return nil, suppressBackgroundCapture("studio_capture_safety_timeout", "Studio did not acknowledge background capture safety check")
	}
}
