package app

import (
	"sort"
	"time"

	"aioverlay/internal/contracts"
	"aioverlay/internal/domain"
)

// readinessObservationLimit bounds the history section. The report answers a question a person is
// asking now; the store keeps more, but a panel that lists ninety old refusals explains none of them.
const readinessObservationLimit = 20

// Readiness assembles the one "why is it not ready" answer from the verdicts the layers already
// hold: catalog connection verdicts, adapter capability declarations (with their author), the model
// references the pipelines actually use, and the watched outcomes. Nothing here decides readiness -
// an aggregator that invents its own opinion is how two answers start disagreeing again.
func (a *App) Readiness() contracts.ReadinessReport {
	a.mu.RLock()
	cfg := cloneConfig(a.cfg)
	a.mu.RUnlock()

	report := contracts.ReadinessReport{Connections: []contracts.ConnectionReadiness{}, Models: []contracts.ModelReadiness{}}
	st := a.catalog.Status()
	report.Catalog.Models, _ = st["models"].(int)
	report.Catalog.LastRefresh, _ = st["last_refresh"].(time.Time)
	if message, ok := st["error"].(string); ok {
		report.Catalog.LastError = message
	}
	report.Catalog.OfferedSnapshot = a.catalog.OfferedVersion()

	verdicts := a.catalog.ConnectionVerdicts(cfg)
	for _, capabilities := range a.registry.Capabilities(cfg) {
		verdict, found := verdicts[capabilities.ProviderID]
		if !found {
			verdict = domain.Ok(domain.ProvenanceConfig)
		}
		// A local runtime is usable only while its helper binary and model files exist; that fact
		// lives in the registry, not in any file the catalog reads, so it joins here rather than
		// pretending to be a catalog rule.
		if status := capabilities.LocalSTT; status != nil && verdict.Ready && !(status.HelperPresent && status.ModelPresent) {
			verdict = domain.NotReady(append(verdict.Blockers, domain.NewBlocker(domain.BlockRuntimeMissing, domain.ProvenanceHost,
				"local runtime %s is missing its helper or model files", capabilities.ProviderID))...)
		}
		if capabilities.HostVersion != "" && report.Catalog.RuntimeVersion == "" {
			report.Catalog.RuntimeVersion = capabilities.HostVersion
		}
		report.Connections = append(report.Connections, contracts.ConnectionReadiness{Adapter: capabilities, Verdict: verdict})
	}

	// Every model reference any pool actually names, with the pools that name it. Disabled slots are
	// skipped: their reason is "the operator turned them off", which is not a readiness question.
	usedBy := map[string][]string{}
	for _, pipeline := range cfg.Pipelines {
		for _, node := range pipeline.Nodes {
			if node.ModelPool == nil {
				continue
			}
			for _, slot := range node.ModelPool.Models {
				if !slot.Enabled || slot.ModelID == "" {
					continue
				}
				usedBy[slot.ModelID] = append(usedBy[slot.ModelID], pipeline.ID+"/"+node.ID)
			}
		}
	}
	refs := make([]string, 0, len(usedBy))
	for ref := range usedBy {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	poolVerdicts := a.catalog.VerdictsFor(cfg, refs)
	for _, ref := range refs {
		verdict := poolVerdicts[ref]
		report.Models = append(report.Models, contracts.ModelReadiness{
			Ref: ref, UsedBy: usedBy[ref], Verdict: verdict, Reason: verdict.Reason(),
		})
	}

	if observed := a.catalog.Observations(); len(observed) > readinessObservationLimit {
		report.Observations = observed[:readinessObservationLimit]
	} else {
		report.Observations = observed
	}
	return report
}
