#!/usr/bin/env python3
"""`just done`: check what changed since the last verified commit, and nothing else.

Design: docs/design/change-aware-completion.md. In short:

* A BASELINE is the nearest ancestor of HEAD that an earlier `just done` verified green on a
  clean tree, with the same Go toolchain and linter. Records live in the repository's common Git
  directory, keyed by commit, so every worktree of the repository shares them.
* With no baseline — or when a changed path is a global gate input (go.mod, vendor/, the
  Justfile, a workflow, a script) or cannot be classified — it runs the full landing gate,
  `just check-ci`.
* Otherwise it runs only what the change can reach: `go vet` and staticcheck (both GOOS
  values) and `go test -short` for the changed packages and every package that imports them,
  the individual tests recorded as READING a changed file (scripts/completion-readers.json,
  written by scripts/completion-census.py), gofmt on the changed Go files, the official-binary
  pin check when Go or pack sources moved, and the Markdown, guide and changelog checks for the
  documents that changed.
* It is read-only: it never formats, stages or commits. A dirty tree is refused BEFORE any
  check runs, and a tree that changes while checks run records nothing.

It is not the landing gate: landing still runs `just check-ci`, the integration tests the change
reaches, and nested-jail verification, as AGENTS.md says.
"""
import concurrent.futures as cf
import fcntl
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

SCHEMA = 1
READERS_FILE = "scripts/completion-readers.json"

# Changes to these are inputs to every check, or to the gate itself: full `just check-ci`.
GLOBAL_FILES = {
    "go.mod", "go.sum", "Justfile", "mise.toml", "mise.lock", "flake.nix", "flake.lock",
    ".gitignore", ".gitattributes", ".goreleaser.yaml", ".vantage.toml", "docs-worker.js",
    "docs-wrangler.toml",
}
GLOBAL_PREFIXES = ("vendor/", ".github/", "scripts/")

# Documents whose Markdown findings are deliberate: specimens of the checker's own rules, which
# the document says must never be linked away (its opening NOTE). Their reader tests still run.
MARKDOWN_SPECIMENS = {"docs/research/vantage-check-0.5.9-findings.md"}

# Context that decides what an inherited result means. A baseline recorded under any other
# value is not used.
GO_ENV_KEYS = ["GOVERSION", "GOOS", "GOARCH", "GOROOT", "GOFLAGS", "CGO_ENABLED", "GOEXPERIMENT",
               "GOAMD64", "GOARM64", "GOTOOLCHAIN", "GOWORK", "CC"]

LINT_PASSES = [
    ("linux", ["go", "vet"]),
    ("linux", ["staticcheck"]),
    ("darwin", ["go", "vet"]),
    ("darwin", ["staticcheck", "-checks=inherit,-SA4023"]),
]

NOT_COVERED = ("integration suite, nested-jail verification and native macOS execution are not "
               "part of `just done`; landing still runs `just check-ci` (AGENTS.md, Workflow)")


class Refusal(Exception):
    """A stop that runs no check. The message names the next step."""


def git(root, *args, check=True):
    r = subprocess.run(["git", "--no-optional-locks", "-C", str(root), *args],
                       capture_output=True)
    if check and r.returncode != 0:
        raise Refusal(f"git {' '.join(args)} failed: {r.stderr.decode(errors='replace').strip()}")
    return r.stdout.decode()


def tool(name):
    path = shutil.which(name)
    if not path:
        raise Refusal(f"`{name}` is not on PATH. Install the development tools (`mise install`) "
                      "and rerun `just done`.")
    return path


# ---------------------------------------------------------------- tree state


def dirty_paths(root):
    out = git(root, "status", "--porcelain=v1", "-z", "--untracked-files=all",
              "--ignore-submodules=none")
    return [e for e in out.split("\0") if e]


