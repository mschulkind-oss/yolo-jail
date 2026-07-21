# Agent config composition — layered regeneration + Lua transforms

**Status:** Design of record (decided 2026-07-20). Supersedes the exploratory
RFC that carried a menu of models and a data-filter vocabulary — this is the line
in the sand. **Not started** (no engine code yet); sequenced in
[ROADMAP.md](ROADMAP.md).

yolo generates each coding agent's in-jail config (Claude's `settings.json`,
Codex's `config.toml`, pi's `settings.json`, …) from host + jail sources. This
doc fixes **how** that generation composes and how a user reshapes it.

---

## 1. The decision, in one paragraph

Each agent config is a **build product yolo regenerates every boot** from an
ordered stack of layers. yolo writes it only into the jail **user scope** (never
the host, never the workspace). A user reshapes what crosses into the jail with a
**Lua transform** — one sandboxed function per surface that receives the composed
config as a decoded value and returns the transformed one. Lua is the *only*
transform mechanism: it is format-agnostic (the config need not be JSON),
Turing-complete enough to express any redaction without yolo growing a
vocabulary, and it operates on yolo's generated artifact, so **it never requires
modifying — or even being able to write — the source config.** In-jail edits
survive regeneration via a capture-diff overlay, and `yolo config render` runs
the whole pipeline offline so you can see and diff the result without a jail.

## 2. Six principles (the line in the sand)

1. **Regenerate, don't reconcile.** The agent's config file is rebuilt from
   sources on every boot, not edited in place. Host-key removal needs zero
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
and returns the version that should be written into the jail. Managed
(security-boundary) keys are re-asserted *after* the hook, so a transform can
never weaken the jail's boundary — it's given `ctx.managed` read-only so it can
see them, but yolo has the last write.

### 3.2 What the hook receives, and the Pi example

The user points `config_transform` at a Lua file (§3.4); it registers a function
per agent (or per surface):

```lua
-- .yolo/config.lua
-- ctx.config  : the composed config as a Lua table (decoded, format-independent)
-- ctx.stage   : the file-tree staging handle (what gets copied into the jail)
-- ctx.managed : read-only view of the keys the jail will enforce regardless
-- ctx.agent   : "pi" | "claude" | … ;  ctx.surface : "settings" | "config" | …

yolo.transform("pi", function(ctx)
  -- The permission gate is a host-only safety net; in the jail the container IS
  -- the boundary, so neither the extension entry nor its file should cross.
  ctx.config.extensions = yolo.reject(ctx.config.extensions, "*permission-gate*")
  ctx.stage.exclude("extensions/permission-gate.ts")
end)
```

The intent is a comment; the two edits are two named calls. Compare the whole
mechanism this replaces — a `{op, path, match}` mini-interpreter encoded as JSON
data, plus a separate file-exclude key, where a typo is silently ignored. Here a
typo'd field or function is a **loud runtime error**, arbitrary reshaping needs
no new yolo op, and "keep this out of the jail" is one place, not two vocabularies.

yolo provides a small helper library (`yolo.reject(list, glob)`,
`yolo.get/set(tbl, path)`, …) as sugar, but the transform is ordinary Lua over an
ordinary table — anything expressible in Lua is expressible here.

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
  just `ctx.stage` (include/exclude by relative-path glob, paths preserved).

This is why the design is generic over "anything we generate this way": adding an
agent whose config is TOML or line-based needs a codec entry, not a new transform
mechanism.

### 3.4 Placement in config, sandbox, and safety

- **Placement:** a top-level `config_transform` key in `yolo-jail.jsonc` naming a
  Lua file (default: auto-load `.yolo/config.lua` if present). It merges across
  config levels like everything else: a user-level `~/.config/yolo-jail/config.jsonc`
  transform applies host-wide; a workspace one is committable and travels with the
  repo. Absent → identity (pass-through).
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
| `managed` | manifest data (image) | global | security-boundary keys — always win, applied after the Lua hook |

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
(applied after both the overlay and the Lua hook, so a security key changed
in-jail is captured but visibly reverts on render — correct).

