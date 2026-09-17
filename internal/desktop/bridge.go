package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

// The production desktop calls the handlers in-process. No localhost listener,
// remote debugging port or browser tab is needed to render Pipeline Studio.
type Request struct {
	ID     string          `json:"id"`
	Path   string          `json:"path"`
	Method string          `json:"method"`
	Body   json.RawMessage `json:"body"`
}
type Reply struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

func Call(ctx context.Context, h http.Handler, r Request) Reply {
	if !strings.HasPrefix(r.Path, "/api/") || strings.ContainsAny(r.Path, "\r\n") || len(r.Path) > 4096 {
		return Reply{Error: "Invalid desktop route"}
	}
	u, parseErr := url.ParseRequestURI(r.Path)
	if parseErr != nil || u.IsAbs() || strings.Contains(u.Path, "..") || !strings.HasPrefix(u.Path, "/api/") {
		return Reply{Error: "Invalid desktop route"}
	}
	if len(r.Body) > 20<<20 {
		return Reply{Error: "Request is too large"}
	}
	if r.Method == "" {
		r.Method = "GET"
	}
	switch r.Method {
	case "GET", "POST", "PUT", "DELETE":
	default:
		return Reply{Error: "Unsupported method"}
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, "http://desktop.invalid"+r.Path, bytes.NewReader(r.Body))
	if err != nil {
		return Reply{Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code/100 != 2 {
		message := strings.TrimSpace(rec.Body.String())
		if len(message) > 2048 {
			message = message[:2048]
		}
		return Reply{Error: message}
	}
	data := rec.Body.Bytes()
	if !json.Valid(data) {
		return Reply{Error: fmt.Sprintf("Invalid response from %s", r.Path)}
	}
	return Reply{OK: true, Data: append(json.RawMessage(nil), data...)}
}
