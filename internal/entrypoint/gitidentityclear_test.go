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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
// is one log-only note per key and nothing on the terminal (launchLogged says where the test
// reads that note from, and what that does not prove).
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
			t.Errorf("the launch did not log removing %s:\n%s", key, logged)
		}
		if strings.Contains(said, key) {
			t.Errorf("a clear is a log-only note, not a terminal line, and the terminal got %s:\n%s", key, said)
		}
	}
}

// wantNothingSaidAboutTheIdentity fails if a launch's terminal output mentions the git identity
// or its record: what every warning forwardIdentity and configureGit can print names.
func wantNothingSaidAboutTheIdentity(t *testing.T, when, said string) {
	t.Helper()
	for _, word := range []string{"user.email", "user.name", "yolo-forwarded", "git identity"} {
		if strings.Contains(said, word) {
			t.Errorf("%s: the launch said something about %s, and should have said nothing:\n%s", when, word, said)
		}
	}
}

// (b) AN IDENTITY THE USER SET IN THE JAIL STAYS, launch after launch, whether the host never
// set one or set one the user then changed. And a launch with nothing to clear SAYS nothing.
//
// The first case fails on an unset of every key the host leaves out; the second on a clear that
// unsets a recorded key whatever value the file holds now (no --fixed-value), which deleted the
// user's own address because yolo had once forwarded a different one.
//
// Both fail if a key the record holds no entry for (git exits 1, with no record at all and with
// a record that lacks the key) is read as a failed read: every launch from a host that sets no
// name or email then warned once per key.
func TestAGitIdentityTheUserSetInTheMacosUserJailIsKept(t *testing.T) {
	const mine = "mine@example.com"
	t.Run("on a host that never set one", func(t *testing.T) {
		h := newIdentityHome(t)
		said, _ := h.launch(t, nil)
		wantNothingSaidAboutTheIdentity(t, "a first launch from a host with no identity", said)
		h.setByHand(t, "user.email", mine)
		for _, when := range []string{"a second launch", "a third launch"} {
			said, _ = h.launch(t, nil)
			wantNothingSaidAboutTheIdentity(t, when+" from a host with no identity", said)
		}
		h.wantValue(t, "after two launches from a host with no email", "user.email", mine)
	})
	t.Run("over the one yolo forwarded", func(t *testing.T) {
		h := newIdentityHome(t)
		h.launch(t, forwardedBoth)
		h.setByHand(t, "user.email", mine)
		said, logged := h.launch(t, nil)
		wantNothingSaidAboutTheIdentity(t, "the launch that kept the user's email", said)
		said, _ = h.launch(t, nil)
		wantNothingSaidAboutTheIdentity(t, "a launch after the record dropped both keys", said)
		h.wantValue(t, "after two launches from a host that stopped setting it", "user.email", mine)
		// The name was not touched, so it is still yolo's and goes.
		h.wantAbsent(t, "the untouched key beside it", "user.name")
		if !strings.Contains(logged, "kept user.email") {
			t.Errorf("the launch did not log that the user's email was kept:\n%s", logged)
		}

		// A KEEP DROPS THE CLAIM TOO (row 3 of the rule table in docs/reference/git-identity.md).
		// The address yolo once forwarded, typed by the user after the keep, is the user's, and a
		// launch from a host that still sets none keeps it. It fails if the exit-5 arm of the
		// clear leaves the key in the record: that launch removes the user's address.
		h.setByHand(t, "user.email", firstEmail)
		h.launch(t, nil)
		h.wantValue(t, "the address yolo once forwarded, typed by the user after a keep", "user.email", firstEmail)
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

// ---------------------------------------------------------------------------
// The record itself: what the launch says when it cannot read, write or trust it.
// ---------------------------------------------------------------------------

// gitRefusingRecord is a git that fails `--file <record> … <op> …` with exit code rc, as a
// git that cannot lock or read the record would, and passes everything else to the real git.
// Only the record is ever named with --file here, so the global config's writes go through.
func gitRefusingRecord(t *testing.T, op string, rc int) string {
	t.Helper()
	return fakeBin(t, "git", `f=; o=
for a in "$@"; do
  case "$a" in --file) f=1 ;; `+op+`) o=1 ;; esac
done
if [ -n "$f" ] && [ -n "$o" ]; then echo 'error: could not lock config file' >&2; exit `+strconv.Itoa(rc)+`; fi
exec '`+gitBin+`' "$@"`)
}

// recordRefusal is the sentence every refusal of the record carries, after its path.
const recordRefusal = ", the record of the git name and email yolo forwarded, "

// wantRecordRefusedOnce fails unless said refuses record exactly once, naming problem.
func wantRecordRefusedOnce(t *testing.T, when, said, record, problem string) {
	t.Helper()
	if n := strings.Count(said, record+recordRefusal); n != 1 {
		t.Errorf("%s: the record is refused %d times, want once:\n%s", when, n, said)
	}
	if !strings.Contains(said, record+recordRefusal+problem) {
		t.Errorf("%s: the refusal does not say the record %s:\n%s", when, problem, said)
	}
}

// configureGitWithin runs configureGit and fails the test if it has not returned within a
// bound. A git still waiting to open fifo is released by opening the FIFO for writing, again
// until configureGit returns, so a failing run leaves no git behind.
func configureGitWithin(t *testing.T, e *Env, fifo string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		configureGit(e)
	}()
	select {
	case <-done:
		return
	case <-time.After(20 * time.Second):
	}
	t.Errorf("configureGit was still running after 20s: a git is waiting to open %s", fifo)
	for {
		if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		select {
		case <-done:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// A RECORD GIT CANNOT READ IS REFUSED ONCE PER LAUNCH, with the command that ends it, and the
// identity is still forwarded. git exits 128 on every read and write of such a file. That came
// back per key: each forwarded key warned at every launch, offering only the hand unset inside
// the jail, which does not stop the warning; and on the launch where the host stopped setting
// a key, the failed read looked like no entry and the clear was skipped in silence.
//
// It fails if the launch stops checking that git can read the record (each key's own write
// warning comes back instead), if the refusal stops offering the record's removal, and if the
// launch after that removal does not start a new record that a later clear acts on.
func TestAGitIdentityRecordGitCannotReadIsRefusedOnceWithItsRemedy(t *testing.T) {
	h := newIdentityHome(t)
	h.launch(t, forwardedBoth)
	record := filepath.Join(h.ws, ".yolo", "home", "config", "git", "yolo-forwarded")
	if err := os.WriteFile(record, []byte("garbage[[\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	said, _ := h.launch(t, forwardedBoth)
	wantRecordRefusedOnce(t, "a launch forwarding the identity", said, record, "is not a file git can read")
	if !offers(t, said, "sudo", "rm", record) {
		t.Errorf("the refusal does not offer `sudo rm %s` as a shell reads it:\n%s", record, said)
	}
	if strings.Contains(said, "could not record") {
		t.Errorf("each forwarded key warned on its own, past the record's refusal:\n%s", said)
	}
	h.wantValue(t, "the identity, forwarded past the unreadable record", "user.email", firstEmail)

	said, _ = h.launch(t, nil)
	wantRecordRefusedOnce(t, "the launch where the host stopped setting it", said, record, "is not a file git can read")
	h.wantValue(t, "a value no readable record claims", "user.email", firstEmail)

	// The remedy, then a launch that forwards the identity again and one that does not.
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	said, _ = h.launch(t, forwardedBoth)
	if strings.Contains(said, record) {
		t.Errorf("the launch after the record was removed still names it:\n%s", said)
	}
	h.launch(t, nil)
	h.wantAbsent(t, "after the record was removed and a new one taken", "user.email")
}

// A RECORD THAT IS NOT A FILE IS REFUSED, WITHOUT WAITING ON IT. git reads a directory as an
// empty config and refuses to write one, so a clear was skipped in silence; and git opening a
// FIFO waits for a writer that never comes, holding the launch with it. Each is refused with
// the command that removes what is there: a directory takes -r.
//
// The directory case fails if the launch stops checking what sits at the record (git's own
// refusal of the directory comes back, offering an rm that cannot remove it); the FIFO case
// fails, after its bound, if configureGit hands git the FIFO.
func TestAGitIdentityRecordThatIsNotAFileIsRefusedWithoutWaiting(t *testing.T) {
	cases := []struct {
		name    string
		plant   func(record string) error
		problem string
		remedy  []string // the command, the record's path appended
	}{
		{
			name:    "a directory",
			plant:   func(record string) error { return os.MkdirAll(filepath.Join(record, "inside"), 0o755) },
			problem: "is a directory",
			remedy:  []string{"sudo", "rm", "-r"},
		},
		{
			name:    "a FIFO",
			plant:   func(record string) error { return syscall.Mkfifo(record, 0o644) },
			problem: "is not a regular file",
			remedy:  []string{"sudo", "rm"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hermeticGit(t)
			e, stderr, _ := gitEnv(t)
			e.Workspace = gitRepo(t)
			e.Vars["YOLO_GIT_EMAIL"] = firstEmail
			configureGit(e)
			record := filepath.Join(e.Home, ".config", "git", "yolo-forwarded")
			if err := os.Remove(record); err != nil {
				t.Fatalf("the first launch kept no record: %v", err)
			}
			if err := tc.plant(record); err != nil {
				t.Skipf("planting %s: %v", tc.name, err)
			}

			delete(e.Vars, "YOLO_GIT_EMAIL")
			stderr.Reset()
			configureGitWithin(t, e, record)

			wantRecordRefusedOnce(t, "a launch with "+tc.name+" at the record", stderr.String(), record, tc.problem)
			if !offers(t, stderr.String(), append(tc.remedy, record)...) {
				t.Errorf("the refusal does not offer `%s %s` as a shell reads it:\n%s",
					strings.Join(tc.remedy, " "), record, stderr)
			}
			h := identityHome{home: e.Home}
			h.wantValue(t, "a value no readable record claims", "user.email", firstEmail)
		})
	}
}

// A RECORD WRITE THAT FAILS IS SAID, with what it costs and the command that pays it: without
// the entry, a host that stops setting the key leaves it in the jail. The identity itself is
// still forwarded. It fails if the failure is only a log-only note, or is not said at all.
func TestAGitIdentityRecordWriteThatFailsIsReported(t *testing.T) {
	hermeticGit(t)
	e, stderr, _ := gitEnv(t)
	e.Workspace = gitRepo(t)
	e.Vars["YOLO_GIT_EMAIL"] = firstEmail
	e.Vars[DarwinLoginPathEnv] = gitRefusingRecord(t, "--replace-all", 4)
	configureGit(e)

	mustContain(t, "a record write that failed", stderr, "could not record that yolo set git user.email")
	if !offers(t, stderr.String(), "git", "config", "--global", "--unset-all", "user.email") {
		t.Errorf("the warning does not offer the command that removes the email by hand:\n%s", stderr)
	}
	h := identityHome{home: e.Home}
	h.wantValue(t, "the identity whose record failed", "user.email", firstEmail)
}

// A CLEAR WHOSE RECORD CANNOT DROP THE KEY IS SAID, with the command that removes the record.
// The value is gone, but the claim stayed, so a later launch would remove the same value again
// even if the user typed it by hand. It fails if the failure is only a log-only note, or stops
// offering the record's removal.
func TestAGitIdentityClearWhoseRecordKeepsTheClaimSaysHowToRemoveIt(t *testing.T) {
	hermeticGit(t)
	e, stderr, _ := gitEnv(t)
	e.Workspace = gitRepo(t)
	e.Vars["YOLO_GIT_EMAIL"] = firstEmail
	configureGit(e)
	record := filepath.Join(e.Home, ".config", "git", "yolo-forwarded")

	delete(e.Vars, "YOLO_GIT_EMAIL")
	e.Vars[DarwinLoginPathEnv] = gitRefusingRecord(t, "--unset-all", 4)
	stderr.Reset()
	configureGit(e)

	mustContain(t, "a record that kept its claim", stderr, "could not drop git user.email")
	if !offers(t, stderr.String(), "sudo", "rm", record) {
		t.Errorf("the warning does not offer `sudo rm %s` as a shell reads it:\n%s", record, stderr)
	}
	h := identityHome{home: e.Home}
	h.wantAbsent(t, "the value the clear removed", "user.email")
}

// A RECORD READ THAT FAILS IS SAID AND RETRIED, never taken for "no entry": that skipped the
// clear in silence. The claim stays, so the next launch whose git can read the record removes
// the value. It fails if a failed read is treated as an absent entry.
func TestAGitIdentityRecordReadThatFailsIsReportedAndRetried(t *testing.T) {
	hermeticGit(t)
	realGit := gitBin
	e, stderr, _ := gitEnv(t)
	e.Workspace = gitRepo(t)
	e.Vars["YOLO_GIT_EMAIL"] = firstEmail
	configureGit(e)

	delete(e.Vars, "YOLO_GIT_EMAIL")
	e.Vars[DarwinLoginPathEnv] = gitRefusingRecord(t, "--get", 128)
	stderr.Reset()
	configureGit(e)

	mustContain(t, "a record read that failed", stderr, "could not read git user.email from")
	if !offers(t, stderr.String(), "git", "config", "--global", "--unset-all", "user.email") {
		t.Errorf("the warning does not offer the command that removes the email by hand:\n%s", stderr)
	}
	h := identityHome{home: e.Home}
	h.wantValue(t, "after the failed read", "user.email", firstEmail)

	e.Vars[DarwinLoginPathEnv] = filepath.Dir(realGit)
	configureGit(e)
	h.wantAbsent(t, "at the next launch, whose git could read the record", "user.email")
}
