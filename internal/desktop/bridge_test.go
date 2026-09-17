package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestInProcessBridge(t *testing.T) {
	calls := 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/test" || r.Method != "POST" {
			t.Error(r.URL, r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	result := Call(context.Background(), h, Request{Path: "/api/test", Method: "POST", Body: json.RawMessage(`{}`)})
	if !result.OK || calls != 1 || !json.Valid(result.Data) {
		t.Fatal(result, calls)
	}
}
func TestBridgeRejectsInvalidRoutesAndMethods(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid route reached handler") })
	for _, path := range []string{"https://bad.invalid/api/config", "/etc/passwd", "/api/../config", "/api/%2e%2e/private", "/api/x\r\nInjected: true"} {
		if r := Call(context.Background(), h, Request{Path: path}); r.OK || r.Error == "" {
			t.Error(path, r)
		}
	}
	if r := Call(context.Background(), h, Request{Path: "/api/config", Method: "CONNECT"}); r.OK {
		t.Fatal(r)
	}
}
func TestBridgeErrorDoesNotBecomeSuccess(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, strings.Repeat("x", 5000), 400) })
	r := Call(context.Background(), h, Request{Path: "/api/config"})
	if r.OK || len(r.Error) > 2048 || r.Error == "" {
		t.Fatal(r)
	}
}
