package cli

// configlistsurfaces_test.go pins the REPORTING half of `config-list`
// (docs/reference/pack-system.md#config-list-visibility): `config render` folds the packs'
// contributions into its preview, `--explain` tells an array ASSEMBLED from its inputs plus
// named pack contributions apart from one a higher layer REPLACED, `config ls` names the
// contributors per array and the replacement, and its capture count includes per-entry list
// captures; `host apply` names the contributing packs, and leads an ownerless or malformed
// list's line with the kind the author wrote.
//
// Every test runs the real verb (configRunW, applyHost) in a temp HOME with real file://
// packs, so deleting a call site fails it — a test of writeListExplain alone would stay green
// with the render no longer passing Lists.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	listKilo  = "git:github.com/mschulkind/kilo-pi-provider"
	listOther = "npm:other"
)

// listPack writes a local pack under home and returns its `packs` entry.
func listPack(t *testing.T, home, name, contributes string) string {
	t.Helper()
	dir := filepath.Join(home, "packs", name)
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"`+name+`","description":"d","contributes":[`+contributes+`]}`)
	return `{"source":"file://` + dir + `","name":"` + name + `"}`
}

// listWorld is a temp HOME whose config selects `packs` (the literal JSON array body), with
// the cwd in a fresh workspace. It returns the home and the workspace's capture store.
func listWorld(t *testing.T, packs func(home string) string) (home, store string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USE_PROFILES", "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[`+packs(home)+`]}`)
	_, store = withWorkspaceCwd(t)
	return home, store
}

// overlayAB is a config-overlay REPLACING pi's packages with [npm:a, npm:b] — the owner-side
// list the contributions append to.
func overlayAB(t *testing.T, home string) string {
	return listPack(t, home, "matt", `{"kind":"config-overlay","surface":"pi/settings",`+
		`"config":{"managed":{"packages":["npm:a","npm:b"]}}}`)
}

func appendKilo(t *testing.T, home string) string {
	return listPack(t, home, "kilo", `{"kind":"config-list","surface":"pi/settings",`+
		`"path":"/packages","add":["`+listKilo+`"]}`)
}

// appendOther repeats kilo's entry on purpose: equal entries contributed twice produce one.
func appendOther(t *testing.T, home string) string {
	return listPack(t, home, "other", `{"kind":"config-list","surface":"pi/settings",`+
		`"path":"/packages","add":["`+listKilo+`","`+listOther+`"]}`)
}

func runConfigVerb(t *testing.T, args ...string) (rc int, stdout, stderr string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc = configRunW(args, &out, &errw)
	return rc, out.String(), errw.String()
}

// renderedJSON pulls the file body out of `config render` output: everything after the `#`
// header lines.
func renderedJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	i := strings.Index(out, "\n{")
	if i < 0 {
		t.Fatalf("no JSON body in render output:\n%s", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out[i+1:]), &m); err != nil {
		t.Fatalf("render body is not JSON: %v\n%s", err, out)
	}
	return m
}

// The preview FOLDS the contributions: the overlay's list, then each pack's entries in
// `packs` order, a repeat written once. Before the preview's scope grew to fold
// `config-overlay` and `config-list` (pack-system.md#config-list-visibility), `render`
// composed defaults < host < managed only, so it could only ever show the owner's own list.
func TestConfigRenderFoldsOverlaysAndLists(t *testing.T) {
	listWorld(t, func(home string) string {
		return `"pi",` + overlayAB(t, home) + `,` + appendKilo(t, home) + `,` + appendOther(t, home)
	})
	rc, out, errw := runConfigVerb(t, "render", "pi/settings")
	if rc != 0 {
		t.Fatalf("render rc=%d\n%s%s", rc, out, errw)
	}
	got, _ := json.Marshal(renderedJSON(t, out)["packages"])
	want := `["npm:a","npm:b","` + listKilo + `","` + listOther + `"]`
	if string(got) != want {
		t.Errorf("packages = %s, want %s\n%s", got, want, out)
	}
}

