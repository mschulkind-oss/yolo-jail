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
