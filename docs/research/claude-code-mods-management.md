---
title: "Installing and managing Claude Code mods in yolo, compared with what yolo built for pi extensions"
date: 2026-10-03
status: in-review
stage: SKETCH
next: "Rule OQ-MOD1 to OQ-MOD3. Meanwhile build step 1, a hooks-module line on the plugin disclosure fix already under way, step 2, a user-guide page, and step 3, the jail delivering a plugin that sits at a pack's root with no skills folder; no ruling holds any of them"
tags: [research, claude, mods, plugins, hooks-modules, marketplaces, packs, pi, extensions, seed-directory]
summary: "yolo needs much less for Claude Code mods than it built for pi extensions. Claude installs, pins, updates and cleans up its own plugins, and a mod installs as a plugin. So yolo should carry Claude's settings, not build a second installer. Most of that already works: your host settings reach every jail, a pack can add a marketplace and enable a plugin, and `yolo config promote` spreads an in-jail install to every jail. What is missing: the plugin disclosure cannot see a mod on main, no user-guide page says any of this, a mod with no skills folder reaches the host but never a jail, a mod's own repository cannot be a `packs` entry without a wrapper, the host's installed plugin files never reach a jail, and an organization policy can silently block yolo's whole plugin route. Three rulings are owed."
---

# Installing and managing Claude Code mods in yolo, compared with what yolo built for pi extensions

**Status:** 2026-10-03, research. Nothing in the tree changes. The maintainer asked: *"Is there anything we need
to add to make the Claude Code mods easier to install and manage, similarly to how we did something for pi
extensions?"*

How this round was done:

