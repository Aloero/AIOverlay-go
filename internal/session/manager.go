package session

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	paudio "aioverlay/internal/platform/audio"
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

type Executor func(context.Context, string, engine.Input, engine.RunOptions) (engine.Result, error)
type ScreenCapture func(context.Context) ([]byte, error)

type Publication struct {
	ID          uint64    `json:"id"`
	PipelineID  string    `json:"pipeline_id"`
	SourceID    string    `json:"source_id"`
	EventID     string    `json:"event_id"`
	Text        string    `json:"text"`
	TriggeredAt time.Time `json:"triggered_at"`
	At          time.Time `json:"at"`
	Automatic   bool      `json:"automatic"`
}

type PreparedCommand struct {
	PipelineID string
	Command    domain.CommandConfig
	Input      engine.Input
	SkipRun    bool
}

type SourceStatus struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Kind            string  `json:"kind"`
	Running         bool    `json:"running"`
	Paused          bool    `json:"paused"`
	Level           float64 `json:"level,omitempty"`
	Segments        uint64  `json:"segments,omitempty"`
	Dropped         uint64  `json:"dropped,omitempty"`
	LastError       string  `json:"last_error,omitempty"`
	LastOutcome     string  `json:"last_outcome,omitempty"`
	TransportState  string  `json:"transport_state,omitempty"`
	SignalState     string  `json:"signal_state,omitempty"`
	LastEvent       string  `json:"last_event,omitempty"`
	LastTranscript  string  `json:"last_transcript,omitempty"`
	TranscriptLagMS int64   `json:"transcript_lag_ms,omitempty"`
}

type pendingSegment struct {
	pipelineID, contextID, eventID string
	generation                     uint64
	start, end                     time.Time
}
type backgroundTurn struct {
	pipelineID string
	ctxCfg     domain.ContextConfig
	generation uint64
	input      engine.Input
	at         time.Time
}

type activeBackgroundTurn struct {
	pipelineID string
	contextID  string
	generation uint64
	cancel     context.CancelFunc
}

type sourceRuntime struct {
	cfg           domain.EventSource
	cancel        context.CancelFunc
	done          chan struct{}
	turnDone      chan struct{}
	mu            sync.RWMutex
	status        SourceStatus
	epoch         uint64
	turns         chan backgroundTurn
	cooldownUntil time.Time
}

type Manager struct {
	mu                sync.RWMutex
	reconcileMu       sync.Mutex
	turnGate          sync.RWMutex
	cfg               domain.Config
	execute           Executor
	capture           ScreenCapture
	backgroundCapture ScreenCapture
	audioCapture      paudio.CaptureFunc
	contexts          *ContextStore
	governor          *Governor
	root              context.Context
	cancel            context.CancelFunc
	started           bool
	paused            bool
	sources           map[string]*sourceRuntime
	sourceOverrides   map[string]bool
	turnSem           chan struct{}
	publish           func(Publication)
	seq               atomic.Uint64
	eventSeq          atomic.Uint64
	sourceSeq         atomic.Uint64
	pendingMu         sync.Mutex
	pending           map[string]pendingSegment
	pendingChanged    chan struct{}
	activeTurnMu      sync.Mutex
	activeTurns       map[string]activeBackgroundTurn
}

func NewManager(execute Executor) *Manager {
	return &Manager{
		execute:         execute,
		audioCapture:    paudio.Capture,
		contexts:        NewContextStore(),
		governor:        NewGovernor(),
		sources:         map[string]*sourceRuntime{},
		sourceOverrides: map[string]bool{},
		turnSem:         make(chan struct{}, 3),
		pending:         map[string]pendingSegment{},
		pendingChanged:  make(chan struct{}),
		activeTurns:     map[string]activeBackgroundTurn{},
	}
}
func (m *Manager) Contexts() *ContextStore { return m.contexts }
func (m *Manager) FreezeTurns() func()     { m.turnGate.Lock(); return m.turnGate.Unlock }
func (m *Manager) Governor() *Governor     { return m.governor }
func (m *Manager) SetCapture(manual, background ScreenCapture) {
	m.mu.Lock()
	m.capture = manual
	m.backgroundCapture = background
	m.mu.Unlock()
}

func (m *Manager) backgroundScreenCapture() ScreenCapture {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.backgroundCapture
}
func (m *Manager) SetAudioCapture(fn paudio.CaptureFunc) {
	m.mu.Lock()
	if fn != nil {
		m.audioCapture = fn
	}
	m.mu.Unlock()
}
func (m *Manager) SetPublisher(fn func(Publication)) { m.mu.Lock(); m.publish = fn; m.mu.Unlock() }

