package session

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	paudio "aioverlay/internal/platform/audio"
	"context"
	"fmt"
	"sync"
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

	var snapshot ContextSnapshot
	waitUntil(t, func() bool {
		snapshot = m.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(time.Second))
		return len(snapshot.Entries) >= 2
	}, "expected transcript and gap after capture failure")
	transcript := snapshot.Entries[0]
	gap := snapshot.Entries[len(snapshot.Entries)-1]
	if transcript.Text != "truncated phrase" || !transcript.Final || !gap.Gap {
		t.Fatalf("expected final STT revision followed by audio gap, got %+v", snapshot.Entries)
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
	if runtime.status.LastError != "" || runtime.status.LastDiagnostic == "" {
		t.Fatalf("audio loss was classified as an error instead of a diagnostic: %+v", runtime.status)
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
	if runtime.status.LastError != "" || runtime.status.LastDiagnostic == "" {
		t.Fatalf("screen coalescing was classified as an error instead of a diagnostic: %+v", runtime.status)
	}
}

func TestPauseRemovesAudioPendingWhileTurnWaitsForSlot(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].SessionBudget.MaxParallelCalls = 1
	cfg.Pipelines[0].Sources[0].Audio.SegmentMS = 100
	cfg.Pipelines[0].Sources[0].Audio.MinimumMS = 80
	manager := NewManager(func(context.Context, string, engine.Input, engine.RunOptions) (engine.Result, error) {
		t.Fatal("turn executed after source was paused")
		return engine.Result{}, nil
	})

	allowEmit := make(chan struct{})
	emitted := make(chan struct{})
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		select {
		case <-allowEmit:
		case <-ctx.Done():
			return ctx.Err()
		}
		voice := make([]int16, 1600)
		for i := range voice {
			voice[i] = 12000
		}
		emit(paudio.Block{Samples: voice, Rate: 16000, At: time.Now()})
		close(emitted)
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() {
		select {
		case <-manager.turnSem:
		default:
		}
		cancel()
		manager.Close()
	}()
	manager.turnSem <- struct{}{}
	close(allowEmit)
	<-emitted
	waitUntil(t, func() bool {
		manager.pendingMu.Lock()
		defer manager.pendingMu.Unlock()
		return len(manager.pending) == 1
	}, "audio segment never became pending")

	manager.Pause(true)
	waitUntil(t, func() bool {
		manager.pendingMu.Lock()
		defer manager.pendingMu.Unlock()
		return len(manager.pending) == 0
	}, "paused source left a dead pending transcript")

	snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(time.Second))
	if len(snapshot.Entries) != 1 || !snapshot.Entries[0].Gap {
		t.Fatalf("paused pending audio loss was hidden: %+v", snapshot.Entries)
	}
}

func TestRepeatedSourceStartStopLeavesNoPendingTurns(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].Sources[0].AutoStart = false
	manager := NewManager(nil)
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()

	for i := 0; i < 100; i++ {
		if err := manager.SetSourceRunning("a", true); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool { return manager.source("a") != nil }, "source did not start")
		if err := manager.SetSourceRunning("a", false); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool { return manager.source("a") == nil }, "source did not stop")
	}

	manager.pendingMu.Lock()
	pending := len(manager.pending)
	manager.pendingMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending turns leaked across start/stop cycles: %d", pending)
	}
	if status := manager.Status()["sources"].([]SourceStatus); len(status) != 0 {
		t.Fatalf("source runtimes leaked after stop: %+v", status)
	}
}

