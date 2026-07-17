"""On-demand container-based Linux builder for the macOS container runtimes.

On podman / Apple Container, when a `packages:` build isn't cached, nix must
offload to Linux.  Instead of a separate QEMU VM (``builder.py`` /
``darwin.linux-builder`` — the roadmap fallback), run a tiny nix+sshd builder
as a container **on the runtime that's already up**: ephemeral, no second
hypervisor, zero idle RAM.  The image is ``packages.builderImage`` (flake),
published to ``ghcr.io/mschulkind-oss/yolo-jail-builder`` and pulled on the Mac.

Mechanism (proven end-to-end on podman): the container runs sshd with
``ForceCommand nix-daemon --stdio``; nix's ssh-ng remote-builder protocol talks
to it, so a host ``nix build --builders 'ssh-ng://root@<addr> …'`` runs the
build inside the container and copies the result back.

Design (all podman-verifiable on Linux; AC differs only in how the host
reaches the container — see ``reachable_address``):
  * **pure** — argv/URI/`builders` builders, key-path helpers.
  * **impure** — pull / run / wait-reachable / stop; the ``builder_session``
    context manager ties them into a start→use→teardown lifecycle.

The host-side private key is generated per-workspace under GLOBAL_STORAGE and
its public half is passed to the container via ``YOLO_BUILDER_PUBKEY`` at run
time (the published image is keyless — one image serves every user).
"""

from __future__ import annotations

import contextlib
import os
import socket
import subprocess
import time
from typing import Iterator, List, Optional, Tuple

from .paths import GLOBAL_STORAGE

# The published builder image (keyless; key injected at run time).  Pinned by
# tag; a digest pin is a hardening follow-up.
BUILDER_IMAGE = "ghcr.io/mschulkind-oss/yolo-jail-builder:latest"
BUILDER_CONTAINER = "yolo-linux-builder"  # fixed name → idempotent reuse/cleanup
BUILDER_SSH_USER = "root"  # the image logs in as root (owns /nix/store)
BUILDER_GUEST_PORT = 22
# Host port we publish the container's sshd to (podman `-p`).  Fixed + high to
# avoid collisions; AC ignores this and exposes the container's own VM IP:22.
BUILDER_HOST_PORT = 31022

# Per-workspace host-daemon key: its PUBLIC half is authorized in the container.
BUILDER_KEY_DIR = GLOBAL_STORAGE / "linux-builder-container"
BUILDER_KEY = BUILDER_KEY_DIR / "id_ed25519"

BUILDER_START_TIMEOUT_S = 60
BUILDER_POLL_INTERVAL_S = 1.0


# ── pure helpers ─────────────────────────────────────────────────────────────


def ensure_keypair(_run=None) -> str:
    """Generate the host-daemon builder keypair if absent; return the PUBLIC key.

    ed25519, no passphrase, 0600 private — the private key stays on the host
    (nix ssh's with it); only the public half enters the container.  Idempotent.
    """
    run = _run or subprocess.run
    if BUILDER_KEY.exists() and BUILDER_KEY.with_suffix(".pub").exists():
        return BUILDER_KEY.with_suffix(".pub").read_text().strip()
    BUILDER_KEY_DIR.mkdir(parents=True, exist_ok=True)
    run(
        [
            "ssh-keygen",
            "-t",
            "ed25519",
            "-N",
            "",
            "-q",
            "-f",
            str(BUILDER_KEY),
            "-C",
            "yolo-linux-builder",
        ],
        check=True,
    )
    try:
        os.chmod(BUILDER_KEY, 0o600)
    except OSError:
        pass
    return BUILDER_KEY.with_suffix(".pub").read_text().strip()


def pull_argv(runtime: str, image: str = BUILDER_IMAGE) -> List[str]:
    """argv to pull the builder image on the given runtime. Pure."""
    if runtime == "container":
        return ["container", "image", "pull", image]
    return [runtime, "pull", image]


def run_argv(
    runtime: str,
    pubkey: str,
    image: str = BUILDER_IMAGE,
    name: str = BUILDER_CONTAINER,
    host_port: int = BUILDER_HOST_PORT,
) -> List[str]:
    """argv to start the builder container detached. Pure.

    podman publishes sshd to 127.0.0.1:<host_port>; Apple Container has no
    ``-p`` (each container gets its own VM IP), so we omit the publish there and
    the caller discovers the container's address via ``reachable_address``.
    """
    common = [
        "run",
        "-d",
        "--rm",
        "--name",
        name,
        "-e",
        f"YOLO_BUILDER_PUBKEY={pubkey}",
    ]
    if runtime == "container":
        return ["container", *common, image]
    # podman: publish the guest sshd to a fixed host loopback port.
    return [
        runtime,
        *common,
        "-p",
        f"127.0.0.1:{host_port}:{BUILDER_GUEST_PORT}",
        image,
    ]


