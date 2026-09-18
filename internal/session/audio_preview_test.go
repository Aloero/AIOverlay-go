package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/domain"
	paudio "aioverlay/internal/platform/audio"
)

func TestPreviewRefusesNativeCaptureStillReleasingAfterPause(t *testing.T) {
	manager := NewManager(nil)
	entered, release, nativeDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		if calls.Add(1) != 1 {
			return errors.New("overlapping native capture")
		}
		close(entered)
		<-ctx.Done()
		<-release // Simulate native teardown still holding the device.
		close(nativeDone)
		return ctx.Err()
	})
	manager.Start(context.Background(), managerConfig())
	t.Cleanup(func() { close(release); manager.Close(); <-nativeDone })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("audio capture did not start")
	}
	manager.Pause(true)
	_, err := manager.PreviewAudio(context.Background(), domain.AudioSourceConfig{Mode: "system"}, time.Second)
	if err == nil || calls.Load() != 1 {
		t.Fatalf("preview overlapped native teardown: calls=%d err=%v", calls.Load(), err)
	}
}

func TestPreviewExcludesAnotherPreviewAndBackgroundStart(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Sources[0].AutoStart = false
	manager.Start(context.Background(), cfg)
	defer manager.Close()
	entered, finished := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		if calls.Add(1) != 1 {
			return errors.New("overlapping native capture")
		}
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); <-finished })
	go func() {
		defer close(finished)
		_, _ = manager.PreviewAudio(ctx, domain.AudioSourceConfig{Mode: "system"}, time.Second)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("preview did not start")
	}
	_, err := manager.PreviewAudio(context.Background(), domain.AudioSourceConfig{Mode: "system"}, time.Second)
	if err == nil || calls.Load() != 1 {
		t.Fatalf("second preview entered capture: calls=%d err=%v", calls.Load(), err)
	}
	if err := manager.SetSourceRunning("a", true); err == nil {
		t.Fatal("background Start was accepted while preview owns capture")
	}
}

func TestPreviewCancellationDoesNotReturnPartialRecording(t *testing.T) {
	manager := NewManager(nil)
	ctx, cancel := context.WithCancel(context.Background())
	manager.SetAudioCapture(func(_ context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		emit(paudio.Block{Samples: make([]int16, 1600), Rate: 16000})
		cancel()
		return ctx.Err()
	})
	preview, err := manager.PreviewAudio(ctx, domain.AudioSourceConfig{Mode: "system"}, time.Second)
	if !errors.Is(err, context.Canceled) || len(preview.PCM16) != 0 {
		t.Fatalf("cancelled preview returned recording: samples=%d err=%v", len(preview.PCM16), err)
	}
	if manager.captureGate.previewActive() {
		t.Fatal("cancelled preview retained capture lease")
	}
}

func TestPreviewHasItsOwnBoundedCaptureDeadline(t *testing.T) {
	manager := NewManager(nil)
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 7*time.Second {
			t.Fatal("preview depends on a caller timeout or accepts excessive capture time")
		}
		return errors.New("no device")
	})
	_, _ = manager.PreviewAudio(context.Background(), domain.AudioSourceConfig{Mode: "system"}, 24*time.Hour)
	if manager.captureGate.previewActive() {
		t.Fatal("failed preview retained capture lease")
	}
}

func TestPreviewCancelledBeforeCaptureDoesNotOpenDevice(t *testing.T) {
	manager := NewManager(nil)
	manager.SetAudioCapture(func(context.Context, domain.AudioSourceConfig, func(paudio.Block)) error {
		t.Fatal("cancelled preview opened a capture device")
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := manager.PreviewAudio(ctx, domain.AudioSourceConfig{Mode: "system"}, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preview: %v", err)
	}
}

func TestPreviewRejectsInvalidSampleRateBeforeAllocation(t *testing.T) {
	manager := NewManager(nil)
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		emit(paudio.Block{Samples: []int16{1}, Rate: -1})
		return ctx.Err()
	})
	preview, err := manager.PreviewAudio(context.Background(), domain.AudioSourceConfig{Mode: "system"}, time.Second)
	if err == nil || len(preview.PCM16) != 0 {
		t.Fatal("invalid preview sample rate was accepted")
	}
}
