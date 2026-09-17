package session

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	paudio "aioverlay/internal/platform/audio"
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func managerConfig() domain.Config {
	return domain.Config{ActivePipelineID: "p", Pipelines: []domain.Pipeline{{
		ID: "p", Enabled: true, EntryNodeID: "screen", OutputNodeID: "output",
		Budget:        domain.ExecutionBudget{MaxParallelCalls: 2, MaxWallTimeMS: 10000},
		Commands:      []domain.CommandConfig{{ID: "answer", Action: "analyze", EntryNodeID: "screen", Intent: domain.IntentRespond, CaptureScreen: true}},
		Contexts:      []domain.ContextConfig{{ID: "c", Name: "c", Enabled: true, MaxAgeS: 60, MaxEntries: 10, MaxChars: 1000, IncludeTranscript: true, IncludeAnswers: true, KeepLatestScreen: true, ScreenMaxAgeS: 60}},
		Sources:       []domain.EventSource{{ID: "a", Name: "a", Kind: domain.SourceAudio, Enabled: true, AutoStart: true, EntryNodeID: "audio", Intent: domain.IntentContext, ContextID: "c", Audio: &domain.AudioSourceConfig{Mode: "system", SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01}}},
		SessionBudget: &domain.SessionBudget{CallsPerMinute: 60, QueueCapacity: 2, MaxParallelCalls: 2, EndToEndMS: 10000},
	}}}
}

func TestPrepareLegacyStyleCommandHasNoContextUnlessSelected(t *testing.T) {
	m := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Commands[0].ContextID = ""
	m.cfg = cfg
	m.capture = func(context.Context) ([]byte, error) { return []byte("screen"), nil }
	p, e := m.PrepareCommand(context.Background(), "analyze")
	if e != nil || string(p.Input.ImageJPEG) != "screen" || p.Input.ContextText != "" {
		t.Fatalf("%+v %v", p, e)
	}
}
func TestContextCommandDoesNotRunModel(t *testing.T) {
	m := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Commands = append(cfg.Pipelines[0].Commands, domain.CommandConfig{ID: "ctx", Action: "screen_context", EntryNodeID: "screen", Intent: domain.IntentContext, ContextID: "c", CaptureScreen: true})
	m.cfg = cfg
	m.capture = func(context.Context) ([]byte, error) { return []byte("screen"), nil }
	p, e := m.PrepareCommand(context.Background(), "screen_context")
	if e != nil || !p.SkipRun {
		t.Fatalf("%+v %v", p, e)
	}
	snap := m.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now())
	if string(snap.ImageJPEG) != "screen" {
		t.Fatal("screen not stored")
	}
}
func TestAudioCaptureDoesNotBlockOnSlowExecutor(t *testing.T) {
	cfg := managerConfig()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	m := NewManager(func(ctx context.Context, _ string, _ engine.Input, _ engine.RunOptions) (engine.Result, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return engine.Result{}, nil
	})
	m.SetAudioCapture(func(ctx context.Context, c domain.AudioSourceConfig, emit func(paudio.Block)) error {
		samples := make([]int16, 1600)
		for i := range samples {
			samples[i] = 10000
		}
		for i := 0; i < 5; i++ {
			emit(paudio.Block{Samples: samples, Rate: 16000, At: time.Now().Add(time.Duration(i) * 100 * time.Millisecond)})
		}
		emit(paudio.Block{Rate: 16000, At: time.Now().Add(time.Second)})
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx, cfg)
	defer func() { close(release); cancel(); m.Close() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background turn did not start")
	}
	if m.Status()["sources"] == nil {
		t.Fatal("status missing")
	}
}

func TestManualCommandWaitsOnlyForPendingTranscriptAtCutoff(t *testing.T) {
	m := NewManager(nil)
	cfg := managerConfig()
	cmd := &cfg.Pipelines[0].Commands[0]
	cmd.ContextID = "c"
	cmd.WaitForTranscriptMS = 200
	m.cfg = cfg
	m.capture = func(context.Context) ([]byte, error) { return []byte("screen"), nil }
	ctxCfg := cfg.Pipelines[0].Contexts[0]
	gen := m.contexts.Generation("p", "c")
	start := time.Now().Add(-time.Second)
	m.addPending(pendingSegment{pipelineID: "p", contextID: "c", eventID: "seg", generation: gen, start: start, end: start.Add(2 * time.Second)})
	go func() {
		time.Sleep(25 * time.Millisecond)
		m.contexts.Append(ctxCfg, "p", gen, ContextEntry{ID: "seg", Kind: "transcript", Text: "question before hotkey", At: start, Start: start, End: start.Add(2 * time.Second), SourceID: "audio"})
		m.finishPending("seg")
	}()
	prepared, err := m.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Input.ContextIncomplete {
		t.Fatal("completed transcript marked incomplete")
	}
	if prepared.Input.ContextText != "[transcript/audio] question before hotkey" {
		t.Fatalf("context=%q", prepared.Input.ContextText)
	}
}
func TestManualCommandTranscriptWaitIsBounded(t *testing.T) {
	m := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Commands[0].ContextID = "c"
	cfg.Pipelines[0].Commands[0].WaitForTranscriptMS = 20
	m.cfg = cfg
	m.capture = func(context.Context) ([]byte, error) { return []byte("screen"), nil }
	gen := m.contexts.Generation("p", "c")
	m.addPending(pendingSegment{pipelineID: "p", contextID: "c", eventID: "stuck", generation: gen, start: time.Now().Add(-time.Second)})
	started := time.Now()
	prepared, err := m.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Input.ContextIncomplete {
		t.Fatal("timeout not disclosed")
	}
	if time.Since(started) > 200*time.Millisecond {
		t.Fatal("manual wait was not bounded")
	}
}

func TestLegacyPipelineDoesNotStartAudioCapture(t *testing.T) {
	m := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Sources = nil
	var calls atomic.Int32
	m.SetAudioCapture(func(context.Context, domain.AudioSourceConfig, func(paudio.Block)) error { calls.Add(1); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx, cfg)
	time.Sleep(20 * time.Millisecond)
	cancel()
	m.Close()
	if calls.Load() != 0 {
		t.Fatalf("audio capture started %d times", calls.Load())
	}
}

func TestAudioPreviewRefusesConcurrentLiveCapture(t *testing.T) {
	m := NewManager(nil)
	m.sources["a"] = &sourceRuntime{cfg: domain.EventSource{ID: "a", Kind: domain.SourceAudio}}
	_, err := m.PreviewAudio(context.Background(), domain.AudioSourceConfig{Mode: "system"}, time.Second)
	if err == nil {
		t.Fatal("preview opened over live audio source")
	}
}

func TestAudioTimestampErrorRecordsContextGap(t *testing.T) {
	m := NewManager(nil)
	cfg := managerConfig()
	emitted := make(chan struct{})
	m.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		emit(paudio.Block{Rate: 16000, At: time.Now(), TimestampError: true})
		close(emitted)
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx, cfg)
	defer func() { cancel(); m.Close() }()
	select {
	case <-emitted:
	case <-time.After(time.Second):
		t.Fatal("audio source did not emit timestamp error")
	}
	snap := m.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(time.Second))
	if len(snap.Entries) != 1 || !snap.Entries[0].Gap {
		t.Fatalf("timestamp error gap missing: %+v", snap.Entries)
	}
	if snap.Text != "[context gap]" {
		t.Fatalf("context=%q", snap.Text)
	}
}

