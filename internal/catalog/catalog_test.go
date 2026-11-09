package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
)

func TestBundledSnapshotLargeAndVision(t *testing.T) {
	path := filepath.Join("..", "..", "runtime", "catalog", "models-dev-api.json")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	models := s.Models()
	if len(models) < 5000 {
		t.Fatalf("expected large catalog, got %d", len(models))
	}
	vision := 0
	free := 0
	for _, m := range models {
		if m.HasImageInput() {
			vision++
		}
		if m.Free {
			free++
		}
	}
	if vision < 1000 {
		t.Fatalf("expected many vision models, got %d", vision)
	}
	if free < 100 {
		t.Fatalf("expected free models, got %d", free)
	}
}

func TestAnnotatedReadyFromPortableConfig(t *testing.T) {
	path := filepath.Join("..", "..", "runtime", "catalog", "models-dev-api.json")
	s, _ := New(path)
	cfg := config.Default()
	cfg.Providers[1].CredentialPool.Keys = append(cfg.Providers[1].CredentialPool.Keys, credential("x", "secret"))
	models := s.Annotated(cfg)
	found := false
	for _, m := range models {
		if m.ProviderID == "openrouter" && m.Ready {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected openrouter models ready")
	}
}

func credential(id, secret string) domain.Credential {
	return domain.Credential{ID: id, Label: id, Secret: secret, Enabled: true, Priority: 100}
}

func TestOpenCodeFreeReadyUsesBundledRuntimeList(t *testing.T) {
	runtime := filepath.Join(t.TempDir(), "opencode-fixture.exe")
	if err := os.WriteFile(runtime, []byte("fixture runtime payload"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIOVERLAY_OPENCODE_BIN", runtime)
	path := filepath.Join("..", "..", "runtime", "catalog", "models-dev-api.json")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	models := s.Annotated(config.Default())
	var mimoReady bool
	for _, m := range models {
		ref := m.ProviderID + "/" + m.ModelID
		if ref == "opencode/mimo-v2.5-free" {
			mimoReady = m.Ready
		}
		if ref == "opencode/deepseek-v4-flash-free" && m.Ready {
			t.Fatal("catalog-only DeepSeek must not be READY")
		}
	}
	if !mimoReady {
		t.Fatal("bundled MiMo should be READY")
	}
}

func TestCatalogOrderIsDeterministicForDuplicateNames(t *testing.T) {
	raw := `{"p":{"name":"Same","models":{"b":{"name":"Dup"},"a":{"name":"Dup"},"c":{"name":"Dup"}}},
	"q":{"name":"Same","models":{"a":{"name":"Dup"}}}}`
	want := []string{"p/a", "p/b", "p/c", "q/a"}
	for i := 0; i < 50; i++ {
		models, err := parse(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		for j, m := range models {
			if got := m.ProviderID + "/" + m.ModelID; got != want[j] {
				t.Fatalf("run %d: position %d = %s, want %s", i, j, got, want[j])
			}
		}
	}
}
