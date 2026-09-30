package cli

// applyhostskillsreserved_test.go pins the RESERVED-CHILD fence and its recovery report at the
// host-apply CALL SITES, not at their helpers.
//
// Both halves ship with helper-level tests in internal/hostskills, and neither of those notices if
// the production call is deleted — which is the shape AGENTS.md names: "a test that pins the CALLEE
// while the CALL SITE is unpinned is not a test". Removing either call from applyHostSkills leaves
// every hostskills test green, so the guarantee has to be asserted through the real apply.
//
// Every test uses a t.TempDir() home. The real $HOME is never read or written.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func assertExists(t *testing.T, path, report string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s should still be where the owning tool put it: %v\nreport:\n%s",
			path, err, report)
	}
}

func assertMissing(t *testing.T, path, report string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s exists — the tree was ADOPTED, which is the measured data loss\nreport:\n%s",
			path, report)
	}
}

// reservedFixture selects a pack that composes .claude/skills and RESERVES the child `synced`,
// mirroring packs/claude's own declaration.
func reservedFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "rf")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"rf","description":"d","contributes":[`+
			`{"kind":"skills","into":".claude/skills","reserved":["synced"]}]}`)
	writeFile(t, filepath.Join(packDir, "briefing", "prose.md"), "rf prose\n")
	writeFile(t, filepath.Join(packDir, "skills", "rfskill", "SKILL.md"),
		"---\nname: rfskill\ndescription: d\n---\nbody\n")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+packDir+`","name":"rf"}]}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	defaultReport(t)
	return home
}

// A non-empty sync root in the user's home is FENCED and ANNOUNCED by a real apply — and survives
// it. Delete the fence from Adoptions and this adopts the tree; delete the notice's place in the
// default view and the line disappears from this report.
func TestHostApplyFencesAndAnnouncesASyncRoot(t *testing.T) {
	home := reservedFixture(t)
	// The shape that defeated every other guard: a manifest two levels down, under a plain dir.
	writeFile(t, filepath.Join(home, ".claude", "skills", "synced", "1111_2222",
		".claude-plugin", "plugin.json"), `{"name":"from-claude-ai"}`)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("a sync root must not break the apply — it is a notice, not a refusal: rc=%d\n%s",
			rc, report)
	}
	if !strings.Contains(report, "synced") {
		t.Fatalf("the apply said NOTHING about the fenced tree — a fence nobody is told about is "+
			"how a user concludes yolo lost their skills:\n%s", report)
	}
	// The tree is still where the other tool put it.
	assertExists(t, filepath.Join(home, ".claude", "skills", "synced", "1111_2222",
		".claude-plugin", "plugin.json"), report)
	// And it was NOT moved into the local pack, which is what adoption would have done.
	assertMissing(t, filepath.Join(home, ".config", "yolo-jail", "local", "skills", "synced"), report)
}

// NEWS ONLY WHEN IT IS NEWS (ST-N2): the first --assert that sees the tree announces it on one
// line, the next says nothing in the default view (it is under --verbose), and a change to the
// tree's content announces it again. A dry run records nothing.
func TestHostApplyAnnouncesASyncRootOnlyWhenItIsNew(t *testing.T) {
	home := reservedFixture(t)
	plugin := filepath.Join(home, ".claude", "skills", "synced", "1111_2222", ".claude-plugin",
		"plugin.json")
	writeFile(t, plugin, `{"name":"from-claude-ai"}`)
	const line = "~/.claude/skills/synced — another tool's sync root; yolo leaves it alone"

	count := func(report string) int { return strings.Count(report, line) }
	if _, dry := applyWith(t, false, nil); count(dry) != 1 {
		t.Fatalf("a dry run over a new sync root must announce it once:\n%s", dry)
	}
	if _, first := applyWith(t, true, strings.NewReader("y\n")); count(first) != 1 {
		t.Fatalf("the first --assert must announce it once — the dry run records nothing:\n%s", first)
	}
	if _, second := applyWith(t, true, nil); count(second) != 0 {
		t.Errorf("an unchanged sync root was announced again:\n%s", second)
	}
	t.Setenv("YOLO_VERBOSE", "1")
	if _, verbose := applyWith(t, false, nil); count(verbose) != 1 {
		t.Errorf("--verbose must still list it:\n%s", verbose)
	}
	t.Setenv("YOLO_VERBOSE", "")
	writeFile(t, plugin, `{"name":"from-claude-ai","version":"2"}`)
	if _, changed := applyWith(t, true, nil); count(changed) != 1 {
		t.Errorf("a sync root whose content changed must be announced again:\n%s", changed)
	}
}

// THE SHIPPED claude PACK says what its reserved tree is, in the notice's one line: the skills
// claude.ai syncs (Claude Code's `syncClaudeAiSkills`), not plugins — `claude plugin list`, which
// the old notice pointed at, said "No plugins installed" on the maintainer's machine.
func TestTheClaudePackNamesItsSyncRoot(t *testing.T) {
	for _, p := range packload.Embedded() {
		if p.Name != "claude" {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindSkills && len(c.Reserved) > 0 {
				if note := c.ReservedNotes["synced"]; !strings.Contains(note, "claude.ai") {
					t.Errorf("packs/claude's reserved_notes for synced = %q; want what fills it", note)
				}
				return
			}
		}
	}
	t.Fatal("packs/claude declares no reserved skills child")
}

// An EMPTY identity-minted bucket is fenced but NOT announced, through the real apply — the
// cried-wolf case, which would otherwise put a line in front of every user who has the feature on
// and nothing synced.
func TestHostApplySaysNothingAboutAnEmptySyncRoot(t *testing.T) {
	home := reservedFixture(t)
	mkdirAllT(t, filepath.Join(home, ".claude", "skills", "synced", "1111_2222"))
	writeFile(t, filepath.Join(home, ".claude", "skills", "synced", ".bucket-1111_2222"), "")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "reserved") {
		t.Errorf("an identity-minted empty bucket is not content; this line would fire for "+
			"everyone, forever, about nothing:\n%s", report)
	}
}

// THE RECOVERY REPORT, at its call site: a home whose sync root a PREVIOUS apply already adopted.
// Delete the ReservedInLocalPack call from applyHostSkills and this goes red.
func TestHostApplyReportsAnAlreadyAdoptedSyncRoot(t *testing.T) {
	home := reservedFixture(t)
	// The invisible damage: the sync root sitting in the local pack, where an old apply put it.
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "skills", "synced",
		"1111_2222", "a-skill", "SKILL.md"), "---\nname: a-skill\ndescription: d\n---\nb\n")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("the recovery report is a report, not a refusal: rc=%d\n%s", rc, report)
	}
	if !strings.Contains(report, "synced") {
		t.Fatalf("a home that is ALREADY wrong learned nothing — the whole defect was that it "+
			"was silent:\n%s", report)
	}
	if !strings.Contains(report, "PREVIOUS apply") {
		t.Errorf("the finding must say this came from an earlier apply, or it reads as something "+
			"this run did:\n%s", report)
	}
}
