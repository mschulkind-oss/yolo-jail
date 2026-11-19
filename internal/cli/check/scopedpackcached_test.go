package check

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func scopedCheckSnapshot(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, root := range roots {
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			result[root] = "absent"
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			value := fmt.Sprint(info.Mode())
			if info.Mode().IsRegular() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				value += "\n" + string(data)
			} else if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				value += "\n" + target
			}
			result[path] = value
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestSectionPacksScopedCachedPinAndRefusalArePureUnderTransportRefusingGit(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(repo, "corpus/skills/chosen"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "corpus/skills/chosen/SKILL.md"), []byte("chosen\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "--all")
	gitRun("-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	commit := gitRun("rev-parse", "HEAD")
	body := func(selector, dir string) string {
		dirField := ""
		if dir != "" {
			dirField = fmt.Sprintf(",dir=%q", dir)
		}
		return fmt.Sprintf("packs=[{pack='demo/corpus',skills={only=[%q]%s}}]\n[pack_sources.demo]\ngit='file://%s'\nref='main'\n", selector, dirField, filepath.ToSlash(repo))
	}
	user := testconfig.User(t, body("chosen", ""))
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("producer: %v %v", err, problems)
	}
	var entry config.PackEntry
	for _, candidate := range entries {
		if candidate.Name == "demo/corpus" {
			entry = candidate
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
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		name, selector, dir string
		refused             bool
	}{{"typed", "chosen", "", false}, {"raw", "chosen", "skills", false}, {"stale", "missing", "", true}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(user.Path, []byte(body(tc.selector, tc.dir)), 0644); err != nil {
				t.Fatal(err)
			}
			before := scopedCheckSnapshot(t, filepath.Dir(user.Path), paths.PacksDir())
			var out bytes.Buffer
			r := newReporter(&out, false)
			o := &Options{}
			o.sectionPacks(r, jsonx.NewOrderedMap())
			if tc.refused {
				if r.failed == 0 || !strings.Contains(out.String(), "available names") || !strings.Contains(out.String(), commit) {
					t.Fatalf("stale cached selector not refused: %s", out.String())
				}
			} else {
				if r.failed != 0 {
					t.Fatalf("valid cached scope failed: %s", out.String())
				}
				found := false
				for _, p := range o.selectedPacks {
					if p.Projection != nil {
						found = true
						if p.Projection.Commit != commit || p.SourceRoot != "" {
							t.Fatalf("pure pin/copy: %+v", p)
						}
					}
				}
				if !found {
					t.Fatal("cached selection dropped")
				}
			}
			if after := scopedCheckSnapshot(t, filepath.Dir(user.Path), paths.PacksDir()); !reflect.DeepEqual(before, after) {
				t.Fatal("check changed source/config/store")
			}
		})
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	before := scopedCheckSnapshot(t, filepath.Dir(user.Path), paths.PacksDir())
	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	if r.failed != 0 || !strings.Contains(out.String(), "selection not yet verified; resolved at next launch") {
		t.Fatalf("no-pin cached branch should be unverified: %s", out.String())
	}
	for _, forbidden := range []string{"candidates:", "selected:", "license:", "omitted"} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("unverified fabricated %s: %s", forbidden, out.String())
		}
	}
	if after := scopedCheckSnapshot(t, filepath.Dir(user.Path), paths.PacksDir()); !reflect.DeepEqual(before, after) {
		t.Fatal("unverified check wrote")
	}
	reads, err := os.ReadFile(trace)
	if err != nil || len(reads) == 0 || strings.Contains(string(reads), "DENIED") {
		t.Fatalf("read-only git trace: %s %v", reads, err)
	}
}
