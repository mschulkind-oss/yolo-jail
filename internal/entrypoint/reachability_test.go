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
	"syscall"
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

// TestReachabilityProbeAttributesAStaleToken is the third fault class, and the one
// with a launch riding on it once OQ-R2 flips.
//
// A listener that restarts republishes a new token; a jail holding the previous file
// is REACHABLE and refused. That is a stale file with a one-line fix (relaunch), and
// it has nothing to do with what the launcher decided about forwarding — which is
// exactly why the escalation set is the unreachable class alone. Fold this into it,
// as the default arm of classifyReachability silently would, and two things break at
// once: the reader is sent after a network stack that is working, and a fatal witness
// refuses to start a jail over a stale file.
//
// Run with the fatal ALREADY ON, because "must never fail a launch" is not a claim a
// warn-mode run can make.
func TestReachabilityProbeAttributesAStaleToken(t *testing.T) {
	shrinkReachabilityBudget(t)
	withReachabilityFatal(t)
	dir := servicesDir(t)

	// A live listener, and a SECOND file naming it with the wrong credential — the
	// shape of a daemon that restarted and republished under a jail holding the
	// previous publication.
	live := liveEndpoint(t, dir, "claude-oauth-broker")
	ep, err := svcendpoint.Read(live)
	if err != nil {
		t.Fatal(err)
	}
	ep.Token = strings.Repeat("a", len(ep.Token))
	stale := filepath.Join(dir, "host-processes"+paths.ServiceEndpointExt)
	if err := svcendpoint.Publish(stale, ep); err != nil {
		t.Fatal(err)
	}

	got, err := runProbe(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		// The launcher DID request forwarding: the one disposition that escalates,
		// so nothing but the fault classification is keeping this launch alive.
		paths.HostLoopbackEnvVar: paths.HostLoopbackRequested,
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: stale,
	})
	if err != nil {
		t.Errorf("a stale token is not a reachability failure and must never abort a boot; "+
			"genFailuresError: %v", err)
	}
	if !strings.Contains(got, "rejected this jail's token") {
		t.Errorf("the warning must name the fault the user can actually fix, got:\n%s", got)
	}
	if strings.Contains(got, "UNREACHABLE") {
		t.Errorf("the address answered — reporting this as unreachable sends the reader "+
			"after a network stack that is working. got:\n%s", got)
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

// TestReachabilityProbeNeverAbortsTheBoot is the WARN-MODE contract, asserted
// through the exact gate that would enforce the other one.
//
// Main calls ProbeServiceReachability immediately above genFailuresError, and
// reachability.go's own TODO names e.genFailure as where OQ-R2's flip plugs in.
// That proximity is deliberate and it is also the hazard: the difference between
// "a jail that warns" and "a jail that will not start" is one call, added to a
// function whose every other failure path is fatal by convention. So the guard is
// spelled against genFailuresError rather than against the slice — that is the
// value Main actually branches on, and a probe that learned to fail the boot would
// have to make it non-nil.
//
// The two services below cover BOTH fault classes the probe can produce from a
// real host (an address that does not answer, and an endpoint the host half never
// published), because a flip could plausibly be written to escalate only one.
func TestReachabilityProbeNeverAbortsTheBoot(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)
	dead := deadEndpoint(t, dir, "claude-oauth-broker")
	unpublished := filepath.Join(dir, "host-processes"+paths.ServiceEndpointExt)

	var out strings.Builder
	e := NewEnv(map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "CLAUDE_OAUTH_BROKER" + paths.ServiceEnvVarSuffix: dead,
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix:      unpublished,
	})
	e.Stderr = &out
	ProbeServiceReachability(e)

	// Guard the guard: if this ever stops warning, the assertions below pass for
	// the wrong reason and the test silently stops covering anything.
	if out.String() == "" {
		t.Fatal("the fixture is meant to be broken; the probe said nothing")
	}
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Errorf("the witness is in WARN mode (OQ-R2) and must record no generator failure, got: %v", fails)
	}
	if err := genFailuresError(e); err != nil {
		t.Errorf("an unreachable host service must not abort the boot while the witness is a "+
			"warning; genFailuresError returned: %v", err)
	}
}

