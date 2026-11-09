package control

import (
	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigExportRequiresGetAndDisablesCaching(t *testing.T) {
	cfg := config.Default()
	cfg.Providers[1].CredentialPool.Keys = []domain.Credential{{ID: "export-test", Secret: "FAKE-EXPORT-SECRET", Enabled: true}}
	s := New("127.0.0.1:0", &auditBackend{cfg: cfg})
	for _, mode := range []struct {
		name    string
		handler http.Handler
	}{
		{"http", s.Handler()}, {"desktop", s.DesktopHandler()},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
				t.Run(method, func(t *testing.T) {
					r := httptest.NewRequest(method, "http://127.0.0.1/api/config/export", nil)
					r.Header.Set("Authorization", "Bearer "+s.DevelopmentToken())
					r.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					mode.handler.ServeHTTP(w, r)
					if method != http.MethodGet {
						if w.Code != http.StatusMethodNotAllowed {
							t.Errorf("status=%d, want 405", w.Code)
						}
						if strings.Contains(w.Body.String(), "FAKE-EXPORT-SECRET") {
							t.Error("rejected method exposed configuration")
						}
						return
					}
					if w.Code != http.StatusOK {
						t.Fatalf("status=%d", w.Code)
					}
					if w.Header().Get("Cache-Control") != "no-store" {
						t.Error("configuration export may be cached")
					}
					if w.Header().Get("Content-Disposition") != "attachment; filename=AIOverlay-config.json" {
						t.Error("attachment header missing")
					}
					if !strings.Contains(w.Body.String(), "FAKE-EXPORT-SECRET") {
						t.Error("explicit export lost its configuration")
					}
				})
			}
		})
	}
}
