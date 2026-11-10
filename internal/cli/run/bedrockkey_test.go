package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// bedrockkey_test.go is docs/design/bedrock-key-provider.md at the launch's own call sites, over
// the shipped claude, aws-auth and bedrock packs with aws-auth's SSO profile configured: the key
// rides the private set, and the selection alone decides which credential an agent gets.

const (
	bedrockBearer = "AWS_BEARER_TOKEN_BEDROCK"
	bearerProbe   = "bk-probe-not-a-real-key"
)

// bedrockKeyChannel composes the launch's channel for profile with the bearer in the private set,
// and returns the options (stderr captured) and the channel.
func bedrockKeyChannel(t *testing.T, profile string) (*Options, *packChannel, *bytes.Buffer) {
	t.Helper()
	home := packHome(t)
	var stderr bytes.Buffer
	o := goldenOptions(t.TempDir(), home)
	o.Stderr = &stderr
	o.Getenv = shellWith(nil)
	o.ProfileName = profile
	selected := awsAuthSelected(t)
	cfg := awsAuthServedConfig(t, selected)
	// A user's own key provider whose name sorts before `bedrock`: the not-started line must still
	// offer the provider that claims the pointer, not the first name on the platform.
	cfg.Set("providers", newConfig("a-key", newConfig("platform", "aws-bedrock",
		"api_key_env_name", []any{bedrockBearer})))
	// Selecting through active_provider joins the user's providers table to the namespace whole
	// (config.LoadSelectableProfiles); the launch's -p overrides it for claude.
	cfg.Set("active_provider", newConfig("claude", "a-key"))
	h := config.NewHydratedEnv()
	h.SetPrivate(bedrockBearer, bearerProbe, config.EnvPlace{Private: true, Key: "env.secrets.set"})
	channel, err := o.composePackChannel(cfg, selected, h)
	if err != nil {
		t.Fatal(err)
	}
	if got := o.checkEnvOverrides(cfg, "podman", selected, channel, nil); len(got) != 0 {
		t.Fatalf("-p %s with the bearer private was refused:\n%s", profile, strings.Join(got, "\n"))
	}
	o.withoutUnselectedProfileDaemons(cfg, selected, []loopholes.JailDaemonSpec{{Name: "aws-auth"}})
	o.noteUnstartedProfileDaemons()
	return o, channel, &stderr
}

// G1 and G2 for -p bedrock-key: the key reaches claude, the SSO pointer does not, the launch is not
// refused, and the aws-auth daemon is not started — the line naming the selection that WOULD start
// it offers bedrock, never bedrock-key. No line prints the key.
func TestABedrockKeyLaunchGetsTheKeyAndNoSSOPointer(t *testing.T) {
	_, channel, stderr := bedrockKeyChannel(t, "bedrock-key")
	env := channel.launchEnv("claude")
	if v, ok := env.Get(bedrockBearer); !ok || v != bearerProbe {
		t.Errorf("claude on bedrock-key lacks %s", bedrockBearer)
	}
	if _, ok := env.Get(pointerVar); ok {
		t.Errorf("claude on bedrock-key received the aws-auth pointer %s", pointerVar)
	}
	out := stderr.String()
	if !strings.Contains(out, "Not started: the aws-auth jail daemon") ||
		!strings.Contains(out, "select one (`-p <agent>=bedrock`)") {
		t.Errorf("the launch must say aws-auth is not started and offer -p bedrock:\n%s", out)
	}
	if strings.Contains(out, bearerProbe) {
		t.Errorf("a launch line printed the key:\n%s", out)
	}
}

// OQ-BK4: -p bedrock is SSO and only SSO. The private bearer reaches no process; the pointer
// reaches claude, and the daemon starts.
func TestABedrockLaunchGetsThePointerAndNoBearer(t *testing.T) {
	_, channel, stderr := bedrockKeyChannel(t, "bedrock")
	env := channel.launchEnv("claude")
	if _, ok := env.Get(bedrockBearer); ok {
		t.Errorf("claude on bedrock received %s; bedrock no longer claims it", bedrockBearer)
	}
	if _, ok := env.Get(pointerVar); !ok {
		t.Errorf("claude on bedrock lost the aws-auth pointer %s", pointerVar)
	}
	if strings.Contains(stderr.String(), "Not started: the aws-auth jail daemon") {
		t.Errorf("-p bedrock must start aws-auth:\n%s", stderr.String())
	}
}

// OQ-BK5 through Run: one launch mixing bedrock and bedrock-key is refused above the backend, the
// remedy names a launch on either alone and not the empty-profile step, and no value is printed.
func TestAMixedBedrockLaunchIsRefusedNamingALaunchOnEither(t *testing.T) {
	ws := t.TempDir()
	reached, rc, out := secretEnvRefusalRun(t, ws, "packs = [\"claude\", \"codex\", \"bedrock\"]\n\n"+
		"[active_provider]\nclaude = \"bedrock-key\"\ncodex = \"bedrock\"\n\n"+
		"[providers.bedrock]\nregion = \"us-east-1\"\n\n[providers.bedrock-key]\nregion = \"us-east-1\"\n\n"+
		"[allow.aws-auth]\nprofile = \"sso-bedrock\"\nunnarrowed = true\n\n"+
		"[env.secrets]\nset = { AWS_BEARER_TOKEN_BEDROCK = \""+bearerProbe+"\" }\n")
	if rc == 0 || reached {
		t.Fatalf("a mixed bedrock/bedrock-key launch must refuse: rc = %d, reached = %v\n%s", rc, reached, out)
	}
	for _, want := range []string{"Refusing to launch", "One launch cannot mix them",
		"`yolo -p bedrock-key` or `yolo -p bedrock`"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal must say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `profile = ""`) {
		t.Errorf("a mixed launch must not be told to empty the SSO profile:\n%s", out)
	}
	if strings.Contains(out, bearerProbe) {
		t.Errorf("the refusal printed the key:\n%s", out)
	}
	requireNoUserEnvFile(t, ws)
}
