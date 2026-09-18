package app

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

type needScreenProvider struct {
	mu     sync.Mutex
	images map[string]string
	bad    string
}

func newNeedScreenProvider() *needScreenProvider {
	return &needScreenProvider{images: map[string]string{}}
}

func (*needScreenProvider) ID() string { return "opencode" }
func (provider *needScreenProvider) Stream(_ context.Context, request providers.Request, _ string) (<-chan providers.Event, <-chan error) {
	provider.mu.Lock()
	provider.images[request.ModelID] = string(request.ImageJPEG)
	var text string
	switch request.ModelID {
	case "decision-fixture":
		if string(request.ImageJPEG) != "screen-1" {
			provider.bad = "decision did not receive pinned initial screen"
		}
		text = `{"action":"need_screen"}`
	case "answer-fixture":
		if string(request.ImageJPEG) != "screen-2" {
			provider.bad = "answer did not receive fresh need_screen capture"
		}
		text = "fresh answer"
	default:
		provider.bad = "unexpected model request: " + request.ModelID
	}
	provider.mu.Unlock()

	events := make(chan providers.Event, 2)
	errors := make(chan error)
	events <- providers.Event{Type: providers.EventDelta, Text: text, ModelID: request.ModelID}
	events <- providers.Event{Type: providers.EventDone, ModelID: request.ModelID}
	close(events)
	close(errors)
	return events, errors
}

func (provider *needScreenProvider) snapshot() (map[string]string, string) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	images := make(map[string]string, len(provider.images))
	for model, image := range provider.images {
		images[model] = image
	}
	return images, provider.bad
}

func TestServerScreenDecideNeedScreenUsesFreshManualCapture(t *testing.T) {
	a := studioTestApp(t)
	cfg := a.GetConfig()
	pipeline := &cfg.Pipelines[0]
	basePool := *pipeline.Nodes[1].ModelPool
	oneModelPool := func(slotID, modelID, resultName string) *domain.ModelPoolConfig {
		pool := basePool
		pool.Selection = nil
		pool.Policy = domain.PoolPriority
		pool.Order = domain.PoolPriority
		pool.Dispatch = domain.DispatchSequential
		pool.MaxCalls = 1
		pool.MaxParallel = 1
		pool.ResultName = resultName
		pool.Models = []domain.ModelSlot{{
			ID: slotID, ProviderID: "opencode", ModelID: modelID, Enabled: true,
			Priority: 100, Weight: 1, QualityScore: 90, MaxAttempts: 1,
		}}
		return &pool
	}

	decisionID := "need-screen-decision"
	captureID := "need-screen-capture"
	answerID := "need-screen-answer"
	pipeline.Nodes = append(pipeline.Nodes,
		domain.Node{ID: decisionID, Type: domain.NodeDecision, Name: "Need screen?", ModelPool: oneModelPool("decision-slot", "decision-fixture", "decision")},
		domain.Node{ID: captureID, Type: domain.NodeCapture, Name: "Fresh screen"},
		domain.Node{ID: answerID, Type: domain.NodeModelPool, Name: "Fresh answer", ModelPool: oneModelPool("answer-slot", "answer-fixture", "")},
	)
	pipeline.Edges = append(pipeline.Edges,
		domain.Edge{ID: "decision-needs-screen", From: decisionID, To: captureID, On: []domain.Outcome{domain.OutcomeNeedScreen}, Priority: 100, MaxVisits: 1},
		domain.Edge{ID: "fresh-screen-answer", From: captureID, To: answerID, On: []domain.Outcome{domain.OutcomeSuccess}, Priority: 100, MaxVisits: 1},
		domain.Edge{ID: "fresh-answer-output", From: answerID, To: pipeline.OutputNodeID, On: []domain.Outcome{domain.OutcomeSuccess}, Priority: 100, MaxVisits: 1},
	)
	pipeline.Commands = append(pipeline.Commands, domain.CommandConfig{
		ID: "screen-decide-fresh", Name: "Screen decide fresh", Action: "screen_decide",
		EntryNodeID: decisionID, Intent: domain.IntentDecide, CaptureScreen: true,
	})
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	provider := newNeedScreenProvider()
	a.registry.Set("opencode", provider)
	server := control.New("127.0.0.1:0", a)
	var captures atomic.Int32
	server.Capture = func() ([]byte, error) {
		count := captures.Add(1)
		return []byte(fmt.Sprintf("screen-%d", count)), nil
	}

	jobID, err := server.StartCommand("screen_decide")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for server.CurrentJob().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	job := server.CurrentJob()
	if job.ID != jobID || job.Running || job.Error != "" || job.Text != "fresh answer" || job.Trace.Outcome != domain.OutcomeSuccess {
		t.Fatalf("need_screen job=%+v", job)
	}
	if captures.Load() != 2 {
		t.Fatalf("manual captures=%d, want initial + fresh", captures.Load())
	}
	images, bad := provider.snapshot()
	if bad != "" {
		t.Fatal(bad)
	}
	if images["decision-fixture"] != "screen-1" || images["answer-fixture"] != "screen-2" {
		t.Fatalf("model images=%+v", images)
	}
	captureEvents := 0
	for _, event := range job.Trace.Events {
		if event.Kind == "capture_start" && event.NodeID == captureID {
			captureEvents++
		}
	}
	if captureEvents != 1 {
		t.Fatalf("need_screen capture trace events=%d", captureEvents)
	}
}

