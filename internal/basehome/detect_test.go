package basehome

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// baseHomeFixture mints a resolved temp base home.
//
// EvalSymlinks AT THE MINT POINT, per AGENTS.md's darwin rule: on macOS t.TempDir()
// returns /var/folders/… which IS a symlink to /private/var/folders/…, so any assertion
// comparing a walked path against the fixture path passes on Linux and fails on darwin.
// Resolved here so the next assertion added cannot forget. (The root-refusal rule is
// unaffected either way — only /var is the link — but the path comparisons are not.)
func baseHomeFixture(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	return home
}

func mkdirAll(t *testing.T, home, rel string) string {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	return p
}

func writeFile(t *testing.T, home, rel, body string) string {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return p
}

func symlink(t *testing.T, home, rel, target string) {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", rel, err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatalf("symlink %s -> %s: %v", rel, target, err)
	}
}

// legacyFixture is the design's own test fixture (§5.2, and the plan's test ideas): a base
// home with a whole-runtime directory, a mixed directory, the SQLite sibling set, a
// credential, a config surface, a content destination and a machine-scope credential dir.
func legacyFixture(t *testing.T) string {
	t.Helper()
	home := baseHomeFixture(t)

	// .claude — mixed, and the one with a redirected config surface.
	writeFile(t, home, ".claude/settings.json", `{"a":1}`)
	writeFile(t, home, ".claude/claude.json", `{"oauthAccount":{}}`)
	writeFile(t, home, ".claude/CLAUDE.md", "briefing")
	writeFile(t, home, ".claude/skills/thing/SKILL.md", "skill")
	writeFile(t, home, ".claude/history.jsonl", "line\n")
	writeFile(t, home, ".claude/projects/-home-someone-code-secret-thing/a.jsonl", strings.Repeat("x", 2048))
	symlink(t, home, ".claude/.credentials.json", "../.claude-shared-credentials/.credentials.json")

	// .copilot — a whole-runtime subtree, the sibling set, and one config surface.
	writeFile(t, home, ".copilot/config.json", "{}")
	writeFile(t, home, ".copilot/session-state/events.jsonl", "e\n")
	writeFile(t, home, ".copilot/session-store.db", strings.Repeat("d", 100))
	writeFile(t, home, ".copilot/session-store.db-wal", strings.Repeat("w", 10))
	writeFile(t, home, ".copilot/session-store.db-shm", strings.Repeat("s", 10))
	mkdirAll(t, home, ".copilot/downloads") // empty: moves whole

	// .gemini — the mixed-directory case the design names.
	writeFile(t, home, ".gemini/antigravity-cli/settings.json", "{}")
	writeFile(t, home, ".gemini/antigravity-cli/antigravity-oauth-token", "tok")
	writeFile(t, home, ".gemini/antigravity-cli/debug.log", "log\n")

	// .codex — the pack that declares no credential hook.
	writeFile(t, home, ".codex/auth.json", "tok")
	writeFile(t, home, ".codex/config.toml", "x = 1")
	writeFile(t, home, ".codex/cache/blob", "b")

	// The machine tier, and core's own dirs.
	writeFile(t, home, ".claude-shared-credentials/.credentials.json", "tok")
	writeFile(t, home, ".gemini-shared-credentials/token", "tok")
	writeFile(t, home, ".cache/npm/thing", "c")
	writeFile(t, home, ".ssh/id_ed25519", "key")
	return home
}

func fixtureDeclsWithSweep() Decls {
	d := fixtureDecls()
	d.StateDirs = append(d.StateDirs, ".oh-omp", ".pi")
	d.ConfigSurfaces = append(d.ConfigSurfaces, ".copilot/config.json")
	d.NonPackDirs = []string{".cache", ".ssh", ".config/git", ".local", "go", ".npm", ".npm-global", ".yolo"}
	d.SweepUnknownTopLevel = true
	return d
}

func candidate(rep Report, rel string) (Entry, bool) {
	for _, e := range rep.Candidates {
		if e.Rel == rel {
			return e, true
		}
	}
	return Entry{}, false
}