func (m *Manager) Start(ctx context.Context, cfg domain.Config) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		m.Apply(cfg)
		return
	}
	m.root, m.cancel = context.WithCancel(ctx)
	m.started = true
	m.cfg = cfg
	m.configureBudgetLocked(cfg)
	m.mu.Unlock()
	m.reconcile()
}
func (m *Manager) Close() {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	cancel := m.cancel
	m.started = false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.stopAll()
}
func (m *Manager) Apply(cfg domain.Config) {
	m.mu.Lock()
	wantedIDs := map[string]bool{}
	if p := activePipeline(cfg); p != nil {
		for _, src := range p.Sources {
			wantedIDs[src.ID] = true
		}
	}
	for id := range m.sourceOverrides {
		if !wantedIDs[id] {
			delete(m.sourceOverrides, id)
		}
	}
	m.cfg = cfg
	m.configureBudgetLocked(cfg)
	started := m.started
	m.mu.Unlock()
	if started {
		m.reconcile()
	}
}
func (m *Manager) configureBudgetLocked(cfg domain.Config) {
	maxParallel, calls := 3, 60
	if p := activePipeline(cfg); p != nil {
		maxParallel = p.Budget.MaxParallelCalls
		if p.SessionBudget != nil {
			if p.SessionBudget.MaxParallelCalls > 0 {
				maxParallel = p.SessionBudget.MaxParallelCalls
			}
			if p.SessionBudget.CallsPerMinute > 0 {
				calls = p.SessionBudget.CallsPerMinute
			}
		}
	}
	m.governor.Configure(maxParallel, calls)
	capTurns := maxParallel
	if capTurns < 1 {
		capTurns = 1
	}
	if capTurns > 16 {
		capTurns = 16
	}
	if cap(m.turnSem) != capTurns {
		m.turnSem = make(chan struct{}, capTurns)
	}
}
func activePipeline(cfg domain.Config) *domain.Pipeline {
	for i := range cfg.Pipelines {
		if cfg.Pipelines[i].ID == cfg.ActivePipelineID {
			return &cfg.Pipelines[i]
		}
	}
	return nil
}
func activePipelineID(cfg domain.Config) string {
	if p := activePipeline(cfg); p != nil {
		return p.ID
	}
	return ""
}
func findContext(p *domain.Pipeline, id string) (domain.ContextConfig, bool) {
	if p == nil || id == "" {
		return domain.ContextConfig{}, false
	}
	for _, c := range p.Contexts {
		if c.ID == id && c.Enabled {
			return c, true
		}
	}
	return domain.ContextConfig{}, false
}
func findCommand(p *domain.Pipeline, action string) (domain.CommandConfig, bool) {
	if p == nil {
		return domain.CommandConfig{}, false
	}
	for _, c := range p.Commands {
		if c.Action == action {
			return c, true
		}
	}
	return domain.CommandConfig{}, false
}

func (m *Manager) Pause(paused bool) { m.mu.Lock(); m.paused = paused; m.mu.Unlock(); m.reconcile() }
func (m *Manager) Paused() bool      { m.mu.RLock(); defer m.mu.RUnlock(); return m.paused }

func (m *Manager) sourceWantedLocked(src domain.EventSource) bool {
	if v, ok := m.sourceOverrides[src.ID]; ok {
		return v
	}
	return src.AutoStart
}
func (m *Manager) SetSourceRunning(id string, running bool) error {
	m.mu.Lock()
	p := activePipeline(m.cfg)
	found := false
	if p != nil {
		for _, src := range p.Sources {
			if src.ID == id && src.Enabled {
				found = true
				break
			}
		}
	}
	if !found {
		m.mu.Unlock()
		return fmt.Errorf("enabled source %q not found", id)
	}
	m.sourceOverrides[id] = running
	m.mu.Unlock()
	m.reconcile()
	return nil
}
func (m *Manager) reconcile() {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	m.mu.RLock()
	started, root := m.started, m.root
	m.mu.RUnlock()
	if !started || root == nil {
		return
	}

	// First stop sources that are no longer desired. Wait outside the manager
	// mutex so COM/audio teardown cannot block status/config readers.
	m.mu.Lock()
	cfg, paused := m.cfg, m.paused
	p := activePipeline(cfg)
	wanted := map[string]domain.EventSource{}
	if p != nil && !paused {
		for _, src := range p.Sources {
			if src.Enabled && m.sourceWantedLocked(src) {
				wanted[src.ID] = src
			}
		}
	}
	var stopped []*sourceRuntime
	for id, runtime := range m.sources {
		next, wantedNow := wanted[id]
		if wantedNow && sameSource(runtime.cfg, next) && !sourceFinished(runtime) {
			continue
		}
		runtime.cancel()
		delete(m.sources, id)
		stopped = append(stopped, runtime)
	}
	m.mu.Unlock()
	for _, runtime := range stopped {
		waitForSourceStop(runtime, 2*time.Second)
	}

	// Config may have changed while native teardown completed. Recompute desired
	// sources before starting replacements; reconcileMu serializes all starters.
	m.mu.Lock()
	cfg, paused, root, started = m.cfg, m.paused, m.root, m.started
	p = activePipeline(cfg)
	wanted = map[string]domain.EventSource{}
	if started && root != nil && p != nil && !paused {
		for _, src := range p.Sources {
			if src.Enabled && m.sourceWantedLocked(src) {
				wanted[src.ID] = src
			}
		}
	}
	for id, src := range wanted {
		if _, ok := m.sources[id]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(root)
		r := &sourceRuntime{
			cfg:      src,
			cancel:   cancel,
			done:     make(chan struct{}),
			turnDone: make(chan struct{}),
			epoch:    m.sourceSeq.Add(1),
			turns:    make(chan backgroundTurn, 1),
			status: SourceStatus{
				ID: src.ID, Name: src.Name, Kind: string(src.Kind), Running: true,
				TransportState: initialTransportState(src.Kind), SignalState: initialSignalState(src.Kind),
			},
		}
		m.sources[id] = r
		go m.runSource(ctx, p.ID, r)
		go m.runTurnQueue(ctx, p.ID, r)
	}
	m.mu.Unlock()
}
func sameSource(a, b domain.EventSource) bool { return reflect.DeepEqual(a, b) }

