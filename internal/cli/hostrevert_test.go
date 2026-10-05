package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// revertFixture is a scratch home with one pack owning one rmw surface, applied once so the
// provenance record exists — the state a revert is about.
//
// mode is the contract the APPLY runs under, and since the `assert` retirement (OQ-CO14) that is
// "own": the one value that writes, which runs the rmw arm for this rmw-declared surface — the
// same writer and the same record a pre-retirement `assert` apply left. The REVERT then runs
// under the contract a test sets with setHostManagement: `none`, or the key unset, which is the
// state OQ-CO14 leaves a home `assert` wrote into.
//
// The surface deliberately carries all three kinds of key by the time the apply is done: one
// the user wrote and nothing declares (`myOwnKey`, recorded `host`), one the pack asserts
// (`telemetry`, recorded `managed`), and one yolo filled because the file did not have it
// (`fillMe`, recorded `defaults`). The three attributions are what the eligible-layer set is
// stated over, so a test that only had `managed` would pass with the set narrowed to it.
func revertFixture(t *testing.T, mode string) (home, surface, record string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")

	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"name":"rv","contributes":[
	  {"kind":"config","config":[{"agent":"rv","name":"settings","codec":"json",
	    "path":"~/.rv/settings.json","mode":"rmw",
	    "defaults":{"fillMe":"byYolo"},"managed":{"telemetry":false}}]}]}`)

	cfg := `{"packs":["file://` + packDir + `"],"confinement":"host"`
	if mode != "" {
		cfg += `,"host_management":"` + mode + `"`
	}
	cfg += `}`
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg)

	surface = filepath.Join(home, ".rv", "settings.json")
	writeFile(t, surface, `{"myOwnKey":"keep"}`)
	record = filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
		"rv-settings.provenance")
	return home, surface, record
}

// setHostManagement rewrites the user config's key to mode, keeping everything else; "" unsets
// it — `none` since OQ-CO14, and the state every home yolo asserted into is in after the upgrade.
func setHostManagement(t *testing.T, home, mode string) {
	t.Helper()
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("the fixture config is not JSON: %v\n%s", err, data)
	}
	if mode == "" {
		delete(m, "host_management")
	} else {
		m["host_management"] = mode
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfg, string(out))
}

// applyOnce runs the writing apply the revert will later undo.
func applyOnce(t *testing.T) {
	t.Helper()
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "host", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("setup apply rc=%d: %s%s", rc, out.String(), errw.String())
	}
}

// TestHostRevertRemovesOnlyWhatYoloWrote is the verb, end to end, and it is stated over the
// PROVENANCE RECORD rather than over a list of key names — the record is the authority
// (hostoverlayprune.go's rule, which this walk inherits), so the test reads it, checks it says
// what the revert will act on, and then checks the revert acted on exactly that.
//
// The `host` line is the safety property: a key the user set themselves is never eligible,
// however the revert widens the set otherwise.
//
// THE REVERT RUNS WITH THE KEY UNSET, which is the point since OQ-CO14: a home yolo wrote into
// and whose key nobody wrote reads as `none` now, and `--revert` is the one way back from there
// to a file purely the user's. It used to refuse under `none` and name "assert" as the way to
// reach it; deleting that refusal's replacement — the revert running under `none` — turns this
// red at the first rc check.
func TestHostRevertRemovesOnlyWhatYoloWrote(t *testing.T) {
	home, surface, record := revertFixture(t, "own")
	applyOnce(t)
	setHostManagement(t, home, "")

	rec, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("setup wrote no provenance record: %v", err)
	}
	for _, want := range []string{"myOwnKey\thost", "telemetry\tmanaged", "fillMe\tdefaults"} {
		if !strings.Contains(string(rec), want) {
			t.Fatalf("the fixture does not produce the attribution %q, so this test is not "+
				"measuring the eligible-layer set:\n%s", want, rec)
		}
	}

	// DRY RUN FIRST — the default posture, and the thing the user reads before asserting.
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--revert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("revert dry run rc=%d: %s%s", rc, out.String(), errw.String())
	}
	report := out.String()
	for _, want := range []string{"would remove telemetry", "would remove fillMe"} {
		if !strings.Contains(report, want) {
			t.Errorf("the dry run does not name %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "myOwnKey") {
		t.Errorf("the dry run offered to remove the USER's key:\n%s", report)
	}
	// The attribution rides each line: a `defaults` removal means something different to the
	// reader than a `managed` one, and the dry run IS the confirmation.
	if !strings.Contains(report, "(managed)") || !strings.Contains(report, "(defaults)") {
		t.Errorf("the dry run does not name the attribution each removal rests on:\n%s", report)
	}
	data, _ := os.ReadFile(surface)
	if !strings.Contains(string(data), "telemetry") {
		t.Errorf("the DRY RUN wrote to the surface:\n%s", data)
	}
	if _, err := os.Stat(record); err != nil {
		t.Errorf("the dry run deleted the provenance record: %v", err)
	}

	// ASSERT: the keys go, the user's stays, the record goes.
	out.Reset()
	errw.Reset()
	if rc := hostMain([]string{"apply", "--revert", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("revert --assert rc=%d: %s%s", rc, out.String(), errw.String())
	}
	data, err = os.ReadFile(surface)
	if err != nil {
		t.Fatalf("the revert deleted the user's file rather than emptying it: %v", err)
	}
	if strings.Contains(string(data), "telemetry") || strings.Contains(string(data), "fillMe") {
		t.Errorf("a key yolo wrote survived the revert:\n%s", data)
	}
	if !strings.Contains(string(data), `"myOwnKey": "keep"`) {
		t.Errorf("the user's own key did not survive the revert:\n%s", data)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Errorf("the provenance record survived the revert (err=%v) — an absent record is "+
			"how this tree spells \"yolo has never rendered here\"", err)
	}
}

// TestApplyRevertActsAtTheHostNotch is the SAME operation through the systematic spelling.
// Both are one verb (OQ-7), and it is pinned separately because the refusal path alone cannot
// see this route: under `none` the ordinary apply refuses, so only an ACTING revert distinguishes
// the wiring from its absence. The key is written "none" here, the explicit twin of the unset
// key the test above reverts under.
func TestApplyRevertActsAtTheHostNotch(t *testing.T) {
	home, surface, record := revertFixture(t, "own")
	applyOnce(t)
	setHostManagement(t, home, "none")

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "host", "--revert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("dry run rc=%d: %s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "would remove telemetry") {
		t.Errorf("`apply --at host --revert` did not report the revert:\n%s", out.String())
	}
	if _, err := os.Stat(record); err != nil {
		t.Errorf("the dry run through this spelling deleted the record: %v", err)
	}

	out.Reset()
	errw.Reset()
	if rc := applyMain([]string{"--at", "host", "--revert", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("assert rc=%d: %s%s", rc, out.String(), errw.String())
	}
	data, err := os.ReadFile(surface)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "telemetry") {
		t.Errorf("`apply --at host --revert --assert` rendered instead of reverting:\n%s", data)
	}
	if !strings.Contains(string(data), "myOwnKey") {
		t.Errorf("the user's key did not survive:\n%s", data)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Errorf("the record survived the revert through this spelling: %v", err)
	}
}

// TestHostRevertMakesTheNextApplyAFirstApplyAgain is why the record is DELETED rather than
// emptied, and it is the observable consequence: hostProvenanceExists decides FirstApply, and
// FirstApply is what gates every "this home already has content" behaviour — the loss
// confirmation among them. A revert that left an empty record behind would leave the home
// claiming a render it no longer has.
//
// THE ASSERTION IS THE FirstApply SIGNAL, not a prompt, and the distinction is one §4.4 warns
// about: confirmHostLosses is gated on `FirstApply && EntryLosses`, so a SCALAR whose value
// merely changes is reported as an ordinary ⚠ and prompts for nothing. Asserting a prompt
// here would have been asserting a behaviour the code does not have, for a reason unrelated
// to the record.
//
// The control is the second half: without a revert, a re-apply into the same home does NOT
// claim a first apply — so the assertion is measuring the deletion rather than a string the
// report always prints.
//
// The revert runs under `none` and the re-apply under `own`, the one round trip the two
// contracts left allow: `--revert` refuses under `own` (TestHostRevertIsRefusedUnderOwn).
//
// AND THE DEFAULTS COME BACK (CO-D13), which is what "a first apply again" has to mean for the
// user: the fixture adds a `stateful` surface carrying a declared default, composed under `own`
// with its capture store. Before CO-D13 the revert left that store's baseline and captures behind,
// so the owned apply after it read the reverted file as edits against the old baseline and wrote
// `"theme": null` — the default the revert took out, replayed as the user's deletion — while the
// report said "first apply".
func TestHostRevertMakesTheNextApplyAFirstApplyAgain(t *testing.T) {
	firstApplyReported := func(t *testing.T, revert bool) (bool, map[string]any) {
		t.Helper()
		home, _, _ := revertFixture(t, "own")
		addStatefulRevertSurface(t, home)
		applyOnce(t)
		if revert {
			setHostManagement(t, home, "none")
			var o, e bytes.Buffer
			if rc := hostMain([]string{"apply", "--revert", "--assert"}, &o, &e, false, nil); rc != 0 {
				t.Fatalf("revert rc=%d: %s%s", rc, o.String(), e.String())
			}
			if theme, left := readJSONMap(t, filepath.Join(home, ".rv", "state.json"))["theme"]; left {
				t.Fatalf("fixture premise — the revert takes the default out: theme = %v", theme)
			}
			setHostManagement(t, home, "own")
		}
		var out, errw bytes.Buffer
		if rc := applyMain([]string{"--at", "host", "--assert"}, &out, &errw, false, nil); rc != 0 {
			t.Fatalf("re-apply rc=%d: %s%s", rc, out.String(), errw.String())
		}
		return strings.Contains(out.String(), "first apply of a surface into this home"),
			readJSONMap(t, filepath.Join(home, ".rv", "state.json"))
	}

	var after, control bool
	var afterDoc map[string]any
	t.Run("after a revert", func(t *testing.T) { after, afterDoc = firstApplyReported(t, true) })
	t.Run("control", func(t *testing.T) { control, _ = firstApplyReported(t, false) })

	if !after {
		t.Error("the apply after a revert did not treat the home as a first apply — deleting " +
			"the provenance record is what restores that, and every guard keyed on " +
			"FirstApply stays disarmed without it")
	}
	if control {
		t.Error("a re-apply with NO revert also reported a first apply, so the assertion " +
			"above is measuring a string the report always prints rather than the deletion")
	}
	if afterDoc["theme"] != "system" {
		t.Errorf("the owned apply after the revert did not write the declared default back: %v — "+
			"the revert left the capture store, and the apply replayed its removal as yours", afterDoc)
	}
}

// addStatefulRevertSurface adds to revertFixture's pack a surface declaring no mode — `stateful`,
// composed whole under `own` with a capture store — carrying one declared default.
func addStatefulRevertSurface(t *testing.T, home string) {
	t.Helper()
	cfg := readJSONMap(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"))
	packs, _ := cfg["packs"].([]any)
	src, _ := packs[0].(string)
	packDir := strings.TrimPrefix(src, "file://")
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"name":"rv","contributes":[
	  {"kind":"config","config":[{"agent":"rv","name":"settings","codec":"json",
	    "path":"~/.rv/settings.json","mode":"rmw",
	    "defaults":{"fillMe":"byYolo"},"managed":{"telemetry":false}},
	   {"agent":"rv","name":"state","codec":"json","path":"~/.rv/state.json",
	    "defaults":{"theme":"system"}}]}]}`)
}

