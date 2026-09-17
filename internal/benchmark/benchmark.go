package benchmark

import (
	"bytes"
	_ "embed"
	"image/jpeg"
	"image/png"
)

//go:embed benchmark.png
var fixturePNG []byte

func ImageJPEG() ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(fixturePNG))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	err = jpeg.Encode(&b, img, &jpeg.Options{Quality: 88})
	return b.Bytes(), err
}
