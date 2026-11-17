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

func TestHostApplySourcedSkillUsesGroupedSelection(t *testing.T) {
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
	writeFile(t, filepath.Join(repo, "skills/second/SKILL.md"), "second\n")
	writeFile(t, filepath.Join(repo, "VERSION"), "payload-v1\n")
	writeFile(t, filepath.Join(repo, "shared/data"), "sibling-v1\n")
	script := filepath.Join(repo, "skills/first/scripts/probe.sh")
	writeFile(t, script, "#!/bin/sh\nset -eu\nhere=$(dirname \"$0\")\nprintf '%s\\n' \"$(cat \"$here/../../../VERSION\")\" \"$(cat \"$here/../../../shared/data\")\"\n")
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	c1 := git("rev-parse", "HEAD")
	first, second, reader := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(first, "pack.toml"), fmt.Sprintf("[[skills]]\nsource = %q\nas = \"first\"\n", "git+file://"+repo+"//skills/first?ref=main"))
	writeFile(t, filepath.Join(second, "pack.toml"), fmt.Sprintf("[[skills]]\nsource = %q\nas = \"second\"\n", "git+file://"+repo+"//skills/second?ref=main"))
	writeFile(t, filepath.Join(reader, "pack.toml"), "[[skills]]\nagent = \"fixture\"\ninto = \".fixture/skills\"\n")
	config := fmt.Sprintf(`{"packs":[{"name":"first","source":%q},{"name":"second","source":%q},{"name":"reader","source":%q}]}`, "file://"+first, "file://"+second, "file://"+reader)
	cfgPath := filepath.Join(home, ".config/yolo-jail/config.jsonc")
	writeFile(t, cfgPath, config)
	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("host apply rc=%d:\n%s", rc, report)
	}
	exposed := filepath.Join(home, ".fixture/skills/first/scripts/probe.sh")
	if out, err := exec.Command(exposed).CombinedOutput(); err != nil || string(out) != "payload-v1\nsibling-v1\n" {
		t.Fatalf("exposed host script: %q %v\n%s", out, err, report)
	}
	// Later row in a separate selected pack invalidates the entire moving pair.
	if err := os.Remove(filepath.Join(repo, "skills/second/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "skills/second/data"), "marker absent\n")
	writeFile(t, filepath.Join(repo, "VERSION"), "invalid-tip\n")
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-m", "invalid later row")
	c2 := git("rev-parse", "HEAD")
	store := &packsrc.Store{Dir: paths.PacksDir()}
	addr, _ := packsrc.Parse("git+file://" + repo + "//skills/first?ref=main")
	_ = addr
	// Remove the existing pair's pacing stamp to exercise the real host writer's due path.
	if err := os.RemoveAll(filepath.Join(store.Dir, "stamps")); err != nil {
		t.Fatal(err)
	}
	rc, report = applyWith(t, true, strings.NewReader(""))
	if rc != 0 || !strings.Contains(report, "skills[0]") || !strings.Contains(report, c2) {
		t.Fatalf("fallback host apply rc=%d:\n%s", rc, report)
	}
	data, err := os.ReadFile(packsrc.SkillsLockPath(cfgPath))
	if err != nil || !strings.Contains(string(data), c1) || strings.Contains(string(data), c2) {
		t.Fatalf("record advanced: %q %v", data, err)
	}
	if out, err := exec.Command(exposed).CombinedOutput(); err != nil || string(out) != "payload-v1\nsibling-v1\n" {
		t.Fatalf("mixed tip delivery: %q %v", out, err)
	}
	t.Cleanup(func() {
		_ = filepath.Walk(store.Dir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(p, fi.Mode().Perm()|0700)
			}
			return nil
		})
	})
}
