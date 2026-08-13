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
			`{"kind":"skills","from":"skills","into":".codex/skills"}]}`)
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
// THE NARROWING NO LONGER HIDES ANYTHING, and the history is worth keeping because it is what
// made the wider defect findable. It used to carry an exemption: `apply --host` was not
// whole-home idempotent, because a surface's provenance record classified a key as `defaults` on
// the first apply (absent, so the default filled it) and as `host` on the second (present — put
// there by that very default), so `host-provenance/pi-settings.provenance` differed between apply
// 1 and apply 2 and converged only from apply 3. That was tracked as V2 and is FIXED
// (entrypoint.keepFilledDefaults); applyhostidempotent_test.go now asserts convergence over the
// WHOLE home, which is where a cross-kind property belongs.
//
// What survives is the narrowing itself, for its own reason: these tests are about which SKILL
// landed where, and a failure that names `.pi/agent/settings.json` would be reporting the config
// kind's business in the skills suite. Scope, not exemption.
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

// S1 AT THE COMMAND LEVEL: two packs claiming one unnamespaced skill name is FATAL, and the message
// names both packs, both source paths, and both remedies.
//
// THIS TEST REPLACES TestApplyHostSkillsLocalPackWinsFlatTierCollision, whose assertion was the
// opposite: §6a-5 made the LOCAL PACK's copy win, because it is appended last precisely so a
// personal skill outranks a shared pack's. The 2026-08-05 ruling gives that up deliberately, and the
// trade is worth recording where the old test was: an intentional override and an accidental clash
// are the SAME declaration, so yolo cannot distinguish them — and the loser got no report line at
// all, which made it the silent-loss class this batch exists to remove. §6a-5's own fix (a later
// layer MAY overwrite yolo's own record, so precedence is layer order rather than a permission) is
// untouched and still exercised by the local pack winning a name no other pack ships.
//
// The fixture is the old one, and every detail is still load-bearing:
//
//   - `codex`, which is unnamespaced. A namespaced pack claims its own SUBTREE, so this pair would
//     not contend at all — that is the message's second remedy.
//   - a SHARED pack listed BEFORE the local pack, which is unavoidable: the local pack is appended
//     last by config.LoadPacks.
//   - the assertion is on the rc and the MESSAGE CONTENT, not on a wording: each `Contains` below is
//     a fact the user needs in order to act (which packs, which files, which two fixes).
func TestApplyHostSkillsCollisionIsFatalAndNamesBothPacks(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(t.TempDir(), "sflat")
	sharedSkill := filepath.Join(shared, "skills", "mine")
	writeFile(t, filepath.Join(shared, "pack.json"),
		`{"name":"sflat","description":"s","contributes":[`+
			`{"kind":"skills","from":"skills","into":".codex/skills"}]}`)
	writeFile(t, filepath.Join(sharedSkill, "SKILL.md"),
		"---\nname: mine\ndescription: d\n---\nSHARED BODY\n")
	writeFile(t, filepath.Join(localPackSkills(home), "mine", "SKILL.md"),
		"---\nname: mine\ndescription: d\n---\nLOCAL BODY\n")
	selectPacks(t, home, `"codex",{"source":"file://`+shared+`","name":"sflat"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, nil) // nothing to adopt: a clean destination never prompts
	if rc == 0 {
		t.Fatalf("two packs claiming `mine` at an unnamespaced destination must FAIL the apply:\n%s",
			report)
	}
	// NOTHING was composed. Asserted on the filesystem rather than the report, because "refused" is
	// only worth saying if the home was really left alone.
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "mine")); !os.IsNotExist(err) {
		t.Errorf("a refused composition still wrote the contended skill (stat err=%v)\n%s",
			err, report)
	}
	// EXACTLY ONE refusal line for the kind. A report-wide Contains is unreliable here — the census
	// and destination lines mention `skills` too — so this asserts the specific line.
	if n := countLines(report, "skills     refused", "name collision"); n != 1 {
		t.Errorf("want exactly one skills-collision refusal line, got %d:\n%s", n, report)
	}
	// THE MESSAGE IS THE FEATURE: a fatal the user cannot act on is worse than the silence it
	// replaces, and this one WILL fire on a real case (a personal pack and a shipped pack both
	// shipping `agent-standards`).
	for _, want := range []string{
		"sflat", "local", // both packs, by name
		sharedSkill, // the shared pack's source path
		filepath.Join(localPackSkills(home), "mine"), // and the local pack's
		"RENAME",      // remedy 1
		"skills_tier", // remedy 2, by the exact key
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the refusal does not mention %q, which the user needs in order to fix "+
				"it:\n%s", want, report)
		}
	}
}

// RENAMING RESOLVES IT, and the local pack still wins a name no other pack ships — which is the half
// of §6a-5 that survives S1: precedence is LAYER ORDER, not a permission a record grants.
func TestApplyHostSkillsRenameResolvesTheCollision(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(t.TempDir(), "sflat")
	writeFile(t, filepath.Join(shared, "pack.json"),
		`{"name":"sflat","description":"s","contributes":[`+
			`{"kind":"skills","from":"skills","into":".codex/skills"}]}`)
	writeFile(t, filepath.Join(shared, "skills", "theirs", "SKILL.md"),
		"---\nname: theirs\ndescription: d\n---\nSHARED BODY\n")
	writeFile(t, filepath.Join(localPackSkills(home), "mine", "SKILL.md"),
		"---\nname: mine\ndescription: d\n---\nLOCAL BODY\n")
	selectPacks(t, home, `"codex",{"source":"file://`+shared+`","name":"sflat"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// TWICE, and the second apply is not a formality — it is the run that exercises the SAVED
	// OWNERSHIP RECORD rather than the per-run claim set (the original test's note, still true).
	for _, label := range []string{"first", "second"} {
		rc, report := applyWith(t, true, nil)
		if rc != 0 {
			t.Fatalf("%s apply --host --assert rc=%d\n%s", label, rc, report)
		}
		for name, body := range map[string]string{"mine": "LOCAL BODY", "theirs": "SHARED BODY"} {
			got, err := os.ReadFile(filepath.Join(home, ".codex", "skills", name, "SKILL.md"))
			if err != nil {
				t.Fatalf("%s apply: %s was not delivered: %v\n%s", label, name, err, report)
			}
			if !strings.Contains(string(got), body) {
				t.Errorf("%s apply: %s carries %q, want %q", label, name, got, body)
			}
		}
		if n := countLines(report, "skipped (yours)"); n != 0 {
			t.Errorf("%s apply: a pack's own entry is not a user-content refusal, got %d such "+
				"lines:\n%s", label, n, report)
		}
	}
}
