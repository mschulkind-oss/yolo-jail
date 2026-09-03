package jailcontent

// skillsaudience_test.go is the `skills` half of briefing-audiences.md inside the jail
// (OQ-BA4: "`skills` is IN, taking the same `agents` field and every rule unchanged").
//
// It is a DIFFERENT mechanism from the briefing half, which is why it needs its own tests
// rather than riding on them: a briefing is composed per destination out of a list of texts,
// while skills are COPIED per destination out of a list of source dirs. The plan's own trap
// says so — "`skills` is not a filter either … step 7 is the same restructure as briefings,
// not a one-line predicate".
//
// The defect being closed, measured before the change: packSkillDirs is a GLOBAL list, so
// every selected pack's skills tree was copied into every destination's staging dir. A
// claude-specific skill landed in ~/.pi/agent/skills with nothing able to stop it.

import (
	"os"
	"path/filepath"
	"testing"
)

// skillTree writes a source dir holding one skill subdir named `skill`.
func skillTree(t *testing.T, skill string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(dir, skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, skill, "SKILL.md"),
		[]byte("---\nname: "+skill+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// stagedSkillNames runs the REAL PrepareSkills over the given targets and sources and returns
// {destination → the skill subdirs staged for it}.
func stagedSkillNames(t *testing.T, targets []SkillTarget, sources []PackSkillSource) map[string][]string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	SetPackSkillTargets(targets)
	SetPackSkillDirs(sources)
	t.Cleanup(func() { SetPackSkillTargets(nil); SetPackSkillDirs(nil) })

	staging, err := PrepareSkills("yolo-test-audience", t.TempDir(), nil, false)
	if err != nil {
		t.Fatalf("PrepareSkills: %v", err)
	}
	out := map[string][]string{}
	for _, target := range targets {
		entries, rerr := os.ReadDir(filepath.Join(staging, target.Staging))
		if rerr != nil {
			t.Fatalf("no staging dir for %s: %v", target.Dest, rerr)
		}
		for _, e := range entries {
			if e.IsDir() {
				out[target.Dest] = append(out[target.Dest], e.Name())
			}
		}
	}
	return out
}

// has reports whether `names` contains `want`.
func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// THE HEADLINE ASSERTION: an addressed skills source is staged only for the destination whose
// owner declared that identity. Two destinations, so "only" is a measurement.
func TestPrepareSkillsStagesAnAddressedSourceOnlyForItsAudience(t *testing.T) {
	got := stagedSkillNames(t,
		[]SkillTarget{
			{Staging: SkillStagingName("claude"), Dest: ".claude/skills", Agent: "claude"},
			{Staging: SkillStagingName("codex"), Dest: ".codex/skills", Agent: "codex"},
		},
		[]PackSkillSource{{Dir: skillTree(t, "claude-only"), Agents: []string{"claude"}}})

	if !has(got[".claude/skills"], "claude-only") {
		t.Errorf("the addressed skill never reached claude's staging dir: %v", got[".claude/skills"])
	}
	if has(got[".codex/skills"], "claude-only") {
		t.Errorf("a claude-addressed skill was copied into codex's staging dir — the whole "+
			"defect this closes (the source list is GLOBAL, so nothing else could stop it): %v",
			got[".codex/skills"])
	}
}

// P2: SILENCE MEANS BROADCAST. A source naming no audience reaches every destination — every
// pack that ships today, and the only thing a pack with no pack.json can ask for.
func TestPrepareSkillsStillBroadcastsAnUnaudiencedSource(t *testing.T) {
	got := stagedSkillNames(t,
		[]SkillTarget{
			{Staging: SkillStagingName("claude"), Dest: ".claude/skills", Agent: "claude"},
			{Staging: SkillStagingName("codex"), Dest: ".codex/skills", Agent: "codex"},
		},
		[]PackSkillSource{{Dir: skillTree(t, "everyones")}})

	for _, dest := range []string{".claude/skills", ".codex/skills"} {
		if !has(got[dest], "everyones") {
			t.Errorf("%s lost the broadcast skill: %v", dest, got[dest])
		}
	}
}

// R4: a destination that declares NO identity gets every broadcast and no addressed source.
// Not an error — it is the state every pack.json was in before the field existed.
func TestPrepareSkillsSkipsADestinationWithNoDeclaredIdentity(t *testing.T) {
	got := stagedSkillNames(t,
		[]SkillTarget{
			{Staging: SkillStagingName("claude"), Dest: ".claude/skills", Agent: "claude"},
			{Staging: SkillStagingName("silent"), Dest: ".silent/skills"},
		},
		[]PackSkillSource{
			{Dir: skillTree(t, "claude-only"), Agents: []string{"claude"}},
			{Dir: skillTree(t, "everyones")},
		})

	if has(got[".silent/skills"], "claude-only") {
		t.Errorf("an addressed source reached a destination with nothing to match against: %v",
			got[".silent/skills"])
	}
	if !has(got[".silent/skills"], "everyones") {
		t.Errorf("an unaddressable destination must still get the broadcast — declaring no "+
			"identity makes a destination unaddressable, not empty: %v", got[".silent/skills"])
	}
}

// The BUILT-IN suite is never scoped. It is core's own content, not a pack's, so it reaches
// every destination regardless of any audience — and a filter applied one layer too wide
// would have taken it out along with the pack sources.
func TestPrepareSkillsAlwaysStagesTheBuiltinSuite(t *testing.T) {
	got := stagedSkillNames(t,
		[]SkillTarget{{Staging: SkillStagingName("codex"), Dest: ".codex/skills", Agent: "codex"}},
		[]PackSkillSource{{Dir: skillTree(t, "claude-only"), Agents: []string{"claude"}}})

	if len(got[".codex/skills"]) == 0 {
		t.Fatal("codex's staging dir is EMPTY — the built-in skill suite is core's own content " +
			"and is not addressable by any pack's audience")
	}
	if has(got[".codex/skills"], "claude-only") {
		t.Errorf("the addressed pack source leaked in anyway: %v", got[".codex/skills"])
	}
}
