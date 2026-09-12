package entrypoint

// hostownednullsteadystate_test.go renders the host notch TWICE ACROSS AN EDIT, which nothing
// else here does, and asserts that a literal `null` typed into the file between the two runs
// survives even when a pack layer declares the same key.
//
// ⚠ THE BRANCH IS THE POINT, not the fixture. The host-notch tests beside it — the
// byte-invariant one, the keys-and-values one, the precedence one — each seed a file and THEN
// render, so they exercise ComposeStateful's ADOPTION branch, where the capture overlay is
// built out of the file in the same breath and cannot disagree with it. The one that does
// write between two applies (TestOwnAdoptionArchivesOnlyTheFirstTime) destroys the capture
// store in between, on purpose, so it adopts a second time rather than reaching steady state.
//
// The host notch HAS a steady state: an `own` apply writes
// `host-capture/<agent>-<name>.last_render`, so the next apply diffs the file against it
// exactly as a jail boot does. That is where the file and the overlay can drift, and it was
// unmeasured at this notch — which is how a null at a declared key came to be deleted outright
// there (measured 2026-09-12, before `3b97094d`: neither the user's null nor the layer's
// value, the key simply gone).
//
// WHY THE EDIT HAS TO HAPPEN AFTER THE FIRST RENDER. `mergeDiff` cannot tell a key the user
// ADDED with a null value from one they DELETED — both are `k: null` in a merge patch — so the
// edit records a TOMBSTONE in the overlay sidecar. The test asserts that tombstone is really
// there before asserting the key survives it, so it fails loudly if its own premise moves
// rather than passing vacuously.
//
// Every test here renders into a t.TempDir() home. ⚠ Never point one at a real home.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// steadyStatePack declares `theme` in `defaults` — a layer BELOW the capture overlay, so it
// loses to a captured value and must lose to a captured null the same way.
func steadyStatePack(t *testing.T, rel string) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json", "path": "~/" + rel,
		"defaults": map[string]any{"theme": "system"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// hostCaptureOverlay reads the overlay sidecar an owned host apply keeps, through the same
// Target the apply resolves it with rather than a path spelled twice.
func hostCaptureOverlay(t *testing.T, home string) map[string]any {
	t.Helper()
	e := &Env{Home: home, Vars: map[string]string{}, hostTarget: true,
		hostOwnership: render.OwnershipOwn}
	b, err := os.ReadFile(prismOverlayPath(e, "acme", "settings"))
	if err != nil {
		t.Fatalf("read the host capture overlay: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode the host capture overlay: %v\n%s", err, b)
	}
	return m
}

func TestANullTypedInAfterAnOwnedHostApplySurvivesTheLayerThatDeclaresIt(t *testing.T) {
	home := t.TempDir()
	rel := ".acme/settings.json"
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mine":"kept"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	apply := func(what string) map[string]any {
		t.Helper()
		if _, err := RenderHostPack(steadyStatePack(t, rel), home,
			render.OwnershipOwn, false, nil); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: read back: %v", what, err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: decode: %v\n%s", what, err, b)
		}
		return m
	}

	// The adopting apply. `theme` comes from the pack's `defaults` because the file does not
	// set it — the baseline the edit below then contradicts.
	if got := apply("the adopting apply")["theme"]; got != "system" {
		t.Fatalf("the adopting apply did not take the pack default: theme = %#v", got)
	}

	// THE EDIT, made after yolo owns the file: the user nulls the key out.
	edited := map[string]any{"mine": "kept", "theme": nil}
	nb, err := json.MarshalIndent(edited, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nb, 0o644); err != nil {
		t.Fatal(err)
	}

	// The steady-state apply.
	after := apply("the steady-state apply")

	// THE PREMISE, asserted so the test cannot pass vacuously: the edit really did land in
	// the overlay as a TOMBSTONE, which is the shape that used to delete the key it records.
	ov := hostCaptureOverlay(t, home)
	tomb, marked := ov["theme"]
	if !marked || tomb != nil {
		t.Fatalf("this test measures a captured edit that records a TOMBSTONE, and the "+
			"capture no longer has that shape — overlay theme = %#v (present: %v). Without "+
			"it the case below proves nothing: re-derive the premise before relaxing it.",
			tomb, marked)
	}

	// THE PROPERTY. Present, and null.
	v, present := after["theme"]
	if !present {
		t.Errorf("`theme` was DELETED by the steady-state apply. The file held it as a "+
			"literal null and a `defaults` layer declares it, so the outcome was neither the "+
			"user's null nor the pack's default — the key simply went. A literal null folds "+
			"at the CAPTURE OVERLAY's precedence (agentcfg.Compose, overlayIdx), so only a "+
			"layer ABOVE the overlay may overrule it, and `defaults` is below.\nfile: %#v", after)
	} else if v != nil {
		t.Errorf("`theme` came back as %#v; the file held a literal null and `defaults` is "+
			"below the capture overlay, so the null wins.\nfile: %#v", v, after)
	}
	if after["mine"] != "kept" {
		t.Errorf("the user's ordinary key did not survive the steady-state apply: %#v", after)
	}

	// AND IT IS A FIXED POINT — the mark lives in the file, not in a sidecar, so a second
	// apply has to re-read it or it drops what the first put back.
	again := apply("the second steady-state apply")
	if v, present := again["theme"]; !present || v != nil {
		t.Errorf("a second apply lost the null: theme = %#v (present: %v)\nfile: %#v",
			v, present, again)
	}
}
