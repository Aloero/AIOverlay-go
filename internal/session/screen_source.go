package session

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
)

type pendingScreenDelivery struct {
	image      []byte
	capturedAt time.Time
	contextDue bool
	triggerDue bool
}

type screenDeliveryClock struct {
	contextInterval time.Duration
	triggerInterval time.Duration
	lastContext     time.Time
	lastTrigger     time.Time
	pending         pendingScreenDelivery
}

func newScreenDeliveryClock(source domain.EventSource) screenDeliveryClock {
	capture := time.Duration(source.IntervalMS) * time.Millisecond
	if capture <= 0 {
		capture = time.Second
	}
	contextInterval := time.Duration(source.ContextIntervalMS) * time.Millisecond
	if contextInterval < capture {
		contextInterval = capture
	}
	triggerInterval := time.Duration(source.TriggerIntervalMS) * time.Millisecond
	if triggerInterval < capture {
		triggerInterval = capture
	}
	return screenDeliveryClock{contextInterval: contextInterval, triggerInterval: triggerInterval}
}

func (clock *screenDeliveryClock) Observe(image []byte, capturedAt time.Time, changed bool, needsContext, needsTrigger bool) (pendingScreenDelivery, bool) {
	if changed {
		clock.pending = pendingScreenDelivery{
			image: append([]byte(nil), image...), capturedAt: capturedAt,
			contextDue: needsContext, triggerDue: needsTrigger,
		}
	} else if len(clock.pending.image) > 0 {
		// A throttled change is delivered using the newest observed frame, not a
		// stale frame captured when the throttle window first opened.
		clock.pending.image = append(clock.pending.image[:0], image...)
		clock.pending.capturedAt = capturedAt
	}
	if len(clock.pending.image) == 0 {
		return pendingScreenDelivery{}, false
	}
	contextNow := clock.pending.contextDue && dueAt(capturedAt, clock.lastContext, clock.contextInterval)
	triggerNow := clock.pending.triggerDue && dueAt(capturedAt, clock.lastTrigger, clock.triggerInterval)
	if !contextNow && !triggerNow {
		return pendingScreenDelivery{}, false
	}
	delivery := pendingScreenDelivery{
		image: append([]byte(nil), clock.pending.image...), capturedAt: clock.pending.capturedAt,
		contextDue: contextNow, triggerDue: triggerNow,
	}
	if contextNow {
		clock.pending.contextDue = false
		clock.lastContext = capturedAt
	}
	if triggerNow {
		clock.pending.triggerDue = false
		clock.lastTrigger = capturedAt
	}
	if !clock.pending.contextDue && !clock.pending.triggerDue {
		clock.pending = pendingScreenDelivery{}
	}
	return delivery, true
}

func dueAt(now, previous time.Time, interval time.Duration) bool {
	return previous.IsZero() || !now.Before(previous.Add(interval))
}

func (m *Manager) runScreenTimer(ctx context.Context, pipelineID string, runtime *sourceRuntime) error {
	interval := time.Duration(runtime.cfg.IntervalMS) * time.Millisecond
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	changeGate := newScreenChangeGate(runtime.cfg)
	deliveryClock := newScreenDeliveryClock(runtime.cfg)
	var observedGeneration uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			capture := m.backgroundScreenCapture()
			m.mu.RLock()
			cfg := m.cfg
			m.mu.RUnlock()
			if capture == nil {
				runtime.mu.Lock()
				runtime.status.LastError = "background screen capture unavailable"
				runtime.mu.Unlock()
				continue
			}
			pipeline := activePipeline(cfg)
			contextCfg, hasContext := findContext(pipeline, runtime.cfg.ContextID)
			generation := m.contexts.Generation(pipelineID, contextCfg.ID)
			if generation != observedGeneration {
				changeGate = newScreenChangeGate(runtime.cfg)
				deliveryClock = newScreenDeliveryClock(runtime.cfg)
				observedGeneration = generation
			}
			image, capturedAt, err := captureBackgroundFrame(ctx, capture)
			if err == nil {
				image, err = cropScreenJPEG(image, runtime.cfg.ScreenRegion)
			}
			if err != nil {
				recordScreenCaptureFailure(runtime, err)
				continue
			}
			if !m.sourceCurrent(runtime.cfg.ID, runtime.epoch) {
				continue
			}
			changed := changeGate.Accept(image, capturedAt)
			needsContext := hasContext && contextCfg.KeepLatestScreen
			needsTrigger := runtime.cfg.Intent != domain.IntentContext
			delivery, ready := deliveryClock.Observe(image, capturedAt, changed, needsContext, needsTrigger)
			if !ready {
				continue
			}
			m.processScreenDelivery(pipelineID, runtime, cfg, delivery.image, delivery.capturedAt, generation, delivery.contextDue, delivery.triggerDue)
		}
	}
}

