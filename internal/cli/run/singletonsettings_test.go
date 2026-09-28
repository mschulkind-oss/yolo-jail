package run

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// settingsEchoChildMain is a fake host-wide daemon: it reads its settings file ONCE at
// startup — the property that made the 2026-09-28 defect possible — binds its socket, and
// answers every connection (preamble first, as behind a real front) with those settings.
func settingsEchoChildMain(socketPath, settingsPath string) int {
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "settings-echo-child:", err)
		return 1
	}
	settings := strings.TrimSpace(string(raw))
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "settings-echo-child:", err)
		return 1
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return 0
		}
		go func(c net.Conn) {
			defer c.Close()
			br := bufio.NewReader(c)
			var hdr [4]byte
			if _, err := io.ReadFull(br, hdr[:]); err != nil {
				return
			}
			if _, err := io.ReadFull(br, make([]byte, binary.BigEndian.Uint32(hdr[:]))); err != nil {
				return
			}
			if _, err := br.ReadString('\n'); err != nil {
				return
			}
			_, _ = c.Write([]byte(settings + "\n"))
		}(conn)
	}
}

// settingsSingleton isolates one host-wide loophole's settings for a test: a private
// state dir (so {settings} resolves under the test's tree, never ~/.local/share), a
// private HOME for the shared log, and the spec whose argv hands the fake daemon the
// settings file. It returns the spec and a writer for that file.
func settingsSingleton(t *testing.T, name string) (*jsonx.OrderedMap, func(string)) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	stateRoot := t.TempDir()
	realStateDir := loopholes.StateDirFor
	loopholes.StateDirFor = func(n string) string { return filepath.Join(stateRoot, n) }
	t.Cleanup(func() { loopholes.StateDirFor = realStateDir })

	settingsPath := loopholes.SettingsFileFor(name)
	write := func(payload string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settingsPath, []byte(payload+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{os.Args[0], "-settings-echo-child", "{socket}", settingsPath})
	t.Cleanup(func() {
		broker.BrokerKill(broker.SingletonDeps(name, nil), syscall.SIGTERM, 2*time.Second)
		_ = os.Remove(paths.HostSingletonLock(name))
	})
	return spec, write
}

// launchSingleton runs this jail's half of a launch against the host-wide loophole:
// startHostSingleton, the real ensure and the real spawn, with its own sockets dir.
func launchSingleton(t *testing.T, name string, spec *jsonx.OrderedMap) (loopholeDaemon, bool, string) {
	t.Helper()
	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	h, ok := o.startHostSingleton(name, spec, socketsDir, "127.0.0.1", hostScopedDaemon())
	return h, ok, buf.String()
}

func singletonPID(t *testing.T, name string) int {
	t.Helper()
	pid, ok := broker.BrokerReadPID(broker.SingletonDeps(name, nil))
	if !ok {
		t.Fatalf("no pid file for %s", name)
	}
	return pid
}

// TestHostSingletonRestartsWhenItsSettingsChange drives the defect the maintainer hit on
// 2026-09-28 through the real spawn path: the settings changed, the launch rewrote the
// file, and the launch REUSED the live daemon, which kept serving the old profile.
//
// Four claims, one fixture:
//   - an unchanged config reuses the daemon (same pid, nothing said);
//   - a changed one restarts it, and this launch's jail gets the new settings;
//   - the one line that says so names the KEY and never a value;
//   - ANOTHER JAIL'S FRONT, opened before the restart and never touched since, reaches the
//     new daemon on its next request. That is the evidence the restart is safe for the jails
//     sharing it (host-daemon-ownership.md HD-D2): each front owns its certificate and token
//     and dials the daemon's socket afresh per connection.
func TestHostSingletonRestartsWhenItsSettingsChange(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process on a host-wide singleton socket")
	}
	name := "yjtest-settings-restart"
	spec, writeSettings := settingsSingleton(t, name)
	writeSettings(`{"profile":"first-secret-value"}`)

	first, ok, out := launchSingleton(t, name, spec)
	if !ok {
		t.Fatalf("the first launch did not come up: %q", out)
	}
	defer first.stop()
	if got := dialFrontLine(t, first.hostPath, "settings"); got != `{"profile":"first-secret-value"}` {
		t.Fatalf("first launch's daemon serves %q", got)
	}
	pid := singletonPID(t, name)

	// Unchanged: reused.
	same, ok, out := launchSingleton(t, name, spec)
	if !ok {
		t.Fatalf("the unchanged relaunch did not come up: %q", out)
	}
	same.stop()
	if got := singletonPID(t, name); got != pid {
		t.Errorf("an unchanged config restarted the daemon (pid %d → %d)", pid, got)
	}
	if strings.Contains(out, "Restarting") {
		t.Errorf("an unchanged config printed a restart: %q", out)
	}

	// Changed: restarted, said once, keys only.
	writeSettings(`{"profile":"second-secret-value"}`)
	next, ok, out := launchSingleton(t, name, spec)
	if !ok {
		t.Fatalf("the relaunch with new settings did not come up: %q", out)
	}
	defer next.stop()
	if got := singletonPID(t, name); got == pid {
		t.Fatalf("the daemon was reused with stale settings (pid still %d); output: %q", pid, out)
	}
	if !strings.Contains(out, "Restarting the host-wide daemon for '"+name+"'") ||
		!strings.Contains(out, "(profile)") {
		t.Errorf("the restart line does not name the daemon and the changed key: %q", out)
	}
	for _, value := range []string{"first-secret-value", "second-secret-value"} {
		if strings.Contains(out, value) {
			t.Errorf("launch output prints the setting VALUE %q: %q", value, out)
		}
	}
	if got := dialFrontLine(t, next.hostPath, "settings"); got != `{"profile":"second-secret-value"}` {
		t.Errorf("the relaunched jail's daemon serves %q, want the new settings", got)
	}
	// The other jail: its front predates the restart and was never re-created.
	if got := dialFrontLine(t, first.hostPath, "settings"); got != `{"profile":"second-secret-value"}` {
		t.Errorf("the earlier jail's front, after the restart, reaches %q — its credential "+
			"path did not recover", got)
	}
}

