package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// `yolo programs` AND `programs.autoprune` ON macos-user, asked of a real sandbox.
//
// The boot catalog runs on this backend now that everything its one line points at works there
// (docs/plans/notch-convergence.md, NC-D26): the bootstrap keeps a boot log, the session names
// the staged pack tree and the workspace (YOLO_PACK_ROOT, YOLO_DARWIN_WORKSPACE), and the launch
// relays the user's `programs.autoprune`. The unit tests pin each half on Linux
// (internal/macosuser/packroot_test.go, internal/cli/programs_test.go,
// internal/entrypoint/catalog_test.go); these ask the sandbox.
//
// The orphan is planted FROM INSIDE THE SANDBOX, under ~/.npm-global, which the home layout
// links into <workspace>/.yolo/home: the exact place a dropped npm program's bytes stay.

// macosUserOrphan is the planted package. No pack or preset declares it.
const macosUserOrphan = "yolo-it-orphan"

// macosUserPlantOrphan is the shell that plants it.
const macosUserPlantOrphan = `mkdir -p ~/.npm-global/lib/node_modules/` + macosUserOrphan +
	` && echo '{}' > ~/.npm-global/lib/node_modules/` + macosUserOrphan + `/package.json`

// `yolo programs ls` inside the sandbox sees the staged packs (it does not decline with "No
// staged packs here"), lists the orphan, and exits 0, a report rather than a gate.
func TestMacosUserProgramsLsSeesTheStagedPacks(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["guardrails"]}`)
	ws := macosUserWorkspace(t, `{}`)
	r := macosUserRunProbe(t, "programs ls", ws, strings.Join([]string{
		macosUserPlantOrphan,
		`echo "=== LS ==="`,
		`yolo programs ls; echo "RC=$?"`,
		`echo "=== END ==="`,
	}, "\n"))
	ls := section(r.stdout, "=== LS ===", "=== END ===")
	if strings.Contains(ls, "No staged packs here") {
		t.Fatalf("`yolo programs ls` in the sandbox found no staged pack tree, so the session "+
			"carries no YOLO_PACK_ROOT (macosuser.BuildRunPlanWithDaemons):\n%s", ls)
	}
	if !strings.Contains(ls, macosUserOrphan) || !strings.Contains(ls, "RC=0") {
		t.Errorf("`yolo programs ls` did not list the planted orphan and exit 0:\n%s", ls)
	}
}

// With `programs.autoprune` on in the user's config, the NEXT launch's bootstrap removes what
// the first planted, says so, and the sandbox finds it gone. Two launches, because the catalog
// observes the previous launch's state.
func TestMacosUserAutopruneRemovesAnOrphan(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["guardrails"], "programs": {"autoprune": true}}`)
	ws := macosUserWorkspace(t, `{}`)
	macosUserRunProbe(t, "autoprune plant", ws, macosUserPlantOrphan+"\necho \"=== END ===\"")

	r := macosUserRunProbe(t, "autoprune", ws, strings.Join([]string{
		`echo "=== PRESENT ==="`,
		`test -e ~/.npm-global/lib/node_modules/` + macosUserOrphan + ` && echo PRESENT || echo GONE`,
		`echo "=== END ==="`,
	}, "\n"))
	if !strings.Contains(r.combined(), "boot autoprune: removing "+macosUserOrphan) {
		t.Errorf("the second launch did not announce removing the orphan:\n%s", r.combined())
	}
	if got := section(r.stdout, "=== PRESENT ===", "=== END ==="); !strings.Contains(got, "GONE") {
		t.Errorf("the orphan survived a launch with programs.autoprune on:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// PACK-TREE PROGRAMS AND DEV OVERRIDES ON macos-user (docs/design/pack-buildable-programs.md §11's
// six done steps; pack-buildable-programs-plan.md Task 10), in BOTH modes: the second account
// (`macos_user_mode` "account") and run-as-you ("self"). The build is the darwin sealed act
// (forked-programs-as-packs.md FP-D24) running the placement script against the act's own checkout;
// a dev override's checkout is built by the same act in place of the pack's tree.
//
// HERMETIC: the pack is a `file://` directory whose tools/x holds a VERSION file and whose build line
// writes a /bin/sh script printing it, with macOS's own shell and tools; the override is a checkout
// in a temporary directory, holding its own VERSION. No agent starts.
//
// ⚠ Mac-only, behind requireMacosUser. Each test removes the capture entries it added.
// ---------------------------------------------------------------------------

const (
	macosUserPackTreePack = "mupacktree"
	macosUserPackTreeBin  = "mupacktree-x"
)

// macosUserPackTreeModes are the two values of `macos_user_mode` every test below runs under.
var macosUserPackTreeModes = []string{"account", "self"}

// macosUserPackTreeBuild is the fixture's build: a script naming the VERSION it was built from.
const macosUserPackTreeBuild = `printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`

// macosUserPackTreeFixture writes the pack (tools/x/VERSION = 1, build writing x, run x), selects it
// in an isolated user config under mode (with extra user-scope keys, each ending in a comma), and
// returns a function setting VERSION, the workspace, and the store and its entries before the test,
// whose new entries the test's cleanup removes.
func macosUserPackTreeFixture(t *testing.T, mode, build, extra string) (func(string), string, string, map[string]bool) {
	t.Helper()
	pack := resolvedTempDir(t)
	src := filepath.Join(pack, "tools", "x")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	setVersion := func(v string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src, "VERSION"), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setVersion("1")
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"`+macosUserPackTreePack+
		`","contributes":[{"kind":"program","bin":"`+macosUserPackTreeBin+`","via":"pack","from":"tools/x",`+
		`"build":"`+buildJSON+`","run":"x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{`+extra+`"macos_user_mode": "`+mode+`", "packs": [{"source": "file://`+pack+`", "name": "`+
		macosUserPackTreePack+`"}]}`)
	store := paths.CapturesDirUnder(os.Getenv("HOME"))
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before, macosUserPackTreeBin) })
	return setVersion, macosUserWorkspace(t, `{}`), store, before
}