func candidateRels(rep Report) []string {
	out := make([]string, 0, len(rep.Candidates))
	for _, e := range rep.Candidates {
		out = append(out, e.Rel)
	}
	sort.Strings(out)
	return out
}

// TestDetectClassifiesTheFixtureTree drives the WALK over the design's fixture. It is the
// half hostmigrate keeps separate from its classify table: a table that only calls the
// predicate is the shape AGENTS.md rules out, because deleting the walk that consumes it
// fails nothing.
func TestDetectClassifiesTheFixtureTree(t *testing.T) {
	home := legacyFixture(t)
	rep := Detect(home, fixtureDeclsWithSweep())

	mustMove := []string{
		".claude/history.jsonl",
		".claude/projects", // no kept leaf beneath: ONE candidate, moves whole
		".codex/cache",
		".copilot/session-state",
		".copilot/session-store.db",
		".copilot/session-store.db-wal",
		".copilot/session-store.db-shm",
		".copilot/downloads", // empty directories move whole
		".gemini/antigravity-cli/debug.log",
	}
	for _, rel := range mustMove {
		if _, ok := candidate(rep, rel); !ok {
			t.Errorf("%s is not a candidate; got %v", rel, candidateRels(rep))
		}
	}

	mustStay := []string{
		".claude/settings.json", ".claude/claude.json", ".claude/CLAUDE.md",
		".claude/skills", ".claude/skills/thing", ".claude/skills/thing/SKILL.md",
		".claude/.credentials.json",
		".claude-shared-credentials", ".claude-shared-credentials/.credentials.json",
		".gemini-shared-credentials", ".gemini-shared-credentials/token",
		".codex/auth.json", ".codex/config.toml",
		".copilot/config.json",
		".gemini/antigravity-cli/settings.json",
		".gemini/antigravity-cli/antigravity-oauth-token",
		".cache", ".cache/npm", ".ssh", ".ssh/id_ed25519",
	}
	// Every one of these is a forced re-login, a lost briefing or a lost key if it moves,
	// which is the H-class harm the classes exist to prevent — so the assertion is on the
	// ENTRY and on every ancestor of it, since a collapsed parent moves the child too.
	for _, rel := range mustStay {
		if e, ok := candidate(rep, rel); ok {
			t.Errorf("%s must never move (got %s, dir=%v)", rel, e.Class, e.Dir)
		}
		for _, e := range rep.Candidates {
			if e.Dir && underOrAt(rel, e.Rel) {
				t.Errorf("%s moves inside collapsed candidate %s", rel, e.Rel)
			}
		}
	}
}

// TestDetectNeverMakesARootACandidate pins the mountpoint constraint: a base state dir is
// a bind-mount anchor, so the entry that moves is always a CHILD. Renaming the dir itself
// orphans a live jail's mount in place (the 2026-07-04 incident PruneShadowedHome's
// contents-only discipline came from).
func TestDetectNeverMakesARootACandidate(t *testing.T) {
	home := legacyFixture(t)
	rep := Detect(home, fixtureDeclsWithSweep())
	for _, root := range rep.Roots {
		if e, ok := candidate(rep, root); ok {
			t.Fatalf("walk root %s is itself a candidate (dir=%v) — that renames a mountpoint", root, e.Dir)
		}
	}
	if len(rep.Roots) == 0 {
		t.Fatal("no roots walked; the assertion above is vacuous")
	}
}

