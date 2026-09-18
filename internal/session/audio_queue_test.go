package session

import (
	"errors"
	"testing"

	"aioverlay/internal/domain"
	paudio "aioverlay/internal/platform/audio"
)

func audioQueueSegment(samples int) capturedAudioSegment {
	return capturedAudioSegment{segment: paudio.Segment{Clip: domain.AudioClip{PCM16: make([]int16, samples)}}}
}

func TestAudioSegmentQueueBoundsPCMBytesIndependentlyOfCount(t *testing.T) {
	queue := newAudioSegmentQueue(10, 10)
	first := audioQueueSegment(4) // 8 bytes
	second := audioQueueSegment(2)
	if err := queue.TryPush(first); err != nil {
		t.Fatal(err)
	}
	if err := queue.TryPush(second); !errors.Is(err, errAudioQueueMemory) {
		t.Fatalf("second push=%v", err)
	}
	if queue.QueuedBytes() != 8 {
		t.Fatalf("queued bytes=%d", queue.QueuedBytes())
	}

	received := <-queue.items
	queue.Release(received)
	if queue.QueuedBytes() != 0 {
		t.Fatalf("released bytes=%d", queue.QueuedBytes())
	}
	if err := queue.TryPush(second); err != nil {
		t.Fatalf("queue did not recover after release: %v", err)
	}
}

func TestAudioSegmentQueueStillHonorsConfiguredCount(t *testing.T) {
	queue := newAudioSegmentQueue(1, 1024)
	if err := queue.TryPush(audioQueueSegment(2)); err != nil {
		t.Fatal(err)
	}
	if err := queue.TryPush(audioQueueSegment(2)); !errors.Is(err, errAudioQueueCapacity) {
		t.Fatalf("count limit=%v", err)
	}
	if queue.QueuedBytes() != 4 {
		t.Fatalf("failed push leaked byte reservation: %d", queue.QueuedBytes())
	}
}

func TestAudioSegmentQueueRejectsSingleOversizedSegment(t *testing.T) {
	queue := newAudioSegmentQueue(4, 4)
	if err := queue.TryPush(audioQueueSegment(3)); !errors.Is(err, errAudioQueueMemory) {
		t.Fatalf("oversized segment=%v", err)
	}
	if queue.QueuedBytes() != 0 {
		t.Fatalf("oversized segment reserved bytes=%d", queue.QueuedBytes())
	}
}
