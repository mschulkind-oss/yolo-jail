package run

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostservicelifecycle_test.go drives the settings-snapshot and startup-reason lifecycles
// through their production callers (prepareLoopholeSettingsForStart, startLoopholesMatching and,
// through it, startExternalService and startHostSingleton): the private files a launch creates,
// when they go, and how a daemon that sends no startup reason is still bounded and cleaned up.

// lifecycleModuleSet writes one approved pack loophole module and returns the set a launch reads.
func lifecycleModuleSet(t *testing.T, name, manifest string) loopholes.Set {
	t.Helper()
	module := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}},
	})
	if enabled := set.Enabled(); len(enabled) != 1 || enabled[0].Name != name {
		t.Fatalf("fixture loophole %s was not selected: %+v", name, enabled)
	}
	return set
}

func lifecycleOptions(t *testing.T, timeout time.Duration) (*Options, *strings.Builder) {
	t.Helper()
	var output strings.Builder
	o := &Options{Workspace: t.TempDir()}
	fillDefaults(o)
	o.Stdout, o.Stderr = &output, &output
	o.ServiceReadyTimeout = timeout
	o.ServiceTermGrace = time.Second
	o.PathExists = func(string) bool { return false }
	return o, &output
}

// privateSettingsSnapshots lists the per-attempt private settings files in a loophole's state dir:
// both the validator's input and a per-jail daemon's snapshot are created there under this prefix.
func privateSettingsSnapshots(t *testing.T, name string) []string {
	t.Helper()
	entries, err := os.ReadDir(loopholes.StateDirFor(name))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "settings-"+loopholes.SettingsFileName+"-") {
			out = append(out, entry.Name())
		}
	}
	return out
}

// readSnapshotMarker parses the marker a shell fixture writes: the octal mode of the settings
// file it was handed, that file's path, then its bytes.
func readSnapshotMarker(t *testing.T, marker string) (mode, path, body string) {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("fixture did not record its settings input: %v", err)
	}
	parts := strings.SplitN(string(data), "\n", 3)
	if len(parts) != 3 {
		t.Fatalf("fixture marker = %q", data)
	}
	return parts[0], parts[1], parts[2]
}

// recordSettingsInput is the shell body every snapshot fixture below shares: it records the mode,
// path and bytes of the settings file it is handed as $1 into the marker $2.
const recordSettingsInput = `stat -c %a "$1" > "$2"; printf '%s\n' "$1" >> "$2"; cat "$1" >> "$2"`

