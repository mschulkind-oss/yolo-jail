package clipimage

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
)

// The caps (design §7.2, PA-D5).
const (
	MaxBytes  = 32 << 20
	MaxPixels = 40_000_000
)

// Image is a clipboard image after the checks: a new PNG encoded from the decoded pixels, and
// what was read.
type Image struct {
	PNG         []byte
	Width       int
	Height      int
	SourceType  string
	SourceBytes int
}

type codec struct {
	config func(io.Reader) (image.Config, error)
	decode func(io.Reader) (image.Image, error)
}

var codecs = map[string]codec{
	"image/png":  {png.DecodeConfig, png.Decode},
	"image/jpeg": {jpeg.DecodeConfig, jpeg.Decode},
	"image/gif":  {gif.DecodeConfig, gif.Decode},
}

// Normalize checks data as the declared type and re-encodes it as PNG. Re-encoding is what
// makes "nothing but pixels crosses" true: EXIF, embedded profiles and trailing bytes are not in
// the output. The size is read from the header and checked before the full decode.
func Normalize(data []byte, mime string) (Image, error) {
	c, ok := codecs[mime]
	if !ok {
		return Image{}, fmt.Errorf("%w: %s; copy the image as PNG, then run `yolo paste` again", ErrUnsupportedType, mime)
	}
	if len(data) > MaxBytes {
		return Image{}, tooLargeBytes()
	}
	cfg, err := c.config(bytes.NewReader(data))
	if err != nil {
		return Image{}, notImage(mime, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return Image{}, notImage(mime, fmt.Errorf("%dx%d", cfg.Width, cfg.Height))
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return Image{}, fmt.Errorf("%w: the clipboard's image is %dx%d, over the %d megapixel cap; scale it down, copy it again, then run `yolo paste` again",
			ErrTooLarge, cfg.Width, cfg.Height, MaxPixels/1_000_000)
	}
	img, err := c.decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, notImage(mime, err)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return Image{}, fmt.Errorf("encoding the PNG: %w", err)
	}
	b := img.Bounds()
	return Image{PNG: out.Bytes(), Width: b.Dx(), Height: b.Dy(), SourceType: mime, SourceBytes: len(data)}, nil
}

func notImage(mime string, err error) error {
	return fmt.Errorf("%w: the clipboard's %s data does not decode (%v); copy the image again, then run `yolo paste` again", ErrNotImage, mime, err)
}
