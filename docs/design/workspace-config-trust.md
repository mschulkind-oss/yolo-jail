---
title: "Who may hand a jail a writable host path from inside a workspace — scope, trust, or both?"
date: 2026-09-25
status: draft
tags: [trust, config, scope, workspace, mounts, consent, gate-placement, icebox]
summary: "The maintainer asked whether yolo should add a mise-style trust layer so a workspace config can declare read-write context mounts, and whether such grants should be allowed only in the untracked local file. Neither half works alone. The local file answers the cloned-repo author and not the in-jail agent, because the local file is writable from inside the jail, even under `workspace_readonly`. A trust record answers the agent, but on a cloned repo it becomes the prompt that OQ-TP9 and the Safehouse comparison both said not to build. The recommendation is to combine them, with each gating a different actor. Grants are refused in the committed file. They are admitted from the local file only against a host-side trust record, written by a new `yolo trust` verb, keyed on the workspace path and a hash of the grant set. They stay free in user config. The tracked-file check is demoted to a warning. Proposed and iceboxed; the maintainer has not decided whether to build it."
---

# Who may hand a jail a writable host path from inside a workspace — scope, trust, or both?

**Status:** DESIGN, 2026-09-25 — iceboxed candidate; the maintainer has not decided whether to build it. Nothing
built. Evidence checked against the tree at `71acddac` plus the working tree on this date. It cites symbols, not
line numbers.

> **In short.** "Only in the untracked local file" and "only after `yolo trust`" each stop one actor and miss
> the other. The local file keeps a *repo author's* grant from arriving by `git clone`. Only a host-side record
> stops *the agent in the jail*, which can write the local file for the next launch. So use both, one per actor,
> and let no single mechanism pretend to cover the pair.

