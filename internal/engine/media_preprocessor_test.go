package engine

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

type mediaJoinProvider struct {
	id      string
	started chan<- string
	release <-chan struct{}
	seen    chan<- providers.Request
}

func (provider *mediaJoinProvider) ID() string { return provider.id }
func (provider *mediaJoinProvider) Stream(ctx context.Context, request providers.Request, _ string) (<-chan providers.Event, <-chan error) {
	events := make(chan providers.Event, 2)
	errors := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errors)
		if provider.seen != nil {
			provider.seen <- request
		}
		if provider.started != nil {
			provider.started <- request.ModelID
		}
		if provider.release != nil && (request.ModelID == "vision-model" || request.ModelID == "stt-model") {
			select {
			case <-provider.release:
			case <-ctx.Done():
				errors <- ctx.Err()
				return
			}
		}
		text := map[string]string{"vision-model": "VISION_RESULT", "stt-model": "AUDIO_RESULT", "solver-model": "FINAL"}[request.ModelID]
		events <- providers.Event{Type: providers.EventDelta, Text: text, ModelID: request.ModelID}
		events <- providers.Event{Type: providers.EventDone, ModelID: request.ModelID}
	}()
	return events, errors
}

type countingLease struct {
	active *atomic.Int32
}

func (lease *countingLease) Commit()  {}
func (lease *countingLease) Release() { lease.active.Add(-1) }

