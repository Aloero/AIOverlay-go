package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

// Cancel at the first post-admission context check, without a scheduling race.
type cancelAtAdmissionCheck struct {
	context.Context
	cancel context.CancelFunc
}

func (ctx cancelAtAdmissionCheck) Err() error {
	ctx.cancel()
	return ctx.Context.Err()
}

type countedAttemptLease struct{ commits, releases atomic.Int32 }

func (lease *countedAttemptLease) Commit()  { lease.commits.Add(1) }
func (lease *countedAttemptLease) Release() { lease.releases.Add(1) }

func TestAttemptCancellationAfterAdmissionDoesNotLeakSemaphore(t *testing.T) {
	eng, cfg := testEngine(t, 1, &scriptedProvider{})
	pipeline := cfg.Pipelines[0]
	node := pipeline.Nodes[1]
	runner := newRunner(eng, pipeline, Input{}, RunOptions{}, &domain.ExecutionTrace{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, outcome, err := runner.singleAttempt(cancelAtAdmissionCheck{ctx, cancel}, node, *node.ModelPool, nodeValue{}, node.ModelPool.Models[0], nil, domain.Credential{})
	if !errors.Is(err, context.Canceled) || outcome != domain.OutcomeCancelled {
		t.Fatalf("outcome=%s err=%v", outcome, err)
	}
	if held := len(runner.sem); held != 0 {
		t.Fatalf("cancelled attempt leaked %d concurrency permits", held)
	}
}

func TestAttemptRejectedAdmissionReleasesWithoutCommitting(t *testing.T) {
	for _, reason := range []string{"session_rejected", "budget_exhausted", "cancelled_after_lease"} {
		t.Run(reason, func(t *testing.T) {
			eng, cfg := testEngine(t, 1, &scriptedProvider{})
			pipeline := cfg.Pipelines[0]
			node := pipeline.Nodes[1]
			lease := &countedAttemptLease{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			before := func(context.Context) (CallLease, error) {
				if reason == "session_rejected" {
					return nil, errors.New("rejected")
				}
				if reason == "cancelled_after_lease" {
					cancel()
				}
				return lease, nil
			}
			if reason == "budget_exhausted" {
				pipeline.Budget.MaxModelCalls = 0
			}
			r := newRunner(eng, pipeline, Input{}, RunOptions{BeforeCall: before}, &domain.ExecutionTrace{})
			_, _, err := r.singleAttempt(ctx, node, *node.ModelPool, nodeValue{}, node.ModelPool.Models[0], nil, domain.Credential{})
			if err == nil || len(r.sem) != 0 || lease.commits.Load() != 0 {
				t.Fatalf("err=%v semaphore=%d commits=%d", err, len(r.sem), lease.commits.Load())
			}
			wantRelease := int32(1)
			if reason == "session_rejected" {
				wantRelease = 0
			}
			if lease.releases.Load() != wantRelease {
				t.Fatalf("releases=%d want=%d", lease.releases.Load(), wantRelease)
			}
		})
	}
}

func TestAttemptTerminalOutcomesAndLeaseLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, text                                                string
		outcome                                                   domain.Outcome
		buffered, transcribe, decision, firstTimeout, idleTimeout bool
		err                                                       error
	}{
		{name: "delta_without_first_event", text: " answer ", outcome: domain.OutcomeSuccess},
		{name: "buffered", text: "answer", buffered: true, outcome: domain.OutcomeSuccess},
		{name: "empty", outcome: domain.OutcomeInvalidResponse},
		{name: "empty_transcription", transcribe: true, outcome: domain.OutcomeSuccess},
		{name: "invalid_decision", text: "unstructured answer", decision: true, outcome: domain.OutcomeInvalidResponse},
		{name: "provider_error", err: &providers.RequestError{Outcome: domain.OutcomeAuthFailed, StatusCode: 401, Message: "denied"}, outcome: domain.OutcomeAuthFailed},
		{name: "first_token_timeout", firstTimeout: true, outcome: domain.OutcomeFirstTokenTimeout},
		{name: "idle_timeout", text: "partial", idleTimeout: true, outcome: domain.OutcomeStreamIdleTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				provider := &scriptedProvider{buffered: tc.buffered, run: func(ctx context.Context, _ providers.Request, _ string, events chan<- providers.Event) error {
					if tc.firstTimeout {
						<-ctx.Done()
						return ctx.Err()
					}
					if tc.buffered {
						time.Sleep(30 * time.Millisecond)
					}
					if tc.text != "" {
						events <- providers.Event{Type: providers.EventDelta, Text: tc.text}
					}
					if tc.idleTimeout {
						<-ctx.Done()
						return ctx.Err()
					}
					if tc.err != nil {
						return tc.err
					}
					events <- providers.Event{Type: providers.EventDone, OutputTokens: 12}
					return nil
				}}
				eng, cfg := testEngine(t, 1, provider)
				pipeline := cfg.Pipelines[0]
				pipeline.Budget.StreamIdleTimeoutMS = 10
				node := pipeline.Nodes[1]
				if tc.decision {
					node.Type = domain.NodeDecision
				}
				pool := *node.ModelPool
				if tc.transcribe {
					pool.Task = "transcribe"
				}
				model := pool.Models[0]
				model.FirstTokenTimeoutMS = 10
				trace := &domain.ExecutionTrace{}
				lease := &countedAttemptLease{}
				r := newRunner(eng, pipeline, Input{}, RunOptions{BeforeCall: func(context.Context) (CallLease, error) { return lease, nil }}, trace)
				text, outcome, err := r.singleAttempt(t.Context(), node, pool, nodeValue{}, model, provider, domain.Credential{ID: "fixture"})
				if outcome != tc.outcome || (err == nil) != (outcome == domain.OutcomeSuccess) {
					t.Fatalf("text=%q outcome=%s err=%v", text, outcome, err)
				}
				if tc.name == "delta_without_first_event" && text != "answer" {
					t.Fatalf("text=%q", text)
				}
				if lease.commits.Load() != 1 || lease.releases.Load() != 1 || len(r.sem) != 0 {
					t.Fatalf("lease commits=%d releases=%d semaphore=%d", lease.commits.Load(), lease.releases.Load(), len(r.sem))
				}
				ends, statuses := 0, 0
				for _, event := range trace.Events {
					if event.Kind == "attempt_end" {
						ends++
						if event.Outcome != tc.outcome {
							t.Fatalf("trace outcome=%s", event.Outcome)
						}
						if tc.buffered && (event.TTFTMS != 0 || event.TPS != 0) {
							t.Fatal("buffered metrics fabricated")
						}
					}
					if event.Kind == "http_status" && event.HTTPStatus == 401 {
						statuses++
					}
				}
				if ends != 1 {
					t.Fatalf("terminal events=%d", ends)
				}
				if tc.err != nil && statuses != 1 {
					t.Fatalf("HTTP status events=%d", statuses)
				}
			})
		})
	}
}

