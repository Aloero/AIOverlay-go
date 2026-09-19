package releaseguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsLifecycleSafetyPauseIsWiredWithoutAutoResume(t *testing.T) {
	overlay, err := os.ReadFile(filepath.Join("..", "platform", "overlay_windows.go"))
	if err != nil {
		t.Fatal(err)
	}
	core, err := os.ReadFile(filepath.Join("..", "platform", "lifecycle.go"))
	if err != nil {
		t.Fatal(err)
	}
	main, err := os.ReadFile(filepath.Join("..", "..", "cmd", "aioverlay", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"overlay_windows.go": string(overlay), "lifecycle.go": string(core), "main.go": string(main)} {
		for _, forbidden := range []string{"PauseBackground(false) // auto", "SuspendBackground(\"windows_resume\")", "SuspendBackground(\"windows_session_unlock\")"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains automatic lifecycle resume token %q", name, forbidden)
			}
		}
	}
	for _, required := range []string{"WTSRegisterSessionNotification", "wmPowerBroadcast", "wmWTSSessionChange", "SuspendBackground"} {
		if !strings.Contains(string(overlay)+string(main), required) {
			t.Fatalf("Windows lifecycle wiring missing %q", required)
		}
	}
	if !strings.Contains(string(core), "windows_suspend") || !strings.Contains(string(core), "windows_session_lock") {
		t.Fatal("lifecycle policy reasons are missing")
	}
}
