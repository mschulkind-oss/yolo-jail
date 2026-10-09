#!/usr/bin/env python3
"""Regenerate scripts/completion-readers.json: which Go tests read repository files outside
their own package directory.

`just done` (scripts/completion-check.py) selects Go tests from the packages a change can reach
through imports. A test that READS a file it does not import — a doc, a manifest, another
package's source — is invisible to the import graph, so this census records those reads, per
test, from Go's own test log (`-test.testlogfile`, the record `go test` uses to invalidate its
cache), and the selector reruns a test whenever a change touches what it read.

Run it after adding or changing a test that reads outside its package directory:

    python3 scripts/completion-census.py

It runs every test of every package once, one process per test function, so it takes minutes,
not seconds. It writes only the readers file. Reads by a test's SUBPROCESSES (git, go build) are
not in the log; that is the same limit Go's own test cache has.
"""
import concurrent.futures as cf
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "scripts" / "completion-readers.json"


def go_packages():
    """(packages with tests, every package directory), for both GOOS values' package sets."""
    out = subprocess.check_output(
        ["go", "list", "-e", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{len .TestGoFiles}}\t{{len .XTestGoFiles}}",
         "./..."], cwd=ROOT, text=True)
    pkgs, dirs = [], set()
    for line in out.splitlines():
        ip, d, t, x = line.split("\t")
        dirs.add(os.path.relpath(d, ROOT))
        if int(t) or int(x):
            pkgs.append((ip, Path(d)))
    return pkgs, dirs


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


def parse_log(text, pkgdir, pkg_dirs):
    """The test log's open/stat entries that belong to no package or another package.

    Ownership is by package, not by directory prefix: internal/cli's tests reading
    internal/cli/run/x.go read another package. A directory that was opened was listed; a file
    that was opened was read; a stat saw size, mode and mtime. All three are what Go's test cache
    hashes for the same entries."""
    opened, listed, stats = set(), set(), set()
    own = os.path.relpath(pkgdir, ROOT)
    for line in text.splitlines():
        op, _, path = line.partition(" ")
        if op not in ("open", "stat"):
            continue
        r = rel(path, pkgdir)
        if r is None or r == ".git" or r.startswith(".git" + os.sep):
            continue
        if owner(r, pkg_dirs) == own:
            continue
        if (ROOT / r).is_dir():
            # A listed or stat'ed directory changes only when a direct child appears or goes.
            listed.add(r)
        elif op == "stat":
            stats.add(r)
        else:
            opened.add(r)
    stats -= opened | listed
    return opened, listed, stats


def tracked_dirs():
    files = subprocess.check_output(["git", "-C", str(ROOT), "ls-files", "-z"], text=True).split("\0")
    by_dir = {}
    for f in filter(None, files):
        by_dir.setdefault(os.path.dirname(f) or ".", set()).add(f)
    return by_dir


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


def main():
    jobs_n = int(os.environ.get("CENSUS_JOBS", str(max(2, (os.cpu_count() or 4) // 2))))
    pkgs, pkg_dirs = go_packages()
    by_dir = tracked_dirs()
    with tempfile.TemporaryDirectory(prefix="completion-census-") as tmp:
        tmp = Path(tmp)

        def build(i):
            b = tmp / f"{i}.test"
            subprocess.run(["go", "test", "-c", "-o", str(b), pkgs[i][0]], cwd=ROOT, check=True,
                           capture_output=True)
            return b

        with cf.ThreadPoolExecutor(jobs_n) as ex:
            bins = list(ex.map(build, range(len(pkgs))))
        jobs = []
        for i, (ip, d) in enumerate(pkgs):
            names = subprocess.run([str(bins[i]), "-test.list", ".*"], cwd=d, capture_output=True,
                                   text=True).stdout.split()
            jobs += [(i, n) for n in names if n[:1].isupper()]
        print(f"census: {len(pkgs)} packages, {len(jobs)} tests, {jobs_n} at a time", flush=True)

        def run(job):
            i, name = job
            ip, d = pkgs[i]
            log = tmp / f"{i}-{name}.log"
            try:
                subprocess.run([str(bins[i]), "-test.short", "-test.run", f"^{name}$",
                                f"-test.testlogfile={log}", "-test.timeout=600s"],
                               cwd=d, capture_output=True, timeout=700)
            except subprocess.TimeoutExpired:
                pass
            text = log.read_text(errors="replace") if log.exists() else ""
            return i, name, parse_log(text, d, pkg_dirs)

        readers, sets, index = {}, [], {}
        with cf.ThreadPoolExecutor(jobs_n) as ex:
            for k, (i, name, (opened, listed, stats)) in enumerate(ex.map(run, jobs)):
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
    doc = {
        "about": "Generated by scripts/completion-census.py; read by scripts/completion-check.py. "
                 "readers: package directory -> test -> index into sets. A set names the repository "
                 "paths outside the package directory that the test read or stat'ed (`D/*` is every "
                 "tracked file directly in D, `D/*.E` every one with that extension) and the "
                 "directories it listed or stat'ed (`D/**` is D and every directory under it). "
                 "Regenerate, do not edit.",
        "sets": sets,
        "readers": {p: dict(sorted(t.items())) for p, t in sorted(readers.items())},
    }
    OUT.write_text(json.dumps(doc, indent=0, sort_keys=False) + "\n")
    print(f"census: wrote {OUT.relative_to(ROOT)} ({sum(len(t) for t in readers.values())} reading tests "
          f"in {len(readers)} packages)")


if __name__ == "__main__":
    sys.exit(main())
