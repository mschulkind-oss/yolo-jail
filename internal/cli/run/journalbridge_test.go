package run

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/journald"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestMain lets startJournal's self-exec'd `yolo internal daemon journal`
// spawn resolve to THIS test binary and actually run the journal daemon, so
// startJournal's socket-bind wait succeeds in-process. execx.SelfExecArgv
// rewrites the leading "yolo" token to os.Executable() (the test binary here),
// so without this dispatch the spawn would re-exec the test binary, which would
// ignore the args and never bind the socket.
func TestMain(m *testing.M) {
	if len(os.Args) >= 4 && os.Args[1] == "internal" && os.Args[2] == "daemon" && os.Args[3] == "journal" {
		os.Exit(journald.Main(os.Args[4:]))
	}
	// `<test-binary> -front-upstream-child <mode> <socket>` is the daemon child
	// for the publishes:"socket" tests: it binds a REAL AF_UNIX socket, which no
	// portable sh one-liner can (precedent:
	// internal/oauthbroker/singletontransport_test.go's TestMain hook).
	if len(os.Args) >= 4 && os.Args[1] == "-front-upstream-child" {
		os.Exit(frontUpstreamChildMain(os.Args[2], os.Args[3]))
	}
	os.Exit(m.Run())
}

// TestResolveJournalMode pins the config→mode normalization table (mirrors the
// pre-port Python _resolve_journal_mode): true is the unprivileged-safe "user"
// default; absent/null/false/"off"/invalid collapse to "off".
func TestResolveJournalMode(t *testing.T) {
	mk := func(set bool, v any) *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		if set {
			m.Set("journal", v)
		}
		return m
	}
	cases := []struct {
		name string
		cfg  *jsonx.OrderedMap
		want string
	}{
		{"absent", mk(false, nil), "off"},
		{"null", mk(true, nil), "off"},
		{"false", mk(true, false), "off"},
		{"true_is_user", mk(true, true), "user"},
		{"off", mk(true, "off"), "off"},
		{"user", mk(true, "user"), "user"},
		{"full", mk(true, "full"), "full"},
		{"invalid", mk(true, "bogus"), "off"},
	}
	for _, tc := range cases {
		if got := resolveJournalMode(tc.cfg); got != tc.want {
			t.Errorf("%s: resolveJournalMode = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestStartJournalStartsBridge is the regression guard for the "declared but
// never spawned" class: with journal:"user" the bridge must actually spawn,
// PUBLISH /…/journal.endpoint, and return a handle named "journal" carrying the
// jail mount path + env var. Before the fix, startLoopholes had no journal step
// at all and this handle never existed.
//
// The bridge is on loopback-tls now (docs/design/loophole-transport.md §8.4), so
// what it brings up is an endpoint FILE published by journald.ServeEndpoint —
// not a socket, and not a front over one: its handler was already net.Conn-based,
// and svcendpoint's own guidance is that such a daemon takes Listen directly.
func TestStartJournalStartsBridge(t *testing.T) {
	// The journal bridge is Linux/podman-only: it forwards `journalctl`, which
	// has no macOS host analog, and startLoopholes never reaches startJournal on
	// the macOS `container` runtime (it returns before the journal step). The
	// spawn+socket-bind path is therefore only meaningful on Linux; on macOS the
	// self-exec'd daemon can't bind under $TMPDIR's long sun_path anyway.
	if runtime.GOOS != "linux" {
		t.Skip("journal bridge is Linux-only (journalctl forwarder)")
	}
	// shortSocketDir, not t.TempDir(). The endpoint file itself has no sun_path
	// limit, but the helper is kept for a second reason that outlived the socket:
	// svcendpoint REFUSES to publish into a group/world-accessible directory
	// (the file carries a bearer token), and MkdirTemp creates 0700 while a
	// hand-rolled MkdirAll would not. The short path also keeps this test honest
	// if the bridge ever regains a socket.
	socketsDir := shortSocketDir(t)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("journal", "user")

	o := &Options{}
	fillDefaults(o)

	h, ok := o.startJournal(socketsDir, cfg, "127.0.0.1")
	if !ok {
		t.Fatal(`startJournal returned ok=false for journal:"user"; the bridge never spawned`)
	}
	defer h.stop()

	if h.name != "journal" {
		t.Errorf("handle name = %q, want journal", h.name)
	}
	wantEndpoint := filepath.Join(socketsDir, "journal.endpoint")
	if h.hostPath != wantEndpoint {
		t.Errorf("hostPath = %q, want %q", h.hostPath, wantEndpoint)
	}
	if h.jailPath != "/run/yolo-services/journal.endpoint" {
		t.Errorf("jailPath = %q, want /run/yolo-services/journal.endpoint", h.jailPath)
	}
	// The _ENDPOINT spelling, and the rename is the point: the variable must
	// describe the VALUE. The retired _SOCKET name is deliberately NOT also
	// emitted — a stale baked client reading an ABSENT variable hits its own
	// clean "not wired up in this jail" exit, where one reading a same-named
	// variable whose value is now an endpoint file would dial a regular file and
	// report something obscure.
	if h.envVarName != "YOLO_SERVICE_JOURNAL_ENDPOINT" {
		t.Errorf("envVarName = %q, want YOLO_SERVICE_JOURNAL_ENDPOINT", h.envVarName)
	}
	// Probe, not existence: a truncated or older-format file would otherwise read
	// as healthy forever, so the daemon would never be respawned and the jail
	// could never reach it.
	if !svcendpoint.Probe(wantEndpoint) {
		t.Errorf("journal endpoint %q was never published in a usable form", wantEndpoint)
	}
	if fi, err := os.Stat(wantEndpoint); err != nil {
		t.Errorf("stat endpoint: %v", err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("endpoint mode = %04o, want 0600 — the file carries this jail's bearer token",
			fi.Mode().Perm())
	}
	if fileExists(filepath.Join(socketsDir, "journal.sock")) {
		t.Error("the retired journal.sock is still being bound — the flip is half-applied")
	}
}

// TestStartJournalSkipsWhenOff confirms the opt-out: no journal key → no
// handle, no spawn.
func TestStartJournalSkipsWhenOff(t *testing.T) {
	socketsDir := t.TempDir()
	cfg := jsonx.NewOrderedMap() // no journal key → "off"

	o := &Options{}
	fillDefaults(o)

	if _, ok := o.startJournal(socketsDir, cfg, "127.0.0.1"); ok {
		t.Fatal("startJournal returned a handle with journal unset; expected skip")
	}
}
