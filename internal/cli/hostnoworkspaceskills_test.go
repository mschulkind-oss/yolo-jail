package cli

// hostnoworkspaceskills_test.go pins the owner's ruling of 2026-11-07
// (docs/reference/agent-briefings.md WS-D24): the host has no workspace skills layer, so
// `yolo host -- <agent>` writes nothing into the workspace for skills — no link at the agent's
// project skills path, no .gitignore line. Both of the launch's hand-overs are driven through
// hostMain, the real front door, in a git work tree that keeps skills under ANOTHER agent's path
// (the shape the removed host half linked), and the workspace tree must be byte-for-byte what the
// test made.

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// wsTreeSnapshot lists every path under ws, with a link's target or a file's content, without
// following links.
func wsTreeSnapshot(t *testing.T, ws string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(ws, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(ws, p)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			snap[rel] = "link -> " + target
		case d.IsDir():
			snap[rel] = "dir"
		default:
			b, _ := os.ReadFile(p)
			snap[rel] = "file " + string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func assertWorkspaceUnchanged(t *testing.T, ws string, before map[string]string, errs string) {
	t.Helper()
	after := wsTreeSnapshot(t, ws)
	for p, v := range after {
		if before[p] != v {
			t.Errorf("the host launch wrote %s (%s) into the workspace:\n%s", p, v, errs)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			t.Errorf("the host launch removed %s from the workspace:\n%s", p, errs)
		}
	}
}

// The exec path: `yolo host -- codex` in a repository that keeps .claude/skills and has no
// .codex/skills and no .gitignore.
func TestHostLaunchWritesNothingIntoTheWorkspaceForSkills(t *testing.T) {
	hostGateHome(t, `{"packs": ["codex"], "host_floor": {"codex": false}}`, nil)
	ws, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ws, ".claude", "skills", "x", "SKILL.md"), "---\nname: x\ndescription: d\n---\nbody\n")
	bin := filepath.Join(t.TempDir(), "bin")
	writeFile(t, filepath.Join(bin, "codex"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	execed := false
	orig := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { execed = true; return nil }
	t.Cleanup(func() { hostSyscallExec = orig })
	before := wsTreeSnapshot(t, ws)

	var out, errw bytes.Buffer
	rc := hostMain([]string{"--", "codex"}, &out, &errw, false, nil)
	if rc != 0 || !execed {
		t.Fatalf("launch rc=%d execed=%v; the launch must reach the hand-over for this test to mean anything:\n%s",
			rc, execed, errw.String())
	}
	assertWorkspaceUnchanged(t, ws, before, errw.String())
}

// The resident path: a launch beside a launch-owned service (`-p codex -- claude`, the
// wire-bridge), in a repository that keeps .agents/skills and has no .claude/skills.
func TestAHostLaunchBesideALaunchOwnedServiceWritesNothingIntoTheWorkspaceForSkills(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	hostGateHome(t, codexConfig(t, upstream.URL), nil)
	ws, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ws, ".agents", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: d\n---\n")
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "claude"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	origExec := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { t.Error("the resident path exec'd"); return nil }
	t.Cleanup(func() { hostSyscallExec = origExec })
	before := wsTreeSnapshot(t, ws)

	var out, errw bytes.Buffer
	rc := hostMain([]string{"-p", "codex", "--", "claude"}, &out, &errw, false, nil)
	if rc != 0 {
		t.Fatalf("rc=%d:\n%s", rc, errw.String())
	}
	assertWorkspaceUnchanged(t, ws, before, errw.String())
}
