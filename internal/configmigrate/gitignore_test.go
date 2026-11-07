package configmigrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInit makes ws a real git work tree whose .gitignore holds ignore, isolated from the user's
// own git config and global excludes file.
func gitInit(t *testing.T, ws, ignore string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("XDG_CONFIG_HOME", realDir(t))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(ws))
	if out, err := exec.Command("git", "-C", ws, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if ignore != "" {
		write(t, filepath.Join(ws, ".gitignore"), ignore)
	}
}

func checkIgnored(t *testing.T, ws, name string) bool {
	t.Helper()
	return exec.Command("git", "-C", ws, "check-ignore", "-q", "--", name).Run() == nil
}

func migrate(t *testing.T, ws string) (*MigrationPlan, *ApplyResult) {
	t.Helper()
	p := plan(t, Request{Workspace: ws})
	res, err := Apply(p, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return p, res
}

// readExclude returns the clone's info/exclude, "" when there is none.
func readExclude(t *testing.T, ws string) string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude"))
	return string(data)
}

// A private local file that .gitignore ignored under its old name is NOT made ignored under its
// new one: the migration writes no ignore file — not .gitignore, not .git/info/exclude — and says
// the one line to add (RC-D71, superseding RC-D33).
func TestMigrateNamesTheIgnoreLineAndWritesNoIgnoreFile(t *testing.T) {
	env(t)
	ws := realDir(t)
	const gitignore = "yolo-jail.local.jsonc\n"
	gitInit(t, ws, gitignore)
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), "{\"packages\": [\"just\"]}\n")
	write(t, filepath.Join(ws, "yolo-jail.local.jsonc"), "{\"mise_tools\": {\"lua\": \"5.4.9\"}}\n")
	excludeBefore := readExclude(t, ws)

	p, res := migrate(t, ws)
	want := "yolo-jail.local.toml is not ignored by git as yolo-jail.local.jsonc was; nothing was added to any ignore file. " +
		"To keep it out of commits, add the line `/yolo-jail.local.toml` to .gitignore or .git/info/exclude"
	if tr := p.Transcript(); !strings.Contains(tr, want) {
		t.Fatalf("transcript lacks %q:\n%s", want, tr)
	}
	backup := "yolo-jail.local.jsonc.pre-migrate is not ignored by git as yolo-jail.local.jsonc was"
	if tr := p.Transcript(); !strings.Contains(tr, backup) || strings.Contains(tr, "yolo-jail.jsonc.pre-migrate is not ignored") {
		t.Fatalf("transcript lacks the private backup's line, or names the tracked file's backup:\n%s", tr)
	}
	if got := readExclude(t, ws); got != excludeBefore {
		t.Fatalf("the migration wrote .git/info/exclude:\nbefore %q\nafter  %q", excludeBefore, got)
	}
	if data, _ := os.ReadFile(filepath.Join(ws, ".gitignore")); string(data) != gitignore {
		t.Fatalf(".gitignore changed: %q", data)
	}
	if checkIgnored(t, ws, "yolo-jail.local.toml") {
		t.Fatal("yolo-jail.local.toml became ignored: something wrote an ignore rule")
	}
	if len(res.Ignores) != 2 || res.Ignores[0].Report(ws) != want+"." {
		t.Fatalf("result ignores = %+v", res.Ignores)
	}
}

// A file that was not ignored before is not made ignored: nothing is added anywhere.
func TestMigrateLeavesANotIgnoredFileAlone(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitInit(t, ws, "")
	write(t, filepath.Join(ws, "yolo-jail.local.jsonc"), "{\"mise_tools\": {\"lua\": \"5.4.9\"}}\n")

	p, res := migrate(t, ws)
	if len(p.Ignores) != 0 || len(res.Ignores) != 0 {
		t.Fatalf("ignores planned for a file git never ignored: %+v %+v", p.Ignores, res.Ignores)
	}
	if strings.Contains(p.Transcript(), "info/exclude") {
		t.Fatalf("transcript mentions info/exclude:\n%s", p.Transcript())
	}
	if data, _ := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude")); strings.Contains(string(data), "yolo-jail") {
		t.Fatalf("info/exclude gained a line: %q", data)
	}
}

// With no git work tree, the migration still runs and says in one line what to add to .gitignore.
func TestMigrateWithoutGitNamesTheIgnoreLine(t *testing.T) {
	env(t)
	ws := realDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(ws))
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), "{\"packages\": [\"just\"]}\n")
	write(t, filepath.Join(ws, "yolo-jail.local.jsonc"), "{\"mise_tools\": {\"lua\": \"5.4.9\"}}\n")

	p, res := migrate(t, ws)
	want := "yolo-jail.local.toml is not ignored by git (git could not be asked here); to keep it out of commits, add the line `yolo-jail.local.toml` to .gitignore"
	tr := p.Transcript()
	if strings.Count(tr, want) != 1 {
		t.Fatalf("transcript lacks the one hint %q:\n%s", want, tr)
	}
	if strings.Contains(tr, "yolo-jail.toml is not ignored") {
		t.Fatalf("the project file got an ignore hint:\n%s", tr)
	}
	if !exists(filepath.Join(ws, "yolo-jail.local.toml")) {
		t.Fatal("the migration did not write yolo-jail.local.toml")
	}
	backupHint := "yolo-jail.local.jsonc.pre-migrate is not ignored by git (git could not be asked here); to keep it out of commits, add the line `yolo-jail.local.jsonc.pre-migrate` to .gitignore"
	if strings.Count(tr, backupHint) != 1 || strings.Contains(tr, "yolo-jail.jsonc.pre-migrate is not ignored") {
		t.Fatalf("transcript lacks the one backup hint %q for the local file only:\n%s", backupHint, tr)
	}
	if len(res.Ignores) != 2 || res.Ignores[0].Report(ws) != want+"." {
		t.Fatalf("result ignores = %+v", res.Ignores)
	}
}