## 6. `yolo config render` — run the pipeline without a jail

The render is *executed*, not static — so there must be a command that runs the
whole pipeline (stage → merge → Lua transform → enforce → encode) offline, in a
temp dir, and prints what it would write, touching no container. It's cheap
because the engine is pure and jail-free by construction.

```bash
yolo config render <agent>                 # every surface, to stdout — no writes
yolo config render claude --surface settings
yolo config render pi --explain [KEYPATH]  # which layer/hook won each leaf (incl. dropped host keys)
yolo config render pi --host F --workspace F --overlay F --format json   # hypotheticals / fixtures
```

It calls the *same* engine the entrypoint calls — "what render prints" is "what
the jail gets." It is simultaneously: the **dev-iteration loop** (edit
`config.lua`, `render --explain`, repeat — no container churn), the **safety-diff
source** (§3.4), the **test harness** (fixture vectors: `inputs → render`,
byte-checked in `go test`), and the `yolo check` config validator.

## 7. Why (the problems this replaces — verified 2026-07-18)

The current mechanism, from the code:

- **A shared mutable file.** The agent rewrites the same `settings.json` yolo
  writes, forcing a bespoke **one-level-deep snapshot three-way merge**
  (`_sync_host_settings`); nested objects/arrays compare atomically, and the
  snapshot loader returns `{}` on *any* error — so one boot with a host JSON typo
  looks identical to "host removed all keys" and rolls the jail back.
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

## 8. Migration (each stage independently shippable)

1. **Engine as a leaf library, no callers** — decode/merge/enforce/render + the
   Lua VM sandbox + the manifest schema, with a fixture corpus (`inputs → render`)
   run by `go test`. This corpus is the spec.
2. **Claude managed offload** — write the security/YOLO keys to Claude's
   *managed* scope (`/etc/claude-code/managed-settings.json`), the one scope yolo
   owns outright (neither user nor workspace). Independent; ship early. (Open
   question: can the rootless entrypoint write `/etc` at boot? — §9.)
3. **pi** — the motivating surface: builtin manifest + the `config.lua` transform;
   deletes `host_pi_files` and the pi three-way merge.
4. **Claude, then the remaining agents** — migrate `settings.json` (+ `.claude.json`
   classified as runtime-state), then the rest get host reflection via the same
   engine.
5. **Deletion** — remove the bespoke merges, snapshot constants, per-agent mount
   blocks, and the `host_*_files` keys.

Each stage ends with a nested-jail verification (per repo `CLAUDE.md`).

## 9. Open questions

- **Managed write-at-boot:** can the rootless-podman entrypoint write
  `/etc/claude-code/managed-settings.json` at boot? If not, security keys fall
  back to the highest jail-user-scope precedence yolo can own (still above the
  user's own edits since the jail is the boundary).
- **Lua helper surface:** which sugar to ship (`yolo.reject/get/set/merge`) vs.
  leave to raw Lua — keep it minimal; every helper is API to maintain.
- **Overlay UX:** does a jail edit pin its keypath over host updates until values
  converge (with `--reset`), or age out?
- **Codec coverage:** JSON + TOML are needed day one (Claude, Codex). YAML/lines
  only if an agent needs them; `raw` covers the rest.
- **Safety-prompt timing:** the render-diff must run the transform to produce the
  diff, so the confirmation is *post-execution* of user Lua (in the sandbox) —
  confirm that's acceptable (it is, given the sandbox has no side effects).

---

*Provenance: consolidated 2026-07-20 from a research→design exploration (agent
config surfaces, host↔jail plumbing, failure modes). The decision to use Lua as
the sole, format-agnostic transform — over a data-filter vocabulary — and the
user-scope-only ownership rule are the settled outcomes; the earlier menu of
models and the `drop`/`dropItems`/`set` data-filter design are dropped and live
in git history.*
