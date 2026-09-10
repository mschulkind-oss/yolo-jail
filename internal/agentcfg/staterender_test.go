package agentcfg

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
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
	surface, ok := packManifest(t).Lookup("copilot", "config")
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
	surface, _ := packManifest(t).Lookup("copilot", "config")
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

// ---------------------------------------------------------------------------
// Managed keys are never captured — in EITHER branch.
//
// Managed is re-asserted after the fold (compose.go's Enforce step), so a
// captured managed key can never reach the written file. The first-migration
// branch has always narrowed its adopted residue against Managed; steady-state
// capture did not, so every boot that saw a managed key edited on disk folded
// that key into the overlay sidecar as permanent, un-actionable noise. The
// tests below pin the rule in the steady-state branch and pin that it is
// SELF-HEALING: a sidecar that already carries the dead key comes back clean.
// ---------------------------------------------------------------------------

// nestedManagedSurface mirrors packs/claude's claude/settings under the
// `autonomous` autonomy variant: managed carries a NESTED object (`permissions`)
// alongside a scalar. The nesting is what makes the rule non-trivial — Enforce
// merges a managed object key-by-key, so a captured SIBLING inside it (Claude's
// own `permissions.ask`) really does survive to the file and must be kept, while
// the leaves managed asserts are dead.
func nestedManagedSurface() manifest.Surface {
	return manifest.Surface{
		Agent: "claude",
		Name:  "settings",
		Path:  "~/.claude/settings.json",
		Codec: "json",
		Managed: map[string]any{
			"permissions": map[string]any{
				"defaultMode": "acceptEdits",
				"allow":       []any{},
			},
			"skipDangerousModePermissionPrompt": true,
		},
	}
}

// rawManagedSurface is the KEYLESS-with-managed shape: a raw surface whose
// managed layer is a whole-file string. No pack yolo ships declares this today,
// but manifest.Surface.Managed is `any` precisely so a surface can pin a whole
// file, so it is representable and must not capture either.
func rawManagedSurface() manifest.Surface {
	s := rawSurface()
	s.Managed = "MANAGED CONTENT\n"
	return s
}

// TestComposeStatefulSteadyStateDropsManagedFromOverlay: an in-jail edit to a
// MANAGED key is not captured. Managed wins the written file regardless, so
// storing it would only make `yolo config diff` report a phantom edit the user
// cannot act on.
func TestComposeStatefulSteadyStateDropsManagedFromOverlay(t *testing.T) {
	lastRender := `{"defaultProjectTrust":"always","theme":"system"}`
	// The agent rewrote BOTH a managed key and an ordinary one.
	current := `{"defaultProjectTrust":"never","theme":"solarized"}`

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
	got := jsonObj(t, string(out.OverlayJSON))
	if _, bad := got["defaultProjectTrust"]; bad {
		t.Errorf("overlay captured a MANAGED key: %v", got)
	}
	// The ordinary edit is untouched — this rule narrows, it does not disable capture.
	if got["theme"] != "solarized" {
		t.Errorf("overlay = %v, want the non-managed edit theme=solarized preserved", got)
	}
	if out.Result.ConfigMap()["defaultProjectTrust"] != "always" {
		t.Errorf("managed key = %v, want always (Enforce wins)", out.Result.ConfigMap()["defaultProjectTrust"])
	}
}

// TestComposeStatefulSteadyStateSelfHealsManagedOverlay is the SELF-HEALING
// half, and the one a naive fix misses: the sidecar ALREADY carries a dead
// managed key (written by an older yolo) and there is NO new edit this boot —
// current == last_render, so the delta is empty. The narrowing runs on the
// ACCUMULATED overlay rather than the incoming delta, so the existing dead key
// is canonicalized away on the next boot instead of sitting there forever.
func TestComposeStatefulSteadyStateSelfHealsManagedOverlay(t *testing.T) {
	same := `{"defaultProjectTrust":"always","theme":"system"}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface()},
		CurrentBytes:      []byte(same),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(same),
		// A sidecar an older yolo wrote: one dead managed key, one real edit.
		OverlayJSON: []byte(`{"defaultProjectTrust":"never","theme":"solarized"}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	got := jsonObj(t, string(out.OverlayJSON))
	if _, bad := got["defaultProjectTrust"]; bad {
		t.Errorf("overlay still carries a dead MANAGED key after a clean boot: %v", got)
	}
	if got["theme"] != "solarized" {
		t.Errorf("overlay = %v, want the pre-existing non-managed edit to survive", got)
	}
}