// --explain on an ASSEMBLED array: the top-level key is marked (its one-word label would
// claim the overlay's array), and the list block names the lower layers' entries and the
// contributing packs in order, then every entry in final order with its source.
func TestConfigRenderExplainShowsAnAssembledList(t *testing.T) {
	listWorld(t, func(home string) string {
		return `"pi",` + overlayAB(t, home) + `,` + appendKilo(t, home) + `,` + appendOther(t, home)
	})
	rc, out, errw := runConfigVerb(t, "render", "pi/settings", "--explain")
	if rc != 0 {
		t.Fatalf("render --explain rc=%d\n%s%s", rc, out, errw)
	}
	if !hasLine(out, "packages\tconfig-overlay:matt", "(+ config-list entries, below)") {
		t.Errorf("the packages key is not marked as carrying list entries:\n%s", out)
	}
	if !hasLine(out, "list /packages", "assembled", "2 entries", "config-list:kilo, config-list:other") {
		t.Errorf("no assembled-list heading naming the contributors in order:\n%s", out)
	}
	order := []string{`"npm:a"` + "\tbase", `"npm:b"` + "\tbase",
		`"` + listKilo + `"` + "\tconfig-list:kilo", `"` + listOther + `"` + "\tconfig-list:other"}
	last := -1
	for _, line := range order {
		i := strings.Index(out, line)
		if i < 0 {
			t.Fatalf("entry line %q missing:\n%s", line, out)
		}
		if i < last {
			t.Errorf("entry line %q is out of order:\n%s", line, out)
		}
		last = i
	}
	if strings.Count(out, `"`+listKilo+`"`) != 1 {
		t.Errorf("the repeated entry must be listed once:\n%s", out)
	}
}

// The other half of the distinction: an array an OVERLAY replaced, with no list on it,
// prints its layer and nothing else — no list block and no mark.
func TestConfigRenderExplainLeavesAReplacedArrayUnmarked(t *testing.T) {
	listWorld(t, func(home string) string { return `"pi",` + overlayAB(t, home) })
	rc, out, errw := runConfigVerb(t, "render", "pi/settings", "--explain")
	if rc != 0 {
		t.Fatalf("render --explain rc=%d\n%s%s", rc, out, errw)
	}
	if !hasLine(out, "packages\tconfig-overlay:matt") {
		t.Fatalf("no provenance line for packages:\n%s", out)
	}
	if strings.Contains(out, "list /") || strings.Contains(out, "config-list") {
		t.Errorf("an overlay-replaced array reads as assembled:\n%s", out)
	}
}

// A HIGHER LAYER REPLACING the assembled array is said, with the layer: pi's managed
// `defaultProjectTrust` (autonomy posture) replaces whatever a list assembles at that path.
func TestConfigRenderExplainSaysWhenManagedReplacesTheList(t *testing.T) {
	listWorld(t, func(home string) string {
		return `"pi",` + listPack(t, home, "trusty", `{"kind":"config-list","surface":"pi/settings",`+
			`"path":"/defaultProjectTrust","add":["npm:x"]}`)
	})
	rc, out, errw := runConfigVerb(t, "render", "pi/settings", "--explain")
	if rc != 0 {
		t.Fatalf("render --explain rc=%d\n%s%s", rc, out, errw)
	}
	if !hasLine(out, "list /defaultProjectTrust", "replaced by the owner's managed layer",
		"config-list:trusty") {
		t.Errorf("the managed replacement of the assembled list is not said:\n%s", out)
	}
	rc, out, errw = runConfigVerb(t, "render", "pi/settings")
	if rc != 0 {
		t.Fatalf("render rc=%d\n%s%s", rc, out, errw)
	}
	if got := renderedJSON(t, out)["defaultProjectTrust"]; got != "always" {
		t.Errorf("defaultProjectTrust = %v, want managed's \"always\"", got)
	}
}

