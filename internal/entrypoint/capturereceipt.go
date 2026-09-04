package entrypoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// capturereceipt.go is the ONE receipt writer that is Go rather than shell.
//
// Every other receipt in this repo is appended by a generated installer running inside the
// jail (receiptShellFns), because that is where the install happens. `yolo capture` is the
// other shape: the act runs on the HOST, the thing it records is a machine-wide store entry,
// and there is no shell to splice a helper into. So the writer is here — but it is the SAME
// SCHEMA, deliberately and structurally: the head comes from receiptPrefix, the field names
// and their order mirror _yolo_receipt line for line, and parseReceiptLine reads what this
// emits. A capture receipt that only its own reader understood would be the parallel ledger
// program-delivery.md §6 warns about.
//
// # Two divergences from the shell writer, both deliberate
//
// ESCAPING IS REAL, not _yolo_scrub's `tr -cd '[:print:]' | tr -d '\"'`. The shell scrubs
// because it is building JSON by string concatenation and cannot escape; Go can, so every
// string field goes through jsonStringLiteral like the head already does. A value that
// survives scrubbing survives this identically, and one that would have been mangled is
// preserved instead.
//
// THE APPEND CAN FAIL ITS CALLER. `_yolo_receipt` is wrapped in `{ … } || true` because an
// unwritable log must never break an agent launch three layers down. Here the receipt IS the
// deliverable half of the act — `yolo capture` exists to produce an entry AND the record of
// it — so a write that fails is reported, not swallowed.

// Receipt kinds and acts this file writes. They are constants rather than literals because
// the reader switches on them and a later slice adds the second act.
const (
	// ReceiptKindCapture is the RESOLVER for a capture: not a fourth name for the
	// installer mechanism (manifest `via:"installer"` → Install.Kind `"native"` → receipt
	// `kind:"installer"` stays a three-name chain), but the store that now stands between
	// the installer and a jail. It is its own kind because receiptKey is (kind, bin): a
	// capture receipt and an installer receipt for one bin must not collapse onto one
	// entry, and reconcileInstallerDigest would sha256 a capture's `path` — a DIRECTORY —
	// if they did.
	ReceiptKindCapture = "capture"
	// ReceiptActRecord is the capture act itself: program-delivery.md §6.3's *record*,
	// the first of the two verbs it names ("capture ships as the installer resolver's
	// implementation of record + materialize"). The second, `materialize`, is written by
	// the launcher when it hardlinks an entry into a home.
	ReceiptActRecord = "record"
)

// CaptureReceipt is §6.3's receipt tuple for one capture — *(declaration, installer URL,
// capture hash, file manifest, platform, time)* — in the receipt schema's own fields.
//
// The mapping is worth stating because five of the six members reuse an existing field:
//
//	declaration    → bin        the pack's `program` contribution, by the name it installs
//	installer URL  → declared   verbatim, exactly as the installer launcher records it
//	capture hash   → resolved   the store KEY: what this declaration resolved to HERE
//	file manifest  → sha256     the sha256 OF the canonical file manifest, of which the key
//	                            is the first 16 chars — so a reader can check one against
//	                            the other, and the manifest itself is a file beside the
//	                            entry rather than a second copy inside this line
//	platform       → platform   the one genuinely new field
//	time           → time       the writer's stamp, as everywhere else
//
// `path` carries the entry root, which is what makes the manifest findable and matches the
// launcher funnels' use of the field (the landing path of the thing installed).
type CaptureReceipt struct {
	// Bin is the binary the captured installer installs.
	Bin string
	// Declared is the installer URL, verbatim from the pack's declaration.
	Declared string
	// Key is the store key — capture.Key(digest), 16 hex chars.
	Key string
	// Digest is the FULL sha256 of the canonical file manifest (64 hex chars). Key is
	// its prefix; both are written so neither has to be derived to be checked.
	Digest string
	// Bytes is the total size of the captured tree's regular files. It is the number the
	// whole subsystem is about (1.2 GB for claude, measured 2026-09-03), so a receipt
	// that omitted it would omit the only quantity a human reads it for.
	Bytes int64
	// Path is the entry root, <CapturesDir>/entries/<key>.
	Path string
	// Platform is "<GOOS>/<GOARCH>" as observed BY THE CAPTURE, not by the host: a
	// capture made on a Mac through podman is a linux capture, and the driver inside the
	// jail is the only party that can tell. It reaches here through the manifest.
	Platform string
	// Act is ReceiptActRecord.
	Act string
	// Time is the moment recorded; rendered as the schema's 20-char UTC stamp.
	Time time.Time
}

// Line renders the receipt as one JSON line, WITHOUT the trailing newline.
//
// The field order mirrors _yolo_receipt exactly — schema, kind, bin, declared, spec,
// resolved, sha256, bytes, path, platform, act, time — with `spec` absent because a capture
// has no install spec (the installer URL is the whole declaration). Order is not semantic to
// any reader, and matching it anyway is what lets a human diff a capture receipt against an
// installer receipt and see one changed field rather than a reshuffle.
func (r CaptureReceipt) Line() string {
	var b strings.Builder
	b.WriteString(receiptPrefix(ReceiptKindCapture, r.Bin, r.Declared))
	if r.Key != "" {
		b.WriteString(`,"resolved":`)
		b.WriteString(jsonStringLiteral(r.Key))
	}
	if r.Digest != "" {
		b.WriteString(`,"sha256":`)
		b.WriteString(jsonStringLiteral(r.Digest))
	}
	if r.Bytes >= 0 {
		fmt.Fprintf(&b, `,"bytes":%d`, r.Bytes)
	}
	if r.Path != "" {
		b.WriteString(`,"path":`)
		b.WriteString(jsonStringLiteral(r.Path))
	}
	if r.Platform != "" {
		b.WriteString(`,"platform":`)
		b.WriteString(jsonStringLiteral(r.Platform))
	}
	act := r.Act
	if act == "" {
		act = ReceiptActRecord
	}
	b.WriteString(`,"act":`)
	b.WriteString(jsonStringLiteral(act))
	b.WriteString(`,"time":`)
	b.WriteString(jsonStringLiteral(r.Time.UTC().Format(receiptTimeLayout)))
	b.WriteString("}")
	return b.String()
}

// receiptTimeLayout is the shell writer's `date -u +%Y-%m-%dT%H:%M:%SZ` — 20 characters,
// second resolution, always UTC. Spelled once here because a Go writer that drifted to
// RFC3339 with a fractional part would produce a stamp the round-trip test's length
// assertion catches and nothing else would.
const receiptTimeLayout = "2006-01-02T15:04:05Z"

// AppendReceiptLine appends one rendered receipt line to a JSONL log, creating the file and
// its parent directory if needed.
//
// O_APPEND and one Write, the same discipline the shell writer states: the kernel serializes
// the implied seek-to-end against every other appender on the inode, so two captures
// finishing at once cannot interleave half-lines.
func AppendReceiptLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
