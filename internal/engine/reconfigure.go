package engine

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	"math"
)

// Reconfigure serializes against Execute. Credential cooldown is reset only for
// a changed secret or an explicit disable/re-enable, not a font/layout save.
func (e *Engine) Reconfigure(cfg domain.Config, registry *providers.Registry) {
	e.configMu.Lock()
	defer e.configMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	before := map[string]domain.Credential{}
	for _, p := range e.cfg.Providers {
		for _, k := range p.CredentialPool.Keys {
			before[p.ID+"/"+k.ID] = k
		}
	}
	present := map[string]bool{}
	for _, p := range cfg.Providers {
		for _, k := range p.CredentialPool.Keys {
			id := p.ID + "/" + k.ID
			present[id] = true
			old, ok := before[id]
			if !ok || old.Secret != k.Secret || old.Enabled != k.Enabled {
				delete(e.cooldown, id)
				if e.lastGood[p.ID] == k.ID {
					delete(e.lastGood, p.ID)
				}
			}
		}
	}
	for id := range e.cooldown {
		if !present[id] {
			delete(e.cooldown, id)
		}
	}
	e.cfg = cfg
	e.registry = registry
}
func (e *Engine) ExportStatistics() *domain.RuntimeStatistics {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := &domain.RuntimeStatistics{Models: map[string]domain.ModelStatistics{}, Cursors: map[string]int{}}
	for k, s := range e.stats {
		out.Models[k] = domain.ModelStatistics{Success: s.Success, Failure: s.Failure, TTFTSamples: s.TTFTSamples, AvgTTFT: s.AvgTTFT, AvgDuration: s.AvgDuration}
	}
	for k, v := range e.rr {
		out.Cursors[k] = v
	}
	return out
}
func (e *Engine) ImportStatistics(s *domain.RuntimeStatistics) {
	if s == nil || len(s.Models) > 10000 || len(s.Cursors) > 10000 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, v := range s.Models {
		if v.Success < 0 || v.Failure < 0 || v.TTFTSamples < 0 || math.IsNaN(v.AvgTTFT) || math.IsNaN(v.AvgDuration) || math.IsInf(v.AvgTTFT, 0) || math.IsInf(v.AvgDuration, 0) || v.AvgTTFT < 0 || v.AvgDuration < 0 {
			continue
		}
		e.stats[k] = modelStat{Success: v.Success, Failure: v.Failure, TTFTSamples: v.TTFTSamples, AvgTTFT: v.AvgTTFT, AvgDuration: v.AvgDuration}
	}
	for k, v := range s.Cursors {
		if v >= 0 {
			e.rr[k] = v
		}
	}
}
