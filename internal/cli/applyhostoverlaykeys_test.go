package cli

// applyhostoverlaykeys_test.go pins ruling R3's first sentence at the COMMAND level: the
// `config-overlay` keys of a pack that LEFT `packs` are removed from the user's config file,
// behind the same confirmation the skills/files retirement rides.
//
// It has to be the command level, because the defect is structural rather than a wrong branch:
// nothing ever asked about a pack absent from `entries`. Only a run of the whole verb with a
// config the pack has left can catch that.
//
// THE MOST IMPORTANT TEST IN THE FILE is TestApplyHostKeepsUserKeySharingAPackKeyName. A key
// removal reads a record to decide whether a value in the user's own file is yolo's output, and
// the direction of that mistake that costs something is removing a key the user typed. The
// record says `host` for such a key, and nothing may look past that.
//
// Every test uses a t.TempDir() home with XDG_CONFIG_HOME inside it. The real $HOME is never
// read or written.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// overlayDropPackJSON is the reproduction from the spec: a pack contributing one
// config-overlay key to a surface the SHIPPED claude pack owns, plus a `files` tree the key
// points at. Both halves matter — the shared prompt has to cover them together.
const overlayDropPackJSON = `{"name":"dropme","description":"d","contributes":[
  {"kind":"files","from":"bin","into":".claude/bin"},
  {"kind":"config-overlay","surface":"claude/settings",
   "config":{"managed":{"fileSuggestion":{"type":"command","command":"~/.claude/bin/pick.sh"}}}}]}`

// overlayDropFixture writes the pack tree and a user config selecting `claude` plus the
// contributor, and returns the home. Selecting claude is not decoration: the overlay targets
// claude/settings, so without it the overlay correctly refuses as having no owner.
func overlayDropFixture(t *testing.T, packJSON string) string {
	t.Helper()
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "dropme")
	writeFile(t, filepath.Join(packDir, "pack.json"), packJSON)
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Dropme prose.\n")
	writeFile(t, filepath.Join(packDir, "bin", "pick.sh"), "#!/bin/sh\necho pick\n")

	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"dropme"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// hostSettingsPath is the file the overlay lands in — claude/settings' host destination.
func hostSettingsPath(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}

// settingsKeys decodes the rendered settings file into a key → value map, so a test asserts
// about KEYS rather than substrings (a substring check would pass on a key that survived inside
// a comment or another value).
func settingsKeys(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(hostSettingsPath(home))
	if err != nil {
		t.Fatalf("reading the rendered settings: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("the rendered settings are not valid JSON (%v):\n%s", err, data)
	}
	return out
}

// hostProvenanceRecord reads the per-key winning-layer record for one surface in a test home.
func hostProvenanceRecord(t *testing.T, home, agent, name string) map[string]string {
	t.Helper()
	path := filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
		agent+"-"+name+".provenance")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, layer, found := strings.Cut(line, "\t")
		if found && key != "" {
			out[key] = layer
		}
	}
	return out
}

// sentinelSettings is a settings file the USER wrote by hand, carrying a key no pack declares.
// Its survival is what "pure RMW" means, asserted separately from the removal itself: a prune
// that removed the right key and ate a neighbour would be no better than the leak.
const sentinelSettings = `{"mySentinel":"do-not-touch"}`

