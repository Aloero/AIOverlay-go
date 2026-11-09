package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/domain"
	paudio "aioverlay/internal/platform/audio"
	"aioverlay/internal/providers"
)

type realtimeSessionFixture struct {
	caps       providers.RealtimeCapabilities
	appended   chan *domain.AudioClip
	started    chan struct{}
	startOnce  sync.Once
	events     chan providers.Event
	errors     chan error
	closeCount atomic.Int32
}

func newRealtimeSessionFixture() *realtimeSessionFixture {
	caps := providers.UnsupportedRealtime("")
	caps.Available = true
	caps.Operations[providers.RealtimeAppendAudio] = true
	caps.Operations[providers.RealtimeRequestResponse] = true
	caps.Operations[providers.RealtimeTextOutput] = true
	return &realtimeSessionFixture{
		caps: caps, appended: make(chan *domain.AudioClip, 4), started: make(chan struct{}),
		events: make(chan providers.Event, 4), errors: make(chan error, 1),
	}
}
func (f *realtimeSessionFixture) Capabilities() providers.RealtimeCapabilities { return f.caps }
func (f *realtimeSessionFixture) Close() error {
	f.closeCount.Add(1)
	return nil
}
func (f *realtimeSessionFixture) AppendAudio(_ context.Context, clip *domain.AudioClip) error {
	copyClip := *clip
	copyClip.PCM16 = append([]int16(nil), clip.PCM16...)
	f.appended <- &copyClip
	return nil
}
func (f *realtimeSessionFixture) StartResponse(context.Context) (<-chan providers.Event, <-chan error, error) {
	f.startOnce.Do(func() { close(f.started) })
	return f.events, f.errors, nil
}
func (f *realtimeSessionFixture) RequestResponse(ctx context.Context) (<-chan providers.Event, <-chan error) {
	events, errors, err := f.StartResponse(ctx)
	if err != nil {
		fallbackEvents := make(chan providers.Event)
		fallbackErrors := make(chan error, 1)
		close(fallbackEvents)
		fallbackErrors <- err
		close(fallbackErrors)
		return fallbackEvents, fallbackErrors
	}
	return events, errors
}

func realtimeManagerConfig() domain.Config {
	cfg := managerConfig()
	cfg.Providers = []domain.ProviderConfig{{
		ID: "live", Label: "Live", Kind: domain.ProviderOpenAILive, Enabled: true,
		BaseURL:        "wss://api.openai.com/v1/live/sessions",
		CredentialPool: domain.CredentialPool{Policy: domain.CredentialSequential, Keys: []domain.Credential{{ID: "k", Secret: "TEST_ONLY", Enabled: true}}},
		Realtime:       &domain.RealtimeProviderConfig{ResponsesModel: "gpt-5.6-luna"},
	}}
	source := &cfg.Pipelines[0].Sources[0]
	source.Intent = domain.IntentContext
	source.ContextID = ""
	source.EntryNodeID = ""
	source.Audio.SegmentMS = 1000
	source.Audio.MinimumMS = 80
	source.Audio.SilenceMS = 200
	source.Realtime = &domain.RealtimeSourceConfig{ProviderID: "live", ModelID: "gpt-live-1", ResponsePolicy: "manual"}
	return cfg
}

func TestRealtimeAudioSourceKeepsOneSessionForSourceLifetime(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	fixture := newRealtimeSessionFixture()
	var opens atomic.Int32
	manager.SetRealtimeOpener(func(context.Context, string, string) (providers.RealtimeSession, error) {
		opens.Add(1)
		return fixture, nil
	})
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		voice := make([]int16, 16000)
		for i := range voice {
			voice[i] = 12000
		}
		emit(paudio.Block{Samples: voice, Rate: 16000, At: time.Now()})
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	select {
	case clip := <-fixture.appended:
		if clip.SampleRate != 16000 || clip.SegmentID == "" {
			t.Fatalf("appended clip=%+v", clip)
		}
	case <-time.After(time.Second):
		t.Fatal("audio segment was not appended to Live session")
	}
	if opens.Load() != 1 {
		t.Fatalf("session opens=%d want=1", opens.Load())
	}
	manager.pending.mu.Lock()
	pending := len(manager.pending.segments)
	manager.pending.mu.Unlock()
	if pending != 0 {
		t.Fatalf("remote-only Live audio fabricated local STT pending=%d", pending)
	}
	cancel()
	manager.Close()
	if fixture.closeCount.Load() != 1 {
		t.Fatalf("session closes=%d want=1", fixture.closeCount.Load())
	}
}

