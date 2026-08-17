package crossaudit

import (
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// syncBuf is a Logger destination safe to read while a daemon goroutine writes.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// captureWarnings points svcendpoint.Logger — where this package's one warning
// goes, deliberately, so an audit failure reads beside the transport lines it
// concerns — at a buffer.
func captureWarnings(t *testing.T) *syncBuf {
	t.Helper()
	buf := &syncBuf{}
	prev := svcendpoint.Logger
	svcendpoint.Logger = log.New(buf, "", 0)
	t.Cleanup(func() { svcendpoint.Logger = prev })
	return buf
}

func restoreSink(t *testing.T) {
	t.Helper()
	prev := svcendpoint.CrossingSink()
	t.Cleanup(func() { svcendpoint.SetCrossingSink(prev) })
}

func sample(outcome string) svcendpoint.Crossing {
	return svcendpoint.Crossing{
		Service: "host-processes", Jail: "yolo-host-services-a1b2c3d4",
		Via: svcendpoint.CrossingViaFront, Outcome: outcome,
		At: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC), Duration: 1500 * time.Millisecond,
		BytesIn: 41, BytesOut: 4096,
	}
}

// ---------------------------------------------------------------------------
// R3 — one destination, and it is named here
// ---------------------------------------------------------------------------

// TestDefaultPathIsOneFilePerHost pins the decision: crossings.log sits beside
// the existing per-service host-service-<name>.log files, and there is ONE of it
// per host rather than one per jail.
//
// Per-service logs answer "why is loophole X broken"; this answers "what crossed
// today", which is inherently cross-jail and cross-service. The jail is a FIELD
// on every record, so a per-jail view is one grep away, while reassembling a
// per-host view out of N per-jail files is not free.
func TestDefaultPathIsOneFilePerHost(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	want := "/home/someone/.local/share/yolo-jail/logs/crossings.log"
	if got := DefaultPath(); got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
	// Measured against the path helper rather than a second spelling of the
	// suffix, so a storage-layout move cannot leave this file behind.
	if got, want := DefaultPath(), filepath.Join(paths.GlobalStorage(), "logs", LogName); got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
	// The destination does not vary with the jail. A per-jail path would have to
	// take an argument; this one cannot.
	if strings.Contains(DefaultPath(), "yolo-host-services") {
		t.Error("DefaultPath is per-jail; the crossing log is one file per host")
	}
}

// ---------------------------------------------------------------------------
// The record on disk
// ---------------------------------------------------------------------------

// TestLineIsGreppableAndPayloadFree pins the exact field set. This is the audit
// log's schema whether or not anyone calls it one, so it is asserted rather than
// left to drift.
func TestLineIsGreppableAndPayloadFree(t *testing.T) {
	got := Line(sample(svcendpoint.CrossingAccepted))
	want := "2026-08-15T12:00:00Z crossing jail=yolo-host-services-a1b2c3d4 " +
		"service=host-processes via=front outcome=accepted reason=- " +
		"bytes_in=41 bytes_out=4096 elapsed_ms=1500\n"
	if got != want {
		t.Errorf("Line =\n  %q\nwant\n  %q", got, want)
	}

	// A rejected crossing carries its reason in the same slot.
	rej := sample(svcendpoint.CrossingRejected)
	rej.Reason = svcendpoint.CrossingReasonTokenMismatch
	if l := Line(rej); !strings.Contains(l, "outcome=rejected reason=token-mismatch") {
		t.Errorf("rejected line = %q, want an outcome/reason pair", l)
	}
}

// TestLineHasNoFieldForContent is the R5 guard as a structural claim: a Crossing
// exposes no payload, so no rendering of one can leak a byte of it. If a content
// field is ever added, this test is where the argument has to be re-made.
func TestLineHasNoFieldForContent(t *testing.T) {
	c := sample(svcendpoint.CrossingAccepted)
	c.Service = "svc"
	c.Jail = "jail"
	line := Line(c)
	for _, banned := range []string{"token", "cert", "payload", "body", "endpoint="} {
		if strings.Contains(line, banned) {
			t.Errorf("Line contains %q: %s", banned, line)
		}
	}
}

// ---------------------------------------------------------------------------
// R6 — bounded on disk
// ---------------------------------------------------------------------------

// TestRotationBoundsTheLog: the jail store and the jail home share one block
// device here, so an unbounded audit log is a disk-exhaustion bug. The active
// file is capped and rotated to exactly ONE archived generation, which is the
// whole retention story: at most 2*MaxBytes ever exists.
func TestRotationBoundsTheLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LogName)
	w := openAt(path)
	w.maxBytes = 1024 // a small cap, so the test writes bytes rather than megabytes

	line := len(Line(sample(svcendpoint.CrossingAccepted)))
	// Enough to fill the cap three times over: two rotations, so the SECOND one
	// proves the archive is replaced rather than accumulating.
	for i := 0; i < (1024/line)*3+3; i++ {
		w.record(sample(svcendpoint.CrossingAccepted))
	}
	w.close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	names := map[string]bool{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
		names[e.Name()] = true
	}
	if len(entries) != 2 {
		t.Fatalf("the log dir holds %d files (%v), want exactly 2 — the active log "+
			"and one archived generation", len(entries), names)
	}
	if !names[LogName] || !names[LogName+ArchiveSuffix] {
		t.Errorf("files = %v, want %s and %s%s", names, LogName, LogName, ArchiveSuffix)
	}
	if max := int64(2 * w.maxBytes); total > max {
		t.Errorf("the crossing log occupies %d bytes, over the %d-byte ceiling", total, max)
	}
	// The control: without it a rotation that TRUNCATED everything would also
	// pass. The most recent record must still be readable.
	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(active), "outcome=accepted") {
		t.Error("the active log holds no records after rotation")
	}
}

