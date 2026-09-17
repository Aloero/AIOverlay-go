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
