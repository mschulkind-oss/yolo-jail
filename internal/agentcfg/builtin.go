package agentcfg

import "github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"

// builtin.go holds the yolo-shipped surface manifests — the per-agent
// "defaults + managed + path + codec" data of docs/plans/agent-settings-composition.md
// §1.1 / §6.5 ①. These are Go-declared (the manifest package documents why:
// defaults/managed carry arbitrary decoded values best expressed as literals,
// and a leaf library needs no file I/O). Phase B lands them one agent at a time;
// pi is the proof-of-concept surface (§Config-composition build in the roadmap).
//
// A surface's Path uses a leading "~/" that the CLI expands to the jail home;
// this package stays path-policy-free (it never touches the filesystem).

// piSettings is the pi settings surface (§6.5 ①): the host mirrors
// ~/.pi/agent/settings.json, yolo defaults theme to "system", and the jail
// enforces defaultProjectTrust=always (the container is the trust boundary, so
// pi should not re-prompt).
var piSettings = manifest.Surface{
	Agent:    "pi",
	Name:     "settings",
	Path:     "~/.pi/agent/settings.json",
	Codec:    "json",
	Defaults: map[string]any{"theme": "system"},
	Managed:  map[string]any{"defaultProjectTrust": "always"},
}

// claudeSettings is claude's settings.json surface — the widest surface (§ table
// row "Claude settings.json (+ .claude.json)"). It faithfully models the STATIC
// force-managed keys the bespoke generator (internal/entrypoint/claude.go
// ConfigureClaude) unconditionally .Set()s after the host three-way merge:
//
//   - permissions: allow=[] deny=[] defaultMode=acceptEdits
//     additionalDirectories=["/"]   (the YOLO posture; see the AGENTS.md note
//     "settings.json sets permissions.allow to [] and defaultMode acceptEdits")
//   - skipDangerousModePermissionPrompt=true
//   - preferences.autoUpdaterStatus="disabled"
//
// FIDELITY GAPS (documented, not faked — the manifest model / current engine
// cannot yet express these, so they are deliberately omitted rather than
// misrepresented):
//
//  1. (CLOSED) Subtree preservation. luahook.Ctx.Enforce now DEEP-merges: a
//     managed nested object merges key-by-key into the existing
//     "permissions"/"preferences" object, so host siblings survive (e.g. a host
//     permissions.ask is kept while yolo forces permissions.allow) — matching
//     the bespoke setDefaultMap+.Set behavior. This was formerly a shallow-set
//     clobber gap; the deep Enforce closed it with no manifest change.
//  2. mcpServers deletion is inexpressible. ConfigureClaude does
//     settings.Delete("mcpServers") to strip a host-provided block. The managed
//     layer can only SET (a managed null would merge in as a literal null, which
//     is WRONG — worse than omitting), so this removal is NOT modeled here. It
//     belongs to the dedicated MCP surface (mcp.go, a separate Phase B item).
//  3. Dynamic (computed, not static) keys are omitted: enabledPlugins.* is
//     derived from which LSP servers are present, and env.ENABLE_LSP_TOOL is
//     "1" iff any LSP server is configured (else the key/env block is pruned).
//     Both depend on runtime LSP config, not fixed data — they belong to the LSP
//     surface, not a static managed literal.
var claudeSettings = manifest.Surface{
	Agent: "claude",
	Name:  "settings",
	Path:  "~/.claude/settings.json",
	Codec: "json",
	Managed: map[string]any{
		"permissions": map[string]any{
			"allow":                 []any{},
			"deny":                  []any{},
			"defaultMode":           "acceptEdits",
			"additionalDirectories": []any{"/"},
		},
		"skipDangerousModePermissionPrompt": true,
		"preferences": map[string]any{
			"autoUpdaterStatus": "disabled",
		},
	},
}

