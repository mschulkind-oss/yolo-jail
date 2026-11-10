#!/usr/bin/env python3
"""Freeze bounded source inputs and publish matched yolo launcher/helper bundles."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import uuid

INPUT_FILES = {"go.mod", "go.sum", "flake.nix", "flake.lock", "Justfile",
               "scripts/build-go.sh", "scripts/stage-source-bundle.sh",
               "scripts/matched-bundle.py", "scripts/dev-wrapper.sh"}
INPUT_DIRS = ("vendor/", "cmd/", "internal/", "packs/")


def _inside(path: str) -> bool:
    return path in INPUT_FILES or any(path.startswith(root) for root in INPUT_DIRS)


def _binary_lists(script: Path) -> tuple[list[str], list[str]]:
    """The linux ship set, and the names in each darwin dir: the darwin host list
    (DARWIN_HOST_BINARIES, which holds the host `yolo`) then the guest list."""
    text = script.read_text()
    values = []
    for name in ("SHIPPED_BINARIES", "DARWIN_HOST_BINARIES", "GUEST_BINARIES"):
        match = re.search(rf"^{name}=\(([^)]*)\)$", text, re.MULTILINE)
        if not match:
            raise ValueError(f"stage script has no {name} declaration: {script}")
        names = match.group(1).split()
        if not names or len(names) != len(set(names)):
            raise ValueError(f"stage script has an invalid {name} declaration")
        values.append(names)
    darwin = values[1] + [n for n in values[2] if n not in values[1]]
    return values[0], darwin


def _platform(root: Path) -> tuple[str, str]:
    goos = subprocess.check_output(["go", "env", "GOHOSTOS"], cwd=root, env=_git_env(), text=True).strip()
    goarch = subprocess.check_output(["go", "env", "GOHOSTARCH"], cwd=root, env=_git_env(), text=True).strip()
    if goos not in ("linux", "darwin"):
        raise RuntimeError(f"matched bundles are supported on Linux and macOS, not {goos}")
    return goos, goarch


def _validate_bundle_contents(bundle: Path, shipped: list[str], guest: list[str],
                              host_os: str, host_arch: str) -> None:
    if not stat.S_ISDIR(bundle.lstat().st_mode):
        raise RuntimeError("incomplete matched bundle: share/yolo-jail is not a directory")
    if {p.name for p in bundle.iterdir()} != {"flake.nix", "flake.lock", "yolo-build-stamp.json", "bin"}:
        raise RuntimeError("incomplete matched bundle: unexpected or missing share/yolo-jail entries")
    bins = bundle / "bin"
    if not stat.S_ISDIR(bins.lstat().st_mode):
        raise RuntimeError("incomplete matched bundle: bin is not a directory")
    expected = {f"linux-{host_arch}": shipped}
    if host_os == "darwin":
        expected[f"darwin-{host_arch}"] = guest
    if {p.name for p in bins.iterdir()} != set(expected):
        raise RuntimeError("incomplete matched bundle: target helper directories do not match this host")
    for dirname, names in expected.items():
        directory = bins / dirname
        if not stat.S_ISDIR(directory.lstat().st_mode):
            raise RuntimeError(f"incomplete matched bundle: {dirname} is not a directory")
        if {p.name for p in directory.iterdir()} != set(names):
            raise RuntimeError(f"incomplete matched bundle: {dirname} does not contain the declared ship set")
        for binary in directory.iterdir():
            mode = binary.lstat().st_mode
            if not stat.S_ISREG(mode) or not mode & 0o111:
                raise RuntimeError(f"incomplete matched bundle: {dirname}/{binary.name} is not an executable file")


def _git_env() -> dict[str, str]:
    # Child probes must not inherit checkout selection or Git redirection state.
    # The parent shell remains untouched; only explicit child environments use this.
    return {key: value for key, value in os.environ.items()
            if not key.startswith("GIT_") and key != "YOLO_REPO_ROOT"}


def _git_paths(repo: Path) -> set[str]:
    git_env = _git_env()
    tracked = subprocess.check_output(["git", "ls-files", "-z", "--cached"], cwd=repo, env=git_env).decode().split("\0")
    untracked = subprocess.check_output(["git", "ls-files", "-z", "--others", "--exclude-standard"], cwd=repo, env=git_env).decode().split("\0")
    candidates = {p for p in tracked + untracked if p and _inside(p) and p != ".git" and not p.startswith(".git/")}
    if not candidates:
        return set()
    result = subprocess.run(["git", "check-ignore", "--no-index", "-z", "--stdin"],
                            cwd=repo, env=git_env, input="\0".join(sorted(candidates)).encode(),
                            stdout=subprocess.PIPE, check=False)
    ignored = set(result.stdout.decode().split("\0"))
    return candidates - ignored


def _check_symlink(repo: Path, rel: str, selected: set[str]) -> str | None:
    source = repo / rel
    if not source.is_symlink():
        return None
    resolved = source.resolve(strict=True)
    try:
        target = resolved.relative_to(repo.resolve()).as_posix()
    except ValueError as exc:
        raise ValueError(f"required input symlink escapes snapshot: {rel} -> {resolved}") from exc
    if target not in selected:
        raise ValueError(f"required input symlink target is not in snapshot: {rel} -> {target}")
    return target


def _copy_repo_file(repo: Path, rel: str, destination: Path) -> int:
    """Copy one selected regular file through a no-follow walk under repo."""
    nofollow = getattr(os, "O_NOFOLLOW", 0)
    directory = getattr(os, "O_DIRECTORY", 0)
    if not nofollow or not directory:
        raise RuntimeError("safe snapshot copying requires O_NOFOLLOW and O_DIRECTORY")
    fd = os.open(repo, os.O_RDONLY | directory | nofollow)
    try:
        parts = PurePosixPath(rel).parts
        for part in parts[:-1]:
            next_fd = os.open(part, os.O_RDONLY | directory | nofollow, dir_fd=fd)
            os.close(fd)
            fd = next_fd
        source_fd = os.open(parts[-1], os.O_RDONLY | nofollow, dir_fd=fd)
        mode = os.fstat(source_fd).st_mode
        if not stat.S_ISREG(mode):
            os.close(source_fd)
            raise ValueError(f"snapshot input is no longer a regular file: {rel}")
        with os.fdopen(source_fd, "rb") as source, destination.open("wb") as target:
            shutil.copyfileobj(source, target)
        return stat.S_IMODE(mode)
    finally:
        os.close(fd)


def collect_inputs(repo: Path) -> list[str]:
    repo = repo.resolve()
    selected = _git_paths(repo)
    # Required top-level build inputs cannot silently disappear from an artifact.
    for rel in ("flake.nix", "flake.lock", "Justfile", "scripts/build-go.sh",
                "scripts/stage-source-bundle.sh", "scripts/matched-bundle.py", "scripts/dev-wrapper.sh", "go.mod"):
        if rel not in selected or not (repo / rel).exists():
            raise ValueError(f"required snapshot input missing: {rel}")
    for rel in sorted(selected):
        source = repo / rel
        if source.is_symlink():
            _check_symlink(repo, rel, selected)
        elif source.exists() and not source.is_file():
            raise ValueError(f"unsupported snapshot input type: {rel}")
    return sorted(rel for rel in selected if (repo / rel).exists())


def _entries(root: Path):
    for path in sorted(root.rglob("*"), key=lambda p: p.relative_to(root).as_posix()):
        rel = path.relative_to(root).as_posix()
        if path.is_symlink():
            yield rel, b"L", os.readlink(path).encode()
        elif path.is_file():
            executable = path.stat().st_mode & 0o111
            yield rel, b"X" if executable else b"F", path.read_bytes()


def snapshot_identity(root: Path) -> str:
    digest = hashlib.sha256()
    for rel, kind, content in _entries(root):
        path_bytes = rel.encode("utf-8")
        digest.update(len(path_bytes).to_bytes(8, "big"))
        digest.update(path_bytes)
        digest.update(kind)
        digest.update(len(content).to_bytes(8, "big"))
        digest.update(content)
    return digest.hexdigest()


def freeze_snapshot(repo: Path, destination: Path) -> str:
    repo = repo.resolve()
    inputs = collect_inputs(repo)
    destination.mkdir(parents=True, exist_ok=False)
    selected = set(inputs)
    for rel in inputs:
        target = destination / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        resolved_target = _check_symlink(repo, rel, selected)
        copy_rel = resolved_target or rel
        mode = _copy_repo_file(repo, copy_rel, target)
        target.chmod(mode)
    return snapshot_identity(destination)


def _run(command, *, cwd: Path, env: dict[str, str], runner=subprocess.run):
    # The build's progress goes to stderr (fd 2): stdout is the caller's. `build-dev` prints the
    # launcher's path there, and `just deploy` discards the first run's stdout, which once hid
    # every progress line behind a silent half-minute build.
    sys.stderr.flush()
    runner(command, cwd=cwd, env=env, check=True, stdout=2)


def assemble(snapshot: Path, destination: Path, *, identity: str, build_kind: str,
             version: str, commit: str, runner=subprocess.run) -> Path:
    """Build from the frozen tree into an unpublished, private output directory.

    `yolo` is built once for the host's platform, and the launcher in bin/ IS that build:
    on Linux it is the bundle's own bin/linux-<arch>/yolo, copied out of the staged tree;
    on macOS it is the bundle's own bin/darwin-<arch>/yolo, the one darwin build of yolo,
    which a macos-user launch pins and stages as the guest's yolo.
    Nothing compiles yolo a second time, so the launcher and the bundle's same-platform
    copy cannot differ (docs/design/installed-launcher-provenance.md, OQ-IP2). A Linux
    jail on a Mac runs a different program by nature: the bundle's linux build.
    """
    snapshot = snapshot.resolve()
    destination.mkdir(parents=True, exist_ok=False)
    host_os, host_arch = _platform(snapshot)
    shipped, guest = _binary_lists(snapshot / "scripts/stage-source-bundle.sh")
    if "yolo" not in shipped:
        raise ValueError("stage script's SHIPPED_BINARIES has no yolo, so the bundle cannot supply the launcher")
    env = _git_env()
    env.pop("YOLO_REPO_ROOT", None)
    env.pop("BUILD_GO_CMDS", None)
    env.update({"VERSION": version, "COMMIT": commit, "SNAPSHOT_IDENTITY": identity,
                "BUILD_KIND": build_kind})
    share = destination / "share" / "yolo-jail"
    stage_env = env.copy()
    stage_env["YOLO_BUNDLE_ARCHES"] = host_arch
    stage_env["YOLO_BUNDLE_GUEST_OSES"] = "darwin" if host_os == "darwin" else ""
    _run([str(snapshot / "scripts/stage-source-bundle.sh"), str(share)],
         cwd=snapshot, env=stage_env, runner=runner)
    if "yolo" not in guest and host_os == "darwin":
        raise ValueError("stage script's darwin set has no yolo, so the bundle cannot supply the launcher")
    launcher = share / "bin" / f"{host_os}-{host_arch}" / "yolo"
    if not launcher.is_file():
        raise RuntimeError(f"native launcher build missing: {launcher}")
    bin_dir = destination / "bin"
    bin_dir.mkdir()
    shutil.copy2(launcher, bin_dir / "yolo")
    validate_bundle(destination, identity, build_kind, shipped=shipped, guest=guest,
                    host_os=host_os, host_arch=host_arch)
    return destination


def _stamp_lines(version: str, commit: str) -> set[str]:
    """The `--version --full` lines a launcher linked with these ldflags stamps prints."""
    # Mirrors internal/version: StampLabel and CommitLabel.
    stamp = version if version not in ("", "unknown") else "unstamped"
    label = commit if commit not in ("", "unknown") else "unknown"
    return {f"build stamp: {stamp}", f"commit: {label}"}


def validate_pair(launcher: Path, bundle: Path, identity: str, build_kind: str, *,
                  stamps: tuple[str, str] | None = None) -> None:
    """Check a launcher/bundle pair; with stamps, also require that exact version and commit.

    Snapshot identity hashes source content only, so one identity can be built from a dirty
    tree at one commit and a clean tree at the next. The version/commit ldflags stamp is not
    part of it, and a reused bundle must still name the tree being built.
    """
    for path in (launcher, bundle / "flake.nix", bundle / "flake.lock", bundle / "yolo-build-stamp.json"):
        try:
            mode = path.lstat().st_mode
        except OSError as exc:
            raise RuntimeError(f"incomplete matched bundle: missing {path}") from exc
        if not stat.S_ISREG(mode):
            raise RuntimeError(f"incomplete matched bundle: {path} is not a regular file")
    if not launcher.lstat().st_mode & 0o111:
        raise RuntimeError("incomplete matched bundle: native launcher is not executable")
    stamp = json.loads((bundle / "yolo-build-stamp.json").read_text())
    if stamp.get("snapshotIdentity") != identity or stamp.get("buildKind") != build_kind:
        raise RuntimeError("incomplete matched bundle: launcher and bundle identity metadata differ")
    if stamps is not None and (stamp.get("version"), stamp.get("commit")) != stamps:
        raise RuntimeError("matched bundle is stamped for a different commit or dirtiness")
    env = _git_env()
    env.pop("YOLO_REPO_ROOT", None)
    result = _run_fresh_executable([str(launcher), "--version", "--full"], env)
    fields = set(result.stdout.splitlines())
    if f"snapshot identity: {identity}" not in fields or f"build kind: {build_kind}" not in fields:
        raise RuntimeError("incomplete matched bundle: native launcher identity does not match bundle metadata")
    if stamps is not None and not _stamp_lines(*stamps) <= fields:
        raise RuntimeError("matched bundle launcher is stamped for a different commit or dirtiness")


def _run_fresh_executable(argv: list[str], env: dict[str, str]) -> subprocess.CompletedProcess:
    """Run a just-written executable, retrying while the kernel reports it busy.

    Executing a file that some process still holds open for writing fails with ETXTBSY. The
    writer here closed it, but a fork in another thread of this process (a concurrent publish)
    can inherit that write descriptor for the instant before its exec closes it, so the busy
    state is real but momentary: CI saw it in test_failed_and_concurrent_publication_is_atomic_
    and_unique (2026-11-02). Go's os/exec retries the same condition for the same reason.
    """
    import errno
    delay = 0.01
    for attempt in range(8):
        try:
            return subprocess.run(argv, check=True, capture_output=True, text=True, env=env)
        except OSError as exc:
            if exc.errno != errno.ETXTBSY or attempt == 7:
                raise
            time.sleep(delay)
            delay *= 2
    raise AssertionError("unreachable")


def validate_bundle(root: Path, identity: str, build_kind: str, *,
                    shipped: list[str] | None = None, guest: list[str] | None = None,
                    host_os: str | None = None, host_arch: str | None = None,
                    stamps: tuple[str, str] | None = None) -> None:
    if not stat.S_ISDIR(root.lstat().st_mode) or {p.name for p in root.iterdir()} != {"bin", "share"}:
        raise RuntimeError("incomplete matched bundle: output root must contain only bin and share")
    launcher_dir = root / "bin"
    if not stat.S_ISDIR(launcher_dir.lstat().st_mode) or {p.name for p in launcher_dir.iterdir()} != {"yolo"}:
        raise RuntimeError("incomplete matched bundle: native bin must contain only yolo")
    bundle = root / "share/yolo-jail"
    validate_pair(root / "bin/yolo", bundle, identity, build_kind, stamps=stamps)
    if shipped is not None and guest is not None and host_os and host_arch:
        _validate_bundle_contents(bundle, shipped, guest, host_os, host_arch)


def _make_readonly(root: Path) -> None:
    for path in sorted(root.rglob("*"), reverse=True):
        if path.is_symlink():
            continue
        mode = path.stat().st_mode
        if path.is_dir():
            path.chmod(0o555)
        else:
            path.chmod((mode & 0o111) | 0o444)
    root.chmod(0o555)


def publish_dev_bundle(home: Path, payload: dict[str, tuple[bytes, int]], *, identity: str) -> Path:
    """Atomically publish a uniquely named home-local development generation."""
    root = home / ".local/share/yolo-jail/dev-bundles"
    root.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=".building-", dir=root))
    try:
        for rel, (content, mode) in payload.items():
            target = staging / PurePosixPath(rel)
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)
            target.chmod(mode)
        validate_bundle(staging, identity, "development")
        _make_readonly(staging)
        final = root / f"{identity[:16]}-{uuid.uuid4().hex}"
        os.rename(staging, final)
        return final
    except Exception:
        shutil.rmtree(staging, ignore_errors=True)
        raise


def _publish_assembled_bundle(home: Path, assembled: Path, identity: str,
                              shipped: list[str], guest: list[str], host_os: str, host_arch: str,
                              stamps: tuple[str, str]) -> Path:
    root = home / ".local/share/yolo-jail/dev-bundles"
    root.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=".building-", dir=root))
    try:
        shutil.copytree(assembled, staging, dirs_exist_ok=True)
        # copytree copies the assembled tree's mtime onto staging (copystat), which is the
        # build's START: a long build would make an in-flight publish look abandoned to the
        # reaper's staging floor, and the published bundle look old. Stamp it as of now.
        os.utime(staging)
        validate_bundle(staging, identity, "development", shipped=shipped, guest=guest,
                        host_os=host_os, host_arch=host_arch, stamps=stamps)
        _make_readonly(staging)
        final = root / f"{identity[:16]}-{uuid.uuid4().hex}"
        os.rename(staging, final)
        return final
    except Exception:
        shutil.rmtree(staging, ignore_errors=True)
        raise


def _touch_reused_bundle(bundle: Path) -> bool:
    """Mark a candidate bundle as used now, BEFORE it is validated or run; False when it cannot be.

    Its mtime is what the Go reaper (prune.PruneDevBundles) keeps the newest few by and what its
    grace window reads. The reaper renames a bundle aside before deleting it and re-reads the
    renamed directory's mtime: a touch that landed first keeps the bundle, and a touch that comes
    after finds no bundle at this name. So a bundle that cannot be touched may be one a sweep is
    removing, and is not reused (the caller skips it)."""
    try:
        os.utime(bundle)
        return True
    except OSError:
        return False


def _reap_dev_bundles(launcher: Path, root: Path, runner=subprocess.run) -> None:
    """Collect unused development bundles with the launcher just published: one rule, in Go
    (`yolo internal dev-bundle-reap`, the same prune.PruneDevBundles `yolo prune` runs), never a
    second copy here. It keeps the newest few and anything a running jail or process uses, and
    prints only when it removed something or declined. Best effort: a failed sweep never fails
    the build, and `yolo prune --apply` retries it."""
    try:
        runner([str(launcher), "internal", "dev-bundle-reap", "--dir", str(root)],
               stdout=subprocess.DEVNULL, check=False, timeout=120)
    except (OSError, subprocess.SubprocessError):
        pass


class DevBuildFailed(RuntimeError):
    """Assembling or publishing the checkout's development bundle failed.

    Only that step raises it: a snapshot that cannot be frozen (a missing required input, an
    escaping symlink) is a refusal about the checkout, not a failed build, and never falls back.
    It carries what the dev mode's fallback (OQ-MDB1) needs to pick an earlier bundle."""

    def __init__(self, cause: BaseException, root: Path, host_os: str, host_arch: str):
        self.cause, self.root, self.host_os, self.host_arch = cause, root, host_os, host_arch
        super().__init__(f"building this checkout's development bundle failed: {str(cause).rstrip('.')}. "
                         "Fix the build error above and rerun.")


