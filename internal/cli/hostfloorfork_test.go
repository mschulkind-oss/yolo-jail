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
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// withForkFloor is the PRODUCTION floor wiring — its ForkPin, ResolveBuild and Build among it —
// with a fake Node distribution, no capture act, Linux as its platform, and a container runtime on
// PATH for CaptureUnavailable to find. The floor's platform is the distribution's, so the Node
// release it fetches is one the distribution serves on a Mac too.
func withForkFloor(t *testing.T) *floortest.Dist {
	t.Helper()
	dist := floortest.NewLinuxDist(t)
	root := floorLoaderRoot(t)
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := productionHostFloor(out, progs)
		f.GOOS, f.GOARCH = dist.GOOS, dist.GOARCH
		f.Node = hostfloor.NodeDist{BaseURL: dist.URL, Shipped: floortest.Shipped,
			Pinned: map[string]string{dist.Platform: dist.SHA256}}
		f.Environ = append(os.Environ(), dist.Environ()...)
		f.Capture = nil
		// The loader check's root is the fixture's own (floorLoaderRoot): a fork's Node script asks
		// for Node's official build's loader, which a NixOS machine without nix-ld lacks.
		f.Root = root
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	stubContainerRuntime(t)
	return dist
}

// stubContainerRuntime makes this test's machine one that can run a capture or a fork's build: a
// runtime named, and a stub of it on PATH for the production floor's CaptureUnavailable to find. A
// floor test of a fork that can build calls it, because otherwise the answer is the machine's own
// PATH: this jail and the Linux CI runners have podman, and the macOS runner has none.
func stubContainerRuntime(t *testing.T) {
	t.Helper()
	t.Setenv("YOLO_RUNTIME", "podman")
	stubBins(t, "podman")
}

// withoutContainerRuntime makes this test's machine one with no runtime to capture or build with:
// podman named, and a PATH holding only the given directories, so the production floor's
// CaptureUnavailable finds no podman on any machine running the test.
func withoutContainerRuntime(t *testing.T, path ...string) {
	t.Helper()
	t.Setenv("YOLO_RUNTIME", "podman")
	t.Setenv("PATH", strings.Join(path, string(os.PathListSeparator)))
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
// then files what `sh build.sh` would leave (writeForkFloorBuild) under the container jail's home.
// relocatable false records the package as embedding /home/agent in a binary.
func forkFloorBuildJail(t *testing.T, runs *int, relocatable bool) func(run.Options) int {
	t.Helper()
	return func(o run.Options) int {
		*runs++
		if !o.Sealed {
			t.Error("the floor's build ran in an unsealed jail")
		}
		writeForkFloorBuild(t, filepath.Join(o.Workspace, forkSourceLeaf), filepath.Join(o.Workspace, captureOutLeaf),
			"/home/agent", capture.Platform(), relocatable)
		return 0
	}
}

// writeForkFloorBuild files into out what `sh build.sh` in checkout would leave, built under home on
// platform — the package, its bin a relative link into it, and a config file naming home absolutely
// — with the manifest a full reference scan writes. The checked-out build.sh is copied into the
// package, so the floor's copy shows which commit was built.
func writeForkFloorBuild(t *testing.T, checkout, out, home, platform string, relocatable bool) {
	t.Helper()
	built, err := os.ReadFile(filepath.Join(checkout, "build.sh"))
	if err != nil {
		t.Errorf("the pinned commit is not checked out in the build workspace: %v", err)
	}
	tree := capture.TreeDir(out)
	pkg := ".npm-global/lib/node_modules/forkcli"
	script := "#!/usr/bin/env node\n" + string(built)
	config := `{"root":"` + home + `/` + pkg + `"}` + "\n"
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
		Schema: capture.ManifestSchema, Home: home, Platform: platform,
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
			Value: home}},
		RefScan: capture.RefScanFull, Relocatable: relocatable,
	}
	if !relocatable {
		m.NotRelocatable = []string{pkg + "/addon.node is not text and embeds " + home}
	}
	if err := capture.WriteManifest(out, m); err != nil {
		t.Fatal(err)
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

// forkLockCommit is what the fork lock pins forkpack/forkcli to, "" when nothing.
func forkLockCommit(t *testing.T) string {
	t.Helper()
	l, err := packsrc.LoadForkLock(forkLockPath())
	if err != nil {
		t.Fatal(err)
	}
	e, _ := l.Get("forkpack/forkcli")
	return e.Commit
}

// THE MOTIVATING CASE AT THE HOST (FP-D18, applying OQ-PF1): a fork selected and never pinned, and
// no `yolo pack install`. `yolo host -- <forked bin>` pins it at what its ref names, says so in one
// line, builds that commit and runs the floor's copy. The next launch, after the branch moved, runs
// the same build: a standing pin never moves at launch. Before FP-D18 this launch ran the copy on
// PATH and told the user to run `yolo pack install`.
func TestHostLaunchOfAnUnpinnedForkPinsItBuildsItAndRunsTheFloorsCopy(t *testing.T) {
	repo, commit, _ := forkFloorHome(t)
	head1 := strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD"))
	source := "git+file://" + repo + "?ref=main"
	dist := withForkFloor(t)
	dist.Publish("forkcli-pkg", "1.0.0", "bin=forkcli")
	runs := 0
	withFakeCaptureJail(t, forkFloorBuildJail(t, &runs, true))
	got := captureHostExec(t)
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "forkcli")

	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher {
		t.Fatalf("rc=%d target=%s, want the floor's copy %s\n%s", rc, got.target, launcher, errw.String())
	}
	if pin := forkLockCommit(t); pin != head1 {
		t.Fatalf("the fork lock pins %q, want the branch's head %s\n%s", pin, head1, errw.String())
	}
	if runs != 1 {
		t.Errorf("the build jail ran %d times, want once\n%s", runs, errw.String())
	}
	if rec := floorRecord(t, "forkcli"); rec.Revision != head1 {
		t.Errorf("the floor runs %s, want the build at the pin it made, %s", rec.Revision, head1)
	}
	want := "yolo host: pinned fork forkpack/forkcli at " + head1[:8] + " (" + source + "); `yolo pack update` moves it"
	if !strings.Contains(errw.String(), want) {
		t.Errorf("the launch does not disclose the pin it made:\nwant %q\n%s", want, errw.String())
	}
	if strings.Contains(errw.String(), "yolo pack install") {
		t.Errorf("a launch that pinned its fork still names `yolo pack install`:\n%s", errw.String())
	}

	commit("second")
	errw.Reset()
	if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher {
		t.Fatalf("second launch: rc=%d target=%s\n%s", rc, got.target, errw.String())
	}
	if pin := forkLockCommit(t); pin != head1 || runs != 1 || strings.Contains(errw.String(), "pinned fork") {
		t.Errorf("a launch moved a standing pin (pin %s, want %s; %d builds, want 1):\n%s", pin, head1, runs, errw.String())
	}
	assertFloorRuns(t, launcher, floorRecord(t, "forkcli").Entry, "# first")
}

