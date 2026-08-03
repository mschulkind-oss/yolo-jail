package cli

// configexclusive_test.go is the HOST half of Option 1
// (docs/design/pack-config-collaboration.md): `apply --host` refuses two `config`
// declarations of one surface identity, and that refusal is what settles ruling R4.
//
// R4 is subtler than "a duplicated line": `apply --host` printed one `rendered` line PER
// DECLARING PACK for the same file — the collision made visible while nothing called it a
// collision, so the output was not silent, just uninterpretable. Deduping the line would have
// hidden the clash; refusing the apply removes the second line by removing the state that
// produced it.
//
// Every test writes into a t.TempDir() home with its own config; the real $HOME is never read
// or written.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two packs, one surface identity — the Layout B shape the fzf example used before it
// converted. `apply --host` must refuse, name both packs, teach `config-overlay`, and write
// NOTHING (the refusal is a pre-flight, not a partial apply).
func TestApplyHostRefusesDuplicateSurfaceOwner(t *testing.T) {
	second := `{"name":"acme-fzf","contributes":[
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	    "path":"~/.acme/settings.json","mode":"rmw","managed":{"fileSuggestion":"run-fzf"}}]}]}`
	home := writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": second,
	})

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc == 0 {
		t.Fatalf("apply --host --assert must REFUSE a doubly-owned surface (R1); rc=0\n%s%s",
			out.String(), errw.String())
	}
	report := out.String() + errw.String()
	for _, want := range []string{
		"acme", "acme-fzf", // both packs
		"acme/settings",  // the identity
		"config-overlay", // the conversion
		"Nothing was written",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("refusal missing %q; got:\n%s", want, report)
		}
	}
	// A pre-flight, not a partial apply: the surface file must not exist.
	if _, err := os.Stat(filepath.Join(home, ".acme", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("a refused apply must write nothing (err=%v)", err)
	}
}

// R4 proper: with the contributor converted to `config-overlay`, exactly ONE `rendered` line
// appears for the surface. The overlay path already printed one line on 47e98e1 — this pins
// that it stayed one, so a later reader does not "fix" the good path.
func TestApplyHostPrintsOneRenderedLinePerSurface(t *testing.T) {
	writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON,
	})

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if n := countRenderedLines(out.String(), "acme/settings"); n != 1 {
		t.Errorf("want exactly 1 `rendered` line for acme/settings, got %d — two lines for one "+
			"file is the tell that two packs are fighting over it (R4), and an overlay is not a "+
			"second declaration:\n%s", n, out.String())
	}
}

// countRenderedLines counts the output lines that report a render of one surface — the R4
// measurement. Matches on the surface identity plus the action word so a provenance or
// overwrite line about the same surface is not miscounted.
func countRenderedLines(report, surface string) int {
	n := 0
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(line, surface) && strings.Contains(line, "rendered") {
			n++
		}
	}
	return n
}

// A pack declaring one identity TWICE is refused the same way. It never reaches podman (there
// is no mount involved at all), so without this check the apply just resolves it silently —
// the same last-writer-wins that makes the cross-pack case dangerous.
func TestApplyHostRefusesSelfDuplicatedSurface(t *testing.T) {
	selfish := `{"name":"selfish","contributes":[
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	    "path":"~/.acme/settings.json","managed":{"telemetry":false}}]},
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	    "path":"~/.acme/settings.json","mode":"rmw","managed":{"other":true}}]}]}`
	writeOverlayFixture(t, map[string]string{"selfish": selfish})

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc == 0 {
		t.Fatalf("a pack declaring one surface identity twice must be refused; rc=0\n%s%s",
			out.String(), errw.String())
	}
	report := out.String() + errw.String()
	if !strings.Contains(report, "selfish") || !strings.Contains(report, "acme/settings") {
		t.Errorf("the refusal must name the pack and the identity; got:\n%s", report)
	}
}
