package releasepack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceArchiveExcludesPrivateEnvironmentAndOverrides(t *testing.T) {
	root := t.TempDir()
	private := []string{
		".env", ".env.production", "nested/.env", "nested/.env.development",
		"settings.local.json", "nested/settings.local.yaml", "nested/settings.local.yml",
	}
	public := []string{"README.md", "examples/config.example.json", "internal/app/app.go"}
	for _, name := range append(append([]string{}, private...), public...) {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, _, err := Source(root, false)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, file := range files {
		names[file.Name] = true
	}
	for _, name := range private {
		if names[name] {
			t.Errorf("private file included in source archive: %s", name)
		}
	}
	for _, name := range public {
		if !names[name] {
			t.Errorf("public source file missing: %s", name)
		}
	}
}
