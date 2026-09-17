package providers

// This file models the current OpenAI Live primary-WebSocket protocol as an
// experiment. It is deliberately not registered as a Provider and does not
// change the public realtime capability matrix until a real provider/VPN run
// proves the concrete endpoint, authentication, billing and lifecycle behavior.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

const openAILiveMaxEventBytes = 4 << 20

type openAILiveTextSocket interface {
	WriteText(context.Context, string) error
	ReadText(context.Context) (string, error)
	Close() error
}

type openAILiveProtocolExperiment struct {
	socket             openAILiveTextSocket
	responsesDelegated bool
	sequence           atomic.Uint64
	mu                 sync.Mutex
	startSent          bool
	started            bool
	closed             bool
}

type openAILiveEnvelope struct {
	Type          string          `json:"type"`
	EventID       string          `json:"event_id,omitempty"`
	ClientEventID string          `json:"client_event_id,omitempty"`
	Session       json.RawMessage `json:"session,omitempty"`
	Audio         json.RawMessage `json:"audio,omitempty"`
	Event         json.RawMessage `json:"event,omitempty"`
	Delta         string          `json:"delta,omitempty"`
}

type openAILiveObservation struct {
	Type          string
	ClientEventID string
	NestedType    string
	Delta         string
}

func newOpenAILiveProtocolExperiment(socket openAILiveTextSocket, responsesDelegated bool) *openAILiveProtocolExperiment {
	return &openAILiveProtocolExperiment{socket: socket, responsesDelegated: responsesDelegated}
}

func openAILiveVerifiedProtocolOperations() map[RealtimeOperation]bool {
	return map[RealtimeOperation]bool{
		RealtimeAppendAudio:     true,
		RealtimeRequestResponse: true,
		RealtimeTextOutput:      true,
	}
}

func (protocol *openAILiveProtocolExperiment) Start(ctx context.Context, session json.RawMessage) error {
	if protocol == nil || protocol.socket == nil {
		return fmt.Errorf("OpenAI Live experiment socket is unavailable")
	}
	if !validJSONObject(session) {
		return fmt.Errorf("OpenAI Live session.start requires a JSON object")
	}
	protocol.mu.Lock()
	if protocol.closed {
		protocol.mu.Unlock()
		return fmt.Errorf("OpenAI Live experiment is closed")
	}
	if protocol.startSent || protocol.started {
		protocol.mu.Unlock()
		return fmt.Errorf("OpenAI Live session.start was already sent")
	}
	protocol.startSent = true
	protocol.mu.Unlock()

	event := openAILiveEnvelope{Type: "session.start", EventID: protocol.nextEventID(), Session: cloneRawJSON(session)}
	if err := protocol.writeEvent(ctx, event); err != nil {
		protocol.mu.Lock()
		protocol.startSent = false
		protocol.mu.Unlock()
		return err
	}
	return nil
}

func (protocol *openAILiveProtocolExperiment) AppendAudio(ctx context.Context, audio json.RawMessage) error {
	if err := protocol.requireStarted(); err != nil {
		return err
	}
	if len(audio) == 0 || !json.Valid(audio) {
		return fmt.Errorf("OpenAI Live input_audio.append requires valid JSON audio data")
	}
	return protocol.writeEvent(ctx, openAILiveEnvelope{
		Type: "input_audio.append", EventID: protocol.nextEventID(), Audio: cloneRawJSON(audio),
	})
}

func (protocol *openAILiveProtocolExperiment) RequestResponse(ctx context.Context) error {
	if err := protocol.requireStarted(); err != nil {
		return err
	}
	if !protocol.responsesDelegated {
		return fmt.Errorf("OpenAI Live response.create requires Responses delegation")
	}
	return protocol.writeEvent(ctx, openAILiveEnvelope{Type: "response.create", EventID: protocol.nextEventID()})
}

func (protocol *openAILiveProtocolExperiment) CloseSession(ctx context.Context) error {
	protocol.mu.Lock()
	if protocol.closed {
		protocol.mu.Unlock()
		return nil
	}
	started := protocol.started
	protocol.mu.Unlock()
	if !started {
		return fmt.Errorf("OpenAI Live session has not started")
	}
	return protocol.writeEvent(ctx, openAILiveEnvelope{Type: "session.close", EventID: protocol.nextEventID()})
}

func (protocol *openAILiveProtocolExperiment) NextServerEvent(ctx context.Context) (openAILiveObservation, error) {
	if protocol == nil || protocol.socket == nil {
		return openAILiveObservation{}, fmt.Errorf("OpenAI Live experiment socket is unavailable")
	}
	raw, err := protocol.socket.ReadText(ctx)
	if err != nil {
		return openAILiveObservation{}, err
	}
	if len(raw) == 0 || len(raw) > openAILiveMaxEventBytes {
		return openAILiveObservation{}, fmt.Errorf("OpenAI Live server event exceeds bounds")
	}
	var event openAILiveEnvelope
	if err := json.Unmarshal([]byte(raw), &event); err != nil || event.Type == "" {
		return openAILiveObservation{}, fmt.Errorf("invalid OpenAI Live server event")
	}
	observation := openAILiveObservation{Type: event.Type, ClientEventID: event.ClientEventID, Delta: event.Delta}
	switch event.Type {
	case "session.started":
		protocol.mu.Lock()
		if !protocol.startSent {
			protocol.mu.Unlock()
			return openAILiveObservation{}, fmt.Errorf("OpenAI Live session.started arrived before session.start")
		}
		protocol.started = true
		protocol.mu.Unlock()
	case "session.closed":
		protocol.mu.Lock()
		protocol.closed = true
		protocol.mu.Unlock()
	case "response.event":
		observation.NestedType, observation.Delta = parseNestedResponseEvent(event.Event)
	}
	return observation, nil
}

func (protocol *openAILiveProtocolExperiment) Started() bool {
	protocol.mu.Lock()
	defer protocol.mu.Unlock()
	return protocol.started
}

func (protocol *openAILiveProtocolExperiment) Close() error {
	if protocol == nil || protocol.socket == nil {
		return nil
	}
	protocol.mu.Lock()
	protocol.closed = true
	protocol.mu.Unlock()
	return protocol.socket.Close()
}

func (protocol *openAILiveProtocolExperiment) requireStarted() error {
	protocol.mu.Lock()
	defer protocol.mu.Unlock()
	if protocol.closed {
		return fmt.Errorf("OpenAI Live experiment is closed")
	}
	if !protocol.started {
		return fmt.Errorf("OpenAI Live session.started has not been received")
	}
	return nil
}

func (protocol *openAILiveProtocolExperiment) writeEvent(ctx context.Context, event openAILiveEnvelope) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if len(raw) > openAILiveMaxEventBytes {
		return fmt.Errorf("OpenAI Live client event exceeds bounds")
	}
	return protocol.socket.WriteText(ctx, string(raw))
}

func (protocol *openAILiveProtocolExperiment) nextEventID() string {
	return fmt.Sprintf("aio-live-exp-%d", protocol.sequence.Add(1))
}

func validJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 || !json.Valid(raw) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func cloneRawJSON(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func parseNestedResponseEvent(raw json.RawMessage) (eventType, delta string) {
	if len(raw) == 0 || !json.Valid(raw) {
		return "", ""
	}
	var event struct {
		Type  string `json:"type"`
		Delta string `json:"delta,omitempty"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return "", ""
	}
	return event.Type, event.Delta
}
