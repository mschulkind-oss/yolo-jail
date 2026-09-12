package render

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// allKinds is every Kind constant, named individually so adding one to target.go without
// deciding its mode policy fails the census test below rather than passing silently. The
// same forcing function packdecl.KnownKinds gives the contribution kinds — Go has no
// exhaustiveness check over an int enum, so the list plus a count assertion is the closest
// thing to one.
var allKinds = []Kind{KindUnset, KindJail, KindGuest, KindHost, KindPreview}

// allNotches is every census KEY the table owes an answer for — the same forcing function one
// axis wider, since `own` made the mode policy a function of (Kind, HostOwnership) rather than
// of Kind alone. The host's three rows are the declared contracts; every other kind carries
// the zero ownership, which is what censusNotch normalizes to.
//
// The HOST-WITHOUT-A-CONTRACT pair is deliberately absent, and TestHostWithNoContractIsUndecided
// is why: it must reach Modes()' fail-closed default rather than a stated policy.
var allNotches = []notch{
	{kind: KindUnset}, {kind: KindJail}, {kind: KindGuest}, {kind: KindPreview},
	{kind: KindHost, ownership: OwnershipNone},
	{kind: KindHost, ownership: OwnershipAssert},
	{kind: KindHost, ownership: OwnershipOwn},
}

// target builds the Target for one census key. In-package, because a notch is exactly the two
// unexported fields — which is the property that makes ownership a thing only a constructor
// can state (see Target.ownership).
func (n notch) target() Target {
	return Target{kind: n.kind, ownership: n.ownership, Home: "/home/x"}
}

// EVERY NOTCH HAS A STATED CENSUS. This is the whole point of Q8: a notch must answer "which
// mechanisms do you run, and which of them record?" rather than inherit the answer from
// whichever branch of a runtime `if` its Kind fell on. "Undecided" is a legitimate answer
// (guest gives it) — silence is not.
func TestEveryNotchHasAModeCensus(t *testing.T) {
	if got, want := len(allKinds), 5; got != want {
		t.Fatalf("allKinds has %d entries, want %d — a Kind was added or removed without "+
			"updating this list, so the census coverage below is no longer exhaustive", got, want)
	}
	// One row per non-host kind, plus one per DECLARABLE contract at the host. Computed rather
	// than written as "7", so adding a fourth `host_management` value fails here by arithmetic
	// instead of passing because someone updated a literal without adding the row.
	if got, want := len(allNotches), len(allKinds)-1+len(DeclarableOwnerships()); got != want {
		t.Fatalf("allNotches has %d entries, want %d — a Kind or a HostOwnership was added "+
			"without a census key, so the coverage below is no longer exhaustive", got, want)
	}
	for _, n := range allNotches {
		if _, stated := modeCensus[n]; !stated {
			t.Errorf("notch %v has no modeCensus entry. Add one: either a real policy (which "+
				"modes it runs, which record) or UndecidedModes(reason) if the notch is not "+
				"built yet. Modes() defaults to undecided so the gap is safe, not silent — but "+
				"it must be WRITTEN DOWN", n)
		}
	}
}

// A ModeSet never claims to record a mechanism it does not run. Two maps cannot enforce this
// on their own, and the combination is meaningless: a provenance record for a render that
// never happens.
func TestRecordsIsAlwaysASubsetOfRuns(t *testing.T) {
	for _, n := range allNotches {
		m := n.target().Modes()
		for mode := range m.records {
			if !m.runs[mode] {
				t.Errorf("notch %v records %q but does not run it — a record for a render that "+
					"never happens", n, mode)
			}
		}
	}
}

