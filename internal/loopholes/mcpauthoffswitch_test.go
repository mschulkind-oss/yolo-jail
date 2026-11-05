package loopholes

import (
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestMCPAuthUserOffWinsOverProjectOnInSelectedDiscovery(t *testing.T) {
	modules := shippedModules(t, "mcp-auth", "codex-auth")
	cfg := jsonx.NewOrderedMap()
	allow := jsonx.NewOrderedMap()
	user := jsonx.NewOrderedMap()
	user.Set("off", true)
	allow.Set("mcp-auth", user)
	cfg.Set("allow", allow)
	requests := jsonx.NewOrderedMap()
	project := jsonx.NewOrderedMap()
	project.Set("off", false)
	requests.Set("mcp-auth", project)
	cfg.Set("requests", requests)
	legacy := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	legacy.Set("mcp-auth-broker", entry)
	cfg.Set("loopholes", legacy)
	got := enabledNames(NewSelectedSet(LaunchBlock(cfg), modules, nil))
	if !slices.Equal(got, []string{"openai-auth-broker"}) {
		t.Fatalf("owner off must keep MCP broker off without disabling Codex's OpenAI broker: %v", got)
	}
	if got := enabledNames(NewSelectedSet(LaunchBlock(jsonx.NewOrderedMap()), modules, nil)); !slices.Contains(got, "mcp-auth-broker") {
		t.Fatalf("normal selected pack lost its MCP broker: %v", got)
	}
}