// readJSONMap decodes a JSON object file.
func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, data)
	}
	return m
}

// TestHostRevertIsRefusedUnderOwn pins the refusal and both CALL SITES: `yolo host apply
// --revert` and `yolo apply --at host --revert` are one operation, so a contract that stopped one
// and not the other would have a way around it.
//
// It was TestHostRevertIsRefusedUnderNoneAndOwn. `none` left the loop with the `assert`
// retirement (OQ-CO14): the revert RUNS there now, since a home `assert` wrote into reads as
// `none` and the revert is its one way back (TestHostRevertRemovesOnlyWhatYoloWrote). What is left
// is `own`, and its refusal is the message OQ-CO14 rewrote: it named "assert" as the way to a
// key-level revert, and names "none" now — never the retired value.
//
// The home is APPLIED FIRST, so there is genuinely something to revert — otherwise a refusal and
// a no-op would be indistinguishable.
func TestHostRevertIsRefusedUnderOwn(t *testing.T) {
	_, surface, record := revertFixture(t, "own")
	applyOnce(t)
	before, err := os.ReadFile(surface)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []func(o, e *bytes.Buffer) int{
		func(o, e *bytes.Buffer) int {
			return hostMain([]string{"apply", "--revert", "--assert"}, o, e, false, nil)
		},
		func(o, e *bytes.Buffer) int {
			return applyMain([]string{"--at", "host", "--revert", "--assert"}, o, e, false, nil)
		},
	} {
		var out, errw bytes.Buffer
		if rc := run(&out, &errw); rc != 1 {
			t.Fatalf("revert under own: rc=%d, want 1\n%s%s", rc, out.String(), errw.String())
		}
		msg := errw.String()
		for _, want := range []string{"host_management", `is "own"`, `set the key to "none"`} {
			if !strings.Contains(msg, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, msg)
			}
		}
		if strings.Contains(msg, "assert") {
			t.Errorf("the refusal still names the retired \"assert\":\n%s", msg)
		}
		// IT MUST BE THE REVERT'S OWN REFUSAL, not the render's: without this the test passes
		// with the --revert route deleted and the command falling through to the ordinary
		// apply — measured, which is why the assertion is here.
		if !strings.Contains(msg, "--revert") {
			t.Errorf("the refusal is the RENDER's, so a deleted --revert route would "+
				"be indistinguishable from a working one:\n%s", msg)
		}
	}
	after, err := os.ReadFile(surface)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("a refused revert wrote to the surface:\n%s", after)
	}
	if _, err := os.Stat(record); err != nil {
		t.Errorf("a refused revert deleted the provenance record: %v", err)
	}
}

