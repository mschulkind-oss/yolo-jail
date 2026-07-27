# The three decisions, in depth

**Status:** design, 2026-07-26. Answers *"tell me more about these 3 decisions"* under the
now-fixed target: **no Go-owned agents at all; agent support exists only as official packs
shipped with yolo; a default jail has no agent.**

**Reads with:** [packs-and-the-prism.md](packs-and-the-prism.md) (phases, contribution kinds,
typed exports), [what-yolo-is.md](what-yolo-is.md) (boundaries),
[../plans/packs-rip-out.md](../plans/packs-rip-out.md) (the work framing),
[../plans/composed-config-work.md](../plans/composed-config-work.md) (the existing list).

**The target state changes two of the three answers.** That is the main finding, and it is
why this doc exists rather than just a longer bullet in the plan.

---

## Decision 1 — where does composition run?

### The question, restated precisely

Not "host or jail" as a binary. The real question is **which of three things happens on the
host**: (a) computing the composed bytes, (b) writing them to their destination, (c)
reconciling in-jail edits back into the overlay. They can be split.

### What the target state changes

Under the old framing (agents built into yolo, always present) host-side composition was
attractive mainly for *error timing*. Under **no-agent-by-default** it becomes something
stronger: **the set of things to compose is now dynamic**, derived from which packs are
enabled. A jail with no agent composes nothing. So composition needs a *plan* before the
container starts, which is host-side work no matter where the bytes get written.

That is a real shift: the question moves from "would host-side be nicer?" to "the host
already has to decide what exists, so how much more should it do?"

### The recommendation

**Compose on the host; write on the host; reconcile on the host at next assembly.** With one
carve-out that is not negotiable: **anything that needs the jail's own filesystem stays
in-jail**, and that set is small and identifiable — the lazy agent-CLI launchers, npm/LSP
installs, mise, and anything reading a nix-store path.

The clean split is **config vs binaries**:

| | Where | Why |
|---|---|---|
| composed config files | host | pure function of config; nothing probes the container |
| capture/reconcile of in-jail edits | host, at next assembly | the files are in a host directory already (`GlobalHome`) |
| agent CLI install | in-jail, lazily | needs the jail's node/npm/PATH; already works this way |
| MCP/LSP server install | in-jail | same |

**Verified, and it is the load-bearing fact:** an empty-agent jail boots today.
`agents: []` validates with a warning
(`[WARN] config.agents: empty list — no coding agents will be installed in the jail`) and a
real nested run reached `BOOT_OK`. So "no agent by default" is not a new capability to
build — it is a supported state already.

### The thing that surprised me, and it is a design problem

That same probe showed **`~/.claude`, `~/.codex` and `~/.copilot` all exist in an
empty-agent jail.** They are not created by that boot — they live in `GlobalHome`
(`~/.local/share/yolo-jail/home/`, 18 directories), which is **shared across every jail on
the machine**. So agent state is machine-global, not per-jail.

Consequences for the target state:

- "No agent by default" cannot mean "a clean home." A user who ever enabled a pack has its
  dirs forever, in every jail.
- Pack *removal* has no natural cleanup point. This is the same gap
  `composed-config-work.md` notes for captured overlay entries, but wider.
- Conversely it is *why* lazy in-jail install works: the install survives the container.

