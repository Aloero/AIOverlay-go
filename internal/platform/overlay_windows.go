//go:build windows

package platform

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"aioverlay/internal/domain"
	"aioverlay/internal/hotkey"
)

const (
	wmDestroy             = 0x0002
	wmPaint               = 0x000F
	wmClose               = 0x0010
	wmNCHitTest           = 0x0084
	wmLButtonUp           = 0x0202
	wmHotKey              = 0x0312
	wmAppTray             = 0x800A
	wmLButtonDblClk       = 0x0203
	wmRButtonUp           = 0x0205
	wmContextMenu         = 0x007B
	wsPopup               = 0x80000000
	wsExTopmost           = 0x00000008
	wsExTransparent       = 0x00000020
	wsExToolWindow        = 0x00000080
	wsExLayered           = 0x00080000
	wsExNoActivate        = 0x08000000
	gwlExStyle            = -20
	hwndTopmost           = -1
	swpNoMove             = 0x0002
	swpNoSize             = 0x0001
	swpNoActivate         = 0x0010
	swpFrameChanged       = 0x0020
	swHide                = 0
	swShowNoActivate      = 4
	lwaAlpha              = 0x00000002
	modAlt                = 0x0001
	modControl            = 0x0002
	modShift              = 0x0004
	modWin                = 0x0008
	modNoRepeat           = 0x4000
	htLeft                = 10
	htRight               = 11
	htTop                 = 12
	htTopLeft             = 13
	htTopRight            = 14
	htBottom              = 15
	htBottomLeft          = 16
	htBottomRight         = 17
	htCaption             = 2
	htClient              = 1
	dtLeft                = 0x0000
	dtCenter              = 0x0001
	dtRight               = 0x0002
	dtVCenter             = 0x0004
	dtWordBreak           = 0x0010
	dtSingleLine          = 0x0020
	dtNoPrefix            = 0x0800
	dtEndEllipsis         = 0x8000
	transparent           = 1
	smCXScreen            = 0
	smCYScreen            = 1
	srcCopy               = 0x00CC0020
	biRGB                 = 0
	dibRGBColors          = 0
	wdaExcludeFromCapture = 0x00000011
	wcaExcludedFromDDA    = 24
	nimAdd                = 0x00000000
	nimDelete             = 0x00000002
	nifMessage            = 0x00000001
	nifIcon               = 0x00000002
	nifTip                = 0x00000004
	tpmRightButton        = 0x0002
	tpmReturnCmd          = 0x0100
	mfString              = 0x0000
	trayOpenStudio        = 1001
	trayToggleOverlay     = 1002
	trayPauseBackground   = 1003
	trayQuit              = 1004
	overlayHeaderHeight   = 36
	overlayEdgeHit        = 8
	overlaySettingsWidth  = 34
)

