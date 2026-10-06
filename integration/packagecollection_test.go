package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A `packages:` entry that names a nixpkgs attribute SET rather than a package —
// "xorg", "python3Packages", "llvmPackages", "gst_all_1" — has no derivation to
// install, and NOTHING between the config and nix's own string coercion can
// notice. Before flake.nix's requireDerivation guard, the whole report was:
//
//	error: cannot coerce a set to a string:
//	  { appres = «thunk»; bdftopcf = «thunk»; «218 attributes elided» }
//
// raised from pkgs/build-support/docker/default.nix. It names 228 xorg
// attributes and never mentions `packages`, yolo, or the entry the user wrote;
// the same config also aborts the /lib farm and the non-container buildEnv the
// same way. That is the exact "symptom two layers from its cause" shape the
// stale-image refusal exists to prevent, so the guard's message is pinned here.
//
// One case per CONSUMER, deliberately. The image contents and the /lib farm
// share a resolution (resolvedPackageSpecs) but make different output choices
// from it, and the non-container buildEnv resolves separately — with its own
// guard, because the tryEval around availableOn would otherwise swallow the
// throw and relabel a typo'd entry "no <system> build". Measured by deleting
// each call: dropping the guard in resolvedPackageSpecs fails the first two
// subtests, dropping the non-container one fails the third.
//
// Eval-only (`.drvPath`), so nothing builds and no container starts — the cost is
// one nixpkgs eval per case.
func TestPackagesEntryNamingACollectionIsRefusedByName(t *testing.T) {
	requireJail(t)
	requireNix(t)

	// The attribute the message must name, and the flake attrs that reach the
	// guard through a different resolution path.
	paths := []struct {
		name  string
		attr  string
		which string
	}{
		{"image contents", "imageClosureRoot", "extraPackages"},
		{"lib farm", "binPathLinks", "extraLibPackages"},
		{"non-container buildEnv", "yoloNoncontainerPackages", "noncontainerResolved"},
	}

	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			_, stderr, err := nixEvalDrvPath(t, p.attr, `["xorg"]`)
			if err == nil {
				t.Fatalf("eval of %s SUCCEEDED with a package collection in "+
					"`packages` — %s no longer guards its resolution, so a "+
					"collection reaches nix's string coercion again", p.attr, p.which)
			}
			for _, want := range []string{
				// The entry the user wrote, quoted as they wrote it.
				`entry "xorg"`,
				// The diagnosis, in words that say what to do about it.
				"COLLECTION",
				"not a package",
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("eval of %s: message does not mention %q\n--- stderr ---\n%s",
						p.attr, want, stderr)
				}
			}
			// The failure mode being fixed: nix's coercion error must no longer
			// be what the user is left holding.
			if strings.Contains(stderr, "cannot coerce a set to a string") {
				t.Errorf("eval of %s: still reports nix's raw coercion error, so "+
					"the guard did not fire before the set was stringified\n"+
					"--- stderr ---\n%s", p.attr, stderr)
			}
		})
	}
}

// A collection refusal names the fix, and the fix is now to name a member: a `packages`
// entry is a nixpkgs attribute path (docs/design/package-nested-attribute-paths.md, OQ-1).
// The message used to say the opposite — "A collection member is NOT selectable from
// `packages`" — which this design falsified. python3Packages is here because its first 49
// names are removed aliases that throw (measured 2026-10-06): the old fixed window of 40
// names found no member and called a set of 12,184 packages one that "holds no packages".
func TestACollectionRefusalNamesAMemberToWriteInstead(t *testing.T) {
	requireJail(t)
	requireNix(t)

	for _, coll := range []string{"xorg", "rocmPackages", "python3Packages"} {
		t.Run(coll, func(t *testing.T) {
			_, stderr, err := nixEvalDrvPath(t, "imageClosureRoot", `["`+coll+`"]`)
			if err == nil {
				t.Fatalf("eval SUCCEEDED with the collection %q in `packages`", coll)
			}
			for _, want := range []string{
				"Members include: ",
				// A member spelled as an entry, and the nix command it mirrors.
				`so "` + coll + `.`,
				"`nix build nixpkgs#" + coll + ".",
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal does not say %q\n--- stderr ---\n%s", want, stderr)
				}
			}
			for _, stale := range []string{"NOT selectable", "holds no packages"} {
				if strings.Contains(stderr, stale) {
					t.Errorf("the refusal still says %q\n--- stderr ---\n%s", stale, stderr)
				}
			}
		})
	}
}

