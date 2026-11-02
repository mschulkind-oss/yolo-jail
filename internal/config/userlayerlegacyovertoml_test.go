package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// legacyLayerOverTOMLFixture is the owner's setup, synthetic: a TOML user config naming an
// aws-auth profile and one env file, and a legacy JSONC --user-layer that empties the profile and
// adds a second env file. It returns the workspace to read from and the layer's path.
func legacyLayerOverTOMLFixture(t *testing.T) (string, string) {
	t.Helper()
	home := layerHome(t, "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, paths.UserConfigTOMLName), "packs = [\"aws-auth\"]\n\n[env]\nfiles = [\"~/.config/yolo-jail/base.env\"]\n\n"+
		"[allow.aws-auth]\nprofile = \"base-profile\"\n")
	write(t, filepath.Join(dir, "base.env"), "BASE=1\n")
	write(t, filepath.Join(dir, "layer.env"), "LAYER=1\n")
	layer := filepath.Join(dir, "bedrock-key.jsonc")
	write(t, layer, `{
  "env_sources": ["~/.config/yolo-jail/layer.env"],
  "loopholes": { "aws-auth": { "settings": { "profile": "" } } }
}
`)
	if msg := ValidateUserLayer(layer); msg != "" {
		t.Fatalf("the front door refuses the layer: %s", msg)
	}
	t.Setenv(UserLayerEnv, layer)
	return t.TempDir(), layer
}

func stringAt(m *jsonx.OrderedMap, path ...string) (string, bool) {
	var cur any = m
	for _, step := range path {
		node, ok := cur.(*jsonx.OrderedMap)
		if !ok {
			return "", false
		}
		if cur, ok = node.Get(step); !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

// A legacy JSONC --user-layer over a TOML user config is read in the TOML spelling the base
// uses, so one key holds one answer: the layer's empty aws-auth profile replaces the base's
// `allow.aws-auth.profile` (rather than sitting beside it as `loopholes.aws-auth.settings.profile`,
// where `yolo config why` and `config dump` reported the base's value), and its env file loads
// after the base's, as a layer's entries always do (it used to load first, so the base's file
// won every name the two share).
func TestALegacyLayerOverATOMLUserConfigSpeaksTheBasesSpelling(t *testing.T) {
	ws, layer := legacyLayerOverTOMLFixture(t)

	for name, read := range map[string]func() (*jsonx.OrderedMap, *Sources, error){
		"the user scope": func() (*jsonx.OrderedMap, *Sources, error) { return UserScopeConfigWithSources(false, func(string) {}) },
		"the launch":     func() (*jsonx.OrderedMap, *Sources, error) { return LoadConfigWithSources(ws, false, func(string) {}) },
	} {
		t.Run(name, func(t *testing.T) {
			cfg, src, err := read()
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := stringAt(cfg, "allow", "aws-auth", "profile"); !ok || got != "" {
				t.Errorf("allow.aws-auth.profile = %q (set %v), want the layer's empty string", got, ok)
			}
			if _, legacy := cfg.Get("loopholes"); legacy {
				t.Errorf("the merged config still holds the legacy loopholes block beside allow: %s", dump(cfg))
			}
			if _, legacy := cfg.Get("env_sources"); legacy {
				t.Errorf("the merged config still holds env_sources beside env.files: %s", dump(cfg))
			}
			var files []string
			for _, e := range EnvEntries(cfg) {
				if s, ok := e.(string); ok {
					files = append(files, filepath.Base(s))
				}
			}
			if strings.Join(files, ",") != "base.env,layer.env" {
				t.Errorf("env files load as %v, want the base's then the layer's", files)
			}
			if locs := src.Locations("config.allow.aws-auth.profile"); len(locs) == 0 || !strings.Contains(locs[0], filepath.Base(layer)) {
				t.Errorf("allow.aws-auth.profile is located at %v, want the layer first", locs)
			}
		})
	}
}

func dump(m *jsonx.OrderedMap) string {
	s, _ := jsonx.DumpsCompact(m)
	return s
}
