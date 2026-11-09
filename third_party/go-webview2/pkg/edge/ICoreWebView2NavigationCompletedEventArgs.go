package edge

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type _ICoreWebView2NavigationCompletedEventArgsVtbl struct {
	_IUnknownVtbl
	GetIsSuccess      ComProc
	GetWebErrorStatus ComProc
	GetNavigationId   ComProc
}

type ICoreWebView2NavigationCompletedEventArgs struct {
	vtbl *_ICoreWebView2NavigationCompletedEventArgsVtbl
}

func (i *ICoreWebView2NavigationCompletedEventArgs) AddRef() uintptr {
	r, _, _ := i.vtbl.AddRef.Call(uintptr(unsafe.Pointer(i)))
	return r
}

// IsSuccess reports whether the navigation completed without an error.
func (i *ICoreWebView2NavigationCompletedEventArgs) IsSuccess() (bool, error) {
	var err error
	var isSuccess int32
	_, _, err = i.vtbl.GetIsSuccess.Call(
		uintptr(unsafe.Pointer(i)),
		uintptr(unsafe.Pointer(&isSuccess)),
	)
	if err != windows.ERROR_SUCCESS {
		return false, err
	}
	return isSuccess != 0, nil
}

// WebErrorStatus carries the WebView2 navigation error code, 0 on success.
func (i *ICoreWebView2NavigationCompletedEventArgs) WebErrorStatus() (int32, error) {
	var err error
	var status int32
	_, _, err = i.vtbl.GetWebErrorStatus.Call(
		uintptr(unsafe.Pointer(i)),
		uintptr(unsafe.Pointer(&status)),
	)
	if err != windows.ERROR_SUCCESS {
		return 0, err
	}
	return status, nil
}
