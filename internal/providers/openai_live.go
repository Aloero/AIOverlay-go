package providers

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"

	"aioverlay/internal/domain"
)

const openAILiveSampleRate = 24000

// OpenAILive is a persistent-session adapter. It intentionally does not
// implement ordinary Provider streaming: using a Live connection as a hidden
// stateless Chat API would collapse two different lifecycle contracts.
type OpenAILive struct {
	cfg       domain.ProviderConfig
	connector realtimeConnector
}

func NewOpenAILive(cfg domain.ProviderConfig, proxy ProxyResolver) *OpenAILive {
	timeout := time.Duration(cfg.RequestDefaults.ConnectTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &OpenAILive{cfg: cfg, connector: newRealtimeConnector(proxy, timeout)}
}

func (provider *OpenAILive) ID() string { return provider.cfg.ID }

func (provider *OpenAILive) Stream(context.Context, Request, string) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errors := make(chan error, 1)
	close(events)
	errors <- &RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "OpenAI Live requires the persistent realtime session contract"}
	close(errors)
	return events, errors
}

func (provider *OpenAILive) RealtimeCapabilities() RealtimeCapabilities {
	capabilities := UnsupportedRealtime("")
	capabilities.Available = true
	capabilities.Operations[RealtimeAppendAudio] = true
	capabilities.Operations[RealtimeVPNFailClosed] = true
	if provider.cfg.Realtime != nil && strings.TrimSpace(provider.cfg.Realtime.ResponsesModel) != "" {
		capabilities.Operations[RealtimeRequestResponse] = true
		capabilities.Operations[RealtimeTextOutput] = true
	}
	return capabilities
}

func (provider *OpenAILive) OpenRealtime(ctx context.Context, request RealtimeOpenRequest) (RealtimeSession, error) {
	if strings.TrimSpace(request.ModelID) == "" {
		return nil, &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "OpenAI Live requires a model ID"}
	}
	if strings.TrimSpace(request.Credential) == "" {
		return nil, &RequestError{Outcome: domain.OutcomeAuthFailed, Message: "OpenAI Live requires an API key"}
	}
	sessionJSON, delegated, err := provider.sessionConfig(request.ModelID)
	if err != nil {
		return nil, err
	}
	protocol, err := dialOpenAILiveTransport(ctx, provider.connector, openAILiveTransportConfig{
		Endpoint: provider.cfg.BaseURL, APIKey: request.Credential,
		Session: sessionJSON, ResponsesDelegated: delegated,
	})
	if err != nil {
		return nil, networkOutcome(err)
	}
	if err := waitForOpenAILiveStart(ctx, protocol); err != nil {
		_ = protocol.Close()
		return nil, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	session := &openAILiveSession{
		protocol: protocol, capabilities: provider.RealtimeCapabilities(),
		ctx: lifetime, cancel: cancel, readDone: make(chan struct{}), serverClosed: make(chan struct{}),
	}
	go session.readLoop()
	return session, nil
}

func (provider *OpenAILive) sessionConfig(model string) (json.RawMessage, bool, error) {
	config := map[string]any{
		"model": model,
		"audio": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": openAILiveSampleRate}},
	}
	if provider.cfg.Realtime != nil {
		if instructions := strings.TrimSpace(provider.cfg.Realtime.Instructions); instructions != "" {
			config["instructions"] = instructions
		}
		if responsesModel := strings.TrimSpace(provider.cfg.Realtime.ResponsesModel); responsesModel != "" {
			responses := map[string]any{"model": responsesModel}
			if provider.cfg.RequestDefaults.MaxOutputTokens > 0 {
				responses["max_output_tokens"] = provider.cfg.RequestDefaults.MaxOutputTokens
			}
			config["delegation"] = map[string]any{"type": "responses", "responses": responses}
		}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, false, &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "OpenAI Live session config is invalid"}
	}
	delegated := provider.cfg.Realtime != nil && strings.TrimSpace(provider.cfg.Realtime.ResponsesModel) != ""
	return raw, delegated, nil
}

