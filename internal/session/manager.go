package session

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	paudio "aioverlay/internal/platform/audio"
	"context"
	"fmt"
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

var errCommandContextChanged = fmt.Errorf("conversation cleared during command preparation: %w", context.Canceled)
var errCapturePermissionChanged = fmt.Errorf("screen capture permission changed during request: %w", context.Canceled)

type PreparedCommand struct {
	PipelineID           string
	Command              domain.CommandConfig
	Input                engine.Input
	Context              domain.ContextConfig
	ContextGeneration    uint64
	ContextGenerations   map[string]uint64
	PermissionGeneration uint64
	Capture              ScreenCapture
	SkipRun              bool
}

type CommandContextPreview struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Generation  uint64 `json:"generation"`
	Entries     int    `json:"entries"`
	Gaps        int    `json:"gaps"`
	HasScreen   bool   `json:"has_screen"`
	ScreenAgeMS int64  `json:"screen_age_ms,omitempty"`
}

type CommandStageContextPreview struct {
	Mode      string `json:"mode"`
	ContextID string `json:"context_id,omitempty"`
}

type CommandPreview struct {
	Action              string                                `json:"action"`
	EntryNodeID         string                                `json:"entry_node_id"`
	CaptureScreen       bool                                  `json:"capture_screen"`
	WaitForTranscriptMS int                                   `json:"wait_for_transcript_ms"`
	Contexts            []CommandContextPreview               `json:"contexts"`
	StageContexts       map[string]CommandStageContextPreview `json:"stage_contexts,omitempty"`
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
	LastDiagnostic  string  `json:"last_diagnostic,omitempty"`
	LastOutcome     string  `json:"last_outcome,omitempty"`
	LastSuppression string  `json:"last_suppression,omitempty"`
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
	pipelineID         string
	ctxCfg             domain.ContextConfig
	generation         uint64
	contextGenerations map[string]uint64
	input              engine.Input
	at                 time.Time
}

type activeBackgroundTurn struct {
	pipelineID         string
	contextGenerations map[string]uint64
	cancel             context.CancelFunc
}

type sourceRuntime struct {
	pipelineID    string
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
	mu                    sync.RWMutex
	reconcileMu           sync.Mutex
	turnGate              sync.RWMutex
	cfg                   domain.Config
	execute               Executor
	capture               ScreenCapture
	backgroundCapture     ScreenCapture
	audioCapture          paudio.CaptureFunc
	captureGate           audioCaptureGate
	contexts              *ContextStore
	governor              *Governor
	root                  context.Context
	cancel                context.CancelFunc
	started               bool
	paused                bool
	permissionGeneration  uint64
	permissionPolicy      capturePermissionPolicy
	permissionPolicySet   bool
	sources               map[string]*sourceRuntime
	sourceOverrides       map[string]bool
	turnSem               chan struct{}
	publish               func(Publication)
	seq                   atomic.Uint64
	eventSeq              atomic.Uint64
	sourceSeq             atomic.Uint64
	audioRestartBaseDelay time.Duration
	audioRestartMaxDelay  time.Duration
	audioRestartMu        sync.Mutex
	audioRestartFailures  map[string]int
	pendingMu             sync.Mutex
	pending               map[string]pendingSegment
	pendingChanged        chan struct{}
	activeTurnMu          sync.Mutex
	activeTurns           map[string]activeBackgroundTurn
}