def head(root):
    commit = git(root, "rev-parse", "--verify", "HEAD^{commit}").strip()
    tree = git(root, "rev-parse", "--verify", "HEAD^{tree}").strip()
    return commit, tree


def context(root):
    tool("go")
    sc = tool("staticcheck")
    env = json.loads(subprocess.run(["go", "env", "-json", *GO_ENV_KEYS], cwd=root,
                                    capture_output=True, check=True).stdout)
    ver = subprocess.run([sc, "-version"], capture_output=True, text=True).stdout.strip()
    return {"go": env, "staticcheck": {"path": os.path.realpath(sc), "version": ver}}


# ---------------------------------------------------------------- records


class Store:
    def __init__(self, root):
        common = git(root, "rev-parse", "--git-common-dir").strip()
        self.dir = (Path(root) / common).resolve() / "yolo-completion" / "verified"
        own = Path(git(root, "rev-parse", "--absolute-git-dir").strip())
        self.private = own / "yolo-completion"

    def load(self, commit):
        try:
            rec = json.loads((self.dir / f"{commit}.json").read_text())
        except (OSError, ValueError):
            return None
        return rec if isinstance(rec, dict) else None

    def baseline(self, root, ctx, max_count=2000):
        """The nearest ancestor of HEAD with a valid record made under this context."""
        for commit in git(root, "rev-list", f"--max-count={max_count}", "HEAD").split():
            if not (self.dir / f"{commit}.json").exists():
                continue
            rec = self.load(commit)
            if (rec and rec.get("schema") == SCHEMA and rec.get("commit") == commit
                    and rec.get("context") == ctx
                    and rec.get("tree") == git(root, "rev-parse", f"{commit}^{{tree}}").strip()):
                return rec
        return None

    def save(self, rec):
        self.dir.mkdir(parents=True, exist_ok=True)
        tmp = self.dir / f".{rec['commit']}.{os.getpid()}.tmp"
        tmp.write_text(json.dumps(rec, indent=1, sort_keys=True) + "\n")
        os.replace(tmp, self.dir / f"{rec['commit']}.json")


# ---------------------------------------------------------------- what changed


def changed(root, base):
    """Every path that differs between the baseline's tree and HEAD's, as (status, path, old
    mode, new mode). Renames are a deletion plus an addition, so both names are classified."""
    raw = git(root, "diff", "--raw", "-z", "--no-renames", "--no-abbrev", base, "HEAD")
    parts = raw.split("\0")
    out = []
    i = 0
    while i + 1 < len(parts):
        meta, path = parts[i], parts[i + 1]
        i += 2
        fields = meta.lstrip(":").split()
        if len(fields) < 5:
            raise Refusal(f"unreadable `git diff --raw` record: {meta!r}")
        out.append((fields[4][0], path, fields[0], fields[1]))
    return out


# ---------------------------------------------------------------- package graph


def go_graph(root, goos):
    """{import path: package} for the module under one GOOS. None when the graph has an error
    other than a package with no files for this GOOS — an unknown graph is a full run."""
    env = dict(os.environ, GOOS=goos)
    r = subprocess.run(["go", "list", "-e", "-json=ImportPath,Dir,Deps,TestImports,XTestImports,"
                        "Error,DepsErrors,GoFiles,TestGoFiles,XTestGoFiles", "./..."],
                       cwd=root, env=env, capture_output=True)
    if r.returncode != 0:
        return None
    dec = json.JSONDecoder()
    text = r.stdout.decode()
    pkgs = {}
    pos = 0
    while pos < len(text):
        while pos < len(text) and text[pos].isspace():
            pos += 1
        if pos >= len(text):
            break
        obj, pos = dec.raw_decode(text, pos)
        err = (obj.get("Error") or {}).get("Err", "")
        if err:
            if "build constraints exclude all Go files" in err or "no Go files" in err:
                continue
            return None
        if obj.get("DepsErrors"):
            return None
        obj["rel"] = os.path.relpath(obj["Dir"], root)
        pkgs[obj["ImportPath"]] = obj
    return pkgs