// TestHostRevertOnAHomeYoloNeverTouched: no record means nothing to revert, and saying so is
// not the same as reporting a clean removal of nothing.
func TestHostRevertOnAHomeYoloNeverTouched(t *testing.T) {
	_, surface, _ := revertFixture(t, "")
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--revert", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("rc=%d: %s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "never rendered") {
		t.Errorf("a home yolo never rendered into should say so:\n%s", out.String())
	}
	data, _ := os.ReadFile(surface)
	if string(data) != `{"myOwnKey":"keep"}` {
		t.Errorf("the revert touched a file yolo has never written:\n%s", data)
	}
}

// TestHostRevertFindsSurfacesAfterThePackIsDropped is why the candidate set includes the
// packs yolo SHIPS and not only the configured ones. A user withdrawing yolo has every
// reason to have already emptied `packs` — and the record lives beside a surface its OWNER
// declares, so without the shipped set the revert would find no surfaces and report a clean
// home while every key yolo wrote was still in the file.
//
// It uses a SHIPPED pack (claude) because that is the set the fallback covers; a `file://`
// pack dropped from config is genuinely unreachable and keeps its keys, which is the honest
// limit rather than a defect.
func TestHostRevertFindsSurfacesAfterThePackIsDropped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	writeFile(t, cfg, `{"packs":["claude"],"confinement":"host","host_management":"own",`+
		`"host_wrappers":false}`)
	stubDeclaredBins(t)
	applyOnce(t)

	settings := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("setup wrote no claude settings: %v", err)
	}
	// The most complete drop there is — packs emptied, and the key with them (`none`).
	writeFile(t, cfg, `{"packs":[],"confinement":"host"}`)

	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--revert", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("rc=%d: %s%s", rc, out.String(), errw.String())
	}
	if strings.Contains(out.String(), "never rendered") {
		t.Fatalf("the revert found no surfaces after `packs` was emptied — every key yolo "+
			"wrote is still in the home:\n%s", out.String())
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"permissions", "skipDangerousModePermissionPrompt"} {
		if strings.Contains(string(data), gone) {
			t.Errorf("a key yolo asserted survived the revert after the pack was dropped "+
				"(%s):\n%s", gone, data)
		}
	}
}

