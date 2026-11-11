package check

// secretenvcallsites_test.go pins `yolo check`'s secret-env call sites by what they do, so deleting
// one fails a test (review major 5): overrideScope's MCPClaims and UnselectedClaims, and the
// entrypoint preflight's public-only env.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func embeddedPackNamed(t *testing.T, name string) *packload.Pack {
	t.Helper()
	for _, p := range packload.Embedded() {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no shipped pack %s", name)
	return nil
}

func noProfiles() (map[string]packload.UserProfile, error) { return nil, nil }

func TestOverrideScopeHandsTheGateTheMCPClaims(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	merged, err := jsonx.Decode([]byte(`{"env": {"secrets": {"set": {"TAVILY_API_KEY": "t-probe"}}},
		"mcp_servers": {"tavily": {"command": "tavily-mcp", "requires_env": ["TAVILY_API_KEY"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := merged.(*jsonx.OrderedMap)
	scope, _ := overrideScope([]*packload.Pack{embeddedPackNamed(t, "claude")}, cfg, packload.ServedDaemons{}, t.TempDir(),
		func(string) {}, noProfiles)
	if _, ok := scope.EnvSourcesFor("claude").Get("TAVILY_API_KEY"); !ok {
		t.Error("the server's requires_env claims the private key for claude, which renders the MCP table")
	}
	if lines := strings.Join(scope.EnvSetLines(packload.DisclosureNotes{}), "\n"); strings.Contains(lines, "TAVILY_API_KEY") {
		t.Errorf("an MCP-claimed name is not a withheld one:\n%s", lines)
	}
}

func TestOverrideScopeHandsTheGateTheUnselectedClaims(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	merged, err := jsonx.Decode([]byte(`{"env": {"set": {"ZAI_API_KEY": "z-probe"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := overrideScope(nil, merged.(*jsonx.OrderedMap), packload.ServedDaemons{}, t.TempDir(), func(string) {}, noProfiles)
	lines := strings.Join(scope.EnvSetLines(packload.DisclosureNotes{}), "\n")
	if !strings.Contains(lines, "the zai provider, which this launch does not select, claims that name") {
		t.Errorf("row 1's line names the unselected provider:\n%s", lines)
	}
}

// The entrypoint preflight builds the boot environment, which every process inherits, so it reads
// the public set alone. Its variable map is local to the function, so the witness is the source:
// the call must be PublicValues, never the whole set. (The launch's own twin is behavioral:
// TestTheSessionInputsReadThePublicSetAlone.)
func TestThePreflightBootEnvReadsThePublicSetAlone(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "entrypoint.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var resolves, public, whole int
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "runEntrypointPreflight" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "ResolveEnv":
				resolves++
			case "PublicValues":
				public++
			case "Values", "Private", "Places":
				whole++
			}
			return true
		})
		return false
	})
	if resolves != 1 || public != 1 || whole != 0 {
		t.Errorf("runEntrypointPreflight: ResolveEnv x%d, PublicValues x%d, other readers x%d; want 1, 1, 0", resolves, public, whole)
	}
}