// TestComposeStatefulSteadyStatePreservesNonManagedEdits pins the other side of
// the narrowing: everything that is NOT managed survives, including a null
// tombstone (a captured deletion, §3.4). This is the live claude/settings shape —
// model / enabledPlugins / preferences / autoMemoryEnabled / an `env` tombstone
// are all real captures that must not be collateral damage.
func TestComposeStatefulSteadyStatePreservesNonManagedEdits(t *testing.T) {
	lastRender := `{"skipDangerousModePermissionPrompt":true,"env":{"A":"1"},"model":"old"}`
	current := `{"skipDangerousModePermissionPrompt":false,"model":"new","autoMemoryEnabled":true}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: nestedManagedSurface()},
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	got := jsonObj(t, string(out.OverlayJSON))
	if _, bad := got["skipDangerousModePermissionPrompt"]; bad {
		t.Errorf("overlay captured a MANAGED key: %v", got)
	}
	if got["model"] != "new" {
		t.Errorf("overlay = %v, want model=new captured", got)
	}
	if got["autoMemoryEnabled"] != true {
		t.Errorf("overlay = %v, want autoMemoryEnabled captured", got)
	}
	// The tombstone is the sharp one: an explicit null must survive the narrowing,
	// or the next boot resurrects the deleted key.
	v, present := got["env"]
	if !present || v != nil {
		t.Errorf("overlay = %v, want an explicit env:null tombstone preserved", got)
	}
}

// TestComposeStatefulSteadyStateKeepsManagedObjectSibling: managed `permissions`
// is an OBJECT, and Enforce merges an object key-by-key — so a captured sibling
// inside it (`permissions.ask`) reaches the file and is a REAL edit, while the
// leaves managed asserts (`permissions.defaultMode`) are dead. The narrowing must
// be the dual of Enforce, not a blanket top-level key drop: a blanket drop would
// silently discard the agent's own permission list on every boot.
func TestComposeStatefulSteadyStateKeepsManagedObjectSibling(t *testing.T) {
	lastRender := `{"permissions":{"defaultMode":"acceptEdits","allow":[]}}`
	// The agent added an `ask` list AND tried to change the managed defaultMode.
	current := `{"permissions":{"defaultMode":"plan","allow":[],"ask":["Bash(rm:*)"]}}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: nestedManagedSurface()},
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	got := jsonObj(t, string(out.OverlayJSON))
	perms, _ := got["permissions"].(map[string]any)
	if perms == nil {
		t.Fatalf("overlay dropped the whole managed object, losing a live sibling: %v", got)
	}
	if _, bad := perms["defaultMode"]; bad {
		t.Errorf("overlay captured a MANAGED leaf: %v", perms)
	}
	ask, _ := perms["ask"].([]any)
	if len(ask) != 1 || ask[0] != "Bash(rm:*)" {
		t.Errorf("overlay permissions = %v, want the non-managed ask sibling preserved", perms)
	}
	// And the render proves the sibling is live rather than noise: Enforce merges
	// managed over it, so `ask` survives while `defaultMode` is yolo's.
	rendered, _ := out.Result.ConfigMap()["permissions"].(map[string]any)
	if rendered["defaultMode"] != "acceptEdits" {
		t.Errorf("rendered defaultMode = %v, want acceptEdits (managed wins)", rendered["defaultMode"])
	}
	if ra, _ := rendered["ask"].([]any); len(ra) != 1 {
		t.Errorf("rendered permissions = %v, want the captured ask sibling to reach the file", rendered)
	}
}

// TestComposeStatefulSteadyStateKeylessManagedDoesNotCapture is the keyless twin.
// A keyless surface has ONE "key" — the whole file — and Ctx.Enforce replaces the
// whole value when managed is non-nil, so a captured whole-file edit can never
// reach the file. Capturing it would store a dead copy of the file forever.
func TestComposeStatefulSteadyStateKeylessManagedDoesNotCapture(t *testing.T) {
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawManagedSurface()},
		CurrentBytes:      []byte("AGENT EDITED THIS\n"),
		LastRenderPresent: true,
		LastRenderBytes:   []byte("MANAGED CONTENT\n"),
		OverlayJSON:       []byte(`null`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	if s := strings.TrimSpace(string(out.OverlayJSON)); s != "null" {
		t.Errorf("overlay = %s, want null — a managed keyless surface cannot hold a live overlay", out.OverlayJSON)
	}
	if out.Result.Config != "MANAGED CONTENT\n" {
		t.Errorf("Config = %q, want the managed whole-file value", out.Result.Config)
	}
}

