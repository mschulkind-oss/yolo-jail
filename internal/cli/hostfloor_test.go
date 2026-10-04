package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/hostpath"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// productionHostFloor is newHostFloor as the package built it, kept so a test can reach the
// production wiring (the call sites) while TestMain holds the disarmed default.
var productionHostFloor = newHostFloor

// disarmTheHostFloor makes the package's default floor one that can install NOTHING: its Node
// distribution is an address nothing listens on and it has no capture or build act, so no test that
// happens to launch a selected pack's agent can download a runtime or boot a capture jail — the
// no-agent-tests rule one level down, as depInstallRun's guard is. It also leaves every pack out
// (`host_floor: false`), so a fixture that launches `yolo host -- claude` against a stub on PATH
// keeps exec'ing that stub: the OQ-HE11 branch, with its one line. A test about the floor itself
// installs its own (withTestFloor).
func disarmTheHostFloor() {
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := productionHostFloor(out, progs)
		f.Include = func(string) bool { return false }
		f.Node.BaseURL = "http://127.0.0.1:1/test-guard-no-node-download"
		f.Capture = func(bin string) error {
			return errors.New("test guard: refusing to run `yolo capture " + bin + "`")
		}
		f.Build = func(p hostfloor.Program, commit string) (*capture.Entry, error) {
			return nil, errors.New("test guard: refusing to build " + p.Bin() + " at " + commit)
		}
		f.Advance = func(_ context.Context, p hostfloor.Program) hostfloor.PatchedState {
			return hostfloor.PatchedState{Reason: "test guard: refusing to advance " + p.Bin()}
		}
		return f
	}
}

// withTestFloor gives this test the PRODUCTION floor wiring — the prefix under HOME, the
// user-scope `host_floor` and `agent_updates`, the capture store — with its Node taken from a fake
// distribution and its npm the fake registry's, and no capture or build act. It returns the distribution,
// whose registry the test publishes into.
func withTestFloor(t *testing.T) *floortest.Dist {
	t.Helper()
	return withTestFloorOn(t, floortest.NewDist(t))
}

// withLinuxTestFloor is withTestFloor with the floor on Linux whatever machine runs the test: the
// one platform where the floor holds a fork's build at all.
func withLinuxTestFloor(t *testing.T) *floortest.Dist {
	t.Helper()
	return withTestFloorOn(t, floortest.NewLinuxDist(t))
}

// withTestFloorOn is withTestFloor over dist, with the floor on the platform dist serves Node for,
// so any Node the floor fetches is one the distribution serves, on a Mac too.
func withTestFloorOn(t *testing.T, dist *floortest.Dist) *floortest.Dist {
	t.Helper()
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := productionHostFloor(out, progs)
		f.GOOS, f.GOARCH = dist.GOOS, dist.GOARCH
		f.Node = hostfloor.NodeDist{BaseURL: dist.URL, Shipped: floortest.Shipped,
			Pinned: map[string]string{dist.Platform: dist.SHA256}}
		f.Environ = append(os.Environ(), dist.Environ()...)
		f.Capture = func(bin string) error {
			return errors.New("test guard: refusing to run `yolo capture " + bin + "`")
		}
		f.Build = func(p hostfloor.Program, commit string) (*capture.Entry, error) {
			return nil, errors.New("test guard: refusing to build " + p.Bin() + " at " + commit)
		}
		f.Advance = func(_ context.Context, p hostfloor.Program) hostfloor.PatchedState {
			return hostfloor.PatchedState{Reason: "test guard: refusing to advance " + p.Bin()}
		}
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	return dist
}

// floorLaunchFixture is a host whose user config selects one fixture pack declaring
// `program floorcli via npm`, with a HAND-INSTALLED floorcli on the caller's PATH — the copy
// HP-DIR4 says `yolo host` does not run. extra is appended to the config object.
func floorLaunchFixture(t *testing.T, extra string) (dist *floortest.Dist, handInstalled string) {
	t.Helper()
	dist, _ = floorHostFixture(t, extra)
	return dist, filepath.Join(stubBins(t, "floorcli"), "floorcli")
}

// floorHostFixture is floorLaunchFixture without the hand-installed copy: a temp HOME whose user
// config selects the floorpack fixture (plus extra), the test floor, and the pack's package
// published. It returns the distribution and the pack's directory.
func floorHostFixture(t *testing.T, extra string) (dist *floortest.Dist, pack string) {
	t.Helper()
	return floorHostFixtureWith(t, `{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}`, extra)
}

