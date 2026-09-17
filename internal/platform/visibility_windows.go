//go:build windows

package platform

// Runs on the overlay window thread. Hiding never clears content, moves the HWND
// or cancels inference. Automatic Show calls cannot undo an explicit user hide.
func (o *winOverlay) toggleVisibility() {
	visible, _, _ := user32.NewProc("IsWindowVisible").Call(o.hwnd.Load())
	o.userHidden = visible != 0
	if o.userHidden {
		pShowWindow.Call(o.hwnd.Load(), swHide)
		return
	}
	o.applyNative()
	pShowWindow.Call(o.hwnd.Load(), swShowNoActivate)
}
func (o *winOverlay) ToggleVisibility() { o.post(0x8007) }
