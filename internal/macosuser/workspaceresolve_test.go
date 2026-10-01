package macosuser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE WORKSPACE MUST REACH EVERY CONSUMER SYMLINK-RESOLVED, and the two tests in this file are
// the halves of one defect that could not be fixed separately.
//
// MEASURED ON HARDWARE 2026-09-13 (macOS 26.5, arm64), running the three Seatbelt probes
// docs/design/declaration-parity.md §6.1's CAUTION asked for:
//
//	profile denies (subpath "/tmp")          → touch /tmp/canary SUCCEEDED (rule never matched)
//	profile denies (subpath "/private/tmp")  → touch /tmp/canary  Operation not permitted
//
// So the kernel canonicalizes the path before the policy is consulted, and an SBPL rule naming
// an UN-RESOLVED path silently matches nothing. `SeatbeltProfile` was handed `workspace` raw
// while `YOLO_HOST_DIR` and `MISE_TRUSTED_CONFIG_PATHS` a few lines away both got
// `resolvePathAbs`, so a workspace reached through a symlink produced a profile whose workspace
// rules were dead. Measured end to end with a real session profile: writing into the workspace
// was `Operation not permitted` under the profile yolo built, and `rc=0` under the same profile
// with the one path resolved.
//
// ⚠ AND IT WAS MASKING A POLICY BYPASS, which is why the fix had to move both call sites at
// once. `HomeContaining` was fed the raw path too, so `/Users/Shared/yolo/link` →
// `/Users/matt/proj` passed the neutral-ground refusal (measured: `✓ all plan invariants hold`).
// Resolving only the profile would have turned a fail-closed bug into a live grant into the
// invoking user's home — the thing that refusal exists to prevent (DP-D15).
func TestBuildRunPlanResolvesTheWorkspaceIntoTheSeatbeltProfile(t *testing.T) {
	// A real symlink on disk, because the whole defect is about what the kernel does with one.
	// The base is resolved where it is MINTED (AGENTS.md's darwin /var/folders rule): on a Mac
	// t.TempDir() is itself under a symlink, and comparing an unresolved fixture path against
	// resolved output is a different bug that passes on Linux.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "realws")
	link := filepath.Join(base, "linkws")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	plan := BuildRunPlan(link, jsonx.NewOrderedMap(), nil, []string{"true"},
		"/opt/yolo-jail/dist-go/darwin-arm64/yolo", "", HomeOverlay{}, HostContext{},
		jsonx.NewOrderedMap(), nil, nil)

	if strings.Contains(plan.Seatbelt, link) {
		t.Errorf("the Seatbelt profile names the SYMLINK %q, which the kernel resolves before "+
			"the policy is consulted — every rule naming it is dead:\n%s", link, plan.Seatbelt)
	}
	if !strings.Contains(plan.Seatbelt, real) {
		t.Errorf("the Seatbelt profile does not name the resolved workspace %q:\n%s",
			real, plan.Seatbelt)
	}
	// The plan's own workspace, and therefore the `cd` the launch argv performs.
	if plan.Workspace != real {
		t.Errorf("plan.Workspace = %q, want the resolved %q", plan.Workspace, real)
	}
	if joined := strings.Join(plan.LaunchArgv, " "); strings.Contains(joined, link) {
		t.Errorf("the launch argv still carries the unresolved workspace:\n%s", joined)
	}
}

// THE HALF THAT MAKES THE ONE ABOVE A SECURITY FIX RATHER THAN A CORRECTNESS ONE. The
// neutral-ground refusal keys on the path it is given, so it is only as good as that path's
// spelling: the LINK spelling of a workspace whose target sits in a user's home reads as
// neutral ground, and the target reads as the violation it is. Measured on hardware 2026-09-13
// through a real `--dry-run`, which accepted `/Users/Shared/yolo/homelink` →
// `/Users/matt/sbprobe-home/proj` with `✓ all plan invariants hold`.
//
// Spelled against an injected users root (a homeLayout) so it runs on the Linux machine that
// develops this repo, where `/Users` does not exist — HomeContaining itself uses macOSHomes.
func TestHomeContainingIsOnlyAsGoodAsThePathsSpelling(t *testing.T) {
	const users = "/FakeUsers"
	layout := homeLayout{usersRoot: users}
	linkSpelling := users + "/Shared/yolo/homelink"
	targetSpelling := users + "/matt/sbprobe-home/proj"

	if _, ok := layout.containing(linkSpelling); ok {
		t.Fatalf("precondition changed: %q should read as neutral ground", linkSpelling)
	}
	home, ok := layout.containing(targetSpelling)
	if !ok || home != users+"/matt" {
		t.Fatalf("containing(%q) = (%q, %v), want (%q, true) — this is the pair that makes "+
			"resolving before the check load-bearing", targetSpelling, home, ok, users+"/matt")
	}
}
