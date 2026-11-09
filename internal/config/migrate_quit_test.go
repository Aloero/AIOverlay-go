package config

import (
	"testing"

	"aioverlay/internal/domain"
)

func TestMigrateFiveAddsQuitHotkey(t *testing.T) {
	cfg := Default()
	cfg.Version = 5
	cfg.Overlay.Hotkeys = map[string]string{"analyze": "Ctrl+Shift+S", "clear": "Ctrl+Shift+C"}
	if err := Migrate(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Version != domain.ConfigVersion || cfg.Overlay.Hotkeys["quit"] != "Ctrl+Shift+Q" {
		t.Fatalf("quit not backfilled: %+v", cfg.Overlay.Hotkeys)
	}
}

func TestMigrateFiveNeverStealsQuitCombination(t *testing.T) {
	cfg := Default()
	cfg.Version = 5
	cfg.Overlay.Hotkeys = map[string]string{"analyze": "Ctrl+Shift+Q", "clear": "Ctrl+Shift+C"}
	if err := Migrate(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Overlay.Hotkeys["quit"] != "" || cfg.Overlay.Hotkeys["analyze"] != "Ctrl+Shift+Q" {
		t.Fatalf("combination stolen: %+v", cfg.Overlay.Hotkeys)
	}
}