// floorHostFixtureWith is floorHostFixture with the floorpack's one program contribution given.
func floorHostFixtureWith(t *testing.T, program, extra string) (dist *floortest.Dist, pack string) {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(floortest.ResolvedTemp(t))
	pack = filepath.Join(floortest.ResolvedTemp(t), "floorpack")
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name":"floorpack","contributes":[`+program+`]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+pack+`","name":"floorpack"}]`+extra+`}`)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	dist = withTestFloor(t)
	dist.Publish("floorcli-pkg", "1.0.0", "bin=floorcli")
	return dist, pack
}

// execCapture stands in for the exec and records what `yolo host` handed it.
type execCapture struct {
	execed bool
	target string
	argv   []string
	env    []string
}

func captureHostExec(t *testing.T) *execCapture {
	t.Helper()
	got := &execCapture{}
	orig := hostSyscallExec
	hostSyscallExec = func(target string, argv, env []string) error {
		got.execed, got.target, got.argv, got.env = true, target, argv, env
		return nil
	}
	t.Cleanup(func() { hostSyscallExec = orig })
	return got
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// TestHostLaunchRunsTheFloorsCopyOfASelectedPacksAgent pins HP-DIR4 at the CALL SITE — hostExec,
// through the production floor wiring — and §8's first two rows: the first launch installs the
// agent into the floor (saying so), and every launch, from a terminal PATH that has a
// hand-installed copy or from a widget's bare PATH, execs the FLOOR's copy by path. The child's
// PATH is the caller's, then the floor's bin/ last (OQ-HE10 (c), HE-D1). Replace the resolution in
// hostExec with the old PATH lookup and the first assertion fails: the target is the stub.
func TestHostLaunchRunsTheFloorsCopyOfASelectedPacksAgent(t *testing.T) {
	dist, handInstalled := floorLaunchFixture(t, "")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli", "--flag"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("hostExec rc=%d execed=%v:\n%s", rc, got.execed, errw.String())
	}
	floorBin := filepath.Join(paths.HostFloorDir(), "bin")
	launcher := filepath.Join(floorBin, "floorcli")
	if got.target != launcher {
		t.Fatalf("exec'd %s, want the floor's copy %s (the caller's PATH has %s, which HP-DIR4 "+
			"says is not the copy `yolo host` runs)", got.target, launcher, handInstalled)
	}
	if strings.Join(got.argv, " ") != "floorcli --flag" {
		t.Errorf("argv = %q: argv[0] stays the name the user typed", got.argv)
	}
	childPath := envValue(got.env, "PATH")
	if !strings.HasPrefix(childPath, filepath.Dir(handInstalled)+string(os.PathListSeparator)) ||
		!strings.HasSuffix(childPath, string(os.PathListSeparator)+floorBin) {
		t.Errorf("child PATH = %s, want the caller's PATH first and %s last", childPath, floorBin)
	}
	if !strings.Contains(errw.String(), "installing floorcli into yolo's floor") {
		t.Errorf("the first launch installed without saying so:\n%s", errw.String())
	}

	// From a Waybar widget: a bare PATH, no mise, no ~/.local/bin. Same copy, no second install.
	t.Setenv("PATH", "/usr/bin:/bin")
	errw.Reset()
	*got = execCapture{}
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher {
		t.Fatalf("from a bare PATH: rc=%d target=%s\n%s", rc, got.target, errw.String())
	}
	if n := len(dist.NpmCalls("install")); n != 1 {
		t.Errorf("npm install ran %d times across two launches, want 1", n)
	}
}

// TestHostLaunchOfAProgramTheFloorCannotHoldRunsThePATHCopyAndSaysSo is OQ-HE11's interim
// behavior (the ruling is open; the task keeps today's behavior): a selected pack's program with
// no floor entry — here `host_floor` leaves the pack out — is looked up on the caller's PATH, and
// one line says the copy is not yolo's. Nothing is installed.
func TestHostLaunchOfAProgramTheFloorCannotHoldRunsThePATHCopyAndSaysSo(t *testing.T) {
	dist, handInstalled := floorLaunchFixture(t, `,"host_floor":{"floorpack":false}`)
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || got.target != handInstalled {
		t.Fatalf("rc=%d target=%s, want the PATH copy %s\n%s", rc, got.target, handInstalled, errw.String())
	}
	if !strings.Contains(errw.String(), "yolo has no copy of floorcli") ||
		!strings.Contains(errw.String(), "host_floor") {
		t.Errorf("the launch did not say the copy is not yolo's, and why:\n%s", errw.String())
	}
	if len(dist.NpmCalls("install")) != 0 {
		t.Error("a program the floor may not hold was installed")
	}
	if _, err := os.Stat(paths.HostFloorDir()); err == nil {
		t.Errorf("the launch created %s for a program with no floor entry", paths.HostFloorDir())
	}
}

// TestHostLaunchThatCannotInstallItsAgentRefusesAndExecsNothing: a first install that fails is
// the launch's failure — exit 127, the installer's own last line, no exec of some other copy.
func TestHostLaunchThatCannotInstallItsAgentRefusesAndExecsNothing(t *testing.T) {
	dist, _ := floorLaunchFixture(t, "")
	dist.Publish("floorcli-pkg", "1.0.0", "bin=floorcli", "fail")
	got := captureHostExec(t)
	var errw bytes.Buffer
	rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil)
	if rc != 127 || got.execed {
		t.Fatalf("rc=%d execed=%v (target %s), want 127 and no exec\n%s", rc, got.execed, got.target, errw.String())
	}
	for _, want := range []string{"could not install floorcli into yolo's floor", "npm ERR! 404"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errw.String())
		}
	}
}

// TestHostLaunchOfATargetGivenAsAPathRunsThatFile: `yolo host -- ~/src/x/dist/floorcli` runs that
// build, keyed on its base name for the composition, and never the floor's copy.
func TestHostLaunchOfATargetGivenAsAPathRunsThatFile(t *testing.T) {
	dist, handInstalled := floorLaunchFixture(t, "")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{handInstalled}, io.Discard, &errw, nil); rc != 0 || got.target != handInstalled {
		t.Fatalf("rc=%d target=%s, want %s as given\n%s", rc, got.target, handInstalled, errw.String())
	}
	if len(dist.NpmCalls("install")) != 0 {
		t.Error("a target given as a path installed the floor's copy")
	}
}

// TestHostChildPathIsTheCallersThenHostPathThenTheFloorsDeduplicated: the caller's PATH, then each
// host_path folder not already on it (HE-D3), then the floor's bin/ (HE-D1), duplicates removed.
func TestHostChildPathIsTheCallersThenHostPathThenTheFloorsDeduplicated(t *testing.T) {
	sep := string(os.PathListSeparator)
	ambient := strings.Join([]string{"/a", "", "/b", "/a", "/floor/bin"}, sep)
	got := hostChildPath(hostpath.New(ambient, nil, nil, "/home/u"), "/floor/bin")
	if want := strings.Join([]string{"/a", "/b", "/floor/bin"}, sep); got != want {
		t.Errorf("hostChildPath = %q, want %q", got, want)
	}
	got = hostChildPath(hostpath.New("/a"+sep+"/b", []string{"/tools", "/a", "/more"}, nil, "/home/u"), "/floor/bin")
	if want := strings.Join([]string{"/a", "/b", "/tools", "/more", "/floor/bin"}, sep); got != want {
		t.Errorf("with host_path, hostChildPath = %q, want %q: host_path's new folders after the "+
			"caller's PATH and before the floor", got, want)
	}
	// A caller with no PATH: the system baseline stands in for it, ahead of host_path's folders
	// and the floor's bin/, so the agent's own commands still resolve (HP-D12).
	baseline := strings.Join(append(hostfloor.BaselinePath(), "/tools", "/floor/bin"), sep)
	for _, empty := range []string{"", sep, sep + sep} {
		if got := hostChildPath(hostpath.New(empty, []string{"/tools"}, nil, "/home/u"), "/floor/bin"); got != baseline {
			t.Errorf("caller PATH %q gives %q, want the baseline, host_path, then the floor: %q", empty, got, baseline)
		}
	}
}

// TestAHostLaunchWithNoPATHHandsItsChildTheBaseline pins the same at the call site: a launcher
// that passes no PATH at all (`env -i yolo host -- floorcli`) still runs the floor's copy, and the
// child's PATH is the system baseline and then the floor's bin/ — not the floor's bin/ alone.
func TestAHostLaunchWithNoPATHHandsItsChildTheBaseline(t *testing.T) {
	floorHostFixture(t, "")
	baseline := hostfloor.BaselinePath()
	if len(baseline) == 0 {
		t.Skip("no baseline system directory exists on this machine")
	}
	got := captureHostExec(t)
	t.Setenv("PATH", "")
	if err := os.Unsetenv("PATH"); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("with no PATH: rc=%d execed=%v\n%s", rc, got.execed, errw.String())
	}
	floorBin := filepath.Join(paths.HostFloorDir(), "bin")
	if got.target != filepath.Join(floorBin, "floorcli") {
		t.Errorf("exec'd %s, want the floor's copy", got.target)
	}
	want := strings.Join(append(baseline, floorBin), string(os.PathListSeparator))
	if childPath := envValue(got.env, "PATH"); childPath != want {
		t.Errorf("child PATH = %q, want the baseline then the floor's bin/: %q", childPath, want)
	}
}

// TestHostLaunchSaysWhatItStartsAndFromWhere pins the hand-over line on the exec path, for each
// origin: the floor's copy, a copy on the caller's PATH, and a target given as a path. It is the
// LAST line before the exec, so a slow startup after it is visibly the agent's.
func TestHostLaunchSaysWhatItStartsAndFromWhere(t *testing.T) {
	_, handInstalled := floorLaunchFixture(t, "")
	stubBins(t, "plaincmd")
	got := captureHostExec(t)
	cases := []struct {
		cmd  string
		want string
	}{
		{"floorcli", "yolo host: starting floorcli (yolo's floor copy, ~/.local/share/yolo-jail/host-floor/bin/floorcli)"},
		{"plaincmd", "yolo host: starting plaincmd (from your PATH, "},
		{handInstalled, "yolo host: starting " + handInstalled + " (as given, " + handInstalled + ")"},
	}
	for _, c := range cases {
		var errw bytes.Buffer
		*got = execCapture{}
		if rc := hostExec(nil, []string{c.cmd}, io.Discard, &errw, nil); rc != 0 || !got.execed {
			t.Fatalf("%s: rc=%d execed=%v\n%s", c.cmd, rc, got.execed, errw.String())
		}
		lines := strings.Split(strings.TrimRight(errw.String(), "\n"), "\n")
		if last := lines[len(lines)-1]; !strings.HasPrefix(last, c.want) {
			t.Errorf("%s: the last line before the exec is %q, want it to start %q", c.cmd, last, c.want)
		}
	}
}

// TestCheckReadsTheLaunchsOwnFloor pins the wiring: `yolo check`'s floor rows are read from the
// construction `yolo host --` installs through, not a second one.
func TestCheckReadsTheLaunchsOwnFloor(t *testing.T) {
	floortest.ResolvedTemp(t)
	opts, ok := checkOptions([]string{"check"}, io.Discard)
	if !ok || opts.HostFloor == nil {
		t.Fatalf("checkOptions left HostFloor unwired (ok=%v)", ok)
	}
	if f := opts.HostFloor(nil); f.Dir != paths.HostFloorDir() || f.Prefix != "yolo host: " {
		t.Errorf("check reads floor %+v, not the launch's", f)
	}
}

// TestTheProductionFloorKnowsWhenThisMachineCannotCapture pins newHostFloor's CaptureUnavailable
// wiring: with no capture in the store and no container runtime on PATH, claude has no floor
// entry here (so a launch looks for it on PATH) rather than an install that would boot a jail it
// cannot boot. A runtime on PATH makes it an install the floor can do. Status only: no capture
// runs in this test.
func TestTheProductionFloorKnowsWhenThisMachineCannotCapture(t *testing.T) {
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_RUNTIME", "podman")
	claude := hostfloor.Program{Pack: "claude", Install: packdecl.Install{Kind: "native", Bin: "claude",
		InstallerURL: "https://example.invalid/install.sh"}}
	empty := floortest.ResolvedTemp(t)
	t.Setenv("PATH", empty)
	f := productionHostFloor(io.Discard, []hostfloor.Program{claude})
	f.GOOS = "linux"
	if st := f.Status(claude); st.Disposition != hostfloor.NoEntry || !strings.Contains(st.Reason, "podman") {
		t.Fatalf("with no runtime: %s (%s), want no floor entry naming podman", st.Disposition, st.Reason)
	}
	writeFile(t, filepath.Join(empty, "podman"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(empty, "podman"), 0o755); err != nil {
		t.Fatal(err)
	}
	if st := f.Status(claude); st.Disposition != hostfloor.Missing {
		t.Errorf("with podman on PATH: %s (%s), want missing — the floor can capture it", st.Disposition, st.Reason)
	}
}
