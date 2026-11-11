package control

import (
	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type auditBackend struct {
	cfg     domain.Config
	started chan struct{}
}

func (a *auditBackend) GetConfig() domain.Config                  { return a.cfg }
func (a *auditBackend) SaveConfig(c domain.Config) error          { a.cfg = c; return nil }
func (a *auditBackend) Catalog(CatalogQuery) (CatalogPage, error) { return CatalogPage{}, nil }
func (a *auditBackend) RefreshCatalog() error                     { return nil }
func (a *auditBackend) RunTest(context.Context, TestRequest) (engine.Result, error) {
	return engine.Result{}, nil
}
func (a *auditBackend) RunBenchmark(context.Context, BenchmarkRequest) (BenchmarkReport, error) {
	return BenchmarkReport{}, nil
}
func (a *auditBackend) Traces() []domain.ExecutionTrace { return nil }
func (a *auditBackend) Readiness() ReadinessReport      { return ReadinessReport{} }
func (a *auditBackend) Universe() Universe              { return Universe{} }
func (a *auditBackend) Run(context.Context, string, engine.Input, engine.RunOptions) (engine.Result, error) {
	a.started <- struct{}{}
	return engine.Result{}, nil
}
func TestAuditCancelDuringCapturePreventsModelRequest(t *testing.T) {
	b := &auditBackend{cfg: config.Default(), started: make(chan struct{}, 1)}
	s := New("127.0.0.1:0", b)
	captured, release := make(chan struct{}), make(chan struct{})
	s.Capture = func() ([]byte, error) { close(captured); <-release; return []byte("fixture"), nil }
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.DesktopHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/job", strings.NewReader(`{"source":"screen"}`)))
	}()
	<-captured
	s.CancelRun()
	close(release)
	<-done
	select {
	case <-b.started:
		t.Fatal("inference began AFTER cancel during capture")
	case <-time.After(100 * time.Millisecond):
	}
}
func TestAuditHeadlessConfigRequiresAuthorization(t *testing.T) {
	b := &auditBackend{cfg: config.Default()}
	b.cfg.Providers[1].CredentialPool.Keys = []domain.Credential{{ID: "test", Secret: "FAKE-PRIVATE-KEY", Enabled: true}}
	s := New("0.0.0.0:17891", b)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/config", nil))
	if w.Code == 200 && strings.Contains(w.Body.String(), "FAKE-PRIVATE-KEY") {
		t.Fatal("unauthenticated HTTP request receives plaintext config secrets")
	}
}
func TestAuditCrossOriginWriteRejected(t *testing.T) {
	b := &auditBackend{cfg: config.Default()}
	s := New("127.0.0.1:17891", b)
	cfg := b.cfg
	cfg.ActivePipelineID = cfg.Pipelines[1].ID
	data, _ := json.Marshal(cfg)
	r := httptest.NewRequest(http.MethodPost, "/api/config/import", strings.NewReader(string(data)))
	r.Header.Set("Origin", "https://untrusted.invalid")
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("cross-origin text/plain config write accepted without token/origin validation")
	}
}
func TestAuditDecodeRejectsBytesBeyondLimit(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/config", strings.NewReader(`{}              {"unexpected":true}`))
	var v map[string]any
	if err := decodeJSON(r, &v, 16); err == nil {
		t.Fatal("trailing JSON beyond LimitReader boundary ignored")
	}
}
