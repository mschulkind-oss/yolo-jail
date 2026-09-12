package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localPackManifest reads the conventional local pack's manifest back as a decoded map.
func localPackManifest(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"))
	if err != nil {
		t.Fatalf("no local pack manifest: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("the manifest promote wrote is not valid JSON (%v):\n%s", err, data)
	}
	return m
}

// overlayKeysOf decodes a capture sidecar's top-level keys.
func overlayKeysOf(t *testing.T, agent, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(prismOverlayPath(agent, name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("the overlay promote rewrote is not valid JSON (%v):\n%s", err, data)
	}
	return m
}

// THE WHOLE POINT OF THE VERB, end to end: a key captured in a jail becomes a DECLARED key
// in the conventional local pack — which needs no `packs` entry and renders at every notch
// — and stops being captured, so the value is declared in exactly one place (§5.2 steps
// 5-6).
//
// The three things this pins beyond "it wrote something" are the ones the design argues
// about:
//
//   - the contribution is a config-overlay naming the SURFACE identity, so the next launch
//     folds it;
//   - the promoted key leaves the capture overlay and every OTHER captured key stays;
//   - `last_render` and the surface file are UNTOUCHED. That is the per-key reset's whole
//     contract: last_render is the branch selector, so truncating it turns the next boot
//     into a first migration, which ADOPTS the on-disk file and re-captures the very key
//     just promoted.
func TestPromoteDeclaresTheKeyLocallyAndClearsOnlyThatKey(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings",
		`{"autoMemoryEnabled":true,"preferences":{"autoUpdaterStatus":"disabled"}}`,
		`{"model":"theirs"}`)
	surfacePath := filepath.Join(w.home, ".claude", "settings.json")
	writeFile(t, surfacePath, `{"model":"theirs","autoMemoryEnabled":true}`)
	lastRenderBefore, err := os.ReadFile(prismLastRenderPath("claude", "settings"))
	if err != nil {
		t.Fatal(err)
	}
	surfaceBefore, err := os.ReadFile(surfacePath)
	if err != nil {
		t.Fatal(err)
	}

	out, errw, rc := w.run("claude", "--keys", "autoMemoryEnabled", "--accept-promotion")
	if rc != 0 {
		t.Fatalf("rc=%d: %s%s", rc, out, errw)
	}

	decl := localPackManifest(t, w.home)
	if decl["name"] != "local" {
		t.Errorf("the created manifest is not named for the pack: %v", decl["name"])
	}
	contributes, _ := decl["contributes"].([]any)
	if len(contributes) != 1 {
		t.Fatalf("contributes = %v, want one config-overlay", contributes)
	}
	c, _ := contributes[0].(map[string]any)
	if c["kind"] != "config-overlay" || c["surface"] != "claude/settings" {
		t.Errorf("contribution = %v, want a config-overlay on claude/settings", c)
	}
	cfg, _ := c["config"].(map[string]any)
	managed, _ := cfg["managed"].(map[string]any)
	if v, ok := managed["autoMemoryEnabled"]; !ok || v != true {
		t.Errorf("the promoted VALUE did not land in the overlay body: %v", cfg)
	}

	overlay := overlayKeysOf(t, "claude", "settings")
	if _, still := overlay["autoMemoryEnabled"]; still {
		t.Errorf("the promoted key is STILL captured — declared and captured at once is the "+
			"double declaration promotion exists to end: %v", overlay)
	}
	if _, kept := overlay["preferences"]; !kept {
		t.Errorf("an unpromoted captured key was reset too: %v", overlay)
	}

	lastRenderAfter, err := os.ReadFile(prismLastRenderPath("claude", "settings"))
	if err != nil {
		t.Fatalf("promote removed the last_render sidecar: %v", err)
	}
	if string(lastRenderAfter) != string(lastRenderBefore) {
		t.Errorf("promote moved last_render — the next boot would take the first-migration "+
			"path and re-adopt the promoted key:\ngot  %s\nwant %s", lastRenderAfter, lastRenderBefore)
	}
	surfaceAfter, err := os.ReadFile(surfacePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(surfaceAfter) != string(surfaceBefore) {
		t.Errorf("promote rewrote the surface file, which the agent is reading right now:\n"+
			"got  %s\nwant %s", surfaceAfter, surfaceBefore)
	}
}

// The consent gate: without --accept-promotion the run is a dry run that WRITES NOTHING and
// names the flag. It is not --yes; it names what is approved ([OQ-CO4], following
// config.AcceptConfigChangesFlag).
func TestPromoteWithoutAcceptWritesNothing(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)
	before := w.overlayFor("claude", "settings")

	out, _, rc := w.run("claude")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(out, "--accept-promotion") {
		t.Errorf("the dry run does not name the flag that writes:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".config", "yolo-jail", "local")); !os.IsNotExist(err) {
		t.Errorf("a run without consent created the local pack (stat err = %v)", err)
	}
	if after := w.overlayFor("claude", "settings"); after != before {
		t.Errorf("a run without consent rewrote the capture overlay:\n%s", after)
	}
}

// §5.6: a destination that already declares the key DEEP-MERGES, the promoted value wins,
// and the pack's own sibling keys survive.
//
// Deep rather than replace, because a capture is a merge PATCH: it holds only the leaves
// that changed, so replacing the subtree would drop whatever the pack declared beside them.
func TestPromoteDeepMergesIntoAnExistingContribution(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	writeFile(t, filepath.Join(w.home, ".config", "yolo-jail", "local", "pack.json"),
		`{"name":"local","contributes":[
		  {"kind":"skills"},
		  {"kind":"config-overlay","surface":"claude/settings",
		   "config":{"managed":{"preferences":{"theme":"dark","autoUpdaterStatus":"enabled"}}}}]}`)
	w.capture("claude", "settings", `{"preferences":{"autoUpdaterStatus":"disabled"}}`, `{}`)

	if _, errw, rc := w.run("claude", "--accept-promotion"); rc != 0 {
		t.Fatalf("rc=%d: %s", rc, errw)
	}

	decl := localPackManifest(t, w.home)
	contributes, _ := decl["contributes"].([]any)
	if len(contributes) != 2 {
		t.Fatalf("promote added a second overlay instead of merging: %v", contributes)
	}
	c, _ := contributes[1].(map[string]any)
	cfg, _ := c["config"].(map[string]any)
	managed, _ := cfg["managed"].(map[string]any)
	prefs, _ := managed["preferences"].(map[string]any)
	if prefs["autoUpdaterStatus"] != "disabled" {
		t.Errorf("the promoted value did not win: %v", prefs)
	}
	if prefs["theme"] != "dark" {
		t.Errorf("the pack's own sibling key was dropped by the merge: %v", prefs)
	}
	if kind, _ := contributes[0].(map[string]any); kind["kind"] != "skills" {
		t.Errorf("promote disturbed another contribution: %v", contributes[0])
	}
}

// §5.1: a FETCHED pack is refused — it is not the user's file to edit, and the next `yolo
// pack install` would overwrite the change from the locked commit. A pack yolo SHIPS is
// refused separately, because its manifest is inside the binary and the reason differs.
func TestPromoteRefusesPackDestinationsItMustNotWrite(t *testing.T) {
	for _, c := range []struct {
		name, entry, dest, want string
	}{
		{"fetched", `{"source":"git+https://example.invalid/p.git","name":"far"}`, "pack:far", "FETCHED"},
		{"embedded", `"claude"`, "pack:claude", "pack yolo SHIPS"},
		{"unselected", `"claude"`, "pack:nosuch", "no configured pack named"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newPromoteWorld(t, `["claude",`+c.entry+`]`)
			w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)
			_, errw, rc := w.run("claude", "--to", c.dest, "--accept-promotion")
			if rc == 0 {
				t.Fatalf("promote --to %s: rc=0, want a refusal", c.dest)
			}
			if !strings.Contains(errw, c.want) {
				t.Errorf("refusal %q does not say %q", errw, c.want)
			}
			if after := w.overlayFor("claude", "settings"); !strings.Contains(after, "autoMemoryEnabled") {
				t.Errorf("a refused promotion still cleared the capture:\n%s", after)
			}
		})
	}
}