// `yolo host apply`: the dry run says the install pins the fork, and pins and builds nothing;
// --assert pins it, says so, and builds and installs that commit — no `yolo pack install` first.
func TestHostApplyPinsAnUnpinnedForkOnlyUnderAssert(t *testing.T) {
	repo, _, _ := forkFloorHome(t)
	head := strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD"))
	withForkFloor(t)
	runs := 0
	withFakeCaptureJail(t, forkFloorBuildJail(t, &runs, true))

	rc, report := applyWith(t, false, nil)
	if rc != 0 || runs != 0 || !strings.Contains(report, "forkcli: would install (not pinned yet") {
		t.Fatalf("dry run rc=%d builds=%d, want it to say the install pins forkcli first:\n%s", rc, runs, report)
	}
	if pin := forkLockCommit(t); pin != "" {
		t.Fatalf("the dry run pinned the fork at %s", pin)
	}
	rc, report = applyWith(t, true, nil)
	if rc != 0 || runs != 1 || !strings.Contains(report, "forkcli commit "+head[:12]+", installed") {
		t.Fatalf("--assert rc=%d builds=%d:\n%s", rc, runs, report)
	}
	if !strings.Contains(report, "pinned fork forkpack/forkcli at "+head[:8]) {
		t.Errorf("--assert does not disclose the pin it made:\n%s", report)
	}
	if pin := forkLockCommit(t); pin != head {
		t.Errorf("--assert pinned %q, want %s", pin, head)
	}
}

