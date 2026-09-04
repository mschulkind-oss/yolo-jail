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
	"strings"

	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
func (o *Options) notePackLoopholesInert(rt string, packs []*packload.Pack, cfg *jsonx.OrderedMap) {
	if backend := backendInertReason(rt); backend != "" {
		// CONFIG-DECLARED loopholes are inert on these backends too, and this report
		// walked packs only — so a user whose `loopholes.<name>.command` names a daemon
		// got no line at all. The AC skip drops both sources (SourcePack and
		// SourceConfig); reporting one of them made the silence look deliberate.
		o.printInertLines(append(backendInertLines(packs, backend), configInertLines(cfg, backend)...))
		return
	}
	o.printInertLines(platformInertLines(packs))
}

// configInertLines is one line per `loopholes.<name>` entry in the user's own config on a
// backend that starts none of them — the SourceConfig half of the same report.
//
// Keyed on presence rather than on Active(): resolving whether a config-declared daemon
// would have run requires probing its `requires`, and on a backend that starts nothing the
// answer cannot change the outcome. Same reasoning backendInertLines gives for not reading
// pack manifests.
func configInertLines(cfg *jsonx.OrderedMap, reason string) []string {
	section := cfgMap(cfg, "loopholes")
	if section == nil {
		return nil
	}
	var lines []string
	for _, name := range section.Keys() {
		lines = append(lines, inertLineFor("your config", loopholes.InertNote{
			Name: name, Axis: loopholes.AxisBackend, Reason: reason,
		}))
	}
	return lines
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

// noteMachineWideWorkspaceState prints one line naming the pack `state` dirs that are
// per-workspace on every other backend and machine-wide on macos-user, because that
// backend's home is a constant.
//
// One line for the whole set, matching the inert-loophole report beside it rather than
// one line per dir: this prints on every launch, and five dirs of noise trains a reader
// to skip the line that matters.
func (o *Options) noteMachineWideWorkspaceState(packs []*packload.Pack) {
	dirs := packload.WritableDirs(packs)
	if len(dirs) == 0 {
		return
	}
	o.pr(o.Stderr).print("[yellow]Note: these are shared across ALL workspaces on macos-user[/yellow] — " +
		strings.Join(dirs, ", ") + ". Every other backend gives each workspace its own copy; " +
		"this backend has one home (/Users/_yolojail) and no mounts, so a session's history " +
		"and state are visible to every other workspace you launch.")
}

// noteMacosUserContentGaps names the two content pipelines that never reach this
// backend. Both are host-side steps inside runContainer, which the macos-user arm
// returns before — the same B-0 shape as pack staging and launch flags, but with a
// fix that is a delivery mechanism rather than a moved call, so it warns for now.
//
// SKILLS AND BRIEFINGS ARE NOW DELIVERED (2026-09-03), by composing the same trees the
// container path composes and copying them over the sandbox home instead of mounting
// them (macoshomeoverlay.go). What survives is a DIFFERENT and smaller statement, and
// this function now makes it: the copy is writable where a bind is `:ro`, and the
// destination home is machine-wide, so a second workspace launching concurrently
// overwrites the first's briefings while its agent is mid-session. Both are consequences
// of the single sandbox home, which docs/design/macos-user-home-tiers.md exists to fix.
//
// The text this replaced said the agent "starts with no AGENTS.md/CLAUDE.md and no
// skills". Leaving it would be the worse failure of the two available: a warning that
// describes a gap yolo has closed teaches the reader to distrust the warnings that are
// still true.
func (o *Options) noteMacosUserContentGaps(packs []*packload.Pack, cfg *jsonx.OrderedMap) {
	if len(packs) > 0 {
		o.pr(o.Stderr).print("[yellow]Note: briefings and skills are delivered by COPY on macos-user[/yellow] — " +
			"every other backend mounts them read-only, so here the agent can edit its own " +
			"skills and briefing (the next launch overwrites them), and because this backend " +
			"has ONE home, a second workspace launched while this one runs replaces them with " +
			"its own.")
	}
	// mise_tools has the SAME defect as lsp_servers below and, until 2026-09-04, none
	// of the same warning — so a config declaring tools got silence and a jail without
	// them. Three independent reasons it cannot work here, all verified on a Mac:
	// nothing provides a `mise` binary the sandbox can reach (no image bakes one, and
	// /opt/homebrew/bin is not on SandboxPath — nor would the host's own mise state
	// under /Users be readable if it were); nothing runs `mise install`, which lives in
	// the CONTAINER command wrapper (setupScript) that this backend never invokes; and
	// the sandbox home has no mise data dir at all.
	//
	// Warned rather than fixed because the fix is a real decision — bake mise, or
	// install it, or declare the tools in `packages:` instead — and shipping the
	// warning is what makes the choice visible instead of the absence.
	if mise := cfgMap(cfg, "mise_tools"); mise != nil && len(mise.Keys()) > 0 {
		o.pr(o.Stderr).print("[yellow]Warning: mise_tools are NOT installed on macos-user[/yellow] — " +
			strings.Join(mise.Keys(), ", ") + ". The mise shims dir is on PATH but nothing " +
			"provides `mise` here and nothing runs `mise install`: that step is part of the " +
			"container provisioning script this backend does not run. Declare these in " +
			"`packages:` instead, which this backend DOES materialize natively.")
	}
	if lsp := cfgMap(cfg, "lsp_servers"); lsp != nil && len(lsp.Keys()) > 0 {
		o.pr(o.Stderr).print("[yellow]Warning: lsp_servers CONFIG renders but the binaries are not installed on macos-user[/yellow] — " +
			strings.Join(lsp.Keys(), ", ") + ". The installer is a generated bootstrap script the " +
			"container path runs and this backend deliberately does not, so an agent is told the " +
			"server is enabled and then cannot start it. Install them yourself, or add them to `packages`.")
	}
	o.noteMacosUserHostByteGaps(packs, cfg)
}

// noteMacosUserHostByteGaps names the grants that carry HOST BYTES into a config
// surface, every one of which is inert here for the same structural reason: the bytes
// cross on a /ctx mount, and this backend has no mounts.
//
// The two halves fail differently, and the difference is worth stating because only
// one of them is honest. A `reads-host` grant renders anyway, from its DEFAULTS layer
// — so the agent runs on a settings file the user did not write and has no way to
// distinguish from one they did. A source-bearing `host_files` entry is filtered out
// of the wire before the bootstrap sees it (macosuser/runplan.go), which at least
// leaves nothing rather than a plausible substitute.
//
// Both are warned rather than fixed: the fix is a delivery mechanism (materialize into
// the sandbox home, the way Apple Container's .yolo-ctx copies work), which is a design
// change and not a launch-time patch. What is NOT acceptable is the prior state, where
// a user pointed at a host file by path and the jail neither used it nor mentioned it.
func (o *Options) noteMacosUserHostByteGaps(packs []*packload.Pack, cfg *jsonx.OrderedMap) {
	var grants []string
	for _, p := range packs {
		granted, _ := p.HonoredHostFiles()
		for _, hf := range granted {
			grants = append(grants, p.Name+": ~/"+hf.From)
		}
	}
	if len(grants) > 0 {
		o.pr(o.Stderr).print("[yellow]Warning: pack reads-host grants do not cross on macos-user[/yellow] — " +
			strings.Join(grants, ", ") + ". The bytes arrive on a /ctx mount and this backend has " +
			"none, so each surface renders from its DEFAULTS layer instead. The agent gets a " +
			"working config file that is not yours.")
	}

	// Read fail-open: LoadHostFiles is the same call the container path makes, and a
	// malformed user config is already reported there. This is a report, not a gate.
	entries, err := config.LoadHostFiles(cfg, nil, false)
	if err != nil {
		return
	}
	var named []string
	for _, e := range entries {
		if e.SourceBearing() {
			named = append(named, "~/"+e.Path)
		}
	}
	if len(named) > 0 {
		o.pr(o.Stderr).print("[yellow]Warning: host_files entries with a `source` are dropped on macos-user[/yellow] — " +
			strings.Join(named, ", ") + ". There is no /ctx/host-user mount to carry the host " +
			"bytes, so these entries are filtered out of the launch entirely and no file appears " +
			"at those paths. Entries with `content`/`defaults` and no `source` are unaffected.")
	}
}
