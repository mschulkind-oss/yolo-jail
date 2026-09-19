package agentcfg

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// piFixedSurface is piSurface() AFTER the pack was corrected: the same surface with the
// shipped default moved off the rejected value, which is guard 2's precondition.
//
// It is a SEPARATE fixture rather than a change to piSurface deliberately. piSurface still
// declares `"theme": "system"`, so every other test in this package exercises the
// pack-not-yet-fixed case, where the repair must stay inert — a change there would replace
// that coverage with a second copy of this file's.
func piFixedSurface() manifest.Surface {
	s := piSurface()
	s.Defaults = map[string]any{"theme": "light/dark"}
	return s
}

// firstMigrationOf composes a first migration (no last_render) of the fixed pi surface over
// the given on-disk file — the shape a user's home was left in by a render that happened
// before the pack was fixed.
func firstMigrationOf(t *testing.T, s manifest.Surface, current string) *StatefulOutput {
	t.Helper()
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: s},
		CurrentBytes:      []byte(current),
		LastRenderPresent: false,
	})
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	return out
}

// TestRepairRemovesTheRejectedValueFromTheAdoptedOverlay is CASE 1, the bug: a home whose
// settings file still holds the `"theme": "system"` yolo shipped as a default.
//
// A first migration ADOPTS the file, so without the repair the unloadable value lands in the
// capture overlay — which outranks `defaults` — and pi fails to start on every boot from then
// on, with no default change able to reach it.
//
// It asserts the whole point of removing the key rather than overwriting it: the render picks
// up the CURRENT default and the provenance record says `defaults`, so the key is
// authoritative by absence and the next default change reaches this home too.
//
// FAILS IF THE CALL SITE IN ComposeStateful IS DELETED — nothing here calls RepairRejected.
func TestRepairRemovesTheRejectedValueFromTheAdoptedOverlay(t *testing.T) {
	out := firstMigrationOf(t, piFixedSurface(),
		`{"defaultProjectTrust":"always","theme":"system"}`)

	if got := out.Result.ConfigMap()["theme"]; got != "light/dark" {
		t.Errorf("theme = %v, want light/dark (the pack's current default)", got)
	}
	if got := out.Result.Provenance["theme"]; got != "defaults" {
		t.Errorf("provenance[theme] = %q, want defaults — the repair must leave the key "+
			"unset so it is filled, not write the replacement into a layer that "+
			"outranks defaults", got)
	}
	if got := jsonObj(t, string(out.OverlayJSON)); len(got) != 0 {
		t.Errorf("overlay = %v, want the rejected value gone from it", got)
	}
	if len(out.Repairs) != 1 {
		t.Fatalf("Repairs = %v, want exactly one reported repair", out.Repairs)
	}
	// The report is the only thing the caller can print, so its content is part of the
	// behavior: the removed value, what now supplies the key, and the program's own failure.
	line := out.Repairs[0].Describe("removed", "the overlay")
	for _, want := range []string{"pi/settings", `theme = "system"`, `"light/dark"`, "Theme not found"} {
		if !strings.Contains(line, want) {
			t.Errorf("Describe() = %q, missing %q", line, want)
		}
	}
}

// TestRepairLeavesAChosenValueAlone is CASE 2, and it is the guard that keeps this mechanism
// from being the blocked one (a default re-written whenever the default changes): a user who
// picked a theme keeps it, even though the pack's default has moved.
//
// `dracula` is deliberately not a builtin either — it comes from ~/.pi/agent/themes/ — so this
// also pins that the entry matches ONE VALUE and never "a value yolo cannot verify".
func TestRepairLeavesAChosenValueAlone(t *testing.T) {
	out := firstMigrationOf(t, piFixedSurface(),
		`{"defaultProjectTrust":"always","theme":"dracula"}`)

	if got := out.Result.ConfigMap()["theme"]; got != "dracula" {
		t.Errorf("theme = %v, want dracula — a chosen value is not yolo's to repair", got)
	}
	if got := jsonObj(t, string(out.OverlayJSON))["theme"]; got != "dracula" {
		t.Errorf("overlay[theme] = %v, want dracula kept in the capture overlay", got)
	}
	if len(out.Repairs) != 0 {
		t.Errorf("Repairs = %v, want none", out.Repairs)
	}
}

// TestRepairIsANoOpWhenTheKeyIsAbsent is CASE 3: a home that never got the bad value. The
// render is the plain fill-if-absent one and NOTHING is reported — a repair notice about a
// file yolo did not touch would be the mechanism's worst failure mode, since it is a sentence
// claiming to have edited the user's config.
func TestRepairIsANoOpWhenTheKeyIsAbsent(t *testing.T) {
	out := firstMigrationOf(t, piFixedSurface(), `{"defaultProjectTrust":"always"}`)

	if got := out.Result.ConfigMap()["theme"]; got != "light/dark" {
		t.Errorf("theme = %v, want light/dark", got)
	}
	if len(out.Repairs) != 0 {
		t.Errorf("Repairs = %v, want none for a key that was never set", out.Repairs)
	}
}

