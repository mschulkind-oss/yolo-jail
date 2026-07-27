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

## 2.5 What a pack contributes — the design layer

**Added 2026-07-26**, because the earlier discussion of "how does pack logic ship" ran
three independent questions together. Disentangling them first, because the answer to each
is different and merging them produces false constraints.

The three questions:

1. **Delivery** — what does the pack contribute, and when must it be in place?
2. **Execution** — if the contribution computes something, where does that computation run?
3. **Reproducibility** — is the contribution inside the unit we can rebuild identically?

I previously argued (2) as though it settled (3): "compose on the host" is a claim about
*execution site and error timing*, and it does **not** answer whether a pack can be an
input to the image. Those are orthogonal. Likewise "packs ship compiled binaries" conflated
(1) and (3) — a compiled MCP server is not pack *content*, it is a *capability
requirement*, and the two bind at different times.

### Phases, not prohibitions

**Refined 2026-07-26.** An earlier draft said pack content as an image input is "off
limits." That framing is wrong in a way that matters: it reads as a rule to enforce, when
the useful thing is a **gradient**.

Better model: a pack has **phases**, and only one of them gets to comment on image input.

| Phase | Runs | May contribute to the image? | Cost of contributing |
|---|---|---|---|
| **provision** | before the image is built | **yes** — this is the phase that speaks to the derivation | a rebuild on adopt/change; the pack joins the image identity |
| **compose** | after the image, before/at jail start | no | none — regenerated every boot |

Nothing is forbidden. A pack *may* put something in `provision` that would have worked in
`compose` — it just pays for it, in rebuild latency and in coupling its own edits to the
image's store path. **Being a good citizen means pushing as much as possible into
`compose`**, and the incentive does that work without a rule: an author who bakes their
prompts into `provision` discovers that editing a prompt now costs a rebuild, and moves it.

This is better than a prohibition for three reasons: the boundary is self-enforcing rather
than validated; it leaves room for the case we haven't thought of (a pack that genuinely
must bake something); and it gives us a place to *report* the cost — "this pack has 3
provision contributions; your next run rebuilds" — instead of a rejection the author has to
work around.

Mapping onto the kinds below: **capability** contributions are `provision`; **content**,
**config values** and **computation** are `compose`. That is the natural assignment, not a
constraint — and it is derivable from the kind, so authors still don't hand-declare a tier.

### The four kinds of contribution

Sorting by kind rather than by pack, because **one pack routinely contributes several
kinds** — this is the correction to "a subset of packs are image inputs":

| Kind | Example | Must be in place by | Image input? | Runs where |
|---|---|---|---|---|
| **Content** | a skill, an AGENTS.md fragment, a briefing | when the agent reads it | no | nowhere — it is files |
| **Config values** | a settings default, a managed key, an MCP entry | when the file is composed | no | nowhere — it is data |
| **Computation** | a generator or transform producing config values | when the file is composed | no | **a real choice** — see (2) |
| **Capability** | a tool, an LSP server, an MCP server, a shared library | before the agent invokes it | **yes, when system-level** — and that is fine, see below | in the jail |

Three of four kinds never touch the image. So the split you're pointing at is real, but:

- **the unit is the contribution, not the pack.** A pack can ship skills (no rebuild) *and*
  declare it needs `ripgrep` (maybe a rebuild). Classifying whole packs would force authors
  to split one logical thing across two packs.
- **it should not be author-declared metadata.** The binding time is *derivable* from the
  kind, because the kind determines what has to be true for the thing to work. If authors
  declare their own tier, we get a fourth place to pin a tool version — which is precisely
  the anti-pattern the existing `packages` / `mise_tools` / project-manifest three-way rule
  exists to prevent (§5, "weak candidates").

### Where the image boundary actually falls

Only the **capability** row can reach the image, and even there it is a tier choice that
already has an established shape:

| Tier | Reproducible | Offline | Cost to adopt | Good for |
|---|---|---|---|---|
| baked into the image | yes | yes | one slow run (automatic) | system-level things: binaries, libraries, anything on PATH for every jail |
| fetched at first use | no | no | one slow first invocation | ecosystem packages that already have a registry (MCP servers, LSP servers, language tooling) |
| composed content | yes | yes | none | files and config values — the other three rows |

### The tension is smaller than it looks — two retractions

An earlier draft of this section said *"a rebuild is a release"* and concluded that packs
should declare a capability and make the user go edit `packages` by hand. **Both halves were
wrong.**

**"A rebuild is a release" — retracted.** I was equating two different things. A *release*
is shipping a new yolo (version bump, host `just install`, everyone else gets it). A
*rebuild* is a local nix build of an image derivation, triggered by a local config change,
affecting one machine. The confusion came from the maintainer's own dev loop, where a
`flake.nix` edit does need a host `just load` to reach their day-to-day jails — but that is
a fact about *developing yolo*, not about *using* it.

For a user, the rebuild path is already the ordinary one: `packages` in config feeds
`YOLO_EXTRA_PACKAGES` into the flake, and the next `yolo` run notices the store path changed
and rebuilds and reloads on its own. **A user adding a package today is a config edit and a
restart — no release, no manual build step.** So "pack needs a capability" lands on a path
that already exists and is already automatic.

**"Declare it and make the user add it to `packages`" — also retracted**, and this is the
worse of the two. It is precisely the opposite of a happy path: the pack knows exactly what
it needs, and we would be making a human transcribe it into a second file to find out
whether it works. That is busywork we invented by mis-modelling the cost.

### What follows instead

**A pack declares its capability needs, and yolo satisfies them the same way it satisfies
`packages` — automatically.** A capability requirement is just another input to the image
derivation, exactly as a config `packages` entry is. Adopting a pack that needs `ripgrep`
should mean: install pack → next run rebuilds → `ripgrep` is there.

