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