_DEV_BUNDLE_NAME = re.compile(r"^[0-9a-f]{16}-[0-9a-f]{32}$")  # prune.devBundleName


def _validate_fallback_bundle(candidate: Path, host_os: str, host_arch: str) -> dict:
    """Check an EARLIER development bundle is a complete one this host can run; return its stamp.

    It was built from other source, so this snapshot's identity, stamps and ship lists do not
    apply: the checkout's edits may have changed SHIPPED_BINARIES, and the bundle was checked
    against its own lists when it was published. What still applies is everything that makes it a
    complete, self-consistent pair for THIS host: the published layout, a development build kind,
    a stamp whose identity is the one its directory is named for, helper directories for exactly
    this host's targets (another architecture's bundle is refused here, before anything runs),
    and a launcher that runs and reports the identity, kind, version and commit its bundle names."""
    if not _DEV_BUNDLE_NAME.match(candidate.name):
        raise RuntimeError(f"not a published development bundle name: {candidate.name}")
    if not stat.S_ISDIR(candidate.lstat().st_mode) or {p.name for p in candidate.iterdir()} != {"bin", "share"}:
        raise RuntimeError("incomplete matched bundle: output root must contain only bin and share")
    if {p.name for p in (candidate / "bin").iterdir()} != {"yolo"}:
        raise RuntimeError("incomplete matched bundle: native bin must contain only yolo")
    bundle = candidate / "share/yolo-jail"
    if {p.name for p in bundle.iterdir()} != {"flake.nix", "flake.lock", "yolo-build-stamp.json", "bin"}:
        raise RuntimeError("incomplete matched bundle: unexpected or missing share/yolo-jail entries")
    stamp = json.loads((bundle / "yolo-build-stamp.json").read_text())
    identity = stamp.get("snapshotIdentity")
    if not isinstance(identity, str) or not identity.startswith(candidate.name[:16]):
        raise RuntimeError("development bundle's stamp names another snapshot than its directory")
    targets = {f"linux-{host_arch}"} | ({f"darwin-{host_arch}"} if host_os == "darwin" else set())
    if {p.name for p in (bundle / "bin").iterdir()} != targets:
        raise RuntimeError("development bundle's helpers are not for this host")
    for dirname in targets:
        helpers = list((bundle / "bin" / dirname).iterdir())
        if not helpers or any(not stat.S_ISREG(h.lstat().st_mode) or not h.lstat().st_mode & 0o111
                              for h in helpers):
            raise RuntimeError(f"incomplete matched bundle: {dirname} is empty or not executable")
    validate_pair(candidate / "bin/yolo", bundle, identity, "development",
                  stamps=(stamp.get("version"), stamp.get("commit")))
    return stamp


