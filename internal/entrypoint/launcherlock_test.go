package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The install-prefix lock must be exclusive on a filesystem whose `mkdir` is not. uutils
// coreutils' mkdir (ubuntu-26.04 CI, recorded 2026-11-14) succeeds on a racing create, so a
// lock that treats a successful mkdir as ownership lets two launchers write one prefix. The
// fake mkdir below is that behavior in its purest form: it reports success for a lock path
// and creates nothing.

// fakeSuccessfulMkdir writes a mkdir that always succeeds for a lock path, and returns its
// directory, to be put first on PATH. Every other path goes to the real mkdir.
func fakeSuccessfulMkdir(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("mkdir")
	if err != nil {
		t.Skip("no mkdir")
	}
	dir := t.TempDir()
	body := "#!/bin/bash\nfor a in \"$@\"; do case \"$a\" in *yolo-update.lock*) exit 0 ;; esac; done\nexec " +
		shellQuoteForTest(real) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "mkdir"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// withFakeMkdir points a launcher command at the always-succeeding mkdir, in update mode (which
// ignores the per-start check marker and goes straight to the install-prefix lock).
func withFakeMkdir(cmd *exec.Cmd, fake string) *exec.Cmd {
	cmd.Env = []string{"HOME=" + cmd.Dir, "PATH=" + fake + ":" + os.Getenv("PATH"), "YOLO_PACK_UPDATE=1"}
	return cmd
}

// Two launchers on one prefix: while the first is inside its update, the second must find the
// lock held and not update, even though mkdir would let it in. THE TEST FAILS TODAY: both run
// the update.
func TestNativeLauncherPrefixLockIsExclusiveWhenMkdirIsNot(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	started, release := p.fifos(t)
	// Only the first update blocks; a second one (the bug) returns at once, so the failure is
	// a red assertion rather than a hang. The "first" claim is a noclobber create, not mkdir.
	first := filepath.Join(p.home, "first-update")
	body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " + shellQuoteForTest(p.log) + "\n" +
		"if [ \"${1:-}\" = update ] && ( set -o noclobber; : > " + shellQuoteForTest(first) + " ) 2>/dev/null; then echo up > " +
		shellQuoteForTest(started) + "; read -r _ < " + shellQuoteForTest(release) + "; fi\n"
	if err := os.WriteFile(p.realBin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := fakeSuccessfulMkdir(t)

	a := withFakeMkdir(p.command(t), fake)
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Wait() }()
	waitFifo(t, started, done)

	out, err := withFakeMkdir(p.command(t), fake).CombinedOutput()
	if n := countLines(p.argvLog(t), nativeUpdateLine); n != 1 {
		t.Errorf("%d launchers entered the update while the first held the prefix lock, want 1:\n%s", n, out)
	}
	if err == nil || !strings.Contains(string(out), "another update holds the install-prefix lock") {
		t.Errorf("the second launcher must be refused and say so (err=%v):\n%s", err, out)
	}
	openRelease(t, release)
	<-done
	if _, err := os.Stat(filepath.Join(p.home, ".local", ".yolo-update.lock")); !os.IsNotExist(err) {
		t.Errorf("the first launcher must release the lock it took (err=%v)", err)
	}
}

// A SIGTERM mid-update releases the install-prefix lock, and while the update runs the lock is
// held by an owner token.
func TestNativeLauncherReleasesThePrefixLockWhenSignalled(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	p.newJailStart(t)
	started, release := p.fifos(t)
	p.blockingUpdate(t, started, release)
	lock := filepath.Join(p.home, ".local", ".yolo-update.lock")

	cmd := p.command(t)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waitFifo(t, started, done)
	tok, err := os.ReadFile(lock)
	if err != nil || strings.TrimSpace(string(tok)) == "" {
		t.Fatalf("the held lock must be a file carrying its owner token, got %q (%v)", tok, err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	openRelease(t, release)
	<-done
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("a SIGTERM mid-act must release the prefix lock (err=%v)", err)
	}
}
