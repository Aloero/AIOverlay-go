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
	started, lastVoice time.Time
	silence            int
	Level              float64
}

func (s *Segmenter) Push(b Block) []Segment {
	var out []Segment
	if b.Rate < 8000 || b.Rate > 192000 {
		return out
	}
	if b.Discontinuity || b.TimestampError {
		if seg, ok := s.Interrupt(); ok {
			out = append(out, seg)
		}
	}
	if s.rate != b.Rate {
		s.Reset()
		s.rate = b.Rate
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
		if voice {
			if len(s.pending) == 0 {
				s.pending = append(s.pending, s.pre...)
				s.started = at.Add(-time.Duration(len(s.pre)) * time.Second / time.Duration(s.rate))
				s.pre = nil
			}
			s.lastVoice = at.Add(time.Duration(len(p)) * time.Second / time.Duration(s.rate))
			s.silence = 0
		} else {
			s.silence += len(p)
		}
		if !s.started.IsZero() {
			s.pending = append(s.pending, p...)
			if len(s.pending) >= s.rate*s.Config.SegmentMS/1000 || s.silence >= s.rate*s.Config.SilenceMS/1000 {
				if seg, ok := s.flushCompleted(); ok {
					out = append(out, seg)
				}
			}
		} else {
			s.pre = append(s.pre, p...)
			cap := s.rate / 5
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

// Interrupt closes the current semantic span without pretending it ended on a
// normal speech/silence boundary. It is used when the capture transport breaks.
func (s *Segmenter) Interrupt() (Segment, bool) {
	segment, ok := s.finishSegment()
	s.pre = nil
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
			PCM16:      append([]int16(nil), s.pending...),
			DurationMS: sampleCount * 1000 / s.rate,
			SampleRate: s.rate,
			Channels:   1,
			StartedAt:  segment.Started,
			EndedAt:    segment.Ended,
		}
	}
	s.resetSpeech()
	return segment, emit
}

func (s *Segmenter) resetSpeech() {
	s.pending = nil
	s.started = time.Time{}
	s.lastVoice = time.Time{}
	s.silence = 0
}

func (s *Segmenter) Reset() {
	s.resetSpeech()
	s.pre = nil
	s.Level = 0
}
