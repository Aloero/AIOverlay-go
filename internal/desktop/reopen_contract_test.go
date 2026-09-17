package desktop

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsStudioCloseUnloadsAndCanReopenContract(t *testing.T) {
	raw, err := os.ReadFile("window_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, token := range []string{
		"installCloseToUnload",
		"wmCloseDesktop",
		"runSession",
		"v.Destroy()",
		"func (w *desktopWindow) Show()",
		"func (w *desktopWindow) Close()",
		"window.__studioNativeClose",
	} {
		if !strings.Contains(s, token) {
			t.Fatalf("missing %q", token)
		}
	}
	if strings.Contains(s, "showWindow.Call(hwnd, 0)") {
		t.Fatal("WM_CLOSE must unload Studio instead of only hiding its window")
	}
}