var (
	user32                            = syscall.NewLazyDLL("user32.dll")
	gdi32                             = syscall.NewLazyDLL("gdi32.dll")
	kernel32                          = syscall.NewLazyDLL("kernel32.dll")
	dwmapi                            = syscall.NewLazyDLL("dwmapi.dll")
	shell32                           = syscall.NewLazyDLL("shell32.dll")
	wtsapi32                          = syscall.NewLazyDLL("wtsapi32.dll")
	pRegisterClassEx                  = user32.NewProc("RegisterClassExW")
	pCreateWindowEx                   = user32.NewProc("CreateWindowExW")
	pDefWindowProc                    = user32.NewProc("DefWindowProcW")
	pShowWindow                       = user32.NewProc("ShowWindow")
	pUpdateWindow                     = user32.NewProc("UpdateWindow")
	pGetMessage                       = user32.NewProc("GetMessageW")
	pTranslateMessage                 = user32.NewProc("TranslateMessage")
	pDispatchMessage                  = user32.NewProc("DispatchMessageW")
	pPostQuitMessage                  = user32.NewProc("PostQuitMessage")
	pRegisterHotKey                   = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey                 = user32.NewProc("UnregisterHotKey")
	pSetWindowLongPtr                 = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtr                 = user32.NewProc("GetWindowLongPtrW")
	pSetWindowPos                     = user32.NewProc("SetWindowPos")
	pSetLayeredWindowAttributes       = user32.NewProc("SetLayeredWindowAttributes")
	pInvalidateRect                   = user32.NewProc("InvalidateRect")
	pBeginPaint                       = user32.NewProc("BeginPaint")
	pEndPaint                         = user32.NewProc("EndPaint")
	pGetClientRect                    = user32.NewProc("GetClientRect")
	pGetWindowRect                    = user32.NewProc("GetWindowRect")
	pGetCursorPos                     = user32.NewProc("GetCursorPos")
	pDrawText                         = user32.NewProc("DrawTextW")
	pFillRect                         = user32.NewProc("FillRect")
	pGetDC                            = user32.NewProc("GetDC")
	pReleaseDC                        = user32.NewProc("ReleaseDC")
	pGetSystemMetrics                 = user32.NewProc("GetSystemMetrics")
	pSetWindowDisplayAffinity         = user32.NewProc("SetWindowDisplayAffinity")
	pSetWindowCompositionAttribute    = user32.NewProc("SetWindowCompositionAttribute")
	pGetModuleHandle                  = kernel32.NewProc("GetModuleHandleW")
	pCreateCompatibleDC               = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC                         = gdi32.NewProc("DeleteDC")
	pSelectObject                     = gdi32.NewProc("SelectObject")
	pBitBlt                           = gdi32.NewProc("BitBlt")
	pDeleteObject                     = gdi32.NewProc("DeleteObject")
	pSetBkMode                        = gdi32.NewProc("SetBkMode")
	pSetTextColor                     = gdi32.NewProc("SetTextColor")
	pCreateSolidBrush                 = gdi32.NewProc("CreateSolidBrush")
	pCreateFont                       = gdi32.NewProc("CreateFontW")
	pDwmFlush                         = dwmapi.NewProc("DwmFlush")
	pShellNotifyIcon                  = shell32.NewProc("Shell_NotifyIconW")
	pWTSRegisterSessionNotification   = wtsapi32.NewProc("WTSRegisterSessionNotification")
	pWTSUnregisterSessionNotification = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }
type msg struct {
	Hwnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}
type wndClassEx struct {
	Size, Style                        uint32
	WndProc                            uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	IconSm                             uintptr
}
type paintStruct struct {
	DC                 uintptr
	Erase              int32
	Paint              rect
	Restore, IncUpdate int32
	RGBReserved        [32]byte
}
type bitmapInfoHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
}
type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}
type compositionData struct {
	Attrib int32
	pad    int32
	Data   unsafe.Pointer
	Size   uintptr
}
type notifyIconData struct {
	Size             uint32
	Hwnd             uintptr
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             uintptr
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUIDItem         [16]byte
	BalloonIcon      uintptr
}

var windowMap sync.Map

type winOverlay struct {
	uiCalls                               chan func()
	done                                  chan struct{}
	registered                            map[string]string
	suspended                             atomic.Bool
	suspendUntil                          atomic.Int64
	visibility                            visibilityState
	configGhost                           bool
	hotkeyError                           string
	lifecycleError                        string
	wtsRegistered                         bool
	protection                            ProtectionStatus
	mu                                    sync.RWMutex
	hwnd                                  atomic.Uintptr
	capturing                             atomic.Bool
	cfg                                   domain.OverlayConfig
	cb                                    Callbacks
	text, meta                            string
	ghost                                 bool
	closed                                bool
	className                             *uint16
	brush, headerBrush, buttonBrush, font uintptr
	stationaryPos                         point
	stationarySince                       time.Time
	stationaryFired                       bool
}

func NewOverlay(cfg domain.OverlayConfig, cb Callbacks) (Overlay, error) {
	return &winOverlay{cfg: cfg, cb: cb, ghost: cfg.ClickThrough, configGhost: cfg.ClickThrough, uiCalls: make(chan func(), 16), done: make(chan struct{})}, nil
}

