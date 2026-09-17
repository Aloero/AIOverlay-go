package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func TestStudioReviewTextCatalogAfterVisionStage(t *testing.T) {
	cfg, cat := selectionConfig(), selectionCatalog(t)
	p := &cfg.Pipelines[0]
	vision := p.Nodes[1]
	vision.ID = "vision"
	vision.ModelPool.Selection = &domain.CatalogSelection{Mode: "manual", Modality: "image"}
	vision.ModelPool.Models = []domain.ModelSlot{{ID: "vision-slot", Enabled: true, ProviderID: "test", ModelID: "a-image", QualityScore: 90}}
	solverPool := *vision.ModelPool
	solverPool.Selection = &domain.CatalogSelection{Mode: "catalog", Modality: "text", FreeOnly: true}
	solverPool.Models = nil
	p.Nodes = []domain.Node{{ID: "screen", Type: domain.NodeScreen}, vision, {ID: "solver", Type: domain.NodeModelPool, ModelPool: &solverPool}, {ID: "output", Type: domain.NodeOutput}}
	p.EntryNodeID = "screen"
	p.OutputNodeID = "output"
	p.Edges = []domain.Edge{{ID: "v", From: "screen", To: "vision", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}, {ID: "s", From: "vision", To: "solver", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}, {ID: "o", From: "solver", To: "output", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}}
	reg, _ := providers.NewRegistry(cfg)
	t.Cleanup(reg.Close)
	script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		if r.ModelID == "a-image" {
			ch <- providers.Event{Type: providers.EventDelta, Text: "extracted"}
			return nil
		}
		if r.ModelID != "c-text" || len(r.ImageJPEG) > 0 {
			t.Errorf("wrong solver payload model=%s bytes=%d", r.ModelID, len(r.ImageJPEG))
		}
		ch <- providers.Event{Type: providers.EventDelta, Text: "final"}
		return nil
	}}
	reg.Set("test", script)
	got, err := New(cfg, reg, cat, nil).Execute(context.Background(), p.ID, Input{ImageJPEG: []byte("frame")}, RunOptions{})
	if err != nil || got.Text != "final" {
		t.Fatalf("text-only catalog after successful vision failed: %v; text=%q", err, got.Text)
	}
	simulated, simErr := Simulate(context.Background(), SimulationRequest{Config: cfg, PipelineID: p.ID}, cat)
	if simErr != nil || simulated.Error != "" || simulated.Calls != 2 || simulated.Trace.Winner == nil || simulated.Trace.Winner.ModelID != "c-text" {
		t.Fatalf("simulation disagrees with execution: error=%v run=%s calls=%d winner=%v", simErr, simulated.Error, simulated.Calls, simulated.Trace.Winner)
	}
}
func TestStudioReviewFailedValidatorHasTerminalOutcome(t *testing.T) {
	e, cfg := testEngine(t, 1, &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		ch <- providers.Event{Type: providers.EventDelta, Text: "bad"}
		return nil
	}})
	p := &cfg.Pipelines[0]
	poolID := p.Nodes[1].ID
	p.Nodes = append(p.Nodes, domain.Node{ID: "validate", Type: domain.NodeValidator, Validator: &domain.ValidatorConfig{MinLength: 20}})
	for i := range p.Edges {
		if p.Edges[i].From == poolID {
			p.Edges[i].To = "validate"
		}
	}
	p.Edges = append(p.Edges, domain.Edge{ID: "valid-output", From: "validate", To: p.OutputNodeID, On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1})
	e.Reconfigure(cfg, e.registry)
	got, err := e.Execute(context.Background(), p.ID, Input{ImageJPEG: []byte("frame")}, RunOptions{})
	if err == nil {
		t.Fatal("expected validator rejection")
	}
	raw, _ := json.Marshal(got.Trace)
	var tr map[string]any
	json.Unmarshal(raw, &tr)
	if tr["outcome"] != "validation_failed" || tr["failure_node_id"] != "validate" || got.Trace.Winner != nil {
		t.Fatalf("failed graph looks successful in history: outcome=%v failure_node=%v winner=%v", tr["outcome"], tr["failure_node_id"], got.Trace.Winner)
	}
}
func TestStudioReviewStructuredUntriedCandidates(t *testing.T) {
	cfg, cat := selectionConfig(), selectionCatalog(t)
	cfg.Pipelines[0].Budget.MaxModelCalls = 1
	reg, _ := providers.NewRegistry(cfg)
	t.Cleanup(reg.Close)
	reg.Set("test", &scriptedProvider{run: func(context.Context, providers.Request, string, chan<- providers.Event) error {
		return &providers.RequestError{Outcome: domain.OutcomeModelUnavailable, Message: "fixture"}
	}})
	got, err := New(cfg, reg, cat, nil).Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{})
	if err == nil {
		t.Fatal("expected exhausted budget")
	}
	raw, _ := json.Marshal(got.Trace)
	var tr struct {
		Events []struct {
			Kind       string `json:"kind"`
			Untried    int    `json:"untried_models"`
			Candidates int    `json:"candidate_count"`
		}
	}
	json.Unmarshal(raw, &tr)
	for _, ev := range tr.Events {
		if ev.Kind == "pool_end" && ev.Candidates == 2 && ev.Untried == 1 {
			return
		}
	}
	t.Fatal("no structured summary of models left untried after budget exhaustion")
}

