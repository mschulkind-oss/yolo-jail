# What yolo is — the boundaries, and how logic could ship

**Status:** conceptual, 2026-07-26. Written to answer two questions that kept surfacing
while sketching [packs-and-the-prism.md](packs-and-the-prism.md):

1. **If a pack ships *logic*, how does that get built, shipped, and cached?** Compiled
   binaries? A build system? Go libraries? Compile-at-first-run?
2. **Where does this all fit?** Is the config system part of yolo, a separate library, or
   *is* it yolo? Are we just "sandvault on macos-user", or "sandvault + nix"? Is nix
   separable? **The proposed test: could you use the config system without a jail?**

Everything below is checked against the tree or probed in a live jail.

---

## Part 1 — The standalone test, answered

**The test passes, and more cleanly than expected.**

`internal/agentcfg` — the whole composition engine. Its complete transitive first-party
dependency set, per `go list -deps ./internal/agentcfg/...`:

```
internal/agentcfg/{codec,manifest,luahook}   # its own subpackages
internal/jsonx  internal/tomlx               # codec helpers
```

That is all of it, and the only third-party dep in the tree is `yuin/gopher-lua`. **Zero**
references to `internal/cli`, `internal/entrypoint`, `internal/paths`, `internal/config`,
mounts, containers, or anything jail-shaped. It is already a library that happens to live
in this repo.

And it *runs* jail-free today: `yolo config render pi` executes host-side, composes from
the real `~/.pi/agent/settings.json`, and prints the result. No `/ctx` mount, no
container — `renderSurface` just does `os.ReadFile(expandHome(s.Path))`
(`internal/cli/config.go:188`).

**So the answer to "could you use it without a jail?" is yes for the engine** — but with
one honest caveat that the import graph hides: **the built-in surface *data* is not
jail-independent.** `internal/agentcfg/builtin.go:104` hardcodes the literal path
`/workspace` in claude's `defaults` and `managed` (`projects["/workspace"]`). The engine
does not know what a jail is; the shipped surface list absolutely does. So extraction is
"engine yes, built-ins no" — which is itself an argument for surfaces-as-data (see Part 2),
since the fix is to parameterize the surface, not the engine.

What the jail adds beyond that is not capability but *two sides*:

| Concern | With a jail | Without |
|---|---|---|
| where the `host` layer comes from | a `:ro` mount at `/ctx/host-<agent>/…` | just the real host file |
| where the output goes | the jail's writable overlay | your actual `~/.claude/` — you are composing over your own config |
| the credential boundary | meaningful: two sides, one of which must not see the other's secrets | **meaningless** — there is only one side |
| capture sidecars | per-workspace, agent edits vs yolo renders | still coherent, but it is now *your* edits vs the tool's |

That last row is the real finding: **composition is jail-independent; the credential
boundary is not.** A standalone "compose my agent config from layers" tool is a coherent
product — it is roughly *chezmoi/home-manager for agent config*. It just isn't a
*sandbox* feature, and the `host_files` user-scope-only rule would have nothing to enforce.

### The dependency graph

```
   ┌──────────────────────┐
   │ jsonx, tomlx         │  leaf codec helpers
   └──────────▲───────────┘
        ┌─────┴────────┐
        │ agentcfg     │  library. no jail deps in code.
        │ (+codec,     │  ← extractable TODAY
        │  manifest,   │  ⚠ builtin.go's surface DATA hardcodes /workspace
        │  luahook)    │
        └──────▲───────┘
               │ consumed by
   ┌───────────┴────────────┐
   │ entrypoint/prism*.go   │  needs: /ctx mounts, overlay dirs, sidecar dir,
   │ cli/config.go          │         jail home, frozen -e env
   └───────────▲────────────┘
               │
   ┌───────────┴────────────────────────────────────┐
   │ sandbox + mounts (cli/run)                     │  ← the actual product
   │   ├─ nix image (flake.nix)                     │  ← swappable in principle
   │   ├─ loopholes (host↔jail capability bridges)  │  ← needs the boundary
   │   └─ agent registry (agents.go)                │  ← needs both
   └────────────────────────────────────────────────┘
```

