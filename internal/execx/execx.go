// Package execx codifies the subprocess / process-liveness incident history
// from the Python code in Go types, so idiomatic-Go habits can't reintroduce
// the bugs the Python comments record (§3 internal/execx, and the tri-state
// standing review item).
//
// Key invariants preserved:
//   - "tool absent = no-op, never error": a missing executable or a timeout on
//     a best-effort probe yields an empty result, not a hard failure (the
//     Python subprocess sites catch FileNotFoundError/TimeoutExpired and
//     return empty).
//   - Tri-state liveness via kill(pid, 0): ESRCH => dead, EPERM => ALIVE
//     (a process we can't signal still exists), any other errno => treat as
//     alive (conservative). A naive `err == nil` check would wrongly report a
//     live-but-unsignalable process as dead.
//
// Source of truth: the subprocess/liveness patterns across src/ (relay orphan
// reaping, _live_yolo_containers, host_service self-checks).
package execx

import (
	"errors"
	"syscall"
)

// Liveness is the tri-state result of a kill(pid, 0) probe. None (unknown) is
// NOT the same as "dead": callers that "decline to act" on unknown must check
// for LivenessUnknown explicitly, never collapse it to dead.
type Liveness int

const (
	// LivenessDead: the process does not exist (ESRCH).
	LivenessDead Liveness = iota
	// LivenessAlive: the process exists — either we signaled it (err==nil) or
	// we lack permission to (EPERM), which still proves existence.
	LivenessAlive
	// LivenessUnknown: an unexpected errno; the caller decides, but must not
	// treat this as a licence to reap/prune.
	LivenessUnknown
)

// ProcessLiveness probes whether pid is alive using signal 0, with the correct
// errno polarity:
//
//	kill(pid, 0) == nil    -> alive (we could have signaled it)
//	errno == ESRCH         -> dead
//	errno == EPERM         -> ALIVE (exists but not ours to signal)
//	other errno            -> unknown
func ProcessLiveness(pid int) Liveness {
	if pid <= 0 {
		return LivenessDead
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return LivenessAlive
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ESRCH:
			return LivenessDead
		case syscall.EPERM:
			return LivenessAlive
		}
	}
	return LivenessUnknown
}

// IsAlive is the convenience predicate: true iff ProcessLiveness is Alive.
// Use ProcessLiveness directly when Unknown must be distinguished from Dead.
func IsAlive(pid int) bool {
	return ProcessLiveness(pid) == LivenessAlive
}
