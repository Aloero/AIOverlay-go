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
	plan := engine.PlanPool(cfg, a.catalog, pool, image)
	if pipelineID == "" {
		pipelineID = cfg.ActivePipelineID
	}
	a.mu.RLock()
	e := a.engine
	a.mu.RUnlock()
	plan.Candidates = e.PreviewOrder(pipelineID+"/"+nodeID, pool, plan.Candidates)
	return plan
}
