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
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	done := make(chan struct{})
	go func() {
		x, e := g.Acquire(ctx, true)
		if e == nil {
			x()
			close(done)
		}
	}()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("manual overcommitted")
	default:
	}
	r()
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
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	order := make(chan string, 2)
	go func() {
		x, e := g.Acquire(ctx, true)
		if e != nil {
			return
		}
		order <- "manual"
		time.Sleep(10 * time.Millisecond)
		x()
	}()
	time.Sleep(10 * time.Millisecond)
	go func() {
		x, e := g.Acquire(ctx, false)
		if e != nil {
			return
		}
		order <- "background"
		x()
	}()
	r()
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
