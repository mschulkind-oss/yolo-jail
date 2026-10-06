package entrypoint

// requiredservice.go is what the boot does when a REQUIRED in-jail service does not start:
// the jail-daemon supervisor step's refusal, its escape hatch and its wording
// (docs/reference/loopback-tls-reachability.md OQ-R8, ruled 2026-10-05: (a)).
//
// A service is required when the launcher names it in paths.JailDaemonReadyNamesEnv, which
// run.serviceEndpointEnvArgs does for the wire bridge exactly when the launcher's serve
// decision says it will serve this launch. The readiness wait in startJailDaemonSupervisor
// then blocks until it answers.
//
// # The hatch reaches this refusal
//
// paths.AllowUnreachableServicesEnv used to downgrade the reachability witness and nothing
// else, so a jail whose bridge could not bind got no shell with it set: two gates, one hatch
// (MEASURED 2026-09-19, hold.go). Under OQ-R8 it reaches this gate too. What a bridge reports
// is mostly the user's own state — a port something else holds, a provider key that never
// arrived — which is what a hatch is for, and the witness's refusal already promised a shell
// to a user who only needs one. Like the witness's, it is honoured only where it suppresses
// something: a boot whose required services all report ready never mentions it.
//
// The hatch covers every way the wait can end without the service (R-D1 in the doc): a
// `failed` report, the supervisor exiting or its pipe failing before the service reported, a
// supervisor that could not be found or started, and a readiness line the wait cannot read.
// Each leaves the same jail, one whose required service is not running, and the override
// notice says so. It does NOT cover the orphan refusal (refuseOnOrphanedJailDaemons): that is
// about daemons a dead supervisor left holding this jail's service ports, continuing would
// start a second supervisor beside them, and it already names its next step, the pid to kill.
//
// # The refusal says what it is about
//
// It used to reach the user as `1 config generator(s) failed: start_jail_daemon_supervisor:
// jail daemon "wire-bridge" cannot publish …`, which calls the relay a config generator and
// names no way forward. It is now a service refusal (Env.refuseService) naming the service, the
// pack whose service it is and the selected pack whose `needs` brought that pack in, the cause
// in the daemon's own words — which carry its next step (free the port, supply the key;
// wirebridged's servePlan) — the daemon's log, and the hatch.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// requiredServiceError is the readiness wait's refusal: required services did not report ready.
type requiredServiceError struct {
	// names are the services it is about: every one that reported `failed`, then every one still
	// waited for when the supervisor never started, went away or garbled a line first.
	names []string
	// cause is what stopped them: the daemon's own reason, or the supervisor's fault.
	cause string
	// logs names each daemon's log file (jailDaemonLogsPhrase).
	logs string
}

func (r *requiredServiceError) Error() string {
	return "required in-jail " + requiredServicePhrase(nil, r.names) + " did not start: " +
		r.cause + "; " + r.logs
}

