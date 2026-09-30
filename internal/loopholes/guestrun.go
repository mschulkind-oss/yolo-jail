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
// the four shapes below, each declined for a reason that is a fact about the declaration and
// this backend rather than about yolo's roadmap.
//
// ONE OF THE FOUR DECLINES ONLY A PLACEMENT, never the daemon: one that declares
// `jail_daemon.host_cmd` is a credential service's DOORWAY (the thin adapter an agent's client
// talks to, which checks the launch's caller token and forwards to the service's host daemon;
// the word is docs/design/host-notch-services.md HS-D15's), and HS-D15 opens it on whichever
// loopback the agent sees. The guest shares the Mac's, so the launch opens it there, outside
// the sandbox, as a launch-owned listener (DoorwaysOutside), and the served set counts it
// (ServedJailDaemons). What the guest declines is only its jail-daemon form.

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
//   - A DOORWAY that declares its host argv (`jail_daemon.host_cmd`, HS-D15): it opens outside
//     the sandbox for this launch instead, on the Mac's loopback the agent shares. Running its
//     jail daemon too would serve one address twice.
//   - A daemon whose argv names the CONTAINER'S LOOPHOLE MOUNT
//     (loopholedecl.JailLoopholeDir, what `{jail_loophole_dir}` resolves to at load), or a
//     JAIL BINARY's container path (loopholedecl.JailBinaryPath, what `{jail_binary:<name>}`
//     resolves to). Both paths exist only inside a container; the guest has no copy there,
//     and OQ-DP8's "runs exactly as declared" leaves the argv alone rather than rewriting it.
func macosUserGuestDecline(s JailDaemonSpec) string {
	switch {
	case s.Intercepts:
		return "it terminates TLS for an intercepted hostname, which needs a container's " +
			"--add-host and a bind to port 443; the sandbox has neither"
	case s.Service:
		return "a pack service runs its host half on this backend instead, when a profiled " +
			"agent's pairing needs it"
	case isDoorway(s):
		return "its doorway opens outside the sandbox on this backend instead, as a listener " +
			"the launch owns on the Mac's loopback, which the agent shares (host-notch-services.md HS-D15)"
	case namesContainerLoopholeDir(s.Cmd):
		return "its argv names the container's loophole mount (" + loopholedecl.JailLoopholeDir("") +
			"…), which the sandbox has no copy of"
	case namesContainerBinary(s.Cmd):
		return "its argv names a binary the launch mounts into a container (" +
			loopholedecl.JailBinaryRoot + "…), which the sandbox has no copy of"
	}
	return ""
}

// namesContainerBinary reports whether argv names a jail binary's container path, what
// `{jail_binary:<name>}` resolves to at load (loopholedecl.JailBinaryPath). The guest declines
// it for the module mount's reason: the file is a container bind the sandbox never receives,
// and the argv is left as declared (OQ-DP8).
func namesContainerBinary(argv []string) bool {
	for _, a := range argv {
		if strings.HasPrefix(a, loopholedecl.JailBinaryRoot) {
			return true
		}
	}
	return false
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

// isDoorway reports whether s is a loophole's doorway with a declared host argv: the shape
// DoorwaysOutside opens outside a sandbox that shares the host's loopback. A pack service and
// an intercepting daemon never are.
func isDoorway(s JailDaemonSpec) bool {
	return len(s.HostCmd) > 0 && !s.Service && !s.Intercepts
}

// DoorwaysOutside is the doorways in specs that a launch on runtime rt opens OUTSIDE its jail,
// as launch-owned listeners on the host's loopback, instead of running their jail daemons
// (docs/design/host-notch-services.md HS-D15): on macos-user, whose agent shares the Mac's
// loopback, every one that declares `jail_daemon.host_cmd`; on every other runtime none, since
// a container has a loopback of its own and runs the doorway inside. Order is kept.
func DoorwaysOutside(rt string, specs []JailDaemonSpec) []JailDaemonSpec {
	if rt != "macos-user" {
		return nil
	}
	return Doorways(specs)
}

// Doorways is every doorway in specs that declares its host argv, whatever runtime composed
// them, order kept: DoorwaysOutside's answer on macos-user, and the set a `yolo host` launch
// chooses its doorways from (internal/cli/run's hostdoorways.go), since the host notch has no
// jail for a doorway to run in instead.
func Doorways(specs []JailDaemonSpec) []JailDaemonSpec {
	var out []JailDaemonSpec
	for _, s := range specs {
		if isDoorway(s) {
			out = append(out, s)
		}
	}
	return out
}

// ServedJailDaemons is the daemons of specs a launch on runtime rt SERVES, wherever it runs
// them: the ones its jail runs (JailDaemonsRunIn) and the doorways it opens outside
// (DoorwaysOutside). Order is kept. It is what the launch settles a served address for and
// what `yolo check` predicts, so the two cannot disagree about which ports a launch answers on.
func ServedJailDaemons(rt string, specs []JailDaemonSpec) []JailDaemonSpec {
	runs, _ := JailDaemonsRunIn(rt, specs)
	served := map[string]bool{}
	for _, s := range runs {
		served[s.Name] = true
	}
	for _, s := range DoorwaysOutside(rt, specs) {
		served[s.Name] = true
	}
	var out []JailDaemonSpec
	for _, s := range specs {
		if served[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

// ServedJailDaemonNames is the names of the daemons a launch on rt serves out of specs
// (ServedJailDaemons), and each one's served listen address: the served set's two inputs.
func ServedJailDaemonNames(rt string, specs []JailDaemonSpec) ([]string, map[string]string) {
	served := ServedJailDaemons(rt, specs)
	names := make([]string, 0, len(served))
	listen := map[string]string{}
	for _, s := range served {
		names = append(names, s.Name)
		if s.Listen != "" {
			listen[s.Name] = s.Listen
		}
	}
	return names, listen
}
