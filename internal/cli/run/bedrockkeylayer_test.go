package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// THE OWNER'S BEDROCK-KEY LAYER over a TOML user config. The user config names aws-auth's
// profile in `[allow.aws-auth]`; an opt-in `--user-layer bedrock-key.jsonc` delivers a static AWS
// key pair through env_sources and used to turn aws-auth off with `enabled: false`. That key
// switches nothing now (a pack's loophole runs whenever its pack is selected), so the served
// pointer beside the pair is refused, and the refusal must name the step that does work: the
// profile set to "" in that layer, so the login service serves nothing
// (packload.UnconfiguredLogins) and its pointer is withheld. With the layer saying so, the launch
// proceeds and the pointer is not in claude's environment.
func TestABedrockKeyLayerNamesTheProfileStepAndThenLaunches(t *testing.T) {
	const pair = `"env_sources": [{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret"}]`
	for _, tc := range []struct {
		name, layer string
		refuses     bool
	}{
		{"enabled false", `{"loopholes": {"aws-auth": {"enabled": false}}, ` + pair + `}`, true},
		{"profile empty", `{"loopholes": {"aws-auth": {"settings": {"profile": ""}}}, ` + pair + `}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserConfigTOML(t, home, "format = 2\npacks = [\"claude\", \"bedrock\", \"aws-auth\"]\n\n"+
				"[allow.aws-auth]\nprofile = \"sso-bedrock\"\n")
			layer := filepath.Join(t.TempDir(), "bedrock-key.jsonc")
			if err := os.WriteFile(layer, []byte(tc.layer), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv(config.UserLayerEnv, layer)
			cfg, err := config.LoadConfig(t.TempDir(), true, func(string) {})
			if err != nil {
				t.Fatal(err)
			}

			o := retireOptions(t, discardBuf())
			o.Getenv = shellWith(nil)
			o.ProfileName = "bedrock"
			selected := awsAuthSelected(t)
			t.Cleanup(loopholes.SnapshotPackModules())
			loopholes.SetPackModules(packLoopholeModules(selected))
			channel := channelFor(t, o, cfg, selected,
				userEnvWith(map[string]string{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret"}))
			got := strings.Join(o.checkEnvOverrides(cfg, "podman", selected, channel, nil), "\n")

			if !tc.refuses {
				if got != "" {
					t.Fatalf("with the profile emptied the launch was refused:\n%s", got)
				}
				if channel.served.Serves("aws-auth") {
					t.Error("with the profile emptied aws-auth is still served")
				}
				if v, ok := channel.launchEnv("claude").Get(pointerVar); ok {
					t.Errorf("claude still receives %s = %v", pointerVar, v)
				}
				return
			}
			for _, want := range []string{"Refusing to launch", "AWS_ACCESS_KEY_ID is delivered by",
				`set aws-auth's profile to ""`, `"loopholes": {"aws-auth": {"settings": {"profile": ""}}}`,
				`profile = "" under [allow.aws-auth]`, "withheld"} {
				if !strings.Contains(got, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "AKIAEXAMPLE") {
				t.Errorf("the refusal printed a credential VALUE:\n%s", got)
			}
		})
	}
}

// THE OWNER'S LAYER AS FILES, through the launch's own call sites: the TOML user config names
// aws-auth's profile and one env file, and the legacy `--user-layer bedrock-key.jsonc` empties the
// profile and adds a second env file. composePackChannel is handed no environment, so it hydrates
// env_sources itself (config.HydrateEnvSources) and filters the served logins
// (loopholes.ConfiguredSetting) from the config config.LoadConfig read, as a launch does. The
// layer's env file loads after the base's, so its value wins a name both set (it loaded first,
// and lost, while the layer's legacy env_sources sat beside the base's env.files), and the empty
// profile leaves aws-auth serving nothing.
func TestTheBedrockKeyLayerLoadsAfterTheBaseAndEmptiesTheProfileAtLaunch(t *testing.T) {
	home := packHome(t)
	writeUserConfigTOML(t, home, "format = 2\npacks = [\"claude\", \"bedrock\", \"aws-auth\"]\n\n"+
		"[env]\nfiles = [\"~/.config/yolo-jail/base.env\"]\n\n[allow.aws-auth]\nprofile = \"sso-bedrock\"\n")
	dir := filepath.Join(home, ".config", "yolo-jail")
	for name, body := range map[string]string{
		"base.env":          "LAYER_ORDER_PROBE=base\nBASE_ONLY=1\n",
		"bedrock-key.env":   "LAYER_ORDER_PROBE=layer\n",
		"bedrock-key.jsonc": `{"env_sources": ["~/.config/yolo-jail/bedrock-key.env"], "loopholes": {"aws-auth": {"settings": {"profile": ""}}}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(config.UserLayerEnv, filepath.Join(dir, "bedrock-key.jsonc"))
	ws := t.TempDir()
	cfg, err := config.LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}

	o := retireOptions(t, discardBuf())
	o.Workspace = ws
	o.Getenv = shellWith(nil)
	o.ProfileName = "bedrock"
	selected := awsAuthSelected(t)
	t.Cleanup(loopholes.SnapshotPackModules())
	loopholes.SetPackModules(packLoopholeModules(selected))
	channel := channelFor(t, o, cfg, selected, nil)

	if got, _ := channel.userEnv.Get("LAYER_ORDER_PROBE"); got != "layer" {
		t.Errorf("LAYER_ORDER_PROBE = %v, want the layer's value: its env file loads after the base's", got)
	}
	if got, _ := channel.userEnv.Get("BASE_ONLY"); got != "1" {
		t.Errorf("BASE_ONLY = %v, want the base's file still loaded", got)
	}
	if channel.served.Serves("aws-auth") {
		t.Error("the layer's empty profile did not reach the launch: aws-auth is still served")
	}
	if got := loopholes.ConfiguredSetting(cfg)("aws-auth", "profile"); got != "" {
		t.Errorf("the launch reads aws-auth's profile as %q, want the layer's empty string", got)
	}
}
