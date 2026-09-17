//go:build go1.18
// +build go1.18

package edge

import "syscall"

// Call calls a COM procedure.
//
//go:uintptrescapes
func (p ComProc) Call(a ...uintptr) (r1, r2 uintptr, lastErr error) {
	// The magic uintptrescapes comment is needed to prevent moving uintptr(unsafe.Pointer(p)) so calls to .Call() also
	// satisfy the unsafe.Pointer rule "(4) Conversion of a Pointer to a uintptr when calling syscall.Syscall."
	// Otherwise it might be that pointers get moved, especially pointer onto the Go stack which might grow dynamically.
	// See https://pkg.go.dev/unsafe#Pointer and https://github.com/golang/go/issues/34474
	r1, r2, _ = syscall.SyscallN(uintptr(p), a...)
	// COM reports HRESULT, not the thread's unrelated GetLastError value.
	lastErr = syscall.Errno(0)
	if int32(r1) < 0 {
		lastErr = syscall.Errno(uint32(r1))
	}
	return
}