// TestDetectMovesNothing is §5.1's "detection never aborts and never moves anything".
// Without it, nothing in this package notices an apply being wired into the walk.
func TestDetectMovesNothing(t *testing.T) {
	home := legacyFixture(t)
	before := snapshot(t, home)
	rep := Detect(home, fixtureDeclsWithSweep())
	if len(rep.Candidates) == 0 {
		t.Fatal("fixture produced no candidates; the snapshot comparison would be vacuous")
	}
	if after := snapshot(t, home); after != before {
		t.Fatalf("detection mutated the base home:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func snapshot(t *testing.T, home string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(home, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(home, p)
		info, err := e.Info()
		if err != nil {
			return err
		}
		lines = append(lines, fmt.Sprintf("%s %s %d", rel, info.Mode(), info.Size()))
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// TestSharedDirsAreStructurallyUnreachable is the design's step-1 claim, and the word
// STRUCTURAL is what it tests: not "the root list happens not to include one" but "the
// walk refuses it when handed it as a root, on purpose". Both routes in are covered —
// named as a root, and reached as a child of one.
func TestSharedDirsAreStructurallyUnreachable(t *testing.T) {
	home := legacyFixture(t)

	d := fixtureDeclsWithSweep()
	d.StateDirs = append(d.StateDirs, ".claude-shared-credentials", ".gemini-shared-credentials")
	rep := Detect(home, d)
	for _, root := range rep.Roots {
		if d.underSharedDir(root) {
			t.Fatalf("walked %s: a machine-scope shared dir must never be a root", root)
		}
	}
	if len(rep.Excluded) != 2 {
		t.Fatalf("Excluded = %v, want both shared dirs recorded", rep.Excluded)
	}
	for _, e := range rep.Candidates {
		if d.underSharedDir(e.Rel) {
			t.Fatalf("%s is a candidate; the machine tier is excluded outright", e.Rel)
		}
	}

	// And as a NESTED copy under a state dir, which the design says steps 2 and the Lstat
	// rule decide.
	nested := baseHomeFixture(t)
	writeFile(t, nested, ".claude/.claude-shared-credentials/.credentials.json", "tok")
	nrep := Detect(nested, Decls{
		StateDirs:  []string{".claude"},
		SharedDirs: []string{".claude-shared-credentials", ".claude/.claude-shared-credentials"},
	})
	if len(nrep.Candidates) != 0 {
		t.Fatalf("nested shared dir produced candidates %v", candidateRels(nrep))
	}
}

// TestSymlinkIntoTheSharedDirIsKept covers §5.1's symlink rule. The case is real rather
// than theoretical: EnsureGlobalStorage removes the old .claude/.credentials.json only
// when it is a REGULAR file, so a host whose copy is a link into the machine tier still
// has one, and following it is not how it is recognised — the target is read as a string.
func TestSymlinkIntoTheSharedDirIsKept(t *testing.T) {
	home := baseHomeFixture(t)
	writeFile(t, home, ".claude-shared-credentials/creds", "tok")
	symlink(t, home, ".claude/linked-creds", "../.claude-shared-credentials/creds")
	symlink(t, home, ".claude/abs-creds", filepath.Join(home, ".claude-shared-credentials", "creds"))
	symlink(t, home, ".claude/stale", "../nowhere/gone")
	symlink(t, home, ".claude/outside", "/etc/hosts")

	rep := Detect(home, Decls{
		StateDirs:  []string{".claude"},
		SharedDirs: []string{".claude-shared-credentials"},
	})
	for _, rel := range []string{".claude/linked-creds", ".claude/abs-creds"} {
		if _, ok := candidate(rep, rel); ok {
			t.Errorf("%s is a candidate; a link into the machine tier is a CREDENTIAL", rel)
		}
	}
	// "a stale link elsewhere is RUNTIME and the link itself moves"
	for _, rel := range []string{".claude/stale", ".claude/outside"} {
		if _, ok := candidate(rep, rel); !ok {
			t.Errorf("%s is not a candidate; got %v", rel, candidateRels(rep))
		}
	}
}

// TestSymlinkedDirIsNotDescended pins that a link to a directory is treated as a leaf.
// Descending one would sweep whatever it points at — the same harm as following a root.
func TestSymlinkedDirIsNotDescended(t *testing.T) {
	outside := baseHomeFixture(t)
	writeFile(t, outside, "real/keepme.txt", "user data")

	home := baseHomeFixture(t)
	mkdirAll(t, home, ".claude")
	symlink(t, home, ".claude/link-to-outside", filepath.Join(outside, "real"))

	rep := Detect(home, Decls{StateDirs: []string{".claude"}})
	e, ok := candidate(rep, ".claude/link-to-outside")
	if !ok {
		t.Fatalf("the link itself must be the candidate; got %v", candidateRels(rep))
	}
	// THE Dir FLAG IS THE OBSERVABLE, not the absence of the target's name. A walk that
	// follows the link descends a subtree with no kept leaf in it, which COLLAPSES back
	// into one candidate at the link's own path — identical to the correct answer except
	// that it now claims a directory's worth of bytes and would be moved with a rename of
	// the link's TARGET's contents. Measured: asserting only "no candidate names
	// keepme.txt" passes with the follow.
	if e.Dir {
		t.Fatalf("%s is a whole-DIRECTORY candidate: the walk followed a symlink to a "+
			"directory and descended it (%d bytes, %d leaves)", e.Rel, e.Bytes, e.Leaves)
	}
	if e.Leaves != 1 {
		t.Errorf("%s covers %d leaves; a link moves as itself", e.Rel, e.Leaves)
	}
	for _, c := range rep.Candidates {
		if strings.Contains(c.Rel, "keepme.txt") {
			t.Fatalf("the walk descended a symlinked directory: %s", c.Rel)
		}
	}
}

// TestTheSharedTierWinsOverEveryLaterRule pins rule 1's POSITION, which is the mutable
// half of the structural claim.
//
// The walk's own descend guard cannot be tested: MEASURED, deleting it changes no
// fixture's outcome, because the classifier already answers CREDENTIAL for the same set
// and a kept directory is never descended. What CAN break is the ordering — rule 1 moving
// below the config or content rule — and that is exactly what the redundant guard in the
// walk exists to survive. So the ordering is what gets the test.
func TestTheSharedTierWinsOverEveryLaterRule(t *testing.T) {
	d := Decls{
		StateDirs:  []string{".claude"},
		SharedDirs: []string{".claude-shared-credentials"},
		// Declarations that would otherwise claim the same paths. A pack CAN name these:
		// nothing validates a content destination against the machine tier.
		ConfigSurfaces: []string{".claude-shared-credentials/settings.json"},
		ContentDests:   []string{".claude-shared-credentials/skills"},
	}
	for _, rel := range []string{
		".claude-shared-credentials/settings.json",
		".claude-shared-credentials/skills",
		".claude-shared-credentials/skills/a/SKILL.md",
	} {
		if got := d.Classify(rel); got != Credential {
			t.Errorf("Classify(%s) = %s, want CREDENTIAL: the machine tier is rule 1 and "+
				"is excluded OUTRIGHT, before any later rule gets a say", rel, got)
		}
	}
}

// TestRootConstraint is §5.1's "the walk root itself is constrained": a symlink root and a
// regular-file root are REFUSED — reported, never followed, never archived — and a missing
// root is a silent no-op.
func TestRootConstraint(t *testing.T) {
	realClaude := baseHomeFixture(t)
	writeFile(t, realClaude, "projects/thing.jsonl", "user data")

	home := baseHomeFixture(t)
	symlink(t, home, ".claude", realClaude) // the user's real ~/.claude
	writeFile(t, home, ".codex", "this is a file where a mountpoint belongs")
	// .copilot is absent entirely.

	rep := Detect(home, Decls{StateDirs: []string{".claude", ".codex", ".copilot"}})

	if len(rep.Candidates) != 0 {
		t.Fatalf("a refused root produced candidates %v — following a .claude symlink sweeps the user's real home", candidateRels(rep))
	}
	if len(rep.Roots) != 0 {
		t.Fatalf("Roots = %v, want none walked", rep.Roots)
	}
	reasons := map[string]string{}
	for _, p := range rep.Refused {
		reasons[p.Root] = p.Reason
	}
	if !strings.Contains(reasons[".claude"], "symlink") {
		t.Errorf("Refused[.claude] = %q, want the symlink reason", reasons[".claude"])
	}
	if !strings.Contains(reasons[".codex"], "not a directory") {
		t.Errorf("Refused[.codex] = %q, want the not-a-directory reason", reasons[".codex"])
	}
	if _, refused := reasons[".copilot"]; refused {
		t.Errorf("a MISSING root must be a no-op, not a refusal: %q", reasons[".copilot"])
	}
	// A refusal is disclosed. A `.claude` symlink in the base home means every jail on
	// this machine has been writing somewhere else.
	warnings := strings.Join(rep.Warnings(), "\n")
	for _, want := range []string{".claude", ".codex", "symlink"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("Warnings() = %q, want it to name %q", warnings, want)
		}
	}
}

// TestGranularity is §5.2's granularity rule: a directory with no kept leaf beneath it is
// ONE candidate and moves whole; a mixed directory is descended and only its runtime
// leaves move.
func TestGranularity(t *testing.T) {
	home := baseHomeFixture(t)
	writeFile(t, home, ".claude/projects/a/b/c.jsonl", strings.Repeat("x", 100))
	writeFile(t, home, ".claude/projects/a/d.jsonl", strings.Repeat("x", 50))
	writeFile(t, home, ".claude/mixed/settings.json", "{}")
	writeFile(t, home, ".claude/mixed/churn.log", strings.Repeat("y", 7))
	mkdirAll(t, home, ".claude/empty")

	rep := Detect(home, Decls{
		StateDirs:      []string{".claude"},
		ConfigSurfaces: []string{".claude/mixed/settings.json"},
	})

	whole, ok := candidate(rep, ".claude/projects")
	if !ok {
		t.Fatalf(".claude/projects is not a candidate; got %v", candidateRels(rep))
	}
	if !whole.Dir {
		t.Error(".claude/projects must be ONE whole-directory candidate, not per-leaf")
	}
	if whole.Bytes != 150 || whole.Leaves != 2 {
		t.Errorf(".claude/projects = %d bytes / %d leaves, want 150/2", whole.Bytes, whole.Leaves)
	}
	for _, rel := range []string{".claude/projects/a", ".claude/projects/a/b/c.jsonl"} {
		if _, ok := candidate(rep, rel); ok {
			t.Errorf("%s is a separate candidate; a collapsed directory moves in ONE rename", rel)
		}
	}

	if empty, ok := candidate(rep, ".claude/empty"); !ok || !empty.Dir {
		t.Errorf("an empty directory moves whole; got %+v (present=%v)", empty, ok)
	}

	if _, ok := candidate(rep, ".claude/mixed"); ok {
		t.Error(".claude/mixed holds a kept leaf, so it must be DESCENDED, not collapsed")
	}
	if leaf, ok := candidate(rep, ".claude/mixed/churn.log"); !ok || leaf.Dir || leaf.Bytes != 7 {
		t.Errorf("mixed dir's runtime leaf = %+v (present=%v), want a 7-byte leaf", leaf, ok)
	}
}

// TestUnknownTopLevelDirsAreSwept is §5.1's third root bullet — the retired/unknown-pack
// case — and its exclusion half. The exclusion is what stops the sweep proposing to
// archive `.ssh`.
func TestUnknownTopLevelDirsAreSwept(t *testing.T) {
	home := baseHomeFixture(t)
	writeFile(t, home, ".retired-agent/transcripts/a.jsonl", "x")
	writeFile(t, home, ".ssh/id_ed25519", "key")
	writeFile(t, home, ".cache/npm/blob", "c")
	writeFile(t, home, ".config/git/config", "[user]")
	writeFile(t, home, ".claude-shared-credentials/.credentials.json", "tok")
	writeFile(t, home, ".bash_history", "ls\n")
	symlink(t, home, ".gitconfig", ".config/git/config")

	d := Decls{
		StateDirs:            []string{".claude"},
		SharedDirs:           []string{".claude-shared-credentials"},
		NonPackDirs:          []string{".ssh", ".cache", ".config/git", "go"},
		SweepUnknownTopLevel: true,
	}
	rep := Detect(home, d)

	if got := rep.Roots; len(got) != 1 || got[0] != ".retired-agent" {
		t.Fatalf("Roots = %v, want only the unknown dir (.claude is absent, the rest are excluded)", got)
	}
	if _, ok := candidate(rep, ".retired-agent/transcripts"); !ok {
		t.Errorf("the retired pack's state is not a candidate; got %v", candidateRels(rep))
	}

	// With the sweep off, the same home yields nothing: the bullet is a switch, not a
	// side effect of the other two.
	d.SweepUnknownTopLevel = false
	if off := Detect(home, d); len(off.Roots) != 0 || len(off.Candidates) != 0 {
		t.Errorf("sweep off still walked %v / %v", off.Roots, candidateRels(off))
	}
}

// TestDeepNestingIsOpaqueRatherThanHalfExamined pins the depth bound's disposition. The
// entry is reported as UNCLASSIFIED (a candidate that says so) and it VETOES the
// collapse of its ancestors, which is the same disposition an unreadable entry gets —
// "reported, never silently skipped", §5.1. An apply that collapsed an ancestor here
// would move a subtree nothing ever examined.
func TestDeepNestingIsOpaqueRatherThanHalfExamined(t *testing.T) {
	home := baseHomeFixture(t)
	deep := ".claude/deep" + strings.Repeat("/x", maxDepth+2)
	writeFile(t, home, deep+"/leaf", "x")

	rep := Detect(home, Decls{StateDirs: []string{".claude"}})
	var opaque []Entry
	for _, e := range rep.Candidates {
		if e.Class == Unclassified {
			opaque = append(opaque, e)
		}
	}
	if len(opaque) == 0 {
		t.Fatalf("no UNCLASSIFIED entry reported; got %v", candidateRels(rep))
	}
	if opaque[0].Note == "" {
		t.Error("an UNCLASSIFIED entry must say why")
	}
	if _, ok := candidate(rep, ".claude/deep"); ok {
		t.Error(".claude/deep collapsed although something beneath it was never examined")
	}
	if rep.Summary() == "" || !strings.Contains(rep.Summary(), "could not be read") {
		t.Errorf("Summary() = %q, want the unreadable count disclosed", rep.Summary())
	}
}

// TestUnreadableEntryIsACandidateAndReported is §5.1's unreadable rule through the real
// mechanism (mode bits). Skipped as root, where mode bits do not deny — the depth test
// above covers the same disposition unconditionally.
func TestUnreadableEntryIsACandidateAndReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the mode bits this test needs (see configtarget_test.go for the same skip)")
	}
	home := baseHomeFixture(t)
	writeFile(t, home, ".claude/settings.json", "{}")
	locked := mkdirAll(t, home, ".claude/locked/inner")
	writeFile(t, home, ".claude/locked/inner/x", "x")
	if err := os.Chmod(filepath.Dir(locked), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(locked), 0o755) })

	rep := Detect(home, Decls{StateDirs: []string{".claude"}, ConfigSurfaces: []string{".claude/settings.json"}})
	e, ok := candidate(rep, ".claude/locked")
	if !ok {
		t.Fatalf("the unreadable dir is not a candidate; got %v", candidateRels(rep))
	}
	if e.Class != Unclassified || e.Note == "" {
		t.Errorf("unreadable entry = %+v, want UNCLASSIFIED with a note", e)
	}
}

