package hostskills

// dangling_test.go pins the F2 fix: a symlink whose target no longer exists is ABSENT, not the
// user's content, and the delivery says which link it cleared and where it pointed.
//
// The finding this comes from broke a working machine. `git mv claude/skills
// packs/matt-core/skills` left 34 rcm-deployed symlinks in a live home pointing at files that
// had moved, and every one of them read to yolo as precious user content — so the pack was
// reported `skipped (yours) … left untouched` and stayed permanently inert. The refusal RULE is
// correct and these tests keep it: what changed is only that a broken link stopped counting as
// content.
//
// The negative cases carry as much weight as the positive one. A test suite that only proves
// "the dangling link was replaced" would pass just as well on a change that replaced EVERY
// symlink, which would be strictly worse than the bug.
//
// Every home is a t.TempDir(). A test here must never touch a real $HOME.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dangle creates a symlink at dir/name pointing somewhere that does not exist, and returns the
// missing target — the value the report has to name.
func dangle(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A plausible stale target: the path an rcm-managed skill sat at before it moved into a pack.
	target := filepath.Join(t.TempDir(), "dotfiles", "claude", "skills", name)
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	return target
}

// THE headline claim, at the leaf skill name the field report hit: the pack's skill lands over
// the dangling link, and the report names the link AND what it pointed at.
func TestFlatDeliversOverADanglingSymlink(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	writeSkill(t, packSkills, "agent-standards", "FROM THE PACK")
	target := dangle(t, req.SkillsDir, "agent-standards")

	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}

	// The pack's content is actually there — this is the "permanently inert" half of the bug.
	data, err := os.ReadFile(filepath.Join(req.SkillsDir, "agent-standards", "SKILL.md"))
	if err != nil {
		t.Fatalf("the pack's skill must land over a dangling link — a link to a file that no "+
			"longer exists is not user content, and refusing it leaves the pack inert: %v", err)
	}
	if !strings.Contains(string(data), "FROM THE PACK") {
		t.Errorf("delivered content is not the pack's: %s", data)
	}
	// It is a real directory now, not a link.
	fi, err := os.Lstat(filepath.Join(req.SkillsDir, "agent-standards"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("the destination is still a symlink — the stale link was not cleared")
	}

	// REPORTED DISTINCTLY. "rendered" alone would hide that yolo unlinked something in a real
	// home; the user must be able to scan for it and to re-create the link if it mattered.
	var cleared *Result
	for i := range results {
		if results[i].Action == ActionCleared {
			cleared = &results[i]
		}
	}
	if cleared == nil {
		t.Fatalf("clearing a dangling symlink must get its OWN line, not be folded into the "+
			"render: %+v", results)
	}
	if !strings.Contains(cleared.Detail, target) {
		t.Errorf("the report must name what the dangling link POINTED AT — that is the one fact "+
			"telling the user which stale deployment this was: %q (want %q)", cleared.Detail, target)
	}
	if !strings.Contains(cleared.Detail, "no longer exists") {
		t.Errorf("the report must say the target is gone: %q", cleared.Detail)
	}
	// And nothing was archived: there was no content to archive.
	for _, r := range results {
		if r.Action == ActionArchived {
			t.Errorf("a broken link holds no content, so nothing may be archived for it: %+v", r)
		}
	}
}

