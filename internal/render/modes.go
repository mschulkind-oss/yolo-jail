package render

import "github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"

// censusModes is the mode set every ModeSet below has to answer for — manifest's closed
// Mode* taxonomy, restated here because that one is unexported. The count is asserted in
// modes_test.go: if manifest grows a fifth mechanism, each census entry needs a decision
// for it rather than a silent absence, and the count assertion is what says so.
var censusModes = []string{
	manifest.ModeStateful, manifest.ModeComputed, manifest.ModeRMW, manifest.ModeUnrendered,
}

// ModeSet declares which of the ENGINE MECHANISMS (manifest.Mode*) a target actually runs,
// and which of those keep a provenance record. It is the mode-side counterpart of FieldSet:
// a per-target census expressed as data with a reason for what it leaves out, so a new notch
// STATES its answer instead of inheriting whichever branch of a runtime `if` it falls into.
//
// WHAT IT REPLACES, and why an `if` was the wrong shape. The branch was
// `if target.KindOf() == render.KindHost { writeProvenanceRecord(…) }` in the rmw writer, and
// its reasoning was correct for the two notches that existed: in a jail `rmw` is one mode
// among four and the surfaces that matter are `stateful`, which do record — so an absent
// record on a jail rmw surface is a legible statement (`config diff` prints exactly that) —
// while at the host `rmw` is the ONLY mode, so "rmw records nothing" degenerates into "the
// host records nothing", which is the gap that let `config diff` report an overlay as having
// LOST a key it in fact won. What rotted was not the conditional but the fact underneath it:
// WHICH MODES A NOTCH RUNS was written down nowhere, so a third notch could only inherit an
// answer. Stating it makes the host's provenance write a consequence of the census rather
// than a special case naming one Kind.
//
// AND THE ORIGINAL JUSTIFICATION FOR `stateful` WAS WRONG, which is why the census had to be
// restated rather than merely relocated (plan §6b D2). `stateful` was justified as
// jail-shaped "because a jail home is disposable, so an edit must survive --rm". The jail home
// is NOT disposable: it is bind-mounted from paths.GlobalHome(), and the sidecars live under
// <workspace>/.yolo/prism/ with the workspace a live host bind — both persist across
// containers. The real reason `stateful` exists is that the destination inside a jail is an
// artifact yolo REGENERATES every boot, so an in-place edit is lost at the next render unless
// it is captured first. That is a fact about regeneration, not about disposability, and it
// therefore applies to any notch where yolo regenerates a file the agent may edit — INCLUDING
// `guest`, whose home is real and non-disposable and which yolo nonetheless provisions. A
// guest census inheriting the disposability reasoning would have ruled `stateful` out for a
// reason that was never true of any notch.
type ModeSet struct {
	// runs is the mechanisms this target executes. A declared mode absent from it is one the
	// target reaches by some other route (the host coerces every surface to rmw) or not at all.
	runs map[string]bool
	// records is the subset of runs that persists a provenance record. Always a subset —
	// asserted in modes_test.go, since "records a mode it never runs" is nonsense a map cannot
	// prevent by itself.
	records map[string]bool
	// excluded is the reason per mode this target does not both run AND record, in whichever
	// of the two senses applies to that mode here. One map rather than two because for a given
	// (target, mode) pair exactly one of "not run" and "run without a record" can hold.
	excluded map[string]string
	// undecided marks a notch whose mode policy nobody has stated yet — distinct from a notch
	// that has stated an empty one. Read Undecided() for what a caller does with it.
	undecided bool
}

// Runs reports whether this target executes the named mechanism.
func (m ModeSet) Runs(mode string) bool { return m.runs[mode] }

// Records reports whether a render through this mechanism at this target persists a
// provenance record. This is the question the rmw writer asks, and it is the whole reason
// the census exists as data: `Records(ModeRMW)` is true at the host because rmw is the only
// mode there, and false in a jail because `stateful` carries the recording duty.
func (m ModeSet) Records(mode string) bool { return m.records[mode] }

// Undecided reports that this target's mode policy has not been stated — the `guest` notch
// until Phase 7 states it, and any Kind with no census entry. An undecided ModeSet runs
// nothing and records nothing, so it is the fail-closed answer at every call site: a notch
// nobody has thought about writes no file and claims no attribution, rather than inheriting
// the jail's four mechanisms or the host's provenance write.
func (m ModeSet) Undecided() bool { return m.undecided }

// Excludes is the ModeSet's Refuse: a one-line reason this target does not run the mode, or
// runs it without keeping a record — and "" when it does both, meaning the caller should not
// have asked. The reasons are the census's own, so the message says why rather than that.
func (m ModeSet) Excludes(mode string) string {
	if m.runs[mode] && m.records[mode] {
		return ""
	}
	if r, ok := m.excluded[mode]; ok {
		return r
	}
	return mode + " has no stated meaning at this confinement level"
}

