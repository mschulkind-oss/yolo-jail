//go:build linux

package entrypoint

// updatebound_linux_test.go runs the launchers' UPDATE under a terminal (program-delivery.md §3.5),
// because the hang it pins exists only there: measured 2026-10-01, `claude install` run by the
// launcher under GNU timeout(1) was stopped by SIGTTOU about 20 ms in, outlived timeout's SIGTERM,
// and never let `claude` start, and a Ctrl-C at the terminal reached none of it. A test run without
// a terminal passes against that launcher. Linux-only for the pty, opened from /dev/ptmx as the
// other launcher tty tests do, since the repo vendors no pty library.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// openPtyPair returns both sides of a fresh pty, skipping the test when none is available.
func openPtyPair(t *testing.T) (master, slave *os.File) {
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
	s, err := os.OpenFile("/dev/pts/"+itoaPty(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		t.Skipf("open slave: %v", err)
	}
	t.Cleanup(func() { m.Close(); s.Close() })
	return m, s
}

func itoaPty(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

var (
	// ignoresTerm outlives SIGTERM: only a SIGKILL ends it.
	ignoresTerm = updateBehavior{"trap '' TERM", "exec sleep 120"}
	// sleeps is an update still running (a slow download, a hung request).
	sleeps = updateBehavior{"", "exec sleep 120"}
	// ignoresInt survives a Ctrl-C: only the grace's SIGKILL ends it.
	ignoresInt = updateBehavior{"trap '' INT", "exec sleep 120"}
	// exitsZeroOnInt stops at a Ctrl-C and exits 0, as claude install does (measured 2026-10-01).
	exitsZeroOnInt = updateBehavior{"trap 'kill $! 2>/dev/null; exit 0' INT", "sleep 120 & wait $!"}
	// rawMode is `claude install`'s shape, measured 2026-10-01: it switches its stdin to raw mode
	// for its progress display at once, and on SIGTERM restores the terminal before it exits. A
	// process in a BACKGROUND process group of its terminal is stopped by SIGTTOU for both, which
	// is where GNU timeout(1) put it. Run with no terminal, both calls fail and it finishes.
	rawMode = updateBehavior{"trap 'stty sane 2>/dev/null; exit 143' TERM",
		"stty raw 2>/dev/null || true\nstty -raw 2>/dev/null || true"}
)

// pathFor is the launcher's PATH: the stand-in `yolo` first when detached, and no `yolo` at all
// otherwise, so the cell never depends on which yolo (if any) the machine running it has.
func pathFor(t *testing.T, detached bool) string {
	t.Helper()
	path := pathWithout(t, "yolo")
	if detached {
		return yoloStandIn(t) + string(os.PathListSeparator) + path
	}
	return path
}

// boundModes are the two ways a launcher can run an update: through yolo's detached, bounded verb,
// and, where no yolo has it, under GNU timeout(1).
func boundModes(t *testing.T) []struct {
	name     string
	detached bool
} {
	t.Helper()
	modes := []struct {
		name     string
		detached bool
	}{{"detached", true}}
	if _, err := exec.LookPath("timeout"); err == nil {
		modes = append(modes, struct {
			name     string
			detached bool
		}{"timeout-fallback", false})
	}
	return modes
}

// waitForPath polls for path to exist, for up to limit.
func waitForPath(t *testing.T, path string, limit time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// ptyLaunch is one launcher run as an interactive launch runs it: the leader of its own session,
// with the pty as its controlling terminal and its stdin, stdout and stderr.
type ptyLaunch struct {
	cmd    *exec.Cmd
	master *os.File
	mu     sync.Mutex
	out    bytes.Buffer
	done   chan struct{}
	err    error
	begun  time.Time
	ended  time.Time
}

func startUnderPty(t *testing.T, p *boundProbe, path string) *ptyLaunch {
	t.Helper()
	master, slave := openPtyPair(t)
	l := &ptyLaunch{master: master, done: make(chan struct{})}
	l.cmd = exec.Command(p.script)
	l.cmd.Dir = p.home
	l.cmd.Env = []string{"HOME=" + p.home, "PATH=" + path, "TERM=xterm"}
	l.cmd.Stdin, l.cmd.Stdout, l.cmd.Stderr = slave, slave, slave
	l.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	l.begun = time.Now()
	if err := l.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				l.mu.Lock()
				l.out.Write(buf[:n])
				l.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		l.err = l.cmd.Wait()
		l.ended = time.Now()
		close(l.done)
	}()
	t.Cleanup(func() {
		select {
		case <-l.done:
		default:
			// The session leader's death orphans whatever process group the update was left in,
			// and the kernel sends a stopped member of an orphaned group SIGHUP and SIGCONT.
			_ = l.cmd.Process.Kill()
			<-l.done
		}
	})
	return l
}

func (l *ptyLaunch) output() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.out.String()
}

// wait waits for the launcher to exit, failing the test (and naming what it printed) when it has
// not within limit: that is the hang this file exists to catch, and it must say so by name.
func (l *ptyLaunch) wait(t *testing.T, limit time.Duration) time.Duration {
	t.Helper()
	select {
	case <-l.done:
	case <-time.After(limit):
		t.Fatalf("the launcher was still running after %s, so the program the user typed never "+
			"started. It printed:\n%s", limit, l.output())
	}
	// Let the reader drain what the launcher's last writes left on the pty.
	time.Sleep(100 * time.Millisecond)
	if l.err != nil {
		t.Errorf("the launcher failed: %v\n%s", l.err, l.output())
	}
	return l.ended.Sub(l.begun)
}

// waitForFile waits for path to exist, failing the test after limit.
func waitForFile(t *testing.T, l *ptyLaunch, path string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the update never started. The launcher printed:\n%s", l.output())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// THE ROOT CAUSE. claude install switches its terminal to raw mode at once; under GNU timeout(1) it
// was in a background process group of that terminal, so the kernel stopped it with SIGTTOU before
// it did any work, and on timeout's SIGTERM its handler restored the terminal and was stopped
// again. Run with no terminal, the same program finishes at once and the agent starts.
func TestAnUpdateThatSwitchesItsTerminalToRawModeFinishesAndTheAgentStarts(t *testing.T) {
	for _, mode := range boundModes(t) {
		t.Run(mode.name, func(t *testing.T) {
			p := newBoundProbe(t, boundProbeOpts{verb: []string{"install"}, behave: rawMode, timeout: 20, grace: 2})
			l := startUnderPty(t, p, pathFor(t, mode.detached))
			took := l.wait(t, 45*time.Second)
			out := l.output()
			t.Logf("the launcher printed:\n%s", out)
			if took >= 20*time.Second {
				t.Errorf("the update ran into its %ds bound (%s) instead of finishing", p.timeout, took)
			}
			for _, want := range []string{"Updating probetool", "UPDATE_FINISHED", "AGENT_RAN"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "update failed") || strings.Contains(out, "timed out") {
				t.Errorf("the update must have succeeded:\n%s", out)
			}
		})
	}
}

// THE BOUND HOLDS FOR A PROGRAM THAT OUTLIVES SIGTERM. GNU timeout without -k sends SIGTERM and
// waits for good; the launcher must instead kill it after UPDATE_GRACE, say the update timed out,
// and run the installed version.
func TestAnUpdateThatIgnoresSIGTERMIsKilledAfterTheGrace(t *testing.T) {
	for _, tpl := range []struct {
		name string
		npm  bool
	}{{"native", false}, {"npm", true}} {
		for _, mode := range boundModes(t) {
			t.Run(tpl.name+"/"+mode.name, func(t *testing.T) {
				p := newBoundProbe(t, boundProbeOpts{npm: tpl.npm, verb: []string{"install"},
					behave: ignoresTerm, timeout: 2, grace: 2})
				l := startUnderPty(t, p, pathFor(t, mode.detached))
				took := l.wait(t, 45*time.Second)
				out := l.output()
				t.Logf("the launcher printed:\n%s", out)
				if took < time.Duration(p.timeout)*time.Second {
					t.Errorf("returned after %s, before the %ds bound: something else ended the update:\n%s",
						took, p.timeout, out)
				}
				if limit := time.Duration(p.timeout+p.grace+8) * time.Second; took > limit {
					t.Errorf("returned after %s, past UPDATE_TIMEOUT (%ds) plus UPDATE_GRACE (%ds)",
						took, p.timeout, p.grace)
				}
				if !strings.Contains(out, "timed out after 2s") {
					t.Errorf("a timed-out update must be said as one:\n%s", out)
				}
				if !strings.Contains(out, "AGENT_RAN") {
					t.Errorf("the installed version must run after a timed-out update:\n%s", out)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(p.realBin)), ".yolo-update.lock")); !os.IsNotExist(err) {
					t.Errorf("a timed-out update must release the install-prefix lock (err=%v)", err)
				}
			})
		}
	}
}

