//go:build darwin

package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
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
