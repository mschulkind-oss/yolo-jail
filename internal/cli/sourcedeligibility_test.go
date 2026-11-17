package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func TestPackInstallDoesNotHideUnresolvedSourceOnlyDeclaringPack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local/share"))
	repo := t.TempDir()
	source := "git+file://" + repo + "//skills/chosen?ref=main"
	writeFile(t, filepath.Join(repo, "pack.toml"), fmt.Sprintf("[[skills]]\nsource = %q\nas = \"chosen\"\n", source))
	writeFile(t, filepath.Join(repo, "skills/chosen/SKILL.md"), "chosen")
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(repo, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "--all"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	cfg := filepath.Join(home, ".config/yolo-jail/config.jsonc")
	writeFile(t, cfg, fmt.Sprintf(`{"packs":[{"name":"only-source","source":%q}]}`, "git+file://"+repo+"?ref=main"))
	var out, errout bytes.Buffer
	rc := packMain([]string{"install"}, &out, &errout, false)
	if rc != 1 || !strings.Contains(out.String()+errout.String(), "sourced skills selection incomplete") {
		t.Fatalf("source-only unresolved hidden: %d %s %s", rc, out.String(), errout.String())
	}
	if _, err := os.Lstat(packsrc.SkillsLockPath(cfg)); !os.IsNotExist(err) {
		t.Fatalf("incomplete selection published record: %v", err)
	}
}
