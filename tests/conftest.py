"""
Session-level fixture to ensure the yolo-jail image is loaded into the container
runtime before integration tests run. This is needed when pytest itself runs inside
a jail (nested-container scenario), where the inner podman has its own separate image
store that doesn't see the outer host's images.
"""

import os
import subprocess
import shutil
import sys
from pathlib import Path
import pytest

# The entrypoint requires MISE_DATA_DIR to be set at import time (it has no
# built-in default).  In real jails the CLI sets MISE_DATA_DIR=/mise, the
# jail-land store mount (see docs/design/jail-state-separation-design.md).
# Tests only need a syntactically valid path; the value isn't exercised.
os.environ.setdefault("MISE_DATA_DIR", "/tmp/yolo-test-mise")

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


@pytest.fixture(autouse=True)
def _simulate_linux_for_unit_tests(request, monkeypatch):
    """Ensure *unit* tests exercise the Linux code paths regardless of host OS.

    Integration tests (marked ``@pytest.mark.slow``) are left untouched so they
    run with the real platform flags and whatever ``YOLO_RUNTIME`` the caller
    set in the environment.

    The CLI's IS_MACOS / IS_LINUX guards change runtime behaviour.  Unit tests
    are heavily mocked and should test the primary (Linux) code path.  Tests
    that specifically target macOS behaviour can override this with::

        monkeypatch.setattr("cli.IS_MACOS", True)
        monkeypatch.setattr("cli.IS_LINUX", False)

    Also clears YOLO_RUNTIME so the mocked tests use their own runtime
    detection rather than inheriting an env var from the test runner.
    """
    is_integration = any(m.name == "slow" for m in request.node.iter_markers())
    if is_integration:
        return  # let integration tests use real platform values

    monkeypatch.delenv("YOLO_RUNTIME", raising=False)
    # When the test suite runs inside a jail, YOLO_VERSION is set, which
    # flips loophole `requirements_met` / `active` to the in-jail
    # container-side branch and breaks host-mode tests.  Clear it for
    # every unit test; in-jail-mode tests already opt back in via
    # monkeypatch.setenv("YOLO_VERSION", ...).
    monkeypatch.delenv("YOLO_VERSION", raising=False)
    if sys.platform == "darwin":
        # Lazily import — cli may not be on sys.path yet for conftest itself
        try:
            import cli as _cli

            monkeypatch.setattr(_cli, "IS_MACOS", False)
            monkeypatch.setattr(_cli, "IS_LINUX", True)
            # The package split copied IS_MACOS/IS_LINUX into each submodule's
            # namespace at import time; the cli.X re-exports above don't
            # propagate back into those modules.  Patch each one too so call
            # sites inside cli.check_cmd / cli.runtime / etc. see Linux.
            for mod_name in (
                "cli.paths",
                "cli.check_cmd",
                "cli.runtime",
                "cli.image",
                "cli.run_cmd",
                "cli.loopholes_runtime",
            ):
                try:
                    mod = __import__(mod_name, fromlist=["IS_MACOS", "IS_LINUX"])
                except ImportError:
                    continue
                if hasattr(mod, "IS_MACOS"):
                    monkeypatch.setattr(mod, "IS_MACOS", False)
                if hasattr(mod, "IS_LINUX"):
                    monkeypatch.setattr(mod, "IS_LINUX", True)
        except ImportError:
            pass


# Storage-path constants that unit tests must never resolve to the real
# machine.  Every cli module that from-imports one gets its binding
# redirected; a test's own monkeypatch (applied later) still wins.
_STORAGE_CONSTANTS = (
    "GLOBAL_STORAGE",
    "GLOBAL_HOME",
    "GLOBAL_MISE",
    "GLOBAL_CACHE",
    "CONTAINER_DIR",
    "AGENTS_DIR",
    "BUILD_DIR",
    "USER_CONFIG_PATH",
)

# Machine-global /tmp paths for the OAuth-broker singleton (defined in
# cli/loopholes_runtime.py, from-imported by cli/__init__.py and
# cli/run_cmd.py).  Redirected for the same reason as the storage
# constants: a live broker on the machine flips `.exists()` branches in
# unit tests, and parallel (xdist) workers would collide on the shared
# /tmp lock/pid files.
_BROKER_SINGLETON_CONSTANTS = (
    "BROKER_SINGLETON_SOCKET",
    "BROKER_SINGLETON_PID_FILE",
    "BROKER_SINGLETON_LOCK",
)


def _is_yolo_cli_module(mod_name: str) -> bool:
    """Match every alias the CLI source tree gets imported under.

    The suite imports the same source files under TWO module identities:
    ``cli`` / ``cli.*`` (tests that sys.path-insert ``src/``) and
    ``src.cli`` / ``src.cli.*`` (tests importing the installed package,
    e.g. tests/test_prune.py).  Distinct module objects hold distinct
    constant bindings, so both must be redirected.  ``src.prune`` is
    matched too: today it only takes paths as arguments, but a future
    module-level storage constant there must not silently re-open the
    real-storage hole.
    """
    return mod_name in ("cli", "src.cli", "src.prune") or mod_name.startswith(
        ("cli.", "src.cli.")
    )


