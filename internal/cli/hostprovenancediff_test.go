package cli

// hostprovenancediff_test.go is the READER half of host-side provenance: `yolo config ls`
// at the host notch reports a MEASURED winner, from the record `yolo host apply --assert`
// writes, instead of one inferred from what the packs declare.
//
// The defect being pinned against (docs/reference/pack-system.md §8, final
// bullet) was a confident wrong answer, not a missing one. With no host record to read, the
// command annotated each contributed key from the declarations and printed
//
//	fileSuggestion  contributed by fzf-overlay but managed won
//
// for a key the overlay in fact WON and no `managed` layer even declares. So the assertions
// here are two-sided: the measured winner must be reported, AND the false "managed won" must
// be absent. A test that only checked for the right string would still pass if both lines
// were printed.
//
// Every test drives a t.TempDir() home and a t.TempDir() state dir. The real $HOME is never
// read or written.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// withHostProvenanceDir returns the directory the host record reader resolves under the home
// writeOverlayFixture just installed, creating it.
//
// IT IS NO LONGER A STUBBED PATH BUILDER. With one resolved target the record's location is
// the TARGET's (render.Target.ProvenancePath, off the home its constructor was given), so the
// seam is the HOME — which the fixture already points at a temp dir. A stubbable path builder
// beside a stubbable notch predicate is exactly the pair
// docs/reference/config-target-resolution.md removed: a test could pin the record's location and
// leave the notch ambient, which is how a reader came to report one notch's outcome as the
// other's.
func withHostProvenanceDir(t *testing.T, home string) string {
	t.Helper()
	dir := render.Host(home, nil, render.OwnershipAssert).ProvenanceDir()
	if dir == "" {
		t.Fatal("render.Host(...).ProvenanceDir() is empty for a real home")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeHostProvenance seeds the record a `yolo host apply --assert` would have written.
func writeHostProvenance(t *testing.T, dir, agent, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, agent+"-"+name+".provenance"),
		[]byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hostNotchTarget is the HOST target — what a directory resolving no workspace resolves, and
// what routes `config ls` to the host record. It replaces a stub of the retired
// surfacesAreLocal: the notch is now a FIELD of the one resolved answer, so a test states it
// by constructing that answer rather than by pinning a predicate.
func hostNotchTarget(t *testing.T) configTarget {
	t.Helper()
	return hostTargetForTest()
}

// THE DEFECT, inverted: an overlay key with NO competing managed value must be reported as
// won — and must NOT say "managed won".
func TestConfigLsHostNotchReportsTheMeasuredWinner(t *testing.T) {
	home := writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON,
	})
	tgt := hostNotchTarget(t)
	dir := withHostProvenanceDir(t, home)
	// What `yolo host apply --assert` measured: the overlay won fileSuggestion (the owner does
	// not declare that key at all), the owner's managed layer won telemetry.
	writeHostProvenance(t, dir, "acme", "settings",
		"fileSuggestion\tconfig-overlay:acme-fzf\ntelemetry\tmanaged\n")

	var out, errw bytes.Buffer
	if rc := configLs(tgt, []string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configLs rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "set by acme-fzf") {
		t.Errorf("the host notch must report the MEASURED winner:\n%s", got)
	}
	// The load-bearing negative. This exact line was the bug.
	if strings.Contains(got, "contributed by acme-fzf but managed won") {
		t.Errorf("config diff still claims `managed won` for a key the overlay WON and no "+
			"managed layer declares — the annotation is inferred, not measured:\n%s", got)
	}
	// A measured answer says where it was measured, since the two notches render different
	// postures into different homes.
	if !strings.Contains(got, "host notch") {
		t.Errorf("a reported winner must be attributed to the notch it was measured at:\n%s", got)
	}
}

// The other side: an overlay that GENUINELY loses to the owner's managed layer still says so.
// The fix must not become "always report a win" — that is the same misreport reversed.
func TestConfigLsHostNotchReportsAGenuineLoss(t *testing.T) {
	pushy := `{"name":"pushy","contributes":[
	  {"kind":"config-overlay","surface":"acme/settings",
	   "config":{"managed":{"telemetry":true}}}]}`
	home := writeOverlayFixture(t, map[string]string{"acme": acmeOwnerPackJSON, "pushy": pushy})
	tgt := hostNotchTarget(t)
	dir := withHostProvenanceDir(t, home)
	// `telemetry` IS the owner's managed key, so the host render measured managed as the
	// winner. This time "managed won" is the truth.
	writeHostProvenance(t, dir, "acme", "settings", "telemetry\tmanaged\n")

	var out, errw bytes.Buffer
	if rc := configLs(tgt, []string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configLs rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "contributed by pushy but managed won") {
		t.Errorf("a genuine loss must still name the contributor AND the winning layer:\n%s", got)
	}
}

