package entrypoint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
)

func (e *Env) recordRemoteMCP(agent string, ctx *luahook.DeriveCtx, layer map[string]any) {
	for name, transport := range luahook.WithheldRemoteMCPServers(ctx) {
		if e.remoteMCPWithheld == nil {
			e.remoteMCPWithheld = map[string]map[string]string{}
		}
		if e.remoteMCPWithheld[agent] == nil {
			e.remoteMCPWithheld[agent] = map[string]string{}
		}
		e.remoteMCPWithheld[agent][name] = transport
	}
	configured := ctx.Tables["mcp_servers"]
	capabilityWithheld := luahook.WithheldMCPServers(ctx)
	for name := range remoteMCPNamesInLayer(layer) {
		raw, ok := configured[name]
		server, _ := raw.(map[string]any)
		if !ok || server == nil {
			continue
		}
		if _, remote := server["url"].(string); !remote {
			continue
		}
		if _, withheld := capabilityWithheld[name]; withheld {
			continue
		}
		if e.remoteMCPRendered == nil {
			e.remoteMCPRendered = map[string][]string{}
		}
		e.remoteMCPRendered[name] = append(e.remoteMCPRendered[name], agent)
	}
}

func remoteMCPNamesInLayer(layer map[string]any) map[string]bool {
	out := map[string]bool{}
	var walk func(any)
	walk = func(value any) {
		object, ok := value.(map[string]any)
		if !ok {
			return
		}
		for key, child := range object {
			if key == "mcp_servers" || key == "mcpServers" || key == "mcp" {
				if servers, ok := child.(map[string]any); ok {
					for name, raw := range servers {
						if server, ok := raw.(map[string]any); ok {
							if _, remote := server["url"].(string); remote {
								out[name] = true
							}
						}
					}
				}
			}
			walk(child)
		}
	}
	walk(layer)
	return out
}

func remoteMCPNotices(withheld map[string]map[string]string, rendered map[string][]string) []string {
	servers := map[string]bool{}
	for _, entries := range withheld {
		for name := range entries {
			servers[name] = true
		}
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		var agents []string
		transport := "http"
		for agent, entries := range withheld {
			if t, ok := entries[name]; ok {
				agents = append(agents, agent)
				if t != "http" {
					transport = t
				}
			}
		}
		sort.Strings(agents)
		available := append([]string(nil), rendered[name]...)
		sort.Strings(available)
		available = uniqueStrings(available)
		if len(available) == 0 {
			verb := "has"
			if len(agents) != 1 {
				verb = "have"
			}
			lines = append(lines, fmt.Sprintf("%s: %s %s no remote MCP form in yolo yet, so no agent in this jail is configured for it", name, strings.Join(agents, ", "), verb))
			continue
		}
		verb, form := "have no", "remote MCP form"
		if transport != "http" {
			form = fmt.Sprintf("%q MCP form", transport)
		}
		if len(agents) == 1 {
			verb = "has no"
		}
		lines = append(lines, fmt.Sprintf("%s: %s %s in yolo yet; it is configured for %s", name, strings.Join(agents, ", "), verb+" "+form, strings.Join(available, ", ")))
	}
	return lines
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
