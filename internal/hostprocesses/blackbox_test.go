package hostprocesses

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// The black-box suite: drive the daemon over the REAL TRANSPORT (cert-pinned,
// token-authenticated loopback TLS) with a PATH-shimmed fake `ps`, covering
// list/tree/pid, the exit-code contract (0/1/2/3/124), the LAUNCH FREEZE (this line
// used to read "per-request config re-read", which is the behaviour OQ-K3 deleted —
// see TestBlackboxAllowlistIsFrozenAtStart, which is that test inverted),
// empty-allowlist, and the failure/edge paths (non-string mode, tree timeout, tree
// ps-nonzero-empty -> exit 0). Byte-level where the fake ps makes output
// deterministic. The daemon runs in-process (BuildHandler + hostservice.ServeEndpoint).
//
// EVERY ASSERTION BELOW IS UNCHANGED BY THE TRANSPORT MIGRATION — only how a
// connection is obtained changed. That is the proof the daemon never learns which
// transport carried its bytes.

func startDaemon(t *testing.T, cfg Config, fakePSDir string) (endpoint string, stop func()) {
	t.Helper()
	// Publish 127.0.0.1 rather than the runtime's gateway name: the client here is on
	// the same machine as the listener. Only the name inside the file differs from
	// production — the pin, the token frame and the ack are the real ones.
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	// os.MkdirTemp creates 0700, which svcendpoint requires of a directory it
	// publishes a credential into. t.TempDir() creates 0755 and is REFUSED.
	dir, err := os.MkdirTemp("/tmp", "yj-hp-bb-")
	if err != nil {
		t.Fatal(err)
	}
	endpoint = filepath.Join(dir, "hp.endpoint")
	// Prepend the fake-ps dir to PATH for the daemon's exec of `ps`.
	//
	// t.Setenv, NOT os.Setenv, and the difference is a flake. This was a global mutation
	// hand-restored inside stop() — and stop() restored PATH BEFORE closing stopCh, so the
	// daemon went on serving with the REAL ps on PATH for the whole teardown window. Any
	// exec in that window runs `ps -o pid,comm -C sway -C waykeeper`, which on a host with
	// no such processes prints NOTHING and still succeeds: rc=0 with empty stdout, which is
	// exactly what TestBlackboxFrontedListModeIsIdentical saw in CI on 2026-08-22
	// (bytes_out=9 — one 9-byte exit frame, zero stdout frames).
	//
	// t.Setenv restores in test cleanup, which runs AFTER the test's deferred stop(), so the
	// fake outlives the daemon by construction. It also PANICS if this test is ever made
	// parallel, which turns the same race into a loud failure instead of a rare empty read.
	//
	// An EMPTY fakePSDir leaves PATH alone, for bsdps_darwin_test.go's runs against the
	// host's real ps. Prepending "" would put the current directory on PATH instead.
	if fakePSDir != "" {
		t.Setenv("PATH", fakePSDir+":"+os.Getenv("PATH"))
	}
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = hostservice.ServeEndpoint(BuildHandler(cfg), endpoint, stopCh)
		close(done)
	}()
	waitEndpoint(t, endpoint)
	return endpoint, func() {
		close(stopCh)
		<-done
		os.RemoveAll(dir)
	}
}

// startFrontedDaemon is startDaemon's OTHER shape, and the pair is the point.
//
// Here the daemon publishes NOTHING: it binds a plain AF_UNIX socket
// (hostservice.ServeFrontedUnix) and yolo's own front (svcendpoint.ServeFront)
// owns the jail-facing endpoint, authenticates, prepends the connection preamble
// and splices. This is what `publishes: "socket"` looks like in production.
//
// The client below is the SAME query() the endpoint suite uses, unchanged. That
// is the demonstration: neither the daemon nor its client learns which of the two
// shapes carried the bytes.
func startFrontedDaemon(t *testing.T, cfg Config, fakePSDir string) (endpoint string, stop func()) {
	t.Helper()
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	// 0700, as svcendpoint requires of a directory it publishes a credential into.
	dir, err := os.MkdirTemp("/tmp", "yj-hp-front-")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "hp.sock")
	endpoint = filepath.Join(dir, "hp.endpoint")

	// t.Setenv for the same reason as startDaemon above: the fake must outlive the daemon,
	// and a global mutation restored inside stop() does the opposite.
	t.Setenv("PATH", fakePSDir+":"+os.Getenv("PATH"))

	daemonStop := make(chan struct{})
	daemonDone := make(chan struct{})
	go func() {
		_ = hostservice.ServeFrontedUnix(BuildHandler(cfg), sock, daemonStop)
		close(daemonDone)
	}()
	waitSocket(t, sock)

	frontStop := make(chan struct{})
	frontDone := make(chan struct{})
	go func() {
		_ = svcendpoint.ServeFront(endpoint, "127.0.0.1", sock, frontStop)
		close(frontDone)
	}()
	waitEndpoint(t, endpoint)

	return endpoint, func() {
		close(frontStop)
		<-frontDone
		close(daemonStop)
		<-daemonDone
		os.RemoveAll(dir)
	}
}

// waitSocket waits for the daemon's bind by TYPE, never by dialing: a
// connect-and-close poll is yolo's readiness-probe shape and would leave
// "conn closed without a request" lines behind it.
func waitSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the daemon never bound its socket")
}

// waitEndpoint waits for a COMPLETE, USABLE endpoint file — Probe, not existence.
func waitEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if svcendpoint.Probe(endpoint) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon never published a usable endpoint")
}

// query sends a request and returns (stdout, stderr, rc).
func query(t *testing.T, endpoint string, req map[string]any) ([]byte, []byte, int) {
	t.Helper()
	c, err := svcendpoint.Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	body, _ := json.Marshal(req)
	if err := frameproto.WriteRequest(c, body); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr []byte
	for {
		f, err := frameproto.ReadFrame(c)
		if err != nil {
			return stdout, stderr, -999
		}
		switch f.StreamID {
		case frameproto.StreamStdout:
			stdout = append(stdout, f.Payload...)
		case frameproto.StreamStderr:
			stderr = append(stderr, f.Payload...)
		case frameproto.StreamExit:
			rc, _ := frameproto.ExitCode(f.Payload)
			return stdout, stderr, rc
		}
	}
}

