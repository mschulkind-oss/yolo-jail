package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostManagementFixture builds a scratch home carrying one pack that owns one rmw surface,
// plus a hand-written user key in that surface's real file — the shape §11's non-regression
// criterion is stated over ("an undeclared key is left byte-identical").
//
// It returns the home, the pack dir and the surface path. mode is written into the user
// config verbatim; "" leaves the key unset, which is the state every existing user is in.
func hostManagementFixture(t *testing.T, mode string) (home, surface string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")

	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"name":"hm","contributes":[
	  {"kind":"config","config":[{"agent":"hm","name":"settings","codec":"json","path":"~/.hm/settings.json","mode":"rmw","managed":{"telemetry":false}}]}]}`)

	cfg := `{"packs":["file://` + packDir + `"],"confinement":"host"`
	if mode != "" {
		cfg += `,"host_management":"` + mode + `"`
	}
	cfg += `}`
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg)

	surface = filepath.Join(home, ".hm", "settings.json")
	writeFile(t, surface, `{"myOwnKey":"keep","telemetry":true}`)
	return home, surface
}

// TestHostManagementAssertIsByteIdenticalToUnset is §11's non-regression criterion, and the
// one thing this change must not break: declaring the value that ALREADY describes today's
// behavior must not change a single byte of what an apply writes.
//
// The proof is a snapshot on both sides of the same apply, from identical starting files:
// render with the key unset, keep the bytes, reset the file, render with `"assert"` written,
// compare. Comparing against a hand-written expectation would test my idea of the render;
// comparing the two renders tests the thing the criterion is about.
//
// It also pins the narrower half the criterion names: the user's own key, which no layer
// declares, survives byte-for-byte in both.
func TestHostManagementAssertIsByteIdenticalToUnset(t *testing.T) {
	const userKey = `{"myOwnKey":"keep","telemetry":true}`

	// BOTH artifacts an apply writes for a surface, not just the file: the rendered bytes and
	// the provenance record beside them. The record is what step 3's --revert consumes and
	// what `yolo config diff` annotates from, so an `assert` that rendered the same file with
	// a different record would satisfy a narrower criterion than the one §11 states.
	render := func(t *testing.T, mode string) (string, string) {
		t.Helper()
		home, surface := hostManagementFixture(t, mode)
		writeFile(t, surface, userKey)
		var out, errw bytes.Buffer
		if rc := applyMain([]string{"--at", "host", "--assert"}, &out, &errw, false, nil); rc != 0 {
			t.Fatalf("apply --at host --assert (host_management %q) rc=%d: %s%s",
				mode, rc, out.String(), errw.String())
		}
		data, err := os.ReadFile(surface)
		if err != nil {
			t.Fatal(err)
		}
		record, err := os.ReadFile(filepath.Join(home, ".local", "share", "yolo-jail",
			"host-provenance", "hm-settings.provenance"))
		if err != nil {
			t.Fatalf("host_management %q wrote no provenance record: %v", mode, err)
		}
		return string(data), string(record)
	}

	var unset, asserted, unsetRec, assertedRec string
	t.Run("unset", func(t *testing.T) { unset, unsetRec = render(t, "") })
	t.Run("assert", func(t *testing.T) { asserted, assertedRec = render(t, "assert") })

	if unset == "" {
		t.Fatal("the unset render produced nothing to compare")
	}
	if unset != asserted {
		t.Errorf("declaring host_management \"assert\" changed the rendered bytes — §11's "+
			"one non-regression criterion.\n--- unset ---\n%s\n--- assert ---\n%s",
			unset, asserted)
	}
	if unsetRec != assertedRec {
		t.Errorf("declaring host_management \"assert\" changed the provenance record.\n"+
			"--- unset ---\n%s\n--- assert ---\n%s", unsetRec, assertedRec)
	}
	// The record must attribute BOTH keys, or the comparison above is over an empty file:
	// the user's own key as `host`, the pack's as `managed`. This is the record --revert
	// consumes, and its `host` line is the one revert may never touch.
	for _, want := range []string{"myOwnKey\thost", "telemetry\tmanaged"} {
		if !strings.Contains(assertedRec, want) {
			t.Errorf("the provenance record is missing %q:\n%s", want, assertedRec)
		}
	}
	// The undeclared key, verbatim. `rmw` rewrites only what the pack declares.
	if !strings.Contains(asserted, `"myOwnKey": "keep"`) {
		t.Errorf("the user's undeclared key did not survive the apply:\n%s", asserted)
	}
	// And yolo's own declared key WAS regenerated, or "byte-identical" would be trivially
	// true because nothing rendered at all.
	if !strings.Contains(asserted, `"telemetry": false`) {
		t.Errorf("the pack's managed key was not regenerated, so this test is comparing two "+
			"no-ops rather than two renders:\n%s", asserted)
	}
}

// TestHostManagementNoneRefusesBothSpellings pins the CALL SITES. Deleting either
// refuseHostManagement line leaves the other spelling refusing and this test red, which is
// the point: `yolo host apply` and `yolo apply --at host` are one operation (OQ-7), so a key
// that stopped one and not the other would be a contract with a way around it.
//
// `own` WAS IN THIS LOOP and is deliberately not any more: it refused only because whole-file
// composition at the host notch was unbuilt, and it is built
// (docs/design/config-ownership-and-promotion.md §10's last step). What replaces its row here
// is TestHostManagementOwnApplies, one function down — the same fixture, asserting the
// opposite outcome, so "own no longer refuses" is a statement something checks rather than a
// row that quietly disappeared.
func TestHostManagementNoneRefusesBothSpellings(t *testing.T) {
	for _, mode := range []string{"none"} {
		t.Run(mode, func(t *testing.T) {
			_, surface := hostManagementFixture(t, mode)
			before, err := os.ReadFile(surface)
			if err != nil {
				t.Fatal(err)
			}
			for _, argv := range [][]string{
				{"--at", "host", "--assert"},
				{"--at", "host"},
			} {
				var out, errw bytes.Buffer
				if rc := applyMain(argv, &out, &errw, false, nil); rc != 1 {
					t.Fatalf("apply %v under %q: rc=%d, want 1\n%s%s",
						argv, mode, rc, out.String(), errw.String())
				}
				if !strings.Contains(errw.String(), "host_management") {
					t.Errorf("apply %v under %q refused without naming the key:\n%s",
						argv, mode, errw.String())
				}
			}
			for _, argv := range [][]string{{"apply", "--assert"}, {"apply"}} {
				var out, errw bytes.Buffer
				if rc := hostMain(argv, &out, &errw, false, nil); rc != 1 {
					t.Fatalf("host %v under %q: rc=%d, want 1\n%s%s",
						argv, mode, rc, out.String(), errw.String())
				}
				if !strings.Contains(errw.String(), "host_management") {
					t.Errorf("host %v under %q refused without naming the key:\n%s",
						argv, mode, errw.String())
				}
			}
			after, err := os.ReadFile(surface)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Errorf("a refused apply wrote to the surface:\n--- before ---\n%s\n--- after ---\n%s",
					before, after)
			}
		})
	}
}

// TestHostManagementNoneStopsShellInitToo is the ABOVE-EVERY-STAGE half, and it is a distinct
// failure from the one above: a refusal placed inside the render would return non-zero, print
// the key, leave the surface untouched — and still append a PATH line to the user's shell rc,
// because `--shell-init` runs after the render in hostApply. That is a command that wrote
// nothing editing a file it was not authorized to touch (P3), and it is exactly how
// `--format json --assert --shell-init` failed before jsonRefusedForPosture moved up.
func TestHostManagementNoneStopsShellInitToo(t *testing.T) {
	home, _ := hostManagementFixture(t, "none")
	rc := filepath.Join(home, ".bashrc")
	writeFile(t, rc, "# untouched\n")
	t.Setenv("SHELL", "/bin/bash")

	var out, errw bytes.Buffer
	if got := hostMain([]string{"apply", "--assert", "--shell-init"}, &out, &errw, false, nil); got != 1 {
		t.Fatalf("rc=%d, want 1\n%s%s", got, out.String(), errw.String())
	}
	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# untouched\n" {
		t.Errorf("a refused `yolo host apply --shell-init` edited the user's shell rc:\n%s", data)
	}
}

// TestHostManagementAssertStillApplies is the other direction, and it is what keeps the
// refusal from being written as "refuse unless the key says none": an unset key and an
// explicit "assert" both APPLY, or step 2 would have broken every existing user in the name
// of a key they never wrote.
func TestHostManagementAssertStillApplies(t *testing.T) {
	for _, mode := range []string{"", "assert"} {
		_, surface := hostManagementFixture(t, mode)
		var out, errw bytes.Buffer
		if rc := applyMain([]string{"--at", "host", "--assert"}, &out, &errw, false, nil); rc != 0 {
			t.Fatalf("host_management %q: rc=%d, want 0\n%s%s", mode, rc, out.String(), errw.String())
		}
		data, err := os.ReadFile(surface)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"telemetry": false`) {
			t.Errorf("host_management %q did not render the pack's managed key:\n%s", mode, data)
		}
	}
}