// TestSummaryNamesStateDirsAndNothingElse is §5.8's redaction rule, and it has its own
// test because no existing disclosure in this repo honors it (noteHostCASAlias prints
// absolute paths on both sides of an arrow).
//
// The rule is not cosmetic. The base home's largest runtime tree is
// `.claude/projects/<the workspace's absolute path, mangled>`, so printing entry paths
// prints the user's directory names to every terminal a launch touches — which is exactly
// what "not workspace names, not absolute source paths" forbids.
func TestSummaryNamesStateDirsAndNothingElse(t *testing.T) {
	home := legacyFixture(t)
	rep := Detect(home, fixtureDeclsWithSweep())
	line := rep.Summary()
	if line == "" {
		t.Fatal("Summary() is empty for a fixture full of candidates")
	}
	for _, forbidden := range []string{
		home,                              // the absolute source path
		"-home-someone-code-secret-thing", // a workspace name, mangled as .claude/projects spells it
		"projects",                        // any entry path at all
		"session-store.db",
		"history.jsonl",
		"\n",
	} {
		if strings.Contains(line, forbidden) {
			t.Errorf("Summary() = %q\n must not contain %q", line, forbidden)
		}
	}
	for _, want := range []string{".claude", ".copilot", "Nothing has been moved"} {
		if !strings.Contains(line, want) {
			t.Errorf("Summary() = %q, want it to name %q", line, want)
		}
	}
}

