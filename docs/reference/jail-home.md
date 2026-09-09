---
status: current
verified: 2026-09-09
verified_commit: d8cf1cf8
covers:
  - internal/cli/run/assemble.go
  - internal/cli/run/assemble_parts.go
  - internal/cli/run/prepare.go
  - internal/cli/run/mounts.go
  - internal/cli/run/runmount.go
  - internal/cli/run/hostfiles.go
  - internal/cli/run/jailprefix.go
  - internal/cli/run/storagehelpers.go
  - internal/storage/
  - internal/config/writablehome.go
  - internal/entrypoint/boot.go
  - internal/entrypoint/env.go
  - internal/entrypoint/fsx.go
  - internal/entrypoint/shims.go
  - internal/entrypoint/scripts.go
  - internal/entrypoint/packhooks.go
tags: [home, mounts, overlays, storage, entrypoint, path]
summary: "How /home/agent is composed: a machine-wide read-only base, per-workspace writable overlays punched through it, staged :ro content on top, and files the entrypoint regenerates into the overlays on every boot. Covers the mount stack, the write rules that keep bind mounts alive, PATH, what is shared at which scope, and how the three backends differ."
---

# The jail home — how `/home/agent` is composed

**Status:** CURRENT as of 2026-09-09, verified against `d8cf1cf8`.

`/home/agent` is not a directory that exists anywhere as a whole. It is composed at
container create out of four ingredients: a **machine-wide read-only base**, **per-workspace
writable overlays** bind-mounted *over* specific paths inside it, **staged read-only
content** (skills, briefings, composed files) layered on top of those, and **files the
entrypoint regenerates into the overlays at every boot**. Nothing persists through the
container itself — it runs `--rm` on a `--read-only` rootfs, so every durable byte is on a
host bind mount.

One question decides where anything goes: **is this state one truth per machine, one per
workspace, or one per boot?**

| Component | Lives in |
| :--- | :--- |
| The machine-wide base, its mountpoints and symlink hatches, layout migration | `internal/storage` (`EnsureGlobalStorage`, `EnsureSymlink`, `MigrateStorageLayout`, `StorageLayoutVersion`) |
| Host storage paths | `internal/paths` (`GlobalHome`, `GlobalCache`, `GlobalMise`, `AgentsDir`, `WorkspaceHomeState`) |
| Per-workspace overlay creation, seeding, layout migrations | `internal/cli/run` (`prepareWsState`, `seedAgentDir`, `migrateOldOverlay`) |
| The mount table, per backend | `internal/cli/run` (`podmanBaseMounts`, `appleContainerBaseMounts`, `ScratchMountArgs`, `ROFileMountArg`, `jailPrefixMountArgs`) |
| Config-declared extra mounts | `internal/cli/run` (`sortedWritableHomeDirs`, `sortedCacheRelocations`, `hostUserFileArgs`, `hostFileWritableDirArgs`, `hostMountArgs`), `internal/config` (`WritableHomeDirs`, `WritableHomeBackingSubdir`) |
| Boot-time generation, and its failure policy | `internal/entrypoint` (`Main`, `genStep`, `genFailuresError`) |
| The write rules that keep bind mounts alive | `internal/entrypoint` (`WriteInPlace`, `ClearContents`, `EnsureRelativeSymlink`, `resetAnchorDir`) |
| PATH | `internal/entrypoint` (`BootPath`, `Env.BlockDir`, `Env.LaunchDir`), `internal/macosuser` (`SandboxPath`) |
| Shared-credential and per-jail-history hooks | `internal/entrypoint` (`Env.linkSharedCredential`, `Env.linkThroughShared`, `Env.isolateHistoryFile`) |
| The claude.json seed sync | `internal/storage` (`SyncClaudeJSONSeed`) |

