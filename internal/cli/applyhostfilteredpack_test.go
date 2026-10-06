package cli

// applyhostfilteredpack_test.go pins the host notch reading a configured pack's DECLARATION
// from the tree its entry's `only`/`exclude` filters leave: the tree the launch stages and loads
// (run's stagePacks), `yolo check` loads (check/packs.go) and config validation loads
// (config/selectedpacks.go).
//
// The bug these pin: resolveConfiguredPack ran its manifest-problem check (NS-D14) over the
// pack's UNFILTERED source tree, so `yolo host apply --assert` refused, as "every launch refuses
// it too", a pack every launch accepts — one whose only problem is a file its entry excludes, or
// a manifest its entry filters out. footerHostPacks made the same unfiltered read. And a
// manifest the filters drop was still READ at the host notch: its declarations were applied to
// the real home although the launch never sees them.
//
// Every test runs against a t.TempDir() home, and each has a control that fails the same way
// without the filter, so a pass cannot come from a fixture that has no problem to filter.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// filteredPackHome writes the local pack "flt" — manifest (or none, when "") plus one skill
// and any extra files (pack-relative path → body) — and a user config selecting `claude` beside
// it, with filter (a raw JSON fragment such as `,"exclude":["x"]`, or "") on its entry and extra
// top-level config keys (leading comma included, or ""). It returns the home.
func filteredPackHome(t *testing.T, manifest string, files map[string]string, filter, extra string) string {
	t.Helper()
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "flt")
	if manifest != "" {
		writeFile(t, filepath.Join(packDir, "pack.json"), manifest)
	}
	writeFile(t, filepath.Join(packDir, "skills", "fltskill", "SKILL.md"),
		"---\nname: fltskill\ndescription: d\n---\nFiltered body.\n")
	for rel, body := range files {
		writeFile(t, filepath.Join(packDir, filepath.FromSlash(rel)), body)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Chdir(t.TempDir())
	selectPacksWith(t, home, `"claude",{"source":"file://`+packDir+`","name":"flt"`+filter+`}`, extra)
	return home
}

// cleanFltManifest is a manifest with no problem of its own: its one skill, into claude's dir.
const cleanFltManifest = `{"name":"flt","description":"d","contributes":[
  {"kind":"skills","from":"skills","into":".claude/skills"}]}`

// reservedBriefing is a file whose presence alone is a load problem (packload's
// reservedBriefingFiles, OQ-PB2), wherever the manifest is fine.
var reservedBriefing = map[string]string{"briefing/CLAUDE.md": "Dual-reader prose.\n"}

// AN EXCLUDED FILE IS NOT A PROBLEM THE HOST MAY REFUSE OVER. The pack's one problem is a file its
// entry excludes, so the launch stages and loads it clean; host apply applies it too.
func TestApplyHostAppliesAPackWhoseOnlyProblemIsAnExcludedFile(t *testing.T) {
	for _, filtered := range []bool{true, false} {
		filter := ""
		if filtered {
			filter = `,"exclude":["briefing/CLAUDE.md"]`
		}
		home := filteredPackHome(t, cleanFltManifest, reservedBriefing, filter, "")
		rc, report := applyWith(t, true, strings.NewReader("y\n"))
		if !filtered {
			// The control: the same file, unexcluded, IS the problem every launch refuses.
			if rc == 0 || !strings.Contains(report, `"briefing/CLAUDE.md"`) {
				t.Fatalf("fixture control: an unexcluded briefing/CLAUDE.md must refuse the apply; "+
					"rc=%d\n%s", rc, report)
			}
			continue
		}
		if rc != 0 {
			t.Fatalf("host apply refused a pack whose only problem its entry excludes; rc=%d\n%s",
				rc, report)
		}
		if strings.Contains(report, "every launch refuses it too") {
			t.Errorf("the report claims a launch refuses a pack the launch stages clean:\n%s", report)
		}
		mustExist(t, filepath.Join(home, ".claude", "skills", "fltskill", "SKILL.md"),
			"the filtered pack's skill")
	}
}

// A MANIFEST THE FILTERS DROP IS NOT READ, so it cannot be refused either: `only` keeps skills/
// and drops a pack.json this yolo cannot decode. The launch sees a manifest-less pack; host apply
// applies the same pack.
func TestApplyHostAppliesAPackWhoseFilterDropsAnUndecodableManifest(t *testing.T) {
	const undecodable = `{"name":"flt","fieldFromANewerYolo":1,"contributes":[]}`
	for _, filtered := range []bool{true, false} {
		filter := ""
		if filtered {
			filter = `,"only":["skills/**"]`
		}
		home := filteredPackHome(t, undecodable, nil, filter, "")
		rc, report := applyWith(t, true, strings.NewReader("y\n"))
		if !filtered {
			if rc == 0 || !strings.Contains(report, "fieldFromANewerYolo") {
				t.Fatalf("fixture control: an unfiltered undecodable manifest must refuse; rc=%d\n%s",
					rc, report)
			}
			continue
		}
		if rc != 0 {
			t.Fatalf("host apply refused a pack whose filter drops the manifest; rc=%d\n%s", rc, report)
		}
		mustExist(t, filepath.Join(home, ".claude", "skills", "fltskill", "SKILL.md"),
			"a manifest-less pack's conventional skills/")
	}
}

