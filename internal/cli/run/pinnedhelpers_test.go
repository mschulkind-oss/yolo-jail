package run

// pinnedhelpers_test.go pins the run package's use of the launch's pinned host yolo
// (docs/design/pinned-host-helpers.md §3.2 to §3.5, PH-D3 to PH-D7): every self-exec that serves a
// jail goes through Options.selfExecArgv, which execs the pin after a stat check and never falls
// back to os.Executable().

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/helperpin"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// pinFixtureBundle makes a bundle root holding bin/<os>-<arch>/yolo with body, and returns the root.
func pinFixtureBundle(t *testing.T, body []byte) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "bin", goruntime.GOOS+"-"+goruntime.GOARCH)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yolo"), body, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// fixturePin is the pin of a fixture bundle whose yolo holds body.
func fixturePin(t *testing.T, body []byte) helperpin.Pin {
	t.Helper()
	pin, err := helperpin.Resolve(pinFixtureBundle(t, body))
	if err != nil {
		t.Fatal(err)
	}
	return pin
}

// shellPin is a pin whose file is a COPY of the shell, so a daemon exec'd from it runs `yolo -c
// <script>` and can say which file it was exec'd from. Never os.Executable().
func shellPin(t *testing.T) helperpin.Pin {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to copy")
	}
	sh, err = filepath.EvalSymlinks(sh)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(sh)
	if err != nil {
		t.Fatal(err)
	}
	return fixturePin(t, body)
}

// withFixtureBundle makes o's RepoRoot resolve to a bundle that holds a host yolo, so a launch
// that reaches the pin's pre-flight finds one.
func withFixtureBundle(t *testing.T, o *Options) string {
	t.Helper()
	root := pinFixtureBundle(t, []byte("fixture yolo"))
	o.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: root, Source: reporoot.FromEnv}, true
	}
	return root
}

// A HOST DAEMON IS EXEC'D FROM THE PIN: the bare `yolo` token of its command becomes the pin's
// path, and the process that runs is that file, not whatever os.Executable() names.
func TestAHostDaemonIsExecdFromThePin(t *testing.T) {
	f := newSupervisedFixture(t)
	pin := shellPin(t)
	f.o.helperPin = &pin
	exeFile := filepath.Join(t.TempDir(), "exe")
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"yolo", "-c", `readlink /proc/$$/exe > "$EXE_FILE"; echo "$YOLO_HELPER_JAIL" > "$EXE_FILE.jail"; : > "$1"; exec sleep 30`, "pinned", "{socket}"})
	env := jsonx.NewOrderedMap()
	env.Set("EXE_FILE", exeFile)
	spec.Set("env", env)

	argv, ok := f.o.resolveDaemonArgv("pinned", spec, filepath.Join(t.TempDir(), "pinned.sock"))
	if !ok || len(argv) == 0 || argv[0] != pin.Path {
		t.Fatalf("resolveDaemonArgv gave %v (ok=%v), want argv[0] = the pin %s\n%s", argv, ok, pin.Path, f.terminal.String())
	}
	h, ok := f.o.startExternalService("pinned", spec, t.TempDir(), "", "", nil)
	if !ok {
		t.Fatalf("the daemon never came up:\n%s", f.terminal.String())
	}
	defer h.stop()
	if !waitFor(10*time.Second, func() bool { b, _ := os.ReadFile(exeFile); return len(b) > 0 }) {
		t.Fatal("the daemon never recorded the file it runs from")
	}
	raw, _ := os.ReadFile(exeFile)
	if got := strings.TrimSpace(string(raw)); got != pin.Path {
		t.Errorf("the daemon runs from %s, want the pin %s", got, pin.Path)
	}
	// It is told which jail it serves, for its start line.
	if !waitFor(10*time.Second, func() bool { b, _ := os.ReadFile(exeFile + ".jail"); return len(b) > 0 }) {
		t.Fatal("the daemon never recorded its jail")
	}
	jail, _ := os.ReadFile(exeFile + ".jail")
	if got := strings.TrimSpace(string(jail)); got != f.o.jailName() || got == "" {
		t.Errorf("the daemon was told jail %q, want %q (%s)", got, f.o.jailName(), helperpin.EnvJail)
	}
}

