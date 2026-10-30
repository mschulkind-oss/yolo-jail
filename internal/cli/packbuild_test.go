package cli

// packbuild_test.go drives `yolo pack build` (docs/design/pack-buildable-programs.md §8, BG-D11)
// through packMain, the verb's dispatch, over a real user config, the real pack selection and the
// launch's own build act (buildFork) with only the build jail's run pipeline substituted, as
// packtreebuild_test.go substitutes it.

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const packBuildFixtureBuild = `printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`

// packBuildHome is a temp HOME whose user config selects pack fx, a `file://` pack declaring the
// pack-tree program x (from tools/x, run x, built by build), plus a base pack and a fork of its
// program, and extra raw config keys. It returns fx's root.
func packBuildHome(t *testing.T, build, extra string) string {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_RUNTIME", "podman")
	t.Chdir(floortest.ResolvedTemp(t))
	previousProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) { return false, true }
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })
	packs := floortest.ResolvedTemp(t)
	fx := filepath.Join(packs, "fx")
	writeFile(t, filepath.Join(fx, "tools", "x", "VERSION"), "1\n")
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	writeFile(t, filepath.Join(fx, "pack.json"), `{"name":"fx","contributes":[{"kind":"program","bin":"x",`+
		`"via":"pack","from":"tools/x","build":"`+buildJSON+`","run":"x"}]}`)
	writeFile(t, filepath.Join(packs, "basepack", "pack.json"),
		`{"name":"basepack","contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`)
	writeForkManifest(t, filepath.Join(packs, "forkpack", "pack.json"), "git+https://example.invalid/tool.git?ref=main")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{`+extra+`"packs":[`+
		`{"source":"file://`+fx+`","name":"fx"},`+
		`{"source":"file://`+filepath.Join(packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+filepath.Join(packs, "forkpack")+`","name":"forkpack"}]}`)
	return fx
}

// fakePackBuildJail stands in for x's build jail: it leaves what the placement script leaves (the
// program's directory and its link) for this machine's jail platform, or exits rc after writing the
// toolchain record (the build line ran and failed). runs counts it.
func fakePackBuildJail(t *testing.T, runs *int, rc int) func(run.Options) int {
	t.Helper()
	return func(o run.Options) int {
		*runs++
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image-identity-abc\n")
		if rc != 0 {
			return rc
		}
		out := filepath.Join(o.Workspace, captureOutLeaf)
		tree := capture.TreeDir(out)
		dir := filepath.Join(tree, ".local", "lib", "yolo-programs", "x")
		writeFile(t, filepath.Join(dir, "x"), "#!/bin/sh\necho x-1\n")
		if err := os.Chmod(filepath.Join(dir, "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(tree, ".local", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../lib/yolo-programs/x/x", filepath.Join(tree, ".local", "bin", "x")); err != nil {
			t.Fatal(err)
		}
		m, err := capture.ManifestForTree(tree, "/home/agent", captureJailPlatform())
		if err != nil {
			t.Fatal(err)
		}
		if err := capture.WriteManifest(out, m); err != nil {
			t.Fatal(err)
		}
		return 0
	}
}

func packBuildRun(t *testing.T, args ...string) (rc int, out, errw string) {
	t.Helper()
	var o, e bytes.Buffer
	rc = packMain(append([]string{"build"}, args...), &o, &e, false)
	return rc, o.String(), e.String()
}

// THE CALL SITE AND THE CACHE: packMain dispatches `build` to the act, which builds x once; a second
// run finds the store's entry under the lock and says up to date with no jail; --force builds again.
// And the entry is the one a launch's identity selects, so the next launch builds nothing. Red if the
// dispatch is removed (the verb becomes "unknown verb", rc 1) or the act is not the launch's.
func TestPackBuildBuildsOnceThenIsUpToDateAndForceRebuilds(t *testing.T) {
	packBuildHome(t, packBuildFixtureBuild, "")
	runs := 0
	withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 0))

	rc, out, errw := packBuildRun(t)
	if rc != 0 || runs != 1 || !strings.Contains(out, "built") || !strings.Contains(out, "program fx/x") {
		t.Fatalf("first run: rc %d, %d jail runs, want 0 and one build line\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
	}
	if strings.Contains(out, "up to date") {
		t.Errorf("a fresh build said up to date:\n%s", out)
	}
	rc, out, errw = packBuildRun(t, "fx")
	if rc != 0 || runs != 1 || !strings.Contains(out, "up to date") {
		t.Fatalf("second run: rc %d, %d jail runs, want 0, no new run and up to date\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
	}
	rc, out, errw = packBuildRun(t, "--force", "fx")
	if rc != 0 || runs != 2 || !strings.Contains(out, "built") || strings.Contains(out, "up to date") {
		t.Fatalf("--force: rc %d, %d jail runs, want 0 and a rebuild\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
	}

	// The launch's identity for the same staged bytes selects what this verb built.
	sel := selectConfiguredHostPacks()
	f := packload.PackTreePrograms(sel.packs)[0]
	digest, err := f.Digest()
	if err != nil {
		t.Fatal(err)
	}
	b := forkBuild{Fork: f, Commit: digest, Platform: forkBuildPlatform("")}
	store := &capture.Store{Dir: paths.CapturesDir()}
	if _, _, err := resolveForkBuild(store, "x", b.Platform, b.selectSource(), digest, b.recipe()); err != nil {
		t.Errorf("a launch's hit check finds no build of x: %v", err)
	}
}

// A failed build is one failed line naming its log and the retry, and the verb exits 1.
func TestPackBuildReportsAFailedBuildWithItsLogAndRetry(t *testing.T) {
	packBuildHome(t, packBuildFixtureBuild, "")
	runs := 0
	withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 3))
	rc, out, errw := packBuildRun(t, "fx")
	if rc != 1 || runs != 1 {
		t.Fatalf("rc %d, %d jail runs, want 1 and one run\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
	}
	for _, want := range []string{"failed", "program fx/x", "its log: ", "yolo pack build fx"} {
		if !strings.Contains(out, want) {
			t.Errorf("the failed line lacks %q:\n%s", want, out)
		}
	}
}

// A name the config does not select is refused with 2, naming `yolo pack ls`, and nothing builds.
func TestPackBuildRefusesAnUnknownPack(t *testing.T) {
	packBuildHome(t, packBuildFixtureBuild, "")
	runs := 0
	withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 0))
	rc, out, errw := packBuildRun(t, "nope")
	if rc != 2 || runs != 0 || !strings.Contains(errw, "yolo pack ls") || !strings.Contains(errw, `"nope"`) {
		t.Fatalf("rc %d, %d runs, want 2, none, and `yolo pack ls`\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
	}
}

// In a jail (config.InJail) the verb refuses with 2, naming the host command, before reading anything.
func TestPackBuildRefusesInAJail(t *testing.T) {
	packBuildHome(t, packBuildFixtureBuild, "")
	t.Setenv("YOLO_VERSION", "1.2.3")
	runs := 0
	withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 0))
	rc, out, errw := packBuildRun(t, "fx", "--force")
	if rc != 2 || runs != 0 || !strings.Contains(errw, "on the host") || !strings.Contains(errw, "`yolo pack build fx --force`") {
		t.Fatalf("rc %d, %d runs, want 2, none, and the host command\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
	}
}

// A fork in a named pack is skipped with a line naming `yolo capture <bin>`, and builds nothing.
func TestPackBuildNamesYoloCaptureForAFork(t *testing.T) {
	packBuildHome(t, packBuildFixtureBuild, "")
	runs := 0
	withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 0))
	rc, out, errw := packBuildRun(t, "forkpack")
	if rc != 0 || runs != 0 || !strings.Contains(out, "yolo capture tool") {
		t.Fatalf("rc %d, %d runs, want 0, none, and `yolo capture tool`\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
	}
	rc, out, _ = packBuildRun(t, "basepack")
	if rc != 0 || !strings.Contains(out, "nothing to build in pack basepack") {
		t.Errorf("a selected pack with no pack-tree program: rc %d\n%s", rc, out)
	}
}

// A DEV OVERRIDE IS WHAT `yolo pack build` BUILDS (OQ-BG3's ruling): the program is built from the
// checkout, as a launch builds it, and the line names the checkout; a second run is up to date. Red if
// packBuildOne stops passing the overrides to ResolvePackTreeProgram (it then builds the pack's tree).
func TestPackBuildBuildsADevOverridesCheckout(t *testing.T) {
	override := floortest.ResolvedTemp(t)
	writeFile(t, filepath.Join(override, "VERSION"), "dev\n")
	packBuildHome(t, packBuildFixtureBuild, `"programs":{"overrides":{"x":{"path":"`+override+`"}}},`)
	runs := 0
	withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 0))
	rc, out, errw := packBuildRun(t, "fx")
	if rc != 0 || runs != 1 || !strings.Contains(out, "built from your checkout "+override) ||
		!strings.Contains(errw, "your checkout "+override+" at tree digest") {
		t.Fatalf("rc %d, %d runs, want one build of the checkout\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
	}
	if rc, out, _ := packBuildRun(t, "fx"); rc != 0 || runs != 1 || !strings.Contains(out, "up to date") {
		t.Errorf("a second run: rc %d, %d runs, want up to date\n%s", rc, runs, out)
	}
}

// `--at host` on a Mac is the darwin act (FP-D24), which builds a pack's tree (the plan's Task 10):
// the build jail is booted on macos-user and handed the placement script from the act's own
// checkout, for darwin. A bad --at is a usage refusal.
func TestPackBuildAtHostOnAMacBuildsInTheDarwinAct(t *testing.T) {
	packBuildHome(t, packBuildFixtureBuild, "")
	prev := packBuildHostGOOS
	packBuildHostGOOS = "darwin"
	t.Cleanup(func() { packBuildHostGOOS = prev })
	withMac(t, macSetup{account: true})
	runs := 0
	var seen run.Options
	fake := fakePackBuildJail(t, &runs, 0)
	withFakeCaptureJail(t, func(o run.Options) int { seen = o; return fake(o) })
	rc, out, errw := packBuildRun(t, "--at", "host", "fx")
	if rc != 0 || runs != 1 || !strings.Contains(out, "built") || !strings.Contains(out, "darwin/") {
		t.Fatalf("rc %d, %d runs, want one darwin build\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
	}
	if seen.Getenv == nil || seen.Getenv("YOLO_RUNTIME") != "macos-user" {
		t.Errorf("the build jail was not booted on macos-user")
	}
	if rc, _, errw := packBuildRun(t, "--at", "moon"); rc != 2 || !strings.Contains(errw, "jail") {
		t.Errorf("--at moon: rc %d, want a usage refusal\n%s", rc, errw)
	}
}

// OQ-BG2 AS RULED, ON `yolo pack build --at host` ON A MAC WITH NO SANDBOX ACCOUNT: the build
// environment was never set up, so the verb refuses — naming `yolo macos-setup` and, since the pack
// declares a darwin prebuilt, the explicit opt-in — and fetches nothing: a prebuilt is never
// substituted for a present source. With the user's opt-in (`programs.overrides.x.prebuilt`, BG-D18)
// the verified prebuilt is fetched instead, with no jail. Delete packBuild's read of
// config.ProgramPrebuiltOptIns and the second half goes red.
func TestPackBuildOnAMacWithNoSandboxAccountRefusesAndTheOptInTakesThePrebuilt(t *testing.T) {
	body := []byte("#!/bin/sh\necho prebuilt-x\n")
	for _, tc := range []struct {
		name, extra string
	}{{"no opt-in", ""}, {"opted in", `"programs":{"overrides":{"x":{"prebuilt":true}}},`}} {
		t.Run(tc.name, func(t *testing.T) {
			fx := packBuildHome(t, packBuildFixtureBuild, tc.extra)
			url, hits, _ := servePrebuilt(t, body)
			writeFile(t, filepath.Join(fx, "pack.json"), `{"name":"fx","contributes":[{"kind":"program","bin":"x",`+
				`"via":"pack","from":"tools/x","build":"make","run":"x","prebuilt":{"darwin/`+goruntime.GOARCH+
				`":{"url":"`+url+`","sha256":"`+sha256Hex(body)+`"}}}]}`)
			prev := packBuildHostGOOS
			packBuildHostGOOS = "darwin"
			t.Cleanup(func() { packBuildHostGOOS = prev })
			withMac(t, macSetup{})
			runs := 0
			withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 0))
			rc, out, errw := packBuildRun(t, "--at", "host", "fx")
			if tc.extra == "" {
				if rc != 1 || runs != 0 || hits.Load() != 0 {
					t.Fatalf("rc %d, %d jail runs, %d fetches; want 1, none, none\nout:\n%s\nerr:\n%s",
						rc, runs, hits.Load(), out, errw)
				}
				for _, want := range []string{"failed", "`yolo macos-setup`", "`yolo config set programs.overrides.x.prebuilt true --user`", "darwin/" + goruntime.GOARCH} {
					if !strings.Contains(out, want) {
						t.Errorf("the refusal lacks %q:\n%s", want, out)
					}
				}
				return
			}
			if rc != 0 || runs != 0 || hits.Load() != 1 || !strings.Contains(out, "fetched") ||
				!strings.Contains(out, "programs.overrides.x.prebuilt") {
				t.Fatalf("rc %d, %d jail runs, %d fetches; want 0, none, one fetch of the opted-in prebuilt\nout:\n%s\nerr:\n%s",
					rc, runs, hits.Load(), out, errw)
			}
		})
	}
}

