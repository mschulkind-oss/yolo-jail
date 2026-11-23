package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// The `packs[N].local` refusals (internal/config/readablepackslocalkey.go) advise
// `yolo config unset packs[N].local|skills --user` and `yolo config set packs[N].ref|local …`. This runs the
// real edit path the dispatcher uses on an inline-table git entry whose pack is already fetched, so the
// advice is proven to work and TestEveryKeyAdviceNamesForUnsetIsRemovable may skip packs[i].local.
func TestPackLocalKeyAdviceEditsAnInlineTableEntry(t *testing.T) {
	project := testconfig.Workspace(t, "")
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v %s", err, data)
		}
	}
	git("init", "-q", "--initial-branch=main")
	if err := os.MkdirAll(filepath.Join(repo, "skills/a"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{"pack.toml": "", "skills/a/SKILL.md": "a\n"} {
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	git("branch", "develop")
	// The published address is an owner/repository URL (direct git entries accept no other); git rewrites it
	// to the local repository, so no network is touched.
	url := "https://example.invalid/fixture/corpus"
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[url \"file://"+filepath.ToSlash(repo)+"\"]\n\tinsteadOf = "+url+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	clone := filepath.ToSlash(repo)
	// fetch puts both branches in the store, so the planned config resolves with no network.
	fetch := func(user testconfig.Fixture) {
		t.Helper()
		var refs []packsrc.RefreshPack
		for _, branch := range []string{"main", "develop"} {
			scopedCLIWrite(t, user.Path, "packs = [{ git = '"+url+"', ref = '"+branch+"' }]\n")
			entries, problems, err := config.LoadPackEntries()
			if err != nil || len(problems) > 0 {
				t.Fatalf("load: %v %v", problems, err)
			}
			for _, e := range entries {
				if e.Name == "fixture/corpus" {
					refs = append(refs, e.RefreshSpec(false))
				}
			}
		}
		store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
		res, err := store.Refresh(refs, packsrc.RefreshOptions{LockPath: packsrc.LockPath(paths.UserConfigPath())})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res {
			if r.Err != nil {
				t.Fatalf("fetch fixture: %v", r.Err)
			}
		}
	}
	entry := "  { git = '" + url + "', ref = 'main', %s }, # tail\n"
	gitKey := "git = '" + url + "'"
	for _, tc := range []struct {
		name  string
		args  []string
		extra string
		want  []string
		gone  []string
	}{
		{"unset local", []string{"unset", "packs[0].local", "--user"}, "local = '" + clone + "'",
			[]string{"# top", "# keep", gitKey, "ref = 'main'", "# tail"}, []string{"local"}},
		{"set ref", []string{"set", "packs[0].ref", "develop", "--user"}, "local = '" + clone + "'",
			[]string{"# top", "# keep", gitKey, "local = '" + clone + "'", "# tail", "develop"}, []string{"'main'"}},
		{"unset skills", []string{"unset", "packs[0].skills", "--user"}, "skills = { only = ['a'] }",
			[]string{gitKey, "ref = 'main'", "# tail"}, []string{"skills"}},
		{"set local", []string{"set", "packs[0].local", clone, "--user"}, "local = '" + clone + "-old'",
			[]string{"# tail", "local = \"" + clone + "\""}, []string{clone + "-old"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := testconfig.User(t, "")
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_ALLOW_MISSING_PROGRAMS", "")
			fetch(user)
			scopedCLIWrite(t, user.Path, "# top\npacks = [\n  # keep\n"+strings.Replace(entry, "%s", tc.extra, 1)+"]\n")
			rc, out, errw := runConfigEditCapture(t, project.Root, tc.args)
			after, _ := os.ReadFile(user.Path)
			t.Logf("rc=%d out=%s err=%s\n%s", rc, out, errw, after)
			if rc != 0 {
				t.Fatalf("advice %v failed: rc=%d %s", tc.args, rc, errw)
			}
			for _, w := range tc.want {
				if !strings.Contains(string(after), w) {
					t.Errorf("missing %q in:\n%s", w, after)
				}
			}
			for _, g := range tc.gone {
				if strings.Contains(string(after), g) {
					t.Errorf("still has %q in:\n%s", g, after)
				}
			}
			if _, err := configdoc.Parse(after, configdoc.TOML); err != nil {
				t.Errorf("invalid TOML: %v\n%s", err, after)
			}
		})
	}
}