// A TYPE CONFLICT (pack-system.md#config-list-type-conflict) refuses the preview exactly as
// it refuses the boot: pi's `theme` default is a string, so a list there is an error naming
// the pack.
func TestConfigRenderRefusesAListTypeConflict(t *testing.T) {
	listWorld(t, func(home string) string {
		return `"pi",` + listPack(t, home, "clash", `{"kind":"config-list","surface":"pi/settings",`+
			`"path":"/theme","add":["dark"]}`)
	})
	rc, out, errw := runConfigVerb(t, "render", "pi/settings")
	if rc == 0 {
		t.Fatalf("render composed over a type conflict:\n%s", out)
	}
	if !strings.Contains(errw, "clash") || !strings.Contains(errw, "/theme") {
		t.Errorf("the refusal must name the pack and the path:\n%s", errw)
	}
}

// A malformed list is SAID by the preview (a launch refuses it), not silently dropped.
func TestConfigRenderReportsAMalformedList(t *testing.T) {
	listWorld(t, func(home string) string {
		return `"pi",` + listPack(t, home, "bogus", `{"kind":"config-list","surface":"noslash",`+
			`"path":"/packages","add":["x"]}`)
	})
	_, out, errw := runConfigVerb(t, "render", "pi/settings")
	if !strings.Contains(errw, "not folded") || !strings.Contains(errw, "pack bogus: config-list") {
		t.Errorf("the malformed list is not reported:\n%s%s", out, errw)
	}
}

// `config ls` per array: the contributors in order with their entry counts, whether the
// array is assembled or replaced (the owner's managed layer, on a third-party owner's
// surface), and the footer's precedence and remedy.
func TestConfigLsNamesListContributorsAndManagedReplacement(t *testing.T) {
	listWorld(t, func(home string) string {
		owner := listPack(t, home, "acme", `{"kind":"config","config":[{"agent":"acme",`+
			`"name":"settings","codec":"json","path":"~/.acme/settings.json",`+
			`"managed":{"pinned":["fixed"]}}]}`)
		pinner := listPack(t, home, "pinner", `{"kind":"config-list","surface":"acme/settings",`+
			`"path":"/pinned","add":["mine"]}`)
		return `"pi",` + appendKilo(t, home) + `,` + appendOther(t, home) + `,` + owner + `,` + pinner
	})
	rc, out, errw := runConfigVerb(t, "ls")
	if rc != 0 {
		t.Fatalf("ls rc=%d\n%s%s", rc, out, errw)
	}
	if !hasLine(out, "# pi/settings", "config-list from kilo, other") {
		t.Errorf("no heading naming pi/settings' list contributors:\n%s", out)
	}
	if !hasLine(out, "/packages", "assembled", "kilo (1 entry), other (2 entries)") {
		t.Errorf("no assembled line naming contributors in order:\n%s", out)
	}
	if !hasLine(out, "/pinned", "pinner (1 entry)", "owner's managed layer replaces this array") {
		t.Errorf("the managed replacement is not said:\n%s", out)
	}
	if !strings.Contains(out, "config-list entries append after every config-overlay") {
		t.Errorf("no list precedence footer:\n%s", out)
	}
}

