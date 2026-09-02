package entrypoint

// packoverlayrender_test.go is the BEHAVIORAL proof that `config-overlay` is wired at both
// render paths (docs/design/pack-config-collaboration.md §6 Option 2).
//
// The kind was inert for a specific reason worth pinning against: every piece existed —
// the schema, the footprint, the combine rule, and full compose-engine support
// (Inputs.Overlays with per-key provenance) — and nothing POPULATED it. So a unit test of
// the engine, or of the collector, proves nothing about the gap. These tests render, then
// read the file the agent would read.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

// overlayOwnerPack is the Layout C OWNER: a pack declaring one surface, with a managed
// key of its own so the precedence check has something to beat the overlay with.
func overlayOwnerPack(t *testing.T, mode string) *packload.Pack {
	t.Helper()
	surface := map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path":     "~/.acme/settings.json",
		"defaults": map[string]any{"theme": "system"},
		"managed":  map[string]any{"telemetry": false},
	}
	if mode != "" {
		surface["mode"] = mode
	}
	raw, err := json.Marshal([]any{surface})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// overlayContributorPack is the CONTRIBUTOR: a different pack asserting keys onto the
// surface the owner declares.
func overlayContributorPack(t *testing.T, name string, managed map[string]any) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"managed": managed})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: "acme/settings", Raw: raw},
		},
	}}
}

// readRenderedJSON decodes a rendered surface from a home.
func readRenderedJSON(t *testing.T, home, rel string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, rel))
	if err != nil {
		t.Fatalf("read rendered surface: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse rendered surface: %v\n%s", err, data)
	}
	return got
}

// overlayRenderEnv is a home + workspace with no /ctx mounts and a captured stderr, so a
// test can assert on both the file and the boot notices.
func overlayRenderEnv(t *testing.T) (*Env, *bytes.Buffer) {
	t.Helper()
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &errw}
	withCtxRoot(t, t.TempDir(), "none")
	return e, &errw
}

// THE JAIL PATH, stateful mode: an overlay declared by pack B lands in the surface pack A
// owns, and the owner's untouched keys survive.
func TestJailRenderAppliesOverlayFromAnotherPack(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	ConfigurePackSurfaces(e, []*packload.Pack{
		overlayOwnerPack(t, ""), // stateful (the default)
		overlayContributorPack(t, "acme-fzf", map[string]any{"fileSuggestion": "run-fzf"}),
	})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("render failed: %v", fails)
	}

	got := readRenderedJSON(t, e.Home, ".acme/settings.json")
	if got["fileSuggestion"] != "run-fzf" {
		t.Errorf("the overlay's key is absent from the owner's surface — config-overlay is "+
			"still inert:\n%#v", got)
	}
	if got["theme"] != "system" {
		t.Errorf("the overlay clobbered a key it never named: theme=%v, want system", got["theme"])
	}
}

// PRECEDENCE, the deliberate one: an overlay folds in BELOW the owner's `managed`, so the
// owner still wins a genuine conflict (pack-system.md §5). This is what makes the kind a
// contribution rather than a takeover.
func TestJailRenderOwnersManagedBeatsOverlay(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	ConfigurePackSurfaces(e, []*packload.Pack{
		overlayOwnerPack(t, ""),
		// Contest BOTH: the owner's managed key (owner must win) and its default (overlay
		// must win — that is the whole point of folding above defaults).
		overlayContributorPack(t, "pushy", map[string]any{"telemetry": true, "theme": "dark"}),
	})
	got := readRenderedJSON(t, e.Home, ".acme/settings.json")
	if got["telemetry"] != false {
		t.Errorf("an overlay overrode the owner's MANAGED key: telemetry=%v, want false "+
			"(the owner wins a genuine conflict)", got["telemetry"])
	}
	if got["theme"] != "dark" {
		t.Errorf("the overlay failed to override the owner's DEFAULT: theme=%v, want dark",
			got["theme"])
	}
}

// PROVENANCE names the contributing PACK, in the sidecar the boot render persists. This is
// what ruling R3's visibility requirement reads from.
func TestJailRenderProvenanceNamesTheContributingPack(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	ConfigurePackSurfaces(e, []*packload.Pack{
		overlayOwnerPack(t, ""),
		overlayContributorPack(t, "acme-fzf", map[string]any{"theme": "dark"}),
	})
	data, err := os.ReadFile(prismProvenancePath(e, "acme", "settings"))
	if err != nil {
		t.Fatalf("read provenance sidecar: %v", err)
	}
	if !strings.Contains(string(data), "theme\tconfig-overlay:acme-fzf") {
		t.Errorf("provenance does not attribute the key to the contributing pack:\n%s", data)
	}
	// And a key the owner's managed layer won is attributed to managed, not the overlay.
	if !strings.Contains(string(data), "telemetry\tmanaged") {
		t.Errorf("provenance should record the managed floor:\n%s", data)
	}
}

