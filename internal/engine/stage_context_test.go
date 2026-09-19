package engine

import (
	"context"
	"fmt"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

type stageContextProvider struct {
	requests []providers.Request
}

func (*stageContextProvider) ID() string { return "opencode" }
func (provider *stageContextProvider) Stream(_ context.Context, request providers.Request, _ string) (<-chan providers.Event, <-chan error) {
	provider.requests = append(provider.requests, request)
	events := make(chan providers.Event, 2)
	errors := make(chan error)
	events <- providers.Event{Type: providers.EventDelta, Text: request.ModelID + "-ok", ModelID: request.ModelID}
	events <- providers.Event{Type: providers.EventDone, ModelID: request.ModelID}
	close(events)
	close(errors)
	return events, errors
}

func stageContextPipeline() (domain.Config, domain.Pipeline) {
	cfg := config.Default()
	pipeline := cfg.Pipelines[0]
	pipeline.ID = "stage-context"
	pipeline.EntryNodeID = "first"
	pipeline.OutputNodeID = "output"
	pipeline.Contexts = []domain.ContextConfig{
		{ID: "decision-context", Name: "Decision", Enabled: true, MaxAgeS: 60, MaxEntries: 10, MaxChars: 1000, KeepLatestScreen: true, ScreenMaxAgeS: 60},
		{ID: "answer-context", Name: "Answer", Enabled: true, MaxAgeS: 60, MaxEntries: 10, MaxChars: 1000, KeepLatestScreen: true, ScreenMaxAgeS: 60},
	}
	firstPool := *pipeline.Nodes[1].ModelPool
	firstPool.Models = []domain.ModelSlot{{ID: "first-slot", ProviderID: "opencode", ModelID: "first-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}}
	firstPool.Prompt.User = "context={{context.text}}"
	secondPool := firstPool
	secondPool.Models = []domain.ModelSlot{{ID: "second-slot", ProviderID: "opencode", ModelID: "second-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}}
	pipeline.Nodes = []domain.Node{
		{ID: "first", Type: domain.NodeModelPool, Name: "First", ContextID: "decision-context", ModelPool: &firstPool},
		{ID: "second", Type: domain.NodeModelPool, Name: "Second", ContextID: "answer-context", ModelPool: &secondPool},
		{ID: "output", Type: domain.NodeOutput, Name: "Output"},
	}
	pipeline.Edges = []domain.Edge{
		{ID: "first-second", From: "first", To: "second", On: []domain.Outcome{domain.OutcomeSuccess}, Priority: 10, MaxVisits: 1},
		{ID: "second-output", From: "second", To: "output", On: []domain.Outcome{domain.OutcomeSuccess}, Priority: 10, MaxVisits: 1},
	}
	cfg.Pipelines = []domain.Pipeline{pipeline}
	cfg.ActivePipelineID = pipeline.ID
	return cfg, pipeline
}

func TestAIStagesSelectDifferentImmutableContexts(t *testing.T) {
	cfg, pipeline := stageContextPipeline()
	registry, err := providers.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	provider := &stageContextProvider{}
	registry.Set("opencode", provider)
	defer registry.Close()
	engine := New(cfg, registry, nil, nil)
	input := Input{ContextText: "DEFAULT", ContextViews: map[string]ContextView{
		"decision-context": {Text: "DECISION_ONLY", ImageJPEG: []byte("decision-image")},
		"answer-context":   {Text: "ANSWER_ONLY", ImageJPEG: []byte("answer-image")},
	}}
	result, err := engine.Execute(context.Background(), pipeline.ID, input, RunOptions{})
	if err != nil || result.Text != "second-model-ok" {
		t.Fatalf("result=%q err=%v", result.Text, err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("requests=%d", len(provider.requests))
	}
	want := []struct{ context, image string }{{"DECISION_ONLY", "decision-image"}, {"ANSWER_ONLY", "answer-image"}}
	for i, request := range provider.requests {
		if request.UserPrompt != fmt.Sprintf("context=%s", want[i].context) || string(request.ImageJPEG) != want[i].image {
			t.Fatalf("request %d prompt=%q image=%q", i, request.UserPrompt, request.ImageJPEG)
		}
	}
}

func TestPinnedManualImageOverridesStageContextImage(t *testing.T) {
	cfg, pipeline := stageContextPipeline()
	pipeline.Nodes = []domain.Node{pipeline.Nodes[0], {ID: "output", Type: domain.NodeOutput, Name: "Output"}}
	pipeline.OutputNodeID = "output"
	pipeline.Edges = []domain.Edge{{ID: "done", From: "first", To: "output", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}}
	cfg.Pipelines[0] = pipeline
	registry, _ := providers.NewRegistry(cfg)
	provider := &stageContextProvider{}
	registry.Set("opencode", provider)
	defer registry.Close()
	engine := New(cfg, registry, nil, nil)
	_, err := engine.Execute(context.Background(), pipeline.ID, Input{
		ImageJPEG: []byte("manual-pinned"), ImagePinned: true,
		ContextViews: map[string]ContextView{"decision-context": {Text: "stage", ImageJPEG: []byte("context-image")}},
	}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 || string(provider.requests[0].ImageJPEG) != "manual-pinned" {
		t.Fatalf("pinned image replaced: %+v", provider.requests)
	}
}

func TestPoolPlannerIncludesExplicitStageContextMedia(t *testing.T) {
	_, pipeline := stageContextPipeline()
	input := DescribePoolInput(pipeline, "first")
	if !input.Reachable || !input.TextAvailable || !input.ImageRequired {
		t.Fatalf("stage context missing from planner: %+v", input)
	}
}
