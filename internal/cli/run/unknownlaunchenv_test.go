package run

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func literalStringPrefix(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)
			if err == nil {
				return text
			}
		}
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			return literalStringPrefix(value.X) + literalStringPrefix(value.Y)
		}
	case *ast.ParenExpr:
		return literalStringPrefix(value.X)
	}
	return ""
}

func recordEmittedLaunchEnvNames(exprs []ast.Expr, emitted map[string]bool) {
	for i := 0; i+1 < len(exprs); i++ {
		if literalStringPrefix(exprs[i]) != "-e" {
			continue
		}
		name, _, ok := strings.Cut(literalStringPrefix(exprs[i+1]), "=")
		if ok && strings.HasPrefix(name, "YOLO_") {
			emitted[name] = true
		}
	}
}

func TestLaunchYOLOEnvRecognitionCoversReadersAndInheritedProducers(t *testing.T) {
	known := knownLaunchYOLOEnvNames(nil, nil)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	readers := 0
	emitted := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Clean(name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch value := n.(type) {
			case *ast.CallExpr:
				recordEmittedLaunchEnvNames(value.Args, emitted)
			case *ast.CompositeLit:
				recordEmittedLaunchEnvNames(value.Elts, emitted)
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			keyIndex := -1
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Getenv" {
				base, ok := selector.X.(*ast.Ident)
				if ok && (base.Name == "o" || base.Name == "os") && len(call.Args) == 1 {
					keyIndex = 0
				}
			} else if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "envLineValue" && len(call.Args) == 2 {
				// envLineValue reads values from the same host-generated environment contract
				// after a launcher attach; these calls are not literal os.Getenv readers.
				keyIndex = 1
			}
			if keyIndex < 0 {
				return true
			}
			literal, ok := call.Args[keyIndex].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			key, err := strconv.Unquote(literal.Value)
			if err != nil || !strings.HasPrefix(key, "YOLO_") {
				return true
			}
			readers++
			if !slices.Contains(known, key) {
				t.Errorf("environment reader in %s uses unrecognized %s", name, key)
			}
			return true
		})
	}
	if readers == 0 {
		t.Fatal("no direct or wrapped YOLO environment readers found; census no longer measures the package")
	}
	for _, name := range []string{"YOLO_PUBLISHED_PORTS", "YOLO_FORWARD_HOST_PORTS", "YOLO_PROGRAMS_AUTOPRUNE"} {
		if !emitted[name] {
			t.Errorf("source census did not find launch producer for %s", name)
		}
		if !slices.Contains(known, name) {
			t.Errorf("source-backed launch producer %s is not recognized", name)
		}
	}
}

func TestKnownLaunchYOLOEnvNamesUseSelectedDeclarations(t *testing.T) {
	selected := &packload.Pack{Name: "selected", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindEnv, Vars: map[string]string{"YOLO_SELECTED_PACK_SETTING": "not-used"}},
		{Kind: packdecl.KindService, Name: "selected-service"},
		{Kind: packdecl.KindProgram, Bin: "selected-tool", Via: "npm", Package: "selected-tool"},
	}}}
	known := knownLaunchYOLOEnvNames([]*packload.Pack{selected}, &packChannel{
		callerTokens: map[string]string{"YOLO_SERVICE_SELECTED_SERVICE_TOKEN": "secret-not-read"},
	})
	for _, name := range []string{
		"YOLO_SELECTED_PACK_SETTING",
		paths.ServiceEnvVarPrefix + "SELECTED_SERVICE" + paths.ServiceEnvVarSuffix,
		"YOLO_SERVICE_SELECTED_SERVICE_TOKEN",
		"YOLO_AUTH_PRELAUNCH_SELECTED_TOOL_FLAG",
		"YOLO_AUTH_PRELAUNCH_SELECTED_TOOL_PATH",
		"YOLO_AUTH_PRELAUNCH_SELECTED_TOOL_LOGIN",
	} {
		if !slices.Contains(known, name) {
			t.Errorf("selected declaration name %s not recognized", name)
		}
	}
	for _, name := range []string{"YOLO_BACKEND", "YOLO_JAIL", "YOLO_REPO_ROOT", "YOLO_SELECTED_OTHER_PACK_SETTING", "YOLO_AUTH_PRELAUNCH_UNSELECTED_TOOL_FLAG"} {
		if slices.Contains(known, name) {
			t.Errorf("unselected or unrecognized name %s was accepted", name)
		}
	}
	if !slices.IsSorted(known) {
		t.Errorf("known names are not deterministic/sorted: %v", known)
	}
}