// RULING R2 at the jail path: an overlay whose target has no owner creates NO file, fails
// NO boot, and is reported by name.
func TestJailRenderOrphanOverlayIsInertAndReported(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	// The owner pack is deliberately absent.
	ConfigurePackSurfaces(e, []*packload.Pack{
		overlayContributorPack(t, "acme-fzf", map[string]any{"fileSuggestion": "run-fzf"}),
	})

	if fails := e.GenFailures(); len(fails) != 0 {
		t.Errorf("an unselected owner must NOT fail the launch (R2): %v", fails)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".acme", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("an ownerless overlay CREATED the file (err=%v) — that would let an overlay "+
			"own a surface by accident, destroying the distinction the kind draws", err)
	}
	report := errw.String()
	for _, want := range []string{"config-overlay", "no effect", "acme/settings", "acme-fzf"} {
		if !strings.Contains(report, want) {
			t.Errorf("orphan report missing %q — R2 requires it be reported BY NAME:\n%s",
				want, report)
		}
	}
}

// A malformed overlay IS fatal, and the split from R2 is the point: an unselected owner is
// not the author's mistake, whereas a body that redeclares the surface is.
func TestJailRenderMalformedOverlayFailsTheBoot(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	bad := &packload.Pack{Name: "sneaky", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{
			Kind: packdecl.KindConfigOverlay, Surface: "acme/settings",
			Raw: json.RawMessage(`{"mode":"rmw","managed":{"k":1}}`),
		}},
	}}
	ConfigurePackSurfaces(e, []*packload.Pack{overlayOwnerPack(t, ""), bad})
	fails := strings.Join(e.GenFailures(), "\n")
	if !strings.Contains(fails, "may not set \"mode\"") {
		t.Errorf("an overlay redefining the owner's mode must fail the boot, got: %q", fails)
	}
}

// RMW mode has no layer fold, so its precedence has to be expressed by write order. Same
// contract: the overlay asserts its key, the owner's managed still wins a conflict, and
// the agent's own unrelated keys survive.
func TestJailRenderRMWSurfaceAppliesOverlayBelowManaged(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	// Seed an agent-owned key, which RMW must preserve.
	dir := filepath.Join(e.Home, ".acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"oauthToken":"secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ConfigurePackSurfaces(e, []*packload.Pack{
		overlayOwnerPack(t, "rmw"),
		overlayContributorPack(t, "acme-fzf", map[string]any{
			"fileSuggestion": "run-fzf", "telemetry": true, // telemetry contests managed
		}),
	})
	got := readRenderedJSON(t, e.Home, ".acme/settings.json")
	if got["fileSuggestion"] != "run-fzf" {
		t.Errorf("the overlay's key is absent from the rmw surface:\n%#v", got)
	}
	if got["telemetry"] != false {
		t.Errorf("an overlay beat the owner's managed key on an rmw surface: telemetry=%v, "+
			"want false", got["telemetry"])
	}
	if got["oauthToken"] != "secret" {
		t.Errorf("rmw lost the agent's own key: %#v", got)
	}
}

// The RMW overlay is ASSERTED, not defaulted: an existing (stale) value for a contributed
// key is overwritten every boot, so a contributor changing its value takes effect. Filling
// only-if-absent would make the fzf case work once and then freeze.
func TestJailRenderRMWOverlayReassertsOverAStaleValue(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	dir := filepath.Join(e.Home, ".acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"fileSuggestion":"stale-from-a-previous-boot"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ConfigurePackSurfaces(e, []*packload.Pack{
		overlayOwnerPack(t, "rmw"),
		overlayContributorPack(t, "acme-fzf", map[string]any{"fileSuggestion": "run-fzf"}),
	})
	if got := readRenderedJSON(t, e.Home, ".acme/settings.json"); got["fileSuggestion"] != "run-fzf" {
		t.Errorf("an rmw overlay must RE-ASSERT its key, not merely seed it: %#v", got)
	}
}