func TestClearContextCancelsOldBackgroundTurnAndAllowsNewGeneration(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].Sources[0].Audio.SegmentMS = 100
	cfg.Pipelines[0].Sources[0].Audio.MinimumMS = 80

	firstStarted := make(chan struct{})
	firstFinished := make(chan struct{})
	secondFinished := make(chan struct{})
	var calls atomic.Int32
	manager := NewManager(func(ctx context.Context, _ string, input engine.Input, options engine.RunOptions) (engine.Result, error) {
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			<-ctx.Done()
			if options.OnObservation != nil {
				options.OnObservation(engine.Observation{Kind: "audio.text", Text: "late old transcript"})
			}
			close(firstFinished)
			return engine.Result{Published: true, Text: "stale answer", Trace: domain.ExecutionTrace{Outcome: domain.OutcomeSuccess}}, nil
		case 2:
			if options.OnObservation != nil {
				options.OnObservation(engine.Observation{Kind: "audio.text", Text: "new transcript"})
			}
			close(secondFinished)
			return engine.Result{Trace: domain.ExecutionTrace{Outcome: domain.OutcomeContextOnly}}, nil
		default:
			return engine.Result{}, fmt.Errorf("unexpected background call")
		}
	})

	emitSecond := make(chan struct{})
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		voice := make([]int16, 1600)
		for i := range voice {
			voice[i] = 12000
		}
		start := time.Now()
		emit(paudio.Block{Samples: voice, Rate: 16000, At: start})
		select {
		case <-emitSecond:
			emit(paudio.Block{Samples: voice, Rate: 16000, At: start.Add(time.Second)})
		case <-ctx.Done():
			return ctx.Err()
		}
		<-ctx.Done()
		return ctx.Err()
	})

	published := make(chan Publication, 1)
	manager.SetPublisher(func(publication Publication) { published <- publication })
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("old generation background turn did not start")
	}
	generation, err := manager.ClearContext("c")
	if err != nil || generation != 2 {
		t.Fatalf("clear generation=%d err=%v", generation, err)
	}
	select {
	case <-firstFinished:
	case <-time.After(time.Second):
		t.Fatal("clear did not cancel old generation turn")
	}

	close(emitSecond)
	select {
	case <-secondFinished:
	case <-time.After(time.Second):
		t.Fatal("new generation audio did not continue after clear")
	}

	snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(2*time.Second))
	if snapshot.Generation != generation || snapshot.Text != "[transcript/a] new transcript" || len(snapshot.Entries) != 1 {
		t.Fatalf("cleared conversation was revived or new transcript lost: %+v", snapshot)
	}
	select {
	case stale := <-published:
		t.Fatalf("old generation published after clear: %+v", stale)
	default:
	}
	if calls.Load() != 2 {
		t.Fatalf("background calls=%d", calls.Load())
	}
}

func TestAudioRuntimeStateSeparatesNoPacketsSilentAndData(t *testing.T) {
	runtime := &sourceRuntime{cfg: domain.EventSource{Kind: domain.SourceAudio}}

	updateAudioStatus(runtime, paudio.Block{Rate: 16000}, 0)
	if runtime.status.TransportState != "alive" || runtime.status.SignalState != "no_packets" {
		t.Fatalf("no-packet state=%+v", runtime.status)
	}

	updateAudioStatus(runtime, paudio.Block{Samples: make([]int16, 160), Rate: 16000, Silent: true}, 0)
	if runtime.status.TransportState != "alive" || runtime.status.SignalState != "silent" {
		t.Fatalf("silent packet state=%+v", runtime.status)
	}

	updateAudioStatus(runtime, paudio.Block{Samples: []int16{1, 2, 3}, Rate: 16000}, 0.1)
	if runtime.status.TransportState != "alive" || runtime.status.SignalState != "data" || runtime.status.Level != 0.1 {
		t.Fatalf("data packet state=%+v", runtime.status)
	}
}

func TestAudioRuntimeStateDistinguishesStoppedAndUnavailable(t *testing.T) {
	stopped := &sourceRuntime{cfg: domain.EventSource{Kind: domain.SourceAudio}, status: SourceStatus{Running: true, TransportState: "alive"}}
	finishSourceStatus(stopped, context.Canceled, context.Canceled)
	if stopped.status.Running || stopped.status.TransportState != "stopped" || stopped.status.LastError != "" {
		t.Fatalf("stopped state=%+v", stopped.status)
	}

	unavailable := &sourceRuntime{cfg: domain.EventSource{Kind: domain.SourceAudio}, status: SourceStatus{Running: true, TransportState: "alive"}}
	finishSourceStatus(unavailable, fmt.Errorf("device invalidated"), nil)
	if unavailable.status.Running || unavailable.status.TransportState != "unavailable" || unavailable.status.LastOutcome != string(domain.OutcomeAnyError) || unavailable.status.LastError != "device invalidated" {
		t.Fatalf("unavailable state=%+v", unavailable.status)
	}
}

func TestDeadAudioRuntimeDoesNotBlockLocalPreview(t *testing.T) {
	manager := NewManager(nil)
	done := make(chan struct{})
	close(done)
	manager.sources["dead"] = &sourceRuntime{
		cfg:  domain.EventSource{ID: "dead", Kind: domain.SourceAudio},
		done: done,
	}
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		emit(paudio.Block{Samples: make([]int16, 4000), Rate: 16000, At: time.Now()})
		return ctx.Err()
	})

	preview, err := manager.PreviewAudio(context.Background(), domain.AudioSourceConfig{Mode: "system"}, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if preview.DurationMS != 250 || len(preview.PCM16) != 4000 {
		t.Fatalf("preview=%+v samples=%d", preview, len(preview.PCM16))
	}
}

func TestBackgroundCaptureNeverFallsBackToManualCapture(t *testing.T) {
	manager := NewManager(nil)
	var manualCalls atomic.Int32
	manager.SetCapture(func(context.Context) ([]byte, error) {
		manualCalls.Add(1)
		return []byte("manual"), nil
	}, nil)

	if capture := manager.backgroundScreenCapture(); capture != nil {
		_, _ = capture(context.Background())
		t.Fatal("background capture silently reused manual Studio capture")
	}
	if manualCalls.Load() != 0 {
		t.Fatal("manual capture was invoked by background capture lookup")
	}
}

