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
	for _, token := range []string{"initializeSTA()", "CoInitializeEx", "CoUninitialize", "dispatchSession(ctx, v", "Hidden: true", "v.Destroy()"} {
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
