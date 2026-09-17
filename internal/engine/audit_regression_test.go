package engine

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func auditSecondStage(e *Engine) *domain.ModelPoolConfig {
	p := &e.cfg.Pipelines[0]
	base := *p.Nodes[1].ModelPool
	base.Models = append([]domain.ModelSlot(nil), base.Models...)
	base.Models[0].ModelID = "second"
	base.Models[0].ID = "second"
	base.Policy = domain.PoolPriority
	p.Nodes = append(p.Nodes, domain.Node{ID: "second", Type: domain.NodeModelPool, ModelPool: &base})
	p.Edges[1].To = "second"
	p.Edges = append(p.Edges, domain.Edge{ID: "last", From: "second", To: p.OutputNodeID, On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1})
	return &base
}
func TestAuditPromptScopesRemainDistinct(t *testing.T) {
	got := make(chan string, 1)
	s := &scriptedProvider{run: func(ctx context.Context, r providers.Request, k string, ch chan<- providers.Event) error {
		text := "stage-one"
		if r.ModelID == "second" {
			got <- r.UserPrompt
			text = "final"
		}
		ch <- providers.Event{Type: providers.EventDelta, Text: text}
		return nil
	}}
	e, cfg := testEngine(t, 1, s)
	pool := auditSecondStage(e)
	pool.Prompt.User = "{{input.text}}|{{previous.output}}"
	_, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("original-frame"), Text: "original"}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v := <-got; v != "original|stage-one" {
		t.Fatalf("original input was overwritten: got %q", v)
	}
}
func TestAuditStreamingAcrossStagesDoesNotConcatenate(t *testing.T) {
	s := &scriptedProvider{run: func(ctx context.Context, r providers.Request, k string, ch chan<- providers.Event) error {
		text := "stage-one"
		if r.ModelID == "second" {
			text = "final"
		}
		ch <- providers.Event{Type: providers.EventDelta, Text: text}
		return nil
	}}
	e, cfg := testEngine(t, 1, s)
	auditSecondStage(e)
	display := ""
	res, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{OnDelta: func(s string) { display += s }, OnReset: func() { display = "" }})
	if err != nil {
		t.Fatal(err)
	}
	if display != res.Text {
		t.Fatalf("display=%q final=%q", display, res.Text)
	}
}
func TestAuditCredentialBudgetReusesAlreadyAdmittedKey(t *testing.T) {
	var calls atomic.Int32
	s := &scriptedProvider{run: func(ctx context.Context, r providers.Request, k string, ch chan<- providers.Event) error {
		calls.Add(1)
		if r.ModelID == "0" {
			return &providers.RequestError{Outcome: domain.OutcomeModelUnavailable, Message: "model unavailable"}
		}
		ch <- providers.Event{Type: providers.EventDelta, Text: "recovered"}
		return nil
	}}
	e, cfg := testEngine(t, 2, s)
	p := &e.cfg.Pipelines[0]
	p.Budget.MaxKeysPerProvider = 1
	p.Nodes[1].ModelPool.Policy = domain.PoolPriority
	e.cfg.Providers[0].CredentialPool.Keys = []domain.Credential{{ID: "one", Secret: "credential-one", Enabled: true}, {ID: "two", Secret: "credential-two", Enabled: true}}
	e.cfg.Providers[0].CredentialPool.MaxKeysPerRequest = 1
	res, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{})
	if err != nil || res.Text != "recovered" {
		t.Fatalf("fallback failed with reusable healthy key: calls=%d err=%v", calls.Load(), err)
	}
}
func TestAuditRequestDefaultsReachProvider(t *testing.T) {
	got := make(chan providers.Request, 1)
	s := &scriptedProvider{run: func(ctx context.Context, r providers.Request, k string, ch chan<- providers.Event) error {
		got <- r
		ch <- providers.Event{Type: providers.EventDelta, Text: "answer"}
		return nil
	}}
	e, cfg := testEngine(t, 1, s)
	b := &e.cfg.Pipelines[0].Budget
	b.ConnectTimeoutMS = 0
	b.FirstTokenTimeoutMS = 0
	b.ResponseTimeoutMS = 0
	b.StreamIdleTimeoutMS = 0
	m := &e.cfg.Pipelines[0].Nodes[1].ModelPool.Models[0]
	m.TimeoutMS = 0
	m.FirstTokenTimeoutMS = 0
	e.cfg.Providers[0].RequestDefaults = domain.RequestDefaults{ConnectTimeoutMS: 41, FirstTokenTimeoutMS: 42, ResponseTimeoutMS: 43, StreamIdleTimeoutMS: 44, MaxOutputTokens: 17}
	_, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.ConnectTimeout != 41*time.Millisecond || r.FirstTokenTimeout != 42*time.Millisecond || r.ResponseTimeout != 43*time.Millisecond || r.StreamIdleTimeout != 44*time.Millisecond {
		t.Fatalf("provider defaults lost: connect=%v first=%v response=%v idle=%v", r.ConnectTimeout, r.FirstTokenTimeout, r.ResponseTimeout, r.StreamIdleTimeout)
	}
}
func TestAuditValidatorFallbackRetainsScreenshot(t *testing.T) {
	got := make(chan int, 1)
	s := &scriptedProvider{run: func(ctx context.Context, r providers.Request, k string, ch chan<- providers.Event) error {
		text := "bad"
		if r.ModelID == "second" {
			got <- len(r.ImageJPEG)
			text = "recovered"
		}
		ch <- providers.Event{Type: providers.EventDelta, Text: text}
		return nil
	}}
	e, cfg := testEngine(t, 1, s)
	auditSecondStage(e)
	p := &e.cfg.Pipelines[0]
	p.Nodes = append(p.Nodes, domain.Node{ID: "validate", Type: domain.NodeValidator, Validator: &domain.ValidatorConfig{MinLength: 4}})
	p.Edges[1].To = "validate"
	p.Edges = append(p.Edges, domain.Edge{ID: "invalid", From: "validate", To: "second", On: []domain.Outcome{domain.OutcomeValidationFailed}, MaxVisits: 1})
	_, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("original-frame")}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v := <-got; v == 0 {
		t.Fatal("validator fallback received no original screenshot")
	}
}
func TestAuditExecuteWaitsForCancelledProvider(t *testing.T) {
	stopped := make(chan struct{})
	s := &scriptedProvider{run: func(ctx context.Context, r providers.Request, k string, ch chan<- providers.Event) error {
		defer close(stopped)
		<-ctx.Done()
		time.Sleep(80 * time.Millisecond)
		return ctx.Err()
	}}
	e, cfg := testEngine(t, 1, s)
	e.cfg.Pipelines[0].Nodes[1].ModelPool.Models[0].FirstTokenTimeoutMS = 10
	_, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{})
	if err == nil {
		t.Fatal("expected timeout")
	}
	select {
	case <-stopped:
	default:
		<-stopped
		t.Fatal("Execute returned before provider worker cleanup completed")
	}
}
func TestAuditFastestHealthyAccountsForFailures(t *testing.T) {
	e := newPolicyEngine()
	m := slots()
	m[0].ModelID = "unreliable"
	m[1].ModelID = "reliable"
	e.stats[modelKey(m[0])] = modelStat{Success: 1, Failure: 100, AvgTTFT: 1}
	e.stats[modelKey(m[1])] = modelStat{Success: 100, Failure: 0, AvgTTFT: 10}
	ordered := e.orderModelsForPolicy("audit", domain.PoolFastest, m[:2])
	if ordered[0].ModelID != "reliable" {
		t.Fatalf("health ignored: first=%s", ordered[0].ModelID)
	}
}
func TestAuditExhaustedEdgeAllowsRemainingFallback(t *testing.T) {
	e, cfg := testEngine(t, 1, &scriptedProvider{run: func(ctx context.Context, r providers.Request, k string, ch chan<- providers.Event) error {
		return fmt.Errorf("fail")
	}})
	p := e.cfg.Pipelines[0]
	p.Edges = []domain.Edge{{ID: "first", From: "models", To: "models", On: []domain.Outcome{domain.OutcomeAnyError}, MaxVisits: 1, Priority: 10}, {ID: "second", From: "models", To: "output", On: []domain.Outcome{domain.OutcomeAnyError}, MaxVisits: 1, Priority: 5}}
	tr := domain.ExecutionTrace{PipelineID: cfg.ActivePipelineID}
	r := newRunner(e, p, Input{}, RunOptions{}, &tr)
	r.edgeVisits["first"] = 1
	edge, ok := r.nextEdge("models", domain.OutcomeNetworkError)
	if !ok || edge.ID != "second" {
		t.Fatalf("exhausted high-priority edge still selected: %s", edge.ID)
	}
}
