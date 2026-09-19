//go:build !windows

package audio

import (
	"errors"
	"testing"

	"aioverlay/internal/domain"
)

func TestProcessLoopbackCapabilityIsExplicitlyUnavailableOffWindows(t *testing.T) {
	capabilities := GetCapabilities()
	if capabilities.ProcessLoopback || capabilities.SystemLoopback || capabilities.Microphone {
		t.Fatalf("non-Windows capabilities=%+v", capabilities)
	}
	if capabilities.MinimumProcessLoopbackBuild != minimumProcessLoopbackBuild || capabilities.ProcessLoopbackReason == "" {
		t.Fatalf("non-Windows process capability reason=%+v", capabilities)
	}
	err := CheckProcessTarget(&domain.AudioProcessConfig{ExecutablePath: `C:\Program Files\Zoom\bin\Zoom.exe`})
	var processErr *ProcessCaptureError
	if !errors.As(err, &processErr) || processErr.Kind != ProcessCaptureUnsupported {
		t.Fatalf("non-Windows process preflight error=%v", err)
	}
}
