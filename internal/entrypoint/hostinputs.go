package entrypoint

// hostinputs.go is the HOST notch's half of the derive inputs (docs/reference/host-agent-environment.md,
// the computed layer at the host, OQ-HC1): what a host render runs every derive over, and the check that keeps a
// jail-only path out of the real home.
//
// THE RULING IS PARITY WITH THE SAME HANDLING (OQ-HC1, 2026-09-28): "host apply and the auto one
// in a wrapper should generate this content." So the host runs the SAME derives, through the
// SAME readers, and the difference between the notches lives in the inputs alone. That is why
// HostInputs carries the wire tables a jail launch exports (YOLO_PROVIDERS, YOLO_PROFILES,
// YOLO_USE_PROFILES, YOLO_MCP_SERVERS, YOLO_LSP_SERVERS) rather than a host-shaped struct of
// its own: the host Env holds them in its Vars, and LoadProviders, LoadProfiles,
// LoadUseProfiles, mcpServersWith and LoadLSPServers read them exactly as a jail's boot does
// (HC-D13). The CLI composes them at user scope (internal/cli's composeHostInputs).
//
// What the inputs never hold: an MCP preset (its command is a jail-only wrapper), a pack's `mcp`
// declaration (OQ-MP3: "reaches only the jail"), a workspace's config, or a via address (no
// jail daemon serves one here). YOLO_MCP_PRESETS is deleted from the Vars even when a caller
// sets it, so the preset expansion cannot run at this notch whatever the caller hands in.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// MCPServersWireEnv and LSPServersWireEnv are the two server tables' wire variables, the
// spelling a jail launch exports (run's `-e YOLO_MCP_SERVERS=` pair) and the one the host
// composition sets, so both notches' readers read one name.
const (
	MCPServersWireEnv = "YOLO_MCP_SERVERS"
	LSPServersWireEnv = "YOLO_LSP_SERVERS"
	mcpPresetsWireEnv = "YOLO_MCP_PRESETS"
)

// HostInputs are the derive inputs of one host render, composed ONCE per invocation by the
// caller and handed to every RenderHostPack call of it (HC-D11), so the dry run, the --assert,
// the launch gate and `yolo config render --at host` render from one composition.
//
// The zero value — and a nil pointer — is the EMPTY composition: no provider, no server, no
// selection. Every derive still runs over it, since parity has no opt-out (OQ-HC1), and each
// writes its empty shape.
type HostInputs struct {
	// Vars are the wire tables, as a jail launch exports them (see the file header).
	Vars map[string]string
	// Packs is the selected pack set, which the selection's two pack-read fields answer over:
	// each surface agent's native capabilities (packload.NativeCapabilities, so a server whose
	// `provides` that agent's own login performs is withheld from it, HC-D6) and the via key
	// variable. nil reads as "no pack declares a capability".
	Packs []*packload.Pack
	// AgentLookup answers requires_env for ONE agent: the environment `yolo host env --agent
	// <agent>` would compose at apply time (HC-D6, OQ-CN6), so a server whose variable only
	// the agent that selected its provider holds is written for that agent alone. nil means
	// nothing is set, and every server that requires a variable is skipped.
	AgentLookup func(agent string) func(string) (string, bool)
}

// vars is the Env's variable matrix for a host render: the wire tables, and never the preset
// list.
func (in *HostInputs) vars() map[string]string {
	out := map[string]string{}
	if in == nil {
		return out
	}
	for k, v := range in.Vars {
		out[k] = v
	}
	delete(out, mcpPresetsWireEnv)
	return out
}

// hostSources is one host render's per-agent derive tables, built lazily and at most once per
// agent: the jail's liveTables over this Env's wire tables, with the agent's own MCP table (the
// requires_env gate asked through that agent's lookup) in place of the shared one — the host
// twin of loadMCPTables + tablesForAgent.
type hostSources struct {
	e       *Env
	in      *HostInputs
	byAgent map[string]hostAgentTables
}

// hostAgentTables is one agent's tables and the servers its requires_env gate removed.
type hostAgentTables struct {
	tables  map[string]map[string]any
	skipped []mcpSkip
}

func newHostSources(e *Env, in *HostInputs) *hostSources {
	return &hostSources{e: e, in: in, byAgent: map[string]hostAgentTables{}}
}

// forAgent is agent's derive tables.
func (h *hostSources) forAgent(agent string) hostAgentTables {
	if t, ok := h.byAgent[agent]; ok {
		return t
	}
	lookup := func(string) (string, bool) { return "", false }
	if h.in != nil && h.in.AgentLookup != nil {
		if l := h.in.AgentLookup(agent); l != nil {
			lookup = l
		}
	}
	servers, skipped := h.e.mcpServersWith(lookup)
	t := hostAgentTables{tables: liveTables(h.e, servers), skipped: skipped}
	h.byAgent[agent] = t
	return t
}

// selectionFor is one surface's resolved selection at the host: the jail's own resolution
// (surfaceSelectionFor) over this render's profile tables and the selected packs.
func (h *hostSources) selectionFor(s manifest.Surface) surfaceSelection {
	var packs []*packload.Pack
	if h.in != nil {
		packs = h.in.Packs
	}
	use := h.e.LoadUseProfiles()
	return surfaceSelectionFor(packs, h.e.LoadProfiles(),
		packload.ProfileTable(use), packload.ProfileSets(use), s)
}

