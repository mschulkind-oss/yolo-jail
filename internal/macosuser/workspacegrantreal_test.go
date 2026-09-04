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

// A stale grant — an ace naming a principal that no longer resolves — must be
// detectable, because that is what a teardown+setup cycle leaves on every
// workspace and it is invisible to the eye: `ls -le` prints such an ace in the
// same shape as a live one, just with a uuid where the name would be.
//
// Synthesizing a DEAD principal is impossible (chmod refuses to write one), so
// this pins the detector's other half: it must not fire on a directory whose aces
// all resolve. The positive case is covered by the shape assertion below plus the
// live measurement recorded in StaleGrantScript's comment.
func TestStaleGrantScriptIgnoresResolvableAces(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ace := "group:staff allow list,add_file,search,readattr,file_inherit,directory_inherit"
	if err := exec.Command("chmod", "+a", ace, dir).Run(); err != nil {
		t.Skipf("cannot apply an ACL here (%v)", err)
	}
	if runScript(t, StaleGrantScript(dir)) {
		out, _ := exec.Command("/bin/ls", "-lde", dir).Output()
		t.Errorf("reported a stale grant on a directory whose aces all resolve:\n%s", out)
	}
	// And a directory with no ACL at all is not stale either.
	plain := filepath.Join(root, "plain")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if runScript(t, StaleGrantScript(plain)) {
		t.Error("reported a stale grant on a directory with no ACL")
	}
}

// The detector keys on a uuid in the PRINCIPAL position, which is what `ls`
// prints when it cannot resolve one. Pinned separately because the positive case
// cannot be staged: a regex that matched nothing would pass the negative test
// above while detecting nothing in the wild.
func TestStaleGrantScriptMatchesRealUnresolvableOutput(t *testing.T) {
	// The exact shape `ls -lde` produced on the affected machine 2026-09-03.
	sample := " 0: 0E7B72D7-D737-43C8-B1DB-5D8E8C7CA00F inherited allow list,add_file\n"
	script := StaleGrantScript("/dev/null")
	// Extract the regex the script greps for and run it against the real sample.
	i, j := strings.Index(script, "grep -qE '"), strings.LastIndex(script, "'")
	if i < 0 || j <= i {
		t.Fatalf("cannot find the pattern in the script: %s", script)
	}
	pattern := script[i+len("grep -qE '") : j]
	cmd := exec.Command("grep", "-qE", pattern)
	cmd.Stdin = strings.NewReader(sample)
	if err := cmd.Run(); err != nil {
		t.Errorf("the pattern does not match real unresolvable-ace output.\npattern: %s\nsample:  %s",
			pattern, sample)
	}
	// And it must NOT match a resolvable one, or every launch reports a stale grant.
	cmd = exec.Command("grep", "-qE", pattern)
	cmd.Stdin = strings.NewReader(" 0: group:_yolojail inherited allow list,add_file\n")
	if err := cmd.Run(); err == nil {
		t.Errorf("the pattern matches a RESOLVABLE ace: %s", pattern)
	}
}
