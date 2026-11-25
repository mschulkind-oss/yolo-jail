package cli

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func TestDirectScopedGitMixedRowsUseCommonPayloadAndComposer(t *testing.T) {
	project := testconfig.Workspace(t, "")
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
	t.Cleanup(func() { _ = packsrc.RemoveSealedSkillTree(user.Root, paths.PacksDir()) })
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
	resolved, err := config.ResolvePackForProcess(entry, config.ResolvePackSpec{Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	p := resolved.Pack
	if len(p.InstallBins()) != 0 || p.SourcedSkillsWithheld != "" || len(p.SourcedSkillRows()) != 3 {
		t.Fatalf("projection activated or withheld: %+v", p)
	}
	results := packload.ResolveSelectedSkillPayloads([]*packload.Pack{p}, store, packsrc.SkillResolveOptions{LockPath: packsrc.SkillsLockPath(paths.UserConfigPath())}, true)
	var payload string
	for _, result := range results {
		if result.State != packsrc.SkillReady || result.Commit != commit {
			t.Fatalf("selected mixed payload: %+v", result)
		}
		if payload == "" {
			payload = result.Payload
		} else if payload != result.Payload {
			t.Fatalf("same repository/commit published separate payloads: %s %s", payload, result.Payload)
		}
	}
	reader := &packload.Pack{Name: "reader", Root: project.Root, Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Agent: "fixture", Into: ".fixture/skills"}}}}
	p = p.ResolveDestinations([]*packload.Pack{p, reader}).Pack
	home := t.TempDir()
	destinations := hostskills.ComposeHostSkillsFor([]*packload.Pack{p, reader}, home, func(string, packdecl.Kind) bool { return true })
	req := hostskills.ComposeRequest{Composed: &hostskills.Manifest{}, ArchiveRoot: hostskills.ArchiveRoot(t.TempDir()), Stamp: "fixture", PackSetComplete: true, Configured: map[string]bool{"demo/corpus": true, "reader": true}}
	if _, err := hostskills.RenderHostSkills(destinations, req, false); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(home, ".fixture/skills")
	items, err := os.ReadDir(destination)
	if err != nil || len(items) != 3 {
		t.Fatalf("selected output: %v %v", items, err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "demo-chosen/SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("marker invented: %v", err)
	}
	for rel, want := range map[string]string{"demo-chosen/notes.md": "folder\n", "demo-marked/SKILL.md": "---\nname: demo-marked\n---\nmarked\n", "demo-remote/SKILL.md": "---\nname: demo-remote\n---\nremote\n", "demo-chosen/LICENSE": "fixture license\n"} {
		got, err := os.ReadFile(filepath.Join(destination, rel))
		if err != nil || !bytes.Equal(got, []byte(want)) {
			t.Fatalf("selected data %s: %q %v", rel, got, err)
		}
	}
}

// fileURLPath is a path spelled for a file:// URL: slashes, with any space or other byte a URL
// path may not carry percent-encoded, so a temp dir with a space in it still names one source.
func fileURLPath(p string) string {
	return (&url.URL{Path: filepath.ToSlash(p)}).EscapedPath()
}
