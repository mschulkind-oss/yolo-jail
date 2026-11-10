package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestConfigWhyRedactsMCPHeadersAtEveryQueryDepth(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "")
	secret := "SYNTHETIC_MCP_SECRET_DO_NOT_PRINT"
	config := `[mcp_servers.linear]
url = "https://mcp.linear.app/mcp"
headers = { Authorization = "` + secret + `", x-custom = "SYNTHETIC_SECOND_HEADER" }
`
	if err := os.WriteFile(fx.userConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mcp_servers", "mcp_servers.linear", "mcp_servers.linear.headers", "mcp_servers.linear.headers.Authorization", "mcp_servers.linear.headers.x-custom"} {
		rc, out, errs := captureStd(t, func() int {
			return registry["config"]([]string{"config", "why", key})
		})
		output := out + "\n" + errs
		if rc != 0 || strings.Contains(output, secret) || strings.Contains(output, "SYNTHETIC_SECOND_HEADER") {
			t.Errorf("config why %s rc=%d leaked a header: output=%q", key, rc, output)
		}
		if key == "mcp_servers.linear.headers" && !strings.Contains(output, "Authorization") {
			t.Errorf("header-map query lost header keys: %q", output)
		}
		if !strings.Contains(output, "<redacted>") {
			t.Errorf("config why %s omitted redaction marker: %q", key, output)
		}
	}
}

func TestConfigWhyNamesPackThatSuppliesUserScopedMCPServer(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "")
	if err := os.WriteFile(fx.userConfig, []byte("packs = [\"chrome-devtools\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "why", "mcp_servers.chrome-devtools"})
	})
	if rc != 0 || !strings.Contains(out, "scope: user — selected pack chrome-devtools") {
		t.Fatalf("config why rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
}

func TestConfigWhyDoesNotInferJailMCPProvenanceFromMergedOrMutableSnapshots(t *testing.T) {
	fx := newNamedVerbFixture(t, "[mcp_servers.workspace-only]\nurl = \"https://workspace.example/mcp\"\nheaders = { Authorization = \"Bearer workspace-sentinel\" }\n", "")
	root := t.TempDir()
	var packs []*packload.Pack
	for _, pair := range [][2]string{{"selected-pack", "selected-server"}, {"launch-only", "launch-only-server"}, {"local", "local-server"}} {
		packDir := filepath.Join(root, pair[0])
		if err := os.MkdirAll(packDir, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := `{"name":"` + pair[0] + `","contributes":[{"kind":"mcp","name":"` + pair[1] + `","config":{"url":"https://` + pair[1] + `.example/mcp"}}]}`
		if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		pack, problems := packload.LoadDir(packDir, pair[0])
		if pack == nil || len(problems) > 0 {
			t.Fatalf("load fixture pack: pack=%v problems=%v", pack, problems)
		}
		packs = append(packs, pack)
	}
	if err := packload.WritePackTreeRecord(root, packs); err != nil {
		t.Fatal(err)
	}
	// In a jail, the config file at this path is the generated merged preflight. It
	// includes the workspace-only MCP entry and cannot prove user-only provenance.
	if err := os.WriteFile(fx.userConfig, []byte(`packs = ["selected-pack"]
[mcp_servers.workspace-only]
url = "https://workspace.example/mcp"
headers = { Authorization = "Bearer workspace-sentinel" }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_VERSION", "test")
	t.Setenv("YOLO_WORKSPACE", fx.workspace)
	t.Setenv("YOLO_SETUP", "podman-self")
	t.Setenv("YOLO_PACK_ROOT", root)

	for _, tc := range []struct {
		name, snapshot, server, source string
	}{
		{"first overlapping launch snapshot", `{"packs":["selected-pack"]}`, "selected-server", "selected-pack"},
		{"later overlapping launch snapshot", `{"packs":["launch-only"]}`, "selected-server", "selected-pack"},
		{"later snapshot and launch-only pack", `{"packs":["selected-pack"]}`, "launch-only-server", "launch-only"},
		{"implicit local pack absent from literal list", `{"packs":[]}`, "local-server", "local"},
		{"missing launch snapshot", "", "selected-server", "selected-pack"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.snapshot == "" {
				if err := os.Remove(config.WorkspaceAssembledConfigPath(fx.workspace, "podman-self")); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			} else {
				snapshot, err := jsonx.Decode([]byte(tc.snapshot))
				if err != nil {
					t.Fatal(err)
				}
				if err := config.WriteAssembledConfig(fx.workspace, "podman-self", snapshot.(*jsonx.OrderedMap)); err != nil {
					t.Fatal(err)
				}
			}
			rc, out, errs := captureStd(t, func() int {
				return registry["config"]([]string{"config", "why", "mcp_servers." + tc.server})
			})
			if rc != 0 || !strings.Contains(out, "scope: unavailable") || !strings.Contains(out, "pack source is "+tc.source) {
				t.Fatalf("config why %s rc=%d stdout=%q stderr=%q", tc.server, rc, out, errs)
			}
			if strings.Contains(out, "scope: user") || strings.Contains(out, "scope: workspace") {
				t.Fatalf("mutable config snapshot inferred MCP scope: %q", out)
			}
		})
	}

	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "why", "mcp_servers.workspace-only"})
	})
	if rc != 0 || !strings.Contains(out, "generated merged preflight config") ||
		strings.Contains(out, "Bearer workspace-sentinel") || !strings.Contains(out, "<redacted>") {
		t.Fatalf("workspace-only server provenance/redaction rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
}

func TestConfigWhyTreatsImplicitUserLocalPackAsUserScopedOnHost(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "")
	if err := os.WriteFile(fx.userConfig, []byte("packs = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	localDir := paths.LocalPackDir()
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"local","contributes":[{"kind":"mcp","name":"local-server","config":{"url":"https://local.example/mcp"}}]}`
	if err := os.WriteFile(filepath.Join(localDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "why", "mcp_servers.local-server"})
	})
	if rc != 0 || !strings.Contains(out, "scope: user — selected pack local supplies this server") ||
		strings.Contains(out, "scope: workspace") {
		t.Fatalf("implicit local pack scope rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
}

func TestConfigWhyNamesFileLineAndDefault(t *testing.T) {
	fx := newNamedVerbFixture(t, "[machine.resources]\ncpus = 4\n",
		"# overrides\n\n[active_provider]\ndefault = \"native\"\nclaude = \"native\"\n")

	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "why", "active_provider.claude"})
	})
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	for _, want := range []string{
		`active_provider.claude = "native"`,
		"set in  ~/.config/yolo-jail/config.d/work.toml:5",
		`default would be: active_provider.default, "native", set in ~/.config/yolo-jail/config.d/work.toml:4`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config why output lacks %q:\n%s", want, out)
		}
	}

	rc, out, errs = captureStd(t, func() int {
		return registry["config"]([]string{"config", "why", "machine.resources.cpus"})
	})
	if rc != 0 || !strings.Contains(out, "machine.resources.cpus = 4") || !strings.Contains(out, "set in  "+fx.project+":2") {
		t.Fatalf("project key rc=%d stdout=%q stderr=%q", rc, out, errs)
	}

	rc, out, errs = captureStd(t, func() int {
		return registry["config"]([]string{"config", "why", "machine.resources.memory"})
	})
	if rc != 0 || !strings.Contains(out, "is not set") || !strings.Contains(out, "yolo config set machine.resources.memory") {
		t.Fatalf("unset key rc=%d stdout=%q stderr=%q", rc, out, errs)
	}

	rc, _, errs = captureStd(t, func() int { return registry["config"]([]string{"config", "why"}) })
	if rc != 2 || !strings.Contains(errs, "usage: yolo config why") {
		t.Fatalf("bare why rc=%d stderr=%q", rc, errs)
	}
}

