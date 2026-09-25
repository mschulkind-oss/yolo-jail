---
title: "Which provider is this session on? — yolo facts in every agent's footer"
date: 2026-09-25
status: draft
tags: [footer, statusline, packs, providers, profiles, claude, pi, omp, agy, copilot, opencode, codex]
summary: "Six of the seven agents yolo ships can show extra text in their footer, through one of three hooks: a status-line command (claude, copilot, agy), a keyed status call in an extension (pi, omp), or a TUI plugin (opencode). Codex has no hook. The design is one core renderer, `yolo internal footer`, that reads yolo's own environment and a template the agent pack supplies, and each pack wires it into its agent's hook at the lowest layer, so a user's own footer replaces it. Provider and profile come first; Bedrock cost and bridge failover state are a separate, later design."
---

# Which provider is this session on? — yolo facts in every agent's footer

**Status:** DESIGN, 2026-09-25, revised after review the same day. Nothing built. MEASURED: each agent's hook
([appendix](#appendix-evidence)) and this jail's env. UNMEASURED: that Claude's status-line command runs in a jail
and inherits its env, since running an agent interactively is out of bounds here.

> **In short.** No agent's footer data names the provider yolo routed it to, so the provider has to come from yolo.
> One core command renders yolo's facts, and each agent pack plugs it into that agent's own footer hook, below any
> footer the user writes.

**Why it matters.** This jail runs Claude on Bedrock while yolo's own profile table says "no profile". Claude's
startup header names the billing once, but nothing on screen stays, and nothing is right for a bridged session.

**The shape.** One core renderer plus one adapter per agent pack, through contribution kinds that already exist.

**Cost.** Claude and agy launches with no footer of their own gain a line; pi and omp gain a status entry; copilot
only once its experimental gate is open. Codex gets nothing.

**Needs your ruling** ([table](#needs-your-ruling)): on by default ([OQ-FT1](#OQ-FT1)), what else it shows
([OQ-FT4](#OQ-FT4)), keeping your own footer ([OQ-FT5](#OQ-FT5)), your host Claude ([OQ-FT6](#OQ-FT6)), Copilot's
experimental flag ([OQ-FT7](#OQ-FT7)), agy's own line ([OQ-FT12](#OQ-FT12)).

---

## The plain question

When I look at an agent's footer, can it tell me which provider and profile this session is on, in every agent that
has a footer, without breaking a footer I already have? Bedrock cost is a separate, later design
([Later](#later-cost-and-failover-a-separate-design)).

**Reads with:** [`providers.md`](../reference/providers.md#profiles-and-options) (what a provider and a profile are);
[`bedrock-plumbing.md`](bedrock-plumbing.md) (the everything profile, [OQ-BR11](bedrock-plumbing.md#OQ-BR11)) and
[`wire-bridge-gateway.md`](wire-bridge-gateway.md) (the bridge signer [OQ-BR10](wire-bridge-gateway.md#OQ-BR10) and
subscription failover [OQ-BR17](wire-bridge-gateway.md#OQ-BR17)).

## Needs your ruling

| Question | Leaning |
|---|---|
| [OQ-FT1](#OQ-FT1) On by default? | Yes, at the lowest layer |
| [OQ-FT4](#OQ-FT4) Anything beyond provider and profile in the first cut? | Only the provider's region |
| [OQ-FT5](#OQ-FT5) How does a user keep their footer and yolo's segment? | Their footer wins; they call the renderer from it |
| [OQ-FT6](#OQ-FT6) Does your real host Claude get the footer too? | Yes, reading the profile selection from your user config |
| [OQ-FT7](#OQ-FT7) Does yolo turn on Copilot's experimental flag? | No |
| [OQ-FT12](#OQ-FT12) In agy, stack yolo's line under agy's own, or replace it? | Stack |

## Terms used throughout

- **Footer** *(coined here)*: the line or lines an agent draws under its input box. Claude calls it the status
  line. The footer is the agent's; yolo only adds text to it.
- **yolo segment** *(coined here)*: the text yolo puts in a footer. It is never the whole footer.
- **Provider** and **profile**: as in [`providers.md`](../reference/providers.md#profiles-and-options). A provider
  is where requests go (for example `bedrock`); a profile is *"a named selection over one provider"*, and
  `use_profiles` picks one per agent. The jail carries the configured profiles in `YOLO_PROFILES`, the per-agent
  selection in `YOLO_USE_PROFILES`, and per-provider settings in `YOLO_PROVIDERS`
  ([channels](../reference/providers.md#two-channels-split-by-payload-type)).
- **Adapter** *(coined here)*: the per-agent piece an agent pack ships to connect the renderer to that agent's hook.
- **Contribution kind**: one of the fixed kinds of thing a pack's manifest may add (a file, a settings value, a
  mount, …), listed in [`pack-system.md`](../reference/pack-system.md).
- **Layer**: an agent's settings file is composed from stacked layers, lowest first: the owning pack's defaults,
  your host file, the workspace file, other packs' overlays, computed values, then the pack's managed values
  ([compose engine](../reference/pack-system.md#config-surfaces-and-the-compose-engine)). A higher layer is merged
  over a lower one key by key: objects merge per key, and any other value replaces.
- **Recomposed** and **edited-in-place** surfaces: most settings files are recomposed from the layers on every boot.
  An edited-in-place one (the manifest's `rmw` mode) is not: yolo fills each missing key once, and from then on the
  file's contents count as the user's. Copilot's config file is one, and *every* file is one on the host.
- **Host notch**: rendering onto your real machine with `yolo host apply`, as opposed to inside a jail.
- **Wire bridge**: yolo's in-jail daemon that stands between an agent and its upstream
  ([`wire-bridge.md`](../reference/wire-bridge.md)). A **bridged** route is one it carries.

## 1. What the footer shows first

The yolo segment shows the **provider**, the provider's **region** when it has one, and the **profile** when its
name differs from the provider's. What that looks like:

- Claude in this jail: `Opus · yolo: bedrock (env)`. With a `bedrock` profile selected: `Opus · yolo: bedrock
  us-east-1`. The model name comes from Claude's own session data; the claude pack's template puts it there.
- Claude under the everything profile: `Opus · yolo: everything (bridge)`.
- pi: its own footer, plus a status entry `yolo: openai-codex (profile codex)`.

The footer names the provider the agent will actually use. When the agent's own switch disagrees with yolo's table,
the switch wins and is marked `(env)`; a table-only footer would call this Bedrock jail a subscription. The renderer
resolves the provider with this rule, in order:

1. **A profile is selected** (`YOLO_USE_PROFILES[<agent>]` names one): the provider is that profile's provider, by
   the rule yolo already uses to pick it. If an agent switch (step 2) names a *different* provider, the segment
   appends `≠ <that provider> (env)`.
2. **No profile, but an agent switch is on.** A switch is an env var the agent pack names as the agent's own
   provider selector, for example `CLAUDE_CODE_USE_BEDROCK` → `bedrock`. It is tested the way the agent tests it:
   Claude counts it on only for `1`, `true`, `yes` or `on`, so `=0` or an empty value is off. Switches are not
   secrets, so reading the value is safe; a credential variable is only ever tested for presence. The first switch
   that is on wins, and the segment is marked `(env)`, meaning "set outside yolo's profiles". This is this jail today.
3. **Neither:** the pack's name for the agent's built-in login, for example `subscription` for claude. yolo cannot
   verify that label, so it comes from the pack and is never guessed.

**A bridged route shows the profile and `(bridge)`, never an upstream.** Under the everything profile
([OQ-BR11](bedrock-plumbing.md#OQ-BR11)) one profile serves Anthropic ids untranslated, translates the rest, and
may fail over per model from the subscription to Bedrock ([OQ-BR17](wire-bridge-gateway.md#OQ-BR17)), so no single
provider name is true for the session. The pack marks which of its routes are bridged; yolo does not infer it.

Degenerate inputs:

- Absent or malformed `YOLO_*` JSON is treated as empty.
- A profile naming an unknown provider renders the profile name alone.
- The renderer never exits non-zero, never writes to stderr, and never prints a credential value.

## 2. One renderer, one adapter per agent

**Rendering lives in one hidden core subcommand, not in per-agent scripts.** A script goes blank when a guardrails
shim blocks a tool it uses, it needs `jq`, which the `macos-user` backend does not bake, and seven scripts would be
seven readers of the profile table. The subcommand, `yolo internal footer` *(coined here; the name is the
implementer's)*, takes the agent's name, switches and login label as arguments, so core still keeps no agent list.
It reads its own process env and, optionally, the agent's JSON on stdin, and prints one line from a template the
pack passes. Core never learns an agent's stdin schema. A template names yolo facts (`{yolo.provider}`) and plain
JSON paths into stdin (`{stdin.model.display_name}`). A missing field renders empty, and a literal `{…}` never
reaches the screen. In a jail the renderer reads no file and makes no network call (at the host, see
[OQ-FT6](#OQ-FT6)). A Go subcommand costs about 2–3 ms per run here (MEASURED).

| Agent | Hook | How yolo delivers it | Who wins |
|---|---|---|---|
| claude | `statusLine: {type: "command", command, …}` in `settings.json`; session JSON on stdin | The claude pack's settings defaults, with a command that runs the renderer. Claude skips the command until workspace trust is accepted; the claude pack already pre-accepts it for the jail workspace, so this bites only at the host ([OQ-FT6](#OQ-FT6)) | Any `statusLine` from the host, the workspace or an in-jail edit |
| agy | Claude-shaped `statusLine`, plus `stack_with_default` to keep agy's own line, in `~/.gemini/antigravity-cli/settings.json` | The agy pack's settings defaults ([OQ-FT12](#OQ-FT12)) | A workspace or in-jail `statusLine`. agy's settings read no host file, so a host `statusLine` never reaches the jail |
| copilot | `statusLine` in `~/.copilot/config.json`, an edited-in-place file. In current copilot (1.0.88) `command` may be a file path or a shell command, with `refreshInterval`; the status line was experimental-gated in 1.0.48 ([OQ-FT7](#OQ-FT7)) | A pack-shipped script file whose path goes into the config as a default. The file is yolo's and is rewritten every boot, so the value in the user's file never needs to change | Any `statusLine` the user sets |
| pi | Extension API: `ctx.ui.setStatus(key, text)`, keyed and sorted | A file into `~/.pi/agent/extensions/`, the route `yolo-openai-auth.js` already takes; it calls `setStatus("yolo", …)` on `session_start` | Nobody needs to: keys coexist |
| omp | The same `setStatus` API; extensions auto-discovered from `~/.oh-omp/agent/extensions` | The same extension file, delivered by the omp pack | Keys coexist |
| opencode | TUI plugin slots from `tui.json` `plugin` (known only from the binary) | Nothing needed for the provider: opencode's prompt row already shows it. A yolo segment through a plugin slot can come when a profile name is wanted | — |
| codex | **None.** `[tui].status_line` takes only built-in item ids, with no custom text, no command and no provider item | Nothing yolo can do until upstream [openai/codex#17827](https://github.com/openai/codex/issues/17827) lands | — |

pi and omp show both provider and profile. pi's own footer prepends `(provider)` only when more than one provider
is available and the line has room, and that is pi's provider id, not yolo's, so the two can both appear. pi also
tracks in-session model switches that yolo's launch-time fact cannot see.

This needs **no new contribution kind and no new manifest field**. Switches and the login label are arguments in
the pack's own command string or extension.

## 3. How a user's own footer survives

Layers merge objects **key by key**, not whole. So a lowest-layer yolo `statusLine` object has its keys merged with
a user's own, and any key yolo sets that the user's does not survives into their footer. Two rules follow:

- **yolo's default sets only `type` and `command`**, the two keys every user `statusLine` sets. A user's value then
  overwrites every key yolo wrote. No `refreshInterval` or `padding`: the first-cut facts are fixed per process,
  and Claude reruns the command on each assistant message anyway. agy's `stack_with_default` is the one exception
  worth weighing ([OQ-FT12](#OQ-FT12)).
- **A value yolo writes into an edited-in-place file must never need to change.** Copilot's config, and every file
  at the host notch, keep yolo's first fill as if the user had written it: a later fix never reaches it, and it
  stays if the feature is turned off. So the value is a stable script path (copilot) or a command whose arguments do
  not change between releases, with the logic in the renderer. Turned off, the script prints nothing rather than
  going missing.

Where yolo writes decides who wins:

| Where yolo writes | Your host `statusLine` | Your in-jail `/statusline` edit |
|---|---|---|
| **The owning pack's defaults (leaning)** | you win | you win |
| Another pack's overlay | yolo wins | you win |
| The computed layer | yolo wins | yolo overwrites it on every boot |
| The pack's managed values | yolo wins | yolo wins |

Only the owning pack can write defaults, so the adapter lives in each agent pack and not in a separate footer pack.
A host `statusLine` often points at a host script that is not staged into the jail, so in the jail it may render
nothing. That is your config, and yolo leaves it alone.

## 4. Where the facts come from, and how fresh they are

| Fact | Source | Freshness |
|---|---|---|
| Provider, profile, region | `YOLO_USE_PROFILES`, `YOLO_PROFILES`, `YOLO_PROVIDERS` in the agent's own env | Fixed for the life of the agent process |
| Agent switch (for example `CLAUDE_CODE_USE_BEDROCK`) | The agent's own env, tested the agent's way | Fixed per process |
| Credential kind (static key, SSO adapter, broker) | Presence of `api_key_env_name`'s var, `AWS_CONTAINER_CREDENTIALS_FULL_URI`, `AWS_ACCESS_KEY_ID` | Fixed per process; not in the first cut ([OQ-FT4](#OQ-FT4)) |
| yolo version | `YOLO_VERSION` | Fixed |
| Jail name, backend | **Not in env.** `YOLO_RUNTIME=podman` is the nested-launch selector, not the outer backend | Needs the launcher to export them |
| Service health | The boot check runs once and writes one count line to `boot.log`; nothing is live | A connection attempt per refresh costs 3–4 ms; kept off the footer |
| Bridged upstream, failover | Only lines in the bridge's log today | **Not fixed per process**; needs a state file the bridge writes ([Later](#later-cost-and-failover-a-separate-design)) |

> [!WARNING]
> **The renderer must read its own env, never `~/.config/yolo-user-env.sh`.** Every attach rewrites that file with
> the latest entry's selection, so it can describe a different entry than the agent process drawing the footer. For
> a native route the process env is authoritative. **For a bridged route it is not:** the bridge keeps the route of
> the first entry that resolved one for the daemon's whole life, so an agent started from a later entry with a
> different selection shows its own profile while the bridge serves the earlier one. That is why a bridged route
> shows `(bridge)` and no upstream until the bridge publishes its state.

## Later: cost and failover (a separate design)

You asked for Bedrock cost in a future doc, so none of it is ruled here. Facts for that doc:

- No agent reports a billed Bedrock figure. Claude prices at Anthropic list, and guesses $5/$25 per million tokens
  for a bridged non-Anthropic model without saying so.
- The bridge publishes no failover state. Once [OQ-BR17](wire-bridge-gateway.md#OQ-BR17) is built, failover is per
  model and holds until that model's limit resets, and the everything route picks an upstream per request by id, so
  any state file is per model (upstream, and since when), not one "current upstream".
- A per-session meter needs a session key the bridge lacks: it is one daemon per jail, and whether Claude's session
  header reaches a custom base URL is UNMEASURED. A display meter also sits close to the gateway doc's refusal of
  budgets, which *"needs a price per model and a ledger per session"*
  ([§7](wire-bridge-gateway.md#7-what-the-bridge-still-refuses)); that doc's owner rules where the line falls.
- Every aws-auth mint shares one session name, `yolo-jail`, so AWS billing cannot tell sessions apart.

> [!WARNING]
> **One defect ships today whatever you decide.** On its streaming paths the wire bridge sends `message_start` with
> empty usage, keeps only output tokens, stops reading after the finish chunk (dropping a trailing usage-only chunk),
> and never asks for `stream_options.include_usage`. That corrupts Claude's own cost and context numbers for every
> bridged model now. It gets a failing test and a fix, and needs no ruling.

## What this does not do

- It does not give codex a footer. There is no hook.
- It does not show a cost. That is the later design.
- It does not name the upstream a bridged session is on.
- It does not check service health on every refresh.
- No setting picks the fields; changing them means your own `statusLine` ([OQ-FT5](#OQ-FT5)).
- It adds no contribution kind, and it does not revive the deleted Lua transform
  ([OQ-LT1](../reference/pack-system.md#oq-lt1)) just to chain two commands.
- It never replaces a footer you wrote.

## Open Questions

Withdrawn on review, 2026-09-25, so the numbering has gaps: where rendering lives and what shows when yolo's table
and the agent's env disagree became design statements in [§2](#2-one-renderer-one-adapter-per-agent) and
[§1](#1-what-the-footer-shows-first); opencode has nothing to decide, since it already shows the provider; and the
three cost questions move to the [later design](#later-cost-and-failover-a-separate-design).

1. 💬 <a id="OQ-FT1"></a>**OQ-FT1: On by default, or opt-in?** This decides whether every claude and agy launch with
   no footer of its own gains a line. Options: (a) on by default, in the pack's defaults; (b) off until a config key
   or pack opts in.

   <!-- vantage: oq id=OQ-FT1 leaning="(a) on by default, in the agent pack's defaults: the lowest layer, so a footer a user already has replaces it and nothing of theirs changes. Pick (b) if no agent's footer should change until asked." -->

   _Leaning:_ (a). You said "an easy way to add", which fits either answer. On by default is easier still, and at the
   lowest layer it changes nothing for anyone who already has a footer. Pick (b) if you would rather no agent's
   footer change until you ask.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-FT4"></a>**OQ-FT4: What else goes in the first cut?** Candidates: region, credential kind, yolo
   version, jail name, backend (the last two need new launcher env vars).

   <!-- vantage: oq id=OQ-FT4 leaning="Only the provider's region: it is the one extra fact that changes the bill. Everything else waits until someone asks for it, and jail name and backend need launcher env vars first." -->

   _Leaning:_ region only. It is the one extra fact that changes the bill, and a footer is short.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-FT5"></a>**OQ-FT5: How does a user keep their own footer and yolo's segment?** Options: (a) their
   footer wins, and they call `yolo internal footer` from their own script (documented as a one-liner); (b) a yolo
   config key naming the user's command, which yolo's wrapper runs first; (c) an engine feature that chains a
   command from a lower layer into a higher one.

   <!-- vantage: oq id=OQ-FT5 leaning="(a) their footer wins and they embed the renderer in it; no engine feature and no wrapper key, since the only user so far has no footer." -->

   _Leaning:_ (a). (c) is the deleted Lua transform again, and your settings have no footer today.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-FT6"></a>**OQ-FT6: Does your real host Claude get the footer too?** `yolo host apply` writes your
   host `~/.claude/settings.json`, where no `YOLO_*` variables exist, and edits it in place, so yolo's value is a
   one-time fill ([§3](#3-how-a-users-own-footer-survives)). Claude also skips the command until you accept
   workspace trust in that directory. Options: (a) yes: the renderer uses `YOLO_*` from its env when a launch put
   them there, and otherwise reads the profile selection from your user config file, the one exception to "reads
   no file"; (b) jail-only, which needs a new switch keeping one setting off the host (today only the autonomy
   settings have one).

   <!-- vantage: oq id=OQ-FT6 leaning="(a) render at the host: env first, else the profile selection from the user config file, as the one exception to reads-no-file; no new host-only switch." -->

   _Leaning:_ (a). One mechanism, and no new switch.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 <a id="OQ-FT7"></a>**OQ-FT7: Is Copilot's status line still experimental, and if so, does yolo turn the flag
   on?** In 1.0.48 it was gated behind `experimental`. In 1.0.88, the version a copilot-pack jail gets, `copilot
   help config` documents `statusLine` with no mention of the gate; whether the gate is actually gone is untested.

   <!-- vantage: oq id=OQ-FT7 leaning="No flag: experimental changes far more than the footer. Write the statusLine and let it appear when Copilot's gate lifts or the user opts in." -->

   _Leaning:_ no flag. Experimental changes far more than the footer. If the gate is gone, this question closes.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 <a id="OQ-FT12"></a>**OQ-FT12: In agy, stack yolo's line under agy's own, or replace it?** Setting any
   `statusLine` replaces agy's built-in line unless `stack_with_default` is true. Options: (a) yolo sets only
   `type` and `command`, so agy users with no footer of their own lose agy's line; (b) yolo also sets
   `stack_with_default: true`, which keeps agy's line but, since layers merge per key, also stacks it over a
   `statusLine` you write unless you set that flag `false`.

   <!-- vantage: oq id=OQ-FT12 leaning="(b) stack: keeping agy's own line for everyone with no footer beats a surprise that costs one key to undo for someone with one." -->

   _Leaning:_ (b). Losing agy's line to gain a provider label is the worse trade, and the surprise in (b) is
   visible and one key to undo.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| DIR-FT1 | **An easy way to put the provider in the claude footer, then other useful facts, in every agent possible; Bedrock cost in a future design.** *"I want to have an easy way to add the provider name to the claude code footer, and possibly some other useful things … it would be nice to be able to put bedrock costs there if possible, but this is certainly a future design doc … and we'd want this spread to all agents possible."* A direction, not a question | 2026-09-25 | [§1](#1-what-the-footer-shows-first), [§2](#2-one-renderer-one-adapter-per-agent), [Later](#later-cost-and-failover-a-separate-design) | — |

## What I would build, in order

1. Separately, and first: the bridge streaming-usage fix in [Later](#later-cost-and-failover-a-separate-design).
2. The renderer, with tests for the resolution rule in [§1](#1-what-the-footer-shows-first) and every degenerate
   input listed there.
3. The claude adapter, then agy's. Done looks like this: a fresh nested jail with a bedrock profile shows
   `yolo: bedrock us-east-1` under Claude's input box, and a workspace `statusLine` replaces it with none of yolo's
   keys left in it. A human confirms that step, and with it the UNMEASURED claim in the status line.
4. The pi and omp extension files.
5. Copilot's script file, once [OQ-FT7](#OQ-FT7) is ruled.

## Appendix: evidence

**MEASURED**: I ran it or read it in an artifact. **READ**: a file or symbol in the tree. **SOURCED**: a URL and a
date.

| Claim | Evidence | Label |
|---|---|---|
| Installed versions | claude 2.1.282, codex-cli 0.156.1, pi 0.87.1, opencode 1.18.32, agy 1.2.9; omp `@oh-labs/oh-omp` 0.15.3 fetched with `npm pack`; copilot 1.0.48 is a leftover in this jail (the copilot pack is not selected and `~/.yolo/bin/launch` has no copilot launcher), 1.0.88 fetched to `/tmp` | MEASURED |
| A copilot-pack jail runs current copilot | `packs/copilot/pack.json` names `@github/copilot` with no version; a launcher for an unpinned package refreshes it at most hourly (`~/.yolo/bin/launch/pi`: `PINNED=0`, `UPDATE_INTERVAL=3600`, `…@latest`) | READ, MEASURED |
| Claude's hook and payload | Strings of `~/.local/share/claude/versions/2.1.282`: the `statusLine` schema (`refreshInterval` min 1 s); the stdin builder with `model`, `workspace`, `cost{total_cost_usd,…}`, `context_window`, `rate_limits` and no provider field; "Skipping StatusLine command execution - workspace trust not accepted"; a 300 ms debounce. Vendor docs `code.claude.com/docs/en/statusline`, fetched 2026-09-24 | MEASURED |
| Claude's startup billing label | Same strings: `ZR={bedrock:"Amazon Bedrock",vertex:"Google Vertex AI",…}` and `i!=="firstParty"?ZR[i]:…:"API Usage Billing"`, returned as `billingType` | MEASURED |
| Claude's switch truthiness | Same strings: `function Oe(e){…return["1","true","yes","on"].includes(n)}`, applied to `CLAUDE_CODE_USE_BEDROCK` | MEASURED |
| Workspace trust pre-accepted in the jail | `packs/claude/pack.json`: `hasTrustDialogAccepted: true` for the workspace | READ |
| Layers merge per key | `internal/agentcfg/engine.go` `mergeValue`: "Arrays and scalars replace wholesale", objects recurse per key; `internal/agentcfg/compose.go`: "Every layer folds through RFC 7386" | READ |
| Layer order | `internal/agentcfg/compose.go`: defaults, host, workspace, `config-overlay:<pack>`, capture overlay, computed, managed | READ |
| Edited-in-place defaults freeze | `packs/copilot/pack.json` config surface `"mode": "rmw"`; `internal/entrypoint/prism.go` `renderSurfaceRMWSurface`: "a value yolo wrote as a `defaults` fill on an earlier apply reads as the user's from the next apply on … no later default will ever displace it", and `yolo host apply` "is pure RMW" | READ |
| agy reads no host settings | `packs/agy/pack.json` settings surface has no `readsHost`; `packs/claude/pack.json` and `packs/pi/pack.json` do | READ |
| agy's stacking flag | agy binary changelog: "Added a `stack_with_default` flag to the `statusLine` configuration to render both the default Antigravity status line and custom status line output vertically stacked"; also "Added a `cost` field" | MEASURED |
| Codex has no hook | Strings of the 0.156.1 binary: a fixed `status_line` item list with no custom, command or provider item. openai/codex#17827 open, updated 2026-09-15 | MEASURED |
| pi | `dist/core/extensions/types.d.ts` (`setStatus`, `setFooter`); `dist/modes/interactive/components/footer.js`: `(provider)` only when `getAvailableProviderCount() > 1` and it fits the width. `packs/pi/pack.json` ships `extensions/yolo-openai-auth.js` into `.pi/agent/extensions/` | MEASURED, READ |
| omp | Binary strings: fixed segment ids, a model segment with no provider, extensions from `~/.oh-omp/agent/extensions` | MEASURED |
| opencode | Binary strings: `tui.json` `plugin`; `single_winner` and multi slots; the prompt row shows the provider. `opencode.ai/docs/tui`, fetched 2026-09-24 | MEASURED |
| copilot | 1.0.48 `app.js`: `STATUS_LINE:"experimental"`, an existing file spawned with a 10 s timeout, no USD or provider field. 1.0.88 `copilot help config` (temp HOME): `command` is "path to an executable script or a shell command", "Plain commands are run with the platform shell (`/bin/sh` …)", `refreshInterval`; its application code is not readable from the binary's strings, so the gate is untested | MEASURED |
| Six agent packs with a hook, seven in all | `"kind": "program"` in claude, codex, pi, omp, opencode, copilot, agy | READ |
| This jail's provider disagreement | `YOLO_USE_PROFILES={}` with `CLAUDE_CODE_USE_BEDROCK=1`; `~/.config/yolo-user-env.sh` exports it as `${CLAUDE_CODE_USE_BEDROCK:-'1'}` above the per-entry channel section, which `internal/cli/run/userenv.go` calls the env_sources defaults | MEASURED, READ |
| A bridged endpoint is visible in the env | `YOLO_PROVIDERS` here: `openai-codex` carries an `anthropic` endpoint at `http://127.0.0.1:8215` | MEASURED |
| The bridge keeps its first route | `internal/wirebridged/boot.go` `run`: "Once serving, its upstream remains fixed for the daemon lifetime"; `waitForActiveRoute` returns the first route that resolves | READ |
| pi has a bridge route | [OQ-BR5](bedrock-plumbing.md#OQ-BR5), ruled 2026-09-25: native Converse and a bridge version; [`wire-bridge-gateway.md`](wire-bridge-gateway.md#decision-ledger) DIR-WG1 offers the bridge to every agent | READ |
| Backend not in env | `internal/cli/run/assemble.go` emits `YOLO_RUNTIME=podman` unconditionally | READ |
| One AWS session name | `internal/awsauth/awsauth.go`: `SessionName = "yolo-jail"` | READ |
| Renderer and connection cost | A `yolo` subcommand about 2–3 ms, a TCP dial 3–4 ms, over ten runs | MEASURED |
| Bridge usage defect | `internal/wirebridge/stream.go`: `anthropicUsage{}` in `message_start`, only `CompletionTokens` read, `if t.finished { return nil, nil }`; `responses.go` the same empty start; no `include_usage` request field in `internal/wirebridge` or `internal/wirebridged` | READ |
