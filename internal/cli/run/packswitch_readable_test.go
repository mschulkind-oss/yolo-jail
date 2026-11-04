package run

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// `packs` IS THE ONLY SWITCH in a TOML user config (config-user.md §7): `loopholes.<name>.enabled`
// has no TOML spelling, so a selected pack's loophole runs, and its `[allow.<pack>]` table is the
// loophole's settings. The legacy spelling (`loopholes.aws-auth.enabled` + `.settings`) and the
// converted one must plan the same launch: aws-auth enabled, with the same settings. The converted
// table is tested both in config.toml itself and in an included file, which is where the owner's
// conversion put it. Before the fix the TOML launches left aws-auth off (its manifest then defaulted
// it off) and handed its daemon no profile.
func TestATOMLUserConfigRunsASelectedPacksLoopholeWithItsAllowSettings(t *testing.T) {
	const legacy = `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "sso-bedrock", "unnarrowed": true}}}}`
	const table = "[allow.aws-auth]\nprofile = \"sso-bedrock\"\nunnarrowed = true\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"legacy jsonc", map[string]string{"config.jsonc": legacy}},
		{"toml main file", map[string]string{"config.toml": "format = 2\npacks = [\"aws-auth\"]\n\n" + table}},
		{"toml included file", map[string]string{
			"config.toml": "format = 2\ninclude_if_found = [\"work.toml\"]\n",
			"work.toml":   "format = 2\npacks = [\"aws-auth\"]\n\n" + table,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ws := t.TempDir()
			cfg, err := config.LoadConfig(ws, true, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			module := shippedPackLoopholeModule(t, "aws-auth")
			set := loopholes.NewSelectedSet(cfgMap(cfg, "loopholes"), []loopholes.PackModule{module}, nil)
			lp, ok := set.Lookup("aws-auth")
			if !ok {
				t.Fatal("aws-auth was not discovered")
			}
			if !lp.Enabled {
				t.Fatalf("aws-auth is disabled; a selected pack's loophole must run (config: %v)", cfg)
			}
			o := goldenOptions(ws, home)
			var stderr bytes.Buffer
			o.Stderr = &stderr
			plan := o.prepareLoopholeSettings([]*loopholes.Loophole{lp}, cfg)
			values := plan.values["aws-auth"]
			if values == nil {
				t.Fatalf("no settings resolved for aws-auth\n%s", stderr.String())
			}
			assertSetting(t, values, "profile", "sso-bedrock")
			assertSetting(t, values, "unnarrowed", true)
		})
	}
}

func assertSetting(t *testing.T, values *jsonx.OrderedMap, key string, want any) {
	t.Helper()
	got, _ := values.Get(key)
	if got != want {
		t.Errorf("settings.%s = %v, want %v", key, got, want)
	}
}

// What the owner's `--user-layer bedrock-key.jsonc` wants, a Bedrock API key the aws-auth pointer
// does not override, is given two ways over a TOML user config: the layer's
// `loopholes.aws-auth.enabled: false`, which keeps aws-auth off (its off switch,
// docs/design/legacy-config-compat.md LC-D3), and a layer that names no profile: an aws-auth with
// no profile serves nothing, so its pointer is withheld and the key beside it overrides nothing
// (packload.ServedDaemons.WithoutUnconfiguredLogins). This pins both halves over a TOML user config
// whose `[allow.aws-auth]` names a profile, through the real user-layer loader and the setting
// reader the launch hands that filter (loopholes.ConfiguredSetting): with no layer the table's profile
// counts, so the pointer is served.
func TestALegacyLayerTakesTheAWSPointerAwayByNamingNoProfile(t *testing.T) {
	home := packHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"),
		[]byte("format = 2\npacks = [\"aws-auth\"]\n\n[allow.aws-auth]\nprofile = \"sso-bedrock\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packs := []*packload.Pack{officialPack(t, "aws-auth")}
	serves := func(layerBody string) (enabled, served bool) {
		t.Helper()
		layer := filepath.Join(t.TempDir(), "bedrock-key.jsonc")
		if err := os.WriteFile(layer, []byte(layerBody), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv(config.UserLayerEnv, layer)
		cfg, err := config.LoadConfig(t.TempDir(), true, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		set := loopholes.NewSelectedSet(loopholes.LaunchBlock(cfg), []loopholes.PackModule{shippedPackLoopholeModule(t, "aws-auth")}, nil)
		lp, ok := set.Lookup("aws-auth")
		if !ok {
			t.Fatal("aws-auth was not discovered")
		}
		return lp.Enabled, packload.ServedInJail([]string{"aws-auth"}).
			WithoutUnconfiguredLogins(packs, loopholes.ConfiguredSetting(cfg)).Serves("aws-auth")
	}
	if enabled, served := serves(`{}`); !enabled || !served {
		t.Errorf("the TOML table's profile gave enabled=%v served=%v; want both", enabled, served)
	}
	if enabled, _ := serves(`{"loopholes": {"aws-auth": {"enabled": false}}}`); enabled {
		t.Error("a layer's `enabled: false` left aws-auth running; it is aws-auth's off switch")
	}
	if enabled, served := serves(`{"loopholes": {"aws-auth": {"settings": {"profile": ""}}}}`); !enabled || served {
		t.Errorf("a layer naming no profile gave enabled=%v served=%v; want the loophole running and "+
			"its pointer withheld", enabled, served)
	}
}
