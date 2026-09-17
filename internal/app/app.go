package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"aioverlay/internal/catalog"
	"aioverlay/internal/config"
	"aioverlay/internal/control"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"aioverlay/internal/providers"
	"aioverlay/internal/session"
	tracestore "aioverlay/internal/trace"
	"aioverlay/internal/tunnel"
)

type App struct {
	recentModels      []string
	recentChanged     bool
	operation         sync.RWMutex
	mu                sync.RWMutex
	store             *config.Store
	cfg               domain.Config
	catalog           *catalog.Service
	registry          *providers.Registry
	tunnel            *tunnel.Manager
	engine            *engine.Engine
	traces            *tracestore.Store
	sessions          *session.Manager
	onConfigChanged   func(domain.Config)
	beforeConfigSave  func(domain.Config) (func(), error)
	maintenanceCancel context.CancelFunc
	maintenanceDone   chan struct{}
}

func New(configPath, catalogPath string) (*App, error) {
	store := config.NewStore(configPath)
	cfg, err := store.LoadOrCreate()
	if err != nil {
		return nil, err
	}
	cat, err := catalog.New(catalogPath)
	if err != nil {
		return nil, err
	}
	traces := tracestore.New(200)
	tun := tunnel.New(cfg.VPN)
	reg, err := providers.NewRegistry(cfg, tun)
	if err != nil {
		tun.Close()
		return nil, err
	}
	eng := engine.New(cfg, reg, cat, traces)
	if cfg.App.PersistRuntimeStats {
		eng.ImportStatistics(cfg.RuntimeStatistics)
	}
	a := &App{recentModels: append([]string(nil), cfg.ControlCenter.RecentModels...), store: store, cfg: cfg, catalog: cat, registry: reg, tunnel: tun, engine: eng, traces: traces}
	a.sessions = session.NewManager(func(ctx context.Context, pipelineID string, input engine.Input, opt engine.RunOptions) (engine.Result, error) {
		return a.Run(ctx, pipelineID, input, opt)
	})
	a.startMaintenance()
	return a, nil
}
func (a *App) BeforeConfigSave(fn func(domain.Config) (func(), error)) {
	a.mu.Lock()
	a.beforeConfigSave = fn
	a.mu.Unlock()
}
func (a *App) OnConfigChanged(fn func(domain.Config)) {
	a.mu.Lock()
	a.onConfigChanged = fn
	a.mu.Unlock()
}
func (a *App) Close() {
	if a.sessions != nil {
		a.sessions.Close()
	}
	if a.maintenanceCancel != nil {
		a.maintenanceCancel()
		<-a.maintenanceDone
	}
	a.operation.Lock()
	defer a.operation.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.App.PersistRuntimeStats || a.recentChanged {
		if a.cfg.App.PersistRuntimeStats {
			a.cfg.RuntimeStatistics = a.engine.ExportStatistics()
		}
		a.cfg.ControlCenter.RecentModels = mergeRecent(a.recentModels, a.cfg.ControlCenter.RecentModels)
		if err := a.store.Save(a.cfg); err != nil {
			log.Printf("runtime statistics save failed: %v", err)
		}
	}
	if a.registry != nil {
		a.registry.Close()
	}
	if a.tunnel != nil {
		a.tunnel.Close()
	}
}
func (a *App) GetConfig() domain.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneConfig(a.cfg)
}
func (a *App) SaveConfig(cfg domain.Config) error {
	return a.saveConfig(cfg, false)
}

// ImportConfig applies a portable config without inheriting Start-now source
// overrides from the previously loaded config. This is separate from a normal
// save because editing an existing source must not unexpectedly pause it.
func (a *App) ImportConfig(cfg domain.Config) error {
	return a.saveConfig(cfg, true)
}

