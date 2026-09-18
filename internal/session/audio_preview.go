package session

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"aioverlay/internal/domain"
	paudio "aioverlay/internal/platform/audio"
)

type AudioPreview struct {
	PCM16      []int16 `json:"-"`
	SampleRate int     `json:"sample_rate"`
	DurationMS int     `json:"duration_ms"`
	Peak       float64 `json:"peak"`
	RMS        float64 `json:"rms"`
}

func (m *Manager) PreviewAudio(ctx context.Context, cfg domain.AudioSourceConfig, duration time.Duration) (AudioPreview, error) {
	if err := ctx.Err(); err != nil {
		return AudioPreview{}, err
	}
	m.mu.RLock()
	for _, runtime := range m.sources {
		if runtime.cfg.Kind == domain.SourceAudio && !sourceFinished(runtime) {
			m.mu.RUnlock()
			return AudioPreview{}, fmt.Errorf("pause the running audio source before local preview")
		}
	}
	m.mu.RUnlock()
	release, err := m.captureGate.acquirePreview()
	if err != nil {
		return AudioPreview{}, err
	}
	defer release()
	if duration < 250*time.Millisecond {
		duration = 250 * time.Millisecond
	}
	if duration > 5*time.Second {
		duration = 5 * time.Second
	}
	m.mu.RLock()
	capture := m.audioCapture
	m.mu.RUnlock()
	if capture == nil {
		return AudioPreview{}, fmt.Errorf("audio capture unavailable")
	}
	cctx, cancel := context.WithTimeout(ctx, duration+2*time.Second)
	defer cancel()
	out := AudioPreview{}
	var sum float64
	var packetErr error
	err = capture(cctx, cfg, func(b paudio.Block) {
		if cctx.Err() != nil {
			return
		}
		if len(b.Samples) == 0 {
			return
		}
		if b.Rate < 8000 || b.Rate > 192000 {
			packetErr = fmt.Errorf("audio preview received an invalid sample rate")
			cancel()
			return
		}
		if out.SampleRate == 0 {
			out.SampleRate = b.Rate
		}
		if b.Rate != out.SampleRate {
			return
		}
		need := out.SampleRate*int(duration/time.Millisecond)/1000 - len(out.PCM16)
		if need <= 0 {
			cancel()
			return
		}
		if need > len(b.Samples) {
			need = len(b.Samples)
		}
		for _, v := range b.Samples[:need] {
			x := float64(v) / 32768
			if x < 0 {
				x = -x
			}
			if x > out.Peak {
				out.Peak = x
			}
			sum += x * x
		}
		out.PCM16 = append(out.PCM16, b.Samples[:need]...)
		if len(out.PCM16) >= out.SampleRate*int(duration/time.Millisecond)/1000 {
			cancel()
		}
	})
	if packetErr != nil {
		return AudioPreview{}, packetErr
	}
	if ctx.Err() != nil {
		return AudioPreview{}, ctx.Err()
	}
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return AudioPreview{}, fmt.Errorf("audio preview timed out: %w", context.DeadlineExceeded)
	}
	complete := out.SampleRate > 0 && len(out.PCM16) >= out.SampleRate*int(duration/time.Millisecond)/1000
	if err != nil && !(complete && errors.Is(err, context.Canceled)) {
		return AudioPreview{}, err
	}
	if out.SampleRate == 0 || len(out.PCM16) == 0 {
		return AudioPreview{}, fmt.Errorf("audio preview received no samples")
	}
	out.DurationMS = len(out.PCM16) * 1000 / out.SampleRate
	out.RMS = math.Sqrt(sum / float64(len(out.PCM16)))
	return out, nil
}
