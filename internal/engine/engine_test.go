package engine

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	tracestore "aioverlay/internal/trace"
)

type fakeProvider struct {
	id     string
	delay  time.Duration
	answer string
	err    error
}

type countingCallLease struct {
	commits  *atomic.Int32
	releases *atomic.Int32
}

func (l *countingCallLease) Commit()  { l.commits.Add(1) }
func (l *countingCallLease) Release() { l.releases.Add(1) }

func (f *fakeProvider) ID() string { return f.id }

// Incremental declares this fake a streaming transport. The delivery shape is the adapter's own
// fact, so a fake must state it instead of inheriting it from the kind of the provider id it
// replaces - a streaming fake on the "opencode" connection still streams.
func (f *fakeProvider) Incremental() bool { return true }
func (f *fakeProvider) Stream(ctx context.Context, r providers.Request, key string) (<-chan providers.Event, <-chan error) {
	e := make(chan providers.Event, 4)
	er := make(chan error, 1)
	go func() {
		defer close(e)
		defer close(er)
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			er <- ctx.Err()
			return
		}
		if f.err != nil {
			er <- f.err
			return
		}
		e <- providers.Event{Type: providers.EventFirstToken, ModelID: r.ModelID}
		e <- providers.Event{Type: providers.EventDelta, Text: f.answer, ModelID: r.ModelID}
		e <- providers.Event{Type: providers.EventDone, ModelID: r.ModelID, OutputTokens: 5}
	}()
	return e, er
}

