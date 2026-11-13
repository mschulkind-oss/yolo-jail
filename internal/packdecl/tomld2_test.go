package packdecl

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

func TestDecodeTOMLGroupedLaunchRowsRetainBothPosturesAndPublicMetadata(t *testing.T) {
	const data = `[[autonomy]]
autonomous = { launch = [{ bin = "pi", flags = ["--permission"] }], config = [{ agent = "pi", name = "settings", codec = "json", path = "~/.pi/settings.json", managed = { autonomous = true } }] }
guarded = { launch = [{ bin = "pi", flags = ["--ask"] }], lists = [{ surface = "pi/settings", path = "/packages", add = ["guarded"] }] }

[[launch]]
bin = "pi"
flags = ["--first"]

[[program]]
bin = "fd"

[[launch]]
bin = "pi"
flags = ["--second"]
replace = true
`
	m, problems := DecodeTOML([]byte(data))
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	if len(m.AutonomyContributions().Autonomous.Config) == 0 || len(m.AutonomyContributions().Guarded.Lists) != 1 {
		t.Fatalf("launch lowering dropped other posture content: %#v", m.AutonomyContributions())
	}
	for _, autonomy := range []bool{true, false} {
		posture := m.PostureFor(autonomy)
		if posture == nil || len(posture.Launch) != 3 {
			t.Fatalf("PostureFor(%t) launch rows = %#v", autonomy, posture)
		}
		wantFlags := [][]string{{"--permission"}, {"--first"}, {"--second"}}
		if !autonomy {
			wantFlags = [][]string{{"--ask"}, {"--first"}, {"--second"}}
		}
		for i, want := range wantFlags {
			row := posture.Launch[i]
			if !reflect.DeepEqual(row.Flags, want) {
				t.Errorf("PostureFor(%t).Launch[%d].Flags = %v, want %v", autonomy, i, row.Flags, want)
			}
			if i == 0 {
				wantKey := "autonomy[0].autonomous.launch[0]"
				if !autonomy {
					wantKey = "autonomy[0].guarded.launch[0]"
				}
				if row.Origin.Format != ManifestFormatTOML || row.Origin.File != ManifestTOMLName || row.Location.Key != wantKey || row.Location.Line == 0 || row.Location.Column == 0 || row.Replace {
					t.Errorf("nested autonomy launch metadata = %#v", row)
				}
				continue
			}
			wantKey := []string{"", "launch[0]", "launch[1]"}[i]
			if row.Origin.Format != ManifestFormatTOML || row.Origin.File != ManifestTOMLName || row.Location.File != ManifestTOMLName || row.Location.Key != wantKey || row.Location.Line == 0 || row.Location.Column == 0 {
				t.Errorf("grouped launch row metadata = %#v", row)
			}
			if row.Replace != (i == 2) {
				t.Errorf("launch row replace = %v, want %v", row.Replace, i == 2)
			}
		}
	}
	// Decoder-only row provenance does not become manifest JSON fields.
	encoded, err := json.Marshal(m.Contributions())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("Origin")) || bytes.Contains(encoded, []byte("Location")) || bytes.Contains(encoded, []byte("Replace")) {
		t.Fatalf("launch provenance leaked into normalized JSON: %s", encoded)
	}
	// Accessors own their returned launch data; callers cannot mutate manifest state.
	m.PostureFor(true).Launch[0].Flags[0] = "mutated"
	if got := m.PostureFor(true).Launch[0].Flags[0]; got != "--permission" {
		t.Fatalf("PostureFor returned aliased launch flags: %q", got)
	}
}

func TestDecodeTOMLLaunchWithoutAutonomyCreatesOneSelectedPostureChannel(t *testing.T) {
	m, problems := DecodeTOML([]byte(`[[launch]]
bin = "pi"
flags = ["--ordinary"]
`))
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	if len(m.AutonomyContributions().Autonomous.Launch) != 1 || len(m.AutonomyContributions().Guarded.Launch) != 1 {
		t.Fatalf("ordinary launch row did not reach both existing posture halves: %#v", m.AutonomyContributions())
	}
	if len(m.Contributions()) != 1 || m.Contributions()[0].Kind != KindAutonomy {
		t.Fatalf("launch created a second channel: %#v", m.Contributions())
	}
}

