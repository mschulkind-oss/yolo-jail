package manifest

// computed.go declares a surface's DYNAMIC layer as data, which is the last thing
// standing between "a pack is data" and reality.
//
// Every surface yolo ships already composes from static layers the manifest carries.
// What it could NOT carry was the computed layer — the content derived from live
// config each boot: the reconciled MCP table, the LSP-driven toggles. Those were built
// by six hand-written Go functions, one per agent, and a Go function is exactly what a
// third-party pack cannot ship (the goSrc fileset would have to contain it at image
// build time). So "core doesn't know what an agent is" stopped being true right here:
// core had to switch on an agent name to pick which builder to call.
//
// The fix is to name the SOURCE and the RESHAPE instead of the function:
//
//	computed:
//	  - from: mcp_servers        # which live table core should read
//	    to: mcpServers           # where in this surface it lands ("" = merge at root)
//	    project: {ops: [...]}    # how to reshape each entry into this agent's dialect
//	    omitEmpty: true          # drop the key entirely when the table is empty
//
// Core owns the sources (it knows what an MCP server is; that is config, not an agent
// concept) and owns the reshape ops (internal/agentcfg/project). A pack picks from
// both. Nothing here mentions an agent.
//
// The op set is NOT extended for this: project.Projection was already derived from
// the five real projections, and C6 confirmed all of them are expressible. This adds
// the wiring — which table, into which key — that the ops themselves never covered.

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/project"
)

// The closed set of live tables a surface may draw from. Closed on purpose: each name
// is a thing CORE knows how to produce, so an unknown name is a typo that would
// otherwise silently yield an empty layer — the failure mode that reads as "my MCP
// servers stopped working" with nothing to grep for.
const (
	// SourceMCPServers is the reconciled shared MCP-server table (config mcp_servers).
	SourceMCPServers = "mcp_servers"
	// SourceLSPServers is the configured LSP-server table (config lsp_servers).
	SourceLSPServers = "lsp_servers"
)

var knownComputedSources = map[string]bool{
	SourceMCPServers: true,
	SourceLSPServers: true,
}

// Computed is one derived layer contribution: take a live table, reshape each entry,
// place the result at To.
type Computed struct {
	// From names the live table (one of the Source* constants).
	From string `json:"from"`

	// To is the surface key the projected table lands under — "mcpServers" for claude
	// and copilot, "mcp" for opencode, "mcp_servers" for codex. Empty merges the
	// projected entries at the surface ROOT, which is how a table whose entries ARE
	// the surface's top-level keys is expressed.
	To string `json:"to,omitempty"`

	// Project is the per-entry reshape. Absent means passthrough — the entry is used
	// as-is, which is what a surface consuming the canonical shape wants.
	Project *project.Projection `json:"project,omitempty"`

	// OmitEmpty drops To ENTIRELY when the source table is empty, instead of emitting
	// an empty object.
	//
	// This is a semantic choice, not cosmetic, and it differs per surface. A surface
	// with no host layer wants the key gone (an empty `mcp` block in opencode's config
	// is noise). A surface WITH a host layer may need the empty object to stay, because
	// the key's absence from the computed layer means "no opinion" and the host block
	// would then survive — which for claude's settings.json mcpServers is precisely
	// wrong. Tombstone covers that case; this covers the other one.
	OmitEmpty bool `json:"omitEmpty,omitempty"`

	// Flags emit individual keys under To, each conditional on the source table.
	//
	// This is the one shape a table reshape cannot express, and it is here because two
	// real surfaces need it: claude's enabledPlugins (a plugin id is enabled when a
	// particular LSP is configured) and its env.ENABLE_LSP_TOOL (set when ANY LSP is).
	// Both are "assert a key because of something in a live table", where the key's name
	// has nothing to do with the table's keys — so no rename, suffix or projection
	// reaches it.
	//
	// A flag whose condition is FALSE emits a tombstone, not an omission. That is what
	// the Go code it replaced did (an explicit Delete), and it is the correct choice: the
	// key may be sitting in the user's host file from a boot when the LSP was configured,
	// and leaving it would enable a plugin that is no longer installed.
	Flags []Flag `json:"flags,omitempty"`

	// Reconcile makes an RMW surface's To key a MANAGED DYNAMIC TABLE: on each write,
	// entries yolo asserted last time are removed and the current table is added, while
	// entries the agent added itself survive.
	//
	// This is the gap the RMW mechanism documented and did not fill. Plain RMW asserts a
	// STATIC key set, so it cannot express a REMOVAL from a dynamic table: with no record
	// of what yolo asserted last boot, "the agent added this server" and "yolo added it
	// and config has since dropped it" look identical on disk. Reconcile closes it by
	// keeping that record — a sidecar listing the names yolo asserted — which is exactly
	// what claude's hand-written .claude.json writer did with
	// yolo-managed-mcp-servers.json. Generalizing it is what let that writer go.
	//
	// Only meaningful on an RMW surface. A composed surface needs nothing like this: its
	// last_render sidecar already is the record.
	Reconcile bool `json:"reconcile,omitempty"`

	// Tombstone emits an RFC-7386 null at To instead of a table, DELETING whatever a
	// lower layer put there.
	//
	// Distinct from OmitEmpty, and conflating them is the mistake this pair exists to
	// prevent: omitting a key leaves a host-provided block intact, while a tombstone
	// removes it. claude's settings.json needs the removal (mcpServers belongs in
	// .claude.json, so a host settings.json carrying one must be stripped); opencode
	// needs the omission. With Tombstone set, From is unused.
	Tombstone bool `json:"tombstone,omitempty"`
}

