package engine

import (
	"aioverlay/internal/catalog"
	"aioverlay/internal/providers"
	"context"
	"errors"
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
	modelInput, outcome, err := r.runMediaPreprocessors(ctx, n, *pool, input)
	if err != nil {
		return nodeValue{}, outcome, err
	}
	gate := &streamGate{runner: r, silent: ctx.Value(silentKey{}) == true}
	ctx = context.WithValue(ctx, streamKey{}, gate)
	ctx = context.WithValue(ctx, planKey{}, newID("plan"))
	models := r.planPool(ctx, n, *pool, modelInput)
	if len(models) == 0 {
		return nodeValue{}, domain.OutcomeModelUnavailable, fmt.Errorf("pool %s has no enabled quality-approved models", n.ID)
	}
	ordered := r.engine.orderPoolModels(r.pipeline.ID+"/"+n.ID, *pool, models)
	switch pool.EffectiveDispatch() {
	case domain.DispatchParallel:
		out, outcome, err := r.racePool(ctx, n, *pool, modelInput, ordered, 0)
		out.image = input.image
		return out, outcome, err
	case domain.DispatchStaggered:
		out, outcome, err := r.racePool(ctx, n, *pool, modelInput, ordered, time.Duration(max(0, pool.HedgeDelayMS))*time.Millisecond)
		out.image = input.image
		return out, outcome, err
	default:
		var lastErr error
		lastOutcome := domain.OutcomeModelUnavailable
		for _, m := range ordered {
			res, outcome, err := r.attemptModel(ctx, n, *pool, modelInput, m)
			if err == nil {
				r.commit(ctx, n, m, res)
				modelInput.text = res
				modelInput.hasOutput = true
				modelInput.image = input.image
				if len(input.image) > 0 && pool.Task != "transcribe" {
					modelInput.imageResolved = true
				}
				if modelInput.vision == "" && len(input.image) > 0 && pool.Task != "transcribe" {
					modelInput.vision = res
				}
				return modelInput, domain.OutcomeSuccess, nil
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

type mediaPreprocessorResult struct {
	kind    string
	value   nodeValue
	outcome domain.Outcome
	err     error
}

func (r *runner) runMediaPreprocessors(ctx context.Context, node domain.Node, pool domain.ModelPoolConfig, input nodeValue) (nodeValue, domain.Outcome, error) {
	branches := 0
	results := make(chan mediaPreprocessorResult, 2)
	launch := func(kind string, preprocessor *domain.ModelPoolConfig, branchInput nodeValue) {
		branches++
		preprocessorNode := domain.Node{
			ID: node.ID + "/" + kind, Type: domain.NodeModelPool, Name: node.Name + " " + kind,
			ContextMode: node.ContextMode, ContextID: node.ContextID, ModelPool: preprocessor,
		}
		go func() {
			value, outcome, err := r.runPool(context.WithValue(ctx, silentKey{}, true), preprocessorNode, branchInput)
			results <- mediaPreprocessorResult{kind: kind, value: value, outcome: outcome, err: err}
		}()
	}

	if pool.VisionPreprocessor != nil {
		if len(input.image) == 0 {
			if !pool.VisionPreprocessorOptional {
				return nodeValue{}, domain.OutcomeUnsupportedModality, &providers.RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "vision preprocessor requires an image"}
			}
		} else {
			visionInput := input
			visionInput.audio = nil
			visionInput.transcript = ""
			launch("vision", pool.VisionPreprocessor, visionInput)
		}
	}
	if pool.AudioPreprocessor != nil {
		if input.audio == nil {
			if !pool.AudioPreprocessorOptional {
				return nodeValue{}, domain.OutcomeUnsupportedModality, &providers.RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "audio preprocessor requires audio"}
			}
		} else {
			audioInput := input
			audioInput.image = nil
			audioInput.vision = ""
			launch("audio", pool.AudioPreprocessor, audioInput)
		}
	}
	if branches == 0 {
		return input, domain.OutcomeSuccess, nil
	}

	modelInput := input
	failures := map[string]mediaPreprocessorResult{}
	for range branches {
		result := <-results
		optional := result.kind == "vision" && pool.VisionPreprocessorOptional || result.kind == "audio" && pool.AudioPreprocessorOptional
		if result.err != nil {
			if optional {
				r.emit(domain.TraceEvent{Kind: "preprocessor_optional_failed", NodeID: node.ID + "/" + result.kind, Outcome: result.outcome, Message: result.err.Error()})
				continue
			}
			failures[result.kind] = result
			continue
		}
		mergeMediaPreprocessorValue(&modelInput, result.kind, result.value)
		if result.kind == "vision" {
			r.observe("vision.text", result.value.text)
		} else if result.kind == "audio" {
			r.observeTranscript(result.value.text)
		}
	}
	for _, kind := range []string{"vision", "audio"} {
		if failure, ok := failures[kind]; ok {
			return nodeValue{}, failure.outcome, failure.err
		}
	}
	return modelInput, domain.OutcomeSuccess, nil
}

func (r *runner) racePool(ctx context.Context, n domain.Node, pool domain.ModelPoolConfig, input nodeValue, models []domain.ModelSlot, delay time.Duration) (nodeValue, domain.Outcome, error) {
	type rr struct {
		slot    domain.ModelSlot
		text    string
		outcome domain.Outcome
		err     error
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
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
		r.emit(domain.TraceEvent{Kind: kind, NodeID: n.ID, PlanID: executionPlanID(ctx), Attempt: &domain.AttemptRef{ProviderID: m.ProviderID, ModelID: m.ModelID}, Message: "Waiting for admission; attempt_start counts the actual model call."})
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
				cancel(errRaceWon)
				input.text = got.text
				input.hasOutput = true
				if len(input.image) > 0 && pool.Task != "transcribe" {
					input.imageResolved = true
				}
				if input.vision == "" && len(input.image) > 0 && pool.Task != "transcribe" {
					input.vision = got.text
				}
				return input, domain.OutcomeSuccess, nil
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
	input = r.contextualizeNodeInput(n, input)
	if pool.Task == "transcribe" {
		input.image = nil
	}
	caps := providers.Media(pc)
	if caps.TranscriptionOnly && pool.Task != "transcribe" {
		return "", domain.OutcomeUnsupportedModality, &providers.RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "Choose a transcription stage for this transport"}
	}
	if input.audio != nil {
		if !caps.Audio {
			if input.audioResolved {
				input.audio = nil
			} else {
				return "", domain.OutcomeUnsupportedModality, &providers.RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "Audio is unsupported by this transport; add a transcription stage"}
			}
		}
		if input.audio != nil && r.engine.catalog != nil {
			ref := catalog.ProviderID(pc) + "/" + strings.TrimPrefix(m.ModelID, catalog.ProviderID(pc)+"/")
			if meta, known := r.engine.catalog.Find(ref); known && !meta.HasAudioInput() {
				if input.audioResolved {
					input.audio = nil
				} else {
					return "", domain.OutcomeUnsupportedModality, &providers.RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "Model does not advertise audio input"}
				}
			}
		}
	} else if pool.Task == "transcribe" {
		return "", domain.OutcomeUnsupportedModality, &providers.RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "No audio to transcribe"}
	}
	if len(input.image) > 0 && r.engine.catalog != nil {
		ref := catalog.ProviderID(pc) + "/" + strings.TrimPrefix(m.ModelID, catalog.ProviderID(pc)+"/")
		if meta, known := r.engine.catalog.Find(ref); known && !meta.HasImageInput() {
			if !input.imageResolved {
				return "", domain.OutcomeUnsupportedModality, &providers.RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "text-only model requires a vision preprocessor"}
			}
			input.image = nil
		}
	}
	keys := r.allowedCredentials(m.ProviderID, r.engine.orderCredentials(m.ProviderID, pc.CredentialPool))
	if pc.Kind == domain.ProviderEmbeddedOpenCode && (pc.OpenCodeProviderID == "" || pc.OpenCodeProviderID == "opencode") && len(keys) == 0 {
		keys = []domain.Credential{{ID: "keyless", Enabled: true}}
	}
	limit := pc.CredentialPool.MaxKeysPerRequest
	if limit <= 0 || limit > len(keys) {
		limit = len(keys)
	}

	var lastErr error = fmt.Errorf("no healthy credentials configured for %s", m.ProviderID)
	lastOutcome := domain.OutcomeAuthFailed
	maxAttempts := m.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	used := 0
	for _, key := range keys {
		if used >= limit {
			break
		}
		admitted := false
		for attemptNo := 0; attemptNo < maxAttempts; attemptNo++ {
			text, outcome, err := r.singleAttempt(ctx, n, pool, input, m, p, key)
			var budget *BudgetError
			if errors.As(err, &budget) && (budget.Scope == "keys" || budget.Scope == "providers") {
				lastErr, lastOutcome = err, outcome
				break
			}
			admitted = true
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
		if admitted {
			used++
		}
	}
	return "", lastOutcome, lastErr
}
