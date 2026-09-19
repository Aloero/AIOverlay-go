package providers

import (
	"aioverlay/internal/domain"
	"context"
	"fmt"
)

type RealtimeOperation string

const (
	RealtimeAppendAudio      RealtimeOperation = "append_audio"
	RealtimeUpdateImage      RealtimeOperation = "update_image"
	RealtimeRequestResponse  RealtimeOperation = "request_response"
	RealtimeSemanticNoAction RealtimeOperation = "semantic_no_action"
	RealtimeTextOutput       RealtimeOperation = "text_output"
	RealtimeInterrupt        RealtimeOperation = "interrupt"
	RealtimeCleanSession     RealtimeOperation = "clean_session"
	RealtimeReconnect        RealtimeOperation = "reconnect"
	RealtimeVPNFailClosed    RealtimeOperation = "vpn_fail_closed"
)

var realtimeOperations = []RealtimeOperation{
	RealtimeAppendAudio,
	RealtimeUpdateImage,
	RealtimeRequestResponse,
	RealtimeSemanticNoAction,
	RealtimeTextOutput,
	RealtimeInterrupt,
	RealtimeCleanSession,
	RealtimeReconnect,
	RealtimeVPNFailClosed,
}

// RealtimeCapabilities describes operations proven by an adapter contract.
// Model metadata never enables these operations by itself.
type RealtimeCapabilities struct {
	Available  bool                       `json:"available"`
	Operations map[RealtimeOperation]bool `json:"operations"`
	Reason     string                     `json:"reason,omitempty"`
}

func UnsupportedRealtime(reason string) RealtimeCapabilities {
	operations := make(map[RealtimeOperation]bool, len(realtimeOperations))
	for _, operation := range realtimeOperations {
		operations[operation] = false
	}
	return RealtimeCapabilities{Operations: operations, Reason: reason}
}

func normalizeRealtimeCapabilities(capabilities RealtimeCapabilities) RealtimeCapabilities {
	operations := make(map[RealtimeOperation]bool, len(realtimeOperations))
	for _, operation := range realtimeOperations {
		operations[operation] = capabilities.Operations[operation]
	}
	capabilities.Operations = operations
	return capabilities
}

func (c RealtimeCapabilities) Supports(operation RealtimeOperation) bool {
	return c.Available && c.Operations[operation]
}

func (c RealtimeCapabilities) Missing(required ...RealtimeOperation) []RealtimeOperation {
	missing := make([]RealtimeOperation, 0, len(required))
	for _, operation := range required {
		if !c.Supports(operation) {
			missing = append(missing, operation)
		}
	}
	return missing
}

type RealtimeCapabilityProvider interface {
	RealtimeCapabilities() RealtimeCapabilities
}

// RealtimeSession owns remote memory and connection lifetime only. Optional
// operations stay in small interfaces so HTTP adapters do not grow dummy methods.
type RealtimeSession interface {
	Capabilities() RealtimeCapabilities
	Close() error
}

type RealtimeAudioAppender interface {
	AppendAudio(context.Context, *domain.AudioClip) error
}

type RealtimeImageUpdater interface {
	UpdateImage(context.Context, []byte) error
}

type RealtimeResponseRequester interface {
	RequestResponse(context.Context) (<-chan Event, <-chan error)
}

// RealtimeResponseStarter separates synchronous admission/dispatch errors from
// the asynchronous response stream. Schedulers use it so rejected starts do not
// consume a committed call budget.
type RealtimeResponseStarter interface {
	StartResponse(context.Context) (<-chan Event, <-chan error, error)
}

type RealtimeInterrupter interface {
	Interrupt(context.Context) error
}

type RealtimeSessionCleaner interface {
	Clean(context.Context) error
}

type RealtimeReconnector interface {
	Reconnect(context.Context) error
}

type RealtimeSessionFactory interface {
	OpenRealtime(context.Context, RealtimeOpenRequest) (RealtimeSession, error)
}

type RealtimeOpenRequest struct {
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
	Credential string `json:"-"`
}

type AdapterCapabilities struct {
	ProviderID string               `json:"provider_id"`
	Label      string               `json:"label"`
	Kind       domain.ProviderKind  `json:"kind"`
	Media      MediaCapabilities    `json:"media"`
	Realtime   RealtimeCapabilities `json:"realtime"`
}

