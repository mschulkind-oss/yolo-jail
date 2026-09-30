//go:build linux

package integration

// detachkeys_linux_test.go is the end-to-end pin on JL-D27
// (docs/design/jail-lifetime-last-session-wins.md §2.3 item 2): podman's detach sequence,
// `ctrl-p` then `ctrl-q`, typed in a session must reach the session's process instead of
// detaching the session's client and leaving that process running in the jail with no
// terminal. The unit tier pins that every run and exec argv carries `--detach-keys=`
// (internal/runtime's TestEveryRunAndExecIntoAJailTurnsOffTheDetachSequence); only a real pty in
// front of a real podman client proves the flag does what podman's help says.
//
// Linux-only for the pty, as attachskewrestart_linux_test.go is.

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// inJailCatCount is a script counting the `cat -v` processes a jail holds, from /proc, since
// the image need not carry ps.
const inJailCatCount = `n=0; for p in /proc/[0-9]*; do ` +
	`c=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null); case "$c" in "cat -v "*) n=$((n+1));; esac; done; ` +
	`echo "CATS=$n"`

// TestTheDetachSequenceNeverLeavesAnAttachedSessionHeadless: a second terminal attaches to a
// running jail at a pty and runs `cat -v`. The detach sequence typed there, one key at a time as a
// person types it, reaches cat (which shows `^P`; the jail's terminal takes `ctrl-q` as flow
// control), the session stays attached, and an end of input ends it with status 0 and no `cat`
// left in the jail. With podman's default the client would have detached at `ctrl-q`: the attach
// would return at once and cat would run on with no terminal.
func TestTheDetachSequenceNeverLeavesAnAttachedSessionHeadless(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)

	const release = "release-first"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-IN-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() { writeRelease(t, dir, release) })
	awaitOutput(t, first, regexp.MustCompile(`FIRST-IN-42`))
	awaitLaunchLockReleased(t, dir, first)

	master, slave := openTestPty(t)
	cmd := exec.Command(yoloBin, append(jailRunArgs(), "--", "sh", "-c",
		`echo DK-READY-$((40+2)); exec cat -v`)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.Env = append(cmd.Env, childRepoRootEnv()...)
	cmd.Env = append(cmd.Env, autoCaptureEnvForSuite()...)
	awaitDetachedWriters(t, dir, launchHome(cmd.Env))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting yolo: %v", err)
	}
	_ = slave.Close()
	out := &syncBuffer{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				_, _ = out.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})

	deadline := time.Now().Add(jailTimeout())
	for !strings.Contains(out.String(), "DK-READY-42") {
		select {
		case err := <-done:
			t.Fatalf("the attach exited (%v) before its command ran:\n%s", err, out)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the attach's command did not start within %s:\n%s", jailTimeout(), out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "Attaching to existing jail") {
		t.Fatalf("the second terminal did not attach:\n%s", out)
	}
	// One key per write, with a pause, so each arrives in a read of its own: podman recognizes
	// the sequence only then, which is how a person types it.
	for _, key := range []byte{0x10, 0x11} {
		time.Sleep(300 * time.Millisecond)
		if _, err := master.Write([]byte{key}); err != nil {
			t.Fatalf("typing %#x: %v", key, err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := master.Write([]byte("hello\r")); err != nil {
		t.Fatalf("typing hello: %v", err)
	}
	for !strings.Contains(out.String(), "^Phello") {
		select {
		case err := <-done:
			t.Fatalf("the attach returned (%v) after the detach sequence: its client detached "+
				"from the session:\n%s", err, out)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("cat never showed the typed ^P and hello:\n%s", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("the attach returned (%v) while its session still ran:\n%s", err, out)
	default:
	}
	// The end of input ends cat, and with it the session.
	if _, err := master.Write([]byte{0x04}); err != nil {
		t.Fatalf("typing ctrl-d: %v", err)
	}
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatalf("waiting for yolo: %v", err)
		}
		if err != nil {
			t.Errorf("the attach ended %v, want status 0 from cat:\n%s", err, out)
		}
	case <-time.After(jailTimeout()):
		t.Fatalf("the attach did not end after ctrl-d:\n%s", out)
	}

	count := runYolo(t, dir, inJailCatCount)
	if !strings.Contains(count.stdout, "CATS=0") {
		t.Errorf("a cat is still running in the jail after its session ended:\n%s", count.combined())
	}
	writeRelease(t, dir, release)
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the first session ended rc %d:\n%s", rc, first.combined())
	}
}
