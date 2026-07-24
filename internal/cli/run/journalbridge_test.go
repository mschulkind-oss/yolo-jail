package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/journald"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
// bind /…/journal.sock, and return a handle named "journal" carrying the jail
// mount path + env var. Before the fix, startLoopholes had no journal step at
// all and this handle never existed.
func TestStartJournalStartsBridge(t *testing.T) {
	socketsDir := t.TempDir()
	cfg := jsonx.NewOrderedMap()
	cfg.Set("journal", "user")

	o := &Options{}
	fillDefaults(o)

	h, ok := o.startJournal(socketsDir, cfg)
	if !ok {
		t.Fatal(`startJournal returned ok=false for journal:"user"; the bridge never spawned`)
	}
	defer h.stop()

	if h.name != "journal" {
		t.Errorf("handle name = %q, want journal", h.name)
	}
	wantSock := filepath.Join(socketsDir, "journal.sock")
	if h.hostSocketPath != wantSock {
		t.Errorf("hostSocketPath = %q, want %q", h.hostSocketPath, wantSock)
	}
	if h.jailSocketPath != "/run/yolo-services/journal.sock" {
		t.Errorf("jailSocketPath = %q, want /run/yolo-services/journal.sock", h.jailSocketPath)
	}
	if h.envVarName != "YOLO_SERVICE_JOURNAL_SOCKET" {
		t.Errorf("envVarName = %q, want YOLO_SERVICE_JOURNAL_SOCKET", h.envVarName)
	}
	if !fileExists(wantSock) {
		t.Errorf("journal socket %q never bound", wantSock)
	}
}

// TestStartJournalSkipsWhenOff confirms the opt-out: no journal key → no
// handle, no spawn.
func TestStartJournalSkipsWhenOff(t *testing.T) {
	socketsDir := t.TempDir()
	cfg := jsonx.NewOrderedMap() // no journal key → "off"

	o := &Options{}
	fillDefaults(o)

	if _, ok := o.startJournal(socketsDir, cfg); ok {
		t.Fatal("startJournal returned a handle with journal unset; expected skip")
	}
}
