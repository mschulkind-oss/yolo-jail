package macosuser

import (
	"bytes"
	"os"
	"path/filepath"
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
	PreconditionStagingRoot: func(d *Deps) string {
		d.StagingRootFacts = func(string) StagingRootFacts { return StagingRootFacts{} }
		return "/Users/Shared/yolo/proj"
	},
	PreconditionStagingRootOwner: func(d *Deps) string {
		d.StagingRootFacts = func(string) StagingRootFacts {
			return StagingRootFacts{Exists: true, IsDir: true, UID: 602, Mode: 0o755}
		}
		return "/Users/Shared/yolo/proj"
	},
	PreconditionStagingRootSafe: func(d *Deps) string {
		d.StagingRootFacts = func(string) StagingRootFacts {
			return StagingRootFacts{Exists: true, IsDir: true, UID: 501, Mode: 0o775}
		}
		return "/Users/Shared/yolo/proj"
	},
	PreconditionRunAsGrant: func(d *Deps) string {
		d.RunAsGrant = func() bool { return false }
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

// The staging root's three facts and the run-as rule are asked after the account and its home
// and before the workspace's, in this order: the ruled privilege preflight (G2 and the staging
// root, docs/design/macos-user-privilege-lifecycle-plan.md task 4).
func TestTheLaunchAsksForTheStagingRootAndTheRunAsGrant(t *testing.T) {
	var ids []string
	for _, c := range LaunchPreconditions() {
		ids = append(ids, c.ID)
	}
	want := "macos,not-root,sandbox-exec,sandbox-user,sandbox-home,staging-root," +
		"staging-root-owner,staging-root-safe,run-as-grant,workspace-location,workspace-shared"
	if got := strings.Join(ids, ","); got != want {
		t.Errorf("preconditions = %s\nwant %s", got, want)
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

// THE SHARING REMEDY QUOTES THE PATH, in the launch's refusal as in yolo check's fix: a project
// named `My Project` is two words to the shell otherwise, and the pasted command would share
// `/Users/Shared/yolo/My` and fail on the stray `Project`. Driven through the launch, so it
// fails if RunMacosUser stops printing this precondition's refusal as well as if the text
// loses its quoting.
func TestTheSharingRemedyQuotesTheWorkspace(t *testing.T) {
	const ws = "/Users/Shared/yolo/My Project"
	want := "yolo yolo-account share '" + ws + "'"
	var buf bytes.Buffer
	d := mockDeps(nil)
	d.Out = &buf
	failOne[PreconditionWorkspaceShared](&d)
	if rc := RunMacosUser(d, newOpts(ws)); rc != 1 {
		t.Fatalf("rc = %d, want 1\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), want) {
		t.Errorf("the launch's refusal does not say %q:\n%s", want, buf.String())
	}
	for _, c := range LaunchPreconditions() {
		if c.ID == PreconditionWorkspaceShared && !strings.Contains(c.Fix(ws), want) {
			t.Errorf("yolo check's fix does not say %q:\n%s", want, c.Fix(ws))
		}
	}
}

// PrivilegeUnmet is what the capture and fork-build acts and the host floor ask: the first unmet
// one of the four privilege facts or of their requirements, in the launch's order, and nothing a
// workspace decides.
func TestPrivilegeUnmetAsksThePrivilegeFactsAndTheirRequirementsOnly(t *testing.T) {
	for _, id := range []string{PreconditionMacOS, PreconditionNotRoot, PreconditionSandboxUser, PreconditionStagingRoot,
		PreconditionStagingRootOwner, PreconditionStagingRootSafe, PreconditionRunAsGrant} {
		d := mockDeps(nil)
		failOne[id](&d)
		if c, unmet := PrivilegeUnmet(d.launchProbes()); !unmet || c.ID != id {
			t.Errorf("%s fails: PrivilegeUnmet = %q, %v", id, c.ID, unmet)
		}
	}
	for _, id := range []string{PreconditionSeatbelt, PreconditionSandboxHome,
		PreconditionNeutralGround, PreconditionWorkspaceShared} {
		d := mockDeps(nil)
		failOne[id](&d)
		if c, unmet := PrivilegeUnmet(d.launchProbes()); unmet {
			t.Errorf("%s fails: PrivilegeUnmet named %q, which is not a privilege fact", id, c.ID)
		}
	}
	// A caller that wired no probe is unknown, which refuses.
	p := mockDeps(nil).launchProbes()
	p.StagingRoot, p.RunAsGrant = nil, nil
	if c, unmet := PrivilegeUnmet(p); !unmet || c.ID != PreconditionStagingRoot {
		t.Errorf("no probes: PrivilegeUnmet = %q, %v, want the staging root unmet", c.ID, unmet)
	}
}

// The impure half of the staging-root probe, on whatever OS runs the test: an absent path, a
// directory with its owner and mode, the `ls -lde` listing's access list, a failed listing, and
// a link that is neither followed nor listed.
func TestStagingRootFactsReadsWithoutFollowingALink(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "staged")
	noACL := func(string) (string, bool) { return "drwxr-xr-x  2 matt  wheel  64 Oct 16 10:00 x\n", true }
	if f := stagingRootFactsWith(root, noACL); f.Exists || f.Err != nil {
		t.Errorf("absent: %+v", f)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	f := stagingRootFactsWith(root, noACL)
	if !f.Exists || !f.IsDir || f.IsSymlink || f.HasACL || f.Mode != 0o755 ||
		f.UID != uint32(os.Geteuid()) || f.Err != nil {
		t.Errorf("a directory of mine: %+v", f)
	}
	if v := JudgeStagingRoot(f, os.Geteuid()); v != StagingRootOK {
		t.Errorf("a 0755 directory of mine is %v, not ok: %+v", v, f)
	}
	withACL := func(string) (string, bool) {
		return "drwxr-xr-x+ 2 matt  wheel  64 Oct 16 10:00 x\n 0: group:_yolojail allow list,search\n", true
	}
	if f := stagingRootFactsWith(root, withACL); !f.HasACL {
		t.Errorf("an access list was not seen: %+v", f)
	}
	if f := stagingRootFactsWith(root, func(string) (string, bool) { return "", false }); f.Err == nil {
		t.Errorf("a failed listing is not unknown: %+v", f)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	listed := false
	f = stagingRootFactsWith(link, func(string) (string, bool) { listed = true; return "", true })
	if !f.Exists || !f.IsSymlink || f.IsDir || listed {
		t.Errorf("a link was followed or listed (listed=%v): %+v", listed, f)
	}
}

func TestLsShowsACLReadsTheLinesAfterTheFirst(t *testing.T) {
	for out, want := range map[string]bool{
		"":                                      false,
		"drwxr-xr-x  2 m  wheel  64 Oct 16 x\n": false,
		"drwxr-xr-x  2 m  wheel  64 Oct 16 x\n\n":                                  false,
		"drwxr-xr-x+ 2 m  wheel  64 Oct 16 x\n 0: user:_yolojail allow add_file\n": true,
	} {
		if got := lsShowsACL(out); got != want {
			t.Errorf("lsShowsACL(%q) = %v, want %v", out, got, want)
		}
	}
}
