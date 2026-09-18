package session

import "errors"

type captureSuppression interface {
	error
	SuppressionReason() string
}

func recordScreenCaptureFailure(runtime *sourceRuntime, err error) {
	if runtime == nil || err == nil {
		return
	}
	var suppressed captureSuppression
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if errors.As(err, &suppressed) && suppressed.SuppressionReason() != "" {
		runtime.status.LastSuppression = suppressed.SuppressionReason()
		runtime.status.LastDiagnostic = err.Error()
		runtime.status.LastError = ""
		return
	}
	runtime.status.LastSuppression = ""
	runtime.status.LastError = err.Error()
}
