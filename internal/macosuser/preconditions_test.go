package macosuser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// failOne returns deps (and the workspace to launch) under which exactly the named precondition
// does not hold and every other one does. One entry per precondition, which
// TestEveryPreconditionHasAFailingCase enforces: a precondition added to the list without a
// way to fail it here is one nothing proves the launch refuses on.
var failOne = map[string]func(d *Deps) string{
	PreconditionMacOS:   func(d *Deps) string { d.IsMacOS = func() bool { return false }; return "/Users/Shared/yolo/proj" },
	PreconditionNotRoot: func(d *Deps) string { d.Geteuid = func() int { return 0 }; return "/Users/Shared/yolo/proj" },
	PreconditionSeatbelt: func(d *Deps) string {
		d.Which = func(n string) bool { return n != "sandbox-exec" }
		return "/Users/Shared/yolo/proj"
	},
	PreconditionSandboxUser: func(d *Deps) string {
		d.SandboxUserExists = func() bool { return false }
		return "/Users/Shared/yolo/proj"
	},
	PreconditionSandboxHome: func(d *Deps) string {
		d.PathIsDir = func(p string) bool { return p != SandboxHome() }
		return "/Users/Shared/yolo/proj"
	},
	PreconditionNeutralGround: func(d *Deps) string { return "/Users/matt/code/proj" },
	PreconditionWorkspaceShared: func(d *Deps) string {
		run := d.RunBash
		d.RunBash = func(s string) int {
			if strings.Contains(s, "ls -lde") {
				run(s) // still recorded
				return 1
			}
			return run(s)
		}
		return "/Users/Shared/yolo/proj"
	},
}

func TestEveryPreconditionHasAFailingCase(t *testing.T) {
	for _, c := range LaunchPreconditions() {
		if failOne[c.ID] == nil {
			t.Errorf("precondition %q has no failing case in failOne", c.ID)
		}
	}
	if len(failOne) != len(LaunchPreconditions()) {
		t.Errorf("failOne has %d cases for %d preconditions", len(failOne), len(LaunchPreconditions()))
	}
}

// The list's shape is what lets the launch stop at the first failure: every precondition is
// complete, and everything it requires comes before it. The in-home rule before the ACL probe
// is the one ordering a user has met as a loop (inhomeorder_test.go), so it is named too.
func TestThePreconditionListIsCompleteAndOrdered(t *testing.T) {
	seen := map[string]int{}
	for i, c := range LaunchPreconditions() {
		if c.ID == "" || c.Name == "" || c.holds == nil || c.Ready == nil || c.Unmet == nil ||
			c.Fix == nil || c.Refusal == nil {
			t.Errorf("precondition %d (%q) is missing a field", i, c.ID)
		}
		if _, dup := seen[c.ID]; dup {
			t.Errorf("two preconditions are named %q", c.ID)
		}
		for _, req := range c.Requires {
			if _, earlier := seen[req]; !earlier {
				t.Errorf("%q requires %q, which is not earlier in the list", c.ID, req)
			}
		}
		seen[c.ID] = i
	}
	if seen[PreconditionNeutralGround] > seen[PreconditionWorkspaceShared] {
		t.Error("the ACL probe is asked before the in-home rule — its remedy refuses every path under a home")
	}
}

// THE CALL SITE: a launch refuses on every precondition, with that precondition's own message,
// before the nix build and before asking anything after it. Delete the walk in RunMacosUser and
// every case launches.
func TestTheLaunchRefusesOnEachPreconditionWithItsOwnMessage(t *testing.T) {
	for _, c := range LaunchPreconditions() {
		t.Run(c.ID, func(t *testing.T) {
			var rec []string
			var buf bytes.Buffer
			d := mockDeps(&rec)
			d.Out = &buf
			ws := failOne[c.ID](&d)
			materialized := false
			d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
				materialized = true
				return mockDarwin(), true, nil
			}

			if rc := RunMacosUser(d, newOpts(ws)); rc != 1 {
				t.Fatalf("rc = %d, want 1\n%s", rc, buf.String())
			}
			want := richtext.Render(c.Refusal(ws), false)
			if strings.TrimSpace(buf.String()) != strings.TrimSpace(want) {
				t.Errorf("the launch printed\n%s\nwant %q's refusal\n%s", buf.String(), c.ID, want)
			}
			if materialized {
				t.Error("reached the nix build")
			}
			// Nothing after the unmet one is asked: the ACL probe is the one subprocess.
			if c.ID != PreconditionWorkspaceShared && strings.Contains(strings.Join(rec, "\n"), "ls -lde") {
				t.Error("asked the ACL probe after an earlier precondition had already refused")
			}
		})
	}
}

// yolo check's walk asks every precondition whose requirements held, and names the one that
// was ASKED and failed as the blocker of each it skipped — never an intermediate skip, which
// would point the reader at a row with no fix on it.
func TestCheckingThePreconditionsSkipsOnlyWhatAFailureMadeMoot(t *testing.T) {
	results := func(id string) (map[string]PreconditionResult, []string) {
		var rec []string
		d := mockDeps(&rec)
		ws := failOne[id](&d)
		out := map[string]PreconditionResult{}
		for _, r := range CheckLaunchPreconditions(d.launchProbes(), ws) {
			out[r.ID] = r
		}
		return out, rec
	}

	t.Run("not macOS blocks everything, and names macOS", func(t *testing.T) {
		got, _ := results(PreconditionMacOS)
		for id, r := range got {
			if id == PreconditionMacOS {
				if !r.Checked || r.Held {
					t.Errorf("macOS: checked=%v held=%v, want a checked failure", r.Checked, r.Held)
				}
				continue
			}
			if r.Checked || r.Blocker.ID != PreconditionMacOS {
				t.Errorf("%s: checked=%v blocker=%q, want unchecked and blocked by macOS", id, r.Checked, r.Blocker.ID)
			}
		}
	})
	t.Run("no sandbox user skips its home and the ACL probe, and asks the rest", func(t *testing.T) {
		got, rec := results(PreconditionSandboxUser)
		for _, id := range []string{PreconditionSandboxHome, PreconditionWorkspaceShared} {
			if got[id].Checked || got[id].Blocker.ID != PreconditionSandboxUser {
				t.Errorf("%s: checked=%v blocker=%q, want blocked by the sandbox user", id, got[id].Checked, got[id].Blocker.ID)
			}
		}
		if r := got[PreconditionNeutralGround]; !r.Checked || !r.Held {
			t.Errorf("the workspace's location is independent of the account and was not asked: %+v", r)
		}
		if strings.Contains(strings.Join(rec, "\n"), "ls -lde") {
			t.Error("ran the ACL probe with no account for its ACE to name")
		}
	})
	t.Run("a workspace under a home is never probed for sharing", func(t *testing.T) {
		got, rec := results(PreconditionNeutralGround)
		if r := got[PreconditionWorkspaceShared]; r.Checked || r.Blocker.ID != PreconditionNeutralGround {
			t.Errorf("sharing: checked=%v blocker=%q, want blocked by the workspace's location", r.Checked, r.Blocker.ID)
		}
		if strings.Contains(strings.Join(rec, "\n"), "ls -lde") {
			t.Error("ran the ACL probe for a workspace whose remedy would refuse it")
		}
	})
}
