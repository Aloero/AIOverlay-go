package session

import (
	"context"
	"fmt"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
)

func (m *Manager) runScreenTimer(ctx context.Context, pipelineID string, r *sourceRuntime) error {
	interval := time.Duration(r.cfg.IntervalMS) * time.Millisecond
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	changeGate := newScreenChangeGate(r.cfg)
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
				r.mu.Lock()
				r.status.LastError = "background screen capture unavailable"
				r.mu.Unlock()
				continue
			}
			contextCfg, _ := findContext(activePipeline(cfg), r.cfg.ContextID)
			generation := m.contexts.Generation(pipelineID, contextCfg.ID)
			if generation != observedGeneration {
				changeGate = newScreenChangeGate(r.cfg)
				observedGeneration = generation
			}
			img, capturedAt, err := captureBackgroundFrame(ctx, capture)
			if err != nil {
				recordScreenCaptureFailure(r, err)
				continue
			}
			if !m.sourceCurrent(r.cfg.ID, r.epoch) {
				continue
			}
			if !changeGate.Accept(img, capturedAt) {
				continue
			}
			m.processScreenFrame(pipelineID, r, cfg, img, capturedAt, generation)
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

func (m *Manager) processScreenFrame(pipelineID string, runtime *sourceRuntime, cfg domain.Config, image []byte, at time.Time, generation uint64) {
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

	if hasContext {
		stored := m.contexts.SetScreen(contextCfg, pipelineID, generation, image, at)
		if contextCfg.KeepLatestScreen && !stored {
			return
		}
	}
	if runtime.cfg.Intent == domain.IntentContext {
		clearSourceSuppression(runtime)
		setSourceOutcome(runtime, domain.OutcomeContextOnly, nil)
		return
	}
	if sourceCooldownActive(runtime, time.Now()) {
		setSourceSuppression(runtime, "cooldown_after_auto_answer")
		return
	}
	clearSourceSuppression(runtime)

	snapshot := ContextSnapshot{}
	if hasContext {
		snapshot = m.contexts.Snapshot(contextCfg, pipelineID, at)
		if snapshot.Generation != generation {
			return
		}
	}
	input := engine.Input{
		ImageJPEG: append([]byte(nil), image...), ContextText: snapshot.Text,
		EntryNodeID: runtime.cfg.EntryNodeID, Intent: string(runtime.cfg.Intent),
		SourceID: runtime.cfg.ID, EventID: fmt.Sprintf("%s-%d", runtime.cfg.ID, m.eventSeq.Add(1)),
	}
	m.enqueueBackgroundTurn(runtime, backgroundTurn{pipelineID: pipelineID, ctxCfg: contextCfg, generation: generation, input: input, at: at})
}
