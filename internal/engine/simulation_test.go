package engine

import (
	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func simConfig() domain.Config {
	c := config.Default()
	p := c.Pipelines[0].Nodes[1].ModelPool
	p.Models = p.Models[:2]
	p.Order = domain.PoolPriority
	p.Dispatch = domain.DispatchSequential
	p.MaxCalls = 8
	p.MaxParallel = 2
	return c
}
func TestSimulationSequentialBudgetAndFallback(t *testing.T) {
	c := simConfig()
	p := c.Pipelines[0].Nodes[1].ModelPool
	req := SimulationRequest{Config: c, PipelineID: c.ActivePipelineID, Outcomes: []SimulationOutcome{{ModelID: p.Models[0].ModelID, Outcome: domain.OutcomeModelUnavailable, DurationMS: 300}}}
	res, err := Simulate(context.Background(), req, nil)
	if err != nil || res.Error != "" || res.Calls != 2 || res.ElapsedMS != 1300 || res.Trace.Winner.ModelID != p.Models[1].ModelID {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	c.Pipelines[0].Budget.MaxModelCalls = 1
	req.Config = c
	res, err = Simulate(context.Background(), req, nil)
	if err != nil || res.Calls != 1 || res.Error == "" {
		t.Fatalf("budget ignored: %+v %v", res, err)
	}
}
func TestSimulationStaggeredRaceUsesVirtualTimeAndCancelsLoser(t *testing.T) {
	c := simConfig()
	p := c.Pipelines[0].Nodes[1].ModelPool
	p.Dispatch = domain.DispatchStaggered
	p.HedgeDelayMS = 2000
	start := time.Now()
	res, err := Simulate(context.Background(), SimulationRequest{Config: c, PipelineID: c.ActivePipelineID, Outcomes: []SimulationOutcome{{ModelID: p.Models[0].ModelID, Outcome: domain.OutcomeSuccess, DurationMS: 9000}}}, nil)
	if err != nil || res.Error != "" || res.ElapsedMS != 3000 || res.Calls != 2 {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("simulation used real delays")
	}
	found := false
	for _, ev := range res.Trace.Events {
		if ev.Kind == "attempt_end" && ev.Outcome == domain.OutcomeCancelled && ev.Message == "race_lost" {
			found = true
		}
	}
	if !found {
		t.Fatal("loser cancellation missing")
	}
}
func TestSimulationRetriesKeysAndRejectsAmbiguousOutcomes(t *testing.T) {
	c := simConfig()
	c.Providers[0].CredentialPool.Keys = []domain.Credential{{ID: "one", Enabled: true, Secret: "synthetic-one"}, {ID: "two", Enabled: true, Secret: "synthetic-two"}}
	c.Providers[0].CredentialPool.MaxKeysPerRequest = 2
	m := c.Pipelines[0].Nodes[1].ModelPool.Models[0]
	res, err := Simulate(context.Background(), SimulationRequest{Config: c, PipelineID: c.ActivePipelineID, Outcomes: []SimulationOutcome{{ModelID: m.ModelID, Attempt: 1, Outcome: domain.OutcomeAuthFailed, DurationMS: 100}}}, nil)
	if err != nil || res.Error != "" || res.Calls != 2 || res.Trace.Winner.CredentialID != "two" {
		t.Fatalf("key rotation %+v %v", res, err)
	}
	_, err = Simulate(context.Background(), SimulationRequest{Config: c, PipelineID: c.ActivePipelineID, Outcomes: []SimulationOutcome{{Outcome: domain.OutcomeAnyError, DurationMS: 1}}}, nil)
	if err == nil {
		t.Fatal("any_error accepted as a concrete result")
	}
}
func TestSimulationNeverChangesOriginalStatisticsOrConfig(t *testing.T) {
	c := simConfig()
	c.RuntimeStatistics = &domain.RuntimeStatistics{Cursors: map[string]int{"model:" + c.ActivePipelineID + "/models": 1}}
	p := c.Pipelines[0].Nodes[1].ModelPool
	p.Order = domain.PoolRoundRobin
	for i := 0; i < 2; i++ {
		res, err := Simulate(context.Background(), SimulationRequest{Config: c, PipelineID: c.ActivePipelineID}, nil)
		if err != nil || res.Trace.Winner.ModelID != p.Models[1].ModelID {
			t.Fatal(res, err)
		}
	}
	if c.RuntimeStatistics.Cursors["model:"+c.ActivePipelineID+"/models"] != 1 {
		t.Fatal("live cursor mutated")
	}
}

func TestSimulationSafetyCapIsExactAndRetainsConditionalError(t *testing.T) {
	c := simConfig()
	p := c.Pipelines[0].Nodes[1].ModelPool
	p.Models = nil
	p.MaxCalls = 1000
	c.Pipelines[0].Budget.MaxModelCalls = 1000
	for i := 0; i < 400; i++ {
		p.Models = append(p.Models, domain.ModelSlot{ID: fmt.Sprint(i), ProviderID: "opencode", ModelID: fmt.Sprint(i), Enabled: true, QualityScore: 100, MaxAttempts: 1})
	}
	res, err := Simulate(context.Background(), SimulationRequest{Config: c, PipelineID: c.ActivePipelineID, Outcomes: []SimulationOutcome{{Outcome: domain.OutcomeModelUnavailable, DurationMS: 1}}}, nil)
	if err != nil || res.Calls != 256 || !strings.Contains(res.Error, "safety") {
		t.Fatalf("calls=%d error=%s (%v)", res.Calls, res.Error, err)
	}
}

func mixedSimulationConfig() domain.Config {
	cfg := config.Default()
	provider := &cfg.Providers[0]
	provider.CredentialPool.Keys = []domain.Credential{{ID: "simulation", Label: "Simulation", Secret: "synthetic", Enabled: true}}
	model := func(id, task, result string) *domain.ModelPoolConfig {
		return &domain.ModelPoolConfig{
			Task: task, ResultName: result, Policy: domain.PoolPriority, MaxParallel: 1, MaxCalls: 1,
			Prompt: domain.PromptConfig{User: "{{input.text}}"},
			Models: []domain.ModelSlot{{ID: id, ProviderID: provider.ID, ModelID: id, Enabled: true, Priority: 100, QualityScore: 100, MaxAttempts: 1}},
		}
	}
	pipeline := domain.Pipeline{
		ID: "mixed", Name: "Mixed", Enabled: true, EntryNodeID: "audio", OutputNodeID: "output",
		Budget: domain.ExecutionBudget{MaxWallTimeMS: 30000, MaxModelCalls: 8, MaxParallelCalls: 2, MaxProviders: 2, MaxKeysPerProvider: 1, ConnectTimeoutMS: 1000, FirstTokenTimeoutMS: 5000, ResponseTimeoutMS: 10000, StreamIdleTimeoutMS: 3000},
		Commands: []domain.CommandConfig{
			{ID: "manual", Action: "analyze", EntryNodeID: "decision", Intent: domain.IntentRespond, CaptureScreen: true},
			{ID: "context", Action: "screen_context", EntryNodeID: "context", Intent: domain.IntentContext, CaptureScreen: true},
		},
		Sources: []domain.EventSource{{ID: "audio-source", Kind: domain.SourceAudio, Enabled: true, EntryNodeID: "audio", Intent: domain.IntentDecide, Audio: &domain.AudioSourceConfig{Mode: "system"}}},
		Nodes: []domain.Node{
			{ID: "audio", Type: domain.NodeAudio, Name: "Audio"},
			{ID: "stt", Type: domain.NodeModelPool, Name: "STT", ModelPool: model("stt-model", "transcribe", "audio.text")},
			{ID: "context", Type: domain.NodeContext, Name: "Context"},
			{ID: "decision", Type: domain.NodeDecision, Name: "Decision", ModelPool: model("decision-model", "decision", "decision")},
			{ID: "capture", Type: domain.NodeCapture, Name: "Capture"},
			{ID: "answer", Type: domain.NodeModelPool, Name: "Answer", ModelPool: model("answer-model", "generate", "answer")},
			{ID: "output", Type: domain.NodeOutput, Name: "Output"},
		},
		Edges: []domain.Edge{
			{ID: "audio-stt", From: "audio", To: "stt", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1},
			{ID: "stt-context", From: "stt", To: "context", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1},
			{ID: "context-decision", From: "context", To: "decision", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1},
			{ID: "decision-answer", From: "decision", To: "answer", On: []domain.Outcome{domain.OutcomeRespond}, MaxVisits: 1},
			{ID: "decision-capture", From: "decision", To: "capture", On: []domain.Outcome{domain.OutcomeNeedScreen}, MaxVisits: 1},
			{ID: "capture-answer", From: "capture", To: "answer", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1},
			{ID: "answer-output", From: "answer", To: "output", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1},
		},
	}
	cfg.Pipelines = []domain.Pipeline{pipeline}
	cfg.ActivePipelineID = pipeline.ID
	return cfg
}

func TestSimulationAudioTriggerCanEndInNoAction(t *testing.T) {
	cfg := mixedSimulationConfig()
	report, err := Simulate(context.Background(), SimulationRequest{
		Config: cfg, PipelineID: "mixed", TriggerID: "audio-source", ContextText: "previous turn",
		Outcomes: []SimulationOutcome{
			{NodeID: "stt", ModelID: "stt-model", Outcome: domain.OutcomeSuccess, DurationMS: 100, Text: "current question"},
			{NodeID: "decision", ModelID: "decision-model", Outcome: domain.OutcomeSuccess, DurationMS: 100, Text: `{"action":"no_action"}`},
		},
	}, nil)
	if err != nil || report.Error != "" || report.Calls != 2 || report.Trace.Outcome != domain.OutcomeNoAction || report.Text != "" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestSimulationNeedScreenUsesSyntheticActionWithoutExtraModelCall(t *testing.T) {
	cfg := mixedSimulationConfig()
	report, err := Simulate(context.Background(), SimulationRequest{
		Config: cfg, PipelineID: "mixed", TriggerID: "audio-source",
		Outcomes: []SimulationOutcome{
			{NodeID: "stt", ModelID: "stt-model", Outcome: domain.OutcomeSuccess, DurationMS: 100, Text: "look at this"},
			{NodeID: "decision", ModelID: "decision-model", Outcome: domain.OutcomeSuccess, DurationMS: 100, Text: `{"action":"need_screen"}`},
			{NodeID: "answer", ModelID: "answer-model", Outcome: domain.OutcomeSuccess, DurationMS: 100, Text: "answer"},
		},
	}, nil)
	if err != nil || report.Error != "" || report.Calls != 3 || report.Text != "answer" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	captureEvents := 0
	for _, event := range report.Trace.Events {
		if event.Kind == "capture_start" && event.NodeID == "capture" {
			captureEvents++
		}
	}
	if captureEvents != 1 {
		t.Fatalf("synthetic capture events=%d", captureEvents)
	}
}

func TestSimulationManualRespondBypassesOnlyDecision(t *testing.T) {
	cfg := mixedSimulationConfig()
	report, err := Simulate(context.Background(), SimulationRequest{
		Config: cfg, PipelineID: "mixed", TriggerID: "manual",
		Outcomes: []SimulationOutcome{{NodeID: "answer", ModelID: "answer-model", Outcome: domain.OutcomeSuccess, DurationMS: 100, Text: "forced answer"}},
	}, nil)
	if err != nil || report.Error != "" || report.Calls != 1 || report.Text != "forced answer" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestSimulationContextCommandStopsWithoutModelCall(t *testing.T) {
	cfg := mixedSimulationConfig()
	report, err := Simulate(context.Background(), SimulationRequest{Config: cfg, PipelineID: "mixed", TriggerID: "context"}, nil)
	if err != nil || report.Error != "" || report.Calls != 0 || report.Trace.Outcome != domain.OutcomeContextOnly {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