// Every mode a target does not both run AND record carries a REASON, for the same argument
// FieldSet.Refuse makes: the message has to say why, not just that. A bare "not applicable"
// sends a reader looking for a bug where there is a decision.
func TestExcludedModesCarryAReason(t *testing.T) {
	for _, n := range allNotches {
		m := n.target().Modes()
		for _, mode := range censusModes {
			why := m.Excludes(mode)
			if m.Runs(mode) && m.Records(mode) {
				if why != "" {
					t.Errorf("notch %v runs and records %q but Excludes() gave a reason: %q",
						n, mode, why)
				}
				continue
			}
			if why == "" {
				t.Errorf("notch %v excludes %q with no reason", n, mode)
			}
			if _, stated := m.excluded[mode]; !stated {
				t.Errorf("notch %v falls back to the generic sentence for %q — the census owes "+
					"this mode its own reason", n, mode)
			}
		}
	}
}

// The census covers manifest's CLOSED mode set, and nothing else. If manifest grows a fifth
// mechanism, every entry above needs a decision for it; this is what says so, since a mode
// missing from a census map is indistinguishable from one deliberately excluded without a
// reason.
func TestCensusModesMatchTheManifestTaxonomy(t *testing.T) {
	if got, want := len(censusModes), 4; got != want {
		t.Fatalf("censusModes has %d entries, want %d", got, want)
	}
	for _, mode := range censusModes {
		if mode == "" {
			t.Error("censusModes holds an empty mode name")
		}
	}
	// The default a surface with no declared mode resolves to must be in the census, or the
	// commonest surface in the tree is the one nobody decided about.
	if !contains(censusModes, manifest.Surface{}.ResolvedMode()) {
		t.Errorf("the default resolved mode %q is not in the census",
			manifest.Surface{}.ResolvedMode())
	}
}

// THE BEHAVIOR THE REFACTOR MUST PRESERVE, stated as the question its one caller asks
// (internal/entrypoint's rmw writer): does an rmw render at this target keep a provenance
// record? True at the host, false in a jail. Getting this backwards is a data-attribution
// bug in both directions — a jail gaining a record falsifies `config diff`'s "this mode keeps
// no sidecar" message, and the host losing one relaunders a dropped pack's keys into "the
// user set this" on the very next apply.
func TestRMWRecordsAtTheHostAndNotInAJail(t *testing.T) {
	if !Host("/home/me", nil, OwnershipAssert).Modes().Records(manifest.ModeRMW) {
		t.Error("the HOST notch must record an rmw render: rmw is its only mode, so \"rmw " +
			"records nothing\" would mean \"the host records nothing\" — and a key a dropped " +
			"pack contributed would come back as `host` instead of retired")
	}
	if Jail("/home/agent", "/workspace", nil).Modes().Records(manifest.ModeRMW) {
		t.Error("a JAIL must NOT record an rmw render: `stateful` carries the recording duty " +
			"there (pack-config-collaboration.md §8), and `config diff` states the absence")
	}
	// The mode set proper, which is the other half of the census: the host coerces every
	// composing surface to rmw, so `stateful` and `computed` do not run there.
	host := Host("/home/me", nil, OwnershipAssert).Modes()
	for _, mode := range []string{manifest.ModeStateful, manifest.ModeComputed} {
		if host.Runs(mode) {
			t.Errorf("the host notch must not run %q — a host render is pure RMW (OQ-4)", mode)
		}
	}
	jail := Jail("/home/agent", "/workspace", nil).Modes()
	for _, mode := range censusModes {
		if !jail.Runs(mode) {
			t.Errorf("a jail runs every mechanism; it does not run %q", mode)
		}
	}
}

// A PREVIEW CARRIES THE JAIL'S CENSUS, for the reason ProfileFor gives its autonomy bit: the
// command exists to show what the jail render produces, so a different mode set would preview
// a file the jail never writes.
func TestPreviewCarriesTheJailCensus(t *testing.T) {
	preview := Preview("/tmp/scratch").Modes()
	jail := Jail("/home/agent", "/workspace", nil).Modes()
	for _, mode := range censusModes {
		if preview.Runs(mode) != jail.Runs(mode) || preview.Records(mode) != jail.Records(mode) {
			t.Errorf("preview and jail disagree on %q (runs %v/%v, records %v/%v) — a preview "+
				"must show what the jail render does", mode,
				preview.Runs(mode), jail.Runs(mode), preview.Records(mode), jail.Records(mode))
		}
	}
}

