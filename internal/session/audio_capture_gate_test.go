package session

import (
	"errors"
	"fmt"
	"testing"
)

func TestAudioCaptureLeaseCannotReleaseReplacement(t *testing.T) {
	var gate audioCaptureGate
	old, err := gate.acquireSource("loopback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.acquireSource("loopback"); !errors.Is(err, errAudioSourceReleasing) {
		t.Fatalf("duplicate owner: %v", err)
	}
	old()
	replacement, err := gate.acquireSource("loopback")
	if err != nil {
		t.Fatal(err)
	}
	defer replacement()
	old()
	if _, err := gate.acquirePreview(); !errors.Is(err, errAudioCaptureActive) {
		t.Fatalf("stale release freed replacement: %v", err)
	}
}

func TestPreviewLeaseCannotReleaseReplacement(t *testing.T) {
	var gate audioCaptureGate
	old, err := gate.acquirePreview()
	if err != nil {
		t.Fatal(err)
	}
	old()
	replacement, err := gate.acquirePreview()
	if err != nil {
		t.Fatal(err)
	}
	defer replacement()
	old()
	if _, err := gate.acquireSource("loopback"); !errors.Is(err, errAudioPreviewActive) {
		t.Fatalf("stale preview release freed replacement: %v", err)
	}
}

func TestAudioCaptureGateAllowsBoundedIndependentSources(t *testing.T) {
	var gate audioCaptureGate
	for i := 0; i < maxNativeAudioCaptures; i++ {
		release, err := gate.acquireSource(fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	if _, err := gate.acquireSource("excess"); !errors.Is(err, errAudioCaptureLimit) {
		t.Fatalf("native capture limit: %v", err)
	}
	if _, err := gate.acquirePreview(); !errors.Is(err, errAudioCaptureActive) {
		t.Fatalf("preview ignored live sources: %v", err)
	}
}
