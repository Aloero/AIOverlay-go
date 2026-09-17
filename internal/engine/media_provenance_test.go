package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func freshCapturePipeline() domain.Pipeline {
	decisionPool := domain.ModelPoolConfig{
		Policy: domain.PoolPriority, MaxParallel: 1, MaxCalls: 1,
		Models: []domain.ModelSlot{{ID: "decision-model", ProviderID: "test", ModelID: "a-image", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}},
	}
	answerPool := domain.ModelPoolConfig{
		Policy: domain.PoolPriority, MaxParallel: 1, MaxCalls: 1,
		Models: []domain.ModelSlot{{ID: "answer-model", ProviderID: "test", ModelID: "c-text", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}},
	}
	return domain.Pipeline{
		ID: "fresh-capture", Name: "Fresh capture", Enabled: true,
		EntryNodeID: "decision", OutputNodeID: "output",
		Budget:   domain.ExecutionBudget{MaxWallTimeMS: 5000, MaxModelCalls: 4, MaxParallelCalls: 1, MaxProviders: 1, MaxKeysPerProvider: 1, ConnectTimeoutMS: 1000, FirstTokenTimeoutMS: 1000, ResponseTimeoutMS: 2000, StreamIdleTimeoutMS: 1000},
		Commands: []domain.CommandConfig{{ID: "decide", Action: "screen_decide", EntryNodeID: "decision", Intent: domain.IntentDecide, CaptureScreen: true}},
		Nodes: []domain.Node{
			{ID: "decision", Name: "Decision", Type: domain.NodeDecision, ModelPool: &decisionPool},
			{ID: "capture", Name: "Fresh screen", Type: domain.NodeCapture},
			{ID: "answer", Name: "Answer", Type: domain.NodeModelPool, ModelPool: &answerPool},
			{ID: "output", Name: "Output", Type: domain.NodeOutput},
		},
		Edges: []domain.Edge{
			{ID: "need-screen", From: "decision", To: "capture", On: []domain.Outcome{domain.OutcomeNeedScreen}, MaxVisits: 1},
			{ID: "captured", From: "capture", To: "answer", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1},
			{ID: "answered", From: "answer", To: "output", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1},
		},
	}
}

func TestFreshCaptureRemainsRequiredForAnswerPlanning(t *testing.T) {
	pipeline := freshCapturePipeline()
	input := DescribePoolInput(pipeline, "answer")
	if !input.Reachable || !input.ImageRequired {
		t.Fatalf("fresh capture was treated as already resolved: %+v", input)
	}
}

func TestFreshCaptureIsNotDroppedForTextOnlyAnswer(t *testing.T) {
	catalog := selectionCatalog(t)
	cfg := selectionConfig()
	cfg.Pipelines = []domain.Pipeline{freshCapturePipeline()}
	cfg.ActivePipelineID = "fresh-capture"

	var decisionCalls, answerCalls atomic.Int32
	provider := &scriptedProvider{run: func(_ context.Context, request providers.Request, _ string, events chan<- providers.Event) error {
		switch request.ModelID {
		case "a-image":
			decisionCalls.Add(1)
			if string(request.ImageJPEG) != "original" {
				return fmt.Errorf("decision lost original screen")
			}
			events <- providers.Event{Type: providers.EventDelta, Text: `{"action":"need_screen"}`}
			return nil
		case "c-text":
			answerCalls.Add(1)
			return fmt.Errorf("text-only answer must not be called without vision preprocessing")
		default:
			return fmt.Errorf("unexpected model %s", request.ModelID)
		}
	}}
	registry, err := providers.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	registry.Set("test", provider)
	t.Cleanup(registry.Close)

	engine := New(cfg, registry, catalog, nil)
	_, err = engine.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("original"), Intent: string(domain.IntentDecide)}, RunOptions{
		Capture: func(context.Context) ([]byte, error) { return []byte("fresh"), nil },
	})
	if err == nil {
		t.Fatal("fresh screen silently disappeared before text-only answer")
	}
	if decisionCalls.Load() != 1 || answerCalls.Load() != 0 {
		t.Fatalf("decision=%d answer=%d err=%v", decisionCalls.Load(), answerCalls.Load(), err)
	}
}