func (o *winOverlay) Run(ctx context.Context) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(o.done)
	if err := o.createWindow(); err != nil {
		return err
	}
	defer o.destroyResources()
	go o.guardLoop(ctx)
	go func() {
		<-ctx.Done()
		if o.hwnd.Load() != 0 {
			pPostMessage := user32.NewProc("PostMessageW")
			pPostMessage.Call(o.hwnd.Load(), wmClose, 0, 0)
		}
	}()
	var m msg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}
func (o *winOverlay) createWindow() error {
	inst, _, _ := pGetModuleHandle.Call(0)
	name, _ := syscall.UTF16PtrFromString("AIOverlayGoWindowClass")
	o.className = name
	wc := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: syscall.NewCallback(globalWndProc), Instance: inst, ClassName: name}
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	style := uintptr(wsExTopmost | wsExToolWindow | wsExLayered | wsExNoActivate)
	if o.ghost {
		style |= wsExTransparent
	}
	w, h := o.cfg.Width, o.cfg.Height
	if w < 320 {
		w = 640
	}
	if h < 160 {
		h = 380
	}
	sw, _, _ := pGetSystemMetrics.Call(smCXScreen)
	x := int(sw) - w - 30
	if x < 0 {
		x = 0
	}
	y := 30
	if o.cfg.Positioned {
		x = o.cfg.X
		y = o.cfg.Y
	}
	hwnd, _, e := pCreateWindowEx.Call(style, uintptr(unsafe.Pointer(name)), 0, wsPopup, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, inst, 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %v", e)
	}
	o.hwnd.Store(hwnd)
	windowMap.Store(hwnd, o)
	o.registerLifecycleNotifications(hwnd)
	o.recreateGDI()
	o.applyNative()
	if err := o.registerHotkeys(); err != nil {
		nativeSettingError(err.Error())
	}
	o.addTrayIcon()
	pShowWindow.Call(hwnd, swShowNoActivate)
	pUpdateWindow.Call(hwnd)
	return nil
}
func (o *winOverlay) registerLifecycleNotifications(hwnd uintptr) {
	result, _, callErr := pWTSRegisterSessionNotification.Call(hwnd, 0) // NOTIFY_FOR_THIS_SESSION
	o.mu.Lock()
	defer o.mu.Unlock()
	if result == 0 {
		o.lifecycleError = fmt.Sprintf("WTS session lifecycle registration failed: %v", callErr)
		o.wtsRegistered = false
		return
	}
	o.lifecycleError = ""
	o.wtsRegistered = true
}

func (o *winOverlay) unregisterLifecycleNotifications() {
	o.mu.Lock()
	registered := o.wtsRegistered
	o.wtsRegistered = false
	o.mu.Unlock()
	if registered && o.hwnd.Load() != 0 {
		pWTSUnregisterSessionNotification.Call(o.hwnd.Load())
	}
}

func globalWndProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	v, ok := windowMap.Load(hwnd)
	if !ok {
		r, _, _ := pDefWindowProc.Call(hwnd, uintptr(message), wparam, lparam)
		return r
	}
	return v.(*winOverlay).wndProc(message, wparam, lparam)
}
func (o *winOverlay) wndProc(message uint32, wparam, lparam uintptr) uintptr {
	switch message {
	case 0x8006:
		for {
			select {
			case fn := <-o.uiCalls:
				fn()
			default:
				return 0
			}
		}
	case wmPowerBroadcast:
		if reason, suspend := backgroundSuspendReason(message, wparam); suspend && o.cb.SuspendBackground != nil {
			go o.cb.SuspendBackground(reason)
		}
		return 1
	case wmWTSSessionChange:
		if reason, suspend := backgroundSuspendReason(message, wparam); suspend && o.cb.SuspendBackground != nil {
			go o.cb.SuspendBackground(reason)
		}
		return 0
	case 0x0232: // WM_EXITSIZEMOVE
		o.saveGeometry()
		return 0

	case 0x8002:
		o.applyCaptureExclusion(true)
		return 0
	case 0x8003:
		o.checkStationary()
		return 0
	case 0x8007:
		o.toggleVisibility()
		return 0
	case 0x8004:
		if !o.visibility.mayShow() {
			return 0
		}
		o.applyNative()
		pShowWindow.Call(o.hwnd.Load(), swShowNoActivate)
		return 0
	case 0x8005:
		pShowWindow.Call(o.hwnd.Load(), swHide)
		return 0
	case wmAppTray:
		o.handleTrayMessage(uint32(lparam))
		return 0
	case wmPaint:
		o.paint()
		return 0
	case wmNCHitTest:
		o.mu.RLock()
		ghost := o.ghost
		o.mu.RUnlock()
		if !ghost {
			return o.hitTest()
		}
		return htClient
	case wmLButtonUp:
		o.mu.RLock()
		ghost := o.ghost
		o.mu.RUnlock()
		if !ghost {
			x, y := clientPointFromLParam(lparam)
			if o.settingsHit(x, y) && o.cb.Control != nil {
				go o.cb.Control()
			}
		}
		return 0
	case wmHotKey:
		if o.suspended.Load() {
			return 0
		}
		switch int(wparam) {
		case 1:
			go o.captureAndAnalyze()
		case 2:
			o.mu.Lock()
			o.ghost = !o.ghost
			// Ghost hotkey is transient until explicitly saved in Studio.
			o.mu.Unlock()
			o.applyNative()
		case 3:
			if o.cb.Cancel != nil {
				go o.cb.Cancel()
			}
			o.Clear()
			o.Hide()
		case 4:
			if o.cb.Control != nil {
				go o.cb.Control()
			}
		case 5:
			o.toggleVisibility()
		case 6, 7, 8:
			if o.cb.Command != nil {
				name := map[int]string{6: "screen_context", 7: "screen_decide", 8: "session_pause"}[int(wparam)]
				go o.cb.Command(name)
			}
		}
		return 0
	case wmClose:
		user32.NewProc("DestroyWindow").Call(o.hwnd.Load())
		return 0
	case wmDestroy:
		o.removeTrayIcon()
		o.unregisterLifecycleNotifications()
		windowMap.Delete(o.hwnd.Load())
		o.hwnd.Store(0)
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(o.hwnd.Load(), uintptr(message), wparam, lparam)
	return r
}
func (o *winOverlay) paint() {
	var ps paintStruct
	dc, _, _ := pBeginPaint.Call(o.hwnd.Load(), uintptr(unsafe.Pointer(&ps)))
	if dc == 0 {
		return
	}
	defer pEndPaint.Call(o.hwnd.Load(), uintptr(unsafe.Pointer(&ps)))
	var rc rect
	pGetClientRect.Call(o.hwnd.Load(), uintptr(unsafe.Pointer(&rc)))
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&rc)), o.brush)
	pSetBkMode.Call(dc, transparent)
	if o.font != 0 {
		pSelectObject.Call(dc, o.font)
	}
	o.mu.RLock()
	text, meta := o.text, o.meta
	o.mu.RUnlock()

	// The top strip is deliberately distinct: in edit mode it is the drag handle.
	header := rc
	header.Bottom = min32(header.Bottom, header.Top+overlayHeaderHeight)
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&header)), o.headerBrush)

	title := header
	title.Left += 12
	title.Right -= overlaySettingsWidth + 18
	pSetTextColor.Call(dc, 0x00B9B1A7)
	drawText(dc, "AI OVERLAY", &title, dtLeft|dtVCenter|dtSingleLine|dtNoPrefix)

	button := settingsRect(rc)
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&button)), o.buttonBrush)
	pSetTextColor.Call(dc, 0x00DDD8D2)
	drawText(dc, "⚙", &button, dtCenter|dtVCenter|dtSingleLine|dtNoPrefix)

	// Empty means empty: no hotkey hints or placeholder copy in the answer surface.
	if strings.TrimSpace(text) != "" {
		body := rc
		body.Left += 14
		body.Top = header.Bottom + 10
		body.Right -= 14
		body.Bottom -= 34
		pSetTextColor.Call(dc, 0x00F7F4F0)
		drawText(dc, text, &body, dtLeft|dtWordBreak|dtNoPrefix)
	}
	if strings.TrimSpace(meta) != "" {
		footer := rc
		footer.Left += 14
		footer.Right -= 12
		footer.Top = footer.Bottom - 28
		footer.Bottom -= 7
		pSetTextColor.Call(dc, 0x009A8F82)
		drawText(dc, meta, &footer, dtRight|dtEndEllipsis|dtSingleLine|dtNoPrefix)
	}
}

