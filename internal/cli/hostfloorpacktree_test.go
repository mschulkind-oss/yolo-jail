package cli

// hostfloorpacktree_test.go drives a PACK-TREE PROGRAM (docs/design/pack-buildable-programs.md, `via:
// "pack"`) and a DEV OVERRIDE through `yolo host` on Linux (the plan's Task 9), from the call sites a
// user reaches: `yolo host -- <bin>` and the readiness act of `yolo host -- <other>`. The production
// floor wiring runs — the pack staged as a launch stages it, the digest, the build act (buildFork),
// the store's admission, the relocating materialize — with only the build jail (captureRunPipeline)
// and the floor's platform (Linux) standing in.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostPackTreeHome is a temp HOME whose user config selects only pack fx, a `file://` pack declaring
// the pack-tree program x from tools/x, run x, built by build ("" for none), with extra raw config
// keys. tools/x/x is a script printing x-<VERSION>, for the copy with no build line. It returns fx's
// root.
func hostPackTreeHome(t *testing.T, build, extra string) string {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(paths.AllowMissingProgramsEnv, "")
	t.Chdir(floortest.ResolvedTemp(t))
	previousProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) { return false, true }
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })
	fx := filepath.Join(floortest.ResolvedTemp(t), "fx")
	writeFile(t, filepath.Join(fx, "tools", "x", "VERSION"), "1\n")
	writePackTreeScript(t, fx, "1")
	buildKey := ""
	if build != "" {
		buildKey = `"build":"` + strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`) + `",`
	}
	writeFile(t, filepath.Join(fx, "pack.json"), `{"name":"fx","contributes":[{"kind":"program","bin":"x",`+
		`"via":"pack","from":"tools/x",`+buildKey+`"run":"x"}]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"host_management":"own",`+
		`"host_apply_on_launch":false,`+extra+`"packs":[{"source":"file://`+fx+`","name":"fx"}]}`)
	return fx
}

// writePackTreeScript writes fx's tools/x/x, a script printing x-<version>.
func writePackTreeScript(t *testing.T, fx, version string) {
	t.Helper()
	x := filepath.Join(fx, "tools", "x", "x")
	writeFile(t, x, "#!/bin/sh\necho x-"+version+"\n")
	if err := os.Chmod(x, 0o755); err != nil {
		t.Fatal(err)
	}
}

// runFloorLauncher runs the floor's launcher for bin with no environment and returns its output.
func runFloorLauncher(t *testing.T, bin string) string {
	t.Helper()
	cmd := exec.Command(filepath.Join(paths.HostFloorDir(), "bin", bin))
	cmd.Env = []string{}
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the floor's %s: %q %v", bin, got, err)
	}
	return string(got)
}

// THE CALL SITE, WITH NO BUILD LINE: `yolo host -- x` copies the pack's tree into the store (no jail),
// materializes it into the floor and execs the floor's copy; an edit to the pack's tree is a new
// digest, so the next launch reinstalls it once; a launch with nothing changed installs nothing. Red
// if the floor's PackTree wiring is removed (x has no floor entry and the launch refuses with 127).
func TestYoloHostDeliversAPackTreeProgramAndFollowsItsTree(t *testing.T) {
	fx := hostPackTreeHome(t, "", "")
	withForkFloor(t)
	jail := 0
	withFakeCaptureJail(t, func(run.Options) int { jail++; return 1 })
	got := captureHostExec(t)
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "x")

	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"x"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher {
		t.Fatalf("rc=%d target=%s, want the floor's copy %s\n%s", rc, got.target, launcher, errw.String())
	}
	if out := runFloorLauncher(t, "x"); out != "x-1\n" || jail != 0 {
		t.Fatalf("the floor's x ran %q with %d build jails, want x-1 and none (no build line)", out, jail)
	}
	rec := floorRecord(t, "x")
	if rec.Via != "pack" || !strings.HasPrefix(rec.Version, "tree ") || len(rec.Revision) != 64 {
		t.Errorf("record = %+v, want a pack-tree record keyed by the tree digest", rec)
	}
	first := rec.Revision

	writePackTreeScript(t, fx, "2")
	errw.Reset()
	if rc := hostExec(nil, []string{"x"}, io.Discard, &errw, nil); rc != 0 {
		t.Fatalf("after an edit: rc=%d\n%s", rc, errw.String())
	}
	if out := runFloorLauncher(t, "x"); out != "x-2\n" {
		t.Fatalf("after an edit the floor's x ran %q, want x-2", out)
	}
	if rec := floorRecord(t, "x"); rec.Revision == first || !strings.Contains(errw.String(), "changed") {
		t.Errorf("the edit was not a new digest and a reinstall: %s\n%s", rec.Revision, errw.String())
	}

	errw.Reset()
	if rc := hostExec(nil, []string{"x"}, io.Discard, &errw, nil); rc != 0 || strings.Contains(errw.String(), "installing x") {
		t.Errorf("a launch with nothing changed: rc=%d, and it installed again:\n%s", rc, errw.String())
	}
}

