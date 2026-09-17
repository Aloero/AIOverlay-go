package providers

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
)

type realtimeCapabilityFixture struct{}

type realtimeSessionFixture struct {
	capabilities RealtimeCapabilities
}

func (s *realtimeSessionFixture) Capabilities() RealtimeCapabilities { return s.capabilities }
func (*realtimeSessionFixture) Close() error                         { return nil }
func (*realtimeSessionFixture) AppendAudio(context.Context, *domain.AudioClip) error {
	return nil
}
func (*realtimeSessionFixture) RequestResponse(context.Context) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errors := make(chan error)
	close(events)
	close(errors)
	return events, errors
}

func (*realtimeCapabilityFixture) ID() string { return "fixture" }
func (*realtimeCapabilityFixture) Stream(context.Context, Request, string) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errors := make(chan error)
	close(events)
	close(errors)
	return events, errors
}
func (*realtimeCapabilityFixture) RealtimeCapabilities() RealtimeCapabilities {
	capabilities := UnsupportedRealtime("")
	capabilities.Available = true
	capabilities.Operations[RealtimeAppendAudio] = true
	capabilities.Operations[RealtimeRequestResponse] = true
	capabilities.Operations[RealtimeTextOutput] = true
	return capabilities
}
func (p *realtimeCapabilityFixture) OpenRealtime(context.Context, RealtimeOpenRequest) (RealtimeSession, error) {
	return &realtimeSessionFixture{capabilities: p.RealtimeCapabilities()}, nil
}

type realtimeExperimentFixture struct {
	session *realtimeExperimentSession
}

type realtimeExperimentSession struct {
	mu sync.Mutex

	capabilities RealtimeCapabilities
	closed       bool
	audio        int
	images       int
	responses    int
	interrupts   int
	cleans       int
	reconnects   int
}

type realtimeExperimentState struct {
	closed     bool
	audio      int
	images     int
	responses  int
	interrupts int
	cleans     int
	reconnects int
}

func newRealtimeExperimentFixture() *realtimeExperimentFixture {
	capabilities := UnsupportedRealtime("")
	capabilities.Available = true
	for _, operation := range []RealtimeOperation{
		RealtimeAppendAudio,
		RealtimeUpdateImage,
		RealtimeRequestResponse,
		RealtimeSemanticNoAction,
		RealtimeTextOutput,
		RealtimeInterrupt,
		RealtimeCleanSession,
		RealtimeReconnect,
	} {
		capabilities.Operations[operation] = true
	}
	return &realtimeExperimentFixture{session: &realtimeExperimentSession{capabilities: capabilities}}
}

func (*realtimeExperimentFixture) ID() string { return "experiment" }
func (*realtimeExperimentFixture) Stream(context.Context, Request, string) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errors := make(chan error)
	close(events)
	close(errors)
	return events, errors
}
func (fixture *realtimeExperimentFixture) RealtimeCapabilities() RealtimeCapabilities {
	return fixture.session.capabilities
}
func (fixture *realtimeExperimentFixture) OpenRealtime(context.Context, RealtimeOpenRequest) (RealtimeSession, error) {
	return fixture.session, nil
}