- **yolo** was read on `main` at the commit this doc lands on, and the pi side through its four design documents.
- **Claude Code** was read from its public documentation, fetched 2026-10-03, and from what 2.1.288 ships for
  plugin authors ([Sources](#sources)).
- **Run here** with a scratch `CLAUDE_CONFIG_DIR`: `claude plugin validate`, `list`, `details` and
  `marketplace list`, under `strace` ([Appendix A](#appendix-a-evidence)). No agent session was started and no
  model or API was called.
- **A review pass** re-ran `validate`, `details` and `list`, probed a project skills folder, read 2.1.288's strings,
  and ran two throwaway Go tests against the tree, deleted afterwards (also [Appendix A](#appendix-a-evidence)).
- **The earlier round** on mods, [claude-code-extensions-footer.md](claude-code-extensions-footer.md), is the
  starting point. This doc repeats none of its API reading.

**Labels.** **READ**: a file in the tree. **SOURCED**: Claude Code's documentation, help text or shipped
reference. **MEASURED**: run here, or read from a file in this jail. **INFERRED**: reasoned, not checked.
**UNMEASURED**: needs a live Claude session, which no test or agent here may start.

> **In short.** Much less than pi needed. pi's own installer keeps no lockfile, shares nothing across jails and
> updates only when asked, so yolo built a shared store and a refresh around it. Claude's has marketplaces,
> versions, auto-update, dependency installs and cleanup, and a mod installs as a plugin. So yolo should compose Claude's settings and place plugin trees,
> never run Claude's installer. Most of that works today. Three small builds need no ruling: a disclosure line that
> names a hooks module, a user-guide page, and a fix so a jail receives a mod that has no skills folder, as the host
> already does ([G10](#g10-a-mod-with-no-skills-folder-reaches-the-host-but-no-jail)). Three rulings decide the rest: whether a mod's repository can be a
> `packs` entry by itself ([OQ-MOD1](#OQ-MOD1)), whether every jail sees the host's installed plugins read-only
> ([OQ-MOD2](#OQ-MOD2)), and whether a pack may enable a marketplace plugin and let Claude install it
> ([OQ-MOD3](#OQ-MOD3)).

## Terms used here

- **Mod**: Claude Code's own term, *"a plugin that changes how Claude Code looks and behaves"*, made of
  JavaScript or TypeScript event handlers
  ([Mods overview](https://code.claude.com/docs/en/plugins/mods/overview)). Not a command hook in settings, which
  Claude now calls a *settings hook* there.
- **Hooks module**: the code file a mod's `hooks/hooks.json` names under `modules`, as defined in
  [claude-code-extensions-footer.md, *Terms used here*](claude-code-extensions-footer.md#terms-used-here).
- **Plugins root**: Claude's directory of plugin files and records, `~/.claude/plugins` unless
  `CLAUDE_CODE_PLUGIN_CACHE_DIR` moves it
  ([Plugin loading reference](https://code.claude.com/docs/en/plugins/loading#find-plugins-on-disk)).
- **Seed directory**: Claude's term for a read-only, pre-built plugins root named by
  `CLAUDE_CODE_PLUGIN_SEED_DIR`, whose marketplaces Claude registers at startup without cloning
  ([Seed containers and CI](https://code.claude.com/docs/en/plugins/org#seed-containers-and-ci)). Not the plugins
  root itself, which Claude writes.
- **Skills-folder plugin**: a plugin Claude auto-loads from `~/.claude/skills/<name>/`, or from the project's
  `.claude/skills/<name>/` once the folder is trusted, id `<name>@skills-dir`, on by default
  ([loading reference](https://code.claude.com/docs/en/plugins/loading#find-where-a-plugin-came-from)).
  The `~/.claude/skills` form is yolo's only delivery route for a plugin today.
- **Wrapped plugin**: yolo's term, used throughout the pack code, for a Claude plugin carried as pack content: a
  plugin tree in a pack whose `skills_tier` is `namespaced`, written whole into the skills folder
  ([packinitplugin.go:3-11](../../internal/cli/packinitplugin.go#L3-L11)). Not a plugin Claude installed.
- **Notch**: one setting of yolo's confinement dial, as defined in
  [agent-footer.md, *Terms used throughout*](../design/agent-footer.md#terms-used-throughout).

## 1. Side by side

The **Gap?** column links the gap in [§2](#2-the-gaps-and-what-each-would-cost). *Claude's* means Claude Code
already does the job, and yolo only has to carry its settings.

| Capability | pi extensions in yolo today | Claude plugins and mods in yolo today | Claude Code's own mechanism | Gap? |
| :--- | :--- | :--- | :--- | :--- |
| **Declare in a pack** | `config-list` appends to pi's `packages` ([pack-system.md](../reference/pack-system.md#adding-entries-to-an-array-config-list)); `files` places one extension file each ([packs/pi/pack.json:62-81](../../packs/pi/pack.json#L62-L81)) | A plugin tree in a `namespaced` pack ([pluginpack.go:374-382](../../internal/pluginpack/pluginpack.go#L374-L382)), scaffolded by `yolo pack init --from-plugin` ([packinitplugin.go:57-83](../../internal/cli/packinitplugin.go#L57-L83)). A `config-overlay` can add `enabledPlugins` keys (INFERRED from [the overlay rules](../reference/pack-system.md#overlay-rules)) | No pack concept. A mod is a folder with `.claude-plugin/plugin.json` and a `hooks/hooks.json` naming `modules` ([Create a mod](https://code.claude.com/docs/en/plugins/mods/create#write-a-mod-yourself)) | [G3](#g3-a-mods-repository-is-not-a-pack-by-itself), [G10](#g10-a-mod-with-no-skills-folder-reaches-the-host-but-no-jail) |
| **Install from npm** | pi installs `npm:` entries into the shared store; yolo runs no npm ([packs/pi/pack.json:189-200](../../packs/pi/pack.json#L189-L200)) | Nothing | An `npm` plugin source in a marketplace entry. A plugin's own `package.json` dependencies install with `--ignore-scripts` under 60 s ([loading reference](https://code.claude.com/docs/en/plugins/loading#nodejs-package-dependencies)). 2.1.288 also carries `claude plugin install <package>@npm`, straight from a registry and gated per account (*"Installing plugins straight from an npm registry (`<package>@npm`) is not enabled for this account"*); MEASURED: binary strings and `install --help`'s `--registry`, not in the docs fetched | *Claude's* |
| **Install from git** | pi clones `git:` entries per workspace; immutable per-commit trees are built only on a held branch ([pi-git-extension-caching.md](../design/pi-git-extension-caching.md)) | A git pack holding the plugin, a subdirectory allowed, pinned in `packs.lock.json` ([addr.go:7-12](../../internal/packsrc/addr.go#L7-L12); [pack-system.md](../reference/pack-system.md#fetch-refresh-lock)) | `github`, `git` and `git-subdir` plugin sources ([loading reference](https://code.claude.com/docs/en/plugins/loading#how-claude-code-computes-the-version)) | [G3](#g3-a-mods-repository-is-not-a-pack-by-itself) |
| **Install from a local path** | A local path in `packages`, loaded in place ([pack-pi-resources.md](../design/pack-pi-resources.md)) | A `file://` pack holding the plugin. Also, with no yolo code: a plugin in the workspace's own `.claude/skills/<name>/` loads in place as a project skills-folder plugin, since the claude pack pre-accepts the workspace's trust dialog ([packs/claude/pack.json:60-65](../../packs/claude/pack.json#L60-L65)). MEASURED with a scratch config: `list --json` skips such a folder until `.claude.json` trusts it, then shows `<name>@skills-dir`, scope `project`, enabled | `--plugin-dir` and `CLAUDE_CODE_PLUGIN_DIRS` (`@inline`, one session), a `directory` marketplace loaded in place, the user and project skills folders ([loading reference](https://code.claude.com/docs/en/plugins/loading#in-place-and-copied-plugins)) | — |
| **Install from a marketplace** | pi has none | Never by yolo ([packdecl.go:176-186](../../internal/packdecl/packdecl.go#L176-L186)). Your own `/plugin install` in a jail lands in that workspace's `~/.claude` ([packs/claude/pack.json:155-159](../../packs/claude/pack.json#L155-L159)) | `claude plugin install name@marketplace`, copied to `cache/<marketplace>/<plugin>/<version>/` ([loading reference](https://code.claude.com/docs/en/plugins/loading#find-plugins-on-disk)). Scope `user` (the default), `project` or `local` picks only which settings file records `enabledPlugins`; the files land in the plugins root either way ([Install plugins](https://code.claude.com/docs/en/plugins/install#choose-an-install-scope); `install --help`). `/plugin install` opens a panel to choose the scope; the shell verb asks only before running a command a marketplace declares (`install --help`'s `-y`) | *Claude's*; [OQ-MOD3](#OQ-MOD3) |
| **Where it lands, and its scope** | npm: one store per machine. git: per workspace ([packs/pi/pack.json:189-206](../../packs/pi/pack.json#L189-L206)). `files`: a read-only mount per launch | A wrapped plugin: `~/.claude/skills/<plugin>/`, a read-only copy per launch in a jail ([assemble.go:873-879](../../internal/cli/run/assemble.go#L873-L879)). Claude's own installs and `dev-mods`: per workspace | Plugins root per user, `@inline` per session, `~/.claude/dev-mods/<session>/` per session ([Create a mod](https://code.claude.com/docs/en/plugins/mods/create#ask-claude-for-a-mod)) | [G5](#g5-the-hosts-installed-plugins-never-reach-a-jail), [G7](#g7-a-mods-saved-state-is-per-workspace) |
| **Enable** | A file in pi's extensions folder, or an entry in `packages`, is loaded ([pack-pi-resources.md](../design/pack-pi-resources.md)) | A skills-folder plugin is on by default, so yolo writes no `enabledPlugins` ([derive.lua:550-561](../../packs/claude/derive.lua#L550-L561)). Your host `enabledPlugins` fold in ([packs/claude/pack.json:94](../../packs/claude/pack.json#L94); [prism_claude_test.go:179-200](../../internal/entrypoint/prism_claude_test.go#L179-L200)) | `enabledPlugins`, merged key by key over six sources ([loading reference](https://code.claude.com/docs/en/plugins/loading#find-where-a-plugin-is-enabled)) | — |
| **Update and pin** | `pi update --extensions` before launch, hourly ([packs/pi/pack.json:26-35](../../packs/pi/pack.json#L26-L35); [prelaunchrefresh.go:85-92](../../internal/entrypoint/prelaunchrefresh.go#L85-L92)). Pins are pi's grammar; yolo pins nothing | A wrapped plugin moves with its pack: a commit never moves, a branch at most hourly ([pack-system.md](../reference/pack-system.md#fetch-refresh-lock)). A `--from-plugin` copy never updates ([packinitplugin.go:152-155](../../internal/cli/packinitplugin.go#L152-L155)) | Version from the manifest, the entry, or the commit. Auto-update after the first message, on by default for `claude-plugins-official` and marketplaces added from claude.ai, off for third-party ones unless their entry sets `autoUpdate`, and off under background-traffic-off ([loading reference](https://code.claude.com/docs/en/plugins/loading#when-auto-update-runs)) | [G8](#g8-marketplace-mods-do-not-auto-update-under-two-profiles) |
| **Share across workspaces** | One mutable store per machine, ruled a leak on 2026-09-26; the fix is held ([`pi-git-extension-caching.md` OQ-5](../design/pi-git-extension-caching.md#OQ-5)) | Pack trees come from one machine pack store, placed per launch. Claude's own installs are per workspace. The host's `~/.claude/plugins` reaches no jail (READ: nothing mounts it). The claude.ai sync runs per workspace: this workspace's `~/.claude/plugins/synced/` exists (MEASURED) | A read-only seed directory ([Seed containers and CI](https://code.claude.com/docs/en/plugins/org#seed-containers-and-ci)). Plugins turned on for your claude.ai account load as `<name>@synced` in every session signed in with it, not under `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` or a token-only credential ([synced plugins](https://code.claude.com/docs/en/plugins/loading#synced-plugins)) | [G5](#g5-the-hosts-installed-plugins-never-reach-a-jail), [OQ-MOD2](#OQ-MOD2) |
| **Reach: jail (podman)** | Store linked by the `shared_directory` hook ([packhooks.go:69](../../internal/entrypoint/packhooks.go#L69)) | Skills folder mounted read-only (MEASURED: `touch` there fails with `Read-only file system`) | — | — |
| **Reach: `yolo host`** | pi manages the real `~/.pi/agent`, with no pre-launch refresh ([host.go:661-669](../../internal/cli/host.go#L661-L669)) | `yolo host apply` writes a wrapped plugin into the real `~/.claude/skills`, replaced whole on change ([plugin.go:89-184](../../internal/hostskills/plugin.go#L89-L184)). yolo's LSP plugin is jail-only ([mcp-configuration.md:527-528](../reference/mcp-configuration.md#L527-L528)) | The real plugins root | — |
| **Reach: macos-user** | Store mirrored into the workspace sidecar ([darwinhomelayout.go:107-112](../../internal/entrypoint/darwinhomelayout.go#L107-L112)) | Skills copied into the sandbox home, read-only; not yet measured on a Mac ([settings-per-setup.md:475](../../userguide/reference/settings-per-setup.md#L475)) | — | Unmeasured |
| **Reach: Apple Container** | The machine store is a bind mount, its lock a `mkdir` because each jail is its own VM ([pi-extension-lifecycle.md:335-337](../design/pi-extension-lifecycle.md#L335-L337)) | Skills as on podman ([settings-per-setup.md:459](../../userguide/reference/settings-per-setup.md#L459)). A pack `mount` is dropped there ([pack-system.md:1377-1381](../reference/pack-system.md#L1377-L1381)) | — | [OQ-MOD2](#OQ-MOD2) |
| **Remove on deselect** | Gone at the next launch; at the host, inserted entries stay once their owner pack is gone too ([pack-system.md:2209-2216](../reference/pack-system.md#L2209-L2216)) | Gone at the next launch; at the host, a dropped pack's skills are archived behind a prompt ([pack-system.md:2407-2420](../reference/pack-system.md#L2407-L2420)) | `claude plugin uninstall`; old versions go 14 days after they are orphaned ([loading reference](https://code.claude.com/docs/en/plugins/loading#cleanup-of-previous-versions)) | — |
| **Disclosure** | `files` and `config-list` print nothing at launch ([packloopholes.go:200-207](../../internal/cli/run/packloopholes.go#L200-L207)) | A wrapped plugin's declared hooks, MCP and LSP servers print per claim as code ([packloopholes.go:184-199](../../internal/cli/run/packloopholes.go#L184-L199)). On main a hooks module at Claude's default location is invisible ([pluginpack.go:153-166](../../internal/pluginpack/pluginpack.go#L153-L166)). A `config-overlay` that enables a marketplace plugin prints nothing ([packloopholes.go:203](../../internal/cli/run/packloopholes.go#L203)), nor does a plugin in the workspace's `.claude/skills/` | `claude plugin validate` prints `hooks:` and `calls:` ([Mods overview](https://code.claude.com/docs/en/plugins/mods/overview#list-what-a-mod-does-before-you-install-one)) | [G1](#g1-the-disclosure-cannot-see-a-mod-on-main) |
| **Validate and test** | Nothing pi-specific | yolo runs no Claude command on a pack | `claude plugin validate [--strict]`, `claude plugin test` with no session, sign-in or network, `details`, `eval` ([Create a mod](https://code.claude.com/docs/en/plugins/mods/create#check-what-claude-code-reads-from-your-mod)). `claude plugin test` in a folder holding no mod says whether mods can load here at all, though not whether an allowlist drops the skills folder ([Check whether mods can load](https://code.claude.com/docs/en/plugins/mods/troubleshoot#check-whether-mods-can-load)) | [G1](#g1-the-disclosure-cannot-see-a-mod-on-main) |
| **Author a mod** | pi loads a file in place | In a jail, `~/.claude/dev-mods` is writable and kept per workspace (MEASURED). So is the workspace's `.claude/skills/`, which Claude watches like the user skills folder (reference:68). `claude plugin init` scaffolds into the read-only `~/.claude/skills`, so it cannot work there (INFERRED from the measured mount), and it scaffolds a plugin of command hooks, not a mod (reference:9). Nothing documents any of this | Claude writes the mod into `dev-mods` and hot-reloads it after *Enable for this session*; it loads only in that session, and the folder is deleted after `cleanupPeriodDays` ([Create a mod](https://code.claude.com/docs/en/plugins/mods/create#use-the-mod-in-other-sessions)). It lays type declarations and a `tsconfig.json` into that folder and any `--plugin-dir` folder at each load ([reference:40-49](#sources)) | [G2](#g2-no-user-guide-page-says-how-to-bring-a-plugin-or-mod), [G4](#g4---from-plugin-copies-claudes-generated-files) |
| **Version floor and rollout flag** | `node_floor` ([packs/pi/pack.json:19](../../packs/pi/pack.json#L19)) | None. A jail installs the latest Claude ([packs/claude/pack.json:24-27](../../packs/claude/pack.json#L24-L27)) | Mods need 2.1.287 or later ([Mods overview](https://code.claude.com/docs/en/plugins/mods/overview#turn-mods-on-or-off)), behind a remote rollout flag that defaults on ([footer research §5](claude-code-extensions-footer.md#5-what-it-cannot-do-and-what-it-costs)) | [G9](#g9-no-claude-version-floor-a-pack-can-declare) |
| **Managed policy** | None | Nothing reads or reports it | An allowlist without `{"source": "skills-dir"}` stops skills-folder plugins and the mods Claude writes; `disableSideloadFlags` stops `--plugin-dir`, `CLAUDE_CODE_PLUGIN_DIRS` and the mods Claude writes; `allowManagedModsOnly` stops every user mod ([control matrix](https://code.claude.com/docs/en/plugins/org#control-matrix); [admin guide](https://code.claude.com/docs/en/plugins/mods/admin#decide-whether-to-leave-mods-on)). In 2.1.288 any `strictPluginOnlyCustomization` lock also skips the skills-folder scan (MEASURED: minified binary strings; the docs name only plain skills) | [G6](#g6-an-organization-policy-silently-blocks-the-whole-route) |

### 1.1 Where Claude's own machinery already does the job

pi's installer keeps no lockfile and shares nothing across jails, so yolo built around it. Claude's installer
versions, caches and updates on its own, so the pi mechanisms mostly do not carry over:

- **A machine-wide read-write store is ruled out.** It is what got pi's store ruled a leak: one jail's install
  changes what another runs. The rulings are general, not pi's: no winner between jails
  ([pi-git-extension-caching.md OQ-3](../design/pi-git-extension-caching.md#OQ-3)) and no leakage of effects
  between jails ([OQ-4](../design/pi-git-extension-caching.md#OQ-4)). **Verdict: rejected.**
- **A pre-launch refresh is not needed.** Claude updates its own installs, with `claude plugin update` and with
  auto-update where a marketplace has it on, and a wrapped plugin moves with its pack's own fetch. The claude program declares no `refresh`
  ([packs/claude/pack.json:5-29](../../packs/claude/pack.json#L5-L29)), and a `claude plugin update` before every
  launch would be a vendor install verb in a jail. **Verdict: rejected.**
- **A yolo-built seed is ruled out.** Building one means running `claude plugin install` into a scratch plugins
  root: a vendor install verb filling a vendor cache. The placer principle says no vendor install verb runs in a
  jail and no vendor cache is filled ([pi-pack-extensions.md:187-188](../design/pi-pack-extensions.md#L187-L188)),
  and a seed built on the host still breaks the second half. It is the same reason yolo's
  LSP plugin was kept off marketplace ids ([OQ-LSP1](../reference/mcp-configuration.md#oq-lsp1)). **Verdict:
  rejected.**

**What already works, by composing settings or with no yolo code at all** (INFERRED from the code and docs
cited; not run end to end):

1. **Your host's marketplace plugins already follow you into each jail.** `claude plugin marketplace add` writes
   `extraKnownMarketplaces` into your user settings
   ([loading reference](https://code.claude.com/docs/en/plugins/loading#check-which-stage-a-plugin-reached)), and
   a user-scope install writes `enabledPlugins` there
   ([Install plugins](https://code.claude.com/docs/en/plugins/install)). Both reach the jail, because the settings surface folds in your host file
   ([packs/claude/pack.json:94](../../packs/claude/pack.json#L94)). At session start Claude clones a declared
   marketplace it lacks and downloads the enabled plugins it has not cached
   ([loading reference](https://code.claude.com/docs/en/plugins/loading#plugins-and-marketplaces-that-arent-on-disk-at-session-start)).
   Today your host declares none (MEASURED: `enabledPlugins: {}`, no `extraKnownMarketplaces`).
2. **An in-jail install can be spread to every jail.** A `/plugin install` writes the same two keys into the
   jail's settings, which yolo captures per workspace. `yolo config promote claude/settings`, run on the host,
   turns them into declared entries in your local pack, *"which renders in every jail and on the host"*
   (`yolo config --help`, READ).
3. **A pack can do the same.** A `config-overlay` on `claude/settings` may set `managed` keys
   ([overlay rules](../reference/pack-system.md#overlay-rules)). `enabledPlugins` is an object map, so the entry
   merges beside yours. pi needed `config-list` because its `packages` is an array; Claude's map does not
   ([pack-pi-resources.md:298-301](../design/pack-pi-resources.md#L298-L301)). No marketplace repository is
   needed: an `extraKnownMarketplaces` entry with a `settings` source lists plugins inline, each with a `github`
   or `git-subdir` source that a `sha` pins to one commit
   ([`extraKnownMarketplaces`](https://code.claude.com/docs/en/settings-reference#extraknownmarketplaces);
   [plugin sources](https://code.claude.com/docs/en/plugins/marketplace-reference#plugin-sources)). So one mod
   repository becomes one settings entry, pinned, with Claude doing the fetch. The overlay prints nothing at
   launch ([packloopholes.go:203](../../internal/cli/run/packloopholes.go#L203)). Whether a pack should do this
   is [OQ-MOD3](#OQ-MOD3).
4. **A mod kept in the repository already loads.** A plugin in the workspace's own `.claude/skills/<name>/`
   loads as a project skills-folder plugin, because the claude pack pre-accepts the workspace's trust dialog
   ([packs/claude/pack.json:60-65](../../packs/claude/pack.json#L60-L65); MEASURED with a scratch config, see
   [Appendix A](#appendix-a-evidence)). It is writable in a jail, watched for saves (reference:68), survives a
   restart, and reaches host sessions in the same folder and every collaborator who clones it.

What this costs, and what the rest of this doc is about:

- **Each workspace fetches its own copy.** The first session in a fresh workspace runs without the plugin until
  the background fetch lands and you run `/reload-plugins` or start again (SOURCED, same section). See
  [G5](#g5-the-hosts-installed-plugins-never-reach-a-jail).
- **A jail has no git credentials of yours**, so a private marketplace cannot be cloned there (INFERRED from
  AGENTS.md's credential boundary).
- **A host marketplace added from a local `directory` names a host path**, which a jail does not have (INFERRED).
- **Plugins turned on for your claude.ai account** already reach every jail on a subscription login, as
  `<name>@synced`, each workspace downloading its own copy (SOURCED,
  [synced plugins](https://code.claude.com/docs/en/plugins/loading#synced-plugins)). Not on Bedrock, or on the
  `codex` and routed profiles, which set `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` (INFERRED from the same page
  and [G8](#g8-marketplace-mods-do-not-auto-update-under-two-profiles)).

## 2. The gaps, and what each would cost

### G1. The disclosure cannot see a mod on main

**Today.** `pluginpack.Components` reports only what a plugin's manifest declares, on the belief that a
`hooks/` directory with no manifest entry is inert ([pluginpack.go:153-166](../../internal/pluginpack/pluginpack.go#L153-L166)).
A mod is exactly that shape: Claude's own tutorial gives `plugin.json` no `hooks` field
([Create a mod](https://code.claude.com/docs/en/plugins/mods/create#write-a-mod-yourself)). So on main a
wrapped mod gets no code-running line at launch, in `yolo pack footprint`, or at `yolo host apply`, and a
`flat` delivery drops its module without naming it ([plugin.go:186-200](../../internal/hostskills/plugin.go#L186-L200)).

**Under way.** The fix that reports Claude's default locations is the footer research's first build step, which
[the roadmap](../plans/roadmap.md#the-queue) reports under way. It is not on main when this is written. It
reports a hooks file under the same words as a command hook, *"runs code at agent lifecycle events"* (READ, in
the change waiting to land).

**What yolo would add.** One more line when `hooks/hooks.json` names `modules`: *runs a hooks module, code
inside Claude Code with your permissions*, followed by the next step, `claude plugin validate <dir>`, which
lists what the module calls. Claude's own `details` does not count a module at all (MEASURED:
`Hooks (0)` for a plugin whose only component is one), so yolo's line would be the only inventory that does.

- **Smallest version:** read one key from a file the fix already reads, and one test that fails without it.
- **Cost:** small. `pluginpack` already reads Claude's plugin layout, so this adds no agent knowledge to core.
- **Verdict: build now**, in or right after the fix that is under way.

### G2. No user-guide page says how to bring a plugin or mod

**Today.** The user guide mentions plugins twice, both about LSP (MEASURED: `rg -c -i plugin userguide`).
`yolo pack init --from-plugin` is documented only in `yolo pack --help`.

**What yolo would add.** One page, *Use a Claude Code plugin or mod*:

- **Four routes:**
  - a pack that wraps the plugin, with the plugin under the pack's `skills/` until
    [G10](#g10-a-mod-with-no-skills-folder-reaches-the-host-but-no-jail) lands;
  - Claude's own `/plugin install`, then `yolo config promote` to reach every jail;
  - a mod for one repository, in its `.claude/skills/<name>/`
    ([§1.1](#11-where-claudes-own-machinery-already-does-the-job), item 4);
  - for developing one, `claude --plugin-dir` on a folder under `/workspace`, or the `dev-mods` folder Claude
    writes into, which lasts one session.
- **Where things live:** what is per workspace and what survives a restart.
- **The traps:** the read-only skills folder, the policy in [G6](#g6-an-organization-policy-silently-blocks-the-whole-route),
  and the per-workspace state in [G7](#g7-a-mods-saved-state-is-per-workspace).
- **Keeping a mod Claude wrote:** wrap its folder from the host path of the jail's `~/.claude`, which on podman
  is `<workspace>/.yolo/home/claude/` ([AGENTS.md:527-530](../../AGENTS.md#L527-L530)). `--from-plugin`
  already does this.

**Verdict: build now.** Docs only, and it is how a user finds any of the routes above.

### G3. A mod's repository is not a pack by itself

**Today.** A repository with `.claude-plugin/plugin.json` at its root and no `pack.json` can be listed in
`packs`. It arrives the way any manifest-less pack does: its content goes to every skills folder the selected
packs name ([mergedest.go:1-13](../../internal/packload/mergedest.go#L1-L13)), at the default `flat` tier. A
flat delivery carries skills only ([plugin.go:21-25](../../internal/hostskills/plugin.go#L21-L25)), so the
hooks module never arrives. A mod in Claude's own shape has no `skills/` folder at all, and then a jail
receives nothing of it ([G10](#g10-a-mod-with-no-skills-folder-reaches-the-host-but-no-jail)).

To get it whole you need a `pack.json` saying `"skills_tier": "namespaced"`. In your own repository the pack
root may itself be the plugin ([pluginpack.go:374-382](../../internal/pluginpack/pluginpack.go#L374-L382)), but
until G10 is fixed that shape reaches only the host. For someone else's, it means `--from-plugin`, which copies
the tree under the pack's `skills/`, the one layout both notches deliver, and that copy never updates on its own
([packinitplugin.go:13-18](../../internal/cli/packinitplugin.go#L13-L18)).

**Claude's own way to make a mod one entry**, with no yolo build: an inline `settings` marketplace naming the
repository at a `sha`, written by a `config-overlay`
([§1.1](#11-where-claudes-own-machinery-already-does-the-job), item 3). It differs from G3 in who fetches. Claude
fetches per workspace, needs the network and, for a private repository, git credentials the jail lacks, and
installs the plugin's dependencies. yolo fetches once on the host, pins in `packs.lock.json`, and, once G1 lands,
names the module at launch. Which one a pack may use is [OQ-MOD3](#OQ-MOD3).

**Why it is not a one-line fix.** The tier is the pack's positive opt-in, by a 2026-08-05 ruling that removed
yolo guessing on a pack's behalf ([packdecl.go:69-91](../../internal/packdecl/packdecl.go#L69-L91)).

**What yolo would add, if [OQ-MOD1](#OQ-MOD1) allows it.** A manifest-less pack whose root carries
`.claude-plugin/plugin.json` is read as `namespaced`. Then a mod is one line, pinned like any pack:
`"git+https://github.com/<owner>/<repo>//<path>?ref=<commit>"`.

- **Smallest version:** one inference where the pack's tier is resolved, read by both notches through the one
  writer, and a test through a staged manifest-less pack. It needs G10 first, or a mod with no `skills/` still
  reaches no jail.
- **Cost:** small in code. The question is the ruling.
- **Verdict: build after [OQ-MOD1](#OQ-MOD1).** It is the pi `config-list` line's equivalent: one entry per extension.

### G4. --from-plugin copies Claude's generated files

**Today.** Claude lays `.claude-plugin/types/` and, when the mod has none, a root `tsconfig.json` extending it,
into the `dev-mods` folder and any `--plugin-dir` folder at each load ([reference:40-49](#sources); also
[Create a mod](https://code.claude.com/docs/en/plugins/mods/create#get-the-types-for-your-build)). They are
written for one Claude build. `--from-plugin` skips only `.git`
([packinitplugin.go:175-181](../../internal/cli/packinitplugin.go#L175-L181)), so wrapping a mod Claude wrote
copies both (INFERRED).

- **Smallest version:** skip `.claude-plugin/types/`, and a root `tsconfig.json` whose `extends` points into it.
- **Cost:** small, plus one more Claude-layout fact in core, beside the `.claude-plugin/` it already reads.
- **Verdict: build with G3, low priority.** A stale copy only misleads `tsc`. Claude rewrites the types folder
  wherever the folder is loaded with `--plugin-dir`, and adds the root `tsconfig.json` only where there is none.

### G5. The host's installed plugins never reach a jail

**Today.** Your host's `enabledPlugins` reach every jail, but the plugin files do not: nothing mounts the host's
plugins root (READ), so each workspace fetches its own ([§1.1](#11-where-claudes-own-machinery-already-does-the-job)).
That is the closest thing to pi's problem: a mod installed once should be usable everywhere. Claude's claude.ai
sync already covers the plugins you turn on for your account, on a subscription login only
([§1.1](#11-where-claudes-own-machinery-already-does-the-job)).

**What yolo would add, if [OQ-MOD2](#OQ-MOD2) allows it.** Hand Claude the host's plugins root as a seed
directory, read-only. A seed has the plugins root's own layout and *"can be mounted at a different path than
you built it at"*; Claude never writes it and turns its auto-update off
([Seed containers and CI](https://code.claude.com/docs/en/plugins/org#seed-containers-and-ci)). Its plugins load
only where `enabledPlugins` names them, which your host settings already do.

- **Smallest version:** declared by the claude pack, with no new kind. Three `mount` entries carry the seed's
  parts: `cache/`, `marketplaces/` and `known_marketplaces.json`. One `env` entry sets
  `CLAUDE_CODE_PLUGIN_SEED_DIR` ([`reads-host` and `mount`](../reference/pack-system.md#reads-host-and-mount)).
  **Never `data/` or `store/`**, which hold plugins' own saved data.
- **Cost:**
  - one disclosure line on every claude launch;
  - Apple Container drops a `mount` ([pack-system.md:1377-1381](../reference/pack-system.md#L1377-L1381)), so it
    keeps per-workspace fetches;
  - macos-user is unmeasured;
  - what a jail runs now follows your host's installs. A host update writes a new version folder and keeps the old
    one 14 days, so a running session is not broken (SOURCED,
    [cleanup](https://code.claude.com/docs/en/plugins/loading#cleanup-of-previous-versions));
  - the host's cache holds each plugin's `node_modules` as installed for the host, so a macOS host's copy may not
    run in a Linux jail. The one host-cache alias yolo has is gated on matching OS and architecture for a
    like reason ([AGENTS.md:396-397](../../AGENTS.md#L396-L397); INFERRED for plugins);
  - an organization allowlist checks a seed marketplace's source too
    ([seed rules](https://code.claude.com/docs/en/plugins/org#seed-containers-and-ci)), so the policy in
    [G6](#g6-an-organization-policy-silently-blocks-the-whole-route) stops a seed as well;
  - it leans on Claude's cache layout, which the 2026-09-19 plugin ruling kept as its one alternative and argued
    against depending on ([pi-pack-extensions.md §7.2](../design/pi-pack-extensions.md#72-the-decision)). Mounting
    the host's own copy reproduces nothing, which is the difference this ruling has to weigh.
- **Check before building.** Claude's static commands never read a seed (MEASURED: `list`, `details` and
  `marketplace list` touched no file under one), and Claude's own check is a `claude -p` run. So one human
  session must confirm that a seed mounted at another path loads a host-enabled plugin.
- **Verdict: the one new capability worth a ruling.**

### G6. An organization policy silently blocks the whole route

**Today.** With a Team or Enterprise login, Claude applies its organization's server-delivered policy
([server-managed settings](https://code.claude.com/docs/en/server-managed-settings#platform-availability)). Any
`strictKnownMarketplaces` allowlist without `{"source": "skills-dir"}` stops every skills-folder plugin
([keep skills-directory plugins loading](https://code.claude.com/docs/en/plugins/org#keep-skills-directory-plugins-loading)),
the mods Claude writes too ([admin guide](https://code.claude.com/docs/en/plugins/mods/admin#decide-whether-to-leave-mods-on)).
That is every plugin yolo delivers: wrapped plugins, yolo's LSP plugin and the planned footer plugin.

- **Seen here.** The policy cached in this jail's `~/.claude/remote-settings.json` sets
  `strictKnownMarketplaces: []` (MEASURED). An empty allowlist blocks every marketplace source, the official one
  included ([control matrix](https://code.claude.com/docs/en/plugins/org#control-matrix)), and a seed's
  marketplaces with it. So where it applies, no marketplace, seed, skills-folder or mods-folder route
  loads a mod, [OQ-MOD2](#OQ-MOD2) and [OQ-MOD3](#OQ-MOD3) included; `--plugin-dir` and `CLAUDE_CODE_PLUGIN_DIRS`
  still would, since the policy sets no `disableSideloadFlags` (SOURCED for each rule; the combination INFERRED;
  whether claude.ai-synced plugins pass it is unread).
- **A second key does the same.** In 2.1.288 the skills-folder scan also returns nothing when
  `strictPluginOnlyCustomization` is `true` or names any of `skills`, `agents`, `hooks` or `mcp` (MEASURED: minified
  binary strings, the scan's guard reading that key; the docs say only that plain skills stop).
- **When it applies.** A launch that exports a `CLAUDE_CODE_USE_*` variable or a non-default
  `ANTHROPIC_BASE_URL` skips the fetch (SOURCED, same page). This jail runs on Bedrock, so the cached copy should
  not apply to it (INFERRED). A subscription launch in this workspace would likely load none of yolo's plugins
  (INFERRED, not seen live).
- **What the user sees.** Only an Errors row in `/plugin`.

**A correction to two documents, made in place.** [The footer research §5](claude-code-extensions-footer.md#5-what-it-cannot-do-and-what-it-costs)
and [OQ-LSP3's falsifier](../reference/mcp-configuration.md#oq-lsp3) said `disableSideloadFlags` stops the skills
folder. Claude's control matrix says it rejects `--plugin-dir`, `--plugin-url`, `--agents`, the SDK's `plugins`
option, `--mcp-config` and `CLAUDE_CODE_PLUGIN_DIRS`, and does not list the skills folder
([control matrix](https://code.claude.com/docs/en/plugins/org#control-matrix), SOURCED). The binary agrees: the
skills-folder scan's guard reads the allowlist, the blocklist and `strictPluginOnlyCustomization`, never
`disableSideloadFlags`, which does refuse the mods folder's hot reload (MEASURED, binary strings). Both lines now
say so.

**What yolo would add.** A notice the claude pack declares: when the cached policy, or a managed settings file at
the host, sets an allowlist without the skills-folder entry, say that yolo's plugins will not load, and that the
organization's admin is who can change it.

- **Cost:** small, once packs can declare such a check at all. That waits on
  [the agent directory map](../design/agent-directory-map.md) and
  [pack-declared file diagnostics](../design/pack-declared-file-diagnostics.md).
- **Rejected: switching to `CLAUDE_CODE_PLUGIN_DIRS`**, which an allowlist does not cover
  ([plan for what managed settings can't enforce](https://code.claude.com/docs/en/plugins/org#plan-for-what-managed-settings-cant-enforce)).
  Choosing a route because a policy misses it works against the organization's stated choice.
- **Verdict: build once that design is ruled; until then the user-guide page names it.**

### G7. A mod's saved state is per workspace

Claude describes `$.store` as *"a key-value store that every session on the machine shares"*
([Mods reference](https://code.claude.com/docs/en/plugins/mods/reference)). In a jail it lives in the
workspace's `~/.claude/plugins/store/` (MEASURED: a built-in mod's store file there), as does
`${CLAUDE_PLUGIN_DATA}` ([loading reference](https://code.claude.com/docs/en/plugins/loading#find-plugins-on-disk)).
So a mod's saved state differs per workspace and from the host. Its options do not all follow: non-secret ones
sit in user settings under `pluginConfigs`, which the host's settings feed, and secret ones in
`~/.claude/.credentials.json` ([`pluginConfigs`](https://code.claude.com/docs/en/settings-reference#pluginconfigs)),
which the claude pack shares across workspaces ([packs/claude/pack.json:160-171](../../packs/claude/pack.json#L160-L171);
INFERRED that a mod's secret lands there in a jail).

**Verdict: keep it, and document it.** Sharing it read-write is the leak [§1.1](#11-where-claudes-own-machinery-already-does-the-job) rules out.

### G8. Marketplace mods do not auto-update under two profiles

yolo turns Claude's background traffic off on the `codex` profile and on routed providers
([derive.lua:753](../../packs/claude/derive.lua#L753), [:903](../../packs/claude/derive.lua#L903)). That also
turns off plugin auto-update unless `FORCE_AUTOUPDATE_PLUGINS=1` is set
([loading reference](https://code.claude.com/docs/en/plugins/loading#when-auto-update-runs)). A wrapped plugin is
unaffected, and a third-party marketplace auto-updates on no profile unless its entry sets `autoUpdate` (same
section).

**Verdict: not now.** The plugin still updates the next time the workspace starts on another profile. Name it on
the guide page, and set the variable in the claude derive only if someone hits it.

### G9. No Claude version floor a pack can declare

Mods need 2.1.287 or later. `node_floor` is the only floor a pack declares. A jail installs the latest Claude,
and `yolo host` runs the floor's own copy ([host.go:661-669](../../internal/cli/host.go#L661-L669)), so both
notches run a recent one.

**Verdict: not now.** Revisit if a mod ships for a notch that can run an older Claude.

### G10. A mod with no skills folder reaches the host but no jail

**Today.** A plugin at a pack's root rides the pack's skills sources into each destination
([jailskills.go:24-53](../../internal/cli/run/jailskills.go#L24-L53)). A pack whose conventional `skills/` folder
is absent has no source and no warning ([governance.go:362-373](../../internal/packload/governance.go#L362-L373)),
so the jail's plan has no layer for it ([skills.go:166-193](../../internal/jailcontent/skills.go#L166-L193)). The
host writes the plugin before it looks at sources at all
([compose.go:897-917](../../internal/hostskills/compose.go#L897-L917)). A mod in Claude's own shape, a manifest and
`hooks/`, is exactly that pack.

MEASURED with throwaway tests against this commit's tree, for a root plugin holding only `.claude-plugin/plugin.json`
and `hooks/`: with no `pack.json`, and with one saying `"skills_tier": "namespaced"` and a `skills` destination,
the jail's sources come out empty and no plugin rides them. The host's composition of the second writes
`first-mod/` whole, `hooks/register.js` included. Adding a `skills/` folder makes the jail deliver it. The
disclosure reads the pack's plugins whether or not they are delivered, so once G1 lands a jail would name a module
it never received (INFERRED).

- **Smallest version:** carry a pack's root plugins to the destinations its skills reach, with or without a
  source, and a test that stages such a pack and finds the module in the jail's skills folder.
- **Cost:** small. Both notches already share the writer.
- **Verdict: build now.** The two notches delivering one pack differently is what the 2026-09-28 parity ruling
  removed ([OQ-NC11](../plans/notch-convergence.md#OQ-NC11)), so no new ruling is needed. G3 depends on it.

## 3. Recommendation

**Compose, never install.** yolo carries Claude's settings and places plugin trees. Claude's own marketplace,
`enabledPlugins` and auto-update do the installing, pinning and updating. Everything Claude-specific below is
declared by `packs/claude`, except G1, G3, G4 and G10, which extend the plugin-layout reading and delivery core
already does in `pluginpack`, `hostskills` and `yolo pack init`.

Build order:

1. **G1, the hooks-module line**, on the disclosure fix that is under way. No ruling.
2. **G2, the user-guide page.** No ruling.
3. **G10, the jail delivering a root plugin with no skills folder.** No ruling: parity is ruled.
4. **Rule [OQ-MOD1](#OQ-MOD1)**, then build G3 and G4: a mod's repository becomes one `packs` line.
5. **Rule [OQ-MOD2](#OQ-MOD2).** If yes, one human session confirms a relocated seed loads, then the claude pack
   gains its mounts and variable, with a test through the shipped manifest and a nested-jail check.
6. **Rule [OQ-MOD3](#OQ-MOD3)**, then say on the guide page whether a pack may enable a marketplace plugin.
7. **G6, the policy notice**, once packs can declare a check.

Not now: G7, G8 and G9, each named on the guide page instead.

## 4. Open Questions

1. 💬 **OQ-MOD1: Is a plugin's own manifest at a pack's root enough to deliver the plugin whole?**

   A mod's repository has `.claude-plugin/plugin.json` and no `pack.json`. Listed in `packs`, it arrives
   flat: skills only, no hooks module ([G3](#g3-a-mods-repository-is-not-a-pack-by-itself)), and in a jail
   nothing at all when it has no `skills/` folder, which G10 fixes under every option. By the 2026-08-05 ruling,
   the tier is the pack's own opt-in.

   - **A — The manifest is the opt-in.** A pack with no `pack.json` whose root is a plugin is delivered whole,
     at every skills folder the selected packs name.
   - **B — The config line says so.** A `packs` entry may carry `skills_tier`.
   - **C — Keep the wrapper.** `--from-plugin`, or a `pack.json` you write.

   <!-- vantage: question id=OQ-MOD1 leaning="A — the manifest is the opt-in: the plugin's author already said what it is, and no other reading delivers it." -->

   _Leaning:_ A. The plugin's author already said what it is, and no other reading delivers it.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-MOD2: Do claude jails see the plugins installed on the host, read-only?**

   Claude can read a seed directory: a read-only plugins folder it never writes, laid out like the host's
   plugin cache. Your host `enabledPlugins` reach every jail but the files do not, so each workspace fetches
   its own. A 2026-09-19 ruling argued against leaning on Claude's cache layout, and the allowlist this jail
   caches stops a seed too ([G5](#g5-the-hosts-installed-plugins-never-reach-a-jail)).

   - **A — Every claude jail.** The claude pack mounts the host's cache, marketplaces and their record, never
     `data/` or `store/`, and the launch names it.
   - **B — When you ask.** The same, behind a setting in your user config.
   - **C — No.** Each workspace keeps fetching its own.

   <!-- vantage: question id=OQ-MOD2 leaning="A — every claude jail: it finishes what folding in your host settings started, and a jail cannot change it." -->

   _Leaning:_ A. It finishes what folding in your host settings started, and a jail cannot change it.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-MOD3: May a pack enable a marketplace plugin and leave the install to Claude?**

   A `config-overlay` on `claude/settings` can add a marketplace, even an inline one pinned by `sha`, and an
   `enabledPlugins` entry today; Claude then fetches the plugin at session start. The placer principle says no
   vendor install verb runs in a jail and no vendor cache is filled. An overlay prints nothing at launch, so the
   ruling also says whether enabling a mod gets a line
   ([§1.1](#11-where-claudes-own-machinery-already-does-the-job), item 3).

   - **A — Yes, for any pack.** yolo writes a setting and runs nothing; Claude pins and updates.
   - **B — Only your own config and local pack**, never a shipped or fetched pack.
   - **C — No.** A pack ships the plugin's tree.

   <!-- vantage: question id=OQ-MOD3 leaning="A — yes, for any pack: it keeps Claude's versions, updates and dependency installs, and yolo still runs nothing." -->

   _Leaning:_ A. It keeps Claude's versions, updates and dependency installs, and yolo still runs nothing.

   **Answer:**

   > _(empty — fill in when decided)_

## Fast-moving — verify before building

- **Claude Code's documentation pages**, fetched 2026-10-03 against 2.1.288. The mods pages are new with 2.1.287.
- **The seed directory's rules**: read-only, forced auto-update off, re-mountable at another path, overriding a
  same-named marketplace. All SOURCED; none seen live.
- **Which folders Claude lays types into**: the mods folder and `--plugin-dir` or `CLAUDE_CODE_PLUGIN_DIRS`
  folders, per the 2.1.288 reference; not the skills folder.
- **Policy reach**: which logins fetch server-managed settings, and that a third-party provider skips them.
- **`claude plugin list` and `details`** read no seed and register no settings-declared marketplace in 2.1.288.
- **Binary-only facts**: `<package>@npm` installs, and `strictPluginOnlyCustomization` skipping the skills-folder
  scan, are read from 2.1.288's minified strings and are in no page fetched.

## Appendix A: evidence

Every Claude run used `/home/agent/.local/share/claude/versions/2.1.288` directly (not the jail's launcher), a scratch
`CLAUDE_CONFIG_DIR` under `/tmp`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, `DISABLE_TELEMETRY=1`, a dead
proxy at `127.0.0.1:9`, and no provider variable.

| Claim | Evidence | Label |
| :--- | :--- | :--- |
| A settings-declared `directory` marketplace is not registered by the static commands | `extraKnownMarketplaces` naming a local marketplace plus `enabledPlugins` for its plugin: `claude plugin list --json` printed `[]`, nothing was written under the config's `plugins/`, and `claude plugin details probe@yolo-probe` printed *"Marketplace 'yolo-probe' not found in configuration"* | MEASURED |
| `CLAUDE_CODE_PLUGIN_DIRS` loads a folder in place, from the process env or the settings `env` block | Both ways, `list --json` showed `probe@inline`, `"scope": "session"`, `installPath` the folder itself | MEASURED |
| A skills-folder plugin is listed and enabled | A copy under the scratch config's `skills/`: `probe2@skills-dir`, `"scope": "user"`, `"enabled": true` | MEASURED |
| Claude's inventory does not count a hooks module | `claude plugin details probe@inline` printed `Hooks (0)`; `claude plugin validate` on the same folder printed `hooks: session.start` and `calls: $.ui.status` | MEASURED |
| The static commands never read a seed | A hand-built seed named by `CLAUDE_CODE_PLUGIN_SEED_DIR`: `list --json` printed `[]`, and `strace -e trace=file` over it showed no path under the seed; `details` and `marketplace list` did not see its marketplace | MEASURED |
| The seed is still in 2.1.288 | `CLAUDE_CODE_PLUGIN_SEED_DIR` occurs 6 times in the binary, beside *"registered from the read-only seed directory"* | MEASURED |
| Nothing left the jail | Each `list` attempted three connections, all to the dead proxy, and none elsewhere (`strace -e trace=connect`) | MEASURED |
| The jail's skills folder is read-only | `touch` and `mkdir` under `~/.claude/skills` failed with `Read-only file system`; `/proc/self/mountinfo` shows it `ro` | MEASURED |
| A mod's store is per workspace | `~/.claude/plugins/store/` in this workspace's `.claude` holds a built-in mod's JSON file | MEASURED |
| `dev-mods` is writable here | `~/.claude/dev-mods/<this session>/` exists, empty | MEASURED |
| The cached organization policy | `~/.claude/remote-settings.json`, written 2026-10-02: `strictKnownMarketplaces: []`; no `disableSideloadFlags`, `blockedMarketplaces` or `strictPluginOnlyCustomization` | MEASURED |
| This launch is on Bedrock | The jail's `~/.claude/settings.json` `env` holds `CLAUDE_CODE_USE_BEDROCK` | MEASURED |
| Your host declares no marketplace plugins | `/ctx/host-claude/settings.json`: `enabledPlugins: {}`, no `extraKnownMarketplaces` | MEASURED |
| The plugin verbs | `claude plugin --help` lists `configure`, `details`, `disable`, `enable`, `eval`, `init`, `install`, `list`, `marketplace`, `prune`, `tag`, `test`, `uninstall`, `update`, `validate` | MEASURED |
| The user guide has no plugin page | `rg -c -i plugin userguide`: two files, `settings-per-setup.md` and `guides/mcp-and-lsp.md`, both about LSP | MEASURED |
| A project skills-folder plugin loads once the folder is trusted | A plugin under a scratch git repository's `.claude/skills/projmod/`: `list --json` printed one `(suppressed)@skills-dir` row with a *"not trusted"* note, then, with `projects.<repo>.hasTrustDialogAccepted: true` in the scratch `.claude.json`, `projmod@skills-dir`, `"scope": "project"`, `"enabled": true` | MEASURED |
| A root plugin with no `skills/` reaches the host but no jail | Throwaway Go tests, deleted after the run: `jailSkillSources` over `SkillsSources()` gave zero sources and zero riding plugins for such a pack, with and without a `namespaced` `pack.json`, and one of each once `skills/` existed; `ComposeHostSkills` then `ComposeInto` on the `pack.json` case wrote `first-mod/` with `hooks/hooks.json` and `hooks/register.js` | MEASURED |
| `disableSideloadFlags` does not gate the skills folder | 2.1.288 strings: the skills-folder scan returns nothing on `strictPluginOnlyCustomization` or when the allowlist or blocklist excludes `skills-dir`; the mods folder's refusal list has both `sideload_disabled` and `local_dirs_blocked` | MEASURED |
| The claude.ai sync has run in this workspace | `~/.claude/plugins/synced/` holds one bucket, dated 2026-09-17 | MEASURED |

## Sources

- **Claude Code's documentation**, fetched 2026-10-03, the pages most used:
  - [Plugin loading reference](https://code.claude.com/docs/en/plugins/loading): origins, enabling, the plugins
    root, versions, auto-update and name conflicts. The backbone of [§1](#1-side-by-side).
  - [Manage plugins for your organization](https://code.claude.com/docs/en/plugins/org): the seed directory, the
    control matrix and the allowlist rules behind [G5](#g5-the-hosts-installed-plugins-never-reach-a-jail) and
    [G6](#g6-an-organization-policy-silently-blocks-the-whole-route).
  - [Mods overview](https://code.claude.com/docs/en/plugins/mods/overview),
    [Create a mod](https://code.claude.com/docs/en/plugins/mods/create) and
    [Manage mods for your organization](https://code.claude.com/docs/en/plugins/mods/admin): what a mod is, the
    mods folder, the version floor and the mod policy keys.
  - [Configure server-managed settings](https://code.claude.com/docs/en/server-managed-settings): which logins
    fetch the organization's policy.
  - [Environment variables](https://code.claude.com/docs/en/env-vars): `CLAUDE_CODE_PLUGIN_DIRS`,
    `CLAUDE_CODE_PLUGIN_CACHE_DIR` and `CLAUDE_CODE_PLUGIN_SEED_DIR`.
- **The plugin-authoring reference** shipped with 2.1.288, `reference.md` beside the types in the per-launch
  folder `/tmp/claude-0/bundled-skills/2.1.288/<build>/plugin-authoring/`. Cited as *reference:N*; lines 40-49
  name the folders Claude lays types into. Regenerate it on a later build rather than trusting the lines.
- [claude-code-extensions-footer.md](claude-code-extensions-footer.md): the API reading, the version floor and the
  rollout flag this round builds on.
- [pi-extension-lifecycle.md](../design/pi-extension-lifecycle.md),
  [pi-git-extension-caching.md](../design/pi-git-extension-caching.md),
  [pack-pi-resources.md](../design/pack-pi-resources.md) and
  [pi-pack-extensions.md](../design/pi-pack-extensions.md): what yolo built and ruled for pi, the baseline of
  [§1](#1-side-by-side).
- [agent-config-distribution.md](agent-config-distribution.md#L247-L270) and
  [agent-config-packs.md](../plans/agent-config-packs.md#L198-L216): the seed directory as first read against
  2.1.220, planned as a bridge and never built.
