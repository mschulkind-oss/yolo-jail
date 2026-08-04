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
	"testing"
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
			`{"kind":"briefing","from":"AGENTS.md","into":".claude/CLAUDE.md"}]}`)
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Pack rule: use rg.\n")
	writeFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), userProse)

	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"prosepack"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home, packDir
}

// localPackAGENTS is where a migrated destination's prose must land, under the TEMP home.
func localPackAGENTS(home string) string {
	return filepath.Join(home, ".config", "yolo-jail", "local", "AGENTS.md")
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
	home, _ := userProseFixture(t, "# My rules\n\nAlways run the tests.\n")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	if n := countLines(report, "[y/N]", "generate"); n != 1 {
		t.Fatalf("want exactly ONE adoption prompt line, got %d:\n%s", n, report)
	}
	// The prose MOVED into the local pack — behavior-preserving, not merely non-destructive.
	local, err := os.ReadFile(localPackAGENTS(home))
	if err != nil {
		t.Fatalf("the user's prose did not reach the local pack: %v\n%s", err, report)
	}
	if !strings.Contains(string(local), "Always run the tests.") {
		t.Errorf("the local pack does not carry the migrated prose:\n%s", local)
	}
	// BEHAVIOR-PRESERVING, which is the whole difference between this and an archive: the
	// destination is REGENERATED, and the user's instructions are still in it — now arriving
	// through the local pack (`<!-- from pack: local -->`) rather than as loose prose.
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
	if !strings.Contains(string(got), "<!-- from pack: local -->") {
		t.Errorf("the user's prose arrived unattributed — it must come through the local "+
			"pack:\n%s", got)
	}
}

// FAIL-CLOSED on stdin. A scripted `apply --host --assert` with no answerable stdin must NOT take
// ownership of the user's file — that is precisely the one-way door the gate exists for.
func TestApplyHostBriefingFailsClosedWithoutStdin(t *testing.T) {
	const userProse = "# My rules\n\nAlways run the tests.\n"
	home, _ := userProseFixture(t, userProse)
	dest := filepath.Join(home, ".claude", "CLAUDE.md")

	rc, report := applyWith(t, true, nil)
	if got, err := os.ReadFile(dest); err != nil || string(got) != userProse {
		t.Errorf("an unconfirmable adoption modified the user's briefing: %v %q", err, got)
	}
	if _, err := os.Stat(localPackAGENTS(home)); !os.IsNotExist(err) {
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
	local, err := os.ReadFile(localPackAGENTS(home))
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
			`{"kind":"briefing","from":"AGENTS.md","into":".claude/CLAUDE.md"}]}`)
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Pack rule: use rg.\n")
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

// THE ORPHAN. Dropping the last pack that contributes prose leaves no generated file behind — it
// is archived, so nothing is deleted and nothing is left with no owner.
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
			`{"kind":"briefing","from":"AGENTS.md","into":".claude/CLAUDE.md"}]}`)
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Pack rule: use rg.\n")
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

	// The prose pack leaves. `claude` still NAMES the destination but ships no prose of its own,
	// so nothing composes into it — the orphan case.
	selectPacks(t, home, `"claude"`)
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply after the drop rc=%d\n%s", rc, report)
	}
	if _, err := os.Lstat(dest); err == nil {
		after, _ := os.ReadFile(dest)
		t.Errorf("a generated briefing with no contributing pack left is an ORPHAN and must be "+
			"retired:\n%q\nreport:\n%s", after, report)
	}
	if got := archivedBriefings(t, home); len(got) == 0 {
		t.Errorf("the retired briefing must be archived, not deleted:\n%s", report)
	}
}