func NewManager(execute Executor) *Manager {
	return &Manager{
		execute:               execute,
		audioCapture:          paudio.Capture,
		contexts:              NewContextStore(),
		governor:              NewGovernor(),
		permissionGeneration:  1,
		sources:               map[string]*sourceRuntime{},
		sourceOverrides:       map[string]bool{},
		turnSem:               make(chan struct{}, 3),
		pending:               map[string]pendingSegment{},
		pendingChanged:        make(chan struct{}),
		activeTurns:           map[string]activeBackgroundTurn{},
		audioRestartBaseDelay: defaultAudioRestartDelay,
		audioRestartMaxDelay:  maxAudioRestartDelay,
		audioRestartFailures:  map[string]int{},
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
	m.permissionPolicy = capturePermissions(cfg)
	m.permissionPolicySet = true
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
	m.paused = false
	m.sourceOverrides = map[string]bool{}
	m.mu.Unlock()
	m.resetAllAudioRestartFailures()
	if cancel != nil {
		cancel()
	}
	m.stopAll()
}
func (m *Manager) Apply(cfg domain.Config) {
	m.apply(cfg, false)
}

// ApplyImported treats imported background permissions as untrusted runtime
// state. Saved topology is applied, but every Start-now override is cleared so
// an import cannot keep or start capture merely by reusing a source ID.
func (m *Manager) ApplyImported(cfg domain.Config) {
	m.apply(cfg, true)
}

func (m *Manager) apply(cfg domain.Config, clearRuntimeOverrides bool) {
	m.mu.Lock()
	pipelineChanged := activePipelineID(m.cfg) != activePipelineID(cfg)
	if !m.permissionPolicySet {
		m.permissionPolicy = capturePermissions(m.cfg)
		m.permissionPolicySet = true
	}
	nextPermissions := capturePermissions(cfg)
	if !reflect.DeepEqual(m.permissionPolicy, nextPermissions) {
		m.permissionGeneration++
		m.permissionPolicy = nextPermissions
	}
	if clearRuntimeOverrides || pipelineChanged {
		m.sourceOverrides = map[string]bool{}
	} else {
		m.pruneSourceOverridesLocked(cfg)
	}
	m.cfg = cfg
	m.configureBudgetLocked(cfg)
	started := m.started
	m.mu.Unlock()
	m.contexts.RetainConfig(cfg)
	if started {
		m.reconcile()
	}
}

func (m *Manager) pruneSourceOverridesLocked(cfg domain.Config) {
	wantedIDs := map[string]bool{}
	if pipeline := activePipeline(cfg); pipeline != nil {
		for _, source := range pipeline.Sources {
			wantedIDs[source.ID] = true
		}
	}
	for id := range m.sourceOverrides {
		if !wantedIDs[id] {
			delete(m.sourceOverrides, id)
		}
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
				if running && src.Kind == domain.SourceAudio && m.captureGate.previewActive() {
					m.mu.Unlock()
					return errAudioPreviewActive
				}
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
	m.resetAudioRestartFailures(id)
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
	var resetRetry []string
	for id, runtime := range m.sources {
		next, wantedNow := wanted[id]
		sameConfig := wantedNow && runtime.pipelineID == activePipelineID(cfg) && sameSource(runtime.cfg, next)
		if sameConfig && !sourceFinished(runtime) {
			continue
		}
		if !sameConfig {
			resetRetry = append(resetRetry, id)
		}
		runtime.cancel()
		delete(m.sources, id)
		stopped = append(stopped, runtime)
	}
	m.mu.Unlock()
	for _, id := range resetRetry {
		m.resetAudioRestartFailures(id)
	}
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
			pipelineID: p.ID,
			cfg:        src,
			cancel:     cancel,
			done:       make(chan struct{}),
			turnDone:   make(chan struct{}),
			epoch:      m.sourceSeq.Add(1),
			turns:      make(chan backgroundTurn, 1),
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
func sameSource(a, b domain.EventSource) bool {
	a.Name = ""
	b.Name = ""
	return reflect.DeepEqual(a, b)
}

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

func waitForChannelUntil(done <-chan struct{}, deadline time.Time) bool {
	return waitForChannel(done, time.Until(deadline))
}

func waitForChannel(done <-chan struct{}, timeout time.Duration) bool {
	if done == nil {
		return true
	}
	if timeout <= 0 {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
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
	contextErr := ctx.Err()
	finishSourceStatus(runtime, err, contextErr)
	if shouldRestartDefaultAudio(runtime, err, contextErr) {
		delay := m.nextAudioRestartDelay(runtime.cfg.ID)
		setAudioRecovering(runtime)
		go m.restartDefaultAudioSource(runtime, delay)
		return
	}
	if contextErr == nil {
		m.resetAudioRestartFailures(runtime.cfg.ID)
	}
}

const (
	defaultAudioRestartDelay      = 250 * time.Millisecond
	maxAudioRestartDelay          = 5 * time.Second
	defaultAudioRestartDrainGrace = 2 * time.Second
	defaultAudioRestartCancelWait = 2 * time.Second
)

func shouldRestartDefaultAudio(runtime *sourceRuntime, runErr, contextErr error) bool {
	return contextErr == nil && runtime.cfg.Kind == domain.SourceAudio && runtime.cfg.Audio != nil &&
		runtime.cfg.Audio.DeviceID == "" && paudio.RecoverableDefaultEndpoint(runErr)
}

func (m *Manager) nextAudioRestartDelay(sourceID string) time.Duration {
	m.audioRestartMu.Lock()
	defer m.audioRestartMu.Unlock()
	failures := m.audioRestartFailures[sourceID] + 1
	m.audioRestartFailures[sourceID] = failures
	return restartBackoff(m.audioRestartBaseDelay, m.audioRestartMaxDelay, failures)
}

func (m *Manager) resetAudioRestartFailures(sourceID string) {
	m.audioRestartMu.Lock()
	delete(m.audioRestartFailures, sourceID)
	m.audioRestartMu.Unlock()
}

func (m *Manager) resetAllAudioRestartFailures() {
	m.audioRestartMu.Lock()
	m.audioRestartFailures = map[string]int{}
	m.audioRestartMu.Unlock()
}

func restartBackoff(base, maximum time.Duration, failures int) time.Duration {
	if base < 0 {
		base = 0
	}
	if maximum < base {
		maximum = base
	}
	if failures <= 1 || base == 0 {
		return base
	}
	delay := base
	for i := 1; i < failures && delay < maximum; i++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func (m *Manager) restartDefaultAudioSource(runtime *sourceRuntime, delay time.Duration) {
	if !drainAudioRuntimeForRestart(runtime, defaultAudioRestartDrainGrace, defaultAudioRestartCancelWait) {
		runtime.mu.Lock()
		runtime.status.TransportState = "unavailable"
		runtime.status.LastDiagnostic = "audio recovery stopped because the previous turn did not release after cancellation"
		runtime.mu.Unlock()
		return
	}
	m.mu.RLock()
	root := m.root
	m.mu.RUnlock()
	if root == nil {
		return
	}
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-root.Done():
		return
	case <-timer.C:
	}

	m.mu.Lock()
	current := m.sources[runtime.cfg.ID]
	if current != runtime || !m.started || m.paused {
		m.mu.Unlock()
		return
	}
	delete(m.sources, runtime.cfg.ID)
	m.mu.Unlock()
	m.reconcile()
}

func drainAudioRuntimeForRestart(runtime *sourceRuntime, grace, cancelWait time.Duration) bool {
	if runtime == nil {
		return true
	}
	if waitForChannel(runtime.turnDone, grace) {
		return true
	}
	if runtime.cancel != nil {
		runtime.cancel()
	}
	return waitForChannel(runtime.turnDone, cancelWait)
}

func setAudioRecovering(runtime *sourceRuntime) {
	runtime.mu.Lock()
	runtime.status.TransportState = "recovering"
	runtime.mu.Unlock()
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

func updateAudioStatus(runtime *sourceRuntime, block paudio.Block, level float64) bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	becameAlive := runtime.status.TransportState != "alive"
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
	return becameAlive
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

func sourceCooldownActive(runtime *sourceRuntime, now time.Time) bool {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return !runtime.cooldownUntil.IsZero() && now.Before(runtime.cooldownUntil)
}

func setSourceSuppression(runtime *sourceRuntime, reason string) {
	runtime.mu.Lock()
	runtime.status.LastSuppression = reason
	runtime.mu.Unlock()
}

func clearSourceSuppression(runtime *sourceRuntime) {
	runtime.mu.Lock()
	runtime.status.LastSuppression = ""
	runtime.mu.Unlock()
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
	runtime.status.LastDiagnostic = reason
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
	policy := m.permissionPolicy
	if !m.permissionPolicySet {
		policy = capturePermissions(m.cfg)
	}
	if !sourcePermissionAllowed(policy, r.cfg) {
		m.mu.RUnlock()
		return
	}
	exec := m.execute
	publisher := m.publish
	cfg := m.cfg
	capture := m.backgroundCapture
	permissionGeneration := m.permissionGeneration
	m.mu.RUnlock()
	capture = m.permissionedCapture(permissionGeneration, capture)
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
	observe := m.transcriptObserver(pipelineID, r, turn)
	res, err := exec(ctx, pipelineID, turn.input, engine.RunOptions{Purpose: "background", Capture: capture, OnObservation: observe})
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
			r.status.LastSuppression = ""
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

func (m *Manager) transcriptObserver(pipelineID string, runtime *sourceRuntime, turn backgroundTurn) func(engine.Observation) {
	return func(observation engine.Observation) {
		if observation.Kind != "audio.text" || turn.ctxCfg.ID == "" {
			return
		}
		entry := transcriptEntry(runtime.cfg.ID, turn, observation)
		accepted := m.contexts.UpsertTranscript(turn.ctxCfg, pipelineID, turn.generation, entry)
		if entry.Final {
			m.finishPending(turn.input.EventID)
		}
		if !accepted {
			return
		}
		runtime.mu.Lock()
		runtime.status.LastTranscript = observation.Text
		if entry.Final {
			runtime.status.TranscriptLagMS = time.Since(turn.at).Milliseconds()
		}
		runtime.status.LastError = ""
		runtime.mu.Unlock()
	}
}

func transcriptEntry(sourceID string, turn backgroundTurn, observation engine.Observation) ContextEntry {
	start, end := time.Time{}, time.Time{}
	if turn.input.Audio != nil {
		start, end = turn.input.Audio.StartedAt, turn.input.Audio.EndedAt
	}
	at := start
	if at.IsZero() {
		at = turn.at
	}
	revision := observation.Revision
	final := observation.Final
	if revision == 0 {
		// Compatibility for existing completed-observation callbacks. Explicit
		// partial producers use a non-zero revision with Final=false.
		revision = 1
		final = true
	}
	id := observation.SegmentID
	if id == "" {
		id = turn.input.EventID
	}
	return ContextEntry{
		ID: id, Text: observation.Text, At: at, Start: start, End: end,
		SourceID: sourceID, Revision: revision, Final: final,
	}
}

func (m *Manager) turnGenerationCurrent(turn backgroundTurn) bool {
	if len(turn.contextGenerations) > 0 {
		for contextID, generation := range turn.contextGenerations {
			if m.contexts.Generation(turn.pipelineID, contextID) != generation {
				return false
			}
		}
		return true
	}
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
	generations := cloneGenerations(turn.contextGenerations)
	if len(generations) == 0 && turn.ctxCfg.ID != "" {
		generations = map[string]uint64{turn.ctxCfg.ID: turn.generation}
	}
	m.activeTurns[id] = activeBackgroundTurn{pipelineID: turn.pipelineID, contextGenerations: generations, cancel: cancel}
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
		if turn.pipelineID != pipelineID {
			continue
		}
		if generation, selected := turn.contextGenerations[contextID]; selected && generation != currentGeneration {
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

func (m *Manager) PreviewCommand(action string) (CommandPreview, error) {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	pipeline := activePipeline(cfg)
	if pipeline == nil {
		return CommandPreview{}, fmt.Errorf("active pipeline missing")
	}
	command, ok := findCommand(pipeline, action)
	if !ok {
		return CommandPreview{}, fmt.Errorf("command %q is not configured in active pipeline", action)
	}
	preview := CommandPreview{Action: action, EntryNodeID: command.EntryNodeID, CaptureScreen: command.CaptureScreen, WaitForTranscriptMS: command.WaitForTranscriptMS, StageContexts: map[string]CommandStageContextPreview{}}
	reachable := map[string]bool{}
	for _, node := range engine.DescribePipelineBehavior(*pipeline).Commands {
		if node.ID != command.ID {
			continue
		}
		for _, item := range node.Reachable {
			reachable[item.ID] = true
		}
	}
	for _, node := range pipeline.Nodes {
		if !reachable[node.ID] || node.Type != domain.NodeModelPool && node.Type != domain.NodeDecision {
			continue
		}
		mode := node.ContextMode
		if mode == "" {
			if node.ContextID != "" {
				mode = "named"
			} else {
				mode = "trigger"
			}
		}
		preview.StageContexts[node.ID] = CommandStageContextPreview{Mode: mode, ContextID: node.ContextID}
	}
	now := time.Now()
	ids := requestedContextIDs(*pipeline, command.EntryNodeID, command.Intent, command.ContextID)
	configs := contextConfigs(pipeline, ids)
	for _, id := range ids {
		contextCfg, exists := configs[id]
		if !exists {
			continue
		}
		snapshot := m.contexts.Snapshot(contextCfg, pipeline.ID, now)
		row := CommandContextPreview{ID: id, Name: contextCfg.Name, Generation: snapshot.Generation, Entries: len(snapshot.Entries), HasScreen: len(snapshot.ImageJPEG) > 0}
		for _, entry := range snapshot.Entries {
			if entry.Gap {
				row.Gaps++
			}
		}
		if row.HasScreen && !snapshot.ImageAt.IsZero() {
			row.ScreenAgeMS = now.Sub(snapshot.ImageAt).Milliseconds()
		}
		preview.Contexts = append(preview.Contexts, row)
	}
	return preview, nil
}

func (m *Manager) PrepareCommand(ctx context.Context, action string) (PreparedCommand, error) {
	return m.PrepareCommandWithCapture(ctx, action, nil)
}
func (m *Manager) PrepareCommandWithCapture(ctx context.Context, action string, override ScreenCapture) (PreparedCommand, error) {
	if err := ctx.Err(); err != nil {
		return PreparedCommand{}, err
	}
	m.mu.RLock()
	cfg := m.cfg
	capture := m.capture
	permissionGeneration := m.permissionGeneration
	m.mu.RUnlock()
	if override != nil {
		capture = override
	}
	capture = m.permissionedCapture(permissionGeneration, capture)
	p := activePipeline(cfg)
	if p == nil {
		return PreparedCommand{}, fmt.Errorf("active pipeline missing")
	}
	cmd, ok := findCommand(p, action)
	if !ok {
		return PreparedCommand{}, fmt.Errorf("command %q is not configured in active pipeline", action)
	}
	ctxCfg, has := findContext(p, cmd.ContextID)
	contextIDs := requestedContextIDs(*p, cmd.EntryNodeID, cmd.Intent, cmd.ContextID)
	configs := contextConfigs(p, contextIDs)
	generations := m.contextGenerations(p.ID, configs)
	gen := generations[ctxCfg.ID]
	triggeredAt := time.Now()
	capturedAt := triggeredAt
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
		if err := ctx.Err(); err != nil {
			return PreparedCommand{}, err
		}
		capturedAt = time.Now()
	}
	incomplete := false
	if cmd.WaitForTranscriptMS > 0 {
		complete, waitErr := m.waitPendingContexts(ctx, p.ID, generations, triggeredAt, time.Duration(cmd.WaitForTranscriptMS)*time.Millisecond)
		if waitErr != nil {
			return PreparedCommand{}, waitErr
		}
		incomplete = !complete
	}
	for contextID, generation := range generations {
		if m.contexts.Generation(p.ID, contextID) != generation {
			return PreparedCommand{}, errCommandContextChanged
		}
	}
	if cmd.Intent == domain.IntentContext && len(img) > 0 {
		if !has || !ctxCfg.KeepLatestScreen {
			return PreparedCommand{}, fmt.Errorf("screen_context command requires a context that stores screens")
		}
		if !m.contexts.SetScreen(ctxCfg, p.ID, gen, img, capturedAt) {
			return PreparedCommand{}, errCommandContextChanged
		}
		return PreparedCommand{PipelineID: p.ID, Command: cmd, Context: ctxCfg, ContextGeneration: gen, ContextGenerations: cloneGenerations(generations), PermissionGeneration: permissionGeneration, Capture: capture, SkipRun: true}, nil
	}
	views, current := m.snapshotContextViews(p.ID, configs, generations, triggeredAt)
	if !current {
		return PreparedCommand{}, errCommandContextChanged
	}
	defaultView := views[cmd.ContextID]
	inputImage := img
	imagePinned := len(img) > 0
	if len(inputImage) == 0 && len(defaultView.ImageJPEG) > 0 {
		inputImage = defaultView.ImageJPEG
	}
	input := engine.Input{
		ImageJPEG: append([]byte(nil), inputImage...), ImagePinned: imagePinned, ContextText: defaultView.Text, ContextViews: views, ContextIncomplete: incomplete,
		EntryNodeID: cmd.EntryNodeID, Intent: string(cmd.Intent), SourceID: cmd.ID, EventID: fmt.Sprintf("%s-%d", cmd.ID, m.eventSeq.Add(1)),
	}
	return PreparedCommand{PipelineID: p.ID, Command: cmd, Input: input, Context: ctxCfg, ContextGeneration: gen, ContextGenerations: cloneGenerations(generations), PermissionGeneration: permissionGeneration, Capture: capture}, nil

}

func (m *Manager) ValidatePreparedCommand(prepared PreparedCommand) error {
	m.mu.RLock()
	pipeline := activePipeline(m.cfg)
	permissionGeneration := m.permissionGeneration
	policy := m.permissionPolicy
	if !m.permissionPolicySet {
		policy = capturePermissions(m.cfg)
	}
	allowed := commandPermissionAllowed(policy, prepared.Command)
	m.mu.RUnlock()
	if pipeline == nil || pipeline.ID != prepared.PipelineID {
		return fmt.Errorf("active pipeline changed before command dispatch: %w", context.Canceled)
	}
	if prepared.PermissionGeneration != 0 && prepared.PermissionGeneration != permissionGeneration {
		return errCapturePermissionChanged
	}
	if !allowed {
		return errCapturePermissionChanged
	}
	for contextID, generation := range prepared.ContextGenerations {
		if m.contexts.Generation(prepared.PipelineID, contextID) != generation {
			return errCommandContextChanged
		}
	}
	if len(prepared.ContextGenerations) == 0 && prepared.Context.ID != "" && m.contexts.Generation(prepared.PipelineID, prepared.Context.ID) != prepared.ContextGeneration {
		return errCommandContextChanged
	}
	return nil
}

// RecordCommandResult stores an accepted manual answer in the context snapshot
// generation that was pinned before the request. It never schedules another turn.
func (m *Manager) RecordCommandResult(prepared PreparedCommand, result engine.Result) bool {
	if !result.Published || result.Text == "" || prepared.Context.ID == "" || !prepared.Context.IncludeAnswers {
		return false
	}
	at := result.Trace.FinishedAt
	if at.IsZero() {
		at = time.Now()
	}
	id := result.Trace.ID
	if id == "" {
		id = prepared.Input.EventID + "-answer"
	}
	return m.contexts.Append(prepared.Context, prepared.PipelineID, prepared.ContextGeneration, ContextEntry{
		ID: id, Kind: "answer", Text: result.Text, At: at, SourceID: "assistant",
	})
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
	m.clearContextSourceStatus(p.ID, c.ID)
	return generation, nil
}
func (m *Manager) clearContextSourceStatus(pipelineID, contextID string) {
	m.mu.RLock()
	runtimes := make([]*sourceRuntime, 0, len(m.sources))
	for _, runtime := range m.sources {
		if runtime.cfg.ContextID == contextID && (runtime.pipelineID == "" || runtime.pipelineID == pipelineID) {
			runtimes = append(runtimes, runtime)
		}
	}
	m.mu.RUnlock()
	for _, runtime := range runtimes {
		runtime.mu.Lock()
		runtime.status.LastTranscript = ""
		runtime.status.TranscriptLagMS = 0
		runtime.mu.Unlock()
	}
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
	m.mu.RLock()
	permissionGeneration := m.permissionGeneration
	m.mu.RUnlock()
	return map[string]any{"active_pipeline_id": pid, "paused": paused, "permission_generation": permissionGeneration, "sources": rows, "contexts": contexts, "governor": m.governor.Snapshot()}
}
