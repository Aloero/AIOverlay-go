package app

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"aioverlay/internal/platform/audio"
	"aioverlay/internal/providers"
	"aioverlay/internal/session"
	"context"
	"fmt"
	"strings"
	"time"
)

func (a *App) openRealtimeSession(ctx context.Context, providerID, modelID string) (providers.RealtimeSession, error) {
	a.mu.RLock()
	registry := a.registry
	eng := a.engine
	a.mu.RUnlock()
	if registry == nil || eng == nil {
		return nil, fmt.Errorf("realtime provider runtime unavailable")
	}
	credential, err := eng.SelectSessionCredential(providerID)
	if err != nil {
		return nil, err
	}
	realtime, err := registry.OpenRealtime(ctx, providers.RealtimeOpenRequest{
		ProviderID: providerID, ModelID: modelID, Credential: credential.Secret,
	})
	eng.RecordSessionCredential(providerID, credential, providers.OutcomeOf(err))
	if err != nil {
		return nil, err
	}
	return realtime, nil
}

func (a *App) StartSessions(ctx context.Context) {
	if a.sessions != nil {
		a.sessions.Start(ctx, a.GetConfig())
	}
}
func (a *App) SetSessionCapture(manual, background session.ScreenCapture) {
	if a.sessions != nil {
		a.sessions.SetCapture(manual, background)
	}
}
func (a *App) SetSessionPublisher(fn func(session.Publication)) {
	if a.sessions != nil {
		a.sessions.SetPublisher(fn)
	}
}
func (a *App) PreviewCommand(action string) (session.CommandPreview, error) {
	if a.sessions == nil {
		return session.CommandPreview{}, fmt.Errorf("session runtime unavailable")
	}
	return a.sessions.PreviewCommand(action)
}
func (a *App) PrepareCommand(ctx context.Context, action string, capture session.ScreenCapture) (session.PreparedCommand, error) {
	if a.sessions == nil {
		return session.PreparedCommand{}, fmt.Errorf("session runtime unavailable")
	}
	return a.sessions.PrepareCommandWithCapture(ctx, action, capture)
}
func (a *App) ValidatePreparedCommand(prepared session.PreparedCommand) error {
	if a.sessions == nil {
		return fmt.Errorf("session runtime unavailable")
	}
	return a.sessions.ValidatePreparedCommand(prepared)
}
func (a *App) RecordCommandObservation(prepared session.PreparedCommand, observation engine.Observation) bool {
	if a.sessions == nil {
		return false
	}
	return a.sessions.RecordCommandObservation(prepared, observation)
}
func (a *App) RecordCommandResult(prepared session.PreparedCommand, result engine.Result) bool {
	if a.sessions == nil {
		return false
	}
	return a.sessions.RecordCommandResult(prepared, result)
}
func (a *App) RequestRealtimeResponse(ctx context.Context, sourceID string) (engine.Result, error) {
	if a.sessions == nil {
		return engine.Result{}, fmt.Errorf("session runtime unavailable")
	}
	return a.sessions.RequestRealtimeResponse(ctx, sourceID)
}

func (a *App) SetSourceRunning(id string, running bool) error {
	if a.sessions == nil {
		return nil
	}
	return a.sessions.SetSourceRunning(id, running)
}
func (a *App) PauseBackground(paused bool) {
	a.setBackgroundPaused(paused, "")
}

func (a *App) SuspendBackground(reason string) {
	a.setBackgroundPaused(true, reason)
}

