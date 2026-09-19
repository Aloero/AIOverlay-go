package session

import (
	"aioverlay/internal/domain"
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestPermissionGenerationRejectsLateModelCapture(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Nodes = []domain.Node{{ID: "fresh-screen", Type: domain.NodeCapture}}
	manager.cfg = cfg
	var captures atomic.Int32
	manager.capture = func(context.Context) ([]byte, error) {
		captures.Add(1)
		return []byte("screen"), nil
	}

	prepared, err := manager.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Capture == nil || captures.Load() != 1 {
		t.Fatalf("prepared capture=%v calls=%d", prepared.Capture != nil, captures.Load())
	}

	revoked := cfg
	revoked.Pipelines = append([]domain.Pipeline(nil), cfg.Pipelines...)
	revoked.Pipelines[0].Nodes = nil
	manager.Apply(revoked)

	if _, err := prepared.Capture(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("late model capture after permission revoke error=%v", err)
	}
	if captures.Load() != 1 {
		t.Fatalf("revoked capture reached platform: calls=%d", captures.Load())
	}
}

func TestNonPermissionConfigChangeKeepsPinnedCaptureValid(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Nodes = []domain.Node{{ID: "fresh-screen", Type: domain.NodeCapture}}
	manager.cfg = cfg
	var captures atomic.Int32
	manager.capture = func(context.Context) ([]byte, error) {
		captures.Add(1)
		return []byte("screen"), nil
	}

	prepared, err := manager.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	updated := cfg
	updated.Pipelines = append([]domain.Pipeline(nil), cfg.Pipelines...)
	updated.Pipelines[0].Contexts = append([]domain.ContextConfig(nil), cfg.Pipelines[0].Contexts...)
	updated.Pipelines[0].Contexts[0].MaxEntries++
	manager.Apply(updated)

	if _, err := prepared.Capture(context.Background()); err != nil {
		t.Fatalf("non-permission config change revoked capture: %v", err)
	}
	if captures.Load() != 2 {
		t.Fatalf("capture calls=%d", captures.Load())
	}
}

func TestFencePermissionsRevokesChangedProcessAudioTarget(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	source := &cfg.Pipelines[0].Sources[0]
	source.Audio.Mode = "process"
	source.Audio.DeviceID = ""
	source.Audio.DeviceRole = ""
	source.Audio.Process = &domain.AudioProcessConfig{ExecutablePath: `C:\Program Files\Zoom\bin\Zoom.exe`}
	manager.cfg = cfg
	manager.permissionPolicy = capturePermissions(cfg)
	manager.permissionPolicySet = true

	cancelled := make(chan struct{})
	runtime := &sourceRuntime{pipelineID: "p", cfg: *source, cancel: func() { close(cancelled) }}
	manager.sources[source.ID] = runtime

	next := cfg
	next.Pipelines = append([]domain.Pipeline(nil), cfg.Pipelines...)
	next.Pipelines[0].Sources = append([]domain.EventSource(nil), cfg.Pipelines[0].Sources...)
	audio := *next.Pipelines[0].Sources[0].Audio
	process := *audio.Process
	process.ExecutablePath = `C:\Program Files\Teams\current\Teams.exe`
	audio.Process = &process
	next.Pipelines[0].Sources[0].Audio = &audio

	manager.FencePermissions(next)
	select {
	case <-cancelled:
	default:
		t.Fatal("process target change did not revoke old audio capture")
	}
}