// TestSummaryIsSilentWithNothingToDisclose pins the zero-candidate case: a clean host
// says nothing, on every launch of every jail, per noteHostCASAlias's "is there anything
// to fix?" rule.
func TestSummaryIsSilentWithNothingToDisclose(t *testing.T) {
	home := baseHomeFixture(t)
	writeFile(t, home, ".claude/settings.json", "{}")
	mkdirAll(t, home, ".copilot") // an empty ROOT is a provisioned mountpoint, not a candidate

	rep := Detect(home, Decls{
		StateDirs:      []string{".claude", ".copilot"},
		ConfigSurfaces: []string{".claude/settings.json"},
	})
	if len(rep.Candidates) != 0 {
		t.Fatalf("clean home produced candidates %v", candidateRels(rep))
	}
	if line := rep.Summary(); line != "" {
		t.Errorf("Summary() = %q, want silence", line)
	}
	if w := rep.Warnings(); len(w) != 0 {
		t.Errorf("Warnings() = %v, want silence", w)
	}
}

// TestDetectOnAMissingHomeIsANoOp — a host that has never launched a jail has no base
// home, and detection is not a reason to create one.
func TestDetectOnAMissingHomeIsANoOp(t *testing.T) {
	rep := Detect(filepath.Join(baseHomeFixture(t), "never-created"), fixtureDeclsWithSweep())
	if len(rep.Candidates) != 0 || len(rep.Roots) != 0 || len(rep.Refused) != 0 {
		t.Fatalf("missing home produced %+v", rep)
	}
	if rep.Summary() != "" {
		t.Errorf("Summary() = %q, want silence", rep.Summary())
	}
}

