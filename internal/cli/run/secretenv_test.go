package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// secret-env-scoping.md §3.4 rows 1, 3 and 4 at the jail notch: the shared file holds the public
// set only, the claiming agent receives its private key, and a private name nothing claims is
// named, never its value.
func TestTheSharedFileCarriesNoPrivateValue(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.ProfileName = "zai"
	h := config.NewHydratedEnv()
	h.SetPublic("GH_TOKEN", "gh-test", config.EnvPlace{Key: "env.set"})
	h.SetPrivate("ZAI_API_KEY", "tok-zai", config.EnvPlace{Private: true, Key: "env.secrets.set"})
	h.SetPrivate("STRIPE_KEY", "tok-stripe", config.EnvPlace{Private: true, Key: "env.secrets.set"})
	channel, err := o.composePackChannel(bareConfig(), setPacks(t), h)
	if err != nil {
		t.Fatal(err)
	}
	shared := string(userEnvFileBody(channel.sharedEnvSourceWinners(), channel))
	// The YOLO_PROVIDERS table names ZAI_API_KEY as zai's api_key_env_name, so the check is the export.
	if !strings.Contains(shared, "export GH_TOKEN=") || strings.Contains(shared, "export ZAI_API_KEY=") ||
		strings.Contains(shared, "STRIPE_KEY") || strings.Contains(shared, "tok-") {
		t.Errorf("yolo-user-env.sh must hold the public set only:\n%s", shared)
	}
	if _, ok := channel.scope.EnvSourcesFor("claude").Get("ZAI_API_KEY"); !ok {
		t.Error("claude on zai receives the private, claimed key")
	}
	o.noteCredentialScope(channel)
	if out := stderr.String(); !strings.Contains(out, "STRIPE_KEY: withheld from every process — nothing in this launch claims it.") ||
		strings.Contains(out, "tok-") {
		t.Errorf("row 4's line, names only:\n%s", out)
	}
}