// accessLog redirects hostservice's package Logger into a buffer for the
// duration of one test, so the TIER-2 access line — the daemon's own record of
// what was asked and by whom — can be asserted on. Restored on cleanup; the
// tests in this file are sequential, and nothing here may run in parallel while
// a global logger is swapped.
type accessLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (a *accessLog) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.b.Write(p)
}

func (a *accessLog) String() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.b.String()
}

func captureAccessLog(t *testing.T) *accessLog {
	t.Helper()
	buf := &accessLog{}
	prev := hostservice.Logger
	hostservice.Logger = log.New(buf, "", 0)
	t.Cleanup(func() { hostservice.Logger = prev })
	return buf
}

// accessLine waits for the request line and returns it. The line is written by
// the connection goroutine's deferred summary, which runs AFTER the exit frame
// the client already read — so reading the buffer straight after query() is a
// race, and this poll is the fix.
func accessLine(t *testing.T, buf *accessLog) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(buf.String(), "\n") {
			if strings.HasPrefix(line, "jail=") {
				return line
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no access line was written within 5s; log was:\n%s", buf.String())
	return ""
}

// writeSettings writes the flat settings file yolo produces for this loophole and
// returns its path — the daemon's ONLY input now. It deliberately goes through a
// real file plus LoadSettings rather than building a Config literal: the read is
// half of what changed, and a suite that skipped it would pass against a daemon
// that could not parse what yolo writes.
func writeSettings(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// settings is writeSettings + LoadSettings: the two steps Main runs at startup,
// collapsed for the tests that only care about the resulting allowlist.
func settings(t *testing.T, content string) Config {
	t.Helper()
	return LoadSettings(writeSettings(t, t.TempDir(), content))
}

// withHostOS makes the daemon this test starts speak goos's ps, restoring the real
// host's on cleanup. It must run BEFORE startDaemon, because BuildHandler reads the
// dialect once.
//
// Every test that asserts one dialect's argv calls it. The default is runtime.GOOS, so
// without it the GNU assertions below would run the BSD arm on check-macos, where the
// fake ps's echo is not a process list and list mode answers exit 1.
func withHostOS(t *testing.T, goos string) {
	t.Helper()
	prev := hostOS
	hostOS = goos
	t.Cleanup(func() { hostOS = prev })
}

// fakePS writes a fake `ps` that echoes its argv (deterministic; real ps has
// volatile fields). Optional behavior knobs via extra shell.
func fakePS(t *testing.T, extra string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-fakeps-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	// Every invocation records itself BEFORE running the body, so a test that gets no
	// output can tell "the fake ran and produced nothing" from "the fake never ran".
	//
	// A file rather than stderr, because several tests below assert stderr BYTE-EXACTLY
	// ("unknown mode: '5'\n") and a marker there would break them. It appends, so a test
	// expecting exactly one exec can see two.
	script := "#!/bin/sh\nprintf '%s %s\\n' \"$0\" \"$*\" >> " +
		filepath.Join(dir, psInvocationLog) + "\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// psInvocationLog is where fakePS records each exec, relative to the dir fakePS returns.
const psInvocationLog = "invoked.log"

// psInvocations reports what the fake ps was actually asked to do, for a failure message.
//
// This exists because the 2026-08-22 CI flake reported only `argv = ""` and left no way to
// tell whether the fake had run at all — the one datum that would have distinguished a
// transport bug from a PATH bug, discarded by the assertion that needed it.
func psInvocations(fakePSDir string) string {
	b, err := os.ReadFile(filepath.Join(fakePSDir, psInvocationLog))
	if err != nil {
		return "(the fake ps was NEVER INVOKED — the daemon exec'd some other ps, " +
			"or none at all)"
	}
	return string(b)
}

func TestBlackboxListMode(t *testing.T) {
	withHostOS(t, "linux")
	cfg := settings(t, `{"visible":["sway","waykeeper"],"fields":["pid","comm"]}`)
	ps := fakePS(t, `echo "ARGS: $*"`+"\n")
	ep, stop := startDaemon(t, cfg, ps)
	defer stop()

	out, errOut, rc := query(t, ep, map[string]any{"mode": "list"})
	if rc != 0 {
		t.Fatalf("list rc=%d, want 0\nstderr=%q\nps invocations: %s", rc, errOut, psInvocations(ps))
	}
	// sorted comms, -C per comm. Same diagnostics as the fronted twin below: these two
	// exist to be compared, so they must FAIL comparably.
	if string(out) != "ARGS: -o pid,comm -C sway -C waykeeper\n" {
		t.Errorf("list argv = %q, want %q\nstderr=%q\nps invocations: %s",
			out, "ARGS: -o pid,comm -C sway -C waykeeper\n", errOut, psInvocations(ps))
	}
}

// TestBlackboxFrontedListModeIsIdentical is TestBlackboxListMode's twin, run
// behind yolo's front instead of on a self-published endpoint — same config, same
// fake ps, same query, and the assertion is that the answer is BYTE-IDENTICAL.
//
// Kept beside the endpoint suite rather than replacing it: one of them alone
// proves the daemon works over one transport, and only the PAIR proves the thing
// this file's header claims, that the daemon never learns which transport carried
// its bytes.
func TestBlackboxFrontedListModeIsIdentical(t *testing.T) {
	withHostOS(t, "linux")
	cfg := settings(t, `{"visible":["sway","waykeeper"],"fields":["pid","comm"]}`)
	ps := fakePS(t, `echo "ARGS: $*"`+"\n")
	ep, stop := startFrontedDaemon(t, cfg, ps)
	defer stop()

	out, errOut, rc := query(t, ep, map[string]any{"mode": "list"})
	if rc != 0 {
		t.Fatalf("fronted list rc=%d, want 0\nstderr=%q\nps invocations: %s",
			rc, errOut, psInvocations(ps))
	}
	// The failure message carries stderr AND the fake's invocation log on purpose. This
	// assertion flaked once in CI (2026-08-22) reporting only `argv = ""`, which was
	// consistent with a transport bug, a handler bug and a PATH bug at the same time —
	// and the daemon's own bytes_out=9 (one exit frame, no stdout frame) could only be
	// read afterwards, from a log nobody keeps. These two fields separate the cases.
	if string(out) != "ARGS: -o pid,comm -C sway -C waykeeper\n" {
		t.Errorf("fronted list argv = %q, want %q\nstderr=%q\nps invocations: %s",
			out, "ARGS: -o pid,comm -C sway -C waykeeper\n", errOut, psInvocations(ps))
	}
}

// TestBlackboxAccessLineIsHostAttributed is the client-side half of the
// preamble work, seen from the daemon: yolo-ps no longer sends a jail_id, and
// the access line is not the poorer for it.
//
// Two things are asserted and both are observable ONLY here, in the daemon's own
// log:
//
//   - keys= now reads "mode" rather than "jail_id,mode". keys= is the sorted
//     list of the request's top-level key NAMES, so it is the direct readout of
//     what the client put on the wire — the one place a re-added field would
//     show up without anything else changing.
//   - jail= is still populated, and with the publication directory's name. The
//     value did not move when the client stopped supplying it, because the host
//     had already taken over asserting it in the connection preamble. That is
//     what makes this deletion a no-op for operators and not a lost column.
func TestBlackboxAccessLineIsHostAttributed(t *testing.T) {
	withHostOS(t, "linux")
	logs := captureAccessLog(t)
	cfg := settings(t, `{"visible":["sway"],"fields":["pid","comm"]}`)
	ps := fakePS(t, `echo "ARGS: $*"`+"\n")
	ep, stop := startFrontedDaemon(t, cfg, ps)
	defer stop()

	// EXACTLY what cmd/yolo-ps now sends for the list default: a mode, and
	// nothing that names the caller.
	if _, _, rc := query(t, ep, map[string]any{"mode": "list"}); rc != 0 {
		t.Fatalf("list rc=%d, want 0", rc)
	}

	line := accessLine(t, logs)
	if !strings.Contains(line, "keys=mode ") {
		t.Errorf("access line keys= is not the bare mode: %q", line)
	}
	if strings.Contains(line, "keys=jail_id,mode") {
		t.Errorf("the client sent a jail_id; it must not name its own jail: %q", line)
	}
	wantJail := filepath.Base(filepath.Dir(ep))
	if !strings.Contains(line, "jail="+wantJail+" ") {
		t.Errorf("access line jail= is not the host's assertion (want %q): %q", wantJail, line)
	}
}

// TestBlackboxSpoofedJailIDIsOverridden is the NEGATIVE case, and it is kept
// rather than deleted alongside the client's jail_id: yolo-ps is not the only
// thing that can open this connection, and the guarantee is about the DAEMON,
// not about one well-behaved client. A request that names its own jail is
// attributed to the jail the host handed the endpoint to, and the lie survives
// only as a key NAME in keys= — visible as a thing that was asked, never adopted
// as a thing that is true.
func TestBlackboxSpoofedJailIDIsOverridden(t *testing.T) {
	withHostOS(t, "linux")
	logs := captureAccessLog(t)
	cfg := settings(t, `{"visible":["sway"],"fields":["pid","comm"]}`)
	ps := fakePS(t, `echo "ARGS: $*"`+"\n")
	ep, stop := startFrontedDaemon(t, cfg, ps)
	defer stop()

	if _, _, rc := query(t, ep, map[string]any{"jail_id": "i-said-so", "mode": "list"}); rc != 0 {
		t.Fatalf("list rc=%d, want 0 (an unknown extra key is not an error)", rc)
	}

	line := accessLine(t, logs)
	if strings.Contains(line, "jail=i-said-so") {
		t.Errorf("the daemon took the client's word for its identity: %q", line)
	}
	wantJail := filepath.Base(filepath.Dir(ep))
	if !strings.Contains(line, "jail="+wantJail+" ") {
		t.Errorf("access line jail= is not the host's assertion (want %q): %q", wantJail, line)
	}
	if !strings.Contains(line, "keys=jail_id,mode") {
		t.Errorf("keys= must still report the spoofed field's NAME — the line "+
			"describes what was asked: %q", line)
	}
}

func TestBlackboxEmptyAllowlistExit3(t *testing.T) {
	cfg := settings(t, `{"visible":[]}`)
	ps := fakePS(t, "echo x\n")
	ep, stop := startDaemon(t, cfg, ps)
	defer stop()
	_, stderr, rc := query(t, ep, map[string]any{"mode": "list"})
	if rc != 3 {
		t.Errorf("empty allowlist rc=%d, want 3", rc)
	}
	// The message names the CURRENT spelling and says the restart is required —
	// both halves matter at the one moment a user is about to go edit a file. The
	// retired top-level key still works, but a message naming it would teach it.
	got := string(stderr)
	for _, want := range []string{
		"loopholes.host-processes.settings.visible is empty",
		"RESTART the jail",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "host_processes.visible") {
		t.Errorf("stderr names the RETIRED spelling: %q", got)
	}
}

func TestBlackboxNonStringModeExit2(t *testing.T) {
	cfg := settings(t, `{"visible":["sway"]}`)
	ps := fakePS(t, "echo x\n")
	ep, stop := startDaemon(t, cfg, ps)
	defer stop()
	// A non-string mode (5) must be rejected exit 2, NOT silently run list.
	_, stderr, rc := query(t, ep, map[string]any{"mode": 5})
	if rc != 2 {
		t.Errorf("non-string mode rc=%d, want 2", rc)
	}
	if string(stderr) != "unknown mode: '5'\n" {
		t.Errorf("stderr = %q, want \"unknown mode: '5'\\n\"", stderr)
	}
}

func TestBlackboxUnknownModeExit2(t *testing.T) {
	cfg := settings(t, `{"visible":["sway"]}`)
	ps := fakePS(t, "echo x\n")
	ep, stop := startDaemon(t, cfg, ps)
	defer stop()
	_, stderr, rc := query(t, ep, map[string]any{"mode": "bogus"})
	if rc != 2 || string(stderr) != "unknown mode: 'bogus'\n" {
		t.Errorf("unknown mode: rc=%d stderr=%q", rc, stderr)
	}
}

func TestBlackboxTreeNonzeroEmptyExit0(t *testing.T) {
	withHostOS(t, "linux")
	cfg := settings(t, `{"visible":["sway"]}`)
	// fake ps exits 1 with EMPTY stdout -> stdout is read regardless ->
	// exit 0 empty, NOT an error.
	ps := fakePS(t, "exit 1\n")
	ep, stop := startDaemon(t, cfg, ps)
	defer stop()
	out, stderr, rc := query(t, ep, map[string]any{"mode": "tree"})
	if rc != 0 {
		t.Errorf("tree ps-nonzero-empty rc=%d, want 0 (stdout read regardless of exit)", rc)
	}
	if len(out) != 0 || len(stderr) != 0 {
		t.Errorf("tree ps-nonzero-empty out=%q stderr=%q, want empty", out, stderr)
	}
}

// TestBlackboxPidModeNotAllowlisted runs on the host's own dialect, whichever it is.
// On Linux handlePid resolves a pid's comm through /proc/<pid>/comm, the one read here
// outside the test's temp dirs; on darwin it asks ps (`ps -o ucomm= -p <pid>`), which
// is the fake below, whose "x" is not on the allowlist either. It used to skip off
// Linux, because without procfs the read failed into the exit-1 "not found" branch;
// the BSD arm is what made darwin reachable. Every other GOOS still skips, since
// neither name source exists there. (-short is not the flag for it: -short means "do
// not start containers", and this starts none.)
func TestBlackboxPidModeNotAllowlisted(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("handlePid names a pid through /proc (Linux) or BSD ps (darwin)")
	}
	cfg := settings(t, `{"visible":["definitely-not-our-comm"]}`)
	ps := fakePS(t, "echo x\n")
	ep, stop := startDaemon(t, cfg, ps)
	defer stop()
	// Our own pid's comm won't be in the allowlist -> exit 2.
	_, stderr, rc := query(t, ep, map[string]any{"mode": "pid", "pid": os.Getpid()})
	if rc != 2 {
		t.Errorf("pid not-allowlisted rc=%d, want 2 (stderr=%q)", rc, stderr)
	}
}

// TestBlackboxAllowlistIsFrozenAtStart is the INVERSION of what this suite used to
// assert, and the inversion is the ruling (docs/reference/pack-system.md OQ-K3).
//
// The old test was TestBlackboxConfigReReadBetweenRequests: it wrote an empty
// allowlist, got exit 3, rewrote the config file, and demanded that the very next
// request honor the edit. That behaviour was real and it was the hole — the same
// property that let an operator widen an allowlist without a restart let an AGENT
// widen its own, mid-session, with no launch and therefore no config-approval gate.
//
// So the assertion flips: the daemon reads its settings ONCE, and a file rewritten
// underneath a running daemon changes nothing until the jail restarts. The edit here
// is the WIDENING direction on purpose — an edit that would grant, ignored.
func TestBlackboxAllowlistIsFrozenAtStart(t *testing.T) {
	dir := t.TempDir()
	path := writeSettings(t, dir, `{"visible":[]}`)
	// Loaded once, exactly as Main does before it accepts a connection.
	cfg := LoadSettings(path)
	ps := fakePS(t, `echo "ARGS: $*"`+"\n")
	ep, stop := startDaemon(t, cfg, ps)
	defer stop()
	if _, _, rc := query(t, ep, map[string]any{"mode": "list"}); rc != 3 {
		t.Fatalf("pre-edit rc=%d, want 3 (empty allowlist)", rc)
	}
	// Widen the file under the running daemon. Nothing may change.
	writeSettings(t, dir, `{"visible":["sway"],"fields":["pid"]}`)
	out, stderr, rc := query(t, ep, map[string]any{"mode": "list"})
	if rc != 3 {
		t.Fatalf("post-edit rc=%d, want 3 — the allowlist is resolved once at launch, so "+
			"rewriting the file must NOT widen a running daemon (out=%q stderr=%q)",
			rc, out, stderr)
	}
	// Reloading is what a restart does, and it must pick the new value up — the
	// freeze is about the running process, not about the file being ignored.
	if reloaded := LoadSettings(path); len(reloaded.Visible) != 1 || reloaded.Visible[0] != "sway" {
		t.Errorf("a fresh load did not see the edit: %v — the freeze must be the daemon "+
			"holding values, not the settings file being unread", reloaded.Visible)
	}
}

// psCalls returns the argument list of each exec the fake ps recorded, in order: the
// "$*" half of each psInvocationLog line, the script's own path being the other half.
func psCalls(fakePSDir string) []string {
	b, err := os.ReadFile(filepath.Join(fakePSDir, psInvocationLog))
	if err != nil {
		return nil
	}
	var calls []string
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		_, args, _ := strings.Cut(line, " ")
		calls = append(calls, args)
	}
	return calls
}

// TestBlackboxTreeMatchesAProcessBelowTheRoot is the Linux tree-mode regression: an
// allowlisted process that is NOT a child of pid 0 must be shown, with its children.
//
// It failed on 5bdac0a8d with only the header returned. --forest draws its glyphs
// inside the comm column, so the daemon, splitting each line on whitespace, read `\_`
// as the comm of every process below the root. The name now comes from
// /proc/<pid>/comm, which is why the allowlisted row here belongs to a REAL process
// (a sleep this test starts) while the forest around it is the fake's: the line says
// `\_ sleep` and only procfs can say the comm is "sleep".
func TestBlackboxTreeMatchesAProcessBelowTheRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("GNU tree mode names a process through /proc/<pid>/comm; procfs is Linux-only")
	}
	withHostOS(t, "linux")
	sl := exec.Command("sleep", "60")
	if err := sl.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sl.Process.Kill(); _, _ = sl.Process.Wait() })
	pid := sl.Process.Pid

	header := "    PID    PPID COMMAND         COMMAND"
	root := "      1       0 init            /sbin/init"
	match := fmt.Sprintf("%7d       1  \\_ sleep         \\_ sleep 60", pid)
	// A descendant of the match. Its pid exists nowhere, so nothing but its PARENT
	// can have kept it.
	child := fmt.Sprintf("999999999 %7d      \\_ child        \\_ child", pid)
	other := "      7       1  \\_ bash          \\_ bash"
	dir := t.TempDir()
	forest := filepath.Join(dir, "forest.txt")
	if err := os.WriteFile(forest, []byte(strings.Join([]string{header, root, match, child, other}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps := fakePS(t, "cat "+shquote.Quote(forest)+"\n")
	ep, stop := startDaemon(t, settings(t, `{"visible":["sleep"]}`), ps)
	defer stop()

	out, errOut, rc := query(t, ep, map[string]any{"mode": "tree"})
	want := strings.Join([]string{header, match, child}, "\n") + "\n"
	if rc != 0 || string(out) != want {
		t.Errorf("tree = rc %d\n%s\nwant rc 0\n%s\nstderr=%q — an allowlisted process below the "+
			"root must be matched by its /proc name, not by the --forest column", rc, out, want, errOut)
	}
}

// ── The BSD arm, driven on any platform against a fake ps ──────────────────────────
//
// withHostOS(t, "darwin") is the whole switch. These pin the argv BSD ps is given and
// the Go-side selection and tree drawing that replace GNU's -C and --forest; the same
// three modes against a REAL BSD ps are bsdps_darwin_test.go's, on check-macos.

// bsdFake is a fake BSD ps that answers the snapshot query with snapshot, the
// name-free pid listing taken before it (bsdPidListArgv) with the first field of every
// snapshot line, and every other query with other (shell code), recording each call.
func bsdFake(t *testing.T, snapshotArgs, snapshot, other string) string {
	t.Helper()
	var pids []string
	for _, line := range strings.Split(snapshot, "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			pids = append(pids, f[0])
		}
	}
	return bsdFakeListing(t, strings.Join(pids, "\n")+"\n", snapshotArgs, snapshot, other)
}

// bsdFakeListing is bsdFake with the name-free listing GIVEN, for a snapshot carrying
// rows that a process name forged and the kernel's own pid list does not.
//
// The files are named through shquote.Quote: t.TempDir() is under $TMPDIR, which may
// hold a space, and a bare path in this shell text would then be two words.
func bsdFakeListing(t *testing.T, pids, snapshotArgs, snapshot, other string) string {
	t.Helper()
	dir := t.TempDir()
	snap := filepath.Join(dir, "snapshot.txt")
	listing := filepath.Join(dir, "pids.txt")
	if err := os.WriteFile(snap, []byte(snapshot), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(listing, []byte(pids), 0o644); err != nil {
		t.Fatal(err)
	}
	return fakePS(t, "case \"$*\" in\n"+
		"  '-ax -o pid=') cat "+shquote.Quote(listing)+" ;;\n"+
		"  '"+snapshotArgs+"') cat "+shquote.Quote(snap)+" ;;\n"+
		"  *) "+other+" ;;\n"+
		"esac\n")
}

// TestBlackboxBSDListModeSelectsInGo: the snapshot is matched against the allowlist in
// Go, and ONLY the matching pids reach the streamed ps, comma-joined, under the
// translated fields (comm -> ucomm). A name with spaces matches whole.
func TestBlackboxBSDListModeSelectsInGo(t *testing.T) {
	withHostOS(t, "darwin")
	cfg := settings(t, `{"visible":["sway","waykeeper","Google Chrome He"],"fields":["pid","comm"]}`)
	ps := bsdFake(t, "-ax -o pid=,ucomm=",
		"  101 sway            \n  102 bash\n  103 waykeeper\n  104 Google Chrome He\n  105 swayidle\n",
		`echo "ARGS: $*"`)
	ep, stop := startDaemon(t, cfg, ps)
	defer stop()

	out, errOut, rc := query(t, ep, map[string]any{"mode": "list"})
	if want := "ARGS: -o pid,ucomm -p 101,103,104\n"; rc != 0 || string(out) != want {
		t.Errorf("BSD list = rc %d out %q, want rc 0 out %q\nstderr=%q\nps invocations: %s",
			rc, out, want, errOut, psInvocations(ps))
	}
	if calls := psCalls(ps); len(calls) != 3 || calls[0] != "-ax -o pid=" || calls[1] != "-ax -o pid=,ucomm=" {
		t.Errorf("BSD list ran %q, want the name-free listing, the snapshot and then the one streamed ps", calls)
	}
}

// TestBlackboxBSDListModeNoMatchIsHeaderAndExit1 keeps GNU's no-match answer: the
// column header and exit 1. The header is taken from a query about the DAEMON's own
// pid and its data row is dropped; with every header suppressed there is nothing to
// show, and the row, the daemon's, must not leak.
func TestBlackboxBSDListModeNoMatchIsHeaderAndExit1(t *testing.T) {
	withHostOS(t, "darwin")
	for _, tc := range []struct {
		name, fields, other, want string
	}{
		{"header", `["pid","comm"]`, `printf '  PID UCOMM\n%s yolo\n' 4242`, "  PID UCOMM\n"},
		{"suppressed", `["pid=","comm="]`, `printf '%s yolo\n' 4242`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := settings(t, `{"visible":["sway"],"fields":`+tc.fields+`}`)
			ps := bsdFake(t, "-ax -o pid=,ucomm=", "  102 bash\n", tc.other)
			ep, stop := startDaemon(t, cfg, ps)
			defer stop()
			out, errOut, rc := query(t, ep, map[string]any{"mode": "list"})
			if rc != 1 || string(out) != tc.want {
				t.Errorf("no match = rc %d out %q, want rc 1 out %q (stderr=%q)", rc, out, tc.want, errOut)
			}
			calls := psCalls(ps)
			wantHeaderQuery := "-p " + strconv.Itoa(os.Getpid())
			if len(calls) != 3 || !strings.HasSuffix(calls[2], wantHeaderQuery) {
				t.Errorf("header query = %q, want one ending %q, the daemon's own pid", calls, wantHeaderQuery)
			}
		})
	}
}

// TestBlackboxBSDListModeSnapshotFailureIsSaid: `ps -ax` lists the daemon itself, so a
// failing ps that listed nothing is reported with its own stderr rather than turned
// into a silent exit 1.
func TestBlackboxBSDListModeSnapshotFailureIsSaid(t *testing.T) {
	withHostOS(t, "darwin")
	ps := fakePS(t, "echo 'ps: boom' >&2\nexit 1\n")
	ep, stop := startDaemon(t, settings(t, `{"visible":["sway"]}`), ps)
	defer stop()
	_, errOut, rc := query(t, ep, map[string]any{"mode": "list"})
	for _, want := range []string{"list mode failed", "ps: boom", "yolo check"} {
		if rc != 1 || !strings.Contains(string(errOut), want) {
			t.Errorf("failed snapshot = rc %d stderr %q, want rc 1 and %q: the failure, ps's "+
				"own words and the next step", rc, errOut, want)
		}
	}
}

// TestBlackboxBSDPidMode: the name comes from `ps -o ucomm= -p <pid>`, and the pid is
// one procfs has never heard of — on Linux /proc/999999999 cannot exist (pid_max is far
// below it), so a pass here is the BSD arm answering, never /proc.
func TestBlackboxBSDPidMode(t *testing.T) {
	withHostOS(t, "darwin")
	const pid = 999999999
	lookup := "-o ucomm= -p 999999999"
	for _, tc := range []struct {
		name, answer   string
		rc             int
		stdout, stderr string
	}{
		{"allowlisted", `printf 'sway            \n'`, 0, "ARGS: -o pid,ucomm,args -p 999999999\n", ""},
		{"not allowlisted", `echo bash`, 2, "", "pid 999999999 has comm='bash' which is not allowlisted\n"},
		{"not found", `exit 1`, 1, "", "pid 999999999 not found\n"},
		// BSD ps prints ucomm raw, so a file name holding a newline prints two lines. The
		// name is ALL of them: its first line alone would be a name the process does not
		// have, here an allowlisted one.
		{"newline in the name", `printf 'sway\nx               \n'`, 2, "",
			"pid 999999999 has comm='sway\\nx' which is not allowlisted\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The lookup gets tc.answer; the streamed query echoes its argv.
			ps := fakePS(t, "case \"$*\" in\n  '"+lookup+"') "+tc.answer+" ;;\n  *) echo \"ARGS: $*\" ;;\nesac\n")
			cfg := settings(t, `{"visible":["sway"],"fields":["pid","comm","args"]}`)
			ep, stop := startDaemon(t, cfg, ps)
			defer stop()
			out, errOut, rc := query(t, ep, map[string]any{"mode": "pid", "pid": pid})
			if rc != tc.rc || string(out) != tc.stdout || string(errOut) != tc.stderr {
				t.Errorf("pid mode = rc %d out %q err %q, want rc %d out %q err %q\nps invocations: %s",
					rc, out, errOut, tc.rc, tc.stdout, tc.stderr, psInvocations(ps))
			}
			if calls := psCalls(ps); len(calls) == 0 || calls[0] != lookup {
				t.Errorf("first ps = %q, want the ucomm lookup %q", calls, lookup)
			}
		})
	}
}