// JailModes is the in-jail boot census: every mechanism runs, and `stateful` is the one that
// records.
//
// The exclusions are the interesting half. Each names a mechanism that HAS no record rather
// than one that lost one, and `config diff` already prints that distinction — which is what
// makes an absent jail sidecar readable instead of alarming.
func JailModes() ModeSet {
	return ModeSet{
		runs: map[string]bool{
			manifest.ModeStateful: true, manifest.ModeComputed: true,
			manifest.ModeRMW: true, manifest.ModeUnrendered: true,
		},
		records: map[string]bool{manifest.ModeStateful: true},
		excluded: map[string]string{
			// The reason is REGENERATION, not disposability — see the type comment. `stateful`
			// exists here because yolo rewrites the destination every boot, so an in-place edit
			// needs capturing first; and because it is available, an rmw surface's missing record
			// is one mode's documented silence rather than the notch's.
			manifest.ModeRMW: "in a jail `rmw` is one mechanism among four and `stateful` — the " +
				"mode that captures edits to a file yolo regenerates every boot — is the one that " +
				"records, so an absent record here means \"this surface's mode keeps no sidecar\", " +
				"not \"this notch records nothing\" (pack-config-collaboration.md §8)",
			manifest.ModeComputed: "`computed` is the stateless render: it writes the surface file " +
				"and no sidecars at all, discarding in-jail edits by declaration, so there is no " +
				"captured divergence for a record to attribute",
			// Present in `runs` on purpose: both notches HONOR an unrendered declaration, and
			// honoring it is writing nothing. Absent from `runs` it would read as "this target
			// cannot express an unrendered surface", which is false.
			manifest.ModeUnrendered: "yolo does not write the file at all, so there is no render " +
				"to attribute a key to",
		},
	}
}

// HostModes is the `yolo apply --host` census: rmw is the ONLY composing mechanism, and it
// therefore records.
//
// The coercion is the resolved decision (OQ-4, host-render-target.md §6.3), not an omission:
// a real home holds the agent's own keys, so every surface is read-modify-written and nothing
// is regenerated from layers alone. Writing it down here is what turns the provenance write
// from "the host is special" into "this notch's only mode is its recording mode".
func HostModes() ModeSet {
	return ModeSet{
		runs:    map[string]bool{manifest.ModeRMW: true, manifest.ModeUnrendered: true},
		records: map[string]bool{manifest.ModeRMW: true},
		excluded: map[string]string{
			manifest.ModeStateful: "a host render is pure read-modify-write (OQ-4): a real home " +
				"holds the agent's own keys, so a surface declaring `stateful` is rendered through " +
				"`rmw` here — there is no regenerated-every-boot artifact to keep a capture " +
				"baseline against",
			manifest.ModeComputed: "`computed` overwrites from layers, which off-container would " +
				"discard keys the user owns; a surface declaring it is rendered through `rmw` here",
			manifest.ModeUnrendered: "yolo does not write the file at all, so there is no render " +
				"to attribute a key to",
		},
	}
}

// UndecidedModes is the census for a notch whose mode policy is not stated yet: nothing runs,
// nothing records, and the reason travels with it so a caller that hits one can say which
// notch is missing an answer rather than printing an empty set.
func UndecidedModes(reason string) ModeSet {
	return ModeSet{undecided: true, excluded: undecidedReasons(reason)}
}

// undecidedReasons gives every mode the same reason, so Excludes answers for all four rather
// than falling through to the generic sentence for a notch that has a specific one.
func undecidedReasons(reason string) map[string]string {
	out := make(map[string]string, len(censusModes))
	for _, mode := range censusModes {
		out[mode] = reason
	}
	return out
}

// modeCensus is the notch → mode-policy table: the ONE place that answers "which mechanisms
// does this confinement level run, and which of them record?", the way ProfileFor answers
// "what does this level imply?" and Fields answers "which contribution kinds apply?".
//
// A MAP with a drift test rather than a switch, because the guest entry has to be a VISIBLE
// hole. Go cannot make a missing enum case a compile error, so the forcing function is the
// same one packdecl uses for its closed kind set: modes_test.go names every Kind and asserts
// the count, so a Kind added without a census entry fails a test that tells its author what to
// write. The fail-closed default in Modes() covers the window in between.
var modeCensus = map[Kind]ModeSet{
	KindJail: JailModes(),
	// A preview shows what the JAIL render produces (that is what `yolo config render` is for),
	// so it carries the jail's census — previewing a different mode set would print a file the
	// jail never writes. Same reasoning ProfileFor uses for the preview's autonomy bit.
	KindPreview: JailModes(),
	KindHost:    HostModes(),
	// GUEST IS DELIBERATELY UNSTATED (env-manager plan Phase 7, Mac-gated). Both of the
	// answers above are mechanically available to it — a guest home is a real home that yolo
	// nonetheless provisions, so `stateful`'s regeneration premise HOLDS there while the host's
	// pure-rmw coercion is equally defensible — and that is precisely why it must not be
	// defaulted into one. Inventing semantics here would re-commit D2's error in the file that
	// exists to prevent it.
	KindGuest: UndecidedModes("the `guest` notch's mode policy is Phase 7's to state: yolo " +
		"provisions a real home there, so `stateful`'s regenerate-every-boot premise applies " +
		"while the host's pure-rmw coercion is equally defensible — neither may be inherited"),
	// KindUnset is not a notch at all (see its doc): a Target nobody's constructor built has
	// chosen no confinement level, so it runs nothing.
	KindUnset: UndecidedModes("this target's confinement level was never set by a constructor, " +
		"so it has no mode policy — see render.KindUnset"),
}

// Modes returns this target's mode census. A Kind with no entry gets the undecided set, which
// is the fail-closed direction and the same asymmetry ProfileFor argues: a mode wrongly not
// run writes no file, while a mode wrongly run writes one nobody asked for.
func (t Target) Modes() ModeSet {
	if m, ok := modeCensus[t.KindOf()]; ok {
		return m
	}
	return UndecidedModes("no mode census has been stated for this confinement level")
}
