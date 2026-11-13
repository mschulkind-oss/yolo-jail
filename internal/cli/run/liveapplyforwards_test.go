package run

import (
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// liveapplyforwards_test.go pins LA-S4d (docs/design/live-config-apply.md): a changed
// machine.forward reaches a running podman jail that reaches the host's loopback through an
// in-jail socat started with `podman exec`, and waits for the restart everywhere else, saying why.
// These drive argv only: whether the forward carries traffic is reachability-shaped, and only a
// real rootless host shows it.

// forwardsFixture is applyFixture whose jail env is env and whose `podman exec` calls are
// recorded and answered rc (0 for each unless failOn names an argv word it fails on).
func forwardsFixture(t *testing.T, boot, now, env, failOn string) (*Options, *strings.Builder, *[][]string) {
	t.Helper()
	o, buf := applyFixture(t, boot, now, applyJail{running: true, env: env})
	var execs [][]string
	inner := o.Exec
	o.Exec = func(argv []string, dir string, e []string, timeout time.Duration) ExecResult {
		if len(argv) > 2 && argv[0] == "podman" && argv[1] == "exec" {
			execs = append(execs, argv)
			if failOn != "" && strings.Contains(strings.Join(argv, " "), failOn) {
				return ExecResult{Ran: true, RC: 3, Stderr: "the jail's port 9000 is held by another process"}
			}
			return ExecResult{Ran: true}
		}
		return inner(argv, dir, e, timeout)
	}
	answer(o, "y")
	return o, buf, &execs
}

const loopbackEnv = "YOLO_HOST_LOOPBACK=requested\nYOLO_FORWARD_HOST_PORTS=[8080]\n"

func TestConfigApplyChangesForwardsLive(t *testing.T) {
	o, buf, execs := forwardsFixture(t, `{"network":{"forward_host_ports":[8080]}}`,
		`{"network":{"forward_host_ports":["9000:9001"]}}`, loopbackEnv, "")
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	out := buf.String()
	for _, line := range []string{
		"- machine.forward 8080: applied now — the jail can still reach host port 8080 at host.containers.internal",
		"+ machine.forward 9000 -> host 9001: applied now",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("missing %q:\n%s", line, out)
		}
	}
	cname := o.jailName()
	var got []string
	for _, a := range *execs {
		got = append(got, strings.Join(a, " "))
	}
	joined := strings.Join(got, "\n")
	// The removal stops the jail's socat for 8080 and starts nothing.
	if !strings.Contains(joined, "podman exec --detach-keys= "+cname+" sh -c "+forwardFreeScript+" sh 8080 1F90 ^socat TCP-LISTEN:8080,") {
		t.Errorf("no stop for 8080:\n%s", joined)
	}
	if strings.Contains(joined, "TCP-LISTEN:8080,bind") {
		t.Errorf("the removal started a forward:\n%s", joined)
	}
	// The addition frees the port, starts the entrypoint's own socat argv dialing the host by
	// name, and waits until it listens.
	for _, want := range []string{
		"podman exec --detach-keys= " + cname + " sh -c " + forwardFreeScript + " sh 9000 2328 ^socat TCP-LISTEN:9000,",
		"podman exec --detach-keys= -d " + cname + " socat TCP-LISTEN:9000,bind=127.0.0.1,fork,reuseaddr TCP:host.containers.internal:9001",
		"podman exec --detach-keys= " + cname + " sh -c " + forwardListeningScript + " sh 9000 2328",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	if _, drifted, ok, err := config.WorkspaceConfigDrift(o.Workspace, "podman-self"); err != nil || !ok || drifted {
		t.Errorf("an applied forward change was not folded: drifted=%v ok=%v err=%v", drifted, ok, err)
	}
}

// A jail that cannot reach the host's loopback, a port something else forwards, and a step that
// fails each wait for the restart with the reason, and nothing is folded.
func TestConfigApplyForwardsThatCannotChangeLiveWait(t *testing.T) {
	for _, c := range []struct {
		name, env, failOn, line string
		execs                   bool
	}{
		{"no loopback", "YOLO_HOST_LOOPBACK=unsupported\n", "",
			"+ machine.forward 9000 -> host 9001: at next restart (yolo stop, then yolo) — this jail cannot reach the host's loopback (YOLO_HOST_LOOPBACK=unsupported)", false},
		{"unsaid", "", "",
			"+ machine.forward 9000 -> host 9001: at next restart (yolo stop, then yolo) — this jail does not say it reaches the host's loopback", false},
		{"forwarded elsewhere", "YOLO_HOST_LOOPBACK=shared\nYOLO_FORWARD_HOST_PORTS=[9000]\n", "",
			"+ machine.forward 9000 -> host 9001: at next restart (yolo stop, then yolo) — local port 9000 is also forwarded by your user config or a provider", false},
		{"held port", loopbackEnv, "sh 9000",
			"+ machine.forward 9000 -> host 9001: at next restart (yolo stop, then yolo) — the jail's port 9000 is held by another process", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			o, buf, execs := forwardsFixture(t, `{}`, `{"network":{"forward_host_ports":["9000:9001"]}}`, c.env, c.failOn)
			if rc := o.configApply(false); rc != 3 {
				t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
			}
			if !strings.Contains(buf.String(), c.line) {
				t.Errorf("missing %q:\n%s", c.line, buf.String())
			}
			if (len(*execs) > 0) != c.execs {
				t.Errorf("podman exec calls = %v, want any: %v", *execs, c.execs)
			}
			if _, drifted, _, _ := config.WorkspaceConfigDrift(o.Workspace, "podman-self"); !drifted {
				t.Error("a waiting forward change was folded")
			}
		})
	}
}

// The in-jail scripts run as written: the listening probe finds a real listener by /proc/net,
// and the free step returns at once for a port nothing holds. Linux only: they read /proc.
func TestTheForwardScriptsReadProcNet(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the in-jail scripts read Linux's /proc/net")
	}
	for _, tool := range []string{"sh", "awk", "pkill"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not on PATH")
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	run := func(script string, args ...string) error {
		return exec.Command("sh", append([]string{"-c", script, "sh"}, args...)...).Run()
	}
	if err := run(forwardListeningScript, strconv.Itoa(port), portHex(port)); err != nil {
		t.Errorf("the listening probe missed a listener on %d: %v", port, err)
	}
	ln.Close()
	if err := run(forwardFreeScript, strconv.Itoa(port), portHex(port), forwardSocatPattern(port)); err != nil {
		t.Errorf("the free step failed on a free port %d: %v", port, err)
	}
}

// Apple Container and macos-user plan no forward step.
func TestTheForwardPlanPerBackend(t *testing.T) {
	boot := mapOf(t, `{"machine":{"forward":[]}}`)
	cur := mapOf(t, `{"machine":{"forward":[{"jail":9000,"host":9001}]}}`)
	for _, rt := range []string{"container", "macos-user"} {
		live, why := forwardsLiveFor(rt, []string{"YOLO_HOST_LOOPBACK=requested"})
		plan := buildApplyPlan(applyPlanInput{rt: rt, running: true, bootOK: true, boot: boot, current: cur,
			forwardsLive: live, forwardsReason: why})
		if len(plan) != 1 || plan[0].kind == changeForward || plan[0].outcome == outcomeAppliedNow {
			t.Errorf("%s: plan = %+v", rt, plan)
		}
	}
	if live, _ := forwardsLiveFor("podman", []string{"YOLO_FWD_HOST_GATEWAY=host.containers.internal"}); !live {
		t.Error("a macOS podman jail's TCP gateway is the same hop")
	}
}
