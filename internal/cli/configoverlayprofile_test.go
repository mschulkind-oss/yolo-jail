package cli

// configoverlayprofile_test.go covers the `profile` modifier on config-overlay at the
// HOST notch's user-facing surface (docs/reference/providers.md#the-profile-modifier):
// `yolo host apply` renders a gated overlay only while its name is the active profile at
// the target surface's agent, and reads that selection from the USER config — the scope
// every host composition draws. Same fixture style as configoverlay_test.go: a throwaway
// $HOME whose user config names the packs, the real $HOME never touched.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// gatedFixture is writeOverlayFixture plus a `profile` body, so a test can state
// which profiles the user selected (an empty profilesJSON means no `profile` key). The `acme`
// binary is stubbed on PATH, for the owner pack acmeAgentOwnerPackJSON declares.
//
// Under `host_management: "own"`: the unset key is `none` since the `assert` retirement
// (OQ-CO14), under which the host renders no config surface, gated overlay or not, so whether
// the gate folded the overlay could not be read off the file.
func gatedFixture(t *testing.T, profilesJSON string, packs map[string]string) string {
	t.Helper()
	home := writeOverlayFixtureUnder(t, packs, "own")
	stubBins(t, "acme")
	if profilesJSON == "" {
		return home
	}
	cfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// Splice the key in ahead of the closing brace: the fixture's config is one object.
	patched := strings.TrimRight(string(data), "\n")
	patched = strings.TrimSuffix(patched, "}") + ",\"profile\":" + profilesJSON + "}"
	if err := os.WriteFile(cfgPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

// acmeAgentOwnerPackJSON is acmeOwnerPackJSON as an AGENT pack: it also installs a CLI named
// acme. A `profile` key selects by the binary a pack installs, so without the program every
// launch, `yolo check` and `yolo host apply` refuse `{"acme": ...}` as naming no CLI, and "*"
// (or the string form) reaches no agent the gate could key acme/settings on.
const acmeAgentOwnerPackJSON = `{"name":"acme","contributes":[
  {"kind":"program","bin":"acme","via":"npm","package":"acme"},
  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
    "path":"~/.acme/settings.json","defaults":{"theme":"system"},
    "managed":{"telemetry":false}}]}]}`

// The gated contributor: acme/settings, only while profile "zai" is active for acme.
const acmeGatedPackJSON = `{"name":"acme-zai","contributes":[
  {"kind":"config-overlay","profile":"zai","surface":"acme/settings",
   "config":{"managed":{"theme":"zai-dark"}}}]}`

// With the profile selected in the USER config, `yolo host apply --assert` renders the
// overlay's key into the real home and names the contributing pack — R3 holding for a
// gated contribution exactly as it holds for an ungated one.
func TestApplyHostRendersGatedOverlayWhenProfileSelected(t *testing.T) {
	gatedFixture(t, `{"acme":"zai"}`, map[string]string{
		"acme":     acmeAgentOwnerPackJSON,
		"acme-zai": acmeGatedPackJSON,
	})
	// R3's contribution line is the --verbose view's since §4.5 (see TestApplyHostNamesThe
	// ContributingPack); the KEY landing in the file is asserted on the file itself below.
	verboseReport(t)

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String()+errw.String(), "config-overlay keys from: acme-zai") {
		t.Errorf("the output must name the contributing pack (R3 holds for a gated overlay):\n%s\n%s",
			out.String(), errw.String())
	}
	data, err := os.ReadFile(filepath.Join(fixtureHome(t), ".acme", "settings.json"))
	if err != nil {
		t.Fatalf("read the rendered surface: %v", err)
	}
	if !strings.Contains(string(data), "zai-dark") {
		t.Errorf("the gated overlay's key is absent from the host render with the profile selected:\n%s", data)
	}
}

// Without the profile selected, the apply SUCCEEDS, the owner's surface carries only the
// owner's keys, and nothing about config-overlay is reported — the same clean skip the
// jail render makes, because selection is the optionality (providers.md#the-profile-modifier).
func TestApplyHostSkipsGatedOverlayWhenProfileNotSelected(t *testing.T) {
	gatedFixture(t, `{"acme":"bedrock"}`, map[string]string{
		"acme":     acmeAgentOwnerPackJSON,
		"acme-zai": acmeGatedPackJSON,
	})

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
		t.Fatalf("an unselected profile must not fail the command: rc=%d\n%s\n%s",
			rc, out.String(), errw.String())
	}
	if report := out.String() + errw.String(); strings.Contains(report, "config-overlay") {
		t.Errorf("an inactive overlay must be a clean skip, not a report:\n%s", report)
	}
	data, err := os.ReadFile(filepath.Join(fixtureHome(t), ".acme", "settings.json"))
	if err != nil {
		t.Fatalf("read the rendered surface: %v", err)
	}
	if strings.Contains(string(data), "zai-dark") {
		t.Errorf("the overlay's key landed without its profile selected:\n%s", data)
	}
	if !strings.Contains(string(data), "system") {
		t.Errorf("the owner's own default was displaced:\n%s", data)
	}
}