// NESTED precedence on RMW, which the write-order model could plausibly get wrong: an
// overlay asserting a SIBLING key under a parent the owner also manages must keep both —
// the overlay's sibling survives AND the owner wins the contested leaf. A whole-subtree
// write on either side would lose one of them.
func TestJailRenderRMWOverlayMergesNestedSiblings(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	surface, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path": "~/.acme/settings.json", "mode": "rmw",
		"managed": map[string]any{"perms": map[string]any{"deny": []any{"owners-value"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	owner := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: surface}},
	}}
	contributor := overlayContributorPack(t, "acme-fzf", map[string]any{
		"perms": map[string]any{
			"allow": []any{"contributed"}, // a sibling the owner never names
			"deny":  []any{"CONTESTED"},   // the owner's key: the owner must win
		},
	})
	ConfigurePackSurfaces(e, []*packload.Pack{owner, contributor})

	got := readRenderedJSON(t, e.Home, ".acme/settings.json")
	perms, _ := got["perms"].(map[string]any)
	if perms == nil {
		t.Fatalf("the nested parent is missing: %#v", got)
	}
	allow, _ := perms["allow"].([]any)
	if len(allow) != 1 || allow[0] != "contributed" {
		t.Errorf("the overlay's nested SIBLING was lost: perms=%#v", perms)
	}
	deny, _ := perms["deny"].([]any)
	if len(deny) != 1 || deny[0] != "owners-value" {
		t.Errorf("the overlay beat the owner's nested managed key: perms=%#v", perms)
	}
}

// R3 at the jail path: an applied overlay is announced at the moment it applies, naming
// the contributing pack. An override folding in below managed is invisible in the file, so
// silence here would leave "which pack set this key" answerable only from a sidecar.
func TestJailRenderAnnouncesAppliedOverlays(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	ConfigurePackSurfaces(e, []*packload.Pack{
		overlayOwnerPack(t, ""),
		overlayContributorPack(t, "acme-fzf", map[string]any{"theme": "dark"}),
	})
	report := errw.String()
	for _, want := range []string{"acme/settings", "config-overlay keys from", "acme-fzf",
		"yolo config diff acme"} {
		if !strings.Contains(report, want) {
			t.Errorf("applied-overlay notice missing %q (R3: an override must be legible):\n%s",
				want, report)
		}
	}
}

// A pack set with no overlays writes no overlay notices at all — the quiet-by-default half
// of R3, and the reason wiring this changed nothing for the shipped packs.
func TestJailRenderWithNoOverlaysIsQuiet(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	ConfigurePackSurfaces(e, []*packload.Pack{overlayOwnerPack(t, "")})
	if strings.Contains(errw.String(), "config-overlay") {
		t.Errorf("a pack set with no overlays must say nothing about them:\n%s", errw.String())
	}
}

// --- the HOST path -----------------------------------------------------------------
//
// RenderHostPack renders ONE pack, so the cross-pack collection has to reach it as a
// parameter. These tests drive it exactly as `yolo host apply --assert` does: collect over the
// whole set, then render each pack against that set. Every home is a t.TempDir().

// THE HOST PATH: an overlay from pack B lands in pack A's surface in the REAL home.
func TestHostRenderAppliesOverlayFromAnotherPack(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "")
	contributor := overlayContributorPack(t, "acme-fzf", map[string]any{"fileSuggestion": "run-fzf"})
	// autonomy=false, matching applyHost: the host notch renders the guarded posture.
	overlays := packoverlay.Collect([]*packload.Pack{owner, contributor}, false, nil)

	results, err := RenderHostPack(owner, home, false, overlays)
	if err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	got := readRenderedJSON(t, home, ".acme/settings.json")
	if got["fileSuggestion"] != "run-fzf" {
		t.Errorf("the overlay's key is absent from the host render:\n%#v", got)
	}
	// R3 host-side: the result names the contributing pack, because the file itself cannot.
	var named bool
	for _, r := range results {
		if r.Surface == "acme/settings" && strings.Join(r.Overlays, ",") == "acme-fzf" {
			named = true
		}
	}
	if !named {
		t.Errorf("the host result must name the contributing pack (R3): %+v", results)
	}
}

// Host precedence matches the jail's: the owner's managed key still wins.
func TestHostRenderOwnersManagedBeatsOverlay(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "")
	pushy := overlayContributorPack(t, "pushy", map[string]any{"telemetry": true, "theme": "dark"})
	overlays := packoverlay.Collect([]*packload.Pack{owner, pushy}, false, nil)

	if _, err := RenderHostPack(owner, home, false, overlays); err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	got := readRenderedJSON(t, home, ".acme/settings.json")
	if got["telemetry"] != false {
		t.Errorf("an overlay overrode the owner's managed key at the host notch: telemetry=%v",
			got["telemetry"])
	}
	if got["theme"] != "dark" {
		t.Errorf("the overlay failed to override the owner's default: theme=%v", got["theme"])
	}
}

