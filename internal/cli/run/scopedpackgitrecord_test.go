package run

import (
	"bytes"
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectScopedGitMixedRecordedRowsIgnoreDeletedSourceAndCache(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git: %v %s", err, data)
		}
		return strings.TrimSpace(string(data))
	}
	put := func(rel, data string) {
		t.Helper()
		file := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "--initial-branch=main")
	put("corpus/skills/chosen/notes.md", "folder\n")
	put("corpus/skills/marked/SKILL.md", "marked\n")
	put("corpus/skills/marked/run.sh", "printf supporting\n")
	put("corpus/skills/hidden/SKILL.md", "hidden\n")
	put("elsewhere/remote/SKILL.md", "remote\n")
	put("LICENSE", "fixture license\n")
	put("corpus/pack.toml", fmt.Sprintf("[[program]]\nbin = 'poison'\nvia = 'npm'\npackage = 'must-not-install'\n[[skills]]\nsource = 'git+file://%s//elsewhere/remote?ref=main'\nas = 'remote'\n[[skills]]\nsource = 'git+https://example.invalid/never/fetch//skill?ref=main'\nas = 'unselected'\n", fileURLPath(repo)))
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "synthetic mixed rows")
	commit := git("rev-parse", "HEAD")
	user := testconfig.User(t, fmt.Sprintf("packs = [{ pack = 'demo/corpus', skills = { only = ['chosen', 'marked', 'remote'] } }]\n[pack_sources.demo]\ngit = 'file://%s'\nref = 'main'\n", fileURLPath(repo)))
	t.Setenv("HOME", user.Root)
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
	refreshed, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: packsrc.LockPath(paths.UserConfigPath())})
	if err != nil || len(refreshed) != 1 || refreshed[0].Err != nil {
		t.Fatalf("pin fixture: %+v %v", refreshed, err)
	}

	var report bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: &report, Stderr: &report}
	const cname = "yolo-test-scoped-git-record"
	root, packs, briefings, err := o.stagePacks(cname)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { discardPackTree(cname, root) })
	if !o.prepareFreshSkillPayloads(cname, "podman", stagedPacks{root: root, packs: packs, briefings: briefings}) {
		t.Fatal(report.String())
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if err := packsrc.RemoveSealedSkillTree(user.Root, paths.PacksDir()); err != nil {
		t.Fatal(err)
	}
	host, err := loadPackTree(root)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := entrypoint.LoadJailPacks(&entrypoint.Env{Home: user.Root, Vars: map[string]string{"YOLO_PACK_ROOT": root}})
	if err != nil {
		t.Fatal(err)
	}
	for _, loaded := range [][]*packload.Pack{host, guest} {
		found := false
		for _, p := range loaded {
			if p.Name != "demo/corpus" {
				continue
			}
			found = true
			rows := p.SourcedSkillRows()
			if len(rows) != 3 || len(p.InstallBins()) != 0 || len(p.Plugins()) != 0 {
				t.Fatalf("not selected-only: %+v", p)
			}
			payload := rows[0].PayloadRoot
			for _, row := range rows {
				if row.Outcome.Commit != commit || row.PayloadRoot != payload || row.Plan == nil {
					t.Fatalf("lost grouped pinned row: %+v", row)
				}
			}
			if rows[0].Plan.MarkerState != "absent" || rows[1].Plan.MarkerState != "present" || rows[2].Plan.Kind != "marker" {
				t.Fatalf("lost kind/state: %+v", rows)
			}
		}
		if !found {
			t.Fatal("scoped pack not reloaded")
		}
	}
}

// fileURLPath is a path spelled for a file:// URL: slashes, with any space or other byte a URL
// path may not carry percent-encoded, so a temp dir with a space in it still names one source.
func fileURLPath(p string) string {
	return (&url.URL{Path: filepath.ToSlash(p)}).EscapedPath()
}
