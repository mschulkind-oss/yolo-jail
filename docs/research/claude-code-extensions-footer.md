---
title: "Claude Code's hooks modules: moving yolo's Claude footer from a status-line command into an extension"
date: 2026-10-03
status: in-review
stage: SKETCH
next: "Rule OQ-CM1 to OQ-CM3. Then build step 1, the plugin disclosure fix, which is worth doing whatever is ruled, and take one live look at the prototype's status entry beside Claude's own hints"
tags: [research, claude, footer, statusline, extensions, hooks-modules, plugins, packs, wire-bridge]
summary: "Claude Code 2.1.287 added hooks modules, which its changelog calls Claude Mods: a plugin runs a TypeScript or JavaScript module inside Claude and can draw a status entry under the prompt, add text to the hint line, or draw a band above the prompt. Most of yolo's Claude footer can move into one. The plugin runs `yolo internal footer` once per session for the billing route and the notch, which are fixed for the process, and redraws the model and effort level from Claude's own events. That ends the process Claude starts on every refresh. It should also bring back the keyboard hints Claude hides whenever any status line is set (inferred from Claude's docs, not seen live), puts yolo's segment beside a user's own status line instead of being replaced by it, and lets a fix reach the host, where today's command is frozen after its first fill. The costs: a version floor (2.1.287), an early-access API that Anthropic can switch off remotely, a model id where the status line had a display name, no effort level until the first request, and a status entry Claude draws in its warning style, a ⚠ in the warning color, read from the binary and not yet seen live. It ships through the plugin route yolo already has, a plugin in the claude pack's skills folder. But yolo's code-running disclosure cannot see a hooks module today, and must be fixed first. A prototype passes `claude plugin validate --strict`, `tsc`, and six `claude plugin test` cases that open no network connection. Three rulings are owed."
---

# Claude Code's hooks modules: moving yolo's Claude footer from a status-line command into an extension

**Status:** 2026-10-03, research. Nothing in the tree changes. The maintainer asked: *"Claude Code just added
JavaScript or TypeScript extensions. I want you to explore how much of what we did for the status bar can be moved
over to extensions and improved by doing that."*

How this round was done:

