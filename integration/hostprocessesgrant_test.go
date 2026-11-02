package integration

// hostprocessesgrant_test.go is the container tier of the host-processes flip
// (docs/reference/loophole-grants.md F3): the shipped `host-processes` loophole
// grants access through its valued `view` grant, not the old `enabled` switch and `visible`
// setting. Selecting the pack runs the daemon; what `yolo-ps` may show is the process names the
// project requested and a gate approved. The unit tiers pin the daemon's decision against a
// grant file (internal/hostprocesses/grants_test.go) and the launch's grant block
// (internal/cli/run); only a launch shows the config, the gate, the grant file, the daemon and
// the in-jail client joined. The macos-user tier is macosuserhostprocesses_test.go.

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// psProbe runs one request through the in-jail client and prints its exit status after the
// client's own output, so a refusal is read off the wire and not off the launch's status.
const psProbe = `yolo-ps; echo "PS-RC=$?"`

// A project that selected the pack and holds no grant gets the not_allowed refusal, exit 64:
// `view` is valued, so it is never requestable by default, and the refusal names the user's
// `yolo allow` before the project's `yolo request`. ps never runs.
func TestHostProcessesWithNoGrantRefusesInTheJail(t *testing.T) {
	requireJail(t)

	writeUserTOML(t, "packs = [\"host-processes\"]\n")
	dir := writeWorkspaceTOML(t, "")

	r := runYolo(t, dir, psProbe)
	if r.rc != 0 {
		t.Fatalf("the launch failed: rc %d\n%s", r.rc, r.combined())
	}
	t.Logf("the jail's output:\n%s", r.combined())
	if !strings.Contains(r.stdout, "PS-RC=64") {
		t.Fatalf("yolo-ps with no grant must exit 64 (a grant refusal):\n%s", r.combined())
	}
	// combined, not stderr alone: the in-jail stderr reaches this process through the
	// launcher, and only the refusal prints these lines.
	for _, want := range []string{
		"yolo allow host-processes on_request view",
		"yolo request host-processes view <process names>",
	} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the refusal must name %q:\n%s", want, r.combined())
		}
	}
}

// A user who allows `view` on request and a project that requests `sleep`, launched with the
// gate accepted (jailRunArgs carries --accept-config-changes), hold the grant: `yolo-ps` lists
// this test's own `sleep`, with its argument, and not its `cat`, whose name is not granted.
func TestHostProcessesViewGrantShowsOnlyTheGrantedNames(t *testing.T) {
	requireJail(t)
	// The subjects are this test's own children on the host the daemon runs on.
	arg := strconv.Itoa(86400 + os.Getpid()%10000)
	shown := exec.Command("sleep", arg)
	hidden := exec.Command("cat")
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

	writeUserTOML(t, "packs = [\"host-processes\"]\n\n[allow.host-processes]\non_request = [\"view\"]\n")
	dir := writeWorkspaceTOML(t, "[requests.host-processes]\nview = [\"sleep\"]\n")

	r := runYolo(t, dir, psProbe)
	if r.rc != 0 {
		t.Fatalf("the launch failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.stdout, "PS-RC=0") {
		t.Fatalf("yolo-ps with `view = [\"sleep\"]` requested, allowed and accepted must exit 0:\n%s",
			r.combined())
	}
	listed := false
	for _, line := range strings.Split(r.stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == strconv.Itoa(shown.Process.Pid) {
			listed = strings.Contains(line, arg)
		}
		if len(fields) > 0 && fields[0] == strconv.Itoa(hidden.Process.Pid) {
			t.Errorf("yolo-ps shows pid %d (`cat`), which the view grant does not name:\n%s",
				hidden.Process.Pid, r.stdout)
		}
	}
	if !listed {
		t.Errorf("yolo-ps does not list the granted `sleep %s` (pid %d) with its argument:\n%s",
			arg, shown.Process.Pid, r.combined())
	}
}
