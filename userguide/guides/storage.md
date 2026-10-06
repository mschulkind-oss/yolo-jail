# Storage

yolo keeps state on your host for your jails: logins, installed tools, caches and one home folder
per project. This page covers what survives a restart, how to see and reclaim disk, and how to move
a large cache to another disk.

## What persists across restarts

A jail's own files are thrown away when it stops, except for these, which are kept on your host:

| What | Where on the host | Shared by |
|---|---|---|
| Claude and Antigravity (`agy`) logins | `~/.local/share/yolo-jail/home/` | Every project on the machine |
| The ChatGPT login for Codex and pi | Held by yolo's login service; see [Logins](authentication.md) | Every project on the machine |
| `gh`, `copilot`, `opencode` and `omp` logins | `<project>/.yolo/home/` | One project |
| Tools you install yourself (`npm -g`, `go install`, `uv tool`) | `<project>/.yolo/home/` | One project |
| Agent sessions and history, shell history, SSH keys you create | `<project>/.yolo/home/` | One project |
| mise tools and runtimes | `~/.local/share/yolo-jail/mise/` on Linux; a volume inside Podman's VM on a Mac | Every project |
| mise tools and runtimes, on Apple Container | A disk of the project's own, in Apple Container's storage | One project |
| Download and build caches (`~/.cache` in the jail) | `~/.local/share/yolo-jail/cache/` | Every project |

Everything under `<project>/.yolo/` belongs to that project's jail; `yolo init` adds it to
`.gitignore`. The mise store is yolo's own: your host's mise installation is never mounted, so
neither side can break the other's tools.

The rest of the jail's home is rebuilt at every start from your config and packs: agent settings
files, skills, house rules, blocked-tool scripts and the shell profile. Edits to those do not
survive unless they are recorded; see [Settings Across Agents](agent-settings.md).

A folder the jail should keep that is not listed here, such as `~/.bun`, can be made writable and
kept per project with `writable_home_dirs`.

## See and reclaim disk

```bash
yolo stores          # every store yolo keeps: its size, how fast it grows, and what reclaims it
yolo prune           # what yolo would reclaim: old images, stale jails, caches, unused tools; deletes nothing
yolo prune --apply   # reclaim it
```

`yolo stores` never deletes anything. Its last column says who reclaims each store: `yolo` means
yolo does, `human` means you decide, and `not yolo's` means the bytes are another tool's. Container
images that have lost their name are always yours to remove, with `podman image prune`, because yolo
cannot tell they were ever its own.

yolo also reclaims most stores by itself, at most once a day each, after a jail starts. That work
never prints to your terminal, which by then belongs to the jail; it is logged in
`<project>/.yolo/housekeeping.log`. Set `YOLO_NO_AUTO_IMAGE_REAP=1` to turn it off.

Two kinds of files yolo will not remove without asking, because getting them back means
downloading them again: shared build-cache files older than 30 days, and mise tool versions that no
jail on the machine has used for 30 days. A launch in a terminal offers them once there is at least
a gigabyte to reclaim. Answering "yes" removes them and makes that cleanup automatic from then on,
and "never" stops the asking.

Each jail records which tool versions it uses, and yolo keeps a version any jail used in the last
30 days, whichever project it was in. Because it judges from those records, the first tool versions
are offered 30 days after you upgrade. On a Mac yolo does not clean up tool versions yet: the store
lives inside the VM, or in the sandbox account on `macos-user`.

On Apple Container each project keeps its own disk for mise's tools, so two projects' jails can
run at once, and a project's first jail downloads its tools again. `yolo stores` lists each disk
with its project and size, and `yolo prune --apply` removes the disk of a project whose folder is
gone. `yolo prune` does not see stopped Apple Container jails; list them with `container ls --all`
and remove one with `container rm <name>`.

## Timezone

The jail uses your host's timezone, so `date` and log timestamps match. yolo takes it from `$TZ` if
you set one, then from `/etc/timezone`, then from where `/etc/localtime` points, and falls back to
UTC. To override it for one launch, export `TZ` before running `yolo`.

## Relocating a Cache Subdir to Other Storage

Every jail shares one cache folder, `~/.local/share/yolo-jail/cache`, mounted read-write at `~/.cache`
inside the jail. It lives on the same disk as your home, and some of its subfolders, such as model
downloads, get very large. `cache_relocations` puts one subfolder on a different disk instead:

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

- **User config only.** A project config is editable from inside the jail, so it must not be able to
  hand out a writable host folder. `yolo check` refuses the key in `yolo-jail.jsonc`.
- **Podman only.** Apple Container and `macos-user` skip the relocation with a warning, and a symlink
  is no workaround on either.

The target's **parent** must already exist; yolo creates only the last folder. That way a typo such as `/data/relcoated/…` fails instead of quietly creating an empty folder on your main disk.

### When it's worth it

Relocate caches that are **large, cold, and write-once/read-sequential** — nothing about them wants to be on NVMe:

| Subdir | Relocate? | Why |
|--------|-----------|-----|
| `huggingface` | **Yes** | Model repos, tens of GiB each, downloaded once and then read sequentially |
| `ms-playwright` | **Yes** | Browser bundles, ~400 MiB apiece, written on install and never again |
| `uv`, `pip`, `go-build` | **No** | Touched on every build and latency-sensitive — moving them to a slow disk makes every build slower |

`yolo prune` prints a `cache/ top 5` panel to help you find your largest cache subfolders.

### Migrating an existing cache

Setting the key on a fresh machine needs nothing else. Moving a cache that already has bytes in it is a manual copy for now — do it in this order:

1. **Stop every jail first.** `yolo ps` lists what is running; exit each session, or run `yolo stop` in each project. A running jail keeps the old folder mounted whatever you change, so once step 7 deletes it, the agent inside would see its cache vanish mid-session.

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

### Symlinking a cache subdir does not work

It is tempting to skip all of the above and just `ln -s /data/… ~/.local/share/yolo-jail/cache/huggingface`. It does not work, and it fails confusingly.

The whole cache directory is bind-mounted into the container as one unit, and podman resolves the **source path** of that mount — not the symlinks inside it. The container therefore gets a symlink pointing at `/data/…`, a path that does not exist in the container's mount namespace. Every in-jail download then fails on a dangling path, while the same symlink resolves perfectly when you `ls` it on the host.

The one exception is `cache/images`, which only the host reads, so symlinking it is safe. Nothing else in the cache is.

