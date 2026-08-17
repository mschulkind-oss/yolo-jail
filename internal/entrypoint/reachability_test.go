package entrypoint

// reachability_test.go covers the in-jail witness. The test that matters most is
// the SILENT one: a probe that warns when everything is fine trains people to
// ignore it, and this probe is on its way to being fatal (OQ-R2), where a
// misfire costs a jail rather than a log line.
//
// Everything here runs in-process against a real loopback-TLS listener — no
// container, so it belongs in the unit gate. What it deliberately CANNOT cover is
// the bug itself: reproducing "the runtime forwards the host gateway somewhere
// else" needs a real jail on a real pasta host, which is why the end-to-end
// assertion lives in integration/reachability_test.go.

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// shrinkReachabilityBudget shortens the probe's timings for the duration of one
// test and restores them after.
//
// The shipped numbers are a POLICY (30s of anti-starvation headroom, retries for a
// daemon caught mid-republish); what the tests exercise is the MECHANISM. Leaving
// them at their real values would make the unreachable cases spend a second each
// re-learning an answer that arrives instantly, for no added coverage.
func shrinkReachabilityBudget(t *testing.T) {
	t.Helper()
	dial, retries, delay, budget :=
		reachabilityDialTimeout, reachabilityRetries, reachabilityRetryDelay, reachabilityBudget
	t.Cleanup(func() {
		reachabilityDialTimeout, reachabilityRetries, reachabilityRetryDelay, reachabilityBudget =
			dial, retries, delay, budget
	})
	reachabilityDialTimeout = 2 * time.Second
	reachabilityRetries = 1
	reachabilityRetryDelay = 10 * time.Millisecond
	reachabilityBudget = 5 * time.Second
}

// servicesDir returns a 0700 directory to publish endpoint files into.
// svcendpoint.Publish refuses a group- or world-accessible directory (it holds a
// credential), and t.TempDir already creates 0700 — this asserts that rather than
// assuming it, so a change in Go's tempdir mode surfaces here and not as a
// confusing publish error.
func servicesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// liveEndpoint stands a real authenticated loopback-TLS listener advertising
// 127.0.0.1 — the in-test stand-in for "the runtime forwards the gateway to the
// host's loopback" — and accepts (and drops) whatever connects, which is exactly
// what the probe's connect-and-close does.
func liveEndpoint(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name+paths.ServiceEndpointExt)
	ln, err := svcendpoint.Listen(path, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return path
}

// deadEndpoint publishes a COMPLETE, well-formed endpoint whose address answers
// nothing: a listener is stood up, its publication captured, the listener closed,
// and the same publication written back. That is the shape of the real outage —
// the file is perfect and the address is unreachable — and it is the only way to
// produce it without a second network namespace.
func deadEndpoint(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name+paths.ServiceEndpointExt)
	ln, err := svcendpoint.Listen(path, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	ep, err := svcendpoint.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	// Close unlinks the publication with the listener, so republish it afterwards.
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if err := svcendpoint.Publish(path, ep); err != nil {
		t.Fatal(err)
	}
	return path
}

// probeWarnings runs the probe over one env matrix and returns everything it said.
func probeWarnings(t *testing.T, vars map[string]string) string {
	t.Helper()
	var out strings.Builder
	e := NewEnv(vars)
	e.Stderr = &out
	ProbeServiceReachability(e)
	return out.String()
}

// TestReachabilityProbeSaysNothingWithNoServices is the "a jail with no loopholes
// must print nothing" requirement, and it covers the near-miss too: the RETIRING
// _SOCKET spelling names a bind-mounted AF_UNIX socket, which no forwarding
// decision can break, so probing it would be noise about a hop that does not
// exist.
func TestReachabilityProbeSaysNothingWithNoServices(t *testing.T) {
	shrinkReachabilityBudget(t)

	if got := probeWarnings(t, map[string]string{"JAIL_HOME": t.TempDir()}); got != "" {
		t.Errorf("a jail with no jail-facing services must print NOTHING, got:\n%s", got)
	}

	got := probeWarnings(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		// A path that does not exist: were this spelling probed at all, it would
		// warn loudly, so silence here proves the filter and not just an empty run.
		paths.ServiceEnvVarPrefix + "CGROUP_DELEGATE_SOCKET": "/run/yolo-services/cgroup-delegate.sock",
	})
	if got != "" {
		t.Errorf("the _SOCKET spelling is a bind-mounted unix socket with no forwarding "+
			"hop to get wrong and must not be probed, got:\n%s", got)
	}
}