func TestKnownLaunchYOLOEnvNamesIncludeHydratedChannelNames(t *testing.T) {
	userEnv := jsonx.NewOrderedMap()
	userEnv.Set("YOLO_CONFIGURED_CHANNEL_NAME", "secret-not-read")
	known := knownLaunchYOLOEnvNames(nil, &packChannel{userEnv: userEnv})
	if !slices.Contains(known, "YOLO_CONFIGURED_CHANNEL_NAME") {
		t.Fatalf("hydrated channel key was not recognized: %v", known)
	}
}

func TestRunInvokesUnknownYOLOEnvironmentWarningBeforeBackend(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	called := 0
	o.WarnUnknownYOLOEnv = func(names []string, _ io.Writer) {
		called++
		// These observed names are produced, wrapped, or read across the host/entrypoint
		// boundary rather than all appearing as literal os.Getenv calls in this package.
		for _, name := range []string{
			"YOLO_AGENT_UPDATES", "YOLO_CAPTURES_DIR", "YOLO_CONTEXT_DIR", "YOLO_CONTRACT_TAGS", "YOLO_CTX_MOUNTS_TOKEN",
			"YOLO_FORWARD_HOST_PORTS", "YOLO_PUBLISHED_PORTS", "YOLO_PROGRAMS_AUTOPRUNE",
			"YOLO_FORK_BUILDS", "YOLO_HOST_DIR", "YOLO_HOST_LAYERS", "YOLO_HOST_LOOPBACK", "YOLO_HOST_PATH_MAP",
			"YOLO_JAIL_DAEMONS", "YOLO_JAIL_DAEMON_READY_NAMES", "YOLO_JAIL_MAIN", "YOLO_LSP_SERVERS",
			"YOLO_MCP_PRESETS", "YOLO_MISE_TOOLS", "YOLO_PATCHED_TREES", "YOLO_PROVIDERS", "YOLO_PROFILES",
			"YOLO_USE_PROFILES", "YOLO_PROVISION_MS", "YOLO_SESSION_ID", "YOLO_USER_LAYER",
			"YOLO_SERVICE_AWS_AUTH_ENDPOINT", "YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT",
			"YOLO_SERVICE_GITHUB_BROKER_ENDPOINT", "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT",
			"YOLO_SERVICE_WIRE_BRIDGE_TOKEN", "YOLO_AUTH_PRELAUNCH_CODEX_FLAG", "YOLO_AUTH_PRELAUNCH_CODEX_PATH",
			"YOLO_PI_PROVIDER_POLICY",
		} {
			if !slices.Contains(names, name) {
				t.Errorf("recognized source-backed name %s missing from callback input %v", name, names)
			}
		}
		for _, name := range []string{
			"YOLO_RUNTIME", "YOLO_PACK_ROOT", "YOLO_CTX_ROOT", "YOLO_MCP_SERVERS", "YOLO_MODEL_FRONTIER",
			"YOLO_NO_TMUX", "YOLO_NO_HERDR", "YOLO_JAIL_BACKEND", "YOLO_BLOCK_CONFIG",
		} {
			if !slices.Contains(names, name) {
				t.Errorf("recognized name %s missing from callback input %v", name, names)
			}
		}
		for _, name := range []string{"YOLO_BACKEND", "YOLO_JAIL", "YOLO_ITEM100_INVENTED_UNKNOWN"} {
			if slices.Contains(names, name) {
				t.Errorf("unrecognized name %s accepted in callback input %v", name, names)
			}
		}
	}
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		if called != 1 {
			t.Errorf("warning callback calls before backend = %d, want exactly one", called)
		}
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if called != 1 {
		t.Fatalf("warning callback calls = %d, want exactly one", called)
	}
}