// `yolo pack --help` lists the verb.
func TestPackHelpListsBuild(t *testing.T) {
	var out, errw bytes.Buffer
	packMain([]string{"--help"}, &out, &errw, false)
	if !strings.Contains(out.String(), "yolo pack build [<pack>...] [--force] [--at jail|host]") {
		t.Errorf("yolo pack --help does not list build:\n%s", out.String())
	}
}

// BG-D21 ON `yolo pack build`: where the pack declares a prebuilt for this platform, EVERY refusal of
// its build names the opt-in — here a build that ran and failed, which is the same line a missing
// runtime or a build jail that did not start takes. A dev override's checkout names none: its `path`
// cannot be combined with `prebuilt`. Red if packBuildOne stops appending Fork.PrebuiltOptInClause.
func TestPackBuildRefusalNamesThePrebuiltOptInExceptForACheckout(t *testing.T) {
	platform := forkBuildPlatform("")
	for _, checkout := range []bool{false, true} {
		t.Run(map[bool]string{false: "pack tree", true: "checkout"}[checkout], func(t *testing.T) {
			dev := floortest.ResolvedTemp(t)
			writeFile(t, filepath.Join(dev, "VERSION"), "dev\n")
			extra := ""
			if checkout {
				extra = `"programs":{"overrides":{"x":{"path":"` + dev + `"}}},`
			}
			fx := packBuildHome(t, packBuildFixtureBuild, extra)
			writeFile(t, filepath.Join(fx, "pack.json"), `{"name":"fx","contributes":[{"kind":"program","bin":"x",`+
				`"via":"pack","from":"tools/x","build":"make","run":"x","prebuilt":{"`+platform+
				`":{"url":"https://example.invalid/x","sha256":"`+strings.Repeat("c", 64)+`"}}}]}`)
			runs := 0
			withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 3))
			rc, out, errw := packBuildRun(t, "fx")
			if rc != 1 || runs != 1 || !strings.Contains(out, "failed") {
				t.Fatalf("rc %d, %d runs, want 1 and one failed build\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
			}
			named := strings.Contains(out, "`yolo config set programs.overrides.x.prebuilt true --user`")
			if checkout && named {
				t.Errorf("a checkout build's refusal names the prebuilt opt-in:\n%s", out)
			}
			if !checkout && (!named || !strings.Contains(out, platform)) {
				t.Errorf("the refusal does not name the prebuilt opt-in for %s:\n%s", platform, out)
			}
		})
	}
}

