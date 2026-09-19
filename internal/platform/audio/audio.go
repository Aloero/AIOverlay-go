// Package audio owns audio acquisition only. It never starts an AI request.
package audio

import (
	"aioverlay/internal/domain"
	"context"
	"time"
)

type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mode string `json:"mode"`
}
type Block struct {
	Samples          []int16
	Rate             int
	At               time.Time
	Silent           bool
	Discontinuity    bool
	TimestampError   bool
	ProcessID        uint32
	ProcessPath      string
	ProcessStartedAt time.Time
}
type CaptureFunc func(context.Context, domain.AudioSourceConfig, func(Block)) error
