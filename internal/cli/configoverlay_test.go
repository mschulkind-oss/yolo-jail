package cli

// configoverlay_test.go covers the two USER-FACING halves of wiring `config-overlay`
// (docs/design/pack-config-collaboration.md §7):
//
//   - R2 — an overlay whose target has no owner is inert AND reported by name in
//     `apply --host`. Not an error (a pack the user did not select is not a mistake), and
//     never silent (the whole no-silent-skip invariant applyhostcensus_test.go enforces).
//   - R3 — per-key provenance is VISIBLE in `yolo config diff`: which pack set which key,
//     and where the owner's managed layer beat it. "Provenance nobody can read does not
//     make an override legible, which was the entire justification for the kind."
//
// Every test writes into a t.TempDir() home with its own config; the real $HOME is never
// read or written.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOverlayFixture points a throwaway $HOME's config at the given pack dirs (each a
// {name, pack.json} pair) and returns the home. The packs are `file://` sources, which
// resolve offline — a git source would need `yolo pack install`.
func writeOverlayFixture(t *testing.T, packs map[string]string) string {
	t.Helper()
	home := t.TempDir()
	packRoot := t.TempDir()
	var entries []string
	// Sorted so the `packs` list — which is the overlay FOLD order — is deterministic.
	for _, name := range sortedStrings(packs) {
		dir := filepath.Join(packRoot, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(packs[name]), 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, `{"source":"file://`+dir+`","name":"`+name+`"}`)
	}
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"packs":[` + strings.Join(entries, ",") + `]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// The owner pack: declares acme/settings with a managed key of its own.
const acmeOwnerPackJSON = `{"name":"acme","contributes":[
  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
    "path":"~/.acme/settings.json","defaults":{"theme":"system"},
    "managed":{"telemetry":false}}]}]}`

// The contributor pack: overlays acme/settings without owning it.
const acmeFzfPackJSON = `{"name":"acme-fzf","contributes":[
  {"kind":"config-overlay","surface":"acme/settings",
   "config":{"managed":{"fileSuggestion":"run-fzf"}}}]}`

// R2 in `apply --host`: with the owner pack absent, the overlay is named, the command
// still succeeds, and no file is created.
func TestApplyHostReportsOrphanOverlay(t *testing.T) {
	home := writeOverlayFixture(t, map[string]string{"acme-fzf": acmeFzfPackJSON})

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, false); rc != 0 {
		t.Fatalf("an unselected owner must not fail the command (R2): rc=%d\n%s\n%s",
			rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	for _, want := range []string{"config-overlay", "no effect", "acme/settings", "acme-fzf"} {
		if !strings.Contains(report, want) {
			t.Errorf("orphan report missing %q — R2 requires it be reported BY NAME:\n%s",
				want, report)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".acme", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("an ownerless overlay created a real-home file (err=%v)", err)
	}
}

// With the owner SELECTED, `apply --host --assert` writes the contributed key and the
// output names the contributing pack — R3 at the host notch, where the surface file
// cannot show the attribution itself.
func TestApplyHostNamesTheContributingPack(t *testing.T) {
	home := writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON,
	})

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true); rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	if !strings.Contains(report, "config-overlay keys from: acme-fzf") {
		t.Errorf("the output must name the contributing pack (R3):\n%s", report)
	}
	data, err := os.ReadFile(filepath.Join(home, ".acme", "settings.json"))
	if err != nil {
		t.Fatalf("read the rendered surface: %v", err)
	}
	if !strings.Contains(string(data), "run-fzf") {
		t.Errorf("the overlay's key never reached the host render:\n%s", data)
	}
}

