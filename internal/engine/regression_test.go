package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

type scriptedProvider struct {
	run      func(context.Context, providers.Request, string, chan<- providers.Event) error
	buffered bool
}

func (s *scriptedProvider) ID() string        { return "opencode" }
func (s *scriptedProvider) Incremental() bool { return !s.buffered }
func (s *scriptedProvider) Stream(ctx context.Context, req providers.Request, key string) (<-chan providers.Event, <-chan error) {
	ev := make(chan providers.Event, 8)
	errs := make(chan error, 1)
	go func() {
		defer close(ev)
		defer close(errs)
		if e := s.run(ctx, req, key, ev); e != nil {
			errs <- e
		}
	}()
	return ev, errs
}
func testEngine(t *testing.T, slots int, script *scriptedProvider) (*Engine, domain.Config) {
	t.Helper()
	cfg := config.Default()
	p := &cfg.Pipelines[0]
	pool := p.Nodes[1].ModelPool
	pool.Policy = domain.PoolParallel
	pool.Models = nil
	pool.MaxParallel = 3
	pool.MaxCalls = 10
	for i := 0; i < slots; i++ {
		pool.Models = append(pool.Models, domain.ModelSlot{ID: fmt.Sprint(i), ProviderID: "opencode", ModelID: fmt.Sprint(i), Enabled: true, Priority: 100 - i, QualityScore: 90, TimeoutMS: 1000, FirstTokenTimeoutMS: 500})
	}
	reg, e := providers.NewRegistry(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(reg.Close)
	reg.Set("opencode", script)
	return New(cfg, reg, nil, nil), cfg
}
func TestRaceNeverConcatenatesCompetingAnswers(t *testing.T) {
	first := make(chan struct{})
	script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		if r.ModelID == "0" {
			ch <- providers.Event{Type: providers.EventDelta, Text: "PROVISIONAL"}
			close(first)
			<-ctx.Done()
			return ctx.Err()
		}
		<-first
		time.Sleep(20 * time.Millisecond)
		ch <- providers.Event{Type: providers.EventDelta, Text: "WINNER"}
		return nil
	}}
	eng, cfg := testEngine(t, 2, script)
	var mu sync.Mutex
	display := ""
	res, e := eng.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{OnDelta: func(d string) { mu.Lock(); display += d; mu.Unlock() }, OnReset: func() { mu.Lock(); display = ""; mu.Unlock() }})
	if e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if display != "WINNER" || res.Text != "WINNER" {
		t.Fatal(display, res.Text)
	}
	if res.Trace.Winner == nil || res.Trace.Winner.ModelID != "1" {
		t.Fatal(res.Trace.Winner)
	}
}
func TestBufferedProviderDoesNotFabricateTTFTOrTPS(t *testing.T) {
	script := &scriptedProvider{buffered: true, run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		time.Sleep(25 * time.Millisecond)
		ch <- providers.Event{Type: providers.EventDelta, Text: "done"}
		ch <- providers.Event{Type: providers.EventDone, OutputTokens: 100}
		return nil
	}}
	eng, cfg := testEngine(t, 1, script)
	eng.cfg.Pipelines[0].Nodes[1].ModelPool.Models[0].FirstTokenTimeoutMS = 2
	res, e := eng.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("x")}, RunOptions{})
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range res.Trace.Events {
		if v.Kind == "attempt_end" && (v.TTFTMS != 0 || v.TPS != 0) {
			t.Fatal(v)
		}
	}
}
func TestFallbackRetainsOriginalScreenshot(t *testing.T) {
	script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		if string(r.ImageJPEG) != "original-frame" {
			return errors.New("fallback lost image")
		}
		if r.ModelID == "0" {
			return &providers.RequestError{Outcome: domain.OutcomeModelUnavailable, Message: "down"}
		}
		ch <- providers.Event{Type: providers.EventDelta, Text: "recovered"}
		return nil
	}}
	eng, cfg := testEngine(t, 1, script)
	p := &eng.cfg.Pipelines[0]
	fallback := p.Nodes[1]
	fallback.ID = "fallback"
	poolCopy := *fallback.ModelPool
	poolCopy.Models = append([]domain.ModelSlot(nil), poolCopy.Models...)
	poolCopy.Models[0].ModelID = "1"
	fallback.ModelPool = &poolCopy
	p.Nodes = append(p.Nodes, fallback)
	p.Edges = append(p.Edges, domain.Edge{ID: "recover", From: p.Nodes[1].ID, To: "fallback", On: []domain.Outcome{domain.OutcomeAnyError}, MaxVisits: 1}, domain.Edge{ID: "recovered", From: "fallback", To: p.OutputNodeID, On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1})
	res, e := eng.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("original-frame")}, RunOptions{})
	if e != nil || res.Text != "recovered" {
		t.Fatal(e, res.Text)
	}
}
func TestCancelledRaceStopsWithoutWaitingForProviderTimeout(t *testing.T) {
	script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	eng, cfg := testEngine(t, 3, script)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e := eng.Execute(ctx, cfg.ActivePipelineID, Input{ImageJPEG: []byte("x")}, RunOptions{})
	if e == nil || time.Since(start) > time.Second {
		t.Fatal("cancellation stalled", e)
	}
}
func TestFailedProviderKeyIsRedactedFromReturnedErrorAndTrace(t *testing.T) {
	const secret = "a-private-key-123"
	script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		return errors.New("echo: " + secret)
	}}
	eng, cfg := testEngine(t, 1, script)
	eng.cfg.Providers[0].CredentialPool.Keys = []domain.Credential{{ID: "test", Secret: secret, Enabled: true}}
	result, e := eng.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("x")}, RunOptions{})
	if e == nil || strings.Contains(e.Error(), secret) {
		t.Fatal("secret in returned error")
	}
	for _, v := range result.Trace.Events {
		if strings.Contains(v.Message, secret) {
			t.Fatal("secret in trace")
		}
	}
}