// TestBlackboxBSDTreeBuildsTheForest pins the BSD tree byte for byte: kept = the
// allowlisted process and every descendant; rows in tree order with --forest's glyphs
// on both name columns; args from ONE query naming exactly the kept pids; a kept
// process that vanished before that query keeps its row as `(ucomm)`.
func TestBlackboxBSDTreeBuildsTheForest(t *testing.T) {
	withHostOS(t, "darwin")
	snapshot := "" +
		"    1     0 launchd\n" +
		"  100     1 sway\n" +
		"  101   100 waybar\n" +
		"  102   101 waybar-helper\n" +
		"  103   100 foot\n" +
		"  104   103 bash\n" +
		"  200     1 Google Chrome He\n" +
		"  201   200 Google Chrome He\n" +
		"  300     1 bash\n"
	args := `printf '  100 sway --my-config\n  101 waybar -c x\n  103 foot\n  104 -bash\n'`
	ps := bsdFake(t, "-ax -o pid=,ppid=,ucomm=", snapshot, args)
	ep, stop := startDaemon(t, settings(t, `{"visible":["sway"]}`), ps)
	defer stop()

	out, errOut, rc := query(t, ep, map[string]any{"mode": "tree"})
	want := "" +
		"PID PPID UCOMM                 ARGS\n" +
		"100    1 sway                  sway --my-config\n" +
		"101  100  \\_ waybar             \\_ waybar -c x\n" +
		"102  101  |   \\_ waybar-helper  |   \\_ (waybar-helper)\n" +
		"103  100  \\_ foot               \\_ foot\n" +
		"104  103      \\_ bash               \\_ -bash\n"
	if rc != 0 || string(out) != want {
		t.Errorf("BSD tree = rc %d\n%s\nwant rc 0\n%s\nstderr=%q\nps invocations: %s",
			rc, out, want, errOut, psInvocations(ps))
	}
	calls := psCalls(ps)
	if wantCalls := []string{"-ax -o pid=", "-ax -o pid=,ppid=,ucomm=", "-o pid=,args= -p 100,101,102,103,104"}; !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("BSD tree ran %q, want %q — args are asked for the kept pids and no others", calls, wantCalls)
	}
}

