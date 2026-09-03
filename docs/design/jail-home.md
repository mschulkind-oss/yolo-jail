# How `/home/agent` works — construction, mounts, and sharing

**Status:** REFERENCE — describes shipped behaviour. **Spot-verified
2026-08-23.** This doc's own invitation ("line numbers drift — trust the named
function") was finally taken up, and the answer is: **the named functions are
almost all still right; nearly every line number is wrong.** What was checked,
and the verdicts:

| Claim | Verdict |
|---|---|
| `podmanBaseMounts` / `appleContainerBaseMounts` exist and do what §2.2 says | **HOLDS**, moved — now `assemble_parts.go:64-124` and `:36-57` |
| The three `:ro`-base escape-hatch symlinks are exactly `.claude.json`, `.gitconfig`, `.bashrc` | **HOLDS** — `internal/storage/ensure.go:97,100,103` |
| `--rm -i --init --read-only` argv | **HOLDS** — `assemble.go:158` |
| Storage layout version = 2 | **HOLDS** — `const StorageLayoutVersion = 2`, `ensure.go:22` |
| `WriteInPlace` / `ClearContents` / `resetAnchorDir` / the four generators | **HOLDS** — `fsx.go:35-40,49-63`; `shims.go:24,39,185,278` |
| `PrepareSkills` / `SkillStagingName` | **HOLDS** — `internal/jailcontent/skills.go:87,77` |
| `workspaceReadonlyMountArgs` at mounts.go:20-53 | **HOLDS** — the one line range in the doc that was still exact |
| Docker removed, three resolvable runtimes | **HOLDS** — `validate.go:128-131` |
| §2.5 per-agent overlay loop | **WRONG** — corrected in place |
| §2.6 claude-shared-credentials mount | **WRONG** — corrected in place |
| §7 gotcha 1 "`os.Rename` no longer appears anywhere in `internal/entrypoint`" | **WRONG** — corrected in place |
| §7 gotcha 7 PATH | **WRONG** — corrected in place, and AGENTS.md is wrong too |

**Not verified:** every line number not named above (they have drifted by up to
~250 lines in `paths.go`), §3's file-class inventory, §4's sharing table, §5's
lifecycle sequence, and §6's UID-mapping specifics.

> [!WARNING]
> **Treat every bare `file.go:NNN` in this doc as unverified unless the table
> above names it.** The systematic drift measured 2026-08-23:
> `internal/paths/paths.go` moved by ~250 lines (`GlobalHome()` is at **:320**,
> not `:67`; `GlobalCache()` **:326** not `:73`; `GlobalMise()` **:323** not
> `:70`; `AgentsDir()` **:332** not `:79`; `JailHostServicesDir` **:46** not
> `:37`; `UserConfigPath()` **:390** not `:85`). `assemble.go`'s env block is at
> **:559-598** (`JAIL_HOME` :562, `MISE_DATA_DIR` :566, `RUSTUP_HOME` :572,
> `CARGO_HOME` :573, `HOME` :586, `YOLO_HOST_DIR` :598), not `:364-392`; nested
> detection is **:223-227**, not `:183`. `ensure.go`: `EnsureGlobalStorage`
> **:41-111**, `EnsureSymlink` **:151-179**, `MigrateStorageLayout` **:251-273**.
> `boot.go`: `Main` at **:400**, generator sequence **:432-521**.
> The function names are the durable part; the numbers are not.

Audience: maintainers and agents working on yolo-jail who need the mental model
fast. Everything here is traced from the live Go tree; each claim carries a
`file:line` reference. Line numbers drift — treat them as "where to look", and
trust the named function over the number.

## 1. The mental model

`/home/agent` is not a directory that exists anywhere as a whole. It is
**composed at container create** from three ingredients: (a) a **shared,
read-only base** — `~/.local/share/yolo-jail/home` on the host, mounted at
`/home/agent:ro` and common to every jail on the machine
(internal/cli/run/assemble_parts.go:42); (b) **per-workspace writable
overlays** — directories and single files under `<workspace>/.yolo/home`
bind-mounted *over* specific paths inside the read-only base
(assemble_parts.go:43-57, assemble.go:153-155); and (c) **files the entrypoint
generates at every boot** into those writable overlays (shims, bashrc, agent
configs — internal/entrypoint/boot.go:392-499). Nothing in the home survives
via the container itself (`--rm`, assemble.go:127); all persistence is
host-side bind mounts. The split answers one question everywhere: *is this
state one-truth-per-host (base / shared mounts) or per-workspace (overlay)?*

## 2. The physical layers, in mount order

### 2.0 What the image contributes: a mountpoint

The image bakes `/home/agent` as an **empty directory**, created under fakeroot
(owned by UID 0) purely so it exists as a mount target on the `--read-only`
rootfs (flake.nix:669). The image's `/etc/passwd` has exactly one user —
`root:x:0:0:root:/home/agent:/bin/bash` (flake.nix:674) — and the image `Env`
does **not** set `HOME`; `HOME=/home/agent` and `JAIL_HOME=/home/agent` are
injected at run time with `-e` (assemble.go:364,380). At runtime the baked
directory is fully covered by mounts; its image content is irrelevant.

### 2.1 The mount stack

