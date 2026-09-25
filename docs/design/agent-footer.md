---
title: "Which provider is this session on? — yolo facts in every agent's footer"
date: 2026-09-25
status: accepted
tags: [footer, statusline, packs, providers, profiles, confinement, claude, pi, omp, agy, copilot, opencode, codex]
summary: "Six of the seven agents yolo ships can show extra text in their footer, through one of three hooks: a status-line command (claude, copilot, agy), a keyed status call in an extension (pi, omp), or a TUI plugin (opencode). Codex has no hook. One core renderer, `yolo internal footer`, prints two facts: what the session is billed through, in plain words, and where the agent runs (jail, guest or host). Each agent pack wires it into its agent's hook, on by default at the lowest layer so a user's own footer replaces it, and always beside the agent's stock status line, never over it. Bedrock cost and bridge failover state are a separate, later design."
---

# Which provider is this session on? — yolo facts in every agent's footer

**Status:** DESIGN, 2026-09-25. Every question is ruled: six in that day's first review, and the two they opened
([OQ-FT13](#OQ-FT13), [OQ-FT14](#OQ-FT14)) in the second. Nothing built. MEASURED: each agent's hook ([appendix](#appendix-evidence)) and this jail's env. UNMEASURED: that Claude's
status-line command runs in a jail and inherits its env, and anything about macos-user, which needs a Mac.

> **In short.** No agent's footer knows what yolo routed it to or where yolo put it, so both facts have to come from
> yolo. One core command renders them, and each agent pack adds them beside what its agent's footer already shows.

**Why it matters.** This jail runs Claude on Bedrock while yolo's own profile table says "no profile". Claude's
startup header names the billing once, and after that nothing on screen says it, or says whether this is a jail.

**The shape.** One core renderer plus one adapter per agent pack, through contribution kinds that already exist.

**Cost.** Claude and agy gain a line, pi and omp a status entry, copilot a footer item; codex gets nothing. Claude
hides most keyboard hints whenever any status line is set, and no key keeps them
([§2](#2-one-renderer-one-adapter-per-agent)).

**Nothing left to rule.** A macos-user session says `jail` ([OQ-FT13](#OQ-FT13)), and a Claude login reads
`Claude subscription`, without naming the plan ([OQ-FT14](#OQ-FT14)).

---

## The plain question

Can every agent's footer tell me what this session is billed through and whether I am in a jail, without taking
away anything it already shows? Bedrock cost is a later design ([Later](#later-cost-and-failover-a-separate-design)).

**Reads with:** [`providers.md`](../reference/providers.md#profiles-and-options) (what a provider and a profile are);
[`bedrock-plumbing.md`](bedrock-plumbing.md) (the everything profile, [OQ-BR11](bedrock-plumbing.md#OQ-BR11)) and
[`wire-bridge-gateway.md`](wire-bridge-gateway.md) (the bridge signer [OQ-BR10](wire-bridge-gateway.md#OQ-BR10) and
subscription failover [OQ-BR17](wire-bridge-gateway.md#OQ-BR17));
[`handoff-guest-notch-macos.md`](../plans/handoff-guest-notch-macos.md) (the three notches, and why `guest` is unbuilt).

## Settled questions

| Question | Ruling |
|---|---|
| [OQ-FT13](#OQ-FT13) What does a macos-user session's footer say, and what tells the renderer? | `jail`: macos-user sets the marker every container launch sets |
| [OQ-FT14](#OQ-FT14) Does the footer name your plan ("Claude Team"), or only the login? | Only the login: `Claude subscription` |

## Terms used throughout

- **Footer** *(coined here)*: the line or lines an agent draws under its input box (Claude's "status line"). The
  footer is the agent's; yolo only adds text to it. The **yolo segment** *(coined here)* is the text yolo adds.
- **Stock status line**: what a footer shows with no configuration: agy's built-in line, pi's footer, copilot's
  footer items. Claude has footer badges and keyboard hints, but no status line of its own.
- **Provider** and **profile**: as in [`providers.md`](../reference/providers.md#profiles-and-options). A provider
  is where requests go (`bedrock`); a profile is *"a named selection over one provider"*, and `use_profiles` picks
  one per agent. The jail carries them in `YOLO_PROFILES`, `YOLO_USE_PROFILES` and `YOLO_PROVIDERS`
  ([channels](../reference/providers.md#two-channels-split-by-payload-type)).
- **Billing route** *(coined here)*: what the session is billed through, in the footer's words: the agent's own
  login (for example a Claude subscription) or a provider (for example Bedrock). Not a region and not an upstream.
- **Notch**: one setting of yolo's `confinement` dial: `jail` (a container, the default), `guest` (a confined real
  home, not built) or `host` (your real machine, where `yolo host apply` writes your agents' config and
  `yolo host -- <agent>` launches one)
  ([the three notches](../plans/handoff-guest-notch-macos.md#1-what-the-three-notches-are-and-why-the-middle-one-matters)).
- **Adapter** *(coined here)*: the per-agent piece an agent pack ships to connect the renderer to that agent's hook,
  through a **contribution kind**: one of the fixed kinds of thing a manifest may add
  ([`pack-system.md`](../reference/pack-system.md)).
- **Layer**: an agent's settings file is composed from stacked layers, lowest first: the owning pack's defaults,
  your host file, the workspace file, other packs' overlays, computed values, the pack's managed values
  ([compose engine](../reference/pack-system.md#config-surfaces-and-the-compose-engine)). Objects merge per key.
- **Recomposed** and **edited-in-place** surfaces: most settings files are recomposed from the layers every boot.
  An edited-in-place one (the manifest's `rmw` mode) is not: yolo fills each missing key once, and from then on the
  file's contents count as the user's. Copilot's config file is one, and *every* file is one at the host.
- **Wire bridge**: yolo's in-jail daemon between an agent and its upstream; a **bridged** route goes through it
  ([`wire-bridge.md`](../reference/wire-bridge.md)).

## 1. What the footer shows

Two facts and nothing else ([OQ-FT4](#OQ-FT4)): the **billing route**, in plain words, and the **notch**; no region,
yolo version, backend, credential kind or jail name. One example per notch (the model name is Claude's own):

| Notch | Claude's footer | What it says |
|---|---|---|
| jail | `Opus · yolo: Bedrock (env) · jail` | This jail: Bedrock switched on outside yolo's profiles |
| host | `Opus · yolo: Claude subscription · host` | Your host Claude, with no profile selected in your user config |
| guest | `Opus · yolo: Bedrock · guest` | Only once the guest notch is built and says so; until then no footer prints `guest` |

Other shapes: with a `bedrock` profile selected, `yolo: Bedrock · jail`; under the everything profile,
`yolo: everything (bridge) · jail`; pi's status entry, `yolo: ChatGPT subscription (profile codex) · jail`.

### 1.1 The billing route

The footer names the route the agent will actually use, so when the agent's own switch disagrees with yolo's table
the switch wins (a table-only footer would call this Bedrock jail a subscription). The rule, in order:

1. **A profile is selected** (`YOLO_USE_PROFILES[<agent>]` names one): the route is that profile's provider, by
   the rule yolo already uses to pick it. If an agent switch (step 2) names a *different* provider, the segment
   appends `≠ <that provider> (env)`.
2. **No profile, but an agent switch is on.** A switch is an env var the agent pack names as the agent's own
   provider selector, for example `CLAUDE_CODE_USE_BEDROCK` → `bedrock`, tested the way the agent tests it: Claude
   counts it on only for `1`, `true`, `yes` or `on`. Switches are not secrets; a credential variable is only ever
   tested for presence. The first switch that is on wins, marked `(env)`: "set outside yolo's profiles".
3. **Neither:** the pack's words for the agent's built-in login, for example `Claude subscription`, never guessed.
   It never names the plan ([OQ-FT14](#OQ-FT14)).

**The plain words come from the pack.** No provider declaration carries a display name, so the pack's command
passes the words for its login and for each provider its profiles select (`bedrock` → `Bedrock`, `openai-codex` →
`ChatGPT subscription`). A provider with no words shows its id; a profile name that differs from its provider's
follows in parentheses.

**A bridged route shows the profile and `(bridge)`, never an upstream.** The everything profile
([OQ-BR11](bedrock-plumbing.md#OQ-BR11)) serves Anthropic ids untranslated, translates the rest, and may fail over
per model to Bedrock ([OQ-BR17](wire-bridge-gateway.md#OQ-BR17)), so no one provider name is true. The pack marks
which of its routes are bridged; yolo does not infer it.

### 1.2 The notch

The renderer reads the notch from what yolo already puts in the agent's environment:

| Notch | What tells the renderer | Evidence |
|---|---|---|
| jail | `YOLO_VERSION` is non-empty, asked through `config.InJail()`, which exists so there is *"one answer to 'am I in a jail?'"*. Every container launch sets it (`-e YOLO_VERSION=` in `commonEnvBlock`, `internal/cli/run/assemble.go`) | READ |
| host | No marker. Bare `claude` has none, and neither does `yolo host -- claude`: `composeHostVars` adds the pack env, `env_sources` and the provider derive's vars, and no `YOLO_*` table or `YOLO_VERSION` | READ |
| guest | Nothing yet, because the notch is unbuilt: `render.KindGuest` has no constructor and `config.ConfinementGuest` is *"Not yet enforced"*. Its launcher (environment-manager Phase 7) must set a marker the renderer can tell from a jail's | READ |

**Absence reads as `host`, deliberately:** a missing marker can only under-claim confinement, never claim a jail
that is not there. The renderer calls `config.InJail` and keeps no copy of the test; the copies already disagree
(`loopholes.inJail` counts an empty `YOLO_VERSION` as a jail).

> [!WARNING]
> **macos-user sets no marker today** (READ: nothing under `internal/macosuser` sets `YOLO_VERSION`, and the profile
> channel it takes instead of the container env block does not either). Its footer would say `host` inside a
> Seatbelt sandbox, though yolo renders that backend at the jail notch (`render.Jail`). UNVERIFIED on a Mac.
> [OQ-FT13](#OQ-FT13) ruled the fix: the macos-user launch sets `YOLO_VERSION`, after every other `config.InJail()`
> caller on that backend is checked.

Degenerate inputs:
- Absent or malformed `YOLO_*` JSON is treated as empty.
- A profile naming an unknown provider renders the profile name alone.
- The renderer never exits non-zero, never writes to stderr, and never prints a credential value.

## 2. One renderer, one adapter per agent

**Rendering lives in one hidden core subcommand, not in per-agent scripts.** A script goes blank when a guardrails
shim blocks a tool it uses, it needs `jq`, which `macos-user` does not bake, and seven scripts would be seven
readers of the profile table. The subcommand, `yolo internal footer` *(coined here; the name is the implementer's)*,
takes the agent's name, switches and plain words as arguments, so core keeps no agent list. It reads its own env
and, optionally, the agent's JSON on stdin, and prints one line from a template the pack passes, so core never
learns an agent's stdin schema. A template names yolo facts (`{yolo.billing}`, `{yolo.notch}`) and JSON paths into
stdin (`{stdin.model.display_name}`); a missing field renders empty, and a literal `{…}` never reaches the screen.
In a jail it reads no file and makes no network call; at the host it reads one ([OQ-FT6](#OQ-FT6)). It costs about
2–3 ms per run here (MEASURED).

**yolo adds; it never takes away** ([DIR-FT2](#DIR-FT2)). Wherever an agent has a stock status line, yolo's segment
goes beside it, and no adapter removes or replaces it:

| Agent | Hook | How yolo delivers it | The agent's own line |
|---|---|---|---|
| claude | `statusLine: {type: "command", command, …}` in `settings.json`; session JSON on stdin | The claude pack's settings defaults, with a command that runs the renderer. Claude skips it until workspace trust is accepted; the pack pre-accepts that for the jail workspace, so it bites only at the host | No stock line. The row sits above Claude's footer badges and keeps them, but Claude then hides most keyboard hints (`esc to interrupt`, `? for shortcuts`, `hold space to speak`). No key keeps them: the one loss this rule cannot prevent |
| agy | Claude-shaped `statusLine`, plus `stack_with_default`, in `~/.gemini/antigravity-cli/settings.json` | The agy pack's settings defaults, with `stack_with_default: true` ([OQ-FT12](#OQ-FT12)) | Any `statusLine` replaces agy's line unless that flag is true, so yolo sets it and agy's line stays |
| copilot | `statusLine` in `~/.copilot/config.json`, an edited-in-place file; `command` is a file path or a shell command | A pack-shipped script whose path goes into the config as a default; yolo rewrites the script every boot, so the value in your file never changes. No experimental flag is set or checked ([OQ-FT7](#OQ-FT7)) | Kept: in 1.0.88 the custom line is one footer item, `custom`, beside fourteen stock ones |
| pi | Extension API: `ctx.ui.setStatus(key, text)`, keyed and sorted | A file into `~/.pi/agent/extensions/`, the route `yolo-openai-auth.js` already takes; it calls `setStatus("yolo", …)` on `session_start` | Kept: status keys sit in pi's footer. The adapter never calls `setFooter`, which replaces it |
| omp | The same `setStatus` API; extensions from `~/.oh-omp/agent/extensions` | The same extension file, delivered by the omp pack | Kept, as in pi |
| opencode | TUI plugin slots from `tui.json` `plugin` (known only from the binary) | A plugin showing the notch; opencode's prompt row already shows the provider. Multi slots only, since a `single_winner` slot holds one plugin's content | Kept |
| codex | **None.** `[tui].status_line` takes only built-in item ids, with no custom text, no command and no provider item | Nothing until upstream [openai/codex#17827](https://github.com/openai/codex/issues/17827) lands | — |

pi's own footer prepends `(provider)` only when more than one provider is available and the line has room, and
that is pi's id, not yolo's words, so both can appear; pi also tracks in-session model switches yolo cannot see.
This needs **no new contribution kind and no new manifest field**: switches and plain words are arguments in the
pack's own command string or extension.

## 3. How a user's own footer survives

Layers merge objects **key by key**, not whole. So a lowest-layer yolo `statusLine` object has its keys merged with
a user's own, and any key yolo sets that the user's does not survives into their footer. Two rules follow:

- **yolo's default sets only `type` and `command`**, the two keys every user `statusLine` sets, so a user's value
  overwrites every key yolo wrote. No `refreshInterval` or `padding`: both facts are fixed per process. agy's
  `stack_with_default: true` is the one exception ([OQ-FT12](#OQ-FT12)). Merged per key, it also reaches a
  `statusLine` you write in agy, keeping agy's own line beside yours unless you set it `false`:
  [DIR-FT2](#DIR-FT2) applied to your footer too, visible and one key to undo.
- **A value yolo writes into an edited-in-place file must never need to change.** Copilot's config, and every file
  at the host, keep yolo's first fill as if the user had written it, so a later fix never reaches it. The value is a
  stable script path (copilot) or a command whose arguments do not change between releases, with the logic in the
  renderer; a provider added later has no words there and shows its id. Turned off, the script prints nothing.

Where yolo writes decides who wins:

| Where yolo writes | Your host `statusLine` | Your in-jail `/statusline` edit |
|---|---|---|
| **The owning pack's defaults (this design, [OQ-FT1](#OQ-FT1))** | you win | you win |
| Another pack's overlay | yolo wins | you win |
| The computed layer | yolo wins | yolo overwrites it on every boot |
| The pack's managed values | yolo wins | yolo wins |

Only the owning pack can write defaults, so the adapter lives in each agent pack, not a separate footer pack. To
keep your own footer *and* yolo's facts, call the renderer from your script ([OQ-FT5](#OQ-FT5)). A host
`statusLine` pointing at an unstaged host script may render nothing in the jail; that is your config, left alone.
agy reads no host settings file, so a host agy `statusLine` never reaches the jail.

## 4. Where the facts come from, and how fresh they are

| Fact | Source | Freshness |
|---|---|---|
| Billing route | `YOLO_USE_PROFILES`, `YOLO_PROFILES`, `YOLO_PROVIDERS` in the agent's own env; at the host, the `use_profiles` selection in your user config file ([OQ-FT6](#OQ-FT6)) | Fixed per process in a jail; read on each run at the host |
| Agent switch (for example `CLAUDE_CODE_USE_BEDROCK`) | The agent's own env, tested the agent's way | Fixed per process |
| Notch | `YOLO_VERSION` through `config.InJail()`; absent means host ([§1.2](#12-the-notch)) | Fixed per process |
| Not shown ([OQ-FT4](#OQ-FT4)) | Region (in `YOLO_PROVIDERS`), yolo version (`YOLO_VERSION`), backend (not in env: `YOLO_RUNTIME=podman` is the nested-launch selector), credential kind, jail name | — |
| Service health | The boot check runs once and writes one count line to `boot.log`; nothing is live | A connection attempt per refresh costs 3–4 ms; kept off the footer |
| Bridged upstream, failover | Only lines in the bridge's log today | **Not fixed per process**; needs a state file the bridge writes ([Later](#later-cost-and-failover-a-separate-design)) |

**At the host, "env first" finds nothing today:** no host launch exports the three tables, so the renderer reads
your user config, resolving profiles as `yolo host env` does (cost per run UNMEASURED). A one-launch
`yolo host -p bedrock -- claude` is not in that file, so its Bedrock switch shows as `(env)`.

> [!WARNING]
> **The renderer must read its own env, never `~/.config/yolo-user-env.sh`**, which every attach rewrites with the
> latest entry's selection. For a native route the process env is authoritative. **For a bridged route it is not:**
> the bridge keeps the first entry's route for the daemon's whole life, so an agent started from a later entry shows
> its own profile while the bridge serves the earlier one. Hence `(bridge)` and no upstream until the bridge
> publishes its state.

## Later: cost and failover (a separate design)

You asked for Bedrock cost in a future doc, so none of it is ruled here. Facts for that doc:

- No agent reports a billed Bedrock figure. Claude prices at Anthropic list, and guesses $5/$25 per million tokens
  for a bridged non-Anthropic model without saying so.
- The bridge publishes no failover state. Once [OQ-BR17](wire-bridge-gateway.md#OQ-BR17) is built, failover is per
  model and holds until that model's limit resets, and the everything route picks an upstream per request by id, so
  any state file is per model (upstream, and since when), not one "current upstream".
- A per-session meter needs a session key the bridge lacks (one daemon per jail; whether Claude's session header
  reaches a custom base URL is UNMEASURED). It also sits close to the gateway doc's refusal of budgets, which
  *"needs a price per model and a ledger per session"* ([§7](wire-bridge-gateway.md#7-what-the-bridge-still-refuses)).
- Every aws-auth mint shares one session name, `yolo-jail`, so AWS billing cannot tell sessions apart.

> [!WARNING]
> **One defect ships today whatever you decide.** On its streaming paths the wire bridge sends `message_start` with
> empty usage, keeps only output tokens, stops reading after the finish chunk, and never asks for
> `stream_options.include_usage`, corrupting Claude's cost and context numbers for every bridged model. It gets a
> failing test and a fix, and needs no ruling.

## What this does not do

- No footer for codex (no hook), no cost, region, yolo version or backend, and no bridged upstream.
- No service-health check on each refresh, and nothing that turns on or tests Copilot's experimental flag.
- It never removes an agent's stock status line or a footer you wrote, and no setting picks the fields: changing
  them means your own `statusLine` ([OQ-FT5](#OQ-FT5)).
- No new contribution kind, and no revival of the deleted Lua transform ([OQ-LT1](../reference/pack-system.md#oq-lt1)).

## Open Questions

Withdrawn on review 2026-09-25, hence the gaps: rendering's home and the table-versus-env rule became
[§2](#2-one-renderer-one-adapter-per-agent) and [§1.1](#11-the-billing-route), opencode's question had nothing to
decide, and the cost questions moved to the [later design](#later-cost-and-failover-a-separate-design). Rulings are
in the [Decision Ledger](#decision-ledger).

1. ✅ <a id="OQ-FT1"></a>[**OQ-FT1**](#OQ-FT1) (ruled 2026-09-25, as its leaning): **On by default, or opt-in?** On
   by default, in each agent pack's defaults ([§3](#3-how-a-users-own-footer-survives)).
2. ✅ <a id="OQ-FT4"></a>[**OQ-FT4**](#OQ-FT4) (ruled 2026-09-25, leaning overruled): **What else goes in?** The
   billing route in plain words and the notch; no region, version or backend ([§1](#1-what-the-footer-shows)).
3. ✅ <a id="OQ-FT5"></a>[**OQ-FT5**](#OQ-FT5) (ruled 2026-09-25, as its leaning): **How does a user keep their own
   footer and yolo's segment?** Their footer wins, and they call the renderer from it.
4. ✅ <a id="OQ-FT6"></a>[**OQ-FT6**](#OQ-FT6) (ruled 2026-09-25, as its leaning): **Does your host Claude get it?**
   Yes: env first, else your user config's profile selection ([§4](#4-where-the-facts-come-from-and-how-fresh-they-are)).
5. ✅ <a id="OQ-FT7"></a>[**OQ-FT7**](#OQ-FT7) (ruled 2026-09-25): **Does yolo turn on Copilot's experimental flag?**
   No, and it does not check it either: the status line is written like every other agent's.
6. ✅ <a id="OQ-FT12"></a>[**OQ-FT12**](#OQ-FT12) (ruled 2026-09-25): **In agy, stack yolo's line with agy's own,
   or replace it?** Stack. It was only ever about agy's built-in line; a footer you write yourself is
   [OQ-FT5](#OQ-FT5)'s, and just works. The rule behind the answer covers every agent: [DIR-FT2](#DIR-FT2).
7. ✅ <a id="OQ-FT13"></a>**OQ-FT13: What does a macos-user session's footer say, and what tells the renderer?**
   Today it would say `host` ([§1.2](#12-the-notch)). Options: (a) the macos-user launch sets `YOLO_VERSION` as the
   container launch does, so it says `jail`, the notch its config resolves to (`ResolveConfinement` defaults to
   `jail`) and the one yolo renders it at; that moves every other `config.InJail()` caller on that backend, so each
   is checked first. (b) A marker only the renderer reads: a second answer to "am I in a jail?". (c) `host` until
   the guest notch is built.

   <!-- vantage: oq id=OQ-FT13 leaning="(a) macos-user sets YOLO_VERSION like the container launch, after each other config.InJail caller on that backend is checked, so its footer says jail: one probe, one answer." -->

   _Leaning:_ (a). One probe keeps one answer, and `host` inside a sandbox is the one label that is plainly wrong.

   **Answer:** (a), ruled 2026-09-25 as leaned: *"macos-user sets YOLO_VERSION like the container launch, after
   each other config.InJail caller on that backend is checked, so its footer says jail: one probe, one answer."*

8. ✅ <a id="OQ-FT14"></a>**OQ-FT14: Does the footer name your plan, or only the login?** The login words come from
   the pack, which cannot know your plan, and Claude's status-line data has no account field. The plan sits in
   Claude's account cache, `~/.claude.json` `oauthAccount.organizationType` (`claude_team` here), and in its
   credential file, which yolo never reads. Options: (a) `Claude subscription`; (b) `Claude Team`, read from that
   cache on every run: a second exception to "reads no file" beside [OQ-FT6](#OQ-FT6)'s, from an undocumented file.

   <!-- vantage: oq id=OQ-FT14 leaning="(a) Claude subscription: it already tells your Team login from Bedrock, since a home holds one login, and naming the plan means reading Claude's undocumented account cache on every run." -->

   _Leaning:_ (a). A home holds one login, so `Claude subscription` already tells your Team login from Bedrock.

   **Answer:** (a), ruled 2026-09-25 as leaned: *"Claude subscription: it already tells your Team login from
   Bedrock, since a home holds one login, and naming the plan means reading Claude's undocumented account cache on
   every run."*

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| DIR-FT1 | **An easy way to put the provider in the claude footer, then other useful facts, in every agent possible; Bedrock cost in a future design.** *"I want to have an easy way to add the provider name to the claude code footer, and possibly some other useful things … it would be nice to be able to put bedrock costs there if possible, but this is certainly a future design doc … and we'd want this spread to all agents possible."* A direction, not a question | 2026-09-25 | [§1](#1-what-the-footer-shows), [§2](#2-one-renderer-one-adapter-per-agent), [Later](#later-cost-and-failover-a-separate-design) | — |
| [OQ-FT1](#OQ-FT1) | **On by default, at the lowest layer (a).** *"let's try it default on"*. A footer a user already has replaces it | 2026-09-25 | [§3](#3-how-a-users-own-footer-survives) | — |
| [OQ-FT4](#OQ-FT4) | **The billing route in plain words (your subscription login, e.g. Teams, versus Bedrock versus another provider) and the notch (jail, guest or host); no region, no yolo version, no backend.** *"I was thinking the exact opposite almost. why would I care about region? set it once. I care teams vs bedrock, don't want yolo version, but I do want to know if I'm in a jail/guest or on the host, don't care about backend."* The leaning (region only) was overruled. Naming the plan itself opened [OQ-FT14](#OQ-FT14), and macos-user's notch opened [OQ-FT13](#OQ-FT13) | 2026-09-25 | [§1](#1-what-the-footer-shows), [§1.2](#12-the-notch) | — |
| [OQ-FT5](#OQ-FT5) | **As its leaning:** *"(a) their footer wins and they embed the renderer in it; no engine feature and no wrapper key, since the only user so far has no footer."* | 2026-09-25 | [§3](#3-how-a-users-own-footer-survives) | — |
| [OQ-FT6](#OQ-FT6) | **As its leaning:** *"(a) render at the host: env first, else the profile selection from the user config file, as the one exception to reads-no-file; no new host-only switch."* | 2026-09-25 | [§2](#2-one-renderer-one-adapter-per-agent), [§4](#4-where-the-facts-come-from-and-how-fresh-they-are) | — |
| [OQ-FT7](#OQ-FT7) | **Set it, and do nothing special about Copilot's gate.** *"why do we care? set it, and it works or it doesn't."* yolo writes Copilot's `statusLine` like every other agent's and neither flips the experimental flag nor checks it. This is the leaning's "no flag", and it also retires the question's other half, whether the gate still exists: nothing depends on it | 2026-09-25 | [§2](#2-one-renderer-one-adapter-per-agent) | — |
| [OQ-FT12](#OQ-FT12) | **(b), stack with agy's own line:** the agy default sets `stack_with_default: true`. *"I don't want to get rid of the stock agent status lines wherever they exist. I want to add our own, not remove what's there. So I think it's your option B … but I'm not sure what it has to do with a user's own status lines, 'cause we decided that in a question up and yeah, it'll just work."* A user's own footer is [OQ-FT5](#OQ-FT5)'s | 2026-09-25 | [§2](#2-one-renderer-one-adapter-per-agent), [§3](#3-how-a-users-own-footer-survives) | — |
| <a id="DIR-FT2"></a>DIR-FT2 | **In every agent, yolo's segment is added beside the agent's stock status line wherever it has one, and never removes or replaces it.** Given with [OQ-FT12](#OQ-FT12): *"I want to add our own, not remove what's there."* Claude's hidden keyboard hints are the one loss no hook avoids | 2026-09-25 | [§2](#2-one-renderer-one-adapter-per-agent) | — |
| [OQ-FT13](#OQ-FT13) | **(a), as its leaning:** the macos-user launch sets `YOLO_VERSION` like the container launch, after each other `config.InJail()` caller on that backend is checked, so its footer says `jail`: one probe, one answer | 2026-09-25 | [§1.2](#12-the-notch) | — |
| [OQ-FT14](#OQ-FT14) | **(a), as its leaning:** a Claude login reads `Claude subscription`. A home holds one login, so that already tells a Team login from Bedrock; naming the plan would read Claude's undocumented account cache on every run | 2026-09-25 | [§1.1](#11-the-billing-route) | — |

## What I would build, in order

1. Separately, and first: the bridge streaming-usage fix in [Later](#later-cost-and-failover-a-separate-design).
2. The renderer, with tests for the rules in [§1.1](#11-the-billing-route) and [§1.2](#12-the-notch) (an empty
   `YOLO_VERSION` reads as host) and every degenerate input listed there.
3. The claude adapter, then agy's. Done: a fresh nested jail with a bedrock profile shows `yolo: Bedrock · jail`
   under Claude's input box; a workspace `statusLine` replaces it with none of yolo's keys left; agy shows its own
   line and yolo's together. A human confirms this, and with it the UNMEASURED claim in the status line.
4. The pi and omp extension files. Done: pi's footer as before, plus a `yolo` status entry.
5. Copilot's script file. Done: its `custom` footer item shows the segment, or it shows nothing and yolo lets it.
6. The host: `yolo host apply` fills Claude's command. Done: host Claude shows `· host` and your config's profile.
7. Last, opencode's plugin (its slot API is known only from binary strings), and macos-user's marker
   ([OQ-FT13](#OQ-FT13)): audit each `config.InJail()` caller reachable on that backend, then set `YOLO_VERSION`
   in its launch. Done: a macos-user Claude shows `· jail`, on a Mac.

## Appendix: evidence

**MEASURED**: I ran it or read it in an artifact. **READ**: a file or symbol in the tree. **SOURCED**: a URL and a
date.

| Claim | Evidence | Label |
|---|---|---|
| Installed versions; seven agent packs, six with a hook | `"kind": "program"` in claude, codex, pi, omp, opencode, copilot, agy (READ); claude 2.1.282, codex-cli 0.156.1, pi 0.87.1, opencode 1.18.32, agy 1.2.9; omp `@oh-labs/oh-omp` 0.15.3 fetched with `npm pack`; copilot 1.0.48 is a leftover in this jail (the copilot pack is not selected and `~/.yolo/bin/launch` has no copilot launcher), 1.0.88 fetched to `/tmp` | MEASURED |
| A copilot-pack jail runs current copilot | `packs/copilot/pack.json` names `@github/copilot` with no version; a launcher for an unpinned package refreshes it at most hourly (`~/.yolo/bin/launch/pi`: `PINNED=0`, `UPDATE_INTERVAL=3600`, `…@latest`) | READ, MEASURED |
| Claude's hook and payload | Strings of `~/.local/share/claude/versions/2.1.282`: the `statusLine` schema (`refreshInterval` min 1 s); the stdin builder with `model`, `workspace`, `cost{total_cost_usd,…}`, `context_window`, `rate_limits` and no provider or account field; "Skipping StatusLine command execution - workspace trust not accepted"; a 300 ms debounce | MEASURED |
| Claude's row keeps the badges, hides the hints | `code.claude.com/docs/en/statusline`, fetched 2026-09-24: *"renders in its own row above the built-in footer badges and does not replace them. With a custom status line configured, Claude Code stops showing most of the footer's keyboard hints"* | SOURCED |
| Claude's startup billing label and switch truthiness | Same strings: `ZR={bedrock:"Amazon Bedrock",vertex:"Google Vertex AI",…}` and `i!=="firstParty"?ZR[i]:…:"API Usage Billing"`, returned as `billingType`; `function Oe(e){…return["1","true","yes","on"].includes(n)}`, applied to `CLAUDE_CODE_USE_BEDROCK` | MEASURED |
| Where Claude keeps the plan | `~/.claude.json` `oauthAccount.organizationType` is `claude_team` in this jail; the credential file's `subscriptionType` was read by key name only | MEASURED |
| Workspace trust pre-accepted in the jail | `packs/claude/pack.json`: `hasTrustDialogAccepted: true` for the workspace | READ |
| No provider display name | `internal/packdecl/contributes.go` `ProviderContribution`: name, endpoints, key name, region, models, options, capabilities | READ |
| The notch probe; guest unbuilt; backend not in env | `internal/config/load.go` `inJail` (`YOLO_VERSION != ""`) and `InJail`; `internal/loopholes/loopholes.go` `inJail` (`os.LookupEnv`, so empty counts); `internal/cli/run/assemble.go` `commonEnvBlock` emits `-e YOLO_VERSION=`, and `YOLO_RUNTIME=podman` unconditionally; `internal/render/target.go` `KindGuest`: *"It has NO constructor yet"*; `internal/config/confinement.go` `ConfinementGuest`: *"Not yet enforced"* | READ |
| Host launches export no table | `internal/cli/host.go` `composeHostVars`: pack env fold, `env_sources`, `packload.AgentEnv`, removals; `YOLO_USE_PROFILES` is read there only for the jail half of `overlayGateProfiles` | READ |
| macos-user sets no marker | `rg YOLO_VERSION internal/macosuser` matches nothing; `packChannel.launchEnv` (`internal/cli/run/profilechannel.go`) sets pack env, provider shape vars and the three tables, and `commonEnvBlock`'s comment says the macos-user arm takes that channel instead of the container env block; `entrypoint.RunDarwinBootstrap` takes an `Env`, whose target is `render.Jail` (`internal/entrypoint/env.go`) | READ |
| Layers merge per key, in this order | `internal/agentcfg/engine.go` `mergeValue`: "Arrays and scalars replace wholesale", objects recurse per key; `internal/agentcfg/compose.go`: "Every layer folds through RFC 7386", in the order defaults, host, workspace, `config-overlay:<pack>`, capture overlay, computed, managed | READ |
| Edited-in-place defaults freeze | `packs/copilot/pack.json` config surface `"mode": "rmw"`; `internal/entrypoint/prism.go` `renderSurfaceRMWSurface`: "a value yolo wrote as a `defaults` fill on an earlier apply reads as the user's from the next apply on … no later default will ever displace it", and `yolo host apply` "is pure RMW" | READ |
| agy | agy binary changelog: "Added a `stack_with_default` flag to the `statusLine` configuration to render both the default Antigravity status line and custom status line output vertically stacked"; `packs/agy/pack.json` settings surface has no `readsHost`, while claude's and pi's do | MEASURED, READ |
| Codex has no hook | Strings of the 0.156.1 binary: a fixed `status_line` item list with no custom, command or provider item. openai/codex#17827 open, updated 2026-09-15 | MEASURED |
| pi | `dist/core/extensions/types.d.ts`: `setStatus` ("Set status text in the footer/status bar") and `setFooter` ("Set a custom footer component, or undefined to restore the built-in footer"); `footer.js`: `(provider)` only when `getAvailableProviderCount() > 1` and it fits. `packs/pi/pack.json` ships `extensions/yolo-openai-auth.js` | MEASURED, READ |
| omp | Binary strings: fixed segment ids, a model segment with no provider, extensions from `~/.oh-omp/agent/extensions` | MEASURED |
| opencode | Binary strings: `tui.json` `plugin`; `single_winner` and multi slots; the prompt row shows the provider. `opencode.ai/docs/tui`, fetched 2026-09-24 | MEASURED |
| copilot | 1.0.48 `app.js`: `STATUS_LINE:"experimental"`. 1.0.88 `copilot help config` (temp HOME): `statusLine` "displayed below the input", `command` "path to an executable script or a shell command". 1.0.88 `app.js`, which that run unpacked into the temp HOME's cache: no `STATUS_LINE` string; the footer has fifteen items, the last `custom` ("Custom configured statusLine.command") | MEASURED |
| This jail's env | `YOLO_USE_PROFILES={}` with `CLAUDE_CODE_USE_BEDROCK=1`; `~/.config/yolo-user-env.sh` exports it as `${CLAUDE_CODE_USE_BEDROCK:-'1'}` above the per-entry channel section, which `internal/cli/run/userenv.go` calls the env_sources defaults. `YOLO_PROVIDERS`: `openai-codex` carries an `anthropic` endpoint at `http://127.0.0.1:8215`, a bridged route | MEASURED, READ |
| The bridge keeps its first route | `internal/wirebridged/boot.go` `run`: "Once serving, its upstream remains fixed for the daemon lifetime"; `waitForActiveRoute` returns the first route that resolves | READ |
| One AWS session name | `internal/awsauth/awsauth.go`: `SessionName = "yolo-jail"` | READ |
| Renderer and connection cost | A `yolo` subcommand about 2–3 ms, a TCP dial 3–4 ms, over ten runs | MEASURED |
| Bridge usage defect | `internal/wirebridge/stream.go`: `anthropicUsage{}` in `message_start`, only `CompletionTokens` read, `if t.finished { return nil, nil }`; `responses.go` the same empty start; no `include_usage` request field in `internal/wirebridge` or `internal/wirebridged` | READ |
