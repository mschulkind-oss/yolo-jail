// Package crossaudit is the host-side SINK for svcendpoint's connection-level
// boundary-crossing records: it decides where they land, renders them, and bounds
// them on disk. The records themselves are svcendpoint's (see its crossing.go,
// which explains why connection-level is the honest ceiling at the front and why
// internal/hostservice's per-request line is a second, non-overlapping tier).
//
// Split from svcendpoint on purpose. svcendpoint is a stdlib-only leaf with zero
// internal imports so the leanest baked clients — cmd/yolo-ps is "a pure
// frameproto client, no config, no json5" — can import the transport without
// dragging anything else in. Knowing where a host's state directory lives is not
// a transport concern, so it lives here, one import away, and svcendpoint keeps
// its property.
//
// # ONE FILE PER HOST, and that is the decision
//
// The destination is GLOBAL_STORAGE/logs/crossings.log — beside the existing
// per-service host-service-<name>.log files, whose directory this follows rather
// than inventing one. It is per HOST, not per jail:
//
//   - The question this log exists to answer is "what crossed the boundary
//     today", which is inherently cross-jail and cross-service. One file answers
//     it with one grep. Per-jail files answer it only by fan-out, and the fan-out
//     silently misses a jail whose directory was already reaped.
//   - The narrower question — "what did jail X cross" — is a FIELD on every
//     record, so per-jail is one `rg jail=` away. The reverse is not free.
//     Choosing the shape you cannot cheaply recover from the other is the wrong
//     way round.
//   - The per-service logs are diagnostics (a daemon's own stderr, unstructured,
//     "why is loophole X broken"). This is an audit trail with a schema. Two
//     different artifacts for two different readers; merging them would make both
//     worse.
//   - Durability: a per-jail path sits under a directory that `yolo prune` sweeps
//     when no live jail needs it, and an audit record's value is precisely that it
//     outlives the jail that made it. This file is not per-jail state and is not
//     swept.
//
// # AUDIT ONLY
//
// Nothing here may fail a crossing. The file is opened LAZILY on the first record
// (so importing this package, or installing it in a process that never crosses
// anything, creates nothing), every error path warns ONCE and then goes
// permanently quiet, and svcendpoint recovers a panicking sink on top of that.
// "The log cannot be opened" and "the disk is full" are states in which crossings
// keep working and are simply not recorded.
//
// # A LENGTH, NEVER A VALUE
//
// Line renders counts and host-chosen names only — no payload byte, no token, no
// certificate, no endpoint line. That is svcendpoint's own logging rule and it is
// inherited rather than restated as a hope: a Crossing has no field that could
// carry content, so no rendering of one can leak it.
package crossaudit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const (
	// LogName is the crossing log's leaf name, under GLOBAL_STORAGE/logs.
	LogName = "crossings.log"

	// ArchiveSuffix names the one archived generation. One, not N: see MaxBytes.
	ArchiveSuffix = ".1"

	// MaxBytes caps the ACTIVE log; on the next record past it the file is
	// renamed to LogName+ArchiveSuffix, replacing whatever was there, and a fresh
	// one starts. So at most 2*MaxBytes = 8 MiB of crossing log exists, ever, with
	// no reaper to write and nothing for `yolo prune` to learn.
	//
	// 4 MiB is roughly 40k records — days of a busy multi-jail host, and the
	// bound matters more than the horizon: this repo's jail store and jail home
	// share one block device, so an unbounded log is a disk-exhaustion bug rather
	// than a tidiness question.
	MaxBytes = 4 << 20
)

// DefaultPath is where crossings land: GLOBAL_STORAGE/logs/crossings.log. One per
// host — see the package comment for the argument.
func DefaultPath() string {
	return filepath.Join(paths.GlobalStorage(), "logs", LogName)
}

// Install points svcendpoint's connection-level audit at DefaultPath.
//
// Called once per host process, at the top of the CLI, because every process that
// can front a crossing is the yolo binary: `yolo run` hosts the fronts for
// `publishes: "socket"` daemons, and each `yolo internal daemon <name>` hosts its
// own listener. Installing there rather than at each listener means a new daemon
// is audited by existing, and a caller cannot forget.
//
// Nothing is opened here. The first CROSSING opens the file, so an invocation
// that crosses nothing — `yolo --version`, every test binary that never installs
// — writes nothing and creates no directory.
func Install() { InstallAt(DefaultPath()) }

