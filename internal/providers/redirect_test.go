package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/domain"
)

type redirectProvider struct {
	name     string
	response string
	request  func(context.Context, string) error
}

func redirectProviders() []redirectProvider {
	request := Request{ModelID: "model", UserPrompt: "private prompt", MaxOutputTokens: 8, ResponseTimeout: 2 * time.Second}
	config := func(base string) domain.ProviderConfig {
		return domain.ProviderConfig{ID: "redirect-test", BaseURL: base, Headers: map[string]string{"X-Custom-Secret": "custom-test-key"}}
	}
	return []redirectProvider{
		{"openai", "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", func(ctx context.Context, base string) error {
			return NewOpenAI(config(base)).stream(ctx, request, "test-api-key", make(chan Event, 16))
		}},
		{"gemini", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n", func(ctx context.Context, base string) error {
			return NewGemini(config(base)).stream(ctx, request, "test-api-key", make(chan Event, 16))
		}},
		{"transcription", `{ "text": "ok" }`, func(ctx context.Context, base string) error {
			cfg := config(base)
			cfg.Kind = domain.ProviderTranscription
			req := request
			req.Task = "transcribe"
			req.Audio = &domain.AudioClip{PCM16: []int16{1, 2}, SampleRate: 16000, Channels: 1}
			return NewTranscription(cfg, nil).request(ctx, req, "test-api-key", make(chan Event, 16))
		}},
	}
}

func TestProviderRedirectDoesNotSendSecretsToAnotherOrigin(t *testing.T) {
	for _, provider := range redirectProviders() {
		for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
			t.Run(provider.name+"/"+strconv.Itoa(status), func(t *testing.T) {
				var hits atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					io.WriteString(w, provider.response)
				}))
				defer target.Close()
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, target.URL+"/stolen", status)
				}))
				defer source.Close()
				err := provider.request(context.Background(), source.URL)
				if hits.Load() != 0 {
					t.Fatalf("redirect sent %d request(s) to another origin", hits.Load())
				}
				if err == nil {
					t.Fatal("cross-origin redirect reported success")
				}
			})
		}
	}
}

func TestProviderSameOriginRedirectKeepsRequest(t *testing.T) {
	for _, provider := range redirectProviders() {
		t.Run(provider.name, func(t *testing.T) {
			var hits atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/redirected" {
					http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
					return
				}
				hits.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != http.MethodPost || len(body) == 0 {
					t.Error("same-origin 307 lost the POST request body")
				}
				if r.Header.Get("Authorization") == "" && r.Header.Get("X-Goog-Api-Key") == "" {
					t.Error("same-origin redirect lost authentication")
				}
				io.WriteString(w, provider.response)
			}))
			defer source.Close()
			if err := provider.request(context.Background(), source.URL); err != nil {
				t.Fatal(err)
			}
			if hits.Load() != 1 {
				t.Fatalf("redirected requests = %d, want 1", hits.Load())
			}
		})
	}
}

func TestProviderRedirectPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, from, to string
		allow          bool
	}{
		{"path", "https://provider.example/v1", "https://provider.example/v2", true},
		{"default https port", "https://provider.example/v1", "https://PROVIDER.example:443/v2", true},
		{"default http port", "http://provider.example/v1", "http://provider.example:80/v2", true},
		{"host", "https://provider.example/v1", "https://other.example/v2", false},
		{"subdomain", "https://provider.example/v1", "https://other.provider.example/v2", false},
		{"port", "https://provider.example/v1", "https://provider.example:8443/v2", false},
		{"downgrade", "https://provider.example/v1", "http://provider.example/v2", false},
		{"scheme", "http://provider.example/v1", "https://provider.example/v2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := &http.Request{URL: mustURL(t, tc.from)}
			next := &http.Request{URL: mustURL(t, tc.to)}
			err := checkProviderRedirect(next, []*http.Request{first})
			if (err == nil) != tc.allow {
				t.Fatalf("allow = %v, error = %v", tc.allow, err)
			}
		})
	}
	first := &http.Request{URL: mustURL(t, "https://provider.example/v1")}
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = first
	}
	if err := checkProviderRedirect(first, via); err == nil || !strings.Contains(err.Error(), "10") {
		t.Fatalf("redirect loop was not bounded: %v", err)
	}
}
