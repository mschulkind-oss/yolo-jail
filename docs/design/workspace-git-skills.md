---
title: Workspace Git skill imports
status: in-review
stage: DESIGN
next: Owner rules on [OQ-WGS1](#OQ-WGS1) and [OQ-WGS2](#OQ-WGS2); then the designer records accepted choices before a plan is written.
depends-on: []
evidence-date: 2026-11-16
evidence-snapshot: HEAD @ efdd6f38f457bd4878e0399b9d123259363156df
---

# Workspace Git skill imports

**Status:** 2026-11-16 (`efdd6f38f457bd4878e0399b9d123259363156df`). The tree checks in [§11](#11-code-checks) verified the header claims and constraints; this is a design-only proposal, not an implementation.

**In short:** A workspace may name Git repositories in a new `[skill_imports.<handle>]` table and receive their skill directories, and nothing else, inside the jail. Workspace `packs` stays refused; what changes is the reason for the refusal.

**Why it matters:** The blanket rule "a workspace cannot cause outside content to enter the jail" blocks the harmless case (five shared skills) in order to block the dangerous one (a pack carrying hooks, programs, mounts, host reads). The owner has directed that the harmless case be allowed.

**The shape:** A dedicated key, not a restricted `packs`. Each entry is pinned to a commit in the config itself and fetched anonymously on the host at launch. It is projected by allow-list to skill directories, staged as the lowest-precedence skill layer, and never delivered to `yolo host`. No pack manifest is read, so no pack effect can ride along.

**Cost:** One new workspace config key, one small CLI group, one projection step on the existing host-side Git fetch and confined staging paths, and a reworded refusal message. It adds no lockfile, approval record or numeric knob.

**Start at [§3](#3-the-call-a-dedicated-key-not-restricted-packs)** — the selection boundary. [§5](#5-how-far-it-goes) defines what crosses it.

**Needs your ruling:** [OQ-WGS1](#OQ-WGS1) (mandatory commit pin or moving refs) · [OQ-WGS2](#OQ-WGS2) (anonymous-only sources or user credentials).

**Reads with:** [`pack-system.md` OQ-PK1](../reference/pack-system.md#oq-pk1) (why workspace `packs` is refused) · [`agent-briefings.md` workspace layer](../reference/agent-briefings.md#the-workspace-layer) and [WS-D24](../reference/agent-briefings.md#ws-d24) (skill priority and host exclusion) · [`workspace-packs.md`](workspace-packs.md) (separate open pack-selection design) · [`trust-paths.md` ledger](trust-paths.md#decision-ledger) (user-scope Git authority).

---

## 1. Problem and owner direction

Workspace `packs` is refused today: `validatePacks` hard-errors on the key, and `LoadPacks` reads only the user file ([`internal/config/packs.go`](../../internal/config/packs.go), [`internal/config/workspacescope.go`](../../internal/config/workspacescope.go)). The ruling behind it is [OQ-PK1](../reference/pack-system.md#oq-pk1) (2026-10-23): "none of the options for now", with a dedicated design requested.

On 2026-11-16 the owner set the direction this document works from, recorded as WGS-R1 in the [ledger](#12-owner-ruling):

- The rule should not be "workspaces may not select packs" in the blanket sense.
- It should be "workspaces may not include things whose features are dangerous".
- Skills from a Git repository are not dangerous in that sense and should be allowed.

This document does not ask that permission again. It answers what the syntax is and how far the allowance extends.

### What "not dangerous" means here

The design separates two things that the phrase "skills are harmless" can blur.

| | Content influence | Effect authority |
|---|---|---|
| What it is | Text and files the agent may read and choose to follow | Host reads/writes, mounts, credentials, network grants, config changes, anything that runs without the agent choosing it |
| Already admitted from a workspace? | Yes. The repository's own skills reach the jail via the confined workspace layer ([OQ-WS1](../reference/agent-briefings.md#oq-ws1)) | No |
| This design | Extends it to pinned third-party Git content | Grants none |

An imported skill **can** influence the agent. It is instruction text, and the agent acts with whatever authority the jail already has. That is the same class of influence a repository already exerts through its own skills and files. It is not a new authority, and it is not a reason to refuse the feature. The design does not claim skills are inert. It guarantees that an import adds no effect authority.

## 2. Terms

| Term | Meaning |
|---|---|
| **Import** *(coined here)* | One `[skill_imports.<handle>]` entry naming a Git repository, commit, path and optional name filter. It is not a pack selection or a general-purpose Git source. |
| **Handle** *(coined here)* | The entry's table key, used in messages and commands. It is not a namespace, repository name or pack name. |
| **Pin** *(coined here)* | The full Git commit object id in `rev`; it determines delivered bytes. It is not a branch or moving ref. |
| **Projection** *(coined here)* | A read-only walk of the fetched tree that selects only allowed skill directories. It is not plugin discovery, manifest interpretation or staging. |
| **Skill directory** *(coined here)* | An immediate child directory beneath the import's `path` containing `SKILL.md`. It is not any nested `skills/` directory discovered inside a plugin. |
| **Import layer** *(coined here)* | Imported skills staged as a separate workspace-derived layer below the workspace's own skills. It is not selected-pack content. |
| **Admission test** *(coined here)* | The three constraints in [§5](#5-how-far-it-goes) for whether content can cross this key; it is not a test suite or manifest declaration. |

## 3. The call: a dedicated key, not restricted `packs`

The repository has user-scope Git packs and a jail-only workspace skill reader, but neither selects third-party Git skills from workspace config. The separate proposal in [`workspace-packs.md`](workspace-packs.md) remains about pack selection, not this skill-only boundary.

- **User-scope `packs` with a Git source** fetches and delivers a whole pack with every effect. It rests on the user's own authority ([OQ-TP9](trust-paths.md#decision-ledger)), which a repository-editable key does not have.
- **The confined workspace skills layer** delivers skills safely, but only from directories already in the checkout.
- **[workspace-packs.md](workspace-packs.md)** proposes workspace selection of shipped packs by bare name and leans against fetched ones. It is unbuilt and in review.

The fetch machinery and the confined staging discipline are reused. The selection semantics are new.

### Options compared

| | A. Dedicated `skill_imports` (recommended) | B. Workspace `packs`, existing grammar, restricted |
|---|---|---|
| Unit selected | Skill directories | A pack |
| What decides safety | Allow-list projection; the manifest is never read | A classifier over `contributes` kinds, plugin discovery, `needs`, `project_dirs` |
| Soundness | By construction: what is not a skill directory is not copied | Depends on the classifier being complete. The packet shows it is not: plugin components are discovered from filesystem defaults without any declared kind ([`internal/pluginpack/pluginpack.go`](../../internal/pluginpack/pluginpack.go)), and `HonoredPlugins` refuses nothing today ([`internal/packload/plugins.go`](../../internal/packload/plugins.go)) |
| Future pack kinds | Fail closed automatically | Fail closed only if every new kind remembers to update the classifier |
| Meaning of `packs` | Unchanged: one word, one meaning, user scope only | Same key means "everything" at user scope and "a filtered part" at workspace scope |
| Selection order, lock, `needs` | None of them involved | All must be redefined for a second principal ([`internal/config/packselection.go`](../../internal/config/packselection.go), [`internal/packsrc/lock.go`](../../internal/packsrc/lock.go)) |
| Reuses pack names and namespaces | No. Skills arrive under their own names | Yes |
| Works on repositories that are not packs | Yes. Any repository with a directory of skills | Only pack-shaped ones |

**Recommendation: A.** The owner's own phrasing is the tell: "packs or plugins or whatever". The thing wanted is the skills, and the container is incidental. Option B makes the container the unit and then has to prove a negative about everything else in it. Option A copies only what it recognises.

Consequences of this recommendation:

- Workspace `packs` remains a hard validation error. Its message changes ([§10](#10-migration)).
- [`workspace-packs.md`](workspace-packs.md) and its [OQ-WP1](workspace-packs.md#OQ-WP1), [OQ-WP2](workspace-packs.md#OQ-WP2), [OQ-WP3](workspace-packs.md#OQ-WP3), [OQ-WP4](workspace-packs.md#OQ-WP4) and [OQ-WP5](workspace-packs.md#OQ-WP5) are neither accepted nor rejected here. Its leaning against fetched workspace packs stays coherent, because an import is not a pack.
- The comment in [`internal/packsrc/lock.go`](../../internal/packsrc/lock.go) that a workspace cannot name a pack stays true.

## 4. Syntax

### 4.1 TOML (workspace config)

```toml
[skill_imports.acme]
git  = "https://github.com/acme/agent-skills"
rev  = "3f9c2b7e1d4a5c6b8e9f0a1b2c3d4e5f60718293"   # full commit object id; required
ref  = "v1.4.0"                 # optional label; read only by `yolo skills update`
path = "skills"                 # optional; default "skills"
only = ["review", "triage"]     # optional; default: every skill directory under path
```

| Field | Required | Rule |
|---|---|---|
| `<handle>` | yes | Same character rules as a `pack_sources` key. Unique by construction, since it is a table key. |
| `git` | yes | `https://` URL with no embedded credentials. Other transports are refused ([§6.2](#62-fetch-is-anonymous-and-inert)). |
| `rev` | yes | Full commit object id, with width and hexadecimal alphabet defined by the repository object format (40 hex digits for this SHA-1 repository; 64 for SHA-256). Abbreviations, tags and branch names are validation errors. |
| `ref` | no | Tag or branch name. It never affects what a launch delivers. |
| `path` | no | Repository-relative directory whose immediate children are the skills. No absolute path, no `..`, no `.git`. This is the same discipline as `project_dirs` ([`internal/packdecl/projectdirs.go`](../../internal/packdecl/projectdirs.go)). |
| `only` | no | Unique immediate-child skill directory basenames, not paths: nonempty, not `.` or `..`, with no `/` or `\`. Absent means all; an explicit `[]` means none. Duplicate or invalid names are validation errors. A valid name absent from the pinned tree is reported at resolution; other listed skills remain eligible. |

Why a keyed table rather than a list: generic config merge union-merges lists and merges tables by key ([`internal/config/load.go`](../../internal/config/load.go)). A table lets a local override file change one import's `rev`, and gives every message and command a stable name. An absent `only` means all directories only when no lower config layer supplies that field; an explicit empty list means no names at that layer. Because list merge is union, an override cannot clear names supplied by a lower layer. Duplicate TOML table definitions are parse errors; duplicate names inside `only` are validation errors.

The `git+…//subdir?ref=…` address form used by packs is not accepted here. Separate fields keep the pin visible in a diff.

### 4.2 CLI

The `yolo skills` command group and both config keys are proposed names; none is registered in the current CLI or schema ([§11](#11-code-checks)).

| Command | Effect |
|---|---|
| `yolo skills add <url> [--as <handle>] [--ref <ref>] [--path <dir>] [--only a,b]` | Resolves the ref (default: the remote's default branch) to a commit and writes the entry with `rev` and `ref`. |
| `yolo skills update [<handle>]` | Re-resolves each entry's `ref` and rewrites `rev`. An entry with no `ref` is left alone and reported. |
| `yolo skills list` | Per import: pin, cached or not, skills that would be delivered, names shadowed, and what the source carries that is not imported. No network. |
| `yolo skills remove <handle>` | Deletes the entry. |

`add`, `update` and `remove` are explicit operator commands that edit the workspace config file. They are the only path by which this feature writes to the repository. A launch never does.

### 4.3 Scope

- **Legal in:** every file that makes up the assembled workspace config, including a local override. All of it passes the same validation and config-change gate; a prompt is not guaranteed on every first launch or unchanged config.
- **Not defined at user scope.** A user already has `packs`. The key is a validation error there in this design, with a message pointing at `packs`. Extending it later is a non-goal, not a refusal on principle.
- **User veto:** a user-only scalar, `workspace_skill_imports = false` (default `true`). It is a scalar because lists cannot be narrowed by an override. When false, imports are parsed, validated and listed, but never fetched or staged.

## 5. How far it goes

### 5.1 The admission test

A kind of content may come through a workspace import only if all three hold:

1. **Inert on arrival.** Delivering it runs nothing, registers nothing and configures nothing. It takes effect only if the agent reads it and chooses to act.
2. **Jail-confined.** It lands only in the staged skill layer inside the jail. It causes no host read, host write, mount, environment change, credential, grant or network rule.
3. **Recognised by shape, not by declaration.** The projection identifies it from the tree alone. Nothing the source says about itself widens what is taken.

### 5.2 Allowed and refused

| Content in the source | Verdict | Why |
|---|---|---|
| Skill directory: `SKILL.md` plus its bundled files | **Delivered** | Passes all three. Bundled scripts arrive as files the agent may choose to run inside the jail, like any file in the workspace. A source execute bit stages as `0755`; otherwise the mode is `0644`, regardless of source read/write bits. Nothing invokes scripts during import. |
| `pack.toml` (any content) | Not read | Test 3. This removes `needs`, `project_dirs`, audiences and alternate skill sources in one stroke. |
| `briefing` | Refused | It passes tests 1 and 2 but is unconditional, always-on instruction text from a third party. It is the named next candidate if the owner wants to go further. It is not admitted by default because the direction names skills. |
| `files`, `config`/surfaces, `env`, `state` | Refused | They place or change things outside the skill layer. `files` preserves executable bits at arbitrary destinations ([`internal/packdecl/kinds.go`](../../internal/packdecl/kinds.go)). |
| `program`, `hooks` | Refused | Automatic or install-time execution. |
| `reads-host`, `mount`, loopholes/intercepts, provider/profile | Refused | Effect authority. |
| Plugin-shaped directories (manifest or filesystem defaults): hooks, MCP/LSP servers, monitors, `bin/`, status-line commands, workflows | Not recognised as plugins | A plugin's own `skills/` directory can be named as `path` and its skills delivered. No plugin component is registered or activated. |
| Any kind added to the pack system later | Refused | It is not on the allow-list. |

### 5.3 Dependency closure

There is none, deliberately. An import is closed over its own skill directories.

- `needs` is not read, so an import cannot pull an embedded pack into selection.
- `project_dirs` is not read, so an import cannot change which workspace paths the existing layer mirrors.
- Git submodules are not followed. A gitlink entry inside a skill directory is treated as a refused entry.
- One import cannot name another source.

### 5.4 Mixed sources: project or reject?

A source that carries skills and also hooks, programs or an MCP server can be handled two ways.

| | Project (recommended) | Reject the whole source |
|---|---|---|
| Usable sources | Most real plugin and pack repositories | Only pure skill repositories |
| Upstream adds a hook later | Nothing changes for the workspace | The import breaks on the next pin bump |
| Risk | A skill may assume a server or program that was not imported and work poorly | None of that kind |
| Signal to the operator | `yolo skills list` and the first-delivery line report visible paths outside selected skill directories; they do not claim an exhaustive manifest/plugin inventory | A refusal |
| Safety | Identical. Neither delivers the non-skill content | Identical |

Because safety is equal, the choice is about usefulness and honesty. Projection is recommended, on the condition that visible paths outside selected skill directories are reported. This does not reveal components described only by manifests or discover latent plugin behavior. Rejection would give upstream authors a way to break downstream workspaces without making anyone safer.

## 6. Git: addressing, pinning, fetch, state

### 6.1 The pin is the lock

The recommended config carries the full commit object id, so there is no lockfile. This addresses several design questions, subject to [OQ-WGS1](#OQ-WGS1).

- **Reproducibility:** the same config identifies the same content on every machine; a cache miss with an unavailable source fails instead of silently changing the content.
- **Updates:** content changes only when `rev` changes. That changes workspace config and is presented by the existing config-change gate when the effective config differs from its recorded snapshot. The gate is not an unconditional first-run prompt: an empty config is recorded silently, an unchanged config is silent, and `--accept-config-changes` can accept a change without an interactive answer. Imports must resolve only after that gate passes. There is no hourly refresh; pack branch refresh in [`internal/packsrc/refresh.go`](../../internal/packsrc/refresh.go) does not apply.
- **No repository mutation at launch:** a workspace lockfile would have to be written into the checkout, and a user-level one would let a workspace write user-global state keyed by names it chose. Neither happens.
- **`packs.lock.json` is untouched.** It stays keyed by pack name under user authority.

A pin bump in a pull request is as opaque as a submodule bump: the reviewer sees a commit id, not the content. The design does not pretend otherwise. Whether moving refs are allowed remains [OQ-WGS1](#OQ-WGS1).

### 6.2 Fetch is anonymous and inert

A repository-editable URL must not be able to exercise the user's Git authority or run anything on the host. The fetch therefore has these properties.

- **HTTPS only, throughout transport.** `ssh`, `file` and plain `http` URLs are refused. Redirects may continue only to HTTPS; redirects to local/file transport or another scheme fail. URL rewrites cannot convert an HTTPS address into a local or credential-bearing source.
- **Credential-free Git isolation.** The fetch has no credential helpers, prompts, ambient user/system Git configuration or environment-based config injection, URL rewrite rules, hooks, filters or submodule initialization. A source needing authentication fails as unavailable. Private sources are [OQ-WGS2](#OQ-WGS2).
- **Read committed objects without checkout execution.** Skill files come from the specified commit's tree; filters and hooks never run. The resolved object must be a commit and its full object id must equal `rev`.

Credentials and grants are otherwise unchanged. The jail gains no Git access and no new network rule. A fetch never triggers a separate credentials or approval prompt. A config change that adds or changes imports still goes through the existing workspace config-change gate described in [§6.1](#61-the-pin-is-the-lock).

### 6.3 State ownership

| State | Owner | Notes |
|---|---|---|
| Import entries, including pins | The workspace config | The only source of truth. |
| Fetched commits | A user-level cache, shared across workspaces | Keyed by repository and full commit id. It is a cache, not authority: deleting it costs a re-fetch. It holds no user pack lock entry and nothing addressable as a pack name. |
| Staged import layer | The launch's staged tree | Rebuilt from cache for a fresh launch or attach; no source data is written into the workspace. |
| Per-import approval records | None | No import-specific approval state; changed imports are part of the effective workspace config seen by the existing gate. |

## 7. Decision and effects

Validation and launch share one interpretation of an entry. The phases differ only in what they may touch.

| Phase | Kind | Runs in | May touch |
|---|---|---|---|
| 1. Parse and validate | Pure | Config validation, `yolo check`, launch | Nothing. No network, no cache write, no prompt. |
| 2. Config-change gate | Existing gate | Fresh launch | Reads workspace config including consumed includes/local override, compares the effective snapshot, and prompts only when a difference needs approval. This is not guaranteed on every first launch; see [§6.1](#61-the-pin-is-the-lock). |
| 3. Resolve | Effect | Fresh launch only, on the host, after the gate passes | Reads cache or performs one anonymous fetch per uncached pin. No attach fetch. |
| 4. Project | Read-only decision plus tree reads | Fresh launch, attach, `yolo skills list` | Walks the cached commit tree and reads selected blobs. It performs no network or writes. |
| 5. Stage | Effect | Fresh launch and attach | Writes the workspace-derived skills staging area only, never the workspace or selected-pack tree. Attach refreshes workspace skills from cache while selected packs remain from the running jail's `pack-trees/.live` tree. |

Validation never fetches, so a config can be checked offline and in CI. `yolo host` does not read workspace imports, fetch them or deliver them ([§8.3](#83-delivery)). Projection reads the cached tree; only its selection decision is side-effect-free. The existing workspace skill refresh path runs on attach without re-staging selected packs.

## 8. Projection, precedence, delivery

### 8.1 Confinement

Imported skill trees use the confined workspace-skill staging path, with a deliberately stricter link boundary than workspace-owned skills ([`internal/jailcontent/workspaceskills.go`](../../internal/jailcontent/workspaceskills.go)):

- Every symlink target must remain within that one imported skill directory; confinement to the workspace root alone is insufficient for imported content.
- Special files and any entry that fails the confinement/read checks refuse the entire skill directory. No partial skill is delivered; other skills in the import may still be delivered.
- The existing workspace-skills cap is shared by workspace-owned and all imported skills: 32 MiB and 4096 files/directories per launch, counted once over the shared staging input, not multiplied per import or agent destination. Imports get no separate allowance or knob.
- Any failure while copying a skill removes that skill's partial staged output and reports the skill and reason.

### 8.2 Names, collisions, precedence

Skills are delivered under their own directory names, with no handle prefix. An agent invokes a skill by its name, and a prefix would change that name.

Precedence, lowest first:

1. **The import layer.**
2. The workspace's own skills.
3. The built-in skill suite.
4. Shared selected-pack skills, in configured/default selection order.
5. The conventional local pack, last and highest priority.

Two properties follow:

- Workspace-owned skills can shadow imported skills; imported content cannot shadow workspace-owned, built-in, shared-pack or conventional local-pack skills.
- A vendored workspace skill with the same name takes precedence over an import.
- When two imports carry the same name, handle sort order decides the winner and every shadow is reported. Ordering is deterministic and independent of TOML declaration order.

### 8.3 Delivery

- **Agents:** every agent that receives the existing workspace layer at that launch. There are no per-agent audiences, since the manifest that would declare them is not read.
- **Environments:** containers and macos-user, the same as the workspace layer.
- **`yolo host`:** none. [WS-D24](../reference/agent-briefings.md#ws-d24) rules that the host has no workspace skills layer. The import layer is workspace-derived, so it falls under that ruling. No question is raised.

## 9. Defaults, failures, and what the operator sees

### 9.1 Defaults

| Setting | Default |
|---|---|
| Feature for a workspace that declares imports | On. `workspace_skill_imports = false` at user scope turns it off. |
| `path` | `skills` |
| `only` | Absent: all immediate-child skill directories; `[]`: none |
| Refresh | Never. Content moves only with `rev`. |

### 9.2 Failures

Imports are additive, lowest-precedence content. Losing one cannot widen anything. Runtime failures therefore withhold the affected import and let the launch continue, while authoring errors stop validation.

| Situation | Result |
|---|---|
| Malformed entry: bad URL scheme, abbreviated/wrong-format `rev`, duplicate or invalid handle/filter name, bad `path` | Validation error, like any other config error. |
| Workspace `packs` present | Validation error (unchanged), with the new message. |
| Pin not cached and fetch fails (offline, auth required, host down) | That import is withheld with a warning naming the handle and cause. The launch continues. |
| Pin cached, network down | Delivered normally. No network is attempted. |
| Fetched commit is not `rev`, or `rev` does not exist | That import is withheld with an error line. |
| `path` absent in the tree, or contains no skill directory | That import is withheld with an error line. |
| `only` names a skill that is not there | That name is reported. The rest of the import is delivered. |
| Entry refused by confinement | That skill is withheld and reported. |
| Attach to a running jail | Refreshes workspace/import skills through the existing workspace-skill staging path from cache only. It never fetches or re-stages selected packs; those remain from the running jail's `pack-trees/.live` tree. An uncached pin is reported unavailable on attach and can be resolved at a later fresh launch. |
| User veto set | Nothing is fetched or staged. `yolo skills list` says so. |

### 9.3 Observable results

A launch with healthy, unchanged imports prints nothing about them. Routine lines for expected configuration bury real failures. Lines appear only for:

- first delivery of an import: handle, URL, abbreviated display pin, skill count, and whether visible paths outside selected skill directories were left unimported;
- a pin change since the last delivery;
- anything withheld, with the reason;
- shadowed names.

`yolo skills list` is the on-demand view of import configuration, cache state, selected skills, collisions, and visible unimported paths. It does not read manifests, discover plugin components or claim to inventory every non-skill effect; it reports only paths visible in the pinned tree without activating them.

## 10. Migration

### What is revised

| Existing statement | After this design |
|---|---|
| [OQ-PK1](../reference/pack-system.md#oq-pk1): workspace `packs` unavailable | **Stands.** Workspaces still cannot select packs. |
| The rationale in `LoadPacks`' comments: no workspace-writable selection of content entering the jail | **Revised.** The boundary becomes: no workspace-writable selection of *effect-bearing* content. Pinned skill content is allowed through `skill_imports`. |
| The refusal message for workspace `packs` | Reworded to say packs are user-scope and to point at `skill_imports` for skills. |
| [OQ-TP9](trust-paths.md#decision-ledger): no approval at pack install | Untouched and not relied on. It rests on user authority, which this key does not have. |
| [OQ-WS1](../reference/agent-briefings.md#oq-ws1), [WS-D24](../reference/agent-briefings.md#ws-d24) | Untouched. The import layer sits under both. |

### Boundaries that remain

- A workspace cannot select a pack, shipped or fetched.
- A workspace cannot cause a hook, program, MCP server, plugin component, mount, host read, environment change or config surface to be delivered.
- A workspace cannot cause the host to authenticate to anything.
- A workspace cannot write user-global lock or approval state, and a launch cannot write to the workspace.
- Nothing workspace-derived reaches `yolo host`.
- Nothing a workspace supplies outranks user-selected content.

Existing workspace configs without these keys remain unchanged. A build that predates `skill_imports` treats it as an unknown top-level key and refuses validation; there is no silent compatibility path ([§11](#11-code-checks)).

### Non-goals

- Workspace selection of packs in any form. That remains with [`workspace-packs.md`](workspace-packs.md).
- Imports at user scope.
- Briefings, slash commands or any second content kind.
- Per-agent audiences, renaming or exclusion filters.
- Moving-ref tracking at launch, and private sources, unless the rulings below change them.
- Cleaning up or garbage-collecting the shared cache.

## 11. Code checks

The evidence packet named four points to verify. All four are resolved against the current checkout; the constraints below replace the former assumptions.

| Check | Finding and design consequence |
|---|---|
| U1 — config confirmation | The launch gate strictly reads workspace config, including consumed local overrides/includes, and compares it with the stored snapshot. It prompts for a non-empty first config or a changed config, but an empty first config is recorded silently, unchanged config is silent, and `--accept-config-changes` accepts without an interactive answer. Therefore imports resolve only after the gate passes; the design does not claim a prompt on every first launch or before every host-side effect. See [`gateread.go`](../../internal/config/gateread.go), [`snapshot.go`](../../internal/config/snapshot.go), and [`preflight.go`](../../internal/cli/run/preflight.go). |
| U2 — names | No `skills` command is registered, and neither `skill_imports` nor `workspace_skill_imports` is a current config key. These remain proposed names, not existing user surface. See [`dispatch.go`](../../internal/cli/dispatch.go) and [`config.go`](../../internal/config/config.go). |
| U3 — older builds | Top-level unknown keys are validation errors. A build predating the proposed key therefore refuses the workspace config rather than silently ignoring the import or delivering it. See [`validate.go`](../../internal/config/validate.go). |
| U4 — executable bits | The existing confined reader stages any file carrying an execute bit as `0755`; otherwise it stages as `0644`, regardless of source read/write bits. Imported skills inherit this rule, so executable bundled scripts remain executable inside the jail but are never invoked by import. See [`workspaceskills.go`](../../internal/jailcontent/workspaceskills.go). |

Residual risks the design accepts and names:

- Imported skill text can steer the agent, within the jail's existing authority.
- A pin bump is opaque in review unless the reviewer inspects the upstream commit diff.
- A projected skill may depend on something that was not imported.
- An anonymous fetch reveals to the named server that this host fetched that repository.

## 12. Owner ruling

### Owner rulings

| ID | Ruling / Decision | Date | Settled in | Built |
|---|---|---|---|---|
| WGS-R1 | Workspaces may include skills from Git repositories. The governing rule is "no dangerous features from a workspace", not "no outside content from a workspace". Syntax and extent are delegated to this design. | 2026-11-16 | [§1](#1-problem-and-owner-direction) | No |

## 13. Design recommendations (not owner rulings)

| ID | Recommendation | Section |
|---|---|---|
| WGS-P1 | Dedicated `[skill_imports.<handle>]` key. Workspace `packs` stays refused. | [§3](#3-the-call-a-dedicated-key-not-restricted-packs) |
| WGS-P2 | Allow-list projection to skill directories. No manifest is read, there is no dependency closure, and future kinds are refused by default. | [§5](#5-how-far-it-goes) |
| WGS-P3 | Mixed sources are projected, not rejected; visible paths outside selected skills are disclosed without claiming exhaustive manifest/plugin inventory. | [§5.4](#54-mixed-sources-project-or-reject) |
| WGS-P4 | Skills only. `briefing` passes the effect tests but is not admitted. | [§5.2](#52-allowed-and-refused) |
| WGS-P5 | Validation has no fetch effects. Fresh-launch fetch follows a passing config-change gate; the gate is not guaranteed to prompt on every first launch. | [§7](#7-decision-and-effects) |
| WGS-P6 | The import layer is lowest precedence and is not delivered to `yolo host`, under WS-D24. | [§8](#8-projection-precedence-delivery) |
| WGS-P7 | Runtime failures withhold the one import and continue. Authoring errors fail validation. | [§9.2](#92-failures) |
| WGS-P8 | On by default, with a user-scope scalar veto. | [§4.3](#43-scope) |

## 14. Open questions

1. 💬 **[OQ-WGS1](#OQ-WGS1): Must every import carry a commit pin, or may it follow a moving ref?**

   A workspace author wants to share a reviewed set of skills, but today workspace `packs` is refused and no Git skill-import command exists. They add an import and run a fresh launch; the design proposes a full commit id so a teammate receives the same content until the config changes. The choice is whether that explicit pin is mandatory or a moving ref may update the imported bytes.

   - **(a) Pin required.** `rev` is mandatory and `ref` is only a label for `yolo skills update`. No lockfile is needed, nothing refreshes on its own, and every content change is a reviewed config change. The cost is a full object id in config (40 hex digits in this SHA-1 repository) and an explicit command to move it.
   - **(b) Moving refs allowed.** `ref = "main"` with no `rev`, refreshed like user packs. This is shorter to write. It needs lock state somewhere: a committed lockfile means launches or commands write the repository, and a user-level one means a workspace drives user-global state. It also lets a third party change what enters the jail with no change to the workspace and no confirmation.
   - **(c) Both, pin by default.** A moving ref is accepted only with an explicit per-entry opt-in. This keeps (a) as the norm but brings back all of (b)'s state questions for the opt-in case.

   <!-- vantage: question id=OQ-WGS1 leaning="Pin required — the workspace config fully describes delivered content." -->

   _Leaning:_ Pin required — the workspace config fully describes delivered content.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **[OQ-WGS2](#OQ-WGS2): May a workspace import come from a source that needs the user's credentials?**

   A teammate clones a workspace whose config names a private Git repository of skills. The proposed fetch has no credentials or helpers, so it cannot read that repository. The choice is whether imports stay anonymous-only, gain a user-controlled allow-list for private sources, or may use whatever credentials Git on the host can reach.

   - **(a) Anonymous only.** HTTPS with no credentials. A private skill repository is reached by the routes that already exist: user-scope `packs`, or vendoring the skills into the checkout where the workspace layer picks them up.
   - **(b) User-approved hosts.** A user-scope setting names hosts or repositories for which workspace imports may use the user's Git credentials. The workspace still cannot grant this to itself. It adds a user-side key and a second fetch mode.
   - **(c) Whatever the user's Git can reach.** SSH and credential helpers as for user packs. A repository-editable line could then make the host authenticate as the user and pull any repository the user can read into the jail.

   <!-- vantage: question id=OQ-WGS2 leaning="Anonymous-only now — user-approved private-source access is a separate extension." -->

   _Leaning:_ Anonymous-only now — user-approved private-source access is a separate extension.

   **Answer:**
   > _(empty — fill in when decided)_