// THE RULING ITSELF: a `packages` entry installs what `nix build nixpkgs#<entry>` builds
// (OQ-1, ruled (C) 2026-10-05). So the oracle here is Nix, asked through `nix build
// --dry-run` and `lib.getLib` against this flake's own nixpkgs pin, and every case compares
// the image to Nix's answer rather than to a store path written down by hand.
//
// texlivePackages.abc.texsource is the case the ruling decided: `texsource` is one of abc's
// outputs, and abc's `texsource` ATTRIBUTE is a different derivation (measured 2026-10-05:
// abc-2.0b-texsource.drv's `out`, against abc-2.0b.drv's `texsource`). Nix builds the
// attribute. The image contents read the attribute under either reading, so the reading
// shows in the /lib farm: following Nix, the farm links the texsource derivation; reading
// the last name as abc's output (the rejected leaning) makes abc the base and links abc's
// own default output instead. Revert-checked 2026-10-06: with the output test reduced to
// "the last name is in the parent's outputs", this case fails and the rest pass.
//
// The farm half also pins the design's §4.2 trap: an output keeps its PARENT as the base,
// so the farm links `lib.getLib gtk4` and never `getLib gtk4.dev`, which is the dev output
// itself and holds no .so.
func TestPackagesEntryInstallsWhatNixBuildBuilds(t *testing.T) {
	requireJail(t)
	requireNix(t)

	cases := packageBuildFixtures
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, err := json.Marshal([]string{c.entry})
			if err != nil {
				t.Fatal(err)
			}
			image := flakeDrvMentions(t, "imageClosureRoot", string(spec))
			want := nixBuildWouldBuild(t, "nixpkgs#"+c.entry)
			if len(want) == 0 {
				t.Fatalf("`nix build --dry-run nixpkgs#%s` named no output", c.entry)
			}
			for _, p := range want {
				if !image[p] {
					t.Errorf("packages=%s: the image does not hold %s, which `nix build "+
						"nixpkgs#%s` builds", spec, p, c.entry)
				}
			}
			if c.rejected != "" {
				for _, p := range nixBuildWouldBuild(t, "nixpkgs#"+c.rejected) {
					if image[p] {
						t.Errorf("packages=%s: the image holds %s, which is `nixpkgs#%s` — the "+
							"reading OQ-1 ruled against — rather than what Nix builds for %q",
							spec, p, c.rejected, c.entry)
					}
				}
			}
			if c.farmBase == "" {
				return
			}
			farm := flakeDrvMentions(t, "binPathLinks", string(spec))
			if lib := nixGetLib(t, c.farmBase); !farm[lib] {
				t.Errorf("packages=%s: the /lib farm does not link %s, the runtime "+
					"libraries of %s, so its .so files are not dlopen-able by soname",
					spec, lib, c.farmBase)
			}
			if c.farmRejected != "" {
				if lib := nixGetLib(t, c.farmRejected); farm[lib] {
					t.Errorf("packages=%s: the /lib farm links %s, the runtime output of %s: "+
						"the entry was resolved as an OUTPUT of %s, the reading OQ-1 ruled "+
						"against, instead of as the attribute Nix builds",
						spec, lib, c.farmRejected, c.farmRejected)
				}
			}
		})
	}
}

// The guard is an identity function on a real package, so every valid spelling
// of a `packages` entry must still resolve. Without this, "refuse collections"
// could be satisfied by refusing everything.
func TestPackagesEntryNamingAPackageStillResolves(t *testing.T) {
	requireJail(t)
	requireNix(t)

	cases := packageResolveFixtures

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, attr := range []string{"imageClosureRoot", "binPathLinks"} {
				drv, stderr, err := nixEvalDrvPath(t, attr, c.spec)
				if err != nil {
					t.Fatalf("eval of %s with packages=%s failed: %v\n--- stderr ---\n%s",
						attr, c.spec, err, stderr)
				}
				if !strings.HasPrefix(drv, "/nix/store/") || !strings.HasSuffix(drv, ".drv") {
					t.Fatalf("eval of %s with packages=%s returned %q, not a .drv path",
						attr, c.spec, drv)
				}
			}
		})
	}
}