type timeoutProbeProvider struct {
	ctx       context.Context
	withDelta bool
}

func (*timeoutProbeProvider) ID() string { return "opencode" }
func (p *timeoutProbeProvider) Stream(ctx context.Context, _ providers.Request, _ string) (<-chan providers.Event, <-chan error) {
	p.ctx = ctx
	events, errs := make(chan providers.Event, 1), make(chan error, 1)
	if p.withDelta {
		events <- providers.Event{Type: providers.EventDelta, Text: "partial"}
	}
	go func() {
		defer close(events)
		defer close(errs)
		<-ctx.Done()
		errs <- ctx.Err()
	}()
	return events, errs
}

func TestAttemptTimeoutCancelsProviderBeforeTerminalCallback(t *testing.T) {
	for _, withDelta := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			provider := &timeoutProbeProvider{withDelta: withDelta}
			eng, cfg := testEngine(t, 1, &scriptedProvider{})
			pipeline := cfg.Pipelines[0]
			pipeline.Budget.StreamIdleTimeoutMS = 10
			node := pipeline.Nodes[1]
			model := node.ModelPool.Models[0]
			model.FirstTokenTimeoutMS = 10
			terminal := 0
			opt := RunOptions{OnTrace: func(event domain.TraceEvent) {
				if event.Kind == "attempt_end" {
					terminal++
					if provider.ctx.Err() == nil {
						t.Error("terminal callback ran before cancelling the timed-out provider")
					}
				}
			}}
			r := newRunner(eng, pipeline, Input{}, opt, &domain.ExecutionTrace{})
			_, _, _ = r.singleAttempt(t.Context(), node, *node.ModelPool, nodeValue{}, model, provider, domain.Credential{})
			if terminal != 1 {
				t.Fatalf("terminal callbacks=%d", terminal)
			}
		})
	}
}

type panickingAttemptLease struct{}

func (*panickingAttemptLease) Commit()  {}
func (*panickingAttemptLease) Release() { panic("fixture release panic") }

func TestAttemptPanicStillReleasesSemaphore(t *testing.T) {
	for _, phase := range []string{"before_call", "lease_release"} {
		t.Run(phase, func(t *testing.T) {
			provider := &scriptedProvider{run: func(_ context.Context, _ providers.Request, _ string, events chan<- providers.Event) error {
				events <- providers.Event{Type: providers.EventDelta, Text: "fixture"}
				return nil
			}}
			eng, cfg := testEngine(t, 1, provider)
			pipeline := cfg.Pipelines[0]
			node := pipeline.Nodes[1]
			opt := RunOptions{BeforeCall: func(context.Context) (CallLease, error) {
				if phase == "before_call" {
					panic("fixture admission panic")
				}
				return &panickingAttemptLease{}, nil
			}}
			r := newRunner(eng, pipeline, Input{}, opt, &domain.ExecutionTrace{})
			func() {
				defer func() {
					if recover() == nil {
						t.Error("fixture did not panic")
					}
				}()
				r.singleAttempt(t.Context(), node, *node.ModelPool, nodeValue{}, node.ModelPool.Models[0], provider, domain.Credential{})
			}()
			if len(r.sem) != 0 {
				t.Fatalf("%s panic leaked semaphore", phase)
			}
		})
	}
}
