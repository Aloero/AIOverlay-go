package engine

import (
	"context"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

type localSTTKeylessFixture struct{ calls int }

func (*localSTTKeylessFixture) ID() string { return "local" }
func (fixture *localSTTKeylessFixture) Stream(_ context.Context, request providers.Request, credential string) (<-chan providers.Event, <-chan error) {
	fixture.calls++
	events := make(chan providers.Event, 2)
	errors := make(chan error)
	if credential != "" {
		errors = make(chan error, 1)
		errors <- &providers.RequestError{Outcome: domain.OutcomeValidationFailed, Message: "local STT received a credential"}
		close(errors)
		close(events)
		return events, errors
	}
	events <- providers.Event{Type: providers.EventDelta, Text: "local transcript", ModelID: request.ModelID}
	events <- providers.Event{Type: providers.EventDone, ModelID: request.ModelID}
	close(events)
	close(errors)
	return events, errors
}

func TestLocalSTTProviderUsesKeylessEngineAdmission(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = []domain.ProviderConfig{{
		ID: "local", Kind: domain.ProviderLocalSTT, Enabled: true,
		CredentialPool: domain.CredentialPool{Policy: domain.CredentialSequential},
		LocalSTT:       &domain.LocalSTTProviderConfig{HelperPath: "helper", ModelPath: "model", ModelID: "local-model"},
	}}
	pool := domain.ModelPoolConfig{
		Task: "transcribe", ResultName: "audio.text", Policy: domain.PoolPriority,
		Models:       []domain.ModelSlot{{ID: "local", ProviderID: "local", ModelID: "local-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}},
		QualityFloor: 0, MaxParallel: 1, MaxCalls: 1,
	}
	pipeline := &cfg.Pipelines[0]
	pipeline.EntryNodeID = "stt"
	pipeline.Nodes = []domain.Node{{ID: "stt", Type: domain.NodeModelPool, ModelPool: &pool}, {ID: "output", Type: domain.NodeOutput}}
	pipeline.OutputNodeID = "output"
	pipeline.Edges = []domain.Edge{{ID: "done", From: "stt", To: "output", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}}

	registry, err := providers.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	fixture := &localSTTKeylessFixture{}
	registry.Set("local", fixture)
	engine := New(cfg, registry, nil, nil)
	result, err := engine.Execute(context.Background(), pipeline.ID, Input{Audio: &domain.AudioClip{PCM16: []int16{1, 2}, SampleRate: 16000, Channels: 1}}, RunOptions{})
	if err != nil || result.Text != "local transcript" || fixture.calls != 1 {
		t.Fatalf("result=%q calls=%d err=%v", result.Text, fixture.calls, err)
	}
}

func TestLocalSTTTraceIsLabeledLocal(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = []domain.ProviderConfig{{
		ID: "local", Kind: domain.ProviderLocalSTT, Enabled: true,
		CredentialPool: domain.CredentialPool{Policy: domain.CredentialSequential},
		LocalSTT:       &domain.LocalSTTProviderConfig{HelperPath: "helper", ModelPath: "model", ModelID: "local-model"},
	}}
	pool := domain.ModelPoolConfig{Task: "transcribe", ResultName: "audio.text", Policy: domain.PoolPriority, Models: []domain.ModelSlot{{ID: "local", ProviderID: "local", ModelID: "local-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}}, QualityFloor: 0, MaxParallel: 1, MaxCalls: 1}
	pipeline := &cfg.Pipelines[0]
	pipeline.EntryNodeID = "stt"
	pipeline.Nodes = []domain.Node{{ID: "stt", Type: domain.NodeModelPool, ModelPool: &pool}, {ID: "output", Type: domain.NodeOutput}}
	pipeline.OutputNodeID = "output"
	pipeline.Edges = []domain.Edge{{ID: "done", From: "stt", To: "output", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}}
	registry, err := providers.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	registry.Set("local", &localSTTKeylessFixture{})
	result, err := New(cfg, registry, nil, nil).Execute(context.Background(), pipeline.ID, Input{Audio: &domain.AudioClip{PCM16: []int16{1}, SampleRate: 16000, Channels: 1}}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range result.Trace.Events {
		if event.Kind == "attempt_start" && event.Attempt != nil {
			if event.Attempt.Transport != "Local STT" {
				t.Fatalf("transport=%q", event.Attempt.Transport)
			}
			return
		}
	}
	t.Fatal("local STT attempt_start missing")
}