// CTRL-C STOPS THE UPDATE AND THE AGENT STARTS. Under GNU timeout(1) the update was in a process
// group of its own, so the terminal's interrupt reached only the launcher, which waited out the
// whole bound. Now it reaches the update, and the launcher goes on to the installed version, saying
// so. Three shapes: a program that dies of the Ctrl-C, one that ignores it and is killed after
// UPDATE_GRACE, and one that stops and exits 0, which claude install does and which is still no
// successful update. The bound is long here, so only the Ctrl-C can explain a prompt return.
func TestCtrlCDuringAnUpdateStopsItAndRunsTheInstalledVersion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		npm    bool
		behave updateBehavior
	}{
		{"native/dies-of-it", false, sleeps},
		{"native/ignores-it", false, ignoresInt},
		{"native/exits-0", false, exitsZeroOnInt},
		{"npm/dies-of-it", true, sleeps},
		{"npm/exits-0", true, exitsZeroOnInt},
	} {
		for _, mode := range boundModes(t) {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				p := newBoundProbe(t, boundProbeOpts{npm: tc.npm, verb: []string{"install"},
					behave: tc.behave, timeout: 30, grace: 2})
				l := startUnderPty(t, p, pathFor(t, mode.detached))
				waitForFile(t, l, p.started, 15*time.Second)
				typed := time.Now()
				if _, err := l.master.Write([]byte{0x03}); err != nil {
					t.Fatal(err)
				}
				l.wait(t, 45*time.Second)
				out := l.output()
				t.Logf("the launcher printed:\n%s", out)
				if after := l.ended.Sub(typed); after > time.Duration(p.grace+8)*time.Second {
					t.Errorf("the launcher went on %s after the Ctrl-C: it did not reach the update", after)
				}
				if !strings.Contains(out, "update interrupted") {
					t.Errorf("an interrupted update must be said as one:\n%s", out)
				}
				if !strings.Contains(out, "AGENT_RAN") {
					t.Errorf("the installed version must run after a Ctrl-C at its update:\n%s", out)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(p.realBin)), ".yolo-update.lock")); !os.IsNotExist(err) {
					t.Errorf("an interrupted update must release the install-prefix lock (err=%v)", err)
				}
			})
		}
	}
}

