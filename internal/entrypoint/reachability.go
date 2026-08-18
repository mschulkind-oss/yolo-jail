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
// jail-facing service that the jail cannot reach is a failed launch", scoped by
// OQ-R3 to mean "yolo TRIED and failed" rather than "this host cannot". This
// landed in WARN mode on purpose, because §10 sequences the witness BEFORE the
// fatal — a warning that misfires is noise, but a fatal that misfires costs a
// jail.
//
// Two of the three things the flip needs are now BUILT, and tested in both modes:
//
//   - THE SCOPING — "unsupported is not broken". The launcher carries its own
//     decision into the jail (paths.HostLoopbackEnvVar), because from inside a jail
//     the two failures are indistinguishable: a service does not answer either
//     way. With it, this file separates a KNOWN LIMITATION (yolo could not get
//     this host's network stack to forward the host's loopback — an old passt, an
//     unrecognised backend) from a FAULT (yolo asked, and the service is still
//     unreachable). Only a fault is ever escalated. See loopbackDisposition.
//   - THE ESCAPE HATCH — paths.AllowUnreachableServicesEnv, mirroring
//     YOLO_ALLOW_STALE_IMAGE: honoured loudly, naming what it suppresses. A hard
//     fatal with no override would leave a user unable to open a shell to fix the
//     very daemon that is failing.
//
// WHAT IS STILL OWED is the flip's own gate, and it is not code: THIS PROBE HAS
// NEVER BEEN OBSERVED AT A REAL BOOT ON A HEALTHY HOST. Every green it has is a
// unit test dialling an in-process listener. Someone has to launch a jail on a
// working host and watch it stay silent, and launch one on a broken host and
// watch it speak, before a false positive here is allowed to cost a jail.
//
// The flip is then one line: reachabilityFatal = true.
//
// (An earlier spelling of this note also owed "name the required passt version in
// the refusal". That is now wrong and is deliberately not carried forward: OQ-R3
// was re-ruled the same day to degrade rather than refuse, so an old-passt host
// reports `unsupported` and is never escalated — there is no refusal left for a
// passt version to appear in.)
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// reachabilityFatal is OQ-R2's flip, deliberately isolated to one boolean.
//
// It is false, and the header names the one thing still owed before it may become
// true: an observation at a real boot on a healthy host. Spelling the mode out as
// a variable rather than leaving the fatal branch unwritten is the point of this
// step — the escalation, the escape hatch and the scoping can all be EXERCISED by
// tests today, so the day this flips is a day of changing one value, not a day of
// writing the load-bearing branch blind against a user's broken host.
//
// Nothing in the boot path assigns it; only tests do.
var reachabilityFatal = false

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

// loopbackDisposition is what the LAUNCHER decided about host-loopback forwarding
// for this jail, read back from paths.HostLoopbackEnvVar.
//
// It is the whole of "unsupported is not broken", and it has to be carried rather
// than derived: from inside a jail the two cases are the same observation — a
// service that does not answer — and the network facts that separate them (which
// rootless stack, which passt, what went on the argv) are all host facts, none of
// which survive into this namespace.
type loopbackDisposition int

const (
	// dispositionUnattributed: the launcher said nothing. An explicit
	// network.mode, the YOLO_NO_HOST_LOOPBACK opt-out, a rootful podman, an
	// unrecognised backend, Apple Container, a nested jail on --net=host, or a
	// launcher older than the variable itself. The launch output is where the
	// reason for any of those lives; an unreachable service here is never
	// escalated, because nothing was positively established about it.
	dispositionUnattributed loopbackDisposition = iota
	// dispositionRequested: yolo put the forwarding option on this container's
	// argv. An unreachable service is then a FAULT — the one case OQ-R2's fatal
	// is for.
	dispositionRequested
	// dispositionUnsupported: yolo identified the rootless stack, could not get it
	// to forward the host's loopback, and launched anyway (OQ-R3: degrade, never
	// refuse). An unreachable service is a KNOWN LIMITATION of the host, and must
	// never fail a launch — a fatal here would reintroduce by the back door
	// exactly the refusal OQ-R3 rejected.
	dispositionUnsupported
)

