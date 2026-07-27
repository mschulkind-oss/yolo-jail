package agentcfg

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// jsonObj decodes a JSON object literal into the generic map model — a tiny
// helper so the state-render tests can assert overlay sidecar contents.
func jsonObj(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("jsonObj: %v", err)
	}
	return m
}

// TestComposeStatefulFirstMigrationDropsStaleKey is the §3.1/§3.2 regression
// vector the migration doc (§6, Phase A) demands: a first-boot input where the
// on-disk file carries a stale key the new pipeline no longer emits. On the
// FIRST migration boot (last_render absent) the render must DROP the stale key
// and the overlay must stay EMPTY — proving the empty-overlay SEED path is
// wired, not the naïve mergeDiff(∅, file) path that would pin the whole file.
func TestComposeStatefulFirstMigrationAdoptsUnassertedKeys(t *testing.T) {
	// The pre-existing bespoke file: a stale key ("legacyPin") plus an in-jail
	// theme change ("dark"). No host layer, no transform.
	current := `{"theme":"dark","legacyPin":"stale","defaultProjectTrust":"always"}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface()}, // no host, no script
		CurrentBytes:      []byte(current),
		LastRenderPresent: false, // first-migration signal
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}

	if !out.FirstMigration {
		t.Error("FirstMigration = false, want true on absent last_render")
	}
	// B1 changed this: a first migration now ADOPTS the on-disk file instead of
	// discarding it, because discarding silently destroyed agent-owned state (the
	// copilot OAuth-token wipe). So keys yolo does NOT assert are PRESERVED —
	// including ones that look "stale", since the engine cannot tell a stale key
	// from an agent's own live state, and guessing wrong loses data.
	//
	// What yolo DOES assert still wins: `theme` is a yolo default, so the file's
	// "dark" does not override it.
	// theme is a DEFAULT (user-overridable), so the file's "dark" legitimately wins
	// over it — that is what a default means, and it is the same result a steady-state
	// capture would give. defaultProjectTrust is MANAGED, so yolo's value still wins
	// no matter what the file said.
	want := map[string]any{
		"theme":               "dark",
		"defaultProjectTrust": "always",
		"legacyPin":           "stale",
	}
	if !reflect.DeepEqual(out.Result.Config, want) {
		t.Errorf("first-migration render mismatch:\n got: %#v\nwant: %#v", out.Result.Config, want)
	}
	// The adopted residue never contains a MANAGED key: managed is re-asserted after
	// the fold, so capturing it would be meaningless noise in the sidecar.
	got := jsonObj(t, string(out.OverlayJSON))
	if _, ok := got["legacyPin"]; !ok {
		t.Errorf("overlay = %v, want the adopted unasserted key", got)
	}
	if _, bad := got["defaultProjectTrust"]; bad {
		t.Errorf("overlay must not capture a MANAGED key: %v", got)
	}
}

// TestComposeStatefulFirstMigrationIgnoresDanglingOverlay is §3.3: last_render
// absent but a stale overlay sidecar present (an aborted-migration leftover).
// The overlay must be RESET to {} and its content must not leak into the render.
func TestComposeStatefulFirstMigrationIgnoresDanglingOverlay(t *testing.T) {
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface()},
		CurrentBytes:      []byte(`{"theme":"dark"}`),
		LastRenderPresent: false,
		OverlayJSON:       []byte(`{"theme":"junk-from-aborted-migration"}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	if !out.FirstMigration {
		t.Error("FirstMigration = false, want true")
	}
	// The core claim survives B1: the STALE SIDECAR's junk value must never leak.
	// theme resolves to the ON-DISK "dark" (adopted, and theme is a user-overridable
	// default), NOT to "junk-from-aborted-migration".
	if got := out.Result.ConfigMap()["theme"]; got != "dark" {
		t.Errorf("theme = %v, want dark (adopted from disk)", got)
	}
	if got := jsonObj(t, string(out.OverlayJSON)); got["theme"] == "junk-from-aborted-migration" {
		t.Errorf("dangling overlay value leaked into the new overlay: %v", got)
	}
}

