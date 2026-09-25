package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/check"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostpackrefresh_test.go pins the HOST NOTCH's launch-time pack refresh at its CALL SITES:
// `yolo host apply`, `yolo apply --at host` and `yolo host -- <bin>` each fetch a git pack
// nobody installed before they resolve it, and the read-only surfaces (`yolo check`,
// `yolo host env`) never fetch. Each positive test fails when its call to refreshHostPacks is
// deleted: the pack is then "never fetched" and the render refuses, or nothing is recorded.

// neverInstalledGitPackHome is gitPackHome over a REAL git repo, not installed — the state a
// user is in the moment they add a git pack to `packs`.
func neverInstalledGitPackHome(t *testing.T, extra string) string {
	t.Helper()
	repo := gitPackRepo(t)
	home := gitPackHome(t, "git+file://"+repo+"//tools/agent-pack?ref=main", extra)
	t.Setenv("YOLO_VERSION", "")
	return home
}

// mirrorCount is how many repositories the pack store has fetched.
func mirrorCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(paths.PacksDir(), "mirrors"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// lockedCommit is the commit the lockfile records for name, or "".
func lockedCommit(t *testing.T, name string) string {
	t.Helper()
	l, err := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
	if err != nil {
		t.Fatal(err)
	}
	e, _ := l.Get(name)
	return e.Commit
}

// `yolo host apply --assert` renders a git pack that was never installed: the command fetched
// it first, and said so on stderr.
func TestHostApplyFetchesANeverInstalledGitPack(t *testing.T) {
	home := neverInstalledGitPackHome(t, "")
	var out, errw bytes.Buffer
	rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n"))
	report := out.String() + errw.String()
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "gitskill", "SKILL.md")); err != nil {
		t.Errorf("the never-installed git pack's skill was not rendered: %v\n%s", err, report)
	}
	if !strings.Contains(errw.String(), "Fetched pack gp: main → ") {
		t.Errorf("the fetch was not disclosed on stderr:\n%s", report)
	}
}

// The systematic spelling is the same operation (OQ-7), so it fetches too.
func TestApplyAtHostFetchesANeverInstalledGitPack(t *testing.T) {
	home := neverInstalledGitPackHome(t, "")
	var out, errw bytes.Buffer
	rc := applyMain([]string{"--at", "host", "--assert"}, &out, &errw, false, strings.NewReader("y\n"))
	report := out.String() + errw.String()
	if rc != 0 {
		t.Fatalf("apply --at host --assert rc=%d\n%s", rc, report)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "gitskill", "SKILL.md")); err != nil {
		t.Errorf("the never-installed git pack's skill was not rendered: %v\n%s", err, report)
	}
}

// In JSON mode the disclosure goes to stderr, so stdout is still exactly one document.
func TestHostApplyJSONKeepsTheFetchDisclosureOffStdout(t *testing.T) {
	neverInstalledGitPackHome(t, "")
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--format", "json"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if strings.Contains(out.String(), "Fetched pack") {
		t.Errorf("the disclosure reached the JSON stdout:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "Fetched pack gp") {
		t.Errorf("the disclosure is missing from stderr:\n%s", errw.String())
	}
}

// `yolo host -- <bin>` fetches before the gate and the composition resolve packs: the pack is
// recorded in the lockfile and disclosed, and the launch proceeds to the PATH lookup.
func TestHostExecFetchesANeverInstalledGitPack(t *testing.T) {
	neverInstalledGitPackHome(t, "")
	var out, errw bytes.Buffer
	rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil)
	if rc != 127 {
		t.Fatalf("rc = %d, want 127 (the PATH lookup)\n%s%s", rc, out.String(), errw.String())
	}
	if len(lockedCommit(t, "gp")) != 40 {
		t.Errorf("the wrapped launch did not fetch and record the pack\n%s", errw.String())
	}
	if !strings.Contains(errw.String(), "Fetched pack gp: main → ") || out.Len() != 0 {
		t.Errorf("the fetch must be disclosed on stderr only; stdout=%q stderr=%q", out.String(), errw.String())
	}
}

// THE READ-ONLY SURFACES NEVER FETCH: `yolo host env` (an observe verb), the agent footer's
// host pack read, the use_profiles CLI-name universe, `yolo config promote`'s fold, and
// `yolo check` (whose Packs section is pinned separately, in internal/cli/check). Every one
// resolves the selected packs; none may reach the network.
func TestReadOnlySurfacesDoNotFetchPacks(t *testing.T) {
	neverInstalledGitPackHome(t, "")

	var out, errw bytes.Buffer
	hostMain([]string{"env"}, &out, &errw, false, nil)
	_ = footerHostPacks()
	_, _ = config.UseProfileCLINames()
	_, unresolved := loadPromoteFold()
	if len(unresolved) != 1 || unresolved[0].Name != "gp" {
		// Anti-vacuity: the fold really asked about the pack, and found it missing.
		t.Fatalf("promote's fold did not see the never-fetched pack: %+v", unresolved)
	}

	opts := check.NewDefaultOptions()
	opts.Build = false
	opts.Stdout, opts.Stderr = &out, &errw
	opts.Now = func() time.Time { return time.Unix(0, 0) }
	opts.Getenv = func(string) string { return "" }
	opts.LookPath = func(string) (string, bool) { return "", false }
	opts.Exec = func([]string, string, []string, time.Duration) check.ExecResult { return check.ExecResult{} }
	out.Reset()
	check.Check(opts)
	if !strings.Contains(out.String(), "Parsed user config") {
		t.Fatalf("`yolo check` never read the user config naming the pack:\n%s", out.String())
	}

	if n := mirrorCount(t); n != 0 {
		t.Errorf("a read-only surface fetched %d pack mirror(s)", n)
	}
	if lockedCommit(t, "gp") != "" {
		t.Error("a read-only surface recorded the pack in the lockfile")
	}

	// The control: the same home DOES fetch through a launch-shaped entry, so the zero
	// above is about the surfaces rather than about a pack that could never be fetched.
	refreshHostPacks(&errw)
	if n := mirrorCount(t); n != 1 {
		t.Errorf("control: the refresh fetched %d mirrors, want 1", n)
	}
}
