package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Host apply --assert must preserve this invocation's validated sourced result
// when a confirmed adoption reloads the selected packs.
func TestHostApplySourcedAdoptionReloadKeepsInvocationResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local/share"))
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	writeFile(t, filepath.Join(repo, "skills/first/SKILL.md"), "---\nname: raw\n---\nfirst\n")
	writeFile(t, filepath.Join(repo, "VERSION"), "payload-v1\n")
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	first, reader := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(first, "pack.toml"), fmt.Sprintf("[[skills]]\nsource = %q\nas = \"first\"\n", "git+file://"+repo+"//skills/first?ref=main"))
	writeFile(t, filepath.Join(reader, "pack.toml"), "[[skills]]\nagent = \"fixture\"\ninto = \".fixture/skills\"\n")
	config := fmt.Sprintf(`{"packs":[{"name":"first","source":%q},{"name":"reader","source":%q}]}`, "file://"+first, "file://"+reader)
	writeFile(t, filepath.Join(home, ".config/yolo-jail/config.jsonc"), config)
	// The user's own, never-composed skill in the destination: the adoption gate's input.
	writeFile(t, filepath.Join(home, ".fixture/skills/mine/SKILL.md"), "---\nname: mine\n---\nuser skill\n")
	t.Cleanup(func() {
		_ = filepath.Walk(paths.PacksDir(), func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(p, fi.Mode().Perm()|0700)
			}
			return nil
		})
	})
	rc, report := applyWith(t, true, strings.NewReader("y\ny\n"))
	t.Logf("first apply rc=%d\n%s", rc, report)
	_, markerErr := os.Lstat(filepath.Join(home, ".fixture/skills/first/SKILL.md"))
	if rc != 0 || markerErr != nil {
		rc2, report2 := applyWith(t, true, strings.NewReader("y\ny\n"))
		_, marker2 := os.Lstat(filepath.Join(home, ".fixture/skills/first/SKILL.md"))
		t.Fatalf("adoption run: rc=%d registration err=%v; immediate rerun rc=%d registration err=%v\nRERUN:\n%s", rc, markerErr, rc2, marker2, report2)
	}
}
