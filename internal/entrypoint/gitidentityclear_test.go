package entrypoint

// gitidentityclear_test.go pins what happens on macos-user to a git name or email the host
// STOPS setting, against the real bootstrap, the real layout and real git.
//
// The global config is persistent on this backend: ~/.gitconfig is the layout's redirect into
// the workspace sidecar, which outlives every launch. The replay used to set a key only when the
// host forwarded one, so an email removed on the host stayed in the sandbox for good. The rule
// now is the deselection rule (docs/reference/providers.md): remove what yolo wrote, keep what
// the user wrote, and never claim a value twice.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// identityHome is one workspace's macos-user home, laid by the real bootstrap at every launch.
type identityHome struct {
	home, ws, packRoot string
}

func newIdentityHome(t *testing.T) identityHome {
	t.Helper()
	hermeticGit(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := identityHome{
		home:     filepath.Join(base, "home"),
		ws:       filepath.Join(base, "ws"),
		packRoot: stagePackForBootstrap(t, "claude"),
	}
	if err := os.MkdirAll(h.home, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(gitBin, "init", "-q", h.ws).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return h
}

// launch boots the workspace once, with the host forwarding identity (nothing, for a host
// that sets no identity), and returns what the bootstrap printed and what it logged.
func (h identityHome) launch(t *testing.T, identity map[string]string) (said, logged string) {
	t.Helper()
	return gitLayoutBoot(t, h.home, h.ws, h.packRoot, identity)
}

// git runs `git config --global <args>` the way the agent does inside the jail: ~/.gitconfig,
// followed through the layout's redirect and links to the sidecar's file. It returns the
// output and the exit code (1 for `--get` of an absent key).
func (h identityHome) git(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(gitBin, append([]string{"config", "--global"}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+h.home, "GIT_CONFIG_GLOBAL="+filepath.Join(h.home, ".gitconfig"))
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		t.Fatalf("git config --global %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), 0
}

// setByHand is the user, inside the jail, running `git config --global <key> <val>`.
func (h identityHome) setByHand(t *testing.T, key, val string) {
	t.Helper()
	if out, rc := h.git(t, key, val); rc != 0 {
		t.Fatalf("git config --global %s %s exited %d:\n%s", key, val, rc, out)
	}
}

// wantValue fails unless the jail's global config holds exactly val for key.
func (h identityHome) wantValue(t *testing.T, when, key, val string) {
	t.Helper()
	if got, rc := h.git(t, "--get", key); rc != 0 || got != val {
		t.Errorf("%s: %s = %q (rc %d), want %q", when, key, got, rc, val)
	}
}

// wantAbsent fails unless the jail's global config holds no value for key, which is what
// `git config --global <key>` exiting 1 means.
func (h identityHome) wantAbsent(t *testing.T, when, key string) {
	t.Helper()
	if got, rc := h.git(t, "--get", key); rc != 1 {
		t.Errorf("%s: %s = %q (rc %d), want no value at all (rc 1)", when, key, got, rc)
	}
}

const (
	firstName  = "First One"
	firstEmail = "first@example.com"
)

var forwardedBoth = map[string]string{"YOLO_GIT_NAME": firstName, "YOLO_GIT_EMAIL": firstEmail}

// (a) A NAME AND EMAIL THE HOST STOPS SETTING LEAVE THE JAIL at the next launch, and the rest of
// the file stays: an alias the user set in the jail, and the safe.directory entry. The removal
// is one boot-log line per key and nothing on the terminal.
//
// It fails on the replay that only ever set a key (the email stayed, and git kept committing as
// the address the user removed on the host); it fails if the record is not written when a key
// is set, since a key with no record is never cleared; and it fails if the clear rewrites the
// file whole, which takes the user's alias with it.
func TestAGitIdentityTheHostStopsSettingLeavesTheMacosUserJail(t *testing.T) {
	h := newIdentityHome(t)
	h.launch(t, forwardedBoth)
	h.wantValue(t, "after a launch forwarding it", "user.email", firstEmail)
	h.wantValue(t, "after a launch forwarding it", "user.name", firstName)
	h.setByHand(t, "alias.st", "status")

	said, logged := h.launch(t, nil)

	h.wantAbsent(t, "after the host stopped setting it", "user.email")
	h.wantAbsent(t, "after the host stopped setting it", "user.name")
	h.wantValue(t, "the user's own entry, after the clear", "alias.st", "status")
	if out, rc := gitAsAnotherOwner(h.home, h.ws, "status"); rc != 0 {
		t.Errorf("the clear cost the workspace its safe.directory entry (rc %d):\n%s", rc, out)
	}
	for _, key := range []string{"user.email", "user.name"} {
		if !strings.Contains(logged, "removed "+key) {
			t.Errorf("the boot log does not record removing %s:\n%s", key, logged)
		}
		if strings.Contains(said, key) {
			t.Errorf("a clear is a boot-log line, not a terminal one, and the terminal got %s:\n%s", key, said)
		}
	}
}

// (b) AN IDENTITY THE USER SET IN THE JAIL STAYS, launch after launch, whether the host never
// set one or set one the user then changed.
//
// The first case fails on an unset of every key the host leaves out; the second on a clear that
// unsets a recorded key whatever value the file holds now (no --fixed-value), which deleted the
// user's own address because yolo had once forwarded a different one.
func TestAGitIdentityTheUserSetInTheMacosUserJailIsKept(t *testing.T) {
	const mine = "mine@example.com"
	t.Run("on a host that never set one", func(t *testing.T) {
		h := newIdentityHome(t)
		h.launch(t, nil)
		h.setByHand(t, "user.email", mine)
		h.launch(t, nil)
		h.launch(t, nil)
		h.wantValue(t, "after two launches from a host with no email", "user.email", mine)
	})
	t.Run("over the one yolo forwarded", func(t *testing.T) {
		h := newIdentityHome(t)
		h.launch(t, forwardedBoth)
		h.setByHand(t, "user.email", mine)
		_, logged := h.launch(t, nil)
		h.launch(t, nil)
		h.wantValue(t, "after two launches from a host that stopped setting it", "user.email", mine)
		// The name was not touched, so it is still yolo's and goes.
		h.wantAbsent(t, "the untouched key beside it", "user.name")
		if !strings.Contains(logged, "kept user.email") {
			t.Errorf("the boot log does not say the user's email was kept:\n%s", logged)
		}
	})
}

// (c) A KEY THE HOST SETS AGAIN AFTER A CLEAR IS FORWARDED AGAIN, and is yolo's again: the next
// clear removes it too. It fails if a clear leaves anything that stops a later forward from
// being written or recorded.
func TestAGitIdentityTheHostSetsAgainAfterAClearIsForwardedAgain(t *testing.T) {
	h := newIdentityHome(t)
	h.launch(t, forwardedBoth)
	h.launch(t, nil)
	h.wantAbsent(t, "after the clear", "user.email")

	h.launch(t, forwardedBoth)
	h.wantValue(t, "after the host set it again", "user.email", firstEmail)

	h.launch(t, nil)
	h.wantAbsent(t, "after the host cleared it a second time", "user.email")
}

// A CLEAR DROPS THE CLAIM. Once yolo removed its value it claims nothing, so the same address
// the user types by hand afterwards is the user's, and a later launch from a host that still
// sets none keeps it. It fails if the key stays in the record after a clear.
func TestAClearedGitIdentityIsNoLongerYolos(t *testing.T) {
	h := newIdentityHome(t)
	h.launch(t, forwardedBoth)
	h.launch(t, nil)
	h.setByHand(t, "user.email", firstEmail)

	h.launch(t, nil)

	h.wantValue(t, "the same address, typed by the user after the clear", "user.email", firstEmail)
}

// A CLEAR THAT FAILS IS SAID, with the command that does it by hand, and keeps the claim, so the
// next launch whose git can write removes the value after all. The failing git refuses only
// `--global … --unset-all`, the one write the clear makes, and passes everything else to the
// real git.
func TestAGitIdentityClearThatFailsIsReportedAndRetried(t *testing.T) {
	hermeticGit(t)
	realGit := gitBin
	e, stderr, _ := gitEnv(t)
	e.Workspace = gitRepo(t)
	e.Vars["YOLO_GIT_EMAIL"] = firstEmail
	configureGit(e)

	refusing := fakeBin(t, "git", `for a in "$@"; do
  case "$a" in --global) g=1 ;; --unset-all) u=1 ;; esac
done
if [ -n "$g" ] && [ -n "$u" ]; then echo 'error: could not lock config file' >&2; exit 4; fi
exec '`+realGit+`' "$@"`)
	delete(e.Vars, "YOLO_GIT_EMAIL")
	e.Vars[DarwinLoginPathEnv] = refusing
	configureGit(e)

	mustContain(t, "a failed clear", stderr, "could not remove git user.email")
	if !offers(t, stderr.String(), "git", "config", "--global", "--unset-all", "user.email") {
		t.Errorf("the warning does not offer the command that removes the email by hand:\n%s", stderr)
	}
	h := identityHome{home: e.Home}
	h.wantValue(t, "after the failed clear", "user.email", firstEmail)

	e.Vars[DarwinLoginPathEnv] = filepath.Dir(realGit)
	configureGit(e)
	h.wantAbsent(t, "at the next launch, whose git could write", "user.email")
}
