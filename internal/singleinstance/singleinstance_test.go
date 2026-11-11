package singleinstance

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsGuardUsesKernelMutex(t *testing.T) {
	raw, err := os.ReadFile("singleinstance_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, token := range []string{"CreateMutexW", "errAlreadyExists", `Local\\AIOverlay-go.single-instance`} {
		if !strings.Contains(s, token) {
			t.Fatalf("windows guard missing %q", token)
		}
	}
	// The kernel object must outlive Ensure(); closing the handle would free
	// the lock while the first instance is still running.
	if strings.Contains(s, "CloseHandle") {
		t.Fatal("mutex handle must be released only by process exit")
	}
	// A failing API must never block an otherwise valid start-up.
	if strings.Contains(s, "return false") {
		t.Fatal("only an existing mutex holder may reject the launch")
	}
}