// THE PRE-LAUNCH REFRESH IS THE SAME ACT, under the same bound: Ctrl-C at it runs the program.
func TestCtrlCDuringThePrelaunchRefreshRunsTheProgram(t *testing.T) {
	for _, mode := range boundModes(t) {
		t.Run(mode.name, func(t *testing.T) {
			p := newBoundProbe(t, boundProbeOpts{
				refresh: &packdecl.Refresh{Argv: []string{"refresh"}, Lock: ".store/.yolo-update.lock"},
				behave:  sleeps, timeout: 30, grace: 2})
			l := startUnderPty(t, p, pathFor(t, mode.detached))
			waitForFile(t, l, p.started, 15*time.Second)
			typed := time.Now()
			if _, err := l.master.Write([]byte{0x03}); err != nil {
				t.Fatal(err)
			}
			l.wait(t, 45*time.Second)
			out := l.output()
			t.Logf("the launcher printed:\n%s", out)
			if after := l.ended.Sub(typed); after > time.Duration(p.grace+8)*time.Second {
				t.Errorf("the launcher went on %s after the Ctrl-C: it did not reach the refresh", after)
			}
			if !strings.Contains(out, "pre-launch refresh was interrupted") || !strings.Contains(out, "AGENT_RAN") {
				t.Errorf("an interrupted refresh must be said, and the program run:\n%s", out)
			}
			if _, err := os.Stat(filepath.Join(p.home, ".store", ".yolo-update.lock")); !os.IsNotExist(err) {
				t.Errorf("an interrupted refresh must release its lock (err=%v)", err)
			}
		})
	}
}

