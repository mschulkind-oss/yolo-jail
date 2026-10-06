---
status: current
stage: DESIGN
next: "Re-verify the prose in full against the tree; it was last verified against 7ad8358c, 2026-09-23"
verified: 2026-09-23
verified_commit: 7ad8358c
covers:
  - internal/entrypoint/mcp.go
  - internal/packload/mcpcompose.go
  - packs/chrome-devtools/pack.json
  - packs/chrome-devtools/bin/chrome-devtools-mcp-wrapper
  - internal/jailcontent/lspplugin.go
  - internal/cli/applyhostlspplugin.go
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
summary: "How MCP and LSP server config reaches an agent: one canonical server table built from config and each selected pack's `mcp` entries (composed on the host for the notch's home, then presets expanded in-jail, null removes, requires_env gates, no ${VAR} interpolation), filtered by the active source's capabilities, and published as a source that each pack's derive.lua projects into its own tool's dialect — except Claude's LSP, which core renders host-side as one generated yolo-lsp plugin in every skills destination, at every launch and by `yolo host apply`. Plus the node/npx wrappers, what is left of their job now that nix-ld covers the loader, and the gap where a custom server bypasses them."
---

# MCP and LSP configuration — one table, projected per tool

**Status:** Verified 2026-09-23 against `7ad8358c`; the LSP sections were
re-checked on 2026-09-25 against the working tree that deleted the LSP install recipes and
pinned the plugin's injection call site (both uncommitted when this was written, so no SHA).
UNMEASURED: no `claude` session has been started against the generated LSP plugin — the facts
about Claude's plugin loader were read statically from its bundle, and by the no-agent-tests
rule a live check is a human's ([Claude's LSP route](#lsp-claudes-route-is-a-generated-plugin)).
`yolo host apply` writes the plugin into the real home since 2026-10-04
([at the host](#at-the-host-yolo-host-apply-writes-it)); that half is covered by unit tests
that drive the apply into a temporary home, and no `claude plugin validate` has been run
against a plugin it wrote.
The [derive-boundary section](#what-the-derive-boundary-removes) was re-checked against
`ca86d945` on 2026-09-26, when the capability-delivery rule's own note folded into it.
MEASURED on 2026-10-01 in a nested jail at `d4e435a3`: claude's rendered `mcpServers` drops a
`provides: "web_search"` server under its own login and keeps it under `-p claude=kilo`
([the recording](#a-launch-with-a-provides-server-recorded-2026-10-01)). The same launches found
the boot's drop notice naming the wrong remedy for that drop, fixed the same day: a withheld
server now gets a line of its own (MEASURED in a nested jail at `1493ac51`, the same two launches).
[Pi's MCP files](#pis-mcp-files) were re-checked on 2026-10-01 against pi 0.99.2's installed
source, when the pi pack moved its render to pi's own `mcp.json`.
[A pack's servers](#a-packs-servers-the-mcp-kind) and [the chrome-devtools
pack](#the-chrome-devtools-pack) were added on 2026-10-05 with the kind and the pack
([mcp-presets-removal.md §13](../design/mcp-presets-removal.md#13-what-i-would-build-in-order) steps
1 and 2). MEASURED once, in this repository's development jail on 2026-10-05: the wrapper,
started under `env -i` with only `HOME`, `PATH=/usr/bin:/bin` and the npm prefix set, handed
`--executablePath /usr/bin/chromium` to chrome-devtools-mcp 1.10.1, passed the autonomous
posture's flags by hand (no launcher in that home), and the server answered `initialize` and a
`list_pages` call with `about:blank`. UNMEASURED: no agent has been started against an entry the
pack composed, at any notch, and nothing has run on a Mac; unit tests run the wrapper against
stand-ins and `yolo host apply --assert` against a fake Node distribution.

MCP config is **pack-declarative**. Core builds **one** canonical server table from the
user's config and the selected packs' `mcp` entries — pack entries under the user's, presets
expanded, custom entries merged, `requires_env` gates applied — and publishes it as a named
source. Each pack's `derive.lua` then *projects* that one table
into its own tool's dialect. Core knows the domain (`mcp_servers`); it never knows the tool.

LSP config follows the same shape for every agent but one. Claude takes a language server only
from a **plugin**, so core renders one plugin of its own, `yolo-lsp`, from the user's
`lsp_servers` table and writes it into every skills destination, at every launch and at
`yolo host apply` — the one place in this pipeline where core writes a vendor's format rather
than a pack
([below](#lsp-claudes-route-is-a-generated-plugin)).

Alongside that, three tiny wrapper scripts sit in front of `node` and `npx` for the servers
yolo itself declares.

| Component | Lives in |
| :--- | :--- |
| Building the canonical table, and the LSP table beside it | `internal/entrypoint` (`Env.LoadMCPServers`, `LoadLSPServers`, `Env.LoadMCPPresetNames`) |
| Publishing it as a composition source | `internal/entrypoint` (`packsurfaces.go`), `internal/agentcfg/manifest` (`SourceMCPServers`, `SourceLSPServers`) |
| Capability-driven MCP filtering at the derive boundary | `internal/agentcfg/luahook` (`eligibleMCPServers`, `withoutProvidesKey`, `sourceCapabilities`) |
| The per-tool projection | each pack's `derive.lua`, run in `internal/agentcfg/luahook` |
| Claude's generated LSP plugin | `internal/jailcontent` (`RenderLSPPlugin`, `writeLSPPlugin`, `IsLSPPlugin`, `SetLSPServers`, `LSPPluginDir`), called from `PrepareSkills`; the table is injected by `internal/cli/run` (`refreshJailBriefings`, `prepare.go`). At the host, `internal/cli` (`applyHostLSPPlugin`, called from `applyHostSkills`) |
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
  ├─ HOST, every launch: each selected pack's `mcp` entries, `~/` joined to the
  │     notch's home, with mcp_servers merged over them
  │     (packload.ComposeMCPServers) → YOLO_MCP_SERVERS
  ├─ HOST, every launch: lsp_servers → jailcontent.writeLSPPlugin
  │     → <every skills destination>/yolo-lsp/.claude-plugin/plugin.json
  │     (Claude's LSP route; mounted at ~/.claude/skills for the claude pack,
  │     copied into the account home by the overlay on macos-user)
  ├─ HOST, `yolo host apply`: lsp_servers → cli.applyHostLSPPlugin
  │     → the same bytes (jailcontent.RenderLSPPlugin) in every skills
  │     destination of the real home
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
  ([HC-D16](../design/host-computed-layer.md#HC-D16)), and the line names the pack that ships
  a server of that name, the `chrome-devtools` pack for that preset
  ([HC-D27](../design/host-computed-layer.md#HC-D27)).
- **A pack's `mcp` entry composes, joined to your real home**, under your own `mcp_servers`,
  when the pack is one yolo ships or one at a path on this machine. A fetched pack's entry is
  left out and named: its command would run unconfined as you whenever the agent starts
  ([HC-D26](../design/host-computed-layer.md#HC-D26)).
- **An entry whose command or arguments name a jail-only path** (`/workspace`, the jail home,
  `/ctx`, the install prefix, `/run/yolo`) is left out, and a surface whose derive output still
  names one is refused rather than written
  ([HC-D14](../design/host-computed-layer.md#HC-D14)).
- **`requires_env` is asked of each agent's host composition**, the environment `yolo host env
  --agent <agent>` prints, over the invoking shell's.

`yolo host -- <agent>` also installs into yolo's floor the program each such server runs (its
`bin`), since the host has no lazy launcher to install it on first use; a failure costs that
server, never the agent, and its line names `yolo host apply --assert`
([HC-D28](../design/host-computed-layer.md#HC-D28)).

Claude at the host gets its `yolo-lsp` plugin, rendered from the same composed table into every
skills destination of the real home ([below](#at-the-host-yolo-host-apply-writes-it)), beside
`ENABLE_LSP_TOOL` in its settings; Copilot gets its native LSP file. An entry left out above is
left out of both.

### A pack's servers: the `mcp` kind

A pack contributes a server the way it contributes a provider: one `kind: "mcp"` entry per
server, composed into the table under the user's own (`packload.ComposeMCPServers`,
[OQ-MP3](../design/mcp-presets-removal.md#OQ-MP3)). `config` is the entry, in exactly the shape a
user writes one under `mcp_servers`:

```jsonc
{"kind": "mcp", "name": "chrome-devtools", "bin": "chrome-devtools-mcp",
 "config": {"command": "/bin/sh",
            "args": ["~/.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper"]}}
```

- **A `command` or `args` word starting `~/` names a path under the home** of the notch the
  entry renders for, and the composer writes it absolute: `/home/agent` in a container jail, the
  sandbox account's home on macos-user, yours at the host. An MCP client starts its servers with
  a scrubbed environment, so the path has to be absolute, and the home is a fact the composing
  side already has ([OQ-MP4](../design/mcp-presets-removal.md#OQ-MP4)). `env` values are
  literal, as everywhere.
- **Composed once per launch, on the host, for that launch's home**: the container launch puts
  the result in `YOLO_MCP_SERVERS`, the macos-user plan composes it from the staged pack tree
  for the sandbox home and bakes it into the bootstrap, and `yolo host apply` composes it for
  your home. The jail reads it as it read your table before.
- **Your entry of the same name merges over the pack's per field**, `env` per variable, and
  `null` removes it — at every notch, and in a jail the `null` also removes a preset of that
  name, as it always did. Your entry is written as you wrote it: a `~/` in it is not joined, so
  an `args` of yours, which replaces the pack's whole, names the wrapper by its absolute path.
- **A server name is sole-owned.** Two selected packs shipping one are refused by config
  validation, which a jail launch and `yolo check` run, and reported by `yolo pack footprint`; the
  host runs no config validation, so there the later pack in `packs` holds the name. One pack
  declaring a name twice is refused when the pack is read.
- **`bin`**, optional, names the `program` the server runs. Only the host reads it
  ([above](#at-the-host-notch)).

### The rules the one loader enforces

Because every projection reads the same table, these apply **identically** to every
MCP-enabled tool.

- **Presets are opt-in and expanded in-jail**, not on the host. Their `command` is the wrapper
  path, baked in — so the servers yolo ships are wrapper-routed by construction. **`macos-user`
  expands none**: it generates no wrappers (their bodies are Linux paths), so a preset entry
  would point every agent at a file that does not exist. The bootstrap writes no entry for a
  preset (`Env.SkipMCPPresets`) and its launch warning names each preset it left out.
- **`null` removes** a server or a preset. Same-file "enabled *and* null-removed" is a
  validation error; across scopes (a user config enables, a workspace nulls) it is intentional
  and allowed.
- **`requires_env` gates** a server: if any listed variable is unset or empty in the jail the
  server is dropped with a notice (and a copy an earlier launch wrote is named as gated, with the
  variable and where to set it, [below](#a-launch-with-a-provides-server-recorded-2026-10-01)), and otherwise the `requires_env` key itself is **stripped**
  before the entry reaches the tool. The gate is asked **per agent**: a variable a provider
  claims reaches only the agent that selected that provider, in that agent's own env file
  ([the credential gate](providers.md#the-credential-gate)), so the server is written into
  that agent's config and no other, and the notice names the agents it was configured for
  (`loadMCPTables` in `internal/entrypoint`). Each agent's answer applies its own file the way
  its launcher sources it, so a profile that sets a variable another profile or the shared
  environment also sets, or that removes one, is honored. On `macos-user` the jail's environment
  is the root-owned session env file the launch writes before its bootstrap runs. The bootstrap
  reads that file into the gate's view and leaves its own process environment unchanged (the
  `hydrate_session_env` boot step), so a server gated on a shared `env_sources` variable is kept
  there as it is in a container. The file is the launched agent's environment, so it also holds
  the values the gate scoped to that agent. When some agent's own env file also names a
  variable, the jail-wide view keeps it only where those files show the value the shared
  environment gives it, and takes that value; otherwise it leaves the variable out, so a scoped
  value reaches only the agents whose file sets it (`scopedMCPView`). The files cannot show the
  shared value when two or more profiles set the variable to that value itself, and the server
  is then configured only for the agents whose file sets it. No `YOLO_` name is taken from the
  file: those are the launcher's.
- **A pack's entry replaces a same-named preset.** The composed table rides in the variable the
  user's own table always did, and that variable overrides the presets by name, so while both are
  on (the key retires in [step 3](../design/mcp-presets-removal.md#13-what-i-would-build-in-order))
  the `chrome-devtools` pack's entry is the one every agent gets.
- **Key order is insertion order** — presets in the order the config listed them, then the
  composed entries, each pack's in pack order and then the user's own — so a projection's output
  is byte-stable across boots.

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
the declare-it remedy. No shipped derive renames them.

**Fixed 2026-10-06: a server the `requires_env` gate removed gets a line of its own too.** The
loader drops such a server before any derive's table is built, so the boundary records nothing for
it, and a copy a previous render left in the file used to be called "not in config", beside the
gate's own `skipped — required env not set` line. `loadMCPTables` now records what the gate
removed from each table it builds (`Env.recordMCPGated`): the jail-wide one, and each agent's own
when the credential gate wrote that agent an env file. The drop notice reads the record of the
table the surface's agent renders (`Env.mcpGatedFor`) and names each such server with the
variables it lacks, by name in whichever table lost the entry, as for a withheld server. With
`ACME_TOKEN` set on one launch and unset on the next, the second prints:

```text
claude/config: dropping from mcpServers (in config, required env not set): acme (needs ACME_TOKEN) — ACME_TOKEN is unset for claude, so the `requires_env` gate left it out; it stays declared under `mcp_servers`, and to deliver it, set ACME_TOKEN in a dotenv file listed under `env_sources` in ~/.config/yolo-jail/config.jsonc on the host, then launch again
```

The remedy names one place, the user config's `env_sources`, where the
[providers guide](../../userguide/guides/providers-and-models.md) puts every key: a workspace
config may list `env_sources` too, but it sits in a repository, and a variable a `requires_env`
gate asks for is usually a credential.

That remedy changes nothing when the variable already reaches another agent through that
agent's own env file, which is where the credential gate puts an `env_sources` value a provider
claims ([the credential gate](providers.md#the-credential-gate)): the value is in `env_sources`
already, and only a profile selection delivers it. So when another agent's own table kept the
server, the case in which the boot also prints `notice: MCP server 'acme' configured only for
codex`, the line names that agent and the profile step instead (`Env.mcpGatedReach`):

```text
claude/config: dropping from mcpServers (in config, required env not set): acme (needs ACME_TOKEN) — ACME_TOKEN reaches only codex, through the profile it selected, so the `requires_env` gate left it out for claude; it stays declared under `mcp_servers`, and to deliver it, select for claude a profile that delivers ACME_TOKEN, as that one does, with the `profile` key in ~/.config/yolo-jail/config.jsonc on the host, then launch again
```

`yolo host apply` had the same defect: its loss list read the derived layer, which never held
the gated server, so the copy in the user's file was listed `(dropped — not in your config)`
under the declare-it remedy. Since 2026-10-06 the line reads
`mcpServers.acme (dropped — in your config, required env not set: ACME_TOKEN)` (by name, as in
the jail), the server leaves the declare-it group for one keyed on `env_sources`, and a first
apply's confirmation gives it that remedy rather than the declare-it one
([the remedy contract](report-tiers.md#the-remedy-contract)).

Both boot lines are
pinned through the boot loop, by `capabilitydropnotice_test.go` and
`requiresenvdropnotice_test.go` (`internal/entrypoint`), the host's by
`hostapplygatedloss_test.go` (`internal/cli`), and the withheld rule's partition by
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
| `~/.pi/agent/mcp-adapter.json` | pi-mcp-adapter; pi-subagents 0.73.0 and later while the adapter runs its tools | never writes it; deletes it only while it holds exactly yolo's render, which is the copy yolo 0.11 wrote there |

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
  ([the host write is per key](host-agent-environment.md#the-host-write-is-per-key)). Under the
  rmw arm (every surface under the retired `host_management: assert`), a server you drop from
  `mcp_servers` lost the fields yolo wrote, and its emptied entry, `{}`, stayed, which pi reports
  and skips as above (MEASURED 2026-10-01 with a scratch test through `RenderHostPack`); under
  `own`, `pi/mcp` composes through `stateful`, and a home `assert` wrote into may still hold such
  an entry. ⚠ `yolo host apply --revert` removes the whole
  `mcpServers` key, your servers with yolo's, because its record is kept per top-level key
  (MEASURED the same way, through `RevertHostRender`); its dry run lists `pi/mcp` `mcpServers`
  before anything is removed. Host apply deletes nothing, so a `mcp-adapter.json` at the host is
  left as it is.

**pi-mcp-adapter duplicates pi's own client.** The two load side by side: pi leaves its own
client out only for an extension that registers `/mcp` while extensions load
(`omitReplacedExtensions`, pi 0.99.2 `dist/core/resource-loader.js`), and the adapter registers
`/mcp-adapter` then, taking `/mcp` only at session start and only when it does not find pi's
(pi-mcp-adapter 3.3.0 and 4.0.0, `index.ts`). Neither reads the other's file, so with the
adapter still in pi's `packages` a server yolo writes starts twice whenever the adapter finds it
too: in `~/.config/mcp/mcp.json` while pi-subagents is selected, or in an `mcp-adapter.json` the
boot kept. yolo does not install the adapter; take `npm:pi-mcp-adapter` out of whichever pack or
settings file lists it. Turning pi's own client off in `pi config` to keep the adapter instead
leaves pi with yolo's servers only while pi-subagents is selected: of the files yolo writes for
pi, the adapter reads only `~/.config/mcp/mcp.json` by default (INFERRED from the two readers
above, not run).

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
now moved back. A `mcp-adapter.json` holding exactly yolo's render is yolo's own leftover and is
deleted (`retireIfMatchesRender`). yolo's render is either what `mcp.json` holds after the boot's
write or the same servers without anything of yours in it, which is what 0.11 wrote there, so a
server you added to `mcp.json` or a server you turned off with `/mcp` does not keep the copy. The
comparison is on the decoded JSON, so key order and indentation do not decide it. A file with one
more key, one different value or a server yolo no longer configures is kept, and pi-mcp-adapter
keeps loading a kept copy until you remove it. The `mcp.json` yolo 0.10.0 wrote is the surface's own file again:
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
nothing beyond the mount that already carries skills; on macos-user the home overlay copies
the same staging into the account's home. `yolo host apply` writes the same file into the real
home ([below](#at-the-host-yolo-host-apply-writes-it)): both notches take their bytes from
`RenderLSPPlugin`.

- **Translation.** Each server's `command` passes through and `args` passes through;
  `fileExtensions` is renamed `extensionToLanguage`. Either list is omitted when empty rather
  than written empty, so a hand-read manifest shows only what was declared. **An entry with no
  `command` is skipped**, never rendered: a server yolo cannot spawn would only make Claude
  report a failure yolo could have declined to cause. (Copilot's derive keeps such an entry,
  minus the command.) The manifest also carries a `name` and a `description`.
- **Ownership marker.** The manifest carries yolo's `x-yolo-managed-by` field, which is how the
  host-side adoption walk (`hostskills.IsYoloPluginDir`) knows the directory is yolo's output
  and never offers to migrate it into the user's local pack. The value is spelled in both
  `jailcontent` and `hostskills`, the second keeping its constant unexported, and a drift test
  pins the two together. The marker alone does not identify the plugin: a namespaced pack's
  skills subtree carries it too, under the pack's name. `IsLSPPlugin` also asks for the name
  `yolo-lsp` and an `lspServers` object, which only this renderer writes.
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

### At the host, `yolo host apply` writes it

`yolo host apply` (and `yolo apply --at host`, the same verb) writes the plugin after it composes
the skills destinations, from the `lsp_servers` table it composed for the derives, so an entry
naming a jail-only path is left out of the plugin as it is of Copilot's file
([at the host notch](#at-the-host-notch)). It follows the launch's rules where a real home allows
them and says where it cannot:

- **Every skills destination the selected packs compose**, created if it does not exist yet,
  for the launch's reason: core knows no agents.
- **The same bytes.** An in-sync plugin is reported `unchanged` and not rewritten, so a dry run
  after an `--assert` finds nothing to do.
- **Nothing else at that name is overwritten.** A `yolo-lsp` the user made, or a skill of that
  name a pack composes there, is refused by name with the rename that lets the servers through,
  and an `--assert` that met one exits 1 (a dry run reports it and exits 0,
  [LSP-I1](#lsp-i1)). A launch stages into an empty directory and writes the plugin last, so it
  never meets either.
- **The dry run says what the `--assert` does.** Whether a pack composes a `yolo-lsp` is read
  from the packs this apply composes, not from the skills record, and what the skills render
  retires counts as gone ([LSP-I3](#lsp-i3)). A dry run records and archives nothing, so read
  from the record it would promise a plugin the `--assert` then refuses, or repeat a refusal the
  user has already acted on.
- **A reserved name is skipped.** A pack contributing to the destination may fence `yolo-lsp`
  as another tool's tree, and a fence is never composed over. The skip is a `--verbose` line
  ([LSP-I6](#lsp-i6)).
- **Archived, not deleted, and unconfirmed** when the table renders nothing, when no selected
  pack composes the destination any more, and when `packs` is empty: every byte moved is one
  yolo wrote. It goes under the skills archive, in a `lsp_servers` directory of the apply's
  generation ([LSP-I4](#lsp-i4)). A destination counts as no longer composed only when every
  configured pack resolved ([LSP-I5](#lsp-i5)), and only when it is not the same directory as
  one still composed: with `~/.codex/skills` a link to `~/.claude/skills`, a home selecting
  claude alone keeps the plugin, since an unselected pack's destination is judged by the
  directory it is rather than by its path.
- **A failure names its next step.** A plugin that cannot be inspected, created, written or
  archived is refused with the directory to check and the `yolo host apply --assert` to rerun.
- **Owned by its manifest, never a record.** The skills record maps a path to the pack that
  composed it, and the dropped-pack retire reads every owner there as a pack. So the plugin is
  recorded nowhere, and that retire's scan of marked directories skips it by its manifest
  (`IsLSPPlugin`, [LSP-I2](#lsp-i2)); a pack literally named `yolo-lsp` is still found there,
  its subtree having no `lspServers`.

Two host verbs do not touch it: `yolo host apply --revert` withdraws no skills of any kind, and
`yolo config render --at host` renders config surfaces, which the plugin is not.

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
declares it any more, so on every jail backend the boot catalog names it as an orphan, and
`yolo programs remove --apply` (or `programs.autoprune`) collects it. That act reads the
disk rather than a record, which is why no one-shot uninstall was written: the sentinel was a
record, and `internal/entrypoint/orphanremove.go`'s header describes it losing exactly these
entries. A workspace whose `lsp_servers` names one of those servers keeps working on the
leftover until it is collected; after that, the server has to come from `PATH` like every
other.

> [!NOTE]
> **`macos-user` collects it too, since 2026-10-04.** The macos-user bootstrap runs the boot
> catalog and keeps the same `<workspace>/.yolo/boot.log` the container boot keeps, so the
> catalog's names land there. The launch relays the user's `programs.autoprune`. The sandbox
> session names the staged pack tree and the workspace, so `yolo programs ls` and
> `yolo programs remove` run inside the sandbox
> ([notch-convergence.md](../plans/notch-convergence.md), the row amending NC-D26). A macos-user
> workspace that had `lsp_servers.python`, `typescript` or `go` set while the recipe existed (it
> installed there from 2026-09-13) still holds those packages until that act runs. The npm
> packages are under `<workspace>/.yolo/home/npm-global/lib/node_modules`, and `gopls` is at
> `<workspace>/.yolo/home/go/bin/gopls`. The sandbox's `~/.npm-global` and `~/go` are links to
> those directories. The bootstrap runs outside the sandbox, so the boot catalog reads, and
> autoprune removes, only beneath those directories themselves; a symbolic link below one is
> named on the terminal, and that boot removes nothing
> ([program-delivery.md OQ-PD28](../design/program-delivery.md#decision-ledger)). Unmeasured on a
> Mac until `TestMacosUserProgramsLsSeesTheStagedPacks` and `TestMacosUserAutopruneRemovesAnOrphan`
> run.

## The chrome-devtools pack

`"packs": ["chrome-devtools"]` gives every selected agent a Chrome DevTools MCP server, in a
container jail, on macos-user and at `yolo host`. The pack carries four contributions, each in
its footprint:

| Contribution | What it does |
| :--- | :--- |
| `program` `chrome-devtools-mcp` (npm, Node floor 22.12) | the server. In a jail and on macos-user every agent's launcher installs it before the agent starts and keeps it current on the agent's own trigger; the program's launcher, which the server runs through, installs it only if nothing has and never updates it by itself, since it runs while the client waits ([MP-D9](../design/mcp-presets-removal.md#MP-D9)). At the host `yolo host apply --assert` or the first `yolo host -- <agent>` installs it into yolo's floor |
| `files` `.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper` | the script the entry runs, off PATH ([MP-D1](../design/mcp-presets-removal.md#MP-D1)) |
| `mcp` `chrome-devtools` | `/bin/sh` and the wrapper's path under the notch's home. `/bin/sh`, because a pack selected by its bare name carries no exec bit |
| `autonomy` | the jail-only chrome flags on the AUTONOMOUS posture's launch flags for the program: `--headless`, `--isolated` and three `--chrome-arg=` flags (`--no-sandbox`, `--disable-setuid-sandbox`, `--disable-gpu`). A jail's launcher adds them to every start; the GUARDED posture, the host's, adds none, so Chrome keeps its own sandbox and opens a window there ([MP-D2](../design/mcp-presets-removal.md#MP-D2)) |

**The wrapper looks for both halves at run time**, because the client hands it a scrubbed
environment and the notch is not something it may guess from one:

- **the server**: the PATH it was given, then `~/.yolo/bin/launch` (a jail's and macos-user's
  launcher), then the npm prefix, then yolo's host floor at
  `~/.local/share/yolo-jail/host-floor/bin`;
- **the browser** ([MP-D5](../design/mcp-presets-removal.md#MP-D5)): `chromium`,
  `chromium-browser`, `google-chrome` or `google-chrome-stable` on PATH, then the standard
  install paths — the image's `/usr/bin/chromium`, `/opt/google/chrome/chrome`, the
  store-delivered farm, `/Applications/Google Chrome.app` and `Chromium.app`, and the same two
  under `~/Applications`. The first found is passed as `--executablePath`; with none, nothing is
  passed and chrome-devtools-mcp looks for Chrome itself. A browser the caller's own arguments
  name (`--browserUrl`, `--wsEndpoint`, `--executablePath`, `--channel`, or `--autoConnect` to
  attach to the Chrome you already run) is never overridden.

It says which on stderr each time it starts the server, and **`sh
~/.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper --check`** prints both finds
and starts nothing — the browser-presence report, since a `requires` contribution can name one
binary on PATH and not an app bundle or a set of alternatives. That departs from half of the
ruling it implements, and is open for the maintainer at
[`OQ-MP9`](../design/mcp-presets-removal.md#OQ-MP9). With no server it exits 127 and
names the step for each notch.

**What each notch needs**: a container jail has the image's chromium, or the store-delivered one
on a `YOLO_STORE_PACKAGES=1` launch. macos-user and the host need a Chrome or Chromium the machine
already has; no pack channel can install a browser. To turn the server off without dropping the
pack, write `"mcp_servers": {"chrome-devtools": null}`.

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

**Removing the `sequential-thinking` preset**, and retiring `mcp_presets` whole, is ruled and
not built: [§13](../design/mcp-presets-removal.md#13-what-i-would-build-in-order) step 3, parked
on [`OQ-PK1`](pack-system.md#oq-pk1). Until it ships both mechanisms are live, and the pack's
entry replaces a same-named preset.

**Two readers do not see a pack's entry yet.** `yolo check`'s dry-run render composes its
`YOLO_MCP_SERVERS` from your config alone (`internal/cli/check/entrypoint.go`), so its preview of
an agent's MCP file lacks a pack's server; and the capability census behind
`required_capabilities` (`config.CapabilitySatisfiers`) counts a `provides` in your
`mcp_servers` and not in a pack's entry. No shipped pack's entry declares `provides`.

**Open for pi's MCP files.** Two calls are written up below with their options: who owns the
host's `mcp.json` server table ([OQ-MC1](#OQ-MC1)), and what yolo does about an old pi or a
leftover `mcp-adapter.json` at the host ([OQ-MC2](#OQ-MC2)). [OQ-MC1](#OQ-MC1) no longer holds a fix: both
faults it names arise under `host_management: assert`, the emptied entry directly and `--revert`
because it runs under no other value (`hostRevertRefusal`). `assert` is ruled retired
([§4.5 there](../design/config-ownership-and-promotion.md#45-retiring-assert--the-two-value-key)),
and since [`OQ-CO14`](../design/config-ownership-and-promotion.md#oq-co14) was ruled on 2026-10-05
nothing holds the retirement's build, which ends both. Also not ruled: whether
`~/.config/mcp/mcp.json` is still written now that pi-subagents 0.74.0 runs `mcp:` tools from pi's
own client, and whether yolo does anything for a pi-mcp-adapter user beyond saying the two clients
duplicate each other.

- 💬 <a id="OQ-MC1"></a>**[`OQ-MC1`](#OQ-MC1) — does the host own `mcp.json`'s server table per
  server, or whole?** Filed 2026-10-02. Today each server is yolo's or yours, which leaves the
  two faults [pi's own file](#pis-mcp-files) records at the host: an emptied `{}` entry under
  the rmw arm (as the retired `assert` ran it), and a `--revert` that removes your servers with
  yolo's.

  - **(a) Per server, and fix both.** `--revert` removes only the servers yolo wrote, and an
    entry yolo empties is deleted. *You keep:* the servers you add with `pi mcp add`.
  - **(b) Whole, as `~/.claude.json`'s table is.** Both faults end with no new code path.
    *You pay:* the next apply removes every server you added with `pi mcp add`.

  <!-- vantage: question id=OQ-MC1 leaning="(a): per server, with --revert taking only yolo's servers and an emptied entry deleted. pi writes this file itself (pi mcp add, /mcp), and owning the table whole would delete what the user added there." -->

  _Leaning:_ **(a).** pi writes this file itself, so owning the table whole deletes what the user
  added with pi's own command.

  **Answer:**
  > _(empty — fill in when decided)_

- 💬 <a id="OQ-MC2"></a>**[`OQ-MC2`](#OQ-MC2) — what does yolo do about an old pi, or a leftover
  `mcp-adapter.json`, at the host?** Filed 2026-10-02 with [OQ-MC1](#OQ-MC1). A pi older than 0.99.0 has no
  MCP client and starts none of the servers in `mcp.json`, and host apply deletes nothing, so a
  `mcp-adapter.json` at the host stays, and pi-mcp-adapter keeps loading it.

  - **(a) A version floor and a deletion.** The pi pack requires pi 0.99.0, and host apply
    retires a host `mcp-adapter.json` by the exact-match rule a jail boot uses.
  - **(b) No floor and no deletion; `yolo check` at the host names each.** It names an old pi with
    the `npm` command that updates it, and a leftover `mcp-adapter.json` with the exact `rm`.
  - **(c) Nothing.**

  <!-- vantage: question id=OQ-MC2 leaning="(b): no version floor and no deletion; yolo check at the host names an old pi and a leftover mcp-adapter.json, each with the one command that fixes it, which is the happy path principle's one-command step." -->

  _Leaning:_ **(b).** It is the [happy path principle](happy-path-principle.md)'s one-command step,
  and it deletes nothing in a real home.

  **Answer:**
  > _(empty — fill in when decided)_

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
| Pack-shipped servers | the `mcp` kind; `chrome-devtools` is the shipped one | `packload.ComposeMCPServers`, `packs/chrome-devtools/pack.json` |
| The home word in an `mcp` entry | `~/` at the start of a `command` or `args` word | `packdecl.MCPHomePrefix` |
| Where the composition is delivered | `YOLO_MCP_SERVERS` on the container argv; the macos-user bootstrap env; the host's derive inputs | `run.jailMCPServers`, `macosuser.BuildRunPlanWithDaemons` (`entrypoint.MCPServersAt`), `cli.composeHostInputs` |
| Where the chrome-devtools wrapper lands | `~/.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper` | `packs/chrome-devtools/pack.json` |
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
| LSP plugin manifest path | `<skills destination>/yolo-lsp/.claude-plugin/plugin.json` | `jailcontent.LSPPluginManifestRel`; written by `jailcontent.writeLSPPlugin` and `cli.applyHostLSPPlugin` |
| Ownership marker | `x-yolo-managed-by: "yolo-jail"` | `jailcontent.yoloPluginManagedBy`, `hostskills.yoloManagedMarker` |
| Claude's LSP switch | `env.ENABLE_LSP_TOOL = "1"` in `settings.json`, only when a server is configured | `packs/claude/derive.lua` |
| Claude Code version the plugin-loader facts were read from | 2.1.278, statically from its bundle | [`OQ-LSP3`](#oq-lsp3) |
| Where the plugin's injection is pinned | `TestLaunchStagesTheLSPPluginFromTheConfig`, `TestLaunchWithoutLSPServersStagesNoPlugin` | `internal/cli/run/lspplugininjection_test.go` |
| Language servers yolo installs | none, on any backend | `internal/entrypoint` (`BootstrapScript` has no LSP arm) |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo, with their original ids. An `LSP-I`
row is an implementation decision rather than a ruling, numbered in a series coined in this table
for the host half of Claude's plugin.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| `D6` | The bootstrap's npm install for a preset is gated on the **same declaration** that builds the server table | Hardcoding a package list beside the preset table lets the two drift, and the failure is a preset that is configured and whose package was never installed. |
| Capability-driven delivery ([above](#capability-driven-mcp-delivery)) | The filter sits at the derive **context boundary**, never in an agent's `derive.lua`, and keys on the selected **source**, never on the agent | The per-agent `provides == "web_search"` branches it replaced were opt-in, and one had drifted: claude's suppressed web search for every profile that was not bedrock or codex, so a Kilo launch, a source with no native search, lost its search MCP because of the agent it ran under. |
| Principle 2 of [`pack-system.md`](pack-system.md) | Core publishes the **domain** (`mcp_servers`); the pack owns the **tool's dialect** | A per-tool branch in core is the agent registry coming back through a different door. The projection changes when the tool changes, which is the pack's business. |
| <a id="oq-lsp1"></a>[`OQ-LSP1`](#oq-lsp1) | **Option D — generate.** yolo authors ONE plugin whose `lspServers` is rendered from the user's own `lsp_servers` table, and delivers it as content. Never a per-language map, never marketplace plugin ids — neither the three it replaced nor the full official set | A per-language map is yolo picking "the" server for a language, which is the user's choice; marketplace ids also need an install path, and a jail runs no vendor install verb. The ruling said "and enables it", but no enable step exists: the skills-tree load path is opt-out. The same rule deleted the install side: the three-entry recipe table that picked `pyright`, `typescript-language-server` and `gopls`, and its install lists, sentinel and refresh arm, went on 2026-09-25, so yolo installs no language server. |
| <a id="oq-lsp3"></a>[`OQ-LSP3`](#oq-lsp3) | Claude **auto-loads a plugin from `~/.claude/skills/*`** with no marketplace, no `enabledPlugins` entry and no flag; it is **enabled by default** there; ONE plugin may declare MANY servers, keyed by server name. Measured statically against Claude Code 2.1.278 | This is the precondition option D rests on, and it is version-pinned. **Falsifier:** a Claude release that removes the skills-tree or session load arm, or a managed policy that stops the skills-tree scan — a `strictKnownMarketplaces` allowlist without `{"source": "skills-dir"}`, a `blockedMarketplaces` entry naming it, or (2.1.288 strings) any `strictPluginOnlyCustomization` lock — shipped on by default, or set by a policy on the user's machine. `disableSideloadFlags` is not one: it gates `--plugin-dir`, `--plugin-url`, `CLAUDE_CODE_PLUGIN_DIRS`, `--agents`, `--mcp-config` and the mods folder, not the skills tree (corrected 2026-10-03, [claude-code-mods-management.md G6](../research/claude-code-mods-management.md#g6-an-organization-policy-silently-blocks-the-whole-route)). Anyone revisiting it re-reads the installed version's plugin assembler. Two servers claiming one extension is a warning, not a load failure. |
| <a id="lsp-i1"></a>[`LSP-I1`](#lsp-i1) | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* At `yolo host apply`, a `yolo-lsp` the plugin cannot be written over makes an `--assert` **exit 1**, and a dry run **exit 0** | The `--assert` did not deliver what the config asks, so a script must see it fail. A dry run's output is the finding ([OQ-RO5](report-tiers.md#why-its-this-way)), and a non-zero observe pass would leave the launch gate unable to read the home at all. |
| <a id="lsp-i2"></a>[`LSP-I2`](#lsp-i2) | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* The plugin is **identified by its manifest alone**: yolo's ownership marker, the name `yolo-lsp`, and an `lspServers` object (`IsLSPPlugin`) | The marker and the name are not enough: the namespaced subtree of a pack called `yolo-lsp` carries both. Recording the plugin in the skills record instead would have the dropped-pack retire read it as a pack's output and offer to retire it on every apply. |
| <a id="lsp-i3"></a>[`LSP-I3`](#lsp-i3) | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **A `yolo-lsp` is a pack's when the packs this apply composes claim that name** at the destination, read from their layers (`hostskills.Collisions`, the authority the skills refusal uses), and **what the skills render archives or clears there is free** | Built first as "any path either skills record attributes to a pack". A dry run records and archives nothing, so that rule had the dry run disagree with the `--assert` both ways: on a new home it promised a plugin the `--assert` refused, and after the user renamed the pack's skill it repeated the refusal while the `--assert` wrote the plugin. Revised the same day. |
| <a id="lsp-i4"></a>[`LSP-I4`](#lsp-i4) | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* A retired plugin is **archived, unconfirmed, under `lsp_servers/`** in the skills archive of the apply's generation | No pack owns it, so it is filed under the config key whose output it was. Every byte moved is one yolo wrote, the asymmetry the skills retire already carries, and an archive costs one `mv` back. |
| <a id="lsp-i5"></a>[`LSP-I5`](#lsp-i5) | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* A destination is retired as **dead only over a complete pack set** | A configured pack that did not resolve may be the one naming the destination, the reason the skills composition's own retire waits for a complete set. |
| <a id="lsp-i6"></a>[`LSP-I6`](#lsp-i6) | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* The **reserved-name skip is a `--verbose` line**, not a warning | A fence is a pack's deliberate declaration, nothing is wrong, and the skills report already puts a skipped entry's line on demand. |
| Orphan-file cleanup ([`config-migration-to-prism.md`](config-migration-to-prism.md#the-two-sidecars-are-different-kinds-of-thing)) | A retired sidecar is deleted by the surface's owner on first composed render, as **data** rather than Go | It was the last thing left in the per-agent render functions besides the computed layer, and keeping it in Go would keep those functions alive for a one-shot cleanup. |
