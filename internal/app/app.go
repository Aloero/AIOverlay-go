package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"aioverlay/internal/benchmark"
	"aioverlay/internal/catalog"
	"aioverlay/internal/config"
	"aioverlay/internal/control"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"aioverlay/internal/providers"
	tracestore "aioverlay/internal/trace"
	"aioverlay/internal/tunnel"
)

type App struct {
	operation       sync.RWMutex
	mu              sync.RWMutex
	store           *config.Store
	cfg             domain.Config
	catalog         *catalog.Service
	registry        *providers.Registry
	tunnel          *tunnel.Manager
	engine          *engine.Engine
	traces          *tracestore.Store
	onConfigChanged func(domain.Config)
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
	return &App{store: store, cfg: cfg, catalog: cat, registry: reg, tunnel: tun, engine: engine.New(cfg, reg, cat, traces), traces: traces}, nil
}
func (a *App) OnConfigChanged(fn func(domain.Config)) {
	a.mu.Lock()
	a.onConfigChanged = fn
	a.mu.Unlock()
}
func (a *App) Close() {
	a.operation.Lock()
	defer a.operation.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
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
	if !a.operation.TryLock() {
		return fmt.Errorf("analysis is running; stop it before saving settings")
	}
	defer a.operation.Unlock()
	return a.saveConfigLocked(cfg)
}

// Caller owns the operation write lock.
func (a *App) saveConfigLocked(cfg domain.Config) error {
	if err := config.Validate(cfg); err != nil {
		return err
	}
	if err := tunnel.Validate(context.Background(), cfg.VPN); err != nil {
		return err
	}
	tun := tunnel.New(cfg.VPN)
	reg, err := providers.NewRegistry(cfg, tun)
	if err != nil {
		tun.Close()
		return err
	}
	if err := a.store.Save(cfg); err != nil {
		reg.Close()
		tun.Close()
		return err
	}
	a.mu.Lock()
	old := a.registry
	oldTunnel := a.tunnel
	a.cfg = cfg
	a.registry = reg
	a.tunnel = tun
	a.engine = engine.New(cfg, reg, a.catalog, a.traces)
	callback := a.onConfigChanged
	a.mu.Unlock()
	if old != nil {
		old.Close()
	}
	if oldTunnel != nil {
		oldTunnel.Close()
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
	for _, m := range items {
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
func (a *App) RefreshCatalog() error { return a.catalog.Refresh() }
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
	return eng.Execute(ctx, pipelineID, input, opt)
}
func (a *App) RunBenchmark(ctx context.Context, req control.BenchmarkRequest) (control.BenchmarkReport, error) {
	img, err := benchmark.ImageJPEG()
	if err != nil {
		return control.BenchmarkReport{}, err
	}
	runs := req.Runs
	if runs <= 0 {
		runs = 3
	}
	if runs > 10 {
		runs = 10
	}
	report := control.BenchmarkReport{Total: runs}
	durations := make([]int64, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		res, runErr := a.Run(ctx, req.PipelineID, engine.Input{ImageJPEG: img}, engine.RunOptions{})
		d := time.Since(start).Milliseconds()
		row := control.BenchmarkRun{Run: i + 1, DurationMS: d, Text: res.Text, Winner: res.Trace.Winner}
		if runErr == nil {
			lower := strings.ToLower(res.Text)
			row.Correct = strings.Contains(lower, "q4") && strings.Contains(lower, "210")
			if row.Correct {
				report.Correct++
			}
		} else {
			row.Text = runErr.Error()
		}
		report.Runs = append(report.Runs, row)
		durations = append(durations, d)
		if ctx.Err() != nil {
			break
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if len(durations) > 0 {
		report.MedianMS = durations[len(durations)/2]
	}
	return report, nil
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