// TestLaunchPreflightValidatorInputIsPrivateAndAlwaysRemoved: on every validator disposition the
// launch's preflight hands the pack's validator a unique private 0600 copy of the frozen bytes —
// secret-bearing values included — never the stable settings path, leaves that stable file exactly
// as it was, and removes the copy before returning.
func TestLaunchPreflightValidatorInputIsPrivateAndAlwaysRemoved(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses a Linux shell validator and stat(1)")
	}
	for _, tc := range []struct {
		mode   string
		accept bool
		class  string
	}{
		{mode: "accept", accept: true},
		{mode: "refuse", class: "configuration"},
		{mode: "timeout", class: string(hostservice.CommandTimedOut)},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			redirectState(t)
			const name = "validator-input"
			const secret = "secret-profile-sentinel"
			marker := filepath.Join(t.TempDir(), "validator.marker")
			validator := recordSettingsInput + `; case "$3" in accept) exit 0;; refuse) ` +
				`printf '%s' '{"reason":"fixture refused","remedy":"fix the fixture"}'; exit 1;; *) sleep 5;; esac`
			set := lifecycleModuleSet(t, name, fmt.Sprintf(`{
  "name": %q, "default_enabled": true,
  "settings": {"profile": {"type": "string", "scope": "user", "default": "default-profile"}},
  "host_daemon": {
    "cmd": ["/bin/false", "{socket}", "{settings}"],
    "settings_check": ["/bin/sh", "-c", %q, "validator", "{settings}", %q, %q],
    "publishes": "socket"
  }
}`, name, validator, marker, tc.mode))
			stable := loopholes.SettingsFileFor(name)
			previous := []byte("{\"profile\": \"previous-valid\"}\n")
			if err := loopholes.WriteSettingsBytes(stable, previous); err != nil {
				t.Fatal(err)
			}
			cfg := settingsConfig(name, secret)
			o, output := lifecycleOptions(t, time.Second)
			started := time.Now()
			ok := o.prepareLoopholeSettingsForStart(set, cfg, func(string) bool { return true })
			elapsed := time.Since(started)
			o.cleanupSettingsSnapshots()
			if ok != tc.accept {
				t.Fatalf("preflight admitted=%v, want %v: refusal=%+v", ok, tc.accept, o.startupRefusal)
			}
			if !tc.accept && (o.startupRefusal == nil || o.startupRefusal.class != tc.class) {
				t.Fatalf("preflight refusal = %+v, want class %q", o.startupRefusal, tc.class)
			}
			if tc.mode == "timeout" && elapsed > hostservice.SettingsCheckTimeout+2*time.Second {
				t.Fatalf("validator timeout was not bounded: %s", elapsed)
			}
			mode, path, body := readSnapshotMarker(t, marker)
			if mode != "600" {
				t.Errorf("validator input mode = %s, want 600", mode)
			}
			if path == stable || filepath.Dir(path) != loopholes.StateDirFor(name) {
				t.Errorf("validator input %q is the stable settings path or outside the state dir", path)
			}
			if !strings.Contains(body, `"profile": "`+secret+`"`) {
				t.Errorf("validator did not see the frozen current settings: %q", body)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("validator input survived its %s preflight: %v", tc.mode, err)
			}
			if left := privateSettingsSnapshots(t, name); len(left) != 0 {
				t.Errorf("private settings files left after %s preflight: %v", tc.mode, left)
			}
			if got, err := os.ReadFile(stable); err != nil || string(got) != string(previous) {
				t.Errorf("preflight changed the stable settings file: got=%q err=%v", got, err)
			}
			if strings.Contains(output.String(), secret) {
				t.Errorf("preflight output disclosed a setting value:\n%s", output.String())
			}
			if !tc.accept {
				return
			}
			// A second accepted preflight gets its own input path, never a reused one.
			again, _ := lifecycleOptions(t, time.Second)
			if !again.prepareLoopholeSettingsForStart(set, cfg, func(string) bool { return true }) {
				t.Fatalf("second preflight refused: %+v", again.startupRefusal)
			}
			again.cleanupSettingsSnapshots()
			if _, second, _ := readSnapshotMarker(t, marker); second == path {
				t.Errorf("two preflights shared one validator input path %q", path)
			}
		})
	}
}

// TestPerJailPrivateSnapshotIsRemovedWhenTheSpawnFails: a settings-check-enabled per-jail daemon
// whose spawn fails — the executable is missing, or it exits before readiness — leaves no private
// snapshot behind, and never had the stable settings path written for it. The exiting daemon proves
// the snapshot it was handed was private, 0600 and present while it ran.
func TestPerJailPrivateSnapshotIsRemovedWhenTheSpawnFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns host processes and uses stat(1)")
	}
	for _, tc := range []struct {
		name string
		kind hostservice.StartupKind
	}{
		{name: "missing executable", kind: hostservice.StartupKindDaemonStartFailed},
		{name: "exits before readiness", kind: hostservice.StartupKindProcessExited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redirectState(t)
			const name = "snapshot-spawn-failure"
			marker := filepath.Join(t.TempDir(), "daemon.marker")
			cmd := fmt.Sprintf(`[%q, "{settings}", %q, "{socket}"]`, "/no/such/yolo-fixture-daemon", marker)
			if tc.kind == hostservice.StartupKindProcessExited {
				cmd = fmt.Sprintf(`["/bin/sh", "-c", %q, "daemon", "{settings}", %q, "{socket}"]`,
					recordSettingsInput+"; exit 3", marker)
			}
			set := lifecycleModuleSet(t, name, fmt.Sprintf(`{
  "name": %q, "default_enabled": true,
  "settings": {"profile": {"type": "string", "scope": "user", "default": "default-profile"}},
  "host_daemon": {
    "cmd": %s,
    "settings_check": ["/bin/sh", "-c", "exit 0", "validator", "{settings}"],
    "publishes": "socket"
  }
}`, name, cmd))
			o, output := lifecycleOptions(t, 2*time.Second)
			cname := "yolo-snapshot-fail-" + strconv.FormatInt(time.Now().UnixNano(), 36)
			handles := o.startLoopholesMatching(set, cname, "podman", settingsConfig(name, "candidate-profile"),
				func(string) bool { return true })
			t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
			if len(handles) != 0 || len(o.startupOutcomes) != 1 || o.startupOutcomes[0].Kind != tc.kind {
				t.Fatalf("failed spawn: handles=%d outcomes=%+v\n%s", len(handles), o.startupOutcomes, output.String())
			}
			if left := privateSettingsSnapshots(t, name); len(left) != 0 {
				t.Fatalf("private daemon snapshot survived the failed spawn: %v", left)
			}
			if _, err := os.Stat(loopholes.SettingsFileFor(name)); !os.IsNotExist(err) {
				t.Fatalf("a settings-check-enabled per-jail start wrote the stable settings path: %v", err)
			}
			if tc.kind != hostservice.StartupKindProcessExited {
				return
			}
			mode, path, body := readSnapshotMarker(t, marker)
			if mode != "600" || path == loopholes.SettingsFileFor(name) ||
				!strings.Contains(body, `"profile": "candidate-profile"`) {
				t.Fatalf("daemon was handed mode=%s path=%q body=%q, want its private 0600 validated snapshot",
					mode, path, body)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("the snapshot the exited daemon read was not removed: %v", err)
			}
		})
	}
}

