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
// # FATAL — an enabled service this jail cannot use is a failed launch
//
// docs/design/loopback-tls-reachability.md OQ-R2 rules the severity: "an enabled
// jail-facing service that the jail cannot reach is a failed launch", scoped by
// OQ-R3 to mean "yolo TRIED and failed" rather than "this host cannot". Since
// 2026-08-18 that is the mode this file SHIPS in. It landed as a warning first,
// because §10 sequences the witness before the fatal — a warning that misfires is
// noise, but a fatal that misfires costs a jail — and three things had to be true
// before the severity could move. All three are:
//
//   - THE SCOPING — "unsupported is not broken". The launcher carries its own
//     decision into the jail (paths.HostLoopbackEnvVar), because from inside a jail
//     the failures are indistinguishable: a service does not answer whichever it
//     was. With it, this file separates a KNOWN LIMITATION (yolo could not get this
//     host's network stack to forward the host's loopback — an old passt) from a
//     FAULT (yolo asked, or there was nothing to ask for, and the service is still
//     unusable) from a launch where nothing was established at all. Every state is
//     spelled rather than three of them sharing one silence (OQ-R6), which is what
//     makes `shared` — a jail on the launcher's own namespace, where there is no
//     forwarding hop to blame — sayable at all. See loopbackDisposition.
//   - THE ESCAPE HATCH — paths.AllowUnreachableServicesEnv, mirroring
//     YOLO_ALLOW_STALE_IMAGE: honoured loudly, naming what it suppresses. A hard
//     fatal with no override would leave a user unable to open a shell to fix the
//     very daemon that is failing.
//   - THE OBSERVATION, which was the one gate that was never code: until 2026-08-18
//     this probe had never been watched at a REAL boot, and every green it had was a
//     unit test dialling an in-process listener. Both directions were observed that
//     day. Healthy host: `YOLO_HOST_LOOPBACK=requested`, both endpoints published,
//     both answered through this probe's own path (TLS, cert-pinned,
//     token-authenticated) in 1-2 ms, nothing on the terminal, the affirmative line
//     in the boot log. Broken host: a service pointed at a dead port produced the
//     warning, the address, and the `requested` diagnosis — which correctly points
//     AWAY from the network stack.
//
// # What is escalated, and what is only diagnosed
//
// SEVERITY is the DISPOSITION's decision and nothing else's: requested and shared
// may fail a launch, unsupported and unknown and an absent variable never may
// (loopbackDisposition.escalates). All THREE fault classes escalate under it
// (OQ-R4) — an endpoint that never published and a listener that refused this
// jail's token are as much "enabled and unusable" as an address that does not
// answer. What the classes still differ in is WHERE TO LOOK, which is what the
// per-class warning and the four diagnosis paragraphs are for; severity
// deliberately does not duplicate that distinction.
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
	"fmt"
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
// It is TRUE: an enabled jail-facing service this jail cannot use refuses the
// launch. Isolating the mode to a variable is what made the flip day a day of
// changing one value rather than of writing the load-bearing branch blind against
// a user's broken host — the escalation, the escape hatch and the scoping were all
// exercised by tests for the whole time it was false.
//
// The false side survives ONLY as a test seam, and the warn wording with it, for
// one property that is otherwise unobservable: the escape hatch may speak only
// where it is actually suppressing something, and the guard enforcing that
// (reportUnusableServices) is a tautology in a binary that can never be in warn
// mode. Nothing in the boot path assigns this; only tests do.
var reachabilityFatal = true

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
	// dispositionUnattributed: the variable is ABSENT, empty, or carries a spelling
	// this binary does not know. Since OQ-R6 that means one thing in practice — the
	// launcher predates the variable, or is newer than this image and has invented a
	// state (the two versions move independently; AGENTS.md, "the baked binaries are
	// frozen at the last host `just load`"). It is the zero value so that every input
	// nobody thought about lands here, and it is never escalated.
	dispositionUnattributed loopbackDisposition = iota
	// dispositionRequested: yolo put the forwarding option on this container's
	// argv. An unreachable service is then a FAULT — the one case OQ-R2's fatal
	// is for.
	dispositionRequested
	// dispositionShared: this jail SHARES the launcher's network namespace —
	// `network.mode: host`, or podman-in-podman, where --net=host is forced. There
	// is no forwarding hop in that mode at all: the loopback a host daemon bound and
	// the loopback this jail dials are one loopback, which is why the launcher
	// published 127.0.0.1 rather than a gateway name for exactly these shapes. So an
	// unreachable service here has no host-stack ambiguity to hide in — it is the
	// STRONGEST case in the set rather than the vaguest (OQ-R5), and it used to
	// arrive as an absent variable, indistinguishable from the weakest.
	dispositionShared
	// dispositionUnsupported: yolo identified the rootless stack, could not get it
	// to forward the host's loopback, and launched anyway (OQ-R3: degrade, never
	// refuse). An unreachable service is a KNOWN LIMITATION of the host, and must
	// never fail a launch — a fatal here would reintroduce by the back door
	// exactly the refusal OQ-R3 rejected.
	dispositionUnsupported
	// dispositionUnknown: the launcher reached NO conclusion — a rootful podman, a
	// backend it does not recognise, a `podman info` it could not read, an explicit
	// network.mode it declined to override, the YOLO_NO_HOST_LOOPBACK opt-out,
	// Apple Container. Nothing was positively established, so nothing may be
	// escalated.
	//
	// It behaves exactly like dispositionUnattributed and is deliberately NOT folded
	// into it: the two differ in what they say about the LAUNCHER, which is the only
	// thing a boot log can use to tell a version skew from a launcher that ran and
	// declined. "unknown" means a launcher that had an opinion about having no
	// opinion; "unattributed" means one that could not have had one.
	dispositionUnknown
)