def affected(graph, changed_pkgs):
    """Packages whose build or tests reach a changed package: itself, its transitive imports,
    and its test files' imports and theirs."""
    out = set()
    for ip, p in graph.items():
        reach = {ip, *(p.get("Deps") or ())}
        for t in (p.get("TestImports") or []) + (p.get("XTestImports") or []):
            reach.add(t)
            reach.update((graph.get(t) or {}).get("Deps") or ())
        if reach & changed_pkgs:
            out.add(ip)
    return out


def owner(path, pkg_dirs):
    d = os.path.dirname(path)
    while d:
        if d in pkg_dirs:
            return d
        d = os.path.dirname(d)
    return "." if "." in pkg_dirs else None


# ---------------------------------------------------------------- readers


def load_readers(root):
    """{package dir: {test: {"read": [...], "listed": [...]}}}, or None when unusable."""
    try:
        doc = json.loads((Path(root) / READERS_FILE).read_text())
        sets = doc["sets"]
        return {pkg: {name: sets[i] for name, i in tests.items()}
                for pkg, tests in doc["readers"].items()}
    except (OSError, ValueError, KeyError, IndexError, TypeError, AttributeError):
        return None


def _read(entries, path):
    """Whether a recorded read set covers path: by name, as `D/*`, or as `D/*<extension>`."""
    d = os.path.dirname(path)
    star = (d + "/*") if d else "*"
    return (path in entries or star in entries
            or star + os.path.splitext(path)[1] in entries)


def listings_touched(root, base, entries):
    """Directories whose listing differs between the baseline and HEAD: the parent of every
    added or deleted path, and the parent of every directory that appeared or went with it."""
    dirs = {}
    for rev in (base, "HEAD"):
        dirs[rev] = set(git(root, "ls-tree", "-r", "-d", "-z", "--name-only", rev).split("\0")) - {""}
    out = set()
    for status, path, _, _ in entries:
        if status not in "AD":
            continue
        out.add(os.path.dirname(path) or ".")
        d = os.path.dirname(path)
        while d and ((d in dirs[base]) != (d in dirs["HEAD"])):
            out.add(os.path.dirname(d) or ".")
            d = os.path.dirname(d)
    return out


def _listed(entries, d):
    """Whether a recorded listing set covers directory d: by name, or under a `D/**`."""
    if d in entries:
        return True
    while d and d != ".":
        if d + "/**" in entries:
            return True
        d = os.path.dirname(d)
    return False


def scanning(rec):
    """A test that reads every file of a kind in some directory, or lists a whole tree.

    What such a test reads moves with ordinary edits — a doc citation added to Go source makes
    the citation test read that doc — so its recorded set cannot be trusted to select it. It runs
    on every change instead; Go's own test cache, which tracks the files it actually opened,
    keeps that cheap when nothing it reads has changed."""
    return any("*" in x for x in rec["read"]) or any(x.endswith("/**") for x in rec["listed"])


def reader_tests(readers, entries, listings):
    """{package dir: {test}} for every scanning test, and every recorded test that read a
    changed path or listed a directory whose entries changed."""
    out = {}
    paths = [p for _, p, _, _ in entries]
    for pkg, tests in readers.items():
        for name, rec in tests.items():
            if (scanning(rec) or any(_read(rec["read"], p) for p in paths)
                    or any(_listed(rec["listed"], d) for d in listings)):
                out.setdefault(pkg, set()).add(name)
    return out


def _read_by_any(readers, path):
    return any(_read(rec["read"], path) for tests in readers.values() for rec in tests.values())


# ---------------------------------------------------------------- the plan


class Plan:
    def __init__(self):
        self.full = []          # reasons a full gate is needed
        self.steps = []         # (label, argv, env overrides, reason, kind)
        self.inherited = []
        self.notes = []

    def add(self, label, argv, reason, env=None, kind="check"):
        self.steps.append((label, argv, env or {}, reason, kind))


