package engine

import (
	"context"
	"fmt"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

// modelAttempt coordinates one admitted call. Stream parsing and resource
// ownership live separately so neither depends on graph routing or retry policy.
type modelAttempt struct {
	runner  *runner
	ctx     context.Context
	node    domain.Node
	model   domain.ModelSlot
	ref     *domain.AttemptRef
	started time.Time
	gate    *streamGate
	success bool
}

func (r *runner) singleAttempt(ctx context.Context, n domain.Node, pool domain.ModelPoolConfig, input nodeValue, m domain.ModelSlot, p providers.Provider, key domain.Credential) (string, domain.Outcome, error) {
	resources, err := acquireAttemptResources(ctx, r.sem, r.opt.BeforeCall)
	if err != nil {
		return "", providers.OutcomeOf(err), err
	}
	defer resources.release()
	if err := r.admit(n, pool, m, key); err != nil {
		r.emitAttempt(ctx, domain.TraceEvent{Kind: "budget_exhausted", NodeID: n.ID, BudgetScope: budgetScope(err), Message: err.Error()})
		return "", domain.OutcomeCancelled, err
	}
	resources.commit()
	attempt := r.newModelAttempt(ctx, n, m, key)
	defer attempt.releaseStreamOwnership()
	return attempt.run(pool, input, p, key.Secret, resources)
}

func (r *runner) newModelAttempt(ctx context.Context, node domain.Node, model domain.ModelSlot, key domain.Credential) *modelAttempt {
	started := time.Now()
	ref := &domain.AttemptRef{
		ID: newID("attempt"), SlotID: model.ID, ProviderID: model.ProviderID,
		ModelID: model.ModelID, CredentialID: key.ID, CredentialAlias: key.Label, Transport: "Direct",
	}
	// The label is a fact about the kind, not a scheduler branch, so the trace stays honest when a
	// new transport is added: one policy row, nothing to remember here.
	if provider, ok := findProvider(r.engine.cfg, model.ProviderID); ok {
		if label := domain.Policy(provider.Kind).TransportLabel; label != "" {
			ref.Transport = label
		}
	}
	if ref.CredentialAlias == "" {
		ref.CredentialAlias = key.ID
	}
	gate, _ := ctx.Value(streamKey{}).(*streamGate)
	return &modelAttempt{runner: r, ctx: ctx, node: node, model: model, ref: ref, started: started, gate: gate}
}

func (a *modelAttempt) run(pool domain.ModelPoolConfig, input nodeValue, provider providers.Provider, credential string, resources *attemptResources) (string, domain.Outcome, error) {
	a.emit(domain.TraceEvent{Kind: "attempt_start", Message: fmt.Sprintf("priority=%d", a.model.Priority)})
	limits := a.resolveLimits()
	ctx, cancel := context.WithTimeout(a.ctx, durationMS(limits.ResponseMS))
	defer cancel()
	req, values := a.request(pool, input, limits)
	a.publishPrompt(req, values)
	events, errs := provider.Stream(ctx, req, credential)
	stream := &attemptStream{
		events: events, errors: errs, request: req, started: a.started,
		emit: a.emit, push: a.pushDelta,
	}
	if pc, ok := findProvider(a.runner.engine.cfg, a.model.ProviderID); ok {
		stream.buffered = !providers.StreamsTokens(pc, provider)
	}
	defer a.cleanupProvider(cancel, resources, stream)
	if outcome, err := stream.consume(ctx, a.ctx, cancel); err != nil {
		return a.fail(outcome, err, stream.ttft)
	}
	if err := validateAttemptResult(a.node, pool, stream.text.String()); err != nil {
		return a.fail(domain.OutcomeInvalidResponse, err, 0)
	}
	return a.succeed(stream)
}

func (r *runner) emitAttempt(ctx context.Context, event domain.TraceEvent) {
	event.PlanID = executionPlanID(ctx)
	if event.Outcome == domain.OutcomeCancelled {
		event.CancelReason = cancellationReason(ctx)
	}
	r.emit(event)
}

func (a *modelAttempt) emit(event domain.TraceEvent) {
	event.NodeID = a.node.ID
	event.Attempt = a.ref
	a.runner.emitAttempt(a.ctx, event)
}

func (a *modelAttempt) pushDelta(text string) {
	if a.gate != nil {
		a.gate.push(a.ref.ID, text)
	}
}

func (a *modelAttempt) releaseStreamOwnership() {
	if !a.success && a.gate != nil {
		a.gate.release(a.ref.ID)
	}
}