// applyThenDropOverlay applies with the contributor selected (so the key lands and is recorded),
// then rewrites the config without it — the lifecycle the whole feature is about. Returns the
// value the key had while the pack was active, for a before/after comparison.
func applyThenDropOverlay(t *testing.T, home string) {
	t.Helper()
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	if _, present := settingsKeys(t, home)["fileSuggestion"]; !present {
		t.Fatalf("the overlay key never landed, so there is nothing to retire:\n%s",
			mustReadFile(t, hostSettingsPath(home)))
	}
	if got := hostProvenanceRecord(t, home, "claude", "settings")["fileSuggestion"]; got !=
		"config-overlay:dropme" {
		t.Fatalf("the key must be attributed to the contributing pack before the drop, got %q "+
			"— the whole prune reads that attribution", got)
	}
	selectPacks(t, home, `"claude"`)
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// THE GOAL. The dropped pack's overlay key is gone from the user's settings after a confirm,
// the user's own key is untouched, and the record stops naming the removed key.
func TestApplyHostRemovesDroppedPackOverlayKeyOnConfirm(t *testing.T) {
	home := overlayDropFixture(t, overlayDropPackJSON)
	writeFile(t, hostSettingsPath(home), sentinelSettings)
	applyThenDropOverlay(t, home)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("a confirmed retire rc=%d\n%s", rc, report)
	}
	keys := settingsKeys(t, home)
	if _, present := keys["fileSuggestion"]; present {
		t.Errorf("the orphaned overlay key survived its pack leaving `packs`:\n%s",
			mustReadFile(t, hostSettingsPath(home)))
	}
	// (a) The user's OWN key survives untouched. Pure RMW is the contract; a removal pass that
	// broke it would be worse than the leak it fixes.
	if got := keys["mySentinel"]; got != "do-not-touch" {
		t.Errorf("the user's own key must survive a key retirement, got %v", got)
	}
	// The owner pack's own managed keys are still asserted — this run RENDERED claude/settings,
	// and the prune must not have raced or undone that.
	if _, present := keys["preferences"]; !present {
		t.Errorf("the owning pack's managed keys must still be asserted:\n%s",
			mustReadFile(t, hostSettingsPath(home)))
	}
	// The record must stop attributing a key that is no longer in the file, or the next apply
	// reports an orphan that does not exist.
	if got, present := hostProvenanceRecord(t, home, "claude", "settings")["fileSuggestion"]; present {
		t.Errorf("the record still attributes the removed key to %q", got)
	}
	for _, want := range []string{"fileSuggestion", "dropme", "no longer configured"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report must name %q:\n%s", want, report)
		}
	}
}