// R2 at the host path: with the owner unselected, the overlay contributes nothing and no
// file appears. (The naming half is the CALLER's — applyHost prints OverlaySet.Orphans;
// see TestApplyHostReportsOrphanOverlay.)
func TestHostRenderOrphanOverlayWritesNothing(t *testing.T) {
	home := t.TempDir()
	contributor := overlayContributorPack(t, "acme-fzf", map[string]any{"fileSuggestion": "run-fzf"})
	overlays := packoverlay.Collect([]*packload.Pack{contributor}, false, nil)

	if len(overlays.Orphans) != 1 {
		t.Fatalf("want the overlay reported as orphaned, got %+v", overlays.Orphans)
	}
	// Rendering the CONTRIBUTOR writes nothing: it declares no surface of its own.
	results, err := RenderHostPack(contributor, home, false, overlays)
	if err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("a contributor-only pack should render no surface, got %+v", results)
	}
	if _, err := os.Stat(filepath.Join(home, ".acme", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("an ownerless overlay CREATED a real-home file (err=%v)", err)
	}
}

// An overlay clobbering an EXISTING host value is warned about in the observe posture,
// attributed to the contributing pack — the host notch's always-warn (§4.2) extended to
// the one writer whose remedy is a different pack than the surface's owner.
func TestHostRenderWarnsWhenAnOverlayClobbersAUserValue(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"fileSuggestion":"my-own-choice"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	owner := overlayOwnerPack(t, "")
	contributor := overlayContributorPack(t, "acme-fzf", map[string]any{"fileSuggestion": "run-fzf"})
	overlays := packoverlay.Collect([]*packload.Pack{owner, contributor}, false, nil)

	// observe=true: the warning must appear BEFORE anything is written (finding D2).
	results, err := RenderHostPack(owner, home, true, overlays)
	if err != nil {
		t.Fatalf("RenderHostPack observe: %v", err)
	}
	var warned string
	for _, r := range results {
		if r.Surface == "acme/settings" {
			warned = strings.Join(r.Overwrites, "; ")
		}
	}
	if !strings.Contains(warned, "fileSuggestion") || !strings.Contains(warned, "acme-fzf") {
		t.Errorf("an overlay overwriting a user value must warn and name the contributing "+
			"pack, got %q", warned)
	}
	// Observe wrote nothing: the user's value is intact.
	if got := readRenderedJSON(t, home, ".acme/settings.json"); got["fileSuggestion"] != "my-own-choice" {
		t.Errorf("observe posture wrote to the real home: %#v", got)
	}
}

// A nil OverlaySet is the documented "no other packs in view" case and must render exactly
// as before this wiring existed.
func TestHostRenderWithNilOverlaySetIsUnchanged(t *testing.T) {
	home := t.TempDir()
	if _, err := RenderHostPack(overlayOwnerPack(t, ""), home, false, nil); err != nil {
		t.Fatalf("RenderHostPack with a nil overlay set: %v", err)
	}
	got := readRenderedJSON(t, home, ".acme/settings.json")
	if got["theme"] != "system" || got["telemetry"] != false {
		t.Errorf("a nil overlay set changed the render: %#v", got)
	}
	if _, present := got["fileSuggestion"]; present {
		t.Errorf("a nil overlay set contributed a key from nowhere: %#v", got)
	}
}

// --- the `profile` modifier at the render paths (profiles-as-pack-variants.md §7) --------------
//
// These drive ConfigurePackSurfaces itself, not the collector: the jail's profile table
// is resolved inside that function and handed to packoverlay.Collect there, so a wiring
// that stops passing the table (or resolves a different one than the variant folds read)
// fails HERE rather than in a unit test that bypasses the call site.

// gatedOverlayContributorPack is overlayContributorPack with the profile gate set.
func gatedOverlayContributorPack(t *testing.T, name, profile string, managed map[string]any) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"managed": managed})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: "acme/settings", Profile: profile, Raw: raw},
		},
	}}
}

