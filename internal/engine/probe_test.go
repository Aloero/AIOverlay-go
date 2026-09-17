package engine

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	"context"
	"testing"
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
