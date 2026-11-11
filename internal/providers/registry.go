package providers

import (
	"context"
	"fmt"
	"sync"

	"aioverlay/internal/domain"
)

type Registry struct {
	mu    sync.RWMutex
	items map[string]Provider
	// media holds each adapter's own capability declaration, so the scheduler and Studio report
	// what the built transport can carry instead of what its kind name suggests.
	media map[string]MediaCapabilities
}

func NewRegistry(cfg domain.Config, resolver ...ProxyResolver) (*Registry, error) {
	var proxy ProxyResolver
	if len(resolver) > 0 {
		proxy = resolver[0]
	}
	return NewRegistryWith(cfg, Deps{Proxy: proxy})
}

// NewRegistryWith builds every enabled provider through the registered factories. Adding a runtime
// host or a protocol means registering a Factory; this function has no knowledge of kinds.
func NewRegistryWith(cfg domain.Config, deps Deps) (*Registry, error) {
	r := &Registry{items: map[string]Provider{}, media: map[string]MediaCapabilities{}}
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		factory, ok := FactoryFor(p.Kind)
		if !ok {
			return nil, fmt.Errorf("provider %s: unsupported kind %s", p.ID, p.Kind)
		}
		impl, caps, err := factory.New(p, deps)
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", p.ID, err)
		}
		r.items[p.ID] = impl
		r.media[p.ID] = caps
	}
	return r, nil
}

// Media returns the declared capabilities of a registered provider, falling back to the config
// table when the adapter is not registered (pure planners run without a registry).
func (r *Registry) Media(cfg domain.ProviderConfig) MediaCapabilities {
	if r == nil {
		return Media(cfg)
	}
	r.mu.RLock()
	caps, ok := r.media[cfg.ID]
	r.mu.RUnlock()
	if ok {
		return caps
	}
	return Media(cfg)
}

// Get returns a registered adapter. A nil or empty registry means "no transport", which callers
// must handle: Studio simulations and headless tests deliberately build an engine with none.
func (r *Registry) Get(id string) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.items[id]
	return p, ok
}

// Set replaces an adapter. Callers that know its capabilities pass them; otherwise the config table
// is used, which is the same answer a planner without a registry would compute.
func (r *Registry) Set(id string, p Provider, caps ...MediaCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[id] = p
	if len(caps) > 0 {
		r.media[id] = caps[0]
	} else {
		delete(r.media, id)
	}
}

func (r *Registry) Capabilities(cfg domain.Config) []AdapterCapabilities {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]AdapterCapabilities, 0, len(cfg.Providers))
	for _, providerConfig := range cfg.Providers {
		adapter := r.items[providerConfig.ID]
		result = append(result, adapterCapabilities(providerConfig, adapter, r.media[providerConfig.ID]))
	}
	return result
}

func (r *Registry) OpenRealtime(ctx context.Context, request RealtimeOpenRequest) (RealtimeSession, error) {
	if r == nil {
		return nil, fmt.Errorf("provider %s is not registered", request.ProviderID)
	}
	r.mu.RLock()
	adapter := r.items[request.ProviderID]
	r.mu.RUnlock()
	if adapter == nil {
		return nil, fmt.Errorf("provider %s is not registered", request.ProviderID)
	}
	return openRealtime(ctx, adapter, request)
}

func (r *Registry) SetBackgroundPaused(paused bool) {
	if r == nil {
		return
	}
	r.mu.RLock()
	listeners := make([]interface{ SetBackgroundPaused(bool) }, 0, len(r.items))
	for _, provider := range r.items {
		if listener, ok := provider.(interface{ SetBackgroundPaused(bool) }); ok {
			listeners = append(listeners, listener)
		}
	}
	r.mu.RUnlock()
	for _, listener := range listeners {
		listener.SetBackgroundPaused(paused)
	}
}

func (r *Registry) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.items {
		if c, ok := p.(interface{ Close() }); ok {
			c.Close()
		}
	}
	r.items = map[string]Provider{}
	r.media = map[string]MediaCapabilities{}
}
