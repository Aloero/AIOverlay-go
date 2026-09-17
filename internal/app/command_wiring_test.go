package app

import (
	"testing"
	"time"

	"aioverlay/internal/control"
	"aioverlay/internal/domain"
)

func TestServerScreenContextCommandUsesAppSessionRuntimeWithoutAI(t *testing.T) {
	a := studioTestApp(t)
	cfg := a.GetConfig()
	pipeline := &cfg.Pipelines[0]
	contextCfg := domain.ContextConfig{
		ID: "manual-context", Name: "Manual context", Enabled: true,
		MaxAgeS: 60, MaxEntries: 10, MaxChars: 1000,
		IncludeTranscript: true, KeepLatestScreen: true, ScreenMaxAgeS: 60,
	}
	pipeline.Contexts = append(pipeline.Contexts, contextCfg)
	pipeline.Nodes = append(pipeline.Nodes, domain.Node{ID: "context-only", Type: domain.NodeContext, Name: "Context only"})
	pipeline.Commands = append(pipeline.Commands, domain.CommandConfig{
		ID: "screen-context", Name: "Store screen", Action: "screen_context",
		EntryNodeID: "context-only", Intent: domain.IntentContext, ContextID: contextCfg.ID, CaptureScreen: true,
	})
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	server := control.New("127.0.0.1:0", a)
	server.Capture = func() ([]byte, error) { return []byte("manual-context-frame"), nil }
	jobID, err := server.StartCommand("screen_context")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for server.CurrentJob().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	job := server.CurrentJob()
	if job.ID != jobID || job.Running || job.Error != "" || job.Trace.Outcome != domain.OutcomeContextOnly {
		t.Fatalf("context command job=%+v", job)
	}
	if len(job.Trace.Events) != 0 || len(a.Traces()) != 0 {
		t.Fatal("context-only command unexpectedly entered the AI engine")
	}

	snapshot := a.sessions.Contexts().Snapshot(contextCfg, pipeline.ID, time.Now().Add(time.Second))
	if string(snapshot.ImageJPEG) != "manual-context-frame" {
		t.Fatalf("context-only command did not store screen: %q", snapshot.ImageJPEG)
	}
}
