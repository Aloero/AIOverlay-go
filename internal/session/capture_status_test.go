package session

import "testing"

type testCaptureSuppression struct {
	reason string
}

func (err testCaptureSuppression) Error() string             { return "privacy capture skipped" }
func (err testCaptureSuppression) SuppressionReason() string { return err.reason }

func TestScreenCapturePrivacyRefusalIsSuppressionNotError(t *testing.T) {
	runtime := &sourceRuntime{}
	recordScreenCaptureFailure(runtime, testCaptureSuppression{reason: "studio_visible_without_capture_exclusion"})
	if runtime.status.LastSuppression != "studio_visible_without_capture_exclusion" {
		t.Fatalf("suppression=%q", runtime.status.LastSuppression)
	}
	if runtime.status.LastError != "" || runtime.status.LastDiagnostic != "privacy capture skipped" {
		t.Fatalf("privacy refusal was reported as failure: %+v", runtime.status)
	}
}

func TestScreenCaptureFailureRemainsError(t *testing.T) {
	runtime := &sourceRuntime{status: SourceStatus{LastSuppression: "old"}}
	recordScreenCaptureFailure(runtime, errTestCaptureFailure{})
	if runtime.status.LastSuppression != "" || runtime.status.LastError != "capture failed" {
		t.Fatalf("capture failure state=%+v", runtime.status)
	}
}

type errTestCaptureFailure struct{}

func (errTestCaptureFailure) Error() string { return "capture failed" }