// TestReachabilityProbeSurvivesAnUnwritableStderr. Every warning this file
// produces goes through e.warn, which no-ops on a nil Stderr — but a probe is the
// kind of code that grows a direct fmt.Fprintln, and the boot path constructs the
// Env before it assigns Stderr. A nil write here is a panic in PID 1's boot, which
// is a jail that does not start, reported as an entrypoint crash with no mention
// of networking.
func TestReachabilityProbeSurvivesAnUnwritableStderr(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)
	dead := deadEndpoint(t, dir, "journal")

	e := NewEnv(map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "JOURNAL" + paths.ServiceEnvVarSuffix: dead,
	})
	e.Stderr = nil // as Main has it until boot.go assigns os.Stderr
	ProbeServiceReachability(e)
}

// TestReachabilityProbeSurvivesAMalformedEndpointFile. Each service is dialled on
// its OWN GOROUTINE, and a panic in a goroutine is not recoverable by the caller:
// it takes down the entrypoint process, which is a jail that never starts. Nothing
// in the boot wraps this call. So every shape the probe can be handed by a
// half-written, truncated or squatted endpoint path has to end in a warning.
//
// These are not hypothetical shapes. The endpoint file is published by a rename,
// but the PATH comes from a launcher-set environment variable that a `jail_endpoint`
// override in a third-party loophole manifest can point anywhere, including at a
// directory or at a file the host half is still writing.
func TestReachabilityProbeSurvivesAMalformedEndpointFile(t *testing.T) {
	shrinkReachabilityBudget(t)

	cases := []struct {
		name  string
		write func(t *testing.T, path string)
	}{
		{"empty file", func(t *testing.T, p string) { writeEndpointRaw(t, p, "") }},
		{"one field", func(t *testing.T, p string) { writeEndpointRaw(t, p, "127.0.0.1:1\n") }},
		{"cert is not base64", func(t *testing.T, p string) {
			writeEndpointRaw(t, p, "127.0.0.1:1 not-base64!!! "+strings.Repeat("a", 64)+"\n")
		}},
		{"cert is base64 but not a certificate", func(t *testing.T, p string) {
			writeEndpointRaw(t, p, "127.0.0.1:1 aGVsbG8= "+strings.Repeat("a", 64)+"\n")
		}},
		{"address does not split", func(t *testing.T, p string) {
			writeEndpointRaw(t, p, "not-a-host-port aGVsbG8= "+strings.Repeat("a", 64)+"\n")
		}},
		{"a directory sits where the endpoint should be", func(t *testing.T, p string) {
			if err := os.MkdirAll(p, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := servicesDir(t)
			path := filepath.Join(dir, "host-processes"+paths.ServiceEndpointExt)
			tc.write(t, path)

			got := probeWarnings(t, map[string]string{
				"JAIL_HOME": t.TempDir(),
				paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: path,
			})
			if got == "" {
				t.Fatal("a service the probe cannot even read must still be reported")
			}
			if !strings.Contains(got, "host-processes") {
				t.Errorf("the warning must name the service, got:\n%s", got)
			}
		})
	}
}

// TestReachabilityProbeNeverOpensANonRegularEndpoint is the shape the malformed-file
// table above cannot express, because it does not end in a warning — it does not end.
//
// os.ReadFile (svcendpoint.Read, and Dial through it) OPENS the path, and opening a
// fifo with no writer blocks forever. No timeout in this file can reach that: the dial
// timeout is never entered, and reachabilityBudget only bounds the retry loop. The
// result is PID 1 wedged in the boot path with nothing printed — a jail that never
// starts, which is strictly worse than the launch failure OQ-R2 is still debating.
//
// It is reachable without an attacker: /run/yolo-services is bind-mounted READ-WRITE,
// its per-jail directory is keyed on the container name and so is the same directory
// every launch, and the endpoint variable is wired on the LOOPHOLE being active rather
// than on the daemon having published. One mkfifo in a jail therefore poisons every
// later boot of that jail.
//
// The assertion is time-bounded on purpose. A regression here HANGS rather than fails,
// and a hung test is reported as a package-wide timeout minutes later with no name
// attached to it — so the probe runs on its own goroutine and the deadline is the
// failure.
func TestReachabilityProbeNeverOpensANonRegularEndpoint(t *testing.T) {
	shrinkReachabilityBudget(t)

	cases := []struct {
		name string
		make func(t *testing.T, path string)
		want string // a word the warning must carry, so the reader knows what is there
	}{
		{
			name: "a fifo nobody is writing to",
			make: func(t *testing.T, p string) {
				if err := syscall.Mkfifo(p, 0o600); err != nil {
					t.Skipf("mkfifo unavailable here: %v", err)
				}
			},
			want: "named pipe",
		},
		{
			name: "a directory",
			make: func(t *testing.T, p string) {
				if err := os.MkdirAll(p, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "directory",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := servicesDir(t)
			path := filepath.Join(dir, "host-processes"+paths.ServiceEndpointExt)
			tc.make(t, path)

			vars := map[string]string{
				"JAIL_HOME": t.TempDir(),
				paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: path,
			}
			done := make(chan string, 1)
			go func() { done <- probeWarnings(t, vars) }()

			var got string
			select {
			case got = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the probe never returned — it opened the path instead of stat'ing it first. " +
					"This is not a slow probe; a writer-less fifo blocks in open(2) forever, which " +
					"is PID 1 wedged in the boot with nothing printed.")
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("the warning must name what is actually at the path (%q), got:\n%s", tc.want, got)
			}
			// And it must not be attributed to the network. Nothing about a local file
			// shape is a transport failure, and faultUnreachable is the ONE class OQ-R2's
			// fatal escalates — a directory used to land there, via
			// classifyReachability's transport default over os.ReadFile's EISDIR.
			if strings.Contains(got, "UNREACHABLE") {
				t.Errorf("a %s at the endpoint path is not a reachability failure and must never "+
					"reach the escalation set, got:\n%s", tc.want, got)
			}
		})
	}
}

// TestReachabilityProbeRefusesAnEnormousEndpointFile is the same argument one step
// along: os.ReadFile has no ceiling, an endpoint is three fields under 2 KiB, and
// slurping a file something in that read-write directory grew without bound is an OOM
// in PID 1 rather than a warning.
func TestReachabilityProbeRefusesAnEnormousEndpointFile(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)
	path := filepath.Join(dir, "host-processes"+paths.ServiceEndpointExt)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: the test cares about the declared SIZE, which is what the gate reads.
	if err := f.Truncate(maxEndpointFileSize + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got := probeWarnings(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: path,
	})
	if !strings.Contains(got, "host-processes") {
		t.Errorf("the warning must name the service, got:\n%s", got)
	}
	if strings.Contains(got, "UNREACHABLE") {
		t.Errorf("an oversized file is not a reachability failure, got:\n%s", got)
	}
}