func sourceFinished(runtime *sourceRuntime) bool {
	select {
	case <-runtime.done:
		return true
	default:
		return false
	}
}

func waitForSourceStop(runtime *sourceRuntime, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	waitForChannelUntil(runtime.done, deadline)
	waitForChannelUntil(runtime.turnDone, deadline)
}

func waitForChannelUntil(done <-chan struct{}, deadline time.Time) {
	if done == nil {
		return
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	list := make([]*sourceRuntime, 0, len(m.sources))
	for _, r := range m.sources {
		r.cancel()
		list = append(list, r)
	}
	m.sources = map[string]*sourceRuntime{}
	m.mu.Unlock()
	for _, runtime := range list {
		waitForSourceStop(runtime, 2*time.Second)
	}
}

func (m *Manager) runSource(ctx context.Context, pipelineID string, runtime *sourceRuntime) {
	defer close(runtime.done)
	defer close(runtime.turns)

	var err error
	switch runtime.cfg.Kind {
	case domain.SourceAudio:
		err = m.runAudio(ctx, pipelineID, runtime)
	case domain.SourceScreenTimer:
		err = m.runScreenTimer(ctx, pipelineID, runtime)
	default:
		err = fmt.Errorf("unsupported source kind %s", runtime.cfg.Kind)
	}
	finishSourceStatus(runtime, err, ctx.Err())
}

func initialTransportState(kind domain.SourceKind) string {
	if kind == domain.SourceAudio {
		return "starting"
	}
	return ""
}

func initialSignalState(kind domain.SourceKind) string {
	if kind == domain.SourceAudio {
		return "unknown"
	}
	return ""
}

func finishSourceStatus(runtime *sourceRuntime, runErr, contextErr error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.status.Running = false

	if runtime.cfg.Kind != domain.SourceAudio {
		if runErr != nil && contextErr == nil {
			runtime.status.LastOutcome = string(domain.OutcomeAnyError)
			runtime.status.LastError = runErr.Error()
		}
		return
	}
	if contextErr != nil {
		runtime.status.TransportState = "stopped"
		return
	}
	runtime.status.TransportState = "unavailable"
	runtime.status.LastOutcome = string(domain.OutcomeAnyError)
	if runErr != nil {
		runtime.status.LastError = runErr.Error()
	}
}

func updateAudioStatus(runtime *sourceRuntime, block paudio.Block, level float64) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.status.TransportState = "alive"
	runtime.status.Level = level
	switch {
	case len(block.Samples) == 0:
		runtime.status.SignalState = "no_packets"
	case block.Silent:
		runtime.status.SignalState = "silent"
	default:
		runtime.status.SignalState = "data"
	}
}

func (m *Manager) recordAudioGap(pipelineID string, r *sourceRuntime, at time.Time, reason string) {
	if at.IsZero() {
		at = time.Now()
	}
	r.mu.Lock()
	r.status.Dropped++
	r.status.LastError = reason
	r.mu.Unlock()
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if p := activePipeline(cfg); p != nil && p.ID == pipelineID {
		if c, ok := findContext(p, r.cfg.ContextID); ok {
			g := m.contexts.Generation(pipelineID, c.ID)
			m.contexts.Append(c, pipelineID, g, ContextEntry{Kind: "gap", Gap: true, At: at, SourceID: r.cfg.ID})
		}
	}
}

func (m *Manager) enqueueAudioSegment(pipelineID string, r *sourceRuntime, segments chan<- paudio.Segment, seg paudio.Segment) {
	select {
	case segments <- seg:
	default:
		m.recordAudioGap(pipelineID, r, seg.Ended, "audio queue overflow: transcript gap recorded")
	}
}

