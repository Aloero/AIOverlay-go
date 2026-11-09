package session

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"aioverlay/internal/engine"
)

func TestPendingWaitIsScopedToPipelineGenerationAndCutoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := NewManager(nil)
		at := time.Now()
		for _, item := range []pendingSegment{
			{eventID: "other-pipeline", pipelineID: "other", contextID: "c", generation: 1, start: at},
			{eventID: "old-generation", pipelineID: "p", contextID: "c", generation: 1, start: at},
			{eventID: "future", pipelineID: "p", contextID: "c", generation: 2, start: at.Add(time.Second)},
			{eventID: "other-context", pipelineID: "p", contextID: "unselected", generation: 2, start: at},
		} {
			manager.addPending(item)
		}
		complete, err := manager.waitPendingContexts(t.Context(), "p", map[string]uint64{"c": 2}, at, time.Millisecond)
		if !complete || err != nil {
			t.Fatalf("unrelated pending blocked snapshot: complete=%v err=%v", complete, err)
		}
		manager.addPending(pendingSegment{eventID: "wanted", pipelineID: "p", contextID: "c", generation: 2, start: at})
		complete, err = manager.waitPendingContexts(t.Context(), "p", map[string]uint64{"c": 2}, at, time.Millisecond)
		if complete || err != nil {
			t.Fatalf("wait limit must yield incomplete, not cancellation: complete=%v err=%v", complete, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		complete, err = manager.waitPending(ctx, "p", "c", 2, at, time.Second)
		if complete || !errors.Is(err, context.Canceled) {
			t.Fatalf("complete=%v err=%v", complete, err)
		}
	})
}

func TestFinishingPendingWakesAllWaitersAndIsIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := NewManager(nil)
		at := time.Now()
		manager.addPending(pendingSegment{eventID: "speech", pipelineID: "p", contextID: "c", generation: 1, start: at})
		done := make(chan bool, 2)
		for range 2 {
			go func() {
				complete, err := manager.waitPending(t.Context(), "p", "c", 1, at, time.Second)
				done <- complete && err == nil
			}()
		}
		synctest.Wait()
		if !manager.finishPending("speech") || manager.finishPending("speech") {
			t.Fatal("pending ownership was not released exactly once")
		}
		for range 2 {
			if !<-done {
				t.Fatal("waiter was not notified of completion")
			}
		}
	})
}

func TestClearingPendingKeepsCurrentGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := NewManager(nil)
		at := time.Now()
		manager.addPending(pendingSegment{eventID: "old", pipelineID: "p", contextID: "c", generation: 1, start: at})
		manager.addPending(pendingSegment{eventID: "new", pipelineID: "p", contextID: "c", generation: 2, start: at})
		manager.clearPendingBeforeGeneration("p", "c", 2)
		if manager.finishPending("old") {
			t.Fatal("old generation survived clear")
		}
		if !manager.finishPending("new") {
			t.Fatal("clear deleted current generation")
		}
	})
}

func TestActiveTurnRegistryOwnsGenerationSnapshotAndCancelsOutsideLock(t *testing.T) {
	manager := NewManager(nil)
	generations := map[string]uint64{"c": 1}
	oldCancelled, newCancelled := false, false
	manager.registerActiveTurn(backgroundTurn{pipelineID: "p", contextGenerations: generations, input: engine.Input{EventID: "old"}}, func() {
		oldCancelled = true
		manager.unregisterActiveTurn("old")
	})
	manager.registerActiveTurn(backgroundTurn{pipelineID: "p", contextGenerations: map[string]uint64{"c": 2}, input: engine.Input{EventID: "new"}}, func() { newCancelled = true })
	generations["c"] = 2
	done := make(chan struct{})
	go func() { manager.cancelContextTurns("p", "c", 2); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation callback held registry lock")
	}
	if !oldCancelled || newCancelled {
		t.Fatalf("old cancelled=%v current cancelled=%v", oldCancelled, newCancelled)
	}
}