// TestLegacyPerJailManifestKeepsTheStableSettingsPath: a per-jail daemon whose manifest declares
// no settings_check is still handed the stable name-keyed settings file, written with the resolved
// settings, and no private snapshot is made for it.
func TestLegacyPerJailManifestKeepsTheStableSettingsPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process and uses stat(1)")
	}
	redirectState(t)
	const name = "legacy-settings-path"
	marker := filepath.Join(t.TempDir(), "daemon.marker")
	set := lifecycleModuleSet(t, name, fmt.Sprintf(`{
  "name": %q, "default_enabled": true,
  "settings": {"profile": {"type": "string", "scope": "user", "default": "default-profile"}},
  "host_daemon": {
    "cmd": ["/bin/sh", "-c", %q, "daemon", "{settings}", %q, "{socket}"],
    "publishes": "socket"
  }
}`, name, recordSettingsInput+"; exit 3", marker))
	o, output := lifecycleOptions(t, 2*time.Second)
	cname := "yolo-legacy-settings-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	handles := o.startLoopholesMatching(set, cname, "podman", settingsConfig(name, "legacy-profile"),
		func(string) bool { return true })
	if len(handles) != 0 {
		t.Fatalf("exiting fixture daemon reported started:\n%s", output.String())
	}
	_, path, body := readSnapshotMarker(t, marker)
	stable := loopholes.SettingsFileFor(name)
	if path != stable || !strings.Contains(body, `"profile": "legacy-profile"`) {
		t.Fatalf("legacy daemon was handed path=%q body=%q, want the stable %q with its settings", path, body, stable)
	}
	if got, err := os.ReadFile(stable); err != nil || !strings.Contains(string(got), `"profile": "legacy-profile"`) {
		t.Fatalf("legacy stable settings file = %q err=%v", got, err)
	}
	if left := privateSettingsSnapshots(t, name); len(left) != 0 {
		t.Fatalf("a legacy manifest got private snapshots: %v", left)
	}
}

// singletonLifecycleFixture is a validator-enabled host-wide loophole whose daemon reads its
// settings ONCE, before binding its socket, and answers each request with them
// (settingsEchoChildMain). Its validator refuses a snapshot naming "invalid" and stalls past its
// bound on one naming "slow".
type singletonLifecycleFixture struct {
	name string
	set  loopholes.Set
	lp   *loopholes.Loophole
	deps broker.Deps
}