func (a *App) setBackgroundPaused(paused bool, safetyReason string) {
	a.operation.RLock()
	defer a.operation.RUnlock()
	a.mu.RLock()
	registry := a.registry
	a.mu.RUnlock()
	if paused {
		if a.sessions != nil {
			if safetyReason != "" {
				a.sessions.PauseForSafety(safetyReason)
			} else {
				a.sessions.Pause(true)
			}
		}
		if registry != nil {
			registry.SetBackgroundPaused(true)
		}
		return
	}
	if registry != nil {
		registry.SetBackgroundPaused(false)
	}
	if a.sessions != nil {
		a.sessions.Pause(false)
	}
}
func (a *App) BackgroundPaused() bool { return a.sessions != nil && a.sessions.Paused() }
func (a *App) SessionStatus() any {
	if a.sessions == nil {
		return map[string]any{"running": false}
	}
	return a.sessions.Status()
}
func (a *App) ClearConversation(id string) (uint64, error) { return a.sessions.ClearContext(id) }
func (a *App) PreviewAudio(ctx context.Context, cfg domain.AudioSourceConfig, d time.Duration) (session.AudioPreview, error) {
	return a.sessions.PreviewAudio(ctx, cfg, d)
}
func (a *App) PreviewLocalSTT(ctx context.Context, audioConfig domain.AudioSourceConfig, providerID string, duration time.Duration) (session.LocalSTTPreview, error) {
	if !a.operation.TryRLock() {
		return session.LocalSTTPreview{}, fmt.Errorf("settings are being saved; try again")
	}
	defer a.operation.RUnlock()
	if a.sessions == nil {
		return session.LocalSTTPreview{}, fmt.Errorf("session runtime unavailable")
	}
	a.mu.RLock()
	registry := a.registry
	var providerConfig domain.ProviderConfig
	found := false
	for _, configured := range a.cfg.Providers {
		if configured.ID == providerID && configured.Enabled {
			providerConfig = configured
			found = true
			break
		}
	}
	a.mu.RUnlock()
	if !found || !domain.Policy(providerConfig.Kind).LocalRuntime || providerConfig.LocalSTT == nil {
		return session.LocalSTTPreview{}, fmt.Errorf("enabled local STT provider %q not found", providerID)
	}
	adapter, ok := registry.Get(providerID)
	if !ok {
		return session.LocalSTTPreview{}, fmt.Errorf("local STT provider %q is not registered", providerID)
	}
	preview, err := a.sessions.PreviewAudio(ctx, audioConfig, duration)
	if err != nil {
		return session.LocalSTTPreview{}, err
	}
	clip := &domain.AudioClip{PCM16: append([]int16(nil), preview.PCM16...), SampleRate: preview.SampleRate, Channels: 1, DurationMS: preview.DurationMS, Final: true}
	request := providers.Request{
		Task: "transcribe", ProviderID: providerID, ModelID: providerConfig.LocalSTT.ModelID, Audio: clip,
		ConnectTimeout:  time.Duration(providerConfig.RequestDefaults.ConnectTimeoutMS) * time.Millisecond,
		ResponseTimeout: time.Duration(providerConfig.RequestDefaults.ResponseTimeoutMS) * time.Millisecond,
	}
	events, errorsCh := adapter.Stream(ctx, request, "")
	text, err := collectLocalSTTPreview(ctx, events, errorsCh)
	if err != nil {
		return session.LocalSTTPreview{}, err
	}
	return session.LocalSTTPreview{
		ProviderID: providerID, ModelID: providerConfig.LocalSTT.ModelID, Text: text,
		SampleRate: preview.SampleRate, DurationMS: preview.DurationMS, Peak: preview.Peak, RMS: preview.RMS,
	}, nil
}

func collectLocalSTTPreview(ctx context.Context, events <-chan providers.Event, errorsCh <-chan error) (string, error) {
	var text strings.Builder
	for events != nil || errorsCh != nil {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if event.Type == providers.EventDelta {
				if text.Len()+len(event.Text) > 2<<20 {
					return "", fmt.Errorf("local STT preview response exceeded 2 MiB")
				}
				text.WriteString(event.Text)
			}
		case streamErr, ok := <-errorsCh:
			if !ok {
				errorsCh = nil
				continue
			}
			if streamErr != nil {
				return "", streamErr
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return strings.TrimSpace(text.String()), nil
}
func (a *App) AudioDevices() ([]audio.Device, error)    { return audio.Devices() }
func (a *App) AudioCapabilities() audio.Capabilities    { return audio.GetCapabilities() }
func (a *App) AudioProcesses() ([]audio.Process, error) { return audio.Processes() }
