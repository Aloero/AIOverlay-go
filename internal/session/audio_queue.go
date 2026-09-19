package session

import (
	"errors"
	"sync"

	"aioverlay/internal/domain"
	paudio "aioverlay/internal/platform/audio"
)

const maxQueuedAudioPCMBytes = 32 << 20

var (
	errAudioQueueCapacity = errors.New("audio queue capacity reached")
	errAudioQueueMemory   = errors.New("audio queue memory limit reached")
)

// The context generation travels with PCM from capture, not from dequeue time.
type capturedAudioSegment struct {
	segment paudio.Segment
	context audioContext
	eventID string
}

type audioContext struct {
	pipelineID string
	config     domain.ContextConfig
	generation uint64
}

type audioSegmentQueue struct {
	items    chan capturedAudioSegment
	maxBytes int

	mu    sync.Mutex
	bytes int
}

func newAudioSegmentQueue(capacity, maxBytes int) *audioSegmentQueue {
	if capacity < 1 {
		capacity = 1
	}
	if maxBytes < 1 {
		maxBytes = maxQueuedAudioPCMBytes
	}
	return &audioSegmentQueue{items: make(chan capturedAudioSegment, capacity), maxBytes: maxBytes}
}

func (queue *audioSegmentQueue) TryPush(segment capturedAudioSegment) error {
	bytes := segmentPCMBytes(segment)
	queue.mu.Lock()
	if bytes > queue.maxBytes-queue.bytes {
		queue.mu.Unlock()
		return errAudioQueueMemory
	}
	queue.bytes += bytes
	queue.mu.Unlock()

	select {
	case queue.items <- segment:
		return nil
	default:
		queue.releaseBytes(bytes)
		return errAudioQueueCapacity
	}
}

func (queue *audioSegmentQueue) Release(segment capturedAudioSegment) {
	queue.releaseBytes(segmentPCMBytes(segment))
}

func (queue *audioSegmentQueue) Close() {
	close(queue.items)
}

func (queue *audioSegmentQueue) QueuedBytes() int {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return queue.bytes
}

func (queue *audioSegmentQueue) releaseBytes(bytes int) {
	queue.mu.Lock()
	queue.bytes -= bytes
	if queue.bytes < 0 {
		queue.bytes = 0
	}
	queue.mu.Unlock()
}

func segmentPCMBytes(segment capturedAudioSegment) int {
	return len(segment.segment.Clip.PCM16) * 2
}
