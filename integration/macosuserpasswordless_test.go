package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// PASSWORDLESS START AND STOP (docs/design/macos-user-privilege-lifecycle-plan.md, task 9;
// the ruling is OQ-MP10 in docs/design/macos-user-privilege-lifecycle.md). Every root sudo on
// the launch and exit path became a host-user act in the host-owned staging root, and every
// crossing into the sandbox account goes through the one rule `yolo yolo-account create` installs,
// `<host user> ALL=(_yolojail) NOPASSWD: /usr/bin/env`. These tests are the measurement of that
// on a Mac: the first proves a launch starts, runs and tears down with root sudo out of reach;
// the second proves the staging root stays the host user's alone even to the sandbox account
// with no Seatbelt profile in the way.
//
// ⚠ NEITHER HAS RUN YET. Both were written on Linux; the first scheduled macos-user.yml run
// that reaches its "Narrow sudo to the enrolled rule" step is what says whether they hold.

// macosUserNarrowSudoEnv declares that root sudo on this machine needs a password, so a
// passwordless launch is a measurement rather than a broad rule passing it vacuously. Only
// macos-user.yml's narrow-sudo step and the owner's runbook set it.
const macosUserNarrowSudoEnv = "YOLO_TEST_MACOS_USER_NARROW_SUDO"

// TestMacosUserPasswordlessStartAndExit is the measurement OQ-MP10 left UNMEASURED: with root
// sudo out of reach, a launch with no terminal starts, runs and tears down. Declared by
// YOLO_TEST_MACOS_USER_NARROW_SUDO=1, which only the narrow-sudo CI step and the owner's
// runbook set; elsewhere it skips, because a broad rule would make it pass vacuously.
func TestMacosUserPasswordlessStartAndExit(t *testing.T) {
	requireMacosUser(t)
	if os.Getenv(macosUserNarrowSudoEnv) != "1" {
		t.Skip("set " + macosUserNarrowSudoEnv + "=1 once root sudo needs a password")
	}
	// A cached credential would pass both probes below through a broad rule, so drop it first.
	_ = exec.Command("/usr/bin/sudo", "-k").Run()
	if runQuiet(10*time.Second, "/usr/bin/sudo", "-n", "/usr/bin/true") {
		t.Fatal("root sudo still needs no password, so this run proves nothing; narrow it first")
	}
	if !runQuiet(10*time.Second, macosuser.RunAsGrantProbeArgv()...) {
		t.Fatal("the G2 probe fails: run `yolo yolo-account create` before this test")
	}
	ws := macosUserWorkspace(t, `{}`)
	before, _ := os.ReadDir(filepath.Join(macosuser.StateDir(), "env"))
	r := runMacosUser(t, ws, `echo "=== AGENT $(id -un) ==="`)
	if r.rc != 0 || !strings.Contains(r.stdout, "=== AGENT _yolojail ===") {
		t.Fatalf("the launch did not reach the agent as _yolojail (rc %d):\n%s", r.rc, r.combined())
	}
	if mentionsPassword(r.combined()) {
		t.Errorf("the launch mentioned a password:\n%s", r.combined())
	}
	after, _ := os.ReadDir(filepath.Join(macosuser.StateDir(), "env"))
	if len(after) > len(before) {
		t.Errorf("the session left %d env file(s) behind", len(after)-len(before))
	}
	fi, err := os.Lstat(macosuser.StateDir())
	if err != nil || fi.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		t.Errorf("the staging root is not this user's: %v", err)
	}
}

// mentionsPassword reports whether a launch's output says "password" as a WORD, the way a sudo
// prompt ("Password:", "[sudo] password for runner:") or a message about one does. A substring
// match is wrong here: the launch prints paths under its HOME, which is this test's t.TempDir(),
// and that directory is named after the test, TestMacosUserPasswordlessStartAndExit, so the
// macOS nightly of 2026-10-25 failed on the login keychain note quoting
// ".../TestMacosUserPasswordlessStartAndExit2679030572/001/Library/Keychains/...".
func mentionsPassword(out string) bool {
	return passwordWord.MatchString(out)
}

// passwordWord is "password" or "passwords", any case, between word boundaries.
var passwordWord = regexp.MustCompile(`(?i)\bpasswords?\b`)

// TestAPasswordMentionIsAWordNotThisTestsOwnName pins the matcher with no jail: the nightly's own
// line, which names only the test's temp directory, is not a mention; a sudo prompt is.
func TestAPasswordMentionIsAWordNotThisTestsOwnName(t *testing.T) {
	innocent := "Note: could not read your login keychain (no login keychain at /private/var/folders/n6/" +
		"hn37q6pj7sj_9dmqqc2113800000gn/T/TestMacosUserPasswordlessStartAndExit2679030572/001/Library/" +
		"Keychains/login.keychain-db), so a certificate authority you trusted only there is not trusted " +
		"in the sandbox this launch"
	if mentionsPassword(innocent) {
		t.Errorf("the test's own temp directory counts as a password mention:\n%s", innocent)
	}
	for _, prompt := range []string{"Password:", "[sudo] password for runner:", "sudo: a password is required",
		"Enter your passwords"} {
		if !mentionsPassword(prompt) {
			t.Errorf("%q is not counted as a password mention", prompt)
		}
	}
}