// THE CALL SITE, WITH A BUILD LINE: the floor's install runs the sealed build act once (the stand-in
// jail), the next launch is a store hit with no jail, and the floor runs what the build left.
func TestYoloHostBuildsAPackTreeProgramOnceInTheSealedAct(t *testing.T) {
	hostPackTreeHome(t, packBuildFixtureBuild, "")
	withForkFloor(t)
	runs := 0
	withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 0))
	captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"x"}, io.Discard, &errw, nil); rc != 0 || runs != 1 {
		t.Fatalf("rc=%d with %d build jails, want 0 and one\n%s", rc, runs, errw.String())
	}
	if out := runFloorLauncher(t, "x"); out != "x-1\n" {
		t.Fatalf("the floor's x ran %q, want the build's x-1", out)
	}
	errw.Reset()
	if rc := hostExec(nil, []string{"x"}, io.Discard, &errw, nil); rc != 0 || runs != 1 {
		t.Errorf("the second launch: rc=%d with %d build jails, want no new build\n%s", rc, runs, errw.String())
	}
}

// THE HOST FLOOR'S REFUSAL: a pack-tree program whose build fails stops `yolo host -- <other>` through
// the readiness act, naming the program, the build's log, the retry `yolo pack build fx --setup host-self` and
// the bypass; with the bypass the launch starts without it. Red if the floor still has no pack-tree
// arm (the refusal would say the floor does not build it, with no log and no retry).
func TestAFailedPackTreeBuildRefusesAHostLaunchNamingItsLogAndRetry(t *testing.T) {
	hostPackTreeHome(t, packBuildFixtureBuild, "")
	withForkFloor(t)
	runs := 0
	withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 3))
	got := captureHostExec(t)
	stubBins(t, "other")
	var errw bytes.Buffer
	rc := hostExec(nil, []string{"other"}, io.Discard, &errw, nil)
	if rc == 0 || got.execed {
		t.Fatalf("rc=%d execed=%v, want a refusal\n%s", rc, got.execed, errw.String())
	}
	for _, want := range []string{"program x (pack fx)", "building x from pack fx's tools/x", "its log: ",
		"yolo pack build fx --setup host-self", paths.AllowMissingProgramsEnv + "=1 yolo host -- other"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the refusal lacks %q:\n%s", want, errw.String())
		}
	}
	t.Setenv(paths.AllowMissingProgramsEnv, "1")
	errw.Reset()
	if rc := hostExec(nil, []string{"other"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Errorf("with the bypass: rc=%d execed=%v, want the launch\n%s", rc, got.execed, errw.String())
	}
}

// THE OVERRIDE ARM AT THE CALL SITE (OQ-BG3's ruling): with programs.overrides.x naming a checkout,
// `yolo host -- x` installs the program from the checkout, never the pack's tree, exactly as it installs
// a pack-tree program, and names the checkout; an unchanged checkout installs nothing new; an edit is
// installed at the next `yolo host` launch and never before it. A checkout the program cannot be built
// from refuses the launch, naming it, and never runs the pack's copy. Red if hostfloorpacktree.go stops
// passing the overrides to ResolvePackTreeProgram (the floor then runs the pack's x-1).
func TestYoloHostBuildsAProgramFromItsDevOverride(t *testing.T) {
	dev := floortest.ResolvedTemp(t)
	hostPackTreeHome(t, "", `"programs":{"overrides":{"x":{"path":"`+dev+`"}}},`)
	withForkFloor(t)
	jail := 0
	withFakeCaptureJail(t, func(run.Options) int { jail++; return 1 })
	got := captureHostExec(t)
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "x")
	named := "x: built from your checkout " + dev + " (programs.overrides.x in ~/.config/yolo-jail/config.jsonc)"

	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"x"}, io.Discard, &errw, nil); rc == 0 || got.execed ||
		!strings.Contains(errw.String(), "your checkout "+dev) {
		t.Fatalf("a checkout with no x: rc=%d execed=%v, want a refusal naming the checkout\n%s", rc, got.execed, errw.String())
	}

	setDev := func(word string) {
		t.Helper()
		writeFile(t, filepath.Join(dev, "x"), "#!/bin/sh\necho "+word+"\n")
		if err := os.Chmod(filepath.Join(dev, "x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setDev("dev-1")
	errw.Reset()
	if rc := hostExec(nil, []string{"x"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher ||
		!strings.Contains(errw.String(), named) {
		t.Fatalf("rc=%d target=%s, want the floor's launcher and the line %q\n%s", rc, got.target, named, errw.String())
	}
	if out := runFloorLauncher(t, "x"); out != "dev-1\n" || jail != 0 {
		t.Fatalf("the floor's x ran %q with %d build jails, want the checkout's dev-1 and none", out, jail)
	}
	first := floorRecord(t, "x").Revision

	errw.Reset()
	if rc := hostExec(nil, []string{"x"}, io.Discard, &errw, nil); rc != 0 || floorRecord(t, "x").Revision != first {
		t.Fatalf("an unchanged checkout: rc=%d, want the same install\n%s", rc, errw.String())
	}

	setDev("dev-2")
	if out := runFloorLauncher(t, "x"); out != "dev-1\n" {
		t.Errorf("an edit reached the floor's x with no launch (%q): nothing is built at run time", out)
	}
	errw.Reset()
	if rc := hostExec(nil, []string{"x"}, io.Discard, &errw, nil); rc != 0 {
		t.Fatalf("after an edit: rc=%d\n%s", rc, errw.String())
	}
	if out := runFloorLauncher(t, "x"); out != "dev-2\n" || floorRecord(t, "x").Revision == first {
		t.Fatalf("after an edit and a launch the floor's x ran %q, want dev-2 at a new digest", out)
	}
}