func newSingletonLifecycleFixture(t *testing.T, name string) *singletonLifecycleFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	emptyLoopholeDirs(t)
	isolatePackModules(t)
	oldSingletonDir := paths.HostSingletonDir
	shortSingletonDir, err := os.MkdirTemp("/tmp", "yhs-")
	if err != nil {
		t.Fatal(err)
	}
	paths.HostSingletonDir = shortSingletonDir
	t.Cleanup(func() {
		paths.HostSingletonDir = oldSingletonDir
		_ = os.RemoveAll(shortSingletonDir)
	})
	validator := `if grep -q slow "$1"; then sleep 5; fi; if grep -q invalid "$1"; then ` +
		`printf '%s' '{"reason":"candidate rejected","remedy":"correct user settings"}'; exit 1; fi; exit 0`
	set := lifecycleModuleSet(t, name, fmt.Sprintf(`{
  "name": %q, "default_enabled": true,
  "settings": {"profile": {"type": "string", "scope": "user", "default": "good"}},
  "host_daemon": {
    "cmd": [%q, "-settings-echo-child", "{socket}", "{settings}"],
    "settings_check": ["/bin/sh", "-c", %q, "validator", "{settings}"],
    "publishes": "socket",
    "scope": "host"
  }
}`, name, os.Args[0], validator))
	lp, ok := set.Lookup(name)
	if !ok {
		t.Fatal("fixture singleton has no record")
	}
	f := &singletonLifecycleFixture{name: name, set: set, lp: lp,
		deps: broker.SingletonDeps(name, []string{os.Args[0], loopholes.SettingsFileFor(name)})}
	t.Cleanup(func() { _ = broker.BrokerKill(f.deps, syscall.SIGTERM, 2*time.Second) })
	return f
}

// frozen is the exact snapshot the launch validates and publishes for profile.
func (f *singletonLifecycleFixture) frozen(t *testing.T, profile string) string {
	t.Helper()
	cfg := settingsConfig(f.name, profile)
	bytes, _, err := loopholes.FrozenSettingsBytes(f.lp, suppliedSettings(cfgMap(cfg, "loopholes"), f.name))
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes)
}

// launch is one jail's fresh launch of the singleton: preflight, publication and the ensure.
func (f *singletonLifecycleFixture) launch(t *testing.T, profile string, seam func(*Options)) (*Options, []loopholeDaemon, string) {
	t.Helper()
	o, output := lifecycleOptions(t, 3*time.Second)
	if seam != nil {
		seam(o)
	}
	cname := "yolo-singleton-life-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	handles := o.startLoopholesMatching(f.set, cname, "podman", settingsConfig(f.name, profile),
		func(string) bool { return true })
	if len(handles) > 0 {
		t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir(cname, false), cname, "podman") })
	}
	return o, handles, output.String()
}

func (f *singletonLifecycleFixture) livePID(t *testing.T) int {
	t.Helper()
	pid, ok := broker.BrokerReadPID(f.deps)
	if !ok || !broker.BrokerIsAlive(f.deps) {
		t.Fatalf("host-wide fixture daemon is not live: pid=%d ok=%v", pid, ok)
	}
	return pid
}

// TestValidatedSingletonSettingsChangeRestartsOntoTheValidatedBytes: a VALID changed snapshot keeps
// the disclosed host-wide restart, and the replacement daemon — which reads its settings once,
// before its socket accepts — serves exactly the bytes that passed validation, to this launch's
// front and to the earlier jail's front alike.
func TestValidatedSingletonSettingsChangeRestartsOntoTheValidatedBytes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host-wide daemon and a Linux shell validator")
	}
	f := newSingletonLifecycleFixture(t, "life-restart")
	_, first, out := f.launch(t, "first-profile", nil)
	if len(first) != 1 {
		t.Fatalf("first launch did not front the singleton:\n%s", out)
	}
	firstBytes := f.frozen(t, "first-profile")
	if got := dialFrontLine(t, first[0].hostPath, "settings"); got != strings.TrimSpace(firstBytes) {
		t.Fatalf("first daemon serves %q, want the validated %q", got, firstBytes)
	}
	pid := f.livePID(t)

	_, second, out := f.launch(t, "second-profile", nil)
	if len(second) != 1 {
		t.Fatalf("valid changed launch did not front the singleton:\n%s", out)
	}
	if next := f.livePID(t); next == pid {
		t.Fatalf("a valid changed snapshot reused the daemon running the previous settings (pid %d)", pid)
	}
	if !strings.Contains(out, "Restarting the host-wide daemon for '"+f.name+"'") || !strings.Contains(out, "(profile)") {
		t.Errorf("the restart was not disclosed by key:\n%s", out)
	}
	if strings.Contains(out, "first-profile") || strings.Contains(out, "second-profile") {
		t.Errorf("launch output disclosed a setting value:\n%s", out)
	}
	secondBytes := f.frozen(t, "second-profile")
	if got, err := os.ReadFile(loopholes.SettingsFileFor(f.name)); err != nil || string(got) != secondBytes {
		t.Errorf("published settings = %q err=%v, want exactly the validated %q", got, err, secondBytes)
	}
	for label, h := range map[string]loopholeDaemon{"this launch's": second[0], "the earlier jail's": first[0]} {
		if got := dialFrontLine(t, h.hostPath, "settings"); got != strings.TrimSpace(secondBytes) {
			t.Errorf("%s front reaches a daemon serving %q, want the validated %q", label, got, secondBytes)
		}
	}
}