// A `pack.jsonc` destination is refused, and the reason is the FILE rather than the pack:
// promote writes JSON, packload reads the .jsonc first, so rewriting it would strip the
// user's comments and writing a pack.json beside it would be ignored — a promotion that
// reports success and delivers nothing.
func TestPromoteRefusesAJsoncManifest(t *testing.T) {
	w := newPromoteWorld(t, `[]`)
	dir := filepath.Join(w.home, ".config", "yolo-jail", "local")
	writeFile(t, filepath.Join(dir, "pack.jsonc"), "// mine\n{\"name\":\"local\"}\n")
	w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)

	_, errw, rc := w.run("claude", "--accept-promotion")
	if rc == 0 {
		t.Fatal("rc=0, want a refusal")
	}
	if !strings.Contains(errw, "pack.jsonc") {
		t.Errorf("the refusal does not name the file it found:\n%s", errw)
	}
	if _, err := os.Stat(filepath.Join(dir, "pack.json")); !os.IsNotExist(err) {
		t.Errorf("promote wrote a pack.json beside the pack.jsonc that yolo would ignore")
	}
}

// §5.2: steps 5-7 are ONE logical write — "if any part fails the whole promotion is
// abandoned with nothing changed: a half-promoted key is declared AND captured".
//
// The reset is what fails here, because that is the order the writes happen in and the only
// failure that can leave the two files disagreeing. The manifest must come back exactly as
// it was — which for a first promotion means not existing at all.
func TestPromoteRollsBackTheManifestWhenTheResetFails(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)
	before := w.overlayFor("claude", "settings")

	real := promoteWriteFile
	promoteWriteFile = func(path string, data []byte) error {
		if strings.HasSuffix(path, ".overlay.json") {
			return errors.New("injected: the sidecar write failed")
		}
		return real(path, data)
	}
	t.Cleanup(func() { promoteWriteFile = real })

	_, errw, rc := w.run("claude", "--accept-promotion")
	if rc == 0 {
		t.Fatal("rc=0 on a failed write")
	}
	if !strings.Contains(errw, "abandoned") {
		t.Errorf("the failure does not say the promotion was abandoned:\n%s", errw)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".config", "yolo-jail", "local", "pack.json")); !os.IsNotExist(err) {
		t.Errorf("the manifest survived a failed promotion (stat err = %v) — the key would "+
			"be declared AND captured", err)
	}
	if after := w.overlayFor("claude", "settings"); after != before {
		t.Errorf("the capture overlay changed on a failed promotion:\n%s", after)
	}
}