func TestManualRealtimeResponseUsesSharedGovernorAndReturnsText(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	manager.cfg = cfg
	manager.started = true
	runtime := &sourceRuntime{pipelineID: "p", cfg: cfg.Pipelines[0].Sources[0], epoch: 1}
	fixture := newRealtimeSessionFixture()
	runtime.realtime = fixture
	manager.sources[runtime.cfg.ID] = runtime

	go func() {
		<-fixture.started
		fixture.events <- providers.Event{Type: providers.EventDelta, Text: "hello"}
		close(fixture.events)
		close(fixture.errors)
	}()
	result, err := manager.RequestRealtimeResponse(context.Background(), runtime.cfg.ID)
	if err != nil || result.Text != "hello" || !result.Published {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if active := manager.governor.Snapshot()["active"]; active != 0 {
		t.Fatalf("governor active=%d after completed response", active)
	}
}

func TestCancelledRealtimeResponseKeepsGovernorLeaseUntilRemoteTerminal(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	manager.cfg = cfg
	manager.started = true
	manager.governor.Configure(1, 60)
	runtime := &sourceRuntime{pipelineID: "p", cfg: cfg.Pipelines[0].Sources[0], epoch: 1}
	fixture := newRealtimeSessionFixture()
	runtime.realtime = fixture
	manager.sources[runtime.cfg.ID] = runtime

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := manager.RequestRealtimeResponse(ctx, runtime.cfg.ID)
		result <- err
	}()
	<-fixture.started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	if active := manager.governor.Snapshot()["active"]; active != 1 {
		t.Fatalf("local cancel released remote concurrency early: active=%d", active)
	}
	blocked, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if lease, err := manager.governor.Acquire(blocked, true); err == nil {
		lease.Release()
		t.Fatal("second call acquired slot while remote response was still active")
	}
	close(fixture.events)
	close(fixture.errors)
	deadline := time.Now().Add(time.Second)
	for manager.governor.Snapshot()["active"] != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if active := manager.governor.Snapshot()["active"]; active != 0 {
		t.Fatalf("remote terminal did not release governor lease: active=%d", active)
	}
}

func TestClearContextReplacesBoundRealtimeSession(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	source := &cfg.Pipelines[0].Sources[0]
	source.ContextID = "c"
	opened := make(chan *realtimeSessionFixture, 4)
	manager.SetRealtimeOpener(func(context.Context, string, string) (providers.RealtimeSession, error) {
		fixture := newRealtimeSessionFixture()
		opened <- fixture
		return fixture, nil
	})
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx, cfg)
	var first *realtimeSessionFixture
	select {
	case first = <-opened:
	case <-time.After(time.Second):
		t.Fatal("initial realtime session did not open")
	}
	if _, err := manager.ClearContext("c"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for first.closeCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if first.closeCount.Load() != 1 {
		t.Fatal("cleared context did not close old remote session")
	}
	select {
	case second := <-opened:
		if second == first {
			t.Fatal("Clear reused the old remote session")
		}
	case <-time.After(time.Second):
		t.Fatal("Clear did not open a fresh remote session")
	}
	manager.Close()
}

func TestRealtimeSourcePolicyChangeRevokesOldSessionImmediately(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	manager.cfg = cfg
	manager.permissionPolicy = capturePermissions(cfg)
	manager.permissionPolicySet = true
	cancelled := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &sourceRuntime{pipelineID: "p", cfg: cfg.Pipelines[0].Sources[0], epoch: 1, cancel: func() { cancel(); close(cancelled) }}
	manager.sources[runtime.cfg.ID] = runtime
	manager.started = true
	manager.root = ctx

	next := cfg
	next.Pipelines = append([]domain.Pipeline(nil), cfg.Pipelines...)
	next.Pipelines[0].Sources = append([]domain.EventSource(nil), cfg.Pipelines[0].Sources...)
	realtime := *next.Pipelines[0].Sources[0].Realtime
	realtime.ModelID = "gpt-live-2"
	next.Pipelines[0].Sources[0].Realtime = &realtime
	manager.FencePermissions(next)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("realtime model change did not revoke old source runtime")
	}
}