func TestPartialTranscriptDoesNotFinishPendingUntilFinalRevision(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	contextCfg := cfg.Pipelines[0].Contexts[0]
	generation := manager.contexts.Generation("p", contextCfg.ID)
	runtime := &sourceRuntime{cfg: cfg.Pipelines[0].Sources[0]}
	turn := backgroundTurn{
		pipelineID: "p",
		ctxCfg:     contextCfg,
		generation: generation,
		input: engine.Input{
			EventID: "segment-1",
			Audio:   &domain.AudioClip{SegmentID: "segment-1", StartedAt: time.Now().Add(-time.Second), EndedAt: time.Now()},
		},
		at: time.Now(),
	}
	manager.addPending(pendingSegment{pipelineID: "p", contextID: contextCfg.ID, eventID: "segment-1", generation: generation})
	observe := manager.transcriptObserver("p", runtime, turn)

	observe(engine.Observation{Kind: "audio.text", Text: "hel", SegmentID: "segment-1", Revision: 1, Final: false})
	manager.pendingMu.Lock()
	_, stillPending := manager.pending["segment-1"]
	manager.pendingMu.Unlock()
	if !stillPending {
		t.Fatal("partial transcript released manual cutoff wait")
	}
	partial := manager.contexts.Snapshot(contextCfg, "p", time.Now().Add(time.Second))
	if len(partial.Entries) != 1 || partial.Entries[0].Final || partial.Entries[0].Revision != 1 {
		t.Fatalf("partial transcript state=%+v", partial.Entries)
	}

	observe(engine.Observation{Kind: "audio.text", Text: "hello", SegmentID: "segment-1", Revision: 2, Final: true})
	manager.pendingMu.Lock()
	_, stillPending = manager.pending["segment-1"]
	manager.pendingMu.Unlock()
	if stillPending {
		t.Fatal("final transcript did not release manual cutoff wait")
	}
	final := manager.contexts.Snapshot(contextCfg, "p", time.Now().Add(time.Second))
	if len(final.Entries) != 1 || !final.Entries[0].Final || final.Entries[0].Revision != 2 || final.Entries[0].Text != "hello" {
		t.Fatalf("final transcript state=%+v", final.Entries)
	}
}

func TestDefaultAudioRestartPolicyRecoversLifecycleErrorsOnlyForDefaultEndpoint(t *testing.T) {
	defaultRuntime := &sourceRuntime{cfg: domain.EventSource{
		Kind:  domain.SourceAudio,
		Audio: &domain.AudioSourceConfig{Mode: "system"},
	}}
	fixedRuntime := &sourceRuntime{cfg: domain.EventSource{
		Kind:  domain.SourceAudio,
		Audio: &domain.AudioSourceConfig{Mode: "system", DeviceID: "fixed-endpoint"},
	}}

	for _, kind := range []paudio.CaptureErrorKind{
		paudio.CaptureErrorDeviceInvalidated,
		paudio.CaptureErrorResourcesInvalidated,
		paudio.CaptureErrorServiceNotRunning,
	} {
		err := &paudio.CaptureError{Operation: "capture", Kind: kind}
		if !shouldRestartDefaultAudio(defaultRuntime, err, nil) {
			t.Fatalf("%s did not enable default-endpoint recovery", kind)
		}
		if shouldRestartDefaultAudio(fixedRuntime, err, nil) {
			t.Fatalf("fixed endpoint would silently change selection after %s", kind)
		}
	}
	if shouldRestartDefaultAudio(defaultRuntime, &paudio.CaptureError{Kind: paudio.CaptureErrorWASAPI}, nil) {
		t.Fatal("generic WASAPI failure enabled automatic restart")
	}
	if shouldRestartDefaultAudio(defaultRuntime, &paudio.CaptureError{Kind: paudio.CaptureErrorDeviceInvalidated}, context.Canceled) {
		t.Fatal("cancelled source scheduled an automatic restart")
	}
}

func TestDefaultAudioEndpointRestartsWithNewEpochAfterInvalidation(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].Sources[0].Audio.DeviceID = ""
	manager := NewManager(nil)
	manager.audioRestartBaseDelay = 0
	manager.audioRestartMaxDelay = 0

	var attempts atomic.Int32
	restarted := make(chan struct{})
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		attempt := attempts.Add(1)
		if attempt == 1 {
			return &paudio.CaptureError{
				Operation: "get buffer",
				HRESULT:   0x88890004,
				Kind:      paudio.CaptureErrorDeviceInvalidated,
			}
		}
		if attempt == 2 {
			close(restarted)
		}
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()

	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("default endpoint did not restart after invalidation")
	}
	waitUntil(t, func() bool {
		runtime := manager.source("a")
		return runtime != nil && runtime.epoch >= 2
	}, "restarted source did not receive a new epoch")
	if attempts.Load() != 2 {
		t.Fatalf("capture attempts=%d", attempts.Load())
	}

	runtime := manager.source("a")
	runtime.mu.RLock()
	status := runtime.status
	runtime.mu.RUnlock()
	if runtime.epoch < 2 || !status.Running {
		t.Fatalf("restarted runtime epoch=%d status=%+v", runtime.epoch, status)
	}

	snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(time.Second))
	if len(snapshot.Entries) != 1 || !snapshot.Entries[0].Gap {
		t.Fatalf("device invalidation did not leave a visible context gap: %+v", snapshot.Entries)
	}
}

