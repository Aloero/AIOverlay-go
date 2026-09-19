package audio

import (
	"aioverlay/internal/domain"
	"math"
	"time"
)

type Segment struct {
	Clip           domain.AudioClip
	Started, Ended time.Time
}

// Segmenter is an energy/silence gate, not an ML speech detector. Owned by one
// capture thread. At most one configured segment plus 200ms pre-roll is retained.
type Segmenter struct {
	Config             domain.AudioSourceConfig
	rate               int
	pending, pre       []int16
	pendingOverlap     int
	carryOverlap       bool
	carryStart         time.Time
	carryEnd           time.Time
	started, lastVoice time.Time
	silence            int
	Level              float64
}

const segmentOverlapMS = 200

func (s *Segmenter) Push(b Block) []Segment {
	var out []Segment
	if b.Discontinuity || b.TimestampError {
		if seg, ok := s.Interrupt(); ok {
			out = append(out, seg)
		}
	}
	// A bad timestamp cannot establish a reliable capture range or manual
	// cutoff. Discard this packet after closing the old span; the session
	// records the gap. Ordinary discontinuity packets still start a new span.
	if b.TimestampError || b.Rate < 8000 || b.Rate > 192000 {
		return out
	}
	if s.rate != b.Rate {
		s.Reset()
		s.rate = b.Rate
	}
	if len(b.Samples) == 0 && s.carryOverlap && b.At.After(s.carryEnd) {
		s.clearCarryOverlap()
		s.pre = nil
	}
	chunk := s.rate / 50
	for offset := 0; offset < len(b.Samples); offset += chunk {
		end := min(offset+chunk, len(b.Samples))
		p := b.Samples[offset:end]
		at := b.At.Add(time.Duration(offset) * time.Second / time.Duration(s.rate))
		energy := 0.0
		for _, v := range p {
			n := float64(v) / 32768
			energy += n * n
		}
		s.Level = math.Sqrt(energy / float64(len(p)))
		voice := s.Level >= s.Config.Threshold
		if s.carryOverlap {
			maxGap := time.Duration(max(1, len(p))) * time.Second / time.Duration(s.rate)
			if !voice || at.After(s.carryEnd.Add(maxGap)) {
				s.clearCarryOverlap()
				s.pre = nil
			}
		}
		if voice {
			if len(s.pending) == 0 {
				s.pending = append(s.pending, s.pre...)
				if s.carryOverlap {
					s.pendingOverlap = len(s.pre)
					s.started = s.carryStart
				} else {
					s.started = at.Add(-time.Duration(len(s.pre)) * time.Second / time.Duration(s.rate))
				}
				s.pre = nil
				s.clearCarryOverlap()
			}
			s.lastVoice = at.Add(time.Duration(len(p)) * time.Second / time.Duration(s.rate))
			s.silence = 0
		} else {
			s.silence += len(p)
		}
		if !s.started.IsZero() {
			s.pending = append(s.pending, p...)
			maxSamples := s.rate * s.Config.SegmentMS / 1000
			switch {
			case maxSamples > 0 && len(s.pending) >= maxSamples:
				if seg, ok := s.flushAtLimit(); ok {
					out = append(out, seg)
				}
			case s.silence >= s.rate*s.Config.SilenceMS/1000:
				if seg, ok := s.flushCompleted(); ok {
					out = append(out, seg)
				}
			}
		} else {
			s.pre = append(s.pre, p...)
			cap := s.preRollLimit()
			if len(s.pre) > cap {
				copy(s.pre, s.pre[len(s.pre)-cap:])
				s.pre = s.pre[:cap]
			}
		}
	}
	if len(b.Samples) == 0 && !s.lastVoice.IsZero() && b.At.Sub(s.lastVoice) >= time.Duration(s.Config.SilenceMS)*time.Millisecond {
		if seg, ok := s.flushCompleted(); ok {
			out = append(out, seg)
		}
	}
	return out
}
func (s *Segmenter) flushCompleted() (Segment, bool) {
	segment, ok := s.finishSegment()
	if ok {
		segment.Clip.Final = true
	}
	return segment, ok
}

func (s *Segmenter) flushAtLimit() (Segment, bool) {
	overlapSamples := s.preRollLimit()
	if overlapSamples > len(s.pending) {
		overlapSamples = len(s.pending)
	}
	overlap := append([]int16(nil), s.pending[len(s.pending)-overlapSamples:]...)
	segment, ok := s.finishSegment()
	if !ok {
		return Segment{}, false
	}
	segment.Clip.Final = true
	if len(overlap) > 0 {
		s.pre = overlap
		s.carryOverlap = true
		s.carryEnd = segment.Ended
		s.carryStart = segment.Ended.Add(-time.Duration(len(overlap)) * time.Second / time.Duration(s.rate))
	}
	return segment, true
}

func (s *Segmenter) preRollLimit() int {
	if s.rate <= 0 {
		return 0
	}
	limit := s.rate * segmentOverlapMS / 1000
	maxSamples := s.rate * s.Config.SegmentMS / 1000
	chunk := max(1, s.rate/50)
	if maxSamples > 0 && limit > maxSamples-chunk {
		limit = max(0, maxSamples-chunk)
	}
	if limit > 0 {
		limit -= limit % chunk
	}
	return limit
}

func (s *Segmenter) clearCarryOverlap() {
	s.carryOverlap = false
	s.carryStart = time.Time{}
	s.carryEnd = time.Time{}
}

func (s *Segmenter) ActiveStart() (time.Time, bool) {
	return s.started, !s.started.IsZero()
}

// Interrupt closes the current semantic span without pretending it ended on a
// normal speech/silence boundary. It is used when the capture transport breaks.
func (s *Segmenter) Interrupt() (Segment, bool) {
	segment, ok := s.finishSegment()
	s.pre = nil
	s.clearCarryOverlap()
	s.Level = 0
	return segment, ok
}

func (s *Segmenter) finishSegment() (Segment, bool) {
	if s.rate <= 0 || s.started.IsZero() || len(s.pending) == 0 {
		s.resetSpeech()
		return Segment{}, false
	}

	sampleCount := len(s.pending)
	duration := time.Duration(sampleCount) * time.Second / time.Duration(s.rate)
	segment := Segment{Started: s.started, Ended: s.started.Add(duration)}
	emit := sampleCount >= s.rate*s.Config.MinimumMS/1000
	if emit {
		segment.Clip = domain.AudioClip{
			PCM16:        append([]int16(nil), s.pending...),
			DurationMS:   sampleCount * 1000 / s.rate,
			SampleRate:   s.rate,
			Channels:     1,
			StartedAt:    segment.Started,
			EndedAt:      segment.Ended,
			OverlapMS:    s.pendingOverlap * 1000 / s.rate,
			Continuation: s.pendingOverlap > 0,
		}
	}
	s.resetSpeech()
	return segment, emit
}

func (s *Segmenter) resetSpeech() {
	s.pending = nil
	s.pendingOverlap = 0
	s.started = time.Time{}
	s.lastVoice = time.Time{}
	s.silence = 0
}

func (s *Segmenter) Reset() {
	s.resetSpeech()
	s.pre = nil
	s.clearCarryOverlap()
	s.Level = 0
}
