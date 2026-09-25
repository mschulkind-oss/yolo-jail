# Storage

## Storage & Persistence

### Seeing what is on disk — `yolo stores`

```bash
yolo stores            # the inventory: every store, its size, who reclaims it, and what nothing does
yolo stores --no-record  # ...without appending a dated sample
```

It **deletes, moves and mutates nothing** — the one exception is a bounded sample ledger (one dated
line per store per run, last 30 kept) so that a second run can report a growth RATE rather than just
a size. `--no-record` opts out of that.

The column worth reading is the last one. `yolo` means yolo has a reclaimer and a trigger for that
store; `human` means it does not and you decide; `not yolo's` means it is somebody else's bytes.
Untagged container images are the standing `human` row: once an image loses its repository name,
yolo has no evidence it was ever yolo's, so it never removes one — `podman image prune` is yours to
run if you want them gone.

Reclaiming is `yolo prune` (dry-run by default, `--apply` to act). Most classes are also reclaimed
automatically after a jail starts, at most once a day each; `YOLO_NO_AUTO_IMAGE_REAP=1` turns that
off. **That automatic work never prints to your terminal** — by the time it runs, the terminal
belongs to whatever is running in the jail, and a line there would land on top of it. It is
recorded in `<workspace>/.yolo/housekeeping.log` instead, beside `boot.log`:

```bash
tail ~/code/myproject/.yolo/housekeeping.log
```

The one class yolo will not reclaim without asking is the shared build-tool cache, because
re-fetching it is unbounded — you will be offered it on a TTY launch when there is at least a
gigabyte of it older than 30 days, and answering "never" stops the asking for good.

All paths below use the same layout on Linux and macOS (`~/.local/share/yolo-jail/` resolves to `/home/$USER/.local/share/yolo-jail/` on Linux and `/Users/$USER/.local/share/yolo-jail/` on macOS). On macOS with Podman Machine, make sure `$HOME` is in the VM's shared folders list.

### Timezone

The host's timezone is passed into the jail via the `TZ` env var, so `date`, log timestamps, cron expressions, and file mtimes inside the jail report the same wall-clock time as the host. Detection order:

