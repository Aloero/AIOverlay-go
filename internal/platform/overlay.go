package platform

import (
	"context"

	"aioverlay/internal/domain"
)

type Callbacks struct {
	Command           func(string)
	Request           func() error
	Geometry          func(domain.OverlayConfig)
	Capture           func() ([]byte, error)
	Analyze           func(imageJPEG []byte)
	Control           func()
	Cancel            func()
	SuspendBackground func(string)
	Quit              func()
}

type Overlay interface {
	Run(context.Context) error
	Apply(domain.OverlayConfig) error
	SetText(string)
	SetMeta(string)
	Show()
	Hide()
	Clear()
	Close() error
}

// Empty state is intentionally quiet: the native chrome communicates that the
// overlay is present. The body is reserved for capture/request state and AI output.
func overlayIdleText(domain.OverlayConfig) string { return "" }
func overlayIdleMeta(domain.OverlayConfig) string { return "" }