func TestDecodeTOMLGroupedLaunchRowsFollowAuthoredOrderAcrossAutonomy(t *testing.T) {
	const data = `[[launch]]
bin = "pi"
flags = ["--before"]
replace = true

[[autonomy]]
autonomous = { launch = [{ bin = "pi", flags = ["--dangerously-skip-permissions"] }], config = [{ agent = "pi", name = "settings", codec = "json", path = "~/.pi/settings.json", managed = { autonomous = true } }] }
guarded = { launch = [{ bin = "pi", flags = ["--ask"] }], lists = [{ surface = "pi/settings", path = "/packages", add = ["guarded"] }] }

[[launch]]
bin = "pi"
flags = ["--after"]
`
	m, problems := DecodeTOML([]byte(data))
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	for _, autonomy := range []bool{true, false} {
		posture := m.PostureFor(autonomy)
		if posture == nil || len(posture.Launch) != 3 {
			t.Fatalf("PostureFor(%t).Launch = %#v", autonomy, posture)
		}
		want := [][]string{{"--before"}, {"--dangerously-skip-permissions"}, {"--after"}}
		if !autonomy {
			want = [][]string{{"--before"}, {"--ask"}, {"--after"}}
		}
		for i, flags := range want {
			if !reflect.DeepEqual(posture.Launch[i].Flags, flags) {
				t.Errorf("PostureFor(%t).Launch[%d].Flags = %v, want %v", autonomy, i, posture.Launch[i].Flags, flags)
			}
		}
		before, nested, after := posture.Launch[0], posture.Launch[1], posture.Launch[2]
		if before.Location.Key != "launch[0]" || before.Location.Line != 1 || !before.Replace || before.Origin.Format != ManifestFormatTOML {
			t.Errorf("before row lost source metadata: %#v", before)
		}
		wantNested := "autonomy[0].autonomous.launch[0]"
		if !autonomy {
			wantNested = "autonomy[0].guarded.launch[0]"
		}
		if nested.Location.Key != wantNested || nested.Replace || nested.Origin.Format != ManifestFormatTOML {
			t.Errorf("autonomy row lost source metadata: %#v", nested)
		}
		if after.Location.Key != "launch[1]" || after.Replace || after.Origin.Format != ManifestFormatTOML {
			t.Errorf("after row lost source metadata: %#v", after)
		}
	}
	if ac := m.AutonomyContributions(); len(ac.Autonomous.Config) == 0 || len(ac.Guarded.Lists) != 1 {
		t.Fatalf("launch lowering dropped autonomy config/list content: %#v", ac)
	}
}

