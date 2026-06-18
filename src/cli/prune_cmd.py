"""``yolo prune`` — disk reclaim command body.

Hardlink-dedup across workspaces, drop stopped containers, sweep old
images and the image-tar cache, age-purge re-downloadable cache
subtrees, and cleanup overlay-shadowed seed subtrees.

Defaults to dry-run; pass --apply to actually reclaim.

The Typer command is registered in cli/__init__.py — this module just
exports the function body and the _fmt_bytes helper so cli/__init__.py
can use it from check() without circular imports.
"""

from typing import List

import typer

from .console import console
from .paths import GLOBAL_CACHE, GLOBAL_HOME, GLOBAL_STORAGE
from .runtime import _detect_runtime


def _fmt_bytes(n: int) -> str:
    """Human-readable byte count: 1536 → '1.5 KiB', 1_500_000_000 → '1.4 GiB'."""
    units = ("B", "KiB", "MiB", "GiB", "TiB")
    size = float(n)
    i = 0
    while size >= 1024 and i < len(units) - 1:
        size /= 1024
        i += 1
    if i == 0:
        return f"{int(size)} {units[i]}"
    return f"{size:.1f} {units[i]}"


def prune_cmd(
    apply: bool = typer.Option(
        False,
        "--apply",
        help="Actually reclaim space.  Without this flag, prune prints what "
        "it WOULD do and exits (safe default).",
    ),
    no_hardlink: bool = typer.Option(
        False,
        "--no-hardlink",
        help="Skip the cross-workspace hardlink dedup pass.",
    ),
    dedup_global: bool = typer.Option(
        False,
        "--dedup-global",
        help="Also hardlink-dedupe inside the shared global cache/mise/home "
        "subtrees.  Opt-in because these can be hundreds of GiB and the "
        "scan takes real time — but that's where the duplicate wheels "
        "live.",
    ),
    no_containers: bool = typer.Option(
        False,
        "--no-containers",
        help="Skip the stopped-container cleanup.",
    ),
    no_images: bool = typer.Option(
        False,
        "--no-images",
        help="Skip the old-image cleanup.",
    ),
    keep_images: int = typer.Option(
        2,
        "--keep-images",
        help="Number of most-recent yolo-jail images to retain (default: 2).",
    ),
    no_image_cache: bool = typer.Option(
        False,
        "--no-image-cache",
        help="Skip the ~/.cache/images/ tarball cleanup.",
    ),
    no_build_roots: bool = typer.Option(
        False,
        "--no-build-roots",
        help="Skip reclaiming orphaned nix-build-root.old.* generations. "
        "These are left aside by the in-jail-repo repopulate dance and are "
        "only swept once no running jail still binds them into "
        "/opt/yolo-jail (with an age grace floor for jails mid-startup).",
    ),
    no_shadowed_home: bool = typer.Option(
        False,
        "--no-shadowed-home",
        help="Skip the shadowed-seed cleanup.  By default, prune deletes "
        "subdirs of the :ro GLOBAL_HOME seed that are fully masked by "
        "overlay mounts at runtime (.cache, .npm, .npm-global, .local, "
        "go).  These can never be read by any live jail but accumulate "
        "tens of GiB from pre-cache-split installs.",
    ),
    image_cache_keep: int = typer.Option(
        3,
        "--image-cache-keep",
        help="Number of most-recent cached image tarballs to retain "
        "under ~/.cache/images/ (default: 3).  Each tar is ~3 GiB, so "
        "this bucket dominates disk use — it's the single biggest win "
        "for most users.  Orphan .tmp files from crashed builds are "
        "always swept regardless of this count.",
    ),
    cache_age: int = typer.Option(
        30,
        "--cache-age",
        help="Purge files under re-downloadable ~/.cache/ subdirs "
        "(uv, pip, npm, go-build, mise, pex, pants, node-gyp, gopls) "
        "older than this many days.  Pass 0 to skip the pass entirely; "
        "pass a smaller number to be more aggressive.  Content is "
        "re-downloadable from PyPI/npm/go/mise on next install.",
    ),
    purge_heavy_caches: bool = typer.Option(
        False,
        "--purge-heavy-caches",
        help="With --cache-age, also purge playwright browsers + huggingface "
        "models older than the cutoff.  Re-download cost is significant "
        "(~400 MiB per browser, multi-GiB per HF model) — opt-in.",
    ),
):
    """Reclaim disk space: hardlink-dedup, drop stale containers + old images.

    Defaults to dry-run — nothing on disk changes unless you pass --apply.
    Only touches yolo-* containers, yolo-jail images, and files under
    ``<workspace>/.yolo/home/{npm-global,local,go}``.  Browser profile
    dirs in the cache (chromium/firefox families) are NEVER touched by
    the age-based purge — those carry live user state.
    """
    from src import prune as _prune

    runtime = _detect_runtime()
    workspaces = _prune._find_yolo_workspaces(runtime)

    mode = "APPLY" if apply else "DRY-RUN"
    console.print(f"[bold]yolo prune ({mode})[/bold]")
    console.print(f"Runtime: {runtime}  Workspaces tracked: {len(workspaces)}")
    for ws in workspaces:
        console.print(f"  • {ws}")
    if not workspaces:
        console.print(
            "[dim]No yolo-* containers found — nothing to dedupe across.[/dim]"
        )

    # --- Pre-report ---
    before = _prune._disk_usage_report(
        workspaces=workspaces, global_storage=GLOBAL_STORAGE
    )
    console.print(
        f"\n[bold]Current usage[/bold]  total={_fmt_bytes(before['total'])}  "
        f"(workspaces={_fmt_bytes(before['workspaces'])}, "
        f"global={_fmt_bytes(before['global_storage'])})"
    )
    breakdown = before.get("breakdown") or {}
    if breakdown:
        console.print("  [dim]global-storage breakdown (largest first):[/dim]")
        for name, size in sorted(breakdown.items(), key=lambda kv: kv[1], reverse=True):
            console.print(f"    {name:<20} {_fmt_bytes(size):>12}")

    # When the cache bucket dominates, break it down too — saves the
    # operator from running `du -sh` manually to find the fat subdir.
    cache_breakdown = before.get("cache_breakdown") or {}
    if cache_breakdown:
        top = sorted(cache_breakdown.items(), key=lambda kv: kv[1], reverse=True)[:5]
        console.print("  [dim]cache/ top 5 (largest first):[/dim]")
        for name, size in top:
            console.print(f"    cache/{name:<14} {_fmt_bytes(size):>12}")

    # Hint: the image tar cache is almost always the biggest offender
    # and an ideal candidate for cold storage (HDD).  Surface it once,
    # proactively, when it exceeds a reasonable SSD budget.
    images_bytes = cache_breakdown.get("images", 0)
    if images_bytes >= 20 * (1024**3):  # 20 GiB
        console.print(
            f"  [yellow]hint:[/yellow] cache/images holds "
            f"{_fmt_bytes(images_bytes)} of jail tarballs.  They're "
            "streamed once at podman load then unused — consider "
            "symlinking this subdir to HDD storage if you have it."
        )

    total_saved = 0
    total_links = 0
    removed_containers: list[str] = []
    removed_images: list[str] = []
    image_cache_bytes = 0
    image_cache_files = 0

    if not no_hardlink and (workspaces or dedup_global):
        console.print("\n[bold]Hardlink dedup[/bold]")
        from rich.progress import (
            BarColumn,
            MofNCompleteColumn,
            Progress,
            SpinnerColumn,
            TextColumn,
            TimeElapsedColumn,
            TimeRemainingColumn,
        )

        entries: list = []
        # Walk phase: unknown total, show indeterminate spinner.
        with Progress(
            SpinnerColumn(),
            TextColumn("[bold]{task.description}[/bold]"),
            TextColumn("[dim]{task.completed:,} files scanned[/dim]"),
            TimeElapsedColumn(),
            console=console,
            transient=True,
        ) as prog:
            task = prog.add_task("scanning", total=None)
            if workspaces:
                for e in _prune._walk_dedupable_files(workspaces):
                    entries.append(e)
                    prog.advance(task)
            if dedup_global:
                for e in _prune._walk_global_dedupable(GLOBAL_STORAGE):
                    entries.append(e)
                    prog.advance(task)
        console.print(f"  candidate files: {len(entries):,}")
        if dedup_global:
            console.print("  [dim]scope: workspaces + global cache/mise/home[/dim]")
        else:
            console.print(
                "  [dim]scope: workspaces only  (pass --dedup-global to include "
                "the shared caches)[/dim]"
            )
        # Dedup phase: we don't know how many links we'll make until
        # we've hashed, so the bar tracks decisions-made as they land.
        # Total is unknown → spinner-like bar, but with a counter.
        with Progress(
            SpinnerColumn(),
            TextColumn("[bold]{task.description}[/bold]"),
            BarColumn(),
            MofNCompleteColumn(),
            TimeElapsedColumn(),
            TimeRemainingColumn(),
            console=console,
            transient=True,
        ) as prog:
            task = prog.add_task("deduping", total=None)

            def cb(advance: int = 1):
                prog.advance(task, advance)

            saved, links = _prune._hardlink_duplicate_files(
                entries, apply=apply, progress_cb=cb
            )
        verb = "would save" if not apply else "saved"
        console.print(f"  {verb}: {_fmt_bytes(saved)} across {links:,} hardlinks")
        total_saved += saved
        total_links += links

    if not no_containers:
        console.print("\n[bold]Stopped yolo-* containers[/bold]")
        removed_containers = _prune._prune_stopped_containers(runtime, apply=apply)
        verb = "would remove" if not apply else "removed"
        if removed_containers:
            console.print(f"  {verb}: {len(removed_containers)}")
            for name in removed_containers:
                console.print(f"    • {name}")
        else:
            console.print("  [dim]none[/dim]")

    if not no_images:
        console.print(f"\n[bold]Old yolo-jail images[/bold]  (keep={keep_images})")
        removed_images = _prune._prune_old_images(
            runtime, keep=keep_images, apply=apply
        )
        verb = "would remove" if not apply else "removed"
        if removed_images:
            console.print(f"  {verb}: {len(removed_images)}")
            for img in removed_images:
                console.print(f"    • {img}")
        else:
            console.print("  [dim]none[/dim]")

    if not no_image_cache:
        console.print(
            f"\n[bold]Cached image tarballs[/bold]  (keep={image_cache_keep})"
        )
        image_cache_bytes, image_cache_files = _prune._prune_image_cache(
            GLOBAL_CACHE / "images",
            keep=image_cache_keep,
            apply=apply,
        )
        verb = "would remove" if not apply else "removed"
        if image_cache_files:
            console.print(
                f"  {verb}: {_fmt_bytes(image_cache_bytes)} across "
                f"{image_cache_files:,} file(s)"
            )
        else:
            console.print("  [dim]none[/dim]")
        total_saved += image_cache_bytes

    build_root_bytes = 0
    build_root_dirs = 0
    if not no_build_roots:
        console.print("\n[bold]Orphaned build-root generations[/bold]")
        # Liveness gate: collect build roots still bound into /opt/yolo-jail
        # by a running jail so the sweep never unlinks an in-use inode.
        # `None` means the runtime couldn't be enumerated → sweep declines.
        referenced = _prune._find_referenced_build_roots(runtime)
        if referenced is None:
            console.print(
                "  [dim]skipped — could not enumerate running jails "
                f"({runtime}); declining to sweep[/dim]"
            )
        else:
            build_root_bytes, build_root_dirs = _prune._prune_orphan_build_roots(
                GLOBAL_STORAGE,
                referenced=referenced,
                # Grace floor well past any jail's resolve→podman-bind window.
                older_than_seconds=3600,
                apply=apply,
            )
            verb = "would remove" if not apply else "removed"
            if build_root_dirs:
                console.print(
                    f"  {verb}: {_fmt_bytes(build_root_bytes)} across "
                    f"{build_root_dirs:,} generation(s)"
                )
            else:
                console.print("  [dim]none[/dim]")
        total_saved += build_root_bytes

    shadowed_bytes = 0
    shadowed_items = 0
    if not no_shadowed_home:
        console.print("\n[bold]Shadowed seed subtrees[/bold]")
        console.print(
            f"  [dim]targets: {', '.join(_prune.SHADOWED_HOME_PATHS)} "
            "(each overlay-masked at runtime)[/dim]"
        )
        shadowed_bytes, shadowed_items = _prune._prune_shadowed_home(
            GLOBAL_HOME, apply=apply
        )
        verb = "would remove" if not apply else "removed"
        if shadowed_items:
            console.print(
                f"  {verb}: {_fmt_bytes(shadowed_bytes)} across "
                f"{shadowed_items:,} path(s)"
            )
        else:
            console.print("  [dim]none[/dim]")
        total_saved += shadowed_bytes

    cache_bytes = 0
    cache_files = 0
    if cache_age > 0:
        subdirs: List[str] = list(_prune.CACHE_PURGE_DEFAULT_SUBDIRS)
        if purge_heavy_caches:
            subdirs.extend(_prune.CACHE_PURGE_HEAVY_SUBDIRS)
        console.print(
            f"\n[bold]Cache purge[/bold]  (subdirs={','.join(subdirs)}, "
            f"age > {cache_age}d)"
        )
        # cache lives at GLOBAL_STORAGE/cache
        cache_bytes, cache_files = _prune._purge_cache_by_age(
            GLOBAL_STORAGE / "cache",
            subdirs=subdirs,
            older_than_days=cache_age,
            apply=apply,
        )
        verb = "would remove" if not apply else "removed"
        console.print(
            f"  {verb}: {_fmt_bytes(cache_bytes)} across {cache_files:,} files"
        )
        total_saved += cache_bytes

    console.print()
    if apply:
        console.print(
            f"[bold green]Reclaimed {_fmt_bytes(total_saved)}[/bold green] via "
            f"{total_links:,} hardlinks, {len(removed_containers)} container(s), "
            f"{len(removed_images)} image(s), {image_cache_files:,} image tar(s), "
            f"{build_root_dirs:,} build-root generation(s), "
            f"{shadowed_items:,} shadowed seed path(s), "
            f"{cache_files:,} cache file(s)."
        )
    else:
        console.print(
            f"[bold yellow]DRY-RUN:[/bold yellow] would reclaim "
            f"{_fmt_bytes(total_saved)} via {total_links:,} hardlinks, remove "
            f"{len(removed_containers)} container(s), "
            f"{len(removed_images)} image(s), "
            f"{image_cache_files:,} image tar(s), "
            f"{build_root_dirs:,} build-root generation(s), "
            f"{shadowed_items:,} shadowed seed path(s), "
            f"{cache_files:,} cache file(s).  "
            f"Re-run with [cyan]--apply[/cyan] to execute."
        )