// TestHostManagementOwnApplies is the row `own` moved to when it stopped refusing: the same
// fixture the refusal test uses, applying rather than being declined, through BOTH spellings.
//
// It is the CLI half of the `own` step — entrypoint's tests measure the bytes; this measures
// that the command reaches them. The fixture's surface declares `rmw`, which the `own` census
// runs unchanged (a pack declaring `rmw` is saying the file holds live agent state, and the
// user's ownership contract does not overrule that) — so the assertion is that yolo's declared
// key lands and the user's undeclared one survives, exactly as under `assert`.
func TestHostManagementOwnApplies(t *testing.T) {
	for _, argv := range [][]string{{"--at", "host", "--assert"}, {"--at", "host"}} {
		hostManagementFixture(t, "own")
		var out, errw bytes.Buffer
		if rc := applyMain(argv, &out, &errw, false, nil); rc != 0 {
			t.Fatalf("apply %v under \"own\": rc=%d, want 0\n%s%s",
				argv, rc, out.String(), errw.String())
		}
		if strings.Contains(errw.String(), "host_management") {
			t.Errorf("apply %v under \"own\" still names the key as a refusal:\n%s",
				argv, errw.String())
		}
	}
	// And the --assert spelling actually wrote: a command that "succeeded" by doing nothing
	// would pass every assertion above.
	_, surface := hostManagementFixture(t, "own")
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("host apply --assert under \"own\": rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	data, err := os.ReadFile(surface)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"telemetry": false`) {
		t.Errorf("`own` did not render the pack's managed key:\n%s", data)
	}
	if !strings.Contains(string(data), `"myOwnKey": "keep"`) {
		t.Errorf("`own` lost the user's undeclared key:\n%s", data)
	}
}

