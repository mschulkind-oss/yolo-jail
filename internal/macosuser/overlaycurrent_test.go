package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// writableAtCleanup is t.TempDir whose tree is made writable again before the test's own removal,
// since the stage seals it (no write bit, directories included) and a non-root user cannot unlink
// inside a sealed directory. Cleanups run last-registered first, so this runs before TempDir's.
func writableAtCleanup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(p, fi.Mode().Perm()|0o700)
			}
			return nil
		})
	})
	return dir
}

// hostOverlayWithDigest lays a one-destination host overlay out the way the run pipeline's builder
// does, its destination list carrying digest.
func hostOverlayWithDigest(t *testing.T, digest string) string {
	t.Helper()
	dir := writableAtCleanup(t)
	if err := os.MkdirAll(filepath.Join(dir, ".pi", "agent", "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".pi", "agent", "skills", "demo", "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := entrypoint.WriteHomeOverlayManifestDigest(dir, []string{".pi/agent/skills"}, digest); err != nil {
		t.Fatal(err)
	}
	return dir
}

// execArgvs runs argvs for real, as the host user runs the stage's commands.
func execArgvs(t *testing.T, cmds [][]string) int {
	t.Helper()
	for _, argv := range cmds {
		if _, err := os.Stat(argv[0]); err != nil {
			t.Skipf("%s is not here: %v", argv[0], err)
		}
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
	}
	return len(cmds)
}

// THROUGH THE REAL STAGE COMMANDS (plain cp, chmod and mv, which run on Linux as on a Mac): a
// staged copy of the same overlay is current, a changed digest is not, and an overlay never
// staged is not.
func TestAStagedOverlayIsCurrentOnlyForTheSameDigest(t *testing.T) {
	sd := writableAtCleanup(t)
	host := hostOverlayWithDigest(t, "aaaa")
	if HomeOverlayStagedCurrent(host, "ws", sd) {
		t.Fatal("an overlay never staged reported current")
	}
	execArgvs(t, StageHomeOverlayCommands(host, "ws", sd))
	if !HomeOverlayStagedCurrent(host, "ws", sd) {
		t.Fatal("a freshly staged copy of the same overlay is not current")
	}
	// The staged copy is sealed: no write bit anywhere in it.
	fi, err := os.Stat(filepath.Join(StagedHomeOverlay("ws", sd), ".pi", "agent", "skills", "demo", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o222 != 0 {
		t.Fatalf("the staged file is %v, want no write bit", fi.Mode().Perm())
	}

	changed := hostOverlayWithDigest(t, "bbbb")
	if HomeOverlayStagedCurrent(changed, "ws", sd) {
		t.Fatal("a staged copy with another digest reported current")
	}
	execArgvs(t, StageHomeOverlayCommands(changed, "ws", sd))
	if !HomeOverlayStagedCurrent(changed, "ws", sd) {
		t.Fatal("the re-staged copy is not current")
	}
	if HomeOverlayStagedCurrent(changed, "other-workspace", sd) {
		t.Fatal("another workspace's staged copy stood in for this one's")
	}
}

// A RE-STAGING INTERRUPTED WHILE IT REMOVES THE OLD COPY never leaves that copy's digest behind:
// the list goes before anything else of it, so a later launch whose overlay has the OLD digest
// re-stages rather than delivering a half-removed tree.
func TestAnInterruptedRestagingLeavesNoTrustedDigest(t *testing.T) {
	sd := writableAtCleanup(t)
	oldHost := hostOverlayWithDigest(t, "aaaa")
	execArgvs(t, StageHomeOverlayCommands(oldHost, "ws", sd))
	cmds := StageHomeOverlayCommands(hostOverlayWithDigest(t, "bbbb"), "ws", sd)
	cut := -1
	for i, c := range cmds {
		if len(c) > 3 && c[3] == dropListScriptName {
			cut = i
		}
	}
	if cut < 0 {
		t.Fatalf("no command drops the old copy's list first:\n%v", cmds)
	}
	execArgvs(t, cmds[:cut+1]) // the launch dies right after
	if _, err := os.Stat(filepath.Join(StagedHomeOverlay("ws", sd), ".pi")); err != nil {
		t.Fatalf("the fixture lost the old copy's content before its removal: %v", err)
	}
	if HomeOverlayStagedCurrent(oldHost, "ws", sd) {
		t.Fatal("a half-removed staged copy still carries a digest a launch trusts")
	}
}

// An overlay with no digest (one an older yolo built) is never current, so it is re-staged.
func TestAnOverlayWithoutADigestIsNeverCurrent(t *testing.T) {
	sd := writableAtCleanup(t)
	host := hostOverlayWithDigest(t, "")
	execArgvs(t, StageHomeOverlayCommands(host, "ws", sd))
	if HomeOverlayStagedCurrent(host, "ws", sd) {
		t.Fatal("a digest-less overlay reported current")
	}
}

// THE CALL SITE: a launch whose staged copy is current runs no overlay copy, and one whose copy is
// stale runs it — through RunMacosUser, so dropping the orchestrator's question fails here.
func TestALaunchStagesTheOverlayOnlyWhenItsStagedCopyIsStale(t *testing.T) {
	for _, current := range []bool{true, false} {
		t.Run(map[bool]string{true: "current", false: "stale"}[current], func(t *testing.T) {
			orig := homeOverlayStagedCurrent
			var asked string
			homeOverlayStagedCurrent = func(tree, cname, sd string) bool {
				asked = tree
				return current
			}
			t.Cleanup(func() { homeOverlayStagedCurrent = orig })

			var rec []string
			d := mockDeps(&rec)
			ws := "/Users/Shared/yolo/proj"
			fakeSupervisor(&d, &rec, ws, "", readyLine, "", false)
			d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
			o := stageTimingOpts(t, ws)
			if rc := RunMacosUser(d, o); rc != 42 {
				t.Fatalf("rc = %d", rc)
			}
			if asked != o.HostHomeOverlay.Tree {
				t.Fatalf("the launch asked about %q, want the host overlay %q", asked, o.HostHomeOverlay.Tree)
			}
			copied := false
			for _, r := range rec {
				if strings.HasPrefix(r, "run:"+cpBin+" -R "+o.HostHomeOverlay.Tree+" ") {
					copied = true
				}
			}
			if copied == current {
				t.Fatalf("staged copy current=%v, overlay copied=%v; commands:\n%s", current, copied, strings.Join(rec, "\n"))
			}
		})
	}
}
