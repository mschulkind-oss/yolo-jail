package main

import (
	"bytes"
	"compress/flate"
	binenc "encoding/binary"
	"hash/crc32"
	"os"
	"strings"
)

// zipwheel.go writes wheel ZIP archives WITHOUT data descriptors.
//
// Go's archive/zip.Writer unconditionally sets the data-descriptor flag (bit 3)
// and writes zeros for CRC/sizes in every local file header, deferring the real
// values to a trailing data descriptor. PyPI rejects such archives outright
// ("ZIP archive not accepted: ZIP contains a data descriptor",
// https://docs.pypi.org/archives) — which broke the v0.7.0 publish. Presetting
// the header does not help (verified: the stdlib writer still streams).
//
// Since a wheel's entire contents are already in memory (fileEntry.content), we
// can compute CRC32 + sizes up front and emit each local header complete, with
// no data descriptor. This hand-rolled encoder produces a spec-compliant archive
// (local headers + central directory + EOCD) matching the previous writeWheel's
// metadata exactly: creator byte (Unix), external attrs (exec for bin/, 0o600
// otherwise), and STORE for bin/ entries / DEFLATE for the rest.

const (
	zipLocalSig   = 0x04034b50
	zipCentralSig = 0x02014b50
	zipEOCDSig    = 0x06054b50
	zipVersion    = 20 // 2.0 — the version needed to extract (deflate)
	methodStore   = 0
	methodDeflate = 8
)

// writeWheel zips the ordered file list to wheelPath with no data descriptors.
// bin/ entries are STORED + executable; all others are DEFLATE-compressed + mode
// 0o600 — matching the prior archive/zip-based writer's metadata.
func writeWheel(files []fileEntry, wheelPath string) error {
	var buf bytes.Buffer
	central := &bytes.Buffer{}
	count := 0

	for _, fe := range files {
		store := strings.Contains(fe.path, "/bin/")
		method := uint16(methodDeflate)
		body := deflate(fe.content)
		if store {
			method = methodStore
			body = fe.content
		}
		crc := crc32.ChecksumIEEE(fe.content)
		usize := uint32(len(fe.content))
		csize := uint32(len(body))
		extAttr := uint32(dataMode) << 16
		if store {
			extAttr = uint32(execMode) << 16
		}
		localOff := uint32(buf.Len())

		// Local file header (no data-descriptor flag; CRC+sizes are real).
		writeU32(&buf, zipLocalSig)
		writeU16(&buf, zipVersion) // version needed to extract
		writeU16(&buf, 0)          // general-purpose flag: 0 (NO data descriptor)
		writeU16(&buf, method)
		writeU16(&buf, 0) // mod time
		writeU16(&buf, 0) // mod date
		writeU32(&buf, crc)
		writeU32(&buf, csize)
		writeU32(&buf, usize)
		writeU16(&buf, uint16(len(fe.path)))
		writeU16(&buf, 0) // extra len
		buf.WriteString(fe.path)
		buf.Write(body)

		// Central directory entry.
		writeU32(central, zipCentralSig)
		writeU16(central, uint16(creatorUnix)<<8|zipVersion) // version made by (Unix | 2.0)
		writeU16(central, zipVersion)                        // version needed
		writeU16(central, 0)                                 // flag
		writeU16(central, method)
		writeU16(central, 0) // mod time
		writeU16(central, 0) // mod date
		writeU32(central, crc)
		writeU32(central, csize)
		writeU32(central, usize)
		writeU16(central, uint16(len(fe.path)))
		writeU16(central, 0) // extra len
		writeU16(central, 0) // comment len
		writeU16(central, 0) // disk number start
		writeU16(central, 0) // internal attrs
		writeU32(central, extAttr)
		writeU32(central, localOff)
		central.WriteString(fe.path)
		count++
	}

	centralOff := uint32(buf.Len())
	buf.Write(central.Bytes())

	// End of central directory record.
	writeU32(&buf, zipEOCDSig)
	writeU16(&buf, 0) // disk number
	writeU16(&buf, 0) // disk with central dir
	writeU16(&buf, uint16(count))
	writeU16(&buf, uint16(count))
	writeU32(&buf, uint32(central.Len()))
	writeU32(&buf, centralOff)
	writeU16(&buf, 0) // comment len

	return os.WriteFile(wheelPath, buf.Bytes(), 0o644)
}

// deflate raw-DEFLATEs b at default compression (the stored form for non-bin
// entries). The wheel format wants a raw deflate stream (no zlib header), which
// is what flate.NewWriter produces.
func deflate(b []byte) []byte {
	var out bytes.Buffer
	w, _ := flate.NewWriter(&out, flate.DefaultCompression)
	_, _ = w.Write(b)
	_ = w.Close()
	return out.Bytes()
}

func writeU16(b *bytes.Buffer, v uint16) { _ = binenc.Write(b, binenc.LittleEndian, v) }
func writeU32(b *bytes.Buffer, v uint32) { _ = binenc.Write(b, binenc.LittleEndian, v) }