// A DAEMON WITH NO PIN IS NEVER STARTED FROM os.Executable(): the argv is refused, and it names
// the reinstall step.
func TestAHostDaemonWithoutAPinIsRefusedNotRunFromTheLauncher(t *testing.T) {
	f := newSupervisedFixture(t)
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"yolo", "internal", "daemon", "x", "{socket}"})
	if argv, ok := f.o.resolveDaemonArgv("x", spec, "/tmp/x.sock"); ok {
		t.Fatalf("a pin-less launch resolved a self-exec argv %v", argv)
	}
	if out := f.terminal.String(); !strings.Contains(out, reporoot.ReinstallSteps) {
		t.Errorf("the refusal does not name the reinstall step:\n%s", out)
	}
}

// A RESTART WITH A CHANGED PIN IS A GIVE-UP: the file the daemon was started from was replaced
// (an upgrade swapped the bundle), so its restart would run another build. The supervisor says so,
// naming the path and the stop step, and starts nothing.
func TestARestartWithAChangedPinGivesUp(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 5*time.Millisecond, 10*time.Second, 5)
	f.o.runtime = "podman"
	pin := shellPin(t)
	f.o.helperPin = &pin
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"yolo", "-c", `echo $$ >> "$PIDS"; : > "$1"; exec sleep 30`, "pinned", "{socket}"})
	env := jsonx.NewOrderedMap()
	env.Set("PIDS", f.pids)
	spec.Set("env", env)
	h, ok := f.o.startExternalService("pinned", spec, t.TempDir(), "", "", nil)
	if !ok {
		t.Fatalf("the daemon never came up:\n%s", f.terminal.String())
	}
	defer h.stop()
	pids := f.spawned()
	if len(pids) != 1 {
		t.Fatalf("want one spawn, have %v", pids)
	}
	// An install replaces the file: a new inode at the same path.
	repl := pin.Path + ".new"
	if err := os.WriteFile(repl, []byte("another build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(repl, pin.Path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pids[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if !ended(h, 20*time.Second) {
		t.Fatalf("supervision never ended for a daemon whose pin was replaced; host log:\n%s", hostLog("pinned"))
	}
	for _, out := range []struct{ what, text string }{{"host log", hostLog("pinned")}, {"launch.log", f.launch.String()}} {
		for _, want := range []string{"host service 'pinned' died (", "not restarting it", "no longer at " + pin.Path,
			"launched again", "yolo stop"} {
			if !strings.Contains(out.text, want) {
				t.Errorf("the %s lacks %q:\n%s", out.what, want, out.text)
			}
		}
		if strings.Contains(out.text, "restarting it in") {
			t.Errorf("the %s says it is restarting a daemon whose pin is gone:\n%s", out.what, out.text)
		}
	}
	if n := len(f.spawned()); n != 1 {
		t.Errorf("a daemon whose pin changed was started again (%d spawns)", n)
	}
	if how := h.end.how(); !strings.Contains(how, "no longer at") {
		t.Errorf("the end's account is %q, want the pin's absence (recorded down)", how)
	}
}

// A PIN THAT IS UNCHANGED RESTARTS AS BEFORE: the control for the give-up above.
func TestARestartWithTheSamePinStillRestarts(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 5*time.Millisecond, 10*time.Second, 5)
	pin := shellPin(t)
	f.o.helperPin = &pin
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"yolo", "-c", `echo $$ >> "$PIDS"; : > "$1"; exec sleep 30`, "pinned2", "{socket}"})
	env := jsonx.NewOrderedMap()
	env.Set("PIDS", f.pids)
	spec.Set("env", env)
	h, ok := f.o.startExternalService("pinned2", spec, t.TempDir(), "", "", nil)
	if !ok {
		t.Fatalf("the daemon never came up:\n%s", f.terminal.String())
	}
	defer h.stop()
	first := f.spawned()
	if len(first) != 1 {
		t.Fatalf("want one spawn, have %v", first)
	}
	_ = syscall.Kill(first[0], syscall.SIGKILL)
	if !waitFor(10*time.Second, func() bool { return len(f.spawned()) == 2 }) {
		t.Fatalf("the daemon was not restarted from an unchanged pin; host log:\n%s", hostLog("pinned2"))
	}
}

