package session

import (
	"errors"
	"sync"
)

const maxNativeAudioCaptures = 64

var (
	errAudioPreviewActive   = errors.New("local audio preview is already active; stop preview before starting background capture")
	errAudioCaptureActive   = errors.New("audio capture is still active or releasing its device; pause background and wait for capture to stop before local preview")
	errAudioSourceReleasing = errors.New("previous audio capture for this source has not released its device")
	errAudioCaptureLimit    = errors.New("native audio capture limit reached")
)

// A source runtime may finish before its native producer unwinds. These leases
// therefore follow the Capture call itself, not membership in Manager.sources.
type audioCaptureGate struct {
	mu      sync.Mutex
	sources map[string]struct{}
	preview bool
}

func (gate *audioCaptureGate) acquireSource(id string) (func(), error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.preview {
		return nil, errAudioPreviewActive
	}
	if _, exists := gate.sources[id]; exists {
		return nil, errAudioSourceReleasing
	}
	if len(gate.sources) >= maxNativeAudioCaptures {
		return nil, errAudioCaptureLimit
	}
	if gate.sources == nil {
		gate.sources = make(map[string]struct{})
	}
	gate.sources[id] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			gate.mu.Lock()
			delete(gate.sources, id)
			gate.mu.Unlock()
		})
	}, nil
}

func (gate *audioCaptureGate) acquirePreview() (func(), error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.preview {
		return nil, errAudioPreviewActive
	}
	if len(gate.sources) != 0 {
		return nil, errAudioCaptureActive
	}
	gate.preview = true
	var once sync.Once
	return func() {
		once.Do(func() {
			gate.mu.Lock()
			gate.preview = false
			gate.mu.Unlock()
		})
	}, nil
}

func (gate *audioCaptureGate) previewActive() bool {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.preview
}
