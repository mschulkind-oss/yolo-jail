package configmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE OWNER'S BEDROCK-KEY LAYER, synthetic: a TOML user config names aws-auth's profile and one
// env file, and a legacy `--user-layer bedrock-key.jsonc` empties the profile (so the login
// loophole serves nothing) and adds a second env file. `yolo config migrate FILE` converts it,
// with or without the `"enabled": true` the old refusal asked for: over a TOML user config the
// pack switch is the selection, so the layer's settings were never dormant. The converted layer
// keeps the empty profile, and a launch passing it reads the empty profile and the base's env file
// before the layer's, as a launch passing the old layer does.
//
// The refusal this replaces compared the layer merged over the base holding BOTH spellings of
// each key (the base's `allow.aws-auth.profile` beside the layer's
// `loopholes.aws-auth.settings.profile`), so it said the old files named the base's profile,
// and the env files loaded in the opposite order (the layer's `env_sources` ahead of the base's
// `env.files`), and its next step (`"enabled": true`) changed neither.
func TestTheBedrockKeyLayerConvertsAndKeepsItsEmptyProfile(t *testing.T) {
	for name, entry := range map[string]string{
		"as written":   `{ "settings": { "profile": "" } }`,
		"enabled true": `{ "enabled": true, "settings": { "profile": "" } }`,
	} {
		t.Run(name, func(t *testing.T) {
			home := env(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			write(t, filepath.Join(dir, paths.UserConfigTOMLName), "packs = [\"aws-auth\"]\n\n[env]\nfiles = [\"~/.config/yolo-jail/base.env\"]\n\n"+
				"[allow.aws-auth]\nprofile = \"base-profile\"\n")
			write(t, filepath.Join(dir, "base.env"), "BASE=1\n")
			write(t, filepath.Join(dir, "bedrock-key.env"), "LAYER=1\n")
			layer := filepath.Join(dir, "bedrock-key.jsonc")
			write(t, layer, "{\n  \"env_sources\": [\"~/.config/yolo-jail/bedrock-key.env\"],\n"+
				"  \"loopholes\": { \"aws-auth\": "+entry+" }\n}\n")

			g, err := Discover(Request{UserConfigDir: dir, Layer: layer})
			if err != nil {
				t.Fatal(err)
			}
			p, err := Plan(g)
			if err != nil {
				t.Fatalf("the layer is refused: %v", err)
			}
			text := string(target(t, p, layer).Bytes)
			for _, want := range []string{"[allow.aws-auth]", `profile = ""`, "bedrock-key.env"} {
				if !strings.Contains(text, want) {
					t.Errorf("the converted layer does not carry %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "base.env") || strings.Contains(text, "base-profile") {
				t.Errorf("the converted layer carries the base's values:\n%s", text)
			}
			if _, err := Apply(p, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			newLayer := filepath.Join(dir, "bedrock-key.toml")
			var cfg *jsonx.OrderedMap
			withUserLayer(newLayer, func() { cfg, err = config.LoadConfig(realDir(t), true, func(string) {}) })
			if err != nil {
				t.Fatal(err)
			}
			allow, _ := mapAt(cfg, "allow")
			aws, _ := mapAt(allow, "aws-auth")
			if got, ok := aws.Get("profile"); !ok || got != "" {
				t.Errorf("a launch passing the converted layer reads aws-auth's profile as %v, want \"\"", got)
			}
			var files []string
			for _, e := range config.EnvEntries(cfg) {
				if s, ok := e.(string); ok {
					files = append(files, filepath.Base(s))
				}
			}
			if strings.Join(files, ",") != "base.env,bedrock-key.env" {
				t.Errorf("a launch passing the converted layer loads %v, want the base's file then the layer's", files)
			}
			if _, err := os.Stat(layer + ".pre-migrate"); err != nil {
				t.Errorf("the original is not kept: %v", err)
			}
		})
	}
}
