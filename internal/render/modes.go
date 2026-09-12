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
	// target reaches by some other route (under `host_management: assert` the host coerces
	// every composing surface to rmw) or not at all.
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
// the census exists as data: `Records(ModeRMW)` is true at the host — under `assert` because
// rmw is the only mode there, under `own` because the record is what `--revert` consumes for
// every surface — and false in a jail, where `stateful` carries the recording duty.
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

// HostAssertModes is the `host_management: assert` census — today's shipped host apply: rmw
// is the ONLY composing mechanism, and it therefore records.
//
// The coercion is the resolved decision (OQ-4, host-render-target.md §6.3) SCOPED TO THIS
// CONTRACT, which is the correction config-ownership-and-promotion.md §3 makes: under
// `assert` the file is the user's and holds the agent's own keys, so every surface is
// read-modify-written and nothing is regenerated from layers alone. Writing it down here is
// what turns the provenance write from "the host is special" into "this contract's only mode
// is its recording mode".
func HostAssertModes() ModeSet {
	return ModeSet{
		runs:    map[string]bool{manifest.ModeRMW: true, manifest.ModeUnrendered: true},
		records: map[string]bool{manifest.ModeRMW: true},
		excluded: map[string]string{
			manifest.ModeStateful: "under `host_management: assert` a host render is pure " +
				"read-modify-write (OQ-4): the file is the user's and holds the agent's own keys, " +
				"so a surface declaring `stateful` is rendered through `rmw` here — there is no " +
				"regenerated artifact to keep a capture baseline against. Declare " +
				"`host_management: own` to have yolo compose the whole file and capture edits",
			manifest.ModeComputed: "`computed` overwrites from layers, which off-container would " +
				"discard keys the user owns; a surface declaring it is rendered through `rmw` here",
			manifest.ModeUnrendered: "yolo does not write the file at all, so there is no render " +
				"to attribute a key to",
		},
	}
}

// HostOwnedModes is the `host_management: own` census: the user has declared these files
// DERIVED OUTPUT, so `stateful` runs — whole-file composition with a capture overlay, the
// jail's own mechanism at a real home — and it records, exactly as it does in a jail.
//
// THREE THINGS IT DELIBERATELY DOES NOT SAY, each of which was available and wrong:
//
//   - It does NOT coerce `rmw` to `stateful`. A pack declares `rmw` for a surface holding
//     live agent state — credentials, sessions — where "regenerate from layers" describes
//     the wrong operation and composition would put a secret on the capture path
//     (manifest.ModeRMW's own doc). `own` is the USER's statement about who owns the file;
//     it is not a licence to overrule the PACK's statement about what kind of file it is.
//     So rmw runs here too, which also means this notch coerces nothing at all — Mechanism's
//     fallback needs a SOLE composing mechanism, and this census has two.
//   - It does NOT run `computed`. A computed surface keeps no capture overlay, so it has no
//     adoption path: the first owned render would replace a real file the user has never had
//     yolo touch, wholesale, with nothing to recover it from. That is the same class OQ-CO9
//     refuses for a keyless surface, and the refusal is the fail-closed answer until a real
//     example argues otherwise. Under `assert` such a surface renders through rmw and is
//     untouched by this.
//   - It does NOT stop recording `rmw`. The provenance record is the only per-home mark yolo
//     leaves at this notch and `yolo host apply --revert` consumes it for every surface, so
//     the recording duty here is the NOTCH's rather than one mode's — which is why a jail's
//     reason for not recording rmw ("`stateful` is available and carries it") does not
//     transfer even though `stateful` is now available here too.
func HostOwnedModes() ModeSet {
	return ModeSet{
		runs: map[string]bool{
			manifest.ModeStateful: true, manifest.ModeRMW: true, manifest.ModeUnrendered: true,
		},
		records: map[string]bool{manifest.ModeStateful: true, manifest.ModeRMW: true},
		excluded: map[string]string{
			manifest.ModeComputed: "`computed` composes the whole file and keeps no capture " +
				"overlay, so it has no adoption path: the first owned render would replace a real " +
				"file wholesale with nothing to recover it from. `own` refuses it rather than " +
				"running it, on OQ-CO9's reasoning — set `host_management: assert` to have this " +
				"surface rendered through `rmw` instead",
			manifest.ModeUnrendered: "yolo does not write the file at all, so there is no render " +
				"to attribute a key to",
		},
	}
}

