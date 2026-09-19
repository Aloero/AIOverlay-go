package platform

import "testing"

func TestBackgroundSuspendReasonRequiresExplicitResume(t *testing.T) {
	for _, test := range []struct {
		message uint32
		wparam  uintptr
		reason  string
	}{
		{message: wmPowerBroadcast, wparam: pbtApmSuspend, reason: "windows_suspend"},
		{message: wmWTSSessionChange, wparam: wtsSessionLock, reason: "windows_session_lock"},
	} {
		reason, suspend := backgroundSuspendReason(test.message, test.wparam)
		if !suspend || reason != test.reason {
			t.Fatalf("message=0x%x wparam=0x%x reason=%q suspend=%v", test.message, test.wparam, reason, suspend)
		}
	}
	for _, event := range []struct {
		message uint32
		wparam  uintptr
	}{
		{message: wmPowerBroadcast, wparam: 0x12}, // PBT_APMRESUMEAUTOMATIC
		{message: wmWTSSessionChange, wparam: wtsSessionUnlock},
	} {
		if reason, suspend := backgroundSuspendReason(event.message, event.wparam); suspend || reason != "" {
			t.Fatalf("resume/unlock must require explicit user resume: reason=%q suspend=%v", reason, suspend)
		}
	}
}
