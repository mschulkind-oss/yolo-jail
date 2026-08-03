# Handoff — a pack cannot install Claude MCP servers on the host

**Audience:** an agent working in the yolo-jail repo.
**Found:** 2026-08-02, verified against `0.7.1+362.g95c9416`.
**Requester's goal:** *"I want to make sure we can install claude mcp servers on the host
from a pack, and do in fact."* Today that is **not possible** — one refusal blocks the
whole surface.

Follow-up to [`handoff-pack-host-management-gaps.md`](handoff-pack-host-management-gaps.md)
(all five gaps there are now closed). This is the next one, found while planning the
adoption.

---

## The gap in one screen

Claude Code keeps **user-scope MCP servers** in `~/.claude.json` under a top-level
`mcpServers` key — the `claude/config` surface, not `claude/settings`. Verified on a real
host:

```console
$ python3 -c "import json; print(json.load(open('~/.claude.json'))['mcpServers'])"
{'tavily': {'type': 'http', 'url': 'https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}'}}
```

(Per-project `projects.<path>.mcpServers` also exists and is correctly out of scope for a
host render — it is workspace-keyed by nature.)

A pack declaring exactly that key via an overlay lints fine and is refused at apply:

```console
$ yolo pack lint /tmp/mcp/pack
✓ pack ok — 2 file(s) stage
declares 1 claim(s):
  config-overlay claude/config  contributes keys (owner still wins)

$ HOME=/tmp/mcp … yolo apply --host --assert
  claude/config        refused: uses ${workspace}, which has no referent on the host
  claude/settings      rendered
```

So `mcpServers` — which has **nothing to do with `${workspace}`** — is unreachable on the
host because a *different, unrelated* key on the same surface is workspace-keyed.

## Root cause

`usesWorkspacePlaceholder` (`internal/entrypoint/hostrender.go:239`) is a **surface-level**
predicate:

```go
func usesWorkspacePlaceholder(s manifest.Surface) bool {
	a := agentcfg.SubstituteWorkspace(s, "\x00A")
	b := agentcfg.SubstituteWorkspace(s, "\x00B")
	return !reflect.DeepEqual(a.Managed, b.Managed) || !reflect.DeepEqual(a.Defaults, b.Defaults)
}
```

`RenderHostPack` refuses the whole surface when it returns true. And the builtin `claude`
pack's `config` surface uses `${workspace}` in **both** layers:

```jsonc
{ "name": "config", "path": "~/.claude.json", "mode": "rmw",
  "defaults": { "projects": { "${workspace}": { "hasTrustDialogAccepted": true } } },
  "managed":  { "projects": { "${workspace}": { "enableAllProjectMcpServers": true } } } }
```

Those two keys are genuinely jail-only (pre-accepting trust for the workspace being
mounted). But they are the *only* `${workspace}` users on the surface, and they make
`~/.claude.json` entirely off-limits at the host notch — including any overlay a user's own
pack contributes to it.

The refusal is **correct in intent, too coarse in granularity.**

## What to build

**Prune the workspace-keyed subtree instead of refusing the surface.** At a host target,
drop the branches whose *key* is (or contains) `${workspace}` and render what remains. If
nothing remains, *then* report the surface as skipped — with a reason naming the pruned
keys, not a bare "uses `${workspace}`".

Sketch:

- Replace the boolean predicate with a `pruneWorkspaceKeyed(s) (Surface, []string)` that
  returns the surface minus workspace-keyed branches, plus the dotted paths it removed.
- In `RenderHostPack`: prune, then
  - if the pruned surface has no `Managed`/`Defaults` content → `skipped: only
    ${workspace}-keyed keys, which have no host referent (projects.${workspace}.*)`
  - else → render, and **report the pruned keys** so it is never silent:
    ```
    claude/config     rendered  /home/me/.claude.json
      skipped ${workspace}-keyed: projects.${workspace}.hasTrustDialogAccepted,
                                  projects.${workspace}.enableAllProjectMcpServers
    ```

That is the same "never silent" discipline the G1 fix established for skills/briefing.

### Care required — `~/.claude.json` is not settings.json

1. **It is live agent state, not just config.** 40K, 32 top-level keys, 17 per-project
   entries on a real host — history, onboarding flags, per-project trust. RMW already only
   touches declared keys, but the blast radius of a bug here is much larger than
   `settings.json`. Worth an explicit test that an untouched 32-key file round-trips
   byte-identically apart from the asserted key.