// launcherLoopbackDisposition reads the launcher's verdict for this jail.
func launcherLoopbackDisposition(e *Env) loopbackDisposition {
	switch e.Getenv(paths.HostLoopbackEnvVar) {
	case paths.HostLoopbackRequested:
		return dispositionRequested
	case paths.HostLoopbackUnsupported:
		return dispositionUnsupported
	default:
		// Absent, empty, or a spelling some future launcher invented — all three
		// are "not attributable". A value this binary does not recognise must never
		// be read as permission to fail a launch, which is why the escalating value
		// is the one that has to be matched exactly and every other input lands
		// here.
		return dispositionUnattributed
	}
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
	var unreachable []string
	for _, res := range results {
		if res == nil {
			continue
		}
		if res.fault == faultUnreachable {
			unreachable = append(unreachable, res.svc.name)
		}
		e.warn(reachabilityWarning(*res))
	}
	if len(unreachable) == 0 {
		return
	}

	// The diagnosis, and then — only for the one case that earns it — the verdict.
	//
	// The ESCALATION SET is deliberately just the unreachable class. An
	// unpublished endpoint or a rejected token is also a service the jail cannot
	// use, but neither has anything to do with what the launcher decided about
	// forwarding, so the disposition cannot attribute them and they stay warnings
	// until they get a ruling of their own.
	disposition := launcherLoopbackDisposition(e)
	e.warn(reachabilityExplanationFor(disposition))
	if disposition != dispositionRequested {
		// "Unsupported is not broken" (OQ-R3), and "not attributable" is not broken
		// either. Neither may ever fail a launch, in this mode or any future one.
		return
	}
	reportUnreachableFault(e, unreachable)
}

// reportUnreachableFault is the escalation point: the ONE place where OQ-R2's
// ruling changes what happens to the boot, written so that the flip is the value
// of reachabilityFatal and nothing else.
func reportUnreachableFault(e *Env, names []string) {
	// The hatch is consulted only where it can actually suppress something. In warn
	// mode the launch was never at risk, so announcing it would be a line about
	// nothing — and an escape hatch that speaks on launches it is not saving trains
	// the reader to skip the line it exists to be read on. That is the same rule
	// internal/cli/run/hostloopback.go's own opt-out follows.
	if reachabilityFatal && e.Getenv(paths.AllowUnreachableServicesEnv) != "" {
		e.warn(unreachableOverrideNotice(names))
		return
	}
	e.warn(unreachableFaultMessage(names, reachabilityFatal))
	if reachabilityFatal {
		// genFailuresError (boot.go) turns this into the error that aborts the boot
		// before the agent is ever exec'd — Main runs this probe immediately above
		// that gate for exactly this call.
		e.genFailure("host services unreachable from inside the jail: " + strings.Join(names, ", "))
	}
}

// unreachableFaultMessage is the verdict, printed in both modes with only the
// parts that genuinely differ differing.
//
// The fatal form MUST name the escape hatch. A refusal a user cannot get past is
// the failure mode OQ-R2's own implementation note is about: the daemon they would
// have to fix is on the host, and the shell they would fix it from is in the jail
// that just refused to start. The warning form must NOT name it, because there is
// nothing yet to escape and a hatch advertised before it does anything is a hatch
// people set once and forget.
func unreachableFaultMessage(names []string, fatal bool) string {
	body := "yolo requested host-loopback forwarding for this jail and " +
		serviceListPhrase(names) + " unreachable.\n" +
		"  That is a FAULT rather than a limitation of this host: the forwarding WAS\n" +
		"  asked for, so no network option is missing — something on the other end is\n" +
		"  down or unreachable for another reason.\n"
	if !fatal {
		return "warning: " + body +
			"  (OQ-R2 rules this a failed launch; this witness is still in warn mode, so the\n" +
			"  jail is starting anyway.)\n" +
			"  docs/design/loopback-tls-reachability.md"
	}
	return "Error: " + body +
		"  Refusing to start: an enabled jail-facing service the jail cannot reach is a\n" +
		"  failed launch (docs/design/loopback-tls-reachability.md, OQ-R2).\n" +
		"  If this jail is knowingly fine without those services — you are debugging the\n" +
		"  host daemon itself, or you only need a shell — launch anyway:\n" +
		"      " + paths.AllowUnreachableServicesEnv + "=1 <your yolo command>"
}

// unreachableOverrideNotice is the hatch being honoured, and it says what it is
// suppressing rather than going quiet — the whole of what makes an override
// trustworthy. It mirrors the stale-image report's "CONTINUING ON A STALE IMAGE",
// including the part that matters most: nothing was repaired.
func unreachableOverrideNotice(names []string) string {
	return "warning: " + paths.AllowUnreachableServicesEnv + " is set — CONTINUING with " +
		serviceListPhrase(names) + " unreachable from inside this jail.\n" +
		"  Nothing was repaired; the launch was merely allowed to proceed, and every\n" +
		"  in-jail client of those services will still fail. Unset it to have an\n" +
		"  unreachable service refuse the launch again.\n" +
		"  docs/design/loopback-tls-reachability.md"
}