// A symlink whose target EXISTS is still the user's content, reached by another route, and is
// still refused. This is the test that fails on an over-broad fix — the whole package Stats
// rather than Lstats on purpose, so a symlinked-but-valid skill dir counts as a skill dir.
func TestFlatStillRefusesAValidSymlink(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	writeSkill(t, packSkills, "shared-name", "FROM THE PACK")

	// The user's real skill lives elsewhere and is linked into the skills dir (exactly what a
	// WORKING rcm deployment looks like).
	real := writeSkill(t, t.TempDir(), "shared-name", "MINE, VIA A LIVE LINK")
	if err := os.MkdirAll(req.SkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(req.SkillsDir, "shared-name")); err != nil {
		t.Fatal(err)
	}

	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	if r := find(t, results, "shared-name"); r.Action != ActionSkippedUser {
		t.Errorf("a symlink whose target EXISTS is user content and must be refused: action = "+
			"%q (%q)", r.Action, r.Detail)
	}
	// The user's content survives THROUGH the link, and the link itself is intact.
	data, err := os.ReadFile(filepath.Join(real, "SKILL.md"))
	if err != nil {
		t.Fatalf("the user's real file must survive: %v", err)
	}
	if !strings.Contains(string(data), "MINE") {
		t.Errorf("the pack wrote through the user's symlink: %s", data)
	}
	fi, err := os.Lstat(filepath.Join(req.SkillsDir, "shared-name"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the user's live symlink was replaced by a real directory")
	}
}

// A real user-authored directory is refused, unchanged. The plain case, kept beside the symlink
// ones so a regression in either shows up here.
func TestFlatStillRefusesARealUserDir(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	writeSkill(t, packSkills, "mine", "FROM THE PACK")
	if err := os.MkdirAll(req.SkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, req.SkillsDir, "mine", "HAND WRITTEN")

	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	if r := find(t, results, "mine"); r.Action != ActionSkippedUser {
		t.Errorf("a real user dir must be refused: action = %q (%q)", r.Action, r.Detail)
	}
	data, _ := os.ReadFile(filepath.Join(req.SkillsDir, "mine", "SKILL.md"))
	if !strings.Contains(string(data), "HAND WRITTEN") {
		t.Errorf("the user's skill was overwritten: %s", data)
	}
}

// A symlink LOOP is NOT dangling. Stat reports ELOOP rather than ENOENT, and ELOOP is not proof
// of absence: a chain of 60 links each pointing at a REAL file reports the same error (verified
// 2026-08-04), so the kernel giving up walking says nothing about what is at the end. It falls
// through to the ownership rule and is treated as the user's — an inert pack, not lost content.
func TestSymlinkLoopIsNotTreatedAsDangling(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	writeSkill(t, packSkills, "loop", "FROM THE PACK")
	if err := os.MkdirAll(req.SkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(req.SkillsDir, "loop")
	b := filepath.Join(req.SkillsDir, "loop-partner")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}

	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	if r := find(t, results, "loop"); r.Action != ActionSkippedUser {
		t.Errorf("a symlink LOOP must not read as dangling — ELOOP is not proof of absence: "+
			"action = %q (%q)", r.Action, r.Detail)
	}
	if _, err := os.Lstat(a); err != nil {
		t.Errorf("the loop link must be left alone: %v", err)
	}
}

// The DIRECTORY case, which the field report never saw because it never got a report to read:
// the whole skills dir deployed as one stale link (the ~/.pi/agent/skills shape) aborted the
// delivery with a bare `mkdir …: file exists` and NO per-entry output at all.
func TestDanglingSkillsDirIsClearedNotFatal(t *testing.T) {
	for _, tier := range []Tier{TierFlat, TierNamespaced} {
		t.Run(tier.String(), func(t *testing.T) {
			req, packSkills := testReq(t, tier)
			writeSkill(t, packSkills, "packskill", "FROM THE PACK")
			// The skills dir ITSELF is the stale link.
			target := dangle(t, filepath.Dir(req.SkillsDir), filepath.Base(req.SkillsDir))

			results, err := Deliver(req)
			if err != nil {
				t.Fatalf("a dangling skills DIR must not abort the delivery — this used to be a "+
					"bare mkdir error with no per-entry report: %v", err)
			}
			var cleared bool
			for _, r := range results {
				if r.Action == ActionCleared && strings.Contains(r.Detail, target) {
					cleared = true
				}
			}
			if !cleared {
				t.Errorf("the cleared skills dir must be reported with its stale target: %+v",
					results)
			}
			if r := find(t, results, "packskill"); r.Action != ActionWrote {
				t.Errorf("the pack's skill must land: action = %q (%q)", r.Action, r.Detail)
			}
		})
	}
}

// The pack's own tier-A subtree deployed as a stale link — the other `mkdir: file exists` abort.
// It must also not downgrade the tier: ProbeTier Stats, so a dangling link reads as free, and
// reporting "was not written by yolo" about a link with nothing behind it would push the pack
// into the flat namespace it declared away from.
func TestDanglingPackSubtreeIsClearedAndKeepsTheTier(t *testing.T) {
	req, packSkills := testReq(t, TierNamespaced)
	writeSkill(t, packSkills, "packskill", "FROM THE PACK")
	target := dangle(t, req.SkillsDir, "matt-core")

	results, err := Deliver(req)
	if err != nil {
		t.Fatalf("a dangling pack subtree must not abort the delivery: %v", err)
	}
	for _, r := range results {
		if r.Action == ActionRefused && strings.Contains(r.Detail, "not written by yolo") {
			t.Errorf("a DANGLING link at the pack's name must not downgrade the tier — there is "+
				"nothing behind it to absorb: %+v", r)
		}
	}
	var cleared bool
	for _, r := range results {
		if r.Action == ActionCleared && strings.Contains(r.Detail, target) {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("the cleared subtree link must be reported with its stale target: %+v", results)
	}
	// Delivered namespaced, as declared.
	if _, err := os.Stat(filepath.Join(req.SkillsDir, "matt-core", "skills", "packskill",
		"SKILL.md")); err != nil {
		t.Errorf("the skill should land in the pack's own namespace: %v", err)
	}
}

// A dangling link ABOVE the skills dir is NOT cleared — unlinking a whole agent home is not a
// decision a skills delivery makes silently — but the error NAMES it, so the user can tell this
// apart from a permission problem.
func TestDanglingAncestorIsNamedNotCleared(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	writeSkill(t, packSkills, "packskill", "x")
	// ~/.claude itself is the stale link; the skills dir would sit inside it.
	agentHome := filepath.Dir(req.SkillsDir)
	target := dangle(t, filepath.Dir(agentHome), filepath.Base(agentHome))

	_, err := Deliver(req)
	if err == nil {
		t.Fatal("a dangling link above the skills dir must still fail — yolo does not unlink a " +
			"whole agent home on its own")
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("the failure must NAME the dangling ancestor and its target, or it is "+
			"indistinguishable from a permission problem: %v", err)
	}
	// And it really was left alone.
	fi, lerr := os.Lstat(agentHome)
	if lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the ancestor link must survive (err=%v)", lerr)
	}
}

// OBSERVE WRITES NOTHING, with a dangling link present. This is the assertion that a dry run
// cannot quietly repair the home: the full recursive tree is compared before and after, so an
// unlink shows up even though it creates no file.
func TestObserveClearsNothing(t *testing.T) {
	for _, tier := range []Tier{TierFlat, TierNamespaced} {
		t.Run(tier.String(), func(t *testing.T) {
			req, packSkills := testReq(t, tier)
			req.Observe = true
			writeSkill(t, packSkills, "agent-standards", "FROM THE PACK")
			// WHERE the link goes is tier-dependent, and deliberately so. A tier-A delivery never
			// touches a SIBLING of its own subtree (that is the property making it safe), so a
			// stale link at a bare skill name is correctly none of its business there; the link it
			// does meet is the one standing where its subtree belongs.
			danglingAt := "agent-standards"
			if tier == TierNamespaced {
				danglingAt = req.Pack
			}
			target := dangle(t, req.SkillsDir, danglingAt)
			home := filepath.Dir(filepath.Dir(req.SkillsDir))
			before := linkSnapshot(t, home)

			results, err := Deliver(req)
			if err != nil {
				t.Fatal(err)
			}
			if after := linkSnapshot(t, home); before != after {
				t.Errorf("observe posture changed the home:\nbefore:\n%s\nafter:\n%s",
					before, after)
			}
			// It still REPORTS the clear, in the future tense.
			var found bool
			for _, r := range results {
				if r.Action == ActionWouldClear && strings.Contains(r.Detail, target) {
					found = true
				}
			}
			if !found {
				t.Errorf("observe must report the clear it WOULD do, in the future tense: %+v",
					results)
			}
		})
	}
}

// F6a: the OBSERVE posture speaks in the future tense. Every other kind already did
// (`would render`, `⚠ would overwrite`); skills said `rendered`, which reads as though the dry
// run mutated the home — precisely the fear a dry run exists to allay.
//
// The ASSERT posture's wording is correct and is pinned here too, because the fix must not
// simply move the tense error to the other posture.
func TestObserveUsesTheFutureTense(t *testing.T) {
	for _, tier := range []Tier{TierFlat, TierNamespaced} {
		t.Run(tier.String(), func(t *testing.T) {
			// OBSERVE: a plain delivery into a free name.
			req, packSkills := testReq(t, tier)
			req.Observe = true
			writeSkill(t, packSkills, "packskill", "x")
			results, err := Deliver(req)
			if err != nil {
				t.Fatal(err)
			}
			if r := find(t, results, "packskill"); r.Action != ActionWouldWrite {
				t.Errorf("observe action = %q, want %q: a dry run that says %q reads as though "+
					"it wrote to the home", r.Action, ActionWouldWrite, ActionWrote)
			}

			// ASSERT: the same delivery reports the PAST tense, which was always right.
			req.Observe = false
			results, err = Deliver(req)
			if err != nil {
				t.Fatal(err)
			}
			if r := find(t, results, "packskill"); r.Action != ActionWrote {
				t.Errorf("assert action = %q, want %q", r.Action, ActionWrote)
			}
		})
	}
}

// An ARCHIVE in observe posture is future-tense too. Same defect, different action word.
func TestObserveArchiveUsesTheFutureTense(t *testing.T) {
	for _, tier := range []Tier{TierFlat, TierNamespaced} {
		t.Run(tier.String(), func(t *testing.T) {
			req, packSkills := testReq(t, tier)
			dropped := writeSkill(t, packSkills, "goes", "bye")
			writeSkill(t, packSkills, "stays", "here")
			if _, err := Deliver(req); err != nil { // assert, to create the state
				t.Fatal(err)
			}
			if err := os.RemoveAll(dropped); err != nil {
				t.Fatal(err)
			}

			req.Observe = true
			results, err := Deliver(req)
			if err != nil {
				t.Fatal(err)
			}
			if r := find(t, results, "goes"); r.Action != ActionWouldArchive {
				t.Errorf("observe archive action = %q, want %q", r.Action, ActionWouldArchive)
			}
			// And the dry run really did not archive it.
			if _, err := os.Stat(filepath.Join(req.SkillsDir, "goes")); err != nil {
				if _, nerr := os.Stat(filepath.Join(req.SkillsDir, "matt-core", "skills",
					"goes")); nerr != nil {
					t.Errorf("observe archived the entry: %v / %v", err, nerr)
				}
			}
		})
	}
}

// A RETIRING entry that has become a dangling link is cleared, not archived: renaming a broken
// link into the archive would report "moved to <path>" as though the user's content were
// recoverable there, when there is no content at all.
func TestRetiringADanglingEntryClearsInsteadOfArchiving(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	dropped := writeSkill(t, packSkills, "goes", "bye")
	// A second skill the pack KEEPS: a pack that ships nothing at all leaves no trace by design
	// and retires nothing, so without this the delivery returns before the retire scan runs.
	writeSkill(t, packSkills, "stays", "here")
	if _, err := Deliver(req); err != nil {
		t.Fatal(err)
	}
	// The pack stops shipping it, AND the delivered copy has been replaced by a stale link
	// (the user re-ran their dotfile manager over the top).
	if err := os.RemoveAll(dropped); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(req.SkillsDir, "goes")); err != nil {
		t.Fatal(err)
	}
	target := dangle(t, req.SkillsDir, "goes")

	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	r := find(t, results, "goes")
	if r.Action != ActionCleared {
		t.Errorf("a dangling entry being retired must be CLEARED, not archived — there is no "+
			"content to recover: action = %q (%q)", r.Action, r.Detail)
	}
	if !strings.Contains(r.Detail, target) {
		t.Errorf("the report must name the stale target: %q", r.Detail)
	}
	if _, err := os.Lstat(filepath.Join(req.SkillsDir, "goes")); !os.IsNotExist(err) {
		t.Errorf("the stale link should be gone: %v", err)
	}
	// The record no longer claims it.
	if _, recorded := req.Manifest.Owner(filepath.Join(req.SkillsDir, "goes")); recorded {
		t.Error("the ownership record must forget a cleared entry")
	}
}