def is_markdown(path):
    return path.endswith(".md")


def plan_selective(root, base, entries, readers):
    plan = Plan()
    if readers is None:
        plan.full.append(f"{READERS_FILE} is missing or unreadable")
        return plan
    for status, path, old, new in entries:
        types = {m[:2] for m in (old, new) if m != "000000"}
        if types - {"10"}:
            plan.full.append(f"{path}: not a regular file (mode {old} -> {new})")
        elif path in GLOBAL_FILES or path.startswith(GLOBAL_PREFIXES):
            plan.full.append(f"{path}: a gate or build input for the whole tree")
    if plan.full:
        return plan

    paths = [p for _, p, _, _ in entries]
    # Only load the Go graph when a path could be Go-owned: every package directory holds a .go file.
    needs_graph = any(p.endswith(".go") or _under_go_dir(root, p) for p in paths)
    graphs = {}
    if needs_graph:
        for goos in ("linux", "darwin"):
            g = go_graph(root, goos)
            if g is None:
                plan.full.append(f"`GOOS={goos} go list ./...` reports an error")
                return plan
            graphs[goos] = g
    pkg_dirs = {p["rel"]: ip for g in graphs.values() for ip, p in g.items()}

    changed_pkgs, gofmt, docs, guide, changelog = set(), [], [], False, False
    owned = False
    reads = reader_tests(readers, entries, listings_touched(root, base, entries))
    for status, path, _, _ in entries:
        o = owner(path, pkg_dirs) if pkg_dirs else None
        if o is not None:
            owned = True
            changed_pkgs.add(pkg_dirs[o])
            if path.endswith(".go") and status != "D":
                gofmt.append(path)
            continue
        if path.endswith(".go"):
            plan.full.append(f"{path}: a Go file in no package of this module")
            continue
        if path.startswith("userguide/"):
            guide = True
            continue
        if path == "CHANGELOG.md":
            changelog = True
        if is_markdown(path):
            if path in MARKDOWN_SPECIMENS:
                plan.notes.append(f"{path}: not Markdown-checked, a specimen document")
            elif status != "D":
                docs.append(path)
            continue
        if _read_by_any(readers, path):
            continue
        plan.full.append(f"{path}: no check or recorded reader covers it")
    if plan.full:
        return plan

    # Go: lint and test what the changed packages reach.
    if changed_pkgs:
        for goos, base in LINT_PASSES:
            pkgs = sorted(affected(graphs[goos], changed_pkgs))
            if pkgs:
                plan.add(f"{' '.join(base[:2]) if base[0] == 'go' else base[0]} GOOS={goos} "
                         f"({len(pkgs)} package{'s' if len(pkgs) != 1 else ''})",
                         base + pkgs, "changed packages and their importers", {"GOOS": goos})
        tested = sorted(affected(graphs["linux"], changed_pkgs))
        if tested:
            plan.add(f"go test -short ({len(tested)} package{'s' if len(tested) != 1 else ''})",
                     ["go", "test", "-short"] + tested, "changed packages and their importers")
    else:
        tested = []
    if gofmt:
        plan.add(f"gofmt -l ({len(gofmt)} file{'s' if len(gofmt) != 1 else ''})",
                 ["gofmt", "-l"] + sorted(gofmt), "changed Go files", kind="gofmt")
    if owned:
        plan.add("official pack binary pins", ["go", "run", "./tools/pack-binaries", "check"],
                 "Go or pack sources changed")

    # Tests recorded as reading a changed path, in packages not already tested whole.
    linux = graphs.get("linux")
    tested_dirs = {linux[ip]["rel"] for ip in tested} if linux else set()
    for pkg in sorted(reads):
        if pkg in tested_dirs or not (Path(root) / pkg).is_dir():
            continue
        names = sorted(reads[pkg])
        rx = "^(" + "|".join(names) + ")$"
        plan.add(f"go test -short -run <{len(names)} reader test{'s' if len(names) != 1 else ''}> "
                 f"./{pkg}", ["go", "test", "-short", "-run", rx, "./" + pkg],
                 "tests that read a changed file, and every scanning test")

    # Documents.
    if changelog:
        plan.add("changelog section extraction tests", ["sh", "scripts/test-changelog-section.sh"],
                 "CHANGELOG.md changed")
    if guide:
        plan.add("user guide closed tree", ["python3", "scripts/check-userguide-closed-tree.py",
                                            "userguide"], "the user guide changed")
        plan.add("vantage-check userguide/", ["scripts/vantage-check.sh", "userguide/"],
                 "the user guide changed")
    if docs:
        plan.add(f"vantage-check ({len(docs)} changed document{'s' if len(docs) != 1 else ''})",
                 ["scripts/vantage-check.sh"] + sorted(docs), "changed Markdown")

    plan.inherited = [
        "every other package's vet, staticcheck and short tests",
        "gofmt of unchanged files",
    ]
    if not owned:
        plan.inherited.append("the official pack binary pins")
    if not guide:
        plan.inherited.append("the user guide and site checks")
    if not changelog:
        plan.inherited.append("the changelog checks")
    return plan


