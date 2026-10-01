package integration

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
)

// A2'S TWIN: A DECLARED PACKAGE WITH NO DARWIN BUILD ABORTS THE LAUNCH, NAMING IT
// (docs/plans/macos-revival-and-distribution-plan.md, A2).
//
// THE GAP. A2 piece 1 shipped on 2026-09-04: a `packages:` entry the darwin flake eval skips is
// FATAL, raised host-side after the eval (internal/macosuser/orchestrator.go, the "These packages
// have no <system> build" refusal), and piece 2 lets an entry say `platforms: ["linux"]` to be
// expected-absent instead. Both are unit-tested on Linux. The hard error has never fired on a
// Mac: every hardware session declared packages that DO build, so the eval's skip list, the
// materializer's reading of it and the refusal have never met.
//
// THE PACKAGE is darwinpkg.FloorExcludedUnbuildable's entry, iptables: the one name of the
// image core that a 2026-09-12 `nix eval` of meta.platforms found unavailable on BOTH darwin
// systems this flake locks (internal/darwinpkg/floor.go). It is a real package with no darwin
// build, which is the case A2 is about, rather than a typo, which the same refusal also catches
// and the unit tests already drive.
//
// TWO LAUNCHES, the plan's RED-then-GREEN pair: declared plainly, the launch must refuse before
// any sandbox starts and name the package; declared `platforms: ["linux"]`, the same workspace
// must launch. The second is what keeps the first honest: a launch that refused EVERY config
// naming iptables, for any reason, would pass the first alone. Both reuse the floor the other
// launch tests built, since the iptables entry adds no derivation to the profile.
func TestMacosUserAPackageWithNoDarwinBuildAbortsTheLaunch(t *testing.T) {
	requireMacosUser(t)
	if len(darwinpkg.FloorExcludedUnbuildable) == 0 {
		t.Fatal("darwinpkg.FloorExcludedUnbuildable is empty, so this test has no package " +
			"known to have no darwin build. Pick another one by reading meta.platforms with " +
			"`nix eval` against the darwin nixpkgs this flake locks; do not guess")
	}
	pkg := darwinpkg.FloorExcludedUnbuildable[0]
	const marker = "=== SANDBOX STARTED ==="
	probe := `echo "` + marker + `"`

	t.Run("declared", func(t *testing.T) {
		ws := macosUserWorkspace(t, fmt.Sprintf(`{"packages": [%q]}`, pkg))
		r := runMacosUser(t, ws, probe)
		out := r.combined()
		if r.rc == 0 || strings.Contains(r.stdout, marker) {
			t.Fatalf("a workspace declaring %q, which has no darwin build, LAUNCHED (rc %d). A2 "+
				"makes that fatal: the jail would start without a tool the user declared, the "+
				"warn-and-skip behavior A2 retired.\n%s", pkg, r.rc, out)
		}
		line := macosUserUnavailableLine(out)
		if line == "" {
			t.Fatalf("the launch refused (rc %d) but not with A2's message (\"These packages "+
				"have no <system> build: …\", orchestrator.go), so this did not exercise the "+
				"refusal it exists to measure:\n%s", r.rc, out)
		}
		if !strings.Contains(line, pkg) || !strings.Contains(line, " build:") {
			t.Errorf("A2's refusal does not name %q on its own line, so a user with a longer "+
				"list cannot tell which entry to fix:\n%s", pkg, line)
		}
		if !strings.Contains(out, `"platforms": ["linux"]`) {
			t.Errorf("the refusal does not offer the `platforms` escape hatch (A2 piece 2), "+
				"which is the remedy for a package that is genuinely Linux-only:\n%s", out)
		}
	})

	t.Run("linux_only", func(t *testing.T) {
		ws := macosUserWorkspace(t, fmt.Sprintf(`{"packages": [{"name": %q, "platforms": ["linux"]}]}`, pkg))
		r := runMacosUser(t, ws, probe)
		if r.rc != 0 || !strings.Contains(r.stdout, marker) {
			t.Fatalf("a workspace declaring %q as Linux-only did not launch (rc %d). "+
				"EffectivePackages drops an entry whose `platforms` excludes darwin before the "+
				"build, so nix never sees it and nothing can refuse it.\nstdout:\n%s\nstderr:\n%s",
				pkg, r.rc, r.stdout, r.stderr)
		}
		if line := macosUserUnavailableLine(r.combined()); line != "" {
			t.Errorf("the launch started and still printed A2's refusal line:\n%s", line)
		}
	})
}

// macosUserUnavailableLine is the line of out that carries A2's refusal, or "".
func macosUserUnavailableLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "These packages have no ") {
			return l
		}
	}
	return ""
}