// TestFailedSingletonCandidateLeavesTheLiveDaemonOnItsSettings: a candidate whose validator times
// out, and a valid candidate whose locked preparation fails, each leave the live singleton, its
// settings record, the stable settings file and the existing jail's front exactly as they were.
// Neither stamps the candidate as the active settings.
func TestFailedSingletonCandidateLeavesTheLiveDaemonOnItsSettings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host-wide daemon and a Linux shell validator")
	}
	f := newSingletonLifecycleFixture(t, "life-preserve")
	_, first, out := f.launch(t, "first-profile", nil)
	if len(first) != 1 {
		t.Fatalf("first launch did not front the singleton:\n%s", out)
	}
	firstBytes := f.frozen(t, "first-profile")
	pid := f.livePID(t)
	stable := loopholes.SettingsFileFor(f.name)

	preserved := func(t *testing.T, what string) {
		t.Helper()
		if got := f.livePID(t); got != pid {
			t.Fatalf("%s replaced the live daemon (pid %d -> %d)", what, pid, got)
		}
		if got, err := os.ReadFile(stable); err != nil || string(got) != firstBytes {
			t.Fatalf("%s changed the stable settings: got=%q err=%v, want %q", what, got, err, firstBytes)
		}
		if drift, judged := broker.RunningSettingsDrift(f.deps); !judged || drift.Stale() {
			t.Fatalf("%s left the live daemon's settings record disagreeing with its settings: %+v judged=%v",
				what, drift, judged)
		}
		if got := dialFrontLine(t, first[0].hostPath, "settings"); got != strings.TrimSpace(firstBytes) {
			t.Fatalf("after %s the existing front reaches %q, want the previous %q", what, got, firstBytes)
		}
	}

	t.Run("validator timeout", func(t *testing.T) {
		o, handles, out := f.launch(t, "slow-profile", nil)
		if len(handles) != 0 || o.startupRefusal == nil || o.startupRefusal.class != string(hostservice.CommandTimedOut) {
			t.Fatalf("timed-out candidate was not refused: handles=%d refusal=%+v\n%s", len(handles), o.startupRefusal, out)
		}
		preserved(t, "a timed-out validator")
	})
	t.Run("preparation failure", func(t *testing.T) {
		o, handles, out := f.launch(t, "prepared-profile", func(o *Options) {
			o.singletonDepsForStart = func(name string, argv []string) broker.Deps {
				deps := broker.SingletonDeps(name, argv)
				deps.PrepareLocked = func() (func() error, error) { return nil, errors.New("fixture preparation failure") }
				return deps
			}
		})
		if len(handles) != 0 || len(o.startupOutcomes) != 1 ||
			o.startupOutcomes[0].Kind != hostservice.StartupKindPreparationFailed {
			t.Fatalf("failed preparation: handles=%d outcomes=%+v\n%s", len(handles), o.startupOutcomes, out)
		}
		preserved(t, "a failed preparation")
	})
}

// Per-jail startup-reason daemons that send NO record. The modes extend TestPerJailReasonChild.
const (
	perJailGrandchildReadyEnv   = "YJ_PER_JAIL_GRANDCHILD_READY"
	perJailGrandchildReleaseEnv = "YJ_PER_JAIL_GRANDCHILD_RELEASE"
	perJailGrandchildResultEnv  = "YJ_PER_JAIL_GRANDCHILD_RESULT"
	perJailWrapperExitedEnv     = "YJ_PER_JAIL_WRAPPER_EXITED"
)

