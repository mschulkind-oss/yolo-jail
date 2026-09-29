package openaiauthhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// OQ-CDX1's host arm (codexdaemon.go; docs/research/codex-background-service.md): in a
// `yolo host -- codex` launch Codex's background server stays off — the managed config's key,
// --no-daemon on the argv, and a one-time shutdown of what an earlier launch's daemon left in the
// managed home — and OQ-CDX2's half: the user's own ~/.codex is never read or changed for it.
// Every test goes through the real prepare, so deleting a call site fails one of them. No Codex
// runs: the "daemon" processes are `sleep`, and ps is stood in for.

// managedDaemonDeps is a logged-in broker over a scratch home and store, with procs standing in
// for ps (nil: the real one).
func managedDaemonDeps(root string, procs *daemonProcs) deps {
	return deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			if request.(map[string]any)["action"] == "status" {
				return json.RawMessage(`{"logged_in":true}`), nil
			}
			return codexViewFixture(), nil
		},
		listen:    net.Listen,
		home:      func() string { return filepath.Join(root, "home") },
		storage:   func() string { return filepath.Join(root, "store") },
		workspace: func() (string, error) { return filepath.Join(root, "work"), nil },
		newToken:  func() (string, error) { return strings.Repeat("5a", 32), nil },
		procs:     procs,
	}
}

func managedCodexHome(root string) string {
	return filepath.Join(root, "store", "host-agents", "codex")
}

// endLaunch runs the launch's (trivial) agent, which releases its live lock and adapter.
func endLaunch(t *testing.T, l *Launch) {
	t.Helper()
	if rc, handled := l.Run("/bin/sh", []string{"sh", "-c", "true"}, os.Environ(), nil, io.Discard, io.Discard); !handled || rc != 0 {
		t.Fatalf("Run = %d, %v", rc, handled)
	}
}

func TestWithoutDaemonAddsTheFlagOnlyWhereCodexAcceptsIt(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want []string // nil: unchanged
	}{
		{[]string{"codex"}, []string{"codex", "--no-daemon"}},
		{[]string{"codex", "fix the bug"}, []string{"codex", "--no-daemon", "fix the bug"}},
		{[]string{"codex", "-m", "gpt-6.1-sol"}, []string{"codex", "--no-daemon", "-m", "gpt-6.1-sol"}},
		{[]string{"codex", "resume", "--last"}, []string{"codex", "--no-daemon", "resume", "--last"}},
		{[]string{"codex", "fork", "--last"}, []string{"codex", "--no-daemon", "fork", "--last"}},
		{[]string{"codex", "exec", "hi"}, []string{"codex", "--no-daemon", "exec", "hi"}},
		{[]string{"codex", "login"}, []string{"codex", "--no-daemon", "login"}},
		// Codex refuses the flag with these (codexdaemon.go names where).
		{[]string{"codex", "agents"}, nil},
		{[]string{"codex", "queue", "019a", "next"}, nil},
		{[]string{"codex", "--remote", "ws://127.0.0.1:4222"}, nil},
		{[]string{"codex", "--remote=ws://127.0.0.1:4222"}, nil},
		{[]string{"codex", "resume", "--remote", "unix://"}, nil},
		{[]string{"codex", "archive", "s", "--remote", "unix://"}, nil},
		// Already there, in either spelling.
		{[]string{"codex", "--no-daemon"}, nil},
		{[]string{"codex", "resume", "--no-daemon"}, nil},
		{[]string{"codex", "--no-daemon=true"}, nil},
	} {
		got, added := withoutDaemon(tc.argv)
		want := tc.want
		if want == nil {
			want = tc.argv
		}
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") || added != (tc.want != nil) {
			t.Errorf("withoutDaemon(%q) = %q, added=%v; want %q", tc.argv, got, added, want)
		}
	}
}

