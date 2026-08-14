package run

// loopholeinert.go is the INERT REPORT: one line when a selected pack's loophole will do
// nothing this launch, whether the reason is the BACKEND or the PLATFORM
// (docs/design/loophole-packaging.md §8 item 2, §3.1).
//
// # This is the B-0 rule applied to a new kind
//
// run.go records B-0 as "a backend that looked provisioned and configured nothing", and the
// whole run pipeline was restructured to end it. Two shipped backends make the loophole kind
// a silent no-op, and both skips are WIDER than draft 1 of the design claimed:
//
//   - Apple Container: startLoopholes returns nil for rt == "container" BEFORE any external
//     service starts, so EVERY pack-shipped host daemon is skipped there, intercepting or not.
//     A different skip from the container-ARGS one draft 1 cited (loopholes/runtime.go's
//     `intercepts` check, which only drops --add-host).
//   - macos-user: the branch returns from Run() long before startLoopholes is reached, so the
//     kind is inert on that backend ENTIRELY. macos-user-nix-and-features.md already states
//     it; nothing printed it.
//
// So a pack whose whole purpose is a loophole could be installed, selected, and completely
// inert, with the jail reporting a successful launch.
//
// # ONE MECHANISM, TWO AXES
//
// §3.1 is explicit that the platform declaration and the inert-backend report share one
// mechanism, because platform (darwin vs linux) and backend (container, macos-user) are two
// axes with ONE answer shape: "this loophole does nothing here, and here is why." Two
// half-messages for one user-visible situation is how B-0 happened in the first place.
//
// The platform half is the PRODUCER's whole answer — loopholes.PlatformInertNotes, selection
// included, not just its rendering. That is a correction: for a batch this file did its own
// selection over its own manifest read while the producer had no callers, and the two
// disagreed about a duplicate (2 lines vs 1) and about a disabled loophole (1 line vs 0). One
// mechanism has to mean one selection, or "shares one mechanism" is a statement about the
// sentence shape rather than about the answer.

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// backendInertReason says why a backend runs NO loophole host service, or "" when it does.
//
// Both answers are measured, and both are wider than draft 1 of the design claimed:
//
//   - container (Apple Container): startLoopholes returns nil for rt == "container" before
//     any external service starts, so EVERY pack-shipped host daemon is skipped there,
//     intercepting or not. That is a different skip from the container-ARGS one
//     (loopholes/runtime.go's `intercepts` skip), which only drops --add-host.
//   - macos-user: the branch returns from Run() long before startLoopholes is reached, so
//     the kind is inert on that backend ENTIRELY. macos-user-nix-and-features.md already
//     states it; nothing printed it.
//
// This is the B-0 rule applied to a new kind — run.go records B-0 as "a backend that looked
// provisioned and configured nothing", and the pipeline was restructured to end it. A pack
// whose whole purpose is a loophole must not look installed on a backend that ignores it.
func backendInertReason(rt string) string {
	switch rt {
	case "container":
		return "inert on this backend — the Apple Container backend starts no loophole host " +
			"services (no socket bind-mount there), so nothing it declares runs this launch"
	case "macos-user":
		return "inert on this backend — the macos-user backend starts no loophole host " +
			"services; a native process already reaches the host directly, so the whole " +
			"mechanism is bypassed"
	}
	return ""
}

// notePackLoopholesInert prints ONE line per pack-shipped loophole that will do nothing this
// launch, naming the axis that made it inert.
//
// ONE MECHANISM, TWO AXES (§3.1, §8). Platform (`darwin` vs `linux`) and backend
// (`container`, `macos-user`) both answer "this loophole does nothing here, and here is
// why", and the design is explicit that splitting them would produce two half-messages for
// one user-visible situation.
//
// BACKEND BEATS PLATFORM when both apply, and that is not arbitrary: an inert backend
// starts no host service whatever the platform says, so the platform answer would be a
// second reason for one outcome. The line the user needs is the one they can act on — and
// on an inert backend that is "switch backends", not "get a different machine".
//
// The platform half is read through internal/loopholedecl (the schema's own
// PlatformsUnsupportedReason), never re-implemented here: two matchers over one declaration
// is how a report and a gate come to disagree. A manifest that will not parse prints
// nothing — the discovery layer's contract is warn-and-continue and it already warns, and a
// second complaint from the launch path about the same file would read as a second bug.
//
// ONE MECHANISM MEANS ONE SELECTION, NOT JUST ONE RENDERING. The rendering converged on
// InertNote.Line() a batch before the selection did, and the gap was measurable: this
// function walked pack loopholes itself and called a private platform reader, while
// loopholes.PlatformInertNotes — whose doc comment states the dedup and the disabled-skip as
// REQUIREMENTS — had zero production callers. Given one loophole declared twice the producer
// emitted ONE note and this report emitted TWO identical lines; given `enabled: false` plus a
// wrong platform the producer emitted ZERO and this report emitted one. Same shape, different
// answers, which is what "one mechanism" is supposed to make impossible.
//
// So the PLATFORM axis is now the producer's answer, whole: this function resolves each pack
// module to a loopholes.Loophole and hands the slice to PlatformInertNotes. The dedup and the
// disabled-skip are the same CODE, not the same intent.
//
// THE BACKEND AXIS KEEPS ITS OWN SELECTION, and the asymmetry is the design's: an inert
// backend is a statement about the LAUNCH ("nothing any of this runs here"), so it applies to
// a disabled loophole too — a pack whose whole purpose is a loophole must not look installed
// on a backend that ignores it (B-0). Only the platform axis is a per-loophole property the
// user's own switch can preempt.
func (o *Options) notePackLoopholesInert(rt string, packs []*packload.Pack) {
	if backend := backendInertReason(rt); backend != "" {
		o.printInertLines(backendInertLines(packs, backend))
		return
	}
	o.printInertLines(platformInertLines(packs))
}

