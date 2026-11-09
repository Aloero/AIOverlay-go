package desktop

import (
	"context"
	"net/http"
)

type Options struct {
	Title        string
	Visible      bool
	Handler      http.Handler
	ConfigPath   string
	Quit         func()
	HotkeyEditor func(bool)
}
type Window interface {
	Run(context.Context) error
	Show()
	Close()
	Capture(func() ([]byte, error)) ([]byte, error)
	CaptureBackground(func() ([]byte, error)) ([]byte, error)
}
