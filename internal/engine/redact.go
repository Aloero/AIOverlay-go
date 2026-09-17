package engine

import "strings"

type safeError struct {
	cause   error
	message string
}

func (e safeError) Error() string { return e.message }
func (e safeError) Unwrap() error { return e.cause }
func (e *Engine) safeError(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	for _, p := range e.cfg.Providers {
		for _, k := range p.CredentialPool.Keys {
			if k.Secret != "" {
				text = strings.ReplaceAll(text, k.Secret, "[redacted]")
			}
		}
	}
	if len(text) > 2048 {
		text = text[:2048] + "…"
	}
	if text == err.Error() {
		return err
	}
	return safeError{err, text}
}