func TestRealtimeProviderRouteChangeRevokesUnchangedSource(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	manager.cfg = cfg
	manager.permissionPolicy = capturePermissions(cfg)
	manager.permissionPolicySet = true
	cancelled := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &sourceRuntime{
		pipelineID: "p", cfg: cfg.Pipelines[0].Sources[0], epoch: 1,
		realtimeFingerprint: realtimeSourceFingerprint(cfg, cfg.Pipelines[0].Sources[0]),
		cancel:              func() { cancel(); close(cancelled) },
	}
	manager.sources[runtime.cfg.ID] = runtime
	manager.started = true
	manager.root = ctx

	next := cfg
	next.Providers = append([]domain.ProviderConfig(nil), cfg.Providers...)
	next.Providers[0].BaseURL = "wss://example.test/v1/live/sessions"
	manager.FencePermissions(next)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("provider endpoint change did not revoke unchanged realtime source")
	}
}

func TestRealtimeRouteRollbackRestartsRevokedSource(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	opened := make(chan *realtimeSessionFixture, 4)
	manager.SetRealtimeOpener(func(context.Context, string, string) (providers.RealtimeSession, error) {
		fixture := newRealtimeSessionFixture()
		opened <- fixture
		return fixture, nil
	})
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx, cfg)
	var first *realtimeSessionFixture
	select {
	case first = <-opened:
	case <-time.After(time.Second):
		t.Fatal("initial realtime session did not open")
	}

	next := cfg
	next.Providers = append([]domain.ProviderConfig(nil), cfg.Providers...)
	next.Providers[0].Realtime = &domain.RealtimeProviderConfig{ResponsesModel: "different-model"}
	manager.FencePermissions(next)
	// Mirrors App's failed-save rollback: restore the old permission policy and
	// re-apply the old config so the revoked runtime cannot remain as a zombie.
	manager.FencePermissions(cfg)
	manager.Apply(cfg)

	deadline := time.Now().Add(time.Second)
	for first.closeCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if first.closeCount.Load() != 1 {
		t.Fatal("rollback did not close the revoked realtime session")
	}
	select {
	case replacement := <-opened:
		if replacement == first {
			t.Fatal("rollback reused revoked realtime session")
		}
	case <-time.After(time.Second):
		t.Fatal("rollback did not restart realtime source on old route")
	}
	manager.Close()
}

type reconnectingRealtimeFixture struct {
	*realtimeSessionFixture
	appendCalls    atomic.Int32
	reconnectCount atomic.Int32
}

func newReconnectingRealtimeFixture() *reconnectingRealtimeFixture {
	base := newRealtimeSessionFixture()
	base.caps.Operations[providers.RealtimeReconnect] = true
	return &reconnectingRealtimeFixture{realtimeSessionFixture: base}
}

func (fixture *reconnectingRealtimeFixture) AppendAudio(_ context.Context, clip *domain.AudioClip) error {
	call := fixture.appendCalls.Add(1)
	if call == 1 {
		return &providers.RequestError{Outcome: domain.OutcomeNetworkError, Message: "synthetic disconnect", Retryable: true}
	}
	copyClip := *clip
	copyClip.PCM16 = append([]int16(nil), clip.PCM16...)
	fixture.appended <- &copyClip
	return nil
}

func (fixture *reconnectingRealtimeFixture) Reconnect(context.Context) error {
	fixture.reconnectCount.Add(1)
	return nil
}

