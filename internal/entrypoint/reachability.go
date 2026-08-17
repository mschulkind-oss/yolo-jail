package entrypoint

// reachability.go is the IN-JAIL witness for loopback-TLS reachability: at boot,
// from inside the jail, dial the ADVERTISED address of every jail-facing service
// this launch wired up, and say — by name, with the address — which ones cannot be
// reached.
//
// # Why this cannot live in `yolo check`
//
// `yolo check` already probes the same endpoints and reports PASS during a total
// outage, and that is structural rather than a bug in its wiring. It runs
// HOST-SIDE, so it dials with svcendpoint.DialLocal, which keeps the published
// PORT and substitutes 127.0.0.1 (dial.go) — the one address a jail cannot use.
// Everything is reachable on the host's loopback by construction, because that is
// where the daemons bind; a host-side prober therefore cannot fail for the reason
// this file exists to catch.
//
// The advertised host (`host.containers.internal`) is only MEANINGFUL from inside a
// network namespace the container runtime built, so the only honest place to
// evaluate it is here. See docs/design/loopback-tls-reachability.md §7.
//
// # What it is actually catching
//
// yolo's host daemons bind the host's loopback and advertise "wherever the host
// is", on the assumption that a rootless network stack forwards that name to the
// host's LOOPBACK. Whether it does is a property of WHICH STACK is in use: true for
// slirp4netns with allow_host_loopback, false for pasta, which podman has defaulted
// to since 5.0 and which forwards to the host's GLOBAL address instead. On such a
// host every loopback-TLS service is unreachable from every jail, and the symptom
// is `connect: connection refused` in whatever client the agent reaches for first,
// hours later, with no clue that the fault is the network stack. See
// docs/design/loopback-tls-reachability.md §2-§3 for the whole map.
//
// # WARN MODE — TODO(OQ-R2): make this FATAL
//
// docs/design/loopback-tls-reachability.md OQ-R2 ruled the end state: "an enabled
// jail-facing service that the jail cannot reach is a failed launch." This landed
// in WARN mode on purpose, because §10 sequences the witness BEFORE the fatal — a
// warning that misfires is noise, but a fatal that misfires costs a jail, and a
// user whose host daemon is merely slow to come up would be left unable to open a
// shell to fix it. The flip is a deliberate follow-up, not an oversight:
//
//   - route the failures into e.genFailure so genFailuresError aborts the boot
//     (Main already runs this immediately before that gate, for exactly this
//     reason);
//   - add the escape hatch OQ-R2's implementation note asks for, mirroring
//     YOLO_ALLOW_STALE_IMAGE — loud, documented, and naming what it suppresses;
//   - name the required passt version in the refusal (OQ-R3: "refuse" is only
//     acceptable if the user can act on it).
//
// Do not flip it until the probe has been observed quiet on a healthy host and
// loud on a broken one.
//
// # Linux only, deliberately
//
// RunDarwinBootstrap (darwin.go) does not call this. macos-user is a native
// sandbox with no network namespace and no pasta, so there is no forwarding hop to
// get wrong — docs/design/loopback-tls-reachability.md §8 puts macOS out of scope
// explicitly.

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// The probe's time budget, spelled as four numbers rather than one, because the
// two failures it has to tell apart have OPPOSITE timing signatures and a single
// number would have to be wrong for one of them.
//
// reachabilityDialTimeout is the ANTI-STARVATION ceiling, and it is the number the
// design doc's risk table is about: "a probe that is merely SLOW under load must
// not read as unreachable". It is set to the same 30s the journald readiness poll
// had to be raised to after 5s missed its budget roughly once in 85 runs on a
// loaded machine and reported a descheduled daemon as a dead one — a measured
// number from this repo beats a guessed one, and this probe is heading for FATAL
// (OQ-R2), where that misfire costs a jail. It costs nothing when things work: a
// dial that succeeds returns in milliseconds, and a REFUSED dial (the measured
// pasta failure — the packet reaches a host stack and gets an RST) returns
// immediately too. The ceiling is only ever spent when packets are being dropped,
// which is precisely the case that deserves patience.
//
// reachabilityRetries/reachabilityRetryDelay are a SEPARATE, deliberately short
// window, and the split is the point. Retrying is worth something only for a
// failure that can heal — a daemon caught mid-republish on a new port answers the
// next attempt — and ECONNREFUSED after the launcher's own host-side readiness gate
// already passed will not heal. Looping on the dial timeout instead would multiply
// the ceiling by the retry count, and worse: on a genuinely broken host, where the
// answer arrives instantly and never changes, it would add half a minute to EVERY
// boot to re-learn the same fact. So the whole probe finishes in about a second on
// the measured failure, and only a blackhole spends the ceiling.
//
// reachabilityBudget caps the total regardless of how many services are probed.
// Services are probed CONCURRENTLY so this stays a true ceiling rather than a
// per-service one that a jail with several loopholes multiplies.
var (
	reachabilityDialTimeout = 30 * time.Second
	reachabilityRetries     = 2
	reachabilityRetryDelay  = 500 * time.Millisecond
	reachabilityBudget      = 30 * time.Second
)