**Why it matters.** [`context-mounts.md`](context-mounts.md) proposes a writable form of `mounts` and leaves
[the question of where one may be declared](context-mounts.md#22-where-an-rw-mount-may-be-declared-deferred) to
this doc. The tree's only precedent is `cache_relocations`, which is user-scope-only *because* "a workspace config
is agent-editable, so it cannot grant read-write host mounts" (`validateCacheRelocations`).

**The shape.** Three places a grant might be written, with one rule each: user config needs nothing, the local
file needs a trust record, and the committed file is refused. The record is written only by a host-side
`yolo trust`, and every launch reads it.

**Cost.** One new host-side record, one verb, and a ceremony that the *human* also pays when they write the
local file themselves. That cost is real ([§4.3](#43-the-honest-cost-the-human-pays-the-agents-toll)).

**Start at [§2](#2-two-actors-and-the-one-thing-each-can-write).** The rest falls out of which actor can write
which file.

**Needs your ruling:** [OQ-WT1](#OQ-WT1), [OQ-WT2](#OQ-WT2), [OQ-WT3](#OQ-WT3), [OQ-WT4](#OQ-WT4), [OQ-WT5](#OQ-WT5), [OQ-WT6](#OQ-WT6), [OQ-WT7](#OQ-WT7), [OQ-WT8](#OQ-WT8), [OQ-WT9](#OQ-WT9); and [OQ-AS3](../research/agent-safehouse.md#OQ-AS3), which is filed in `agent-safehouse.md` and which [OQ-WT8](#OQ-WT8) asks whether to absorb.

**Reads with:** [`context-mounts.md`](context-mounts.md) (owns the *mount* mechanics; this doc owns only its
[§2.2](context-mounts.md#22-where-an-rw-mount-may-be-declared-deferred) trust predicate), [`config-safety.md`](../reference/config-safety.md) (the config-change approval gate that
already exists, and that this design has to justify itself against),
[`gate-placement-principle.md`](../reference/gate-placement-principle.md) (Test 1, which every option is
scored on), [`trust-paths.md`](trust-paths.md) ([OQ-TP6](trust-paths.md#decision-ledger) and
[OQ-TP9](trust-paths.md#decision-ledger)), [`agent-safehouse.md`](../research/agent-safehouse.md)
([OQ-AS3](../research/agent-safehouse.md#OQ-AS3) and [§7.2](../research/agent-safehouse.md#72-where-they-genuinely-disagree)/[§9](../research/agent-safehouse.md#9-negative-space--what-not-to-adopt), the argument this doc answers). **No `-plan.md`
sketch exists.** It was deferred because this doc was written under a one-new-file constraint.

---

## Defined terms

- **Committed file** — `yolo-jail.jsonc` (or its `yolo-jail.json` fallback) in the workspace root. It is
  conventionally tracked in version control, and this doc treats it as the file that travels with the repo.
- **Local file** — `yolo-jail.local.jsonc` (or its `yolo-jail.local.json` fallback)
  (`config.WorkspaceLocalConfigName`). It merges over the committed file, and wins. It is conventionally
  untracked, and nothing enforces that ([§1.2](#12-nothing-keeps-the-local-file-untracked)).
- **Workspace scope** — both files together, plus whatever they include: the result of
  `config.LoadWorkspaceConfig`. The local file is workspace scope in every existing reader and refusal
  ([§1.1](#11-the-local-file-is-workspace-scope-everywhere)).
- **User scope** — `~/.config/yolo-jail/config.jsonc`, plus a `--user-layer` file when one is passed. Writing it
  takes host-user authority.
- **Inexpressible** — the existing term ([`storage-and-config.md`](../reference/storage-and-config.md)) for a key
  whose reader reads only the user file. A workspace value is not refused so much as never consulted, and a
  validator adds a `user-scope only` error so the user is told.
- **Approval gate** — the existing config-change approval ([`config-safety.md`](../reference/config-safety.md)).
  At every fresh launch it diffs the canonical workspace config against a host-side record and asks y/N. With no
  TTY it refuses unless `--accept-config-changes` is passed.
- **Grant** *(coined here)* — a config element that gives the jail a host capability the tree currently refuses
  to take from workspace scope. The first and motivating member is an rw context mount
  ([context-mounts §2.1](context-mounts.md#21-config-shape)). [§6](#6-what-a-trusted-grant-could-unlock) lists
  the candidates.
- **Grant set** *(coined here)* — the grants one config file declares, after `~` expansion and symlink
  resolution on the host, in canonical order.
- **Trust record** *(coined here)* — a host-side file saying that the human approved a given grant set for a given
  workspace. Only `yolo trust` writes it. [§4](#4-the-recommendation-c--scope-for-the-author-trust-for-the-agent)
  specifies it.
- **Trust predicate** — [context-mounts §2.2](context-mounts.md#22-where-an-rw-mount-may-be-declared-deferred)'s
  term for the one question this doc must answer: *is this grant from a trusted source?*

## 1. What exists today

### 1.1 The local file is workspace scope, everywhere

`LoadWorkspaceConfig` loads the committed file and then the local file with one shared cycle set, and merges
them with the local file winning. Every workspace-scope refusal reads that combined result, and the tree tests the
local-file case explicitly. `TestValidateCacheRelocationsWorkspaceLocalScopeAlsoRejected` and
`TestWorkspaceLocalConfigProviderAddressIsRefusedToo` pin it, and `workspaceLoopholeEntries` walks both names.
`yolo check` reads both files, because a 2026-09-08 measurement found an unknown key in the local file passing
check and then refusing the launch (the comment in `check.go`). The approval gate diffs both, and its
non-interactive refusal names the local file's path specifically (`snapshot.go`). So **the local file carries no
extra authority today, and no less.** It is a second workspace file.

### 1.2 Nothing keeps the local file untracked

No production code asks git about it: no `check-ignore` and no `ls-files` outside tests. yolo does run git
elsewhere (`version`, `srcskew`, `macosuser.real`), so a git probe would not be yolo's first. What the tree
has:

- `yolo config-ref` says "keep it out of git (global gitignore)". That is advice.
- `yolo init` writes the committed file, including any `--mount` entries **into its `mounts` block**. It appends
  only `.yolo/` to `.gitignore`, never the local file.
- This repo's own `.gitignore` ignores `yolo-jail.local.jsonc` and **not** the `yolo-jail.local.json` fallback
  that `LoadWorkspaceConfig` also reads (`git check-ignore` on both, 2026-09-25).
- `yolo apply --sealed` refuses when the local file is present, calling it "gitignored, machine-local". That is a
  description of the convention, not a check of it.

By contrast, `.yolo/` *is* made un-committable by construction. `paths.EnsureWorkspaceStateDir` writes a
self-ignoring `.gitignore` (`*`) inside it, "however the repo's own .gitignore is written". That precedent matters
for [§3.1](#31-option-a--scope-only-grants-only-in-the-local-file).

### 1.3 The local file is writable from inside the jail, even under `workspace_readonly`

`/workspace` is bind-mounted read-write. `workspace_readonly` lays `:ro` binds over the listed paths *and over
`yolo-jail.jsonc`* (`workspaceReadonlyMountArgs`), and it names no other config file. The local file, both `.json`
fallbacks and any `include_if_found` target inside the workspace stay writable. An absent local file can simply be
created. **So an in-jail agent can always write a local file that the next host launch merges.** The approval gate
is what stands between that write and the launch ([`config-safety.md`](../reference/config-safety.md) P2).

### 1.4 The approval gate is already a per-workspace, content-keyed, host-side consent record

This is the fact any trust proposal must face first. The gate already:

- keeps its record host-side, under `paths.ApprovalsDir()`, keyed by the workspace's container name, and
  never mounted into a jail ([OQ-D1](../reference/config-safety.md#oq-d1));
- compares canonical JSON (`SnapshotJSON`) of *resolved* workspace config, so a changed include re-asks;
- asks a fresh workspace on its first launch, which covers a cloned repo ([OQ-S3](../reference/config-safety.md#oq-s3));
- refuses without a TTY unless `--accept-config-changes` is passed. That approval is a flag rather than an env var
  on purpose, so it cannot be inherited ([OQ-D2](../reference/config-safety.md#oq-d2));
- runs on every backend, macos-user included.

It is mise's paranoid mode applied to a whole file ([Appendix B](#appendix-b-mise-and-direnv)). So *"add a trust
layer"* cannot mean *"add a content-keyed consent record"*, because one exists. What the gate lacks is
**differentiation**. A diff adding a package and a diff adding a writable host path look alike, get the same y/N,
and are both granted by `--accept-config-changes`. The tree's own response is to treat the gate as
insufficient for exactly this class: "A key that would be unsafe to read from the workspace is refused by scope
validation instead" (config-safety, *What this does not license*). Every user-scope-only key exists because
somebody judged this gate not enough.

### 1.5 The scope table, for the keys that reach the host

| Key | Workspace scope today | Why (from the refusal text) |
|---|---|---|
| `mounts` (ro only) | allowed, behind the approval gate | none; [OQ-AS3](../research/agent-safehouse.md#OQ-AS3) asks |
| `env_sources` | allowed, behind the approval gate | none; [OQ-AS3](../research/agent-safehouse.md#OQ-AS3) asks |
| `devices`, `gpu`, `kvm`, `network` | allowed, behind the approval gate | not argued anywhere found |
| `cache_relocations` (rw host mount) | refused | agent-editable, so no rw host mounts |
| source-bearing `host_files` | refused | travels with the repo and is agent-editable |
| provider `base_url` | refused | decides where inference, and hydrated credentials, go |
| `programs` | refused | authorizes deleting binaries from the workspace home |
| `agent_updates` | refused | would let an agent freeze its own updates |
| `packs`, `profiles`/`use_profiles`, `adapters` | inexpressible and refused (`use_profiles` is only refused) | the `packs` security model, applied to what runs and where inference goes |
| loophole install, `env`, `doctor_cmd`, `settings` | refused | agent-editable; `env` and `doctor_cmd` reach a host daemon |
| `host_wrappers`, `host_apply_on_launch`, `host_management`, `promotion_target` | refused | acts on the real home or host `PATH`, machine-wide |
| `perf_logging` | refused | read before workspace config loads |

The first three rows are the whole ro-context surface. [OQ-AS3](../research/agent-safehouse.md#OQ-AS3) already asks whether the first two leave
workspace scope. **This doc does not absorb [OQ-AS3](../research/agent-safehouse.md#OQ-AS3). It pairs with it** ([OQ-WT8](#OQ-WT8)). AS3 decides
where ro reach may sit; this doc decides what a trust record adds. If AS3 rules ro reach user-scope-only, this
doc's local-file-plus-trust route is the natural place for a per-workspace ro mount to go.

## 2. Two actors, and the one thing each can write

[Test 1](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it)
asks whether the actor could already do the guarded act. [The corollary](../reference/gate-placement-principle.md#the-corollary-that-generates-the-real-work)
asks us to name the actor. There are three, and they write different files:

| Actor | Can write | Could it already hand out a writable host path? |
|---|---|---|
| **The human**, on the host | every file | yes; it can write user config. A gate on it is theatre |
| **A repo author** (cloned repo, pulled branch, dependency submodule) | the committed file, and anything it includes | **no** |
| **The in-jail agent** | the committed file (unless `workspace_readonly` is set), **always** the local file, and in-workspace includes | **no** |

Both non-human actors fail Test 1, so a gate on each passes the test. This is where a trust record parts company
with the one [OQ-TP9](trust-paths.md#decision-ledger) deleted. The fetched-pack prompt guarded an act that already required a user-scope write, so
it refused an actor who had passed a stronger gate. A grant in workspace scope requires no such write. The
Safehouse comparison reached the same answer about Safehouse's own trust gate
([§7.2 A](../research/agent-safehouse.md#72-where-they-genuinely-disagree): "For Safehouse's gate the answer is
**no**").

The same research doc's [§9 item 1](../research/agent-safehouse.md#9-negative-space--what-not-to-adopt) says
*not* to port a prompt, and to prefer a scope rule. **That advice holds for the repo author and fails for the
agent.** A scope rule makes the author's grant inexpressible, since the committed file cannot say it. But "the
local file only" is also a scope rule, and the agent can write the local file. For the agent, no scope rule
inside the workspace works. What does work is either (a) moving the grant outside the workspace, meaning user
config, or (b) a record the agent cannot write, meaning host-side trust. So the options narrow to those, plus
combinations.

## 3. The options

Every option is judged on six things: the threat it stops, the friction, in-jail behavior, macos-user, CI and
no-TTY launches, and the failure rule. The failure rule is fixed by [OQ-TP6](trust-paths.md#decision-ledger) and
[context-mounts §2.2](context-mounts.md#22-where-an-rw-mount-may-be-declared-deferred): **a grant from an
untrusted source refuses the launch.** It is never dropped and never downgraded to ro. No option gets a
`YOLO_ALLOW_*` hatch. A hatch is for broken user config, and an untrusted grant is not broken config. Its remedies
are to trust it or delete it.

### 3.1 Option A — scope only: grants only in the local file

Refuse a grant in the committed file and admit it from the local file, optionally checking that the local file is
untracked.

- **Stops:** the repo author. **Does not stop:** the agent (see [§1.3](#13-the-local-file-is-writable-from-inside-the-jail-even-under-workspace_readonly)).
  The approval gate still diffs the agent's edit, which is the same undifferentiated y/N as today.
- **The untracked check cannot be made sound.** Each case below shows why:

  | Case | What a git probe says | Why it is not enough |
  |---|---|---|
  | No repository | "not a repo" | a later `git init && git add .` commits the file, and nothing re-checks |
  | Other VCS (hg, Sapling, Pijul, Fossil; jj without a colocated `.git`) | "not a repo" | tracked, and invisible to the probe |
  | Workspace is a subdirectory of a parent repo (dotfiles, monorepo) | correct, if probed with `-C <workspace>` | fine, but only at probe time |
  | `.gitignore` entry removed | untracked but no longer ignored | the next `git add -A` commits it; probe *ignored*, not *untracked*, or this case passes |
  | The `.json` fallback | often not ignored (this repo's case) | the probe must cover every name `LoadWorkspaceConfig` reads (`workspaceConfigNames`) |
  | Symlinked local file | depends on the link | the link target might be a tracked file elsewhere |
  | `include_if_found` from the local file | the include is a different file | each include needs its own probe, or grants must be refused from includes |
  | No `git` binary on the host | nothing | a minimal CI image or a fresh machine may lack it |

  A check is a point-in-time lint about a property that can change without yolo running. Its honest severity is
  a **warning** ([OQ-WT3](#OQ-WT3)).
- **A precedent cuts against leaning on the file's name.** `--user-layer` exists *instead of* a withdrawn
  auto-merged `config.local.jsonc`, because such a file "activates because a file EXISTS, invisibly at the call
  site" (`userlayer.go`). The workspace local file is that mechanism at workspace scope. Loading it with *more*
  authority deepens the invisibility. The disclosure line ([§4.5](#45-disclosure-unchanged-and-unsuppressible))
  is what answers this, not the name.
- **Friction:** none beyond creating the file. **In-jail:** in-jail `yolo check` can apply the scope rule, since it
  sees both files. **macos-user, CI:** unchanged, and no prompt is added.
- **Variant A′** *(coined here)*: move the local file to `.yolo/config.jsonc`, which `EnsureWorkspaceStateDir`
  already makes un-committable. That settles the tracking question for git without a probe, but not for other VCS.
  It is still jail-writable, so it changes nothing for the agent.

### 3.2 Option B — a host-side trust record

A `yolo trust` verb records that the human approved a workspace's grants. At launch, a grant with no matching
record refuses. Grants may sit in either workspace file.

- **Stops:** the agent. Its edit changes the grant set, so the hash misses and the launch refuses. **Stops the
  author only by prompting**, and on a cloned repo that prompt is the Safehouse gate [§9](../research/agent-safehouse.md#9-negative-space--what-not-to-adopt) advised against: a stranger's
  repo can *ask* for a writable host path, and a busy human answers. The y reflex that
  [OQ-S1](../reference/config-safety.md#oq-s1) warns about lands exactly here.
- **Friction:** one `yolo trust` per change to the grant set.
- **In-jail:** the record lives host-side, so the jail cannot write it and cannot read it. In-jail `yolo check`
  can report *which* grants the launch carried, using the delivered assembled config, but not whether they are
  trusted.
- **macos-user:** it runs on the same path as the approval gate, which is inside that backend's arm.
- **CI and no-TTY:** a setup step runs `yolo trust` on the host. That is a declaration by an actor who already has
  host authority. [Test 1](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it)
  allows a declaration as the right actor test, and it is not a hatch.

### 3.3 Option C — both, one mechanism per actor

A's scope rule gates the repo author, so a grant in the committed file is refused and the repo cannot even ask. B's
record gates the agent, so a grant in the local file needs trust. User config is unchanged: no trust, no prompt,
per Test 1. The git probe survives as a warning. [§4](#4-the-recommendation-c--scope-for-the-author-trust-for-the-agent)
specifies it.

### 3.4 Option D — user scope only, which is the status quo rule applied to rw mounts

Treat an rw mount like `cache_relocations`: user config only. That already covers every actor. Neither the author
nor the agent can write `~/.config`, it needs no new mechanism, and no prompt anywhere. It costs the thing the
maintainer asked for: the declaration no longer sits with the workspace. A per-workspace table in user config (a
path-keyed section, *coined here as* **workspace-keyed user config**) would recover "per workspace" without "in the
workspace". Nothing like it exists today, and it is its own design.

### 3.5 Scored

| | Repo author | In-jail agent | New mechanism | Human friction | CI |
|---|---|---|---|---|---|
| A | stopped | **not stopped** | none, or a lint | none | unchanged |
| B | prompted | stopped | record + verb | a `yolo trust` per change | setup step |
| C | stopped | stopped | record + verb | a `yolo trust` per change, local file only | setup step |
| D | stopped | stopped | none | edit `~/.config` | unchanged |

**A is the only option that fails the brief** ([§1.3](#13-the-local-file-is-writable-from-inside-the-jail-even-under-workspace_readonly)).
D is the cheapest correct answer. C is the correct answer that keeps the declaration in the workspace. B is C
without the part that keeps a stranger from asking.

## 4. The recommendation: C — scope for the author, trust for the agent

Recommend **C**, and **ice it** until an rw mount is actually wanted
([OQ-WT1](#OQ-WT1)). While iced, D is the rule: an rw mount, if built first, is user-scope-only like
`cache_relocations`. That is the house default, and C can be added later without breaking any config D admits.

### 4.1 Where a grant may be written

| File | A grant there |
|---|---|
| user config (and `--user-layer`) | honored, with no trust needed |
| the local file's own top level | honored **only** if a trust record matches |
| the committed file | refused, with a message that names the local file and `yolo trust` |
| any `include_if_found` target, from either workspace file | refused ([OQ-WT2](#OQ-WT2)) |

The grant builder reads the local file **directly**, the way `LoadCacheRelocations` reads the user file, and never
from the merged map. The merge does not record which file an element came from, and a builder that cannot say
where an element came from cannot apply this table ([`context-mounts.md` §2.2](context-mounts.md#22-where-an-rw-mount-may-be-declared-deferred)). `yolo check` applies the same table.

### 4.2 The trust record

- **Key:** the workspace's resolved host path. The record lives under a new directory beside
  `paths.ApprovalsDir()` and is named by the same container-name key (`runtime.FromWorkspace`), so a moved
  workspace loses its trust. That fails safe, as the approval gate does.
- **Content:** the grant set itself, as canonical JSON, plus its SHA-256. The grant set is stored and not only
  hashed, so that `yolo trust --show` can display what was trusted and a mismatch can be shown as a diff. **The
  hash covers the grant set, not the whole file**, so a package edit in the local file goes through the approval
  gate alone and does not re-ask trust ([OQ-WT4](#OQ-WT4)).
- **Resolution:** paths are resolved on the host when `yolo trust` runs, and again when the launch starts.
  If a symlink retargets, the set changes and trust must be granted again.
- **One writer:** `yolo trust`, on the host. It writes atomically (temp file plus rename). Launches only read the
  record, so two concurrent launches cannot race on it.
- **Never mounted into any jail.** The directory is under `~/.local/share/yolo-jail`, which
  [context-mounts §2.3](context-mounts.md#23-refusal-set)'s credential-boundary predicate already refuses as an rw
  source. A trusted grant therefore cannot mount the trust store writable.

### 4.3 The honest cost: the human pays the agent's toll

The local file has two writers, the human and the agent, and yolo cannot tell their writes apart. So the human who
typed an rw mount into their own local file must also run `yolo trust`. For that actor, Test 1 says the step is
ceremony. The design accepts that because the alternative is the gap. The cost is bounded three ways: it applies
once per grant-set change and not per launch; it applies to the local file only; and D (write the grant in user
config) is always available to a human who would rather not pay it.

### 4.4 At launch

- A trusted grant set, or no grants: nothing new happens.
- An untrusted grant set: **refuse** with the grant set, a diff against the recorded set when one exists, and the
  exact `yolo trust` command. This holds with a TTY and without one. There is no launch prompt and no launch
  flag. `--accept-config-changes` **never** grants trust ([OQ-WT5](#OQ-WT5)). This is direnv's shape (refuse,
  name the verb), not mise's normal-mode shape (prompt) ([Appendix B](#appendix-b-mise-and-direnv)).
- The order is scope first, then trust, then the approval gate. A refusal tells the user what to do before the
  launch asks them anything else.
- The check runs identically on every backend, including macos-user, even where the backend cannot deliver the
  mount. Delivery is [context-mounts](context-mounts.md#3-delivering-context-dirs-on-macos-user)' business, and a
  config's trust verdict must not depend on which backend reads it.

### 4.5 Disclosure, unchanged and unsuppressible

Trust decides whether a grant is honored, never whether it is announced.
[context-mounts §2.4](context-mounts.md#24-disclosure)'s per-mount line prints every launch, trusted or not, as an
[OQ-RO3](../reference/report-tiers.md#why-its-this-way) disclosure. It is teed to `launch.log` and not hidden by
`YOLO_NO_BANNER`. That line is also the answer to the "a file activates invisibly" objection from
[§3.1](#31-option-a--scope-only-grants-only-in-the-local-file).

### 4.6 The verb

`yolo trust` shows the grant set and the full workspace-config diff, and asks once. On yes, it writes the trust
record **and** the approval baseline (the second is what `yolo check --accept-config-changes` writes), so one
edit is not consented to twice ([OQ-WT6](#OQ-WT6)). `yolo trust --revoke` deletes the record, so the next launch refuses.
`yolo trust --show` prints the record and whether the current grant set matches it. Non-interactively, `yolo trust
--yes` exists for CI setup steps, because running it already takes host authority.

In-jail, `yolo trust` for the jail's own workspace (`jailOwnWorkspace`) **refuses, naming the host**, because no
host launch reads the jail's store and a silent success would lie. For any other workspace it works against the
jail's own `~/.local/share/yolo-jail`, which governs only nested launches. That follows
[P1 (trust flows downward)](trust-paths.md#p1-trust-flows-downward-and-a-parent-controlling-its-child-is-not-a-finding)
and Test 2 ([OQ-WT9](#OQ-WT9)).

### 4.7 State that already exists

There is no rw grant form today, so no record exists to migrate, and no config can hold a grant until the feature
ships. Existing local files, and this repo's committed ro `mounts` entry, are untouched by this design. Moving ro
mounts is [OQ-AS3](../research/agent-safehouse.md#OQ-AS3)'s to decide.

### 4.8 What done looks like

- An rw mount in the committed file fails `yolo check` and refuses the launch. The message names the local file.
- The same mount in the local file refuses the launch, naming `yolo trust`, until `yolo trust` runs on the host.
  After that the launch proceeds and prints the rw disclosure line.
- An in-jail edit to that grant, such as a changed path, refuses the next host launch with a diff against the
  trusted set. An in-jail edit adding only a package does not trigger trust.
- `--accept-config-changes` on a no-TTY launch with an untrusted grant still refuses.
- `yolo trust --revoke` makes the next launch refuse.
- In-jail `yolo trust` in `/workspace` refuses and names the host.
- The same config gets the same verdict on podman, Apple Container and macos-user.

## 5. What this does not propose

- **No trust for the committed file, ever,** under C. The maintainer's "never in a git-committed file" becomes a
  refusal rather than a prompt.
- **No change to the approval gate**, to ro `mounts`, or to `env_sources`. Those belong to [OQ-AS3](../research/agent-safehouse.md#OQ-AS3).
- **No "trust this workspace" blanket.** Trust attaches to a grant set, so a trusted workspace does not trust its
  next grant.
- **No env var** to trust or skip trust, for the reason [OQ-D2](../reference/config-safety.md#oq-d2) gave for `--accept-config-changes`.
- **No trust store in the workspace**, including under `.yolo/`, which the jail can write.

## 6. What a trusted grant could unlock

Only the rw context mount is proposed for v1. The rest are candidates, sorted by whether the key's own refusal text
says "agent-editable" (a record the agent cannot write would answer that reason) or names a structural reason
(which trust cannot answer). Verified against each validator's message ([Appendix A](#appendix-a-evidence)).

| Candidate | The refusal's reason | Could trust answer it? |
|---|---|---|
| rw context mounts ([context-mounts](context-mounts.md)) | not built; the `cache_relocations` reason applies | **yes, the v1 case** |
| `cache_relocations` | agent-editable | yes |
| source-bearing `host_files` | agent-editable, travels with the repo | yes |
| provider `base_url` | agent-editable; steers inference and credentials | yes, but it is an exfiltration channel ([OQ-WT7](#OQ-WT7)) |
| ro `mounts`, `env_sources` | none today | only if [OQ-AS3](../research/agent-safehouse.md#OQ-AS3) moves them |
| `programs`, `agent_updates` | agent-editable | technically; the payoff is unclear |
| `packs` ([OQ-MP7](mcp-presets-removal.md#OQ-MP7)'s safe subset) | install-shaped; [OQ-MP7](mcp-presets-removal.md#OQ-MP7) ruled the axis to be host reach, not install | a different question: MP7 wants a subset a **committed** file may declare |
| `host_wrappers`, `host_apply_on_launch`, `host_management`, `promotion_target` | act on the real home or host `PATH`, machine-wide | **no**; a machine-wide effect is not per-workspace |
| `perf_logging` | read before workspace config loads | **no**; the ordering is structural |
| loophole install, `env`, `doctor_cmd`, `settings` | agent-editable; `env` and `doctor_cmd` reach a host daemon | not proposed: host execution is the axis [OQ-MP7](mcp-presets-removal.md#OQ-MP7) reopened, and it needs its own ruling |

## 7. Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| `yolo trust` becomes the new y reflex | medium | it is a separate command, run on the host, per grant-set change; no launch prompt |
| The human finds the local-file toll ([§4.3](#43-the-honest-cost-the-human-pays-the-agents-toll)) annoying and pastes grants into the committed file | low | refused, with the message naming the local file |
| A builder reads grants off the merged map | medium | the builder reads the local file directly, and a test fails if a committed-file grant reaches the argv |
| The trust record and the approval baseline drift apart | low | `yolo trust` writes both; the launch checks each independently |

## Open Questions

1. 💬 <a id="OQ-WT1"></a>**[OQ-WT1](#OQ-WT1): Which rule — C, or D (user scope only)?** And is it iced?

   _Leaning:_ **C, iced**. Until an rw mount is actually wanted, D is the rule, since that is the
   `cache_relocations` precedent. Thaw when someone needs an rw mount that is workspace-specific and cannot move
   into the workspace. C only adds to D, so shipping D first forecloses nothing.

   <!-- vantage: oq id=OQ-WT1 leaning="C (scope for the repo author, host-side trust for the in-jail agent), iced; D (user scope only, the cache_relocations precedent) is the rule until an rw mount is actually wanted, and C only adds to it." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-WT2"></a>**[OQ-WT2](#OQ-WT2): Are grants refused from `include_if_found` targets?** Includes may
   use `../`, and so may name files outside the workspace, and each include is a separate file for tracking
   purposes.

   _Leaning:_ **Refuse them**, from either workspace file. The builder reads the local file's own top level only.
   A grant always sits in one file that `yolo trust` names.

   <!-- vantage: oq id=OQ-WT2 leaning="Refuse grants from any include_if_found target; the builder reads the local file's own top level only, so a grant always sits in one named file." -->

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-WT3"></a>**[OQ-WT3](#OQ-WT3): Does yolo probe whether the local file is tracked, and how
   severe is a hit?**

   _Leaning:_ **A warning** in `yolo check` and on the launch, when `git -C <workspace>` reports any
   workspace-config name as tracked *or not ignored*. The probe is silent when there is no repo or no `git`. It
   is never a refusal, because [§3.1](#31-option-a--scope-only-grants-only-in-the-local-file)'s table shows the
   property cannot be kept. Separately, `yolo init` should add the local file's names to `.gitignore`, alongside
   `.yolo/`.

   <!-- vantage: oq id=OQ-WT3 leaning="A warning, never a refusal: git -C <workspace> reports a workspace-config name tracked or not ignored; silent with no repo or no git. And yolo init adds the local names to .gitignore." -->

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-WT4"></a>**[OQ-WT4](#OQ-WT4): Does the trust hash cover the grant set, or the whole local
   file?**

   _Leaning:_ **The grant set**, resolved on the host. Hashing the whole file would re-ask trust on every package
   edit, doubling the approval gate's prompts. That is the fatigue [OQ-S1](../reference/config-safety.md#oq-s1)
   ruled against.

   <!-- vantage: oq id=OQ-WT4 leaning="The grant set, resolved on the host, keyed on the workspace path; a whole-file hash re-asks on every package edit and doubles the approval gate, the fatigue OQ-S1 ruled against." -->

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 <a id="OQ-WT5"></a>**[OQ-WT5](#OQ-WT5): At launch, an untrusted grant is refused with the verb named (the
   direnv shape), or prompted for (the mise shape)?** And may `--accept-config-changes` grant trust?

   _Leaning:_ **Refuse and name `yolo trust`**, with a TTY or without. There is no launch flag, and
   `--accept-config-changes` never grants trust. A launch prompt is the Safehouse prompt that
   [§9](../research/agent-safehouse.md#9-negative-space--what-not-to-adopt) advised against. A blanket CI flag
   would re-merge the two consents this design separates.

   <!-- vantage: oq id=OQ-WT5 leaning="Refuse and name yolo trust, TTY or not; no launch prompt, no launch flag, and --accept-config-changes never grants trust." -->

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 <a id="OQ-WT6"></a>**[OQ-WT6](#OQ-WT6): Does `yolo trust` also record the approval baseline?**

   _Leaning:_ **Yes.** It shows the full workspace-config diff beside the grant set, and a yes writes both. The
   same human, on the same host, should not consent to one edit twice. The approval gate still re-asks for any
   later edit.

   <!-- vantage: oq id=OQ-WT6 leaning="Yes: yolo trust shows the full workspace diff beside the grant set and writes both records, as yolo check --accept-config-changes does." -->

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 <a id="OQ-WT7"></a>**[OQ-WT7](#OQ-WT7): Beyond rw mounts, which of [§6](#6-what-a-trusted-grant-could-unlock)'s
   "yes" rows become trust-gated grants?**

   _Leaning:_ **Only rw mounts in v1.** Next would be `cache_relocations`, which is the same class of rw host
   mount. Provider `base_url` stays user-scope-only even though trust could admit it, because a steered endpoint
   receives every credential the provider hydrates, and a mistaken trust there costs more than a mistaken mount.

   <!-- vantage: oq id=OQ-WT7 leaning="Only rw context mounts in v1; cache_relocations next (the same class); provider base_url stays user-scope-only because a steered endpoint receives hydrated credentials." -->

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 <a id="OQ-WT8"></a>**[OQ-WT8](#OQ-WT8): Does this doc absorb [OQ-AS3](../research/agent-safehouse.md#OQ-AS3), or pair with it?**

   _Leaning:_ **Pair.** AS3 stays the question of whether ro `mounts` and `env_sources` leave workspace scope. This
   doc adds a third answer to it: "local file plus trust". AS3 can then rule without this doc being built.

   <!-- vantage: oq id=OQ-WT8 leaning="Pair: OQ-AS3 keeps the ro mounts/env_sources scope question and gains a third answer, local file plus trust, without depending on this doc being built." -->

   **Answer:**
   > _(empty — fill in when decided)_

9. 💬 <a id="OQ-WT9"></a>**[OQ-WT9](#OQ-WT9): In-jail `yolo trust` — refused outright, or allowed for nested
   workspaces?**

   _Leaning:_ **Allowed for nested workspaces, and refused for the jail's own**, naming the host. The jail's store
   governs only its own children (P1 and Test 2). A refusal for the jail's own workspace stops an agent believing it
   trusted the outer launch.

   <!-- vantage: oq id=OQ-WT9 leaning="Allowed for nested workspaces against the jail's own store (P1, Test 2); refused for the jail's own workspace, naming the host, so an agent cannot believe it trusted the outer launch." -->

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling | Date | Folded into |
|---|---|---|---|
| — | _none yet_ | | |

## Appendix A: Evidence

Symbols checked on 2026-09-25 at `71acddac`. None of the cited files had uncommitted edits.

- **Loading.** `config.LoadWorkspaceConfig` (committed, then local, shared `seen`, `MergeConfig` with local
  winning) and `config.LoadConfig` (user under workspace; in-jail for the own workspace it reads
  `.yolo/config-assembled.json`), both in `internal/config/load.go`. `LoadJSONCWithIncludes` refuses a `/` or `~`
  include and resolves relative ones, `..` included. `config.WorkspaceLocalConfigName` is in `config.go`.
  `workspaceConfigNames` in `internal/cli/configtarget.go` lists the `.json` fallbacks.
- **Local file as workspace scope.** `workspaceLoopholeEntries` (`validate_loopholes.go`),
  `TestValidateCacheRelocationsWorkspaceLocalScopeAlsoRejected`, `TestWorkspaceLocalConfigProviderAddressIsRefusedToo`,
  the local-file comment in `internal/cli/check/check.go`, and the non-interactive refusal naming the local path
  in `internal/config/snapshot.go`.
- **No tracking check.** `rg 'check-ignore|ls-files' internal --glob '!*_test.go'` finds only a comment in
  `internal/macosuser/seatbelt.go`. `yolo init` in `internal/cli/init.go` (`mountsBlock`, and the `.gitignore`
  append). `applySealed` in `internal/cli/apply.go`. `paths.EnsureWorkspaceStateDir` and its self-ignoring
  `.gitignore` in `internal/paths/paths.go`. `git check-ignore -v` ignores `yolo-jail.local.jsonc`
  (`.gitignore`), and exits 1 for `yolo-jail.local.json`.
- **Jail writability.** `workspaceReadonlyMountArgs` (`internal/cli/run/mounts.go`) binds only
  `yolo-jail.jsonc` `:ro`, beside the listed entries.
- **The approval gate.** `CheckConfigChanges`, `ApprovalSnapshotPath` and `AcceptConfigChangesFlag` (`snapshot.go`),
  `paths.ApprovalsDir`, the in-jail refusal of `--accept-config-changes` in `internal/cli/check/check.go`. Its
  rulings are in [`config-safety.md`](../reference/config-safety.md#why-its-this-way).
- **Scope refusals** (the message text quoted in [§1.5](#15-the-scope-table-for-the-keys-that-reach-the-host) and
  [§6](#6-what-a-trusted-grant-could-unlock)). In `internal/config/validate.go`: `validateCacheRelocations`,
  `validateHostWrappers`, `validateHostApplyOnLaunch`, `validateHostManagement`, `validateAgentUpdates`,
  `validatePerfLogging`, `validatePromotionTarget`, and `validateProviderAddressScope` with
  `providerAddressScopeMessage`. Elsewhere: `programs.go`, the source-bearing entry refusal in `hostfiles.go`,
  `packs.go`, `profiles.go`, `adapters.go`, `validate_loopholes.go`, `validate_loopholesettings.go`.
  `LoadCacheRelocations` (`relocations.go`) reads the user file directly and returns nothing in-jail.
- **No scope rule.** `validateMounts` and `validateEnvSources` check shape only, and so do `validateDevices`,
  `validateGPU`, `validateKVM` and `validateNetwork`.
- **Nested inheritance.** `internal/config/inherit.go`: `mounts` and `cache_relocations` are not inherited;
  `env_sources` is.
- **Precedents.** The `userlayer.go` package comment (the withdrawn `config.local.jsonc`, and Test 1 applied to
  `--user-layer`). [OQ-TP9](trust-paths.md#decision-ledger) and
  [its preserved argument](trust-paths.md#why-the-gate-was-theatre--the-argument-preserved).
  `TestTheLockfileHoldsNoApprovalRecord` (`internal/packsrc/lock_test.go`). `TestTheLaunchHasNoQuietFlag`
  (`internal/cli/runcmd_test.go`). [OQ-MP7](mcp-presets-removal.md#OQ-MP7).

## Appendix B: mise and direnv

- **mise.** `mise trust [CONFIG_FILE]` marks a file trusted, and there are `--untrust`, `--ignore` and `--show`
  forms. In normal mode, "safe" files load without trust. Those are files with only `min_version`, plain `[tools]`
  versions, and templateless `[tasks]`. Execution commands (`mise run`, `mise install`, `mise exec`) auto-trust
  their active config. Otherwise mise "may prompt, skip the config in some discovery paths, or fail with an
  untrusted-config error when it cannot prompt". Trust is shared across git worktrees. **Paranoid mode** requires
  explicit trust for every non-global file, "binds direct file approvals to configuration content" (an edit needs
  renewed trust), disables auto-trust and CI exemption, and cannot be toggled by a project. Global config is
  exempt as operator-owned. Sources: <https://mise.jdx.dev/cli/trust.html>, <https://mise.jdx.dev/paranoid.html>,
  <https://mise.jdx.dev/security.html> (fetched 2026-09-25).
  *Lesson taken:* the global-config exemption is Test 1. The content binding is what the approval gate already
  does. The auto-trust on execution is what this design refuses.
- **direnv.** An `.envrc` is blocked until `direnv allow`, and blocked again after any edit ("Run `direnv allow` to
  approve its content"). The allow record is a file under `$XDG_DATA_HOME/direnv/allow` named by
  SHA-256 over the absolute path, a newline, and the file's contents (`fileHash` in `internal/cmd/rc.go`). `direnv
  deny` writes a path-keyed record. Sources: <https://direnv.net/man/direnv.1.html>;
  <https://github.com/direnv/direnv/blob/master/internal/cmd/rc.go> (fetched 2026-09-25).
  *Lesson taken:* refuse and name the verb, not prompt. Key on path plus content, and keep the record outside the
  directory it governs.
