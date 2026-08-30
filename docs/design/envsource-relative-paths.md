---
title: "Where Relative env_sources Paths Resolve: Beside the Declaring File"
date: 2026-08-30
status: accepted
tags: [env, config, host, security, env-sources]
summary: "A relative env_sources file entry resolves beside the file that declared it — the user config at ~/.config/yolo-jail/, each include at the include's own directory, the workspace config at the workspace root — implemented by anchoring at LOAD time, where per-file provenance still exists. The host notch keeps a refusal as a backstop for unanchored entries, whose only remaining resolution is the cwd, which a workspace controls."
---

# Where relative `env_sources` paths resolve — beside the declaring file

**Status:** DECIDED and IMPLEMENTED, 2026-08-30 (OQ-E1/E2 ruled the same day; commit
`7f600ef7`). The host-notch refusal that preceded the ruling shipped hours earlier as
`b08dda02` and is superseded by it — kept, narrowed to a backstop.

**The short version.** A relative `env_sources` file entry (`"prod.env"`) means **beside
the file that declared it** — the same convention `include_if_found` already uses — at
both notches. The loader anchors each file's entries the moment it reads that file
(`AnchorEnvSources` inside `LoadJSONCWithIncludes`), which is the only place per-file
provenance still exists: the merge concatenates user config, includes, layer, and
workspace config into one list, and absolute paths survive a concat while provenance
does not. Workspace-declared entries therefore do not move at all (the workspace config
sits at the workspace root, so beside-the-file IS workspace-relative), and the one entry
whose meaning changes is a **user-config entry in a jail launch** — workspace-relative
before, config-dir now — which was the hole: a cloned repo could plant `prod.env` in the
workspace and a user config's relative entry fed it into the jail's environment.

**Reads with:** [`host-agent-environment.md`](host-agent-environment.md) (the design this
extends — its §5.4/§6.1 step 3 define the env_sources channel at the host notch),
[`storage-and-config.md`](storage-and-config.md) (the user/workspace config scopes the
boundary argument leans on).

---

## 1. The rule

An `env_sources` entry is one of two shapes (the `env_sources` entry in `yolo
config-ref`; `ResolveEnvSources`, `internal/config/envsources.go`):

- a **string** — a dotenv FILE to read, or
- an **object** — inline vars, with `null` spelling unset.

A string that starts with neither `/` nor `~` is *relative*, and resolves **beside the
declaring file**:

| Declaring file | A relative entry anchors at | Changed from |
| :--- | :--- | :--- |
| `~/.config/yolo-jail/config.jsonc` | `~/.config/yolo-jail/` | was workspace-relative in jail launches; refused at the host notch (2026-08-30, `b08dda02`) |
| an `include_if_found` file | the include's own directory | was workspace-relative in jail launches |
| `--user-layer` file | the layer file's directory | was workspace-relative in jail launches |
| `<workspace>/yolo-jail{,.local}.jsonc` | the workspace root | **unchanged** — beside-the-file *is* workspace-relative here |

Inline entries are not paths and never see any of this. Absolute and `~`-relative
entries pass through every layer untouched.

## 2. How it is built — anchoring at load time

