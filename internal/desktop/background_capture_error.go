package desktop

// backgroundCaptureSuppressedError marks an intentional privacy refusal. The
// session runtime recognizes the SuppressionReason method without importing the
// desktop package, so capture policy stays at the UI boundary.
type backgroundCaptureSuppressedError struct {
	reason  string
	message string
}

func (err *backgroundCaptureSuppressedError) Error() string { return err.message }
func (err *backgroundCaptureSuppressedError) SuppressionReason() string {
	return err.reason
}

func suppressBackgroundCapture(reason, message string) error {
	return &backgroundCaptureSuppressedError{reason: reason, message: message}
}
