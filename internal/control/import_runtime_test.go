package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
)

type importAwareBackend struct {
	cfg      domain.Config
	imported bool
}

func (backend *importAwareBackend) GetConfig() domain.Config           { return backend.cfg }
func (backend *importAwareBackend) SaveConfig(cfg domain.Config) error { backend.cfg = cfg; return nil }
func (backend *importAwareBackend) Catalog(CatalogQuery) (CatalogPage, error) {
	return CatalogPage{}, nil
}
func (backend *importAwareBackend) RefreshCatalog() error { return nil }
func (backend *importAwareBackend) RunTest(context.Context, TestRequest) (engine.Result, error) {
	return engine.Result{}, nil
}
func (backend *importAwareBackend) RunBenchmark(context.Context, BenchmarkRequest) (BenchmarkReport, error) {
	return BenchmarkReport{}, nil
}
func (backend *importAwareBackend) Traces() []domain.ExecutionTrace { return nil }
func (backend *importAwareBackend) Readiness() ReadinessReport      { return ReadinessReport{} }
func (backend *importAwareBackend) ImportConfig(cfg domain.Config) error {
	backend.imported = true
	backend.cfg = cfg
	return nil
}

func TestConfigImportUsesRuntimeSafeApplyWhenBackendSupportsIt(t *testing.T) {
	cfg := config.Default()
	cfg.Pipelines[0].Sources = []domain.EventSource{{ID: "audio", AutoStart: true}}
	backend := &importAwareBackend{cfg: cfg}
	server := New("127.0.0.1:0", backend)
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/config/import", strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.DesktopHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
	}
	if !backend.imported || backend.cfg.Pipelines[0].Sources[0].AutoStart {
		t.Fatalf("import-aware apply not used: imported=%v source=%+v", backend.imported, backend.cfg.Pipelines[0].Sources[0])
	}
	var result struct {
		AutostartDisabled       int  `json:"autostart_disabled"`
		RuntimeOverridesCleared bool `json:"runtime_overrides_cleared"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.AutostartDisabled != 1 || !result.RuntimeOverridesCleared {
		t.Fatalf("import response=%+v", result)
	}
}
