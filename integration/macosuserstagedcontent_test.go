package integration

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
)

// Item 4 of docs/plans/runbooks/macos-user-manual-checks.md, unattended: content actually
// reached the agent. The install is unit-covered; what only a launch shows is that the launch
// composes and stages it FOR REAL, through sudo, into the sandbox home the agent reads.
//
// ⚠ IT ASSERTS A NAMED SUBSET, NEVER A COUNT (§0.5). The item's two hand runs recorded fourteen
// and thirteen skills and both were right, and the set has changed since: commit 66ad2a48d
// removed the source-tree-only skill the second run was keyed on, and the built-ins are now
// the two on-demand skills. A count would fail on every such change and say nothing about
// whether staging works. What must hold on every launch with the claude pack selected is that
// both built-ins arrive and the briefing is yolo's.
//
// It reads through ~/.claude, the path the agent reads, never the sidecar spelling.
func TestMacosUserStagedSkillsAndBriefingReachTheAgent(t *testing.T) {
	requireMacosUser(t)
	// The claude pack is what makes ~/.claude a skills and briefing destination; without a
	// pack there is no destination and nothing to assert.
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)

	r := runMacosUser(t, ws, macosUserStagedContentProbe())
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END STAGED ===") {
		t.Fatalf("the macos-user launch did not run its staged-content probe (rc %d), so "+
			"nothing below can be read as item 4's answer.\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	skills, title := parseStagedContent(section(r.stdout, "=== STAGED ===", "=== END STAGED ==="))
	for _, want := range macosUserStagedSkillsWant {
		if !skills[want] {
			t.Errorf("built-in skill %q did not reach ~/.claude/skills/%s/SKILL.md "+
				"(item 4). Staged: %v\nfull output:\n%s", want, want, stagedNames(skills), r.stdout)
		}
	}
	if !strings.HasPrefix(title, macosUserBriefingTitlePrefix) {
		t.Errorf("~/.claude/CLAUDE.md opens with %q, want a line starting %q (item 4: the "+
			"briefing yolo composed, not an empty or foreign file)\nfull output:\n%s",
			title, macosUserBriefingTitlePrefix, r.stdout)
	}
}

// macosUserStagedSkillsWant is spelled out rather than read from the embedded skills, for
// macosUserHomeTierWantLinks' reason: a list derived from the code under test is satisfied by
// whatever that code says. TestMacosUserStagedContentProbeReadsAFixture keeps it honest.
var macosUserStagedSkillsWant = []string{"configuring-the-jail", "diagnosing-the-jail"}

// macosUserBriefingTitlePrefix is the opening every composed briefing has; the setup name after
// it is the launch's, and DP-B19 in macosuserdeclparity_test.go asserts it whole.
const macosUserBriefingTitlePrefix = "# YOLO Environment — setup "

// macosUserStagedContentProbe prints one `skill|<name>` line per skill directory that holds a
// readable SKILL.md, and `title|<first line>` of the briefing (`title|NONE` when it cannot be
// read or is empty).
func macosUserStagedContentProbe() string {
	return strings.Join([]string{
		`echo "=== STAGED ==="`,
		`for f in "$HOME"/.claude/skills/*/SKILL.md; do if [ -r "$f" ]; then echo "skill|$(basename "$(dirname "$f")")"; fi; done`,
		`t=$(head -n 1 "$HOME/.claude/CLAUDE.md" 2>/dev/null) || t=NONE`,
		`echo "title|${t:-NONE}"`,
		`echo "=== END STAGED ==="`,
	}, "\n")
}

// parseStagedContent reads the probe's fenced body: the set of staged skill names and the
// briefing's first line.
func parseStagedContent(body string) (map[string]bool, string) {
	skills := map[string]bool{}
	title := ""
	for _, line := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok {
			continue
		}
		switch k {
		case "skill":
			skills[v] = true
		case "title":
			title = v
		}
	}
	return skills, title
}

func stagedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestMacosUserStagedContentProbeReadsAFixture is the Linux preflight of the probe above: the
// script run UNSANDBOXED against a home laid out with every embedded built-in skill and a
// briefing, and the parser reading what it printed. It proves the script, the parser and the
// spelled-out list (each wanted name must be a real embedded skill), never the staging. Not
// behind requireMacosUser, for the content preflight's reason: counting it would let the macOS
// job pass its "something ran" check with no sandbox started.
func TestMacosUserStagedContentProbeReadsAFixture(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH")
	}
	embedded := map[string]bool{}
	entries, err := fs.ReadDir(builtinskills.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, err := fs.Stat(builtinskills.FS, e.Name()+"/SKILL.md"); err == nil {
			embedded[e.Name()] = true
		}
	}
	for _, want := range macosUserStagedSkillsWant {
		if !embedded[want] {
			t.Errorf("macosUserStagedSkillsWant names %q, which is not an embedded built-in "+
				"skill (embedded: %v); the Mac test would fail for the list, not the launch",
				want, stagedNames(embedded))
		}
	}

	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name := range embedded {
		p := filepath.Join(home, ".claude", "skills", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("skill"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory without a SKILL.md is not a staged skill and must not be reported as one.
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	briefing := filepath.Join(home, ".claude", "CLAUDE.md")
	title := macosUserBriefingTitlePrefix + "seatbelt-yolo-account"
	if err := os.WriteFile(briefing, []byte(title+"\n\nprose\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func() string {
		t.Helper()
		cmd := exec.Command("bash", "-c", macosUserStagedContentProbe())
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the probe did not run cleanly: %v\n%s", err, out)
		}
		return string(out)
	}

	out := run()
	skills, got := parseStagedContent(section(out, "=== STAGED ===", "=== END STAGED ==="))
	if strings.Join(stagedNames(skills), ",") != strings.Join(stagedNames(embedded), ",") {
		t.Errorf("probe reported skills %v, want exactly %v\n%s", stagedNames(skills), stagedNames(embedded), out)
	}
	if got != title {
		t.Errorf("probe reported title %q, want %q\n%s", got, title, out)
	}

	// With no briefing at all the probe must say so rather than print an empty title.
	if err := os.Remove(briefing); err != nil {
		t.Fatal(err)
	}
	out = run()
	if _, got = parseStagedContent(section(out, "=== STAGED ===", "=== END STAGED ===")); got != "NONE" {
		t.Errorf("with no briefing the probe reported title %q, want NONE\n%s", got, out)
	}
}