// TestComposeStatefulSteadyStateCapturesEdit is the §5 steady-state loop: an
// in-jail edit (theme changed on disk vs. last_render) is captured into the
// overlay and SURVIVES the regeneration.
func TestComposeStatefulSteadyStateCapturesEdit(t *testing.T) {
	lastRender := `{"defaultProjectTrust":"always","theme":"system"}`
	current := `{"defaultProjectTrust":"always","theme":"solarized"}` // agent edited theme

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface()},
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	if out.FirstMigration {
		t.Error("FirstMigration = true, want false in steady state")
	}
	// The edit survives: overlay outranks the default, so theme stays solarized.
	if out.Result.ConfigMap()["theme"] != "solarized" {
		t.Errorf("theme = %v, want solarized (in-jail edit must survive regen)", out.Result.ConfigMap()["theme"])
	}
	if got := jsonObj(t, string(out.OverlayJSON)); got["theme"] != "solarized" {
		t.Errorf("overlay = %v, want {theme:solarized}", got)
	}
}

// TestComposeStatefulSteadyStateNoEdit: current == last_render, so the delta is
// empty, the overlay is unchanged, and the render is stable.
func TestComposeStatefulSteadyStateNoEdit(t *testing.T) {
	same := `{"defaultProjectTrust":"always","theme":"system"}`
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface()},
		CurrentBytes:      []byte(same),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(same),
		OverlayJSON:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	if got := jsonObj(t, string(out.OverlayJSON)); len(got) != 0 {
		t.Errorf("overlay = %v, want {} (no edit)", got)
	}
	if out.Result.ConfigMap()["theme"] != "system" {
		t.Errorf("theme = %v, want system", out.Result.ConfigMap()["theme"])
	}
}

// TestComposeStatefulSteadyStateCapturesDeletionTombstone is the §3.4 fix
// exercised end-to-end: an in-jail DELETION of a host-provided key is captured
// as a null tombstone in the overlay and, because the overlay outranks host,
// the key stays deleted on the next render even though the host layer would
// re-emit it. This proves mergeAccumulate's tombstone preservation is wired.
func TestComposeStatefulSteadyStateCapturesDeletionTombstone(t *testing.T) {
	host := `{"extra":"fromHost"}`
	// Last boot rendered defaults<host<managed = theme:system, extra:fromHost, dpt.
	lastRender := `{"defaultProjectTrust":"always","extra":"fromHost","theme":"system"}`
	// Agent deleted "extra" in-jail.
	current := `{"defaultProjectTrust":"always","theme":"system"}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface(), HostBytes: []byte(host)},
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	// The deletion wins over the host re-emission: "extra" is gone from the render.
	if _, present := out.Result.ConfigMap()["extra"]; present {
		t.Errorf("extra present in render (%v), want deleted (overlay tombstone must beat host)", out.Result.ConfigMap()["extra"])
	}
	// And the tombstone is persisted in the overlay (as an explicit null).
	got := jsonObj(t, string(out.OverlayJSON))
	v, ok := got["extra"]
	if !ok || v != nil {
		t.Errorf("overlay[extra] = %v (present=%v), want an explicit null tombstone", v, ok)
	}
}

// TestComposeStatefulSteadyStateAbsentCurrentSkipsCapture: if the surface file
// is absent on disk in steady state (e.g. the agent deleted it), capture is
// SKIPPED (bias toward under-capture — never freeze a spurious "delete
// everything" into the never-aging overlay) and the file is regenerated fresh.
func TestComposeStatefulSteadyStateAbsentCurrentSkipsCapture(t *testing.T) {
	lastRender := `{"defaultProjectTrust":"always","theme":"system"}`
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface()},
		CurrentBytes:      nil, // file absent
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{"theme":"solarized"}`), // a prior real edit
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	if out.FirstMigration {
		t.Error("FirstMigration = true, want false (last_render present)")
	}
	// The prior overlay is preserved untouched (no spurious tombstones added)...
	got := jsonObj(t, string(out.OverlayJSON))
	if got["theme"] != "solarized" || len(got) != 1 {
		t.Errorf("overlay = %v, want prior {theme:solarized} preserved (capture skipped)", got)
	}
	// ...and the file is regenerated with that overlay applied.
	if out.Result.ConfigMap()["theme"] != "solarized" {
		t.Errorf("theme = %v, want solarized (regenerated from preserved overlay)", out.Result.ConfigMap()["theme"])
	}
}

