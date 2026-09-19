package session

import (
	"context"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
)

func requestedContextIDs(pipeline domain.Pipeline, entryID string, intent domain.CommandIntent, defaultContextID string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, 4)
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		result = append(result, id)
	}
	add(defaultContextID)
	for _, id := range engine.ContextIDsForRoute(pipeline, entryID, intent) {
		add(id)
	}
	return result
}

func contextConfigs(pipeline *domain.Pipeline, ids []string) map[string]domain.ContextConfig {
	result := make(map[string]domain.ContextConfig, len(ids))
	for _, id := range ids {
		if cfg, ok := findContext(pipeline, id); ok {
			result[id] = cfg
		}
	}
	return result
}

func (m *Manager) contextGenerations(pipelineID string, configs map[string]domain.ContextConfig) map[string]uint64 {
	result := make(map[string]uint64, len(configs))
	for id := range configs {
		result[id] = m.contexts.Generation(pipelineID, id)
	}
	return result
}

func (m *Manager) snapshotContextViews(pipelineID string, configs map[string]domain.ContextConfig, generations map[string]uint64, cutoff time.Time) (map[string]engine.ContextView, bool) {
	views := make(map[string]engine.ContextView, len(configs))
	for id, cfg := range configs {
		snapshot := m.contexts.Snapshot(cfg, pipelineID, cutoff)
		if snapshot.Generation != generations[id] {
			return nil, false
		}
		views[id] = engine.ContextView{Text: snapshot.Text, ImageJPEG: append([]byte(nil), snapshot.ImageJPEG...)}
	}
	return views, true
}

func (m *Manager) waitPendingContexts(ctx context.Context, pipelineID string, generations map[string]uint64, cutoff time.Time, limit time.Duration) (bool, error) {
	if len(generations) == 0 || limit <= 0 {
		return true, nil
	}
	deadline, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for {
		m.pendingMu.Lock()
		waiting := false
		for _, pending := range m.pending {
			generation, selected := generations[pending.contextID]
			if selected && pending.pipelineID == pipelineID && pending.generation == generation && !pending.start.After(cutoff) {
				waiting = true
				break
			}
		}
		changed := m.pendingChanged
		m.pendingMu.Unlock()
		if !waiting {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.Done():
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil
		case <-changed:
		}
	}
}

func cloneGenerations(values map[string]uint64) map[string]uint64 {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]uint64, len(values))
	for id, generation := range values {
		result[id] = generation
	}
	return result
}
