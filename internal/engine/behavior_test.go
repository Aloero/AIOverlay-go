package engine

import (
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
)

func TestBehaviorReportKeepsLegacyScreenshotSimple(t *testing.T) {
	cfg := config.Default()
	pipeline := cfg.Pipelines[0]
	report := DescribePipelineBehavior(pipeline)

	if len(report.Commands) != 1 || len(report.Sources) != 0 || len(report.Contexts) != 0 {
		t.Fatalf("legacy behavior changed: %+v", report)
	}
	command := report.Commands[0]
	if command.Capture != "screen" || command.Intent != domain.IntentRespond || command.ContextRole != "" || !command.UsesAI {
		t.Fatalf("legacy command=%+v", command)
	}
	if len(command.AIStages) != 1 || command.AIStages[0].Input.AudioRequired {
		t.Fatalf("legacy AI stages=%+v", command.AIStages)
	}
}

func TestBehaviorReportShowsAudioSTTWithoutAutomaticAnswer(t *testing.T) {
	cfg := mixedSimulationConfig()
	pipeline := cfg.Pipelines[0]
	pipeline.Contexts = []domain.ContextConfig{{ID: "conversation", Name: "Conversation", Enabled: true, IncludeTranscript: true, KeepLatestScreen: true, MaxAgeS: 120, MaxEntries: 30, MaxChars: 24000}}
	pipeline.Sources[0].ContextID = "conversation"
	pipeline.Sources[0].Intent = domain.IntentContext
	report := DescribePipelineBehavior(pipeline)

	if len(report.Sources) != 1 {
		t.Fatalf("sources=%+v", report.Sources)
	}
	source := report.Sources[0]
	if source.Capture != "system_audio" || source.ContextRole != "write" || source.ContextName != "Conversation" || !source.UsesAI {
		t.Fatalf("source=%+v", source)
	}
	if len(source.AIStages) != 1 || source.AIStages[0].ID != "stt" || !source.AIStages[0].Input.AudioRequired {
		t.Fatalf("audio context must show STT as the only AI stage: %+v", source.AIStages)
	}
	if source.Reachable[len(source.Reachable)-1].Type != domain.NodeContext {
		t.Fatalf("context-only audio route did not stop at Context: %+v", source.Reachable)
	}
}

func TestBehaviorReportShowsScreenContextAsLocalOnly(t *testing.T) {
	cfg := mixedSimulationConfig()
	pipeline := cfg.Pipelines[0]
	pipeline.Contexts = []domain.ContextConfig{{ID: "conversation", Name: "Conversation", Enabled: true, MaxAgeS: 120, MaxEntries: 30, MaxChars: 24000}}
	pipeline.Sources = []domain.EventSource{{
		ID: "screen-source", Name: "Screen context", Kind: domain.SourceScreenTimer, Enabled: true,
		EntryNodeID: "context", Intent: domain.IntentContext, ContextID: "conversation", IntervalMS: 3000,
	}}
	report := DescribePipelineBehavior(pipeline)
	source := report.Sources[0]
	if source.UsesAI || len(source.AIStages) != 0 || source.ContextRole != "write" || source.Capture != "screen_periodic" {
		t.Fatalf("screen context source=%+v", source)
	}
}

func TestBehaviorReportManualRespondSkipsDecisionModel(t *testing.T) {
	cfg := mixedSimulationConfig()
	report := DescribePipelineBehavior(cfg.Pipelines[0])
	manual := report.Commands[0]
	if manual.ID != "manual" || !manual.UsesAI {
		t.Fatalf("manual=%+v", manual)
	}
	for _, stage := range manual.AIStages {
		if stage.ID == "decision" {
			t.Fatalf("manual respond incorrectly calls Decision: %+v", manual.AIStages)
		}
	}
}
