//go:build !windows

package platform

func ApplyWindowProtection(uintptr, bool) ProtectionStatus {
	return ProtectionStatus{Mode: "unsupported"}
}
