package providers

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/url"
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

// Capabilities is this adapter's own declaration of what it can carry. It is reported to the
// scheduler and to Studio, so no layer has to infer modality support from the provider's name.
func (provider *OpenAILive) Capabilities() MediaCapabilities { return Media(provider.cfg) }

func init() {
	Register(Factory{
		Kind: domain.ProviderOpenAILive,
		New: func(p domain.ProviderConfig, deps Deps) (Provider, MediaCapabilities, error) {
			adapter := NewOpenAILive(p, deps.Proxy)
			return adapter, adapter.Capabilities(), nil
		},
	})
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
	if provider.cfg.Realtime != nil {
		if strings.TrimSpace(provider.cfg.Realtime.ResponsesModel) != "" {
			capabilities.Operations[RealtimeRequestResponse] = true
			capabilities.Operations[RealtimeTextOutput] = true
		}
		if provider.cfg.Realtime.Store {
			capabilities.Operations[RealtimeReconnect] = true
		}
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
	sessionID, err := waitForOpenAILiveStart(ctx, protocol)
	if err != nil {
		_ = protocol.Close()
		return nil, err
	}
	store := provider.cfg.Realtime != nil && provider.cfg.Realtime.Store
	if store && strings.TrimSpace(sessionID) == "" {
		_ = protocol.Close()
		return nil, &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "OpenAI Live stored session did not provide a session ID"}
	}
	lifetime, cancel := context.WithCancel(context.Background())
	readDone := make(chan struct{})
	serverClosed := make(chan struct{})
	session := &openAILiveSession{
		protocol: protocol, capabilities: provider.RealtimeCapabilities(),
		ctx: lifetime, cancel: cancel, readDone: readDone, serverClosed: serverClosed,
		connector: provider.connector, primaryEndpoint: provider.cfg.BaseURL, apiKey: request.Credential,
		responsesDelegated: delegated, store: store, sessionID: sessionID,
	}
	go session.readLoop(protocol, readDone, serverClosed)
	return session, nil
}

func (provider *OpenAILive) sessionConfig(model string) (json.RawMessage, bool, error) {
	config := map[string]any{
		"model": model,
		"audio": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": openAILiveSampleRate}},
	}
	if provider.cfg.Realtime != nil {
		if provider.cfg.Realtime.Store {
			config["store"] = true
		}
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

func waitForOpenAILiveStart(ctx context.Context, protocol *openAILiveProtocol) (string, error) {
	for events := 0; events < 16; events++ {
		observation, err := protocol.NextServerEvent(ctx)
		if err != nil {
			return "", networkOutcome(err)
		}
		switch observation.Type {
		case "session.started":
			return observation.SessionID, nil
		case "error", "session.closed":
			return "", &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live session failed during startup"}
		}
	}
	return "", &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "OpenAI Live did not acknowledge session.start within the event bound"}
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
	capabilities RealtimeCapabilities
	ctx          context.Context
	cancel       context.CancelFunc
	closeOnce    sync.Once
	reconnectMu  sync.Mutex

	connectionMu       sync.RWMutex
	protocol           *openAILiveProtocol
	readDone           chan struct{}
	serverClosed       chan struct{}
	connector          realtimeConnector
	primaryEndpoint    string
	apiKey             string
	responsesDelegated bool
	store              bool
	sessionID          string

	mu       sync.Mutex
	response *openAILiveResponse
}

func (session *openAILiveSession) Capabilities() RealtimeCapabilities { return session.capabilities }

func (session *openAILiveSession) AppendAudio(ctx context.Context, clip *domain.AudioClip) error {
	payload, err := liveAudioJSON(clip)
	if err != nil {
		return err
	}
	session.reconnectMu.Lock()
	defer session.reconnectMu.Unlock()
	protocol, err := session.currentProtocol()
	if err != nil {
		return err
	}
	if err := protocol.AppendAudio(ctx, payload); err != nil {
		return networkOutcome(err)
	}
	return nil
}

func (session *openAILiveSession) RequestResponse(ctx context.Context) (<-chan Event, <-chan error) {
	events, errors, err := session.StartResponse(ctx)
	if err == nil {
		return events, errors
	}
	fallbackEvents := make(chan Event)
	fallbackErrors := make(chan error, 1)
	close(fallbackEvents)
	fallbackErrors <- err
	close(fallbackErrors)
	return fallbackEvents, fallbackErrors
}

func (session *openAILiveSession) StartResponse(ctx context.Context) (<-chan Event, <-chan error, error) {
	session.reconnectMu.Lock()
	defer session.reconnectMu.Unlock()
	events := make(chan Event, 16)
	errors := make(chan error, 1)
	response := &openAILiveResponse{ctx: ctx, events: events, errors: errors}

	session.mu.Lock()
	if session.response != nil {
		session.mu.Unlock()
		return nil, nil, &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "OpenAI Live already has an active delegated response"}
	}
	select {
	case <-session.ctx.Done():
		session.mu.Unlock()
		return nil, nil, &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live session is closed"}
	default:
	}
	session.response = response
	session.mu.Unlock()

	protocol, err := session.currentProtocol()
	if err != nil {
		session.finishResponse(response, err)
		return nil, nil, err
	}
	if err := protocol.RequestResponse(ctx); err != nil {
		err = networkOutcome(err)
		session.finishResponse(response, err)
		return nil, nil, err
	}
	return events, errors, nil
}

