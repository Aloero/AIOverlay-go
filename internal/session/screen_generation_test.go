package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"aioverlay/internal/domain"
)

func TestManualCaptureCannotRepopulateConversationClearedDuringCapture(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].Commands = []domain.CommandConfig{{ID: "context-command", Action: "screen_context", EntryNodeID: "context", Intent: domain.IntentContext, ContextID: "c", CaptureScreen: true}}
	manager := NewManager(nil)
	manager.cfg = cfg
	manager.capture = func(context.Context) ([]byte, error) {
		if _, err := manager.ClearContext("c"); err != nil {
			t.Fatal(err)
		}
		return []byte("old-conversation-frame"), nil
	}
	_, _ = manager.PrepareCommand(context.Background(), "screen_context")
	snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now())
	if len(snapshot.ImageJPEG) != 0 {
		t.Fatal("capture callback populated a conversation cleared after the command began")
	}
}

func TestClearedGenerationRejectsLateBackgroundFrame(t *testing.T) {
	cfg := managerConfig()
	manager := NewManager(nil)
	manager.cfg = cfg
	manager.started = true
	runtime := &sourceRuntime{cfg: domain.EventSource{ID: "timer", Kind: domain.SourceScreenTimer, Intent: domain.IntentDecide, ContextID: "c", EntryNodeID: "decision"}, epoch: 1, turns: make(chan backgroundTurn, 1)}
	manager.sources["timer"] = runtime
	generation := manager.contexts.Generation("p", "c")
	if _, err := manager.ClearContext("c"); err != nil {
		t.Fatal(err)
	}
	manager.processScreenFrame("p", runtime, cfg, []byte("late-frame"), time.Now(), generation)
	if len(runtime.turns) != 0 {
		t.Fatal("late frame started a turn after conversation clear")
	}
	snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now())
	if len(snapshot.ImageJPEG) != 0 || runtime.status.LastEvent != "" {
		t.Fatal("late frame changed the cleared context or source event state")
	}
}

func TestManualContextClearDuringCaptureCancelsBeforeAI(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].Commands[0].ContextID = "c"
	manager := NewManager(nil)
	manager.cfg = cfg
	manager.capture = func(context.Context) ([]byte, error) {
		if _, err := manager.ClearContext("c"); err != nil {
			t.Fatal(err)
		}
		return []byte("manual-frame"), nil
	}
	_, err := manager.PrepareCommand(context.Background(), "analyze")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cleared context did not cancel preparation: %v", err)
	}
}

func TestCancelledBackgroundCaptureDoesNotReturnFrame(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	capture := func(context.Context) ([]byte, error) { cancel(); return []byte("late-frame"), nil }
	image, _, err := captureBackgroundFrame(ctx, capture)
	if !errors.Is(err, context.Canceled) || len(image) != 0 {
		t.Fatalf("cancelled capture returned frame: %q %v", image, err)
	}
}

func TestScreenTimerClearDuringCaptureKeepsNextUnchangedFrameEligible(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := managerConfig()
		cfg.Pipelines[0].Sources = []domain.EventSource{{ID: "timer", Kind: domain.SourceScreenTimer, Enabled: true, AutoStart: true, Intent: domain.IntentContext, ContextID: "c", EntryNodeID: "context", IntervalMS: 1000, ChangedOnly: true}}
		manager := NewManager(nil)
		var captures atomic.Int32
		manager.SetCapture(nil, func(context.Context) ([]byte, error) {
			if captures.Add(1) == 1 {
				if _, err := manager.ClearContext("c"); err != nil {
					return nil, err
				}
			}
			return []byte("unchanged-screen"), nil
		})
		manager.Start(t.Context(), cfg)
		defer manager.Close()
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now())
		if len(snapshot.ImageJPEG) != 0 {
			t.Fatal("in-flight frame repopulated cleared context")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		snapshot = manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now())
		if captures.Load() != 2 || string(snapshot.ImageJPEG) != "unchanged-screen" {
			t.Fatalf("change detector suppressed new-generation frame: captures=%d image=%q", captures.Load(), snapshot.ImageJPEG)
		}
	})
}
