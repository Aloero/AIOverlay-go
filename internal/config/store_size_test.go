package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRejectsOversizedConfigWithoutReplacingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store := NewStore(path)
	if err := store.Save(Default()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oversized := Default()
	oversized.Pipelines[0].Name = strings.Repeat("x", 8<<20)
	if err := store.Save(oversized); err == nil || !strings.Contains(err.Error(), "exceeds 8 MiB") {
		t.Fatalf("oversized save must fail before replacement: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("oversized save replaced the existing configuration")
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("previous configuration is no longer readable: %v", err)
	}
}

func TestStoreImportRejectsIndentationGrowthWithoutReplacingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store := NewStore(path)
	if err := store.Save(Default()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.Pipelines[0].Name = ""
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The compact input is valid and below the read limit, but formatting it
	// for persistence would make the next application startup reject the file.
	cfg.Pipelines[0].Name = strings.Repeat("x", (8<<20)-len(raw)-1)
	raw, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("compact fixture must fit the read limit: %v", err)
	}
	if _, err := store.Import(bytes.NewReader(raw)); err == nil || !strings.Contains(err.Error(), "exceeds 8 MiB") {
		t.Fatalf("import must reject oversized persisted JSON: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed import replaced the existing configuration")
	}
}
