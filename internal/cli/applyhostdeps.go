package cli

// applyhostdeps.go resolves a pack's `program` AND `requires` contributions into the invoking
// host's REAL dep state for `yolo host apply` (pack-host-management-plan.md Phase 8). It replaces
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
// A missing host dep does not fail `yolo host apply` YET, and `yolo check-deps` is still the
// verb that exits non-zero for a CI to gate on — but it is no longer merely informational:
// since the verdict block landed it reaches `state`, which the apply's survey counts and the
// verdict names, so a dry run says in one sentence that an --assert would not complete
// (docs/design/report-tiers.md §4.9). The exit code follows in that section's build step 5,
// with the install prompt and the fatal decline; OQ-RO7 scopes which kinds it covers.

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
}

// hostDepState is the three-way answer about one declared dependency, and THREE is the point:
// docs/design/report-tiers.md §4.9 point 6 rules that a dependency yolo could not probe is NOT
// missing — yolo may not call an environment unready on evidence it does not have — so "not
// probed" is a state of its own rather than a missing one with an excuse. The ordering is by
// consequence, so a survey deduplicating one binary across two packs keeps the worse answer.
type hostDepState int

const (
	depNotProbed hostDepState = iota
	depPresent
	depMissing
)

// state is the ONE authority for which of the three a contribution is in: depLines switches on
// it and the apply's survey counts it, so the words in the report and the counts in the verdict
// cannot disagree about a binary. They are the same question asked twice, and asking it twice
// was how a dep line came to be a fact that reached neither the exit code nor the roll-up.
func (h *hostDeps) state(c packdecl.Contribution) hostDepState {
	if c.Bin == "" {
		return depNotProbed
	}
	r, ok := h.byBin[c.Bin]
	switch {
	case !ok:
		return depNotProbed
	case r.Present:
		return depPresent
	default:
		return depMissing
	}
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
	reqs := packDepRequirements(p)
	if len(reqs) == 0 {
		return h
	}
	for _, r := range depcheck.Check(reqs) {
		h.byBin[r.Bin] = r
	}
	return h
}

// hostDepPreflight is the WHOLE RUN's dep probe: every configured pack's binaries, resolved
// once, BEFORE the first render (docs/design/report-tiers.md §4.9 point 1, §9 step 5).
//
// The probe used to run per pack INSIDE the render loop, and the position is not a detail. The
// next step makes a declined install fatal at the prompt, and a fatal that fires in the middle
// of the loop would leave the packs already visited WRITTEN and the rest not — so *"we cannot
// continue"* would not also mean *"nothing was written"*, which is the only form of that
// sentence worth saying. Only a pre-flight delivers both, and it has to land before the abort
// does or the abort inherits a half-applied home.
//
// THIS COMMIT CHANGES NO OUTPUT. The probe prints nothing and the report still names each
// binary at its own contribution, in the same order; what moved is WHEN the host is asked.
type hostDepPreflight struct {
	byPack map[*packload.Pack]*hostDeps
}

// hostDepProbe is the per-pack probe, behind a var so a test can observe WHEN the run asks the
// host rather than only what it learned. That is the property this step is about, and it is
// invisible in the report by construction — a hoisted probe and a probe in the loop produce
// byte-identical output, which is exactly why the refactor needs a seam to be testable at all.
var hostDepProbe = resolveHostDeps

// probeHostDeps takes the pre-flight over the resolved pack set.
//
// Every pack is probed, including the many that declare no dependency: resolveHostDeps returns
// early for those without shelling out, and probing unconditionally keeps this loop the same
// shape as the render loop it precedes — a filter here would be a second, quieter answer to
// "which packs have dependencies?" than the one the report gives.
func probeHostDeps(loaded []*packload.Pack) hostDepPreflight {
	pf := hostDepPreflight{byPack: make(map[*packload.Pack]*hostDeps, len(loaded))}
	for _, p := range loaded {
		pf.byPack[p] = hostDepProbe(p)
	}
	return pf
}