type queuedManualProvider struct {
	started chan string
	release chan struct{}
}

func (*queuedManualProvider) ID() string { return "opencode" }
func (provider *queuedManualProvider) Stream(ctx context.Context, request providers.Request, _ string) (<-chan providers.Event, <-chan error) {
	events := make(chan providers.Event, 2)
	errors := make(chan error, 1)
	image := string(request.ImageJPEG)
	provider.started <- image
	go func() {
		defer close(events)
		defer close(errors)
		if image == "manual-frame-1" {
			select {
			case <-provider.release:
			case <-ctx.Done():
				errors <- ctx.Err()
				return
			}
		}
		events <- providers.Event{Type: providers.EventDelta, Text: image, ModelID: request.ModelID}
		events <- providers.Event{Type: providers.EventDone, ModelID: request.ModelID}
	}()
	return events, errors
}

func TestTwoManualCommandsPinDistinctScreensBeforeQueuedExecution(t *testing.T) {
	a := studioTestApp(t)
	provider := &queuedManualProvider{started: make(chan string, 2), release: make(chan struct{})}
	a.registry.Set("opencode", provider)
	server := control.New("127.0.0.1:0", a)
	var captures atomic.Int32
	server.Capture = func() ([]byte, error) {
		n := captures.Add(1)
		return []byte(fmt.Sprintf("manual-frame-%d", n)), nil
	}

	firstID, err := server.StartCommand("analyze")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case image := <-provider.started:
		if image != "manual-frame-1" {
			t.Fatalf("first request image=%q", image)
		}
	case <-time.After(time.Second):
		t.Fatal("first manual request did not reach provider")
	}

	secondID, err := server.StartCommand("analyze")
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatalf("manual IDs collided: %q", firstID)
	}
	if captures.Load() != 2 {
		t.Fatalf("queued command did not pin its screen at admission: captures=%d", captures.Load())
	}
	job := server.CurrentJob()
	if job.ID != firstID || job.QueuedID != secondID {
		t.Fatalf("manual queue state=%+v", job)
	}

	close(provider.release)
	select {
	case image := <-provider.started:
		if image != "manual-frame-2" {
			t.Fatalf("queued request used wrong pinned screen: %q", image)
		}
	case <-time.After(time.Second):
		t.Fatal("queued manual request did not reach provider")
	}
	deadline := time.Now().Add(time.Second)
	for server.CurrentJob().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	job = server.CurrentJob()
	if job.ID != secondID || job.Text != "manual-frame-2" || job.Error != "" {
		t.Fatalf("queued manual result=%+v", job)
	}
}
