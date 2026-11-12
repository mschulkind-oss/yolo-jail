---
title: "Pack manifests: the same rules, shown on the maintainer's own packs"
date: 2026-10-25
status: accepted
stage: BUILT
next: "Nothing is owed: the shipped loophole manifests took their TOML form on 2026-10-30 (RC-D28)"
tags: [packs, manifest, toml, conventions, design]
summary: "Each of the maintainer's twelve packs, before and after: a manifest-less folder for the simple ones, a short TOML manifest for the rest, models declared once, and flags that add instead of replace. Every section is ruled. Part of the readable-config redesign."
---

# Pack manifests: the same rules, shown on the maintainer's own packs

**Status:** accepted 2026-10-25 (the maintainer, after reading every document: *"it all sounds
good and I want to move forward"*). Built by 2026-10-30, and every shipped pack is a `pack.toml`, a model said once
(`every_model`, `twin_of`, `compact_at`) included ([RC-D29](readable-config.md#RC-D29)), and the shipped loophole
manifests are `loophole.toml` since `9839d26d3` ([RC-D28](readable-config.md#RC-D28)); the [ledger](readable-config.md#decision-ledger) names each. A legacy
`pack.json` is still read for a few releases, and `yolo pack migrate` converts one. The "today"
manifests are the maintainer's twelve
packs, read on 2026-10-24. MEASURED (2026-11-12, in the tree): no `pack.json` and no loophole
`manifest.jsonc` is left under `packs/`; no runtime observation is recorded in this doc.

**How to read this.** [§1](#1-a-small-pack-luna-worker) to [§4](#4-flags-that-add-and-a-model-said-once)
are the shape, pack by pack; [§5](#5-the-migration) is when it lands. Every section is approved;
the rulings are in the [ledger](readable-config.md#decision-ledger).

> **In short.** A pack's folders say what it is; the manifest says only what a folder cannot.
> Four of the twelve packs need no manifest at all, and the two big ones lose four-fifths of
> their lines without losing a fact.

**Reads with:** [`readable-config.md`](readable-config.md) (front door and ledger),
[`config-migration.md`](config-migration.md) (`yolo pack migrate` and the release order),
[`manifest-language.md`](manifest-language.md) and [`pack-conventions.md`](pack-conventions.md)
(whose questions this takes over).

---

## 1. A small pack: `luna-worker`

Today, 23 lines for one setting:

```jsonc
{
  "name": "luna-worker",
  "description": "Work machines only (listed by tag-work's work.jsonc): pi-subagents' worker role runs on GPT-6 Luna whenever the parent pi session is on Amazon Bedrock; …",
  "contributes": [
    { "kind": "config-overlay", "surface": "pi/settings",
      "config": { "managed": { "subagents": { "agentOverridesByProvider": { "amazon-bedrock": {
        "worker": { "model": "amazon-bedrock/global.openai.gpt-6-luna" } } } } } } }
  ]
}
```

Proposed: a folder, no manifest, one file in pi's own format.

```text
luna-worker/
  overlays/pi-settings.jsonc
  README.md                         # its first line is the description
```

```jsonc
// Work machines: pi's worker subagent runs on GPT-6 Luna when the session is on Bedrock.
{"subagents": {"agentOverridesByProvider": {"amazon-bedrock": {"worker": {"model": "amazon-bedrock/global.openai.gpt-6-luna"}}}}}
```

`coordinator-reads-blocked` is the same shape; `workspace-questions` is a `briefing/` folder and
nothing else; `claude-bedrock-global` is `overlays/claude-settings@bedrock.jsonc`, the `@bedrock`
being the profile gate its manifest carries today.

## 2. A pack with programs: `matt-fzf`

Today, 56 lines: three programs with five install hints each, a files entry, an overlay.

```jsonc
{ "kind": "program", "bin": "fd",
  "install_hints": { "brew": "fd", "apt": "fd-find", "dnf": "fd-find", "pacman": "fd", "nix": "fd" } },
{ "kind": "program", "bin": "fzf",
  "install_hints": { "brew": "fzf", "apt": "fzf", "dnf": "fzf", "pacman": "fzf", "nix": "fzf" } },
{ "kind": "files", "from": "bin", "into": ".claude/bin" },
{ "kind": "config-overlay", "surface": "claude/settings",
  "config": { "managed": { "fileSuggestion": { "type": "command", "command": "~/.claude/bin/file-suggestion.sh" } } } }
```

Proposed, `pack.toml`, with `bin/` moved to `home/.claude/bin/`:

```toml
description = "Matt's fd|fzf custom file finder for Claude Code's @-file completion."

[[program]]
bin = "fd"
install = { apt = "fd-find", dnf = "fd-find" }   # the package is the bin name unless a manager differs

[[program]]
bin = "fzf"                                      # no install line: every manager calls it fzf

[[program]]
bin = "jq"

[[overlay]]
surface = "claude/settings"
set.fileSuggestion = { type = "command", command = "~/.claude/bin/file-suggestion.sh" }
```

## 3. No manifest at all

Approved 2026-10-25 as convention C6 ([`config-user.md` §8](config-user.md#8-lines-you-never-write)).
Folders say what they are; `pack.toml` is for what a folder cannot say: a program to install, a
source to fetch and patch, a build, a model.

```text
<pack>/
  home/            laid into the home directory, file by file; merges with other packs' files
  skills/          as today
  briefing/        as today
  overlays/        <surface>[@<profile>].<ext>, in the agent's own format
  patches/         as today
  pack.toml        only if something above cannot say it
```

Under it, 25 of `matt`'s 39 `files` entries (127 lines) vanish into `home/.pi/agent/extensions/`,
and its seven overlay bodies (425 lines, pi's and claude's own settings) move to `overlays/`.
For pi, a shorter route already shipped: one entry `{"kind": "files", "agents": ["pi"], "from":
"files/pi"}` loads a folder as a pi package ([OQ-PR1](../reference/pack-system.md#oq-pr1), ruled
2026-10-05); `matt` predates it.

## 4. Flags that add, and a model said once

`agy-capture` today restates `--dangerously-skip-permissions` because a later pack's flag list
replaces an earlier one's whole; delete the line and agy prompts for every tool. Proposed, a
contribution states only what it changes, and replacing is said in as many words:

```toml
[[launch]]
bin = "agy"
flags = ["--model", "gemini-3.8-flash-medium"]     # adds to the agy pack's list; replace = true would not
```

`meta-muse` today writes six model rows into pi's `models.json` format, 199 lines, a seven-level
thinking map six times. Proposed, in yolo's model shape, for every agent:

```toml
[providers.meta.every_model]
api = "openai-responses"           # new fact: the wire API
thinking = ["minimal", "low", "medium", "high", "xhigh", "max"]   # new fact
accepts = ["text", "image"]
max_tokens = 128_000

[providers.meta.models."muse-spark-1.3-contributor"]
name = "Spark 1.3"
context = 1_048_576
images = { max_width = 2000, max_height = 2000, max_bytes = 4_718_592, jpeg_quality = 80 }   # new fact
price = { input = 0.1, output = 0.2, cache_read = 0.002, cache_write = 0 }

[providers.meta.models]
"muse-wonder-1.0-contributor"            = { name = "Wonder 1.0",      context = 1_007_997 }
"muse-wonder-1.0-contributor[200k]"      = { twin_of = "muse-wonder-1.0-contributor", compact_at = 200_000 }
```

`twin_of` takes every fact of the named row and sends its id on the wire; each agent's render
writes its own file from these rows.

Approved 2026-10-25, all six levers ([OQ-RC19](readable-config.md#decision-ledger)): *"the four
smallest packs become folders, and the two largest keep every fact in a fifth of the lines."* The
install hint was settled in the same round (*"what does all mean here?"* … *"I like default, not
all, and then also have it just default to the bin name if not given"*): the package name is the
bin name unless written; `install = "ripgrep"` names it for every manager, and a table names only
the managers that differ, with `default` for the rest. `false` is "no hint": `install = false` for a
program no manager packages, `install = { default = false, brew = "…" }` for one only some do
([RC-D25](readable-config.md#RC-D25)). The same `false` declares a provider option with no default
(`options.pin_model = false`), and a config surface row lists the keys its `managed` patch deletes
as `unset = ["/tui/notice"]`.

## 5. The migration

`yolo pack migrate <path>` rewrites the manifest, keeps comments as the config migration does,
moves plain-copy `files` entries into `home/` or a pi package folder, and uses `git mv` in a
checkout, so history follows. A local pack in the old format is refused naming the command; a
fetched one is refused once the old reader is gone, naming the author's migration and the fork
([OQ-RC8](readable-config.md#decision-ledger)). A yolo of 0.11.1 or earlier refuses any new
manifest field outright, so a new manifest shape is a new file name however it is done.

Ruled 2026-10-25 ([OQ-RC5](readable-config.md#decision-ledger)): together with the configs. *"one
break, and the migrate command does the moving; packs are the bigger files and the same rules
apply."* The next release reads both formats of config and manifest, `yolo pack migrate` runs
beside `yolo config migrate`, and a later release reads only the new ones
([`config-migration.md` §3](config-migration.md#3-the-order-of-releases)).

## 6. Principles

1. **A folder is the declaration.** A manifest names only what a folder cannot.
2. **Big content lives in its own file, in its owner's format.** An overlay body is pi's or
   claude's settings, not yolo's.
3. **A contribution states only what it changes.** Lists add and objects merge; `replace = true`
   says otherwise.
4. **A model is declared once, in yolo's shape, for every agent.**
5. **The obvious is not written.** No install hint when the package is the bin name; `codec` from the
   extension; `scope = "workspace"` and `run = bin` as defaults. Shipped packs keep the
   per-manager form, which keeps the test that every shipped hint cites its source.
6. **A pack is named by where it comes from**, never by its manifest
   ([`config-user.md` §4](config-user.md#4-naming-a-pack-that-is-not-shipped)).

## Appendix: evidence

### A.1 The twelve packs

1,688 manifest lines, 394 of them comments. `matt` is 860 lines and `meta-muse` 512; the other ten
run from 9 to 74. By contribution kind, `matt` is `config-overlay` 7 entries / 425 lines,
`files` 39 / 271, `config` 1 / 57, `autonomy` 1 / 42, the other five kinds 8 / 59.

Most comments explain yolo, not the pack: `agy-capture` gives 24 of its 74 lines to two warnings
about flag lists replacing (`launchFlagClaims` in `internal/packload/packload.go` assigns, it does
not append) and overlays replacing objects. Both large packs also carry long comments on a key
that pi's derive overwrites silently at every boot (`subagents.defaultModel` and its siblings),
which `yolo config render` cannot show because it omits the computed layer; that is a `pack lint`
finding, in the seed, not a shape change.

### A.2 Why ten files take fifty lines

A `files` entry is a read-only mount over its whole destination, and two entries claiming one
path refuse the launch ([`pack-system.md`](../reference/pack-system.md), the `files` section), so
a pack cannot hand over a folder another pack also writes into. `matt` copies 25 files into
`~/.pi/agent/extensions/` with 25 entries, 127 lines, each writing its path twice. The list form
for a folder that should not mirror the home:

```toml
[[files]]
from = "files/extensions"
into = ".pi/agent/extensions"
only = ["turn-bell.ts", "versions.ts", "board", "work-control"]
```

### A.3 The compression exercise

Comments removed and minified, then gzip −9; ratio is compressed over minified, lower meaning
more repetitive; the prose baseline is `what-yolo-is.md` cut to the same size.

| File | Minified | gzip ratio | Prose, same size |
| :--- | ---: | ---: | ---: |
| `matt` | 15,264 | **0.24** | 0.40 |
| `meta-muse` | 4,513 | **0.25** | 0.43 |
| `matt-mods` | 1,224 | **0.37** | 0.56 |
| `agy-capture` | 801 | 0.53 | 0.60 |
| `matt-fzf` | 667 | 0.47 | 0.61 |
| the seven smallest | 203–574 | 0.59–0.74 | 0.64–0.74 |
| `matt`'s 25 extension copies | 2,794 | **0.15** | — |
| `meta-muse`'s six model rows | 2,608 | **0.18** | — |

The small packs are not repetitive; their trouble is per-line noise. The repetition is the
extension copies and the model rows. Before and after: `matt-fzf` 56 lines → the file in [§2](#2-a-pack-with-programs-matt-fzf); the 25
extension copies 127 → 30 as a list entry, or 0 under `home/`; `matt` whole 15,264 minified
bytes → 5,315 with P1 and P2. The ratio cannot be the target: a reshaped file compresses *worse*
because the repeats gzip was discarding are gone, so the scorecard is lines a person reads, facts
written more than once, and a compressed size that stays flat.

### A.4 What this takes over

[OQ-M1](manifest-language.md#OQ-M1) and [OQ-M2](manifest-language.md#OQ-M2) by
[OQ-RC5](readable-config.md#decision-ledger) and [OQ-RC2](readable-config.md#decision-ledger), ruled TOML; [OQ-M3](manifest-language.md#OQ-M3)
stays there. [OQ-PC1](pack-conventions.md#OQ-PC1) to [OQ-PC3](pack-conventions.md#OQ-PC3) by the
`home/` folder. [`pack-conventions.md`](pack-conventions.md#appendix-a-counts) counted ten
single-file `files` entries in `matt` on 2026-10-05 and found about five lines saved; that count
covered pi-path entries only, where this one covers every copy-only entry across his packs, 39.
Its rejections of a one-value `install_hints` and of a provider implying its profile were its own
verdicts, not the maintainer's rulings; the second is now ruled the other way
([`config-user.md` §5](config-user.md#5-which-setup-each-agent-runs-on)).