// perJailSilentChildMain runs the record-less fixture modes. ok=false means mode is not one of them.
func perJailSilentChildMain(mode string) (code int, ok bool) {
	switch mode {
	case "silent-exit":
		return 2, true
	case "silent-hang":
		time.Sleep(time.Hour)
		return 0, true
	case "silent-grandchild-exit", "silent-grandchild-hang":
		if err := spawnReasonHoldingGrandchild(); err != nil {
			fmt.Fprintln(os.Stderr, "silent fixture:", err)
			return 4, true
		}
		if mode == "silent-grandchild-exit" {
			return 2, true
		}
		time.Sleep(time.Hour)
		return 0, true
	case "grandchild":
		return reasonHoldingGrandchildMain(), true
	case "daemonize":
		return daemonizingWrapperMain(), true
	}
	return 0, false
}

// spawnReasonHoldingGrandchild starts a descendant in a session of its own — out of reach of the
// failed start's process-group kill — that keeps the startup-reason endpoint (fd 3) open, and waits
// until it is running.
func spawnReasonHoldingGrandchild() error {
	ready := os.Getenv(perJailGrandchildReadyEnv)
	cmd := exec.Command(os.Args[0], "-test.run=^TestPerJailReasonChild$")
	cmd.Env = append(os.Environ(), perJailReasonChildModeEnv+"=grandchild", perJailReasonChildDelayEnv+"=")
	cmd.ExtraFiles = []*os.File{os.NewFile(3, "startup-reason")}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("grandchild never became ready")
}

// reasonHoldingGrandchildMain holds fd 3 until released, then writes to it and records the result:
// a write that fails says the launch closed its end of the channel.
func reasonHoldingGrandchildMain() int {
	if err := os.WriteFile(os.Getenv(perJailGrandchildReadyEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return 2
	}
	release := os.Getenv(perJailGrandchildReleaseEnv)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(release); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return 3
		}
		time.Sleep(10 * time.Millisecond)
	}
	result := "write-ok"
	if _, err := os.NewFile(3, "startup-reason").Write([]byte{0, 0, 0, 1}); err != nil {
		result = "write-failed: " + err.Error()
	}
	_ = os.WriteFile(os.Getenv(perJailGrandchildResultEnv), []byte(result), 0o600)
	return 0
}

// daemonizingWrapperMain is a clean daemonizing wrapper: it starts the real service in its own
// process group, waits until that service's socket accepts, and exits 0 without a record.
func daemonizingWrapperMain() int {
	socket := os.Args[len(os.Args)-1]
	cmd := exec.Command(os.Args[0], "-front-upstream-child", "line", socket)
	cmd.Env = append(os.Environ(), perJailReasonChildModeEnv+"=")
	if err := cmd.Start(); err != nil {
		return 4
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond); err == nil {
			_ = conn.Close()
			_ = os.WriteFile(os.Getenv(perJailWrapperExitedEnv), []byte("exiting"), 0o600)
			return 0
		}
		time.Sleep(10 * time.Millisecond)
	}
	return 5
}

