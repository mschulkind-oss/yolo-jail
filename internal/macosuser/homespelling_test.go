package macosuser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// homespelling_test.go pins the neutral-ground refusal against the OTHER SPELLINGS of a path
// inside a user's home: the ones a default macOS install resolves to the same directory, but a
// byte comparison with "/Users" does not
// (docs/design/configurable-workspace-root.md §5, the first two rows of its table).
//
//   - `/USERS/matt/x`, `/users/matt/x` — APFS compares names case-insensitively by default.
//   - `/System/Volumes/Data/Users/matt/x` — `/Users` is a FIRMLINK to that directory on the
//     Data volume, and a firmlink is not a symlink, so resolvePathAbs (filepath.EvalSymlinks)
//     passes the spelling through unchanged.
//
// Each spelling is driven through every production caller of the check rather than through
// the function alone, so deleting a call site fails a test here: the launch's precondition walk,
// `yolo check`'s report of the same list, the plan invariants a dry-run prints,
// `yolo macos-fix-permissions`, and the capture plan.
//
// These run on Linux, where none of the spellings exist: the check is lexical, and the facts
// it needs about the filesystem are part of its layout. What only a Mac can say — that the
// spellings really do reach the same directory — is homespelling_darwin_test.go.

// inHomeSpellings are spellings of /Users/matt/code/proj that reach the same directory on a
// default macOS install, each with the home the refusal must name in the path's own spelling.
var inHomeSpellings = []struct{ workspace, home string }{
	{"/USERS/matt/code/proj", "/USERS/matt"},
	{"/users/matt/code/proj", "/users/matt"},
	{"/System/Volumes/Data/Users/matt/code/proj", "/System/Volumes/Data/Users/matt"},
	{"/system/volumes/data/USERS/matt/code/proj", "/system/volumes/data/USERS/matt"},
}

func TestTheLaunchRefusesEverySpellingOfAPathInAHome(t *testing.T) {
	for _, tc := range inHomeSpellings {
		t.Run(tc.workspace, func(t *testing.T) {
			var buf bytes.Buffer
			d := mockDeps(nil) // every probe passes, so only the location can refuse
			d.Out = &buf
			materialized := false
			d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
				materialized = true
				return mockDarwin(), true, nil
			}
			if rc := RunMacosUser(d, newOpts(tc.workspace)); rc != 1 {
				t.Fatalf("rc = %d, want 1 — the launch accepted a workspace inside %s:\n%s",
					rc, tc.home, buf.String())
			}
			if want := tc.workspace + " is inside the home folder " + tc.home; !strings.Contains(buf.String(), want) {
				t.Errorf("refused, but not for the home — want %q:\n%s", want, buf.String())
			}
			if materialized {
				t.Error("reached the nix build for a workspace inside a home")
			}
		})
	}
}

func TestYoloCheckReportsEverySpellingOfAPathInAHome(t *testing.T) {
	for _, tc := range inHomeSpellings {
		var found bool
		for _, r := range CheckLaunchPreconditions(mockDeps(nil).launchProbes(), tc.workspace) {
			if r.ID != PreconditionNeutralGround {
				continue
			}
			found = true
			if !r.Checked || r.Held {
				t.Errorf("%s: checked=%v held=%v, want the location reported as unmet",
					tc.workspace, r.Checked, r.Held)
			}
		}
		if !found {
			t.Fatalf("the check's list has no %q row", PreconditionNeutralGround)
		}
	}
}

func TestThePlanInvariantsRefuseEverySpellingOfAPathInAHome(t *testing.T) {
	for _, tc := range inHomeSpellings {
		plan := BuildRunPlan(tc.workspace, jsonx.NewOrderedMap(), nil, []string{"true"},
			"/opt/yolo-jail/dist-go/darwin-arm64/yolo", "", HomeOverlay{}, HostContext{},
			jsonx.NewOrderedMap(), nil, nil)
		if !plan.OffendingHomeSet || plan.OffendingHome != tc.home {
			t.Errorf("%s: plan home = (%q, %v), want (%q, true)",
				tc.workspace, plan.OffendingHome, plan.OffendingHomeSet, tc.home)
		}
		if !anyContains(PlanInvariants(plan), "is inside the home directory "+tc.home) {
			t.Errorf("%s: the plan invariants do not refuse it: %v", tc.workspace, PlanInvariants(plan))
		}
	}
}

