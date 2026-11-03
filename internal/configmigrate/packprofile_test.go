package configmigrate

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The base user configs the owner's 2026-11-02 shape (synthetic) is read over: an all-JSONC user
// config that includes overrides.jsonc, a TOML one that includes it (a piecemeal mix), and a TOML
// one that does not include it, the file then migrated as a --user-layer
// (`yolo config migrate FILE`).
const (
	baseJSONCIncludes = "config.jsonc includes it"
	baseTOMLIncludes  = "config.toml includes it"
	baseLayer         = "the file as a --user-layer"
)

// packProfileFixture writes the owner's shape: a user config selecting a local pack that declares
// the profile "or", and an overrides.jsonc carrying its own provider and profile, an empty
// mcp_servers, a cache relocation, and a `profile` map naming the pack's profile for every agent
// and a null for one.
func packProfileFixture(t *testing.T, base string) (dir, overrides, ws string) {
	t.Helper()
	home := env(t)
	dir = filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "packs", "routes", "pack.toml"), `name = "routes"

[[profile]]
name = "or"
provider = "openrouter"
`)
	legacyPacks := `"packs": ["claude", "agy", "openrouter", "` + filepath.Join(dir, "packs", "routes") + `"]`
	tomlPacks := `packs = ["claude", "agy", "openrouter", { pack = "local/routes", bare = true }]` + "\n"
	switch base {
	case baseTOMLIncludes:
		write(t, filepath.Join(dir, "config.toml"), tomlPacks+`include_if_found = ["overrides.jsonc"]`+"\n")
	case baseJSONCIncludes:
		write(t, filepath.Join(dir, "config.jsonc"), `{`+legacyPacks+`, "include_if_found": ["overrides.jsonc"]}`+"\n")
	default:
		write(t, filepath.Join(dir, "config.toml"), tomlPacks)
	}
	overrides = filepath.Join(dir, "overrides.jsonc")
	// A relocation target whose parent exists on every host (macOS has no /var/cache).
	pip, _ := json.Marshal(filepath.Join(dir, "pip-elsewhere"))
	write(t, overrides, `{
  "providers": { "local": { "endpoints": { "anthropic": { "base_url": "http://localhost:8080" } }, "api_key_env_name": "LOCAL_API_KEY", "models": { "default": { "id": "local-default" } } } },
  "profiles": { "local": { "provider": "local", "model": "default" } },
  "mcp_servers": {},
  "cache_relocations": { "pip": `+string(pip)+` },
  "profile": { "agy": null, "*": "or" }
}
`)
	return dir, overrides, realDir(t)
}

// profileSetsRead is the agent-to-profile selection the launch folds from the config it reads
// (with layer passed as --user-layer when set), over the packs the user scope selects (the packs
// validation resolves).
func profileSetsRead(t *testing.T, ws, layer string) map[string][]string {
	t.Helper()
	t.Setenv(config.UserLayerEnv, layer)
	defer t.Setenv(config.UserLayerEnv, "")
	cfg, _, err := config.LoadConfigWithSources(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	packs, complete := config.UserScopeSelectedPacks()
	if !complete {
		t.Fatal("the fixture's packs did not all resolve")
	}
	return packload.ProfileSets(config.ConfigProfileFold(cfg, packs).Table)
}

// packProfileGate is the launch's config gate over the user config in dir (with layer passed as
// --user-layer when set), the undeclared-profile refusal included.
func packProfileGate(t *testing.T, dir, ws, layer string) []string {
	t.Helper()
	t.Setenv(config.UserLayerEnv, layer)
	defer t.Setenv(config.UserLayerEnv, "")
	return config.RunMigrationGate(dir, ws, loopholes.NewResolver()).Problems
}

// A `profile` naming a profile a selected pack declares, not one the user files declare, converts
// to active_provider naming that same profile: the launch reads the two keys through one fold
// against one set of declared names, so the selection is the same. A null is "native". It
// converts over every base the owner's file can sit on, and the launch accepts the result.
func TestAProfileAPackDeclaresConvertsToActiveProvider(t *testing.T) {
	for _, base := range []string{baseJSONCIncludes, baseTOMLIncludes, baseLayer} {
		t.Run(base, func(t *testing.T) {
			dir, overrides, ws := packProfileFixture(t, base)
			layer := ""
			if base == baseLayer {
				layer = overrides
			}
			before := profileSetsRead(t, ws, layer)
			if !reflect.DeepEqual(before["claude"], []string{"or"}) || before["agy"] != nil {
				t.Fatalf("the fixture does not select the pack's profile for claude and none for agy: %v", before)
			}
			if problems := packProfileGate(t, dir, ws, layer); len(problems) > 0 {
				t.Fatalf("the launch refuses the fixture itself: %v", problems)
			}
			p := plan(t, Request{UserConfigDir: dir, Workspace: ws, Layer: layer})
			if len(p.NotConverted) > 0 {
				t.Fatalf("not converted: %+v", p.NotConverted)
			}
			if _, err := Apply(p, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			converted := filepath.Join(dir, "overrides.toml")
			got := readFile(t, converted)
			for _, want := range []string{"[active_provider]", `default = "or"`, `agy = "native"`, "[providers.local]", `model = "default"`} {
				if !strings.Contains(got, want) {
					t.Errorf("overrides.toml lacks %s:\n%s", want, got)
				}
			}
			if layer != "" {
				layer = converted
			}
			if after := profileSetsRead(t, ws, layer); !reflect.DeepEqual(after, before) {
				t.Errorf("the converted config selects differently:\n  before %v\n  after  %v", before, after)
			}
			if problems := packProfileGate(t, dir, ws, layer); len(problems) > 0 {
				t.Errorf("the launch refuses the converted config: %v", problems)
			}
		})
	}
}

// A shipped pack's profile in a list converts the same way, beside a user-declared one, and the
// inherited delivery (the same lowering) carries it rather than declining.
func TestAShippedPackProfileInAListConvertsAndCrossesInherited(t *testing.T) {
	env(t)
	values := mustJSON(t, `{"packs": ["claude", "bedrock"],
	  "profiles": {"work": {"provider": "bedrock", "model": "opus"}},
	  "profile": {"claude": ["bedrock-bridge", "work"]}}`)
	lowered, err := LowerInheritedUserScope(values)
	if err != nil {
		t.Fatalf("the inherited delivery declined a pack's profile: %v", err)
	}
	sel, _ := mapAt(lowered, "active_provider")
	if sel == nil {
		t.Fatalf("no active_provider in %v", lowered)
	}
	v, _ := sel.Get("claude")
	if !reflect.DeepEqual(v, []any{"bedrock-bridge", "bedrock/work"}) {
		t.Fatalf("active_provider.claude = %#v", v)
	}
}

// A name holding '/' means provider/variant in active_provider and no such thing in the legacy
// key, so it cannot be carried as written; it is reported, naming the next step.
func TestASlashedUndeclaredProfileNameIsReported(t *testing.T) {
	_, reason := selectionValue(newUserView(nil), "a/b")
	if reason == "" || !strings.Contains(reason, "provider/variant") {
		t.Fatalf("reason = %q", reason)
	}
}
