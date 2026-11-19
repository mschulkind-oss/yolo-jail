package cli

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func scopedCLISnapshot(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	for _, root := range roots {
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			snapshot[root] = "absent"
			continue
		}
		if err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := e.Info()
			if err != nil {
				return err
			}
			value := fmt.Sprint(info.Mode())
			switch {
			case info.Mode().IsRegular():
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				value += "\n" + string(data)
			case info.Mode()&os.ModeSymlink != 0:
				link, err := os.Readlink(path)
				if err != nil {
					return err
				}
				value += "\n" + link
			}
			snapshot[path] = value
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func TestPackExplainAndAddDryRunUseCachedPinWithoutTransportOrWrites(t *testing.T) {
	repo := t.TempDir()
	gitRun := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitRun("init", "-q", "--initial-branch=main")
	scopedCLIWrite(t, filepath.Join(repo, "corpus/skills/chosen/SKILL.md"), "chosen\n")
	gitRun("add", "--all")
	gitRun("-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	commit := gitRun("rev-parse", "HEAD")
	body := func(selector, dir string, listed bool) string {
		packs := "[]"
		if listed {
			packs = fmt.Sprintf("[{pack='demo/corpus',skills={only=[%q]", selector)
			if dir != "" {
				packs += fmt.Sprintf(",dir=%q", dir)
			}
			packs += "}}]"
		}
		return fmt.Sprintf("packs=%s\n[pack_sources.demo]\ngit='file://%s'\nref='main'\n", packs, filepath.ToSlash(repo))
	}
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, body("chosen", "", true))
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("producer: %v %v", err, problems)
	}
	var entry config.PackEntry
	for _, e := range entries {
		if e.Name == "demo/corpus" {
			entry = e
		}
	}
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	lockPath := packsrc.LockPath(paths.UserConfigPath())
	refreshed, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: lockPath, Force: true})
	if err != nil || len(refreshed) != 1 || refreshed[0].Err != nil {
		t.Fatalf("fixture refresh: %+v %v", refreshed, err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	trace := filepath.Join(t.TempDir(), "git-reads")
	script := "#!/bin/sh\nread_command=\nfor arg do\n case \"$arg\" in\n cat-file|rev-parse|ls-tree|symbolic-ref|show-ref) read_command=$arg;;\n fetch|clone|init|checkout|archive|reset|update-ref|worktree|remote|gc|maintenance) printf 'DENIED %s\\n' \"$arg\" >> " + shquote.Quote(trace) + ";exit 91;;\n esac\ndone\n[ -n \"$read_command\" ] || exit 92\nprintf '%s\\n' \"$read_command\" >> " + shquote.Quote(trace) + "\nexec " + shquote.Quote(realGit) + " \"$@\"\n"
	scopedCLIWrite(t, filepath.Join(bin, "git"), script)
	if err := os.Chmod(filepath.Join(bin, "git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, surface := range []string{"explain", "add"} {
		for _, tc := range []struct {
			name, selector, dir string
			refused             bool
		}{{"typed", "chosen", "", false}, {"raw", "chosen", "skills", false}, {"stale", "missing", "", true}} {
			t.Run(surface+"/"+tc.name, func(t *testing.T) {
				scopedCLIWrite(t, user.Path, body(tc.selector, tc.dir, surface == "explain"))
				before := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir())
				args := []string{surface, "demo/corpus"}
				if surface == "add" {
					args = append(args, "--only", tc.selector, "--dry-run")
					if tc.dir != "" {
						args = append(args, "--skills-dir", tc.dir)
					}
				}
				rc, out, errw := runPackCapture(t, project.Root, args)
				if tc.refused {
					if rc == 0 || !strings.Contains(errw, "available names") || !strings.Contains(errw, commit) {
						t.Fatalf("stale readable selector: %d %s %s", rc, out, errw)
					}
				} else if rc != 0 || errw != "" || !strings.Contains(out, "inspected: cached commit "+commit) || !strings.Contains(out, "selected: [chosen]") {
					t.Fatalf("cached pure inspection: %d %s %s", rc, out, errw)
				}
				if after := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir()); !reflect.DeepEqual(before, after) {
					t.Fatal("pure inspection changed config/source/store")
				}
			})
		}
	}
	wholeBody := "packs=[{pack='demo/corpus',subdir='skills'}]\n" + strings.SplitN(body("chosen", "", false), "\n", 2)[1]
	scopedCLIWrite(t, user.Path, wholeBody)
	wholeBefore := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir())
	rc, wholeOut, wholeErr := runPackCapture(t, project.Root, []string{"explain", "demo/corpus"})
	if rc != 0 || wholeErr != "" || !strings.Contains(wholeOut, "mode=whole") || !strings.Contains(wholeOut, "inspected: cached commit "+commit) || !strings.Contains(wholeOut, "stages 1 file(s)") {
		t.Fatalf("pure cached whole group: %d %s %s", rc, wholeOut, wholeErr)
	}
	if after := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir()); !reflect.DeepEqual(wholeBefore, after) {
		t.Fatal("whole Explain wrote config/source/store")
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{"explain", "add"} {
		scopedCLIWrite(t, user.Path, body("chosen", "", surface == "explain"))
		before := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir())
		args := []string{surface, "demo/corpus"}
		if surface == "add" {
			args = append(args, "--only", "chosen", "--dry-run")
		}
		rc, out, errw := runPackCapture(t, project.Root, args)
		if rc != 0 || errw != "" || !strings.Contains(out, "selection not yet verified; resolved at next launch") {
			t.Fatalf("unverified no-pin %s: %d %s %s", surface, rc, out, errw)
		}
		for _, forbidden := range []string{"candidates:", "selected:", "license:", "omitted:", "delivers", "file(s)"} {
			if strings.Contains(out, forbidden) {
				t.Fatalf("unverified invented %s: %s", forbidden, out)
			}
		}
		if after := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir()); !reflect.DeepEqual(before, after) {
			t.Fatal("no-pin inspector wrote")
		}
	}
	scopedCLIWrite(t, user.Path, wholeBody)
	wholeBefore = scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir())
	rc, wholeOut, wholeErr = runPackCapture(t, project.Root, []string{"explain", "demo/corpus"})
	if rc != 0 || wholeErr != "" || !strings.Contains(wholeOut, "selection not yet verified; resolved at next launch") || strings.Contains(wholeOut, "file(s)") {
		t.Fatalf("whole no-pin state/counts: %d %s %s", rc, wholeOut, wholeErr)
	}
	if after := scopedCLISnapshot(t, repo, filepath.Dir(user.Path), paths.PacksDir()); !reflect.DeepEqual(wholeBefore, after) {
		t.Fatal("whole no-pin Explain wrote")
	}
	reads, err := os.ReadFile(trace)
	if err != nil || len(reads) == 0 || strings.Contains(string(reads), "DENIED") {
		t.Fatalf("read-only trace: %s %v", reads, err)
	}
}
