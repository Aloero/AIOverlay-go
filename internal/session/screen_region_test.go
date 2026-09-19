package session

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"aioverlay/internal/domain"
)

func TestCropScreenJPEGUsesSelectedRegionInRAM(t *testing.T) {
	imageSource := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			imageSource.Set(x, y, color.RGBA{R: uint8(x * 20), G: uint8(y * 30), B: 10, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, imageSource, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	cropped, err := cropScreenJPEG(encoded.Bytes(), &domain.ScreenRegionConfig{X: 2, Y: 1, Width: 3, Height: 4})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(cropped))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 3 || decoded.Bounds().Dy() != 4 {
		t.Fatalf("cropped bounds=%v", decoded.Bounds())
	}
}

func TestCropScreenJPEGRejectsRegionOutsideCapture(t *testing.T) {
	imageSource := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var encoded bytes.Buffer
	_ = jpeg.Encode(&encoded, imageSource, nil)
	if _, err := cropScreenJPEG(encoded.Bytes(), &domain.ScreenRegionConfig{X: 3, Y: 3, Width: 4, Height: 4}); err == nil {
		t.Fatal("out-of-bounds screen region accepted")
	}
}
