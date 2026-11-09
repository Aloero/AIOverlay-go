package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"aioverlay/internal/session"
)

type commandPreviewOnly struct{ *auditBackend }

func (*commandPreviewOnly) PreviewCommand(action string) (session.CommandPreview, error) {
	return session.CommandPreview{Action: action, EntryNodeID: "preview-only"}, nil
}

type statusOnly struct{ *auditBackend }

func (*statusOnly) SessionStatus() any { return map[string]bool{"paused": false} }

type pauseOnly struct {
	*auditBackend
	paused bool
}

func (b *pauseOnly) PauseBackground(paused bool) { b.paused = paused }
func (b *pauseOnly) BackgroundPaused() bool      { return b.paused }

type sourceOnly struct {
	*auditBackend
	source  string
	running bool
}

func (b *sourceOnly) SetSourceRunning(id string, running bool) error {
	b.source, b.running = id, running
	return nil
}

type clearOnly struct{ *auditBackend }

func (*clearOnly) ClearConversation(string) (uint64, error) { return 42, nil }

type realtimeOnly struct{ *auditBackend }

func (*realtimeOnly) RequestRealtimeResponse(context.Context, string) (engine.Result, error) {
	return engine.Result{Text: "local fixture response"}, nil
}

type commandOnly struct{ *auditBackend }

func (*commandOnly) PrepareCommand(context.Context, string, session.ScreenCapture) (session.PreparedCommand, error) {
	return session.PreparedCommand{PipelineID: "fixture", Command: domain.CommandConfig{ID: "context"}, SkipRun: true}, nil
}
func (*commandOnly) ValidatePreparedCommand(session.PreparedCommand) error { return nil }
func (*commandOnly) RecordCommandObservation(session.PreparedCommand, engine.Observation) bool {
	return true
}
func (*commandOnly) RecordCommandResult(session.PreparedCommand, engine.Result) bool { return true }

func TestSessionHandlersRequireOnlyTheirOwnCapability(t *testing.T) {
	base := &auditBackend{}
	pause := &pauseOnly{auditBackend: base}
	source := &sourceOnly{auditBackend: base}
	for _, tc := range []struct {
		name, method, path, body, want string
		backend                        Backend
	}{
		{"preview", "GET", "/api/session/command/preview?action=analyze", "", "preview-only", &commandPreviewOnly{base}},
		{"status", "GET", "/api/session/status", "", `"paused":false`, &statusOnly{base}},
		{"pause", "POST", "/api/session/pause", `{"paused":true}`, `"paused":true`, pause},
		{"source", "POST", "/api/session/source", `{"id":"audio","running":true}`, `"running":true`, source},
		{"clear", "POST", "/api/session/context/clear", `{"id":"conversation"}`, `"generation":42`, &clearOnly{base}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := New("127.0.0.1:0", tc.backend)
			response := httptest.NewRecorder()
			server.DesktopHandler().ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), tc.want) {
				t.Fatalf("independent capability rejected: HTTP %d %s", response.Code, response.Body.String())
			}
		})
	}
	if !pause.paused || source.source != "audio" || !source.running {
		t.Fatal("capability operation not dispatched")
	}
}

func TestPauseCommandDoesNotRequireCaptureOrAudioCapabilities(t *testing.T) {
	backend := &pauseOnly{auditBackend: &auditBackend{}}
	server := New("127.0.0.1:0", backend)
	id, err := server.StartCommand("session_pause")
	if err != nil || id != "session-pause" || !backend.paused {
		t.Fatalf("id=%q err=%v paused=%v", id, err, backend.paused)
	}
}

func TestIndependentCommandAndRealtimeCapabilitiesUseJobQueue(t *testing.T) {
	for _, realtime := range []bool{false, true} {
		name := "command"
		var backend Backend = &commandOnly{&auditBackend{}}
		if realtime {
			name = "realtime"
			backend = &realtimeOnly{&auditBackend{}}
		}
		t.Run(name, func(t *testing.T) {
			server := New("127.0.0.1:0", backend)
			t.Cleanup(server.CancelRun)
			var id string
			var err error
			if realtime {
				id, err = server.StartRealtimeResponse("audio")
			} else {
				id, err = server.StartCommand("screen_context")
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			job, err := server.jobs.Wait(ctx, id)
			if err != nil || job.Error != "" || job.Running {
				t.Fatalf("job=%+v err=%v", job, err)
			}
			if realtime && job.Text != "local fixture response" {
				t.Fatalf("text=%q", job.Text)
			}
			if !realtime && job.Trace.Outcome != domain.OutcomeContextOnly {
				t.Fatalf("outcome=%s", job.Trace.Outcome)
			}
		})
	}
}
