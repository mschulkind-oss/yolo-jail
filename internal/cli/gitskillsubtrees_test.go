package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// This regression uses the author-facing caller, not a new resolver seam. Its
// fixture is a mixed repository: selecting a skill must not activate the rest.
func TestPackLintSourcedSkillDirectory(t *testing.T) {
	remote := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = remote
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.invalid")
	writeFile(t, filepath.Join(remote, "skills/core-tools/elicitation/SKILL.md"), "---\nname: upstream\ndescription: fixture\n---\nselected marker\n")
	writeFile(t, filepath.Join(remote, "skills/core-tools/elicitation/assets/methods.csv"), "selected asset\n")
	writeFile(t, filepath.Join(remote, "skills/other/SKILL.md"), "not selected\n")
	writeFile(t, filepath.Join(remote, "hooks/start"), "never executed\n")
	writeFile(t, filepath.Join(remote, ".claude-plugin/plugin.json"), `{"name":"unselected"}`)
	writeFile(t, filepath.Join(remote, "LICENSE"), "fixture terms\n")
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	commit := git("rev-parse", "HEAD")
	pack := t.TempDir()
	writeFile(t, filepath.Join(pack, "pack.toml"), fmt.Sprintf("[[skills]]\nsource = %q\nas = \"elicitation\"\n", "git+file://"+remote+"//skills/core-tools/elicitation?ref="+commit))
	rc, report := lintReport(t, pack)
	if rc != 0 {
		t.Fatalf("offline lint rejected sourced skill declaration (contents need not be fetched):\n%s", report)
	}
	if !strings.Contains(report, "contents") || !strings.Contains(report, "not verified") {
		t.Fatalf("offline lint must disclose unverified contents:\n%s", report)
	}
}
