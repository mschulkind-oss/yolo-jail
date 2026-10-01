---
title: "What a jail derives, the host leaves empty — for a reason that holds for one input in four"
date: 2026-09-27
status: accepted
stage: BUILT
next: "Graduate the built body into the reference that owns host-apply rendering, by the one-at-a-time rules in docs/plans/README.md; choose the fold target first"
tags: [host, notch, derive, computed, providers, mcp, lsp, profiles, pi, host-apply]
summary: "yolo host apply used to render no derive's content, so every derived pack surface reached the real home with its declared layers only: no provider catalog, no MCP or LSP entries, no openai-codex list for pi. The stated reason was jail-absolute paths, and only the MCP presets carry any. The maintainer ruled host parity with the same handling: the host now runs the jail's derives over inputs composed at user scope, lands their output per key, and refuses a surface whose output names a jail path. This doc records the argument, the ruling, the build, and what a look at host pi with pi-automode found."
vantage:
  status-chip: true
---

# What a jail derives, the host leaves empty — for a reason that holds for one input in four

**Status:** 2026-09-28 at `358f877d`, as ruled in [OQ-HC1](#OQ-HC1)–[OQ-HC3](#OQ-HC3): `yolo host apply` and a wrapped launch's automatic apply run every derive over user-scope inputs. The build follows [§6](#6-the-proposed-shape-under-b1) with the ruling's two changes: no registration option, since every derived surface takes part, and a jail-path check on the output ([HC-D14](#HC-D14)). [§14](#14-what-was-built) says what each surface now gets. MEASURED after the build: `yolo config render --at host` for `codex` and `opencode` on 2026-09-28 ([§14](#14-what-was-built)), and each of its tests failing with its call site removed. UNMEASURED: no agent has been started against a file a host apply wrote. Revised 2026-09-29 by [HC-D25](#HC-D25): a computed leaf yolo wrote and stops asserting is cleared at the next apply. [§7](#7-fixes-that-do-not-wait-for-a-ruling)'s fixes and [§8.1](#81-measured) item 3's message shipped before the ruling, and their rows say where each changed what was measured. Sections 2 to 6 describe the tree before the build: evidence MEASURED at `97220184` in a clean build under a temporary home, with no host, no pi CLI and no jail started. Code claims cite a symbol, never a line.

> **In short.** The host leaves the computed layer empty because a jail's derive inputs carry
> jail paths, yet only the MCP presets do: the provider table is already composed at the host for
> `yolo host --`, and the LSP table is host-valid as written. So deriving at the host means
> composing host inputs, not rewriting derives, and leaving it out stops being stable once the
> `assert` retirement is built, since then no `computed` surface has any host path.

**Why it matters.** At `yolo host apply` every derived surface renders its declared layers only.
Host pi keeps pi-ai's own `openai-codex` list, no MCP server, LSP server or provider row reaches
the real home, and yolo's own remedy for a dropped MCP entry deletes the entry
([§4](#4-what-the-empty-layer-costs)).

**The shape, as ruled.** Host apply composes the four derive inputs at user scope and runs every
derive over them, with no per-surface opt-in; the output lands per key, and a surface whose
output names a jail path is refused ([§14](#14-what-was-built)). The proposal it replaced let a
registration declare itself host-valid ([§6](#6-the-proposed-shape-under-b1)).

**Cost.** A registration option, a provider composition inside host apply, a per-key write the
host's `rmw` arm lacks today, and provider catalogs that become yolo-owned tables at the host,
where a hand-added provider survives today.

**Start at [§3](#3-why-the-computed-layer-is-jail-only--the-stated-reasons-and-which-hold)**, the
reasons. Everything else falls out of which of them hold.

**Rulings (2026-09-28, in review):** [OQ-HC1](#OQ-HC1), host parity with the same handling: the host runs the jail's derives over host-composed inputs; [OQ-HC2](#OQ-HC2) and [OQ-HC3](#OQ-HC3) follow from it, as leaned. All three are built.

**Reads with:** [`model-lists-and-pickers.md`](model-lists-and-pickers.md#OQ-ML3) (the ruling that
asked for this doc), [`host-render-target.md`](host-render-target.md#33-what-each-target-supplies)
(the premise re-argued here), [`config-ownership-and-promotion.md`](config-ownership-and-promotion.md#45-retiring-assert--the-two-value-key)
(the `assert` retirement), [`notch-scoped-config-contributions.md`](notch-scoped-config-contributions.md)
(pi-automode at the host), and [`host-computed-layer-plan.md`](host-computed-layer-plan.md) (the
implementation sketch the build followed, now superseded by [§14](#14-what-was-built)).

---

## Terms, in plain words

- **Notch.** A setting of yolo's confinement dial: jail, guest or host
  ([`yolo-as-environment-manager.md` §4](yolo-as-environment-manager.md#4-confinement-a-dial-with-three-notches)).
  The host notch means no confinement, and your real home.
- **Host apply.** `yolo host apply`, which renders the selected packs' config surfaces into your
  real home. It is a dry run unless given `--assert`.
- **Surface.** One agent config file a pack declares, named `agent/name`: `pi/models` is
  `~/.pi/agent/models.json`. Its **mode** is `stateful`, `computed`, `rmw` or `unrendered`
  ([the four modes](config-ownership-and-promotion.md#22-the-four-surface-modes)).
- **Derive.** A pack's `yolo.derive(agent, surface, fn)` function in `derive.lua`. Its output is
  the surface's **computed layer**. It reads four live **input tables**, `providers`,
  `mcp_servers`, `lsp_servers` and `use_profiles`, plus the active profile's selection
  (`luahook.DeriveCtx`).
- **Declared layers.** What a manifest writes down: `defaults`, `managed`, and other packs'
  `config-overlay` and `config-list` contributions. The opposite of what a derive computes.
- **`host_management`.** Your user-scope statement of who owns your real agent files: `none`,
  `assert` or `own` ([§4.1](config-ownership-and-promotion.md#41-the-key)). `assert` is ruled
  retired and not yet built.
- **Host-valid** *(coined here)*. A value that means the same thing in your real home as in a
  jail: no jail-absolute path, no `${workspace}`, no address only a jail daemon serves. It does not
  mean "safe", and it does not mean "the same value the jail gets".
- **Host-derivable** *(coined here)*. A surface whose pack declares that its derive, given
  host-valid inputs, produces host-valid output. It is a different fact from "has a derive", which
  `packload.DerivedSurfaces` already reads from the registrations.

## 1. The verdict

My recommendation is **B1** ([§5](#5-the-options)): a derive declares itself host-derivable on
its own registration, and host apply runs exactly those derives over inputs it composes from your
user config and your selected packs' provider facts. The derives do not change, because none of
them was ever the problem: none embeds a jail path of its own, and every jail-absolute value a
host render would have to fear arrives through one input table, `mcp_servers`, from the MCP
presets. What does change is how their output lands in a file you own: per key, by the derive's
own in-full declaration, and never through a tombstone
([§6.4](#64-per-contract-behavior)).

Several fixes do not wait for that ruling, and one of them is a startup error in host pi today
([§7](#7-fixes-that-do-not-wait-for-a-ruling)).

## 2. What the host renders today

### 2.1 The host runs each derive, but only to learn key names

Host apply does call every derive, once per surface, from `hostTableKeys` in
[`hostrender.go`](../../internal/entrypoint/hostrender.go). It feeds the derive **sentinel** input
tables and keeps only which keys the output declares in full (`ctx.in_full`), so it knows which
keys to write wholesale. The content comes from the declared layers alone (`hostTableLayer`), and
`hostTableKeys`' own comment says so: "The CONTENT does not, and must not" cross.

### 2.2 The inventory

MEASURED at `97220184`: the jail column is `deriveComputedLayer` over providers composed from
every embedded pack, one MCP preset and one LSP server; the host columns are `RenderHostPack`
into a fresh home.

| Surface | Mode | What a jail's derive writes | Host, `assert` | Host, `own` |
| :--- | :--- | :--- | :--- | :--- |
| `agy/mcp` | computed | `mcpServers` | `{"mcpServers": {}}` | refused |
| `claude/config` | rmw | `mcpServers` | skipped when no overlay targets it: only `${workspace}`-keyed keys remain | same |
| `claude/settings` | stateful | `env` (with `ENABLE_LSP_TOOL` when an LSP server is configured, otherwise empty) and a tombstone on `mcpServers`, always; with `-p codex`, the model picker and allowlist. None of these is declared in full | declared keys only | same |
| `codex/config` | stateful | `mcp_servers`, `model_providers`; the profile's `model` | empty `[mcp_servers]`, no `model_providers` | same, under a header that said "composed at jail start" and names `yolo host apply` since [HC-D5](#HC-D5): `render.Target.GeneratedHeader`, pinned by `TestAnOwnedHostRenderNamesTheHostApplyInItsBanner` |
| `copilot/lsp` | computed | `lspServers` | `{"lspServers": {}}` | refused |
| `copilot/mcp` | computed | `mcpServers` | `{"mcpServers": {}}` | refused |
| `oh-omp/models` | computed, yaml | `providers` | refused: no `rmw` encoder for yaml | refused |
| `opencode/config` | stateful | `mcp`, `provider`; the profile's `model` and `small_model` | `"mcp": {}`, no `provider` | same |
| `pi/settings` | stateful | with `-p codex`: `defaultProvider`, `defaultModel`, the pi-subagents policy | declared keys only | same |
| `pi/models` | computed | `providers` rows | `{}`, which pi rejects ([§8.1](#81-measured)); `{"providers": {}}` since [HC-D1](#HC-D1), pinned by `TestAHostApplyIntoAFreshHomeWritesAModelsFileWithProviders` | refused |
| `pi/codex-models` | computed | the declared `openai-codex` list | `{}` ([ML-D8](model-lists-and-pickers.md#ML-D8)) | refused |
| `pi/mcp` | computed | `mcpServers` | `{"mcpServers": {}}` | refused |
| `mise/config` (core) | stateful | `[tools]` from `mise_tools`, a computed layer core supplies rather than a derive | absent: host apply walks pack surfaces only | same |

The `files` kind is unaffected: host apply delivers pi's `yolo-openai-auth.js` and each agent's
footer script as it does in a jail.

### 2.3 What happens to an entry you added by hand

MEASURED at `97220184` with a scratch test that seeded each real file with one hand-added entry,
then ran `RenderHostPack` under each contract:

| Hand-added entry in the real file | `assert` | `own`, first apply |
| :--- | :--- | :--- |
| A provider in `codex/config`'s `model_providers`, `pi/models`'s `providers` or `opencode/config`'s `provider` | **kept** | kept for codex and opencode; `pi/models` refused |
| An MCP server in `codex/config`, `opencode/config` or `pi/mcp` | **dropped**, and reported; but with three or more entries in the table the write kept every other one while the report named all of them, until the fix to `entrypoint.regenerateManagedTables`, pinned by `TestAHostTableWriteKeepsOnlyTheConfiguredEntry` ([HC-D5](#HC-D5)) | kept for codex and opencode, **but reported as dropped** until `entrypoint.statefulTableLosses`, which reports only what the owned write drops, pinned by `TestAnOwnedHostApplyDoesNotReportAnEntryItKeeps` ([HC-D5](#HC-D5)); `pi/mcp` refused |

The catalogs survive at the host for a reason that is easy to miss: the key-name probe's sentinel
provider has no address, so no catalog derive writes a row for it and none declares its catalog a
table. So at the host today a catalog is the user's, and an MCP table is yolo's.

## 3. Why the computed layer is jail-only — the stated reasons, and which hold

No maintainer ruling says the host computed layer is empty. Four statements say it, each in code
or in a design doc, and each gives a reason:

| Stated reason | Where it is stated | Ruling or statement | Does it hold? |
| :--- | :--- | :--- | :--- |
| "Its values embed jail-absolute paths" | `render.KindHost`'s comment ([`target.go`](../../internal/render/target.go)); the header of [`hostrender.go`](../../internal/entrypoint/hostrender.go); [host-render-target §3.3](host-render-target.md#33-what-each-target-supplies), "Jail-derived => host target gets none" | Statements, 2026-07-27 onward | **For one input.** The MCP presets' commands name the jail's generated node wrapper and the jail's npm prefix (`Env.mcpServersWith`, through `Env.McpWrappersBin` and `Env.NpmBin`). Knowing those paths at the host is not the obstacle: [OQ-MP4](mcp-presets-removal.md#OQ-MP4) dissolved that premise, since the host states the jail's PATH directories (`paths.JailPathHomeDirs`) and the host notch gets only their `.local/bin` overlap. The obstacle is that the wrapper is written only by a jail's boot and the npm prefix is the jail's, so neither exists in a real home. A pack's ruled `mcp` declaration is not a host input at all ([OQ-MP3](mcp-presets-removal.md#15-open-questions): it "reaches only the jail"). A user's own `mcp_servers` entry is whatever the user wrote, host-valid unless it spells a jail path. LSP commands are bare names such as `gopls`. Provider rows are URLs such as `https://api.cerebras.ai/v1`. No shipped `derive.lua` spells a jail path of its own (MEASURED: a search of every shipped derive for `/home/agent`, `/workspace`, `/opt/yolo`, `/run/yolo`, `/ctx/` and `HOME` finds none) |
| `${workspace}` has no host referent | [env-manager plan OQ-2](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase), RESOLVED 2026-08-01; [host-render-target §6.6](host-render-target.md#66-a-host-target-is-user-scoped-not-workspace-scoped) | **Ruling** | **Yes, and it is already handled** without emptying the layer: `PruneWorkspaceKeyed` drops the `${workspace}`-keyed branches by name and renders the rest |
| "A host apply selects no variant" | the "NO profile table" comment in `RenderHostPack` | A code decision; no ruling found | **For `-p`, which host apply has none of.** Not for `use_profiles`, your user-scope choice, which `yolo host --` already honors (`effectiveHostProfiles`). Whether host apply should is [OQ-HC3](#OQ-HC3) |
| The provider table is composed only at launch | nowhere in the tree; it is the reading `config-ref`'s host-notch text invites | — | **No.** `composedHostProviders` in [`host.go`](../../internal/cli/host.go) composes the same table host-side, from user-scope config and the selected packs' facts, for `yolo host --` and `yolo host env`. [OQ-CS10](../reference/providers.md#oq-cs10) makes it a constraint: "the composition is host-launch-time, so the derive is too" |

> [!WARNING]
> **The intuitive reading is that the derives are jail-specific, and it is wrong.** Every derive
> is a pure function of its input tables and the selection, in a sandbox with no `io`. What made
> the jail's computed layer unfit for the real home was the jail's *inputs*. Composing the inputs
> at the host is the whole design.

## 4. What the empty layer costs

1. **Every row of [§2.2](#22-the-inventory)'s last two columns.** Host pi keeps pi-ai's own
   `openai-codex` catalog, with GPT-5.x ids and no 1M variants
   ([ML-D8](model-lists-and-pickers.md#ML-D8)); no agent at the host gets your MCP servers, LSP
   servers or provider rows.
2. **yolo's own remedy deleted the entry it named, until [HC-D2](#HC-D2).** When a host apply
   would drop an MCP entry, its remedy said: declare it under `mcp_servers` in your user config,
   "one entry there reaches every agent" (`mcpEntryRemedy`,
   [`hostapplyremedy.go`](../../internal/cli/hostapplyremedy.go)).
   MEASURED by the research pass with a clean build: with `mcpServers.tavily` in
   `~/.pi/agent/mcp-adapter.json` **and** the same `mcp_servers.tavily` in the user config, the dry
   run still warned that the entry would be dropped, and `--assert` after `y` left
   `{"mcpServers": {}}`. The one remedy that works at the host today is a `config-overlay` naming
   each surface, for example in the local pack, and since [HC-D2](#HC-D2) that is the remedy the
   apply names (`mcpEntryRemedy`, pinned by `TestFollowingTheHostMCPRemedyKeepsTheEntry`). A later
   fix makes it name the surfaces and table keys that lost entries in the run (`droppedTablesOf`,
   pinned by `TestTheDroppedEntryRemedyNamesTheTableThatLostTheEntry`), so an LSP server dropped
   from `copilot/lsp` is no longer handed codex's `mcp_servers`. Since [HC-D20](#HC-D20), with the
   host running the derives over the user's own tables, the remedy names `mcp_servers`,
   `lsp_servers` and `providers` first again and keeps the overlay as the one-agent alternative
   (pinned by `TestTheHostMCPRemedyNamesWhatReachesTheHost` and
   `TestAnMCPServersEntryKeepsTheHostEntry`).
3. **`config-ref` stated the wrong reason, until [HC-D3](#HC-D3).** Its host-notch text said a
   provider has "no derive to feed" because "nothing in those files is a provider"
   (`config_ref.txt`). `pi/models`, `codex/config`, `opencode/config` and `oh-omp/models` carry
   provider rows, and `pi/codex-models` carries one provider's model list. Since [HC-D3](#HC-D3) the
   row says host apply composes no provider table and names those five, and a test measures the
   set from the shipped derives (`providerCarryingSurfaces`, in
   `internal/cli/confighostproviderdoc_test.go`).
4. **The status quo has an expiry date.** `assert` is ruled retired
   ([§4.5](config-ownership-and-promotion.md#45-retiring-assert--the-two-value-key)), leaving `none`
   and `own`, and `own` refuses every `computed` surface (`render.HostOwnedModes`). Once the
   retirement is built, `agy/mcp`, `copilot/lsp`, `copilot/mcp`, `oh-omp/models`, `pi/models`,
   `pi/codex-models` and `pi/mcp` have **no host path at all**, and neither does a pack overlay
   aimed at one of them, which is item 2's only working remedy. That is
   [OQ-HC2](#OQ-HC2), and it matters whatever [OQ-HC1](#OQ-HC1) rules.

## 5. The options

| Option | What changes | Cost | Verdict |
| :--- | :--- | :--- | :--- |
| **A.** Keep it ([ML-D8](model-lists-and-pickers.md#ML-D8)) | Nothing | Everything in [§4](#4-what-the-empty-layer-costs), and after the retirement no host path for any `computed` surface | Acceptable only until the retirement is built; the maintainer chose it "for now" for the codex list ([OQ-ML3](model-lists-and-pickers.md#OQ-ML3)) |
| **B1.** A registration declares its surface host-derivable | Host apply composes the inputs ([§6.2](#62-the-host-inputs)) and runs only the declared derives | A registration option; provider composition in host apply; a per-key write ([HC-D10](#HC-D10)); host columns in `config ls` and `config render`; catalogs become yolo-owned tables at the host ([§6.4](#64-per-contract-behavior)) | **Recommended.** Fails closed for a pack nobody audited |
| **B2.** Per key, inside the derive, beside `ctx.in_full` | A derive marks which of its keys may cross | Every derive author reasons key by key; one more wrapper in the decoder; finer host `rmw` table semantics | Rejected for now. One shipped key must not reach a real file, `claude/settings`' `mcpServers` tombstone, and it stays behind by a rule about the sentinel rather than an author's marking ([HC-D10](#HC-D10)); `env`, the one object a derive returns with no selection and does not declare in full, crosses as leaves. The selection-keyed keys (`modelPicker`, `pi/settings`' `subagents`, a profile's `model`) are gated by [OQ-HC3](#OQ-HC3), not by a marking. It can be added later without undoing B1 |
| **B3.** Every derive runs at the host, with a `ctx.notch` | Derives branch on the notch themselves | Reverses the "content does not cross" rule for every pack, fetched ones included; a derive that spells a jail path writes it into your real home | Rejected: it fails open, and it closes the "`DeriveCtx` carries no notch" gap ([notch-scoped §1](notch-scoped-config-contributions.md#1-the-case-and-why-no-channel-carries-it-today)) for a use this design does not need |
| **C.** A second channel for the codex list | For example, the extension asks `yolo internal …` at load | Fixes one surface; reopens the channel class [ML-D3](model-lists-and-pickers.md#ML-D3) rejected; pi's load then depends on a yolo binary | Rejected |

## 6. The proposed shape, under B1

### 6.1 Principles

- **P1. Same function, different inputs.** A host-derivable derive computes the same function at
  both notches. It never branches on the notch; one that needs to is not host-derivable.
- **P2. User scope only.** Every host input comes from your user config and your selected packs'
  provider facts and native capabilities, never from a workspace
  ([env-manager OQ-2](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)),
  and never from a pack's `mcp` declaration, which by
  [OQ-MP3](mcp-presets-removal.md#15-open-questions) "reaches only the jail".
- **P3. Undeclared means today.** A surface whose registration does not declare it renders exactly
  as it does now.
- **P4. One writer.** Host apply's code path is the only thing that writes a derived value into
  your real home, and every caller of it composes the inputs the same way
  ([HC-D11](#HC-D11)). `yolo host --` reaches that writer through its launch gate when
  `host_apply_on_launch` is on, which `host_management: own` turns on by default; its own launch
  composition sets the agent's environment and writes no derived value.

### 6.2 The host inputs

| Input | Host value | Decided by |
| :--- | :--- | :--- |
| `providers` | `composedHostProviders`' table: the user's `providers` entries over the selected packs' provider facts, with no adapter address a pack's own service serves | [OQ-CS10](../reference/providers.md#oq-cs10); ES-D18 in [the credential-sources ledger](credential-sources-separation.md#10-decision-ledger) |
| `ctx.via_url` | `""`. Host apply resolves no profile, so no via route is selected; under [OQ-HC3](#OQ-HC3) it resolves `use_profiles` and clears every via address the way `yolo host --` does (`packload.ViaInert`), because no jail daemon serves one here | [WG-I12](wire-bridge-gateway.md#WG-I12) |
| `mcp_servers` | Your user-scope `mcp_servers` entries, less any entry whose command or arguments name a jail path, which is named in the report. The `mcp_presets` expansion never runs at the host, and each preset it skips is named too. A pack's `mcp` declaration is never an input | [OQ-2](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) for the scope; [OQ-MP3](mcp-presets-removal.md#15-open-questions) for a pack's declaration; for the presets, their commands exist only in a jail, and [`mcp-presets-removal.md`](mcp-presets-removal.md) retires them ([HC-D6](#HC-D6)) |
| `requires_env` on an MCP entry | Checked **per surface agent**, against the environment `yolo host env --agent <agent>` would compose at apply time, as a jail checks it per agent. A server is written for each agent whose composition holds its variables, and a skipped one is named with its variables and the agents that did get it | [OQ-CN6](../reference/providers.md#oq-cn6); [HC-D6](#HC-D6) |
| Native capabilities | Each surface agent's own, from the selected packs' `program` declarations (`packload.NativeCapabilities`), as in a jail. So a server whose `provides` that agent's built-in login already performs is withheld from it: a `provides: "web_search"` server reaches neither claude's nor agy's surfaces | [HC-D6](#HC-D6) |
| `lsp_servers` | Your user-scope entries as written, less any that names a jail path. A `command` must resolve on the host's `PATH`, as it must in a jail | [OQ-LSP1](../reference/mcp-configuration.md#oq-lsp1); [HC-D6](#HC-D6) |
| `use_profiles`, and the selected profile and provider | Empty, unless [OQ-HC3](#OQ-HC3) rules otherwise | [OQ-HC3](#OQ-HC3) |
| `${workspace}` | Unbound; its branches are pruned by name, as today | [OQ-2](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) |

The composition runs **once per invocation** and every host-target reader of that invocation uses
it ([HC-D11](#HC-D11)): `yolo host apply`'s dry run, its `--assert`, its `--format json`
document, the launch gate `yolo host --` runs before a wrapped agent starts, `yolo config ls --at
host` and `yolo config render --at host`. So a preview shows what the write would do, and a
wrapped launch neither undoes an apply's rows nor reports them as drift.

### 6.3 Which surfaces declare it

Every shipped derived surface can, since none spells a jail path
([§3](#3-why-the-computed-layer-is-jail-only--the-stated-reasons-and-which-hold)), and the build
declares each one only after a test shows that its host output carries no jail-absolute value and
removes no key of yours outside a table declared in full ([§6.7](#67-what-done-looks-like) item 4).
The declaration exists for the next pack, not for these. `mise/config` is not a pack surface and
stays absent ([§9](#9-what-this-does-not-propose)).

### 6.4 Per-contract behavior

| Contract | A host-derivable surface | An undeclared one |
| :--- | :--- | :--- |
| `assert` (until retired) | Rendered through `rmw`, as every surface is under `assert`, with the derive's output as the computed layer, applied per key (below). A table declared in full is written wholesale, **so your `mcp_servers` entries are in it** and item 2 of [§4](#4-what-the-empty-layer-costs) becomes true advice | Today's behavior |
| `own` | A `stateful` or `rmw` surface takes the derive's output as its computed layer, per key as under `assert`. A `computed` surface per [OQ-HC2](#OQ-HC2); leaning: rendered through `stateful`, so the first owned render adopts the existing file | Today's behavior, with the refusal text reworded |
| `none` | Nothing is written | Nothing is written |

**Per key, by the declaration, and never through a tombstone** ([HC-D10](#HC-D10)). A derive's
output reaches a file you own by these rules, under both contracts:

1. **A table declared in full** (`ctx.in_full`) is yolo's, and is written wholesale, as the host
   writes its declared table layer today.
2. **Any other object a derive returns asserts only the leaves it names**; the rest of that object
   stays as your file holds it. The shipped case is `claude/settings`' `env`: with no LSP server
   the derive returns an empty `env` and yours is untouched, and with one it sets
   `ENABLE_LSP_TOOL` beside your variables.
3. **A tombstone is dropped before either arm sees it.** At the host the only layer below a derive
   is your real file, so a tombstone can only delete a key you wrote, the class
   [`packs/claude/derive.lua`](../../packs/claude/derive.lua) records as a data-loss bug for
   `enabledPlugins`. The shipped case is `claude/settings`' `mcpServers` tombstone: a `mcpServers`
   key in your real `settings.json` stays, as it does today. Applied under `own` only, it would
   also break the criterion that switching a home from `assert` to `own` keeps every key and every
   value ([config-ownership §11](config-ownership-and-promotion.md#11-success-criteria)).
4. **A leaf yolo stops asserting is cleared when the file still holds what yolo wrote**
   ([HC-D25](#HC-D25), revising the first build's rule, under which it stayed in the file under
   `assert`, attributed `retired:computed`). The rmw arm keeps a computed-leaf record beside the
   provenance record, the value yolo wrote at each leaf's RFC 6901 pointer, recorded only when
   yolo's write is what put it there. So removing your last LSP server removes `ENABLE_LSP_TOOL`
   from your real `env`, and moving claude off Bedrock removes `CLAUDE_CODE_USE_BEDROCK`, while a
   value you wrote before yolo asserted it, or changed after, stays. The top-level key still reads
   `retired:computed` in the provenance record when nothing under it is asserted any more. Under
   `own` the `stateful` recomposition stops writing it, so the two contracts agree.

> [!WARNING]
> **Handing the derive's output to today's `rmw` computed write clears your `env`.**
> `regenerateManagedTables` ([`prism.go`](../../internal/entrypoint/prism.go)) clears and
> rewrites every object-valued computed key, declared in full or not. MEASURED at `97220184`
> with a scratch test: `claude/settings`' derive over empty host inputs returns
> `{"env": {}, "mcpServers": null}` with nothing declared in full, and that write turned a file's
> `{"env": {"MY_VAR": "x"}}` into `{"env": {}}`. It is the defect `hostTableKeys` was fixed for
> ([CO13](config-ownership-and-promotion.md#built-2026-09-25--what-shipped)), arriving by
> another road, so rule 2 is an obligation on the build, not a description of today's writer.

Rule 2 is what the `in_full` declaration already means for a `stateful` surface and for the host's
table probe. For a surface a pack declares `rmw`, the jail's arm does not read the declaration yet,
and what it should do is [OQ-CO15](config-ownership-and-promotion.md#oq-co15), whose leaning is
rule 2. The host follows that ruling for such a surface, since one declaration must mean one thing
at both notches. No shipped `rmw`-declared derive returns an object it does not declare in full
(`claude/config` returns only `mcpServers`), so nothing shipped waits on it.

**The catalogs change owner.** A provider catalog declared in full becomes a yolo-owned table at the
host, so a provider you added by hand to a real file is dropped at the next host apply, where
[§2.3](#23-what-happens-to-an-entry-you-added-by-hand) shows it survives today. Whether catalogs
stay declared in full is [OQ-CO16](config-ownership-and-promotion.md#oq-co16), which should be
ruled with the host in view. While they are, the first host apply that makes a table yolo's in a
home asks before dropping anything in it ([HC-D8](#HC-D8)).

### 6.5 Degenerate inputs and failure paths

| Case | What happens |
| :--- | :--- |
| No selected pack declares a host-derivable surface | Today's behavior, and no provider composition runs |
| Empty provider table | Each derive writes its empty shape; `pi/models` writes `{"providers": {}}` ([HC-D1](#HC-D1)) |
| A derive raises an error at the host | That surface is refused, naming the error, and its file is left alone; other surfaces render ([HC-D7](#HC-D7)) |
| The provider composition fails, for example on a malformed `providers` entry | The apply refuses before writing anything, naming the error, as `yolo host --` does ([HC-D7](#HC-D7)) |
| An MCP preset is enabled | Not expanded; the report names it |
| A user `mcp_servers` or `lsp_servers` entry names a jail path | Omitted at the host and named in the report ([HC-D6](#HC-D6)) |
| An MCP entry's `requires_env` variable is in no surface agent's composition | The entry is skipped everywhere and named, with the variable ([HC-D6](#HC-D6)) |
| It is in some agents' compositions only, for example a provider credential that reaches only the agent selecting that provider | The entry is written for those agents only, and the report names them ([HC-D6](#HC-D6)) |
| The entry is written, and the agent is started outside `yolo host`, from an IDE or directly | The agent resolves `${VAR}` from its own environment, which holds only what your shell gave it. A variable that came from `env_sources` or a provider credential is absent there, and that one server fails when the agent spawns it. The file is right for a launch through `yolo host` or a wrapper, the launch that composed the check |
| An MCP entry sets `provides` and a surface agent's own login performs it | Withheld from that agent, as in a jail ([HC-D6](#HC-D6)) |
| A derive returns a tombstone | Dropped before the write ([HC-D10](#HC-D10)) |
| A derive returns an object it does not declare in full | Its leaves are asserted; nothing else under that key changes ([HC-D10](#HC-D10)) |
| Host yolo is older than the registration option | It runs no derive for content, so the option is inert and the host keeps today's behavior. An older entrypoint in a jail ignores it too |
| Config changes after an apply | The real file reflects the last apply until the next one, as declared layers do today |
| Two applies at once | Unchanged from today's host apply; this design adds no writer, and under [OQ-HC3](#OQ-HC3) one record beside the provenance record |

### 6.6 Forbidden behavior

- Never run an undeclared derive for content at the host.
- Never read a workspace config for a host input.
- Never write a jail-absolute value into your real home: a preset command, a path under
  `/workspace`, `/opt/yolo-jail`, `/run/yolo` or `/ctx`, or a jail home.
- Never compose an adapter address or a `via` route into a host input.
- Never take a pack's `mcp` declaration as a host input
  ([OQ-MP3](mcp-presets-removal.md#15-open-questions)).
- Never apply a tombstone to a real file, and never clear and rewrite an object a derive does not
  declare in full ([HC-D10](#HC-D10)).
- Never select a variant host apply was not told about. It has no `-p`.

### 6.7 What done looks like

1. With `mcp_servers.tavily` in your user config, `yolo host apply --assert` writes it into every
   host-derivable MCP surface, and an existing identical entry is neither warned about nor dropped.
   An entry that sets `provides` is written into the surfaces of every agent whose own login does
   not perform it, and nowhere else: a `provides: "web_search"` entry skips claude's and agy's.
2. Host pi registers the declared `openai-codex` list, 1M variants included, and pi-ai's GPT-5.x
   ids are gone from its picker, as in a jail. [ML-D8](model-lists-and-pickers.md#ML-D8)'s
   behavior and its test are inverted in the same change.
3. `yolo config ls --at host` shows a `computed` layer for each host-derivable surface, and
   `yolo config render --at host` shows its content.
4. A test renders every declared surface at the host and finds no jail-absolute value, and it
   fails when a preset is let through. The same test seeds each real file with a key of yours
   outside every table the derive declares in full (a variable in `claude/settings`' `env`, a
   `mcpServers` key in `settings.json`), and fails if the render removes or changes one.
5. `config-ref`'s host-notch text names the provider facts a host apply renders.

## 7. Fixes that do not wait for a ruling

These were defects in what ships, each with one right answer. All are built, and each ledger
row names its commit.

1. **[HC-D1](#HC-D1): `pi/models` always has `providers`.** The surface declares
   `"defaults": {"providers": {}}`, so neither a host apply into a home with no `models.json` nor a
   jail with no pi-reachable provider writes the `{}` pi rejects.
2. **[HC-D2](#HC-D2): the host MCP remedy names what reaches the host.** Until a host-derivable
   surface consumes `mcp_servers`, host apply's remedy names a `config-overlay` per surface, for
   example in the local pack, and stops promising that one `mcp_servers` entry reaches every agent.
   Since [HC-D20](#HC-D20), with the host deriving from the user config's tables, the remedy names
   those tables first again, with the overlay as the one-agent alternative.
3. **[HC-D3](#HC-D3): `config-ref`'s `provider` line says what is true**: host-rendered files carry
   provider facts, and host apply composes no provider table, so they render without them.
4. **[HC-D4](#HC-D4): a created file is a render.** A host apply that creates a file where none
   existed reports it as rendered, never as unchanged or already in sync.
5. **[HC-D5](#HC-D5): a loss line describes the write.** Under `own`, the report names an entry as
   dropped only when the `stateful` write drops it.
6. **[HC-D12](#HC-D12): a revert leaves a file its agent reads.** `yolo host apply --revert`
   keeps an empty declared default, so it no longer turns the `{"providers": {}}` HC-D1 writes
   back into the `{}` pi rejects.

## 8. What we found while looking at host pi + auto mode

The maintainer reported that host pi with `@czottmann/pi-automode` "didn't even launch correctly".
Nothing below reproduces that: the host's state is unknown to us, and **the maintainer will debug
it with us**. What follows is what the tree does, split by how we know it.

### 8.1 MEASURED

1. **`models.json` becomes `{}`, and pi shows an error at every start.** A host
   `yolo host apply --assert` into a home with no `~/.pi/agent/models.json` created it as `{}`.
   pi 0.87.1's `ModelsConfigSchema` requires `providers` (`dist/core/model-config.js`), its loader
   returned "Invalid models.json schema: providers: must have required properties providers",
   interactive mode prints it as `models.json error: …`, and pi continues with no custom providers.
   `{"providers": {}}` loads cleanly. **It happens in jails too**: a boot whose only agent pack is pi
   writes `{}` (re-run 2026-09-27), because since `92c20cc6`, in no release yet, pi's catalog never
   writes `openai-codex`, which leaves a pi-only jail no row. [HC-D1](#HC-D1) is the fix, built in
   `packs/pi/pack.json` and pinned by `internal/entrypoint/pimodelsproviders_test.go`: both now
   write `{"providers": {}}`, and the next boot or `--assert` repairs a `{}` an earlier one left.
   A `yolo host apply --revert --assert` removed that default again and left `{}`; since
   [HC-D12](#HC-D12) a revert keeps an empty declared default and names it
   (`entrypoint.keptShapeDefault`, pinned by `TestARevertKeepsPiModelsProviders`). Under `own` the
   surface is still refused, so nothing repairs it there ([OQ-HC2](#OQ-HC2)).
2. **The report hides that write.** The dry run said `pi/models` was unchanged, and the assert
   counted it among the destinations already in sync, while the file was created. Only `--verbose`
   showed `pi/models rendered`. Since [HC-D4](#HC-D4) a file the apply creates is a change in both
   postures (`entrypoint.hostSurfaceWouldChange`, pinned by
   `TestAHostApplyCountsAFileItCreatesAsAChange`), and a later fix makes that include one created
   through a dangling link (`TestAHostApplyThroughADanglingLinkReportsItAsAChange`).
3. **A direct or IDE launch cannot refresh the `openai-codex` login.** The host-delivered
   `yolo-openai-auth.js`, loaded under node with no yolo environment and yolo on `PATH`, failed its
   refresh with "OpenAI credential service: openai-auth-client:
   YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT is not set". Only `yolo host --` sets the host socket
   variable (`openaiauthhost.prepare`), and the error named the jail's variable instead of saying
   to launch through `yolo host`. Since the fix, with no route and outside a jail, it says pi
   was not started through `yolo host` and to launch it with `yolo host -- pi`, the client's words
   after it (`brokerFailure` in `yolo-openai-auth.js`, pinned by
   `TestPiOpenAIAuthOutsideAJailSaysToLaunchThroughYoloHost`). The extension counts either
   `YOLO_VERSION` or a `~/.yolo/bin` directory as a jail, so it cannot give that advice inside
   one, even with a scrubbed environment. MEASURED through pi
   0.87.1's own `discoverAndLoadExtensions` in a throwaway home, with no session and no network:
   a host's login and refresh both name `yolo host -- pi`, and with `YOLO_VERSION` set both keep
   the client's message.
4. **`use_profiles` changes nothing for host pi.** With `use_profiles: {"pi": "codex"}`, host
   `settings.json` got no `defaultProvider` or `defaultModel`, and `yolo host env --agent pi`
   composed only the two `YOLO_AUTH_PRELAUNCH_PI_*` variables the in-jail launcher reads. pi
   registers no `yolo.env` ([OQ-HC3](#OQ-HC3)).
5. **Under `own`, pi's three `computed` surfaces are refused**, with text naming `assert` as the
   remedy, and the `pi` wrapper `own` writes is `exec yolo host -- pi "$@"`.
6. **Two things that are not the cause.** A pack with two `autonomy` contributions gets a clear
   refusal naming the fix. And the adapter refusal (ES-D18) cannot reach pi, since every shipped
   adapter targets `anthropic`; the credential gate refuses only a missing key, and names its hatch.

### 8.2 Read from source, not run

1. **`yolo host -- pi` needs an OpenAI login before pi starts**, for any pi invocation, `pi
   --version` included, whatever provider pi then uses. `openaiauthhost.prepare` gates on the
   program's basename being `codex` or `pi`, starts the broker singleton, asks its status and, when
   not logged in, requests a browser login, on port 1455 or on 1457 when 1455 is taken, that waits
   up to 15 minutes. Its failures exit 1 with "yolo host: prepare shared OpenAI authentication:
   …". Every wrapped `pi` takes this path. It has shipped since `6d118252`, in v0.9.0. Not run end to end, because that
   would reach this jail's live broker.
2. **pi installs a missing `npm:` package at startup, with no catch on that path**
   (`package-manager.js`'s `resolve` → `installParsedSource`, pi 0.87.1). So with npm or the
   registry unavailable on the host, the first pi start after the posture list lands fails
   outright. pi-automode 1.17.0 has no install scripts and one dependency.
3. **pi-automode with no classifier model configured uses the session model**
   (`resolveClassifier` in its `classifier.ts`), so it blocks only when there is no session model or
   that provider fails, which includes item 3 of [§8.1](#81-measured).

The last two correct two rows of
[notch-scoped §4.5](notch-scoped-config-contributions.md#45-failure-paths), which now say so.

### 8.3 SUSPECTED, and what would tell them apart

| Candidate | What you would see |
| :--- | :--- |
| [§8.1](#81-measured) item 1 | pi starts, with a red `models.json error` line |
| [§8.2](#82-read-from-source-not-run) item 1 | `yolo host -- pi` or a wrapped `pi` opens a browser or exits with "prepare shared OpenAI authentication" before pi prints anything |
| [§8.2](#82-read-from-source-not-run) item 2 | pi exits during startup with an npm error |
| [§8.1](#81-measured) item 3 with pi-automode | pi starts, and tool calls are blocked after a refresh failure |
| A host yolo older than the posture lists (`bbe5c878`), and so older than host verbs refusing a manifest with problems (`d4aa6a43`) | `yolo host apply --assert` and `yolo host --` exit 0 and read the pack declaring the posture list as an empty manifest, so pi's `packages` lacks pi-automode and nothing says why. Only `yolo check`, `yolo pack lint` and a jail launch refuse the manifest by name ([notch-scoped §4.5](notch-scoped-config-contributions.md#45-failure-paths)) |

What would settle it: the first screen pi printed; `yolo host apply --verbose`;
`cat ~/.pi/agent/models.json`; `yolo openai-auth status`; `yolo --version`; your
`host_management` value; and whether pi started through `yolo host`, a wrapper, an IDE or directly.
Until then no candidate gets a question here: the `models.json` one is fixed by [HC-D1](#HC-D1),
the login before exec belongs to [`agent-credentials.md`'s OpenAI service](../reference/agent-credentials.md#the-openai-subscription-credential-service), and the npm
install and the classifier belong to
[`notch-scoped-config-contributions.md`](notch-scoped-config-contributions.md).

## 9. What this does not propose

- **No host `mise/config`.** Host apply renders pack surfaces, and `mise_tools` at the host are
  yours ([`host-tool-provisioning.md`](host-tool-provisioning.md)).
- **No `ctx.notch`**, by P1.
- **No `-p` for host apply.** Only [OQ-HC3](#OQ-HC3)'s `use_profiles` could select anything.
- **No MCP presets at the host**, including re-rendered with host commands.
- **No pack `mcp` declaration at the host.** [OQ-MP3](mcp-presets-removal.md#15-open-questions)
  kept that kind out of the host-grant banner because it "reaches only the jail". Carrying one to
  the host would change that premise, so it would be a question for that ruling, not a detail of
  this design.
- **No change to any jail render**, beyond [HC-D1](#HC-D1)'s default.
- **No change to the OpenAI broker's host launch** ([§8.2](#82-read-from-source-not-run) item 1),
  until the debugging says whether it is what was hit.

## 10. Risks

| Risk | Mitigation |
| :--- | :--- |
| A pack declares host-derivable and its derive spells a jail path | [§6.7](#67-what-done-looks-like) item 4 covers shipped packs; for a fetched pack the declaration is the author's claim, and the host apply banner already names what each pack writes |
| A derive's output removes a key of yours from a real file, through a tombstone or through an object cleared and rewritten | [HC-D10](#HC-D10)'s per-key rules, and the seeded-key half of [§6.7](#67-what-done-looks-like) item 4 |
| A hand-added provider is dropped at the host | [OQ-CO16](config-ownership-and-promotion.md#oq-co16); [HC-D8](#HC-D8) asks first |
| Host pi's first `/login` selects no model, since `gpt-5.5` is no longer registered ([ML-D4](model-lists-and-pickers.md#ML-D4)) | pi says so and `/model` picks one; under [OQ-HC3](#OQ-HC3) `use_profiles` sets the default |
| A literal `api_key` in your `providers` config lands in a real agent file | It is your own value, and a jail already writes it to disk under the workspace's home overlay |

## 11. Open questions

1. ✅ <a id="OQ-HC1"></a>**[OQ-HC1](#OQ-HC1): Does the host notch run derives for content, and at
   what grain?** This is the question [OQ-ML3](model-lists-and-pickers.md#OQ-ML3)'s ruling left for
   a doc of its own: *"basically option 1, but then make sure there's a design doc about the host
   option left."* It decides whether host pi gets yolo's `openai-codex` list, whether any host agent
   gets your MCP, LSP and provider entries, and whether ML-D8 is inverted. Options, costed in
   [§5](#5-the-options): **A**, keep ML-D8; **B1**, per registration; **B2**, per key; **B3**, every
   derive with a notch.

      _Leaning:_ **B1.** The inputs, not the derives, were the obstacle, and B1 fixes the inputs while
   failing closed for a pack nobody audited. Its output lands per key ([HC-D10](#HC-D10)), so no
   derive removes a key of yours. Its cost: a registration option; host apply composing the
   provider table; a per-key write the host's `rmw` arm does not have today; catalogs that become
   yolo's at the host, so [OQ-CO16](config-ownership-and-promotion.md#oq-co16) reaches the host;
   and host pi's picker losing pi-ai's GPT-5.x ids.

   <!-- vantage: oq id=OQ-HC1 -->

   **Answer:**
   > **Ruled in review 2026-09-28:** *"yes of course host apply and the auto one in a wrapper
   > should generate this content. we're trying for host parity with the same handling."* The
   > host runs the SAME derives the jail runs, through the same handling: `yolo host apply`, and
   > the automatic apply a wrapper launch does (`host_apply_on_launch`), render every derived
   > surface's computed layer over inputs composed at user scope ([§6.2](#62-the-host-inputs)).
   > That is none of the options as written: not B1, because a surface does not opt in, so the
   > handling is the jail's; not B3, because derives do not branch on the notch. The obstacle was
   > the inputs, not the derives ([§3](#3-why-the-computed-layer-is-jail-only--the-stated-reasons-and-which-hold)),
   > so the per-notch difference lives where the inputs are composed. B3's fail-open objection,
   > a derive that spells a jail path, is answered by composing host inputs and by checking the
   > host output for a jail path, an implementation decision for the build to ledger.

2. ✅ <a id="OQ-HC2"></a>**[OQ-HC2](#OQ-HC2): Under `host_management: own`, does a `computed`
   surface render through `stateful`, or stay refused?** `render.HostOwnedModes` refuses it on
   [OQ-CO9](config-ownership-and-promotion.md#13-decision-ledger)'s reasoning, "until a real
   example argues otherwise", and that ruling itself covers only keyless surfaces. The real
   examples now exist: `pi/models`, `pi/codex-models`, `pi/mcp`, `copilot/mcp`, `copilot/lsp`,
   `agy/mcp` and `oh-omp/models`. This decides whether those files have any host
   path once `assert` is gone, under A as much as under B, because a pack overlay aimed at one of
   them is refused under `own` too. It is not an implementation choice, because
   [§4.5](config-ownership-and-promotion.md#45-retiring-assert--the-two-value-key)'s obligations
   say that refusal "need[s] new text, not deletion", which assumes it stays.

      _Leaning:_ **Through `stateful`.** Capture-then-regenerate is the adoption path the refusal says
   is missing, so the first owned render adopts the file rather than replacing it; and the
   retirement's reason, *"a single promote away from having their configs managed correctly"*,
   cannot hold for a file `own` refuses. Its cost: those files are composed whole, with a capture
   baseline, under `own`. `oh-omp/models` also needs a yaml encoder for that path, which is
   UNMEASURED.

   <!-- vantage: oq id=OQ-HC2 -->

   **Answer:**
   > **Ruled 2026-09-28, as leaned, by [OQ-HC1](#OQ-HC1)'s parity ruling:** under `own` a
   > `computed` surface renders through `stateful`, the first owned render adopting the file
   > rather than replacing it. Without it those files would have no host path once `assert`
   > retires, which parity rules out.

3. ✅ <a id="OQ-HC3"></a>**[OQ-HC3](#OQ-HC3): Does host apply render the variant `use_profiles`
   selects?** It selects none today, by a code comment and no ruling. `yolo host --` does honor
   `use_profiles`, but only for the environment, so for pi, which reads no yolo environment, the
   setting does nothing at the host ([§8.1](#81-measured) item 4). It decides whether a direct or
   IDE launch of pi starts on your chosen provider and model. It matters only if
   [OQ-HC1](#OQ-HC1) is not A, since the selection keys come from derives.

      _Leaning:_ **Yes, `use_profiles` only**, with the jail's edge-triggered rule: yolo writes the
   selection when the chosen profile changes, and the per-key deselect rule
   ([OQ-PSW2](../reference/providers.md#oq-psw2)) clears only what it wrote, so your own later
   `/model` pick stands. Its cost: a per-home selection record beside the provenance record, and
   yolo writing a model choice into a real agent file. If the answer is no, host apply names
   `use_profiles` as not applied, so it stops doing nothing silently.

   <!-- vantage: oq id=OQ-HC3 -->

   **Answer:**
   > **Ruled 2026-09-28, as leaned, by [OQ-HC1](#OQ-HC1)'s parity ruling:** host apply writes
   > the selection `use_profiles` picks, with the jail's own edge-triggered rule: yolo writes it
   > when the chosen profile changes and clears only what it wrote
   > ([OQ-PSW2](../reference/providers.md#oq-psw2)), so your own later `/model` pick stands.

## 12. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-ML3 | *Inherited from [`model-lists-and-pickers.md`](model-lists-and-pickers.md#OQ-ML3).* **A, for now: `yolo host apply` renders no `openai-codex` list.** *"basically option 1, but then make sure there's a design doc about the host option left."* This doc is that design, and [OQ-HC1](#OQ-HC1) is the question it left | 2026-09-27 | [model-lists OQ-ML3](model-lists-and-pickers.md#OQ-ML3) | ✅ [ML-D8](model-lists-and-pickers.md#ML-D8) |
| OQ-HC1 | **Maintainer ruling:** *"yes of course host apply and the auto one in a wrapper should generate this content. we're trying for host parity with the same handling."* The host runs the jail's derives over user-scope inputs, in host apply and in the wrapper's automatic apply; no per-surface opt-in and no notch branch. Supersedes [OQ-ML3](model-lists-and-pickers.md#OQ-ML3)'s "for now" A at the host | 2026-09-28 | [§11](#11-open-questions) | ✅ `358f877d` ([§14](#14-what-was-built)) |
| OQ-HC2 | **Maintainer ruling**, as leaned, by [OQ-HC1](#OQ-HC1)'s parity: under `own` a `computed` surface renders through `stateful`, adopting the file on the first owned render | 2026-09-28 | [§11](#11-open-questions) | ✅ `358f877d` ([HC-D24](#HC-D24)) |
| OQ-HC3 | **Maintainer ruling**, as leaned, by [OQ-HC1](#OQ-HC1)'s parity: host apply writes the `use_profiles` selection with the jail's edge-triggered rule | 2026-09-28 | [§11](#11-open-questions) | ✅ `358f877d` ([HC-D17](#HC-D17), [HC-D18](#HC-D18)) |
| <a id="HC-D1"></a>HC-D1 | *Implementation decision.* **`pi/models` declares `"defaults": {"providers": {}}`.** pi 0.87.1 rejects a `models.json` without `providers`, and both a host apply into a fresh home and a pi-only jail wrote `{}` before it ([§8.1](#81-measured) item 1). With the default, both render `{"providers": {}}`, and the `-short` suites of `internal/entrypoint`, `internal/cli`, `internal/packload` and `packs` still pass (MEASURED by the research pass in a scratch copy). A derive's rows replace it, since the catalog is declared in full; under `rmw` a default fills only an absent key, so an existing host file keeps its own `providers`. A regression test asserts the rendered file has an object-valued `providers` at both notches | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | ✅ `packs/pi/pack.json`, pinned by `internal/entrypoint/pimodelsproviders_test.go`; kept through `--revert` since [HC-D12](#HC-D12), `TestARevertKeepsPiModelsProviders` |
| <a id="HC-D2"></a>HC-D2 | *Implementation decision.* **Host apply's MCP remedy names a `config-overlay` per surface,** for example in the local pack, because that is the only declaration that reaches a host MCP table today ([§4](#4-what-the-empty-layer-costs) item 2). Once a host-derivable surface consumes `mcp_servers`, the remedy names `mcp_servers` for that surface again. **Superseded 2026-09-28 by [HC-D20](#HC-D20)**: the host now runs every derive over the user's tables, so the remedy names `mcp_servers`, `lsp_servers` and `providers` first, and this row's per-surface `config-overlay` is the one-agent alternative | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | ✅ `mcpEntryRemedy`, pinned by `TestFollowingTheHostMCPRemedyKeepsTheEntry`; the example and keys from the tables that lost entries, `droppedTablesOf`, pinned by `TestTheDroppedEntryRemedyNamesTheTableThatLostTheEntry`; the [user guide](../../userguide/guides/migrating-to-packs.md#step-2-apply) and the [remedy contract](../reference/report-tiers.md#the-remedy-contract) |
| <a id="HC-D3"></a>HC-D3 | *Implementation decision.* **`config-ref`'s host-notch `provider` line says host-rendered files carry provider facts** and render without them because host apply composes no provider table; it changes again with [OQ-HC1](#OQ-HC1) | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | ✅ `7d12abb0` |
| <a id="HC-D4"></a>HC-D4 | *Implementation decision.* **A host apply that creates a file reports it as rendered,** in the dry run and the assert, never as unchanged or in sync ([§8.1](#81-measured) item 2) | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | ✅ `entrypoint.hostSurfaceWouldChange`, pinned by `TestAHostApplyCountsAFileItCreatesAsAChange`; through a dangling link, `TestAHostApplyThroughADanglingLinkReportsItAsAChange` |
| <a id="HC-D5"></a>HC-D5 | *Implementation decision.* **Under `own`, a loss line names only what the `stateful` write drops.** MEASURED: a first owned apply kept a hand-added `mcp_servers` entry in `codex/config` and `mcp` entry in `opencode/config` while the report named both as dropped ([§2.3](#23-what-happens-to-an-entry-you-added-by-hand)). The loss list is computed by the mechanism that writes. The same change makes `own`'s `codex/config` header stop saying "composed at jail start" at the host | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | ✅ `entrypoint.hostMechanismTableLosses` and `render.Target.GeneratedHeader`, pinned by `TestAnOwnedHostApplyDoesNotReportAnEntryItKeeps` and `TestAnOwnedHostRenderNamesTheHostApplyInItsBanner`; a table of three or more entries is cleared whole, under both contracts and in a jail, by `entrypoint.regenerateManagedTables`, pinned by `TestAHostTableWriteKeepsOnlyTheConfiguredEntry` and `TestABootDropsEveryHandAddedServerFromClaudeJSON`; the JSON no-banner test reads the written files, `TestJSONSurfaceGetsNoGeneratedHeader` |
| <a id="HC-D6"></a>HC-D6 | *Implementation decision*, if [OQ-HC1](#OQ-HC1) is not A. **The host `mcp_servers` input is your user-scope entries, with no preset expansion and no pack `mcp` declaration, and it is filtered per surface agent, as a jail filters it.** A pack's declaration stays out because [OQ-MP3](mcp-presets-removal.md#15-open-questions) ruled it "reaches only the jail". Presets stay out because their node wrapper is written only by a jail's boot and their npm prefix is the jail's; the host can state those paths ([OQ-MP4](mcp-presets-removal.md#OQ-MP4) dissolved the premise that it cannot), but no real home has what they name, and [`mcp-presets-removal.md`](mcp-presets-removal.md) retires them. A user entry, MCP or LSP, whose command or arguments name a jail path is omitted and named, since writing it would break [§6.6](#66-forbidden-behavior)'s rule against jail-absolute values; which prefixes count is the implementer's list, and it includes the jail home, `/workspace`, `/opt/yolo-jail`, `/run/yolo` and `/ctx`. **Per surface agent:** `requires_env` is checked against the environment `yolo host env --agent <agent>` would compose at apply time, since that composition scopes a provider credential to the agent that selected it ([OQ-CN6](../reference/providers.md#oq-cn6)); and the capability filter runs with that agent's native capabilities (`packload.NativeCapabilities`), so a server whose `provides` the agent's own login performs is withheld from it. Without the capabilities the filter keeps every server, and a server a jail withholds from claude or agy would be written for them at the host, where the same config should render the same entries. A skipped server is named with its variables and with the agents that did get it, as the jail's notice names it | 2026-09-27 | [§6.2](#62-the-host-inputs) | ✅ `358f877d` |
| <a id="HC-D7"></a>HC-D7 | *Implementation decision*, if [OQ-HC1](#OQ-HC1) is not A. **A derive error at the host refuses that surface only; a provider composition error refuses the apply before any write.** The first is one pack's defect, and the rest of the apply is independent of it. The second is an input every host-derivable surface shares, and `yolo host --` refuses on it too | 2026-09-27 | [§6.5](#65-degenerate-inputs-and-failure-paths) | ✅ `358f877d` |
| <a id="HC-D8"></a>HC-D8 | *Implementation decision*, if [OQ-HC1](#OQ-HC1) is not A and catalogs stay declared in full ([OQ-CO16](config-ownership-and-promotion.md#oq-co16)). **The first host apply that makes a table yolo's in a home confirms before dropping an entry in it,** as a first apply does today (`confirmHostLosses`). Without it a home already asserted would lose a hand-added provider with a report and no prompt, because the prompt fires only on a first apply | 2026-09-27 | [§6.4](#64-per-contract-behavior) | ✅ `358f877d` |
| <a id="HC-D9"></a>HC-D9 | *Implementation decision*, if [OQ-HC1](#OQ-HC1) rules B1. **The declaration is an option on the `yolo.derive` registration, not a manifest field.** The registration already is the computed-layer declaration (`packload.DerivedSurfaces`), and "its output is host-valid" is a fact about that function. Its spelling is the implementer's | 2026-09-27 | [§5](#5-the-options) | ⛔ not built: [OQ-HC1](#OQ-HC1) ruled against a per-surface opt-in, so there is no registration option |
| <a id="HC-D10"></a>HC-D10 | *Implementation decision*, if [OQ-HC1](#OQ-HC1) is not A. **A derive's output reaches a real file per key: a table declared in full is written wholesale, any other object asserts only its leaves, and a tombstone is dropped before either arm sees it** ([§6.4](#64-per-contract-behavior)). Leaves, because that is what `ctx.in_full` already means for a `stateful` surface and for the host's table probe ([CO13](config-ownership-and-promotion.md#built-2026-09-25--what-shipped)), and today's `rmw` computed write would otherwise clear `claude/settings`' `env` (MEASURED, [§6.4](#64-per-contract-behavior)). No tombstone, because at the host the only layer below a derive is your real file, so a tombstone can only delete your key; and applying it under `own` alone would break [config-ownership §11](config-ownership-and-promotion.md#11-success-criteria)'s switch criterion. For a surface a pack declares `rmw`, [OQ-CO15](config-ownership-and-promotion.md#oq-co15) governs, and the host follows its ruling | 2026-09-27 | [§6.4](#64-per-contract-behavior) | ✅ `358f877d` |
| <a id="HC-D11"></a>HC-D11 | *Implementation decision*, if [OQ-HC1](#OQ-HC1) is not A. **One host-input composition per invocation, shared by every host-target reader:** the apply's dry run, `--assert` and `--format json`, the launch gate `yolo host --` runs before a wrapped agent, and `yolo config ls` and `render` at `--at host`. The launch gate runs the apply's own code path with writes on (`hostApplyGateApply`), so a composition any of them skipped would render catalogs without providers and undo an apply's rows, or report them as drift, at every wrapped launch | 2026-09-27 | [§6.2](#62-the-host-inputs) | ✅ `358f877d` |
| <a id="HC-D12"></a>HC-D12 | *Implementation decision.* **`yolo host apply --revert` keeps a `defaults` key whose declared default is an empty object or array and that still holds exactly that, and names it as kept.** MEASURED: on a fresh home with packs `["pi"]`, `--revert --assert` after an `--assert` removed `pi/models`' `providers` default and left `{}` ([§8.1](#81-measured) item 1). A revert empties a file rather than deleting it, and deleting a file yolo created was the other answer; it needs a record of creation yolo does not keep, and the revert refuses to guess it. An empty default holds nothing to withdraw: it is the shape the pack declares its file needs. A non-empty default still goes, and so does an empty one the user has since filled | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | ✅ `entrypoint.keptShapeDefault`, pinned by `TestARevertKeepsPiModelsProviders` and `TestHostRevertKeepsAnEmptyDefaultAndSaysSo` |
| <a id="HC-D13"></a>HC-D13 | *Implementation decision.* **The host inputs travel as the jail's own wire tables, in the host Env's variables** (`YOLO_PROVIDERS`, `YOLO_PROFILES`, `YOLO_USE_PROFILES`, `YOLO_MCP_SERVERS`, `YOLO_LSP_SERVERS`), so the readers a jail's boot uses (`LoadProviders`, `LoadProfiles`, `LoadUseProfiles`, `mcpServersWith`, `LoadLSPServers`, `surfaceSelectionFor`) read the host composition unchanged. That is the ruling's "same handling" as code: the one per-notch difference is the composition in `composeHostInputs` (internal/cli). `YOLO_MCP_PRESETS` is deleted from the variables even when a caller sets it | 2026-09-28 | [OQ-HC1](#OQ-HC1) | ✅ `358f877d` |
| <a id="HC-D14"></a>HC-D14 | *Implementation decision.* **The jail-path check runs over the derive's output, keys and values, and refuses that surface by name.** The roots are derived from the jail render target the boot projects onto (its home and workspace), plus the four mounts a launch supplies to every container jail (the context root, the install prefix, `/run/yolo`, the service endpoint directory). A root counts only as a whole path token. The check reads the derive's output and not the file, because the file's other keys are the user's or the declared layers'. The same predicate (`entrypoint.JailPathsIn`) omits a user's `mcp_servers` or `lsp_servers` entry at composition ([HC-D6](#HC-D6)), so one bad entry costs that entry, and the output check is the backstop for a derive spelling a path itself. | 2026-09-28 | [OQ-HC1](#OQ-HC1) | ✅ `358f877d` |
| <a id="HC-D15"></a>HC-D15 | *Implementation decision.* **A root the rendered home lies at or under is not a jail path there.** `yolo host apply` run inside a jail renders into that jail's `/home/agent`, and an account whose home is `/home/agent` is the same case: a path under the real home is host-valid | 2026-09-28 | [HC-D14](#HC-D14) | ✅ `358f877d` |
| <a id="HC-D16"></a>HC-D16 | *Implementation decision.* **MCP presets are excluded at the host, not composed for it, and each one is named in the report with its remedy.** A preset's command is the node wrapper only a jail's boot writes, run against the jail's npm prefix; composing a host spelling would mean yolo installing and maintaining npm packages in a real home, which [`mcp-presets-removal.md`](mcp-presets-removal.md) retires the presets rather than grow. The report line says to declare the server under `mcp_servers` with a command the machine has | 2026-09-28 | [OQ-HC1](#OQ-HC1), [HC-D6](#HC-D6) | ✅ `358f877d` |
| <a id="HC-D17"></a>HC-D17 | *Implementation decision.* **At the host, a file value the selection record does not hold is outranked by the first activation; a recorded key whose value differs is the user's own pick and stands.** The jail's rule outranks a value its host layer carried ([OQ-SW1](../reference/providers.md#oq-sw1)); at the host the real file is that layer and there is no other, so the record decides instead (`hostSelectionBaseline`). Without it a `defaultModel` the user set before choosing `use_profiles` would win forever and the selection would do nothing, the silent no-op [OQ-HC3](#OQ-HC3) exists to end. Both contracts ask the same function | 2026-09-28 | [OQ-HC3](#OQ-HC3) | ✅ `358f877d` |
| <a id="HC-D18"></a>HC-D18 | *Implementation decision.* **Under `assert` the selection record lives beside the provenance record** (`render.Target.SelectionPath`), since that contract keeps no capture store, as the config-list insert record already does. The rmw arm decides the edge over the file as it stands, writes only the values the new record holds, deletes a cleared key, and persists the record after the write. Under `own` the record stays in the capture store | 2026-09-28 | [OQ-HC3](#OQ-HC3) | ✅ `358f877d` |
| <a id="HC-D19"></a>HC-D19 | *Implementation decision.* **The rmw arm takes the derive's leaves through `surfaceContribs`, applied after its table write and before `managed`.** Every rmw helper (the write, the preview, the change predicate, the loss and formatting probes) already takes that value, so the leaves reach each without a new parameter, and a jail never sets them. The provenance record labels each leaf's top-level key `computed`, at the record's top-level grain, so a leaf yolo stops asserting reads `retired:computed` ([§6.4](#64-per-contract-behavior) rule 4); since [HC-D25](#HC-D25) the leaf itself is also cleared when the file still holds yolo's value. Empty objects are pruned from the leaves, since writing one would add a key the user never had | 2026-09-28 | [HC-D10](#HC-D10) | ✅ `358f877d` |
| <a id="HC-D20"></a>HC-D20 | *Implementation decision.* **The dropped-entry remedy names the user config's tables again** (`mcp_servers`, `lsp_servers`, `providers`), with the per-surface `config-overlay` kept as the one-agent alternative, and groups on the user config. [HC-D2](#HC-D2) said it would, once a host surface consumed those tables | 2026-09-28 | [HC-D2](#HC-D2) | ✅ `358f877d` |
| <a id="HC-D21"></a>HC-D21 | *Implementation decision.* **The tables written wholesale are the key-name probe's and the real derive's in-full keys together.** The probe (`hostTableKeys`) keeps a table yolo's when its derive omits the key over empty input (codex's and opencode's `omitEmpty`), so removing the last server regenerates the table empty rather than leaving the file's copy. Each table holds the overlays' entries, then the derive's, then managed's | 2026-09-28 | [HC-D10](#HC-D10) | ✅ `358f877d` |
| <a id="HC-D22"></a>HC-D22 | *Implementation decision.* **A surface whose declared layers were all `${workspace}`-keyed is skipped only when its derive computes nothing either.** `claude/config` was skipped at the host for that reason and now renders its derive's `mcpServers`, so yolo owns `~/.claude.json`'s user-scope server list at the host as in a jail: a server added with `claude mcp add` is a loss the first apply confirms, and declaring it under `mcp_servers` keeps it | 2026-09-28 | [OQ-HC1](#OQ-HC1) | ✅ `358f877d` |
| <a id="HC-D23"></a>HC-D23 | *Implementation decision.* **`provider` and `profile` leave the kinds the host notch names as not applying.** Both render into the files of the surfaces their facts reach, as `config-overlay` does, so the `--verbose` report names the composed provider table and the selection in one `input` line, and each input the composition leaves out gets a line of its own in every view. A composition that fails refuses the apply in both postures, before anything is written | 2026-09-28 | [HC-D7](#HC-D7) | ✅ `358f877d` |
| <a id="HC-D24"></a>HC-D24 | *Implementation decision.* **`own`'s `computed` → `stateful` is a stated coercion in the census** (`render.ModeSet.coerce`), since that census runs two composing mechanisms and `Mechanism`'s derived fallback coerces only onto a sole one. A keyless `computed` surface is still refused, by `hostStatefulRefusal`'s [OQ-CO9](config-ownership-and-promotion.md#13-decision-ledger) carve-out | 2026-09-28 | [OQ-HC2](#OQ-HC2) | ✅ `358f877d` |
| <a id="HC-D25"></a>HC-D25 | *Implementation decision, from a review; revises [§6.4](#64-per-contract-behavior) rule 4.* **At the host the rmw arm clears a computed leaf yolo wrote once its derive stops asserting it, when the file still holds yolo's value.** Measured: `yolo host apply` on `bedrock` wrote `CLAUDE_CODE_USE_BEDROCK` into the real `~/.claude/settings.json`, moving claude to `codex` and applying again left it, every later launch composed it as the user's host layer, and claude ran in Bedrock mode with no AWS credential while [PP-D1](providers-and-profiles-redesign.md#PP-D1)'s line blamed the user; `ENABLE_LSP_TOOL` stayed the same way after the last LSP server went. The provenance record is per top-level key, and `env` reads `computed` whenever any leaf under it is yolo's, so a **computed-leaf record** (coined here) keeps, per leaf, its RFC 6901 pointer and the value yolo wrote, recorded only when yolo's write is what put that value in the file (`render.Target.LeafRecordPath`, beside the provenance record; `entrypoint.hostRMWLeafRecord`). A leaf the file already held with that value is the user's and is never recorded, and a recorded one the user changed is theirs; a clear removes the leaf from the file's own content before any layer writes, so a live layer asserting the same path (another pack's config-overlay, managed, a default) still sets it, and keeps its parent; the record is written after the write, never in a dry run, and a revert removes it. The selection namespace keeps its own record ([HC-D18](#HC-D18)). A launch reads the same record to tell a platform switch yolo wrote from one the user wrote (`render.HostLeafWrote`). Not covered: a value an older build wrote before the record existed reads as the user's, the direction that deletes nothing | 2026-09-29 | [HC-D10](#HC-D10), [HC-D19](#HC-D19) | ✅ `hostRMWLeafRecord`; pinned by `TestHostApplyClearsTheBedrockSwitchItWroteWhenClaudeLeavesBedrock` and `TestTheHostLeafRecordNamesTheSwitchYoloWrote` |

## 13. Evidence

- **The inventory and the pi-only jail**, [§2.2](#22-the-inventory) and [§8.1](#81-measured) item 1:
  scratch tests in a clean `git archive` of `97220184` under a temporary home, run by the research
  pass on 2026-09-27; the pi-only jail case was re-run for this doc. The scratch tests are not
  committed.
- **Hand-added entries**, [§2.3](#23-what-happens-to-an-entry-you-added-by-hand): a scratch test
  written for this doc, same copy, same day.
- **The MCP remedy loss**, [§4](#4-what-the-empty-layer-costs) item 2, and the report counts,
  [§8.1](#81-measured) item 2: the research pass, with a clean build of `yolo` and a temporary home.
- **pi 0.87.1**, read from its installed package and never run as a CLI: `ModelsConfigSchema`, the
  `models.json error` line in `modes/interactive/interactive-mode.js`, and the package manager's
  install path. **pi-automode 1.17.0**, read from its npm tarball.
- **The jail-path search**, [§3](#3-why-the-computed-layer-is-jail-only--the-stated-reasons-and-which-hold):
  `rg` over `packs/*/derive.lua` at `97220184`.
- **The per-key rules**, [§6.4](#64-per-contract-behavior) and [HC-D10](#HC-D10): two scratch
  tests in the same copy, 2026-09-27. One ran every shipped derive with no selection, over empty
  and over sentinel input tables: the only object returned without an in-full declaration is
  `claude/settings`' `env`, and the only tombstone is that surface's `mcpServers`. The other handed
  `claude/settings`' output to `regenerateManagedTables` over a file holding `env.MY_VAR`, which
  it cleared.
- **The fixes and the extension message**, [§7](#7-fixes-that-do-not-wait-for-a-ruling) and
  [§8.1](#81-measured) item 3: each has a committed test that fails with its fix reverted, named
  in its commit. The message was also measured through pi 0.87.1's own
  `discoverAndLoadExtensions`, in a throwaway home with this tree's `yolo` on `PATH`, no session
  and no network, 2026-09-27. The [HC-D5](#HC-D5) loss test was run against a `git archive` of
  `v0.10.0` too: its steady-state case fails there for both agents, so that release already
  reported a kept hand-added entry as dropped.

## 14. What was built

As of `358f877d`, each derived surface at the host, under `assert` and under `own`:

| Surface | What host apply now writes | Under `own` |
| :--- | :--- | :--- |
| `pi/models` | a row per pi-reachable provider in the composed table (`openai-codex` never, as in a jail) | through `stateful` |
| `pi/codex-models` | the declared `openai-codex` list, 1M variants included, which the delivered extension registers ([ML-D8](model-lists-and-pickers.md#ML-D8) superseded) | through `stateful` |
| `pi/mcp`, `copilot/mcp`, `agy/mcp` | your `mcp_servers`, filtered per agent | through `stateful` |
| `copilot/lsp` | your `lsp_servers` | through `stateful` |
| `oh-omp/models` | provider rows | through `stateful`; refused under `assert`, which has no yaml encoder for `rmw` |
| `claude/config` | your `mcp_servers` as `mcpServers` ([HC-D22](#HC-D22)) | `rmw`, as declared |
| `claude/settings` | `env.ENABLE_LSP_TOOL` beside your variables. `use_profiles` naming claude's `codex` profile is left out and named: it needs the wire bridge, which no host process runs | `stateful`, as declared |
| `codex/config`, `opencode/config` | your MCP servers, a row for each provider the agent can reach, and the selected profile's model (MEASURED 2026-09-28 with `yolo config render --at host`: `codex` → `model = "gpt-6-sol"`; opencode on `cerebras` → its provider row, `model` and `small_model`) | `stateful`, as declared |
| `pi/settings` | the selected profile's `defaultProvider` and `defaultModel` and the pi-subagents policy | `stateful`, as declared |

The inputs are composed once per invocation ([HC-D11](#HC-D11)) by `composeHostInputs`, for the
dry run, `--assert`, `--format json`, the launch gate, and `yolo config render --at host`. `yolo
config ls --at host` lists each derived surface's `computed` layer from the derive
registrations. The tests are in `internal/entrypoint/hostcomputedlayer_test.go` and
`internal/cli/hostcomputedlayer_test.go`; each fails with its call site removed (checked by
mutation on 2026-09-28).

**Not built, and why:** `yolo config reset --at host` under `own` still truncates a surface to
its declared layers, and the next apply restores the computed layer over it. A computed leaf
that changes a value of yours (a first `use_profiles` activation over an earlier
`defaultModel`) is a change the report counts, not an overwrite line: the overwrite report
reads the declared layers only.