2. **`mcpServers` is a dynamic managed *table*, and tables get regenerated wholesale.**
   `regenerateManagedTables` (`internal/entrypoint/prism.go:466`) clears and rewrites a
   managed table's block, on the documented rationale that a server present in the file but
   absent from the derived layer is either stale or user-added-through-the-UI.

   **Requester's ruling (2026-08-02): "if you manage mcpServers through yolo, you give up
   `claude mcp add`, that's fine."** That makes wholesale regeneration *correct* rather than
   destructive — yolo is the sole author, so an undeclared server is stale by definition. So
   merge-on-host is a **preference, not a requirement**. Two things still needed:
   - **Announce every drop** (`noteDroppedManagedEntries` already exists) — silent deletion
     is the failure mode even when replacement is the right policy.
   - **The FIRST apply must not eat an existing entry** before the user has declared it.
     Either warn-and-refuse on an undeclared pre-existing server, or make it loud enough in
     `observe` that the user declares it first. This is the one-way-door moment.

3. **Interpolation covers `env` values ONLY — not `url`, not `args`.** Verified:
   `interpolateEnv` (`internal/entrypoint/mcp.go:41`) is called only on `cfg.Get("env")`
   (`mcp.go:181`), so `${VAR}` anywhere else is left literal with no warning.

   This matters concretely. The requester's *jail* config uses the interpolating form and is
   correct today:
   ```jsonc
   "tavily": {"command": "npx", "args": ["-y", "tavily-mcp@latest"],
              "env": {"TAVILY_API_KEY": "${TAVILY_API_KEY}"}}     // ← expands ✓
   ```
   but their *host* `~/.claude.json` entry embeds the key in the URL:
   ```jsonc
   "tavily": {"type": "http",
              "url": "https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}"}  // ← literal ✗
   ```
   A pack contributing the second form would write `${TAVILY_API_KEY}` verbatim and the
   server would silently 401. **Decide:** either extend interpolation to `url` (and warn on
   unresolved, as `interpolateEnv` does), or document that a host MCP entry must use the
   `command`+`env` form. Extending `url` is preferable — the `http` transport is otherwise
   unusable with any secret. Either way the key itself must keep coming from
   `env_sources` (an untracked file), never from pack content: a pack's `env` kind is
   static-strings-only by design and must not become a secret carrier.

## Acceptance

- A pack contributing `mcpServers` via `config-overlay` on `claude/config` **renders on the
  host**, and `claude mcp list` shows the server.
- The `projects.${workspace}.*` keys are pruned and **named in the output**, not silently
  dropped and not fatal.
- A pre-existing hand-added MCP server is **never silently** destroyed: dropping it is
  acceptable policy (see §2), but the drop must be reported, and the first-ever apply must
  make it obvious before it happens.
- A `${VAR}` in a server's `url` either expands or produces a warning — never a silent
  literal (§3).
- Second `--assert` byte-identical; unrelated keys in a 32-key `~/.claude.json` untouched.

## Also worth deciding

`copilot/mcp`, `agy/mcp`, and `opencode/config` carry MCP tables too and already render at
the host (they are not workspace-keyed). So Claude is the odd one out purely because of
where Claude Code chose to store user MCP config. Once this lands, "install MCP servers on
the host from a pack" is uniform across agents — which is the real goal.

## Reproduction

```console
$ mkdir -p /tmp/mcp/pack /tmp/mcp/.config/yolo-jail
$ printf '# stub\n' > /tmp/mcp/pack/AGENTS.md
$ cat > /tmp/mcp/pack/pack.json <<'EOF'
{ "name": "matt-mcp", "contributes": [
  { "kind": "config-overlay", "surface": "claude/config",
    "config": { "managed": { "mcpServers": {
      "tavily": { "type": "http", "url": "https://example/mcp" } } } } } ] }
EOF
$ echo '{ "packs": ["claude", "file:///tmp/mcp/pack"] }' > /tmp/mcp/.config/yolo-jail/config.jsonc
$ yolo pack lint /tmp/mcp/pack                                    # ✓ ok
$ HOME=/tmp/mcp XDG_CONFIG_HOME=/tmp/mcp/.config yolo apply --host --assert
  claude/config   refused: uses ${workspace}, which has no referent on the host   # ← the gap
```

Relevant code: `internal/entrypoint/hostrender.go:239` (predicate), same file's
`RenderHostPack` loop (refusal), `internal/entrypoint/prism.go:466`
(`regenerateManagedTables`), `packs/claude/pack.json` (the `${workspace}` keys).
