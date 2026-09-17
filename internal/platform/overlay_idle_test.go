package platform

import (
	"aioverlay/internal/domain"
	"testing"
)

func TestOverlayIdleStateHasNoInstructionalCopy(t *testing.T) {
	cfg := domain.OverlayConfig{Hotkeys: map[string]string{"analyze": "Alt+S", "control": "Alt+O"}}
	if got := overlayIdleText(cfg); got != "" {
		t.Fatalf("idle body must be blank, got %q", got)
	}
	if got := overlayIdleMeta(cfg); got != "" {
		t.Fatalf("idle footer must be blank, got %q", got)
	}
}