// TestHostSingletonRefusesAStaleDaemonItCannotRestart: when the ensure cannot take the
// spawn lock it may not kill (that would race another launch's spawn), so it cannot replace
// a daemon known to run other settings. Fronting that daemon anyway is serving stale
// settings silently, so the launch refuses THIS jail's front instead and names the remedy.
func TestHostSingletonRefusesAStaleDaemonItCannotRestart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process on a host-wide singleton socket")
	}
	name := "yjtest-settings-locked"
	spec, writeSettings := settingsSingleton(t, name)
	writeSettings(`{"profile":"old-secret-value"}`)
	first, ok, out := launchSingleton(t, name, spec)
	if !ok {
		t.Fatalf("the first launch did not come up: %q", out)
	}
	defer first.stop()
	pid := singletonPID(t, name)

	writeSettings(`{"profile":"new-secret-value"}`)
	lock := paths.HostSingletonLock(name)
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(lock) })

	_, ok, out = launchSingleton(t, name, spec)
	if ok {
		t.Fatalf("a daemon known to run other settings was fronted anyway: %q", out)
	}
	if got := singletonPID(t, name); got != pid {
		t.Errorf("the daemon was killed without the spawn lock (pid %d → %d)", pid, got)
	}
	for _, want := range []string{"Refusing to use the host-wide daemon for '" + name + "'",
		"(profile)", broker.CycleCommand(name)} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal %q lacks %q", out, want)
		}
	}
	if strings.Contains(out, "secret-value") {
		t.Errorf("the refusal prints a setting value: %q", out)
	}
}

// TestAttachNotesSingletonSettingsDrift pins the attach's call site. An attach never
// restarts a daemon (it runs above the config-change approval gate), so the note is the
// whole of what it does about a changed setting, and deleting the call would leave an
// attach silent about a daemon serving settings the config no longer says.
func TestAttachNotesSingletonSettingsDrift(t *testing.T) {
	if !callsIn(funcDecl(t, "run.go", "attachExisting"))["noteSingletonSettingsDrift"] {
		t.Error("attachExisting no longer calls noteSingletonSettingsDrift")
	}
	if callsIn(funcDecl(t, "singletonsettings.go", "noteSingletonSettingsDrift"))["EnsureSingleton"] {
		t.Error("the attach note ensures the daemon — an attach must report, never restart")
	}
}

// TestHostSingletonGoesThroughTheSettingsAwareEnsure pins startHostSingleton to the ensure
// that can report a stale daemon it could not replace. BrokerSpawn returns only a path, so
// a call site moved back to it would front such a daemon with no word.
func TestHostSingletonGoesThroughTheSettingsAwareEnsure(t *testing.T) {
	calls := callsIn(funcDecl(t, "loopholesruntime.go", "startHostSingleton"))
	if !calls["EnsureSingleton"] || calls["BrokerSpawn"] {
		t.Errorf("startHostSingleton must ensure through broker.EnsureSingleton, not BrokerSpawn: %v", calls)
	}
}