func TestRealtimeAppendDisconnectForksWithoutReplayingAmbiguousSegment(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	cfg.Pipelines[0].Sources[0].ContextID = "c"
	fixture := newReconnectingRealtimeFixture()
	manager.SetRealtimeOpener(func(context.Context, string, string) (providers.RealtimeSession, error) {
		return fixture, nil
	})
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		voice := make([]int16, 16000*2)
		for index := range voice {
			voice[index] = 12000
		}
		emit(paudio.Block{Samples: voice, Rate: 16000, At: time.Now()})
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()
	select {
	case <-fixture.appended:
	case <-time.After(time.Second):
		t.Fatal("post-reconnect segment was not appended")
	}
	if fixture.reconnectCount.Load() != 1 {
		t.Fatalf("reconnect count=%d want=1", fixture.reconnectCount.Load())
	}
	if fixture.appendCalls.Load() != 2 {
		t.Fatalf("ambiguous segment was replayed: append calls=%d for two captured segments", fixture.appendCalls.Load())
	}
	snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(time.Second))
	gaps := 0
	for _, entry := range snapshot.Entries {
		if entry.Gap {
			gaps++
		}
	}
	if gaps != 1 {
		t.Fatalf("ambiguous append gap count=%d entries=%+v", gaps, snapshot.Entries)
	}
	runtime := manager.source(cfg.Pipelines[0].Sources[0].ID)
	runtime.mu.RLock()
	status := runtime.status
	runtime.mu.RUnlock()
	if status.Dropped != 1 || !strings.Contains(status.LastRecovery, "not replayed") {
		t.Fatalf("reconnect diagnostic=%+v", status)
	}
}

func TestManualScreenshotDoesNotConsumeLiveRemoteMemory(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	manager.cfg = cfg
	manager.capture = func(context.Context) ([]byte, error) { return []byte("isolated-screen"), nil }
	runtime := &sourceRuntime{pipelineID: "p", cfg: cfg.Pipelines[0].Sources[0], epoch: 1}
	fixture := newRealtimeSessionFixture()
	runtime.realtime = fixture
	manager.sources[runtime.cfg.ID] = runtime

	prepared, err := manager.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if string(prepared.Input.ImageJPEG) != "isolated-screen" || prepared.Input.SourceID != "answer" {
		t.Fatalf("manual screenshot input=%+v", prepared.Input)
	}
	if prepared.Input.ContextText != "" || len(prepared.Input.ContextViews) != 0 {
		t.Fatalf("isolated manual screenshot inherited local context: %+v", prepared.Input.ContextViews)
	}
	select {
	case <-fixture.started:
		t.Fatal("manual screenshot unexpectedly requested a Live delegated response")
	default:
	}
	if fixture.appended != nil && len(fixture.appended) != 0 {
		t.Fatal("manual screenshot unexpectedly appended media to Live session")
	}
}

func TestRealtimeProviderFailureDoesNotTriggerForkReconnect(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	fixture := newReconnectingRealtimeFixture()
	runtime := &sourceRuntime{pipelineID: "p", cfg: cfg.Pipelines[0].Sources[0], epoch: 1, realtime: fixture}
	manager.cfg = cfg
	manager.sources[runtime.cfg.ID] = runtime
	err := &providers.RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "model rejected response", Retryable: false}
	if manager.recoverRealtimeSession(context.Background(), runtime, err, "must not reconnect") {
		t.Fatal("non-retryable provider failure triggered fork reconnect")
	}
	if fixture.reconnectCount.Load() != 0 {
		t.Fatalf("reconnects=%d", fixture.reconnectCount.Load())
	}
}

func TestUnavailableProcessTargetDoesNotOpenRealtimeSessionOrCapture(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	source := &cfg.Pipelines[0].Sources[0]
	source.Audio.Mode = "process"
	source.Audio.DeviceID = ""
	source.Audio.DeviceRole = ""
	source.Audio.Process = &domain.AudioProcessConfig{ExecutablePath: `C:\Program Files\Zoom\bin\Zoom.exe`}
	manager.cfg = cfg
	var opens, captures atomic.Int32
	manager.SetRealtimeOpener(func(context.Context, string, string) (providers.RealtimeSession, error) {
		opens.Add(1)
		return newRealtimeSessionFixture(), nil
	})
	manager.SetAudioCapture(func(context.Context, domain.AudioSourceConfig, func(paudio.Block)) error {
		captures.Add(1)
		return nil
	})
	runtime := &sourceRuntime{pipelineID: "p", cfg: *source, epoch: 1}
	err := manager.runAudio(context.Background(), "p", runtime)
	if err == nil {
		t.Fatal("process-only capture unexpectedly passed unsupported/unavailable preflight")
	}
	if opens.Load() != 0 || captures.Load() != 0 {
		t.Fatalf("preflight leaked work: realtime opens=%d capture calls=%d", opens.Load(), captures.Load())
	}
}

