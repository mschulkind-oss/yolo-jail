---
status: in-review
verified: 2026-10-01
verified_commit: d4e435a3
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
  - internal/cli/run/servicessession.go
  - internal/cli/run/persistencemap.go
  - internal/cli/run/durabledir.go
  - internal/cli/run/trackingcleanup.go
  - internal/cli/run/shareddirsources.go
  - internal/durable/
  - internal/jailcontent/persistence.go
  - internal/jailcontent/persistencesection.go
  - internal/entrypoint/durablereport.go
  - internal/cli/check/section_durabledir.go
  - internal/basehome/
  - internal/cli/run/keeper.go
  - internal/cli/run/keeperspawn.go
  - internal/cli/run/keeperwatch.go
  - internal/cli/run/lifecycle.go
  - internal/cli/run/sessionlock.go
  - internal/entrypoint/jailmain.go
  - internal/entrypoint/sharedlink.go
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
stage: DESIGN
next: "Rule OQ-JH1, whether a user may relocate .yolo or .yolo/home with a symbolic link: options (a) keep the refusal and document a bind mount, (b) accept a link whose target is recorded host-side, (c) a YOLO_ALLOW_* hatch; leaning (a)"
summary: "How /home/agent is composed: a per-jail read-only skeleton, per-workspace writable overlays punched through it, staged :ro content on top, and files the entrypoint regenerates into the overlays on every boot. Covers the mount stack, the write rules that keep bind mounts alive, PATH, what is shared at which scope, the storage classes the briefing tells every agent about and the durable dir, and how the three backends differ."
---

# The jail home — how `/home/agent` is composed