Reading it: **agentcfg is a leaf.** Everything else depends on the boundary existing.
`nix` is genuinely separable in the sense that another image format could supply the same
binaries — but it is load-bearing for a property nothing else here provides: a
*reproducible* environment, which is what makes "the same jail on two machines" true.

### So what *is* yolo?

Honestly: **yolo is a sandbox product with an unusually good config-composition engine
inside it.** Three things, only two of which are core:

1. **The boundary** (mounts, `:ro` home, credential omission, loopholes) — this is the
   product. It is what "sandvault" comparisons are about.
2. **Reproducibility** (nix image, pinned packages) — the differentiator that makes the
   boundary *useful* rather than merely restrictive. Without it you have a sandbox you
   cannot recreate.
3. **Config composition** (the prism) — **incidental to the sandbox, and separable.** It
   exists because agents need config *inside* the boundary and their host config must not
   simply be copied in. But the engine itself knows nothing about any of that.

**Are we sandvault + nix?** Partly, and the honest differentiators are (a) the
composition engine, (b) the loophole protocol — a general host-capability bridge, not a
fixed allowlist — and (c) multi-agent support as data rather than per-tool integrations.
None of those are sandboxing. If the sandbox were commoditized tomorrow, (a) and (b) are
what would remain interesting.

**The uncomfortable implication, stated plainly:** if agentcfg is a leaf library and the
composition system is separable, then the pack system is *also* not fundamentally a jail
feature. It is an agent-config distribution mechanism that yolo happens to be a good host
for. That is worth knowing before deciding how much of yolo becomes packs.

---

## Part 2 — How pack logic could ship

