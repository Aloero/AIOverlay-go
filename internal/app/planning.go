package app

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
)

func (a *App) ProviderDirectory() []domain.ProviderMeta { return a.catalog.Directory() }
func (a *App) PreviewPool(cfg domain.Config, pool domain.ModelPoolConfig, image bool) engine.PoolPlan {
	return engine.PlanPool(cfg, a.catalog, pool, image)
}
