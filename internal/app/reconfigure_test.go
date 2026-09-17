package app

import (
	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"reflect"
	"testing"
)

func TestUIOnlySaveKeepsWarmEngineRegistryAndTunnel(t *testing.T) {
	a := studioTestApp(t)
	eng, reg, tun := a.engine, a.registry, a.tunnel
	cfg := a.GetConfig()
	cfg.Overlay.FontSize++
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if a.engine != eng || a.registry != reg || a.tunnel != tun {
		t.Fatal("UI save discarded warm state")
	}
}
func TestConfigPreparationFailureDoesNotCommit(t *testing.T) {
	a := studioTestApp(t)
	cfg := a.GetConfig()
	old := cfg
	cfg.Overlay.Hotkeys["control"] = "Ctrl+F12"
	if a.SaveConfig(cfg) == nil {
		t.Fatal("accepted reserved key")
	}
	disk, err := config.NewStore(a.ConfigPath()).Load()
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(disk.Overlay.Hotkeys, cfg.Overlay.Hotkeys) {
		t.Fatal("invalid draft committed")
	}
	_ = old
}
func TestChangedProviderKeepsEngineButRebuildsConnections(t *testing.T) {
	a := studioTestApp(t)
	eng, reg := a.engine, a.registry
	cfg := a.GetConfig()
	cfg.Providers[0].CredentialPool.Keys = []domain.Credential{{ID: "new", Secret: "test-only", Enabled: true}}
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if a.engine != eng || a.registry == reg {
		t.Fatal("wrong reconfiguration boundaries")
	}
}

func TestProviderLabelOnlyKeepsConnections(t *testing.T) {
	a := studioTestApp(t)
	reg := a.registry
	cfg := a.GetConfig()
	cfg.Providers[0].Label = "Different label"
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if reg != a.registry {
		t.Fatal("label invalidated runtime")
	}
}
