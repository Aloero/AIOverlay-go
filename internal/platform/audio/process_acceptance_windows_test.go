//go:build windows

package audio

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/domain"
)

// This acceptance test is intentionally opt-in. Set AIO_PROCESS_LOOPBACK_EXE
// to one running application that is actively producing audio. It verifies the
// real Windows process-loopback path rather than the cross-platform policy layer.
func TestProcessLoopbackPhysicalAcceptance(t *testing.T) {
	executable := os.Getenv("AIO_PROCESS_LOOPBACK_EXE")
	if executable == "" {
		t.Skip("set AIO_PROCESS_LOOPBACK_EXE to a running, audible Windows application")
	}
	capabilities := GetCapabilities()
	if !capabilities.ProcessLoopback {
		t.Fatalf("process loopback unavailable: %s", capabilities.ProcessLoopbackReason)
	}
	config := domain.AudioSourceConfig{
		Mode: "process", Process: &domain.AudioProcessConfig{ExecutablePath: executable},
		SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var processID atomic.Uint32
	var dataBlocks atomic.Int32
	err := Capture(ctx, config, func(block Block) {
		if block.ProcessID != 0 {
			processID.Store(block.ProcessID)
		}
		if len(block.Samples) > 0 && !block.Silent {
			dataBlocks.Add(1)
		}
	})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if processID.Load() == 0 {
		t.Fatal("process-loopback capture never reported the resolved target PID")
	}
	if dataBlocks.Load() == 0 {
		t.Fatal("process-loopback target produced no captured audio blocks during the acceptance window")
	}
}
