package cli

// applyhostbriefings_test.go is the COMMAND-level gate for §6a: `briefing` is generated
// wholesale, and the one-way door into that ownership is a confirmation.
//
// The entrypoint-level tests (internal/entrypoint/hostbriefing_test.go) cover the composition,
// the migration and the retire in isolation. These exist because the GATE only exists here — the
// confirmation, the fail-closed stdin, and the observe-writes-nothing property are all properties
// of applyHost's wiring, and every one of them was a thing an earlier host kind got wrong at
// exactly this level.
//
// Every test uses a t.TempDir() home with XDG_CONFIG_HOME inside it. The real $HOME is never read
// or written — load-bearing here beyond the usual, since the real ~/.claude/CLAUDE.md holds the
// maintainer's own hand-written instructions and the real ~/.config/yolo-jail holds this jail's
// live config.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// userProseFixture builds a home whose ~/.claude/CLAUDE.md is HAND-WRITTEN, plus a pack that
// contributes prose to the same destination. That is the migration's shape: the file exists, yolo
// has no record of writing it, and a pack is about to own it.
func userProseFixture(t *testing.T, userProse string) (home, packDir string) {
	t.Helper()
	home = t.TempDir()
	packDir = filepath.Join(t.TempDir(), "prosepack")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"prosepack","description":"p","contributes":[`+
			`{"kind":"briefing","from":"briefing/prose.md","into":".claude/CLAUDE.md"}]}`)
	writeFile(t, filepath.Join(packDir, "briefing", "prose.md"), "Pack rule: use rg.\n")
	writeFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), userProse)

	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"prosepack"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home, packDir
}

// localPackBriefing is where a migrated destination's prose must land, under the TEMP home: the
// local pack's briefing/local.md, never its root AGENTS.md, which no reader reads as pack prose
// (docs/reference/pack-system.md#briefing-p1).
func localPackBriefing(home string) string {
	return filepath.Join(home, ".config", "yolo-jail", "local", "briefing", "local.md")
}

// treeHashes is a recursive {relative path → sha256} of a home, for asserting that observe wrote
// NOTHING. A listing alone would miss an in-place rewrite of the same size.
func treeHashes(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(home, func(p string, fi os.FileInfo, werr error) error {
		if werr != nil || fi == nil || fi.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(home, p)
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// countLines returns how many report lines contain every one of `all`.
//
// Asserting on a SPECIFIC line rather than on the whole report, because the report mentions most
// kind names in its census output — a report-wide strings.Contains for "briefing" is true of
// every apply ever produced, so it cannot distinguish "the gate fired" from "the census listed
// the kind".
func countLines(report string, all ...string) int {
	n := 0
	for _, line := range strings.Split(report, "\n") {
		match := true
		for _, want := range all {
			if !strings.Contains(line, want) {
				match = false
				break
			}
		}
		if match {
			n++
		}
	}
	return n
}

// THE ONE-WAY DOOR. A hand-written briefing is not adopted without an explicit `y`, and the
// prompt fires exactly once, on the line that names the file.
func TestApplyHostBriefingConfirmsBeforeAdoptingUserProse(t *testing.T) {
	const userProse = "# My rules\n\nAlways run the tests.\n"
	home, _ := userProseFixture(t, userProse)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	if n := countLines(report, "[y/N]", "generate"); n != 1 {
		t.Fatalf("want exactly ONE adoption prompt line, got %d:\n%s", n, report)
	}
	// The prose MOVED into the local pack — behavior-preserving, not merely non-destructive.
	local, err := os.ReadFile(localPackBriefing(home))
	if err != nil {
		t.Fatalf("the user's prose did not reach the local pack: %v\n%s", err, report)
	}
	// VERBATIM, and nothing else: the local pack is the user's own file, composed into every
	// agent's briefing, so an annotation written here (the old `<!-- migrated from … -->`
	// marker) would reach the agent as a label on the user's own rules. Provenance lives in the
	// apply report, which names the adopted destination.
	if string(local) != userProse {
		t.Errorf("the local pack must hold the migrated prose verbatim, unannotated:\n"+
			"got  %q\nwant %q", local, userProse)
	}
	// BEHAVIOR-PRESERVING, which is the whole difference between this and an archive: the
	// destination is REGENERATED, and the user's instructions are still in it — now arriving
	// through the local pack rather than as loose prose.
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("the destination was not regenerated: %v", err)
	}
	if !strings.Contains(string(got), "Pack rule: use rg.") {
		t.Errorf("the destination is missing the pack's prose:\n%s", got)
	}
	if !strings.Contains(string(got), "Always run the tests.") {
		t.Errorf("the user's instructions no longer reach their agent — the migration is "+
			"supposed to preserve behavior, not merely avoid deleting:\n%s", got)
	}
	// That line is ALSO the proof the prose came through the local pack: the destination was
	// regenerated from packs alone, and the only other pack here ships "Pack rule: use rg.", so
	// the user's rule can only have arrived via the local pack checked above.
	if strings.Contains(string(got), "<!-- migrated from ") {
		t.Errorf("the compiled briefing carries a migration marker — the user's own rules must "+
			"not arrive labelled:\n%s", got)
	}
	if strings.Contains(string(got), "<!-- from pack:") {
		t.Errorf("a default composition carries a per-pack label; briefing_provenance is off:\n%s", got)
	}
}

// FAIL-CLOSED on stdin. A scripted `yolo host apply --assert` with no answerable stdin must NOT take
// ownership of the user's file — that is precisely the one-way door the gate exists for.
func TestApplyHostBriefingFailsClosedWithoutStdin(t *testing.T) {
	const userProse = "# My rules\n\nAlways run the tests.\n"
	home, _ := userProseFixture(t, userProse)
	dest := filepath.Join(home, ".claude", "CLAUDE.md")

	rc, report := applyWith(t, true, nil)
	if got, err := os.ReadFile(dest); err != nil || string(got) != userProse {
		t.Errorf("an unconfirmable adoption modified the user's briefing: %v %q", err, got)
	}
	if _, err := os.Stat(localPackBriefing(home)); !os.IsNotExist(err) {
		t.Errorf("an unconfirmable adoption moved prose into the local pack (stat err=%v)", err)
	}
	// The rc is deliberately unchanged, for confirmDroppedPackRetire's reason: nothing the user
	// asked for failed, and a permanent non-zero would make every scripted apply look broken.
	if rc != 0 {
		t.Errorf("an unconfirmed adoption must not fail the apply, rc=%d\n%s", rc, report)
	}
	if n := countLines(report, "not adopted"); n != 1 {
		t.Errorf("want one line saying nothing was adopted, got %d:\n%s", n, report)
	}
}

// DECLINING leaves everything, and is a deferral rather than a dead end: a later `y` still works.
func TestApplyHostBriefingDeclineLeavesEverything(t *testing.T) {
	const userProse = "# My rules\n\nAlways run the tests.\n"
	home, _ := userProseFixture(t, userProse)
	dest := filepath.Join(home, ".claude", "CLAUDE.md")

	if rc, report := applyWith(t, true, strings.NewReader("n\n")); rc != 0 {
		t.Fatalf("declined adoption rc=%d\n%s", rc, report)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != userProse {
		t.Errorf("a declined adoption modified the user's briefing: %v %q", err, got)
	}
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("adoption after a decline rc=%d\n%s", rc, report)
	}
	local, err := os.ReadFile(localPackBriefing(home))
	if err != nil || !strings.Contains(string(local), "Always run the tests.") {
		t.Errorf("declining must be a deferral, not a dead end: %v %q", err, local)
	}
}

// OBSERVE WRITES NOTHING AND NEVER PROMPTS. Asserted on a recursive hash of the whole home, so an
// in-place rewrite of identical length cannot slip through.
func TestApplyHostBriefingObserveWritesNothing(t *testing.T) {
	home, _ := userProseFixture(t, "# My rules\n\nAlways run the tests.\n")
	before := treeHashes(t, home)

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("observe rc=%d\n%s", rc, report)
	}
	after := treeHashes(t, home)
	var diffs []string
	for p, h := range after {
		if before[p] != h {
			diffs = append(diffs, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			diffs = append(diffs, p+" (removed)")
		}
	}
	sort.Strings(diffs)
	if len(diffs) != 0 {
		t.Errorf("observe changed the home: %v\n%s", diffs, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("observe must not prompt — it writes nothing:\n%s", report)
	}
	// It must still SAY what an assert would take over, which is how the user learns before the
	// prompt exists.
	if n := countLines(report, "would move your prose into"); n != 1 {
		t.Errorf("want one 'would move' preview line, got %d:\n%s", n, report)
	}
}

// NO PROMPT WHEN NOTHING IS AT STAKE, twice over: a clean home never asks, and a SECOND apply
// after an adoption never asks again (the record proves ownership). A confirmation that fires
// every run is one people learn to answer blind.
func TestApplyHostBriefingIsIdempotentAndDoesNotRepromtp(t *testing.T) {
	home, _ := userProseFixture(t, "# My rules\n\nAlways run the tests.\n")
	dest := filepath.Join(home, ".claude", "CLAUDE.md")

	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	first, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	rc, report := applyWith(t, true, nil) // NO stdin: a re-prompt would fail closed and abort
	if rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("a home yolo has already composed must not re-prompt:\n%s", report)
	}
	second, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("the briefing changed on re-apply:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if n := strings.Count(string(second), "Pack rule: use rg."); n != 1 {
		t.Errorf("the pack's prose appears %d times — wholesale composition cannot double "+
			"anything:\n%s", n, second)
	}
}

// A CLEAN HOME never prompts: there is no prose to adopt, so the destination is simply generated.
func TestApplyHostBriefingCleanHomeNeverPrompts(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "prosepack")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"prosepack","description":"p","contributes":[`+
			`{"kind":"briefing","from":"briefing/prose.md","into":".claude/CLAUDE.md"}]}`)
	writeFile(t, filepath.Join(packDir, "briefing", "prose.md"), "Pack rule: use rg.\n")
	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"prosepack"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, nil) // no stdin: a prompt would fail closed
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("a clean home has nothing to adopt and must not prompt:\n%s", report)
	}
	got, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil || !strings.Contains(string(got), "Pack rule: use rg.") {
		t.Errorf("the destination was not generated: %v %q", err, got)
	}
}