// TestHostRevertRefusesTheFlagsItCannotShare: --revert is a different OPERATION, not a
// modifier of the render, so a flag that belongs to the render is refused by name rather than
// silently ignored. --shell-init used to be the one that mattered — it WROTE, after the stage a
// revert replaces — and it is removed now (HE-D1), so it refuses on its own; the case stays so
// that a revert beside it still edits no shell rc.
func TestHostRevertRefusesTheFlagsItCannotShare(t *testing.T) {
	home, _, _ := revertFixture(t, "")
	rc := filepath.Join(home, ".bashrc")
	writeFile(t, rc, "# untouched\n")
	t.Setenv("SHELL", "/bin/bash")

	for _, argv := range [][]string{
		{"apply", "--revert", "--assert", "--shell-init"},
		{"apply", "--revert", "--format", "json"},
	} {
		var out, errw bytes.Buffer
		if got := hostMain(argv, &out, &errw, false, nil); got != 2 {
			t.Errorf("host %v: rc=%d, want 2 (misuse)\n%s%s", argv, got, out.String(), errw.String())
		}
	}
	data, _ := os.ReadFile(rc)
	if string(data) != "# untouched\n" {
		t.Errorf("a refused `--revert --shell-init` edited the user's shell rc:\n%s", data)
	}
}

