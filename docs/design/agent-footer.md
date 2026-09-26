---
title: "Which provider is this session on? — yolo facts in every agent's footer"
date: 2026-09-25
status: in-review
tags: [footer, statusline, packs, providers, profiles, confinement, claude, pi, omp, agy, copilot, opencode, codex]
summary: "Six of the seven agents yolo ships can show extra text in their footer, through one of three hooks: a status-line command (claude, copilot, agy), a keyed status call in an extension (pi, omp), or a TUI plugin (opencode). Codex has no hook. One core renderer, `yolo internal footer`, prints two facts: what the session is billed through, in plain words, and where the agent runs (jail, guest or host). Each agent pack wires it into its agent's hook, on by default at the lowest layer so a user's own footer replaces it, and always beside the agent's stock status line, never over it. Bedrock cost and bridge failover state are a separate, later design."
---

# Which provider is this session on? — yolo facts in every agent's footer

**Status:** BUILT, 2026-09-25. Every question but one is ruled: six in that day's first review, and the two they
opened ([OQ-FT13](#OQ-FT13), [OQ-FT14](#OQ-FT14)) in the second. One is open: [OQ-FT15](#OQ-FT15), how a one-launch
`yolo host -p` reaches the host footer. All seven build items are built: the bridge's
streamed-usage fix, the renderer, an adapter for each of the six agents with a hook, the host's profile read, and
macos-user's jail marker ([as built](#21-as-built); [what I would build](#what-i-would-build-in-order)). Not yet
seen under a live agent: a human checks each footer. MEASURED: each agent's hook ([appendix](#appendix-evidence)),
this jail's env and the renderer's cost at both notches. UNMEASURED: that Claude's status-line command runs in a jail
and inherits its env, that opencode draws the plugin's text from `tui.jsonc`, and anything about macos-user, which
needs a Mac. Review moved opencode's plugin list out of `tui.json` and made the host profile read write nothing
([§2.1](#21-as-built)).

> **In short.** No agent's footer knows what yolo routed it to or where yolo put it, so both facts have to come from
> yolo. One core command renders them, and each agent pack adds them beside what its agent's footer already shows.

**Why it matters.** This jail runs Claude on Bedrock while yolo's own profile table says "no profile". Claude's
startup header names the billing once, and after that nothing on screen says it, or says whether this is a jail.

**The shape.** One core renderer plus one adapter per agent pack, through contribution kinds that already exist.

**Cost.** Claude and agy gain a line, pi and omp a status entry, copilot a footer item, opencode two words in its
prompt row; codex gets nothing. Claude
hides most keyboard hints whenever any status line is set, and no key keeps them
([§2](#2-one-renderer-one-adapter-per-agent)).

**Needs your ruling:** [OQ-FT15](#OQ-FT15), how a one-launch `yolo host -p` reaches the host footer. Already
ruled: a macos-user session says `jail` ([OQ-FT13](#OQ-FT13)), and a Claude login reads `Claude subscription`,
without naming the plan ([OQ-FT14](#OQ-FT14)).

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

> [!NOTE]
> **macos-user set no marker until 2026-09-25**, so its footer would have said `host` inside a Seatbelt sandbox,
> though yolo renders that backend at the jail notch (`render.Jail`). [OQ-FT13](#OQ-FT13) ruled the fix and it is
> built: the macos-user launch sets `YOLO_VERSION` to the launcher's version, last, in the session env file. What
> else that moves on this backend is audited in [§2.2](#22-what-macos-users-marker-moves). UNVERIFIED on a Mac.

Degenerate inputs:
- Absent or malformed `YOLO_*` JSON is treated as empty.
- A profile naming an unknown provider renders the profile name alone.
- The renderer never exits non-zero, never writes to stderr, and never prints a credential value.

### 1.3 The thinking level, next to the model

Added 2026-09-25 at the maintainer's request (*"add the thinking level next to the model display in our custom
footer"*). One format everywhere yolo draws it: `Model · level`, the agent's own words for the level (`Opus ·
high`), and nothing at all when the agent reports none, never a guess. The value is the LIVE session's wherever the
agent exposes one, so a mid-session change shows.

Only claude's footer is one yolo draws the model in, because its `statusLine` replaces Claude's own line; every other
agent keeps its stock footer, which already names the model, and in each of those the stock footer already shows
the level beside it. So yolo adds the level to claude alone and repeats nothing:

| Agent | Where the level is | What yolo does |
|---|---|---|
| claude | the status-line JSON's `effort.level`, the live, effective level, present only for a model that supports effort (2.1.283 strings: `...OS(model)&&{effort:{level:…}}` beside `thinking:{enabled:…}`) | `{ · \|stdin.effort.level}` after the model. A model without effort shows the model alone; the `thinking.enabled` boolean is not shown |
| pi | pi's stock footer: `model • level`, or `model • thinking off` (`dist/modes/interactive/components/footer.js`, 0.87.1) | nothing; it is already next to the model |
| omp | the stock status line's `model` segment appends the level (`showThinkingLevel`, on in the default presets; `status-line/segments.ts` at upstream `8b619a2f`) | nothing |
| opencode | the prompt row shows `· <variant>` after the model when a variant is selected (1.18.32 binary) | nothing |
| copilot | its footer's `showModelEffort` (1.0.48 `app.js`) | nothing |
| agy | agy's own line, which stays above yolo's ([`OQ-FT12`](#OQ-FT12)); whether agy's status-line JSON carries a level was not established from the binary, and agy is never run to find out | nothing |

A command already frozen into a user's Claude `settings.json` ([§3](#3-how-a-users-own-footer-survives)) keeps its old template, and so keeps showing the
model alone; a fresh file gets the new one.

## 2. One renderer, one adapter per agent

**Rendering lives in one hidden core subcommand, not in per-agent scripts.** A script goes blank when a guardrails
shim blocks a tool it uses, it needs `jq`, which `macos-user` does not bake, and seven scripts would be seven
readers of the profile table. The subcommand, `yolo internal footer` *(coined here; the name is the implementer's)*,
takes the agent's name, switches and plain words as arguments, so core keeps no agent list. It reads its own env
and, optionally, the agent's JSON on stdin, and prints one line from a template the pack passes, so core never
learns an agent's stdin schema. A template names yolo facts (`{yolo.billing}`, `{yolo.notch}`) and JSON paths into
stdin (`{stdin.model.display_name}`); a missing field renders empty, and a literal `{…}` never reaches the screen.
`{prefix|name}` prints prefix before the value only when there is one, so an optional field never leaves its
separator behind; a renderer older than that form reads it as an unknown name and prints nothing.
In a jail it reads no file and makes no network call. At the host it reads your user config, and, when that selects
any profile, the packs it selects ([OQ-FT6](#OQ-FT6)). A run costs a median 2.2 ms in a jail and 4.8 ms at
the host when it composes a profile (MEASURED, [§2.1](#21-as-built)).

**yolo adds; it never takes away** ([DIR-FT2](#DIR-FT2)). Wherever an agent has a stock status line, yolo's segment
goes beside it, and no adapter removes or replaces it:

| Agent | Hook | How yolo delivers it | The agent's own line |
|---|---|---|---|
| claude | `statusLine: {type: "command", command, …}` in `settings.json`; session JSON on stdin | The claude pack's settings defaults, with a command that runs the renderer. Claude skips it until workspace trust is accepted; the pack pre-accepts that for the jail workspace, so it bites only at the host | No stock line. The row sits above Claude's footer badges and keeps them, but Claude then hides most keyboard hints (`esc to interrupt`, `? for shortcuts`, `hold space to speak`). No key keeps them: the one loss this rule cannot prevent |
| agy | Claude-shaped `statusLine`, plus `stack_with_default`, in `~/.gemini/antigravity-cli/settings.json` | The agy pack's settings defaults, with `stack_with_default: true` ([OQ-FT12](#OQ-FT12)) | Any `statusLine` replaces agy's line unless that flag is true, so yolo sets it and agy's line stays |
| copilot | `statusLine` in `~/.copilot/config.json`, an edited-in-place file; `command` is a file path or a shell command | A pack-shipped script whose path goes into the config as a default; yolo rewrites the script every boot, so the value in your file never changes. The value runs the script with `sh` only when it is readable, not the bare path, because no file an embedded pack ships can carry an exec bit and a backend may deliver no script ([as built](#21-as-built)). No experimental flag is set or checked ([OQ-FT7](#OQ-FT7)) | Kept: in 1.0.88 the custom line is one footer item, `custom`, beside fourteen stock ones |
| pi | Extension API: `ctx.ui.setStatus(key, text)`, keyed and sorted | A file into `~/.pi/agent/extensions/`, the route `yolo-openai-auth.js` already takes; it calls `setStatus("yolo", …)` on `session_start` | Kept: status keys sit in pi's footer. The adapter never calls `setFooter`, which replaces it |
| omp | The same `setStatus` API; extensions from `~/.oh-omp/agent/extensions` | The same extension file with omp's arguments, delivered by the omp pack to `extensions/yolo-footer/index.js`, a directory of yolo's own | Kept, as in pi |
| opencode | TUI plugin slots, loaded from the specs in the `plugin` lists of `tui.json` and `tui.jsonc` (typed in the shipped `@opencode-ai/plugin` package) | A plugin showing the notch; opencode's prompt row already shows the provider. The opencode pack's `tui` surface defaults `plugin` to `["./yolo/footer.js"]`, and a `files` contribution delivers the plugin to `~/.config/opencode/yolo/footer.js`. Append slots only: the other two modes, `replace` and `single_winner`, show a plugin's content instead of opencode's own | Kept |
| codex | **None.** `[tui].status_line` takes only built-in item ids, with no custom text, no command and no provider item | Nothing until upstream [openai/codex#17827](https://github.com/openai/codex/issues/17827) lands | — |

pi's own footer prepends `(provider)` only when more than one provider is available and the line has room, and
that is pi's id, not yolo's words, so both can appear; pi also tracks in-session model switches yolo cannot see.
This needs **no new contribution kind and no new manifest field**: switches and plain words are arguments in the
pack's own command string or extension.

### 2.1 As built

Built 2026-09-25, and not yet seen under a live agent: a human confirms build items 3 to 7
([what I would build](#what-i-would-build-in-order)).

- **The renderer** is the package `internal/footer`, reached as `yolo internal footer` (`internal/cli/internal.go`).
  Its flags: `--agent`, `--login`, `--switch VAR=ID` (repeatable; the first that is on wins), `--truthy` (the values
  a switch counts as on, trimmed and case-insensitive; absent, any non-empty value is on), `--words ID=WORDS`
  (repeatable), `--bridged NAME` (repeatable; a profile name or a provider id) and `--template`. Every flag takes one
  value and an unknown flag is skipped with its value, so a command frozen into an edited-in-place file outlives the
  flags it was written with. It reads the three tables with the entrypoint's own loaders and resolves a profile
  with `packload.ProviderFor`, the rule the launch uses; a provider missing from `YOLO_PROVIDERS` counts as unknown.
  Every value it substitutes loses its control characters, so an agent's JSON cannot carry a terminal escape into
  the footer. It reads stdin only when the template names a `{stdin.…}` field, at most 1 MiB and for at most
  300 ms. A run costs a median 2.2 ms over twenty runs (MEASURED).
- **claude**: login `Claude subscription`; all six provider switches Claude 2.1.282 tests, in its order
  (`CLAUDE_CODE_USE_BEDROCK`, `_FOUNDRY`, `_ANTHROPIC_AWS`, `_ANTHROPIC_GOOGLE_CLOUD`, `_MANTLE`, `_VERTEX`), with
  its truthiness. The ids for the five that are not yolo providers are coined in the pack's command, from Claude's
  own keys. Their words are Claude's own labels, except two shortened the way `bedrock` → `Bedrock` is: Claude's
  `Amazon Bedrock (Mantle)` is `Bedrock Mantle`, keeping a parenthesis out of a segment that uses them for `(env)`,
  and `Google Vertex AI` is `Vertex AI`. `openai-codex`, `cerebras` and `kilo` are marked bridged: claude speaks
  only `anthropic`, and none of the three declares an `anthropic` endpoint of its own, so claude reaches each at the
  wire bridge's address. Template: `{stdin.model.display_name}{ · |stdin.effort.level} · yolo: {yolo.billing} ·
  {yolo.notch}`, so the session's effort level sits next to the model ([§1.3](#13-the-thinking-level-next-to-the-model)).
- **agy**: login `Google AI subscription`, template `yolo: {yolo.billing} · {yolo.notch}` (agy's own line, which
  stays, already names the model), and `stack_with_default: true`.
- **copilot**: login `Copilot subscription`, and the same three routes marked bridged, since copilot prefers an
  `anthropic` endpoint whenever a provider offers one. The script is `packs/copilot/footer/yolo-footer.sh`, delivered
  by a `files` contribution to `~/.copilot/yolo/footer.sh`, and the config default is
  `test ! -r ~/.copilot/yolo/footer.sh || sh ~/.copilot/yolo/footer.sh`. Three facts shape that value:
  - Copilot 1.0.88 spawns a command that is an existing path directly, which needs an exec bit, and runs anything
    else with `/bin/sh` (MEASURED). `embed.FS` reports every embedded file as 0444 (`packs/embed.go`), so the value
    names `sh`.
  - Copilot counts a non-zero exit as a failure and warns *"Status line command failed; the status line will be
    blank"* (MEASURED). macos-user renders copilot's config but delivers no `files` contribution
    ([per setup](../../userguide/reference/settings-per-setup.md#what-a-pack-can-contribute-per-setup)), so there the value finds
    no script. The `test` makes that case exit 0 with nothing printed.
  - Copilot expands env references in the command before a shell sees it (MEASURED), so the value carries no `$`.

  The script sits in `~/.copilot/yolo/`, a directory only yolo uses. `preparePackFiles`' one-time migration of the
  old pack-file mountpoints archives every unclaimed empty file in the directory that holds a single-file target, and
  `~/.copilot` itself is copilot's own state.
- **pi and omp**: one extension file each, `packs/pi/extensions/yolo-footer.js` and
  `packs/omp/extensions/yolo-footer.js`, delivered by a `files` contribution into the agent's own extension
  discovery: `~/.pi/agent/extensions/yolo-footer.js`, beside `yolo-openai-auth.js`, and
  `~/.oh-omp/agent/extensions/yolo-footer/index.js`. On `session_start`, in a session with a UI, it runs the
  renderer once and calls `ctx.ui.setStatus("yolo", line)`. It never calls `setFooter`, and with no `yolo` on PATH
  or an empty line it sets nothing. The renderer's arguments sit in the file as a JSON array, `FOOTER_ARGS`, which the
  adapter tests read out and run. The login words are `pi's own login` and `omp's own login`: neither agent has one
  built-in login to name. `openai-codex` reads `ChatGPT subscription`. omp marks `openai-codex` bridged, because it
  speaks no Responses wire and so reaches that provider at the wire bridge's `anthropic` address. pi marks nothing:
  every shipped adapter converts to `anthropic`, which pi does not speak. Template `yolo: {yolo.billing} ·
  {yolo.notch}`. omp's file sits in a subdirectory, which omp's discovery loads as `index.js`, because `~/.oh-omp` is
  workspace state and the migration above would otherwise scan your own extensions directory.
- **opencode**: a TUI plugin, `packs/opencode/footer/yolo-footer.js`, delivered to `~/.config/opencode/yolo/footer.js`
  and listed by a new `tui` surface on `~/.config/opencode/tui.jsonc`, whose one default is
  `plugin: ["./yolo/footer.js"]`. opencode 1.18.32 reads both `tui.json` and `tui.jsonc` in its global config dir,
  resolves a relative spec against the directory of the file that lists it, requires a file plugin to
  default-export `{ id, tui }`, and maps `@opentui/solid/jsx-runtime` to the copy compiled into its own binary for
  any plugin file outside `node_modules`. So the plugin builds its element with
  `jsx("text", …)` and needs no JSX transform (MEASURED, [appendix](#appendix-evidence)). `tui(api)` runs the
  renderer once with the template `yolo: {yolo.notch}` and registers two **append slots**, OpenTUI's name for a
  slot that keeps the host's own content and adds each plugin's after it: `home_prompt_right` and
  `session_prompt_right`, the right end of the prompt's info row, where opencode names the model and its provider.
  The text takes the theme's muted color, the provider name's. With no `yolo` on PATH it registers nothing. More
  facts about it:
  - **The list is in `tui.jsonc`, never `tui.json`, so yolo cannot block opencode's move of your TUI settings.**
    opencode 1.18.32 moves `theme`, `keybinds` and `tui` out of `opencode.json` into a new `tui.json` when it
    starts, but only while `tui.json` does not exist, and after that its TUI reads them from the tui files alone.
    A `tui.json` that yolo created first would leave those keys in `opencode.json`, where nothing reads them (found
    in review; MEASURED against the binary, [appendix](#appendix-evidence)). A host apply over an `opencode.json` that
    still holds them creates no `tui.json` and leaves them for opencode to move (MEASURED;
    `internal/cli`'s `TestHostApplyLeavesOpencodesThemeMigrationAlone`). Whatever a revert leaves in `tui.jsonc`,
    the move is unaffected, since it looks for `tui.json` alone.
  - A list merges whole within one file, so a `tui.jsonc` of yours that sets `plugin` replaces yolo's entry. That is
    [§3](#3-how-a-users-own-footer-survives)'s rule applied to a list: to keep both, add `"./yolo/footer.js"` to that
    list, and to drop yolo's plugin, set `"plugin": []` there. A `tui.jsonc` with only other keys keeps yolo's list
    beside them. opencode concatenates the two files' lists, so a `plugin` list in your `tui.json` keeps yolo's entry
    beside yours.
  - A `tui.jsonc` of yours that opencode reads but yolo's JSON codec does not, one with comments or a trailing comma,
    gets no plugin at the host and is left byte for byte (MEASURED, a scratch-home host apply).
  - The plugin stays out of `~/.config/opencode/plugin/` and `plugins/`, which opencode's server loader scans for its
    own plugins, and out of `~/.config/opencode` itself, which is opencode's.
- **The host's profile read** ([OQ-FT6](#OQ-FT6)) is `hostFooterTables` (`internal/cli/hostfooter.go`), which the
  `internal footer` arm hands the renderer. The renderer asks it only outside a jail, only when the agent's env carries
  no `YOLO_USE_PROFILES`, and only for a template that names `{yolo.billing}`. It resolves as `yolo host env` with
  no `-p` does: the user config's `use_profiles`, your `profiles`, the composed provider table, and
  `packload.ResolveProfiles` over them. So a one-launch `-p` is invisible to it
  ([§4](#4-where-the-facts-come-from-and-how-fresh-they-are)). It parts from that path in one place, because it
  delivers nothing and runs on every refresh: it reads each selected pack's declarations where the pack store already
  holds them, rather than staging fetched packs into a temp dir, and skips a fetched pack whose tree for the pinned
  commit is not checked out rather than checking it out (`packsrc.Store.ResolveExisting`). A profile only that pack
  declares then reads as its bare name, an under-claim. So a refresh writes nothing to the pack store, and two
  sessions refreshing at once cannot race on a checkout (found in review; `internal/cli`'s
  `TestHostFooterChecksNoFetchedPackOut`). A failure leaves a table empty, so the footer names the login, and
  nothing reaches stderr. A median run costs 4.8 ms with a profile selected and 2.1 ms with none, against 2.2 ms in
  a jail (twenty runs each, MEASURED). The first host run of a build also materializes the embedded pack tree under
  `~/.local/share/yolo-jail/embedded-packs/`, the same lease every host `yolo` takes. A scratch-home
  `yolo host apply --assert` with a bedrock profile selected for claude filled Claude's `statusLine`, and running it
  printed `Opus · yolo: Bedrock · host`; with `CLAUDE_CODE_USE_VERTEX=1` it printed
  `Opus · yolo: Bedrock ≠ Vertex AI (env) · host` (MEASURED).
- **macos-user's marker** ([OQ-FT13](#OQ-FT13)): `buildPlan` (`internal/macosuser/orchestrator.go`) sets
  `YOLO_VERSION` to `version.Get` over the launch's repo root, the value the container arm computes. It sets it after
  `env_sources` and the caller's own sandbox env, so no composed layer can empty it. It travels in the **session env
  file**, the file this backend writes the sandbox's env into for the provisioning stage's and the agent's argv to
  read (`ExecWithEnvFile`). The bootstrap never sees it: its env is a closed list
  (`buildBootstrapEnv`) passed through `env -i`. [§2.2](#22-what-macos-users-marker-moves) is the audit the ruling
  asked for.
- **Delivering the files into the workspace overlay.** On Apple Container a launch copies each single-file `files`
  target into the workspace overlay, `<workspace>/.yolo/home`, which is that jail's own home. On podman it creates an
  empty mountpoint there for a target under a declared state dir, such as omp's under `~/.oh-omp`. Both writes are
  made beneath the overlay (`copyFileBeneath` and `mountpointBeneath`, `internal/cli/run/wsstatebeneath.go`, over
  `os.Root`). So a symlink the jail leaves at a target, or at a directory above it, cannot carry the write onto a host
  file. Before review both writes followed such a link (found in review; the tests in
  `internal/cli/run/wsstatebeneath_test.go` fail on the old code). The copy fix covers every `acMaterialize` caller,
  not only the footer's.

Three limits the rulings leave:

- **The pack marks a route bridged by name, so only a provider that exists when the command is written.** A
  provider you declare yourself with only an `openai` endpoint, or one a later release ships after the command
  was frozen into a host file, shows without `(bridge)`. The shipped set is checked: `internal/footer`'s
  `TestBridgedRoutesAreMarked` resolves every shipped provider for claude, copilot, pi and omp with the launch's own
  resolver and fails when an adapter's list drifts from it.
- **The defaults reach the host too.** `yolo host apply` renders the same pack defaults and `files`. So it fills
  Claude's, agy's and copilot's `statusLine` and opencode's `plugin` in `tui.jsonc` once, where the file has none,
  and writes `~/.copilot/yolo/footer.sh`, the pi and omp extensions and opencode's plugin (MEASURED for claude, pi,
  omp and opencode in a scratch home). agy's fill includes `stack_with_default: true`. The frozen values name only
  stable paths and flags, so a later renderer fix reaches them.
- **macos-user delivers no `files` contribution**
  ([per setup](../../userguide/reference/settings-per-setup.md#what-a-pack-can-contribute-per-setup)). So there, copilot's item
  stays empty (its command finds no script and exits 0), and pi and omp get no extension. opencode's plugin list
  names a file that is not there, so opencode logs a `[tui.plugin]` line to its console, which its TUI keeps closed
  on errors (`openConsoleOnError: false` in 1.18.32). Claude's and agy's footers need no file and say `jail`. All
  UNVERIFIED on a Mac.

### 2.2 What macos-user's marker moves

[OQ-FT13](#OQ-FT13) set the marker only after checking every other reader on that backend. The marker reaches
only processes started from the session env file: the provisioning stage, the agent, and any `yolo` either one
runs. None of the `yolo internal` verbs a session runs by itself reads it: `node-floor-satisfied`, `refresh-servers`,
`capture-materialize` and `openai-auth-client` (READ). Each reader that changes moves a `yolo` in the sandbox from
the host's answer to a jail's. The sandbox runs as its own account, with `HOME` set to that account's home, so the
host's answer was about that account, never about yours. Each row is READ, and each effect is UNVERIFIED on a Mac.

| Reader | What it decides | In the macos-user sandbox, with the marker |
|---|---|---|
| `internal/footer` (`Notch`, `WithHostTables`) | The notch; whether to compose the tables from the user config | Says `jail`, and reads the three tables from the launch env, which this backend carries (`packChannel.launchEnv`). The purpose of the change |
| `hostApplyGate` (`internal/cli/hostapplygate.go`) | The host-apply check before `yolo host -- <agent>` | A no-op. It used to evaluate the sandbox account's config as if it were yours |
| `refuseInJailPromote` (`internal/cli/configpromote.go`) | Whether `yolo config promote` may write | Refused, naming the host command. It used to write into the sandbox account's `~/.config/yolo-jail` |
| `packUpdate` (`internal/cli/packupdate.go`) | Whether `yolo pack update` ends with `host apply --assert` | It no longer does. That apply would have rendered into the sandbox account's home |
| The five retired-key validators in `internal/config/validate.go` (`agents`, `journal`, `host_processes`, a provider's bare `base_url`, `agent_profiles`) | Error or warning for a retired key | A warning, as in a container jail. The launch on the host has already refused the key |
| `checkCacheRelocations`, `checkHostFiles` (`internal/config`) | Whether validation stats host paths | Shape and scope are still checked; the paths are not stat'ed from inside the sandbox |
| The workspace-scope check in `internal/config/validate_loopholes.go` | Error or warning for a workspace file setting a user-scope loophole key | A warning, as in a container jail |
| `InheritedLaunchPath` (`internal/config/userlayer.go`) | Whether the user scope folds in `~/.config/yolo-jail/inherited-launch.jsonc` | It looks for the file; only the container run writes one (`internal/cli/run/inheritscope.go`), so nothing changes |
| `LoadCacheRelocations` (`internal/config/relocations.go`) | A launch's relocation binds | None from inside the sandbox; this backend does not implement relocations and warns about them anyway |
| The assembled-snapshot read in `LoadConfig` (`internal/config/load.go`) | Whether config loading takes `<workspace>/.yolo/config-assembled.json` | Unchanged: it also needs the workspace to be `$YOLO_WORKSPACE` or `/workspace`, which a macos-user workspace is not |
| `banner.Side` (`internal/banner`) | The startup banner's last field | `in-jail` instead of `host` |
| `version.Get` (`internal/version`) | The version a `yolo` reports | The launcher's, because the variable wins; the staged binary is the launcher's own, so the value is the same |
| `yolo check`'s `inJail` (`internal/cli/check`) | Which sections run | The host-only sections step aside with an "Inside jail" note: image, disk usage, macos-user readiness, loopholes, host-service liveness, GPU, KVM, host wrappers, the legacy base-home note, `--accept-config-changes`. The storage-layout migration is skipped. The one jail-only section, nix-ld, globs `/mise/installs/node`, which a Mac does not have, so it prints nothing |
| `yolo loopholes` (`internal/loopholes`) | Doctor checks and activation | `status` says doctor checks are host-side instead of running host doctor commands in the sandbox. `list` judges requirements the in-jail way, so a loophole with host bind mounts counts active only when their jail-side path exists |
| `yolo stores` (`internal/cli/stores`) | The report's frame | Labeled as this jail's own stores, without the host-only post-launch trigger |
| `yolo prune` (`internal/prune`) | The host-store sections | Skips yolo's superseded store outputs and refuses the opt-in nix store GC |
| `yolo config drift` (`internal/cli/configdrift.go`) | A note on what it does not compare | Adds the note that user-level config edits are not compared from in here. The note is worded for a container's launch snapshot |
| `processOwnsWorkspace` (`internal/cli/configtarget.go`) | Whether a config verb's target is this process's home | Unchanged: it also needs the workspace at `/workspace` |
| The run pipeline's `inJail` (`internal/cli/run`), which also picks the `/mise` store through `jailMiseStoreDir` (`internal/cli/run/storagehelpers.go`) | A launch started from inside the sandbox | Takes the nested-jail branches: the `/mise` store, no image load, no housekeeping reapers, no host-path probes. It used to take the host branches as the sandbox account. Whether a launch from inside the sandbox can work at all is not known; this row says only which branches it takes |
| The launch log's and boot log's variable-name lists | Logging | Name the variable only; no behavior |

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
  command that runs a stable script path, and exits 0 silently where no script was delivered (copilot), or a
  command whose arguments do not change between releases, with the logic in the renderer; a provider added later
  has no words there and shows its id. Turned off, the script prints nothing.

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
your user config, resolving profiles as `yolo host env` does (a median 4.8 ms per run with a profile selected,
MEASURED: [§2.1](#21-as-built)). A one-launch
`yolo host -p bedrock -- claude` is not in that file, so its Bedrock switch shows as `(env)`. When your config does
select a profile, a one-launch `-p` naming another is invisible too, and the footer names the config's:
`yolo host -p zai -- claude` with `use_profiles: {claude: bedrock}` shows `Bedrock` while the process carries z.ai's
base URL, because that launch exports no table and the renderer tests only the agent's own switches (found in review;
MEASURED with a stub `claude` that runs its filled status-line command). Closing it means `yolo host --` exporting its agent's selection, which is not
built: a nested `yolo` the agent starts would read that variable as its launch env. How to export it is
[OQ-FT15](#OQ-FT15).

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

> [!NOTE]
> **The bridge's streamed-usage defect is fixed, 2026-09-25** (not yet measured on a live bridged
> turn). The bridge used to send `message_start` with empty usage, keep only output tokens, stop reading after the
> finish chunk, and never ask for `stream_options.include_usage`, so Claude's cost and context numbers were wrong for
> every bridged model. Now a streamed chat-completions request asks for usage, the translator reads past the finish
> chunk to the usage chunk, and `message_delta` carries input tokens, cache reads and output tokens in Anthropic's
> terms, on the Responses route too. A provider can decline the request field through `supports_usage_in_streaming`.
> [`wire-bridge.md`](../reference/wire-bridge.md#streamed-usage) is the reference. It needed no ruling.

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

9. 💬 <a id="OQ-FT15"></a>**OQ-FT15: How should `yolo host --` tell the footer about a one-launch `-p`?** Opened
   2026-09-25 from [§4](#4-where-the-facts-come-from-and-how-fresh-they-are)'s measured case:
   `yolo host -p zai -- claude` with `use_profiles: {claude: bedrock}` shows `Bedrock`. Two facts decide the
   options. First, the renderer takes all three tables from the env as soon as `YOLO_USE_PROFILES` is set
   (`footer.WithHostTables`), so exporting the selection alone would leave it with no profiles or providers to
   resolve it against. Second, outside a jail the renderer is the only reader of these three names in the
   environment today: `overlayGateProfiles` (`internal/cli/host.go`) reads `YOLO_USE_PROFILES` only at the jail
   notch, and the other readers run in-jail. Options:
   (a) `yolo host --` exports the three tables it already composed for the launch, with only its own agent's
   entry in `YOLO_USE_PROFILES`. The renderer needs no change. Cost: these are the jail's wire names, so any
   later host-side reader of them would take one launch's selection for the user's table.
   (b) `yolo host --` exports one footer-only variable naming the profile, for example `YOLO_FOOTER_PROFILE`
   *(coined here)*, and the host arm of the renderer resolves it against the tables it already composes. No
   other reader can mistake it for a launch table. Cost: a second name for the same fact, and a small change to
   `hostFooterTables`.
   (c) Leave it unbuilt, and document that a one-launch `-p` at the host shows the config's selection or
   `(env)`. Cost: the footer can name the wrong provider, which is the one thing it exists to get right.

   <!-- vantage: oq id=OQ-FT15 leaning="(b) a footer-only variable naming the profile, resolved by the host renderer against the tables it already composes: it fixes the measured wrong label without putting the jail's wire tables into a host process env." -->

   _Leaning:_ (b). It fixes the measured wrong label without putting the jail's wire tables into a host
   agent's env, where a later host reader could take them for the user's config.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| DIR-FT1 | **An easy way to put the provider in the claude footer, then other useful facts, in every agent possible; Bedrock cost in a future design.** *"I want to have an easy way to add the provider name to the claude code footer, and possibly some other useful things … it would be nice to be able to put bedrock costs there if possible, but this is certainly a future design doc … and we'd want this spread to all agents possible."* A direction, not a question | 2026-09-25 | [§1](#1-what-the-footer-shows), [§2](#2-one-renderer-one-adapter-per-agent), [Later](#later-cost-and-failover-a-separate-design) | — |
| [OQ-FT1](#OQ-FT1) | **On by default, at the lowest layer (a).** *"let's try it default on"*. A footer a user already has replaces it | 2026-09-25 | [§3](#3-how-a-users-own-footer-survives) | 2026-09-25: claude, agy and copilot defaults; the pi and omp extensions and opencode's plugin list, each delivered by the owning pack |
| [OQ-FT4](#OQ-FT4) | **The billing route in plain words (your subscription login, e.g. Teams, versus Bedrock versus another provider) and the notch (jail, guest or host); no region, no yolo version, no backend.** *"I was thinking the exact opposite almost. why would I care about region? set it once. I care teams vs bedrock, don't want yolo version, but I do want to know if I'm in a jail/guest or on the host, don't care about backend."* The leaning (region only) was overruled. Naming the plan itself opened [OQ-FT14](#OQ-FT14), and macos-user's notch opened [OQ-FT13](#OQ-FT13) | 2026-09-25 | [§1](#1-what-the-footer-shows), [§1.2](#12-the-notch) | 2026-09-25: the renderer prints these two facts only |
| [OQ-FT5](#OQ-FT5) | **As its leaning:** *"(a) their footer wins and they embed the renderer in it; no engine feature and no wrapper key, since the only user so far has no footer."* | 2026-09-25 | [§3](#3-how-a-users-own-footer-survives) | 2026-09-25: nothing to build; a test pins that a user's own wins |
| [OQ-FT6](#OQ-FT6) | **As its leaning:** *"(a) render at the host: env first, else the profile selection from the user config file, as the one exception to reads-no-file; no new host-only switch."* | 2026-09-25 | [§2](#2-one-renderer-one-adapter-per-agent), [§4](#4-where-the-facts-come-from-and-how-fresh-they-are) | 2026-09-25: `hostFooterTables`, handed to the renderer by `yolo internal footer` ([§2.1](#21-as-built)) |
| [OQ-FT7](#OQ-FT7) | **Set it, and do nothing special about Copilot's gate.** *"why do we care? set it, and it works or it doesn't."* yolo writes Copilot's `statusLine` like every other agent's and neither flips the experimental flag nor checks it. This is the leaning's "no flag", and it also retires the question's other half, whether the gate still exists: nothing depends on it | 2026-09-25 | [§2](#2-one-renderer-one-adapter-per-agent) | 2026-09-25: copilot's default and script |
| [OQ-FT12](#OQ-FT12) | **(b), stack with agy's own line:** the agy default sets `stack_with_default: true`. *"I don't want to get rid of the stock agent status lines wherever they exist. I want to add our own, not remove what's there. So I think it's your option B … but I'm not sure what it has to do with a user's own status lines, 'cause we decided that in a question up and yeah, it'll just work."* A user's own footer is [OQ-FT5](#OQ-FT5)'s | 2026-09-25 | [§2](#2-one-renderer-one-adapter-per-agent), [§3](#3-how-a-users-own-footer-survives) | 2026-09-25 |
| <a id="DIR-FT2"></a>DIR-FT2 | **In every agent, yolo's segment is added beside the agent's stock status line wherever it has one, and never removes or replaces it.** Given with [OQ-FT12](#OQ-FT12): *"I want to add our own, not remove what's there."* Claude's hidden keyboard hints are the one loss no hook avoids | 2026-09-25 | [§2](#2-one-renderer-one-adapter-per-agent) | 2026-09-25, for all six agents with a hook: pi and omp through `setStatus` only, opencode through append slots only |
| [OQ-FT13](#OQ-FT13) | **(a), as its leaning:** the macos-user launch sets `YOLO_VERSION` like the container launch, after each other `config.InJail()` caller on that backend is checked, so its footer says `jail`: one probe, one answer | 2026-09-25 | [§1.2](#12-the-notch) | 2026-09-25: the marker in `buildPlan`, and the audit in [§2.2](#22-what-macos-users-marker-moves); UNVERIFIED on a Mac |
| [OQ-FT14](#OQ-FT14) | **(a), as its leaning:** a Claude login reads `Claude subscription`. A home holds one login, so that already tells a Team login from Bedrock; naming the plan would read Claude's undocumented account cache on every run | 2026-09-25 | [§1.1](#11-the-billing-route) | 2026-09-25 |

## What I would build, in order

1. Separately, and first: the bridge streaming-usage fix in [Later](#later-cost-and-failover-a-separate-design).
   Built 2026-09-25: a failing test first (a recorded stream whose usage chunk follows the
   finish chunk), then the fix on both streaming routes. Done when a bridged Claude turn on a live upstream shows
   non-zero input tokens; that check is still open.
2. The renderer, with tests for the rules in [§1.1](#11-the-billing-route) and [§1.2](#12-the-notch) (an empty
   `YOLO_VERSION` reads as host) and every degenerate input listed there. Built 2026-09-25
   (`internal/footer`, [as built](#21-as-built)), with a test per rule and per degenerate input.
3. The claude adapter, then agy's. Done: a fresh nested jail with a bedrock profile shows `yolo: Bedrock · jail`
   under Claude's input box; a workspace `statusLine` replaces it with none of yolo's keys left; agy shows its own
   line and yolo's together. A human confirms this, and with it the UNMEASURED claim in the status line. Built
   2026-09-25: unit tests run each pack's command through `/bin/sh` against the renderer and
   compose a user's `statusLine` over the default. The human check is still open.
4. The pi and omp extension files. Done: pi's footer as before, plus a `yolo` status entry. Built 2026-09-25: unit
   tests run each shipped file under node against the renderer, with a stand-in for the agent's extension API. The
   check under a live pi and omp is still open.
5. Copilot's script file. Done: its `custom` footer item shows the segment, or it shows nothing and yolo lets it.
   Built 2026-09-25; the check under a live copilot is still open.
6. The host: `yolo host apply` fills Claude's command. Done: host Claude shows `· host` and your config's profile.
   Built 2026-09-25: a scratch-home host apply filled the command, which printed `Opus · yolo: Bedrock · host`. On
   review the read stopped checking a fetched pack out on a refresh (`packsrc.Store.ResolveExisting`). The look at
   a live host Claude is still open.
7. Last, opencode's plugin (its slot API is known only from binary strings), and macos-user's marker
   ([OQ-FT13](#OQ-FT13)): audit each `config.InJail()` caller reachable on that backend, then set `YOLO_VERSION`
   in its launch. Done: a macos-user Claude shows `· jail`, on a Mac. Built 2026-09-25. The slot API turned out to be
   typed in the shipped `@opencode-ai/plugin` package, and the binary's loader confirmed how it is read. A unit test
   loads the shipped plugin under node the way opencode loads a file plugin. On review the list moved from `tui.json`
   to `tui.jsonc`, so yolo never stops opencode moving a theme out of `opencode.json` ([§2.1](#21-as-built)). The
   audit is [§2.2](#22-what-macos-users-marker-moves), and unit tests run the rendered env file through the launch's own
   reader on Linux. Still open: a look at a live opencode, and the Mac check. The Mac check's instrument is the
   macos-user CI workflow, whose `-run '^TestMacosUser'` selects `TestMacosUserFooterSaysJail`
   (`integration/macosuserfooter_test.go`). That test asks a real launch for the marker, for where `yolo` resolves,
   and for the renderer's notch, and it has not yet run on a Mac.

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
| Claude's provider switch order | Strings of 2.1.282: `De()` tests `CLAUDE_CODE_USE_BEDROCK`, `_FOUNDRY`, `_ANTHROPIC_AWS`, `_ANTHROPIC_GOOGLE_CLOUD`, `_MANTLE` and `_VERTEX` in that order, after a gateway check; the labels are `ZR={bedrock:"Amazon Bedrock",vertex:"Google Vertex AI",foundry:"Microsoft Foundry",anthropicAws:"Claude Platform on AWS",anthropicGoogleCloud:"Claude Platform on Google Cloud",mantle:"Amazon Bedrock (Mantle)",gateway:"Cloud gateway"}` | MEASURED |
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
| omp | Binary strings: fixed segment ids, a model segment with no provider, extensions from `~/.oh-omp/agent/extensions`. The binary's embedded `extension-loading.md`: native discovery takes a direct `*.ts`/`*.js` or a subdirectory's `index.ts`/`index.js`, one level deep; the factory is `module.default ?? module`; `disabledExtensions` names `extension-module:<dir>` for a `<dir>/index.js` | MEASURED |
| opencode | Binary strings: the `plugin` list of `tui.json`/`tui.jsonc`; `single_winner` and multi slots; the prompt row shows the provider. `opencode.ai/docs/tui`, fetched 2026-09-24 | MEASURED |
| opencode's TUI plugin API | `@opencode-ai/plugin` 1.18.32, fetched with `npm pack` into a temp dir: `dist/tui.d.ts` types `TuiPluginModule = { id?, tui, server?: never }`, `api.slots.register(plugin)` with `TuiSlotPlugin`'s `id?: never`, a renderer `(ctx, props) => JSX.Element` with `ctx.theme`, and the host slot names (`TuiHostSlotMap`) | MEASURED |
| How opencode loads and draws it | Strings of the `opencode-linux-x64` 1.18.32 binary: `TuiConfig.loadState` takes each `plugin` list from the global `tui.json`/`tui.jsonc`, `OPENCODE_TUI_CONFIG`, project files and `.opencode` dirs, concatenated and deduplicated; `resolvePluginSpec` resolves `./…` against the listing file's directory; the module must default-export an object with `tui()`, not also `server()`, and a path plugin must export `id`; a failed plugin is reported with `console.error("[tui.plugin] …")`, and the TUI renderer is created with `openConsoleOnError:!1`; the server loader, separately, globs `{plugin,plugins}/*.{ts,js}` in config dirs. Slot modes as rendered: `home_logo`, `home_prompt`, `session_prompt` `replace`; `home_footer`, `sidebar_title`, `sidebar_footer` `single_winner`; every other slot no mode, which `@opentui/solid` 0.4.5's `Slot` reads as `append` (`local.mode ?? "append"`), keeping its own children. The prompt passes `session_prompt_right` as its `right` prop, drawn in the row with `u.model.parsed().model` and `.provider` | MEASURED |
| opencode moves TUI settings only into an absent `tui.json` | Strings of the same binary. `TuiConfig.loadState` first runs a migration over every `opencode.json`/`opencode.jsonc` it finds (the global config dir, upward from the cwd, each config directory, `OPENCODE_CONFIG`). For one holding `theme`, `keybinds` or `tui`, it skips when `<dir>/tui.json` exists (`let O=g.join(g.dirname($),"tui.json");if(await B.exists(O))continue;`), and otherwise writes `tui.json` with those keys, keeps `<file>.tui-migration.bak` and strips them from the source. It then reads TUI config only from tui files: `<dir>/tui.json` then `<dir>/tui.jsonc` (`` function h(o,t){return[z.join(o,`${t}.json`),z.join(o,`${t}.jsonc`)]} ``), `OPENCODE_TUI_CONFIG`, project files and `.opencode` dirs. The plugin lists accumulate across those files through `deduplicatePluginOrigins`, which keys a `file://` spec by its full URL and any other by package name | MEASURED |
| A plain-JS plugin can build an element | The binary's OpenTUI runtime-plugin support maps `@opentui/solid`, `@opentui/solid/jsx-runtime`, `solid-js` and `solid-js/store` to its compiled copies; `@opentui/core` 0.4.5's `runtime-plugin.js`: *"non-`node_modules` source files get a dedicated rewrite loader immediately"*, `.js` included; `@opentui/solid`'s `jsx-runtime.js` exports `jsx(type, props)` over `createElement` and `spread` | MEASURED |
| copilot | 1.0.48 `app.js`: `STATUS_LINE:"experimental"`. 1.0.88 `copilot help config` (temp HOME): `statusLine` "displayed below the input", `command` "path to an executable script or a shell command". 1.0.88 `app.js`, which that run unpacked into the temp HOME's cache: no `STATUS_LINE` string; the footer has fifteen items, the last `custom` ("Custom configured statusLine.command") | MEASURED |
| How copilot runs `command` | 1.0.88 `app.js`, unpacked by a `--version` probe in a temp HOME: after `~` and env expansion (`pathExpandHome(envResolveVars(command, process.env))`), a command that is an existing path is spawned without a shell, and anything else with one; its help says *"Plain commands are run with the platform shell (`/bin/sh` on Unix-like systems"*. The child's close handler resolves only on exit code 0; any other rejects as `exit:<code>`, and a run of failures warns once: *"Status line command failed; the status line will be blank."* | MEASURED |
| This jail's env | `YOLO_USE_PROFILES={}` with `CLAUDE_CODE_USE_BEDROCK=1`; `~/.config/yolo-user-env.sh` exports it as `${CLAUDE_CODE_USE_BEDROCK:-'1'}` above the per-entry channel section, which `internal/cli/run/userenv.go` calls the env_sources defaults. `YOLO_PROVIDERS`: `openai-codex` carries an `anthropic` endpoint at `http://127.0.0.1:8215`, a bridged route | MEASURED, READ |
| The bridge keeps its first route | `internal/wirebridged/boot.go` `run`: "Once serving, its upstream remains fixed for the daemon lifetime"; `waitForActiveRoute` returns the first route that resolves | READ |
| One AWS session name | `internal/awsauth/awsauth.go`: `SessionName = "yolo-jail"` | READ |
| Renderer and connection cost | A `yolo` subcommand about 2–3 ms, a TCP dial 3–4 ms, over ten runs | MEASURED |
| The renderer's cost at each notch | A binary built from the 2026-09-25 tree, twenty runs each, in a temp HOME: a median 2.2 ms in a jail (env only), 4.8 ms (max 6.0) at the host with a profile selected in the user config, 2.1 ms at the host with none | MEASURED |
| What `yolo host apply` writes | The same binary, `yolo host apply --assert` into a temp HOME with packs claude, pi, omp and opencode: `claude/settings`, `pi/files` (both extensions), `omp/files`, `opencode/files` and `opencode/tui` rendered; `tui.jsonc` held `{"plugin": ["./yolo/footer.js"]}` and the plugin was 0444. Rebuilt after review, over an `opencode.json` holding `theme` and `keybinds`: no `tui.json` was created and both keys stayed. A `tui.jsonc` of the user's with a comment and a trailing comma was left byte for byte, with no plugin added | MEASURED |
| The macos-user marker reaches the agent | `internal/macosuser/jailmarker_test.go`: the session env file exports `YOLO_VERSION`, no composed layer empties it, the launch's own env-file reader (`ExecWithEnvFile` under `env -i`) hands it to the wrapped process, and the dry-run plan names it. On Linux only | READ, MEASURED |
| Bridge usage defect (as found, before the 2026-09-25 fix) | `internal/wirebridge/stream.go`: `anthropicUsage{}` in `message_start`, only `CompletionTokens` read, `if t.finished { return nil, nil }`; `responses.go` the same empty start; no `include_usage` request field in `internal/wirebridge` or `internal/wirebridged` | READ |