// The rollback restores an EXISTING manifest byte-for-byte, not to some re-encoding of it:
// a rollback that reformatted the user's file would itself be a change they did not ask for.
func TestPromoteRollbackRestoresAnExistingManifestVerbatim(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	manifest := filepath.Join(w.home, ".config", "yolo-jail", "local", "pack.json")
	const body = "{\"name\":\"local\",   \"contributes\":[]}\n" // deliberately odd spacing
	writeFile(t, manifest, body)
	w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)

	real := promoteWriteFile
	promoteWriteFile = func(path string, data []byte) error {
		if strings.HasSuffix(path, ".overlay.json") {
			return errors.New("injected")
		}
		return real(path, data)
	}
	t.Cleanup(func() { promoteWriteFile = real })

	if _, _, rc := w.run("claude", "--accept-promotion"); rc == 0 {
		t.Fatal("rc=0 on a failed write")
	}
	got, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("the rollback rewrote the user's manifest:\ngot  %q\nwant %q", got, body)
	}
}

// `--to host` writes nothing in this step, and says so rather than falling back to `local`:
// they are different files with different reach, and §5.1's warning is that promote must
// not offer `host` as though it were the same kind of thing. The PLAN still runs, which is
// how a user learns which of their surfaces even has a host layer.
func TestPromoteToHostRefusesTheWriteAndKeepsThePlan(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)

	out, _, rc := w.run("claude", "--plan", "--to", "host")
	if rc != 0 {
		t.Fatalf("the plan must still run: rc=%d", rc)
	}
	if !strings.Contains(out, "autoMemoryEnabled") {
		t.Errorf("the --to host plan classified nothing:\n%s", out)
	}

	_, errw, rc := w.run("claude", "--to", "host", "--accept-promotion")
	if rc == 0 {
		t.Fatal("rc=0, want a refusal")
	}
	if !strings.Contains(errw, "not built") {
		t.Errorf("the refusal does not say the write is missing:\n%s", errw)
	}
	if after := w.overlayFor("claude", "settings"); !strings.Contains(after, "autoMemoryEnabled") {
		t.Errorf("the refused host promotion cleared the capture anyway:\n%s", after)
	}
}