@pytest.fixture(autouse=True)
def _hermetic_storage_paths(request, monkeypatch, tmp_path_factory):
    """Redirect every storage-path constant to a per-test scratch root.

    Unit tests that miss a monkeypatch otherwise operate on the REAL
    yolo-jail state of whatever machine runs the suite — on 2026-07-04 a
    suite run left ~1650 litter dirs in the real AGENTS_DIR, and
    real-path writes were a live suspect while diagnosing a severed-jail
    incident.  Safe-by-default: tests get a scratch root; the handful of
    integration tests (``slow`` marker) that genuinely need real state
    keep it.

    Only module-level *bindings* are redirected — code that re-derives a
    path from ``Path.home()`` at call time (rare; ``_host_mise_dir``) is
    redirected explicitly below.
    """
    is_integration = any(m.name == "slow" for m in request.node.iter_markers())
    if is_integration:
        return

    root = tmp_path_factory.mktemp("yolo-hermetic")
    # Broker singleton files live directly in /tmp in production, so code
    # writes them without mkdir-ing a parent — create the redirect dir up
    # front to mirror that.  (Tests that actually BIND an AF_UNIX socket
    # use the short-path ``sock_dir`` fixture instead: on macOS this
    # tmp_path-based location can exceed the 104-byte sun_path cap.)
    broker_dir = root / "broker"
    broker_dir.mkdir()
    values = {
        "GLOBAL_STORAGE": root / "storage",
        "GLOBAL_HOME": root / "storage" / "home",
        "GLOBAL_MISE": root / "storage" / "mise",
        "GLOBAL_CACHE": root / "storage" / "cache",
        "CONTAINER_DIR": root / "storage" / "containers",
        "AGENTS_DIR": root / "storage" / "agents",
        "BUILD_DIR": root / "storage" / "build",
        "USER_CONFIG_PATH": root / "config.jsonc",
        "BROKER_SINGLETON_SOCKET": broker_dir / "broker.sock",
        "BROKER_SINGLETON_PID_FILE": broker_dir / "broker.pid",
        "BROKER_SINGLETON_LOCK": broker_dir / "broker.lock",
    }
    assert set(values) == set(_STORAGE_CONSTANTS) | set(_BROKER_SINGLETON_CONSTANTS)
    # tests/test_prune.py imports ``from src.cli import app`` lazily,
    # INSIDE the test body — after this fixture ran.  Import the package
    # eagerly so its constant bindings exist in sys.modules and get
    # redirected here (a fresh import mid-test would resolve to the real
    # ~/.local/share/yolo-jail and walk/mutate host storage).
    try:
        import src.cli  # noqa: F401
    except ImportError:
        pass
    for mod_name, mod in list(sys.modules.items()):
        if mod is None or not _is_yolo_cli_module(mod_name):
            continue
        for const, value in values.items():
            if hasattr(mod, const):
                monkeypatch.setattr(mod, const, value)
    for storage_alias in ("cli.storage", "src.cli.storage"):
        storage_mod = sys.modules.get(storage_alias)
        if storage_mod is not None and hasattr(storage_mod, "_host_mise_dir"):
            monkeypatch.setattr(
                storage_mod, "_host_mise_dir", lambda: root / "host-mise"
            )


def _detect_runtime() -> str | None:
    for rt in ("podman", "container"):
        if shutil.which(rt):
            return rt
    return None


def _image_exists(runtime: str) -> bool:
    # Inside a jail, podman may lack unqualified-search registries, so the
    # short name "yolo-jail:latest" fails to resolve even when the image is
    # loaded — always check the localhost-qualified name too, or every run
    # rebuilds and re-loads the 3.1GB image.
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
    runtime. On the host this is a no-op (cli.py handles it). Inside a jail the inner
    podman has an empty image store, so we build via the host nix daemon and load.

    Only integration tests (``slow`` marker) ever run the image, so a
    fast-only invocation (``-m "not slow"``) skips the build/load entirely —
    it costs ~30s serial and, under pytest-xdist, would run once PER WORKER
    (concurrent nix builds + duplicate 3.1GB podman loads).
    """
    if not any(item.get_closest_marker("slow") for item in request.session.items):
        return  # no integration tests selected — image never used

    in_container = sys.platform != "darwin" and (
        Path("/run/.containerenv").exists() or Path("/.dockerenv").exists()
    )
    if not in_container:
        return  # cli.py already handles this on the host

    runtime = _detect_runtime()
    if runtime is None:
        pytest.skip("No container runtime (podman/container) found")

    if _image_exists(runtime):
        return  # Already loaded from a previous session (persistent home dir)

    # With --read-only root, podman storage is on a read-only filesystem and
    # cannot load new images.  Skip gracefully — unit tests don't need the image.
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

    # Build via host nix daemon (NIX_REMOTE=daemon + /nix/var/nix/daemon-socket are
    # mounted into the jail by cli.py so nix can delegate builds to the host daemon).
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

    # streamLayeredImage produces an executable script that outputs the image
    # tar to stdout — we must execute it and pipe to `runtime load`, not read
    # the script as a file.  This matches the streaming pipeline in cli.py.
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
            # Warn but don't fail — unit tests don't need the image
            import warnings

            warnings.warn(
                f"{runtime} load failed (integration tests may be skipped): "
                f"{load.stderr.decode().strip()}"
            )
            return
        print(f"[conftest] {load.stdout.decode().strip()}")
    finally:
        result_link.unlink(missing_ok=True)