// launcherLoopbackDisposition reads the launcher's verdict for this jail.
// String names the disposition for the boot log. The spellings match the env-var
// values the launcher sets (paths.HostLoopback*) rather than inventing a second
// vocabulary, so a line in the log and a line in the launch output are comparable
// without a translation table.
func (d loopbackDisposition) String() string {
	switch d {
	case dispositionRequested:
		return paths.HostLoopbackRequested
	case dispositionShared:
		return paths.HostLoopbackShared
	case dispositionUnsupported:
		return paths.HostLoopbackUnsupported
	case dispositionUnknown:
		return paths.HostLoopbackUnknown
	default:
		// The one name with no env-var spelling behind it, because it is the one
		// state the launcher cannot ASSERT: it is what this binary calls a variable
		// that was never set or was set to something it does not understand.
		return "unattributed"
	}
}

// escalates reports whether a service this jail cannot use may FAIL THE LAUNCH,
// given what the launcher said about host-loopback forwarding. It is the whole of
// severity's decision table, and it is written as an ALLOWLIST of the two positive
// claims for the same reason launcherLoopbackDisposition matches its spellings
// exactly: every value nobody has thought of yet has to land on "never fail a
// launch" without anyone having to remember to come back here and add it.
//
//   - requested — yolo put the forwarding option on this container's argv, so the
//     network stack is the one thing already ruled out. This is the case OQ-R2's
//     fatal was written for.
//   - shared — the jail is on the launcher's own network namespace, so there is no
//     forwarding hop for a host limitation to hide behind and the address the
//     daemons published is the launcher's own loopback. OQ-R5 rules this the
//     STRONGEST case in the set rather than the vaguest.
//
// unsupported is excluded by OQ-R3: a host yolo could not get to forward its
// loopback has done nothing wrong, and refusing it here would reintroduce by the
// back door exactly the refusal that ruling rejected. unknown and unattributed are
// excluded one step further out — nothing was positively established about this
// launch, so there is nothing to escalate on.
func (d loopbackDisposition) escalates() bool {
	switch d {
	case dispositionRequested, dispositionShared:
		return true
	default:
		return false
	}
}

