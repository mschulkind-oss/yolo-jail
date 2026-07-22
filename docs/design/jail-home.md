# How `/home/agent` works — construction, mounts, and sharing

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
│        .npm-global/ .local/ go/ .yolo-shims/ .config/ .ssh/
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
     /opt/yolo-jail <- the yolo source tree, :ro                (assemble.go:180)
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
| `ws/yolo-shims` | `/home/agent/.yolo-shims` | rw | blocked-tool + launcher shims (:46) |
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

### 2.5 Per-agent config-dir overlays (assemble.go:152-155)

For each **selected** agent with overlay dirs: `ws/<subdir>` →
`/home/agent/.<subdir>` rw (dot stripped by `agentOverlaySubdirs`). Overlay
dirs per agent (internal/agents/agents.go): claude→`.claude`,
copilot→`.copilot`, gemini→`.gemini`, pi→`.pi`, codex→`.codex`;
opencode has none. Creation/seeding happens in `prepareWsState`
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

### 2.6 Claude shared credentials (assemble.go:156-160)

`GLOBAL_HOME/.claude-shared-credentials` → same path in-container, **rw**, only
when claude is selected (non-AC). The dir and its `.credentials.json` are
ensured/migrated host-side (ensure.go:69-80); the OAuth broker reads the same
host file. How the jail's `~/.claude/.credentials.json` reaches it: §4.2.

### 2.7 Writable home dirs (config `writable_home_dirs`)

Extra `$HOME` subpaths the user declares writable, for agent extensions that
hardcode a home path yolo doesn't manage (motivating case: pi-lens writing
`~/.pi-lens`). Each entry `<p>` is bound `ws/writable-home/<p>` →
`/home/agent/<p>` **rw**, nested inside the `:ro` base — podman auto-creates the
nested mountpoint under the read-only parent, so nothing has to pre-exist in
`GLOBAL_HOME`. Backing dirs are created in `prepareWsState`
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

### 2.7 Remaining mounts, in assembly order

Only the home-relevant ones expanded; the rest one-lined for orientation.

- **`ws/yolo-user-env.sh`** → `/home/agent/.config/yolo-user-env.sh` rw;
  written pre-assembly by `writeUserEnvFile` (run.go:176-177; mount
  assemble.go:171-176). AC: materialized.
- **In-jail CLI repo** → `/opt/yolo-jail` **ro**, always; source = the
  workspace itself when it *is* the yolo tree, else repoRoot with flake.nix,
  else workspace (`repoMountSource`, assemble_parts.go:299-307;
  assemble.go:178-180).
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
- **Skills**: per selected agent with a skills target,
  `AGENTS_DIR/<cname>/skills-<agent>` → `/home/agent/<Skills>` **ro**
  (assemble.go:315-321); targets `.claude/skills`, `.copilot/skills`,
  `.gemini/skills` (agents.go). Staging rebuilt host-side **every invocation**
  by `agents.PrepareSkills` (internal/agents/skills.go:20-53): clears staging
  contents in place (inode-preserving), writes the built-in
  `jail-startup/SKILL.md`, then copies host `~/.<agent>/skills/*` dereferencing
  symlinks.
- **Host `~/.claude` files** (claude selected): `host_claude_files` entries
  (default `["settings.json"]`, internal/config/config.go:42) plus scripts
  referenced from settings hooks/statusLine/fileSuggestion →
  `/ctx/host-claude/<fname>` **ro**
  + `YOLO_HOST_CLAUDE_FILES` manifest (`hostClaudeFileArgs`,
  internal/cli/run/hostclaude.go:16-53, 96-154). Same pattern for pi:
  `/ctx/host-pi/` (hostclaude.go:60-91).
- **Briefings**: per selected agent, `AGENTS_DIR/<cname>/<Staging>` →
  `/home/agent/<Mount>` **ro** (assemble.go:329-341). Pairs (agents.go): claude
  `CLAUDE.md`→`.claude/CLAUDE.md`, copilot `AGENTS-copilot.md`→
  `.copilot/AGENTS.md`, gemini→`.gemini/AGENTS.md`, opencode→
  `.config/opencode/AGENTS.md`, pi→`.pi/agent/AGENTS.md`, codex→
  `.codex/AGENTS.md`. Content regenerated host-side on **every** invocation by
  `refreshJailBriefings` (prepare.go:20-93; called run.go:121-122) with
  inode-preserving writes so live mounts see updates.
- **Loophole runtime mounts** (`loopholes.RuntimeArgsFor`,
  internal/loopholes/runtime.go:26-129): module dir →
  `/etc/yolo-jail/loopholes/<name>` ro + state dir →
  `/var/lib/yolo-jail/loopholes/<name>` ro (both jail_daemon-only), CA cert +
  `NODE_EXTRA_CA_CERTS`,
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
- `~/.yolo-shims/` — **RemoveAll + recreate every boot** (shims.go:24-25):
  blocked-tool shims from `YOLO_BLOCK_CONFIG`, agent lazy-install launchers
  (skipped when a blocked shim of the same name exists, shims.go:170-171),
  pnpm launcher. Safe to rmtree because it is a *directory* mount — the dir
  itself is the anchor, its contents are not.
- `~/.bashrc` (via the base's `.bashrc → .config/bashrc` symlink; truncate in
  place "for the bind mount", shell.go:46-48), `~/.yolo-bootstrap.sh`,
  `~/.yolo-venv-precreate.sh` (shell.go:141-145, 259-261),
  `~/.yolo-ca-bundle.crt` (always written, even empty; system.go:17-63).