// gatedByName is what agent's requires_env gate removed, each server mapped to the variables
// it lacks, for the loss list (entryLossLines). Nil when it removed nothing.
func (t hostAgentTables) gatedByName() map[string][]string {
	if len(t.skipped) == 0 {
		return nil
	}
	out := make(map[string][]string, len(t.skipped))
	for _, s := range t.skipped {
		out[s.name] = s.missing
	}
	return out
}

// skippedNotes are the per-surface lines for the servers agent's requires_env gate removed,
// in the words the jail's boot notice uses.
func (t hostAgentTables) skippedNotes() []string {
	var out []string
	for _, s := range t.skipped {
		out = append(out, fmt.Sprintf("MCP server %q not written — required env not set for "+
			"this agent: %s", s.name, strings.Join(s.missing, ", ")))
	}
	return out
}

// jailOnlyRoots are the absolute paths that exist inside a jail and in no real home, DERIVED
// from the jail render target the boot projects onto rather than listed by hand where the
// target can answer: that target's home and workspace (NewEnv's defaults, render.Jail), then the
// four mounts a launch supplies to every container jail — the /ctx context root, the install
// prefix, the /run/yolo tree the boot writes (store packages, caller tokens) and the service
// endpoint directory.
//
// A root the RENDERED home lies at or under is dropped (HC-D15): a host render whose home is
// /home/agent renders where /home/agent/... is the real home and not a jail path. That is a user
// whose account home happens to be /home/agent, and an in-jail verb that composes the host notch's
// inputs without writing them (`yolo config render`'s host target); `yolo host apply` itself
// refuses inside a jail since 2026-10-04, rendering into no jail's home.
func jailOnlyRoots(renderedHome string) []string {
	jail := NewEnv(nil).renderTarget()
	candidates := []string{jail.Home, jail.Workspace, packload.CtxRoot, paths.JailPrefixDir,
		filepath.Dir(StorePackagesRoot), paths.JailHostServicesDir}
	var out []string
	for _, r := range candidates {
		if r == "" || r == "/" {
			continue
		}
		if renderedHome != "" && pathWithin(filepath.Clean(renderedHome), filepath.Clean(r)) {
			continue
		}
		out = append(out, filepath.Clean(r))
	}
	sort.Strings(out)
	return out
}

// pathWithin reports whether p is root or lies under it, by path component.
func pathWithin(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+"/")
}

// JailPathsIn names every place in v that spells a jail-only path, as "<dotted key>: <value>",
// sorted — nil when there is none. v is a decoded value in either model (plain maps or jsonx's
// ordered ones); keys are checked as well as string values, since a key a render writes is
// written into the file too.
//
// A path is found where it STARTS a token: at the start of the string or after a character no
// path spells (a space, `=`, `:`, a quote), and where it ENDS one: the string's end, a `/`, or
// such a character. So `--dir=/workspace/x` and `/workspace` match, while `/workspaces`,
// `/srv/workspace` and `https://h/workspace` do not.
//
// Exported for internal/cli, whose host input composition omits a user's `mcp_servers` or
// `lsp_servers` entry that names one (HC-D6) with the same predicate this package's output
// check uses (HC-D14), so the two cannot disagree about what a jail path is.
func JailPathsIn(v any, renderedHome string) []string {
	roots := jailOnlyRoots(renderedHome)
	var out []string
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		join := func(k string) string {
			if prefix == "" {
				return k
			}
			return prefix + "." + k
		}
		switch t := v.(type) {
		case map[string]any:
			for _, k := range sortedKeys(t) {
				if root := jailRootIn(k, roots); root != "" {
					out = append(out, join(k)+": "+k)
				}
				walk(join(k), t[k])
			}
		case *jsonx.OrderedMap:
			if t == nil {
				return
			}
			for _, k := range t.Keys() {
				if root := jailRootIn(k, roots); root != "" {
					out = append(out, join(k)+": "+k)
				}
				sub, _ := t.Get(k)
				walk(join(k), sub)
			}
		case []any:
			for i, e := range t {
				walk(fmt.Sprintf("%s[%d]", prefix, i), e)
			}
		case string:
			if jailRootIn(t, roots) != "" {
				out = append(out, prefix+": "+t)
			}
		}
	}
	walk("", v)
	sort.Strings(out)
	return out
}

// jailRootIn is the first root s spells as a path token, or "".
func jailRootIn(s string, roots []string) string {
	for _, r := range roots {
		for from := 0; from < len(s); {
			i := strings.Index(s[from:], r)
			if i < 0 {
				break
			}
			i += from
			before := i == 0 || !isPathChar(s[i-1])
			end := i + len(r)
			after := end == len(s) || s[end] == '/' || !isPathChar(s[end])
			if before && after {
				return r
			}
			from = i + 1
		}
	}
	return ""
}

// isPathChar reports whether c can sit inside a path segment or join two, so a root found
// beside it is part of a longer path rather than a path of its own.
func isPathChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("._-~/+@%", c) >= 0
}