// OQ-BG2 FOR A DEV OVERRIDE: a Mac that never set up its build environment refuses a checkout's
// build naming the checkout (SourceSaid) and the setup step, and never the prebuilt opt-in, which
// `path` cannot be combined with; the pack's own tree names both.
func TestTheMacSetupRefusalOfACheckoutNamesTheCheckoutAndNoOptIn(t *testing.T) {
	f := packload.Fork{Pack: "fx", Bin: "x", From: "tools/x", Run: "x",
		Prebuilts: map[string]packdecl.PrebuiltBuild{"darwin/arm64": {URL: "https://example.invalid/x"}}}
	if why := darwinPackTreeUnbuildableReason(f, "darwin/arm64"); !strings.Contains(why, "pack fx's tools/x") ||
		!strings.Contains(why, "`yolo config set programs.overrides.x.prebuilt true --user`") {
		t.Errorf("the pack tree's refusal = %q, want its source and the opt-in", why)
	}
	f.Checkout = "/Users/u/src/x"
	why := darwinPackTreeUnbuildableReason(f, "darwin/arm64")
	if !strings.Contains(why, "your checkout /Users/u/src/x") || !strings.Contains(why, "`yolo macos-setup`") ||
		strings.Contains(why, "prebuilt") || strings.Contains(why, "tools/x") {
		t.Errorf("the checkout's refusal = %q, want the checkout and the setup step, and no opt-in", why)
	}
}
