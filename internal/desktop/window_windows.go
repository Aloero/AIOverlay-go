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
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"aioverlay/internal/platform"
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

const (
	gwlpWndProc        = -4
	wmCloseDesktop     = 0x0010
	wmNCDestroyDesktop = 0x0082
)

var desktopWindowMap sync.Map
var desktopSubclassProc = syscall.NewCallback(desktopSubclassWndProc)

type desktopWindow struct {
	mu          sync.Mutex
	view        webview.WebView
	options     Options
	ctx         context.Context
	captureMu   sync.Mutex
	origWndProc uintptr
	protection  platform.ProtectionStatus
	appClosing  atomic.Bool
	openCh      chan struct{}
	closeCh     chan struct{}
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
	w.mu.Unlock()

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
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
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
	v := webview.NewWithOptions(webview.WebViewOptions{Debug: false, DataPath: profile, AutoFocus: true, WindowOptions: webview.WindowOptions{Title: options.Title, Width: 1320, Height: 880, Center: false, Hidden: true}})
	if v == nil {
		return fmt.Errorf("не удалось создать WebView2. Установите Microsoft Edge WebView2 Runtime через Install-WebView2.ps1 и повторите открытие Studio")
	}
	w.mu.Lock()
	w.view = v
	w.origWndProc = 0
	w.mu.Unlock()
	w.installCloseToUnload(v)
	watchDone := make(chan struct{})
	var requests sync.WaitGroup
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
	w.applyStealth(v, options.Stealth)
	semaphore := make(chan struct{}, 8)
	err = v.Bind("studioDispatch", func(r Request) error {
		// Bind callbacks execute on the UI thread. All potentially slow work is
		// scheduled in Go and delivered with Dispatch; never block the message loop.
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
	v.SetHtml(html)
	showWindow.Call(uintptr(v.Window()), 5)
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
		w.mu.Lock()
		stealth := w.options.Stealth
		w.mu.Unlock()
		w.applyStealth(v, stealth)
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
func (w *desktopWindow) SetStealth(on bool) {
	w.mu.Lock()
	w.options.Stealth = on
	v := w.view
	w.mu.Unlock()
	if v != nil {
		v.Dispatch(func() { w.applyStealth(v, on) })
	}
}
func (w *desktopWindow) applyStealth(v webview.WebView, on bool) {
	status := platform.ApplyWindowProtection(uintptr(v.Window()), on)
	w.mu.Lock()
	w.protection = status
	w.mu.Unlock()
}
func (w *desktopWindow) Diagnostics() any {
	w.mu.Lock()
	defer w.mu.Unlock()
	return map[string]any{"loaded": w.view != nil, "protection": w.protection}
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
