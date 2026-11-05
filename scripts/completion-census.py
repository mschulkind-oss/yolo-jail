#!/usr/bin/env python3
"""Regenerate scripts/completion-readers.json: which Go tests read repository files outside
their own package directory.

`just done` (scripts/completion-check.py) selects Go tests from the packages whose own files
changed. A test that READS a file it does not import — a doc, a manifest, another package's
source — is invisible to that selection, so this census records those reads, per test, from Go's
own test log (`-test.testlogfile`, the record `go test` uses to invalidate its cache), and the
selector reruns a test whenever a change touches what it read.

Run it after adding or changing a test that reads outside its package directory:

    python3 scripts/completion-census.py

It runs every test of every package once, one process per test function, so it takes minutes,
not seconds. It writes only the readers file. Reads by a test's SUBPROCESSES (git, go build) are
not in the log; that is the same limit Go's own test cache has.

A `stat` of the repository root directory, by any spelling, records nothing; an `open` of it (a
listing) is still recorded as `.`. The output must not depend on where the checkout lives, and
production code stats the jail's own workspace path, the literal `/workspace`, which names the
repository root only in the checkout that IS `/workspace`. The other root stats in a test log
are incidental too: `os.StartProcess` stats a subprocess's working directory, and resolving a
path stats each of its ancestors. Neither reads the root's contents, and a test that does read
them opens the root or the paths under it, which are recorded.

A test that OPENS a path under the literal `/workspace` outside the checkout stops the census.
An Env that names no workspace defaults to the literal `/workspace` (`Env.WorkspaceDir`), so such a test reads whatever file the machine has
there: in this repository's jail, the live checkout's. The census cannot tell that read from a
real one when it runs from the checkout that IS `/workspace`, where it would record the file,
while every other checkout drops it, so the two outputs differ. So the census
refuses, naming every such test, wherever it can see the leak: from any other checkout.

For a detached gate checkout nested under a private durable `worktrees/` directory, the test
temporary root can be a dedicated sibling directory outside that checkout but still lexically
under `/workspace`. Set `TMPDIR` and `YOLO_CENSUS_FIXTURE_ROOT` to the same fresh directory and
create only the census marker described by `validate_fixture_root`. The census consumes that
marker before running tests. It accepts only the owned, mode-0700, canonical sibling namespace;
the broad durable parent and source roots are never exempt, and an open whose resolved target
leaves the fixture root remains a leak. Ordinary runs need no fixture-root setting.
"""
import concurrent.futures as cf
import json
import os
import stat
import subprocess
import sys
import tempfile
from pathlib import Path

from go_packages import package_patterns

class CensusError(RuntimeError):
    """A collector failure that must stop regeneration without writing partial output."""


ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "scripts" / "completion-readers.json"


def go_packages():
    """(packages with tests, every active Git-visible package directory)."""
    try:
        patterns = package_patterns(ROOT)
    except (OSError, RuntimeError, subprocess.CalledProcessError) as exc:
        raise CensusError("Go package selection failed; repair the Go package graph and rerun "
                          f"scripts/completion-census.py: {exc}") from exc
    if not patterns:
        return [], set()
    try:
        out = subprocess.check_output(
            ["go", "list", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{len .TestGoFiles}}\t{{len .XTestGoFiles}}",
             *patterns], cwd=ROOT, text=True, stderr=subprocess.PIPE)
    except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
        detail = getattr(exc, "stderr", b"")
        if isinstance(detail, bytes):
            detail = detail.decode(errors="replace")
        raise CensusError(f"go list failed; fix package loading and rerun scripts/completion-census.py: {detail or exc}") from exc
    pkgs, dirs = [], set()
    try:
        for line in out.splitlines():
            ip, d, t, x = line.split("\t")
            dirs.add(os.path.relpath(d, ROOT))
            if int(t) or int(x):
                pkgs.append((ip, Path(d)))
    except ValueError as exc:
        raise CensusError(f"go list returned an unreadable package record {line!r}; rerun scripts/completion-census.py") from exc
    return pkgs, dirs


