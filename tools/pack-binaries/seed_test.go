package main

// seed_test.go pins what main pins between two releases (docs/design/broker-as-a-pack.md
// BP-D15, OQ-BP7 ruled 2026-10-05: main pins its own build): the digest-only check `just
// check-ci` runs, the digest-only pin it names, and the seed `just install` runs, which builds
// this machine's builds from the tree into the cache every launch reads.
//
// Call-site checks — each test fails if its production line goes:
//   - drop packbin.Seed from task.seed → TestSeedAdmitsThisMachinesBuildsAtTheirPins,
//     TestASeededBuildIsWhatAFromSourceLaunchRuns.
//   - drop the repin arm of task.seed → TestSeedRepinsAProgramEditedAfterThePin.
//   - drop the digest comparison from compare → TestDigestOnlyCheckRefusesAStalePin.
//   - drop the url-keeping branch of pinManifest → TestDigestOnlyPinKeepsEachURL.
//   - drop deps.cacheDir's default (paths.PackBinariesDir) → TestSeedDefaultsToTheCacheEveryLaunchReads.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packbin"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/releasematrix"
)

// The machine fixture is fixtureCheckout for THIS machine's architecture, so seed has a build to
// make here on every runner: the release ships `yolo` to linux and darwin on runtime.GOARCH, and
// the loophole runs toold on the host and in the jail, so BP-D7 wants darwin/<arch> and
// linux/<arch>, and a launch here asks for this machine's host build and linux/<arch>.

var (
	machineHost = runtime.GOOS + "/" + runtime.GOARCH
	machineJail = "linux/" + runtime.GOARCH
)

// machineMain is the program a from-source tree ships; marker says which tree built it.
func machineMain(marker string) string {
	return "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"toold from " + marker + "\") }\n"
}

func machineManifest() string {
	platforms := []string{"darwin/" + runtime.GOARCH, "linux/" + runtime.GOARCH}
	var builds []string
	for _, p := range platforms {
		builds = append(builds, `      "`+p+`": `+placeholder(p)+`, // the `+p+` build`)
	}
	return `// A fixture loophole whose program runs on the host and in the jail.
{
  "name": "tool",
  "description": "fixture",
  "version": 1,
  /* the program both daemons run */
  "binaries": {
    "toold": {
` + strings.Join(builds, "\n") + `
    },
  },
  "host_daemon": {"cmd": ["{binary:toold}", "--socket", "{socket}"], "publishes": "socket"},
  "jail_daemon": {"cmd": ["{jail_binary:toold}", "serve"]},
}
`
}

func machineCheckout(t *testing.T) string {
	t.Helper()
	root := fixtureCheckout(t)
	writeFile(t, root, releasematrix.GoreleaserConfig,
		"builds:\n  - id: yolo\n    main: ./cmd/yolo\n    goos: [linux, darwin]\n    goarch: ["+
			runtime.GOARCH+"]\n")
	writeFile(t, root, "cmd/toold/main.go", machineMain("the pinned tree"))
	writeFile(t, root, fixtureManifestPath, machineManifest())
	return root
}

// pins reads the fixture's sha256 for each platform.
func pins(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, bb := range decodeFixture(t, root).Binaries[0].Builds {
		out[bb.Platform] = bb.SHA256
	}
	return out
}

func mustRun(t *testing.T, root string, args ...string) result {
	t.Helper()
	r := runTool(t, root, args...)
	if r.code != 0 {
		t.Fatalf("%v: exit %d\n%s%s", args, r.code, r.stdout, r.stderr)
	}
	return r
}

// cacheFiles lists the builds under a pack-binary cache as "<sha256>/<name>".
func cacheFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	sums, _ := os.ReadDir(dir)
	for _, s := range sums {
		if !s.IsDir() {
			continue
		}
		names, _ := os.ReadDir(filepath.Join(dir, s.Name()))
		for _, n := range names {
			out = append(out, s.Name()+"/"+n.Name())
		}
	}
	return out
}

