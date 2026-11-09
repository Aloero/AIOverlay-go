package engine

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func TestMediaPreprocessorsValidateAllRequiredInputsBeforeStarting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		image   bool
		audio   bool
		missing string
	}{
		{name: "missing_audio", image: true, missing: "audio"},
		{name: "missing_image", audio: true, missing: "image"},
		{name: "both_missing", missing: "image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls atomic.Int32
				provider := &scriptedProvider{run: func(_ context.Context, _ providers.Request, _ string, events chan<- providers.Event) error {
					calls.Add(1)
					events <- providers.Event{Type: providers.EventDelta, Text: "preprocessed"}
					return nil
				}}
				eng, cfg := testEngine(t, 1, provider)
				pipeline := cfg.Pipelines[0]
				node := pipeline.Nodes[1]
				pool := *node.ModelPool
				vision, audio := pool, pool
				audio.Task = "transcribe"
				audio.ResultName = "audio.text"
				pool.VisionPreprocessor = &vision
				pool.AudioPreprocessor = &audio
				node.ModelPool = &pool
				input := nodeValue{}
				if tc.image {
					input.image = []byte("frame")
				}
				if tc.audio {
					input.audio = &domain.AudioClip{PCM16: []int16{1}, SampleRate: 16000, Channels: 1}
				}
				trace := &domain.ExecutionTrace{}
				runner := newRunner(eng, pipeline, Input{}, RunOptions{}, trace)
				_, outcome, err := runner.runMediaPreprocessors(t.Context(), node, pool, input)
				if err == nil || outcome != domain.OutcomeUnsupportedModality || !strings.Contains(err.Error(), tc.missing) {
					t.Fatalf("outcome=%s err=%v; want missing %s", outcome, err, tc.missing)
				}
				// Let any accidentally launched branch finish before checking
				// that rejected input caused neither calls nor late trace events.
				synctest.Wait()
				if calls.Load() != 0 || len(trace.Events) != 0 {
					t.Fatalf("invalid input started work: calls=%d trace_events=%d", calls.Load(), len(trace.Events))
				}
			})
		})
	}
}
