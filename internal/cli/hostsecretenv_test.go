package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// An unanchored relative private file is dropped at `yolo host` as a public one is: the only
// resolution left for it is the current directory, which a workspace controls.
func TestHostSecretEnvDropsARelativePrivateFile(t *testing.T) {
	secrets := jsonx.NewOrderedMap()
	secrets.Set("files", []any{"keys.env", "/abs/keys.env"})
	env := jsonx.NewOrderedMap()
	env.Set("secrets", secrets)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("env", env)
	var warned []string
	got := hostScopedEnvSources(cfg, func(m string) { warned = append(warned, m) })
	gotEnv, _ := getMapValue(got, "env")
	gotSecrets, _ := getMapValue(gotEnv, "secrets")
	files, _ := getList(gotSecrets, "files")
	if len(files) != 1 || files[0] != "/abs/keys.env" {
		t.Errorf("env.secrets.files keeps the absolute entry only, got %v", files)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], `env.secrets.files: "keys.env" is relative`) {
		t.Errorf("the dropped entry is named: %v", warned)
	}
	if orig, _ := getList(secrets, "files"); len(orig) != 2 {
		t.Errorf("the caller's config is not edited: %v", orig)
	}
}

// hostSecretEnvConfig is a user config with a claimed private key (zai's), an unclaimed private
// name, and one public value: docs/reference/providers.md#the-two-env-sets-public-and-private rows 1, 3 and 4 at `yolo host`.
const hostSecretEnvConfig = "packs = [\"zai\"]\nhost_management = \"own\"\nhost_apply_on_launch = false\n\n" +
	"[env]\nset = { GH_TOKEN = \"g\" }\n\n" +
	"[env.secrets]\nset = { ZAI_API_KEY = \"z-probe\", STRIPE_KEY = \"s-probe\" }\n"