// Between releases a url still names the last one, so the check `just check-ci` runs compares
// the digests alone: a pin for v0.2.0 passes it, where the release's check for v0.3.0 refuses
// the url.
func TestDigestOnlyCheckAcceptsAURLNamingAnEarlierRelease(t *testing.T) {
	root := fixtureCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	r := mustRun(t, root, "check")
	if !strings.Contains(r.stdout, "this tree builds every official build's pinned sha256 (2 builds)") {
		t.Errorf("the digest-only check did not say what it checked:\n%s", r.stdout)
	}
	if r := runTool(t, root, "check", "0.3.0"); r.code != 1 || !strings.Contains(r.stderr, "url names v0.2.0's release") {
		t.Errorf("the release's check no longer refuses another release's url: exit %d\n%s", r.code, r.stderr)
	}
}

// What `just check-ci` refuses: a pin the tree no longer reproduces, naming the binary, the
// platform, both digests and the digest-only pin command.
func TestDigestOnlyCheckRefusesAStalePin(t *testing.T) {
	root := fixtureCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	old := pins(t, root)
	writeFile(t, root, "cmd/toold/main.go", strings.Replace(fixtureMain, `"toold"`, `"toold, edited"`, 1))

	r := runTool(t, root, "check")
	if r.code != 1 {
		t.Fatalf("the digest-only check of an edited program: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{
		fixtureManifestPath + ": binary toold (linux/amd64): this tree builds sha256 ",
		"and the manifest pins " + old["linux/amd64"],
		"binary toold (darwin/amd64)",
		"run `just pin-pack-binaries` and commit the result",
		"problem(s) with the official pack binaries against this tree",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, r.stderr)
		}
	}
	if strings.Contains(r.stderr, "pin-pack-binaries 0.2.0") {
		t.Errorf("between releases the refusal names a release's pin:\n%s", r.stderr)
	}
}

// The digest-only pin writes each sha256 and keeps each url, comments and all, and the check
// then passes — the release's check for the url's own version too.
func TestDigestOnlyPinKeepsEachURL(t *testing.T) {
	root := fixtureCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	old := pins(t, root)
	writeFile(t, root, "cmd/toold/main.go", strings.Replace(fixtureMain, `"toold"`, `"toold, edited"`, 1))

	r := mustRun(t, root, "pin")
	if !strings.Contains(r.stdout, "pinned to this tree's builds (each url kept)") {
		t.Errorf("pin did not say it kept the urls:\n%s", r.stdout)
	}
	body := readFile(t, root, fixtureManifestPath)
	for _, keep := range []string{"// A fixture loophole", "/* the program the host daemon runs */",
		"// the linux/amd64 build"} {
		if !strings.Contains(body, keep) {
			t.Errorf("the digest-only pin lost %q:\n%s", keep, body)
		}
	}
	for _, bb := range decodeFixture(t, root).Binaries[0].Builds {
		if want := releasematrix.AssetURL("toold", "0.2.0", bb.Platform); bb.URL != want {
			t.Errorf("%s url = %s, want the one v0.2.0's pin wrote, %s", bb.Platform, bb.URL, want)
		}
		if bb.SHA256 == old[bb.Platform] {
			t.Errorf("%s sha256 did not move after the program did", bb.Platform)
		}
	}
	mustRun(t, root, "check")
	mustRun(t, root, "check", "0.2.0")
	if r := mustRun(t, root, "pin"); !strings.Contains(r.stdout, "already pinned to this tree's builds") {
		t.Errorf("a second digest-only pin rewrote the manifest:\n%s", r.stdout)
	}
}

// A url the census refuses is one the digest-only pin would keep, so it refuses before it builds,
// naming the release's pin, which writes the url.
func TestDigestOnlyPinRefusesAURLItWouldKeep(t *testing.T) {
	root := fixtureCheckout(t)
	writeFile(t, root, fixtureManifestPath, strings.ReplaceAll(fixtureManifest("darwin/amd64", "linux/amd64"),
		releasematrix.ReleaseDownloadBase, "https://example.test/elsewhere/"))
	before := readFile(t, root, fixtureManifestPath)
	r := runTool(t, root, "pin")
	if r.code != 1 || !strings.Contains(r.stderr, "is not the release file BP-D8 names") ||
		!strings.Contains(r.stderr, "nothing was pinned") || r.fetched {
		t.Errorf("the digest-only pin of a bad url: exit %d fetched=%v\n%s%s", r.code, r.fetched, r.stdout, r.stderr)
	}
	if after := readFile(t, root, fixtureManifestPath); after != before {
		t.Errorf("a refused pin rewrote the manifest:\n%s", after)
	}
	mustRun(t, root, "pin", "0.2.0")
}