func (m *Manager) runAudio(ctx context.Context, pipelineID string, runtime *sourceRuntime) error {
	if runtime.cfg.Audio == nil {
		return fmt.Errorf("audio settings missing")
	}

	capture, queueCapacity, err := m.audioRuntimeConfig(pipelineID)
	if err != nil {
		return err
	}

	segments := make(chan paudio.Segment, queueCapacity)
	producerDone := make(chan error, 1)
	go func() {
		producerDone <- m.produceAudioSegments(ctx, pipelineID, runtime, capture, segments)
		close(segments)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case segment, ok := <-segments:
			if !ok {
				return captureResult(ctx, <-producerDone)
			}
			runtime.mu.Lock()
			runtime.status.Segments++
			runtime.status.LastEvent = segment.Ended.Format(time.RFC3339Nano)
			runtime.mu.Unlock()
			m.processAudioSegment(ctx, pipelineID, runtime, segment)
		}
	}
}

func (m *Manager) audioRuntimeConfig(pipelineID string) (paudio.CaptureFunc, int, error) {
	m.mu.RLock()
	capture := m.audioCapture
	cfg := m.cfg
	m.mu.RUnlock()
	if capture == nil {
		return nil, 0, fmt.Errorf("audio capture unavailable")
	}

	queueCapacity := 4
	pipeline := activePipeline(cfg)
	if pipeline == nil || pipeline.ID != pipelineID {
		return nil, 0, fmt.Errorf("active pipeline changed before audio source start")
	}
	if pipeline.SessionBudget != nil && pipeline.SessionBudget.QueueCapacity > 0 {
		queueCapacity = pipeline.SessionBudget.QueueCapacity
	}
	return capture, queueCapacity, nil
}

