package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func readableDefaultPackRemote(t *testing.T) (remote, work, commit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	remote = filepath.Join(base, "packs-remote")
	work = filepath.Join(base, "work")
	git := func(dir string, args ...string) string {
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
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	git(base, "init", "-q", "--bare", "--initial-branch=trunk", remote)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	git(work, "init", "-q", "--initial-branch=trunk")
	manifest := filepath.Join(work, "tools", "pack.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"ignored"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "tools", "version"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(work, "add", "-A")
	git(work, "commit", "-qm", "initial")
	commit = git(work, "rev-parse", "HEAD")
	git(work, "remote", "add", "origin", "file://"+filepath.ToSlash(remote))
	git(work, "push", "-q", "origin", "trunk")
	git(remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
	return remote, work, commit
}

func writeReadablePackSource(t *testing.T, remote string) {
	t.Helper()
	dir := filepath.Join(paths.Home(), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "packs = ['acme/tools']\n[pack_sources.acme]\ngit = 'file://" + filepath.ToSlash(remote) + "'\n"
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchRefreshUsesReadableOmittedRefAndRecordsRemoteDefault(t *testing.T) {
	hostRefreshEnv(t)
	remote, work, commit := readableDefaultPackRemote(t)
	writeReadablePackSource(t, remote)
	var said []string
	RefreshConfiguredPacks(func(line string) { said = append(said, line) }, func(line string) { said = append(said, line) })

	lock, err := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lock.Get("acme/tools")
	if !ok || entry.Ref != "trunk" || entry.Commit != commit {
		t.Fatalf("launch refresh lock = %+v, present %v; want advertised trunk at %s (output %q)", entry, ok, commit, said)
	}

	git := func(dir string, args ...string) string {
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
	if err := os.WriteFile(filepath.Join(work, "tools", "version"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(work, "add", "-A")
	git(work, "commit", "-qm", "move-default")
	second := git(work, "rev-parse", "HEAD")
	git(work, "branch", "alice/review", second)
	git(work, "push", "-q", "origin", "alice/review")
	git(remote, "symbolic-ref", "HEAD", "refs/heads/alice/review")
	probeSource := "git+file://" + filepath.ToSlash(remote) + "?ref=alice%2Freview"
	probe, err := (&packsrc.Store{Dir: paths.PacksDir()}).Refresh([]packsrc.RefreshPack{{Name: "probe", Source: probeSource}}, packsrc.RefreshOptions{
		Force: true, LockPath: packsrc.LockPath(paths.UserConfigPath()),
	})
	if err != nil || len(probe) != 1 || probe[0].Err != nil || probe[0].Commit != second {
		t.Fatalf("moving the mirror default = %+v, %v; want commit %s", probe, err, second)
	}

	// The producer's durable lock must survive a new resolver process even when its
	// mirror now advertises another branch.
	entries, err := config.LoadPacks(nil)
	entries = config.WithoutSelectedByDefault(entries)
	if err != nil || len(entries) != 1 {
		t.Fatalf("load readable packs after refresh = %v, %v", entries, err)
	}
	selected, complete := config.SelectedPackDeclarations(entries, config.UserScopeSelection(), nil)
	if !complete || len(selected) != 1 || selected[0].Name != "acme/tools" {
		t.Fatalf("selected declarations = %v, complete %v", selected, complete)
	}
	resolved, err := config.ResolvePack(entries[0], config.ResolvePackSpec{ReadOnlyStore: true, Getenv: func(string) string { return "" }})
	if err != nil || resolved.Pack == nil {
		t.Fatalf("fresh read-only resolution = %+v, %v", resolved, err)
	}
	version, err := os.ReadFile(filepath.Join(resolved.Pack.Root, "version"))
	if err != nil || string(version) != "first\n" || entries[0].Source != "git+file://"+filepath.ToSlash(remote)+"//tools" {
		t.Fatalf("resolution changed pinned content or authored source: version %q err %v source %q", version, err, entries[0].Source)
	}
}
