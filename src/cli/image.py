"""Container-image build/load pipeline.

Owns the path from ``nix build .#ociImage`` through cached tar
materialization to ``<runtime> load``.  The flow:

  1. _build_image_store_path runs nix and returns the resulting store path.
  2. auto_load_image checks the per-runtime sentinel (last-load-<runtime>)
     and the runtime's image store.  If either is stale, it materializes
     the store path to a cached tar via _materialize_image and loads it.
  3. On Apple Container, the Nix-produced V2 docker tar is converted to
     OCI format via skopeo or podman (_convert_via_skopeo / _daemon)
     before _load_image_for_apple_container hands it to ``container``.

Sentinel + cache layout under ~/.local/share/yolo-jail/:
  build/last-load-<runtime>     — newline-separated store paths "loaded"
  build/last-load-size          — last streamed byte count (size estimate)
  cache/images/<sha256-16>.tar  — materialized image tars per store path
"""

import hashlib
import json
import os
import re
import shutil
import subprocess
from pathlib import Path
from typing import List, Optional, Union

from .console import console
from .paths import (
    BUILD_DIR,
    GLOBAL_CACHE,
    IS_MACOS,
    JAIL_IMAGE,
    JAIL_IMAGE_SHORT,
)


def _image_load_cmd(runtime: str, tar_path: str) -> list[str]:
    """Return the command to load a container image from a tar archive."""
    if runtime == "container":
        return ["container", "image", "load", "-i", tar_path]
    return [runtime, "load", "-i", tar_path]


def _image_inspect_cmd(runtime: str, image: str) -> list[str]:
    """Return the command to inspect a container image."""
    return [runtime, "image", "inspect", image]


def _jail_image(runtime: str) -> str:
    """Return the jail image name appropriate for the given runtime."""
    if runtime == "container":
        return JAIL_IMAGE_SHORT
    return JAIL_IMAGE


def _load_image_for_apple_container(tar_path: str, console, status=None) -> bool:
    """Load a Nix-built OCI image tar into Apple Container CLI.

    Apple Container only accepts OCI-layout tars, but Nix's image
    tooling produces a non-OCI tar.  We convert using (in priority
    order):
      1. skopeo (no daemon needed — works standalone)
      2. podman save --format oci-archive (needs Podman Machine)
    """
    skopeo = shutil.which("skopeo")
    if skopeo:
        return _convert_via_skopeo(tar_path, console, status)

    if shutil.which("podman"):
        return _convert_via_daemon("podman", tar_path, console, status)

    console.print(
        "[bold red]Cannot convert Nix image to OCI format for Apple Container.[/bold red]"
    )
    console.print(
        "[dim]Install one of: skopeo (recommended, no daemon needed) or podman.[/dim]"
    )
    return False


def _convert_via_skopeo(tar_path: str, console, status=None) -> bool:
    """Convert V2 image tar → OCI tar via skopeo (no daemon needed)."""
    import tempfile

    with tempfile.TemporaryDirectory(prefix="yolo-oci-") as oci_dir:
        if status:
            status.update("[bold cyan]Converting to OCI format via skopeo...")
        copy_result = subprocess.run(
            [
                "skopeo",
                "copy",
                f"docker-archive:{tar_path}",
                f"oci:{oci_dir}:{JAIL_IMAGE_SHORT}",
            ],
            capture_output=True,
        )
        if copy_result.returncode != 0:
            console.print("[bold red]skopeo conversion to OCI failed.[/bold red]")
            stderr = copy_result.stderr.decode().strip()
            if stderr:
                console.print(f"  [dim]{stderr}[/dim]")
            return False

        # Tar up the OCI directory for Apple Container
        oci_tar = tar_path + ".oci.tar"
        if status:
            status.update("[bold cyan]Loading OCI image into Apple Container...")
        tar_result = subprocess.run(
            ["tar", "cf", oci_tar, "-C", oci_dir, "."],
            capture_output=True,
        )
        if tar_result.returncode != 0:
            console.print("[bold red]Failed to create OCI tar.[/bold red]")
            return False

        apple_result = subprocess.run(
            ["container", "image", "load", "-i", oci_tar],
            capture_output=True,
        )
        Path(oci_tar).unlink(missing_ok=True)

        if apple_result.returncode != 0:
            console.print(
                "[bold red]Failed to load OCI image into Apple Container.[/bold red]"
            )
            stderr = apple_result.stderr.decode().strip()
            if stderr:
                console.print(f"  [dim]{stderr}[/dim]")
            return False

    return True


