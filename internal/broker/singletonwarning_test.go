package broker

import (
	"bytes"
	"strings"
	"testing"
)

// TestFailedSpawnWarningNamesTheLoophole pins the half of the `scope: "host"`
// generalization that has no mechanical consequence and is therefore the half most
// easily left behind: the DIAGNOSTIC.
//
// This package's lifecycle stopped being the broker's alone when
// `host_daemon.scope: "host"` landed — SingletonDeps builds these Deps from any
// loophole's manifest, and its paths, argv and log are all derived from the name.
// reportFailedSpawn was not: it said "the Claude OAuth broker singleton" for every
// caller, and the sentence after it named Claude auth. The broker is the only
// loophole declaring the scope today, so nothing MISBEHAVES — which is exactly why
// a test is the only thing that will notice. The second host-scoped daemon to fail
// its spawn would print a warning naming a different loophole entirely, at the one
// moment its owner is reading the launch output, and send them to
// `yolo broker status` for a daemon that is not the broker.
//
// It asserts through BrokerSpawn rather than calling reportFailedSpawn directly:
// the reporter is unexported and the thing worth pinning is that the failure path
// reaches it with the record's name still attached.
func TestFailedSpawnWarningNamesTheLoophole(t *testing.T) {
	// A daemon that exits at startup without binding — the measured shape (the
	// missing-openssl case), and the branch that produces a warning at all.
	st := &fakeState{spawnPID: 11, spawnExited: true}
	deps := newFakeDeps(t, st)
	deps.Name = "yjtest-other-singleton"
	var buf bytes.Buffer
	deps.Out = &buf
	_ = BrokerSpawn(deps)

	out := buf.String()
	if !strings.Contains(out, "yjtest-other-singleton") {
		t.Errorf("the failed-spawn warning does not name the loophole from the record, so a "+
			"host-scoped daemon that is not the broker reports someone else's failure:\n%s", out)
	}
	if strings.Contains(out, "Claude") {
		t.Errorf("the failed-spawn warning still hardcodes the broker; SingletonDeps builds "+
			"these Deps for any `host_daemon.scope: \"host\"` loophole:\n%s", out)
	}
}

// TestSingletonDepsCarriesTheName is the CALL-SITE half. The warning above can only
// name the loophole if the constructor every production path goes through actually
// puts it on the record — and a Deps whose Name is empty degrades silently to the
// generic phrasing, which is a passing warning and a lost fact.
func TestSingletonDepsCarriesTheName(t *testing.T) {
	if got := SingletonDeps("yjtest-named", nil).Name; got != "yjtest-named" {
		t.Errorf("SingletonDeps(...).Name = %q, want %q — every host-scoped spawn is built "+
			"here, so a dropped name silences the loophole's identity in its own failure "+
			"warning", got, "yjtest-named")
	}
	if got := RealDeps().Name; got != BrokerLoopholeName {
		t.Errorf("RealDeps().Name = %q, want %q", got, BrokerLoopholeName)
	}
}