// serviceEndpoint is one jail-facing service this launch wired up: the name to put
// in a diagnostic, and the in-jail path of its published endpoint file.
//
// The path is NOT an address. The address lives inside the file so it can change
// without relaunching a jail whose environment is frozen at container start, which
// is also why this probe re-reads the file rather than trusting anything cached.
type serviceEndpoint struct {
	name string
	path string
}

// reachabilityFault classifies WHY a service could not be reached, because the
// three answers have three different fixes and collapsing them would send the user
// looking in the wrong place.
type reachabilityFault int

const (
	// faultUnpublished: the launcher wired the variable but no usable endpoint file
	// is there. The host half never published, or the per-jail directory was
	// removed under a running container.
	faultUnpublished reachabilityFault = iota
	// faultUnreachable: the file is good and the address in it does not answer.
	// THIS is the loopback-forwarding bug; everything else in this file is
	// scaffolding around making this one line appear.
	faultUnreachable
	// faultRejected: reachable, and the listener refused this jail's token — the
	// file is stale relative to the running listener.
	faultRejected
)

// reachabilityResult is one probed service's verdict. addr is the ADVERTISED
// host:port and is the only field of the endpoint that may ever be printed: the
// endpoint file also carries this jail's bearer token, and a diagnostic is not a
// place for it (svcendpoint's package comment states the rule; this honours it by
// never holding the Endpoint value beyond reading HostPort out of it).
type reachabilityResult struct {
	svc   serviceEndpoint
	fault reachabilityFault
	addr  string
	err   error
}

// ProbeServiceReachability dials every ENABLED jail-facing loopback-TLS service
// from inside the jail and warns about each one it cannot reach.
//
// SILENCE IS THE HEALTHY OUTPUT, and "enabled" is not a judgement this function
// makes: it reads the YOLO_SERVICE_<NAME>_ENDPOINT variables the launcher set on
// this container, one per service it deliberately wired up. A jail with no
// loopholes has none of them, so it prints nothing and dials nothing — no probing
// of things that merely exist, which is what makes the eventual fatal (OQ-R2)
// tolerable: under loophole-activation.md nothing is on unless it was asked for, so
// "enabled but unreachable" is a genuine contradiction rather than an unused
// service being noisy.
func ProbeServiceReachability(e *Env) {
	svcs := enabledServiceEndpoints(e)
	if len(svcs) == 0 {
		return
	}

	deadline := time.Now().Add(reachabilityBudget)
	results := make([]*reachabilityResult, len(svcs))
	var wg sync.WaitGroup
	for i, svc := range svcs {
		wg.Add(1)
		go func(i int, svc serviceEndpoint) {
			defer wg.Done()
			results[i] = probeService(svc, deadline)
		}(i, svc)
	}
	wg.Wait()

	// One line per broken service, then the shared explanation ONCE. Repeating a
	// paragraph about pasta's forwarding under every service is how a real finding
	// gets skimmed past: on a broken host EVERY jail-facing service fails for the
	// same reason at the same instant, so the diagnosis is a property of the launch
	// and not of any one of them.
	unreachable := false
	for _, res := range results {
		if res == nil {
			continue
		}
		if res.fault == faultUnreachable {
			unreachable = true
		}
		e.warn(reachabilityWarning(*res))
	}
	if unreachable {
		e.warn(reachabilityExplanation)
	}
}

