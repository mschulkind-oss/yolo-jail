# Packs and the prism — one delivery mechanism for all agent support

**Status:** conceptual sketch, 2026-07-26. Not a plan — a shape to argue with before
anyone commits. Written in response to: *"what would it be like if we built the pack
system and then pulled all agent support out into 'official' packs?"*

**Audience:** whoever is deciding whether the pack system is a sharing feature or an
architecture. Those are different bets.

**Reads with:** [agent-config-packs.md](../plans/agent-config-packs.md) (the concrete pack
proposal — this doc is the higher-level frame around it),
[composed-file-permissions.md](composed-file-permissions.md) (the prism's posture rules),
[agent-settings-composition.md](../plans/agent-settings-composition.md) (the engine).

**Two questions this doc raised and does not answer** — *how would pack-shipped logic be
built and cached?* and *where does the config system end and the jail begin?* — are
answered in [what-yolo-is.md](what-yolo-is.md). Its findings sharpen con 2 and the
§6 ranking below; read it before deciding between bets A/B/C.

---

## 1. The two systems, in one paragraph each

**The prism** answers *how is a config file built?* Every file yolo generates is composed
from an ordered stack of layers — `defaults < host < workspace < overlay < computed` —
then a Lua transform, then a re-asserted `managed` layer. It regenerates every boot, so a
dropped input simply disappears rather than needing to be un-applied. Today the surfaces
it composes are Go literals in `internal/agentcfg/builtin.go`, plus user-declared ones
from `host_files`.

**Packs** answer *where does config come from, and who can share it?* A pack is a
directory at a `(repo, path, branch)` triple, fetched host-side (the jail has no git
credentials), carrying skills, AGENTS.md prose, lint conventions, MCP sets. The unit of
sharing is a git ref, so sharing needs no PR and rollback is a ref change.

**The load-bearing connection already exists in the design.** A pack is not a parallel
mechanism — *a pack contributes a **layer** to a prism surface*
([agent-config-packs.md §6.1](../plans/agent-config-packs.md)). Specifically the
`workspace` layer, which is implemented, tested, and has **zero non-test producers**
today. It folds above `defaults` and `host` and below `overlay`/`computed`/`transform`/
`managed` — meaning a company pack can set a default a user then overrides in-jail, and
can never overrule what yolo asserts. That ordering is not a coincidence; it is what makes
packs safe to accept from a colleague's branch.

---

## 2. The reframe: what if agent support *were* packs?

Today "supporting an agent" means writing Go:

| What | Where | Size |
|---|---|---|
| the agent registry (install method, overlay dirs, skills path, briefing pair, YOLO flags) | `internal/agents/agents.go` | 340 lines |
| its composed surfaces (path, codec, defaults, managed) | `internal/agentcfg/builtin.go` | 441 lines |
| its boot writer (`Configure*Prism`) | `internal/entrypoint/prism*.go` | 2,207 lines |
| its built-in skills | `internal/agents/builtinskills/` | 5 files |

The reframe: **make all of that data, shipped as first-party packs.** yolo becomes an
engine plus a set of official packs — `claude`, `copilot`, `codex`, `pi`, `opencode`,
`agy` — that are structurally identical to a pack a colleague writes. Adding an agent
becomes authoring a pack; the seventh agent needs no yolo release.

### The precedent that makes this credible

**This pattern already ships here.** `bundled_loopholes/` is exactly it: a directory of
plugin dirs, each with a `manifest.jsonc`, embedded via `go:embed all:audio
all:claude-oauth-broker all:host-processes`, discovered at runtime by
`loopholes.Discover` which unions **bundled** and **user** dirs through one code path. A
user loophole and a bundled loophole are the same kind of thing.

So "official packs beside user packs, one discovery path" is not a new architecture for
this codebase — it is the loophole model applied to agent support. That is the single
strongest argument for the reframe, and it is worth weighing more heavily than any
aesthetic case.

### What it would look like

```
packs/claude/                     # first-party, embedded, versioned with yolo
  pack.jsonc                      # what agents.go's AgentSpec holds today
  surfaces/settings.jsonc         # what builtin.go's claudeSettings holds today
  skills/…                        # what builtinskills/ holds today
  briefing.md                     # the CLAUDE.md fragment
~/.local/share/yolo-jail/packs/   # fetched user/company packs, same shape
  acme-rust-review/
```

`yolo config ls` would then show a `SOURCE` column — `pack:claude` vs `pack:acme-…` vs
`user` (`host_files`) — and provenance in `--explain` would name the pack rather than the
generic `workspace` layer.

---

## 3. Pros

**1. Adding or fixing an agent stops being a release.** Today a new agent means editing
four Go files and shipping a binary. As a pack it is a directory — and a user can fix a
broken agent config *locally, now*, instead of filing an issue and waiting.

**2. It closes a real asymmetry.** `pi`, `codex` and `opencode` have `Skills: ""` and get
`continue`d, so they receive **no skills at all — including yolo's own built-in suite**.
That is a two-line registry bug today; under packs it is structurally impossible, because
skills arrive with the pack rather than being wired per agent.

**3. One mechanism instead of several.** `host_files`, `mise_tools`, `mcp_presets`,
built-in skills, briefings and agent surfaces are six ways to get content into a jail.
They already share an engine; packs would give them one *declaration* format too. That is
fewer concepts for a user and fewer code paths for us.

**4. Testability improves in a way Go literals cannot.** A pack is data, so a pack is a
fixture. "Does the codex pack still produce a valid `config.toml`?" becomes a table test
over pack directories rather than a nested-jail run per agent.

**5. It makes the "which layer won?" question answerable.** Provenance today says
`workspace` for everything non-builtin. With packs as named sources, `yolo config diff`
can say *this key came from `pack:acme-rust-review`* — which is the legibility gap
[composed-file-permissions.md §8](composed-file-permissions.md) argues is the real problem
with the whole composed-config story.

**6. Dogfooding.** If the official agents ride the same path as user packs, the user path
cannot rot — a class of bug that *does* happen (the `host_files` feature shipped with the
config loader and the renderer both complete and nothing connecting them, because no
first-party consumer exercised it).

## 4. Cons

**1. It converts compile-time errors into runtime ones.** `builtin.go` is type-checked: a
malformed surface fails `go build`. A pack fails at boot, in a jail, as a warning — and
the codebase's own convention is fail-open (`genStep` downgrades generator errors to
warnings so boot never aborts). Trading "cannot ship" for "warns in a log" on the path
that configures every agent is the single biggest cost, and it is not obviously
recoverable by validation, because the validator would itself be new code.

