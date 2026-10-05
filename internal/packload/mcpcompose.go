package packload

// mcpcompose.go composes the `mcp` contribution kind (packdecl.KindMCP) into yolo's own
// `mcp_servers` table — docs/design/mcp-presets-removal.md OQ-MP3, ruled 2026-09-20: "a named
// server entry composed into mcp_servers the way the provider kind composes into providers".
// Every selected pack's entries come first, the user's `mcp_servers` table is merged over them
// per field, and a user `null` removes one. Every agent pack's derive renders the composed table
// in its own dialect, so neither core nor the contributing pack names an agent.
//
// HOST-SIDE, ONCE PER NOTCH (OQ-MP4, dissolved: "compose HOST-SIDE, exactly like providers"):
// the launch composes for the container's home (run's YOLO_MCP_SERVERS), the macos-user plan for
// the sandbox account's (macosuser's bootstrap env), and `yolo host apply` for your own
// (cli's composeHostInputs). Each passes the home its notch renders into, and that home is the
// one thing that differs: an entry's `~/` words (packdecl.MCPHomePrefix) are joined to it, since
// an MCP client spawns servers with a scrubbed environment and needs an absolute path.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// HeldMCPServer is one `mcp` contribution and the pack that declared it.
type HeldMCPServer struct {
	Pack   string
	Server packdecl.MCPContribution
}

// HeldMCPServers is every `mcp` declaration of packs that holds its server name, in packs' order
// then declaration order, under the one rule for a sole-owned claim two packs declare: the later
// one holds it (laterWins, NC-D59). The refusal of such a pair is not this function's: validation
// refuses it before any launch (config's validatePackMCPServers, through MCPNameCollisions), and
// `yolo pack footprint` reports it (Collisions).
func HeldMCPServers(packs []*Pack) []HeldMCPServer {
	var all []HeldMCPServer
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, s := range p.Decl.MCPContributions() {
			all = append(all, HeldMCPServer{Pack: p.Name, Server: s})
		}
	}
	holds := laterWins(len(all), func(i int) string { return all[i].Server.Name })
	var out []HeldMCPServer
	for i, s := range all {
		if holds[i] {
			out = append(out, s)
		}
	}
	return out
}

// MCPNameCollisions is one message per MCP server name more than one declaration of packs ships,
// sorted by name, each naming the packs and the next step. Per declaration rather than per pack,
// for packProviderNameConflicts' reason: one manifest declaring a name twice is just as silent at
// the compose, where the second entry would replace the first.
func MCPNameCollisions(packs []*Pack) []string {
	byName := map[string][]string{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, s := range p.Decl.MCPContributions() {
			byName[s.Name] = append(byName[s.Name], p.Name)
		}
	}
	names := make([]string, 0, len(byName))
	for name, by := range byName {
		if len(by) > 1 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out []string
	for _, name := range names {
		var distinct []string
		seen := map[string]bool{}
		for _, p := range byName[name] {
			if !seen[p] {
				seen[p] = true
				distinct = append(distinct, p)
			}
		}
		who := "packs " + strings.Join(distinct, " and ")
		if len(distinct) == 1 {
			who = "pack " + distinct[0] + " more than once"
		}
		out = append(out, fmt.Sprintf("MCP server %q is shipped by %s — a server name is "+
			"sole-owned, the key its entry lands under in mcp_servers, so one would silently replace "+
			"the other. Remove one of those packs from `packs` in ~/.config/yolo-jail/config.jsonc; "+
			"to change the server you keep, write mcp_servers.%s there, which merges over it",
			name, who, name))
	}
	return out
}

// ComposeMCPServers is the `mcp_servers` table a notch renders: every held `mcp` declaration of
// packs, its `~/` words joined to home, then the user's table over it — a `null` removes the
// pack's entry and stays in the table as a removal (so the jail's preset expansion loses that
// name too, as it always has), and an object merges over the pack's entry per field, `env` per
// variable. A user entry no pack ships is copied as written. The result holds nothing the caller
// owns, so a consumer may edit it. Never nil.
//
// Interim coexistence with `mcp_presets` needs no rule here: a jail expands its presets first and
// lets this table override them by name (entrypoint's mcpServersWith), so a pack's entry replaces
// a preset of the same name.
func ComposeMCPServers(user *jsonx.OrderedMap, packs []*Pack, home string) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, held := range HeldMCPServers(packs) {
		entry, ok := DecodeMCPEntry(held.Server, home)
		if !ok {
			continue // the manifest validator has refused the shape already
		}
		out.Set(held.Server.Name, entry)
	}
	if user == nil {
		return out
	}
	for _, name := range user.Keys() {
		v, _ := user.Get(name)
		if v == nil {
			out.Set(name, nil)
			continue
		}
		u, ok := jsonx.DeepCopy(v).(*jsonx.OrderedMap)
		if !ok {
			out.Set(name, jsonx.DeepCopy(v)) // malformed; the config validator reported it
			continue
		}
		if cur, seen := out.Get(name); seen {
			if pm, isMap := cur.(*jsonx.OrderedMap); isMap {
				mergeMCPEntry(pm, u)
				continue
			}
		}
		out.Set(name, u)
	}
	return out
}