func (session *realtimeExperimentSession) Capabilities() RealtimeCapabilities {
	return session.capabilities
}
func (session *realtimeExperimentSession) Close() error {
	session.mu.Lock()
	session.closed = true
	session.mu.Unlock()
	return nil
}
func (session *realtimeExperimentSession) AppendAudio(_ context.Context, clip *domain.AudioClip) error {
	if clip == nil || len(clip.PCM16) == 0 {
		return fmt.Errorf("audio is required")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return fmt.Errorf("session closed")
	}
	session.audio++
	return nil
}
func (session *realtimeExperimentSession) UpdateImage(_ context.Context, image []byte) error {
	if len(image) == 0 {
		return fmt.Errorf("image is required")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return fmt.Errorf("session closed")
	}
	session.images++
	return nil
}
func (session *realtimeExperimentSession) RequestResponse(context.Context) (<-chan Event, <-chan error) {
	events := make(chan Event, 2)
	errors := make(chan error, 1)
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		errors <- fmt.Errorf("session closed")
		close(events)
		close(errors)
		return events, errors
	}
	session.responses++
	responseNumber := session.responses
	session.mu.Unlock()

	if responseNumber == 1 {
		events <- Event{Type: EventDone, Outcome: domain.OutcomeNoAction}
	} else {
		events <- Event{Type: EventDelta, Text: "synthetic answer"}
		events <- Event{Type: EventDone, Outcome: domain.OutcomeRespond}
	}
	close(events)
	close(errors)
	return events, errors
}
func (session *realtimeExperimentSession) Interrupt(context.Context) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return fmt.Errorf("session closed")
	}
	session.interrupts++
	return nil
}
func (session *realtimeExperimentSession) Clean(context.Context) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return fmt.Errorf("session closed")
	}
	session.audio = 0
	session.images = 0
	session.cleans++
	return nil
}
func (session *realtimeExperimentSession) Reconnect(context.Context) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return fmt.Errorf("session closed")
	}
	session.reconnects++
	return nil
}
func (session *realtimeExperimentSession) state() realtimeExperimentState {
	session.mu.Lock()
	defer session.mu.Unlock()
	return realtimeExperimentState{
		closed: session.closed, audio: session.audio, images: session.images,
		responses: session.responses, interrupts: session.interrupts,
		cleans: session.cleans, reconnects: session.reconnects,
	}
}

type invalidRealtimeFixture struct {
	capabilities RealtimeCapabilities
}

func (*invalidRealtimeFixture) ID() string { return "invalid" }
func (*invalidRealtimeFixture) Stream(context.Context, Request, string) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errors := make(chan error)
	close(events)
	close(errors)
	return events, errors
}
func (fixture *invalidRealtimeFixture) RealtimeCapabilities() RealtimeCapabilities {
	return fixture.capabilities
}
func (fixture *invalidRealtimeFixture) OpenRealtime(context.Context, RealtimeOpenRequest) (RealtimeSession, error) {
	return &realtimeSessionFixture{capabilities: fixture.capabilities}, nil
}

func TestCurrentHTTPAdaptersDoNotClaimPersistentRealtime(t *testing.T) {
	cfg := config.Default()
	registry, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)

	for _, capabilities := range registry.Capabilities(cfg) {
		if capabilities.Realtime.Available {
			t.Fatalf("%s unexpectedly claims realtime readiness", capabilities.ProviderID)
		}
		for _, operation := range realtimeOperations {
			if capabilities.Realtime.Supports(operation) {
				t.Fatalf("%s unexpectedly supports %s", capabilities.ProviderID, operation)
			}
		}
	}
}

func TestRealtimeCapabilityMatrixRequiresEachOperationExplicitly(t *testing.T) {
	providerConfig := domain.ProviderConfig{ID: "fixture", Label: "Fixture", Kind: domain.ProviderOpenAICompatible, Enabled: true, AudioInput: true}
	capabilities := adapterCapabilities(providerConfig, &realtimeCapabilityFixture{})

	if !capabilities.Realtime.Supports(RealtimeAppendAudio) || !capabilities.Realtime.Supports(RealtimeRequestResponse) {
		t.Fatal("declared realtime operations are unavailable")
	}
	missing := capabilities.Realtime.Missing(RealtimeUpdateImage, RealtimeInterrupt, RealtimeVPNFailClosed)
	if len(missing) != 3 {
		t.Fatalf("missing operations=%v", missing)
	}
	if !capabilities.Media.Audio {
		t.Fatal("media capability was lost while describing realtime")
	}
}