**2. `Configure*Prism` is not all data, and pretending otherwise is where this breaks.**
Those 2,207 lines contain genuine per-agent *logic*: claude's `mcpServers` tombstone and
LSP-plugin toggles, the `.claude.json` read-modify-write that must never wipe runtime
state, gemini's MCP reconciliation, the copilot LSP reshape, mise's retire surgery on a
workspace file. A pack format expressive enough to hold that becomes a programming
language; one that is not leaves a Go remainder — and then we have *both*. **The honest
version of this proposal splits the four Go artifacts, not all of them:** registry +
surfaces + skills + briefing are data; the boot writers largely are not.

**3. A pack is a supply chain.** Today "yolo supports claude" is a claim about a signed
release. Under packs, agent support is a fetched git ref — and
[agent-config-packs.md](../plans/agent-config-packs.md) already has to solve fetch
credentials, a lockfile, approvals, rollback and an in-jail-writer rule for *user* packs.
Making the official agents ride that machinery means the machinery must be right before
anything works at all, rather than being an opt-in feature that can ship half-good.

**4. Bootstrapping and offline.** The image build is hermetic and offline. Official packs
would have to be embedded (like `bundled_loopholes`), which is fine — but it means there
are now two pack *kinds* (embedded, fetched) with different trust and update stories, and
the "structurally identical" claim quietly weakens.

**5. It is a migration on a system that just stabilized.** The prism cutover completed
2026-07-22; `host_files` shipped 2026-07-25. Both are young, and the audit in
[composed-file-permissions.md §4](composed-file-permissions.md) found five verified
defects still open in what exists. Re-platforming agent support onto packs before those
are fixed risks porting the defects into a new mechanism where they are harder to see.

**6. Versioning gets a new axis.** An official pack and the engine that reads it can now
skew. Today they cannot: they are the same binary. That is a real simplification being
given up, and it wants a compatibility rule (pack schema version, engine minimum) before
the first pack ships rather than after.

---

## 5. What else could be extracted this way

Asked directly, and worth answering independently of the agent question — some of these
are better candidates than agent support is.

**Already data — the proof the pattern works:**

- **Loopholes** (`bundled_loopholes/`). `manifest.jsonc` per capability, `go:embed`,
  bundled and user dirs unioned by one `Discover`. Nothing to do; this is the model.