// A host notch with no apply yet: the winner is UNMEASURED and says so, with the remedy. It
// must not fall back to inferring, and it must not borrow the jail's message — the host
// renders every surface, so "this mode keeps no record" is not the reason here.
func TestConfigLsHostNotchWithNoApplyYet(t *testing.T) {
	home := writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON,
	})
	tgt := hostNotchTarget(t)
	withHostProvenanceDir(t, home) // no record seeded

	var out, errw bytes.Buffer
	if rc := configLs(tgt, []string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configLs rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	// The contribution is still LISTED — "my overlay did nothing" needs an answer either way.
	if !strings.Contains(got, "fileSuggestion") || !strings.Contains(got, "contributed by acme-fzf") {
		t.Errorf("the contribution must be listed even with no record:\n%s", got)
	}
	if !strings.Contains(got, "not measured") {
		t.Errorf("an absent record must read as UNMEASURED rather than as a winner:\n%s", got)
	}
	if !strings.Contains(got, "host apply --assert") {
		t.Errorf("the host-notch absence has a remedy and must name it:\n%s", got)
	}
	// Not the jail's by-design message: the host records every surface it writes, so an
	// absence here is "nobody has applied yet", a different state with a different fix.
	if strings.Contains(got, "keeps no provenance sidecar") {
		t.Errorf("the host absence borrowed the jail's by-design message:\n%s", got)
	}
	// And above all: no guessed winner.
	if strings.Contains(got, "managed won") {
		t.Errorf("with nothing measured, config ls still guessed a winner:\n%s", got)
	}
}

// The host notch must NOT read the jail's sidecar. The two notches render different postures
// into different homes, so reporting one as the other is the same class of wrong answer as
// inferring — just sourced from a real file, which makes it more convincing and no more true.
func TestConfigLsHostNotchIgnoresTheJailSidecar(t *testing.T) {
	home := writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON,
	})
	_, jailDir := withSidecarDir(t)
	tgt := hostNotchTarget(t)
	withHostProvenanceDir(t, home) // the HOST record is absent
	// A jail record that says something the host's would not.
	writeProvenanceSidecar(t, jailDir, "acme", "settings",
		"fileSuggestion\tconfig-overlay:acme-fzf\n")

	var out, errw bytes.Buffer
	if rc := configLs(tgt, []string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configLs rc=%d, stderr=%s", rc, errw.String())
	}
	if got := out.String(); strings.Contains(got, "set by acme-fzf") {
		t.Errorf("the host notch reported the JAIL's measurement as its own:\n%s", got)
	}
}

// EMPTY RECORD ≠ NEVER RENDERED. A host render that attributed no keys writes an empty
// record; that is a measurement ("nothing was attributed"), not an absence. Reading it as
// absent would answer a measured question with "we do not know" — the mirror image of the
// original defect.
func TestConfigLsHostNotchEmptyRecordIsNotUnmeasured(t *testing.T) {
	home := writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON,
	})
	tgt := hostNotchTarget(t)
	dir := withHostProvenanceDir(t, home)
	writeHostProvenance(t, dir, "acme", "settings", "") // rendered, nothing attributed

	var out, errw bytes.Buffer
	if rc := configLs(tgt, []string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configLs rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if strings.Contains(got, "not measured") {
		t.Errorf("an EMPTY record means the render attributed nothing — a measurement, not an "+
			"absence:\n%s", got)
	}
	// A key the render did not attribute means the key is not in the file. Reported as that
	// fact, not as a loss to a layer nobody recorded.
	if !strings.Contains(got, "not in the rendered file") {
		t.Errorf("a contributed key absent from a WRITTEN record must be reported as absent "+
			"from the file:\n%s", got)
	}
	if strings.Contains(got, "managed won") {
		t.Errorf("an empty record must not be annotated with a guessed winner:\n%s", got)
	}
}