func TestAudioRestartBackoffIsBoundedAndResetsAfterData(t *testing.T) {
	manager := NewManager(nil)
	manager.audioRestartBaseDelay = 250 * time.Millisecond
	manager.audioRestartMaxDelay = 5 * time.Second

	want := []time.Duration{
		250 * time.Millisecond,
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		5 * time.Second,
		5 * time.Second,
	}
	for i, expected := range want {
		if got := manager.nextAudioRestartDelay("a"); got != expected {
			t.Fatalf("failure %d delay=%v want=%v", i+1, got, expected)
		}
	}
	manager.resetAudioRestartFailures("a")
	if got := manager.nextAudioRestartDelay("a"); got != 250*time.Millisecond {
		t.Fatalf("restart backoff did not reset after healthy data: %v", got)
	}
}

func TestAudioRestartBackoffZeroBaseStaysImmediateForTests(t *testing.T) {
	for failures := 1; failures <= 10; failures++ {
		if got := restartBackoff(0, 0, failures); got != 0 {
			t.Fatalf("failures=%d delay=%v", failures, got)
		}
	}
}

func TestRepeatedDefaultEndpointInvalidationAdvancesEpochAndPreservesGaps(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].Sources[0].Audio.DeviceID = ""
	manager := NewManager(nil)
	manager.audioRestartBaseDelay = 0
	manager.audioRestartMaxDelay = 0

	var attempts atomic.Int32
	stable := make(chan struct{})
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		attempt := attempts.Add(1)
		if attempt <= 3 {
			return &paudio.CaptureError{
				Operation: "get buffer",
				HRESULT:   0x88890004,
				Kind:      paudio.CaptureErrorDeviceInvalidated,
			}
		}
		if attempt == 4 {
			close(stable)
		}
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()

	select {
	case <-stable:
	case <-time.After(time.Second):
		t.Fatal("default endpoint did not recover after repeated invalidation")
	}
	waitUntil(t, func() bool {
		runtime := manager.source("a")
		return runtime != nil && runtime.epoch == 4 && !sourceFinished(runtime)
	}, "stable runtime did not reach epoch 4")
	if attempts.Load() != 4 {
		t.Fatalf("capture attempts=%d", attempts.Load())
	}

	snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(time.Second))
	gaps := 0
	for _, entry := range snapshot.Entries {
		if entry.Gap {
			gaps++
		}
	}
	if gaps != 3 {
		t.Fatalf("invalidation gaps=%d entries=%+v", gaps, snapshot.Entries)
	}
}

func TestHealthyAudioBlockResetsRestartBackoff(t *testing.T) {
	manager := NewManager(nil)
	manager.audioRestartBaseDelay = 250 * time.Millisecond
	manager.audioRestartMaxDelay = 5 * time.Second
	_ = manager.nextAudioRestartDelay("a")
	_ = manager.nextAudioRestartDelay("a")

	runtime := &sourceRuntime{cfg: domain.EventSource{
		ID: "a", Kind: domain.SourceAudio,
		Audio: &domain.AudioSourceConfig{Mode: "system", SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01},
	}}
	queue := newAudioSegmentQueue(1, maxQueuedAudioPCMBytes)
	capture := func(_ context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		emit(paudio.Block{Rate: 16000, At: time.Now()})
		return fmt.Errorf("synthetic stop after healthy open")
	}
	_ = manager.produceAudioSegments(context.Background(), "", runtime, capture, queue)

	if got := manager.nextAudioRestartDelay("a"); got != 250*time.Millisecond {
		t.Fatalf("healthy stream did not reset backoff: %v", got)
	}
}