**Strong candidates (nearly pure data, no per-item logic):**

- **MCP presets.** A named bundle of MCP servers is already just a table; as packs they
  become shareable, which is precisely requirement 2 of the pack ask ("one shared thing
  that works in opencode *and* Pi *and* Claude"). Best first extraction: highest value,
  least logic.
- **Blocked-tool shims** (`derived.go`, 354 lines). Each is `{name, message, suggestion,
  block_flags}` → a generated `/bin/sh` shim. Pure data with a fixed template, and a
  company would plausibly want to *share* a house policy ("no `curl`, use `httpie`").
- **LSP server definitions.** Command + args + file extensions per language. Already
  config-shaped; the Go side is a reshape per agent.
- **Briefings / AGENTS.md fragments.** Already prose. The only reason they are Go is
  `agentsmd.go`'s template; a pack fragment appended is the same operation.
- **Built-in skills.** Already files in a directory embedded with `go:embed`. Moving them
  into `packs/<agent>/skills/` is a path change, not a redesign.

**Weak candidates (logic disguised as data):**

- **`Configure*Prism` writers** — see con 2. The static *declarations* extract; the
  reconciliation logic does not.
- **mise/nix tool tiers.** `mise_tools`, `packages` and `mise.toml` already have a
  documented three-way rule keyed on *whose requirement it is*; a pack layer adds a
  fourth place to put a version pin, which is the anti-pattern that rule exists to
  prevent. Extract only if a pack can be *forbidden* from pinning project tool versions.
- **Anything touching credentials.** MCP servers with `${VAR}` interpolation reach into
  `env_sources`; a pack that can name an env var can name a secret. The scope rule from
  `host_files` (source-bearing entries are user-config-only, enforced by construction) is
  the precedent that would have to be repeated.

**Not candidates:**

- **Mount assembly, the credential boundary, the OAuth broker.** These are the security
  model. `AgentSpec.HostFiles` in particular is a *hard-coded allowlist deliberately made
  unwidenable by config* — the retired `host_claude_files` keys are the counter-example.
  If agent support becomes a pack, **that field must stay in Go**, or the pack system has
  reopened the exact hole `a84b11c` closed. This is the sharpest constraint on the whole
  idea and it should be settled before anything else.

---

## 6. The shape of a decision

Three positions, and they are genuinely different bets rather than points on a line:

| | Bet | Cost if wrong |
|---|---|---|
| **A. Packs are a sharing feature** (the current plan) | user packs only; official agents stay Go | the user path stays under-exercised and can rot (con 6's failure mode, already observed once) |
| **B. Packs are the architecture** (this sketch, full) | everything data, yolo is an engine | runtime failures on the path that configures every agent; a pack format that grows into a language |
| **C. Split by artifact** | registry + surfaces + skills + briefings become packs; boot writers and the credential boundary stay Go | two mechanisms during the transition; the "structurally identical" story is partial |

**C is the one that survives its own objections**, and it is close to what
[agent-config-packs.md §6.1](../plans/agent-config-packs.md) already describes — a pack
contributes a *layer*, not a whole subsystem. The sequencing that follows:

1. Ship packs as designed (item 5) — user scope, opt-in, nothing depends on it.
2. Extract **MCP presets** first. Pure data, immediately shareable, and it exercises the
   pack→prism-layer seam with something that cannot break boot.
3. Fix the five open defects in the existing prism ([§4](composed-file-permissions.md))
   *before* re-platforming anything, so they are not carried forward.
4. Then extract per artifact — skills, briefings, blocked tools, LSP — each independently
   revertable, and each proving the seam a bit harder.
5. Agent registry + surfaces last, and **`HostFiles` never**.

Two questions gate all of it. The first: **does a pack-declared surface get to name a host
file?** If yes, the pack system is a credential-boundary change and needs that scrutiny
first. If no — and no is the right answer — then packs are a layer mechanism, the
boundary stays in Go, and every step above is safe to take one at a time.

The second, from [what-yolo-is.md](what-yolo-is.md): **may pack logic run unsandboxed?**
If yes, packs inherit MCP's trust model (fetch-time human approval), which the lockfile +
approval step in the proposal already provides — consistent, but it means a pack can run
arbitrary code. If no, pack logic is Lua-only and a Go remainder stays. Answering this
decides whether con 2's "Go remainder" is a transitional state or the permanent design.