def _convert_via_daemon(daemon: str, tar_path: str, console, status=None) -> bool:
    """Convert V2 image tar → OCI tar via podman daemon save."""
    if status:
        status.update(f"[bold cyan]Loading image into {daemon} (for OCI conversion)...")
    load_result = subprocess.run(
        [daemon, "load", "-i", tar_path],
        capture_output=True,
    )
    if load_result.returncode != 0:
        console.print(
            f"[bold red]Failed to load image into {daemon} for conversion.[/bold red]"
        )
        stderr = load_result.stderr.decode().strip()
        if stderr:
            console.print(f"  [dim]{stderr}[/dim]")
        return False

    oci_tar = tar_path + ".oci.tar"
    if status:
        status.update(f"[bold cyan]Converting to OCI format via {daemon} save...")
    save_cmd = [
        daemon,
        "save",
        "--format",
        "oci-archive",
        "-o",
        oci_tar,
        JAIL_IMAGE,
    ]
    save_result = subprocess.run(save_cmd, capture_output=True)
    if save_result.returncode != 0:
        console.print(f"[bold red]Failed to export OCI image from {daemon}.[/bold red]")
        return False

    if status:
        status.update("[bold cyan]Loading OCI image into Apple Container...")
    apple_result = subprocess.run(
        ["container", "image", "load", "-i", oci_tar],
        capture_output=True,
    )
    Path(oci_tar).unlink(missing_ok=True)

    if apple_result.returncode != 0:
        console.print(
            "[bold red]Failed to load OCI image into Apple Container.[/bold red]"
        )
        stderr = apple_result.stderr.decode().strip()
        if stderr:
            console.print(f"  [dim]{stderr}[/dim]")
        return False

    return True


def _summarize_nix_line(line: str) -> str:
    """Extract a short human-readable summary from nix build stderr."""
    # "copying path '/nix/store/hash-name-1.0' from ..."
    m = re.search(r"copying path '/nix/store/[a-z0-9]+-(.+?)'", line)
    if m:
        return f"Fetching {m.group(1)}"
    # "building '/nix/store/hash-name.drv'..."
    m = re.search(r"building '/nix/store/[a-z0-9]+-(.+?)\.drv'", line)
    if m:
        return f"Building {m.group(1)}"
    # "evaluating derivation ..." or just "evaluating"
    if "evaluating" in line.lower():
        return "Evaluating flake..."
    # Progress counters like "[3/5 built, 2 copied (10.2 MiB)]"
    m = re.match(r"\[[\d/]+ (?:built|copied|fetched).*\]", line.strip())
    if m:
        return line.strip()
    return ""


