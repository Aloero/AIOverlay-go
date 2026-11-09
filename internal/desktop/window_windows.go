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
var affinity = user32.NewProc("SetWindowDisplayAffinity")
var getVisible = user32.NewProc("IsWindowVisible")
var isIconic = user32.NewProc("IsIconic")
var messageBox = user32.NewProc("MessageBoxW")
var setWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
var callWindowProc = user32.NewProc("CallWindowProcW")
var defWindowProc = user32.NewProc("DefWindowProcW")
var getSystemMetrics = user32.NewProc("GetSystemMetrics")
var getWindowRect = user32.NewProc("GetWindowRect")

const (
	gwlpWndProc        = -4
	wmCloseDesktop     = 0x0010
	wmNCDestroyDesktop = 0x0082
	smRemoteSession    = 0x1000
)

// isRemoteSession reports an interactive RDP session. There both hardware
// GPU composition and SetWindowDisplayAffinity can present a blank (white)
// client area, which users perceive as "Studio opens to a white screen".
func isRemoteSession() bool {
	m, _, _ := getSystemMetrics.Call(smRemoteSession)
	return m != 0
}

var desktopWindowMap sync.Map
var desktopSubclassProc = syscall.NewCallback(desktopSubclassWndProc)

type desktopWindow struct {
	mu              sync.Mutex
	view            webview.WebView
	options         Options
	ctx             context.Context
	captureMu       sync.Mutex
	origWndProc     uintptr
	appClosing      atomic.Bool
	blank           atomic.Bool
	gpuFallbackUsed atomic.Bool
	diag            *studioDiag
	diagSessions    atomic.Uint64
	openCh          chan struct{}
	closeCh         chan struct{}
}

// errStudioBlank ends a session whose document never answered the readiness
// probe so Run can retry once with software composition.
var errStudioBlank = errors.New("studio document did not respond")

// studioDiag keeps a timestamped trace of Studio window creation next to
// config.json. A blank window on a user machine is environment-dependent and
// cannot be reproduced remotely, so the build reports what WebView2 actually
// did instead of guessing. Events carry no keys, prompts or request bodies.
type studioDiag struct {
	mu         sync.Mutex
	path       string
	started    time.Time
	escalation int
	events     []map[string]any
}

func newStudioDiag(configPath string) *studioDiag {
	dir := filepath.Dir(configPath)
	if dir == "" || dir == "." {
		dir, _ = os.Getwd()
	}
	d := &studioDiag{path: filepath.Join(dir, "studio-diagnostics.json"), started: time.Now()}
	// The render ladder survives process restarts: a machine whose GPU path
	// blanks Studio should not have to rediscover software rendering daily.
	if raw, err := os.ReadFile(d.path); err == nil {
		var saved struct {
			RenderEscalation *int `json:"renderEscalation"`
		}
		if json.Unmarshal(raw, &saved) == nil && saved.RenderEscalation != nil {
			d.escalation = maxInt(0, minInt(2, *saved.RenderEscalation))
		}
	}
	return d
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// renderArgs returns browser arguments for the current ladder level:
// 0 default, 1 software composition, 2 fully software rendering.
func (d *studioDiag) renderArgs() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch d.escalation {
	case 1:
		return "--disable-gpu-compositing"
	case 2:
		return "--disable-gpu --disable-gpu-compositing"
	}
	return ""
}

func (d *studioDiag) level() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.escalation
}

// raise moves the render ladder up one step (or jumps to software rendering
// for documents that never respond at all) and persists it.
func (d *studioDiag) raise(to int) int {
	d.mu.Lock()
	if to == 2 {
		d.escalation = 2
	} else if d.escalation < 2 {
		d.escalation++
	}
	level := d.escalation
	d.mu.Unlock()
	return level
}

func (d *studioDiag) reset() {
	d.mu.Lock()
	changed := d.escalation != 0
	d.escalation = 0
	d.mu.Unlock()
	if changed {
		d.record("render_ladder_reset", map[string]any{"reason": "session worked with data requests"})
	}
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
		"started":          d.started.Format(time.RFC3339),
		"pid":              os.Getpid(),
		"renderEscalation": d.escalation,
		"events":           d.events,
	}, "", "  ")
	d.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(d.path, raw, 0o600)
}

type diagRect struct{ Left, Top, Right, Bottom int32 }

