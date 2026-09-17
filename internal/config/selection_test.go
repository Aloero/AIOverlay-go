package config

import (
	"aioverlay/internal/domain"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCatalogOnlyPoolAndCanvasLayoutRoundTrip(t *testing.T) {
	cfg := Default()
	pool := cfg.Pipelines[0].Nodes[1].ModelPool
	pool.Models = nil
	pool.Selection = &domain.CatalogSelection{Mode: "catalog", Modality: "auto", FreeOnly: true, ProviderIDs: []string{"opencode"}, DefaultQualityScore: 0}
	cfg.ControlCenter.Layout = map[string]domain.CanvasLayout{cfg.ActivePipelineID: {PanX: -123, PanY: 51, Zoom: .75}}
	s := NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(got, cfg) {
		t.Fatal("catalog settings did not round trip", err)
	}
}
func TestRejectInvalidCatalogSelection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection domain.CatalogSelection
	}{{"mode", domain.CatalogSelection{Mode: "everything", Modality: "auto"}}, {"modality", domain.CatalogSelection{Mode: "catalog", Modality: "sound"}}, {"provider", domain.CatalogSelection{Mode: "catalog", Modality: "auto", ProviderIDs: []string{"unknown"}}}, {"duplicate", domain.CatalogSelection{Mode: "catalog", Modality: "auto", ProviderIDs: []string{"opencode", "opencode"}}}, {"quality", domain.CatalogSelection{Mode: "catalog", Modality: "auto", DefaultQualityScore: 101}}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Pipelines[0].Nodes[1].ModelPool.Selection = &tc.selection
			if Validate(cfg) == nil {
				t.Fatal("invalid selection accepted")
			}
		})
	}
}