// `config ls` reads the capture store for a list path: a captured deletion replaces the
// array (and reset is named), per-entry captures are counted on the line, and the table's
// capture column counts them as list entries rather than keys.
func TestConfigLsReportsCapturedListState(t *testing.T) {
	_, store := listWorld(t, func(home string) string { return `"pi",` + appendKilo(t, home) })
	writeSidecar(t, store, "pi", "settings", `{"packages":null}`, `{}`)
	if err := os.WriteFile(filepath.Join(store, "pi-settings.list-capture.json"),
		[]byte(`{"/packages":{"add":["npm:mine","npm:two"],"remove":["npm:gone"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errw := runConfigVerb(t, "ls", "--all")
	if rc != 0 {
		t.Fatalf("ls rc=%d\n%s%s", rc, out, errw)
	}
	if !hasLine(out, "/packages", "kilo (1 entry)", "a captured in-jail edit replaces this array",
		"yolo config reset pi/settings") {
		t.Errorf("the captured replacement is not said:\n%s", out)
	}
	if !hasLine(out, "2 added and 1 removed in-jail") {
		t.Errorf("the per-entry captures at the path are not counted:\n%s", out)
	}
	if !hasLine(out, "pi/settings", "1 key, 3 list entries ⚠") {
		t.Errorf("the capture column does not count list entries:\n%s", out)
	}
}

// A surface holding ONLY per-entry list captures is diverged too: the column must not read
// "–", and the footer counts it.
func TestConfigLsCountsAListOnlyCapture(t *testing.T) {
	_, store := listWorld(t, func(string) string { return `"pi"` })
	if err := os.WriteFile(filepath.Join(store, "pi-settings.list-capture.json"),
		[]byte(`{"/packages":{"add":["npm:mine"],"remove":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errw := runConfigVerb(t, "ls", "--all")
	if rc != 0 {
		t.Fatalf("ls rc=%d\n%s%s", rc, out, errw)
	}
	if !hasLine(out, "pi/settings", "1 list entry ⚠") {
		t.Errorf("a list-only capture is not flagged:\n%s", out)
	}
	if !strings.Contains(out, "1 surface has captured in-jail edits") {
		t.Errorf("the divergence footer does not count it:\n%s", out)
	}
}

// `host apply` names the packs appending entries to a surface, and leads an ownerless list's
// line — and a malformed one's refusal — with `config-list`, the kind the author wrote.
func TestHostApplyNamesListContributorsOrphansAndProblems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	owner := listPack(t, home, "acme", `{"kind":"config","config":[{"agent":"acme",`+
		`"name":"settings","codec":"json","mode":"rmw","path":"~/.acme/settings.json",`+
		`"managed":{"k":"v"}}]}`)
	lister := listPack(t, home, "lister", `{"kind":"config-list","surface":"acme/settings",`+
		`"path":"/extras","add":["one"]}`)
	orphan := listPack(t, home, "stray", `{"kind":"config-list","surface":"nobody/settings",`+
		`"path":"/extras","add":["one"]}`)
	selectPacks(t, home, owner+","+lister+","+orphan)
	verboseReport(t)

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("host apply rc=%d\n%s", rc, report)
	}
	if !hasLine(report, "config-list entries from: lister") {
		t.Errorf("no line naming the list contributor:\n%s", report)
	}
	if !hasLine(report, "config-list  no effect", "nobody/settings", "(pack stray)") {
		t.Errorf("the ownerless list is not reported under its own kind:\n%s", report)
	}
	if hasLine(report, "config-overlay", "(pack stray)") {
		t.Errorf("the ownerless list is reported as a config-overlay:\n%s", report)
	}

	bogus := listPack(t, home, "bogus", `{"kind":"config-list","surface":"noslash",`+
		`"path":"/extras","add":["one"]}`)
	selectPacks(t, home, owner+","+bogus)
	rc, report = applyWith(t, false, nil)
	if rc == 0 {
		t.Errorf("a malformed list must fail the apply:\n%s", report)
	}
	if !hasLine(report, "config-list refused", "pack bogus: config-list") {
		t.Errorf("the malformed list is not refused under its own kind:\n%s", report)
	}
}

// `pack lint` and `pack footprint` both list a config-list's claim — the array it appends to
// and what it appends — through the one shared claim printer.
func TestPackLintAndFootprintListAConfigList(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"kilo","description":"d","contributes":[`+
		`{"kind":"config-list","surface":"pi/settings","path":"/packages","add":["`+listKilo+`"]}]}`)

	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("lint rc=%d:\n%s", rc, report)
	}
	if !hasLine(report, "config-list", "pi/settings#/packages", "appends 1 entry", listKilo) {
		t.Errorf("lint does not list the config-list delivery:\n%s", report)
	}

	var out, errw bytes.Buffer
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("footprint rc=%d:\n%s%s", rc, out.String(), errw.String())
	}
	if !hasLine(out.String(), "config-list", "pi/settings#/packages", "appends 1 entry") {
		t.Errorf("footprint does not report the claim:\n%s%s", out.String(), errw.String())
	}
}
