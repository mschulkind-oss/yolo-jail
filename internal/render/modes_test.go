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

// EVERY Kind HAS A STATED CENSUS. This is the whole point of Q8: a notch must answer "which
// mechanisms do you run, and which of them record?" rather than inherit the answer from
// whichever branch of a runtime `if` its Kind fell on. "Undecided" is a legitimate answer
// (guest gives it) — silence is not.
func TestEveryKindHasAModeCensus(t *testing.T) {
	if got, want := len(allKinds), 5; got != want {
		t.Fatalf("allKinds has %d entries, want %d — a Kind was added or removed without "+
			"updating this list, so the census coverage below is no longer exhaustive", got, want)
	}
	for _, k := range allKinds {
		if _, stated := modeCensus[k]; !stated {
			t.Errorf("Kind %d has no modeCensus entry. Add one: either a real policy (which "+
				"modes it runs, which record) or UndecidedModes(reason) if the notch is not "+
				"built yet. Modes() defaults to undecided so the gap is safe, not silent — but "+
				"it must be WRITTEN DOWN", k)
		}
	}
}

// A ModeSet never claims to record a mechanism it does not run. Two maps cannot enforce this
// on their own, and the combination is meaningless: a provenance record for a render that
// never happens.
func TestRecordsIsAlwaysASubsetOfRuns(t *testing.T) {
	for _, k := range allKinds {
		m := (Target{kind: k}).Modes()
		for mode := range m.records {
			if !m.runs[mode] {
				t.Errorf("Kind %d records %q but does not run it — a record for a render that "+
					"never happens", k, mode)
			}
		}
	}
}

// Every mode a target does not both run AND record carries a REASON, for the same argument
// FieldSet.Refuse makes: the message has to say why, not just that. A bare "not applicable"
// sends a reader looking for a bug where there is a decision.
func TestExcludedModesCarryAReason(t *testing.T) {
	for _, k := range allKinds {
		m := (Target{kind: k}).Modes()
		for _, mode := range censusModes {
			why := m.Excludes(mode)
			if m.Runs(mode) && m.Records(mode) {
				if why != "" {
					t.Errorf("Kind %d runs and records %q but Excludes() gave a reason: %q",
						k, mode, why)
				}
				continue
			}
			if why == "" {
				t.Errorf("Kind %d excludes %q with no reason", k, mode)
			}
			if _, stated := m.excluded[mode]; !stated {
				t.Errorf("Kind %d falls back to the generic sentence for %q — the census owes "+
					"this mode its own reason", k, mode)
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
	if !Host("/home/me", nil).Modes().Records(manifest.ModeRMW) {
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
	host := Host("/home/me", nil).Modes()
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
// answer is a function of its Kind alone and cannot drift per call site. Pinned because the
// failure mode it replaces was exactly a per-call-site answer (an `if` in the writer).
func TestModesIsAFunctionOfKindAlone(t *testing.T) {
	for _, k := range allKinds {
		a := (Target{kind: k, Home: "/a", Workspace: "/ws-a"}).Modes()
		b := (Target{kind: k, Home: "/b"}).Modes()
		for _, mode := range censusModes {
			if a.Runs(mode) != b.Runs(mode) || a.Records(mode) != b.Records(mode) {
				t.Errorf("Kind %d: Modes() varies with the target's other fields for %q", k, mode)
			}
		}
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
