package cli

// applyhostdeps.go resolves a pack's `program` AND `requires` contributions into the invoking
// host's REAL dep state for `apply --host` (pack-host-management-plan.md Phase 8). It replaces
// a static line — "install below jail is confirm-gated; not run by apply --host yet" — which
// was true and useless: the gate is real, but the line never said WHICH binary was missing or
// what would fix it, and that is the only part the user can act on today.
//
// Both kinds share this path because below the jail notch they ask the host the same question
// (yolo bakes no image there, so every dep is the host's); they differ in what they do to a
// JAIL, where `program` gets a launcher and `requires` gets an assertion. The report names
// which kind asked, since "yolo would install this" and "this must already exist" send the
// reader to different places.
//
// The probe is internal/depcheck, the same checker `yolo check-deps` calls, over the same
// declared install_hints. That reuse is load-bearing rather than tidy: a second probe here
// would drift from check-deps on exactly the questions that decide the output — which
// package manager wins on this host, which remedy leads (the pack's own installer, or a
// package-manager hint), and what a missing-bin-with-no-hint means — and the design's rule
// for this data is ONE checker over ONE declared list (depcheck's package doc, env-manager
// plan OQ-8).
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
// program/requires contribution. Per-pack rather than per-contribution so N deps cost one
// manager detection; keyed by bin rather than iterated so the report follows the manifest's
// order instead of depcheck's sorted-by-bin order.
type hostDeps struct {
	byBin map[string]depcheck.Result
	// remaining counts the dep contributions not yet reported, and sawMissing whether
	// any of them was missing. Together they place the "apply installs nothing" note once,
	// after the LAST dep line: the deferral is a property of the COMMAND, not of a dep,
	// so repeating it under every missing binary is noise and printing it under the first
	// one wedges it between two dep lines.
	remaining  int
	sawMissing bool
}

// isDepKind reports whether a contribution feeds the host dep probe. Both `program` and
// `requires` do, and deliberately: below the jail notch yolo bakes no image, so "yolo would
// install this" and "this must already exist" are the same question about the host. They
// differ in what they do to a JAIL, not in what they ask of a host — which is why
// DepRequirements folds them together and this predicate has to as well, or a `requires`
// would be counted for the report and not probed (or vice versa).
func isDepKind(k packdecl.Kind) bool {
	return k == packdecl.KindProgram || k == packdecl.KindRequires
}

