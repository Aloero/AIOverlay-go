package app

import (
	"context"
	"testing"
	"time"

	"aioverlay/internal/domain"
	paudio "aioverlay/internal/platform/audio"
	"aioverlay/internal/providers"
)

type localSTTPreviewProvider struct {
	testing *testing.T
}

func (*localSTTPreviewProvider) ID() string { return "local-preview" }
func (provider *localSTTPreviewProvider) Stream(_ context.Context, request providers.Request, credential string) (<-chan providers.Event, <-chan error) {
	provider.testing.Helper()
	if credential != "" || request.Task != "transcribe" || request.Audio == nil || request.ModelID != "preview-model" {
		provider.testing.Fatalf("unexpected local preview request=%+v credential=%q", request, credential)
	}
	events := make(chan providers.Event, 2)
	errors := make(chan error)
	events <- providers.Event{Type: providers.EventDelta, Text: "preview transcript", ModelID: request.ModelID}
	events <- providers.Event{Type: providers.EventDone, ModelID: request.ModelID}
	close(events)
	close(errors)
	return events, errors
}

func TestPreviewLocalSTTUsesRAMCaptureAndConfiguredLocalProvider(t *testing.T) {
	app := studioTestApp(t)
	app.mu.Lock()
	app.cfg.Providers = append(app.cfg.Providers, domain.ProviderConfig{
		ID: "local-preview", Label: "Local Preview", Kind: domain.ProviderLocalSTT, Enabled: true,
		LocalSTT: &domain.LocalSTTProviderConfig{HelperPath: "helper", ModelPath: "model", ModelID: "preview-model"},
	})
	app.mu.Unlock()
	app.registry.Set("local-preview", &localSTTPreviewProvider{testing: t})
	app.sessions.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		samples := make([]int16, 4000)
		for index := range samples {
			samples[index] = 1200
		}
		emit(paudio.Block{Samples: samples, Rate: 16000, At: time.Now()})
		<-ctx.Done()
		return ctx.Err()
	})

	preview, err := app.PreviewLocalSTT(context.Background(), domain.AudioSourceConfig{
		Mode: "system", SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01,
	}, "local-preview", 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Text != "preview transcript" || preview.ProviderID != "local-preview" || preview.ModelID != "preview-model" {
		t.Fatalf("preview=%+v", preview)
	}
	if preview.DurationMS != 250 || preview.SampleRate != 16000 || preview.RMS <= 0 {
		t.Fatalf("capture metrics=%+v", preview)
	}
}
