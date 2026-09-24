//go:build darwin

package prune

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// darwinSZOMB is SZOMB from <sys/proc.h>: a zombie holds no file open.
const darwinSZOMB = 5

// darwinCommMax is MAXCOMLEN: a p_comm this long may have been cut.
const darwinCommMax = 16

func init() { platformProcStarts = darwinProcStarts }

// darwinProcStarts lists this euid's live processes through the kern.proc.uid sysctl.
// false — "could not ask" — when the sysctl fails, so every legacy tree is kept.
//
// The name is the EXEC PATH's basename from kern.procargs2 where the kernel will give it
// (always, for this uid's own processes), and the 16-byte p_comm otherwise, flagged
// truncated at full length: a cut name cannot be proven not to be a yolo or a .test.
func darwinProcStarts() ([]ProcStart, bool) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Geteuid())
	if err != nil {
		return nil, false
	}
	out := make([]ProcStart, 0, len(procs))
	for i := range procs {
		kp := &procs[i].Proc
		pid := int(kp.P_pid)
		if pid <= 0 || kp.P_stat == darwinSZOMB {
			continue
		}
		sec, nsec := kp.P_starttime.Unix()
		p := ProcStart{PID: pid, Start: time.Unix(sec, nsec)}
		if path := execPath(pid); path != "" {
			p.Name = filepath.Base(path)
		} else {
			comm := kp.P_comm[:]
			if i := bytes.IndexByte(comm, 0); i >= 0 {
				comm = comm[:i]
			}
			p.Name = string(comm)
			p.NameTruncated = len(comm) >= darwinCommMax
		}
		out = append(out, p)
	}
	return out, true
}

// execPath reads the executable path kern.procargs2 carries after its argc word.
func execPath(pid int) string {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) <= 4 {
		return ""
	}
	buf = buf[4:]
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf)
}
