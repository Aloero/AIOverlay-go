package modelhost

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// endpoint is the shape a control endpoint has in production: a password plus an extra header, both
// of which must stay on the origin the host handed us.
func testEndpoint(base string) Endpoint {
	return Endpoint{BaseURL: base, Username: "opencode", Password: "runtime-password", Headers: map[string]string{"X-Custom-Secret": "custom-test-key"}}
}

func TestEndpointRedirectDoesNotLeaveOrigin(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var hits atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				io.WriteString(w, `{"ok":true}`)
			}))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/stolen", status)
			}))
			defer source.Close()
			code, _, err := testEndpoint(source.URL).DoJSON(context.Background(), http.MethodPost, "/session", map[string]string{"prompt": "private prompt"}, nil, 2*time.Second)
			if hits.Load() != 0 {
				t.Fatalf("a redirect forwarded the host password to another origin %d times", hits.Load())
			}
			if err == nil && code/100 == 2 {
				t.Fatal("a cross-origin redirect was reported as success")
			}
		})
	}
}

func TestEndpointSameOriginRedirectKeepsRequest(t *testing.T) {
	var sawBody, sawAuth, sawCustom bool
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/redirected" {
			http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
			return
		}
		sawBody = len(r.Header.Get("Content-Length")) > 0 || r.ContentLength > 0
		sawAuth = r.Header.Get("Authorization") != ""
		sawCustom = r.Header.Get("X-Custom-Secret") == "custom-test-key"
		io.WriteString(w, `{"id":"ses_1"}`)
	}))
	defer source.Close()
	var out struct {
		ID string `json:"id"`
	}
	code, _, err := testEndpoint(source.URL).DoJSON(context.Background(), http.MethodPost, "/session", map[string]string{"prompt": "private prompt"}, &out, 2*time.Second)
	if err != nil || code != http.StatusOK {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if out.ID != "ses_1" {
		t.Fatalf("decoded response %+v", out)
	}
	if !sawBody || !sawAuth || !sawCustom {
		t.Fatalf("same-origin redirect lost body=%v auth=%v headers=%v", sawBody, sawAuth, sawCustom)
	}
}

func TestEndpointRejectsUnusableResponses(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "x")
			for i := 0; i < MaxResponseBytes/1024/1024+2; i++ {
				_, _ = io.WriteString(w, strings.Repeat("x", 1024*1024))
			}
		}))
		defer source.Close()
		if _, _, err := testEndpoint(source.URL).DoJSON(context.Background(), http.MethodGet, "/big", nil, nil, 5*time.Second); err == nil {
			t.Fatal("an unbounded response was accepted")
		}
	})
	t.Run("not json", func(t *testing.T) {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "<html>upstream proxy error</html>")
		}))
		defer source.Close()
		var out map[string]any
		_, _, err := testEndpoint(source.URL).DoJSON(context.Background(), http.MethodGet, "/session", nil, &out, 2*time.Second)
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("a 2xx body that is not JSON must be marked unusable, got %v", err)
		}
	})
}
