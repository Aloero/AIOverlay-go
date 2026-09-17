package session

import (
	"context"
	"testing"
	"time"
)

func TestGovernorManualWaitsWithoutOvercommit(t *testing.T) {
	g := NewGovernor()
	g.Configure(1, 100)
	r, _ := g.Acquire(context.Background(), false)
	r.Commit()
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	done := make(chan struct{})
	go func() {
		x, e := g.Acquire(ctx, true)
		if e == nil {
			x.Commit()
			x.Release()
			close(done)
		}
	}()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("manual overcommitted")
	default:
	}
	r.Release()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("manual did not acquire after release")
	}
}
func TestGovernorPriorityBlocksNewBackground(t *testing.T) {
	g := NewGovernor()
	g.Configure(1, 100)
	r, _ := g.Acquire(context.Background(), false)
	r.Commit()
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	order := make(chan string, 2)
	go func() {
		x, e := g.Acquire(ctx, true)
		if e != nil {
			return
		}
		x.Commit()
		order <- "manual"
		time.Sleep(10 * time.Millisecond)
		x.Release()
	}()
	time.Sleep(10 * time.Millisecond)
	go func() {
		x, e := g.Acquire(ctx, false)
		if e != nil {
			return
		}
		x.Commit()
		order <- "background"
		x.Release()
	}()
	r.Release()
	if got := <-order; got != "manual" {
		t.Fatalf("first=%s", got)
	}
	select {
	case got := <-order:
		if got != "background" {
			t.Fatalf("second=%s", got)
		}
	case <-ctx.Done():
		t.Fatal("background never resumed")
	}
}

func TestGovernorReservationCountsOnlyCommittedCalls(t *testing.T) {
	g := NewGovernor()
	g.Configure(1, 1)

	reserved, err := g.Acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := g.Snapshot()
	if snapshot["calls_last_minute"] != 0 || snapshot["calls_reserved"] != 1 {
		t.Fatalf("reservation counted as dispatch: %+v", snapshot)
	}
	reserved.Release()

	dispatched, err := g.Acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	dispatched.Commit()
	dispatched.Commit()
	dispatched.Release()
	dispatched.Release()
	snapshot = g.Snapshot()
	if snapshot["calls_last_minute"] != 1 || snapshot["calls_reserved"] != 0 || snapshot["active"] != 0 {
		t.Fatalf("committed dispatch accounting is wrong: %+v", snapshot)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = g.Acquire(ctx, false); err == nil {
		t.Fatal("rate limit ignored committed dispatch")
	}
}