func drawText(dc uintptr, s string, r *rect, flags uintptr) {
	if s == "" {
		return
	}
	u, err := syscall.UTF16FromString(displayText(s))
	if err != nil || len(u) == 0 {
		return
	}
	pDrawText.Call(dc, uintptr(unsafe.Pointer(&u[0])), wordFromInt32(-1), uintptr(unsafe.Pointer(r)), flags)
}
func (o *winOverlay) recreateGDI() {
	if o.brush != 0 {
		pDeleteObject.Call(o.brush)
	}
	if o.headerBrush != 0 {
		pDeleteObject.Call(o.headerBrush)
	}
	if o.buttonBrush != 0 {
		pDeleteObject.Call(o.buttonBrush)
	}
	o.brush, _, _ = pCreateSolidBrush.Call(0x00191512)
	o.headerBrush, _, _ = pCreateSolidBrush.Call(0x00221F1C)
	o.buttonBrush, _, _ = pCreateSolidBrush.Call(0x00302C28)
	if o.font != 0 {
		pDeleteObject.Call(o.font)
	}
	o.mu.RLock()
	fontSize := o.cfg.FontSize
	o.mu.RUnlock()
	height := -int32(fontSize * 96 / 72)
	face, _ := syscall.UTF16PtrFromString("Segoe UI")
	o.font, _, _ = pCreateFont.Call(wordFromInt32(height), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
}
func (o *winOverlay) applyNative() {
	if o.hwnd.Load() == 0 {
		return
	}
	o.mu.RLock()
	cfg, ghost := o.cfg, o.ghost
	o.mu.RUnlock()
	ex, _, _ := pGetWindowLongPtr.Call(o.hwnd.Load(), wordFromInt32(gwlExStyle))
	ex |= wsExTopmost | wsExToolWindow | wsExLayered | wsExNoActivate
	if ghost {
		ex |= wsExTransparent
	} else {
		ex &^= wsExTransparent
	}
	pSetWindowLongPtr.Call(o.hwnd.Load(), wordFromInt32(gwlExStyle), ex)
	pSetWindowPos.Call(o.hwnd.Load(), uintptr(^uintptr(0)), 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpFrameChanged)
	alpha := byte(cfg.Opacity * 255)
	if alpha < 38 {
		alpha = 38
	}
	pSetLayeredWindowAttributes.Call(o.hwnd.Load(), 0, uintptr(alpha), lwaAlpha)
	if cfg.Stealth {
		o.applyCaptureExclusion(true)
	} else {
		o.applyCaptureExclusion(false)
	}
}
func (o *winOverlay) applyCaptureExclusion(on bool) {
	status := ApplyWindowProtection(o.hwnd.Load(), on)
	o.mu.Lock()
	o.protection = status
	o.mu.Unlock()
}

func (o *winOverlay) hitTest() uintptr {
	var p point
	var r rect
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	pGetWindowRect.Call(o.hwnd.Load(), uintptr(unsafe.Pointer(&r)))
	b := int32(overlayEdgeHit)
	left, right, top, bottom := p.X-r.Left < b, r.Right-p.X <= b, p.Y-r.Top < b, r.Bottom-p.Y <= b
	switch {
	case top && left:
		return htTopLeft
	case top && right:
		return htTopRight
	case bottom && left:
		return htBottomLeft
	case bottom && right:
		return htBottomRight
	case left:
		return htLeft
	case right:
		return htRight
	case top:
		return htTop
	case bottom:
		return htBottom
	case o.settingsHit(p.X-r.Left, p.Y-r.Top):
		return htClient
	case p.Y-r.Top < overlayHeaderHeight:
		return htCaption
	default:
		return htClient
	}
}
func (o *winOverlay) registerHotkeys() error {
	o.mu.RLock()
	bindings := copyBindings(o.cfg.Hotkeys)
	o.mu.RUnlock()
	return o.replaceHotkeys(bindings)
}
func parseHotkey(s string) (uint32, uint32, error) {
	b, err := hotkey.Parse(s)
	return b.Modifiers, b.Key, err
}

func (o *winOverlay) captureAndAnalyze() {
	if o.cb.Request != nil {
		o.mu.Lock()
		o.stationaryFired = true
		o.mu.Unlock()
		if err := o.cb.Request(); err != nil {
			o.SetMeta(err.Error())
		}
		return
	}

	if !o.capturing.CompareAndSwap(false, true) {
		return
	}
	defer o.capturing.Store(false)
	o.SetText("Захватываю экран…")
	o.SetMeta("CAPTURE")
	o.Show()
	o.mu.Lock()
	o.stationaryFired = true
	o.mu.Unlock()
	capture := o.cb.Capture
	if capture == nil {
		capture = captureScreenJPEG
	}
	img, err := capture()
	if err != nil {
		o.SetText("CAPTURE ERROR\n" + err.Error())
		o.Show()
		return
	}
	if o.cb.Analyze != nil {
		o.SetText("Анализирую экран…")
		o.SetMeta("TRYING")
		o.cb.Analyze(img)
	}
}
func (o *winOverlay) addTrayIcon() {
	h := o.hwnd.Load()
	if h == 0 {
		return
	}
	inst, _, _ := pGetModuleHandle.Call(0)
	name, _ := syscall.UTF16PtrFromString("APP")
	icon, _, _ := user32.NewProc("LoadImageW").Call(inst, uintptr(unsafe.Pointer(name)), 1, 16, 16, 0x8000)
	var n notifyIconData
	n.Size = uint32(unsafe.Sizeof(n))
	n.Hwnd = h
	n.ID = 1
	n.Flags = nifMessage | nifTip
	n.CallbackMessage = wmAppTray
	if icon != 0 {
		n.Icon = icon
		n.Flags |= nifIcon
	}
	copy(n.Tip[:], syscall.StringToUTF16("AIOverlay — открыть Studio"))
	pShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&n)))
}
func (o *winOverlay) removeTrayIcon() {
	h := o.hwnd.Load()
	if h == 0 {
		return
	}
	n := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Hwnd: h, ID: 1}
	pShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&n)))
}
func (o *winOverlay) handleTrayMessage(message uint32) {
	switch message {
	case wmLButtonDblClk:
		if o.cb.Control != nil {
			go o.cb.Control()
		}
	case wmRButtonUp, wmContextMenu:
		o.showTrayMenu()
	}
}
func (o *winOverlay) showTrayMenu() {
	create := user32.NewProc("CreatePopupMenu")
	appendMenu := user32.NewProc("AppendMenuW")
	track := user32.NewProc("TrackPopupMenu")
	destroy := user32.NewProc("DestroyMenu")
	foreground := user32.NewProc("SetForegroundWindow")
	menu, _, _ := create.Call()
	if menu == 0 {
		return
	}
	defer destroy.Call(menu)
	add := func(id uintptr, label string) {
		p, _ := syscall.UTF16PtrFromString(label)
		appendMenu.Call(menu, mfString, id, uintptr(unsafe.Pointer(p)))
	}
	add(trayOpenStudio, "Открыть Studio")
	add(trayToggleOverlay, "Показать / скрыть overlay")
	add(trayPauseBackground, "Пауза / продолжить фон")
	add(trayQuit, "Выход")
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	foreground.Call(o.hwnd.Load())
	cmd, _, _ := track.Call(menu, tpmRightButton|tpmReturnCmd, uintptr(pt.X), uintptr(pt.Y), 0, o.hwnd.Load(), 0)
	switch cmd {
	case trayOpenStudio:
		if o.cb.Control != nil {
			go o.cb.Control()
		}
	case trayToggleOverlay:
		o.toggleVisibility()
	case trayPauseBackground:
		if o.cb.Command != nil {
			go o.cb.Command("session_pause")
		}
	case trayQuit:
		if o.cb.Quit != nil {
			go o.cb.Quit()
		}
	}
}