// waitingFor is the names the readiness wait has not yet heard from, sorted.
func waitingFor(ready map[string]bool) []string {
	out := make([]string, 0, len(ready))
	for name := range ready {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// notStartedError is the readiness wait's refusal from what it heard: failed maps each service
// that reported `failed` to its own reason, pending is every service it never heard from, and
// cause is why it stopped hearing (unused when pending is empty). One service keeps its cause
// unlabelled — the daemon's reason verbatim, or the supervisor's fault — and several label each.
func notStartedError(home string, failed map[string]string, pending []string, cause string) *requiredServiceError {
	failedNames := make([]string, 0, len(failed))
	for name := range failed {
		failedNames = append(failedNames, name)
	}
	sort.Strings(failedNames)
	names := append(failedNames, pending...)
	r := &requiredServiceError{names: names, logs: jailDaemonLogsPhrase(home, names)}
	if len(names) == 1 {
		r.cause = cause
		if len(failedNames) == 1 {
			r.cause = failed[failedNames[0]]
		}
		return r
	}
	causes := make([]string, 0, len(failedNames)+1)
	for _, name := range failedNames {
		causes = append(causes, "'"+name+"': "+failed[name])
	}
	if len(pending) > 0 {
		causes = append(causes, "'"+strings.Join(pending, "', '")+"': "+cause)
	}
	r.cause = strings.Join(causes, "; ")
	return r
}

// startJailDaemons is the boot step that starts the jail-daemon supervisor and decides what
// its failure does to the boot. A required service that did not start refuses the boot, unless
// the reachability hatch is set, in which case the boot continues saying what it let through.
// Any other failure (the orphan refusal) refuses whatever the hatch says.
//
// Either way the failure is a SERVICE refusal, never a config generator's (genStep): the boot's
// last line lists it under its own heading (genFailuresError).
func startJailDaemons(b *bootRun) {
	e := b.e
	err := startJailDaemonSupervisor(e)
	if err == nil {
		return
	}
	var req *requiredServiceError
	if !errors.As(err, &req) {
		e.warn("Error: " + err.Error())
		e.refuseService(err.Error(), false)
		return
	}
	// The witness skips what was reported here (R-D3), refused or let through.
	for _, name := range req.names {
		e.markNotReady(name)
	}
	// The staged packs, for the pack phrase. A pack tree that did not load leaves the phrase
	// naming the service alone; that failure is configure_pack_surfaces' to report.
	packs, _ := b.jailPacks()
	who := requiredServicePhrase(packs, req.names)
	if e.Getenv(paths.AllowUnreachableServicesEnv) != "" {
		e.warn(requiredServiceOverrideNotice(who, req))
		return
	}
	e.warn(requiredServiceRefusal(who, req))
	e.refuseService(who+" did not start: "+req.cause, true)
}

// requiredServicePhrase names the services and, from packs (the staged ones), the pack whose
// `service` each daemon runs for and the selected pack whose `needs` names that pack:
// `service 'wire-bridge' (from pack "wire-bridge", which pack "claude" needs)`. A service no staged
// pack declares is named alone, as is every service when packs is nil.
func requiredServicePhrase(packs []*packload.Pack, names []string) string {
	held, _ := packload.HeldServices(packs)
	owner := map[string]string{}
	for _, h := range held {
		owner[h.Service.Name] = h.Pack
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		part := "'" + name + "'"
		if pack := owner[name]; pack != "" {
			if by := packNeeding(packs, pack); by != "" {
				part += fmt.Sprintf(" (from pack %q, which pack %q needs)", pack, by)
			} else {
				part += fmt.Sprintf(" (from pack %q)", pack)
			}
		}
		parts = append(parts, part)
	}
	if len(parts) == 1 {
		return "service " + parts[0]
	}
	return "services " + strings.Join(parts, ", ")
}

// packNeeding is the first of packs, other than target itself, whose `needs` names target, or
// "". A need counts whether or not its `when_bins` held: the phrase says the pack names it,
// which is true either way.
func packNeeding(packs []*packload.Pack, target string) string {
	for _, p := range packs {
		if p == nil || p.Decl == nil || p.Name == target {
			continue
		}
		for _, need := range p.Decl.DeclaredNeeds() {
			if need.Pack == target {
				return p.Name
			}
		}
	}
	return ""
}

// pronoun is "it" for one service and "them" for several.
func pronoun(names []string) string {
	if len(names) == 1 {
		return "it"
	}
	return "them"
}

// capitalized is s with its first byte upper-cased (the log phrase opens a line here).
func capitalized(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// requiredServiceRefusal is the verdict, printed where the wait ended. Like the witness's
// (unusableServicesMessage) it MUST name the hatch: the shell a user would fix the daemon
// from is in the jail that just refused to start.
func requiredServiceRefusal(who string, r *requiredServiceError) string {
	it := pronoun(r.names)
	return "Error: required in-jail " + who + " did not start, so this jail cannot use " + it + ".\n" +
		"  Why: " + r.cause + "\n" +
		"  " + capitalized(r.logs) + "\n" +
		"  Refusing to start: a required service that did not start is a failed launch\n" +
		"  (docs/reference/loopback-tls-reachability.md, OQ-R8).\n" +
		"  If you only need a shell — to fix this from inside, or because this jail can do\n" +
		"  without " + it + " — launch anyway:\n" +
		"      " + paths.AllowUnreachableServicesEnv + "=1 <your yolo command>"
}

// requiredServiceOverrideNotice is the hatch being honoured. It says what it let through and
// why, and that nothing was repaired, as the witness's override notice does.
func requiredServiceOverrideNotice(who string, r *requiredServiceError) string {
	it := pronoun(r.names)
	return "warning: " + paths.AllowUnreachableServicesEnv + " is set — CONTINUING although " +
		"required in-jail " + who + " did not start.\n" +
		"  Why: " + r.cause + "\n" +
		"  " + capitalized(r.logs) + "\n" +
		"  Nothing was repaired; the launch was merely allowed to proceed, and every client\n" +
		"  of " + it + " in this jail will fail. Unset the variable to have a required service\n" +
		"  that did not start refuse the launch again.\n" +
		"  docs/reference/loopback-tls-reachability.md"
}
