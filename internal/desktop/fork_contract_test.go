package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaintainedWebViewForkOwnsSTAAndSynchronousTeardown(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(path string) string {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	host := read("internal/desktop/window_windows.go")
	fork := read("third_party/go-webview2/webview.go")
	edge := read("third_party/go-webview2/pkg/edge/chromium.go")
	core := read("third_party/go-webview2/pkg/edge/corewebview2.go")
	for _, token := range []string{"initializeSTA()", "CoInitializeEx", "CoUninitialize", "dispatchSession(ctx, v", "Background: &studioBackground", "v.Destroy()"} {
		if !strings.Contains(host, token) {
			t.Fatal("host missing", token)
		}
	}
	if strings.Contains(core, "func init()") {
		t.Fatal("hidden import-time COM ownership returned")
	}
	for _, token := range []string{"w.browser.Close()", "delete(windowContext, w.hwnd)", "w.destroyed"} {
		if !strings.Contains(fork, token) {
			t.Fatal("missing synchronous cleanup", token)
		}
	}
	for _, token := range []string{"controller.vtbl.Close.Call", "webview.vtbl.Release.Call", "environment.vtbl.Release.Call"} {
		if !strings.Contains(edge, token) {
			t.Fatal("missing COM cleanup", token)
		}
	}
}

// The white Studio window: WebView2 never observes ShowWindow on its parent, so
// a hidden->shown (or minimized->restored) host transition that does not reach
// put_IsVisible leaves only the default background on screen.
func TestWebViewForkForwardsHostVisibility(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(path string) string {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	fork := read("third_party/go-webview2/webview.go")
	edge := read("third_party/go-webview2/pkg/edge/chromium.go")
	for _, token := range []string{"case w32.WMShowWindow:", "case wmSyncVisibility:", "w.syncVisibility()", "LpfnWndProc:   wndprocCallback"} {
		if !strings.Contains(fork, token) {
			t.Fatal("fork does not forward host visibility:", token)
		}
	}
	for _, token := range []string{"e.controller.PutIsVisible(visible)", "e.SetVisible(visible != 0 && iconic == 0)", "e.applyBackgroundColor()", "uintptr(unsafe.Pointer(&kind))"} {
		if !strings.Contains(edge, token) {
			t.Fatal("controller visibility/background contract missing:", token)
		}
	}
}