func captureBackgroundFrame(ctx context.Context, capture ScreenCapture) ([]byte, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, err
	}
	image, err := capture(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, err
	}
	return image, time.Now(), nil
}

func cropScreenJPEG(payload []byte, region *domain.ScreenRegionConfig) ([]byte, error) {
	if region == nil {
		return append([]byte(nil), payload...), nil
	}
	decoded, err := jpeg.Decode(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("decode background screen for region: %w", err)
	}
	bounds := decoded.Bounds()
	requested := image.Rect(region.X, region.Y, region.X+region.Width, region.Y+region.Height)
	if requested.Min.X < bounds.Min.X || requested.Min.Y < bounds.Min.Y || requested.Max.X > bounds.Max.X || requested.Max.Y > bounds.Max.Y || requested.Empty() {
		return nil, fmt.Errorf("screen region is outside captured image bounds")
	}
	cropped := image.NewRGBA(image.Rect(0, 0, requested.Dx(), requested.Dy()))
	for y := 0; y < requested.Dy(); y++ {
		for x := 0; x < requested.Dx(); x++ {
			cropped.Set(x, y, decoded.At(requested.Min.X+x, requested.Min.Y+y))
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, cropped, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("encode background screen region: %w", err)
	}
	return out.Bytes(), nil
}

// processScreenFrame preserves the pre-R2 call contract used by integration
// tests and direct source events: one accepted frame updates context and may
// trigger its configured route. Timer sources use processScreenDelivery to
// apply independent context and AI cadences.
func (m *Manager) processScreenFrame(pipelineID string, runtime *sourceRuntime, cfg domain.Config, image []byte, at time.Time, generation uint64) {
	m.processScreenDelivery(pipelineID, runtime, cfg, image, at, generation, true, true)
}

func (m *Manager) processScreenDelivery(pipelineID string, runtime *sourceRuntime, cfg domain.Config, image []byte, at time.Time, generation uint64, publishContext, trigger bool) {
	pipeline := activePipeline(cfg)
	if pipeline == nil || pipeline.ID != pipelineID {
		return
	}
	contextCfg, hasContext := findContext(pipeline, runtime.cfg.ContextID)
	if hasContext && generation != m.contexts.Generation(pipelineID, contextCfg.ID) {
		return
	}
	runtime.mu.Lock()
	runtime.status.LastEvent = at.Format(time.RFC3339Nano)
	runtime.mu.Unlock()

	if publishContext && hasContext && contextCfg.KeepLatestScreen {
		if !m.contexts.SetScreen(contextCfg, pipelineID, generation, image, at) {
			return
		}
	}
	if !trigger || runtime.cfg.Intent == domain.IntentContext {
		clearSourceSuppression(runtime)
		setSourceOutcome(runtime, domain.OutcomeContextOnly, nil)
		return
	}
	if sourceCooldownActive(runtime, time.Now()) {
		setSourceSuppression(runtime, "cooldown_after_auto_answer")
		return
	}
	clearSourceSuppression(runtime)

	contextIDs := requestedContextIDs(*pipeline, runtime.cfg.EntryNodeID, runtime.cfg.Intent, runtime.cfg.ContextID)
	configs := contextConfigs(pipeline, contextIDs)
	generations := m.contextGenerations(pipelineID, configs)
	if hasContext {
		generations[contextCfg.ID] = generation
	}
	views, current := m.snapshotContextViews(pipelineID, configs, generations, at)
	if !current {
		return
	}
	defaultView := views[runtime.cfg.ContextID]
	input := engine.Input{
		ImageJPEG: append([]byte(nil), image...), ImagePinned: true, ContextText: defaultView.Text, ContextViews: views, DefaultContextID: runtime.cfg.ContextID,
		EntryNodeID: runtime.cfg.EntryNodeID, Intent: string(runtime.cfg.Intent),
		SourceID: runtime.cfg.ID, EventID: fmt.Sprintf("%s-%d", runtime.cfg.ID, m.eventSeq.Add(1)),
	}
	m.enqueueBackgroundTurn(runtime, backgroundTurn{pipelineID: pipelineID, ctxCfg: contextCfg, generation: generation, contextGenerations: cloneGenerations(generations), input: input, at: at})
}
