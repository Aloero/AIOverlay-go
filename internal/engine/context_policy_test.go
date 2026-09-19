package engine

import (
	"context"
	"testing"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func TestAIStageSelectsNamedOrNoContextWithoutChangingPinnedEventMedia(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		contextID   string
		wantContext string
	}{
		{name: "named", mode: "named", contextID: "other", wantContext: "NAMED_CONTEXT"},
		{name: "none", mode: "none", wantContext: ""},
		{name: "trigger", mode: "trigger", wantContext: "TRIGGER_CONTEXT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen providers.Request
			provider := &scriptedProvider{run: func(_ context.Context, request providers.Request, _ string, events chan<- providers.Event) error {
				seen = request
				events <- providers.Event{Type: providers.EventDelta, Text: "answer"}
				return nil
			}}
			engine, cfg := testEngine(t, 1, provider)
			stage := &engine.cfg.Pipelines[0].Nodes[1]
			stage.ContextMode = tt.mode
			stage.ContextID = tt.contextID
			stage.ModelPool.Prompt.User = "context={{context.text}}"
			_, err := engine.Execute(context.Background(), cfg.ActivePipelineID, Input{
				ImageJPEG:   []byte("PINNED_FRAME"),
				ImagePinned: true,
				ContextText: "TRIGGER_CONTEXT",
				ContextViews: map[string]ContextView{
					"other": {Text: "NAMED_CONTEXT", ImageJPEG: []byte("OTHER_FRAME")},
				},
			}, RunOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if seen.UserPrompt != "context="+tt.wantContext {
				t.Fatalf("context policy sent %q", seen.UserPrompt)
			}
			if string(seen.ImageJPEG) != "PINNED_FRAME" {
				t.Fatalf("context policy replaced pinned event frame: %q", seen.ImageJPEG)
			}
		})
	}
}

func TestContextIDsForRouteIncludesOnlyReachableNamedStages(t *testing.T) {
	pipeline := domain.Pipeline{
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeScreen},
			{ID: "decision", Type: domain.NodeDecision, ContextMode: "named", ContextID: "decision-context"},
			{ID: "answer", Type: domain.NodeModelPool, ContextMode: "named", ContextID: "answer-context"},
			{ID: "unused", Type: domain.NodeModelPool, ContextMode: "named", ContextID: "unused-context"},
		},
		Edges: []domain.Edge{
			{ID: "a", From: "start", To: "decision", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1},
			{ID: "b", From: "decision", To: "answer", On: []domain.Outcome{domain.OutcomeRespond}, MaxVisits: 1},
		},
	}
	got := ContextIDsForRoute(pipeline, "start", domain.IntentRespond)
	if len(got) != 1 || got[0] != "answer-context" {
		t.Fatalf("manual respond context dependencies=%v", got)
	}
}
