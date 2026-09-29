package macosuser

import (
	"bytes"
	"strings"
	"testing"
)

// THE ORDERING TEST. A workspace under a user's home fails BOTH the in-home rule and the ACL
// probe — nothing shares a home with the sandbox group — and the two refusals name opposite
// remedies: the probe's names `yolo macos-fix-permissions <workspace>`, which refuses every
// path under a home. So which one a launch asks first decides whether the user gets a fix or
// a loop (the launch says run the fix, the fix says no, the launch says it again).
//
// It fails if the two checks are swapped back: the probe would run (it is recorded), and its
// refusal, not the move, would be what the user reads.
func TestAnInHomeWorkspaceIsRefusedBeforeTheACLProbeRuns(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := grantDeps(&rec, &buf) // the ACL probe FAILS, as it does for every path under a home
	materialized := false
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		materialized = true
		return mockDarwin(), true, nil
	}

	rc := RunMacosUser(d, newOpts("/Users/matt/code/proj"))

	if rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	if strings.Contains(strings.Join(rec, "\n"), "ls -lde") {
		t.Error("the ACL probe ran before the in-home refusal — its remedy is one that refuses " +
			"this path, which is the loop")
	}
	got := buf.String()
	for _, want := range []string{
		"/Users/matt/code/proj is inside the home folder /Users/matt",
		"mv /Users/matt/code/proj /Users/Shared/yolo/proj",
		"yolo macos-fix-permissions /Users/Shared/yolo/proj",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "is not shared with the sandbox user") ||
		strings.Contains(got, "macos-fix-permissions /Users/matt") {
		t.Errorf("the refusal sends the user to macos-fix-permissions for the path it refuses:\n%s", got)
	}
	if materialized {
		t.Error("reached the nix build for a workspace the launch refuses")
	}
}

// The rule stands on its own, not only as the ACL probe's tiebreak: with the probe PASSING (an
// ACE someone added by hand), a workspace under a home is still refused before the nix build.
// Until this ordering it was caught only by PlanInvariants, which a launch reaches AFTER that
// build — up to half an hour to be told to move the project.
func TestAnInHomeWorkspaceIsRefusedBeforeTheNixBuild(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec) // every probe passes
	d.Out = &buf
	materialized := false
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		materialized = true
		return mockDarwin(), true, nil
	}

	if rc := RunMacosUser(d, newOpts("/Users/matt/code/proj")); rc != 1 {
		t.Fatalf("rc = %d, want 1\n%s", rc, buf.String())
	}
	if materialized {
		t.Error("reached the nix build for a workspace under a home")
	}
	if !strings.Contains(buf.String(), "is inside the home folder /Users/matt") {
		t.Errorf("refused, but not for the home:\n%s", buf.String())
	}
}

// The refusal names a fix that REACHES the path it names: the fix-permissions it prints is one
// the command accepts, and the path it refuses is one the command would have refused. This is
// the property the old order broke, asserted against the command itself rather than a sentence.
func TestTheInHomeRefusalNamesAFixTheFixCommandAccepts(t *testing.T) {
	var buf bytes.Buffer
	d := mockDeps(nil)
	d.Out = &buf
	if rc := MacosFixPermissions(d, "/Users/matt/code/proj"); rc != 1 {
		t.Fatalf("macos-fix-permissions accepted a path under a home (rc %d), so this test's "+
			"premise is gone", rc)
	}
	buf.Reset()

	msg := inHomeWorkspaceRefusal("/Users/matt/code/proj", "/Users/matt")
	const dest = "/Users/Shared/yolo/proj"
	if !strings.Contains(msg, "yolo macos-fix-permissions "+dest) {
		t.Fatalf("the refusal names no fix-permissions for the destination:\n%s", msg)
	}
	if rc := MacosFixPermissions(d, dest); rc != 0 {
		t.Errorf("macos-fix-permissions refuses the destination the refusal names (rc %d):\n%s", rc, buf.String())
	}
}

// Two edge spellings: a path the shell would split is quoted in both commands, and a workspace
// that IS a home is never told to move the home.
func TestTheInHomeRefusalQuotesPathsAndNeverMovesAHome(t *testing.T) {
	msg := inHomeWorkspaceRefusal("/Users/matt/My Project", "/Users/matt")
	for _, want := range []string{
		"mv '/Users/matt/My Project' '/Users/Shared/yolo/My Project'",
		"yolo macos-fix-permissions '/Users/Shared/yolo/My Project'",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not say %q:\n%s", want, msg)
		}
	}
	whole := inHomeWorkspaceRefusal("/Users/other", "/Users/other")
	if strings.Contains(whole, "mv ") {
		t.Errorf("the refusal tells the user to move a whole home:\n%s", whole)
	}
	if !strings.Contains(whole, SharedRootDefault()) {
		t.Errorf("the refusal for a home names no neutral ground:\n%s", whole)
	}
}
