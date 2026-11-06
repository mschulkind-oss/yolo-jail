package clipimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func fixture(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 40), uint8(y * 40), 200, 255})
		}
	}
	return img
}

func encode(t *testing.T, kind string, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	var err error
	switch kind {
	case "png":
		err = png.Encode(&b, img)
	case "jpeg":
		err = jpeg.Encode(&b, img, nil)
	case "gif":
		err = gif.Encode(&b, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestNormalizeReencodesEachAcceptedTypeAsPNG(t *testing.T) {
	for kind, mime := range map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"} {
		img, err := Normalize(encode(t, kind, fixture(5, 3)), mime)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if img.Width != 5 || img.Height != 3 {
			t.Fatalf("%s: %dx%d", kind, img.Width, img.Height)
		}
		if _, err := png.Decode(bytes.NewReader(img.PNG)); err != nil {
			t.Fatalf("%s: output is not a PNG: %v", kind, err)
		}
	}
}

// TestNormalizeDropsMetadataAndTrailingBytes is P4: nothing but decoded pixels crosses.
func TestNormalizeDropsMetadataAndTrailingBytes(t *testing.T) {
	j := encode(t, "jpeg", fixture(4, 4))
	exif := append([]byte("Exif\x00\x00"), []byte("GPS-SECRET-MARKER")...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(exif)+2))
	withExif := append(append(append([]byte{}, j[:2]...), append(seg, exif...)...), j[2:]...)
	img, err := Normalize(withExif, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(img.PNG, []byte("GPS-SECRET-MARKER")) {
		t.Fatal("the EXIF segment crossed into the output")
	}

	p := append(encode(t, "png", fixture(4, 4)), []byte("TRAILING-SECRET")...)
	img, err = Normalize(p, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(img.PNG, []byte("TRAILING-SECRET")) {
		t.Fatal("bytes after IEND crossed into the output")
	}
}

// patchIHDR rewrites a PNG's declared size and fixes the chunk's CRC, so the header claims
// dimensions the data does not hold.
func patchIHDR(p []byte, w, h uint32) []byte {
	out := append([]byte{}, p...)
	// signature(8) + length(4) + "IHDR"(4) = 16: width at 16, height at 20, CRC at 29.
	binary.BigEndian.PutUint32(out[16:], w)
	binary.BigEndian.PutUint32(out[20:], h)
	binary.BigEndian.PutUint32(out[29:], crc32.ChecksumIEEE(out[12:29]))
	return out
}

func TestNormalizeRefusals(t *testing.T) {
	good := encode(t, "png", fixture(4, 4))
	cases := []struct {
		name string
		data []byte
		mime string
		want error
	}{
		{"oversized header", patchIHDR(good, 10000, 10000), "image/png", ErrTooLarge},
		{"truncated png", good[:len(good)-20], "image/png", ErrNotImage},
		{"png declared as jpeg", good, "image/jpeg", ErrNotImage},
		{"bytes over the cap", make([]byte, MaxBytes+1), "image/png", ErrTooLarge},
		{"unknown type", good, "image/webp", ErrUnsupportedType},
	}
	for _, tc := range cases {
		if _, err := Normalize(tc.data, tc.mime); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}
