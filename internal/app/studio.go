package app

import (
	"aioverlay/internal/buildinfo"
	"aioverlay/internal/config"
	"aioverlay/internal/contracts"
	"aioverlay/internal/engine"
	"context"
	"fmt"
)

func (a *App) StudioState() contracts.StudioState {
	a.mu.RLock()
	id := a.cfg.ActivePipelineID
	e := a.engine
	recent := mergeRecent(a.recentModels, a.cfg.ControlCenter.RecentModels)
	a.mu.RUnlock()
	return contracts.StudioState{RecentModels: recent, ActivePipelineID: id, Revision: e.ConfigRevision(), Credentials: e.CredentialStates(), Version: buildinfo.Version}
}
func (a *App) Simulate(ctx context.Context, req engine.SimulationRequest) (engine.SimulationReport, error) {
	if req.Config.Version == 0 {
		req.Config = a.GetConfig()
	}
	if err := config.Migrate(&req.Config); err != nil {
		return engine.SimulationReport{}, err
	}
	if err := config.Validate(req.Config); err != nil {
		return engine.SimulationReport{}, err
	}
	a.mu.RLock()
	e := a.engine
	a.mu.RUnlock()
	req.Config.RuntimeStatistics = e.ExportStatistics()
	req.InitialCredentials = e.CredentialStates()
	return engine.Simulate(ctx, req, a.catalog)
}
func (a *App) ProbeConnection(ctx context.Context, req engine.ConnectionProbe, opt engine.RunOptions) (engine.Result, error) {
	if !a.operation.TryRLock() {
		return engine.Result{}, fmt.Errorf("settings are being applied")
	}
	defer a.operation.RUnlock()
	a.mu.RLock()
	e := a.engine
	a.mu.RUnlock()
	result, err := e.Probe(ctx, req, opt)
	a.recordRecentUsage(result.Trace)
	return result, err
}
