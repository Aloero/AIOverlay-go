package session

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	paudio "aioverlay/internal/platform/audio"
)

func TestSessionMixedLifecycleSoakKeepsStateBounded(t *testing.T) {
	const restartCycles = 60

	cfg := managerConfig()
	pipeline := &cfg.Pipelines[0]
	pipeline.Contexts[0].MaxEntries = 20
	pipeline.Commands[0].ContextID = "c"
	pipeline.Sources[0].Audio.DeviceID = ""
	pipeline.Sources[0].Audio.SegmentMS = 100
	pipeline.Sources[0].Audio.MinimumMS = 80

	var transcriptSeq atomic.Uint64
	manager := NewManager(func(_ context.Context, _ string, input engine.Input, options engine.RunOptions) (engine.Result, error) {
		if input.Audio != nil && options.OnObservation != nil {
			sequence := transcriptSeq.Add(1)
			options.OnObservation(engine.Observation{
				Kind:      "audio.text",
				Text:      fmt.Sprintf("turn-%d", sequence),
				SegmentID: input.EventID,
				Revision:  1,
				Final:     true,
			})
		}
		return engine.Result{Trace: domain.ExecutionTrace{Outcome: domain.OutcomeContextOnly}}, nil
	})
	manager.audioRestartBaseDelay = 0
	manager.audioRestartMaxDelay = 0
	manager.SetCapture(func(context.Context) ([]byte, error) { return []byte("manual-screen"), nil }, nil)

	attemptStarted := make(chan int, restartCycles+1)
	invalidate := make(chan struct{}, restartCycles)
	var attempts atomic.Int32
	base := time.Now()
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		attempt := int(attempts.Add(1))
		if attempt <= restartCycles {
			voice := make([]int16, 1600)
			for index := range voice {
				voice[index] = 12000
			}
			emit(paudio.Block{Samples: voice, Rate: 16000, At: base.Add(time.Duration(attempt) * time.Second)})
		}
		attemptStarted <- attempt
		if attempt > restartCycles {
			<-ctx.Done()
			return ctx.Err()
		}
		select {
		case <-invalidate:
			return &paudio.CaptureError{
				Operation: "get buffer",
				HRESULT:   0x88890004,
				Kind:      paudio.CaptureErrorDeviceInvalidated,
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() {
		cancel()
		manager.Close()
	}()

	var concurrent sync.WaitGroup
	errCh := make(chan error, 2)
	concurrent.Add(2)
	go func() {
		defer concurrent.Done()
		for index := 0; index < restartCycles/6; index++ {
			if _, err := manager.ClearContext("c"); err != nil {
				errCh <- fmt.Errorf("clear context: %w", err)
				return
			}
		}
	}()
	go func() {
		defer concurrent.Done()
		for index := 0; index < restartCycles; index++ {
			prepared, err := manager.PrepareCommand(context.Background(), "analyze")
			if err != nil {
				errCh <- fmt.Errorf("prepare manual command: %w", err)
				return
			}
			if string(prepared.Input.ImageJPEG) != "manual-screen" || prepared.Input.Intent != string(domain.IntentRespond) {
				errCh <- fmt.Errorf("manual snapshot changed during soak")
				return
			}
		}
	}()

	for expected := 1; expected <= restartCycles; expected++ {
		select {
		case attempt := <-attemptStarted:
			if attempt != expected {
				t.Fatalf("capture attempt=%d want=%d", attempt, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("capture attempt %d did not start", expected)
		}
		invalidate <- struct{}{}
	}

	select {
	case attempt := <-attemptStarted:
		if attempt != restartCycles+1 {
			t.Fatalf("stable capture attempt=%d", attempt)
		}
	case <-time.After(time.Second):
		t.Fatal("stable capture did not start after restart soak")
	}

	concurrent.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	waitUntil(t, func() bool {
		manager.pendingMu.Lock()
		defer manager.pendingMu.Unlock()
		return len(manager.pending) == 0
	}, "pending transcripts remained after soak")
	waitUntil(t, func() bool {
		manager.activeTurnMu.Lock()
		defer manager.activeTurnMu.Unlock()
		return len(manager.activeTurns) == 0
	}, "active background turns remained after soak")

	snapshot := manager.contexts.Snapshot(pipeline.Contexts[0], pipeline.ID, base.Add((restartCycles+2)*time.Second))
	if len(snapshot.Entries) > pipeline.Contexts[0].MaxEntries {
		t.Fatalf("context exceeded configured bound: %d > %d", len(snapshot.Entries), pipeline.Contexts[0].MaxEntries)
	}
	if runtime := manager.source("a"); runtime == nil || runtime.epoch != restartCycles+1 || sourceFinished(runtime) {
		t.Fatalf("unexpected stable runtime after soak: %#v", runtime)
	}

	if err := manager.SetSourceRunning("a", false); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.source("a") == nil }, "source did not stop after soak")
	manager.pendingMu.Lock()
	pending := len(manager.pending)
	manager.pendingMu.Unlock()
	manager.activeTurnMu.Lock()
	active := len(manager.activeTurns)
	manager.activeTurnMu.Unlock()
	if pending != 0 || active != 0 {
		t.Fatalf("runtime state leaked after stop: pending=%d active=%d", pending, active)
	}
}