func waitForOpenAILiveStart(ctx context.Context, protocol *openAILiveProtocol) error {
	for events := 0; events < 16; events++ {
		observation, err := protocol.NextServerEvent(ctx)
		if err != nil {
			return networkOutcome(err)
		}
		switch observation.Type {
		case "session.started":
			return nil
		case "error", "session.closed":
			return &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live session failed during startup"}
		}
	}
	return &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "OpenAI Live did not acknowledge session.start within the event bound"}
}

func networkOutcome(err error) error {
	if err == nil {
		return nil
	}
	outcome := OutcomeOf(err)
	if outcome == domain.OutcomeInvalidResponse {
		outcome = domain.OutcomeNetworkError
	}
	return &RequestError{Outcome: outcome, Message: err.Error(), Retryable: outcome == domain.OutcomeNetworkError || outcome == domain.OutcomeTimeout}
}

type openAILiveResponse struct {
	ctx    context.Context
	events chan Event
	errors chan error
	closed bool
}

type openAILiveSession struct {
	protocol     *openAILiveProtocol
	capabilities RealtimeCapabilities
	ctx          context.Context
	cancel       context.CancelFunc
	readDone     chan struct{}
	serverClosed chan struct{}
	closeOnce    sync.Once

	mu       sync.Mutex
	response *openAILiveResponse
}

func (session *openAILiveSession) Capabilities() RealtimeCapabilities { return session.capabilities }

func (session *openAILiveSession) AppendAudio(ctx context.Context, clip *domain.AudioClip) error {
	payload, err := liveAudioJSON(clip)
	if err != nil {
		return err
	}
	return session.protocol.AppendAudio(ctx, payload)
}

func (session *openAILiveSession) RequestResponse(ctx context.Context) (<-chan Event, <-chan error) {
	events := make(chan Event, 16)
	errors := make(chan error, 1)
	response := &openAILiveResponse{ctx: ctx, events: events, errors: errors}

	session.mu.Lock()
	if session.response != nil {
		session.mu.Unlock()
		errors <- &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "OpenAI Live already has an active delegated response"}
		close(events)
		close(errors)
		return events, errors
	}
	select {
	case <-session.ctx.Done():
		session.mu.Unlock()
		errors <- &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live session is closed"}
		close(events)
		close(errors)
		return events, errors
	default:
	}
	session.response = response
	session.mu.Unlock()

	if err := session.protocol.RequestResponse(ctx); err != nil {
		session.finishResponse(response, err, true)
		return events, errors
	}
	go session.watchResponseCancellation(response)
	return events, errors
}

func (session *openAILiveSession) watchResponseCancellation(response *openAILiveResponse) {
	select {
	case <-response.ctx.Done():
		session.finishResponse(response, response.ctx.Err(), false)
	case <-session.ctx.Done():
	}
}

