package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// TestMacosUserGitHubBrokerRunsAReadAgainstTheHostGH is TestGitHubBrokerRunsAReadAgainstTheHostGH
// on the macos-user backend, on a Mac: the same script, the same stand-in host gh and the same
// checker (githubbroker_test.go), so a difference between the two backends shows up as one of
// the same assertions failing rather than as a second test drifting from the first.
//
// WHAT ONLY THIS TEST CAN SEE, none of it run before: that a macos-user launch starts the
// github-broker host daemon behind yolo's front with this launch's scope file; that the pack's
// `gh` forwarder lands in the sandbox's block dir, ahead of anything else named gh; that the
// staged yolo inside Seatbelt reaches the daemon at the endpoint the launch handed the sandbox,
// with the connection preamble the audit keys on; that the broker runs the HOST user's gh with
// the canonical argv, GH_HOST pinned and no token in its environment; and that the scope file
// goes with the launch.
//
// THE SCOPE IS APPROVED BY THE LAUNCH ITSELF. runMacosUser passes --accept-config-changes, which
// writes the approval record's scope part at the gate (BB-D30), so this test needs no separate
// `yolo check` the way the podman fixture's recordScope is; the launch's own disclosure line is
// asserted instead.
//
// NOTHING REACHES GITHUB. A GitHub-hosted runner carries a real gh, so the stand-in is put first
// on this process's PATH, which the launcher and the daemon it spawns inherit, and the test
// refuses to go on unless `gh` resolves to it here.
//
// The run store is shared by every test of the run (runstore_test.go), so the audit log is read
// for this workspace's lines only.
func TestMacosUserGitHubBrokerRunsAReadAgainstTheHostGH(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["github"]}`)
	ws := macosUserWorkspace(t, `{}`)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ()))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("remote", "add", "origin", "https://github.com/yolo-it/app.git")
	// The broker on for THIS workspace alone, by the command a user runs in it (OQ-BB13).
	// YOLO_VERSION is blanked because this is the host's verb.
	if r := runCommand(t, ws, []string{"loopholes", "enable", "github-broker"},
		withEnv("YOLO_VERSION=")); r.rc != 0 || !strings.Contains(r.stdout, "github-broker is on for ") {
		t.Fatalf("yolo loopholes enable github-broker: rc %d\n%s", r.rc, r.combined())
	}

	bin := resolvedTempDir(t)
	argvLog := writeFakeHostGH(t, bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got, err := exec.LookPath("gh"); err != nil || got != filepath.Join(bin, "gh") {
		t.Fatalf("`gh` resolves to %q (%v) on the launcher's PATH, not the stand-in in %s: the "+
			"broker would run a real gh against GitHub, which no test may do", got, err, bin)
	}

	r := runMacosUser(t, ws, githubBrokerReadScript())
	if line := "github-broker: scope for this workspace: yolo-it/app"; !strings.Contains(r.combined(), line) {
		t.Errorf("the launch did not say it started the broker with the approved scope (%q):\n%s%s",
			line, r.combined(), brokerDaemonLog(t))
	}
	assertGitHubBrokerRead(t, r, ws, argvLog, ws)
}