// Done steps 1 to 3: a launch builds the program in the darwin act and runs it, an edit rebuilds it
// once, and an unchanged tree builds nothing and says nothing.
func TestMacosUserPackTreeProgramBuildsOnceAndRebuildsOnChange(t *testing.T) {
	for _, mode := range macosUserPackTreeModes {
		t.Run(mode, func(t *testing.T) {
			requireMacosUser(t)
			setVersion, ws, store, before := macosUserPackTreeFixture(t, mode, macosUserPackTreeBuild, "")
			built := "built program " + macosUserPackTreePack + "/" + macosUserPackTreeBin
			launch := func(what string) string {
				t.Helper()
				r := runMacosUserAs(t, mode, ws, macosUserPackTreeBin, withHostSemantics())
				t.Logf("%s (%s):\n%s\nnew store entries: %v", what, mode, r.combined(),
					newCaptureEntries(t, store, before, macosUserPackTreeBin))
				if r.rc != 0 {
					t.Fatalf("%s: rc %d", what, r.rc)
				}
				return r.combined()
			}
			if out := launch("the first launch"); !strings.Contains(out, "x-1") || strings.Count(out, built) != 1 {
				t.Fatalf("the first launch did not build once in the darwin act and run x-1 (want one %q)", built)
			}
			if added := newCaptureEntries(t, store, before, macosUserPackTreeBin); len(added) != 1 {
				t.Errorf("the first launch added %d entries, want 1: %v", len(added), added)
			}
			setVersion("2")
			if out := launch("the launch after the edit"); !strings.Contains(out, "x-2") || strings.Count(out, built) != 1 {
				t.Fatalf("the launch after the edit did not rebuild once and serve x-2")
			}
			if out := launch("the launch with no change"); !strings.Contains(out, "x-2") || strings.Contains(out, built) {
				t.Errorf("the launch with no change rebuilt, or did not serve x-2")
			}
			if added := newCaptureEntries(t, store, before, macosUserPackTreeBin); len(added) != 2 {
				t.Errorf("there are %d new entries after three launches, want 2: %v", len(added), added)
			}
		})
	}
}

