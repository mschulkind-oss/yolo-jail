package run

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// docs/reference/providers.md#the-two-env-sets-public-and-private rows 1, 3 and 4 at the jail notch: the shared file holds the public
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

// secretEnvRefusalRun drives Run on macos-user over a TOML user config, returning whether the
// backend was reached, the exit code and the streams. The backend is a stub, so the launch either
// refuses above the dispatch or hands it a launch env.
func secretEnvRefusalRun(t *testing.T, ws, userTOML string) (bool, int, string) {
	t.Helper()
	// On the host: inside a jail the remedy is the host-run line (config.remedyCommand), which
	// the in-jail form of these tests would read instead of the command a host user types.
	t.Setenv("YOLO_VERSION", "")
	if err := os.Unsetenv("YOLO_VERSION"); err != nil {
		t.Fatal(err)
	}
	home := packHome(t)
	writeUserConfigTOML(t, home, userTOML)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = []string{"bash"}
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		reached = true
		return 0
	}
	rc := Run(*o)
	return reached, rc, stdout.String() + stderr.String()
}

// requireNoUserEnvFile fails when any yolo-user-env.sh exists under the workspace: the refusal
// comes before any vehicle is written (docs/reference/providers.md#the-two-env-sets-public-and-private SE-D7).
func requireNoUserEnvFile(t *testing.T, ws string) {
	t.Helper()
	_ = filepath.WalkDir(ws, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Name() == "yolo-user-env.sh" {
			t.Errorf("a refused launch wrote %s", p)
		}
		return nil
	})
}

// docs/reference/providers.md#the-two-env-sets-public-and-private (row 2) at the jail notch: a selected provider's key in the public
// set refuses the launch above the backend dispatch, naming the move and never the value, and
// nothing is written first (SE-D7). No hatch (SE-D10).
func TestAPublicProviderKeyRefusesBeforeAnythingIsWritten(t *testing.T) {
	ws := t.TempDir()
	reached, rc, out := secretEnvRefusalRun(t, ws, "packs = [\"claude\", \"zai\"]\n\n"+
		"[active_provider]\nclaude = \"zai\"\n\n[env]\nset = { ZAI_API_KEY = \"zai-probe\" }\n")
	if rc == 0 || reached {
		t.Fatalf("a public provider key must refuse before the backend: rc = %d, reached = %v\n%s", rc, reached, out)
	}
	for _, want := range []string{"Refusing to launch: ",
		"config.env.set: ZAI_API_KEY is set in your public environment ([env] set), and the zai provider this launch selects claims it as its key.",
		"Move it to the private set: yolo config secret ZAI_API_KEY --user"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal must say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "zai-probe") {
		t.Errorf("the refusal printed the value:\n%s", out)
	}
	requireNoUserEnvFile(t, ws)
}

// SE-D2 at the jail notch: one name in both sets refuses whatever claims it, naming both --drop
// commands; nothing claims FOO here.
func TestANameInBothSetsRefusesBeforeAnythingIsWritten(t *testing.T) {
	ws := t.TempDir()
	reached, rc, out := secretEnvRefusalRun(t, ws, "packs = []\n\n[env]\nset = { FOO = \"a-probe\" }\n\n"+
		"[env.secrets]\nset = { FOO = \"b-probe\" }\n")
	if rc == 0 || reached {
		t.Fatalf("a name in both sets must refuse before the backend: rc = %d, reached = %v\n%s", rc, reached, out)
	}
	for _, want := range []string{"Refusing to launch: ",
		"config.env: FOO is set in both your public environment ([env] set) and your private one ([env.secrets] set).",
		"yolo config secret FOO --drop --user", "yolo config unsecret FOO --drop --user"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal must say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "-probe") {
		t.Errorf("the refusal printed a value:\n%s", out)
	}
	requireNoUserEnvFile(t, ws)
}
