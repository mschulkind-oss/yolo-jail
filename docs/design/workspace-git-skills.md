---
title: Workspace Git skills use the broker, not unfettered credentials
status: accepted
stage: DECIDED
next: Implemented in commits 0a49b801, 643ff063, a9c186c6 and the current CLI slice; complete the required landing gates and review.
depends-on: []
evidence-date: 2026-11-16
evidence-snapshot: HEAD @ fec51d3229693bf65aa3fbf2bd2dd4ca8a7b5ece
---

# Workspace Git skills use the broker, not unfettered credentials

**Status:** 2026-11-16 (`fec51d3229693bf65aa3fbf2bd2dd4ca8a7b5ece`). The owner rulings are recorded in the [ledger](#12-decision-ledger), and no owner ruling is pending. The command-context contradiction U7 ([§11](#11-code-checks)) is closed by designer recommendation WGS-P15: the commands that fetch or touch host-owned state run on the host, and inside a jail they print the established host-run line and change nothing ([§4.2](#42-cli), [§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker)). The config, brokered state, confined staging and public command group are implemented in commits `0a49b801`, `643ff063`, `a9c186c6` and the current CLI slice; final gates and review remain.


> **In short.** A workspace may select Git skill directories by immutable commit or moving ref, and receive those skills—and nothing else—inside the jail. Resolution must use this workspace’s existing Git-broker access filter; it cannot exercise unfettered host credentials.

**Why it matters.** The blanket rule "a workspace cannot cause outside content to enter the jail" blocks the harmless case (five shared skills) in order to block the dangerous one (a pack carrying hooks, programs, mounts, host reads). The owner has directed that skills are allowed, using pins or moving refs and the existing workspace Git broker filter.

**The shape.** A dedicated key, not restricted `packs`. Entries may identify an immutable full commit or a moving ref. Any resolution/fetch must pass through the existing workspace Git broker filter and cannot bypass or widen its access. An allow-list projection admits only skill directories, staged as the lowest-precedence skill layer, and never delivered to `yolo host`. No pack manifest is read, so no pack effect can ride along. A moving ref's resolved commit is kept in host-side state for this workspace. It advances only at a fresh launch or an explicit `yolo skills` command run on the host, never from inside a jail, and the change is reported when it happens.

**Cost.** One new workspace config key, one small CLI group, one host-side caller of the Git broker's existing decision and upstream leg, a per-workspace object store and resolution record on the host, one projection step on the confined staging path, and a reworded refusal message. It adds no credential, grant, scope entry or approval record.

**Start at [§3](#3-the-call-a-dedicated-key-not-restricted-packs)** — the selection boundary. [§5](#5-how-far-it-goes) defines what crosses it.

**Needs your ruling:** None. The owner has ruled both ref forms and the existing workspace Git broker filter. Where each `yolo skills` command runs and what it may write is designer recommendation WGS-P15 in [§13](#13-design-recommendations-not-owner-rulings); it adds no credential, grant or host operation, so it raises no owner question.

**Reads with:** [`workspace-git-skills-plan.md`](workspace-git-skills-plan.md) (the serial implementation hand-off) · [`pack-system.md` OQ-PK1](../reference/pack-system.md#oq-pk1) (why workspace `packs` is refused) · [`agent-briefings.md` workspace layer](../reference/agent-briefings.md#the-workspace-layer) and [WS-D24](../reference/agent-briefings.md#ws-d24) (skill priority and host exclusion) · [`workspace-packs.md`](workspace-packs.md) (separate open pack-selection design) · [`trust-paths.md` ledger](trust-paths.md#decision-ledger) (user-scope Git authority).

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
| **Transport route** *(coined here)* | The path the bytes take: a host process (a fresh launch, or a host command), then the broker's upstream leg, then the remote. It carries no authority of its own. |
| **Import store** *(coined here)* | Host-side Git objects fetched for this workspace's imports. It is a cache, not an authority, and not a checkout. |
| **Resolution record** *(coined here)* | Host-side state, one per handle, naming the commit an import currently resolves to and the access class it was obtained under. It is not a lockfile in the repository, not `packs.lock.json`, and not an approval record. |
| **Refresh** *(coined here)* | Asking the remote, through the broker decision, what a moving ref points at now, and replacing the resolution record. It is not a re-projection, never happens on attach, and is never performed by a process inside a jail. |
| **Host command** *(coined here)* | A `yolo skills` command that takes effect only when run on the host: `add`, `update` and `pin`. It is not a command the jail forwards; inside a jail it prints a line for a human to run and does nothing else. |
| **Host-run line** | The existing `yolo host-run -C <workspace> -- …` instruction a jailed command prints. It says nothing ran and asks a human to paste one line in a host terminal ([`internal/cli/hostrun.go`](../../internal/cli/hostrun.go)). It is not transparent forwarding and gives the jail process no host process or host write. |
| **Explicit-command authority** *(coined here)* | The request authority a host command uses outside a launch: the Git scope and grants in this workspace's last config approval, less anything no longer current ([§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker)). It is not the operator's own Git access and is never wider than the last approval. |

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

The `yolo skills` command group and both config keys are implemented in the current tree ([§11](#11-code-checks)).

| Command | Effect when run on the host | Inside a jail |
|---|---|---|
| `yolo skills add <url> [--as <handle>] [--ref <ref>] [--track] [--path <dir>] [--only a,b]` | Resolves the ref (default: the remote's default branch, written as its branch name) through the broker decision under explicit-command authority. On success it adds the fetched objects to the import store, writes the resolution record, and writes the entry to the workspace config: without `--track` a pinned entry with `rev` and `ref`, with `--track` a moving-ref entry with `ref` only. If the decision is refused or the fetch fails, it writes nothing anywhere. | Prints the host-run line. No config write, no host write, no network. |
| `yolo skills update [<handle>]` | Acts only on an entry that has a valid resolution record; any other entry is reported as resolving at the next fresh launch and is not contacted. Pinned entry with a `ref` label: re-resolves the label, stores the objects, rewrites `rev` in the workspace config and writes the record. Pinned entry with no label: left alone and reported. Moving-ref entry: forces a refresh of the resolution record; the config is not touched. A refused decision or failed fetch leaves config and record as they were. | Prints the host-run line. No config write, no host write, no network. |
| `yolo skills pin <handle>` | Turns a moving-ref entry into a pinned one by writing the commit in its valid resolution record as `rev` in the workspace config and keeping `ref` as the label. With no valid record it writes nothing and says the import resolves at the next fresh launch. No network; host state is read, not written. | Prints the host-run line. Nothing is written. |
| `yolo skills list` | Per import: mode, configured ref, recorded commit and when it was resolved, access class, whether the commit is in the import store, skills that would be delivered, names shadowed, and visible paths that are not imported. No network, no writes. | Prints each declared import from the workspace config (handle, repository, mode, `ref`/`rev`, `path`, `only`), states that resolved commits and store state are host-side and not visible from inside a jail, and prints the host-run line for the full view. No network, no writes. |
| `yolo skills remove <handle>` | Deletes the entry from the workspace config. The import store and resolution record are left alone ([§10](#10-migration)); a record with no matching entry is ignored. | The same: a workspace-scope config edit, which a jail may already make. Host state is not touched. |

Without `--as`, the default handle is derived from the validated URL's final non-empty repository path component: one terminal `.git` suffix is removed, and the remaining spelling is preserved. If the URL has no valid component, the command refuses before any fetch or write and suggests `--as <handle>`. An explicit handle is validated by the same rules; an existing handle is refused before effects.

Pinning is the default for `add` because a pin is reproducible and visible in review. Tracking is one flag away, since the owner allows both.

**Where the commands run** (designer recommendation WGS-P15). `add`, `update` and `pin` are host commands: each needs a fetch under this workspace's approved Git access, the host-owned resolution record, or both, and a process inside a jail has neither. Run inside a jail, they follow the pattern `yolo pack add` and jailed user-scope `yolo config` edits already use: print `yolo host-run -C <workspace> -- yolo skills <arguments as typed>`, say that nothing ran, and return before any effect. A human starts the host command; the jail is not given a host process, a host write, or any operation on import state. `remove` and `list` need only the workspace config inside a jail, so they run there with the reduced effects in the table.

**What each result says.** Every command reports what it actually changed, and nothing it did not:

- `add` on success prints the same "record created" row a launch would ([§9.3](#93-observable-results)), then says the entry was written to the workspace config and is delivered at the next fresh launch or attach once the config-change gate has approved the changed config. It does not claim any running jail has the skills.
- `update` prints the "record changes commit" row when the commit moved, or says the import is already current. It says running jails keep what they staged until the next attach or fresh launch.
- `pin` names the commit written. `remove` says the entry is gone from the config and that a running jail keeps the staged skills until its next attach or fresh launch.
- A jailed `add`, `update` or `pin` says only that nothing ran and gives the one line to paste. It never reports a resolution, because none happened.

Hand-editing an entry in the workspace config, from either side, is legitimate and fetches nothing. The entry resolves at the next fresh launch, after the config-change gate.

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

**Transport route.** The jail does not exist when a fresh launch resolves imports, and a host command is not in one, so the bytes never travel through the jail-side remote helper. The host process (the launch, or the host command) asks the broker's decision, and the fetch runs on the broker's own upstream leg: host `ssh` for SSH, the host credential helper for HTTPS only when the decision is authenticated. The route carries no authority. Whatever it may do was decided by the request authority above, and running on the host adds nothing to it.

**Smallest integration.** The broker serves requests from the jail-side helper; no host-side caller of its Git protocol service exists today (U5 in [§11](#11-code-checks)). Import resolution needs one host-side caller that reuses the same decision and upstream checks, limited to fetch (`git-upload-pack`). A fresh launch gives it the launch's gate-approved scope and grants; a host command gives it explicit-command authority (below). It is one caller with two sources of the same two inputs. It must not copy the checks or fetch around them, and it is not the user-scope pack fetch.

**No workspace-driven host Git.** The fetch never runs Git inside the checkout and never reads the checkout's Git config, so a workspace cannot steer it with `url.*.insteadOf`, `credential.helper`, `core.sshCommand` or similar settings. It reads committed objects only: no checkout, hooks, filters or submodules, and no pack manifest.

**Commands outside a launch** (designer recommendation WGS-P15). `yolo skills add` and `yolo skills update` resolve imports only when run on the host. No in-jail resolution path exists in this design: the jail-side Git helper carries Git protocol only, and no operation is added that lets a jail process write, or ask the host to write, the import store or a resolution record. The Git broker's operation surface stays fetch and push.

*Explicit-command authority.* A host command is not inside a launch, so it has no live per-launch scope. Its request authority is built from host-owned data only:

- the Git scope and grants in this workspace's last config approval, read once at the start of the command through `config.ApprovedScope(workspace, "git")` and `config.ApprovedGrants(workspace, "git")` ([`internal/config/scopeapproval.go`](../../internal/config/scopeapproval.go));
- less anything that is no longer current. An approved remote URL that is gone from the checkout's remotes, read as text the way the gate reads them, is treated as absent. So is an approved read grant that current user config no longer carries.

Current state can only narrow this authority. A remote or grant added since the last approval counts for nothing until a fresh launch's gate has recorded it. The host command never runs the config-change gate and never writes an approval record.

The command passes that authority to the same host-side caller a launch uses, and takes the same broker decision from the table above. It is not the operator's own Git access: an import URL outside the approved scope gets no credential even though the person at the host terminal could fetch it by hand. The broker's anonymous-HTTPS attempt applies exactly as it does in a launch; it is existing policy for a public URL, not a fallback after a credentialed attempt, and no credential can be acquired during it.

*No approved scope.* A workspace with no approval record has an empty explicit-command authority. An `https://` import gets the anonymous attempt only. An SSH or scp-style import is refused, and the message says this workspace has no approved Git access recorded yet and that a fresh launch records it. It does not suggest adding a remote or a grant.

*Which entries `update` may contact.* `update` acts only on an entry with a valid resolution record ([§6.3](#63-state-and-ownership)). A record exists only because a fresh launch resolved the entry after the gate, or because `add` was run on the host with that URL on its command line. An entry written or changed by hand, including from inside a jail, has no valid record, so no host command contacts its URL; it resolves at the next fresh launch, after the gate.

*Later launches re-decide.* What a host command stores carries its access class, and every launch and attach applies the cache rule in [§6.4](#64-refresh-locking-and-concurrent-launches) under its own live decision. A command that ran under a since-narrowed approval therefore cannot cause delivery the workspace is no longer entitled to.

*Locking.* A host command holds one import transaction lock across declaration recheck, fetch, targeted config edit, record publication and rollback. If a launch holds it, the command waits for the holder rather than skipping, since the operator asked for the resolution explicitly.

*Partial failure.* Fetched objects are add-only. A command publishes its record only after the targeted config edit succeeds; a record-write failure rolls the config edit back under the same transaction. A refused or failed new add leaves no owned import state directory or lock artifact.

### 6.3 State and ownership

The Git broker streams a fetch and keeps no copy, so it has no cache or lock to reuse. The user-scope pack lock (`packs.lock.json`) sits beside user config and rests on user authority, so it is not used either. Imports get their own state, owned by the host and scoped to one workspace.

| State | Owner | Written by | Rule |
|---|---|---|---|
| Import declaration | Workspace config | By hand from either side; `yolo skills add`, `update` (pinned entry with a label) and `pin` on the host; `yolo skills remove` from either side | Names a repository and either a pin or a moving ref. Passes the config-change gate. |
| Import store | Host-side state for this workspace: outside the checkout, not beside user config, the same ownership class as the workspace's config-approval record | The resolve step of a fresh launch; `yolo skills add` and `update` run on the host. Nothing inside a jail | Git objects only, added and never rewritten. A cache, not an authority ([§6.4](#64-refresh-locking-and-concurrent-launches)). Not shared between workspaces, so one workspace's access never supplies another's content. |
| Resolution record | Same owner as the import store | The resolve step of a fresh launch; `yolo skills add` and `update` run on the host. Nothing inside a jail. Read by attach and by host `yolo skills list` and `pin` | One per handle: the URL as configured, repository identity, full ref name (moving only), commit id, access class, and when it was resolved. |
| Staged import layer | The launch's staged tree | Fresh launch and attach | Only projected skills are delivered. The workspace and the selected-pack tree are never touched. |
| Approval | The existing config-change gate | Unchanged | There is no per-import approval record. |

A resolution record is valid only while the entry's `git` and `ref` (or `rev`) still equal what it recorded. If the entry changes, the record is treated as absent. A pinned import has a record too, so that the access class its commit was obtained under is known.

Nothing in this table is in the repository, and nothing in it is user-global. Ownership splits by side. The workspace config is the only import state a process inside a jail can read or write. The import store and resolution record are host-only: not mounted in a jail, not readable there, and not writable there directly or by request, because no host operation on them is exposed to a jail ([§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker)). What a jail receives is the staged import layer, written for it by the host at fresh launch and attach. The host state uses the workspace identity `runtime.FromWorkspace(workspace)`; a dedicated sibling under `paths.GlobalStorage()` is a plausible location, while the exact import-store leaf remains an implementation choice ([§11](#11-code-checks)).

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

**Locking.** One exclusive writer transaction covers fetch-and-record-write and, for host commands, declaration recheck, config edit and rollback. It is held only by a live process. The implementation locks the existing real workspace-parent directory inode read-only, then takes configedit’s workspace-directory lock: no durable lock pathname is created or deleted. This conservatively serializes sibling-workspace writers, an implementation trade-off; their objects and records remain separate. Readers do not take either writer lock.

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
| 3. Resolve | Effect | A fresh launch, after the gate has passed and the launch's approved scope and grants are fixed; or `yolo skills add` / `update` run on the host, under explicit-command authority ([§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker)). Never attach, and never a process inside a jail | Takes the broker decision. A launch uses the network only in the cases listed in [§6.4](#64-refresh-locking-and-concurrent-launches); a host command uses it because it was asked to. Writes the import store and resolution record. A launch never touches the workspace; the host commands edit the workspace config as [§4.2](#42-cli) lists. Neither touches user config or any approval record. |
| 4. Project | Read-only decision plus tree reads | Fresh launch, attach, `yolo skills list` run on the host | Walks the tree of the recorded commit in the import store and reads selected blobs. No network, no writes, no record change. |
| 5. Stage | Effect | Fresh launch and attach | Writes the workspace-derived skills staging area only, never the workspace or selected-pack tree. Attach re-stages from the current record and store; selected packs remain from the running jail's `pack-trees/.live` tree. |

Validation never fetches, so a config can be checked offline and in CI. `yolo host` does not read workspace imports, resolve refs, fetch them or deliver them ([§8.3](#83-delivery)). The explicit commands that both edit workspace config and write host-owned state run on the host only ([§4.2](#42-cli)); inside a jail they print the host-run line and have no effect in any phase. No phase is reachable from a jail process except the workspace-config edits a jail may already make.

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
| Where `yolo skills` commands take effect | `add`, `update`, `pin`: on the host only; inside a jail they print the host-run line. `remove`: either side. `list`: full on the host, declarations only inside a jail. |
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
| User veto set | Nothing is resolved or staged. `yolo skills list` says so. Host `add` and `update` fetch nothing, write nothing, and name the veto. |
| `add`, `update` or `pin` run inside a jail | Nothing runs. The host-run line is printed. No config, host state or network is touched. |
| Host `add` when the decision is refused, the anonymous attempt fails, or the remote cannot be reached | Nothing is written: no entry, no objects, no record. The message gives the reason in the wording of the matching row above. |
| Host `update` when the decision is refused or the fetch fails | Config and record are left as they were, and the handle and reason are reported. Other handles in the same run are still processed. |
| Host `add` or `update` for a workspace with no approval record | HTTPS: anonymous attempt only. SSH or scp-style: refused, with a message that no approved Git access is recorded yet and that a fresh launch records it. |
| Host `update` or `pin` on an entry with no valid resolution record | Not contacted and not changed. The message says the import resolves at the next fresh launch. |
| The last approval lists a remote or grant that is no longer current | Treated as absent by host commands ([§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker)). The import is then decided as out of scope. |
| `yolo skills list` inside a jail | Declarations only, with a statement that resolved state is host-side and the host-run line for the full view. |

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
- A process inside a jail cannot resolve an import, and cannot write or request a write to the import store or a resolution record. Its only import-related write is to the workspace config, which the config-change gate then sees.
- A host `yolo skills` command has no more Git access than the workspace's last approval gave a launch, and never the operator's ambient credentials.
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

The evidence packet named four points to verify. U1–U4 are resolved against the current checkout. The planner's current-tree audit also confirms U5 and U6 below. U7 was a contradiction between the command rules and the host-owned state rules, not an unchecked source seam. It is closed below by designer recommendation WGS-P15, within the owner's settled rulings on ref forms and the existing Git-broker filter, and without a new owner question.

| Check | Finding and design consequence |
|---|---|
| U1 — config confirmation | The launch gate strictly reads workspace config, including consumed local overrides/includes, and compares it with the stored snapshot. It prompts for a non-empty first config or a changed config, but an empty first config is recorded silently, unchanged config is silent, and `--accept-config-changes` accepts without an interactive answer. Therefore imports resolve only after the gate passes; the design does not claim a prompt on every first launch or before every host-side effect. See [`gateread.go`](../../internal/config/gateread.go), [`snapshot.go`](../../internal/config/snapshot.go), and [`preflight.go`](../../internal/cli/run/preflight.go). |
| U2 — names | The `skills` command is registered, and `skill_imports` plus `workspace_skill_imports` are canonical readable config keys with their intended workspace/user scopes. The proposal's names have shipped; see [`dispatch.go`](../../internal/cli/dispatch.go), [`config.go`](../../internal/config/config.go) and [`skillimports.go`](../../internal/config/skillimports.go). |
| U3 — older builds | Top-level unknown keys are validation errors. A build predating the proposed key therefore refuses the workspace config rather than silently ignoring the import or delivering it. See [`validate.go`](../../internal/config/validate.go). |
| U4 — executable bits | The existing confined reader stages any file carrying an execute bit as `0755`; otherwise it stages as `0644`, regardless of source read/write bits. Imported skills inherit this rule, so executable bundled scripts remain executable inside the jail but are never invoked by import. See [`workspaceskills.go`](../../internal/jailcontent/workspaceskills.go). |

| Check | Finding | Design consequence |
|---|---|---|
| U5 — host-side broker entry | `internal/gitbroker/importfetch.go` exposes the host import caller. It narrows approved remotes/grants against current state, uses the existing remote-helper protocol and reuses the broker's decision/credential path; no daemon operation is added. | A fresh launch and explicit host commands call this filtered fetch path; no ambient host Git transport or authority widening is used. |
| U6 — state location | Host workspace identity is `runtime.FromWorkspace(workspace)` ([`naming.go`](../../internal/runtime/naming.go)). The import store and resolution records are implemented in a separate host-only workspace-keyed directory under `paths.GlobalStorage()`; approvals remain under `approvals/`, are not mounted into jails, and have a closed record file list ([`paths.go`](../../internal/paths/paths.go), [`scopeapproval.go`](../../internal/config/scopeapproval.go), [`internal/skillimports`](../../internal/skillimports)). | Import state stays separate from approval records, checkout and user config; jail readers use projected immutable objects only. |

U5 and U6 are code-backed integration facts. U7 is the command execution model built on them. It uses only mechanisms the tree already has and adds no broker operation.

| Check | Finding | Design consequence |
|---|---|---|
| U7 — command context and state effects | The jail form of `yolo host-run` prints a line for a human to paste and runs nothing on the host ([`hostrun.go`](../../internal/cli/hostrun.go)). `yolo pack add`/`remove` and jailed user-scope `yolo config` edits already print that line and return before any effect, while workspace-scope config edits proceed inside a jail ([`packadd.go`](../../internal/cli/packadd.go), [`configeditverbs.go`](../../internal/cli/configeditverbs.go)). The jail-side Git path carries Git protocol only and exposes no write to host state ([`broker.go`](../../internal/gitbroker/broker.go)). `config.ApprovedScope` and `config.ApprovedGrants` report the last approved Git scope and grants to a non-launch host reader ([`scopeapproval.go`](../../internal/config/scopeapproval.go)). Repository-binding ceilings are write-only, so there is no repository-scoped read grant to lean on. | `add`, `update` and `pin` take effect on the host only and print the host-run line inside a jail. `remove` is a workspace-scope config edit on either side. `list` is full on the host and declaration-only inside a jail. Host commands fetch through the U5 caller under explicit-command authority: the last approved scope and grants, narrowed by current state, with the broker's anonymous-HTTPS behavior unchanged. No jail-to-host operation, credential, grant or scope entry is added ([§4.2](#42-cli), [§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker), WGS-P15). |

Residual risks the design accepts and names:

- Imported skill text can steer the agent, within the jail's existing authority.
- A pin bump is opaque in review unless the reviewer inspects the upstream commit diff.
- A moving ref changes delivered instruction text with no config change and no prompt. The move is reported once, by whichever process recorded it.
- Two machines, or two launches either side of a refresh, can deliver different commits of a moving ref.
- A workspace-named HTTPS URL outside scope causes the host to make an anonymous request to that server at launch, after the config-change gate. `yolo skills add` on the host makes the same anonymous request for the URL on its command line before any gate; a human who pastes a host-run line an agent composed should read the URL in it.
- A host command reads the last approval once. Another session rewriting that record during the command is not detected; the command's result is still re-decided by every later launch and attach before anything is delivered.
- Resolving or updating an import from inside a jail is not possible. An agent that wants a newer commit must ask a human to run one line on the host, or wait for the next due refresh at a fresh launch.
- A projected skill may depend on something that was not imported.
- A brokered fetch reveals to the authorized server that the workspace requested that repository.

## 12. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
|---|---|---|---|---|
| WGS-R1 | Workspaces may include Git skills; the governing boundary excludes dangerous pack effects, not outside content as such. | 2026-11-16 | [§1](#1-problem-and-owner-direction) | Yes |
| <a id="OQ-WGS1"></a>[OQ-WGS1](#OQ-WGS1) | Owner: both full commit pins and moving refs are allowed. Pin-only is not the design. The owner left moving-ref state, refresh and approval to the designer; those are recommendations WGS-P9 and WGS-P11 to WGS-P14 in [§13](#13-design-recommendations-not-owner-rulings), not part of this ruling. | 2026-11-16 | [§6.1](#61-both-immutable-pins-and-moving-refs-are-allowed) | Yes |
| <a id="OQ-WGS2"></a>[OQ-WGS2](#OQ-WGS2) | Owner: imports can never use host credentials unfettered; resolution/fetch must be filtered through this workspace’s existing Git broker access. How the broker's existing decision is applied, including its anonymous-HTTPS behavior, is recommendation WGS-P10, not part of this ruling. | 2026-11-16 | [§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker) | Yes |

## 13. Design recommendations (not owner rulings)

| ID | Recommendation | Section |
|---|---|---|
| WGS-P1 | Dedicated `[skill_imports.<handle>]` key. Workspace `packs` stays refused. | [§3](#3-the-call-a-dedicated-key-not-restricted-packs) |
| WGS-P2 | Allow-list projection to skill directories. No manifest is read, there is no dependency closure, and future kinds are refused by default. | [§5](#5-how-far-it-goes) |
| WGS-P3 | Mixed sources are projected, not rejected; visible paths outside selected skills are disclosed without claiming exhaustive manifest/plugin inventory. | [§5.4](#54-mixed-sources-project-or-reject) |
| WGS-P4 | Skills only. `briefing` passes the effect tests but is not admitted. | [§5.2](#52-allowed-and-refused) |
| WGS-P5 | Validation has no fetch effects. Resolution runs only at a fresh launch after the config-change gate, or in an explicit `yolo skills` command run on the host, and always under the broker decision. Attach never resolves, and nothing inside a jail does. | [§7](#7-decision-and-effects) |
| WGS-P6 | The import layer is lowest precedence and is not delivered to `yolo host`, under WS-D24. | [§8](#8-projection-precedence-delivery) |
| WGS-P7 | Authoring errors fail validation. Every runtime failure withholds that one import with a warning, and the launch continues. | [§9.2](#92-failures) |
| WGS-P8 | On by default, with a user-scope scalar veto. | [§4.3](#43-scope) |
| WGS-P9 | `rev` present means pinned; `ref` alone means moving. `yolo skills add` pins by default and `--track` follows a ref. Tags are frozen until an explicit update; branches follow. | [§4](#4-syntax), [§6.1](#61-both-immutable-pins-and-moving-refs-are-allowed) |
| WGS-P10 | An import fetch takes the Git broker's existing decision unchanged, including its anonymous-HTTPS attempt. The import URL never enters scope, no grant is created, and nothing is fetched outside the broker when it refuses. The only integration is one host-side caller of the same decision and upstream leg, used by fresh launches and by host `yolo skills` commands alike. | [§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker) |
| WGS-P11 | Fetched objects and the resolved commit live in host-owned, per-workspace state: an import store and a resolution record. Neither is in the repository, user-global, shared between workspaces, or an approval record. | [§6.3](#63-state-and-ownership) |
| WGS-P12 | A branch is refreshed at a fresh launch on the existing pack refresh interval, or by `yolo skills update`. There is no new setting. One lock covers fetch-and-record-write; readers take none; a busy lock skips a due refresh. | [§6.4](#64-refresh-locking-and-concurrent-launches) |
| WGS-P13 | Approving the entry at the config-change gate covers later movement of its ref. A move raises no prompt and is reported by the process that recorded it. | [§6.1](#61-both-immutable-pins-and-moving-refs-are-allowed), [§9.3](#93-observable-results) |
| WGS-P14 | The import store is a cache, not an authority: stored content is delivered only under a current broker decision at least as strong as the access class it was obtained under. A failed refresh delivers the last recorded commit with a warning. | [§6.4](#64-refresh-locking-and-concurrent-launches) |
| WGS-P15 | `yolo skills add`, `update` and `pin` take effect on the host only; inside a jail they print the existing host-run line and change nothing. `remove` edits workspace config on either side; `list` is full on the host and declaration-only in a jail. A host command fetches under the workspace's last approved Git scope and grants, narrowed by current state and never widened, through the same caller as a launch. `update` contacts only entries that already have a valid resolution record. No jail-to-host operation on import state exists. | [§4.2](#42-cli), [§6.2](#62-git-access-is-filtered-by-the-existing-workspace-broker), [§6.3](#63-state-and-ownership) |