// TestReachabilityProbeSilentWhenReachable: the healthy path is silent. This is
// the test that has to hold before OQ-R2 flips the warning to a launch failure.
func TestReachabilityProbeSilentWhenReachable(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)
	path := liveEndpoint(t, dir, "host-processes")

	got := probeWarnings(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: path,
	})
	if got != "" {
		t.Errorf("a reachable service must produce no output at all, got:\n%s", got)
	}
}

// TestReachabilityProbeNamesServiceAndAddress is the finding itself. Without BOTH
// the service name and the address it could not reach, the line is unactionable —
// the reader cannot tell which loophole is down, nor that the address is a
// gateway name rather than a host they control.
func TestReachabilityProbeNamesServiceAndAddress(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)
	path := deadEndpoint(t, dir, "claude-oauth-broker")

	ep, err := svcendpoint.Read(path)
	if err != nil {
		t.Fatal(err)
	}

	got := probeWarnings(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "CLAUDE_OAUTH_BROKER" + paths.ServiceEnvVarSuffix: path,
	})
	if !strings.Contains(got, "claude-oauth-broker") {
		t.Errorf("the warning must NAME the service, got:\n%s", got)
	}
	if !strings.Contains(got, ep.HostPort) {
		t.Errorf("the warning must name the ADVERTISED address it could not reach (%s), got:\n%s",
			ep.HostPort, got)
	}
	if !strings.Contains(got, "UNREACHABLE") {
		t.Errorf("an unreachable service must be attributed as such, not as a missing or "+
			"stale endpoint — those have different fixes. got:\n%s", got)
	}
}

// TestReachabilityProbeNeverPrintsTheToken. The endpoint file is a CREDENTIAL: it
// carries this jail's bearer token next to the address, and every diagnostic in
// the tree is bound by the rule that the path may be named and the contents may
// not. A warning is printed on a failure path, which is exactly where that rule
// tends to get forgotten.
func TestReachabilityProbeNeverPrintsTheToken(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)
	path := deadEndpoint(t, dir, "journal")

	ep, err := svcendpoint.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	got := probeWarnings(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "JOURNAL" + paths.ServiceEnvVarSuffix: path,
	})
	if strings.Contains(got, ep.Token) {
		t.Error("the probe leaked this jail's bearer token into a boot warning")
	}
}

// TestReachabilityProbeAttributesAnUnpublishedEndpoint. "The launcher wired the
// variable and the host half never published" is a different fault with a
// different fix from "the address does not answer", and collapsing the two sends
// the reader hunting the network stack for a missing file.
func TestReachabilityProbeAttributesAnUnpublishedEndpoint(t *testing.T) {
	shrinkReachabilityBudget(t)
	missing := filepath.Join(servicesDir(t), "host-processes"+paths.ServiceEndpointExt)

	got := probeWarnings(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: missing,
	})
	if !strings.Contains(got, "host-processes") {
		t.Errorf("the warning must name the service, got:\n%s", got)
	}
	if !strings.Contains(got, missing) {
		t.Errorf("the warning must name the endpoint file that is absent, got:\n%s", got)
	}
	if strings.Contains(got, "UNREACHABLE") {
		t.Errorf("an unpublished endpoint is not a reachability failure and must not be "+
			"reported as one, got:\n%s", got)
	}
	if strings.Contains(got, "RootlessNetworkCmd") {
		t.Errorf("the network-stack diagnosis belongs only to an actual reachability "+
			"failure; a missing file has nothing to do with forwarding, got:\n%s", got)
	}
}

