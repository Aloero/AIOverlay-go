package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"aioverlay/internal/catalog"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	tracestore "aioverlay/internal/trace"
)

type Input struct {
	ImageJPEG []byte
	Text      string
}

type RunOptions struct {
	OnDelta func(string)
	OnReset func()
	OnTrace func(domain.TraceEvent)
}

type Result struct {
	Text  string
	Trace domain.ExecutionTrace
}

type Engine struct {
	cfg      domain.Config
	registry *providers.Registry
	catalog  *catalog.Service
	traces   *tracestore.Store
	configMu sync.RWMutex
	mu       sync.Mutex
	rr       map[string]int
	lastGood map[string]string
	cooldown map[string]time.Time
	stats    map[string]modelStat
}

func New(cfg domain.Config, registry *providers.Registry, cat *catalog.Service, traces *tracestore.Store) *Engine {
	if traces == nil {
		traces = tracestore.New(100)
	}
	return &Engine{
		cfg: cfg, registry: registry, catalog: cat, traces: traces,
		rr: map[string]int{}, lastGood: map[string]string{},
		cooldown: map[string]time.Time{}, stats: map[string]modelStat{},
	}
}

func (e *Engine) Traces() *tracestore.Store { return e.traces }

func (e *Engine) Execute(ctx context.Context, pipelineID string, input Input, opt RunOptions) (Result, error) {
	e.configMu.RLock()
	defer e.configMu.RUnlock()
	p, ok := findPipeline(e.cfg, pipelineID)
	if !ok {
		return Result{}, fmt.Errorf("pipeline %q not found", pipelineID)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.Budget.MaxWallTimeMS)*time.Millisecond)
	defer cancel()
	t := domain.ExecutionTrace{ID: newID("trace"), PipelineID: p.ID, StartedAt: time.Now()}
	r := newRunner(e, p, input, opt, &t)
	text, err := r.run(ctx, p.EntryNodeID, nodeValue{image: input.ImageJPEG, text: input.Text})
	cancel()
	r.workers.Wait()
	t.FinishedAt = time.Now()
	if t.Winner != nil {
		t.Winner = r.redact.event(domain.TraceEvent{Attempt: t.Winner}).Attempt
	}
	t.PipelineID = r.redact.text(t.PipelineID)
	e.traces.Add(t)
	return Result{Text: text, Trace: domain.CloneTrace(t)}, e.safeError(err)
}