**Reads with:** [`storage-and-config.md`](storage-and-config.md) (which host directory has
which lifetime, and the config scopes), [`pack-system.md`](pack-system.md) (a pack's
`state`, `skills`, `briefing` and `files` contributions — the declarations that decide most
of this table's contents),
[`image-staging-vs-baking.md`](image-staging-vs-baking.md#the-mounted-prefix) (the
`/opt/yolo-jail` prefix mounts),
[`agent-briefings.md`](agent-briefings.md) (what the staged briefing files contain).
`yolo config-ref` is the authority for config keys.

---

## Invariants

These are the rules a maintainer breaks by accident, and every one of them has cost a
regression.

**No rename-writes to mount-visible files.** A file→file bind mount pins the inode captured
at container start. A tmp-file-plus-rename swaps in a *new* inode the mount cannot see, so
every running jail silently stops seeing refreshes. `WriteInPlace` truncates and rewrites
the same inode instead, and `internal/entrypoint/fsx.go`'s header codifies the ban for the
whole package. There is **no exception** for a mount-visible file. `os.Rename` legitimately
survives on paths nothing binds — image autoload, prune, boot-log rotation — so state the
rule as "no rename-writes to mount-visible files" rather than as a count of `os.Rename`
call sites, which is a grep-checkable claim that fails for a reason that does not matter.

**Never remove a mount-anchor directory.** Removing the directory detaches the mount.
`ClearContents` empties a directory in place; `resetAnchorDir` is the generator-facing
version for the two generated-script dirs, whose contents are wiped every boot while the
directories themselves survive. Unlinking such a directory under the `:ro` home either
fails `EROFS` or, worse, succeeds and detaches a live mount.

**Symlinks are relative, and compared as raw link strings.** Resolution has to happen
through the *container's* mount table, never the host's, so nothing in the boot path
resolves a link before comparing it (`EnsureRelativeSymlink`). The one absolute link is
the per-jail history file, which is created and resolved entirely inside the jail.

**Every mount source is created host-side before create.** podman fails the *whole
container* on a missing bind source with a bare `statfs …: no such file or directory`,
which reads as a yolo bug. Single-file mountpoints are touched, directories are
`MkdirAll`ed, and for anything nested inside the `:ro` base the *mountpoint* is created
too.

**The read-only base is never written from inside the jail.** Every boot-time write lands
in a writable overlay. Anything that does end up genuinely shared — the base's symlink
targets, `~/.cache`, `/mise`, a machine-scope credential dir — must be append-only,
convergent, or single-writer, because two jails regenerate their configs concurrently and
would otherwise fight.

**A failed generator refuses the boot.** `genStep` collects failures rather than aborting
at the first one, so a single boot reports every problem; `genFailuresError` then turns a
non-empty set into a refusal *before* the agent is exec'd. This is the A12 ruling. A
generator must return `nil` when its input is legitimately **absent** — that is not a
failure, and routing optional inputs through `genStep` turns "nothing to do" into a jail
that will not start.

## The mount stack

The image contributes only a **mountpoint**: `/home/agent` is baked as an empty directory
under fakeroot so it exists as a mount target on the read-only rootfs. The image's
`/etc/passwd` has exactly one user, whose home is `/home/agent`, and the image `Env` does
not set `HOME` — that arrives at run time with `-e`. At runtime the baked directory is
fully covered by mounts and its image content is irrelevant.

On the podman backend, in tiers:

```
/home/agent
│
│  image layer: empty dir, mountpoint only
│
├─[1] BASE  :ro   <global storage>/home                    shared by ALL jails
│        the union of every SHIPPED pack's declared writable and shared dirs,
│        plus the single-file mountpoints, plus three relative symlinks that
│        point INTO writable overlays:
│          .bashrc      -> .config/bashrc
│          .claude.json -> .claude/claude.json
│          .gitconfig   -> .config/git/config
│
├─[2] rw   per-workspace overlays from <workspace>/.yolo/home
│        .npm-global/ .local/ go/ .config/ .ssh/ .yolo/bin/
│        + one bind per pack-declared workspace-scope state dir (.claude/, …)
│        + the single-file binds (.bash_history and the .yolo-* files)
│
├─[3] rw   machine-shared dirs
│        .cache <- <global storage>/cache            (all workspaces, one truth)
│        + one bind per pack-declared machine-scope state dir
│
├─[4] :ro  staged content, layered over the overlays
│        briefings, skills trees, pack `files` trees, granted host files,
│        the user config for nested jails
│
└─ siblings outside /home/agent
     /workspace       the workspace, rw
     /mise            the mise store (a named volume on macOS)
     /opt/yolo-jail   TWO :ro mounts: the linux binaries and the flake bundle
     /ctx/*           read-only context: host files, config `mounts`, packs
     /run/yolo-services   host-service sockets
```

Tier order is what makes the design work: the base is read-only and machine-wide, and every
writable path is a **punch** through it at a known destination. Adding a writable path
therefore means adding a mount *and* a mountpoint inside the base — see
[Extra mounts a config declares](#extra-mounts-a-config-declares).

**One anchor serves both generated-script dirs.** `~/.yolo/bin` is a single writable bind,
and the entrypoint writes `block/` and `launch/` inside it. They stay separate
*directories* because their positions on PATH differ and must, but gathering them in the
filesystem is not gathering them on PATH — and nothing may ever put the shared parent on
PATH, or a lazy installer would become reachable from a blocker's position.

**Assembly is a pure function of its input.** `assembleRunCmd` takes a resolved input
struct and returns argv; every emitter that iterates a set sorts it, so the argv is
deterministic and golden-testable. Adjacency in the argv is cosmetic — podman sorts mounts
by destination depth, so a nested bind works wherever it is emitted.

### Extra mounts a config declares

Four config keys add home mounts, and they differ in what they let a *workspace-scope*
config reach — which is the whole of their scope story.

- **`writable_home_dirs`** — extra `$HOME` subpaths made read-write, for an agent extension
  that hardcodes a home path yolo does not manage. Each entry is backed by a directory
  under the workspace's own state dir and bound over `/home/agent/<path>`. Guarded by
  `checkWritableHomeDir`, which rejects absolute paths, `..` escapes, a `:` (a podman
  mount-option footgun) and any first segment yolo already manages. **Safe at any scope**:
  the destination is confined under `/home/agent` and the backing store is the workspace's
  own, so a jail editing its workspace config gains nothing it could not get by writing to
  `/workspace`.
- **`cache_relocations`** — a rw bind nested *inside* the `.cache` mount, so
  `~/.cache/<subdir>` is an ordinary writable directory backed by other storage.
  **User-scope only**, because it mounts an arbitrary *host* path read-write, which is an
  escalation primitive if a jail-editable config could set it. Both ends are provisioned:
  podman would create the mountpoint itself, but root-owned with the wrong mode, and a
  missing *target* kills the container outright. Only the target's last path component is
  created — `MkdirAll` over the whole path would turn a typo into a silently-wrong empty
  directory back on the root filesystem, the exact failure relocation exists to prevent.
- **The host-CAS alias** — a rw bind nested *inside* the `.cache` mount, exactly like a
  relocation, and the only one of these that **no config key selects**: yolo picks both ends
  from a table of recognised *content-addressed* host caches (`internal/hostcas`, `pants/lmdb_store`
  today) so a jail shares the host user's own store instead of pooling a second copy of it
  ([`../design/disk-levers-and-backfill.md`](../design/disk-levers-and-backfill.md) L9 /
  [OQ-BF10](../design/disk-levers-and-backfill.md#111-decision-ledger)).
  The source is the launching user's `$XDG_CACHE_HOME`/`$HOME/.cache`; the destination is the path
  the jail's own copy of the tool already uses, so **the mount lands on top of the jail's private
  copy**, which is what strands rather than moves it. It is **writable**, which is a strictly
  bigger trust step than the `:ro` nix-store bind, and the CAS property is the whole of what makes
  that acceptable: a jail can add a blob under a wrong name only for the reading tool to reject it,
  so the worst case is wasted space. A **path-keyed** store is categorically excluded for the
  opposite reason — the jail would choose both the key and the bytes, and the host tool would read
  them *because of where they sit*. Gated on podman, a non-macOS host, matching host/jail platform,
  and a host store that exists, is a directory, is writable and is not empty-over-a-warm-copy; every
  failure degrades to the jail's own copy and none can refuse a launch. Disclosed on stderr at every
  launch that makes one, and explained per store by `yolo stores`.
- **`host_files`** — any file the user wants in the jail, composed by the same engine that
  composes agent settings. Three mount-relevant halves, all in
  `internal/cli/run/hostfiles.go`: a source-bearing entry's host path is bound `:ro` under
  `/ctx/host-user/<slug>`; the resolved entry list rides one env var so the entrypoint never
  re-reads config and derives the same slug by construction; and the *destination* needs a
  writable path, which `HostFileEntry.StagingFor` sorts into three cases (below). Scope is
  **per entry**: a `source`-bearing entry is user-config-only, a source-less one is legal at
  any scope.
- **`mounts`** — each `host[:container]` pair bound `:ro`, defaulting to `/ctx/<basename>`.
  Nothing to do with the home.

Where a `host_files` destination lands decides whether the composed write can succeed at
all:

| Destination | Staging | Mechanism |
| :--- | :--- | :--- |
| inside an existing rw overlay (`.config/`, `.cache/`, `.local/`, `go/`, `.npm-global/`, a selected pack's state dir) | none | already writable; staging would shadow a yolo mount |
| a home-root file | symlink | a **relative, dangling** symlink in the base pointing into the rw `.config` overlay — the same hatch `.bashrc` and `.claude.json` use |
| a new top-level dir | writable subtree | the `writable_home_dirs` recipe: backing dir, base mountpoint, nested rw bind |

> [!WARNING]
> **The dangling symlink is load-bearing and not interchangeable.** A directory bind makes
> the destination a *directory*, so the composed write fails "is a directory". A
> pre-created **empty** backing file is worse: `os.Stat` on a bind-mounted empty file
> *succeeds*, so a `mode: once` surface's seed-if-absent guard returns early on the first
> boot and the file stays empty forever. Dangling is what keeps `once` correct.

### Staged read-only content

Four kinds of content are composed **host-side** into a per-jail staging directory under
`<global storage>/agents/<container name>/` and then bound `:ro` at a home-relative
destination the *pack declares*. Core reads the destination off the declaration; there is no
per-agent table anywhere.

- **Skills** — the source is the per-pack staging dir rather than the pack's own tree,
  because `jailcontent.PrepareSkills` merges three sources into it (built-ins < every pack's
  skills < the user's own host skills) and that merge has to land somewhere. Rebuilt on
  every invocation.
- **Briefings** — keyed by **destination**, not by pack, because the composed prose varies
  per destination. The destination list and the staging filename both come from
  `briefingDestinations` / `briefingStagingName`, which the write half calls too: that
  coupling is what makes the file that is composed and the file that is mounted the same
  file by construction. A mismatch is silent, because a missing bind source for a *file* is
  not an error the way a missing directory is — the jail just comes up with a blank
  briefing.
- **Pack `files` trees** — an opaque tree the pack owns, bound at its declared `into`.
- **Granted host files** — each pack's `reads-host` contribution, bound `:ro` at the `/ctx`
  path `packload.CtxPath` derives from its `into`. There is no config key and no env var
  listing them: which host files cross into the jail is a credential boundary fixed in
  yolo-shipped code. **The gate is ORIGIN, not a baked list** — `HonoredHostFiles` honors a
  grant only for an embedded or local pack and refuses a fetched one outright. The
  entrypoint re-derives the identical set in-jail from the same manifests, which is why
  `CtxPath` is deliberately the single definition both sides call.

> [!WARNING]
> **Both skills and briefings must be deduplicated by destination before the argv is
> emitted.** Several packs contributing to one path is designed behavior for both kinds —
> `skills` merges and `briefing` concatenates, and the staged file already holds all of it —
> but podman rejects a duplicate mount destination and kills the boot. Exactly one bind per
> path may be emitted, and the dedup lives beside the write half rather than in the argv
> builder.

### Everything else on the argv

The remaining mounts are grouped by what they are for, and none of them touch
`/home/agent` except where noted. The authority for the set is `assembleRunCmd`; this is
orientation, not an inventory.

- **Scratch** — the rootfs is read-only, so writable scratch is explicit: anonymous volumes
  or tmpfs for `/tmp`, `/var/tmp` and the two container dirs, plus tmpfs `/run` and
  `/dev/shm`. Selected by `ephemeral_storage`. Anonymous volumes die with the container.
- **The in-jail CLI and its flake bundle** — two `:ro` mounts making up the same install
  prefix a host install has, `bin/` beside `share/yolo-jail/`, so the in-jail CLI finds its
  repo root exactly the way a host install does with no `YOLO_REPO_ROOT` set. The image
  bakes only the mountpoints (a read-only rootfs cannot grow one) and the `/bin/<name>`
  symlinks that point into them.
- **The mise store at `/mise`** — a host bind on Linux, a named volume on macOS. Inside a
  nested jail the store is `/mise` itself, so every nesting depth shares one store. The
  host's own mise data directory is never mounted.
- **Host nix daemon socket and store** — mounted when both exist and the runtime is not
  Apple Container; macOS podman additionally requires an opt-in env var. Without it, nix
  in the jail fails with "build users group has no members".
- **Git identity and the global gitignore** — host-composed and mounted `:ro` together
  (`gitIdentityMountArgs`): a composed `[user]`/`[core]` config, plus the host's
  `core.excludesFile` when it resolves to a real file.
- **Host-service sockets** — a per-jail host directory bound at `/run/yolo-services`,
  carrying the cgroup-delegate socket and each loophole daemon's relay.
- **Pack manifests at `/ctx/packs`** — `:ro`, and that is load-bearing rather than
  tidiness: a manifest is an *input* to composition, and an agent that could rewrite one
  in-jail could grant its own pack a host file on the next boot.
- **Workspace-side shadows** — `workspace_readonly` re-mounts, per-side venv shadows backed
  by the workspace state dir, and `/dev/null` over a couple of files whose presence would
  confuse a tool.

**A nested jail cannot bind a host source file that is itself a bind mountpoint.**
`ROFileMountArg` handles this by dereferencing: it copies the source into the workspace
state dir first and binds *that*. Any new `:ro` single-*file* mount needs the same
treatment.

**The attach path adds no mounts.** It is exec-only; the mount table is frozen at container
create.

## PATH

`BootPath` is the single authority, applied once in `execBash`:

```
$HOME/.yolo/bin/block : $HOME/.yolo/bin/launch : $NPM_CONFIG_PREFIX/bin :
<mise shims> : $GOPATH/bin : $HOME/.local/bin : /run/yolo/packages/bin :
/bin : /usr/bin
```

The two generated dirs are **adjacent, at the front**, and their order relative to each
other carries the meaning:

- **Blockers first.** Interception is their entire job, so they must precede everything —
  the launchers included. A tool that is both blocked and pack-declared gets one of each,
  and the blocker wins by position.
- **Lazy installers second, ahead of every install prefix.** A launcher installs into
  `$NPM_CONFIG_PREFIX/bin` or `$HOME/.local/bin`; ordered *after* those, it is unreachable
  the moment it succeeds. The lazy install then works exactly once per home and the hourly
  update the same script carries never runs again. Evergreen agent dependencies need the
  launcher to mediate **every** invocation.

`/run/yolo/packages/bin` is the store-delivered package farm, and its position immediately
before `/bin` is chosen to change nothing: the binaries it holds are the ones the image
would otherwise bake into `/bin`, so one step ahead of `/bin` leaves every precedence
relation above it exactly as it was. It is spelled unconditionally even though most jails
never opt in, because a PATH that varies by launch is a second authority in disguise — the
directory simply does not exist on a jail that bakes, and a non-existent PATH entry costs
nothing.

`/opt/yolo-jail/bin` is deliberately **not** on PATH, even though that is where every yolo
binary now lives: the image bakes `/bin/<name>` symlinks into the mount instead, which
reaches the same names without moving this list — and keeps working for a consumer that
scrubs PATH and spells `/bin/yolo`.

> [!WARNING]
> **Three copies of this order exist and they are independently written.** `BootPath` is
> the authority; the `.bashrc` export in `internal/entrypoint/shell.go` is a second copy,
> and `macosuser.SandboxPath` is a third for the no-container backend. The first two
> disagreed about `$HOME/.local/bin` — second in one, fifth in the other — for months,
> behind a test that only asserted "blockers first, launchers last" and nothing about the
> middle. They are now compared **entry by entry**. Any change to the order changes all
> three.

> [!WARNING]
> **What the launchers' old position bought is now a generation-time check, and it must
> never be spelled "is this name already resolvable on PATH?"** `launcherShadows` writes no
> launcher for a name `/bin`, `/usr/bin`, the store-package farm or a declared mise tool
> already provides. Spelled the wider way it folds in the dirs a launcher *installs into*,
> so after one successful install no launcher is written, PATH resolves the installed
> binary directly, and evergreen works exactly once — green, silent, and identical to the
> freeze the design exists to end. The install prefixes are excluded by the property that
> defines them: every one lives under the jail home, and nothing the image ships does.

The trade is stated rather than assumed: shadowing a baked binary is **expressible** again,
where the old position made it structurally impossible. A name the image bakes still beats
a pack's declared version, because the check declines to write the launcher. Re-check that
before baking a package whose name a pack also claims.

## What the boot writes, and what persists

The entrypoint re-runs its full `Main` on **every** invocation — container start *and*
exec-into-existing — and everything it writes lands in the writable overlays.

**Regenerated every boot, convergent by design.** Identical inputs produce identical bytes
on the same inode, which is what makes an exec into a running jail safe:

- The two generated-script dirs, cleared contents-only: blockers from the blocked-tool
  config, then a lazy installer per pack `program`, then the package-manager launchers.
- `~/.bashrc` (through the base's symlink), the bootstrap and venv-precreate scripts, and
  the combined CA bundle — the last always written, even empty, before the bashrc and
  before any child spawn, so the trust-store env vars propagate to every child the
  entrypoint starts.
- The mise config: created with the base tools, or surgically healed in place, and written
  only when it changed.
- The MCP wrapper scripts under `~/.local/bin`.
- The store-delivered package farm, which runs **first** among the generators: the
  `ld.so.cache` step scans its lib dir, and the launcher-collision check stats its bin dir.
- Every **pack-declared surface**, in one loop with no switch on any tool name
  (`ConfigurePackSurfaces`), then the pack hooks, then the user's `host_files` entries
  through the same composition engine. Composed agent settings are read-modify-write with
  forced jail-managed keys; a sidecar recording the previously-managed names is what makes
  the reconcile convergent, so a user's own additions survive.
- The host nvim config, copy-merged into the `.config` overlay.

**Seeded once, then owned by the jail.** A pack's workspace-scope state dir is seeded from
the machine base by `seedAgentDir`, which copies **top-level regular files only** — auth
tokens — never overwrites, and never recurses. Surface keys declared as seed-if-absent fill
only when missing.

**Runtime state, written by use rather than by boot.** Launcher stamps and install
receipts under `~/.cache`, the perf log (appended, trimmed to a bounded number of runs), the
socat log, bash history, and each agent's own session and history state inside its overlay.

**Shared mutable.** `~/.cache`, `/mise`, and any machine-scope credential dir.

**Deliberately never touched.** The staged `:ro` content (mounted by the CLI — the
entrypoint does nothing for skills or briefings), user-authored keys in agent settings,
non-managed MCP servers, and any directory that is a mount anchor.

`mise hook-env` is never run at boot: it holds a lock and then spawns `uv` through the mise
shim, which *is* mise, so it deadlocks against itself. Interactive shells get mise's shell
hooks from the generated bashrc instead.

**Stale-wrapper cleanup is a cutover, not tidiness.** Older entrypoints generated in-jail
clients as scripts in `~/.local/bin`, which precedes `/bin` on PATH — so a script written
by a previous boot would shadow the baked binary of the same name *forever*, and for the
loophole clients that is worse than stale: the retired scripts speak a transport the run
pipeline no longer publishes, so a surviving script reports "not available" in a jail where
the loophole is running fine. The boot unlinks them by name, only ever as regular files.
The pre-rename generated-script dirs are emptied for the same reason.

## Sharing semantics

| Scope | What lives there |
| :--- | :--- |
| **Per machine, all workspaces** | the `:ro` base home; the machine-scope shared dirs (rw); the mise store at `/mise`; the cache at `~/.cache`; the image-load cache; the layout-version marker; the user config |
| **Per workspace** | everything under `<workspace>/.yolo/home` — the rw overlays, the single-file mountpoints, each pack's workspace-scope state dir, the writable-home backing dirs, the venv shadows |
| **Per jail (container name)** | container tracking files, the briefing and skills staging tree, the socat log, the broker relay log |
| **Per host workspace, inside one home** | the agent history file, keyed on a hash of the host workspace path |
| **Per boot** | `/tmp`, `/run`, `/dev/shm`, anonymous volumes, PID files |
| **Host-only, never mounted** | the host's own mise data, and host credentials generally |

### Why the base is read-only, with symlink hatches

`EnsureGlobalStorage` builds the base as the **union** of every *shipped* pack's writable
and shared dirs. That union is deliberately **not** selection-gated: a `host_files` entry
must never be able to claim a path a pack added tomorrow needs.

On top of that it creates the single-file mountpoints and three **relative** symlinks. The
trick is that the base is read-only but these links resolve *through the container's mount
table* into per-workspace writable overlays — so a tool that atomic-renames one of those
files (an agent rewriting its own `~/.claude.json` constantly) lands its write in a
writable mount. `EnsureSymlink` migrates a pre-existing regular file's data into the target
before re-linking, so an upgrade does not lose the file.

Existing single-file mountpoints are created **only if missing**: one that already exists
may carry restrictive permissions from a prior container's UID mapping, and is deliberately
left alone.

### Shared credentials

One OAuth credential per machine, shared by every jail: the entrypoint makes the agent's
credentials file a **relative** symlink into a machine-scope shared directory — relative so
it resolves through whichever mount backs the agent's own state dir into the separately
mounted shared dir.

Neither the file nor the directory is named in core Go. Both come from a pack's
`shared_credentials` hook, applied by `Env.linkSharedCredential`, which refuses a shared
directory the pack did not also declare as a shared dir. The link decision itself is
`Env.linkThroughShared`, and every decision it returns is logged.

The rule is **schema-blind: the shared file always wins.** A pre-existing local file is
copied out only if the shared one is *empty*, and otherwise discarded.

> [!WARNING]
> **Do not reintroduce a credential merge.** The old one picked a winner by comparing an
> expiry field inside one vendor's OAuth dict — a vendor-schema merge inside a generically
> named hook, which did nothing for the second consumer whose token is shaped differently.
> And do not write credential-*moving* code here: an earlier version of this same path
> unlinked the real file before re-linking and destroyed the token on every boot. Copy,
> never move, and only into a file that is missing. A stranded duplicate costs disk; a
> deleted credential costs a login yolo cannot perform for you. The accepted failure mode
> is the inverse — a revoked shared credential outliving a fresh local login — and the fix
> is to delete the shared file and log in once more.

### The claude.json seed

`SyncClaudeJSONSeed` runs in `prepareWsState` when the pack owning that state dir is
loaded. Forward (seed → workspace) fills only *missing* keys. Reverse (workspace → seed)
fires only when the workspace has a truthy login account the seed lacks, and copies **only**
the login and onboarding keys — MCP server lists and per-project state never leak into the
shared seed. Parse and IO errors degrade to no-ops.

### History isolation

The agent's history file is symlinked to a per-host-workspace file named by a hash of the
host workspace path, driven by a pack's `per_jail_history` hook. This is belt and braces:
even where a state dir is shared across workspaces — Apple Container's single writable home
— history stays distinct per host workspace.

## Lifecycle

**Fresh launch.** `EnsureGlobalStorage` runs first, before config load. Then: config-change
approval, the per-workspace launch `flock`, removal of a stale stopped container, image
autoload, `prepareWsState`, argv assembly, and the container run. The in-container command
is wrapped with provisioning — `mise install` (install only; resolution happens there, not
on an upgrade), the bootstrap script, the venv precreate script, an optional store prune
gated on an env var and on no other jail being live — and then the target command.

**Reuse and attach.** An `exec` into the running container: no `prepareWsState`, no
provisioning wrapper, no mount changes. The entrypoint still re-runs its whole generator
sequence inside the exec, which is safe because every generator is convergent. The
jail-daemon supervisor is guarded by a tmpfs PID-file liveness probe, and port forwarding
skips already-bound ports.

**Every invocation, attach included,** rewrites the staged briefings and skills host-side
with inode-preserving writes, so live single-file mounts inside a running jail pick up the
new content.

**Restart** is fresh-launch semantics with warm caches: `--rm` means the container layer
never survives, everything that matters is on host bind mounts, and the bootstrap script is
idempotent.

**Storage layout migration** is one-time, versioned and marker-stamped. It prunes dangling
host-mise symlinks only when a `canReclaim` callback allows it, and the run command wires
that callback to refuse — so reclamation defers until nothing is live.

## Ownership and UIDs

The container user is **root (UID 0)** with home `/home/agent`. Docker is removed:
`runtime: "docker"` is a validation error, and the resolvable runtimes are `podman`,
`container` (Apple Container) and `macos-user`.

- **Rootless podman, normal branch** — an identity map for ID 0 plus a range map above it,
  matching gidmaps, `/dev/fuse`, and the capabilities nested podman needs. Under rootless
  podman the intermediate ID 0 *is* the invoking host user, so **container root == host
  user** and every write to a bind mount lands host-side owned by you. The upper range maps
  into the subuid range, which is what enables nesting; the image ships its own
  `/etc/subuid` and `/etc/subgid` entries for the *inner* level.
- **NVIDIA GPU branch** — the same identity map, but the runc runtime and no fuse.
- **Nested** (detected from the container-marker files) — `--userns host` and no uidmap at
  all: doubly-nested user namespaces fail mounting `/proc`.
- **Apple Container** — no uidmap or userns flags at all.

There is no `chown` anywhere in the run path. Ownership preservation is purely the rootless
mapping plus the fact that every mount source is created by the host-side CLI process
itself.

## Backend differences

**Apple Container** is structurally different and its mount assembly must be tested
separately.

- **No `:ro` base.** The whole workspace state dir is mounted read-write at `/home/agent`
  in one bind — a device-limit workaround. So every *workspace-scope* declared home dir is
  already writable and its writes already land in the right place, with no explicit mount.
- **Machine-scope shared dirs still need their own mounts**, nested inside that bind exactly
  as the cache is. Leaving them to the single bind is a **silent degradation**, not a
  failure: a shared credential keeps working but becomes per-workspace forever, so every new
  workspace demands a fresh login. The tell that separates the two tiers is which *side* of
  the mount the argv reads from — the workspace state dir means the bind already covers it,
  the machine base means it does not.
- **Single-file binds are unsupported**, so those cases are *materialized* into the
  workspace state dir instead. Anything added as a single-file mount needs an AC arm.
- **`cache_relocations` are skipped** with one warning for the whole set — not because the
  backend cannot nest a bind, but because relocation is unverified on real hardware, and a
  relocation that silently did not take leaves the jail writing the very bytes the user
  moved back onto the filesystem they moved them off.
- **Mountpoints are not pre-created** under the workspace state dir, and do not need to be:
  the mountpoint is auto-created inside a read-write parent bind. podman needs the
  pre-create only because *its* base is `:ro`, where the OCI runtime's `mkdirat` fails
  `EROFS`. The two backends differ here for a reason about the parent mount's mode, not
  about the nested directory.

**macos-user** has no mounts at all. Its home is a staging directory the launch **copies**
into, its sandbox profile allows writes to the whole sandbox home, and it therefore carries
only the source-less half of `host_files`. See
[`macos-user-nix-and-features.md`](macos-user-nix-and-features.md).

> [!WARNING]
> **The `EROFS`-on-nested-mountpoint mechanism is version-dependent, not a cross-runtime
> guarantee.** The pre-create exists because the OCI runtime cannot create a mountpoint
> inside a `:ro` bind — but seven live experiments on one podman build could not reproduce
> that: podman auto-created the nested mountpoint in every realistic variant, and the only
> reproduction was a `:ro` bind whose *host source* was itself read-only. Keep the
> pre-create regardless: it is cheap, idempotent, and makes mode and ownership
> deterministic instead of inheriting podman's.

## What this does not license

- **Not** a writable base. Every new writable path is a punch through the `:ro` base at a
  named destination, with its mountpoint created host-side.
- **Not** a fourth PATH. There are three independently-written copies of one order and that
  is already one too many; `BootPath` is the authority and a second spelling in the boot
  path is refused by test.
- **Not** a per-agent branch anywhere in the mount table. Which dirs are writable, which
  are machine-shared, where skills and briefings land, and what gets seeded are all **pack
  declarations**. Core does not know what an agent is.
- **Not** an exception to the write rules for "just this one file". Both the rename ban and
  the anchor-directory ban exist because a violation is *silent* — a running jail stops
  seeing updates, or a mount detaches — and neither failure names itself.

## Current values

Verified at `d8cf1cf8`. The prose above explains what each of these is for; this table is
the only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Machine base home | `<global storage>/home`, mounted `/home/agent:ro` | `paths.GlobalHome`, `podmanBaseMounts` |
| Per-workspace state | `<workspace>/.yolo/home` | `paths.WorkspaceHomeState` |
| Storage layout version | 2 | `storage.StorageLayoutVersion` |
| Base symlink hatches | `.bashrc` → `.config/bashrc`, `.claude.json` → `.claude/claude.json`, `.gitconfig` → `.config/git/config` | `storage.EnsureGlobalStorage` |
| Generated-script dirs, and their bind anchor | `~/.yolo/bin/{block,launch}`, anchored at `~/.yolo/bin` | `Env.BlockDir`, `Env.LaunchDir`, `Env.GeneratedBinDir` |
| Retired generated-script dirs (emptied, not removed) | `~/.yolo-shims`, `~/.yolo-launchers` | `entrypoint.retiredGeneratedDirs` |
| PATH | `BlockDir:LaunchDir:NpmBin:MiseShims:GoBin:LocalBin:StorePackagesBin:/bin:/usr/bin` | `entrypoint.BootPath` |
| Store-package farm | `/run/yolo/packages` (`bin/`, `lib/`) | `entrypoint.StorePackagesBin`, `StorePackagesLib` |
| mise store | `/mise`, with `MISE_DATA_DIR`, `RUSTUP_HOME`, `CARGO_HOME` under it | `assemble_parts.go`, `internal/cli/run/storagehelpers.go` (`jailMiseStoreDir`) |
| mise store volume name (macOS) | `yolo-mise-data-v2` | `internal/cli/run/assemble.go` (`miseStoreVolume`) |
| Writable-home backing subdir | `writable-home` | `config.WritableHomeBackingSubdir` |
| Container run flags | `--rm -i --init --read-only` | `internal/cli/run/assemble.go` |
| Scratch dirs, and the mode key | `/tmp`, `/var/tmp`, `/var/lib/containers`, `/var/cache/containers`, `/run`, `/dev/shm`; `ephemeral_storage` | `run.ScratchMountArgs` |
| In-jail install prefix | `/opt/yolo-jail/bin` + `/opt/yolo-jail/share/yolo-jail`, both `:ro` | `run.jailPrefixMountArgs` |
| Host-service socket dir, in-jail | `/run/yolo-services` | `paths.JailHostServicesDir` |
| Staged content root, per jail | `<global storage>/agents/<container name>/` | `paths.AgentsDir` |
| Pack manifest mount | `/ctx/packs`, `:ro`, with `YOLO_PACK_ROOT` | `internal/cli/run/assemble.go` (`packCtxDir`) |
| Vestigial mount | `~/.yolo-entrypoint.lock` — mounted, touched and reserved; nothing `flock`s it | `assemble_parts.go`, `storage/ensure.go`, `config/writablehome.go` |
| The real launch lock | one host-side `flock` per workspace, under the state dir's `locks/` | `internal/cli/run/flock.go` |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo, with their original ids.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| `A12` | A failed config generator **refuses the boot**, after every remaining generator has run | It used to warn and discard, so a failed generator still yielded a running jail whose agent silently read a missing or half-written config — the worst outcome available for a config surface, because the jail looks healthy and the misconfiguration surfaces later as inexplicable agent behavior. |
| `B2` / [`OQ-PD12a`](../design/program-delivery.md#decision-ledger) | The lazy-installer dir moves **ahead of the install prefixes**, and the anti-shadowing protection becomes a generation-time check | A launcher after what it installs is unreachable from its own second invocation onward, so the evergreen update the launcher exists to carry stopped running. The check converts a structural impossibility into a handled case, and that cost is accepted rather than hidden. `docs/design/program-delivery.md` [§3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) is the authority for the whole decision. |
| `OQ-6` | Both generated-script dirs live under **one** bind anchor at `~/.yolo/bin` | They need one writable bind, not two, and gathering them in the filesystem is not gathering them on PATH. It also retires the word "shim", which had come to name one of these dirs while colloquially naming the host's launch wrappers — three mechanisms, two of them sharing a word. |
| `OQ-C` | Skills and briefings are deduplicated **by destination** before the argv | Several packs contributing to one destination is the feature for both kinds, and the old advice — "do not declare an `into` another pack uses" — was unfollowable in the configuration it most matters for: an agent pack naming a skills dir plus a house-rules pack sharing a corpus. podman kills the boot on a duplicate destination, so the rule has to be enforced, not documented. |
| `#39` | Apple Container mounts machine-scope shared dirs explicitly, and a stranded per-workspace copy is **copied** into place, never moved | The single writable home made a machine-scope credential silently per-workspace. Repairing it by moving would re-run the experiment that destroyed a token on every boot. |
| `C8` | The in-jail binaries and flake bundle are **mounted**, not baked | It takes the Go sources out of the image derivation, so a Go-only commit costs no image rebuild. The security delta — what executes in the jail is host-mutable with no rebuild — is the trade being made deliberately; see [`image-staging-vs-baking.md`](image-staging-vs-baking.md#the-security-delta). |