// writeEndpointRaw drops arbitrary bytes at path, bypassing svcendpoint.Publish —
// which is the point: Publish only ever writes well-formed files, and the shapes
// under test are the ones a reader can be handed anyway.
func writeEndpointRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReachabilityProbeIgnoresLookalikeVariables. "A jail with no loopholes prints
// nothing" is only true if the ENDPOINT filter is exact, and the environment a
// jail boots with is not a curated list — env_sources hydration and the user's own
// shell put arbitrary names in it. Every variable below names a path that does not
// exist, so anything the filter lets through warns loudly and the silence is proof
// rather than an artefact of an empty matrix.
func TestReachabilityProbeIgnoresLookalikeVariables(t *testing.T) {
	shrinkReachabilityBudget(t)
	const absent = "/nonexistent/yolo-services/whatever.endpoint"

	got := probeWarnings(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		// The retiring AF_UNIX spelling — a bind-mounted socket with no
		// forwarding hop to get wrong.
		paths.ServiceEnvVarPrefix + "CGROUP_DELEGATE_SOCKET": absent,
		// Right prefix, no suffix at all.
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES": absent,
		// Right suffix, wrong prefix.
		"MY_APP" + paths.ServiceEnvVarSuffix: absent,
		// Both affixes, but EMPTY — the launcher never wires a service without an
		// address, so an empty value is somebody else's variable, not a service
		// this jail is expected to reach.
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: "",
		// Ordinary jail environment, for good measure.
		"YOLO_VERSION": "9.9.9-test",
		"PATH":         "/bin",
	})
	if got != "" {
		t.Errorf("only YOLO_SERVICE_<NAME>_ENDPOINT with a value names a jail-facing service; "+
			"the probe spoke about something else:\n%s", got)
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

// --- the two prerequisites of OQ-R2's flip: the scoping, and the escape hatch ---

// withReachabilityFatal runs the rest of the test in the mode OQ-R2 rules for the
// end state, and restores warn mode afterwards.
//
// This is the reason the mode is a variable rather than an unwritten branch. The
// fatal path is the one that costs a jail when it is wrong, so it has to be
// exercised BEFORE it is turned on — a branch first executed on a user's broken
// host is a branch nobody has ever seen work.
func withReachabilityFatal(t *testing.T) {
	t.Helper()
	prev := reachabilityFatal
	t.Cleanup(func() { reachabilityFatal = prev })
	reachabilityFatal = true
}

// runProbe drives the probe over one env matrix and hands back both of its
// outputs: what it said, and whether it recorded a failure that would abort the
// boot. The second is the one the flip is about, and it is read through
// genFailuresError — the value Main actually branches on.
func runProbe(t *testing.T, vars map[string]string) (string, error) {
	t.Helper()
	var out strings.Builder
	e := NewEnv(vars)
	e.Stderr = &out
	ProbeServiceReachability(e)
	return out.String(), genFailuresError(e)
}

// brokenServiceVars is a jail with one enabled jail-facing service whose
// advertised address answers nothing — the shape of the real outage — plus
// whatever the launcher told this jail about host-loopback forwarding.
func brokenServiceVars(t *testing.T, extra map[string]string) map[string]string {
	t.Helper()
	vars := map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "CLAUDE_OAUTH_BROKER" + paths.ServiceEnvVarSuffix: deadEndpoint(t, servicesDir(t), "claude-oauth-broker"),
	}
	for k, v := range extra {
		vars[k] = v
	}
	return vars
}