// A KEY-ONLY DROP STILL PROMPTS. The pack that left contributed an overlay and nothing else, so
// there is no path to retire — and a gate keyed on "is there a path?" would let the key removal
// through silently, which is the one outcome R3 rules out ("it rides the same gate rather than a
// separate silent path"). The key half must be able to raise the prompt by itself.
func TestApplyHostKeyOnlyDropStillAsksBeforeRemoving(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "keyonly")
	// No `files`, no `skills` — an overlay and nothing else.
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"keyonly","description":"d","contributes":[
		  {"kind":"config-overlay","surface":"claude/settings",
		   "config":{"managed":{"fileSuggestion":{"type":"command","command":"~/x.sh"}}}}]}`)
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "prose\n")
	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"keyonly"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeFile(t, hostSettingsPath(home), sentinelSettings)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	if _, present := settingsKeys(t, home)["fileSuggestion"]; !present {
		t.Fatal("the overlay key never landed, so there is nothing to retire")
	}
	selectPacks(t, home, `"claude"`)

	// With NO stdin, the key must survive: a key-only drop is gated exactly like a path.
	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	// The gate's WARNING block is the observable: promptYesNo short-circuits on a nil stdin
	// before it writes the "[y/N]" line, so what proves the gate was reached is the heading it
	// prints first, naming the key. A key-only drop reaching this means the prompt is not keyed
	// on there being a path.
	if !strings.Contains(report, "no longer in your config") ||
		!strings.Contains(report, "fileSuggestion") {
		t.Errorf("a key-only drop must raise the confirmation by itself, not slip through "+
			"a path-keyed gate:\n%s", report)
	}
	if _, present := settingsKeys(t, home)["fileSuggestion"]; !present {
		t.Errorf("an unconfirmed key-only retire removed the key:\n%s",
			mustReadFile(t, hostSettingsPath(home)))
	}
	// And a yes removes it.
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("confirmed apply rc=%d\n%s", rc, report)
	}
	if _, present := settingsKeys(t, home)["fileSuggestion"]; present {
		t.Errorf("a confirmed key-only retire left the key:\n%s",
			mustReadFile(t, hostSettingsPath(home)))
	}
	if got := settingsKeys(t, home)["mySentinel"]; got != "do-not-touch" {
		t.Errorf("the user's own key changed, got %v", got)
	}
}

// DECLINING LEAVES EVERYTHING. The explicit-no path: the key stays, the file is byte-identical
// to what the render left, and the run says so.
func TestApplyHostKeepsOverlayKeyOnDecline(t *testing.T) {
	home := overlayDropFixture(t, overlayDropPackJSON)
	writeFile(t, hostSettingsPath(home), sentinelSettings)
	applyThenDropOverlay(t, home)

	rc, report := applyWith(t, true, strings.NewReader("n\n"))
	keys := settingsKeys(t, home)
	if _, present := keys["fileSuggestion"]; !present {
		t.Errorf("a declined retire removed the key anyway:\n%s",
			mustReadFile(t, hostSettingsPath(home)))
	}
	if got := keys["mySentinel"]; got != "do-not-touch" {
		t.Errorf("the user's own key changed on a declined retire, got %v", got)
	}
	// The rc is deliberately unchanged: declining is an answer, not a failure. (Same reasoning
	// as the path half — a permanent non-zero would make every scripted apply after a drop look
	// broken, with no non-interactive way to answer.)
	if rc != 0 {
		t.Errorf("a declined retire must not be an error, rc=%d\n%s", rc, report)
	}
	if !strings.Contains(report, "still in your home") {
		t.Errorf("a decline must report what was left:\n%s", report)
	}
}

// FAIL-CLOSED. No stdin — a CI or scripted `apply --host --assert` — removes nothing. A
// confirmation nobody can answer must not default to editing a real config file.
func TestApplyHostOverlayKeyRemovalFailsClosedWithoutStdin(t *testing.T) {
	home := overlayDropFixture(t, overlayDropPackJSON)
	applyThenDropOverlay(t, home)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("a fail-closed retire must not be an error, rc=%d\n%s", rc, report)
	}
	if _, present := settingsKeys(t, home)["fileSuggestion"]; !present {
		t.Errorf("with no stdin to confirm, the key must be left alone:\n%s",
			mustReadFile(t, hostSettingsPath(home)))
	}
}

// OBSERVE REPORTS AND WRITES NOTHING. The dry run is what gives the user the information before
// any prompt exists, so it has to name the key and the pack — and leave the file byte-identical.
func TestApplyHostObserveReportsOverlayKeyWithoutRemoving(t *testing.T) {
	home := overlayDropFixture(t, overlayDropPackJSON)
	applyThenDropOverlay(t, home)
	before := mustReadFile(t, hostSettingsPath(home))

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("observe rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("observe must not prompt — it writes nothing:\n%s", report)
	}
	for _, want := range []string{"would remove key", "fileSuggestion", "dropme"} {
		if !strings.Contains(report, want) {
			t.Errorf("observe must name %q:\n%s", want, report)
		}
	}
	if after := mustReadFile(t, hostSettingsPath(home)); after != before {
		t.Errorf("observe modified the file:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

// THE MOST IMPORTANT TEST HERE. A key the USER set, whose name a pack's overlay also uses, is
// NOT removed — the record says `host`, and that is the whole safety property. Being wrong in
// this direction deletes something the user typed and yolo never wrote.
//
// The provenance record is CONSTRUCTED rather than round-tripped through two applies, because
// the state being tested is precisely "the user owns this key" and there is no apply sequence
// that produces it while a pack is also declaring the same name.
func TestApplyHostKeepsUserKeySharingAPackKeyName(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"claude"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// The user's own fileSuggestion — the same key name overlayDropPackJSON's overlay uses.
	writeFile(t, hostSettingsPath(home),
		`{"mySentinel":"do-not-touch","fileSuggestion":{"type":"command","command":"~/bin/mine.sh"}}`)
	// A record attributing it to the USER, in a home where `dropme` is not configured. This is
	// the shape of every hand-written key yolo has ever rendered alongside.
	writeFile(t, filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
		"claude-settings.provenance"),
		"fileSuggestion\thost\nmySentinel\thost\npreferences\tmanaged\n")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	keys := settingsKeys(t, home)
	got, present := keys["fileSuggestion"]
	if !present {
		t.Fatalf("the USER's own key was removed — a `host` attribution is the one thing this "+
			"pass may never look past:\n%s", mustReadFile(t, hostSettingsPath(home)))
	}
	obj, isObj := got.(map[string]any)
	if !isObj || obj["command"] != "~/bin/mine.sh" {
		t.Errorf("the user's own VALUE must survive intact, got %v", got)
	}
	if got := keys["mySentinel"]; got != "do-not-touch" {
		t.Errorf("the user's other key changed, got %v", got)
	}
	// And it is not merely left in place — it is never even OFFERED for removal, so the user is
	// not asked to defend their own key.
	if strings.Contains(report, "would remove key") || strings.Contains(report, "removed key") {
		t.Errorf("a `host` key must not appear in the retirement report at all:\n%s", report)
	}
}

// A KEY ANOTHER SELECTED PACK STILL CONTRIBUTES IS NOT AN ORPHAN — and the posture this bites in
// is OBSERVE, which is why it is asserted there.
//
// The record holds ONE winner per key, so two packs contributing the same key leave only the
// last named. Drop THAT one and the record says `config-overlay:<dropped>` for a key the
// surviving pack is still setting. An assert self-corrects (its render rewrites the record with
// the live attribution before the prune reads it), but observe writes no record — so a prune
// that trusted the record alone would print "would remove" for a key the very next --assert
// keeps. A dry run that predicts a removal that will not happen is worse than no preview.
func TestApplyHostKeepsOverlayKeyAnotherPackStillContributes(t *testing.T) {
	home := t.TempDir()
	keeperDir := filepath.Join(t.TempDir(), "keeper")
	writeFile(t, filepath.Join(keeperDir, "pack.json"),
		`{"name":"keeper","description":"d","contributes":[
		  {"kind":"config-overlay","surface":"claude/settings",
		   "config":{"managed":{"fileSuggestion":{"type":"command","command":"~/bin/keeper.sh"}}}}]}`)
	writeFile(t, filepath.Join(keeperDir, "AGENTS.md"), "Keeper prose.\n")
	selectPacks(t, home, `"claude",{"source":"file://`+keeperDir+`","name":"keeper"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	writeFile(t, hostSettingsPath(home),
		`{"mySentinel":"do-not-touch","fileSuggestion":{"type":"command","command":"~/bin/keeper.sh"}}`)
	// The record names DROPME as the key's last winner (it was the later pack, so it won the
	// attribution) — and dropme is gone from the config while keeper remains.
	writeFile(t, filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
		"claude-settings.provenance"), "fileSuggestion\tconfig-overlay:dropme\n")

	// OBSERVE: the stale attribution must not become a predicted removal.
	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("observe rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "would remove key fileSuggestion") {
		t.Errorf("a key a SELECTED pack still contributes must not be predicted for removal — "+
			"the record's one-winner-per-key coarseness named a dropped pack:\n%s", report)
	}
	// And the assert that follows keeps it, with the surviving pack's value.
	rc, report = applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	got, present := settingsKeys(t, home)["fileSuggestion"]
	if !present {
		t.Fatalf("a key a SELECTED pack still contributes was removed:\n%s",
			mustReadFile(t, hostSettingsPath(home)))
	}
	obj, isObj := got.(map[string]any)
	if !isObj || obj["command"] != "~/bin/keeper.sh" {
		t.Errorf("the surviving pack's value must be what is in the file, got %v", got)
	}
}

