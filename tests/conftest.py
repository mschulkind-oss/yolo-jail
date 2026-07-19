"""
Session-level fixture to ensure the yolo-jail image is loaded into the container
runtime before integration tests run. This is needed when pytest itself runs inside
a jail (nested-container scenario), where the inner podman has its own separate image
store that doesn't see the outer host's images.
"""

import subprocess
import shutil
import sys
from pathlib import Path
import pytest

REPO_ROOT = Path(__file__).parent.parent.resolve()
JAIL_IMAGE = "yolo-jail:latest"


@pytest.fixture
def sock_dir():
    """Short per-test dir under /tmp for AF_UNIX sockets — sun_path is
    capped at 108 bytes on Linux / 104 on macOS, and pytest's tmp_path
    (/private/var/folders/... on macOS) exceeds it."""
    import tempfile

    base = "/private/tmp" if sys.platform == "darwin" else "/tmp"
    d = Path(tempfile.mkdtemp(dir=base, prefix="yj-sock-"))
    yield d
    shutil.rmtree(d, ignore_errors=True)


def _detect_runtime() -> str | None:
    for rt in ("podman", "container"):
        if shutil.which(rt):
            return rt
    return None


def _image_exists(runtime: str) -> bool:
    for name in (JAIL_IMAGE, f"localhost/{JAIL_IMAGE}"):
        result = subprocess.run(
            [runtime, "image", "inspect", name],
            capture_output=True,
        )
        if result.returncode == 0:
            return True
    return False


@pytest.fixture(scope="session", autouse=True)
def _ensure_nix_in_path():
    """On macOS, ensure /nix/var/nix/profiles/default/bin is in PATH for
    test subprocesses that invoke cli.py (which calls ``nix build``)."""
    import os

    nix_bin = "/nix/var/nix/profiles/default/bin"
    if sys.platform == "darwin" and nix_bin not in os.environ.get("PATH", ""):
        os.environ["PATH"] = nix_bin + ":" + os.environ.get("PATH", "")


@pytest.fixture(scope="session", autouse=True)
def ensure_jail_image(request):
    """
    Before any test runs, ensure yolo-jail:latest is loaded into the local container
    runtime. Only integration tests (``slow`` marker) ever run the image, so a
    fast-only invocation skips the build/load entirely.
    """
    if not any(item.get_closest_marker("slow") for item in request.session.items):
        return

    in_container = sys.platform != "darwin" and (
        Path("/run/.containerenv").exists() or Path("/.dockerenv").exists()
    )
    if not in_container:
        return

    runtime = _detect_runtime()
    if runtime is None:
        pytest.skip("No container runtime (podman/container) found")

    if _image_exists(runtime):
        return

    storage_check = subprocess.run(
        [runtime, "info", "--format", "{{.Store.GraphRoot}}"],
        capture_output=True,
        timeout=10,
    )
    if storage_check.returncode != 0:
        import warnings

        warnings.warn(
            "Container runtime storage unavailable (read-only filesystem?) — "
            "integration tests may be skipped"
        )
        return

    print(
        f"\n[conftest] Loading {JAIL_IMAGE} into inner {runtime} (this may take a minute)..."
    )

    build = subprocess.run(
        [
            "nix",
            "--extra-experimental-features",
            "nix-command flakes",
            "build",
            ".#ociImage",
            "--impure",
            "--out-link",
            str(REPO_ROOT / ".run-result"),
        ],
        cwd=str(REPO_ROOT),
        capture_output=True,
    )

    result_link = REPO_ROOT / ".run-result"
    if build.returncode != 0:
        pytest.fail(
            f"nix build failed inside jail — cannot load {JAIL_IMAGE}.\n"
            f"stderr: {build.stderr.decode()}\n"
            "Ensure the host nix daemon socket is mounted (/nix/var/nix/daemon-socket) "
            "and NIX_REMOTE=daemon is set."
        )

    resolved = str(result_link.resolve())
    try:
        stream_proc = subprocess.Popen(
            [resolved],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )
        load = subprocess.run(
            [runtime, "load"],
            stdin=stream_proc.stdout,
            capture_output=True,
        )
        stream_proc.wait()
        if stream_proc.returncode != 0 or load.returncode != 0:
            import warnings

            warnings.warn(
                f"{runtime} load failed (integration tests may be skipped): "
                f"{load.stderr.decode().strip()}"
            )
            return
        print(f"[conftest] {load.stdout.decode().strip()}")
    finally:
        result_link.unlink(missing_ok=True)