// GUEST IS REPRESENTABLE AND UNDECIDED (plan §6b D2, Phase 7). This is the inverse of the
// tests above: the assertion is that guest has NO policy yet, that its emptiness is marked as
// a pending decision rather than an answer, and that the pending state is fail-closed. When
// Phase 7 states guest's census, this test is the one that must be rewritten — deliberately,
// because "guest now has a policy" is exactly the change that should not pass silently.
func TestGuestModePolicyIsUndecidedNotInherited(t *testing.T) {
	guest := (Target{Home: "/Users/agent", Workspace: "/Users/matt/code/proj", kind: KindGuest}).Modes()
	if !guest.Undecided() {
		t.Fatal("guest's mode census is no longer marked undecided. If Phase 7 stated it, " +
			"replace this test with the assertions for that policy — do not just delete it")
	}
	// Fail-closed: nothing runs, nothing records. Not the jail's four mechanisms (which the
	// pre-Q1 shape inference would have handed it) and not the host's provenance write.
	for _, mode := range censusModes {
		if guest.Runs(mode) {
			t.Errorf("an undecided guest must run nothing; it runs %q", mode)
		}
		if guest.Records(mode) {
			t.Errorf("an undecided guest must record nothing; it records %q", mode)
		}
	}
	// And the reason names the notch and the phase, so a caller that hits one can say which
	// decision is missing rather than printing an empty set.
	why := guest.Excludes(manifest.ModeStateful)
	if !strings.Contains(why, "guest") || !strings.Contains(why, "Phase 7") {
		t.Errorf("guest's exclusion reason must name the notch and where its answer belongs; "+
			"got %q", why)
	}
	// The regeneration premise, pinned as PROSE because it is the correction that motivated
	// this file: `stateful` is not jail-shaped because a jail home is disposable (it is not —
	// it is bind-mounted from paths.GlobalHome()). It is jail-shaped because yolo regenerates
	// the destination every boot, which is equally true at guest. A reason resting on
	// disposability would justify guest's undecidedness with a fact about no notch at all.
	if strings.Contains(why, "disposable") {
		t.Errorf("guest's reason invokes DISPOSABILITY, which is not why `stateful` exists — "+
			"the jail home is bind-mounted and persists. The premise is REGENERATION; got %q", why)
	}
}

// An unconstructed Target runs nothing. The zero value is the one Kind a caller outside this
// package can produce, so the census's default has to be the harmless one — a bare
// render.Target{} must not write a file or claim an attribution.
func TestUnsetTargetRunsNoMode(t *testing.T) {
	var zero Target
	m := zero.Modes()
	if !m.Undecided() {
		t.Fatal("the zero Target must have an undecided mode census")
	}
	for _, mode := range censusModes {
		if m.Runs(mode) || m.Records(mode) {
			t.Errorf("an unset target must neither run nor record %q", mode)
		}
	}
}

