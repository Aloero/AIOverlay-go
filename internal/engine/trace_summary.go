package engine

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	"context"
	"errors"
)

type planKey struct{}

var errRaceWon = errors.New("another model won this pool")
var ErrUserCancelled = errors.New("request cancelled by user")

func executionPlanID(ctx context.Context) string { id, _ := ctx.Value(planKey{}).(string); return id }
func budgetScope(err error) string {
	var b *BudgetError
	if errors.As(err, &b) {
		return b.Scope
	}
	return ""
}
func cancellationReason(ctx context.Context) string {
	switch {
	case errors.Is(context.Cause(ctx), errRaceWon):
		return "race_lost"
	case errors.Is(context.Cause(ctx), ErrUserCancelled):
		return "user"
	default:
		return "cancelled"
	}
}
func finishTrace(t *domain.ExecutionTrace, err error, cause error, node string, outcome domain.Outcome, redact redactor) {
	t.Outcome = domain.OutcomeSuccess
	if err == nil {
		return
	}
	t.Error = redact.text(err.Error())
	t.FailureNodeID = redact.text(node)
	t.Winner = nil
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(cause, context.DeadlineExceeded):
		t.Outcome = domain.OutcomeTimeout
	case errors.Is(err, context.Canceled), errors.Is(cause, ErrUserCancelled):
		t.Outcome = domain.OutcomeCancelled
	case outcome != "" && outcome != domain.OutcomeSuccess:
		t.Outcome = outcome
	default:
		t.Outcome = providers.OutcomeOf(err)
	}
	if t.Outcome == domain.OutcomeCancelled {
		if errors.Is(cause, ErrUserCancelled) {
			t.CancelReason = "user"
		} else if budgetScope(err) == "" {
			t.CancelReason = "cancelled"
		}
	}
}

// Summaries are computed only after attempt workers have joined. One plan ID is
// one pool visit; retries on another key do not count as another model.
func poolCompletionEvents(events []domain.TraceEvent) []domain.TraceEvent {
	type progress struct {
		event domain.TraceEvent
		tried map[string]bool
	}
	all := map[string]*progress{}
	order := []string{}
	for _, ev := range events {
		if ev.PlanID == "" {
			continue
		}
		if ev.Kind == "pool_plan" {
			all[ev.PlanID] = &progress{event: ev, tried: map[string]bool{}}
			order = append(order, ev.PlanID)
		}
		if p := all[ev.PlanID]; p != nil && ev.Kind == "attempt_start" && ev.Attempt != nil {
			a := ev.Attempt
			key := a.SlotID
			if key == "" {
				key = a.ProviderID + "/" + a.ModelID
			}
			p.tried[key] = true
		}
	}
	out := make([]domain.TraceEvent, 0, len(order))
	for _, id := range order {
		p := all[id]
		n := len(p.tried)
		left := p.event.CandidateCount - n
		if left < 0 {
			left = 0
		}
		out = append(out, domain.TraceEvent{Kind: "pool_end", NodeID: p.event.NodeID, PlanID: id, CandidateCount: p.event.CandidateCount, AttemptedModels: n, UntriedModels: left})
	}
	return out
}
