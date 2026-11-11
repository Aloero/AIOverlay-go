package control

import (
	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"aioverlay/internal/providers"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type capabilityTestBackend struct {
	cfg domain.Config
}

func (b *capabilityTestBackend) GetConfig() domain.Config           { return b.cfg }
func (b *capabilityTestBackend) SaveConfig(cfg domain.Config) error { b.cfg = cfg; return nil }
func (*capabilityTestBackend) Catalog(CatalogQuery) (CatalogPage, error) {
	return CatalogPage{}, nil
}
func (*capabilityTestBackend) RefreshCatalog() error { return nil }
func (*capabilityTestBackend) RunTest(context.Context, TestRequest) (engine.Result, error) {
	return engine.Result{}, nil
}
func (*capabilityTestBackend) RunBenchmark(context.Context, BenchmarkRequest) (BenchmarkReport, error) {
	return BenchmarkReport{}, nil
}
func (*capabilityTestBackend) Traces() []domain.ExecutionTrace { return nil }
func (*capabilityTestBackend) Readiness() ReadinessReport      { return ReadinessReport{} }
func (*capabilityTestBackend) ProviderCapabilities() []providers.AdapterCapabilities {
	return []providers.AdapterCapabilities{{
		ProviderID: "direct",
		Label:      "Direct",
		Kind:       domain.ProviderOpenAICompatible,
		Media:      providers.MediaCapabilities{Audio: true, Image: true},
		Realtime:   providers.UnsupportedRealtime("not implemented"),
	}}
}

func TestProviderCapabilitiesEndpointIsReadOnlyAndExplicit(t *testing.T) {
	backend := &capabilityTestBackend{cfg: config.Default()}
	server := New("127.0.0.1:0", backend)
	request := httptest.NewRequest(http.MethodGet, "/api/providers/capabilities", nil)
	response := httptest.NewRecorder()

	server.DesktopHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result []providers.AdapterCapabilities
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("capabilities=%+v", result)
	}
	realtime := result[0].Realtime
	if realtime.Available || realtime.Operations[providers.RealtimeAppendAudio] {
		t.Fatalf("unsupported realtime became ready: %+v", realtime)
	}
}