// claudeConfig is claude's .claude.json surface (§ table row: the second Claude
// surface, "config"). The bespoke ConfigureClaude, after loading the file:
//
//   - FORCES projects["/workspace"].enableAllProjectMcpServers=true (.Set) —
//     modeled here as Managed.
//   - DEFAULTS projects["/workspace"].hasTrustDialogAccepted=true (setDefault,
//     user-overridable) — modeled here as Defaults.
//
// FIDELITY GAPS:
//
//  1. (CLOSED) Nested default+managed on the SAME object now coexist: Defaults
//     deep-merges projects["/workspace"].hasTrustDialogAccepted=true, and the
//     deep-merge Enforce then merges the managed
//     projects["/workspace"].enableAllProjectMcpServers=true into the SAME
//     sub-object rather than replacing "projects" — so both survive, matching
//     the bespoke force-one/setDefault-other behavior on the live sub-map.
//  2. mcpServers is DYNAMIC: ConfigureClaude reconciles it from LoadMCPServers()
//     minus the managed-MCP sidecar. Not static data — omitted here; it is the
//     MCP surface's job (mcp.go, separate Phase B item).
var claudeConfig = manifest.Surface{
	Agent: "claude",
	Name:  "config",
	Path:  "~/.claude.json",
	Codec: "json",
	Defaults: map[string]any{
		"projects": map[string]any{
			"/workspace": map[string]any{
				"hasTrustDialogAccepted": true,
			},
		},
	},
	Managed: map[string]any{
		"projects": map[string]any{
			"/workspace": map[string]any{
				"enableAllProjectMcpServers": true,
			},
		},
	},
}

// geminiSettings is gemini's settings.json surface (§ table row "Copilot /
// Gemini / opencode / pi / Codex settings"). It faithfully models the STATIC
// force-sets ConfigureGemini (internal/entrypoint/agent_configs.go) applies
// after loading ~/.gemini/settings.json — and, crucially, it splits them into
// the CORRECT layer per §7's flagged subtlety:
//
//   - security.approvalMode="yolo" and security.enablePermanentToolApproval=true
//     are written with setDefault (a host value silently wins). They are
//     USER-OVERRIDABLE DEFAULTS, so they live in the Defaults layer. §7 calls
//     this out as a latent security-posture bug ("Gemini using setdefault … a
//     user value silently disables the intended YOLO default"); the manifest
//     preserves the bespoke behavior faithfully rather than silently promoting
//     it to managed. Promoting the security posture to managed is a deliberate
//     policy change, not a port, and is out of scope here.
//   - general.enableAutoUpdate=false and general.enableAutoUpdateNotification=
//     false are written with .Set on the general sub-map (they overwrite any
//     host value). They are FORCE-MANAGED, so they live in the Managed layer.
//
// FIDELITY GAPS (documented, not faked):
//
//  1. (CLOSED) Subtree preservation (same as claude). The deep-merge Enforce
//     merges the managed "general" object key-by-key into the existing general
//     object, so host siblings under general survive — matching the bespoke
//     setDefaultMap+.Set on the live sub-map. Formerly a shallow-clobber gap.
//  2. mcpServers is DYNAMIC: ConfigureGemini reconciles it from LoadMCPServers()
//     plus LSP-as-MCP wrappers, minus the managed sidecar. Not static data —
//     omitted here; it belongs to the dedicated MCP surface (mcp.go, a separate
//     Phase B item), exactly as for claude.
var geminiSettings = manifest.Surface{
	Agent: "gemini",
	Name:  "settings",
	Path:  "~/.gemini/settings.json",
	Codec: "json",
	Defaults: map[string]any{
		"security": map[string]any{
			"approvalMode":                "yolo",
			"enablePermanentToolApproval": true,
		},
	},
	Managed: map[string]any{
		"general": map[string]any{
			"enableAutoUpdate":             false,
			"enableAutoUpdateNotification": false,
		},
	},
}

