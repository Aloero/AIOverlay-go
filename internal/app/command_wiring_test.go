package app

import (
	"context"
	"testing"
	"time"

	"aioverlay/internal/control"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
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
	if job.ID != jobID || job.Running || job.Error != "" || job.Purpose != "context_command" || job.Trace.Purpose != "context_command" || job.Trace.Outcome != domain.OutcomeContextOnly {
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

type manualAnswerProvider struct{}

func (*manualAnswerProvider) ID() string { return "opencode" }
func (*manualAnswerProvider) Stream(_ context.Context, request providers.Request, _ string) (<-chan providers.Event, <-chan error) {
	events := make(chan providers.Event, 2)
	errors := make(chan error)
	if len(request.ImageJPEG) == 0 {
		close(events)
		close(errors)
		return events, errors
	}
	events <- providers.Event{Type: providers.EventDelta, Text: "stored manual answer", ModelID: request.ModelID}
	events <- providers.Event{Type: providers.EventDone, ModelID: request.ModelID}
	close(events)
	close(errors)
	return events, errors
}

func TestServerManualAnswerReturnsToSelectedContextWithoutAnotherTurn(t *testing.T) {
	a := studioTestApp(t)
	cfg := a.GetConfig()
	pipeline := &cfg.Pipelines[0]
	contextCfg := domain.ContextConfig{
		ID: "answer-context", Name: "Answer context", Enabled: true,
		MaxAgeS: 60, MaxEntries: 10, MaxChars: 2000,
		IncludeTranscript: true, IncludeAnswers: true, KeepLatestScreen: true, ScreenMaxAgeS: 60,
	}
	pipeline.Contexts = append(pipeline.Contexts, contextCfg)
	pipeline.Commands[0].ContextID = contextCfg.ID
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a.registry.Set("opencode", &manualAnswerProvider{})

	server := control.New("127.0.0.1:0", a)
	server.Capture = func() ([]byte, error) { return []byte("manual-frame"), nil }
	jobID, err := server.StartCommand("analyze")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for server.CurrentJob().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	job := server.CurrentJob()
	if job.ID != jobID || job.Running || job.Error != "" || job.Text != "stored manual answer" {
		t.Fatalf("manual job=%+v", job)
	}

	snapshot := a.sessions.Contexts().Snapshot(contextCfg, pipeline.ID, time.Now().Add(time.Second))
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].Kind != "answer" || snapshot.Entries[0].Text != "stored manual answer" {
		t.Fatalf("manual answer was not stored in selected context: %+v", snapshot.Entries)
	}
	if len(a.Traces()) != 1 {
		t.Fatalf("storing assistant output triggered another AI turn: traces=%d", len(a.Traces()))
	}
}

type decisionNoActionProvider struct{}

func (*decisionNoActionProvider) ID() string { return "opencode" }
func (*decisionNoActionProvider) Stream(_ context.Context, request providers.Request, _ string) (<-chan providers.Event, <-chan error) {
	events := make(chan providers.Event, 2)
	errors := make(chan error)
	events <- providers.Event{Type: providers.EventDelta, Text: `{"action":"no_action"}`, ModelID: request.ModelID}
	events <- providers.Event{Type: providers.EventDone, ModelID: request.ModelID}
	close(events)
	close(errors)
	return events, errors
}

func TestServerScreenDecideNoActionUsesSemanticDecisionCommand(t *testing.T) {
	a := studioTestApp(t)
	cfg := a.GetConfig()
	pipeline := &cfg.Pipelines[0]
	pool := *pipeline.Nodes[1].ModelPool
	pool.Models = pool.Models[:1]
	pool.ResultName = "decision"
	decisionID := "manual-decision"
	pipeline.Nodes = append(pipeline.Nodes, domain.Node{ID: decisionID, Type: domain.NodeDecision, Name: "Decide", ModelPool: &pool})
	pipeline.Commands = append(pipeline.Commands, domain.CommandConfig{
		ID: "screen-decide", Name: "Screen decide", Action: "screen_decide",
		EntryNodeID: decisionID, Intent: domain.IntentDecide, CaptureScreen: true,
	})
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a.registry.Set("opencode", &decisionNoActionProvider{})

	server := control.New("127.0.0.1:0", a)
	server.Capture = func() ([]byte, error) { return []byte("decision-frame"), nil }
	jobID, err := server.StartCommand("screen_decide")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for server.CurrentJob().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	job := server.CurrentJob()
	if job.ID != jobID || job.Running || job.Error != "" || job.Text != "" || job.Purpose != "decision_command" || job.Trace.Purpose != "decision_command" || job.Trace.Outcome != domain.OutcomeNoAction {
		t.Fatalf("decision no_action job=%+v", job)
	}
	if !control.RestoresPreviousPresentation(job) {
		t.Fatal("no_action decision command would not restore prior overlay presentation")
	}
}