func TestPriorityPoolFallsBack(t *testing.T) {
	cfg := config.Default()
	p := &cfg.Pipelines[0]
	pool := p.Nodes[1].ModelPool
	pool.Policy = domain.PoolPriority
	pool.Models = []domain.ModelSlot{{ID: "a", ProviderID: "opencode", ModelID: "a", Enabled: true, Priority: 100, QualityScore: 90, TimeoutMS: 1000, FirstTokenTimeoutMS: 500, MaxAttempts: 1}, {ID: "b", ProviderID: "openrouter", ModelID: "b", Enabled: true, Priority: 90, QualityScore: 90, TimeoutMS: 1000, FirstTokenTimeoutMS: 500, MaxAttempts: 1}}
	cfg.Providers[1].CredentialPool.Keys = []domain.Credential{{ID: "k", Secret: "x", Enabled: true, Priority: 100}}
	reg, _ := providers.NewRegistry(cfg)
	reg.Set("opencode", &fakeProvider{id: "opencode", err: &providers.RequestError{Outcome: domain.OutcomeModelUnavailable, Message: "down"}})
	reg.Set("openrouter", &fakeProvider{id: "openrouter", answer: "ok"})
	eng := New(cfg, reg, nil, tracestore.New(10))
	res, err := eng.Execute(context.Background(), p.ID, Input{ImageJPEG: []byte("x")}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "ok" {
		t.Fatalf("got %q", res.Text)
	}
}

func TestCallLeaseCommitsOnlyAfterLocalBudgetAdmission(t *testing.T) {
	cfg := config.Default()
	pipeline := &cfg.Pipelines[0]
	pool := pipeline.Nodes[1].ModelPool
	pool.Policy = domain.PoolPriority
	pool.MaxCalls = 1
	pool.Models = []domain.ModelSlot{{
		ID: "retry", ProviderID: "opencode", ModelID: "retry", Enabled: true,
		Priority: 100, QualityScore: 90, TimeoutMS: 1000, FirstTokenTimeoutMS: 500, MaxAttempts: 2,
	}}

	registry, err := providers.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	registry.Set("opencode", &fakeProvider{id: "opencode", err: &providers.RequestError{Outcome: domain.OutcomeNetworkError, Message: "network"}})
	engine := New(cfg, registry, nil, nil)

	var acquired, committed, released atomic.Int32
	_, err = engine.Execute(context.Background(), pipeline.ID, Input{ImageJPEG: []byte("frame")}, RunOptions{
		BeforeCall: func(context.Context) (CallLease, error) {
			acquired.Add(1)
			return &countingCallLease{commits: &committed, releases: &released}, nil
		},
	})
	if err == nil {
		t.Fatal("expected pool call budget to stop retry")
	}
	if acquired.Load() != 2 || committed.Load() != 1 || released.Load() != 2 {
		t.Fatalf("leases acquired=%d committed=%d released=%d", acquired.Load(), committed.Load(), released.Load())
	}
}

func TestStaggeredRaceReturnsFastWinner(t *testing.T) {
	cfg := config.Default()
	p := &cfg.Pipelines[0]
	pool := p.Nodes[1].ModelPool
	pool.Policy = domain.PoolStaggered
	pool.HedgeDelayMS = 10
	pool.Models = []domain.ModelSlot{{ID: "slow", ProviderID: "opencode", ModelID: "slow", Enabled: true, Priority: 100, QualityScore: 90, TimeoutMS: 1000, FirstTokenTimeoutMS: 500}, {ID: "fast", ProviderID: "openrouter", ModelID: "fast", Enabled: true, Priority: 90, QualityScore: 90, TimeoutMS: 1000, FirstTokenTimeoutMS: 500}}
	cfg.Providers[1].CredentialPool.Keys = []domain.Credential{{ID: "k", Secret: "x", Enabled: true, Priority: 100}}
	reg, _ := providers.NewRegistry(cfg)
	reg.Set("opencode", &fakeProvider{id: "opencode", delay: 100 * time.Millisecond, answer: "slow"})
	reg.Set("openrouter", &fakeProvider{id: "openrouter", delay: 5 * time.Millisecond, answer: "fast"})
	eng := New(cfg, reg, nil, nil)
	start := time.Now()
	res, err := eng.Execute(context.Background(), p.ID, Input{ImageJPEG: []byte("x")}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "fast" {
		t.Fatalf("got %q", res.Text)
	}
	if time.Since(start) > 80*time.Millisecond {
		t.Fatalf("race too slow")
	}
}

func TestVisionPreprocessorFeedsTextModel(t *testing.T) {
	cfg := config.Default()
	p := &cfg.Pipelines[0]
	pool := p.Nodes[1].ModelPool
	pool.Policy = domain.PoolPriority
	pool.Models = []domain.ModelSlot{{ID: "llm", ProviderID: "openrouter", ModelID: "llm", Enabled: true, Priority: 100, QualityScore: 90, TimeoutMS: 1000, FirstTokenTimeoutMS: 500}}
	pool.VisionPreprocessor = &domain.ModelPoolConfig{Policy: domain.PoolPriority, Models: []domain.ModelSlot{{ID: "vlm", ProviderID: "opencode", ModelID: "vlm", Enabled: true, Priority: 100, QualityScore: 90, TimeoutMS: 1000, FirstTokenTimeoutMS: 500}}, QualityFloor: 80, MaxParallel: 1, MaxCalls: 1, Prompt: domain.PromptConfig{User: "extract"}}
	cfg.Providers[1].CredentialPool.Keys = []domain.Credential{{ID: "k", Secret: "x", Enabled: true, Priority: 100}}
	reg, _ := providers.NewRegistry(cfg)
	reg.Set("opencode", &fakeProvider{id: "opencode", answer: "QUESTION 42"})
	reg.Set("openrouter", &fakeProvider{id: "openrouter", answer: "42"})
	eng := New(cfg, reg, nil, nil)
	res, err := eng.Execute(context.Background(), p.ID, Input{ImageJPEG: []byte("x")}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "42" {
		t.Fatal(res.Text)
	}
}

func TestFirstTokenTimeout(t *testing.T) {
	cfg := config.Default()
	p := &cfg.Pipelines[0]
	pool := p.Nodes[1].ModelPool
	pool.Policy = domain.PoolPriority
	pool.Models = []domain.ModelSlot{{ID: "slow", ProviderID: "opencode", ModelID: "slow", Enabled: true, Priority: 100, QualityScore: 90, TimeoutMS: 500, FirstTokenTimeoutMS: 10}}
	reg, _ := providers.NewRegistry(cfg)
	reg.Set("opencode", &fakeProvider{id: "opencode", delay: 100 * time.Millisecond, answer: "late"})
	eng := New(cfg, reg, nil, nil)
	_, err := eng.Execute(context.Background(), p.ID, Input{ImageJPEG: []byte("x")}, RunOptions{})
	if err == nil {
		t.Fatal("expected timeout")
	}
	var re *providers.RequestError
	if !errors.As(err, &re) && err.Error() == "" {
		t.Fatal(err)
	}
}

func TestMaxModelCallsCountsActualCredentialRequests(t *testing.T) {
	cfg := config.Default()
	p := &cfg.Pipelines[0]
	p.Budget.MaxModelCalls = 1
	pool := p.Nodes[1].ModelPool
	pool.Policy = domain.PoolPriority
	pool.Models = []domain.ModelSlot{{ID: "m", ProviderID: "openrouter", ModelID: "m", Enabled: true, Priority: 100, QualityScore: 90, TimeoutMS: 1000, FirstTokenTimeoutMS: 500, MaxAttempts: 1}}
	cfg.Providers[1].CredentialPool.Keys = []domain.Credential{{ID: "k1", Secret: "1", Enabled: true, Priority: 100}, {ID: "k2", Secret: "2", Enabled: true, Priority: 90}}
	reg, _ := providers.NewRegistry(cfg)
	reg.Set("openrouter", &fakeProvider{id: "openrouter", err: &providers.RequestError{Outcome: domain.OutcomeAuthFailed, Message: "bad"}})
	eng := New(cfg, reg, nil, nil)
	_, err := eng.Execute(context.Background(), p.ID, Input{ImageJPEG: []byte("x")}, RunOptions{})
	if err == nil {
		t.Fatal("expected budget failure")
	}
	if !strings.Contains(err.Error(), "max model calls") {
		t.Fatalf("unexpected: %v", err)
	}
}