// TestReachabilityProbeScopesAnUnsupportedHostAsALimitation is the substantive
// half of "unsupported is not broken" (OQ-R3, which re-ruled refusal out the same
// day it ruled it in).
//
// On a host where yolo could not get the network stack to forward the host's
// loopback, an unreachable service is a KNOWN LIMITATION: the user has done nothing
// wrong, launching is the correct outcome, and no future severity may change that.
// The test runs with the fatal ALREADY ON, because that is the only way to assert
// the second half — a rule that only holds while nothing is at stake is not the
// rule OQ-R3 needs. Without this scoping the two rulings collide and an old-passt
// host cannot launch a jail at all, which is precisely the refusal that was
// rejected.
func TestReachabilityProbeScopesAnUnsupportedHostAsALimitation(t *testing.T) {
	shrinkReachabilityBudget(t)
	withReachabilityFatal(t)

	got, err := runProbe(t, brokenServiceVars(t, map[string]string{
		paths.HostLoopbackEnvVar: paths.HostLoopbackUnsupported,
	}))
	if err != nil {
		t.Errorf("a host yolo could not ask must never fail a launch — that is OQ-R3's "+
			"refusal arriving by the back door. genFailuresError: %v", err)
	}
	if !strings.Contains(got, "claude-oauth-broker") {
		t.Errorf("the service must still be named, got:\n%s", got)
	}
	if !strings.Contains(got, "KNOWN LIMITATION") {
		t.Errorf("an unsupported host must be reported as a limitation, not as a fault, got:\n%s", got)
	}
	if strings.Contains(got, "FAULT") {
		t.Errorf("this host did nothing wrong; calling it a fault sends the reader after the "+
			"wrong thing entirely. got:\n%s", got)
	}
	if strings.Contains(got, paths.AllowUnreachableServicesEnv) {
		t.Errorf("nothing was suppressed and nothing was at risk, so the escape hatch has no "+
			"business being mentioned, got:\n%s", got)
	}
}

