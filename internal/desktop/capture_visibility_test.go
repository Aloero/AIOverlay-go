package desktop

import "testing"

func TestCaptureDoesNotRestoreHiddenOrMinimizedStudio(t *testing.T) {
	for _, c := range []struct {
		name                     string
		visible, minimized, want bool
	}{{"visible", true, false, true}, {"hidden", false, false, false}, {"minimized", true, true, false}, {"hidden-minimized", false, true, false}} {
		t.Run(c.name, func(t *testing.T) {
			if got := hideForCapture(c.visible, c.minimized); got != c.want {
				t.Fatalf("hide=%t want %t", got, c.want)
			}
		})
	}
}
