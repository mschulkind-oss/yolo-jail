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

// TestComposeStatefulFirstMigrationAdoptsUnassertedKeys is the §3.1/§3.2
// regression vector the migration doc (§6, Phase A) demands, with the rule B1
// inverted: a first-boot input where the on-disk file carries a key the new
// pipeline no longer emits.
//
// §3.2 asked for the stale key to be DROPPED and the overlay to stay EMPTY. B1
// rejected that, because nothing here can tell a stale key from the agent's own
// live state and guessing wrong is the copilot OAuth wipe — so a key yolo does
// not assert is ADOPTED, while what yolo does assert still wins the render. The
// stale-key half of §3.1 survives only where ownership is PROVABLE, which is a
// computed-owned table: see
// TestComposeStatefulFirstMigrationDropsComputedTableWholesale.
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
	// Only the PLAIN edit is captured. Capture used to be layer-agnostic — the
	// overlay recorded whatever changed on disk and let precedence sort it out at
	// render — but storing a delta this very test proves can never win made the
	// sidecar accumulate permanent noise, and `yolo config diff` report a phantom
	// edit the user could not act on. The overlay is now narrowed against the
	// layers that outrank it, so it holds only edits that can actually reach the
	// file.
	got := jsonObj(t, string(out.OverlayJSON))
	if _, bad := got["dynamicKey"]; bad {
		t.Errorf("overlay = %v, want the out-ranked computed key NOT captured", got)
	}
	if got["theme"] != "solarized" {
		t.Errorf("overlay = %v, want the plain edit captured {theme:solarized}", got)
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
// captured managed key can never reach the written file. Steady-state capture
// did not narrow against Managed at all, so every boot that saw a managed key
// edited on disk folded it into the overlay sidecar as permanent, un-actionable
// noise. The tests below pin the rule in BOTH branches, pin that it is
// SELF-HEALING (a sidecar that already carries the dead key comes back clean),
// and pin that it narrows at LEAF granularity — a managed object is merged
// key-by-key, so a sibling inside it is a real edit in either branch.
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

// TestComposeStatefulFirstMigrationKeepsManagedObjectSibling is the ADOPTION twin
// of TestComposeStatefulSteadyStateKeepsManagedObjectSibling, and the granularity
// the adoption branch got wrong. Managed `permissions` is an OBJECT, so Enforce
// merges it key-by-key and a leaf managed does NOT hold — Claude's own
// `permissions.ask` — really does reach the file. It must survive adoption exactly
// as it survives steady-state capture.
//
// This is not a hypothetical: the two branches disagreeing means a jail that loses
// its last_render sidecar (a fresh workspace, a deleted or truncated sidecar, an
// interrupted migration) silently discards the agent's permission list, while the
// very next boot keeps it.
func TestComposeStatefulFirstMigrationKeepsManagedObjectSibling(t *testing.T) {
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
	perms, _ := got["permissions"].(map[string]any)
	if perms == nil {
		t.Fatalf("adoption dropped the whole managed object, losing a live sibling: %v", got)
	}
	if _, bad := perms["defaultMode"]; bad {
		t.Errorf("adoption captured a MANAGED leaf: %v", perms)
	}
	ask, _ := perms["ask"].([]any)
	if len(ask) != 1 || ask[0] != "Bash(rm:*)" {
		t.Errorf("adopted permissions = %v, want the non-managed ask sibling preserved", perms)
	}
	if got["model"] != "agent-picked" {
		t.Errorf("overlay = %v, want the unasserted key adopted", got)
	}
	// And the sibling is live rather than noise: Enforce merges managed over it, so
	// `ask` reaches the file while `defaultMode` is yolo's.
	rendered, _ := out.Result.ConfigMap()["permissions"].(map[string]any)
	if rendered["defaultMode"] != "acceptEdits" {
		t.Errorf("rendered defaultMode = %v, want acceptEdits (managed wins)", rendered["defaultMode"])
	}
	if ra, _ := rendered["ask"].([]any); len(ra) != 1 {
		t.Errorf("rendered permissions = %v, want the adopted ask sibling to reach the file", rendered)
	}
}

// TestComposeStatefulFirstMigrationDropsComputedTableWholesale is the other half
// of the adoption narrowing, and the half that must stay WHOLESALE. A top-level
// key the COMPUTED layer holds as an object is a table yolo regenerates in full
// (codex's mcp_servers, opencode's mcp, mise's tools), so a stale entry sitting
// under it on disk is yolo's own output from a previous boot, not agent state.
//
// The leaf-level pass cannot express this: dropOverriddenKeys recurses into an
// object-valued owner and KEEPS every key the owner lacks — which is exactly the
// stale entry. Adoption therefore drops the whole table first, and only then
// hands the residue to the leaf-level narrowing. Delete that first pass and the
// dropped server comes back, breaking §2 principle 1 (regenerate, don't
// reconcile).
func TestComposeStatefulFirstMigrationDropsComputedTableWholesale(t *testing.T) {
	computed := map[string]any{"mcpServers": map[string]any{
		"live": map[string]any{"command": "/bin/live"},
	}}
	// On disk: yolo's own table from a previous boot, still carrying a server that
	// has since been dropped from config, plus a key the agent owns.
	current := `{"mcpServers":{"live":{"command":"/bin/live"},` +
		`"stale":{"command":"/gone"}},"model":"agent-picked"}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface(), Computed: computed},
		CurrentBytes:      []byte(current),
		LastRenderPresent: false,
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	got := jsonObj(t, string(out.OverlayJSON))
	if _, bad := got["mcpServers"]; bad {
		t.Errorf("adoption kept part of a computed-owned table: %v", got)
	}
	if got["model"] != "agent-picked" {
		t.Errorf("overlay = %v, want the unasserted key adopted", got)
	}
	rendered, _ := out.Result.ConfigMap()["mcpServers"].(map[string]any)
	if _, resurrected := rendered["stale"]; resurrected {
		t.Errorf("rendered mcpServers = %v, want the dropped server to stay dropped", rendered)
	}
	if _, ok := rendered["live"]; !ok {
		t.Errorf("rendered mcpServers = %v, want yolo's regenerated table", rendered)
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

// ---------------------------------------------------------------------------
// Computed keys are never captured either — the same rule, the larger half.
//
// Computed folds ABOVE the capture overlay (compose.go), so a captured edit to a
// computed-owned key can no more win than a managed one: "yolo's freshly
// regenerated data wins over a stale in-jail edit — §2 principle 1 regenerate,
// don't reconcile". Capture was recording keys the design has already ruled must
// lose. Measured in a live jail, this is most of the noise: mise/config's whole
// `tools` capture, codex/config's whole `mcp_servers`, opencode/config's whole
// `mcp`.
//
// The hazard is the sharper one, too. Remove an MCP server from your config and
// computed stops emitting it — at which point a previously-captured copy would
// WIN and silently resurrect the server you deleted. Same invisible-pending-edit
// shape as the `permissions` case.
// ---------------------------------------------------------------------------

// TestComposeStatefulSteadyStateDropsComputedFromOverlay: an in-jail edit to a
// key the computed layer supplies is not captured.
func TestComposeStatefulSteadyStateDropsComputedFromOverlay(t *testing.T) {
	computed := map[string]any{"mcpServer": "yolo-reconciled"}
	lastRender := `{"defaultProjectTrust":"always","theme":"system","mcpServer":"yolo-reconciled"}`
	current := `{"defaultProjectTrust":"always","theme":"solarized","mcpServer":"agent-hacked"}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface(), Computed: computed},
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	got := jsonObj(t, string(out.OverlayJSON))
	if _, bad := got["mcpServer"]; bad {
		t.Errorf("overlay captured a COMPUTED key: %v", got)
	}
	if got["theme"] != "solarized" {
		t.Errorf("overlay = %v, want the non-computed edit preserved", got)
	}
	if out.Result.ConfigMap()["mcpServer"] != "yolo-reconciled" {
		t.Errorf("mcpServer = %v, want yolo's regenerated value to win",
			out.Result.ConfigMap()["mcpServer"])
	}
}

