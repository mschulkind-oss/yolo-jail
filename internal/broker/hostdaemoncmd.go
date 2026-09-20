// The SET-WIDE half of the management surface: the report over every host-scoped
// daemon, and the one function that spells the command which cycles a given one.
//
// The per-daemon bodies are brokercmd.go's; this file adds only what needs more
// than one member — and it renders each member THROUGH PrintStatus rather than
// assembling a second version of the same facts, so the one-daemon report and the
// set report can never disagree about what "healthy" looks like.
package broker

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
)

// HostDaemonVerb is the management verb's registry name, in ONE place because
// three surfaces spell it: the command's own help, the refusals below, and the
// launch-path warning about an alive-but-incompatible daemon
// (internal/cli/run/loopholesruntime.go). A literal in each of those is how the
// last one came to name a command that cycles a different daemon.
const HostDaemonVerb = "host-daemon"

// CycleCommand is THE answer to "what do I type to cycle the daemon called
// <name>?", and it is a function precisely because the answer used to be a
// constant: `yolo broker restart`, printed at the one moment a user is being told
// their token refresh is silently broken, for whichever of the host-scoped
// daemons had failed. For two of the three that command cycles a different
// process and leaves the broken one running (OQ-HD2).
//
// IT DOES NOT SPECIAL-CASE THE BROKER. `yolo broker restart` still works and
// always will — the alias is retained — but there is one grammar here rather than
// a switch on a loophole name, so the sentence is right for a daemon that ships
// tomorrow without anybody editing this function.
func CycleCommand(name string) string {
	return "yolo " + HostDaemonVerb + " restart " + name
}

// SetStatus is one member's row in the set-wide report: the loophole name and
// whether anything declares it, followed by the SAME Status the single-daemon
// report serializes. Embedded, so the JSON is flat and a consumer that knows
// `yolo broker status --format json` already knows this document.
type SetStatus struct {
	Loophole string `json:"loophole"`
	Declared bool   `json:"declared"`
	Status
}

// SetDeps are the seams for the set-wide report. Set is the membership (derived
// by Singletons); For builds the per-member CLIDeps, and is injectable so a test
// can describe a whole machine's worth of daemons without one running.
type SetDeps struct {
	Out, Err    io.Writer
	Color       bool
	IsTTYStdout func() bool
	Format      string
	Set         []Singleton
	For         func(Singleton) CLIDeps
}

// PrintSetStatus reports every member of the host-scoped set, exiting 0 only when
// every one of them is healthy.
//
// AN EMPTY SET IS REPORTED, NOT INVENTED. Discovery is fail-safe-empty — in a
// jail, and in any process that resolved no packs, no pack loophole is visible at
// all — so "nothing declares one and nothing is running" is a real answer and
// exits 0. Reporting it beats a bare prompt, which reads as a bug.
func PrintSetStatus(d SetDeps) int {
	if outfmt.IsJSON(d.Format) {
		rows := make([]SetStatus, 0, len(d.Set))
		worst := 0
		for _, s := range d.Set {
			st := BrokerStatus(d.For(s).Life)
			if !st.Healthy {
				worst = 1
			}
			rows = append(rows, SetStatus{Loophole: s.Name, Declared: s.Declared, Status: st})
		}
		enc, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			fmt.Fprintf(d.Err, "host-daemon status: encoding the report failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(d.Out, string(enc))
		return worst
	}

	out := newPrinter(CLIDeps{Out: d.Out, Color: d.Color, IsTTYStdout: d.IsTTYStdout})
	if len(d.Set) == 0 {
		out.print("[dim]No host-wide daemon is declared on this machine, and none is running.[/dim]")
		out.print("  [dim]A loophole declares one with `host_daemon.scope: \"host\"`; " +
			"`yolo loopholes list` shows what this machine has.[/dim]")
		return 0
	}
	rc := 0
	for i, s := range d.Set {
		if i > 0 {
			out.print("")
		}
		deps := d.For(s)
		// Out/Color/Format are the SET's, not the member's: one report, one writer,
		// and a per-member Format would let a JSON request arrive here as prose.
		deps.Out, deps.Err = d.Out, d.Err
		deps.Color, deps.IsTTYStdout, deps.Format = d.Color, d.IsTTYStdout, ""
		if PrintStatus(deps) != 0 {
			rc = 1
		}
	}
	return rc
}

// ResolveSingleton turns a NAME into the record the command bodies act on: the
// derived set's entry when it has one, with THE CLAUDE BROKER'S OWN RECORD filling
// any gap for its own name. ok=false means the caller must refuse (see
// UnknownSingleton).
//
// THE GAP IS TWO STATES, not one, and the second is the one that bites. A name can
// be missing from the set entirely — a jail, a host whose `packs` list does not
// name claude, any process that resolved no packs — and a name can be PRESENT but
// unspawnable, which is what a rendezvous-derived member is: discovered from a PID
// file on disk, with no manifest behind it and therefore no argv. `yolo broker
// restart` resolving to that second record would refuse to restart the broker on
// precisely the machine where the broker is running, which is worse than the
// Claude-only surface this replaced.
//
// It is the ONE name with a built-in record, and that is what "alias" means here
// rather than a special case leaking back in: `yolo broker` promises a specific
// daemon, so it resolves to that daemon's own constants. Nothing else gets a
// fallback, because nothing else has one to get.
func ResolveSingleton(set []Singleton, name string) (Singleton, bool) {
	for _, s := range set {
		if s.Name != name {
			continue
		}
		if len(s.Argv) == 0 && name == BrokerLoopholeName {
			return BrokerSingleton(), true
		}
		return s, true
	}
	if name == BrokerLoopholeName {
		return BrokerSingleton(), true
	}
	return Singleton{}, false
}

// UnknownSingleton is the refusal for a name the derived set does not hold: it
// says which name, and lists the set rather than leaving the user to guess it.
//
// A LISTING, never a suggestion to "check the docs": the membership is derived per
// machine from what its packs declare, so this process is the only thing that can
// say what the set is.
func UnknownSingleton(name string, set []Singleton) string {
	msg := fmt.Sprintf("no host-wide daemon named %q is declared on this machine or running on it", name)
	if len(set) == 0 {
		return msg + "; nothing declares one here (`yolo loopholes list` shows this machine's loopholes)"
	}
	msg += "; known:"
	for _, s := range set {
		msg += " " + s.Name
	}
	return msg
}