// THE ORPHAN. Dropping the last pack that DECLARES a destination leaves no generated file behind
// — it is archived, so nothing is deleted and nothing is left with no owner. Dropping a pack that
// only contributed PROSE is not that case since the host base exists (notch-convergence item 26):
// the agent pack still declares the destination, so yolo still composes it, from the host base
// alone, and the dropped pack's prose leaves by regeneration rather than by an archive.
//
// The fixture is a CLEAN home deliberately: with pre-existing user prose the migration creates the
// local pack, which then contributes to the same destination forever, so the destination is never
// orphaned. That is the migration working (the prose keeps reaching the agent — see
// TestApplyHostBriefingConfirmsBeforeAdoptingUserProse) rather than the orphan case, and testing
// the orphan through it would have asserted nothing.
func TestApplyHostBriefingDroppingThePackLeavesNoOrphan(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "prosepack")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"prosepack","description":"p","contributes":[`+
			`{"kind":"briefing","from":"briefing/prose.md","into":".claude/CLAUDE.md"}]}`)
	writeFile(t, filepath.Join(packDir, "briefing", "prose.md"), "Pack rule: use rg.\n")
	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"prosepack"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the first apply should have generated the destination: %v", err)
	}

	// The prose pack leaves. `claude` still NAMES the destination, so yolo still owns it and
	// composes the host base into it, followed by the claude pack's own prose (its worktree
	// section, docs/design/durable-scratch-space.md DS-D33); the dropped pack's rule is gone,
	// and nothing is archived.
	selectPacks(t, home, `"claude"`)
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply after the prose pack's drop rc=%d\n%s", rc, report)
	}
	claudeOwn, err := officialpacks.FS.ReadFile("claude/briefing/worktrees.md")
	if err != nil {
		t.Fatalf("the claude pack's own prose: %v", err)
	}
	want := jailcontent.ComposeBriefingSections(jailcontent.HostBriefingBase("", paths.IsMacOS),
		[]jailcontent.BriefingSection{{Pack: "claude", Text: strings.TrimRight(string(claudeOwn), "\n")}}, false)
	after, err := os.ReadFile(dest)
	if err != nil || strings.Contains(string(after), "Pack rule: use rg.") || string(after) != want {
		t.Errorf("with only the agent pack left the destination is the host base and the claude "+
			"pack's own prose: %v\n%q\n%s", err, after, report)
	}
	if got := archivedBriefings(t, home); len(got) != 0 {
		t.Errorf("a destination a selected pack still declares is not an orphan: %v\n%s", got, report)
	}

	// The agent pack leaves too: nothing declares the destination, so it is the orphan.
	selectPacks(t, home, ``)
	rc, report = applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply after the last pack's drop rc=%d\n%s", rc, report)
	}
	if _, err := os.Lstat(dest); err == nil {
		after, _ := os.ReadFile(dest)
		t.Errorf("a generated briefing no selected pack declares is an ORPHAN and must be "+
			"retired:\n%q\nreport:\n%s", after, report)
	}
	if got := archivedBriefings(t, home); len(got) == 0 {
		t.Errorf("the retired briefing must be archived, not deleted:\n%s", report)
	}
}

// THE HOST CALL SITE, pinned: `briefing_provenance: true` in the USER config must reach the host
// render (and the adoption comparison beside it). The default path is asserted by
// TestApplyHostBriefingConfirmsBeforeAdoptingUserProse; this fails if applyHostBriefings stops
// reading the key and hard-codes the default.
func TestApplyHostBriefingLabelsPackProseWhenTheUserConfigAsks(t *testing.T) {
	home, packDir := userProseFixture(t, "# My rules\n\nAlways run the tests.\n")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"briefing_provenance": true, "packs":["claude",{"source":"file://`+packDir+
			`","name":"prosepack"}]}`)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	got, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("the destination was not regenerated: %v", err)
	}
	if !strings.Contains(string(got), "<!-- from pack: prosepack -->\nPack rule: use rg.") {
		t.Errorf("briefing_provenance: true did not label the pack's prose:\n%s", got)
	}
}

