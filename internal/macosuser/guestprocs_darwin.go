//go:build darwin

package macosuser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// darwinSZOMB is SZOMB from <sys/proc.h>: a zombie has closed every file it held.
const darwinSZOMB = 5

// guestProcessesReal is Deps.GuestProcesses on macOS: every process on the machine, from the
// kern.proc.all sysctl, which needs no privilege — its pid, parent, start time, and whether it
// is the sandbox's (guestprocs.go). Nothing here needs root: the host user cannot read another
// account's arguments or open files, but every process's ids and start time are public (ps
// prints them for any user), and the run-as-you mode reads the environment of your own
// processes alone, which kern.procargs2 gives their owner.
func guestProcessesReal(mode Mode) ([]GuestProc, error) {
	c := guestProcClassifier{markerDir: guestMarkerDir()}
	if mode == ModeSelf {
		c.self, c.selfUID = true, uint32(os.Getuid())
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			c.machineHome = LaunchHomes(ModeSelf, "", home).Machine
		}
		// The account may be there from an earlier account-mode launch; on a Mac without it
		// there is nothing to look for, which is not a failure to ask.
		// Asked once per process: a finishing launch re-reads the table every few milliseconds.
		selfModeAccount.Do(func() {
			selfModeAccount.uid, selfModeAccount.err = sandboxUIDReal()
		})
		if selfModeAccount.err == nil {
			c.account, c.accountKnown = selfModeAccount.uid, true
		}
	} else {
		uid, err := sandboxUIDReal()
		if err != nil {
			return nil, err
		}
		c.account, c.accountKnown = uid, true
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("kern.proc.all: %w", err)
	}
	rows := make([]guestProcRow, 0, len(procs))
	for i := range procs {
		kp := &procs[i]
		pid := int(kp.Proc.P_pid)
		if pid < 0 || kp.Proc.P_stat == darwinSZOMB {
			continue
		}
		sec, nsec := kp.Proc.P_starttime.Unix()
		r := guestProcRow{
			PID:   pid,
			PPID:  int(kp.Eproc.Ppid),
			UIDs:  [3]uint32{kp.Eproc.Pcred.P_ruid, kp.Eproc.Ucred.Uid, kp.Eproc.Pcred.P_svuid},
			Start: time.Unix(sec, nsec),
			Name:  commName(kp.Proc.P_comm[:]),
		}
		if c.self && c.hasUID(r, c.selfUID) && pid != os.Getpid() {
			if buf, err := unix.SysctlRaw("kern.procargs2", pid); err == nil {
				r.Env, r.EnvKnown = procArgs2Env(buf)
			}
			if !r.EnvKnown {
				r.Alive = unix.Kill(pid, 0) != unix.ESRCH
			}
		}
		rows = append(rows, r)
	}
	return c.classify(rows), nil
}

// guestMarkerDir is the directory the sandbox marker's value names a file in.
func guestMarkerDir() string { return filepath.Join(stateDir, sandboxEnvLeaf) }

// commName is a kinfo_proc's p_comm, the first bytes of the name its last exec ran, up to the
// first NUL. Public like the rest of the row, and only ever printed: a process names itself.
func commName(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// sandboxUID caches the account's uid once read: a finishing launch reads the table every few
// milliseconds while it waits for its guest, and the uid does not change under it.
var sandboxUID struct {
	sync.Mutex
	uid   uint32
	known bool
}

// selfModeAccount is the run-as-you mode's one look for the account, absent or not.
var selfModeAccount struct {
	sync.Once
	uid uint32
	err error
}

// sandboxUIDReal is the sandbox account's uid through `/usr/bin/id -u`, which asks the directory
// service (os/user's pure-Go path reads /etc/passwd, which a Mac's accounts are not in).
func sandboxUIDReal() (uint32, error) {
	sandboxUID.Lock()
	defer sandboxUID.Unlock()
	if sandboxUID.known {
		return sandboxUID.uid, nil
	}
	out, rc := outputWithTimeout(exec.Command("/usr/bin/id", "-u", "--", SandboxUser), 10*time.Second)
	if rc != 0 {
		return 0, fmt.Errorf("`/usr/bin/id -u %s` exited %d", SandboxUser, rc)
	}
	n, err := strconv.ParseUint(strings.TrimSpace(out), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("`/usr/bin/id -u %s` printed %q", SandboxUser, strings.TrimSpace(out))
	}
	sandboxUID.uid, sandboxUID.known = uint32(n), true
	return sandboxUID.uid, nil
}