// TestLogFileIsPrivate: the record names jails and services rather than secrets,
// but it is the user's own account of what crossed their boundary, and it lives
// in the state dir with everything else that is 0600.
func TestLogFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), LogName)
	w := openAt(path)
	w.record(sample(svcendpoint.CrossingAccepted))
	w.close()

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("crossing log mode = %#o, want 0600", perm)
	}
}

// ---------------------------------------------------------------------------
// R4 — AUDIT-ONLY: a broken destination is not a failure mode
// ---------------------------------------------------------------------------

// TestUnopenableDestinationWarnsOnceThenGoesQuiet: a log that cannot be opened
// warns exactly once and is then silent forever. Logging the same failure per
// crossing would turn a disk problem into a log flood, which is its own outage.
func TestUnopenableDestinationWarnsOnceThenGoesQuiet(t *testing.T) {
	warnings := captureWarnings(t)

	// A regular file standing where the log's PARENT directory must be: mkdir
	// fails, open fails, and no amount of retrying will change that.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := openAt(filepath.Join(blocker, LogName))
	for i := 0; i < 5; i++ {
		w.record(sample(svcendpoint.CrossingAccepted))
	}

	got := strings.Count(warnings.String(), "crossing audit")
	if got != 1 {
		t.Errorf("logged %d warnings for an unopenable destination, want exactly 1:\n%s",
			got, warnings.String())
	}
	if !strings.Contains(warnings.String(), "crossings continue") {
		t.Errorf("the warning does not say the crossings are unaffected: %s", warnings.String())
	}
}

// TestInstallAtAnUnopenablePathCannotBreakACrossing is the test that matters most
// for this feature: with the audit destination broken, a real jail-facing
// connection still authenticates, still carries its bytes both ways, and still
// tears down cleanly.
//
// A real ServeFront and a real AF_UNIX daemon, not a stub — the failure this
// guards against (an audit sink that can fail a crossing) is only observable when
// bytes actually move.
func TestInstallAtAnUnopenablePathCannotBreakACrossing(t *testing.T) {
	restoreSink(t)
	captureWarnings(t)

	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	InstallAt(filepath.Join(blocker, LogName))
	if svcendpoint.CrossingSink() == nil {
		t.Fatal("InstallAt left no sink; the lazy open is what defers the failure")
	}

	endpoint := startEchoFront(t)
	for i, msg := range []string{"one", "two"} {
		conn, err := svcendpoint.Dial(endpoint, 5*time.Second)
		if err != nil {
			t.Fatalf("crossing %d: dial: %v", i, err)
		}
		if _, err := conn.Write([]byte(msg)); err != nil {
			t.Fatalf("crossing %d: write: %v", i, err)
		}
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		if err != nil || string(buf[:n]) != "got:"+msg {
			t.Fatalf("crossing %d: response = %q, %v — an unopenable AUDIT log broke "+
				"the DATA path", i, buf[:n], err)
		}
		_ = conn.Close()
	}
}

// TestInstalledSinkWritesRealCrossings closes the loop: a working destination
// receives what a real connection produced, so the two halves are wired and not
// merely present.
func TestInstalledSinkWritesRealCrossings(t *testing.T) {
	restoreSink(t)
	path := filepath.Join(t.TempDir(), LogName)
	InstallAt(path)

	endpoint := startEchoFront(t)
	conn, err := svcendpoint.Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	var data string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(path)
		if data = string(b); strings.Contains(data, "crossing ") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(data, "service=echo") || !strings.Contains(data, "via=front") ||
		!strings.Contains(data, "outcome=accepted") {
		t.Errorf("the crossing log holds %q, want a record of the echo front's crossing", data)
	}
	// R5, end to end: the endpoint file's token never reaches the log.
	ep, err := svcendpoint.Read(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data, ep.Token) || strings.Contains(data, "ping") {
		t.Error("the crossing log contains a secret or a payload byte")
	}
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// startEchoFront stands up a real front over a real AF_UNIX echo daemon.
//
// /tmp-rooted, NOT t.TempDir(): darwin's sun_path is 104 bytes including the NUL
// and a TMPDIR-rooted socket path overruns it — a break that shipped once and
// showed up only on check-macos as "bind: invalid argument".
func startEchoFront(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-ca-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	upstream := filepath.Join(dir, "up.sock")
	if len(upstream) > 103 {
		t.Fatalf("socket path is %d bytes, over darwin's 103-byte sun_path limit: %s",
			len(upstream), upstream)
	}
	endpoint := filepath.Join(dir, "echo.endpoint")

	ln, err := net.Listen("unix", upstream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				// The connection preamble first (svcendpoint §5.5). A daemon that
				// skipped it would echo yolo's frame back with the request glued
				// behind it — which is exactly what this echo did before, and what
				// makes "a prefix is not a concatenation" concrete.
				if _, err := svcendpoint.ReadPreamble(conn); err != nil {
					return
				}
				buf := make([]byte, 64)
				n, err := conn.Read(buf)
				if err != nil {
					return
				}
				_, _ = conn.Write(append([]byte("got:"), buf[:n]...))
			}()
		}
	}()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = svcendpoint.ServeFront(endpoint, "127.0.0.1", upstream, stop) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if svcendpoint.Probe(endpoint) {
			return endpoint
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the front never published a complete endpoint at %s", endpoint)
	return ""
}
