package entrypoint

// reachabilitywarning_test.go pins two properties of what the witness SAYS that the
// suite next door holds only by accident.
//
// Both were found by mutating reachability.go and watching a green run: the probe
// stopped reading the advertised address at all, and the verdict lost its singular
// form, and every existing assertion still passed. A warning is the entire product
// of a witness that is silent when healthy, so an assertion on it that passes for
// the wrong reason is the same as having none.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestReachabilityWarningNamesTheAddressOnAClassTheDialErrorCannot closes a hole in
// TestReachabilityProbeNamesServiceAndAddress, which asserts the advertised address
// appears in the warning for an UNREACHABLE service — a class where Go's own dial
// error already carries it ("dial tcp 127.0.0.1:33689: connect: connection refused",
// which the warning quotes verbatim). MEASURED 2026-08-18: with probeService's
// svcendpoint.Read deleted so that addr stayed empty, that test still passed; the
// line it was checking had degraded to "Dialing its advertised address failed: …"
// and the address it matched was the stdlib's.
//
// The REJECTED class has no such accident to lean on. svcendpoint's auth errors are
// "the listener closed without an accept ack" and "unexpected accept byte" (token.go)
// — no address in either — so the only way "at <host:port>" can appear in this
// warning is the field probeService fills by reading the endpoint file. That makes
// this the one fault class that actually holds the property, and it is also the class
// that most needs it: a stale-credential finding is fixed by relaunching the jail
// that holds the file, and identifying WHICH listener answered is how a reader with
// several loopholes knows which one restarted.
//
// The disposition is `unsupported` so the launch is not refused. Severity is not what
// is under test here, and a refusal would add the override paragraph to the output
// for no reason.
func TestReachabilityWarningNamesTheAddressOnAClassTheDialErrorCannot(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)

	// A live listener, and a second file naming it with the wrong credential: the
	// shape of a daemon that restarted and republished under a jail still holding
	// the previous publication.
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

	got := probeWarnings(t, map[string]string{
		"JAIL_HOME":              t.TempDir(),
		paths.HostLoopbackEnvVar: paths.HostLoopbackUnsupported,
		paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix: stale,
	})

	// The rendered phrase, not a bare substring of the address: "at <addr> rejected"
	// can only be produced by reachabilityWarning's target, which is the addr field.
	if want := "at " + ep.HostPort + " rejected"; !strings.Contains(got, want) {
		t.Errorf("the warning must name the listener that refused this jail (%q); without it a "+
			"reader with several loopholes cannot tell which daemon restarted. got:\n%s", want, got)
	}
	// And the fallback wording must not be what is standing in for it. It exists for
	// the genuinely address-less case (an endpoint file that was never published),
	// and reaching it here means the address was available and was not read.
	if strings.Contains(got, "its advertised address") {
		t.Errorf("the address was published and the probe read it back — the no-address "+
			"fallback here means probeService stopped filling it in. got:\n%s", got)
	}
}

// TestReachabilityVerdictAgreesWithHowManyServicesItNames pins serviceListPhrase's
// two arms. The verdict is assembled around it — "…and <phrase> unusable from inside
// it" — so the phrase carries both the count and the verb, and getting it wrong
// produces "1 services (claude-oauth-broker) are unusable" on the single-service
// case, which is the common one: a jail with the broker enabled and nothing else.
//
// Every other test in this package asserts that the NAMES appear, which the plural
// arm satisfies for one service as happily as for two — measured, with the singular
// arm disabled. That is a small defect with an outsized reach: this sentence is the
// first thing a refused launch prints, and it is the sentence a user quotes when
// they report the refusal.
func TestReachabilityVerdictAgreesWithHowManyServicesItNames(t *testing.T) {
	shrinkReachabilityBudget(t)

	t.Run("one service", func(t *testing.T) {
		dir := servicesDir(t)
		got, err := runProbe(t, map[string]string{
			"JAIL_HOME":              t.TempDir(),
			paths.HostLoopbackEnvVar: paths.HostLoopbackRequested,
			paths.ServiceEnvVarPrefix + "CLAUDE_OAUTH_BROKER" + paths.ServiceEnvVarSuffix: deadEndpoint(t, dir, "claude-oauth-broker"),
		})
		if err == nil {
			t.Fatal("fixture drift: a requested-and-unreachable service must refuse the launch")
		}
		if want := "service 'claude-oauth-broker' is unusable"; !strings.Contains(got, want) {
			t.Errorf("the verdict must read as one service (%q), got:\n%s", want, got)
		}
	})

	t.Run("two services", func(t *testing.T) {
		dir := servicesDir(t)
		got, err := runProbe(t, map[string]string{
			"JAIL_HOME":              t.TempDir(),
			paths.HostLoopbackEnvVar: paths.HostLoopbackRequested,
			paths.ServiceEnvVarPrefix + "CLAUDE_OAUTH_BROKER" + paths.ServiceEnvVarSuffix: deadEndpoint(t, dir, "claude-oauth-broker"),
			paths.ServiceEnvVarPrefix + "HOST_PROCESSES" + paths.ServiceEnvVarSuffix:      deadEndpoint(t, dir, "host-processes"),
		})
		if err == nil {
			t.Fatal("fixture drift: two unreachable services must refuse the launch")
		}
		// The count and the list, in the order the probe sorts them.
		if want := "2 services (claude-oauth-broker, host-processes) are unusable"; !strings.Contains(got, want) {
			t.Errorf("the verdict must count and list what it names (%q), got:\n%s", want, got)
		}
	})
}