// A MANAGED CODEX LAUNCH RUNS WITH --no-daemon, AND SAYS SO; a pi launch's argv is its own.
func TestAManagedCodexLaunchRunsWithoutTheBackgroundServer(t *testing.T) {
	root := t.TempDir()
	launch, err := prepare(managedDaemonDeps(root, nil), codexPrelaunch, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer endLaunch(t, launch)
	argv, disclosure := launch.Argv([]string{"codex", "fix it"})
	if strings.Join(argv, " ") != "codex --no-daemon fix it" {
		t.Errorf("the managed Codex launch runs %q, want --no-daemon after argv[0]", argv)
	}
	text := strings.Join(disclosure, "\n")
	for _, want := range []string{"yolo CHANGED the command you asked for:",
		"you asked for: codex 'fix it'", "yolo will run: codex --no-daemon 'fix it'", "--no-daemon"} {
		if !strings.Contains(text, want) {
			t.Errorf("the rewrite's disclosure lacks %q:\n%s", want, text)
		}
	}
	if argv, disclosure := launch.Argv([]string{"codex", "agents"}); strings.Join(argv, " ") != "codex agents" || disclosure != nil {
		t.Errorf("`codex agents` refuses --no-daemon, yet the launch runs %q (disclosure %q)", argv, disclosure)
	}

	pi, err := prepare(managedDaemonDeps(t.TempDir(), nil), piPrelaunch, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if argv, disclosure := pi.Argv([]string{"pi"}); strings.Join(argv, " ") != "pi" || disclosure != nil {
		t.Errorf("a pi launch's argv was rewritten to %q", argv)
	}
}

// THE MANAGED CONFIG TURNS THE BACKGROUND SERVER OFF whatever the user's own config says, and
// keeps the user's other [features] keys; the user's own config is not changed (OQ-CDX2).
func TestTheManagedCodexConfigTurnsTheBackgroundServerOff(t *testing.T) {
	root := t.TempDir()
	ordinary := filepath.Join(root, "home", ".codex")
	if err := os.MkdirAll(ordinary, 0o700); err != nil {
		t.Fatal(err)
	}
	userConfig := "[features]\ndaemon_auto_start = true\nweb_search_request = true\n"
	if err := os.WriteFile(filepath.Join(ordinary, "config.toml"), []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	launch, err := prepare(managedDaemonDeps(root, nil), codexPrelaunch, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer endLaunch(t, launch)
	managed, err := tomlx.DecodeFile(filepath.Join(managedCodexHome(root), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	features, _ := managed["features"].(map[string]any)
	if v, ok := features["daemon_auto_start"]; !ok || v != false {
		t.Errorf("managed config features.daemon_auto_start = %v (present=%v), want false: %v", v, ok, managed)
	}
	if features["web_search_request"] != true {
		t.Errorf("the managed config dropped the user's other [features] key: %v", features)
	}
	if got, _ := os.ReadFile(filepath.Join(ordinary, "config.toml")); string(got) != userConfig {
		t.Errorf("the user's own config.toml changed:\n%s", got)
	}
}

// THE UPDATER IS TURNED OFF IN THE MANAGED HOME'S DAEMON SETTINGS, every other key kept, where a
// daemon ever ran; a home no daemon ever ran in gets no daemon directory at all.
func TestTheManagedHomesDaemonUpdaterIsTurnedOffKeepingItsOtherSettings(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(managedCodexHome(root), daemonStateDir)
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(state, daemonSettingsFile)
	before := `{"remoteControlEnabled":true,"shutdownGraceSeconds":10,"updater":{"updateIntervalMinutes":30}}`
	if err := os.WriteFile(settings, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	launch, err := prepare(managedDaemonDeps(root, nil), codexPrelaunch, &errw)
	if err != nil {
		t.Fatal(err)
	}
	endLaunch(t, launch)
	var got map[string]any
	raw, _ := os.ReadFile(settings)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("settings.json: %v\n%s", err, raw)
	}
	updater, _ := got["updater"].(map[string]any)
	if updater["autoUpdateEnabled"] != false || updater["updateIntervalMinutes"] != float64(30) {
		t.Errorf("updater = %v, want autoUpdateEnabled false beside the kept interval", updater)
	}
	if got["remoteControlEnabled"] != true || got["shutdownGraceSeconds"] != float64(10) {
		t.Errorf("the merge dropped a key it does not own: %s", raw)
	}
	if !strings.Contains(errw.String(), "turned Codex's background updater off") {
		t.Errorf("the settings write was not said:\n%s", errw.String())
	}
	// Once written, a later launch writes nothing and says nothing.
	errw.Reset()
	again, err := prepare(managedDaemonDeps(root, nil), codexPrelaunch, &errw)
	if err != nil {
		t.Fatal(err)
	}
	endLaunch(t, again)
	if after, _ := os.ReadFile(settings); !bytes.Equal(after, raw) || strings.Contains(errw.String(), "updater") {
		t.Errorf("a second launch rewrote the settings or spoke of them:\n%s\n%s", after, errw.String())
	}

	fresh := t.TempDir()
	first, err := prepare(managedDaemonDeps(fresh, nil), codexPrelaunch, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	endLaunch(t, first)
	if _, err := os.Stat(filepath.Join(managedCodexHome(fresh), daemonStateDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a home no daemon ran in got a daemon directory: %v", err)
	}
}

func TestTurnUpdaterOffLeavesAFileItCannotReadAsItIs(t *testing.T) {
	for _, body := range []string{`not json`, `["a"]`, `{"updater":true}`} {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		changed, err := turnUpdaterOff(path)
		if err == nil || changed {
			t.Errorf("%s: turnUpdaterOff = %v, %v; want an error and no write", body, changed, err)
		}
		if got, _ := os.ReadFile(path); string(got) != body {
			t.Errorf("%s: the file was changed to %s", body, got)
		}
	}
}

// fakeDaemon is a real process standing in for Codex's server or updater: `sleep`, leading its
// own process group as a setsid'd daemon does.
type fakeDaemon struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func startFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d := &fakeDaemon{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(d.done) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-d.done
	})
	return d
}

func (d *fakeDaemon) pid() int { return d.cmd.Process.Pid }

// ended reports whether the process exits within a short wait.
func (d *fakeDaemon) ended() bool {
	select {
	case <-d.done:
		return true
	case <-time.After(3 * time.Second):
		return false
	}
}

// running reports whether it is still running after a moment.
func (d *fakeDaemon) running() bool {
	select {
	case <-d.done:
		return false
	case <-time.After(200 * time.Millisecond):
		return true
	}
}

// writeRecord writes one pid record the way Codex does (backend/pid.rs's PidRecord).
func writeRecord(t *testing.T, codexHome, name string, pid int, start string) {
	t.Helper()
	dir := filepath.Join(codexHome, daemonStateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"pid":` + strconv.Itoa(pid) + `,"processStartTime":"` + start + `"}`
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakePS answers ps for the pids it knows, as the named command started at the given time, and
// records every signal sent (then sends it for real).
type fakePS struct {
	mu      sync.Mutex
	known   map[int][2]string // pid -> {lstart, command}
	failing map[int]bool
	signals []int
}

func newFakePS() *fakePS { return &fakePS{known: map[int][2]string{}, failing: map[int]bool{}} }

func (f *fakePS) procs() *daemonProcs {
	real := realDaemonProcs()
	return &daemonProcs{
		alive: real.alive,
		facts: func(pid int) (string, string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.failing[pid] {
				return "", "", errors.New("ps: operation not permitted")
			}
			if k, ok := f.known[pid]; ok {
				return k[0], k[1], nil
			}
			return "Mon Jan  1 00:00:00 2024", "/usr/bin/something-else", nil
		},
		pgid: real.pgid,
		signal: func(pid int, sig syscall.Signal) error {
			f.mu.Lock()
			f.signals = append(f.signals, pid)
			f.mu.Unlock()
			return syscall.Kill(pid, sig)
		},
	}
}

const recordedStart = "Tue Sep 29 18:13:01 2026"

// A SERVER AND AN UPDATER AN EARLIER LAUNCH LEFT IN THE MANAGED HOME ARE STOPPED — the server by
// its pid, the updater by its process group, as Codex's own stop does — and said, and the daemon's
// package copy goes at the next launch, once nothing its records name is running.
func TestALeftoverDaemonInTheManagedHomeIsStoppedAndItsCopyReclaimed(t *testing.T) {
	root := t.TempDir()
	home := managedCodexHome(root)
	server, updater := startFakeDaemon(t), startFakeDaemon(t)
	ps := newFakePS()
	ps.known[server.pid()] = [2]string{recordedStart, "/home/u/.codex/packages/app-server-daemon/current/bin/codex app-server --listen unix:// --managed-daemon"}
	ps.known[updater.pid()] = [2]string{recordedStart, "codex app-server daemon pid-update-loop"}
	writeRecord(t, home, "daemon.pid", server.pid(), recordedStart)
	writeRecord(t, home, "daemon-updater.pid", updater.pid(), recordedStart)
	copyDir := filepath.Join(home, daemonPackagesDir, "releases", "0.158.0-x86_64-unknown-linux-musl", "bin")
	if err := os.MkdirAll(copyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "codex"), make([]byte, 3<<20), 0o700); err != nil {
		t.Fatal(err)
	}

	var errw bytes.Buffer
	launch, err := prepare(managedDaemonDeps(root, ps.procs()), codexPrelaunch, &errw)
	if err != nil {
		t.Fatal(err)
	}
	endLaunch(t, launch)
	if !server.ended() || !updater.ended() {
		t.Fatalf("the leftover daemon survived the launch:\n%s", errw.String())
	}
	if len(ps.signals) != 2 || ps.signals[0] != server.pid() || ps.signals[1] != -updater.pid() {
		t.Errorf("signals went to %v, want the server's pid %d then the updater's group %d",
			ps.signals, server.pid(), -updater.pid())
	}
	for _, want := range []string{"stopped Codex's background server (pid " + strconv.Itoa(server.pid()),
		"stopped Codex's background updater (pid " + strconv.Itoa(updater.pid())} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the stop was not said (%q):\n%s", want, errw.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, daemonPackagesDir)); err != nil {
		t.Fatalf("the copy went while the processes it records were still stopping: %v", err)
	}

	errw.Reset()
	next, err := prepare(managedDaemonDeps(root, ps.procs()), codexPrelaunch, &errw)
	if err != nil {
		t.Fatal(err)
	}
	endLaunch(t, next)
	if _, err := os.Stat(filepath.Join(home, daemonPackagesDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the daemon's package copy survived a launch with nothing of it running: %v\n%s", err, errw.String())
	}
	if !strings.Contains(errw.String(), "removed Codex's background-server copy") || !strings.Contains(errw.String(), "3 MiB") {
		t.Errorf("the removal was not said with its size:\n%s", errw.String())
	}
	if len(ps.signals) != 2 {
		t.Errorf("the second launch signalled again: %v", ps.signals)
	}
}

// A RECORD THAT DOES NOT PROVE ITS PROCESS IS LEFT ALONE: an app server whose start time differs
// (a reused pid, or the same daemon read in another time zone), or a process that is not Codex's
// app server, is never signalled. A process ps cannot describe is not signalled either, and it
// keeps the package copy in place (so does the first; TestALiveAppServerWhoseStartTimeDiffersKeepsItsCopy).
func TestADaemonRecordThatDoesNotMatchItsProcessIsNeverSignalled(t *testing.T) {
	root := t.TempDir()
	home := managedCodexHome(root)
	reused, other, opaque := startFakeDaemon(t), startFakeDaemon(t), startFakeDaemon(t)
	ps := newFakePS()
	ps.known[reused.pid()] = [2]string{"Wed Sep 30 09:00:00 2026", "codex app-server --listen unix://"}
	ps.known[other.pid()] = [2]string{recordedStart, "/usr/bin/vim notes.txt"}
	ps.failing[opaque.pid()] = true
	writeRecord(t, home, "daemon.pid", reused.pid(), recordedStart)
	writeRecord(t, home, "app-server.pid", other.pid(), recordedStart)
	writeRecord(t, home, "daemon-updater.pid", opaque.pid(), recordedStart)
	if err := os.MkdirAll(filepath.Join(home, daemonPackagesDir), 0o700); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	launch, err := prepare(managedDaemonDeps(root, ps.procs()), codexPrelaunch, &errw)
	if err != nil {
		t.Fatal(err)
	}
	endLaunch(t, launch)
	if len(ps.signals) != 0 || !reused.running() || !other.running() || !opaque.running() {
		t.Fatalf("a process the records do not prove to be Codex's was signalled: %v\n%s", ps.signals, errw.String())
	}
	if _, err := os.Stat(filepath.Join(home, daemonPackagesDir)); err != nil {
		t.Errorf("the copy went while ps could not say what a recorded pid is: %v", err)
	}
	if !strings.Contains(errw.String(), "left pid "+strconv.Itoa(opaque.pid())+" alone") {
		t.Errorf("the undecidable record was not named:\n%s", errw.String())
	}
}

// A LIVE APP SERVER WHOSE START TIME IS NOT THE RECORD'S IS LEFT RUNNING, AND SO IS ITS COPY. The
// time `ps` prints is local time in the caller's locale, and Codex recorded it from the
// environment of whichever launch started the daemon, so a different time zone, locale or clock
// step gives a live daemon a start time that no longer matches. Codex's own check calls that
// "cannot verify … PID record retained" (backend/pid.rs, process_matches_record), not stale. Taking
// it for stale deleted the package copy from under a server still running from it, and said
// "nothing yolo launches runs it". Here the record is the only one, so nothing else holds the copy.
func TestALiveAppServerWhoseStartTimeDiffersKeepsItsCopy(t *testing.T) {
	root := t.TempDir()
	home := managedCodexHome(root)
	server := startFakeDaemon(t)
	ps := newFakePS()
	ps.known[server.pid()] = [2]string{"Tue Sep 29 22:13:01 2026",
		"/home/u/.codex/packages/app-server-daemon/current/bin/codex app-server --listen unix:// --managed-daemon"}
	writeRecord(t, home, "daemon.pid", server.pid(), recordedStart)
	if err := os.MkdirAll(filepath.Join(home, daemonPackagesDir, "releases"), 0o700); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	launch, err := prepare(managedDaemonDeps(root, ps.procs()), codexPrelaunch, &errw)
	if err != nil {
		t.Fatal(err)
	}
	endLaunch(t, launch)
	if len(ps.signals) != 0 || !server.running() {
		t.Fatalf("a daemon whose record cannot be verified was signalled: %v\n%s", ps.signals, errw.String())
	}
	if _, err := os.Stat(filepath.Join(home, daemonPackagesDir)); err != nil {
		t.Errorf("the package copy went while an app server its record may name is running: %v\n%s", err, errw.String())
	}
	if !strings.Contains(errw.String(), "left pid "+strconv.Itoa(server.pid())+" alone") ||
		!strings.Contains(errw.String(), "start time") {
		t.Errorf("the unverifiable record was not named with its reason:\n%s", errw.String())
	}
	if strings.Contains(errw.String(), "removed Codex's background-server copy") {
		t.Errorf("the launch said it removed the copy:\n%s", errw.String())
	}
}

// ANOTHER LIVE LAUNCH OF THE HOME KEEPS ITS DAEMON: an older yolo's session may still be attached,
// so a launch that is not alone stops nothing; the first launch that is alone does.
func TestAnotherLiveLaunchOfTheManagedHomeKeepsItsDaemonRunning(t *testing.T) {
	root := t.TempDir()
	home := managedCodexHome(root)
	ps := newFakePS()
	first, err := prepare(managedDaemonDeps(root, ps.procs()), codexPrelaunch, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	server := startFakeDaemon(t)
	ps.known[server.pid()] = [2]string{recordedStart, "codex app-server --listen unix://"}
	writeRecord(t, home, "daemon.pid", server.pid(), recordedStart)

	second, err := prepare(managedDaemonDeps(root, ps.procs()), codexPrelaunch, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !server.running() || len(ps.signals) != 0 {
		t.Fatalf("a launch beside another live one stopped the daemon: %v", ps.signals)
	}
	endLaunch(t, second)
	endLaunch(t, first)

	third, err := prepare(managedDaemonDeps(root, ps.procs()), codexPrelaunch, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	endLaunch(t, third)
	if !server.ended() {
		t.Errorf("the first launch alone in the home did not stop the leftover daemon")
	}
}

// OQ-CDX2: THE USER'S OWN ~/.codex IS NEVER TOUCHED. A daemon the user started there — running,
// recorded exactly as Codex records it, with its settings and its package copy — is not signalled,
// its settings are not written and its copy is not removed, by a managed launch.
func TestAManagedLaunchNeverTouchesTheUsersOwnCodexDaemon(t *testing.T) {
	root := t.TempDir()
	ordinary := filepath.Join(root, "home", ".codex")
	users := startFakeDaemon(t)
	ps := newFakePS()
	ps.known[users.pid()] = [2]string{recordedStart, "codex app-server --listen unix://"}
	writeRecord(t, ordinary, "daemon.pid", users.pid(), recordedStart)
	writeRecord(t, ordinary, "daemon-updater.pid", users.pid(), recordedStart)
	settings := filepath.Join(ordinary, daemonStateDir, daemonSettingsFile)
	if err := os.WriteFile(settings, []byte(`{"remoteControlEnabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ordinary, daemonPackagesDir, "releases"), 0o700); err != nil {
		t.Fatal(err)
	}
	launch, err := prepare(managedDaemonDeps(root, ps.procs()), codexPrelaunch, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	endLaunch(t, launch)
	if len(ps.signals) != 0 || !users.running() {
		t.Errorf("the user's own Codex daemon was signalled: %v", ps.signals)
	}
	if got, _ := os.ReadFile(settings); string(got) != `{"remoteControlEnabled":true}` {
		t.Errorf("the user's own daemon settings were written: %s", got)
	}
	if _, err := os.Stat(filepath.Join(ordinary, daemonPackagesDir)); err != nil {
		t.Errorf("the user's own daemon copy was removed: %v", err)
	}
}
