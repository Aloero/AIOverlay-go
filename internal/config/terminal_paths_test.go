package config

import (
	"testing"

	"aioverlay/internal/domain"
)

func TestContextOnlyCommandIsValidTerminalWithoutOutput(t *testing.T) {
	cfg := Default()
	pipeline := &cfg.Pipelines[0]
	pipeline.Nodes = append(pipeline.Nodes, domain.Node{ID: "context-only", Type: domain.NodeContext, Name: "Context only"})
	pipeline.Commands = append(pipeline.Commands, domain.CommandConfig{
		ID: "store-screen", Action: "screen_context", EntryNodeID: "context-only",
		Intent: domain.IntentContext, CaptureScreen: true,
	})
	if err := Validate(cfg); err != nil {
		t.Fatalf("context-only terminal rejected: %v", err)
	}
}

func TestDecisionNoActionIsValidSemanticTerminal(t *testing.T) {
	cfg := Default()
	pipeline := &cfg.Pipelines[0]
	decisionPool := *pipeline.Nodes[1].ModelPool
	pipeline.Nodes = append(pipeline.Nodes, domain.Node{ID: "decision-only", Type: domain.NodeDecision, Name: "Decision", ModelPool: &decisionPool})
	pipeline.Commands = append(pipeline.Commands, domain.CommandConfig{
		ID: "decide-screen", Action: "screen_decide", EntryNodeID: "decision-only",
		Intent: domain.IntentDecide, CaptureScreen: true,
	})
	if err := Validate(cfg); err != nil {
		t.Fatalf("no_action terminal rejected: %v", err)
	}
}

func TestRespondEntryRejectsErrorOnlyPathToOutput(t *testing.T) {
	cfg := Default()
	pipeline := &cfg.Pipelines[0]
	modelNode := pipeline.Nodes[1]
	pipeline.Commands[0].EntryNodeID = modelNode.ID
	for i := range pipeline.Edges {
		if pipeline.Edges[i].From == modelNode.ID {
			pipeline.Edges[i].On = []domain.Outcome{domain.OutcomeAnyError}
		}
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("respond command accepted a graph where successful model output has no terminal path")
	}
}

func TestRespondEntryRejectsDisconnectedNormalBranch(t *testing.T) {
	cfg := Default()
	pipeline := &cfg.Pipelines[0]
	pipeline.Nodes = append(pipeline.Nodes, domain.Node{ID: "orphan-model", Type: domain.NodeModelPool, Name: "Orphan", ModelPool: pipeline.Nodes[1].ModelPool})
	pipeline.Commands[0].EntryNodeID = "orphan-model"
	if err := Validate(cfg); err == nil {
		t.Fatal("unfinished respond branch accepted")
	}
}

func TestCommandAndSourceCannotShareTriggerID(t *testing.T) {
	cfg := Default()
	pipeline := &cfg.Pipelines[0]
	pipeline.Contexts = append(pipeline.Contexts, domain.ContextConfig{
		ID: "ctx", Name: "Context", Enabled: true,
		MaxAgeS: 60, MaxEntries: 10, MaxChars: 1000, ScreenMaxAgeS: 60,
	})
	pipeline.Sources = append(pipeline.Sources, domain.EventSource{
		ID: pipeline.Commands[0].ID, Name: "Ambiguous source", Kind: domain.SourceScreenTimer,
		Enabled: true, EntryNodeID: pipeline.EntryNodeID, Intent: domain.IntentContext,
		ContextID: "ctx", IntervalMS: 1000,
	})
	if err := Validate(cfg); err == nil {
		t.Fatal("command/source trigger ID collision was accepted")
	}
}
