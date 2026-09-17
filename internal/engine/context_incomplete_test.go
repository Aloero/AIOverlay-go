package engine

import (
	"context"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func TestContextIncompleteIsVisibleInExecutionTrace(t *testing.T) {
	cfg := config.Default()
	pipeline := &cfg.Pipelines[0]
	pool := pipeline.Nodes[1].ModelPool
	pool.Models = pool.Models[:1]

	registry, err := providers.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	registry.Set("opencode", &fakeProvider{id: "opencode", answer: "answer"})

	result, err := New(cfg, registry, nil, nil).Execute(context.Background(), pipeline.ID, Input{
		ImageJPEG:         []byte("frame"),
		ContextIncomplete: true,
		EntryNodeID:       pipeline.EntryNodeID,
		Intent:            string(domain.IntentRespond),
	}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}

	for _, event := range result.Trace.Events {
		if event.Kind == "context_incomplete" {
			return
		}
	}
	t.Fatal("manual transcript timeout was not preserved in the execution trace")
}
