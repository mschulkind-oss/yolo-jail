package run

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
)

// TestTheIncompatibleDaemonWarningNamesTheCommandThatCyclesTHATDaemon is the test
// OQ-HD2 exists for, and it pins the CALL SITE rather than the sentence builder.
//
// # The defect
//
// startHostSingleton detects the one state every other liveness surface calls
// healthy: a daemon that predates the connection preamble is still listening at
// the derived path, will consume the front's preamble AS the client's request, and
// so fails every request while connect-and-close probes — including the in-jail
// reachability witness — report green. yolo does not kill it (two yolo versions on
// one host would take turns restarting each other's daemon); it warns and names
// the fix.
//
// The warning interpolated the loophole name into every clause BUT the fix, which
// read `Fix it with: yolo broker restart`. For `openai-auth-broker` or `aws-auth`
// that command cycles a DIFFERENT daemon and leaves the broken one running — a
// wrong instruction inside the sentence presenting itself as the remedy, printed
// at the moment a user is told their credential refresh is silently broken.
//
// # Why this fixture reaches it
//
// singletonFixture leaves exactly what an ALREADY-RUNNING pre-conversion daemon
// leaves: a bound socket and a live PID file, and NO capability stamp beside the
// PID file — the stamp is written by BrokerSpawn, and this daemon was never
// spawned by this build. So BrokerIsAlive is true, SingletonSpeaksPreamble is
// false, and the branch fires. The loophole name is deliberately not the broker's:
// under the old sentence this test's assertion is exactly what fails.
func TestTheIncompatibleDaemonWarningNamesTheCommandThatCyclesTHATDaemon(t *testing.T) {
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

	h, ok := o.startHostSingleton(name, hostScopedSpec(t.TempDir()+"/spawned"),
		socketsDir, "127.0.0.1", hostScopedDaemon())
	if !ok {
		t.Fatalf("the host-scoped service did not come up; output: %q", buf.String())
	}
	defer h.stop()

	out := buf.String()
	if !strings.Contains(out, "does not speak the connection preamble") {
		t.Fatalf("the alive-but-incompatible warning did not fire, so this test is "+
			"asserting nothing; output: %q", out)
	}
	want := broker.CycleCommand(name)
	if !strings.Contains(out, want) {
		t.Errorf("the warning does not name %q — the command it prints must cycle THIS "+
			"daemon, not whichever one the sentence was written for:\n%s", want, out)
	}
	if strings.Contains(out, "yolo broker restart") {
		t.Errorf("the warning still names `yolo broker restart` for %q, which cycles a "+
			"DIFFERENT daemon and leaves this one broken:\n%s", name, out)
	}
}