// Flag is one conditional key assertion. See Computed.Flags.
type Flag struct {
	// Key is the key emitted under the enclosing declaration's To.
	Key string `json:"key"`
	// Value is emitted when the condition holds. A false condition emits null.
	Value any `json:"value"`
	// WhenPresent names an entry in the source table; the flag holds when it exists.
	WhenPresent string `json:"whenPresent,omitempty"`
	// WhenAny holds when the source table has ANY entry. Mutually exclusive with
	// WhenPresent.
	WhenAny bool `json:"whenAny,omitempty"`
}

// Validate reports every structural problem in a computed declaration.
func (c Computed) Validate() []string {
	var problems []string
	if c.Tombstone {
		if c.To == "" {
			problems = append(problems, "computed: tombstone needs a \"to\" (there is no root to delete)")
		}
		if c.From != "" {
			problems = append(problems, fmt.Sprintf(
				"computed: tombstone must not set \"from\" (got %q) — it deletes a key rather than "+
					"deriving one", c.From))
		}
		if c.Project != nil {
			problems = append(problems, "computed: tombstone must not set \"project\"")
		}
		return problems
	}
	if c.From == "" {
		problems = append(problems, "computed: missing \"from\"")
	} else if !knownComputedSources[c.From] {
		problems = append(problems, fmt.Sprintf(
			"computed: unknown source %q (expected %s, %s)", c.From,
			SourceMCPServers, SourceLSPServers))
	}
	if c.Project != nil {
		for _, prob := range c.Project.Validate() {
			problems = append(problems, "computed: "+prob)
		}
	}
	if len(c.Flags) > 0 {
		if c.To == "" {
			problems = append(problems, "computed: flags need a \"to\" to nest under")
		}
		if c.Project != nil {
			problems = append(problems, "computed: flags and project are alternatives — "+
				"flags assert keys of their own, project reshapes the table's entries")
		}
	}
	for i, f := range c.Flags {
		if f.Key == "" {
			problems = append(problems, fmt.Sprintf("computed: flags[%d]: missing \"key\"", i))
		}
		switch {
		case f.WhenPresent != "" && f.WhenAny:
			problems = append(problems, fmt.Sprintf(
				"computed: flags[%d]: whenPresent and whenAny are mutually exclusive", i))
		case f.WhenPresent == "" && !f.WhenAny:
			problems = append(problems, fmt.Sprintf(
				"computed: flags[%d]: needs whenPresent or whenAny (an unconditional key "+
					"belongs in defaults or managed)", i))
		}
	}
	return problems
}

// BuildComputed assembles a surface's computed layer from its declarations.
//
// tables supplies the live data, keyed by source name — core passes what it loaded, so
// this function stays free of any config or filesystem dependency and is directly
// testable. A source a caller did not supply is treated as EMPTY rather than an error:
// on macos-user, or in a jail with no MCP configured, "the table is absent" and "the
// table is empty" describe the same world, and a declaration must behave the same in
// both or a surface would render differently for a reason the user cannot see.
func BuildComputed(decls []Computed, tables map[string]map[string]any) (map[string]any, error) {
	if len(decls) == 0 {
		return nil, nil
	}
	out := map[string]any{}
	for _, c := range decls {
		if c.Tombstone {
			out[c.To] = nil
			continue
		}
		table := tables[c.From]
		if len(c.Flags) > 0 {
			flags := map[string]any{}
			for _, f := range c.Flags {
				if flagHolds(f, table) {
					flags[f.Key] = f.Value
				} else {
					// Tombstone, not omission — see Computed.Flags.
					flags[f.Key] = nil
				}
			}
			out[c.To] = flags
			continue
		}
		if len(table) == 0 && c.OmitEmpty {
			continue
		}
		projected := table
		if c.Project != nil {
			var err error
			projected, err = c.Project.Apply(table)
			if err != nil {
				return nil, fmt.Errorf("computed %s: %w", c.From, err)
			}
		}
		if projected == nil {
			projected = map[string]any{}
		}
		if c.To == "" {
			// Root merge: the table's entries ARE this surface's top-level keys. A
			// collision with another declaration is a pack bug, so it is loud.
			for k, v := range projected {
				if _, dup := out[k]; dup {
					return nil, fmt.Errorf(
						"computed %s: key %q already set by an earlier declaration", c.From, k)
				}
				out[k] = v
			}
			continue
		}
		out[c.To] = projected
	}
	return out, nil
}

// flagHolds evaluates one flag's condition against the source table.
func flagHolds(f Flag, table map[string]any) bool {
	if f.WhenAny {
		return len(table) > 0
	}
	_, present := table[f.WhenPresent]
	return present
}
