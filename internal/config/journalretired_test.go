package config

import (
	"strings"
	"testing"
)

// journalretired_test.go pins the SECOND half of loophole-activation.md §1.4: core's
// config schema named exactly two loopholes by hand, `host_processes` went on
// 2026-08-18, and `journal` is the one whose removal makes the pair mean something —
// after it, the schema names no loophole at all.

// TestRetiredJournalKeyIsRefusedAndNamesItsReplacement pins the DELETION, and pins it
// as a REFUSAL.
//
// The difference from `host_processes` is worth stating, because it makes silence a
// worse answer here rather than an equal one: that key CONFIGURED a daemon, this one
// TURNED ONE ON. A config that still says `"journal": "full"` and gets nothing has an
// agent that cannot read the host's logs and no thread back to the key that was
// ignored — "my logs stopped working" leads nowhere.
//
// The message has three jobs and all three are asserted, because migrating the value
// alone leaves `yolo-journalctl` just as broken: select the pack, enable the loophole,
// and (only for the old "full") write the setting — in the USER config, because `full`
// is declared `scope: "user"` and a workspace file supplying it is itself refused.
// That scope is the ruling rather than a detail (OQ-K4's "security half"):
// `"journal": "full"` was settable from an agent-editable file with no scope rule at
// all.
func TestRetiredJournalKeyIsRefusedAndNamesItsReplacement(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	cfg := decode(t, `{"journal": "full"}`)
	errs, warns := ValidateConfig(cfg, t.TempDir(), nil)
	hits := containing(errs, "config.journal")
	if len(hits) != 1 {
		t.Fatalf("errors = %v, want ONE refusal naming the retired key; a key that stopped "+
			"working must not be ignored, and it must not be reported twice either "+
			"(it stays in knownTopLevelConfigKeys so the generic unknown-key error does "+
			"not fire beside this one)", errs)
	}
	for _, want := range []string{
		"REMOVED",
		"packs",    // the bridge is a pack now; migrating the value alone is not enough
		"journal",  // the loophole's name, which is also the pack's
		"enabled",  // the switch that replaced "user"/true
		"settings", // where the mode went
		"full",     // the escalation, by the name it now has
		"USER",     // and the scope, which is the point of the move
	} {
		if !strings.Contains(hits[0], want) {
			t.Errorf("refusal %q does not mention %q", hits[0], want)
		}
	}
	if len(containing(warns, "config.journal")) != 0 {
		t.Errorf("warnings = %v — on the host this is an error, not a warning", warns)
	}
}

// TYPE AND ENUM CHECKS GO WITH THE KEY. `journal` used to accept `off|user|full` or a
// boolean and error on anything else; asking a user to fix the shape of a value they
// must delete is two contradictory instructions about one line.
//
// The table is every shape the old validator distinguished, and they now all produce
// exactly the same one message.
func TestRetiredJournalKeyHasNoShapeLeftToBeWrong(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	for _, body := range []string{
		`{"journal": "full"}`,
		`{"journal": "user"}`,
		`{"journal": "off"}`,
		`{"journal": true}`,
		`{"journal": "bogus"}`, // the old hard error
		`{"journal": 17}`,      // the old type error
		`{"journal": null}`,    // the old silent pass
	} {
		errs, _ := ValidateConfig(decode(t, body), t.TempDir(), nil)
		if len(containing(errs, "expected one of")) != 0 {
			t.Errorf("%s: errors = %v — a removed key has no vocabulary left to be outside of",
				body, errs)
		}
		if len(containing(errs, "REMOVED")) != 1 {
			t.Errorf("%s: errors = %v, want the refusal regardless of the value's shape "+
				"(including null, which the old validator accepted silently)", body, errs)
		}
	}
}

// The same asymmetry validateAgentsRetired carries, for the same measured reason:
// in-jail the config is the HOST-GENERATED snapshot, so an error there refuses every
// nested launch over a key the in-jail user cannot fix at its source — and it would
// make `yolo check` (which merges the real files) disagree with the launch.
func TestRetiredJournalKeyIsAWarningInsideAJail(t *testing.T) {
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	cfg := decode(t, `{"journal": "user"}`)
	errs, warns := ValidateConfig(cfg, t.TempDir(), nil)
	if len(containing(errs, "config.journal")) != 0 {
		t.Errorf("errors = %v — in-jail this must not refuse: the file is the host's "+
			"snapshot, not something the in-jail user typed", errs)
	}
	hits := containing(warns, "config.journal")
	if len(hits) != 1 {
		t.Fatalf("warnings = %v, want the downgraded notice", warns)
	}
	if !strings.Contains(hits[0], "HOST config") {
		t.Errorf("in-jail notice %q does not say where the key actually has to be removed, "+
			"which is the only actionable thing about it", hits[0])
	}
}

// TestTheInheritCensusStopsEmittingJournal closes the loop, and this key had further to
// fall than `host_processes` did: `journal` was classified into BOTH scopes right up
// until the conversion — the one key the census described as "a reserved loophole `yolo
// loopholes` reports and an inner launcher starts". Now it is a host ERROR, so a
// generated inner scope still carrying it would hand a nested launcher a config that
// refuses itself.
func TestTheInheritCensusStopsEmittingJournal(t *testing.T) {
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		for _, k := range InheritKeys(scope) {
			if k == "journal" {
				t.Errorf("the %s scope still emits journal — a nested launcher would read a "+
					"key this build refuses", scope)
			}
		}
	}
	if _, _, reason, ok := InheritDisposition("journal"); !ok || reason == "" {
		t.Error("journal has no census entry — a key in NO scope is still listed, with the " +
			"reason it is excluded, because \"assigned to neither\" has to be a decision on " +
			"the record rather than an omission")
	}
}

// AND CORE'S SCHEMA NOW NAMES NO LOOPHOLE, which is the whole point of the sprint
// rather than a consequence of it (loophole-activation.md §1.4). Both retired keys stay
// LISTED so each earns exactly one targeted message instead of a generic unknown-key
// error beside it — so "named" here means classified as live schema, which is what the
// census answers.
//
// Pinned as a property over the census rather than as two absences, so a THIRD loophole
// name creeping into core's schema fails here even though nobody thought to name it in
// a test.
func TestCoresSchemaNamesNoLoopholeInEitherInheritScope(t *testing.T) {
	loopholeShaped := map[string]string{
		"host_processes": "loopholes.host-processes.settings",
		"journal":        "loopholes.journal.enabled + .settings",
	}
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		for _, k := range InheritKeys(scope) {
			if replacement, isLoophole := loopholeShaped[k]; isLoophole {
				t.Errorf("the %s scope emits %q, a key that names ONE SPECIFIC LOOPHOLE. "+
					"Core's config schema is not supposed to know a loophole exists any more "+
					"— the values belong under %s, declared by that loophole's own manifest",
					scope, k, replacement)
			}
		}
	}
	// The live half: `loopholes` itself is still in both scopes, and must be. It is the
	// GENERIC key — it names no loophole — and dropping it would make `yolo loopholes
	// list` blind in-jail and leave an inner launcher with nothing to spawn from.
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		found := false
		for _, k := range InheritKeys(scope) {
			if k == "loopholes" {
				found = true
			}
		}
		if !found {
			t.Errorf("the %s scope no longer emits `loopholes` — the two retired keys moved "+
				"INTO it, so dropping it would take both capabilities with them", scope)
		}
	}
}