// TestRepairIsIdempotentOnTheSecondRender is CASE 4: feed the repaired render straight back in
// as the next boot's state — the file and last_render are what was just written and the
// overlay is the sidecar that was just persisted — and the second render must repair nothing
// while producing the same result.
//
// This is what lets an entry sit on the per-render path with no sentinel: the mechanism has no
// state of its own, so "already done" and "never needed" are the same observation.
func TestRepairIsIdempotentOnTheSecondRender(t *testing.T) {
	first := firstMigrationOf(t, piFixedSurface(),
		`{"defaultProjectTrust":"always","theme":"system"}`)

	second, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piFixedSurface()},
		CurrentBytes:      first.Result.Encoded,
		LastRenderPresent: true,
		LastRenderBytes:   first.Result.Encoded,
		OverlayJSON:       first.OverlayJSON,
	})
	if err != nil {
		t.Fatalf("ComposeStateful (second render): %v", err)
	}
	if len(second.Repairs) != 0 {
		t.Errorf("Repairs = %v on the second render, want none", second.Repairs)
	}
	if !reflect.DeepEqual(second.Result.Config, first.Result.Config) {
		t.Errorf("second render differs:\n got: %#v\nwant: %#v",
			second.Result.Config, first.Result.Config)
	}
	if got := second.Result.Provenance["theme"]; got != "defaults" {
		t.Errorf("provenance[theme] = %q on the second render, want defaults", got)
	}
}

// TestRepairReachesAValueFrozenInTheOverlay is the OTHER freeze topology, and the reason the
// call site is inside ComposeStateful rather than over the caller's file bytes: a home that
// has already migrated carries the rejected value in the PERSISTED OVERLAY, not on disk.
//
// Repairing the file instead would be worse than doing nothing there — the steady-state
// capture would read the missing key as a deletion and record a null tombstone, freezing the
// key ABSENT, which is the same bug one value over.
func TestRepairReachesAValueFrozenInTheOverlay(t *testing.T) {
	same := `{"defaultProjectTrust":"always","theme":"system"}`
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: piFixedSurface()},
		CurrentBytes:      []byte(same),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(same),
		OverlayJSON:       []byte(`{"theme":"system"}`),
	})
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	if got := out.Result.ConfigMap()["theme"]; got != "light/dark" {
		t.Errorf("theme = %v, want light/dark", got)
	}
	if got := jsonObj(t, string(out.OverlayJSON))["theme"]; got != nil {
		t.Errorf("overlay[theme] = %v, want the key gone (not a null tombstone)", got)
	}
	if len(out.Repairs) != 1 {
		t.Errorf("Repairs = %v, want one", out.Repairs)
	}
}

// TestRepairIsInertUntilThePackIsFixed is GUARD 2, and it is load-bearing twice over: it is
// what makes the repair a transitional fix for a pack that has ALREADY moved, and it is why
// every other test in this package — all of which declare `"theme": "system"` as the pi
// default — still sees the value it declares.
//
// Firing here would delete the key and hand it straight back to `defaults`, which would
// re-supply the same unloadable value: a boot notice every launch and no fix.
func TestRepairIsInertUntilThePackIsFixed(t *testing.T) {
	out := firstMigrationOf(t, piSurface(), // still ships the rejected value
		`{"defaultProjectTrust":"always","theme":"system"}`)

	if got := out.Result.ConfigMap()["theme"]; got != "system" {
		t.Errorf("theme = %v, want system left in place while the pack still declares it", got)
	}
	if len(out.Repairs) != 0 {
		t.Errorf("Repairs = %v, want none: the default would only re-supply the value", out.Repairs)
	}
}

// TestRepairIsInertWithoutADefaultToFallBackOn is the other half of guard 2: a surface with no
// `defaults` entry for the key has nothing to fill it, so removing it would silently drop a
// key rather than fix one.
func TestRepairIsInertWithoutADefaultToFallBackOn(t *testing.T) {
	s := piSurface()
	s.Defaults = map[string]any{"defaultModel": "sonnet"}
	out := firstMigrationOf(t, s, `{"theme":"system"}`)

	if got := out.Result.ConfigMap()["theme"]; got != "system" {
		t.Errorf("theme = %v, want it left alone when no default can refill it", got)
	}
	if len(out.Repairs) != 0 {
		t.Errorf("Repairs = %v, want none", out.Repairs)
	}
}

// TestRejectedValuesEntriesAreJustified is a review gate on the closed list, not a behavior
// test: every entry has to name a real surface and carry the measured reason the target
// rejects the value, because that reason is the entire justification for editing a user's
// config file — and it is what a reader needs in the boot notice to accept the edit.
func TestRejectedValuesEntriesAreJustified(t *testing.T) {
	for _, rv := range rejectedValues {
		if rv.Agent == "" || rv.Surface == "" || rv.Key == "" {
			t.Errorf("entry %+v: agent, surface and key are all required", rv)
		}
		if rv.Value == nil {
			t.Errorf("entry %+v: an entry names ONE exact value; nil would match an absent key", rv)
		}
		if len(rv.Why) < 40 {
			t.Errorf("entry %+v: Why must state the target's own failure, in enough detail "+
				"to justify the edit to the user whose file it is", rv)
		}
	}
}
