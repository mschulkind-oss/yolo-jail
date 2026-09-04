//go:build darwin

package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runScript runs a WorkspaceGrantedScript for real and reports its exit status.
func runScript(t *testing.T, script string) bool {
	t.Helper()
	return exec.Command("bash", "-c", script).Run() == nil
}

// THE PROBE MUST BE TESTED AGAINST REAL `ls -lde` OUTPUT, and this file exists
// because the first cut of it was not. Its unit test stubbed RunBash, so the
// grep never ran against anything, and it shipped matching only the literal
// "group:<g> allow" — while `ls` renders an INHERITED ace as
// "group:<g> inherited allow". The result was a false NEGATIVE on exactly the
// case the check exists to wave through: a workspace that inherited correctly.
//
// A stub cannot catch that class. Only running the real script against a real
// directory can, so these do.
func TestWorkspaceGrantedScriptAgainstRealDirs(t *testing.T) {
	root := t.TempDir()
	group := "staff" // a group every macOS account is in; the ACE shape is what matters

	// A directly-applied inheritable ACE — the shape `macos-setup` puts on the
	// shared root, rendered by ls WITHOUT the "inherited" keyword.
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	ace := "group:" + group + " allow list,add_file,search,delete,add_subdirectory," +
		"delete_child,readattr,writeattr,file_inherit,directory_inherit"
	if err := exec.Command("chmod", "+a", ace, parent).Run(); err != nil {
		t.Skipf("cannot apply an ACL here (%v) — filesystem may not support them", err)
	}

	// A child that INHERITED it — rendered WITH the "inherited" keyword. This is
	// the case the shipped bug got wrong.
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}

	// A sibling outside the ACL'd parent: no grant at all.
	ungranted := filepath.Join(root, "ungranted")
	if err := os.Mkdir(ungranted, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		dir  string
		want bool
	}{
		{"directly applied ace", parent, true},
		{"INHERITED ace", child, true},
		{"no ace", ungranted, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runScript(t, WorkspaceGrantedScript(tc.dir, group)); got != tc.want {
				out, _ := exec.Command("/bin/ls", "-lde", tc.dir).Output()
				t.Errorf("probe = %v, want %v\nls -lde:\n%s", got, tc.want, out)
			}
		})
	}
}

// NOT TESTED, and it cannot be: an ACE naming a principal that no longer exists.
// `chmod` refuses to WRITE one ("Unable to translate '<uuid>' to a UUID"), so the
// state only arises by deleting the principal after the ACE exists — which a test
// cannot stage without creating and destroying an account. A permanently-skipping
// test would be worse than this comment (docs/plans/roadmap.md: "a green test that
// never ran is not evidence").
//
// What it looks like in the wild, measured 2026-09-03: every directory under the
// shared root carried inherited ACEs for uuid 0E7B72D7-… , which resolves to no
// user and no group, left behind when the _yolojail account was recreated with a
// fresh GeneratedUID. `ls` prints the raw uuid rather than "group:_yolojail", so
// the probe matches nothing and reports NOT granted — the right answer, since such
// an ACE grants the current sandbox account exactly nothing.

// The setup-time check lists exactly the children that are not shared, and says
// nothing when they all are. Bounded by the number of workspaces, so it runs the
// real script against a real directory tree rather than a stub — the same lesson
// the probe above was fixed for.
func TestUngrantedChildrenScriptListsOnlyTheUnshared(t *testing.T) {
	root := t.TempDir()
	ace := "group:staff allow list,add_file,search,delete,add_subdirectory," +
		"delete_child,readattr,writeattr,file_inherit,directory_inherit"

	// Names deliberately share no substring: "unshared-ws" CONTAINS "shared-ws",
	// which made the first version of this test fail on its own assertion.
	shared := filepath.Join(root, "granted-ws")
	unshared := filepath.Join(root, "missing-ws")
	for _, d := range []string{shared, unshared} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := exec.Command("chmod", "+a", ace, shared).Run(); err != nil {
		t.Skipf("cannot apply an ACL here (%v)", err)
	}

	// Exit 0 means "found at least one unshared child".
	out, _ := exec.Command("bash", "-c", UngrantedChildrenScript(root, "staff")).CombinedOutput()
	if !strings.Contains(string(out), "missing-ws") {
		t.Errorf("did not report the unshared workspace:\n%s", out)
	}
	if strings.Contains(string(out), "granted-ws") {
		t.Errorf("reported a workspace that IS shared:\n%s", out)
	}
	if !runScriptFound(t, UngrantedChildrenScript(root, "staff")) {
		t.Error("exit status did not signal that an unshared child was found")
	}

	// With every child shared it must stay quiet, or setup prompts on every run.
	if err := exec.Command("chmod", "+a", ace, unshared).Run(); err != nil {
		t.Fatal(err)
	}
	if runScriptFound(t, UngrantedChildrenScript(root, "staff")) {
		out, _ := exec.Command("bash", "-c", UngrantedChildrenScript(root, "staff")).CombinedOutput()
		t.Errorf("still reports unshared children when all are shared:\n%s", out)
	}

	// An EMPTY root must also stay quiet: a fresh machine has no workspaces, and a
	// prompt there would be the first thing a new user sees.
	if runScriptFound(t, UngrantedChildrenScript(t.TempDir(), "staff")) {
		t.Error("reported unshared children under an empty root")
	}
}

// runScriptFound reports whether the script exited 0 ("found something").
func runScriptFound(t *testing.T, script string) bool {
	t.Helper()
	return exec.Command("bash", "-c", script).Run() == nil
}
