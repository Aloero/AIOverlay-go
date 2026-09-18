package session

import (
	"context"
	"fmt"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	paudio "aioverlay/internal/platform/audio"
)

func (m *Manager) captureAudioContext(pipelineID string, runtime *sourceRuntime) audioContext {
	m.mu.RLock()
	pipeline := activePipeline(m.cfg)
	if pipeline == nil || pipeline.ID != pipelineID {
		m.mu.RUnlock()
		return audioContext{pipelineID: pipelineID}
	}
	config, _ := findContext(pipeline, runtime.cfg.ContextID)
	m.mu.RUnlock()
	return audioContext{pipelineID: pipelineID, config: config, generation: m.contexts.Generation(pipelineID, config.ID)}
}

func (m *Manager) audioContextCurrent(captured audioContext) bool {
	m.mu.RLock()
	active := activePipelineID(m.cfg) == captured.pipelineID
	m.mu.RUnlock()
	if !active {
		return false
	}
	return captured.config.ID == "" || captured.generation == m.contexts.Generation(captured.pipelineID, captured.config.ID)
}

func (m *Manager) recordAudioGap(captured audioContext, runtime *sourceRuntime, at time.Time, reason string) {
	if !m.audioContextCurrent(captured) {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	runtime.mu.Lock()
	runtime.status.Dropped++
	runtime.status.LastDiagnostic = reason
	runtime.mu.Unlock()
	m.contexts.Append(captured.config, captured.pipelineID, captured.generation, ContextEntry{Kind: "gap", Gap: true, At: at, SourceID: runtime.cfg.ID})
}

func (m *Manager) enqueueAudioSegment(runtime *sourceRuntime, queue *audioSegmentQueue, captured capturedAudioSegment) {
	if !m.audioContextCurrent(captured.context) {
		return
	}
	if err := queue.TryPush(captured); err != nil {
		m.recordAudioGap(captured.context, runtime, captured.segment.Ended, err.Error()+": transcript gap recorded")
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

	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := m.captureGate.acquireSource(runtime.cfg.ID)
	if err != nil {
		return err
	}
	queue := newAudioSegmentQueue(queueCapacity, maxQueuedAudioPCMBytes)
	producerDone := make(chan struct{})
	var captureErr error
	go func() {
		defer close(producerDone)
		defer queue.Close()
		defer release()
		captureErr = m.produceAudioSegments(ctx, pipelineID, runtime, capture, queue)
	}()
	// done must acknowledge native teardown, not just cancellation of the queue
	// consumer. Manager's stop wait stays bounded even for a stuck driver.
	defer func() { <-producerDone }()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case segment, ok := <-queue.items:
			if !ok {
				<-producerDone
				return captureResult(ctx, captureErr)
			}
			queue.Release(segment)
			runtime.mu.Lock()
			runtime.status.Segments++
			runtime.status.LastEvent = segment.segment.Ended.Format(time.RFC3339Nano)
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

func (m *Manager) produceAudioSegments(ctx context.Context, pipelineID string, runtime *sourceRuntime, capture paudio.CaptureFunc, queue *audioSegmentQueue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	segmenter := paudio.Segmenter{Config: *runtime.cfg.Audio}
	captured := m.captureAudioContext(pipelineID, runtime)
	err := capture(ctx, *runtime.cfg.Audio, func(block paudio.Block) {
		if ctx.Err() != nil {
			return
		}
		current := m.captureAudioContext(pipelineID, runtime)
		if current.generation != captured.generation || current.config.ID != captured.config.ID {
			// Clear also discards pre-roll and the unfinished phrase. Neither may
			// be relabelled as new speech when a later packet completes it.
			segmenter.Reset()
		}
		captured = current
		if block.Discontinuity || block.TimestampError {
			m.recordAudioGap(captured, runtime, block.At, "audio discontinuity: transcript context contains a gap")
		}
		for _, segment := range segmenter.Push(block) {
			m.enqueueAudioSegment(runtime, queue, capturedAudioSegment{segment: segment, context: captured})
		}
		if updateAudioStatus(runtime, block, segmenter.Level) {
			m.resetAudioRestartFailures(runtime.cfg.ID)
		}
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}

	interruptedAt := time.Now()
	if tail, ok := segmenter.Interrupt(); ok {
		m.enqueueAudioSegment(runtime, queue, capturedAudioSegment{segment: tail, context: captured})
		interruptedAt = tail.Ended
	}
	m.recordAudioGap(captured, runtime, interruptedAt, "audio source interrupted: transcript context contains a gap")
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

func (m *Manager) processAudioSegment(parent context.Context, pipelineID string, r *sourceRuntime, captured capturedAudioSegment) {
	seg := captured.segment
	if !m.sourceCurrent(r.cfg.ID, r.epoch) || !m.audioContextCurrent(captured.context) {
		return
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	p := activePipeline(cfg)
	if p == nil || p.ID != pipelineID {
		return
	}
	ctxCfg := captured.context.config
	hasCtx := ctxCfg.ID != ""
	gen := captured.context.generation
	snap := ContextSnapshot{}
	if hasCtx {
		snap = m.contexts.Snapshot(ctxCfg, pipelineID, seg.Ended)
		if snap.Generation != gen {
			return
		}
	}
	clip := seg.Clip
	clip.SegmentID = fmt.Sprintf("%s-%d", r.cfg.ID, m.eventSeq.Add(1))
	clip.StartedAt = seg.Started
	clip.EndedAt = seg.Ended
	intent := r.cfg.Intent
	if !clip.Final {
		intent = domain.IntentContext
	}
	if clip.Final && sourceCooldownActive(r, time.Now()) {
		setSourceSuppression(r, "cooldown_after_auto_answer")
		if !hasCtx {
			return
		}
		intent = domain.IntentContext
	} else {
		clearSourceSuppression(r)
	}
	input := engine.Input{Audio: &clip, ContextText: snap.Text, ImageJPEG: snap.ImageJPEG, EntryNodeID: r.cfg.EntryNodeID, Intent: string(intent), SourceID: r.cfg.ID, EventID: clip.SegmentID}
	if hasCtx {
		m.addPending(pendingSegment{pipelineID: pipelineID, contextID: ctxCfg.ID, eventID: clip.SegmentID, generation: gen, start: seg.Started, end: seg.Ended})
	}
	m.enqueueBackgroundTurn(r, backgroundTurn{pipelineID: pipelineID, ctxCfg: ctxCfg, generation: gen, input: input, at: seg.Ended})
}
