---
status: current
verified: 2026-09-09
verified_commit: d8cf1cf8
covers:
  - internal/cli/run/assemble.go
  - internal/cli/run/assemble_parts.go
  - internal/cli/run/prepare.go
  - internal/cli/run/homeskeleton.go
  - internal/cli/run/mounts.go
  - internal/cli/run/runmount.go
  - internal/cli/run/hostfiles.go
  - internal/cli/run/jailprefix.go
  - internal/cli/run/storagehelpers.go
  - internal/cli/run/wsstatebeneath.go
  - internal/storage/
  - internal/paths/homeskeleton.go
  - internal/paths/statefile.go
  - internal/config/writablehome.go
  - internal/config/hostfiles.go
  - internal/config/selectedpacks.go
  - internal/entrypoint/boot.go
  - internal/entrypoint/env.go
  - internal/entrypoint/fsx.go
  - internal/entrypoint/shims.go
  - internal/entrypoint/scripts.go
  - internal/entrypoint/packhooks.go
tags: [home, mounts, overlays, storage, entrypoint, path]
summary: "How /home/agent is composed: a per-jail read-only skeleton, per-workspace writable overlays punched through it, staged :ro content on top, and files the entrypoint regenerates into the overlays on every boot. Covers the mount stack, the write rules that keep bind mounts alive, PATH, what is shared at which scope, and how the three backends differ."
---

# The jail home — how `/home/agent` is composed

