package session

import (
	"context"
	"sync"
)

type activeBackgroundTurn struct {
	pipelineID         string
	contextGenerations map[string]uint64
	cancel             context.CancelFunc
}

// Cancellation callbacks run after unlocking: a cancelled worker may unregister
// itself immediately. Registry entries own their generation snapshots.
type activeTurnRegistry struct {
	mu    sync.Mutex
	turns map[string]activeBackgroundTurn
}

func (registry *activeTurnRegistry) add(id string, turn activeBackgroundTurn) {
	turn.contextGenerations = cloneGenerations(turn.contextGenerations)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.turns == nil {
		registry.turns = make(map[string]activeBackgroundTurn)
	}
	registry.turns[id] = turn
}

func (registry *activeTurnRegistry) remove(id string) {
	if id == "" {
		return
	}
	registry.mu.Lock()
	delete(registry.turns, id)
	registry.mu.Unlock()
}

func (registry *activeTurnRegistry) cancelStale(pipelineID, contextID string, current uint64) {
	registry.mu.Lock()
	var cancels []context.CancelFunc
	for _, turn := range registry.turns {
		if turn.pipelineID != pipelineID {
			continue
		}
		if generation, selected := turn.contextGenerations[contextID]; selected && generation != current {
			cancels = append(cancels, turn.cancel)
		}
	}
	registry.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}
