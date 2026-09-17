package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aioverlay/internal/domain"
)

func TestDefaultRoundTripPortableJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	s := NewStore(path)
	want := Default()
	want.Providers[1].CredentialPool.Keys = append(want.Providers[1].CredentialPool.Keys, credential("k1", "sk-test"))
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ActivePipelineID != want.ActivePipelineID {
		t.Fatalf("active pipeline mismatch")
	}
	if got.Providers[1].CredentialPool.Keys[0].Secret != "sk-test" {
		t.Fatalf("portable secret not preserved")
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte(`"secret": "sk-test"`)) {
		t.Fatalf("expected explicit portable secret in JSON")
	}
}

func TestRejectUnknownJSONFields(t *testing.T) {
	_, err := Decode(strings.NewReader(`{"version":1,"unknown":1}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRejectUnknownProviderInPool(t *testing.T) {
	cfg := Default()
	cfg.Pipelines[0].Nodes[1].ModelPool.Models[0].ProviderID = "missing"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

func credential(id, secret string) domain.Credential {
	return domain.Credential{ID: id, Label: id, Secret: secret, Enabled: true, Priority: 100}
}

func TestDefaultDoesNotPreloadStudio(t *testing.T) {
	if Default().ControlCenter.AutoOpen {
		t.Fatal("Studio should stay unloaded until explicitly opened")
	}
}
