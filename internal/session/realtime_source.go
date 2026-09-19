package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"aioverlay/internal/providers"
)

type RealtimeOpener func(context.Context, string, string) (providers.RealtimeSession, error)

func (m *Manager) openRealtimeSource(ctx context.Context, runtime *sourceRuntime) error {
	config := runtime.cfg.Realtime
	if config == nil {
		return nil
	}
	m.mu.RLock()
	opener := m.realtimeOpen
	m.mu.RUnlock()
	if opener == nil {
		return fmt.Errorf("persistent realtime session opener is unavailable")
	}
	setRealtimeState(runtime, "starting", "")
	session, err := opener(ctx, config.ProviderID, config.ModelID)
	if err != nil {
		setRealtimeState(runtime, "unavailable", err.Error())
		return err
	}
	caps := session.Capabilities()
	if !caps.Supports(providers.RealtimeAppendAudio) {
		_ = session.Close()
		err := fmt.Errorf("realtime adapter does not support audio append")
		setRealtimeState(runtime, "unavailable", err.Error())
		return err
	}
	if config.ResponsePolicy == "each_segment" && (!caps.Supports(providers.RealtimeRequestResponse) || !caps.Supports(providers.RealtimeTextOutput)) {
		_ = session.Close()
		err := fmt.Errorf("realtime adapter does not support delegated text responses")
		setRealtimeState(runtime, "unavailable", err.Error())
		return err
	}
	runtime.realtimeMu.Lock()
	runtime.realtime = session
	runtime.realtimeMu.Unlock()
	setRealtimeState(runtime, "alive", "")
	return nil
}

func (m *Manager) closeRealtimeSource(runtime *sourceRuntime) {
	runtime.realtimeMu.Lock()
	session := runtime.realtime
	runtime.realtime = nil
	runtime.realtimeMu.Unlock()
	if session != nil {
		_ = session.Close()
	}
	setRealtimeState(runtime, "stopped", "")
}

func (m *Manager) realtimeSession(runtime *sourceRuntime) providers.RealtimeSession {
	runtime.realtimeMu.Lock()
	defer runtime.realtimeMu.Unlock()
	return runtime.realtime
}

func setRealtimeState(runtime *sourceRuntime, state, diagnostic string) {
	runtime.mu.Lock()
	runtime.status.RemoteState = state
	if runtime.cfg.Realtime != nil {
		runtime.status.RemoteProvider = runtime.cfg.Realtime.ProviderID
		runtime.status.RemoteModel = runtime.cfg.Realtime.ModelID
	}
	if diagnostic != "" {
		runtime.status.LastDiagnostic = diagnostic
	}
	runtime.mu.Unlock()
}

