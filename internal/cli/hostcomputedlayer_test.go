package cli

// hostcomputedlayer_test.go pins OQ-HC1 at the CLI's front doors (docs/reference/host-agent-environment.md):
// `yolo host apply --assert`, the automatic apply a wrapped launch runs (`host_apply_on_launch`),
// and `yolo config render|ls --at host` all compose the host's derive inputs (composeHostInputs)
// and hand them to the render, so your MCP servers, LSP servers, providers and selected model
// reach host agents' files as they reach a jail's. Each test goes through the real command, in a
// temp HOME with YOLO_CTX_ROOT pointed at an empty dir, so deleting the composition from
// applyHostSurveyed or configRenderHost fails it.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostComputedHome is a temp HOME whose user config is cfg, with the environment neutralized:
// not in a jail, no staged host layers, an empty ctx root, the declared binaries stubbed.
func hostComputedHome(t *testing.T, cfg string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USE_PROFILES", "")
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	t.Chdir(t.TempDir())
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg)
	stubDeclaredBins(t)
	return home
}

// readJSONAt decodes home/rel.
func readJSONAt(t *testing.T, home, rel string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, rel))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v\n%s", rel, err, raw)
	}
	return m
}

// THE REAL `yolo host apply --assert`, end to end: pi with a user MCP server, an LSP server and
// `profile` selecting the codex profile. Every derived pi surface gets its computed layer,
// and the inputs the host does not carry — a preset, an entry naming a jail path — are named.
func TestYoloHostApplyAssertWritesTheComputedLayer(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["pi"],
		"mcp_servers":{"tavily":{"command":"npx","args":["-y","tavily-mcp"]},
		               "jailed":{"command":"/workspace/bin/mcp"}},
		"mcp_presets":["sequential-thinking"],
		"profile":{"pi":"codex"}}`)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()

	models, _ := readJSONAt(t, home, ".pi/agent/yolo-openai-codex-models.json")["models"].([]any)
	if len(models) == 0 {
		t.Errorf("pi/codex-models holds no openai-codex list at the host")
	}
	servers, _ := readJSONAt(t, home, ".pi/agent/mcp-adapter.json")["mcpServers"].(map[string]any)
	if _, ok := servers["tavily"]; !ok {
		t.Errorf("your mcp_servers entry did not reach pi/mcp: %v", servers)
	}
	for _, absent := range []string{"jailed", "sequential-thinking"} {
		if _, ok := servers[absent]; ok {
			t.Errorf("%s reached a real home: %v", absent, servers)
		}
	}
	if !strings.Contains(report, "mcp_servers jailed is not written at the host") ||
		!strings.Contains(report, "mcp_presets sequential-thinking is not written at the host") {
		t.Errorf("the omitted inputs are not named in the report:\n%s", report)
	}
	settings := readJSONAt(t, home, ".pi/agent/settings.json")
	if settings["defaultProvider"] != "openai-codex" || settings["defaultModel"] == nil {
		t.Errorf("profile pi=codex did not select pi's provider and model: %v", settings)
	}
}

// THE WRAPPER'S AUTOMATIC APPLY — through its real call site, `yolo host -- <program>` with
// `host_apply_on_launch` — renders the computed layer too (OQ-HC1: "and the auto one in a
// wrapper"). A settled home gains an MCP server in the user config; the next wrapped launch
// writes it into claude's file, and a second launch finds nothing to do.
func TestTheWrapperAutoApplyWritesTheComputedLayer(t *testing.T) {
	home := gateFixture(t, true)
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"],"host_apply_on_launch":true,
		  "mcp_servers":{"tavily":{"command":"npx","args":["-y","tavily-mcp"]}}}`)

	var out, errw bytes.Buffer
	rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil)
	report := out.String() + errw.String()
	if rc != 127 {
		t.Fatalf("rc = %d, want 127 (the launch proceeds past the gate to the PATH lookup)\n%s", rc, report)
	}
	servers, _ := readJSONAt(t, home, ".claude.json")["mcpServers"].(map[string]any)
	if _, ok := servers["tavily"]; !ok {
		t.Fatalf("the wrapper's automatic apply did not write your mcp_servers entry into "+
			"~/.claude.json: %v\n%s", servers, report)
	}
	// Settled: the observe pass the gate runs derives from the same inputs, so it does not
	// report the rows it just wrote as drift.
	out.Reset()
	errw.Reset()
	if rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil); rc != 127 ||
		strings.Contains(out.String()+errw.String(), "synchronized") {
		t.Errorf("a second wrapped launch re-applied a settled home (rc=%d):\n%s%s", rc,
			out.String(), errw.String())
	}
}

// `yolo config render --at host` shows the computed layer's content — the bytes host apply
// would write, derive included — and `yolo config ls --at host` lists the layer.
func TestConfigRenderAndLsAtHostShowTheComputedLayer(t *testing.T) {
	hostComputedHome(t, `{"packs":["pi"],"mcp_servers":{"tavily":{"command":"npx"}}}`)
	rc, out, errs := runConfigVerb(t, "render", "pi/mcp", "--at", "host")
	if rc != 0 {
		t.Fatalf("config render --at host rc=%d\n%s%s", rc, out, errs)
	}
	if !strings.Contains(previewBody(t, out), `"tavily"`) {
		t.Errorf("the host preview of pi/mcp does not show your server:\n%s", out)
	}
	rc, out, errs = runConfigVerb(t, "render", "pi/codex-models", "--at", "host")
	if rc != 0 || !strings.Contains(out, `"models"`) {
		t.Errorf("the host preview of pi/codex-models shows no list (rc=%d):\n%s%s", rc, out, errs)
	}
	rc, out, errs = runConfigVerb(t, "ls", "--at", "host", "--all")
	if rc != 0 {
		t.Fatalf("config ls --at host rc=%d\n%s%s", rc, out, errs)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "pi/codex-models ") && !strings.Contains(line, "computed") {
			t.Errorf("config ls --at host does not list pi/codex-models' computed layer: %q", line)
		}
	}
}

// The host render's jail-path check reads the install prefix from internal/paths, which cannot
// import the launcher's own constant: the two spellings must stay one path.
func TestTheJailPrefixHasOneSpelling(t *testing.T) {
	if paths.JailPrefixDir != run.JailPrefixDir {
		t.Errorf("paths.JailPrefixDir = %q, run.JailPrefixDir = %q", paths.JailPrefixDir, run.JailPrefixDir)
	}
}
