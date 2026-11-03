package configmigrate

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// mixedSelectionFixture writes a piecemeal mix: an already-TOML user config whose providers table
// declares the openrouter variant "or", an alias "orr" and the openrouter provider's own model,
// including a still-JSONC overrides.jsonc whose legacy `profile` key selects selection.
func mixedSelectionFixture(t *testing.T, selection string) (dir, overrides, ws string) {
	t.Helper()
	home := env(t)
	dir = filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), `packs = ["claude", "openrouter"]
include_if_found = ["overrides.jsonc"]

[providers.openrouter]
alias = "orr"
model = "anthropic/claude-sonnet-4.5"

[providers.openrouter.variants.or]
model = "anthropic/claude-opus-4.1"
`)
	overrides = filepath.Join(dir, "overrides.jsonc")
	write(t, overrides, `{ "profile": `+selection+` }`+"\n")
	return dir, overrides, realDir(t)
}

// A legacy `profile` in a JSONC file names what an already-TOML file declares: a variant by its
// own name (what RC-D32 converts a profile to), a provider, or a provider's alias. The mix
// launches, selecting what active_provider gives that declaration, and migrating the JSONC file
// converts it to the active_provider spelling that selects the same thing (RC-D57).
func TestALegacyProfileNamingATOMLDeclarationLaunchesAndConverts(t *testing.T) {
	for _, tc := range []struct {
		name, selection, want string
		set                   []string
	}{
		{"a variant", `"or"`, `default = "openrouter/or"`, []string{"openrouter/or"}},
		{"a variant for every agent", `{"*": "or"}`, `default = "openrouter/or"`, []string{"openrouter/or"}},
		{"a variant in a list", `{"claude": ["or"]}`, `claude = ["openrouter/or"]`, []string{"openrouter/or"}},
		{"the provider", `"openrouter"`, `default = "openrouter"`, []string{"openrouter"}},
		{"the alias", `{"*": "orr"}`, `default = "orr"`, []string{"orr"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, ws := mixedSelectionFixture(t, tc.selection)
			if problems := packProfileGate(t, dir, ws, ""); len(problems) > 0 {
				t.Fatalf("the launch refuses the mix: %v", problems)
			}
			before := selectedProfiles(t, ws)
			if want := impliedFor(t, ws, tc.set); !reflect.DeepEqual(before, want) {
				t.Fatalf("the mix selects %+v for claude, want what %v selects, %+v", before, tc.set, want)
			}
			p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
			if len(p.NotConverted) > 0 {
				t.Fatalf("not converted: %+v", p.NotConverted)
			}
			if _, err := Apply(p, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			got := readFile(t, filepath.Join(dir, "overrides.toml"))
			if !strings.Contains(got, tc.want) {
				t.Errorf("overrides.toml lacks %s:\n%s", tc.want, got)
			}
			if problems := packProfileGate(t, dir, ws, ""); len(problems) > 0 {
				t.Errorf("the launch refuses the converted config: %v", problems)
			}
			if sets := profileSetsRead(t, ws, ""); !reflect.DeepEqual(sets["claude"], tc.set) {
				t.Errorf("the converted config names %v for claude, want %v", sets["claude"], tc.set)
			}
			if after := selectedProfiles(t, ws); !reflect.DeepEqual(after, before) {
				t.Errorf("the converted config selects %+v for claude, want %+v", after, before)
			}
		})
	}
}

// selectedProfiles is what each name the launch folds for claude resolves to, through the one
// namespace every reader hands packload.ResolveProfiles (config.LoadSelectableProfiles).
func selectedProfiles(t *testing.T, ws string) []packload.UserProfile {
	t.Helper()
	return impliedFor(t, ws, profileSetsRead(t, ws, "")["claude"])
}

// impliedFor is what each of names resolves to in the config's namespace.
func impliedFor(t *testing.T, ws string, names []string) []packload.UserProfile {
	t.Helper()
	cfg, _, err := config.LoadConfigWithSources(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	packs, _ := config.UserScopeSelectedPacks()
	profiles, err := config.LoadSelectableProfiles(nil, cfg, packs)
	if err != nil {
		t.Fatal(err)
	}
	var out []packload.UserProfile
	for _, name := range names {
		p, ok := profiles[name]
		if !ok && !packProfileDeclaredIn(packs, name) {
			t.Fatalf("%q resolves to nothing in %v", name, profiles)
		}
		out = append(out, p)
	}
	return out
}

func packProfileDeclaredIn(packs []*packload.Pack, name string) bool {
	for _, declared := range packload.DeclaredProfileNames(packs, nil) {
		if declared == name {
			return true
		}
	}
	return false
}

// Migrating one file on its own (`yolo config migrate FILE`, read as a --user-layer) over a JSONC
// base whose `packs` holds a path entry converts exactly: the base stays as written, and the
// proof reads its path pack the same on both sides rather than respelling one side's as
// `local/<name>` (RC-D58).
func TestALayerOverAJSONCBaseWithAPathPackConverts(t *testing.T) {
	for _, tc := range []struct{ name, layer, packDir string }{
		{"a layer selecting a pack profile", `{ "profile": {"*": "or"}, "cache_relocations": {"pip": "HOME/pip-x"} }`, ".config/yolo-jail/packs"},
		{"a layer naming packs of its own", `{ "packs": ["claude", "openrouter"] }`, ".config/yolo-jail/packs"},
		{"a base pack outside the packs directory", `{ "profile": {"*": "or"} }`, "src/mypacks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := env(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			routes := filepath.Join(home, tc.packDir, "routes")
			write(t, filepath.Join(routes, "pack.toml"), "name = \"routes\"\n\n[[profile]]\nname = \"or\"\nprovider = \"openrouter\"\n")
			base := filepath.Join(dir, "config.jsonc")
			baseText := `{"packs": ["claude", "openrouter", "` + routes + `"]}` + "\n"
			write(t, base, baseText)
			layer := filepath.Join(home, "elsewhere", "layer.jsonc")
			// HOME stands for the test home: a relocation target's parent must exist (macOS has no /var/cache).
			write(t, layer, strings.ReplaceAll(tc.layer, "HOME", filepath.ToSlash(home))+"\n")
			ws := realDir(t)
			p := plan(t, Request{UserConfigDir: dir, Workspace: ws, Layer: layer})
			if len(p.NotConverted) > 0 {
				t.Fatalf("not converted: %+v", p.NotConverted)
			}
			if _, err := Apply(p, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, base); got != baseText {
				t.Errorf("the base changed:\n%s", got)
			}
			converted := filepath.Join(home, "elsewhere", "layer.toml")
			if !exists(converted) {
				t.Fatalf("the layer was not converted")
			}
			if problems := packProfileGate(t, dir, ws, converted); len(problems) > 0 {
				t.Errorf("the launch refuses the converted layer: %v", problems)
			}
		})
	}
}

// A selected pack's profile of the same name keeps it, at launch and in the conversion: the
// profile is what already works (OQ-RC3), so the legacy name stays the pack's profile.
func TestAPackProfileKeepsItsNameOverATOMLVariant(t *testing.T) {
	dir, _, ws := mixedSelectionFixture(t, `"or"`)
	write(t, filepath.Join(dir, "packs", "routes", "pack.toml"), "name = \"routes\"\n\n[[profile]]\nname = \"or\"\nprovider = \"openrouter\"\n")
	write(t, filepath.Join(dir, "config.toml"), `packs = ["claude", "openrouter", { pack = "local/routes", bare = true }]
include_if_found = ["overrides.jsonc"]

[providers.openrouter.variants.or]
model = "anthropic/claude-opus-4.1"
`)
	if problems := packProfileGate(t, dir, ws, ""); len(problems) > 0 {
		t.Fatalf("the launch refuses the mix: %v", problems)
	}
	if got := profileSetsRead(t, ws, "")["claude"]; !reflect.DeepEqual(got, []string{"or"}) {
		t.Fatalf("claude selects %v", got)
	}
	if before := selectedProfiles(t, ws); !reflect.DeepEqual(before, []packload.UserProfile{{}}) {
		t.Fatalf("the variant took the pack profile's name: %+v", before)
	}
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if len(p.NotConverted) > 0 {
		t.Fatalf("not converted: %+v", p.NotConverted)
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "overrides.toml")); !strings.Contains(got, `default = "or"`) {
		t.Errorf("overrides.toml does not carry the pack profile by name:\n%s", got)
	}
}