func (a *App) saveConfig(cfg domain.Config, imported bool) error {
	var thaw func()
	if a.sessions != nil {
		thaw = a.sessions.FreezeTurns()
		defer thaw()
	}
	if !a.operation.TryLock() {
		return fmt.Errorf("analysis is running; stop it before saving settings")
	}
	defer a.operation.Unlock()
	return a.saveConfigLockedWithSessionPolicy(cfg, imported)
}

// Caller owns the operation write lock.
func (a *App) saveConfigLocked(cfg domain.Config) error {
	return a.saveConfigLockedWithSessionPolicy(cfg, false)
}

// Caller owns the operation write lock.
func (a *App) saveConfigLockedWithSessionPolicy(cfg domain.Config, imported bool) error {
	if err := config.Migrate(&cfg); err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	a.mu.RLock()
	previous := a.cfg
	cfg.ControlCenter.RecentModels = mergeRecent(a.recentModels, cfg.ControlCenter.RecentModels)
	reg, tun, eng := a.registry, a.tunnel, a.engine
	prepare, callback := a.beforeConfigSave, a.onConfigChanged
	a.mu.RUnlock()
	networkChanged := !sameConnections(previous.Providers, cfg.Providers) || !sameTunnel(previous.VPN, cfg.VPN)
	var err error
	if networkChanged {
		if err = tunnel.Validate(context.Background(), cfg.VPN); err != nil {
			return err
		}
		tun = tunnel.New(cfg.VPN)
		reg, err = providers.NewRegistry(cfg, tun)
		if err != nil {
			tun.Close()
			return err
		}
	}
	cleanupNew := func() {
		if networkChanged {
			reg.Close()
			tun.Close()
		}
	}
	var undo func()
	if prepare != nil {
		undo, err = prepare(cfg)
		if err != nil {
			cleanupNew()
			return err
		}
	}
	if cfg.App.PersistRuntimeStats {
		cfg.RuntimeStatistics = eng.ExportStatistics()
	}
	if err = a.store.Save(cfg); err != nil {
		if undo != nil {
			undo()
		}
		cleanupNew()
		return err
	}
	// Engine keeps policy cursors, health and stats, changing only the immutable
	// configuration view between runs. UI-only saves keep the transports too.
	eng.Reconfigure(cfg, reg)
	a.mu.Lock()
	oldReg, oldTun := a.registry, a.tunnel
	a.cfg = cfg
	a.recentModels = append([]string(nil), cfg.ControlCenter.RecentModels...)
	a.recentChanged = false
	a.registry = reg
	a.tunnel = tun
	a.mu.Unlock()
	if networkChanged {
		oldReg.Close()
		oldTun.Close()
	}
	if a.sessions != nil {
		if imported {
			a.sessions.ApplyImported(cfg)
		} else {
			a.sessions.Apply(cfg)
		}
	}
	if callback != nil {
		callback(cloneConfig(cfg))
	}
	return nil
}