// HostUnmanagedModes is the `host_management: none` census: the user owns their agents'
// config files entirely, so nothing composes and nothing records.
//
// `unrendered` runs, for the reason JailModes gives: both notches HONOR an unrendered
// declaration, and honoring it is writing nothing. Everything else is excluded, which makes
// Mechanism answer "not decided" for every writing mode — the fail-closed answer a render
// entry must read as "do not render this surface here". `yolo host apply` refuses above this
// (hostManagementRefusal) so the census is the second line rather than the first, and it is
// deliberately still a full statement: a refusal that exists in one command is not a policy.
func HostUnmanagedModes() ModeSet {
	const why = "`host_management` is \"none\", which declares your agents' config files " +
		"yours entirely — so the host notch composes nothing"
	return ModeSet{
		runs:    map[string]bool{manifest.ModeUnrendered: true},
		records: map[string]bool{},
		excluded: map[string]string{
			manifest.ModeStateful: why,
			manifest.ModeComputed: why,
			manifest.ModeRMW:      why,
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

// notch is the census KEY: the pair a target's mode policy is a function of. It was the Kind
// alone until `own`, and the widening is the point rather than a complication — "which
// mechanisms does the host run?" has three answers and the USER picks one
// (config-ownership-and-promotion.md §4.1), so a table keyed on Kind could only have held one
// of them and inferred the rest. It stays ENUMERABLE, which is what keeps the drift test
// (modes_test.go's allNotches) able to name every entry the table owes an answer for.
type notch struct {
	kind      Kind
	ownership HostOwnership
}

// censusNotch is this target's census key. Ownership is dropped for every kind but the host,
// because it is the HOST's contract and has no referent elsewhere — a jail renders a home
// yolo provisions, so "who owns this file" is not a question the user gets asked there. Doing
// it here rather than trusting the constructors is what keeps the table total: a Target built
// inside this package with a stray ownership still lands on its notch's one entry.
func (t Target) censusNotch() notch {
	if t.kind != KindHost {
		return notch{kind: t.kind}
	}
	return notch{kind: t.kind, ownership: t.ownership}
}

// modeCensus is the notch → mode-policy table: the ONE place that answers "which mechanisms
// does this confinement level run, and which of them record?", the way ProfileFor answers
// "what does this level imply?" and Fields answers "which contribution kinds apply?".
//
// A MAP with a drift test rather than a switch, because the guest entry has to be a VISIBLE
// hole. Go cannot make a missing enum case a compile error, so the forcing function is the
// same one packdecl uses for its closed kind set: modes_test.go names every notch and asserts
// the count, so a notch added without a census entry fails a test that tells its author what
// to write. The fail-closed default in Modes() covers the window in between.
var modeCensus = map[notch]ModeSet{
	{kind: KindJail}: JailModes(),
	// A preview shows what the JAIL render produces (that is what `yolo config render` is for),
	// so it carries the jail's census — previewing a different mode set would print a file the
	// jail never writes. Same reasoning ProfileFor uses for the preview's autonomy bit.
	{kind: KindPreview}: JailModes(),
	// THE HOST HAS THREE ENTRIES, one per declared contract, and that is the whole of `own` at
	// this layer: the notch's mechanisms are a function of what the user declared, not of the
	// fact that it is the host. A host target with NO contract resolved (OwnershipUnstated) is
	// deliberately absent, so Modes() hands it the undecided set and it writes nothing.
	{kind: KindHost, ownership: OwnershipNone}:   HostUnmanagedModes(),
	{kind: KindHost, ownership: OwnershipAssert}: HostAssertModes(),
	{kind: KindHost, ownership: OwnershipOwn}:    HostOwnedModes(),
	// GUEST IS DELIBERATELY UNSTATED (env-manager plan Phase 7, Mac-gated). Both of the
	// answers above are mechanically available to it — a guest home is a real home that yolo
	// nonetheless provisions, so `stateful`'s regeneration premise HOLDS there while the host's
	// pure-rmw coercion is equally defensible — and that is precisely why it must not be
	// defaulted into one. Inventing semantics here would re-commit D2's error in the file that
	// exists to prevent it.
	{kind: KindGuest}: UndecidedModes("the `guest` notch's mode policy is Phase 7's to state: " +
		"yolo provisions a real home there, so `stateful`'s regenerate-every-boot premise " +
		"applies while the host's pure-rmw coercion is equally defensible — neither may be " +
		"inherited"),
	// KindUnset is not a notch at all (see its doc): a Target nobody's constructor built has
	// chosen no confinement level, so it runs nothing.
	{kind: KindUnset}: UndecidedModes("this target's confinement level was never set by a " +
		"constructor, so it has no mode policy — see render.KindUnset"),
}

// Modes returns this target's mode census. A notch with no entry gets the undecided set,
// which is the fail-closed direction and the same asymmetry ProfileFor argues: a mode wrongly
// not run writes no file, while a mode wrongly run writes one nobody asked for. The two
// entries that reach it are a Kind added without a census entry, and a HOST target whose
// caller never resolved the user's `host_management` contract.
func (t Target) Modes() ModeSet {
	if m, ok := modeCensus[t.censusNotch()]; ok {
		return m
	}
	if t.kind == KindHost {
		return UndecidedModes("this host target was built without resolving the user's " +
			"`host_management` contract, so there is nothing to say which mechanisms it may " +
			"run — see render.OwnershipUnstated")
	}
	return UndecidedModes("no mode census has been stated for this confinement level")
}

// Mechanism reports which of the mechanisms this target EXECUTES for a surface DECLARING
// `declared`, and false when the census cannot say — the fail-closed answer, which a caller
// must read as "do not render this surface at this notch" rather than as a default.
//
// It is the question a render ENTRY asks, and it is not Runs(). Runs answers "do you execute
// rmw?"; a dispatch holds a surface whose pack declared `stateful` and needs to know what to
// run for it, and at the host those have different answers: the notch runs rmw alone and
// renders a `stateful` surface THROUGH it (HostModes' exclusion says exactly that, in prose).
// Until this existed the host entry answered it by calling renderSurfaceRMWSurface
// unconditionally — correct, and correct for a reason written down in a file that code never
// read, so HostModes could have been changed to say something else with every host render
// still doing rmw. That is the rot the type comment above says this census exists to end.
//
// THE COERCION RULE, DERIVED RATHER THAN DECLARED. A target that does not run the declared
// mode renders the surface through its SOLE composing mechanism: the one mode in `runs` that
// writes a file. Exactly one is what makes a coercion expressible at all — "every surface is
// read-modify-written" is a sentence a notch can only say while it has one way to write — so
// a notch running several (a jail; a host that one day renders `stateful` too) coerces
// nothing, because every declared mode it runs is already its own answer and the fallback is
// unreachable there. A notch running none, or several while running none of the declaration,
// has no answer this table can supply, and says so.
//
// `unrendered` is a mechanism like any other here: a target that runs it answers with it, and
// honoring it means writing nothing. It is deliberately NOT a candidate for the coercion
// fallback — silently answering "write nothing" for a surface a pack asked to have rendered
// is the one wrong answer that would look like success.
func (m ModeSet) Mechanism(declared string) (string, bool) {
	// Stated rather than left to fall out of the empty maps below, so an undecided notch's
	// answer stays "nobody has said" even if UndecidedModes is one day given a runs entry.
	if m.Undecided() {
		return "", false
	}
	if m.Runs(declared) {
		return declared, true
	}
	var sole string
	for _, mode := range censusModes {
		if mode == manifest.ModeUnrendered || !m.Runs(mode) {
			continue
		}
		if sole != "" {
			// Several ways to write and the declaration names none of them: the census has
			// stated no coercion, and picking one here would invent the notch's policy.
			return "", false
		}
		sole = mode
	}
	return sole, sole != ""
}
