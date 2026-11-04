#!/usr/bin/env python3
"""Select this module's Go packages from Git-visible Go files.

Go's ./... walks ignored files too. Start from files Git tracks or reports as non-ignored
untracked, then ask go list which selected directories have source under the active build tags
and GOOS. Go still sees every file in each selected directory, including ignored extras.
"""
import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# The short suite's go test -timeout, which is per PACKAGE, for both runs of it: `just
# test-fast` (the Justfile spells it, and test-go-packages.py pins the spelling to this) and
# `just done`'s changed-package run (completion-check.py). internal/cli is the long pole: one
# process of thousands of tests, serial by design (t.Setenv throughout), starting tens of
# thousands of git processes, most of them its patched-build tests' replays. Measured 2026-11-03,
# with no test hung in any run: about 3 minutes on a 32-core Linux machine, about 9 on an idle
# macOS CI runner (check-macos's three shards summed), where a process start costs far more, and
# 22.6 on a Mac also running the macos-user and integration suites
# (docs/lab-reports/2026-11-03-mac-slice-12a-witness-w1.md). The bound sits at twice the
# slowest, so a hang still panics with every goroutine's stack.
SHORT_TEST_TIMEOUT = "45m"


def _has_git_metadata(root):
    """Whether Git metadata exists for this directory; metadata makes Git errors fatal."""
    for directory in (root, *root.parents):
        if os.path.lexists(directory / ".git"):
            return True
    return bool(os.environ.get("GIT_DIR") or os.environ.get("GIT_WORK_TREE")
                or os.environ.get("GIT_COMMON_DIR"))


def _git_go_files(root):
    try:
        result = subprocess.run(
            ["git", "-C", str(root), "ls-files", "-z", "--cached", "--others", "--exclude-standard",
             "--", "*.go", ":(exclude)vendor/**"],
            check=True, stdout=subprocess.PIPE)
    except subprocess.CalledProcessError as exc:
        raise RuntimeError("Git-aware Go package selection failed in a Git checkout") from exc
    return [p for p in result.stdout.decode().split("\0") if p]


def _listed_packages(root, env, *patterns):
    result = subprocess.run(
        ["go", "list", "-e", "-json=ImportPath,Dir,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles,Error",
         *patterns], cwd=root, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        raise RuntimeError(result.stderr.decode(errors="replace").strip() or
                           f"go list exited {result.returncode}")
    decoder = json.JSONDecoder()
    text = result.stdout.decode()
    pos, selected = 0, []
    while pos < len(text):
        while pos < len(text) and text[pos].isspace():
            pos += 1
        if pos == len(text):
            break
        obj, pos = decoder.raw_decode(text, pos)
        if obj.get("Error"):
            error = obj["Error"].get("Err", "package loading failed")
            if error.startswith("build constraints exclude all Go files"):
                continue
            raise RuntimeError(f"go list {obj.get('ImportPath', 'package')}: {error}")
        if any(obj.get(key) for key in ("GoFiles", "CgoFiles", "TestGoFiles", "XTestGoFiles")):
            rel = os.path.relpath(obj["Dir"], root)
            selected.append("." if rel == "." else "./" + Path(rel).as_posix())
    return sorted(set(selected))


def _eligible_source(path):
    parts = Path(path).parts
    directories = parts[:-1]
    return (path.endswith(".go") and not Path(path).name.startswith((".", "_"))
            and not any(part == "testdata" or part.startswith((".", "_")) for part in directories)
            and not any(part == "vendor" for part in directories))


def package_patterns(root=ROOT, env=None):
    """Return active local package patterns rooted in Git-visible Go source directories.

    Git's cached set includes tracked files even when an ignore rule matches them; its others
    set includes non-ignored untracked files only. Go list applies build constraints and module
    semantics to those candidate directories. Its source list selects the package, not its
    contents: additional ignored files in that real package remain visible to Go and its checks.
    """
    root = Path(root).resolve()
    if not _has_git_metadata(root):
        return _listed_packages(root, env, "./...")

    candidates = set()
    for path in _git_go_files(root):
        if not _eligible_source(path):
            continue
        rel = Path(path)
        # Go's ./... does not cross a nested module boundary, even if that module is untracked.
        parents = list(rel.parents)
        if any((root / parent / "go.mod").is_file() for parent in parents if str(parent) != "."):
            continue
        candidates.add("." if str(rel.parent) == "." else "./" + rel.parent.as_posix())
    if not candidates:
        return []
    return _listed_packages(root, env, *sorted(candidates))


def run(args):
    if not args:
        raise ValueError("usage: go_packages.py run <command> [args ...] [-- <selected package> ...]")
    if "--" in args:
        split = args.index("--")
        command, requested = args[:split], args[split + 1:]
    else:
        command, requested = args, None
    if not command:
        raise ValueError("a command is required before --")
    source_archive = not _has_git_metadata(ROOT)
    if source_archive and requested is None:
        return subprocess.call(command + ["./..."], cwd=ROOT)
    packages = package_patterns()
    if requested is not None:
        unknown = sorted(set(requested) - set(packages))
        if unknown:
            raise ValueError("requested package is not selected by Git-visible Go files: " +
                             ", ".join(unknown))
        packages = requested
    if not packages:
        raise ValueError("no Go packages selected from Git-visible Go files")
    return subprocess.call(command + packages, cwd=ROOT)


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    try:
        if argv and argv[0] == "run":
            return run(argv[1:])
        if argv:
            raise ValueError("expected subcommand: run")
        print("\n".join(package_patterns()))
        return 0
    except (OSError, RuntimeError, ValueError) as exc:
        print(f"go package selection: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
