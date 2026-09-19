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
}

func NewRegistry(cfg domain.Config, resolver ...ProxyResolver) (*Registry, error) {
	var proxy ProxyResolver
	if len(resolver) > 0 {
		proxy = resolver[0]
	}
	r := &Registry{items: map[string]Provider{}}
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		var impl Provider
		switch p.Kind {
		case domain.ProviderOpenAICompatible:
			impl = NewOpenAI(p, proxy)
		case domain.ProviderTranscription:
			impl = NewTranscription(p, proxy)
		case domain.ProviderLocalSTT:
			impl = NewLocalSTT(p)
		case domain.ProviderGemini:
			impl = NewGemini(p, proxy)
		case domain.ProviderEmbeddedOpenCode:
			impl = NewEmbeddedOpenCode(p, proxy)
		case domain.ProviderOpenAILive:
			impl = NewOpenAILive(p, proxy)
		default:
			return nil, fmt.Errorf("provider %s: unsupported kind %s", p.ID, p.Kind)
		}
		r.items[p.ID] = impl
	}
	return r, nil
}

func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.items[id]
	return p, ok
}
func (r *Registry) Set(id string, p Provider) { r.mu.Lock(); defer r.mu.Unlock(); r.items[id] = p }

func (r *Registry) Capabilities(cfg domain.Config) []AdapterCapabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]AdapterCapabilities, 0, len(cfg.Providers))
	for _, providerConfig := range cfg.Providers {
		adapter := r.items[providerConfig.ID]
		result = append(result, adapterCapabilities(providerConfig, adapter))
	}
	return result
}

func (r *Registry) OpenRealtime(ctx context.Context, request RealtimeOpenRequest) (RealtimeSession, error) {
	r.mu.RLock()
	adapter := r.items[request.ProviderID]
	r.mu.RUnlock()
	if adapter == nil {
		return nil, fmt.Errorf("provider %s is not registered", request.ProviderID)
	}
	return openRealtime(ctx, adapter, request)
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.items {
		if c, ok := p.(interface{ Close() }); ok {
			c.Close()
		}
	}
	r.items = map[string]Provider{}
}
