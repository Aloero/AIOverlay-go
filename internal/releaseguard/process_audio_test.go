package releaseguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessLoopbackNativePathCannotSilentlyDowngradeToSystemMix(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "platform", "audio", "process_windows.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{
		`VAD\Process_Loopback`,
		"processLoopbackActivation",
		"processLoopbackIncludeTree",
		"ActivateAudioInterfaceAsync",
		"sameProcessIdentity",
		"ProcessCaptureChanged",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("process-loopback native boundary missing %q", required)
		}
	}
	for _, forbidden := range []string{"processLoopbackExcludeTree", "enumerator()", "default endpoint"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("process-only capture contains unsafe fallback token %q", forbidden)
		}
	}
}
