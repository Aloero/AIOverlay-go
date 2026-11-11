package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	opencodehost "aioverlay/internal/modelhost/opencode"

	"aioverlay/internal/domain"
	"aioverlay/internal/modelhost"
)

// EmbeddedOpenCode is the protocol adapter for the OpenCode HTTP API: it builds one request,
// parses one response and maps failures onto domain outcomes. It has no idea whether the runtime is
// a child process started next to this executable or a service someone else supervises - that is
// modelhost's job, which is why upgrading or replacing the runtime does not touch this file.
type EmbeddedOpenCode struct {
	cfg   domain.ProviderConfig
	proxy ProxyResolver
	host  modelhost.Host
}

func NewEmbeddedOpenCode(cfg domain.ProviderConfig, resolver ...ProxyResolver) *EmbeddedOpenCode {
	var proxy ProxyResolver
	if len(resolver) > 0 {
		proxy = resolver[0]
	}
	return newEmbeddedOpenCode(cfg, Deps{Proxy: proxy})
}

func init() {
	Register(Factory{
		Kind: domain.ProviderEmbeddedOpenCode,
		New: func(cfg domain.ProviderConfig, deps Deps) (Provider, MediaCapabilities, error) {
			adapter := newEmbeddedOpenCode(cfg, deps)
			return adapter, adapter.Capabilities(), nil
		},
	})
}

// Capabilities is this adapter's own declaration: the OpenCode HTTP API as the app uses it carries a
// screenshot plus text and answers in text.
func (p *EmbeddedOpenCode) Capabilities() MediaCapabilities { return Media(p.cfg) }

// newEmbeddedOpenCode wires the adapter to a model host. Deps.HostFactory replaces that host without
// touching this adapter: that is the whole point of the boundary, and what makes "upgrade the
// bundled runtime" or "point the same protocol at another engine" a wiring change.
func newEmbeddedOpenCode(cfg domain.ProviderConfig, deps Deps) *EmbeddedOpenCode {
	hostConfig := opencodehost.Config{
		ProviderID:    openCodeProviderID(cfg),
		UpstreamURL:   cfg.BaseURL,
		CredentialEnv: cfg.CredentialEnv,
	}
	return &EmbeddedOpenCode{
		cfg:   cfg,
		proxy: deps.Proxy,
		host:  deps.host(cfg, func() modelhost.Host { return opencodehost.New(hostConfig) }),
	}
}

func (p *EmbeddedOpenCode) ID() string { return p.cfg.ID }

// Incremental reports that this transport delivers the answer as one body, not as tokens. The
// scheduler uses it to decide whether a first-token deadline is meaningful at all.
func (p *EmbeddedOpenCode) Incremental() bool { return false }

// Host exposes the model host behind this connection, so the capability table and the Studio
// "what is actually running" panel ask the host instead of guessing from the provider kind.
func (p *EmbeddedOpenCode) Host() modelhost.Host { return p.host }

func (p *EmbeddedOpenCode) Stream(ctx context.Context, r Request, credential string) (<-chan Event, <-chan error) {
	events := make(chan Event, 4)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		events <- Event{Type: EventStart, ModelID: r.ModelID}
		text, err := p.run(ctx, r, credential)
		if err != nil {
			errs <- err
			return
		}
		events <- Event{Type: EventFirstToken, ModelID: r.ModelID}
		events <- Event{Type: EventDelta, Text: text, ModelID: r.ModelID}
		events <- Event{Type: EventDone, ModelID: r.ModelID}
	}()
	return events, errs
}