// `just install`'s step: this machine's builds — its host build and linux/<arch> for the jail,
// once each — built from the tree and admitted to the cache at their pinned digests, 0555. A
// build for another machine is not built or seeded.
func TestSeedAdmitsThisMachinesBuildsAtTheirPins(t *testing.T) {
	root := machineCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	pinned := pins(t, root)
	cache := t.TempDir()

	r := mustRun(t, root, "seed", cache)
	if !r.fetched {
		t.Error("seed built without the pinned toolchain")
	}
	want := map[string]bool{pinned[machineHost] + "/toold": true, pinned[machineJail] + "/toold": true}
	got := cacheFiles(t, cache)
	if len(got) != len(want) {
		t.Fatalf("the cache holds %v, want exactly this machine's builds %v\n%s", got, want, r.stdout)
	}
	for _, f := range got {
		if !want[f] {
			t.Errorf("seed put %s in the cache, which no launch here asks for", f)
		}
	}
	for _, platform := range []string{machineHost, machineJail} {
		p := packbin.Path(cache, pinned[platform], "toold")
		sum, err := fileSHA256(p)
		if err != nil || sum != pinned[platform] {
			t.Errorf("%s: sha256 %s (%v), and the manifest pins %s", p, sum, err, pinned[platform])
		}
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o555 {
			t.Errorf("%s is not the cache's 0555: %v", p, err)
		}
		if !strings.Contains(r.stdout, "seeded binary toold ("+platform+")") &&
			!strings.Contains(r.stdout, "binary toold ("+platform+") is already in the cache") {
			t.Errorf("seed did not say what it seeded for %s:\n%s", platform, r.stdout)
		}
	}
	again := mustRun(t, root, "seed", cache)
	if !strings.Contains(again.stdout, "is already in the cache") {
		t.Errorf("a second seed did not find its builds:\n%s", again.stdout)
	}
}

// The integration harness's case: a program edited after its pin is not seeded under either
// digest, the manifest is not touched, and the refusal names the pin command.
func TestSeedRefusesAStalePinWithoutRepin(t *testing.T) {
	root := machineCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	writeFile(t, root, "cmd/toold/main.go", machineMain("an edited tree"))
	before := readFile(t, root, fixtureManifestPath)
	cache := t.TempDir()

	r := runTool(t, root, "seed", cache)
	if r.code != 1 {
		t.Fatalf("seed of a stale pin: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{"binary toold (" + machineJail + "): this tree builds sha256 ",
		"run `just pin-pack-binaries` and commit the result", "were not seeded"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, r.stderr)
		}
	}
	if got := cacheFiles(t, cache); len(got) != 0 {
		t.Errorf("a stale pin seeded %v", got)
	}
	if after := readFile(t, root, fixtureManifestPath); after != before {
		t.Errorf("seed without --repin rewrote the manifest:\n%s", after)
	}
}