// R3 in `config diff`: the contributed key is listed against the pack that set it.
func TestConfigDiffShowsOverlayProvenance(t *testing.T) {
	writeOverlayFixture(t, map[string]string{
		"acme":     acmeOwnerPackJSON,
		"acme-fzf": acmeFzfPackJSON,
	})
	dir := withSidecarDir(t)
	// The boot render's provenance sidecar: the overlay won `fileSuggestion`, the owner's
	// managed layer won `telemetry`.
	writeProvenanceSidecar(t, dir, "acme", "settings",
		"fileSuggestion\tconfig-overlay:acme-fzf\ntelemetry\tmanaged\n")

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"acme"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	for _, want := range []string{
		"acme/settings",                // the surface
		"config-overlay from acme-fzf", // who contributes
		"fileSuggestion",               // the key
		"set by acme-fzf",              // and that it won
	} {
		if !strings.Contains(got, want) {
			t.Errorf("config diff missing %q — an override nobody can read is not legible (R3):\n%s",
				want, got)
		}
	}
}

// The load-bearing case: an overlay that LOST. The surface file shows the owner's value
// with no hint a pack ever contested it, so the diff has to name both the contributor and
// the layer that beat it — otherwise "my key did nothing" has no answer.
func TestConfigDiffShowsAnOverlayThatLost(t *testing.T) {
	pushy := `{"name":"pushy","contributes":[
	  {"kind":"config-overlay","surface":"acme/settings",
	   "config":{"managed":{"telemetry":true}}}]}`
	writeOverlayFixture(t, map[string]string{"acme": acmeOwnerPackJSON, "pushy": pushy})
	dir := withSidecarDir(t)
	// The boot recorded `managed` as the winner: the owner beat the overlay.
	writeProvenanceSidecar(t, dir, "acme", "settings", "telemetry\tmanaged\n")

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"acme"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "contributed by pushy but managed won") {
		t.Errorf("a LOST overlay must name the contributor AND the winning layer:\n%s", got)
	}
	// And the footer explains the precedence rather than leaving it inferred.
	if !strings.Contains(got, "BELOW the owning pack's managed layer") {
		t.Errorf("the footer must state the precedence rule:\n%s", got)
	}
}

// An `rmw` surface writes no sidecars by design, so there is no recorded winner. The diff
// must say THAT rather than reporting the overlay as lost — the two are different states
// and conflating them sends a user chasing a non-defect.
func TestConfigDiffOverlayOnRMWSurfaceSaysNoProvenanceRecorded(t *testing.T) {
	rmwOwner := `{"name":"acme","contributes":[
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	    "path":"~/.acme/settings.json","mode":"rmw","managed":{"telemetry":false}}]}]}`
	writeOverlayFixture(t, map[string]string{"acme": rmwOwner, "acme-fzf": acmeFzfPackJSON})
	withSidecarDir(t) // no provenance sidecar: rmw writes none

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"acme"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "fileSuggestion") || !strings.Contains(got, "contributed by acme-fzf") {
		t.Errorf("the contribution must still be listed for an rmw surface:\n%s", got)
	}
	if !strings.Contains(got, "keeps no provenance sidecar") {
		t.Errorf("an rmw surface has no recorded winner and must say so, not imply a loss:\n%s", got)
	}
}

// An agent with neither captured edits nor overlays is still an error (rc 1) — the
// pre-existing contract, which the added overlay section must not weaken into a silent 0.
func TestConfigDiffStillRejectsAnAgentWithNothingToShow(t *testing.T) {
	writeOverlayFixture(t, map[string]string{"acme": acmeOwnerPackJSON})
	withSidecarDir(t)
	var out, errw bytes.Buffer
	if rc := configDiff([]string{"nosuchagent"}, &out, &errw, false); rc != 1 {
		t.Errorf("an agent with no surfaces should still be rc 1, got %d\n%s", rc, errw.String())
	}
}

// writeProvenanceSidecar seeds a surface's provenance record — the "key\tlayer" lines the
// boot render persists.
func writeProvenanceSidecar(t *testing.T, dir, agent, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, agent+"-"+name+".provenance"),
		[]byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
