package engine

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"aioverlay/internal/catalog"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	tracestore "aioverlay/internal/trace"
)

type Input struct {
	Audio                                  *domain.AudioClip
	AudioText, ContextText, VisionText     string
	EntryNodeID, Intent, SourceID, EventID string

	ImageJPEG []byte
	Text      string
}

type RunOptions struct {
	Capture    func(context.Context) ([]byte, error)
	BeforeCall func() error

	OnPrompt func(PromptSnapshot)
	OnStart  func(domain.ExecutionTrace)
	Purpose  string
	OnDelta  func(string)
	OnReset  func()
	OnTrace  func(domain.TraceEvent)
}

type Result struct {
	Observations []Observation
	Published    bool

	Text  string
	Trace domain.ExecutionTrace
}

type Engine struct {
	random            *rand.Rand
	cfg               domain.Config
	registry          *providers.Registry
	catalog           *catalog.Service
	traces            *tracestore.Store
	configMu          sync.RWMutex
	mu                sync.Mutex
	rr                map[string]int
	lastGood          map[string]string
	cooldown          map[string]time.Time
	stats             map[string]modelStat
	credentialResults map[string]CredentialState
	revision          uint64
}

func New(cfg domain.Config, registry *providers.Registry, cat *catalog.Service, traces *tracestore.Store) *Engine {
	if traces == nil {
		traces = tracestore.New(100)
	}
	return &Engine{random: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
		cfg: cfg, registry: registry, catalog: cat, traces: traces,
		rr: map[string]int{}, lastGood: map[string]string{},
		cooldown: map[string]time.Time{}, stats: map[string]modelStat{}, credentialResults: map[string]CredentialState{}, revision: 1,
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
	t := domain.ExecutionTrace{ID: newID("trace"), PipelineID: p.ID, PipelineName: p.Name, ConfigRevision: e.revision, Budget: p.Budget, Purpose: "pipeline", StartedAt: time.Now()}
	if opt.Purpose != "" {
		t.Purpose = opt.Purpose
	}
	t.Budget.Extra = nil
	t.PipelineName = (redactor{e.secrets()}).text(t.PipelineName)
	if opt.OnStart != nil {
		opt.OnStart(domain.CloneTrace(t))
	}
	r := newRunner(e, p, input, opt, &t)
	entry := p.EntryNodeID
	if input.EntryNodeID != "" {
		entry = input.EntryNodeID
	}
	t.SourceID = input.SourceID
	t.EventID = input.EventID
	t.Intent = input.Intent
	text, err := r.run(ctx, entry, nodeValue{image: input.ImageJPEG, audio: input.Audio, transcript: input.AudioText, vision: input.VisionText, text: input.Text})
	terminalCause := context.Cause(ctx)
	cancel()
	r.workers.Wait()
	for _, event := range poolCompletionEvents(t.Events) {
		r.emit(event)
	}
	finishTrace(&t, err, terminalCause, r.lastNode, r.lastOutcome, r.redact)
	if err == nil && r.waited {
		t.Outcome = domain.OutcomeWait
		t.Winner = nil
	}
	t.FinishedAt = time.Now()
	if t.Winner != nil {
		t.Winner = r.redact.event(domain.TraceEvent{Attempt: t.Winner}).Attempt
	}
	t.PipelineID = r.redact.text(t.PipelineID)
	e.traces.Add(t)
	return Result{Text: text, Trace: domain.CloneTrace(t), Published: r.published, Observations: r.observations}, e.safeError(err)
}
