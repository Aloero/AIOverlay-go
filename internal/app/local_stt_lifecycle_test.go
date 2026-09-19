package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

type backgroundPauseFixture struct {
	app      *App
	observed []struct {
		pausedArg     bool
		sessionPaused bool
	}
}

func (*backgroundPauseFixture) ID() string { return "pause-fixture" }
func (*backgroundPauseFixture) Stream(context.Context, providers.Request, string) (<-chan providers.Event, <-chan error) {
	events := make(chan providers.Event)
	errors := make(chan error)
	close(events)
	close(errors)
	return events, errors
}
func (fixture *backgroundPauseFixture) SetBackgroundPaused(paused bool) {
	fixture.observed = append(fixture.observed, struct {
		pausedArg     bool
		sessionPaused bool
	}{pausedArg: paused, sessionPaused: fixture.app.BackgroundPaused()})
}

func TestPauseBackgroundOrdersSourceStopBeforeProviderUnloadAndResumeBeforeSourceStart(t *testing.T) {
	app := studioTestApp(t)
	fixture := &backgroundPauseFixture{app: app}
	app.registry.Set("pause-fixture", fixture)

	app.PauseBackground(true)
	if len(fixture.observed) != 1 || !fixture.observed[0].pausedArg || !fixture.observed[0].sessionPaused {
		t.Fatalf("pause ordering=%+v", fixture.observed)
	}
	app.PauseBackground(false)
	if len(fixture.observed) != 2 || fixture.observed[1].pausedArg || !fixture.observed[1].sessionPaused {
		t.Fatalf("resume provider notification must run before source resume: %+v", fixture.observed)
	}
	if app.BackgroundPaused() {
		t.Fatal("background remained paused after resume")
	}
}

func TestProviderHotSwapInheritsPausedLocalSTTResourceState(t *testing.T) {
	app := studioTestApp(t)
	app.PauseBackground(true)

	model := filepath.Join(t.TempDir(), "model.bin")
	if err := os.WriteFile(model, []byte("model"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := domain.ProviderConfig{
		ID: "local-paused", Label: "Local Paused", Kind: domain.ProviderLocalSTT, Enabled: true,
		CredentialPool:  domain.CredentialPool{Policy: domain.CredentialSequential},
		RequestDefaults: domain.RequestDefaults{ConnectTimeoutMS: 1000, FirstTokenTimeoutMS: 1000, ResponseTimeoutMS: 5000, StreamIdleTimeoutMS: 1000, MaxOutputTokens: 64},
		LocalSTT: &domain.LocalSTTProviderConfig{
			HelperPath: os.Args[0], ModelPath: model, ModelID: "local-model", ResourcePolicy: "unload_on_pause",
		},
	}
	if err := app.SaveProvider(provider); err != nil {
		t.Fatal(err)
	}
	var local *providers.LocalSTTResourceStatus
	for _, capability := range app.registry.Capabilities(app.GetConfig()) {
		if capability.ProviderID == provider.ID {
			local = capability.LocalSTT
			break
		}
	}
	if local == nil || !local.BackgroundPaused || local.ResourcePolicy != "unload_on_pause" || local.Warm || local.ActiveRequests != 0 {
		t.Fatalf("hot-swapped local STT status=%+v", local)
	}
}

func TestSafetySuspendPropagatesProviderPauseAndRequiresExplicitResume(t *testing.T) {
	app := studioTestApp(t)
	fixture := &backgroundPauseFixture{app: app}
	app.registry.Set("pause-fixture", fixture)

	app.SuspendBackground("windows_suspend")
	status, ok := app.SessionStatus().(map[string]any)
	if !ok || status["paused"] != true || status["pause_reason"] != "windows_suspend" {
		t.Fatalf("suspend status=%+v", app.SessionStatus())
	}
	if len(fixture.observed) != 1 || !fixture.observed[0].pausedArg || !fixture.observed[0].sessionPaused {
		t.Fatalf("provider suspend propagation=%+v", fixture.observed)
	}

	app.SuspendBackground("windows_session_lock")
	status = app.SessionStatus().(map[string]any)
	if status["pause_reason"] != "windows_session_lock" {
		t.Fatalf("repeated safety reason not updated: %+v", status)
	}
	if len(fixture.observed) != 2 || !fixture.observed[1].pausedArg {
		t.Fatalf("repeated safety pause provider events=%+v", fixture.observed)
	}

	app.PauseBackground(false)
	status = app.SessionStatus().(map[string]any)
	if status["paused"] != false || status["pause_reason"] != "" {
		t.Fatalf("explicit resume did not clear safety pause: %+v", status)
	}
	if len(fixture.observed) != 3 || fixture.observed[2].pausedArg || !fixture.observed[2].sessionPaused {
		t.Fatalf("provider resume ordering=%+v", fixture.observed)
	}
}
