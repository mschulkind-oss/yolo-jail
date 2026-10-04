package notty

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperEnv makes this test binary, re-executed, act as the process that HAS a controlling
// terminal: it runs the probe once directly and once through Run, and writes both answers.
const helperEnv = "NOTTY_TEST_HELPER_OUT"

// stopHelperEnv makes this test binary, re-executed, run a long command through Run and end the
// way `yolo internal no-terminal` ends (WrapperExit), recording in the named directory when the
// command started and whether Run called it Stopped.
const stopHelperEnv = "NOTTY_TEST_STOP_HELPER_DIR"

// probe prints whether it could open /dev/tty (its controlling terminal) and whether its stdin
// is a terminal: the two ways an installer reaches a human (PS-D1).
const probe = `if (exec 3</dev/tty) 2>/dev/null; then echo HAS_TTY; else echo NO_TTY; fi; ` +
	`if [ -t 0 ]; then echo STDIN_TTY; else echo STDIN_NOT_TTY; fi`

func TestMain(m *testing.M) {
	if out := os.Getenv(helperEnv); out != "" {
		os.Exit(runHelper(out))
	}
	if dir := os.Getenv(stopHelperEnv); dir != "" {
		os.Exit(runStopHelper(dir))
	}
	if dir := os.Getenv(graceHelperEnv); dir != "" {
		os.Exit(runGraceHelper(dir))
	}
	os.Exit(m.Run())
}

func runHelper(out string) int {
	var b strings.Builder
	direct := exec.Command("sh", "-c", probe)
	direct.Stdin = os.Stdin
	o, err := direct.Output()
	if err != nil {
		return 3
	}
	b.WriteString("direct:" + strings.Join(strings.Fields(string(o)), ",") + "\n")
	detached := exec.Command("sh", "-c", probe)
	var buf strings.Builder
	detached.Stdout = &buf
	if err := Run(detached); err != nil {
		return 4
	}
	b.WriteString("run:" + strings.Join(strings.Fields(buf.String()), ",") + "\n")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		return 5
	}
	return 0
}

func runStopHelper(dir string) int {
	err := Run(exec.Command("sh", "-c", `echo > "$1/started"; exec sleep 20`, "sh", dir))
	var st *Stopped
	if errors.As(err, &st) {
		if werr := os.WriteFile(filepath.Join(dir, "stopped"), []byte(st.Signal.String()), 0o644); werr != nil {
			return 5
		}
	}
	return WrapperExit(err)
}

// A child killed by a signal exits 128+N, one that exits exits with its status, and a command
// that cannot start is 127: the wrapper verb's exit is the shell's convention.
func TestExitCodeFollowsTheShellsConvention(t *testing.T) {
	if got := ExitCode(Run(exec.Command("sh", "-c", "exit 7"))); got != 7 {
		t.Errorf("exit 7 -> %d", got)
	}
	selfKilled := Run(exec.Command("sh", "-c", "kill -TERM $$"))
	if got := ExitCode(selfKilled); got != 128+int(syscall.SIGTERM) {
		t.Errorf("SIGTERM -> %d, want %d", got, 128+int(syscall.SIGTERM))
	}
	// A signal the command sent itself was never this process's, so it is no Stopped, and a
	// wrapper exits with the status rather than dying of it.
	var st *Stopped
	if errors.As(selfKilled, &st) {
		t.Errorf("a command that killed itself was reported Stopped by %s", st.Signal)
	}
	if got := WrapperExit(selfKilled); got != 128+int(syscall.SIGTERM) {
		t.Errorf("WrapperExit of a self-inflicted SIGTERM -> %d, want %d", got, 128+int(syscall.SIGTERM))
	}
	if got := ExitCode(Run(exec.Command(filepath.Join(t.TempDir(), "absent")))); got != 127 {
		t.Errorf("unstartable -> %d, want 127", got)
	}
	if got := ExitCode(Run(exec.Command("true"))); got != 0 {
		t.Errorf("true -> %d", got)
	}
}

// Prepare clears what would hand the child a terminal back: a caller's Setctty and its stdin.
func TestPrepareClearsATerminalTheCallerLeft(t *testing.T) {
	c := exec.Command("true")
	c.Stdin = os.Stdin
	c.SysProcAttr = &syscall.SysProcAttr{Setctty: true, Ctty: 0}
	Prepare(c)
	if !c.SysProcAttr.Setsid || c.SysProcAttr.Setctty || c.Stdin != nil {
		t.Errorf("Prepare left setsid=%v setctty=%v stdin=%v", c.SysProcAttr.Setsid,
			c.SysProcAttr.Setctty, c.Stdin)
	}
}

// A SIGNAL RUN FORWARDED ENDS THE WRAPPER THE SAME WAY (PS-D7). The command is in a session of its
// own, so a signal sent to this process is all that reaches it, through Run's forwarding. Run calls
// the command's death by that signal Stopped, and WrapperExit then dies of the signal itself, so a
// shell waiting on the wrapper sees the death it would have seen running the command: a Ctrl-C
// stops a script only when its command died of SIGINT, and an exit with 130 lets it go on.
func TestAForwardedSignalStopsTheWrapperToo(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			dir := t.TempDir()
			helper := exec.Command(os.Args[0], "-test.run=^$")
			helper.Env = append(os.Environ(), stopHelperEnv+"="+dir)
			if err := helper.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(15 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = helper.Process.Kill()
					_ = helper.Wait()
					t.Fatal("the command never started")
				}
				time.Sleep(10 * time.Millisecond)
			}
			begun := time.Now()
			if err := helper.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err := helper.Wait()
			if elapsed := time.Since(begun); elapsed > 10*time.Second {
				t.Errorf("the command outlived %s by %s: it was not forwarded", sig, elapsed)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "stopped")); string(got) != sig.String() {
				t.Errorf("Run did not call the command's death by %s Stopped (recorded %q)", sig, got)
			}
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("the wrapper exited cleanly after %s: %v", sig, err)
			}
			if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != sig {
				t.Errorf("the wrapper must die of %s itself, got %v", sig, err)
			}
		})
	}
}