// hostSecretEnvRun launches `yolo host -- bash` over hostSecretEnvConfig, written as the user's
// TOML config, with shell exported in the invoking shell.
func hostSecretEnvRun(t *testing.T, shell map[string]string) (map[string]string, string) {
	t.Helper()
	rc, env, errs := hostGateRunIn(t, `{}`, shell, nil, "bash", func(home string) {
		dir := filepath.Join(home, ".config", "yolo-jail")
		if err := os.Remove(filepath.Join(dir, "config.jsonc")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(hostSecretEnvConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if rc != 0 || env == nil {
		t.Fatalf("yolo host -- bash: rc = %d, reached exec = %v\nstderr:\n%s", rc, env != nil, errs)
	}
	return env, errs
}

// A command no provider selection reaches receives the public set alone; the unclaimed private
// name is named with the user-scope remedy and never its value.
func TestHostSecretEnvKeepsThePrivateSetFromAnUnclaimingCommand(t *testing.T) {
	t.Setenv("STRIPE_KEY", "")
	if err := os.Unsetenv("STRIPE_KEY"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_TOKEN", "")
	if err := os.Unsetenv("GH_TOKEN"); err != nil {
		t.Fatal(err)
	}
	env, errs := hostSecretEnvRun(t, nil)
	if env["GH_TOKEN"] != "g" {
		t.Errorf("the public value reaches the command: GH_TOKEN=%q", env["GH_TOKEN"])
	}
	for _, k := range []string{"ZAI_API_KEY", "STRIPE_KEY"} {
		if v, ok := env[k]; ok && v != "" {
			t.Errorf("%s is private and this command claims it nowhere, got %q", k, v)
		}
	}
	if !strings.Contains(errs, "STRIPE_KEY: withheld from every process — nothing in this launch claims it.") ||
		!strings.Contains(errs, "yolo config unsecret STRIPE_KEY --user") {
		t.Errorf("row 4's line, with the user-scope remedy:\n%s", errs)
	}
	if strings.Contains(errs, "s-probe") || strings.Contains(errs, "z-probe") {
		t.Errorf("a line never carries a value:\n%s", errs)
	}
}

// A private name the invoking shell exports too: the command holds the shell's own value, and the
// line says yolo did not add it, still naming the remedy (docs/reference/providers.md#the-two-env-sets-public-and-private).
func TestHostSecretEnvSaysAShellValuePassesThrough(t *testing.T) {
	env, errs := hostSecretEnvRun(t, map[string]string{"STRIPE_KEY": "shell"})
	if env["STRIPE_KEY"] != "shell" {
		t.Errorf("the invoking shell's own value passes through: %q", env["STRIPE_KEY"])
	}
	if !strings.Contains(errs, "STRIPE_KEY: not added by yolo") ||
		!strings.Contains(errs, "yolo config unsecret STRIPE_KEY --user") {
		t.Errorf("the inherited wording, with the user-scope remedy:\n%s", errs)
	}
}

// hostPublicKeyConfig holds zai's key in the PUBLIC set: docs/reference/providers.md#the-two-env-sets-public-and-private row 2.
const hostPublicKeyConfig = "packs = [\"zai\"]\n\n[env]\nset = { ZAI_API_KEY = \"z-probe\" }\n"

// Row 2 at the host notch: `yolo host --` and `yolo host env` refuse a selected provider's key in
// the public set before anything is exec'd or printed for a shell to eval (SE-D7), in the jail's
// words, and never print the value.
func TestHostRefusesAPublicProviderKey(t *testing.T) {
	const move = "Move it to the private set: yolo config secret ZAI_API_KEY --user"
	rc, env, errs := hostGateRun(t, hostPublicKeyConfig, nil, nil, "bash")
	if rc == 0 || env != nil || !strings.Contains(errs, move) ||
		!strings.Contains(errs, "config.env.set: ZAI_API_KEY is set in your public environment ([env] set)") {
		t.Errorf("yolo host -- bash must refuse before the exec, naming the move: rc = %d, reached = %v\n%s", rc, env != nil, errs)
	}
	if strings.Contains(errs, "z-probe") {
		t.Errorf("the refusal printed the value:\n%s", errs)
	}
	c := composeHostLaunch("bash", "", nil, func(string) {})
	if c.err == nil || !strings.Contains(c.err.Error(), move) {
		t.Errorf("the composition carries the refusal: %v", c.err)
	}
	var out, errw bytes.Buffer
	if rc := hostEnv([]string{"--agent", "bash"}, &out, &errw); rc == 0 || out.Len() != 0 {
		t.Errorf("yolo host env must exit non-zero with nothing for a shell to eval: rc = %d\nstdout:\n%s", rc, out.String())
	}
	if !strings.Contains(errw.String(), move) || strings.Contains(errw.String()+out.String(), "z-probe") {
		t.Errorf("yolo host env names the move and never the value:\n%s", errw.String())
	}
}

// SE-D2 at the host notch: a name in both sets refuses, naming both --drop commands.
func TestHostRefusesANameInBothSets(t *testing.T) {
	rc, env, errs := hostGateRun(t, "packs = []\n\n[env]\nset = { FOO = \"a-probe\" }\n\n"+
		"[env.secrets]\nset = { FOO = \"b-probe\" }\n", nil, nil, "bash")
	if rc == 0 || env != nil || !strings.Contains(errs, "yolo config secret FOO --drop --user") ||
		!strings.Contains(errs, "yolo config unsecret FOO --drop --user") || strings.Contains(errs, "-probe") {
		t.Errorf("yolo host -- bash must refuse a name in both sets: rc = %d, reached = %v\n%s", rc, env != nil, errs)
	}
}

// The host notch hands the gate the MCP servers' claims: a private name only a server's
// requires_env claims reaches the agent that renders the server, and nobody else (SE-D13).
func TestHostLaunchPassesTheMCPClaimsToTheGate(t *testing.T) {
	// The invoking environment may hold a real key of this name; the launch must not inherit it.
	t.Setenv("TAVILY_API_KEY", "")
	if err := os.Unsetenv("TAVILY_API_KEY"); err != nil {
		t.Fatal(err)
	}
	cfg := "packs = [\"claude\"]\n\n[env.secrets]\nset = { TAVILY_API_KEY = \"t-probe\" }\n\n" +
		"[mcp_servers.tavily]\ncommand = \"tavily-mcp\"\nrequires_env = [\"TAVILY_API_KEY\"]\n"
	if env, errs := hostGateLaunchWith(t, cfg, nil, nil, "claude"); env["TAVILY_API_KEY"] != "t-probe" {
		t.Errorf("claude renders the MCP table, so the server's claim delivers the key: %q\n%s", env["TAVILY_API_KEY"], errs)
	}
	if env, errs := hostGateLaunchWith(t, cfg, nil, nil, "bash"); env["TAVILY_API_KEY"] != "" {
		t.Errorf("a bare command never receives a private value\n%s", errs)
	}
}

// The host notch tells the gate what a shipped provider this launch does not select claims, so
// row 1's line names the remedy.
func TestHostLaunchPassesTheUnselectedClaimsToTheGate(t *testing.T) {
	cfg := "packs = [\"claude\"]\n\n[env]\nset = { ZAI_API_KEY = \"z-probe\" }\n"
	_, errs := hostGateLaunchWith(t, cfg, nil, nil, "claude")
	if !strings.Contains(errs, "the zai provider, which this launch does not select, claims that name") {
		t.Errorf("row 1's unselected-provider line is missing:\n%s", errs)
	}
}
