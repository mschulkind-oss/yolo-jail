//go:build linux

package notty

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// helperEnv makes this test binary, re-executed, act as the process that HAS a controlling
// terminal: it runs the probe once directly and once through Run, and writes both answers.
const helperEnv = "NOTTY_TEST_HELPER_OUT"

// probe prints whether it could open /dev/tty (its controlling terminal) and whether its stdin
// is a terminal: the two ways an installer reaches a human (PS-D1).
const probe = `if (exec 3</dev/tty) 2>/dev/null; then echo HAS_TTY; else echo NO_TTY; fi; ` +
	`if [ -t 0 ]; then echo STDIN_TTY; else echo STDIN_NOT_TTY; fi`

func TestMain(m *testing.M) {
	if out := os.Getenv(helperEnv); out != "" {
		os.Exit(runHelper(out))
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

// openPty opens a pty pair, or skips when this machine has none to give.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	var unlock int
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(m), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		unix.Close(m)
		t.Skipf("unlockpt: %v", e)
	}
	n, err := unix.IoctlGetUint32(m, unix.TIOCGPTN)
	if err != nil {
		unix.Close(m)
		t.Skipf("ptsname: %v", err)
	}
	s, err := os.OpenFile("/dev/pts/"+itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		unix.Close(m)
		t.Skipf("open pts: %v", err)
	}
	master = os.NewFile(uintptr(m), "ptmx")
	t.Cleanup(func() { master.Close(); s.Close() })
	return master, s
}

func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// PS-D1's property, measured rather than asserted from the argv: a process with a controlling
// terminal runs a probe directly (the control: it reaches /dev/tty, and its stdin is the
// terminal), then through Run, where it can reach neither. Without the control the second half
// would pass on any machine whose tests run with no terminal at all.
func TestRunLeavesTheChildNoTerminal(t *testing.T) {
	_, slave := openPty(t)
	out := filepath.Join(t.TempDir(), "answers")
	helper := exec.Command(os.Args[0], "-test.run=^$")
	helper.Env = append(os.Environ(), helperEnv+"="+out)
	helper.Stdin, helper.Stdout, helper.Stderr = slave, slave, slave
	helper.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := helper.Run(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := "direct:HAS_TTY,STDIN_TTY\nrun:NO_TTY,STDIN_NOT_TTY\n"
	if string(got) != want {
		t.Errorf("answers =\n%s\nwant\n%s", got, want)
	}
}

// A child killed by a signal exits 128+N, one that exits exits with its status, and a command
// that cannot start is 127: the wrapper verb's exit is the shell's convention.
func TestExitCodeFollowsTheShellsConvention(t *testing.T) {
	if got := ExitCode(Run(exec.Command("sh", "-c", "exit 7"))); got != 7 {
		t.Errorf("exit 7 -> %d", got)
	}
	if got := ExitCode(Run(exec.Command("sh", "-c", "kill -TERM $$"))); got != 128+int(syscall.SIGTERM) {
		t.Errorf("SIGTERM -> %d, want %d", got, 128+int(syscall.SIGTERM))
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
