package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// revertFixture is a scratch home with one pack owning one rmw surface, applied once so the
// provenance record exists — the state a revert is about.
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
func TestHostRevertRemovesOnlyWhatYoloWrote(t *testing.T) {
	_, surface, record := revertFixture(t, "assert")
	applyOnce(t)

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
// see this route: under `none`/`own` the ordinary apply refuses too, so only an ACTING revert
// distinguishes the wiring from its absence.
func TestApplyRevertActsAtTheHostNotch(t *testing.T) {
	_, surface, record := revertFixture(t, "assert")
	applyOnce(t)

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
func TestHostRevertMakesTheNextApplyAFirstApplyAgain(t *testing.T) {
	firstApplyReported := func(t *testing.T, revert bool) bool {
		t.Helper()
		revertFixture(t, "assert")
		applyOnce(t)
		if revert {
			var o, e bytes.Buffer
			if rc := hostMain([]string{"apply", "--revert", "--assert"}, &o, &e, false, nil); rc != 0 {
				t.Fatalf("revert rc=%d: %s%s", rc, o.String(), e.String())
			}
		}
		var out, errw bytes.Buffer
		if rc := applyMain([]string{"--at", "host", "--assert"}, &out, &errw, false, nil); rc != 0 {
			t.Fatalf("re-apply rc=%d: %s%s", rc, out.String(), errw.String())
		}
		return strings.Contains(out.String(), "first apply into this home")
	}

	var after, control bool
	t.Run("after a revert", func(t *testing.T) { after = firstApplyReported(t, true) })
	t.Run("control", func(t *testing.T) { control = firstApplyReported(t, false) })

	if !after {
		t.Error("the apply after a revert did not treat the home as a first apply — deleting " +
			"the provenance record is what restores that, and every guard keyed on " +
			"FirstApply stays disarmed without it")
	}
	if control {
		t.Error("a re-apply with NO revert also reported a first apply, so the assertion " +
			"above is measuring a string the report always prints rather than the deletion")
	}
}

// TestHostRevertIsRefusedUnderNoneAndOwn pins both refusals and both CALL SITES: `yolo host
// apply --revert` and `yolo apply --at host --revert` are one operation, so a contract that
// stopped one and not the other would have a way around it.
//
// The home is APPLIED FIRST, under `assert`, so there is genuinely something to revert when
// the key changes — otherwise a refusal and a no-op would be indistinguishable.
func TestHostRevertIsRefusedUnderNoneAndOwn(t *testing.T) {
	for _, mode := range []string{"none", "own"} {
		t.Run(mode, func(t *testing.T) {
			home, surface, record := revertFixture(t, "assert")
			applyOnce(t)
			before, err := os.ReadFile(surface)
			if err != nil {
				t.Fatal(err)
			}
			// Now declare the value that refuses, keeping the same pack.
			cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
			data, err := os.ReadFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, cfg, strings.Replace(string(data),
				`"host_management":"assert"`, `"host_management":"`+mode+`"`, 1))

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
					t.Fatalf("revert under %q: rc=%d, want 1\n%s%s",
						mode, rc, out.String(), errw.String())
				}
				if !strings.Contains(errw.String(), "host_management") ||
					!strings.Contains(errw.String(), mode) {
					t.Errorf("the refusal must name the key and the value that decided it:\n%s",
						errw.String())
				}
				// IT MUST BE THE REVERT'S OWN REFUSAL, not the render's. Both refuse under
				// these two values and both name the key, so without this the test passes
				// with the --revert route deleted and the command falling through to the
				// ordinary apply — measured, which is why the assertion is here.
				if !strings.Contains(errw.String(), "--revert") {
					t.Errorf("the refusal is the RENDER's, so a deleted --revert route would "+
						"be indistinguishable from a working one:\n%s", errw.String())
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
		})
	}
}

// TestHostRevertOnAHomeYoloNeverTouched: no record means nothing to revert, and saying so is
// not the same as reporting a clean removal of nothing.
func TestHostRevertOnAHomeYoloNeverTouched(t *testing.T) {
	_, surface, _ := revertFixture(t, "assert")
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
	writeFile(t, cfg, `{"packs":["claude"],"confinement":"host","host_management":"assert"}`)
	stubDeclaredBins(t)
	applyOnce(t)

	settings := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("setup wrote no claude settings: %v", err)
	}
	// The most complete drop there is.
	writeFile(t, cfg, `{"packs":[],"confinement":"host","host_management":"assert"}`)

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
// modifier of the render, so the two flags that belong to the render are refused by name
// rather than silently ignored. --shell-init is the one that matters — it WRITES, after the
// stage a revert replaces, so ignoring it would have a revert edit the user's shell rc.
func TestHostRevertRefusesTheFlagsItCannotShare(t *testing.T) {
	home, _, _ := revertFixture(t, "assert")
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
	revertFixture(t, "assert")
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--revert", "--at", "jail"}, &out, &errw, false, nil); rc != 2 {
		t.Fatalf("rc=%d, want 2\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "host") {
		t.Errorf("the refusal must name the notch the verb belongs to:\n%s", errw.String())
	}
}
