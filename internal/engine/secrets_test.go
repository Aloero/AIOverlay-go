package engine

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	"context"
	"strings"
	"testing"
)

func TestSecretSplitAcrossDeltasNeverEntersUICallback(t *testing.T) {
	secret := "credential-example-not-real"
	e, cfg := testEngine(t, 1, &scriptedProvider{run: func(ctx context.Context, req providers.Request, key string, ch chan<- providers.Event) error {
		for _, s := range []string{"before ", secret[:9], secret[9:18], secret[18:], " after"} {
			ch <- providers.Event{Type: providers.EventDelta, Text: s}
		}
		return nil
	}})
	cfg.Providers[0].CredentialPool.Keys = []domain.Credential{{ID: "fixture", Secret: secret, Enabled: true, Label: "example"}}
	e.cfg = cfg
	var visible string
	result, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte{1}}, RunOptions{OnReset: func() { visible = "" }, OnDelta: func(s string) {
		visible += s
		if strings.Contains(visible, secret) || strings.Contains(visible, secret[:18]) {
			t.Error("credential prefix leaked to display")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "before [redacted] after" || visible != result.Text {
		t.Fatal(result.Text, visible)
	}
}
func TestDisabledPipelineCannotRunThroughExplicitID(t *testing.T) {
	e, cfg := testEngine(t, 1, &scriptedProvider{run: func(context.Context, providers.Request, string, chan<- providers.Event) error {
		t.Error("disabled pipeline reached provider")
		return nil
	}})
	cfg.Pipelines[0].Enabled = false
	e.cfg = cfg
	if _, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte{1}}, RunOptions{}); err == nil {
		t.Fatal("disabled pipeline executed")
	}
}
