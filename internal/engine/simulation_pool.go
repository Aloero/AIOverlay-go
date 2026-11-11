package engine

import (
	"aioverlay/internal/catalog"
	"aioverlay/internal/domain"
	"fmt"
	"sort"
	"strings"
)

type simulatedMediaPreprocessor struct {
	kind     string
	pool     *domain.ModelPoolConfig
	input    nodeValue
	optional bool
}

type simulatedMediaPreprocessorResult struct {
	kind    string
	value   nodeValue
	outcome domain.Outcome
	err     error
	endedAt int64
	until   map[string]int64
}

func (s *simulator) mediaPreprocessors(node domain.Node, pool domain.ModelPoolConfig, input nodeValue) (nodeValue, domain.Outcome, error) {
	branches := make([]simulatedMediaPreprocessor, 0, 2)
	if pool.VisionPreprocessor != nil {
		if len(input.image) == 0 {
			if !pool.VisionPreprocessorOptional {
				return nodeValue{}, domain.OutcomeUnsupportedModality, fmt.Errorf("vision preprocessor requires an image")
			}
		} else {
			branchInput := input
			branchInput.audio = nil
			branchInput.transcript = ""
			branches = append(branches, simulatedMediaPreprocessor{kind: "vision", pool: pool.VisionPreprocessor, input: branchInput, optional: pool.VisionPreprocessorOptional})
		}
	}
	if pool.AudioPreprocessor != nil {
		if input.audio == nil {
			if !pool.AudioPreprocessorOptional {
				return nodeValue{}, domain.OutcomeUnsupportedModality, fmt.Errorf("audio preprocessor requires audio")
			}
		} else {
			branchInput := input
			branchInput.image = nil
			branchInput.vision = ""
			branches = append(branches, simulatedMediaPreprocessor{kind: "audio", pool: pool.AudioPreprocessor, input: branchInput, optional: pool.AudioPreprocessorOptional})
		}
	}
	if len(branches) == 0 {
		return input, domain.OutcomeSuccess, nil
	}

	start := s.now
	initialUntil := cloneSimulationTimes(s.until)
	eventStart := len(s.r.trace.Events)
	results := make([]simulatedMediaPreprocessorResult, 0, len(branches))
	maxEnd := start
	for _, branch := range branches {
		s.now = start
		s.until = cloneSimulationTimes(initialUntil)
		branchNode := domain.Node{
			ID: node.ID + "/" + branch.kind, Type: domain.NodeModelPool, Name: node.Name + " " + branch.kind,
			ContextMode: node.ContextMode, ContextID: node.ContextID, ModelPool: branch.pool,
		}
		value, outcome, branchErr := s.pool(branchNode, branch.input)
		if s.now > maxEnd {
			maxEnd = s.now
		}
		results = append(results, simulatedMediaPreprocessorResult{
			kind: branch.kind, value: value, outcome: outcome, err: branchErr, endedAt: s.now, until: cloneSimulationTimes(s.until),
		})
	}
	s.until = mergeSimulationTimes(initialUntil, results)
	s.now = maxEnd
	sort.SliceStable(s.r.trace.Events[eventStart:], func(i, j int) bool {
		return s.r.trace.Events[eventStart+i].AtMS < s.r.trace.Events[eventStart+j].AtMS
	})

	modelInput := input
	failures := map[string]simulatedMediaPreprocessorResult{}
	for index, result := range results {
		branch := branches[index]
		if result.err != nil {
			if branch.optional {
				s.emit(domain.TraceEvent{Kind: "preprocessor_optional_failed", NodeID: node.ID + "/" + result.kind, Outcome: result.outcome, Message: result.err.Error()})
				continue
			}
			failures[result.kind] = result
			continue
		}
		mergeMediaPreprocessorValue(&modelInput, result.kind, result.value)
	}
	for _, branch := range branches {
		if failure, ok := failures[branch.kind]; ok {
			return nodeValue{}, failure.outcome, failure.err
		}
	}
	return modelInput, domain.OutcomeSuccess, nil
}

