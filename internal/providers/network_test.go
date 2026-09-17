package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"aioverlay/internal/domain"
)

type testProxyResolver struct {
	url string
	err error
}

func (r testProxyResolver) ProxyURL(context.Context) (string, error) { return r.url, r.err }

func TestRequestTransportUsesExplicitAIVPNProxy(t *testing.T) {
	tr, err := requestTransport(context.Background(), testProxyResolver{url: "http://user:pass@127.0.0.1:23456"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	u, err := tr.Proxy(&http.Request{URL: mustURL(t, "https://provider.example/v1")})
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.Host != "127.0.0.1:23456" || u.User.Username() != "user" {
		t.Fatalf("unexpected proxy: %#v", u)
	}
}
func TestRequestTransportFailsClosedWhenAIVPNUnavailable(t *testing.T) {
	_, err := requestTransport(context.Background(), testProxyResolver{err: errors.New("broken tunnel")}, 0)
	if err == nil {
		t.Fatal("expected fail-closed error")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestDirectProviderDoesNotBypassBrokenAIVPN(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	p := NewOpenAI(domain.ProviderConfig{ID: "test", BaseURL: srv.URL}, testProxyResolver{err: errors.New("tunnel down")})
	events, errs := p.Stream(context.Background(), Request{ModelID: "m", UserPrompt: "x"}, "")
	for range events {
	}
	err := <-errs
	if err == nil || hits.Load() != 0 {
		t.Fatalf("direct fallback occurred: err=%v hits=%d", err, hits.Load())
	}
}

func TestOpenCodeDoesNotStartWhenAIVPNResolverFails(t *testing.T) {
	p := NewEmbeddedOpenCode(domain.ProviderConfig{ID: "oc", OpenCodeProviderID: "anthropic"}, testProxyResolver{err: errors.New("tunnel down")})
	_, err := p.run(context.Background(), Request{ModelID: "anthropic/model"}, "key")
	if err == nil || !strings.Contains(err.Error(), "AI VPN unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}
