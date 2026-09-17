package engine

import (
	"aioverlay/internal/catalog"
	"aioverlay/internal/providers"
	"context"
	"fmt"
	"strings"
	"time"

	"aioverlay/internal/domain"
)

func (r *runner) runPool(ctx context.Context, n domain.Node, input nodeValue) (nodeValue, domain.Outcome, error) {
	pool := n.ModelPool
	if pool == nil {
		return nodeValue{}, domain.OutcomeInvalidResponse, fmt.Errorf("pool missing")
	}
	modelInput := input
	// VLM -> LLM composite: a configured vision preprocessor is executed first and its output becomes text input.
	if pool.VisionPreprocessor != nil {
		visionNode := domain.Node{ID: n.ID + "/vision", Type: domain.NodeModelPool, Name: n.Name + " Vision", ModelPool: pool.VisionPreprocessor}
		v, outcome, err := r.runPool(context.WithValue(ctx, silentKey{}, true), visionNode, input)
		if err != nil {
			return nodeValue{}, outcome, err
		}
		modelInput = nodeValue{text: v.text}
	}
	gate := &streamGate{runner: r, silent: ctx.Value(silentKey{}) == true}
	ctx = context.WithValue(ctx, streamKey{}, gate)
	models := r.planPool(n, *pool, modelInput)
	if len(models) == 0 {
		return nodeValue{}, domain.OutcomeModelUnavailable, fmt.Errorf("pool %s has no enabled quality-approved models", n.ID)
	}
	ordered := r.engine.orderPoolModels(r.pipeline.ID+"/"+n.ID, *pool, models)
	switch pool.Policy {
	case domain.PoolParallel:
		return r.racePool(ctx, n, *pool, modelInput, ordered, 0)
	case domain.PoolStaggered:
		return r.racePool(ctx, n, *pool, modelInput, ordered, time.Duration(max(0, pool.HedgeDelayMS))*time.Millisecond)
	default:
		var lastErr error
		lastOutcome := domain.OutcomeModelUnavailable
		for _, m := range ordered {
			res, outcome, err := r.attemptModel(ctx, n, *pool, modelInput, m)
			if err == nil {
				r.commit(ctx, n, m, res)
				return nodeValue{text: res}, domain.OutcomeSuccess, nil
			}
			lastErr = err
			lastOutcome = outcome
			if terminalPoolBudget(err) || ctx.Err() != nil {
				break
			}
		}
		return nodeValue{}, lastOutcome, lastErr
	}
}

func (r *runner) racePool(ctx context.Context, n domain.Node, pool domain.ModelPoolConfig, input nodeValue, models []domain.ModelSlot, delay time.Duration) (nodeValue, domain.Outcome, error) {
	type rr struct {
		slot    domain.ModelSlot
		text    string
		outcome domain.Outcome
		err     error
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan rr, len(models))
	maxParallel := min(pool.MaxParallel, cap(r.sem))
	if maxParallel <= 0 {
		maxParallel = 1
	}
	if maxParallel > len(models) {
		maxParallel = len(models)
	}
	var active int
	next := 0
	launch := func(m domain.ModelSlot) {
		kind := "candidate_start"
		if delay > 0 && active > 0 {
			kind = "hedge_start"
		}
		r.emit(domain.TraceEvent{Kind: kind, NodeID: n.ID, Attempt: &domain.AttemptRef{ProviderID: m.ProviderID, ModelID: m.ModelID}, Message: "Waiting for admission; attempt_start counts the actual model call."})
		active++
		r.workers.Add(1)
		go func() {
			defer r.workers.Done()
			txt, o, err := r.attemptModel(ctx, n, pool, input, m)
			ch <- rr{slot: m, text: txt, outcome: o, err: err}
		}()
	}
	if delay == 0 {
		for next < len(models) && active < maxParallel {
			launch(models[next])
			next++
		}
	} else {
		launch(models[0])
		next = 1
	}
	var timer *time.Timer
	if delay > 0 && next < len(models) {
		timer = time.NewTimer(delay)
		defer timer.Stop()
	}
	var lastErr error
	lastOutcome := domain.OutcomeModelUnavailable
	for active > 0 {
		var timerC <-chan time.Time
		if timer != nil {
			timerC = timer.C
		}
		select {
		case got := <-ch:
			active--
			if got.err == nil {
				r.commit(ctx, n, got.slot, got.text)
				cancel()
				return nodeValue{text: got.text}, domain.OutcomeSuccess, nil
			}
			lastErr = got.err
			lastOutcome = got.outcome
			if terminalPoolBudget(got.err) {
				next = len(models)
			}
			if next < len(models) && active < maxParallel {
				launch(models[next])
				next++
				if delay > 0 && next < len(models) {
					timer.Reset(delay)
				}
			}
		case <-timerC:
			if next < len(models) && active < maxParallel {
				launch(models[next])
				next++
			}
			if next < len(models) {
				timer.Reset(delay)
			} else {
				timer = nil
			}
		case <-ctx.Done():
			return nodeValue{}, domain.OutcomeCancelled, ctx.Err()
		}
	}
	return nodeValue{}, lastOutcome, lastErr
}