// TestComposeStatefulCorruptLastRenderReseeds is §3.3 generalized: a present
// but undecodable last_render sidecar cannot be diffed against, so it is treated
// as a first migration (re-seed with empty overlay) — the recovery path, never
// a boot-breaking error.
func TestComposeStatefulCorruptLastRenderReseeds(t *testing.T) {
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface()},
		CurrentBytes:      []byte(`{"theme":"dark"}`),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(`{not valid json`),
		OverlayJSON:       []byte(`{"theme":"solarized"}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful should recover from corrupt last_render, got error: %v", err)
	}
	if !out.FirstMigration {
		t.Error("FirstMigration = false, want true (corrupt last_render re-seeds)")
	}
	// B1: the recovery re-seed ADOPTS the on-disk file rather than discarding it —
	// a corrupt sidecar is exactly when discarding would destroy live agent state.
	// So the on-disk "dark" survives (theme is a user-overridable default), while
	// the STALE OVERLAY's "solarized" is still thrown away.
	if got := out.Result.ConfigMap()["theme"]; got != "dark" {
		t.Errorf("theme = %v, want dark (adopted from disk on re-seed)", got)
	}
	if got := jsonObj(t, string(out.OverlayJSON)); got["theme"] == "solarized" {
		t.Errorf("stale overlay value survived the re-seed: %v", got)
	}
}

// TestComposeStatefulEmptyLastRenderReseeds: a present but 0-byte last_render is
// as untrustworthy as an absent one, so it is treated as a first migration — which
// since B1 means ADOPTING the on-disk file rather than discarding it.
func TestComposeStatefulEmptyLastRenderReseeds(t *testing.T) {
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface()},
		CurrentBytes:      []byte(`{"theme":"dark","legacyPin":"stale"}`),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(``), // 0-byte sidecar
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	if !out.FirstMigration {
		t.Error("FirstMigration = false, want true (empty last_render re-seeds)")
	}
	// B1: an empty sidecar is a re-seed, and a re-seed now ADOPTS. legacyPin is a
	// key yolo does not assert, so it is preserved — the engine cannot distinguish a
	// stale key from the agent's own live state, and guessing wrong loses data.
	if _, present := out.Result.ConfigMap()["legacyPin"]; !present {
		t.Error("legacyPin dropped; an adopting re-seed must preserve unasserted keys")
	}
}

// TestComposeStatefulOverlayAbsentInitsEmpty is §3.3 case 3: last_render present
// but the overlay sidecar is absent (last boot had no edits). Initialize the
// overlay to {} and run the steady-state loop normally.
func TestComposeStatefulOverlayAbsentInitsEmpty(t *testing.T) {
	lastRender := `{"defaultProjectTrust":"always","theme":"system"}`
	current := `{"defaultProjectTrust":"always","theme":"solarized"}`
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface()},
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       nil, // overlay sidecar absent
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	if out.FirstMigration {
		t.Error("FirstMigration = true, want false (last_render present)")
	}
	// The edit is still captured against the present last_render.
	if out.Result.ConfigMap()["theme"] != "solarized" {
		t.Errorf("theme = %v, want solarized", out.Result.ConfigMap()["theme"])
	}
}

// TestComposeStatefulCorruptCurrentSkipsCapture: an undecodable current file
// (a botched in-jail edit) is not capturable and must self-heal — skip capture,
// preserve the prior overlay, and regenerate a valid file rather than breaking
// the boot with an error.
func TestComposeStatefulCorruptCurrentSkipsCapture(t *testing.T) {
	lastRender := `{"defaultProjectTrust":"always","theme":"system"}`
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface()},
		CurrentBytes:      []byte(`{corrupt`),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{"theme":"solarized"}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful should self-heal a corrupt current file, got error: %v", err)
	}
	if out.FirstMigration {
		t.Error("FirstMigration = true, want false (last_render valid)")
	}
	got := jsonObj(t, string(out.OverlayJSON))
	if got["theme"] != "solarized" || len(got) != 1 {
		t.Errorf("overlay = %v, want prior {theme:solarized} preserved (corrupt current => skip capture)", got)
	}
	if out.Result.ConfigMap()["theme"] != "solarized" {
		t.Errorf("theme = %v, want solarized (regenerated from preserved overlay)", out.Result.ConfigMap()["theme"])
	}
}

// TestComposeStatefulUnknownCodecFailsLoud: a bad surface codec is a real
// programmer error and must surface, not silently no-op.
func TestComposeStatefulUnknownCodecFailsLoud(t *testing.T) {
	s := piSurface()
	s.Codec = "bogus"
	if _, err := ComposeStateful(StatefulInputs{Base: Inputs{Surface: s}}); err == nil {
		t.Fatal("expected error for unknown codec, got nil")
	}
}

// TestComposeStatefulComputedBeatsCapturedEdit proves the computed layer flows
// through the stateful harness (via Base.Computed, which the harness does NOT
// overwrite — only Base.Overlay is) AND that its precedence holds in steady
// state: yolo's per-boot regenerated value wins over a captured in-jail edit to
// the SAME key. This is the exact claude scenario — an agent flips a
// yolo-computed dynamic key (an LSP toggle), the edit is captured into the
// overlay, and the next boot's fresh computation must still win (§2 principle
// 1). Meanwhile a genuine user edit to a NON-computed key survives via overlay.
func TestComposeStatefulComputedBeatsCapturedEdit(t *testing.T) {
	// Last boot: defaults<computed<managed → theme:system, dynamicKey:"on", dpt.
	lastRender := `{"defaultProjectTrust":"always","dynamicKey":"on","theme":"system"}`
	// Agent flipped the computed key AND edited a plain key in-jail.
	current := `{"defaultProjectTrust":"always","dynamicKey":"off","theme":"solarized"}`

	out, err := ComposeStateful(StatefulInputs{
		Base: Inputs{
			Surface:  piSurface(),
			Computed: map[string]any{"dynamicKey": "on"}, // yolo recomputes it "on" this boot
		},
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	// Computed beats the captured edit: the dynamic key reverts to yolo's value.
	if out.Result.ConfigMap()["dynamicKey"] != "on" {
		t.Errorf("dynamicKey = %v, want on (computed must beat captured edit)", out.Result.ConfigMap()["dynamicKey"])
	}
	// The plain in-jail edit still survives (computed doesn't touch it).
	if out.Result.ConfigMap()["theme"] != "solarized" {
		t.Errorf("theme = %v, want solarized (non-computed edit survives)", out.Result.ConfigMap()["theme"])
	}
	// Both edits are still CAPTURED in the overlay (capture is layer-agnostic; the
	// overlay records what changed on disk — precedence is decided at render, so
	// the dynamicKey delta is stored yet out-ranked by computed on the next fold).
	got := jsonObj(t, string(out.OverlayJSON))
	if got["dynamicKey"] != "off" || got["theme"] != "solarized" {
		t.Errorf("overlay = %v, want both edits captured {dynamicKey:off,theme:solarized}", got)
	}
}

// B1 (⚠ DATA LOSS): a first-migration boot must ADOPT the on-disk file rather than
// discarding it.
//
// copilot/config renders statefully with Defaults {"yolo": true} and NO host layer.
// On any boot where last_render is absent or corrupt — a fresh workspace, a deleted
// sidecar, an interrupted first migration — the old code seeded an EMPTY overlay, so
// the render collapsed a file holding copilot_tokens / logged_in_users /
// last_logged_in_user down to {"yolo": true}. The user is silently logged out and the
// token is gone. Steady state recovers, which is why it went unnoticed.
//
// Adopting means seeding the overlay from mergeDiff(pureRender, current): everything
// the agent owns that yolo does not assert is preserved.
func TestFirstMigrationAdoptsOnDiskFile(t *testing.T) {
	surface, ok := BuiltinManifest().Lookup("copilot", "config")
	if !ok {
		t.Fatal("builtin manifest missing copilot/config")
	}
	// A live config.json as copilot itself would have written it.
	live := `{"copilot_tokens":{"gh":"secret-token"},` +
		`"logged_in_users":["ada"],"last_logged_in_user":"ada","model":"x"}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: surface},
		CurrentBytes:      []byte(live),
		LastRenderPresent: false, // the first-migration / lost-sidecar case
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	if err := json.Unmarshal(out.Result.Encoded, &got); err != nil {
		t.Fatalf("decode render: %v", err)
	}
	if _, ok := got["copilot_tokens"]; !ok {
		t.Errorf("OAuth token wiped by a first-migration boot: %v", got)
	}
	for _, k := range []string{"logged_in_users", "last_logged_in_user", "model"} {
		if _, ok := got[k]; !ok {
			t.Errorf("agent-owned key %q lost on first migration: %v", k, got)
		}
	}
	// yolo's own default must still be asserted.
	if got["yolo"] != true {
		t.Errorf("yolo default not applied: %v", got)
	}
	if !out.FirstMigration {
		t.Error("FirstMigration should still be reported true")
	}
}

// A first-migration boot with NO file on disk must still start from a clean
// overlay — adoption must not invent content.
func TestFirstMigrationWithNoFileStaysClean(t *testing.T) {
	surface, _ := BuiltinManifest().Lookup("copilot", "config")
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: surface},
		LastRenderPresent: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out.OverlayJSON)) != "{}" {
		t.Errorf("overlay = %s, want {} with no file on disk", out.OverlayJSON)
	}
}