// YOLO_JAIL is no yolo dial and no yolo code writes it, so it stays unrecognized as a fixed name
// (above). It reaches a jail's environment through the user's own inline `env_sources`, which the
// jail's inherited-launch file carries into every nested launch. So the launch recognizes it from
// the env_sources it hydrates: a nested launch from such a jail does not warn about it, and a host
// shell that sets YOLO_JAIL with no config behind it still does. This pins the call site handing
// the composed channel to the warning; pass it nil and the name is warned about again.
func TestALaunchRecognizesTheYOLONamesItsOwnEnvSourcesSet(t *testing.T) {
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	writeUserConfigJSON(t, home, `{"env_sources": [{"YOLO_JAIL": "1"}]}`)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	var names []string
	called := 0
	o.WarnUnknownYOLOEnv = func(known []string, _ io.Writer) {
		called++
		names = known
	}
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if called != 1 {
		t.Fatalf("warning callback calls = %d, want exactly one", called)
	}
	if !slices.Contains(names, "YOLO_JAIL") {
		t.Errorf("YOLO_JAIL, set by the user's env_sources, is warned about as unrecognized: %v", names)
	}
	if slices.Contains(names, "YOLO_ITEM100_INVENTED_UNKNOWN") {
		t.Errorf("a name no config sets was recognized: %v", names)
	}
}

// YOLO_MCP_PRESETS retired with the `mcp_presets` key on 2026-10-23, but a jail an older yolo
// started still exports it, so a nested or integration launch from one inherits it. Its user
// cannot unset it in an environment yolo set up, so the launch must recognize it rather than
// warn about it on every nested launch.
func TestARetiredMCPPresetsWireFromAnOlderOuterJailIsRecognized(t *testing.T) {
	if !slices.Contains(knownLaunchYOLOEnvNames(nil, nil), "YOLO_MCP_PRESETS") {
		t.Fatal("YOLO_MCP_PRESETS is not a recognized launch name, so every launch from a jail an " +
			"older yolo started warns about a variable its user cannot act on")
	}
}

// The session-home bindings (per-session-profiles.md SP-D36) are exported session-wide in a jail,
// so a launch nested in one inherits them and must not warn about a name its user cannot unset.
func TestTheSessionHomeBindingsAreARecognizedLaunchName(t *testing.T) {
	if !slices.Contains(knownLaunchYOLOEnvNames(nil, nil), entrypoint.SessionHomesEnv) {
		t.Fatalf("%s is not a recognized launch name, so every launch nested in a jail with a "+
			"session home warns about it", entrypoint.SessionHomesEnv)
	}
}

func TestUnknownYOLOEnvironmentWarningUsesLaunchLogWriter(t *testing.T) {
	const unknown = "YOLO_ITEM98_LOG_UNKNOWN"
	t.Setenv(unknown, "secret-item98-value")
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		return 0
	}
	// Run must supply the current writer after attachLaunchLog installs its tee.
	o.WarnUnknownYOLOEnv = func(names []string, out io.Writer) {
		if !slices.Contains(names, unknown) {
			fmt.Fprintf(out, "warning: %s is not recognized\n", unknown)
		}
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if strings.Count(stderr.String(), "warning: "+unknown) != 1 || strings.Contains(stderr.String(), "secret-item98-value") {
		t.Fatalf("terminal must contain the warning once and omit its value:\n%s", stderr.String())
	}
	log := readLaunchLog(t, ws)
	if strings.Count(log, "warning: "+unknown) != 1 || strings.Contains(log, "secret-item98-value") {
		t.Fatalf("launch log must contain the warning once and omit its value:\n%s", log)
	}
}

func TestUnknownYOLOBackendDoesNotSelectRuntime(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "", &stdout, &stderr, nil)
	o.Getenv = func(name string) string {
		if name == "YOLO_BACKEND" {
			return "podman"
		}
		return ""
	}
	if rt, ok := o.resolveRuntime(jsonx.NewOrderedMap()); ok {
		t.Fatalf("YOLO_BACKEND selected runtime %q", rt)
	}
}

func TestRecognizedYOLORuntimeStillSelectsPodman(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "podman", &stdout, &stderr, nil)
	if rt, ok := o.resolveRuntime(jsonx.NewOrderedMap()); !ok || rt != "podman" {
		t.Fatalf("YOLO_RUNTIME=podman selection = %q, %v; want podman, true", rt, ok)
	}
}
