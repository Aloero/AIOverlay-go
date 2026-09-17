package config

import (
	"aioverlay/internal/domain"
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMigrateVersionOneAndPreserveNestedExtensions(t *testing.T) {
	c := Default()
	c.Version = 1
	c.Providers[0].Extra = map[string]json.RawMessage{"future_auth": json.RawMessage(`{"precision":123456789012345678901234567890}`)}
	c.Pipelines[0].Nodes[1].ModelPool.Models[0].Extra = map[string]json.RawMessage{"future_cost": json.RawMessage(`"preserve"`)}
	raw, _ := json.Marshal(c)
	m, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != domain.ConfigVersion {
		t.Fatal("not migrated")
	}
	store := NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err = store.Save(m); err != nil {
		t.Fatal(err)
	}
	m, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(m.Providers[0].Extra["future_auth"], []byte("123456789012345678901234567890")) || len(m.Pipelines[0].Nodes[1].ModelPool.Models[0].Extra) == 0 {
		t.Fatal("unknown fields lost")
	}
}
func TestDuplicateAndFutureConfigRejectedWithoutOverwrite(t *testing.T) {
	c := Default()
	c.Version = 999
	raw, _ := json.Marshal(c)
	if _, err := Decode(bytes.NewReader(raw)); err == nil {
		t.Fatal("future schema accepted")
	}
	for _, raw := range []string{`{"version":1,"version":2}`, `{"x":{"a":1,"a":2}}`, `{"x":1} {"y":2}`} {
		if err := CheckJSON([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
}
func TestCredentialEnvironmentCannotOverrideProxyIsolation(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "NO_PROXY", "OPENCODE_CONFIG_CONTENT", "HOME", "NODE_OPTIONS"} {
		c := Default()
		c.Providers[0].CredentialEnv = []string{name}
		if Validate(c) == nil {
			t.Fatal(name)
		}
	}
}
