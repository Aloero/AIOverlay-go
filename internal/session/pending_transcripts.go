package session

import (
	"context"
	"sync"
	"time"
)

type pendingSegment struct {
	pipelineID, contextID, eventID string
	sourceID                       string
	generation                     uint64
	start, end                     time.Time
}

// pendingTranscripts owns segment membership and wakeups under one mutex.
// A waiter observes both atomically, so completion cannot be lost between them.
type pendingTranscripts struct {
	mu       sync.Mutex
	segments map[string]pendingSegment
	changed  chan struct{}
}

func (pending *pendingTranscripts) notifyLocked() {
	if pending.changed != nil {
		close(pending.changed)
	}
	pending.changed = make(chan struct{})
}

func (pending *pendingTranscripts) add(segment pendingSegment) {
	if segment.eventID == "" || segment.contextID == "" {
		return
	}
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if pending.segments == nil {
		pending.segments = make(map[string]pendingSegment)
	}
	pending.segments[segment.eventID] = segment
	pending.notifyLocked()
}

func (pending *pendingTranscripts) finish(id string) bool {
	if id == "" {
		return false
	}
	pending.mu.Lock()
	defer pending.mu.Unlock()
	_, exists := pending.segments[id]
	if exists {
		delete(pending.segments, id)
		pending.notifyLocked()
	}
	return exists
}

func (pending *pendingTranscripts) removeMatching(match func(pendingSegment) bool) []pendingSegment {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	var removed []pendingSegment
	for id, segment := range pending.segments {
		if match(segment) {
			removed = append(removed, segment)
			delete(pending.segments, id)
		}
	}
	if len(removed) > 0 {
		pending.notifyLocked()
	}
	return removed
}

func (pending *pendingTranscripts) takeSource(sourceID string) []pendingSegment {
	return pending.removeMatching(func(segment pendingSegment) bool { return segment.sourceID == sourceID })
}

func (pending *pendingTranscripts) clearBeforeGeneration(pipelineID, contextID string, current uint64) {
	pending.removeMatching(func(segment pendingSegment) bool {
		return segment.pipelineID == pipelineID && segment.contextID == contextID && segment.generation != current
	})
}

func (pending *pendingTranscripts) waitState(pipelineID string, generations map[string]uint64, cutoff time.Time) (bool, <-chan struct{}) {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	for _, segment := range pending.segments {
		generation, selected := generations[segment.contextID]
		if selected && segment.pipelineID == pipelineID && segment.generation == generation && !segment.start.After(cutoff) {
			return true, pending.changed
		}
	}
	return false, pending.changed
}

func (pending *pendingTranscripts) wait(ctx context.Context, pipelineID string, generations map[string]uint64, cutoff time.Time, limit time.Duration) (bool, error) {
	if len(generations) == 0 || limit <= 0 {
		return true, nil
	}
	deadline, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for {
		waiting, changed := pending.waitState(pipelineID, generations, cutoff)
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
