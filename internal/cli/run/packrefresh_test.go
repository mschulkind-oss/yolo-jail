package run

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// packrefresh_test.go pins the launch-time pack refresh at the RUN PIPELINE: that a launch
// really calls it (the call site, not just the callee — delete `o.refreshPacks()` from Run and
// TestLaunchFetchesANeverInstalledGitPack goes red), that it is a no-op in a jail, and that it
// never reaches the network for an embedded or local pack. The policy itself is pinned in
// internal/packsrc/refresh_test.go.

// refreshGitRepo builds a real git repo holding a minimal pack named gp, on `main`.
func refreshGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	files := map[string]string{
		"pack.json":               `{"name":"gp"}`,
		"skills/gpskill/SKILL.md": "---\nname: gpskill\ndescription: from git\n---\nbody\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "gp"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_AUTHOR_NAME=t",
			"GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// hostRefreshEnv states the delivery situation: a host (no YOLO_VERSION) with no staged tree.
func hostRefreshEnv(t *testing.T) string {
	t.Helper()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	return home
}

// THE CALL SITE. A launch whose config names a git pack nobody ever installed comes up with
// that pack staged — the refresh fetched it before staging resolved it — and says so.
// Without the refresh, stagePacks' PackRoot fails "never been fetched" and Run returns 1.
func TestLaunchFetchesANeverInstalledGitPack(t *testing.T) {
	home := hostRefreshEnv(t)
	repo := refreshGitRepo(t)
	writeUserPacks(t, home, `[{"name": "gp", "source": "git+file://`+repo+`?ref=main"}]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	var staged string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, packRoot, _ string, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		staged = packRoot
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(staged, "gp", "skills", "gpskill", "SKILL.md")); err != nil {
		t.Errorf("the never-installed git pack was not staged under %q: %v\nstdout:\n%s", staged, err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Fetched pack gp: main → ") {
		t.Errorf("the launch did not disclose the fetch:\n%s", stdout.String())
	}
	l, err := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := l.Get("gp"); !ok || len(e.Commit) != 40 || e.Ref != "main" {
		t.Errorf("the launch did not record the pack in the lockfile: %+v (present %v)", e, ok)
	}
}

// IN-JAIL THE REFRESH DOES NOTHING: the jail has no pack store and no git credentials. The
// control half proves the same config does fetch on a host, so the first half is not passing
// for an unrelated reason.
func TestRefreshConfiguredPacksIsANoOpInAJail(t *testing.T) {
	home := hostRefreshEnv(t)
	repo := refreshGitRepo(t)
	writeUserPacks(t, home, `[{"name": "gp", "source": "git+file://`+repo+`?ref=main"}]`)
	mirrors := filepath.Join(paths.PacksDir(), "mirrors")

	var said []string
	say := func(s string) { said = append(said, s) }
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	RefreshConfiguredPacks(say, say)
	if _, err := os.Stat(mirrors); err == nil {
		t.Fatalf("an in-jail refresh fetched into %s", mirrors)
	}
	if len(said) != 0 {
		t.Errorf("an in-jail refresh printed %q", said)
	}

	t.Setenv("YOLO_VERSION", "")
	RefreshConfiguredPacks(say, say)
	if entries, err := os.ReadDir(mirrors); err != nil || len(entries) != 1 {
		t.Errorf("control: a host refresh did not fetch (%v, %v)", entries, err)
	}
}

// EMBEDDED AND LOCAL PACKS NEVER REACH THE STORE: nothing is fetched, locked or stamped for
// them, and nothing is said. A git binary that does not exist would make any attempt fail
// loudly, but the assertion is on the store, which is where any attempt would leave a trace.
func TestRefreshConfiguredPacksLeavesEmbeddedAndLocalPacksAlone(t *testing.T) {
	home := hostRefreshEnv(t)
	local := t.TempDir()
	writeUserPacks(t, home, `["claude", {"name": "mine", "source": "file://`+local+`"}]`)

	var said []string
	say := func(s string) { said = append(said, s) }
	RefreshConfiguredPacks(say, say)
	if _, err := os.Stat(paths.PacksDir()); err == nil {
		entries, _ := os.ReadDir(paths.PacksDir())
		t.Errorf("a refresh of embedded and local packs wrote into the pack store: %v", entries)
	}
	if len(said) != 0 {
		t.Errorf("nothing was fetched, but the refresh printed %q", said)
	}
	if _, err := os.Stat(packsrc.LockPath(paths.UserConfigPath())); err == nil {
		t.Error("a refresh of embedded and local packs wrote the lockfile")
	}
}

// A --DRY-RUN LAUNCH FETCHES NOTHING: it materializes nothing, and a fetch, a checkout and a
// lockfile rewrite would all be materializing. TestLaunchFetchesANeverInstalledGitPack is the
// control — the same config, without --dry-run, fetches.
func TestDryRunLaunchDoesNotRefreshPacks(t *testing.T) {
	home := hostRefreshEnv(t)
	repo := refreshGitRepo(t)
	writeUserPacks(t, home, `[{"name": "gp", "source": "git+file://`+repo+`?ref=main"}]`)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	o.DryRun = true
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		return 0
	}
	_ = Run(*o)
	if _, err := os.Stat(filepath.Join(paths.PacksDir(), "mirrors")); err == nil {
		t.Errorf("a --dry-run launch fetched a pack\nstdout:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Fetched pack") {
		t.Errorf("a --dry-run launch disclosed a fetch:\n%s", stdout.String())
	}
}

// (e) AT THE CALL SITE: a failed fetch with a cached copy reaches the WARN callback as the one
// "could not refresh" line. Deleting the warn(o.Warning()) loop from RefreshConfiguredPacks
// fails here; the packsrc tests pin only the line's text.
func TestRefreshConfiguredPacksWarnsWhenAFetchFailsOverACachedCopy(t *testing.T) {
	home := hostRefreshEnv(t)
	repo := refreshGitRepo(t)
	writeUserPacks(t, home, `[{"name": "gp", "source": "git+file://`+repo+`?ref=main"}]`)
	RefreshConfiguredPacks(func(string) {}, func(string) {})
	// Stale the branch's stamp and take the remote away, so the next refresh fetches and fails.
	if err := os.RemoveAll(filepath.Join(paths.PacksDir(), "stamps")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(repo, repo+".gone"); err != nil {
		t.Fatal(err)
	}
	var said, warned []string
	RefreshConfiguredPacks(func(s string) { said = append(said, s) }, func(s string) { warned = append(warned, s) })
	if len(warned) != 1 || !strings.Contains(warned[0], "pack gp: could not refresh main") ||
		!strings.Contains(warned[0], "using the cached") {
		t.Errorf("warn got %q (say got %q), want the one cached-copy warning", warned, said)
	}
}

// THE LAUNCH'S STORE is bounded by LaunchFetchTimeout and Detached. The struct half pins the
// constructor; the behavioral half pins that RefreshConfiguredPacks fetches THROUGH it: a git
// on PATH records whether it ran as the leader of its own session, which only Detached does.
func TestLaunchRefreshRunsGitDetachedWithTheLaunchBudget(t *testing.T) {
	if s := launchStore(); s.Timeout != packsrc.LaunchFetchTimeout || !s.Detached {
		t.Errorf("launchStore() = %+v, want Timeout %v and Detached", s, packsrc.LaunchFetchTimeout)
	}
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc: the session check below reads /proc/<pid>/stat")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	home := hostRefreshEnv(t)
	repo := refreshGitRepo(t)
	writeUserPacks(t, home, `[{"name": "gp", "source": "git+file://`+repo+`?ref=main"}]`)
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "sessions")
	script := "#!/bin/sh\nread -r _ _ _ _ _ sid _ < /proc/$$/stat\necho \"$$ $sid\" >> '" + log +
		"'\nexec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	RefreshConfiguredPacks(func(string) {}, func(string) {})
	data, err := os.ReadFile(log)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		t.Fatalf("the refresh ran no git through PATH: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if f := strings.Fields(line); len(f) != 2 || f[0] != f[1] {
			t.Errorf("git ran inside the caller's session (pid sid = %q): not Detached", line)
		}
	}
}