// TestHostApplyGateIsANoOpUnderHostManagementNone is §4.1's consequence: under `none` there
// is no rendered host surface, so `host_apply_on_launch` has nothing to check and the launch
// check becomes a NO-OP rather than a nag.
//
// The home is left deliberately UNAPPLIED with the opt-in ON — the strictest row in §4.3's
// table (no TTY, no approval), which without this branch refuses the launch outright and
// prints a remedy naming a command that itself refuses. So this fails if the branch in
// hostApplyGate is deleted, and it fails loudly: `false` means a `none` user's `claude` stops
// booting.
//
// ⚠ `own` IS NOT IN THIS LOOP, and the second half below is why: it shared the exit only while
// the apply it offers to run refused. Now that `own` renders, a stale owned home is MORE worth
// reporting than an asserted one — the file is derived output, so stale means it disagrees with
// its own definition.
func TestHostApplyGateIsANoOpUnderHostManagementNone(t *testing.T) {
	home := gateFixture(t, true)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"],"host_apply_on_launch":true,"host_management":"none"}`)

	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Errorf("the gate stopped a launch under host_management \"none\" — there is no "+
			"render for it to find stale:\n%s", errw.String())
	}
	if errw.Len() != 0 {
		t.Errorf("the gate nagged under host_management \"none\":\n%s", errw.String())
	}

	// The control, and the `own` assertion in one: the SAME unapplied home refuses under both
	// contracts that DO render. Without the control the `none` assertion above could pass on a
	// home that simply had nothing to report; without `own` in it, the branch could be widened
	// back to "anything but assert" with everything still green.
	for _, mode := range []string{"", "own"} {
		h := gateFixture(t, true)
		if mode != "" {
			writeFile(t, filepath.Join(h, ".config", "yolo-jail", "config.jsonc"),
				`{"packs":["claude"],"host_apply_on_launch":true,"host_management":"`+mode+`"}`)
		}
		var errw bytes.Buffer
		if hostApplyGate(&errw, nil, "claude") {
			t.Errorf("an unapplied home under host_management %q passed the gate — whatever "+
				"renders is what this gate checks:\n%s", mode, errw.String())
		}
	}
}
