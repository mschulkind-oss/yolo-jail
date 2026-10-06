package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostManagementFixture builds a scratch home carrying one pack that owns one rmw surface,
// plus a hand-written user key in that surface's real file — the shape §11's non-regression
// criterion is stated over ("an undeclared key is left byte-identical").
//
// It returns the home and the surface path. mode is written into the user config verbatim; ""
// leaves the key unset, which is the state every existing user is in and which means "none"
// since the `assert` retirement (OQ-CO14).
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

// TestHostManagementUnsetIsNoneAndSaysSoOnlyAtTheAct is OQ-CO14 face 2 at the CLI: since the
// `assert` retirement the unset key is `none`, with NO prompt and NO notice at upgrade — so a
// writing apply over a home with the key unset refuses exactly as an explicit "none" does,
// writes nothing, and says so only at the act, naming which of the two it read. It replaces
// TestHostManagementAssertIsByteIdenticalToUnset, whose subject — that declaring the value the
// unset key resolves to changes nothing — is the same claim about the new default: the two
// spellings write the same nothing, and only the first line of the refusal tells them apart.
func TestHostManagementUnsetIsNoneAndSaysSoOnlyAtTheAct(t *testing.T) {
	refusal := func(t *testing.T, mode string) string {
		t.Helper()
		home, surface := hostManagementFixture(t, mode)
		var out, errw bytes.Buffer
		if rc := applyMain([]string{"--at", "host", "--assert"}, &out, &errw, false, nil); rc != 1 {
			t.Fatalf("apply --at host --assert (host_management %q) rc=%d, want 1: %s%s",
				mode, rc, out.String(), errw.String())
		}
		if data, _ := os.ReadFile(surface); string(data) != `{"myOwnKey":"keep","telemetry":true}` {
			t.Errorf("host_management %q wrote the surface:\n%s", mode, data)
		}
		if _, err := os.Stat(filepath.Join(home, ".local", "share", "yolo-jail",
			"host-provenance")); err == nil {
			t.Errorf("host_management %q left a provenance record — yolo wrote here", mode)
		}
		return errw.String()
	}
	unset := refusal(t, "")
	none := refusal(t, "none")
	if !strings.Contains(unset, `is unset in `) || !strings.Contains(unset, `which means "none"`) {
		t.Errorf("the unset refusal does not say the key is unset and reads as none:\n%s", unset)
	}
	if !strings.Contains(none, `is "none" in `) {
		t.Errorf("the explicit refusal does not say the key is none:\n%s", none)
	}
	// Everything after the first sentence is the same refusal.
	_, uTail, _ := strings.Cut(unset, ": your agents'")
	_, nTail, _ := strings.Cut(none, ": your agents'")
	if uTail == "" || uTail != nTail {
		t.Errorf("unset and none refuse differently past the state they read:\n%s\n---\n%s", unset, none)
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
//
// The UNSET key joined the loop with the `assert` retirement (OQ-CO14): it means "none" now.
// And the remedy the refusal prints is pinned too, since it is the one OQ-CO14 rewrote: it
// names "own", `yolo config promote` for a key kept by hand, and `--revert` for what an earlier
// yolo wrote — and never the retired "assert" it used to name.
func TestHostManagementNoneRefusesBothSpellings(t *testing.T) {
	for _, mode := range []string{"none", ""} {
		t.Run("host_management="+mode, func(t *testing.T) {
			_, surface := hostManagementFixture(t, mode)
			before, err := os.ReadFile(surface)
			if err != nil {
				t.Fatal(err)
			}
			check := func(what, msg string) {
				t.Helper()
				for _, want := range []string{"host_management", `Set it to "own"`,
					"yolo config promote", "yolo host apply --revert"} {
					if !strings.Contains(msg, want) {
						t.Errorf("%s under %q refused without saying %q:\n%s", what, mode, want, msg)
					}
				}
				if strings.Contains(msg, "assert\"") || strings.Contains(msg, `"assert`) {
					t.Errorf("%s under %q still names the retired \"assert\":\n%s", what, mode, msg)
				}
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
				check(fmt.Sprintf("apply %v", argv), errw.String())
			}
			for _, argv := range [][]string{{"apply", "--assert"}, {"apply"}} {
				var out, errw bytes.Buffer
				if rc := hostMain(argv, &out, &errw, false, nil); rc != 1 {
					t.Fatalf("host %v under %q: rc=%d, want 1\n%s%s",
						argv, mode, rc, out.String(), errw.String())
				}
				check(fmt.Sprintf("host %v", argv), errw.String())
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

// TestHostManagementNoneStopsShellInitToo was the ABOVE-EVERY-STAGE half: a refusal placed
// inside the render would return non-zero, print the key, leave the surface untouched — and
// still append a PATH line to the user's shell rc, because `--shell-init` ran after the render
// in hostApply. That is a command that wrote nothing editing a file it was not authorized to
// touch (P3), and it is exactly how `--format json --assert --shell-init` failed before
// jsonRefusedForPosture moved up.
//
// `--shell-init` is removed now (HE-D1) and refuses in the parse, before this gate is reached,
// so the exit code is the removal's 2 rather than the gate's 1. What the test protected is
// unchanged and still asserted: under `none`, that argv edits no shell rc.
func TestHostManagementNoneStopsShellInitToo(t *testing.T) {
	home, _ := hostManagementFixture(t, "none")
	rc := filepath.Join(home, ".bashrc")
	writeFile(t, rc, "# untouched\n")
	t.Setenv("SHELL", "/bin/bash")

	var out, errw bytes.Buffer
	if got := hostMain([]string{"apply", "--assert", "--shell-init"}, &out, &errw, false, nil); got != 2 {
		t.Fatalf("rc=%d, want 2 (the removed flag refuses)\n%s%s", got, out.String(), errw.String())
	}
	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# untouched\n" {
		t.Errorf("a refused `yolo host apply --shell-init` edited the user's shell rc:\n%s", data)
	}
}

// TestHostManagementRetiredAssertIsRefusedByName is OQ-CO14 face 1 at every host verb that would
// have written under it: a config still saying "assert" is refused with the retirement message —
// naming "none" and "own", `yolo config promote` and `--revert` — not with `none`'s sentences,
// which would describe a file the user did not write. Both spellings of the apply, both postures,
// `--revert`, and a wrapped `yolo host -- <bin>` launch, which must stop before the program runs.
// Nothing is written. It replaces TestHostManagementAssertStillApplies, the other direction of
// the old rule (an explicit "assert" applied).
func TestHostManagementRetiredAssertIsRefusedByName(t *testing.T) {
	_, surface := hostManagementFixture(t, "assert")
	before, err := os.ReadFile(surface)
	if err != nil {
		t.Fatal(err)
	}
	type run struct {
		name string
		argv []string
		main func([]string, *bytes.Buffer, *bytes.Buffer) int
		pfx  string
	}
	viaApply := func(argv []string, out, errw *bytes.Buffer) int { return applyMain(argv, out, errw, false, nil) }
	viaHost := func(argv []string, out, errw *bytes.Buffer) int { return hostMain(argv, out, errw, false, nil) }
	for _, r := range []run{
		{"apply --at host --assert", []string{"--at", "host", "--assert"}, viaApply, "yolo host apply: "},
		{"apply --at host", []string{"--at", "host"}, viaApply, "yolo host apply: "},
		{"host apply --assert", []string{"apply", "--assert"}, viaHost, "yolo host apply: "},
		{"host apply --revert", []string{"apply", "--revert"}, viaHost, "yolo host apply --revert: "},
		{"apply --at host --revert --assert", []string{"--at", "host", "--revert", "--assert"}, viaApply,
			"yolo host apply --revert: "},
		// A binary that is not on PATH, never `true`: past a missing refusal the launch would
		// EXEC its program, replacing the test process, so a real binary here would read as a
		// pass with the refusal deleted. A missing one ends the launch with 127 instead.
		{"host -- <bin>", []string{"--", "no-such-agent-binary"}, viaHost, "yolo host: "},
	} {
		t.Run(r.name, func(t *testing.T) {
			var out, errw bytes.Buffer
			if rc := r.main(r.argv, &out, &errw); rc != 1 {
				t.Fatalf("rc=%d, want 1\n%s%s", rc, out.String(), errw.String())
			}
			msg := errw.String()
			for _, want := range []string{r.pfx + `host_management: "assert" is RETIRED`, `"none"`, `"own"`,
				"yolo config promote", "yolo host apply --revert"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, `is "none" in`) || strings.Contains(msg, "is unset in") {
				t.Errorf("the retired value got none's sentences:\n%s", msg)
			}
		})
	}
	if after, _ := os.ReadFile(surface); string(after) != string(before) {
		t.Errorf("a refused verb wrote the surface:\n%s", after)
	}
}

// TestOwnTakesOverAHomeAssertWroteAndArchivesItOnce is OQ-CO14 face 2's other half, through the
// command: a home yolo asserted into, with the key unset, is left exactly as `assert` last
// rendered it — the unset default is `none`, and the apply refuses without a byte moved — and
// choosing `own` later takes it over the way `yolo host apply` always does: one adoption archive
// of the file as `assert` left it, made at the first owned apply and never again.
//
// The asserted home is built the way the retired `assert` wrote it: the fixture surface first
// declares `rmw` (the arm `assert` coerced every surface through, which an owned host still runs
// for a surface declaring it) and is applied; then the declaration goes back to `stateful`, the
// shape `own` composes and adopts. The archive appearing is the production adoption path
// (entrypoint.archiveAdoption, from persistStatefulSurface), reached from the command.
func TestOwnTakesOverAHomeAssertWroteAndArchivesItOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	packDir := t.TempDir()
	manifest := func(mode string) string {
		return `{"name":"hm","contributes":[{"kind":"config","config":[{"agent":"hm","name":"settings",` +
			`"codec":"json","path":"~/.hm/settings.json"` + mode + `,"managed":{"telemetry":false}}]}]}`
	}
	cfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	config := func(mode string) {
		cfg := `{"packs":["file://` + packDir + `"],"confinement":"host","host_wrappers":false`
		if mode != "" {
			cfg += `,"host_management":"` + mode + `"`
		}
		writeFile(t, cfgPath, cfg+`}`)
	}
	apply := func(wantRC int) string {
		t.Helper()
		var out, errw bytes.Buffer
		if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, nil); rc != wantRC {
			t.Fatalf("host apply --assert rc=%d, want %d\n%s%s", rc, wantRC, out.String(), errw.String())
		}
		return out.String() + errw.String()
	}
	surface := filepath.Join(home, ".hm", "settings.json")
	writeFile(t, surface, `{"myOwnKey":"keep","telemetry":true}`)

	// What `assert` left: an rmw render, recorded.
	writeFile(t, filepath.Join(packDir, "pack.json"), manifest(`,"mode":"rmw"`))
	config("own")
	apply(0)
	asserted, err := os.ReadFile(surface)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(asserted), `"myOwnKey": "keep"`) || !strings.Contains(string(asserted), `"telemetry": false`) {
		t.Fatalf("the asserted home is not what an rmw apply leaves:\n%s", asserted)
	}
	writeFile(t, filepath.Join(packDir, "pack.json"), manifest(""))

	// Upgrade day: the key unset, so none. The apply refuses and the file does not move.
	config("")
	if msg := apply(1); !strings.Contains(msg, "is unset in") {
		t.Errorf("the unset apply did not refuse as none:\n%s", msg)
	}
	if now, _ := os.ReadFile(surface); string(now) != string(asserted) {
		t.Fatalf("the file moved under none:\n%s\nwant as assert left it:\n%s", now, asserted)
	}

	// Choosing own: the first owned apply archives the file as `assert` left it.
	config("own")
	apply(0)
	archive := filepath.Join(home, ".local", "share", "yolo-jail", "archive", "config",
		"hm-settings", "settings.json")
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("own's takeover made no adoption archive at %s: %v", archive, err)
	}
	if string(got) != string(asserted) {
		t.Errorf("the archive holds:\n%s\nwant the file as assert left it:\n%s", got, asserted)
	}
	// Once: a later apply over a changed file does not re-archive.
	writeFile(t, surface, `{"myOwnKey":"changed","telemetry":false}`)
	apply(0)
	if again, _ := os.ReadFile(archive); string(again) != string(asserted) {
		t.Errorf("a later owned apply rewrote the archive:\n%s", again)
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
	// "none" written, and UNSET — which is "none" since the `assert` retirement (OQ-CO14), so
	// a home whose key nobody wrote gets the same silent no-op at upgrade: no prompt, no notice.
	for _, cfg := range []string{
		`{"packs":["claude"],"host_apply_on_launch":true,"host_management":"none"}`,
		`{"packs":["claude"],"host_apply_on_launch":true}`,
	} {
		home := gateFixture(t, true)
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg)

		var errw bytes.Buffer
		if !hostApplyGate(&errw, nil, "claude") {
			t.Errorf("the gate stopped a launch under %s — there is no render for it to find "+
				"stale:\n%s", cfg, errw.String())
		}
		if errw.Len() != 0 {
			t.Errorf("the gate nagged under %s:\n%s", cfg, errw.String())
		}
	}

	// The control, and the `own` assertion in one: the SAME unapplied home auto-applies under
	// the one contract that DOES render. (The unset key was the other half of this control
	// until the retirement moved it to the no-op above.)
	h := gateFixture(t, true)
	writeFile(t, filepath.Join(h, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"],"host_apply_on_launch":true,"host_management":"own"}`)
	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Errorf("an unapplied home under host_management \"own\" failed the gate:\n%s", errw.String())
	}
	if !strings.Contains(errw.String(), "synchronized host configuration") {
		t.Errorf("expected synchronized notice under own, got:\n%s", errw.String())
	}
}