// backendInertLines is one line per pack-shipped loophole on an inert backend.
//
// Deduped by (pack, loophole) for the same reason the platform producer dedups by name — one
// mistake, one line — but keyed on the pair, because this report's line is prefixed with the
// pack and two packs shipping one name is a distinct (and separately refused) situation.
//
// It does NOT read the manifests. A backend that starts no host services says nothing about
// what any manifest declares, so resolving them would make the answer depend on a file whose
// contents cannot change it — and an unreadable manifest would then silence a line that is
// true regardless.
func backendInertLines(packs []*packload.Pack, reason string) []string {
	var lines []string
	seen := map[string]bool{}
	for _, p := range packs {
		for _, lp := range packLoopholes(p) {
			key := lp.Pack + "\x00" + lp.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			lines = append(lines, inertLineFor(lp.Pack,
				loopholes.InertNote{Name: lp.Name, Axis: loopholes.AxisBackend, Reason: reason}))
		}
	}
	return lines
}

// platformInertLines is the PRODUCER's answer, prefixed with the pack that shipped each
// loophole.
//
// The pack name is prefixed here rather than folded into the note because it is a fact about
// THIS report's context (which selected pack shipped it) and not about the loophole —
// `yolo loopholes list` renders the same note for a hand-placed loophole that has no pack at
// all. The note→pack mapping is built from the resolved records, so a note the producer DROPS
// (a duplicate, a disabled loophole) contributes no line by construction rather than by this
// function also remembering to drop it.
func platformInertLines(packs []*packload.Pack) []string {
	var resolved []*loopholes.Loophole
	packOf := map[string]string{}
	for _, p := range packs {
		for _, decl := range packLoopholes(p) {
			lp := resolveInertLoophole(decl.Dir)
			if lp == nil {
				continue
			}
			if _, seen := packOf[lp.Name]; !seen {
				packOf[lp.Name] = decl.Pack
			}
			resolved = append(resolved, lp)
		}
	}
	var lines []string
	for _, note := range loopholes.PlatformInertNotes(resolved) {
		lines = append(lines, inertLineFor(packOf[note.Name], note))
	}
	return lines
}

// printInertLines emits the report, sorted, to stderr. Sorted because the lines are assembled
// from a map-free walk of two nested slices whose order is the config's, and a launch notice
// that reorders between launches for one unchanged config reads as churn.
func (o *Options) printInertLines(lines []string) {
	if len(lines) == 0 {
		return
	}
	sort.Strings(lines)
	out := o.pr(o.Stderr)
	for _, l := range lines {
		out.print("[yellow]" + l + "[/yellow]")
	}
}

// resolveInertLoophole loads one module dir as a resolved record for the platform producer,
// returning nil when it cannot be read.
//
// TOLERANT (loopholes.LoadLoophole → LoadDirTolerant), matching every other cross-version
// manifest read: a manifest carrying a key only a newer build knows must not make this report
// claim the loophole is fine, nor make it shout. An unreadable manifest yields nothing here —
// the discovery layer's contract is warn-and-continue and it already warns about the same
// file; a second complaint from the launch path would read as a second, unrelated bug.
//
// It goes through the loopholes package rather than decoding the manifest here, because the
// producer takes RECORDS and the record is where `enabled` and the evaluated `platforms`
// declaration both live. Reading the manifest directly is what let this report skip the
// disabled check: a *loopholedecl.Manifest answers "which platforms" and a *Loophole answers
// "is this loophole inert here", which is the question being asked.
func resolveInertLoophole(dir string) *loopholes.Loophole {
	lp, err := loopholes.LoadLoophole(dir)
	if err != nil {
		return nil
	}
	return lp
}

// inertLineFor renders what THIS report prints for one (pack, loophole, reason), so a test can
// match a whole line rather than guess at a substring.
//
// A function rather than a marker constant, because after the convergence onto
// loopholes.InertNote there is no single interesting substring left to key on: the two axes
// share only Line's fixed "loophole <name> is " prefix, and a marker taken from either axis's
// reason clause would silently stop matching the other — the drift the single rendering exists
// to prevent. Producing the exact line from the same inputs the report uses cannot drift at
// all.
func inertLineFor(pack string, note loopholes.InertNote) string {
	return pack + ": " + note.Line()
}