// TestReachabilityProbeCallsRequestedForwardingAFault is the other side of the
// split. yolo asked this host's stack to forward loopback and the service is still
// unreachable, so the network option is the one thing already ruled out — saying so
// is what stops a reader spending an afternoon on pasta flags for a daemon that is
// simply not running. In warn mode it is still only a warning.
func TestReachabilityProbeCallsRequestedForwardingAFault(t *testing.T) {
	shrinkReachabilityBudget(t)

	got, err := runProbe(t, brokenServiceVars(t, map[string]string{
		paths.HostLoopbackEnvVar: paths.HostLoopbackRequested,
	}))
	if err != nil {
		t.Errorf("the witness is still in WARN mode and must not abort the boot: %v", err)
	}
	if !strings.Contains(got, "FAULT") {
		t.Errorf("a launch that requested forwarding and still cannot reach the service must be "+
			"attributed as a fault, got:\n%s", got)
	}
	if strings.Contains(got, "KNOWN LIMITATION") {
		t.Errorf("forwarding was requested, so this is not a host limitation, got:\n%s", got)
	}
	// Warn mode has nothing to escape, and a hatch advertised before it does
	// anything is one people set once and forget.
	if strings.Contains(got, paths.AllowUnreachableServicesEnv) {
		t.Errorf("the hatch must stay quiet while it is suppressing nothing, got:\n%s", got)
	}
}

// TestReachabilityProbeFatalModeFailsOnlyAFault runs the end state OQ-R2 ruled and
// asserts both halves of it at once: a fault aborts the boot, and the refusal names
// the way past itself.
//
// The escape hatch is not optional politeness here. The daemon the user has to fix
// is on the HOST, and the shell they would fix it from is in the jail that just
// refused to start; a fatal with no override is a tool that locks the door and
// posts the key inside.
func TestReachabilityProbeFatalModeFailsOnlyAFault(t *testing.T) {
	shrinkReachabilityBudget(t)
	withReachabilityFatal(t)

	got, err := runProbe(t, brokenServiceVars(t, map[string]string{
		paths.HostLoopbackEnvVar: paths.HostLoopbackRequested,
	}))
	if err == nil {
		t.Fatal("a requested-and-still-unreachable service is what OQ-R2's fatal is for; " +
			"genFailuresError returned nil")
	}
	if !strings.Contains(err.Error(), "claude-oauth-broker") {
		t.Errorf("the abort must name the service that caused it, got: %v", err)
	}
	if !strings.Contains(got, paths.AllowUnreachableServicesEnv+"=1") {
		t.Errorf("a refusal must name the override that gets past it, or the user cannot open a "+
			"shell to fix the daemon that is failing. got:\n%s", got)
	}
}

// TestReachabilityProbeEscapeHatchKeepsTheJail: the hatch, honoured. It mirrors
// YOLO_ALLOW_STALE_IMAGE in both directions — the launch proceeds, AND the output
// says what is being suppressed rather than going quiet, including the part that
// matters most: nothing was repaired.
func TestReachabilityProbeEscapeHatchKeepsTheJail(t *testing.T) {
	shrinkReachabilityBudget(t)
	withReachabilityFatal(t)

	got, err := runProbe(t, brokenServiceVars(t, map[string]string{
		paths.HostLoopbackEnvVar:          paths.HostLoopbackRequested,
		paths.AllowUnreachableServicesEnv: "1",
	}))
	if err != nil {
		t.Errorf("%s must keep the jail launching: %v", paths.AllowUnreachableServicesEnv, err)
	}
	if !strings.Contains(got, paths.AllowUnreachableServicesEnv) {
		t.Errorf("a hatch that suppresses a launch failure silently is indistinguishable from a "+
			"jail with no problem, got:\n%s", got)
	}
	if !strings.Contains(got, "CONTINUING") {
		t.Errorf("the override must state that it is continuing anyway, got:\n%s", got)
	}
	if !strings.Contains(got, "claude-oauth-broker") {
		t.Errorf("the override must name what it is continuing past, got:\n%s", got)
	}
}

