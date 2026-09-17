package control

import (
	"context"
	"errors"

	"aioverlay/internal/domain"
	"aioverlay/internal/session"
	"testing"
	"time"
)

func TestPresentationArbiterRejectsAutoTriggeredBeforeManual(t *testing.T) {
	start := time.Now()
	arbiter := PresentationArbiter{}
	job := Job{ID: "manual", Purpose: "command", Started: start, Running: true}
	arbiter.ObserveJob(job)

	auto := session.Publication{ID: 1, Text: "old auto", TriggeredAt: start.Add(-time.Second)}
	if arbiter.AcceptBackground(job, auto) {
		t.Fatal("background answer replaced a running manual answer")
	}

	job.Running = false
	job.Finished = start.Add(time.Second)
	arbiter.ObserveJob(job)
	if arbiter.AcceptBackground(job, auto) {
		t.Fatal("rejected background answer was reconsidered after manual completion")
	}
}

func TestPresentationArbiterRejectsAutoTriggeredDuringManual(t *testing.T) {
	start := time.Now()
	arbiter := PresentationArbiter{}
	job := Job{ID: "manual", Purpose: "command", Started: start, Finished: start.Add(2 * time.Second)}
	arbiter.ObserveJob(job)

	auto := session.Publication{ID: 1, Text: "late callback", TriggeredAt: start.Add(time.Second)}
	if arbiter.AcceptBackground(job, auto) {
		t.Fatal("auto answer triggered during manual ownership was published")
	}
}

func TestPresentationArbiterAcceptsNewerAutoAfterManual(t *testing.T) {
	start := time.Now()
	arbiter := PresentationArbiter{}
	job := Job{ID: "manual", Purpose: "command", Started: start, Finished: start.Add(time.Second)}
	arbiter.ObserveJob(job)

	auto := session.Publication{ID: 7, Text: "fresh auto", TriggeredAt: start.Add(2 * time.Second)}
	if !arbiter.AcceptBackground(job, auto) {
		t.Fatal("fresh background answer after manual completion was suppressed")
	}
	if arbiter.AcceptBackground(job, auto) {
		t.Fatal("same background publication was accepted twice")
	}
}

func TestContextCommandDoesNotOwnOverlayOrBlockBackgroundPublication(t *testing.T) {
	start := time.Now()
	arbiter := PresentationArbiter{}
	job := Job{ID: "context", Purpose: "context_command", Started: start, Running: true}
	arbiter.ObserveJob(job)

	if PresentsInOverlay(job) {
		t.Fatal("context-only command unexpectedly owns overlay presentation")
	}
	auto := session.Publication{ID: 1, Text: "fresh auto", TriggeredAt: start.Add(time.Millisecond)}
	if !arbiter.AcceptBackground(job, auto) {
		t.Fatal("running context-only command suppressed unrelated background answer")
	}
}

func TestOverlayPresentationPurposesAreExplicit(t *testing.T) {
	for _, purpose := range []string{"pipeline", "command"} {
		if !PresentsInOverlay(Job{ID: "job", Purpose: purpose}) {
			t.Fatalf("%s should own overlay presentation", purpose)
		}
	}
	for _, purpose := range []string{"context_command", "connection_test", "benchmark", ""} {
		if PresentsInOverlay(Job{ID: "job", Purpose: purpose}) {
			t.Fatalf("%s unexpectedly owns overlay presentation", purpose)
		}
	}
}

func TestDecisionCommandNoActionRestoresPreviousPresentation(t *testing.T) {
	job := Job{
		ID: "decide", Purpose: "decision_command", Running: false,
		Trace: domain.ExecutionTrace{Outcome: domain.OutcomeNoAction},
	}
	if !PresentsInOverlay(job) || !IsDecisionPresentationJob(job) || !RestoresPreviousPresentation(job) {
		t.Fatalf("decision no_action presentation contract not recognized: %+v", job)
	}

	job.Trace.Outcome = domain.OutcomeRespond
	if RestoresPreviousPresentation(job) {
		t.Fatal("responding decision command restored stale presentation")
	}
	job.Trace.Outcome = domain.OutcomeNoAction
	job.Error = "provider failure"
	if RestoresPreviousPresentation(job) {
		t.Fatal("failed decision command hid its error as no_action")
	}
}

func TestScreenDecisionUsesDecisionPresentationPurpose(t *testing.T) {
	if got := commandPurpose("screen_decide"); got != "decision_command" {
		t.Fatalf("screen_decide purpose=%q", got)
	}
	if got := commandPurpose("screen_context"); got != "context_command" {
		t.Fatalf("screen_context purpose=%q", got)
	}
	if got := commandPurpose("analyze"); got != "command" {
		t.Fatalf("analyze purpose=%q", got)
	}
}

func TestManualCaptureHonorsContextBeforeAndAfterNativeCapture(t *testing.T) {
	server := New("127.0.0.1:0", &auditBackend{})
	var calls int
	server.Capture = func() ([]byte, error) {
		calls++
		return []byte("frame"), nil
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := server.manualCapture(cancelled); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("pre-cancel capture err=%v calls=%d", err, calls)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	server.Capture = func() ([]byte, error) {
		calls++
		close(started)
		<-release
		return []byte("late-frame"), nil
	}
	ctx, stop := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := server.manualCapture(ctx)
		result <- err
	}()
	<-started
	stop()
	close(release)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("post-cancel capture err=%v", err)
	}
}
