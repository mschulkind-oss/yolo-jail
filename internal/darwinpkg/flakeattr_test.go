package darwinpkg

// flakeattr_test.go is the DRIFT GATE between this package's two flake-attribute
// constants and flake.nix itself.
//
// Why it has to exist: ProfileAttr and UnavailableAttr are plain Go strings, and nix
// resolves a missing flake attribute at RUN time — so renaming an attr on one side and
// not the other compiles, passes every unit test, and fails only on a real Mac with an
// opaque "does not provide attribute" from nix. The N2 rename touched both sides at once,
// which is exactly the moment to nail the coupling down.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFlakeExposesTheAttrsThisPackageRealizes(t *testing.T) {
	// Repo root is two dirs up from this package (internal/darwinpkg → internal → repo).
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "flake.nix"))
	if err != nil {
		t.Skipf("cannot read flake.nix (%v) — skipping cross-check", err)
	}
	flake := string(data)

	// The buildEnv profile is exposed under the per-system `packages` set, so the
	// assertion is on the `packages.<attr> =` binding rather than a bare mention — a
	// comment naming the attr must not satisfy this.
	if !strings.Contains(flake, "packages."+ProfileAttr+" = ") {
		t.Errorf("flake.nix has no `packages.%s = ` binding — darwinpkg realizes "+
			"`.#packages.<system>.%s` and nix would fail at run time with a missing "+
			"attribute", ProfileAttr, ProfileAttr)
	}
	// The skip list is a top-level per-system attr (not under `packages`).
	if !strings.Contains(flake, UnavailableAttr+" = ") {
		t.Errorf("flake.nix has no `%s = ` binding — darwinpkg evals "+
			"`.#%s.<system>`", UnavailableAttr, UnavailableAttr)
	}

	// And the OLD darwin-shaped names must be GONE from the CODE, not merely unused:
	// leaving `yoloDarwinPackages` behind as an alias would let a stale caller keep
	// resolving a second copy of the same buildEnv under a name whose whole problem was
	// that it claimed a platform the mechanism does not have.
	//
	// COMMENTS are exempt, and deliberately: the rename's rationale ("it was called
	// yoloDarwinPackages because macos-user was the only caller") is worth keeping where
	// the attr is defined, and a gate that forbids naming the thing you renamed makes the
	// history unwritable. Stripping comments first keeps the assertion on the binding.
	for _, dead := range []string{"yoloDarwinPackages", "darwinUnavailablePackages"} {
		if strings.Contains(stripNixComments(flake), dead) {
			t.Errorf("flake.nix code still references %q — the mechanism is per-system, so "+
				"a darwin-named attr is a name that lies (N2)", dead)
		}
	}
}

// stripNixComments drops each line's `#`-to-end-of-line comment. Crude on purpose: it
// would also cut a `#` inside a nix string literal, which is fine for a test that only ever
// searches the result for identifier names.
func stripNixComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