func settingsRect(client rect) rect {
	r := client
	r.Left = max32(client.Left+8, client.Right-overlaySettingsWidth-8)
	r.Right = client.Right - 8
	r.Top = client.Top + 5
	r.Bottom = min32(client.Bottom, client.Top+overlayHeaderHeight-5)
	return r
}
func (o *winOverlay) settingsHit(x, y int32) bool {
	var client rect
	pGetClientRect.Call(o.hwnd.Load(), uintptr(unsafe.Pointer(&client)))
	r := settingsRect(client)
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}
func clientPointFromLParam(v uintptr) (int32, int32) {
	return int32(int16(uint16(v))), int32(int16(uint16(v >> 16)))
}
func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func wordFromInt32(v int32) uintptr { return uintptr(int64(v)) }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (o *winOverlay) guardLoop(ctx context.Context) {
	privacy := time.NewTicker(2500 * time.Millisecond)
	stationary := time.NewTicker(250 * time.Millisecond)
	defer privacy.Stop()
	defer stationary.Stop()
	for {
		select {
		case <-privacy.C:
			o.mu.RLock()
			stealth := o.cfg.Stealth
			o.mu.RUnlock()
			if stealth {
				o.post(0x8002)
			}
		case <-stationary.C:
			if o.suspended.Load() && time.Now().UnixNano() > o.suspendUntil.Load() {
				o.SetHotkeyEditor(false)
			}
			o.post(0x8003)
		case <-ctx.Done():
			return
		}
	}
}
func (o *winOverlay) checkStationary() {
	o.mu.Lock()
	ms := o.cfg.StationaryMS
	o.mu.Unlock()
	if ms <= 0 {
		return
	}
	var p point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	o.mu.Lock()
	if p != o.stationaryPos {
		o.stationaryPos = p
		o.stationarySince = time.Now()
		o.stationaryFired = false
		o.mu.Unlock()
		return
	}
	if !o.stationaryFired && !o.stationarySince.IsZero() && time.Since(o.stationarySince) >= time.Duration(ms)*time.Millisecond {
		o.stationaryFired = true
		o.mu.Unlock()
		go o.captureAndAnalyze()
		return
	}
	o.mu.Unlock()
}
func (o *winOverlay) Apply(cfg domain.OverlayConfig) error {
	if err := hotkey.Validate(cfg.Hotkeys); err != nil {
		return err
	}
	if o.hwnd.Load() == 0 {
		o.mu.Lock()
		o.cfg = cfg
		o.configGhost = cfg.ClickThrough
		o.ghost = cfg.ClickThrough
		o.mu.Unlock()
		return nil
	}
	return o.onUI(func() error { return o.applySettings(cfg) })
}