// TestApplyRevertIsTheHostNotchs: the verb consumes the HOST provenance record, which is the
// only notch that keeps one. Naming that beats silently reverting nothing at the jail notch.
func TestApplyRevertIsTheHostNotchs(t *testing.T) {
	revertFixture(t, "")
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--revert", "--at", "jail"}, &out, &errw, false, nil); rc != 2 {
		t.Fatalf("rc=%d, want 2\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "host") {
		t.Errorf("the refusal must name the notch the verb belongs to:\n%s", errw.String())
	}
}

// A REVERT KEEPS PI'S `providers`, through the verb a user types. pi/models declares
// `"providers": {}` because pi rejects a models.json without it (HC-D1), and a revert of a home
// whose models.json the apply created used to leave `{}`, which pi reports at every start. The
// dry run names the key as kept, the --assert leaves an object `providers`, and the count the
// report closes on is still only what it removed.
func TestHostRevertKeepsAnEmptyDefaultAndSaysSo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["pi"],"host_management":"own","host_wrappers":false}`)
	stubDeclaredBins(t)
	applyOnce(t)
	setHostManagement(t, home, "none")
	models := filepath.Join(home, ".pi", "agent", "models.json")

	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--revert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("revert dry run rc=%d: %s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "keeps providers") {
		t.Errorf("the dry run does not say it keeps pi/models' `providers`:\n%s", out.String())
	}
	if strings.Contains(out.String(), "would remove providers") {
		t.Errorf("the dry run offers to remove `providers`:\n%s", out.String())
	}

	out.Reset()
	errw.Reset()
	if rc := hostMain([]string{"apply", "--revert", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("revert --assert rc=%d: %s%s", rc, out.String(), errw.String())
	}
	data, err := os.ReadFile(models)
	if err != nil {
		t.Fatalf("the revert deleted models.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("models.json after the revert: %v\n%s", err, data)
	}
	if _, isObj := doc["providers"].(map[string]any); !isObj {
		t.Errorf("after the revert models.json has no object `providers`, which pi rejects at "+
			"every start:\n%s", data)
	}
	if !strings.Contains(out.String(), "keeps providers") {
		t.Errorf("the revert does not say it kept `providers`:\n%s", out.String())
	}
}