func TestFixedAudioEndpointDoesNotAutoRestartAfterInvalidation(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].Sources[0].Audio.DeviceID = "fixed-endpoint"
	manager := NewManager(nil)
	manager.audioRestartBaseDelay = 0
	manager.audioRestartMaxDelay = 0

	var attempts atomic.Int32
	manager.SetAudioCapture(func(context.Context, domain.AudioSourceConfig, func(paudio.Block)) error {
		attempts.Add(1)
		return &paudio.CaptureError{
			Operation: "get buffer",
			HRESULT:   0x88890004,
			Kind:      paudio.CaptureErrorDeviceInvalidated,
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()
	waitUntil(t, func() bool {
		runtime := manager.source("a")
		return runtime != nil && sourceFinished(runtime)
	}, "fixed endpoint runtime did not finish")

	select {
	case <-time.After(40 * time.Millisecond):
		if attempts.Load() != 1 {
			t.Fatalf("fixed endpoint silently retried or switched: attempts=%d", attempts.Load())
		}
	case <-ctx.Done():
		t.Fatal("test context ended unexpectedly")
	}
}

func TestPreparedManualRequestKeepsImmutablePinnedSnapshot(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	pipeline := &cfg.Pipelines[0]
	pipeline.Commands[0].ContextID = "c"
	manager.cfg = cfg

	contextCfg := pipeline.Contexts[0]
	generation := manager.contexts.Generation(pipeline.ID, contextCfg.ID)
	now := time.Now()
	if !manager.contexts.UpsertTranscript(contextCfg, pipeline.ID, generation, ContextEntry{
		ID: "before", Text: "before capture", At: now.Add(-time.Second), Revision: 1, Final: true,
	}) {
		t.Fatal("could not seed transcript context")
	}
	if !manager.contexts.SetScreen(contextCfg, pipeline.ID, generation, []byte("old-context-screen"), now.Add(-time.Second)) {
		t.Fatal("could not seed screen context")
	}

	captureBuffer := []byte("manual-screen")
	manager.capture = func(context.Context) ([]byte, error) { return captureBuffer, nil }
	prepared, err := manager.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if string(prepared.Input.ImageJPEG) != "manual-screen" || prepared.Input.ContextText != "[transcript] before capture" {
		t.Fatalf("unexpected prepared input: image=%q context=%q", prepared.Input.ImageJPEG, prepared.Input.ContextText)
	}

	captureBuffer[0] = 'X'
	if !manager.contexts.SetScreen(contextCfg, pipeline.ID, generation, []byte("late-context-screen"), time.Now().Add(time.Second)) {
		t.Fatal("late screen update was rejected unexpectedly")
	}
	if !manager.contexts.UpsertTranscript(contextCfg, pipeline.ID, generation, ContextEntry{
		ID: "after", Text: "after capture", At: time.Now().Add(time.Second), Revision: 1, Final: true,
	}) {
		t.Fatal("late transcript update was rejected unexpectedly")
	}
	if _, err := manager.ClearContext("c"); err != nil {
		t.Fatal(err)
	}

	if string(prepared.Input.ImageJPEG) != "manual-screen" {
		t.Fatalf("late state mutated pinned manual screenshot: %q", prepared.Input.ImageJPEG)
	}
	if prepared.Input.ContextText != "[transcript] before capture" {
		t.Fatalf("late state mutated immutable context snapshot: %q", prepared.Input.ContextText)
	}
}

func TestImportedConfigClearsStartNowOverrideEvenWhenSourceIDMatches(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].Sources[0].AutoStart = false
	manager := NewManager(nil)
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()
	if manager.source("a") != nil {
		t.Fatal("non-autostart source started before explicit override")
	}
	if err := manager.SetSourceRunning("a", true); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.source("a") != nil }, "explicit Start now did not start source")

	imported := cfg
	imported.Pipelines = append([]domain.Pipeline(nil), cfg.Pipelines...)
	imported.Pipelines[0].Sources = append([]domain.EventSource(nil), cfg.Pipelines[0].Sources...)
	imported.Pipelines[0].Sources[0].AutoStart = false
	manager.ApplyImported(imported)

	waitUntil(t, func() bool { return manager.source("a") == nil }, "import preserved running source override")
	manager.mu.RLock()
	_, overrideExists := manager.sourceOverrides["a"]
	manager.mu.RUnlock()
	if overrideExists {
		t.Fatal("import left Start-now override behind")
	}
}

func TestNormalApplyPreservesStartNowOverride(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].Sources[0].AutoStart = false
	manager := NewManager(nil)
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()
	if err := manager.SetSourceRunning("a", true); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.source("a") != nil }, "source did not start")

	edited := cfg
	edited.Overlay.FontSize++
	manager.Apply(edited)
	if manager.source("a") == nil {
		t.Fatal("ordinary config save unexpectedly cleared Start-now override")
	}
}

