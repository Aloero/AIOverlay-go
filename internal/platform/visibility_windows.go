//go:build windows

package platform

// Runs on the overlay window thread. The user latch is independent from the
// current HWND visibility: transient capture/Clear hiding cannot accidentally
// turn an explicit user hide into a Show on the next callback.
func (o *winOverlay) toggleVisibility() {
	show := o.visibility.toggleUser()
	if !show {
		pShowWindow.Call(o.hwnd.Load(), swHide)
		return
	}
	o.applyNative()
	pShowWindow.Call(o.hwnd.Load(), swShowNoActivate)
}
func (o *winOverlay) ToggleVisibility() { o.post(0x8007) }
