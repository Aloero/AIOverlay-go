package engine

import (
	"fmt"
	"strings"
	"time"

	"aioverlay/internal/domain"
)

func allowsEmptyModelResult(pool domain.ModelPoolConfig) bool {
	return pool.Task == "transcribe" || pool.ResultName == "audio.text"
}

func validateAttemptResult(node domain.Node, pool domain.ModelPoolConfig, text string) error {
	if strings.TrimSpace(text) == "" && !allowsEmptyModelResult(pool) {
		return fmt.Errorf("empty response")
	}
	if node.Type == domain.NodeDecision {
		_, err := parseDecision(text)
		return err
	}
	return nil
}

func (a *modelAttempt) fail(outcome domain.Outcome, err error, ttft int64) (string, domain.Outcome, error) {
	duration := time.Since(a.started)
	a.emit(domain.TraceEvent{
		Kind: "attempt_end", Outcome: outcome, Message: err.Error(),
		TTFTMS: ttft, DurationMS: duration.Milliseconds(),
	})
	a.runner.engine.recordModel(a.model, outcome, time.Duration(ttft)*time.Millisecond, duration)
	return "", outcome, err
}

func (a *modelAttempt) succeed(stream *attemptStream) (string, domain.Outcome, error) {
	duration := time.Since(a.started)
	ttft, tps := stream.successMetrics(duration)
	a.emit(domain.TraceEvent{
		Kind: "attempt_end", Outcome: domain.OutcomeSuccess,
		TTFTMS: ttft, DurationMS: duration.Milliseconds(), OutputTokens: stream.outputTokens, TPS: tps,
	})
	r := a.runner
	r.engine.recordModel(a.model, domain.OutcomeSuccess, time.Duration(ttft)*time.Millisecond, duration)
	a.success = true
	r.traceMu.Lock()
	r.successes[a.node.ID+"/"+a.model.ID] = a.ref
	r.traceMu.Unlock()
	return r.redact.text(strings.TrimSpace(stream.text.String())), domain.OutcomeSuccess, nil
}
