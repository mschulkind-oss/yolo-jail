# Generated-config composition — layered regeneration + Lua transforms

**Status:** Design of record — **FINALIZED 2026-07-20** (all §9 questions
resolved). Supersedes the exploratory RFC that carried a menu of models and a
data-filter vocabulary — this is the line in the sand. **Per-phase status:**
**Phase A complete** — the engine is built + tested (`internal/agentcfg`, with
`compose.go`/`engine.go`/`manifest`/`codec`/`luahook` and their tests). **Phase
B complete** — all **agent** surfaces are in the manifest and reachable via
`yolo config render` (the non-agent surfaces — mise, standalone MCP/LSP, git/jj
identity — are *not* yet manifest-modeled; `yolo config render mise` reports "no
surfaces", and only `pi/claude/gemini/copilot/opencode/codex/agy` are known).
**Phase C complete (2026-07-22)** — every surface renders through the
prism at boot: `internal/entrypoint`'s `Configure*Prism` functions are the sole
config path (`boot.go` calls them unconditionally; the `YOLO_PRISM_SURFACES`
cutover gate is retired), the six bespoke `Configure*` writers and their
dead helpers are deleted, and `agy` was born directly on the prism. The obsolete
snapshot/managed-MCP sidecars are cleaned up on each surface's first-migration
boot. Remaining non-agent surfaces (mise, MCP/LSP standalone, git/jj identity)
still have bespoke generators; folding them onto the prism is tracked separately
in §8 / [ROADMAP.md](ROADMAP.md).

yolo generates a number of config files inside the jail from host + jail sources
— coding-agent settings (Claude's `settings.json`, Codex's `config.toml`, pi's
`settings.json`, …), but **also** the MCP-server config, LSP config, the global
mise config, and git/jj identity. This doc fixes **how** any such generated
config composes and how a user reshapes it. Agent config is the motivating and
widest case; the model is deliberately generic over **every file yolo generates
this way** (see §1.1 for the inventory).

---

## 1. The decision, in one paragraph

Each generated config is a **build product yolo regenerates every boot** from an
ordered stack of layers. yolo writes it only into the jail **user scope** (never
the host, never the workspace). A user reshapes what crosses into the jail with a
**Lua transform** — one sandboxed function per **surface** (a surface = one file
yolo generates) that receives the composed config as a decoded value and returns
the transformed one. Lua is the *only*
transform mechanism: it is format-agnostic (the config need not be JSON),
Turing-complete enough to express any redaction without yolo growing a
vocabulary, and it operates on yolo's generated artifact, so **it never requires
modifying — or even being able to write — the source config.** In-jail edits
survive regeneration via a capture-diff overlay, and `yolo config render` runs
the whole pipeline offline so you can see and diff the result without a jail.

### 1.1 What yolo generates this way (the surfaces)

The pipeline applies to every file yolo composes from host/config sources. From
the current entrypoint (`internal/entrypoint`), these are:

| Surface | Generator | Codec | Composes from |
|---|---|---|---|
| Claude `settings.json` (+ `.claude.json`) | `claude.go` | json | host `~/.claude` + config + managed |
| Copilot / Gemini / opencode / pi / Codex settings | `agent_configs.go`, `codex.go` | json / **toml** (Codex) | host files + config |
| **MCP servers** (per-agent config) | `mcp.go` | json | `mcp_servers` + `mcp_presets` (config) + builtin presets |
| **LSP servers** | `mcp.go` (`LoadLSPServers`) / `agent_configs.go` | json | `lsp_servers` (config) + defaults |
| **Global mise config** (`~/.config/mise/config.toml`) | `mise.go` | **toml** | `mise_tools` (config) + `miseBaseTools` defaults |
| **git / jj identity** | `identity.go` | (git config kv) | `YOLO_GIT_*` / `YOLO_JJ_*` env |

All of these are *composed from user-influenceable input* and are the pipeline's
domain. **Not** in scope (yolo-authored artifacts with no user-config layer to
compose or redact — they are generated *code/data*, not composed *config*):
generated shell scripts (bashrc, shims, agent/pkg launchers, MCP node wrappers,
`yolo-cglimit`/`journalctl` helpers, bootstrap), and fixed system files (CA
bundle, `/etc/timezone`, PID files). A surface earns the pipeline when there's a
host or config layer to merge and a reason a user might want to reshape it;
otherwise it stays a plain generator. The manifest (§3.3) is where a surface is
declared, so widening coverage later is adding a manifest entry, not new
machinery.

**Corollary — trivial merges are the default, so special-cased "merge this config
key" plumbing retires.** Several current surfaces exist *only* to deep-merge a
defaults set with a config key: the global mise config merges `miseBaseTools`
(defaults) with `mise_tools` (config) via a bespoke `MergeMiseTools` +
`YOLO_MISE_TOOLS` env pathway; MCP/LSP do the same with `mcp_servers`/`lsp_servers`.
Under the prism these are **not** special cases: the config key *is* the
`workspace` layer, the builtin set *is* the `defaults` layer, and the engine's
deep-merge composes them — no per-key merge function, no dedicated env var. A Lua
transform is needed only when a surface wants *more* than a merge (drop a default,
rewrite a value); the plain "merge my config over the defaults" case needs zero
code beyond declaring the surface. So adopting the prism deletes those hand-rolled
merges rather than adding a parallel path.

## 2. Six principles (the line in the sand)

1. **Regenerate, don't reconcile.** Each generated config file (agent settings,
   MCP, LSP, mise, identity — §1.1) is rebuilt from sources on every boot
   by the engine, not edited in place. (For the agent-config surfaces this is now
   live: boot runs the `Configure*Prism` writers in `internal/entrypoint`, which
   compose through `agentcfg`. The remaining non-agent surfaces — mise, standalone
   MCP/LSP, identity — still use bespoke generators pending their fold-in; see the
   Phase-C status in the header.) Host-key removal needs zero
   memory: a dropped host key is simply absent from the next render. (This alone
   deletes today's snapshot/rollback three-way merge and its poison-on-typo
   failure — see §7.)
