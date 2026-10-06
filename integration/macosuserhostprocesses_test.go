package integration

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// TestMacosUserYoloPsListsAnAllowlistedHostProcess is the host-processes loophole on macos-user,
// end to end, now that its manifest declares darwin and the guest stages `yolo-ps`
// (macosuser.GuestClients): the host daemon's BSD-ps arm answers, the sandbox account reads the
// endpoint through the ACL grant, and the agent's `yolo-ps` lists an allowlisted host process —
// the window onto the host's process table the Seatbelt profile otherwise denies it
// (cross-process-procargs-deny) — and nothing outside the allowlist.
//
// THE SUBJECTS ARE THIS TEST'S OWN CHILDREN on the host: a `sleep` with a unique argument, whose
// ucomm `visible` names, and a `cat` blocked on a pipe, whose ucomm it does not. So the window is
// asserted both ways: the allowlisted pid is listed with its argument, and the other pid is not.
func TestMacosUserYoloPsListsAnAllowlistedHostProcess(t *testing.T) {
	requireMacosUser(t)
	// A long sleep whose argument no other process on the runner is likely to carry.
	arg := strconv.Itoa(86400 + os.Getpid()%10000)
	shown := exec.Command("/bin/sleep", arg)
	hidden := exec.Command("/bin/cat")
	stdin, err := hidden.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []*exec.Cmd{shown, hidden} {
		if err := c.Start(); err != nil {
			t.Fatalf("start %v: %v", c.Args, err)
		}
		c := c
		t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	}
	t.Cleanup(func() { _ = stdin.Close() })

	packHome(t, `{"packs": ["host-processes"], "loopholes": {"host-processes": {"enabled": true, `+
		`"settings": {"visible": ["sleep"]}}}}`)
	ws := macosUserWorkspace(t, `{}`)
	r := macosUserRunProbe(t, "host-processes", ws, strings.Join([]string{
		`echo "=== CLIENT ==="`,
		`command -v yolo-ps || echo MISSING`,
		`echo "=== PS ==="`,
		`yolo-ps; echo "PS_RC=$?"`,
		`echo "=== END ==="`,
	}, "\n"))
	diag := func() string {
		return fmt.Sprintf("\n--- launch stdout:\n%s\n--- launch stderr:\n%s", r.stdout, r.stderr)
	}

	if client := strings.TrimSpace(section(r.stdout, "=== CLIENT ===", "=== PS ===")); client !=
		macosuser.GuestBinaryPath("yolo-ps", "") {
		t.Errorf("yolo-ps resolves to %q in the sandbox, want the staged guest binary %s%s",
			client, macosuser.GuestBinaryPath("yolo-ps", ""), diag())
	}
	ps := section(r.stdout, "=== PS ===", "=== END ===")
	if !strings.Contains(ps, "PS_RC=0") {
		t.Fatalf("`yolo-ps` failed in the sandbox:\n%s%s", ps, diag())
	}
	listed := false
	for _, line := range strings.Split(ps, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == strconv.Itoa(shown.Process.Pid) {
			listed = strings.Contains(line, arg)
		}
		if len(fields) > 0 && fields[0] == strconv.Itoa(hidden.Process.Pid) {
			t.Errorf("yolo-ps shows pid %d (`cat`), which `visible` does not name:\n%s", hidden.Process.Pid, ps)
		}
	}
	if !listed {
		t.Errorf("yolo-ps does not list the allowlisted `sleep %s` (pid %d) with its argument:\n%s%s",
			arg, shown.Process.Pid, ps, diag())
	}
}
