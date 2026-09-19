package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type openAILiveSocketFixture struct {
	mu     sync.Mutex
	writes []string
	reads  chan string
	closed bool
}

func newOpenAILiveSocketFixture() *openAILiveSocketFixture {
	return &openAILiveSocketFixture{reads: make(chan string, 16)}
}

func (socket *openAILiveSocketFixture) WriteText(ctx context.Context, text string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	socket.mu.Lock()
	defer socket.mu.Unlock()
	if socket.closed {
		return io.ErrClosedPipe
	}
	socket.writes = append(socket.writes, text)
	return nil
}

func (socket *openAILiveSocketFixture) ReadText(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case text, ok := <-socket.reads:
		if !ok {
			return "", io.EOF
		}
		return text, nil
	}
}

func (socket *openAILiveSocketFixture) Close() error {
	socket.mu.Lock()
	socket.closed = true
	socket.mu.Unlock()
	return nil
}

func (socket *openAILiveSocketFixture) writtenEvents(t *testing.T) []openAILiveEnvelope {
	t.Helper()
	socket.mu.Lock()
	defer socket.mu.Unlock()
	result := make([]openAILiveEnvelope, 0, len(socket.writes))
	for _, raw := range socket.writes {
		var event openAILiveEnvelope
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		result = append(result, event)
	}
	return result
}

func TestOpenAILiveExperimentRequiresSessionStartedBeforeCommands(t *testing.T) {
	socket := newOpenAILiveSocketFixture()
	protocol := newOpenAILiveProtocol(socket, true)
	if err := protocol.Start(context.Background(), json.RawMessage(`{"model":"gpt-live-1"}`)); err != nil {
		t.Fatal(err)
	}
	if protocol.Started() {
		t.Fatal("session became active before session.started acknowledgement")
	}
	if err := protocol.AppendAudio(context.Background(), json.RawMessage(`"AA=="`)); err == nil {
		t.Fatal("audio was appended before session.started")
	}
	if err := protocol.RequestResponse(context.Background()); err == nil {
		t.Fatal("response.create was sent before session.started")
	}

	socket.reads <- `{"type":"session.started","event_id":"server-1","client_event_id":"aio-live-exp-1","session":{"id":"live_123"}}`
	observation, err := protocol.NextServerEvent(context.Background())
	if err != nil || observation.Type != "session.started" || !protocol.Started() {
		t.Fatalf("session acknowledgement=%+v err=%v", observation, err)
	}
}

func TestOpenAILiveExperimentAudioAppendDoesNotCreateResponse(t *testing.T) {
	socket := newOpenAILiveSocketFixture()
	protocol := newOpenAILiveProtocol(socket, true)
	if err := protocol.Start(context.Background(), json.RawMessage(`{"model":"gpt-live-1"}`)); err != nil {
		t.Fatal(err)
	}
	socket.reads <- `{"type":"session.started","event_id":"server-1"}`
	if _, err := protocol.NextServerEvent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := protocol.AppendAudio(context.Background(), json.RawMessage(`"AQID"`)); err != nil {
		t.Fatal(err)
	}

	events := socket.writtenEvents(t)
	if len(events) != 2 || events[0].Type != "session.start" || events[1].Type != "session.input_audio.append" {
		t.Fatalf("client events=%+v", events)
	}
	if string(events[1].Audio) != `"AQID"` {
		t.Fatalf("audio payload=%s", events[1].Audio)
	}
	for _, event := range events {
		if event.Type == "response.create" {
			t.Fatal("session.input_audio.append implicitly created a response")
		}
	}
}

