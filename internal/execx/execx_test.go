package execx

import (
	"os"
	"os/exec"
	"testing"
)

func TestLivenessSelfAlive(t *testing.T) {
	if got := ProcessLiveness(os.Getpid()); got != LivenessAlive {
		t.Errorf("self liveness = %v, want Alive", got)
	}
}

func TestLivenessDeadPid(t *testing.T) {
	// Spawn a trivial child, reap it, then probe — it must read Dead.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait() // reap so the pid is fully gone (no zombie)
	if got := ProcessLiveness(pid); got != LivenessDead {
		t.Errorf("reaped-child liveness = %v, want Dead", got)
	}
}

func TestLivenessInit(t *testing.T) {
	// pid 1 exists but is not ours to signal as a normal user: EPERM => Alive.
	// (As root, kill(1,0) returns nil => also Alive.) Either way, never Dead.
	if got := ProcessLiveness(1); got == LivenessDead {
		t.Error("pid 1 reported Dead — EPERM must map to Alive")
	}
}

func TestLivenessNonPositive(t *testing.T) {
	if got := ProcessLiveness(0); got != LivenessDead {
		t.Errorf("pid 0 = %v, want Dead", got)
	}
	if got := ProcessLiveness(-5); got != LivenessDead {
		t.Errorf("pid -5 = %v, want Dead", got)
	}
}
