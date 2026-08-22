package hostprocesses

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
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
	t.Setenv("PATH", fakePSDir+":"+os.Getenv("PATH"))
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

// TestBlackboxPidModeNotAllowlisted is the one test here that reads something
// outside its own temp dirs: handlePid resolves a pid's comm through
// /proc/<pid>/comm. Without procfs that read fails and the daemon takes the
// exit-1 "not found" branch instead of the exit-2 not-allowlisted branch this
// asserts — so the gate is a PLATFORM gate, not a cost gate. (-short is not the
// flag for it: -short means "do not start containers", and this starts none.)
func TestBlackboxPidModeNotAllowlisted(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("handlePid reads /proc/<pid>/comm; procfs is Linux-only")
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
// assert, and the inversion is the ruling (docs/design/pack-config-keys.md OQ-K3).
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
