package platform

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsOverlayRecoveryAndIdentityContract(t *testing.T) {
	overlay, err := os.ReadFile("overlay_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := os.ReadFile("identity_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	common, err := os.ReadFile("overlay.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(overlay) + string(common)
	for _, token := range []string{"AI OVERLAY", "addTrayIcon", "Открыть Studio", "trayToggleOverlay", "settingsRect", `drawText(dc, "⚙"`} {
		if !strings.Contains(s, token) {
			t.Fatalf("overlay missing %q", token)
		}
	}
	if !strings.Contains(string(identity), "SetCurrentProcessExplicitAppUserModelID") || !strings.Contains(string(identity), "--app-user-model-id=") {
		t.Fatal("Windows application grouping identity is incomplete")
	}
}

func TestWindowsCaptureDoesNotCallGetDIBits(t *testing.T) {
	raw, err := os.ReadFile("capture_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "GetDIBits") {
		t.Fatal("legacy GetDIBits path returned")
	}
	for _, token := range []string{"CreateDIBSection", "GdiFlush", "BitBlt"} {
		if !strings.Contains(s, token) {
			t.Fatalf("capture missing %s", token)
		}
	}
}
