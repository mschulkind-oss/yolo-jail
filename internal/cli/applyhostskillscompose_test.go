package cli

// applyhostskillscompose_test.go is the COMMAND-level gate for §6a-2: `skills` is composed
// wholesale, and the one-way door into that ownership is a confirmation.
//
// The package-level tests (internal/hostskills/compose_test.go) cover the composition, the migration
// and the retire in isolation. These exist because the GATE only exists here — the confirmation, the
// fail-closed stdin, and the observe-writes-nothing property are all properties of applyHost's
// wiring, and every one of them was a thing an earlier host kind got wrong at exactly this level.
//
// Every test uses a t.TempDir() home with XDG_CONFIG_HOME inside it. The real $HOME is never read or
// written — load-bearing here beyond the usual, since the real ~/.claude/skills holds the
// maintainer's own skills and the real ~/.config/yolo-jail holds this jail's live config.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// userSkillsFixture builds the mixed home §6a-2 measured and this change has to get right:
//
//   - ~/.claude/skills/mine — hand-written;
//   - ~/.codex/skills/mine — the SAME name, DIFFERENT content (a real conflict);
//   - ~/.pi/agent/skills/mine — byte-identical to claude's (the common case: a union, not a
//     conflict);
//   - a pack shipping a third skill into ~/.codex/skills at FLAT tier.
//
// Mixed tiers are deliberate: claude is namespaced and codex/pi are flat, which is what ships, and
// the flat half is the only one where a collision can even be represented (§6a-5).
func userSkillsFixture(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	packDir := filepath.Join(t.TempDir(), "shared")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"shared","description":"s","contributes":[`+
			`{"kind":"skills","from":"skills","into":".codex/skills","tier":"flat"}]}`)
	writeFile(t, filepath.Join(packDir, "skills", "packonly", "SKILL.md"),
		"---\nname: packonly\ndescription: d\n---\nPACK BODY\n")

	writeFile(t, filepath.Join(home, ".claude", "skills", "mine", "SKILL.md"),
		"---\nname: mine\ndescription: d\n---\nCLAUDE VERSION\n")
	writeFile(t, filepath.Join(home, ".codex", "skills", "mine", "SKILL.md"),
		"---\nname: mine\ndescription: d\n---\nCODEX VERSION\n")
	writeFile(t, filepath.Join(home, ".pi", "agent", "skills", "mine", "SKILL.md"),
		"---\nname: mine\ndescription: d\n---\nCLAUDE VERSION\n")

	selectPacks(t, home, `"claude","codex","pi",{"source":"file://`+packDir+`","name":"shared"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// localPackSkills is where a migrated skill must land, under the TEMP home.
func localPackSkills(home string) string {
	return filepath.Join(home, ".config", "yolo-jail", "local", "skills")
}

// linkAwareHashes is treeHashes plus SYMLINK TARGETS, which is what an "observe wrote nothing"
// assertion needs here that the briefing kind's did not: this mechanism CLEARS dangling links and
// MOVES directories, and neither operation creates a file for a content-only snapshot to notice.
func linkAwareHashes(t *testing.T, home string) map[string]string {
	t.Helper()
	out := treeHashes(t, home)
	err := filepath.Walk(home, func(p string, fi os.FileInfo, werr error) error {
		if werr != nil || fi == nil || fi.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		target, rerr := os.Readlink(p)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(home, p)
		out["link:"+rel] = target
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// skillsHashes is linkAwareHashes narrowed to every skills destination plus the local pack — the
// paths this kind owns.
//
// The narrowing exists for one measured reason and is not a general licence. `apply --host` is NOT
// whole-home idempotent at head: a surface's provenance record classifies a key as `default` on the
// first apply (the key is absent, so the default fills it) and as `host` on the second (the key is
// now present in the file), so `host-provenance/pi-settings.provenance` differs between apply 1 and
// apply 2 and converges from apply 3 on. Verified against a binary built from HEAD without this
// change, so it is pre-existing and belongs to the `config` kind. Asserting the whole home here
// would pin that unrelated wobble into the skills suite; the OBSERVE test still asserts over the
// WHOLE home, because observe writes nothing at all and has no such excuse.
func skillsHashes(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	roots := []string{
		filepath.Join(".claude", "skills"), filepath.Join(".codex", "skills"),
		filepath.Join(".pi", "agent", "skills"),
		filepath.Join(".config", "yolo-jail", "local"),
	}
	for rel, h := range linkAwareHashes(t, home) {
		bare := strings.TrimPrefix(rel, "link:")
		for _, root := range roots {
			if strings.HasPrefix(bare, root+string(filepath.Separator)) {
				out[rel] = h
			}
		}
	}
	return out
}

// bodiesUnder is the set of non-blank SKILL.md lines readable anywhere under root — how "is the
// user's content still reachable?" is asked without knowing what name it ended up under.
func bodiesUnder(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi == nil || fi.IsDir() || filepath.Base(p) != "SKILL.md" {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				out[line] = true
			}
		}
		return nil
	})
	return out
}

// THE ONE-WAY DOOR, and the whole §6a-2 outcome in one test: on confirm, the identical duplicate
// unions silently, the DIFFERING pair both survive under distinct names with a warning naming both
// sources, and every skill reaches every agent.
func TestApplyHostSkillsMigratesAndComposesEverywhere(t *testing.T) {
	home := userSkillsFixture(t)

	rc, report := applyWith(t, true, strings.NewReader("y\ny\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	if n := countLines(report, "[y/N]", "Move these skills"); n != 1 {
		t.Fatalf("want exactly ONE skills adoption prompt line, got %d:\n%s", n, report)
	}

	// TWO entries in the local pack, not three: the byte-identical pi copy unioned into claude's.
	entries, err := os.ReadDir(localPackSkills(home))
	if err != nil {
		t.Fatalf("the local pack was not created: %v\n%s", err, report)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) != 2 {
		t.Errorf("want 2 local-pack entries (the identical pair unions to one), got %v", names)
	}
	// The UNION is silent: reported, but not warned about, because identical content under one name
	// is not a conflict and warning about it would train the user to ignore the line that is.
	if n := countLines(report, "unioned into your local pack"); n != 1 {
		t.Errorf("want exactly one union line, got %d:\n%s", n, report)
	}
	// The CONFLICT is loud, exactly once, and names both sources.
	if n := countLines(report, "kept both (renamed)"); n != 1 {
		t.Errorf("want exactly one rename line, got %d:\n%s", n, report)
	}
	if n := countLines(report, "name conflict"); n != 1 {
		t.Errorf("the conflict warning must fire exactly ONCE, at the migration — got %d:\n%s",
			n, report)
	}
	if n := countLines(report, "kept both (renamed)", "codex"); n != 1 {
		t.Errorf("the rename line must name the agent the loser came from:\n%s", report)
	}

	// BOTH hand-written bodies survive, and each reaches EVERY agent — the win the ruling is after.
	dests := []string{
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".pi", "agent", "skills"),
	}
	for _, dir := range dests {
		got := bodiesUnder(t, dir)
		for _, want := range []string{"CLAUDE VERSION", "CODEX VERSION"} {
			if !got[want] {
				t.Errorf("%s does not carry %q — one local pack composed into every destination is "+
					"the whole point of the migration", dir, want)
			}
		}
	}
	if !bodiesUnder(t, filepath.Join(home, ".codex", "skills"))["PACK BODY"] {
		t.Errorf("the pack's own skill did not reach its declared destination:\n%s", report)
	}

	// THE PROPERTY THAT MUST NEVER BREAK: nothing the user wrote is absent from BOTH the destination
	// and the local pack.
	live := bodiesUnder(t, localPackSkills(home))
	for _, dir := range dests {
		for k := range bodiesUnder(t, dir) {
			live[k] = true
		}
	}
	for _, want := range []string{"CLAUDE VERSION", "CODEX VERSION"} {
		if !live[want] {
			t.Errorf("%q is absent from BOTH every destination and the local pack — that is the one "+
				"property this change may never break", want)
		}
	}
}

// IDEMPOTENT, and it never re-prompts. A second apply with NO stdin would fail closed and abort if
// the gate re-fired, so this asserts both at once.
func TestApplyHostSkillsIsIdempotentAndDoesNotReprompt(t *testing.T) {
	home := userSkillsFixture(t)
	if rc, report := applyWith(t, true, strings.NewReader("y\ny\n")); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	first := skillsHashes(t, home)

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("a home yolo has already composed must not re-prompt:\n%s", report)
	}
	if n := countLines(report, "name conflict"); n != 0 {
		t.Errorf("the conflict warning must fire ONLY at the migration — a warning on every apply "+
			"trains the user to ignore it, got %d:\n%s", n, report)
	}
	var diffs []string
	second := skillsHashes(t, home)
	for p, h := range second {
		if first[p] != h {
			diffs = append(diffs, p)
		}
	}
	for p := range first {
		if _, ok := second[p]; !ok {
			diffs = append(diffs, p+" (removed)")
		}
	}
	sort.Strings(diffs)
	if len(diffs) != 0 {
		t.Errorf("the second apply changed the home: %v\n%s", diffs, report)
	}
}