func (r *runner) attemptModel(ctx context.Context, n domain.Node, pool domain.ModelPoolConfig, input nodeValue, m domain.ModelSlot) (text string, outcome domain.Outcome, err error) {
	defer func() {
		if err != nil {
			r.emit(domain.TraceEvent{Kind: "candidate_end", NodeID: n.ID, Attempt: &domain.AttemptRef{ProviderID: m.ProviderID, ModelID: m.ModelID}, Outcome: outcome, Message: err.Error()})
		}
	}()
	if ctx.Err() != nil {
		return "", providers.OutcomeOf(ctx.Err()), ctx.Err()
	}
	p, ok := r.engine.registry.Get(m.ProviderID)
	if !ok {
		return "", domain.OutcomeProviderUnavailable, fmt.Errorf("provider %s not registered", m.ProviderID)
	}
	pc, ok := findProvider(r.engine.cfg, m.ProviderID)
	if !ok {
		return "", domain.OutcomeProviderUnavailable, fmt.Errorf("provider config missing")
	}
	if len(input.image) > 0 && r.engine.catalog != nil {
		ref := catalog.ProviderID(pc) + "/" + strings.TrimPrefix(m.ModelID, catalog.ProviderID(pc)+"/")
		if meta, known := r.engine.catalog.Find(ref); known && !meta.HasImageInput() {
			return "", domain.OutcomeUnsupportedModality, &providers.RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "text-only model requires a vision preprocessor"}
		}
	}
	keys := r.engine.orderCredentials(m.ProviderID, pc.CredentialPool)
	if pc.Kind == domain.ProviderEmbeddedOpenCode && (pc.OpenCodeProviderID == "" || pc.OpenCodeProviderID == "opencode") && len(keys) == 0 {
		keys = []domain.Credential{{ID: "keyless", Enabled: true}}
	}
	limit := pc.CredentialPool.MaxKeysPerRequest
	if r.pipeline.Budget.MaxKeysPerProvider > 0 && (limit <= 0 || r.pipeline.Budget.MaxKeysPerProvider < limit) {
		limit = r.pipeline.Budget.MaxKeysPerProvider
	}
	if limit <= 0 || limit > len(keys) {
		limit = len(keys)
	}
	var lastErr error = fmt.Errorf("no healthy credentials configured for %s", m.ProviderID)
	lastOutcome := domain.OutcomeAuthFailed
	maxAttempts := m.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	for i := 0; i < limit; i++ {
		key := keys[i]
		for attemptNo := 0; attemptNo < maxAttempts; attemptNo++ {
			text, outcome, err := r.singleAttempt(ctx, n, pool, input, m, p, key)
			r.engine.markCredential(m.ProviderID, key, outcome, pc.CredentialPool.RateLimitCooldownS)
			if err == nil {
				return text, domain.OutcomeSuccess, nil
			}
			lastErr, lastOutcome = err, outcome
			if terminalPoolBudget(err) || ctx.Err() != nil {
				return "", outcome, err
			}
			if outcome == domain.OutcomeAuthFailed || outcome == domain.OutcomeRateLimited || outcome == domain.OutcomeQuotaExceeded {
				break
			}
			if outcome != domain.OutcomeTimeout && outcome != domain.OutcomeNetworkError && outcome != domain.OutcomeProviderUnavailable {
				break
			}
		}
	}
	return "", lastOutcome, lastErr
}
