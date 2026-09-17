package app

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/platform/audio"
	"aioverlay/internal/session"
	"context"
	"time"
)

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
func (a *App) PrepareCommand(ctx context.Context, action string) (session.PreparedCommand, error) {
	return a.sessions.PrepareCommand(ctx, action)
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