// TestBlackboxBSDTreeEdges: no match is the header and exit 0 with no args query (GNU's
// kept list is the header alone), and an empty snapshot from a failing ps is exit 0 with
// no output, as TestBlackboxTreeNonzeroEmptyExit0 pins for GNU.
func TestBlackboxBSDTreeEdges(t *testing.T) {
	withHostOS(t, "darwin")
	t.Run("no match", func(t *testing.T) {
		ps := bsdFake(t, "-ax -o pid=,ppid=,ucomm=", "    1     0 launchd\n  300     1 bash\n", "echo unexpected")
		ep, stop := startDaemon(t, settings(t, `{"visible":["sway"]}`), ps)
		defer stop()
		out, _, rc := query(t, ep, map[string]any{"mode": "tree"})
		if rc != 0 || string(out) != "PID PPID UCOMM ARGS\n" {
			t.Errorf("no match = rc %d out %q, want rc 0 and the header alone", rc, out)
		}
		if calls := psCalls(ps); len(calls) != 2 {
			t.Errorf("no match ran %q, want the name-free listing and the snapshot alone", calls)
		}
	})
	t.Run("empty snapshot", func(t *testing.T) {
		ps := fakePS(t, "exit 1\n")
		ep, stop := startDaemon(t, settings(t, `{"visible":["sway"]}`), ps)
		defer stop()
		out, errOut, rc := query(t, ep, map[string]any{"mode": "tree"})
		if rc != 0 || len(out) != 0 || len(errOut) != 0 {
			t.Errorf("empty snapshot = rc %d out %q err %q, want rc 0 and nothing", rc, out, errOut)
		}
	})
}