- **The API** was read from what Claude Code 2.1.288 ships for plugin authors: its type declarations, its reference
  note and its examples ([sources](#sources)).
- **Claude's behavior** was read from strings in the 2.1.288 binary.
- **yolo** was read at `026fca67`.
- **No agent session was started**, so nothing about what Claude draws on screen is measured.
- **Run here:** `claude plugin validate` and `claude plugin test` on a prototype outside the repository, `tsc`, and
  `yolo internal footer`. The renderer makes no agent or API call. The plugin test opened no network connection at
  all ([Appendix A](#appendix-a-evidence)).
- **A review round** re-ran `claude plugin validate` (plain and `--strict`, and on a copy carrying yolo's delivery
  marker), `tsc` and the renderer, and read the binary for how a status entry is drawn and which headers Claude
  sends. It did not re-run `claude plugin test`.

**Labels.** **READ**: a file in the tree. **SOURCED**: Claude Code's shipped reference, types or changelog.
**MEASURED**: I ran it here, or read the string from the binary. **INFERRED**: reasoned, not checked.
**UNMEASURED**: needs a live Claude session, which no test or agent here may start.

> **In short.** Keep the logic in Go and move the drawing into Claude. A small plugin calls the existing renderer
> once per session and draws its line as a status entry. Claude redraws the model and effort from its own events
> with no process at all. That should fix three limits of the status-line route, the hidden keyboard hints
> (INFERRED), the replace-or-be-replaced rule, and the frozen host command. It costs a version floor, some
> early-access risk, and a line Claude draws as a warning (`⚠ yolo: …` in the warning color, read from the binary).

## Terms used here

- **Footer**, **yolo segment**, **billing route**, **notch**, **wire bridge**, **adapter**: as defined in
  [agent-footer.md, *Terms used throughout*](../design/agent-footer.md#terms-used-throughout).
- **Status-line command**: Claude's `statusLine` setting of type `command`, a shell command Claude runs and whose
  output it draws as a row of the footer ([agent-footer.md §2](../design/agent-footer.md#2-one-renderer-one-adapter-per-agent)).
- **Hooks module**: Claude Code's own term for *"a TypeScript or JavaScript file that `hooks/hooks.json` names under
  `modules`"* in a plugin, exporting `register(on, options)` (SOURCED, reference:11-16). Claude's changelog
  announces the feature as *"Claude Mods: plugins may now modify deeper behavior"* (SOURCED, changelog:97,
  2.1.287). It is not a **command hook**, the older kind configured in `settings.json` (the API calls those
  *classic* hooks, reference:27).
- **`$`**: the engine interface every hook receives, through which a module reaches everything outside it
  (SOURCED, reference:16-24).
- **Status entry** *(coined here)*: the line a plugin pins with `$.ui.status(text)`. The API calls it *"this
  plugin's status line"* (types:2263). This doc uses another word so it is never confused with Claude's
  `statusLine` setting.
- **Skills-dir plugin**: a plugin Claude auto-loads from `~/.claude/skills/<name>/`, named `<name>@skills-dir`
  (SOURCED, reference:68; MEASURED, `claude plugin --help`). It is the route yolo's generated LSP plugin already
  takes ([lspplugin.go:17-35](../../internal/jailcontent/lspplugin.go#L17-L35)).

## 1. The Claude footer today

**What it is.** The claude pack's settings defaults set `statusLine` to `yolo internal footer` with Claude's six
provider switches, seven plain-words labels, three bridged routes and the template
`{stdin.model.display_name}{ · |stdin.effort.level} · yolo: {yolo.billing} · {yolo.notch}`
([pack.json:86-91](../../packs/claude/pack.json#L86-L91), READ). The renderer:

- reads the notch from `config.InJail()` ([footer.go:167-172](../../internal/footer/footer.go#L167-L172));
- reads the billing route from the agent's env by three rules in order
  ([footer.go:186-225](../../internal/footer/footer.go#L186-L225));
- always exits 0 and prints nothing on failure ([footer.go:473-484](../../internal/footer/footer.go#L473-L484)).

At the host, with no tables in the env, it also reads your user config on every run
([hostfooter.go:54-115](../../internal/cli/hostfooter.go#L54-L115), READ).

**How often it runs.** Claude 2.1.288 re-runs the command when any of `tokenUsage`, `permissionMode`, `vimMode`,
`mainLoopModel`, `fastMode`, `effortValue`, `thinkingEnabled` or `prStatus` changes (MEASURED, binary strings).
The design doc read a 300 ms debounce in 2.1.282
([agent-footer.md:660](../design/agent-footer.md#L660)). So while a reply streams, a token count that keeps
changing can start the renderer up to about three times a second (INFERRED). Each run is a shell plus the
29 MB `yolo` binary, about 2.2 ms for the renderer alone in a jail and 4.8 ms at the host with a profile
selected (MEASURED there, [agent-footer.md:194-195](../design/agent-footer.md#L194-L195)). Only the model and
effort ever change. The billing route, the switches and the notch are fixed per process
([agent-footer.md:419-421](../design/agent-footer.md#L419-L421)).

**Its limits:**

| Limit | Why | Where |
|---|---|---|
| Claude hides most keyboard hints (`esc to interrupt`, `? for shortcuts`) | Any `statusLine` setting does that, and no key brings them back. The one loss [DIR-FT2](../design/agent-footer.md#DIR-FT2) could not avoid | [agent-footer.md:202](../design/agent-footer.md#L202), [:661](../design/agent-footer.md#L661) (SOURCED there) |
| Your own `statusLine` replaces yolo's whole | The setting holds one command. To keep both, you call the renderer from your script ([OQ-FT5](../design/agent-footer.md#OQ-FT5)) | [agent-footer.md §3](../design/agent-footer.md#3-how-a-users-own-footer-survives) |
| The host value is frozen | `yolo host apply` fills a missing key once, then counts it as yours, so a later template or flag never arrives. A host filled before the effort level was added still shows the model alone | [prism.go:1006-1010](../../internal/entrypoint/prism.go#L1006-L1010), [agent-footer.md:179-180](../design/agent-footer.md#L179-L180) |
| A process per refresh | Above | — |
| Bridged upstream and failover cannot be shown cheaply | A per-refresh read of bridge state would cost a connection on every refresh, 3–4 ms, and the bridge publishes no state yet | [agent-footer.md:423-424](../design/agent-footer.md#L423-L424) |

## 2. What the extension API offers

Everything here is SOURCED from the 2.1.288 types unless marked. The types file opens with *"EARLY ACCESS: this
surface may change between releases without notice"* (types:4).

### 2.1 Where a plugin can draw

| Site | What it is | Surfaces | Fit for the footer |
|---|---|---|---|
| `$.ui.status(text)` | *"Pins `text` as this plugin's status line under the prompt, beside the engine's own pinned notices"*, one per plugin (types:2263-2273). In the terminal it is a pinned, low-priority notification with the text `<plugin name>: <text>` (MEASURED, binary strings `plugin-status-${E}` and `` `${lre(e)}: ${IFe(n)}` ``). The pinned-notice block draws each entry as `⚠ <text>` in the theme's `warning` color unless the notice names a color, which `$.ui.status`'s does not, cut at the row's end (MEASURED, binary strings: `cre` draws `[qb," ",E.text]` with `color:E.color??"warning"` and `wrap:E.wrap?"wrap":"truncate"`; `qb="⚠"`). So yolo's segment would read `⚠ yolo: claude-opus-5-5 · high · Bedrock · jail` in the color of a warning. The same block carries the engine's own pinned warnings, such as failed transcript writes. Desktop and SDK hosts are sent a `ui_status` message, *"One status line per plugin"* (MEASURED, binary strings) | terminal; a push to the others, whose drawing is UNMEASURED | **Adopt first**, with the warning style as a known cost for the live look. It is the prototype's site |
| `PromptHint`, its `tail` prop | The hint line itself. A hook's `tail` is drawn dim at the end of Claude's own line, and *"the terminal keeps the engine's line (its pills stay live)"*. *"No other surface draws it yet"* (types:9531-9566). That line is the one a configured `statusLine` thins ([agent-footer.md:661](../design/agent-footer.md#L661)), so under a user's own `statusLine` this site may show little or nothing (INFERRED; UNMEASURED) | terminal draws; desktop raises but does not draw `tail` | **Fallback** if the live look puts the status entry somewhere poor or its warning style reads badly. It is dim, not a warning |
| `SessionMode`, its `modes` prop | The dim mode labels at the right of the prompt footer. A hook adds one label (types:9516-9529) | terminal, desktop | Rejected for the footer: one word, and `jail` is already in the segment |
| `AbovePrompt` | A band above the input *"where the engine draws nothing of its own"*, collapsible (types:9569-9620) | terminal, desktop | Not for the default footer, which would cost rows on every screen. Kept for a richer view a user opens |
| `Pane` | A framed region; the one site drawn on every surface (types:9630-9632) | all | Too heavy for a footer |

No `StatusLine` render component exists (types:8712, the full list), so a plugin cannot draw into the
`statusLine` row or remove it.

### 2.2 Events and nouns a footer needs

| Need | API | Notes |
|---|---|---|
| Run once per session | `session.start`: *"once per process for each loaded plugin, before the first prompt, then once per fresh load"*, and awaited (types:4053-4063). Its input is `cwd`, `surface`, `isInteractive` (types:10982-10997) | It carries no model |
| The model | `$.session.model()`, *"as `/model` shows it"* (types:2581-2583); `turn.step`'s `e.model`, *"as the engine resolved it for this step (the session's, a fallback's)"* (types:12639-12641); `classic.PostModelSwitch`'s `to_model`, a resolved id (types:7337-7356) | Ids, or for `$.session.model()` whatever `/model` shows, whose exact spelling is UNMEASURED. Nothing in the types carries a display name like the status line's `Opus` (MEASURED, a search of the types file) |
| The effort level | `turn.step`'s `e.effort`, *"the session's setting or the model's default, absent for a model without effort"* (types:12643-12647). Classic hooks fired inside a tool call also carry `effort.level` (types:693-701) | No event in the types names an effort change, and no getter exists (MEASURED, a search of the types file). So it is unknown before the first request. Whether `/effort` raises `config.set` is UNMEASURED. The settings schema has an `effortLevel` key, *"Persisted effort level for supported models"* (MEASURED, binary strings), which `$.settings.read()` would return. But it is the saved choice, not this session's level, so drawing it would be a guess |
| Cost, context, rate limits | `session.measure`, pushed *"after each main-thread turn, and when a rate-limit window moves a whole point"* (types:4133-4143); `$.session.usage()`, the same figures *"as the status line has them"*, free to call (types:2622-2642) | Claude's own estimate at list price, as today |
| Run yolo | `$.process.run(argv, { cwd, env, stdin, timeoutMs })`: *"Commands on the host, run as the user the session runs as. CLI only."* No shell; 30 s default timeout (types:3285-3307). `env` is *"Variables set over the host process's own environment"* (types:7545-7547) | The child inherits Claude's env, as the status-line command does |
| Read a file | `$.fs.read(path)`, text or bytes, at most 4 MiB (types:3017-3034) | — |
| Read the env | `$.env.get(name)`, the name a string literal (types:3373-3390) | — |
| Read settings | `$.settings.read()`, merged settings with `statusLine` and `env` included, read-only (types:3355-3370) | Lets the plugin see a `statusLine` and yield to it |
| Reach a local service | `$.http.fetch(url, init)` (types:3261-3282) | The bridge's loopback address, once it publishes state |
| Keep work alive | `$.clock.every` and `$.clock.after` timers, dropped on reload (reference:124-131) | — |

The module runs *"in an environment of its own, with no DOM and no Node"* (reference:23). A hook has 10 s of its own
time per dispatch, and waiting on a `$` call does not count (`HookBudget.ms: 10_000`, types:4803-4812;
reference:124).

### 2.3 How the module reads yolo's facts, with no Node

| Option | Verdict |
|---|---|
| **`$.process.run(["yolo", "internal", "footer", …])` once per session load** | **Adopt.** One renderer stays the one reader of the profile tables ([agent-footer.md §2](../design/agent-footer.md#2-one-renderer-one-adapter-per-agent)), and nothing is ported to TypeScript. The child inherits Claude's own env, so it sees exactly what today's command sees, at both notches. One run measured a median 6.2 ms here, and the cost is paid once, not per refresh ([Appendix A](#appendix-a-evidence)). This is the pi adapter's shape ([yolo-footer.js:20-51](../../packs/pi/extensions/yolo-footer.js#L20-L51)) |
| `$.fs.read` of a facts file yolo writes at launch | Rejected. The facts belong to one process's env, and one jail can hold several Claude processes started from attaches with different selections ([agent-footer.md:445-450](../design/agent-footer.md#L445-L450)). At the host a bare `claude` has no launch to write the file. It would also be a second channel for a fact the env already carries |
| `$.env.get` of the three tables, resolved in TypeScript | Rejected: a second renderer and a port of `packload.ProviderFor` |

## 3. Each footer fact, today and as an extension

| Fact | Today (status-line command) | As an extension | Better, same or worse, and why |
|---|---|---|---|
| **Billing route** | The renderer, on every refresh, from the env ([footer.go:186-225](../../internal/footer/footer.go#L186-L225)) | The same renderer, once per session load, from the same env | **Same value, much cheaper.** It is fixed per process ([agent-footer.md:419-420](../design/agent-footer.md#L419-L420)). At the host with no tables in the env, a mid-session edit to your user config no longer shows until the next session. The process's real route did not change either (INFERRED) |
| **Notch** | `config.InJail()` on every refresh ([footer.go:167-172](../../internal/footer/footer.go#L167-L172)) | The same, once | **Same value, cheaper.** Detection stays in Go, so guest's future marker lands in one place ([agent-footer.md §1.2](../design/agent-footer.md#12-the-notch)) |
| **Model** | `stdin.model.display_name`, for example `Opus` | `$.session.model()`, then each main-loop request's `e.model` and each model switch | **Worse in wording**: an id such as `claude-opus-5-5`, since the API carries no display name. **Better in truth**: `turn.step` names the model a request actually went to, a fallback included (types:12639-12641) |
| **Thinking level** | `stdin.effort.level`, live, re-run on an `effortValue` change | Each main-loop request's `e.effort` | **Worse**: unknown before the first request, and a `/effort` change shows only at the next request. Never guessed, per [agent-footer.md §1.3](../design/agent-footer.md#13-the-thinking-level-next-to-the-model) |
| **Claude's cost estimate** (later design) | In the stdin JSON, not shown | `session.measure` pushes `cost.usd` (types:10410-10433) | **Same data, better delivery**: pushed after each turn, no process. Still Anthropic list price, not what Bedrock bills ([agent-footer.md:473-474](../design/agent-footer.md#L473-L474)) |
| **Billed Bedrock cost** (later design) | Not available | Not available | **Same**: no agent reports it |
| **Bridge upstream and failover** (later design) | A read per refresh, kept off the footer for its cost; and no bridge state exists | One read per turn (`turn.complete`) or per `session.measure`, through `$.http.fetch` or `$.fs.read` | **Better, once the bridge publishes per-model state** ([agent-footer.md, Later](../design/agent-footer.md#later-cost-and-failover-a-separate-design)). A failover could also raise one `$.ui.toast` |
| **A per-session meter** (later design) | No session key in the footer. But Claude already stamps its session id on its API requests: `X-Claude-Code-Session-Id` is among the headers of its main request path and of its Bedrock and Vertex clients (MEASURED, binary strings `var pbt="X-Claude-Code-Session-Id"`, `function xw(){return{"x-app":…,"User-Agent":…,[pbt]:q()}}` and the per-request `xe={...xw(),…}`) | `$.session.id()` (types:2588-2592) gives the plugin one | **Better on the display side only.** The plugin knows its session. `turn.step` can rewrite only the model and effort (types:12626), so the plugin adds nothing on the wire. Whether the header Claude already sends reaches the bridge at a custom `ANTHROPIC_BASE_URL`, and so whether the bridge can already tell sessions apart without any plugin, is UNMEASURED |

## 4. What an extension makes newly possible

- **yolo's segment sits beside your own `statusLine`.** The status entry and the `statusLine` row are different
  sites, so neither replaces the other. The pinned-notice block is a child of the prompt's footer column with no
  `statusLine` condition on it (MEASURED, binary strings: `e(D1,{})` among that column's children); the on-screen
  layout is UNMEASURED. The plugin can also
  read your `statusLine` through `$.settings.read()` and act on it. Whether it should stay quiet is
  [OQ-CM2](#OQ-CM2).
- **Claude's keyboard hints come back,** because yolo no longer needs a `statusLine` at all
  ([OQ-CM1](#OQ-CM1)). Claude hides most hints *"with a custom status line configured"*
  ([agent-footer.md:661](../design/agent-footer.md#L661), SOURCED there). With none configured they show
  (INFERRED from that wording).
- **Fresh by events, with no process per refresh.** One renderer run per session load. The model and effort
  redraw in-process on each request and model switch. A `-p` run starts no process at all.
- **Fixes reach the host.** On every `yolo host apply` a changed plugin tree is replaced whole
  ([plugin.go:166-182](../../internal/hostskills/plugin.go#L166-L182); [agent-footer.md §3](../design/agent-footer.md#3-how-a-users-own-footer-survives)
  has the frozen alternative). So the never-change rule for frozen commands no longer binds Claude.
- **The template becomes a setting.** A `userConfig` field is a `/config` row, and a change reloads the module
  (reference:70). That would answer [agent-footer.md](../design/agent-footer.md#what-this-does-not-do)'s *"no setting
  picks the fields"* without your own `statusLine`. Not built in the prototype.
- **A band above the prompt** for a richer view: service health, bridge upstream per model, Claude's cost
  estimate. Better opened on demand than drawn by default ([§2.1](#21-where-a-plugin-can-draw)).
- **Live bridge failover and cost,** read once per turn rather than once per refresh, once the bridge publishes
  them ([§3](#3-each-footer-fact-today-and-as-an-extension)).

## 5. What it cannot do, and what it costs

- **Version floor: 2.1.287** (SOURCED, changelog:95-97). Claude 2.1.274 fixed an "unknown key" notice for a
  top-level `$schema` in `hooks/hooks.json` (SOURCED, changelog:1261), so older loaders complained about unknown
  keys there. What a pre-2.1.287 loader does with `modules` is UNMEASURED. No older binary is on disk.
  - **In a jail** the claude pack installs the latest Claude, unpinned ([pack.json:24-27](../../packs/claude/pack.json#L24-L27)), so the floor holds.
  - **At the host** your Claude may be older.
- **A remote switch and local policy can turn it off.** Installed plugins' modules wait on a rollout flag,
  `tengu_plugin_hooks_modules`, default on (MEASURED, binary strings: `var pIe=!0`). Where GrowthBook (Anthropic's
  remote feature-flag service) is off, the default applies: *"a third-party provider, or telemetry opted out"*
  (MEASURED). So a Bedrock session is on, while a subscription session gets what the server says. Also off:
  safe mode, `disableAllHooks`, managed-only hooks, and `--bare` (MEASURED, binary strings). A managed
  `disableSideloadFlags` setting stops skills-dir loading altogether
  ([mcp-configuration.md, OQ-LSP3](../reference/mcp-configuration.md#oq-lsp3)), and so does any managed
  `strictKnownMarketplaces` allowlist unless it names the skills-dir sentinel: *"Policy-list sentinel for the
  ~/.claude/skills/ auto-load (@skills-dir plugins). In strictKnownMarketplaces: opt the scan back IN (by default
  any allowlist blocks it)"* (MEASURED, binary strings).
- **It fails quietly.** The transcript names a module that did not load only for the folders a session hot-reloads
  in that sense: the mods folder, a `--plugin-dir` or a `CLAUDE_CODE_PLUGIN_DIRS` folder. Everywhere else the line
  goes to the debug log alone (reference:72-75). A skills-dir plugin is not in that list, so in every session the
  footer would vanish with nothing on screen, against [the happy-path principle](../reference/happy-path-principle.md).
- **It looks like a warning.** The status entry is drawn as `⚠ yolo: …` in the theme's warning color, in the block
  that holds Claude's own pinned warnings ([§2.1](#21-where-a-plugin-can-draw); MEASURED from minified strings, so
  confirm it in the live look). A permanent footer drawn as a warning is a cost the status-line row does not have.
- **Surfaces.** The status entry is drawn in the terminal. Desktop and the SDK are sent a `ui_status` message whose
  drawing is UNMEASURED. But the prototype sends them nothing: it returns at `session.start` when `isInteractive`
  is false, which the types define as *"false for a `-p` run or the SDK"* (types:10993-10996), and the reference
  counts desktop among *"a long-lived headless session (SDK, desktop)"* (reference:69). So a desktop or SDK session
  gets no yolo segment and runs no renderer. `PromptHint.tail` is terminal-only.
- **Workspace trust.** *"hooks modules not loaded until workspace trust is accepted"* (MEASURED, binary strings).
  That is the gate the status-line command already has, which the claude pack pre-accepts in a jail
  ([pack.json:61-65](../../packs/claude/pack.json#L61-L65)). **Same.**
- **Consent prompts.** The only consent prompt found is the dev-mods one: *"A mod is code Claude wrote; it runs
  with your permissions"*, with *"Enable for this session"* (MEASURED, binary strings). That prompt covers mods
  Claude writes into `~/.claude/dev-mods`, not installed or skills-dir plugins. No prompt was found for an
  installed or skills-dir module (INFERRED from absence).
- **The module has no Node.** It reaches yolo only through `$`, which suits a one-process design ([§2.3](#23-how-the-module-reads-yolos-facts-with-no-node)). The
  static scanner is strict:
  - env names must be literals (types:3373-3390);
  - `$` may be passed only to a function declared at the top of the file. A probe passing it to a closure failed
    `claude plugin validate` with *"$ is passed to "refresh", which is not a function declared at the top of this
    file"* (MEASURED).
- **macos-user.** Skills reach the sandbox home through the home overlay, and `yolo` is on the sandbox PATH, as
  [agent-footer.md:23-25](../design/agent-footer.md#L23-L25) measured in CI. The child would inherit the sandbox
  env, `YOLO_VERSION` included, so it says `jail` (INFERRED; unchecked on a Mac).
- **Host notch.** `$.process.run` resolves `yolo` on Claude's own `PATH`, as `sh -c` does for today's command
  (INFERRED). The plugin is delivered by `yolo host apply`, unlike yolo's LSP plugin, which is jail-only
  ([mcp-configuration.md:527-528](../reference/mcp-configuration.md#L527-L528)).
- **Another agent reading the same folder.** Copilot also reads `~/.claude/skills` for skills
  ([workspace-skills.md:125](../design/workspace-skills.md#L125)). Whether it would load a hooks module there, or
  stumble on `modules`, is UNMEASURED.
- **Early-access churn.** Shapes may move between releases (types:4). The pack installs Claude unpinned, so a
  break would reach every jail at once (INFERRED).

## 6. Delivery through yolo's existing plugin mechanism

**The route exists, and it needs no new contribution kind.** A plugin can sit in a pack's skills folder,
`<pack>/skills/<plugin>/.claude-plugin/plugin.json` ([pluginpack.go:371-387](../../internal/pluginpack/pluginpack.go#L371-L387)),
and a skills contribution carries it ([plugins.go:27-34](../../internal/packload/plugins.go#L27-L34)). The claude
pack's skills contribution has no `from`, so the conventional `skills/` folder applies
([pack.json:47-54](../../packs/claude/pack.json#L47-L54)). That folder does not exist at `026fca67`
(`packs/claude/` holds `briefing/`, `loopholes/`, `derive.lua` and `pack.json`, READ), so step 2 creates it. The pack is `skills_tier: "namespaced"`
([pack.json:190](../../packs/claude/pack.json#L190)), so the tree lands whole at `~/.claude/skills/<plugin>/`
([plugin.go:91](../../internal/hostskills/plugin.go#L91)). The embed takes dot-folders (`all:claude`,
[embed.go:87](../../packs/embed.go#L87)). One writer serves both notches. In a jail `~/.claude/skills` is a
read-only bind of yolo's staging copy ([assemble.go:878-879](../../internal/cli/run/assemble.go#L878-L879), and
`ro` in this jail's `/proc/self/mountinfo`, MEASURED).

That Claude loads a hooks module from a skills-dir plugin is SOURCED: such a plugin *"is watched … saving a file
reloads the hooks module"* (reference:68). Seeing it load in a live session is UNMEASURED.

> [!WARNING]
> **yolo's disclosure cannot see a hooks module, or any plugin hook at its default location.**
> (Fixed 2026-10-03, as build-order step 1 below; this callout records the finding.)
> `pluginpack.Components` reports only the manifest's own fields, on the stated belief that *"a `hooks/` directory
> with no manifest entry is inert to the tools"* ([pluginpack.go:153-166](../../internal/pluginpack/pluginpack.go#L153-L166)).
> For Claude Code 2.1.288 that is false. `claude plugin validate` read the prototype's `hooks/hooks.json` and listed
> its hooks and calls, though its `plugin.json` has no `hooks` field (MEASURED, [Appendix A](#appendix-a-evidence)).
> So such a plugin gets none of yolo's code-running disclosures: not the footprint's
> ([footprint.go:711](../../internal/packload/footprint.go#L711)), not the launch line
> ([packloopholes.go:556-616](../../internal/cli/run/packloopholes.go#L556-L616)), and not the host-apply warning
> ([plugin.go:129-151](../../internal/hostskills/plugin.go#L129-L151)). That is live today for any wrapped plugin,
> a fetched pack's included. At the host, a module's `$.process.run` runs commands as you (types:3285).
>
> The hooks file is not the only default location the manifest walk misses. Claude's changelog SOURCES three more:
> a root `.mcp.json` declares MCP servers (changelog:4489), a plugin may carry *"a default monitors file"*
> (changelog:1640) and a top-level `monitors` key (changelog:5204), and *"Plugins can now ship executables under
> `bin/` and invoke them as bare commands from the Bash tool"* (changelog:5459). `pluginpack.Manifest` reads no
> `monitors` field at all ([pluginpack.go:63-81](../../internal/pluginpack/pluginpack.go#L63-L81)).

**The fix makes yolo's own plugin a standing disclosure.** Once step 1 lands, the claude pack's `yolo` plugin counts
as code-running. The jail-code line covers every selected pack, shipped ones included, and cannot be gated
([packloopholes.go:650-683](../../internal/cli/run/packloopholes.go#L650-L683)), so every claude launch would print
it. The host-apply warning is always-warn by design, so every `yolo host apply` would print it too
([plugin.go:129-151](../../internal/hostskills/plugin.go#L129-L151)). That is correct, since the plugin does run
code, but it is a cost of shipping it (INFERRED from the code; not run).

**The delivered tree is not the prototype.** A namespaced delivery adds yolo's ownership marker,
`"x-yolo-managed-by": "yolo-jail"`, to `plugin.json` ([plugin.go:280-299](../../internal/hostskills/plugin.go#L280-L299),
[tier.go:158](../../internal/hostskills/tier.go#L158)). On a copy of the prototype with that key added,
`claude plugin validate --strict` fails with *"Unknown field 'x-yolo-managed-by'. Claude Code ignores it at load
time."* (MEASURED). Loading is unaffected, but a strict validation in CI must run on the source tree, not on a
delivered one.

**The statusLine on an older Claude, or where modules are off.** The plugin cannot create, draw into or remove a
`statusLine` ([§2.1](#21-where-a-plugin-can-draw)). And yolo cannot tell at compose time whether Claude will load the module, since the rollout
flag is decided remotely. So there is no seamless fallback. What remains:

- **The plugin yields to yolo's own command.** Where the merged settings still carry a `statusLine` naming
  `yolo internal footer`, the plugin draws nothing, so the facts never show twice. The prototype does this
  ([Appendix B](#appendix-b-the-prototype)). Such a value survives in two places:
  - a host file `yolo host apply` filled before;
  - a jail, because the claude settings surface folds your host file in (`readsHost`,
    [pack.json:94](../../packs/claude/pack.json#L94)).
- **yolo does not remove that frozen value.** A dropped default *"is not yolo's output to retire"*, and that rule is
  a standing ruling ([compose.go:278-299](../../internal/agentcfg/compose.go#L278-L299)). The narrow exception, a
  value the target cannot load, does not apply, because the old command still runs
  ([rejectedvalues.go:1-50](../../internal/agentcfg/rejectedvalues.go#L1-L50)). So `yolo host apply` should name
  the next step instead: one line saying the `statusLine` is yolo's old footer command, and that deleting it brings
  Claude's hints back while the plugin draws the segment.
- **Whether to keep shipping the `statusLine` default at all** is [OQ-CM1](#OQ-CM1).

**Tests.** Keep `FOOTER_ARGS` a JSON array in the module, so `internal/footer`'s adapter tests can read and run it
as they do pi's (`TestExtensionAdaptersSetOneYoloStatus`, `TestBridgedRoutesAreMarked`,
[agent-footer.md:256-267](../design/agent-footer.md#L256-L267)). `claude plugin test` runs the engine's own dispatch
under a kit whose bottom hook *"throws, naming its event"* for anything a test does not answer (types:14917). So no
model request can be made. This round's run also opened no network connection. Whether it may run in CI is
[OQ-CM3](#OQ-CM3). One trap: the jail's lazy `claude` launcher puts `--dangerously-skip-permissions` ahead of the
arguments, and `claude plugin test` refuses that spelling (MEASURED). A test must call the real binary, or the
launcher with `YOLO_NO_LAUNCH_FLAGS=1`, which makes it add no flags
([launchflags.go:53](../../internal/entrypoint/launchflags.go#L53); the generated launcher's `_yolo_launch_argv`
returns early on it, READ). CI runs no jail launcher, so there it is the real binary either way. A second trap:
`plugin test` consults the remote rollout flag. 2.1.288 fixed it *"reporting mods as turned off remotely when it
had only read an out-of-date saved setting"* (changelog:93). So a CI run should keep Claude's feature-flag service
off, the traffic-off environment this round used, which makes the default (on) apply.

## 7. Other Claude-specific shims that could follow

### 7.1 The doorbell deliverer

The doorbell is unbuilt ([agent-event-watchers.md](../design/agent-event-watchers.md)). Its Claude deliverer is
designed as a command hook with `asyncRewake`, a 86400 s timeout, re-armed on `Stop`, which finds Claude's pid
through `$PPID` (INFERRED there,
[agent-event-watchers.md §3.4](../design/agent-event-watchers.md#34-deliverers-per-agent)).

As a hooks module it becomes pi's shape:

- one `$.process.spawn` of `yolo notify --follow` for the session's life, killed when the module unloads
  (types:3326-3345);
- each ping queued with `$.prompt.submit`, which starts a turn once the session is idle (reference:132);
- the session id from `$.session.id()`.

One gap: the API has no getter for Claude's own pid, which the reader registration wants (INFERRED from the
nouns, types:2136-3410). A `$.process.run` of `sh -c 'echo $PPID'` might answer it, if Claude spawns the child
itself rather than through a worker (INFERRED). **Verdict: shortlisted.** Name it as Claude's deliverer from 2.1.287 when the doorbell's
build starts, with the `asyncRewake` hook as the fallback.

### 7.2 The jail indicator

`SetupJailIndicator` retitles the kitty tab or tmux pane from the launcher, for any agent
([terminal.go:47-55](../../internal/cli/terminal.go#L47-L55)). It runs outside Claude, so nothing about it can move
into a plugin. A `SessionMode` label `jail` would repeat the footer's notch. **Verdict: rejected, not a move.**

### 7.3 The briefing through `prompt.compose`

A plugin can add a `session`-scoped section to the system prompt (types:3925-3932, 7788-7860). Today the claude
pack composes `~/.claude/CLAUDE.md` ([pack.json:34-46](../../packs/claude/pack.json#L34-L46)). That file works on
every Claude version and shows in `/memory`. A section in the system prompt would be invisible there, would depend
on an early-access API, and buys only dynamic content, which the briefing does not need. **Verdict: rejected.**

## 8. Recommendation

**Move the drawing, keep the logic.** Ship one plugin, named `yolo` so Claude's own prefix reads `yolo:`. It runs
the renderer once per session load and draws one status entry: the model, the effort level and yolo's facts. Keep
it separate from yolo's LSP plugin, so a loader that rejects one cannot take the other down.

Build order:

1. **Fix the plugin disclosure.** `pluginpack.Components` should also report a hooks file at Claude's default
   location, `hooks/hooks.json`, as code-running, whatever the manifest says. Add a test that fails on today's
   code. This is worth doing whatever is ruled below, since it is live for every wrapped plugin today. Cover the
   other default locations in the same change: a root `.mcp.json`, a default monitors file and the `monitors` key,
   and `bin/` executables, each SOURCED in the changelog ([§6](#6-delivery-through-yolos-existing-plugin-mechanism)).
   **Done 2026-10-03**, with `.lsp.json` too. What each report now says is in
   [pack-system.md](../reference/pack-system.md#transparency-at-every-launch).
2. **Ship the plugin dormant beside the `statusLine` default**, in a new `packs/claude/skills/yolo/`. It yields to
   yolo's own command, so nothing changes on screen yet. With step 1 in, every claude launch then discloses it
   ([§6](#6-delivery-through-yolos-existing-plugin-mechanism)). Extend the adapter tests to read its `FOOTER_ARGS`.
3. **One live look,** a human check, since no test may start an agent. In a fresh nested jail with the default
   removed, check:
   - where the status entry sits;
   - whether it reads as a warning, the `⚠` and the warning color the binary suggests;
   - that the hints are back;
   - how long a model id reads;
   - that the effort level appears after the first request.

   If the status entry sits badly or reads as a warning, try `PromptHint.tail` instead, and check it beside a
   user's own `statusLine` too, since that line thins the hint row.
4. **Drop the `statusLine` default**, as [OQ-CM1](#OQ-CM1) rules. `yolo host apply` names the deletion where a frozen
   yolo command remains.
5. **Later, with the cost and failover design:** the bridge publishes per-model state, and the plugin reads it once
   per turn.
6. **When the doorbell is built:** its Claude deliverer as a hooks module ([§7.1](#71-the-doorbell-deliverer)).

## 9. Open Questions

1. 💬 **OQ-CM1: When Claude can load the plugin, does yolo stop shipping the `statusLine` default?**

   Dropping it brings the hints back and ends the per-refresh process. But the footer then depends on an
   early-access API that Anthropic can switch off remotely or a policy can block, it vanishes silently when it
   does, and the binary suggests Claude draws it as a warning ([§5](#5-what-it-cannot-do-and-what-it-costs)). The
   live look in step 3 settles the last point before this is ruled.

   - **A — Plugin only.** Drop the default once the live look passes. The plugin yields to a yolo command it finds.
   - **B — Both.** Keep the default too. The plugin then yields everywhere, and nothing improves.
   - **C — Status line only.** Ship no plugin.

   <!-- vantage: question id=OQ-CM1 leaning="A — plugin only: the jail always runs a Claude new enough, and the hint loss was the one cost DIR-FT2 could not avoid." -->

   _Leaning:_ A. The jail always runs a Claude new enough, and the hint loss was the one cost
   [DIR-FT2](../design/agent-footer.md#DIR-FT2) could not avoid.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-CM2: Does your own `statusLine` still turn yolo's segment off?**

   Today it does, because the two share one setting ([OQ-FT5](../design/agent-footer.md#OQ-FT5)). With the plugin
   they are separate sites, so both can show.

   - **A — Show both.** yolo's entry stays beside your line. To turn it off, you disable the `yolo@skills-dir`
     plugin.
   - **B — Yield to any `statusLine`.** The plugin stays quiet whenever one is set, as today.

   <!-- vantage: question id=OQ-CM2 leaning="A — show both: DIR-FT2's add-never-remove reaches your own line too, and if you call the renderer from your script, drop that call." -->

   _Leaning:_ A. [DIR-FT2](../design/agent-footer.md#DIR-FT2)'s "add, never remove" then covers your own line too.
   If your script calls the renderer, drop that call.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-CM3: May CI run `claude plugin test`?**

   The house rule allows `--version` probes only. `claude plugin validate` is a static read. `claude plugin test`
   starts the Claude binary non-interactively and runs the engine's dispatch under a kit that answers nothing on
   its own, so no model request can be made. This round's run opened no network connection.

   - **A — Allow it** beside `validate`, calling the real binary with Claude's non-essential traffic turned off.
   - **B — Keep the rule.** Test the module under node with a stand-in `$`, as pi's adapter is tested.

   <!-- vantage: question id=OQ-CM3 leaning="A — allow it: it is the one check that runs the real engine's dispatch over an early-access API, and it makes no request." -->

   _Leaning:_ A. It is the one check that runs the real engine's dispatch over an early-access API, and it makes no
   request.

   **Answer:**

   > _(empty — fill in when decided)_

## Fast-moving — verify before building

- **Every line number in the types file** is 2.1.288's. The file is regenerated per build, and the API is early
  access (types:4).
- **The version floor, 2.1.287**, and the rollout flag `tengu_plugin_hooks_modules` with its default on.
- **The status entry's terminal form,** `⚠ <plugin name>: <text>` in the warning color, as a pinned low-priority
  notification. It was read from minified code, so the names `lre`, `aYe`, `IFe`, `cre`, `D1` and `qb` will not
  survive a release.
- **The session header,** `X-Claude-Code-Session-Id` among Claude's default request headers (`pbt`, `xw`).
- **The `statusLine` refresh triggers** and the 300 ms debounce.
- **That a skills-dir plugin is enabled by default,** measured against 2.1.278
  ([OQ-LSP3](../reference/mcp-configuration.md#oq-lsp3)).

## Appendix A: evidence

| Claim | Evidence | Label |
|---|---|---|
| The API's shapes | Types file lines as cited; reference lines as cited | SOURCED |
| Introduced in 2.1.287 | Changelog lines 95-97: *"Added Claude Mods: plugins may now modify deeper behavior"*; 2.1.288 added `$.ui.selection()` (line 5) | SOURCED |
| Status entry's terminal form | Binary strings: `` status:(E,N)=>{…X=`plugin-status-${E}`…H.addNotification({key:X,kind:"event",text:aYe(E,N),priority:"low",pinned:!0})} `` and `` function aYe(e,n){return`${lre(e)}: ${IFe(n)}`} ``; a pinned notice goes to `notifications.pinned`, which `D1` maps through `cre`, whose text arm is `r(n,{color:H,wrap:N,children:[qb," ",E.text]})` with `H=E.color??"warning"`, `N=E.wrap?"wrap":"truncate"`, and `qb="⚠"` | MEASURED |
| The pinned-notice block ignores `statusLine` | Binary strings: `Wat=e(D1,{})` is placed among the prompt footer column's children (`children:[fw,gw,GO,Wat,H6,…]`) with no `statusLine` condition | MEASURED |
| Claude stamps its session id on requests | Binary strings: `var pbt="X-Claude-Code-Session-Id"`, `function xw(){return{"x-app":…,"User-Agent":JO(),[pbt]:q()}}`, the per-request header set `xe={...xw(),...fe,...u0(),…}` beside `x-claude-code-agent-id`, and `F7()`, built from `xw()`, spread into the Bedrock and Vertex clients' `defaultHeaders` | MEASURED |
| The delivered manifest fails `--strict` | A copy of the prototype with `"x-yolo-managed-by": "yolo-jail"` added: `claude plugin validate --strict` printed *"Unknown field 'x-yolo-managed-by'. Claude Code ignores it at load time."* and *"Validation failed (--strict treats warnings as errors)"*, exit 1 | MEASURED |
| `strictKnownMarketplaces` blocks the skills-dir scan | Binary strings: *"Policy-list sentinel for the ~/.claude/skills/ auto-load (@skills-dir plugins). In strictKnownMarketplaces: opt the scan back IN (by default any allowlist blocks it)"* | MEASURED |
| `ui_status` to SDK hosts | Binary strings: `subtype:R("ui_status")`, *"One status line per plugin"* | MEASURED |
| Rollout flag and its default | Binary strings: `var JQ="tengu_plugin_hooks_modules";var pIe=!0`; *"installed plugins' hooks modules not loaded: rollout flag (…) is off"*; the source map naming *"from the default (GrowthBook is off for this session: a third-party provider, or telemetry opted out)"* | MEASURED |
| Workspace-trust gate | Binary strings: *"hooks modules not loaded until workspace trust is accepted: "* | MEASURED |
| Dev-mods consent | Binary strings: *"A mod is code Claude wrote; it runs with your permissions."*, *"Enable for this session"* | MEASURED |
| `statusLine` triggers | Binary strings: `["tokenUsage","permissionMode","vimMode","mainLoopModel","fastMode","effortValue","thinkingEnabled","prStatus"]`; *"Skipping StatusLine command execution - workspace trust not accepted"* | MEASURED |
| Validate reads `hooks/hooks.json` with no manifest field | `claude plugin validate` on the prototype, whose `plugin.json` has no `hooks` field, printed `./register.ts hooks: session.start, turn.step, classic.PostModelSwitch` | MEASURED |
| Prototype validates | `claude plugin validate <dir>`: passed, one warning (no `author`). After adding `author`, `claude plugin validate --strict <dir>`: *"✔ Validation passed"*, exit 0, listing calls `$.process.run (via renderFacts), $.session.model, $.settings.read (via yoloStatusLineIsSet), $.ui.status (via show)` | MEASURED |
| Prototype type-checks | `tsc -p <prototype>/tsconfig.json` (TypeScript 6.0.3; the types file's own suggested options; `include` the types file, `yolo-footer/hooks`, `yolo-footer/tests`): exit 0. Negative control: `$.ui.statuz` in a copy fails with TS2551, exit 2 | MEASURED |
| Prototype tests, no network | `strace -f -e trace=connect` over `/home/agent/.local/bin/claude plugin test <dir>`, with `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, `DISABLE_TELEMETRY=1`, a dead `HTTPS_PROXY`, and `CLAUDE_CODE_USE_BEDROCK` unset: 6 pass, 0 fail, exit 0, and **no `connect()` call** in the trace. Negative control: removing the `show($)` call at `session.start` fails 2 of the tests then present | MEASURED |
| The PATH launcher breaks `plugin test` | `claude plugin test …` through `~/.yolo/bin/launch/claude` printed *"not run from this spelling. Use `claude plugin test [dir]`, with no options before `plugin`"*. The launcher's `LAUNCH_FLAGS=(--dangerously-skip-permissions)` precede the arguments | MEASURED |
| One renderer run's cost | `yolo internal footer` with the prototype's `FOOTER_ARGS`, from node's `execFileSync` with no shell, 20 runs: printed `Bedrock · jail`; median 6.2 ms, min 4.2, max 11.8 | MEASURED |
| No engine-written types in the folder | After validate and test, the prototype folder holds only the files written for it (`fd -H -u`) | MEASURED |
| Skills staging is read-only in a jail | `/proc/self/mountinfo`: `/home/agent/.claude/skills ro` | MEASURED |

## Appendix B: the prototype

**Where it is:** `$YOLO_DURABLE_DIR/claude-ext-prototype/yolo-footer/`, outside the repository, with its
`tsconfig.json` one level up. It is not wired into `packs/` or `internal/`.

**Files:**

- `.claude-plugin/plugin.json`: name `yolo`, version, description, author.
- `hooks/hooks.json`: `{"modules": ["./register.ts"]}`.
- `hooks/register.ts`, which:
  - holds `FOOTER_ARGS` as JSON: the claude pack's command with the template `{yolo.billing} · {yolo.notch}`;
  - on `session.start` in an interactive session (never a `-p` run or the SDK, desktop included), yields to a yolo
    `statusLine` found through `$.settings.read()`,
    else runs the renderer once through `$.process.run` (2 s timeout, stdin closed), reads `$.session.model()`
    and pins `<model> · <facts>`;
  - on each main-loop `turn.step`, redraws when the model or effort changed;
  - on `classic.PostModelSwitch`, redraws with the new model and no effort;
  - draws nothing, and clears any entry, when the renderer fails or yields.
- `tests/footer.test.ts`: six cases under `claude-code/testing`. Every noun is answered by the test's own hooks
  beneath the plugin. The cases:
  - one renderer run at start;
  - a failed renderer;
  - a `-p` run;
  - a model switch;
  - the yield, and no yield to a user's own command;
  - a main-loop request against a subagent's.

**Not in it:** `PromptHint.tail`, a `userConfig` template, a display-name mapping, and anything bridge-related.

## Sources

- **The types**: `/tmp/claude-0/bundled-skills/2.1.288/824b122c92bcb425567d4d6446f64c98/plugin-authoring/types/claude-code.d.ts`,
  20014 lines, written by Claude Code 2.1.288 when its plugin-authoring skill loads. Its header calls it the
  authority. Cited as *types:N*. A per-launch path: regenerate it on a later build rather than trusting these
  lines.
- **The reference**: `reference.md` beside it, 176 lines, the map of the API. Cited as *reference:N*.
- **The examples**: `examples/tool-call.ts`, which the prototype follows for `$.ui.status`, plus `band.tsx` and
  `pane.tsx`.
- **The changelog**: `/home/agent/.claude/cache/changelog.md`, Claude Code's own. Cited as *changelog:N*. Used for
  the version floor.
- **The binary**: `/home/agent/.local/share/claude/versions/2.1.288`, minified, read by quoted string. Used where
  the types are silent: the drawing, the rollout flag and the trust gate.
- [agent-footer.md](../design/agent-footer.md): the built footer design this doc would revise. Every footer
  ruling cited here is there.
- [agent-event-watchers.md](../design/agent-event-watchers.md): the doorbell and its per-agent deliverers ([§7.1](#71-the-doorbell-deliverer)).
- [mcp-configuration.md, OQ-LSP3](../reference/mcp-configuration.md#oq-lsp3): the measured skills-dir plugin
  route this delivery reuses.
