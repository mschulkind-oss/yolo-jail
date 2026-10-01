package cli

// hostfloorfork_test.go drives the host floor's FORK recipe (internal/hostfloor/built.go;
// docs/design/forked-programs-as-packs.md FP-D4, the plan's step 7) through the call sites a user
// reaches: `yolo pack install` pins a REAL local git repository standing in for the fork's remote,
// `yolo host -- <forked bin>` builds the pinned commit through the build act (buildFork: the lock,
// the checkout out of the pack store, the sealed capture jail, the admit and the build receipt) and
// runs the floor's copy, and `yolo pack update` moves the pin. Only three things are the test's: the
// run pipeline the build jail would boot (captureRunPipeline, which files what a build would leave
// and never runs the fork's build line), the floor's Node (a fake distribution), and Linux as the
// floor's platform.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// withFloorOnLinux makes the floor newHostFloor builds a Linux one, whatever this machine is: the
// one platform a fork's build (made in a Linux capture jail) is the floor's on.
func withFloorOnLinux(t *testing.T) {
	t.Helper()
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := orig(out, progs)
		f.GOOS = "linux"
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
}

// withForkFloor is the PRODUCTION floor wiring — its ForkPin, ResolveBuild and Build among it —
// with a fake Node distribution, no capture act, Linux as its platform, and a container runtime on
// PATH for CaptureUnavailable to find.
func withForkFloor(t *testing.T) *floortest.Dist {
	t.Helper()
	dist := floortest.NewDist(t)
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := productionHostFloor(out, progs)
		f.GOOS = "linux"
		f.Node = hostfloor.NodeDist{BaseURL: dist.URL, Shipped: floortest.Shipped,
			Pinned: map[string]string{dist.Platform: dist.SHA256}}
		f.Environ = append(os.Environ(), dist.Environ()...)
		f.Capture = nil
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	t.Setenv("YOLO_RUNTIME", "podman")
	stubBins(t, "podman")
	return dist
}

