package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
)

var durableNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// durableCheckFixture is a workspace whose durable dir holds two worktrees — one made in the
// jail (the jail's spelling in its admin file), one on the host — and a notes directory.
func durableCheckFixture(t *testing.T) (ws, dir string) {
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
	mk := func(p string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for id, spec := range map[string]struct {
		gitdir, head string
		idle         time.Duration
	}{
		"land":       {"/workspace/.yolo/durable/worktrees/land/.git", "1a2b3c4d5e6f7a8b", 2 * 24 * time.Hour},
		"fix-footer": {filepath.Join(dir, "worktrees", "fix-footer", ".git"), "ref: refs/heads/fix-footer", 41 * 24 * time.Hour},
	} {
		mk(filepath.Join(dir, "worktrees", id, ".git"), []byte("gitdir: x\n"))
		mk(filepath.Join(dir, "worktrees", id, "src", "a.go"), make([]byte, 1500))
		admin := filepath.Join(ws, ".git", "worktrees", id)
		mk(filepath.Join(admin, "gitdir"), []byte(spec.gitdir+"\n"))
		mk(filepath.Join(admin, "HEAD"), []byte(spec.head+"\n"))
		when := durableNow.Add(-spec.idle)
		if err := os.Chtimes(filepath.Join(admin, "HEAD"), when, when); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(dir, "notes", "n.md"), []byte("x"))
	return ws, dir
}

func runDurableSection(o *Options, ws string) string {
	var buf bytes.Buffer
	o.sectionDurableDir(newReporter(&buf, false), ws)
	return buf.String()
}

// ON THE HOST the git-derived columns say "in-jail only" and no git runs at all: the
// workspace's .git/config is jail-writable (docs/design/durable-scratch-space.md §4). Size,
// idle and branch come from metadata and git's admin files, and a worktree made in the jail —
// whose admin file names /workspace/… — is found at the host's own path.
func TestTheHostDurableSectionRunsNoGit(t *testing.T) {
	ws, dir := durableCheckFixture(t)
	var ran []string
	o := &Options{
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return durableNow },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			ran = append(ran, strings.Join(argv, " "))
			return ExecResult{Ran: true}
		},
	}
	out := runDurableSection(o, ws)
	for _, want := range []string{
		"Durable dir\n",
		"  " + dir + "  3 kB measured, 2 worktrees, 1 other entry\n",
		"  worktrees/fix-footer  1 kB measured  idle 41 days  fix-footer  in-jail only  in-jail only\n",
		"  worktrees/land  1 kB measured  idle 2 days  detached at 1a2b3c4d  in-jail only  in-jail only\n",
		"  other entries  notes\n",
		"yolo deletes nothing here.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the host section does not say %q:\n%s", want, out)
		}
	}
	if len(ran) != 0 {
		t.Errorf("host yolo ran %q in a jail-writable repository", ran)
	}
}

// IN THE JAIL every column is computed: unique commits by rev-list excluding the worktree's
// own branch, changed files by `git status --porcelain`.
func TestTheInJailDurableSectionComputesEveryColumn(t *testing.T) {
	_, dir := durableCheckFixture(t)
	env := map[string]string{"YOLO_VERSION": "9.9.9", durable.EnvVar: dir, "YOLO_HOST_DIR": "/host/ws"}
	var ran []string
	o := &Options{
		Getenv: func(k string) string { return env[k] },
		Now:    func() time.Time { return durableNow },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			k := strings.Join(argv, " ")
			ran = append(ran, k)
			switch {
			case strings.Contains(k, "rev-list") && strings.Contains(k, "--exclude=fix-footer"):
				return ExecResult{Ran: true, Stdout: "3\n"}
			case strings.Contains(k, "rev-list"):
				return ExecResult{Ran: true, Stdout: "0\n"}
			case strings.Contains(k, "fix-footer status"):
				return ExecResult{Ran: true, Stdout: " M a\n?? b\n"}
			case strings.Contains(k, "status"):
				return ExecResult{Ran: true}
			}
			return ExecResult{}
		},
	}
	out := runDurableSection(o, "/elsewhere")
	for _, want := range []string{
		"  worktrees/fix-footer  1 kB measured  idle 41 days  fix-footer  3 unique commits  2 changed files\n",
		"  worktrees/land  1 kB measured  idle 2 days  detached at 1a2b3c4d  0 unique commits  clean\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the in-jail section does not say %q:\n%s", want, out)
		}
	}
	for _, k := range ran {
		if strings.Contains(k, "rev-list") && !strings.HasSuffix(k, "--branches --remotes") {
			t.Errorf("unique commits counted against the wrong refs: %q", k)
		}
	}
}

// A workspace with no durable dir gets no section: a check of a workspace that never
// launched reads as it did before the durable dir existed.
func TestTheDurableSectionIsSilentWithoutTheDir(t *testing.T) {
	o := &Options{Getenv: func(string) string { return "" }, Now: time.Now}
	if out := runDurableSection(o, t.TempDir()); out != "" {
		t.Errorf("printed %q with no durable dir", out)
	}
}