1. `$TZ` on the host (if you've explicitly set one, it wins)
2. `/etc/timezone` plain-text zone name (Debian, Ubuntu, Arch)
3. `/etc/localtime` symlink target suffix (Fedora, macOS — `/var/db/timezone/zoneinfo/<zone>`)

If none of these resolve, the jail falls back to UTC. Override per-jail by exporting `TZ` in the shell you use to launch `yolo`, or by setting it in `env` inside `yolo-jail.jsonc`.

### What Persists Across Restarts

| Data | Location (Host) | Shared? |
|------|-----------------|---------|
| Claude and Antigravity (`agy`) logins | `~/.local/share/yolo-jail/home/.claude-shared-credentials/`, `.gemini-shared-credentials/` | All jails on the machine |
| `gh`, `copilot`, `opencode` and `omp` logins | `<workspace>/.yolo/home/` | Per workspace |
| Installed tools (npm, go) | `~/.local/share/yolo-jail/home/` | All jails |
| Mise tools & runtimes | `~/.local/share/yolo-jail/mise/` on Linux (bind-mounted at `/mise` inside the jail); podman named volume `yolo-mise-data-v2` on macOS and Apple Container, also mounted at `/mise` | All jails |
| Bash history | `<workspace>/.yolo/home/bash_history` | Per workspace |
| Claude sessions | `<workspace>/.yolo/home/claude/projects/` | Per workspace |
| Copilot sessions | `<workspace>/.yolo/home/copilot/session-state/` | Per workspace |
| SSH keys | `<workspace>/.yolo/home/ssh/` | Per workspace |

**Mise storage is jail-land only:** the host's `~/.local/share/mise/` is never mounted — jails and the host maintain fully independent mise installations, so neither side can break the other's tool installs and host↔jail mise version skew doesn't matter. Every jail sees the same store at the same path, `/mise`; only the backing differs per platform (a yolo-owned host directory on Linux, the `yolo-mise-data-v2` named volume on macOS and Apple Container). In-jail behavior is identical everywhere, and the store persists across jail restarts.

### What Gets Regenerated

On every jail start, the entrypoint regenerates:
- `.bashrc` — prompt, aliases, PATH, mise integration
- Shim scripts — blocked tool interceptors
- MCP config — `mcp-config.json` / `settings.json`
- LSP config — `lsp-config.json`
- Bootstrap script — tool installation (idempotent)

### Relocating a Cache Subdir to Other Storage

Every jail shares one cache directory, `~/.local/share/yolo-jail/cache`, bind-mounted read-write at `~/.cache` inside the container. One exception, and it needs no configuration: if this machine has a **content-addressed** cache yolo recognises — `~/.cache/pants/lmdb_store` today — a jail mounts *your own* copy of it, writable, in place of keeping a second one, so up to ~27 GB stops existing twice. The launch says so on stderr when it happens, `yolo stores` lists it in its own section as bytes yolo will never reclaim, and the jail's now-unused private copy is left for the ordinary 30-day cache purge. It is skipped entirely on macOS, on Apple Container, and whenever the host store is missing, unwritable, or empty while the jail's copy is warm — in every one of those cases the jail simply keeps its own copy, exactly as before. It sits on whatever filesystem `$HOME` is on, and some of its subdirs get very large. `cache_relocations` lets one subdir come from a different disk instead:

```jsonc
// ~/.config/yolo-jail/config.jsonc — user scope ONLY, never yolo-jail.jsonc
{
  "cache_relocations": {
    // cache subdir name → absolute host path
    "huggingface": "/data/relocated/yolo-jail/cache/huggingface"
  }
}
```

That mounts the target read-write at `/home/agent/.cache/huggingface`, nested inside the usual cache mount. Inside the jail it is an ordinary writable directory — `HF_HOME` and every other tool's cache path stay exactly as they are.

Two constraints worth knowing before you plan a move:

- **User scope only.** The key is read straight from `~/.config/yolo-jail/config.jsonc` and never from the merged config. `/workspace` is bind-mounted read-write into the jail, so a workspace config is agent-editable — it must not be able to hand out read-write host mounts. `yolo check` errors if the key shows up in `yolo-jail.jsonc`.
- **Podman only.** Apple Container (`runtime: "container"`) warns and skips the relocation. The `macos-user` backend has no container and no bind mounts, and since 2026-08-24 it warns too. **There is no workaround there** — an earlier version of this line suggested a plain host symlink, which does not work: that backend's sandbox profile denies writes outside your workspace, its own home, `/tmp` and `/var/folders`, and denies reads under `/Volumes`, so a symlink into other storage resolves to a path the agent cannot use.

The target's **parent** must already exist; only the last path component is created for you. That asymmetry is deliberate — auto-creating the whole path turns a typo like `/data/relcoated/…` into a silently-wrong empty directory back on the root filesystem, which is the exact failure the feature exists to prevent.

#### When it's worth it

Relocate caches that are **large, cold, and write-once/read-sequential** — nothing about them wants to be on NVMe:

| Subdir | Relocate? | Why |
|--------|-----------|-----|
| `huggingface` | **Yes** | Model repos, tens of GiB each, downloaded once and then read sequentially |
| `ms-playwright` | **Yes** | Browser bundles, ~400 MiB apiece, written on install and never again |
| `uv`, `pip`, `go-build` | **No** | Touched on every build and latency-sensitive — moving them to a slow disk makes every build slower |

The concrete case that motivated the feature: a 948 GiB root filesystem at **100% full**, with 241 GiB of it the yolo cache — and 185 GiB of *that* a single `cache/huggingface` holding about fifteen diffusers model repos (FLUX.1-schnell 32G, FLUX.1-Kontext-dev 32G, SDXL-base 20G, …). An 11 TB HDD on the same machine sat at 53%. One subdir, one line of config. `yolo prune` prints a `cache/ top 5` panel if you want to find your own equivalent.

#### Migrating an existing cache

Setting the key on a fresh machine needs nothing else. Moving a cache that already has bytes in it is a manual copy for now — do it in this order:

1. **Stop every jail first.** `yolo ps` lists what's running; exit each session (or `podman stop <name>`). This is not just about a torn copy from a download racing your `rsync`. Podman bind mounts are `rprivate`: a container's mounts are fixed when it starts, so a jail that was already up keeps the *old* host directory mounted at `~/.cache/huggingface` no matter what you change afterwards. Step 3 only copies, so nothing looks wrong yet — but once step 7 deletes that old directory, the agent inside a still-running jail sees its cache vanish mid-session, even though the bytes are safely at the new target. It reads exactly like cache corruption, and the only fix is the restart you skipped.

2. **Create the target's parent** (yolo creates the final component itself):

   ```bash
   mkdir -p /data/relocated/yolo-jail/cache
   ```

3. **Copy the bytes.**

   ```bash
   rsync -aH --info=progress2 \
     ~/.local/share/yolo-jail/cache/huggingface/ \
     /data/relocated/yolo-jail/cache/huggingface/
   ```

   `-a` is the load-bearing flag. `huggingface_hub` stores each file once under `blobs/<etag>` and points at it from `snapshots/<rev>/<file>` with a *relative* symlink; `-a` implies `-l`, so the links are copied as links and their targets stay valid at the new location. `-H` is cheap insurance for subdirs that use hard links (an HF cache uses none). What you must **not** add is `-L`/`--copy-links` — nor reach for `cp -aL`, `tar -h`, or a file manager that dereferences — since that writes a full copy behind every snapshot link, roughly doubling the transfer and more when several revisions of a repo are cached. That refills the disk you are trying to empty.

4. **Verify** before you delete anything. A second `rsync` in dry-run mode should have nothing left to do:

   ```bash
   du -sh ~/.local/share/yolo-jail/cache/huggingface /data/relocated/yolo-jail/cache/huggingface
   rsync -aHn --itemize-changes \
     ~/.local/share/yolo-jail/cache/huggingface/ \
     /data/relocated/yolo-jail/cache/huggingface/
   ```

5. **Set the config** in `~/.config/yolo-jail/config.jsonc` as shown above, then `yolo check` — it validates the key's scope, the subdir names, and that each target's parent exists.

6. **Restart your jails** and confirm from inside one that `~/.cache/huggingface` still has the models and is writable.

7. **Reclaim the space.** Only now delete the original: `rm -rf ~/.local/share/yolo-jail/cache/huggingface`. yolo recreates it as an empty stub mountpoint on the next start.

#### Symlinking a cache subdir does not work

It is tempting to skip all of the above and just `ln -s /data/… ~/.local/share/yolo-jail/cache/huggingface`. It does not work, and it fails confusingly.

The whole cache directory is bind-mounted into the container as one unit, and podman resolves the **source path** of that mount — not the symlinks inside it. The container therefore gets a symlink pointing at `/data/…`, a path that does not exist in the container's mount namespace. Every in-jail download then fails on a dangling path, while the same symlink resolves perfectly when you `ls` it on the host.

The one exception is `cache/images`, which holds jail image tarballs. Those are only ever read host-side, before any container exists, so symlinking that subdir is safe. Nothing else in the cache is. (Nothing WRITES a tarball there any more — the image is copied layer by layer since layer-aware delivery landed — so what is left is a backlog `yolo prune` reclaims.)

---

## Container Reuse

By default, `yolo` reuses an existing container for the same workspace:

```bash
yolo             # Creates container yolo-<hash>
yolo             # Reuses yolo-<hash> via exec
yolo stop       # Stops this workspace's jail (the next launch is fresh)
```

Containers are named deterministically based on the workspace path. Use `yolo ps` to see running containers.

---
