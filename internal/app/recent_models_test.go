package app

import (
	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"context"
	"slices"
	"testing"
)

func TestRecentModelsReflectActualProbeAndPersistOnSave(t *testing.T) {
	a := studioTestApp(t)
	p := a.GetConfig().Providers[0]
	p.CredentialPool.Keys = []domain.Credential{{ID: "test", Enabled: true, Secret: "TEST_ONLY_PROVIDER_KEY_027"}}
	if err := a.SaveProvider(p); err != nil {
		t.Fatal(err)
	}
	if len(a.StudioState().RecentModels) != 0 {
		t.Fatal("saving credentials falsely marks models used")
	}
	a.registry.Set("opencode", &uxProbeProvider{})
	_, err := a.ProbeConnection(context.Background(), engine.ConnectionProbe{Confirm: true, ProviderID: "opencode", CredentialID: "test", ModelID: "fixture"}, engine.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(a.StudioState().RecentModels, "opencode/fixture") {
		t.Fatal("actual model absent from recent usage")
	}
	c := a.GetConfig()
	c.Overlay.FontSize++
	if err = a.SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	stored, err := config.NewStore(a.ConfigPath()).Load()
	if err != nil || !slices.Contains(stored.ControlCenter.RecentModels, "opencode/fixture") {
		t.Fatal("usage not portable after save", err)
	}
}
