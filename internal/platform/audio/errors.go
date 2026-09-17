package audio

import (
	"errors"
	"fmt"
)

type CaptureErrorKind string

const (
	CaptureErrorWASAPI               CaptureErrorKind = "wasapi"
	CaptureErrorDeviceInvalidated    CaptureErrorKind = "device_invalidated"
	CaptureErrorResourcesInvalidated CaptureErrorKind = "resources_invalidated"
	CaptureErrorServiceNotRunning    CaptureErrorKind = "service_not_running"
)

const (
	audclntEDeviceInvalidated    uint32 = 0x88890004
	audclntEServiceNotRunning    uint32 = 0x88890010
	audclntEResourcesInvalidated uint32 = 0x88890026
)

type CaptureError struct {
	Operation string
	HRESULT   uint32
	Kind      CaptureErrorKind
}

func (err *CaptureError) Error() string {
	return fmt.Sprintf("WASAPI %s: HRESULT 0x%08x", err.Operation, err.HRESULT)
}

func classifyWASAPIHRESULT(code uint32) CaptureErrorKind {
	switch code {
	case audclntEDeviceInvalidated:
		return CaptureErrorDeviceInvalidated
	case audclntEResourcesInvalidated:
		return CaptureErrorResourcesInvalidated
	case audclntEServiceNotRunning:
		return CaptureErrorServiceNotRunning
	default:
		return CaptureErrorWASAPI
	}
}

// RecoverableDefaultEndpoint identifies lifecycle failures that can be handled
// by rebuilding a default-endpoint stream. The session layer applies bounded
// backoff and never substitutes a default endpoint for a configured fixed ID.
func RecoverableDefaultEndpoint(err error) bool {
	var captureErr *CaptureError
	if !errors.As(err, &captureErr) {
		return false
	}
	switch captureErr.Kind {
	case CaptureErrorDeviceInvalidated, CaptureErrorResourcesInvalidated, CaptureErrorServiceNotRunning:
		return true
	default:
		return false
	}
}