func (m *Manager) produceAudioSegments(ctx context.Context, pipelineID string, runtime *sourceRuntime, capture paudio.CaptureFunc, segments chan<- paudio.Segment) error {
	segmenter := paudio.Segmenter{Config: *runtime.cfg.Audio}
	err := capture(ctx, *runtime.cfg.Audio, func(block paudio.Block) {
		if block.Discontinuity || block.TimestampError {
			m.recordAudioGap(pipelineID, runtime, block.At, "audio discontinuity: transcript context contains a gap")
		}
		for _, segment := range segmenter.Push(block) {
			m.enqueueAudioSegment(pipelineID, runtime, segments, segment)
		}
		updateAudioStatus(runtime, block, segmenter.Level)
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}

	interruptedAt := time.Now()
	if tail, ok := segmenter.Interrupt(); ok {
		m.enqueueAudioSegment(pipelineID, runtime, segments, tail)
		interruptedAt = tail.Ended
	}
	m.recordAudioGap(pipelineID, runtime, interruptedAt, "audio source interrupted: transcript context contains a gap")
	if err != nil {
		return err
	}
	return fmt.Errorf("audio capture ended unexpectedly")
}

func captureResult(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (m *Manager) notifyPendingLocked() {
	close(m.pendingChanged)
	m.pendingChanged = make(chan struct{})
}
func (m *Manager) addPending(p pendingSegment) {
	if p.eventID == "" || p.contextID == "" {
		return
	}
	m.pendingMu.Lock()
	m.pending[p.eventID] = p
	m.notifyPendingLocked()
	m.pendingMu.Unlock()
}
func (m *Manager) finishPending(id string) {
	if id == "" {
		return
	}
	m.pendingMu.Lock()
	if _, ok := m.pending[id]; ok {
		delete(m.pending, id)
		m.notifyPendingLocked()
	}
	m.pendingMu.Unlock()
}
func (m *Manager) waitPending(ctx context.Context, pipelineID, contextID string, generation uint64, cutoff time.Time, limit time.Duration) (bool, error) {
	if contextID == "" || limit <= 0 {
		return true, nil
	}
	deadline, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for {
		m.pendingMu.Lock()
		waiting := false
		for _, p := range m.pending {
			if p.pipelineID == pipelineID && p.contextID == contextID && p.generation == generation && !p.start.After(cutoff) {
				waiting = true
				break
			}
		}
		changed := m.pendingChanged
		m.pendingMu.Unlock()
		if !waiting {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.Done():
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil
		case <-changed:
		}
	}
}

func (m *Manager) processAudioSegment(parent context.Context, pipelineID string, r *sourceRuntime, seg paudio.Segment) {
	if !m.sourceCurrent(r.cfg.ID, r.epoch) {
		return
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	p := activePipeline(cfg)
	if p == nil || p.ID != pipelineID {
		return
	}
	ctxCfg, hasCtx := findContext(p, r.cfg.ContextID)
	gen := uint64(0)
	snap := ContextSnapshot{}
	if hasCtx {
		gen = m.contexts.Generation(pipelineID, ctxCfg.ID)
		snap = m.contexts.Snapshot(ctxCfg, pipelineID, seg.Ended)
	}
	clip := seg.Clip
	clip.SegmentID = fmt.Sprintf("%s-%d", r.cfg.ID, m.eventSeq.Add(1))
	clip.StartedAt = seg.Started
	clip.EndedAt = seg.Ended
	intent := r.cfg.Intent
	if !clip.Final {
		intent = domain.IntentContext
	}
	r.mu.RLock()
	cool := r.cooldownUntil
	r.mu.RUnlock()
	if !cool.IsZero() && time.Now().Before(cool) && hasCtx {
		intent = domain.IntentContext
	}
	input := engine.Input{Audio: &clip, ContextText: snap.Text, ImageJPEG: snap.ImageJPEG, EntryNodeID: r.cfg.EntryNodeID, Intent: string(intent), SourceID: r.cfg.ID, EventID: clip.SegmentID}
	if hasCtx {
		m.addPending(pendingSegment{pipelineID: pipelineID, contextID: ctxCfg.ID, eventID: clip.SegmentID, generation: gen, start: seg.Started, end: seg.Ended})
	}
	m.enqueueBackgroundTurn(r, backgroundTurn{pipelineID: pipelineID, ctxCfg: ctxCfg, generation: gen, input: input, at: seg.Ended})
}
func (m *Manager) runScreenTimer(ctx context.Context, pipelineID string, r *sourceRuntime) error {
	interval := time.Duration(r.cfg.IntervalMS) * time.Millisecond
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	changeGate := newScreenChangeGate(r.cfg)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case at := <-ticker.C:
			capture := m.backgroundScreenCapture()
			m.mu.RLock()
			cfg := m.cfg
			m.mu.RUnlock()
			if capture == nil {
				r.mu.Lock()
				r.status.LastError = "background screen capture unavailable"
				r.mu.Unlock()
				continue
			}
			img, err := capture(ctx)
			if err != nil {
				r.mu.Lock()
				r.status.LastError = err.Error()
				r.mu.Unlock()
				continue
			}
			if !m.sourceCurrent(r.cfg.ID, r.epoch) {
				continue
			}
			if !changeGate.Accept(img, at) {
				continue
			}
			r.mu.Lock()
			r.status.LastEvent = at.Format(time.RFC3339Nano)
			r.mu.Unlock()
			p := activePipeline(cfg)
			if p == nil || p.ID != pipelineID {
				continue
			}
			ctxCfg, has := findContext(p, r.cfg.ContextID)
			gen := uint64(0)
			if has {
				gen = m.contexts.Generation(pipelineID, ctxCfg.ID)
				m.contexts.SetScreen(ctxCfg, pipelineID, gen, img, at)
				if r.cfg.Intent == domain.IntentContext {
					setSourceOutcome(r, domain.OutcomeContextOnly, nil)
					continue
				}
			}
			snap := ContextSnapshot{}
			if has {
				snap = m.contexts.Snapshot(ctxCfg, pipelineID, at)
			}
			input := engine.Input{ImageJPEG: append([]byte(nil), img...), ContextText: snap.Text, EntryNodeID: r.cfg.EntryNodeID, Intent: string(r.cfg.Intent), SourceID: r.cfg.ID, EventID: fmt.Sprintf("%s-%d", r.cfg.ID, m.eventSeq.Add(1))}
			m.enqueueBackgroundTurn(r, backgroundTurn{pipelineID: pipelineID, ctxCfg: ctxCfg, generation: gen, input: input, at: at})
		}
	}
}
func (m *Manager) enqueueBackgroundTurn(r *sourceRuntime, turn backgroundTurn) {
	if r == nil || !m.sourceCurrent(r.cfg.ID, r.epoch) {
		return
	}
	select {
	case r.turns <- turn:
	default:
		var old backgroundTurn
		select {
		case old = <-r.turns:
		default:
		}
		m.recordCoalescedTurn(r, old)
		select {
		case r.turns <- turn:
		default:
		}
	}
}

func (m *Manager) recordCoalescedTurn(runtime *sourceRuntime, turn backgroundTurn) {
	m.recordDroppedTurn(runtime, turn, "automatic event coalesced to the newest context")
}

func (m *Manager) recordDroppedTurn(runtime *sourceRuntime, turn backgroundTurn, reason string) {
	if turn.input.EventID != "" {
		if turn.input.Audio != nil && turn.ctxCfg.ID != "" {
			m.contexts.Append(turn.ctxCfg, turn.pipelineID, turn.generation, ContextEntry{Kind: "gap", Gap: true, At: turn.at, SourceID: runtime.cfg.ID})
		}
		m.finishPending(turn.input.EventID)
	}
	runtime.mu.Lock()
	runtime.status.Dropped++
	runtime.status.LastError = reason
	runtime.mu.Unlock()
}

func (m *Manager) runTurnQueue(ctx context.Context, pipelineID string, r *sourceRuntime) {
	defer close(r.turnDone)
	defer m.discardQueuedTurns(r)
	for {
		select {
		case <-ctx.Done():
			return
		case turn, ok := <-r.turns:
			if !ok {
				return
			}
			if !m.sourceCurrent(r.cfg.ID, r.epoch) {
				m.recordDroppedTurn(r, turn, "source stopped before queued event could run")
				continue
			}
			if !m.turnGenerationCurrent(turn) {
				m.finishPending(turn.input.EventID)
				continue
			}
			m.mu.RLock()
			sem := m.turnSem
			m.mu.RUnlock()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				m.recordDroppedTurn(r, turn, "source stopped while event waited for a processing slot")
				return
			}
			m.executeBackgroundTurn(ctx, pipelineID, r, turn)
			<-sem
		}
	}
}