func (session *openAILiveSession) readLoop(protocol *openAILiveProtocol, readDone chan struct{}, serverClosed chan struct{}) {
	defer close(readDone)
	defer session.finishActiveResponse(&RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live session ended"})
	for {
		observation, err := protocol.NextServerEvent(session.ctx)
		if err != nil {
			if session.ctx.Err() == nil {
				session.finishActiveResponse(networkOutcome(err))
			}
			return
		}
		switch observation.Type {
		case "session.closed":
			closeSignal(serverClosed)
			return
		case "error":
			session.finishActiveResponse(&RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live server reported an error"})
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
		session.finishResponse(response, nil)
	case "response.failed", "response.incomplete":
		session.finishResponse(response, &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live delegated response did not complete"})
	}
}

func realtimeResponseTerminal(eventType string) bool {
	return eventType == "response.completed" || eventType == "response.failed" || eventType == "response.incomplete"
}

func (session *openAILiveSession) finishActiveResponse(err error) {
	session.mu.Lock()
	response := session.response
	session.mu.Unlock()
	if response != nil {
		session.finishResponse(response, err)
	}
}

func (session *openAILiveSession) finishResponse(response *openAILiveResponse, err error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.response != response || response.closed {
		return
	}
	response.closed = true
	if err != nil {
		select {
		case response.errors <- err:
		default:
		}
	}
	close(response.events)
	close(response.errors)
	session.response = nil
}

func (session *openAILiveSession) currentProtocol() (*openAILiveProtocol, error) {
	session.connectionMu.RLock()
	protocol := session.protocol
	session.connectionMu.RUnlock()
	if protocol == nil {
		return nil, &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live connection is unavailable"}
	}
	return protocol, nil
}

func (session *openAILiveSession) Reconnect(ctx context.Context) error {
	if !session.store || !session.capabilities.Supports(RealtimeReconnect) {
		return &RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "OpenAI Live reconnect requires explicit stored-session policy"}
	}
	session.reconnectMu.Lock()
	defer session.reconnectMu.Unlock()
	select {
	case <-session.ctx.Done():
		return &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "OpenAI Live session is closed"}
	default:
	}
	session.mu.Lock()
	activeResponse := session.response != nil
	session.mu.Unlock()
	if activeResponse {
		return &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "OpenAI Live cannot reconnect while a delegated response is active or ambiguous"}
	}

	session.connectionMu.RLock()
	oldProtocol, oldReadDone, sourceSessionID := session.protocol, session.readDone, session.sessionID
	session.connectionMu.RUnlock()
	if sourceSessionID == "" {
		return &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "OpenAI Live did not provide a stored session ID for reconnect"}
	}
	if oldProtocol != nil {
		_ = oldProtocol.Close()
	}
	if oldReadDone != nil {
		select {
		case <-oldReadDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	endpoint, err := openAILiveForkEndpoint(session.primaryEndpoint, sourceSessionID)
	if err != nil {
		return err
	}
	forkConfig, err := openAILiveForkConfig(session.store)
	if err != nil {
		return err
	}
	protocol, err := dialOpenAILiveTransport(ctx, session.connector, openAILiveTransportConfig{
		Endpoint: endpoint, APIKey: session.apiKey, Session: forkConfig, ResponsesDelegated: session.responsesDelegated,
	})
	if err != nil {
		return networkOutcome(err)
	}
	newSessionID, err := waitForOpenAILiveStart(ctx, protocol)
	if err != nil {
		_ = protocol.Close()
		return err
	}
	if newSessionID == "" {
		_ = protocol.Close()
		return &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "OpenAI Live fork did not provide a new session ID"}
	}
	readDone := make(chan struct{})
	serverClosed := make(chan struct{})
	session.connectionMu.Lock()
	session.protocol = protocol
	session.readDone = readDone
	session.serverClosed = serverClosed
	session.sessionID = newSessionID
	session.connectionMu.Unlock()
	go session.readLoop(protocol, readDone, serverClosed)
	return nil
}

func openAILiveForkEndpoint(primaryEndpoint, sessionID string) (string, error) {
	endpoint, err := parseRealtimeEndpoint(primaryEndpoint)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "OpenAI Live fork requires a source session ID"}
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + url.PathEscape(sessionID) + "/fork"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}

func openAILiveForkConfig(store bool) (json.RawMessage, error) {
	config := map[string]any{
		"audio": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": openAILiveSampleRate}},
	}
	if store {
		config["store"] = true
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "OpenAI Live fork config is invalid"}
	}
	return raw, nil
}

func (session *openAILiveSession) Close() (closeErr error) {
	session.closeOnce.Do(func() {
		session.reconnectMu.Lock()
		defer session.reconnectMu.Unlock()
		session.connectionMu.RLock()
		protocol, readDone, serverClosed := session.protocol, session.readDone, session.serverClosed
		session.connectionMu.RUnlock()
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		if protocol != nil {
			if err := protocol.CloseSession(ctx); err != nil {
				closeErr = err
			} else if serverClosed != nil {
				select {
				case <-serverClosed:
				case <-ctx.Done():
				}
			}
		}
		cancel()
		session.cancel()
		if protocol != nil {
			if err := protocol.Close(); closeErr == nil && err != nil {
				closeErr = err
			}
		}
		if readDone != nil {
			<-readDone
		}
		session.connectionMu.Lock()
		session.protocol = nil
		session.connectionMu.Unlock()
	})
	return closeErr
}

func closeSignal(signal chan struct{}) {
	if signal == nil {
		return
	}
	select {
	case <-signal:
	default:
		close(signal)
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
