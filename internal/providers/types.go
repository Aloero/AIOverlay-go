package providers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"aioverlay/internal/domain"
)

type Request struct {
	Audio             *domain.AudioClip
	Task              string
	ProviderID        string
	ModelID           string
	SystemPrompt      string
	UserPrompt        string
	ImageJPEG         []byte
	TextInput         string
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
	Retryable  bool
}

func (e *RequestError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s (HTTP %d): %s", e.Outcome, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Outcome, e.Message)
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