func (p *EmbeddedOpenCode) run(ctx context.Context, r Request, credential string) (string, error) {
	providerID, modelID := splitOpenCodeRef(r.ModelID, openCodeProviderID(p.cfg))
	expected := openCodeProviderID(p.cfg)
	if providerID != expected {
		return "", &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Model provider does not match the configured OpenCode connection"}
	}
	upstreamProxy := ""
	var err error
	if p.proxy != nil {
		upstreamProxy, err = p.proxy.ProxyURL(ctx)
		if err != nil {
			return "", &RequestError{Outcome: domain.OutcomeNetworkError, Message: "AI VPN unavailable: " + err.Error(), Retryable: true}
		}
	}
	if upstreamProxy == "" {
		// The runtime is the only caller here and it always needs an egress: this host runs in
		// countries and networks where AAAA answers exist but do not connect, so the child is
		// pointed at an IPv4-forcing loopback proxy rather than left to retry.
		upstreamProxy, err = ipv4ProxyURL()
		if err != nil {
			return "", fmt.Errorf("IPv4 proxy: %w", err)
		}
	}
	// Everything that changes how the runtime must be started goes into Need; the host decides
	// which of it makes two instances incompatible, and reuses a warm process when nothing changed.
	need := modelhost.Need{Credential: credential, Proxy: upstreamProxy, OutputTokenCap: r.MaxOutputTokens}
	endpoint, release, err := p.host.Endpoint(ctx, need)
	if err != nil {
		return "", hostFailure(err)
	}
	defer release()

	sid, err := p.createSession(ctx, endpoint)
	if err != nil {
		return "", err
	}
	// Delete the session after each request. This does not promise the third-party runtime never
	// writes session data, but prevents unbounded screenshot history.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if ctx.Err() != nil {
			if _, _, err := endpoint.DoJSON(cleanup, http.MethodPost, "/session/"+sid+"/abort", nil, nil, time.Second); err != nil {
				log.Print("OpenCode session abort was not acknowledged")
			}
		}
		if _, _, err := endpoint.DoJSON(cleanup, http.MethodDelete, "/session/"+sid, nil, nil, time.Second); err != nil {
			log.Print("OpenCode session deletion was not acknowledged")
		}
	}()

	parts := []any{map[string]any{"type": "text", "text": r.UserPrompt}}
	if len(r.ImageJPEG) > 0 {
		parts = append(parts, map[string]any{"type": "file", "mime": "image/jpeg", "filename": "screen.jpg", "url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(r.ImageJPEG)})
	}
	payload := map[string]any{"model": map[string]any{"providerID": providerID, "modelID": modelID}, "system": r.SystemPrompt, "parts": parts, "tools": map[string]bool{"*": false}}
	var resp struct {
		Info struct {
			Error json.RawMessage `json:"error"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	status, raw, err := endpoint.DoJSON(ctx, http.MethodPost, "/session/"+sid+"/message", payload, &resp, r.ResponseTimeout)
	if err != nil {
		return "", endpointError(err)
	}
	if status/100 != 2 {
		return "", HTTPError(status, errorBody(raw))
	}
	if len(resp.Info.Error) > 0 && string(resp.Info.Error) != "null" {
		return "", openCodeError(resp.Info.Error)
	}
	var out strings.Builder
	for _, part := range resp.Parts {
		if part.Type == "text" {
			out.WriteString(part.Text)
		}
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "OpenCode returned empty response"}
	}
	return strings.TrimSpace(out.String()), nil
}

func (p *EmbeddedOpenCode) createSession(ctx context.Context, endpoint modelhost.Endpoint) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	status, raw, err := endpoint.DoJSON(ctx, http.MethodPost, "/session", map[string]any{"title": "AIOverlay-go"}, &out, 5*time.Second)
	if err != nil {
		return "", endpointError(err)
	}
	if status/100 != 2 {
		return "", HTTPError(status, errorBody(raw))
	}
	if out.ID == "" {
		return "", &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "OpenCode session id missing"}
	}
	return out.ID, nil
}

// splitOpenCodeRef accepts both "provider/model" and a bare model id, because the catalog names
// models by reference while a connection already knows its own provider.
func splitOpenCodeRef(ref, fallbackProvider string) (string, string) {
	ref = strings.TrimSpace(ref)
	if i := strings.Index(ref, "/"); i > 0 {
		return ref[:i], ref[i+1:]
	}
	if fallbackProvider == "" {
		fallbackProvider = "opencode"
	}
	return fallbackProvider, ref
}

// openCodeProviderID is the upstream provider this connection serves inside the runtime. It stays a
// protocol detail of OpenCode's config document rather than a field every provider has to carry.
func openCodeProviderID(cfg domain.ProviderConfig) string { return cfg.GatewayProviderID() }

// hostFailure maps a model host error onto an outcome. Startup problems are retryable by nature;
// anything else from the host layer keeps its own message, which never contains a key.
func hostFailure(err error) error {
	var requestError *RequestError
	if errors.As(err, &requestError) {
		return requestError
	}
	if errors.Is(err, modelhost.ErrStartupTimeout) {
		return &RequestError{
			Outcome:   domain.OutcomeProviderUnavailable,
			Message:   "embedded OpenCode did not become healthy within " + modelhost.StartupTimeout.String(),
			Retryable: true,
		}
	}
	return &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: err.Error(), Retryable: true}
}

// endpointError separates "the host answered, but not with what we can use" from "we never got an
// answer", which the two need to report as different outcomes.
func endpointError(err error) error {
	if errors.Is(err, modelhost.ErrInvalidResponse) {
		return &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "Invalid OpenCode JSON response"}
	}
	return networkError(err)
}

// errorBody bounds the upstream text that reaches HTTPError. Only documented codes are read from
// it; the body itself is never shown, so a large or hostile response cannot inflate a trace.
func errorBody(raw []byte) string {
	const limit = 4096
	if len(raw) > limit {
		return string(raw[:limit])
	}
	return string(raw)
}
