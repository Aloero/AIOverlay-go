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

func TestBehaviorReportKeepsLatestContextScreenUnresolvedAcrossSTT(t *testing.T) {
	cfg := mixedSimulationConfig()
	pipeline := cfg.Pipelines[0]
	pipeline.Contexts = []domain.ContextConfig{{
		ID: "conversation", Name: "Conversation", Enabled: true,
		IncludeTranscript: true, KeepLatestScreen: true,
		MaxAgeS: 120, MaxEntries: 30, MaxChars: 24000, ScreenMaxAgeS: 30,
	}}
	pipeline.Sources[0].ContextID = "conversation"

	audioStart := []inputState{sourceInputState(pipeline, pipeline.Sources[0])}
	sttInput := describePoolInput(pipeline, "stt", audioStart)
	if !sttInput.AudioRequired || sttInput.ImageRequired {
		t.Fatalf("STT should receive audio without raw context screen: %+v", sttInput)
	}
	decisionInput := describePoolInput(pipeline, "decision", audioStart)
	if decisionInput.AudioRequired || !decisionInput.ImageRequired || !decisionInput.TextAvailable {
		t.Fatalf("latest context screen was lost or falsely resolved after STT: %+v", decisionInput)
	}

	report := DescribePipelineBehavior(pipeline)
	var decisionStage *BehaviorAIStage
	for i := range report.Sources[0].AIStages {
		stage := &report.Sources[0].AIStages[i]
		if stage.ID == "decision" {
			decisionStage = stage
			break
		}
	}
	if decisionStage == nil || !decisionStage.Input.ImageRequired {
		t.Fatalf("behavior summary hid possible context image from Decision: %+v", report.Sources[0].AIStages)
	}

	pipeline.Contexts[0].KeepLatestScreen = false
	withoutScreen := describePoolInput(pipeline, "decision", []inputState{sourceInputState(pipeline, pipeline.Sources[0])})
	if withoutScreen.ImageRequired {
		t.Fatalf("disabled latest-screen context still requires image: %+v", withoutScreen)
	}
}

func TestBehaviorReportShowsEffectiveScreenCadenceAndRegion(t *testing.T) {
	cfg := mixedSimulationConfig()
	pipeline := cfg.Pipelines[0]
	pipeline.Contexts = []domain.ContextConfig{{ID: "screen-context", Name: "Screen", Enabled: true, MaxAgeS: 60, MaxEntries: 10, MaxChars: 1000, KeepLatestScreen: true, ScreenMaxAgeS: 60}}
	pipeline.Sources = []domain.EventSource{{
		ID: "screen-source", Name: "Screen", Kind: domain.SourceScreenTimer, Enabled: true,
		EntryNodeID: "context", Intent: domain.IntentContext, ContextID: "screen-context",
		IntervalMS: 2000, ContextIntervalMS: 4000, TriggerIntervalMS: 8000,
		ChangedOnly: true, DebounceMS: 500, MaxWaitMS: 5000,
		ScreenRegion: &domain.ScreenRegionConfig{X: 10, Y: 20, Width: 640, Height: 480},
	}}
	report := DescribePipelineBehavior(pipeline)
	if len(report.Sources) != 1 || report.Sources[0].Screen == nil {
		t.Fatalf("screen policy missing: %+v", report.Sources)
	}
	screen := report.Sources[0].Screen
	if screen.CaptureIntervalMS != 2000 || screen.ContextIntervalMS != 4000 || screen.TriggerIntervalMS != 8000 || !screen.ChangedOnly {
		t.Fatalf("screen policy=%+v", screen)
	}
	if screen.Region == nil || screen.Region.Width != 640 || screen.Region.Height != 480 {
		t.Fatalf("screen region=%+v", screen.Region)
	}
}

func TestBehaviorReportShowsPersistentRealtimeSourceWithoutFakeGraphRoute(t *testing.T) {
	cfg := config.Default()
	pipeline := &cfg.Pipelines[0]
	pipeline.Sources = []domain.EventSource{{
		ID: "live", Name: "Live", Kind: domain.SourceAudio, Enabled: true, Intent: domain.IntentContext,
		Audio:    &domain.AudioSourceConfig{Mode: "system", SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01},
		Realtime: &domain.RealtimeSourceConfig{ProviderID: "live-provider", ModelID: "gpt-live-1", ResponsePolicy: "manual"},
	}}
	report := DescribePipelineBehavior(*pipeline)
	if len(report.Sources) != 1 {
		t.Fatalf("sources=%+v", report.Sources)
	}
	source := report.Sources[0]
	if !source.UsesAI || len(source.Reachable) != 0 || len(source.AIStages) != 1 {
		t.Fatalf("realtime source=%+v", source)
	}
	if source.RealtimeProviderID != "live-provider" || source.RealtimeModelID != "gpt-live-1" || source.RealtimeResponsePolicy != "manual" {
		t.Fatalf("realtime metadata=%+v", source)
	}
	if !source.AIStages[0].Input.AudioRequired || source.AIStages[0].ContextMode != "remote" {
		t.Fatalf("realtime AI stage=%+v", source.AIStages[0])
	}
}
