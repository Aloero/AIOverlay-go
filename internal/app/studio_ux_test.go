package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/control"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func studioCall(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func TestStudioSimulationAndShortcutsUseRealHandlersWithoutModels(t *testing.T) {
	a := studioTestApp(t)
	s := control.New("127.0.0.1:0", a)
	h := s.DesktopHandler()
	cfg := a.GetConfig()
	w := studioCall(t, h, "POST", "/api/simulation", map[string]any{"config": cfg, "pipeline_id": cfg.ActivePipelineID, "outcomes": []any{}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var report struct {
		Simulated bool `json:"simulated"`
		Calls     int  `json:"calls"`
	}
	if json.Unmarshal(w.Body.Bytes(), &report) != nil || !report.Simulated || report.Calls != 1 {
		t.Fatal(w.Body.String())
	}
	if s.CurrentJob().ID != "" || len(a.Traces()) != 0 {
		t.Fatal("simulation modified real jobs/history")
	}
	w = studioCall(t, h, "POST", "/api/catalog/selection", map[string]any{"references": []string{}, "references_only": true, "limit": 40})
	var page control.CatalogPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 0 {
		t.Fatal("empty favorites leaked full catalog", w.Body.String())
	}
	w = studioCall(t, h, "POST", "/api/limits/preview", map[string]any{"slot": domain.ModelSlot{}, "budget": domain.ExecutionBudget{}, "defaults": domain.RequestDefaults{FirstTokenTimeoutMS: 1234}})
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"provider"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`1234`)) {
		t.Fatal(w.Body.String())
	}
	w = studioCall(t, h, "POST", "/api/providers/test", map[string]any{"provider_id": "opencode", "model_id": "fixture", "confirm": false})
	if w.Code != 400 || s.CurrentJob().ID != "" {
		t.Fatal("unconfirmed probe started")
	}
}

type uxProbeProvider struct {
	calls atomic.Int32
	bad   atomic.Bool
}

func (p *uxProbeProvider) ID() string { return "opencode" }
func (p *uxProbeProvider) Stream(ctx context.Context, r providers.Request, key string) (<-chan providers.Event, <-chan error) {
	p.calls.Add(1)
	if len(r.ImageJPEG) > 0 || r.MaxOutputTokens != 32 || key != "TEST_ONLY_PROVIDER_KEY_027" {
		p.bad.Store(true)
	}
	events := make(chan providers.Event, 1)
	errs := make(chan error)
	events <- providers.Event{Type: providers.EventDelta, Text: "OK"}
	close(events)
	close(errs)
	return events, errs
}
func TestStudioConfirmedProbeDoesNotChangeActivePipeline(t *testing.T) {
	a := studioTestApp(t)
	cfg := a.GetConfig()
	pc := cfg.Providers[0]
	pc.CredentialPool.Keys = []domain.Credential{{ID: "test", Label: "Test", Secret: "TEST_ONLY_PROVIDER_KEY_027", Enabled: true}}
	if err := a.SaveProvider(pc); err != nil {
		t.Fatal(err)
	}
	fake := &uxProbeProvider{}
	a.registry.Set("opencode", fake)
	s := control.New("127.0.0.1:0", a)
	w := studioCall(t, s.DesktopHandler(), "POST", "/api/providers/test", map[string]any{"provider_id": "opencode", "model_id": "fixture", "credential_id": "test", "confirm": true})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for s.CurrentJob().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	job := s.CurrentJob()
	if job.Running || job.Error != "" || job.Purpose != "connection_test" || job.Trace.Purpose != "connection_test" || fake.calls.Load() != 1 || fake.bad.Load() {
		t.Fatalf("probe mismatch %+v", job)
	}
	if a.GetConfig().ActivePipelineID != cfg.ActivePipelineID {
		t.Fatal("probe activated ephemeral pipeline")
	}
	info := a.StudioState()
	if len(info.Credentials) != 1 || info.Credentials[0].Outcome != domain.OutcomeSuccess {
		t.Fatal(info)
	}
}