func (m *Manager) discardQueuedTurns(runtime *sourceRuntime) {
	for {
		select {
		case turn, ok := <-runtime.turns:
			if !ok {
				return
			}
			m.recordDroppedTurn(runtime, turn, "source stopped before queued event could run")
		default:
			return
		}
	}
}

func (m *Manager) executeBackgroundTurn(parent context.Context, pipelineID string, r *sourceRuntime, turn backgroundTurn) {
	if turn.input.Audio != nil {
		defer m.finishPending(turn.input.EventID)
	}
	m.turnGate.RLock()
	defer m.turnGate.RUnlock()
	if !m.sourceCurrent(r.cfg.ID, r.epoch) || !m.turnGenerationCurrent(turn) {
		return
	}
	m.mu.RLock()
	exec := m.execute
	publisher := m.publish
	cfg := m.cfg
	m.mu.RUnlock()
	capture := m.backgroundScreenCapture()
	if exec == nil {
		return
	}
	p := activePipeline(cfg)
	if p == nil || p.ID != pipelineID {
		return
	}
	timeout := 30 * time.Second
	if p.SessionBudget != nil && p.SessionBudget.EndToEndMS > 0 {
		timeout = time.Duration(p.SessionBudget.EndToEndMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	turnID := m.registerActiveTurn(turn, cancel)
	defer func() {
		m.unregisterActiveTurn(turnID)
		cancel()
	}()
	if !m.turnGenerationCurrent(turn) {
		return
	}
	sttDone := atomic.Bool{}
	observe := func(o engine.Observation) {
		if o.Kind != "audio.text" || turn.ctxCfg.ID == "" {
			return
		}
		var start, end time.Time
		if turn.input.Audio != nil {
			start, end = turn.input.Audio.StartedAt, turn.input.Audio.EndedAt
		}
		at := start
		if at.IsZero() {
			at = turn.at
		}
		final := true
		if turn.input.Audio != nil {
			final = turn.input.Audio.Final
		}
		entry := ContextEntry{
			ID:       turn.input.EventID,
			Text:     o.Text,
			At:       at,
			Start:    start,
			End:      end,
			SourceID: r.cfg.ID,
			Revision: 1,
			Final:    final,
		}
		if m.contexts.UpsertTranscript(turn.ctxCfg, pipelineID, turn.generation, entry) {
			r.mu.Lock()
			r.status.LastTranscript = o.Text
			r.status.TranscriptLagMS = time.Since(turn.at).Milliseconds()
			r.status.LastError = ""
			r.mu.Unlock()
		}
		sttDone.Store(true)
		m.finishPending(turn.input.EventID)
	}
	res, err := exec(ctx, pipelineID, turn.input, engine.RunOptions{Purpose: "background", Capture: capture, OnObservation: observe})
	if turn.input.Audio != nil && !sttDone.Load() {
		m.finishPending(turn.input.EventID)
	}
	if !m.sourceCurrent(r.cfg.ID, r.epoch) || !m.turnGenerationCurrent(turn) {
		return
	}
	setSourceOutcome(r, backgroundOutcome(res), err)
	if err != nil {
		return
	}
	if res.Published && res.Text != "" {
		if p.SessionBudget != nil && p.SessionBudget.CooldownMS > 0 {
			r.mu.Lock()
			r.cooldownUntil = time.Now().Add(time.Duration(p.SessionBudget.CooldownMS) * time.Millisecond)
			r.mu.Unlock()
		}
		if turn.ctxCfg.ID != "" && turn.ctxCfg.IncludeAnswers {
			m.contexts.Append(turn.ctxCfg, pipelineID, turn.generation, ContextEntry{ID: res.Trace.ID, Kind: "answer", Text: res.Text, At: time.Now(), SourceID: "assistant"})
		}
		if publisher != nil && m.sourceCurrent(r.cfg.ID, r.epoch) && m.turnGenerationCurrent(turn) {
			publisher(Publication{ID: m.seq.Add(1), PipelineID: pipelineID, SourceID: r.cfg.ID, EventID: turn.input.EventID, Text: res.Text, TriggeredAt: turn.at, At: time.Now(), Automatic: true})
		}
	}
}

func (m *Manager) turnGenerationCurrent(turn backgroundTurn) bool {
	if turn.ctxCfg.ID == "" || turn.generation == 0 {
		return true
	}
	return m.contexts.Generation(turn.pipelineID, turn.ctxCfg.ID) == turn.generation
}

func (m *Manager) registerActiveTurn(turn backgroundTurn, cancel context.CancelFunc) string {
	id := turn.input.EventID
	if id == "" {
		id = fmt.Sprintf("background-turn-%d", m.eventSeq.Add(1))
	}
	m.activeTurnMu.Lock()
	m.activeTurns[id] = activeBackgroundTurn{
		pipelineID: turn.pipelineID, contextID: turn.ctxCfg.ID, generation: turn.generation, cancel: cancel,
	}
	m.activeTurnMu.Unlock()
	return id
}

func (m *Manager) unregisterActiveTurn(id string) {
	if id == "" {
		return
	}
	m.activeTurnMu.Lock()
	delete(m.activeTurns, id)
	m.activeTurnMu.Unlock()
}

func (m *Manager) cancelContextTurns(pipelineID, contextID string, currentGeneration uint64) {
	m.activeTurnMu.Lock()
	var cancels []context.CancelFunc
	for _, turn := range m.activeTurns {
		if turn.pipelineID == pipelineID && turn.contextID == contextID && turn.generation != currentGeneration {
			cancels = append(cancels, turn.cancel)
		}
	}
	m.activeTurnMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *Manager) clearPendingBeforeGeneration(pipelineID, contextID string, currentGeneration uint64) {
	m.pendingMu.Lock()
	changed := false
	for id, pending := range m.pending {
		if pending.pipelineID == pipelineID && pending.contextID == contextID && pending.generation != currentGeneration {
			delete(m.pending, id)
			changed = true
		}
	}
	if changed {
		m.notifyPendingLocked()
	}
	m.pendingMu.Unlock()
}

func backgroundOutcome(result engine.Result) domain.Outcome {
	if result.Published && result.Text != "" {
		return domain.OutcomeRespond
	}
	if result.Trace.Outcome != "" {
		return result.Trace.Outcome
	}
	return domain.OutcomeSuccess
}

func setSourceOutcome(runtime *sourceRuntime, outcome domain.Outcome, err error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err != nil {
		if outcome == "" || outcome == domain.OutcomeSuccess {
			outcome = domain.OutcomeAnyError
		}
		runtime.status.LastOutcome = string(outcome)
		runtime.status.LastError = err.Error()
		return
	}
	runtime.status.LastOutcome = string(outcome)
	runtime.status.LastError = ""
}
func (m *Manager) source(id string) *sourceRuntime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sources[id]
}
func (m *Manager) sourceCurrent(id string, epoch uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := m.sources[id]
	return r != nil && r.epoch == epoch && m.started && !m.paused
}

func (m *Manager) PrepareCommand(ctx context.Context, action string) (PreparedCommand, error) {
	return m.PrepareCommandWithCapture(ctx, action, nil)
}
func (m *Manager) PrepareCommandWithCapture(ctx context.Context, action string, override ScreenCapture) (PreparedCommand, error) {
	m.mu.RLock()
	cfg := m.cfg
	capture := m.capture
	m.mu.RUnlock()
	if override != nil {
		capture = override
	}
	p := activePipeline(cfg)
	if p == nil {
		return PreparedCommand{}, fmt.Errorf("active pipeline missing")
	}
	cmd, ok := findCommand(p, action)
	if !ok {
		return PreparedCommand{}, fmt.Errorf("command %q is not configured in active pipeline", action)
	}
	at := time.Now()
	var img []byte
	var err error
	if cmd.CaptureScreen {
		if capture == nil {
			return PreparedCommand{}, fmt.Errorf("screen capture unavailable")
		}
		img, err = capture(ctx)
		if err != nil {
			return PreparedCommand{}, err
		}
	}
	ctxCfg, has := findContext(p, cmd.ContextID)
	snap := ContextSnapshot{}
	gen := uint64(0)
	incomplete := false
	if has {
		gen = m.contexts.Generation(p.ID, ctxCfg.ID)
		if cmd.WaitForTranscriptMS > 0 {
			complete, waitErr := m.waitPending(ctx, p.ID, ctxCfg.ID, gen, at, time.Duration(cmd.WaitForTranscriptMS)*time.Millisecond)
			if waitErr != nil {
				return PreparedCommand{}, waitErr
			}
			incomplete = !complete
		}
		if cmd.Intent == domain.IntentContext && len(img) > 0 {
			m.contexts.SetScreen(ctxCfg, p.ID, gen, img, at)
			return PreparedCommand{PipelineID: p.ID, Command: cmd, SkipRun: true}, nil
		}
		snap = m.contexts.Snapshot(ctxCfg, p.ID, at)
	}
	inputImage := img
	if len(inputImage) == 0 && len(snap.ImageJPEG) > 0 {
		inputImage = snap.ImageJPEG
	}
	input := engine.Input{ImageJPEG: append([]byte(nil), inputImage...), ContextText: snap.Text, ContextIncomplete: incomplete, EntryNodeID: cmd.EntryNodeID, Intent: string(cmd.Intent), SourceID: cmd.ID, EventID: fmt.Sprintf("%s-%d", cmd.ID, m.eventSeq.Add(1))}
	return PreparedCommand{PipelineID: p.ID, Command: cmd, Input: input}, nil
}
func (m *Manager) ClearContext(contextID string) (uint64, error) {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	p := activePipeline(cfg)
	if p == nil {
		return 0, fmt.Errorf("active pipeline missing")
	}
	c, ok := findContext(p, contextID)
	if !ok {
		return 0, fmt.Errorf("context not found")
	}
	generation := m.contexts.Clear(p.ID, c.ID)
	m.clearPendingBeforeGeneration(p.ID, c.ID, generation)
	m.cancelContextTurns(p.ID, c.ID, generation)
	return generation, nil
}
func (m *Manager) Status() map[string]any {
	m.mu.RLock()
	cfg := m.cfg
	paused := m.paused
	list := make([]*sourceRuntime, 0, len(m.sources))
	for _, r := range m.sources {
		list = append(list, r)
	}
	m.mu.RUnlock()
	p := activePipeline(cfg)
	rows := make([]SourceStatus, 0, len(list))
	for _, r := range list {
		r.mu.RLock()
		st := r.status
		st.Paused = paused
		r.mu.RUnlock()
		rows = append(rows, st)
	}
	contexts := []map[string]any{}
	pid := ""
	if p != nil {
		pid = p.ID
		contexts = m.contexts.Status(p.ID, p.Contexts)
	}
	return map[string]any{"active_pipeline_id": pid, "paused": paused, "sources": rows, "contexts": contexts, "governor": m.governor.Snapshot()}
}

type AudioPreview struct {
	PCM16      []int16 `json:"-"`
	SampleRate int     `json:"sample_rate"`
	DurationMS int     `json:"duration_ms"`
	Peak       float64 `json:"peak"`
	RMS        float64 `json:"rms"`
}

func (m *Manager) PreviewAudio(ctx context.Context, cfg domain.AudioSourceConfig, duration time.Duration) (AudioPreview, error) {
	m.mu.RLock()
	for _, runtime := range m.sources {
		if runtime.cfg.Kind == domain.SourceAudio && !sourceFinished(runtime) {
			m.mu.RUnlock()
			return AudioPreview{}, fmt.Errorf("pause the running audio source before local preview")
		}
	}
	m.mu.RUnlock()
	if duration < 250*time.Millisecond {
		duration = 250 * time.Millisecond
	}
	if duration > 5*time.Second {
		duration = 5 * time.Second
	}
	m.mu.RLock()
	capture := m.audioCapture
	m.mu.RUnlock()
	if capture == nil {
		return AudioPreview{}, fmt.Errorf("audio capture unavailable")
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := AudioPreview{}
	var sum float64
	err := capture(cctx, cfg, func(b paudio.Block) {
		if len(b.Samples) == 0 {
			return
		}
		if out.SampleRate == 0 {
			out.SampleRate = b.Rate
		}
		if b.Rate != out.SampleRate {
			return
		}
		need := out.SampleRate*int(duration/time.Millisecond)/1000 - len(out.PCM16)
		if need <= 0 {
			cancel()
			return
		}
		if need > len(b.Samples) {
			need = len(b.Samples)
		}
		for _, v := range b.Samples[:need] {
			x := float64(v) / 32768
			if x < 0 {
				x = -x
			}
			if x > out.Peak {
				out.Peak = x
			}
			sum += x * x
		}
		out.PCM16 = append(out.PCM16, b.Samples[:need]...)
		if len(out.PCM16) >= out.SampleRate*int(duration/time.Millisecond)/1000 {
			cancel()
		}
	})
	if err != nil && cctx.Err() == nil {
		return AudioPreview{}, err
	}
	if out.SampleRate == 0 || len(out.PCM16) == 0 {
		return AudioPreview{}, fmt.Errorf("audio preview received no samples")
	}
	out.DurationMS = len(out.PCM16) * 1000 / out.SampleRate
	out.RMS = math.Sqrt(sum / float64(len(out.PCM16)))
	return out, nil
}
