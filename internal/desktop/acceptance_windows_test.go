//go:build windows

package desktop

import (
	"context"
	"github.com/jchv/go-webview2/webviewloader"
	"net/http"
	"os"
	"testing"
	"time"
)

// This is a real desktop acceptance test, deliberately opt-in. Cross-compilation
// and Linux source-contract tests cannot substitute for executing it on Windows.
func TestWindowsStudioRepeatedOpenCloseReleasesHWND(t *testing.T) {
	if os.Getenv("AIOVERLAY_WINDOWS_ACCEPTANCE") != "1" {
		t.Skip("set AIOVERLAY_WINDOWS_ACCEPTANCE=1 on an interactive Windows desktop")
	}
	if _, err := webviewloader.GetInstalledVersion(); err != nil {
		t.Fatal(err)
	}
	w := New(Options{Title: "AIOverlay lifecycle acceptance", Stealth: true, Handler: http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		out.Header().Set("Content-Type", "application/json")
		out.Write([]byte(`{}`))
	})}).(*desktopWindow)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Studio manager did not stop")
		}
	}()
	wait := func(fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !fn() {
			if time.Now().After(deadline) {
				t.Fatal("Studio lifecycle transition timed out")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	for i := 0; i < 10; i++ {
		w.Show()
		var hwnd uintptr
		wait(func() bool {
			w.mu.Lock()
			defer w.mu.Unlock()
			if w.view != nil {
				hwnd = uintptr(w.view.Window())
				return hwnd != 0
			}
			return false
		})
		w.Close()
		wait(func() bool { w.mu.Lock(); nilView := w.view == nil; w.mu.Unlock(); return nilView })
		alive, _, _ := user32.NewProc("IsWindow").Call(hwnd)
		if alive != 0 {
			t.Fatalf("iteration %d: old HWND survived teardown", i)
		}
		// Waiting for profile cleanup is a separate boundary from HWND destruction.
		time.Sleep(120 * time.Millisecond)
	}
}
