//go:build linux

package run

import (
	"syscall"
	"testing"
)

// TestAKeepersChildGetsTheDeathSignal: on Linux the kernel sends a keeper's child SIGTERM when the
// keeper dies, however it dies (JL-D32).
func TestAKeepersChildGetsTheDeathSignal(t *testing.T) {
	attr := &syscall.SysProcAttr{Setsid: true}
	setChildDeathSignal(attr)
	if attr.Pdeathsig != syscall.SIGTERM || !attr.Setsid {
		t.Errorf("attr = %+v, want Pdeathsig SIGTERM and the session kept", attr)
	}
}

// TestAKeepersSelfExecsRunItsOwnInode is JL-D5: a keeper's scratch remover and daemons run the binary
// the keeper was exec'd from, /proc/self/exe, never the path `just install` may have replaced; a
// launch's still resolve its path.
func TestAKeepersSelfExecsRunItsOwnInode(t *testing.T) {
	pin := fixturePin(t, []byte("a yolo"))
	o := &Options{keeperMode: true, helperPin: &pin}
	if got, err := o.selfExecArgv([]string{"yolo", "internal", "scratch-rm"}); err != nil || got[0] != "/proc/self/exe" || got[1] != "internal" {
		t.Errorf("a keeper's self-exec is %q (%v)", got, err)
	}
	launch := &Options{helperPin: &pin}
	if got, err := launch.selfExecArgv([]string{"yolo", "internal"}); err != nil || got[0] != pin.Path {
		t.Errorf("a launch's self-exec is %q (%v), want its pin %s", got, err, pin.Path)
	}
	if got, err := o.selfExecArgv([]string{"/usr/bin/other", "x"}); err != nil || got[0] != "/usr/bin/other" {
		t.Errorf("a non-yolo argv was rewritten: %q (%v)", got, err)
	}
}
