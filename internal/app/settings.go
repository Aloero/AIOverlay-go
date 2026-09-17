package app

import (
	"aioverlay/internal/domain"
	"fmt"
)

// Switching a saved pipeline must not tear down warm transports or reset model
// rotation and credential health. The runner receives the selected ID explicitly.
func (a *App) SetActivePipeline(id string) error {
	if !a.operation.TryLock() {
		return fmt.Errorf("analysis is running; stop it before changing the active pipeline")
	}
	defer a.operation.Unlock()
	cfg := a.GetConfig()
	found := false
	for _, p := range cfg.Pipelines {
		if p.ID == id && p.Enabled {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("pipeline is missing or disabled")
	}
	if cfg.ActivePipelineID == id {
		return nil
	}
	cfg.ActivePipelineID = id
	if err := a.store.Save(cfg); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return nil
}

// Provider-only changes are atomic against other saves and do not commit an
// incomplete graph draft submitted by the UI.
func (a *App) SaveProvider(p domain.ProviderConfig) error {
	if !a.operation.TryLock() {
		return fmt.Errorf("analysis is running; stop it before saving a connection")
	}
	defer a.operation.Unlock()
	cfg := a.GetConfig()
	found := false
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == p.ID {
			cfg.Providers[i] = p
			found = true
			break
		}
	}
	if !found {
		cfg.Providers = append(cfg.Providers, p)
	}
	return a.saveConfigLocked(cfg)
}
