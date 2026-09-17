package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/control"
	"aioverlay/internal/domain"
)

func studioTestApp(t *testing.T) *App {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := config.NewStore(p).Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	app, err := New(p, filepath.Join("..", "..", "runtime", "catalog", "models-dev-api.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}
func TestActivePipelinePreservesWarmRegistryAndEngine(t *testing.T) {
	a := studioTestApp(t)
	registry, engine := a.registry, a.engine
	next := a.cfg.Pipelines[1].ID
	if err := a.SetActivePipeline(next); err != nil {
		t.Fatal(err)
	}
	if a.registry != registry || a.engine != engine {
		t.Fatal("activation discarded warm provider or scheduling state")
	}
	loaded, err := a.store.Load()
	if err != nil || loaded.ActivePipelineID != next {
		t.Fatal("selection was not persisted", err)
	}
	a.operation.RLock()
	err = a.SetActivePipeline(a.cfg.Pipelines[0].ID)
	a.operation.RUnlock()
	if err == nil || a.GetConfig().ActivePipelineID != next {
		t.Fatal("busy selection changed active pipeline")
	}
	if err := a.SetActivePipeline("missing"); err == nil {
		t.Fatal("missing pipeline accepted")
	}
}
func TestProviderSaveDoesNotReplacePipelines(t *testing.T) {
	a := studioTestApp(t)
	before := a.GetConfig()
	p := before.Providers[0]
	p.ID = "oc_anthropic"
	p.OpenCodeProviderID = "anthropic"
	p.Label = "Anthropic"
	p.CredentialEnv = []string{"ANTHROPIC_API_KEY"}
	p.CredentialPool.Keys = []domain.Credential{{ID: "test", Secret: "TEST-ONLY-KEY", Label: "Test alias", Enabled: true}}
	if err := a.SaveProvider(p); err != nil {
		t.Fatal(err)
	}
	after := a.GetConfig()
	b, _ := json.Marshal(before.Pipelines)
	c, _ := json.Marshal(after.Pipelines)
	if !bytes.Equal(b, c) || len(after.Providers) != len(before.Providers)+1 {
		t.Fatal("connection save changed graph or missed provider")
	}
}
func TestStudioPlanningAPIsNeverRunModels(t *testing.T) {
	a := studioTestApp(t)
	handler := control.New("127.0.0.1:0", a).Handler()
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request(http.MethodGet, "/api/providers/catalog", nil)
	var directory []domain.ProviderMeta
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &directory) != nil || len(directory) < 20 {
		t.Fatalf("bad directory %d", w.Code)
	}
	found := false
	for _, p := range directory {
		if p.ID == "anthropic" {
			found = len(p.CredentialEnv) > 0 && p.Models > 0
		}
	}
	if !found {
		t.Fatal("directory lost provider metadata")
	}
	cfg := a.GetConfig()
	pool := *cfg.Pipelines[0].Nodes[1].ModelPool
	pool.Models = nil
	pool.QualityFloor = 0
	pool.Selection = &domain.CatalogSelection{Mode: "catalog", Modality: "image", FreeOnly: true}
	w = request(http.MethodPost, "/api/pool/plan", map[string]any{"config": cfg, "pool": pool, "image": true})
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"candidates":[{`)) {
		t.Fatalf("empty catalog plan: %d", w.Code)
	}
	if bytes.Contains(w.Body.Bytes(), []byte(`"secret"`)) {
		t.Fatal("plan exposed credential data")
	}
	w = request(http.MethodPost, "/api/pipelines/active", map[string]string{"id": cfg.Pipelines[1].ID})
	if w.Code != 200 || a.GetConfig().ActivePipelineID != cfg.Pipelines[1].ID {
		t.Fatal("activation endpoint failed")
	}
	if len(a.Traces()) != 0 {
		t.Fatal("settings or planning unexpectedly executed models")
	}
	w = request(http.MethodGet, "/api/catalog?provider_id=google&limit=500", nil)
	var page control.CatalogPage
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) == 0 {
		t.Fatal("catalog empty")
	}
	for _, m := range page.Items {
		if m.ProviderID != "google" {
			t.Fatal("provider-specific catalog mixed other providers")
		}
	}
}
