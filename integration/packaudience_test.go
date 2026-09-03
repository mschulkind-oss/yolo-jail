package integration

// packaudience_test.go is briefing-audiences.md end to end in a real container: an ADDRESSED
// contribution reaches the agent it named and NOTHING ELSE.
//
// It is here rather than only in unit tests because the jail's two halves live in different
// functions and different packages: refreshJailBriefings COMPOSES one file per destination and
// assembleRunCmd MOUNTS it, PrepareSkills COPIES per destination and the argv binds the
// staging dir. A unit test can pin either side, and one does pin the pair on the filesystem
// (run.TestJailBriefingStagingNameAgreesWithTheMount) — but only a container can answer "is
// the prose in the file the agent will actually open, and absent from the other one".
//
// TWO agent packs are the whole fixture design. "Reached only its audience" is not a
// measurement in a one-agent jail: every possible bug looks like a pass.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackAudienceDeliversToOneAgentOnly selects `claude` and `codex` plus one content pack
// whose briefing prose and skills tree are both addressed to claude. It asserts the positive
// and the negative for each kind, in one container.
func TestPackAudienceDeliversToOneAgentOnly(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	// An ADDRESSED briefing: it names its audience and no path, because where claude reads is
	// the claude pack's business (P4). Its source is a file of its own, so the assertion is
	// about routing rather than about the conventional AGENTS.md.
	if err := os.MkdirAll(filepath.Join(pack, "prose"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "prose", "claude.md"),
		[]byte("CLAUDEONLY prefer rg over grep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An ADDRESSED skills tree, same rule, different mechanism (copy-per-destination rather
	// than compose-per-destination) — which is why both are asserted here.
	skill := filepath.Join(pack, "skills", "claude-only-demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"),
		[]byte("---\nname: claude-only-demo\ndescription: addressed to claude\n---\n# Demo\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(
		`{"name":"house","description":"addressed house rules","contributes":[`+
			`{"kind":"briefing","from":"prose/claude.md","agents":["claude"]},`+
			`{"kind":"skills","from":"skills","agents":["claude"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "codex", "file://`+pack+`"]}`)

	// One command, four facts. `rg -c` exits non-zero on no match, so the negatives are
	// spelled as explicit `|| echo`, not as an exit code — a bare `!rg` would make a missing
	// FILE indistinguishable from absent content, and the codex briefing exists either way.
	r := runYolo(t, dir, strings.Join([]string{
		`rg -c CLAUDEONLY /home/agent/.claude/CLAUDE.md && echo BRIEFING_REACHED_CLAUDE`,
		`rg -c CLAUDEONLY /home/agent/.codex/AGENTS.md || echo BRIEFING_SKIPPED_CODEX`,
		`ls -d /home/agent/.claude/skills/claude-only-demo && echo SKILL_REACHED_CLAUDE`,
		`ls -d /home/agent/.codex/skills/claude-only-demo || echo SKILL_SKIPPED_CODEX`,
		// The base briefing is NOT scoped — only pack prose is — so codex must still have
		// been briefed. Without this, "the audience filter deleted codex's whole briefing"
		// would read as a pass.
		`rg -c 'Jail Environment' /home/agent/.codex/AGENTS.md && echo CODEX_STILL_BRIEFED`,
	}, "; "))

	for _, want := range []string{
		"BRIEFING_REACHED_CLAUDE",
		"BRIEFING_SKIPPED_CODEX",
		"SKILL_REACHED_CLAUDE",
		"SKILL_SKIPPED_CODEX",
		"CODEX_STILL_BRIEFED",
	} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("missing %s — an addressed contribution must reach the agent it named and "+
				"nothing else, at both kinds, while leaving every unaddressed layer alone\n"+
				"rc %d\nstdout: %s\nstderr: %s", want, r.rc, r.stdout, r.stderr)
		}
	}
}

// TestPackAudienceRefusesAnAgentTheJailDoesNotHave is P3 in a real launch: the jail must
// REFUSE rather than start with prose addressed to nobody.
//
// It belongs here because the refusal's whole value is that the launch stops, and "the launch
// stops" is a container fact. The unit tests assert the message; this asserts the consequence.
func TestPackAudienceRefusesAnAgentTheJailDoesNotHave(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pack, "prose"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "prose", "x.md"), []byte("rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// `codex` is a real agent and a real shipped pack — it is simply not selected below, which
	// under P3 is the same mistake as a typo and earns the same refusal.
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(
		`{"name":"house","description":"h","contributes":[`+
			`{"kind":"briefing","from":"prose/x.md","agents":["codex"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "file://`+pack+`"]}`)

	r := runYolo(t, dir, "true")
	if r.rc == 0 {
		t.Fatalf("the launch STARTED with prose addressed to an agent this jail does not have; "+
			"a silently inert selector is indistinguishable from a working one\n%s", r.combined())
	}
	for _, want := range []string{"codex", "house", "Agents your `packs` provide"} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("refusal missing %q — it has to name the string, the pack and the "+
				"candidates:\n%s", want, r.combined())
		}
	}
}
