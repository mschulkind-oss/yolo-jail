---
title: Workspace Git skills use the broker, not unfettered credentials
status: draft
stage: SKETCH
next: Designer closes moving-ref state, Git-broker integration, and resulting approval/disclosure mechanics before a plan is written.
depends-on: []
evidence-date: 2026-11-16
evidence-snapshot: HEAD @ f282b84ea
---

# Workspace Git skills use the broker, not unfettered credentials

**Status:** 2026-11-16 (`f282b84ea`). The original code checks remain bounded evidence, but the owner rulings changed the pinning and fetch-authority assumptions; moving-ref state and Git-broker integration still need design work. Nothing is built.

> **In short.** A workspace may select Git skill directories by immutable commit or moving ref, and receive those skills—and nothing else—inside the jail. Resolution must use this workspace’s existing Git-broker access filter; it cannot exercise unfettered host credentials.

**Why it matters.** The blanket rule "a workspace cannot cause outside content to enter the jail" blocks the harmless case (five shared skills) in order to block the dangerous one (a pack carrying hooks, programs, mounts, host reads). The owner has directed that skills are allowed, using pins or moving refs and the existing workspace Git broker filter.

**The shape.** A dedicated key, not restricted `packs`. Entries may identify an immutable full commit or a moving ref. Any resolution/fetch must pass through the existing workspace Git broker filter and cannot bypass or widen its access. An allow-list projection admits only skill directories, staged as the lowest-precedence skill layer, and never delivered to `yolo host`. No pack manifest is read, so no pack effect can ride along. The state and approval mechanics for moving refs remain for the designer to close.

**Cost.** One new workspace config key, one small CLI group, one projection step on the existing host-side Git fetch and confined staging paths, and a reworded refusal message. It adds no new credential/grant authority. Moving-ref state, refresh and approval mechanics are not yet designed.

