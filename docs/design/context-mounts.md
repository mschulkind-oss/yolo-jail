---
title: "Context directories: a writable form, and a macos-user delivery where Seatbelt is the authority and the link is only a name"
date: 2026-09-25
status: draft
tags: [mounts, ctx, macos-user, seatbelt, trust, parity, disclosure]
summary: "Two proposals the maintainer raised together on 2026-09-25. First, a read-write form of the config `mounts` key: refused at the credential boundary, and disclosed on every launch as an injection channel into the host. Second, context directories on macos-user with no copy: a root-owned tree of links, named by a new env var, with Seatbelt rules on the resolved paths doing all the enforcing. The second re-opens DP-D15's fatal refusal, and narrows it rather than overturning it. Where an rw mount may be declared is deferred to workspace-config-trust.md. Nine questions are open. Nothing is built."
---

# Context directories: a writable form, and a macos-user delivery where Seatbelt is the authority and the link is only a name

**Status:** DESIGN, 2026-09-25. Nothing built. Evidence was checked against the working tree on this date. It cites symbols, never line numbers.

> **In short.** A context mount has two jobs: making host bytes *appear* at a named path, and
> *deciding who may write them*. On the container backends, one bind does both jobs. On macos-user
> they come apart. A root-owned symlink can do the naming, but only Seatbelt can do the deciding,
> and it judges the resolved target, never the link. So the macos-user design is to name the
> bytes with a link, enforce with the profile, and refuse any source the sandbox uid cannot reach
> at the POSIX layer. The read-write form is the same feature plus one new disclosure: writable
> host bytes that a host program later reads are an injection channel.

