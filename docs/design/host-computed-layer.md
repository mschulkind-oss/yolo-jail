---
title: "What a jail derives, the host leaves empty — for a reason that holds for one input in four"
date: 2026-09-27
status: in-review
tags: [host, notch, derive, computed, providers, mcp, lsp, profiles, pi, host-apply]
summary: "yolo host apply renders no derive's content, so every derived pack surface reaches the real home with its declared layers only: no provider catalog, no MCP or LSP entries, no openai-codex list for pi. The stated reason is jail-absolute paths, and only the MCP presets carry any. This doc proposes letting a derive declare its output host-valid and running it over inputs the host composes at user scope, argues why leaving it out stops being an option once assert retires, and records what a look at host pi with pi-automode found."
vantage:
  status-chip: true
---

# What a jail derives, the host leaves empty — for a reason that holds for one input in four

**Status:** DESIGN, 2026-09-27. Nothing built. Evidence MEASURED at `b0460995` in a clean build under a temporary home, with no host, no pi CLI and no jail started. Code claims cite a symbol, never a line.

> **In short.** The host leaves the computed layer empty because a jail's derive inputs carry
> jail paths, yet only the MCP presets do: the provider table is already composed at the host for
> `yolo host --`, and the LSP table is host-valid as written. So deriving at the host means
> composing host inputs, not rewriting derives, and leaving it out stops being stable once the
> `assert` retirement is built, since then no `computed` surface has any host path.