// TestBlackboxBSDWithNoPSSaysWhatToDo: when the BSD arm cannot run ps at all, every
// mode fails with exit 1, says so, and names the next step, `yolo check` on the host,
// whose self-check asks the same ps the same question.
func TestBlackboxBSDWithNoPSSaysWhatToDo(t *testing.T) {
	withHostOS(t, "darwin")
	t.Setenv("PATH", t.TempDir())
	ep, stop := startDaemon(t, settings(t, `{"visible":["sway"]}`), "")
	defer stop()
	for _, req := range []map[string]any{{"mode": "list"}, {"mode": "pid", "pid": 1}, {"mode": "tree"}} {
		_, errOut, rc := query(t, ep, req)
		want := req["mode"].(string) + " mode failed: "
		if rc != 1 || !strings.HasPrefix(string(errOut), want) || !strings.Contains(string(errOut), "`yolo check` on the host") {
			t.Errorf("%v with no ps = rc %d stderr %q, want rc 1, %q and the next step", req, rc, errOut, want)
		}
	}
}

// TestBlackboxBSDTreeArgsFailureSaysWhatToDo: the BSD tree's second ps, the args query,
// names the same next step when it cannot start. The fake deletes itself once it has
// answered the snapshot, so that query finds no ps.
func TestBlackboxBSDTreeArgsFailureSaysWhatToDo(t *testing.T) {
	withHostOS(t, "darwin")
	ps := bsdFake(t, "-ax -o pid=,ppid=,ucomm=", "    1     0 launchd\n  100     1 sway\n", "echo unexpected")
	snapshotThenGone := "case \"$*\" in\n  '-ax -o pid=,ppid=,ucomm=') rm -f \"$0\" ;;\nesac\n"
	script := filepath.Join(ps, "ps")
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	// Appended, so it runs after the case statement has printed the snapshot.
	if err := os.WriteFile(script, append(b, snapshotThenGone...), 0o755); err != nil {
		t.Fatal(err)
	}
	// PATH is the fake and the two tools it runs, and nothing else, so that once the
	// fake is gone no other ps can answer in its place.
	tools := t.TempDir()
	for _, tool := range []string{"cat", "rm"} {
		real, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(tools, tool)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", ps+":"+tools)
	ep, stop := startDaemon(t, settings(t, `{"visible":["sway"]}`), "")
	defer stop()
	_, errOut, rc := query(t, ep, map[string]any{"mode": "tree"})
	if rc != 1 || !strings.HasPrefix(string(errOut), "tree mode failed: ") ||
		!strings.Contains(string(errOut), "`yolo check` on the host") {
		t.Errorf("BSD tree whose args query finds no ps = rc %d stderr %q, want rc 1, the failure "+
			"and the next step\nps invocations: %s", rc, errOut, psInvocations(ps))
	}
}

// TestBlackboxTreeWithNoPSSaysWhatToDo is the GNU twin of the tree case above: a tree
// whose ps cannot be started names the same next step. (List and pid mode stream their
// ps through hostservice, which words its own spawn failure.)
func TestBlackboxTreeWithNoPSSaysWhatToDo(t *testing.T) {
	withHostOS(t, "linux")
	t.Setenv("PATH", t.TempDir())
	ep, stop := startDaemon(t, settings(t, `{"visible":["sway"]}`), "")
	defer stop()
	_, errOut, rc := query(t, ep, map[string]any{"mode": "tree"})
	if rc != 1 || !strings.HasPrefix(string(errOut), "tree mode failed: ") ||
		!strings.Contains(string(errOut), "`yolo check` on the host") {
		t.Errorf("GNU tree with no ps = rc %d stderr %q, want rc 1, the failure and the next step", rc, errOut)
	}
}

// TestBlackboxBSDForgedRowsSelectNothing is the regression for a process name that
// writes rows into a BSD snapshot. BSD ps prints ucomm RAW (adv_cmds print.c, ucomm()),
// and darwin takes it from the executable's file name, which may hold a newline: a
// process run from a file named "a\n600 sway" prints a second line that claims pid 600
// is called sway. On macos-user the agent's own processes are in the host's `ps -ax`, so
// without this defense it could have the host stream any process's command line, the
// very thing Seatbelt denies it. Each case failed on ebd1ce7cf by selecting the forged
// pid. The secret-holder's pid must reach no ps the daemon runs after the snapshot; it
// is above every kernel's pid ceiling, so it can never be the daemon's own.
func TestBlackboxBSDForgedRowsSelectNothing(t *testing.T) {
	withHostOS(t, "darwin")
	for _, tc := range []struct {
		name, mode, snapshotArgs, pids, snapshot, want string
		rc                                             int
		victim                                         string
	}{
		{
			// pid 9000600 is on two rows. The kernel holds a pid once, so one is forged.
			name: "list, a second row for a live pid", mode: "list", snapshotArgs: "-ax -o pid=,ucomm=",
			pids:     "1\n500\n9000600\n",
			snapshot: "    1 launchd\n  500 a\n9000600 sway       \n  9000600 secret-holder\n",
			rc:       1, victim: "9000600",
		},
		{
			// pid 9000700 is on one row, but no process held it when the name-free listing
			// ran: whatever is born at 9000700 before the next ps would be shown.
			name: "list, a row for a pid nobody holds", mode: "list", snapshotArgs: "-ax -o pid=,ucomm=",
			pids:     "1\n500\n",
			snapshot: "    1 launchd\n  500 a\n9000700 sway       \n",
			rc:       1, victim: "9000700",
		},
		{
			// The forged row renames launchd, so every process on the machine would be
			// kept as its descendant and have its args asked for.
			name: "tree, a second row for launchd", mode: "tree", snapshotArgs: "-ax -o pid=,ppid=,ucomm=",
			pids:     "1\n400\n500\n9000600\n",
			snapshot: "    1     0 launchd\n  400     1 zsh\n  500   400 a\n1 0 sway      \n  9000600     1 secret-holder\n",
			want:     "PID PPID UCOMM ARGS\n", victim: "9000600",
		},
		{
			name: "tree, a row for a pid nobody holds", mode: "tree", snapshotArgs: "-ax -o pid=,ppid=,ucomm=",
			pids:     "1\n500\n",
			snapshot: "    1     0 launchd\n  500     1 a\n9000700 1 sway      \n",
			want:     "PID PPID UCOMM ARGS\n", victim: "9000700",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Every later query answers as if the victim existed, with its secret.
			ps := bsdFakeListing(t, tc.pids, tc.snapshotArgs, tc.snapshot,
				`printf '  PID UCOMM\n  '`+tc.victim+`' secret-holder --token=hunter2\n'`)
			ep, stop := startDaemon(t, settings(t, `{"visible":["sway"]}`), ps)
			defer stop()
			out, errOut, rc := query(t, ep, map[string]any{"mode": tc.mode})
			if rc != tc.rc || strings.Contains(string(out), "hunter2") || (tc.mode == "tree" && string(out) != tc.want) {
				t.Errorf("%s = rc %d out %q (stderr=%q), want rc %d and no forged pid shown",
					tc.mode, rc, out, errOut, tc.rc)
			}
			calls := psCalls(ps)
			if len(calls) < 2 || calls[0] != "-ax -o pid=" || calls[1] != tc.snapshotArgs {
				t.Errorf("ps ran %q, want the name-free listing and then the snapshot", calls)
			}
			for _, c := range calls[min(2, len(calls)):] {
				for _, tok := range strings.FieldsFunc(c, func(r rune) bool { return r == ' ' || r == ',' }) {
					if tok == tc.victim {
						t.Errorf("ps was asked about the forged pid %s: %q", tc.victim, c)
					}
				}
			}
		})
	}
}