```
/home/agent  (as seen inside a podman jail)
│
│  image layer: empty dir, mountpoint only            (flake.nix:669)
│
├─[1] BASE  :ro   ~/.local/share/yolo-jail/home       shared by ALL jails
│        contains: union of every agent's overlay dirs, shared dirs,
│        touch-file mountpoints, and three relative symlinks that point
│        INTO writable overlays:                      (storage/ensure.go:39-102)
│          .bashrc      -> .config/bashrc
│          .claude.json -> .claude/claude.json
│          .gitconfig   -> .config/git/config
│
├─[2] rw overlays from <workspace>/.yolo/home          per-WORKSPACE
│        .npm-global/ .local/ go/ .yolo-shims/ .yolo-launchers/ .config/ .ssh/
│        + 8 single-file binds (.bash_history, .yolo-*)
│        + .claude/ .copilot/ ... (selected agents only)
│                                        (assemble_parts.go:43-57; assemble.go:153-155)
├─[3] rw shared   .cache        <- ~/.local/share/yolo-jail/cache   all workspaces
│                                                     (assemble_parts.go:48)
├─[4] rw shared   .claude-shared-credentials          claude selected only
│                 <- GLOBAL_HOME/.claude-shared-credentials  (assemble.go:157-160)
│
├─[5] :ro single files layered on top of the overlays:
│        briefings (.claude/CLAUDE.md, ...), skills dirs, .config/git/ignore,
│        .config/yolo-jail/config.jsonc, .config/yolo-user-env.sh (rw)
│
└─ siblings outside /home/agent:
     /mise        <- GLOBAL_MISE bind (named volume on macOS)  (assemble_parts.go:59-64)
     /workspace   <- the workspace, rw                          (assemble_parts.go:41)
     /opt/yolo-jail <- BAKED into the image (not a mount): real-file CLI copies
                       at /opt/yolo-jail/bin + flake bundle at share/yolo-jail
                       (flake.nix installPrefix). No source bind any more.
     /ctx/*       <- read-only context (host nvim/claude files, config mounts)
```

Notation for the rest of this section: `ws` = `<workspace>/.yolo/home` (created
by `prepareWsState`, internal/cli/run/prepare.go:131-173); `GLOBAL_HOME` =
`~/.local/share/yolo-jail/home` (internal/paths/paths.go:67); `GLOBAL_CACHE` =
`.../cache` (paths.go:73); `GLOBAL_MISE` = `.../mise` (paths.go:70);
`AGENTS_DIR` = `.../agents` (paths.go:79). Assembly order is fixed by
`assembleRunCmd` (internal/cli/run/assemble.go). "rw" = no `:ro` suffix.

### 2.2 Base mounts (podman branch, `podmanBaseMounts`, assemble_parts.go:37-66)

| Host path | Container path | Mode | Purpose |
|---|---|---|---|
| `<workspace>` | `/workspace` | rw | the project (assemble_parts.go:41) |
| `GLOBAL_HOME` | `/home/agent` | **ro** | shared base home (:42) |
| `ws/npm-global` | `/home/agent/.npm-global` | rw | npm globals (:43) |
| `ws/local` | `/home/agent/.local` | rw | `~/.local` (bin, share) (:44) |
| `ws/go` | `/home/agent/go` | rw | GOPATH (:45) |
| `ws/yolo-shims` | `/home/agent/.yolo-shims` | rw | blocked-tool shims — FIRST on PATH (:46) |
| `ws/yolo-launchers` | `/home/agent/.yolo-launchers` | rw | lazy-install launchers — LAST on PATH (:52) |
| `ws/config` | `/home/agent/.config` | rw | `~/.config` (:47) |
| `GLOBAL_CACHE` | `/home/agent/.cache` | rw | **shared across workspaces** (:48) |
| `ws/yolo-bootstrap.sh` | `/home/agent/.yolo-bootstrap.sh` | rw | single-file bind (:49) |
| `ws/yolo-venv-precreate.sh` | `/home/agent/.yolo-venv-precreate.sh` | rw | (:50) |
| `ws/yolo-perf.log` | `/home/agent/.yolo-perf.log` | rw | (:51) |
| `ws/yolo-socat.log` | `/home/agent/.yolo-socat.log` | rw | (:52) |
| `ws/yolo-entrypoint.lock` | `/home/agent/.yolo-entrypoint.lock` | rw | (:53) |
| `ws/yolo-ca-bundle.crt` | `/home/agent/.yolo-ca-bundle.crt` | rw | (:54) |
| `ws/yolo-installed-lsps` | `/home/agent/.yolo-installed-lsps` | rw | (:55) |
| `ws/bash_history` | `/home/agent/.bash_history` | rw | (:56) |
| `ws/ssh` | `/home/agent/.ssh` | rw | dir, mkdir 0700 (prepare.go:134) (:57) |
| `GLOBAL_MISE` (or volume) | `/mise` | rw | mise store — see §2.4 (:59-64) |

All single-file mountpoints are touched host-side before create so the bind has
an inode to pin (prepare.go:140-146; GLOBAL_HOME side: storage/ensure.go:84-91).

**Apple Container branch** (`appleContainerBaseMounts`,
assemble_parts.go:18-32): no `:ro` base — the whole `ws` dir is mounted rw at
`/home/agent` (device-limit workaround), plus `GLOBAL_CACHE`, the
`yolo-mise-data-v2` volume, and bare `--tmpfs` scratch. Single-file cases are
**materialized** into `ws` instead of mounted (`acMaterialize`,
internal/cli/run/helpers2.go; assemble.go:172-173) because AC can't do
single-file binds.

### 2.3 Scratch (`ScratchMountArgs`, internal/cli/run/runmount.go:20-42)

The rootfs is `--read-only` (assemble.go:127), so writable scratch is explicit.
Default mode `ephemeral_storage: "volume"`: anonymous volumes for `/tmp`,
`/var/tmp`, `/var/lib/containers`, `/var/cache/containers` plus `--tmpfs /run`
and `--tmpfs /dev/shm:size=2g` (runmount.go:24-32). Mode `"tmpfs"`: all six
tmpfs (runmount.go:34-41). Linux podman also gets `--read-only-tmpfs=false`
(assemble.go:132-134). Anonymous volumes die with the container (`--rm`).

### 2.4 The mise store at `/mise`

`GLOBAL_MISE` bind on Linux; named volume `yolo-mise-data-v2` on macOS
(assemble_parts.go:59-64; const assemble.go:19). Inside a nested jail the store
is `/mise` itself, so every nesting depth shares one store (`jailMiseStoreDir`,
internal/cli/run/storagehelpers.go:21-26; storage-side twin
`JailMiseStoreDir`, ensure.go:170-175). Env: `MISE_DATA_DIR=/mise`,
`RUSTUP_HOME=/mise/rustup`, `CARGO_HOME=/mise/cargo` (assemble.go:368-375).
There is **no `/mise` symlink anymore** — `/mise` is the mount target; layout
v2 removed the old shared-host-dir + symlink scheme, and the host's own
`~/.local/share/mise` is never mounted (ensure.go:17-20, 143-148).