// FAIL-CLOSED on stdin. A scripted `apply --host --assert` with no answerable stdin must NOT take
// ownership of the user's skills — that is precisely the one-way door the gate exists for.
func TestApplyHostSkillsFailsClosedWithoutStdin(t *testing.T) {
	home := userSkillsFixture(t)
	before := linkAwareHashes(t, home)

	rc, report := applyWith(t, true, nil)
	// EVERY user skill is exactly where it was. Asserted over the whole home rather than per path,
	// because "the migration ran partway" is the failure mode a per-path check would miss.
	for _, rel := range []string{
		filepath.Join(".claude", "skills", "mine", "SKILL.md"),
		filepath.Join(".codex", "skills", "mine", "SKILL.md"),
		filepath.Join(".pi", "agent", "skills", "mine", "SKILL.md"),
	} {
		if before[rel] == "" {
			t.Fatalf("fixture bug: %s was not hashed", rel)
		}
		if got := linkAwareHashes(t, home)[rel]; got != before[rel] {
			t.Errorf("an unconfirmable adoption modified %s", rel)
		}
	}
	if _, err := os.Stat(localPackSkills(home)); !os.IsNotExist(err) {
		t.Errorf("an unconfirmable adoption created the local pack (stat err=%v)", err)
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
func TestApplyHostSkillsDeclineLeavesEverything(t *testing.T) {
	home := userSkillsFixture(t)
	before := linkAwareHashes(t, home)

	if rc, report := applyWith(t, true, strings.NewReader("n\nn\n")); rc != 0 {
		t.Fatalf("declined adoption rc=%d\n%s", rc, report)
	}
	for _, rel := range []string{
		filepath.Join(".claude", "skills", "mine", "SKILL.md"),
		filepath.Join(".codex", "skills", "mine", "SKILL.md"),
	} {
		if got := linkAwareHashes(t, home)[rel]; got != before[rel] {
			t.Errorf("a declined adoption modified %s", rel)
		}
	}
	if _, err := os.Stat(localPackSkills(home)); !os.IsNotExist(err) {
		t.Errorf("a declined adoption created the local pack (stat err=%v)", err)
	}
	if rc, report := applyWith(t, true, strings.NewReader("y\ny\n")); rc != 0 {
		t.Fatalf("adoption after a decline rc=%d\n%s", rc, report)
	}
	if _, err := os.Stat(filepath.Join(localPackSkills(home), "mine")); err != nil {
		t.Errorf("declining must be a deferral, not a dead end: %v", err)
	}
}

// OBSERVE WRITES NOTHING AND NEVER PROMPTS. Asserted on a recursive hash of the whole home INCLUDING
// symlink targets, because this mechanism clears links and moves directories — neither of which
// creates a file a content-only snapshot would notice.
func TestApplyHostSkillsObserveWritesNothing(t *testing.T) {
	home := userSkillsFixture(t)
	before := linkAwareHashes(t, home)

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("observe rc=%d\n%s", rc, report)
	}
	after := linkAwareHashes(t, home)
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
	// It must still SAY what an assert would take over, and resolve the conflict the same way, which
	// is how the user learns before the prompt exists. `would keep both` in particular: a dry run
	// that consulted the filesystem would see every target free and promise one bare name for both.
	if n := countLines(report, "would move to your local pack"); n != 1 {
		t.Errorf("want one 'would move' preview line, got %d:\n%s", n, report)
	}
	if n := countLines(report, "would keep both (renamed)"); n != 1 {
		t.Errorf("observe must resolve the conflict the way the write will, got %d rename "+
			"previews:\n%s", n, report)
	}
	if n := countLines(report, "would union into your local pack"); n != 1 {
		t.Errorf("want one 'would union' preview line, got %d:\n%s", n, report)
	}
}

// §6a-5 AT THE COMMAND LEVEL: the local pack's copy WINS a flat-tier collision with a shared pack's
// same-named skill.
//
// This is the acceptance test §6a-5 requires, and the fixture details are all load-bearing:
//
//   - `codex`, whose tier is FLAT. At namespaced tier each pack gets its own subtree so a collision
//     cannot be represented — the probe that used `claude` had both copies coexist and proved
//     nothing, which is how the defect survived.
//   - a SHARED pack listed BEFORE the local pack, which is unavoidable: the local pack is appended
//     last by config.LoadPacks. Under the old rule the earlier pack's recorded entry could not be
//     overwritten by anything, so the local pack lost.
//   - the assertion is on the delivered CONTENT, not on the report. The old behavior printed a
//     `skipped (yours)` line, so a report-shaped assertion would pass on a fix that changed only the
//     wording.
func TestApplyHostSkillsLocalPackWinsFlatTierCollision(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(t.TempDir(), "sflat")
	writeFile(t, filepath.Join(shared, "pack.json"),
		`{"name":"sflat","description":"s","contributes":[`+
			`{"kind":"skills","from":"skills","into":".codex/skills","tier":"flat"}]}`)
	writeFile(t, filepath.Join(shared, "skills", "mine", "SKILL.md"),
		"---\nname: mine\ndescription: d\n---\nSHARED BODY\n")
	writeFile(t, filepath.Join(localPackSkills(home), "mine", "SKILL.md"),
		"---\nname: mine\ndescription: d\n---\nLOCAL BODY\n")
	selectPacks(t, home, `"codex",{"source":"file://`+shared+`","name":"sflat"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	dest := filepath.Join(home, ".codex", "skills", "mine", "SKILL.md")
	// TWICE, and the second apply is not a formality — it is the run that exercises the OWNERSHIP
	// RECORD. On a first apply both layers write inside one run, so the per-run claim set alone
	// decides the collision; only on a re-apply does the SAVED record answer it, and the record is
	// where §6a-5 actually lived (`OwnedBy(dest, thisPack)`). A single-apply assertion passes with
	// the defect fully restored, verified by mutation.
	for i, label := range []string{"first", "second"} {
		rc, report := applyWith(t, true, nil) // nothing to adopt: a clean destination never prompts
		if rc != 0 {
			t.Fatalf("%s apply --host --assert rc=%d\n%s", label, rc, report)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("%s apply: the skill was not delivered: %v\n%s", label, err, report)
		}
		if !strings.Contains(string(got), "LOCAL BODY") {
			t.Errorf("%s apply: the LOCAL PACK's copy must win a flat-tier collision — it is "+
				"appended last precisely because a personal skill outranks a shared pack's "+
				"(§6a-5). Got:\n%s\nreport:\n%s", label, got, report)
		}
		// And nothing was reported as the user's: under the old rule the second apply printed
		// `skipped (yours) … belongs to pack "sflat"` against a name the local pack was entitled to.
		if n := countLines(report, "skipped (yours)"); n != 0 {
			t.Errorf("%s apply: a pack overwriting another PACK's entry is not a user-content "+
				"refusal, got %d such lines:\n%s", label, n, report)
		}
		_ = i
	}
}
