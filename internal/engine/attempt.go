package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func (r *runner) singleAttempt(ctx context.Context, n domain.Node, pool domain.ModelPoolConfig, input nodeValue, m domain.ModelSlot, p providers.Provider, key domain.Credential) (string, domain.Outcome, error) {

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return "", domain.OutcomeCancelled, ctx.Err()
	}
	if ctx.Err() != nil {
		return "", domain.OutcomeCancelled, ctx.Err()
	}
	if err := r.admit(n, pool, m, key); err != nil {
		r.emit(domain.TraceEvent{Kind: "budget_exhausted", NodeID: n.ID, Message: err.Error()})
		return "", domain.OutcomeCancelled, err
	}
	started := time.Now()
	attemptID := newID("attempt")
	gate, _ := ctx.Value(streamKey{}).(*streamGate)
	success := false
	defer func() {
		if !success && gate != nil {
			gate.release(attemptID)
		}
	}()
	attempt := &domain.AttemptRef{ID: attemptID, ProviderID: m.ProviderID, ModelID: m.ModelID, CredentialID: key.ID, CredentialAlias: key.Label, Transport: "Direct"}
	if pc, ok := findProvider(r.engine.cfg, m.ProviderID); ok && pc.Kind == domain.ProviderEmbeddedOpenCode {
		attempt.Transport = "Via OpenCode"
	}
	if attempt.CredentialAlias == "" {
		attempt.CredentialAlias = key.ID
	}
	r.emit(domain.TraceEvent{Kind: "attempt_start", NodeID: n.ID, Attempt: attempt, Message: fmt.Sprintf("priority=%d", m.Priority)})
	respTimeout := durationMS(firstPositive(m.TimeoutMS, r.pipeline.Budget.ResponseTimeoutMS, 15000))
	actx, cancel := context.WithTimeout(ctx, respTimeout)
	defer cancel()
	user := renderPrompt(pool.Prompt.User, input.text)
	system := renderPrompt(pool.Prompt.System, input.text)
	req := providers.Request{ProviderID: m.ProviderID, ModelID: m.ModelID, SystemPrompt: system, UserPrompt: user, ImageJPEG: input.image, TextInput: input.text, MaxOutputTokens: firstPositive(pcMaxOutput(r.engine.cfg, m.ProviderID), 2048), ConnectTimeout: durationMS(r.pipeline.Budget.ConnectTimeoutMS), FirstTokenTimeout: durationMS(firstPositive(m.FirstTokenTimeoutMS, r.pipeline.Budget.FirstTokenTimeoutMS, 8000)), ResponseTimeout: respTimeout, StreamIdleTimeout: durationMS(r.pipeline.Budget.StreamIdleTimeoutMS)}
	events, errs := p.Stream(actx, req, key.Secret)
	buffered := false
	if mode, ok := p.(interface{ Incremental() bool }); ok {
		buffered = !mode.Incremental()
	}
	firstTokenTimer := time.NewTimer(req.FirstTokenTimeout)
	var firstTokenC <-chan time.Time = firstTokenTimer.C
	if buffered {
		firstTokenTimer.Stop()
		firstTokenC = nil
		r.emit(domain.TraceEvent{Kind: "buffered", NodeID: n.ID, Attempt: attempt, Message: "Provider returns a complete answer; TTFT/TPS are unavailable."})
	}
	defer firstTokenTimer.Stop()
	var idleTimer *time.Timer
	var idleC <-chan time.Time
	defer func() {
		if idleTimer != nil {
			idleTimer.Stop()
		}
	}()
	resetIdle := func() {
		if req.StreamIdleTimeout <= 0 {
			return
		}
		if idleTimer == nil {
			idleTimer = time.NewTimer(req.StreamIdleTimeout)
		} else {
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(req.StreamIdleTimeout)
		}
		idleC = idleTimer.C
	}
	firstTokenSeen := false
	var text strings.Builder
	var ttft int64
	var outTokens int
	for events != nil || errs != nil {
		select {
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			switch ev.Type {
			case providers.EventFirstToken:
				if !firstTokenSeen {
					firstTokenSeen = true
					ttft = time.Since(started).Milliseconds()
					if !buffered {
						r.emit(domain.TraceEvent{Kind: "first_token", NodeID: n.ID, Attempt: attempt, TTFTMS: ttft})
					}
					if !firstTokenTimer.Stop() {
						select {
						case <-firstTokenTimer.C:
						default:
						}
					}
				}
			case providers.EventDelta:
				if !firstTokenSeen {
					firstTokenSeen = true
					ttft = time.Since(started).Milliseconds()
					if !buffered {
						r.emit(domain.TraceEvent{Kind: "first_token", NodeID: n.ID, Attempt: attempt, TTFTMS: ttft})
					}
				}
				resetIdle()
				text.WriteString(ev.Text)
				if gate != nil {
					gate.push(attemptID, ev.Text)
				}
			case providers.EventDone:
				if ev.OutputTokens > 0 {
					outTokens = ev.OutputTokens
				}
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				outcome := providers.OutcomeOf(err)
				var pe *providers.RequestError
				if errors.As(err, &pe) {
					r.emit(domain.TraceEvent{Kind: "http_status", NodeID: n.ID, Attempt: attempt, HTTPStatus: pe.StatusCode})
				}
				r.emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: outcome, Message: err.Error(), TTFTMS: ttft, DurationMS: time.Since(started).Milliseconds()})
				r.engine.recordModel(m, outcome, time.Duration(ttft)*time.Millisecond, time.Since(started))
				return "", outcome, err
			}
		case <-firstTokenC:
			if !firstTokenSeen {
				cancel()
				err := &providers.RequestError{Outcome: domain.OutcomeFirstTokenTimeout, Message: "first token timeout", Retryable: true}
				r.emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: err.Outcome, Message: err.Error(), DurationMS: time.Since(started).Milliseconds()})
				return "", err.Outcome, err
			}
		case <-idleC:
			cancel()
			err := &providers.RequestError{Outcome: domain.OutcomeStreamIdleTimeout, Message: "stream idle timeout", Retryable: true}
			r.emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: err.Outcome, Message: err.Error(), TTFTMS: ttft, DurationMS: time.Since(started).Milliseconds()})
			return "", err.Outcome, err
		case <-actx.Done():
			outcome := domain.OutcomeTimeout
			if ctx.Err() == context.Canceled {
				outcome = domain.OutcomeCancelled
			}
			err := actx.Err()
			r.emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: outcome, Message: err.Error(), TTFTMS: ttft, DurationMS: time.Since(started).Milliseconds()})
			return "", outcome, err
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		err := fmt.Errorf("empty response")
		r.emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: domain.OutcomeInvalidResponse, Message: err.Error(), DurationMS: time.Since(started).Milliseconds()})
		return "", domain.OutcomeInvalidResponse, err
	}
	dur := time.Since(started)
	tps := 0.0
	generation := dur - time.Duration(ttft)*time.Millisecond
	if generation <= 0 {
		generation = dur
	}
	if !buffered && outTokens > 0 && generation >= time.Millisecond {
		tps = float64(outTokens) / generation.Seconds()
	}
	if buffered {
		ttft = 0
	}
	r.emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: domain.OutcomeSuccess, TTFTMS: ttft, DurationMS: dur.Milliseconds(), OutputTokens: outTokens, TPS: tps})
	r.engine.recordModel(m, domain.OutcomeSuccess, time.Duration(ttft)*time.Millisecond, dur)
	success = true
	r.traceMu.Lock()
	r.successes[n.ID+"/"+m.ID] = attempt
	r.traceMu.Unlock()
	return strings.TrimSpace(text.String()), domain.OutcomeSuccess, nil
}