func TestAudioCaptureFailureFlushesTailAndRecordsGap(t *testing.T) {
	cfg := managerConfig()
	type processedAudio struct {
		clip   domain.AudioClip
		intent string
	}
	processed := make(chan processedAudio, 1)
	m := NewManager(func(_ context.Context, _ string, input engine.Input, opt engine.RunOptions) (engine.Result, error) {
		if input.Audio == nil {
			return engine.Result{}, nil
		}
		if opt.OnObservation != nil {
			opt.OnObservation(engine.Observation{Kind: "audio.text", Text: "truncated phrase"})
		}
		processed <- processedAudio{clip: *input.Audio, intent: input.Intent}
		return engine.Result{}, nil
	})
	m.SetAudioCapture(func(_ context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		voice := make([]int16, 1600)
		for i := range voice {
			voice[i] = 12000
		}
		emit(paudio.Block{Samples: voice, Rate: 16000, At: time.Now()})
		return fmt.Errorf("device invalidated")
	})

	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx, cfg)
	defer func() {
		cancel()
		m.Close()
	}()

	select {
	case got := <-processed:
		if got.clip.Final {
			t.Fatal("interrupted audio tail was marked final")
		}
		if got.intent != string(domain.IntentContext) {
			t.Fatalf("interrupted audio intent=%q, want context", got.intent)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupted audio tail was not offered to the configured route")
	}

	snapshot := m.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(time.Second))
	if len(snapshot.Entries) < 2 {
		t.Fatalf("expected transcript and gap, got %+v", snapshot.Entries)
	}
	transcript := snapshot.Entries[0]
	gap := snapshot.Entries[len(snapshot.Entries)-1]
	if transcript.Text != "truncated phrase" || transcript.Final || !gap.Gap {
		t.Fatalf("expected incomplete transcript followed by gap, got %+v", snapshot.Entries)
	}
}

