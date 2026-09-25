package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// TestLaunchFetchesANeverInstalledGitPack is the launch-time pack fetch end to end in a real
// container (maintainer ruling, 2026-09-25): a user config naming a git pack that nobody ever
// ran `yolo pack install` for launches with that pack DELIVERED — its skill mounted in the
// jail — and the launch discloses the fetch. Before the ruling this launch refused with
// "run `yolo pack install`".
//
// The remote is a git repository served over git+file://, so the fetch is real git with no
// network. The unit tests pin the ref rule (internal/packsrc/refresh_test.go) and the call
// site (internal/cli/run/packrefresh_test.go); only a started container proves the fetched
// tree is what gets staged, mounted and composed.
//
// SKIPPED INSIDE A JAIL, and not as a convenience: the refresh is a deliberate no-op there
// (the jail has no pack store and no git credentials), so a nested launch of this fixture
// refuses exactly as it should. It runs wherever the launcher is on a host, which is CI.
func TestLaunchFetchesANeverInstalledGitPack(t *testing.T) {
	requireJail(t)
	if os.Getenv("YOLO_VERSION") != "" {
		t.Skip("inside a yolo jail the launch-time pack refresh is a no-op by design; this runs on a host")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	skill := filepath.Join(repo, "skills", "fetched-demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"),
		[]byte("---\nname: fetched-demo\ndescription: from a fetched pack\n---\n# Fetched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "pack"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_AUTHOR_NAME=t",
			"GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", {"name": "fetched", "source": "git+file://`+repo+`?ref=main"}]}`)

	r := runYolo(t, dir, `ls /home/agent/.claude/skills/fetched-demo/SKILL.md`)
	if r.rc != 0 {
		t.Fatalf("a launch with a never-installed git pack failed: rc %d\nstdout: %s\nstderr: %s",
			r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "SKILL.md") {
		t.Errorf("the fetched pack's skill was not delivered:\n%s", r.combined())
	}
	if !strings.Contains(r.combined(), "Fetched pack fetched: main → ") {
		t.Errorf("the launch did not disclose the fetch:\n%s", r.combined())
	}
}
