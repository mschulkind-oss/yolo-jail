package packload

// mcpclaims.go is the MCP half of a claim (docs/reference/providers.md#the-two-env-sets-public-and-private SE-D13): an MCP
// server's `requires_env` claims a name for the agents whose config names that server, and the
// credential gate delivers a PRIVATE value of that name to those agents only. It never makes a
// public value a refusal (SE-D15); only a provider's claim does.

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// MCPEnvClaims maps each name a composed MCP server's `requires_env` lists to the servers listing
// it, sorted. servers is the composed `mcp_servers` table (ComposeMCPServers); a removed entry
// (nil, "off") claims nothing. Nil when no server lists one.
func MCPEnvClaims(servers *jsonx.OrderedMap) map[string][]string {
	if servers == nil {
		return nil
	}
	var out map[string][]string
	for _, server := range servers.Keys() {
		v, _ := servers.Get(server)
		entry, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		req, ok := entry.Get("requires_env")
		if !ok {
			continue
		}
		list, ok := req.([]any)
		if !ok {
			continue
		}
		for _, n := range list {
			name, ok := n.(string)
			if !ok || name == "" {
				continue
			}
			if out == nil {
				out = map[string][]string{}
			}
			out[name] = appendUnique(out[name], server)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// MCPAgents is every agent whose config names the composed MCP servers
// (docs/reference/providers.md SEP-D5): the bins of every `[[program]]` a selected pack declares
// whose pack ships a derive.lua, since each such agent's derive renders the one composed MCP
// table. A program with no derive renders none. Sorted, deduplicated.
func MCPAgents(packs []*Pack) []string {
	var out []string
	for _, p := range packs {
		if p == nil || p.Decl == nil || DeriveScript(p) == "" {
			continue
		}
		out = append(out, p.InstallBins()...)
	}
	return sortedUnique(out)
}