func (o *winOverlay) post(message uint32) {
	if h := o.hwnd.Load(); h != 0 {
		user32.NewProc("PostMessageW").Call(h, uintptr(message), 0, 0)
	}
}
func (o *winOverlay) SetText(s string) {
	o.mu.Lock()
	o.text = s
	o.mu.Unlock()
	if o.hwnd.Load() != 0 {
		pInvalidateRect.Call(o.hwnd.Load(), 0, 1)
	}
}
func (o *winOverlay) SetMeta(s string) {
	o.mu.Lock()
	o.meta = s
	o.mu.Unlock()
	if o.hwnd.Load() != 0 {
		pInvalidateRect.Call(o.hwnd.Load(), 0, 1)
	}
}
func (o *winOverlay) Show() { o.post(0x8004) }
func (o *winOverlay) Hide() { o.post(0x8005) }
func (o *winOverlay) Clear() {
	o.SetText("")
	o.SetMeta("")
}
func (o *winOverlay) Close() error { o.post(wmClose); return nil }
func (o *winOverlay) destroyResources() {
	if o.brush != 0 {
		pDeleteObject.Call(o.brush)
		o.brush = 0
	}
	if o.headerBrush != 0 {
		pDeleteObject.Call(o.headerBrush)
		o.headerBrush = 0
	}
	if o.buttonBrush != 0 {
		pDeleteObject.Call(o.buttonBrush)
		o.buttonBrush = 0
	}
	if o.font != 0 {
		pDeleteObject.Call(o.font)
		o.font = 0
	}
}