func TestPipelineSwitchDoesNotReuseSameIDSourceRuntime(t *testing.T) {
	cfg := managerConfig()
	first := cfg.Pipelines[0]
	second := first
	second.ID = "p2"
	second.Name = "second"
	cfg.Pipelines = []domain.Pipeline{first, second}
	cfg.ActivePipelineID = first.ID

	manager := NewManager(nil)
	started := make(chan string, 2)
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		manager.mu.RLock()
		pipelineID := ""
		if runtime := manager.sources["a"]; runtime != nil {
			pipelineID = runtime.pipelineID
		}
		manager.mu.RUnlock()
		started <- pipelineID
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()
	select {
	case pipelineID := <-started:
		if pipelineID != "p" {
			t.Fatalf("first runtime pipeline=%q", pipelineID)
		}
	case <-time.After(time.Second):
		t.Fatal("first pipeline source did not start")
	}
	firstRuntime := manager.source("a")
	if firstRuntime == nil {
		t.Fatal("first runtime missing")
	}

	cfg.ActivePipelineID = "p2"
	manager.Apply(cfg)
	select {
	case pipelineID := <-started:
		if pipelineID != "p2" {
			t.Fatalf("second runtime pipeline=%q", pipelineID)
		}
	case <-time.After(time.Second):
		t.Fatal("second pipeline source did not start")
	}
	secondRuntime := manager.source("a")
	if secondRuntime == nil || secondRuntime == firstRuntime || secondRuntime.pipelineID != "p2" || secondRuntime.epoch == firstRuntime.epoch {
		t.Fatalf("source runtime was reused across pipelines: first=%p/%d second=%p/%d pipeline=%q", firstRuntime, firstRuntime.epoch, secondRuntime, secondRuntime.epoch, secondRuntime.pipelineID)
	}
}

func TestPipelineSwitchClearsStartNowOverride(t *testing.T) {
	cfg := managerConfig()
	first := cfg.Pipelines[0]
	first.Sources[0].AutoStart = false
	second := first
	second.ID = "p2"
	second.Name = "second"
	second.Sources = append([]domain.EventSource(nil), first.Sources...)
	second.Sources[0].AutoStart = false
	cfg.Pipelines = []domain.Pipeline{first, second}
	cfg.ActivePipelineID = first.ID

	manager := NewManager(nil)
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()
	if err := manager.SetSourceRunning("a", true); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.source("a") != nil }, "first pipeline source did not start")

	cfg.ActivePipelineID = "p2"
	manager.Apply(cfg)
	waitUntil(t, func() bool { return manager.source("a") == nil }, "Start-now override leaked into second pipeline")
	manager.mu.RLock()
	_, exists := manager.sourceOverrides["a"]
	manager.mu.RUnlock()
	if exists {
		t.Fatal("pipeline switch retained source runtime override")
	}
}

func TestSessionRestartDropsPauseAndStartNowRuntimeState(t *testing.T) {
	cfg := managerConfig()
	cfg.Pipelines[0].Sources[0].AutoStart = false
	manager := NewManager(nil)
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		<-ctx.Done()
		return ctx.Err()
	})

	firstCtx, firstCancel := context.WithCancel(context.Background())
	manager.Start(firstCtx, cfg)
	if err := manager.SetSourceRunning("a", true); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return manager.source("a") != nil }, "Start-now source did not start")
	manager.Pause(true)
	manager.audioRestartMu.Lock()
	manager.audioRestartFailures["a"] = 4
	manager.audioRestartMu.Unlock()
	firstCancel()
	manager.Close()

	if manager.Paused() {
		t.Fatal("pause state survived session Close")
	}
	manager.mu.RLock()
	overrides := len(manager.sourceOverrides)
	manager.mu.RUnlock()
	manager.audioRestartMu.Lock()
	retries := len(manager.audioRestartFailures)
	manager.audioRestartMu.Unlock()
	if overrides != 0 || retries != 0 {
		t.Fatalf("runtime state survived Close: overrides=%d retries=%d", overrides, retries)
	}

	secondCtx, secondCancel := context.WithCancel(context.Background())
	manager.Start(secondCtx, cfg)
	defer func() { secondCancel(); manager.Close() }()
	if manager.source("a") != nil {
		t.Fatal("Start-now override survived into a new session")
	}
}

func TestScreenCooldownUpdatesContextWithoutStartingAITurn(t *testing.T) {
	cfg := managerConfig()
	contextCfg := cfg.Pipelines[0].Contexts[0]
	source := domain.EventSource{
		ID: "screen", Name: "screen", Kind: domain.SourceScreenTimer, Enabled: true,
		EntryNodeID: "decision", Intent: domain.IntentDecide, ContextID: contextCfg.ID,
	}
	cfg.Pipelines[0].Sources = []domain.EventSource{source}
	manager := NewManager(nil)
	manager.cfg = cfg
	manager.started = true
	runtime := &sourceRuntime{
		pipelineID: "p", cfg: source, epoch: 1, turns: make(chan backgroundTurn, 1),
		status: SourceStatus{ID: source.ID, Running: true},
	}
	runtime.cooldownUntil = time.Now().Add(time.Minute)
	manager.sources[source.ID] = runtime

	at := time.Now()
	manager.processScreenFrame("p", runtime, cfg, []byte("cooldown-frame"), at)
	if len(runtime.turns) != 0 {
		t.Fatal("screen cooldown still started an AI turn")
	}
	snapshot := manager.contexts.Snapshot(contextCfg, "p", at.Add(time.Millisecond))
	if string(snapshot.ImageJPEG) != "cooldown-frame" {
		t.Fatalf("screen context was not updated during cooldown: %q", snapshot.ImageJPEG)
	}
	runtime.mu.RLock()
	suppression := runtime.status.LastSuppression
	runtime.mu.RUnlock()
	if suppression != "cooldown_after_auto_answer" {
		t.Fatalf("screen cooldown reason=%q", suppression)
	}

	runtime.mu.Lock()
	runtime.cooldownUntil = time.Now().Add(-time.Second)
	runtime.mu.Unlock()
	manager.processScreenFrame("p", runtime, cfg, []byte("after-cooldown"), at.Add(time.Second))
	if len(runtime.turns) != 1 {
		t.Fatal("screen AI turn did not resume after cooldown")
	}
	runtime.mu.RLock()
	suppression = runtime.status.LastSuppression
	runtime.mu.RUnlock()
	if suppression != "" {
		t.Fatalf("expired screen cooldown reason remained visible: %q", suppression)
	}
}

