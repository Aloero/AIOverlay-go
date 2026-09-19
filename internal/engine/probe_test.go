package engine

import (
	"context"
	"strings"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func TestExplicitConnectionTestUsesOneKeyAndNoImage(t *testing.T) {
	calls := 0
	script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, k string, ch chan<- providers.Event) error {
		calls++
		if len(r.ImageJPEG) != 0 || r.MaxOutputTokens != 32 || k != "synthetic-key" {
			t.Errorf("incorrect test payload %+v", r)
		}
		ch <- providers.Event{Type: providers.EventDelta, Text: "OK"}
		return nil
	}}
	e, c := testEngine(t, 1, script)
	c.Providers[0].CredentialPool.Keys = []domain.Credential{{ID: "test", Secret: "synthetic-key", Enabled: true}}
	e.Reconfigure(c, e.registry)
	result, err := e.Probe(context.Background(), ConnectionProbe{ProviderID: "opencode", ModelID: "0", CredentialID: "test", Confirm: true}, RunOptions{})
	if err != nil || calls != 1 || result.Trace.Purpose != "connection_test" || e.cfg.ActivePipelineID != c.ActivePipelineID {
		t.Fatal(result, err, calls)
	}
	_, err = e.Probe(context.Background(), ConnectionProbe{ProviderID: "opencode", ModelID: "0", CredentialID: "test"}, RunOptions{})
	if err == nil || calls != 1 {
		t.Fatal("unconfirmed probe made a call")
	}
	states := e.CredentialStates()
	if len(states) != 1 || states[0].Outcome != domain.OutcomeSuccess {
		t.Fatal(states)
	}
}

func TestConnectionProbeRejectsSessionAndLocalTaskTransports(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = append(cfg.Providers,
		domain.ProviderConfig{ID: "local", Kind: domain.ProviderLocalSTT, Enabled: true, LocalSTT: &domain.LocalSTTProviderConfig{HelperPath: "helper", ModelPath: "model", ModelID: "local-model"}},
		domain.ProviderConfig{ID: "live", Kind: domain.ProviderOpenAILive, Enabled: true, BaseURL: "wss://api.openai.com/v1/live/sessions", CredentialPool: domain.CredentialPool{Policy: domain.CredentialSequential, Keys: []domain.Credential{{ID: "k", Secret: "test", Enabled: true}}}},
	)
	registry, err := providers.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	engine := New(cfg, registry, nil, nil)
	for _, test := range []struct {
		provider string
		model    string
		key      string
		contains string
	}{
		{provider: "local", model: "local-model", contains: "audio transcription preview"},
		{provider: "live", model: "gpt-live-1", key: "k", contains: "persistent Live source"},
	} {
		_, err := engine.Probe(context.Background(), ConnectionProbe{ProviderID: test.provider, ModelID: test.model, CredentialID: test.key, Confirm: true}, RunOptions{})
		if err == nil || !strings.Contains(err.Error(), test.contains) {
			t.Fatalf("provider %s probe error=%v", test.provider, err)
		}
	}
}