// TestCoreProvisionedSubdirsAreNotRenamed pins the mountpoint rule one level below a walk
// root, which is where a real host tripped it: `.pi` is a pack state dir and therefore a
// root, `.pi/agent` is core-provisioned, and EnsureGlobalStorage creates it EMPTY — so
// "a directory with no kept leaf beneath it moves whole" proposed renaming a mountpoint
// the next launch cannot recreate inside the :ro home bind.
//
// Found by running the real binary against a fresh fixture home, not by any unit test
// here: every fixture in this file that names `.pi/agent` also puts a config surface in
// it, which is exactly the shape that hides this.
func TestCoreProvisionedSubdirsAreNotRenamed(t *testing.T) {
	home := baseHomeFixture(t)
	mkdirAll(t, home, ".pi/agent") // provisioned, empty: the tripping case
	mkdirAll(t, home, ".pi/cache") // not provisioned, empty: moves whole
	writeFile(t, home, ".pi/agent/debug.log", "churn")

	d := Decls{StateDirs: []string{".pi"}, NonPackDirs: []string{".pi/agent", ".config/git"}}
	rep := Detect(home, d)

	if e, ok := candidate(rep, ".pi/agent"); ok {
		t.Errorf(".pi/agent is a candidate (%+v); it is a mountpoint, not legacy state", e)
	}
	if _, ok := candidate(rep, ".pi/agent/debug.log"); !ok {
		t.Errorf("the churn INSIDE a provisioned dir must still move; got %v", candidateRels(rep))
	}
	if _, ok := candidate(rep, ".pi/cache"); !ok {
		t.Errorf(".pi/cache must still collapse; got %v", candidateRels(rep))
	}

	// And with the dir empty — the state a fresh host is actually in.
	fresh := baseHomeFixture(t)
	mkdirAll(t, fresh, ".pi/agent")
	if r := Detect(fresh, d); len(r.Candidates) != 0 {
		t.Errorf("a freshly provisioned home produced candidates %v", candidateRels(r))
	}
}

