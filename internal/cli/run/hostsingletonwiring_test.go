package run

import (
	"os"
	"strings"
	"testing"
)

// hostsingletonwiring_test.go pins the two one-line PRODUCTION WIRINGS that
// `host_daemon.scope: "host"` needs and that no behavioural test in this package
// reaches. Both were found by mutation: deleting either one leaves the entire unit
// gate green, including every test in hostsingleton_test.go, which is the shape
// AGENTS.md names — the callee is pinned, the caller is not.
//
// # Why these two are source assertions and the rest of the file's are not
//
// The same reason TestBrokerLifecycleIsGatedOnTheLoopholeRecord gives: the insert
// loop lives inside runNormal, which needs a container, and the scope comparison's
// interesting input (a HostDaemon whose Scope is the ZERO VALUE) cannot be produced
// through discovery at all — parseHostDaemon defaults an absent `scope` to
// ScopeJail and discover.go writes ScopeJail by hand, so the only way to observe a
// "" scope is the way it actually happened once: a field dropped between the
// decoder and the record (resolve() in loopholes/load.go, fixed in the same commit
// that added the field). A test that hand-built the record would be asserting about
// a struct rather than about the branch.
//
// So these assert the EXPRESSION. That is weaker than driving it, and it is the
// strongest thing available at this seam; the alternative measured today is nothing
// at all.

// runSource reads run.go, the file both assertions below are about.
func runSource(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestHostScopedHandleContributesNoContainerEnv is the CONSUMER half of
// TestHostScopedHandleEmitsNoEnvVar, and without it that test pins a value nobody
// is required to read.
//
// startHostSingleton returns a handle with an EMPTY envVarName, deliberately: a
// host-scoped loophole's jail-facing variable is emitted much earlier, by
// hostServicesMountArgs at argv-assembly time (see that function's own note on why
// the optimism is load-bearing). runNormal's insert loop must therefore SKIP such a
// handle.
//
// Delete the skip and the loop appends `-e` followed by `"" + "=" + jailPath` — a
// literal `-e =/var/lib/yolo-jail/host-services/claude-oauth-broker.endpoint` in the
// container argv of every jail that selected `packs: ["claude"]` with a live broker.
// That is an environment variable with no name, on every launch, for the one loophole
// that is on by default. Measured 2026-08-19: removing the skip passes `go test
// -short ./...` in full.
func TestHostScopedHandleContributesNoContainerEnv(t *testing.T) {
	src := runSource(t)
	if !strings.Contains(src, `if svc.envVarName == "" {`) {
		t.Error("runNormal's host-service insert loop no longer skips a handle with no env " +
			"var name. A host-scoped loophole's handle carries one by design " +
			"(TestHostScopedHandleEmitsNoEnvVar), so without the skip every claude jail's " +
			"argv gains a nameless `-e =<path>` pair")
	}
	// The control: the loop must still INSERT for a handle that does carry a name, or
	// the assertion above would be satisfied by a loop that emits nothing at all and
	// every per-jail loophole would lose its endpoint variable.
	if !strings.Contains(src, `svc.envVarName + "=" + svc.jailPath`) {
		t.Error("runNormal no longer inserts `-e <VAR>=<jailPath>` for a handle that has a " +
			"variable — the skip above is only correct while the ordinary path still runs")
	}
}

// TestScopeDispatchComparesAgainstScopeHost pins the DIRECTION of startLoopholes'
// scope test, which is a property the sprint that added `scope` wrote down in three
// places and pinned in none.
//
// loopholedecl.HostDaemon.Scope says it: "The zero value of the FIELD is \"\", not
// ScopeJail, and every reader must therefore compare against ScopeHost rather than
// against ScopeJail". The reason is asymmetric damage. Comparing against ScopeHost
// makes a lost field cost a SPAWN — one extra per-jail daemon, loud, and the shape
// every non-singleton loophole already has. Comparing against ScopeJail makes a lost
// field turn EVERY manifest daemon into a host-wide singleton: yolo would decline to
// start a process the manifest asked for and instead ensure one at
// /tmp/yolo-<name>.sock shared across every jail on the machine.
//
// This is not hypothetical. The field WAS dropped once, in the commit that added it
// — resolve() in loopholes/load.go rebuilt HostDaemon field by field and left Scope
// out, so every `scope: "host"` manifest arrived with Scope="". Under the inverted
// comparison that same defect would have shipped as one shared daemon per loophole
// instead of two brokers per host, and TestHostDaemonFieldsSurviveLoad (the test
// written for the drop) would not have distinguished them.
//
// Measured 2026-08-19: rewriting the comparison to `!= loopholes.ScopeJail` passes
// `go test -short ./...` in full.
func TestScopeDispatchComparesAgainstScopeHost(t *testing.T) {
	body, err := os.ReadFile("loopholesruntime.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	if !strings.Contains(src, "hd.Scope == loopholes.ScopeHost") {
		t.Error("startLoopholes no longer dispatches on `hd.Scope == loopholes.ScopeHost`. " +
			"A record with no scope at all must be PER-JAIL: the zero value of the field is " +
			"\"\", so a comparison written the other way round (`!= ScopeJail`) makes a " +
			"dropped field promote every manifest daemon to a host-wide singleton")
	}
	if strings.Contains(src, "hd.Scope != loopholes.ScopeJail") {
		t.Error("the scope dispatch compares against ScopeJail. That inverts which way a lost " +
			"field fails: it must cost a spawn, never a shared daemon nobody ensured " +
			"(loopholedecl.HostDaemon.Scope states the rule; resolve() has already dropped " +
			"this field once)")
	}
}
