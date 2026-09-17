//go:build windows

package platform

import (
	"fmt"
	"runtime"
	"unsafe"
)

var pCreateDIBSection = gdi32.NewProc("CreateDIBSection")
var pGdiFlush = gdi32.NewProc("GdiFlush")

func captureScreenJPEG() ([]byte, error) {
	// GetDC/ReleaseDC and GDI flush must use the thread that acquired the DC.
	runtime.LockOSThread()
	frame, err := captureFrame(windowsCaptureAPI())
	runtime.UnlockOSThread()
	if err != nil {
		return nil, err
	}
	return encodeScreen(frame)
}

func windowsCaptureAPI() gdiCaptureAPI {
	return gdiCaptureAPI{
		size: func() (int, int) {
			w, _, _ := pGetSystemMetrics.Call(smCXScreen)
			h, _, _ := pGetSystemMetrics.Call(smCYScreen)
			return int(w), int(h)
		},
		screenDC: func() (uintptr, error) {
			v, _, e := pGetDC.Call(0)
			if v == 0 {
				return 0, fmt.Errorf("GetDC(screen): %v", e)
			}
			return v, nil
		},
		releaseDC: func(v uintptr) { pReleaseDC.Call(0, v) },
		memoryDC: func(dc uintptr) (uintptr, error) {
			v, _, e := pCreateCompatibleDC.Call(dc)
			if v == 0 {
				return 0, fmt.Errorf("CreateCompatibleDC: %v", e)
			}
			return v, nil
		},
		deleteDC: func(v uintptr) { pDeleteDC.Call(v) },
		dib: func(dc uintptr, w, h int) (uintptr, []byte, error) {
			info := bitmapInfo{Header: bitmapInfoHeader{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32, Compression: biRGB, SizeImage: uint32(w * h * 4)}}
			var bits *byte
			bmp, _, e := pCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&info)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
			if bmp == 0 || bits == nil {
				if bmp != 0 {
					pDeleteObject.Call(bmp)
				}
				return 0, nil, fmt.Errorf("CreateDIBSection(%dx%d): %v", w, h, e)
			}
			return bmp, unsafe.Slice(bits, w*h*4), nil
		},
		deleteBitmap: func(v uintptr) { pDeleteObject.Call(v) },
		selectBitmap: func(dc, bmp uintptr) (uintptr, error) {
			v, _, e := pSelectObject.Call(dc, bmp)
			if v == 0 || v == ^uintptr(0) {
				return 0, fmt.Errorf("SelectObject(bitmap): %v", e)
			}
			return v, nil
		},
		bitBlt: func(dst, src uintptr, w, h int) error {
			v, _, e := pBitBlt.Call(dst, 0, 0, uintptr(w), uintptr(h), src, 0, 0, srcCopy|0x40000000)
			if v == 0 {
				return fmt.Errorf("BitBlt(screen): %v", e)
			}
			return nil
		},
		flush: func() error {
			v, _, e := pGdiFlush.Call()
			if v == 0 {
				return fmt.Errorf("GdiFlush(screen): %v", e)
			}
			return nil
		},
	}
}

func CaptureScreen() ([]byte, error) { return captureScreenJPEG() }