func (m *Manager) processRealtimeAudioSegment(parent context.Context, pipelineID string, runtime *sourceRuntime, captured capturedAudioSegment, clip domain.AudioClip) {
	defer m.finishPending(clip.SegmentID)
	if !m.sourceCurrent(runtime.cfg.ID, runtime.epoch) || !m.audioContextCurrent(captured.context) {
		return
	}
	session := m.realtimeSession(runtime)
	appender, ok := session.(providers.RealtimeAudioAppender)
	if !ok {
		setSourceOutcome(runtime, domain.OutcomeUnsupportedModality, fmt.Errorf("realtime audio append unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	err := appender.AppendAudio(ctx, &clip)
	cancel()
	if err != nil {
		if m.recoverRealtimeSession(parent, runtime, err, "audio append may have been accepted before disconnect; segment was not replayed") {
			m.recordRealtimeGap(captured.context, runtime, clip.EndedAt)
			return
		}
		setRealtimeState(runtime, "unavailable", err.Error())
		setSourceOutcome(runtime, providers.OutcomeOf(err), err)
		if runtime.cancel != nil {
			runtime.cancel()
		}
		return
	}
	setRealtimeState(runtime, "alive", "")
	runtime.mu.Lock()
	runtime.status.LastDiagnostic = "audio appended to remote Live session; exact model processing acknowledgement is unavailable"
	runtime.mu.Unlock()

	if runtime.cfg.Realtime.ResponsePolicy == "each_segment" && clip.Final {
		if sourceCooldownActive(runtime, time.Now()) {
			setSourceSuppression(runtime, "cooldown_after_auto_answer")
			return
		}
		text, responseErr := m.realtimeResponse(parent, runtime, false)
		if responseErr != nil {
			if m.recoverRealtimeSession(parent, runtime, responseErr, "delegated response may have started before disconnect; response was not replayed") {
				return
			}
			setSourceOutcome(runtime, providers.OutcomeOf(responseErr), responseErr)
			return
		}
		clearSourceSuppression(runtime)
		m.mu.RLock()
		pipeline := activePipeline(m.cfg)
		m.mu.RUnlock()
		if pipeline != nil && pipeline.SessionBudget != nil && pipeline.SessionBudget.CooldownMS > 0 {
			runtime.mu.Lock()
			runtime.cooldownUntil = time.Now().Add(time.Duration(pipeline.SessionBudget.CooldownMS) * time.Millisecond)
			runtime.mu.Unlock()
		}
		setSourceOutcome(runtime, domain.OutcomeRespond, nil)
		m.publishRealtimeResult(pipelineID, runtime, clip.SegmentID, text, true)
	}
}

func (m *Manager) RequestRealtimeResponse(ctx context.Context, sourceID string) (engine.Result, error) {
	runtime := m.source(sourceID)
	if runtime == nil || runtime.cfg.Realtime == nil || !m.sourceCurrent(sourceID, runtime.epoch) {
		return engine.Result{}, fmt.Errorf("running realtime source %q not found", sourceID)
	}
	text, err := m.realtimeResponse(ctx, runtime, true)
	if err != nil {
		m.recoverRealtimeSession(context.Background(), runtime, err, "manual response may have started before disconnect; command was not replayed")
	}
	trace := domain.ExecutionTrace{
		ID: fmt.Sprintf("realtime-%d", m.eventSeq.Add(1)), PipelineID: runtime.pipelineID,
		PipelineName: runtime.pipelineID, SourceID: runtime.cfg.ID, Intent: string(domain.IntentRespond),
		Purpose: "realtime_command", StartedAt: time.Now(),
	}
	if err != nil {
		trace.Outcome = providers.OutcomeOf(err)
		trace.FinishedAt = time.Now()
		return engine.Result{Trace: trace}, err
	}
	trace.Outcome = domain.OutcomeRespond
	trace.FinishedAt = time.Now()
	result := engine.Result{Text: text, Published: strings.TrimSpace(text) != "", Trace: trace}
	if result.Published {
		m.recordRealtimeAnswer(runtime, result.Text, trace.FinishedAt, trace.ID)
	}
	return result, nil
}

func (m *Manager) realtimeResponse(ctx context.Context, runtime *sourceRuntime, highPriority bool) (string, error) {
	session := m.realtimeSession(runtime)
	if session == nil {
		return "", fmt.Errorf("realtime source is not connected")
	}
	starter, ok := session.(providers.RealtimeResponseStarter)
	if !ok {
		return "", fmt.Errorf("realtime adapter cannot start a bounded response")
	}
	lease, err := m.governor.Acquire(ctx, highPriority)
	if err != nil {
		return "", err
	}
	events, errors, err := starter.StartResponse(ctx)
	if err != nil {
		lease.Release()
		return "", err
	}
	lease.Commit()
	var text strings.Builder
	for events != nil || errors != nil {
		select {
		case event, open := <-events:
			if !open {
				events = nil
				continue
			}
			if event.Type == providers.EventDelta {
				if text.Len()+len(event.Text) > 4<<20 {
					m.holdRealtimeLease(lease, events, errors)
					return "", fmt.Errorf("realtime text exceeded the 4 MiB safety limit")
				}
				text.WriteString(event.Text)
			}
		case responseErr, open := <-errors:
			if !open {
				errors = nil
				continue
			}
			if responseErr != nil {
				lease.Release()
				return "", responseErr
			}
		case <-ctx.Done():
			m.holdRealtimeLease(lease, events, errors)
			return "", ctx.Err()
		}
	}
	lease.Release()
	return strings.TrimSpace(text.String()), nil
}

func (m *Manager) holdRealtimeLease(lease *GovernorLease, events <-chan providers.Event, errors <-chan error) {
	go func() {
		for events != nil || errors != nil {
			select {
			case _, open := <-events:
				if !open {
					events = nil
				}
			case _, open := <-errors:
				if !open {
					errors = nil
				}
			}
		}
		lease.Release()
	}()
}

func (m *Manager) recoverRealtimeSession(parent context.Context, runtime *sourceRuntime, cause error, diagnostic string) bool {
	outcome := providers.OutcomeOf(cause)
	var requestError *providers.RequestError
	if errors.As(cause, &requestError) && !requestError.Retryable {
		return false
	}
	if outcome != domain.OutcomeNetworkError && outcome != domain.OutcomeTimeout && outcome != domain.OutcomeProviderUnavailable {
		return false
	}
	session := m.realtimeSession(runtime)
	if session == nil || !session.Capabilities().Supports(providers.RealtimeReconnect) {
		return false
	}
	reconnector, ok := session.(providers.RealtimeReconnector)
	if !ok {
		return false
	}
	setRealtimeState(runtime, "recovering", diagnostic)
	base := parent
	if base == nil || base.Err() != nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, 5*time.Second)
	err := reconnector.Reconnect(ctx)
	cancel()
	if err != nil {
		setRealtimeState(runtime, "unavailable", "realtime reconnect failed: "+err.Error())
		return false
	}
	runtime.mu.Lock()
	runtime.status.Dropped++
	runtime.status.LastError = ""
	runtime.status.LastOutcome = string(outcome)
	runtime.status.LastRecovery = diagnostic + "; fork reconnect succeeded"
	runtime.status.LastDiagnostic = runtime.status.LastRecovery
	runtime.mu.Unlock()
	setRealtimeState(runtime, "alive", "")
	return true
}

func (m *Manager) recordRealtimeGap(captured audioContext, runtime *sourceRuntime, at time.Time) {
	if captured.config.ID == "" || !m.audioContextCurrent(captured) {
		return
	}
	m.contexts.Append(captured.config, captured.pipelineID, captured.generation, ContextEntry{
		Kind: "gap", Gap: true, At: at, SourceID: runtime.cfg.ID,
	})
}

func (m *Manager) publishRealtimeResult(pipelineID string, runtime *sourceRuntime, eventID, text string, automatic bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	at := time.Now()
	m.recordRealtimeAnswer(runtime, text, at, eventID+"-answer")
	m.mu.RLock()
	publisher := m.publish
	m.mu.RUnlock()
	if publisher != nil && m.sourceCurrent(runtime.cfg.ID, runtime.epoch) {
		publisher(Publication{ID: m.seq.Add(1), PipelineID: pipelineID, SourceID: runtime.cfg.ID, EventID: eventID, Text: text, TriggeredAt: at, At: at, Automatic: automatic})
	}
}

func (m *Manager) recordRealtimeAnswer(runtime *sourceRuntime, text string, at time.Time, id string) {
	if runtime.cfg.ContextID == "" {
		return
	}
	m.mu.RLock()
	pipeline := activePipeline(m.cfg)
	m.mu.RUnlock()
	if pipeline == nil || pipeline.ID != runtime.pipelineID {
		return
	}
	config, ok := findContext(pipeline, runtime.cfg.ContextID)
	if !ok || !config.IncludeAnswers {
		return
	}
	generation := m.contexts.Generation(pipeline.ID, config.ID)
	m.contexts.Append(config, pipeline.ID, generation, ContextEntry{ID: id, Kind: "answer", SourceID: "assistant", Text: text, At: at})
}

func (m *Manager) replaceRealtimeSourcesForContext(pipelineID, contextID string) {
	m.mu.Lock()
	var detached []*sourceRuntime
	for id, runtime := range m.sources {
		if runtime.pipelineID != pipelineID || runtime.cfg.ContextID != contextID || runtime.cfg.Realtime == nil {
			continue
		}
		runtime.cancel()
		delete(m.sources, id)
		detached = append(detached, runtime)
	}
	m.mu.Unlock()
	if len(detached) == 0 {
		return
	}
	go func() {
		for _, runtime := range detached {
			waitForSourceStop(runtime, 2*time.Second)
		}
		m.reconcile()
	}()
}