// The census is the ONE table, the way ProfileFor and Fields are — so a Target's Modes()
// answer is a function of its NOTCH alone and cannot drift per call site. Pinned because the
// failure mode it replaces was exactly a per-call-site answer (an `if` in the writer).
//
// ⚠ IT VARIES THE OWNERSHIP FIELD TOO, and that is not decoration. It varied only Home and
// Workspace until `own`, so a Target grown a new field at its zero value sat OUTSIDE the
// assertion entirely: a census that ignored ownership — or one call site fabricating its own
// — would have landed with this test green, which is the silence the census exists to end.
// Every future field the notch is a function of belongs in this loop on the same reasoning.
func TestModesIsAFunctionOfTheNotchAlone(t *testing.T) {
	for _, n := range allNotches {
		a := Target{kind: n.kind, ownership: n.ownership, Home: "/a", Workspace: "/ws-a"}.Modes()
		b := Target{kind: n.kind, ownership: n.ownership, Home: "/b"}.Modes()
		for _, mode := range censusModes {
			if a.Runs(mode) != b.Runs(mode) || a.Records(mode) != b.Records(mode) {
				t.Errorf("notch %v: Modes() varies with the target's other fields for %q", n, mode)
			}
		}
	}
	// AND THE OWNERSHIP FIELD IS LOAD-BEARING AT THE HOST: the three contracts answer three
	// different things, so a census that dropped the field would fail here rather than
	// silently give every host target one policy. Stated as the mechanism each names for
	// every DECLARED mode, because that is the answer a render entry dispatches on.
	//
	// ⚠ EVERY MODE, not just `stateful`. This asked only about a `stateful`-declaring surface
	// until 2026-09-12, and a one-column question pins only the DISPATCH — that the three
	// contracts differ — while leaving each row's CONTENTS unpinned. Measured: deleting `rmw`
	// from HostOwnedModes()'s runs and records left the whole short suite green while
	// reversing two of the three decisions that function's own doc comment enumerates. `own`
	// then had a sole composing mechanism, so Mechanism's fallback coerced BOTH `rmw` and
	// `computed` to `stateful` — and a credential-shaped `rmw` surface composed into the host
	// capture store, which is the §6.2 privacy harm bullet one refuses in as many words.
	wantMechanism := map[HostOwnership]map[string]string{
		// `none` composes nothing: undecided for every WRITING mode. `unrendered` is honored
		// at every notch — honoring it IS writing nothing — so it answers as itself
		// everywhere, which is why it is the one row all three contracts share.
		OwnershipNone: {manifest.ModeUnrendered: manifest.ModeUnrendered},
		// `assert` has ONE composing mechanism, so its fallback coerces everything to it.
		// That coercion is the contract: today's behavior, unchanged.
		OwnershipAssert: {
			manifest.ModeStateful:   manifest.ModeRMW,
			manifest.ModeRMW:        manifest.ModeRMW,
			manifest.ModeComputed:   manifest.ModeRMW,
			manifest.ModeUnrendered: manifest.ModeUnrendered,
		},
		// `own` has TWO, so it coerces nothing: each declaration runs as itself, and
		// `computed` — which has no adoption path — is refused rather than coerced (OQ-CO9).
		OwnershipOwn: {
			manifest.ModeStateful:   manifest.ModeStateful,
			manifest.ModeRMW:        manifest.ModeRMW,
			manifest.ModeUnrendered: manifest.ModeUnrendered,
		},
	}
	for ownership, want := range wantMechanism {
		for _, declared := range censusModes {
			got, decided := Host("/home/me", nil, ownership).Modes().Mechanism(declared)
			if decided != (want[declared] != "") || got != want[declared] {
				t.Errorf("host under %q renders a %q surface through %q (decided=%v), want "+
					"%q — the declared contract is what picks the mechanism, and a census "+
					"that ignored render.Target's ownership field, or whose row lost a mode, "+
					"would answer this differently",
					ownership, declared, got, decided, want[declared])
			}
		}
	}
	// The same field is inert everywhere else: ownership is the HOST's contract, so setting it
	// on a jail target must not move that notch's census (Target.censusNotch drops it).
	plain := Jail("/home/agent", "/workspace", nil).Modes()
	stray := Target{kind: KindJail, Home: "/home/agent", Workspace: "/workspace",
		ownership: OwnershipOwn}.Modes()
	for _, mode := range censusModes {
		if plain.Runs(mode) != stray.Runs(mode) || plain.Records(mode) != stray.Records(mode) {
			t.Errorf("a jail target's census moved when ownership was set, for %q — "+
				"`host_management` has no referent at a notch yolo provisions", mode)
		}
	}
}