// Shared with the architecture regression: it evaluates the same valid entries
// as the image, library-farm, and Nix build comparison tests. Collection members
// must work on both Linux architectures; ROCm's clr depends on x86_64-only LLVM.
var packageBuildFixtures = []struct {
	name  string
	entry string
	// farmBase is the attribute whose `lib.getLib` the /lib farm must link; "" skips it.
	farmBase string
	// farmRejected is the base of the reading OQ-1 ruled against; the farm must not
	// link its `lib.getLib`.
	farmRejected string
	// rejected is that reading's installable, as Nix spells it; the image must not
	// hold what it builds.
	rejected string
}{
	{name: "an output of a top-level package", entry: "gtk4.dev", farmBase: "gtk4"},
	{name: "a collection member", entry: "gst_all_1.gstreamer", farmBase: "gst_all_1.gstreamer"},
	{name: "an output of a collection member", entry: "gst_all_1.gstreamer.dev",
		farmBase: "gst_all_1.gstreamer"},
	{name: "a member whose name is also one of its parent's outputs",
		entry:    "texlivePackages.abc.texsource",
		farmBase: "texlivePackages.abc.texsource", farmRejected: "texlivePackages.abc",
		rejected: "texlivePackages.abc^texsource"},
	{name: "a quoted name", entry: `nerd-fonts."m+"`, farmBase: `nerd-fonts."m+"`},
}

var packageResolveFixtures = []struct {
	name string
	spec string
}{
	// libX11 is the top-level attribute a user reaching for "xorg" wants.
	{"plain name", `["libX11"]`},
	// A dotted entry is an attribute path: an output, a member, a member's output.
	{"an output", `["gtk4.dev"]`},
	{"a collection member", `["gst_all_1.gstreamer"]`},
	{"a collection member's output", `["gst_all_1.gstreamer.dev"]`},
	{"object form with outputs", `[{"name":"gtk4","outputs":["out","dev"]}]`},
	// The spelling the macos-user refusal tells a user to write, for a member.
	{"object form naming a member", `[{"name":"gst_all_1.gstreamer","platforms":["linux"]}]`},
	{"no packages at all", `[]`},
}

// Fixture availability is an assertion, not a reason to skip an architecture.
// ROCm's clr worked locally on x86_64 but made the ARM64 resolution tests fail.
func TestPackageResolutionFixturesSupportBothLinuxArchitectures(t *testing.T) {
	requireJail(t)
	requireNix(t)
	cases := append([]struct{ name, spec string }{}, packageResolveFixtures...)
	for _, c := range packageBuildFixtures {
		spec, err := json.Marshal([]string{c.entry})
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct{ name, spec string }{"build oracle: " + c.name, string(spec)})
	}
	for _, system := range []string{"x86_64-linux", "aarch64-linux"} {
		for _, c := range cases {
			t.Run(system+"/"+c.name, func(t *testing.T) {
				_, stderr, err := nixEvalDrvPathForSystem(t, system, "imageClosureRoot", c.spec, "NIXPKGS_ALLOW_UNSUPPORTED_SYSTEM=")
				if err != nil {
					t.Fatalf("fixture packages=%s does not resolve on %s: %v\n%s", c.spec, system, err, stderr)
				}
			})
		}
	}
}

// An unknown package name is NOT a collection, and the non-container path's
// warn-and-skip is what makes a package with no build for this system survivable
// there. The guard sits next to that decision (inside the same resolution) and
// must not have turned a skip into an abort.
func TestUnknownPackageStillWarnsAndSkips(t *testing.T) {
	requireJail(t)
	requireNix(t)

	drv, stderr, err := nixEvalDrvPath(t, "yoloNoncontainerPackages",
		`["yolo-no-such-package","libX11"]`)
	if err != nil {
		t.Fatalf("eval failed; an unknown name must be skipped, not fatal: %v\n"+
			"--- stderr ---\n%s", err, stderr)
	}
	if !strings.HasPrefix(drv, "/nix/store/") {
		t.Fatalf("eval returned %q, not a store path", drv)
	}
	if !strings.Contains(stderr, "yolo-no-such-package") {
		t.Errorf("the skip was silent — no warning naming the dropped package\n"+
			"--- stderr ---\n%s", stderr)
	}
}