// A RETIRED attribution is still eligible. After the drop, the render relabels the key
// `retired:config-overlay:dropme` (the anti-laundering pass), so a prune that accepted only the
// live spelling would work in observe and then find nothing in the very apply that writes.
func TestApplyHostRemovesRetiredAttributedOverlayKey(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"claude"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	writeFile(t, hostSettingsPath(home),
		`{"mySentinel":"do-not-touch","fileSuggestion":{"type":"command","command":"~/x.sh"}}`)
	writeFile(t, filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
		"claude-settings.provenance"),
		"fileSuggestion\tretired:config-overlay:dropme\nmySentinel\thost\n")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	if _, present := settingsKeys(t, home)["fileSuggestion"]; present {
		t.Errorf("a `retired:config-overlay:<pack>` key must be retirable — it is the label the "+
			"record carries on every apply AFTER the drop:\n%s",
			mustReadFile(t, hostSettingsPath(home)))
	}
	if got := settingsKeys(t, home)["mySentinel"]; got != "do-not-touch" {
		t.Errorf("the user's own key changed, got %v", got)
	}
	if !strings.Contains(report, "dropme") {
		t.Errorf("the report must still name the pack a retired label attributes to:\n%s", report)
	}
}

// NO RECORD AT ALL means yolo has never asserted this surface in this home, so it has nothing
// here to retire. Absent-means-never-rendered is the reading the whole first-apply signal rests
// on, and a prune that read it as "nothing is attributed, so every key is fair game" would
// delete a hand-written config on the first ever apply.
func TestApplyHostRetiresNothingWithoutAProvenanceRecord(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"claude"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeFile(t, hostSettingsPath(home),
		`{"mySentinel":"do-not-touch","fileSuggestion":{"type":"command","command":"~/x.sh"}}`)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	keys := settingsKeys(t, home)
	for _, want := range []string{"mySentinel", "fileSuggestion"} {
		if _, present := keys[want]; !present {
			t.Errorf("%q was removed with no record to authorize it:\n%s", want,
				mustReadFile(t, hostSettingsPath(home)))
		}
	}
}

