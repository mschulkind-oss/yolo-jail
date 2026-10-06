//go:build linux || darwin

package notty

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// THE PACKAGE'S TESTS RUN ON BOTH OSES THE JAIL LAUNCHERS RUN ON. macos-user's launchers bound an
// update through the staged darwin yolo's no-terminal verb, so this package is exercised on
// darwin by ci.yml's check-macos job, not only on Linux. Only two helpers differ by OS, each in
// a file of its own: opening a pty (pty_linux_test.go, pty_darwin_test.go) and telling whether a
// pid has exited (procgone_linux_test.go, procgone_darwin_test.go). Everything else, the TestMain
// helpers included, is in notty_test.go and bound_test.go with no build tag.

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
