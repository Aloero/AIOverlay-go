package platform

const (
	wmPowerBroadcast   = 0x0218
	wmWTSSessionChange = 0x02B1
	pbtApmSuspend      = 0x0004
	wtsSessionLock     = 0x0007
	wtsSessionUnlock   = 0x0008
)

func backgroundSuspendReason(message uint32, wparam uintptr) (string, bool) {
	switch message {
	case wmPowerBroadcast:
		if uint32(wparam) == pbtApmSuspend {
			return "windows_suspend", true
		}
	case wmWTSSessionChange:
		if uint32(wparam) == wtsSessionLock {
			return "windows_session_lock", true
		}
	}
	return "", false
}
