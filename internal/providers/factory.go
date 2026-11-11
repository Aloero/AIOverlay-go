package providers

import (
	"fmt"
	"sync"

	"aioverlay/internal/domain"
	"aioverlay/internal/modelhost"
)

// Deps are the application-scoped services an adapter may need. This is the only place new
// dependencies enter the provider layer, so adapter constructors keep one signature and a host
// implementation can be substituted without editing the scheduler.
type Deps struct {
	// Proxy is the optional AI-VPN egress resolver. nil means ordinary networking plus the
	// adapters' own IPv4 fallback.
	Proxy ProxyResolver
	// HostFactory builds the model host for a hosted kind. Production leaves it nil and the kind
	// uses its bundled host; tests and a replacement runtime (another local engine, a supervised
	// service) inject their own here. This is the only seam a host swap needs.
	HostFactory func(cfg domain.ProviderConfig) (modelhost.Host, error)
}

// Factory registers one provider kind: how to construct its adapter and what that adapter can
// actually carry.
type Factory struct {
	Kind domain.ProviderKind
	// New returns the adapter and its own capability declaration. The Studio capability table
	// prefers this over the config-level table, because the adapter is the thing that will speak
	// the protocol and can inspect its own configuration.
	New func(cfg domain.ProviderConfig, deps Deps) (Provider, MediaCapabilities, error)
}

var (
	factoryMu sync.RWMutex
	factories = map[domain.ProviderKind]Factory{}
)

// Register adds a factory. Duplicate or malformed registration is a programming error, so it fails
// at init time where it is visible instead of at the first request.
func Register(f Factory) {
	if f.Kind == "" || f.New == nil {
		panic("providers: Register requires Kind and New")
	}
	factoryMu.Lock()
	defer factoryMu.Unlock()
	if _, exists := factories[f.Kind]; exists {
		panic(fmt.Sprintf("providers: kind %s is registered twice", f.Kind))
	}
	factories[f.Kind] = f
}

// FactoryFor returns the factory registered for a kind.
func FactoryFor(kind domain.ProviderKind) (Factory, bool) {
	factoryMu.RLock()
	defer factoryMu.RUnlock()
	f, ok := factories[kind]
	return f, ok
}

// RegisteredKinds lists the kinds the built adapters cover. A test compares it against the domain
// policy table so the two registries cannot drift apart.
func RegisteredKinds() []domain.ProviderKind {
	factoryMu.RLock()
	defer factoryMu.RUnlock()
	out := make([]domain.ProviderKind, 0, len(factories))
	for kind := range factories {
		out = append(out, kind)
	}
	return out
}

// host resolves the model host for a hosted kind: an injected one wins, otherwise the kind's own
// bundled default. This is the seam that lets the embedded runtime be replaced without touching a
// protocol adapter or the scheduler.
func (d Deps) host(cfg domain.ProviderConfig, fallback func() modelhost.Host) modelhost.Host {
	if d.HostFactory != nil {
		if host, err := d.HostFactory(cfg); err == nil && host != nil {
			return host
		}
	}
	return fallback()
}
