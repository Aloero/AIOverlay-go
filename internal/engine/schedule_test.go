package engine

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	"context"
	"sync"
	"testing"
)

func TestIndependentRoundRobinAndParallelDispatch(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	script := &scriptedProvider{run: func(ctx context.Context, req providers.Request, key string, ch chan<- providers.Event) error {
		mu.Lock()
		seen[req.ModelID]++
		mu.Unlock()
		ch <- providers.Event{Type: providers.EventDelta, Text: "answer"}
		return nil
	}}
	e, cfg := testEngine(t, 3, script)
	p := cfg.Pipelines[0].Nodes[1].ModelPool
	p.Order = domain.PoolRoundRobin
	p.Dispatch = domain.DispatchParallel
	p.MaxParallel = 1 // global/pool cap still wins over a parallel strategy
	cfg.Pipelines[0].Budget.MaxParallelCalls = 1
	e.Reconfigure(cfg, e.registry)
	for i := 0; i < 3; i++ {
		if _, err := e.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"0", "1", "2"} {
		if seen[id] != 1 {
			t.Fatalf("independent ordering lost: %v", seen)
		}
	}
}
func TestExplicitDispatchOverridesLegacyPolicy(t *testing.T) {
	p := domain.ModelPoolConfig{Policy: domain.PoolParallel, Order: domain.PoolWeighted, Dispatch: domain.DispatchSequential}
	if p.EffectiveOrder() != domain.PoolWeighted || p.EffectiveDispatch() != domain.DispatchSequential {
		t.Fatal(p)
	}
}

func TestOrderPreviewDoesNotConsumeRoundRobin(t *testing.T) {
	e, cfg := testEngine(t, 3, &scriptedProvider{})
	p := cfg.Pipelines[0].Nodes[1].ModelPool
	p.Order = domain.PoolRoundRobin
	key := cfg.ActivePipelineID + "/models"
	e.rr["model:"+key] = 1
	for i := 0; i < 3; i++ {
		got := e.PreviewOrder(key, *p, p.Models)
		if got[0].ID != "1" {
			t.Fatal(got)
		}
	}
	if e.rr["model:"+key] != 1 {
		t.Fatal("preview consumed live cursor")
	}
}