// TestComposeStatefulSteadyStateKeylessSelfHealsManagedOverlay is the keyless
// self-heal: a sidecar an older yolo wrote already pins a dead whole-file value,
// and a clean boot must clear it.
func TestComposeStatefulSteadyStateKeylessSelfHealsManagedOverlay(t *testing.T) {
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawManagedSurface()},
		CurrentBytes:      []byte("MANAGED CONTENT\n"),
		LastRenderPresent: true,
		LastRenderBytes:   []byte("MANAGED CONTENT\n"),
		OverlayJSON:       []byte(`"STALE DEAD CAPTURE\n"`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	if s := strings.TrimSpace(string(out.OverlayJSON)); s != "null" {
		t.Errorf("overlay = %s, want null — the dead keyless capture must be canonicalized away", out.OverlayJSON)
	}
}

// TestComposeStatefulKeylessWithoutManagedStillCaptures is the guard on the
// keyless rule: it keys off MANAGED, not off keyless-ness. Every keyless surface
// yolo ships today (host_files' raw/lines) declares no managed layer, and their
// edit-survives-regeneration behaviour must be untouched.
func TestComposeStatefulKeylessWithoutManagedStillCaptures(t *testing.T) {
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawSurface()},
		CurrentBytes:      []byte("AGENT EDITED THIS\n"),
		LastRenderPresent: true,
		LastRenderBytes:   []byte("original\n"),
		OverlayJSON:       []byte(`null`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	if out.Result.Config != "AGENT EDITED THIS\n" {
		t.Errorf("Config = %q, want the captured edit to survive on an unmanaged keyless surface", out.Result.Config)
	}
}

// TestComposeStatefulFirstMigrationDropsManagedObjectSubtree pins that the
// first-migration branch is UNCHANGED by the shared narrowing. Adoption runs
// dropYoloOwnedSubtrees first, which already removes every top-level key the
// pure render holds as an object — and a managed object key is always one of
// those, because Enforce puts it there. So adoption never reaches the deep half
// of the rule, and its behaviour is the same before and after.
func TestComposeStatefulFirstMigrationDropsManagedObjectSubtree(t *testing.T) {
	current := `{"permissions":{"defaultMode":"plan","ask":["Bash(rm:*)"]},"model":"agent-picked"}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: nestedManagedSurface()},
		CurrentBytes:      []byte(current),
		LastRenderPresent: false,
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	got := jsonObj(t, string(out.OverlayJSON))
	if _, bad := got["permissions"]; bad {
		t.Errorf("adoption must not adopt a yolo-owned/managed subtree: %v", got)
	}
	if got["model"] != "agent-picked" {
		t.Errorf("overlay = %v, want the unasserted key adopted", got)
	}
}

// TestComposeStatefulSteadyStateKeepsTombstoneUnderManagedObject guards the one
// asymmetry in the narrowing: the rule drops only what is PROVABLY dead from the
// (overlay, owner) pair alone, and a null tombstone under an OBJECT-valued owner
// is not.
//
// The agent deleted the whole `permissions` object. Managed re-adds its own keys
// afterwards, so the tombstone looks redundant — but it is not: what it actually
// erases is whatever the LOWER layers (host, workspace, a pack's config-overlay)
// put under `permissions`, and those are invisible from here. Dropping it would
// silently resurrect them. Keeping a redundant tombstone is noise; dropping a
// live one is data loss, and this repo has paid for that class already (B1).
func TestComposeStatefulSteadyStateKeepsTombstoneUnderManagedObject(t *testing.T) {
	host := `{"permissions":{"ask":["Bash(rm:*)"]}}`
	lastRender := `{"permissions":{"defaultMode":"acceptEdits","allow":[],"ask":["Bash(rm:*)"]}}`
	current := `{}` // agent deleted the whole permissions object

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: nestedManagedSurface(), HostBytes: []byte(host)},
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	got := jsonObj(t, string(out.OverlayJSON))
	v, present := got["permissions"]
	if !present || v != nil {
		t.Fatalf("overlay = %v, want the permissions:null tombstone preserved", got)
	}
	// And it is live: the host's `ask` really is gone from the render, while
	// managed's own keys come back.
	rendered, _ := out.Result.ConfigMap()["permissions"].(map[string]any)
	if _, resurrected := rendered["ask"]; resurrected {
		t.Errorf("rendered permissions = %v, want the host `ask` to stay deleted", rendered)
	}
	if rendered["defaultMode"] != "acceptEdits" {
		t.Errorf("rendered permissions = %v, want managed re-asserted", rendered)
	}
}
