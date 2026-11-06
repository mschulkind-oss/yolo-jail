package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestHostLaunchWithNoDoorwayHoldsASessionAcrossTheExec is SU-D12 (named-setups.md §12.2): every
// `yolo host -- <command>` holds a session record for its lifetime, not only one that opens a
// doorway, so `yolo ps`'s host source is complete. A launch that needs no service execs its
// agent, so the session's lock must cross the exec: at the exec, the session dir exists, its
// record names the host notch and the workspace, a listing reads it as live, and the lock's
// descriptor is not close-on-exec. When the launch returns without exec'ing (the exec failed),
// the dir is gone.
func TestHostLaunchWithNoDoorwayHoldsASessionAcrossTheExec(t *testing.T) {
	base := redirectSessionBase(t)
	hostGateHome(t, `{"packs": ["claude"]}`, wcShell(nil))
	bin := t.TempDir()
	must(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var atExec []runtime.Session
	var listedOK, lockInherited, execed bool
	origExec := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error {
		execed = true
		atExec, listedOK = runtime.ListSessions(base)
		if len(atExec) == 1 {
			lockInherited = lockCrossesExec(t, filepath.Join(atExec[0].Dir, paths.HostServicesSessionLockName))
		}
		return errors.New("exec refused by the test")
	}
	t.Cleanup(func() { hostSyscallExec = origExec })

	var out, errw bytes.Buffer
	hostMain([]string{"--", "claude"}, &out, &errw, false, nil)
	if !execed {
		t.Fatalf("the launch never reached the exec:\n%s", errw.String())
	}
	if !listedOK || len(atExec) != 1 {
		t.Fatalf("at the exec, sessions under %s = %+v (listed %v); want exactly this launch's\n%s",
			base, atExec, listedOK, errw.String())
	}
	s := atExec[0]
	if s.Notch != runtime.NotchHost || s.Workspace == "" || s.Liveness != runtime.SessionLive {
		t.Errorf("the session = %+v; want a live host session naming its workspace", s)
	}
	if !lockInherited {
		t.Error("the session's lock is close-on-exec: it would be released the moment the agent replaced yolo")
	}
	if after, _ := runtime.ListSessions(base); len(after) != 0 {
		t.Errorf("after a launch that did not exec, its session dir is left: %+v", after)
	}
}

// lockCrossesExec reports whether this process holds path open on a descriptor that is not
// close-on-exec.
func lockCrossesExec(t *testing.T, path string) bool {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd to read descriptors from: %v", err)
	}
	for _, e := range entries {
		fd, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err != nil || target != path {
			continue
		}
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err == nil && flags&unix.FD_CLOEXEC == 0 {
			return true
		}
	}
	return false
}