func TestDecodeTOMLGroupedLaunchDiagnosticsRetainRowLocationAcrossReaderModes(t *testing.T) {
	for _, tc := range []struct {
		name, row, want string
	}{
		{name: "unknown", row: "future_flag = true", want: `unknown field "future_flag"`},
		{name: "shared rename", row: `carry = "main"`, want: `"carry" was renamed to "patch_branch"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(`[[launch]]
bin = "pi"
flags = ["--unknown"]
` + tc.row + `

[[autonomy]]
autonomous = { launch = [{ bin = "pi", flags = ["--permission"] }] }
guarded = { launch = [{ bin = "pi", flags = ["--ask"] }] }

[[launch]]
bin = "pi"
flags = ["--sibling"]
`)
			const location = "pack.toml:1:1 (launch[0])"
			if _, problems := DecodeTOML(data); len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), location) || !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("strict diagnostics lost launch row location or shared validation: %v", problems)
			}
			for name, decode := range map[string]func([]byte) (*Manifest, []string, []string){"use": DecodeTOMLForUse, "tolerant": DecodeTOMLTolerant} {
				m, problems, skipped := decode(data)
				joined := strings.Join(skipped, "\n")
				if len(problems) != 0 || len(skipped) != 1 || !strings.Contains(joined, location) || !strings.Contains(joined, tc.want) || strings.Contains(joined, "autonomy[0]") {
					t.Errorf("%s diagnostics lost launch row location or shared skew policy: problems=%v skipped=%v", name, problems, skipped)
					continue
				}
				for _, posture := range []bool{true, false} {
					got := m.PostureFor(posture)
					wantNested := map[bool]string{true: "autonomy[0].autonomous.launch[0]", false: "autonomy[0].guarded.launch[0]"}[posture]
					if got == nil || len(got.Launch) != 3 || got.Launch[0].Location.Key != "launch[0]" || got.Launch[1].Location.Key != wantNested || got.Launch[2].Location.Key != "launch[1]" {
						t.Errorf("%s PostureFor(%t) disturbed retained sibling rows: %#v", name, posture, got)
					}
				}
			}
		})
	}
}

func TestDecodeTOMLGroupedLaunchEmptyBinDiagnosticNamesLaunchRow(t *testing.T) {
	data := []byte(`[[launch]]
bin = ""
flags = ["--bad"]

[[autonomy]]
autonomous = { launch = [{ bin = "pi", flags = ["--permission"] }] }
guarded = { launch = [{ bin = "pi", flags = ["--ask"] }] }
`)
	for name, decode := range map[string]func([]byte) (*Manifest, []string, []string){
		"strict":   func(data []byte) (*Manifest, []string, []string) { m, p := DecodeTOML(data); return m, p, nil },
		"use":      DecodeTOMLForUse,
		"tolerant": DecodeTOMLTolerant,
	} {
		_, problems, _ := decode(data)
		joined := strings.Join(problems, "\n")
		if len(problems) == 0 || !strings.Contains(joined, "pack.toml:1:1 (launch[0])") || !strings.Contains(joined, "bin") || strings.Contains(joined, "autonomy[0]") {
			t.Errorf("%s empty-bin diagnostic lost launch row location: %v", name, problems)
		}
	}
}

func TestDecodeTOMLConfigSurfacesInferKnownExtensionsBeforeValidation(t *testing.T) {
	const data = `[[config]]
config = [
  { agent = "pi", name = "json", path = "~/.pi/settings.jsonc" },
  { agent = "pi", name = "toml", path = "~/.pi/config.toml" },
  { agent = "pi", name = "yaml", path = "~/.pi/config.yaml" },
  { agent = "pi", name = "explicit", path = "~/.pi/config.json", codec = "raw" },
]
[[autonomy]]
autonomous = { config = [{ agent = "pi", name = "nested", path = "~/.pi/nested.json", managed = { allowed = true } }] }
`
	m, problems := DecodeTOML([]byte(data))
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	surfaces, problems := manifest.DecodeSurfaces(m.SurfaceContributions())
	if len(problems) != 0 {
		t.Fatalf("DecodeSurfaces after public reader: %v; raw=%s", problems, m.SurfaceContributions())
	}
	want := []string{"json", "toml", "yaml", "raw"}
	if len(surfaces) != len(want) {
		t.Fatalf("decoded surfaces = %#v", surfaces)
	}
	for i := range want {
		if surfaces[i].Codec != want[i] {
			t.Errorf("surface %q codec = %q, want %q", surfaces[i].Name, surfaces[i].Codec, want[i])
		}
	}
	postureOverlays := m.OverlayContributions()
	if len(postureOverlays) != 1 {
		t.Fatalf("posture overlay candidates = %#v", postureOverlays)
	}
	if _, _, wellFormed, problems := manifest.DecodePostureOverlay("nested", postureOverlays[0].Config); !wellFormed || len(problems) != 0 {
		t.Fatalf("nested surface did not reach DTO validation: wellFormed=%v problems=%v; config=%s", wellFormed, problems, postureOverlays[0].Config)
	}
}

func TestDecodeTOMLConfigSurfaceCodecInferenceRefusesUnmappedPaths(t *testing.T) {
	for name, path := range map[string]string{"extensionless": "~/.pi/settings", "unknown": "~/.pi/settings.conf"} {
		t.Run(name, func(t *testing.T) {
			data := []byte("[[config]]\nconfig = [{ agent = \"pi\", name = \"settings\", path = \"" + path + "\" }]\n")
			_, problems := DecodeTOML(data)
			if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "codec") || !strings.Contains(strings.Join(problems, "\n"), "config[0].config[0]") {
				t.Fatalf("missing actionable located codec error: %v", problems)
			}
		})
	}
}

func TestGroupedLaunchRejectsMalformedFieldsAndLegacyJSONStillRetiresLaunch(t *testing.T) {
	for name, data := range map[string]string{
		"empty bin":       "[[launch]]\nbin = \"\"\n",
		"non-string flag": "[[launch]]\nbin = \"pi\"\nflags = [1]\n",
		"flags not list":  "[[launch]]\nbin = \"pi\"\nflags = \"--x\"\n",
		"replace type":    "[[launch]]\nbin = \"pi\"\nreplace = \"yes\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, problems := DecodeTOML([]byte(data)); len(problems) == 0 {
				t.Fatal("malformed launch row was accepted")
			}
		})
	}
	if _, problems := Decode([]byte(`{"contributes":[{"kind":"launch","bin":"pi"}]}`)); len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), `kind "launch" has been REMOVED`) ||
		!strings.Contains(strings.Join(problems, "\n"), `an [[autonomy.autonomous.launch]] table with bin = "<bin>" and flags = ["<flag>"]`) {
		t.Fatalf("legacy JSON launch kind retirement changed: %v", problems)
	}
}

func TestGroupedLaunchUnknownFieldUsesSharedReaderSkewPolicy(t *testing.T) {
	data := []byte(`[[launch]]
bin = "pi"
flags = ["--safe"]
future_flag = true
`)
	if _, problems := DecodeTOML(data); len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "future_flag") {
		t.Fatalf("strict grouped launch reader did not refuse unknown field: %v", problems)
	}
	for name, decode := range map[string]func([]byte) (*Manifest, []string, []string){"use": DecodeTOMLForUse, "tolerant": DecodeTOMLTolerant} {
		m, problems, skipped := decode(data)
		if len(problems) != 0 || len(skipped) != 1 || m == nil || len(m.PostureFor(true).Launch) != 1 || m.PostureFor(true).Launch[0].Flags[0] != "--safe" {
			t.Errorf("%s grouped launch skew behavior: manifest=%#v problems=%v skipped=%v", name, m, problems, skipped)
		}
	}
}

func TestOverlayAccessorsExportDeepCopiedOriginAndRowMetadata(t *testing.T) {
	const data = `[[overlay]]
surface = "pi/settings"
set.theme = "dark"
replace = true

[[autonomy]]
autonomous = { config = [{ agent = "pi", name = "settings", path = "~/.pi/settings.jsonc", codec = "json", managed = { own = true } }, { agent = "claude", name = "settings", path = "~/.claude/settings.json", codec = "json", managed = { other = true } }] }
replace = true
`
	m, problems := DecodeTOML([]byte(data))
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	declared := m.ConfigOverlayContributions()
	all := m.OverlayContributions()
	if len(declared) != 1 || !declared[0].Replace || declared[0].Origin.Format != ManifestFormatTOML || declared[0].Origin.File != ManifestTOMLName || declared[0].Location.Key != "overlay[0]" || declared[0].Location.Line != 1 || declared[0].Location.Column == 0 {
		t.Fatalf("declared overlay metadata = %#v", declared)
	}
	if len(all) != 3 {
		t.Fatalf("all overlays = %#v", all)
	}
	for i, key := range []string{"autonomy[0].autonomous.config[0]", "autonomy[0].autonomous.config[1]"} {
		row := all[i+1]
		if row.Location.Key != key || row.Location.Line == 0 || row.Location.Column == 0 || row.Origin.Format != ManifestFormatTOML || row.Origin.File != ManifestTOMLName || !row.Replace {
			t.Errorf("nested overlay row %d metadata = %#v", i, row)
		}
	}
	if all[1].Location.Line != all[2].Location.Line || all[1].Location.Column >= all[2].Location.Column {
		t.Fatalf("nested config rows do not retain distinct source positions: %#v / %#v", all[1].Location, all[2].Location)
	}
	declared[0].Config[0] = 'x'
	all[1].Config[0] = 'x'
	if bytes.Equal(declared[0].Config, m.ConfigOverlayContributions()[0].Config) || bytes.Equal(all[1].Config, m.OverlayContributions()[1].Config) {
		t.Fatal("overlay accessors exposed aliased config bytes")
	}
}

func TestJSONOverlayAccessorRetainsLegacyOriginWithoutReplace(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[{"kind":"config-overlay","surface":"pi/settings","config":{"managed":{"theme":"dark"}}}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	rows := m.ConfigOverlayContributions()
	if len(rows) != 1 || rows[0].Origin.Format != ManifestFormatJSON || rows[0].Origin.File != ManifestName || rows[0].Location.File != ManifestName || rows[0].Replace {
		t.Fatalf("legacy overlay metadata = %#v", rows)
	}
}

func TestConfigOverlayMetadataIsOmittedFromContributionJSON(t *testing.T) {
	m, problems := DecodeTOML([]byte("[[overlay]]\nsurface = \"pi/settings\"\nset.theme = \"dark\"\nreplace = true\n"))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	encoded, err := json.Marshal(m.Contributions())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("Origin")) || bytes.Contains(encoded, []byte("Replace")) || bytes.Contains(encoded, []byte("Location")) {
		t.Fatalf("decoder-only metadata leaked into JSON: %s", encoded)
	}
}