func TestAudioCooldownUsesContextOnlyOrSkipsWhenNoContextExists(t *testing.T) {
	cfg := managerConfig()
	manager := NewManager(nil)
	manager.cfg = cfg
	manager.started = true
	source := cfg.Pipelines[0].Sources[0]
	source.Intent = domain.IntentDecide
	runtime := &sourceRuntime{
		pipelineID: "p", cfg: source, epoch: 1, turns: make(chan backgroundTurn, 1),
		status: SourceStatus{ID: source.ID, Running: true}, cooldownUntil: time.Now().Add(time.Minute),
	}
	manager.sources[source.ID] = runtime
	segment := paudio.Segment{
		Started: time.Now(), Ended: time.Now().Add(100 * time.Millisecond),
		Clip: domain.AudioClip{PCM16: make([]int16, 1600), SampleRate: 16000, Channels: 1, DurationMS: 100, Final: true},
	}
	manager.processAudioSegment(context.Background(), "p", runtime, segment)
	select {
	case turn := <-runtime.turns:
		if turn.input.Intent != string(domain.IntentContext) {
			t.Fatalf("audio cooldown intent=%q", turn.input.Intent)
		}
		manager.finishPending(turn.input.EventID)
	default:
		t.Fatal("audio cooldown with context stopped transcript/context processing")
	}

	withoutContext := source
	withoutContext.ID = "no-context"
	withoutContext.ContextID = ""
	noContextRuntime := &sourceRuntime{
		pipelineID: "p", cfg: withoutContext, epoch: 2, turns: make(chan backgroundTurn, 1),
		status: SourceStatus{ID: withoutContext.ID, Running: true}, cooldownUntil: time.Now().Add(time.Minute),
	}
	manager.sources[withoutContext.ID] = noContextRuntime
	manager.processAudioSegment(context.Background(), "p", noContextRuntime, segment)
	if len(noContextRuntime.turns) != 0 {
		t.Fatal("audio cooldown without context spent an AI turn")
	}
	noContextRuntime.mu.RLock()
	suppression := noContextRuntime.status.LastSuppression
	noContextRuntime.mu.RUnlock()
	if suppression != "cooldown_after_auto_answer" {
		t.Fatalf("audio cooldown reason=%q", suppression)
	}
}

func TestManualAnswerUsesPinnedContextGeneration(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Commands[0].ContextID = "c"
	cfg.Pipelines[0].Contexts[0].IncludeAnswers = true
	manager.cfg = cfg
	manager.capture = func(context.Context) ([]byte, error) { return []byte("screen"), nil }

	prepared, err := manager.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	result := engine.Result{
		Published: true,
		Text:      "manual answer",
		Trace: domain.ExecutionTrace{
			ID: "manual-trace", FinishedAt: time.Now(), Outcome: domain.OutcomeSuccess,
		},
	}
	if !manager.RecordCommandResult(prepared, result) {
		t.Fatal("accepted manual answer was not stored")
	}
	snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(time.Second))
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].Kind != "answer" || snapshot.Entries[0].Text != "manual answer" {
		t.Fatalf("manual answer context=%+v", snapshot.Entries)
	}

	prepared, err = manager.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ClearContext("c"); err != nil {
		t.Fatal(err)
	}
	if manager.RecordCommandResult(prepared, result) {
		t.Fatal("late manual answer revived a cleared context generation")
	}
	snapshot = manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(time.Second))
	if len(snapshot.Entries) != 0 {
		t.Fatalf("cleared context was revived: %+v", snapshot.Entries)
	}
}

func TestManualAnswerIsNotStoredWhenContextDisallowsAnswers(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Commands[0].ContextID = "c"
	cfg.Pipelines[0].Contexts[0].IncludeAnswers = false
	manager.cfg = cfg
	manager.capture = func(context.Context) ([]byte, error) { return []byte("screen"), nil }
	prepared, err := manager.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if manager.RecordCommandResult(prepared, engine.Result{Published: true, Text: "answer"}) {
		t.Fatal("manual answer ignored include_answers=false")
	}
}