func (a *App) Catalog(q control.CatalogQuery) (control.CatalogPage, error) {
	a.mu.RLock()
	cfg := cloneConfig(a.cfg)
	a.mu.RUnlock()
	items := a.catalog.Annotated(cfg)
	search := strings.ToLower(strings.TrimSpace(q.Search))
	providerFilter := strings.ToLower(strings.TrimSpace(q.Provider))
	out := make([]domain.ModelMeta, 0, 128)
	refs := map[string]int{}
	for i, ref := range q.References {
		refs[ref] = i
	}
	for _, m := range items {
		if q.ReferencesOnly {
			if _, ok := refs[m.ProviderID+"/"+m.ModelID]; !ok {
				continue
			}
		}
		if q.ProviderID != "" && m.ProviderID != q.ProviderID {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(m.ModelID+" "+m.ModelName+" "+m.ProviderID+" "+m.ProviderName+" "+m.Family), search) {
			continue
		}
		if providerFilter != "" && !strings.Contains(strings.ToLower(m.ProviderID+" "+m.ProviderName), providerFilter) {
			continue
		}
		if q.Vision && !m.HasImageInput() {
			continue
		}
		if q.Audio && !m.HasAudioInput() {
			continue
		}
		if q.Free && !m.Free {
			continue
		}
		if q.Reasoning && !m.Reasoning {
			continue
		}
		if q.Ready && !m.Ready {
			continue
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if q.ReferencesOnly {
			return refs[out[i].ProviderID+"/"+out[i].ModelID] < refs[out[j].ProviderID+"/"+out[j].ModelID]
		}
		if out[i].Ready != out[j].Ready {
			return out[i].Ready
		}
		if out[i].Free != out[j].Free {
			return out[i].Free
		}
		return catalog.HeuristicQuality(out[i].ModelID) > catalog.HeuristicQuality(out[j].ModelID)
	})
	total := len(out)
	off := q.Offset
	if off < 0 {
		off = 0
	}
	if off > total {
		off = total
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	end := off + limit
	if end > total {
		end = total
	}
	return control.CatalogPage{Items: out[off:end], Total: total, Offset: off, Limit: limit}, nil
}
func (a *App) RefreshCatalog() error                           { return a.catalog.Refresh() }
func (a *App) RefreshCatalogContext(ctx context.Context) error { return a.catalog.RefreshContext(ctx) }
func (a *App) CatalogStatus() any                              { return a.catalog.Status() }
func (a *App) RunTest(ctx context.Context, req control.TestRequest) (engine.Result, error) {
	var image []byte
	var err error
	if req.ImageBase64 != "" {
		raw := req.ImageBase64
		if i := strings.Index(raw, ","); i >= 0 {
			raw = raw[i+1:]
		}
		image, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return engine.Result{}, fmt.Errorf("decode image: %w", err)
		}
	}
	return a.Run(ctx, req.PipelineID, engine.Input{ImageJPEG: image, Text: req.Text}, engine.RunOptions{})
}
func (a *App) Run(ctx context.Context, pipelineID string, input engine.Input, opt engine.RunOptions) (engine.Result, error) {
	if !a.operation.TryRLock() {
		return engine.Result{}, fmt.Errorf("settings are being saved; try again")
	}
	defer a.operation.RUnlock()
	a.mu.RLock()
	eng := a.engine
	if pipelineID == "" {
		pipelineID = a.cfg.ActivePipelineID
	}
	a.mu.RUnlock()
	if opt.BeforeCall == nil && a.sessions != nil {
		high := opt.Purpose != "background"
		opt.BeforeCall = func(callCtx context.Context) (engine.CallLease, error) {
			return a.sessions.Governor().Acquire(callCtx, high)
		}
	}
	result, err := eng.Execute(ctx, pipelineID, input, opt)
	a.recordRecentUsage(result.Trace)
	return result, err
}

func (a *App) Traces() []domain.ExecutionTrace { return a.traces.List() }
func (a *App) ConfigPath() string              { return a.store.Path }

func cloneConfig(in domain.Config) domain.Config { // JSON clone avoids callers mutating live state.
	b, _ := jsonMarshal(in)
	var out domain.Config
	_ = jsonUnmarshal(b, &out)
	return out
}

// Isolated wrappers keep the rest of the package easy to test/migrate.
var jsonMarshal = func(v any) ([]byte, error) { return json.Marshal(v) }
var jsonUnmarshal = func(b []byte, v any) error { return json.Unmarshal(b, v) }

func (a *App) startMaintenance() {
	ctx, cancel := context.WithCancel(context.Background())
	a.maintenanceCancel = cancel
	a.maintenanceDone = make(chan struct{})
	go func() {
		defer close(a.maintenanceDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		last := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.mu.RLock()
				hours := a.cfg.App.CatalogRefreshHours
				a.mu.RUnlock()
				if hours > 0 && time.Since(last) >= time.Duration(hours)*time.Hour {
					last = time.Now()
					if err := a.catalog.RefreshContext(ctx); err != nil && ctx.Err() == nil {
						log.Printf("catalog refresh: %v", err)
					}
				}
			}
		}
	}()
}