// TestReachabilityProbeHatchStaysQuietInWarnMode is the hatch's OTHER contract, and
// the one a green suite was not holding: it may only speak where it is actually
// saving a launch.
//
// The sibling tests assert that the hatch is not mentioned — but they never SET it,
// so they hold whatever the code does with it, and the `reachabilityFatal &&` guard
// in reportUnreachableFault could be deleted with every test still green. This sets
// it, in the shipped (warn) mode, where it is suppressing nothing.
//
// What goes wrong without the guard is not cosmetic. The override notice REPLACES the
// finding: a user who set the variable once — on the host, in front of `yolo`, from
// where it is forwarded into every jail — would get "CONTINUING with … unreachable"
// on a launch that was never at risk, and would lose the FAULT attribution that says
// the forwarding is already ruled out. That is the same rule
// internal/cli/run/hostloopback.go's own opt-out follows, and it is pinned there.
func TestReachabilityProbeHatchStaysQuietInWarnMode(t *testing.T) {
	shrinkReachabilityBudget(t)
	// Deliberately NOT withReachabilityFatal: warn mode is what ships today.

	got, err := runProbe(t, brokenServiceVars(t, map[string]string{
		paths.HostLoopbackEnvVar:          paths.HostLoopbackRequested,
		paths.AllowUnreachableServicesEnv: "1",
	}))
	if err != nil {
		t.Errorf("the witness is in warn mode; nothing may abort the boot: %v", err)
	}
	if !strings.Contains(got, "FAULT") {
		t.Errorf("the hatch suppressed a finding it was not saving anything from — the "+
			"fault attribution is the whole content of this warning. got:\n%s", got)
	}
	if strings.Contains(got, "CONTINUING") {
		t.Errorf("nothing was continued past: the launch was never at risk in warn mode, "+
			"and an override that announces itself on launches it is not saving is one "+
			"people stop reading. got:\n%s", got)
	}
	if strings.Contains(got, paths.AllowUnreachableServicesEnv) {
		t.Errorf("the hatch must stay quiet while it is suppressing nothing, got:\n%s", got)
	}
}

// TestReachabilityProbeFatalModeStaysSilentOnAHealthyJail. The single most
// important property of the flip: a working jail must be untouched by it. A fatal
// that fires on a reachable service is not a strict check, it is a product that
// will not start.
func TestReachabilityProbeFatalModeStaysSilentOnAHealthyJail(t *testing.T) {
	shrinkReachabilityBudget(t)
	withReachabilityFatal(t)
	dir := servicesDir(t)

	got, err := runProbe(t, map[string]string{
		"JAIL_HOME":              t.TempDir(),
		paths.HostLoopbackEnvVar: paths.HostLoopbackRequested,
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: liveEndpoint(t, dir, "host-processes"),
	})
	if err != nil {
		t.Errorf("a reachable service must not fail the launch, even in fatal mode: %v", err)
	}
	if got != "" {
		t.Errorf("silence is the healthy output in every mode, got:\n%s", got)
	}
}

// TestReachabilityWitnessShipsInWarnMode pins the ONE value whose flip costs a jail.
//
// reachability.go names exactly what is still owed before it may become true — an
// observation at a real boot on a healthy host, which has not happened — and the file
// is written so that the flip is a one-character edit. That is precisely why it needs
// a guard: the change that turns every unreachable-service launch into a refusal is
// indistinguishable, in a diff, from a typo, and nothing else in the tree would fail.
//
// This test is not asserting that warn mode is right forever. It is asserting that the
// flip is a DELIBERATE act: whoever flips it deletes this test and writes down that the
// observation happened.
func TestReachabilityWitnessShipsInWarnMode(t *testing.T) {
	if reachabilityFatal {
		t.Fatal("reachabilityFatal is true. OQ-R2's flip is gated on one thing that is not " +
			"code: this probe has never been observed at a real boot on a healthy host. " +
			"Until it has, a false positive here costs a jail rather than a log line — see " +
			"the WARN MODE section of reachability.go.")
	}
}