func TestRegistryRejectsRealtimeBeforeAnyNetworkFallback(t *testing.T) {
	cfg := config.Default()
	registry, err := NewRegistry(cfg, testProxyResolver{err: fmt.Errorf("network should not be consulted")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)

	_, err = registry.OpenRealtime(context.Background(), RealtimeOpenRequest{ProviderID: cfg.Providers[1].ID, ModelID: "model", Credential: "secret"})
	if err == nil || OutcomeOf(err) != domain.OutcomeUnsupportedModality {
		t.Fatalf("unsupported realtime open=%v", err)
	}
}

func TestRegistryOpensOnlyExplicitRealtimeFactory(t *testing.T) {
	cfg := config.Default()
	registry, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	registry.Set("fixture", &realtimeCapabilityFixture{})

	session, err := registry.OpenRealtime(context.Background(), RealtimeOpenRequest{ProviderID: "fixture", ModelID: "model", Credential: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !session.Capabilities().Supports(RealtimeAppendAudio) {
		t.Fatal("opened session lost declared capability")
	}
}

func TestRealtimeOpenRejectsAdvertisedOperationWithoutSessionInterface(t *testing.T) {
	capabilities := UnsupportedRealtime("")
	capabilities.Available = true
	capabilities.Operations[RealtimeUpdateImage] = true
	fixture := &invalidRealtimeFixture{capabilities: capabilities}

	_, err := openRealtime(context.Background(), fixture, RealtimeOpenRequest{ProviderID: "invalid"})
	if err == nil {
		t.Fatal("session without advertised image interface was accepted")
	}
}

func TestRealtimeSessionContractExperiment(t *testing.T) {
	fixture := newRealtimeExperimentFixture()
	session, err := openRealtime(context.Background(), fixture, RealtimeOpenRequest{ProviderID: "experiment", ModelID: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	capabilities := session.Capabilities()
	if capabilities.Supports(RealtimeVPNFailClosed) {
		t.Fatal("synthetic experiment must not claim VPN verification")
	}
	for _, operation := range []RealtimeOperation{
		RealtimeAppendAudio, RealtimeUpdateImage, RealtimeRequestResponse,
		RealtimeSemanticNoAction, RealtimeTextOutput, RealtimeInterrupt,
		RealtimeCleanSession, RealtimeReconnect,
	} {
		if !capabilities.Supports(operation) {
			t.Fatalf("experiment does not support %s", operation)
		}
	}

	audio := session.(RealtimeAudioAppender)
	images := session.(RealtimeImageUpdater)
	responses := session.(RealtimeResponseRequester)
	interrupt := session.(RealtimeInterrupter)
	cleaner := session.(RealtimeSessionCleaner)
	reconnector := session.(RealtimeReconnector)

	clip := &domain.AudioClip{PCM16: []int16{1, 2, 3}, SampleRate: 16000, Channels: 1}
	if err := audio.AppendAudio(context.Background(), clip); err != nil {
		t.Fatal(err)
	}
	if err := images.UpdateImage(context.Background(), []byte("frame")); err != nil {
		t.Fatal(err)
	}
	if state := fixture.session.state(); state.responses != 0 || state.audio != 1 || state.images != 1 {
		t.Fatalf("media update triggered generation: %+v", state)
	}

	firstEvents, firstErrors := responses.RequestResponse(context.Background())
	first := collectRealtimeEvents(t, firstEvents, firstErrors)
	if len(first) != 1 || first[0].Type != EventDone || first[0].Outcome != domain.OutcomeNoAction || first[0].Text != "" {
		t.Fatalf("no_action response=%+v", first)
	}

	secondEvents, secondErrors := responses.RequestResponse(context.Background())
	second := collectRealtimeEvents(t, secondEvents, secondErrors)
	if len(second) != 2 || second[0].Text != "synthetic answer" || second[1].Outcome != domain.OutcomeRespond {
		t.Fatalf("text response=%+v", second)
	}

	if err := interrupt.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := reconnector.Reconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	beforeClean := fixture.session.state()
	if beforeClean.interrupts != 1 || beforeClean.reconnects != 1 || beforeClean.audio != 1 || beforeClean.images != 1 {
		t.Fatalf("operation state=%+v", beforeClean)
	}
	if err := cleaner.Clean(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterClean := fixture.session.state()
	if afterClean.cleans != 1 || afterClean.audio != 0 || afterClean.images != 0 {
		t.Fatalf("clean session did not isolate prior media: %+v", afterClean)
	}
}

func collectRealtimeEvents(t *testing.T, events <-chan Event, errors <-chan error) []Event {
	t.Helper()
	var result []Event
	for events != nil || errors != nil {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			result = append(result, event)
		case err, ok := <-errors:
			if !ok {
				errors = nil
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return result
}
