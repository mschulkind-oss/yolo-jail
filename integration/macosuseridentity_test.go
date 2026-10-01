package integration

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// RUNBOOK ITEM 1'S TWIN: WHOSE SANDBOX A LAUNCH STARTS, AND WHERE
// (docs/plans/runbooks/macos-user-manual-checks.md item 1, §0.5;
// docs/plans/handoff-macos-user-open-threads.md §3).
//
// THE GAP. Every other launch test behind the gate asks what a sandbox can see or do, and none
// asks whose it is. So the scheduled macos-user job started sandboxes every night without ever
// asserting that `sudo --user=_yolojail` landed, that the `cd` into the workspace in
// macosuser.LaunchArgv's inner shell ran, or that the home is the account's rather than the
// invoking user's. Runbook item 1 is that question asked by hand, and it passed on hardware on
// 2026-09-10 and 2026-09-12; nothing repeated it unattended.
//
// WHAT IT ASSERTS, on one probe: `whoami` and `id -un` are macosuser.SandboxUser, the numeric
// uid is the one the host resolves for that account (so a name that happened to print right
// over the wrong uid still fails), the PHYSICAL working directory is the workspace the fixture
// minted (resolved where it was minted, per AGENTS.md's darwin path-resolution rule), and $HOME
// is macosuser.SandboxHome(). Two of those are the item's own; uid and HOME cost nothing on the
// same probe and are what "whose" means.
func TestMacosUserLaunchRunsAsTheSandboxAccountInTheWorkspace(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{}`)

	wantUID := macosUserAccountUID(t)
	r := runMacosUser(t, ws, macosUserIdentityProbe())
	if r.rc != 0 {
		t.Fatalf("the macos-user launch failed (rc %d) before the identity probe could answer, "+
			"so nothing below says whose sandbox this was.\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	got := macosUserHomeProbeFields(t, r.stdout, "IDENTITY")
	want := map[string]string{
		"whoami": macosuser.SandboxUser,
		"id-un":  macosuser.SandboxUser,
		"uid":    wantUID,
		"pwd":    ws,
		"home":   macosuser.SandboxHome(),
	}
	why := map[string]string{
		"whoami": "the launch's `sudo --user=" + macosuser.SandboxUser + "` did not land: the " +
			"sandbox runs as another account, so every grant and every deny this backend " +
			"computes for that account is about somebody else",
		"id-un": "the same question as whoami, asked of the kernel's credential rather than of " +
			"the login record",
		"uid": "the name printed is right and the uid is not the account the host knows by that " +
			"name, so the account the launch switched to is not the one macos-setup created",
		"pwd": "the sandbox did not start in the workspace: macosuser.LaunchArgv's inner shell " +
			"`cd`s into it before exec'ing the agent, and an agent started elsewhere edits the " +
			"wrong tree",
		"home": "the sandbox's $HOME is not the account home, so the layout, the overlay and the " +
			"login rc files the bootstrap laid there are not what the agent reads",
	}
	for key, w := range want {
		if got[key] != w {
			t.Errorf("%s|%s, want %s: %s.\nfull output:\n%s", key, got[key], w, why[key], r.stdout)
		}
	}
	if got["uid"] == strconv.Itoa(os.Getuid()) {
		t.Errorf("the sandbox runs as the invoking user's uid %s: there is no privilege "+
			"transition at all, so this is not a sandbox account's session", got["uid"])
	}
}

// macosUserIdentityProbe prints one `key|value` line per fact, fenced the way
// macosUserHomeProbeFields reads. Each fact is a command's own output, with its error folded in,
// so a missing tool reads as a wrong value rather than as an empty line.
func macosUserIdentityProbe() string {
	return strings.Join([]string{
		`echo "=== IDENTITY ==="`,
		`echo "whoami|$(whoami 2>&1)"`,
		`echo "id-un|$(id -un 2>&1)"`,
		`echo "uid|$(id -u 2>&1)"`,
		`echo "pwd|$(pwd -P 2>&1)"`,
		`echo "home|$HOME"`,
		`echo "=== END IDENTITY ==="`,
	}, "\n")
}

// macosUserAccountUID is the sandbox account's uid as the HOST resolves it, the authority the
// in-sandbox `id -u` is compared against.
func macosUserAccountUID(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "id", "-u", macosuser.SandboxUser).Output()
	if err != nil {
		t.Fatalf("resolving %s's uid on the host: %v (the gate found the account, so this is "+
			"a directory-service fault, not a missing setup)", macosuser.SandboxUser, err)
	}
	return strings.TrimSpace(string(out))
}

// TestMacosUserIdentityProbeParsesAnUnsandboxedRun is the Linux preflight of the probe above,
// the shape TestMacosUserContentProbeReadsARealLayout keeps: the real script, run UNSANDBOXED by
// this process in a directory it minted, must report this process's own user, uid, directory
// and home. It proves the script and its parser, never the sandbox. Not behind
// requireMacosUser, for that preflight's reason: counting it would let the macOS job pass its
// "something ran" check with no sandbox started.
func TestMacosUserIdentityProbeParsesAnUnsandboxedRun(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH")
	}
	// The expected name is this uid's as `id -un` resolves it, asked separately: the probe's
	// job here is to print and parse it, and a uid with no name (a bare uid in a chroot) has
	// no answer to compare against.
	name, err := exec.Command("id", "-un").Output()
	if err != nil {
		t.Skipf("this uid has no user name to compare against: %v", err)
	}
	me := strings.TrimSpace(string(name))
	dir := resolvedTempDir(t)
	home := resolvedTempDir(t)
	cmd := exec.Command("bash", "-c", macosUserIdentityProbe())
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the probe did not run cleanly: %v\n%s", err, out)
	}
	got := macosUserHomeProbeFields(t, string(out), "IDENTITY")
	for key, want := range map[string]string{
		"whoami": me,
		"id-un":  me,
		"uid":    strconv.Itoa(os.Getuid()),
		"pwd":    dir,
		"home":   home,
	} {
		if got[key] != want {
			t.Errorf("%s|%s unsandboxed, want %s — the probe or its parser misreports the one "+
				"case whose answer is known.\n%s", key, got[key], want, out)
		}
	}
}