// fakeDirEntry is a DirEntry whose Info() fails with something other than NotExist — the
// state a leaf reaches when it is present and cannot be stat'd.
type fakeDirEntry struct{ name string }

func (f fakeDirEntry) Name() string      { return f.name }
func (f fakeDirEntry) IsDir() bool       { return false }
func (f fakeDirEntry) Type() os.FileMode { return 0 }
func (f fakeDirEntry) Info() (os.FileInfo, error) {
	return nil, fmt.Errorf("stat %s: %w", f.name, os.ErrPermission)
}

// stubReadDir points the package's listing seam at fn for one test.
func stubReadDir(t *testing.T, fn func(string) ([]os.DirEntry, error)) {
	t.Helper()
	saved := readDir
	readDir = fn
	t.Cleanup(func() { readDir = saved })
}

// TestAnUnlistableRootIsRefusedNeverProposed is the §6 bottoming rule, and it is a
// DIFFERENT case from the Lstat refusals beside it: a directory can be perfectly
// stat-able and still deny a listing.
//
// The invariant is narrow and load-bearing. §5.1 says an unreadable ENTRY is a reported
// candidate, and the walk implements that — but at depth 0 the entry IS the root, so the
// general rule would name a walk root as a whole-directory candidate. The OCI runtime
// cannot mkdir inside the `:ro` base, so a later apply acting on that candidate would
// rename a mountpoint the next podman launch cannot recreate.
//
// Stubbed rather than provoked: as uid 0 a mode-0000 directory lists happily, so a
// permission fixture cannot reach this branch at all (MEASURED in this jail).
func TestAnUnlistableRootIsRefusedNeverProposed(t *testing.T) {
	home := baseHomeFixture(t)
	mkdirAll(t, home, ".claude")
	writeFile(t, home, ".claude/history.jsonl", "x")

	target := filepath.Join(home, ".claude")
	stubReadDir(t, func(name string) ([]os.DirEntry, error) {
		if name == target {
			return nil, fmt.Errorf("readdir %s: %w", name, os.ErrPermission)
		}
		return os.ReadDir(name)
	})

	rep := Detect(home, Decls{StateDirs: []string{".claude"}})

	for _, c := range rep.Candidates {
		if c.Rel == ".claude" {
			t.Errorf("the walk root .claude was proposed as a candidate (%+v); §6 forbids it "+
				"— the next podman launch cannot recreate a mountpoint inside the :ro base", c)
		}
	}
	var refused bool
	for _, r := range rep.Refused {
		if r.Root == ".claude" {
			refused = true
		}
	}
	if !refused {
		t.Errorf("an unlistable root must be REFUSED and reported, got Refused=%+v", rep.Refused)
	}
}

