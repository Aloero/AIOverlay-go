//go:build windows

package platform

import (
	"fmt"
	"golang.org/x/sys/windows"
	"time"
	"unsafe"
)

// These statuses describe Win32 acceptance/readback, not invisibility to every capture API.
func ApplyWindowProtection(hwnd uintptr, on bool) ProtectionStatus {
	s := ProtectionStatus{Requested: on, CheckedAt: time.Now(), Mode: "none"}
	affinity := uintptr(0)
	if on {
		affinity = 0x11
		s.Mode = "exclude"
		if windows.RtlGetVersion().BuildNumber < 19041 {
			affinity = 1
			s.Mode = "monitor-only"
		}
	}
	ok, _, err := pSetWindowDisplayAffinity.Call(hwnd, affinity)
	if ok == 0 {
		s.Error = fmt.Sprintf("SetWindowDisplayAffinity: %v", err)
	} else {
		var actual uint32
		got, _, e := user32.NewProc("GetWindowDisplayAffinity").Call(hwnd, uintptr(unsafe.Pointer(&actual)))
		s.Applied = got != 0 && actual == uint32(affinity)
		if !s.Applied {
			s.Error = fmt.Sprintf("GetWindowDisplayAffinity mismatch (0x%x): %v", actual, e)
		}
	}
	value := int32(0)
	if on {
		value = 1
	}
	data := compositionData{Attrib: wcaExcludedFromDDA, Data: unsafe.Pointer(&value), Size: unsafe.Sizeof(value)}
	if e := pSetWindowCompositionAttribute.Find(); e != nil {
		s.CompositionError = "Composition exclusion API unavailable"
	} else {
		ok, _, e := pSetWindowCompositionAttribute.Call(hwnd, uintptr(unsafe.Pointer(&data)))
		if ok == 0 {
			s.CompositionError = fmt.Sprintf("Composition exclusion: %v", e)
		}
	}
	return s
}