func adapterCapabilities(config domain.ProviderConfig, adapter Provider) AdapterCapabilities {
	realtime := UnsupportedRealtime("Persistent realtime transport is not implemented for this adapter")
	if capabilityProvider, ok := adapter.(RealtimeCapabilityProvider); ok {
		realtime = normalizeRealtimeCapabilities(capabilityProvider.RealtimeCapabilities())
		if realtime.Available {
			if _, ok := adapter.(RealtimeSessionFactory); !ok {
				realtime = UnsupportedRealtime("Realtime capability contract is incomplete: session factory is missing")
			}
		}
	}
	return AdapterCapabilities{
		ProviderID: config.ID,
		Label:      config.Label,
		Kind:       config.Kind,
		Media:      Media(config),
		Realtime:   realtime,
	}
}

func openRealtime(ctx context.Context, adapter Provider, request RealtimeOpenRequest) (RealtimeSession, error) {
	capabilityProvider, ok := adapter.(RealtimeCapabilityProvider)
	if !ok {
		return nil, &RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "Persistent realtime transport is not implemented for this adapter"}
	}
	capabilities := normalizeRealtimeCapabilities(capabilityProvider.RealtimeCapabilities())
	if !capabilities.Available {
		message := capabilities.Reason
		if message == "" {
			message = "Persistent realtime transport is unavailable"
		}
		return nil, &RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: message}
	}
	factory, ok := adapter.(RealtimeSessionFactory)
	if !ok {
		return nil, fmt.Errorf("realtime adapter contract is invalid: session factory is missing")
	}
	session, err := factory.OpenRealtime(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := validateRealtimeSessionContract(session, capabilities); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

func validateRealtimeSessionContract(session RealtimeSession, advertised RealtimeCapabilities) error {
	if session == nil {
		return fmt.Errorf("realtime adapter returned a nil session")
	}
	sessionCapabilities := normalizeRealtimeCapabilities(session.Capabilities())
	for _, operation := range realtimeOperations {
		if advertised.Supports(operation) && !sessionCapabilities.Supports(operation) {
			return fmt.Errorf("realtime session does not preserve advertised capability %s", operation)
		}
	}
	checks := []struct {
		operation RealtimeOperation
		supported bool
	}{
		{RealtimeAppendAudio, implementsRealtimeAudioAppender(session)},
		{RealtimeUpdateImage, implementsRealtimeImageUpdater(session)},
		{RealtimeRequestResponse, implementsRealtimeResponseRequester(session)},
		{RealtimeSemanticNoAction, implementsRealtimeResponseRequester(session)},
		{RealtimeTextOutput, implementsRealtimeResponseRequester(session)},
		{RealtimeInterrupt, implementsRealtimeInterrupter(session)},
		{RealtimeCleanSession, implementsRealtimeCleaner(session)},
		{RealtimeReconnect, implementsRealtimeReconnector(session)},
	}
	for _, check := range checks {
		if advertised.Supports(check.operation) && !check.supported {
			return fmt.Errorf("realtime session advertises %s without implementing its contract", check.operation)
		}
	}
	return nil
}

func implementsRealtimeAudioAppender(session RealtimeSession) bool {
	_, ok := session.(RealtimeAudioAppender)
	return ok
}
func implementsRealtimeImageUpdater(session RealtimeSession) bool {
	_, ok := session.(RealtimeImageUpdater)
	return ok
}
func implementsRealtimeResponseRequester(session RealtimeSession) bool {
	if _, ok := session.(RealtimeResponseStarter); ok {
		return true
	}
	_, ok := session.(RealtimeResponseRequester)
	return ok
}
func implementsRealtimeInterrupter(session RealtimeSession) bool {
	_, ok := session.(RealtimeInterrupter)
	return ok
}
func implementsRealtimeCleaner(session RealtimeSession) bool {
	_, ok := session.(RealtimeSessionCleaner)
	return ok
}
func implementsRealtimeReconnector(session RealtimeSession) bool {
	_, ok := session.(RealtimeReconnector)
	return ok
}