**This needs a ruling** and it is not currently written down anywhere: is agent/pack state
per-machine (today's behavior, shared `GlobalHome`) or per-jail? Today's answer is defensible
— it is what makes an agent's login survive a restart — but it should be a decision rather
than an accident.

### Also verified: the MCP bootstrap is not agent-gated

`internal/entrypoint/shell.go:167` installs `chrome-devtools-mcp` and
`server-sequential-thinking` unconditionally — `if ! command -v chrome-devtools-mcp`. In the
empty-agent probe it installed **112 npm packages for zero agents.** Under an all-packs
model this is exactly what should become a pack contribution (an MCP pack), gated on
something actually wanting it. Small, concrete, and it is a real win of the target state
rather than a hypothetical one.

### Backends

`macos-user` is the acid test and it *favors* this recommendation: it has no mounts and no
`/ctx`, so "compose then mount `:ro`" is meaningless there — but "compose then **write**"
works fine, because host and jail share a filesystem. Host-side composition is the only
option that has one story across all three backends. Apple Container's inability to
bind-mount single files (`acMaterialize`) likewise becomes irrelevant if the file is already
materialized before the container starts.

### Cost, honestly

In-jail re-render dies. Today an agent edits a composed file and the next boot reconciles
from inside. Host-side means reconciliation happens at *assembly*, so a running jail cannot
re-render itself — you would need `yolo config render` to be invokable in-jail and to write
through, or accept that re-render requires a restart. **That is the one genuine loss**, and
it is the sub-question worth deciding explicitly rather than discovering.

---

## Decision 2 — are projections data or code?

### The spec, enumerated

Five real projections exist. I read each one; these are the exact operations:

| Projection | Location | Operations required |
|---|---|---|
| **codex** MCP | `codex.go:15` | passthrough, default-empty (`args`), conditional-include (`env` only if non-empty) |
| **opencode** MCP | `agent_configs.go:131` | **rename** (`env`→`environment`), **fold** (`command`+`args` → one array), **inject constants** (`type:"local"`, `enabled:true`) |
| **gemini** MCP | `agent_configs.go:167` | **cross-type derive** (LSP defs → MCP entries), key-suffix (`<name>-lsp`), arg templating |
| **claude** MCP | `prism_claude.go:104` | **route to a different surface** (`.claude.json`, not `settings.json`) + **tombstone** (`mcpServers: nil` to strip a host block) |
| **copilot** LSP | `prism.go:400` | default-empty ×2, **conditional-OMIT** — and note *omit ≠ null*, because a null leaf is an RFC-7386 tombstone the engine would drop |

That is **eight distinct operations**, and two of them are the kind that break naive designs:

- **`conditional-OMIT` vs `tombstone-null` is a semantic distinction**, not a formatting one.
  A mapping language that renders "absent" as null silently converts "leave this alone" into
  "delete this." Any candidate must be able to express both.
- **cross-type derivation** (gemini building MCP entries out of LSP definitions) is a
  projection whose input is *a different export type than the one it emits*. This is the
  case that most resembles a program.

### The recommendation

**A declarative projection with a typed operation set — not a general language, not Go.**
Specifically: a projection is data listing named operations (`rename`, `fold`, `inject`,
`default`, `omit_if_absent`, `suffix_key`, `route_to`, `tombstone`), with Lua as an escape
hatch for the residue.

Two reasons this is the answer rather than a compromise:

1. **The eight operations are a closed set derived from five real cases, not a guess.** A
   language designed to cover them is small. A language designed "for projections in
   general" is unbounded — that is the failure mode to avoid.
2. **The target state removes "Go" as an option**, and this is the decisive argument. If
   projections stay compiled into yolo, then adding an agent still requires a yolo release,
   and "all agent support is packs" is false. **Decision 2 is therefore forced by the target
   state**, where previously it was a genuine choice. Worth stating plainly: under the old
   bet-A framing, "projections in Go, packs are data" was defensible and probably cheapest.
   It is no longer available.

**One projection dies on its own:** gemini is being removed (tranche 0), which deletes the
cross-type-derivation case. That does *not* mean the design can ignore it — copilot's
LSP surface has the same shape — but it does mean the hardest case is not load-bearing for
any *shipping* agent, which lowers the risk of getting it slightly wrong at first.

### Prerequisite that is currently broken

If Lua is the escape hatch, note that **the per-surface Lua seam has never run**:
`manifest.Surface.Transform` is populated from `host_files` config
(`entrypoint/hostfiles.go:135`) and validated, but the compose path passes only `in.Script`
— the global `config.lua` pair — and the field's sole readers are two display checks in
`configls.go`. Filed as work item 1.9. **Do not design a projection language on top of a
hook that has never executed**; fix it first, cheaply, and learn from it.

---

## Decision 3 — what do "agents as packs" mean for the stateful boot writers?

### This is the decision the target state changes most, and in a good way

The framing has been "2,200 lines of per-agent logic, some of it not data." I went through
the actual call sites and **that framing overstates the problem substantially.**

**Every single surface already routes through exactly two mechanisms:**

- `renderSurfaceStateful` (`prism.go:101`) — the overlay-capturing path
- `renderSurfaceComputed` (`prism.go:258`) — the pure per-boot overwrite

Grepping the call sites: mise, codex, claude, opencode, pi, copilot, gemini, agy and the
`host_files` dynamic surfaces are **all** one of those two calls. There is no third
mechanism, and no agent has a bespoke render path.

So what is the per-agent code actually doing? Three things:

1. **building a `computed` map** — that is the projection, decision 2
2. **`os.MkdirAll`** of the agent's config dir (`prism.go:338, 372, 442, 485`)
3. **a one-shot migration `os.Remove`** guarded by `out.FirstMigration`
   (`prism.go:351-352, 459-460`) — deleting a dead bespoke sidecar

All three are **declarable.** A directory to create is data. A one-time file to delete on
first migration is data (and is transitional anyway — those removals exist to clean up
pre-prism sidecars and can eventually be deleted outright).

### The recommendation

**Reframe from data-vs-logic to per-agent *facts* (pack data) vs reconciliation *mechanisms*
(engine, agent-agnostic, selected by name).** A pack declares:

```
surface:
  path: ~/.codex/config.toml
  codec: toml
  mode: stateful          # names an ENGINE mechanism; the pack does not implement it
  defaults: {...}
  managed: {...}
  computed: <projection>
```

Counting against the inventory: **two mechanisms cover every existing surface.** That is a
small enough number to be the answer. The engine keeps `stateful` and `computed`; packs
supply facts.

### The one genuinely hard case, and why it does not break this

`writeClaudeJSON` (`claude.go:54-72`) is a read-modify-write over `~/.claude.json`, a file
claude owns and actively writes (~33 keys). It must merge yolo's assertions without wiping
live state: `loadObject` → `setDefaultMap(mcpServers)` → prune managed names → `updateFrom` →
`Set(projects[ws].enableAllProjectMcpServers, true)` → `setDefault(hasTrustDialogAccepted)`.
It is not a transform because its output depends on what the agent wrote since last boot.

But it is a **third mechanism**, not a special case:
`mode: read_modify_write` + `assert: {...}` + `preserve_unknown: true`. The engine implements
it once; the pack declares which keys yolo asserts. And
[composed-config-work.md](../plans/composed-config-work.md) item **2.2b already wants to move
`copilot/config` onto this same pattern for an independent correctness reason** (it can wipe a
live OAuth token). So the mechanism earns its place regardless of packs — which is the
strongest kind of argument for adding one.

**Three engine mechanisms, then: `stateful`, `computed`, `read_modify_write`.** That is the
whole surface taxonomy.

### What must stay in Go — the hard rule

`AgentSpec.HostFiles` (`agents.go:31-35`). Its own doc comment states the rule:

> It is a CREDENTIAL BOUNDARY: which host files cross into the jail is decided here, in
> yolo-shipped code, and can never be widened by a user/workspace config (that is what
> retiring `host_claude_files`/`host_pi_files` bought).

Its only mount consumer is `hostFileArgs` (`cli/run/hostclaude.go`), which reads *no config
key* and emits *no env var* — deliberately. Two entries exist today: claude's
`.claude/settings.json` and pi's `.pi/agent/settings.json`.

**But "stays in Go" is the wrong resolution under the target state** — if official packs are
structurally identical to user packs and agent support is entirely packs, a hardcoded Go
allowlist keyed by agent name means agent support *isn't* entirely packs.

**The resolution already exists in the codebase, and it is scope, not hardcoding.**
`host_files` solved the identical problem: a **source-bearing entry is user-config-only**,
enforced by construction — `SourceLessHostFilesFrom` (`config/hostfiles.go:945`) parses
workspace scope and returns *only* source-less entries, so a workspace config physically
cannot name a host file. Apply the same rule to packs: **a pack may declare that it wants a
host file; only user-scope config may grant it.** The pack requests, the human approves.
That preserves the boundary without a per-agent Go table, and it reuses a mechanism that
already ships.

This is the sub-question that most needs an explicit human ruling, because it is the one
place where the target state and the security model actually pull against each other.

---

## Order of play

1. **Decision 3's reframe is free — take it now.** Three named engine mechanisms
   (`stateful`, `computed`, `read_modify_write`) covering every surface is not a research
   project; two of the three already exist and the third is already wanted for a correctness
   fix (2.2b). This also *shrinks* decisions 1 and 2 by removing the "2,200 lines of
   irreducible logic" fear from both.
2. **Decision 2 is forced, so spend the time on the operation set, not the choice.** The
   target state eliminates Go projections. Design against the eight enumerated operations;
   fix work item 1.9 first so the Lua escape hatch is real.
3. **Decision 1 last of the three** — it is the largest port, and its main new argument
   (the compose set is dynamic under no-agent-by-default) only became visible once the target
   state was fixed.
4. **Unchanged: the tranche 0–2 prerequisites still come before all of it.** Nothing above
   removes the need to delete gemini, fix the correctness cluster, un-hardcode `/workspace`,
   and de-compose the credential surfaces.

## Still needs a human ruling

- **Pack-declared host files: request-plus-user-grant, or never?** The `host_files`
  source-bearing precedent says request-plus-grant works. This is the one real tension
  between the target state and the credential boundary.
- **Is agent/pack state per-machine or per-jail?** Today it is per-machine by accident of a
  shared `GlobalHome` — verified: agent dirs exist in an empty-agent jail. It should be a
  decision, and it determines whether pack removal can ever clean up.
- **Does a running jail need to re-render?** Host-side composition means reconcile happens at
  assembly. If in-jail re-render must survive, it needs an explicit mechanism.
- **First-migration vs user-asked-to-discard** — already open as
  `composed-config-work.md` §2.1, and it gates tranche 2, which gates the rip-out.