**Start at [§3](#3-the-call-a-dedicated-key-not-restricted-packs)** — the selection boundary. [§5](#5-how-far-it-goes) defines what crosses it.

**Needs your ruling:** None. The owner has ruled both ref forms and the broker boundary; the designer now closes the remaining mechanics.

**Reads with:** [`pack-system.md` OQ-PK1](../reference/pack-system.md#oq-pk1) (why workspace `packs` is refused) · [`agent-briefings.md` workspace layer](../reference/agent-briefings.md#the-workspace-layer) and [WS-D24](../reference/agent-briefings.md#ws-d24) (skill priority and host exclusion) · [`workspace-packs.md`](workspace-packs.md) (separate open pack-selection design) · [`trust-paths.md` ledger](trust-paths.md#decision-ledger) (user-scope Git authority).

---

## 1. Problem and owner direction

Workspace `packs` is refused today: `validatePacks` hard-errors on the key, and `LoadPacks` reads only the user file ([`internal/config/packs.go`](../../internal/config/packs.go), [`internal/config/workspacescope.go`](../../internal/config/workspacescope.go)). The ruling behind it is [OQ-PK1](../reference/pack-system.md#oq-pk1) (2026-10-23): "none of the options for now", with a dedicated design requested.

On 2026-11-16 the owner set the direction this document works from, recorded as WGS-R1 in the [ledger](#12-decision-ledger):

- The rule should not be "workspaces may not select packs" in the blanket sense.
- It should be "workspaces may not include things whose features are dangerous".
- Skills from a Git repository are not dangerous in that sense and should be allowed.

This document does not ask that permission again. The owner has ruled on both ref forms and the credential boundary; the remaining work is design completion, not another owner ruling.

### What "not dangerous" means here

The design separates two things that the phrase "skills are harmless" can blur.

| | Content influence | Effect authority |
|---|---|---|
| What it is | Text and files the agent may read and choose to follow | Host reads/writes, mounts, credentials, network grants, config changes, anything that runs without the agent choosing it |
| Already admitted from a workspace? | Yes. The repository's own skills reach the jail via the confined workspace layer ([OQ-WS1](../reference/agent-briefings.md#oq-ws1)) | No |
| This design | Extends it to third-party Git content selected by a pin or moving ref | Grants none |

An imported skill **can** influence the agent. It is instruction text, and the agent acts with whatever authority the jail already has. That is the same class of influence a repository already exerts through its own skills and files. It is not a new authority, and it is not a reason to refuse the feature. The design does not claim skills are inert. It guarantees that an import adds no effect authority.

## 2. Terms

| Term | Meaning |
|---|---|
| **Import** *(coined here)* | One `[skill_imports.<handle>]` entry naming a Git repository, pinned commit or moving ref, path and optional name filter. It is not a pack selection or a general-purpose Git source. |
| **Handle** *(coined here)* | The entry's table key, used in messages and commands. It is not a namespace, repository name or pack name. |
| **Pinned import** *(coined here)* | An import whose selected content is identified by a full Git commit object id. It is not an import that tracks a moving ref. |
| **Moving-ref import** *(coined here)* | An import that names a ref whose target may change over time. It is not content reproducibly identified by that ref name alone. |
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

**Decision: A.** The owner’s direction is skills, not pack effects. A dedicated key copies only what it recognises and keeps `packs` meaning unchanged.

Consequences of this recommendation:

- Workspace `packs` remains a hard validation error. Its message changes ([§10](#10-migration)).
- [`workspace-packs.md`](workspace-packs.md) and its [OQ-WP1](workspace-packs.md#OQ-WP1), [OQ-WP2](workspace-packs.md#OQ-WP2), [OQ-WP3](workspace-packs.md#OQ-WP3), [OQ-WP4](workspace-packs.md#OQ-WP4) and [OQ-WP5](workspace-packs.md#OQ-WP5) are neither accepted nor rejected here. Its leaning against fetched workspace packs stays coherent, because an import is not a pack.
- The comment in [`internal/packsrc/lock.go`](../../internal/packsrc/lock.go) that a workspace cannot name a pack stays true.

## 4. Syntax

### 4.1 TOML (workspace config)

```toml
[skill_imports.acme]
git  = "https://github.com/acme/agent-skills"
rev  = "3f9c2b7e1d4a5c6b8e9f0a1b2c3d4e5f60718293"   # immutable pinned form
path = "skills"                 # optional; default "skills"
only = ["review", "triage"]     # optional; default: every skill directory under path
```

A moving-ref entry uses `ref` instead of `rev`; the exact resolved-target state and refresh contract are not yet designed.

| Field | Required | Rule |
|---|---|---|
| `<handle>` | yes | Same character rules as a `pack_sources` key. Unique by construction, since it is a table key. |
| `git` | yes | A repository address handled only through the existing workspace Git-broker access filter. Accepted URL/SSH/HTTPS forms and identity mapping follow that broker’s current contract; this design does not independently grant host credentials. |
| `rev` | One of `rev` or moving `ref` | Full commit object id, with width and hexadecimal alphabet defined by the repository object format. Pin and moving-ref modes are both allowed; exact mutual-exclusion and moving-ref representation/state rules are designer work. |
| `ref` | One of `rev` or moving `ref` | Tag or branch name. With a pin, it may label the pin; without one, it selects a moving-ref import whose resolution/refresh behavior remains to be designed. |
| `path` | no | Repository-relative directory whose immediate children are the skills. No absolute path, no `..`, no `.git`. This is the same discipline as `project_dirs` ([`internal/packdecl/projectdirs.go`](../../internal/packdecl/projectdirs.go)). |
| `only` | no | Unique immediate-child skill directory basenames, not paths: nonempty, not `.` or `..`, with no `/` or `\`. Absent means all; an explicit `[]` means none. Duplicate or invalid names are validation errors. A valid name absent from the resolved tree is reported at resolution; other listed skills remain eligible. |

Why a keyed table rather than a list: generic config merge union-merges lists and merges tables by key ([`internal/config/load.go`](../../internal/config/load.go)). A table lets a local override file change one import's source/ref selection, and gives every message and command a stable name. An absent `only` means all directories only when no lower config layer supplies that field; an explicit empty list means no names at that layer. Because list merge is union, an override cannot clear names supplied by a lower layer. Duplicate TOML table definitions are parse errors; duplicate names inside `only` are validation errors.

The `git+…//subdir?ref=…` address form used by packs is not accepted here. Separate fields keep the selected source and ref mode visible. Exact interaction of pin and moving-ref fields is part of the remaining design work.

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

## 6. Git: addressing, ref forms, access, and state

### 6.1 Both immutable pins and moving refs are allowed

The owner explicitly allows both full commit pins and moving refs. A pinned import identifies immutable content. A moving-ref import follows a named ref whose target may change. The pin-only recommendation is withdrawn.

A pin change is a workspace-config change and remains subject to the existing config-change gate. A moving ref can resolve to different content without a config-text change; how that content change is noticed, represented in state, approved, refreshed, and observed is not settled here. Do not claim deterministic delivery, a refresh schedule, a lockfile/cache policy, or an approval prompt for such a change until the designer specifies those mechanics.

No moving-ref mechanism may write to the workspace, silently change a committed file, or convert the import into user-global authority. The exact representation and state ownership are design work, not an owner question.

### 6.2 Git access is filtered by the existing workspace broker

Workspace imports may never use host Git credentials unfettered. Repository resolution and fetching must be mediated by the Git broker’s access filter for this workspace. An import declaration cannot grant itself access, broaden the workspace’s existing access, bypass the broker, or invoke an arbitrary host Git helper.

Authenticated access is available only to the extent the existing workspace Git broker authorizes it. Do not add a separate credential/grant/config route or change any live credential, grant, or config as part of this design update. Exact protocol handoff, repository identity resolution, filtering, cache interaction, and denial behavior require read-only grounding and designer completion.

The safety boundary still holds: read committed Git objects without checkout execution; do not run hooks or filters, initialize submodules, or interpret pack manifests. The host fetch path must not become an alternate authority channel.

### 6.3 State ownership is incomplete for moving refs

| State | Owner | Settled constraint |
|---|---|---|
| Import declaration | Workspace config | Names a repository and either an immutable pin or moving ref. |
| Resolved ref target | Not yet assigned | Must be designed without workspace writes or user-global authority; moving refs make this necessary. |
| Fetched objects/cache | Existing Git-broker/cache mechanism to be grounded | Must remain cache, not authority, and access must stay workspace-filtered. |
| Staged import layer | Launch's staged tree | Only projected skills are delivered; never mutate workspace or selected-pack tree. |
| Per-import approval record | Not decided | Do not add one by assumption; reconcile moving-ref changes with the existing config-change gate in the design. |

The designer’s next action is to ground these constraints in current Git-broker integration and specify the resolution, refresh, approval, cache, and failure contract before a plan is written.

## 7. Decision and effects

Validation and launch share one interpretation of an entry. The phases differ only in what they may touch.

| Phase | Kind | Runs in | May touch |
|---|---|---|---|
| 1. Parse and validate | Pure | Config validation, `yolo check`, launch | Nothing. No network, no cache write, no prompt. |
| 2. Config-change gate | Existing gate | Fresh launch | Reads workspace config including consumed includes/local override, compares the effective snapshot, and prompts only when a difference needs approval. This is not guaranteed on every first launch; see [§6.1](#61-both-immutable-pins-and-moving-refs-are-allowed). |
| 3. Resolve | Effect | Fresh launch only, after the applicable approval/access checks | Must use the workspace-filtered Git broker. Exact moving-ref timing and cache behavior remain design work. No arbitrary host credential path. |
| 4. Project | Read-only decision plus tree reads | Fresh launch, attach, `yolo skills list` | Walks the cached commit tree and reads selected blobs. It performs no network or writes. |
| 5. Stage | Effect | Fresh launch and attach | Writes the workspace-derived skills staging area only, never the workspace or selected-pack tree. Attach refreshes workspace skills from cache while selected packs remain from the running jail's `pack-trees/.live` tree. |

Validation never fetches, so a config can be checked offline and in CI. `yolo host` does not read workspace imports, resolve refs, fetch them or deliver them ([§8.3](#83-delivery)). Projection reads the cached tree; only its selection decision is side-effect-free. The existing workspace skill refresh path runs on attach without re-staging selected packs; whether it can resolve or refresh a moving ref is not yet decided.

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
| Ref forms | Full commit pins and moving refs are both permitted. |
| Moving-ref refresh | Not yet designed; no cadence or launch behavior is claimed. |

### 9.2 Failures

Imports are additive, lowest-precedence content. Losing one cannot widen anything. Authoring errors stop validation. Runtime denial, withholding and launch-continuation behavior must follow the existing broker contract and are not yet specified.

| Situation | Result |
|---|---|
| Malformed entry: unsupported source/ref form, invalid handle/filter name, bad `path`, or invalid pin | Validation error, like any other config error. Accepted source/ref syntax follows the existing workspace Git broker; exact validation mapping is part of the design work. |
| Workspace `packs` present | Validation error (unchanged), with the new message. |
| Broker denies access, source is unavailable, or resolution/fetch fails | The import must not bypass the broker; withholding, warning severity and launch-continuation behavior are to be specified against current broker failure patterns. |
| Cached objects exist but broker access has changed or the ref moved | Cache authorization, freshness, and delivery behavior are unresolved; a cache must not bypass the current workspace access filter. |
| A pinned commit is not the requested `rev`, or a moving ref cannot resolve | The import must not deliver different content silently; exact withholding/report behavior is part of the moving-ref and broker failure design. |
| `path` absent in the tree, or contains no skill directory | That import is withheld with an error line. |
| `only` names a skill that is not there | That name is reported. The rest of the import is delivered. |
| Entry refused by confinement | That skill is withheld and reported. |
| Attach to a running jail | Must preserve the rule that attach never fetches or re-stages selected packs; whether a moving ref is resolved from cache or left unchanged is part of the design work. Selected packs remain from the running jail's `pack-trees/.live` tree. |
| User veto set | Nothing is resolved or staged. `yolo skills list` says so. |

### 9.3 Observable results

A launch with healthy, unchanged imports prints nothing about them. Routine lines for expected configuration bury real failures. The designer must align moving-ref changes and broker denials with the existing Git-broker disclosure contract; exact user-visible event conditions remain to be specified. When disclosure is warranted, it covers:

- first delivery of an import: handle, source/ref identity, resolved-target display, skill count, and whether visible paths outside selected skill directories were left unimported;
- a resolved-content change since the last delivery, with the exact condition depending on the moving-ref state design;
- anything withheld, with the reason;
- shadowed names.

`yolo skills list` is the on-demand view of import configuration, cache state, selected skills, collisions, and visible unimported paths. It does not read manifests, discover plugin components or claim to inventory every non-skill effect; it reports only paths visible in the resolved tree without activating them.

## 10. Migration

### What is revised

| Existing statement | After this design |
|---|---|
| [OQ-PK1](../reference/pack-system.md#oq-pk1): workspace `packs` unavailable | **Stands.** Workspaces still cannot select packs. |
| The original workspace `packs` refusal rationale in `LoadPacks` comments | **Revised.** The boundary excludes workspace-selected effect-bearing content, while a dedicated skills-only key is allowed. |
| The refusal message for workspace `packs` | Reworded to say packs are user-scope and to point at `skill_imports` for skills. |
| [OQ-TP9](trust-paths.md#decision-ledger): no approval at pack install | Untouched and not relied on. It rests on user authority, which this key does not have. |
| [OQ-WS1](../reference/agent-briefings.md#oq-ws1), [WS-D24](../reference/agent-briefings.md#ws-d24) | Untouched. The import layer sits under both. |

### Boundaries that remain

- A workspace cannot select a pack, shipped or fetched.
- A workspace cannot cause a hook, program, MCP server, plugin component, mount, host read, environment change or config surface to be delivered.
- A workspace cannot bypass the workspace Git broker or cause host Git credentials to be used outside the broker’s existing access filter.
- A workspace cannot write user-global lock or approval state, and a launch cannot write to the workspace.
- Nothing workspace-derived reaches `yolo host`.
- Nothing a workspace supplies outranks user-selected content.

Existing workspace configs without these keys remain unchanged. A build that predates `skill_imports` treats it as an unknown top-level key and refuses validation; there is no silent compatibility path ([§11](#11-code-checks)).

### Non-goals

- Workspace selection of packs in any form. That remains with [`workspace-packs.md`](workspace-packs.md).
- Imports at user scope.
- Briefings, slash commands or any second content kind.
- Per-agent audiences, renaming or exclusion filters.
- Moving-ref resolution, refresh, approval, cache semantics and Git-broker integration; these are the next design action, not settled details.
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
- A pin bump is opaque in review unless the reviewer inspects the upstream commit diff; moving refs also need an explicit change/approval disclosure contract.
- A projected skill may depend on something that was not imported.
- A brokered fetch reveals to the authorized server that the workspace requested that repository.

## 12. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
|---|---|---|---|---|
| WGS-R1 | Workspaces may include Git skills; the governing boundary excludes dangerous pack effects, not outside content as such. | 2026-11-16 | [§1](#1-problem-and-owner-direction) | No |
| <a id="OQ-WGS1"></a>[OQ-WGS1](#OQ-WGS1) | Both full commit pins and moving refs are allowed. Pin-only is not the design; moving-ref state, refresh, and approval mechanics remain for the designer to specify. | 2026-11-16 | [§6](#6-git-addressing-ref-forms-access-and-state) | No |
| <a id="OQ-WGS2"></a>[OQ-WGS2](#OQ-WGS2) | Imports can never use host credentials unfettered; resolution/fetch must be filtered through this workspace’s existing Git broker access. | 2026-11-16 | [§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker) | No |

## 13. Design recommendations (not owner rulings)

| ID | Recommendation | Section |
|---|---|---|
| WGS-P1 | Dedicated `[skill_imports.<handle>]` key. Workspace `packs` stays refused. | [§3](#3-the-call-a-dedicated-key-not-restricted-packs) |
| WGS-P2 | Allow-list projection to skill directories. No manifest is read, there is no dependency closure, and future kinds are refused by default. | [§5](#5-how-far-it-goes) |
| WGS-P3 | Mixed sources are projected, not rejected; visible paths outside selected skills are disclosed without claiming exhaustive manifest/plugin inventory. | [§5.4](#54-mixed-sources-project-or-reject) |
| WGS-P4 | Skills only. `briefing` passes the effect tests but is not admitted. | [§5.2](#52-allowed-and-refused) |
| WGS-P5 | Validation has no fetch effects. Resolution must follow applicable config approval and workspace-broker access checks; exact moving-ref sequencing remains design work. | [§7](#7-decision-and-effects) |
| WGS-P6 | The import layer is lowest precedence and is not delivered to `yolo host`, under WS-D24. | [§8](#8-projection-precedence-delivery) |
| WGS-P7 | Authoring errors fail validation. Runtime denial, withholding and continuation behavior must follow the existing broker contract and be specified by the designer. | [§9.2](#92-failures) |
| WGS-P8 | On by default, with a user-scope scalar veto. | [§4.3](#43-scope) |