func TestManualCutoffDoesNotMoveWhileScreenshotCaptureRuns(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Commands[0].ContextID = "c"
	cfg.Pipelines[0].Commands[0].WaitForTranscriptMS = 500
	manager.cfg = cfg
	generation := manager.contexts.Generation("p", "c")

	captureStarted := make(chan struct{})
	captureRelease := make(chan struct{})
	capture := func(context.Context) ([]byte, error) {
		close(captureStarted)
		<-captureRelease
		return []byte("manual-frame"), nil
	}
	result := make(chan PreparedCommand, 1)
	errs := make(chan error, 1)
	go func() {
		prepared, err := manager.PrepareCommandWithCapture(context.Background(), "analyze", capture)
		if err != nil {
			errs <- err
			return
		}
		result <- prepared
	}()
	<-captureStarted

	postTriggerStart := time.Now()
	manager.addPending(pendingSegment{
		pipelineID: "p", contextID: "c", eventID: "after-trigger",
		generation: generation, start: postTriggerStart, end: postTriggerStart.Add(time.Second),
	})
	close(captureRelease)
	defer manager.finishPending("after-trigger")

	select {
	case err := <-errs:
		t.Fatal(err)
	case prepared := <-result:
		if prepared.Input.ContextIncomplete {
			t.Fatal("speech that started during screenshot capture moved the manual cutoff")
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("manual command waited for speech that started after the trigger")
	}
}

func TestScreenContextUsesCaptureCompletionTimestamp(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	pipeline := &cfg.Pipelines[0]
	pipeline.Nodes = append(pipeline.Nodes, domain.Node{ID: "context-only-timestamp", Type: domain.NodeContext})
	pipeline.Commands = append(pipeline.Commands, domain.CommandConfig{
		ID: "context-timestamp", Action: "screen_context", EntryNodeID: "context-only-timestamp",
		Intent: domain.IntentContext, ContextID: "c", CaptureScreen: true,
	})
	manager.cfg = cfg

	captureStarted := make(chan struct{})
	captureRelease := make(chan struct{})
	capture := func(context.Context) ([]byte, error) {
		close(captureStarted)
		<-captureRelease
		return []byte("context-frame"), nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := manager.PrepareCommandWithCapture(context.Background(), "screen_context", capture)
		done <- err
	}()
	<-captureStarted
	releasedAt := time.Now()
	close(captureRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	snapshot := manager.contexts.Snapshot(pipeline.Contexts[0], pipeline.ID, time.Now().Add(time.Second))
	if string(snapshot.ImageJPEG) != "context-frame" {
		t.Fatalf("stored context frame=%q", snapshot.ImageJPEG)
	}
	if snapshot.ImageAt.Before(releasedAt) {
		t.Fatalf("screen context timestamp predates capture completion: image_at=%v released_at=%v", snapshot.ImageAt, releasedAt)
	}
}

func TestAudioRestartDrainLetsCurrentTurnFinishBeforeCancellation(t *testing.T) {
	turnDone := make(chan struct{})
	var cancelled atomic.Int32
	runtime := &sourceRuntime{
		turnDone: turnDone,
		cancel:   func() { cancelled.Add(1) },
	}
	go func() {
		time.Sleep(5 * time.Millisecond)
		close(turnDone)
	}()

	if !drainAudioRuntimeForRestart(runtime, 50*time.Millisecond, 50*time.Millisecond) {
		t.Fatal("gracefully finishing turn blocked audio restart")
	}
	if cancelled.Load() != 0 {
		t.Fatal("graceful tail was cancelled before its drain window ended")
	}
}

func TestAudioRestartDrainCancelsStuckTurnBeforeReplacement(t *testing.T) {
	turnDone := make(chan struct{})
	cancelled := make(chan struct{})
	var once sync.Once
	runtime := &sourceRuntime{
		turnDone: turnDone,
		cancel: func() {
			once.Do(func() { close(cancelled) })
		},
	}
	go func() {
		<-cancelled
		close(turnDone)
	}()

	if !drainAudioRuntimeForRestart(runtime, 0, 50*time.Millisecond) {
		t.Fatal("cancelled turn did not release before audio restart")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("stuck prior turn was not cancelled before replacement")
	}
}

func TestAudioRestartDrainRefusesOverlapWithNonCooperativeTurn(t *testing.T) {
	runtime := &sourceRuntime{
		turnDone: make(chan struct{}),
		cancel:   func() {},
	}
	if drainAudioRuntimeForRestart(runtime, 0, 5*time.Millisecond) {
		t.Fatal("audio restart allowed a second runtime while the old turn was still alive")
	}
}