**Status:** verified 2026-10-01 against `d4e435a3`, the whole doc. One question is open,
[`OQ-JH1`](#OQ-JH1), on relocating `.yolo` with a symbolic link. The whole-document
verification stamp above is unchanged; three parts were reconciled separately against
`8e65f3444` on 2026-10-16, when the designs that built them graduated here: the
[per-jail home skeleton](#why-the-home-root-is-a-read-only-skeleton-with-symlink-hatches), the
[claude.json seed](#the-claudejson-seed), and the
[storage classes and the durable dir](#storage-classes-and-the-durable-dir). MEASURED: the podman
skeleton, rootful in a nested jail and rootless in CI on both arches; the durable dir and its
briefing section in a podman jail on a rootless host; on Apple Container, the storage-classes
section and a writable `$YOLO_DURABLE_DIR`, and fake-seed delivery and directory layout, both
recorded HOLDS in [run 37133569003](https://github.com/mschulkind-oss/yolo-jail/actions/runs/37133569003),
2026-10-03. UNMEASURED: real Claude authentication on Apple Container; owed:
[a native seed regression check](#the-owed-apple-container-seed-gate).
**Stale since `57ed9e5ae` (2026-11-06):** each named setup now keeps its own home under
`<workspace>/.yolo/setups/<name>/home` (`paths.SetupHomeState`), and `<workspace>/.yolo/home` is
only the legacy home a launch moves from (`paths.LegacyHomeState`). The passages below that name
`.yolo/home` as the per-workspace overlay source are owed a re-verification against
[named setups](../design/named-setups.md).

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
| The jail's keeper, the hold main process and the session count | `internal/cli/run` (`KeeperMain`, `startKeeper`, `reapOrphanedJails`, `sessionlock.go`), `internal/entrypoint` (`jailmain.go`) |

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
│        the user config for nested jails, and the per-agent env files
│        (.config/yolo-agent-env <- <workspace>/.yolo/home/agent-env; the
│        credential gate's per-agent half, providers.md#the-credential-gate)
│
└─ siblings outside /home/agent
     /workspace       the workspace, rw
     /mise            the mise store (a named volume on macOS; per workspace on Apple Container)
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
  ([host-cache aliasing](disk-footprint.md#host-cas-aliasing),
  [OQ-BF10](disk-footprint.md#why-its-this-way)).
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
- **`mounts`** — each string `host[:container]` pair bound `:ro`, defaulting to `/ctx/<basename>`;
  an object element `{"host", "at", "mode"}` with `"mode": "rw"`, read from the user scope only
  (`config.LoadRWMounts`), is bound with no mode suffix and disclosed on every launch
  (`run.configCtxMounts`, [`context-mounts.md`](context-mounts.md#read-write-context-mounts)). Every container
  launch carries `YOLO_CONTEXT_DIR=/ctx`. Nothing to do with the home.

Where a `host_files` destination lands decides whether the composed write can succeed at
all:

| Destination | Staging | Mechanism |
| :--- | :--- | :--- |
| inside an existing rw bind (`.config/`, `.cache/`, `.local/`, `go/`, `.npm-global/`, a selected pack's workspace or machine-scope state dir, a `writable_home_dirs` entry) | none | already writable; staging would shadow a yolo mount, or bind a second time at the same destination, which podman refuses |
| a home-root file | symlink | a **relative, dangling** symlink in the skeleton pointing into the rw `.config` overlay — the same hatch `.bashrc` and `.claude.json` use |
| a new top-level dir, including one only an **unselected** pack declares | writable subtree | the `writable_home_dirs` recipe: backing dir, skeleton mountpoint, nested rw bind |

That table is podman's, and its symlink row holds on macos-user too: the home layout lays the same
relative link in the sandbox account home, from the same `StagingFor` and `SymlinkTarget` calls, so
a home-root file lands in the workspace's own `.config` on both
([HT-D9](macos-user-home-tiers.md#ht-d9)). The exception is the three login rc files the
macos-user bootstrap writes itself on every launch, `.zprofile`, `.zshrc` and `.bash_profile`:
they stay real files in the account home, and an entry naming one is refused there with the next
step ([HT-D12](macos-user-home-tiers.md#ht-d12), [HT-D13](macos-user-home-tiers.md#ht-d13)). The
other two rows have no macos-user counterpart, because that home is writable as it stands: a
destination under a layout link lands in the workspace, and one in a new top-level directory lands
in the account home every workspace shares. Apple Container stages nothing: its whole home is the read-write workspace state dir, so every
destination is writable as it stands.

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
  because `jailcontent.PrepareSkillsWith` merges three sources into it (the workspace's
  project-scope skills < built-ins < every pack's skills, the local pack last) and that merge has
  to land somewhere ([the layers](agent-briefings.md#skills)). Rebuilt on every invocation.
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

- **Scratch** — the rootfs is read-only, so writable scratch is explicit: podman volumes
  or tmpfs for `/tmp`, `/var/tmp` and the two container dirs, plus tmpfs `/run` and
  `/dev/shm`. Selected by `ephemeral_storage`. The volumes are named per launch, and a
  detached remover deletes them after the jail exits, so quitting does not wait for the
  delete ([why](perf-logging.md#the-linger-was-the-scratch-volumes)).
- **The in-jail CLI and its flake bundle** — two `:ro` mounts making up the same install
  prefix a host install has, `bin/` beside `share/yolo-jail/`, so the in-jail CLI finds its
  complete bundle exactly the way a host install resolves it. The image
  bakes only the mountpoints (a read-only rootfs cannot grow one) and the `/bin/<name>`
  symlinks that point into them.
- **The mise store at `/mise`** — a host bind on Linux, one named volume on a Podman Machine.
  Inside a nested jail the store is `/mise` itself, so every nesting depth shares one store. The
  host's own mise data directory is never mounted. **On Apple Container each workspace has its
  own**, a named volume `<container name>.mise` that the launch creates labelled with its
  workspace: a disk there attaches to one VM at a time, and one shared volume let only one
  workspace's jail run ([OQ-MB1](../research/macos-backend-performance.md#OQ-MB1)). `yolo prune`
  removes the disk of a workspace that is gone, and `yolo stores` lists them.
- **Host nix daemon socket and store** — mounted when both exist and the runtime is not
  Apple Container; macOS podman additionally requires an opt-in env var. Without it, nix
  in the jail fails with "build users group has no members".
- **Git identity and the global gitignore** — host-composed and mounted `:ro` together
  (`gitIdentityMountArgs`): a composed `[user]`/`[core]` config, plus the host's
  `core.excludesFile` when it resolves to a real file.
- **Host-service endpoints** — a per-jail host directory, `/tmp/yolo-host-services-<8hex>`
  (the suffix is `paths.JailShortHash` of the container name), bound read-write at
  `/run/yolo-services`. It holds each host service's
  [endpoint file](loophole-transport.md#the-endpoint-file-is-a-credential), the cgroup
  delegate's socket, and the socket of a config loophole that declares no transport. The
  spawn (`startLoopholesMatching`), which runs in the jail's
  [keeper](#lifecycle), creates it, `0700`, and nothing earlier in the launch does, so a
  launch refused before its host services start leaves no directory behind. The keeper's
  teardown (`stopLoopholes`) removes it only once the runtime answers that no container of
  that name exists, running or not. It stays in place while a relaunch holds the workspace lock, while a container of
  that name still exists, and when the runtime cannot be asked, and the next launch of the
  workspace reuses it. "Exists" rather than "runs" because a relaunch releases the
  workspace lock when its bounded wait for a running container gives up, so its container
  can still be only created while its endpoint files are already here. No host daemon
  binds its own socket here: a jail's daemon binds `paths.FrontSocketFile`, outside the directory,
  and this directory holds the endpoint files its front publishes. A keeper that dies without its teardown (`SIGKILL`, OOM)
  leaves the directory behind. If its jail outlived it, the next launch on the machine reaps
  that jail once no session is left in it (`reapOrphanedJails`) and removes the directory
  through the same checks.
  ⚠ If the jail is gone too, nothing sweeps the directory until that workspace's next
  launch ends, or the host reboots.
- **On macos-user, one host-services directory per session** (`HSD-4`). A session is one
  macos-user invocation: a sandbox and its host services, from launch to teardown. There is no
  container and no mount, so the sandbox reads its endpoint files where they are, through the
  per-file ACL grant the plan stages. The spawn creates
  `/tmp/yolo-host-services-<8hex>-<random>` (`os.MkdirTemp`, `0700`), the session publishes
  every endpoint there, and its fronted daemons' upstream sockets are keyed by a hash of that
  path. The session holds an exclusive `flock` on `.session.lock` inside it for its whole life,
  and the lock reaches that name already held: it is created under a pending name, locked, and
  renamed, so no sweep finds a live session's lock free.
  Its teardown removes that directory and no other, without the container checks, which answer
  a question about a relaunch publishing into the same directory and have nothing to answer
  here. A session killed without its teardown leaves its directory behind with its lock free,
  and the next macos-user launch on the machine removes it. Every session directory whose lock
  is held, that has no lock file, or whose lock cannot be opened stays. The directory the
  workspace's container name selects is not used on this backend. A `--dry-run` names
  `…-<session>` in place of the suffix, because it creates none.
- **Pack manifests at `/ctx/packs`** — `:ro`, and that is load-bearing rather than
  tidiness: a manifest is an *input* to composition, and an agent that could rewrite one
  in-jail could grant its own pack a host file on the next boot, or hand the next attach
  a declaration it composes from. The source is this launch's own pack tree, one per launch
  and never edited, so the jail sees the packs it booted with until it stops
  ([`OQ-PK2`](pack-system.md#oq-pk2)).
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
> **Two copies of this order are written independently, and a third shares its head.**
> `BootPath` is the authority; the `.bashrc` export in `internal/entrypoint/shell.go` is a
> second copy. The two disagreed about `$HOME/.local/bin` (second in one, fifth in the
> other) for months, behind a test that only asserted "blockers first, launchers last" and
> nothing about the middle. They are now compared **entry by entry**. `macosuser.SandboxPath`,
> the no-container backend's PATH, takes its head from `entrypoint.HomePathDirs`, the function
> `BootPath` is built from, and keeps its own macOS tail
> ([OQ-PD27](../design/program-delivery.md#decision-ledger)). Any change to the order changes
> `BootPath` and the `.bashrc` export.

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
workspace, legacy bytes included, is deleted on every backend: since the shared home stopped
being writable its only inputs were legacy bytes, empty mountpoint files and `claude.json`,
which the seed sync already handles. Surface keys declared as seed-if-absent fill only when
missing.

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

**Shared mutable.** `~/.cache`, `/mise` (except on Apple Container, where it is per
workspace), and any machine-scope credential dir.

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
| **Per machine, all workspaces** | the machine store `<global storage>/home` (the machine-scope shared dirs, rw, and the Claude login seed); the mise store at `/mise`, except on Apple Container; the cache at `~/.cache`; the image build dir (load sentinel, GC roots); the layout-version marker; the user config |
| **Per workspace** | everything under `<workspace>/.yolo/home` — the rw overlays, the single-file bind sources, each pack's workspace-scope state dir, the writable-home backing dirs, the venv shadows; on Apple Container, the workspace's tool disk at `/mise` |
| **Per jail (container name)** | the podman home skeletons and each launch's pack tree; container tracking files, the briefing and skills staging tree, the socat log; the keeper's log, liveness lock, start record and session lock |
| **Per host workspace, inside one home** | the agent history file, keyed on a hash of the host workspace path |
| **Per container** (the jail's start to its stop) | `/tmp`, `/run`, `/dev/shm`, the per-launch scratch volumes, PID files, the session records |
| **Host-only, never mounted** | the host's own mise data, and host credentials generally |

**One vendor key in the shared cache is shared across workspaces, and is left that way.** Claude
Code keeps MCP logs under `~/.cache/claude-cli-nodejs/<cwd>/`, where `<cwd>` is its working
directory with each `/` written as `-`. Every container jail's cwd is `/workspace`, so every
workspace on the machine writes into one key,
`-workspace`. Its contents are interleaved MCP logs and nothing else (measured 2026-09-30: four
`mcp-logs-*` directories, 1.8 MB). A per-workspace bind over that one path would be a mount made
for one vendor's cache layout, so none is made
([`WP-D1`](workspace-path-mirroring.md#WP-D1)).

### Why the home root is a read-only skeleton, with symlink hatches

A **skeleton** (a term coined by the design that built it) is a directory holding only the
mountpoints and redirect links a launch's binds need, and no file content. Each fresh podman
launch builds a NEW one (`buildHomeSkeleton`) from THIS launch's selected packs, config and
`host_files` entries, under `<global storage>/agents/<container name>/home/`, and binds it `:ro`
at `/home/agent`. So a pack the jail did not select has no effect inside it: the rule
[`DIR-BH1`](#dir-bh1), *"a non-selected pack can never have an impact"*, has no exceptions.

It replaced ONE machine-wide base, `<global storage>/home`, that every podman jail shared. That
base was a leftover, not a design: it was the one **writable** home every jail shared until it was
made `:ro` on 2026-04-07, which left a shell holding the union of every *shipped* pack's dirs
(it is provisioned before the config is loaded), plus every mountpoint any launch had ever made
there. One workspace's `writable_home_dirs` and `host_files` links, every unselected pack's dirs
and their **legacy bytes** (files left from when the base was the shared writable home) showed up
in every jail, and a jail that did not select claude could read the machine's Claude refresh
token and `oauthAccount` through it, since rootless jail root is the host user and mode `0600`
does not stop that read ([`DIR-BH2`](#dir-bh2)).

The three relative symlinks are the hatch. The root is read-only, but these links resolve
*through the container's mount table* into per-workspace writable overlays, so a tool that
atomic-renames one of those files (an agent rewriting its own `~/.claude.json` constantly) lands
its write in a writable mount. A link whose target dir is not bound in this jail (`.claude.json`
in a jail without claude) dangles and reads as absent, which is the right answer there.

#### What a skeleton holds

- Core's own dirs (`paths.HomeSkeletonCoreDirs`) and the single-file mountpoints
  (`paths.HomeFileMountpoints`). Core's list is `paths.BaseHomeCoreDirs` without the pi pack's
  `.pi/agent`, which would otherwise put a `~/.pi` in every jail; `BaseHomeCoreDirs` keeps it
  only because `internal/basehome`'s legacy walk must treat an old base's empty `.pi/agent` as
  core's.
- The mountpoints for the selected packs' writable and shared dirs.
- The three redirect links (`paths.HomeFileRedirects`).
- Mountpoints for `config.WritableHomeDirs(cfg)`; for `host_files`, the writable-dir
  mountpoints and the symlink links.
- Mountpoints for the selected packs' `files` targets outside a writable dir
  (`packFilesSkeletonEntries`), their skills destinations and their briefing destinations.

**Every bind destination directly under `/home/agent` either exists in the skeleton or sits
inside another bind**, which a test over the golden argv pins. The mountpoints stay even though
podman auto-creates a nested one in every realistic variant measured
([the warning under Backend differences](#backend-differences)): they are cheap and make mode
and ownership deterministic.

#### When a skeleton is built

- **Only on the fresh-launch path, and only for podman**, after config load, after the attach
  decision, under the workspace lock, as one call in `runContainer` after `prepareHostFiles`, so
  both inputs exist. Its result is written straight into the argv input, so the skeleton and the
  argv binding it come from one value. Never on attach. Apple Container and macos-user build none.
- **Fatal, then best-effort, in that order.** Core's dirs, the selected packs' dirs, the
  single-file mountpoints and the redirects fail the launch, naming the path, since podman's own
  error would name neither; every config- and pack-driven entry after them only warns, and those
  warnings reach the launch output. Because the fatal entries come first, a pack entry that lands
  on a redirect name (a pack `files` entry into `.gitconfig`) is the one that fails, as a warning,
  instead of failing every launch. A best-effort file mountpoint that finds a link or a directory
  in its place warns rather than passing over it.
- **A launch that starts no container leaves no skeleton.** A fatal entry removes the builder's
  own partial directory, and every return between the build and the container start (the
  pre-flights that read the assembled argv, a runtime that never started) removes the skeleton
  first (`discardUnheldSkeleton`). No container ever held it, so the rule against removing a
  mountpoint under a live jail does not apply.

#### Where a skeleton lives, and when it goes

<a id="the-three-rules-a-skeleton-obeys"></a>

The shared base avoided three hazards by accident, by being host-only, only ever growing and
never pruned. A skeleton obeys them as rules:

1. **It is built outside the workspace**, under `paths.HomeSkeletonRoot(cname)`
   ([`OQ-BH9`](#oq-bh9)), never in the workspace overlay. `/workspace/.yolo/home` is writable from
   inside the jail (measured), so a skeleton there would be read-only in name only, and the host's
   own `MkdirAll` and link writes would follow links the jail planted. `AgentsDir/<cname>` is
   host-only, keyed per jail, already bound into jails for skills and briefings, and already reaped
   by liveness. `jailcontent.PrepareSkills` clears only the contents of `skills-*` there, so it
   never touches a sibling `home/`.
2. **No mountpoint is removed under a live jail.** A host-side `rmdir` of a mountpoint silently
   detaches the bind inside a running jail (measured in a user+mount namespace). The launch path
   cannot prove that no container of the name is alive: a failed probe reads as "no container", a
   failed stale-container removal can fall through to the fresh path, and the workspace lock is
   fail-open. So a skeleton is **never edited**: an attach builds nothing, and each fresh launch
   builds a new directory, timestamp-prefixed (`os.MkdirTemp`), rather than touching an old one
   ([`OQ-BH10`](#oq-bh10)). Editing in place would need a tri-state liveness probe and a lock that
   reports acquisition, neither of which the launch has.
3. **`<state>/home` stays as the machine store** ([The machine store](#the-machine-store)).

Old skeletons go with the jail's whole `agents/<container name>` entry, through
`PruneOrphanAgentStaging`, which reaps nothing when liveness is unknown, never the launching
jail's own name, keeps every name that is live **or tracked**, and has a one-hour age floor. It
runs from `yolo prune` and from a launch's debounced housekeeping slot; a capture also removes the
entry after it finishes. Two cleanups at the launch's own end make that reaper reach a skeleton:

- **The tracking file goes once the container is known gone.** The tracking file
  (`CONTAINER_DIR/<cname>`, the container-to-workspace map `yolo ps` reads) used to survive every
  normal exit of a `--rm` jail, so a workspace in use kept its entry and gained a skeleton per
  launch. Now each of the three ends of a launch (normal exit, the signal arm, a runtime that never
  started) removes it, only when the runtime answered that no container of the name exists and the
  workspace lock was free (`forgetGoneContainer`, over the tri-state `probeExistingContainer`).
  Each end releases the launch's own lock first, the normal exit included, because the lock's other
  release waits for the container to show as running, and a child that exits before it ever does
  would otherwise find its own lock held.
- **The launch removes its own skeleton** on the same known-gone evidence ([`OQ-BH16`](#oq-bh16)).
  Removing a skeleton whose container is known gone is not the modification
  [`OQ-BH10`](#oq-bh10) forbids. "Could not ask the runtime" removes nothing, so only a launch
  killed with no teardown (SIGKILL, OOM) leaves one for the reaper, and on a machine where one
  workspace is used alone that means `yolo prune --apply`, since the housekeeping slot always keeps
  the launching jail's own name.

> [!WARNING]
> **The "lock was free" half needs `workspaceLock.Close` to unlock explicitly.** The normal exit
> takes the workspace lock in `stopLoopholes` and again in `forgetGoneContainer` right after, and a
> child the launch forked in between (the housekeeping slot's podman calls) holds a copy of the
> first descriptor until its exec. Closing a descriptor does not release a `flock` another copy
> still holds, so on a slow runner the second take failed and the skeleton stayed (CI,
> ubuntu-24.04-arm, 2026-09-28). An explicit unlock releases it for every copy. Do not add a wait
> instead: that puts latency on the quit path to hide a lock this launch had already released.

#### The machine store

The **machine store** (a term coined with the skeleton) is `<state>/home` once it stopped being
mounted at `/home/agent`. It holds the machine-scope shared dirs and the Claude login seed, and
backs the shared dirs' rw binds on both container backends, the seed sync, the OAuth broker's
default credential file and the stranded-credential rescue (`migrateOldOverlay`).
`config.agentWritableTrees` keeps it, because jails still write the shared dirs; the host-built
`:ro` skeleton is not on that list.

`storage.EnsureGlobalStorage` writes into it only the storage dirs, every shipped pack's shared dir
(a bind source, since it runs before config loads) and the credential migration; the home loops,
the single-file mountpoints and the redirects moved to the skeleton builder, and so did
`prepareWsState`'s, `prepareHostFiles`'s and the pack `files` writes. A fresh launch writes nothing
else there, which a snapshot test holds byte-identical.

**Legacy bytes are left in place, unmounted and unread.** Nothing is moved or deleted. The
2026-09-20 rulings for those bytes still bind any future code that touches them: move legacy
runtime artifacts, never delete them, and make any delete path an explicit opt-in. The old union's empty directories
stay too, so a downgrade keeps working. The launch no longer refuses over them, and
`YOLO_ALLOW_LEGACY_BASE_HOME` is gone; `yolo check` keeps a detection-only report
(`internal/basehome`, `basehome.Report.CleanupLines`) whose printed `mv` is optional cleanup the
user runs, never a yolo verb ([`OQ-BH13`](#oq-bh13), [`DIR-BH0`](#dir-bh0)).
`PruneShadowedHome` still empties shadowed seeds and never removes a directory, because a jail
started by an older yolo still mounts `<state>/home`.

#### What pins the skeleton

`integration/homeskeleton_test.go` (`TestPodmanHomeIsAPerJailReadOnlySkeleton`) passed rootful in
a nested jail and **rootless in CI on both arches** on 2026-09-25, its `claude only`, `codex only`
and `writable_home_dirs` subtests all green: `/home/agent` bound `ro`, a write to the home root
failing `EROFS`, `/home/agent` and `/home/agent/.config` statting `0:0 755` (jail root maps to the
host user who built the skeleton), and a `writable_home_dirs` bind writable inside the `:ro`
skeleton. Its `codex only` subtest plants a machine login in a machine store **private to the
test** (the harness's isolated home links the whole state dir back to the machine's, so a plant
through it would overwrite the developer's real login) and searches all of `~` for it. At the unit
level: `TestEveryHomeBindHasASkeletonEntry`, `TestRunContainerBuildsTheSkeletonOnTheFreshPath`,
`TestTheSkeletonCarriesOnlyTheSelectedPacksDirs`, `TestCoreSkeletonDirsNameNoPacksDir`,
`TestEachFreshLaunchGetsANewSkeleton`, `TestAttachLeavesTheSkeletonByteIdentical`,
`TestAFreshLaunchLeavesTheMachineStoreByteIdentical`,
`TestOnlyTheListedFunctionsNameTheMachineStore` and the tracking-cleanup tests in
`internal/cli/run/trackingcleanup_test.go`.

> [!NOTE]
> A nested jail's skeleton lives under the outer jail's home, which the outer agent can write.
> The outer jail is that nested jail's host, as for every other host-side input.

### Reserving a name is not creating a directory

Two different things have been spelled with the same list, and they are separate:

- **Creating** a directory is what the skeleton builder and the machine store do.
- **Reserving** a name is a config-validation rule that creates nothing: a
  `writable_home_dirs` entry may not claim a first segment yolo already manages, and a
  `host_files` destination under a dir that is already bound read-write gets no staging.

Reservation covers the **selected** packs only, by the maintainer's ruling
([`OQ-BH14`](#oq-bh14)), and so does the skeleton: an
unselected pack is treated as if it does not exist. (The machine store is the exception on the
creating side, below.) So `writable_home_dirs: [".codex"]` passes in a workspace that does not
select codex and is refused once it does, naming the pack; one user-scope entry can be legal
in one workspace and refused in another, and that is accepted. A `host_files` entry under
`~/.codex/` in a claude-only jail is staged as an ordinary new top-level dir. The launch
hands its staged set to the reservation (`HostFileEntry.StagingFor` and its three readers: the
workspace-overlay half, the skeleton, the argv); validation, which runs before staging,
resolves the same selection from the user config and the pack store, `needs` closure included
(`resolveSelectedPacks`). It resolves with `packsrc.Store.ResolveExisting`, so validation
writes nothing into the pack store; a configured pack the store cannot resolve reserves
nothing, because staging refuses that launch anyway; and a configured pack with an
`only`/`exclude` filter is loaded from a copy staged through that filter, since the filter can
drop the manifest. A `host_files` destination under a selected pack's **shared** dir, or under
a `writable_home_dirs` entry, is already under a read-write bind and is not staged again: a
second bind at one destination is one podman refuses.

Two things are not pack names and stay reserved in every workspace: core's own dirs and
files, and `.claude` as a `writable_home_dirs` segment, because core's `~/.claude.json`
redirect targets `.claude/claude.json`.

**Only the machine store's shared-dir creation reads every shipped pack.**

- **The machine store's directories.** `EnsureGlobalStorage` creates every shipped pack's
  shared dir in `<global storage>/home`, because it runs before the config is loaded. That
  makes a bind source, which a jail mounts only when it selects the pack. The fresh launch
  then creates the selected packs' own, on both container backends, so a configured pack's
  shared dir has a source too (`ensureSharedDirSources`).
- **The `host_files` surface reservation**, in contrast, covers only the SELECTED packs' surfaces, plus core's
  own ([`OQ-BH15`](#oq-bh15), built 2026-09-25; `selectedSurfacePaths`): a destination
  such as `~/.codex/config.toml` is refused only when a selected pack composes it. A collision
  between two selected writers is still refused at launch (`config.SurfaceCollisions`).

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

For Claude, two launches change what the link carries. Since
[CL-D22](../design/claude-login-without-interception.md#CL-D22) every jail launch points
Claude's credential store at the shared directory itself, so Claude opens the real file there and
reads nothing through the link. A launch that opts into the credential view
(`YOLO_CLAUDE_CREDENTIAL_VIEW=1`) skips Claude's hook altogether: the broker writes a regular
file at that path, and a link would put the machine's refresh token back in front of Claude
([`agent-credentials.md`](agent-credentials.md#the-claude-oauth-broker)).

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
workspace on the machine reads and writes one store. No shipped pack declares it. `packs/pi`
did, for `~/.pi/agent/npm` → `~/.pi-shared-npm`, its extension package store, from 2026-09-21
([`OQ-1`](pi-extensions.md#why-its-this-way)) until 2026-10-05, when one jail's
refresh rewriting the prefix every pi jail loaded from ended it
([XB-D14](../design/pi-extension-store-builds.md#XB-D14)): pi's `unshare_directory` hook now
replaces the link with the workspace's own directory, and a fresh launch on a container backend
that finds `~/.local/share/yolo-jail/home/.pi-shared-npm` still there says it can be deleted.

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
whole home is that directory. Apple Container synced the podman path until the
[`OQ-BH12`](#oq-bh12) fix, so the seed never reached one of its jails. The host-side paths and
link defenses are pinned by `internal/cli/run/acseed_test.go`
(`TestAppleContainerSeedReachesTheJailsClaudeJSON`, `TestAppleContainerLoginIsLearnedByTheSeed`,
`TestTheSeedDoesNotWriteThroughAJailPlantedLink`) and `internal/storage/claudejsonlink_test.go`;
the forward allowlist by `TestSeedForwardsOnlyTheLoginKeys` and
`TestAFilePlantedInTheMachineStoreReachesNoWorkspace`.

**Seed evidence, reconciled 2026-10-07 against `931489400b`.**
[TestAppleContainerFreshWorkspaceBootsWithTheLoginSeed](../../integration/applecontainerhome_test.go)
recorded `AC-PARITY login-seed VERDICT: HOLDS` on the self-hosted Mac in
[run 37133569003](https://github.com/mschulkind-oss/yolo-jail/actions/runs/37133569003),
2026-10-03, at `5ca9b74856b1a181e91b0c30022f578382d54204`. The fake account arrived in
both the host copy and the jail's `~/.claude.json`; `~/.claude` existed and the undotted
`~/claude` and `~/npm-global` did not. The test uses a private machine store, starts no
agent and makes no authentication request; the entrypoint adds workspace settings to the
delivered JSON, so their presence is not evidence that the seed forwarded non-login keys. This is
**fake-seed delivery and layout**, not proof that Claude is logged in. Real authentication and reverse learning after a
real login remain UNMEASURED on that backend.

> [!WARNING]
> **Historical HOLDS is not current target acceptance.** That experiment accepted either
> verdict. Since 2026-10-09 the login-seed check fails on a bad verdict, with offline controls
> (a known-good result, a missing jail email, each wrong directory fact, malformed or missing
> probe facts, a host-copy read error) and a structural test pinning its native caller's
> hard-failure branch. Those promote the source regression; they do not run or prove the Apple
> Container boot, and neither they nor the historical observation proves authentic login.

<a id="the-owed-apple-container-seed-gate"></a>**Owed: the native seed regression check.** On a
native Apple Container host, mutate the actual container seed target/copy caller, run
`TestAppleContainerFreshWorkspaceBootsWithTheLoginSeed` and observe its specific failure, then
restore the source and observe it pass. Linux tests cannot stand in for that target result, and
it needs no agent or API call. Real Claude authentication and reverse learning after a real login
stay UNMEASURED on that backend beside it. The [roadmap](../plans/roadmap.md) carries the item.

### History isolation

The agent's history file is symlinked to a per-host-workspace file named by a hash of the
host workspace path, driven by a pack's `per_jail_history` hook. This is belt and braces:
even where a state dir is shared across workspaces — Apple Container's single writable home
— history stays distinct per host workspace.

## Storage classes and the durable dir

Every briefing tells the agent which of its paths outlive the jail, and gives it one durable
place with a name. That exists because an agent put its landing worktree in `/tmp`, a restart
deleted it, and the next command ran its cherry-picks in the workspace on `main`, ungated: the
briefing said only that the home was *"persistent across sessions"* while most of it was
read-only, so a failed write sent the agent to `/tmp`.

### Storage-class terms

- **Storage class** — a kind of location, defined by what survives which event, who shares it,
  what yolo cleans up, and which disk holds it. It classes **locations**, not the bytes at a
  vendor path (state, cache or the user's own), which is a different question.
- **Durable** — survives the jail's exit and the next launch of the same workspace. It does not
  mean backed up, and it does not mean shared across workspaces.
- **Per-launch** — created for one fresh launch, shared by every terminal that attaches to that
  jail, and deleted after the jail exits. In yolo a restart *is* a fresh launch: a jail has no
  in-place restart.
- **Storage-class map** — the list of paths a launch makes writable, each with its class,
  computed from the inputs that build the mount argv. It is a view of the mount plan, not a
  second source of truth. The code calls it the **persistence map** (`jailcontent.PersistenceMap`,
  built by `persistenceMapFor` in `internal/cli/run`).
- **Durable dir** — `<workspace>/.yolo/durable`, the one agent-neutral durable directory,
  exported as `$YOLO_DURABLE_DIR`. It is not called "scratch" because **scratch volumes** already
  names the per-launch mounts ([`perf-logging.md`](perf-logging.md#the-linger-was-the-scratch-volumes)).
- **Durable worktree** — a git worktree registered with the workspace's repository whose
  directory lies beneath the durable dir. It is what the launch line counts.

All but "durable" and "per-launch" were coined by the design that built this.

### Four principles

- <a id="DS-P1"></a>**DS-P1. The briefing describes the mounts the launch made, not a
  hand-written summary of them.** A hand-written line had already drifted; only a list derived
  from the launch's own inputs stays true when a pack adds a writable directory.
- <a id="DS-P2"></a>**DS-P2. The durable place belongs to no agent.** Core does not know what an
  agent is. A hand-made worktree, a landing tree or a build any agent needs next session goes to
  one path every agent is told about. Where an agent's own tool already has a durable default, the
  tool keeps it, and the briefing recommends it to no one else.
- <a id="DS-P3"></a>**DS-P3. `/tmp` stays per-launch, and the briefing says so.** The fix is the
  location, not a check before each use.
- <a id="DS-P4"></a>**DS-P4. yolo reports what accumulates and never deletes your work.** Only
  the agent that made a worktree knows whether it holds unlanded work; yolo keeps the growth visible
  ([`OQ-DS2`](#oq-ds2)). The one removal is of worktrees a pack's tool made and left there: clean,
  with nothing hidden from status, on a branch (which, with its commits, stays), unused and idle
  for a day
  ([the tool-managed worktree reaper](disk-footprint.md#tool-managed-worktrees)). The maintainer's
  2026-10-24 request put that reaper in [`OQ-DS2`](#oq-ds2)'s place for those trees.

### The storage classes

The persistence map has five classes (`jailcontent.PathClass`), and the briefing renders the
first four, plus the rest of the home as read-only:

| Class | podman | Apple Container | macos-user |
| :--- | :--- | :--- | :--- |
| **per launch** | `/tmp`, `/var/tmp`, `/var/lib/containers`, `/var/cache/containers` (scratch volumes), `/run`, `/dev/shm` | the same set, `tmpfs` in the VM's RAM | none |
| **per workspace** | `$YOLO_DURABLE_DIR`; the pack-bound home dirs from `<ws>/.yolo/home`; the per-side shadows | `$YOLO_DURABLE_DIR`; **all of `/home/agent`** | `$YOLO_DURABLE_DIR`; the account home's links into `<ws>/.yolo/home` |
| **every workspace on this machine** | `~/.cache`, `/mise`, the shared-credential dirs, each machine-scope pack dir | nested binds from the same store | the account home's real directories |
| **the workspace itself** | `/workspace` | `/workspace` | its real path |
| **internal** | `~/.yolo/bin`, the single-file binds (logs, a lock, the CA bundle, shell history) | | |
| read-only | the rest of `/home/agent`, staged briefings and skills, `/ctx`, `/opt/yolo-jail`, `/nix/store` | rewritten at each launch | the staged skills and briefings, write-denied by Seatbelt |

<a id="DS-D15"></a><a id="DS-D7"></a>

**Internal** holds yolo's own writable mounts; it is in the map so the pin covers them, and the
section never names them, saying instead that the read-only home has *"a few files yolo keeps
there itself"* ([DS-D15](#DS-D15)). The per-launch volumes are deliberately not durable
([DS-D7](#DS-D7)): podman silently reuses a named volume that exists, so a durable `/tmp` would
hand a relaunch the last session's sockets and locks and the nested podman store, with no
liveness signal to reap it by. Pointing `TMPDIR` at the durable dir is rejected for the same
reasons; tools assume `TMPDIR` is cleaned for them.

On **macos-user** there is no per-launch class: `/tmp` is the machine's own `/private/tmp` and
`/var/folders`, which survive the launch, are shared with the host user and every workspace, and
are cleared at reboot (inferred, not measured), so a name there must be unique (`mktemp`). That
inverts the podman risk: a `/tmp` worktree there survives a restart and collides instead. At the
**host notch** (`yolo host -- <agent>`) the briefing gets one static sentence, that `/tmp` is the
machine's own and may not survive a reboot, and no variable ([`OQ-DS3`](#oq-ds3)).

| Class | Shared by | yolo cleans up |
| :--- | :--- | :--- |
| per launch | the terminals attached to one jail | everything, after the jail exits; stragglers at the next launch or `yolo prune --apply` |
| per workspace | every agent and terminal in this workspace's jail, and the host user | nothing; `yolo prune` may hardlink-deduplicate identical files in the pack-bound dirs, and deletes nothing in the durable dir |
| every workspace on this machine | every workspace's jail | `~/.cache` subdirs by age (`PurgeCacheByAge`), masked seeds (`PruneShadowedHome`) |
| the workspace itself | every jail and host process on the project | nothing |

The guidance the section gives follows: source and anything to commit in the workspace, never a
worktree there; worktrees, landing trees and long builds' outputs in the durable dir; nothing of
the agent's own in another tool's directory; regenerable caches only in the machine-wide class,
through each tool's own cache setting; throwaway files only in the per-launch class, never a
worktree; and **secrets nowhere the agent chooses**, since credentials reach a jail through
yolo's own channels and a copy the agent writes is one nothing rotates or revokes.

### The storage-class briefing section

<a id="DS-D1"></a><a id="DS-D13"></a><a id="DS-D14"></a><a id="DS-D16"></a><a id="DS-D17"></a>

"Storage classes: what survives a restart" is core's, rendered by `jailcontent` from the
persistence map after the Environment section and before the Loopholes section, into every
selected pack's briefing destination, attach included. The map is a pure function of the
launch's runtime, `ephemeral_storage`, the selected packs' writable and shared dirs, the
workspace and the notch, and reads the definitions the argv reads (`prune.ScratchSlots`,
`paths.HomeSurfaces`, `packload.WritableDirs`, `packload.SharedDirs`), never a second list. A
unit test compares the map against the assembled golden argv per backend in **both directions**,
and checks each mount's class against its argv source, so adding a writable mount without the map
knowing fails the gate ([DS-D1](#DS-D1), [DS-D17](#DS-D17)). The map leaves out a `host_files`
entry's staged parent dir, a loophole's own binds and the nix daemon socket, none of them a place
for work ([DS-D14](#DS-D14)).

The section is **agent-neutral** by the maintainer's ruling ([DS-D13](#DS-D13): *"we can't have
a claude-only solution … we need to be clear about lifecycles, storage profiles, whatever. and
give guidance"*), names no agent's directory, and recommends no location but the durable dir. The
Environment section's Home line points at it per backend: podman *"mostly read-only"*, Apple
Container *"writable, and kept for this workspace"*, macos-user *"one account home, shared by
every workspace"*; a caller with no map gets no parenthetical, so no input can produce the old
*"persistent"* claim ([DS-D16](#DS-D16)).

<a id="DS-D18"></a><a id="DS-D29"></a><a id="DS-D31"></a><a id="DS-D32"></a><a id="DS-D34"></a><a id="DS-D35"></a>

What it says, and why each choice stays (the rendered text is pinned in
`internal/jailcontent/persistencesection_test.go`):

- **It leads with the answer**: `$YOLO_DURABLE_DIR` and its path, then the worktree paragraph,
  then the classes ([DS-D18](#DS-D18)). An agent given only the classes and what vanishes put its
  clones in `/tmp`, and corrected, chose `~/.local`. With no durable dir the lead says why and
  names no path.
- **What the dir is for, in the maintainer's words** ([DS-D35](#DS-D35)): scratch space for what
  the agent does not want a restart to wipe; not part of the project; the user never needs to look
  in it; anything meant for the project or the user goes where it otherwise would.
- **Any layout**, with worktrees in `worktrees/<task>`; a harness's `/tmp` instruction does not
  outrank it; and the home dirs are the agents' and tools' own state, never for work
  ([DS-D29](#DS-D29)).
- **Its lifetime is said once** ([DS-D31](#DS-D31)): it survives restarts, and yolo never deletes
  your work in it, the one removal being clean, idle worktrees a pack's tool left there, which that
  pack's briefing names ([DS-P4](#DS-P4)); it lives in `.yolo`, *"which git ignores"* ([DS-D32](#DS-D32), not "hidden from git", which
  read as "git cannot touch it"), so a `git clean -fdx` (or `-fdX`) in the workspace deletes it,
  and the agent never runs one there. It says *"survive a restart"*, not "next session", since a
  new session in the same launch still sees `/tmp`.
- **Apple Container's wording is chosen by the map's two facts, never by the backend's name**
  ([DS-D34](#DS-D34)): a per-launch set in RAM is gone when the jail stops and uses the jail's
  memory (so podman under `ephemeral_storage: "tmpfs"` says that too), and a whole-home bind is
  writable and kept in `.yolo/home`, where yolo deletes nothing but its own files. The read-only
  bullet becomes *"Rewritten at each launch"*, since there a write may succeed and lasts only until
  the next launch.
- **A nested jail whose workspace lies in the enclosing jail's scratch** gets a caveat that
  replaces the lifetime sentence rather than following it, and an attach carries the caveat too,
  since it rewrites the briefing ([DS-D21](#DS-D21), [DS-D32](#DS-D32)).

### The durable dir

<a id="DS-D2"></a><a id="DS-D6"></a><a id="DS-D8"></a><a id="DS-D9"></a><a id="DS-D21"></a><a id="DS-D30"></a>

- **Path.** `<workspace>/.yolo/durable`, reached through the workspace's own mount at its in-jail
  spelling: `/workspace/.yolo/durable` on the container backends, the real path on macos-user.
  It has **no mount of its own** ([DS-D8](#DS-D8)): the same relative geometry on the host and in
  the jail is what keeps a relative worktree path valid on both sides, and a fixed alias such as
  `~/durable` would break that and add a punch to the skeleton. It lives inside the workspace, not
  in the machine store, because the per-workspace home already does, and so it goes with the
  workspace when the workspace is deleted ([DS-D9](#DS-D9): *"agent home is in the workspace as
  well, so that's a weird constraint to have"*). `.yolo` is the one writable, per-workspace place
  every jail backend already has, with no new mount and no new Seatbelt rule.
- **One writer of the directory itself: the launcher** (`ensureDurableDir`, over `durable.Ensure`),
  on every fresh launch and every jail backend, beneath a root on `.yolo` opened with
  `paths.OpenStateDirRoot`, after `EnsureWorkspaceStateDir` has made `.yolo` and its ignore file.
  An existing directory is left as it is; its contents belong to the agents ([DS-D2](#DS-D2)).
- **The variable.** `YOLO_DURABLE_DIR` holds the absolute in-jail path and is set in the
  environment every process in the jail inherits, **only when the directory exists at launch**,
  never at the host notch ([`OQ-DS1`](#oq-ds1), [`OQ-DS3`](#oq-ds3)). An attach reads it from the
  running jail's frozen environment and makes nothing.
- **No exclude writes.** `.yolo/.gitignore`'s `*` already hides it, so yolo writes no repository
  `.gitignore` or `.git/info/exclude` entry ([DS-D6](#DS-D6)). `git status`, `git add -A`, `rg`
  and `fd` skip it (measured, `rg --hidden` and `fd -H` included), and Go and pytest skip
  dot-directories. What does not: `git clean` with both `-d` and `-x` or `-X` (measured: without
  `-d` a clean does not reach `.yolo`); a walk told to ignore nothing (`fd -HI`, `rg -uu`,
  `find`, `du`); and watchers, indexers and backup tools that keep their own exclude lists.
- **Failure never refuses the launch** ([durable-dir failure paths](#durable-dir-failure-paths)).

<a id="DS-D3"></a>

> [!WARNING]
> **Host code never follows a link, reads a file's content, or removes anything beneath the
> durable dir** ([DS-D3](#DS-D3)). Its only contact is the report's `lstat` walk and git's admin
> files, read as bytes beneath an `os.Root`, and it **runs no `git` in the workspace**: the
> workspace's `.git/config` is jail-writable and git executes programs a config names
> (`core.fsmonitor` runs on `git status`), so yolo running git there would run jail-chosen code as
> the host user. Every reader opens the dir through `durable.Open`, which refuses a link at `.yolo`
> as well as at `durable` ([DS-D24](#DS-D24)). `yolo prune` never deletes from it.
>
> The one deletion beneath it is the jail's own, never the host's: the pi pack's reaper of the
> worktrees pi-subagents left in the directory that pack chose
> ([tool-managed worktrees](disk-footprint.md#tool-managed-worktrees)).

### The durable worktree convention

<a id="DS-D10"></a><a id="DS-D28"></a>

A hand-made worktree goes to `$YOLO_DURABLE_DIR/worktrees/<task>`, named for the work, made with
**`git worktree add --lock`** and driven with `git -C <path>` rather than `cd <path> && …`, so a
missing tree fails the command instead of running it in the workspace ([DS-D10](#DS-D10)). On the
container backends the briefing says why beside the command, in the words [DS-D32](#DS-D32)
settled: git records it under this jail's `/workspace`, a path the host does not have, so without
the lock a `git worktree prune` or `git gc` on the host would drop its registration. On macos-user,
where the host and the jail spell the workspace alike, it asks for the lock without that reason.
Removal is the creator's: `git worktree unlock <path> && git worktree remove <path>`, which refuses
while the tree holds uncommitted changes; `git worktree remove -f -f <path>` is named only as the
form that discards them, since the doubled `-f` overrides that refusal as well as the lock
([DS-D28](#DS-D28)). Two agents choosing one name get git's own `already exists`.

<a id="DS-D5"></a>

A tool that already has a durable default keeps it: `.claude/worktrees/`, `~/.codex/worktrees`,
`.pi/worktrees`, `~/.local/share/opencode/worktree` ([DS-D5](#DS-D5)). The briefing offers none of
them as the pattern for hand-made worktrees.

#### A worktree records absolute paths

git writes both worktree links as absolute paths by default, the repository's
`.git/worktrees/<id>/gitdir` and the worktree's `.git` file, so a worktree made in a container jail
names `/workspace/…`, which the host does not have. Measured with git 2.55.0, a repository moved to
another absolute path (what the host sees):

| Made with | `git worktree prune` at the new path | `git` inside it | `git clean -fdx` |
| :--- | :--- | :--- | :--- |
| plain `git worktree add` | **drops its registration** | `fatal: not a git repository` | **deletes the directory** |
| `--lock` | keeps it | fails the same way | **deletes the directory** |
| `--relative-paths` | keeps it | works | skips it |

The branch's commits live in the main repository's refs and survive a pruned registration; the
worktree's uncommitted files survive on disk but lose their index and `HEAD`.

> [!WARNING]
> **`--relative-paths` is the user's opt-in, never yolo's default, and the briefing does not name
> it.** It fixes all three columns, but it sets `extensions.relativeWorktrees` and raises the
> repository format version, and git 2.47.2 then refuses `status`, `log` and
> `git config --local --unset` on the whole repository. Removing the worktree leaves the extension
> set, it lands in the user's own `.git/config` through the live workspace bind, and an agent
> cannot see which gits the user runs on the host. It is repairable,
> `git config -f .git/config --unset extensions.relativeWorktrees` working even from the older git
> (measured), but the user meets a broken repository first and has to know that repair. On
> macos-user and at the host notch the workspace is at its real path, so this fault does not arise
> there. Mounting the workspace at the host's path is
> [`workspace-path-mirroring.md`](workspace-path-mirroring.md)'s question.

### The durable-dir report

<a id="DS-D11"></a><a id="DS-D19"></a><a id="DS-D20"></a><a id="DS-D22"></a><a id="DS-D24"></a><a id="DS-D25"></a><a id="DS-D26"></a><a id="DS-D27"></a><a id="DS-D36"></a>

yolo never deletes a worktree you made in the durable dir, so it makes them hard to forget
([`OQ-DS2`](#oq-ds2)). The worktrees a pack's tool made and left go by
[the tool-managed worktree reaper](disk-footprint.md#tool-managed-worktrees):

- **The launch line**, computed by the in-jail boot (`entrypoint.ReportDurableDir`; the bootstrap
  on macos-user) on every fresh launch and printed with the boot's other lines, so `launch.log` and
  `boot.log` carry it: worktree count, total size, and the oldest idle worktree by name, with a
  pointer to `yolo check` and the reminder that yolo deletes none of them but clean worktrees a
  pack's tool left idle for a day ([DS-D11](#DS-D11)).
  Nothing prints for an empty dir. A second line counts **prunable registrations** whose path lies
  below the workspace or the per-launch set, which is the `/tmp` incident class caught at the next
  launch; it sends the reader to `git worktree prune -n -v`, never a bare prune, because inside a
  container jail git calls every host-made worktree prunable ([DS-D20](#DS-D20)). A registration
  elsewhere is not judged, since a container jail cannot see a worktree the host made beside the
  repository, and the host's spelling (`YOLO_HOST_DIR`) and the workspace's own resolved spelling
  are aliases of `/workspace` ([DS-D25](#DS-D25)). On the container backends the walk runs in the
  main process's boot alone; every session's pass, the first included, skips it, declared as a
  session-pass exclusion (`notSessionPass`), so an attach never repeats the line
  ([DS-D36](#DS-D36); measured 0.14 s on a boot pass over 100,000 files, about 1 µs on a session's).
- **The figures.** Count, branch, lock and idle come from git's own admin files under
  `.git/worktrees`, read beneath an `os.Root` with no link followed, never from running git on the
  launch path, where the macos-user bootstrap's `/usr/bin/git` could be the stub that asks to
  install the developer tools ([DS-D19](#DS-D19)). Idle is the newer of a worktree's admin `HEAD`
  and `index` mtimes. Size is the `lstat` total of the whole dir, following no link, under a
  wall-clock budget. A walk that finishes records its total in `.yolo/durable-size.json`, beside
  the dir; a stopped one prints the last recorded total and its age, with its own `≥` floor
  labeled as not the total, and starts `yolo-jaild durable-size` detached, a walk with no budget at
  the lowest CPU priority that records the next total, so the boot never waits on it (a 410 GB dir
  printed *"≥ 12.9 GB"* before). The record is written and read only in the jail; host reports
  measure for themselves ([DS-D3](#DS-D3)). A workspace that is not a git
  repository counts directories under `worktrees/`. None of this refuses or delays the launch past
  the budget.
- **`yolo check`** adds a Durable dir section listing each worktree with size, idle, branch,
  **unique commits** (commits reachable from its `HEAD` and from no other local branch or
  remote-tracking ref: what deleting the worktree and its branch would lose) and changed files,
  plus other entries and prunable registrations, and a footer naming the one command that deletes
  the dir and the removal advice. On the host the last two columns print *"in-jail only"*. Its
  walks and git calls share one budget, a column past it reads *"not computed"*, and one scan
  reads a bounded number of registrations and says when it stopped ([DS-D27](#DS-D27)).
- **`yolo stores`** adds a *workspace durable dirs* section, one row per workspace, with verdict
  `human` and no reclaimer. ⚠ It covers only workspaces the runtime still lists a container for,
  and jails run `--rm`, so a workspace whose jail has exited gets no row; yolo keeps no durable
  list of workspaces, and the launch line and `yolo check` are the views that never miss one
  ([DS-D22](#DS-D22)). Both print only for a durable dir that exists.
- **Jail-chosen strings** (entry names, registration paths, branches) pass through
  `durable.Printable`, which drops control and bidi-format characters, and the `yolo stores` note
  escapes rich markup, so a directory name cannot drive or restyle the host terminal
  ([DS-D26](#DS-D26)).

### Each agent's own pack

<a id="DS-D4"></a><a id="DS-D23"></a><a id="DS-D33"></a>

The section and the variable reach every agent, and neither names one. Where an agent's workflow
tool must be pointed at the dir, **its own pack does it**, never core:

- **pi**: pi-subagents defaults its worktrees to `os.tmpdir()`, the per-launch class, which makes it
  the one shipped workflow tool that needed a change. The pack's extension
  (`packs/pi/home/.pi/agent/extensions/yolo-durable-worktrees.js`) sets `worktreeBaseDir` in
  `~/.pi/agent/extensions/subagent/config.json` to `$YOLO_DURABLE_DIR/worktrees/pi-subagents` when
  that is unset and the variable is set ([DS-D4](#DS-D4), [DS-D23](#DS-D23)). Not a pack `env`
  value, which is literal and cannot spell a per-backend workspace path, and not
  `PI_SUBAGENTS_WORKTREE_DIR` in the process environment, which every child process (a test runner)
  would inherit. Moving them out of `/tmp` also moved them out of the one class a restart cleans,
  so the same extension bounds them: at most once an hour it starts `yolo-jaild reap-worktrees`,
  on the worktree directory in effect when it lies beneath the durable dir. That removes the
  clean, unlocked, unused `pi-worktree-*` trees idle past a day whose HEAD is a branch, with no
  edit hidden from status and no git operation in progress, keeping the branch, and keeps and
  lists every other one
  ([tool-managed worktrees](disk-footprint.md#tool-managed-worktrees)).
  pi-dynamic-workflows hard-codes `<repo>/.pi/worktrees`, which survives and shows as untracked;
  only an upstream setting can move it.
- **claude**: Claude Code's own worktrees stay in `<repo>/.claude/worktrees/` ([DS-D5](#DS-D5)).
  Redirecting them costs more than it buys: a `WorktreeCreate` hook loses Claude Code's cleanup
  sweep, `.worktreeinclude` and transcript-follows-worktree, Claude Code prompts at the host notch
  to enter a path outside `.claude/worktrees/`, and it refuses a symlinked `.claude/worktrees`
  outright. yolo writes no exclude for it: where it is git-ignored, Claude Code wrote the exclude.
- **codex** and **opencode** keep their workspace-durable defaults; copilot, agy and omp have no
  workflow worktree tool yet.

The claude and pi worktree text is each pack's own skill, `yolo-claude` and `yolo-pi`, named by a
pointer in the pack's `briefing/` ([BD-D2](agent-briefings.md#BD-D2)); both are addressed to that
agent alone (`"agents": ["claude"]`, `["pi"]`), since an unaddressed `briefing/`
file broadcasts to every agent and would hand one agent's tool facts to all of them
([DS-D33](#DS-D33)). Each text holds at both notches and in a launch with no durable dir: it names
a path under `$YOLO_DURABLE_DIR` only in a sentence conditioned on the variable being set, tells
the agent to ask the user where the section says there is none, and puts the user's or project's
own place for a hand-made worktree first. Pinned by
`TestEachAgentPackShipsItsOwnWorktreeProseToItsOwnAgent` and
`TestShippedAgentPacksBriefOnlyTheirOwnAgentAboutWorktrees`.

> [!WARNING]
> **The addressed lines open a version-skew window that is not ruled.** An entrypoint built before
> the audiences field refuses a briefing line with no `into`, which fails the boot. The window is a
> host `yolo` built from this tree paired with such an entrypoint: `just install` and a release
> each ship both halves together, and the source-skew gate catches a from-source launch only when
> the checkout contains the binary's commit. Measured, every such old reader already refuses the
> claude and pi manifests for other reasons, so the lines widen no window today. Whether a shipped
> pack may use the addressed shape at all is
> [`OQ-D6`](../design/slots-and-contributions.md#OQ-D6), open in that design.

> [!WARNING]
> **pi-subagents ends every worktree run with a plain `git worktree prune`**, which in a container
> jail drops the registration of every **unlocked** worktree whose recorded path is the host's.
> Worktrees made by the section's convention are locked and kept; one made on the host beside the
> repository, or in its `.claude/worktrees` at the host notch, is exposed unless locked. Nothing in
> yolo closes this; the remedy is upstream, pruning only the run's own registrations.

### Durable-dir failure paths

| Failure | What happens | Who finds out |
| :--- | :--- | :--- |
| `.yolo` is a symbolic link | `OpenStateDirRoot` refuses; no dir, no variable | the briefing and the launch line name the reason |
| a `workspace_readonly` entry is the durable dir or an ancestor (`.`, `.yolo`, `.yolo/durable`, compared lexically) | none is made; no variable | both say durable space is unavailable because the workspace is read-only there |
| `mkdir` fails otherwise (disk full, permissions) | no variable; the launch continues | both name the error |
| an attach to a jail started without one | `durable.Check` names a cause still present, else says a fresh launch tries again; nothing is made ([DS-D30](#DS-D30)) | the attach's briefing |
| macos-user `--dry-run` | no directory is made; it checks and describes the path a real launch would export | the dry run |
| the jail later deletes or replaces the dir, or leaves a link in it | nothing; the host follows nothing beneath it | the agent at its next write; the next launch's line |
| the workspace itself sits in an enclosing jail's per-launch mount (a nested jail under `/tmp`) | the dir is made, and is only as durable as that mount | the section's caveat ([DS-D21](#DS-D21)) |

### What the durable dir does not cover

- **Backups.** Durable means the next launch, not a copy.
- **Sharing work between workspaces.** The dir is per workspace; the machine-wide class is for
  caches.
- **Reclaiming `.claude/worktrees`, `.pi/worktrees` or `~/.codex/worktrees`.** Those are their
  agents'; the report counts only the durable dir.
- **The Claude Code harness's scratchpad under `/tmp`.** yolo cannot move it; the section says
  what it is.
- **A nudge at the moment of the mistake**, a `git` shim warning on `worktree add` into `/tmp` or a
  hook. A shim named `git` sits in front of every git call every agent makes, and a hook needs yolo
  to own `core.hooksPath` in someone's repository; revisit only if the section demonstrably fails
  to change the behavior.

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
whose tee is the first write under `.yolo`. It names the link and where it points, the `rm` that
clears a link the jail left, and, for a directory the user moved there on purpose, the
`rm` and `mv` that move it back, since `rm` alone would leave the next launch an empty directory.
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
`config-assembled.json` and `config-boot.json` (`writeWorkspaceSnapshot`); and the scratch
remover's in-flight lock, `scratch-rm.lock`. The one writer that may outlive its workspace, the
detached scratch remover, writes its `housekeeping.log` line through
`OpenExistingWorkspaceStateFile` instead, which never creates `.yolo` or the workspace above it
([SV-D7](perf-logging.md#the-linger-was-the-scratch-volumes)). The two logs' trim
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
credentials had the broker serve their access token to every jail that asked.
`internal/oauthbroker/credslink_test.go` covers the reads. The write is a read-modify-write, because
since [CL-D22](../design/claude-login-without-interception.md#CL-D22) the file is also Claude's own
store ([CL-D25](../design/claude-login-without-interception.md#CL-D25)):
`claudeview.UpdateSharedFile` opens a root on the directory refusing a link at it, takes Claude's
lock there by making the directory `.storage-write.lock`, reads the current bytes as a regular file
only (a link at the name reads as absent), and renames a fresh `O_EXCL` temp file over the name,
which replaces a link rather than following it. The lock is jail-writable state the broker handles
while it holds `refresh.lock`, so it is never followed, a stale one is broken by removing that one
empty directory and nothing inside it, and every wait for it ends at `StorageLockWait`, after which
the write goes ahead without it: a lock a jail made unremovable, or plants back each time it is
broken, would otherwise hold every jail's refresh on the machine (`acquireStorageLock`).
`internal/claudeview/sharedfile_test.go` and `internal/oauthbroker/sharedfilewrite_test.go` cover
the write and the lock.

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

<a id="oq-jh1-background"></a>**Background to [`OQ-JH1`](#OQ-JH1), below.** Two facts added 2026-09-30, when this was triaged and left open. A bind mount is not free: on
Linux it needs root (a `sudo mount --bind` or an `/etc/fstab` line), and macOS has no bind mount
without a third-party filesystem such as bindfs, so under (a) a Mac user has no supported way to
move the directory short of moving the whole workspace. And (c) runs against the standing rule
that an escape hatch is for a broken user configuration, never for yolo's own safety check, which
leaves (a) or (b) as the real choice.

- 💬 <a id="OQ-JH1"></a>**[`OQ-JH1`](#OQ-JH1) — may a user relocate `.yolo` or `.yolo/home` with a symbolic link?** The launch now
  refuses one, with no override, because it cannot tell a link the user made from one a jail made.
  A user who moved the overlay to another disk that way (the machine-store directories are
  routinely large) is refused on the next launch. Options:

  - (a) keep the refusal and document a bind
    mount as the way to relocate, since a mountpoint `Lstat`s as a directory;
  - (b) accept a link whose
    target is recorded host-side, outside every jail-writable directory, as the user's;
  - (c) add a
    `YOLO_ALLOW_*` hatch.

  Two facts added 2026-09-30 bear on (a) and (c): [the background above](#oq-jh1-background).

  <!-- vantage: question id=OQ-JH1 leaning="(a): keep the refusal and document a bind mount as the way to relocate. A hatch would be for yolo's own safety check rather than a broken config, and (b) adds a host-side record for a layout nothing documents." -->

  _Leaning:_ **(a)**, keep the refusal and document a bind mount as the way to relocate. A hatch
  would be for yolo's own safety check rather than a broken config, and (b) adds a host-side
  record for a layout nothing documents.

  **Answer:**

## Lifecycle

**Fresh launch.** `EnsureGlobalStorage` runs first, before config load. Then: config-change
approval, the per-workspace launch `flock`, removal of a stale stopped container, image
autoload, `prepareWsState`, `prepareHostFiles`, the selected packs' shared-dir sources and a
new home skeleton (podman), the durable dir (every jail backend), and argv assembly. The launch then spawns the jail's **keeper**
(the term is [`jail-lifetime-last-session-wins.md`](../design/jail-lifetime-last-session-wins.md)'s):
one background host process per running container jail, `yolo internal daemon jail-keeper`,
handed a `0600` plan file, the launch lock, a progress pipe and a lifeline. The keeper starts
what the launch disclosed, in order: the port forwards, the host services and their launch
check, the credential view, and then the container.

**The container's main process is a hold, and every session is an exec.** Pid 1 boots and then
only keeps the container running, until a SIGTERM. Every session, the first included, enters
with `<runtime> exec`. The first runs the provisioning stage on its own terminal, under an
in-jail `flock`, and records one outcome. The stage is an optional store prune, gated on an env
var the launcher sets only when no other jail is live; `mise install` (install only; resolution
happens there, not on an upgrade); the venv precreate script; and the bootstrap script last,
since it alone may refuse the launch, over a Node floor a selected pack declares
(`internal/provision`). Every later session waits for that outcome before its own boot pass,
and a refusal refuses it too.

**The keeper ends the jail.** Each session's launcher holds the jail's host-side session lock
shared; when the keeper can take it exclusively, the last session is gone, and the keeper stops
the container and runs the teardown. It ends the same way when the container ends some other
way or on a SIGTERM, and never on a timer. Nothing restarts it, and the one thing it restarts is
a host daemon it started that dies mid-session, up to five times in a row with the in-jail
supervisor's backoff ([HD-D25](../design/host-daemon-ownership.md#HD-D25)); a port forward or the
cgroup delegate is not restarted: an
arrival at a jail whose keeper died is refused and names `yolo stop`. Since 2026-10-01 it says
when a host service it started ends on its own while the jail is up, for a host daemon once its
restarts are given up on: one line in its log and an
entry in its start record, which each session that was in prints at its quit and every later
arrival as it enters, naming how the service ended, its log, and `yolo stop` then a launch as
what starts it again. A machine-wide daemon an older yolo left running is not this keeper's and
is not watched, and a daemonizing
wrapper's clean exit that leaves its service reachable is not recorded
([JL-D71](../design/jail-lifetime-last-session-wins.md#JL-D71)).

**Reuse and attach.** An `exec` into the running container: no `prepareWsState`, no new
skeleton, no provisioning of its own, no mount changes. The entrypoint still re-runs its whole
generator sequence inside the exec, which is safe because every generator is convergent. The
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

The container user is **root (UID 0)** with home `/home/agent`. Docker is not a supported container runtime. Choose a container setup by name: `podman-self` or
`apple-container-self`; Seatbelt setups run on macOS without a container.

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
  ([`OQ-BH12`](#oq-bh12)); fake-seed delivery and layout
  were [observed on hardware](#the-claudejson-seed), without testing real authentication. The
  one-time legacy migrations write to the dotted paths too.
- **Machine-scope shared dirs still need their own mounts**, nested inside that bind exactly
  as the cache is. Leaving them to the single bind is a **silent degradation**, not a
  failure: a shared credential keeps working but becomes per-workspace forever, so every new
  workspace demands a fresh login. The tell that separates the two tiers is which *side* of
  the mount the argv reads from — the workspace state dir means the bind already covers it,
  the machine store means it does not.
- **Single-file binds are copied rather than bound**, *materialized* into the workspace state
  dir. That is a choice: `container` 1.1.0 binds a single regular file and honors `:ro`
  (measured 2026-09-14), and the copy is kept because it needs no version gate (`acMaterialize`
  states the trade). Anything added as a single-file mount needs an AC arm.
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
into, and its sandbox profile allows writes to the whole sandbox home except the staged skills
and briefings, which it write-protects as the container backends' `:ro` binds do. Host files
cross by copy too: a source-bearing `host_files` file and each pack's `reads-host` grant are
copied into a root-owned `/ctx` tree the launch stages (`YOLO_CTX_ROOT`), and a directory
`source` is skipped with a warning; a home-root destination is the podman skeleton's link, laid in
the account home. It reaches the same two tiers by a different
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

Verified at `d4e435a3`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

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
| mise store volume name (macOS) | `yolo-mise-data-v2` on a Podman Machine; `<container name>.mise`, one per workspace, on Apple Container | `internal/prune/misevolumes.go` (`SharedMiseVolume`, `MiseVolumeName`) |
| Writable-home backing subdir | `writable-home` | `config.WritableHomeBackingSubdir` |
| Container run flags | `--rm -i --init --read-only`, and `--sig-proxy=false` and `--detach-keys=` on podman; never `-t`: the main process is a hold, and the first session's `exec` takes the terminal | `internal/cli/run/assemble.go` |
| The container's main process, and how a session enters | `yolo-entrypoint --yolo-hold-main '<stage>'` as pid 1's child, with `YOLO_JAIL_MAIN=hold`, started by the jail's keeper and ended only by a SIGTERM; every session by `<runtime> exec`, the first with `--yolo-first-session`; every podman `run` and `exec` with `--detach-keys=`, which turns the detach sequence off | `internal/entrypoint/jailmain.go`, `internal/cli/run/jailmain.go`, `runtime.DetachKeysArgs` |
| A session's record, in-jail, and its hangup | `/run/yolo/sessions/<id>`, the session's pid and start time, named by the `YOLO_SESSION_ID` its launcher passes its exec, the first session's included; the session's signal arm runs `yolo-entrypoint --yolo-hangup-session <id>`, which sends SIGHUP then SIGCONT to that session's processes and never stops the jail. For an attach, only in a jail whose `YOLO_CONTRACT_TAGS` has `session-hangup` | `internal/entrypoint/sessionhangup.go`, `internal/cli/run/sessionhangup.go` |
| The jail's keeper | `yolo internal daemon jail-keeper`, one per running container jail, spawned by the fresh launch with a `0600` plan file, a progress pipe, a lifeline and the launch lock; it owns the host services and the container, and ends when the session lock says the last session is gone or the runtime says the container is | `internal/cli/run/keeper.go`, `internal/cli/run/keeperspawn.go` |
| The keeper's host state | its liveness lock `locks/<container name>.keeper`, held exclusively for its life; its start record `owners/<container name>.keeper.json` (`0600`), which a jail whose keeper died is reaped from and which lists each host service that ended while the jail was up; its log `logs/jail-keeper-<container name>.log`, truncated at each keeper's start once it holds the liveness lock and mirrored into `launch.log` once the jail is ready | `internal/cli/run/keeperstate.go`, `internal/cli/run/keeperframe.go`, `internal/cli/run/keeperwatch.go` |
| The jail's main-process and provisioning state, in-jail | `/run/yolo/main/`: `boot`, `provision.sh`, `provision.lock`, `provision.claimed`, `provision.outcome`, `first-session` | `internal/entrypoint/jailmain.go` (`jailMainDir`) |
| The session lock | one host-side `flock` per container name, `locks/<container name>.sessions`, held shared by every session's launcher, and taken exclusively by the keeper, which then ends the jail | `internal/cli/run/sessionlock.go` |
| The owner-PID file | `owners/<container name>`, naming the jail's keeper; removed only while it still names whoever removes it, and kept, with the keeper's start record, when the keeper's stop did not end the container, so the jail left behind reads as one whose keeper is gone | `internal/cli/run/lifecycle.go`, `internal/cli/run/keeper.go` |
| The stop record | `<global storage>/owners/<container name>.stopped`: why the jail ended, written before the stop by the yolo process that stops it (the keeper at its drain or on a signal, `yolo stop`, an attach-skew restart, the reaper), and printed by every session its end cuts short; a stop from outside yolo writes none, and the session says so | `internal/cli/run/stopreason.go` |
| Scratch dirs, and the mode key | `/tmp`, `/var/tmp`, `/var/lib/containers`, `/var/cache/containers`, `/run`, `/dev/shm`; `ephemeral_storage` | `run.ScratchMountArgs` |
| In-jail install prefix | `/opt/yolo-jail/bin` + `/opt/yolo-jail/share/yolo-jail`, both `:ro` | `run.jailPrefixMountArgs` |
| Host-service socket dir, in-jail | `/run/yolo-services` | `paths.JailHostServicesDir` |
| Host-service dir, host side | `/tmp/yolo-host-services-<8hex>`, built in `paths.HostSingletonDir` | `paths.HostServicesDir` |
| Host-service dir, one macos-user session | `/tmp/yolo-host-services-<8hex>-<random>`, with the session's lock `.session.lock` in it | `paths.HostServicesSessionPrefix`, `paths.HostServicesSessionLockName`, `internal/cli/run/servicessession.go` |
| Staged content root, per jail | `<global storage>/agents/<container name>/` | `paths.AgentsDir` |
| Pack manifest mount | `/ctx/packs`, `:ro`, with `YOLO_PACK_ROOT`; source `<global storage>/agents/<container name>/pack-trees/<UTC stamp>-<random>`, one per launch | `internal/cli/run/assemble.go` (`packCtxDir`), `paths.PackTreeRoot`, `internal/cli/run/packtree.go` |
| Vestigial mount | `~/.yolo-entrypoint.lock` — mounted, touched and reserved; nothing `flock`s it | `assemble_parts.go`, `paths.HomeFileMountpoints`, `config/writablehome.go` |
| The real launch lock | one host-side `flock` per workspace, under the state dir's `locks/` | `internal/cli/run/flock.go` |
| Durable dir and its variable | `<workspace>/.yolo/durable` (`/workspace/.yolo/durable` in a container jail), `YOLO_DURABLE_DIR`; worktrees under `worktrees/` | `durable.DirName`, `durable.EnvVar`, `durable.ContainerJailPath`, `durable.WorktreesDir` |
| Durable-dir report budgets | size walk 2 s; `yolo check`'s section 10 s; at most 1000 registrations a scan; admin files read 4 KiB at most; directories read in batches of 256 | `durable.WalkBudget`, `durableCheckBudget`, `internal/durable/report.go` (`maxRegistrations`, `adminFileCap`, `readDirBatch`) |
| pi-subagents' durable base | `$YOLO_DURABLE_DIR/worktrees/pi-subagents`, as `worktreeBaseDir` in `~/.pi/agent/extensions/subagent/config.json` | `packs/pi/home/.pi/agent/extensions/yolo-durable-worktrees.js` |
| Its reaper | at most hourly, `pi-worktree-*` trees idle past 24 h; report `.yolo-reap.json` and rate limit `.yolo-reap.stamp` in that directory | `REAP_INTERVAL_MS` in the extension, `worktreereap.DefaultFloor`, `worktreereap.ReportName`, `worktreereap.StampName` |
| Durable size record | `<workspace>/.yolo/durable-size.json`, the last complete count, jail-only | `durable.SizeRecordName`, `durable.CountMain` |
| Storage-class section heading | `## Storage classes: what survives a restart` | `jailcontent.persistenceHeading` |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo, with their original ids.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| `A12` | A failed config generator **refuses the boot**, after every remaining generator has run | It used to warn and discard, so a failed generator still yielded a running jail whose agent silently read a missing or half-written config — the worst outcome available for a config surface, because the jail looks healthy and the misconfiguration surfaces later as inexplicable agent behavior. |
| `B2` / [`OQ-PD12a`](../design/program-delivery.md#decision-ledger) | The lazy-installer dir moves **ahead of the install prefixes**, and the anti-shadowing protection becomes a generation-time check | A launcher after what it installs is unreachable from its own second invocation onward, so the evergreen update the launcher exists to carry stopped running. The check converts a structural impossibility into a handled case, and that cost is accepted rather than hidden. `docs/design/program-delivery.md` [§3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) is the authority for the whole decision. |
| `OQ-6` | Both generated-script dirs live under **one** bind anchor at `~/.yolo/bin` | They need one writable bind, not two, and gathering them in the filesystem is not gathering them on PATH. It also retires the word "shim", which had come to name one of these dirs while colloquially naming the host's launch wrappers — three mechanisms, two of them sharing a word. |
| `OQ-C` | Skills and briefings are deduplicated **by destination** before the argv | Several packs contributing to one destination is the feature for both kinds, and the old advice — "do not declare an `into` another pack uses" — was unfollowable in the configuration it most matters for: an agent pack naming a skills dir plus a house-rules pack sharing a corpus. podman kills the boot on a duplicate destination, so the rule has to be enforced, not documented. |
| `#39` | Apple Container mounts machine-scope shared dirs explicitly, and a stranded per-workspace copy is **copied** into place, never moved | The single writable home made a machine-scope credential silently per-workspace. Repairing it by moving would re-run the experiment that destroyed a token on every boot. |
| <a id="dir-bh1"></a>`DIR-BH1` | **The maintainer's principle:** *"a non-selected pack can never have an impact."* The skeleton, name reservation ([`OQ-BH14`](#oq-bh14)) and `host_files`' surface-path reservation ([`OQ-BH15`](#oq-bh15)) all follow the selection, and no config validation rule reads the shipped set | Every exception found so far was an unselected pack's effect reaching a jail. Readers that describe packs you have not selected (`yolo pack ls`, `footprint`) and `EnsureGlobalStorage`, which runs before config loads, are the only ones that read the whole shipped set, and none of them validates config. |
| <a id="dir-bh2"></a>`DIR-BH2` | **The maintainer's direction:** the home is per jail, not shared (*"Why is this not just per jail? It seems like it's nothing."*). That it is a `:ro` skeleton from the selected packs, with `<state>/home` kept as the machine store, is the design's reading | A shared base leaked one workspace's config and every unselected pack's dirs, and a claude-less jail could read the machine's Claude credential file through it. Binding the workspace overlay whole at `/home/agent`, as Apple Container does, would give up `EROFS` on undeclared paths and rename every podman overlay out of its dot-stripped layout; it is that backend's mount-count workaround, not a model. |
| <a id="dir-bh0"></a>`DIR-BH0` | **The maintainer's call:** no `yolo base-home --archive` verb; the user runs a printed `mv` | The verb was built and deleted the same day. Cleaning up legacy bytes is the user's act, and nothing on the launch path needs them gone. |
| <a id="oq-bh4"></a>[`OQ-BH4`](#oq-bh4) | The launch path classifies no legacy bytes; the one remaining allowlist is `claudeJSONSeedKeys`, core-owned | With nothing mounted or seeded from the legacy bytes there is nothing to classify at launch; the classifier in `internal/basehome` serves only `yolo check`'s report. |
| <a id="oq-bh9"></a>[`OQ-BH9`](#oq-bh9) | The skeleton lives under `paths.AgentsDir()/<cname>/` (`paths.HomeSkeletonRoot`), reaped by `PruneOrphanAgentStaging`; delegated to the build | Host-only, keyed per jail, already reaped by liveness. Inside the workspace overlay it would be jail-writable. |
| <a id="oq-bh10"></a>[`OQ-BH10`](#oq-bh10) | **The maintainer's ruling:** a new skeleton per fresh launch, never modified afterwards; old ones go with the jail's `AgentsDir` entry through the reaper, which declines when liveness is unknown | Editing a skeleton in place would need a liveness answer the launch path cannot give, and a host `rmdir` under a live jail detaches its bind. |
| <a id="oq-bh12"></a>[`OQ-BH12`](#oq-bh12) | Apple Container gets runtime-aware seed paths in `prepareWsState` rather than podman's mounts; delegated to the build | That backend is limited by mount count, and its whole home is the overlay, so the seed must target `wsState/.claude.json`. The [native regression check](#the-owed-apple-container-seed-gate) is still owed. |
| <a id="oq-bh13"></a>[`OQ-BH13`](#oq-bh13) | **The maintainer's ruling:** the launch refusal over legacy bytes and `YOLO_ALLOW_LEGACY_BASE_HOME` went in the change that stopped the mount and deleted `seedAgentDir`; `yolo check` keeps a reworded detection-only report with the printed `mv` as optional cleanup | The refusal's reason was that every jail could read the bytes and the seed copied them; neither is true any more, and a hatch for a refusal with no reason is a second path to delete. |
| <a id="oq-bh14"></a>[`OQ-BH14`](#oq-bh14) | **The maintainer's ruling:** name reservation covers only the **selected** packs' directories; an unselected pack is treated as nonexistent (*"packs could come from anywhere and be added, removed, whatever"*) | The shipped set never covered what a jail could select, so reserving it was an unselected pack's effect. One user-scope entry may be legal in one workspace and refused in another, and a pack selected later can refuse an entry that passed before; both are accepted. |
| <a id="oq-bh15"></a>[`OQ-BH15`](#oq-bh15) | `host_files`' surface-path reservation covers only the selected packs' surfaces, resolved as `writable_home_dirs` resolves them; `config.SurfaceCollisions` still refuses two writers at launch | Confirmed in review: *"why is there even a thing that can read a pack that is not selected?"* It was the last validation rule reading the shipped set. |
| <a id="oq-bh16"></a>[`OQ-BH16`](#oq-bh16) | A launch removes its own skeleton at exit on the known-gone evidence that removes its tracking file; "could not ask" removes nothing; delegated to the build (*"it should get cleaned up when we can clean it up"*) | Without it a workspace used alone kept one skeleton per launch until `yolo prune --apply`. Removing a skeleton no container holds is not the modification [`OQ-BH10`](#oq-bh10) forbids. |
| <a id="oq-ds1"></a>[`OQ-DS1`](#oq-ds1) | The durable dir is `<workspace>/.yolo/durable`, exported as `$YOLO_DURABLE_DIR` | The name must say durable, and "scratch" already means the per-launch volumes here. |
| <a id="oq-ds2"></a>[`OQ-DS2`](#oq-ds2) | **The maintainer's ruling:** yolo never deletes the durable dir's contents, and reports them at every launch, in `yolo check` and in `yolo stores` (*"anything that lets them just not slip by forever"*). **Superseded for tool-managed worktrees** by the maintainer's request of 2026-10-24: *"I want to make sure every... every point that can expand is bounded in some way and has a clean up to keep it to its minimal footprint"*. A worktree a pack's tool made and left (pi-subagents' `pi-worktree-*`) is removed by [the tool-managed worktree reaper](disk-footprint.md#tool-managed-worktrees) once nothing in it can be lost; every other durable worktree keeps this ruling | A worktree's value is its unlanded commits and uncommitted files, which age says nothing about, so an age reaper would delete work. The reaper therefore never judges by age alone: it removes a tree only when its status is clean, no index bit hides an edit, its HEAD is a branch (which outlives the tree, with its reflog), and no git operation is in progress |
| <a id="oq-ds3"></a>[`OQ-DS3`](#oq-ds3) | The host notch gets one static sentence in the host header and no variable | Host `/tmp` is the machine's own, and a per-launch variable cannot reach the files `yolo host apply` writes. |
| `C8` | The in-jail binaries and flake bundle are **mounted**, not baked | It takes the Go sources out of the image derivation, so a Go-only commit costs no image rebuild. The security delta — what executes in the jail is host-mutable with no rebuild — is the trade being made deliberately; see [`image-staging-vs-baking.md`](image-staging-vs-baking.md#the-security-delta). |
| `HSD-1` | The host-services dir has **one creator, the spawn**, and the teardown removes it **only when the runtime answers** that no container of that name exists, running or not. (`HSD` stands for host-services dir, an id coined for this ledger.) | A second creation earlier in the launch had no teardown on the refusal paths between it and the spawn, so every refused launch left an empty directory in `/tmp`. The teardown used to read "could not ask the runtime" as "not running" and delete a possibly-live jail's endpoint files on no evidence. It asks whether a container exists, not whether one runs, because a relaunch releases the workspace lock once its bounded wait for a running container gives up, and a container that is only created is invisible to the running-only listing. That is the rule the tracking-file cleanup (`forgetGoneContainer`) already follows in the same teardown. Declining costs one directory, which the next launch of the workspace reuses. |
| `HSD-2` | The host-services dir is built in `paths.HostSingletonDir`, not in a literal `/tmp` | The two are the same directory in production. A test package that isolates its host singletons (`testsupport.IsolateHostSingletons`) then carries these dirs into its private directory too. Before that, tests that started host services and never stopped them left empty `/tmp/yolo-host-services-<8hex>` dirs on every run. |
| `HSD-3` | Reaping an orphaned jail also removes its host-services dir, by calling the launch teardown (`stopLoopholes`) with the orphan's name rather than deleting the directory directly | The orphan's owner died without its teardown, so nothing else removes the directory, and its endpoint files name listeners that died with the owner. Going through the teardown keeps its checks, the orphan's relaunch lock and then the container existence probe, which matter because a relaunch of the orphan's workspace publishes into the same directory. The cost is one `shutdown.container_check` timing mark inside the launch's reap span. |
| `HSD-4` / [`HD-D1`](../design/host-daemon-ownership.md#HD-D1) | On macos-user each **session** has its own host-services dir, removed by its own teardown, and a dead session's dir is collected only when its lock is free | Two sessions of one workspace shared the directory the container name selects. The second session's front replaced the first's endpoint file, and the first teardown removed the directory under the survivor, whose jail lost its Claude OAuth broker endpoint (measured on a Mac, [`OQ-HD10`](../design/host-daemon-ownership.md#11-decision-ledger)). A shared directory cannot be fixed by counting its users: an endpoint file names one front, and a front lives in one session's yolo process. A missing lock file stays because a session creates its directory before its lock, so that state is a session starting up as much as a dead one. |
