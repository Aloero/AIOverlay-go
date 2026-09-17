package app

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
)

func (a *App) ProviderDirectory() []domain.ProviderMeta { return a.catalog.Directory() }
func (a *App) PreviewPool(cfg domain.Config, pool domain.ModelPoolConfig, image bool) engine.PoolPlan {
	return engine.PlanPool(cfg, a.catalog, pool, image)
}

func (a *App) PreviewOrderedPool(cfg domain.Config, pool domain.ModelPoolConfig, image bool, pipelineID, nodeID string) engine.PoolPlan {
	if pipelineID == "" {
		pipelineID = cfg.ActivePipelineID
	}
	var input engine.PoolInput
	for _, p := range cfg.Pipelines {
		if p.ID == pipelineID {
			input = engine.DescribePoolInput(p, nodeID)
			break
		}
	}
	if input.Known {
		image = input.ImageRequired
	}
	plan := engine.PlanPool(cfg, a.catalog, pool, image)
	plan.Input = input
	if input.Known && !input.Reachable {
		plan.Candidates = []domain.ModelSlot{}
		plan.Skipped = map[string]int{"unreachable": 1}
	}
	a.mu.RLock()
	e := a.engine
	a.mu.RUnlock()
	plan.Candidates = e.PreviewOrder(pipelineID+"/"+nodeID, pool, plan.Candidates)
	return plan
}