def builder_uri(host: str, port: int = BUILDER_HOST_PORT) -> str:
    """The ssh-ng store/builder URI nix uses to reach the container. Pure."""
    return f"ssh-ng://{BUILDER_SSH_USER}@{host}:{port}?ssh-key={BUILDER_KEY}"


def builders_line(host: str, port: int = BUILDER_HOST_PORT, max_jobs: int = 4) -> str:
    """A nix ``--builders`` spec pointing at the container. Pure.

    Format: ``ssh-ng://user@host system key maxjobs`` — the host daemon offloads
    matching-system builds here.  System is fixed to aarch64-linux (the arch a
    Mac needs; the container image is aarch64-linux).
    """
    return f"ssh-ng://{BUILDER_SSH_USER}@{host}:{port} aarch64-linux {BUILDER_KEY} {max_jobs}"


def nix_ssh_opts() -> str:
    """NIX_SSHOPTS for talking to an ephemeral container (no host-key pinning).

    The container regenerates its host key each boot, so pinning is pointless;
    accept-new + throwaway known-hosts avoids interactive prompts and stale-key
    failures.  This is a build-only localhost/VM-IP endpoint.
    """
    return "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"


# ── impure lifecycle ─────────────────────────────────────────────────────────


def reachable(host: str, port: int, timeout: float = 1.0) -> bool:
    """True if something accepts TCP at host:port (sshd up). Never raises."""
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except OSError:
        return False


def reachable_address(
    runtime: str, name: str = BUILDER_CONTAINER
) -> Optional[Tuple[str, int]]:
    """Where the host reaches the running builder's sshd — runtime-specific.

    podman: the published loopback port (127.0.0.1:BUILDER_HOST_PORT).
    Apple Container: the container's own VM IP on :22, read from ``container
    ls`` (AC has no host port-publish; the per-container VM IP is directly
    reachable — verified on real HW at 192.168.64.2:22).
    Returns ``(host, port)`` or None if it can't be determined.
    """
    if runtime != "container":
        return "127.0.0.1", BUILDER_HOST_PORT
    try:
        r = subprocess.run(
            ["container", "ls"], capture_output=True, text=True, timeout=10
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if r.returncode != 0:
        return None
    for line in r.stdout.strip().splitlines()[1:]:  # skip header
        parts = line.split()
        if parts and parts[0] == name:
            # The ADDR column carries the container IP (e.g. 192.168.64.2/24).
            for tok in parts:
                if tok.count(".") == 3:
                    return tok.split("/")[0], BUILDER_GUEST_PORT
    return None


def _wait_reachable(
    runtime: str,
    timeout_s: float = BUILDER_START_TIMEOUT_S,
    interval_s: float = BUILDER_POLL_INTERVAL_S,
    _sleep=time.sleep,
    _now=time.monotonic,
) -> Optional[Tuple[str, int]]:
    """Poll until the builder's sshd answers; return its (host, port) or None."""
    deadline = _now() + timeout_s
    while _now() < deadline:
        addr = reachable_address(runtime)
        if addr and reachable(addr[0], addr[1]):
            return addr
        _sleep(interval_s)
    addr = reachable_address(runtime)
    return addr if (addr and reachable(addr[0], addr[1])) else None


def stop(runtime: str, name: str = BUILDER_CONTAINER) -> None:
    """Tear down the builder container (best-effort; --rm cleans the rest)."""
    cli = "container" if runtime == "container" else runtime
    with contextlib.suppress(OSError, subprocess.SubprocessError):
        subprocess.run([cli, "rm", "-f", name], capture_output=True, timeout=15)


@contextlib.contextmanager
def builder_session(runtime: str, _run=None) -> Iterator[Optional[str]]:
    """Start the builder container, yield its ``--builders`` line, tear it down.

    Ephemeral by construction: the container is removed on exit (``--rm`` +
    explicit stop), so nothing lingers or holds RAM between builds.  Yields
    ``None`` if the builder couldn't be started/reached — the caller falls back
    (QEMU builder, or a clear error).  ``_run`` is injectable for tests.
    """
    run = _run or subprocess.run
    pubkey = ensure_keypair(_run=run)
    # Fresh start each session — a stale container from a crashed run would
    # hold the name / a dead port.
    stop(runtime)
    started = False
    try:
        run(pull_argv(runtime), capture_output=True, timeout=600)
        proc = run(
            run_argv(runtime, pubkey), capture_output=True, text=True, timeout=60
        )
        started = getattr(proc, "returncode", 1) == 0
        if not started:
            yield None
            return
        addr = _wait_reachable(runtime)
        if addr is None:
            yield None
            return
        yield builders_line(addr[0], addr[1])
    finally:
        if started:
            stop(runtime)