// A HOST TARGET WITH NO CONTRACT RESOLVED IS UNDECIDED — the fail-closed default, and the one
// case OwnershipUnstated exists for. It is NOT the "unset key" state: config resolves an
// absent `host_management` to `assert` and an unreadable user config to `none`, so reaching
// this means a caller built a host target without asking the boundary at all, and such a
// target must write nothing rather than inherit whichever contract looks likeliest.
func TestHostWithNoContractIsUndecided(t *testing.T) {
	m := Host("/home/me", nil, OwnershipUnstated).Modes()
	if !m.Undecided() {
		t.Fatal("a host target with no resolved `host_management` contract must be undecided")
	}
	for _, mode := range censusModes {
		if m.Runs(mode) || m.Records(mode) {
			t.Errorf("an unresolved host target must neither run nor record %q", mode)
		}
	}
	if why := m.Excludes(manifest.ModeStateful); !strings.Contains(why, "host_management") {
		t.Errorf("the reason must name the contract that was not resolved; got %q", why)
	}
}

// THE OWNED HOST KEEPS A CAPTURE STORE, and the other two contracts keep none — the
// directory half of the same declaration the census is the mechanism half of
// (config-ownership-and-promotion.md §6.2). Resolved through the Target so the writer and
// every reader have one definition; asserted here so a hand-built path elsewhere has
// something to disagree with.
func TestHostCaptureStoreIsOwnOnly(t *testing.T) {
	owned := Host("/home/me", nil, OwnershipOwn)
	if got, want := owned.SidecarDir(), "/home/me/.local/share/yolo-jail/host-capture"; got != want {
		t.Errorf("owned host capture store is %q, want %q", got, want)
	}
	// The provenance record does NOT move into it: two directories, two lifetimes (§6.2).
	if got, want := owned.ProvenanceDir(),
		"/home/me/.local/share/yolo-jail/host-provenance"; got != want {
		t.Errorf("owned host provenance dir is %q, want %q — the record stays where `assert` "+
			"writes it, or reverting an `assert` home would depend on a dir only `own` creates",
			got, want)
	}
	for _, ownership := range []HostOwnership{OwnershipUnstated, OwnershipNone, OwnershipAssert} {
		if dir := Host("/home/me", nil, ownership).SidecarDir(); dir != "" {
			t.Errorf("host under %q keeps a capture store at %q — it composes no whole file, "+
				"so there is no baseline to diff against and no edits to capture",
				ownership, dir)
		}
	}
	// The three capture files, named the same way a jail names them, and "" wherever there is
	// no store — so a caller that skipped the check gets an unwritable path, not a relative one.
	for _, got := range []string{
		owned.OverlayPath("acme", "settings"),
		owned.LastRenderPath("acme", "settings"),
		owned.SelectionPath("acme", "settings"),
	} {
		if !strings.HasPrefix(got, owned.SidecarDir()+"/acme-settings.") {
			t.Errorf("capture sidecar %q is not under the store with the jail's own naming", got)
		}
	}
	if p := Host("/home/me", nil, OwnershipAssert).OverlayPath("acme", "settings"); p != "" {
		t.Errorf("a contract with no store answered %q for a capture sidecar path", p)
	}
}