2. **yolo owns the user scope only.** Config lands under `/home/agent/…` (a
   per-workspace r/w overlay). yolo never writes an agent's *project*/workspace
   config; `/workspace` is the operating agent's and mirrors the host, except the
   enumerated `/dev/null` isolation shadows. This is the **config-ownership
   principle** in [../design/storage-and-config.md](../design/storage-and-config.md)
   §1.1 — the durable statement; this doc obeys it.
3. **Compose by layered deep-merge.** Sources stack in a fixed precedence order
   and deep-merge over the *decoded* structure (format-independent). No depth
   cliff.
4. **Transform with Lua, not a data vocabulary.** Redaction/reshaping is a Lua
   hook per surface — format-agnostic, no closed op-set to memorize, no silent
   unknown-key drops. §3.
5. **Never touch the source.** The transform runs on yolo's *composed output*, a
   build product yolo fully controls; the host file stays `:ro` and unmodified,
   and no assumption is made that yolo could rewrite it (it may be read-only, or
   a format yolo won't round-trip).
6. **In-jail edits survive** via a capture-diff overlay (§5), and the entire
   pipeline is runnable offline via `yolo config render` (§6) — which is also how
   the config-change safety prompt diffs a Lua transform's effect.

## 3. The Lua transform — the abstraction

### 3.1 Shape

The composition pipeline for one **surface** (one config file yolo generates):

```
decode(host, workspace, defaults)   →  tables            # per-surface codec (§3.3)
deepMerge(defaults, host, workspace, overlay)  →  merged  # §4 precedence
transform(merged, ctx)              →  shaped             # the Lua hook, §3.2
enforce(shaped, managed)            →  final              # managed keys win, applied AFTER Lua
encode(final)                       →  bytes → write to the jail user-scope path
```

The Lua hook sits between the merge and the managed-enforce step. It sees the
fully-composed config (defaults + host + jail-declared + captured in-jail edits)
and returns the version that should be written into the jail. The `managed` layer
(yolo's asserted keys) is re-applied *after* the hook, so a transform can't
silently drop yolo's keys from the *generated file* — it gets `ctx.managed`
read-only so it can see them, but yolo has the last write. This is a
composition-precedence guarantee, **not** the security boundary — the container +
the injected YOLO flag are (see §9); `managed` never becomes an OS-level file.

### 3.2 What the hook receives (a taste — full worked example in §6.5)

The user points `config_transform` at a Lua file (§3.4); it registers a function
per agent (or per surface):

```lua
-- yolo-jail.config.lua (workspace) or ~/.config/yolo-jail/config.lua (user) — §3.4
-- ctx.config  : the composed config, PARSED to a Lua table (yolo did the decode)
-- ctx.stage   : the file-tree staging handle (what gets copied into the jail)
-- ctx.managed : read-only view of the keys the jail will enforce regardless
-- ctx.agent   : "pi" | "claude" | … ;  ctx.surface : "settings" | "config" | …
-- Return value (or the mutated ctx.config) is re-encoded by yolo. No yolo helper
-- lib — it's plain Lua over a plain table.

yolo.transform("pi", function(ctx)
  -- The permission gate is a host-only safety net; in the jail the container IS
  -- the boundary, so neither the extension entry nor its file should cross.
  local kept = {}
  for _, ext in ipairs(ctx.config.extensions) do
    if not ext:find("permission%-gate") then kept[#kept + 1] = ext end
  end
  ctx.config.extensions = kept
  ctx.stage.exclude("extensions/permission-gate.ts")
end)
```

The intent is a comment; the reshaping is plain Lua over a plain table. Compare
the whole mechanism this replaces — a `{op, path, match}` mini-interpreter
encoded as JSON data, plus a separate file-exclude key, where a typo is silently
ignored. Here a typo'd field is a **loud runtime error**, arbitrary reshaping
needs no new yolo op, and "keep this out of the jail" is one place.

**No helper library.** yolo's *only* contribution to the transform is
**parsing**: for a surface in a known structured format (JSON, YAML, TOML), yolo
decodes the config and hands the hook the parsed table (and re-encodes the return
value). It ships **no** `reject`/`get`/`set`/`merge` sugar — those are one-liners
in stock Lua (`string.find`, table iteration), and every helper is API to
maintain and version. The contract is deliberately tiny: *parse → your function →
re-encode*.

### 3.3 Format-agnostic by construction

The merge and the transform operate on a **decoded value** (a Lua table), not on
JSON text. Each surface's builtin manifest declares its **codec** —
`json | toml | yaml | lines | raw` — and yolo owns the decode/encode round-trip:

- **Structured codecs** (`json`/`toml`/`yaml`): host + jail sources decode to
  tables, deep-merge, the Lua hook transforms the table, yolo encodes back to the
  surface's own format. Claude gets JSON, Codex gets TOML — the *same* hook API,
  because the hook never sees the format.
- **`raw`**: the hook gets the config as a string and returns a string, for
  formats yolo won't structurally round-trip. The escape hatch that keeps "don't
  assume JSON" honest.
- **Tree surfaces** (`extensions/`, `skills/`, `hooks/`): the hook gets no table,
  just `ctx.stage` (include/exclude by relative-path glob, paths preserved). A
  tree surface can also carry a yolo-shipped `defaults` layer: the `skills/`
  surface stages a **built-in skill suite** (`internal/agents/builtinskills`,
  embedded in the binary) *under* the host skills, so a same-named host skill
  overrides the built-in — the ordinary `defaults` < `host` precedence, applied
  to a tree. When this surface moves onto the engine (Phase B), that staging
  order is the behavior to preserve; `PrepareSkills` already implements it.

This is why the design is generic over "anything we generate this way": adding an
agent whose config is TOML or line-based needs a codec entry, not a new transform
mechanism.

### 3.4 Placement in config, sandbox, and safety

- **Placement:** two fixed, auto-loaded locations, parallel to the two config
  files — **not** under `.yolo/` (that dir is gitignored working state, so a
  committed transform can't live there):
  - **Workspace:** `yolo-jail.config.lua` at the repo root, beside
    `yolo-jail.jsonc`. Tracked, committable, travels with the repo.
  - **User:** `~/.config/yolo-jail/config.lua`, beside the user `config.jsonc`.
    Host-wide, per-user. **This is the primary case** — a user's personal
    redactions that apply to every workspace they jail.

  Both auto-load if present (no config key needed); both run when both exist —
  **user first, then workspace**, each a `yolo.transform(...)` registration, so a
  workspace transform composes on top of (and can override) the user one. Neither
  present → identity (pass-through). An explicit `config_transform` key in
  `yolo-jail.jsonc` may still point elsewhere for the unusual case.
- **Sandbox:** a pure-Go Lua VM (e.g. `gopher-lua`, no cgo — the Go-only world
  post-wipe means no cross-language interpreter is needed). The environment is
  **locked down: no `os`, `io`, `require`, network, or filesystem** beyond the
  `ctx` handles. The transform is a pure function of its inputs; determinism is
  required (a non-deterministic transform breaks the overlay's diff).
- **Safety prompt:** you can't statically diff "what a Lua function does," so the
  config-change confirmation diffs the **rendered output** — run `yolo config
  render` before vs. after and show that diff (§6). This is strictly better than
  diffing config *text*, and it works identically whether a change touches the
  Lua or the layers.
- **Loud failure:** a Lua error (typo, nil index) fails the render with the file,
  line, and message — never a silent partial config. Fail-closed: on a transform
  error, keep the last good render with a visible warning rather than shipping a
  half-transformed file.

## 4. Layers and scope

Structured surfaces compose from a fixed five-layer stack (lowest → highest
precedence):

| Layer | Source | Scope | Purpose |
|---|---|---|---|
| `defaults` | manifest data (image) | global | yolo builtin, user-overridable |
| `host` | staged host files, parsed fresh each boot (`:ro`) | per-host | the user's host config |
| `workspace` | `agent_config.<agent>` in `yolo-jail.jsonc` (user cfg merged under workspace cfg) | per-workspace | jail-only config the user declares |
| `runtime` overlay | capture-diff sidecar (§5) | per-workspace | what changed in-jail |
| `managed` | manifest data (image) | global | yolo's asserted keys — win the merge, applied after the Lua hook (a precedence guarantee in the generated file, not an OS enforcement — §9) |

Deep-merge semantics: objects merge at every depth, `null` deletes a key, arrays
replace by default; a surface's manifest may pin `append` (with dedupe) for a
keypath (e.g. an allow-list). The staging that produces the `host` layer is
**include-first**: only what a builtin surface/glob names crosses into the jail,
so a new host file is fail-closed (not staged) until declared — the transform
redacts *within* what's staged, it is not the safety boundary.

## 5. Surviving regeneration — the capture-diff overlay

Because yolo regenerates the user-scope file every boot and the agent's session
can also write that same file — via a `/config` command, a permission approval,
**or a plain file edit** (every agent has a shell and file tools) — the two share
one file. Surviving in-jail edits is therefore universal, and mechanism-agnostic:

Two sidecars the agent never sees, in `<workspace>/.yolo/…`: `last_render` (the
exact bytes yolo wrote last boot) and `overlay` (accumulated jail edits). Each
boot:

```
delta   = mergeDiff(last_render, current_file)   # current = last_render + ANY in-jail edit
overlay = deepMerge(overlay, delta)              # accumulate; deletions are null tombstones
render  = pipeline(defaults, host, workspace, overlay, transform, managed)   # §3.1
write(render); last_render = render
```

The diff is against *the bytes on disk*, so it captures the edit however it was
made — that is what makes the overlay agent- and mechanism-agnostic. Three
details: **precedence** (overlay outranks host/workspace so your edit wins; an
entry auto-retires when the host value converges to it; `yolo config overlay
--reset <agent>` is the escape hatch); **deletions** (null tombstones, so a
removed key isn't resurrected — the exact bug in today's merge); **managed**
(applied after both the overlay and the Lua hook, so a yolo-managed key changed
in-jail is captured but visibly reverts on render — correct; note this governs
the generated file only, not the security boundary, which is the container — §9).

## 6. `yolo config render` — run the pipeline on demand

The render is *executed*, not static — so there must be a command that runs the
whole pipeline (stage → merge → Lua transform → enforce → encode), prints what it
would write, and touches no live agent config. It runs **both** host-side (the
edit-before-launch loop, no container needed) **and inside the jail** (the
operating agent's "what is my config, and why?" aid — §9). It's cheap because the
engine is pure: host-side it renders in a temp dir; in-jail it renders from the
same layers a boot render would use (once boot is on the engine — Phase B/C),
read-only.

```bash
yolo config render <agent>                 # every surface, to stdout — no writes
yolo config render claude --surface settings
yolo config render pi --explain [KEYPATH]  # which layer/hook won each leaf (incl. dropped host keys)
yolo config render pi --host F --workspace F --overlay F --format json   # hypotheticals / fixtures
```

It calls the same engine boot now uses for the agent-config surfaces (Phase C —
see the header status), so for those surfaces "what render prints" is "what the
jail gets": boot's `Configure*Prism` writers and `yolo config render` both drive
`agentcfg`. Render is simultaneously: the **dev-iteration loop** (edit
`config.lua`, `render --explain`, repeat — no container churn), the **safety-diff
source** (§3.4), and the **test harness** (fixture vectors: `inputs → render`,
byte-checked in `go test`).

The `yolo check` config validator's entrypoint preflight also exercises the real
boot path now: it calls the `Configure*Prism` writers (pointing their §5 sidecars
at a temp workspace so the dry run never touches the live one), so it validates
the engine's output, not a stale parallel generator.

## 6.5 Worked example — the pi permission gate, end to end

One concrete change followed through every stage. **Goal:** the host pi keeps its
`permission-gate` extension (approval prompts on the host), but the jail — where
the container *is* the boundary — should not load it.

**① Sources.** The host file yolo never writes:

```jsonc
// ~/.pi/agent/settings.json   (host — read-only to yolo)
{ "theme": "dark",
  "defaultModel": "claude-fable-5",
  "extensions": ["extensions/permission-gate.ts", "extensions/git-helper.ts"] }
```

The builtin pi manifest (yolo-shipped data) declares the surface + its enforced keys:

```jsonc
// manifest: agent=pi, surface=settings, codec=json
{ "path": "~/.pi/agent/settings.json",
  "defaults": { "theme": "system" },
  "managed":  { "defaultProjectTrust": "always" } }   // jail-enforced, wins last
```

**② The user's transform** — the *only* thing the user writes:

```lua
-- yolo-jail.config.lua  (repo root, committed; runs in the workspace trust domain)
yolo.transform("pi", function(ctx)
  local kept = {}
  for _, ext in ipairs(ctx.config.extensions) do
    if not ext:find("permission%-gate") then kept[#kept + 1] = ext end
  end
  ctx.config.extensions = kept          -- drop it from the settings array
  ctx.stage.exclude("extensions/permission-gate.ts")   -- and don't stage the file
end)
```

**③ Pipeline** (§3.1), for the `pi/settings` surface:

```
decode(host json) ─┐
defaults ──────────┤ deepMerge → { theme:"dark", defaultModel:"claude-fable-5",
overlay (empty) ───┘              extensions:[permission-gate, git-helper] }
        │  transform(merged, ctx)      → extensions:[git-helper]   (+ stage.exclude)
        │  enforce(managed)            → + defaultProjectTrust:"always"
        ▼  encode(json) → write /home/agent/.pi/agent/settings.json (user scope)
```

**④ What lands in the jail** — and what `yolo config render pi` prints, no
container needed:

```jsonc
{ "theme": "dark",
  "defaultModel": "claude-fable-5",
  "extensions": ["extensions/git-helper.ts"],   // gate gone
  "defaultProjectTrust": "always" }              // managed, enforced
// extensions/permission-gate.ts — never staged into the jail tree
```

`yolo config render pi --explain extensions` shows the provenance:
`host → [git-helper, permission-gate]`, then `transform dropped permission-gate`.

**⑤ An in-jail edit survives.** Inside the jail you set `theme` to `"light"` (via
pi's UI or by editing the file). Next boot: `mergeDiff(last_render, current)` →
`{theme:"light"}` captured into the overlay; the render now has `theme:"light"`,
and it **stays** every boot after — until you `--reset` or set it back to the host
`"dark"` (§5, §9). The host's `dark` no longer wins because the overlay outranks
the host layer.

Note what did **not** happen: the host `settings.json` was never modified; nothing
was written to `/workspace`; yolo needed no ability to parse or round-trip pi's
extension `.ts` files (they're a *tree* surface — staged/excluded, never decoded);
and the whole result was previewable with `render` before any jail started.

## 7. Why (the problems this replaced — verified 2026-07-18, retired 2026-07-22)

The mechanism the prism replaced, from the code as it stood pre-cutover (the
`Configure*` writers and `syncHostSettings` are now deleted — see the Phase-C
status):

- **A shared mutable file.** The agent rewrote the same `settings.json` yolo
  writes, forcing a bespoke **one-level-deep snapshot three-way merge**
  (`syncHostSettings`, `claude.go` / `agent_configs.go`); nested objects/arrays
  compared atomically, and the
  snapshot loader returned `{}` on *any* error — so one boot with a host JSON typo
  looked identical to "host removed all keys" and rolled the jail back.
- **No transform step.** A host key stays out of the jail only if it collides
  with a hardcoded force-managed key. Redaction (pi's permission gate) is
  inexpressible.
- **Flat filename mounts** (`host_pi_files`) that reject path separators, so
  subdirectory/linked config is inexpressible; the unknown-key handling here is
  the `host_pi_files` parity bug the Go port already hit.
- **Per-agent, per-language force-sets** applied by code order, with Gemini using
  `setdefault` (a user value silently disables the intended YOLO default — a
  latent security-posture bug).

Layered regeneration + a Lua transform + the ownership principle collapse all of
these into one engine driven by per-agent manifests, with the reshaping expressed
once, in a real language, on yolo's own output.

## 8. Migration — serial foundation, then parallel fan-out

Structured as three phases; the parallelism is called out because it maps to how
this gets built (see ROADMAP "Config-composition build").

**Phase A — engine (serial gate).** A leaf library with **no callers**, fully
testable in isolation. Pin the interfaces (`layer`/`surface`/`manifest`/`ctx`)
first, then these four parallelize:
1. pure functions — `decode`/`deepMerge`/`enforce`/`render`/`mergeDiff` over
   generic values, per-codec (JSON + TOML first);
2. the Lua VM sandbox (`gopher-lua`, locked down) + the `ctx` bridge;
3. the manifest schema + loader;
4. the fixture corpus (`inputs → render`, `go test`) — **this is the spec.**
Cap Phase A with `yolo config render` (host-side + in-jail, §6) so every later
surface is verifiable.

**Phase B — surfaces (fan out; mutually independent on the frozen engine).**
✅ **Done for the agent-config surfaces.**
- **pi first** as the proof-of-concept — exercises tree staging + a transform +
  the overlay; deletes `host_pi_files` and the pi three-way merge.
- then in parallel, one commit each: **Claude** (widest — `settings.json` +
  `.claude.json` as runtime-state), **gemini**, **copilot**, **opencode**,
  **Codex** (TOML codec), plus **agy**, which was born directly on the prism (no
  bespoke writer ever existed). Each `Configure*Prism` retires its bespoke merge
  and gains the Lua transform + `render` for free; each landed + verified in a
  nested jail on its own. The non-agent surfaces — **MCP** (`mcp.go`), **LSP**,
  **mise** (`mise.go`), **identity** — are not yet ported and keep their bespoke
  generators for now.

**Phase C — deletion (serial, last).** ✅ **Done (2026-07-22) for the agent-config
surfaces.** The `YOLO_PRISM_SURFACES` cutover gate is retired, `boot.go` calls the
`Configure*Prism` writers unconditionally, and the six bespoke `Configure*`
writers plus their now-dead helpers (the three-way merge, the codex TOML dumper,
the numeric-equality cluster) are deleted. The obsolete snapshot/managed-MCP
sidecars are removed on each surface's first-migration boot. The `host_*_files`
keys survive (the prism host layer reads through them). Deletion of the non-agent
bespoke generators waits on their Phase-B port.

Each stage ends with a nested-jail verification (per repo `CLAUDE.md`).

## 9. Decisions (all settled)

**Settled (2026-07-20):**

- **No Lua helper library.** yolo's only contribution to a transform is *parsing*:
  a surface in a known structured format (JSON/YAML/TOML) is decoded to a table,
  passed to the user function, and re-encoded on return. No `reject`/`get`/`set`/
  `merge` sugar — those are stock-Lua one-liners, and every helper is API to
  maintain (§3.2).
- **Overlay: no aging, only reset.** A captured jail edit **stays forever** until
  the user either runs `yolo config overlay --reset <agent>` or sets the value
  in-jail back to the host value (at which point the delta is empty and the entry
  auto-drops — the natural convergence, nothing timer-based). "Aging out" is not a
  thing; there's no principled clock for it and it would silently resurrect host
  values. (§5.)
- **Codecs: minimal.** JSON + TOML day one (Claude, Codex). YAML/lines only when
  an agent actually needs them; `raw` (string in/out) covers everything else.
- **Sandbox is mandatory, same safety domain as the source.** The transform is
  arbitrary unvalidated user code, so it runs in the locked-down VM (no `os`/`io`/
  `require`/net/fs — §3.4). It stays in the **same trust domain as the config it
  transforms**: a workspace-committed `config.lua` runs with the workspace's
  authority, a user-level one with the user's — a transform never gains privilege
  over the sources it composes. The config-change safety prompt diffs the
  *rendered output* (post-execution in the sandbox, which is side-effect-free), so
  you approve the effect, not the opaque script.
- **The security model does NOT rely on an OS-managed config file.** yolo's YOLO
  enforcement is the injected `--dangerously-skip-permissions`-class flag
  (`internal/agents/agents.go`), and the container is the security boundary — the
  jail runs unconfined *by design*, so there is nothing in-jail to lock down via
  config. The `managed` **layer** in this doc is just "yolo's keys are applied
  last in the composition, so a transform or overlay can't silently drop them
  from the *generated* file" — a composition-precedence guarantee, not a
  tamper-proof OS mechanism. An earlier draft proposed writing Claude's
  `/etc/claude-code/managed-settings.json` for true OS-level enforcement;
  **dropped** — that file is `rw` in the jail (the jail user is root), so it
  guarantees nothing a normal render layer doesn't, and treating it as a security
  tier would be misleading. `managed` stays a layer, never an OS file.
- **Live host edits → jail restart, always.** A host config change is picked up
  on the next `yolo` invocation (staging + full re-render at boot). There is **no**
  live in-jail resync and no `yolo config sync` — a running jail keeps the config
  it booted with; restart to pick up host changes. Simple and predictable; matches
  how the rest of the jail treats host state.
- **`yolo config render` runs INSIDE the jail too, not just host-side.** It's the
  in-jail "what is my config, and why?" aid the operating agent needs while
  working — `render`/`--explain` on demand, read-only (no boundary concern). The
  in-jail `yolo` already ships (mounted from `/opt/yolo-jail`), so this is wiring,
  not a new surface — with one requirement to honor: the render's *inputs* must be
  reachable in the jail. The composed layers (defaults+managed from the image, the
  staged host layer, the workspace config, and the overlay sidecar) are all
  already present in the jail per §4–§5, so an in-jail render reproduces the boot
  render without reaching back to the host. Host-side `render` stays too (for the
  edit-before-launch loop); same engine, same output, both places.

*(No open questions remain — the design is settled. Implementation is sequenced
in §8.)*

---

*Provenance: consolidated 2026-07-20 from a research→design exploration (agent
config surfaces, host↔jail plumbing, failure modes). The decision to use Lua as
the sole, format-agnostic transform — over a data-filter vocabulary — and the
user-scope-only ownership rule are the settled outcomes; the earlier menu of
models and the `drop`/`dropItems`/`set` data-filter design are dropped and live
in git history.*
