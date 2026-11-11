package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"aioverlay/internal/domain"
)

type Request struct {
	Audio        *domain.AudioClip
	Task         string
	ProviderID   string
	ModelID      string
	SystemPrompt string
	UserPrompt   string
	ImageJPEG    []byte
	TextInput    string
	// AppParams are interpreted solely by the delivery application of a harness transport
	// (reasoning effort, thinking mode); direct protocol adapters ignore them.
	AppParams         map[string]json.RawMessage
	MaxOutputTokens   int
	ConnectTimeout    time.Duration
	FirstTokenTimeout time.Duration
	ResponseTimeout   time.Duration
	StreamIdleTimeout time.Duration
}

type EventType string

const (
	EventStart      EventType = "start"
	EventFirstToken EventType = "first_token"
	EventDelta      EventType = "delta"
	EventDone       EventType = "done"
)

type Event struct {
	Type         EventType
	Text         string
	ModelID      string
	Outcome      domain.Outcome
	InputTokens  int
	OutputTokens int
}

type Provider interface {
	ID() string
	Stream(ctx context.Context, req Request, credential string) (<-chan Event, <-chan error)
}

type RequestError struct {
	Outcome    domain.Outcome
	StatusCode int
	Message    string
	// Detail is a bounded reason quoted from the provider when it stated one - a quota limit, a
	// retired model. Message stays the stable sentence the UI and the tests match on; Detail is the
	// evidence a human acts on. It is never the raw response body and never a credential.
	Detail    string
	Retryable bool
}

func (e *RequestError) Error() string {
	head := string(e.Outcome)
	if e.StatusCode != 0 {
		head = fmt.Sprintf("%s (HTTP %d)", e.Outcome, e.StatusCode)
	}
	if strings.TrimSpace(e.Detail) != "" {
		return fmt.Sprintf("%s: %s - %s", head, e.Message, e.Detail)
	}
	return fmt.Sprintf("%s: %s", head, e.Message)
}

func OutcomeOf(err error) domain.Outcome {
	if err == nil {
		return domain.OutcomeSuccess
	}
	var e *RequestError
	if errors.As(err, &e) {
		return e.Outcome
	}
	if errors.Is(err, context.Canceled) {
		return domain.OutcomeCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.OutcomeTimeout
	}
	return domain.OutcomeInvalidResponse
}

func sendEvent(ctx context.Context, ch chan<- Event, ev Event) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