func TestStudioReviewInputContextAndSimulationAgree(t *testing.T) {
	c := simConfig()
	p := &c.Pipelines[0]
	n := p.Nodes[1]
	n.ID = "vision"
	solver := n
	solver.ID = "solver"
	mp := *n.ModelPool
	solver.ModelPool = &mp
	p.Nodes = []domain.Node{{ID: "screen", Type: domain.NodeScreen}, n, solver, {ID: "output", Type: domain.NodeOutput}}
	p.EntryNodeID = "screen"
	p.OutputNodeID = "output"
	p.Edges = []domain.Edge{{ID: "1", From: "screen", To: "vision", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}, {ID: "2", From: "vision", To: "solver", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}, {ID: "3", From: "solver", To: "output", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}}
	ctx := DescribePoolInput(*p, "solver")
	if ctx.ImageRequired || !ctx.TextAvailable || !ctx.Reachable {
		t.Fatal(ctx)
	}
	p.Edges = append(p.Edges, domain.Edge{ID: "fallback", From: "vision", To: "solver", On: []domain.Outcome{domain.OutcomeAnyError}, MaxVisits: 1})
	ctx = DescribePoolInput(*p, "solver")
	if !ctx.Mixed || !ctx.ImageRequired {
		t.Fatal("error path lost screenshot context", ctx)
	}
	solver.ModelPool.VisionPreprocessor = &domain.ModelPoolConfig{}
	ctx = DescribePoolInput(*p, "solver")
	if ctx.ImageRequired || ctx.Source != "solver/vision" {
		t.Fatal("composite solver needs extracted text", ctx)
	}
	ctx = DescribePoolInput(*p, "solver/vision")
	if !ctx.ImageRequired {
		t.Fatal("vision stage lost original path", ctx)
	}
}