`AnchorEnvSources` (`internal/config/envsources.go`) rewrites a loaded config's relative
entries to absolute paths under the file's directory, and it runs **inside
`LoadJSONCWithIncludes`** — the one funnel every config file already passes through: the
top configs, each include (via the loader's own recursion), the workspace and local
configs, and the `--user-layer` file. The inherited-launch file is the one out-of-funnel
read and anchors itself beside itself.

Per-file timing is the whole design. By the time `MergeConfig` concatenates the lists,
per-entry provenance is gone — that is why the cheap version of this feature (anchor at
the user config's dir *after* the merge) was rejected: it would guess the declaring
file, and guess wrong for includes. Anchoring before the concat means each file's
entries carry their own provenance as absolute paths, and the merged list needs none.

Two deliberate leftovers:

- **`ResolveEnvSourcePath` is unchanged** and remains the fallback for relative entries
  no loader anchored — resolution against the workspace root. In practice that is a
  pre-ruling assembled snapshot read verbatim in-jail, or a hand-built config; both keep
  the behavior they were written under.
- **The host notch still refuses unanchored relative entries**
  (`hostScopedEnvSources`, `internal/cli/host.go`): every entry a yolo loader produced
  arrives anchored, so what reaches the host notch still relative has no trustworthy
  anchor, and its only remaining resolution is the **cwd — which a workspace controls**.
  The refusal is the backstop, not the rule; its warning still names the remedy.

> [!WARNING]
> The one implementation that must never exist is "anchor at the user config's dir,
> host notch only, after the merge." It looks like a one-line anchor swap, and it is
> option B below: one spelling meaning workspace-relative in a jail and config-dir at
> the host notch, plus a wrong-file guess for included entries — invisible to the user.

## 3. Why the rule exists — the boundary this closes

The composed config at the host notch is user-scope only (`ecfd2255`): the workspace
`yolo-jail.jsonc` is agent-editable — `/workspace` is bind-mounted rw — so letting it
set `LD_PRELOAD`/`BASH_ENV`/`NODE_OPTIONS` for a host process is arbitrary code
execution, reached by cloning a repo. That closed the *config-merge* door. A relative
path re-opened the same boundary through the *filesystem* — and, less obviously but
equally really, through the **jail**: a user-config entry resolved against the
*workspace* in a jail launch, so the repo's `.env` fed the jail's environment from a
file the config never named. Beside-the-declaring-file closes both doors with one rule:
no config entry ever resolves against a directory the workspace controls, at either
notch.

## 4. The option space, with verdicts

| Option | Rule | Verdict |
| :--- | :--- | :--- |
| **A. Refusal at the host notch** | Relative entries skipped with a warning naming the remedy | Shipped 2026-08-30 (`b08dda02`), **superseded the same day** by OQ-E1's ruling — correct security, wrong shape: it banned a useful spelling instead of fixing what the spelling meant. Survives as the backstop (§2). |
| **B. Anchor at the user config's dir, host notch only** | Post-merge anchor swap in `hostEnvVars` | **Rejected.** A dialect split (one spelling, two meanings by surface) plus a wrong-file guess for included entries. See the warning in §2. |
| **C. Beside the declaring file, everywhere** | Anchor at load time, per file, both notches | **RULED and IMPLEMENTED** (`7f600ef7`). Git's `include.path` convention; the only version where the workspace's own entries are structurally unchanged. |

## 5. Non-goals

- No dotenv dialect changes: files have no "unset" syntax and will not get one.
- Nothing about inline entries, `null` removals, or ordering — settled 2026-08-30 in
  `19f92de1`.
- Not a secrets-management design: where secrets *live* is
  [`storage-and-config.md`](storage-and-config.md)'s subject; this doc is only about
  what a relative path points at.

## 6. Risks and how they landed

| Risk (from the proposal) | How it landed |
| :--- | :--- |
| Provenance plumbing touches the most load-bearing config code | The load-time design avoided plumbing entirely — one function called at the one funnel every file already passes through; no merge changes, no new shapes |
| User-config entries silently change meaning in existing jails | Deliberate, ruled (OQ-E2: unified); discovery through the resolution warnings, which name the new absolute path, and a one-time config-diff re-approval for the workspace config |
| The dialect split re-enters as "host-only anchoring" | OQ-E2 ruled it out in advance; §2's warning records why |

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-E1 | **Declaring-file anchoring (option C) replaces the refusal** — *"yes"* | 2026-08-30 | §1, §2 (`7f600ef7`) |
| OQ-E2 | **Unified: both notches, jail included** — *"yes unified"*; host-only anchoring is B wearing C's implementation and is ruled out | 2026-08-30 | §1 table, §4 (`7f600ef7`) |
