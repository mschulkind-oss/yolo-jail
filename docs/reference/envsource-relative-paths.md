---
status: current
verified: 2026-09-09
verified_commit: 38873c0d
covers:
  - internal/config/envsources.go
  - internal/config/load.go
  - internal/config/userlayer.go
  - internal/cli/host.go
tags: [env, config, host, security, env-sources]
summary: "A relative `env_sources` file entry resolves beside the file that declared it — the same convention `include_if_found` uses — because the loader anchors each file's entries at load time, where per-file provenance still exists. No config entry ever resolves against a directory the workspace controls, at either notch."
---

# Where a relative `env_sources` path resolves

**Status:** CURRENT as of 2026-09-09, verified against `38873c0d`.

An `env_sources` entry is either a **string** — a dotenv file to read — or an **object** of
inline variables, where `null` spells an unset. A string beginning with neither `/` nor `~` is
*relative*, and it resolves **beside the file that declared it**.

That is the same convention `include_if_found` already uses, and it holds at both notches (a
jail launch and `yolo host`). It is implemented by **anchoring at load time**: each config file's
relative entries are rewritten to absolute paths the moment that file is read, which is the only
place per-file provenance still exists.

| Component | Lives in |
| :--- | :--- |
| The anchor, and the resolution fallback | `internal/config` (`AnchorEnvSources`, `ResolveEnvSourcePath`, `ResolveEnvSources`) |
| The one funnel every config file passes through | `internal/config` (`LoadJSONCWithIncludes`) |
| The out-of-funnel read that anchors itself | `internal/config` (`applyInheritedLaunch`, `userlayer.go`) |
| The host-notch backstop for unanchored entries | `internal/cli` (`hostScopedEnvSources`) |

**Reads with:** [`host-agent-environment.md`](host-agent-environment.md) (the channel these
entries feed on the host — `env_sources` is what makes a credential reach a host agent's process
environment at all), [`storage-and-config.md`](storage-and-config.md) (the
user/workspace config scopes this rule leans on). For the `env_sources` key's own schema, run
`yolo config-ref`.

---

## The rule

| Declaring file | A relative entry anchors at |
| :--- | :--- |
| `~/.config/yolo-jail/config.jsonc` | the user config directory |
| an `include_if_found` file | that include's own directory |
| a `--user-layer` file | the layer file's directory |
| `<workspace>/yolo-jail{,.local}.jsonc` | the workspace root — beside-the-file *is* workspace-relative here |

Inline object entries are not paths and never see any of this. Absolute and `~`-relative entries
pass through every layer untouched.

## Why anchoring happens at load time

By the time `MergeConfig` concatenates the user config's entries, its includes', any layer's and
the workspace's into one list, **per-entry provenance is gone** — but absolute paths survive a
concat. So each file anchors its own entries before the lists ever meet, and the merged list
needs to carry no provenance at all.

The include case is the proof the anchor must be per-file: an include's relative entry anchored
at the *top* config's directory would look beside the wrong file, invisibly.

`AnchorEnvSources` therefore runs **inside `LoadJSONCWithIncludes`** — the funnel the top
configs, each include (through the loader's own recursion), the workspace and local configs, and
the `--user-layer` file all already pass through. The inherited nested-launch config is the one
read outside that funnel, and it anchors itself beside itself.

> [!WARNING]
> **Never anchor after the merge.** "Anchor at the user config's directory, host notch only,
> after the merge" looks like a one-line swap and is the one implementation that must not exist:
> it gives one spelling two meanings by surface (workspace-relative in a jail, config-dir at the
> host notch) and guesses the wrong file for every included entry. Both failures are invisible to
> the user.

## The boundary this closes

The composed config at the host notch is user-scope only: the workspace `yolo-jail.jsonc` is
agent-editable, so letting it set `LD_PRELOAD`/`BASH_ENV`/`NODE_OPTIONS` for a *host* process is
arbitrary code execution, reachable by cloning a repo. That closed the config-merge door.

A relative path re-opened the same boundary through the **filesystem**, and — less obviously —
through the **jail**: a user-config entry that resolved against the *workspace* meant a cloned
repo could plant a `prod.env` the user's config then fed into the jail's environment, from a file
the config never named.

Beside-the-declaring-file closes both doors with one rule: **no config entry ever resolves
against a directory the workspace controls, at either notch.** The workspace's own entries do not
move at all, which is what makes the rule cheap.

## Two deliberate leftovers

- **`ResolveEnvSourcePath` still falls back to the workspace root** for a relative entry no
  loader anchored. In practice that is a pre-ruling assembled snapshot read verbatim in-jail, or
  a hand-built config — artifacts a newer loader never touched, which keep the behavior they were
  written under.
- **The host notch still refuses unanchored relative entries** (`hostScopedEnvSources`). Every
  entry a yolo loader produced arrives absolute, so an entry that reaches the host notch still
  relative has no trustworthy anchor and only one remaining resolution: the **cwd, which a
  workspace controls**. The refusal is the backstop, not the rule, and its warning names the
  remedy.

## What this does not license

- **No dotenv dialect changes.** A dotenv file has no "unset" syntax and does not get one; that
  is what an inline `null` is for.
- **Nothing about inline entries, `null` removals, or ordering** — a separate, settled mechanism.
- **Not a secrets-management design.** Where secrets live is
  [`storage-and-config.md`](storage-and-config.md)'s subject; this rule only
  fixes what a relative path points at.

## Why it's this way

| Ruling | Why it holds |
| :--- | :--- |
| <a id="oq-e1"></a>[**OQ-E1**](#oq-e1) — declaring-file anchoring replaces the host-notch refusal | The refusal had the right security and the wrong shape: it banned a useful spelling instead of fixing what the spelling meant. It survives as the backstop above. |
| <a id="oq-e2"></a>[**OQ-E2**](#oq-e2) — unified across both notches, jail included | Host-only anchoring is the rejected post-merge option wearing this one's implementation: one spelling, two meanings, plus a wrong-file guess for includes. The cost of unifying — a user-config entry's meaning changed inside a jail launch — was the fix's whole point. |

## Current values

Verified at `38873c0d`.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Relative-entry test | starts with neither `/` nor `~` | `config.AnchorEnvSources` |
| Anchor call sites | `LoadJSONCWithIncludes` (every file, recursively) and `applyInheritedLaunch` | `internal/config/load.go`, `internal/config/userlayer.go` |
| Unanchored fallback | the workspace root | `config.ResolveEnvSourcePath` |
| Host-notch backstop | skip with a warning naming the remedy | `internal/cli/host.go` (`hostScopedEnvSources`) |
