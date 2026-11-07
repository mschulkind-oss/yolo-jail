package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// captureDispatchRCIn runs a command through the real dispatcher from dir.
func captureDispatchRCIn(t *testing.T, dir string, args []string) (int, string, string) {
	t.Helper()
	t.Chdir(dir)
	return captureDispatchRC(t, args)
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("no usable git here: %v: %s", err, out)
	}
	return dir
}

// `yolo init` writes the pointer file and nothing else: the project's .gitignore and the clone's
// .git/info/exclude bytes are the same before and after (the owner, 2026-11-07: yolo does not
// edit your git settings for you; .yolo/ ignores itself).
func TestInitWritesThePointerFileOnly(t *testing.T) {
	dir := initRepo(t)
	gitignore := filepath.Join(dir, ".gitignore")
	const before = "node_modules/\n"
	must(t, os.WriteFile(gitignore, []byte(before), 0o644))
	exclusion := filepath.Join(dir, ".git", "info", "exclude")
	excludeBytes, _ := os.ReadFile(exclusion)
	excludeBefore := string(excludeBytes)
	rc, stdout, _ := captureDispatchRCIn(t, dir, []string{"init"})
	if rc != 0 || !strings.Contains(stdout, "Created yolo-jail.toml") {
		t.Fatalf("init rc=%d stdout=%q", rc, stdout)
	}
	body, err := os.ReadFile(filepath.Join(dir, "yolo-jail.toml"))
	if err != nil {
		t.Fatalf("init wrote no yolo-jail.toml: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 3 {
		t.Errorf("pointer file is %d lines, want three:\n%s", len(lines), body)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "#") {
			t.Errorf("pointer line is not a comment (a default would be scaffolded): %q", l)
		}
	}
	for _, want := range []string{"docs.yolo-jail.mschulkind.dev", "yolo config set"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("pointer file never says %q:\n%s", want, body)
		}
	}
	if got, _ := os.ReadFile(gitignore); string(got) != before {
		t.Errorf(".gitignore changed: %q", got)
	}
	if got, _ := os.ReadFile(exclusion); string(got) != excludeBefore {
		t.Errorf("yolo init wrote .git/info/exclude:\nbefore %q\nafter  %q", excludeBefore, got)
	}
	if strings.Contains(stdout, "info/exclude") {
		t.Errorf("init still talks about info/exclude:\n%s", stdout)
	}
}

// Outside a work tree there is nowhere to exclude from, and no ignore file is made.
func TestInitOutsideARepositoryWritesNoIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if rc, _, _ := captureDispatchRCIn(t, dir, []string{"init"}); rc != 0 {
		t.Fatalf("init rc=%d", rc)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("init made a .gitignore (stat err %v)", err)
	}
}

// Either format already present: nothing is written, nothing is shadowed, the exclude is not
// touched, and the message names the file that was found.
func TestInitRefusesWhenEitherFormatExists(t *testing.T) {
	for _, name := range []string{"yolo-jail.toml", "yolo-jail.jsonc", "yolo-jail.json"} {
		t.Run(name, func(t *testing.T) {
			dir := initRepo(t)
			must(t, os.WriteFile(filepath.Join(dir, name), []byte("# mine\n"), 0o644))
			_, stdout, _ := captureDispatchRCIn(t, dir, []string{"init"})
			if !strings.Contains(stdout, name+" already exists.") {
				t.Errorf("no already-exists message for %s: %q", name, stdout)
			}
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if e.Name() != name && e.Name() != ".git" {
					t.Errorf("init wrote %s beside an existing %s", e.Name(), name)
				}
			}
			if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != "# mine\n" {
				t.Errorf("existing %s was changed: %q", name, got)
			}
			if b, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude")); strings.Contains(string(b), ".yolo/") {
				t.Errorf("init touched the exclude although it wrote nothing")
			}
		})
	}
}

// --mount is an explicit request, so it is written, and the file still loads under the strict reader.
func TestInitMountFlagWritesTOMLMounts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	if rc, _, _ := captureDispatchRCIn(t, dir, []string{"init", "-m", "/data/sets", "--mount=/a:/ctx/b"}); rc != 0 {
		t.Fatalf("init rc=%d", rc)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "yolo-jail.toml"))
	if !strings.Contains(string(body), `"/data/sets"`) || !strings.Contains(string(body), `"/a:/ctx/b"`) {
		t.Fatalf("mounts not written:\n%s", body)
	}
	if _, err := config.LoadReadableConfig(dir, true, nil); err != nil {
		t.Errorf("the scaffolded file does not load strictly: %v\n%s", err, body)
	}
}
