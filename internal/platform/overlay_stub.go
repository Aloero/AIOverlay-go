//go:build !windows

package platform

import (
	"context"
	"errors"

	"aioverlay/internal/domain"
)

var ErrOverlayUnsupported = errors.New("native overlay is supported on Windows only")

type stubOverlay struct{}

func NewOverlay(domain.OverlayConfig, Callbacks) (Overlay, error) { return &stubOverlay{}, nil }
func (*stubOverlay) Run(ctx context.Context) error                { <-ctx.Done(); return nil }
func (*stubOverlay) Apply(domain.OverlayConfig) error             { return nil }
func (*stubOverlay) SetText(string)                               {}
func (*stubOverlay) SetMeta(string)                               {}
func (*stubOverlay) Show()                                        {}
func (*stubOverlay) Hide()                                        {}
func (*stubOverlay) Clear()                                       {}
func (*stubOverlay) Close() error                                 { return nil }
