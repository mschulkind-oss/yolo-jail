---
status: current
stage: CURRENT
verified: 2026-10-16
verified_commit: 8e65f344
covers:
  - internal/footer/
  - internal/cli/hostfooter.go
  - internal/cli/internal.go
  - packs/claude/pack.json
  - packs/agy/pack.json
  - packs/copilot/pack.json
  - packs/copilot/home/.copilot/yolo/
  - packs/pi/home/.pi/agent/extensions/yolo-footer.js
  - packs/omp/home/.oh-omp/agent/extensions/yolo-footer/index.js
  - packs/opencode/pack.json
  - packs/opencode/home/.config/opencode/yolo/
tags: [footer, statusline, packs, providers, profiles, confinement, claude, pi, omp, agy, copilot, opencode, codex]
summary: "How yolo puts two facts into every agent's footer that has a hook: what the session is billed through, in plain words, and where the agent runs (jail, guest or host). One core renderer, `yolo internal footer`, prints them; each agent pack wires it into its agent's own hook as a lowest-layer default, beside the agent's stock status line and never over it. Codex has no hook."
---

# The agent footer — billing route and notch in every agent's status line

**Status:** Verified 2026-10-16 against `8e65f344`. Graduated from the design doc
`docs/design/agent-footer.md`, deleted in `eb202147a` (git history keeps it). MEASURED: each agent's hook
([Evidence](#evidence)), the renderer's output and cost at both notches, the host apply's
writes in a scratch home, and the macos-user marker on a Mac (then `TestMacosUserFooterSaysJail` in the
scheduled `macos-user.yml` workflow). UNMEASURED: any footer under a live agent session, because no
test may start an agent ([What has and has not been observed](#what-has-and-has-not-been-observed)).

**Not re-verified since named setups.** Since `3bd66c7cf` (2026-11-04) the footer prints the
launch's setup name, read from `YOLO_SETUP`, and the `guest` notch and its `YOLO_CONFINEMENT`
marker are retired, so [The notch](#the-notch) describes the retired dial and is owed a
re-verification against [named setups](../design/named-setups.md). The Mac test above is now
`TestMacosUserFooterSaysItsSetup`.

No agent's footer knows what yolo routed it to or where yolo put it, so both facts come from yolo.
One core command renders them, and each agent pack adds that text beside what its agent's footer
already shows. A jail running Claude on Bedrock, for example, shows
`Opus · yolo: Bedrock (env) · jail` under the input box for the whole session, where Claude itself
names the billing only once, in its startup header.

| Component | Lives in |
| :--- | :--- |
| The renderer, `yolo internal footer` | `internal/footer` (`ParseArgs`, `Notch`, `WithHostTables`), dispatched from `internal/cli/internal.go` |
| The host's profile read | `hostFooterTables` in `internal/cli` (`hostfooter.go`) |
| The host launch's wire tables | `hostComposition.wireTables`, appended by `composeHostVarsWith` (`internal/cli`) |
| Claude, agy and copilot adapters | each pack's `settings` surface `defaults` (`packs/claude`, `packs/agy`, `packs/copilot`), plus `packs/copilot/home/.copilot/yolo/footer.sh` |
| pi and omp adapters | `packs/pi/home/.pi/agent/extensions/yolo-footer.js`, `packs/omp/home/.oh-omp/agent/extensions/yolo-footer/index.js` |
| opencode adapter | `packs/opencode/home/.config/opencode/yolo/footer.js` and the pack's `tui` surface |
| macos-user's jail marker | `buildPlan` in `internal/macosuser` |

**Reads with:** [`providers.md`](providers.md#profiles-and-options) (what a provider and a profile
are); [`wire-bridge.md`](wire-bridge.md) (the bridge a bridged route goes through);
[the three notches](../../userguide/guides/confinement.md).

---

## Terms used throughout

- **Footer** *(a term this design coined)*: the line or lines an agent draws under its input box
  (Claude's "status line"). The footer is the agent's; yolo only adds text to it. The **yolo
  segment** *(also coined here)* is the text yolo adds.
- **Stock status line**: what a footer shows with no configuration: agy's built-in line, pi's
  footer, copilot's footer items. Claude has footer badges and keyboard hints, but no status line
  of its own.
- **Provider** and **profile**: as in [`providers.md`](providers.md#profiles-and-options). A
  provider is where requests go (`bedrock`); a profile is *"a named selection over one provider"*,
  and `use_profiles` picks one per agent. The jail carries them in the three **wire tables**,
  `YOLO_PROFILES`, `YOLO_USE_PROFILES` and `YOLO_PROVIDERS`
  ([channels](providers.md#two-channels-split-by-payload-type)).
- **Billing route** *(coined here)*: what the session is billed through, in the footer's words: the
  agent's own login (for example a Claude subscription) or a provider (for example Bedrock). Not a
  region and not an upstream.
- **Notch**: one setting of yolo's `confinement` dial: `jail` (a container, the default), `guest` (a
  confined real home: on macOS the macos-user backend; not built on Linux) or `host` (your real
  machine, where `yolo host apply` writes your agents' config and `yolo host -- <agent>` launches
  one) ([the three notches](../../userguide/guides/confinement.md)).
- **Adapter** *(coined here)*: the per-agent piece an agent pack ships to connect the renderer to
  that agent's hook, through a **contribution kind**: one of the fixed kinds of thing a manifest may
  add ([`pack-system.md`](pack-system.md)).
- **Layer**: an agent's settings file is composed from stacked layers, lowest first: the owning
  pack's defaults, your host file, the workspace file, other packs' overlays, computed values, the
  pack's managed values ([compose engine](pack-system.md#config-surfaces-and-the-compose-engine)).
  Objects merge per key.
- **Recomposed** and **edited-in-place** surfaces: most settings files are recomposed from the
  layers every boot. An edited-in-place one (the manifest's `rmw` mode) is not: yolo fills each
  missing key once, and from then on the file's contents count as the user's. Copilot's settings
  file is one, and *every* file is one at the host.
- **Wire bridge**: yolo's in-jail daemon between an agent and its upstream; a **bridged** route
  goes through it ([`wire-bridge.md`](wire-bridge.md)).

## What the footer shows

Two facts and nothing else ([OQ-FT4](#OQ-FT4)): the **billing route**, in plain words, and the
**notch**. No region, yolo version, backend, credential kind or jail name. One example per notch
(the model name is Claude's own):

| Notch | Claude's footer | What it says |
| :--- | :--- | :--- |
| jail | `Opus · yolo: Bedrock (env) · jail` | A jail with Bedrock switched on outside yolo's profiles |
| host | `Opus · yolo: Claude subscription · host` | Your host Claude, with no profile selected in your user config |
| guest | `Opus · yolo: Bedrock · guest` | A macOS guest session, whose launch says so ([The notch](#the-notch)) |

Other shapes: with a `bedrock` profile selected, `yolo: Bedrock · jail`; under the everything
profile, `yolo: everything (bridge) · jail`; pi's status entry,
`yolo: ChatGPT subscription (profile codex) · jail`.

### The billing route

The footer names the route the agent will actually use, so when the agent's own switch disagrees
with yolo's table, the switch wins. The rule, in order:

1. **A profile is selected** (`YOLO_USE_PROFILES[<agent>]` names one): the route is that profile's
   provider, by the rule the launch uses to pick it (`packload.ProviderFor`). If an agent switch
   (step 2) names a *different* provider, the segment appends `≠ <that provider> (env)`.
2. **No profile, but an agent switch is on.** A switch is an env var the agent pack names as the
   agent's own provider selector, for example `CLAUDE_CODE_USE_BEDROCK` → `bedrock`, tested the way
   the agent tests it: Claude counts it on only for `1`, `true`, `yes` or `on`. Switches are not
   secrets; a credential variable is only ever tested for presence. The first switch that is on
   wins, marked `(env)`: "set outside yolo's profiles".
3. **Neither:** the pack's words for the agent's built-in login, for example `Claude subscription`,
   never guessed. It never names the plan ([OQ-FT14](#OQ-FT14)).

**The plain words come from the pack.** No provider declaration carries a display name, so the
pack's command passes the words for its login and for each provider its profiles select (`bedrock`
→ `Bedrock`, `openai-codex` → `ChatGPT subscription`). A provider with no words shows its id; a
profile name that differs from its provider's follows in parentheses.

**A bridged route shows the profile and `(bridge)`, never an upstream.** The everything profile
serves Anthropic ids untranslated, translates the rest, and may fail over per model to Bedrock, so
no one provider name is true. The pack marks which of its routes are bridged; yolo does not infer
it.

### The notch

The renderer reads the notch from what yolo already puts in the agent's environment:

| Notch | What tells the renderer |
| :--- | :--- |
| jail | `YOLO_VERSION` is non-empty, asked through `config.InJail()`, which exists so there is one answer to "am I in a jail?". Every container launch sets it (`commonEnvBlock` in `internal/cli/run`), and so does the macos-user launch ([OQ-FT13](#OQ-FT13)) |
| host | No marker. Bare `claude` has none, and neither does `yolo host -- claude`: `composeHostVarsWith` adds the pack env, `env_sources`, the provider derive's vars and the three wire tables ([FT-D2](#FT-D2)), and no `YOLO_VERSION` |
| guest | `YOLO_CONFINEMENT=guest` beside `YOLO_VERSION`. The macOS guest launch sets it through its launch env (`config.NotchEnv`); the renderer asks `config.SessionNotch` of it only when `config.InJail()` holds, so the variable alone never claims a sandbox. A jail-notch launch sets nothing, so a `runtime: "macos-user"` launch at the jail notch says `jail` |

**Absence reads as `host`, deliberately:** a missing marker can only under-claim confinement, never
claim a jail that is not there.

> [!WARNING]
> **Ask `config.InJail()`; never copy its test.** The copies already disagree:
> `loopholes.inJail` counts an empty `YOLO_VERSION` as a jail.

Degenerate inputs:

- Absent or malformed `YOLO_*` JSON is treated as empty.
- A profile naming an unknown provider renders the profile name alone; a provider missing from
  `YOLO_PROVIDERS` counts as unknown.
- The renderer never exits non-zero, never writes to stderr, and never prints a credential value.

### The thinking level, next to the model

Added at the maintainer's request (*"add the thinking level next to the model display in our custom
footer"*). One format everywhere yolo draws it: `Model · level`, the agent's own words for the level
(`Opus · high`), and nothing at all when the agent reports none, never a guess. The value is the
live session's wherever the agent exposes one, so a mid-session change shows.

Claude's is the only footer in which yolo draws the model, because its `statusLine` replaces Claude's own
line. Every other agent keeps its stock footer, which already names the model and shows the level
beside it, so yolo adds the level to Claude alone and repeats nothing:

| Agent | Where the level is | What yolo does |
| :--- | :--- | :--- |
| claude | the status-line JSON's `effort.level`, the live, effective level, present only for a model that supports effort | `{ · \|stdin.effort.level}` after the model; a model without effort shows the model alone, and the `thinking.enabled` boolean is not shown |
| pi | its stock footer: `model • level`, or `model • thinking off` | nothing |
| omp | the stock status line's `model` segment appends the level (`showThinkingLevel`) | nothing |
| opencode | the prompt row shows `· <variant>` after the model when a variant is selected | nothing |
| copilot | its footer's `showModelEffort` | nothing |
| agy | agy's own line, which stays above yolo's ([OQ-FT12](#OQ-FT12)); whether agy's status-line JSON carries a level is not established, and agy is never run to find out | nothing |

A command already frozen into a user's host Claude `settings.json`
([How a user's own footer survives](#how-a-users-own-footer-survives)) keeps the template it was
written with, so a host filled before the level was added shows the model alone; a fresh file gets
the current template.

## One renderer, one adapter per agent

**Rendering lives in one hidden core subcommand, not in per-agent scripts.** A script goes blank
when a guardrails shim blocks a tool it uses, it needs `jq`, which `macos-user` does not bake, and
seven scripts would be seven readers of the profile table.

**yolo adds; it never takes away** ([DIR-FT2](#DIR-FT2)). Wherever an agent has a stock status line,
yolo's segment goes beside it, and no adapter removes or replaces it. The one loss no hook avoids is
Claude's: any `statusLine` makes Claude hide most of its keyboard hints.

This needs **no new contribution kind and no new manifest field**: switches and plain words are
arguments in the pack's own command string or extension.

### The renderer

`yolo internal footer` *(the name is the implementer's)* takes the agent's name, switches and plain
words as arguments, so core keeps no agent list. It reads its own env and, optionally, the agent's
JSON on stdin, and prints one line from a template the pack passes, so core never learns an agent's
stdin schema.

- **Templates** name yolo facts (`{yolo.billing}`, `{yolo.notch}`) and JSON paths into stdin
  (`{stdin.model.display_name}`). A missing field renders empty, and a literal `{…}` never reaches
  the screen. `{prefix|name}` prints the prefix before the value only when there is one, so an
  optional field never leaves its separator behind; a renderer older than that form reads it as an
  unknown name and prints nothing.
- **Flags**: `--agent`, `--login`, `--switch VAR=ID` (repeatable; the first that is on wins),
  `--truthy` (the values a switch counts as on, trimmed and case-insensitive; absent, any non-empty
  value is on), `--words ID=WORDS` (repeatable), `--bridged NAME` (repeatable; a profile name or a
  provider id) and `--template`. `--help` on the subcommand is the authority. **Every flag takes one
  value and an unknown flag is skipped with its value**, so a command frozen into an
  edited-in-place file outlives the flags it was written with.
- **Inputs**: the three wire tables, read with the entrypoint's own loaders. Every value it
  substitutes loses its control characters, so an agent's JSON cannot carry a terminal escape into
  the footer. It reads stdin only when the template names a `{stdin.…}` field, bounded in size and
  time ([Current values](#current-values)).
- **In a jail it reads no file and makes no network call.** At the host, with no tables in its env,
  it reads your user config, and, when that selects any profile, the packs it selects
  ([OQ-FT6](#OQ-FT6); [The host's profile read](#the-hosts-profile-read)).
- **Cost**: a run is a few milliseconds at either notch, so it is cheap enough to run on every
  refresh.

### Each agent's adapter

| Agent | Hook | How yolo delivers it | The agent's own line |
| :--- | :--- | :--- | :--- |
| claude | `statusLine: {type: "command", command}` in `settings.json`; session JSON on stdin | The claude pack's settings defaults, with a command that runs the renderer. Claude skips it until workspace trust is accepted; the pack pre-accepts that for the jail workspace, so it bites only at the host | No stock line. The row sits above Claude's footer badges and keeps them, but Claude hides most keyboard hints (`esc to interrupt`, `? for shortcuts`, `hold space to speak`). No key keeps them |
| agy | Claude-shaped `statusLine`, plus `stack_with_default`, in `~/.gemini/antigravity-cli/settings.json` | The agy pack's settings defaults, with `stack_with_default: true` ([OQ-FT12](#OQ-FT12)) | Any `statusLine` replaces agy's line unless that flag is true, so yolo sets it and agy's line stays |
| copilot | `statusLine` in `~/.copilot/settings.json`, an edited-in-place file; `command` is a file path or a shell command | A pack-shipped script whose invocation goes into the config as a default; yolo rewrites the script every boot, so the value in your file never changes. No experimental flag is set or checked ([OQ-FT7](#OQ-FT7)) | Kept: the custom line is one footer item, `custom`, beside the stock ones |
| pi | Extension API: `ctx.ui.setStatus(key, text)`, keyed and sorted | A file in `~/.pi/agent/extensions/`, beside `yolo-openai-auth.js`; it calls `setStatus("yolo", …)` on `session_start` | Kept: status keys sit in pi's footer. The adapter never calls `setFooter`, which replaces it |
| omp | The same `setStatus` API; extensions from `~/.oh-omp/agent/extensions` | The same extension shape, delivered to `extensions/yolo-footer/index.js`, a directory of yolo's own | Kept, as in pi |
| opencode | TUI plugin slots, loaded from the `plugin` lists of `tui.json` and `tui.jsonc` | A plugin showing the notch, in append slots only; opencode's prompt row already shows the provider | Kept |
| codex | **None.** `[tui].status_line` takes only built-in item ids: no custom text, no command, no provider item | Nothing until upstream [openai/codex#17827](https://github.com/openai/codex/issues/17827) lands | — |

Per agent, as built:

- **claude**: login `Claude subscription`; every provider switch Claude tests, in Claude's own
  order (`CLAUDE_CODE_USE_BEDROCK`, `_FOUNDRY`, `_ANTHROPIC_AWS`, `_ANTHROPIC_GOOGLE_CLOUD`,
  `_MANTLE`, `_VERTEX`), with Claude's truthiness. The ids for the five that are not yolo providers
  are coined in the pack's command, from Claude's own keys; their words are Claude's own labels,
  except `Bedrock Mantle` (keeping a parenthesis out of a segment that uses them for `(env)`) and
  `Vertex AI`. `openai-codex`, `cerebras` and `kilo` are marked bridged: claude speaks only
  `anthropic`, and none of the three declares an `anthropic` endpoint of its own, so claude reaches
  each at the wire bridge's address. Its template puts the effort level next to the model
  ([The thinking level](#the-thinking-level-next-to-the-model)).
- **agy**: login `Google AI subscription`, a template of billing route and notch only (agy's own
  line, which stays, already names the model), and `stack_with_default: true`.
- **copilot**: login `Copilot subscription`, with the same three routes marked bridged, since
  copilot prefers an `anthropic` endpoint whenever a provider offers one. The script is delivered by
  a `files` contribution to `~/.copilot/yolo/footer.sh`, a directory only yolo uses, and the config
  value is `test ! -r ~/.copilot/yolo/footer.sh || sh ~/.copilot/yolo/footer.sh`. Three measured
  copilot behaviors shape that value:
  - Copilot spawns a command that is an existing path directly, which needs an exec bit, and runs
    anything else with `/bin/sh`. The value names `sh`, which needs no exec bit; it was chosen
    when an embedded pack could carry none, which [the exec bit](pack-system.md#the-exec-bit) has
    since closed.
  - Copilot counts a non-zero exit as a failure and blanks the line with a warning. The `test`
    makes a missing script exit 0 with nothing printed, for a backend that delivered none.
  - Copilot expands env references in the command before a shell sees it, so the value carries no
    `$`.
- **pi and omp**: one extension file each, delivered by a `files` contribution into the agent's own
  extension discovery. On `session_start`, in a session with a UI, it runs the renderer once and
  calls `ctx.ui.setStatus("yolo", line)`; with no `yolo` on PATH or an empty line it sets nothing.
  Neither adapter awaits the renderer, because pi and omp await every `session_start` handler
  before the session is usable and a cold renderer takes over a second: the handler returns at
  once, the line lands when the renderer answers (within its 2-second timeout), and a line whose
  session was replaced or shut down meanwhile is dropped.
  The renderer's arguments sit in the file as a JSON array, `FOOTER_ARGS`, which the adapter tests
  read out and run. The login words are `pi's own login` and `omp's own login`: neither agent has
  one built-in login to name. omp marks `openai-codex` bridged, because it speaks no Responses wire
  and so reaches that provider at the wire bridge's `anthropic` address; pi marks nothing, since
  every shipped adapter converts to `anthropic`, which pi does not speak. omp's file sits in a
  subdirectory, loaded as `index.js`, because `~/.oh-omp` is workspace state and the pack-file
  migration below would otherwise scan your own extensions directory.
- **opencode**: the plugin is delivered to `~/.config/opencode/yolo/footer.js` and listed by the
  pack's `tui` surface on `~/.config/opencode/tui.jsonc`, whose one default is
  `plugin: ["./yolo/footer.js"]`. opencode resolves a relative spec against the directory of the
  file that lists it, requires a file plugin to default-export `{ id, tui }`, and maps
  `@opentui/solid/jsx-runtime` to its own compiled copy for a plugin outside `node_modules`, so the
  plugin builds its element with `jsx("text", …)` and needs no JSX transform. `tui(api)` runs the
  renderer once with a notch-only template and registers two **append slots**, OpenTUI's name for a
  slot that keeps the host's own content and adds each plugin's after it: `home_prompt_right` and
  `session_prompt_right`, the right end of the prompt's info row, where opencode names the model and
  provider. The text takes the theme's muted color. With no `yolo` on PATH it registers nothing.
  `tui()` does not await the renderer, for the same reason as pi's handler: the slots register when
  the renderer answers, so a session does not wait on it. The plugin has no session event, so it
  has no superseded or shut-down session to drop.

> [!WARNING]
> **opencode's list goes in `tui.jsonc`, never `tui.json`.** opencode moves `theme`, `keybinds` and
> `tui` out of `opencode.json` into a new `tui.json` when it starts, but only while `tui.json` does
> not exist, and after that reads them from the tui files alone. A `tui.json` yolo created first
> would strand those keys in `opencode.json`, where nothing reads them. A host apply over an
> `opencode.json` that still holds them creates no `tui.json` and leaves them for opencode to move
> (`TestHostApplyLeavesOpencodesThemeMigrationAlone` in `internal/cli`).

More about opencode's list: a list merges whole within one file, so a `tui.jsonc` of yours that sets
`plugin` replaces yolo's entry. To keep both, add `"./yolo/footer.js"` to your list; to drop yolo's,
set `"plugin": []`. opencode concatenates the two files' lists, so a `plugin` list in your
`tui.json` keeps yolo's entry beside yours. A `tui.jsonc` that opencode reads but yolo's JSON codec
does not (comments, a trailing comma) gets no plugin at the host and is left byte for byte. The
plugin stays out of `~/.config/opencode/plugin/` and `plugins/`, which opencode's server loader
scans for its own plugins.

**Delivering single files into the workspace overlay.** On Apple Container a launch copies each
single-file `files` target into the workspace overlay, `<workspace>/.yolo/home`, which is that
jail's home; on podman it creates an empty mountpoint there for a target under a declared state
dir, such as omp's under `~/.oh-omp`. Both writes are made beneath the overlay (`copyFileBeneath`
and `mountpointBeneath` in `internal/cli/run`, over `os.Root`), so a symlink the jail leaves at a
target, or at a directory above it, cannot carry the write onto a host file. The copy rule covers
every `acMaterialize` caller, not only the footer's. `preparePackFiles`' one-time migration of the
old pack-file mountpoints archives every unclaimed empty file in the directory that holds a
single-file target, which is why every adapter file sits in a directory of yolo's own.

### The host's profile read

At the host, an agent started outside `yolo host` carries no wire tables, so the renderer asks
`hostFooterTables` for them ([OQ-FT6](#OQ-FT6)). It asks only outside a jail, only when the agent's
env carries no `YOLO_USE_PROFILES`, and only for a template that names `{yolo.billing}`. It resolves
as `yolo host env` with no `-p` does: the user config's `profile` key, your `profiles`, the composed
provider table, and `packload.ResolveProfiles` over them. It parts from that path in one place,
because it delivers nothing and runs on every refresh: it reads each selected pack's declarations
where the pack store already holds them, and skips a fetched pack whose tree for the pinned commit
is not checked out rather than checking it out (`packsrc.Store.ResolveExisting`). A profile only
that pack declares then reads as its bare name, an under-claim. So a refresh writes nothing to the
pack store, and two sessions refreshing at once cannot race on a checkout
(`TestHostFooterChecksNoFetchedPackOut`). A failure leaves a table empty, so the footer names the
login, and nothing reaches stderr. The first host run of a build also materializes the embedded pack
tree, the same lease every host `yolo` takes.

**A selection the host launch refuses is left out** ([FT-D1](#FT-D1)): `hostFooterTables` asks the
host's pairing gate (`packload.PairingRefusal`) for each selected agent and keeps only the
selections it composes, so the footer names the login. Claude's `codex` profile is not one of them:
`yolo host -- claude` serves it with a launch-owned bridge, so the footer names
`codex (bridge) · host` ([HS-D10](../design/host-notch-services.md#HS-D10)).

### What macos-user's marker moves

The macos-user launch sets `YOLO_VERSION` to the launcher's version (`version.Get` over the launch's
repo root, the value the container arm computes), after `env_sources` and the caller's own sandbox
env, so no composed layer can empty it ([OQ-FT13](#OQ-FT13)). It travels in the **session env
file**, the file this backend writes the sandbox's env into for the provisioning stage's and the
agent's argv to read (`ExecWithEnvFile`). The bootstrap's own process environment is a closed list
passed through `env -i`; it reads the session env file into its generator env but takes no `YOLO_`
name from it, so `config.InJail()` stays false there.

The marker reaches only processes started from the session env file: the provisioning stage, the
agent, and any `yolo` either one runs. None of the `yolo internal` verbs a session runs by itself
reads it. Each reader that changes moves a `yolo` in the sandbox from the host's answer to a jail's.
The sandbox runs as its own account, with `HOME` set to that account's home, so the host's answer
was about that account, never about yours. The readers, each read from the code and none observed
on a Mac except the footer's:

| Reader | In the macos-user sandbox, with the marker |
| :--- | :--- |
| `internal/footer` (`Notch`, `WithHostTables`) | Says `jail`, and reads the three tables from the launch env, which this backend carries (`packChannel.launchEnv`) |
| `hostApplyGate` (`internal/cli`) | A no-op, instead of evaluating the sandbox account's config as if it were yours |
| `refuseInJailPromote` (`internal/cli`) | `yolo config promote` is refused, naming the host command |
| `refuseHostApplyInJail` (`internal/cli`) | `yolo host apply`, and `yolo apply` at the host notch, are refused with nothing written |
| `packUpdate` (`internal/cli`) | `yolo pack update` no longer ends with a `host apply --assert` into the sandbox account's home |
| The retired-key validators in `internal/config` | A warning, as in a container jail; the launch on the host has already refused the key |
| `checkCacheRelocations`, `checkHostFiles` (`internal/config`) | Shape and scope are checked; host paths are not stat'ed from inside the sandbox |
| The workspace-scope loophole-key check (`internal/config`) | A warning, as in a container jail |
| `InheritedLaunchPath`, `LoadCacheRelocations` (`internal/config`) | No change in effect: only the container run writes either input |
| The assembled-snapshot read in `LoadConfig` (`internal/config`) | Taken: the launch writes `<workspace>/.yolo/config-assembled.json` and the session names its workspace in `$YOLO_WORKSPACE`, so `yolo config dump` reads the latest launch's merged config |
| `banner.Side`, `version.Get` | `in-jail` instead of `host`; the launcher's version, which is the same value |
| `yolo check`'s `inJail` (`internal/cli/check`) | The host-only sections step aside with an "Inside jail" note |
| `yolo loopholes`, `yolo stores`, `yolo prune`, `yolo config drift` | Each takes its in-jail frame: doctor checks are host-side, stores are labeled as this jail's, host-store sections are skipped, and drift notes what it cannot compare |
| `processOwnsWorkspace` (`internal/cli`) | Unchanged: it also needs the workspace at `/workspace` |
| The run pipeline's `inJail` (`internal/cli/run`) | A launch from inside the sandbox takes the nested-jail branches; whether such a launch can work at all is not known |

## How a user's own footer survives

Layers merge objects **key by key**, not whole. So a lowest-layer yolo `statusLine` object has its
keys merged with a user's own, and any key yolo sets that the user's does not set survives into
their footer. Two rules follow:

- **yolo's default sets only `type` and `command`**, the two keys every user `statusLine` sets, so a
  user's value overwrites every key yolo wrote. No `refreshInterval` or `padding`: both facts are
  fixed per process. agy's `stack_with_default: true` is the one exception
  ([OQ-FT12](#OQ-FT12)): merged per key, it also reaches a `statusLine` you write in agy, keeping
  agy's own line beside yours unless you set it `false`. That is [DIR-FT2](#DIR-FT2) applied to your
  footer too, visible and one key to undo.
- **A value yolo writes into an edited-in-place file must never need to change.** Copilot's
  settings, and every file at the host, keep yolo's first fill as if the user had written it, so a
  later fix never reaches it. So the value is either a command that runs a stable script path and
  exits 0 silently where no script was delivered (copilot), or a command whose arguments do not
  change between releases, with the logic in the renderer. A provider added later has no words
  there and shows its id.

Where yolo writes decides who wins:

| Where yolo writes | Your host `statusLine` | Your in-jail `/statusline` edit |
| :--- | :--- | :--- |
| **The owning pack's defaults (what yolo does, [OQ-FT1](#OQ-FT1))** | you win | you win |
| Another pack's overlay | yolo wins | you win |
| The computed layer | yolo wins | yolo overwrites it on every boot |
| The pack's managed values | yolo wins | yolo wins |

Only the owning pack can write defaults, so the adapter lives in each agent pack, not a separate
footer pack. To keep your own footer *and* yolo's facts, call the renderer from your script
([OQ-FT5](#OQ-FT5)). A host `statusLine` pointing at an unstaged host script may render nothing in
the jail; that is your config, left alone. agy reads no host settings file, so a host agy
`statusLine` never reaches the jail.

**The defaults reach the host too.** `yolo host apply` renders the same pack defaults and `files`:
it fills Claude's, agy's and copilot's `statusLine` and opencode's `plugin` in `tui.jsonc` once,
where the file has none, and writes copilot's script, the pi and omp extensions and opencode's
plugin. The frozen values name only stable paths and flags, so a later renderer fix reaches them.

## Where the facts come from, and how fresh they are

| Fact | Source | Freshness |
| :--- | :--- | :--- |
| Billing route | The three wire tables in the agent's own env, which a jail launch and `yolo host --` both set ([FT-D2](#FT-D2)); for a host agent whose env has none, the selection in your user config ([OQ-FT6](#OQ-FT6)) | Fixed per process when the env carries them; else read on each run at the host |
| Agent switch (for example `CLAUDE_CODE_USE_BEDROCK`) | The agent's own env, tested the agent's way | Fixed per process |
| Notch | `YOLO_VERSION` through `config.InJail()`; absent means host ([The notch](#the-notch)) | Fixed per process |
| Not shown ([OQ-FT4](#OQ-FT4)) | Region, yolo version, backend (not in env: `YOLO_RUNTIME` is the nested-launch selector), credential kind, jail name | — |
| Service health | Not shown: the boot check runs once and writes one count line to `boot.log`, and a connection attempt per refresh would cost milliseconds on every refresh | — |
| Bridged upstream, failover | Not shown: only lines in the bridge's log | Not fixed per process; needs a state file the bridge writes ([Later](#later-cost-and-failover-a-separate-design)) |

**At the host, "env first" finds the launch's tables.** `yolo host --` exports the three tables it
composed for its one agent, under the names a jail launch uses ([FT-D2](#FT-D2)), so a one-launch
`yolo host -p zai -- claude` names `zai` (the provider's id, since claude's command passes no words
for it) rather than the config's selection. Every launch sets all three, the empty selection `{}`
included, so one started inside another launch replaces what it inherited, and `yolo host env`
prints the same three lines. The tables follow the whole composition, its removals included, and a
composed entry under a table's name is dropped, so an `env_sources` null cannot remove a table
([FT-D3](#FT-D3)). Outside the entrypoint's own loaders, the one reader of these names in a host
process env is `overlayGateProfiles`, which reads `YOLO_USE_PROFILES` only at the jail notch, so no
host reader mistakes one launch's selection for the user's table.

> [!WARNING]
> **The renderer must read its own env, never `~/.config/yolo-user-env.sh`**, which every attach
> rewrites with the latest entry's selection. For a native route the process env is authoritative.
> **For a bridged route it is not:** the bridge keeps the first entry's route for the daemon's whole
> life, so an agent started from a later entry shows its own profile while the bridge serves the
> earlier one. Hence `(bridge)` and no upstream until the bridge publishes its state.

## Limits

- **The pack marks a route bridged by name, so only for a provider that exists when the command is
  written.** A provider you declare yourself with only an `openai` endpoint, or one a later release
  ships after the command was frozen into a host file, shows without `(bridge)`. The shipped set is
  checked: `TestBridgedRoutesAreMarked` (`internal/footer`) resolves every shipped provider for
  claude, copilot, pi and omp with the launch's own resolver and fails when an adapter's list drifts
  from it.
- **A one-launch `-p` is invisible to a claude started outside it.** With `"packs": ["claude",
  "zai"]`, a claude started by `yolo host -p zai -- claude` names z.ai, but one started outside
  `yolo host` names the config's selection, because it has no launch.
- **Claude's keyboard hints.** Any `statusLine` hides most of them, and no key keeps them.

## What this does not do

- No footer for codex (no hook), no cost, region, yolo version or backend, and no bridged upstream.
- No service-health check on each refresh, and nothing that turns on or tests Copilot's
  experimental flag.
- It never removes an agent's stock status line or a footer you wrote, and no setting picks the
  fields: changing them means your own `statusLine` ([OQ-FT5](#OQ-FT5)).
- No new contribution kind, and no revival of the deleted Lua transform
  ([OQ-LT1](pack-system.md#oq-lt1)).

## Later: cost and failover (a separate design)

The maintainer asked for Bedrock cost in a future design, so none of it is ruled here
([DIR-FT1](#DIR-FT1)). Facts for that design:

- No agent reports a billed Bedrock figure. Claude prices at Anthropic list, and guesses a list
  price for a bridged non-Anthropic model without saying so.
- The bridge publishes no failover state. Failover is per model and holds until that model's limit
  resets, and the everything route picks an upstream per request by id, so any state file is per
  model (upstream, and since when), not one "current upstream".
- A per-session meter needs a session key the bridge lacks (one daemon per jail; whether Claude's
  session header reaches a custom base URL is UNMEASURED). It also sits close to the gateway
  design's refusal of budgets, which *"needs a price per model and a ledger per session"*
  ([gateway §7](../design/wire-bridge-gateway.md#7-what-the-bridge-still-refuses)).
- Every aws-auth mint shares one session name (`awsauth.SessionName`), so AWS billing cannot tell
  sessions apart.
- The bridge reports streamed usage in Anthropic's terms, so Claude's cost and context numbers are
  right for a bridged model ([`wire-bridge.md`](wire-bridge.md#streamed-usage)); not yet observed on
  a live bridged turn.

## What has and has not been observed

Every adapter is pinned by unit tests that run what the pack ships against the real renderer: each
settings command through `/bin/sh`, the pi and omp extension files and opencode's plugin under node
with a stand-in for the agent's API, copilot's script, and a user's `statusLine` composed over the
default. The host's profile read and one-launch export are pinned in `internal/cli`
(`hostwiretables_test.go`, `hostfooter_test.go`), and the macos-user marker by
`internal/macosuser/jailmarker_test.go` on Linux and `integration/macosuserfooter_test.go` on a Mac,
which has passed in the scheduled `macos-user.yml` workflow.

Outstanding, a human check, since no test may start an agent: one live session per agent with a
hook, reading its footer back. Specifically:

- a fresh jail with a bedrock profile shows `yolo: Bedrock · jail` under Claude's input box, which
  also settles that Claude's status-line command runs in a jail and inherits its env;
  a workspace `statusLine` replaces it with none of yolo's keys left; agy shows its own line and
  yolo's together;
- pi's and omp's footers as before, plus a `yolo` status entry;
- copilot's `custom` footer item shows the segment;
- a host Claude shows `· host` and your config's profile;
- opencode draws the plugin's text from `tui.jsonc`;
- a macos-user session's footer under a live agent;
- a bridged Claude turn on a live upstream shows non-zero input tokens.

## Evidence

The vendor facts the adapters rest on. **MEASURED** means read from a binary, a shipped package or a
run; **SOURCED** means a vendor's published page.

| Claim | Evidence | Label |
| :--- | :--- | :--- |
| Claude's hook and payload | Claude's binary strings: the `statusLine` schema; the stdin builder with `model`, `workspace`, `cost`, `context_window`, `rate_limits` and no provider or account field; status-line commands skipped until workspace trust is accepted | MEASURED |
| Claude's row keeps the badges, hides the hints | `code.claude.com/docs/en/statusline`: *"renders in its own row above the built-in footer badges and does not replace them. With a custom status line configured, Claude Code stops showing most of the footer's keyboard hints"* | SOURCED |
| Claude's provider switches, order and truthiness | Binary strings: the switch test order, the label table, and `["1","true","yes","on"]` applied to `CLAUDE_CODE_USE_BEDROCK` | MEASURED |
| No provider display name | `ProviderContribution` in `internal/packdecl`: name, endpoints, key name, region, models, options, capabilities | READ |
| agy stacks only with a flag | agy's changelog: a `stack_with_default` flag renders both the default and the custom status line | MEASURED |
| Codex has no hook | Binary strings: a fixed `status_line` item list with no custom, command or provider item; openai/codex#17827 open | MEASURED |
| pi | Its extension types: `setStatus` ("Set status text in the footer/status bar") and `setFooter` (replaces the built-in footer); its footer shows `(provider)` only when more than one provider is available and it fits | MEASURED |
| omp | Binary strings: fixed segment ids; extensions from `~/.oh-omp/agent/extensions`; discovery takes a direct `*.js` or a subdirectory's `index.js`, one level deep | MEASURED |
| opencode's TUI plugin API and loader | The shipped `@opencode-ai/plugin` types (`TuiPluginModule`, `api.slots.register`, the host slot names) and the binary's loader: `plugin` lists from both tui files concatenated and deduplicated, relative specs against the listing file, slot modes `replace`, `single_winner` and the default `append` | MEASURED |
| opencode moves TUI settings only into an absent `tui.json` | Binary strings of the migration in `TuiConfig.loadState`, which skips a directory whose `tui.json` exists | MEASURED |
| copilot's `statusLine` | `copilot help config` (`command`: "path to an executable script or a shell command"); its `app.js`: an existing path is spawned without a shell, anything else with one, after `~` and env expansion; a non-zero exit blanks the line with a warning; the footer's last item is `custom` | MEASURED |
| Layers merge per key | `mergeValue` in `internal/agentcfg` ("Arrays and scalars replace wholesale"; objects recurse per key) | READ |
| Edited-in-place defaults freeze | copilot's settings surface is `"mode": "rmw"`; `renderSurfaceRMWSurface` in `internal/entrypoint`: a defaults fill reads as the user's from the next apply on, and `yolo host apply` is pure RMW | READ |
| The bridge keeps its first route | `internal/wirebridged`'s boot: once serving, its upstream remains fixed for the daemon's lifetime | READ |

## Why it's this way

Rulings a maintainer would otherwise undo, keeping their original ids, which code comments and
sibling docs cite.

| ID | Ruling |
| :--- | :--- |
| <a id="DIR-FT1"></a>DIR-FT1 | **A direction, not a question:** an easy way to put the provider in the claude footer, then other useful facts, in every agent possible; Bedrock cost in a future design. *"I want to have an easy way to add the provider name to the claude code footer, and possibly some other useful things … and we'd want this spread to all agents possible."* |
| <a id="DIR-FT2"></a>DIR-FT2 | **In every agent, yolo's segment is added beside the agent's stock status line wherever it has one, and never removes or replaces it.** *"I want to add our own, not remove what's there."* So pi and omp use `setStatus` only, opencode append slots only, agy `stack_with_default`. Claude's hidden keyboard hints are the one loss no hook avoids |
| <a id="OQ-FT1"></a>[`OQ-FT1`](#OQ-FT1) | **On by default, at the lowest layer** (*"let's try it default on"*). A footer a user already has replaces it; moving the adapter to an overlay or managed layer would make yolo beat the user's own `statusLine` |
| <a id="OQ-FT4"></a>[`OQ-FT4`](#OQ-FT4) | **The billing route in plain words and the notch; no region, no yolo version, no backend.** *"why would I care about region? set it once. I care teams vs bedrock, don't want yolo version, but I do want to know if I'm in a jail/guest or on the host, don't care about backend."* |
| <a id="OQ-FT5"></a>[`OQ-FT5`](#OQ-FT5) | **A user's own footer wins, and they embed the renderer in it;** no engine feature and no wrapper key |
| <a id="OQ-FT6"></a>[`OQ-FT6`](#OQ-FT6) | **Render at the host: env first, else the profile selection from the user config file,** the one exception to "reads no file"; no new host-only switch |
| <a id="OQ-FT7"></a>[`OQ-FT7`](#OQ-FT7) | **Set Copilot's `statusLine` and do nothing special about its gate.** *"why do we care? set it, and it works or it doesn't."* yolo neither flips Copilot's experimental flag nor checks it |
| <a id="OQ-FT12"></a>[`OQ-FT12`](#OQ-FT12) | **Stack with agy's own line:** the agy default sets `stack_with_default: true`. *"I don't want to get rid of the stock agent status lines wherever they exist."* |
| <a id="OQ-FT13"></a>[`OQ-FT13`](#OQ-FT13) | **macos-user sets `YOLO_VERSION` like the container launch, so its footer says `jail`: one probe, one answer.** Set only after every other `config.InJail()` reader on that backend was checked ([What macos-user's marker moves](#what-macos-users-marker-moves)) |
| <a id="OQ-FT14"></a>[`OQ-FT14`](#OQ-FT14) | **A Claude login reads `Claude subscription`, without naming the plan.** A home holds one login, so that already tells a Team login from Bedrock; naming the plan would read Claude's undocumented account cache on every run |
| <a id="OQ-FT15"></a>[`OQ-FT15`](#OQ-FT15) | **How `yolo host --` tells the footer about a one-launch `-p`**: decided as an implementation choice, [FT-D2](#FT-D2) |
| <a id="FT-D1"></a>FT-D1 | *Implementation decision.* **At the host, a `use_profiles` selection the host launch refuses is left out of the footer's table, so the footer names the login.** Only the pairing gate is asked, because it is the one refusal that turns on the selection itself. Chosen over a footer that names the refusal: the claude drawing it was never refused, since `yolo host` did not start it on that selection. Revised by [HS-D10](../design/host-notch-services.md#HS-D10): a selection the host serves with a launch-owned bridge is kept |
| <a id="FT-D2"></a>FT-D2 | *Implementation decision, answering [OQ-FT15](#OQ-FT15).* **`yolo host --` exports `YOLO_USE_PROFILES`, `YOLO_PROFILES` and `YOLO_PROVIDERS` as it composed them, with only its own agent's entry in the selection.** The renderer is unchanged. Chosen over a footer-only variable because the notch is an input to one code path per concern (*"host is supposed to act like everywhere else"*), and a footer-only name would be a host-only second name for a fact a jail already carries under these names — the shape [OQ-FT13](#OQ-FT13) refused for the notch marker. Every `yolo host --` sets all three, empty tables included. Pinned by `hostwiretables_test.go` in `internal/cli`. Reversible: a separate variable later changes only the launch's writer and the renderer's host arm |
| <a id="FT-D3"></a>FT-D3 | *Implementation decision, building [FT-D2](#FT-D2).* **The tables join the host composition's own variable list**, rather than being overlaid at the exec, because `yolo host env` prints that list and must print the same lines. One method, `wireTables`, serves the agent and each launch-owned service. As amended by [NC-D72](../plans/notch-convergence.md#NC-D72), the tables follow the whole composition, its removals included, and a composed entry under a table's name is dropped, so no `env_sources` null can hand the agent a parent launch's table or none (`TestTheHostExecKeepsTheWireTablesOverEnvSources`) |

## Current values

Verified at `8e65f344`. The prose above explains what each of these is for; this table is the only
place the exact values are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Renderer subcommand | `yolo internal footer` | `internal/cli/internal.go` |
| Stdin read cap | 1 MiB | `stdinLimit`, `internal/footer` |
| Stdin read timeout | 300 ms | `stdinTimeout`, `internal/footer` |
| Renderer cost (measured, twenty runs, median) | about 2.2 ms in a jail; about 4.8 ms at the host with a profile selected, 2.1 ms with none | measured at the design's build |
| Claude template | `{stdin.model.display_name}{ · \|stdin.effort.level} · yolo: {yolo.billing} · {yolo.notch}` | `packs/claude/pack.json` |
| agy, pi, omp template | `yolo: {yolo.billing} · {yolo.notch}` | each pack |
| opencode template | `yolo: {yolo.notch}` | `packs/opencode/home/.config/opencode/yolo/footer.js` |
| Copilot script destination | `~/.copilot/yolo/footer.sh` | `packs/copilot/pack.json` |
| pi extension destination | `~/.pi/agent/extensions/yolo-footer.js` | `packs/pi/pack.json` |
| omp extension destination | `~/.oh-omp/agent/extensions/yolo-footer/index.js` | `packs/omp/pack.json` |
| opencode plugin destination and list | `~/.config/opencode/yolo/footer.js`, listed in `~/.config/opencode/tui.jsonc` | `packs/opencode/pack.json` |
| The guest marker | `YOLO_CONFINEMENT=guest` | `config.NotchEnv` |