// NOTHING IS APPLIED FROM A MANIFEST THE FILTERS DROP — clean or malformed. The launch loads the
// filtered tree, which has no manifest, so a config-overlay declared there reaches no jail; it
// must not reach the real home either. Checking problems on the filtered tree while still reading
// declarations from the unfiltered one would apply both of these.
//
// Under `host_management: "own"`: the unset key is `none` since the `assert` retirement
// (OQ-CO14), under which the host composes no config surface, so the clean control's overlay
// could never land and "left out" would be true with or without the filter.
func TestApplyHostAppliesNoDeclarationFromAManifestTheFiltersDrop(t *testing.T) {
	const overlay = `{"kind":"config-overlay","surface":"claude/settings",` +
		`"config":{"managed":{"fltOverlayKey":"from-flt"}}}`
	manifests := map[string]string{
		"clean": `{"name":"flt","description":"d","contributes":[` + overlay + `]}`,
		"malformed": `{"name":"flt","description":"d","contributes":[` + overlay + `,
  {"kind":"autonomy","guarded":{"launch":[{"bin":"claude","flags":[]}]}},
  {"kind":"autonomy","autonomous":{"launch":[{"bin":"claude","flags":["--x"]}]}}]}`,
	}
	for name, manifest := range manifests {
		for _, filtered := range []bool{true, false} {
			filter := ""
			if filtered {
				filter = `,"only":["skills/**"]`
			}
			home := filteredPackHome(t, manifest, nil, filter, `,"host_management":"own"`)
			rc, report := applyWith(t, true, strings.NewReader("y\n"))
			if !filtered {
				// The controls: unfiltered, the clean manifest's key lands and the malformed one
				// refuses — so the filtered run below has something to leave out.
				if name == "clean" {
					if rc != 0 || settingsKeys(t, home)["fltOverlayKey"] != "from-flt" {
						t.Fatalf("fixture control: the unfiltered overlay did not land; rc=%d\n%s", rc, report)
					}
				} else if rc == 0 {
					t.Fatalf("fixture control: an unfiltered malformed manifest must refuse\n%s", report)
				}
				continue
			}
			if rc != 0 {
				t.Fatalf("%s: rc=%d\n%s", name, rc, report)
			}
			if _, has := settingsKeys(t, home)["fltOverlayKey"]; has {
				t.Errorf("%s: host apply applied a config-overlay from a manifest the entry's `only` "+
					"drops, which no launch reads\n%s", name, report)
			}
		}
	}
}

// `yolo host env` (and `yolo host --`, one composeHostVars) composes a filtered pack's env: the
// resolver no longer refuses, as malformed, a pack whose problem its entry excludes. Unexcluded,
// the same problem refuses the launch (NC-D5), as it refuses a jail's.
func TestHostEnvComposesAPackWhoseOnlyProblemIsAnExcludedFile(t *testing.T) {
	manifest := `{"name":"flt","description":"d","contributes":[
  {"kind":"env","vars":{"FLT_PACK_VAR":"from-flt"}}]}`
	for _, filtered := range []bool{true, false} {
		filter := ""
		if filtered {
			filter = `,"exclude":["briefing/CLAUDE.md"]`
		}
		filteredPackHome(t, manifest, reservedBriefing, filter, "")
		var out, errw bytes.Buffer
		rc := hostMain([]string{"env", "--agent", "bash"}, &out, &errw, false, nil)
		has := strings.Contains(out.String(), "FLT_PACK_VAR")
		if !filtered {
			if rc == 0 || has || !strings.Contains(errw.String(), "flt") {
				t.Fatalf("fixture control: an unexcluded reserved file must refuse the launch, "+
					"naming the pack: rc=%d\n%s%s", rc, out.String(), errw.String())
			}
			continue
		}
		if rc != 0 {
			t.Fatalf("yolo host env rc=%d\n%s%s", rc, out.String(), errw.String())
		}
		if !has {
			t.Errorf("yolo host env dropped a pack whose only problem its entry excludes:\n%s%s",
				out.String(), errw.String())
		}
	}
}

// THE FOOTER READS THE SAME FILTERED DECLARATION the host launch composes (footerHostPacks): a
// provider declared by a pack whose only problem is excluded is in its table, and one declared by
// a manifest the filters drop is not.
func TestHostFooterTablesReadTheFilteredDeclaration(t *testing.T) {
	const manifest = `{"name":"flt","description":"d","contributes":[
  {"kind":"provider","name":"fltprov","endpoints":{"openai":{"base_url":"https://flt.example/v1"}}}]}`
	const selects = `,"profiles":{"fp":{"provider":"fltprov"}},"profile":{"claude":"fp"}`
	cases := []struct {
		name, filter string
		files        map[string]string
		want         bool
	}{
		{"excluded problem", `,"exclude":["briefing/CLAUDE.md"]`, reservedBriefing, true},
		{"unexcluded problem (control)", "", reservedBriefing, false},
		{"manifest filtered out", `,"only":["skills/**"]`, nil, false},
		{"manifest kept (control)", "", nil, true},
	}
	for _, c := range cases {
		filteredPackHome(t, manifest, c.files, c.filter, selects)
		tables := hostFooterTables()
		if has := strings.Contains(tables.Providers, "flt.example"); has != c.want {
			t.Errorf("%s: provider in the footer's table = %v, want %v: %+v", c.name, has, c.want, tables)
		}
	}
}
