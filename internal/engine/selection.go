package engine

import (
	"fmt"
	"sort"

	"aioverlay/internal/catalog"
	"aioverlay/internal/domain"
)

// PoolPlan is also used by Studio. Planning never rotates cursors or sends requests.
type PoolPlan struct {
	Candidates []domain.ModelSlot `json:"candidates"`
	Skipped    map[string]int     `json:"skipped"`
	Mode       string             `json:"mode"`
}

func PlanPool(cfg domain.Config, cat *catalog.Service, pool domain.ModelPoolConfig, imageInput bool) PoolPlan {
	plan := PoolPlan{Candidates: []domain.ModelSlot{}, Skipped: map[string]int{}, Mode: "manual"}
	if pool.Selection != nil && pool.Selection.Mode != "" {
		plan.Mode = pool.Selection.Mode
	}
	slotRef := func(m domain.ModelSlot) string { return m.ProviderID + "/" + m.ModelID }
	overrides := map[string]domain.ModelSlot{}
	seen := map[string]bool{}
	rank := map[string]int{}
	if pool.Selection != nil {
		for i, id := range pool.Selection.ProviderIDs {
			rank[id] = i
		}
	}
	for _, m := range pool.Models {
		overrides[slotRef(m)] = m
	}
	accept := func(m domain.ModelSlot) bool {
		if !m.Enabled {
			plan.Skipped["disabled"]++
			return false
		}
		if m.QualityScore < pool.QualityFloor {
			plan.Skipped["quality_floor"]++
			return false
		}
		return true
	}
	// Explicit slots remain valid even when absent from Models.dev. Known
	// incompatible modalities are rejected in attemptModel before network admission.
	if plan.Mode != "catalog" {
		for _, m := range pool.Models {
			seen[slotRef(m)] = true
			if accept(m) {
				plan.Candidates = append(plan.Candidates, m)
			}
		}
	}
	manualEnd := len(plan.Candidates)
	if plan.Mode != "manual" && cat != nil {
		for _, route := range cat.Routes(cfg) {
			if len(rank) > 0 {
				if _, ok := rank[route.ConfigID]; !ok {
					continue
				}
			}
			key := route.ConfigID + "/" + route.ModelID
			if seen[key] {
				continue
			}
			seen[key] = true
			if !route.Ready {
				plan.Skipped["not_configured"]++
				continue
			}
			if pool.Selection.FreeOnly && !route.Model.Free {
				plan.Skipped["not_free"]++
				continue
			}
			if imageInput && pool.Selection.Modality == "text" {
				plan.Skipped["needs_vision"]++
				continue
			}
			needsImage := imageInput || pool.Selection.Modality == "image"
			if needsImage && !route.Model.HasImageInput() {
				plan.Skipped["needs_vision"]++
				continue
			}
			if !needsImage && pool.Selection.Modality == "text" && route.Model.HasImageInput() {
				plan.Skipped["text_only_filter"]++
				continue
			}
			hasTextOutput := false
			for _, mod := range route.Model.OutputModalities {
				if mod == "text" {
					hasTextOutput = true
				}
			}
			if !hasTextOutput {
				plan.Skipped["non_text_output"]++
				continue
			}
			m := domain.ModelSlot{ID: "catalog:" + key, ProviderID: route.ConfigID, ModelID: route.ModelID, Enabled: true, Weight: 1, QualityScore: pool.Selection.DefaultQualityScore, MaxAttempts: 1}
			if manual, ok := overrides[key]; ok {
				m = manual
			}
			if accept(m) {
				plan.Candidates = append(plan.Candidates, m)
			}
		}
	}
	// Priority ties: quality, configured provider order, then a stable model ID.
	less := func(a, b domain.ModelSlot) bool {
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if a.QualityScore != b.QualityScore {
			return a.QualityScore > b.QualityScore
		}
		if rank[a.ProviderID] != rank[b.ProviderID] {
			return rank[a.ProviderID] < rank[b.ProviderID]
		}
		return slotRef(a) < slotRef(b)
	}
	if plan.Mode == "catalog" {
		sort.SliceStable(plan.Candidates, func(i, j int) bool { return less(plan.Candidates[i], plan.Candidates[j]) })
	}
	if plan.Mode == "manual_then_catalog" {
		tail := plan.Candidates[manualEnd:]
		sort.SliceStable(tail, func(i, j int) bool { return less(tail[i], tail[j]) })
		// Dispatch ordering uses priority. Keep catalog fallbacks below every explicit slot.
		minPriority := 0
		for _, m := range plan.Candidates[:manualEnd] {
			if m.Priority < minPriority {
				minPriority = m.Priority
			}
		}
		for i := manualEnd; i < len(plan.Candidates); i++ {
			plan.Candidates[i].Priority = minPriority - 1
		}
	}
	return plan
}

func (r *runner) planPool(n domain.Node, pool domain.ModelPoolConfig, input nodeValue) []domain.ModelSlot {
	plan := PlanPool(r.engine.cfg, r.engine.catalog, pool, len(input.image) > 0)
	r.emit(domain.TraceEvent{Kind: "pool_plan", NodeID: n.ID, Message: fmt.Sprintf("source=%s; candidates=%d; policy=%s; parallel<=%d; calls<=%d; hedge=%dms", plan.Mode, len(plan.Candidates), pool.Policy, min(max(1, pool.MaxParallel), cap(r.sem)), pool.MaxCalls, pool.HedgeDelayMS)})
	reasons := make([]string, 0, len(plan.Skipped))
	for reason := range plan.Skipped {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		r.emit(domain.TraceEvent{Kind: "candidates_skipped", NodeID: n.ID, Message: fmt.Sprintf("%s: %d", reason, plan.Skipped[reason])})
	}
	return plan.Candidates
}

func (e *Engine) orderPoolModels(key string, pool domain.ModelPoolConfig, models []domain.ModelSlot) []domain.ModelSlot {
	if pool.Selection == nil || pool.Selection.Mode != "manual_then_catalog" {
		return e.orderModelsForPolicy(key, pool.Policy, models)
	}
	pins := map[string]bool{}
	for _, m := range pool.Models {
		pins[m.ProviderID+"/"+m.ModelID] = true
	}
	var first, rest []domain.ModelSlot
	for _, m := range models {
		if pins[m.ProviderID+"/"+m.ModelID] {
			first = append(first, m)
		} else {
			rest = append(rest, m)
		}
	}
	first = e.orderModelsForPolicy(key, pool.Policy, first)
	return append(first, rest...)
}