// Done step 4 (OQ-BG3's ruling): `programs.overrides` points the program at a checkout; the launch
// builds it from the checkout in the darwin act and names it, an unchanged relaunch reuses that build,
// and an edit there is rebuilt at the next launch.
func TestMacosUserPackTreeProgramOverrideBuildsTheCheckout(t *testing.T) {
	for _, mode := range macosUserPackTreeModes {
		t.Run(mode, func(t *testing.T) {
			requireMacosUser(t)
			dev := resolvedTempDir(t)
			setDev := func(v string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dev, "VERSION"), []byte(v+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, ws, _, _ := macosUserPackTreeFixture(t, mode, macosUserPackTreeBuild,
				`"programs": {"overrides": {"`+macosUserPackTreeBin+`": {"path": "`+dev+`"}}}, `)
			named := macosUserPackTreeBin + ": built from your checkout " + dev
			built := "built program " + macosUserPackTreePack + "/" + macosUserPackTreeBin + " (built from your checkout "
			for _, step := range []struct{ version, want, how string }{
				{"dev1", "x-dev1", " — rebuilt at tree digest "},
				{"", "x-dev1", " — reused its build at tree digest "},
				{"dev2", "x-dev2", " — rebuilt at tree digest "},
			} {
				if step.version != "" {
					setDev(step.version)
				}
				want := step.want
				r := runMacosUserAs(t, mode, ws, macosUserPackTreeBin, withHostSemantics())
				out := r.combined()
				t.Logf("%s, %q:\n%s", mode, step.version, out)
				if r.rc != 0 || !strings.Contains(out, want) {
					t.Fatalf("rc %d, want the checkout's %s", r.rc, want)
				}
				if !strings.Contains(out, named) || !strings.Contains(out, step.how) {
					t.Errorf("the launch does not name the checkout build (%q ...%q)", named, step.how)
				}
				rebuilt := strings.Count(out, built)
				if (step.version != "" && rebuilt != 1) || (step.version == "" && rebuilt != 0) {
					t.Errorf("the launch built the checkout %d times", rebuilt)
				}
			}
		})
	}
}

// Done step 6: a build that fails refuses that program alone, naming its log, the retry and the
// hatch; with YOLO_ALLOW_MISSING_PROGRAMS=1 the sandbox starts. The readiness act is on.
func TestMacosUserPackTreeProgramBuildFailureRefusesOnlyIt(t *testing.T) {
	for _, mode := range macosUserPackTreeModes {
		t.Run(mode, func(t *testing.T) {
			requireMacosUser(t)
			_, ws, _, _ := macosUserPackTreeFixture(t, mode, "echo building; exit 3", "")
			t.Setenv(paths.AllowMissingProgramsEnv, "")
			r := runMacosUserAs(t, mode, ws, "echo SESSION-RAN", withHostSemantics(), withReadiness())
			out := r.combined()
			t.Logf("%s, the broken build:\n%s", mode, out)
			if r.rc == 0 || strings.Contains(out, "SESSION-RAN") {
				t.Fatalf("rc %d: the launch did not stop for the broken build", r.rc)
			}
			for what, want := range map[string]string{
				"the program":   macosUserPackTreeBin,
				"the build log": filepath.Join(".yolo", "build-"),
				"the retry":     "yolo pack build " + macosUserPackTreePack,
				"the hatch":     paths.AllowMissingProgramsEnv + "=1",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal does not name %s (%q)", what, want)
				}
			}
			if strings.Contains(out, "run it on a container backend") {
				t.Errorf("the refusal sends a pack-tree build's failure to a container backend")
			}
			r = runMacosUserAs(t, mode, ws, "echo SESSION-RAN", withHostSemantics(), withReadiness(),
				withEnv(paths.AllowMissingProgramsEnv+"=1"))
			if r.rc != 0 || !strings.Contains(r.combined(), "SESSION-RAN") {
				t.Fatalf("rc %d: the hatch did not start the sandbox\n%s", r.rc, r.combined())
			}
		})
	}
}

// macosUserPrebuiltURLEnv and macosUserPrebuiltSHAEnv name a real https artifact for done step 5 on a
// Mac: the darwin root pool ignores SSL_CERT_FILE, so the container cell's in-test TLS server cannot be
// trusted here. The artifact is a program for darwin/<arch>, a /bin/sh script included.
const (
	macosUserPrebuiltURLEnv = "YOLO_TEST_DARWIN_PREBUILT_URL"
	macosUserPrebuiltSHAEnv = "YOLO_TEST_DARWIN_PREBUILT_SHA256"
)

// Done step 5: a pack with no tools/x and a darwin `prebuilt` runs the verified artifact, and the
// launch names its sha256. Needs a real https artifact (macosUserPrebuiltURLEnv).
func TestMacosUserPackTreeProgramUsesThePrebuiltWithoutSources(t *testing.T) {
	for _, mode := range macosUserPackTreeModes {
		t.Run(mode, func(t *testing.T) {
			requireMacosUser(t)
			url, sum := os.Getenv(macosUserPrebuiltURLEnv), os.Getenv(macosUserPrebuiltSHAEnv)
			if url == "" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(sum) {
				t.Skipf("set %s to an https artifact for darwin/%s and %s to its sha256 to run done step 5 here",
					macosUserPrebuiltURLEnv, runtime.GOARCH, macosUserPrebuiltSHAEnv)
			}
			pack := resolvedTempDir(t) // no tools/x
			if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"`+macosUserPackTreePack+
				`","contributes":[{"kind":"program","bin":"`+macosUserPackTreeBin+`","via":"pack","from":"tools/x",`+
				`"build":"make","run":"x","prebuilt":{"darwin/`+runtime.GOARCH+`":{"url":"`+url+`","sha256":"`+sum+`"}}}]}`),
				0o644); err != nil {
				t.Fatal(err)
			}
			packHome(t, `{"macos_user_mode": "`+mode+`", "packs": [{"source": "file://`+pack+`", "name": "`+
				macosUserPackTreePack+`"}]}`)
			store := paths.CapturesDirUnder(os.Getenv("HOME"))
			before := captureEntryNames(t, store)
			t.Cleanup(func() { removeNewCaptureEntries(t, store, before, macosUserPackTreeBin) })
			ws := macosUserWorkspace(t, `{}`)
			r := runMacosUserAs(t, mode, ws, macosUserPackTreeBin, withHostSemantics(), withReadiness())
			out := r.combined()
			t.Logf("%s:\n%s", mode, out)
			line := macosUserPackTreeBin + ": prebuilt artifact from pack " + macosUserPackTreePack +
				" (no tools/x in the pack) — sha256 " + sum[:12]
			if r.rc != 0 || !strings.Contains(out, line) {
				t.Errorf("rc %d: the launch did not run the prebuilt and name its sha256 (%q)", r.rc, line)
			}
		})
	}
}