**Why it matters.** Today every context mount is read-only on every backend that delivers one, and
macos-user delivers none. The workaround for "I need the agent to write there" is to move the
directory into the workspace. The workaround on macos-user is a different backend.
[DP-D15](declaration-parity.md#7-ruled-divergent-and-the-ones-i-would-re-open) ruled the macos-user
case a fatal refusal, and even that refusal is unbuilt: the code only warns.

**The shape.** One `mounts` list with a per-entry mode. One agent-facing root, `YOLO_CONTEXT_DIR`,
exported on every backend. On macos-user, a root-owned link tree plus generated Seatbelt rules,
gated by a DAC preflight run as the sandbox uid.

**Cost.** macos-user can deliver only sources on neutral ground or outside `/Users`. Most
context sources live under the user's own home, so v1 keeps refusing the common case there
([OQ-CX7](#OQ-CX7)). AGENTS.md's "ONE host directory is bind-mounted WRITABLE" sentence has to be
restated, because it was never literally true.

**Start at [§3.1](#31-does-dp-d15-still-hold).** It is the re-opened ruling, and everything
macos-specific hangs off its answer.

**Needs your ruling:** [OQ-CX1](#OQ-CX1), [OQ-CX2](#OQ-CX2), [OQ-CX3](#OQ-CX3), [OQ-CX4](#OQ-CX4), [OQ-CX5](#OQ-CX5), [OQ-CX6](#OQ-CX6), [OQ-CX7](#OQ-CX7), [OQ-CX8](#OQ-CX8), [OQ-CX9](#OQ-CX9).

**Reads with:** [`declaration-parity.md`](declaration-parity.md) (DP-D15, DP-B1, DP-B2 and [§6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured),
the rulings this doc re-opens and the probes it relies on),
[`../reference/macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md) (the Seatbelt
"read-only half of a bind" argument this generalizes), [`trust-paths.md`](trust-paths.md) ([OQ-TP9](trust-paths.md#decision-ledger),
why pack grants are disclosure-only), [`workspace-config-trust.md`](workspace-config-trust.md)
(owns where an rw mount may be declared; [§2.2](#22-where-an-rw-mount-may-be-declared-deferred)).
**No `-plan.md` sketch exists yet.** It was deferred because this doc was written under a
one-new-file constraint.

---

## Defined terms

- **Context mount** — any declaration that makes a host path visible to the jail outside the
  workspace. There are two: a config `mounts` entry and a pack `mount` contribution
  (`packdecl.KindMount`). The existing term for the destination tree is `/ctx`.
- **Context source** (coined here) — the host path a context mount names, after `~` expansion and
  symlink resolution on the host at launch.
- **rw context mount** (coined here) — a context mount whose destination the jail may write. No
  such thing exists today.
- **Context dir** (coined here) — the directory the agent reaches context mounts through: `/ctx`
  on container backends, and on macos-user the root-owned link tree of [§3.2](#32-where-the-bytes-are-named).
  It is named by `YOLO_CONTEXT_DIR` (coined here; nothing in the tree spells it today).
- **Composition-input root** — what `YOLO_CTX_ROOT` already names: where the *entrypoint* reads
  host bytes it composes from (`entrypoint.ctxRootDir`, `remapCtx`). Absent on podman,
  `~/.yolo-ctx` on Apple Container, and `macosuser.StagedCtxRoot` on macos-user. It is not
  agent-facing, and it is distinct from the context dir on Apple Container.
- **Neutral ground** — the macos-user term (`macosuser.PlanInvariants`, `HomeContaining`) for
  locations outside every real `/Users/<name>` home. In practice that means the shared root
  `/Users/Shared/yolo`, which carries inheriting `_yolojail` group ACEs
  (`macosuser.SharedRootProvisionCommands`).
- **DAC preflight** (coined here) — a check that runs as the sandbox uid before the sandbox starts,
  asking the kernel whether that uid can read (and, for rw, write) a context source. It is
  POSIX permissions plus ACLs, with no Seatbelt involved.
- **Writable set** — the paths `macosuser.SeatbeltProfile` re-allows for `file-write*` after its
  root deny: the workspace, the sandbox home, `/tmp`, `/private/tmp`, `/var/folders`,
  `/private/var/folders`, and `/dev`.

## 1. What exists today, per backend

| | config `mounts` | pack `mount` |
|---|---|---|
| **podman, Linux** | `-v src:dest:ro` for every entry. A missing source is skipped with a warning. Workspace-scopable behind the [config-change diff prompt](../reference/config-safety.md). Not inherited by nested jails. | `-v ~/<host>:/ctx/<into>:ro` for a dir. A single file goes through `ROFileMountArg`. A missing source is skipped. Disclosed on the launch banner and in `yolo pack footprint` as a host READ. There is no approval gate ([OQ-TP9](trust-paths.md#decision-ledger)). |
| **podman, macOS machine** | Same argv. The source must be inside the VM's shared set (`$HOME` and `/private` by default), and nothing probes that. | Same. |
| **Apple Container** | Delivered `:ro` from `container` 1.1.0 (`acROBindsFloor`). Below that version, or when the version is unreadable, every entry is **skipped with a reason** (`roBindsUnsupported`). A writable "read-only" mount is never handed out. Each share uses a per-VM directory-sharing device. | Directories follow the same rule. **A single-file `mount` is skipped at every version.** |
| **macos-user** | **Not delivered.** One warning per entry (`run.noteMacosUserCtxMountGaps`), giving COPY as the reason. `appliedCtxMounts` returns nil, so the briefing claims nothing. DP-D15 ruled this a **fatal refusal**, and that refusal is **unbuilt**. | Same warning. ⚠ `notePackHostAccess` still discloses the read as if it happened, which is a live contradiction ([DP-B2](declaration-parity.md#51-macos-user-read-by-nobody-warned-by-nobody)). |

No backend has a writable context mount. The only writable host binds today are the workspace, yolo's
own state and cache dirs, `cache_relocations` targets (user scope, podman only), and the one
[host-CAS alias](disk-levers-and-backfill.md#OQ-BF10).

Two premises in the brief are stale and should not be built on:

- "Approved at pack install against the word read-only" describes a gate
  [OQ-TP9](trust-paths.md#decision-ledger) deleted on 2026-09-04. DP-B2's row and
  `docs/guides/macos.md` still say it.
- `docs/guides/macos.md` describes macos-user `mounts` as "silently ignored, with no warning". That
  has been false since the warning landed.

## 2. Read-write context mounts

### 2.1 Config shape

The string form stays **read-only only**, with unchanged semantics. The writable form is an
**object element** in the same list:

```jsonc
"mounts": [
  "~/code/lib",                                        // unchanged: ro, /ctx/<basename>
  { "host": "~/scratch/datasets", "at": "/ctx/data", "mode": "rw" },
  { "host": "~/notes", "mode": "ro" }                 // object form, ro, at /ctx/notes
]
```

- `mode` is required in the object form and must be `"ro"` or `"rw"`. There is no default, because a
  mode nobody wrote is how a writable mount gets handed out by accident.
- `at` is optional. It defaults to `/ctx/<basename after resolution>`, the rule string entries
  already follow. **For `rw`, `at` must lie under `/ctx`.** A writable bind elsewhere could shadow
  a home path or a composed surface, and `writable_home_dirs` and `cache_relocations` are the
  shaped tools for those cases.
- A `:rw` string suffix is **rejected with a message naming the object form**. It must not be parsed
  as a mode. Paths may contain colons, and the `:ro` suffix already mis-parses into a
  silently-skipped mount (`splitMountSpec`).
- Duplicate `at` values across all entries, packs' `/ctx/<into>` included, are a `yolo check`
  error. Today a collision is just whichever bind wins.
- A missing rw source is skipped with a warning, the same as ro. yolo **never creates** a
  context source.

Whether this should be a separate key instead is [OQ-CX1](#OQ-CX1).

### 2.2 Where an rw mount may be declared: deferred

**This doc does not decide which config files may declare an rw element.** That question, including
whether a workspace's committed `yolo-jail.jsonc` may, belongs to
[`workspace-config-trust.md`](workspace-config-trust.md). It recommends that an rw element be
refused in `yolo-jail.jsonc` and in any include, admitted from `yolo-jail.local.jsonc`'s own top
level only when a host-side trust record matches, and free in user config; until that is ruled,
user scope only ([OQ-WT1](workspace-config-trust.md#OQ-WT1)).

What this doc needs from that ruling is one predicate: **is this rw element from a trusted
source?** Everything below consumes that predicate and takes no position on how it is computed:

- The mount builder must take rw elements **only** from sources the predicate admits, never from
  a merged view that cannot say where an element came from. A validation-only read of the merged
  config is fine.
- An element the predicate rejects is a `yolo check` error and a launch refusal. It is never
  silently dropped, and never downgraded to ro, because a downgrade is a mount the user did not
  write.
- String and `"mode": "ro"` elements keep today's scoping and the
  [config-change diff prompt](../reference/config-safety.md) unless the sibling doc rules
  otherwise.

### 2.3 Refusal set

A `rw` context source is refused, fatally and at launch (on resolved paths, on the host), when any
of these hold:

1. **It trips the credential-boundary predicate.** That is `paths.WorkspaceScopeBreach`'s rule,
   applied to the source instead of the workspace: the source is or contains `$HOME`,
   `~/.config/yolo-jail` or `~/.local/share/yolo-jail`, or it sits inside either yolo dir.
   **Share the predicate; do not copy it.** A second spelling is how the capture-store exemption
   would fall out of one of them.
2. **It overlaps the workspace in either direction.** Inside the workspace, the mount is redundant.
   Containing the workspace, it is a second writable path to the workspace that bypasses
   `workspace_readonly`, including the overlay that locks `yolo-jail.jsonc`.
3. **On macos-user only**, it is or contains `/var/yolo-jail` or the sandbox home. Both are yolo's,
   and a Seatbelt allow there would reopen what the root-owned staging exists to close.

`~/.ssh`, `~/.aws`, `~/.gnupg` and the like are **not** in the predicate, and
`WorkspaceScopeBreach` does not refuse them either ("a workspace UNDER the home is the ordinary
case"). Whether rw adds a named list is [OQ-CX2](#OQ-CX2). Whether the ro form gets the predicate
at all is [OQ-CX3](#OQ-CX3).

`yolo check` on the host runs the same predicate over the same resolution. In-jail it skips the
filesystem half, as `mounts` validation already does.

### 2.4 Disclosure

Each rw mount gets **one launch line, every launch**. It is a disclosure in the
[OQ-RO3](../reference/report-tiers.md#why-its-this-way) sense: never suppressible, teed to
`<workspace>/.yolo/launch.log`, and shown under `YOLO_NO_BANNER`. It names three things:

- the host path and the jail path;
- **the injection sentence**: anything on this machine that later reads the source reads what the
  jail wrote there, including a symlink the jail planted;
- on rootful podman only, the ownership consequence from [§2.5](#25-ownership).

The reasoning is the [host-CAS](disk-levers-and-backfill.md#OQ-BF10) gate's, turned into a
disclosure instead of a gate. A path-keyed writable dir lets the jail choose both the key and the
bytes. That is exactly why `hostcas` admits only content-addressed stores. A user-declared rw mount
is path-keyed by definition, so it cannot pass that gate. It is admissible only because a trusted source named it
([§2.2](#22-where-an-rw-mount-may-be-declared-deferred)), and the user is told what it opens every launch.

The ro form gets no new line. Its disclosure stays the existing banner for packs and the briefing
for config entries.

### 2.5 Ownership

| Backend | A write by in-jail root | A write by in-jail uid ≠ 0 |
|---|---|---|
| rootless podman (`--uidmap 0:0:1 --uidmap 1:1:65536`) | the host user | a **subuid**. The host user cannot delete it without `podman unshare`. |
| rootful podman | **host root** | that uid, on the host |
| nested (`--userns host`) | the outer jail's mapping | the outer jail's mapping |
| podman macOS machine, Apple Container | unmeasured (virtiofs) | unmeasured |
| macos-user | n/a | `_yolojail`. The host user can modify it only where an inheriting group ACE applies, meaning under the shared root. |

The agent runs as root on container backends (Claude YOLO), so the common case lands as the host
user. yolo **does not chown**. Rewriting ownership in a user's directory is a mutation nobody
asked for. Whether rootful podman refuses rw instead of disclosing is [OQ-CX9](#OQ-CX9).

### 2.6 Concurrency

There is **no locking, by design**. Two jails, or a jail and the host, sharing an rw source get
last-writer-wins, the same as two host processes. Tools that need exclusion already bring it
(git's `index.lock`, for example). Doc it in `config-ref`, and add no lock.

### 2.7 Packs

**Not in v1** ([OQ-CX4](#OQ-CX4)). A pack `mount` stays read-only. A pack asking for rw would need
a new footprint sentence ("WRITES to a path in YOUR HOME"). With the approval gate gone
([OQ-TP9](trust-paths.md#decision-ledger)), a selected pack would get a writable path into the user's home on disclosure alone, and
no shipped pack needs it.

### 2.8 The AGENTS.md invariant, restated

As written, "ONE host directory is bind-mounted WRITABLE into the jail, and it is the only one" is
false today: the workspace, yolo's cache dir and `cache_relocations` targets are all writable
binds. What the invariant actually protects is narrower, and it should say so:

> **Exactly one host-TOOL-owned directory is bind-mounted writable, and the gate is content
> addressing** (pants' `lmdb_store`, via `hostcas`). Every other writable host bind is one the
> user named in a trusted config source (`cache_relocations`, rw `mounts`) or the workspace itself,
> and each is disclosed.

### 2.9 Forbidden behavior

- Never emit `:z` or `:Z` on a context mount. The run path uses `label=disable`, and a relabel
  would rewrite SELinux labels on the host tree.
- Never decide an rw mount from a config view that has lost provenance ([§2.2](#22-where-an-rw-mount-may-be-declared-deferred)).
- Never create, chown or chmod a context source on a container backend.
- Never hand out a writable mount as the fallback for a read-only one. This extends
  `roBindsUnsupported`'s rule to the new mode.
- rw is **not** gated by `acROBindsFloor`, because the floor exists for `:ro`. An rw mount works on
  every Apple Container version, and the gate must not refuse it for a property it does not need.

## 3. Delivering context dirs on macos-user

### 3.1 Does DP-D15 still hold?

DP-D15 (ruled 2026-09-12) refuses the launch because "we can't do /ctx by copying, some of these
directories are huge". The premise that made copying the only option was [§6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured)'s finding that the
symlink "fails twice, and neither failure is Seatbelt's". The MAC half was fixable. The DAC half
was not: a foreign uid, and a home that need not be world-traversable.

**That finding is about sources the sandbox uid cannot reach, not about every source.** A source
the DAC preflight passes (on neutral ground, or a world-readable system path) needs no copy and no
new ACL. A link names it, and a generated allow opens it. So the proposal narrows DP-D15:

- **Deliver** by link plus Seatbelt when the source passes the DAC preflight and the siting rules
  ([§3.5](#35-the-dac-half)).
- **Refuse fatally, as DP-D15 ruled**, otherwise, keyed on the declaration being present. That
  refusal also gets built; it is unbuilt today.

This is [OQ-CX5](#OQ-CX5). If DP-D15 is kept unchanged, [§3](#3-delivering-context-dirs-on-macos-user) reduces to building the refusal.

### 3.2 Where the bytes are named

- **Not `/ctx`.** A new top-level directory needs `/etc/synthetic.conf` and a reboot ([§6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured)).
- **Not the sandbox home.** It is one link set per machine (concurrent launches contend, last one
  wins; see [home tiers](../reference/macos-user-home-tiers.md)), and it is agent-writable, so the
  agent could re-point a name.
- **Not the workspace sidecar.** It is agent-writable for the same reason.
- **Yes: `macosuser.StagedCtxRoot`** (`/var/yolo-jail/ctx/<cname>`). It is root-owned, replaced
  wholesale each launch by `StageCtxCommands`, and already the composition-input root. Links go in at
  exactly the relative paths the container `/ctx` would use. So the tree **is** `/ctx` on this
  backend, and a pack surface that names `/ctx/<into>` resolves through `remapCtx` unchanged. It
  shares one namespace with the composed copies (`host-user/…`), just as `/ctx` does on podman.
  The collision rule is the `yolo check` duplicate-destination error from [§2.1](#21-config-shape).

`YOLO_CONTEXT_DIR` is exported **on every launch, on every backend**. It is `/ctx` on podman and
on Apple Container (not `~/.yolo-ctx`, which is the composition-input root), and `StagedCtxRoot`
on macos-user, where the directory always exists, possibly empty. Agents and pack content spell
context paths as `$YOLO_CONTEXT_DIR/<rel>`. The name and the parity are [OQ-CX6](#OQ-CX6).

A config entry whose `at` is **outside `/ctx`** has no place in the tree, so on macos-user it is
**refused**, naming the entry. It is never remapped to a basename the user did not choose.

### 3.3 The link is only a name

Every link is created by the root-owned staging step with an **absolute target equal to the
resolved source**. The agent cannot re-point it, because the link and its parent are root-owned.
That property keeps the *name* honest. **It is not security.** The agent can create its own
symlink anywhere in the writable set, pointing anywhere, and nothing is gained, because Seatbelt
evaluates the **target** of every access, absolute or relative (measured: [§6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured)'s Probe 1). So:

- A relative escape (`$YOLO_CONTEXT_DIR/src/../../..`, or a `../`-target link inside the source)
  resolves, and it is judged where it lands.
- A link inside a **rw** source that points outside it grants the jail nothing. It is still a
  hazard for a *host* program that follows it later, and [§2.4](#24-disclosure)'s injection
  sentence says so.
- **Every access decision is the profile's.** No design choice may rely on the link for
  confinement. A PlanInvariants check pins that ([§4](#4-staging-and-the-tests-that-pin-each-piece)).

### 3.4 The Seatbelt rules

The rules are generated from **resolved** paths only. A rule on an unresolved path matches nothing
([§6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured)'s Probe 2; `/tmp` vs `/private/tmp`). Each rule carries a `#seatbelt-test-id:<name>#`.
Ordering is last-match-wins, and it must be:

1. The existing base: `(allow default)`, the `file-write*` root deny plus the writable set, the
   `/Volumes` and `/Users` read-denies and their re-allows.
2. **Per context source:** `(allow file-read* (subpath src))`. For a source under `/Users/Shared/`,
   `ancestorLiterals` also covers its ancestors. That is the same traversal fix the workspace
   needed, with siblings still denied.
3. **Per rw source:** `(allow file-write* (subpath src))`.
4. **Every deny that exists to win, after all of the above:** keychains, `readonlyDenies`, and
   **per ro source** `(deny file-write* (subpath src))`.

A ro source outside the writable set is already unwritable, so its write deny is belt-and-braces.
A **ro source inside the writable set** (the workspace, a rw source, `/tmp`) is **refused on
macos-user**. With no mount namespace, one path carries one mode. Emitting the deny would silently
make part of the workspace read-only, and omitting it would silently make the "read-only" mount
writable. The container backends can give the same bytes two modes at two paths. This backend
cannot.

### 3.5 The DAC half

Seatbelt only narrows. The sandbox uid still needs POSIX (plus ACL) access, and it is a foreign
uid. v1 therefore:

- **Runs the DAC preflight** as `_yolojail` on each source before the sandbox starts: read and
  traverse, plus write for rw. A failure is the fatal refusal, naming the source, the uid, and the
  remedy (move it under the shared root, or use a container backend). The kernel is asked directly;
  the answer is never computed from mode bits, because ACLs decide too.
- **Sites ro sources** anywhere outside a real `/Users/<name>` home: under `/Users/Shared`, or
  outside `/Users` entirely (`/opt`, `/usr/local`, `/Library/...`).
- **Sites rw sources under the shared root only.** Only there does a file the sandbox creates
  inherit an ACE that lets the host user modify it later. Elsewhere, a `_yolojail`-owned file in
  the user's tree would be readable but not editable by the user, which is a silent ownership
  trap.
- **Refuses sources inside a real home**, as `PlanInvariants` does for the workspace ("shares only
  neutral ground"). Even where DAC passes (a `0755` home), git-style upward stats would need
  ancestor grants inside the home, and `ancestorLiterals` refuses those on purpose. Relaxing this is
  [OQ-CX7](#OQ-CX7).

The preflight checks the **root** of the source, not the subtree. A `0700` subdirectory fails at
use time with `EACCES`, where rootless-podman root would have read it. That is a stated delta.
The briefing says so, and nothing hides it.

**The ACL grant the maintainer raised, and its lifecycle, if [OQ-CX7](#OQ-CX7) wants it.** The
shape follows `EndpointGrantCommands`: a `user:_yolojail` ACE, so the host user's own membership
in `SandboxGroup` does not widen it, plus `search` on each ancestor. What it costs:

- **A new read-only ACE.** The shipped `WorkspaceACLAces` rights grant write, delete and chown.
- **A recursive walk** (`FixPermissionsScript`'s shape), because inheritance applies at create time
  only. Files moved in later with `mv` do not inherit.
- **Persistence.** The ACEs outlive the session and survive in the user's own tree, so cleanup
  needs a recorded ledger of what was granted and a `chmod -a` walk (`WorkspaceACLStripScript` is
  the all-or-nothing `-N` version, which would also strip ACEs the user set).
- **UUID voiding.** An ACE names a UUID, so a torn-down and recreated account voids every grant
  while `ls -le` still shows it (`WorkspaceGrantedScript`).
- **Losing the backstop.** `search` on `/Users/<user>` removes the DAC wall that today backs up
  the profile's `/Users` deny. The runbook's measured `EACCES` on `~/.ssh` would then rest on
  `.ssh`'s own mode alone.

[§6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured) declined this for exactly these reasons, and nothing here changes them. They are what the
ruling would be accepting.

### 3.6 `/Volumes` and TCC

- **`/Volumes`.** The profile denies reads under `/Volumes` except `Macintosh HD`. A source on an
  external or network volume needs a per-source re-allow after that deny. It is unmeasured
  whether volume ownership flags ("ignore ownership") and TCC's removable-volume and
  network-volume protections allow a `sudo -u _yolojail` process through.
- **TCC-protected dirs** (Desktop, Documents, Downloads, iCloud Drive). By default they are `0700`
  in a real home, so the siting rule and the DAC preflight refuse them before TCC is reached. TCC
  matters only if [OQ-CX7](#OQ-CX7) opens home sources. Which TCC database and which responsible
  process apply to a `sudo -u` child of a terminal app is unmeasured.

v1 refuses both ([OQ-CX8](#OQ-CX8)).

### 3.7 What cannot be matched

- **Path identity.** There is no `/ctx`. `pwd -P`, `realpath` and `git rev-parse --show-toplevel`
  print the host path, and tools that record paths (build caches, `compile_commands.json`) record
  host paths.
- **Two modes for one set of bytes** ([§3.4](#34-the-seatbelt-rules)): refused.
- **Custom destinations outside `/ctx`** ([§3.2](#32-where-the-bytes-are-named)): refused.
- **Subtree readability** ([§3.5](#35-the-dac-half)): stated, not hidden.
- **Liveness is *better* than DP-L1's copy.** The bytes are live, so the snapshot delta [§6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured)
  worried about for a growing log dir does not arise.
- **Concurrency of the name.** `StagedCtxRoot` is per workspace and replaced each launch, so a
  second concurrent launch of the *same* workspace with different `mounts` re-points the first
  jail's names. This is an existing property of that tree, not a new one. Access is unaffected,
  because each process keeps its own profile.
- **Hard links: unmeasured.** Path-keyed rules may be sidestepped by a hard link, created in a
  writable dir, to a file the profile denies, on the same volume. If that works, it works against
  today's workspace and `/tmp` equally. A rw source adds one more writable dir, and no new class.

### 3.8 What the briefing says

The "## Additional Context Mounts" section drops "(read-only)" from its heading and labels each
entry instead:

```text
- `$YOLO_CONTEXT_DIR/data` (read-write; host `~/scratch/datasets`)
- `$YOLO_CONTEXT_DIR/lib` (read-only; host `/opt/lib`)
```

It prints the path the agent actually uses, meaning the expanded value per backend. **Pack mounts
are listed too**, with their pack name. Today they are absent, so an agent learns a pack mount's
path only from the pack's own text. The Limitations line becomes "context mounts under
`$YOLO_CONTEXT_DIR` are read-only unless marked read-write". On macos-user, one more line states
the [§3.7](#37-what-cannot-be-matched) deltas that change agent behavior: real paths are visible,
and subtrees may be unreadable. `MountDescriptions`' `host:container` string cannot carry a mode,
and its reader splits on the first colon, so that shape must become a struct. The field names are
the implementer's.

## 4. Staging, and the tests that pin each piece

Each step ships alone. Every test named below **fails if the production call site is deleted**,
not merely if the callee changes.

| Step | What ships | The call-site test | Mac hardware? |
|---|---|---|---|
| **1** | rw on podman and Apple Container: the object form, the trust-predicate hook (its computation is [`workspace-config-trust.md`](workspace-config-trust.md)'s), the refusal set, the launch disclosure, per-entry briefing labels | the assembled argv from `Run`'s assembler carries `src:dest` with no `:ro` for an rw entry, and `:ro` for every other entry; an rw element the trust predicate rejects fails `yolo check`, and the launch refuses it; each refusal fires from `Run` on a fixture `$HOME`; the disclosure line appears on the launch stream **with `YOLO_NO_BANNER=1` set**; the briefing from `prepare` carries both labels | no |
| **2** | `YOLO_CONTEXT_DIR` on every backend; the briefing prints it | the container argv carries `YOLO_CONTEXT_DIR=/ctx` on podman **and** Apple Container; the macos-user `RunPlan` bootstrap env carries it | no |
| **3** | build DP-D15's fatal refusal for anything [§3](#3-delivering-context-dirs-on-macos-user) does not deliver, and fix the banner contradiction (DP-B2) | a macos-user plan with a declared, undeliverable mount refuses from `Run`; with no `mounts` key it does not refuse; the banner stops announcing a read that does not happen | no |
| **4** | macos-user ro delivery: links in `StagedCtxRoot`, Seatbelt rules, DAC preflight, siting | `SeatbeltProfile` output from the **plan builder** (not a direct call) contains each allow and deny with its test-id, in the [§3.4](#34-the-seatbelt-rules) order; `PlanInvariants` refuses a plan with a context link lacking a matching allow, or a ro source inside the writable set | **yes** (below) |
| **5** | macos-user rw delivery (shared root only) | as step 4, for the write allow, plus a nested-ro-in-rw case | **yes** |
| **6** | pack rw, only if [OQ-CX4](#OQ-CX4) rules for it | the footprint sentence and the argv | no |

Step 3 is worth shipping even if everything after it is rejected: it closes a ruled-but-unbuilt
refusal and a live misleading disclosure. Steps 1–3 are verifiable on Linux in-jail. The
rootless-ownership cells of [§2.5](#25-ownership) are not verifiable there
([AGENTS.md's carve-out](../../AGENTS.md#testing): a nested podman is rootful), and need a real
rootless host or CI.

**Mac-hardware probes** (a `sandbox-exec` run under the generated profile as `_yolojail`, extending
`integration/macosuserseatbelt_test.go`'s registry). Each one must show:

- a ro source is readable and unwritable (`EPERM`, not `EACCES`, which is what proves the profile
  decided);
- a rw source is writable;
- a sibling of either stays denied;
- an agent-created link to a denied path is refused;
- a relative `..` walk out of a source is refused;
- the root-owned link cannot be replaced;
- the DAC preflight refuses a `0700` source;
- git works inside a shared-root source (the ancestor-literal case);
- a hard link into a rw source (records the answer to [§3.7](#37-what-cannot-be-matched));
- a `/Volumes` source and a TCC dir (records the answer to [OQ-CX8](#OQ-CX8)).

## 5. What this does NOT propose

- **No copy of any directory.** DP-L1 keeps its file-shaped cells, and DP-D15's size argument
  stands.
- **No locking** across jails ([§2.6](#26-concurrency)).
- **No `chown`, relabel, or ACL mutation on container backends.**
- **No rw from packs in v1.** Which config files may declare rw is not proposed here
  ([§2.2](#22-where-an-rw-mount-may-be-declared-deferred)).
- **No `/ctx` on macOS**, and no `synthetic.conf`.
- **No change to what string `mounts` entries do on the container backends.**

## 6. Alternatives, with verdicts

| Alternative | Verdict |
|---|---|
| `:rw` suffix on the string form | **Rejected.** Paths may contain colons, and `:ro` already mis-parses silently. |
| Separate `writable_mounts` key | **Viable.** The key-level scope check is simpler; the cost is a second list for one concept ([OQ-CX1](#OQ-CX1)). |
| macos-user: copy the tree | **Rejected** by DP-D15 (size). |
| macos-user: links in the sandbox home or workspace sidecar | **Rejected.** Agent-writable names, plus machine-wide contention. |
| macos-user: no link, hand the agent the resolved path | **Viable, and weaker.** It has the same enforcement but no `/ctx`-shaped name, so pack text naming `/ctx/<into>` cannot resolve through `remapCtx`. |
| macos-user: ACL grants for home sources | **Deferred** to [OQ-CX7](#OQ-CX7). [§6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured)'s reasons stand. |
| Lock a shared rw dir | **Rejected.** No tool expects it, and a stale lock is its own outage. |

## 7. Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| A user mounts a dir a host tool trusts (a dotfiles repo, a CI config dir) rw, and the jail writes into it | medium | the injection sentence on every launch; the trust predicate ([§2.2](#22-where-an-rw-mount-may-be-declared-deferred)) |
| A future edit orders a new allow after a deny that must win | medium | the ordering test ([§4](#4-staging-and-the-tests-that-pin-each-piece), step 4); the test-id registry |
| Subuid-owned files the host user cannot delete (rootless, non-root writer) | low | documented in [§2.5](#25-ownership) and `config-ref` |
| Hard-link bypass of path rules | unknown | measure ([§4](#4-staging-and-the-tests-that-pin-each-piece)); it exposes today's workspace equally |
| v1 macos-user refuses the common case (home-sited sources) and reads as broken | high | the refusal names the remedy; [OQ-CX7](#OQ-CX7) |

## Open Questions

1. 💬 <a id="OQ-CX1"></a>**[OQ-CX1](#OQ-CX1): object elements in `mounts`, or a separate key?**
   **(a)** A per-element `mode` in `mounts` ([§2.1](#21-config-shape)). **(b)** A new
   `writable_mounts` key. A key-level scope rule, like `cache_relocations`', is simpler to enforce
   than an element-level one, which matters only if
   [`workspace-config-trust.md`](workspace-config-trust.md) rules for scope rather than a trust record.

   _Leaning:_ **(a)**. It gives one list, one briefing section and one duplicate check. Either
   shape can carry the trust predicate from [§2.2](#22-where-an-rw-mount-may-be-declared-deferred).

   <!-- vantage: oq id=OQ-CX1 leaning="(a): a per-element mode in mounts. One list, one briefing section, one duplicate check; either shape can carry the trust predicate of section 2.2." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-CX2"></a>**[OQ-CX2](#OQ-CX2): does the rw refusal set name credential dirs
   (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/gh`, …)?** **(a)** The boundary predicate plus
   workspace overlap only ([§2.3](#23-refusal-set)). **(b)** Also a named list.

   _Leaning:_ **(a)**. An actor who passes the trust predicate already has host-user authority
   ([gate-placement Test 1](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it)).
   A named list rots, and it reads as a completeness claim nobody maintains. The disclosure names
   the path every launch.

   <!-- vantage: oq id=OQ-CX2 leaning="(a): the boundary predicate plus workspace overlap only. An actor who passes the trust predicate already has host-user authority (gate-placement Test 1); a named credential-dir list rots and reads as a completeness claim; the disclosure names the path every launch." -->

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-CX3"></a>**[OQ-CX3](#OQ-CX3): does the ro form get the boundary predicate too?**
   Today a ro mount of `$HOME` or of yolo's state dir is accepted. This repo's own
   `yolo-jail.jsonc` mounts `~/.local/share/yolo-jail/logs` read-only, and that mount would trip
   the "inside a yolo dir" clause.

   _Leaning:_ **no**. Reading is not the injection channel, and the ro form is workspace-scopable
   behind the diff prompt. Refusing would break a mount this repo relies on.

   <!-- vantage: oq id=OQ-CX3 leaning="No: the ro form keeps today's rules. Reading is not the injection channel, the ro form is workspace-scopable behind the diff prompt, and this repo's own yolo-jail.jsonc mounts ~/.local/share/yolo-jail/logs ro, which the predicate would refuse." -->

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-CX4"></a>**[OQ-CX4](#OQ-CX4): may a pack declare a rw `mount`?**
   ([§2.7](#27-packs).)

   _Leaning:_ **not in v1**. There is no approval gate any more ([OQ-TP9](trust-paths.md#decision-ledger)), so a selected pack
   would write into the user's home on disclosure alone, and no shipped pack needs it. Revisit
   with a concrete pack.

   <!-- vantage: oq id=OQ-CX4 leaning="Not in v1. With OQ-TP9's gate gone a selected pack would get a writable path into the user's home on disclosure alone, and no shipped pack needs it. Revisit with a concrete pack." -->

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 <a id="OQ-CX5"></a>**[OQ-CX5](#OQ-CX5): does
   [DP-D15](declaration-parity.md#7-ruled-divergent-and-the-ones-i-would-re-open) still hold?**
   **(a)** Keep it: every macos-user context mount refuses, and v1 only builds the refusal.
   **(b)** Narrow it: deliver by link plus Seatbelt wherever the DAC preflight and siting rules
   pass, and refuse fatally elsewhere ([§3.1](#31-does-dp-d15-still-hold)).

   _Leaning:_ **(b)**. DP-D15's reason was size, and its premise was that copying is the only
   mechanism. For a reachable source neither applies: nothing is copied, and the bytes are live.

   <!-- vantage: oq id=OQ-CX5 leaning="(b): narrow DP-D15. Deliver by link plus Seatbelt where the DAC preflight and siting rules pass; refuse fatally elsewhere. DP-D15's reason was size and its premise was that a copy is the only mechanism; for a reachable source nothing is copied." -->

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 <a id="OQ-CX6"></a>**[OQ-CX6](#OQ-CX6): `YOLO_CONTEXT_DIR`, on every backend?**
   ([§3.2](#32-where-the-bytes-are-named).) The name is coined here, and it sits beside the
   existing `YOLO_CTX_ROOT`, which means something else.

   _Leaning:_ **yes, on every backend, always exported, under this name** (or another the
   maintainer prefers). Parity is the point: pack text and agents write one spelling everywhere.
   Renaming `YOLO_CTX_ROOT` instead is out of scope.

   <!-- vantage: oq id=OQ-CX6 leaning="Yes: export YOLO_CONTEXT_DIR on every launch and backend (/ctx on podman and Apple Container, StagedCtxRoot on macos-user), so pack text and agents use one spelling; the name is the maintainer's to change." -->

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 <a id="OQ-CX7"></a>**[OQ-CX7](#OQ-CX7): sources inside a real user home on macos-user.**
   **(a)** Refuse, as v1 does. **(b)** An opt-in per-source ACL grant with a recorded cleanup
   ledger ([§3.5](#35-the-dac-half)). **(c)** Allow where DAC already passes, emitting ancestor
   literals inside the home.

   _Leaning:_ **(a) for v1**. (c) extends `ancestorLiterals` into homes, which it refuses
   deliberately, and whether a `literal` allows listing the directory is unmeasured. (b) is [§6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured)'s
   declined consent surface. If the refusal proves intolerable, prefer (b) over (c).

   <!-- vantage: oq id=OQ-CX7 leaning="(a) for v1: refuse home-sited sources. (c) extends ancestorLiterals into homes, which it deliberately refuses; (b) is §6.1's declined consent surface. If the refusal proves intolerable, prefer (b) over (c)." -->

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 <a id="OQ-CX8"></a>**[OQ-CX8](#OQ-CX8): `/Volumes` and TCC-protected sources.**
   ([§3.6](#36-volumes-and-tcc).)

   _Leaning:_ **refuse until measured**, naming the reason. Record the probe results from
   [§4](#4-staging-and-the-tests-that-pin-each-piece) and revisit.

   <!-- vantage: oq id=OQ-CX8 leaning="Refuse /Volumes and TCC-protected sources on macos-user until measured; record the probe results and revisit." -->

   **Answer:**
   > _(empty — fill in when decided)_

9. 💬 <a id="OQ-CX9"></a>**[OQ-CX9](#OQ-CX9): rw on rootful podman.** Writes land host-root-owned
   in the user's tree.

   _Leaning:_ **disclose, don't refuse**. Rootful is uncommon and deliberate, and the launch line
   says what will happen.

   <!-- vantage: oq id=OQ-CX9 leaning="Disclose, don't refuse: rootful is uncommon and deliberate, and the launch line names the root-owned-writes consequence." -->

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling | Date | Folded into |
|---|---|---|---|
| — | _none yet_ | | |

## Appendix A: Evidence

All of these are symbols, checked against the working tree on 2026-09-25.

- **Config `mounts` delivery.** `run.splitMountSpec` (last colon followed by `/`) and the
  `config.mounts` loop in `internal/cli/run/assemble.go` (`-v host:container:ro`, missing paths
  skipped). `run.appliedCtxMounts` and `roBindsUnsupported`/`acROBindsFloor`
  (`internal/cli/run/backendcaps.go`). `mountDescriptions` in `internal/cli/run/prepare.go`.
  `config.validateMounts` checks shape and warns on existence; there is no boundary check. The
  `mounts` entry in `internal/cli/config_ref.txt` says "There is no writable form".
- **Pack `mount`.** `run.hostMountArgs` (`internal/cli/run/packhostgrants.go`): the source is
  `homeDir()/From`, the destination is `/ctx/<into>`, and the doc comment says "this grant's reader
  is the AGENT". `packload.Pack.HonoredMounts` refuses nothing. The footprint sentence is in
  `internal/packload/footprint.go`. It is absent from the briefing: `MountDescriptions` is built from
  config `mounts` only.
- **The existing scope precedent** (input to [`workspace-config-trust.md`](workspace-config-trust.md)). `config.validateCacheRelocations` and
  `config.LoadCacheRelocations`: the key is read from user config only, and a workspace-scoped key
  is a `yolo check` error.
- **The credential-boundary predicate.** `paths.WorkspaceScopeBreach` and `paths.scopeExempt`
  (`internal/paths/workspacescope.go`) resolve both sides and do not refuse `~/.ssh`.
  `run.refuseWorkspaceScope` is its launch caller.
- **Ownership.** `podmanNestingArgs` (`internal/cli/run/assemble_parts.go`) supplies the rootless
  uidmaps and `--userns host` when nested. `label=disable` is in the same file.
  `docs/reference/jail-home.md` covers "Ownership and UIDs".
- **Host CAS.** `internal/hostcas`, `internal/cli/run/hostcasalias.go`, and
  [OQ-BF10](disk-levers-and-backfill.md#OQ-BF10).
- **macos-user today.** `run.noteMacosUserCtxMountGaps` (`internal/cli/run/macosctxtree.go`) warns
  and gives COPY as the reason. `macosuser.StagedCtxRoot` and `StageCtxCommands` (root-owned,
  `a+rX`, rm-then-mv). `YOLO_CTX_ROOT` is set in the bootstrap env only when a tree is staged, and
  `PlanInvariants` checks that. `entrypoint.ctxRootDir` and `remapCtx` read it per call.
- **Seatbelt.** `macosuser.SeatbeltProfile`, `readonlyDenies` and `ancestorLiterals`
  (`internal/macosuser/seatbelt.go`). `ancestorLiterals` covers only `/Users/Shared/`, "a real
  user's home must NOT gain traversal grants". The test-id registry is
  `integration/macosuserseatbelt_test.go`, and `TestEverySeatbeltDenyCarriesATestID` checks that
  every deny has an id.
- **Probes (measured 2026-09-13, macOS 26.5 arm64).**
  [`declaration-parity.md` §6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured):
  Seatbelt judges the symlink target, whether it is absolute or relative. A rule on an unresolved
  path matches nothing. `/var/yolo-jail` is readable and unwritable (`EPERM`, not `EACCES`).
- **ACLs.** `macosuser.WorkspaceACLAces` (the rights include write, delete and chown),
  `SharedRootProvisionCommands` (`2770`, inheriting group ACEs), `FixPermissionsScript`,
  `WorkspaceGrantedScript` (inheritance at create time only, UUID voiding),
  `WorkspaceACLStripScript` (`chmod -N`), `EndpointGrantCommands` (a `user:` read ACE plus
  `search` on the parent, and why `user:` rather than `group:`).
- **Neutral ground.** `macosuser.HomeContaining` and `PlanInvariants` ("shares only neutral
  ground").
- **The briefing.** `jailcontent.BriefingContent` (`internal/jailcontent/briefing.go`) renders
  "## Additional Context Mounts (read-only)", splits on the first colon, and has the conditional
  Limitations bullet.
- **Rulings re-opened or relied on.** DP-D15, DP-B1, DP-B2, DP-L1 and [§6.1](declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured) in
  [`declaration-parity.md`](declaration-parity.md). [OQ-TP9](trust-paths.md#decision-ledger) in [`trust-paths.md`](trust-paths.md).
  [OQ-RO3](../reference/report-tiers.md#why-its-this-way) in [`../reference/report-tiers.md`](../reference/report-tiers.md).
