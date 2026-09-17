package control

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"context"
	"fmt"
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
