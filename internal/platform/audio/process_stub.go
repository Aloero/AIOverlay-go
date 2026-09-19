//go:build !windows

package audio

import (
	"fmt"

	"aioverlay/internal/domain"
)

func GetCapabilities() Capabilities {
	return Capabilities{
		MinimumProcessLoopbackBuild: minimumProcessLoopbackBuild,
		ProcessLoopbackReason:       "Захват приложения доступен только на Windows 10/11 build 20348 или новее",
	}
}

func Processes() ([]Process, error) {
	return nil, fmt.Errorf("process-only audio capture requires Windows 10/11 build %d or newer", minimumProcessLoopbackBuild)
}

func CheckProcessTarget(config *domain.AudioProcessConfig) error {
	if config == nil {
		return &ProcessCaptureError{Kind: ProcessCaptureUnavailable, Message: "process-only capture has no process selection"}
	}
	return &ProcessCaptureError{Kind: ProcessCaptureUnsupported, Message: GetCapabilities().ProcessLoopbackReason}
}
