package platform

import (
	"bytes"
	"errors"
	"image/jpeg"
	"reflect"
	"testing"
)

func testCaptureAPI(log *[]string, fail string) gdiCaptureAPI {
	hit := func(s string) error {
		*log = append(*log, s)
		if s == fail {
			return errors.New(s)
		}
		return nil
	}
	return gdiCaptureAPI{
		size:     func() (int, int) { return 2, 1 },
		screenDC: func() (uintptr, error) { return 1, hit("screen") }, releaseDC: func(uintptr) { hit("release") },
		memoryDC: func(uintptr) (uintptr, error) { return 2, hit("memory") }, deleteDC: func(uintptr) { hit("delete_dc") },
		dib: func(uintptr, int, int) (uintptr, []byte, error) {
			return 3, []byte{0, 0, 255, 0, 255, 0, 0, 0}, hit("dib")
		}, deleteBitmap: func(uintptr) { hit("delete_dib") },
		selectBitmap: func(_, b uintptr) (uintptr, error) {
			if b == 3 {
				return 4, hit("select")
			}
			return 3, hit("restore")
		},
		bitBlt: func(uintptr, uintptr, int, int) error { return hit("copy") }, flush: func() error { return hit("flush") },
	}
}
func TestCaptureCopiesTopDownBGRAAndRestoresBeforeDelete(t *testing.T) {
	var log []string
	img, e := captureFrame(testCaptureAPI(&log, ""))
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(img.Pix, []byte{255, 0, 0, 255, 0, 0, 255, 255}) {
		t.Fatal(img.Pix)
	}
	want := []string{"screen", "memory", "dib", "select", "copy", "flush", "restore", "delete_dib", "delete_dc", "release"}
	if !reflect.DeepEqual(log, want) {
		t.Fatal(log)
	}
	raw, e := encodeScreen(img)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := jpeg.Decode(bytes.NewReader(raw))
	if e != nil || decoded.Bounds().Dx() != 2 {
		t.Fatal(e)
	}
}
func TestCaptureCleanupOnFailure(t *testing.T) {
	for _, step := range []string{"screen", "memory", "dib", "select", "copy", "flush"} {
		t.Run(step, func(t *testing.T) {
			var log []string
			_, e := captureFrame(testCaptureAPI(&log, step))
			if e == nil {
				t.Fatal("missing failure")
			}
			if step != "screen" && log[len(log)-1] != "release" {
				t.Fatal(log)
			}
			if step == "copy" || step == "flush" {
				if !reflect.DeepEqual(log[len(log)-4:], []string{"restore", "delete_dib", "delete_dc", "release"}) {
					t.Fatal(log)
				}
			}
		})
	}
}
func TestRejectInvalidCaptureDimensions(t *testing.T) {
	for _, size := range [][2]int{{0, 1}, {1, -1}, {100000, 100000}} {
		var log []string
		g := testCaptureAPI(&log, "")
		g.size = func() (int, int) { return size[0], size[1] }
		if _, e := captureFrame(g); e == nil {
			t.Fatal(size)
		}
		if len(log) > 0 {
			t.Fatal("allocated invalid frame")
		}
	}
}
func TestRejectShortBGRA(t *testing.T) {
	if _, e := bgraFrame([]byte{0}, 2, 2); e == nil {
		t.Fatal("accepted truncated pixels")
	}
}
