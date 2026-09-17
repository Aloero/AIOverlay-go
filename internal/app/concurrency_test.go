package app

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"aioverlay/internal/providers"
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type orderedProvider struct {
	started   chan string
	release   chan struct{}
	active    atomic.Int32
	maxActive atomic.Int32
}

func (*orderedProvider) ID() string { return "opencode" }

func (p *orderedProvider) Stream(ctx context.Context, request providers.Request, _ string) (<-chan providers.Event, <-chan error) {
	events := make(chan providers.Event, 3)
	errors := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errors)
		active := p.active.Add(1)
		defer p.active.Add(-1)
		for {
			current := p.maxActive.Load()
			if active <= current || p.maxActive.CompareAndSwap(current, active) {
				break
			}
		}
		p.started <- request.UserPrompt
		select {
		case <-p.release:
		case <-ctx.Done():
			errors <- ctx.Err()
			return
		}
		events <- providers.Event{Type: providers.EventFirstToken, ModelID: request.ModelID}
		events <- providers.Event{Type: providers.EventDelta, Text: "ok", ModelID: request.ModelID}
		events <- providers.Event{Type: providers.EventDone, ModelID: request.ModelID}
	}()
	return events, errors
}

func TestAppRunSharesHardLimitAndPrioritizesManualWaiter(t *testing.T) {
	a := studioTestApp(t)
	cfg := a.GetConfig()
	pipeline := &cfg.Pipelines[0]
	pool := pipeline.Nodes[1].ModelPool
	pool.Policy = domain.PoolPriority
	pool.Prompt = domain.PromptConfig{User: "{{input.text}}"}
	pool.Models = []domain.ModelSlot{{
		ID: "fixture", ProviderID: "opencode", ModelID: "fixture", Enabled: true,
		Priority: 100, QualityScore: 100, TimeoutMS: 5000, FirstTokenTimeoutMS: 2000, MaxAttempts: 1,
	}}
	pipeline.SessionBudget = &domain.SessionBudget{CallsPerMinute: 100, QueueCapacity: 2, MaxParallelCalls: 1, EndToEndMS: 5000}
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	provider := &orderedProvider{started: make(chan string, 3), release: make(chan struct{}, 3)}
	a.registry.Set("opencode", provider)

	type runResult struct{ err error }
	results := make(chan runResult, 3)
	startRun := func(text, purpose string) {
		go func() {
			_, err := a.Run(context.Background(), pipeline.ID, engine.Input{ImageJPEG: []byte("frame"), Text: text}, engine.RunOptions{Purpose: purpose})
			results <- runResult{err: err}
		}()
	}

	startRun("background-1", "background")
	if got := waitStarted(t, provider.started); got != "background-1" {
		t.Fatalf("first dispatch=%q", got)
	}

	startRun("manual", "command")
	waitCondition(t, func() bool { return a.sessions.Governor().Snapshot()["manual_waiters"] == 1 }, "manual run did not become a priority waiter")
	startRun("background-2", "background")
	assertNoDispatch(t, provider.started, 30*time.Millisecond)

	provider.release <- struct{}{}
	if got := waitStarted(t, provider.started); got != "manual" {
		t.Fatalf("manual did not get next shared slot: %q", got)
	}
	provider.release <- struct{}{}
	if got := waitStarted(t, provider.started); got != "background-2" {
		t.Fatalf("background resumed out of order: %q", got)
	}
	provider.release <- struct{}{}

	for range 3 {
		if result := <-results; result.err != nil {
			t.Fatal(result.err)
		}
	}
	if provider.maxActive.Load() != 1 {
		t.Fatalf("shared hard limit overcommitted: max active=%d", provider.maxActive.Load())
	}
}

func waitStarted(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case value := <-started:
		return value
	case <-time.After(time.Second):
		t.Fatal("provider dispatch did not start")
		return ""
	}
}

func assertNoDispatch(t *testing.T, started <-chan string, duration time.Duration) {
	t.Helper()
	select {
	case value := <-started:
		t.Fatalf("unexpected concurrent dispatch %q", value)
	case <-time.After(duration):
	}
}

func waitCondition(t *testing.T, condition func() bool, failure string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(failure)
}