// A cleared link does not become a permanent exception: the second apply is an ordinary update
// of yolo's own entry, and the tree is identical. An accumulating or re-clearing render would
// show up as a diff.
func TestDeliveryOverADanglingLinkIsIdempotent(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	writeSkill(t, packSkills, "agent-standards", "FROM THE PACK")
	dangle(t, req.SkillsDir, "agent-standards")

	if _, err := Deliver(req); err != nil {
		t.Fatal(err)
	}
	first := treeSnapshot(t, req.SkillsDir)
	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	if second := treeSnapshot(t, req.SkillsDir); first != second {
		t.Errorf("second apply changed the tree:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	for _, r := range results {
		if r.Action == ActionCleared {
			t.Errorf("nothing is left to clear on the second apply: %+v", r)
		}
	}
}

// linkSnapshot renders a tree including SYMLINK TARGETS and file contents, which treeSnapshot
// cannot do: it Walks with Lstat-free semantics and would either follow or skip links. An unlink
// creates no file, so only a snapshot that records links can prove observe did not perform one.
func linkSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case d.Type()&os.ModeSymlink != 0:
			target, rerr := os.Readlink(p)
			if rerr != nil {
				return rerr
			}
			b.WriteString("l " + rel + " -> " + target + "\n")
		case d.IsDir():
			b.WriteString("d " + rel + "\n")
		default:
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			b.WriteString("f " + rel + " " + string(data) + "\n")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
