package run

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
)

// TestTheIncompatibleDaemonRefusalNamesTheCommandThatCyclesTHATDaemon is the test OQ-HD2 exists
// for, carried over to the refusal that replaced its warning (HD-D5 (3)), and it pins the CALL
// SITE that detects the state rather than only the sentence builder.
//
// # The defect
//
// startHostSingleton detects the one state every other liveness surface calls healthy: a daemon
// that predates the connection preamble is still listening at the derived path, will consume the
// front's preamble AS the client's request, and so fails every request while connect-and-close
// probes — including the in-jail reachability witness — report green. yolo does not kill it (two
// yolo versions on one host would take turns restarting each other's daemon); the launch refuses
// and names the fix.
//
// The warning before the refusal interpolated the loophole name into every clause BUT the fix,
// which read `Fix it with: yolo broker restart`. For `openai-auth-broker` or `aws-auth` that
// command cycles a DIFFERENT daemon and leaves the broken one running — a wrong instruction inside
// the sentence presenting itself as the remedy.
//
// # Why this fixture reaches it
//
// singletonFixture leaves exactly what an ALREADY-RUNNING pre-conversion daemon leaves: a bound
// socket and a live PID file, and NO capability stamp beside the PID file — the stamp is written
// by the spawn, and this daemon was never spawned by this build. So BrokerIsAlive is true,
// SingletonSpeaksPreamble is false, and the handle is marked. The loophole name is deliberately not
// the broker's: under the old sentence this test's assertion is exactly what fails.
func TestTheIncompatibleDaemonRefusalNamesTheCommandThatCyclesTHATDaemon(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("binds host-wide /tmp singleton paths")
	}
	name := "yjtest-singleton-cycle"
	singletonFixture(t, name)

	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	o.Stderr = &buf

	h, ok := o.startHostSingleton(name, hostScopedSpec(t.TempDir()+"/spawned"),
		socketsDir, "127.0.0.1", hostScopedDaemon())
	if !ok {
		t.Fatalf("the host-scoped service did not come up; output: %q", buf.String())
	}
	defer h.stop()
	if !h.predatesPreamble {
		t.Fatalf("the alive-but-incompatible daemon was not marked, so this test is asserting "+
			"nothing; output: %q", buf.String())
	}

	refused := o.runLaunchChecks("podman", []loopholeDaemon{h}, nil, freshLaunchCheck)
	if refused == nil {
		t.Fatal("a fresh launch whose host-wide daemon predates the preamble was not refused")
	}
	out := refused.markup("Refusing this launch")
	if !strings.Contains(out, "does not speak the connection preamble") {
		t.Errorf("the refusal does not say what the daemon lacks:\n%s", out)
	}
	want := broker.CycleCommand(name)
	if !strings.Contains(out, want) {
		t.Errorf("the refusal does not name %q — the command it prints must cycle THIS "+
			"daemon, not whichever one the sentence was written for:\n%s", want, out)
	}
	if strings.Contains(out, "yolo broker restart") {
		t.Errorf("the refusal names `yolo broker restart` for %q, which cycles a "+
			"DIFFERENT daemon and leaves this one broken:\n%s", name, out)
	}
	if strings.Contains(buf.String(), "does not speak the connection preamble") {
		t.Errorf("startHostSingleton still prints a warning the refusal replaced:\n%s", buf.String())
	}
}