func TestStudioReviewEveryOrderAndDispatchCombination(t *testing.T) {
	for _, order := range []domain.PoolPolicy{domain.PoolPriority, domain.PoolRoundRobin, domain.PoolWeighted, domain.PoolFastest} {
		for _, dispatch := range []domain.DispatchPolicy{domain.DispatchSequential, domain.DispatchParallel, domain.DispatchStaggered} {
			t.Run(string(order)+"/"+string(dispatch), func(t *testing.T) {
				var active, peak atomic.Int32
				script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
					n := active.Add(1)
					defer active.Add(-1)
					for {
						p := peak.Load()
						if n <= p || peak.CompareAndSwap(p, n) {
							break
						}
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(4 * time.Millisecond):
					}
					ch <- providers.Event{Type: providers.EventDelta, Text: "answer"}
					return nil
				}}
				e, cfg := testEngine(t, 3, script)
				pool := cfg.Pipelines[0].Nodes[1].ModelPool
				pool.Order = order
				pool.Dispatch = dispatch
				pool.MaxParallel = 2
				pool.HedgeDelayMS = 1
				cfg.Pipelines[0].Budget.MaxParallelCalls = 2
				e.Reconfigure(cfg, e.registry)
				got, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{})
				if err != nil || got.Text != "answer" || active.Load() != 0 {
					t.Fatal(got.Text, err, active.Load())
				}
				cap := int32(2)
				if dispatch == domain.DispatchSequential {
					cap = 1
				}
				if peak.Load() > cap {
					t.Fatal("parallel budget exceeded", peak.Load())
				}
			})
		}
	}
}
func TestStudioReviewPromptSnapshotIsRedactedNotInTrace(t *testing.T) {
	e, cfg := testEngine(t, 1, &scriptedProvider{run: func(_ context.Context, _ providers.Request, _ string, ch chan<- providers.Event) error {
		ch <- providers.Event{Type: providers.EventDelta, Text: "answer"}
		return nil
	}})
	cfg.Providers[0].CredentialPool.Keys = []domain.Credential{{ID: "k", Enabled: true, Secret: "SENSITIVE_FIXTURE_KEY"}}
	e.Reconfigure(cfg, e.registry)
	var snapshot PromptSnapshot
	got, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame"), Text: "PRIVATE_TASK SENSITIVE_FIXTURE_KEY"}, RunOptions{OnPrompt: func(p PromptSnapshot) { snapshot = p }})
	if err != nil || !snapshot.ImageAttached || strings.Contains(snapshot.Values.InputText, "SENSITIVE_FIXTURE_KEY") {
		t.Fatal("snapshot was not bounded/redacted", err)
	}
	raw, _ := json.Marshal(got.Trace)
	if bytes.Contains(raw, []byte("PRIVATE_TASK")) {
		t.Fatal("automatic trace exposed prompt input")
	}
}

func TestStudioReviewWeightedPreviewDoesNotAdvanceRuntimeRandomness(t *testing.T) {
	e, cfg := testEngine(t, 3, &scriptedProvider{})
	e.random = rand.New(rand.NewPCG(3, 7))
	expected := rand.New(rand.NewPCG(3, 7))
	pool := cfg.Pipelines[0].Nodes[1].ModelPool
	pool.Order = domain.PoolWeighted
	for i := 0; i < 10; i++ {
		e.PreviewOrder("preview", *pool, pool.Models)
	}
	if e.random.Uint64() != expected.Uint64() {
		t.Fatal("preview changed live weighted choice state")
	}
}

func TestDecisionNoActionIsNormalTerminalAndDoesNotRunAnswer(t *testing.T) {
	e, cfg := testEngine(t, 1, &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		ch <- providers.Event{Type: providers.EventDelta, Text: `{"action":"no_action"}`}
		return nil
	}})
	p := &cfg.Pipelines[0]
	answer := p.Nodes[1]
	decisionPool := *answer.ModelPool
	decisionPool.Prompt = domain.PromptConfig{User: "Decide"}
	decision := domain.Node{ID: "decision", Type: domain.NodeDecision, Name: "Need response?", ModelPool: &decisionPool}
	p.Nodes = append(p.Nodes, decision)
	p.Edges = append(p.Edges, domain.Edge{ID: "decision-answer", From: "decision", To: answer.ID, On: []domain.Outcome{domain.OutcomeRespond}, MaxVisits: 1})
	e.Reconfigure(cfg, e.registry)
	var calls atomic.Int32
	e.registry.Set("opencode", &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		calls.Add(1)
		ch <- providers.Event{Type: providers.EventDelta, Text: `{"action":"no_action"}`}
		return nil
	}})
	got, err := e.Execute(context.Background(), p.ID, Input{EntryNodeID: "decision", Intent: string(domain.IntentDecide), Text: "hello"}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Published || got.Text != "" || got.Trace.Outcome != domain.OutcomeNoAction || calls.Load() != 1 {
		t.Fatalf("published=%v text=%q outcome=%s calls=%d", got.Published, got.Text, got.Trace.Outcome, calls.Load())
	}
}