// The ownership contract decides `--to host` before any surface does, and the two refusals
// are not interchangeable: at `none` yolo writes nothing into the real home, at `own` the
// file is derived output and a key written into it is composed over.
func TestPromoteToHostRefusesUnderTheOwnershipContract(t *testing.T) {
	for mode, want := range map[string]string{"none": "yours entirely", "own": "DERIVED output"} {
		t.Run(mode, func(t *testing.T) {
			w := newPromoteWorld(t, `["claude"]`)
			writeFile(t, filepath.Join(w.home, ".config", "yolo-jail", "config.jsonc"),
				`{"packs":["claude"],"host_management":"`+mode+`"}`)
			w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)

			_, errw, rc := w.run("claude", "--plan", "--to", "host")
			if rc == 0 {
				t.Fatalf("host_management %q: rc=0, want a refusal", mode)
			}
			if !strings.Contains(errw, want) {
				t.Errorf("host_management %q refusal %q does not say %q", mode, errw, want)
			}
		})
	}
}

// A promotion of a key that is not promotable writes nothing at all — the refusals in the
// plan are refusals, not warnings (§5.4: "refused BY NAME with the losing layer, never
// promoted with a warning").
func TestPromoteWritesNothingWhenEveryKeyIsHeld(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings", `{"model":"same"}`, `{"model":"same"}`) // redundant

	out, _, rc := w.run("claude", "--accept-promotion")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(out, "Nothing to promote") {
		t.Errorf("a run with nothing promotable did not say so:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".config", "yolo-jail", "local", "pack.json")); !os.IsNotExist(err) {
		t.Errorf("promote created a manifest with no key to declare (stat err = %v)", err)
	}
}

// THE MANIFEST PROMOTE WRITES IS READ BACK BY THE REAL READER, not by this test's idea of
// the shape: after a promotion, `yolo config diff` must report the key as a config-overlay
// contributed by `local`.
//
// It is the check a decode-based assertion cannot make. A manifest with the wrong kind
// name, a mistyped surface identity, or a body the overlay decoder refuses would still
// satisfy "the JSON has the fields I expected" while contributing nothing — the silent
// non-delivery this verb exists to end, reproduced by the verb itself.
func TestPromotedKeyIsReadBackAsAPackContribution(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)

	if _, errw, rc := w.run("claude", "--accept-promotion"); rc != 0 {
		t.Fatalf("rc=%d: %s", rc, errw)
	}

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"diff", "claude"}, &out, &errw); rc != 0 {
		t.Fatalf("config diff rc=%d: %s%s", rc, out.String(), errw.String())
	}
	report := out.String()
	if !strings.Contains(report, "config-overlay from local") {
		t.Errorf("the promoted key is not read back as a local-pack contribution:\n%s", report)
	}
	if !strings.Contains(report, "autoMemoryEnabled") {
		t.Errorf("the promoted key is missing from the contribution report:\n%s", report)
	}
}