func TestAudioAndVisionPreprocessorsRunInParallelOnOneSnapshot(t *testing.T) {
	base := domain.ExecutionBudget{MaxWallTimeMS: 5000, MaxModelCalls: 6, MaxParallelCalls: 3, MaxProviders: 3, MaxKeysPerProvider: 1, ConnectTimeoutMS: 1000, FirstTokenTimeoutMS: 1000, ResponseTimeoutMS: 2000, StreamIdleTimeoutMS: 1000}
	credential := domain.CredentialPool{Policy: domain.CredentialSequential, MaxKeysPerRequest: 1, Keys: []domain.Credential{{ID: "k", Secret: "fixture", Enabled: true}}}
	cfg := domain.Config{ActivePipelineID: "p", Providers: []domain.ProviderConfig{
		{ID: "vision", Kind: domain.ProviderOpenAICompatible, Enabled: true, BaseURL: "https://example.invalid", CredentialPool: credential},
		{ID: "stt", Kind: domain.ProviderTranscription, Enabled: true, BaseURL: "https://example.invalid", CredentialPool: credential},
		{ID: "solver", Kind: domain.ProviderOpenAICompatible, Enabled: true, BaseURL: "https://example.invalid", CredentialPool: credential},
	}}
	vision := &domain.ModelPoolConfig{Task: "generate", Policy: domain.PoolPriority, Models: []domain.ModelSlot{{ID: "v", ProviderID: "vision", ModelID: "vision-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}}, QualityFloor: 0, MaxParallel: 1, MaxCalls: 1, Prompt: domain.PromptConfig{User: "vision"}}
	audio := &domain.ModelPoolConfig{Task: "transcribe", ResultName: "audio.text", Policy: domain.PoolPriority, Models: []domain.ModelSlot{{ID: "a", ProviderID: "stt", ModelID: "stt-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}}, QualityFloor: 0, MaxParallel: 1, MaxCalls: 1, Prompt: domain.PromptConfig{User: "stt"}}
	solver := &domain.ModelPoolConfig{Task: "generate", Policy: domain.PoolPriority, Models: []domain.ModelSlot{{ID: "s", ProviderID: "solver", ModelID: "solver-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}}, QualityFloor: 0, MaxParallel: 1, MaxCalls: 1, Prompt: domain.PromptConfig{User: "A={{audio.text}} V={{vision.text}}"}, VisionPreprocessor: vision, AudioPreprocessor: audio}
	pipeline := domain.Pipeline{ID: "p", Name: "p", Enabled: true, EntryNodeID: "join", OutputNodeID: "out", Budget: base, Nodes: []domain.Node{{ID: "join", Type: domain.NodeModelPool, Name: "Join", ModelPool: solver}, {ID: "out", Type: domain.NodeOutput, Name: "Out"}}, Edges: []domain.Edge{{ID: "done", From: "join", To: "out", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}}}
	cfg.Pipelines = []domain.Pipeline{pipeline}

	registry, err := providers.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	started := make(chan string, 2)
	release := make(chan struct{})
	seen := make(chan providers.Request, 4)
	registry.Set("vision", &mediaJoinProvider{id: "vision", started: started, release: release, seen: seen})
	registry.Set("stt", &mediaJoinProvider{id: "stt", started: started, release: release, seen: seen})
	registry.Set("solver", &mediaJoinProvider{id: "solver", seen: seen})
	engine := New(cfg, registry, nil, nil)
	var active, maxActive atomic.Int32
	beforeCall := func(context.Context) (CallLease, error) {
		now := active.Add(1)
		for {
			old := maxActive.Load()
			if now <= old || maxActive.CompareAndSwap(old, now) {
				break
			}
		}
		return &countingLease{active: &active}, nil
	}
	resultCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := engine.Execute(context.Background(), "p", Input{ImageJPEG: []byte("same-image"), ImagePinned: true, Audio: &domain.AudioClip{PCM16: []int16{1, 2, 3}, SampleRate: 16000, Channels: 1, SegmentID: "segment"}}, RunOptions{BeforeCall: beforeCall})
		resultCh <- result
		errCh <- err
	}()

	first := <-started
	select {
	case second := <-started:
		if first == second {
			t.Fatalf("same preprocessor started twice: %q", first)
		}
	case <-time.After(time.Second):
		t.Fatal("audio and vision preprocessors did not overlap")
	}
	close(release)
	result := <-resultCh
	if err := <-errCh; err != nil || result.Text != "FINAL" {
		t.Fatalf("result=%q err=%v", result.Text, err)
	}
	if maxActive.Load() < 2 || active.Load() != 0 {
		t.Fatalf("shared admission active=%d max=%d", active.Load(), maxActive.Load())
	}
	var solverRequest providers.Request
	for i := 0; i < 3; i++ {
		request := <-seen
		if request.ModelID == "solver-model" {
			solverRequest = request
		}
	}
	if !strings.Contains(solverRequest.UserPrompt, "A=AUDIO_RESULT V=VISION_RESULT") {
		t.Fatalf("solver prompt=%q", solverRequest.UserPrompt)
	}
	if solverRequest.Audio != nil {
		t.Fatal("text-only solver received raw audio after explicit STT preprocessing")
	}
}

func TestSimulationMediaPreprocessorsJoinAtSlowestBranch(t *testing.T) {
	base := domain.ExecutionBudget{MaxWallTimeMS: 5000, MaxModelCalls: 6, MaxParallelCalls: 3, MaxProviders: 3, MaxKeysPerProvider: 1, ConnectTimeoutMS: 1000, FirstTokenTimeoutMS: 1000, ResponseTimeoutMS: 3000, StreamIdleTimeoutMS: 1000}
	credential := domain.CredentialPool{Policy: domain.CredentialSequential, MaxKeysPerRequest: 1, Keys: []domain.Credential{{ID: "k", Secret: "fixture", Enabled: true}}}
	cfg := domain.Config{ActivePipelineID: "p", Providers: []domain.ProviderConfig{
		{ID: "vision", Kind: domain.ProviderOpenAICompatible, Enabled: true, BaseURL: "https://example.invalid", CredentialPool: credential},
		{ID: "stt", Kind: domain.ProviderTranscription, Enabled: true, BaseURL: "https://example.invalid", CredentialPool: credential},
		{ID: "solver", Kind: domain.ProviderOpenAICompatible, Enabled: true, BaseURL: "https://example.invalid", CredentialPool: credential},
	}}
	vision := &domain.ModelPoolConfig{Task: "generate", Policy: domain.PoolPriority, Models: []domain.ModelSlot{{ID: "v", ProviderID: "vision", ModelID: "vision-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}}, QualityFloor: 0, MaxParallel: 1, MaxCalls: 1}
	audio := &domain.ModelPoolConfig{Task: "transcribe", ResultName: "audio.text", Policy: domain.PoolPriority, Models: []domain.ModelSlot{{ID: "a", ProviderID: "stt", ModelID: "stt-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}}, QualityFloor: 0, MaxParallel: 1, MaxCalls: 1}
	solver := &domain.ModelPoolConfig{Task: "generate", Policy: domain.PoolPriority, Models: []domain.ModelSlot{{ID: "s", ProviderID: "solver", ModelID: "solver-model", Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}}, QualityFloor: 0, MaxParallel: 1, MaxCalls: 1, VisionPreprocessor: vision, AudioPreprocessor: audio}
	pipeline := domain.Pipeline{ID: "p", Name: "p", Enabled: true, EntryNodeID: "join", OutputNodeID: "out", Budget: base, Nodes: []domain.Node{{ID: "join", Type: domain.NodeModelPool, Name: "Join", ModelPool: solver}, {ID: "out", Type: domain.NodeOutput, Name: "Out"}}}
	cfg.Pipelines = []domain.Pipeline{pipeline}

	trace := domain.ExecutionTrace{ID: "simulation", PipelineID: "p", Budget: base}
	runner := newRunner(New(cfg, nil, nil, nil), pipeline, Input{}, RunOptions{}, &trace)
	sim := &simulator{
		ctx: context.Background(), r: runner,
		req: SimulationRequest{Config: cfg, Outcomes: []SimulationOutcome{
			{NodeID: "join/vision", ModelID: "vision-model", Outcome: domain.OutcomeSuccess, DurationMS: 1000, FirstTokenMS: 100, Text: "VISION_RESULT"},
			{NodeID: "join/audio", ModelID: "stt-model", Outcome: domain.OutcomeSuccess, DurationMS: 1500, FirstTokenMS: 100, Text: "AUDIO_RESULT"},
			{NodeID: "join", ModelID: "solver-model", Outcome: domain.OutcomeSuccess, DurationMS: 500, FirstTokenMS: 100, Text: "FINAL"},
		}},
		until: map[string]int64{}, modelAttempts: map[string]int{}, report: SimulationReport{Simulated: true},
	}
	value, outcome, err := sim.pool(pipeline.Nodes[0], nodeValue{
		image: []byte("same-image"), imagePinned: true,
		audio: &domain.AudioClip{PCM16: []int16{1}, SampleRate: 16000, Channels: 1},
	})
	if err != nil || outcome != domain.OutcomeSuccess || value.text != "FINAL" {
		t.Fatalf("value=%+v outcome=%s err=%v", value, outcome, err)
	}
	if sim.now != 2000 {
		t.Fatalf("virtual join elapsed=%dms want=2000ms", sim.now)
	}
	if runner.calls.Load() != 3 {
		t.Fatalf("calls=%d want=3", runner.calls.Load())
	}
	starts := map[string]int64{}
	for _, event := range trace.Events {
		if event.Kind == "attempt_start" && (event.NodeID == "join/vision" || event.NodeID == "join/audio") {
			starts[event.NodeID] = event.AtMS
		}
	}
	if starts["join/vision"] != 0 || starts["join/audio"] != 0 {
		t.Fatalf("preprocessor starts=%v want both at 0ms", starts)
	}
}
