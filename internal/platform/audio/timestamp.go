package audio

import "time"

const qpc100ns = 100 * time.Nanosecond

type captureClock struct {
	anchored bool
	qpc      uint64
	at       time.Time
}

// PacketStart maps WASAPI's 100ns QPC timestamp for the first frame onto the
// process wall clock. The first packet provides the wall-clock anchor; later
// packets keep WASAPI timing even when polling itself jitters.
func (clock *captureClock) PacketStart(qpc uint64, frames, rate int, observed time.Time, timestampError bool) time.Time {
	fallback := observed.Add(-packetDuration(frames, rate))
	if timestampError || qpc == 0 {
		return fallback
	}
	if !clock.anchored || qpc < clock.qpc || !safeQPCDelta(qpc-clock.qpc) {
		clock.anchored = true
		clock.qpc = qpc
		clock.at = fallback
		return fallback
	}
	return clock.at.Add(time.Duration(qpc-clock.qpc) * qpc100ns)
}

func packetDuration(frames, rate int) time.Duration {
	if frames <= 0 || rate <= 0 {
		return 0
	}
	return time.Duration(frames) * time.Second / time.Duration(rate)
}

func safeQPCDelta(delta uint64) bool {
	const maxDuration = uint64(^uint64(0) >> 1)
	return delta <= maxDuration/uint64(qpc100ns)
}