// TestAFailedStatCostsTheSizeNotTheClass pins P5 the right way round.
//
// Classify is a function of the PATH and the declarations, so a failed stat tells us
// nothing about what a file IS. Overwriting the class on a stat error inverted P5 for the
// files it exists to protect: a declared credential became Unclassified, and Unclassified
// means RUNTIME means archive. The cost of that mistake is a re-login on every workspace.
func TestAFailedStatCostsTheSizeNotTheClass(t *testing.T) {
	home := baseHomeFixture(t)
	mkdirAll(t, home, ".claude")

	target := filepath.Join(home, ".claude")
	stubReadDir(t, func(name string) ([]os.DirEntry, error) {
		if name == target {
			return []os.DirEntry{
				fakeDirEntry{name: ".credentials.json"},
				fakeDirEntry{name: "session-store.db"},
			}, nil
		}
		return os.ReadDir(name)
	})

	rep := Detect(home, Decls{
		StateDirs:       []string{".claude"},
		CredentialFiles: []string{".claude/.credentials.json"},
	})

	for _, c := range rep.Candidates {
		if strings.HasSuffix(c.Rel, ".credentials.json") {
			t.Errorf("a DECLARED credential was proposed for archiving because its stat failed "+
				"(%+v) — a stat error is not evidence against a declaration", c)
		}
	}
	// The other half of the same branch: an unstattable RUNTIME leaf is still a candidate,
	// so this test cannot pass by the walk simply dropping everything it cannot stat.
	var sawRuntime bool
	for _, c := range rep.Candidates {
		if strings.HasSuffix(c.Rel, "session-store.db") {
			sawRuntime = true
		}
	}
	if !sawRuntime {
		t.Error("an unstattable RUNTIME leaf must still be reported as a candidate")
	}
}