// THE RULING'S CASE: fork, change the program, `just install`, and it works. --repin re-pins
// every build's digest (each url kept, so the release's own pin still writes those), then seeds
// the new build — so the manifest the install embeds pins the program the tree now builds.
func TestSeedRepinsAProgramEditedAfterThePin(t *testing.T) {
	root := machineCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	old := pins(t, root)
	writeFile(t, root, "cmd/toold/main.go", machineMain("a fork"))
	cache := t.TempDir()

	r := mustRun(t, root, "seed", "--repin", cache)
	if !strings.Contains(r.stdout, "re-pinned binary toold") || !strings.Contains(r.stdout, "commit the manifest") {
		t.Errorf("seed --repin did not say it re-pinned, and what to commit:\n%s", r.stdout)
	}
	// Committing moves HEAD through packs/, which version.SourceSkew compares the installed
	// binary's stamp against, so the step after the commit is another install (BP-D23).
	if !strings.Contains(r.stdout, "then re-run `just install`") {
		t.Errorf("seed --repin does not say to re-run `just install` after the commit:\n%s", r.stdout)
	}
	now := pins(t, root)
	for _, platform := range []string{machineHost, machineJail} {
		if now[platform] == old[platform] {
			t.Errorf("%s, which this machine built, is still pinned to the old build %s", platform,
				now[platform])
		}
	}
	for _, bb := range decodeFixture(t, root).Binaries[0].Builds {
		if want := releasematrix.AssetURL("toold", "0.2.0", bb.Platform); bb.URL != want {
			t.Errorf("%s url = %s; the re-pin keeps %s", bb.Platform, bb.URL, want)
		}
	}
	if !strings.Contains(readFile(t, root, fixtureManifestPath), "/* the program both daemons run */") {
		t.Error("the re-pin lost the manifest's comments")
	}
	for _, platform := range []string{machineHost, machineJail} {
		if !packbin.Present(packbin.Path(cache, now[platform], "toold")) {
			t.Errorf("the re-pinned %s build is not in the cache: %v", platform, cacheFiles(t, cache))
		}
	}
	// What this machine did not build keeps its pin, so the landing gate refuses it until
	// `just pin-pack-binaries` re-pins every platform. The fixture pins darwin/<arch> and
	// linux/<arch>: on Linux the darwin one is a platform this machine did not build, and on a
	// Mac there is none.
	other := machineHost == machineJail
	if r := runTool(t, root, "check"); (r.code != 0) != other {
		t.Errorf("check after a re-pin: exit %d, and another platform's pin is stale: %v\n%s", r.code,
			other, r.stderr)
	}
	mustRun(t, root, "pin")
	mustRun(t, root, "check")
}

// A RE-PIN WRITES ONLY WHAT THIS MACHINE BUILT AND VERIFIED (BP-D25): its host build and its
// jail build. A Mac whose native build did not reproduce a Linux-made pin must not rewrite every
// platform's digest from its own toolchain, so another platform's pin is left as it was, for
// `just pin-pack-binaries` and the landing gate. The fixture ships to both architectures, so on
// any runner there are platforms this machine does not build.
func TestSeedRepinRewritesOnlyThisMachinesPlatforms(t *testing.T) {
	root := machineCheckout(t)
	writeFile(t, root, releasematrix.GoreleaserConfig,
		"builds:\n  - id: yolo\n    main: ./cmd/yolo\n    goos: [linux, darwin]\n    goarch: [amd64, arm64]\n")
	var builds []string
	for _, p := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"} {
		builds = append(builds, `      "`+p+`": `+placeholder(p)+`,`)
	}
	manifest := strings.Replace(machineManifest(), `      "darwin/`+runtime.GOARCH+`": `+
		placeholder("darwin/"+runtime.GOARCH)+`, // the darwin/`+runtime.GOARCH+` build
      "linux/`+runtime.GOARCH+`": `+placeholder("linux/"+runtime.GOARCH)+`, // the linux/`+
		runtime.GOARCH+` build`, strings.Join(builds, "\n"), 1)
	writeFile(t, root, fixtureManifestPath, manifest)
	mustRun(t, root, "pin", "0.2.0")
	old := pins(t, root)
	if len(old) != 4 {
		t.Fatalf("the fixture pins %v, want four platforms", old)
	}
	writeFile(t, root, "cmd/toold/main.go", machineMain("a fork"))

	r := mustRun(t, root, "seed", "--repin", t.TempDir())
	now := pins(t, root)
	mine := map[string]bool{machineHost: true, machineJail: true}
	for platform, sum := range now {
		switch {
		case mine[platform] && sum == old[platform]:
			t.Errorf("%s, which this machine built, was not re-pinned", platform)
		case !mine[platform] && sum != old[platform]:
			t.Errorf("%s, which this machine did not build, was re-pinned to %s", platform, sum)
		}
	}
	if !strings.Contains(r.stdout, "just pin-pack-binaries") {
		t.Errorf("seed --repin does not name the pin that re-pins the other platforms:\n%s", r.stdout)
	}
}