// TestComposeStatefulSteadyStateSelfHealsComputedOverlay: the sidecar already
// carries a dead computed key and there is no new edit this boot. It must come
// back clean — this is the live mise/config, codex/config and opencode/config
// shape, where the ENTIRE captured object is computed-owned.
func TestComposeStatefulSteadyStateSelfHealsComputedOverlay(t *testing.T) {
	// mise: the computed [tools] table is exactly the injected YOLO_MISE_TOOLS pins.
	computed := map[string]any{"tools": map[string]any{
		"neovim":     "nightly",
		"pipx:swarf": "latest",
	}}
	same := `{"defaultProjectTrust":"always","theme":"system"}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface(), Computed: computed},
		CurrentBytes:      []byte(same),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(same),
		OverlayJSON:       []byte(`{"tools":{"neovim":"nightly","pipx:swarf":"latest"}}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	got := jsonObj(t, string(out.OverlayJSON))
	if _, bad := got["tools"]; bad {
		t.Errorf("overlay still carries a fully-computed capture after a clean boot: %v", got)
	}
	if len(got) != 0 {
		t.Errorf("overlay = %v, want {} — every captured key was computed-owned", got)
	}
}

// TestComposeStatefulSteadyStateKeepsComputedObjectSibling is the GRANULARITY
// test, and the one a top-level drop gets wrong. Computed deep-merges, so it
// owns only the leaves it actually supplies:
//
//   - mise's computed [tools] holds the injected YOLO_MISE_TOOLS pins, and
//     prism_mise.go is explicit that "a user-added global tool is captured into
//     the overlay and survives";
//   - claude's computed enabledPlugins holds the LSP-driven toggles, so a plugin
//     the user enabled themselves is theirs.
//
// Dropping `tools` or `enabledPlugins` wholesale would delete real user state.
func TestComposeStatefulSteadyStateKeepsComputedObjectSibling(t *testing.T) {
	computed := map[string]any{"enabledPlugins": map[string]any{
		"gopls-lsp@claude-plugins-official": true,
	}}
	lastRender := `{"defaultProjectTrust":"always","theme":"system",` +
		`"enabledPlugins":{"gopls-lsp@claude-plugins-official":true}}`
	// The agent turned the computed plugin off AND enabled one of its own.
	current := `{"defaultProjectTrust":"always","theme":"system",` +
		`"enabledPlugins":{"gopls-lsp@claude-plugins-official":false,"my-own-plugin":true}}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface(), Computed: computed},
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	got := jsonObj(t, string(out.OverlayJSON))
	plugins, _ := got["enabledPlugins"].(map[string]any)
	if plugins == nil {
		t.Fatalf("overlay dropped the whole computed-owned object, losing user state: %v", got)
	}
	if _, bad := plugins["gopls-lsp@claude-plugins-official"]; bad {
		t.Errorf("overlay captured a COMPUTED leaf: %v", plugins)
	}
	if plugins["my-own-plugin"] != true {
		t.Errorf("overlay enabledPlugins = %v, want the user's own plugin preserved", plugins)
	}
	// And the render proves both halves: yolo's toggle wins, the user's survives.
	rendered, _ := out.Result.ConfigMap()["enabledPlugins"].(map[string]any)
	if rendered["gopls-lsp@claude-plugins-official"] != true {
		t.Errorf("rendered = %v, want yolo's regenerated toggle to win", rendered)
	}
	if rendered["my-own-plugin"] != true {
		t.Errorf("rendered = %v, want the user's own plugin to reach the file", rendered)
	}
}

// TestComposeStatefulSteadyStateDropsKeyDeletedByComputedTombstone: a null in the
// computed layer DELETES the key from the render (claude's mcpServers tombstone,
// which strips a host block), so anything the overlay holds there is dead.
func TestComposeStatefulSteadyStateDropsKeyDeletedByComputedTombstone(t *testing.T) {
	computed := map[string]any{"mcpServers": nil}
	lastRender := `{"defaultProjectTrust":"always","theme":"system"}`
	current := `{"defaultProjectTrust":"always","theme":"system","mcpServers":{"x":{"command":"y"}}}`

	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piSurface(), Computed: computed},
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(lastRender),
		OverlayJSON:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	got := jsonObj(t, string(out.OverlayJSON))
	if _, bad := got["mcpServers"]; bad {
		t.Errorf("overlay captured a key the computed tombstone deletes: %v", got)
	}
	if _, present := out.Result.ConfigMap()["mcpServers"]; present {
		t.Errorf("render = %v, want mcpServers deleted by the computed tombstone",
			out.Result.ConfigMap())
	}
}

// TestComposeStatefulSteadyStateKeylessComputedDoesNotCapture: the keyless twin
// for the computed layer. Computed is a whole-value layer above the overlay on a
// keyless surface, so a captured whole-file edit can never reach the file.
func TestComposeStatefulSteadyStateKeylessComputedDoesNotCapture(t *testing.T) {
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: rawSurface(), Computed: "COMPUTED CONTENT\n"},
		CurrentBytes:      []byte("AGENT EDITED THIS\n"),
		LastRenderPresent: true,
		LastRenderBytes:   []byte("COMPUTED CONTENT\n"),
		OverlayJSON:       []byte(`null`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful error: %v", err)
	}
	if s := strings.TrimSpace(string(out.OverlayJSON)); s != "null" {
		t.Errorf("overlay = %s, want null — a computed keyless surface cannot hold a live overlay",
			out.OverlayJSON)
	}
	if out.Result.Config != "COMPUTED CONTENT\n" {
		t.Errorf("Config = %q, want the computed whole-file value", out.Result.Config)
	}
}