def _under_go_dir(root, path):
    d = os.path.dirname(path)
    while d:
        full = Path(root) / d
        if full.is_dir() and any(f.suffix == ".go" for f in full.iterdir() if f.is_file()):
            return True
        d = os.path.dirname(d)
    return False


# ---------------------------------------------------------------- running


def run_step(root, step):
    label, argv, env, reason, kind = step
    start = time.monotonic()
    r = subprocess.run(argv, cwd=root, env=dict(os.environ, **env), stdin=subprocess.DEVNULL,
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    out = r.stdout.decode(errors="replace")
    rc = r.returncode
    if kind == "gofmt" and rc == 0 and out.strip():
        out = "gofmt needs to run on:\n" + out + "Run `just format`, commit, and rerun `just done`.\n"
        rc = 1
    return label, argv, rc, out, time.monotonic() - start


def run_steps(root, steps, log):
    results = []
    with cf.ThreadPoolExecutor(max(1, len(steps))) as ex:
        for label, argv, rc, out, secs in ex.map(lambda s: run_step(root, s), steps):
            mark = "ok  " if rc == 0 else "FAIL"
            print(f"  [{mark}] {label}  ({secs:.1f}s)", flush=True)
            log.write(f"$ {' '.join(argv)}\n{out}\n[exit {rc}]\n\n")
            if rc != 0:
                print("\n".join("        " + line for line in out.rstrip().splitlines()[-60:]))
            results.append({"label": label, "argv": argv, "rc": rc, "seconds": round(secs, 2)})
    return results


def run_full(root, log):
    start = time.monotonic()
    print("  running `just check-ci` (the landing gate)", flush=True)
    p = subprocess.Popen(["just", "check-ci"], cwd=root, stdin=subprocess.DEVNULL,
                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    for line in p.stdout:
        text = line.decode(errors="replace")
        sys.stdout.write(text)
        log.write(text)
    rc = p.wait()
    secs = time.monotonic() - start
    print(f"  [{'ok  ' if rc == 0 else 'FAIL'}] just check-ci  ({secs:.1f}s)", flush=True)
    return [{"label": "just check-ci", "argv": ["just", "check-ci"], "rc": rc,
             "seconds": round(secs, 2)}]


# ---------------------------------------------------------------- main


def short(c):
    return c[:12]


def main():
    root = Path(git(".", "rev-parse", "--show-toplevel").strip())
    store = Store(root)
    store.private.mkdir(parents=True, exist_ok=True)
    lock = open(store.private / "lock", "w")
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        raise Refusal("another `just done` is running in this worktree. Wait for it to finish, "
                      "then rerun `just done`.")

    dirty = dirty_paths(root)
    if dirty:
        print("just done: the working tree is not clean; no check ran:")
        for e in dirty[:40]:
            print("  " + e)
        if len(dirty) > 40:
            print(f"  ... and {len(dirty) - 40} more")
        raise Refusal("commit the work (run `just format` first if gofmt is the reason), then "
                      "rerun `just done`.")

    commit, tree = head(root)
    ctx = context(root)
    base = store.baseline(root, ctx)

    if base and base["commit"] == commit:
        print(f"just done: {short(commit)} was already verified ({base.get('route')}, "
              f"{base.get('verified_at')}); nothing changed since.")
        print(f"Not covered: {NOT_COVERED}.")
        return 0

    if base is None:
        entries = None
        plan = Plan()
        plan.full.append("no verified ancestor of HEAD with this toolchain (first run here, or "
                         "the Go toolchain or staticcheck changed)")
    else:
        entries = changed(root, base["commit"])
        if not entries:
            plan = Plan()
            # Same tree as the baseline: its result covers this commit too.
            print(f"just done: HEAD's tree is the tree verified at {short(base['commit'])}; "
                  "nothing to check.")
        else:
            plan = plan_selective(root, base["commit"], entries, load_readers(root))

    with open(store.private / "last.log", "w") as log:
        if base is None or plan.full:
            print("just done: running the full gate, because:")
            for reason in plan.full[:10]:
                print("  - " + reason)
            if len(plan.full) > 10:
                print(f"  ... and {len(plan.full) - 10} more")
            route = "full"
            results = run_full(root, log)
            # Changed documents get their own checks too; check-ci covers only the guide and changelog.
            if entries:
                docs = sorted(p for s, p, _, _ in entries if is_markdown(p) and s != "D"
                              and p not in MARKDOWN_SPECIMENS
                              and not p.startswith("userguide/") and p != "CHANGELOG.md"
                              and not _under_go_dir(root, p))
                if docs:
                    results += run_steps(root, [(f"vantage-check ({len(docs)} changed documents)",
                                                 ["scripts/vantage-check.sh"] + docs, {},
                                                 "changed Markdown", "check")], log)
            origin = commit
        else:
            route = "selective" if plan.steps else "unchanged"
            n = len(entries or [])
            print(f"just done: {n} path{'s' if n != 1 else ''} changed since {short(base['commit'])} "
                  f"(verified {base.get('verified_at')}); checking only what they reach:")
            for note in plan.notes:
                print("  note: " + note)
            results = run_steps(root, plan.steps, log) if plan.steps else []
            origin = base.get("origin", base["commit"])

    failed = [r for r in results if r["rc"] != 0]
    if failed:
        print(f"\njust done: FAILED ({', '.join(r['label'] for r in failed)}). Nothing was "
              f"recorded. Full output: {store.private / 'last.log'}. Fix it, commit, and rerun "
              "`just done`.")
        return 1

    after = dirty_paths(root)
    if after or head(root)[0] != commit:
        print("just done: the tree changed while the checks ran; nothing was recorded:")
        for e in after[:20]:
            print("  " + e)
        raise Refusal("wait for whatever is writing to finish, commit, and rerun `just done`.")

    store.save({
        "schema": SCHEMA, "commit": commit, "tree": tree, "context": ctx, "route": route,
        "baseline": base["commit"] if base else None, "origin": origin,
        "verified_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "steps": results,
    })
    if route == "selective" and plan.inherited:
        print(f"Inherited from {short(base['commit'])} (inputs unchanged): "
              + "; ".join(plan.inherited) + ".")
    print(f"Not covered: {NOT_COVERED}.")
    print(f"just done: verified {short(commit)} ({route}); working tree clean.")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Refusal as e:
        print(f"just done: {e}", file=sys.stderr)
        sys.exit(1)
