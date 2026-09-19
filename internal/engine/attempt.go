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

	emit := func(ev domain.TraceEvent) {
		ev.PlanID = executionPlanID(ctx)
		if ev.Outcome == domain.OutcomeCancelled {
			ev.CancelReason = cancellationReason(ctx)
		}
		r.emit(ev)
	}
	semHeld := false
	select {
	case r.sem <- struct{}{}:
		semHeld = true
	case <-ctx.Done():
		return "", domain.OutcomeCancelled, ctx.Err()
	}
	if ctx.Err() != nil {
		return "", domain.OutcomeCancelled, ctx.Err()
	}
	defer func() {
		if semHeld {
			<-r.sem
		}
	}()
	var lease CallLease
	leaseHeld := false
	if r.opt.BeforeCall != nil {
		var gateErr error
		lease, gateErr = r.opt.BeforeCall(ctx)
		if gateErr != nil {
			return "", providers.OutcomeOf(gateErr), gateErr
		}
		if lease != nil {
			leaseHeld = true
			defer func() {
				if leaseHeld {
					lease.Release()
				}
			}()
		}
	}
	if ctx.Err() != nil {
		return "", domain.OutcomeCancelled, ctx.Err()
	}
	if err := r.admit(n, pool, m, key); err != nil {
		emit(domain.TraceEvent{Kind: "budget_exhausted", NodeID: n.ID, BudgetScope: budgetScope(err), Message: err.Error()})
		return "", domain.OutcomeCancelled, err
	}
	if lease != nil {
		lease.Commit()
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
	attempt := &domain.AttemptRef{ID: attemptID, SlotID: m.ID, ProviderID: m.ProviderID, ModelID: m.ModelID, CredentialID: key.ID, CredentialAlias: key.Label, Transport: "Direct"}
	if pc, ok := findProvider(r.engine.cfg, m.ProviderID); ok && pc.Kind == domain.ProviderEmbeddedOpenCode {
		attempt.Transport = "Via OpenCode"
	}
	if attempt.CredentialAlias == "" {
		attempt.CredentialAlias = key.ID
	}
	emit(domain.TraceEvent{Kind: "attempt_start", NodeID: n.ID, Attempt: attempt, Message: fmt.Sprintf("priority=%d", m.Priority)})
	pc, _ := findProvider(r.engine.cfg, m.ProviderID)
	limits := ResolveLimits(m, r.pipeline.Budget, pc.RequestDefaults)
	emit(domain.TraceEvent{Kind: "effective_limits", NodeID: n.ID, Attempt: attempt, Message: fmt.Sprintf("connect=%dms; first_token=%dms; response=%dms; idle=%dms; output_tokens=%d", limits.ConnectMS, limits.FirstTokenMS, limits.ResponseMS, limits.IdleMS, limits.MaxOutputTokens)})
	respTimeout := durationMS(limits.ResponseMS)
	actx, cancel := context.WithTimeout(ctx, respTimeout)
	defer cancel()
	values := PromptValues{InputText: r.input.Text, PreviousOutput: input.text, VisionText: input.vision, AudioText: input.transcript, ContextText: input.contextText}
	system, user := RenderRequestPrompt(pool.Prompt, values)
	req := providers.Request{Audio: input.audio, Task: pool.Task, ProviderID: m.ProviderID, ModelID: m.ModelID, SystemPrompt: system, UserPrompt: user, ImageJPEG: input.image, TextInput: input.text, MaxOutputTokens: limits.MaxOutputTokens, ConnectTimeout: durationMS(limits.ConnectMS), FirstTokenTimeout: durationMS(limits.FirstTokenMS), ResponseTimeout: respTimeout, StreamIdleTimeout: durationMS(limits.IdleMS)}
	if r.opt.OnPrompt != nil {
		snapshot := BoundPromptSnapshot(PromptSnapshot{NodeID: r.redact.text(n.ID), ProviderID: r.redact.text(m.ProviderID), ModelID: r.redact.text(m.ModelID), ImageAttached: len(req.ImageJPEG) > 0, Values: PromptValues{InputText: r.redact.text(values.InputText), PreviousOutput: r.redact.text(values.PreviousOutput), VisionText: r.redact.text(values.VisionText), AudioText: r.redact.text(values.AudioText), ContextText: r.redact.text(values.ContextText)}})
		r.callbackMu.Lock()
		r.opt.OnPrompt(snapshot)
		r.callbackMu.Unlock()
	}
	events, errs := p.Stream(actx, req, key.Secret)
	defer func() {
		cancel()
		remainingEvents, remainingErrors, complete := drainProviderChannels(events, errs, 1500*time.Millisecond)
		if complete {
			return
		}
		emit(domain.TraceEvent{Kind: "worker_cleanup_timeout", NodeID: n.ID, Attempt: attempt, Outcome: domain.OutcomeProviderUnavailable, Message: "Provider did not acknowledge cancellation within 1500ms; concurrency remains reserved until local teardown completes"})
		heldLease := lease
		semHeld = false
		leaseHeld = false
		go holdAttemptResourcesUntilProviderStops(r.sem, heldLease, remainingEvents, remainingErrors)
	}()
	buffered := false
	if mode, ok := p.(interface{ Incremental() bool }); ok {
		buffered = !mode.Incremental()
	}
	firstTokenTimer := time.NewTimer(req.FirstTokenTimeout)
	var firstTokenC <-chan time.Time = firstTokenTimer.C
	if buffered {
		firstTokenTimer.Stop()
		firstTokenC = nil
		emit(domain.TraceEvent{Kind: "buffered", NodeID: n.ID, Attempt: attempt, Message: "Provider returns a complete answer; TTFT/TPS are unavailable."})
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
						emit(domain.TraceEvent{Kind: "first_token", NodeID: n.ID, Attempt: attempt, TTFTMS: ttft})
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
						emit(domain.TraceEvent{Kind: "first_token", NodeID: n.ID, Attempt: attempt, TTFTMS: ttft})
					}
				}
				resetIdle()
				if text.Len()+len(ev.Text) > 16<<20 {
					err := &providers.RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "Model text exceeded the 16 MiB safety limit"}
					emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: err.Outcome, Message: err.Message, DurationMS: time.Since(started).Milliseconds()})
					r.engine.recordModel(m, err.Outcome, 0, time.Since(started))
					return "", err.Outcome, err
				}
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
					emit(domain.TraceEvent{Kind: "http_status", NodeID: n.ID, Attempt: attempt, HTTPStatus: pe.StatusCode})
				}
				emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: outcome, Message: err.Error(), TTFTMS: ttft, DurationMS: time.Since(started).Milliseconds()})
				r.engine.recordModel(m, outcome, time.Duration(ttft)*time.Millisecond, time.Since(started))
				return "", outcome, err
			}
		case <-firstTokenC:
			if !firstTokenSeen {
				cancel()
				err := &providers.RequestError{Outcome: domain.OutcomeFirstTokenTimeout, Message: "first token timeout", Retryable: true}
				emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: err.Outcome, Message: err.Error(), DurationMS: time.Since(started).Milliseconds()})
				r.engine.recordModel(m, err.Outcome, time.Duration(ttft)*time.Millisecond, time.Since(started))
				return "", err.Outcome, err
			}
		case <-idleC:
			cancel()
			err := &providers.RequestError{Outcome: domain.OutcomeStreamIdleTimeout, Message: "stream idle timeout", Retryable: true}
			emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: err.Outcome, Message: err.Error(), TTFTMS: ttft, DurationMS: time.Since(started).Milliseconds()})
			r.engine.recordModel(m, err.Outcome, time.Duration(ttft)*time.Millisecond, time.Since(started))
			return "", err.Outcome, err
		case <-actx.Done():
			outcome := domain.OutcomeTimeout
			if ctx.Err() == context.Canceled {
				outcome = domain.OutcomeCancelled
			}
			err := actx.Err()
			emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: outcome, Message: err.Error(), TTFTMS: ttft, DurationMS: time.Since(started).Milliseconds()})
			r.engine.recordModel(m, outcome, time.Duration(ttft)*time.Millisecond, time.Since(started))
			return "", outcome, err
		}
	}
	if strings.TrimSpace(text.String()) == "" && !allowsEmptyModelResult(pool) {
		err := fmt.Errorf("empty response")
		emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: domain.OutcomeInvalidResponse, Message: err.Error(), DurationMS: time.Since(started).Milliseconds()})
		r.engine.recordModel(m, domain.OutcomeInvalidResponse, 0, time.Since(started))
		return "", domain.OutcomeInvalidResponse, err
	}
	if n.Type == domain.NodeDecision {
		if _, err := parseDecision(text.String()); err != nil {
			emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: domain.OutcomeInvalidResponse, Message: err.Error(), DurationMS: time.Since(started).Milliseconds()})
			r.engine.recordModel(m, domain.OutcomeInvalidResponse, 0, time.Since(started))
			return "", domain.OutcomeInvalidResponse, err
		}
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
	emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: attempt, Outcome: domain.OutcomeSuccess, TTFTMS: ttft, DurationMS: dur.Milliseconds(), OutputTokens: outTokens, TPS: tps})
	r.engine.recordModel(m, domain.OutcomeSuccess, time.Duration(ttft)*time.Millisecond, dur)
	success = true
	r.traceMu.Lock()
	r.successes[n.ID+"/"+m.ID] = attempt
	r.traceMu.Unlock()
	return r.redact.text(strings.TrimSpace(text.String())), domain.OutcomeSuccess, nil
}

