package entrypoint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
)

// THE LAUNCH LINE IS A STEP OF BOTH BOOTS, from the one table: the call site is the table
// entry, so deleting it, or excluding a boot from it, fails here — the durable dir is
// exported on podman, Apple Container and macos-user alike, and each prints its report.
func TestTheDurableReportIsAStepOfBothBoots(t *testing.T) {
	var step *bootStep
	for _, s := range bootSteps() {
		if s.name == "report_durable_dir" {
			s := s
			step = &s
		}
	}
	if step == nil {
		t.Fatal("the boot step table has no report_durable_dir: no launch reports the durable dir")
	}
	for _, target := range []bootTarget{bootContainer, bootDarwin} {
		if why := step.excludedFrom(target); why != "" {
			t.Errorf("the %s boot skips the durable report: %s", target, why)
		}
	}

	ws, dir := durableFixture(t)
	var stderr bytes.Buffer
	e := NewEnv(map[string]string{durable.EnvVar: dir})
	e.Workspace = ws
	e.Stderr = &stderr
	runSteps(&bootRun{e: e, target: bootContainer}, []bootStep{*step})
	if !strings.HasPrefix(stderr.String(), "Durable dir: 1 entry, 1 B — ") {
		t.Errorf("the step printed %q", stderr.String())
	}
}

// Worktrees made on the host record the host's spelling of the workspace; in the jail that
// is YOLO_HOST_DIR, and the report maps it to /workspace, so a live worktree is counted and a
// gone one outside the durable dir is reported as prunable.
func TestTheDurableReportMapsTheHostSpellingAndFindsGoneRegistrations(t *testing.T) {
	ws, dir := durableFixture(t)
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	host := "/home/someone/code/proj"
	if err := os.MkdirAll(filepath.Join(dir, "worktrees", "land"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worktrees", "land", ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	register := func(id, gitdir string) {
		admin := filepath.Join(ws, ".git", "worktrees", id)
		if err := os.MkdirAll(admin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(admin, "gitdir"), []byte(gitdir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		head := filepath.Join(admin, "HEAD")
		if err := os.WriteFile(head, []byte("ref: refs/heads/"+id+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(head, now.Add(-3*24*time.Hour), now.Add(-3*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	register("land", host+"/.yolo/durable/worktrees/land/.git")
	// Gone, and inside the workspace: judged. A tree under the jail's /tmp is judged the same
	// way (prune's slots are the gone roots too); a fixture cannot rely on t.TempDir() being
	// there, so the in-workspace case stands for both.
	gone := filepath.Join(ws, ".claude", "worktrees", "gone-land")
	register("gone", gone+"/.git")
	// Gone from here, but beside the repository on the host, where a container jail cannot
	// look: never called gone, which it was at every launch.
	// (A literal path: the fixture's own temp dir is under /tmp, which IS a gone root.)
	register("sib", "/home/someone/code/proj-wt/sib/.git")

	var stderr bytes.Buffer
	e := NewEnv(map[string]string{durable.EnvVar: dir, "YOLO_HOST_DIR": host})
	e.Workspace = ws
	e.Stderr = &stderr
	reportDurableDir(e, func() time.Time { return now })
	out := stderr.String()
	for _, want := range []string{
		"Durable dir: 1 worktree and 1 other entry, ",
		"oldest idle 3 days (land) — `yolo check` lists them; yolo deletes nothing here.",
		"Durable dir: 1 worktree registration points at paths that no longer exist (" + gone + ")",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

// No variable, no line: the launcher said why the launch has no durable dir.
func TestTheDurableReportIsSilentWithoutTheVariable(t *testing.T) {
	var stderr bytes.Buffer
	e := NewEnv(map[string]string{})
	e.Stderr = &stderr
	reportDurableDir(e, time.Now)
	if stderr.Len() != 0 {
		t.Errorf("printed %q with no durable dir", stderr.String())
	}
}

// durableFixture is a workspace whose durable dir holds one entry, a note.
func durableFixture(t *testing.T) (ws, dir string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws = filepath.Join(base, "ws")
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err = durable.Ensure(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws, dir
}