// `yolo capture <forked bin>` of a fork never pinned: it pins it, says so, and builds that commit —
// the explicit rebuild needs no `yolo pack install` before it either.
func TestCaptureOfAnUnpinnedForkPinsItAndBuildsIt(t *testing.T) {
	repo, _, _ := forkFloorHome(t)
	head := strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD"))
	runs := 0
	withFakeCaptureJail(t, forkFloorBuildJail(t, &runs, true))
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"forkcli"}, &out, &errw, false); rc != 0 || runs != 1 {
		t.Fatalf("capture rc=%d builds=%d\n%s\n%s", rc, runs, out.String(), errw.String())
	}
	if pin := forkLockCommit(t); pin != head {
		t.Errorf("capture pinned %q, want %s", pin, head)
	}
	if !strings.Contains(out.String()+errw.String(), "pinned fork forkpack/forkcli at "+head[:8]) {
		t.Errorf("capture does not disclose the pin it made:\n%s\n%s", out.String(), errw.String())
	}
}

// `yolo capture <forked bin>` INSIDE A JAIL pins nothing (no act there does: the fork lock is the
// host's), so a fork the jail's lock does not pin is refused with where its pin is made — never
// "the next launch pins it" or `yolo pack install`, which in a jail pin nothing either.
func TestCaptureOfAnUnpinnedForkInAJailNamesTheHost(t *testing.T) {
	forkFloorHome(t)
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	runs := 0
	withFakeCaptureJail(t, forkFloorBuildJail(t, &runs, true))
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"forkcli"}, &out, &errw, false); rc == 0 || runs != 0 {
		t.Fatalf("capture in a jail rc=%d builds=%d, want a refusal and no build\n%s\n%s", rc, runs, out.String(), errw.String())
	}
	said := out.String() + errw.String()
	if !strings.Contains(said, "recorded on the host") {
		t.Errorf("capture in a jail does not say the fork's pin is the host's:\n%s", said)
	}
	for _, bad := range []string{"the next launch pins it", "yolo pack install", "pinned fork"} {
		if strings.Contains(said, bad) {
			t.Errorf("capture in a jail says %q:\n%s", bad, said)
		}
	}
	if pin := forkLockCommit(t); pin != "" {
		t.Errorf("capture in a jail pinned the fork at %s", pin)
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
	delivered := buildForksForLaunch(run.ForkBuildRequest{Pins: pins, Platform: captureJailPlatform()}, io.Discard, io.Discard, false)
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
// so the lock's commit no longer answers for it) and the edited source cannot be pinned (FP-D18:
// --assert pins it as a launch does, and its ref names nothing), that --assert says it could not
// install it and removes the entry, since the floor never serves a build the lock does not name.
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
	if rc != 1 || !strings.Contains(report, "forkcli: could not install it") ||
		!strings.Contains(report, "forkcli: removed (it is built from source by fork pack forkpack, "+
			"and it has no pin, and pinning it failed") {
		t.Fatalf("--assert with the pin gone and no pin to make rc=%d, want 1:\n%s", rc, report)
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

// withMacForkFloor is withForkFloor on a Mac (FP-D24): the PRODUCTION floor, its build wiring
// included, with darwin as its platform over a darwin Node distribution, no capture act, and a Mac
// whose sandbox account is set up, at a terminal.
func withMacForkFloor(t *testing.T) *floortest.Dist {
	t.Helper()
	dist := floortest.NewDistOn(t, "darwin", goruntime.GOARCH)
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := productionHostFloor(out, progs)
		f.GOOS, f.GOARCH = dist.GOOS, dist.GOARCH
		f.Node = hostfloor.NodeDist{BaseURL: dist.URL, Shipped: floortest.Shipped,
			Pinned: map[string]string{dist.Platform: dist.SHA256}}
		f.Environ = append(os.Environ(), dist.Environ()...)
		f.Capture = nil
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	withMac(t, macSetup{account: true, terminal: true})
	return dist
}

// THE MAC'S FLOOR BUILDS A FORK THROUGH THE MACOS-USER ACT (FP-D24), at its call site: on a Mac whose
// runtime is podman, `yolo host -- forkcli` hands its build to the macos-user runtime for this one
// build, sealed; the pipeline's macos-user arm runs the fork-build act with the build's id for
// this host's platform (darwin/<arch> on a Mac); the floor admits what the act left, under a receipt
// naming its toolchain record, relocates it out of the build's staging home and runs it. The next
// launch finds that build and builds nothing. Drop the Mac's runtime from the Build wiring and the
// container build runs instead.
func TestAMacHostLaunchOfAForkBuildsItAsTheSandboxAccount(t *testing.T) {
	repo, _, _ := forkFloorHome(t)
	head := strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD"))
	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack install rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	withMacForkFloor(t)
	t.Setenv("YOLO_RUNTIME", "podman")
	const record = "yolo 9.9.9, darwin floor /nix/store/test-profile macOS 26.0"
	var acts []macosuser.ForkBuildOptions
	origAct := macForkBuildAct
	macForkBuildAct = func(_ macosuser.Deps, o macosuser.ForkBuildOptions, dest, toolchain string, _ bool) int {
		acts = append(acts, o)
		home := macosuser.CaptureStagingHome(macosuser.ForkBuildStagingRoot("", o.BuildID))
		writeForkFloorBuild(t, o.Source, dest, home, capture.Platform(), true)
		writeFile(t, toolchain, record)
		return 0
	}
	t.Cleanup(func() { macForkBuildAct = origAct })
	jails := 0
	withFakeCaptureJail(t, func(o run.Options) int {
		jails++
		if !o.Sealed || o.Getenv == nil || o.Getenv("YOLO_RUNTIME") != "macos-user" {
			t.Errorf("the Mac's build jail is sealed=%v under runtime %q, want a sealed macos-user build",
				o.Sealed, func() string {
					if o.Getenv == nil {
						return ""
					}
					return o.Getenv("YOLO_RUNTIME")
				}())
			return 1
		}
		// The arm the pipeline runs on macos-user, driven as the pipeline would.
		return o.MacosUserRun(jsonx.NewOrderedMap(), o.Workspace, nil, o.Args, "/flake", "", macosuser.HomeOverlay{},
			macosuser.HostContext{}, false, jsonx.NewOrderedMap(), nil, macosuser.JailDaemons{})
	})
	got := captureHostExec(t)
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "forkcli")

	errw.Reset()
	if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher {
		t.Fatalf("rc=%d target=%s, want the floor's copy %s\n%s", rc, got.target, launcher, errw.String())
	}
	if jails != 1 || len(acts) != 1 {
		t.Fatalf("%d build jails and %d fork-build acts, want one of each\n%s", jails, len(acts), errw.String())
	}
	// THE HOST'S PLATFORM, which on a Mac is darwin/<arch>: the one the act builds for and the floor
	// looks its builds up under.
	b := forkBuild{Fork: floorForkBuild(forkFloorProgram(t), head).Fork, Commit: head, Platform: capture.Platform()}
	if acts[0].BuildID != b.id() || acts[0].Build != "sh build.sh" {
		t.Errorf("the act built %q under id %q, want `sh build.sh` under the host platform's build id %q",
			acts[0].Build, acts[0].BuildID, b.id())
	}
	if !strings.Contains(errw.String(), "as the macos-user sandbox account, sealed under Seatbelt") {
		t.Errorf("the launch does not say the sandbox account builds it:\n%s", errw.String())
	}
	rec := floorRecord(t, "forkcli")
	if rec.Revision != head {
		t.Errorf("the floor runs %s, want the build at the pin %s", rec.Revision, head)
	}
	assertFloorRuns(t, launcher, rec.Entry, "# first")
	entry, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(rec.Capture)
	if err != nil {
		t.Fatalf("the record names no store entry: %v", err)
	}
	if builds, _ := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(entry.Root)); len(builds) != 1 ||
		builds[0].Platform != capture.Platform() || builds[0].Toolchain != record {
		t.Errorf("build receipts = %+v, want one build for this host naming its toolchain record", builds)
	}
	installHome := filepath.Dir(filepath.Dir(filepath.Dir(rec.Entry)))
	if cfg, _ := os.ReadFile(filepath.Join(installHome, ".npm-global", "lib", "node_modules", "forkcli",
		"config.json")); !strings.Contains(string(cfg), installHome) {
		t.Errorf("the floor's copy was not relocated out of the build's staging home: %s", cfg)
	}

	errw.Reset()
	if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || got.target != launcher || jails != 1 {
		t.Fatalf("second launch: rc=%d target=%s jails=%d, want the darwin build found and nothing built\n%s",
			rc, got.target, jails, errw.String())
	}
}

// forkFloorProgram is forkcli as the floor reads it from forkFloorHome's selection.
func forkFloorProgram(t *testing.T) hostfloor.Program {
	t.Helper()
	sel := selectConfiguredHostPacks()
	p, ok := floorProgram(floorPrograms(sel.packs), "forkcli")
	if !ok {
		t.Fatal("the selection delivers no forkcli")
	}
	return p
}