// TestPerJailRecordlessStartupReasonCleanup: an opted-in per-jail daemon that sends no record is
// classified by its process — exited before readiness, or alive past the readiness bound — and the
// start returns within that bound even when an escaped descendant still holds the reason endpoint,
// whose parent end the launch has closed by the time it returns.
func TestPerJailRecordlessStartupReasonCleanup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns host processes")
	}
	for _, tc := range []struct {
		mode       string
		timeout    time.Duration
		maxElapsed time.Duration
		kind       hostservice.StartupKind
		grandchild bool
	}{
		// Nothing else holds the endpoint, so its EOF ends the read well before the bound.
		{mode: "silent-exit", timeout: 3 * time.Second, maxElapsed: 2 * time.Second, kind: hostservice.StartupKindProcessExited},
		{mode: "silent-hang", timeout: 400 * time.Millisecond, maxElapsed: 1500 * time.Millisecond, kind: hostservice.StartupKindReadinessTimedOut},
		{mode: "silent-grandchild-exit", timeout: 1500 * time.Millisecond, maxElapsed: 2700 * time.Millisecond,
			kind: hostservice.StartupKindProcessExited, grandchild: true},
		{mode: "silent-grandchild-hang", timeout: 1500 * time.Millisecond, maxElapsed: 2700 * time.Millisecond,
			kind: hostservice.StartupKindReadinessTimedOut, grandchild: true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			scratch := t.TempDir()
			ready, release, result := filepath.Join(scratch, "ready"), filepath.Join(scratch, "release"), filepath.Join(scratch, "result")
			t.Setenv(perJailGrandchildReadyEnv, ready)
			t.Setenv(perJailGrandchildReleaseEnv, release)
			t.Setenv(perJailGrandchildResultEnv, result)
			t.Cleanup(func() {
				_ = os.WriteFile(release, []byte("go"), 0o600)
				if data, err := os.ReadFile(ready); err == nil {
					if pid, err := strconv.Atoi(string(data)); err == nil {
						deadline := time.Now().Add(5 * time.Second)
						for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
							time.Sleep(20 * time.Millisecond)
						}
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
			})
			started := time.Now()
			o, handles, refusal, output := runPerJailReasonFixture(t, tc.mode, tc.timeout, false, 0)
			elapsed := time.Since(started)
			if elapsed > tc.maxElapsed {
				t.Fatalf("record-less start took %s, beyond its bound %s (readiness %s)", elapsed, tc.maxElapsed, tc.timeout)
			}
			if len(handles) != 0 || refusal != nil || len(o.startupOutcomes) != 1 {
				t.Fatalf("record-less failure: handles=%d refusal=%+v outcomes=%+v\n%s", len(handles), refusal, o.startupOutcomes, output)
			}
			got := o.startupOutcomes[0]
			if got.Kind != tc.kind || got.ReasonRead.Kind == hostservice.StartupReasonReadRecord || got.Reason != "" {
				t.Fatalf("outcome = %s read=%s reason=%q, want %s with no record", got.Kind, got.ReasonRead.Kind, got.Reason, tc.kind)
			}
			if !strings.Contains(output, "cannot reach it") {
				t.Fatalf("record-less failure lost its ordinary readiness warning:\n%s", output)
			}
			if !tc.grandchild {
				return
			}
			if _, err := os.Stat(ready); err != nil {
				t.Fatalf("no descendant held the reason endpoint during the start: %v", err)
			}
			if err := os.WriteFile(release, []byte("go"), 0o600); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			var data []byte
			for time.Now().Before(deadline) {
				if data, _ = os.ReadFile(result); len(data) > 0 {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !strings.HasPrefix(string(data), "write-failed") {
				t.Fatalf("the escaped descendant's endpoint still had a reader after the start returned: %q", data)
			}
		})
	}
}

// TestPerJailDaemonizingWrapperWithStartupReasonIsReady: a clean daemonizing wrapper that opted
// into startup reasons, hands its service to a child and exits 0 without a record, is Ready on the
// accepted socket; the wrapper's exit does not end the service the keeper watches.
func TestPerJailDaemonizingWrapperWithStartupReasonIsReady(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns host processes and binds an AF_UNIX socket")
	}
	exited := filepath.Join(t.TempDir(), "wrapper-exited")
	t.Setenv(perJailWrapperExitedEnv, exited)
	o, handles, refusal, output := runPerJailReasonFixture(t, "daemonize", 3*time.Second, false, 0)
	if len(handles) != 1 || refusal != nil || len(o.startupOutcomes) != 1 {
		t.Fatalf("daemonizing wrapper was not started: handles=%d refusal=%+v outcomes=%+v\n%s",
			len(handles), refusal, o.startupOutcomes, output)
	}
	if got := o.startupOutcomes[0]; got.Kind != hostservice.StartupKindReady ||
		got.Readiness != hostservice.StartupReadinessAccepted || got.ReasonRead.Kind == hostservice.StartupReasonReadRecord {
		t.Fatalf("daemonizing wrapper outcome = %s/%s read=%s, want ready/accepted with no record",
			got.Kind, got.Readiness, got.ReasonRead.Kind)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(exited); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(exited); err != nil {
		t.Fatalf("wrapper never reached its exit: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	select {
	case <-handles[0].end.done:
		t.Fatalf("the wrapper's clean exit ended the service it handed to its child: %s", handles[0].end.how())
	default:
	}
}
