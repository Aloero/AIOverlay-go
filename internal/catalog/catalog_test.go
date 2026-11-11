package catalog

import (
	"encoding/json"
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
	// The allow-list is refreshed with scripts/refresh-catalog.py, so pick the fixtures out of the
	// shipped files instead of naming models that upstream retires over time.
	enabled := struct {
		Models []struct {
			Ref  string `json:"ref"`
			Name string `json:"name"`
		} `json:"models"`
	}{}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "opencode-enabled.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &enabled); err != nil {
		t.Fatal(err)
	}
	if len(enabled.Models) == 0 {
		t.Fatal("the bundled OpenCode allow-list is empty")
	}
	listed := map[string]bool{}
	for _, row := range enabled.Models {
		listed[row.Ref] = true
	}
	servedRef := enabled.Models[0].Ref
	catalogOnlyRef := ""
	for ref := range s.byRef {
		if strings.HasPrefix(ref, "opencode/") && !listed[ref] {
			catalogOnlyRef = ref
			break
		}
	}
	if catalogOnlyRef == "" {
		t.Fatal("need at least one catalog-only OpenCode model to prove the gate")
	}
	var servedReady *bool
	for _, m := range s.Annotated(config.Default()) {
		ref := m.ProviderID + "/" + m.ModelID
		if ref == servedRef {
			value := m.Ready
			servedReady = &value
		}
		if ref == catalogOnlyRef && m.Ready {
			t.Fatalf("%s is catalog-only and must not be READY", catalogOnlyRef)
		}
	}
	if servedReady == nil {
		t.Fatalf("%s is in the bundled allow-list but was not annotated", servedRef)
	}
	if !*servedReady {
		t.Fatalf("%s is in the bundled allow-list and must be READY", servedRef)
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
