package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"aioverlay/internal/domain"
	paudio "aioverlay/internal/platform/audio"
)

func TestPreparedCommandPinsNamedStageContexts(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	pipeline := &cfg.Pipelines[0]
	pipeline.Contexts = append(pipeline.Contexts, domain.ContextConfig{ID: "private", Name: "Private", Enabled: true, MaxAgeS: 60, MaxEntries: 10, MaxChars: 1000, IncludeTranscript: true, KeepLatestScreen: true, ScreenMaxAgeS: 60})
	pipeline.Commands[0].ContextID = "c"
	pipeline.Commands[0].EntryNodeID = "solver"
	pipeline.Nodes = []domain.Node{{ID: "solver", Type: domain.NodeModelPool, Name: "Solver", ContextMode: "named", ContextID: "private"}}
	manager.cfg = cfg
	manager.capture = func(context.Context) ([]byte, error) { return []byte("manual-screen"), nil }
	triggerCfg := pipeline.Contexts[0]
	privateCfg := pipeline.Contexts[1]
	triggerGen := manager.contexts.Generation(pipeline.ID, triggerCfg.ID)
	privateGen := manager.contexts.Generation(pipeline.ID, privateCfg.ID)
	now := time.Now()
	manager.contexts.Append(triggerCfg, pipeline.ID, triggerGen, ContextEntry{ID: "trigger", Kind: "transcript", Text: "TRIGGER_CONTEXT", At: now.Add(-time.Second)})
	manager.contexts.Append(privateCfg, pipeline.ID, privateGen, ContextEntry{ID: "private", Kind: "transcript", Text: "PRIVATE_STAGE_CONTEXT", At: now.Add(-time.Second)})

	prepared, err := manager.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Input.ContextText != "[transcript] TRIGGER_CONTEXT" {
		t.Fatalf("route context=%q", prepared.Input.ContextText)
	}
	view := prepared.Input.ContextViews["private"]
	if view.Text != "[transcript] PRIVATE_STAGE_CONTEXT" || string(prepared.Input.ImageJPEG) != "manual-screen" || !prepared.Input.ImagePinned {
		t.Fatalf("stage view=%+v input image=%q pinned=%v", view, prepared.Input.ImageJPEG, prepared.Input.ImagePinned)
	}
	if _, err := manager.ClearContext("private"); err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidatePreparedCommand(prepared); !errors.Is(err, context.Canceled) {
		t.Fatalf("cleared named stage context remained dispatchable: %v", err)
	}
}

func TestSourceRenameDoesNotRestartCaptureRuntime(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	manager.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, _ func(paudio.Block)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx, cfg)
	defer func() { cancel(); manager.Close() }()
	waitUntil(t, func() bool { return manager.source("a") != nil }, "audio source did not start")
	before := manager.source("a")
	next := cfg
	next.Pipelines = append([]domain.Pipeline(nil), cfg.Pipelines...)
	next.Pipelines[0].Sources = append([]domain.EventSource(nil), cfg.Pipelines[0].Sources...)
	next.Pipelines[0].Sources[0].Name = "Renamed only"
	manager.Apply(next)
	after := manager.source("a")
	if before != after {
		t.Fatal("source rename restarted capture runtime")
	}
}
