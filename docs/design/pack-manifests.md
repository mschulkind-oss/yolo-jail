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
[`pack-conventions.md`](pack-conventions.md) (whose questions this takes over), and
[§7](#7-the-manifest-language-principles-alternatives-and-rulings) (the manifest-language analysis
this doc absorbed: its principles, the syntaxes it weighed, and its rulings).

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

## 7. The manifest language: principles, alternatives and rulings

The manifest-language analysis (2026-09-20) asked two independent questions: what the declaration
*model* is, and what it is *written in*. It argued that the model change carries most of the gain
under any syntax. The readable-config redesign ruled both on 2026-10-25 ([§7.5](#75-decision-ledger)):
the model change, in TOML. This section keeps what that analysis settled; its measurements are in
[A.5](#a5-the-2026-09-measurements).

### 7.1 Principles M1 to M5

> **The line that must not move.** [`pack-system.md`](../reference/pack-system.md): *"The claim
> enumeration must be TOTAL. A crossing with no claim is a crossing that appears in **no report at
> all**."* So: **compress repetition, never information.** Deriving a value a pack already states
> removes redundancy; dropping a `mount` because a pack "conventionally" mounts something removes a
> claim. The first is the goal; the second is forbidden in every syntax.

- **M1. A declaration is inert data, never an effect.** The origin gate rests on it
  ([`trust-paths.md`](trust-paths.md)): a pack that could run at boot would make shipping content
  and executing code one grant.
- **M2. The claim enumeration is total.** Compression removes repetition, never a distinct fact.
- **M3. No bespoke language.** yolo does not invent a config language or maintain its parser.
- **M4. Readable by someone who did not write it.** The audience for a pack file is a maintainer
  and an agent, not the author alone.
- **M5. Any computation is pure, sandboxed, and its RESULT is what is validated.** If a manifest
  may ever compute, the value it produces is schema-checked and footprinted exactly as if it had
  been written literally, so M2 survives by construction. This is the condition on which
  [OQ-M3](#OQ-M3) reopens.

### 7.2 The agent name is declared once, never derived

A pack provides zero or one agents and names the one it provides, once per pack
([`OQ-D5`](slots-and-contributions.md#7-decision-ledger), ruled 2026-09-20, which restates
[`OQ-BA2`](../reference/agent-briefings.md#oq-ba2)'s refusal of a derived identity at pack scope).
An earlier draft derived it from `program.bin`. Neither the pack's `name` nor its bin can be the
source: `packs/omp` is named `omp` with bin `oh-omp`, and `oh-omp` is the agent its briefings and
skills declare. The saving comes from deleting the per-contribution repeats either way.

### 7.3 Alternatives weighed

| Option | What it is | Verdict |
| :--- | :--- | :--- |
| A. JSON (JSONC), restructured | The model change only: group by kind, identity once, per-kind defaults | The syntax-free half; it carries the two largest sources of repetition (the `kind` tags and the repeated identity). Taken, in TOML ([OQ-M1](#OQ-M1)) |
| B. Data-only Lua | A chunk returning a table, run in the existing pure sandbox (`internal/agentcfg/luahook`) | Not taken: the manifest becomes code executed to be read, a trust-model change even when sandboxed ([OQ-M3](#OQ-M3), M5) |
| C. Starlark | Hermetic, deterministic config language with a Go implementation | Not taken: a new dependency, a second language beside Lua, and still config-as-code |
| D. Jsonnet, CUE, Dhall | Full config languages | Not taken: heaviest dependencies, least familiar, overpowered for a file that should be data |
| E. YAML or TOML alone | A syntax swap with no model change | No fix on its own: it moves the same repetition into another file. TOML rides *with* the model change ([OQ-M2](#OQ-M2)) |

### 7.4 Background to [OQ-M1](#OQ-M1)

"The model change" is three syntax-independent levers: group entries by kind, so the key is the
kind; state the pack's agent once ([§7.2](#72-the-agent-name-is-declared-once-never-derived)); and
give each kind defaults for `into`, `path` and `codec`. An older yolo reads a regrouped manifest as
an empty pack and refuses defaults built as omissions
([`pack-conventions.md` §2](pack-conventions.md#2-what-an-older-yolo-does-with-a-convention)),
which is why a new manifest shape is a new file name ([§5](#5-the-migration)). That conflicted with
the premise the analysis began from, the owner's *"We don't need transitions"* (2026-09-20), while
hosts run different yolos; [OQ-RC5](readable-config.md#decision-ledger)'s one break with a migrate
command is the answer. The same review
handed this question a fourth lever, a posture naming a surface instead of restating it
([`pack-conventions.md` §7](pack-conventions.md#7-considered-and-not-proposed)); the ruling took the
TOML overlay form of [§3](#3-no-manifest-at-all) and [§4](#4-flags-that-add-and-a-model-said-once).

### 7.5 Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-M1"></a>[OQ-M1](#OQ-M1) | **Yes: grouped by kind, the pack's identity stated once, in the same break as the configs.** Ruled as lever P3 of [OQ-RC19](readable-config.md#decision-ledger), landing under [OQ-RC5](readable-config.md#decision-ledger); the `exposes` split is not the break it waits for | 2026-10-25 | [§4](#4-flags-that-add-and-a-model-said-once) | ✅ by 2026-10-30: every shipped pack manifest is a `pack.toml` |
| <a id="OQ-M2"></a>[OQ-M2](#OQ-M2) | **TOML, with the model change.** Ruled by [OQ-RC2](readable-config.md#decision-ledger) for every file a person writes, manifests included; option E was no fix on its own, and the model change ([OQ-M1](#OQ-M1)) rides with it | 2026-10-25 | [§7.3](#73-alternatives-weighed) | ✅ by 2026-10-30 |
| <a id="OQ-M3"></a>[OQ-M3](#OQ-M3) | **Moot: a manifest is a literal TOML file, so nothing is executed to be read.** It mattered only if [OQ-M2](#OQ-M2) picked a code syntax. A code syntax proposed again reopens it, with [M5](#71-principles-m1-to-m5) as the condition | 2026-10-25 | [§7.1](#71-principles-m1-to-m5) | nothing to build |
| <a id="OQ-M4"></a>[OQ-M4](#OQ-M4) | **No: the user and workspace config keep JSONC whatever [OQ-M2](#OQ-M2) rules.** Decided as an implementation choice, [MN-D1](#MN-D1). Reversed with it on 2026-10-25 | 2026-09-30 | [§7.5](#75-decision-ledger) | nothing to build |
| <a id="MN-D1"></a>MN-D1 | *Implementation decision, [OQ-M4](#OQ-M4).* **The user and workspace config keep JSONC, whatever [OQ-M2](#OQ-M2) rules for manifests**: they are settings with no claims to enumerate, so M2 does not reach them, and yolo had already deleted its one scripted user config, `config.lua` ([`OQ-LT1`](../reference/pack-system.md#oq-lt1)). **Reversed 2026-10-25:** the configs move to TOML ([OQ-RC2](readable-config.md#decision-ledger)) | 2026-09-30 | [§7.5](#75-decision-ledger) | nothing to build |

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

[OQ-M1](#OQ-M1) and [OQ-M2](#OQ-M2) by
[OQ-RC5](readable-config.md#decision-ledger) and [OQ-RC2](readable-config.md#decision-ledger), ruled TOML; [OQ-M3](#OQ-M3)
is moot ([§7.5](#75-decision-ledger)). [OQ-PC1](pack-conventions.md#OQ-PC1) to [OQ-PC3](pack-conventions.md#OQ-PC3) by the
`home/` folder. [`pack-conventions.md`](pack-conventions.md#appendix-a-counts) counted ten
single-file `files` entries in `matt` on 2026-10-05 and found about five lines saved; that count
covered pi-path entries only, where this one covers every copy-only entry across his packs, 39.
Its rejections of a one-value `install_hints` and of a provider implying its profile were its own
verdicts, not the maintainer's rulings; the second is now ruled the other way
([`config-user.md` §5](config-user.md#5-which-setup-each-agent-runs-on)).

### A.5 The 2026-09 measurements

The manifest-language analysis measured the JSON manifests first, with `gzip -c <file> | wc -c`
over the real files on 2026-09-20:

| File | raw | gzip | ratio |
| :--- | ---: | ---: | ---: |
| the user config, `config.jsonc` | 2 575 | 1 252 | 0.49 |
| this repository's workspace config, `yolo-jail.jsonc` | 6 170 | 2 643 | 0.43 |
| `packs/pi/pack.json` | 2 662 | 717 | 0.27 |
| `packs/claude/pack.json` | 4 162 | 1 015 | 0.24 |
| `README.md` (prose, for scale) | 17 761 | 7 104 | 0.40 |

In `packs/claude/pack.json` the most frequent keys were `kind` (17, one per entry), `name` (8),
`agent` (6), `config` (5), and `codec`, `path` and `managed` (4 each). Four causes: the pack
repeated its own identity; a flat tagged union carried a `kind` tag per entry; keys spelled
mechanism (`managed`, `computed`, `retireOnFirstRender`, `after`, `mode: rmw`) rather than intent;
and there was no default for the common case.

On 2026-09-22 the same manifest was restructured by hand per option A, both variants
machine-checked fact-preserving and canonicalized like the baseline:

| rendering | raw | gzip | ratio |
| :--- | ---: | ---: | ---: |
| baseline | 4142 | 1006 | 0.243 |
| A1 (strict option A) | 3313 | 942 | 0.284 |
| A2 (+ keyed by discriminator) | 2838 | 879 | 0.310 |
| data-only Lua, A2's facts | 2227 | 841 | 0.378 |

Neither restructuring reached the ~0.40 prose threshold the analysis had set, and three
measurements showed that threshold unsound: prose at the manifest's size is 0.49, not 0.40, since
the ratio rises with size; `jq -c` alone (minifying, no model change) reaches 0.368, because 44% of
the baseline was indent whitespace; and A1 drops 20% of raw bytes but only 6.4% of gzip bytes, the
signature of deleting repetition gzip had already predicted.

> [!WARNING]
> **Compression ratio is orthogonal to the complaint it was chosen to quantify.** The objection is
> about what a human must read; deleting predicted repetition moves the number the wrong way. The
> direction is robust across xz, bzip2 and gzip levels 1 to 9; the threshold is not. Do not
> re-derive a byte-ratio gate for a manifest question. [A.3](#a3-the-compression-exercise) reaches
> the same verdict from the maintainer's own packs.