**Status:** CURRENT as of 2026-09-09, verified against `d8cf1cf8`. The home root was
rewritten 2026-09-25 for the per-jail skeleton, the machine store, name reservation and the
Apple Container seed ([`base-home-legacy-state.md`](../design/base-home-legacy-state.md)),
against the working tree of that build; the rest was not re-verified then. The rule for host
code in jail-writable state
([Host code in jail-writable state](#host-code-in-jail-writable-state)) was added the same day,
against the working tree of that build.

`/home/agent` is not a directory that exists anywhere as a whole. It is composed at
container create out of four ingredients: a **read-only home skeleton of this jail's own**
(podman), **per-workspace writable overlays** bind-mounted *over* specific paths inside it, **staged read-only
content** (skills, briefings, composed files) layered on top of those, and **files the
entrypoint regenerates into the overlays at every boot**. Nothing persists through the
container itself — it runs `--rm` on a `--read-only` rootfs, so every durable byte is on a
host bind mount.

One question decides where anything goes: **is this state one truth per machine, one per
workspace, or one per boot?**

| Component | Lives in |
| :--- | :--- |
| The per-jail home skeleton (podman): its mountpoints and redirect links | `internal/cli/run` (`buildHomeSkeleton`), `internal/paths` (`HomeSkeletonRoot`, `HomeSkeletonCoreDirs`, `HomeFileMountpoints`, `HomeFileRedirects`) |
| The machine store (shared dirs, the Claude login seed), layout migration | `internal/storage` (`EnsureGlobalStorage`, `MigrateStorageLayout`, `StorageLayoutVersion`) |
| Host storage paths | `internal/paths` (`GlobalHome`, `GlobalCache`, `GlobalMise`, `AgentsDir`, `WorkspaceHomeState`) |
| Per-workspace overlay creation, per backend, and layout migrations | `internal/cli/run` (`prepareWsState`, `preparePodmanBindSources`, `claudeJSONInWsState`, `migrateOldOverlay`, `rescueSharedDir`) |
| Host operations in jail-writable state, beneath an `os.Root` | `internal/cli/run/wsstatebeneath.go` (`openStateRoot`, `linkedWorkspaceState`, `bindSourceDirBeneath`, `bindSourceFileBeneath`, `copyFileBeneath`, `writeFileBeneath`, `copyFileIfMissing`, `readRegularFileIn`) |
| The mount table, per backend | `internal/cli/run` (`podmanBaseMounts`, `appleContainerBaseMounts`, `ScratchMountArgs`, `ROFileMountArg`, `jailPrefixMountArgs`) |
| Config-declared extra mounts | `internal/cli/run` (`sortedWritableHomeDirs`, `sortedCacheRelocations`, `hostUserFileArgs`, `hostFileWritableDirArgs`, `hostMountArgs`), `internal/config` (`WritableHomeDirs`, `WritableHomeBackingSubdir`) |
| Name reservation for those keys, over the selected packs | `internal/config` (`reservedHomeSegments`, `HostFileEntry.StagingFor`, `resolveSelectedPacks`) |
| Boot-time generation, and its failure policy | `internal/entrypoint` (`Main`, `genStep`, `genFailuresError`) |
| The write rules that keep bind mounts alive | `internal/entrypoint` (`WriteInPlace`, `ClearContents`, `EnsureRelativeSymlink`, `resetAnchorDir`) |
| PATH | `internal/entrypoint` (`BootPath`, `Env.BlockDir`, `Env.LaunchDir`), `internal/macosuser` (`SandboxPath`) |
| Shared-credential, shared-directory and per-jail-history hooks | `internal/entrypoint` (`Env.linkSharedCredential`, `Env.linkSharedDirectory`, `Env.linkIntoSharedDir`, `Env.linkThroughShared`, `Env.isolateHistoryFile`) |
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
`MkdirAll`ed, and for anything nested inside the `:ro` home root the *mountpoint* is created
too, in the skeleton.

**The read-only home root is never written from inside the jail.** Every boot-time write
lands in a writable overlay. Anything that does end up genuinely shared — `~/.cache`,
`/mise`, a machine-scope credential dir — must be append-only,
convergent, or single-writer, because two jails regenerate their configs concurrently and
would otherwise fight.

**Host code touches jail-writable state only beneath an `os.Root`.** A jail can write the
workspace overlay and `<workspace>/.yolo` above it, so any path host code reads, writes,
copies or removes there, or any directory above one, may be a link the last jail left. Open an
`os.Root` on the narrowest jail-writable directory, refusing a link at the root itself, and
name every path relative to it; a podman bind source that is a link is replaced, not merely
refused. [Host code in jail-writable state](#host-code-in-jail-writable-state) has the rule,
the helpers and the members of the class not yet converted.

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
├─[1] ROOT  :ro   <global storage>/agents/<cname>/home/<stamp>   THIS jail's own
│        a SKELETON: mountpoints for core's dirs, the SELECTED packs' writable
│        and shared dirs, the single-file binds and this launch's config-driven
│        binds, plus three relative symlinks that point INTO writable overlays:
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

Tier order is what makes the design work: the root is read-only and holds no content, and
every writable path is a **punch** through it at a known destination. Adding a writable path
therefore means adding a mount *and* a mountpoint in the skeleton — see
[Extra mounts a config declares](#extra-mounts-a-config-declares) and
[Why the home root is a read-only skeleton](#why-the-home-root-is-a-read-only-skeleton-with-symlink-hatches).

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
  mount-option footgun) and any first segment yolo already manages: core's own, or a dir a
  **selected** pack declares ([Reserving a name is not creating a
  directory](#reserving-a-name-is-not-creating-a-directory)). **Safe at any scope**:
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
| inside an existing rw bind (`.config/`, `.cache/`, `.local/`, `go/`, `.npm-global/`, a selected pack's workspace or machine-scope state dir, a `writable_home_dirs` entry) | none | already writable; staging would shadow a yolo mount, or bind a second time at the same destination, which podman refuses |
| a home-root file | symlink | a **relative, dangling** symlink in the skeleton pointing into the rw `.config` overlay — the same hatch `.bashrc` and `.claude.json` use |
| a new top-level dir, including one only an **unselected** pack declares | writable subtree | the `writable_home_dirs` recipe: backing dir, skeleton mountpoint, nested rw bind |

That table is podman's. Apple Container stages nothing: its whole home is the read-write
workspace state dir, so every destination is writable as it stands.

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
- **Granted host files** — each pack's `reads-host` contribution and each of its config
  surfaces that declares `readsHost`, bound `:ro` at the `/ctx` path `packload.CtxPath`
  derives (from the contribution's `into`, or from the surface's own path). There is no config
  key and no env var listing them: which host files cross into the jail is a credential
  boundary fixed in yolo-shipped code. **The protection is DISCLOSURE, not a gate** —
  `HonoredHostFiles` refuses nothing since
  [`OQ-TP9`](../design/trust-paths.md#decision-ledger) retired the fetched-pack origin check
  (2026-09-04); what keeps a hostile declaration
  out is that selecting a pack means writing user-scope config as the host user, and every
  grant is named on the launch banner. The
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
- `~/.bashrc` (through the skeleton's redirect link), the bootstrap and venv-precreate scripts, and
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

**Seeded once, then owned by the jail.** Nothing is copied from the machine store into a
workspace but the Claude login ([The claude.json seed](#the-claudejson-seed)). `seedAgentDir`,
which copied every top-level regular file of a pack's machine-store dir into each new
workspace, legacy bytes included, is deleted
([`base-home-legacy-state.md`](../design/base-home-legacy-state.md#27-the-seed)). Surface
keys declared as seed-if-absent fill only when missing.

**Runtime state, written by use rather than by boot.** Launcher stamps and install
receipts under `~/.cache`, the perf log (appended, trimmed to a bounded number of runs), the
socat log, bash history, and each agent's own session and history state inside its overlay.
The in-jail copy of yolo's embedded packs is the same kind: the first in-jail process that
reads a pack (an in-jail `yolo` command; the boot itself reads none) writes it at
`~/.local/share/yolo-jail/embedded-packs/<hash>`, and later processes of the same build reuse
it. That lands in the per-workspace `~/.local` on every backend: the `local` bind on podman,
the single home bind on Apple Container, and the layout's `~/.local` symlink on macos-user.
It is the jail's own tree, never the host's
([`storage-and-config.md`](storage-and-config.md#machine-wide-storage) gives each backend's host path).

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
| **Per machine, all workspaces** | the machine store `<global storage>/home` (the machine-scope shared dirs, rw, and the Claude login seed); the mise store at `/mise`; the cache at `~/.cache`; the image-load cache; the layout-version marker; the user config |
| **Per workspace** | everything under `<workspace>/.yolo/home` — the rw overlays, the single-file bind sources, each pack's workspace-scope state dir, the writable-home backing dirs, the venv shadows |
| **Per jail (container name)** | the podman home skeletons; container tracking files, the briefing and skills staging tree, the socat log, the broker relay log |
| **Per host workspace, inside one home** | the agent history file, keyed on a hash of the host workspace path |
| **Per boot** | `/tmp`, `/run`, `/dev/shm`, anonymous volumes, PID files |
| **Host-only, never mounted** | the host's own mise data, and host credentials generally |

### Why the home root is a read-only skeleton, with symlink hatches

A **skeleton** (a term [`base-home-legacy-state.md`](../design/base-home-legacy-state.md#1-the-question-and-the-answer)
coined) is a directory holding only the mountpoints and redirect links a launch's binds
need, and no file content. Each fresh podman launch builds a NEW one (`buildHomeSkeleton`)
from THIS launch's selected packs, config and `host_files` entries, under
`<global storage>/agents/<container name>/home/`, and binds it `:ro` at `/home/agent`. So a
pack the jail did not select has no effect inside it.

It replaced ONE machine-wide base, `<global storage>/home`, that every podman jail shared.
That base was provisioned before the config was loaded, so it held the union of every
*shipped* pack's dirs, plus every mountpoint any launch had ever made there: one
workspace's `writable_home_dirs` and `host_files` links, every unselected pack's dirs, and
the machine's shared credential dirs showed up in every jail.

It holds core's dirs, the selected packs' writable and shared dirs, the single-file
mountpoints, the config- and pack-driven mountpoints, and three **relative** symlinks. The
trick is that the root is read-only but these links resolve *through the container's mount
table* into per-workspace writable overlays — so a tool that atomic-renames one of those
files (an agent rewriting its own `~/.claude.json` constantly) lands its write in a
writable mount. A link whose target dir is not bound in this jail (`.claude.json` in a jail
without claude) dangles and reads as absent, which is the right answer there.

- **Fatal, then best-effort.** Core's dirs, the selected packs' dirs, the single-file
  mountpoints and the redirects fail the launch, naming the path; every config- and
  pack-driven entry after them only warns. A pack entry that lands on a redirect name is
  therefore the one that fails, as a warning.
- **Never edited, never removed under a live jail.** A host-side `rmdir` of a mountpoint
  silently detaches the bind inside a running jail, so an attach builds nothing and a fresh
  launch builds a new directory rather than touching an old one. Old skeletons go with the
  jail's whole `agents/<container name>` entry, through `PruneOrphanAgentStaging`, which
  declines when liveness is unknown and keeps a name while it is live or tracked. A launch
  drops its jail's tracking file once the runtime says the container is gone
  (`forgetGoneContainer`), which is what lets the reaper reach the entry at all. A launch that
  starts no container (a refusal after the build, a runtime that never ran) removes the
  skeleton it built, since nothing ever held it.

### Reserving a name is not creating a directory

Two different things have been spelled with the same list, and they are separate:

- **Creating** a directory is what the skeleton builder and the machine store do.
- **Reserving** a name is a config-validation rule that creates nothing: a
  `writable_home_dirs` entry may not claim a first segment yolo already manages, and a
  `host_files` destination under a dir that is already bound read-write gets no staging.

Reservation covers the **selected** packs only, by the maintainer's ruling
([`OQ-BH14`](../design/base-home-legacy-state.md#OQ-BH14)), and so does the skeleton: an
unselected pack is treated as if it does not exist. (The machine store is the exception on the
creating side, below.) So `writable_home_dirs: [".codex"]` passes in a workspace that does not
select codex and is refused once it does, naming the pack; one user-scope entry can be legal
in one workspace and refused in another, and that is accepted. A `host_files` entry under
`~/.codex/` in a claude-only jail is staged as an ordinary new top-level dir. The launch
hands its staged set to the reservation; validation resolves the same selection from the
user config and the pack store.

Two things are not pack names and stay reserved in every workspace: core's own dirs and
files, and `.claude` as a `writable_home_dirs` segment, because core's `~/.claude.json`
redirect targets `.claude/claude.json`.

Two lists are still read from every *shipped* pack:

- **The machine store's directories.** `EnsureGlobalStorage` creates every shipped pack's
  shared dir in `<global storage>/home`, because it runs before the config is loaded. That
  makes a bind source, which a jail mounts only when it selects the pack. The fresh launch
  then creates the selected packs' own, on both container backends, so a configured pack's
  shared dir has a source too (`ensureSharedDirSources`).
- **The `host_files` surface reservation.** A destination some shipped pack composes as a
  surface (`~/.codex/config.toml`) is refused whatever the selection. It is a list of files,
  not directories, and [`OQ-BH14`](../design/base-home-legacy-state.md#OQ-BH14) did not rule on it
  ([`OQ-BH15`](../design/base-home-legacy-state.md#OQ-BH15) asks whether it should narrow too); a
  collision with a configured pack's surface is refused at launch instead
  (`config.SurfaceCollisions`).

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

### Shared directories

The same mechanism for a whole subdirectory instead of one file, requested by a pack's
`shared_directory` hook and applied by `Env.linkSharedDirectory`: the home-relative path the
pack names becomes a relative symlink AT the machine-scope directory it declares, so every
workspace on the machine reads and writes one store. `packs/pi` is the shipped case —
`~/.pi/agent/npm` → `~/.pi-shared-npm`, pi's extension package store — and the reason is
version drift rather than disk: N per-workspace copies leave one jail silently running a
different extension version from its neighbour
([`OQ-1`](../design/pi-extension-lifecycle.md#6-open-questions)).

Both hooks share ONE implementation of the decision table, because that table's ORDER is
what a data-loss bug once got wrong. What the directory shape changes:

* **Empty means no entries**, not zero bytes. A directory's `Size()` is filesystem-defined —
  on some filesystems an empty directory reports a block allocation — so the file rule's test
  would read an empty store as *populated* and discard the workspace's tree.
* **The copy is a strict tree copy** that reports every per-entry error and recreates symlinks
  rather than following them (npm's `.bin` entries are relative links into sibling packages).
  It is deliberately not the boot path's `copyTree`, which drops per-entry errors by design and
  so cannot tell a complete copy from a partial one.
* **A copy in progress is marked inside the shared directory** (`.yolo-copy-incomplete`). A
  tree copy can fail part-way, and without the mark the next launch would read the partial tree
  as the populated side that wins and discard the local store — the same silent failure the file
  rule avoids by construction, arriving one launch later.

> [!NOTE]
> The one-time migration runs IN THE JAIL rather than on the host, because `rename(2)` cannot
> cross a mount point: the local path is a bind of the workspace overlay and the store is a bind
> of the machine store, so a move is `EXDEV` even on one device (measured). It copies.

### The claude.json seed

`SyncClaudeJSONSeed` runs in `prepareWsState` when the pack owning that state dir is
loaded. Both directions copy **only** the login and onboarding keys (`claudeJSONSeedKeys`),
so MCP server lists and per-project state never travel through the shared seed either way.
Forward (seed → workspace) fills only *missing* keys. Reverse (workspace → seed) fires only
when the workspace has a truthy login account the seed lacks. Parse and IO errors degrade to
no-ops. A side that is a **symlink** (or anything but a regular file) is neither read nor
written: the workspace file is one the jail can replace, and the sync runs on the host, so
following a link let a jail aim a host write. The workspace side is named **beneath an
`os.Root` on the overlay**, so a link at a directory above the file is refused too: with only
the file itself checked, a jail that replaced `wsState/claude` with a link to a host
directory had the forward pass create `claude.json` there, holding the seed's login. The write
is a temp file, created `O_EXCL`, renamed over the path.

The workspace side is the file the jail reads as `~/.claude.json`, which differs per backend
(`claudeJSONInWsState`): `<workspace>/.yolo/home/claude/claude.json` on podman, the target of
the skeleton's redirect, and `<workspace>/.yolo/home/.claude.json` on Apple Container, whose
whole home is that directory. Apple Container synced the podman path until the design's
[`OQ-BH12`](../design/base-home-legacy-state.md#OQ-BH12) fix, so the seed never reached one
of its jails. The fix is unmeasured on hardware.

### History isolation

The agent's history file is symlinked to a per-host-workspace file named by a hash of the
host workspace path, driven by a pack's `per_jail_history` hook. This is belt and braces:
even where a state dir is shared across workspaces — Apple Container's single writable home
— history stays distinct per host workspace.

## Host code in jail-writable state

**Jail-writable state** (a term this page coins) is any host directory a jail can write into
through any of its mounts. Three places qualify, and the host launcher reads, writes and
copies in all three on the next launch, as the host user:

* **The workspace overlay**, `<workspace>/.yolo/home` (`wsState` in the code). Apple Container
  binds it whole at `/home/agent`; podman binds its entries one at a time.
* **`<workspace>/.yolo` itself, overlay included.** Both backends bind the workspace at
  `/workspace` and hide nothing under it, so a jail reaches `/workspace/.yolo/home` directly,
  unless a `workspace_readonly` entry covers `.yolo`.
* **A machine-scope shared dir** in the machine store, `<state>/home/<dir>`, bound read-write
  into every jail whose packs declare it.

So any path the launcher touches there, or any directory above one, or the overlay and `.yolo`
themselves, may be a symbolic link the last jail left. A plain path operation follows it. A
write truncates the host file the link names; a read copies a host file into the jail's own
state; a `MkdirAll` creates directories outside the overlay. And podman resolves a **bind
source**, the host path on the left of a `-v` flag, on the host, so a link left at one binds
whatever it points to into the next jail: `wsState/claude` pointing at the host's `~/.claude`
would hand the jail the host's own directory, read-write.

**The rule: host code touches jail-writable state only beneath an `os.Root`.** `os.Root`
resolves every component itself and refuses one that leaves the root, including a component
swapped for a link between two calls; it follows a link only when the link is relative and
stays inside. The helpers are in `internal/cli/run/wsstatebeneath.go`, and, for the files
directly under `.yolo` and for host code outside the run pipeline, in
`internal/paths/statefile.go`:

| Operation | Helper | What a link does to it |
| :--- | :--- | :--- |
| Open the root | `openStateRoot` (the overlay: refuses a link at it or at `.yolo`), `openDirRefusingLink` (one directory) | refused, with an error naming the path; each open is checked against the directory that was `Lstat`ed |
| A podman directory bind source | `bindSourceDirBeneath`, `ensureBindSourceDir` | a link at any component is **replaced** by a real directory, and the launch names it |
| A podman single-file bind source | `bindSourceFileBeneath` | a link is replaced by an empty regular file; a regular file is left alone |
| Write or copy a file | `writeFileBeneath`, `writeFileBeneathMode`, `copyFileBeneath` | a link at the file is replaced; one above it that leaves the root refuses the write; a regular file is rewritten in place, keeping its inode |
| Copy only into a missing file (migrations) | `copyFileIfMissing`, `copyLinkIfMissing` | the source must `Lstat` as a regular file and is never followed; anything at the target, a dangling link included, is an existing target; a link is copied as a link |
| Read a host-consumed file | `readRegularFileIn` | a link at the file or its directory is not read |
| A file directly under `.yolo` | `paths.OpenWorkspaceStateFile`, `paths.WriteWorkspaceStateFile`, `paths.OpenStateDirRoot` | a link at `.yolo` is refused, naming it; a link at the file is replaced by a regular file |
| Open existing state without creating it | `paths.OpenWorkspaceStateSubdir` (`.yolo/home`, `.yolo/prism`), `paths.OpenStateSubdirRoot` (a child of an open root) | a link at `.yolo` or at the subdir is refused, naming it; a missing one is an ordinary not-exist error |
| Read or write one file beneath an open root | `paths.ReadRegularFileBeneath`, `paths.OpenRegularFileBeneath`, `paths.WriteRegularFileBeneath` | a read refuses a link at the file, even one that stays inside the root; a write replaces it with a regular file and rewrites a regular one in place |

Replacing a bind source rather than refusing it is deliberate. Refusing only the host's own
`mkdir` still hands the link to podman, and a link at a bind source is never the jail's
ordinary use of its home: the jail sees the source as the mountpoint itself, which it cannot
replace, and reaches the source only through `/workspace/.yolo/home`. On Apple Container the
overlay IS the jail's home, a link in it is the jail user's own, and the guest resolves it, so
there `prepareWsState` only creates directories beneath the root and leaves links alone.

**A linked `.yolo` or `.yolo/home` refuses the launch.** `Run` checks both
(`linkedWorkspaceState`) right after the workspace-scope guard and before the launch log,
whose tee is the first write under `.yolo`, and names the link and the `rm` that clears it.
There is no override: a link there is indistinguishable from one a jail planted, and it would
carry every write below it, and every bind source under it, wherever it points
([`OQ-JH1`](#OQ-JH1) is the open question about a user who relocated the directory on purpose).

**Converted call sites.** `prepareWsState` opens one root on the overlay and does everything
beneath it: the bind sources (`preparePodmanBindSources`), Apple Container's pack dirs, the
legacy layout migrations (`migrateOldOverlay`, whose source was read through a link: a linked
`claude-projects` copied a host tree into the jail's `~/.claude/projects`), the settings copy,
the shared-dir rescue (`rescueSharedDir`, whose machine-wide target is opened as its own root,
so a link in the shared dir cannot reach its sibling, the Claude login seed) and the seed sync.
Beyond it: `ROFileMountArg`'s nested-bind copy, the composed gitconfig
(`gitIdentityMountArgs`), `acMaterialize` and `acMaterializeTree` (whose `RemoveAll` deleted a
host directory through a linked overlay), `writeUserEnvFile` (whose write and `chmod` put the
hydrated secrets in a host file of the jail's choosing), `userConfigMountArgs`,
`venvShadowMountArgs`'s backing dirs, `prepareHostFiles`, the pack `files` mountpoints
(`mountpointBeneath`) and `readHandoff`, whose content becomes a section of the jail's own
briefing, so a link at `.yolo/handover.md` copied any host file it named into the jail.
`internal/cli/run/wsstatelinks_test.go` plants a link at each and above each, and each case
failed on the code before it. Each of the three bind-source preparers (`prepareWsState`,
`venvShadowMountArgs`, `prepareHostFiles`) prints a `Replaced a symbolic link at <path>` line
for every link it replaced (`printReplacedLinks`).

**The machine store's shared credential file.** `storage.EnsureGlobalStorage` runs as the host
user before any config loads, on every launch and every `yolo check`, and makes
`<state>/home/.claude-shared-credentials/.credentials.json` a regular file, migrating the
legacy `<state>/home/.claude/.credentials.json` into it. It did so with a link-following
`stat`, an `O_CREATE` touch and an `os.Create` copy, so a dangling link a claude jail left at
that name created the link's target on the host, and a link to an empty host file received
the legacy credential. It now works beneath a root on the shared dir (`paths.OpenStateDirRoot`,
refusing the dir itself as a link), replaces a non-regular file at the name, and creates the
file `O_EXCL`; `internal/storage/sharedcredlink_test.go` failed on the code before it.

**Files directly under `.yolo`.** These sit beside the overlay rather than in it, and are
opened by `internal/paths/statefile.go`: `OpenWorkspaceStateFile` and `WriteWorkspaceStateFile`
create `.yolo` through `EnsureWorkspaceStateDir`, open a root on it with `OpenStateDirRoot`
(which refuses a linked `.yolo` with a `LinkedStateDirError` naming it), remove anything at the
file name that is not a regular file, and check that what they opened is one. The callers are
`attachLaunchLog`'s `launch.log`, whose tee appended every launch line (they quote the
jail-writable workspace config) to whatever host file a link named; the host perf log
(`hostPerfFileSink`, through `perf.FileSinkTo`); `housekeeping.log`; and the config snapshots
`config-assembled.json` and `config-boot.json` (`writeWorkspaceSnapshot`). The two logs' trim
runs through the open descriptor (`perf.TrimRunsInOpenFile`), never a second open by path.
`EnsureWorkspaceStateDir`'s own `.gitignore` is created `O_EXCL` beneath the same root and is
skipped under a linked `.yolo`. The handoff rename (`consumeHandoff`), the pack `files` ownership
manifest (read through `readRegularFileIn`, saved through a temp file written beneath a root and
renamed) and its legacy archive (`archiveLegacyPackFileMountpoint`) run beneath a root on `.yolo`
too. `internal/cli/run/wsstatefiles_test.go`, `internal/config/snapshotlink_test.go` and
`internal/paths/statefile_test.go` plant a link at each, dangling and not, and a linked `.yolo`,
and each case failed on the code before it. The manifest's read half guards what is parsed, not
what the manifest says: the jail can write a regular manifest there as easily as a link.

**The capture at jail exit.** `captureOnTerminate` (`internal/cli/configcapture.go`) runs on the
host when a jail exits, reading each capture surface under the overlay and its sidecars under
`.yolo/prism`, and writing the `.overlay.json` and list-capture sidecars back. By plain path, a
link at a surface or a directory above one read a host file into the overlay the next launch
composes, and a link at a sidecar had `os.WriteFile` truncate the host file it named and write
capture JSON into it. It now opens a root on `.yolo/home` and one on `.yolo/prism`
(`paths.OpenWorkspaceStateSubdir`, which creates nothing and refuses a link at `.yolo` or at the
subdir), and `captureSurfaceAt` (`internal/cli/configdiff.go`) names every file beneath one
through `captureFile`. `internal/cli/configcapturelinks_test.go` plants a link at each file and at
each directory on the way, dangling and not.

**`yolo prune --apply`.** Prune runs as the host user over every tracked workspace's overlay and
the machine store. The age purge (`PurgeAgentLogs`, `PurgeCacheByAge`, both through
`purgeOldFilesUnder`) walks, checks and removes beneath a root: the overlay's is opened with
`paths.OpenWorkspaceStateSubdir`, so a link at `.yolo`, `.yolo/home` or an agent's log dir is
refused rather than walked into, and a directory swapped for a link between the walk and the
removal is refused at the removal. By plain path it deleted the old files of the host directory
the link named. The dedup (`WalkDedupableWorkspaces`, `WalkGlobalDedupable`,
`HardlinkDuplicateFiles`) walks and hashes beneath a root too, and links with `linkat(2)` between
two directory descriptors, each opened beneath its own root, checking that both names are still
the files it hashed (`linkBeneath`, `internal/prune/dedupbeneath.go`). By plain path, a directory
swapped for a link after the walk had it replace a host file with a hardlink to the jail's copy,
or hardlink the host file into the jail's tree, handing the jail a writable name for its inode.
The global cache and a relocated cache segment are opened as roots that FOLLOW a link at the root
itself: each is a jail's mountpoint, which the jail cannot replace, and a user may relocate one on
purpose. `internal/prune/jailwritable_test.go` covers each shape, dangling and not.

**The Claude OAuth broker's credential file.** The host broker (`internal/oauthbroker`) reads
`<state>/home/.claude-shared-credentials/.credentials.json`, a file in a shared dir every claude
jail binds read-write, through `readCreds`: a root on the directory (`paths.OpenStateDirRoot`) and
a regular-file-only open, with a link at the name refused and named. Every read goes through it:
the token answer (`oauthFromCreds`), the self-check (`gradeSharedCreds`, which reports a link as a
FAIL naming it) and the log line (`describeCreds`). By plain path, a link to the host's own Claude
credentials had the broker serve their access token to every jail that asked. The write
(`WriteTokens`) needed no change: it renames a fresh `O_EXCL` temp file over the name, which
replaces a link rather than following it. `internal/oauthbroker/credslink_test.go` covers both.

**The pack `files` retirement.** `preparePackFiles` removes the mountpoints the ownership manifest
records and the configured packs no longer claim (`retirePackFileMountpoints`). The manifest is in
`.yolo`, so the jail chooses every recorded path and its digest. The check that a mountpoint is
unchanged and its removal are both made beneath a root on the overlay, opened refusing a link at
it or at `.yolo`. The removal was an `os.Remove` by path after an `EvalSymlinks` containment check,
which a link at `.yolo/home` itself passed (both sides resolved into the host directory), so a
forged digest removed any host file whose content the jail knows; and a directory swapped for a
link after the check had the removal delete the host file of that name.
`internal/cli/run/packfilesretire_test.go` covers both.

**Host-side `yolo config` at a workspace target.** `configTarget`
(`internal/cli/configtarget.go`) resolves a workspace's surfaces to its overlay under
`.yolo/home` and its sidecars to `.yolo/prism`, and the verbs in `internal/cli/configdiff.go`,
`configls.go` and `configprovenance.go` read and write them as the host user. By plain path, a
link at a sidecar had `diff` print a host file as captured edits and `ls` count or list its keys;
a link at the surface had `reset`, which runs whenever the workspace's jail is not running,
truncate the host file it named, after discarding the captures; a link at `.yolo/prism` had
`reset` remove host files of the sidecars' names; and `capture --force` read a host file into
the overlay and wrote capture JSON through a sidecar link into the file it named. Each file is
now a `captureFile` opened beneath a root from `paths.OpenWorkspaceStateSubdir`
(`storeFile`, `surfaceStateFile`): a read takes only a regular file, a write replaces a link with
a regular file, a remove takes the link itself, and a link at a name being read, or at `.yolo`,
the subdir or a directory on the way, is refused. `reset` checks the surface before it discards
anything. Each refusal names the path and says the jail can write it, and `diff` and `ls` exit 1
rather than report a store they did not read. The host notch and the jail that owns the
workspace keep plain paths, since there every file is the process's own.
`internal/cli/configverblinks_test.go` plants a link at each file and at each directory on the
way, dangling and not, per verb. `yolo config promote` and `yolo apply --sealed` read the
workspace store the same way. Promote opens its sidecars through `storeFile`
(`wsOverlayFile`, `wsLastRenderFile`, `wsListCaptureFile`). A refused link
fails its plan with exit 1 before anything is written. Its overlay write-back goes through the
same `captureFile`, so a link the jail swaps in at the overlay is replaced, and a linked
`.yolo/prism` abandons the promotion. The write-back is atomic (`captureFile.replace`: a temp
file and a rename, both beneath the root), and so is every other sidecar write: the overlay and list-capture sidecars that capture and
capture-on-terminate write, and the baseline reset re-seeds, each at its store's mode
(`SidecarFileMode`, 0600 in a real home). No mount names a file in either store. The surface
reset truncates is the exception, and is rewritten in place in every form, because it is
mount-visible: a `host_files` source, a single-file bind target in-jail, or one of the
single-file bind sources at the top of `<workspace>/.yolo/home` (`bash_history`,
`yolo-user-env.sh`, …), which `~/.bash_history` maps onto. `apply --sealed` counts captured keys
through `sealedConfigTarget` (`overlayKeyCount`) and refuses to seal over a sidecar it could not
read.

**What this does not close.** podman resolves a bind source when the container is created, after
the preparation has replaced any link, so a jail running CONCURRENTLY with a write path into this
overlay can plant one in between. The launch lock rules out a second jail of this workspace; a jail
whose own workspace contains this one is the remaining writer. Closing that needs bind sources
podman opens by descriptor, which it does not offer.

<a id="OQ-JH1"></a>**[`OQ-JH1`](#OQ-JH1) — may a user relocate `.yolo` or `.yolo/home` with a symbolic link?** The launch now
refuses one, with no override, because it cannot tell a link the user made from one a jail made.
A user who moved the overlay to another disk that way (the machine-store directories are
routinely large) is refused on the next launch. Options: (a) keep the refusal and document a bind
mount as the way to relocate, since a mountpoint `Lstat`s as a directory; (b) accept a link whose
target is recorded host-side, outside every jail-writable directory, as the user's; (c) add a
`YOLO_ALLOW_*` hatch. Leaning (a): a hatch would be for yolo's own safety check rather than a
broken config, and (b) adds a host-side record for a layout nothing documents.

## Lifecycle

**Fresh launch.** `EnsureGlobalStorage` runs first, before config load. Then: config-change
approval, the per-workspace launch `flock`, removal of a stale stopped container, image
autoload, `prepareWsState`, `prepareHostFiles`, the selected packs' shared-dir sources and a
new home skeleton (podman), argv assembly, and the container run. The in-container command
is wrapped with provisioning — `mise install` (install only; resolution happens there, not
on an upgrade), the bootstrap script, the venv precreate script, an optional store prune
gated on an env var and on no other jail being live — and then the target command.

**Reuse and attach.** An `exec` into the running container: no `prepareWsState`, no new
skeleton, no provisioning wrapper, no mount changes. The entrypoint still re-runs its whole generator
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

- **No `:ro` home root, and no skeleton.** The whole workspace state dir is mounted
  read-write at `/home/agent` in one bind — a device-limit workaround. So every
  *workspace-scope* declared home dir is already writable and its writes already land in the
  right place, with no explicit mount.
- **The state dir is laid out as the home itself.** `prepareWsState` creates the selected
  packs' dirs at their dotted names (`.claude`, not podman's `claude`) and syncs the login
  seed with `.claude.json`, and creates none of podman's dot-stripped bind sources, which on
  this backend were stray entries in the jail's home (`~/claude`, `~/npm-global`)
  ([`OQ-BH12`](../design/base-home-legacy-state.md#OQ-BH12), unmeasured on hardware). The
  one-time legacy migrations write to the dotted paths too.
- **Machine-scope shared dirs still need their own mounts**, nested inside that bind exactly
  as the cache is. Leaving them to the single bind is a **silent degradation**, not a
  failure: a shared credential keeps working but becomes per-workspace forever, so every new
  workspace demands a fresh login. The tell that separates the two tiers is which *side* of
  the mount the argv reads from — the workspace state dir means the bind already covers it,
  the machine store means it does not.
- **Single-file binds are unsupported**, so those cases are *materialized* into the
  workspace state dir instead. Anything added as a single-file mount needs an AC arm.
- **`cache_relocations` are skipped** with one warning for the whole set — not because the
  backend cannot nest a bind, but because relocation is unverified on real hardware, and a
  relocation that silently did not take leaves the jail writing the very bytes the user
  moved back onto the filesystem they moved them off.
- **Mountpoints are not pre-created** under the workspace state dir, and do not need to be:
  the mountpoint is auto-created inside a read-write parent bind. podman needs the
  pre-create only because *its* home root is `:ro`, where the OCI runtime's `mkdirat` fails
  `EROFS`. The two backends differ here for a reason about the parent mount's mode, not
  about the nested directory.

**macos-user** has no mounts at all. Its home is a staging directory the launch **copies**
into, its sandbox profile allows writes to the whole sandbox home, and it therefore carries
only the source-less half of `host_files`. It reaches the same two tiers by a different
primitive: every directory the podman argv binds from `<workspace>/.yolo/home` is a SYMLINK
from the sandbox account home into that same sidecar, and each pack-declared `scope: machine`
directory stays in the account home and is mirrored back into the sidecar so the relative
credential link above still resolves. See
[`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) and
[`macos-user-home-tiers.md`](macos-user-home-tiers.md).

> [!WARNING]
> **The `EROFS`-on-nested-mountpoint mechanism is version-dependent, not a cross-runtime
> guarantee.** The pre-create exists because the OCI runtime cannot create a mountpoint
> inside a `:ro` bind — but seven live experiments on one podman build could not reproduce
> that: podman auto-created the nested mountpoint in every realistic variant, and the only
> reproduction was a `:ro` bind whose *host source* was itself read-only. Keep the
> pre-create regardless: it is cheap, idempotent, and makes mode and ownership
> deterministic instead of inheriting podman's.

## What this does not license

- **Not** a writable home root. Every new writable path is a punch through the `:ro`
  skeleton at a named destination, with its mountpoint created in the skeleton host-side.
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

Verified at `d8cf1cf8`; the home-root rows were updated 2026-09-25 with the skeleton. The
prose above explains what each of these is for; this table is the only place the values
themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Podman home root | a new `<global storage>/agents/<container name>/home/<UTC stamp>-<random>` per fresh launch, mounted `/home/agent:ro` | `paths.HomeSkeletonRoot`, `buildHomeSkeleton`, `podmanBaseMounts` |
| Machine store | `<global storage>/home`: the shared dirs' bind sources and the Claude login seed; mounted at `/home/agent` by nothing | `paths.GlobalHome`, `storage.EnsureGlobalStorage` |
| Per-workspace state | `<workspace>/.yolo/home` | `paths.WorkspaceHomeState` |
| Storage layout version | 2 | `storage.StorageLayoutVersion` |
| Skeleton redirect links | `.bashrc` → `.config/bashrc`, `.claude.json` → `.claude/claude.json`, `.gitconfig` → `.config/git/config` | `paths.HomeFileRedirects`, `buildHomeSkeleton` |
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
| Vestigial mount | `~/.yolo-entrypoint.lock` — mounted, touched and reserved; nothing `flock`s it | `assemble_parts.go`, `paths.HomeFileMountpoints`, `config/writablehome.go` |
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
| `DIR-BH2` / [`OQ-BH10`](../design/base-home-legacy-state.md#10-decision-ledger) | Each podman jail gets its **own** read-only skeleton, a new one per fresh launch, never edited | A shared base leaked one workspace's config and every unselected pack's dirs, and a claude-less jail could read the machine's Claude credential file through it. Editing a skeleton in place would need a liveness answer the launch path cannot give. |
| [`OQ-BH14`](../design/base-home-legacy-state.md#10-decision-ledger) | Name reservation covers only the **selected** packs | Packs come from anywhere and are added and removed at will, so the shipped set never covered what a jail could select; reserving it was an unselected pack's effect. |
| `C8` | The in-jail binaries and flake bundle are **mounted**, not baked | It takes the Go sources out of the image derivation, so a Go-only commit costs no image rebuild. The security delta — what executes in the jail is host-mutable with no rebuild — is the trade being made deliberately; see [`image-staging-vs-baking.md`](image-staging-vs-baking.md#the-security-delta). |
