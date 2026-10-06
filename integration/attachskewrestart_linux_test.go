//go:build linux

package integration

// attachskewrestart_linux_test.go drives the attach contract gate's TERMINAL arm end to end: a
// real pty on the launch's stdin and stdout, the `Restart jail now? [Y/n]` question answered
// yes, the stand-in older jail stopped, and the same launch carrying on as a fresh one whose
// jail runs the command. The unit tier pins each step against a faked runtime
// (internal/cli/run/contracttags_test.go); this is the one place the steps run in sequence
// against podman, including the fresh launch's own TTY path after the prompt read its answer.
//
// Linux-only for the pty: it opens /dev/ptmx directly, as the provision and lingerprobe tests
// do, since the repo vendors no pty library.

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openTestPty returns a pty pair, skipping when the machine cannot allocate one — a sandbox
// without /dev/ptmx is not evidence either way.
func openTestPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	var unlock int32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, m.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		t.Skipf("unlockpt: %v", e)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		m.Close()
		t.Skipf("ptsname: %v", err)
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		t.Skipf("open slave: %v", err)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}

// privateYoloState replaces the isolated home's link to the machine's yolo-jail state
// directory with an empty one of this test's own.
//
// A launch at a terminal ASKS the machine-wide questions that store holds the answers to, the
// cache-reclaim offer first among them (internal/cli/run/offer.go), and this test must neither
// answer one (an answer is recorded in the MACHINE's store, and a "y" deletes cache files) nor
// block on one. An empty store has measured nothing, so nothing is offered. Everything else a
// launch keeps there is a cache or a lock it rebuilds: nix out-links for derivations already in
// the store, the embedded-pack lease, the image-load sentinel. The images themselves live in
// podman's store, which stays shared, so the launch still finds the suite's image loaded.
func privateYoloState(t *testing.T) {
	t.Helper()
	link := os.ExpandEnv("$HOME/.local/share/yolo-jail")
	if err := os.Remove(link); err != nil {
		t.Fatalf("removing the isolated home's link to the machine's yolo-jail state: %v", err)
	}
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAttachRestartsAnOlderJailAtATerminal(t *testing.T) {
	requireJail(t)
	dir, cname := startStandInOlderJail(t)
	privateYoloState(t)
	master, slave := openTestPty(t)

	// $YOLO_CONTRACT_TAGS expands in the JAIL's shell: the boot echoes the command with the
	// literal name, so only the fresh jail's own answer contains the tags.
	cmd := exec.Command(yoloBin, append(jailRunArgs(), "-p", "claude=zai", "--", "bash", "-lc",
		`echo FRESH-TAGS-"$YOLO_CONTRACT_TAGS"`)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.Env = append(cmd.Env, childRepoRootEnv()...)
	cmd.Env = append(cmd.Env, autoCaptureEnvForSuite()...)
	cmd.Env = append(cmd.Env, readinessEnvForSuite()...)
	awaitDetachedWriters(t, dir, launchHome(cmd.Env))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting yolo: %v", err)
	}
	// The child holds its own copies; the parent's would keep the master from ever seeing EOF.
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
	for !strings.Contains(out.String(), "Restart jail now? [Y/n]") {
		select {
		case err := <-done:
			t.Fatalf("yolo exited (%v) without asking to restart the older jail:\n%s", err, out)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no restart question within %s:\n%s", jailTimeout(), out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "every session in it (1 running now)") {
		t.Errorf("the question must name the sessions the restart ends:\n%s", out)
	}
	if _, err := master.Write([]byte("y\n")); err != nil {
		t.Fatalf("answering the question: %v", err)
	}

	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatalf("waiting for yolo: %v", err)
		}
		if err != nil {
			t.Fatalf("the restarted launch failed: %v\n%s", err, out)
		}
	case <-time.After(jailTimeout()):
		forceRemoveContainer(dir)
		t.Fatalf("the restarted launch did not finish within %s:\n%s", jailTimeout(), out)
	}
	// The reader goroutine may still be draining; give it a moment to see the last bytes.
	time.Sleep(200 * time.Millisecond)
	got := out.String()
	if !strings.Contains(got, "Stopping "+cname) {
		t.Errorf("the restart must say it is stopping the jail:\n%s", got)
	}
	if !strings.Contains(got, "FRESH-TAGS-entry-channel,agent-env-files,profile-sets,session-hangup") {
		t.Errorf("the command must run in a FRESH jail, one this yolo launched with its contract "+
			"tags:\n%s", got)
	}
	if strings.Contains(got, "Attaching to existing jail") {
		t.Errorf("a restarted launch must not attach:\n%s", got)
	}
}
