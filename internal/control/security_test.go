package control

import (
	"aioverlay/internal/config"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPAuthorizationIsSeparateFromDesktopBridge(t *testing.T) {
	s := New("127.0.0.1:0", &auditBackend{cfg: config.Default()})
	request := func(token, origin, host string) int {
		r := httptest.NewRequest("GET", "http://127.0.0.1/api/config", nil)
		r.Host = host
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if request("", "", "127.0.0.1") != 401 {
		t.Fatal("anonymous read allowed")
	}
	if request(s.DevelopmentToken(), "", "127.0.0.1") != 200 {
		t.Fatal("authenticated read denied")
	}
	if request(s.DevelopmentToken(), "https://evil.invalid", "127.0.0.1") != 403 || request(s.DevelopmentToken(), "", "evil.invalid") != 403 {
		t.Fatal("origin/host bypass")
	}
	w := httptest.NewRecorder()
	s.DesktopHandler().ServeHTTP(w, httptest.NewRequest("GET", "http://desktop.invalid/api/config", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "pipelines") {
		t.Fatal("in-process bridge broken")
	}
}
func TestDevelopmentBindRejectsLAN(t *testing.T) {
	for _, a := range []string{"0.0.0.0:8888", ":8888", "192.168.1.2:8888", "example.com:8888"} {
		if validateListenAddress(a) == nil {
			t.Fatal(a)
		}
	}
}
