//go:build windows

// Package singleinstance allows one AIOverlay process per interactive session.
// A second copy would race the first one for config.json, hotkey registrations
// and capture targets while showing no window of its own.
package singleinstance

import (
	"syscall"
	"unsafe"
)

var (
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	pCreateMutexW = kernel32.NewProc("CreateMutexW")
)

const mutexName = "Local\\AIOverlay-go.single-instance"

const errAlreadyExists = syscall.Errno(183)

// Ensure reports whether this process is the first to take the session mutex.
// The handle is deliberately never closed: the operating system releases the
// mutex when the process exits, including after a crash, so no stale lock file
// can block the next launch.
func Ensure() bool {
	name, err := syscall.UTF16PtrFromString(mutexName)
	if err != nil {
		return true
	}
	handle, _, callErr := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		// The API itself failed; that must not become a reason a normal
		// start-up cannot run.
		return true
	}
	return callErr != errAlreadyExists
}