// TestReachabilityProbeInShippedModeCannotAbortAnyBoot is the launch-safety property
// stated as a sweep rather than as three examples: in the mode that SHIPS, no
// combination of what the launcher said, what shape the endpoint is in, and whether
// the escape hatch is set may produce an error out of genFailuresError.
//
// genFailuresError is the value Main branches on, so it is the only honest subject.
// The disposition axis includes a value this binary does not know, because the image
// and the launcher version independently (AGENTS.md: the baked binaries are frozen at
// the last host `just load`) and an unrecognised spelling must never be read as
// permission to fail a launch. The hatch axis includes "0" and "false", because the
// hatch is a "any non-empty value" switch and a reader who assumes otherwise would be
// wrong in the direction that keeps a jail down.
func TestReachabilityProbeInShippedModeCannotAbortAnyBoot(t *testing.T) {
	shrinkReachabilityBudget(t)
	// Deliberately NOT withReachabilityFatal: the subject is the shipped mode.

	shapes := map[string]func(t *testing.T, dir string) string{
		"an address that answers nothing": func(t *testing.T, dir string) string {
			return deadEndpoint(t, dir, "claude-oauth-broker")
		},
		"an endpoint the host never published": func(t *testing.T, dir string) string {
			return filepath.Join(dir, "claude-oauth-broker"+paths.ServiceEndpointExt)
		},
		"a truncated endpoint file": func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "claude-oauth-broker"+paths.ServiceEndpointExt)
			writeEndpointRaw(t, p, "127.0.0.1:1\n")
			return p
		},
		"a directory where the endpoint belongs": func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "claude-oauth-broker"+paths.ServiceEndpointExt)
			if err := os.MkdirAll(p, 0o700); err != nil {
				t.Fatal(err)
			}
			return p
		},
	}
	dispositions := []string{
		"",
		paths.HostLoopbackRequested,
		paths.HostLoopbackUnsupported,
		"a-spelling-from-a-newer-launcher",
	}
	hatches := []string{"", "1", "0", "false"}

	for shapeName, mkShape := range shapes {
		for _, disp := range dispositions {
			for _, hatch := range hatches {
				name := shapeName + "/disposition=" + orNone(disp) + "/hatch=" + orNone(hatch)
				t.Run(name, func(t *testing.T) {
					vars := map[string]string{
						"JAIL_HOME": t.TempDir(),
						paths.ServiceEnvVarPrefix + "CLAUDE_OAUTH_BROKER" + paths.ServiceEnvVarSuffix: mkShape(t, servicesDir(t)),
					}
					if disp != "" {
						vars[paths.HostLoopbackEnvVar] = disp
					}
					if hatch != "" {
						vars[paths.AllowUnreachableServicesEnv] = hatch
					}
					got, err := runProbe(t, vars)
					if err != nil {
						t.Errorf("the shipped witness is a WARNING and may not abort a boot for "+
							"any input: %v", err)
					}
					// Guard the guard: a sweep whose fixtures stopped being broken would
					// pass for the wrong reason and cover nothing.
					if got == "" {
						t.Error("the fixture is meant to be broken; the probe said nothing")
					}
				})
			}
		}
	}
}

// orNone renders an empty axis value as something a subtest name can carry.
func orNone(s string) string {
	if s == "" {
		return "absent"
	}
	return s
}

// TestReachabilityProbeNeverEscalatesWhatItCannotAttribute sweeps the values that
// mean "the launcher said nothing definite": absent (an explicit network.mode, the
// YOLO_NO_HOST_LOOPBACK opt-out, a rootful or unrecognised runtime, Apple
// Container, a nested jail — and any launcher older than the variable), empty, and
// a spelling from some future launcher this binary does not know.
//
// The last one is the one worth having a test for. The image and the launcher
// version independently (AGENTS.md: the baked binaries are frozen at the last host
// `just load`), so an unrecognised value is a real state, and reading one as
// permission to fail a launch would turn a version skew into a jail nobody can
// start.
func TestReachabilityProbeNeverEscalatesWhatItCannotAttribute(t *testing.T) {
	shrinkReachabilityBudget(t)
	withReachabilityFatal(t)

	cases := map[string]map[string]string{
		"absent":                 {},
		"empty":                  {paths.HostLoopbackEnvVar: ""},
		"a value we do not know": {paths.HostLoopbackEnvVar: "requested-via-some-future-mechanism"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := runProbe(t, brokenServiceVars(t, extra))
			if err != nil {
				t.Errorf("an unattributable failure must never abort the boot: %v", err)
			}
			if !strings.Contains(got, "UNREACHABLE") {
				t.Errorf("it must still be reported, just not escalated, got:\n%s", got)
			}
		})
	}
}

// runProbeBothSinks runs the witness with the terminal and the log-only sink kept
// SEPARATE, which is the whole point of Env.LogOnly: a healthy launch must stay
// silent for the person watching it while still leaving evidence for the person
// reading the log afterwards.
func runProbeBothSinks(t *testing.T, vars map[string]string) (term, log string, err error) {
	t.Helper()
	var termBuf, logBuf strings.Builder
	e := NewEnv(vars)
	e.Stderr = &termBuf
	e.LogOnly = &logBuf
	ProbeServiceReachability(e)
	return termBuf.String(), logBuf.String(), genFailuresError(e)
}