func TestCancelledRealtimeResponseDropsLateServerTextAndKeepsItOutOfContext(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	source := &cfg.Pipelines[0].Sources[0]
	source.ContextID = "c"
	manager.cfg = cfg
	manager.started = true
	manager.governor.Configure(1, 60)
	runtime := &sourceRuntime{pipelineID: "p", cfg: *source, epoch: 1}
	fixture := newRealtimeSessionFixture()
	runtime.realtime = fixture
	manager.sources[source.ID] = runtime

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := manager.RequestRealtimeResponse(ctx, source.ID)
		result <- err
	}()
	<-fixture.started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}

	// The remote side may still deliver data because this adapter does not claim
	// a verified interrupt operation. These events must be drained only; they may
	// not reopen local publication or context after the manual command cancelled.
	fixture.events <- providers.Event{Type: providers.EventDelta, Text: "LATE_REMOTE_PRIVATE_TEXT"}
	close(fixture.events)
	close(fixture.errors)
	deadline := time.Now().Add(time.Second)
	for manager.governor.Snapshot()["active"] != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if active := manager.governor.Snapshot()["active"]; active != 0 {
		t.Fatalf("late remote terminal did not release governor lease: active=%d", active)
	}
	snapshot := manager.contexts.Snapshot(cfg.Pipelines[0].Contexts[0], "p", time.Now().Add(time.Second))
	for _, entry := range snapshot.Entries {
		if strings.Contains(entry.Text, "LATE_REMOTE_PRIVATE_TEXT") {
			t.Fatalf("late server text escaped cancelled response into context: %+v", snapshot.Entries)
		}
	}
}

func TestClearContextStartsFreshRealtimeSessionInsteadOfForkingOldOne(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	cfg.Pipelines[0].Sources[0].ContextID = "c"
	opened := make(chan *reconnectingRealtimeFixture, 4)
	manager.SetRealtimeOpener(func(context.Context, string, string) (providers.RealtimeSession, error) {
		fixture := newReconnectingRealtimeFixture()
		opened <- fixture
		return fixture, nil
	})
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx, cfg)
	var first *reconnectingRealtimeFixture
	select {
	case first = <-opened:
	case <-time.After(time.Second):
		t.Fatal("initial realtime session did not open")
	}
	if _, err := manager.ClearContext("c"); err != nil {
		t.Fatal(err)
	}
	select {
	case second := <-opened:
		if second == first {
			t.Fatal("Clear reused the old remote session")
		}
	case <-time.After(time.Second):
		t.Fatal("Clear did not open a new primary remote session")
	}
	if first.reconnectCount.Load() != 0 {
		t.Fatalf("Clear forked old remote history: reconnects=%d", first.reconnectCount.Load())
	}
	manager.Close()
}

func TestRealtimeAppendStatusDoesNotClaimServerProcessedAudio(t *testing.T) {
	manager := NewManager(nil)
	cfg := realtimeManagerConfig()
	manager.cfg = cfg
	manager.started = true
	runtime := &sourceRuntime{pipelineID: "p", cfg: cfg.Pipelines[0].Sources[0], epoch: 1}
	fixture := newRealtimeSessionFixture()
	runtime.realtime = fixture
	manager.sources[runtime.cfg.ID] = runtime

	captured := capturedAudioSegment{
		context: audioContext{pipelineID: "p"},
		segment: paudio.Segment{Clip: domain.AudioClip{PCM16: []int16{1, 2, 3, 4}, SampleRate: 16000, Channels: 1, SegmentID: "seg", Final: true}, Started: time.Now(), Ended: time.Now().Add(10 * time.Millisecond)},
		eventID: "seg",
	}
	manager.processRealtimeAudioSegment(context.Background(), "p", runtime, captured, captured.segment.Clip)
	select {
	case <-fixture.appended:
	case <-time.After(time.Second):
		t.Fatal("realtime append did not reach session fixture")
	}
	runtime.mu.RLock()
	status := runtime.status
	runtime.mu.RUnlock()
	if !strings.Contains(status.LastDiagnostic, "exact model processing acknowledgement is unavailable") {
		t.Fatalf("append status overclaimed processing: %+v", status)
	}
	for _, forbidden := range []string{"server processed", "processed successfully", "heard", "услыш"} {
		if strings.Contains(strings.ToLower(status.LastDiagnostic), strings.ToLower(forbidden)) {
			t.Fatalf("append status contains unsupported acknowledgement claim %q: %q", forbidden, status.LastDiagnostic)
		}
	}
}