func TestOpenAILiveExperimentExplicitResponseRequiresDelegationAndParsesNestedText(t *testing.T) {
	withoutDelegation := newOpenAILiveProtocol(newOpenAILiveSocketFixture(), false)
	if err := withoutDelegation.Start(context.Background(), json.RawMessage(`{"model":"gpt-live-1"}`)); err != nil {
		t.Fatal(err)
	}
	withoutDelegation.socket.(*openAILiveSocketFixture).reads <- `{"type":"session.started"}`
	if _, err := withoutDelegation.NextServerEvent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := withoutDelegation.RequestResponse(context.Background()); err == nil {
		t.Fatal("response.create ignored Responses delegation requirement")
	}

	socket := newOpenAILiveSocketFixture()
	protocol := newOpenAILiveProtocol(socket, true)
	if err := protocol.Start(context.Background(), json.RawMessage(`{"model":"gpt-live-1"}`)); err != nil {
		t.Fatal(err)
	}
	socket.reads <- `{"type":"session.started"}`
	if _, err := protocol.NextServerEvent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := protocol.RequestResponse(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := socket.writtenEvents(t)
	if len(events) != 2 || events[1].Type != "response.create" {
		t.Fatalf("explicit response event=%+v", events)
	}

	socket.reads <- `{"type":"response.event","event_id":"server-2","event":{"type":"response.output_text.delta","delta":"hello"}}`
	observation, err := protocol.NextServerEvent(context.Background())
	if err != nil || observation.Type != "response.event" || observation.NestedType != "response.output_text.delta" || observation.Delta != "hello" {
		t.Fatalf("nested response=%+v err=%v", observation, err)
	}
}

func TestOpenAILiveExperimentKeepsUnverifiedOperationsUnsupported(t *testing.T) {
	verified := openAILiveProtocolOperations()
	for _, operation := range []RealtimeOperation{RealtimeAppendAudio, RealtimeRequestResponse, RealtimeTextOutput} {
		if !verified[operation] {
			t.Fatalf("verified OpenAI Live operation missing: %s", operation)
		}
	}
	for _, operation := range []RealtimeOperation{
		RealtimeUpdateImage, RealtimeSemanticNoAction, RealtimeInterrupt,
		RealtimeCleanSession, RealtimeReconnect, RealtimeVPNFailClosed,
	} {
		if verified[operation] {
			t.Fatalf("unverified OpenAI Live operation claimed: %s", operation)
		}
	}
}

func TestOpenAILiveExperimentRejectsSessionStartedBeforeSessionStart(t *testing.T) {
	socket := newOpenAILiveSocketFixture()
	protocol := newOpenAILiveProtocol(socket, false)
	socket.reads <- `{"type":"session.started"}`
	if _, err := protocol.NextServerEvent(context.Background()); err == nil {
		t.Fatal("out-of-order session.started activated Live experiment")
	}
	if protocol.Started() {
		t.Fatal("out-of-order session.started changed protocol state")
	}
}

func TestOpenAILiveExperimentClosesOnlyAfterProtocolCloseAndBoundsEvents(t *testing.T) {
	socket := newOpenAILiveSocketFixture()
	protocol := newOpenAILiveProtocol(socket, false)
	if err := protocol.Start(context.Background(), json.RawMessage(`{"model":"gpt-live-1"}`)); err != nil {
		t.Fatal(err)
	}
	socket.reads <- `{"type":"session.started"}`
	if _, err := protocol.NextServerEvent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := protocol.CloseSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if events := socket.writtenEvents(t); events[len(events)-1].Type != "session.close" {
		t.Fatalf("close event=%+v", events)
	}
	socket.reads <- `{"type":"session.closed","reason":"client_request"}`
	if observation, err := protocol.NextServerEvent(context.Background()); err != nil || observation.Type != "session.closed" {
		t.Fatalf("session.closed=%+v err=%v", observation, err)
	}
	if err := protocol.AppendAudio(context.Background(), json.RawMessage(`"AA=="`)); err == nil {
		t.Fatal("closed Live session accepted audio")
	}

	boundedSocket := newOpenAILiveSocketFixture()
	bounded := newOpenAILiveProtocol(boundedSocket, false)
	if err := bounded.Start(context.Background(), json.RawMessage(`{"model":"gpt-live-1"}`)); err != nil {
		t.Fatal(err)
	}
	boundedSocket.reads <- `{"type":"session.started"}`
	if _, err := bounded.NextServerEvent(context.Background()); err != nil {
		t.Fatal(err)
	}
	oversized := make([]byte, openAILiveMaxEventBytes+1)
	for index := range oversized {
		oversized[index] = 'x'
	}
	oversized[0] = '"'
	oversized[len(oversized)-1] = '"'
	if err := bounded.AppendAudio(context.Background(), oversized); err == nil || err.Error() != "OpenAI Live client event exceeds bounds" {
		t.Fatalf("oversized Live event error=%v", err)
	}
	if events := boundedSocket.writtenEvents(t); len(events) != 1 {
		t.Fatalf("oversized Live event reached socket: %+v", events)
	}

	blocked := newOpenAILiveProtocol(newOpenAILiveSocketFixture(), false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := blocked.NextServerEvent(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked Live read ignored context: %v", err)
	}
}
