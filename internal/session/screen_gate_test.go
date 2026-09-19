package session

import (
	"aioverlay/internal/domain"
	"testing"
	"time"
)

func TestScreenChangeGateDebouncesStableChange(t *testing.T) {
	gate := newScreenChangeGate(domain.EventSource{ChangedOnly: true, DebounceMS: 500, MaxWaitMS: 5000})
	start := time.Now()

	if !gate.Accept([]byte("first"), start) {
		t.Fatal("first frame should establish initial context")
	}
	if gate.Accept([]byte("second"), start.Add(time.Second)) {
		t.Fatal("new frame should wait for debounce")
	}
	if gate.Accept([]byte("second"), start.Add(1200*time.Millisecond)) {
		t.Fatal("candidate was published before debounce elapsed")
	}
	if !gate.Accept([]byte("second"), start.Add(1600*time.Millisecond)) {
		t.Fatal("stable changed frame was not published")
	}
}

func TestScreenChangeGateCancelsChangeThatReverts(t *testing.T) {
	gate := newScreenChangeGate(domain.EventSource{ChangedOnly: true, DebounceMS: 500, MaxWaitMS: 5000})
	start := time.Now()
	gate.Accept([]byte("stable"), start)
	gate.Accept([]byte("noise"), start.Add(time.Second))

	if gate.Accept([]byte("stable"), start.Add(1200*time.Millisecond)) {
		t.Fatal("reverted frame should not publish a change")
	}
	if gate.hasCandidate || !gate.changeAt.IsZero() {
		t.Fatal("reverted frame did not clear pending change")
	}
}

func TestScreenChangeGatePublishesMovingScreenAtDeadline(t *testing.T) {
	gate := newScreenChangeGate(domain.EventSource{ChangedOnly: true, DebounceMS: 1000, MaxWaitMS: 2500})
	start := time.Now()
	gate.Accept([]byte("frame-0"), start)

	for i, frame := range [][]byte{[]byte("frame-1"), []byte("frame-2"), []byte("frame-3")} {
		if gate.Accept(frame, start.Add(time.Duration(i+1)*700*time.Millisecond)) {
			t.Fatalf("moving frame %d published before deadline", i+1)
		}
	}
	if !gate.Accept([]byte("frame-4"), start.Add(3600*time.Millisecond)) {
		t.Fatal("continuously changing screen waited forever")
	}
}

func TestScreenChangeGateWithoutChangedOnlyPublishesEveryFrame(t *testing.T) {
	gate := newScreenChangeGate(domain.EventSource{})
	start := time.Now()
	if !gate.Accept([]byte("same"), start) || !gate.Accept([]byte("same"), start.Add(time.Second)) {
		t.Fatal("periodic source unexpectedly suppressed a frame")
	}
}

func TestScreenDeliveryClockSeparatesContextAndAIIntervals(t *testing.T) {
	clock := newScreenDeliveryClock(domain.EventSource{IntervalMS: 1000, ContextIntervalMS: 2000, TriggerIntervalMS: 5000})
	start := time.Unix(100, 0)
	first, ok := clock.Observe([]byte("one"), start, true, true, true)
	if !ok || !first.contextDue || !first.triggerDue {
		t.Fatalf("first delivery=%+v ok=%v", first, ok)
	}
	if _, ok := clock.Observe([]byte("two"), start.Add(time.Second), true, true, true); ok {
		t.Fatal("throttled change delivered too early")
	}
	contextOnly, ok := clock.Observe([]byte("latest"), start.Add(2*time.Second), false, true, true)
	if !ok || !contextOnly.contextDue || contextOnly.triggerDue || string(contextOnly.image) != "latest" {
		t.Fatalf("context cadence=%+v ok=%v", contextOnly, ok)
	}
	trigger, ok := clock.Observe([]byte("newest"), start.Add(5*time.Second), false, true, true)
	if !ok || trigger.contextDue || !trigger.triggerDue || string(trigger.image) != "newest" {
		t.Fatalf("trigger cadence=%+v ok=%v", trigger, ok)
	}
}
