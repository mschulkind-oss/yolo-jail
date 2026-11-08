---
status: current
stage: CURRENT
verified: 2026-10-16
verified_commit: 8e65f3444
covers:
  - internal/config/mounts.go
  - internal/config/brokerfence.go
  - internal/paths/contextdir.go
  - internal/paths/workspacescope.go
  - internal/cli/run/ctxmounts.go
  - internal/cli/run/macosctxtree.go
  - internal/cli/run/backendcaps.go
  - internal/cli/run/packhostgrants.go
  - internal/macosuser/ctxlinks.go
  - internal/macosuser/seatbelt.go
  - internal/macosuser/runplan.go
  - internal/jailcontent/briefing.go
tags: [mounts, ctx, macos-user, seatbelt, trust, parity, disclosure]
summary: "How a host path outside the workspace reaches a jail: the config `mounts` key in a read-only and a read-write form, a pack's `mount` contribution, the context dir every backend names in YOLO_CONTEXT_DIR, and the macos-user delivery where a root-owned link names the bytes and the Seatbelt profile decides access."
---

# Context mounts — host paths a jail sees outside the workspace

**Status:** Verified 2026-10-16 against `8e65f3444`. MEASURED on a Mac: every Seatbelt context
case, the DAC preflight, a real launch reading and writing through `$YOLO_CONTEXT_DIR` and the
hard-link measurement passed in the scheduled `macos-user.yml` run
[37121866798](https://github.com/mschulkind-oss/yolo-jail/actions/runs/37121866798) at
`0e34798c6`, 2026-10-03. The `/Volumes` half of [`OQ-CX8`](#oq-cx8) was MEASURED 2026-10-18 on
a developer Mac, two of its three volume kinds ([below](#unbuilt-and-unmeasured)). UNMEASURED:
an ownership-*enabled* volume, and everything built after that run: the copies of a pack's single-file
`mount` and of a directory `host_files` entry ([CX-D25](#CX-D25)), and the closed staged-tree
root ([CX-D26](#CX-D26)). See [Unbuilt and unmeasured](#unbuilt-and-unmeasured).

A **context mount** makes a host path visible to a jail outside the workspace. There are two
declarations: a config `mounts` element and a pack `mount` contribution
(`packdecl.KindMount`). On the container backends one bind does two jobs at once: it makes the
host bytes *appear* at a named path, and it *decides who may write them*. On macos-user the two
jobs come apart. A root-owned symbolic link does the naming, the Seatbelt profile does the
deciding, and it judges the resolved target of every access, never the link.

| Component | Lives in |
| :--- | :--- |
| The `mounts` key: both element forms, the trust predicate, the refusal set | `internal/config` (`ParseMounts`, `rwMountTrusted`, `LoadRWMounts`, `rwMountRefusal`, `validateMounts`, `MountElementWhere`) |
| The credential-boundary predicate the refusal set shares | `internal/paths` (`WorkspaceScopeBreach`, `WritableSourceScopeBreach`) |
| The context dir, its env var, yolo's reserved children | `internal/paths` (`ContextDirEnv`, `ReservedContextPaths`) |
| The container argv, the launch disclosure, the briefing entries | `internal/cli/run` (`configCtxMounts`, `packCtxMounts`, `rwMountDisclosure`, `briefedCtxMounts`, `hostMountArgs`) |
| The macos-user delivery: siting, links, copies, the preflight | `internal/cli/run` (`planMacosUserCtxMounts`, `macosCtxLinks`, `buildMacosCtxTree`, `noteMacosUserRWMounts`), `internal/macosuser` (`SiteContextLinks`, `ContextSiting`, `SiteContextCopies`, `StageContextDirCommands`, `ContextPreflight`, `SeatbeltProfileWithContext`, `PlanInvariants`) |
| The briefing's section | `internal/jailcontent` (`ContextMount`, `BriefingContent`) |

**Reads with:** [`jail-home.md`](jail-home.md) (the rest of the mount table, and the other
config keys that add mounts), [`macos-user-home-tiers.md`](macos-user-home-tiers.md) (the
Seatbelt argument this generalizes), [`gate-placement-principle.md`](gate-placement-principle.md)
(why a declared read-write mount is disclosed rather than gated).
[`../design/workspace-config-trust.md`](../design/workspace-config-trust.md) owns which config
files may declare a read-write element, and
[`../design/declaration-parity.md`](../design/declaration-parity.md) owns the parity rulings
(DP-D15, DP-L1) this builds on. `yolo config-ref` is the authority for the key's syntax.

---

## Defined terms

- **Context source** — the host path a context mount names, after `~` expansion and symlink
  resolution on the host at launch.
- **rw context mount** — a context mount whose destination the jail may write. Only a config
  `mounts` element in the object form with `"mode": "rw"` is one.
- **Context dir** — the directory the agent reaches context mounts through: `/ctx` on the
  container backends, and on macos-user the root-owned tree at `macosuser.StagedCtxRoot`. It is
  named by `YOLO_CONTEXT_DIR` on every backend ([The context dir](#the-context-dir)).
- **Composition-input root** — what `YOLO_CTX_ROOT` names: where the *entrypoint* reads host
  bytes it composes from (`entrypoint.ctxRootDir`, `remapCtx`). It is not agent-facing. Absent on
  podman, `~/.yolo-ctx` on Apple Container (so there it differs from the context dir), and
  `macosuser.StagedCtxRoot` on macos-user.
- **Neutral ground** — on macos-user, a location outside every real `/Users/<name>` home. In
  practice that is the shared root `/Users/Shared/yolo`, which carries inheriting `_yolojail`
  group ACEs (`macosuser.SharedRootProvisionCommands`).
- **DAC preflight** — a check run as the sandbox account before the sandbox starts, asking the
  kernel whether that account can read (and, for rw, write) a context source. It covers POSIX
  permissions plus ACLs, with no Seatbelt involved.
- **Writable set** — the paths `macosuser.SeatbeltProfile` re-allows for `file-write*` after its
  root deny: the workspace, the sandbox home, the machine temp directories, and `/dev`.

All but the first two were coined by the design that built this, and survive here.

## Invariants

- **Never emit `:z` or `:Z` on a context mount.** The run path uses `label=disable`, and a
  relabel would rewrite SELinux labels on the host tree.
- **Never decide a read-write mount from a config view that has lost its provenance.** The mount
  builder takes rw elements only from sources the trust predicate admits
  ([Where a read-write mount may be declared](#where-a-read-write-mount-may-be-declared)).
- **Never create, chown or chmod a context source on a container backend.** A missing source,
  either mode, is skipped with a warning (an optional one with one skip line).
- **Optional never skips a safety refusal** ([CX-D28](#CX-D28)).
- **Never hand out a writable mount as the fallback for a read-only one.** That is
  `roBindsUnsupported`'s rule, and it covers the new mode too.
- **rw is not gated by `acROBindsFloor`.** The Apple Container floor exists for `:ro`; an rw
  bind works on every version, and refusing it there would refuse it for a property it does not
  need.
- **On macos-user every access decision is the profile's.** No design choice may rely on the
  link for confinement ([The link is only a name](#the-link-is-only-a-name));
  `PlanInvariants` refuses a plan whose context link lacks its matching allow.
- **One parser.** Every reader of the `mounts` key parses through `config.ParseMounts`, the
  broker mount fence (`config.validateBrokerMountFence`) included, so no reader can skip an
  object element ([CX-D1](#CX-D1), [CX-D8](#CX-D8)).

## Read-write context mounts

### The config shape

The string form is **read-only only**: `"~/code/lib"` binds `:ro` at `/ctx/<basename>`, and
`host:container` names the destination. The writable form is an **object element** in the same
list:

```jsonc
"mounts": [
  "~/code/lib",                                        // read-only, /ctx/lib
  { "host": "~/scratch/datasets", "at": "/ctx/data", "mode": "rw" },
  { "host": "~/notes", "mode": "ro" }                 // object form, read-only, /ctx/notes
]
```

<a id="CX-D1"></a><a id="CX-D8"></a><a id="CX-D24"></a>

- `mode` is required in the object form and is `"ro"` or `"rw"`, with no default, because a
  mode nobody wrote is how a writable mount gets handed out by accident.
- `at` defaults to `/ctx/<basename after resolution>`. **For `rw`, `at` must lie under `/ctx`**
  and may not be `/ctx` itself: a writable bind elsewhere could shadow a home path or a composed
  surface, and `writable_home_dirs` and `cache_relocations` are the shaped tools for those cases.
- A `:rw` string suffix is **refused with a message naming the object form**, never parsed as a
  mode: paths may contain colons, and a string suffix already mis-parses silently. The object
  form also refuses an unknown key, a colon in `host` or `at` (nothing in `-v src:dest[:ro]`
  escapes one), and a `..` in `at`; `at` is cleaned ([CX-D8](#CX-D8)).
- The object form is an element of `mounts`, not a separate `writable_mounts` key: the broker
  mount fence already scopes `mounts` per element, so a second list would buy nothing
  ([CX-D1](#CX-D1)).
- A missing source, either mode, is skipped with a warning that names the element as written and
  the file, line and column that wrote it, then the fix and the check (`MountElementWhere`,
  [CX-D24](#CX-D24)). yolo **never creates** a context source.

<a id="CX-D11"></a><a id="CX-D15"></a>

**Destination collisions are a `yolo check` error.** Two context mounts at one destination,
pack `/ctx/<into>` grants included, are refused, and so is a `mounts` element at, inside or
around one of yolo's own children of `/ctx` (`paths.ReservedContextPaths`: the pack trees, the
capture store, `host_files` sources, the host nvim config). The error reports only a collision a
`mounts` element is party to, counts only an element or grant whose source exists (the launch
skips the rest, so they land nowhere), and is skipped for the jail's own workspace inside a jail,
where a bare element's destination would be computed from host paths that are not there
([CX-D11](#CX-D11)). The reservation is unconditional, including on a launch that makes no such
bind, so a config's validity does not depend on whether this machine has an nvim config.
Without it a bare `~/work/packs` passed `yolo check` and podman refused it late, and a nested
one had podman create its mountpoint inside the outer bind's host directory, even through a
`:ro` bind ([CX-D15](#CX-D15), measured with podman 5.8.7 in a nested, rootful jail). Two
declarations nesting is the user's own layout and is not refused on the container backends.

### Optional mounts

<a id="CX-D27"></a><a id="CX-D28"></a><a id="CX-D29"></a><a id="CX-D30"></a>

An object element may say `"optional": true` (default `false`), ruled as the
[`OQ-CX7` amendment](#oq-cx7-amended). A required element a backend cannot deliver refuses as
before; an optional one is skipped with one plain line and the launch goes on:

```jsonc
{ "host": "~/.local/share/yolo-jail/logs", "at": "/ctx/host-yolo-logs", "mode": "ro", "optional": true }
```

```text
skipped optional mount: ~/.local/share/yolo-jail/logs → /ctx/host-yolo-logs (macos-user can't reach a folder inside a home)
```

- **The key is `optional` on the existing object form**, beside `host`, `at` and `mode`, not a
  new `source`/`target` spelling ([CX-D27](#CX-D27)). The object form already carries every
  per-mount option, and `mode` stays required, so an optional element still says its mode. The
  string form has no optional spelling and always means required.
- **Optional means "may be absent", never "may be unsafe"** ([CX-D28](#CX-D28)). It skips only
  delivery failures: a missing source, the macos-user siting's reach and layout rules, the
  sandbox account's DAC preflight, an Apple Container below the `:ro` floor, and a source a macOS
  Podman Machine does not share. It never skips the [refusal set](#the-refusal-set)
  (`paths.WritableSourceScopeBreach` included), the broker mount fence, a `yolo check` destination
  collision, or the three macos-user siting reasons marked `Unsafe`: a source containing every home,
  inside the sandbox account's home, or overlapping yolo's state directory. Those refuse an
  optional element exactly as a required one, and they are asked on their own whenever a link is
  refused at all (`ContextSiting.unsafeVerdict`, over every alias spelling of the source), so a
  delivery rule that happens to refuse first, such as an `at` outside `/ctx` or a Data-volume
  spelling, cannot hide one.
- **The string form has no optional spelling:** a `:optional` suffix is refused, as `:rw` is,
  with the object form to paste.
- **A missing source** is a skip line for an optional element and no validation warning, so the
  launch says it once; a required element keeps its warning and file location, unchanged
  ([CX-D24](#CX-D24), [CX-D29](#CX-D29)).
- **Every backend, one line** (`config.OptionalMountSkipLine`): the host path as written, the jail
  path, and a few-word reason. On macos-user, dropping an optional link re-sites the rest, so a
  required link refused only for nesting with it is delivered. `yolo check` prints the same lines
  as information, never `[WARN]` or `[FAIL]`, through `run.OptionalMountSkips`, which predicts no
  pack-grant collision, Podman Machine share or Apple Container floor ([CX-D30](#CX-D30)).
- **The macos-user refusal** now reads as a headline, the refused mounts with their brief reasons,
  then the fixes: copy the folder under the shared root, mark it optional (with the exact JSON for
  each element that may be), remove it, or use a container runtime.
- **The briefing marks every optional element "optional; may be absent".** The macos-user
  briefing is composed before the DAC preflight runs, so an element skipped by the preflight alone
  is still listed there; the marker and the launch's skip line say it may not be present.

### Mount sources and links

[oq-cx-symlink](#oq-cx-symlink) governs every `mounts` element a **workspace** config declares,
read-only or writable, JSONC or TOML. A user-scope element is not judged by it.

- **A source written inside the workspace, or inside another mount, is refused.** "Written" means
  the path as the entry spells it, with `~` expanded and made absolute, before any symbolic link
  is followed. The jail already has the workspace at `/workspace`, and a link there is the
  agent's: `mounts = ["./notes"]` with `notes` linked to `~/.ssh` would otherwise bind `~/.ssh`.
  "Another mount" is any other element of the workspace or user config, matched as written and
  as resolved. `yolo check` and the launch both refuse it at the entry's line.
- **Any other link is followed, and the approval names where it lands.** The config-change
  prompt, the no-terminal refusal, the `--accept-config-changes` report and
  `yolo check --accept-config-changes` show each mount's resolved destination, with the path as
  written beside it when the two differ. The approval record holds those destinations, so a link
  repointed after the approval changes the record and asks again.
- **The launch binds the approved destination.** It resolves the source again and refuses the
  launch, naming both paths, when the source now resolves elsewhere; the next step is to launch
  again and approve the new destination at the prompt.

The first launch after this rule shipped asks once for every workspace that declares a mount,
because the record now holds the destinations.

### Where a read-write mount may be declared

<a id="CX-D7"></a>

The **trust predicate** answers one question: is this rw element from a trusted source? It is
`config.rwMountTrusted`, a function of the scope an element was declared in, and today it admits
the **user scope** alone: `~/.config/yolo-jail/config.jsonc`, the files it includes, and a
`--user-layer` ([CX-D7](#CX-D7)). Both halves read it. `config.LoadRWMounts` reads the user scope
directly and is the only source the run pipeline mounts a read-write element from;
`validateMounts` re-reads the workspace config and makes a read-write element there a
`yolo check` error, and so a launch refusal. It is never silently dropped and never downgraded
to read-only, because a downgrade is a mount the user did not write.

Inside a jail the user scope is that jail's own, whose generated `config.jsonc` never carries
`mounts`, so only a `--user-layer` can declare one; a nested launch honors it against the jail's
own paths, trust flowing downward. String and `"mode": "ro"` elements keep the ordinary
workspace scoping and the [config-change diff prompt](config-safety.md).

#### A project may request one, once your config allows it

[RC-D24](../design/readable-config.md#RC-D24) supersedes [`OQ-WT1`](../design/workspace-config-trust.md#OQ-WT1) with one exception. A
**request** is a `mounts` table with `write = true` written by a project's TOML config file
itself, `yolo-jail.toml` or `yolo-jail.local.toml`. A file either one includes cannot request
one, and neither can a TOML file a JSONC config includes, so the file a human reviews is the
file that asks. The **ceiling** is
two lines in your user config:

```toml
[allow.mounts]
on_request = ["write"]
```

- **Without the ceiling, the request is refused.** `yolo check` and the launch report it at the
  entry's line, and the message names the user config file, the two lines to add, and the command that writes
  them, `yolo config add allow.mounts.on_request write --user`. `config add` and `config set`
  create a missing table or list, so the command works on a user config with no `[allow.mounts]`.
  It sets only the ceiling: each writable request is still approved by you at launch.
- **With the ceiling, the request is approved like any other config change.** The entry is part
  of the workspace config the [config-change gate](config-safety.md) diffs, so approving the diff
  approves the mount, and a changed request asks again. The prompt prints each writable mount in
  bold red above the diff, and marks the diff line that holds `"write": true`. Each such line
  carries the words `WRITABLE HOST MOUNT`, so the mount stays visible with color off. The
  no-terminal refusal shows the same block.
- **`--accept-config-changes` approves a request as it approves a repository scope change**
  (BB-D30): it records every part, as `y` would. It never approves one unseen. Before recording,
  it prints the same red block, as it already does for a changed scope.
- **`always` is refused for mounts.** A standing grant would skip the per-project approval that
  the ruling requires. `on_request` takes only `"write"`.
- **The JSONC reader is not backported.** A read-write element that a `yolo-jail.jsonc` or
  `yolo-jail.local.jsonc` wrote keeps the refusal above, even when your config allows requests.
  The check is per element: it looks at the files that wrote the element.
- **The request still meets every other rule.** The [refusal set](#the-refusal-set) applies to
  it, as do the broker mount fence, the `/ctx`-only rule and the destination rules. The ceiling
  changes only where a read-write element may be written. It never changes what the element may
  reach.

- **The host must be absolute or start with `~`.** A relative path would resolve against
  wherever yolo was started.
- **`allow` is user-scope only.** A workspace config that sets it is refused at the key.
- **The launch binds what the gate approved.** The config-change gate reads the workspace files
  once, with their record, and runs every refusal above on that read before it asks. The
  launch then binds each approved request at the destination the gate showed
  ([Mount sources and links](#mount-sources-and-links)), and nothing re-reads the files to learn
  what to mount. `yolo check --accept-config-changes` runs the same refusals on its own read,
  prints the same block and records the same destinations. Inside a jail, for the jail's own
  workspace, the request is not judged again: the host has already judged it against its user's
  ceiling.

### The refusal set

<a id="CX-D2"></a><a id="CX-D16"></a>

A rw context source is refused, fatally, on resolved paths on the host, when any of these hold
(`config.rwMountRefusal`):

1. **It trips the credential-boundary predicate**: it is or contains `$HOME`,
   `~/.config/yolo-jail` or `~/.local/share/yolo-jail`, or sits inside either yolo dir. This is
   `paths.WorkspaceScopeBreach`'s rule applied to the source, as one function body
   (`paths.WritableSourceScopeBreach`) with **no capture-store exemption**: the exemption exists
   because `yolo capture` launches a jail on its own scratch tree, and a writable mount of the
   store would hand one jail every other workspace's captured installers ([CX-D16](#CX-D16)).
2. **It overlaps the workspace in either direction.** Inside it, the mount is redundant;
   containing it, it is a second writable path to the workspace that bypasses
   `workspace_readonly`, including the overlay that locks `yolo-jail.jsonc`.
3. **On macos-user**, it is, contains or sits in yolo's state dir (`/var/yolo-jail`) or the
   sandbox home. That clause holds for both modes there, because the context dir lives in the
   state dir ([The DAC preflight and siting](#the-dac-preflight-and-siting)).

`~/.ssh`, `~/.aws` and the like are **not** in the predicate. There is **no named list of
credential dirs** ([CX-D2](#CX-D2)): an rw element passes only from a source the trust predicate
admits, whose writer already holds the host user's authority, so a list refuses nobody who could
not copy `~/.ssh` themselves ([gate-placement Test 1](gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it)),
and a list would read as a completeness claim. The read-only form keeps its ordinary rules and
takes no share of the predicate ([`OQ-CX3`](#oq-cx3)).

`yolo check` on the host runs the same predicate over the same resolution; in a jail it skips the
filesystem half, as the rest of `mounts` validation does.

### The launch disclosure

<a id="CX-D12"></a>

Each rw mount gets **one launch line, on every launch**, in bold yellow because it names a
writable hole in the boundary (`run.rwMountDisclosure`). It is a disclosure in the
[`OQ-RO3`](report-tiers.md#why-its-this-way) sense: never suppressible, teed to
`<workspace>/.yolo/logs/launch.log`, and shown under `YOLO_NO_BANNER`. It names the host path and the
jail path, and the **injection sentence**: anything on this machine that later reads the source
reads what the jail wrote there, including a symlink the jail planted. On a rootful podman host it
adds the ownership consequence ([Ownership](#ownership)), and only when podman answered
`security.rootless: false` on a real host (`run.rootfulPodmanHost`): a nested podman reports
rootful because of `--userns host`, and its writes land under the outer jail's mapping
([CX-D12](#CX-D12)). The assembler prints the line where it emits the read-write bind, so no path
can emit one without the other.

The reasoning is the [host-CAS](jail-home.md#extra-mounts-a-config-declares) gate's, turned into
a disclosure instead of a gate. A path-keyed writable directory lets the jail choose both the key
and the bytes, which is why `hostcas` admits only content-addressed stores. A user-declared rw
mount is path-keyed by definition, so it is admissible only because a trusted source named it,
and the user is told what it opens every launch. The read-only form gets no new line: the launch
banner discloses pack grants, and the briefing lists config entries.

### Ownership

| Backend | A write by in-jail root | A write by an in-jail uid other than 0 |
| :--- | :--- | :--- |
| rootless podman | the host user | a **subuid**; the host user cannot delete it without `podman unshare` |
| rootful podman | **host root** | that uid, on the host |
| nested (`--userns host`) | the outer jail's mapping | the outer jail's mapping |
| podman macOS machine, Apple Container | unmeasured (virtiofs) | unmeasured |
| macos-user | n/a | `_yolojail`; the host user can modify it only where an inheriting group ACE applies, meaning under the shared root |

<a id="CX-D6"></a>

The agent runs as root on the container backends, so the common case lands as the host user.
yolo **does not chown**: rewriting ownership in a user's directory is a mutation nobody asked
for. On rootful podman an rw mount is **disclosed, not refused** ([CX-D6](#CX-D6)): in-jail root
maps to host root there (`podmanNestingArgs`), so the workspace bind already lands its writes as
host root and nothing refuses that bind; refusing only the rw mount would close one of two
identical doors.

### Concurrency

There is **no locking, by design.** Two jails, or a jail and the host, sharing an rw source get
last-writer-wins, the same as two host processes; tools that need exclusion bring their own
(git's `index.lock`). A lock nobody expects is its own outage when it goes stale.

### Packs

<a id="CX-D3"></a>

A pack `mount` is **read-only**: `-v ~/<from>:/ctx/<into>:ro` for a directory, `ROFileMountArg`
for a single file, a missing source skipped, the read disclosed on the launch banner and in
`yolo pack footprint`, and no approval gate
([`OQ-TP9`](../design/trust-paths.md#decision-ledger)). **No rw form is built until a pack needs
one** ([CX-D3](#CX-D3)); no shipped pack declares a `mount` at all. What such a pack would need
is a footprint sentence ("WRITES to a path in YOUR HOME"), the argv and the refusal set on its
source. It would need no new gate: [`OQ-TP9`](../design/trust-paths.md#decision-ledger) made
disclosure the boundary for what a selected pack reaches on the host, and selecting the pack is
the consent.

### The one host-tool-owned writable bind

[`AGENTS.md`](../../AGENTS.md#invariants--gotchas) states which writable binds a jail gets:
**exactly one host-TOOL-owned directory is bind-mounted writable, and the gate is content
addressing** (pants' `lmdb_store`, through `hostcas`). Every other writable host bind is the
workspace, yolo's own state and cache directories, or one the user named in their user-scope
config: `cache_relocations`, and a read-write `mounts` element. Of those, only the read-write
mount gets a launch line on every launch. The sentence used to read "ONE host directory is
bind-mounted WRITABLE", which was never literally true; it was restated when the rw form was
built.

## The context dir

<a id="CX-D4"></a><a id="CX-D14"></a>

**`YOLO_CONTEXT_DIR` is exported on every launch and every backend** (`paths.ContextDirEnv`):
`/ctx` on podman and Apple Container, and `macosuser.StagedCtxRoot` on macos-user, where the
directory is created every launch, possibly empty ([CX-D4](#CX-D4)). Agents and pack content
spell context paths as `$YOLO_CONTEXT_DIR/<rel>`. macos-user cannot have `/ctx`, so a variable
is the only spelling every backend can share, and one set on some backends only would make every
reader branch. Setting it always departs on purpose from `YOLO_PACK_ROOT`'s rule that absence is
the honest signal: this directory always exists and the briefing lists what is in it, so an
empty one says "no context mounts", which is true. `YOLO_CTX_ROOT` keeps its own meaning and its
set-only-when-staged rule.

Every backend also links `~/ctx` in the agent's home to `$YOLO_CONTEXT_DIR`, so the mounts are
browsable from home ([OQ-MM6](../design/macos-user-model.md#OQ-MM6)). Per backend:

- **podman:** the host-built home skeleton lays the link, last, because the home root is read-only in
  the jail. If a `writable_home_dirs`, `host_files` or pack entry claims `ctx` or anything under it,
  the skeleton lays no link, keeps the claimed directory, and says so in one line.
- **Apple Container:** the boot step `link_context_dir` lays it (untested on hardware).
- **macos-user:** the same boot step lays it. The sandbox home is shared by every workspace, so the
  step retargets a link that points into yolo's own staged context tree
  (`YOLO_CTX_STAGED_PARENT`) and leaves any other link or entry alone. It is unit-tested only; it has
  not run on a Mac.

The boot step never replaces anything it does not own: it says so in one line and leaves it.

On macos-user the empty tree is staged by `macosuser.StageEmptyCtxCommands` with the same
rm-then-mv shape as its siblings, and the variable rides both the bootstrap env and the agent's
session env file, since the launch argv's `env -i` list is closed; so every macos-user launch
writes a session env file. `PlanInvariants` checks the context dir is staged, root-owned and in
both envs ([CX-D14](#CX-D14)).

### A running jail takes `mounts` changes

On podman a launch mounts `/ctx` itself as a tmpfs the jail sees read-only, freezes the
`live-ctx-mounts` contract tag, and keeps a host-side record of the config mounts it bound
(`paths.AgentsDir()/<cname>/ctx-mounts/record.json`, keyed to the jail by a token in its
`YOLO_CTX_MOUNTS_TOKEN`). `yolo config apply`, and an attach a human is at, then add, remove and
re-point `mounts` elements under `/ctx` in the running jail, read-only and read-write alike, after
the launch's own approval gate; an element with `at` outside `/ctx` waits for a restart. The
injector is `yolo internal ctx-attach`, run under `podman unshare` on a rootless host and directly
otherwise. It clones the source mount, marks it read-only before the jail can see it, enters the
jail's mount namespace, and makes the mountpoint through a private writable clone of `/ctx`. The
jail's own `/ctx` stays read-only. Apple Container, a macOS Podman Machine and a jail launched
before this report the change as waiting for the next restart; macos-user takes it at the next
session. Design and rulings: [`live-config-apply.md`](../design/live-config-apply.md).

## Delivery on macos-user

macos-user has no mounts. A context mount there is delivered by a **root-owned link** in the
context dir plus **generated Seatbelt rules** on the resolved source, gated by a **DAC
preflight**, wherever the siting rules admit the source; every other declared context mount
refuses the launch, naming its reason (`run.planMacosUserCtxMounts`, first on the macos-user
arm, before the config-change prompt and any host service, `--dry-run` included).

### DP-D15, narrowed

<a id="CX-D9"></a><a id="CX-D10"></a><a id="CX-D20"></a>

[DP-D15](../design/declaration-parity.md#7-ruled-divergent-and-the-ones-i-would-re-open) ruled
an undeliverable macos-user context mount a **fatal refusal** rather than a silent absence,
because a copy was then the only mechanism and "some of these directories are huge". A source the
sandbox account can reach needs no copy, so the ruling is narrowed, not overturned
([`OQ-CX5`](#oq-cx5)): **deliver** by link plus Seatbelt where the DAC preflight and the siting
rules pass, and **refuse fatally** everywhere else, keyed on the declaration being present.

- A declared entry whose source does **not exist** is skipped with the container backends' own
  warning and does not refuse: it would be absent on every backend, so it is not this backend's
  deficiency, and the refusal stays keyed on a deliverable declaration ([CX-D9](#CX-D9)).
- The launch banner **discloses a pack `mount` here** as it does on a container, because the read
  happens ([CX-D20](#CX-D20)).
- The host nvim config is **not** refused although DP-D15's row names it: nothing declares it,
  so refusing it would refuse every launch on a Mac that has one ([CX-D10](#CX-D10)).

### Where the bytes are named

<a id="CX-D21"></a>

The links live in **`macosuser.StagedCtxRoot`** (`/var/yolo-jail/ctx/<cname>`), root-owned and
replaced wholesale each launch, at exactly the relative paths the container `/ctx` would use. So
the tree **is** `/ctx` on this backend, and a pack surface naming `/ctx/<into>` resolves through
`remapCtx` unchanged. It shares one namespace with the composed copies (`host-user/…`), as `/ctx`
does on podman.

> [!WARNING]
> **Not `/ctx`, not the sandbox home, not the workspace sidecar.** A new top-level directory on
> macOS needs `/etc/synthetic.conf` and a reboot. The sandbox home is one link set per machine
> and agent-writable, and so is the sidecar, so the agent could re-point a name in either. A
> `mounts` entry whose `at` lies **outside `/ctx`** has no place in the tree and is refused,
> naming the entry, never remapped to a basename the user did not choose.

Links do not set `YOLO_CTX_ROOT` or change the host-layer report: they are what the agent opens,
not inputs the entrypoint composes from. They ride the composed tree's `.new`-then-swap
(`macosuser.StageContextDirCommands`), made after its recursive `chmod`, so no `chmod -R` ever
meets a link into the user's files ([CX-D21](#CX-D21)).

### The link is only a name

Every link is created by the root-owned staging step with an **absolute target equal to the
resolved source**, and the agent cannot re-point it, because the link and its parent are
root-owned. That keeps the *name* honest; **it is not security.** The agent can make its own link
anywhere in the writable set, pointing anywhere, and gains nothing, because Seatbelt evaluates
the **target** of every access, absolute or relative (measured 2026-09-13, macOS 26.5 arm64,
[`declaration-parity.md` §6.1](../design/declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured)).
A relative escape (`$YOLO_CONTEXT_DIR/src/../../..`) is judged where it lands. A link inside a
**rw** source that points outside it grants the jail nothing; it is still a hazard for a *host*
program that follows it later, which the launch disclosure's injection sentence names.

### The Seatbelt rules

<a id="CX-D18"></a>

The rules are generated from **resolved** paths only, since a rule on an unresolved path matches
nothing (`/tmp` against `/private/tmp`), and each carries a `#seatbelt-test-id:<name>#`. Ordering
is last-match-wins among the rules matching one operation, so the context rules sit by operation
class (`macosuser.SeatbeltProfileWithContext`, [CX-D18](#CX-D18)):

1. The base: `(allow default)`, the `file-write*` root deny plus the writable set, and the
   `/Volumes` and `/Users` read-denies with their re-allows.
2. **The rw sources' write allow**, right after the writable-set allow and before the
   `workspace_readonly` and staged-content denies.
3. **The read allow for every source**, after the `/Users` deny and before both keychain denies,
   so a source holding a keychain still cannot read it. For a source under `/Users/Shared/`,
   `ancestorLiterals` also opens its ancestors, the traversal fix the workspace needed, with
   siblings still denied.
4. **The ro sources' write deny**, last in the write block.

With nothing linked the profile is byte-identical to the base. The ro write deny cannot be proved
at runtime, because a source the siting admits is never in the writable set and the root deny
refuses the write first; the test-id registry records that, and the EPERM probe is registered
against `write-outside-deny`, the rule that decides it.

> [!WARNING]
> **A read-only source inside the writable set is refused** (inside the workspace, an rw source,
> or the machine temp directories). With no mount namespace, one path carries one mode: emitting
> the deny would silently make part of the workspace read-only, and omitting it would silently
> make the "read-only" mount writable. The container backends can give the same bytes two modes
> at two paths; this backend cannot.

### The DAC preflight and siting

<a id="CX-D17"></a><a id="CX-D19"></a><a id="CX-D23"></a>

Seatbelt only narrows; the sandbox account still needs POSIX and ACL access, and it is a foreign
uid. So:

- **The DAC preflight** (`macosuser.ContextPreflight`) runs
  `sudo --user=_yolojail /bin/test -r <source>`, plus `-x` for a directory and `-w` for an rw
  source, in `RunMacosUser` after the launch preconditions and before the nix build
  ([CX-D19](#CX-D19)). The kernel is asked as the sandbox account rather than computing an
  answer from mode bits, because ACLs decide too. Every source is asked, a source's later probes
  are skipped once one fails, and one refusal names every source to move, the account, and the
  remedy (move it under the shared root, or use a container backend). The plan carries the same
  probes, and `PlanInvariants` refuses a link without them.
- **A read-only source** may sit anywhere outside a real `/Users/<name>` home: under
  `/Users/Shared`, or outside `/Users` (`/opt`, `/usr/local`, `/Library/…`).
- **A read-write source** must sit **under the shared root**. Only there does a file the sandbox
  creates inherit an ACE that lets the host user modify it later; elsewhere a `_yolojail`-owned
  file in the user's tree is readable but not editable by the user, a silent ownership trap.
- **A source inside a real home is refused** ([`OQ-CX7`](#oq-cx7), ruled as [CX-D23](#CX-D23)):
  a source to share goes in a shared location. Even where DAC would pass, git-style upward stats
  need ancestor grants inside the home, which `ancestorLiterals` refuses on purpose. An
  [optional](#optional-mounts) element there is skipped instead.

The siting rules are `macosuser.SiteContextLinks`, lexical over the source as the host resolved
it, with every macOS fact a field of `macosuser.ContextSiting` so a Linux test can state each
one ([CX-D17](#CX-D17)). Beyond the rules above they refuse: a source containing the users root,
whose read allow would reopen every home; a source spelled through a firmlink, a case variant of
`/Users`, or the Data volume's own mount point (`/System/Volumes/Data`); a source of either mode
at, inside or containing yolo's state dir; an rw source inside an ro one; and two context mounts
whose jail paths coincide or nest, or one at, inside or around a reserved name or a selected
pack's declared `reads-host` destination, since a link cannot hold another link. Every overlap
compares as the volume does (`ContextSiting.FoldCase`, folded on a default install); only the two
admissions, the shared root and the boot volume, compare byte for byte.

The preflight checks the **root** of a source, not its subtree: a `0700` subdirectory fails at
use time with `EACCES` where rootless-podman root would have read it. The briefing says so.

> [!WARNING]
> **Do not reach into a user's home with an ACL grant or a clone.** A `user:_yolojail` ACE plus
> `search` on each ancestor was the obvious way to deliver a home source, and the maintainer
> rejected it ([CX-D23](#CX-D23)): it needs a new read-only ACE, a recursive walk (inheritance
> applies at create time only), a recorded ledger to undo it (`WorkspaceACLStripScript` strips
> every ACE, the user's own included), dies silently when the account is recreated (an ACE names a
> UUID), and `search` on `/Users/<user>` removes the DAC wall that backs up the profile's `/Users`
> deny. A per-launch APFS clone of the folder was rejected as slow. The cost is accepted: most
> context sources live in a home, so macos-user refuses the common case and the refusal names the
> remedy.

### Volumes and privacy-protected folders

<a id="CX-D5"></a>

A source on a volume under `/Volumes` other than the boot volume, or in a privacy-protected
folder (Desktop, Documents, Downloads, iCloud Drive), **refuses fatally, naming the reason**
([CX-D5](#CX-D5), answering [`OQ-CX8`](#oq-cx8)): an unmeasured delivery could fail only when
the agent opens the path, the "surprisingly not there" DP-D15 rules out. The privacy-protected
folders sit inside a real home, which the siting refuses anyway; MEASURED on the
2026-10-03 run, Desktop, Documents and Downloads were unreadable to `_yolojail` at the DAC layer.
Whether volume ownership flags and the system's removable- and network-volume protections let a
`sudo -u _yolojail` process through is unmeasured.

`cache_relocations` alone is narrowed: a relocation **target** on a volume under `/Volumes` is
admitted, because a relocation exists to put a cache on another disk, and a launch-time write
under the session profile refuses a volume the sandbox cannot write before the agent starts
([CR-D2](../plans/cache-relocation.md#CR-D2)). Context mounts keep this refusal.

### Copies: a pack's single-file mount, and a directory host_files entry

<a id="CX-D25"></a><a id="CX-D26"></a>

On macos-user a selected pack's `mount` whose source is a **single file is always copied**, never
linked ([CX-D25](#CX-D25)): the host CLI copies `~/<from>` into the composed context tree at the
grant's own `/ctx` path (`packload.MountCtxPath`), recorded in `HostContext.Copied`, apart from
the host-layer report's `Delivered`. Every pack grant names a path in the user's home, which the
siting refuses for a link, so a link would reach no real file grant, and one file has none of
DP-D15's size problem. Two mechanisms for one declaration are not offered: a link first and a copy
only where the siting refuses would answer differently for one file moved between two folders. A
**directory** grant keeps the link and its refusal.

A copy is sited by its destination alone (`macosuser.SiteContextCopies`): under `/ctx`, and not
at, inside or around a reserved name, a selected pack's `reads-host` destination, another copy
or a link; `PlanInvariants` re-sites the links against `Copied`. A file that exists and cannot be
read ends the launch, naming the privacy-controls grant, the pack and a container runtime as next
steps; an absent one is skipped with the container backends' line. The briefing marks the entry
"copied at launch; host edits arrive at the next launch", the one stated delta against a
container's live bind.

The same date's other half: a **directory `host_files` entry**, which is not a context mount,
is copied into the same tree, confined to its source (a link is followed only where it resolves
inside it, to a file or a folder; one that leads back into a folder the walk is already in is
skipped) and capped per entry and per launch; over a cap, or on a failed copy, the launch ends
naming the entry. DP-D15's size reason never applied, since a container's directory `host_files`
is a full copy at boot too (`entrypoint.stageHostFile`).

**The staged tree is the sandbox account's to read and no other account's**
([CX-D26](#CX-D26)). `macosuser.StageCtxCommands` makes the contents `a+rX`, which the sandbox
account's reads need, and closes the tree's **root**: `0700` plus a
`user:_yolojail allow list,search` ACE. It was world-readable before, and since the copies it can
hold whole user folders, routinely `~/.aws` or `~/.ssh`. Because the staged files must stay
readable, a directory `host_files` entry's own modes are recorded beside the copy
(`entrypoint.HostFileDirModes`, under `host-user/_modes/` in the tree) and the bootstrap creates
each file in the home with them, so a `0600` key arrives `0600`; a file the record does not name
is created owner-only. The host-side copy never gives another account a bit its source lacked
(`run.ctxCopyMode`). One consequence on the host: `yolo stores` can no longer measure inside a
workspace's staged tree as the user, and says so on that row.

> [!WARNING]
> **UNMEASURED on a Mac: whether that ACE lets `_yolojail` through the closed root.** Both rights
> are spelled as in the shared root's provisioning ACE (`macosuser.dirRights`), but no Mac has
> run a launch with the root closed. If the ACE does not admit the sandbox account, nothing in the
> tree arrives on any launch that stages one, which is every launch carrying host bytes at all,
> and a delivered `reads-host` grant's fail-closed read ends the launch. The `macos-user.yml`
> dispatch settles it (`TestMacosUserDeliversHostBytesByCopy`,
> `TestMacosUserGlobalGitignoreAppliesInTheSandbox`), as does one human launch on a Mac that
> delivers host bytes.

### What cannot be matched

- **Path identity.** There is no `/ctx`: `pwd -P`, `realpath` and `git rev-parse --show-toplevel`
  print the host path, and tools that record paths record host paths.
- **Two modes for one set of bytes**, and **destinations outside `/ctx`**: refused.
- **Subtree readability**: stated in the briefing, not hidden.
- **Liveness is better than a copy.** The linked bytes are live, so a growing directory never
  goes stale. Only the single-file copies are snapshots.
- **Concurrency of the name.** The staged tree is per workspace and replaced each launch, so a
  second concurrent launch of the same workspace with different `mounts` re-points the first
  jail's names. Access is unaffected, because each process keeps its own profile.
- **Hard links.** A hard link made in a writable directory to a file the profile denies does
  **not** carry that file into a writable source (MEASURED, the 2026-10-03 run).

## What the briefing says

<a id="CX-D13"></a><a id="CX-D22"></a>

The "Additional Context Mounts" section names `$YOLO_CONTEXT_DIR` and its value on one line
inside the section, so a jail with nothing bound gains no line, and labels each entry with its
mode and resolved host path; a pack grant says which pack, and a read-write entry adds one
sentence that writes there reach the host ([CX-D13](#CX-D13)). Pack mounts are listed beside
config mounts. The Limitations line reads "context mounts under `$YOLO_CONTEXT_DIR` are
read-only unless marked read-write". Entries are `jailcontent.ContextMount` structs, since a
`host:container` string could carry no mode and its reader split on the first colon. The Apple
Container `:ro` floor is decided per entry in the two deciders the argv also uses
(`run.configCtxMounts`, `run.packCtxMounts`), so an rw entry stays listed on the backend that
binds it.

On macos-user the briefing lists each entry at its path under the context dir and adds one
sentence for the two deltas that change agent behavior: tools that resolve paths print the host
path, and a subfolder the sandbox account may not read stays unreadable. A copied file entry is
marked as copied and left out of that sentence. That backend's rw launch line prints where the
links are handed over (`run.noteMacosUserRWMounts`), naming the context-dir path, with no
ownership sentence, since there is no podman ([CX-D22](#CX-D22)). A backend briefs only what its
own decider delivered (`run.briefedCtxMounts`), so a refused mount is never listed.

## Tests and Mac probes

Each test named here fails if the production call site is deleted, not merely if the callee
changes.

| Piece | Pinned by |
| :--- | :--- |
| The rw form, the trust predicate, the refusal set, the disclosure, the briefing labels (podman, Apple Container) | `internal/config/mounts_test.go`, `internal/cli/run/ctxmountsrw_test.go`; end to end with `YOLO_CONTEXT_DIR` by `integration/contextmounts_test.go` |
| `YOLO_CONTEXT_DIR` on every backend | the podman golden argv, `internal/cli/run/contextdir_test.go`, `internal/macosuser/contextdir_test.go` |
| The macos-user refusal and delivery from `Run` | `internal/cli/run/macosctxtree_test.go`, `briefingapplied_test.go` |
| The plan's links, profile order and invariants | `internal/macosuser/ctxlinks_test.go` (the staging commands run for real on any OS with `/bin/ln`), `internal/macosuser/contextsiting_darwin_test.go` |
| The copies and the closed root | `internal/cli/run/macosctxcopies_test.go`, `macosctxdirentry_test.go`, `internal/macosuser/ctxtree_test.go`; the dry run names each copy (`macosuser.PrintPlan`) |

**The Mac probes** extend `integration/macosuserseatbelt_test.go`'s test-id registry (the
`context_` cases, run as the runner's own user under the generated profile, each beside a bare
control) and `integration/macosusercontextmounts_test.go` (the DAC preflight, a real launch
reading and writing through `$YOLO_CONTEXT_DIR`, the link replacement, and the `/Volumes` and
privacy-folder measurements, behind the launch gate). Together they show a ro source readable
and unwritable (`EPERM`, not `EACCES`, which proves the profile decided), a rw source writable, a
sibling denied, an agent-made link and a relative `..` walk refused, the root-owned link not
replaceable, the preflight refusing a `0700` source, git working inside a shared-root source,
and the hard-link answer (`TestMacosUserSeatbeltContextHardLinkMeasurement`, which logs and
asserts nothing). The copy probes are `TestMacosUserCopiesAPackFileMountIntoTheContextDir` and
`TestMacosUserDeliversHostBytesByCopy`.

The rootless-ownership cells of [Ownership](#ownership) cannot be verified in a nested jail,
which is rootful ([AGENTS.md's carve-out](../../AGENTS.md#testing)); they need a real rootless
host or CI.

## Unbuilt and unmeasured

- **A pack rw `mount`** is not built, and waits for a pack that needs one ([CX-D3](#CX-D3)).
- **Where an rw element may be declared** waits on
  [`OQ-WT1`](../design/workspace-config-trust.md#OQ-WT1); until then, user scope only.
- **MEASURED on a Mac 2026-10-18, two volume kinds of three** ([`OQ-CX8`](#oq-cx8)'s other half;
  on a developer Mac (macOS 26.5, arm64); `macos-user.yml` has not confirmed it, its runner
  having no second volume —
  [the run's record](../design/macos-user-suite-on-a-developer-mac.md#7-measurements-this-run-produced)).
  On an APFS volume with ownership *disabled* and on an ExFAT volume, a `cache_relocations`
  target under `/Volumes` was **DELIVERED**: the sandbox wrote through the link and the file was
  at the target. **The grant is per-target, not per-volume** — a sibling directory on the same
  volume was refused under the profile on both, including on ExFAT, where the target itself
  *"was NOT granted (the volume took no access entries)"* and delivery still worked. ⚠ Whether
  this clears the [CX-D5](#CX-D5) refusal is the maintainer's call, and an ownership-*enabled*
  APFS volume remains unmeasured: enabling it needs root `sudo`, which that host does not grant
  without a password.
- **UNMEASURED on a Mac:** the single-file and directory copies
  ([CX-D25](#CX-D25)); whether the closed root's ACE admits the sandbox account
  ([CX-D26](#CX-D26)); and whether macOS's `/bin/test` answers through `access(2)` with the ACLs
  weighed ([CX-D19](#CX-D19)).
- **UNMEASURED anywhere:** rw ownership on a podman macOS machine and on Apple Container
  (virtiofs).
- **Live risks:** a user mounts rw a directory a host tool trusts (a dotfiles repo, a CI config
  dir), which the injection sentence and the trust predicate are the only defenses against; and a
  future edit orders a new Seatbelt allow after a deny that must win, which the ordering test and
  the test-id registry catch.

## What this does not do

- **No copy of a directory a context mount names.** DP-D15's size argument stands for config
  `mounts` and a pack's directory `mount`; only the single-file and `host_files` copies above
  exist.
- **No locking** across jails, **no `chown`, relabel or ACL change** on a container backend, and
  **no rw from packs**.
- **No `/ctx` on macOS** and no `synthetic.conf`.
- **No change to what string `mounts` entries do** on the container backends.

## Current values

Verified at `8e65f3444`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Context dir variable | `YOLO_CONTEXT_DIR` | `paths.ContextDirEnv` |
| Context dir, container backends | `/ctx` | `internal/cli/run/assemble.go` |
| Context dir, macos-user | `/var/yolo-jail/ctx/<cname>` | `macosuser.StagedCtxRoot` |
| yolo's reserved children of the context dir | `/ctx/packs`, `/ctx/captures`, `/ctx/host-user`, `/ctx/host-nvim-config` | `paths.ReservedContextPaths` |
| Apple Container `:ro` bind floor | `container` 1.1.0 | `run.acROBindsFloor` |
| macos-user shared root | `/Users/Shared/yolo` | `macosuser.SharedRootDefault` |
| Staged-tree root mode and ACE | `0700`, `user:_yolojail allow list,search` | `macosuser.StageCtxCommands` |
| Directory `host_files` copy caps | 32 MiB and 4096 files and folders per entry; 64 MiB and 8192 per launch | `internal/cli/run/macosctxtree.go` |

## Why it's this way

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| <a id="oq-cx-symlink"></a>[`Mount sources and links`, 2026-10-29](#oq-cx-symlink) | Every mount a workspace config declares, read-only or writable: (1) a source whose written path is inside the workspace, or inside any other mount (no mounts in mounts), is refused; (2) the config-change approval shows and records the source's resolved destination beside the written path, and the launch refuses a source that now resolves elsewhere. Built: [Mount sources and links](#mount-sources-and-links) | The maintainer: *"let's just include the destination in the approval prompt and recording. this combined with no symlinks in agent writeable locations (this means no mounts in mounts I guess) makes it safe."* A link in a place the jail can write is the agent's, so following it lets the agent choose what is mounted; a link elsewhere is safe once the human approves where it lands. |
| <a id="oq-cx3"></a>[`OQ-CX3`](#oq-cx3) | The read-only form keeps its ordinary rules and takes no share of the rw refusal predicate, answered by [OQ-PR3](podman-readiness.md#why-its-this-way) | The maintainer ruled that a jail mounting the logs dir may read it: *"It's just like anything else on the host, it's outside of their view."* The shared predicate would refuse that mount. Whether a workspace config may declare any host mount is a scope question, [OQ-AS3](../research/agent-safehouse.md#OQ-AS3), whose presentation should name a user-scope ro mount of `$HOME`. |
| <a id="oq-cx5"></a>[`OQ-CX5`](#oq-cx5) | DP-D15 is narrowed: deliver by link plus Seatbelt where the preflight and siting pass, refuse fatally elsewhere | DP-D15 is conditional in the maintainer's words (*"if we can't do it, we can't do it, we'll just make it a fatal error"*), and [OQ-DP4](../design/declaration-parity.md#decision-ledger) asked for delivery *"however we can make it work"*. DP-D15 said "can't" because a copy was the only mechanism; for a reachable source it is not. |
| <a id="oq-cx7"></a>[`OQ-CX7`](#oq-cx7) | A context source inside a real home stays refused on macos-user; no ACL grant, no per-launch clone ([CX-D23](#CX-D23)) | The maintainer: *"I don't want to start setting ACLs or whatever on files and home directories … If you want to share something, you must put it in a shared location, and I think that's going to solidify mental models … And I also don't want this clone. That could be very slow."* |
| <a id="oq-cx7-amended"></a>[`OQ-CX7`, amended 2026-10-16](#oq-cx7-amended) | A `mounts` object element may be `"optional": true`: a backend that cannot deliver it skips it with one line instead of refusing ([Optional mounts](#optional-mounts)). A required element, and every safety refusal, is unchanged | The maintainer: *"Yes, I like the optional mount choice. Let's add that."* The home-source refusal stands; optional is how one config serves macos-user and a container backend at once. |
| <a id="oq-cx8"></a>[`OQ-CX8`](#oq-cx8) | A `/Volumes` source or a privacy-protected folder refuses fatally until measured ([CX-D5](#CX-D5)) | An unmeasured delivery fails only when the agent opens the path. Revisit with the `/Volumes` measurement; the privacy-folder half was measured unreadable. |