func allowsEmptyModelResult(pool domain.ModelPoolConfig) bool {
	return pool.Task == "transcribe" || pool.ResultName == "audio.text"
}

func drainProviderChannels(events <-chan providers.Event, errs <-chan error, timeout time.Duration) (<-chan providers.Event, <-chan error, bool) {
	if events == nil && errs == nil {
		return nil, nil, true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for events != nil || errs != nil {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case _, ok := <-errs:
			if !ok {
				errs = nil
			}
		case <-timer.C:
			return events, errs, false
		}
	}
	return nil, nil, true
}

func holdAttemptResourcesUntilProviderStops(sem chan struct{}, lease CallLease, events <-chan providers.Event, errs <-chan error) {
	for events != nil || errs != nil {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case _, ok := <-errs:
			if !ok {
				errs = nil
			}
		}
	}
	<-sem
	if lease != nil {
		lease.Release()
	}
}

func (r *runner) contextualizeNodeInput(node domain.Node, input nodeValue) nodeValue {
	mode := node.ContextMode
	if mode == "" && node.ContextID != "" {
		mode = "named"
	}
	switch mode {
	case "none":
		input.contextText = ""
		if !input.imagePinned {
			input.image = nil
			input.imageResolved = false
		}
		return input
	case "named":
		view, ok := r.input.ContextViews[node.ContextID]
		if !ok {
			input.contextText = ""
			if !input.imagePinned {
				input.image = nil
				input.imageResolved = false
			}
			return input
		}
		input.contextText = view.Text
		if !input.imagePinned {
			input.image = append(input.image[:0], view.ImageJPEG...)
			input.imageResolved = false
		}
	}
	return input
}