func TestStoppedAudioSourceCanBeStartedAgain(t *testing.T) {
	cfg := managerConfig()
	var attempts atomic.Int32
	m := NewManager(nil)
	m.SetAudioCapture(func(context.Context, domain.AudioSourceConfig, func(paudio.Block)) error {
		attempts.Add(1)
		return fmt.Errorf("device invalidated")
	})

	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx, cfg)
	defer func() {
		cancel()
		m.Close()
	}()

	waitUntil(t, func() bool { return attempts.Load() == 1 }, "initial capture did not start")
	var stoppedRuntime *sourceRuntime
	waitUntil(t, func() bool {
		stoppedRuntime = m.source("a")
		return stoppedRuntime != nil && sourceFinished(stoppedRuntime)
	}, "failed source runtime did not finish")
	waitUntil(t, func() bool {
		select {
		case <-stoppedRuntime.turnDone:
			return true
		default:
			return false
		}
	}, "turn queue leaked after capture stopped")

	if err := m.SetSourceRunning("a", true); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return attempts.Load() == 2 }, "explicit restart did not create a new capture runtime")
}

func waitUntil(t *testing.T, condition func() bool, failure string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(failure)
}

func TestBackgroundOutcomeDistinguishesNoActionAndResponse(t *testing.T) {
	noAction := engine.Result{Trace: domain.ExecutionTrace{Outcome: domain.OutcomeNoAction}}
	if got := backgroundOutcome(noAction); got != domain.OutcomeNoAction {
		t.Fatalf("no_action became %q", got)
	}

	response := engine.Result{Published: true, Text: "answer", Trace: domain.ExecutionTrace{Outcome: domain.OutcomeSuccess}}
	if got := backgroundOutcome(response); got != domain.OutcomeRespond {
		t.Fatalf("published answer became %q", got)
	}
}

func TestSourceOutcomeKeepsSemanticSilenceSeparateFromError(t *testing.T) {
	runtime := &sourceRuntime{}
	setSourceOutcome(runtime, domain.OutcomeNoAction, nil)
	if runtime.status.LastOutcome != string(domain.OutcomeNoAction) || runtime.status.LastError != "" {
		t.Fatalf("no_action status=%+v", runtime.status)
	}

	setSourceOutcome(runtime, domain.OutcomeRateLimited, fmt.Errorf("provider unavailable"))
	if runtime.status.LastOutcome != string(domain.OutcomeRateLimited) || runtime.status.LastError != "provider unavailable" {
		t.Fatalf("error status=%+v", runtime.status)
	}
}

func TestCoalescedAudioTurnRecordsContextGap(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	contextCfg := cfg.Pipelines[0].Contexts[0]
	generation := manager.contexts.Generation("p", contextCfg.ID)
	runtime := &sourceRuntime{cfg: cfg.Pipelines[0].Sources[0]}
	turn := backgroundTurn{
		pipelineID: "p",
		ctxCfg:     contextCfg,
		generation: generation,
		input:      engine.Input{EventID: "audio-event", Audio: &domain.AudioClip{PCM16: []int16{1}, SampleRate: 16000, Channels: 1}},
		at:         time.Now(),
	}

	manager.addPending(pendingSegment{pipelineID: "p", contextID: contextCfg.ID, eventID: "audio-event", generation: generation})
	manager.recordCoalescedTurn(runtime, turn)

	snapshot := manager.contexts.Snapshot(contextCfg, "p", time.Now().Add(time.Second))
	if len(snapshot.Entries) != 1 || !snapshot.Entries[0].Gap {
		t.Fatalf("audio loss must be visible as a context gap: %+v", snapshot.Entries)
	}
	if _, exists := manager.pending["audio-event"]; exists {
		t.Fatal("coalesced audio event remained pending")
	}
}

func TestCoalescedScreenTurnDoesNotInventTranscriptGap(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	contextCfg := cfg.Pipelines[0].Contexts[0]
	generation := manager.contexts.Generation("p", contextCfg.ID)
	runtime := &sourceRuntime{cfg: domain.EventSource{ID: "screen", Kind: domain.SourceScreenTimer}}
	turn := backgroundTurn{
		pipelineID: "p",
		ctxCfg:     contextCfg,
		generation: generation,
		input:      engine.Input{EventID: "screen-event", ImageJPEG: []byte("frame")},
		at:         time.Now(),
	}

	manager.recordCoalescedTurn(runtime, turn)

	snapshot := manager.contexts.Snapshot(contextCfg, "p", time.Now().Add(time.Second))
	if len(snapshot.Entries) != 0 || snapshot.Text != "" {
		t.Fatalf("screen coalescing invented conversation loss: %+v", snapshot.Entries)
	}
	if runtime.status.Dropped != 1 {
		t.Fatalf("screen coalescing was not counted: %+v", runtime.status)
	}
}
