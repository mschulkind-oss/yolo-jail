package integration

// packaudience_test.go is docs/reference/agent-briefings.md#audiences-what-varies-per-destination end to end in a real container: an ADDRESSED
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
//
// THE `file://` ENTRIES CARRY NO `name`, and that is not laziness. A bare `"file://<dir>"`
// entry takes its name from the source directory's BASENAME — never from the pack.json
// `name`, which is informational (packload.Pack's Name field comment has the three jobs the
// effective name has to do). These fixtures stage under a directory named `house-rules`
// while their manifests say `house`, so the refusal below naming `house-rules` is the
// end-to-end measurement of that rule rather than a fixture detail.
//
// The entries were NAMED `house` here from 2026-09-03 to 2026-09-05, as a workaround for
// what looked like a defect: t.TempDir() hands out numeric names (`.../TestName/002`), so
// the refusal read `pack 002`, and Pack.Name's docstring promised the manifest would win.
// The docstring was wrong, not the code. Giving the fixture a real directory name is what
// removes the confusion without hiding the behaviour.

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

	pack := filepath.Join(t.TempDir(), "house-rules")
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

// TestPackAudienceRoutesBriefingFilesPerAgent is the `briefing/` convention's routing in a
// launched jail (docs/reference/pack-system.md#briefing, #briefing-governance): ONE pack whose
// `briefing/` directory holds three files, two of them addressed by manifest lines and one named
// by nothing, beside a root AGENTS.md.
//
// Each file tests a different rule, and the four together are why this is one launch rather than
// four:
//
//   - `briefing/shared.md` is named by NO contribution, so it broadcasts implicitly — and it must
//     keep broadcasting although the same pack declares two narrow deliveries (P3 (briefing
//     defaults), "declarations add; they never subtract"). That is the trap R5 (briefing
//     defaults) records: every notch once kept its own "does this pack declare anything of this
//     kind?" gate, and declaring one narrow delivery switched the pack's whole implicit
//     broadcast off.
//   - `briefing/claude.md` and `briefing/codex.md` are each addressed to one agent by `agents`, so
//     each must reach its own agent's file and not the other one. The FILENAME is not what routes
//     them: "a filename never addresses" (#briefing-non-goals), so these fixtures are named after
//     their audience only to make a misrouting readable in the failure output.
//   - the root `AGENTS.md` is the pack repository's own instructions and is never pack prose (P1
//     (briefing defaults)), so its text must reach neither agent.
//
// It also asserts the documented ORDER inside one destination: a pack's delivered files form one
// section, byte-wise by pack-relative path, and a file routed elsewhere does not split it. So in
// both destinations the addressed file (`briefing/claude.md`, `briefing/codex.md`) precedes
// `briefing/shared.md`.
//
// No agent is started: the assertions read the two composed briefing files the launch wrote.
func TestPackAudienceRoutesBriefingFilesPerAgent(t *testing.T) {
	requireJail(t)

	const (
		sharedText = "SHAREDBRIEFING every agent in this jail reads this"
		claudeText = "CLAUDEBRIEFING only claude reads this"
		codexText  = "CODEXBRIEFING only codex reads this"
		repoText   = "REPOINSTRUCTIONS for contributors to this pack repository"
	)
	// A real directory name, for the reason the file header gives: the effective pack name is
	// the source directory's basename.
	pack := filepath.Join(t.TempDir(), "house-rules")
	if err := os.MkdirAll(filepath.Join(pack, "briefing"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"briefing/shared.md": sharedText,
		"briefing/claude.md": claudeText,
		"briefing/codex.md":  codexText,
		"AGENTS.md":          repoText,
	} {
		if err := os.WriteFile(filepath.Join(pack, filepath.FromSlash(rel)), []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Two addressed contributions and NOTHING for shared.md: naming it would make it a declared
	// delivery, and the implicit broadcast is the half this test exists to keep honest.
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(
		`{"name":"house","description":"per-agent house rules","contributes":[`+
			`{"kind":"briefing","from":"briefing/claude.md","agents":["claude"]},`+
			`{"kind":"briefing","from":"briefing/codex.md","agents":["codex"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "codex", "file://`+pack+`"]}`)

	// Both files are printed whole and fenced, and every assertion is made here rather than with
	// a shell exit code: a missing FILE and absent TEXT are different defects, and a failure is
	// only diagnosable with the composed briefing in front of the reader.
	const (
		claudeFile = "/home/agent/.claude/CLAUDE.md"
		codexFile  = "/home/agent/.codex/AGENTS.md"
		absent     = "__BRIEFING_FILE_ABSENT__"
	)
	var script []string
	for _, f := range []string{claudeFile, codexFile} {
		script = append(script,
			`echo "=== BEGIN `+f+` ==="`,
			`cat `+f+` 2>/dev/null || echo `+absent,
			`echo "=== END `+f+` ==="`)
	}
	r := runYolo(t, dir, strings.Join(script, "; "))
	if r.rc != 0 {
		t.Fatalf("launch failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}

	cases := []struct {
		agent, file, routed, notRouted string
	}{
		{"claude", claudeFile, claudeText, codexText},
		{"codex", codexFile, codexText, claudeText},
	}
	for _, c := range cases {
		body := section(r.stdout, "=== BEGIN "+c.file+" ===", "=== END "+c.file+" ===")
		if strings.TrimSpace(body) == "" || strings.Contains(body, absent) {
			t.Errorf("%s's briefing %s was not written at all — every assertion below would be "+
				"vacuous\nstdout: %s\nstderr: %s", c.agent, c.file, r.stdout, r.stderr)
			continue
		}
		if !strings.Contains(body, sharedText) {
			t.Errorf("%s did not receive briefing/shared.md, which no contribution names and so "+
				"broadcasts implicitly — declaring two addressed files switched the pack's "+
				"implicit broadcast off (P3 (briefing defaults); the trap R5 (briefing "+
				"defaults) records):\n%s", c.agent, body)
		}
		if !strings.Contains(body, c.routed) {
			t.Errorf("%s did not receive the file addressed to it (`agents: [%q]`):\n%s",
				c.agent, c.agent, body)
		}
		if strings.Contains(body, c.notRouted) {
			t.Errorf("%s received the file addressed to the OTHER agent — an `agents` audience "+
				"must reach the agent it names and nothing else:\n%s", c.agent, body)
		}
		if strings.Contains(body, repoText) {
			t.Errorf("%s received the pack's root AGENTS.md, which is the pack repository's own "+
				"instructions and never pack prose (P1 (briefing defaults)):\n%s", c.agent, body)
		}
		// The order is only meaningful once both halves are present; their absence is already
		// reported above.
		if i, j := strings.Index(body, c.routed), strings.Index(body, sharedText); i >= 0 && j >= 0 && i > j {
			t.Errorf("%s's section is out of order: its addressed file must precede "+
				"briefing/shared.md, a pack's files being ordered byte-wise by pack-relative path "+
				"(docs/reference/pack-system.md#briefing):\n%s", c.agent, body)
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

	// The directory basename, NOT the manifest's `name`, is what the refusal must print —
	// see the file header. `house-rules` is distinctive enough that matching it in the
	// combined output cannot happen by accident, which a bare t.TempDir() number is not.
	pack := filepath.Join(t.TempDir(), "house-rules")
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
	// `house-rules` is the SOURCE DIRECTORY's name; the pack.json says `house`. Asserting
	// the former is what makes this the end-to-end pin of where an effective name comes
	// from — a launch that printed `house` would mean the manifest had started winning,
	// and the staging dir, the prune key and the /ctx mount path would each need to move
	// with it (run.TestConfiguredPackNameComesFromTheAddressNotTheManifest).
	for _, want := range []string{"codex", "house-rules", "Agents your `packs` provide"} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("refusal missing %q — it has to name the string, the pack and the "+
				"candidates:\n%s", want, r.combined())
		}
	}
}