// An entry that names nothing is refused naming the step that failed, on the image path,
// and SKIPPED with the same reason on the non-container path, whose warn-and-skip (fatal on
// macos-user, host-side) is what it was for a top-level typo. The dotted path is the new
// way to get here: an output typo lands on a derivation, so its outputs are listed.
func TestPackagesEntryNamingNothingSaysWhichNameIsMissing(t *testing.T) {
	requireJail(t)
	requireNix(t)

	_, stderr, err := nixEvalDrvPath(t, "imageClosureRoot", `["gtk4.nosuch"]`)
	if err == nil {
		t.Fatal(`the image eval SUCCEEDED with "gtk4.nosuch" in packages`)
	}
	for _, want := range []string{
		`entry "gtk4.nosuch"`,
		`nixpkgs.gtk4 has no attribute "nosuch"`,
		"its outputs are out, dev",
		"nix search nixpkgs",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("image refusal does not say %q\n--- stderr ---\n%s", want, stderr)
		}
	}

	drv, stderr, err := nixEvalDrvPath(t, "yoloNoncontainerPackages",
		`["rocmPackages.yolo-no-such-member","libX11"]`)
	if err != nil {
		t.Fatalf("the non-container eval failed; a missing member must be skipped like a "+
			"missing package: %v\n--- stderr ---\n%s", err, stderr)
	}
	if !strings.HasPrefix(drv, "/nix/store/") {
		t.Fatalf("eval returned %q, not a store path", drv)
	}
	if want := `nixpkgs.rocmPackages has no attribute "yolo-no-such-member"`; !strings.Contains(stderr, want) {
		t.Errorf("the skip does not say %q\n--- stderr ---\n%s", want, stderr)
	}
}

// A skipped entry is named as the user wrote it, output and all, because the macos-user
// refusal that follows tells the user to write {"name": "<pkg>", "platforms": ["linux"]},
// and the base path ("cudaPackages.libcublas") filled in there would mark a different entry.
// An unfree member is skipped on every system, which makes the case reachable on Linux;
// NIXPKGS_ALLOW_UNFREE is cleared so an opted-in machine does not install it instead.
func TestASkippedDottedEntryIsNamedAsWritten(t *testing.T) {
	requireJail(t)
	requireNix(t)

	_, stderr, err := nixEvalDrvPath(t, "yoloNoncontainerPackages",
		`["cudaPackages.libcublas.dev","libX11"]`, "NIXPKGS_ALLOW_UNFREE=")
	if err != nil {
		t.Fatalf("the non-container eval failed; an unfree member must be skipped: %v\n"+
			"--- stderr ---\n%s", err, stderr)
	}
	if want := `skipping "cudaPackages.libcublas.dev"`; !strings.Contains(stderr, want) {
		t.Errorf("the skip does not name the entry as written (%s)\n--- stderr ---\n%s", want, stderr)
	}
}

// An object whose `name` already selects an output AND lists `outputs` selects twice; it is
// refused on both resolution paths, naming the one spelling that says it once.
func TestAnObjectSelectingAnOutputTwiceIsRefused(t *testing.T) {
	requireJail(t)
	requireNix(t)

	for _, attr := range []string{"imageClosureRoot", "yoloNoncontainerPackages"} {
		t.Run(attr, func(t *testing.T) {
			_, stderr, err := nixEvalDrvPath(t, attr, `[{"name":"gtk4.dev","outputs":["out"]}]`)
			if err == nil {
				t.Fatal(`eval SUCCEEDED with {"name":"gtk4.dev","outputs":["out"]}`)
			}
			for _, want := range []string{"selects an output twice", `{"name": "gtk4", "outputs": ["dev","out"]}`} {
				if !strings.Contains(stderr, want) {
					t.Errorf("refusal does not say %q\n--- stderr ---\n%s", want, stderr)
				}
			}
		})
	}
}

// flakeDrvMentions returns the set of store paths `.#packages.<system>.<attr>`'s derivation
// names (its environment carries the image's package list and the /lib farm's loop) with
// YOLO_EXTRA_PACKAGES set to spec.
func flakeDrvMentions(t *testing.T, attr, spec string) map[string]bool {
	t.Helper()
	drv, stderr, err := nixEvalDrvPath(t, attr, spec)
	if err != nil {
		t.Fatalf("eval of %s with packages=%s failed: %v\n--- stderr ---\n%s", attr, spec, err, stderr)
	}
	out, err := exec.Command("nix", "--extra-experimental-features", "nix-command",
		"derivation", "show", drv).Output()
	if err != nil {
		t.Fatalf("nix derivation show %s: %v", drv, err)
	}
	set := map[string]bool{}
	for _, p := range storePathRe.FindAllString(string(out), -1) {
		set[p] = true
	}
	return set
}