// appendBrowserArgs adds WebView2 browser arguments without overwriting the
// identity argument that platform.ApplyApplicationIdentity maintains in the
// same environment variable.
func appendBrowserArgs(extra string) {
	args := strings.TrimSpace(os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"))
	for _, want := range strings.Fields(extra) {
		if strings.Contains(args, want) {
			continue
		}
		if args != "" {
			args += " "
		}
		args += want
	}
	_ = os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", args)
}

func windowRectForDiag(v webview.WebView) any {
	var r diagRect
	ok, _, _ := getWindowRect.Call(uintptr(v.Window()), uintptr(unsafe.Pointer(&r)))
	if ok == 0 {
		return "unavailable"
	}
	return []int{int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)}
}

var (
	printWindowProc = user32.NewProc("PrintWindow")
	getDCProc       = user32.NewProc("GetDC")
	releaseDCProc   = user32.NewProc("ReleaseDC")
	gdi32           = syscall.NewLazyDLL("gdi32.dll")
	compatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	compatibleBmp   = gdi32.NewProc("CreateCompatibleBitmap")
	selectObject    = gdi32.NewProc("SelectObject")
	deleteDCProc    = gdi32.NewProc("DeleteDC")
	deleteObjProc   = gdi32.NewProc("DeleteObject")
	getDIBitsProc   = gdi32.NewProc("GetDIBits")
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// presentationLooksBlank rasterizes the window via PrintWindow with
// PW_RENDERFULLCONTENT (reaching the DirectComposition tree) and reports
// uniform output: the reported failure class paints a flat white client area
// although navigation, scripts and bridge traffic all succeed. Must run on
// the window's UI thread.
func presentationLooksBlank(v webview.WebView) map[string]any {
	out := map[string]any{}
	hwnd := uintptr(v.Window())
	var r diagRect
	if ok, _, e := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		out["error"] = "GetWindowRect: " + e.Error()
		return out
	}
	width, height := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if width < 64 || height < 64 || width > 16384 || height > 16384 {
		out["error"] = fmt.Sprintf("invalid rect %dx%d", width, height)
		return out
	}
	hdc, _, e := getDCProc.Call(hwnd)
	if hdc == 0 {
		out["error"] = "GetDC: " + e.Error()
		return out
	}
	defer releaseDCProc.Call(hdc, hwnd)
	memDC, _, e := compatibleDC.Call(hdc)
	if memDC == 0 {
		out["error"] = "CreateCompatibleDC: " + e.Error()
		return out
	}
	defer deleteDCProc.Call(memDC)
	bmp, _, e := compatibleBmp.Call(hdc, uintptr(width), uintptr(height))
	if bmp == 0 {
		out["error"] = "CreateCompatibleBitmap: " + e.Error()
		return out
	}
	defer deleteObjProc.Call(bmp)
	old, _, _ := selectObject.Call(memDC, bmp)
	defer selectObject.Call(memDC, old)
	// 2 = PW_RENDERFULLCONTENT.
	if pw, _, e := printWindowProc.Call(hwnd, memDC, 2); pw == 0 {
		out["error"] = "PrintWindow: " + e.Error()
		return out
	}
	info := bitmapInfoHeader{Size: 40, Width: int32(width), Height: int32(-height), Planes: 1, BitCount: 32}
	pixels := make([]byte, width*height*4)
	if got, _, e := getDIBitsProc.Call(memDC, bmp, 0, uintptr(height), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), 0); got == 0 {
		out["error"] = "GetDIBits: " + e.Error()
		return out
	}
	// Skip the top strip (non-client title bar) and sample a grid.
	counts := map[uint32]int{}
	total := 0
	for y := height * 8 / 100; y < height; y += max(1, height/64) {
		for x := 0; x < width; x += max(1, width/64) {
			i := (y*width + x) * 4
			color := uint32(pixels[i]) | uint32(pixels[i+1])<<8 | uint32(pixels[i+2])<<16
			counts[color]++
			total++
		}
	}
	if total == 0 {
		out["error"] = "no samples"
		return out
	}
	dominant, best := uint32(0), 0
	for c, n := range counts {
		if n > best {
			dominant, best = c, n
		}
	}
	ratio := float64(best) / float64(total)
	out["samples"] = total
	out["distinct"] = len(counts)
	out["dominantRatio"] = ratio
	out["dominantColor"] = fmt.Sprintf("%06x", dominant)
	out["blank"] = ratio > 0.985
	return out
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
		"gpuFallback":   os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"),
	}
	if version, err := webviewloader.GetInstalledVersion(); err != nil {
		start["webview2Error"] = err.Error()
	} else {
		start["webview2Version"] = version
	}
	diag.record("process_start", start)
	defer diag.writeNow()

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
			if errors.Is(err, errStudioBlank) && ctx.Err() == nil {
				startSession()
				continue
			}
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
	profile, err := os.MkdirTemp("", "aioverlay-studio-")
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
	// WebView2 honours this environment variable when it creates the browser
	// environment. Under RDP, GPU composition frequently paints the window
	// white; software output stays reliable there.
	remote := isRemoteSession()
	if remote {
		appendBrowserArgs("--disable-gpu --disable-gpu-compositing")
	} else if extra := w.diag.renderArgs(); extra != "" {
		appendBrowserArgs(extra)
	}
	w.diag.record("session_begin", map[string]any{
		"index":       w.diagSessions.Add(1),
		"remote":      remote,
		"gpuFallback": w.gpuFallbackUsed.Load(),
		"renderLevel": w.diag.level(),
		"browserArgs": os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"),
		"htmlBytes":   len(html),
	})
	pageReady := make(chan struct{})
	var readyOnce sync.Once
	v := webview.NewWithOptions(webview.WebViewOptions{Debug: false, DataPath: profile, AutoFocus: true, WindowOptions: webview.WindowOptions{Title: options.Title, Width: 1320, Height: 880, Center: false, Hidden: true}})
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
	var bridgeReqs atomic.Int64
	sessionStart := time.Now()
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
	v.SetSize(860, 560, webview.HintMin)
	w.fitToWorkArea(v)
	w.applyIdentity(v)
	// Studio is deliberately never hidden from screen capture: it is a config
	// editor the user opens, edits and closes, and WDA_EXCLUDEFROMCAPTURE on a
	// WebView2 window can blank presentation on some drivers. Stealth applies
	// to the overlay only.
	semaphore := make(chan struct{}, 8)
	err = v.Bind("studioDispatch", func(r Request) error {
		// Bind callbacks execute on the UI thread. All potentially slow work is
		// scheduled in Go and delivered with Dispatch; never block the message loop.
		bridgeReqs.Add(1)
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
			case "/desktop/ready":
				// The injected binding script proves the document is alive.
				readyOnce.Do(func() {
					close(pageReady)
					w.diag.record("renderer_ready", nil)
				})
				reply = Reply{OK: true, Data: json.RawMessage(`{}`)}
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
	var navSeen, navOK atomic.Bool
	var navStatus atomic.Int32
	v.OnNavigationCompleted(func(isSuccess bool, status int32) {
		navSeen.Store(true)
		navOK.Store(isSuccess)
		navStatus.Store(status)
		w.diag.record("navigation_completed", map[string]any{"success": isSuccess, "webErrorStatus": status})
	})
	v.SetHtml(html)
	w.diag.record("html_set", map[string]any{"bytes": len(html)})
	showWindow.Call(uintptr(v.Window()), 5)
	vis, _, _ := getVisible.Call(uintptr(v.Window()))
	w.diag.record("window_shown", map[string]any{"visible": vis != 0, "rect": windowRectForDiag(v)})
	// Probe the loaded document through the injected binding. If nothing ever
	// answers, the window is white for the user; say so explicitly instead of
	// failing silently.
	w.dispatchSession(ctx, v, func() {
		v.Eval(`window.studioDispatch && window.studioDispatch({id:"native-ready",path:"/desktop/ready",method:"POST"})`)
	})
	go func() {
		timer := time.NewTimer(8 * time.Second)
		defer timer.Stop()
		select {
		case <-pageReady:
		case <-watchDone:
		case <-ctx.Done():
		case <-timer.C:
			// The document never answered: some GPU drivers paint the window
			// white without failing navigation. Once per process, destroy the
			// session so Run restarts it with software composition; a second
			// blank session reports itself instead of looping.
			data := map[string]any{"timeoutMs": 8000, "navigationSeen": navSeen.Load(), "browserArgs": os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"), "rect": windowRectForDiag(v)}
			if navSeen.Load() {
				data["navigationSuccess"] = navOK.Load()
				data["webErrorStatus"] = navStatus.Load()
			}
			if !w.gpuFallbackUsed.Swap(true) {
				data["decision"] = "restart-with-software-composition"
				data["newLevel"] = w.diag.raise(2)
				w.diag.record("renderer_timeout", data)
				w.blank.Store(true)
				v.Dispatch(func() { v.Destroy() })
				return
			}
			data["decision"] = "report"
			w.diag.record("renderer_timeout", data)
			ShowError("AIOverlay · Pipeline Studio", "Окно Studio открылось, но интерфейс не загрузился даже в режиме программной отрисовки. Обновите Microsoft Edge WebView2 Runtime через Install-WebView2.ps1. При работе через удалённый рабочий стол помогает пересоздание сеанса RDP или локальный запуск AIOverlay.\n\nПодробный отчёт сохранён в файле studio-diagnostics.json рядом с config.json — приложите его к обращению автору.")
		}
	}()
	// Direct presentation check: screenshot the window itself once the
	// document is known alive. A uniform client area means the user sees a
	// blank white/black window despite everything working, so restart once
	// per process at the fully software rendering level.
	go func() {
		wait := time.NewTimer(6 * time.Second)
		defer wait.Stop()
		select {
		case <-pageReady:
			wait.Reset(2500 * time.Millisecond)
		case <-watchDone:
			return
		case <-ctx.Done():
			return
		case <-wait.C:
			return
		}
		select {
		case <-wait.C:
		case <-watchDone:
			return
		case <-ctx.Done():
			return
		}
		w.dispatchSession(ctx, v, func() {
			data := presentationLooksBlank(v)
			blank, _ := data["blank"].(bool)
			data["renderLevel"] = w.diag.level()
			switch {
			case !blank:
				w.diag.record("presentation_check", data)
			case !w.gpuFallbackUsed.Swap(true):
				data["decision"] = "restart-with-software-rendering"
				data["newLevel"] = w.diag.raise(2)
				w.diag.record("presentation_check", data)
				w.blank.Store(true)
				v.Dispatch(func() { v.Destroy() })
			default:
				data["decision"] = "report"
				w.diag.record("presentation_check", data)
				ShowError("AIOverlay · Pipeline Studio", "Окно Studio отрисовывается пустым даже при программном рендеринге. Отчёт сохранён в файле studio-diagnostics.json рядом с config.json — приложите его к обращению автору.")
			}
		})
	}()
	go func() {
		select {
		case <-ctx.Done():
			v.Dispatch(func() { v.Destroy() })
		case <-watchDone:
		}
	}()
	v.Run()
	if w.blank.Swap(false) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errStudioBlank
	}
	// Indirect white-window signal: users who see a blank window close it and
	// reopen immediately, even though the invisible document happily fetches
	// data. Any quick dismissal (app not exiting) climbs the render ladder;
	// a session that stayed open long enough to be actually used resets it.
	elapsed := time.Since(sessionStart)
	reqs := bridgeReqs.Load()
	switch {
	case elapsed >= 45*time.Second:
		w.diag.reset()
	case !remote && ctx.Err() != nil && !w.appClosing.Load() && elapsed < 15*time.Second:
		w.diag.record("suspected_blank_close", map[string]any{"elapsedMs": elapsed.Milliseconds(), "requests": reqs, "newLevel": w.diag.raise(0)})
	}
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

// Keep the complete Studio window reachable on small monitors and scaled desktops.
func (w *desktopWindow) fitToWorkArea(v webview.WebView) {
	var r struct{ Left, Top, Right, Bottom int32 }
	ok, _, _ := user32.NewProc("SystemParametersInfoW").Call(0x30, 0, uintptr(unsafe.Pointer(&r)), 0)
	if ok == 0 {
		return
	}
	width := min(1320, int(r.Right-r.Left)-24)
	height := min(880, int(r.Bottom-r.Top)-24)
	if width < 480 || height < 360 {
		return
	}
	v.SetSize(min(860, width), min(560, height), webview.HintMin)
	x, y := int(r.Left)+(int(r.Right-r.Left)-width)/2, int(r.Top)+(int(r.Bottom-r.Top)-height)/2
	user32.NewProc("SetWindowPos").Call(uintptr(v.Window()), 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0x4|0x10)
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
	copy(buf, syscall.StringToUTF16("AIOverlay-config.json"))
	filter := syscall.StringToUTF16("JSON configuration")
	filter = append(filter, syscall.StringToUTF16("*.json")...)
	filter = append(filter, 0)
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
func cleanupProfile(path string) {
	var err error
	for i := 0; i < 25; i++ {
		if err = os.RemoveAll(path); err == nil {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	log.Printf("Studio profile cleanup pending: %v", err)
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
