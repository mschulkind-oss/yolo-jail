package loopholes

// guestrun.go is THE ONE ANSWER to "which of this launch's composed jail daemons does the
// jail on runtime rt actually run?" — read by the launch's served set, by `yolo check`'s
// prediction of it, and by the macos-user arm that hands the runnable ones to the guest's
// supervisor and declines the rest by name. One function, so the prediction, the served set
// and what the supervisor starts cannot be three different selections.
//
// A container runs every daemon its payload names. macos-user runs them too since OQ-DP8 and
// OQ-DP9 (docs/design/declaration-parity.md, ruled 2026-09-28: "if you would have run it in
// the jail container, you run it on the guest"), confined by the Seatbelt profile — except
// the three shapes below, each declined for a reason that is a fact about the declaration and
// this backend rather than about yolo's roadmap.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// DeclinedJailDaemon is one composed jail daemon a runtime does not run, and why.
type DeclinedJailDaemon struct {
	Spec JailDaemonSpec
	Why  string
}

// JailDaemonsRunIn splits specs into the daemons a jail on runtime rt runs and the ones it
// declines. Order is kept on both sides. Every runtime but macos-user runs every one.
func JailDaemonsRunIn(rt string, specs []JailDaemonSpec) ([]JailDaemonSpec, []DeclinedJailDaemon) {
	if rt != "macos-user" {
		return specs, nil
	}
	var runs []JailDaemonSpec
	var declined []DeclinedJailDaemon
	for _, s := range specs {
		if why := macosUserGuestDecline(s); why != "" {
			declined = append(declined, DeclinedJailDaemon{Spec: s, Why: why})
			continue
		}
		runs = append(runs, s)
	}
	return runs, declined
}

// macosUserGuestDecline is why the macos-user guest does not run s, "" when it does.
//
//   - AN INTERCEPTING LOOPHOLE's daemon (the Claude OAuth terminator). Its whole job is to be
//     what an intercepted hostname resolves to, which takes the container's `--add-host`; the
//     guest shares the Mac's resolver and cannot pin a name, and the terminator's default
//     port, 443, is privileged for the sandbox account. Keyed on the intercept list, the
//     same fact admitsJailSideEffects skips Apple Container on — not on a name.
//     jail-daemon-on-macos-user-plan.md §Don't rules it declined for good.
//   - A PACK SERVICE's daemon (the wire bridge). A pack service reaches this backend through
//     its host half, launch-owned, started when a profiled agent's pairing needs it
//     (docs/plans/notch-convergence.md OQ-NC1 A, NC-D65; internal/cli/run's
//     macosuserservices.go). Running its jail daemon too would serve one address twice.
//   - A daemon whose argv names the CONTAINER'S LOOPHOLE MOUNT
//     (loopholedecl.JailLoopholeDir, what `{jail_loophole_dir}` resolves to at load). That
//     path exists only inside a container; the guest has no copy there, and OQ-DP8's
//     "runs exactly as declared" leaves the argv alone rather than rewriting it.
func macosUserGuestDecline(s JailDaemonSpec) string {
	switch {
	case s.Intercepts:
		return "it terminates TLS for an intercepted hostname, which needs a container's " +
			"--add-host and a bind to port 443; the sandbox has neither"
	case s.Service:
		return "a pack service runs its host half on this backend instead, when a profiled " +
			"agent's pairing needs it"
	case namesContainerLoopholeDir(s.Cmd):
		return "its argv names the container's loophole mount (" + loopholedecl.JailLoopholeDir("") +
			"…), which the sandbox has no copy of"
	}
	return ""
}

func namesContainerLoopholeDir(argv []string) bool {
	root := loopholedecl.JailLoopholeDir("")
	for _, a := range argv {
		if strings.HasPrefix(a, root) {
			return true
		}
	}
	return false
}

// JailDaemonNamesRunIn is the names of the daemons rt runs out of specs, and each one's
// served listen address: the served set's two inputs.
func JailDaemonNamesRunIn(rt string, specs []JailDaemonSpec) ([]string, map[string]string) {
	runs, _ := JailDaemonsRunIn(rt, specs)
	names := make([]string, 0, len(runs))
	listen := map[string]string{}
	for _, s := range runs {
		names = append(names, s.Name)
		if s.Listen != "" {
			listen[s.Name] = s.Listen
		}
	}
	return names, listen
}
