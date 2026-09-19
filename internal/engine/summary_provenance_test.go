package engine

import (
	"context"
	"sync/atomic"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

type summaryProvider struct{ calls atomic.Int32 }

func (*summaryProvider) ID() string { return "opencode" }
func (provider *summaryProvider) Stream(_ context.Context, request providers.Request, _ string) (<-chan providers.Event, <-chan error) {
	provider.calls.Add(1)
	events := make(chan providers.Event, 2)
	errors := make(chan error)
	events <- providers.Event{Type: providers.EventDelta, Text: "compressed facts", ModelID: request.ModelID}
	events <- providers.Event{Type: providers.EventDone, ModelID: request.ModelID}
	close(events)
	close(errors)
	return events, errors
}

func summaryPipeline(t *testing.T) (domain.Config, *summaryProvider) {
	cfg := config.Default()
	p := &cfg.Pipelines[0]
	p.Contexts = []domain.ContextConfig{{ID: "conversation", Name: "Conversation", Enabled: true, MaxAgeS: 60, MaxEntries: 10, MaxChars: 1000, IncludeTranscript: true, ScreenMaxAgeS: 60}}
	pool := *p.Nodes[1].ModelPool
	pool.Models = []domain.ModelSlot{{ID: "summary", ProviderID: "opencode", ModelID: "summary-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}}
	pool.ResultName = "context.summary"
	pool.Prompt.User = "Summarize {{context.text}}"
	p.Nodes[1].ModelPool = &pool
	p.Nodes[1].ContextMode = "named"
	p.Nodes[1].ContextID = "conversation"
	provider := &summaryProvider{}
	return cfg, provider
}

func TestSummaryStageRefusesInferenceWithoutSourceEntryIDs(t *testing.T) {
	cfg, provider := summaryPipeline(t)
	registry, _ := providers.NewRegistry(cfg)
	registry.Set("opencode", provider)
	defer registry.Close()
	engine := New(cfg, registry, nil, nil)
	_, err := engine.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("screen"), ImagePinned: true, ContextViews: map[string]ContextView{"conversation": {Text: "facts"}}, DefaultContextID: "conversation"}, RunOptions{})
	if err == nil || provider.calls.Load() != 0 {
		t.Fatalf("summary without provenance dispatched: calls=%d err=%v", provider.calls.Load(), err)
	}
}

func TestSummaryObservationCarriesContextAndDerivedEntries(t *testing.T) {
	cfg, provider := summaryPipeline(t)
	registry, _ := providers.NewRegistry(cfg)
	registry.Set("opencode", provider)
	defer registry.Close()
	engine := New(cfg, registry, nil, nil)
	var observation Observation
	_, err := engine.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("screen"), ImagePinned: true, ContextViews: map[string]ContextView{"conversation": {Text: "facts", EntryIDs: []string{"entry-a", "entry-b"}}}, DefaultContextID: "conversation"}, RunOptions{OnObservation: func(value Observation) {
		if value.Kind == "context.summary" {
			observation = value
		}
	}})
	if err == nil {
		// A summary result is data, not a publishable answer; reaching Output is intentionally invalid.
		t.Fatal("summary stage unexpectedly became a user answer")
	}
	if provider.calls.Load() != 1 || observation.ContextID != "conversation" || len(observation.DerivedFrom) != 2 || observation.DerivedFrom[0] != "entry-a" {
		t.Fatalf("summary observation=%+v calls=%d", observation, provider.calls.Load())
	}
}