var storePathRe = regexp.MustCompile(`/nix/store/[0-9a-z]{32}-[A-Za-z0-9+._?=-]+`)

// nixBuildWouldBuild asks Nix which store paths `nix build <installable>` builds, against
// THIS flake's nixpkgs pin (--inputs-from), without building or substituting anything.
func nixBuildWouldBuild(t *testing.T, installable string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), nixEvalTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nix", "--extra-experimental-features", "nix-command flakes",
		"build", "--dry-run", "--json", "--no-link", "--option", "substitute", "false",
		"--inputs-from", repoRoot, installable)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("nix build --dry-run %s: %v\n--- stderr ---\n%s", installable, err, stderr.String())
	}
	var built []struct {
		Outputs map[string]string `json:"outputs"`
	}
	if err := json.Unmarshal(out, &built); err != nil {
		t.Fatalf("nix build --dry-run %s: unreadable JSON %q: %v", installable, out, err)
	}
	var paths []string
	for _, b := range built {
		for _, p := range b.Outputs {
			paths = append(paths, p)
		}
	}
	return paths
}

// nixGetLib returns the store path of `lib.getLib <attrPath>` in this flake's nixpkgs pin:
// the runtime-library output the /lib farm links for a package.
func nixGetLib(t *testing.T, attrPath string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), nixEvalTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nix", "--extra-experimental-features", "nix-command flakes",
		"eval", "--raw", "--inputs-from", repoRoot,
		"nixpkgs#legacyPackages."+nixSystem(),
		"--apply", "p: (p.lib.getLib p."+attrPath+").outPath")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("lib.getLib %s: %v\n--- stderr ---\n%s", attrPath, err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// nixEvalDrvPath evaluates `.#packages.<system>.<attr>.drvPath` with
// YOLO_EXTRA_PACKAGES set to spec and env appended, and returns (stdout, stderr, err).
//
// --impure is load-bearing here, unlike in expectedInstallPrefix: the whole
// point is that the flake reads YOLO_EXTRA_PACKAGES through builtins.getEnv.
// stderr is CAPTURED rather than dropped for the same reason — it carries both
// the guard's abort and the warn-and-skip notice these tests assert on.
func nixEvalDrvPath(t *testing.T, attr, spec string, env ...string) (string, string, error) {
	t.Helper()
	return nixEvalDrvPathForSystem(t, nixSystem(), attr, spec, env...)
}

func nixEvalDrvPathForSystem(t *testing.T, system, attr, spec string, env ...string) (string, string, error) {
	t.Helper()
	if repoRoot == "" {
		t.Skip("module root unresolved")
	}
	ctx, cancel := context.WithTimeout(context.Background(), nixEvalTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nix",
		"--extra-experimental-features", "nix-command flakes",
		"eval", "--impure", "--raw",
		fmt.Sprintf(".#packages.%s.%s.drvPath", system, attr))
	cmd.Dir = repoRoot
	cmd.Env = append(append(cmd.Environ(), "YOLO_EXTRA_PACKAGES="+spec), env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// A deadline that ends the eval says what nix was waiting on (hangreport_test.go): on
	// 2026-10-03 four of these were killed with an EMPTY stderr, which named nothing.
	hang := armHangReport(cmd)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return strings.TrimSpace(string(out)), stderr.String() + "\n(nix eval timed out)\n" + hang.String(), err
	}
	return strings.TrimSpace(string(out)), stderr.String(), err
}

// nixEvalTimeout bounds one nixEvalDrvPath. A variable so TestANixEvalThatOverrunsSaysWhatItWaitedOn
// can run one out in a second.
var nixEvalTimeout = 4 * time.Minute

// nixSystem is this machine's nix system double. The flake's outputs are
// per-system (flake-utils eachSystem), and hardcoding x86_64-linux would make
// every case above skip-or-fail on the maintainer's arm64 Mac.
func nixSystem() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	return arch + "-" + runtime.GOOS
}

func requireNix(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("nix is not on PATH")
	}
}
