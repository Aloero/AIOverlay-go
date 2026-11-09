package session

import (
	"context"
	"testing"
	"time"

	"aioverlay/internal/domain"
	paudio "aioverlay/internal/platform/audio"
)

func TestClearContextDiscardsSpeechStillInSegmenter(t *testing.T) {
	cfg := managerConfig()
	manager := NewManager(nil)
	manager.cfg = cfg
	manager.started = true
	runtime := &sourceRuntime{cfg: cfg.Pipelines[0].Sources[0], epoch: 1}
	manager.sources[runtime.cfg.ID] = runtime
	queue := newAudioSegmentQueue(4, maxQueuedAudioPCMBytes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	capture := func(_ context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		voice := make([]int16, 1600)
		for i := range voice {
			voice[i] = 12000
		}
		start := time.Now()
		emit(paudio.Block{Samples: voice, Rate: 16000, At: start})
		if _, err := manager.ClearContext("c"); err != nil {
			t.Fatal(err)
		}
		// Silence completes the old phrase after Clear, before any STT request.
		emit(paudio.Block{Rate: 16000, At: start.Add(time.Second)})
		cancel()
		return ctx.Err()
	}
	_ = manager.produceAudioSegments(ctx, "p", runtime, capture, queue)
	if len(queue.items) != 0 || queue.QueuedBytes() != 0 {
		t.Fatal("speech captured before Clear remained eligible for transcription in the new conversation")
	}
}

func TestClearContextRejectsAudioAlreadyInCaptureQueue(t *testing.T) {
	cfg := managerConfig()
	manager := NewManager(nil)
	manager.cfg = cfg
	manager.started = true
	runtime := &sourceRuntime{cfg: cfg.Pipelines[0].Sources[0], epoch: 1, turns: make(chan backgroundTurn, 1)}
	manager.sources[runtime.cfg.ID] = runtime
	queue := newAudioSegmentQueue(4, maxQueuedAudioPCMBytes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capture := func(_ context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		voice := make([]int16, 16000)
		for i := range voice {
			voice[i] = 12000
		}
		emit(paudio.Block{Samples: voice, Rate: 16000, At: time.Now()})
		cancel()
		return ctx.Err()
	}
	_ = manager.produceAudioSegments(ctx, "p", runtime, capture, queue)
	if len(queue.items) != 1 {
		t.Fatal("test needs one complete segment waiting for processing")
	}
	if _, err := manager.ClearContext("c"); err != nil {
		t.Fatal(err)
	}
	captured := <-queue.items
	queue.Release(captured)
	manager.processAudioSegment(context.Background(), "p", runtime, captured)
	if len(runtime.turns) != 0 || len(manager.pending.segments) != 0 {
		t.Fatal("old queued PCM was relabelled with the current context generation")
	}
}

func TestClearContextStartsNextPhraseWithoutOldPrerollOrSpeech(t *testing.T) {
	cfg := managerConfig()
	manager := NewManager(nil)
	manager.cfg = cfg
	runtime := &sourceRuntime{cfg: cfg.Pipelines[0].Sources[0]}
	queue := newAudioSegmentQueue(4, maxQueuedAudioPCMBytes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capture := func(_ context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		start := time.Now()
		oldVoice, newVoice := make([]int16, 1600), make([]int16, 1600)
		for i := range oldVoice {
			oldVoice[i], newVoice[i] = 9000, 14000
		}
		emit(paudio.Block{Samples: oldVoice, Rate: 16000, At: start})
		if _, err := manager.ClearContext("c"); err != nil {
			t.Fatal(err)
		}
		emit(paudio.Block{Samples: newVoice, Rate: 16000, At: start.Add(time.Second)})
		emit(paudio.Block{Rate: 16000, At: start.Add(2 * time.Second)})
		cancel()
		return ctx.Err()
	}
	_ = manager.produceAudioSegments(ctx, "p", runtime, capture, queue)
	if len(queue.items) != 1 {
		t.Fatalf("new conversation segments=%d, want 1", len(queue.items))
	}
	captured := <-queue.items
	if captured.context.generation != 2 || len(captured.segment.Clip.PCM16) != 1600 {
		t.Fatalf("new phrase has wrong generation or size: %d, %d", captured.context.generation, len(captured.segment.Clip.PCM16))
	}
	for _, sample := range captured.segment.Clip.PCM16 {
		if sample != 14000 {
			t.Fatal("old conversation audio crossed Clear in the segmenter")
		}
	}
}

func TestOldAudioGapDoesNotRepopulateClearedContext(t *testing.T) {
	cfg := managerConfig()
	manager := NewManager(nil)
	manager.cfg = cfg
	runtime := &sourceRuntime{cfg: cfg.Pipelines[0].Sources[0]}
	captured := manager.captureAudioContext("p", runtime)
	if _, err := manager.ClearContext("c"); err != nil {
		t.Fatal(err)
	}
	manager.recordAudioGap(captured, runtime, time.Now(), "old queue overflow")
	snapshot := manager.contexts.Snapshot(captured.config, "p", time.Now())
	if len(snapshot.Entries) != 0 {
		t.Fatal("old capture failure inserted a gap into a cleared conversation")
	}
}

func TestOldPipelineAudioCannotCrossActivePipelineSwitch(t *testing.T) {
	cfg := managerConfig()
	second := cfg.Pipelines[0]
	second.ID = "p2"
	cfg.Pipelines = append(cfg.Pipelines, second)

	manager := NewManager(nil)
	manager.cfg = cfg
	manager.started = true
	runtime := &sourceRuntime{
		pipelineID: "p", cfg: cfg.Pipelines[0].Sources[0], epoch: 1,
		turns: make(chan backgroundTurn, 1),
	}
	manager.sources[runtime.cfg.ID] = runtime

	captured := capturedAudioSegment{
		segment: paudio.Segment{
			Started: time.Now(), Ended: time.Now().Add(100 * time.Millisecond),
			Clip: domain.AudioClip{PCM16: make([]int16, 1600), SampleRate: 16000, Channels: 1, Final: true},
		},
		context: manager.captureAudioContext("p", runtime),
	}
	if !manager.audioContextCurrent(captured.context) {
		t.Fatal("test capture was not current before pipeline switch")
	}

	manager.cfg.ActivePipelineID = "p2"
	if manager.audioContextCurrent(captured.context) {
		t.Fatal("old pipeline PCM remained current after active pipeline switch")
	}
	manager.processAudioSegment(context.Background(), "p", runtime, captured)
	if len(runtime.turns) != 0 || len(manager.pending.segments) != 0 {
		t.Fatal("old pipeline PCM crossed into the new active pipeline")
	}
}

func TestStaleAudioGapDoesNotOverwriteCurrentDiagnostics(t *testing.T) {
	cfg := managerConfig()
	manager := NewManager(nil)
	manager.cfg = cfg
	runtime := &sourceRuntime{pipelineID: "p", cfg: cfg.Pipelines[0].Sources[0]}
	captured := manager.captureAudioContext("p", runtime)
	if _, err := manager.ClearContext("c"); err != nil {
		t.Fatal(err)
	}
	runtime.status.LastDiagnostic = "current diagnostic"
	manager.recordAudioGap(captured, runtime, time.Now(), "stale gap")
	if runtime.status.LastDiagnostic != "current diagnostic" || runtime.status.Dropped != 0 {
		t.Fatalf("stale generation changed current source status: %+v", runtime.status)
	}
}

func TestTimestampErrorRecordsGapWithoutQueuingCorruptSpeech(t *testing.T) {
	cfg := managerConfig()
	manager := NewManager(nil)
	manager.cfg = cfg
	runtime := &sourceRuntime{cfg: cfg.Pipelines[0].Sources[0]}
	queue := newAudioSegmentQueue(4, maxQueuedAudioPCMBytes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capture := func(_ context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		voice := make([]int16, 16000)
		for i := range voice {
			voice[i] = 12000
		}
		emit(paudio.Block{Samples: voice, Rate: 16000, At: time.Now(), TimestampError: true})
		cancel()
		return ctx.Err()
	}
	_ = manager.produceAudioSegments(ctx, "p", runtime, capture, queue)
	if len(queue.items) != 0 {
		t.Fatal("timestamp-error packet was queued for an ordinary AI turn")
	}
	snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now())
	if len(snapshot.Entries) != 1 || !snapshot.Entries[0].Gap {
		t.Fatal("discarded timestamp-error audio was not recorded as a gap")
	}
	if runtime.status.LastDiagnostic != "audio timestamp error: packet discarded and transcript gap recorded" {
		t.Fatalf("discard reason=%q", runtime.status.LastDiagnostic)
	}
}
