---
status: current
stage: CURRENT
next: "Make the boot's drop notice (noteDroppedManagedEntries, internal/entrypoint/prism.go) tell a server the requires_env gate removed from one not in config, as it tells a capability-withheld one since 2026-10-01: a copy a previous render left in the file is still called not in config"
verified: 2026-09-23
verified_commit: 7ad8358c
covers:
  - internal/entrypoint/mcp.go
  - internal/jailcontent/lspplugin.go
  - internal/cli/run/prepare.go
  - internal/agentcfg/luahook/derive.go
  - internal/entrypoint/mcp_wrappers.go
  - internal/entrypoint/packsurfaces.go
  - internal/agentcfg/manifest/
  - packs/claude/derive.lua
  - packs/codex/derive.lua
  - packs/copilot/derive.lua
  - packs/opencode/derive.lua
  - packs/agy/derive.lua
  - packs/pi/derive.lua
tags: [mcp, lsp, packs, prism, config, wrappers]
summary: "How MCP and LSP server config reaches an agent: one canonical server table built in-jail from config (presets expanded, null removes, requires_env gates, no ${VAR} interpolation), filtered by the active source's capabilities, and published as a source that each pack's derive.lua projects into its own tool's dialect — except Claude's LSP, which core renders host-side as one generated yolo-lsp plugin in every skills destination. Plus the node/npx wrappers, what is left of their job now that nix-ld covers the loader, and the gap where a custom server bypasses them."
---

# MCP and LSP configuration — one table, projected per tool