The problem: data extracts cleanly, but `internal/entrypoint/prism*.go` is 917 non-test lines (the 2,207 figure counts `_test.go`)
of *genuine* per-agent logic (claude's `mcpServers` tombstone and LSP toggles, the
`.claude.json` read-modify-write that must never wipe state, gemini's MCP reconciliation,
mise's retire surgery on a workspace file). A pack format expressive enough to hold that
becomes a programming language; one that isn't leaves a Go remainder.

### First: three questions, not one

**Corrected 2026-07-26.** Everything below originally treated "how does pack logic ship" as
one question. It is three, and they are orthogonal:

1. **Delivery** — what does a pack contribute, and when must it be in place?
2. **Execution** — where does a pack's computation run?
3. **Reproducibility** — is the contribution inside the unit we can rebuild identically?

My "compose on the host" argument answers **(2) only**. It is a claim about execution site
and error timing; it says nothing about whether a pack can be an input to the image. And the
option table below silently mixed (1) and (3) — treating a compiled binary as pack *content*
when it is really a *capability requirement*, which binds at a different time.

**(1) and (3) are answered in [packs-and-the-prism.md §2.5](packs-and-the-prism.md)**: four
contribution kinds (content, config values, computation, capability), of which only
*capability* can reach the image, and the classification is per-**contribution**, not
per-pack — one pack routinely ships several kinds. A system-level capability **is** an image
input, and yolo should satisfy it automatically the way a config `packages` entry already is
— the image identity becoming a function of installed packs is correct, not a cost.

**This section is now only about (2).** Read it that way; it does not decide delivery.

### Which of these are actually constraints?

**Added 2026-07-26, on the note that architecture is still open.** The list below was
originally written as "constraints," and that framing smuggled in a bias toward the status
quo. Separating them honestly:

| Stated as a constraint | Really? |
|---|---|
| `CGO_ENABLED=0` kills Go plugins | **a choice.** We set it. It could be flipped |
| `vendor/` is small, new deps are costly | **a choice** (a convention, even) |
| `genStep` is fail-open so pack errors become warnings | **a choice, and it was the wrong one — RULED 2026-07-26: failures become fatal and halting.** See [../plans/open-rulings.md](../plans/open-rulings.md) ruling 5 |
| the nix store is `:ro`, GOPATH is writable | **inherent** to the mount design |
| the *compile* inside a nix sandbox has no network | **inherent** to `sandbox = true` |
| **an in-jail render can only use what's in the jail** | **inherent** — and the one that matters |

Only the last three survive, and flipping the first three does not rescue (d) or (e)
anyway — the reasons they lose are *coupling* (a pack edit forcing an image rebuild) and
*failure surface* (a compiler behind a warning-only handler), not the settings. So the
ranking is stable under "we can change the architecture."

But dropping the status-quo bias does surface something the original table missed
entirely, and it beats every row in it.

### The option the constraint-framing hid: compose on the host

Composition **never probes the running container.** I checked every `computed` layer
producer (`prism.go:448`, `agent_configs.go:167`): they read config, env vars, and
computed paths, and that is all — no `os.Stat`, no `exec.Command`, no `LookPath` anywhere
in the layer construction. Composition is a **pure function of config**.

That means *where* it runs is a free choice, not a requirement — and two things already
prove the host side works: `yolo config render` composes host-side today, and the jail home
is a host directory (`paths.GlobalHome()`) that yolo writes into before the container
starts.

Moving composition host-side changes the pack-logic question qualitatively:

- **Pack logic never enters the jail.** It runs on the host, before the container exists,
  and only its *output* is mounted in. The jail-side trust question — "may a pack run
  arbitrary code next to the agent?" — **stops being asked**, because there is no jail yet.
- **Failures become pre-flight, not fail-open.** A bad pack fails at `yolo check` or at run
  assembly, where erroring is normal, instead of at `genStep` where it downgrades to a
  warning and the agent silently gets unconfigured files. This retires con 1 of the packs
  sketch — the single biggest cost — without needing a validator.
- **Compiled pack logic becomes ordinary.** The host has a real toolchain, a network, and
  no hermeticity requirement. Option (c) reduces to "yolo runs a host subprocess," which is
  what it already does for loophole `host_daemon`s.
- **`readonly` gets easier too.** If output is composed before the container starts, it can
  be mounted `:ro` — which is exactly what work item 3.2 wants and currently can't have,
  because you cannot compose *into* a `:ro` mount.

The cost, stated plainly: **it breaks in-jail re-render.** Today an agent can edit a
composed file and yolo reconciles on the next boot from inside. Host-side composition means
the reconcile has to happen host-side too, so `last_render`/overlay capture must be readable
from the host — which it is (same bind-mounted dir), but the *timing* changes: capture would
move to run-teardown or the next run's assembly. That is work item 3.3's question arriving a
different way. It also does not fit **macos-user**, where there is no mount step at all and
"host" and "jail" are the same filesystem.

**This is worth designing before choosing a pack-logic mechanism**, because it dissolves
most of the mechanism question rather than answering it.

### The constraints that decide the in-jail case

1. **The image build is hermetic** — `CGO_ENABLED=0`, `-mod=vendor`, `-trimpath`, and the
   `goSrc` fileset only sees `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/`,
   `bundled_loopholes/` (`flake.nix:61-77`, `100-112`).

   ⚠ **Corrected 2026-07-26.** An earlier draft said this "cannot compile something
   fetched from git, because fetching is a network act." That is wrong, and the distinction
   matters. Nix hermeticity is *no network in ordinary derivations*, **not** no network at
   all: fixed-output derivations and builtin fetchers are allowed to reach out because
   their output hash pins the result. Probed in this jail — `builtins.fetchTarball` with a
   deliberately wrong `sha256` returns a **hash mismatch**, i.e. it fetched:

   ```
   specified: sha256:00000000000000000000000000000000
   got:       sha256:13y49fs389xasgv7v64bwyk76yjl6k1f35qs8sqq1aw2vfy1wfxj
   ```

   And `flake.nix` **already relies on this** (`builtins.fetchTarball` at `:214`/`:268`
   for pinned-nixpkgs specs, `fetchurl {url; hash;}` at `:223`/`:284`). So a hash-pinned
   pack *could* be fetched and compiled at image-build time. The reason to reject it is
   **cost and coupling, not impossibility**: the pack becomes part of the image's nix store
   path, so every pack edit is a full image rebuild plus a host `just load` — which
   destroys the entire point of packs (change config without a release). Secondarily,
   `sandbox = true` means the *compile* still has no network, so the pack would need its
   own vendored deps or a second FOD.

   **Note the scope of that conclusion**, per the three-questions split above: it rejects
   *making pack **content** an image input* — baking skills or config values into the store
   path, so editing a prompt forces a rebuild. That is question (3). It does **not** mean a
   pack can never contribute to the image: a pack needing a system **capability** should feed
   the derivation exactly as a config `packages` entry does, which needs no FOD at all
   ([packs-and-the-prism.md §2.5](packs-and-the-prism.md)).
2. **Only two third-party deps are vendored**: `BurntSushi/toml` and `yuin/gopher-lua`.
   Adding a wasm runtime means `go mod vendor` + a fileset update, and `codec.go` already
   forbids new deps by convention.
3. **There IS a Go toolchain in the jail, baked**: `/bin/go` → the nix store, go1.26.4,
   independent of mise (verified). `gcc`, `node` and `python3` are baked too. So
   compile-in-jail is *technically* possible offline. Note the toolchain is baked **for the
   user** (`flake.nix:659` — "no mise download on first use"); yolo itself never invokes it.
4. **The nix store is `:ro`**; `~/go` (GOPATH) and `~/.cache/go-build` are writable and
   **persist across workspaces** (the latter is `paths.GlobalCache()`, bind-mounted, ~4 GB
   today). So a build cache has somewhere to live, and it is shared — which cuts both ways.
5. **Shipped binaries are `CGO_ENABLED=0` and `-trimpath`** (read off the baked `/bin/yolo`
   with `go version -m`). This is what kills Go plugins outright — see (d).

### The three code-execution seams that already exist

This is the most useful framing, because **yolo already ships logic three ways** and each
has a real precedent to copy rather than invent:

| Seam | What executes | Sandbox | Precedent quality |
|---|---|---|---|
| **Lua transform** | a script, in-process | **strong** — gopher-lua with only base/table/string/math open; no `os`, `io`, `package`, `debug` (verified) | pure computation only; cannot read a file or spawn |
| **Loopholes** | a host or jail daemon named by `manifest.jsonc` | none — but it is an **argv `[]string`**, not a shell string (`loopholes.go:94`), so no shell injection surface | stronger than it looks: the loophole dir is bind-mounted `:ro` at `/etc/yolo-jail/loopholes/<name>` (`runtime.go:40`), and **a loophole shipping its own script and having it executed is a tested contract** — `runtime_test.go:189` uses `{"python3", "/etc/yolo-jail/loopholes/jd-mod/jail.py"}`. User loopholes live in a jail-writable dir |
| **MCP servers** | **arbitrary executables**, `npm install -g` on first boot | none | **this is already pack-shipped logic in production** |

That third row is the answer to "is there a story for shipping compiled stuff?" — **there
already is one, and it is the one the ecosystem uses.** An MCP server is third-party code,
fetched at runtime, cached in a writable overlay, invoked over a JSON protocol. Same for
the agent lazy-install launchers (`~/.yolo-shims/<agent>` checks `-x $REAL_BIN`, installs
on first use) and the LSP installs (guarded by the `~/.yolo-installed-lsps` sentinel).

### The options, costed

**Scope reminder:** these are answers to question (2), *where does a pack's computation
run*. Rows (b)/(d)/(e) also carry delivery baggage, which is why they lose — but they lose
on the delivery axis, not the execution one.

| | Where built | When | Cache | Trust | Verdict |
|---|---|---|---|---|---|
| **(a) Lua**, extended from transforms to whole generators | nowhere — interpreted | at render | none needed | **strong sandbox** — `openSandboxLibs` opens only base/table/string/math, then nils `os io require package load loadstring loadfile dofile dostring collectgarbage`; 5s instruction budget. No file read, no spawn, no clock, no randomness | **best first answer.** Already vendored, already the extension story. Limit: the sandbox has no `io`, so a generator that must *read another file* needs yolo to pass it in — a fine API constraint, not a blocker. **Its first task is not new work but a fix**: `Surface.Transform` is already declared, documented and parsed, yet never reaches the compose path (work item 1.9) |
| **(b) WASM** | pack author's machine | ship prebuilt | content-addressed | strong | **no.** Vendoring a pure-Go runtime (wazero) roughly *doubles* `vendor/` from its current 7.9 MB and adds ~1 MB to each of the **four** shipped binaries, plus a `goSrc` fileset update, plus a toolchain expectation on pack authors (no `tinygo`/`rustc` in the image), and there is no precedent here. Revisit only if (a) provably can't express the work |
| **(c) Subprocess + JSON protocol** | pack author's machine, or npm/pip at first run | first use | overlay dir + sentinel | **none** — it is arbitrary code | **the realistic answer for heavy logic**, and it has *three* precedents, not one: MCP servers, the loophole `jail_daemon` script contract, and the agent lazy-launchers. The protocol shape is the only genuinely new thing |
| **(d) Go plugins** (`plugin.Open`) | must match the host binary exactly | build time | n/a | none | **no, and this one is *proven*, not predicted.** `plugin` requires CGO; the shipped binaries are `CGO_ENABLED=0`, so `plugin.Open` returns **`plugin: not implemented`** — it cannot work at all without changing how yolo is built. Even with CGO forced on, the shipped `-trimpath` makes a non-trimpath plugin fail with *"plugin was built with a different version of package internal/goarch"*. Sounds cleanest, is dead on arrival |
| **(e) Compile-at-first-run + cache** | in-jail `/bin/go`, or a nix FOD at image build (see constraint 1) | first boot after fetch | `~/.cache/go-build` — but it is **shared across all workspaces**, so a per-pack cache is a cross-workspace side channel | none — a compiler in the trust path | **worst trade.** Works offline for a vendored pack (~2s cold for a trivial package, minutes for real code). Killed by a second-order problem: `genStep` is **fail-open**, so a compile failure downgrades to a warning and the jail boots with the pack silently inert — the same failure mode as con 1, but now with a compiler's error surface. The precedents (npm/LSP installs) are *downloads*, not compiles |
| **(f) No pack logic** | — | — | — | — | **the honest default.** Packs are data; logic stays in yolo |
| **(g) Compose on the host** (see above) | host, before the container exists | at run assembly | normal host build cache; no jail involvement | **the question doesn't arise** — no jail exists yet | **the option the others were competing for.** Makes (c) trivial and retires the fail-open failure mode. Cost: in-jail re-render and macos-user both need rethinking |

### Recommendation

**Design (g) first; ship (f) → (a) → (c) within it. Never (d)/(e).**

The ordering matters more than the list. (g) is not a sixth mechanism — it is a decision
about *where the mechanism runs*, and it makes the others cheaper:

1. **Decide (g) before anything else.** If composition moves host-side, pack logic never
   crosses the boundary, failures become pre-flight, and "how do we sandbox pack logic?"
   stops being a question. If it stays in-jail, every row above keeps its jail-side trust
   cost. This is one decision that reprices the whole table, so it should not be made
   implicitly by continuing to render in `entrypoint`.
2. **Ship (f).** Packs carry data. Already most of the value (registry, surfaces, skills,
   briefings, MCP presets, blocked tools), and where
   [packs-and-the-prism.md](packs-and-the-prism.md) lands too. Unaffected by (g).
3. **Escalate to (a) Lua for shaping.** Sandboxed, vendored, works on every codec. Its
   first task is a *fix*, not new work: `Surface.Transform` is declared, documented and
   parsed but never reaches the compose path (item 1.9). Also unaffected by (g) — a pure
   function runs the same on either side.
4. **Reserve (c) subprocess for heavy or stateful work.** Under (g) this is easy and
   ordinary — a host subprocess, like a loophole `host_daemon`. Under in-jail rendering it
   means accepting MCP's unsandboxed boundary. **This is the row (g) changes most**, and the
   reason to decide (g) first.
5. **Never (d).** Under the old framing I called this proven-impossible because
   `CGO_ENABLED=0` makes `plugin.Open` return `plugin: not implemented`. With architecture
   open, that's a flag we could flip — so the honest reason is the *version lock*: a plugin
   must match the host binary's Go version and every dep version exactly, and yolo is built
   by nix while packs are not. `-trimpath` alone already breaks it
   (*"built with a different version of package internal/goarch"*). Flipping CGO would buy a
   mechanism that still fails on every toolchain bump.
6. **Never (e).** Also not about settings: a compiler in the config path means pack authors
   ship source, every consumer compiles it, and the failure lands wherever composition runs.
   The nix-FOD variant is possible but makes each pack edit an image rebuild plus a host
   `just load` — which defeats the purpose of packs. Under (g) the whole idea is moot,
   because the host can just run a prebuilt binary.

**The load-bearing insight, restated for an open architecture:** the question "how do we
ship compiled logic?" is downstream of a question nobody asked — **where does composition
run?** Answer that first and most of the mechanism question dissolves: on the host, pack
logic is just a subprocess and needs no sandbox story at all. In the jail, yolo still
doesn't need to become a build system, because MCP already made "fetch and run a
third-party executable over a JSON protocol" normal. Either way, a pack that needs real
logic should look like an MCP server, not like a plugin.

---

## What this means for the pack decision

Three consequences that change the ranking in
[packs-and-the-prism.md §6](packs-and-the-prism.md):

- **The data/logic split is sharper than "split by artifact" implied.** It is not that
  some artifacts are data and some are logic — it is that *composition shaping* is Lua-able
  and *stateful reconciliation* is not. `writeClaudeJSON` is the clearest example: a
  read-modify-write that must never wipe 33 keys of agent state is not a transform.
- **`agentcfg` being a leaf library is an argument for extracting it, not for packs.** If
  the goal is reuse, the extractable unit is the *engine*, not the *agent support*. Those
  are different projects and only one of them needs the pack machinery.
- **The hardcoded `/workspace` in `builtin.go` is a small, concrete first step** that
  serves both directions. Surfaces need a substitution mechanism (`${workspace}`) before
  they can be pack data *or* before the engine is reusable outside a jail. It is the
  cheapest thing on this page that de-risks the expensive things.
- **Host-side composition is a bigger lever than the pack format.** Because composition
  never probes the container, moving it host-side is *available*, and it retires con 1 of
  the packs sketch (runtime-instead-of-compile-time errors) by making pack failures
  pre-flight. If packs are going to happen, this ordering — move composition, then add
  packs — is strictly better than adding packs to an in-jail renderer and then discovering
  the error-surface problem.
- **The gating question from the pack sketch stands and gets sharper:** may a
  pack-declared surface name a host file? Now also: **may pack logic run unsandboxed?** If
  the answer to the second is "yes, like MCP", then packs inherit MCP's trust model — which
  is *fetch-time human approval*, and the pack plan already has a lockfile and an approval
  step. That is consistent. If the answer is "no", pack logic is Lua-only, and the Go
  remainder stays.
