package config

import (
	"aioverlay/internal/domain"
	"testing"
)

func sourceContractFixture() (domain.Pipeline, map[string]domain.Node, map[string]domain.ProviderConfig) {
	node := domain.Node{ID: "entry", Type: domain.NodeModelPool}
	pipeline := domain.Pipeline{
		ID: "p", Nodes: []domain.Node{node},
		Contexts: []domain.ContextConfig{{ID: "c", Enabled: true, MaxAgeS: 60, MaxEntries: 10, MaxChars: 1000, ScreenMaxAgeS: 60, KeepLatestScreen: true}},
		Commands: []domain.CommandConfig{{ID: "manual", Action: "analyze", Intent: domain.IntentRespond, EntryNodeID: "entry", CaptureScreen: true}},
		Sources:  []domain.EventSource{{ID: "source", Kind: domain.SourceScreenTimer, Intent: domain.IntentContext, ContextID: "c", EntryNodeID: "entry", IntervalMS: 1000}},
	}
	providers := map[string]domain.ProviderConfig{"live": {ID: "live", Enabled: true, Kind: domain.ProviderOpenAILive, Realtime: &domain.RealtimeProviderConfig{ResponsesModel: "fixture"}}}
	return pipeline, map[string]domain.Node{"entry": node}, providers
}

func setContractLiveSource(p *domain.Pipeline) {
	s := &p.Sources[0]
	s.Kind = domain.SourceAudio
	s.Audio = &domain.AudioSourceConfig{Mode: "system", SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01}
	s.Realtime = &domain.RealtimeSourceConfig{ProviderID: "live", ModelID: "fixture", ResponsePolicy: "manual"}
}

func TestSourceValidationErrorContract(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*domain.Pipeline)
	}{
		{"valid_screen", "", func(*domain.Pipeline) {}},
		{"valid_live", "", setContractLiveSource},
		{"contexts_before_commands", "context c limits invalid", func(p *domain.Pipeline) { p.Contexts[0].MaxAgeS = 0; p.Commands[0].ID = "" }},
		{"node_context_mode", "node entry has invalid context mode", func(p *domain.Pipeline) { p.Nodes[0].ContextMode = "typo" }},
		{"command_id", "invalid or duplicate command ID", func(p *domain.Pipeline) { p.Commands = append(p.Commands, p.Commands[0]) }},
		{"command_action", "pipeline p has duplicate command action \"analyze\"", func(p *domain.Pipeline) { c := p.Commands[0]; c.ID = "second"; p.Commands = append(p.Commands, c) }},
		{"analyze_capture", "analyze command must capture the screen", func(p *domain.Pipeline) { p.Commands[0].CaptureScreen = false }},
		{"source_collision", "invalid, duplicate or ambiguous source ID", func(p *domain.Pipeline) { p.Sources[0].ID = "manual" }},
		{"disabled_context", "source source references disabled or missing context", func(p *domain.Pipeline) { p.Contexts[0].Enabled = false }},
		{"unknown_entry", "source source references missing entry", func(p *domain.Pipeline) { p.Sources[0].EntryNodeID = "missing" }},
		{"screen_cadence", "screen context interval cannot be faster than local capture interval", func(p *domain.Pipeline) { p.Sources[0].IntervalMS = 2000; p.Sources[0].ContextIntervalMS = 1000 }},
		{"screen_debounce", "screen source debounce cannot exceed maximum wait", func(p *domain.Pipeline) { p.Sources[0].DebounceMS = 2000; p.Sources[0].MaxWaitMS = 1000 }},
		{"audio_required", "audio source missing settings", func(p *domain.Pipeline) { p.Sources[0].Kind = domain.SourceAudio }},
		{"live_provider", "source source realtime provider is missing, disabled or not openai_live", func(p *domain.Pipeline) { setContractLiveSource(p); p.Sources[0].Realtime.ProviderID = "missing" }},
		{"live_intent", "source source realtime route cannot claim semantic no_action", func(p *domain.Pipeline) { setContractLiveSource(p); p.Sources[0].Intent = domain.IntentDecide }},
		{"live_policy", "source source manual realtime policy must use context intent", func(p *domain.Pipeline) { setContractLiveSource(p); p.Sources[0].Intent = domain.IntentRespond }},
		{"session_budget", "session limits invalid", func(p *domain.Pipeline) { p.SessionBudget = &domain.SessionBudget{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pipeline, nodes, providers := sourceContractFixture()
			tc.change(&pipeline)
			err := validateSources(pipeline, nodes, providers)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