// ONE PROMPT, NOT TWO (ruling R3: overlay keys ride "the same confirm"). A drop that orphans
// BOTH a file and a key asks once, and the single prompt names both.
func TestApplyHostAsksOnceForBothPathsAndKeys(t *testing.T) {
	home := overlayDropFixture(t, overlayDropPackJSON)
	applyThenDropOverlay(t, home)
	file := filepath.Join(home, ".claude", "bin", "pick.sh")
	mustExist(t, file, "the first apply delivered it")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	if n := strings.Count(report, "[y/N]"); n != 1 {
		t.Errorf("a single edit to `packs` must produce exactly ONE confirmation, got %d:\n%s",
			n, report)
	}
	// The one prompt covers both halves: the path and the key are both named in it.
	// Slice out the prompt region ONLY — heading through the [y/N] line. Checking the whole
	// report would pass on a prompt that named neither, since the removal lines printed
	// AFTERWARDS mention both; what matters is that the user saw them BEFORE answering.
	prompt := report
	start := strings.Index(report, "no longer in your config")
	end := strings.Index(report, "[y/N]")
	if start < 0 || end < start {
		t.Fatalf("the prompt region is missing from the report:\n%s", report)
	}
	prompt = report[start:end]
	for _, want := range []string{"pick.sh", "fileSuggestion"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt must name %q so the user knows what they are agreeing to:\n%s",
				want, prompt)
		}
	}
	// And a yes retires both.
	mustNotExist(t, file, "the user confirmed")
	if _, present := settingsKeys(t, home)["fileSuggestion"]; present {
		t.Errorf("the key survived a confirmed retire:\n%s",
			mustReadFile(t, hostSettingsPath(home)))
	}
}

// A SECOND apply after a retirement is a NO-OP and does not re-report. The record was updated,
// so there is nothing left to find — a prune that kept reporting a key it already removed would
// prompt forever, which is the "trains people to hit y blind" failure.
func TestApplyHostDoesNotReRetireAnAlreadyRemovedKey(t *testing.T) {
	home := overlayDropFixture(t, overlayDropPackJSON)
	applyThenDropOverlay(t, home)
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("first retire rc=%d\n%s", rc, report)
	}
	before := mustReadFile(t, hostSettingsPath(home))

	rc, report := applyWith(t, true, nil) // nil stdin: nothing may need confirming
	if rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "fileSuggestion") {
		t.Errorf("a key already retired must not be reported again:\n%s", report)
	}
	if after := mustReadFile(t, hostSettingsPath(home)); after != before {
		t.Errorf("the second apply changed the file:\n--- before\n%s\n--- after\n%s",
			before, after)
	}
}