**Why it matters.** At `yolo host apply` every derived surface renders its declared layers only.
Host pi keeps pi-ai's own `openai-codex` list, no MCP server, LSP server or provider row reaches
the real home, and yolo's own remedy for a dropped MCP entry deletes the entry
([§4](#4-what-the-empty-layer-costs)).

**The shape.** A derive registration may declare its output *host-valid*; host apply composes the
four derive inputs at user scope and runs only those derives
([§6](#6-the-proposed-shape-under-b1)).

**Cost.** A registration option, a provider composition inside host apply, and provider catalogs
that become yolo-owned tables at the host, where a hand-added provider survives today.

**Start at [§3](#3-why-the-computed-layer-is-jail-only--the-stated-reasons-and-which-hold)**, the
reasons. Everything else falls out of which of them hold.

**Needs your ruling:** [OQ-HC1](#OQ-HC1), then [OQ-HC2](#OQ-HC2) and [OQ-HC3](#OQ-HC3).

**Reads with:** [`model-lists-and-pickers.md`](model-lists-and-pickers.md#OQ-ML3) (the ruling that
asked for this doc), [`host-render-target.md`](host-render-target.md#33-what-each-target-supplies)
(the premise re-argued here), [`config-ownership-and-promotion.md`](config-ownership-and-promotion.md#45-retiring-assert--the-two-value-key)
(the `assert` retirement), [`notch-scoped-config-contributions.md`](notch-scoped-config-contributions.md)
(pi-automode at the host), and [`host-computed-layer-plan.md`](host-computed-layer-plan.md) (the
implementation sketch, incomplete while [OQ-HC1](#OQ-HC1) is open).

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
user config and your selected packs. The derives do not change, because none of them was ever the
problem: none embeds a jail path of its own, and every jail-absolute value a host render would
have to fear arrives through one input table, `mcp_servers`, from the MCP presets.

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

MEASURED at `b0460995`: the jail column is `deriveComputedLayer` over providers composed from
every embedded pack, one MCP preset and one LSP server; the host columns are `RenderHostPack`
into a fresh home.

| Surface | Mode | What a jail's derive writes | Host, `assert` | Host, `own` |
| :--- | :--- | :--- | :--- | :--- |
| `agy/mcp` | computed | `mcpServers` | `{"mcpServers": {}}` | refused |
| `claude/config` | rmw | `mcpServers` | skipped when no overlay targets it: only `${workspace}`-keyed keys remain | same |
| `claude/settings` | stateful | `env.ENABLE_LSP_TOOL` with an LSP server; with `-p codex`, the model picker and allowlist | declared keys only | same |
| `codex/config` | stateful | `mcp_servers`, `model_providers`; the profile's `model` | empty `[mcp_servers]`, no `model_providers` | same, under a header saying "composed at jail start" |
| `copilot/lsp` | computed | `lspServers` | `{"lspServers": {}}` | refused |
| `copilot/mcp` | computed | `mcpServers` | `{"mcpServers": {}}` | refused |
| `oh-omp/models` | computed, yaml | `providers` | refused: no `rmw` encoder for yaml | refused |
| `opencode/config` | stateful | `mcp`, `provider`; the profile's `model` and `small_model` | `"mcp": {}`, no `provider` | same |
| `pi/settings` | stateful | with `-p codex`: `defaultProvider`, `defaultModel`, the pi-subagents policy | declared keys only | same |
| `pi/models` | computed | `providers` rows | `{}`, which pi rejects ([§8.1](#81-measured)) | refused |
| `pi/codex-models` | computed | the declared `openai-codex` list | `{}` ([ML-D8](model-lists-and-pickers.md#ML-D8)) | refused |
| `pi/mcp` | computed | `mcpServers` | `{"mcpServers": {}}` | refused |
| `mise/config` (core) | stateful | `[tools]` from `mise_tools`, a computed layer core supplies rather than a derive | absent: host apply walks pack surfaces only | same |

The `files` kind is unaffected: host apply delivers pi's `yolo-openai-auth.js` and each agent's
footer script as it does in a jail.

### 2.3 What happens to an entry you added by hand

MEASURED at `b0460995` with a scratch test that seeded each real file with one hand-added entry,
then ran `RenderHostPack` under each contract:

| Hand-added entry in the real file | `assert` | `own`, first apply |
| :--- | :--- | :--- |
| A provider in `codex/config`'s `model_providers`, `pi/models`'s `providers` or `opencode/config`'s `provider` | **kept** | kept for codex and opencode; `pi/models` refused |
| An MCP server in `codex/config`, `opencode/config` or `pi/mcp` | **dropped**, and reported | kept for codex and opencode, **but reported as dropped**; `pi/mcp` refused |

The catalogs survive at the host for a reason that is easy to miss: the key-name probe's sentinel
provider has no address, so no catalog derive writes a row for it and none declares its catalog a
table. So at the host today a catalog is the user's, and an MCP table is yolo's.

## 3. Why the computed layer is jail-only — the stated reasons, and which hold

No maintainer ruling says the host computed layer is empty. Four statements say it, each in code
or in a design doc, and each gives a reason:

| Stated reason | Where it is stated | Ruling or statement | Does it hold? |
| :--- | :--- | :--- | :--- |
| "Its values embed jail-absolute paths" | `render.KindHost`'s comment ([`target.go`](../../internal/render/target.go)); the header of [`hostrender.go`](../../internal/entrypoint/hostrender.go); [host-render-target §3.3](host-render-target.md#33-what-each-target-supplies), "Jail-derived => host target gets none" | Statements, 2026-07-27 onward | **For one input.** The MCP presets' commands name the jail's generated node wrapper and the jail's npm prefix (`Env.mcpServersWith`, through `Env.McpWrappersBin` and `Env.NpmBin`). A user's own `mcp_servers` entry is whatever the user wrote, host-valid unless it spells a jail path. LSP commands are bare names such as `gopls`. Provider rows are URLs such as `https://api.cerebras.ai/v1`. No shipped `derive.lua` spells a jail path of its own (MEASURED: a search of every shipped derive for `/home/agent`, `/workspace`, `/opt/yolo`, `/run/yolo`, `/ctx/` and `HOME` finds none) |
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
2. **yolo's own remedy deletes the entry it names.** When a host apply would drop an MCP entry,
   its remedy says: declare it under `mcp_servers` in your user config, "one entry there reaches
   every agent" (`mcpEntryRemedy`, [`hostapplyremedy.go`](../../internal/cli/hostapplyremedy.go)).
   MEASURED by the research pass with a clean build: with `mcpServers.tavily` in
   `~/.pi/agent/mcp-adapter.json` **and** the same `mcp_servers.tavily` in the user config, the dry
   run still warned that the entry would be dropped, and `--assert` after `y` left
   `{"mcpServers": {}}`. The one remedy that works at the host today is a `config-overlay` naming
   each surface, for example in the local pack.
3. **`config-ref` states the wrong reason.** Its host-notch text says a provider has "no derive to
   feed" because "nothing in those files is a provider" (`config_ref.txt`). `pi/models`,
   `codex/config`, `opencode/config` and `oh-omp/models` carry provider rows, and
   `pi/codex-models` carries one provider's model list.
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
| **B1.** A registration declares its surface host-derivable | Host apply composes the inputs ([§6.2](#62-the-host-inputs)) and runs only the declared derives | A registration option; provider composition in host apply; host columns in `config ls` and `config render`; catalogs become yolo-owned tables at the host ([§6.4](#64-per-contract-behavior)) | **Recommended.** Fails closed for a pack nobody audited |
| **B2.** Per key, inside the derive, beside `ctx.in_full` | A derive marks which of its keys may cross | Every derive author reasons key by key; one more wrapper in the decoder; finer host `rmw` table semantics | Rejected for now: no shipped derive has a key that must stay behind once the inputs are host-valid. It can be added later without undoing B1 |
| **B3.** Every derive runs at the host, with a `ctx.notch` | Derives branch on the notch themselves | Reverses the "content does not cross" rule for every pack, fetched ones included; a derive that spells a jail path writes it into your real home | Rejected: it fails open, and it closes the "`DeriveCtx` carries no notch" gap ([notch-scoped §1](notch-scoped-config-contributions.md#1-the-case-and-why-no-channel-carries-it-today)) for a use this design does not need |
| **C.** A second channel for the codex list | For example, the extension asks `yolo internal …` at load | Fixes one surface; reopens the channel class [ML-D3](model-lists-and-pickers.md#ML-D3) rejected; pi's load then depends on a yolo binary | Rejected |

## 6. The proposed shape, under B1

### 6.1 Principles

- **P1. Same function, different inputs.** A host-derivable derive computes the same function at
  both notches. It never branches on the notch; one that needs to is not host-derivable.
- **P2. User scope only.** Every host input comes from your user config and your selected packs,
  never from a workspace ([env-manager OQ-2](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase)).
- **P3. Undeclared means today.** A surface whose registration does not declare it renders exactly
  as it does now.
- **P4. One writer.** Host apply is the only thing that writes a derived value into your real home.
  `yolo host --` composes the same inputs for its launch environment and writes no file.

### 6.2 The host inputs

| Input | Host value | Decided by |
| :--- | :--- | :--- |
| `providers` | `composedHostProviders`' table: no adapter address a pack's own service serves, and every `via` cleared, so `ctx.via_url` is `""` | [OQ-CS10](../reference/providers.md#oq-cs10); ES-D18 in [the credential-sources ledger](credential-sources-separation.md#10-decision-ledger); [WG-I12](wire-bridge-gateway.md#WG-I12) |
| `mcp_servers` | Your user-scope `mcp_servers` entries, less any entry whose command or arguments name a jail path, which is named in the report. The `mcp_presets` expansion never runs at the host, and each preset it skips is named too | [OQ-2](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) for the scope; for the presets, their commands exist only in a jail, and [`mcp-presets-removal.md`](mcp-presets-removal.md) retires them ([HC-D6](#HC-D6)) |
| `requires_env` on an MCP entry | Checked against the environment `yolo host env` would compose at apply time; each skipped server is named with its missing variables | [HC-D6](#HC-D6) |
| `lsp_servers` | Your user-scope entries as written, less any that names a jail path. A `command` must resolve on the host's `PATH`, as it must in a jail | [OQ-LSP1](../reference/mcp-configuration.md#oq-lsp1); [HC-D6](#HC-D6) |
| `use_profiles`, and the selection | Empty, unless [OQ-HC3](#OQ-HC3) rules otherwise | [OQ-HC3](#OQ-HC3) |
| `${workspace}` | Unbound; its branches are pruned by name, as today | [OQ-2](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) |

The composition runs **once per invocation** and every host-target reader of that invocation uses
it: `yolo host apply` (dry run and `--assert`), `yolo config ls --at host` and
`yolo config render --at host`. So a preview shows what the write would do.

### 6.3 Which surfaces declare it

Every shipped derived surface can, since none spells a jail path
([§3](#3-why-the-computed-layer-is-jail-only--the-stated-reasons-and-which-hold)), and the build
declares each one only after a test shows its host output carries no jail-absolute value. The
declaration exists for the next pack, not for these. `mise/config` is not a pack surface and stays
absent ([§9](#9-what-this-does-not-propose)).

### 6.4 Per-contract behavior

| Contract | A host-derivable surface | An undeclared one |
| :--- | :--- | :--- |
| `assert` (until retired) | `rmw` with the derive's output as the computed layer; a table declared in full is written wholesale, **so your `mcp_servers` entries are in it** and item 2 of [§4](#4-what-the-empty-layer-costs) becomes true advice | Today's behavior |
| `own` | Per [OQ-HC2](#OQ-HC2). Leaning: rendered through `stateful`, so the first owned render adopts the existing file | Today's behavior, with the refusal text reworded |
| `none` | Nothing is written | Nothing is written |

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
| An MCP entry's `requires_env` variable is missing | The entry is skipped and named, with the variable ([HC-D6](#HC-D6)) |
| Host yolo is older than the registration option | It runs no derive for content, so the option is inert and the host keeps today's behavior. An older entrypoint in a jail ignores it too |
| Config changes after an apply | The real file reflects the last apply until the next one, as declared layers do today |
| Two applies at once | Unchanged from today's host apply; this design adds no writer, and under [OQ-HC3](#OQ-HC3) one record beside the provenance record |

### 6.6 Forbidden behavior

- Never run an undeclared derive for content at the host.
- Never read a workspace config for a host input.
- Never write a jail-absolute value into your real home: a preset command, a path under
  `/workspace`, `/opt/yolo-jail`, `/run/yolo` or `/ctx`, or a jail home.
- Never compose an adapter address or a `via` route into a host input.
- Never select a variant host apply was not told about. It has no `-p`.

### 6.7 What done looks like

1. With `mcp_servers.tavily` in your user config, `yolo host apply --assert` writes it into every
   host-derivable MCP surface, and an existing identical entry is neither warned about nor dropped.
2. Host pi registers the declared `openai-codex` list, 1M variants included, and pi-ai's GPT-5.x
   ids are gone from its picker, as in a jail. [ML-D8](model-lists-and-pickers.md#ML-D8)'s
   behavior and its test are inverted in the same change.
3. `yolo config ls --at host` shows a `computed` layer for each host-derivable surface, and
   `yolo config render --at host` shows its content.
4. A test renders every declared surface at the host and finds no jail-absolute value, and it
   fails when a preset is let through.
5. `config-ref`'s host-notch text names the provider facts a host apply renders.

## 7. Fixes that do not wait for a ruling

These are defects in what ships, each with one right answer. None is built.

1. **[HC-D1](#HC-D1): `pi/models` always has `providers`.** The surface declares
   `"defaults": {"providers": {}}`, so neither a host apply into a home with no `models.json` nor a
   jail with no pi-reachable provider writes the `{}` pi rejects.
2. **[HC-D2](#HC-D2): the host MCP remedy names what reaches the host.** Until a host-derivable
   surface consumes `mcp_servers`, host apply's remedy names a `config-overlay` per surface, for
   example in the local pack, and stops promising that one `mcp_servers` entry reaches every agent.
3. **[HC-D3](#HC-D3): `config-ref`'s `provider` line says what is true**: host-rendered files carry
   provider facts, and host apply composes no provider table, so they render without them.
4. **[HC-D4](#HC-D4): a created file is a render.** A host apply that creates a file where none
   existed reports it as rendered, never as unchanged or already in sync.
5. **[HC-D5](#HC-D5): a loss line describes the write.** Under `own`, the report names an entry as
   dropped only when the `stateful` write drops it.

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
   writes `openai-codex`, which leaves a pi-only jail no row. [HC-D1](#HC-D1) is the fix.
2. **The report hides that write.** The dry run said `pi/models` was unchanged, and the assert
   counted it among the destinations already in sync, while the file was created. Only `--verbose`
   shows `pi/models rendered` ([HC-D4](#HC-D4)).
3. **A direct or IDE launch cannot refresh the `openai-codex` login.** The host-delivered
   `yolo-openai-auth.js`, loaded under node with no yolo environment and yolo on `PATH`, failed its
   refresh with "OpenAI credential service: openai-auth-client:
   YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT is not set". Only `yolo host --` sets the host socket
   variable (`openaiauthhost.prepare`), and the error names the jail's variable instead of saying
   to launch through `yolo host`.
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
| A host yolo older than `0965feeb` | the apply or launch refuses the pack's manifest by name |

What would settle it: the first screen pi printed; `yolo host apply --verbose`;
`cat ~/.pi/agent/models.json`; `yolo openai-auth status`; `yolo --version`; your
`host_management` value; and whether pi started through `yolo host`, a wrapper, an IDE or directly.
Until then no candidate gets a question here: the `models.json` one is fixed by [HC-D1](#HC-D1),
the login before exec belongs to [`openai-auth-broker.md`](openai-auth-broker.md), and the npm
install and the classifier belong to
[`notch-scoped-config-contributions.md`](notch-scoped-config-contributions.md).

## 9. What this does not propose

- **No host `mise/config`.** Host apply renders pack surfaces, and `mise_tools` at the host are
  yours ([`host-tool-provisioning.md`](host-tool-provisioning.md)).
- **No `ctx.notch`**, by P1.
- **No `-p` for host apply.** Only [OQ-HC3](#OQ-HC3)'s `use_profiles` could select anything.
- **No MCP presets at the host**, including re-rendered with host commands.
- **No change to any jail render**, beyond [HC-D1](#HC-D1)'s default.
- **No change to the OpenAI broker's host launch** ([§8.2](#82-read-from-source-not-run) item 1),
  until the debugging says whether it is what was hit.

## 10. Risks

| Risk | Mitigation |
| :--- | :--- |
| A pack declares host-derivable and its derive spells a jail path | [§6.7](#67-what-done-looks-like) item 4 covers shipped packs; for a fetched pack the declaration is the author's claim, and the host apply banner already names what each pack writes |
| A hand-added provider is dropped at the host | [OQ-CO16](config-ownership-and-promotion.md#oq-co16); [HC-D8](#HC-D8) asks first |
| Host pi's first `/login` selects no model, since `gpt-5.5` is no longer registered ([ML-D4](model-lists-and-pickers.md#ML-D4)) | pi says so and `/model` picks one; under [OQ-HC3](#OQ-HC3) `use_profiles` sets the default |
| A literal `api_key` in your `providers` config lands in a real agent file | It is your own value, and a jail already writes it to disk under the workspace's home overlay |

## 11. Open questions

1. 💬 <a id="OQ-HC1"></a>**[OQ-HC1](#OQ-HC1): Does the host notch run derives for content, and at
   what grain?** This is the question [OQ-ML3](model-lists-and-pickers.md#OQ-ML3)'s ruling left for
   a doc of its own: *"basically option 1, but then make sure there's a design doc about the host
   option left."* It decides whether host pi gets yolo's `openai-codex` list, whether any host agent
   gets your MCP, LSP and provider entries, and whether ML-D8 is inverted. Options, costed in
   [§5](#5-the-options): **A**, keep ML-D8; **B1**, per registration; **B2**, per key; **B3**, every
   derive with a notch.

   <!-- vantage: oq id=OQ-HC1 leaning="B1: a derive registration declares its surface host-derivable, and host apply runs only those derives over inputs composed at user scope (the ES-D18-stripped provider table, mcp_servers without presets, lsp_servers as written, no selection unless OQ-HC3). It fails closed for an unaudited pack, and A expires when the assert retirement is built." -->

   _Leaning:_ **B1.** The inputs, not the derives, were the obstacle, and B1 fixes the inputs while
   failing closed for a pack nobody audited. Its cost: a registration option; host apply composing
   the provider table; catalogs that become yolo's at the host, so [OQ-CO16](config-ownership-and-promotion.md#oq-co16)
   reaches the host; and host pi's picker losing pi-ai's GPT-5.x ids.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-HC2"></a>**[OQ-HC2](#OQ-HC2): Under `host_management: own`, does a `computed`
   surface render through `stateful`, or stay refused?** `render.HostOwnedModes` refuses it on
   [OQ-CO9](config-ownership-and-promotion.md#13-decision-ledger)'s reasoning, "until a real
   example argues otherwise", and that ruling itself covers only keyless surfaces. The real
   examples now exist: `pi/models`, `pi/codex-models`, `pi/mcp`, `copilot/mcp`, `copilot/lsp`,
   `agy/mcp` and `oh-omp/models`. This decides whether those files have any host
   path once `assert` is gone, under A as much as under B, because a pack overlay aimed at one of
   them is refused under `own` too. It is not an implementation choice, because
   [§4.5](config-ownership-and-promotion.md#45-retiring-assert--the-two-value-key)'s obligations
   say that refusal "need[s] new text, not deletion", which assumes it stays.

   <!-- vantage: oq id=OQ-HC2 leaning="Render through stateful. Capture-then-regenerate is exactly the adoption path the refusal says computed lacks, so the first owned render adopts the file instead of replacing it; and the retirement ruling's reason, one promote away from managed configs, cannot hold for a file own refuses." -->

   _Leaning:_ **Through `stateful`.** Capture-then-regenerate is the adoption path the refusal says
   is missing, so the first owned render adopts the file rather than replacing it; and the
   retirement's reason, *"a single promote away from having their configs managed correctly"*,
   cannot hold for a file `own` refuses. Its cost: those files are composed whole, with a capture
   baseline, under `own`. `oh-omp/models` also needs a yaml encoder for that path, which is
   UNMEASURED.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-HC3"></a>**[OQ-HC3](#OQ-HC3): Does host apply render the variant `use_profiles`
   selects?** It selects none today, by a code comment and no ruling. `yolo host --` does honor
   `use_profiles`, but only for the environment, so for pi, which reads no yolo environment, the
   setting does nothing at the host ([§8.1](#81-measured) item 4). It decides whether a direct or
   IDE launch of pi starts on your chosen provider and model. It matters only if
   [OQ-HC1](#OQ-HC1) is not A, since the selection keys come from derives.

   <!-- vantage: oq id=OQ-HC3 leaning="Yes, use_profiles only, never -p, with the jail's edge-triggered selection rule: yolo writes the selection when the chosen profile changes and clears only what it wrote (OQ-PSW2), so pi's own later /model choice stands." -->

   _Leaning:_ **Yes, `use_profiles` only**, with the jail's edge-triggered rule: yolo writes the
   selection when the chosen profile changes, and the per-key deselect rule
   ([OQ-PSW2](../reference/providers.md#oq-psw2)) clears only what it wrote, so your own later
   `/model` pick stands. Its cost: a per-home selection record beside the provenance record, and
   yolo writing a model choice into a real agent file. If the answer is no, host apply names
   `use_profiles` as not applied, so it stops doing nothing silently.

   **Answer:**
   > _(empty — fill in when decided)_

## 12. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-ML3 | *Inherited from [`model-lists-and-pickers.md`](model-lists-and-pickers.md#OQ-ML3).* **A, for now: `yolo host apply` renders no `openai-codex` list.** *"basically option 1, but then make sure there's a design doc about the host option left."* This doc is that design, and [OQ-HC1](#OQ-HC1) is the question it left | 2026-09-27 | [model-lists OQ-ML3](model-lists-and-pickers.md#OQ-ML3) | ✅ [ML-D8](model-lists-and-pickers.md#ML-D8) |
| <a id="HC-D1"></a>HC-D1 | *Implementation decision.* **`pi/models` declares `"defaults": {"providers": {}}`.** pi 0.87.1 rejects a `models.json` without `providers`, and both a host apply into a fresh home and a pi-only jail write `{}` today ([§8.1](#81-measured) item 1). With the default, both render `{"providers": {}}`, and the `-short` suites of `internal/entrypoint`, `internal/cli`, `internal/packload` and `packs` still pass (MEASURED by the research pass in a scratch copy). A derive's rows replace it, since the catalog is declared in full; under `rmw` a default fills only an absent key, so an existing host file keeps its own `providers`. A regression test asserts the rendered file has an object-valued `providers` at both notches | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | — |
| <a id="HC-D2"></a>HC-D2 | *Implementation decision.* **Host apply's MCP remedy names a `config-overlay` per surface,** for example in the local pack, because that is the only declaration that reaches a host MCP table today ([§4](#4-what-the-empty-layer-costs) item 2). Once a host-derivable surface consumes `mcp_servers`, the remedy names `mcp_servers` for that surface again | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | — |
| <a id="HC-D3"></a>HC-D3 | *Implementation decision.* **`config-ref`'s host-notch `provider` line says host-rendered files carry provider facts** and render without them because host apply composes no provider table; it changes again with [OQ-HC1](#OQ-HC1) | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | — |
| <a id="HC-D4"></a>HC-D4 | *Implementation decision.* **A host apply that creates a file reports it as rendered,** in the dry run and the assert, never as unchanged or in sync ([§8.1](#81-measured) item 2) | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | — |
| <a id="HC-D5"></a>HC-D5 | *Implementation decision.* **Under `own`, a loss line names only what the `stateful` write drops.** MEASURED: a first owned apply kept a hand-added `mcp_servers` entry in `codex/config` and `mcp` entry in `opencode/config` while the report named both as dropped ([§2.3](#23-what-happens-to-an-entry-you-added-by-hand)). The loss list is computed by the mechanism that writes. The same change makes `own`'s `codex/config` header stop saying "composed at jail start" at the host | 2026-09-27 | [§7](#7-fixes-that-do-not-wait-for-a-ruling) | — |
| <a id="HC-D6"></a>HC-D6 | *Implementation decision*, if [OQ-HC1](#OQ-HC1) is not A. **The host `mcp_servers` input is your user-scope entries, with no preset expansion, and `requires_env` is checked against what `yolo host env` would compose.** Presets name the jail's generated node wrapper and its npm prefix, which no host has, and [`mcp-presets-removal.md`](mcp-presets-removal.md) retires them. A user entry, MCP or LSP, whose command or arguments name a jail path is omitted and named, since writing it would break [§6.6](#66-forbidden-behavior)'s rule against jail-absolute values; which prefixes count is the implementer's list, and it includes the jail home, `/workspace`, `/opt/yolo-jail`, `/run/yolo` and `/ctx`. `yolo host env`'s composition is the environment yolo launches an agent with at the host; a skipped server is named with its variables, as the jail's notice names it | 2026-09-27 | [§6.2](#62-the-host-inputs) | — |
| <a id="HC-D7"></a>HC-D7 | *Implementation decision*, if [OQ-HC1](#OQ-HC1) is not A. **A derive error at the host refuses that surface only; a provider composition error refuses the apply before any write.** The first is one pack's defect, and the rest of the apply is independent of it. The second is an input every host-derivable surface shares, and `yolo host --` refuses on it too | 2026-09-27 | [§6.5](#65-degenerate-inputs-and-failure-paths) | — |
| <a id="HC-D8"></a>HC-D8 | *Implementation decision*, if [OQ-HC1](#OQ-HC1) is not A and catalogs stay declared in full ([OQ-CO16](config-ownership-and-promotion.md#oq-co16)). **The first host apply that makes a table yolo's in a home confirms before dropping an entry in it,** as a first apply does today (`confirmHostLosses`). Without it a home already asserted would lose a hand-added provider with a report and no prompt, because the prompt fires only on a first apply | 2026-09-27 | [§6.4](#64-per-contract-behavior) | — |
| <a id="HC-D9"></a>HC-D9 | *Implementation decision*, if [OQ-HC1](#OQ-HC1) rules B1. **The declaration is an option on the `yolo.derive` registration, not a manifest field.** The registration already is the computed-layer declaration (`packload.DerivedSurfaces`), and "its output is host-valid" is a fact about that function. Its spelling is the implementer's | 2026-09-27 | [§5](#5-the-options) | — |

## 13. Evidence

- **The inventory and the pi-only jail**, [§2.2](#22-the-inventory) and [§8.1](#81-measured) item 1:
  scratch tests in a clean `git archive` of `b0460995` under a temporary home, run by the research
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
  `rg` over `packs/*/derive.lua` at `b0460995`.