// of returns one pack's probed deps. Nil-safe in both directions — an empty pre-flight, or a
// pack it never saw — because the answer for an unprobed pack is "not probed", which
// hostDeps.state already produces for an empty table (§4.9 point 6: yolo may not call an
// environment unready on evidence it does not have).
func (pf hostDepPreflight) of(p *packload.Pack) *hostDeps {
	if h := pf.byPack[p]; h != nil {
		return h
	}
	return &hostDeps{byBin: map[string]depcheck.Result{}}
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

// hostDepFinding is everything one declared binary contributes to the report: the probed
// state, and — for a missing one — the REMEDY, which is now the tier-3 group's to state
// (docs/design/report-tiers.md §4.4, §9 step 4) rather than this line's.
//
// The split is the step. §4.4 groups a blocker by its remedy key, and for a dependency the key
// is the BINARY, across packs: two packs declaring `rg` are one missing dependency on one host
// with one install command, and printing the command under each declaration is the same
// remedy-per-emitter repetition the MCP line had three copies of. So the per-contribution line
// keeps what varies per contribution — which kind asked, which binary, present or missing —
// and the command, its package-manager alternative, and the reason there is none travel to the
// group through this struct.
type hostDepFinding struct {
	// State is the three-way answer (see hostDepState).
	State hostDepState
	// Remedy is the install command depcheck resolved for the detected manager, or "" when
	// nothing covers it. Empty for a present or unprobed dep, which need no remedy.
	Remedy string
	// Alt is the package-manager alternative, present only when Remedy is the tool's OWN
	// installer. Second rather than first because a first-party installer carries a
	// first-party updater while a distro package pins whatever that repo has.
	Alt string
	// NoRemedy is why a missing binary has none, when Remedy is empty. §4.4: a loss with no
	// remedy says so rather than borrowing a `⚠` it cannot cash.
	NoRemedy string
}

// finding resolves one dep contribution into the struct above. It is the ONE place the probe's
// answer becomes report material, so the line, the survey's counts and the group's remedy are
// three renderings of one fact rather than three readings of the probe.
func (h *hostDeps) finding(c packdecl.Contribution) hostDepFinding {
	f := hostDepFinding{State: h.state(c)}
	if f.State != depMissing {
		return f
	}
	r := h.byBin[c.Bin]
	if r.Remedy == "" {
		// Missing with no remedy is still missing. Reporting only the deps yolo can fix would
		// silently cap the list at whatever the pack declared hints for — the same
		// no-silent-skip rule the kind census enforces one level up.
		f.NoRemedy = noRemedyReason(c, r.Manager)
		return f
	}
	f.Remedy = r.Remedy
	if r.Fallback != "" {
		f.Alt = fmt.Sprintf("or via %s: %s", r.Manager, r.Fallback)
	}
	return f
}

// depLine is the per-contribution line for one dep, and there is always exactly one: the
// malformed cases report why they could not be probed rather than going quiet, which is the G1
// failure mode the caller's kind census exists to prevent.
//
// The kind is printed from the contribution rather than hardcoded, because `program` and
// `requires` share this reporting path and the difference matters to the reader: one means
// "yolo would install this into a jail", the other "this must already exist". Same probe,
// different claim.
func (h *hostDeps) depLine(c packdecl.Contribution) string {
	label := string(c.Kind)
	r := h.byBin[c.Bin]
	switch h.state(c) {
	case depNotProbed:
		if c.Bin == "" {
			// `bin` is required for both kinds, so there is nothing to probe — but "your
			// manifest is broken" is a better answer than silence.
			return fmt.Sprintf("  [yellow]%-10s[/yellow] declares no \"bin\" — nothing to "+
				"probe; `yolo pack lint` explains why", label)
		}
		// Defensive: DepRequirements returns every program/requires carrying a Bin, so this
		// is unreachable unless the two diverge. Report it rather than dropping the line.
		return fmt.Sprintf("  [yellow]%-10s[/yellow] [yellow]?[/yellow] %-16s not probed",
			label, c.Bin)
	case depPresent:
		return fmt.Sprintf("  [dim]%-10s[/dim] [green]✓[/green] %-16s present at %s",
			label, r.Bin, r.Path)
	}
	// MISSING, and nothing else: the remedy is the tier-3 group's, stated once per binary.
	return fmt.Sprintf("  [yellow]%-10s[/yellow] [red]✗[/red] %-16s MISSING", label, c.Bin)
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