// A DECLARED BRIEFING SOURCE THAT DELIVERS NOTHING IS REPORTED AT THE HOST NOTCH TOO
// (docs/reference/pack-system.md#briefing-p4): an absent `from` and a blank one each print a warning
// naming the path, as the jail launch does. Before, the host dropped the problem, and with the
// fallback chain gone the prose was then lost without a word.
func TestApplyHostReportsAnUnmetBriefingFrom(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "gappy")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"gappy","description":"p","contributes":[`+
			`{"kind":"briefing","from":"prose/missing.md","agents":["claude"]},`+
			`{"kind":"briefing","from":"prose/blank.md","agents":["claude"]}]}`)
	writeFile(t, filepath.Join(packDir, "prose", "blank.md"), "  \n")
	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"gappy"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	for _, write := range []bool{false, true} {
		rc, report := applyWith(t, write, nil)
		if rc != 0 {
			t.Fatalf("write=%v: an unmet `from` is a warning, not a failure; rc=%d\n%s", write, rc, report)
		}
		for _, want := range []string{"prose/missing.md", "prose/blank.md"} {
			if n := countLines(report, "⚠ briefing", "gappy", want); n != 1 {
				t.Errorf("write=%v: want ONE warning naming %s, got %d:\n%s", write, want, n, report)
			}
		}
	}
}

// afterFixture is a home with the user's own ~/mine.md and a pack composing prose into
// ~/.foo/AGENTS.md whose `after` names `after` — DP-B26's shape. extraPacks are prepended to the
// selection (`"claude",` for one), and the home is returned.
func afterFixture(t *testing.T, after, extraPacks string) string {
	t.Helper()
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "foo")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"foo","description":"f","contributes":[`+
			`{"kind":"briefing","from":"briefing/prose.md","into":".foo/AGENTS.md","after":"host:`+after+`"}]}`)
	writeFile(t, filepath.Join(packDir, "briefing", "prose.md"), "Foo rule: be brief.\n")
	selectPacks(t, home, extraPacks+`{"source":"file://`+packDir+`","name":"foo"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// DP-B26, END TO END: a briefing whose `after` names the user's own host file opens with it at the
// host, in the jail's bytes, and the report names the file. It was silently ignored.
func TestApplyHostBriefingPrependsTheAfterFile(t *testing.T) {
	home := afterFixture(t, "mine.md", "")
	writeFile(t, filepath.Join(home, "mine.md"), "MY OWN RULES\n")

	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	got, err := os.ReadFile(filepath.Join(home, ".foo", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "MY OWN RULES\n\n---\n\n") || !strings.Contains(string(got), "Foo rule: be brief.") {
		t.Errorf("~/.foo/AGENTS.md does not open with ~/mine.md above a `---` and hold the pack's "+
			"prose:\n%s", got)
	}
	if n := countLines(report, "~/.foo/AGENTS.md opens with your ~/mine.md"); n != 1 {
		t.Errorf("want one report line naming ~/mine.md, got %d:\n%s", n, report)
	}
}

// A BROADCAST PACK LISTED FIRST DOES NOT HIDE THE `after`. The broadcast reaches ~/.foo/AGENTS.md
// through a copy the resolver synthesizes, which carries no `after`, and it is the first pack at that
// path — so a destination whose `after` came from the first pack there, rather than the first
// contribution carrying one, read none and left ~/mine.md out silently (DP-B26's symptom).
func TestApplyHostBriefingTakesTheAfterPastABroadcastListedFirst(t *testing.T) {
	bcastDir := filepath.Join(t.TempDir(), "bcast")
	writeFile(t, filepath.Join(bcastDir, "pack.json"),
		`{"name":"bcast","description":"b","contributes":[{"kind":"briefing","from":"briefing/b.md"}]}`)
	writeFile(t, filepath.Join(bcastDir, "briefing", "b.md"), "Broadcast rule.\n")
	home := afterFixture(t, "mine.md", `{"source":"file://`+bcastDir+`","name":"bcast"},`)
	writeFile(t, filepath.Join(home, "mine.md"), "MY OWN RULES\n")

	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	got, err := os.ReadFile(filepath.Join(home, ".foo", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Not vacuous: the broadcast did reach the destination, so its copy was the first pack there.
	if !strings.Contains(string(got), "Broadcast rule.") {
		t.Fatalf("fixture: the broadcast did not reach ~/.foo/AGENTS.md:\n%s", got)
	}
	if !strings.HasPrefix(string(got), "MY OWN RULES\n\n---\n\n") {
		t.Errorf("~/.foo/AGENTS.md does not open with ~/mine.md:\n%s", got)
	}
	if n := countLines(report, "~/.foo/AGENTS.md opens with your ~/mine.md"); n != 1 {
		t.Errorf("want one report line naming ~/mine.md, got %d:\n%s", n, report)
	}
}

// EDITING the `after` file re-renders the destination with no prompt: the destination is yolo's
// own (the record says so), and the file is an input to it like any pack's prose.
func TestApplyHostBriefingReRendersWhenTheAfterFileChanges(t *testing.T) {
	home := afterFixture(t, "mine.md", "")
	writeFile(t, filepath.Join(home, "mine.md"), "FIRST\n")
	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	writeFile(t, filepath.Join(home, "mine.md"), "SECOND\n")

	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("apply after the edit rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("an edit to the `after` file asked a question:\n%s", report)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".foo", "AGENTS.md"))
	if !strings.HasPrefix(string(got), "SECOND\n") || strings.Contains(string(got), "FIRST") {
		t.Errorf("the destination was not re-rendered from the edited file:\n%s", got)
	}
	// Settled now: the line moves to the detail view, with the destination's own.
	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 ||
		countLines(report, "opens with your ~/mine.md") != 0 {
		t.Errorf("a settled destination's `after` line is detail; rc=%d\n%s", rc, report)
	}
}

// THE SHIPPED SHAPE: the claude pack's `after` names its own destination. A hand-written
// ~/.claude/CLAUDE.md is adopted once — moved into the local pack, which composes it back — and
// never prepended as well, so the user's prose appears exactly once and the next apply neither
// grows the file nor asks again.
func TestApplyHostBriefingNeverPrependsTheDestinationItself(t *testing.T) {
	const userProse = "# My rules\n\nAlways run the tests.\n"
	home, _ := userProseFixture(t, userProse)
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	first, _ := os.ReadFile(dest)
	if n := strings.Count(string(first), "Always run the tests."); n != 1 {
		t.Errorf("the user's prose appears %d times — the destination was prepended to "+
			"itself:\n%s", n, first)
	}
	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 || strings.Contains(report, "[y/N]") {
		t.Errorf("second apply rc=%d or re-prompted:\n%s", rc, report)
	}
	if second, _ := os.ReadFile(dest); string(second) != string(first) {
		t.Errorf("the destination changed on re-apply:\n--- first\n%s\n--- second\n%s", first, second)
	}
}

// AN `after` NAMING ANOTHER PACK'S DESTINATION is yolo's output, so it is not read — even while
// that file still holds the user's hand-written prose, which the adoption moves into the local
// pack and composes back into every destination, ~/.foo/AGENTS.md included. Read as well, the
// prose would reach ~/.foo/AGENTS.md twice. The report says so under --verbose only, since it
// changes nothing and every shipped pack reaches it.
func TestApplyHostBriefingDoesNotPrependAnotherPacksDestination(t *testing.T) {
	home := afterFixture(t, ".claude/CLAUDE.md", `"claude",`)
	writeFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "HAND WRITTEN RULE\n")
	defaultReport(t)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	foo, _ := os.ReadFile(filepath.Join(home, ".foo", "AGENTS.md"))
	if n := strings.Count(string(foo), "HAND WRITTEN RULE"); n != 1 {
		t.Errorf("the user's prose reaches ~/.foo/AGENTS.md %d times, want once (through the "+
			"local pack) — ~/.claude/CLAUDE.md, a file yolo composes, was read in as well:\n%s", n, foo)
	}
	if countLines(report, `after: "host:.claude/CLAUDE.md" is not read`) != 0 {
		t.Errorf("the skip is detail; the default view printed it:\n%s", report)
	}
	verboseReport(t)
	if _, report := applyWith(t, false, nil); countLines(report, "~/.foo/AGENTS.md",
		`after: "host:.claude/CLAUDE.md" is not read`, "a briefing destination this apply composes") != 1 {
		t.Errorf("--verbose must name the skip once, as a destination this apply composes:\n%s", report)
	}
}

// AN UNREADABLE `after` FILE warns in a jail launch's words and the destination is composed
// without it — and a FIFO, which a read would block on, does not stall the apply.
func TestApplyHostBriefingWarnsOfAnUnreadableAfterFile(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T, home string)
		why   string
	}{
		"dangling link": {func(t *testing.T, home string) {
			if err := os.Symlink(filepath.Join(home, "gone.md"), filepath.Join(home, "mine.md")); err != nil {
				t.Fatal(err)
			}
		}, "which does not exist. ~/.foo/AGENTS.md is composed without it. Restore the target, or remove the link."},
		"directory": {func(t *testing.T, home string) {
			if err := os.MkdirAll(filepath.Join(home, "mine.md"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "it is not a regular file. ~/.foo/AGENTS.md is composed without it."},
		"fifo": {func(t *testing.T, home string) {
			if err := syscall.Mkfifo(filepath.Join(home, "mine.md"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "it is not a regular file. ~/.foo/AGENTS.md is composed without it."},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			home := afterFixture(t, "mine.md", "")
			tc.setup(t, home)
			type result struct {
				rc     int
				report string
			}
			done := make(chan result, 1)
			go func() {
				rc, report := applyWith(t, true, strings.NewReader(""))
				done <- result{rc, report}
			}()
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Minute):
				t.Fatal("the apply blocked on the `after` file")
			}
			if got.rc != 0 {
				t.Fatalf("an unreadable `after` file is a warning, never a refusal: rc=%d\n%s",
					got.rc, got.report)
			}
			if n := countLines(got.report, "Warning: the host briefing ~/mine.md for ~/.foo/AGENTS.md "+
				"was not read", tc.why); n != 1 {
				t.Errorf("want one warning ending %q, got %d:\n%s", tc.why, n, got.report)
			}
			body, _ := os.ReadFile(filepath.Join(home, ".foo", "AGENTS.md"))
			if !strings.Contains(string(body), "Foo rule: be brief.") || strings.Contains(string(body), "\n---\n") {
				t.Errorf("want the destination composed without the file:\n%s", body)
			}
		})
	}
}

// applyFourTimes runs four --asserts, each answering stdin, and returns the destination's bytes
// after each one — the idempotence the launch gate depends on, since a `yolo host -- <bin>` whose
// observe pass always sees a change re-applies on every start.
func applyFourTimes(t *testing.T, dest, stdin string) []string {
	t.Helper()
	var got []string
	for i := 0; i < 4; i++ {
		if rc, report := applyWith(t, true, strings.NewReader(stdin)); rc != 0 {
			t.Fatalf("apply %d rc=%d\n%s", i+1, rc, report)
		}
		b, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("apply %d: %v", i+1, err)
		}
		got = append(got, string(b))
	}
	return got
}

// THE DOTFILES SHAPE: the destination is a symlink to the user's canonical file, and the pack's
// `after` names that canonical file. One file under two names, so a path comparison took it for the
// user's: the render writes through the link, and every later apply prepended yolo's previous output
// to itself — the file grew on every apply, and the launch gate re-applied on every start. Compared
// by file identity it is this destination, which the adoption moves into the local pack once.
func TestApplyHostBriefingDoesNotGrowADestinationLinkedToItsAfterFile(t *testing.T) {
	home := afterFixture(t, "AGENTS.md", "")
	writeFile(t, filepath.Join(home, "AGENTS.md"), "MY CANONICAL RULES\n")
	dest := filepath.Join(home, ".foo", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "AGENTS.md"), dest); err != nil {
		t.Fatal(err)
	}

	got := applyFourTimes(t, dest, "y\n")
	for i, body := range got {
		if n := strings.Count(body, "MY CANONICAL RULES"); n != 1 {
			t.Errorf("after apply %d the user's prose appears %d times, want once (through the "+
				"local pack):\n%s", i+1, n, body)
		}
		if body != got[0] {
			t.Errorf("apply %d changed the destination (%d bytes, then %d) — yolo's own output was "+
				"read back in through the link", i+1, len(got[0]), len(body))
		}
	}
}

// THE OTHER DIRECTION: the `after` file is a symlink to the destination itself. On the first apply
// the destination does not exist yet, so the link dangles and is warned about; from then on it is
// this destination's own output, and must not be prepended to itself.
func TestApplyHostBriefingDoesNotGrowADestinationItsAfterFileLinksTo(t *testing.T) {
	home := afterFixture(t, "mine.md", "")
	dest := filepath.Join(home, ".foo", "AGENTS.md")
	if err := os.Symlink(dest, filepath.Join(home, "mine.md")); err != nil {
		t.Fatal(err)
	}

	got := applyFourTimes(t, dest, "")
	for i, body := range got {
		if body != got[0] || strings.Contains(body, "\n---\n") {
			t.Errorf("apply %d: the destination was prepended to itself (%d bytes, then %d):\n%s",
				i+1, len(got[0]), len(body), body)
		}
	}
}

// THE SKIP'S REASON NAMES A FILE BY PATH, AND A PATH IS NOT MARKUP. ~/mine.md links to another
// pack's destination, whose directory a pack may spell with brackets (`into` allows them), so the
// --verbose line's reason carries that path. Printed unescaped, `[bold]` in it restyled the line
// and vanished from it, naming a file that does not exist.
func TestApplyHostBriefingEscapesThePathInTheSkipReason(t *testing.T) {
	barDir := filepath.Join(t.TempDir(), "bar")
	const odd = ".b[bold]/AGENTS.md"
	writeFile(t, filepath.Join(barDir, "pack.json"),
		`{"name":"bar","description":"b","contributes":[`+
			`{"kind":"briefing","from":"briefing/prose.md","into":"`+odd+`"}]}`)
	writeFile(t, filepath.Join(barDir, "briefing", "prose.md"), "Bar rule.\n")
	home := afterFixture(t, "mine.md", `{"source":"file://`+barDir+`","name":"bar"},`)
	if err := os.Symlink(filepath.Join(home, filepath.FromSlash(odd)), filepath.Join(home, "mine.md")); err != nil {
		t.Fatal(err)
	}
	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(odd))); err != nil {
		t.Fatalf("fixture: the first apply did not compose ~/%s: %v", odd, err)
	}

	verboseReport(t)
	_, report := applyWith(t, false, nil)
	want := richtext.Render(richtext.Escape("it is the same file as ~/"+odd+
		", a briefing destination this apply composes"), false)
	if countLines(report, `after: "host:mine.md" is not read`, want) != 1 {
		t.Errorf("--verbose must name the skip once, with ~/%s spelled as it is on disk:\n%s",
			odd, report)
	}
}

// THE RECORD HALF OF THE SKIP RULE, AT THE RENDER. claude is selected and then dropped, so on the
// second apply ~/.claude/CLAUDE.md is no longer a destination of this composition — only the
// briefing record still knows it is yolo's output — and foo's `after` names it. The render must
// compose with the record as the gate does, or it reads that file back in as the user's.
func TestApplyHostBriefingSkipsAFileTheRecordListsAtTheRender(t *testing.T) {
	home := afterFixture(t, ".claude/CLAUDE.md", `"claude",`)
	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	foo := filepath.Join(home, ".foo", "AGENTS.md")
	first, _ := os.ReadFile(foo)
	if claude, _ := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md")); len(claude) == 0 {
		t.Fatal("fixture: the first apply did not compose ~/.claude/CLAUDE.md")
	}
	cfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil || !strings.Contains(string(cfg), `"claude",`) {
		t.Fatalf("fixture: no claude to drop in %q (%v)", cfg, err)
	}
	writeFile(t, cfgPath, strings.Replace(string(cfg), `"claude",`, "", 1))

	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("apply after dropping claude rc=%d\n%s", rc, report)
	}
	if second, _ := os.ReadFile(foo); string(second) != string(first) {
		t.Errorf("~/.foo/AGENTS.md changed once claude was dropped — the composed "+
			"~/.claude/CLAUDE.md was read back in:\n--- first\n%s\n--- second\n%s\n%s", first, second, report)
	}
}
