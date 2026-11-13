package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
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
	hostGateHome(t, `{"packs": ["claude"]}`, wcShell(nil))
	base := paths.HostServicesBase(false)
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
	// The exec keeps the pid, so the agent's life is the recorded process's (SUP-D29).
	if s.PID != os.Getpid() {
		t.Errorf("the session records pid %d; want the launch's own, %d", s.PID, os.Getpid())
	}
	// The record names the launch's setup and start, so `yolo ps` lists it under host-self with
	// an age (named-setups.md SU-D11, the session half).
	if s.Setup != "host-self" || s.Started.IsZero() {
		t.Errorf("the session records setup %q, started %v; want host-self and a start time", s.Setup, s.Started)
	}
	if !lockInherited {
		t.Error("the session's lock is close-on-exec: it would be released the moment the agent replaced yolo")
	}
	if after, _ := runtime.ListSessions(base); len(after) != 0 {
		t.Errorf("after a launch that did not exec, its session dir is left: %+v", after)
	}
}

// TestHostLaunchWithServicesButNoDoorwayHoldsOneSession: a launch that starts a launch-owned
// service and opens no doorway stays resident, and holds exactly one session for its run, not one
// per service; once it returns, none is left.
func TestHostLaunchWithServicesButNoDoorwayHoldsOneSession(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	cfg := codexConfig(t, upstream.URL)
	var base string
	var during []runtime.Session
	var listed bool
	l := runServiceLaunchWith(t, cfg, []string{"-p", "codex"}, "", nil, func(string) {
		base = paths.HostServicesBase(false)
		orig := startLaunchService
		startLaunchService = func(p *launchservice.Plan, env map[string]string) (*launchservice.Running, error) {
			during, listed = runtime.ListSessions(base)
			return orig(p, env)
		}
		t.Cleanup(func() { startLaunchService = orig })
	})
	if l.rc != 0 || len(l.started) == 0 {
		t.Fatalf("rc = %d, started %d services; want a resident launch with one\n%s", l.rc, len(l.started), l.errs)
	}
	if l.execed {
		t.Fatal("a launch with a service exec'd its agent")
	}
	if !listed || len(during) != 1 || during[0].Notch != runtime.NotchHost || during[0].Liveness != runtime.SessionLive ||
		during[0].Setup != "host-self" {
		t.Fatalf("while starting its service, sessions under %s = %+v (listed %v); want exactly one live host session\n%s",
			base, during, listed, l.errs)
	}
	if after, _ := runtime.ListSessions(base); len(after) != 0 {
		t.Errorf("after the launch returned, its session dir is left: %+v", after)
	}
}

// lockCrossesExec reports whether this process holds path open on a descriptor that is not
// close-on-exec. It finds the descriptor by file identity (fstat's device and inode against the
// path's), which works the same on Linux and darwin, so it has no /proc to need.
func lockCrossesExec(t *testing.T, path string) bool {
	t.Helper()
	var want unix.Stat_t
	if err := unix.Stat(path, &want); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	var lim unix.Rlimit
	maxFD := 1024
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &lim); err == nil && lim.Cur < uint64(maxFD) {
		maxFD = int(lim.Cur)
	}
	for fd := 0; fd < maxFD; fd++ {
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Dev != want.Dev || st.Ino != want.Ino {
			continue
		}
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err == nil && flags&unix.FD_CLOEXEC == 0 {
			return true
		}
	}
	return false
}
