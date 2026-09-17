package platform

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"math"
)

// gdiCaptureAPI keeps acquisition/lifetime testable without a Windows desktop.
// Pixels are a top-down, 32-bit BGRA DIB, never a bitmap selected in GetDIBits.
type gdiCaptureAPI struct {
	size         func() (int, int)
	screenDC     func() (uintptr, error)
	releaseDC    func(uintptr)
	memoryDC     func(uintptr) (uintptr, error)
	deleteDC     func(uintptr)
	dib          func(uintptr, int, int) (uintptr, []byte, error)
	deleteBitmap func(uintptr)
	selectBitmap func(uintptr, uintptr) (uintptr, error)
	bitBlt       func(uintptr, uintptr, int, int) error
	flush        func() error
}

func captureFrame(g gdiCaptureAPI) (*image.RGBA, error) {
	w, h := g.size()
	if w <= 0 || h <= 0 || int64(w)*int64(h) > 64*1024*1024 {
		return nil, fmt.Errorf("invalid or excessive screen size: %dx%d", w, h)
	}
	dc, err := g.screenDC()
	if err != nil {
		return nil, err
	}
	defer g.releaseDC(dc)
	mem, err := g.memoryDC(dc)
	if err != nil {
		return nil, err
	}
	defer g.deleteDC(mem)
	bmp, pixels, err := g.dib(dc, w, h)
	if err != nil {
		return nil, err
	}
	defer g.deleteBitmap(bmp)
	old, err := g.selectBitmap(mem, bmp)
	if err != nil {
		return nil, err
	}
	// Restore the previous object before deleting either the DIB or the DC.
	defer g.selectBitmap(mem, old)
	if err = g.bitBlt(mem, dc, w, h); err != nil {
		return nil, err
	}
	// GDI batches operations. Flush on the same OS thread before CPU reads.
	if err = g.flush(); err != nil {
		return nil, err
	}
	return bgraFrame(pixels, w, h)
}

func bgraFrame(pixels []byte, w, h int) (*image.RGBA, error) {
	if w <= 0 || h <= 0 || int64(w)*int64(h) > 64*1024*1024 || int64(len(pixels)) < int64(w)*int64(h)*4 {
		return nil, fmt.Errorf("invalid BGRA buffer %dx%d (%d bytes)", w, h, len(pixels))
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i] = pixels[i+2]
		out.Pix[i+1] = pixels[i+1]
		out.Pix[i+2] = pixels[i]
		out.Pix[i+3] = 255
	}
	return out, nil
}

func encodeScreen(frame *image.RGBA) ([]byte, error) {
	img := resizeScreen(frame, 1800)
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 86}); err != nil {
		return nil, fmt.Errorf("encode screen JPEG: %w", err)
	}
	return b.Bytes(), nil
}

// Bilinear resampling avoids nearest-neighbour jaggies on text and small diagrams.
func resizeScreen(src *image.RGBA, limit int) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if max(w, h) <= limit || limit <= 0 {
		return src
	}
	ratio := float64(limit) / float64(max(w, h))
	nw, nh := max(1, int(float64(w)*ratio)), max(1, int(float64(h)*ratio))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := math.Max(0, (float64(y)+.5)*float64(h)/float64(nh)-.5)
		y0 := int(sy)
		y1 := min(h-1, y0+1)
		fy := sy - float64(y0)
		for x := 0; x < nw; x++ {
			sx := math.Max(0, (float64(x)+.5)*float64(w)/float64(nw)-.5)
			x0 := int(sx)
			x1 := min(w-1, x0+1)
			fx := sx - float64(x0)
			p00, p10, p01, p11 := y0*src.Stride+x0*4, y0*src.Stride+x1*4, y1*src.Stride+x0*4, y1*src.Stride+x1*4
			d := y*dst.Stride + x*4
			for c := 0; c < 3; c++ {
				a := float64(src.Pix[p00+c])*(1-fx) + float64(src.Pix[p10+c])*fx
				b := float64(src.Pix[p01+c])*(1-fx) + float64(src.Pix[p11+c])*fx
				dst.Pix[d+c] = byte(a*(1-fy) + b*fy + .5)
			}
			dst.Pix[d+3] = 255
		}
	}
	return dst
}