// A witness that says nothing when healthy is indistinguishable from a witness that
// never ran — and once the flip lands, "no complaint" IS the evidence the launch was
// allowed on. So the healthy boot has to leave a positive record, in the log only.
//
// Observed 2026-08-18 at a real boot: the entrypoint's log carried the config-render
// notices and the cgroup line and NOTHING from this witness, which was correct and
// unreadable. Establishing that it had run took the perf log and a grep.
func TestReachabilityProbeRecordsThatItRanOnAHealthyJail(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)

	term, log, err := runProbeBothSinks(t, map[string]string{
		"JAIL_HOME":              t.TempDir(),
		paths.HostLoopbackEnvVar: paths.HostLoopbackRequested,
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: liveEndpoint(t, dir, "host-processes"),
	})
	if err != nil {
		t.Fatalf("a reachable service must not fail the launch: %v", err)
	}

	// The terminal is unchanged: silence is still the healthy output there.
	if term != "" {
		t.Errorf("a healthy jail must stay silent on the TERMINAL, got:\n%s", term)
	}
	// The log is not.
	for _, want := range []string{"reachability:", "1/1", "requested"} {
		if !strings.Contains(log, want) {
			t.Errorf("the boot log must record that the witness ran; missing %q\n--- log ---\n%s", want, log)
		}
	}
}

// The record has to be TRUE, not merely present: a count that cannot distinguish a
// reachable service from a broken one is worse than no count, because it reads as
// confirmation.
func TestReachabilityRecordCountsOnlyWhatItReached(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)

	_, log, _ := runProbeBothSinks(t, map[string]string{
		"JAIL_HOME":              t.TempDir(),
		paths.HostLoopbackEnvVar: paths.HostLoopbackUnsupported,
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix:      liveEndpoint(t, dir, "host-processes"),
		paths.ServiceEnvVarPrefix + "CLAUDE_OAUTH_BROKER" + paths.ServiceEnvVarSuffix: deadEndpoint(t, dir, "claude-oauth-broker"),
	})

	if !strings.Contains(log, "1/2") {
		t.Errorf("one of two services was unreachable; the log must say so\n--- log ---\n%s", log)
	}
	if !strings.Contains(log, "unsupported") {
		t.Errorf("the record must carry the disposition that governs severity\n--- log ---\n%s", log)
	}
}

// A nil LogOnly is every caller that is not a real boot, including every other test
// in this file. It must not panic and must not leak the record onto the terminal.
func TestReachabilityRecordIsSilentWithNoLog(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)

	got, err := runProbe(t, map[string]string{
		"JAIL_HOME": t.TempDir(),
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: liveEndpoint(t, dir, "host-processes"),
	})
	if err != nil {
		t.Fatalf("unexpected failure: %v", err)
	}
	if got != "" {
		t.Errorf("the log-only record must never reach Stderr, got:\n%s", got)
	}
}

// In a real boot both sinks are the SAME FILE, so their relative order is a real
// property that two separate test buffers cannot see. Observed 2026-08-18 in an
// actual boot log: the summary landed between a service's warning and the
// explanation of that warning, splitting the two things a reader holds together.
func TestReachabilityRecordLandsAfterTheFindingsItSummarises(t *testing.T) {
	shrinkReachabilityBudget(t)

	// One buffer for both sinks: this IS the boot log's shape.
	var both strings.Builder
	e := NewEnv(brokenServiceVars(t, map[string]string{
		paths.HostLoopbackEnvVar: paths.HostLoopbackRequested,
	}))
	e.Stderr = &both
	e.LogOnly = &both
	ProbeServiceReachability(e)

	out := both.String()
	summary := strings.Index(out, "reachability:")
	if summary < 0 {
		t.Fatalf("no summary recorded:\n%s", out)
	}
	// The explanation is the last thing the witness says about a broken service.
	explain := strings.LastIndex(out, "docs/design/loopback-tls-reachability.md")
	if explain < 0 {
		t.Fatalf("no explanation emitted for a broken service:\n%s", out)
	}
	if summary < explain {
		t.Errorf("the summary must not split a finding from its explanation\n"+
			"  summary at %d, explanation ends at %d\n--- output ---\n%s", summary, explain, out)
	}
}
