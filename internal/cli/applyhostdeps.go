package cli

// applyhostdeps.go resolves a pack's `program` contributions into the invoking host's REAL
// dep state for `apply --host` (pack-host-management-plan.md Phase 8). It replaces a static
// line — "install below jail is confirm-gated; not run by apply --host yet" — which was
// true and useless: the gate is real, but the line never said WHICH binary was missing or
// what would fix it, and that is the only part the user can act on today.
//
// The probe is internal/depcheck, the same checker `yolo check-deps` calls, over the same
// declared install_hints. That reuse is load-bearing rather than tidy: a second probe here
// would drift from check-deps on exactly the questions that decide the output — which
// package manager wins on this host, and what a missing-bin-with-no-hint means — and the
// design's rule for this data is ONE checker over ONE declared list (depcheck's package
// doc, env-manager plan OQ-8).
//
// What it deliberately does NOT do is install. Running the remedies is env-manager plan
// Phase 4.3, whose batched, elevation-class-grouped confirm UX (OQ-6/7/9) is its own
// increment; the report says so once so a reader does not mistake "reported" for "done".
// A missing host dep therefore does not fail `apply --host` either — the report is
// informational, and `yolo check-deps` is the verb that exits non-zero for a CI to gate on.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/depcheck"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostDeps is one pack's resolved host-dep state: probed once per pack, then consulted per
// program contribution. Per-pack rather than per-contribution so N programs cost one
// manager detection; keyed by bin rather than iterated so the report follows the manifest's
// order instead of depcheck's sorted-by-bin order.
type hostDeps struct {
	byBin map[string]depcheck.Result
	// remaining counts the program contributions not yet reported, and sawMissing whether
	// any of them was missing. Together they place the "apply installs nothing" note once,
	// after the LAST program line: the deferral is a property of the COMMAND, not of a dep,
	// so repeating it under every missing binary is noise and printing it under the first
	// one wedges it between two dep lines.
	remaining  int
	sawMissing bool
}

// resolveHostDeps probes the host for every binary this pack's program contributions
// declare. A pack with no program contributions probes nothing — depcheck.DetectManager
// shells out looking for apt/dnf/pacman/brew, and that cost should not be paid by the many
// packs that declare no host dep at all.
func resolveHostDeps(p *packload.Pack) *hostDeps {
	h := &hostDeps{byBin: map[string]depcheck.Result{}}
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindProgram {
			h.remaining++
		}
	}
	reqs := packDepRequirements(p)
	if len(reqs) == 0 {
		return h
	}
	for _, r := range depcheck.Check(reqs) {
		h.byBin[r.Bin] = r
	}
	return h
}

// packDepRequirements adapts one pack's declared DepRequirements to the shared checker's
// input. Extracted from check-deps' configuredDepRequirements (which now calls it) so both
// commands feed the same probe through the same adapter: two adapters would be two answers
// to "does a program with no install_hints count as a requirement", and the point of
// depcheck is that there is one.
func packDepRequirements(p *packload.Pack) []depcheck.Requirement {
	var reqs []depcheck.Requirement
	for _, d := range p.Decl.DepRequirements() {
		reqs = append(reqs, depcheck.Requirement{Bin: d.Bin, Hints: d.Hints})
	}
	return reqs
}

// lines returns the `apply --host` report line(s) for one program contribution: its
// resolved present/missing state, plus — on the pack's LAST program contribution, if any
// were missing — the note that apply only reports them. Always at least one line, including
// for the malformed cases: a declared kind that produces no output at all is the G1 failure
// mode the caller's census loop exists to prevent.
func (h *hostDeps) lines(c packdecl.Contribution) []string {
	h.remaining--
	out := []string{h.depLine(c)}
	// The note belongs to the whole program block, so it trails the last line of it — and
	// only when there is a deferred install to talk about.
	if h.remaining <= 0 && h.sawMissing {
		out = append(out, "    [dim]apply --host reports host deps; it installs nothing. The "+
			"confirm-gated install is env-manager plan Phase 4.3.[/dim]")
	}
	return out
}

// depLine is the one-line verdict for a program contribution, and it always produces a
// line: the malformed cases report why they could not be probed rather than going quiet.
func (h *hostDeps) depLine(c packdecl.Contribution) string {
	if c.Bin == "" {
		// `bin` is required for a program contribution, so there is nothing to probe — but
		// "your manifest is broken" is a better answer than silence.
		return "  [yellow]program[/yellow]    declares no \"bin\" — nothing to probe; " +
			"`yolo pack lint` explains why"
	}
	r, ok := h.byBin[c.Bin]
	if !ok {
		// Defensive: DepRequirements returns every program carrying a Bin, so this is
		// unreachable unless the two diverge. Report it rather than dropping the line.
		return fmt.Sprintf("  [yellow]program[/yellow]    [yellow]?[/yellow] %-16s not probed", c.Bin)
	}
	if r.Present {
		return fmt.Sprintf("  [dim]program[/dim]    [green]✓[/green] %-16s present at %s", r.Bin, r.Path)
	}
	h.sawMissing = true
	if r.Remedy != "" {
		return fmt.Sprintf("  [yellow]program[/yellow]    [red]✗[/red] %-16s MISSING → %s", r.Bin, r.Remedy)
	}
	// Missing with no remedy is still missing. Reporting only the deps yolo can fix would
	// silently cap the list at whatever the pack declared hints for — the same no-silent-skip
	// rule the caller's kind census enforces one level up.
	return fmt.Sprintf("  [yellow]program[/yellow]    [yellow]?[/yellow] %-16s MISSING, %s",
		r.Bin, noRemedyReason(c, r.Manager))
}

// noRemedyReason explains WHY a missing bin has no install line, which is three different
// situations the user acts on differently. Worth distinguishing because "no remedy to offer"
// alone reads as a yolo limitation in the one case where it is the pack's omission:
//
//   - hints exist but none for this host's manager → the pack author can add one
//   - no hints at all → nothing to add a manager to
//   - either, plus a `via` → the pack DOES know how to install this into a jail, and naming
//     that is honest where a flat "no remedy" would not be. Every shipped pack is this case
//     (they declare via npm/installer and no hints), so it is the common output. yolo still
//     will not run it: an `npm -g` or a curl-to-shell against a real toolchain is precisely
//     the elevation question env-manager Phase 4.3's confirm owns.
func noRemedyReason(c packdecl.Contribution, mgr string) string {
	var reason string
	if len(c.InstallHints) > 0 {
		reason = fmt.Sprintf("install_hints cover %s but not %s (this host's manager)",
			strings.Join(sortedHintManagers(c.InstallHints), "/"), mgr)
	} else {
		reason = fmt.Sprintf("the pack declares no install_hints, so there is nothing to run for %s", mgr)
	}
	if c.Via != "" {
		reason += fmt.Sprintf("; its JAIL install (via %s) is not run against a real host", c.Via)
	}
	return reason
}

// sortedHintManagers keeps the hint list deterministic — this line is compared in tests and
// read by humans, and Go's map order is neither.
func sortedHintManagers(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
