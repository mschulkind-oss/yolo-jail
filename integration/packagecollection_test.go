package integration

import (
	"context"
	"fmt"
	"os/exec"
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

// The guard is an identity function on a real package, so every valid spelling
// of a `packages` entry must still resolve. Without this, "refuse collections"
// could be satisfied by refusing everything.
func TestPackagesEntryNamingAPackageStillResolves(t *testing.T) {
	requireJail(t)
	requireNix(t)

	cases := []struct {
		name string
		spec string
	}{
		// libX11 is the top-level attribute a user reaching for "xorg" wants.
		{"plain name", `["libX11"]`},
		// The dotted form still means an OUTPUT, not a collection member.
		{"output shorthand", `["gtk4.dev"]`},
		{"object form with outputs", `[{"name":"gtk4","outputs":["out","dev"]}]`},
		{"no packages at all", `[]`},
	}

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

// nixEvalDrvPath evaluates `.#packages.<system>.<attr>.drvPath` with
// YOLO_EXTRA_PACKAGES set to spec, and returns (stdout, stderr, err).
//
// --impure is load-bearing here, unlike in expectedInstallPrefix: the whole
// point is that the flake reads YOLO_EXTRA_PACKAGES through builtins.getEnv.
// stderr is CAPTURED rather than dropped for the same reason — it carries both
// the guard's abort and the warn-and-skip notice these tests assert on.
func nixEvalDrvPath(t *testing.T, attr, spec string) (string, string, error) {
	t.Helper()
	if repoRoot == "" {
		t.Skip("module root unresolved")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nix",
		"--extra-experimental-features", "nix-command flakes",
		"eval", "--impure", "--raw",
		fmt.Sprintf(".#packages.%s.%s.drvPath", nixSystem(), attr))
	cmd.Dir = repoRoot
	cmd.Env = append(cmd.Environ(), "YOLO_EXTRA_PACKAGES="+spec)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), stderr.String(), err
}

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
