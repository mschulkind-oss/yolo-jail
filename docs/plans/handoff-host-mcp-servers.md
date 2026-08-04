# Handoff — a pack cannot install Claude MCP servers on the host

> ## ✅ FIXED — re-verified 2026-08-03 against `0.7.1+380.ga3d6d4e`
>
> Shipped in `4d1aa68` ("a pack can install MCP servers on the host"), implemented exactly as
> §"What to build" recommended: **prune the workspace-keyed subtree, name what was pruned,
> render the rest.** Verified end-to-end — a `config-overlay` on `claude/config` carrying
> `mcpServers` now renders:
>
> ```console
> $ HOME=/tmp/m2 … yolo apply --host --assert
>   claude/config        rendered  /tmp/m2/.claude.json
>     skipped ${workspace}-keyed (no host referent): projects.${workspace}.enableAllProjectMcpServers,
>                                                    projects.${workspace}.hasTrustDialogAccepted
> ```
>
> Both hazards from §"Care required" are handled, and better than asked:
>
> - **Wholesale table regeneration now warns, in `observe` AND `assert`**, naming the entry
>   and the remedy:
>   `⚠ would damage your existing entry: mcpServers.my-hand-added (dropped — not in your
>   config) (yolo owns this table; declare the entry under `mcp_servers` to keep it)`
> - **The `${VAR}` warning is WRONG for the `env` block — see [§The `${VAR}` warning
>   misdiagnoses the `env` case](#the-var-warning-misdiagnoses-the-env-case) below.** The
>   *mechanism* (host render does not resolve variables) is right; the *message* calls the
>   correct outcome a hazard and recommends inlining a live credential.
> - Unrelated keys in the file are untouched (verified with a sentinel key).
>
> **One new asymmetry found while verifying — see [§Dangling state on pack
> drop](#dangling-state-on-pack-drop) at the bottom.**

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
  literal (§3). **But a `${VAR}` inside an `env` value must NOT warn** — the literal is
  correct there, because Claude Code resolves it at launch (see the `${VAR}` section below).
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

---

## The `${VAR}` warning misdiagnoses the `env` case

**New finding, 2026-08-03.** `apply --host` prints, for an `mcpServers` entry whose `env`
value contains `${VAR}`:

```
⚠ ${TAVILY_API_KEY} written LITERALLY — apply --host does not resolve variables; put the
  value in the file directly, or manage this server in the jail, where `env_sources` expands it
```

**Claude Code expands `${VAR}` in `mcpServers.*.env` itself, at server-launch time.** So for
the `env` block the literal is not a hazard — it is the **correct and desired** file content,
and the warning's first remedy ("put the value in the file directly") recommends inlining a
live credential into a file a pack may carry.

Verified empirically (Claude Code 2.1.220): a `~/.claude.json` containing a literal
`"env": {"RESOLVED": "${MY_PROBE_VAR}"}`, with the MCP command pointed at a script that dumps
its environment:

```console
$ cat /tmp/envt2/captured.txt
RESOLVED=[SECRET_VALUE_123]        # ← the agent resolved it; the file kept the literal
```

### Why the two notches genuinely differ

The mechanism is right and should not change. What differs is who resolves, and the message
conflates them:

| notch | who resolves | correct file content |
|---|---|---|
| **jail** | **yolo** (`interpolateEnv`, `mcp.go:41`) — the jail never sourced the user's host env file, so a literal would reach the server unresolved | the resolved value |
| **host** | **Claude Code**, from the user's already-exported shell env (their `bashrc` sources `~/.config/claude/env`) | **the literal `${VAR}`** |

Same key, opposite correct behavior. The current message applies the jail's reasoning to the
host.

### Suggested fix

Scope the warning by **position**, not by presence of `${…}`:

- **inside an `env` value** → not a warning at all. At most an informational line:
  `${TAVILY_API_KEY} left literal — Claude Code resolves it from your environment at launch`.
  Better still: say nothing, since this is the intended shape.
- **anywhere else** (`url`, `args`, `command`) → keep warning, because Claude Code's expansion
  *is* `env`-specific and a `${VAR}` in a `url` really is inert. This is almost certainly the
  case that motivated the warning; it was over-generalized to all positions.
- **Drop "put the value in the file directly" as the lead remedy** regardless. For a secret it
  is the harmful advice, and it is what a user will copy-paste. If a literal value genuinely
  is wanted, the user does not need to be told how.

Downstream effect of getting this right: a pack can carry `"env": {"TAVILY_API_KEY":
"${TAVILY_API_KEY}"}` and be **fully shareable and trackable** — it holds a *reference*, never
a credential — with the secret staying in an untracked host file. That is the whole point of
the indirection, and today's warning steers users away from it.

---

## Dangling state on pack drop

**New finding, 2026-08-03 (`0.7.1+380`).** Not part of the MCP work — found while verifying
it. `dc16f35` ("prune a dropped pack's staged tree, so it stops rendering") fixed the *jail*
side and the host **briefing**, but the host cleanup is **briefing-only**. Drop a pack from
`packs` and re-apply:

```console
$ HOME=/tmp/rq2 … yolo apply --host --assert       # pack removed from config
  pack/briefing        removed (pack no longer configured)  /tmp/rq2/.claude/CLAUDE.md   ✓

$ ls /tmp/rq2/.claude/bin/
file-suggestion.sh                                  ← files tree ORPHANED, never mentioned

$ python3 -c "…settings.json…"
fileSuggestion: {"command": "~/.claude/bin/file-suggestion.sh", "type": "command"}
                                                    ← overlay key STILL ASSERTED
```

Re-running never mentions either. So briefing is cleaned and announced, while a `files` tree
and a `config-overlay` key are left behind **silently**.

The overlay key is the sharper half: `fileSuggestion` survives pointing at a script that a
later `rm -rf` of the pack dir would remove, leaving Claude Code with a broken
`type: command` hook. That is exactly the failure mode `~/.dotfiles`' orphaned
`file-suggestion.sh` had before this work (noted in
[`handoff-fzf-pack-adoption.md`](handoff-fzf-pack-adoption.md) §1).

The existing cleanup lives at `internal/entrypoint/hostbriefing.go:173` (`res.Action += "
(pack no longer configured)"`) — there is no equivalent for the `files` or overlay paths.

**Decision needed, not obviously "delete both."** Symmetry with the guide's stated contract
(*"undo yolo's management is: stop declaring the key and re-apply, which drops it"*) argues
for removing them. But a host `files` tree may hold something the user has since come to
depend on, and silently deleting from a real `$HOME` is the hazard this whole workstream has
been careful about. Suggested split:

- **overlay/managed keys** — drop them (they are pure assertion, and the guide already
  promises this), and *report* the drop like briefing does;
- **`files` trees** — do **not** delete; report them as orphaned, once, with the path, so the
  user can remove them deliberately. `apply --host` already has the vocabulary for this
  (`⚠ would damage your existing entry: …`).

Either way the rule this workstream keeps arriving at applies: **never silent.**

---

## Two nits found while adopting the packs (2026-08-03)

Found building three real personal packs and applying them to a real host.

1. **`apply --host` in OBSERVE mode reports skills as `rendered`, not `would render`.**
   Every other kind uses the future tense in a dry-run (`would render`, `⚠ would
   overwrite`); per-skill lines say `skills  agent-standards  rendered  invoke as
   /agent-standards`. Verified it writes nothing — the destination stayed absent — so this
   is cosmetic, but it reads as though observe mutated the home, which is exactly the fear
   a dry-run exists to allay.

2. **`yolo pack footprint` has no `--allow-exec`.** `pack lint --allow-exec <dir>` accepts
   a pack shipping an executable, but `pack footprint <dir>` on the same pack exits 1 with
   the exec-bit refusal, so a pack you *can* lint you cannot inspect. `lint` already prints
   the footprint, so the workaround is easy — but the flag asymmetry is surprising, and
   `footprint`'s docstring advertises it as the way to inspect a pack you are authoring.
