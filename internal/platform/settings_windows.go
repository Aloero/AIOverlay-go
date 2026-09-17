//go:build windows

package platform

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/hotkey"
	"fmt"
	"reflect"
	"syscall"
	"time"
	"unsafe"
)

func (o *winOverlay) onUI(fn func() error) error {
	if o.hwnd.Load() == 0 {
		return fmt.Errorf("overlay window is not available")
	}
	result := make(chan error, 1)
	select {
	case o.uiCalls <- func() { result <- fn() }:
	case <-o.done:
		return fmt.Errorf("overlay is closed")
	}
	o.post(0x8006)
	select {
	case err := <-result:
		return err
	case <-o.done:
		return fmt.Errorf("overlay closed before applying settings")
	}
}
func (o *winOverlay) replaceHotkeys(values map[string]string) error {
	register := func(i int, b hotkey.Binding) error {
		ok, _, e := pRegisterHotKey.Call(o.hwnd.Load(), uintptr(i), uintptr(b.Modifiers|modNoRepeat), uintptr(b.Key))
		if ok == 0 {
			return e
		}
		return nil
	}
	unregister := func(i int) { pUnregisterHotKey.Call(o.hwnd.Load(), uintptr(i)) }
	err := hotkey.Replace(o.registered, values, register, unregister)
	o.mu.Lock()
	if err != nil {
		o.hotkeyError = err.Error()
	} else {
		o.hotkeyError = ""
	}
	o.mu.Unlock()
	if err == nil {
		o.registered = copyBindings(values)
	}
	return err
}
func copyBindings(src map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range src {
		out[k] = v
	}
	return out
}
func (o *winOverlay) applySettings(cfg domain.OverlayConfig) error {
	if !reflect.DeepEqual(o.registered, cfg.Hotkeys) || o.suspended.Load() {
		if err := o.replaceHotkeys(cfg.Hotkeys); err != nil {
			return err
		}
		if o.suspended.Load() {
			for i := range hotkey.Actions {
				pUnregisterHotKey.Call(o.hwnd.Load(), uintptr(i+1))
			}
			o.registered = nil
		}
	}
	o.mu.Lock()
	// A transient Ghost hotkey is not undone by an unrelated font/layout save.
	if cfg.ClickThrough != o.configGhost {
		o.ghost = cfg.ClickThrough
	}
	o.configGhost = cfg.ClickThrough
	old := o.cfg
	o.cfg = cfg
	o.mu.Unlock()
	if old.Width != cfg.Width || old.Height != cfg.Height {
		pSetWindowPos.Call(o.hwnd.Load(), 0, 0, 0, uintptr(cfg.Width), uintptr(cfg.Height), swpNoMove|swpNoActivate|0x4)
	}
	o.recreateGDI()
	o.applyNative()
	pInvalidateRect.Call(o.hwnd.Load(), 0, 1)
	return nil
}
func (o *winOverlay) SetHotkeyEditor(active bool) {
	o.suspended.Store(active)
	if active {
		o.suspendUntil.Store(time.Now().Add(20 * time.Second).UnixNano())
	}
	_ = o.onUI(func() error {
		if active {
			for i := range hotkey.Actions {
				pUnregisterHotKey.Call(o.hwnd.Load(), uintptr(i+1))
			}
			o.registered = nil
			return nil
		}
		o.mu.RLock()
		values := copyBindings(o.cfg.Hotkeys)
		o.mu.RUnlock()
		return o.replaceHotkeys(values)
	})
}
func (o *winOverlay) Diagnostics() any {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return map[string]any{"protection": o.protection, "hotkey_error": o.hotkeyError, "ghost": o.ghost, "ghost_transient": o.ghost != o.configGhost, "hotkey_editor": o.suspended.Load(), "user_hidden": o.visibility.hiddenByUser(), "visibility_suppressed": o.visibility.suppressed()}
}
func (o *winOverlay) saveGeometry() {
	var r rect
	if ok, _, _ := pGetWindowRect.Call(o.hwnd.Load(), uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	o.mu.Lock()
	o.cfg.Width = int(r.Right - r.Left)
	o.cfg.Height = int(r.Bottom - r.Top)
	o.cfg.X = int(r.Left)
	o.cfg.Y = int(r.Top)
	o.cfg.Positioned = true
	cfg := o.cfg
	o.mu.Unlock()
	if o.cb.Geometry != nil {
		go o.cb.Geometry(cfg)
	}
}
func nativeSettingError(message string) {
	p, _ := syscall.UTF16PtrFromString(message)
	title, _ := syscall.UTF16PtrFromString("AIOverlay · горячие клавиши")
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(title)), 0x30)
}
