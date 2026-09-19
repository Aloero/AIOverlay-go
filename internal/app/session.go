package app

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"aioverlay/internal/platform/audio"
	"aioverlay/internal/providers"
	"aioverlay/internal/session"
	"context"
	"fmt"
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
	if a.sessions != nil {
		a.sessions.Pause(paused)
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
func (a *App) AudioDevices() ([]audio.Device, error) { return audio.Devices() }
