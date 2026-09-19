package providers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aioverlay/internal/domain"
)

func localSTTProviderFixture(t *testing.T) *LocalSTT {
	t.Helper()
	model := filepath.Join(t.TempDir(), "model.bin")
	if err := os.WriteFile(model, []byte("synthetic model resource"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := NewLocalSTT(domain.ProviderConfig{
		ID: "local", Kind: domain.ProviderLocalSTT, Enabled: true,
		LocalSTT: &domain.LocalSTTProviderConfig{
			HelperPath: os.Args[0], ModelPath: model, ModelID: "local-model",
			Args: []string{"-test.run=^TestLocalSTTRuntimeHelperProcess$"},
		},
	})
	t.Cleanup(provider.Close)
	return provider
}

func TestLocalSTTProviderReusesHelperAndCancelsRequest(t *testing.T) {
	provider := localSTTProviderFixture(t)
	clip := &domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}
	text, err := collectLocalSTTProviderText(context.Background(), provider, clip)
	if err != nil || text != "samples=160" {
		t.Fatalf("first transcription text=%q err=%v", text, err)
	}
	provider.mu.Lock()
	firstRuntime := provider.runtime
	provider.mu.Unlock()
	if firstRuntime == nil || !firstRuntime.Alive() {
		t.Fatal("local STT helper was not kept warm")
	}

	slow := &domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}
	slow.PCM16[0] = 9999
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err = collectLocalSTTProviderText(ctx, provider, slow)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) && OutcomeOf(err) != domain.OutcomeTimeout {
		t.Fatalf("cancelled provider transcription err=%v", err)
	}

	text, err = collectLocalSTTProviderText(context.Background(), provider, clip)
	if err != nil || text != "samples=160" {
		t.Fatalf("provider did not recover after request cancellation: text=%q err=%v", text, err)
	}
	provider.mu.Lock()
	secondRuntime := provider.runtime
	provider.mu.Unlock()
	if secondRuntime != firstRuntime {
		t.Fatal("request cancellation restarted the shared helper")
	}
}

func TestLocalSTTHelperEnvironmentIsSanitizedAndResourceStatusIsLocal(t *testing.T) {
	provider := localSTTProviderFixture(t)
	t.Setenv("AIO_TEST_SECRET_SHOULD_NOT_LEAK", "super-secret")
	command, err := provider.helperCommand()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command.Env, "\n")
	if strings.Contains(joined, "super-secret") || strings.Contains(joined, "AIO_TEST_SECRET_SHOULD_NOT_LEAK") {
		t.Fatal("local STT helper inherited unrelated environment secret")
	}
	if !strings.Contains(joined, "AIO_STT_PROTOCOL="+localSTTProtocol) || !strings.Contains(joined, "AIO_STT_MODEL_ID=local-model") {
		t.Fatalf("local STT helper contract missing from env: %s", joined)
	}
	status := localSTTResourceStatus(provider.cfg)
	if !status.Configured || !status.HelperPresent || !status.ModelPresent || status.ModelBytes == 0 || status.ModelID != "local-model" {
		t.Fatalf("resource status=%+v", status)
	}
}

func TestLocalSTTProviderMissingModelFailsWithoutFallback(t *testing.T) {
	provider := NewLocalSTT(domain.ProviderConfig{
		ID: "local", Kind: domain.ProviderLocalSTT, Enabled: true,
		LocalSTT: &domain.LocalSTTProviderConfig{HelperPath: os.Args[0], ModelPath: filepath.Join(t.TempDir(), "missing.bin"), ModelID: "missing"},
	})
	defer provider.Close()
	_, err := collectLocalSTTProviderText(context.Background(), provider, &domain.AudioClip{PCM16: []int16{1}, SampleRate: 16000, Channels: 1})
	if err == nil || OutcomeOf(err) != domain.OutcomeModelUnavailable {
		t.Fatalf("missing local model error=%v", err)
	}
	provider.mu.Lock()
	runtime := provider.runtime
	provider.mu.Unlock()
	if runtime != nil {
		t.Fatal("missing model started a helper or fallback runtime")
	}
}

func collectLocalSTTProviderText(ctx context.Context, provider *LocalSTT, clip *domain.AudioClip) (string, error) {
	events, errorsCh := provider.Stream(ctx, Request{Task: "transcribe", ProviderID: provider.ID(), ModelID: provider.cfg.LocalSTT.ModelID, Audio: clip}, "")
	var text strings.Builder
	for events != nil || errorsCh != nil {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if event.Type == EventDelta {
				text.WriteString(event.Text)
			}
		case err, ok := <-errorsCh:
			if !ok {
				errorsCh = nil
				continue
			}
			if err != nil {
				return "", err
			}
		}
	}
	return text.String(), nil
}

func TestLocalSTTCapabilitiesReportResourcesWithoutStartingHelper(t *testing.T) {
	provider := localSTTProviderFixture(t)
	capabilities := adapterCapabilities(provider.cfg, provider)
	if capabilities.LocalSTT == nil || !capabilities.LocalSTT.HelperPresent || !capabilities.LocalSTT.ModelPresent {
		t.Fatalf("local STT capabilities=%+v", capabilities.LocalSTT)
	}
	if capabilities.LocalSTT.ResourcePolicy != "keep_warm" || capabilities.LocalSTT.Warm || capabilities.LocalSTT.ActiveRequests != 0 {
		t.Fatalf("cold default lifecycle status=%+v", capabilities.LocalSTT)
	}
	provider.mu.Lock()
	runtime := provider.runtime
	provider.mu.Unlock()
	if runtime != nil {
		t.Fatal("capability inspection started local STT helper")
	}
	if !capabilities.Media.Audio || !capabilities.Media.TranscriptionOnly || capabilities.Media.Image {
		t.Fatalf("local STT media capabilities=%+v", capabilities.Media)
	}
}

