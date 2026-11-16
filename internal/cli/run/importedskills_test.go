package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/skillimports"
)

func TestImportedSkillsReachJailThroughLaunch(t *testing.T) {
	o, workspace, _ := wsSkillsLaunch(t, `["claude", "git"]`)
	cfg := importLaunchConfig()
	store, commit := prepareRunImportStore(t, workspace)
	o.skillImportGatePassed = true
	o.approvedScopes = map[string][]config.ScopeRepo{"git-broker": {{Repo: "https://example.test/acme/skills"}}}
	called := 0
	previousResolver := resolveSkillImportForLaunch
	resolveSkillImportForLaunch = func(_ context.Context, gotStore *skillimports.Store, entry config.SkillImport,
		authority skillimports.Authority, _ time.Time) (skillimports.Resolution, string, error) {
		called++
		if gotStore.Directory() != store.Directory() || entry.Handle != "acme" {
			t.Fatalf("fresh resolver got store/entry = %s/%+v", gotStore.Directory(), entry)
		}
		if len(authority.ApprovedRemotes) != 1 || authority.ApprovedRemotes[0] != entry.Git {
			t.Fatalf("fresh resolver did not receive this gate's approved Git URL: %+v", authority)
		}
		record := runImportResolution(entry, commit)
		if err := gotStore.WriteResolution(record); err != nil {
			t.Fatal(err)
		}
		return record, "", nil
	}
	defer func() { resolveSkillImportForLaunch = previousResolver }()
	_, _, _, err := o.stagePacks(wsSkillsCname)
	if err != nil {
		t.Fatal(err)
	}
	o.resolveFreshSkillImports(cfg)
	if called != 1 {
		t.Fatalf("fresh launch resolver calls = %d, want 1", called)
	}

	staging := stageWorkspaceSkillsWithConfig(t, o, "podman", cfg)
	root := filepath.Join(staging, jailcontent.SkillStagingName("claude"))
	got, err := os.ReadFile(filepath.Join(root, "review", "SKILL.md"))
	if err != nil || string(got) != "review from selected commit" {
		t.Fatalf("staged imported skill = %q, %v", got, err)
	}
	for _, absent := range []string{"other", "README.md", "pack.toml"} {
		if _, err := os.Stat(filepath.Join(root, absent)); !os.IsNotExist(err) {
			t.Errorf("non-selected import content %q reached staging: %v", absent, err)
		}
	}
	assertRunImportCallerOrder(t)
}

func TestImportAttachNeverFetches(t *testing.T) {
	o, workspace, _ := wsSkillsLaunch(t, `["claude"]`)
	cfg := importLaunchConfig()
	store, commit := prepareRunImportStore(t, workspace)
	entry := config.SkillImports(cfg)[0]
	if err := store.WriteResolution(runImportResolution(entry, commit)); err != nil {
		t.Fatal(err)
	}
	contacts := 0
	previousResolver := resolveSkillImportForLaunch
	resolveSkillImportForLaunch = func(context.Context, *skillimports.Store, config.SkillImport,
		skillimports.Authority, time.Time) (skillimports.Resolution, string, error) {
		contacts++
		return skillimports.Resolution{}, "", os.ErrPermission
	}
	defer func() { resolveSkillImportForLaunch = previousResolver }()
	staging := stageWorkspaceSkillsWithConfig(t, o, "podman", cfg)
	if contacts != 0 {
		t.Fatalf("attach staging contacted the resolver %d times", contacts)
	}
	got, err := os.ReadFile(filepath.Join(staging, jailcontent.SkillStagingName("claude"), "review", "SKILL.md"))
	if err != nil || string(got) != "review from selected commit" {
		t.Fatalf("attach did not stage its recorded commit: %q, %v", got, err)
	}
}

func assertRunImportCallerOrder(t *testing.T) {
	t.Helper()
	source, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	fresh := afterFunc(string(source), "func (o *Options) runContainer(")
	gate := strings.Index(fresh, "o.skillImportGatePassed = true")
	resolve := strings.Index(fresh, "o.resolveFreshSkillImports(cfg)")
	refresh := strings.Index(fresh, "o.refreshJailBriefings(cname, cfg, rt, staged,")
	if gate < 0 || resolve < gate || refresh < resolve {
		t.Fatalf("fresh container import resolution must follow its gate and precede staging; gate=%d resolve=%d refresh=%d", gate, resolve, refresh)
	}
	macos := afterFunc(string(source), "func Run(opts Options)")

	gate = strings.Index(macos, "o.skillImportGatePassed = true")
	resolve = strings.Index(macos, "o.resolveFreshSkillImports(cfg)")
	refresh = strings.Index(macos, "o.refreshJailBriefings(cname, cfg, rt, staged,")
	if gate < 0 || resolve < gate || refresh < resolve {
		t.Fatalf("macos-user import resolution must follow its gate and precede staging; gate=%d resolve=%d refresh=%d", gate, resolve, refresh)
	}
	attach := afterFunc(string(source), "func (o *Options) attachExisting(")
	attachRefresh := strings.Index(attach, "o.refreshJailBriefings(cname, cfg, rt, view.staged,")
	if attachRefresh < 0 || strings.Contains(attach[:attachRefresh], "o.resolveFreshSkillImports(cfg)") {
		t.Fatal("attach must project existing records through refreshJailBriefings without calling the fresh resolver")
	}
}

func importLaunchConfig() *jsonx.OrderedMap {
	entry := jsonx.NewOrderedMap()
	entry.Set("git", "https://example.test/acme/skills")
	entry.Set("ref", "main")
	entry.Set("path", "skills")
	entry.Set("only", []any{"review"})
	imports := jsonx.NewOrderedMap()
	imports.Set("acme", entry)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("skill_imports", imports)
	return cfg
}

func prepareRunImportStore(t *testing.T, workspace string) (*skillimports.Store, string) {
	t.Helper()
	store, err := skillimports.Open(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureObjectStore(); err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	source := filepath.Join(temp, "source.git")
	work := filepath.Join(temp, "work")
	runTestGit(t, "", "init", "--bare", "--template=", source)
	runTestGit(t, "", "clone", source, work)
	runTestGit(t, work, "config", "user.name", "workspace import test")
	runTestGit(t, work, "config", "user.email", "skills@example.test")
	for path, body := range map[string]string{
		"skills/review/SKILL.md": "review from selected commit",
		"skills/other/SKILL.md":  "other skill must not stage",
		"README.md":              "not a skill",
		"pack.toml":              "never read pack manifests",
	} {
		full := filepath.Join(work, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runTestGit(t, work, "add", "-A")
	runTestGit(t, work, "-c", "commit.gpgsign=false", "commit", "-m", "skills")
	commit := strings.TrimSpace(runTestGit(t, work, "rev-parse", "HEAD"))
	runTestGit(t, work, "push", "origin", "HEAD:refs/heads/main")
	runTestGit(t, "", "--git-dir", store.ObjectDirectory(), "fetch", source, commit)
	return store, commit
}

func runImportResolution(entry config.SkillImport, commit string) skillimports.Resolution {
	return skillimports.Resolution{Version: 1, Handle: entry.Handle, Git: entry.Git, Ref: entry.Ref,
		ResolvedRef: "refs/heads/main", Repo: "example.test/acme/skills", Commit: commit,
		AccessClass: skillimports.AccessAnonymous, ResolvedAt: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
}

func runTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