def _fallback_dev_launcher(root: Path, host_os: str, host_arch: str) -> tuple[Path, dict] | None:
    """The newest complete development bundle under root this host can run (OQ-MDB1), or None.

    Newest by directory mtime, the reaper's own "most recently used" (prune.PruneDevBundles,
    which always keeps the newest few). Each candidate is touched BEFORE it is validated, the
    reaper race guard a reuse takes (_touch_reused_bundle); one that cannot be touched is skipped.
    A candidate that fails validation gets its mtime back, so a broken bundle is not kept alive
    as the newest."""
    try:
        entries = [(p.lstat().st_mtime, p) for p in root.iterdir() if _DEV_BUNDLE_NAME.match(p.name)]
    except OSError:
        return None
    for mtime, candidate in sorted(entries, key=lambda e: e[0], reverse=True):
        if not _touch_reused_bundle(candidate):
            continue
        try:
            return candidate / "bin/yolo", _validate_fallback_bundle(candidate, host_os, host_arch)
        except (OSError, ValueError, RuntimeError, AttributeError, subprocess.SubprocessError):
            try:
                os.utime(candidate, (mtime, mtime))
            except OSError:
                pass
    return None


def build_dev_bundle(repo: Path, home: Path, *, builder=assemble, runner=subprocess.run,
                     reaper=_reap_dev_bundles) -> Path:
    """Reuse a complete bundle of this identity and stamp, otherwise publish a fresh immutable build.

    A candidate must match the snapshot identity AND the version/commit the ldflags embed, so
    the same content built at another commit or dirtiness is a miss, never a stale stamp.
    After a publish, the new launcher collects the bundles nothing uses any more (reaper).
    """
    repo = repo.resolve()
    home = home.resolve()
    version, commit = _source_stamps(repo)
    stamps = (version, commit)
    root = home / ".local/share/yolo-jail/dev-bundles"
    root.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="yolo-matched-") as td:
        snapshot = Path(td) / "snapshot"
        identity = freeze_snapshot(repo, snapshot)
        host_os, host_arch = _platform(snapshot)
        shipped, guest = _binary_lists(snapshot / "scripts/stage-source-bundle.sh")
        for candidate in sorted(root.glob(f"{identity[:16]}-*")):
            if not _touch_reused_bundle(candidate):
                continue
            try:
                validate_bundle(candidate, identity, "development", shipped=shipped, guest=guest,
                                host_os=host_os, host_arch=host_arch, stamps=stamps)
                return candidate / "bin/yolo"
            except (OSError, ValueError, RuntimeError, json.JSONDecodeError,
                    subprocess.CalledProcessError):
                continue
        output = Path(td) / "bundle"
        print(f"Building a development bundle for this checkout ({host_os}-{host_arch}): the source "
              "changed since the last one. Later runs of the same source reuse it.",
              file=sys.stderr, flush=True)
        started = time.monotonic()
        try:
            builder(snapshot, output, identity=identity, build_kind="development",
                    version=version, commit=commit, runner=runner)
            published = _publish_assembled_bundle(home, output, identity, shipped, guest, host_os,
                                                  host_arch, stamps)
        except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as exc:
            raise DevBuildFailed(exc, root, host_os, host_arch) from exc
        print(f"Built the development bundle in {time.monotonic() - started:.0f} s.",
              file=sys.stderr, flush=True)
        reaper(published / "bin/yolo", root)
        return published / "bin/yolo"


