package cli

// packsourcerename_records_test.go pins what `yolo pack source rename` does beyond the handles
// (docs/design/pack-naming.md §3.6, PN-D10): the handle-keyed tables, the pins that follow each
// pack, and an omitted-ref git source that stays at its pinned commit.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// The plan renames every table keyed by a handle under the source, and leaves the others. Fails if
// the table rewrite is deleted from planSourceRename.
func TestPackSourceRenamePlansTheHandleKeyedTables(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "x", "pack.json"), `{}`)
	body := "packs = [\"acme/x\"]\n\n[pack_sources]\nacme = { path = '" + filepath.ToSlash(src) + "' }\n\n" +
		"[allow.\"acme/x\"]\nmode = \"fast\"\n\n[allow.git]\nalways = [\"fetch\"]\n"
	user := testconfig.User(t, body)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	workspace := testconfig.Workspace(t, "").Root
	t.Chdir(workspace)
	var errw strings.Builder
	plan, target, rc := planSourceRename(workspace, "acme", "tools", &errw)
	if rc != 0 {
		t.Fatalf("plan rc=%d: %s", rc, errw.String())
	}
	data := target.ExpectedBytes()
	for _, m := range append([]configedit.Mutation{plan.first}, plan.then...) {
		p, err := configedit.PlanEdit(data, target.Format, m)
		if err != nil {
			t.Fatalf("planning %+v: %v", m, err)
		}
		data = p.Updated
	}
	got := string(data)
	doc, err := configdoc.Parse(data, target.Format)
	if err != nil {
		t.Fatalf("the planned config does not parse: %v\n%s", err, got)
	}
	rawAllow, _ := doc.Values.Get("allow")
	allow, _ := rawAllow.(packsrc.Object)
	if allow == nil {
		t.Fatalf("the planned config has no allow table:\n%s", got)
	}
	renamed, _ := allow.Get("tools/x")
	table, _ := renamed.(packsrc.Object)
	if mode, _ := table.Get("mode"); mode != "fast" {
		t.Errorf("allow.\"tools/x\" is %v, want the old table's mode = \"fast\":\n%s", renamed, got)
	}
	if _, ok := allow.Get("git"); !ok {
		t.Errorf("the rename dropped [allow.git]:\n%s", got)
	}
	if strings.Contains(got, "acme") {
		t.Errorf("the planned config still names acme:\n%s", got)
	}
	if len(plan.tables) != 1 || plan.tables[0] != `[allow."tools/x"]` {
		t.Errorf("the plan names %v as renamed tables, want [allow.\"tools/x\"]", plan.tables)
	}
}

// The renamed packs' pins follow them: the lock entry the source's pack had under acme/x is under
// tools/x after the rename. Fails if the verb's record move is deleted.
func TestPackSourceRenameMovesThePacksPins(t *testing.T) {
	workspace, userPath := sourceRenameFixture(t)
	lockPath := packsrc.LockPath(userPath)
	if err := packsrc.WithLock(paths.PacksDir(), lockPath, nil, func(l *packsrc.Lock) (bool, error) {
		l.Set(packsrc.LockEntry{Name: "acme/x", Source: "file://" + filepath.ToSlash(sourceRenamePackDir(t, userPath))})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	rc, out, errw := runSourceRename(t, workspace, "", "acme", "tools", "--yes")
	if rc != 0 {
		t.Fatalf("rename rc=%d\nstdout=%s\nstderr=%s", rc, out, errw)
	}
	lock, err := packsrc.LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.Get("tools/x"); !ok {
		t.Errorf("the pin did not follow the rename: %+v\nstdout=%s\nstderr=%s", lock.Packs, out, errw)
	}
	if _, ok := lock.Get("acme/x"); ok {
		t.Errorf("the pin stayed under the old handle: %+v", lock.Packs)
	}
	if !strings.Contains(out, "pack tools/x was acme/x") {
		t.Errorf("the rename does not say the records moved:\n%s", out)
	}
}

// sourceRenamePackDir is the fixture's x pack directory, read from its user config.
func sourceRenamePackDir(t *testing.T, userPath string) string {
	t.Helper()
	data, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(data), "path = '")
	if !ok {
		t.Fatalf("no source path in %s", data)
	}
	dir, _, _ := strings.Cut(rest, "'")
	return filepath.Join(filepath.FromSlash(dir), "x")
}

// A git source written without `ref` renames, and its pack stays at the commit it was pinned to
// although its branch has moved on: the rename fetches nothing.
func TestPackSourceRenameKeepsAnOmittedRefGitPackAtItsPinnedCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	base := filepath.Join(home, "repos")
	remote := filepath.Join(base, "packs.git")
	work := filepath.Join(base, "work")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	sourceRenameGit(t, base, "init", "-q", "--bare", "--initial-branch=trunk", remote)
	sourceRenameGit(t, work, "init", "-q", "--initial-branch=trunk")
	writeFile(t, filepath.Join(work, "x", "pack.json"), `{}`)
	sourceRenameGit(t, work, "add", "-A")
	sourceRenameGit(t, work, "commit", "-qm", "first")
	first := sourceRenameGit(t, work, "rev-parse", "HEAD")
	sourceRenameGit(t, work, "remote", "add", "origin", "file://"+filepath.ToSlash(remote))
	sourceRenameGit(t, work, "push", "-q", "origin", "trunk")
	sourceRenameGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/trunk")

	userPath := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	writeFile(t, userPath, "packs = ['acme/x']\n[pack_sources.acme]\ngit = 'file://"+filepath.ToSlash(remote)+"'\n")
	entries, err := config.LoadPacks(func(string) {})
	if err != nil || len(entries) != 1 || !entries[0].AllowDefaultRef {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	lockPath := packsrc.LockPath(userPath)
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	if _, err := store.Refresh([]packsrc.RefreshPack{entries[0].RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: lockPath}); err != nil {
		t.Fatal(err)
	}
	// The branch moves on after the pin.
	writeFile(t, filepath.Join(work, "x", "next"), "next\n")
	sourceRenameGit(t, work, "add", "-A")
	sourceRenameGit(t, work, "commit", "-qm", "second")
	sourceRenameGit(t, work, "push", "-q", "origin", "trunk")

	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rc, out, errw := runSourceRename(t, workspace, "", "acme", "tools", "--yes")
	if rc != 0 {
		t.Fatalf("renaming an omitted-ref git source: rc=%d\nstdout=%s\nstderr=%s", rc, out, errw)
	}
	lock, err := packsrc.LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	pin, ok := lock.Get("tools/x")
	if !ok || pin.Commit != first {
		t.Errorf("tools/x is pinned at %+v (ok=%v), want its first commit %s: the rename moved it", pin, ok, first)
	}
	if _, ok := lock.Get("acme/x"); ok {
		t.Errorf("the old handle kept a pin: %+v", lock.Packs)
	}
}

func sourceRenameGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
