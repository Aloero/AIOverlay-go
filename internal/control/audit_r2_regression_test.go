package control

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"context"
	"testing"
	"testing/synctest"
)

func TestR2AuditCancelledTaskRejectsLateDelta(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var j JobManager
		started := make(chan struct{})
		released := make(chan struct{})
		_, err := j.startTaskPurpose(t.Context(), "p", "command", func(ctx context.Context, opt engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
			close(started)
			<-ctx.Done()
			opt.OnDelta("LATE_AFTER_CANCEL")
			close(released)
			return engine.Result{Trace: domain.ExecutionTrace{}}, nil, ctx.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		<-started
		j.Cancel()
		<-released
		synctest.Wait()
		if j.Snapshot().Text != "" {
			t.Fatalf("cancelled owner changed displayed text: %q", j.Snapshot().Text)
		}
	})
}
func TestR2AuditQueuedContextOnlyKeepsCompletedFirstAnswerObservable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var j JobManager
		release := make(chan struct{})
		started := make(chan struct{})
		first, _ := j.queueTaskPurpose(t.Context(), "p", "command", nil, func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
			close(started)
			<-release
			return engine.Result{Text: "FIRST_ANSWER", Published: true}, nil, nil
		})
		<-started
		_, err := j.queueTaskPurpose(t.Context(), "p", "context_command", nil, func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
			return engine.Result{Trace: domain.ExecutionTrace{Outcome: domain.OutcomeContextOnly}}, nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		close(release)
		synctest.Wait()
		got := j.Snapshot()
		if got.ID != first || got.Text != "FIRST_ANSWER" {
			t.Fatalf("100ms polling can miss completed answer; snapshot ID=%s purpose=%s text=%q", got.ID, got.Purpose, got.Text)
		}
	})
}