def validate_and_publish_dev(home: Path, payload: dict[str, tuple[bytes, int]], identity: str) -> Path:
    try:
        return publish_dev_bundle(home, payload, identity=identity)
    except RuntimeError as exc:
        raise RuntimeError(f"incomplete matched bundle: {exc}") from exc


def publish_install_generation(launcher: Path, bundle: Path, identity: str,
                               env: dict[str, str], runner=subprocess.run, *,
                               shipped: list[str] | None = None, guest: list[str] | None = None,
                               host_os: str | None = None, host_arch: str | None = None) -> str:
    staged = runner([str(launcher), "internal", "bundle-dir", "--stage"], env=env,
                    text=True, capture_output=True, check=True).stdout.strip()
    shutil.copytree(bundle, staged, dirs_exist_ok=True)
    validate_pair(launcher, Path(staged), identity, "installed")
    if shipped is not None and guest is not None and host_os and host_arch:
        _validate_bundle_contents(Path(staged), shipped, guest, host_os, host_arch)
    runner([str(launcher), "internal", "bundle-dir", "--activate", staged], env=env, check=True)
    return staged


def _source_stamps(repo: Path) -> tuple[str, str]:
    """The version and commit a build of repo stamps. Nothing about where the checkout is:
    an install is a snapshot (docs/design/installed-launcher-provenance.md, OQ-IP1)."""
    def git(*args):
        try:
            return subprocess.check_output(["git", *args], cwd=repo, env=_git_env(), text=True,
                                           stderr=subprocess.DEVNULL).strip()
        except subprocess.CalledProcessError:
            return ""
    return (git("describe", "--tags", "--dirty", "--always", "--long", "--abbrev=12"),
            git("rev-parse", "--short=12", "HEAD"))


