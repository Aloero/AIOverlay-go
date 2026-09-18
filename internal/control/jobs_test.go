package control

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type fakeLive struct{}

func (fakeLive) Run(ctx context.Context, id string, in engine.Input, o engine.RunOptions) (engine.Result, error) {
	o.OnTrace(domain.TraceEvent{Kind: "attempt_start"})
	o.OnDelta("partial")
	<-ctx.Done()
	return engine.Result{Trace: domain.ExecutionTrace{PipelineID: id}}, fmt.Errorf("cancelled")
}
func TestJobRemainsLiveUntilCancelledAndRejectsOverlap(t *testing.T) {
	var jobs JobManager
	_, e := jobs.Start(context.Background(), fakeLive{}, "test", engine.Input{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = jobs.Start(context.Background(), fakeLive{}, "test", engine.Input{}); e == nil {
		t.Fatal("overlap accepted")
	}
	end := time.Now().Add(time.Second)
	for jobs.Snapshot().Text == "" && time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	if jobs.Snapshot().Text != "partial" {
		t.Fatal("no streaming update")
	}
	jobs.Cancel()
	for jobs.Snapshot().Running && time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	if jobs.Snapshot().Running || jobs.Snapshot().Error == "" {
		t.Fatal("did not cancel")
	}
}

func TestManualCommandQueueKeepsOnePendingTaskAndStableIDs(t *testing.T) {
	var jobs JobManager
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})

	firstRun := func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		close(firstStarted)
		<-releaseFirst
		return engine.Result{Text: "first", Trace: domain.ExecutionTrace{ID: "trace-first"}}, nil, nil
	}
	secondRun := func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		close(secondStarted)
		return engine.Result{Text: "second", Trace: domain.ExecutionTrace{ID: "trace-second"}}, nil, nil
	}

	firstID, err := jobs.queueTaskPurpose(context.Background(), "p", "command", nil, firstRun)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first manual command did not start")
	}
	secondID, err := jobs.queueTaskPurpose(context.Background(), "p", "command", nil, secondRun)
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatalf("manual command IDs collided: %q", firstID)
	}
	if snapshot := jobs.Snapshot(); snapshot.ID != firstID || snapshot.QueuedID != secondID {
		t.Fatalf("queue snapshot=%+v", snapshot)
	}
	if _, err := jobs.queueTaskPurpose(context.Background(), "p", "command", nil, secondRun); err == nil {
		t.Fatal("third manual command exceeded bounded one-item queue")
	}

	close(releaseFirst)
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("queued manual command did not start")
	}
	deadline := time.Now().Add(time.Second)
	for jobs.Snapshot().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	final := jobs.Snapshot()
	if final.ID != secondID || final.Text != "second" || final.QueuedID != "" {
		t.Fatalf("queued command result mixed with first: %+v", final)
	}
}

func TestClearDropsPendingManualCommand(t *testing.T) {
	var jobs JobManager
	started := make(chan struct{})
	release := make(chan struct{})
	var secondRuns atomic.Int32
	first := func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		close(started)
		<-release
		return engine.Result{}, nil, nil
	}
	second := func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		secondRuns.Add(1)
		return engine.Result{}, nil, nil
	}
	if _, err := jobs.queueTaskPurpose(context.Background(), "p", "command", nil, first); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := jobs.queueTaskPurpose(context.Background(), "p", "command", nil, second); err != nil {
		t.Fatal(err)
	}
	jobs.Clear()
	close(release)
	deadline := time.Now().Add(time.Second)
	for jobs.Snapshot().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if secondRuns.Load() != 0 || jobs.Snapshot().QueuedID != "" {
		t.Fatalf("cleared pending command ran: count=%d job=%+v", secondRuns.Load(), jobs.Snapshot())
	}
}

func TestClearCancelsPreparationAfterPendingManualBecomesActive(t *testing.T) {
	var jobs JobManager
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	prepareStarted := make(chan struct{})
	prepareCancelled := make(chan struct{})
	var secondRuns atomic.Int32

	first := func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		close(firstStarted)
		<-releaseFirst
		return engine.Result{}, nil, nil
	}
	second := func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		secondRuns.Add(1)
		return engine.Result{}, nil, nil
	}
	prepare := func(ctx context.Context) error {
		close(prepareStarted)
		<-ctx.Done()
		close(prepareCancelled)
		return ctx.Err()
	}
	if _, err := jobs.queueTaskPurpose(context.Background(), "p", "command", nil, first); err != nil {
		t.Fatal(err)
	}
	<-firstStarted
	queuedResult := make(chan error, 1)
	go func() {
		_, err := jobs.queueTaskPurpose(context.Background(), "p", "command", prepare, second)
		queuedResult <- err
	}()
	<-prepareStarted
	close(releaseFirst)

	deadline := time.Now().Add(time.Second)
	for {
		job := jobs.Snapshot()
		if job.Running && job.QueuedID == "" && job.ID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued manual did not become active: %+v", job)
		}
		time.Sleep(time.Millisecond)
	}
	jobs.Clear()
	select {
	case <-prepareCancelled:
	case <-time.After(time.Second):
		t.Fatal("Clear did not cancel active queued preparation")
	}
	select {
	case <-queuedResult:
	case <-time.After(time.Second):
		t.Fatal("queued StartCommand preparation remained blocked")
	}
	if secondRuns.Load() != 0 {
		t.Fatalf("cancelled queued run executed %d times", secondRuns.Load())
	}
}
