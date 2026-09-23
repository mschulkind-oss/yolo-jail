package cli

// applyhostlocalmove_test.go pins the local-pack MOVE at the command level
// (pack-briefing-defaults.md §4): an earlier yolo migrated the user's adopted prose into the
// local pack's ROOT AGENTS.md, which is no longer read as pack prose (P1). yolo chose that
// location, so the next `yolo host apply` moves it into briefing/, reports the move, and refuses
// — naming both files — when the target is already taken.
//
// The entrypoint tests cover the move's cases in isolation. These exist because the properties
// that matter are the WIRING's: that the apply calls the move at all, that it re-resolves the
// pack set so the moved prose reaches the destinations in the SAME apply, that a refusal stops
// the kind before anything is composed, and that observe writes nothing. Every test uses a temp
// HOME (userProseFixture's pattern); the real ~/.config/yolo-jail is never touched.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// legacyLocalPackHome is a home selecting the claude pack whose local pack carries the user's
// prose in its root AGENTS.md — the layout the old migration wrote — and no briefing/ yet.
func legacyLocalPackHome(t *testing.T, prose string) (home, legacy, target string) {
	t.Helper()
	home = t.TempDir()
	local := filepath.Join(home, ".config", "yolo-jail", "local")
	legacy = filepath.Join(local, "AGENTS.md")
	target = filepath.Join(local, "briefing", "local.md")
	writeFile(t, legacy, prose)
	selectPacks(t, home, `"claude"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home, legacy, target
}

// THE CALL SITE. One apply on a home with local/AGENTS.md leaves the prose in
// local/briefing/local.md, reports the move naming both files, and the prose still reaches the
// agent's destination — in that same apply, which is the re-resolve's job.
func TestApplyHostMovesTheLocalPacksRootAgentsMd(t *testing.T) {
	const prose = "# My rules\n\nAlways run the tests.\n"
	home, legacy, target := legacyLocalPackHome(t, prose)

	rc, report := applyWith(t, true, nil) // no stdin: nothing here may need a prompt
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	if n := countLines(report, "moved", legacy, target); n != 1 {
		t.Errorf("want ONE report line naming the move and both files, got %d:\n%s", n, report)
	}
	if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
		t.Errorf("local/AGENTS.md is still there (stat err=%v)\n%s", err, report)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != prose {
		t.Errorf("the prose did not move verbatim into %s: %v %q", target, err, got)
	}
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	got, err := os.ReadFile(dest)
	if err != nil || !strings.Contains(string(got), "Always run the tests.") {
		t.Errorf("the moved prose does not reach the agent in the same apply: %v\n%q\n%s",
			err, got, report)
	}

	// Idempotent: a second apply has nothing to move and changes nothing.
	rc, report = applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}
	if n := countLines(report, "moved", legacy); n != 0 {
		t.Errorf("a second apply reported a move again:\n%s", report)
	}
	if again, _ := os.ReadFile(dest); string(again) != string(got) {
		t.Errorf("the destination changed on re-apply:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

// THE TARGET IS TAKEN: refused, naming both files, rc 1 — and the kind STOPS before compose. The
// fixture is the shape that loses prose if it did not: a destination yolo already composed WITH
// the root AGENTS.md's prose (as an older yolo did), plus a different briefing/local.md. Composing
// anyway would regenerate the destination from briefing/local.md alone and drop the user's
// AGENTS.md prose from their agent — the loss the move exists to prevent.
func TestApplyHostRefusesTheMoveWhenTheTargetIsTaken(t *testing.T) {
	home, legacy, target := legacyLocalPackHome(t, "")
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	// Compose the destination once from the prose the older layout delivered…
	writeFile(t, target, "Legacy rule.\n")
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("setup apply rc=%d\n%s", rc, report)
	}
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	composed, err := os.ReadFile(dest)
	if err != nil || !strings.Contains(string(composed), "Legacy rule.") {
		t.Fatalf("setup: the destination was not composed: %v %q", err, composed)
	}
	// …then put that prose back where the older yolo kept it, and take the target name.
	if err := os.Rename(target, legacy); err != nil {
		t.Fatal(err)
	}
	writeFile(t, target, "A different rule.\n")

	rc, report := applyWith(t, true, nil)
	if rc != 1 {
		t.Errorf("a refused move must fail the apply, rc=%d\n%s", rc, report)
	}
	if n := countLines(report, "refused", legacy, target); n != 1 {
		t.Errorf("want ONE refusal line naming both files, got %d:\n%s", n, report)
	}
	if got, _ := os.ReadFile(legacy); string(got) != "Legacy rule.\n" {
		t.Errorf("the refusal touched %s: %q", legacy, got)
	}
	if got, _ := os.ReadFile(target); string(got) != "A different rule.\n" {
		t.Errorf("the refusal touched %s: %q", target, got)
	}
	if got, _ := os.ReadFile(dest); string(got) != string(composed) {
		t.Errorf("a refused move must stop the briefing render before compose — the "+
			"destination was regenerated without the user's AGENTS.md prose:\nbefore:\n%s\n"+
			"after:\n%s\n%s", composed, got, report)
	}
	if got := archivedBriefings(t, home); len(got) != 0 {
		t.Errorf("a refused move must not retire anything: %v\n%s", got, report)
	}
}

// OBSERVE writes nothing and says what the move would do. It previews no destination, because
// one composed without the move would misreport every destination the local pack feeds: the
// fixture's destination was composed from the local pack's prose, so a preview composed before
// the move would call it an orphan to retire.
func TestApplyHostObservePreviewsTheMoveAndWritesNothing(t *testing.T) {
	home, legacy, target := legacyLocalPackHome(t, "# My rules\n")
	// A destination composed from that prose, as an older yolo did from the root AGENTS.md.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(legacy, target); err != nil {
		t.Fatal(err)
	}
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("setup apply rc=%d\n%s", rc, report)
	}
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if got, err := os.ReadFile(dest); err != nil || !strings.Contains(string(got), "# My rules") {
		t.Fatalf("setup: the destination was not composed: %v %q", err, got)
	}
	if err := os.Rename(target, legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(target)); err != nil {
		t.Fatal(err)
	}
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
	if n := countLines(report, "would move", legacy, target); n != 1 {
		t.Errorf("want ONE 'would move' line naming both files, got %d:\n%s", n, report)
	}
	if n := countLines(report, "not previewed"); n != 1 {
		t.Errorf("want one line saying the destinations are not previewed, got %d:\n%s", n, report)
	}
	if n := countLines(report, dest); n != 0 {
		t.Errorf("observe previewed %s as if the move had not happened:\n%s", dest, report)
	}
}

// ONE FILE FOR BOTH. When the move and an adoption run in the same apply, the adopted prose is
// appended to the MOVED file rather than starting a second one — the migration writer and the
// move name the same file, so the user gets one file in the order yolo reported.
func TestApplyHostMoveAndMigrationShareOneFile(t *testing.T) {
	home, legacy, target := legacyLocalPackHome(t, "Old local rule.\n")
	writeFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "Hand-written rule.\n")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
		t.Errorf("local/AGENTS.md is still there (stat err=%v)", err)
	}
	got, err := os.ReadFile(target)
	if want := "Old local rule.\n\nHand-written rule.\n"; err != nil || string(got) != want {
		t.Errorf("the migration did not append to the moved file:\ngot  %q (%v)\nwant %q",
			got, err, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Errorf("want exactly one file in local/briefing/, got %d", len(entries))
	}
	dest, _ := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	for _, want := range []string{"Old local rule.", "Hand-written rule."} {
		if !strings.Contains(string(dest), want) {
			t.Errorf("the destination is missing %q:\n%s\n%s", want, dest, report)
		}
	}
}

// AN ADDRESSED LOCAL pack.json STILL NAMING THE LEGACY FILE STOPS THE KIND. Without the refusal
// the move made the file the local pack's implicit broadcast (governance never reads the
// reserved `from`), so prose the user routed to claude alone was composed into pi's briefing
// by the same apply. The refusal names the pack.json edit, and nothing is composed.
func TestApplyHostRefusesTheMoveWhileTheLocalManifestNamesTheLegacyFile(t *testing.T) {
	home, legacy, target := legacyLocalPackHome(t, "Claude only.\n")
	selectPacks(t, home, `"claude","pi"`)
	manifest := filepath.Join(filepath.Dir(legacy), "pack.json")
	writeFile(t, manifest, `{"name":"local","contributes":[`+
		`{"kind":"briefing","from":"AGENTS.md","agents":["claude"]}]}`)

	rc, report := applyWith(t, true, nil)
	if rc != 1 {
		t.Fatalf("want rc 1 (the move refused), got %d\n%s", rc, report)
	}
	if n := countLines(report, "refused", manifest, "briefing/local.md"); n != 1 {
		t.Errorf("want ONE refusal line naming the pack.json and the edit, got %d:\n%s", n, report)
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != "Claude only.\n" {
		t.Errorf("the refusal touched %s: %v %q", legacy, err, got)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("the refusal created %s", target)
	}
	for _, dest := range []string{".pi/agent/AGENTS.md", ".claude/CLAUDE.md"} {
		if got, err := os.ReadFile(filepath.Join(home, dest)); err == nil &&
			strings.Contains(string(got), "Claude only.") {
			t.Errorf("%s was composed with the local prose despite the refusal:\n%s", dest, got)
		}
	}
}