// THE GATE AT THE JAIL RENDER, active half: with the profile selected at the surface's
// agent, the overlay's keys land in the file and the provenance sidecar attributes them
// to the contributing pack — everything an ungated overlay already guarantees, inherited
// unchanged (the gate decides presence, not precedence or attribution).
func TestJailRenderGatedOverlayAppliesWhenProfileActive(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	e.Vars["YOLO_USE_PROFILES"] = `{"acme":"zai"}`
	ConfigurePackSurfaces(e, []*packload.Pack{
		overlayOwnerPack(t, ""),
		gatedOverlayContributorPack(t, "acme-zai", "zai", map[string]any{"theme": "dark"}),
	})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("render failed: %v", fails)
	}

	got := readRenderedJSON(t, e.Home, ".acme/settings.json")
	if got["theme"] != "dark" {
		t.Errorf("the gated overlay's key is absent with the profile ACTIVE:\n%#v", got)
	}
	if got["telemetry"] != false {
		t.Errorf("the owner's managed key did not survive the gated overlay: telemetry=%v", got["telemetry"])
	}
	data, err := os.ReadFile(prismProvenancePath(e, "acme", "settings"))
	if err != nil {
		t.Fatalf("read provenance sidecar: %v", err)
	}
	if !strings.Contains(string(data), "theme\tconfig-overlay:acme-zai") {
		t.Errorf("provenance must attribute the gated key to the contributing pack:\n%s", data)
	}
}

// THE GATE AT THE JAIL RENDER, inactive half: without the profile selected, the boot
// succeeds, the owner's surface renders exactly what it declares, nothing is announced,
// and no overlay state is left behind for the next boot to resurrect.
func TestJailRenderGatedOverlaySkipsWhenProfileInactive(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	// A profile IS active — just not this one, so the skip cannot be mistaken for "no
	// table reached the render".
	e.Vars["YOLO_USE_PROFILES"] = `{"acme":"bedrock"}`
	ConfigurePackSurfaces(e, []*packload.Pack{
		overlayOwnerPack(t, ""),
		gatedOverlayContributorPack(t, "acme-zai", "zai", map[string]any{"theme": "dark"}),
	})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Errorf("an inactive profile must not fail the launch: %v", fails)
	}

	// The base surface is the owner's and only the owner's.
	got := readRenderedJSON(t, e.Home, ".acme/settings.json")
	if got["theme"] != "system" {
		t.Errorf("the owner's default was displaced by an INACTIVE overlay: theme=%v", got["theme"])
	}
	if got["telemetry"] != false {
		t.Errorf("the owner's managed key changed under an inactive overlay: telemetry=%v", got["telemetry"])
	}
	// Quiet in both directions: no applied notice (it did not apply) and no orphan report
	// (the reason it did not apply is the selection, which the launch line already states).
	if report := errw.String(); strings.Contains(report, "config-overlay") {
		t.Errorf("an inactive overlay must be a clean skip, not a report:\n%s", report)
	}
	data, err := os.ReadFile(prismProvenancePath(e, "acme", "settings"))
	if err != nil {
		t.Fatalf("read provenance sidecar: %v", err)
	}
	if strings.Contains(string(data), "config-overlay:") {
		t.Errorf("an inactive overlay left provenance behind:\n%s", data)
	}
}

// THE HOST PATH, gated exactly as applyHost gates it: the same Collect call shape with a
// profile table, rendered through RenderHostPack. The active half only — the inactive half
// is the jail test's property plus the collector's, and `yolo host apply`'s own table
// source is pinned in internal/cli (configoverlayprofile_test.go).
func TestHostRenderGatedOverlayAppliesWhenProfileActive(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "")
	contributor := gatedOverlayContributorPack(t, "acme-zai", "zai", map[string]any{"theme": "dark"})
	overlays := packoverlay.Collect([]*packload.Pack{owner, contributor}, false,
		map[string]string{"acme": "zai"})

	if _, err := RenderHostPack(owner, home, false, overlays); err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	got := readRenderedJSON(t, home, ".acme/settings.json")
	if got["theme"] != "dark" {
		t.Errorf("the gated overlay's key is absent from the host render with the profile "+
			"active:\n%#v", got)
	}
}

// packs/zai's profile-gated overlay — this file's first real consumer — was deleted with
// the env_shape vocabulary on 2026-09-02 (provider-catalog-and-selection.md §3.1): the
// endpoint now reaches claude through the agent pack's own env derive
// (internal/packload AgentEnv) at BOTH notches, so there is no settings key for a bare
// `claude` to read. The gate MECHANISM keeps its fixture tests above; what shipped
// through it moved.
