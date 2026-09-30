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
	o := &Options{keeperMode: true}
	if got := o.selfExecArgv([]string{"yolo", "internal", "scratch-rm"}); got[0] != "/proc/self/exe" || got[1] != "internal" {
		t.Errorf("a keeper's self-exec is %q", got)
	}
	launch := &Options{}
	if got := launch.selfExecArgv([]string{"yolo", "internal"}); got[0] == "/proc/self/exe" || got[0] == "yolo" {
		t.Errorf("a launch's self-exec is %q, want its binary's path", got)
	}
	if got := o.selfExecArgv([]string{"/usr/bin/other", "x"}); got[0] != "/usr/bin/other" {
		t.Errorf("a non-yolo argv was rewritten: %q", got)
	}
}
