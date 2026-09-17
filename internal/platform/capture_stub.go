//go:build !windows

package platform

import "fmt"

func CaptureScreen() ([]byte, error) { return nil, fmt.Errorf("screen capture requires Windows") }
