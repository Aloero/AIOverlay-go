package desktop

import "testing"

func TestBackgroundCaptureSuppressionExposesStableReason(t *testing.T) {
	err := suppressBackgroundCapture("studio_capture_safety_timeout", "details")
	suppressed, ok := err.(interface{ SuppressionReason() string })
	if !ok || suppressed.SuppressionReason() != "studio_capture_safety_timeout" || err.Error() != "details" {
		t.Fatalf("suppression contract=%T %v", err, err)
	}
}