// serviceListPhrase renders the affected services as a sentence fragment ending
// in the right verb, so the two messages above read correctly for one service and
// for several without either of them branching.
func serviceListPhrase(names []string) string {
	if len(names) == 1 {
		return "service '" + names[0] + "' is"
	}
	return strconv.Itoa(len(names)) + " services (" + strings.Join(names, ", ") + ") are"
}

// reachabilityExplanationFor picks the diagnosis that matches what the launcher
// actually did. The three are genuinely different findings — a host limitation, a
// fault, and an unattributed failure — and printing the mechanism paragraph at
// someone whose launcher already told them the specific reason is how the useful
// line gets lost.
func reachabilityExplanationFor(d loopbackDisposition) string {
	switch d {
	case dispositionRequested:
		return reachabilityFaultExplanation
	case dispositionUnsupported:
		return reachabilityLimitationExplanation
	default:
		return reachabilityExplanation
	}
}

// reachabilityLimitationExplanation is the OQ-R3 path: yolo could not ask this
// host to forward its loopback, and launched anyway. It must not read as an error
// — launching is the correct outcome and the user has done nothing wrong — and it
// must point at the launch output rather than repeat it, because the actionable
// half (the passt version, the command that checks) is host knowledge this side
// of the boundary does not have.
const reachabilityLimitationExplanation = "" +
	"  This is a KNOWN LIMITATION of this host, not a fault in this jail.\n" +
	"  yolo could not get this host's rootless network stack to forward the host's\n" +
	"  LOOPBACK into the jail — an old passt, or a capability it could not confirm —\n" +
	"  and launched anyway rather than refusing to run on a machine whose owner may\n" +
	"  not be able to change it. This jail's launch output names what to upgrade and\n" +
	"  the command that checks. Nothing else about the jail is affected, and this\n" +
	"  will never fail a launch.\n" +
	"  docs/design/loopback-tls-reachability.md"

// reachabilityFaultExplanation is the other side of that split: the option went
// out on the argv and the service is still unreachable, so the network stack is
// the one thing already ruled out. Naming that first is the whole value of
// carrying the launcher's decision in — it is what stops a reader spending an
// afternoon on pasta flags when their daemon is simply not running.
const reachabilityFaultExplanation = "" +
	"  yolo DID ask this host's network stack to forward the host's loopback into\n" +
	"  this jail on this launch, so the forwarding is not what is missing.\n" +
	"  Look at the host side: is the daemon running (`yolo check` on the host), and\n" +
	"  did it republish its endpoint after a restart? Every in-jail client of those\n" +
	"  services will fail the same way until it answers.\n" +
	"  docs/design/loopback-tls-reachability.md"

// reachabilityExplanation is the UNATTRIBUTED diagnosis — the one printed when the
// launcher told this jail nothing, which is also every launch by a launcher older
// than paths.HostLoopbackEnvVar. It is the widest of the three because it is the
// one that has to cover a case it cannot narrow: it walks the mechanism and then
// lists every branch the launcher might have taken, since it does not know which.
//
// It names the mechanism first and the remedy second, in that order
// on purpose: the remedy (docs/design/loopback-tls-reachability.md §6) now exists
// in the launcher — internal/cli/run/hostloopback.go asks the runtime to forward
// the host's loopback — so anyone reading this line is on a host where the
// launcher DECLINED to ask, and the reason it declined was printed at launch. The
// last line is what turns "your services are down" into somewhere to look; the
// launcher's own line is the actionable half, and this points at it rather than
// guessing which of its branches fired.
const reachabilityExplanation = "" +
	"  Every in-jail client of those services will fail the same way.\n" +
	"  yolo's host daemons bind the host's LOOPBACK and advertise the container\n" +
	"  runtime's gateway name, which only works if the runtime forwards that name to\n" +
	"  the host's loopback. Whether it does is a property of the rootless network\n" +
	"  stack AND of the address podman writes for that name: FALSE for pasta —\n" +
	"  podman's default since 5.0, which forwards to the host's global address — and\n" +
	"  true for slirp4netns only when allow_host_loopback is set AND the gateway name\n" +
	"  is pinned at slirp's own host address, which podman does not do by itself.\n" +
	"  yolo emits both. On the host, `podman info --format\n" +
	"  '{{.Host.RootlessNetworkCmd}}'` says which stack is in use.\n" +
	"  yolo asks for that forwarding itself on the default network.mode; if it could\n" +
	"  not — old passt, an explicit network.mode, a rootful or unrecognised runtime,\n" +
	"  or YOLO_NO_HOST_LOOPBACK — it said so in this jail's launch output above.\n" +
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