func cloneSimulationTimes(source map[string]int64) map[string]int64 {
	copy := make(map[string]int64, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func mergeSimulationTimes(initial map[string]int64, results []simulatedMediaPreprocessorResult) map[string]int64 {
	merged := cloneSimulationTimes(initial)
	for _, result := range results {
		for key, value := range result.until {
			current, exists := merged[key]
			if !exists || value < 0 || current >= 0 && value > current {
				merged[key] = value
			}
		}
	}
	return merged
}

func mergeMediaPreprocessorValue(target *nodeValue, kind string, value nodeValue) {
	switch kind {
	case "vision":
		target.vision = value.text
		target.imageResolved = true
		if target.text == "" {
			target.text = value.text
		}
	case "audio":
		target.transcript = value.text
		target.audioResolved = true
	}
}

func (s *simulator) pool(n domain.Node, input nodeValue) (nodeValue, domain.Outcome, error) {
	p := n.ModelPool
	if p == nil {
		return nodeValue{}, domain.OutcomeInvalidResponse, fmt.Errorf("pool missing")
	}
	modelInput, preprocessorOutcome, err := s.mediaPreprocessors(n, *p, input)
	if err != nil {
		return nodeValue{}, preprocessorOutcome, err
	}
	oldPlan := s.planID
	s.planID = fmt.Sprintf("sim-plan-%d", len(s.r.trace.Events))
	defer func() { s.planID = oldPlan }()
	plan := PlanPool(s.req.Config, s.r.engine.catalog, *p, len(modelInput.image) > 0 && !modelInput.imageResolved)
	models := s.r.engine.orderPoolModels(s.r.pipeline.ID+"/"+n.ID, *p, plan.Candidates)
	s.emit(domain.TraceEvent{Kind: "pool_plan", NodeID: n.ID, CandidateCount: len(models), Message: fmt.Sprintf("source=%s; candidates=%d; order=%s; dispatch=%s", plan.Mode, len(models), p.EffectiveOrder(), p.EffectiveDispatch())})
	reasons := make([]string, 0, len(plan.Skipped))
	for reason := range plan.Skipped {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		count := plan.Skipped[reason]
		s.emit(domain.TraceEvent{Kind: "candidates_skipped", NodeID: n.ID, Message: fmt.Sprintf("%s: %d", reason, count)})
	}
	mode := p.EffectiveDispatch()
	parallel := min(max(1, p.MaxParallel), cap(s.r.sem))
	if mode == domain.DispatchSequential {
		parallel = 1
	}
	stagger := mode == domain.DispatchStaggered && p.HedgeDelayMS > 0
	active := []*simChain{}
	next := 0
	hedgeAt := s.now + int64(p.HedgeDelayMS)
	lastOutcome := domain.OutcomeModelUnavailable
	var lastErr error = fmt.Errorf("no eligible models")
	launch := func() bool {
		if next >= len(models) || s.halted {
			return false
		}
		m := models[next]
		next++
		kind := "candidate_start"
		if stagger && len(active) > 0 {
			kind = "hedge_start"
		}
		s.emit(domain.TraceEvent{Kind: kind, NodeID: n.ID, Attempt: &domain.AttemptRef{ProviderID: m.ProviderID, ModelID: m.ModelID}})
		pc, ok := findProvider(s.req.Config, m.ProviderID)
		if !ok || !pc.Enabled {
			lastOutcome = domain.OutcomeProviderUnavailable
			lastErr = fmt.Errorf("provider is not configured")
			s.emit(domain.TraceEvent{Kind: "candidate_end", NodeID: n.ID, Outcome: lastOutcome, Message: lastErr.Error()})
			return false
		}
		if len(modelInput.image) > 0 && s.r.engine.catalog != nil {
			ref := catalog.ProviderID(pc) + "/" + strings.TrimPrefix(m.ModelID, catalog.ProviderID(pc)+"/")
			if meta, known := s.r.engine.catalog.Find(ref); known && !meta.HasImageInput() && !modelInput.imageResolved {
				lastOutcome = domain.OutcomeUnsupportedModality
				lastErr = fmt.Errorf("text-only model needs vision stage")
				s.emit(domain.TraceEvent{Kind: "candidate_end", NodeID: n.ID, Outcome: lastOutcome, Message: lastErr.Error()})
				return false
			}
		}
		keys := s.r.engine.orderCredentials(m.ProviderID, pc.CredentialPool)
		if len(keys) == 0 {
			if anonymous, ok := pc.AnonymousCredential(); ok {
				keys = []domain.Credential{anonymous}
			}
		}
		maxKeys := pc.CredentialPool.MaxKeysPerRequest
		if maxKeys <= 0 {
			maxKeys = len(keys)
		}
		c := &simChain{model: m, provider: pc, keys: keys, maxKeys: maxKeys}
		if err := s.admit(n, c); err != nil {
			lastErr = err
			if terminalPoolBudget(err) {
				next = len(models)
			}
			lastOutcome = c.outcome
			s.emit(domain.TraceEvent{Kind: "candidate_end", NodeID: n.ID, Outcome: lastOutcome, Message: err.Error()})
			return false
		}
		active = append(active, c)
		return true
	}
	fill := func() {
		for next < len(models) && len(active) < parallel && !s.halted {
			if launch() && stagger {
				break
			}
		}
	}
	fill()
	for len(active) > 0 {
		if err := s.check(); err != nil {
			for _, c := range active {
				s.end(n, c, domain.OutcomeCancelled, "simulation limit")
			}
			return nodeValue{}, domain.OutcomeCancelled, err
		}
		at := int64(s.r.pipeline.Budget.MaxWallTimeMS)
		for _, c := range active {
			at = min(at, c.finish)
			if !c.emittedFirst && c.first > 0 {
				at = min(at, c.first)
			}
		}
		if stagger && next < len(models) && len(active) < parallel && !s.halted {
			at = min(at, hedgeAt)
		}
		if at > s.now {
			s.now = at
		}
		if s.now >= int64(s.r.pipeline.Budget.MaxWallTimeMS) {
			for _, c := range active {
				s.end(n, c, domain.OutcomeTimeout, "max wall time")
			}
			return nodeValue{}, domain.OutcomeTimeout, fmt.Errorf("max wall time reached")
		}
		// Stable candidate order defines ties in a virtual rehearsal, never a speed prediction.
		for _, c := range active {
			if !c.emittedFirst && c.first > 0 && c.first <= s.now {
				c.emittedFirst = true
				s.emit(domain.TraceEvent{Kind: "first_token", NodeID: n.ID, Attempt: c.ref, TTFTMS: c.first - c.start})
			}
		}
		slotFreed := false
		for i := 0; i < len(active); {
			c := active[i]
			if c.finish > s.now {
				i++
				continue
			}
			s.end(n, c, c.outcome, "conditional outcome")
			if c.outcome == domain.OutcomeSuccess {
				s.r.trace.Winner = c.ref
				s.emit(domain.TraceEvent{Kind: "winner", NodeID: n.ID, Attempt: c.ref, Outcome: domain.OutcomeSuccess, Message: "Conditional answer accepted"})
				for _, loser := range active {
					if loser != c {
						s.end(n, loser, domain.OutcomeCancelled, "race_lost")
					}
				}
				s.report.Remaining += len(models) - next
				modelInput.text = c.text
				modelInput.hasOutput = true
				modelInput.image = input.image
				if len(input.image) > 0 && p.Task != "transcribe" {
					modelInput.imageResolved = true
				}
				if modelInput.vision == "" && len(input.image) > 0 && p.Task != "transcribe" {
					modelInput.vision = c.text
				}
				return modelInput, domain.OutcomeSuccess, nil
			}
			lastOutcome = c.outcome
			lastErr = fmt.Errorf("conditional %s", c.outcome)
			s.failedKey(c)
			if s.advanceKey(c) {
				if err := s.admit(n, c); err == nil {
					i++
					continue
				} else {
					lastErr = err
					if terminalPoolBudget(err) {
						next = len(models)
					}
					lastOutcome = c.outcome
				}
			}
			active = append(active[:i], active[i+1:]...)
			slotFreed = true
		}
		if len(active) == 0 || !stagger {
			fill()
			hedgeAt = s.now + int64(p.HedgeDelayMS)
		} else if next < len(models) && len(active) < parallel && !s.halted {
			// Just as execution, an error can fill a released slot before the next hedge.

			if slotFreed || s.now >= hedgeAt {
				fill()
				hedgeAt = s.now + int64(p.HedgeDelayMS)
			}
		}
	}
	return nodeValue{}, lastOutcome, lastErr
}