- `~/.config/mise/config.toml` — created with base tools or surgically healed
  in place; written only when changed (mise.go:37-145).
- MCP wrappers `~/.local/bin/mcp-wrappers/{node,npx}` etc.
  (mcp_wrappers.go:7-15); `~/.local/bin/yolo-cglimit`, `yolo-journalctl`
  (scripts.go:10-17).
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
| **Per-workspace** | everything in `<workspace>/.yolo/home`: the rw overlays (`npm-global`, `local`, `go`, `yolo-shims`, `config`, `ssh`), the 8 single-file mountpoints (7 `yolo-*` files + `bash_history`), per-selected-agent config dirs |
| **Per-jail (container name)** | `containers/` tracking files, `agents/<cname>/` briefing+skills staging (paths.go:76-79), `logs/<cname>-socat.log` + `logs/broker-relay-<sha1(cname)[:8]>.log` (network.go:36; loopholesruntime.go:370) — `logs/host-service-<name>.log` is per-service, shared (loopholesruntime.go:214) |
| **Per-host-workspace inside a home** | Claude history keyed on `sha256(YOLO_HOST_DIR)[:12]` (§4.4) |
| **Per-boot / ephemeral** | `/tmp`, `/run`, `/dev/shm`, anonymous volumes, `/tmp/yolo-jaild.pid` |
| **Host-only, never mounted** | host `~/.local/share/mise` (ensure.go:143-148), host credentials generally |

### 4.1 Why GLOBAL_HOME is `:ro` with symlink escape hatches

`EnsureGlobalStorage` (ensure.go:39-108) builds the base: the **union** of all
agents' overlay dirs (`agents.AllOverlayDirs`, agents.go) plus shared dirs, the
touch-file mountpoints, and three **relative** symlinks — `.claude.json →
.claude/claude.json`, `.gitconfig → .config/git/config`, `.bashrc →
.config/bashrc` (ensure.go:94-102). The trick: the base is read-only, but these
symlinks resolve *through the mount table* into per-workspace rw overlays, so
tools that atomic-rename those files (Claude rewrites `~/.claude.json`
constantly) land their writes in a writable mount. `EnsureSymlink` migrates a
pre-existing regular file's data into the target before re-linking
(ensure.go:113-141).

### 4.2 Shared Claude credentials

One OAuth credential per host, shared by all jails: the entrypoint makes
`~/.claude/.credentials.json` a **relative** symlink to
`../.claude-shared-credentials/.credentials.json` (claude.go:273-299) —
relative so it resolves through whichever mount backs `~/.claude` (the
per-workspace overlay) into the separately mounted shared dir. A pre-existing
regular file is harvested first (OAuth token merged by max `expiresAt`,
claude.go:350-378). That harvest is the **one sanctioned tmp+rename** in the
codebase — legal only because the shared dir is a rw *directory* mount, where
rename works (fsx.go; claude.go:350-378). Host side, `EnsureGlobalStorage`
migrates the old single-file location and touches the shared file
(ensure.go:69-80); the OAuth broker loophole reads the same path.

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
(claude.go:242-271; `YOLO_HOST_DIR` set at assemble.go:392). Belt-and-braces:
even where a `.claude` dir is shared across workspaces (Apple Container's
single writable home, assemble_parts.go:18-32), history stays distinct per
host workspace.

## 5. Lifecycle

**Fresh launch**: `EnsureGlobalStorage` runs first, before config load
(run.go:34,81-88); then (run.go:95-327) config approval → workspace flock
(run.go:137-153) → remove stale stopped container (lifecycle.go:98-111) →
image autoload → `prepareWsState` (run.go:173) → assemble argv →
`run --rm -i --init --read-only ...` (assemble.go:127). The in-container command is wrapped with provisioning:
`mise trust/install/upgrade`, `~/.yolo-bootstrap.sh`,
`~/.yolo-venv-precreate.sh`, optional store prune gated on
`YOLO_STORE_PRUNE_OK=1` (only when no other jail is live), then the target
command (internal/cli/run/command.go:8-47, 54-90; run.go:194-210).

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
   non-mount-visible paths like image autoload and prune). The one exception
   for mount-visible files: the credentials harvest into a rw *directory*
   mount (§4.2).
2. **Never remove a mount-anchor directory** (`ClearContents`,
   fsx.go:14-17,49-63) — removing the dir detaches the mount (2026-07-04
   regression). Empty contents in place instead. `~/.yolo-shims` is the noted
   exception: it's a dir mount whose *contents* are wiped every boot, the dir
   itself survives.
3. **Symlinks are relative and compared as raw link strings**, never resolved
   (fsx.go:19-21) — resolution must happen through the container's mount
   table, not the host's. (One exception: the claude `history.jsonl` link is
   absolute — created and resolved entirely inside the jail, claude.go:262-270.)
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
7. **Three different PATHs** exist: the exec'd process PATH
   (`SHIM_DIR:NPM_BIN:MISE_SHIMS:GO_BIN:LOCAL_BIN:/bin:/usr/bin`,
   boot.go:355-358), the bashrc PATH (`.local/bin` second, shell.go:104-110),
   and the bootstrap-script PATH (shell.go:159). Don't assume they agree about
   `~/.local/bin` precedence.
8. **Apple Container is structurally different**: whole-`ws` rw home, no `:ro`
   enforcement for context mounts, single-file mounts materialized. Test AC
   paths separately when touching mount assembly.