func (session *openAILiveSession) readLoop() {
	defer close(session.readDone)
	defer session.finishActiveResponse(&RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live session ended"}, true)
	for {
		observation, err := session.protocol.NextServerEvent(session.ctx)
		if err != nil {
			if session.ctx.Err() == nil {
				session.finishActiveResponse(networkOutcome(err), true)
			}
			return
		}
		switch observation.Type {
		case "session.closed":
			session.closeServerClosed()
			return
		case "error":
			session.finishActiveResponse(&RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live server reported an error"}, true)
		case "response.event":
			session.consumeResponseEvent(observation)
		}
	}
}

func (session *openAILiveSession) consumeResponseEvent(observation openAILiveObservation) {
	session.mu.Lock()
	response := session.response
	if response == nil {
		session.mu.Unlock()
		return
	}
	if response.closed {
		if realtimeResponseTerminal(observation.NestedType) {
			session.response = nil
		}
		session.mu.Unlock()
		return
	}
	ctx := response.ctx
	events := response.events
	session.mu.Unlock()

	switch observation.NestedType {
	case "response.output_text.delta":
		if observation.Delta != "" {
			select {
			case events <- Event{Type: EventDelta, Text: observation.Delta}:
			case <-ctx.Done():
			}
		}
	case "response.completed":
		session.finishResponse(response, nil, true)
	case "response.failed", "response.incomplete":
		session.finishResponse(response, &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live delegated response did not complete"}, true)
	}
}

func realtimeResponseTerminal(eventType string) bool {
	return eventType == "response.completed" || eventType == "response.failed" || eventType == "response.incomplete"
}

func (session *openAILiveSession) finishActiveResponse(err error, releaseRemote bool) {
	session.mu.Lock()
	response := session.response
	session.mu.Unlock()
	if response != nil {
		session.finishResponse(response, err, releaseRemote)
	}
}

// releaseRemote is false on local cancellation: the remote generation has no
// verified cancel operation, so the session keeps it reserved until a terminal
// response.event arrives. This prevents overlapping remote generations.
func (session *openAILiveSession) finishResponse(response *openAILiveResponse, err error, releaseRemote bool) {
	session.mu.Lock()
	if session.response != response {
		session.mu.Unlock()
		return
	}
	if !response.closed {
		response.closed = true
		if err != nil {
			select {
			case response.errors <- err:
			default:
			}
		}
		close(response.events)
		close(response.errors)
	}
	if releaseRemote {
		session.response = nil
	}
	session.mu.Unlock()
}

func (session *openAILiveSession) Close() (closeErr error) {
	session.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		if err := session.protocol.CloseSession(ctx); err != nil {
			closeErr = err
		} else {
			select {
			case <-session.serverClosed:
			case <-ctx.Done():
			}
		}
		cancel()
		session.cancel()
		if err := session.protocol.Close(); closeErr == nil && err != nil {
			closeErr = err
		}
		<-session.readDone
	})
	return closeErr
}

func (session *openAILiveSession) closeServerClosed() {
	select {
	case <-session.serverClosed:
	default:
		close(session.serverClosed)
	}
}

func liveAudioJSON(clip *domain.AudioClip) (json.RawMessage, error) {
	if clip == nil || clip.Channels != 1 || clip.SampleRate < 8000 || clip.SampleRate > 192000 || len(clip.PCM16) == 0 {
		return nil, &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "OpenAI Live requires bounded mono PCM16 audio"}
	}
	samples := resamplePCM16(clip.PCM16, clip.SampleRate, openAILiveSampleRate)
	if len(samples) == 0 || len(samples) > openAILiveSampleRate*30 {
		return nil, &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "OpenAI Live audio chunk exceeds the 30 second bound"}
	}
	raw := make([]byte, len(samples)*2)
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(raw[index*2:], uint16(sample))
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	payload, err := json.Marshal(encoded)
	if err != nil {
		return nil, &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "OpenAI Live audio encoding failed"}
	}
	return payload, nil
}

func resamplePCM16(input []int16, sourceRate, targetRate int) []int16 {
	if len(input) == 0 || sourceRate <= 0 || targetRate <= 0 {
		return nil
	}
	if sourceRate == targetRate {
		return append([]int16(nil), input...)
	}
	outputLength := int(math.Round(float64(len(input)) * float64(targetRate) / float64(sourceRate)))
	if outputLength < 1 {
		outputLength = 1
	}
	output := make([]int16, outputLength)
	if len(input) == 1 {
		for index := range output {
			output[index] = input[0]
		}
		return output
	}
	ratio := float64(sourceRate) / float64(targetRate)
	for index := range output {
		position := float64(index) * ratio
		left := int(position)
		if left >= len(input)-1 {
			output[index] = input[len(input)-1]
			continue
		}
		fraction := position - float64(left)
		value := float64(input[left])*(1-fraction) + float64(input[left+1])*fraction
		output[index] = int16(math.Round(value))
	}
	return output
}
