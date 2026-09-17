package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelineStudioContract(t *testing.T) {
	root := filepath.Join("..", "..", "web", "static")
	app, err := os.ReadFile(filepath.Join(root, "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(root, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := os.ReadFile(filepath.Join(root, "application-settings.js"))
	s := string(app) + string(index) + string(settings)
	for _, token := range []string{"studioDispatch", "__studioReply", "/api/job", "/api/capture", "vision_preprocessor", "first_token_timeout_ms", "max_attempts", "quality_score", "renderEdgeInspector", "renderProviders", "renderSettings", "renderRuns", "Экспорт JSON", "Проверить захват", "TEXT ONLY", "IMAGE", "FREE", "AI VPN", "vpnEnabled", "vpnConfig", "/api/vpn/check"} {
		if !strings.Contains(s, token) {
			t.Errorf("Studio missing contract %q", token)
		}
	}
	for _, token := range []string{"buildExe", "buildProduct", "buildCompany", "buildIcon"} {
		if strings.Contains(s, token) {
			t.Errorf("runtime Studio must not expose pre-build field %q", token)
		}
	}
	if strings.Contains(s, "https://cdn.") {
		t.Error("remote frontend dependency")
	}
}
