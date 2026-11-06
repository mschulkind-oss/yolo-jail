package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// launchedJailEnv is the environment a launched jail's boot reads: argv's -e pairs, with the
// channel section of the yolo-user-env.sh the launch delivered into ws's home over them, as
// hydrate_user_env lays a plain-form line over the container's environment.
func launchedJailEnv(t *testing.T, argv []string, ws string) map[string]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(runtimeHome(ws, "podman"), "yolo-user-env.sh"))
	if err != nil {
		t.Fatalf("the launch delivered no channel file: %v", err)
	}
	return overChannel(t, envPairs(argv), body)
}

// assembledJailEnv is launchedJailEnv for an argv assembled by hand from in: the channel in
// composes, delivered by its writer (deliveredFiles).
func assembledJailEnv(t *testing.T, o *Options, in *assembleInput, argv []string) map[string]string {
	t.Helper()
	shared, _ := deliveredFiles(t, in.envChannel(o))
	return overChannel(t, envPairs(argv), []byte(shared))
}

func overChannel(t *testing.T, vars map[string]string, body []byte) map[string]string {
	t.Helper()
	channel, ok := entrypoint.ParseEntryChannel(body)
	if !ok {
		t.Fatalf("the channel file has no complete channel section:\n%s", body)
	}
	for k, v := range channel {
		vars[k] = v
	}
	return vars
}

// sessionInputsConfig declares one of each session input (live-config-apply.md LA-S4a): a blocked
// tool, a mise tool, an LSP server and an MCP server.
func sessionInputsConfig() *jsonx.OrderedMap {
	cfg, err := jsonx.Decode([]byte(`{
		"agents": ["claude"],
		"security": {"blocked_tools": [{"name": "curl", "message": "sessioninputs-blocked"}]},
		"mise_tools": {"jq": "1.8.0"},
		"lsp_servers": {"sil": {"command": "sessioninputs-lsp", "args": [], "fileExtensions": {".sil": "sil"}}},
		"mcp_servers": {"sim": {"command": "sessioninputs-mcp", "args": []}}
	}`))
	if err != nil {
		panic(err)
	}
	return cfg.(*jsonx.OrderedMap)
}

// THE SESSION INPUTS RIDE THE PER-ENTRY CHANNEL, NOT THE ARGV (LA-S4a): every session's boot
// hydrates the channel before the steps that read them, and an attach rewrites it, so a change
// reaches the next session of a running jail. A frozen -e copy would be a second source the
// rewrite cannot reach, and it would put an MCP server's literal env in `podman inspect`.
func TestTheSessionInputsRideTheChannelNotTheArgv(t *testing.T) {
	a := assembleWithConfigAssembled(t, sessionInputsConfig())
	for _, name := range sessionInputNames {
		if v, ok := envValue(a.argv, name); ok {
			t.Errorf("the launch argv still carries %s=%s; the channel is its one crossing", name, v)
		}
	}
	got := map[string]string{}
	for _, kv := range a.channelEnv(t, sessionInputNames...) {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for name, want := range map[string]string{
		"YOLO_BLOCK_CONFIG":  "sessioninputs-blocked",
		"YOLO_MISE_TOOLS":    `"jq": "1.8.0"`,
		"MISE_DISABLE_TOOLS": "pnpm",
		"YOLO_LSP_SERVERS":   "sessioninputs-lsp",
		"YOLO_MCP_SERVERS":   "sessioninputs-mcp",
	} {
		if !strings.Contains(got[name], want) {
			t.Errorf("the channel's %s=%q, want it to carry %q", name, got[name], want)
		}
	}
}

// THE SEAL OVER THE CHANNEL: a sealed build's channel carries the blocked tools (a build is still
// confined by them) and no MCP or LSP server or mise tool of the user's (agentServerTables,
// config.JailMiseTools).
func TestASealedChannelCarriesOnlyTheBlockedTools(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	o.Sealed = true
	channel, err := o.launchChannel(sessionInputsConfig(), claudePackFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, in := range channel.sessionInputs {
		got[in.name] = in.value
	}
	if !strings.Contains(got["YOLO_BLOCK_CONFIG"], "sessioninputs-blocked") {
		t.Errorf("a sealed channel's YOLO_BLOCK_CONFIG=%q lost the blocked tool", got["YOLO_BLOCK_CONFIG"])
	}
	for name, want := range map[string]string{"YOLO_MISE_TOOLS": "{}", "YOLO_LSP_SERVERS": "{}", "YOLO_MCP_SERVERS": "{}"} {
		if got[name] != want {
			t.Errorf("a sealed channel's %s=%q, want %s", name, got[name], want)
		}
	}
	shared, _ := deliveredFiles(t, channel)
	for _, secret := range []string{"sessioninputs-lsp", "sessioninputs-mcp", "1.8.0"} {
		if strings.Contains(shared, secret) {
			t.Errorf("a sealed channel's file carries the user's %q:\n%s", secret, shared)
		}
	}
}

// THE ORDER IN THE CHANNEL SECTION: after the wire tables and before the shared pack env fold
// (userEnvFileBody), so a pack env of the same name still wins, as it did over the argv copy.
func TestTheSessionInputsFollowTheWireTables(t *testing.T) {
	a := assembleWithConfigAssembled(t, sessionInputsConfig())
	shared, _ := deliveredFiles(t, a.in.envChannel(a.o))
	tables := -1
	for _, k := range entrypoint.WireTables() {
		at := strings.Index(shared, "export "+k+"='")
		if at < 0 {
			t.Fatalf("the channel lacks the wire table %s:\n%s", k, shared)
		}
		tables = max(tables, at)
	}
	for _, name := range sessionInputNames {
		if at := strings.Index(shared, "export "+name+"='"); at < tables {
			t.Errorf("%s is not written after the wire tables (at %d, tables at %d):\n%s", name, at, tables, shared)
		}
	}
}