// resolveHostDeps probes the host for every binary this pack's program/requires
// contributions declare. A pack with neither probes nothing — depcheck.DetectManager
// shells out looking for apt/dnf/pacman/brew, and that cost should not be paid by the many
// packs that declare no host dep at all.
func resolveHostDeps(p *packload.Pack) *hostDeps {
	h := &hostDeps{byBin: map[string]depcheck.Result{}}
	for _, c := range p.Decl.Contributions() {
		if isDepKind(c.Kind) {
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
		reqs = append(reqs, depcheck.Requirement{
			Bin: d.Bin, Hints: d.Hints,
			// The pack's OWN installer, derived from the program contribution it already
			// declares. depcheck prefers it over a package-manager hint.
			SelfInstall: d.SelfInstall,
		})
	}
	return reqs
}

// lines returns the `apply --host` report line(s) for one program/requires contribution: its
// resolved present/missing state, plus — on the pack's LAST dep contribution, if any were
// missing — the note that apply only reports them. Always at least one line, including
// for the malformed cases: a declared kind that produces no output at all is the G1 failure
// mode the caller's census loop exists to prevent.
func (h *hostDeps) lines(c packdecl.Contribution) []string {
	h.remaining--
	out := h.depLines(c)
	// The note belongs to the whole dep block, so it trails the last line of it — and
	// only when there is a deferred install to talk about.
	if h.remaining <= 0 && h.sawMissing {
		out = append(out, "    [dim]apply --host reports host deps; it installs nothing. The "+
			"confirm-gated install is env-manager plan Phase 4.3.[/dim]")
	}
	return out
}

// depLines is the verdict for one dep contribution, and it always produces at least one
// line: the malformed cases report why they could not be probed rather than going quiet. A
// second line appears only for the package-manager alternative to a pack's own installer.
//
// The kind is printed from the contribution rather than hardcoded, because `program` and
// `requires` share this reporting path and the difference matters to the reader: one means
// "yolo would install this into a jail", the other "this must already exist". Same probe,
// different claim.
func (h *hostDeps) depLines(c packdecl.Contribution) []string {
	label := string(c.Kind)
	if c.Bin == "" {
		// `bin` is required for both kinds, so there is nothing to probe — but "your
		// manifest is broken" is a better answer than silence.
		return []string{fmt.Sprintf("  [yellow]%-10s[/yellow] declares no \"bin\" — nothing to "+
			"probe; `yolo pack lint` explains why", label)}
	}
	r, ok := h.byBin[c.Bin]
	if !ok {
		// Defensive: DepRequirements returns every program/requires carrying a Bin, so this
		// is unreachable unless the two diverge. Report it rather than dropping the line.
		return []string{fmt.Sprintf("  [yellow]%-10s[/yellow] [yellow]?[/yellow] %-16s not probed",
			label, c.Bin)}
	}
	if r.Present {
		return []string{fmt.Sprintf("  [dim]%-10s[/dim] [green]✓[/green] %-16s present at %s",
			label, r.Bin, r.Path)}
	}
	h.sawMissing = true
	if r.Remedy != "" {
		out := []string{fmt.Sprintf("  [yellow]%-10s[/yellow] [red]✗[/red] %-16s MISSING → %s",
			label, r.Bin, r.Remedy)}
		// The package-manager alternative, when the primary remedy is the tool's OWN
		// installer. Second, not first: a first-party installer carries a first-party
		// updater, while a distro package pins whatever that repo has (github-copilot-cli
		// was 16 nixpkgs releases behind when this was measured).
		if r.Fallback != "" {
			out = append(out, fmt.Sprintf("    [dim]or via %s: %s[/dim]", r.Manager, r.Fallback))
		}
		return out
	}
	// Missing with no remedy is still missing. Reporting only the deps yolo can fix would
	// silently cap the list at whatever the pack declared hints for — the same no-silent-skip
	// rule the caller's kind census enforces one level up.
	return []string{fmt.Sprintf("  [yellow]%-10s[/yellow] [yellow]?[/yellow] %-16s MISSING, %s",
		label, r.Bin, noRemedyReason(c, r.Manager))}
}

// noRemedyReason explains WHY a missing bin has no install line, which is two different
// situations the user acts on differently. Worth distinguishing because "no remedy to offer"
// alone reads as a yolo limitation in the one case where it is the pack's omission:
//
//   - hints exist but none for this host's manager → the pack author can add one
//   - no hints at all → nothing to add a manager to
//
// A `via` used to be mentioned here as "the pack DOES know how to install this into a jail,
// but yolo will not run it against a real host" — the common case, since every shipped pack
// declared a via and no matching hint. It is GONE from this path because it no longer
// reaches it: a well-formed `program` now derives its remedy FROM that very via (the tool's
// own installer, preferred over a package-manager hint — see depcheck's selfInstallFlavor),
// so having a via means having a remedy. What is left here is a `requires` (which installs
// nothing by definition) or a program whose via/package is malformed, and `pack lint` is the
// verb for the second.
func noRemedyReason(c packdecl.Contribution, mgr string) string {
	if len(c.InstallHints) > 0 {
		return fmt.Sprintf("install_hints cover %s but not %s (this host's manager)",
			strings.Join(sortedHintManagers(c.InstallHints), "/"), mgr)
	}
	if c.Kind == packdecl.KindRequires {
		return fmt.Sprintf("the pack declares no install_hints for it, and a `requires` "+
			"binary is never installed by yolo — install it yourself for %s", mgr)
	}
	return fmt.Sprintf("the pack declares no install_hints, so there is nothing to run for %s", mgr)
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
