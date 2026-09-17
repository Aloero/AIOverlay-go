//go:build !windows

package audio

import (
	"aioverlay/internal/domain"
	"context"
	"fmt"
)

func Devices() ([]Device, error) {
	return nil, fmt.Errorf("WASAPI audio capture requires Windows 10/11")
}
func Capture(context.Context, domain.AudioSourceConfig, func(Block)) error {
	return fmt.Errorf("WASAPI audio capture requires Windows 10/11")
}