The design questions this leaves are real but ordinary, and none of them is "make the user
do it by hand":

- **Latency and visibility.** The first run after adopting a capability-bearing pack is slow.
  That is *already true* of adding a package, so the answer is the existing one: say so
  clearly while it builds. Worth surfacing at pack-install time — "this pack needs `X`; your
  next run will rebuild the image" — so the slow run is expected rather than mysterious.
- **Failure.** A pack naming a package that does not exist must fail *at pack install or
  `yolo check`*, not mid-build. This is the same pre-flight-validation argument as
  everywhere else in this cluster.
- **Trust.** A pack contributing to the image derivation is a real escalation: it can name
  arbitrary nixpkgs attributes. This wants the same approval gate the pack lockfile already
  provides, and it is a genuinely stronger permission than shipping a skill file.
- **Attribution.** When a jail has an unexpected binary on PATH, it must be answerable which
  pack put it there. Same provenance need as `yolo config diff` naming a layer.

**Where the image identity lands.** It does become a function of installed packs — I claimed
that was a cost worth avoiding, but it is simply *correct*: if a pack changes what is in the
environment, a reproducible environment must reflect that. Pretending otherwise would mean
the image no longer describes the jail.

The one thing that stays off-limits is **pack content as an image input** — a pack whose
skills or config values are baked into the store path, so editing a prompt triggers a
rebuild. That is the case [what-yolo-is.md](what-yolo-is.md) rejects, and it stays rejected:
content and config values are read at compose time, and nothing about them needs to be in
the derivation.

## 2.6 Packs consume other packs — typed exports

**Added 2026-07-26**, from: *"I want MCP servers defined in one pack, and then the agent
packs know how to take MCP server exports/configs as an input to insert them into their
agent."*

This is the piece that turns packs from a flat set of layer-contributors into a **typed
graph**, and it is the right shape — because the codebase already proves it works.

### It is already implemented in Go, which is the strongest possible evidence

There is exactly **one canonical MCP form** today (`LoadMCPServers`, `mcp.go:120` — an
ordered map of `name → {command, args, env}`), and each agent applies a **pure projection**
of it into its own dialect:

| Agent | Projection of the same canonical entry |
|---|---|
| **codex** | `{command, args, env}` — near-passthrough (`codex.go:15`) |
| **opencode** | `{type:"local", command:[cmd, ...args], enabled:true, environment:env}` — *renames* `env`, *folds* command+args into one array, *adds* two keys (`agent_configs.go:131`) |
| **gemini** | passthrough, plus synthesized `<lsp>-lsp` entries wrapping each LSP server (`agent_configs.go:167`) |
| **claude** | `mcpServers` in `.claude.json`, with tombstone pruning of managed names |

So "one definition, N agent-specific insertions" is not speculative — it is what the code
does. The pack version simply moves the *definition* into a pack and the *projection* into
the agent pack. The shapes above are also the acceptance test: a design that can't express
opencode's rename-and-fold is not expressive enough.

### The shape

Two new declarations, and they are duals:

- a pack **exports** a typed value: `exports: { mcp_servers: { … } }`
- an agent pack declares an **import** with a projection: "for each `mcp_servers` entry in
  scope, insert it into surface `X` at key `Y`, shaped like this"

The consumer never names the producer. An agent pack imports *the type*, not
`pack:acme-tools` — so adding an MCP pack requires no edit to any agent pack, which is the
whole point. This is the same late-binding the prism already has between `host_files` and
surfaces.

### Why this is a genuine improvement, not just tidier

- **It kills the N×M problem.** Six agents × every MCP source is currently six hand-written
  builders that must each be updated. With exports it is N projections + M definitions.
- **It makes the pack system compositional**, which is the actual justification for packs
  as an *architecture* rather than a sharing feature (§6's bet B vs A). A flat pack set is
  just a config file with extra steps; a typed graph is a system.
- **It generalizes past MCP for free.** LSP servers, blocked tools and tool requirements
  have the same shape: one definition, N agent dialects. `mcp_servers` is the first export
  type, not a special case. Note gemini's row above already *derives* MCP entries from LSP
  definitions — a pack-to-pack projection existing in Go today.
- **It answers "what is an official agent pack for?"** — it is the thing that owns the
  projections. That is a much clearer role than "a bag of that agent's data," and it is
  where the per-agent knowledge legitimately lives.

### What has to be decided before this can be built

Three real questions, all tractable:

1. **Is the projection data or code?** Opencode's rename-and-fold is beyond a key-mapping
   table, so either the projection language handles renames/array-folding/constant-injection
   (a small template language — sufficient for all four cases above), or projections are
   `compose`-phase computation, which is the Lua row of the execution question. **The four
   real projections are the spec**; design against them, not in the abstract.
2. **Who arbitrates collisions?** Two packs exporting `mcp_servers.foo`. The `host_files`
   precedent — deep merge in declaration order for objects, hard error naming both slugs for
   keyless ones — extends here directly.
3. **Does an export imply a capability?** An MCP server definition usually needs the server
   *installed*. That is the `provision`/`compose` phase split above: the export is `compose`
   (config values), any binary it needs is `provision`. Keeping those separable is what lets
   an MCP pack be adopted without a rebuild when the server is an npm package.

### What this leaves for the execution question

With (1) adopted, the only remaining live question from [what-yolo-is.md](what-yolo-is.md)
is the **computation** row: where a pack's generator runs. That is now a much smaller
question than it looked, because it no longer has to carry the image-input problem — and the
argument there (run it where failures are pre-flight rather than fail-open, which the host
side gives you for free) stands on its own.

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
