package platform

import "sync"

// visibilityState separates an explicit user latch from transient suppression.
// Publication may update content while userHidden is true, but it may not reveal
// the HWND until the user toggles it again.
type visibilityState struct {
	mu          sync.Mutex
	userHidden  bool
	suppressors int
}

func (v *visibilityState) toggleUser() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.userHidden = !v.userHidden
	return !v.userHidden && v.suppressors == 0
}
func (v *visibilityState) mayShow() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return !v.userHidden && v.suppressors == 0
}
func (v *visibilityState) hiddenByUser() bool { v.mu.Lock(); defer v.mu.Unlock(); return v.userHidden }
func (v *visibilityState) suppress() func() {
	v.mu.Lock()
	v.suppressors++
	v.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			v.mu.Lock()
			if v.suppressors > 0 {
				v.suppressors--
			}
			v.mu.Unlock()
		})
	}
}
func (v *visibilityState) suppressed() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.suppressors > 0
}
