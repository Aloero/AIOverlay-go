package releaseguard

import (
	"aioverlay/internal/config"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretGuardRejectsDisabledKeysAndVPNProfiles(t *testing.T) {
	for _, raw := range []string{`{"providers":[{"credential_pool":{"keys":[{"secret":"fixture-only","enabled":false}]}}]}`, `{"vpn":{"enabled":false,"config":{"type":"vless"}}}`, `{"future_auth":{"password":"fixture-only"}}`, `{"runtime_statistics":{"models":{}}}`, `{"headers":{"Authorization":"Bearer fixture-only"}}`, `{"headers":{"x-goog-api-key":"fixture-only"}}`} {
		if err := CheckConfigJSON([]byte(raw)); err == nil {
			t.Fatal("private configuration accepted")
		}
	}
	raw, _ := json.Marshal(config.Default())
	if err := CheckConfigJSON(raw); err != nil {
		t.Fatal(err)
	}
}
func TestLFSPointerNeverBecomesReleaseRuntime(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "runtime"), 0755)
	os.WriteFile(filepath.Join(root, "runtime", "manifest.json"), []byte(`{"files":[{"path":"runtime/child.exe","sha256":"x","license":"runtime/LICENSE"}]}`), 0644)
	os.WriteFile(filepath.Join(root, "runtime", "child.exe"), []byte("version https://git-lfs.github.com/spec/v1\noid sha256:123\nsize 123\n"), 0644)
	if err := Runtimes(root); err == nil {
		t.Fatal("LFS pointer accepted")
	}
}