### 2.5 Per-pack state-dir overlays (assemble.go:185-196)

> ### ⚠ Retracted 2026-08-23: "for each **selected agent** with overlay dirs"
>
> **There is no per-agent branch left.** The loop is registry-driven off the
> pack `state` contributions: writable dirs come from `packload.WritableDirs`
> (`assemble.go:185-188`) and machine-scope shared dirs from
> `packload.SharedDirs` (`:193-196`). `agentOverlaySubdirs` and
> `internal/agents/agents.go` are both gone. The old `assemble.go:152-155`
> anchor now points at unrelated `/ctx`-mount code — a *silently wrong* line
> reference, which is the failure mode this doc's header warns about.
>
> Also stale in the old list: **`gemini` is not an agent** — that tree belongs to
> agy, under `.gemini/antigravity-cli/` (`internal/entrypoint/env.go:280-290`).

Each pack's `state` contribution declares a home-relative dir and a scope
(`workspace`, the default, or `machine`). For each: `ws/<subdir>` →
`/home/agent/.<subdir>` rw. The live workspace-scope set at time of writing:
claude→`.claude`, copilot→`.copilot`, pi→`.pi`, codex→`.codex`,
agy→`.gemini/antigravity-cli`; opencode declares none. **Read the packs, not this
list** — it is pack data, and adding a pack changes it with no core edit.
Creation/seeding happens in `prepareWsState`
(prepare.go:136-171): mkdir, then `seedAgentDir(GLOBAL_HOME/.<subdir>,
ws/<subdir>)` copies **top-level regular files only** (auth tokens), never
overwrites, skips subdirs (storagehelpers.go:39-65). Claude extras:
`syncClaudeJSONSeed` (§4.3) and legacy migrations (`ws/claude-projects` →
`ws/claude/projects`, `ws/claude-settings.json` → `ws/claude/settings.json`;
prepare.go:153-165). Copilot/gemini get their own selection-gated migrations
(`ws/copilot-sessions` → `ws/copilot/session-state`, `ws/gemini-history` →
`ws/gemini/history`; prepare.go:166-171).

> Historical note: the old per-file `.yolo/home/copilot-mcp-config.json`-style
> mounts described in AGENTS.md are gone — the overlay is now whole-dir.

### 2.6 Claude shared credentials

`GLOBAL_HOME/.claude-shared-credentials` → same path in-container, **rw**, only
when the claude pack is selected (non-AC). The dir and its `.credentials.json`
are ensured/migrated host-side (`ensure.go:74`); the OAuth broker reads the same
host file. How the jail's `~/.claude/.credentials.json` reaches it: §4.2.

> ### ⚠ Retracted 2026-08-23: the `assemble.go:156-160` anchor
>
> **This mount is no longer hardcoded core logic and is not in `assemble.go` at
> all.** It is a pack `state` declaration with `scope: machine` —
> `packs/claude/pack.json:125,136` — consumed by the generic `SharedDirs` loop at
> `assemble.go:193-196` (§2.5). Nothing in core names
> `.claude-shared-credentials`; other live references are
> `internal/entrypoint/env.go:305-307` and `internal/storage/ensure.go:74`.
>
> This is the same shape as §4.2's note that "neither the file nor the dir is
> named in Go any more" — the two sections had drifted apart, and §4.2 was the
> one that was right.

### 2.7 Writable home dirs (config `writable_home_dirs`)