**Status:** Verified 2026-09-23 against `7ad8358c`; the LSP sections were
re-checked on 2026-09-25 against the working tree that deleted the LSP install recipes and
pinned the plugin's injection call site (both uncommitted when this was written, so no SHA).
UNMEASURED: no `claude` session has been started against the generated LSP plugin — the facts
about Claude's plugin loader were read statically from its bundle, and by the no-agent-tests
rule a live check is a human's ([Claude's LSP route](#lsp-claudes-route-is-a-generated-plugin)).
The [derive-boundary section](#what-the-derive-boundary-removes) was re-checked against
`ca86d945` on 2026-09-26, when the capability-delivery rule's own note folded into it.
MEASURED on 2026-10-01 in a nested jail at `d4e435a3`: claude's rendered `mcpServers` drops a
`provides: "web_search"` server under its own login and keeps it under `-p claude=kilo`
([the recording](#a-launch-with-a-provides-server-recorded-2026-10-01)). The same launches found
the boot's drop notice naming the wrong remedy for that drop, fixed the same day: a withheld
server now gets a line of its own (MEASURED in a nested jail at `1493ac51`, the same two launches).
[Pi's MCP files](#pis-mcp-files) were re-checked on 2026-10-01 against pi 0.99.2's installed
source, when the pi pack moved its render to pi's own `mcp.json`.

MCP config is **pack-declarative**. Core builds **one** canonical server table in-jail from
the user's config — presets expanded, custom entries merged, `requires_env` gates applied —
and publishes it as a named source. Each pack's `derive.lua` then *projects* that one table
into its own tool's dialect. Core knows the domain (`mcp_servers`); it never knows the tool.

LSP config follows the same shape for every agent but one. Claude takes a language server only
from a **plugin**, so core renders one plugin of its own, `yolo-lsp`, from the user's
`lsp_servers` table and stages it into every skills destination — the one place in this
pipeline where core writes a vendor's format rather than a pack
([below](#lsp-claudes-route-is-a-generated-plugin)).

Alongside that, three tiny wrapper scripts sit in front of `node` and `npx` for the servers
yolo itself declares.

| Component | Lives in |
| :--- | :--- |
| Building the canonical table, and the LSP table beside it | `internal/entrypoint` (`Env.LoadMCPServers`, `LoadLSPServers`, `Env.LoadMCPPresetNames`) |
| Publishing it as a composition source | `internal/entrypoint` (`packsurfaces.go`), `internal/agentcfg/manifest` (`SourceMCPServers`, `SourceLSPServers`) |
| Capability-driven MCP filtering at the derive boundary | `internal/agentcfg/luahook` (`eligibleMCPServers`, `withoutProvidesKey`, `sourceCapabilities`) |
| The per-tool projection | each pack's `derive.lua`, run in `internal/agentcfg/luahook` |
| Claude's generated LSP plugin | `internal/jailcontent` (`writeLSPPlugin`, `SetLSPServers`, `LSPPluginDir`), called from `PrepareSkills`; the table is injected by `internal/cli/run` (`refreshJailBriefings`, `prepare.go`) |
| The wrapper scripts | `internal/entrypoint` (`GenerateMCPWrappers`, `nodeWrapper`, `npxWrapper`, `chromeWrapper`) |
| Host-side validation of the two config keys | `internal/config` (`validate.go`) |

**Reads with:** [`pack-system.md`](pack-system.md) (surfaces, layers, `derive.lua`, and the
principle that core knows the domain and never the tool),
[`mise-node-dynamic-linking.md`](mise-node-dynamic-linking.md) (the loader story the wrappers
used to carry), `yolo config-ref` (the authority for `mcp_servers`, `mcp_presets` and
`lsp_servers`). A pack-shipped AgentCore web-search entry for every Bedrock profile is proposed,
unbuilt, in [`bedrock-web-search.md`](../design/bedrock-web-search.md).

---

## The pipeline

```
yolo-jail.jsonc: mcp_servers, mcp_presets, lsp_servers
  → validated host-side
  ├─ HOST, every launch: lsp_servers → jailcontent.writeLSPPlugin
  │     → <every skills destination>/yolo-lsp/.claude-plugin/plugin.json
  │     (Claude's LSP route; mounted at ~/.claude/skills for the claude pack)
  → shipped into the jail as JSON env vars
  → in-jail LoadMCPServers(): expand presets → merge custom entries
      (override / add / null-remove) → apply the requires_env gate
      [NO ${VAR} interpolation, at any notch]
  → published once as the canonical source tables (SourceMCPServers, SourceLSPServers)
  → at the derive boundary: drop MCP servers whose `provides` the active
      source already performs, then strip `provides` from the survivors
  → each pack's derive.lua PROJECTS those tables into its tool's format
```

**There are no per-agent `configure_*` functions and no agent registry.** Every MCP
projection, and every LSP projection except Claude's, is the pack's, not core's. Claude's LSP
plugin is the exception, and it names no agent: core writes it into every skills
destination rather than asking which one is Claude's.

### At the host notch

`yolo host apply`, and the automatic apply a wrapped launch runs, run the same pipeline from the
derive boundary on, with the same derives
([OQ-HC1](host-agent-environment.md#oq-hc1)). The tables come from the USER config alone,
composed in `internal/cli`'s `composeHostInputs` and handed to the render as the same wire
variables a launch exports ([HC-D13](../design/host-computed-layer.md#HC-D13)). Three things
differ, each named in the report:

- **No preset is expanded.** Its command is a wrapper only a jail's boot writes
  ([HC-D16](../design/host-computed-layer.md#HC-D16)).
- **An entry whose command or arguments name a jail-only path** (`/workspace`, the jail home,
  `/ctx`, the install prefix, `/run/yolo`) is left out, and a surface whose derive output still
  names one is refused rather than written
  ([HC-D14](../design/host-computed-layer.md#HC-D14)).
- **`requires_env` is asked of each agent's host composition**, the environment `yolo host env
  --agent <agent>` prints, over the invoking shell's.

Claude's `yolo-lsp` plugin stays jail-only (below); Claude at the host gets `ENABLE_LSP_TOOL`
in its settings, and Copilot gets its native LSP file.

### The rules the one loader enforces

Because every projection reads the same table, these apply **identically** to every
MCP-enabled tool.

- **Presets are opt-in and expanded in-jail**, not on the host. Their `command` is the wrapper
  path, baked in — so the servers yolo ships are wrapper-routed by construction.
- **`null` removes** a server or a preset. Same-file "enabled *and* null-removed" is a
  validation error; across scopes (a user config enables, a workspace nulls) it is intentional
  and allowed.
- **`requires_env` gates** a server: if any listed variable is unset or empty in the jail the
  server is dropped with a notice, and otherwise the `requires_env` key itself is **stripped**
  before the entry reaches the tool. The gate is asked **per agent**: a variable a provider
  claims reaches only the agent that selected that provider, in that agent's own env file
  ([the credential gate](providers.md#the-credential-gate)), so the server is written into
  that agent's config and no other, and the notice names the agents it was configured for
  (`loadMCPTables` in `internal/entrypoint`).
- **Key order is insertion order** — presets in the order the config listed them, then custom
  entries — so a projection's output is byte-stable across boots.

> [!WARNING]
> **`${VAR}` is not interpolated by yolo, at any notch. Do not add it back as a convenience.**
> yolo writes the reference **verbatim** into every field and whoever launches the server
> resolves it from their own environment. Two independent structural reasons:
>
> 1. **The value had no layer.** Every other value in a rendered surface has a provenance
>    answer, and the host-render story depends on being able to ask *who set this key*. An
>    interpolated secret entered the file without passing through any layer, so `config diff`
>    could not attribute it and the orphan-key prune could not tell yolo's output from the
>    user's. It was a value sneaking in the side door.
> 2. **It sourced config content from process env at render time.** `env_sources` is a jail
>    *provisioning* input — what the container's environment contains. Using it as a
>    *rendering* input made the bytes written to a config file depend on the ambient
>    environment of whoever ran the render, which is the one input the confinement model
>    deliberately does not treat as configuration.
>
> It was also unnecessary, which is what made the trade one-sided: the boot hydrates every
> `env_sources` variable into the process environment before any generator runs, so it is
> already in the environment of every process the entrypoint spawns — and the consuming agent
> can resolve `${VAR}` itself. yolo resolving first bought nothing and wrote a plaintext secret
> into a file it does not own.
>
> If a real need for declared secret references appears, the honest form is a **layer** with
> provenance, resolved at launch — not a string substitution during render.

Both notches now write the same literal and **nothing warns**. The old host-side `${VAR}`
warning went with the mechanism it papered over: its first remedy was *"put the value in the
file directly"* — advice to inline a live credential into a file a pack may carry — and it was
surface-wide, so it flagged the `env` case, where a literal `${VAR}` is exactly the desired
content, in the same words as the `url` case.

### What the derive boundary removes

One more cut happens after the loader and before any pack sees the table, where the derive
context is built. yolo coined two terms for it, and this section is where both are defined:

- <a id="authentication-source"></a>An **authentication source** is the credential-and-endpoint
  mode an agent runs under for one launch. It is either the provider a profile selects at that
  agent's CLI name, such as Kilo or Z.AI, or, when no profile selects one, the agent's own
  built-in login, such as Claude's subscription or agy's Google sign-in. It is not the agent:
  one agent runs under different sources in different profiles.
- <a id="capability-driven-mcp-delivery"></a>**Capability-driven MCP delivery** is the
  per-render rule that omits an MCP server when the active authentication source already
  performs the job that server declares with `provides`. A search MCP is not delivered to an
  agent whose login searches natively.

The rule compares the server's `provides` with the source's capabilities, and nothing else:

1. A server with no `provides` makes no claim, and always passes.
2. A server whose `provides` names a capability the source declares is dropped. One naming any
   other capability passes. Only the exact-name match is dropped, which keeps the resolver
   generic: a future named job needs no edit to it and none to a pack.
3. `requires_env` is a separate gate, and a server has to pass both. It runs upstream, in the
   loader, so a server whose variable is absent is gone whatever the source declares. Being
   eligible by capability never stands in for the credential.

A selected provider's capabilities are a field of its row in the composed providers table. A
built-in login has no row, which is why a pack declares its agent's native `capabilities` on its
`kind: "program"` contribution (`packdecl.Manifest.NativeCapabilities`). **A selected provider
that the composed table has no row for resolves to the EMPTY set, never to the built-in one.** An
absent row means the launcher composed nothing for that name, not that the agent fell back to its
own login, and borrowing the agent's capabilities there would suppress a server on a source that
never claimed to replace it.

The decision is made per rendered target and follows the selected source, never the agent's name
and never the jail-wide pack set, so changing one agent's profile changes only that target's
result. Because it sits at the context boundary, the one place both production callers pass
through (the surface render and the env composition), no pack can opt out of it and none has to
opt in. Which packs declare which capabilities is pack data: `rg -n web_search packs/*/pack.json`
answers it, and no list here does.

**It is not `required_capabilities`**, though the two share one vocabulary: both read an
`mcp_servers.<name>.provides`, a `providers.<name>.capabilities` in the composed table, and the
`capabilities` a pack declares for its agent's built-in login, so a change to any of those
declarations moves both. `required_capabilities` refuses a launch whose declared requirement
nothing satisfies, judging each agent by the same authentication source this rule reads, any one
agent being enough. It counts every profile of an agent's active set rather than the first alone,
and a provider the user's own config declares capabilities for, whether or not a profile selects
it. This rule chooses among things that already work. Config validation refuses
two `mcp_servers` entries that declare the same `provides`, which keeps that choice unambiguous.

Then `provides` itself is stripped from every surviving entry, unconditionally: it is yolo's
own vocabulary, and a pack that copies an entry verbatim into its agent's file would otherwise
leak it there.

> [!WARNING]
> **Do not move the `provides` strip beside the `requires_env` strip in the loader.** The
> loader runs upstream of the derive boundary, so a `provides` removed there is gone before the
> filter can read it, and capability-driven delivery silently becomes a no-op with every test
> green. The two keys are stripped at two layers on purpose: `requires_env` gates delivery and
> must go first, `provides` feeds the filter and must go after it.

#### A launch with a `provides` server, recorded 2026-10-01

**MEASURED** in a nested podman jail, launched from a throwaway workspace with the binary
`just build-go` made at `d4e435a3`, `YOLO_REPO_ROOT` naming that tree, and an isolated `HOME`
whose user config was `{"packs": ["claude", "kilo"], "env_sources": [{"KILO_API_KEY":
"placeholder-not-a-key"}]}`. The workspace config declared two servers, one claiming the job:

```jsonc
{"mcp_servers": {
  "probe-search": {"command": "/bin/true", "args": ["search"], "provides": "web_search"},
  "probe-plain": {"command": "/bin/true", "args": ["plain"]}}}
```

Each launch ran `jq -c .mcpServers ~/.claude.json` in place of an agent:

```console
$ yolo run --accept-config-changes -- bash -lc 'jq -c .mcpServers ~/.claude.json'
{"probe-plain":{"args":["plain"],"command":"/bin/true"}}
$ yolo run --accept-config-changes -p claude=kilo -- bash -lc 'jq -c .mcpServers ~/.claude.json'
Profile kilo: declared by kilo; claude → provider "kilo", on its "anthropic" endpoint
{"probe-plain":{"args":["plain"],"command":"/bin/true"},"probe-search":{"args":["search"],"command":"/bin/true"}}
```

Under claude's own login, which `packs/claude` declares searches natively, the server claiming
`web_search` is gone. Under Kilo, whose provider row declares no capabilities, it is delivered,
and with `provides` stripped. Without a value for `KILO_API_KEY` the kilo launch refused before
the jail started, and a value in the launching shell alone was refused too, as relayed to no
agent; the placeholder reached no model.

The native launch that ran after the kilo launch, in the same workspace, printed the boot's drop
notice for that entry, and named the wrong remedy for it:

```text
claude/config: dropping from mcpServers (not in config): probe-search — add under `mcp_servers` to keep it, reaching every agent
```

`probe-search` is under `mcp_servers`. It was dropped by the derive boundary, and following the
notice changed nothing. The first native launch in the workspace printed no such line, so the
notice reads the entry the previous render left in `~/.claude.json`.

**Fixed 2026-10-01: a withheld server gets a line of its own.** The render step that runs a
surface's derive records what the boundary withheld from the ctx it handed that derive
(`luahook.WithheldMCPServers`, the filter's own rule, read by `Env.recordMCPWithheld`), and the
drop notice (`noteDroppedManagedEntries`, `internal/entrypoint/prism.go`) splits the entries
leaving the table into the ones withheld and the rest. Only the rest keep the declare-it remedy.
**MEASURED** with the same two launches and configs, in a nested podman jail with the binary
`just build-go` made at `1493ac51`, the native launch after the kilo one now prints, and its
`jq` output is `probe-plain` alone, as before:

```text
claude/config: dropping from mcpServers (in config, withheld by capability): probe-search (provides web_search) — claude's own login does that job itself, so yolo does not deliver it to this agent; it stays declared under `mcp_servers`, and to deliver it anyway, remove its `provides`
```

A selected provider is named as the source instead (`provider "zai" does that job itself`). The
match is by name, in whichever table lost the entry, because core cannot say which table a derive
builds from its MCP servers, so a derive that renamed its servers would leave a withheld one under
the declare-it remedy. No shipped derive renames them. ⚠ A server the `requires_env` gate removed
is not covered: the loader drops it before the derive's table is built, so the boundary records
nothing for it, and a copy a previous render left in the file is still called "not in config",
beside the gate's own `skipped — required env not set` line. Pinned through the boot loop by
`capabilitydropnotice_test.go` (`internal/entrypoint`), and the rule's partition by
`TestWithheldMCPServersIsWhatTheDeriveWasNotHanded` (`internal/agentcfg/luahook`).

### The projection, and how tools differ

Each pack declares its own config surface and a `derive.lua` that reads the canonical table
and returns its tool's shape. **The per-tool table is pack data**: the file paths, the key
names and the schema live in each pack's `pack.json` and `derive.lua`, which are the
authority. What is worth knowing is *how the shapes differ*, because that is what a new
projection has to get right:

- Most tools take a `{command, args, env}` object map under some spelling of `mcpServers`.
- One takes TOML tables instead of JSON objects.
- One flattens `command` plus `args` into a single argv **array** and renames `env`.
- One tool's MCP goes in a *different file* from its permissions, so its projection writes two
  surfaces.
- Pi projects the canonical table into `~/.pi/agent/mcp.json` (`mcpServers`), the file pi's own
  MCP client reads. It is also a file pi writes itself, so the projection merges into it rather
  than replacing it. See [Pi's MCP files](#pis-mcp-files) for the second file it writes and the
  one it retires.

### Pi's MCP files

Pi has had its own MCP client since 0.99.0. It reads `~/.pi/agent/mcp.json` always, and a
project's `.pi/mcp.json` only while pi trusts the project (pi 0.99.2,
`dist/extensions/mcp/config.js`, `loadMcpConfig`). The pi pack writes the first file, writes a
second while pi-subagents is selected, and cleans up the file it wrote before pi had a client.
Each rule below is a surface field in [`packs/pi/pack.json`](../../packs/pi/pack.json),
documented on `manifest.Surface` in [`manifest.go`](../../internal/agentcfg/manifest/manifest.go);
the rulings are [AM-R1 and AM-R2](../design/agent-directory-map.md#13-decision-ledger).

| File | Read by | What yolo does |
| :--- | :--- | :--- |
| `~/.pi/agent/mcp.json` | pi's own MCP client, in every project; pi-subagents through 0.72, and 0.74.0 without pi-mcp-adapter, for an agent's `mcp:` tools | writes your servers at every boot, beside any server or setting already there (`stateful`, `pi/mcp`) |
| `~/.config/mcp/mcp.json` | pi-subagents; also pi-mcp-adapter, as its shared global file | writes the same servers while pi-subagents is in pi's `packages`, and nothing otherwise (`pi/subagents-mcp`) |
| `~/.pi/agent/mcp-adapter.json` | pi-mcp-adapter; pi-subagents 0.73.0 and later while the adapter runs its tools | never writes it; deletes it only while it holds exactly what `mcp.json` holds after the boot's write, which is the copy yolo 0.11 wrote there |

**pi's own file.**

- **It is merged into, never replaced.** pi writes this file too: `pi mcp add` and
  `pi mcp remove`, and `/mcp`'s enable, disable and exposure changes. So the surface is
  `stateful` and its table is not declared in full
  ([CO13](../design/config-ownership-and-promotion.md#co13--how-a-derive-says-it-fills-a-computed-table-in-full--decided)).
  The first boot adopts the servers and top-level settings it finds there as yours; a server or
  setting you change later is captured and survives the next boot; a server yolo stops
  configuring leaves. For a field yolo writes in one of its own servers, the config wins.
  ⚠ A server of yolo's that you disable with `/mcp` and then drop from the config leaves
  `{"enabled": false}` behind, which pi reports as a server with no `command` and skips
  (MEASURED 2026-10-01 with a scratch test through the boot loop, `ConfigurePackSurfaces`; the
  report is pi's `validateMcpServerConfig`, read in pi 0.99.2's `dist/core/mcp-servers.js`).
- **pi never gates this file on trust.** Only a project's `.pi/mcp.json` waits for trust, so the
  servers yolo writes start in every project. yolo never writes a project's `.pi/mcp.json`;
  whether pi loads a repository's own is the pi pack's project-trust posture:
  `defaultProjectTrust: "always"` in a jail, the container being the boundary
  ([`workspace-mcp-sources.md` §1](../design/workspace-mcp-sources.md)), and `"ask"` at the host,
  where a pi with no terminal to ask on treats the project as untrusted (pi 0.99.2,
  `dist/core/project-trust.js`).
- **Variables are pi's to expand, in fewer places than pi-mcp-adapter expands them.** yolo writes
  the table verbatim ([no `${VAR}` interpolation](#the-rules-the-one-loader-enforces)). pi
  expands `${VAR}` and `$VAR` in `env` and `headers` values only, and a variable that is not set
  stops that one server with a named error; in `command`, `args` and `cwd` it expands only a
  leading `~/` (`dist/extensions/mcp/runtime.js`). pi-mcp-adapter also expanded `${VAR}` in
  `args`, to an empty string when unset, so a server that relied on that behaves differently.
- **At the host**, `yolo host apply` writes your `mcp_servers` here per server, beside the
  servers you added with `pi mcp add`, which stay
  ([the host write is per key](host-agent-environment.md#the-host-write-is-per-key)). Under
  `host_management: assert`, a server you drop from `mcp_servers` loses the fields yolo wrote, and
  its emptied entry, `{}`, stays, which pi reports and skips as above (MEASURED 2026-10-01 with a
  scratch test through `RenderHostPack`). ⚠ `yolo host apply --revert` removes the whole
  `mcpServers` key, your servers with yolo's, because its record is kept per top-level key
  (MEASURED the same way, through `RevertHostRender`); its dry run lists `pi/mcp` `mcpServers`
  before anything is removed. Host apply deletes nothing, so a `mcp-adapter.json` at the host is
  left as it is.

**pi-mcp-adapter duplicates pi's own client.** The two cannot share one server set: with the
adapter still in pi's `packages`, each server yolo writes starts twice, pi's copy from
`mcp.json` and the adapter's from `~/.config/mcp/mcp.json` while pi-subagents is selected, or
from an `mcp-adapter.json` the boot kept. yolo does not install the adapter; take
`npm:pi-mcp-adapter` out of whichever pack or settings file lists it.

**pi-subagents' file.** Where pi-subagents finds the servers for an agent's `mcp:` tools
depends on its version (`getConfigPaths` in its `src/runs/shared/mcp-direct-tool-allowlist`):

- **Through 0.72** it reads `~/.config/mcp/mcp.json`, then `~/.pi/agent/mcp.json`, then the
  project's `.mcp.json` and `.pi/mcp.json`, merged by name, and runs the tools only through
  pi-mcp-adapter (read in 0.35.1 and in a 0.71.0 fork).
- **0.73.0** reads `mcp-adapter.json` where it read `mcp.json`, still through the adapter.
- **0.74.0**, with no adapter loaded, takes the tools from pi's own client, whose servers come
  from `mcp.json`; with the adapter loaded it behaves as 0.73.0 (its CHANGELOG).

So `~/.config/mcp/mcp.json` is the one file every version reads while the adapter runs the tools,
and it is still written. The rules for it:

- **Selected** means an entry of pi/settings' `packages` whose name is `pi-subagents`: `npm:`
  with or without a version, a git or URL source ending in `/pi-subagents` (a fork counts), or a
  package-filter object whose `source` is one of those. The list is read from the settings file
  as the same boot rendered it, so a pack's `config-list`, the host's settings and an in-jail
  `pi install` all count (`whenListed`).
- **A file already there is merged into.** The surface is `stateful`, so its first render adopts
  the servers and settings it finds as the user's, and a server added to it later, such as by
  pi-mcp-adapter's "Add globally", survives the next boot. A server yolo stops configuring still
  leaves, because the capture record knows it was yolo's.
- **Deselecting** writes nothing, and removes the file only while it is still exactly yolo's last
  render with no edit captured in it.
- **At the host**, `yolo host apply` skips it with a stated reason, under every
  `host_management` value (`notAtHost`). Host apply writes your `mcp_servers` into pi's own
  `mcp.json` there ([`host-agent-environment.md`'s computed layer at the host](host-agent-environment.md#what-each-surface-gets-at-the-host)),
  and this cross-tool file stays yours.

**The retired copies.** 0.11.0 moved the render from `mcp.json` to `mcp-adapter.json`, and it has
now moved back. A `mcp-adapter.json` holding exactly what `mcp.json` holds after the boot's write
is yolo's own leftover and is deleted (`retireIfMatchesRender`). The comparison is on the decoded
JSON, so key order and indentation do not decide it. A file with one more key, one different
value or a server yolo no longer configures is kept, and so is every copy while `mcp.json` holds
a server or setting of yours, since the two files then differ; pi-mcp-adapter keeps loading a
kept copy until you remove it. The `mcp.json` yolo 0.10.0 wrote is the surface's own file again:
one that holds exactly yolo's render is adopted with nothing of yours in it, so its servers leave
when the config drops them, while one written from MCP settings that have changed since is
adopted as yours, and pi now starts its servers.

**Convergence — how a dropped server disappears** — is the composition engine's job, not a
per-tool one:

- For a composed surface, a fold renders the file from the layers it *has*, so a layer that
  stopped claiming a key simply does not contribute it and the key is not in the output. There
  is nothing to launder.
- For a read-modify-write surface — one holding live agent state, where "regenerate from
  layers" describes the wrong operation — the provenance record carries a `retired:<layer>`
  label for a key no live layer claims any more. Without it, a key yolo wrote would read back
  as the *user's* the moment its pack was dropped, and that is self-reinforcing: once a key
  reads as the user's, every mechanism asking "did yolo write this?" answers no, forever.

> [!WARNING]
> **The `yolo-managed-mcp-servers.json` sidecars are retired, not load-bearing.** They were how
> a pre-prism bespoke writer remembered which server names it owned. Two packs still name one —
> in `retireOnFirstRender`, so the first render through the composition engine **deletes** it.
> A missing file is not an error there; the common case is that there was nothing to clean. Do
> not write a new sidecar to solve a convergence problem: provenance and the fold already
> answer it.

## LSP: Claude's route is a generated plugin

`lsp_servers` is one table, and agents consume it through different mechanisms:

- **Copilot's** native config is the same shape as yolo's table, so its pack's derive projects
  every entry near-verbatim, the in-jail way every MCP projection works.
- **Claude** has no settings-level key for a language server. A server reaches it only through
  a **plugin** — a directory whose manifest declares `lspServers`, each entry a command plus an
  extension-to-language map. So yolo authors one: the **LSP plugin**, a directory named
  `yolo-lsp` whose manifest is rendered from the user's whole `lsp_servers` table. Any language
  the user configures reaches Claude; there is no per-language list and no marketplace.
- **opencode and Pi** can take a server (a native `lsp` key; the `pi-lens` extension's config)
  but no pack writes one yet — see [Unbuilt](#unbuilt). **Codex and agy** have no
  user-configurable LSP surface at all.

**No server is a default, and yolo installs none.** An absent `lsp_servers` renders no plugin
and projects nothing for Copilot; a present one renders config and still installs nothing
([below](#binaries-are-the-users)).

### How the plugin is rendered

The launcher renders it **on the host, on every launch**, while it stages each pack's skills
tree — `writeLSPPlugin`, called from `PrepareSkills`, on both the container backends and
macos-user. It is not a derive and does not run in the jail. The claude pack delivers its
skills staging at `~/.claude/skills`, which Claude reads plugins from, so delivery needs
nothing beyond the mount that already carries skills.

- **Translation.** Each server's `command` passes through and `args` passes through;
  `fileExtensions` is renamed `extensionToLanguage`. Either list is omitted when empty rather
  than written empty, so a hand-read manifest shows only what was declared. **An entry with no
  `command` is skipped**, never rendered: a server yolo cannot spawn would only make Claude
  report a failure yolo could have declined to cause. (Copilot's derive keeps such an entry,
  minus the command.) The manifest also carries a `name` and a `description`.
- **Ownership marker.** The manifest carries yolo's `x-yolo-managed-by` field, which is how the
  host-side adoption walk (`hostskills.IsYoloPluginDir`) knows the directory is yolo's output
  and never offers to migrate it into the user's local pack. The value is duplicated between
  `jailcontent` and `hostskills` rather than imported — `jailcontent` must not depend on the
  host-skills composer — and a drift test pins the two together.
- **Written last.** It goes in after the built-in skills and every pack's skills, so a pack
  that ships a directory of the same name cannot replace it with something Claude would load
  as an LSP declaration.
- **Written into every skills destination**, not only Claude's. Core knows no agents, and
  teaching the staging loop which destination is Claude's would be the agent registry again; a
  destination whose tool does not read plugins simply holds a directory it ignores (INFERRED
  from the other agents' loaders, not measured for each).
- **Removed when empty.** With nothing configured, a stale `yolo-lsp` directory is deleted, so
  dropping the last server stops it reaching Claude. The staging is cleared per launch anyway;
  this covers a caller that stages without clearing.
- **Per-launch state, scoped.** The run pipeline injects the table (`SetLSPServers`) because
  `jailcontent` reads no config itself. That makes it process-wide state, and auto-capture
  runs the same pipeline in-process, so `packRecordScope` snapshots and restores it; its
  source-scan tripwire fails a new record that joins without a declared lifetime.

**Nothing enables the plugin, and nothing needs to.** A plugin auto-loaded from the skills
tree is enabled by default there — the vendor's settings test for that load path is opt-out
rather than opt-in — so the claude derive writes **no `enabledPlugins` at all**. Its one LSP
assertion is `env.ENABLE_LSP_TOOL`, set when any server is configured and absent otherwise.

> [!WARNING]
> **The `.claude-plugin/` segment is mandatory on this load path.** Claude resolves the
> manifest only at `<dir>/.claude-plugin/plugin.json` for a plugin loaded off the skills tree;
> the root-level `plugin.json` fallback that marketplace installs get does not apply. A
> manifest at the directory root is silently not found.

> [!WARNING]
> **Do not inline an `lspServers` table into Claude's `settings.json`.** No such key exists:
> Claude's only producer of LSP configuration takes a plugin, and the one settings-side
> mention of `lspServers` in its bundle is a hook-scanner exclusion list, not a schema entry.
> The plugin is the only route, which is why it exists.

> [!WARNING]
> **Do not bring back per-language `enabledPlugins` toggles, and never tombstone one.** A
> tombstone is a delete aimed at the lower layers, and the lowest layer under the derive is
> the user's own host `settings.json` — so "remove yolo's stale enable" removes the user's
> deliberate enable of the same plugin id. A marketplace id in a host layer is the user's,
> and composition must leave it alone.

**The injection call site is pinned.** The renderer's own tests call `SetLSPServers`
themselves, so they cannot see the run pipeline's one injection line. Two tests in
`internal/cli/run/lspplugininjection_test.go` run the launch's own content path instead —
`stagePacks`, then `refreshJailBriefings`, which calls `PrepareSkills` — and read the manifest
back off disk: `TestLaunchStagesTheLSPPluginFromTheConfig` (a configured server lands in the
staged skills tree) and `TestLaunchWithoutLSPServersStagesNoPlugin` (a second launch whose
config drops the table stages no plugin, the process-wide record deliberately not reset in
between). MEASURED 2026-09-25 in a scratch copy of the tree: deleting the injection line fails
both, and an injection that skips an empty table fails the second.

**The plugin is jail-only.** The host render (`yolo apply --at host`) composes skills
through its own path and does not render `yolo-lsp`.

<a id="binaries-are-the-users"></a>**Binaries are the user's.** The plugin and Copilot's projection name a
`command`, and yolo installs nothing behind it, on any backend: the `command` must already
resolve on `PATH`, and the user brings the server through `mise_tools`, a pack `program`, or an
absolute path.
There used to be an exception — a three-entry recipe table in `internal/config` mapped
`python`, `typescript` and `go` to `pyright`, `typescript-language-server`/`typescript` and
`gopls`, which the bootstrap installed, the agent launchers kept current and a
`~/.yolo-installed-lsps` sentinel uninstalled when dropped. It picked a server for a language,
the opinion [`OQ-LSP1`](#oq-lsp1) removed from Claude's side, and it is deleted with all of that
plumbing, so the recipe names are no longer special: `lsp_servers.python` is a server like any
other.

What the recipe installed before the deletion is **left in place**, not uninstalled. Nothing
declares it any more, so **on the container backends** the boot catalog names it as an orphan,
and `yolo programs remove --apply` (or `programs.autoprune`) collects it. That act reads the
disk rather than a record, which is why no one-shot uninstall was written: the sentinel was a
record, and `internal/entrypoint/orphanremove.go`'s header describes it losing exactly these
entries. A workspace whose `lsp_servers` names one of those servers keeps working on the
leftover until it is collected; after that, the server has to come from `PATH` like every
other.

> [!WARNING]
> **On `macos-user` nothing collects it.** The boot catalog, and the autoprune it ends in, run
> only in the container entrypoint (`entrypoint.Main`), not in `RunDarwinBootstrap`. The
> in-sandbox `yolo programs` refuses, because this backend sets `YOLO_PACK_ROOT` only on the
> bootstrap's own argv and never in the sandbox session (`programsEnv` in
> `internal/cli/programs.go`). A macos-user workspace that had `lsp_servers.python`,
> `typescript` or `go` set while the recipe existed (it installed there from 2026-09-13) keeps
> those packages until they are deleted by hand. The npm packages are under
> `<workspace>/.yolo/home/npm-global/lib/node_modules`, and `gopls` is at
> `<workspace>/.yolo/home/go/bin/gopls` (the sandbox's `~/.npm-global` and `~/go` are links to
> those directories). This is an open gap, not a ruling.

## The node/npx wrapper

`GenerateMCPWrappers` writes three scripts at boot: `node` and `npx` into a `mcp-wrappers`
subdirectory of the local bin dir, and a fatter `chrome-devtools-mcp-wrapper` one level up
beside it.

The `node` and `npx` wrappers now do exactly two things: export the fontconfig variables, and
`exec` the **nix** binary directly.

- **Fontconfig** is chromium's font configuration, unrelated to the loader.
- **`exec`ing `/bin/node` rather than the mise shim** skips mise's per-directory environment
  resolution on every MCP cold start, and gets a binary whose `RPATH` makes it self-contained.

> [!WARNING]
> **The wrappers no longer set `LD_LIBRARY_PATH`, and re-adding it is the whack-a-mole this
> repo deliberately ended.** The loader problem they used to guard — an FHS node losing
> `libstdc++` when an agent scrubbed the child environment before spawning an MCP server — is
> covered *structurally* now, by nix-ld as the FHS ELF interpreter. See
> [`mise-node-dynamic-linking.md`](mise-node-dynamic-linking.md). The baked
> `LD_LIBRARY_PATH` in the image environment stays, for a different class entirely
> (`dlopen`-by-soname from nix-built processes); the **per-call-site re-assertions** were the
> problem, and they are gone.

A wrapper is **self-contained on purpose** — it sets its environment from `$HOME`-relative
constants and `exec`s. It must never call out to `npm config get` or shell out for a path:
that is what makes it robust to being handed a sanitized environment.

The chrome wrapper additionally starts headless chromium if it is not already answering, and
**resolves chromium rather than assuming a path**: a baked image has it at a known
`/usr/bin` symlink, while a launch that delivers the image's bulk extras from the mounted nix
store has no such symlink and a `chromium` on PATH instead. The baked path is tried first so a
jail that bakes behaves exactly as it always did.

> [!NOTE]
> **The chrome wrapper is generated on every container boot and nothing yolo generates spawns
> it.** No preset's `command` names it; its only other references in the tree are the boot
> catalog's declared-orphan entry — which is what stops `catalogLocalBinOrphans` reporting it
> as an unowned file in `~/.local/bin` — and tests. The `chrome-devtools` preset spawns `mcp-wrappers/node`
> directly and lets the MCP server start chromium itself.

### Where the wired preset finds chromium

Because the preset does not go through the fat wrapper, the resolution the wrapper does has to
happen again in the argv the preset emits: `chrome-devtools-mcp` is passed
`--executablePath`, and that value is **resolved when the entry is generated, not written into
the source**. A baked image answers with its `/usr/bin` symlink; a store-delivered launch
answers with the `/run/yolo/packages` farm, which is where `.#yoloImageExtras` puts chromium
once `.#ociImageLean` has stopped baking it. With nothing to resolve the value falls back to
the baked path, so a jail with no chromium fails the way it always did.

The search is the **launcher-collision check's probe path** (`imageProbePath`) rather than a
second search order of its own — one answer to *what does this launch provide*, which already
counts the store farm and excludes the per-home install prefixes. That exclusion is also what
makes the answer available this early: the farm is built by the **first** generator in the
boot, while the install prefixes are still being filled in when config is rendered.

> [!WARNING]
> **Pinning `/usr/bin/chromium` here is a real outage, not a tidiness point.** That symlink is
> created only inside `mkBinPathLinks`' `withChromium` block, and `.#ociImageLean` — the image
> a `YOLO_STORE_PACKAGES=1` launch builds — turns that block off. A pinned argv therefore names
> a path that does not exist on exactly the launches that do have a chromium. See
> [`image-staging-vs-baking.md`](image-staging-vs-baking.md#store-delivered-packages).

### The gap: a custom server bypasses the wrapper

Custom `mcp_servers` entries are stored **verbatim**. Whatever `command` the user wrote is what
the tool gets, and there is **no rewrite step** routing a bare `node` or `npx` through the
wrapper. So a config like

```jsonc
"mcp_servers": {
  "example": {
    "command": "npx",
    "args": ["-y", "some-mcp@latest"],
    "env": { "SOME_API_KEY": "${SOME_API_KEY}" }
  }
}
```

resolves `npx` through PATH to the mise shim, not to the wrapper.

**What that costs is now small, and worth stating precisely** so nobody re-fixes the old
problem. The crash class is gone: nix-ld resolves the FHS node's libraries env-free, so a
scrubbed-environment spawn no longer dies. What a bypassing server misses is the fontconfig
export (which matters only if it drives chromium) and the mise-shim resolution cost on cold
start. The mitigation, if it is ever wanted, is to generalize the preset pattern — rewrite a
bare `node`/`npx` command to the wrapper path inside the one shared loader — which is one
change in one place rather than a call-site hunt.

## What this does not license

- **Not** a filter on a workspace's own MCP config. yolo composes the canonical table as a
  *source*; a repo's `.vscode/mcp.json` or `.mcp.json` reaching the agent is intended, and the
  `/dev/null` shadow that used to blank one of them was removed on 2026-09-22 — see
  [`../design/workspace-mcp-sources.md`](../design/workspace-mcp-sources.md) for the position, the
  measured per-agent file sets, and the one open precedence question.
- **Not** a per-tool branch in core. Core publishes the domain table; the pack projects it.
  The one exception is Claude's LSP plugin, which core renders in a Claude format — and even
  that names no agent, going to every skills destination.
- **Not** a per-language LSP map, a marketplace plugin id, or an `lspServers` key in Claude's
  settings. The plugin is generated from the user's whole table ([`OQ-LSP1`](#oq-lsp1)).
- **Not** an LSP install path — no recipe table, no install list, no sentinel, on any backend.
  yolo renders the config; the binary is the user's ([above](#binaries-are-the-users)).
- **Not** an opinion about which server serves a language. The user's `command` is the choice;
  yolo's job is to deliver it.
- **Not** `${VAR}` interpolation, in either notch, in any field.
- **Not** a new sidecar file for convergence. The fold and the provenance record answer it.
- **Not** an `LD_LIBRARY_PATH` export in a wrapper, or anywhere else per call site.
- **Not** a wrapper that shells out to discover a path. Self-contained or it is not robust to
  the case it exists for.
- **Not** an absolute chromium path written into the `chrome-devtools` argv. Where chromium is
  depends on how the launch got its packages, so the entry resolves it.

## Unbuilt

**LSP producers for opencode and Pi.** opencode's config file is already a pack surface with no
`lsp` producer in its derive; Pi's `pi-lens` extension reads `lsp.servers` from
`~/.pi-lens/config.json`, which no pack writes.

**Removing the `sequential-thinking` preset** is ruled and not built —
[`OQ-MP1`](../design/mcp-presets-removal.md#decision-ledger).

**Open for pi's MCP files**, not yet ruled: whether `~/.config/mcp/mcp.json` is still written now
that pi-subagents 0.74.0 runs `mcp:` tools from pi's own client; whether yolo does anything for a
pi-mcp-adapter user beyond saying the two clients duplicate each other; whether pi gets a version
floor, since a pi older than 0.99.0 has no client and starts none of these servers; whether the
host owns `mcp.json`'s server table whole, as it owns `~/.claude.json`'s, which would end the
emptied entries above and make `--revert`'s whole-table removal take only yolo's, at the cost of
the servers you add with `pi mcp add`; and whether
`yolo host apply` retires a host `mcp-adapter.json` by the same exact-match rule a jail boot uses.

## Current values

Verified at `7ad8358c`; the LSP rows re-checked 2026-09-25 against the working tree. The prose
above explains what each of these is for; this table is the only place the values themselves
are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Config keys | `mcp_servers`, `mcp_presets`, `lsp_servers` | `yolo config-ref` |
| Wire form into the jail | one JSON env var per key | `internal/cli/run/assemble.go` |
| Canonical source names | `mcp_servers`, `lsp_servers` | `manifest.SourceMCPServers`, `SourceLSPServers` |
| Available presets | `chrome-devtools`, `sequential-thinking` | `Env.LoadMCPServers` |
| Wrapper locations | `~/.local/bin/mcp-wrappers/{node,npx}`; `~/.local/bin/chrome-devtools-mcp-wrapper` | `Env.McpWrappersBin`, `GenerateMCPWrappers` |
| What a wrapper exports | `FONTCONFIG_FILE`, `FONTCONFIG_PATH` — and nothing else | `internal/entrypoint/mcp_wrappers.go` |
| What a wrapper execs | the nix `/bin/node` / `/bin/npx` | `internal/entrypoint/mcp_wrappers.go` |
| Chrome debug endpoint defaults | overridable by `CHROME_DEBUG_PORT` / `CHROME_DEBUG_ADDR` | `chromeWrapper` |
| Where the wired `chrome-devtools` argv gets chromium | resolved against what the launch provides, falling back to the baked `/usr/bin` path | `chromiumExecutablePath` |
| Retired sidecar name | `yolo-managed-mcp-servers.json`, deleted on first composed render | each pack's `retireOnFirstRender` |
| Bootstrap-installed MCP packages | gated on the same preset declaration that builds the table | `Env.LoadMCPPresetNames` |
| MCP entry key the capability filter reads, then strips | `provides` | `luahook.eligibleMCPServers`, `withoutProvidesKey` |
| Where a source's capabilities are declared | `providers.<name>.capabilities` for a selected provider (a pack default under the user's override); `capabilities` on a `kind: "program"` contribution for an agent's built-in login | `packdecl.Contribution.Capabilities`, `packdecl.Manifest.NativeCapabilities`; resolved by `luahook.sourceCapabilities` |
| Where capability-driven delivery is pinned | the acceptance cells, each driving `ConfigurePackSurfaces` over the shipped packs and the launcher's composed wire tables | `internal/entrypoint/capabilitymcp_test.go` (re-checked at `ca86d945`) |
| LSP plugin directory name | `yolo-lsp` | `jailcontent.LSPPluginDir` |
| LSP plugin manifest path | `<skills destination>/yolo-lsp/.claude-plugin/plugin.json` | `jailcontent.writeLSPPlugin` |
| Ownership marker | `x-yolo-managed-by: "yolo-jail"` | `jailcontent.yoloPluginManagedBy`, `hostskills.yoloManagedMarker` |
| Claude's LSP switch | `env.ENABLE_LSP_TOOL = "1"` in `settings.json`, only when a server is configured | `packs/claude/derive.lua` |
| Claude Code version the plugin-loader facts were read from | 2.1.278, statically from its bundle | [`OQ-LSP3`](#oq-lsp3) |
| Where the plugin's injection is pinned | `TestLaunchStagesTheLSPPluginFromTheConfig`, `TestLaunchWithoutLSPServersStagesNoPlugin` | `internal/cli/run/lspplugininjection_test.go` |
| Language servers yolo installs | none, on any backend | `internal/entrypoint` (`BootstrapScript` has no LSP arm) |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo, with their original ids.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| `D6` | The bootstrap's npm install for a preset is gated on the **same declaration** that builds the server table | Hardcoding a package list beside the preset table lets the two drift, and the failure is a preset that is configured and whose package was never installed. |
| Capability-driven delivery ([above](#capability-driven-mcp-delivery)) | The filter sits at the derive **context boundary**, never in an agent's `derive.lua`, and keys on the selected **source**, never on the agent | The per-agent `provides == "web_search"` branches it replaced were opt-in, and one had drifted: claude's suppressed web search for every profile that was not bedrock or codex, so a Kilo launch, a source with no native search, lost its search MCP because of the agent it ran under. |
| Principle 2 of [`pack-system.md`](pack-system.md) | Core publishes the **domain** (`mcp_servers`); the pack owns the **tool's dialect** | A per-tool branch in core is the agent registry coming back through a different door. The projection changes when the tool changes, which is the pack's business. |
| <a id="oq-lsp1"></a>[`OQ-LSP1`](#oq-lsp1) | **Option D — generate.** yolo authors ONE plugin whose `lspServers` is rendered from the user's own `lsp_servers` table, and delivers it as content. Never a per-language map, never marketplace plugin ids — neither the three it replaced nor the full official set | A per-language map is yolo picking "the" server for a language, which is the user's choice; marketplace ids also need an install path, and a jail runs no vendor install verb. The ruling said "and enables it", but no enable step exists: the skills-tree load path is opt-out. The same rule deleted the install side: the three-entry recipe table that picked `pyright`, `typescript-language-server` and `gopls`, and its install lists, sentinel and refresh arm, went on 2026-09-25, so yolo installs no language server. |
| <a id="oq-lsp3"></a>[`OQ-LSP3`](#oq-lsp3) | Claude **auto-loads a plugin from `~/.claude/skills/*`** with no marketplace, no `enabledPlugins` entry and no flag; it is **enabled by default** there; ONE plugin may declare MANY servers, keyed by server name. Measured statically against Claude Code 2.1.278 | This is the precondition option D rests on, and it is version-pinned. **Falsifier:** a Claude release that removes the skills-tree or session load arm, or a managed-settings `disableSideloadFlags` in force — shipped on by default, or set by a policy on the user's machine. Anyone revisiting it re-reads the installed version's plugin assembler. Two servers claiming one extension is a warning, not a load failure. |
| Orphan-file cleanup ([`config-migration-to-prism.md`](config-migration-to-prism.md#the-two-sidecars-are-different-kinds-of-thing)) | A retired sidecar is deleted by the surface's owner on first composed render, as **data** rather than Go | It was the last thing left in the per-agent render functions besides the computed layer, and keeping it in Go would keep those functions alive for a one-shot cleanup. |
