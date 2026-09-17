package session

import (
	"errors"
	"sync"

	paudio "aioverlay/internal/platform/audio"
)

const maxQueuedAudioPCMBytes = 32 << 20

var (
	errAudioQueueCapacity = errors.New("audio queue capacity reached")
	errAudioQueueMemory   = errors.New("audio queue memory limit reached")
)

type audioSegmentQueue struct {
	items    chan paudio.Segment
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
	return &audioSegmentQueue{items: make(chan paudio.Segment, capacity), maxBytes: maxBytes}
}

func (queue *audioSegmentQueue) TryPush(segment paudio.Segment) error {
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

func (queue *audioSegmentQueue) Release(segment paudio.Segment) {
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

func segmentPCMBytes(segment paudio.Segment) int {
	return len(segment.Clip.PCM16) * 2
}