// AN INSTALLER RE-RUN IS AN UPDATE TOO. With no verb declared the update re-runs the vendor's
// installer, and a Ctrl-C there now ends the installer, not the launch: the installed version
// runs. What the stopped installer left is recorded nowhere, because a receipt for it would vouch
// for half an install (PS-D7, which still ends the launcher at a COLD install).
func TestCtrlCDuringAnInstallerUpdateRunsTheInstalledVersionAndRecordsNothing(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	installer := "#!/bin/bash\necho \"$$\" > \"$HOME/update.pid\"\n: > \"$HOME/update.started\"\nexec sleep 120\n"
	for _, mode := range boundModes(t) {
		t.Run(mode.name, func(t *testing.T) {
			url := serveBody(t, 200, "application/x-sh", installer)
			p := newBoundProbe(t, boundProbeOpts{installerURL: url, behave: sleeps, timeout: 30, grace: 2})
			l := startUnderPty(t, p, pathFor(t, mode.detached))
			waitForFile(t, l, p.started, 15*time.Second)
			if _, err := l.master.Write([]byte{0x03}); err != nil {
				t.Fatal(err)
			}
			l.wait(t, 45*time.Second)
			out := l.output()
			t.Logf("the launcher printed:\n%s", out)
			if !strings.Contains(out, "update interrupted") || !strings.Contains(out, "AGENT_RAN") {
				t.Errorf("an interrupted installer update must be said, and the installed version run:\n%s", out)
			}
			receipts, _ := os.ReadFile(filepath.Join(p.home, "ws", ".yolo", "receipts.jsonl"))
			if len(receipts) != 0 {
				t.Errorf("an interrupted installer must leave no receipt, got:\n%s", receipts)
			}
		})
	}
}

// A HANGUP DURING AN UPDATE ENDS THE LAUNCHER AND RELEASES ITS LOCK. A closed terminal sends SIGHUP
// to its foreground process group, the launcher's. Before, the launcher died at once and left the
// install-prefix lock behind, so the next ten minutes of launches said "another update is in
// progress" and the next after that broke the lock and ran the update again. Its own group stands
// in for the terminal's foreground group here.
func TestAHangupDuringAnUpdateReleasesTheLockAndEndsTheLauncher(t *testing.T) {
	for _, mode := range boundModes(t) {
		t.Run(mode.name, func(t *testing.T) {
			p := newBoundProbe(t, boundProbeOpts{verb: []string{"install"}, behave: sleeps, timeout: 30, grace: 2})
			cmd := exec.Command(p.script)
			cmd.Dir = p.home
			cmd.Env = []string{"HOME=" + p.home, "PATH=" + pathFor(t, mode.detached)}
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			exited := make(chan struct{})
			go func() { done <- cmd.Wait(); close(exited) }()
			t.Cleanup(func() {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				select {
				case <-exited:
				case <-time.After(5 * time.Second):
				}
			})
			if !waitForPath(t, p.started, 15*time.Second) {
				t.Fatalf("the update never started:\n%s", out.String())
			}
			lock := filepath.Join(p.home, ".local", ".yolo-update.lock")
			if _, err := os.Stat(lock); err != nil {
				t.Fatalf("the update runs without the install-prefix lock: %v", err)
			}
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP); err != nil {
				t.Fatal(err)
			}
			var err error
			select {
			case err = <-done:
			case <-time.After(20 * time.Second):
				t.Fatalf("the launcher outlived the hangup by 20s:\n%s", out.String())
			}
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("the launcher must end by the hangup, got %v:\n%s", err, out.String())
			}
			if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGHUP {
				t.Errorf("the launcher must die of SIGHUP itself, got %v", err)
			}
			if strings.Contains(out.String(), "AGENT_RAN") {
				t.Errorf("a hung-up launcher must not start the program:\n%s", out.String())
			}
			if _, err := os.Stat(lock); !os.IsNotExist(err) {
				t.Errorf("a hangup must release the install-prefix lock (err=%v)", err)
			}
		})
	}
}
