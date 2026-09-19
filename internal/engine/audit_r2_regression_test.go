package engine

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestR2AuditEmptyTranscriptionDoesNotFallback(t *testing.T) {
	var calls atomic.Int32
	eng, cfg := testEngine(t, 2, &scriptedProvider{buffered: true, run: func(_ context.Context, _ providers.Request, _ string, ch chan<- providers.Event) error {
		calls.Add(1)
		ch <- providers.Event{Type: providers.EventDelta, Text: ""}
		ch <- providers.Event{Type: providers.EventDone}
		return nil
	}})
	p := &eng.cfg.Pipelines[0]
	p.Nodes[0].Type = domain.NodeAudio
	pool := p.Nodes[1].ModelPool
	pool.Task = "transcribe"
	pool.ResultName = "audio.text"
	pool.Policy = domain.PoolPriority
	pool.Dispatch = domain.DispatchSequential
	for i := range pool.Models {
		pool.Models[i].MaxAttempts = 1
	}
	eng.cfg.Providers[0].Kind = domain.ProviderTranscription
	eng.cfg.Providers[0].CredentialPool.Keys = []domain.Credential{{ID: "audit", Secret: "FIXTURE_NOT_REAL", Enabled: true}}
	_, err := eng.Execute(context.Background(), cfg.ActivePipelineID, Input{Audio: &domain.AudioClip{PCM16: make([]int16, 4000), SampleRate: 16000, Channels: 1, DurationMS: 250, Final: true}, Intent: string(domain.IntentContext)}, RunOptions{})
	if calls.Load() != 1 || err != nil {
		t.Fatalf("valid empty transcription treated as failure: calls=%d err=%v", calls.Load(), err)
	}
}
func TestR2AuditDecisionKeepsNetworkErrorOutcome(t *testing.T) {
	eng, cfg := testEngine(t, 1, &scriptedProvider{run: func(context.Context, providers.Request, string, chan<- providers.Event) error {
		return &providers.RequestError{Outcome: domain.OutcomeNetworkError, Message: "offline"}
	}})
	p := &eng.cfg.Pipelines[0]
	p.Nodes[1].Type = domain.NodeDecision
	p.Nodes[1].ModelPool.Models[0].MaxAttempts = 1
	result, _ := eng.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame"), Intent: string(domain.IntentDecide)}, RunOptions{})
	for _, ev := range result.Trace.Events {
		if ev.Kind == "node_end" && ev.NodeID == p.Nodes[1].ID {
			if ev.Outcome != domain.OutcomeNetworkError {
				t.Fatalf("network failure routed as %s", ev.Outcome)
			}
			return
		}
	}
	t.Fatal("missing decision node end")
}

type auditLease struct{ released bool }

func (*auditLease) Commit()    {}
func (l *auditLease) Release() { l.released = true }

type auditStuckProvider struct {
	events chan providers.Event
	errs   chan error
}

func (*auditStuckProvider) ID() string { return "opencode" }
func (p *auditStuckProvider) Stream(context.Context, providers.Request, string) (<-chan providers.Event, <-chan error) {
	return p.events, p.errs
}
func TestR2AuditStuckProviderDoesNotFreeConcurrencyBeforeTermination(t *testing.T) {
	eng, _ := testEngine(t, 1, &scriptedProvider{run: func(context.Context, providers.Request, string, chan<- providers.Event) error { return nil }})
	synctest.Test(t, func(t *testing.T) {
		p := eng.cfg.Pipelines[0]
		n := p.Nodes[1]
		m := n.ModelPool.Models[0]
		m.TimeoutMS = 1
		m.FirstTokenTimeoutMS = 1
		lease := &auditLease{}
		stuck := &auditStuckProvider{make(chan providers.Event), make(chan error)}
		defer close(stuck.events)
		defer close(stuck.errs)
		r := newRunner(eng, p, Input{}, RunOptions{BeforeCall: func(context.Context) (CallLease, error) { return lease, nil }}, &domain.ExecutionTrace{})
		_, _, _ = r.singleAttempt(t.Context(), n, *n.ModelPool, nodeValue{}, m, stuck, domain.Credential{ID: "audit"})
		if lease.released {
			t.Errorf("hard concurrency lease released while provider channels still open after %v cleanup", 1500*time.Millisecond)
		}
	})
}
