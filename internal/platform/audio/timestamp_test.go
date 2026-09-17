package audio

import (
	"testing"
	"time"
)

func TestCaptureClockUsesQPCInsteadOfPollingJitter(t *testing.T) {
	var clock captureClock
	observed := time.Date(2026, 9, 17, 20, 0, 0, 0, time.UTC)
	first := clock.PacketStart(10_000_000, 1600, 16000, observed, false)
	if want := observed.Add(-100 * time.Millisecond); !first.Equal(want) {
		t.Fatalf("first=%v want=%v", first, want)
	}

	second := clock.PacketStart(11_000_000, 1600, 16000, observed.Add(180*time.Millisecond), false)
	if want := first.Add(100 * time.Millisecond); !second.Equal(want) {
		t.Fatalf("polling jitter changed capture time: got=%v want=%v", second, want)
	}
}

func TestCaptureClockTimestampErrorUsesFallbackWithoutMovingAnchor(t *testing.T) {
	var clock captureClock
	observed := time.Date(2026, 9, 17, 20, 0, 0, 0, time.UTC)
	first := clock.PacketStart(20_000_000, 1600, 16000, observed, false)

	badObserved := observed.Add(250 * time.Millisecond)
	bad := clock.PacketStart(99_000_000, 1600, 16000, badObserved, true)
	if want := badObserved.Add(-100 * time.Millisecond); !bad.Equal(want) {
		t.Fatalf("timestamp error did not use fallback: got=%v want=%v", bad, want)
	}

	next := clock.PacketStart(22_000_000, 1600, 16000, observed.Add(400*time.Millisecond), false)
	if want := first.Add(200 * time.Millisecond); !next.Equal(want) {
		t.Fatalf("bad timestamp moved QPC anchor: got=%v want=%v", next, want)
	}
}

func TestCaptureClockResetsOnBackwardQPC(t *testing.T) {
	var clock captureClock
	observed := time.Now()
	_ = clock.PacketStart(10_000, 800, 8000, observed, false)
	resetObserved := observed.Add(time.Second)
	got := clock.PacketStart(9_000, 800, 8000, resetObserved, false)
	if want := resetObserved.Add(-100 * time.Millisecond); !got.Equal(want) {
		t.Fatalf("backward QPC did not reset safely: got=%v want=%v", got, want)
	}
}
