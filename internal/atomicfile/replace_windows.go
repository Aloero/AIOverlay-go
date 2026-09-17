//go:build windows

package atomicfile

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"syscall"
	"unsafe"
)

// Never remove the live file first. ReplaceFile keeps the destination security
// descriptor; the original remains in place when sharing/antivirus blocks replace.
func replaceFile(from, to string) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	if _, err = os.Stat(to); errors.Is(err, os.ErrNotExist) {
		return windows.MoveFileEx(src, dst, windows.MOVEFILE_WRITE_THROUGH)
	} else if err != nil {
		return err
	}
	ok, _, callErr := syscall.NewLazyDLL("kernel32.dll").NewProc("ReplaceFileW").Call(uintptr(unsafe.Pointer(dst)), uintptr(unsafe.Pointer(src)), 0, 0, 0, 0)
	if ok == 0 {
		return callErr
	}
	return nil
}
