package control

import (
	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	paudio "aioverlay/internal/platform/audio"
	"aioverlay/internal/session"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

type sessionBackend interface {
	PrepareCommand(context.Context, string, session.ScreenCapture) (session.PreparedCommand, error)
	RecordCommandResult(session.PreparedCommand, engine.Result) bool
	PauseBackground(bool)
	SetSourceRunning(string, bool) error
	BackgroundPaused() bool
	SessionStatus() any
	ClearConversation(string) (uint64, error)
	AudioDevices() ([]paudio.Device, error)
	PreviewAudio(context.Context, domain.AudioSourceConfig, time.Duration) (session.AudioPreview, error)
}

func (s *Server) StartCommand(action string) (string, error) {
	b, ok := s.backend.(sessionBackend)
	if !ok {
		if action == "analyze" {
			return s.StartScreenLegacy()
		}
		return "", fmt.Errorf("command runtime unavailable")
	}
	if action == "session_pause" {
		b.PauseBackground(!b.BackgroundPaused())
		return "session-pause", nil
	}
	live, ok := s.backend.(liveBackend)
	if !ok {
		return "", fmt.Errorf("live execution unavailable")
	}
	pipelineID := s.backend.GetConfig().ActivePipelineID
	purpose := commandPurpose(action)
	return s.jobs.startTaskPurpose(s.ctx, pipelineID, purpose, func(ctx context.Context, opt engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		prepared, err := b.PrepareCommand(ctx, action, func(callCtx context.Context) ([]byte, error) {
			if s.Capture == nil {
				return nil, fmt.Errorf("capture unavailable")
			}
			return s.Capture()
		})
		if err != nil {
			return engine.Result{}, nil, err
		}
		if prepared.SkipRun {
			return engine.Result{Trace: emptyCommandTrace(prepared.PipelineID, prepared.Command.ID, purpose)}, nil, nil
		}
		res, err := live.Run(ctx, prepared.PipelineID, prepared.Input, opt)
		if err == nil {
			b.RecordCommandResult(prepared, res)
		}
		return res, nil, err
	})
}
func commandPurpose(action string) string {
	switch action {
	case "screen_context":
		return "context_command"
	case "screen_decide":
		return "decision_command"
	default:
		return "command"
	}
}

func emptyCommandTrace(pipeline, source, purpose string) (tr domain.ExecutionTrace) {
	tr.PipelineID = pipeline
	tr.SourceID = source
	tr.Purpose = purpose
	tr.Outcome = domain.OutcomeContextOnly
	tr.StartedAt = time.Now()
	tr.FinishedAt = tr.StartedAt
	return
}

func (s *Server) handleAudioPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b, ok := s.backend.(sessionBackend)
	if !ok {
		http.Error(w, "audio runtime unavailable", 501)
		return
	}
	var req struct {
		Config     domain.AudioSourceConfig `json:"config"`
		DurationMS int                      `json:"duration_ms"`
	}
	if err := decodeJSON(r, &req, 16<<10); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.DurationMS == 0 {
		req.DurationMS = 2000
	}
	if err := config.ValidateAudio(req.Config); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.DurationMS+2000)*time.Millisecond)
	defer cancel()
	preview, err := b.PreviewAudio(ctx, req.Config, time.Duration(req.DurationMS)*time.Millisecond)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	wav := paudio.WAV(preview.PCM16, preview.SampleRate)
	writeJSON(w, map[string]any{"sample_rate": preview.SampleRate, "duration_ms": preview.DurationMS, "peak": preview.Peak, "rms": preview.RMS, "wav_base64": base64.StdEncoding.EncodeToString(wav)})
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if b, ok := s.backend.(sessionBackend); ok {
		writeJSON(w, b.SessionStatus())
		return
	}
	writeJSON(w, map[string]any{"paused": true, "sources": []any{}})
}
func (s *Server) handleSessionPause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b, ok := s.backend.(sessionBackend)
	if !ok {
		http.Error(w, "session runtime unavailable", 501)
		return
	}
	var req struct {
		Paused *bool `json:"paused"`
	}
	if err := decodeJSON(r, &req, 4<<10); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	paused := !b.BackgroundPaused()
	if req.Paused != nil {
		paused = *req.Paused
	}
	b.PauseBackground(paused)
	writeJSON(w, map[string]bool{"paused": paused})
}
func (s *Server) handleContextClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b, ok := s.backend.(sessionBackend)
	if !ok {
		http.Error(w, "session runtime unavailable", 501)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &req, 4<<10); err != nil || req.ID == "" {
		http.Error(w, "context id required", 400)
		return
	}
	g, err := b.ClearConversation(req.ID)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "generation": g})
}
func (s *Server) handleAudioDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b, ok := s.backend.(sessionBackend)
	if !ok {
		http.Error(w, "audio runtime unavailable", 501)
		return
	}
	d, err := b.AudioDevices()
	if err != nil {
		http.Error(w, err.Error(), 501)
		return
	}
	writeJSON(w, d)
}

func (s *Server) PublishBackground(p session.Publication) {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	if !p.TriggeredAt.After(s.backgroundClearAt) {
		return
	}
	if p.ID <= s.background.ID {
		return
	}
	s.background = p
}
func (s *Server) BackgroundPublication() session.Publication {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	return s.background
}

func (s *Server) handleSessionSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b, ok := s.backend.(sessionBackend)
	if !ok {
		http.Error(w, "session runtime unavailable", 501)
		return
	}
	var req struct {
		ID      string `json:"id"`
		Running bool   `json:"running"`
	}
	if err := decodeJSON(r, &req, 4<<10); err != nil || req.ID == "" {
		http.Error(w, "source id required", 400)
		return
	}
	if err := b.SetSourceRunning(req.ID, req.Running); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "running": req.Running})
}
func (s *Server) ClearPresentation() {
	s.jobs.Clear()
	s.backgroundMu.Lock()
	s.backgroundClearAt = time.Now()
	s.background = session.Publication{}
	s.backgroundMu.Unlock()
}