// copilotConfig is copilot's config.json surface (§ table row "Copilot / Gemini
// / opencode / pi / Codex settings"). The bespoke ConfigureCopilot
// (internal/entrypoint/agent_configs.go) writes the LITERAL string
// {"yolo": true}\n to ~/.copilot/config.json — but ONLY when the file is
// missing. Write-if-absent is exactly DEFAULT-layer semantics (a host value
// silently wins, yolo never clobbers), so `yolo: true` lives in the Defaults
// layer, not Managed. It is deliberately NOT force-managed: the bespoke code
// never overwrites an existing config.json.
//
// FIDELITY GAPS (documented, not faked):
//
//  1. File-level vs key-level write-if-absent. The bespoke code keys off the
//     EXISTENCE OF THE FILE: if ~/.copilot/config.json exists at all — even as
//     {"someOtherKey": 1} with no "yolo" key — it is left untouched, so "yolo"
//     is NOT added. The manifest default is key-level: composed against a host
//     config.json that lacks "yolo", it ADDS yolo:true (the default fills the
//     absent key). The two agree in the two cases that actually occur — no host
//     file (default yolo:true lands) and a host file that already sets yolo
//     (host wins) — and diverge only for a host config.json that exists yet
//     omits yolo, which the bespoke corpus does not produce (yolo owns this
//     file). Faithful for every real input; the edge is recorded, not faked.
//  2. Exact-byte serialization differs. ConfigureCopilot emits the hand-written
//     literal {"yolo": true}\n (compact, one line). The json codec re-encodes as
//     sorted-key, 2-space-indented JSON ({\n  "yolo": true\n}\n). Same decoded
//     value, different bytes — a formatting-only gap inherent to routing the
//     surface through the shared codec, and harmless (copilot re-reads it as
//     JSON).
//  3. mcp-config.json and lsp-config.json are DYNAMIC: ConfigureCopilot rebuilds
//     them from LoadMCPServers()/LoadLSPServers() on every boot. Not static data
//     — omitted here; they belong to the dedicated MCP and LSP surfaces
//     (separate Phase B items), exactly as for claude and gemini.
var copilotConfig = manifest.Surface{
	Agent:    "copilot",
	Name:     "config",
	Path:     "~/.copilot/config.json",
	Codec:    "json",
	Defaults: map[string]any{"yolo": true},
}

// opencodeConfig is opencode's opencode.json surface (§ table row "Copilot /
// Gemini / opencode / pi / Codex settings"). The bespoke ConfigureOpencode
// (internal/entrypoint/agent_configs.go), after loading the file:
//
//   - DEFAULTS $schema="https://opencode.ai/config.json" (setDefault, a host
//     value silently wins) — modeled here as Defaults.
//   - FORCES permission="allow" (.Set, always overwrites) — modeled here as
//     Managed. This is the YOLO posture: opencode never re-prompts.
//
// FIDELITY GAPS (documented, not faked — the manifest model / current engine
// cannot yet express these, so they are deliberately omitted rather than
// misrepresented):
//
//  1. The "mcp" block is a DYNAMIC transform, not static data. ConfigureOpencode
//     reads e.LoadMCPServers() and TRANSLATES each shared MCP server into
//     opencode's NATIVE schema (an object of {type:"local", command:[cmd,...args],
//     enabled:true, environment:{...}} per server), reconciles it against a
//     yolo-managed sidecar (yolo-managed-mcp-servers.json — prunes previously
//     managed names, then re-adds the freshly translated set), and DELETES the
//     "mcp" key entirely when the result is empty. None of that is fixed literal
//     data: it is a schema-translating, list-reconciling computation over runtime
//     MCP config. It is therefore NOT baked into this manifest's Defaults/Managed.
//     This is exactly the kind of concern a Lua transform (or a dedicated MCP
//     surface, cf. claude/gemini/copilot above) handles in a later Phase B step;
//     the static surface models only $schema + permission, and the MCP
//     translation + sidecar reconciliation is left to that transform-shaped step.
//  2. Sidecar side effect is inexpressible. Beyond opencode.json itself, the
//     bespoke code WRITES a second file (yolo-managed-mcp-servers.json) recording
//     which MCP names yolo owns. The manifest models a single surface's content,
//     not companion-file emission; that side effect belongs to the same MCP
//     transform/surface as gap 1.
var opencodeConfig = manifest.Surface{
	Agent:    "opencode",
	Name:     "config",
	Path:     "~/.config/opencode/opencode.json",
	Codec:    "json",
	Defaults: map[string]any{"$schema": "https://opencode.ai/config.json"},
	Managed:  map[string]any{"permission": "allow"},
}

