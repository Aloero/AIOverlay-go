package app

import (
	"maps"
	"slices"

	"aioverlay/internal/contracts"
	"aioverlay/internal/domain"
	hermeshost "aioverlay/internal/modelhost/hermes"
)

// hermesProbe is created once per process: version detection launches a short child process and
// must not repeat on every settings-page request.
func (a *App) hermesProbe() *hermeshost.Host {
	a.hermesOnce.Do(func() { a.hermesHost = hermeshost.New(hermeshost.Config{}) })
	return a.hermesHost
}

// Universe assembles the model-settings surface from the same route verdicts the scheduler
// routes with. It adds no opinions of its own: "available" here means "a route says so", which
// is the guarantee that what the settings page shows cannot contradict what a run will do.
func (a *App) Universe() contracts.Universe {
	a.mu.RLock()
	cfg := cloneConfig(a.cfg)
	a.mu.RUnlock()

	universe := contracts.Universe{Snapshot: a.catalog.OfferedVersion()}
	byID := map[string]domain.ProviderConfig{}
	for _, p := range cfg.Providers {
		byID[p.ID] = p
	}

	// Applications and their live state.
	capabilities := a.registry.Capabilities(cfg)
	apps := []contracts.UniverseApp{{ID: string(domain.AppDirect), Label: "Direct API", Available: true}}
	ocVersion := ""
	for _, capability := range capabilities {
		if capability.HostVersion != "" {
			ocVersion = capability.HostVersion
			break
		}
	}
	apps = append(apps, contracts.UniverseApp{ID: string(domain.AppOpenCode), Label: "OpenCode", Available: a.catalog.RuntimePresent(), Version: ocVersion})
	probe := a.hermesProbe()
	hermesAvailable := false
	if _, err := probe.Executable(); err == nil {
		hermesAvailable = true
	}
	apps = append(apps, contracts.UniverseApp{ID: string(domain.AppHermes), Label: "Hermes", Available: hermesAvailable, Version: probe.Version()})

	// Models: every configured route, deduplicated per catalog reference, with the union of
	// applications that can carry it.
	type entry struct {
		model contracts.UniverseModel
		ready bool
	}
	entries := map[string]*entry{}
	providerApps := map[string]map[string]bool{}
	for _, route := range a.catalog.Routes(cfg) {
		ref := route.Model.ProviderID + "/" + route.Model.ModelID
		appID := string(domain.AppDirect)
		if provider, ok := byID[route.ConfigID]; ok {
			appID = string(provider.DeliveryApp())
		}
		target, found := entries[ref]
		if !found {
			target = &entry{model: contracts.UniverseModel{
				Ref: ref, ProviderID: route.Model.ProviderID, ModelID: route.Model.ModelID,
				InputModalities: route.Model.InputModalities, Free: route.Model.Free,
				Reasoning: route.Model.Reasoning, Context: route.Model.Context,
			}}
			entries[ref] = target
		}
		if route.Verdict.Ready && !target.ready {
			target.ready = true
			target.model.Reason = ""
		}
		if !target.ready {
			if target.model.Reason == "" {
				target.model.Reason = route.Verdict.Reason()
			}
		}
		if !slices.Contains(target.model.Apps, appID) {
			target.model.Apps = append(target.model.Apps, appID)
		}
		if providerApps[route.Model.ProviderID] == nil {
			providerApps[route.Model.ProviderID] = map[string]bool{}
		}
		providerApps[route.Model.ProviderID][appID] = true
	}

	directory := map[string]domain.ProviderMeta{}
	for _, meta := range a.catalog.Directory() {
		directory[meta.ID] = meta
	}
	registered := map[string]domain.ProviderConfig{}
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		id := p.CatalogProvider()
		// The settings page must list every registered provider even before its models appear
		// in any route - registration itself is a fact worth showing.
		if providerApps[id] == nil {
			providerApps[id] = map[string]bool{}
		}
		providerApps[id][string(p.DeliveryApp())] = true
		if current, seen := registered[id]; !seen || (current.Enabled && len(current.CredentialPool.Keys) == 0 && len(p.CredentialPool.Keys) > 0) {
			registered[id] = p
		}
	}
	providerIDs := slices.Sorted(maps.Keys(providerApps))
	for _, id := range providerIDs {
		meta := directory[id]
		appsFor := slices.Sorted(maps.Keys(providerApps[id]))
		provider := contracts.UniverseProvider{ID: id, Name: meta.Name, Models: meta.Models, Apps: appsFor}
		if meta.Name == "" {
			provider.Name = id
		}
		if p, ok := registered[id]; ok {
			provider.RegisteredVia = string(p.DeliveryApp())
			for _, key := range p.CredentialPool.Keys {
				if key.Enabled && key.Secret != "" {
					provider.Keyed = true
					break
				}
			}
		}
		universe.Providers = append(universe.Providers, provider)
	}
	for _, ref := range slices.Sorted(maps.Keys(entries)) {
		item := entries[ref]
		item.model.Ready = item.ready
		slices.Sort(item.model.Apps)
		universe.Models = append(universe.Models, item.model)
	}
	universe.Apps = apps
	return universe
}