// MCPServerSources names, per server of ComposeMCPServers' table, the pack whose entry it starts
// from, for a report: "chrome-devtools (pack chrome-devtools)", and a user override says so
// ("…, your mcp_servers merged over it"). A user-only entry is not named: it is yours.
func MCPServerSources(user *jsonx.OrderedMap, packs []*Pack) []string {
	var out []string
	for _, held := range HeldMCPServers(packs) {
		line := held.Server.Name + " (pack " + held.Pack
		if user != nil {
			if v, ok := user.Get(held.Server.Name); ok {
				if v == nil {
					continue // removed by your null
				}
				line += ", your mcp_servers merged over it"
			}
		}
		out = append(out, line+")")
	}
	return out
}

// DecodeMCPEntry is one declaration's entry as an mcp_servers value for home: its object, with
// every `command` and `args` word that begins with packdecl.MCPHomePrefix joined to home. ok is
// false for an entry that is not an object.
func DecodeMCPEntry(s packdecl.MCPContribution, home string) (*jsonx.OrderedMap, bool) {
	v, err := jsonx.Decode(s.Entry)
	if err != nil {
		return nil, false
	}
	entry, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return nil, false
	}
	if cmd, ok := entry.Get("command"); ok {
		if w, isStr := cmd.(string); isStr {
			entry.Set("command", joinMCPHome(w, home))
		}
	}
	if args, ok := entry.Get("args"); ok {
		if list, isList := args.([]any); isList {
			for i, a := range list {
				if w, isStr := a.(string); isStr {
					list[i] = joinMCPHome(w, home)
				}
			}
		}
	}
	return entry, true
}

// joinMCPHome is w with a leading packdecl.MCPHomePrefix replaced by home and a separator, w
// itself for any other word. home is the notch's, never with a trailing slash ("/" excepted).
func joinMCPHome(w, home string) string {
	rest, ok := strings.CutPrefix(w, packdecl.MCPHomePrefix)
	if !ok || home == "" {
		return w
	}
	return strings.TrimSuffix(home, "/") + "/" + rest
}

// mergeMCPEntry merges the user's entry u over the pack's dst, per field: `env` per variable when
// both are objects, and every other field replaced; a null field removes the pack's.
func mergeMCPEntry(dst, u *jsonx.OrderedMap) {
	for _, k := range u.Keys() {
		v, _ := u.Get(k)
		if v == nil {
			dst.Delete(k)
			continue
		}
		if k == "env" {
			if um, isMap := v.(*jsonx.OrderedMap); isMap {
				if cur, ok := dst.Get(k); ok {
					if dm, isMap := cur.(*jsonx.OrderedMap); isMap {
						for _, ek := range um.Keys() {
							ev, _ := um.Get(ek)
							if ev == nil {
								dm.Delete(ek)
								continue
							}
							dm.Set(ek, ev)
						}
						continue
					}
				}
			}
		}
		dst.Set(k, v)
	}
}

// mcpClaimDetail is an `mcp` claim's Detail in `yolo pack footprint`: the command and arguments
// the agent's MCP client starts, `~/` words as declared (the home is the notch's), and the
// program it runs when the declaration names one.
func mcpClaimDetail(c packdecl.Contribution) string {
	entry, ok := DecodeMCPEntry(packdecl.MCPContribution{Name: c.Name, Bin: c.Bin, Entry: c.Raw}, "")
	if !ok {
		return "an MCP server entry"
	}
	words := []string{}
	if cmd, ok := entry.Get("command"); ok {
		if s, isStr := cmd.(string); isStr {
			words = append(words, s)
		}
	}
	if args, ok := entry.Get("args"); ok {
		if list, isList := args.([]any); isList {
			for _, a := range list {
				if s, isStr := a.(string); isStr {
					words = append(words, s)
				}
			}
		}
	}
	detail := "runs " + strings.Join(words, " ")
	if c.Bin != "" {
		detail += " (program " + c.Bin + ")"
	}
	return detail
}
