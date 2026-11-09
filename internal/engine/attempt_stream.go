package engine

import (
	"context"
	"errors"
	"strings"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

const maxAttemptTextBytes = 16 << 20

// attemptStream consumes provider channels; it neither admits calls nor changes
// model health, graph state, or resource ownership.
type attemptStream struct {
	events                <-chan providers.Event
	errors                <-chan error
	request               providers.Request
	started               time.Time
	emit                  func(domain.TraceEvent)
	push                  func(string)
	buffered              bool
	text                  strings.Builder
	ttft                  int64
	outputTokens          int
	firstSeen             bool
	firstTimer, idleTimer *time.Timer
	firstC, idleC         <-chan time.Time
}

func (s *attemptStream) consume(ctx, parent context.Context, cancel context.CancelFunc) (domain.Outcome, error) {
	s.startTimers()
	defer s.stopTimers()
	for s.events != nil || s.errors != nil {
		select {
		case event, ok := <-s.events:
			if !ok {
				s.events = nil
				continue
			}
			if err := s.accept(event); err != nil {
				return providers.OutcomeOf(err), err
			}
		case err, ok := <-s.errors:
			if !ok {
				s.errors = nil
				continue
			}
			if err != nil {
				var requestError *providers.RequestError
				if errors.As(err, &requestError) {
					s.emit(domain.TraceEvent{Kind: "http_status", HTTPStatus: requestError.StatusCode})
				}
				return providers.OutcomeOf(err), err
			}
		case <-s.firstC:
			cancel()
			return attemptTimeout(domain.OutcomeFirstTokenTimeout, "first token timeout")
		case <-s.idleC:
			cancel()
			return attemptTimeout(domain.OutcomeStreamIdleTimeout, "stream idle timeout")
		case <-ctx.Done():
			outcome := domain.OutcomeTimeout
			if parent.Err() == context.Canceled {
				outcome = domain.OutcomeCancelled
			}
			return outcome, ctx.Err()
		}
	}
	return domain.OutcomeSuccess, nil
}

func (s *attemptStream) accept(event providers.Event) error {
	switch event.Type {
	case providers.EventFirstToken:
		s.markFirstToken()
	case providers.EventDelta:
		s.markFirstToken()
		s.resetIdle()
		if s.text.Len()+len(event.Text) > maxAttemptTextBytes {
			s.ttft = 0 // Preserve the invalid-output trace/statistics contract.
			return &providers.RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "Model text exceeded the 16 MiB safety limit"}
		}
		s.text.WriteString(event.Text)
		s.push(event.Text)
	case providers.EventDone:
		if event.OutputTokens > 0 {
			s.outputTokens = event.OutputTokens
		}
	}
	return nil
}

func (s *attemptStream) markFirstToken() {
	if s.firstSeen {
		return
	}
	s.firstSeen = true
	s.ttft = time.Since(s.started).Milliseconds()
	if !s.buffered {
		s.emit(domain.TraceEvent{Kind: "first_token", TTFTMS: s.ttft})
	}
	stopAttemptTimer(s.firstTimer)
	s.firstC = nil
}

func (s *attemptStream) startTimers() {
	s.firstTimer = time.NewTimer(s.request.FirstTokenTimeout)
	s.firstC = s.firstTimer.C
	if s.buffered {
		stopAttemptTimer(s.firstTimer)
		s.firstC = nil
		s.emit(domain.TraceEvent{Kind: "buffered", Message: "Provider returns a complete answer; TTFT/TPS are unavailable."})
	}
}

func (s *attemptStream) resetIdle() {
	if s.request.StreamIdleTimeout <= 0 {
		return
	}
	if s.idleTimer == nil {
		s.idleTimer = time.NewTimer(s.request.StreamIdleTimeout)
	} else {
		stopAttemptTimer(s.idleTimer)
		s.idleTimer.Reset(s.request.StreamIdleTimeout)
	}
	s.idleC = s.idleTimer.C
}

func (s *attemptStream) stopTimers() {
	stopAttemptTimer(s.firstTimer)
	stopAttemptTimer(s.idleTimer)
}

func stopAttemptTimer(timer *time.Timer) {
	if timer != nil && !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func attemptTimeout(outcome domain.Outcome, message string) (domain.Outcome, error) {
	return outcome, &providers.RequestError{Outcome: outcome, Message: message, Retryable: true}
}

func (s *attemptStream) successMetrics(duration time.Duration) (int64, float64) {
	if s.buffered {
		return 0, 0
	}
	generation := duration - time.Duration(s.ttft)*time.Millisecond
	if generation <= 0 {
		generation = duration
	}
	if s.outputTokens > 0 && generation >= time.Millisecond {
		return s.ttft, float64(s.outputTokens) / generation.Seconds()
	}
	return s.ttft, 0
}
