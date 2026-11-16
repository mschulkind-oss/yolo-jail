package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImportedSkillsReachJail exercises the fresh-launch resolver through the normal approved
// Git scope and verifies the selected commit bytes at the existing workspace-skills destination.
// Its upstream is a synthetic bare repository served by the fixture's local fake SSH executable.
func TestImportedSkillsReachJail(t *testing.T) {
	workspaceConfig := `{"skill_imports":{"acme":{"git":"git@fixture.test:o/r.git","ref":"main","path":"skills","only":["review"]}}}`
	userConfig := `{"packs":["git","claude"],"brokered":{"git":{"grants":[` + gitReadGrant + `]}}}`
	fx := newGitBrokerFixtureWith(t, userConfig, workspaceConfig)
	seedImportedSkillCommit(t, fx)
	fx.recordScope(t, "git@fixture.test:o/r.git")

	script := `test "$(cat "$HOME/.claude/skills/review/SKILL.md")" = "review from brokered commit"
` +
		`test ! -e "$HOME/.claude/skills/other"
` +
		`test ! -e "$HOME/.claude/skills/README.md"
` +
		`test ! -e "$HOME/.claude/skills/pack.toml"`
	result := runCommand(t, fx.dir, []string{"run", "--", "bash", "-lc", script}, fx.opts...)
	if result.rc != 0 {
		t.Fatalf("fresh launch did not stage only the selected imported skill (rc=%d):\n%s\n%s",
			result.rc, result.combined(), gitBrokerFixtureDiagnostics(t, fx))
	}
	if argv, err := os.ReadFile(fx.argvLog); err != nil || !strings.Contains(string(argv), "git-upload-pack") {
		t.Fatalf("the host Git broker did not use the fixture's SSH upstream: argv=%q err=%v\nlaunch output:\n%s", argv, err, result.combined())
	}
}

func seedImportedSkillCommit(t *testing.T, fx gitBrokerFixture) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	hostGit(t, t.TempDir(), "clone", "-q", fx.bare, work)
	for path, body := range map[string]string{
		"skills/review/SKILL.md": "review from brokered commit",
		"skills/other/SKILL.md":  "other skill is not selected",
		"README.md":              "repository root is not a skill",
		"pack.toml":              "pack manifests are never interpreted",
	} {
		full := filepath.Join(work, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hostGit(t, work, "add", "-A")
	hostGit(t, work, "-c", "user.name=it", "-c", "user.email=it@example.test", "-c", "commit.gpgsign=false",
		"commit", "-q", "-m", "skills")
	hostGit(t, work, "push", "origin", "HEAD:refs/heads/main")
}