def _estimate_image_size(store_path: str, sentinel: Path) -> int:
    """Estimate the image stream size in bytes. Returns 0 if unknown."""
    # First, check if we saved a size from a previous stream
    size_file = sentinel.parent / f"{sentinel.name}-size"
    if size_file.exists():
        try:
            return int(size_file.read_text().strip())
        except (ValueError, OSError):
            pass
    # Fall back to nix closure size (approximates uncompressed image)
    try:
        r = subprocess.run(
            [
                "nix",
                "--extra-experimental-features",
                "nix-command flakes",
                "path-info",
                "--closure-size",
                store_path,
            ],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if r.returncode == 0:
            # Output format: "/nix/store/...\t<size>" or just the path with -S flag
            parts = r.stdout.strip().split()
            for p in reversed(parts):
                if p.isdigit():
                    return int(p)
    except (subprocess.TimeoutExpired, FileNotFoundError):
        pass
    return 0


def _build_image_store_path(
    repo_root: Path,
    extra_packages: Optional[List[Union[str, dict]]] = None,
    *,
    out_link: Path,
    status_message: str,
) -> tuple[Optional[str], list[str]]:
    """Run the nix image build and return the resulting store path on success."""
    build_env = os.environ.copy()
    pkg_json = json.dumps(extra_packages) if extra_packages else ""
    if extra_packages:
        build_env["YOLO_EXTRA_PACKAGES"] = pkg_json

    build_stderr_tail: list[str] = []
    try:
        process = subprocess.Popen(
            [
                "nix",
                "--extra-experimental-features",
                "nix-command flakes",
                "build",
                ".#ociImage",
                "--impure",
                "--out-link",
                str(out_link),
                "--print-build-logs",
            ],
            cwd=repo_root,
            env=build_env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            text=True,
        )
    except FileNotFoundError:
        return None, ["nix command not found"]

    with console.status(status_message, spinner="dots") as status:
        if process.stderr:
            for line in iter(process.stderr.readline, ""):
                clean = line.rstrip()
                if clean:
                    build_stderr_tail.append(clean)
                    if len(build_stderr_tail) > 30:
                        build_stderr_tail.pop(0)
                    summary = _summarize_nix_line(clean)
                    if summary:
                        status.update(f"[bold blue]{summary}[/bold blue]")

    process.wait()
    if process.returncode != 0:
        return None, build_stderr_tail

    return str(out_link.resolve()), build_stderr_tail


def _format_progress(current: int, estimate: int) -> str:
    """Format byte progress with optional percentage."""
    mb = current / (1024 * 1024)
    cur_str = f"{mb / 1024:.1f} GB" if mb >= 1024 else f"{mb:.0f} MB"
    if estimate > 0:
        pct = min(int(current * 100 / estimate), 99)  # Cap at 99% until done
        return f"{cur_str} ({pct}%)"
    return cur_str


def _read_loaded_paths(sentinel: Path) -> set[str]:
    """Read the set of store paths that have been loaded into this runtime."""
    if not sentinel.exists():
        return set()
    return {line.strip() for line in sentinel.read_text().splitlines() if line.strip()}


def _add_loaded_path(sentinel: Path, store_path: str):
    """Add a store path to the sentinel, capping at 10 entries (LRU)."""
    paths = (
        [line.strip() for line in sentinel.read_text().splitlines() if line.strip()]
        if sentinel.exists()
        else []
    )
    # Remove if already present (will re-add at end as most recent)
    paths = [p for p in paths if p != store_path]
    paths.append(store_path)
    # Keep only the 10 most recent
    if len(paths) > 10:
        paths = paths[-10:]
    sentinel.write_text("\n".join(paths) + "\n")


def _image_cache_path(store_path: str) -> Path:
    """Return the cached tar file path for a nix store path.

    Images are cached in GLOBAL_CACHE/images/ keyed by a hash of the store path.
    Using a file lets ``podman load -i`` detect existing layers and skip them,
    which is ~30x faster than streaming through a pipe when layers are shared
    across project configs.
    """
    cache_dir = GLOBAL_CACHE / "images"
    cache_dir.mkdir(parents=True, exist_ok=True)
    path_hash = hashlib.sha256(store_path.encode()).hexdigest()[:16]
    return cache_dir / f"{path_hash}.tar"


def _stream_image_command(store_path: str) -> list[str]:
    """Return the command to stream the container image tarball to stdout.

    On macOS the streaming script has a Linux shebang and cannot execute
    locally.  If a remote builder is configured in ``/etc/nix/machines``,
    we first ``nix copy`` the closure to the builder, then execute the
    script there via SSH.  Falls back to local execution (Linux hosts).
    """
    if not IS_MACOS:
        return [store_path]

    machines_file = Path("/etc/nix/machines")
    if not machines_file.exists():
        # Fallback: try local execution (will likely fail)
        return [store_path]

    for line in machines_file.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split()
        if len(parts) >= 2 and "linux" in parts[1]:
            builder_uri = parts[0]  # e.g. ssh-ng://nix-builder
            # Derive the SSH host from the URI
            ssh_host = builder_uri.replace("ssh-ng://", "").replace("ssh://", "")
            # Copy the closure to the builder
            copy_result = subprocess.run(
                ["nix", "copy", "--to", builder_uri, store_path],
                capture_output=True,
                timeout=300,
            )
            if copy_result.returncode != 0:
                # nix copy failed — fall back to local execution
                return [store_path]
            return ["ssh", ssh_host, store_path]

    return [store_path]


def _materialize_image(store_path: str, cache_file: Path, status) -> int:
    """Stream the nix image to a cache tar file.  Returns byte count."""
    sentinel = BUILD_DIR / "last-load-size"
    estimated_size = _estimate_image_size(store_path, sentinel)

    status.update("[bold cyan]Materializing image to cache...")
    stream_cmd = _stream_image_command(store_path)
    stream_proc = subprocess.Popen(
        stream_cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
    )

    total_bytes = 0
    chunk_size = 1024 * 1024  # 1 MB
    tmp_file = cache_file.with_suffix(".tmp")
    assert stream_proc.stdout is not None  # PIPE set above; guarantees this is non-None
    try:
        with open(tmp_file, "wb") as f:
            while True:
                chunk = stream_proc.stdout.read(chunk_size)
                if not chunk:
                    break
                f.write(chunk)
                total_bytes += len(chunk)
                progress = _format_progress(total_bytes, estimated_size)
                status.update(f"[bold cyan]Caching image... {progress}")
        stream_proc.wait()
        if stream_proc.returncode != 0:
            tmp_file.unlink(missing_ok=True)
            return 0
        tmp_file.rename(cache_file)
    except Exception:
        tmp_file.unlink(missing_ok=True)
        raise

    # Save size for future estimates
    size_file = BUILD_DIR / "last-load-size"
    size_file.write_text(str(total_bytes))
    return total_bytes


def auto_load_image(
    repo_root: Path,
    extra_packages: Optional[List[Union[str, dict]]] = None,
    runtime: str = "podman",
):
    """Cheaply check if the nix image needs to be reloaded into the container runtime."""
    # Per-runtime sentinel tracks all store paths loaded into this runtime
    sentinel = BUILD_DIR / f"last-load-{runtime}"
    # Use a PID-unique out-link to avoid races when multiple jails build concurrently
    out_link = BUILD_DIR / f"run-result-{os.getpid()}"
    pkg_json = json.dumps(extra_packages) if extra_packages else ""
    current_path, build_stderr_tail = _build_image_store_path(
        repo_root,
        extra_packages=extra_packages,
        out_link=out_link,
        status_message="[bold blue]Checking jail image...",
    )

    if current_path is None:
        err_summary = (
            "\n".join(build_stderr_tail[-10:]) if build_stderr_tail else "unknown error"
        )
        console.print(
            f"[yellow]Warning: nix build failed:[/yellow]\n[dim]{err_summary}[/dim]"
        )
        # If the image already exists in the runtime, proceed.
        image_name = _jail_image(runtime)
        check = subprocess.run(
            _image_inspect_cmd(runtime, image_name),
            capture_output=True,
        )
        if check.returncode == 0:
            console.print(f"[yellow]Using existing {image_name} image.[/yellow]")
            return
        # No image in runtime — try loading from the most recent cached tar.
        # This handles nested jails where nix build fails but the host already
        # cached the image tar in the shared GLOBAL_CACHE.
        cache_dir = GLOBAL_CACHE / "images"
        if cache_dir.is_dir():
            tars = sorted(
                cache_dir.glob("*.tar"), key=lambda p: p.stat().st_mtime, reverse=True
            )
            for tar_file in tars:
                console.print(
                    f"[yellow]Loading image from cache: {tar_file.name}[/yellow]"
                )
                if runtime == "container":
                    if _load_image_for_apple_container(str(tar_file), console):
                        console.print(
                            "[bold green]Done: loaded image from cache[/bold green]"
                        )
                        return
                else:
                    cache_load_result = subprocess.run(
                        _image_load_cmd(runtime, str(tar_file)),
                        capture_output=True,
                    )
                    if cache_load_result.returncode == 0:
                        console.print(
                            "[bold green]Done: loaded image from cache[/bold green]"
                        )
                        return
        console.print(
            f"[bold red]No existing {image_name} image found. Cannot start jail.[/bold red]"
        )
        return

    # 2. Check if this store path has already been loaded into the runtime.
    # The sentinel can lie: podman storage may have been pruned, reset, or
    # migrated since the sentinel was written. Verify the image actually
    # exists in the runtime — if not, force a reload regardless of sentinel.
    loaded_paths = _read_loaded_paths(sentinel)
    image_name = _jail_image(runtime)
    image_present = (
        subprocess.run(
            _image_inspect_cmd(runtime, image_name),
            capture_output=True,
        ).returncode
        == 0
    )

    if current_path not in loaded_paths or not image_present:
        # Print the reason for the reload
        if not image_present and current_path in loaded_paths:
            console.print(
                f"[bold blue]Image load needed:[/bold blue] sentinel claims loaded, "
                f"but {image_name} is missing from {runtime} (storage reset / pruned?)"
            )
        elif not loaded_paths:
            console.print(
                f"[bold blue]Image load needed:[/bold blue] first run (no images loaded into {runtime} yet)"
            )
        else:
            console.print(
                "[bold blue]Image load needed:[/bold blue] nix store path changed"
            )
            console.print(f"  [dim]new: {current_path}[/dim]")
            if pkg_json:
                console.print(f"  [dim]packages: {pkg_json}[/dim]")
        try:
            with console.status(
                f"[bold cyan]Preparing image for {runtime}...", spinner="bouncingBar"
            ) as status:
                # Materialize the nix image to a cached tar file (or reuse existing).
                # Using a file lets `podman load -i` detect existing layers and skip
                # them (~1-2s), vs piping which must transfer all bytes (~30-40s).
                cache_file = _image_cache_path(current_path)
                if not cache_file.exists():
                    total_bytes = _materialize_image(current_path, cache_file, status)
                    if total_bytes == 0:
                        console.print(
                            "[bold red]Error streaming image to cache.[/bold red]"
                        )
                        out_link.unlink(missing_ok=True)
                        return
                    mb = total_bytes / (1024 * 1024)
                    size_str = f"{mb / 1024:.1f} GB" if mb >= 1024 else f"{mb:.0f} MB"
                    console.print(f"  [dim]Cached image: {size_str}[/dim]")

                # Load from cached file — podman detects existing layers and skips them
                load_ok = False
                load_result: Optional[subprocess.CompletedProcess[bytes]] = None
                if runtime == "container":
                    load_ok = _load_image_for_apple_container(
                        str(cache_file), console, status
                    )
                else:
                    status.update(f"[bold cyan]Loading image into {runtime}...")
                    load_result = subprocess.run(
                        _image_load_cmd(runtime, str(cache_file)),
                        capture_output=True,
                    )
                    load_ok = load_result.returncode == 0

            if not load_ok:
                if runtime != "container" and load_result is not None:
                    console.print(
                        f"[bold red]Error loading image into {runtime}.[/bold red]"
                    )
                    stderr = load_result.stderr.decode().strip()
                    if stderr:
                        console.print(f"  [dim]{stderr}[/dim]")
            else:
                _add_loaded_path(sentinel, current_path)
                console.print("[bold green]Done: loaded image[/bold green]")
        except Exception as e:
            console.print(f"[bold red]Error streaming image: {e}[/bold red]")

    # Cleanup temp link
    out_link.unlink(missing_ok=True)