// TestMacosUserTheGuestCannotWriteTheStagingRoot is PL-2's measurement, the ruling's table
// redone at the real root: AS _yolojail, through the rule and under NO Seatbelt profile, so only
// the file modes and access lists answer, the sandbox account cannot create in the staging root
// or in any category directory a launch staged into, nor touch a staged profile; and it CAN read
// that profile and run the staged yolo, which are the two positive controls that keep every
// refusal from being a path that was never there.
//
// It holds a session open, because a profile and the session's env files exist only while one
// runs, and asks from the host side. A pack is selected so packs/ holds a staged tree.
func TestMacosUserTheGuestCannotWriteTheStagingRoot(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)
	sync := macosUserSyncDir(t, ws, ".yolo-it-staging-root")
	root := macosuser.StateDir()
	guestRoot := macosuser.GuestBinDir("")
	binsBefore, _ := os.ReadDir(guestRoot)
	profilesBefore, _ := filepath.Glob(filepath.Join(root, "profile-*.sb"))
	session := holdSession(t, ws, sync, heldSessionScript(sync, "", ""))
	session.awaitUp(t)

	profilesAfter, _ := filepath.Glob(filepath.Join(root, "profile-*.sb"))
	var profile, staged string
	for _, p := range profilesAfter {
		if !slices.Contains(profilesBefore, p) {
			profile = p
			break
		}
	}
	binsAfter, _ := os.ReadDir(guestRoot)
	for _, entry := range binsAfter {
		if slices.ContainsFunc(binsBefore, func(before os.DirEntry) bool { return before.Name() == entry.Name() }) {
			continue
		}
		candidate := filepath.Join(guestRoot, entry.Name(), "yolo")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			staged = candidate
			break
		}
	}
	if profile == "" {
		t.Fatalf("the session staged no new profile-*.sb under %s (before %v, after %v)",
			root, profilesBefore, profilesAfter)
	}
	if staged == "" {
		t.Fatalf("the live session staged no private yolo under %s (before %v, after %v)", guestRoot, binsBefore, binsAfter)
	}

	// THE POSITIVE CONTROLS, first: what the guest must be able to do through the same rule.
	if out, err := runAsSandboxAccount(t, "/bin/cat", profile); err != nil || !strings.Contains(out, "(version 1)") {
		t.Errorf("as %s, `cat %s` failed (%v), so the refusals below could be a rule that does "+
			"not run at all:\n%s", macosuser.SandboxUser, profile, err, out)
	}
	if out, err := runAsSandboxAccount(t, staged, "--version"); err != nil {
		t.Errorf("as %s, `%s --version` failed (%v): the guest cannot run its own staged yolo:\n%s",
			macosuser.SandboxUser, staged, err, out)
	}

	// THE REFUSALS. Each target must exist (as this user sees it), or a failed create proves
	// only that the directory was never made.
	probe := fmt.Sprintf(".yolo-it-guest-write-%d", os.Getpid())
	dirs := []string{root}
	for _, leaf := range []string{"bin", "packs", "env"} {
		dirs = append(dirs, filepath.Join(root, leaf))
	}
	// The other category directories (PL-2) are refused the same way when this launch made
	// them; a launch with no context mount or capture need not, so their absence is logged.
	for _, leaf := range []string{"home-overlay", "ctx", "captures"} {
		d := filepath.Join(root, leaf)
		if _, err := os.Stat(d); err != nil {
			t.Logf("%s is absent after this launch (%v); not probed", d, err)
			continue
		}
		dirs = append(dirs, d)
	}
	for _, d := range dirs {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("%s is not a directory this launch staged (%v), so the refusal below would "+
				"say nothing", d, err)
			continue
		}
		target := filepath.Join(d, probe)
		if out, err := runAsSandboxAccount(t, "/usr/bin/touch", target); err == nil {
			t.Errorf("as %s, `touch %s` SUCCEEDED: the sandbox account can create in the staging "+
				"root, which must be the host user's alone\n%s", macosuser.SandboxUser, target, out)
			_, _ = runAsSandboxAccount(t, "/bin/rm", "-f", target)
		}
	}
	if out, err := runAsSandboxAccount(t, "/usr/bin/touch", profile); err == nil {
		t.Errorf("as %s, `touch %s` SUCCEEDED: the sandbox account can write the profile it is "+
			"confined by\n%s", macosuser.SandboxUser, profile, out)
	}

	if r := session.release(t); r.rc != 0 {
		t.Errorf("the held session exited %d:\n%s", r.rc, lastLines(r.combined(), 60))
	}
}

// runAsSandboxAccount runs argv as the sandbox account through the one rule
// (macosuser.RunAsSandboxArgv), from "/" so a cwd the account cannot traverse decides nothing,
// and returns its combined output.
func runAsSandboxAccount(t *testing.T, argv ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := sandboxCommand(ctx, argv...)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("`%s` did not finish within 60s", strings.Join(cmd.Args, " "))
	}
	return string(out), err
}
