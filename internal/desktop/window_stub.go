//go:build !windows

package desktop

import (
	"context"
	"fmt"
)

type unsupported struct{}

func New(Options) Window { return unsupported{} }
func (unsupported) Run(context.Context) error {
	return fmt.Errorf("embedded Pipeline Studio requires Windows; --headless is available for development only")
}
func (unsupported) Show()                                                       {}
func (unsupported) Close()                                                      {}
func (unsupported) Capture(fn func() ([]byte, error)) ([]byte, error)           { return fn() }
func (unsupported) CaptureBackground(fn func() ([]byte, error)) ([]byte, error) { return fn() }