// forkFloorHome is a temp HOME whose user config selects a base pack declaring forkcli via npm and
// a fork pack building it from a real local git repository, Node-package shaped (the 2026-10-01
// stand-in's shape: a bin under ~/.npm-global linking into the package). It returns the repository,
// a func that commits to it, and the fork pack's manifest, for a test that edits the declaration.
func forkFloorHome(t *testing.T) (repo string, commit func(msg string) string, forkManifest string) {
	t.Helper()
	repo, commit = forkRepo(t)
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(floortest.ResolvedTemp(t))
	packs := floortest.ResolvedTemp(t)
	writeFile(t, filepath.Join(packs, "basepack", "pack.json"),
		`{"name":"basepack","contributes":[{"kind":"program","bin":"forkcli","via":"npm","package":"forkcli-pkg"}]}`)
	forkManifest = filepath.Join(packs, "forkpack", "pack.json")
	writeFile(t, forkManifest, `{"name":"forkpack","contributes":[`+
		`{"kind":"program","bin":"forkcli","via":"source","fork_of":"basepack",`+
		`"source":"git+file://`+repo+`?ref=main","build":"sh build.sh",`+
		`"produces":[".npm-global/bin/forkcli",".npm-global/lib/node_modules/forkcli"]}]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+filepath.Join(packs, "forkpack")+`","name":"forkpack"}]}`)
	origAuth, origRefresh, origApply := prepareOpenAIAuthHost, programRefresh, hostApplyFromPackUpdate
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	programRefresh = func(richtext.Printer, io.Writer) int { return 0 }
	hostApplyFromPackUpdate = func([]string, io.Writer, io.Writer, bool, io.Reader) int { return 0 }
	t.Cleanup(func() {
		prepareOpenAIAuthHost, programRefresh, hostApplyFromPackUpdate = origAuth, origRefresh, origApply
	})
	return repo, commit, forkManifest
}

// forkFloorBuildJail stands in for the sealed build jail: it checks the seal and the checkout,
// then files what `sh build.sh` would leave — the package, its bin a relative link into it, and a
// config file naming the build home absolutely — with the manifest a full reference scan writes.
// The checked-out build.sh is copied into the package, so the floor's copy shows which commit was
// built. relocatable false records the package as embedding /home/agent in a binary.
func forkFloorBuildJail(t *testing.T, runs *int, relocatable bool) func(run.Options) int {
	t.Helper()
	return func(o run.Options) int {
		*runs++
		if !o.Sealed {
			t.Error("the floor's build ran in an unsealed jail")
		}
		built, err := os.ReadFile(filepath.Join(o.Workspace, forkSourceLeaf, "build.sh"))
		if err != nil {
			t.Errorf("the pinned commit is not checked out in the build workspace: %v", err)
		}
		out := filepath.Join(o.Workspace, captureOutLeaf)
		tree := capture.TreeDir(out)
		pkg := ".npm-global/lib/node_modules/forkcli"
		script := "#!/usr/bin/env node\n" + string(built)
		config := `{"root":"/home/agent/` + pkg + `"}` + "\n"
		writeFile(t, filepath.Join(tree, filepath.FromSlash(pkg), "config.json"), config)
		writeFile(t, filepath.Join(tree, filepath.FromSlash(pkg), "cli.js"), script)
		if err := os.Chmod(filepath.Join(tree, filepath.FromSlash(pkg), "cli.js"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(tree, ".npm-global", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		link := "../lib/node_modules/forkcli/cli.js"
		if err := os.Symlink(link, filepath.Join(tree, ".npm-global", "bin", "forkcli")); err != nil {
			t.Fatal(err)
		}
		m := &capture.Manifest{
			Schema: capture.ManifestSchema, Home: "/home/agent", Platform: capture.Platform(),
			Surfaces: []string{".npm-global", ".local", "go"}, Excluded: capture.DefaultExcludes(),
			Entries: []capture.ManifestEntry{
				{Path: ".npm-global", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".npm-global/bin", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".npm-global/bin/forkcli", Kind: capture.KindSymlink, Target: link},
				{Path: ".npm-global/lib", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".npm-global/lib/node_modules", Kind: capture.KindDir, Mode: "0755"},
				{Path: pkg, Kind: capture.KindDir, Mode: "0755"},
				{Path: pkg + "/cli.js", Kind: capture.KindFile, Mode: "0755", Size: int64(len(script))},
				{Path: pkg + "/config.json", Kind: capture.KindFile, Mode: "0644", Size: int64(len(config))},
			},
			AbsoluteRefs: []capture.AbsoluteRef{{Path: pkg + "/config.json", Kind: capture.RefFileContent,
				Value: "/home/agent"}},
			RefScan: capture.RefScanFull, Relocatable: relocatable,
		}
		if !relocatable {
			m.NotRelocatable = []string{pkg + "/addon.node is not text and embeds /home/agent"}
		}
		if err := capture.WriteManifest(out, m); err != nil {
			t.Fatal(err)
		}
		return 0
	}
}

// The motivating path, end to end on the host: a pinned fork's program, selected, runs as the
// floor's copy of the build at the pin — built once in the sealed jail, materialized out of the
// jail's home, started on the floor's own Node — and the base's npm package is never installed.
// The next launch builds nothing; a moved pin builds and installs the new commit.
func TestHostLaunchOfAPinnedForkBuildsItAndRunsTheFloorsCopy(t *testing.T) {
	repo, commit, _ := forkFloorHome(t)
	head1 := strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD"))
	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack install rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	dist := withForkFloor(t)
	dist.Publish("forkcli-pkg", "1.0.0", "bin=forkcli")
	runs := 0
	withFakeCaptureJail(t, forkFloorBuildJail(t, &runs, true))
	got := captureHostExec(t)
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "forkcli")

	errw.Reset()
	if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher {
		t.Fatalf("rc=%d target=%s, want the floor's copy %s\n%s", rc, got.target, launcher, errw.String())
	}
	if runs != 1 {
		t.Fatalf("the build jail ran %d times on the first launch, want once\n%s", runs, errw.String())
	}
	rec := floorRecord(t, "forkcli")
	if rec.Via != "source" || rec.Revision != head1 {
		t.Errorf("the floor's record is %+v, want the build at the pin %s", rec, head1)
	}
	assertFloorRuns(t, launcher, rec.Entry, "# first")
	// THE BUILD IS THE STORE'S, under its receipt, and the host's copy was moved out of the jail's
	// home: the config file names the floor's install, not /home/agent.
	store := &capture.Store{Dir: paths.CapturesDir()}
	entry, err := store.Resolve(rec.Capture)
	if err != nil {
		t.Fatalf("the record names no store entry: %v", err)
	}
	if builds, _ := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(entry.Root)); len(builds) != 1 ||
		builds[0].Revision != head1 {
		t.Errorf("build receipts = %+v", builds)
	}
	installHome := filepath.Dir(filepath.Dir(filepath.Dir(rec.Entry)))
	if cfg, _ := os.ReadFile(filepath.Join(installHome, ".npm-global", "lib", "node_modules", "forkcli",
		"config.json")); strings.Contains(string(cfg), "/home/agent") {
		t.Errorf("the floor's copy still names the jail's home: %s", cfg)
	}
	if calls := dist.NpmCalls("install"); len(calls) != 0 {
		t.Errorf("the floor installed the base's npm package for a forked program: %v", calls)
	}
	if !strings.Contains(errw.String(), "building it from fork pack forkpack's source in a sealed jail") {
		t.Errorf("the launch does not say it builds:\n%s", errw.String())
	}

	// The second launch: the floor's copy is current, and nothing is built.
	errw.Reset()
	if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher || runs != 1 {
		t.Fatalf("second launch: rc=%d target=%s builds=%d\n%s", rc, got.target, runs, errw.String())
	}

	// A MOVED PIN: the branch moves, `yolo pack update` moves the pin, and the next launch builds
	// and runs the new commit.
	head2 := commit("second")
	if rc := packMain([]string{"update"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack update rc=%d\n%s", rc, errw.String())
	}
	errw.Reset()
	if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher {
		t.Fatalf("after the pin moved: rc=%d target=%s\n%s", rc, got.target, errw.String())
	}
	if runs != 2 {
		t.Fatalf("the build jail ran %d times in all, want once per pin\n%s", runs, errw.String())
	}
	rec = floorRecord(t, "forkcli")
	if rec.Revision != head2 {
		t.Errorf("after the pin moved the floor runs %s, want %s", rec.Revision, head2)
	}
	assertFloorRuns(t, launcher, rec.Entry, "# second")
	if !strings.Contains(errw.String(), "now pins it at commit "+head2[:12]) {
		t.Errorf("the reinstall does not say the pin moved:\n%s", errw.String())
	}
}

// THE HOST AND A JAIL ASK FOR ONE BUILD: a fork a jail launch built (its own pin reader and its own
// build call, buildForksForLaunch) is the floor's copy at the host with no second build — found by
// the floor's hit check, which keys the store on the same commit, recipe and platform — even on a
// machine with no container runtime to build one. Without that hit the floor would have no build,
// no way to make one, and the launch would look for forkcli on a PATH that has none.
func TestHostLaunchRunsTheBuildAJailLaunchMadeOnAMachineThatCannotBuild(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("a jail launch's build is of linux/" + goruntime.GOARCH + ", which only a Linux host's floor holds")
	}
	forkFloorHome(t)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack install rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	dist := withForkFloor(t)
	dist.Publish("forkcli-pkg", "1.0.0", "bin=forkcli")
	runs := 0
	withFakeCaptureJail(t, forkFloorBuildJail(t, &runs, true))

	sel := selectConfiguredHostPacks()
	pins := packload.LoadForkPins(packload.Forks(sel.packs), forkLockPath())
	delivered := buildForksForLaunch(pins, captureJailPlatform(), io.Discard, io.Discard, false)
	if d := delivered["forkcli"]; d.Key == "" || runs != 1 {
		t.Fatalf("the jail launch's build: %+v, %d builds", d, runs)
	}

	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := orig(out, progs)
		f.CaptureUnavailable = func() string { return "no container runtime (podman) is on PATH" }
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	got := captureHostExec(t)
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "forkcli")
	errw.Reset()
	if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher {
		t.Fatalf("rc=%d target=%s, want the floor's copy %s\n%s", rc, got.target, launcher, errw.String())
	}
	if runs != 1 {
		t.Errorf("the host built the fork again (%d builds in all)\n%s", runs, errw.String())
	}
	if rec := floorRecord(t, "forkcli"); rec.Capture != delivered["forkcli"].Key {
		t.Errorf("the floor runs entry %s, want the one the jail launch built, %s", rec.Capture, delivered["forkcli"].Key)
	}
}

// A build that cannot move out of the jail's home is not the floor's copy: the launch says so,
// naming that home, runs the copy on PATH (OQ-HE11), and does not rebuild on the next launch — the
// same commit and recipe would build the same bytes.
func TestHostLaunchOfAForkWhoseBuildCannotLeaveTheJailRunsThePathCopy(t *testing.T) {
	forkFloorHome(t)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack install rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	withForkFloor(t)
	runs := 0
	withFakeCaptureJail(t, forkFloorBuildJail(t, &runs, false))
	stub := filepath.Join(stubBins(t, "forkcli"), "forkcli")
	got := captureHostExec(t)
	for launch := 1; launch <= 2; launch++ {
		errw.Reset()
		if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || got.target != stub {
			t.Fatalf("launch %d: rc=%d target=%s, want the PATH copy %s\n%s", launch, rc, got.target, stub, errw.String())
		}
		for _, want := range []string{"yolo has no copy of forkcli on this machine",
			"built for the jail's home, /home/agent", "runs in a jail only"} {
			if !strings.Contains(errw.String(), want) {
				t.Errorf("launch %d: stderr lacks %q:\n%s", launch, want, errw.String())
			}
		}
	}
	if runs != 1 {
		t.Errorf("the build jail ran %d times over two launches, want once", runs)
	}
	if _, err := os.Stat(filepath.Join(paths.HostFloorDir(), "bin", "forkcli")); !os.IsNotExist(err) {
		t.Errorf("the floor holds a launcher for a build that cannot run outside a jail: %v", err)
	}
}

// `yolo host apply`: the dry run says it would install the pinned fork and builds nothing;
// --assert builds and installs it, naming the commit; and once its pin is gone (the source edited,
// so the lock's commit no longer answers for it) the next --assert removes the entry, since the
// floor never serves a build the lock does not name.
func TestHostApplyProvisionsAPinnedForkAndRemovesItWhenThePinGoes(t *testing.T) {
	repo, _, forkManifest := forkFloorHome(t)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack install rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	head := strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD"))
	withForkFloor(t)
	runs := 0
	withFakeCaptureJail(t, forkFloorBuildJail(t, &runs, true))
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "forkcli")

	rc, report := applyWith(t, false, nil)
	if rc != 0 || !strings.Contains(report, "forkcli: would install (not installed yet)") || runs != 0 {
		t.Fatalf("dry run rc=%d builds=%d, want it to say it would install forkcli:\n%s", rc, runs, report)
	}
	rc, report = applyWith(t, true, nil)
	if rc != 0 || runs != 1 || !strings.Contains(report, "forkcli commit "+head[:12]+", installed") {
		t.Fatalf("--assert rc=%d builds=%d:\n%s", rc, runs, report)
	}
	if _, err := os.Stat(launcher); err != nil {
		t.Fatalf("--assert did not provision forkcli: %v", err)
	}

	body, err := os.ReadFile(forkManifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, forkManifest, strings.Replace(string(body), "?ref=main", "?ref=other", 1))
	// A program the floor does not deliver is the dependency gate's to find on PATH (HP-D9), and
	// a missing one refuses the apply before the floor stage runs: a hand-installed copy is there.
	stubBins(t, "forkcli")
	rc, report = applyWith(t, true, nil)
	if rc != 0 || !strings.Contains(report, "forkcli: removed (it is built from source by fork pack forkpack, "+
		"and its source changed since it was pinned") {
		t.Fatalf("--assert with the pin gone rc=%d:\n%s", rc, report)
	}
	if _, err := os.Stat(launcher); err == nil {
		t.Error("the floor kept a build its fork's lock no longer names")
	}
}

// floorRecord reads the floor's record of bin, as `yolo check` and a launch do.
func floorRecord(t *testing.T, bin string) *hostfloor.Record {
	t.Helper()
	rec, ok := (&hostfloor.Floor{Dir: paths.HostFloorDir()}).Records()[bin]
	if !ok {
		t.Fatalf("the floor holds no record of %s", bin)
	}
	return rec
}

// assertFloorRuns runs the floor's launcher with no environment and checks it started the build's
// program on the floor's own Node, and that the program is the build of the commit whose build.sh
// says want.
func assertFloorRuns(t *testing.T, launcher, entry, want string) {
	t.Helper()
	cmd := exec.Command(launcher, "--version")
	cmd.Env = []string{}
	got, err := cmd.CombinedOutput()
	if err != nil || string(got) != "node:"+entry+" --version\n" {
		t.Fatalf("the floor's launcher ran %q (%v), want the build's program on the floor's Node", got, err)
	}
	body, err := os.ReadFile(entry)
	if err != nil || !strings.Contains(string(body), want) {
		t.Errorf("the floor's program is %q (%v), want the build of the commit whose build.sh says %q", body, err, want)
	}
}