// TestReachabilityProbeReportsEveryBrokenService, in a stable order. One warning
// per service, because a jail that lost its whole transport should say so once per
// thing the agent is about to reach for — and Go's map iteration order is random,
// so an unsorted probe would reorder boot output run to run and make two identical
// boots look different.
func TestReachabilityProbeReportsEveryBrokenService(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)
	broker := deadEndpoint(t, dir, "claude-oauth-broker")
	hostProcs := deadEndpoint(t, dir, "host-processes")

	vars := map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "CLAUDE_OAUTH_BROKER" + paths.ServiceEnvVarSuffix: broker,
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix:      hostProcs,
	}
	got := probeWarnings(t, vars)
	brokerAt := strings.Index(got, "claude-oauth-broker")
	procsAt := strings.Index(got, "host-processes")
	if brokerAt < 0 || procsAt < 0 {
		t.Fatalf("both broken services must be reported, got:\n%s", got)
	}
	if brokerAt > procsAt {
		t.Errorf("warnings must be ordered by service name so two identical boots read "+
			"identically, got:\n%s", got)
	}

	// The shared diagnosis appears ONCE, however many services broke. On a host
	// with the forwarding bug every jail-facing service fails at the same instant
	// for the same reason, so repeating the paragraph per service is how the
	// finding gets skimmed past.
	if n := strings.Count(got, "RootlessNetworkCmd"); n != 1 {
		t.Errorf("the shared explanation must be printed exactly once, saw it %d times:\n%s", n, got)
	}

	// Same matrix, again: the order must not depend on which way the map iterated.
	for i := 0; i < 5; i++ {
		if again := probeWarnings(t, vars); again != got {
			t.Fatalf("probe output is not deterministic across runs:\nfirst:\n%s\nrun %d:\n%s",
				got, i, again)
		}
	}
}

// TestReachabilityProbeAlwaysDialsAtLeastOnce. An exhausted budget must not turn
// into a silent pass: a probe that returns "reachable" without having touched the
// service is indistinguishable from a healthy jail, so the failure would never be
// noticed — and once OQ-R2 makes this fatal, a silent pass is the only bug class
// that cannot be caught by watching for false alarms.
func TestReachabilityProbeAlwaysDialsAtLeastOnce(t *testing.T) {
	shrinkReachabilityBudget(t)
	reachabilityBudget = 0 // the deadline is already gone before the first attempt
	dir := servicesDir(t)
	path := deadEndpoint(t, dir, "host-processes")

	got := probeWarnings(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: path,
	})
	if !strings.Contains(got, "UNREACHABLE") {
		t.Errorf("an exhausted budget skipped the dial and reported nothing — silence here "+
			"is indistinguishable from a healthy jail. got:\n%s", got)
	}
}

// TestReachabilityProbeStaysWithinItsBudget. The budget is the thing that makes
// the eventual fatal survivable, so it must actually bound the probe — including
// when nothing answers and the retry loop is the only thing stopping it. A blocked
// probe is a jail that never starts.
func TestReachabilityProbeStaysWithinItsBudget(t *testing.T) {
	shrinkReachabilityBudget(t)
	reachabilityBudget = 750 * time.Millisecond
	reachabilityDialTimeout = 750 * time.Millisecond
	reachabilityRetries = 1000 // as if it would retry forever

	// A listener that accepts and then says nothing: the TLS handshake hangs, so
	// only the dial timeout can end this. That is the blackhole shape — the one
	// case where the anti-starvation ceiling is genuinely spent.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold it open; never handshake.
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	dir := servicesDir(t)
	path := filepath.Join(dir, "blackhole"+paths.ServiceEndpointExt)
	donor := deadEndpoint(t, dir, "donor")
	ep, err := svcendpoint.Read(donor)
	if err != nil {
		t.Fatal(err)
	}
	ep.HostPort = net.JoinHostPort("127.0.0.1", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	if err := svcendpoint.Publish(path, ep); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	got := probeWarnings(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "BLACKHOLE" + paths.ServiceEnvVarSuffix: path,
	})
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Errorf("the probe must be bounded by its budget (%s); it took %s",
			reachabilityBudget, elapsed)
	}
	if !strings.Contains(got, "blackhole") {
		t.Errorf("a service that never completes its handshake is unreachable and must be "+
			"reported, got:\n%s", got)
	}
}
