package platform

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsOverlayHasDragHeaderAndStudioButton(t *testing.T) {
	raw, err := os.ReadFile("overlay_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, token := range []string{
		"overlayHeaderHeight", "settingsRect", "settingsHit", "htCaption",
		"wmLButtonUp", "go o.cb.Control()", `drawText(dc, "⚙"`,
		"headerBrush", "buttonBrush",
	} {
		if !strings.Contains(s, token) {
			t.Fatalf("overlay chrome missing %q", token)
		}
	}
	for _, forbidden := range []string{"Ответ ИИ появится здесь", "анализ экрана\\n\\n", "— Studio"} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("overlay still contains removed hint %q", forbidden)
		}
	}
}