def _install_guard(env: dict[str, str]) -> None:
    if env.get("YOLO_VERSION"):
        raise RuntimeError("'just install' is host-only — refusing inside a jail (YOLO_VERSION set).")


def _atomic_install_launcher(source: Path, gobin: Path, name: str = "yolo") -> Path:
    gobin.mkdir(parents=True, exist_ok=True)
    staged = gobin / f".{name}-install-{uuid.uuid4().hex}"
    shutil.copy2(source, staged)
    os.replace(staged, gobin / name)
    return gobin / name


def _resolved_path(path: str | Path) -> str:
    try:
        return subprocess.check_output(["readlink", "-f", str(path)], text=True,
                                       stderr=subprocess.DEVNULL, env=_git_env()).strip()
    except (OSError, subprocess.CalledProcessError):
        return str(path)


def command(repo: Path, mode: str, *, home: Path, gobin: Path | None = None,
            name: str = "yolo", args: list[str] | None = None, builder=assemble, runner=subprocess.run,
            execer=os.execvpe, argv0: str | None = None):
    if mode in ("build-dev", "dev"):
        try:
            launcher = build_dev_bundle(repo, home, builder=builder, runner=runner)
            notice = f"Matched development bundle: {launcher}"
        except DevBuildFailed as failure:
            # build-dev PRODUCES this checkout's bundle, so a failed build is its failure; only
            # running (dev) falls back to the newest earlier development build (OQ-MDB1). Never
            # to the installed launcher, and with no earlier development build it fails as before.
            if mode == "build-dev":
                raise
            found = _fallback_dev_launcher(failure.root, failure.host_os, failure.host_arch)
            if found is None:
                raise RuntimeError(f"{failure} There is no earlier development bundle to run "
                                   "instead.") from failure
            launcher, stamp = found
            notice = (f"matched-bundle: {failure}\n"
                      "Your checkout's edits are NOT running. Running the last development build "
                      f"instead: commit {stamp.get('commit') or 'unknown'}, build stamp "
                      f"{stamp.get('version') or 'unstamped'}, at {launcher}")
        if mode == "build-dev":
            return launcher
        if "YOLO_REPO_ROOT" in os.environ:
            print("Ignoring obsolete YOLO_REPO_ROOT; remove that export and invoke the matched bundle by path.", file=sys.stderr)
        child_env = os.environ.copy()
        child_env.pop("YOLO_REPO_ROOT", None)
        # stderr, flushed: stdout belongs to the launcher, and an exec discards Python's buffers.
        print(notice, file=sys.stderr, flush=True)
        # The launcher runs under the name it was invoked as (a `just deploy-dev yolo-dev` link), so
        # its own name checks see `yolo-dev`; `just dev` runs the wrapper by its path and keeps the
        # launcher's.
        shown = argv0 if argv0 and Path(argv0).name != "dev-wrapper.sh" else str(launcher)
        execer(str(launcher), [shown, *(args or [])], child_env)
        return 0
    if mode != "install":
        raise ValueError(f"unknown matched bundle mode: {mode}")
    _install_guard(os.environ)
    version, commit = _source_stamps(repo)
    state = home / ".local/share/yolo-jail"
    state.mkdir(parents=True, exist_ok=True)
    env = _git_env()
    env.pop("YOLO_REPO_ROOT", None)
    env["HOME"] = str(home)
    with tempfile.TemporaryDirectory(prefix="yolo-matched-") as td:
        work = Path(td)
        snapshot = work / "snapshot"
        identity = freeze_snapshot(repo, snapshot)
        shipped, guest = _binary_lists(snapshot / "scripts/stage-source-bundle.sh")
        host_os, host_arch = _platform(snapshot)
        output = work / "bundle"
        assemble(snapshot, output, identity=identity, build_kind="installed",
                 version=version, commit=commit, runner=runner)
        launcher = output / "bin/yolo"
        if gobin is None:
            raw = subprocess.check_output(["go", "env", "GOBIN"], cwd=snapshot,
                                          env=_git_env(), text=True).strip()
            gobin = Path(raw) if raw else Path(subprocess.check_output(
                ["go", "env", "GOPATH"], cwd=snapshot, env=_git_env(), text=True).strip()) / "bin"
        subprocess.run([str(launcher), "internal", "migrate-host", f"--gobin={gobin}",
                        f"--name={name}"], env=env, check=True)
        publish_install_generation(launcher, output / "share/yolo-jail", identity, env,
                                   runner=runner, shipped=shipped, guest=guest,
                                   host_os=host_os, host_arch=host_arch)
        installed = _atomic_install_launcher(launcher, gobin, name)
        print(f"Installed {installed}")
        resolved = shutil.which(name)
        if resolved and _resolved_path(resolved) != _resolved_path(installed):
            print(file=sys.stderr)
            print(f"⚠ WARNING: this install is NOT the {name} your shell runs.", file=sys.stderr)
            print(f"    installed:  {installed}\n    resolves:   {resolved}", file=sys.stderr)
            print(f"  Every launch will keep using the older one. Put {gobin} ahead of it on PATH (or remove {resolved}), then run 'hash -r'.", file=sys.stderr)
            print(file=sys.stderr)
        if resolved and resolved != str(installed):
            print(f"⚠ PATH resolves {name} to {resolved}, not the copy just installed at {installed}.", file=sys.stderr)
            print(f"  Remove the other install, or put {gobin} earlier in PATH.", file=sys.stderr)
        return installed


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("build-dev", "dev", "install"))
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--home", type=Path, default=Path.home())
    parser.add_argument("--gobin", type=Path)
    parser.add_argument("--name", default="yolo")
    parser.add_argument("--argv0", help="dev mode: the name the dev wrapper was invoked as")
    raw = sys.argv[1:]
    if "--" in raw:
        split = raw.index("--")
        args = parser.parse_args(raw[:split])
        forwarded = raw[split + 1:]
    else:
        args = parser.parse_args(raw)
        forwarded = []
    try:
        if (not args.name or "/" in args.name or args.name in (".", "..")
                or any(ord(ch) < 0x20 or ord(ch) == 0x7f for ch in args.name)):
            raise ValueError(f"--name must be a plain file name, not {args.name!r}")
        if args.name.endswith(".test"):
            # internal/packload treats a binary named *.test as a Go test binary.
            raise ValueError(f"--name {args.name!r} ends in .test, which yolo reads as a Go test binary; "
                             "pick another name, such as yolo-dev")
        result = command(args.repo, args.mode, home=args.home, gobin=args.gobin, name=args.name,
                         args=forwarded, execer=os.execvpe, argv0=args.argv0)
        if args.mode == "build-dev":
            print(result)
        return result if isinstance(result, int) else 0
    except (OSError, subprocess.CalledProcessError, RuntimeError, ValueError) as exc:
        print(f"matched-bundle: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
