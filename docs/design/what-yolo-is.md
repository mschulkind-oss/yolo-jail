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

The problem: data extracts cleanly, but `internal/entrypoint/prism*.go` is ~2,200 lines
of *genuine* per-agent logic (claude's `mcpServers` tombstone and LSP toggles, the
`.claude.json` read-modify-write that must never wipe state, gemini's MCP reconciliation,
mise's retire surgery on a workspace file). A pack format expressive enough to hold that
becomes a programming language; one that isn't leaves a Go remainder.

### The constraints that decide it

1. **The image build is hermetic and offline** — `-mod=vendor`, no network, and the
   `goSrc` fileset only sees `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/`,
   `bundled_loopholes/`. **It cannot compile something fetched from git**, because
   fetching is a network act.
2. **Only two third-party deps are vendored**: `BurntSushi/toml` and `yuin/gopher-lua`.
   Adding a wasm runtime means `go mod vendor` + a fileset update, and `codec.go` already
   forbids new deps by convention.
3. **There IS a Go toolchain in the jail, baked**: `/bin/go` → the nix store, go1.26.4,
   independent of mise (verified). So compile-in-jail is *technically* possible offline.
4. **The nix store is `:ro`**; `~/go` (GOPATH) and the overlay dirs are writable. So a
   build cache has somewhere to live.

### The three code-execution seams that already exist

This is the most useful framing, because **yolo already ships logic three ways** and each
has a real precedent to copy rather than invent:

| Seam | What executes | Sandbox | Precedent quality |
|---|---|---|---|
| **Lua transform** | a script, in-process | **strong** — gopher-lua with only base/table/string/math open; no `os`, `io`, `package`, `debug` (verified) | pure computation only; cannot read a file or spawn |
| **Loopholes** | a host or jail daemon named by `manifest.jsonc` | none — it is a command string | already data-driven, already `go:embed`-bundled, already unions bundled + user dirs in one `Discover` |
| **MCP servers** | **arbitrary executables**, `npm install -g` on first boot | none | **this is already pack-shipped logic in production** |

That third row is the answer to "is there a story for shipping compiled stuff?" — **there
already is one, and it is the one the ecosystem uses.** An MCP server is third-party code,
fetched at runtime, cached in a writable overlay, invoked over a JSON protocol. Same for
the agent lazy-install launchers (`~/.yolo-shims/<agent>` checks `-x $REAL_BIN`, installs
on first use) and the LSP installs (guarded by the `~/.yolo-installed-lsps` sentinel).

### The options, costed

| | Where built | When | Cache | Trust | Verdict |
|---|---|---|---|---|---|
| **(a) Lua**, extended from transforms to whole generators | nowhere — interpreted | at render | none needed | **strong sandbox** | **best first answer.** Already vendored, already the extension story. Limit: the sandbox has no `io`, so a generator that must *read another file* needs yolo to pass it in — which is a fine API constraint, not a blocker |
| **(b) WASM** | pack author's machine | ship prebuilt | content-addressed | strong | **no.** New vendored runtime, new toolchain expectation for pack authors, and no existing precedent here. Revisit only if (a) provably can't express the work |
| **(c) Subprocess + JSON protocol** | pack author's machine, or npm/pip at first run | first use | overlay dir + sentinel | **none** — it is arbitrary code | **the realistic answer for heavy logic.** It is exactly what MCP and the agent launchers already do; the protocol shape is the only new thing |
| **(d) Go plugins** (`plugin.Open`) | must match the host binary exactly | build time | n/a | none | **no.** Requires identical Go version, identical dep versions, and CGO — a nix-built static binary and a fetched plugin will essentially never match. This is the option that sounds cleanest and is least viable |
| **(e) Compile-at-first-run + cache** | in-jail, `/bin/go` | first boot after fetch | `~/go` or a new `GlobalStorage` sibling, keyed by content hash | none | **technically possible** (a baked toolchain exists, offline) **but the worst trade**: minutes of first-boot latency, a compiler in the trust path, per-jail rebuilds, and a cache-invalidation story to get wrong. The precedents (npm/LSP installs) are *downloads*, not compiles |
| **(f) No pack logic** | — | — | — | — | **the honest default.** Packs are data; logic stays in yolo |

### Recommendation

**Layer (f) → (a) → (c), and never (d)/(e).**

1. **Start at (f).** Packs carry data. This is already most of the value (registry,
   surfaces, skills, briefings, MCP presets, blocked tools) and it is where
   [packs-and-the-prism.md](packs-and-the-prism.md) lands too.
2. **Escalate to (a) Lua for computation.** The transform hook already exists, is
   sandboxed, and works on every codec. Widening it from "reshape a surface" to "generate
   a surface" is an incremental change to a mechanism that ships. Most of what looks like
   logic in `prism*.go` is *shaping*, which Lua does well.
3. **Reserve (c) subprocess for genuinely heavy or stateful work**, and accept that it is
   unsandboxed — because MCP already established that boundary. If a pack needs to run a
   real program, it should run it *the way an MCP server runs*, not through a new
   mechanism.
4. **Never (d).** Go plugins against a nix-built binary is a version-lock trap.
5. **Never (e) as the primary story.** Compile-at-first-run means a compiler in the boot
   path of every jail. If a pack needs compiled code, the author compiles it and ships the
   artifact — which is (b) or (c), both of which are better.

**The load-bearing insight:** the question "how do we ship compiled logic?" has an answer
that is not a new subsystem. **yolo does not need to become a build system, because MCP
already made "fetch and run a third-party executable over a JSON protocol" the normal
thing.** A pack that needs real logic should look like an MCP server, not like a plugin.

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
- **The gating question from the pack sketch stands and gets sharper:** may a
  pack-declared surface name a host file? Now also: **may pack logic run unsandboxed?** If
  the answer to the second is "yes, like MCP", then packs inherit MCP's trust model — which
  is *fetch-time human approval*, and the pack plan already has a lockfile and an approval
  step. That is consistent. If the answer is "no", pack logic is Lua-only, and the Go
  remainder stays.
