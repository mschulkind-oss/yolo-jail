package broker

import (
	"os"
	"strconv"
	"strings"
)

// launchCheckStamp marks a singleton as spawned by a yolo that knows the launch check
// (internal/hostservice/launchcheck.go coins the term). Bump it only if what a launch may conclude
// from the stamp changes.
//
// # The question it answers
//
// A host-wide daemon that answers the launch check `unknown action: launch-check` is in one of two
// states, and a launch must not confuse them (docs/design/host-daemon-ownership.md HD-D6):
//
//   - a yolo older than the check spawned it and it kept running through the upgrade. Restarting it
//     with this yolo fixes the answer, so the launch refuses and names `yolo host-daemon restart`
//     (OQ-HD11).
//   - a yolo that knows the check spawned it, and its program still does not answer. Then the
//     loophole's manifest declares a check its program lacks, and the next restart starts the same
//     program from the same manifest: the launch keeps the warning a per-launch daemon gets, because
//     a refusal naming that restart would refuse again after it.
//
// The answer alone cannot tell the two apart; this stamp does, without a byte of protocol.
//
// # Why it names the pid
//
// The stamp is a sibling of the PID file, written at spawn and removed with it by BrokerKill. A yolo
// older than this stamp does not know the file, so when it stops and respawns the daemon it leaves
// this one behind, naming the pid of a process that is gone. A stamp is honored only while it names
// the pid the PID file does.
const launchCheckStamp = "launch-check-v1"

// launchCheckStampPath is the stamp file, beside the PID file and the preamble's stamp.
func launchCheckStampPath(deps Deps) string { return deps.PIDFilePath + ".launch-check" }

// StampLaunchCheck records that this yolo spawned the singleton whose pid is pid. EnsureSingleton
// writes it at every spawn; a test standing up a daemon as this yolo would have started it writes
// it too. Best effort, as the preamble's stamp is: a stamp that could not be written reads as a
// daemon an older yolo started, which costs one refusal and one restart.
func StampLaunchCheck(deps Deps, pid int) {
	_ = os.WriteFile(launchCheckStampPath(deps),
		[]byte(launchCheckStamp+" "+strconv.Itoa(pid)+"\n"), 0o644)
}

// SpawnedKnowingLaunchCheck reports whether the running singleton was spawned by a yolo that knows
// the launch check: the stamp is present, current, and names the pid in the PID file. False when
// there is no PID file, no stamp, a stamp of another version, or one naming another pid.
func SpawnedKnowingLaunchCheck(deps Deps) bool {
	pid, ok := BrokerReadPID(deps)
	if !ok {
		return false
	}
	raw, err := os.ReadFile(launchCheckStampPath(deps))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(raw)) == launchCheckStamp+" "+strconv.Itoa(pid)
}

// StampPreamble records that this yolo spawned the singleton, so it expects the connection
// preamble (SingletonSpeaksPreamble). EnsureSingleton writes it at every spawn; it is exported for
// the tests that stand up a daemon as this yolo would have started it.
func StampPreamble(deps Deps) {
	_ = os.WriteFile(singletonStampPath(deps), []byte(singletonStamp+"\n"), 0o644)
}