// THE STORE'S MODES, and the measured reason the directory is not 0600. A directory's execute
// bit is the right to resolve a name inside it, so 0600 leaves even the owner unable to open
// any file in the store while `ls` still works — the failure this assertion exists to keep
// out (config-ownership-and-promotion.md §6.2).
func TestHostCaptureStoreIsTraversableAndPrivate(t *testing.T) {
	owned := Host("/home/me", nil, OwnershipOwn)
	if got := owned.SidecarDirMode(); got != 0o700 {
		t.Errorf("the host capture store's directory mode is %O, want 0700 — without the "+
			"execute bit the owner cannot open a single file in it", got)
	}
	if got := owned.SidecarFileMode(); got != 0o600 {
		t.Errorf("the host capture store's file mode is %O, want 0600 — the overlay holds "+
			"whatever the user's real config held that yolo does not declare", got)
	}
	jail := Jail("/home/agent", "/workspace", nil)
	if jail.SidecarDirMode() != 0o755 || jail.SidecarFileMode() != 0o644 {
		t.Errorf("the jail's sidecar modes moved (%O/%O) — tightening them is a separate "+
			"ruling (§6.2 lists it for the roadmap), not a side effect of the host store",
			jail.SidecarDirMode(), jail.SidecarFileMode())
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// THE HOST COERCES EVERY WRITING MODE TO rmw, and Mechanism is where a render entry reads
// that instead of assuming it. HostModes says it in three places — `runs` holds rmw alone,
// and the `stateful`/`computed` exclusions both end "is rendered through `rmw` here" — and
// this is the machine-readable form of those sentences.
func TestHostMechanismCoercesEveryWritingModeToRMW(t *testing.T) {
	host := Host("/home/me", nil, OwnershipAssert).Modes()
	for _, declared := range []string{manifest.ModeStateful, manifest.ModeComputed, manifest.ModeRMW} {
		got, decided := host.Mechanism(declared)
		if !decided || got != manifest.ModeRMW {
			t.Errorf("a surface declaring %q renders through %q (decided=%v) at the host, want "+
				"%q — HostModes runs rmw alone, so this is the coercion its exclusions describe",
				declared, got, decided, manifest.ModeRMW)
		}
	}
	// And `unrendered` is answered with itself, not coerced into a write. Honoring it is
	// writing nothing, and the host census runs it for exactly that reason.
	if got, decided := host.Mechanism(manifest.ModeUnrendered); !decided || got != manifest.ModeUnrendered {
		t.Errorf("unrendered resolved to %q (decided=%v), want itself — coercing it would turn "+
			"a surface yolo declares it does not write into one it does", got, decided)
	}
}

// A NOTCH THAT RUNS EVERY MODE COERCES NOTHING: the jail's answer for each declared mode is
// that mode. This is the other half of the coercion rule — the fallback must be unreachable
// wherever the declaration is already something the notch runs — and it is what makes
// Mechanism safe to put in a dispatch shared with the boot path.
func TestJailMechanismIsTheDeclaredModeItself(t *testing.T) {
	jail := Jail("/home/agent", "/workspace", nil).Modes()
	preview := Preview("/tmp/scratch").Modes()
	for _, declared := range censusModes {
		if got, decided := jail.Mechanism(declared); !decided || got != declared {
			t.Errorf("jail: %q resolved to %q (decided=%v), want itself — the jail runs all four,"+
				" so no declaration is coerced there", declared, got, decided)
		}
		if got, decided := preview.Mechanism(declared); !decided || got != declared {
			t.Errorf("preview: %q resolved to %q (decided=%v), want itself", declared, got, decided)
		}
	}
}

// AN UNDECIDED NOTCH NAMES NO MECHANISM, for every declared mode — the fail-closed direction
// Modes() argues for and the answer a caller has to refuse on. `guest` is the live instance:
// until Phase 7 states its policy, a surface declared for it must not be written by whichever
// mechanism some other notch happens to use.
func TestUndecidedNotchNamesNoMechanism(t *testing.T) {
	for _, k := range []Kind{KindGuest, KindUnset} {
		m := (Target{kind: k}).Modes()
		for _, declared := range censusModes {
			if got, decided := m.Mechanism(declared); decided {
				t.Errorf("Kind %d named %q for a surface declaring %q — an unstated notch must "+
					"name nothing, or it inherits a policy nobody wrote", k, got, declared)
			}
		}
		// The refusal a caller prints comes from the census's own reason, not from the
		// caller's vocabulary — so the message says which notch is missing an answer.
		if why := m.Excludes(manifest.ModeStateful); why == "" {
			t.Errorf("Kind %d gives no reason to refuse with", k)
		}
	}
}
