package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteWheelNoDataDescriptor is the regression for the v0.7.0 PyPI failure
// ("ZIP archive not accepted: ZIP contains a data descriptor"). Every local file
// header must have general-purpose flag bit 3 CLEAR and carry a real (non-zero)
// CRC + sizes, so PyPI accepts the archive.
func TestWriteWheelNoDataDescriptor(t *testing.T) {
	dir := t.TempDir()
	wheel := filepath.Join(dir, "test-0.1-py3-none-any.whl")
	files := []fileEntry{
		{path: "yolo_jail/bin/yolo", content: bytes.Repeat([]byte("BINARY\x00"), 5000)},                // STORE + exec
		{path: "yolo_jail-0.1.dist-info/METADATA", content: []byte("Name: yolo-jail\nVersion: 0.1\n")}, // DEFLATE
		{path: "yolo_jail-0.1.dist-info/RECORD", content: []byte("yolo_jail/bin/yolo,,\n")},
	}
	if err := writeWheel(files, wheel); err != nil {
		t.Fatalf("writeWheel: %v", err)
	}
	raw, err := os.ReadFile(wheel)
	if err != nil {
		t.Fatal(err)
	}

	// Scan every local file header (sig 0x04034b50) and assert flag bit3 clear +
	// CRC non-zero.
	sig := []byte{0x50, 0x4b, 0x03, 0x04}
	found := 0
	for i := 0; i+30 < len(raw); i++ {
		if !bytes.Equal(raw[i:i+4], sig) {
			continue
		}
		flag := uint16(raw[i+6]) | uint16(raw[i+7])<<8
		if flag&0x8 != 0 {
			t.Errorf("local header at %d has the data-descriptor flag set (PyPI rejects this)", i)
		}
		crc := uint32(raw[i+14]) | uint32(raw[i+15])<<8 | uint32(raw[i+16])<<16 | uint32(raw[i+17])<<24
		if crc == 0 {
			t.Errorf("local header at %d has zero CRC (data-descriptor deferral — PyPI rejects)", i)
		}
		found++
	}
	if found != len(files) {
		t.Fatalf("found %d local headers, want %d", found, len(files))
	}
}

// TestWriteWheelValidAndCorrectMethods: the archive round-trips through a
// standard zip reader, contents match, and bin/ is STORED (exec) while others
// are DEFLATE-compressed.
func TestWriteWheelValidAndCorrectMethods(t *testing.T) {
	dir := t.TempDir()
	wheel := filepath.Join(dir, "w.whl")
	binContent := bytes.Repeat([]byte("compressible-ish "), 1000)
	files := []fileEntry{
		{path: "pkg/bin/tool", content: binContent},
		{path: "pkg/data.txt", content: bytes.Repeat([]byte("aaaa"), 1000)},
	}
	if err := writeWheel(files, wheel); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(wheel)
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("archive not readable: %v", err)
	}
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	// bin/ entry: STORED, executable mode.
	b := byName["pkg/bin/tool"]
	if b == nil {
		t.Fatal("missing pkg/bin/tool")
	}
	if b.Method != zip.Store {
		t.Errorf("bin entry method = %d, want Store(0)", b.Method)
	}
	if b.ExternalAttrs>>16&0o111 == 0 {
		t.Errorf("bin entry not executable: mode %o", b.ExternalAttrs>>16)
	}
	// data entry: DEFLATE.
	d := byName["pkg/data.txt"]
	if d == nil || d.Method != zip.Deflate {
		t.Errorf("data entry method = %v, want Deflate(8)", d)
	}
	// Contents round-trip.
	for _, fe := range files {
		f := byName[fe.path]
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", fe.path, err)
		}
		var got bytes.Buffer
		_, _ = got.ReadFrom(rc)
		rc.Close()
		if !bytes.Equal(got.Bytes(), fe.content) {
			t.Errorf("%s content mismatch", fe.path)
		}
	}
}