func launcherLoopbackDisposition(e *Env) loopbackDisposition {
	switch e.Getenv(paths.HostLoopbackEnvVar) {
	case paths.HostLoopbackRequested:
		return dispositionRequested
	case paths.HostLoopbackShared:
		return dispositionShared
	case paths.HostLoopbackUnsupported:
		return dispositionUnsupported
	case paths.HostLoopbackUnknown:
		return dispositionUnknown
	default:
		// Absent, empty, or a spelling some future launcher invented — all three
		// are "not attributable". A value this binary does not recognise must never
		// be read as permission to fail a launch, which is why the escalating values
		// are the ones that have to be matched exactly and every other input lands
		// here. Adding spellings is therefore free in the direction that costs a
		// jail: an image older than its launcher reads a new state as this one.
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

	// The affirmative record, to the LOG only. A healthy jail's witness is silent on
	// the terminal by design, and that silence is indistinguishable from a witness
	// that never ran — an ambiguity that stops being academic once this can refuse a
	// launch, because "no complaint" is then the evidence the launch was allowed on.
	// probeService returns nil for a service it reached, so the nil count IS the
	// reachable count.
	//
	// DEFERRED so it lands after every warning below. Both sinks are the same file in
	// a real boot, and emitted inline this summary wedged itself BETWEEN a finding and
	// the explanation of that finding — observed 2026-08-18 in an actual boot log,
	// splitting the two things a reader has to hold together.
	defer func() {
		reached := 0
		for _, res := range results {
			if res == nil {
				reached++
			}
		}
		e.note(fmt.Sprintf("reachability: %d/%d enabled service(s) reachable, disposition=%s",
			reached, len(results), launcherLoopbackDisposition(e)))
	}()

	// One line per broken service, then the shared explanation ONCE. Repeating a
	// paragraph about pasta's forwarding under every service is how a real finding
	// gets skimmed past: on a broken host EVERY jail-facing service fails for the
	// same reason at the same instant, so the diagnosis is a property of the launch
	// and not of any one of them.
	//
	// TWO LISTS, because severity and diagnosis are scoped differently, and that
	// difference IS OQ-R4. `unusable` is every fault class — each one means "this
	// service is enabled and this jail cannot use it", which is the whole of what the
	// severity rule is about. `unreachable` is the one class the launcher's forwarding
	// decision can say anything about, and it alone earns the disposition paragraph: a
	// missing endpoint file printed under a paragraph about pasta sends the reader to
	// the wrong machine entirely.
	var unreachable, unusable []string
	for _, res := range results {
		if res == nil {
			continue
		}
		if res.fault == faultUnreachable {
			unreachable = append(unreachable, res.svc.name)
		}
		unusable = append(unusable, res.svc.name)
		e.warn(reachabilityWarning(*res))
	}

	if len(unusable) == 0 {
		return
	}

	// The diagnosis — only for the class it describes — and then the verdict, only
	// for the launches whose disposition can carry one.
	disposition := launcherLoopbackDisposition(e)
	if len(unreachable) > 0 {
		e.warn(reachabilityExplanationFor(disposition))
	}
	if !disposition.escalates() {
		// "Unsupported is not broken" (OQ-R3), and "not attributable" is not broken
		// either. Neither may ever fail a launch, whatever the fault class — the fault
		// says what is wrong with the service, never whether this host could have been
		// asked. See loopbackDisposition.escalates for the whole table.
		return
	}
	reportUnusableServices(e, disposition, unusable)
}

// reportUnusableServices is the escalation point: the ONE place where OQ-R2's
// ruling changes what happens to the boot, written so that the mode is the value
// of reachabilityFatal and nothing else.
func reportUnusableServices(e *Env, d loopbackDisposition, names []string) {
	// The hatch is consulted only where it can actually suppress something. In warn
	// mode the launch was never at risk, so announcing it would be a line about
	// nothing — and an escape hatch that speaks on launches it is not saving trains
	// the reader to skip the line it exists to be read on. That is the same rule
	// internal/cli/run/hostloopback.go's own opt-out follows.
	if reachabilityFatal && e.Getenv(paths.AllowUnreachableServicesEnv) != "" {
		e.warn(unusableOverrideNotice(names))
		return
	}
	e.warn(unusableServicesMessage(d, names, reachabilityFatal))
	if reachabilityFatal {
		// genFailuresError (boot.go) turns this into the error that aborts the boot
		// before the agent is ever exec'd — Main runs this probe immediately above
		// that gate for exactly this call.
		e.genFailure("host services unusable from inside the jail: " + strings.Join(names, ", "))
	}
}

// unusableServicesMessage is the verdict, printed in both modes with only the
// parts that genuinely differ differing.
//
// The fatal form MUST name the escape hatch. A refusal a user cannot get past is
// the failure mode OQ-R2's own implementation note is about: the daemon they would
// have to fix is on the host, and the shell they would fix it from is in the jail
// that just refused to start. The warning form must NOT name it, because there is
// nothing yet to escape and a hatch advertised before it does anything is a hatch
// people set once and forget.
//
// The LEAD SENTENCE is per-disposition and cannot be shared, which is the one thing
// widening the escalation set to `shared` (OQ-R5) actually cost. The old single
// spelling opened "yolo requested host-loopback forwarding for this jail", true of
// the only value that could reach it then and flatly false of a launch that never
// needed any forwarding — and a verdict whose first sentence describes machinery the
// reader's jail does not have is worse than no verdict.
func unusableServicesMessage(d loopbackDisposition, names []string, fatal bool) string {
	body := unusableServicesLead(d, names)
	if !fatal {
		return "warning: " + body +
			"  (OQ-R2 rules this a failed launch; this witness is running in warn mode on\n" +
			"  this launch, so the jail is starting anyway.)\n" +
			"  docs/design/loopback-tls-reachability.md"
	}
	return "Error: " + body +
		"  Refusing to start: an enabled jail-facing service the jail cannot use is a\n" +
		"  failed launch (docs/design/loopback-tls-reachability.md, OQ-R2).\n" +
		"  If this jail is knowingly fine without those services — you are debugging the\n" +
		"  host daemon itself, or you only need a shell — launch anyway:\n" +
		"      " + paths.AllowUnreachableServicesEnv + "=1 <your yolo command>"
}

// unusableServicesLead states WHAT happened and why it is this host's fault rather
// than this host's limitation. Only the escalating dispositions reach it, so it has
// exactly two arms — and the shared arm must not name a forwarding hop, because
// that is precisely the machinery a shared namespace does not have.
//
// It says "unusable" rather than "unreachable" because since OQ-R4 the list can hold
// any of the three fault classes: an endpoint that never published and a listener
// that refused this jail's token are both here, and calling either one unreachable
// would send the reader to the network for a file. The per-service warnings above
// already carry which is which.
func unusableServicesLead(d loopbackDisposition, names []string) string {
	if d == dispositionShared {
		return "this jail SHARES the network namespace of whatever launched it, and " +
			serviceListPhrase(names) + " unusable from inside it.\n" +
			"  That is a FAULT rather than a limitation of this host: there is no forwarding\n" +
			"  hop in this mode for anything to be missing from — the address is the\n" +
			"  launcher's own loopback — so the other end is down, never published, or\n" +
			"  answering with a credential this jail does not hold.\n"
	}
	// dispositionRequested, the only other value escalates() lets through. Spelled
	// as the fallback rather than as a second `if` so that a disposition added to
	// the escalating set without a lead of its own gets the widest true sentence
	// instead of silently getting the shared one.
	return "yolo requested host-loopback forwarding for this jail and " +
		serviceListPhrase(names) + " unusable from inside it.\n" +
		"  That is a FAULT rather than a limitation of this host: the forwarding WAS\n" +
		"  asked for, so no network option is missing — the other end is down, never\n" +
		"  published, or answering with a credential this jail does not hold.\n"
}

// unusableOverrideNotice is the hatch being honoured, and it says what it is
// suppressing rather than going quiet — the whole of what makes an override
// trustworthy. It mirrors the stale-image report's "CONTINUING ON A STALE IMAGE",
// including the part that matters most: nothing was repaired.
func unusableOverrideNotice(names []string) string {
	return "warning: " + paths.AllowUnreachableServicesEnv + " is set — CONTINUING with " +
		serviceListPhrase(names) + " unusable from inside this jail.\n" +
		"  Nothing was repaired; the launch was merely allowed to proceed, and every\n" +
		"  in-jail client of those services will still fail. Unset it to have an\n" +
		"  unusable service refuse the launch again.\n" +
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
// actually did. They are genuinely different findings — a host limitation, a fault,
// a jail with no forwarding hop at all, and a failure nothing can attribute — and
// printing the mechanism paragraph at someone whose launcher already told them the
// specific reason is how the useful line gets lost.
//
// `unknown` and an unattributed launch share one text on purpose: both mean nobody
// established anything about the forwarding, so the widest explanation is the only
// honest one, and it already ends by pointing at the launch output rather than
// guessing which branch fired.
func reachabilityExplanationFor(d loopbackDisposition) string {
	switch d {
	case dispositionRequested:
		return reachabilityFaultExplanation
	case dispositionShared:
		return reachabilitySharedExplanation
	case dispositionUnsupported:
		return reachabilityLimitationExplanation
	default:
		return reachabilityExplanation
	}
}

// reachabilitySharedExplanation is the diagnosis for a jail that shares the
// launcher's network namespace, and it needs its own paragraph because all three of
// the others would be actively MISLEADING here rather than merely imprecise.
//
// There is no forwarding hop in this mode. `--net=host` — chosen, or forced for
// podman-in-podman because netavark cannot create a netns without NET_ADMIN — puts
// the jail on the launcher's own stack, so the loopback a daemon bound and the
// loopback this jail dials are ONE loopback. That is why every host daemon published
// 127.0.0.1 for this launch instead of a gateway name (internal/cli/run's
// advertiseHostFor: "not merely correct, it is the ONLY thing that works").
//
// So a reader sent to check pasta, --map-host-loopback or host.containers.internal
// would be checking a hop their jail does not have — an afternoon spent on a network
// stack that is not in the path. Everything true about this case points one way: the
// address is right and nothing is answering on it, so the daemon is the subject. The
// last line is there because "the host" is ambiguous in exactly the shape that
// produces this state most often — inside a nested jail it means the OUTER jail, not
// the machine at the bottom of the stack.
const reachabilitySharedExplanation = "" +
	"  This jail SHARES the network namespace of whatever launched it (--net=host,\n" +
	"  which podman-in-podman also forces), so there is no forwarding to have gone\n" +
	"  wrong: the address above is the launcher's own loopback, and this jail's\n" +
	"  loopback is the same one. Nothing about the rootless network stack — pasta,\n" +
	"  slirp4netns, the gateway name — is in the path on this launch, so checking\n" +
	"  those is checking a hop that does not exist here.\n" +
	"  Look at the daemon instead: is it running (`yolo check` where this jail was\n" +
	"  launched from), and did it republish its endpoint after a restart? For a\n" +
	"  nested jail that is the jail that launched this one, not the host machine.\n" +
	"  docs/design/loopback-tls-reachability.md"

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

// reachabilityExplanation is the diagnosis for the two states that establish
// nothing: `unknown` — a launcher that ran and reached no conclusion — and an
// unattributed launch, which since OQ-R6 means a launcher older than
// paths.HostLoopbackEnvVar or one whose spelling this image does not know. It is the
// widest of the four because it is the one that has to cover a case it cannot
// narrow: it walks the mechanism and then lists every branch the launcher might have
// taken, since it does not know which.
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
//
// # There is deliberately no pre-open stat here any more
//
// There used to be: endpointReadFault, which stat'd svc.path before the first read
// because os.ReadFile of a writer-less fifo blocks in open(2) forever and no timeout
// in this file could reach it. That guard now lives inside svcendpoint.Read itself
// (readEndpointFile), where it belongs — the endpoint format is one file and its
// readers are four packages, and the two that were still exposed are worse off than
// this one was. The in-jail OAuth terminator reads the same read-write-mounted
// directory with no deadline anywhere, and its symptom is Claude Code never starting
// with no error at all; `yolo check` and the run pipeline's readiness polls hang the
// same way. A guard that only ever covered the boot path left those standing.
//
// Nothing observable here changed. The gate returns ErrEndpointMalformed, which
// classifyReachability already maps to faultUnpublished, and it carries the same
// fileKindName wording ("named pipe (fifo)", "directory") into the same warning —
// which is what TestReachabilityProbeNeverOpensANonRegularEndpoint asserts, and it
// still asserts it against this function rather than against svcendpoint.
//
// The one real difference: a fifo or an oversized file now travels the RETRY LOOP
// instead of short-circuiting above it, because Read reports it as an ordinary error
// rather than being pre-empted. Three instant failures and two retry delays is about
// a second, on a boot that is already producing a warning, and the alternative is a
// second guard here whose only job is to skip a sleep. That is not worth two places
// that both decide what an endpoint file may be.
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