func TestLocalSTTKeepWarmIgnoresBackgroundPause(t *testing.T) {
	provider := localSTTProviderFixture(t)
	provider.cfg.LocalSTT.ResourcePolicy = "keep_warm"
	clip := &domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}
	if _, err := collectLocalSTTProviderText(context.Background(), provider, clip); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	warm := provider.runtime
	provider.mu.Unlock()
	provider.SetBackgroundPaused(true)
	provider.mu.Lock()
	stillWarm := provider.runtime
	paused := provider.backgroundPaused
	provider.mu.Unlock()
	if !paused || warm == nil || stillWarm != warm || !warm.Alive() {
		t.Fatalf("keep-warm policy unloaded helper: warm=%p current=%p paused=%v", warm, stillWarm, paused)
	}
}

func TestLocalSTTUnloadOnPauseWaitsForActiveRequest(t *testing.T) {
	provider := localSTTProviderFixture(t)
	provider.cfg.LocalSTT.ResourcePolicy = "unload_on_pause"
	clip := &domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}
	clip.PCM16[0] = 8888
	result := make(chan error, 1)
	go func() {
		_, err := collectLocalSTTProviderText(context.Background(), provider, clip)
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		provider.mu.Lock()
		active, runtime := provider.activeRequests, provider.runtime
		provider.mu.Unlock()
		if active == 1 && runtime != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("local STT request never became active")
		}
		time.Sleep(time.Millisecond)
	}
	provider.SetBackgroundPaused(true)
	provider.mu.Lock()
	during := provider.runtime
	pending := provider.unloadPending
	active := provider.activeRequests
	provider.mu.Unlock()
	if during == nil || !during.Alive() || !pending || active != 1 {
		t.Fatalf("pause killed active request: runtime=%p pending=%v active=%d", during, pending, active)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	after := provider.runtime
	active = provider.activeRequests
	pending = provider.unloadPending
	provider.mu.Unlock()
	if after != nil || active != 0 || pending {
		t.Fatalf("helper was not unloaded after last active request: runtime=%p active=%d pending=%v", after, active, pending)
	}
}

func TestLocalSTTResumeCancelsPendingUnload(t *testing.T) {
	provider := localSTTProviderFixture(t)
	provider.cfg.LocalSTT.ResourcePolicy = "unload_on_pause"
	clip := &domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}
	clip.PCM16[0] = 8888
	result := make(chan error, 1)
	go func() {
		_, err := collectLocalSTTProviderText(context.Background(), provider, clip)
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		provider.mu.Lock()
		active := provider.activeRequests
		provider.mu.Unlock()
		if active == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("local STT request never became active")
		}
		time.Sleep(time.Millisecond)
	}
	provider.SetBackgroundPaused(true)
	provider.SetBackgroundPaused(false)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	runtime := provider.runtime
	pending := provider.unloadPending
	paused := provider.backgroundPaused
	provider.mu.Unlock()
	if runtime == nil || !runtime.Alive() || pending || paused {
		t.Fatalf("resume failed to retain warm helper: runtime=%p pending=%v paused=%v", runtime, pending, paused)
	}
}

func TestLocalSTTPreviewWhilePausedUnloadsAfterRequest(t *testing.T) {
	provider := localSTTProviderFixture(t)
	provider.cfg.LocalSTT.ResourcePolicy = "unload_on_pause"
	provider.SetBackgroundPaused(true)
	clip := &domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}
	text, err := collectLocalSTTProviderText(context.Background(), provider, clip)
	if err != nil || text != "samples=160" {
		t.Fatalf("paused preview text=%q err=%v", text, err)
	}
	provider.mu.Lock()
	runtime := provider.runtime
	active := provider.activeRequests
	provider.mu.Unlock()
	if runtime != nil || active != 0 {
		t.Fatalf("paused one-shot preview left helper warm: runtime=%p active=%d", runtime, active)
	}
}

func TestLocalSTTStatusReportsWarmLifecycleWithoutStartingHelper(t *testing.T) {
	provider := localSTTProviderFixture(t)
	provider.cfg.LocalSTT.ResourcePolicy = "unload_on_pause"
	status := provider.LocalSTTStatus()
	if status.Warm || status.ActiveRequests != 0 || status.ResourcePolicy != "unload_on_pause" {
		t.Fatalf("cold status=%+v", status)
	}
	if _, err := collectLocalSTTProviderText(context.Background(), provider, &domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}); err != nil {
		t.Fatal(err)
	}
	status = provider.LocalSTTStatus()
	if !status.Warm || status.ActiveRequests != 0 || status.BackgroundPaused {
		t.Fatalf("warm status=%+v", status)
	}
	provider.SetBackgroundPaused(true)
	status = provider.LocalSTTStatus()
	if status.Warm || !status.BackgroundPaused {
		t.Fatalf("paused status=%+v", status)
	}
}