// With no directory named, seed fills the cache a launch on this machine reads: the default
// deps' cacheDir and internal/loopholes' BinaryCacheDir resolve to one directory under HOME.
func TestSeedDefaultsToTheCacheEveryLaunchReads(t *testing.T) {
	root := machineCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	home := t.TempDir()
	t.Setenv("HOME", home)

	d := defaultDeps(root, os.Environ())
	g := hostGo(t)
	d.toolchain = func() (string, error) { return g, nil } // never download in a test
	d.packs = os.DirFS(filepath.Join(root, "packs"))       // the fixture's tree, not this one's
	var out, errb bytes.Buffer
	if code := run([]string{"seed"}, &out, &errb, d); code != 0 {
		t.Fatalf("seed: exit %d\n%s%s", code, out.String(), errb.String())
	}
	launch := loopholes.BinaryCacheDir()
	if launch != paths.PackBinariesDirUnder(home) {
		t.Fatalf("the launch reads %s, not the cache under HOME %s", launch, paths.PackBinariesDirUnder(home))
	}
	if !packbin.Present(packbin.Path(launch, pins(t, root)[machineJail], "toold")) {
		t.Errorf("seed did not fill the cache the launch reads (%s): %v\n%s", launch,
			cacheFiles(t, launch), out.String())
	}
}

// A FROM-SOURCE LAUNCH RUNS THE TREE'S PROGRAM, WITH NO NETWORK. The tree is pinned for v0.2.0,
// whose release does not hold this build (no release does), and no server exists: the loophole
// is active from what seed put in the cache, its host argv runs the tree's own build, and the
// jail's build is the one the launch binds.
func TestASeededBuildIsWhatAFromSourceLaunchRuns(t *testing.T) {
	root := machineCheckout(t)
	writeFile(t, root, "cmd/toold/main.go", machineMain("this very tree"))
	mustRun(t, root, "pin", "0.2.0")
	cache := t.TempDir()
	mustRun(t, root, "seed", cache)
	pinned := pins(t, root)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION") // a host launch, wherever this test runs
	prev := loopholes.BinaryCacheDir
	loopholes.BinaryCacheDir = func() string { return cache }
	t.Cleanup(func() { loopholes.BinaryCacheDir = prev })

	spec := jsonx.NewOrderedMap()
	spec.Set("enabled", true)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("tool", spec)
	module := filepath.Join(root, filepath.FromSlash(filepath.Dir(fixtureManifestPath)))
	set := loopholes.NewSet(loopholes.DiscoverOptions{LoopholesConfig: cfg,
		PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}}})
	lp, ok := set.Lookup("tool")
	if !ok {
		t.Fatalf("the fixture loophole was not discovered from %s", module)
	}
	if !lp.Active() {
		reason, _ := lp.InactiveReason()
		t.Fatalf("a seeded tree's loophole is not active: %s", reason)
	}
	hostBuild := packbin.Path(cache, pinned[machineHost], "toold")
	if lp.HostDaemon == nil || len(lp.HostDaemon.Cmd) == 0 || lp.HostDaemon.Cmd[0] != hostBuild {
		t.Fatalf("the host daemon runs %v, want the seeded build %s", lp.HostDaemon, hostBuild)
	}
	out, err := exec.Command(hostBuild).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "toold from this very tree") {
		t.Errorf("the host argv's program is not the tree's: %q, %v", out, err)
	}

	args := set.RuntimeArgsFor(set.Enabled(), "podman")
	mount := packbin.Path(cache, pinned[machineJail], "toold") + ":" +
		loopholedecl.JailBinaryPath("tool", "toold") + ":ro"
	found := false
	for i := 0; i+1 < len(args); i++ {
		found = found || (args[i] == "-v" && args[i+1] == mount)
	}
	if !found {
		t.Errorf("the launch does not bind the seeded jail build (-v %s):\n%q", mount, args)
	}
}