// A renamed legacy key is never offered to `yolo config set`, which refuses it: `config why`
// names the key that replaced it instead.
func TestConfigWhyNamesRemoteMCPServerScope(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "")
	if err := os.WriteFile(fx.userConfig, []byte("packs = [\"git\", \"github\"]\n[mcp_servers.linear]\nurl = \"https://mcp.linear.app/mcp\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspaceConfig := filepath.Join(fx.workspace, "yolo-jail.toml")
	if err := os.WriteFile(workspaceConfig, []byte("[mcp_servers.linear]\nurl = \"https://staging.linear.app/mcp\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "why", "mcp_servers.linear"})
	})
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	want := "scope: workspace — yolo-jail.toml changes its url, so it signs in separately from your user-level linear"
	if !strings.Contains(out, want) {
		t.Fatalf("config why omitted MCP scope %q:\n%s", want, out)
	}
}

func TestConfigWhyReportsUnknownLaunchPackScope(t *testing.T) {
	newNamedVerbFixture(t, "", "")
	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "why", "mcp_servers.launch-added"})
	})
	if rc != 0 || !strings.Contains(out, "scope: unavailable") || !strings.Contains(out, "inside the running jail") {
		t.Fatalf("standalone why rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
}

func TestConfigWhyNamesTheReplacementForARenamedKey(t *testing.T) {
	newNamedVerbFixture(t, "", "")
	for key, want := range map[string]string{
		"packages":       "packages was renamed, so use install.nix instead",
		"resources.cpus": "resources.cpus was renamed, so use machine.resources.cpus instead",
	} {
		rc, out, errs := captureStd(t, func() int { return registry["config"]([]string{"config", "why", key}) })
		if rc != 0 || !strings.Contains(out, want) || strings.Contains(out, "yolo config set "+key) {
			t.Fatalf("config why %s rc=%d stdout=%q stderr=%q; want %q", key, rc, out, errs, want)
		}
	}
}

// A private inline value is printed by neither `config why` at any depth nor `config dump`
// (review major 2c); the name still is.
func TestConfigWhyAndDumpNeverPrintAPrivateInlineValue(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "")
	secret := "SYNTHETIC_PRIVATE_DSN_DO_NOT_PRINT"
	body := "[env.secrets]\nset = { SENTRY_DSN = \"" + secret + "\" }\n"
	if err := os.WriteFile(fx.userConfig, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"config", "why", "env"}, {"config", "why", "env.secrets"},
		{"config", "why", "env.secrets.set"}, {"config", "why", "env.secrets.set.SENTRY_DSN"}, {"config", "dump"}} {
		rc, out, errs := captureStd(t, func() int { return registry["config"](args) })
		if rc != 0 || strings.Contains(out+errs, secret) {
			t.Errorf("%v rc=%d leaked a private value: %q", args, rc, out+errs)
		}
		if !strings.Contains(out, "SENTRY_DSN") && args[len(args)-1] != "SENTRY_DSN" && !strings.HasSuffix(args[len(args)-1], "SENTRY_DSN") {
			t.Errorf("%v lost the private name: %q", args, out)
		}
	}
}