// The command that threads inheriting ACEs through a tree is the one where a missed spelling
// costs most: it would grant the sandbox group a path inside the home, "exactly where a stray
// grant silently exposes ~/.ssh" (29b00697).
func TestMacosFixPermissionsRefusesEverySpellingOfAPathInAHome(t *testing.T) {
	for _, tc := range inHomeSpellings {
		var rec []string
		var buf bytes.Buffer
		d := mockDeps(&rec)
		d.Out = &buf
		if rc := MacosFixPermissions(d, tc.workspace); rc != 1 {
			t.Errorf("%s: rc = %d, want 1:\n%s", tc.workspace, rc, buf.String())
		}
		if len(rec) != 0 {
			t.Errorf("%s: shelled out before refusing: %v", tc.workspace, rec)
		}
	}
}

func TestTheCapturePlanRefusesEverySpellingOfAStagingTreeInAHome(t *testing.T) {
	for _, tc := range inHomeSpellings {
		opts := testCaptureOptions()
		opts.CaptureRoot = tc.workspace
		plan := BuildCapturePlan(opts)
		if !plan.OffendingHomeSet || plan.OffendingHome != tc.home {
			t.Errorf("%s: capture home = (%q, %v), want (%q, true)",
				tc.workspace, plan.OffendingHome, plan.OffendingHomeSet, tc.home)
		}
		if !anyContains(CapturePlanInvariants(plan), "stages on neutral ground") {
			t.Errorf("%s: the capture invariants do not refuse it: %v",
				tc.workspace, CapturePlanInvariants(plan))
		}
	}
}

// THE SHARED ROOT STAYS NEUTRAL UNDER EVERY SPELLING OF /Users, and its own name is matched
// exactly. This change only ever moves a path from accepted to refused: the two spellings of
// /Users/Shared below were accepted before and still are, and `/Users/SHARED` was refused as a
// home before and still is. Folding the exemption too would ACCEPT a spelling the rest of the
// plan does not treat as the shared root — ancestorLiterals keys on "/Users/Shared/" byte for
// byte — and, on a case-sensitive volume, a real account named "shared".
func TestTheSharedRootsExemptionIsItsExactName(t *testing.T) {
	for _, ws := range []string{
		"/Users/Shared/yolo/proj",
		"/USERS/Shared/yolo/proj",
		"/System/Volumes/Data/Users/Shared/yolo/proj",
	} {
		if home, ok := HomeContaining(ws); ok {
			t.Errorf("%s reads as inside the home %q", ws, home)
		}
	}
	if home, ok := HomeContaining("/Users/SHARED/yolo/proj"); !ok || home != "/Users/SHARED" {
		t.Errorf("/Users/SHARED/yolo/proj = (%q, %v), want it refused as before", home, ok)
	}
}

// THE FACTS ARE INJECTED, so the comparison is the same code on the Linux machine that develops
// this repo and on a Mac, and a test can state each one separately. A layout that compares names
// by their bytes (Linux, a case-sensitive APFS volume) does not fold; one that names no aliases
// knows only its root.
func TestTheHomeLayoutAppliesOnlyTheFactsItIsGiven(t *testing.T) {
	const root, alias = "/FakeUsers", "/Fake/Data/FakeUsers"
	cases := []struct {
		name   string
		layout homeLayout
		ws     string
		home   string // "" = neutral ground
	}{
		{"the root itself", homeLayout{usersRoot: root}, root + "/matt/x", root + "/matt"},
		{"no folding: another case is another directory", homeLayout{usersRoot: root}, "/FAKEUSERS/matt/x", ""},
		{"folding: another case is the root", homeLayout{usersRoot: root, foldCase: true}, "/FAKEUSERS/matt/x", "/FAKEUSERS/matt"},
		{"no alias: the firmlink's other end is unknown", homeLayout{usersRoot: root}, alias + "/matt/x", ""},
		{"an alias is the root", homeLayout{usersRoot: root, aliases: []string{alias}}, alias + "/matt/x", alias + "/matt"},
		{"an alias folds with the root", homeLayout{usersRoot: root, aliases: []string{alias}, foldCase: true}, "/FAKE/data/fakeusers/matt/x", "/FAKE/data/fakeusers/matt"},
		{"an alias's own shared child is neutral", homeLayout{usersRoot: root, aliases: []string{alias}}, alias + "/Shared/x", ""},
		{"a home itself", homeLayout{usersRoot: root, foldCase: true}, "/fakeusers/matt", "/fakeusers/matt"},
		{"a sibling of the root", homeLayout{usersRoot: root, foldCase: true}, "/FakeUsersX/matt/x", ""},
	}
	for _, tc := range cases {
		home, ok := tc.layout.containing(tc.ws)
		if ok != (tc.home != "") || home != tc.home {
			t.Errorf("%s: containing(%q) = (%q, %v), want %q", tc.name, tc.ws, home, ok, tc.home)
		}
	}
}