// reachabilityExplanation is the diagnosis, printed once when anything came back
// unreachable. It names the mechanism rather than a remedy on purpose: the remedy
// is a launcher change (docs/design/loopback-tls-reachability.md §6) that does not
// exist yet, and telling a user to fix something they cannot fix is worse than
// telling them exactly what is broken.
const reachabilityExplanation = "" +
	"  Every in-jail client of those services will fail the same way.\n" +
	"  yolo's host daemons bind the host's LOOPBACK and advertise the container\n" +
	"  runtime's gateway name, which only works if the runtime forwards that name to\n" +
	"  the host's loopback. Whether it does is a property of the rootless network\n" +
	"  stack: true for slirp4netns with allow_host_loopback, FALSE for pasta —\n" +
	"  podman's default since 5.0 — which forwards to the host's global address\n" +
	"  instead. On the host, `podman info --format '{{.Host.RootlessNetworkCmd}}'`\n" +
	"  says which stack is in use.\n" +
	"  docs/design/loopback-tls-reachability.md is the whole map."

// enabledServiceEndpoints lists the loopback-TLS services this launch wired up, in
// a deterministic order (the results are warnings, and Go's map order is not
// stable, so an unsorted probe would reorder boot output run to run).
//
// It reads the ENDPOINT spelling only. The retiring YOLO_SERVICE_<NAME>_SOCKET
// variables (cgroup-delegate, today) name a bind-mounted AF_UNIX socket, which the
// container runtime's forwarding decisions cannot touch — there is no hop to get
// wrong, so there is nothing here to witness. That the two spellings are kept
// distinct is exactly what makes this filter possible; see hostServiceEnvVar vs
// hostServiceSocketEnvVar in internal/cli/run.
func enabledServiceEndpoints(e *Env) []serviceEndpoint {
	var out []serviceEndpoint
	for key, val := range e.Vars {
		if !strings.HasPrefix(key, paths.ServiceEnvVarPrefix) ||
			!strings.HasSuffix(key, paths.ServiceEnvVarSuffix) || val == "" {
			continue
		}
		out = append(out, serviceEndpoint{name: serviceNameFor(key, val), path: val})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// serviceNameFor recovers the service's real name for the diagnostic.
//
// The endpoint file is normally <name>.endpoint, so its base name IS the name,
// hyphens and all. A loophole may override the location with `jail_endpoint`,
// though, in which case the path says nothing — so the fallback is the env var,
// which is always generated from the service name even though the round trip is
// lossy (upper-cased, non-alphanumerics folded to underscores). A slightly wrong
// name in a warning beats an empty one.
func serviceNameFor(envKey, endpointPath string) string {
	base := filepath.Base(endpointPath)
	if name := strings.TrimSuffix(base, paths.ServiceEndpointExt); name != base && name != "" {
		return name
	}
	slug := strings.TrimSuffix(strings.TrimPrefix(envKey, paths.ServiceEnvVarPrefix), paths.ServiceEnvVarSuffix)
	if slug == "" {
		return envKey
	}
	return strings.ToLower(slug)
}

// probeService returns nil when svc is reachable, or the fault that stopped it.
//
// It dials the FULL authenticated path — pinned certificate, bearer token, ack —
// rather than a bare TCP connect, for two reasons. It is the exact path every
// in-jail client travels, so a green probe means what a reader assumes it means;
// and a bare connect would fail the TLS handshake at the listener, turning every
// boot into a rejected-connection record in the audit trail. Connect-then-close is
// a shape the framework explicitly supports: the front signals EOF upstream when a
// client wrote no payload bytes, which is what stops this probe wedging a framed
// daemon (svcendpoint/front.go's splice, pinned by front_probe_test.go).
func probeService(svc serviceEndpoint, deadline time.Time) *reachabilityResult {
	var last *reachabilityResult
	for attempt := 0; ; attempt++ {
		// THE FIRST ATTEMPT ALWAYS RUNS, whatever the clock says. A budget check
		// that could skip the dial entirely would report a service as reachable
		// without ever having touched it — the single answer this probe must never
		// produce, and one that would be invisible: a silent pass looks exactly like
		// a healthy jail. Only the RETRIES are clock-gated.
		timeout := reachabilityDialTimeout
		if attempt > 0 {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return last
			}
			if remaining < timeout {
				timeout = remaining
			}
		}

		// Read before dialling only to learn the ADVERTISED address for the
		// diagnostic; Dial re-reads the file itself, which is what lets a
		// republication between attempts be picked up.
		addr := ""
		if ep, err := svcendpoint.Read(svc.path); err == nil {
			addr = ep.HostPort
		}

		conn, err := svcendpoint.Dial(svc.path, timeout)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		last = &reachabilityResult{svc: svc, fault: classifyReachability(err), addr: addr, err: err}

		if attempt >= reachabilityRetries {
			return last
		}
		if time.Until(deadline) <= reachabilityRetryDelay {
			return last
		}
		time.Sleep(reachabilityRetryDelay)
	}
}

// classifyReachability maps a dial error onto the fault the user has to fix.
// svcendpoint's typed errors carry the attribution deliberately (endpointfile.go),
// so anything they do not claim is a transport failure — which is the case this
// probe exists for, and therefore the right default rather than an "unknown"
// bucket nobody would act on.
func classifyReachability(err error) reachabilityFault {
	switch {
	case errors.Is(err, svcendpoint.ErrEndpointMissing), errors.Is(err, svcendpoint.ErrEndpointMalformed):
		return faultUnpublished
	case errors.Is(err, svcendpoint.ErrAuthRejected):
		return faultRejected
	default:
		return faultUnreachable
	}
}

// reachabilityWarning renders one verdict. It names the SERVICE and the ADDRESS —
// without those two facts the line is unactionable, and "the address it could not
// reach" is the whole content of the finding.
func reachabilityWarning(res reachabilityResult) string {
	// The address may be unknown — an unpublished endpoint has no address to
	// report — so the sentence has to read correctly without it rather than
	// printing an empty one.
	target := "its advertised address"
	if res.addr != "" {
		target = res.addr
	}
	switch res.fault {
	case faultUnpublished:
		return "warning: host service '" + res.svc.name + "' is enabled but its endpoint file " +
			res.svc.path + " is missing or incomplete: " + res.err.Error() + "\n" +
			"  The host-side daemon never published, or this jail's services directory was\n" +
			"  removed after the container mounted it. Relaunch the jail to republish it."
	case faultRejected:
		return "warning: host service '" + res.svc.name + "' at " + target +
			" rejected this jail's token\n" +
			"  Reachable, but " + res.svc.path + " is stale relative to the running listener\n" +
			"  — it restarted and republished, or a predecessor's file was left behind.\n" +
			"  Relaunch the jail to republish it."
	default:
		return "warning: host service '" + res.svc.name + "' is enabled but UNREACHABLE from " +
			"inside this jail\n" +
			"  Dialing " + target + " failed: " + res.err.Error()
	}
}
