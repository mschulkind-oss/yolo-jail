---
title: Workspace Git skills use the broker, not unfettered credentials
status: accepted
stage: DECIDED
next: Write the implementation plan. Its first step checks the three code seams U5–U7 in §11; none of them changes a decision here.
depends-on: []
evidence-date: 2026-11-16
evidence-snapshot: HEAD @ c3ac9c649
---

# Workspace Git skills use the broker, not unfettered credentials

**Status:** 2026-11-16 (`c3ac9c649`). Both owner rulings are recorded in the [ledger](#12-decision-ledger). The mechanics they left to the designer are closed in [§6](#6-git-addressing-ref-forms-access-and-state), [§7](#7-decision-and-effects) and [§9](#9-defaults-failures-and-what-the-operator-sees) as designer recommendations ([§13](#13-design-recommendations-not-owner-rulings)), not owner rulings. Three code seams are named but unchecked ([§11](#11-code-checks)). Nothing is built.

> **In short.** A workspace may select Git skill directories by immutable commit or moving ref, and receive those skills—and nothing else—inside the jail. Resolution must use this workspace’s existing Git-broker access filter; it cannot exercise unfettered host credentials.

**Why it matters.** The blanket rule "a workspace cannot cause outside content to enter the jail" blocks the harmless case (five shared skills) in order to block the dangerous one (a pack carrying hooks, programs, mounts, host reads). The owner has directed that skills are allowed, using pins or moving refs and the existing workspace Git broker filter.

**The shape.** A dedicated key, not restricted `packs`. Entries may identify an immutable full commit or a moving ref. Any resolution/fetch must pass through the existing workspace Git broker filter and cannot bypass or widen its access. An allow-list projection admits only skill directories, staged as the lowest-precedence skill layer, and never delivered to `yolo host`. No pack manifest is read, so no pack effect can ride along. A moving ref's resolved commit is kept in host-side state for this workspace. It advances only at a fresh launch or an explicit command, and the change is reported when it happens.

**Cost.** One new workspace config key, one small CLI group, one host-side caller of the Git broker's existing decision and upstream leg, a per-workspace object store and resolution record on the host, one projection step on the confined staging path, and a reworded refusal message. It adds no credential, grant, scope entry or approval record.

**Start at [§3](#3-the-call-a-dedicated-key-not-restricted-packs)** — the selection boundary. [§5](#5-how-far-it-goes) defines what crosses it.

**Needs your ruling:** None. The owner has ruled both ref forms and the broker boundary. The remaining mechanics are settled here as designer recommendations WGS-P9 to WGS-P14, which the owner may overturn.

**Reads with:** [`pack-system.md` OQ-PK1](../reference/pack-system.md#oq-pk1) (why workspace `packs` is refused) · [`agent-briefings.md` workspace layer](../reference/agent-briefings.md#the-workspace-layer) and [WS-D24](../reference/agent-briefings.md#ws-d24) (skill priority and host exclusion) · [`workspace-packs.md`](workspace-packs.md) (separate open pack-selection design) · [`trust-paths.md` ledger](trust-paths.md#decision-ledger) (user-scope Git authority).

---

## 1. Problem and owner direction

Workspace `packs` is refused today: `validatePacks` hard-errors on the key, and `LoadPacks` reads only the user file ([`internal/config/packs.go`](../../internal/config/packs.go), [`internal/config/workspacescope.go`](../../internal/config/workspacescope.go)). The ruling behind it is [OQ-PK1](../reference/pack-system.md#oq-pk1) (2026-10-23): "none of the options for now", with a dedicated design requested.

On 2026-11-16 the owner set the direction this document works from, recorded as WGS-R1 in the [ledger](#12-decision-ledger):

- The rule should not be "workspaces may not select packs" in the blanket sense.
- It should be "workspaces may not include things whose features are dangerous".
- Skills from a Git repository are not dangerous in that sense and should be allowed.

This document does not ask that permission again. The owner has ruled on both ref forms and the credential boundary. Everything else in this document is design work the owner delegated, and is labelled as a recommendation.

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
| **Entry selection** *(coined here)* | What an import entry chooses: one repository, one commit (named directly or through a ref), one `path` and an optional name filter. It is a config decision that passes the config-change gate. It is not the projection, which only reads what was selected. |
| **Repository identity** | The normalized name the Git broker uses for grants: lowercased host, explicit port kept, user-info dropped, one trailing `.git` removed ([`internal/gitproto/identity.go`](../../internal/gitproto/identity.go)). It is not the URL used to connect, and equal identity does not admit a URL. |
| **Broker decision** *(coined here)* | The Git broker's existing answer for one fetch of one URL under this workspace's approved scope and the user's read grant: authenticated, anonymous attempt only, or refused ([§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker)). It is not a new check and not the admission test above. |
| **Access class** *(coined here)* | Which broker decision content was obtained under: `authenticated` or `anonymous`. It is recorded with the content and is not a grant. |
| **Request authority** *(coined here)* | The inputs that decide whether a fetch may happen: the workspace's gate-approved repository scope and the user's read grant. It is not the process that carries the bytes. |
| **Transport route** *(coined here)* | The path the bytes take: host launch process, then the broker's upstream leg, then the remote. It carries no authority of its own. |
| **Import store** *(coined here)* | Host-side Git objects fetched for this workspace's imports. It is a cache, not an authority, and not a checkout. |
| **Resolution record** *(coined here)* | Host-side state, one per handle, naming the commit an import currently resolves to and the access class it was obtained under. It is not a lockfile in the repository, not `packs.lock.json`, and not an approval record. |
| **Refresh** *(coined here)* | Asking the remote, through the broker decision, what a moving ref points at now, and replacing the resolution record. It is not a re-projection and never happens on attach. |

## 3. The call: a dedicated key, not restricted `packs`

The repository has user-scope Git packs and a jail-only workspace skill reader, but neither selects third-party Git skills from workspace config. The separate proposal in [`workspace-packs.md`](workspace-packs.md) remains about pack selection, not this skill-only boundary.

- **User-scope `packs` with a Git source** fetches and delivers a whole pack with every effect. It rests on the user's own authority ([OQ-TP9](trust-paths.md#decision-ledger)), which a repository-editable key does not have.
- **The confined workspace skills layer** delivers skills safely, but only from directories already in the checkout.
- **[workspace-packs.md](workspace-packs.md)** proposes workspace selection of shipped packs by bare name and leans against fetched ones. It is unbuilt and in review.

The confined staging discipline is reused. The fetch goes through the Git broker's decision and upstream leg, not through the user-scope pack fetch, which runs on the user's own authority ([§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker)). The selection semantics are new.

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

A moving-ref entry has `ref` and no `rev`:

```toml
[skill_imports.acme-main]
git = "https://github.com/acme/agent-skills"
ref = "main"                    # moving form: follows the branch
```

The mode is read from the fields alone. `rev` present means pinned; `rev` absent means moving ([§6.1](#61-both-immutable-pins-and-moving-refs-are-allowed)).

| Field | Required | Rule |
|---|---|---|
| `<handle>` | yes | Same character rules as a `pack_sources` key. Unique by construction, since it is a table key. |
| `git` | yes | A repository address in a transport the Git broker supports: `ssh://`, `git+ssh://`, `ssh+git://`, `https://`, or scp-style `[user@]host:path`. Local paths, `http://`, `git://`, `file://`, remote-helper forms and URLs carrying credentials are validation errors, decided from the text alone. A valid address grants nothing; whether it can be fetched is the broker decision in [§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker). |
| `rev` | Required for a pinned import | Full commit object id, with width and hexadecimal alphabet defined by the repository object format. Abbreviated ids are validation errors. When present, the import is pinned and this commit is the only content delivered. |
| `ref` | Required when `rev` is absent | A branch or tag name, short (`main`, `v2`) or full (`refs/heads/main`, `refs/tags/v2`). Other ref namespaces and `HEAD` are validation errors. Without `rev` it selects a moving-ref import. With `rev` it is only a label that `yolo skills update` uses to re-pin; a launch never resolves it or checks it against `rev`. An entry with neither field is a validation error. |
| `path` | no | Repository-relative directory whose immediate children are the skills. No absolute path, no `..`, no `.git`. This is the same discipline as `project_dirs` ([`internal/packdecl/projectdirs.go`](../../internal/packdecl/projectdirs.go)). |
| `only` | no | Unique immediate-child skill directory basenames, not paths: nonempty, not `.` or `..`, with no `/` or `\`. Absent means all; an explicit `[]` means none. Duplicate or invalid names are validation errors. A valid name absent from the resolved tree is reported at resolution; other listed skills remain eligible. |

Why a keyed table rather than a list: generic config merge union-merges lists and merges tables by key ([`internal/config/load.go`](../../internal/config/load.go)). A table lets a local override file change one import's source/ref selection, and gives every message and command a stable name. An absent `only` means all directories only when no lower config layer supplies that field; an explicit empty list means no names at that layer. Because list merge is union, an override cannot clear names supplied by a lower layer. Duplicate TOML table definitions are parse errors; duplicate names inside `only` are validation errors.

The `git+…//subdir?ref=…` address form used by packs is not accepted here. Separate fields keep the selected source and ref mode visible in review: a diff that removes `rev` turns a pin into a moving ref, and says so on its face.

### 4.2 CLI

The `yolo skills` command group and both config keys are proposed names; none is registered in the current CLI or schema ([§11](#11-code-checks)).

| Command | Effect |
|---|---|
| `yolo skills add <url> [--as <handle>] [--ref <ref>] [--track] [--path <dir>] [--only a,b]` | Resolves the ref (default: the remote's default branch, written as its branch name) through the broker decision. Without `--track` it writes a pinned entry with `rev` and `ref`. With `--track` it writes a moving-ref entry with `ref` only. |
| `yolo skills update [<handle>]` | Pinned entry with a `ref` label: re-resolves the label and rewrites `rev` in the config. Pinned entry with no label: left alone and reported. Moving-ref entry: forces a refresh of the resolution record; the config is not touched. |
| `yolo skills pin <handle>` | Turns a moving-ref entry into a pinned one by writing the commit in its resolution record as `rev` and keeping `ref` as the label. No network. |
| `yolo skills list` | Per import: mode, configured ref, recorded commit and when it was resolved, access class, whether the commit is in the import store, skills that would be delivered, names shadowed, and visible paths that are not imported. No network. |
| `yolo skills remove <handle>` | Deletes the entry. The import store is left alone ([§10](#10-migration)). |

Pinning is the default for `add` because a pin is reproducible and visible in review. Tracking is one flag away, since the owner allows both.

`add`, `update` on a pinned entry, `pin` and `remove` are explicit operator commands that edit the workspace config file. They are the only path by which this feature writes to the repository. A launch never does.

Every network step in these commands goes through the broker decision in [§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker). No command runs host Git with the operator's ambient credentials.

Every message that suggests a next step names one of these commands. The workspace config is ordinary config and may be written by hand, but no message tells anyone to hand-edit the resolution record, the import store, an approval record or `.git/config`.

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

The owner explicitly allows both full commit pins and moving refs ([OQ-WGS1](#OQ-WGS1)). The pin-only recommendation is withdrawn. The rest of this subsection is designer recommendation.

| | Pinned import | Moving-ref import |
|---|---|---|
| Entry | `rev`, optional `ref` label | `ref`, no `rev` |
| Content delivered | Exactly the `rev` commit | The commit in the resolution record ([§6.3](#63-state-and-ownership)) |
| Changes when | The config changes, which the gate sees | A refresh finds the ref at a different commit; the config does not change |
| Same content on every machine | Yes | No |
| Network at launch | Only if the commit is not yet in the import store | Only when a refresh is due or nothing is recorded yet ([§6.4](#64-refresh-locking-and-concurrent-launches)) |

**Ref names.** A full name (`refs/heads/…`, `refs/tags/…`) is used as written. A short name is tried as a branch and as a tag. If both exist the import is withheld, and the message asks for the full name; the design does not pick one. An annotated tag resolves to the commit it points at. The resolution record stores the full name that was found.

**Tags are frozen, branches follow.** A ref that resolves to a tag is resolved once and kept until `yolo skills update`. A ref that resolves to a branch is refreshed on the schedule in [§6.4](#64-refresh-locking-and-concurrent-launches). This is the rule user-scope packs already follow for tags and branches.

**What the gate approves.** The config-change gate shows and approves the entry: this repository, this ref, followed over time. That approval covers later movement of the ref. A moved target raises no prompt and creates no approval record; it is reported when it happens ([§9.3](#93-observable-results)). Asking again at every move would make a moving ref a pin with extra steps, which is not what the owner allowed.

**Reproducibility.** A pinned import is reproducible everywhere. A moving-ref import is not: two machines, or one machine on two days, can deliver different commits. Between refreshes on one host, one workspace delivers one commit. `yolo skills list` shows that commit, and `yolo skills pin <handle>` writes it into the config when the team wants everyone on it.

No moving-ref mechanism writes to the workspace, changes a committed file, or turns the import into user-global authority.

### 6.2 Git access is filtered by the existing workspace broker

Workspace imports may never use host Git credentials unfettered; resolution and fetch are filtered through this workspace's existing Git broker access ([OQ-WGS2](#OQ-WGS2)). An import declaration cannot grant itself access, broaden the workspace's existing access, bypass the broker, or invoke a host Git helper. The rest of this subsection is designer recommendation on how that ruling is met.

**Request authority.** A fetch for an import is decided by the same two inputs that decide a fetch the agent makes from inside the jail ([`git-broker.md`](../reference/git-broker.md#admission-scope-and-read-grant)):

- the workspace's repository scope, which is the remote URLs in the checkout's `.git/config`, read as text and approved at the config-change gate;
- the user's Git read grant, which a workspace cannot create.

The import's `git` URL is never added to that scope. It is only asked about. No grant, scope entry or config key is created or changed for imports.

**The broker decision.** For one import URL the broker's existing rules give one of three answers. The import path takes that answer and adds no rule of its own.

| Import URL | Broker decision | Result for the import |
|---|---|---|
| Exact URL is in the approved scope, and the user read grant covers its repository identity | Authenticated fetch | Fetched with access class `authenticated` |
| `https://` URL that does not meet the row above | Anonymous attempt only; no credential can be acquired during the request | Fetched with access class `anonymous` if the server serves it without credentials; otherwise withheld |
| SSH or scp-style URL that does not meet the first row | Refused | Withheld |
| Unsupported transport, or credentials in the URL | Refused | Already a validation error ([§4.1](#41-toml-workspace-config)) |

Scope matching and identity are separate checks, as they are today: a URL whose repository identity equals an in-scope remote's is not thereby in scope.

The anonymous row is current broker behavior, not a new allowance. The agent inside the jail can already make that same anonymous fetch. Inheriting it means a public skill repository needs no remote, grant or credential, and a private one works only where the workspace already has authenticated access to it.

**No route around the decision.** When the decision is refused, or an anonymous attempt fails, the import is withheld. The import path never fetches directly, never retries outside the broker, and never falls back to the user-scope pack fetch or its lock.

**Transport route.** The jail does not exist when a fresh launch resolves imports, so the bytes do not travel through the jail-side remote helper. The host launch process asks the broker's decision, and the fetch runs on the broker's own upstream leg: host `ssh` for SSH, the host credential helper for HTTPS only when the decision is authenticated. The route carries no authority. Whatever it may do was decided by the request authority above.

**Smallest integration.** The evidence shows the broker serving the jail-side helper and does not show a host-side caller. The integration this design asks for is one host-side caller of the same decision function and the same upstream leg, limited to fetch (`git-upload-pack`), and given the launch's gate-approved scope and grants. It is not a second copy of the checks. Whether such an entry exists today is unchecked (U5 in [§11](#11-code-checks)).

**No workspace-driven host Git.** The fetch never runs Git inside the checkout and never reads the checkout's Git config, so a workspace cannot steer it with `url.*.insteadOf`, `credential.helper`, `core.sshCommand` or similar settings. It reads committed objects only: no checkout, hooks, filters or submodules, and no pack manifest.

**Commands outside a launch.** `yolo skills add` and `update` use the same decision. Run on the host, they use the workspace's last approved scope and the user read grant, which are the inputs an unchanged next launch would use; with no approved scope yet, only the anonymous row is available. Run inside a jail, they go through that jail's own broker access, and they can edit the workspace config but cannot write host state. Where these commands run and whether the approved scope is readable outside a launch are unchecked (U7 in [§11](#11-code-checks)).

### 6.3 State and ownership

The Git broker streams a fetch and keeps no copy, so it has no cache or lock to reuse. The user-scope pack lock (`packs.lock.json`) sits beside user config and rests on user authority, so it is not used either. Imports get their own state, owned by the host and scoped to one workspace.

| State | Owner | Written by | Rule |
|---|---|---|---|
| Import declaration | Workspace config | The operator, or the config-editing `yolo skills` commands | Names a repository and either a pin or a moving ref. Passes the config-change gate. |
| Import store | Host-side state for this workspace: outside the checkout, not beside user config, the same ownership class as the workspace's config-approval record | The resolve step of a fresh launch; host-side `yolo skills add` and `update` | Git objects only, added and never rewritten. A cache, not an authority ([§6.4](#64-refresh-locking-and-concurrent-launches)). Not shared between workspaces, so one workspace's access never supplies another's content. |
| Resolution record | Same owner as the import store | Same writers | One per handle: the URL as configured, repository identity, full ref name (moving only), commit id, access class, and when it was resolved. |
| Staged import layer | The launch's staged tree | Fresh launch and attach | Only projected skills are delivered. The workspace and the selected-pack tree are never touched. |
| Approval | The existing config-change gate | Unchanged | There is no per-import approval record. |

A resolution record is valid only while the entry's `git` and `ref` (or `rev`) still equal what it recorded. If the entry changes, the record is treated as absent. A pinned import has a record too, so that the access class its commit was obtained under is known.

Nothing in this table is in the repository, and nothing in it is user-global. The agent inside the jail cannot write any of it. The exact location of this per-workspace host state is unchecked (U6 in [§11](#11-code-checks)).

**Immutable staging.** Projection and staging read the tree of one commit id from the import store. A commit id names fixed content, so what a launch stages cannot change underneath it, even while another process adds objects or replaces the record.

### 6.4 Refresh, locking and concurrent launches

This subsection is designer recommendation.

**When the network is used.** A fresh launch, after the config-change gate, does this for each import:

1. Take the broker decision for the entry's URL. This is a local check. If it is refused, the import is withheld and nothing stored is used.
2. Find the commit wanted. For a pin it is `rev`. For a moving ref it is the commit in a valid resolution record, unless a refresh is due.
3. Use the network only if one of these holds: the commit is not in the import store; there is no valid record; a refresh is due; or the stored content needs a higher access class than the current decision gives (see "Cache is not authority" below).

A healthy pinned import therefore uses the network once. A healthy moving-ref import uses it when a refresh is due.

**When a refresh is due.** A branch is refreshed at a fresh launch once its record is older than the refresh interval user-scope packs already use ([`pack-system.md`](../reference/pack-system.md#fetch-refresh-lock)). It is the same constant, not a second value and not a setting. A tag is never due. `yolo skills update` forces a refresh of either. Attach never refreshes.

**Cache is not authority.** Stored content is delivered only when the current broker decision is at least the access class it was obtained under:

| Stored access class | Current broker decision | Result |
|---|---|---|
| `anonymous` | Authenticated or anonymous | Usable |
| `authenticated` | Authenticated | Usable |
| `authenticated` | Anonymous only | Must be obtained again anonymously before delivery. If that succeeds the record is rewritten as `anonymous`; if not, the import is withheld. |
| Either | Refused | Withheld |

So removing a remote from the workspace, or the user withdrawing the read grant, stops delivery of content that was only reachable with credentials, even though the objects are still on disk.

**When the remote cannot be reached.** If the broker decision allows the fetch but the refresh fails (offline, server error, ref deleted upstream), a moving-ref import with a valid record and a usable stored commit delivers that commit, with a warning. With nothing usable, the import is withheld. Either way the launch continues ([§9.2](#92-failures)).

**Locking.** One exclusive lock per workspace import state covers fetch-and-record-write, and nothing else. It is held only by a live process.

- Objects are added, never rewritten. A record is replaced whole, so a reader sees the old record or the new one, never a mixture.
- Readers take no lock. Projection, attach and `yolo skills list` read the record once and then read by commit id.
- A launch that wants to refresh and finds the lock held does not wait. It uses the current record as if no refresh were due.
- A launch with no usable record and the lock held waits for the holder, then reads what the holder wrote. If the holder failed, it tries once itself.
- Two launches of one workspace may therefore stage different commits only if a refresh lands between their reads. Each stages one whole commit.

## 7. Decision and effects

Validation and launch share one interpretation of an entry. The phases differ only in what they may touch.

| Phase | Kind | Runs in | May touch |
|---|---|---|---|
| 1. Parse and validate | Pure | Config validation, `yolo check`, launch | Nothing. No network, no cache write, no prompt. |
| 2. Config-change gate | Existing gate | Fresh launch | Reads workspace config including consumed includes/local override, compares the effective snapshot, and prompts only when a difference needs approval. This is not guaranteed on every first launch; see [§6.1](#61-both-immutable-pins-and-moving-refs-are-allowed). |
| 3. Resolve | Effect | Fresh launch only, after the gate has passed and the launch's approved scope and grants are fixed | Takes the broker decision, and uses the network only in the cases listed in [§6.4](#64-refresh-locking-and-concurrent-launches). Writes the import store and resolution record. Never touches the workspace, user config or any approval record. |
| 4. Project | Read-only decision plus tree reads | Fresh launch, attach, `yolo skills list` | Walks the tree of the recorded commit in the import store and reads selected blobs. No network, no writes, no record change. |
| 5. Stage | Effect | Fresh launch and attach | Writes the workspace-derived skills staging area only, never the workspace or selected-pack tree. Attach re-stages from the current record and store; selected packs remain from the running jail's `pack-trees/.live` tree. |

Validation never fetches, so a config can be checked offline and in CI. `yolo host` does not read workspace imports, resolve refs, fetch them or deliver them ([§8.3](#83-delivery)).

Entry selection and projection are different things. Entry selection is the config decision of which repository, commit, path and names; it is what the gate approves and what resolve acts on. Projection has no effect of its own: given one commit, it decides which directories are skills, and it cannot fetch, resolve or record anything.

**Attach.** Attach never resolves, refreshes, fetches or writes a resolution record. It passes the config-change gate as it does today, takes the broker decision against live scope (a local check), and re-stages from the current record and import store under the access-class rule in [§6.4](#64-refresh-locking-and-concurrent-launches). If another launch has advanced the record since this jail started, attach stages the newer commit. An import with no valid record or no stored commit is withheld on attach with a line saying it resolves at the next fresh launch.

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
| Ref forms | Full commit pins and moving refs are both permitted. `rev` present means pinned; `ref` alone means moving. |
| `yolo skills add` | Writes a pin. `--track` writes a moving ref. |
| Moving-ref refresh | Branch: at a fresh launch once the record is older than the existing pack refresh interval, or on `yolo skills update`. Tag: only on `yolo skills update`. Never on attach. |
| Prompt when a moving ref moves | None. The move is reported ([§9.3](#93-observable-results)). |

### 9.2 Failures

Imports are additive, lowest-precedence content. Losing one cannot widen anything. So the rule is: authoring errors stop validation; every runtime failure withholds that one import, prints a warning naming the handle and the reason, and lets the launch continue. No runtime failure of an import stops a launch, and none is silent.

| Situation | Result |
|---|---|
| Malformed entry: unsupported transport, credentials in the URL, invalid handle or filter name, bad `path`, abbreviated or malformed `rev`, invalid `ref`, or neither `rev` nor `ref` | Validation error, like any other config error. |
| Workspace `packs` present | Validation error (unchanged), with the new message. |
| Broker decision is refused (for example an SSH URL that is not an approved workspace remote, or no user read grant) | Withheld. Nothing stored is used. The warning says the repository is outside this workspace's Git access; it does not suggest a way to widen that access. |
| Anonymous attempt fails because the server wants credentials | Withheld, with the same wording. No retry with credentials and no fetch outside the broker. |
| Broker decision allows the fetch, but the remote cannot be reached or the refresh fails | Moving ref with a usable recorded commit: that commit is delivered, with a warning that it may be stale. Pinned commit already in the store: delivered, with no warning, since no network was needed. Otherwise withheld. |
| Stored content was obtained with credentials and the workspace no longer has authenticated access | Obtained again anonymously if the broker allows an anonymous attempt and it succeeds; otherwise withheld ([§6.4](#64-refresh-locking-and-concurrent-launches)). |
| The server will not serve the pinned commit, or what arrives is not the `rev` commit | Withheld. The tip of the `ref` label is never delivered in its place. |
| Moving ref's short name matches both a branch and a tag | Withheld; the message asks for the full ref name. |
| Moving ref no longer exists upstream | Treated as a failed refresh: the last recorded commit is delivered with a warning naming the missing ref, or the import is withheld if there is none. |
| Moving ref now points at a different commit | Delivered at the new commit and reported ([§9.3](#93-observable-results)). Rewritten history is treated the same as any other move. |
| `path` absent in the tree, or contains no skill directory | That import is withheld with an error line. |
| `only` names a skill that is not there | That name is reported. The rest of the import is delivered. |
| Entry refused by confinement | That skill is withheld and reported. |
| Attach to a running jail | Never fetches, refreshes or re-stages selected packs. Imports are re-staged from the current record and store under the current broker decision; one with nothing usable is withheld until the next fresh launch ([§7](#7-decision-and-effects)). Selected packs remain from the running jail's `pack-trees/.live` tree. |
| Another launch holds the import lock | A due refresh is skipped and the current record is used. With no usable record, this launch waits for the holder ([§6.4](#64-refresh-locking-and-concurrent-launches)). |
| User veto set | Nothing is resolved or staged. `yolo skills list` says so. |

### 9.3 Observable results

A launch with healthy, unchanged imports prints nothing about them. Routine lines for expected configuration bury real failures. Lines are printed only for these events, as labeled rows in the style of the Git broker's launch disclosure:

- **A resolution record is created.** Handle, repository identity, ref or pin, commit id, access class, skill count, and whether visible paths outside selected skill directories were left unimported.
- **A resolution record changes commit.** Handle, ref, previous commit id and new commit id. This is the report of a moving ref's move.
- **A stale commit is delivered** because a refresh failed, with the reason.
- **Anything is withheld**, with the reason.
- **Names are shadowed.**

The first two are printed by whatever writes the record: the launch that resolved it, or the `yolo skills` command that did. A launch that only reads a record another process wrote prints nothing for it. `yolo skills list` always shows the current commit and when it was resolved.

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
- A workspace cannot bypass the workspace Git broker or cause host Git credentials to be used outside the broker’s existing access filter. An import entry adds nothing to repository scope and creates no grant.
- Content fetched for one workspace is not delivered to another, and content fetched with credentials is not delivered once the workspace loses that access.
- A workspace cannot write user-global lock or approval state, and a launch cannot write to the workspace.
- Nothing workspace-derived reaches `yolo host`.
- Nothing a workspace supplies outranks user-selected content.

Existing workspace configs without these keys remain unchanged. A build that predates `skill_imports` treats it as an unknown top-level key and refuses validation; there is no silent compatibility path ([§11](#11-code-checks)).

### Non-goals

- Workspace selection of packs in any form. That remains with [`workspace-packs.md`](workspace-packs.md).
- Imports at user scope.
- Briefings, slash commands or any second content kind.
- Per-agent audiences, renaming or exclusion filters.
- Any change to the Git broker's scope, grants or decision rules, including its anonymous-HTTPS behavior. Imports use them as they are.
- A setting for the refresh interval, or a per-move approval prompt.
- Cleaning up or garbage-collecting the import store.

## 11. Code checks

The evidence packet named four points to verify. All four (U1–U4) are resolved against the current checkout; the constraints below replace the former assumptions. U5–U7 were raised by the broker integration and are not yet checked.

| Check | Finding and design consequence |
|---|---|
| U1 — config confirmation | The launch gate strictly reads workspace config, including consumed local overrides/includes, and compares it with the stored snapshot. It prompts for a non-empty first config or a changed config, but an empty first config is recorded silently, unchanged config is silent, and `--accept-config-changes` accepts without an interactive answer. Therefore imports resolve only after the gate passes; the design does not claim a prompt on every first launch or before every host-side effect. See [`gateread.go`](../../internal/config/gateread.go), [`snapshot.go`](../../internal/config/snapshot.go), and [`preflight.go`](../../internal/cli/run/preflight.go). |
| U2 — names | No `skills` command is registered, and neither `skill_imports` nor `workspace_skill_imports` is a current config key. These remain proposed names, not existing user surface. See [`dispatch.go`](../../internal/cli/dispatch.go) and [`config.go`](../../internal/config/config.go). |
| U3 — older builds | Top-level unknown keys are validation errors. A build predating the proposed key therefore refuses the workspace config rather than silently ignoring the import or delivering it. See [`validate.go`](../../internal/config/validate.go). |
| U4 — executable bits | The existing confined reader stages any file carrying an execute bit as `0755`; otherwise it stages as `0644`, regardless of source read/write bits. Imported skills inherit this rule, so executable bundled scripts remain executable inside the jail but are never invoked by import. See [`workspaceskills.go`](../../internal/jailcontent/workspaceskills.go). |

Unchecked seams. Each has a stated rule either way, so none changes a decision; the plan checks them first.

| Check | What is unknown | Rule either way |
|---|---|---|
| U5 — host-side broker entry | Whether the broker's decision function and upstream leg can be called from the host launch process, or only through the jail-side helper. | If absent, add one caller of the same function, fetch only. Never copy the checks or fetch around them ([§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker)). |
| U6 — state location | Where per-workspace host state such as the config-approval record lives, and whether the import store and resolution record can sit beside it. | Host-owned, per workspace, outside the checkout, not beside user config ([§6.3](#63-state-and-ownership)). |
| U7 — command context | Whether `yolo skills` commands run on the host, in a jail, or both, and whether the approved scope is readable outside a launch. | Host: last approved scope plus user read grant, else the anonymous row only. Jail: the jail's own broker access, and no host-state writes ([§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker)). |

Residual risks the design accepts and names:

- Imported skill text can steer the agent, within the jail's existing authority.
- A pin bump is opaque in review unless the reviewer inspects the upstream commit diff.
- A moving ref changes delivered instruction text with no config change and no prompt. The move is reported once, by whichever process recorded it.
- Two machines, or two launches either side of a refresh, can deliver different commits of a moving ref.
- A workspace-named HTTPS URL outside scope causes the host to make an anonymous request to that server at launch, after the config-change gate.
- A projected skill may depend on something that was not imported.
- A brokered fetch reveals to the authorized server that the workspace requested that repository.

## 12. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
|---|---|---|---|---|
| WGS-R1 | Workspaces may include Git skills; the governing boundary excludes dangerous pack effects, not outside content as such. | 2026-11-16 | [§1](#1-problem-and-owner-direction) | No |
| <a id="OQ-WGS1"></a>[OQ-WGS1](#OQ-WGS1) | Owner: both full commit pins and moving refs are allowed. Pin-only is not the design. The owner left moving-ref state, refresh and approval to the designer; those are recommendations WGS-P9 and WGS-P11 to WGS-P14 in [§13](#13-design-recommendations-not-owner-rulings), not part of this ruling. | 2026-11-16 | [§6.1](#61-both-immutable-pins-and-moving-refs-are-allowed) | No |
| <a id="OQ-WGS2"></a>[OQ-WGS2](#OQ-WGS2) | Owner: imports can never use host credentials unfettered; resolution/fetch must be filtered through this workspace’s existing Git broker access. How the broker's existing decision is applied, including its anonymous-HTTPS behavior, is recommendation WGS-P10, not part of this ruling. | 2026-11-16 | [§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker) | No |

## 13. Design recommendations (not owner rulings)

| ID | Recommendation | Section |
|---|---|---|
| WGS-P1 | Dedicated `[skill_imports.<handle>]` key. Workspace `packs` stays refused. | [§3](#3-the-call-a-dedicated-key-not-restricted-packs) |
| WGS-P2 | Allow-list projection to skill directories. No manifest is read, there is no dependency closure, and future kinds are refused by default. | [§5](#5-how-far-it-goes) |
| WGS-P3 | Mixed sources are projected, not rejected; visible paths outside selected skills are disclosed without claiming exhaustive manifest/plugin inventory. | [§5.4](#54-mixed-sources-project-or-reject) |
| WGS-P4 | Skills only. `briefing` passes the effect tests but is not admitted. | [§5.2](#52-allowed-and-refused) |
| WGS-P5 | Validation has no fetch effects. Resolution runs only at a fresh launch after the config-change gate, or in an explicit command, and always under the broker decision. Attach never resolves. | [§7](#7-decision-and-effects) |
| WGS-P6 | The import layer is lowest precedence and is not delivered to `yolo host`, under WS-D24. | [§8](#8-projection-precedence-delivery) |
| WGS-P7 | Authoring errors fail validation. Every runtime failure withholds that one import with a warning, and the launch continues. | [§9.2](#92-failures) |
| WGS-P8 | On by default, with a user-scope scalar veto. | [§4.3](#43-scope) |
| WGS-P9 | `rev` present means pinned; `ref` alone means moving. `yolo skills add` pins by default and `--track` follows a ref. Tags are frozen until an explicit update; branches follow. | [§4](#4-syntax), [§6.1](#61-both-immutable-pins-and-moving-refs-are-allowed) |
| WGS-P10 | An import fetch takes the Git broker's existing decision unchanged, including its anonymous-HTTPS attempt. The import URL never enters scope, no grant is created, and nothing is fetched outside the broker when it refuses. The only integration is one host-side caller of the same decision and upstream leg. | [§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker) |
| WGS-P11 | Fetched objects and the resolved commit live in host-owned, per-workspace state: an import store and a resolution record. Neither is in the repository, user-global, shared between workspaces, or an approval record. | [§6.3](#63-state-and-ownership) |
| WGS-P12 | A branch is refreshed at a fresh launch on the existing pack refresh interval, or by `yolo skills update`. There is no new setting. One lock covers fetch-and-record-write; readers take none; a busy lock skips a due refresh. | [§6.4](#64-refresh-locking-and-concurrent-launches) |
| WGS-P13 | Approving the entry at the config-change gate covers later movement of its ref. A move raises no prompt and is reported by the process that recorded it. | [§6.1](#61-both-immutable-pins-and-moving-refs-are-allowed), [§9.3](#93-observable-results) |
| WGS-P14 | The import store is a cache, not an authority: stored content is delivered only under a current broker decision at least as strong as the access class it was obtained under. A failed refresh delivers the last recorded commit with a warning. | [§6.4](#64-refresh-locking-and-concurrent-launches) |
