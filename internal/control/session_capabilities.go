package control

import (
	"context"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	paudio "aioverlay/internal/platform/audio"
	"aioverlay/internal/session"
)

// Each handler asks only for the operation it consumes. A backend can add an
// audio preview or a session status without implementing unrelated commands.
type commandPreviewBackend interface {
	PreviewCommand(string) (session.CommandPreview, error)
}

type commandBackend interface {
	PrepareCommand(context.Context, string, session.ScreenCapture) (session.PreparedCommand, error)
	ValidatePreparedCommand(session.PreparedCommand) error
	RecordCommandObservation(session.PreparedCommand, engine.Observation) bool
	RecordCommandResult(session.PreparedCommand, engine.Result) bool
}

type backgroundPauseBackend interface {
	PauseBackground(bool)
	BackgroundPaused() bool
}

type sourceControlBackend interface {
	SetSourceRunning(string, bool) error
}

type realtimeResponseBackend interface {
	RequestRealtimeResponse(context.Context, string) (engine.Result, error)
}

type sessionStatusBackend interface{ SessionStatus() any }

type conversationClearBackend interface{ ClearConversation(string) (uint64, error) }

type audioDeviceBackend interface {
	AudioDevices() ([]paudio.Device, error)
}

type audioCapabilityBackend interface{ AudioCapabilities() paudio.Capabilities }

type audioProcessBackend interface {
	AudioProcesses() ([]paudio.Process, error)
}

type audioPreviewBackend interface {
	PreviewAudio(context.Context, domain.AudioSourceConfig, time.Duration) (session.AudioPreview, error)
}

type localSTTPreviewer interface {
	PreviewLocalSTT(context.Context, domain.AudioSourceConfig, string, time.Duration) (session.LocalSTTPreview, error)
}
