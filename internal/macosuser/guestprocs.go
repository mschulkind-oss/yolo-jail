package macosuser

// guestprocs.go decides which processes on the machine are the SANDBOX's, for the pack-tree
// holder proof (packtreeproof.go). The two modes answer differently:
//
//   - the account mode's sandbox is every process one of whose uids is the second account's,
//     as it always was;
//   - the run-as-you mode's sandbox runs as you, so a uid says nothing. Its processes are the
//     ones carrying a SANDBOX MARKER (a term coined here): a variable the run-as-you argv sets
//     on every process it starts inside the profile (sandboxEnvPairs) and your shell never
//     does — the session env file's path (SandboxEnvFileEnv) naming a file in the staging
//     root's env directory, or the machine root (entrypoint.DarwinMachineHomeEnv), which a
//     launch with no env file still sets. A process the launch started carries one, and so does every
//     child that keeps its environment; a child that drops it is still counted while a marked
//     ancestor lives, since Seatbelt confines a sandboxed process's children too.
//
// THE TRI-STATE STAYS: a table that cannot be read is an error, and the caller keeps the tree.
// A process of yours whose environment cannot be read, and which is still alive, is counted as
// a sandbox process: it may hold a tree, and keeping one costs only disk until the next prune.
//
// ⚠ What escapes the marker: a sandboxed process that both drops the variable and outlives
// every marked ancestor. That takes an agent rewriting its own environment before it detaches,
// which the misguided-agent principle (docs/reference/misguided-agent-principle.md) puts out of
// scope; the cost is that prune may remove a tree such a process still reads.

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// guestProcRow is one row of the machine's process table as the darwin reader takes it: the
// public part (ids, start, name) for every process, and, for a process of yours in the
// run-as-you mode, its environment.
type guestProcRow struct {
	PID, PPID int
	// UIDs are the real, effective and saved uids.
	UIDs  [3]uint32
	Start time.Time
	Name  string
	// Env is the process's environment; EnvKnown is false when it was not read or could not be.
	Env      []string
	EnvKnown bool
	// Alive is whether the process still existed when its environment could not be read; it is
	// consulted only when EnvKnown is false.
	Alive bool
}

// guestProcClassifier is what decides a row is the sandbox's.
type guestProcClassifier struct {
	// account is the second account's uid; accountKnown is false when there is no account to
	// look for (the run-as-you mode on a Mac without it).
	account      uint32
	accountKnown bool
	// self is set in the run-as-you mode: your processes are read for the marker.
	self    bool
	selfUID uint32
	// markerDir is the staging root's env directory the env-file marker must name a file in,
	// and machineHome the machine root the machine-home marker must name ( checks neither).
	markerDir, machineHome string
}

func (c guestProcClassifier) hasUID(r guestProcRow, uid uint32) bool {
	return r.UIDs[0] == uid || r.UIDs[1] == uid || r.UIDs[2] == uid
}

// marked reports whether r's environment carries the sandbox marker, or could not be read for a
// process still alive.
func (c guestProcClassifier) marked(r guestProcRow) bool {
	if !r.EnvKnown {
		return r.Alive
	}
	for _, kv := range r.Env {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case k == SandboxEnvFileEnv && c.markerDir != "" &&
			strings.HasPrefix(filepath.Clean(v), filepath.Clean(c.markerDir)+"/"):
			return true
		case k == entrypoint.DarwinMachineHomeEnv && c.machineHome != "" &&
			filepath.Clean(v) == filepath.Clean(c.machineHome):
			return true
		}
	}
	return false
}

// classify turns the table into the holder proof's rows.
func (c guestProcClassifier) classify(rows []guestProcRow) []GuestProc {
	out := make([]GuestProc, 0, len(rows))
	byPID := make(map[int]int, len(rows))
	mark := make([]bool, len(rows))
	for i, r := range rows {
		byPID[r.PID] = i
		switch {
		case c.accountKnown && c.hasUID(r, c.account):
			mark[i] = true
		case c.self && c.hasUID(r, c.selfUID):
			mark[i] = c.marked(r)
		}
	}
	// A process of yours below a marked ancestor is inside the same profile.
	if c.self {
		for i, r := range rows {
			if mark[i] || !c.hasUID(r, c.selfUID) {
				continue
			}
			seen := map[int]bool{r.PID: true}
			for pid := r.PPID; pid > 1 && !seen[pid]; {
				seen[pid] = true
				j, ok := byPID[pid]
				if !ok {
					break
				}
				if mark[j] && c.hasUID(rows[j], c.selfUID) {
					mark[i] = true
					break
				}
				pid = rows[j].PPID
			}
		}
	}
	for i, r := range rows {
		out = append(out, GuestProc{PID: r.PID, PPID: r.PPID, Sandbox: mark[i], Start: r.Start, Name: r.Name})
	}
	return out
}

// parseProcArgs2 reads a KERN_PROCARGS2 buffer: a native-endian int32 argc, the executable path,
// NUL padding, argc arguments, then the environment, each NUL-terminated. ok is false for a
// buffer too short to hold argc.
func parseProcArgs2(buf []byte) (argv, env []string, ok bool) {
	if len(buf) < 4 {
		return nil, nil, false
	}
	argc := int(int32(binary.LittleEndian.Uint32(buf[:4])))
	rest := buf[4:]
	// The executable path, then its padding.
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil, nil, true
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	next := func() (string, bool) {
		if len(rest) == 0 {
			return "", false
		}
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			s := string(rest)
			rest = nil
			return s, true
		}
		s := string(rest[:j])
		rest = rest[j+1:]
		return s, true
	}
	for n := 0; n < argc; n++ {
		s, more := next()
		if !more {
			return argv, nil, true
		}
		argv = append(argv, s)
	}
	for {
		s, more := next()
		if !more || s == "" {
			return argv, env, true
		}
		env = append(env, s)
	}
}

// procArgs2Env is a KERN_PROCARGS2 buffer's environment, and whether it was READ. An empty one
// is not read: macOS 26 hides a platform binary's environment from kern.procargs2, its owner's
// read included (the Mac run of 2026-11-03, §6), and returns the arguments alone, so a sandboxed
// `sh` would otherwise read as known and unmarked. A process with truly no environment carries
// no marker either, and counting it while alive costs only disk, as the tri-state above says.
func procArgs2Env(buf []byte) (env []string, known bool) {
	_, env, ok := parseProcArgs2(buf)
	if !ok || len(env) == 0 {
		return nil, false
	}
	return env, true
}
