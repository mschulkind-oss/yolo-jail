//go:build linux

package cgd

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTargetFromPidfdReadsTheKernelAttestedPid(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Skipf("pidfd_open unavailable: %v", err)
	}
	f := os.NewFile(uintptr(fd), "pidfd")
	defer f.Close()
	tgt, err := TargetFromPidfd(f)
	if err != nil {
		t.Fatal(err)
	}
	if tgt.PID != os.Getpid() || !tgt.Alive() {
		t.Errorf("target = %+v alive=%v, want pid %d alive", tgt, tgt.Alive(), os.Getpid())
	}
}

func TestTargetFromPidfdRefusesAnyOtherKindOfFile(t *testing.T) {
	f, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := TargetFromPidfd(f); err == nil {
		t.Error("a regular file was accepted as a pidfd")
	}
}