// codexConfig is codex's config.toml surface (§ table row "Copilot / Gemini /
// opencode / pi / Codex settings") — the one TOML-codec surface, so it also
// exercises the toml codec (internal/agentcfg/codec/toml.go) end to end. The
// bespoke ConfigureCodex (internal/entrypoint/codex.go), after decoding the
// existing config.toml with tomlx.DecodeOrdered, FORCES two top-level scalars
// via .Set (always overwrites) — modeled here as Managed:
//
//   - approval_policy   = "never"                (never re-prompt for approval)
//   - sandbox_mode      = "danger-full-access"   (the YOLO posture: the
//     container is the sandbox, so codex must not add its own)
//
// Both are plain top-level TOML strings — the exact shape the codec's
// deterministic subset emitter round-trips cleanly (scalar `k = "v"` lines,
// sorted keys). There are NO default (setDefault) keys in ConfigureCodex, so
// the Defaults layer is empty.
//
// FIDELITY GAPS (documented, not faked):
//
//  1. mcp_servers is DYNAMIC and OMITTED (as for claude/gemini/copilot/opencode).
//     ConfigureCodex translates e.LoadMCPServers() into codex's TOML table shape
//     ([mcp_servers.<name>] sub-tables), reconciles them against the
//     yolo-managed-mcp-servers.json sidecar, and deletes the whole key when
//     empty. That is a schema-translating, list-reconciling computation over
//     runtime MCP config, not static data — it belongs to the dedicated MCP
//     surface / Lua transform (a separate Phase B item), not this static surface.
//  2. CODEC-EXTENSION FOLLOW-UP — inline tables are unsupported by the codec's
//     deterministic emitter. The bespoke dumpCodexTOML emits a per-server `env`
//     block as an INLINE table (`env = { A = "1", B = "2" }`), and the whole
//     [mcp_servers.<name>] header is a sub-table nested under mcp_servers. The
//     codec (codec/toml.go) round-trips nested tables as `[a.b]` headers and
//     arrays-of-tables as `[[a]]`, but it has NO inline-table output — a nested
//     map always emits as a `[table.sub]` header, never `{ ... }`. So the moment
//     the dynamic mcp_servers block (gap 1) is routed through this codec, an
//     env-bearing server would render as a `[mcp_servers.<name>.env]` sub-table
//     rather than codex's inline form. Both decode to the same value and codex
//     reads either, so this is a byte-shape (not semantic) gap; it is recorded
//     as the codec-extension the codec worker already flagged (inline tables /
//     datetimes / mixed arrays), NOT faked here. The STATIC surface below uses
//     none of those features, so it round-trips exactly.
//
// The Sidecar side effect (yolo-managed-mcp-servers.json) is inexpressible in a
// single-surface manifest, exactly as noted for gemini/opencode; it rides along
// with the same MCP transform as gap 1.
var codexConfig = manifest.Surface{
	Agent: "codex",
	Name:  "config",
	Path:  "~/.codex/config.toml",
	Codec: "toml",
	Managed: map[string]any{
		"approval_policy": "never",
		"sandbox_mode":    "danger-full-access",
	},
}

// BuiltinManifest returns the yolo-shipped manifest of all surfaces yolo knows
// how to compose. Phase B grows this list (mcp, lsp, mise remain); it
// currently carries pi, claude (settings + config), gemini, copilot, opencode,
// and codex. It panics on
// a malformed builtin (a programming error in this file, caught by tests), never
// at runtime for user input.
func BuiltinManifest() *manifest.Manifest {
	m, err := manifest.New(
		piSettings,
		claudeSettings,
		claudeConfig,
		geminiSettings,
		copilotConfig,
		opencodeConfig,
		codexConfig,
	)
	if err != nil {
		panic("agentcfg: malformed builtin manifest: " + err.Error())
	}
	return m
}