// THE KEEPER'S PLAN CARRIES THE PIN, round-trips it through the plan file, and the keeper built
// from it holds the same pin.
func TestTheKeeperPlanCarriesThePin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "podman", &stdout, &stderr, nil)
	pin := fixturePin(t, []byte("a yolo"))
	o.helperPin = &pin
	plan, err := o.keeperPlanFor(jsonx.NewOrderedMap(), "podman", "yolo-pin", stagedPacks{}, nil, nil, nil, "", "",
		[]string{"podman", "run"}, &assembleInput{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Pin != pin {
		t.Fatalf("the plan carries pin %+v, want %+v", plan.Pin, pin)
	}
	path, err := writeKeeperPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	read, err := readKeeperPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if read.Pin != pin {
		t.Errorf("the plan file round-tripped pin %+v, want %+v", read.Pin, pin)
	}
	raw, _ := json.Marshal(plan)
	if !strings.Contains(string(raw), `"pin":`) {
		t.Errorf("the plan's JSON has no pin key: %s", raw)
	}
	k := newKeeper(read, KeeperSeams{}, nil, nil, nil, nil)
	if k.o.helperPin == nil || *k.o.helperPin != pin {
		t.Errorf("the keeper built from the plan holds pin %v, want %+v", k.o.helperPin, pin)
	}
}

// The macos-user keeper's plan carries it too.
func TestTheMacosUserKeeperPlanCarriesThePin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	pin := fixturePin(t, []byte("a yolo"))
	o.helperPin = &pin
	o.macosUserKey = &macosUserKeying{}
	plan, err := o.macosUserKeeperPlanFor(jsonx.NewOrderedMap(), "macos-user", "yolo-pin", stagedPacks{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Pin != pin {
		t.Fatalf("the macos-user plan carries pin %+v, want %+v", plan.Pin, pin)
	}
}

// A KEEPER'S SELF-EXEC: on Linux its own inode (JL-D5, /proc/self/exe, which now names the pin's
// inode because the keeper was spawned from the pin); where there is no /proc/self/exe, the pin
// from its plan, checked first. Never os.Executable().
func TestAKeepersSelfExecIsItsOwnInodeOnLinuxAndThePinElsewhere(t *testing.T) {
	pin := fixturePin(t, []byte("a yolo"))
	o := &Options{keeperMode: true, helperPin: &pin}
	argv, err := o.selfExecArgvWith("/proc/self/exe", []string{"yolo", "internal", "daemon", "x"})
	if err != nil || argv[0] != "/proc/self/exe" || argv[3] != "x" {
		t.Errorf("a Linux keeper's self-exec is %v (%v), want /proc/self/exe", argv, err)
	}
	argv, err = o.selfExecArgvWith("", []string{"yolo", "internal", "daemon", "x"})
	if err != nil || argv[0] != pin.Path {
		t.Errorf("an off-Linux keeper's self-exec is %v (%v), want the plan's pin %s", argv, err, pin.Path)
	}
	// The pin changed under it: that spawn does not happen, and the error names the path.
	if err := os.Remove(pin.Path); err != nil {
		t.Fatal(err)
	}
	o.runtime, o.keeperJailName = "podman", "yolo-x"
	argv, err = o.selfExecArgvWith("", []string{"yolo", "internal", "daemon", "x"})
	if err == nil || argv != nil || !strings.Contains(err.Error(), pin.Path) || !strings.Contains(err.Error(), "yolo stop") {
		t.Errorf("a changed pin gave %v, %v; want no argv and an error naming %s and the stop step", argv, err, pin.Path)
	}
	// A command that is not the bare yolo token is left alone.
	argv, err = o.selfExecArgvWith("", []string{"sh", "-c", "true"})
	if err != nil || argv[0] != "sh" {
		t.Errorf("a non-yolo command became %v (%v)", argv, err)
	}
}

// A LAUNCH'S SELF-EXEC IS ITS PIN, and with none (or a changed one) it is an error: never
// os.Executable() or the bare token.
func TestALaunchSelfExecIsItsPinAndNeverTheLauncher(t *testing.T) {
	pin := fixturePin(t, []byte("a yolo"))
	o := &Options{helperPin: &pin, runtime: "podman", Workspace: t.TempDir()}
	argv, err := o.selfExecArgv([]string{"yolo", "internal", "x"})
	if err != nil || argv[0] != pin.Path {
		t.Fatalf("a launch's self-exec is %v (%v), want the pin", argv, err)
	}
	none := &Options{runtime: "podman", Workspace: t.TempDir()}
	if argv, err := none.selfExecArgv([]string{"yolo", "internal", "x"}); err == nil || argv != nil {
		t.Errorf("a launch with no pin self-exec'd %v (%v)", argv, err)
	} else if !strings.Contains(err.Error(), reporoot.ReinstallSteps) {
		t.Errorf("the refusal does not name the reinstall step: %v", err)
	}
	if err := os.Remove(pin.Path); err != nil {
		t.Fatal(err)
	}
	if argv, err := o.selfExecArgv([]string{"yolo", "internal", "x"}); err == nil || argv != nil ||
		!strings.Contains(err.Error(), pin.Path) || !strings.Contains(err.Error(), reporoot.ReinstallSteps) {
		t.Errorf("a launch whose pin is gone gave %v (%v), want a refusal naming %s and the reinstall step", argv, err, pin.Path)
	}
}

// A LAUNCH WITHOUT A PIN REFUSES BEFORE SPAWNING: a bundle with no host yolo stops the launch with
// the resolver's message (which names the reinstall step) before any keeper, daemon or build runs.
func TestALaunchWithoutAPinRefusesBeforeSpawning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	empty := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := skewOptions(t, empty, nil, &stdout, &stderr)
	// skewOptions' bundle holds a host yolo; this one must not.
	if err := os.RemoveAll(filepath.Join(empty, "bin")); err != nil {
		t.Fatal(err)
	}
	spawned := false
	o.StartDetached = func([]string, *os.File) error { spawned = true; return nil }
	defaultSpawner := defaultKeeperSpawner
	defer func() { defaultKeeperSpawner = defaultSpawner }()
	defaultKeeperSpawner = func(*Options, string, *os.File, *os.File, *os.File, []*os.File) (func() int, error) {
		spawned = true
		return nil, nil
	}
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	out := stderr.String()
	if !strings.Contains(out, filepath.Join(empty, "bin")) || !strings.Contains(out, "Reinstall yolo") {
		t.Errorf("the refusal names neither the missing host yolo nor the reinstall step:\n%s", out)
	}
	if spawned {
		t.Error("the launch spawned something before refusing for want of a pin")
	}
}

// A DRY RUN RESOLVES NO PIN: it starts no helper, so a bundle without one does not stop it
// (PH-D3: a launch that starts none resolves none).
func TestADryRunResolvesNoPin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := runFatalOptions(t, t.TempDir(), "macos-user", &stdout, &stderr)
	empty := t.TempDir()
	o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{Root: empty}, true }
	o.DryRun = true
	t.Setenv("HOME", t.TempDir())
	_ = Run(*o)
	if strings.Contains(stderr.String(), "Reinstall yolo") && strings.Contains(stderr.String(), "no host yolo") {
		t.Errorf("a dry run refused for want of a pin:\n%s", stderr.String())
	}
}

// sharedTestBundle is the bundle every fixture's pin comes from: its host yolo is a symlink to THIS
// test binary, which TestMain turns into any `yolo internal daemon <name>` a fixture execs (a pin
// is a path, and the test binary is not replaced under a running test). Made once by
// installSharedTestPin, which TestMain calls.
var sharedTestPin helperpin.Pin

// installSharedTestPin makes the shared bundle and pins its yolo; the returned func removes it.
func installSharedTestPin() func() {
	root, err := os.MkdirTemp("", "yolo-test-pin-")
	if err != nil {
		panic(err)
	}
	self, err := os.Executable()
	if err != nil {
		panic(err)
	}
	dir := filepath.Join(root, "bin", goruntime.GOOS+"-"+goruntime.GOARCH)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	if err := os.Symlink(self, filepath.Join(dir, "yolo")); err != nil {
		panic(err)
	}
	pin, err := helperpin.Resolve(root)
	if err != nil {
		panic(err)
	}
	sharedTestPin = pin
	return func() { _ = os.RemoveAll(root) }
}

// testPin is a fresh pointer to the shared test pin, for a fixture's Options.
func testPin() *helperpin.Pin {
	pin := sharedTestPin
	return &pin
}