// THE KEY'S DEFAULT GATES AT BOTH HOST RENDER CALL SITES: `yolo host apply --assert` and `yolo
// config render --at host` hand overlayGateProfiles the packs they render, so "*" (or the string
// form) reaches acme, the CLI the owner pack installs, and the gated overlay folds. Handed no
// packs, the default reaches no agent and the overlay is skipped in silence while the named
// entry still folds it.
func TestHostRendersFoldAGatedOverlayThroughTheProfileKeysDefault(t *testing.T) {
	for name, sel := range map[string]string{"string form": `"zai"`, "\"*\"": `{"*":"zai"}`} {
		packs := map[string]string{"acme": acmeAgentOwnerPackJSON, "acme-zai": acmeGatedPackJSON}
		t.Run(name+"/yolo host apply --assert", func(t *testing.T) {
			gatedFixture(t, sel, packs)
			var out, errw bytes.Buffer
			if rc := applyHost(&out, &errw, false, true, nil); rc != 0 {
				t.Fatalf("host apply --assert rc=%d\n%s\n%s", rc, out.String(), errw.String())
			}
			data, err := os.ReadFile(filepath.Join(fixtureHome(t), ".acme", "settings.json"))
			if err != nil {
				t.Fatalf("read the rendered surface: %v", err)
			}
			if !strings.Contains(string(data), "zai-dark") {
				t.Errorf("the key's default did not gate the overlay in at host apply:\n%s", data)
			}
		})
		t.Run(name+"/yolo config render --at host", func(t *testing.T) {
			gatedFixture(t, sel, packs)
			t.Chdir(t.TempDir())
			rc, out, errs := runConfigVerb(t, "render", "acme/settings", "--at", "host")
			if rc != 0 {
				t.Fatalf("config render --at host rc=%d\n%s%s", rc, out, errs)
			}
			if !strings.Contains(out, "zai-dark") {
				t.Errorf("the key's default did not gate the overlay into the host preview:\n%s%s", out, errs)
			}
		})
	}
}

// overlayGateProfiles' two branches, pinned apart: the jail half decodes the very table
// the boot render gated on (YOLO_USE_PROFILES), the host half lowers the USER config's
// profile and nothing else — the boundary UserScopeConfig states and the tests
// above exercise end to end.
func TestOverlayGateProfilesReadsEachNotchesOwnTable(t *testing.T) {
	// Jail: the launcher-emitted table, with a null at a key (the merge-patch removal the
	// lowering exists to drop) beside a real selection.
	t.Setenv("YOLO_USE_PROFILES", `{"acme":"zai","pi":null}`)
	jail := overlayGateProfiles(render.KindJail, nil)
	if jail["acme"] != "zai" {
		t.Errorf("jail table = %v, want acme=zai", jail)
	}
	if _, present := jail["pi"]; present {
		t.Errorf("a null profile decodes as a selection of an empty name: %v", jail)
	}

	// Host: the user config's profile, read through the fixture's $HOME.
	writeUserProfiles := func(body string) {
		t.Helper()
		home := t.TempDir()
		cfgDir := filepath.Join(home, ".config", "yolo-jail")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := `{"profile":` + body + `}`
		if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	}
	writeUserProfiles(`{"acme":"zai"}`)
	host := overlayGateProfiles(render.KindHost, nil)
	if host["acme"] != "zai" {
		t.Errorf("host table = %v, want acme=zai from the user config", host)
	}
	// A jail-side env var must NOT leak into the host branch.
	t.Setenv("YOLO_USE_PROFILES", `{"acme":"bedrock"}`)
	if again := overlayGateProfiles(render.KindHost, nil); again["acme"] != "zai" {
		t.Errorf("the host branch read the jail's env table: %v", again)
	}
	// The key's "*" gates for the agents the caller's packs install (PP-D10), as a jail's
	// launcher folded it into the table the jail branch reads: over no packs it reaches no one.
	writeUserProfiles(`{"*":"zai"}`)
	var claude []*packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "claude" {
			claude = append(claude, p)
		}
	}
	if host := overlayGateProfiles(render.KindHost, claude); host["claude"] != "zai" || len(host) != 1 {
		t.Errorf("host table over the claude pack = %v, want claude=zai alone", host)
	}
	if host := overlayGateProfiles(render.KindHost, nil); len(host) != 0 {
		t.Errorf("\"*\" over no packs must reach no one, got %v", host)
	}
}

// fixtureHome returns the throwaway $HOME writeOverlayFixture pointed the config at.
// applyHost resolves the real home through os.UserHomeDir, which the fixture pinned via
// t.Setenv — re-reading it here keeps the assertions on the same home the command used.
func fixtureHome(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return home
}
