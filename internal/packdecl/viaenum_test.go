package packdecl

// viaenum_test.go holds the `via` vocabulary to ONE set.
//
// Three sites read that enum — the strict validator (validateContribution), the
// tolerant decoder's skip (unknownViaSkip) and the projection the jail installs from
// (InstallContributions) — and they used to spell the two values independently, with
// nothing coupling them. That is a live drift risk rather than a hypothetical one: it
// was MEASURED by adding a third value to both switches in contributes.go, which left
// the whole suite green while DecodeTolerant still dropped every contribution using
// it. The author sees a manifest that validates; the jail installs nothing; the only
// evidence is a skip note nobody reads.
//
// skewvia_test.go pins what happens to an unknown via (author hears, jail boots). This
// file pins WHICH VALUES ARE UNKNOWN — that the three sites cannot come to disagree
// about the membership question itself.

import (
	"slices"
	"testing"
)

// TestViaVocabularyIsOneSet sweeps the known values, the empty via, and two mechanisms
// no build knows, asserting the equivalence that makes the enum single:
//
//	validateContribution accepts X  ⇔  unknownViaSkip keeps X  ⇔  InstallContributions
//	gives X a non-empty Install.Kind
//
// with ONE documented exception, asserted rather than excused: an EMPTY via is refused
// by the validator and still KEPT by the tolerant decoder, because a program naming no
// mechanism is malformed in a way both ends of the version boundary understand — it is
// reported, never silently dropped (TestEmptyViaStaysFatalOnBothPaths).
//
// It fails if a value reaches any one site's switch without reaching KnownVia, which is
// the drift the fix exists to make unrepresentable.
func TestViaVocabularyIsOneSet(t *testing.T) {
	probes := append(KnownVias(), "", "uv", "pipx")
	for _, via := range probes {
		t.Run("via="+via, func(t *testing.T) {
			// Both payload fields are populated so a KNOWN via never fails on a missing
			// required field: the only thing under test here is membership.
			c := Contribution{
				Kind: KindProgram, Bin: "x", Via: via,
				Package: "pkg", URL: "https://example.invalid/i.sh",
			}
			known := KnownVia(via)

			accepted := len(validateContribution("contributes[0]", c)) == 0
			if accepted != known {
				t.Errorf("validateContribution accepts=%v, KnownVia=%v — the strict path knows a "+
					"different set than KnownVia does", accepted, known)
			}

			// The tolerant path drops what it does not know, EXCEPT the empty via, which is
			// structure and stays (reported by validateContribution above).
			kept := unknownViaSkip(0, c) == ""
			wantKept := known || via == ""
			if kept != wantKept {
				t.Errorf("unknownViaSkip keeps=%v, want %v (KnownVia=%v) — the version boundary "+
					"knows a different set than KnownVia does", kept, wantKept, known)
			}

			// The projection the jail installs from: a known via renders a delivery kind, an
			// unknown one renders nothing rather than a mechanism no installer implements.
			installs := (&Manifest{Contributes: []Contribution{c}}).InstallContributions()
			if len(installs) != 1 {
				t.Fatalf("a program contribution must project to exactly one Install, got %+v", installs)
			}
			if installable := installs[0].Kind != ""; installable != known {
				t.Errorf("InstallContributions gave Kind=%q (installable=%v), KnownVia=%v — the "+
					"install projection knows a different set than KnownVia does",
					installs[0].Kind, installable, known)
			}
		})
	}
}

// TestKnownViasCoversTheShippedMechanisms is the floor under the table above: an
// equivalence between three sites is satisfiable by all three being empty. These two
// values are what the shipped packs declare (npm for the agent CLIs, installer for the
// curl-to-shell ones), so dropping either from the vocabulary uninstalls agents.
func TestKnownViasCoversTheShippedMechanisms(t *testing.T) {
	got := KnownVias()
	for _, want := range []string{"npm", "installer"} {
		if !slices.Contains(got, want) {
			t.Errorf("KnownVias() = %v, missing %q — every pack declaring it stops installing", got, want)
		}
	}
	// The diagnostics quote the set; a validator naming values it no longer accepts is
	// the drift in the other direction.
	if viaList() != "npm or installer" {
		t.Errorf("viaList() = %q, want the set as the error messages name it", viaList())
	}
}