Extra `$HOME` subpaths the user declares writable, for agent extensions that
hardcode a home path yolo doesn't manage (motivating case: pi-lens writing
`~/.pi-lens`). Each entry `<p>` is bound `ws/writable-home/<p>` →
`/home/agent/<p>` **rw**, nested inside the `:ro` base. `prepareWsState` creates
BOTH ends: the backing dir (load-bearing — podman fails the whole container on a
missing bind source) and the mountpoint inside `GLOBAL_HOME`. That second MkdirAll
is belt-and-braces rather than strictly required: the code comment says the OCI
runtime cannot auto-create a mountpoint inside a `:ro` bind (crun `mkdirat` →
EROFS, surfacing as `conmon bytes "": readObjectStart`), but seven live
podman 5.8.4 experiments could not reproduce that — podman auto-created the nested
mountpoint in every realistic variant, and the only reproduction was a `:ro` bind
whose *host source* was itself read-only. Keep the pre-create anyway: it is cheap,
idempotent, and makes mode/ownership deterministic (`drwxr-xr-x` 755 rather than
podman's `drwxr-xr-t` 1755). Treat the stated EROFS mechanism as
version-dependent, not a cross-runtime guarantee. Backing dirs are created in
`prepareWsState`
(prepare.go); mounts emitted in `podmanBaseMounts` (assemble_parts.go), sorted.
Derived by `config.WritableHomeDirs` from the **merged** config (safe at any
scope — see below); validated by `validateWritableHomeDirs`.

Guard rules (`checkWritableHomeDir`, internal/config/writablehome.go): reject
absolute paths, `..` escapes, `:` (podman mount-option footgun), and any first
segment yolo already manages (the base overlays, single-file mounts, `:ro`-base
symlinks, and every agent overlay dir — those are already rw, so the key would
only shadow a yolo mount there).

**Scope contrast with `cache_relocations`:** cache_relocations is user-scope-
ONLY because it mounts an arbitrary HOST path rw (an escalation primitive if a
jail-editable config could set it). writable_home_dirs confines the destination
under `/home/agent` and backs it into the workspace's own `.yolo/home`, so a
jail editing its workspace config gains nothing it couldn't get by writing to
`/workspace` — hence it is safe at any scope and read from the merged config.

**Other backends:** no-op. Apple Container mounts all of `ws` → `/home/agent`
rw in one bind (§2.2), and macos-user's Seatbelt profile allows writes to the
whole sandbox home, so every declared path is already writable there.

### 2.8 User-declared host files (config `host_files`)

Any file a user wants in the jail, composed by the same engine that generates the
agent settings (`docs/plans/host-file-staging.md`). Each entry becomes a surface
owned by the pseudo-agent `user`, named by an injective slug derived from its
destination path. Three mount-relevant pieces, all emitted by
`internal/cli/run/hostfiles.go`:

- **Source input** (source-bearing entries only): the host path bound `:ro` at
  `/ctx/host-user/<slug>` (`hostUserFileArgs`). Files go through
  `ROFileMountArg` for the nested-jail deref; directories bind directly. A source
  that does not exist is **skipped**, because podman fails the whole container on
  a missing bind source and an uncreated host dotfile is a normal state — the
  surface then falls back to its `defaults` layer.
- **Wire form**: the resolved entry list rides `-e YOLO_HOST_FILES=<json>`
  (`hostFilesEnv`). The host CLI is the single source of truth — it alone can read
  the user config and stat host paths — so the entrypoint never re-reads config,
  and the slug it derives is guaranteed to match the mount emitted here.
- **Writable destination**: because the home base is `:ro`, where a destination
  lands decides whether the composed write succeeds at all.
  `config.HostFileEntry.StagingFor` sorts each into three cases
  (docs/design/composed-file-permissions.md §7.5):

| Destination | Staging | Mechanism |
|---|---|---|
| under `.config/`, `.cache/`, `.local/`, `go/`, `.npm-global/`, or a **selected** agent's overlay dir | none | already a rw bind; staging would shadow a yolo mount |
| a home-root file (`~/.npmrc`) | symlink | a **relative, dangling** symlink in `GLOBAL_HOME` → `.config/yolo-home/<slug>`, resolving through the mount table into the rw `.config` overlay — the same hatch `.bashrc`/`.claude.json` use (§4.1) |
| a new top-level dir (`~/foo/bar.json`) | writable subtree | the `writable_home_dirs` recipe: backing dir + `GLOBAL_HOME` mountpoint + nested rw bind |

The symlink shape is load-bearing and not interchangeable: a directory bind would
make the destination a *directory* (the composed write then fails "is a
directory"), and a pre-created **empty** backing file is worse — `os.Stat` on a
bind-mounted empty file *succeeds*, so `mode: once`'s seed-if-absent guard returns
early on the first boot and the file stays empty forever. Dangling is what keeps
`once` correct.

An entry's destination may not be a path yolo owns as a single file/symlink, nor
any yolo-composed agent surface (`hostFileReservedDests`). Scope is **per entry**:
a `source`-bearing entry is user-config-only (a credential boundary — see
[agent-credentials.md §2.4](agent-credentials.md)), while a source-less one is
legal at any scope.

**Other backends:** Apple Container mounts all of `ws` rw at `/home/agent`, so
destinations are writable but the `/ctx/host-user` single-file binds hit
apple/container#1089. macos-user has no mounts at all and therefore carries only
the **source-less** entries.

### 2.9 Remaining mounts, in assembly order

Only the home-relevant ones expanded; the rest one-lined for orientation.

- **`ws/yolo-user-env.sh`** → `/home/agent/.config/yolo-user-env.sh` rw;
  written pre-assembly by `writeUserEnvFile` (run.go:176-177; mount
  assemble.go:171-176). AC: materialized.
- **In-jail CLI repo**: no longer a mount. `/opt/yolo-jail` is BAKED into the
  image (`flake.nix` `installPrefix`): real-file copies of the four shipped
  binaries at `/opt/yolo-jail/bin` plus the flake bundle at
  `/opt/yolo-jail/share/yolo-jail`. The in-jail CLI finds its repo root the same
  way a host install does — exe-relative bundle discovery (`reporoot.Resolve`
  step 3) — so there is no source bind and no `YOLO_REPO_ROOT` env to set.
- **Host nix daemon + store**: `/nix/var/nix/daemon-socket` rw + `/nix/store`
  ro + `NIX_REMOTE=daemon`, when both exist and runtime isn't AC; macOS podman
  additionally requires opt-in via `YOLO_NIX_HOST_DAEMON`
  (`shouldMountHostNix`, internal/cli/run/hostprobes.go:15-30;
  assemble.go:209-217).
- **Global gitignore**: host `core.excludesFile` (or `~/.config/git/ignore`) →
  `/home/agent/.config/git/ignore` **ro** + `YOLO_GLOBAL_GITIGNORE` env
  (`gitignoreMountArgs`, assemble_parts.go:124-149). Nested jails dereference a
  bind-mounted source by copying it to `ws/.config/git/ignore` first
  (`ROFileMountArg`, runmount.go:91-107).
- **Config `mounts`**: each `host[:container]` → `:ro`, default target
  `/ctx/<basename>` (assemble.go:103-124, 477-484). Skipped on AC.
- **Port-forward sockets**: `/tmp/yolo-fwd-<cname>` → `/tmp/yolo-fwd` rw
  (Linux podman, bridge mode, `forward_host_ports` non-empty;
  assemble_parts.go:155-174; host socat: internal/cli/run/network.go:22-41).
- **Host-services sockets**: `/tmp/yolo-host-services-<sha1(cname)[:8]>` →
  `/run/yolo-services` rw, always for non-AC (`hostServicesMountArgs`,
  assemble_parts.go:189-200; target const paths.go:37). Carries
  `cgroup-delegate.sock`, loophole daemon relays (`claude-oauth-broker.sock`,
  `host-processes.sock`, `journal.sock`, …) (loopholesruntime.go:166-270).
- **Devices/GPU/KVM/nesting**: no `/home/agent` paths (assemble_parts.go:204-296,
  72-118; helpers2.go:93-177).
- **Host nvim config** → `/ctx/host-nvim-config` **ro** when present
  (assemble.go:282-286); the entrypoint copy-merges it into `~/.config/nvim`
  (§3).
- **/dev/null shadows**: over `/workspace/.vscode/mcp.json` and
  `/workspace/.overmind.sock` when present (assemble.go:288-294).
- **`workspace_readonly` overlays**: `yolo-jail.jsonc` + each listed rel path
  re-mounted `:ro` over `/workspace/...` (mounts.go:20-53).
- **Per-side venv shadows**: `.venv` ∪ mise venv path ∪ `per_side_paths`, each
  backed by `ws/venv-shadows/<rel with "/"→"__">` mounted rw over
  `/workspace/<rel>` — jail-side venvs never collide with host-side ones
  (`venvShadowMountArgs`, mounts.go:62-96).
- **User config for nested jails**: `~/.config/yolo-jail/config.jsonc` →
  same path in home, **ro** (assemble_parts.go:310-323; paths.go:85).
- **Skills**: per `skills` contribution, `AGENTS_DIR/<cname>/skills-<pack>` →
  `/home/agent/<into>` **ro** (assemble.go's `packSkillTargets` loop; staging
  name is `jailcontent.SkillStagingName`).
  There is no per-agent target table any more — `.claude/skills`,
  `.pi/agent/skills` and the rest are each some pack's `into` (the Go registry
  that held them was `internal/agents/agents.go`, now gone). Staging rebuilt
  host-side **every invocation** by `jailcontent.PrepareSkills`
  (internal/jailcontent/skills.go): clears staging contents in place
  (inode-preserving), writes the built-in skills, then copies
  host `~/.<agent>/skills/*` dereferencing symlinks.
- **Host agent-config files** (claude/pi selected): the yolo-declared,
  non-widenable host-file set — each pack's `reads-host` contribution, claude
  and pi each declaring just `settings.json` — → the `/ctx` path
  `packload.CtxPath` derives from its `into` (`/ctx/host-claude/settings.json`,
  `/ctx/host-pi/settings.json`) **ro** (`hostFileArgs`,
  internal/cli/run/packhostgrants.go). No config key and no `YOLO_HOST_*_FILES`
  env: which host files cross into the jail is a credential boundary fixed in
  yolo-shipped code, not a config knob (the retired
  `host_claude_files`/`host_pi_files` keys; plan §10.4). **The gate is ORIGIN,
  not a baked list.** It was `agents.AgentSpec.HostFiles`, a fixed per-agent Go
  constant, until that registry was deleted; a pack declares the grant now and
  `HonoredHostFiles` honors it only for an embedded or local pack, refusing a
  fetched one outright. The entrypoint re-derives the identical set in-jail from
  the same pack manifests — not from a registry, which is why `CtxPath` is
  deliberately the single definition both sides call — and reads
  `settings.json` fail-open from the mount.
- **Briefings**: per DESTINATION,
  `AGENTS_DIR/<cname>/briefing-<escaped into>` → `/home/agent/<into>` **ro**
  (assemble.go's briefing loop; the destination list and the staging name both come
  from `briefingDestinations`/`briefingStagingName` in
  `internal/cli/run/briefingdest.go`, which the write half also calls). The key was
  the PACK until 2026-09-03; it moved because the composed content now varies per
  destination (`briefing-audiences.md` §5), so a pack no longer identifies a file.
  Destinations are pack data, not pairs in a table:
  claude→`.claude/CLAUDE.md`, copilot→`.copilot/copilot-instructions.md`,
  codex→`.codex/AGENTS.md`, opencode→`.config/opencode/AGENTS.md`,
  pi→`.pi/agent/AGENTS.md`, agy→`.gemini/config/AGENTS.md`. Two packs
  naming one `into` is legal and expected (an agent pack plus a house-rules
  pack); first writer wins the mount, since podman rejects a duplicate mount
  destination. Content regenerated host-side on **every** invocation by
  `refreshJailBriefings` (prepare.go; called from run.go) with inode-preserving
  writes so live mounts see updates.
- **Loophole runtime mounts** (`loopholes.RuntimeArgsFor`,
  internal/loopholes/runtime.go): module dir →
  `/etc/yolo-jail/loopholes/<name>` ro + state dir →
  `/var/lib/yolo-jail/loopholes/<name>` ro (both jail_daemon-only). The state
  mount is the WHOLE dir only when the manifest declares no `state_files`;
  with the key, each named file crosses on its own `:ro` mount and nothing else
  does (`claude-oauth-broker` ships it so its CA private key stays host-side —
  issue #33). Plus CA cert + `NODE_EXTRA_CA_CERTS`,
  `host_bind_mounts` (e.g. audio: pulse/pipewire sockets rw + `/etc/asound.conf`
  ro), `host_devices` (`/dev/snd`).

The attach path (run.go:331-361) adds **no mounts** — it is exec-only; the
mount table is frozen at container create.

## 3. What the entrypoint generates at boot vs what persists

The entrypoint (`cmd/yolo-entrypoint` → internal/entrypoint) re-runs its full
`Main` on **every** invocation — container start *and* exec-into-existing
(boot.go:392-499). Every step goes through `genStep`: errors become warnings,
boot never aborts (boot.go:527-531). All writes land in the writable overlays;
the `:ro` base is never written from inside.

File classes:

**Generated each boot (regenerate-in-place, convergent):**
- **Two dirs, opposite ends of PATH** — both cleared CONTENTS-ONLY every boot
  (`resetAnchorDir`, shims.go), never `RemoveAll`: each is a bind-mount anchor
  whose parent is `:ro`, so unlinking the dir fails EROFS and leaves the stale
  children in place.
  - `~/.yolo-shims/` — **blockers** (`GenerateShims`): the blocked-tool shims from
    `YOLO_BLOCK_CONFIG`. Ordered FIRST on PATH, because interception is the job.
  - `~/.yolo-launchers/` — **lazy installers** (`GenerateAgentLaunchers` for each
    pack `program`, then `GeneratePackageManagerLaunchers` for pnpm). Ordered LAST,
    after `/bin`, so a launcher is reached only when nothing else provides the
    name — which is what stops a pack declaring `program fzf` from shadowing (and
    breaking) the image's `/bin/fzf`. A tool that is both blocked AND
    pack-declared gets one of each and the blocker wins by position.
- `~/.bashrc` (via the base's `.bashrc → .config/bashrc` symlink; truncate in
  place "for the bind mount", shell.go:46-48), `~/.yolo-bootstrap.sh`,
  `~/.yolo-venv-precreate.sh` (shell.go:141-145, 259-261),
  `~/.yolo-ca-bundle.crt` (always written, even empty; system.go:17-63).
- `~/.config/mise/config.toml` — created with base tools or surgically healed
  in place; written only when changed (mise.go:37-145).
- MCP wrappers `~/.local/bin/mcp-wrappers/{node,npx}` etc.
  (mcp_wrappers.go:7-15). `yolo-cglimit` and `yolo-journalctl` are NO LONGER
  generated here — the image bakes them as Go binaries at `/bin`
  (flake.nix `shippedBinaries`), and scripts.go now only UNLINKS the scripts an
  older entrypoint left in `~/.local/bin`, which precedes `/bin` on PATH and
  would otherwise shadow them forever.
- Agent configs — read-modify-write with forced jail-managed keys:
  `~/.claude/settings.json` (three-way host merge then forced keys,
  claude.go:29-97), `~/.claude.json` (claude.go:99-114), gemini/opencode/codex
  settings + managed-MCP sidecars (agent_configs.go, codex.go), copilot
  mcp/lsp configs regenerated (agent_configs.go:120-139). Sidecar files
  (`yolo-managed-mcp-servers.json`) make reconcile convergent: only
  previously-managed server names are deleted before re-merge — user-added
  servers survive (agent_configs.go; claude.go:102-105).
- `~/.config/nvim/**` copy-merged from `/ctx/host-nvim-config`
  (boot.go:216-266).

**Seeded once, then owned by the jail:**
- Agent overlay dirs seeded from GLOBAL_HOME top-level files (auth tokens),
  never overwritten (storagehelpers.go:39-65).
- `~/.copilot/config.json` written only if missing (agent_configs.go:113-118).
- `setDefault` keys in agent settings (gemini approvalMode, claude
  hasTrustDialogAccepted, opencode `$schema`) fill only when absent.

**Runtime state (written by use, not boot):**
- launcher stamps `~/.cache/yolo-agent-stamps/*`,
  `~/.cache/yolo-package-manager-stamps/*` (shims.go); `~/.yolo-installed-lsps`
  sentinel written by the bootstrap script (shell.go:181,249);
  `~/.yolo-perf.log` (append, trimmed to 50 runs, boot.go:54-88);
  `~/.yolo-socat.log`; `~/.bash_history`; agent session/history state in the
  overlay dirs.

**Shared mutable:**
- `~/.claude-shared-credentials/.credentials.json` (§4.2), `~/.cache`, `/mise`.

**Deliberately never touched by the entrypoint:** skills dirs (mounted `:ro`
by the CLI; boot.go:459), `mise hook-env` at boot (flock deadlock — hook-env
spawns uv via the mise shim which *is* mise; boot.go:496), user-authored keys
in agent settings, non-managed MCP servers, any directory that is a mount
anchor (fsx.go:49-63).

## 4. Sharing semantics: one truth per host, per workspace, or per boot

| Scope | What lives there |
|---|---|
| **Per-host (all workspaces)** | `GLOBAL_HOME` `:ro` base + `.claude-shared-credentials` (rw); `GLOBAL_MISE` at `/mise`; `GLOBAL_CACHE` at `~/.cache`; image-load cache under `cache/images/` (internal/image/image.go:139-145); layout-version marker; `~/.config/yolo-jail/config.jsonc` |
| **Per-workspace** | everything in `<workspace>/.yolo/home`: the rw overlays (`npm-global`, `local`, `go`, `yolo-shims`, `yolo-launchers`, `config`, `ssh`), the 8 single-file mountpoints (7 `yolo-*` files + `bash_history`), per-selected-agent config dirs |
| **Per-jail (container name)** | `containers/` tracking files, `agents/<cname>/` briefing+skills staging (paths.go:76-79), `logs/<cname>-socat.log` + `logs/broker-relay-<sha1(cname)[:8]>.log` (network.go:36; loopholesruntime.go:370) — `logs/host-service-<name>.log` is per-service, shared (loopholesruntime.go:214) |
| **Per-host-workspace inside a home** | Claude history keyed on `sha256(YOLO_HOST_DIR)[:12]` (§4.4) |
| **Per-boot / ephemeral** | `/tmp`, `/run`, `/dev/shm`, anonymous volumes, `/tmp/yolo-jaild.pid` |
| **Host-only, never mounted** | host `~/.local/share/mise` (ensure.go:143-148), host credentials generally |

### 4.1 Why GLOBAL_HOME is `:ro` with symlink escape hatches

`EnsureGlobalStorage` (ensure.go:39-108) builds the base: the **union** of every
SHIPPED pack's overlay dirs (`packload.EmbeddedWritableDirs` +
`EmbeddedSharedDirs` — the union deliberately is NOT selection-gated, so a
`host_files` entry can never claim a path a pack added tomorrow needs; this was
`agents.AllOverlayDirs` in the deleted registry), plus the
touch-file mountpoints, and three **relative** symlinks — `.claude.json →
.claude/claude.json`, `.gitconfig → .config/git/config`, `.bashrc →
.config/bashrc` (ensure.go:94-102). The trick: the base is read-only, but these
symlinks resolve *through the mount table* into per-workspace rw overlays, so
tools that atomic-rename those files (Claude rewrites `~/.claude.json`
constantly) land their writes in a writable mount. `EnsureSymlink` migrates a
pre-existing regular file's data into the target before re-linking
(ensure.go:113-141).

### 4.2 Shared credentials (claude's is the live case)

One OAuth credential per host, shared by all jails: the entrypoint makes
`~/.claude/.credentials.json` a **relative** symlink to
`../.claude-shared-credentials/.credentials.json` — relative so it resolves
through whichever mount backs `~/.claude` (the per-workspace overlay) into the
separately mounted shared dir.

Neither the file nor the dir is named in Go any more. Both come from the pack's
`shared_credentials` hook (`file` + `sharedDir`), applied by
`Env.linkSharedCredential` (`internal/entrypoint/packhooks.go`), which refuses a
`sharedDir` the pack did not also declare in `sharedDirs`. The symlink decision
itself is `Env.linkThroughShared` (`internal/entrypoint/claude.go`), and every
decision it returns is logged to `~/.yolo-shared-creds.log`.

**The harvest is gone** (2026-08-17, `pack-code-separation.md` §5/OQ-3), and with
it this section's old claim to hold the codebase's **one sanctioned tmp+rename**.
A pre-existing regular file used to be merged into the shared one by max
`expiresAt` over claude's `claudeAiOauth` dict — a claude-schema merge inside a
generically-named hook, which did nothing for the second consumer whose token is
shaped differently. The rule is now schema-blind: **the shared file always wins**;
a local file is copied out only if the shared one is *empty*, and otherwise
discarded. The accepted failure mode (a revoked shared credential outliving a
fresh local login, fixed by `rm`-ing the shared file and logging in once more) is
documented at `linkThroughShared`.

Host side, `EnsureGlobalStorage` migrates the old single-file location and touches
the shared file (ensure.go:69-80); the OAuth broker loophole reads the same path.

### 4.3 claude.json seed sync

`SyncClaudeJSONSeed` (internal/storage/claudejson.go:27-56), run in
`prepareWsState` when claude is selected (prepare.go:153-156): forward
(seed→workspace) fills only missing keys; reverse (workspace→seed) fires only
when the workspace has a truthy `oauthAccount` the seed lacks, and copies
**only** `oauthAccount` + `hasCompletedOnboarding` — `mcpServers`/`projects`
never leak into the shared seed. Parse/IO errors degrade to no-ops
(claudejson.go:61-74).

### 4.4 History isolation

`~/.claude/history.jsonl` is symlinked to
`~/.claude/jail-history/<sha256(YOLO_HOST_DIR)[:12]>.jsonl`
(`Env.isolateHistoryFile`, internal/entrypoint/packhooks.go, driven by the pack's
`per_jail_history` hook; `YOLO_HOST_DIR` set at assemble.go:392). Belt-and-braces:
even where a `.claude` dir is shared across workspaces (Apple Container's
single writable home, assemble_parts.go:18-32), history stays distinct per
host workspace.

## 5. Lifecycle

**Fresh launch**: `EnsureGlobalStorage` runs first, before config load
(run.go:34,81-88); then (run.go:95-327) config approval → workspace flock
(run.go:137-153) → remove stale stopped container (lifecycle.go:98-111) →
image autoload → `prepareWsState` (run.go:173) → assemble argv →
`run --rm -i --init --read-only ...` (assemble.go:127). The in-container command is wrapped with provisioning:
`mise install` (no upgrade — resolution happens on install only), `~/.yolo-bootstrap.sh`,
`~/.yolo-venv-precreate.sh`, optional store prune gated on
`YOLO_STORE_PRUNE_OK=1` (only when no other jail is live), then the target
command (internal/cli/run/command.go:15-49, 56-92; run.go:194-210).

**Reuse/attach** (run.go:116-129, 331-361): `rt exec -i [-t] <identityEnv>
<cname> yolo-entrypoint <cmd>` — no `prepareWsState`, no provisioning wrapper,
no mount changes. But the entrypoint re-runs its whole generator sequence
inside the exec (boot.go:392-500) — idempotent by design: identical content →
identical bytes on the same inode; shim dir wiped and rebuilt; managed-MCP
sidecars reconcile; the jail-daemon supervisor is guarded by a tmpfs PID-file
liveness probe (runtime.go:87-128); port forwarding skips already-bound ports
(runtime.go:150-157).

**Every invocation (including attach)** re-runs `refreshJailBriefings`
host-side (run.go:121-122), rewriting the staged briefings/skills with
inode-preserving writes so live single-file mounts inside running jails update.

**Restart**: `--rm` means the container layer never survives; everything that
matters is on host bind mounts, so a restart is just fresh-launch semantics
with warm caches (tools in `~/.local`, `/mise`, `~/.cache` already present —
the bootstrap script is idempotent and skips installed tools).

**Storage layout migration**: `MigrateStorageLayout` (ensure.go:208-235)
stamps `layout-version` = 2; it prunes dangling host-mise symlinks only when a
`canReclaim` callback allows — the run command wires `func() bool { return
false }` (conservative; run.go:81-88), so pruning defers until nothing is live.

## 6. Ownership and UIDs

Docker is **removed** — `runtime: "docker"` is a validation error
(internal/config/validate.go:106-108); resolvable runtimes are `podman`,
`container` (Apple Container), `macos-user` (internal/cli/run/preflight.go:89-118).
Any "-u UID:GID docker" language in older docs is stale.

- The container user is **root (UID 0)**, home `/home/agent` (flake.nix:674).
- **Podman rootless, normal branch**: `--uidmap 0:0:1 --uidmap 1:1:65536` (+
  matching gidmaps), `/dev/fuse`, caps SYS_ADMIN/MKNOD/NET_ADMIN/NET_RAW
  (assemble_parts.go:102-117). Under rootless podman, intermediate ID 0 is the
  invoking host user — so **container root == host user**, and every write to a
  bind mount lands host-side owned by you. UIDs 1..65536 map into the subuid
  range, enabling nested podman. The image ships `root:100000:65536` in
  /etc/subuid+subgid for the *inner* nesting level (flake.nix:442-443).
- **NVIDIA GPU branch**: same identity uidmap but `--runtime runc`, no fuse/
  MKNOD (assemble_parts.go:89-101).
- **Nested** (detected via `/run/.containerenv` or `/.dockerenv`,
  assemble.go:183): `--userns host`, no uidmap — doubly-nested userns fails on
  /proc (assemble_parts.go:74-87).
- **Apple Container**: no uidmap/userns flags at all (assemble_parts.go:18-32).

There is no chown anywhere in the run path. Ownership preservation is purely
the rootless mapping plus the fact that every mount source is mkdir'd/touched
by the host-side CLI process itself (ensure.go:39-91; prepare.go:131-183).
One wrinkle: pre-existing file mountpoints may carry restrictive perms from a
prior container's UID mapping and are deliberately left alone (ensure.go:82-91).

## 7. Gotchas

1. **Truncate-in-place, never tmp+rename** (`WriteInPlace`,
   internal/entrypoint/fsx.go:7-13,35-40). A file→file bind mount pins the
   inode captured at container start; a rename swaps in a new inode the mount
   can't see, and running jails silently stop seeing refreshes. The fsx
   header codifies the ban on rename-writes outside fsx (enforced by
   convention/review, not tooling — `os.Rename` legitimately remains in
   non-mount-visible paths like image autoload and prune). **There is no
   exception left for mount-visible files**: the one that used to be listed here
   was the credentials harvest's tmp+rename into a rw *directory* mount, and the
   harvest is deleted (§4.2, 2026-08-17). The rule is exceptionless for
   mount-visible files — don't reintroduce one by reading this gotcha as
   permission.

   > [!WARNING]
   > **Corrected 2026-08-23: "`os.Rename` no longer appears anywhere in
   > `internal/entrypoint`" is false.** There is exactly one call —
   > `internal/entrypoint/bootlog.go:90`, rotating the boot log to
   > `bootLogPrevName`. It does **not** violate the rule (the boot log is not a
   > bind-mounted content file), so the *rule* stands unchanged. But the
   > absolute phrasing had become a grep-checkable claim that fails, and a
   > reader who runs the grep and finds a hit will reasonably conclude the rule
   > lapsed. State the rule as "no rename-writes to mount-visible files", which
   > is what it always meant, rather than as a count of `os.Rename`.
2. **Never remove a mount-anchor directory** (`ClearContents`,
   fsx.go:14-17,49-63) — removing the dir detaches the mount (2026-07-04
   regression). Empty contents in place instead. `~/.yolo-shims` and
   `~/.yolo-launchers` are the noted exceptions: dir mounts whose *contents* are
   wiped every boot while the dirs themselves survive (`resetAnchorDir`).
3. **Symlinks are relative and compared as raw link strings**, never resolved
   (fsx.go:19-21) — resolution must happen through the container's mount
   table, not the host's. (One exception: the claude `history.jsonl` link is
   absolute — created and resolved entirely inside the jail,
   packhooks.go's `isolateHistoryFile`.)
4. **Stale-wrapper cleanup**: every boot removes regular-file
   `~/.local/bin/yolo` / `yolo-ps` (older entrypoints wrote Python scripts
   there; the Go binaries are baked into the image now) plus shim-dir
   leftovers (`_yolo_bootstrap.py`, `_yolo_python`, …)
   (internal/entrypoint/scripts.go:19-38). If you see mysterious old-yolo
   behavior, check whether this cleanup ran.
5. **The shared-home render fight**: GLOBAL_HOME is `:ro` and every jail's
   entrypoint regenerates agent configs into *per-workspace* overlays on every
   exec. Anything that ends up genuinely shared (base symlink targets,
   `~/.cache`, `/mise`, shared credentials) must be either append-only,
   convergent, or single-writer — two jails regenerating the same shared file
   with different inputs would fight. This constraint is the main driver behind
   the settings-composition redesign; see
   **docs/plans/agent-settings-composition.md** (the Prism RFC) for where
   host↔jail settings merging (today: three-way merge for claude/pi only,
   claude.go:149-207; nothing for gemini/copilot/opencode/codex) is headed.
6. **`mise hook-env` is never run at boot** (flock deadlock: hook-env spawns
   uv via the mise shim, which *is* mise; boot.go:496, shell.go:112-119).
   Interactive shells get `mise activate`-style hooks from the generated
   bashrc instead.
7. **Several different PATHs exist, and the authority is `BootPath`**
   (`internal/entrypoint/boot.go:356-361`), applied by `execBash` at `:367`.
   Verified 2026-08-23, exactly:

   ```
   ShimDir : NpmBin : MiseShims : GoBin : LocalBin : /bin : /usr/bin : LauncherDir
   ```

   i.e. `$HOME/.yolo-shims`, `$NPM_CONFIG_PREFIX/bin`, `<mise-shims>`,
   `$GOPATH/bin`, `$HOME/.local/bin`, `/bin`, `/usr/bin`,
   `$HOME/.yolo-launchers`. The two generated dirs sit at **opposite ends** on
   purpose (§3): blockers must precede the real binary, lazy installers must not.

   > [!WARNING]
   > **Two published spellings of this PATH are wrong, in different ways
   > (measured 2026-08-23).** This gotcha used to end at `/usr/bin`, **omitting
   > `~/.yolo-launchers` entirely** — which is the whole point of the
   > opposite-ends design, so the omission read as if launchers were early.
   > And `AGENTS.md`'s "PATH order (exact)" line puts `$HOME/.local/bin`
   > **second** and `$NPM_CONFIG_PREFIX/bin` third; the live order is npm →
   > mise-shims → go → **local**. AGENTS.md is right only that
   > `.yolo-launchers` is last. `boot.go:343` asserts that AGENTS.md "mirrors"
   > `BootPath`; it does not. **`BootPath` is the authority** — the doc comment
   > naming its mirror is itself the stale artifact.

   The other PATHs still differ and still must not be assumed to agree: the
   bashrc PATH (`.local/bin` second, shell.go:128-132 — the export itself is at
   `shell.go:130`) and the bootstrap-script PATH (`shell.go:206`). *(Both anchors
   corrected 2026-08-23: the old `shell.go:104-110` points at the TLS
   trust-store block and `shell.go:159` at a blank line.)*

   > [!WARNING]
   > **A fourth PATH has drifted from `BootPath` and it is a real
   > inconsistency** (found 2026-08-23). `boot.go:520-523` sets a second,
   > near-duplicate PATH pre-exec for the mise-trust subprocess — and it
   > **omits `e.LocalBin()`**. Same list otherwise, including `LauncherDir` last.
   > So a tool resolvable from `~/.local/bin` under the agent's PATH is *not*
   > resolvable in that subprocess. Neither list is generated from the other.
8. **Apple Container is structurally different**: whole-`ws` rw home, no `:ro`
   enforcement for context mounts, single-file mounts materialized. Test AC
   paths separately when touching mount assembly.