def _git_metadata(root):
    for directory in (root, *root.parents):
        if os.path.lexists(directory / ".git"):
            return True
    return bool(os.environ.get("GIT_DIR") or os.environ.get("GIT_WORK_TREE")
                or os.environ.get("GIT_COMMON_DIR"))


def _git_files(root, *selection):
    try:
        result = subprocess.run(["git", "-C", str(root), "ls-files", *selection, "-z"],
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    except OSError as exc:
        raise CensusError(f"could not run Git source inventory; install/repair Git and rerun "
                          f"scripts/completion-census.py: {exc}") from exc
    if result.returncode:
        raise CensusError("Git source inventory failed; repair the checkout and rerun "
                          f"scripts/completion-census.py: {result.stderr.decode(errors='replace').strip()}")
    return {os.fsdecode(path) for path in result.stdout.split(b"\0") if path}


class GitVisibility:
    """Repository paths that are tracked or not ignored, queried live for newly-created paths."""

    def __init__(self, root):
        self.root = Path(root).resolve()
        self.files = None if not _git_metadata(self.root) else _git_files(
            self.root, "--cached", "--others", "--exclude-standard")
        self.dirs = None
        if self.files is not None:
            self.dirs = {"."}
            for path in self.files:
                parent = os.path.dirname(path)
                while parent and parent != ".":
                    self.dirs.add(parent)
                    parent = os.path.dirname(parent)

    def contains(self, path, directory=False):
        if self.files is None:  # An archive has no ignore/tracking authority; keep normal coverage.
            return True
        known = self.dirs if directory else self.files
        if path in known:
            return True
        # Tests may create a real, nonignored source dependency after the initial inventory.
        # Query just that path rather than freezing out newly visible dependencies.
        entries = _git_files(self.root, "--cached", "--others", "--exclude-standard", "--",
                             ":(literal)" + path)
        if entries:
            return True
        if directory:
            # Empty or ignored-only directories are not repository source dependencies. Their
            # unignored parent may still be retained through tracked/nonignored descendants.
            return False
        # Missing nonignored files can be real optional-source dependencies (e.g. os.Stat).
        # Retain them unless Git says ignore rules exclude the path. --no-index still sees
        # ignore rules for force-tracked paths, which were accepted from the cached inventory.
        try:
            result = subprocess.run(["git", "-C", str(self.root), "check-ignore", "--quiet",
                                     "--no-index", "--", path],
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        except OSError as exc:
            raise CensusError(f"could not run Git ignore check; install/repair Git and rerun "
                              f"scripts/completion-census.py: {exc}") from exc
        if result.returncode == 0:
            return False
        if result.returncode == 1:
            return True
        raise CensusError("Git ignore check failed; repair the checkout and rerun "
                          f"scripts/completion-census.py: {result.stderr.decode(errors='replace').strip()}")


def parse_log(text, pkgdir, pkg_dirs, visibility=None):
    """The Git-visible testlog open/stat entries outside this test's own Go package."""
    opened, listed, stats = set(), set(), set()
    own = os.path.relpath(pkgdir, ROOT)
    visibility = visibility or GitVisibility(ROOT)
    for line in text.splitlines():
        op, _, path = line.partition(" ")
        if op not in ("open", "stat"):
            continue
        r = rel(path, pkgdir)
        if r is None or r == ".git" or r.startswith(".git" + os.sep):
            continue
        if r == "." and op == "stat":
            # A stat of the root itself is not a read of the repository (module docstring).
            continue
        if owner(r, pkg_dirs) == own:
            continue
        is_dir = (ROOT / r).is_dir()
        if not visibility.contains(r, directory=is_dir):
            continue
        if is_dir:
            # A listed or stat'ed directory changes only when a direct child appears or goes.
            listed.add(r)
        elif op == "stat":
            stats.add(r)
        else:
            opened.add(r)
    stats -= opened | listed
    return opened, listed, stats


JAIL_WORKSPACE = "/workspace"
FIXTURE_ROOT_ENV = "YOLO_CENSUS_FIXTURE_ROOT"
FIXTURE_ROOT_PREFIX = "cc-"
FIXTURE_ROOT_MARKER = ".completion-census-fixture"
FIXTURE_ROOT_MARKER_TEXT = "completion-census dedicated fixture root v1\n"


def _is_within(path, base):
    """Component-aware containment; unlike string prefixes, sibling prefixes do not match."""
    try:
        return os.path.commonpath((os.fspath(path), os.fspath(base))) == os.fspath(base)
    except (TypeError, ValueError):
        return False


def _resolve_with_missing_leaf(path):
    """Resolve every existing component, allowing only a genuinely missing suffix."""
    suffix = []
    current = Path(path)
    while True:
        try:
            current.lstat()
        except FileNotFoundError:
            parent = current.parent
            if parent == current:
                raise
            suffix.append(current.name)
            current = parent
            continue
        resolved = current.resolve(strict=True)
        for component in reversed(suffix):
            resolved = resolved / component
        return resolved


def _fixture_path_is_confined(path, fixture_root):
    """Prove an unresolvable fixture path through no-follow directory descriptors."""
    try:
        relative = os.path.relpath(os.fspath(path), os.fspath(fixture_root))
    except (OSError, ValueError):
        return False
    if relative == ".":
        components = ()
    elif relative == ".." or relative.startswith(".." + os.sep) or os.path.isabs(relative):
        return False
    else:
        components = Path(relative).parts

    directory = getattr(os, "O_DIRECTORY", 0)
    nofollow = getattr(os, "O_NOFOLLOW", 0)
    if not directory or not nofollow:
        return False
    flags = os.O_RDONLY | directory | nofollow
    descriptors = []
    try:
        root_fd = os.open(fixture_root, flags)
        descriptors.append(root_fd)
        root_info = os.fstat(root_fd)
        named_root = os.stat(fixture_root, follow_symlinks=False)
        if (not stat.S_ISDIR(root_info.st_mode) or root_info.st_uid != os.getuid()
                or stat.S_IMODE(root_info.st_mode) != 0o700
                or (root_info.st_dev, root_info.st_ino) != (named_root.st_dev, named_root.st_ino)):
            return False

        current_fd = root_fd
        for index, component in enumerate(components):
            if component in ("", ".", "..") or os.sep in component:
                return False
            try:
                info = os.stat(component, dir_fd=current_fd, follow_symlinks=False)
            except FileNotFoundError:
                # Only the remaining suffix is absent; all existing ancestors were opened
                # beneath the private anchor without following a link.
                return True
            if stat.S_ISLNK(info.st_mode):
                return False
            if index == len(components) - 1:
                return True
            if not stat.S_ISDIR(info.st_mode):
                return False
            child_fd = os.open(component, flags, dir_fd=current_fd)
            descriptors.append(child_fd)
            opened = os.fstat(child_fd)
            if (not stat.S_ISDIR(opened.st_mode)
                    or (opened.st_dev, opened.st_ino) != (info.st_dev, info.st_ino)):
                return False
            current_fd = child_fd
        return True
    except (OSError, ValueError):
        return False
    finally:
        for descriptor in reversed(descriptors):
            try:
                os.close(descriptor)
            except OSError:
                pass


def validate_fixture_root(root=ROOT, environ=None):
    """Validate and consume a fresh private TMPDIR namespace for a detached gate checkout."""
    environ = os.environ if environ is None else environ
    configured = environ.get(FIXTURE_ROOT_ENV)
    if not configured:
        return None
    tmpdir = environ.get("TMPDIR")
    if not tmpdir or tmpdir != configured:
        raise CensusError(f"{FIXTURE_ROOT_ENV} must exactly match TMPDIR; refusing an unproved fixture namespace")

    candidate = Path(configured)
    try:
        canonical = candidate.resolve(strict=True)
        source = Path(root).resolve(strict=True)
        source_parent = source.parent
        durable_parent = source_parent.parent
    except OSError as exc:
        raise CensusError(f"cannot resolve source and fixture roots: {exc}") from exc

    # The caller provides one fresh sibling of this detached worktree under its private durable
    # parent. Never treat a source root, one of its ancestors, or the broad parent as fixtures.
    if (not candidate.is_absolute() or candidate != canonical or source_parent.name != "worktrees"
            or durable_parent.name != "durable" or durable_parent.parent.name != ".yolo"
            or candidate.parent != durable_parent or not candidate.name.startswith(FIXTURE_ROOT_PREFIX)
            or _is_within(candidate, source) or _is_within(source, candidate)
            or candidate == Path(JAIL_WORKSPACE) or candidate == durable_parent):
        raise CensusError(f"{FIXTURE_ROOT_ENV} is not a dedicated sibling fixture namespace")

    try:
        dir_info = candidate.lstat()
        marker = candidate / FIXTURE_ROOT_MARKER
        marker_info = marker.lstat()
        entries = list(candidate.iterdir())
        if (not stat.S_ISDIR(dir_info.st_mode) or dir_info.st_uid != os.getuid()
                or stat.S_IMODE(dir_info.st_mode) != 0o700):
            raise CensusError("fixture namespace must be an owned, non-symlink, mode-0700 directory")
        if (entries != [marker] or not stat.S_ISREG(marker_info.st_mode)
                or marker_info.st_uid != os.getuid() or stat.S_IMODE(marker_info.st_mode) != 0o600
                or marker.read_text() != FIXTURE_ROOT_MARKER_TEXT):
            raise CensusError("fixture namespace must contain only its owned mode-0600 census marker")
        marker.unlink()
        if list(candidate.iterdir()):
            raise CensusError("fixture namespace changed while its marker was consumed")
    except CensusError:
        raise
    except (OSError, RuntimeError) as exc:
        raise CensusError(f"cannot validate the dedicated fixture namespace: {exc}") from exc
    return canonical


def jail_workspace_opens(text, pkgdir, fixture_root=None):
    """Open paths under literal /workspace that are not this checkout or proven fixtures."""
    leaks = set()
    fixture_root = Path(fixture_root).resolve(strict=True) if fixture_root is not None else None
    for line in text.splitlines():
        op, _, path = line.partition(" ")
        if op != "open":
            continue
        p = Path(os.path.normpath(path if os.path.isabs(path) else os.path.join(pkgdir, path)))
        if p != Path(JAIL_WORKSPACE) and not _is_within(p, Path(JAIL_WORKSPACE)):
            continue
        if rel(str(p), pkgdir) is not None:
            continue
        if _is_within(Path(ROOT), p):  # an ancestor of this checkout
            continue
        if fixture_root is not None and _is_within(p, fixture_root):
            try:
                resolved = _resolve_with_missing_leaf(p)
            except (OSError, RuntimeError, ValueError):
                if _fixture_path_is_confined(p, fixture_root):
                    continue
                # A path-resolution error cannot be exempted lexically. The descriptor walk
                # proves only currently existing, non-symlink components beneath the private
                # fixture anchor; every unproved target remains a leak.
                leaks.add(str(p))
                continue
            # A fixture alias that resolves into a checkout or elsewhere is not a fixture read.
            if _is_within(resolved, fixture_root):
                continue
            leaks.add(str(resolved))
            continue
        leaks.add(str(p))
    return leaks


def refuse_jail_workspace_opens(leaks):
    """Stop the census when any test opened the jail workspace from outside it (module
    docstring); leaks maps (package directory, test) to the paths it opened."""
    if not leaks:
        return
    lines = [f"  {pkg} {name}: {', '.join(sorted(paths))}" for (pkg, name), paths in sorted(leaks.items())]
    raise CensusError(
        "these tests open the machine's own /workspace, not this checkout or a fixture, which "
        "the census cannot tell from a repository read when it runs from the checkout that IS "
        "/workspace:\n" + "\n".join(lines) +
        "\nGive the test's Env a workspace of its own (Env.Workspace, or YOLO_WORKSPACE for NewEnv) "
        "so it stops defaulting to /workspace, then rerun scripts/completion-census.py")


def tracked_dirs(root=ROOT):
    """Tracked files by directory, for compressing stable directory coverage patterns."""
    root = Path(root).resolve()
    if not _git_metadata(root):
        return {}
    files = _git_files(root, "--cached")
    by_dir = {}
    for f in files:
        by_dir.setdefault(os.path.dirname(f) or ".", set()).add(f)
    return by_dir


def rel(p, base):
    """Repository-relative path for a test-log entry, or None when it is outside the tree."""
    if not os.path.isabs(p):
        p = os.path.join(base, p)
    p = os.path.normpath(p)
    root = str(ROOT)
    if p == root:
        return "."
    if not p.startswith(root + os.sep):
        return None
    return os.path.relpath(p, root)


def owner(rel_path, pkg_dirs):
    """The package directory a repository-relative path belongs to: itself or its nearest
    ancestor that is a package directory, or None."""
    d = rel_path
    while d and d != ".":
        if d in pkg_dirs:
            return d
        d = os.path.dirname(d)
    return None


def compress(paths, by_dir):
    """Replace "every tracked file directly in D" with `D/*`, and "every tracked file with
    extension E directly in D" (for .go, every non-test one) with `D/*E`. The selector matches
    `D/*E` against any path in D with that extension, so a `_test.go` change can select a reader
    of non-test sources: more than needed, never less."""
    out = set(paths)
    for d, members in by_dir.items():
        prefix = "" if d == "." else d + "/"
        if len(members) > 1 and members <= out:
            out -= members
            out.add(prefix + "*")
            continue
        by_ext = {}
        for f in members:
            ext = os.path.splitext(f)[1]
            if ext and not f.endswith("_test.go"):
                by_ext.setdefault(ext, set()).add(f)
        for ext, group in by_ext.items():
            if len(group) > 1 and group <= out:
                out -= {f for f in out if os.path.dirname(f) == d and f.endswith(ext)}
                out.add(prefix + "*" + ext)
    return sorted(out)


def compress_listed(listed, by_dir):
    """Replace "every tracked directory at or under D" with `D/**`, outermost first."""
    dirs = set()
    for d in by_dir:
        while d and d != ".":
            dirs.add(d)
            d = os.path.dirname(d)
    out = set(listed)
    for d in sorted(dirs, key=lambda x: x.count("/")):
        tree = {x for x in dirs if x == d or x.startswith(d + "/")}
        if len(tree) > 1 and tree <= out:
            out -= tree
            out.add(d + "/**")
    return sorted(out)


def checked_run(command, cwd, timeout, description):
    try:
        result = subprocess.run(command, cwd=cwd, capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired as exc:
        raise CensusError(f"{description} timed out after {timeout}s; resolve the slow/hung collector test "
                          "and rerun scripts/completion-census.py") from exc
    except OSError as exc:
        raise CensusError(f"could not run {description}: {exc}; repair the toolchain and rerun "
                          "scripts/completion-census.py") from exc
    if result.returncode:
        stdout = result.stdout if result.stdout else "<empty>"
        stderr = result.stderr if result.stderr else "<empty>"
        detail = (f"command argv: {result.args!r}\n"
                  f"direct return code: {result.returncode}\n"
                  f"stdout (verbatim):\n{stdout}\n"
                  f"stderr (verbatim):\n{stderr}")
        raise CensusError(f"{description} failed:\n{detail}; fix the failing test/collector and rerun "
                          "scripts/completion-census.py")
    return result


def list_tests(binary, cwd):
    result = checked_run([str(binary), "-test.list", ".*"], cwd, 700, "Go test listing")
    return [name for name in result.stdout.split() if name[:1].isupper()]


def run_test(binary, cwd, name, log):
    checked_run([str(binary), "-test.short", "-test.run", f"^{name}$",
                 f"-test.testlogfile={log}", "-test.timeout=600s"],
                cwd, 700, f"Go test {cwd} {name}")
    try:
        return log.read_text(errors="replace")
    except OSError as exc:
        raise CensusError(f"Go test {cwd} {name} did not leave its test log: {exc}; "
                          "rerun scripts/completion-census.py") from exc


def main():
    fixture_root = validate_fixture_root(ROOT, os.environ)
    jobs_n = int(os.environ.get("CENSUS_JOBS", str(max(2, (os.cpu_count() or 4) // 2))))
    pkgs, pkg_dirs = go_packages()
    by_dir = tracked_dirs()
    visibility = GitVisibility(ROOT)
    with tempfile.TemporaryDirectory(prefix="completion-census-") as tmp:
        tmp = Path(tmp)

        def build(i):
            b = tmp / f"{i}.test"
            checked_run(["go", "test", "-c", "-o", str(b), pkgs[i][0]], ROOT, 700,
                        f"Go test compile {pkgs[i][0]}")
            return b

        with cf.ThreadPoolExecutor(jobs_n) as ex:
            bins = list(ex.map(build, range(len(pkgs))))
        jobs = []
        for i, (ip, d) in enumerate(pkgs):
            jobs += [(i, n) for n in list_tests(bins[i], d)]
        print(f"census: {len(pkgs)} packages, {len(jobs)} tests, {jobs_n} at a time", flush=True)

        def run(job):
            i, name = job
            ip, d = pkgs[i]
            log = tmp / f"{i}-{name}.log"
            text = run_test(bins[i], d, name, log)
            return i, name, parse_log(text, d, pkg_dirs, visibility), jail_workspace_opens(text, d, fixture_root)

        readers, sets, index, leaks = {}, [], {}, {}
        with cf.ThreadPoolExecutor(jobs_n) as ex:
            for k, (i, name, (opened, listed, stats), leaked) in enumerate(ex.map(run, jobs)):
                if leaked:
                    leaks[(os.path.relpath(pkgs[i][1], ROOT), name)] = leaked
                if opened or listed or stats:
                    pkg = os.path.relpath(pkgs[i][1], ROOT)
                    entry = {"read": compress(opened | stats, by_dir), "listed": compress_listed(listed, by_dir)}
                    key = json.dumps(entry, sort_keys=True)
                    if key not in index:
                        index[key] = len(sets)
                        sets.append(entry)
                    readers.setdefault(pkg, {})[name] = index[key]
                if k and k % 1000 == 0:
                    print(f"census: {k}/{len(jobs)}", flush=True)
    refuse_jail_workspace_opens(leaks)  # before any output is written
    doc = {
        "about": "Generated by scripts/completion-census.py; read by scripts/completion-check.py. "
                 "readers: package directory -> test -> index into sets. A set names the repository "
                 "paths outside the package directory that are tracked or nonignored at collection time. "
                 "Ignored-only generated paths are excluded; `D/*` is every tracked file directly in D, "
                 "`D/*.E` every one with that extension; `D/**` is D and every tracked directory under it. "
                 "Regenerate, do not edit.",
        "sets": sets,
        "readers": {p: dict(sorted(t.items())) for p, t in sorted(readers.items())},
    }
    OUT.write_text(json.dumps(doc, indent=0, sort_keys=False) + "\n")
    print(f"census: wrote {OUT.relative_to(ROOT)} ({sum(len(t) for t in readers.values())} reading tests "
          f"in {len(readers)} packages)")


if __name__ == "__main__":
    try:
        sys.exit(main())
    except CensusError as exc:
        print(f"completion census: {exc}", file=sys.stderr)
        sys.exit(1)
