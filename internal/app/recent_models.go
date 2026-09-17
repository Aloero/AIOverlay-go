package app

import (
	"aioverlay/internal/catalog"
	"aioverlay/internal/domain"
	"slices"
	"strings"
)

func mergeRecent(groups ...[]string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, refs := range groups {
		for _, ref := range refs {
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			out = append(out, ref)
			if len(out) >= 30 {
				return out
			}
		}
	}
	return out
}

// Called only after a real adapter attempt. Planning/simulation never changes
// recent usage. References contain no prompt, screenshot or credential material.
func (a *App) recordRecentUsage(t domain.ExecutionTrace) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var refs []string
	by := map[string]domain.ProviderConfig{}
	for _, p := range a.cfg.Providers {
		by[p.ID] = p
	}
	for i := len(t.Events) - 1; i >= 0; i-- {
		ev := t.Events[i]
		if ev.Kind != "attempt_start" || ev.Attempt == nil {
			continue
		}
		p, ok := by[ev.Attempt.ProviderID]
		if !ok {
			continue
		}
		id := catalog.ProviderID(p)
		model := ev.Attempt.ModelID
		if p.Kind == domain.ProviderEmbeddedOpenCode {
			model = strings.TrimPrefix(model, id+"/")
		}
		refs = append(refs, id+"/"+model)
	}
	next := mergeRecent(refs, a.recentModels, a.cfg.ControlCenter.RecentModels)
	if !slices.Equal(next, a.recentModels) {
		a.recentModels = next
		a.recentChanged = true
	}
}