func TestSelectedContextIsIncludedWithoutMagicTemplateVariable(t *testing.T) {
	p := domain.PromptConfig{System: "system", User: "Answer the screenshot: {{input.text}}"}
	_, user := RenderRequestPrompt(p, PromptValues{InputText: "task", ContextText: "older conversation"})
	if !strings.Contains(user, "older conversation") || strings.Count(user, "older conversation") != 1 {
		t.Fatalf("%q", user)
	}
	p.User = "Context={{context.text}}\nTask={{input.text}}"
	_, user = RenderRequestPrompt(p, PromptValues{InputText: "task", ContextText: "older conversation"})
	if strings.Count(user, "older conversation") != 1 {
		t.Fatalf("duplicate context: %q", user)
	}
}

func TestTranscriptObservationCarriesFinalRevisionMetadata(t *testing.T) {
	var observed Observation
	runner := &runner{
		input: Input{EventID: "event", Audio: &domain.AudioClip{SegmentID: "segment"}},
		opt:   RunOptions{OnObservation: func(observation Observation) { observed = observation }},
	}
	runner.observeTranscript("hello")
	if observed.Kind != "audio.text" || observed.Text != "hello" || observed.SegmentID != "segment" || observed.Revision != 1 || !observed.Final {
		t.Fatalf("observation=%+v", observed)
	}
}

func decisionFallbackFixture(t *testing.T) (*Engine, domain.Config, *domain.Pipeline) {
	t.Helper()
	engine, cfg := testEngine(t, 2, &scriptedProvider{})
	pipeline := &cfg.Pipelines[0]
	base := *pipeline.Nodes[1].ModelPool
	base.Policy = domain.PoolPriority
	base.Order = domain.PoolPriority
	base.Dispatch = domain.DispatchSequential
	base.MaxParallel = 1
	base.MaxCalls = 2
	base.ResultName = "decision"
	base.Prompt = domain.PromptConfig{User: "Decide"}
	decision := domain.Node{ID: "decision-fallback", Type: domain.NodeDecision, Name: "Decision fallback", ModelPool: &base}
	pipeline.Nodes = append(pipeline.Nodes, decision)
	engine.Reconfigure(cfg, engine.registry)
	return engine, cfg, pipeline
}

func TestDecisionInvalidStructuredResponseFallsBackToNextModel(t *testing.T) {
	engine, _, pipeline := decisionFallbackFixture(t)
	var calls atomic.Int32
	engine.registry.Set("opencode", &scriptedProvider{run: func(_ context.Context, request providers.Request, _ string, events chan<- providers.Event) error {
		calls.Add(1)
		text := `{"action":"unsupported"}`
		if request.ModelID == "1" {
			text = `{"action":"no_action"}`
		}
		events <- providers.Event{Type: providers.EventDelta, Text: text, ModelID: request.ModelID}
		return nil
	}})

	result, err := engine.Execute(context.Background(), pipeline.ID, Input{
		EntryNodeID: "decision-fallback", Intent: string(domain.IntentDecide), Text: "hello",
	}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Published || result.Trace.Outcome != domain.OutcomeNoAction || calls.Load() != 2 {
		t.Fatalf("published=%v outcome=%s calls=%d", result.Published, result.Trace.Outcome, calls.Load())
	}
	invalidAttempts := 0
	for _, event := range result.Trace.Events {
		if event.Kind == "attempt_end" && event.Outcome == domain.OutcomeInvalidResponse {
			invalidAttempts++
		}
	}
	if invalidAttempts != 1 {
		t.Fatalf("invalid decision attempts=%d", invalidAttempts)
	}
}

func TestDecisionNoActionDoesNotTryFallbackModel(t *testing.T) {
	engine, _, pipeline := decisionFallbackFixture(t)
	var calls atomic.Int32
	engine.registry.Set("opencode", &scriptedProvider{run: func(_ context.Context, request providers.Request, _ string, events chan<- providers.Event) error {
		calls.Add(1)
		events <- providers.Event{Type: providers.EventDelta, Text: `{"action":"no_action"}`, ModelID: request.ModelID}
		return nil
	}})

	result, err := engine.Execute(context.Background(), pipeline.ID, Input{
		EntryNodeID: "decision-fallback", Intent: string(domain.IntentDecide), Text: "hello",
	}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Published || result.Trace.Outcome != domain.OutcomeNoAction || calls.Load() != 1 {
		t.Fatalf("published=%v outcome=%s calls=%d", result.Published, result.Trace.Outcome, calls.Load())
	}
}
