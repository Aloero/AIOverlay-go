package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/modelhost"
)

// fakeHost is a model host supplied from the outside, i.e. exactly the seam a replacement runtime
// or a test uses instead of the bundled child process.
type fakeHost struct {
	endpoint modelhost.Endpoint
	needs    []modelhost.Need
}

func (f *fakeHost) ID() string      { return "fake" }
func (f *fakeHost) Version() string { return "test" }
func (f *fakeHost) Close()          {}
func (f *fakeHost) Served(context.Context, modelhost.Need) ([]modelhost.ModelRef, error) {
	return nil, modelhost.ErrServedUnsupported
}
func (f *fakeHost) Endpoint(_ context.Context, need modelhost.Need) (modelhost.Endpoint, func(), error) {
	f.needs = append(f.needs, need)
	return f.endpoint, func() {}, nil
}

func TestSplitOpenCodeRef(t *testing.T) {
	p, m := splitOpenCodeRef("anthropic/claude-sonnet", "fallback")
	if p != "anthropic" || m != "claude-sonnet" {
		t.Fatalf("%s %s", p, m)
	}
	p, m = splitOpenCodeRef("mimo-v2.5-free", "opencode")
	if p != "opencode" || m != "mimo-v2.5-free" {
		t.Fatalf("%s %s", p, m)
	}
}
func TestHTTPErrorNormalization(t *testing.T) {
	cases := []struct {
		status int
		msg    string
		want   string
	}{{401, "no", "auth_failed"}, {429, "slow", "rate_limited"}, {403, `{"error":{"code":"unsupported_country_region_territory"}}`, "region_restricted"}, {404, "model", "model_unavailable"}, {503, "down", "provider_unavailable"}}
	for _, c := range cases {
		if got := string(HTTPError(c.status, c.msg).Outcome); got != c.want {
			t.Fatalf("%d got %s want %s", c.status, got, c.want)
		}
	}
}

func TestOpenCodeErrorEnvelopeIsStructuredAndDoesNotEchoPayload(t *testing.T) {
	for _, c := range []struct {
		status int
		out    domain.Outcome
	}{{401, domain.OutcomeAuthFailed}, {429, domain.OutcomeRateLimited}, {503, domain.OutcomeProviderUnavailable}} {
		raw, _ := json.Marshal(map[string]any{"name": "APIError", "data": map[string]any{"statusCode": c.status, "message": "SECRET", "responseBody": "SECRET"}})
		got := openCodeError(raw)
		if got.Outcome != c.out || got.StatusCode != c.status || got.Message != "OpenCode provider request failed" {
			t.Fatal(got)
		}
	}
}

// The point of the host boundary, tested where it is decided: the adapter is driven by a host someone
// else supplied. Nothing here starts the bundled runtime, so the same adapter would talk to another
// engine that speaks the API, and a test needs no child process.
func TestEmbeddedOpenCodeAdapterRunsAgainstAnInjectedHost(t *testing.T) {
	var gotPaths []string
	var gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPaths = append(gotPaths, r.URL.Path)
		switch r.URL.Path {
		case "/session":
			io.WriteString(w, `{"id":"ses_1"}`)
		case "/session/ses_1/message":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotBody)
			io.WriteString(w, `{"info":{},"parts":[{"type":"text","text":"  answered  "}]}`)
		case "/session/ses_1":
			io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter := newEmbeddedOpenCode(domain.ProviderConfig{ID: "oc", Kind: domain.ProviderEmbeddedOpenCode, Enabled: true}, Deps{
		HostFactory: func(domain.ProviderConfig) (modelhost.Host, error) {
			return &fakeHost{endpoint: modelhost.Endpoint{BaseURL: server.URL, Username: "opencode", Password: "pw"}}, nil
		},
	})
	text, err := adapter.run(context.Background(), Request{
		ModelID: "opencode/glm-5.3-flash-free", UserPrompt: "что на экране", ImageJPEG: []byte{1, 2, 3},
		SystemPrompt: "be brief", ResponseTimeout: 2 * time.Second,
	}, "credential")
	if err != nil {
		t.Fatal(err)
	}
	if text != "answered" {
		t.Fatalf("text=%q", text)
	}
	// The cleanup handler deletes the session after the answer, so asserting on the last request
	// would assert on the teardown: look for the message call anywhere in the sequence.
	var sawMessage, sawDelete bool
	for _, path := range gotPaths {
		switch path {
		case "/session/ses_1/message":
			sawMessage = true
		case "/session/ses_1":
			sawDelete = true
		}
	}
	if !sawMessage {
		t.Fatalf("no message request in %v", gotPaths)
	}
	if !sawDelete {
		t.Fatalf("the session was left behind: %v", gotPaths)
	}
	if gotAuth != "Basic "+base64.StdEncoding.EncodeToString([]byte("opencode:pw")) {
		t.Fatal("the endpoint credentials were not used")
	}
	model, _ := gotBody["model"].(map[string]any)
	if model["providerID"] != "opencode" || model["modelID"] != "glm-5.3-flash-free" {
		t.Fatalf("model routing %+v", model)
	}
	if _, ok := gotBody["tools"]; !ok {
		t.Fatal("the adapter must keep runtime tools disabled")
	}
}

// TestFactoriesCoverThePolicyTable keeps the two registries of provider kinds from drifting: a kind
// with facts but no adapter would be configurable and then fail at the first request.
func TestFactoriesCoverThePolicyTable(t *testing.T) {
	factories := map[domain.ProviderKind]bool{}
	for _, kind := range RegisteredKinds() {
		factories[kind] = true
	}
	for _, kind := range domain.ProviderRegistrations() {
		if !factories[kind] {
			t.Errorf("kind %s has a policy row but no registered factory", kind)
		}
	}
	for _, kind := range RegisteredKinds() {
		if !domain.KnownProviderKind(kind) {
			t.Errorf("kind %s has a factory but no policy row", kind)
		}
	}
}
