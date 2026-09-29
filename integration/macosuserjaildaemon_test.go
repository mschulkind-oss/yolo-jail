package integration

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// TestMacosUserJailDaemonRunsConfinedInTheGuest is steps 3 and 4 of
// docs/design/jail-daemon-on-macos-user-plan.md on the hardware, built on OQ-DP8 and OQ-DP9 of
// docs/design/declaration-parity.md: a bare `"packs": ["claude"]` selects the OpenAI refresh
// adapter, and the macos-user launch must stage a DARWIN yolo-jaild into the sandbox's own
// prefix, start `yolo-jaild supervise` under the session's Seatbelt profile as the sandbox
// account, and have the adapter bind — then leave no supervisor behind when the command exits.
//
// The subject is the OpenAI adapter rather than the plan's hello-daemon: hello-daemon's argv
// names the container's loophole mount, which the guest declines by name (loopholes'
// guestrun.go), and an embedded pack's files are 0444 anyway (OQ-BP5).
//
// WHAT ONLY THIS TEST CAN SEE, every item of it unexecuted before: that the Go-built darwin
// yolo-jaild is signed well enough for the kernel to exec from /var/yolo-jail/bin; that
// sandbox-exec admits the supervisor and the adapter it execs; that the adapter can READ its
// root-owned 0600 daemon env file through the `user:` ACE (its log says "serving on", not
// "idling, serving nothing"); that it can write its log under the sandbox home; and that the
// stop's SIGTERM, relayed by `sudo -n`, ends the supervisor.
func TestMacosUserJailDaemonRunsConfinedInTheGuest(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)
	log := "$HOME/.local/state/yolo-jail-daemons/openai-auth-broker.log"
	r := macosUserRunProbe(t, "jail-daemon", ws, strings.Join([]string{
		`echo "=== PROBE ==="`,
		`echo "JAILD=$(command -v yolo-jaild || echo NONE)"`,
		`for i in $(seq 1 50); do grep -q 'serving on' "` + log + `" 2>/dev/null && break; sleep 0.2; done`,
		`echo "=== LOG ==="`,
		`cat "` + log + `" 2>&1 || echo "NO LOG"`,
		`echo "=== PS ==="`,
		`ps -axo user=,command= | grep '[y]olo-jaild' || echo "NO PROCESS"`,
		`echo "=== END ==="`,
	}, "\n"))
	out := r.combined()
	probe := section(r.stdout, "=== PROBE ===", "=== LOG ===")
	logBody := section(r.stdout, "=== LOG ===", "=== PS ===")
	ps := section(r.stdout, "=== PS ===", "=== END ===")

	if want := "JAILD=" + macosuser.GuestBinaryPath(macosuser.JaildName, ""); !strings.Contains(probe, want) {
		t.Errorf("the sandbox does not resolve yolo-jaild to the staged guest prefix (want %q):\n%s",
			want, probe)
	}
	if !strings.Contains(out, "Started openai-auth-broker inside the sandbox") {
		t.Errorf("the launch did not disclose the guest's jail daemon:\n%s", out)
	}
	if strings.Contains(logBody, "spawn failed:") || !strings.Contains(logBody, "serving on") {
		t.Errorf("the OpenAI adapter did not start and serve in the guest (spawn failed, or no "+
			"caller token read from the daemon env file):\n%s\nlaunch output:\n%s", logBody, out)
	}
	// Listed from inside the session (BSD ps may truncate the user column, so the command is
	// what is matched).
	if !strings.Contains(ps, macosuser.GuestBinaryPath(macosuser.JaildName, "")+" supervise") {
		t.Errorf("no yolo-jaild supervise runs during the session:\n%s", ps)
	}

	// Nothing survives the session: the stop is SIGTERM to the supervisor's process group.
	deadline := time.Now().Add(20 * time.Second)
	for {
		left, _ := exec.Command("pgrep", "-u", macosuser.SandboxUser, "-f", "yolo-jaild").Output()
		if strings.TrimSpace(string(left)) == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("a yolo-jaild of %s survives the session (pids %s)", macosuser.SandboxUser,
				strings.TrimSpace(string(left)))
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// And the in-jail binary is the SANDBOX's, never on the host's PATH (OQ-DP8).
	if p, err := exec.LookPath("yolo-jaild"); err == nil {
		t.Errorf("yolo-jaild resolves on the HOST's PATH (%s); the host ship set is {yolo}", p)
	}
}