// InstallAt is Install pointed somewhere else. For tests, and for a caller that
// has already resolved which home it is writing into.
func InstallAt(path string) { svcendpoint.SetCrossingSink(openAt(path).record) }

// Line renders one record: a timestamp, then logfmt-style key=value pairs in a
// fixed order, one line, newline-terminated.
//
// Deliberately close to internal/frameproto's per-request access line
// (`jail=… keys=… rc=… elapsed_ms=… bytes_out=…`) so the two tiers read as
// siblings; deliberately prefixed `crossing` so one grep separates them again.
// "-" rather than an empty value for an absent reason, because a bare `reason=`
// is ambiguous between "nothing to say" and "a field that got lost".
func Line(c svcendpoint.Crossing) string {
	reason := c.Reason
	if reason == "" {
		reason = "-"
	}
	return fmt.Sprintf(
		"%s crossing jail=%s service=%s via=%s outcome=%s reason=%s bytes_in=%d bytes_out=%d elapsed_ms=%d\n",
		c.At.UTC().Format(time.RFC3339), c.Jail, c.Service, c.Via, c.Outcome, reason,
		c.BytesIn, c.BytesOut, c.Duration.Milliseconds())
}

// writer is the bounded append-only file behind the sink. One mutex covers the
// file, its size and the disabled flag: records arrive from every connection
// goroutine in the process.
type writer struct {
	path     string
	maxBytes int

	mu       sync.Mutex
	f        *os.File
	size     int64
	disabled bool
}

func openAt(path string) *writer { return &writer{path: path, maxBytes: MaxBytes} }

// record appends one crossing. It is the sink svcendpoint calls, on the crossing's
// own goroutine AFTER its last byte — so a slow disk delays a teardown, never a
// payload.
//
// EVERY failure path here ends in disable(): one warning, then permanent silence.
// Retrying per crossing would turn a full disk into a log flood, and returning an
// error would give a caller something to mishandle. There is no caller.
func (w *writer) record(c svcendpoint.Crossing) {
	line := Line(c)

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.disabled {
		return
	}
	if w.f == nil {
		if err := w.open(); err != nil {
			w.disable("open", err)
			return
		}
	}
	if w.size+int64(len(line)) > int64(w.maxBytes) {
		if err := w.rotate(); err != nil {
			w.disable("rotate", err)
			return
		}
	}
	n, err := w.f.WriteString(line)
	w.size += int64(n)
	if err != nil {
		w.disable("write", err)
	}
}

// open creates the log 0600, appending. Called with w.mu held.
//
// It creates the logs directory too: the crossing log is usually a passenger in a
// directory the run pipeline already made, but a host daemon can be the first
// writer after a fresh install, and "the audit is missing because nobody had
// started a jail yet" is a worse answer than one MkdirAll.
func (w *writer) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f, w.size = f, st.Size()
	return nil
}

// rotate retires the active log to exactly one archived generation. Called with
// w.mu held.
//
// Rename, not copy-and-truncate: a rename is atomic, so a reader tailing the log
// never sees a half-empty file, and the archive replaces its predecessor in the
// same step — which is what makes the 2*MaxBytes ceiling a property rather than a
// promise about a reaper that would have to exist.
func (w *writer) rotate() error {
	if err := w.f.Close(); err != nil {
		w.f = nil
		return err
	}
	w.f, w.size = nil, 0
	if err := os.Rename(w.path, w.path+ArchiveSuffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return w.open()
}

// disable records the one warning and stops. Called with w.mu held.
//
// The warning goes to svcendpoint.Logger rather than to a logger of this
// package's own, so it lands in whatever file the daemon's diagnostics already go
// to (the supervisor redirects each host service's stderr into
// logs/host-service-<name>.log) and reads beside the transport lines it concerns.
func (w *writer) disable(what string, err error) {
	w.disabled = true
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	svcendpoint.Logger.Printf("crossing audit: cannot %s %s: %v — this is AUDIT ONLY, "+
		"crossings continue unaffected and unrecorded", what, w.path, err)
}

// close releases the file. For tests; a host process holds the log until it exits,
// which is what an append-only audit log wants.
func (w *writer) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
}