// TestBlackboxBSDListModeMatchesLongNamesAsGNUDoes: GNU list mode's -C matches a name of
// 15 bytes or more on its first 15 (procps-ng 4.0.7, measured 2026-10-04), so the BSD
// list compares the first 15 bytes of both names. The full program name, the 15 bytes a
// Linux ps shows and the 16 a Mac's ps shows all find `chrome-devtools-mcp`, whose ucomm
// is `chrome-devtools-`; a shorter name still matches whole (`sway` is not `swayidle`).
func TestBlackboxBSDListModeMatchesLongNamesAsGNUDoes(t *testing.T) {
	withHostOS(t, "darwin")
	snapshot := "  101 chrome-devtools-\n  102 chrome-devtools\n  103 chrome-devtoolX\n  104 swayidle\n  105 sway\n"
	for _, tc := range []struct{ name, visible, pids string }{
		{"the full name", `"chrome-devtools-mcp"`, "101,102"},
		{"what Linux shows", `"chrome-devtools"`, "101,102"},
		{"what a Mac shows", `"chrome-devtools-"`, "101,102"},
		{"a short name is whole", `"sway"`, "105"},
		{"fourteen bytes is whole", `"chrome-devtool"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := bsdFake(t, "-ax -o pid=,ucomm=", snapshot, `echo "ARGS: $*"`)
			ep, stop := startDaemon(t, settings(t, `{"visible":[`+tc.visible+`],"fields":["pid"]}`), ps)
			defer stop()
			out, errOut, rc := query(t, ep, map[string]any{"mode": "list"})
			if tc.pids == "" {
				if rc != 1 {
					t.Errorf("list %s = rc %d out %q, want rc 1 (no match)", tc.visible, rc, out)
				}
				return
			}
			if want := "ARGS: -o pid -p " + tc.pids + "\n"; rc != 0 || string(out) != want {
				t.Errorf("list %s = rc %d out %q (stderr=%q), want rc 0 out %q", tc.visible, rc, out, errOut, want)
			}
		})
	}
}
