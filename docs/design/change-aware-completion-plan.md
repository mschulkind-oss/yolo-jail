---
title: "Sketch: map read-only completion onto the real recipe"
date: 2026-10-09
status: draft
stage: SKETCH
next: "Close the default context profile, renderer adapter and complete observer controls; then replace the diagnostic with a promotion-grade real-done fixture"
depends-on: [change-aware-completion.md]
tags: [testing, tooling, implementation-sketch]
summary: "Source-prepared baseline and failure protocol, real-recipe diagnostic reds, and explicit remaining engineering gates; not a build hand-off."
---

# Sketch: map read-only completion onto the real recipe

**Status:** 2026-10-09 — source preparation and bounded offline diagnostics only; no implementation.
Written against `b2eeffc12b631d5cda108238e77a6c4cb5bb777c`; newer primary planning was read
separately. No gate ran on the project checkout; only copied-recipe fake-tool diagnostics ran.
No build, integration, native execution or performance measurement ran.

**Design:** [completion behavior](change-aware-completion.md). The design wins on behavior;
the tree wins on facts; this sketch is advice and is not a hand-off. There are no owner questions.
The smart preparation role must close the engineering gates below; the implementation role must
not invent their missing evidence.

## Before promotion

| Gap | Prepared result | Still required before hand-off |
| :--- | :--- | :--- |
| Context | Two-phase identity protocol; ambient Go/test inputs traced; no Go argv on prose | Source-audited default profile and an executable positive resolver fixture |
| Anchors/referrers | Actual released renderer and checker pipelines inspected; source matcher differs | Provisioned adapter using those pipelines, complete graph and compatibility fixtures |
| Baseline/history | Real Git probes cover intermediate revert, both merge-parent edges, both rename names, linked administration | Add dirty/mode/copy/shallow/error cases to the real front-door fixture |
| Persistence/failure | Exact record paths, ordering, crash/failure behavior below | Executable atomic-publication, interruption and stale-failure controls |
| Stable interval | Standalone observation replaces certification; Linux inode event detects restored writes | Registration, directory/replacement/alias, overflow and native supported-backend controls |
| Caller tests | Real recipe full control; deterministic prose red and caller-deletion red | Closed fake context/adapter protocol, reproducible red assertions and bounded prototype-positive controls; restored production green is required after implementation |

No column claiming a prepared protocol claims a built implementation. Promotion remains blocked
on engineering evidence, not an owner ruling. Do not hand this file to a builder as a closed plan.

## Existing file map and proposed interface

| Path | Future use; not an edit made here |
| :--- | :--- |
| [Completion recipe](../../Justfile#L481-L498) | Call checkout-only `python3 scripts/completion-check.py`; remove mutating `check` dependency |
| [Quality recipes](../../Justfile#L409-L474) | Keep `check` and `check-ci` unchanged; full route invokes real `just check-ci` |
| `scripts/completion-check.py` (new, proposed) | Zero-argument worktree front door; internal helpers are not a new installed CLI |
| `scripts/test-completion-check.py` (new, proposed) | Real `just done`, real Git, fake quality tools; add its read-only invocation to `lint-ci` |
| [Vantage wrapper](../../scripts/vantage-check.sh) | Preserve output isolation and exit status; strict actual changed-doc checks |
| [Wrapper tests](../../scripts/test-vantage-check.py) | Existing fake-tool protocol and call-site test pattern |
| [Lint pin](../../internal/capture/lintgate_pin_test.go), [toolchain pin](../../internal/capture/gatetoolchain_pin_test.go) | Preserve existing lint commands; extend dependency/caller evidence |
| [Official binary call sites](../../tools/pack-binaries/callsites_test.go) | Preserve `check`/`check-ci` pin dependencies; test real completion reachability |
| [Source citations](../../internal/paths/doccitations_test.go) | Preserve full short-suite coverage and exceptions; do not replace this with Vantage slugs |
| [Testing/landing instructions](../../AGENTS.md#testing) | Eventual completion description only; existing obligations remain |

Advice: Python matches existing offline script tests and offers NUL-safe subprocess capture and
atomic rename without a new Go invocation. Cheap choices: private function names and factoring.
Constraint: analyzer dependencies and platform observer interface are **not** closed cheap choices.
No source, recipe, instructions or configuration edits are authorized by this preparation.

## Verification context

**Binding behavior:** ordinary standalone invocation; no task-start flag, caller certificate,
completion-specific environment switch or Go-family argv on the positive prose route.

The full-run capture may query effective Go settings, then stores only the identified non-secret
context and tool/input identities. The later probe resolves files without executing Go, gofmt or
staticcheck. It must establish these boundaries, not reconstruct defaults from a version string:

| Input boundary | Source finding and preparation requirement |
| :--- | :--- |
| Go user settings | Go 1.26.7 `cmd/go/internal/cfg.EnvFile` uses explicit `GOENV` or `os.UserConfigDir()/go/env`; `initEnvCache` also reads `GOROOT/go.env`. Record both presence and content identity, including missing-to-present transitions |
| Effective build selection | Environment overrides file defaults; implicit cgo depends on C-compiler PATH availability. Automatic toolchain/workspace selection, tags, overlays, architecture knobs and compiler wrappers cannot be ignored |
| Tool resolution | Fingerprint resolved executable plus the compiler/standard-library installation, not just `VERSION`; direct tools and declared mise layout need offline resolver fixtures. An unknown shim selects full |
| Staticcheck | Project/ancestor configuration and effective build environment are inputs, as well as its executable and Darwin exclusion |
| Official builds | [Recipe](../../tools/pack-binaries/recipe.go) scrubs most GO/CGO inputs, but retains selected cache paths; [toolchain resolver](../../tools/pack-binaries/toolchain.go) consults proxy environment and Go env files before selecting exact Go 1.26.7 |
| Short tests | Tests and called production readers inspect HOME, PATH, CI, dynamic variable names and YOLO test controls; an environment-key grep is not a complete external-file audit |
| Documentation tools | Actual checker release/executable, wrapper, `.vantage.toml`, ignore/discovery inputs and provisioned parser modules are context; latest resolution is not a stable version pin |

Source checked from the installed Go source and repository on 2026-10-09; see [research](../research/completion-check-inputs.md#verification-context-is-not-just-go-version).
The supported default profile must be accepted automatically after a real full success. Unsupported
custom settings still run full, with no inherited coverage. Do not achieve reachability by clearing
ambient variables, replacing HOME or changing gate flags: that would verify another context.

**Actionable remaining audit:** enumerate the effective external readers reachable under
`go test -short ./...`, distinguish test-local `t.Setenv`/temp fixtures from inherited inputs,
and specify default-profile file roots and tool resolution. Prove changed Go env file, newly
available compiler, PATH/tool replacement, ignored settings and test-control inputs select full.
Use resolved-file identities on prose, not `go env` disguised as a cheap metadata probe. No
supported profile is yet source-proven; a fake `{}` Go-environment reply cannot stand in for it.

## Anchors and incoming references

The cached released Vantage 0.9.2 binary contains readable bundled source modules. Inspection
found these actual mechanisms, not an inferred heading regex:

- [Renderer pipeline](https://github.com/mschulkind-oss/vantage/blob/v0.9.2/packages/vantage-md/src/pipeline.ts): raw HTML, directives and sanitization precede question-anchor assignment, then heading slugs.
- [Question-anchor assignment](https://github.com/mschulkind-oss/vantage/blob/v0.9.2/packages/vantage-md/src/rehypeVantageAnchors.ts): a carried valid ID fills only an element without an existing nonempty ID.
- [Checker index](https://github.com/mschulkind-oss/vantage/blob/v0.9.2/packages/vantage-check/src/core/slugs.ts): mdast heading text uses `github-slugger`; HTML `id`/`name` and valid question IDs are added separately.
- [Checker links](https://github.com/mschulkind-oss/vantage/blob/v0.9.2/packages/vantage-check/src/rules/links.ts): visits link, image and definition nodes; relative targets and fragments are checked separately.

The source module names are observed in the released bundle; external links were not fetched.
The CLI exposes check/index/style-guide, **not** parsed-tree or anchor export. A strict PASS does
not provide an ordered anchor signature. Renderer sanitization and checker HTML indexing differ.
The Go citation matcher also differs: it scans explicit double-quoted IDs even in fenced samples,
uses ATX-only heading/fence matching, and does not implement question directives or full GFM.

**Adapter contract to finish:** return ordered rendered element IDs with heading ownership,
checker target IDs/numbered mapping, and parsed link/image/definition targets, using the released
pipelines. Separately preserve the existing source matcher's target interpretation. Compare both
sides of every history edge, not only endpoint sets. Include setext headings, duplicate/collision
order, inline markup/image alt, explicit IDs, sanitized HTML, valid/invalid question directives,
mixed/long fences, nested containers and line anchors in equivalence fixtures.

Advice: reuse provisioned upstream parser modules rather than vendor a second Markdown parser.
Remaining dependency: identify an offline installed/exported interface and pin compatible inputs;
the compiled checker alone is not that interface. Until this is proven, the proposed adapter is
not an executable dependency, and the shortcut is not ready for implementation.

Referrers may be a conservative superset of all tracked Markdown with parsed targets resolving to
the changed files. Read both historical and current sides; check surviving current referrers.
A bounded textual prefilter may add false positives, never exclude candidates on an unproved
syntax assumption. Read/parse/discovery failure selects full. On full, retain the source-citation
test's exact directories, root files, self-skip and fixture/history exceptions. No claim of a new
lightweight source-citation executable is made here.

## Whole-task baseline and Git commands

Use `git -C <resolved-root>` throughout; reject ambient Git location/index/object overrides and
unsupported replace/graft/shallow history rather than silently changing the meaning of the range.
Resolve `--absolute-git-dir` for records, **not** `--git-common-dir`: linked worktrees have distinct
administration directories and share the common directory. Never assume `.git` is a directory.

Git syntax probed in a synthetic repository with local-only history:

```bash
git -C "$root" rev-parse --show-toplevel --absolute-git-dir
git -C "$root" cat-file -e "$base^{commit}"
git -C "$root" merge-base --is-ancestor "$base" HEAD
git -C "$root" diff-tree --no-commit-id -r --raw -z --no-renames "$base" HEAD
git -C "$root" rev-list --parents "$base..HEAD"
# For every listed child, for EACH parent, including parents outside the range:
git -C "$root" diff-tree --no-commit-id -r --raw -z --no-renames "$parent" "$child"
git -C "$root" diff --cached --raw -z --no-renames HEAD
git -C "$root" diff --raw -z --no-renames
git -C "$root" ls-files --others --exclude-standard -z
git -C "$root" status --porcelain=v2 -z --untracked-files=all --ignore-submodules=none
```

`--no-renames` deliberately yields deletion plus addition: both names force full, without a rename
similarity threshold. A copy leaves an added destination, also full. Parse raw records with their
old/new modes and object IDs; a status letter alone loses executable/type changes. An undecodable
path or malformed/error output is unknown. Collect first so dirty-path reasons can be printed,
then refuse initial dirty state before gates. Missing/unverifiable/nonancestor baseline selects
full; unreadable Git identity refuses. Empty union selects full.

**Observed diagnostic:** net baseline-to-HEAD hid the intermediate source modification and revert;
the per-parent edge union retained it. Both merge-parent comparisons exposed their different
paths. Rename raw output contained both old and new paths. Ordinary and linked worktrees resolved
distinct absolute Git directories. These are Git syntax probes, not front-door regression coverage.

## Record persistence and failure evidence

Proposed private protocol, to test before promotion; no manual import or task-start record:

| Location under the worktree Git directory | Contents |
| :--- | :--- |
| `completion/lock` | Nonblocking process-held completion-record lock; no editor fence |
| `completion/attempts/<id>/` | External gate logs, selection/context report and final attempt JSON |
| `completion/latest.json` | Schema/producer, attempt ID and running/failed/success state |
| `completion/success.json` | Last validated reusable success and origin of inherited full coverage |

A success record names schema/producer, canonical worktree identity, HEAD/tree, context identity,
route, actual command argv/exit outcomes, clean/observer result, relative log paths and originating
full attempt ID. Logs must exist/read completely; a missing result or interrupted/latest-running
attempt is not a pass. Never persist credentials or arbitrary environment values. An unsupported
context/observer can produce a full-gate report but **not** a reusable success pointer.

Ordering constraint: acquire lock; create unique attempt; atomically publish latest-running;
collect/probe; run gates with external logs; final clean/context/observer validation; flush logs
and final attempt; atomically replace success; atomically finalize latest. Write temporary JSON
in the same directory, flush/fsync, `os.replace`, then directory fsync on supported local storage.
A crash between success and latest-finalization leaves latest-running: conservatively rerun full.
Do not remove old diagnostic logs during a failing retry or select an older record after latest red.
Lock is released on process exit; unsupported locking/filesystem semantics cannot mint a baseline.

Any verifier failure preserves its status and leaves success bytes unchanged. A latest failed,
interrupted or stability-unknown attempt forces fresh full verification on retry, including the
same tree. A record with missing objects, mismatched worktree/schema/producer/context, bad origin
or missing logs also selects full. First clean supported full success establishes the baseline
without a user marker; only later proven prose success inherits and advances it.

Remaining executable cases: permission/disk/write/rename failure, crash at each ordering boundary,
missing/truncated logs, unsupported schema, nonancestor/deleted objects, forged/mismatched origin,
failed retry after older green, detached/ordinary/linked worktree separation and concurrent records.

## Stable interval

**Do not re-open:** standalone invocation has no certification prerequisite. Record locks and
endpoint equality do not detect every writer. The [design amendment](change-aware-completion.md#6-cleanliness-and-a-stable-verification-interval)
requires automatic bounded observation; it does not assert enforced immutability.

A Linux `inotify` file-inode diagnostic observed modification events after deliberate write/restore
while endpoint bytes matched. That proves only this primitive. A production monitor needs file
and directory watches, input registration before classification, registration/drain barriers,
atomic replacement/alias handling, Git HEAD/index/ref inputs, relevant external context roots,
watch-loss/overflow/error treatment and a final drain before publication. Cache/log output must be
outside observed task inputs, not mistaken for source edits.

**Actionable remaining proof:** build diagnostic fixtures for startup races, replace-then-restore,
hard-link writes, create/delete paths, overflow and lost watches; identify supported filesystem
semantics and the native macOS backend. Unsupported observation runs full without a reusable
baseline; observed input mutation refuses. A polling implementation may not claim to catch a
restore between polls. Native support is unverified, not inferred from Linux events. No suite-wide
lock is introduced, and no token asks a caller to promise that other writers stopped.

## Executable real-done diagnostic

The full test listing below is a **diagnostic red**, not the final regression suite. It copies
only the real recipe and necessary wrapper into a fresh synthetic Git repository; Go/gofmt/
staticcheck, shell leaf checks and doc checking are fake. Git and just are real. Each history
mutation verifies fixture ownership first. Logs remain outside task inputs. It never provisions,
builds, calls an API, starts an agent or invokes a jail.

Save the listing as the proposed `scripts/test-completion-check.py` only in an authorized future
implementation. Run `python3 scripts/test-completion-check.py <checkout-root>`. Current-tree
expected exit is 1 at `listed prose invoked forbidden quality tools`; the full control witnesses
both GOOS vet/staticcheck passes, short units and official pins. Current full control also records
`gofmt -w`, which is the defect to remove, not future read-only coverage.

For the caller mutation use a fresh fixture and pass `delete-caller`. Expected exit 1 names
`('go', ['vet', './...'], 'linux', [])` even though the recipe itself prints a clean success.
The deletion is only in the copied synthetic recipe. Restore the real recipe for a new control;
that control passes the six coverage assertions but remains red at the prose assertion. There is
no post-implementation green, and neither fake version replies nor fake `{}` metadata prove a
supported production context.

**Before promotion:** replace fake metadata with the closed resolver/analyzer fixture protocol,
assert automatic success/origin/latest records and immutable success on red, add the history,
sourceful/unknown/dirty/mode/context/observer cases above, and prove every actual caller/dependency
deletion turns the test red. Unexpected argv exits 97; injected leaf failure exits 23. Independently
assert strict changed-doc checks and parsed index, read-only formatting, unchanged tracked/index/
ref/untracked bytes, selected/excluded coverage and absence of runtime/install/publication calls.
The proposed test invocation in `lint-ci` must itself be pinned, so deleting the test caller fails.

## Future build slices and reporting fence

After promotion only: baseline/full read-only front door first; proven prose shortcut second;
package-impact selection and exact unrelated-result reuse stay separate. Promotion requires
closed dependencies/interfaces, complete reproducible failing tests and commands, a source-reviewed
file map, bounded diagnostic/prototype positives and commit-sized tasks with gates/fences.
After implementation, acceptance additionally requires restored independent production greens,
including the real helper/dependency/test-caller deletion controls. Prototype positives do not
satisfy that acceptance or claim landed behavior. Preserve full CI/local source landing,
changed-path integration, nested fresh-binary verification and rootless/native carve-outs.

Roadmap links already route these documents; no ordering change is needed. This preparation does
not copy the older roadmap or speed plan back into the current primary. No projected speedup or
unrun source/runtime coverage is claimed.

### Diagnostic test listing

```python
#!/usr/bin/env python3
"""Offline diagnostic only: real Justfile/just, fake quality tools, synthetic Git history.
No production helper is implemented or simulated. Intended future contract test.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

SOURCE = Path(sys.argv[1]).resolve()
ROOT = Path(tempfile.mkdtemp(prefix='completion-fixture-')).resolve()
print('diagnostic artifacts:', ROOT, flush=True)
CASE = ROOT / 'real-done-case'
assert not CASE.exists(), 'preserve the previous diagnostic; use a fresh directory'
CASE.mkdir()
BIN = ROOT / 'fake-bin'
BIN.mkdir(exist_ok=True)
LOG = ROOT / 'tool-argv.jsonl'
REAL_PYTHON = sys.executable
JUST = shutil.which('just')
assert JUST and REAL_PYTHON

FAKE = r'''import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
if name == 'sh' and len(args) == 2 and args[0] == '-cu':
    os.execv('/bin/sh', ['/bin/sh', *args])
if name == 'python3' and args and args[0] == 'scripts/completion-check.py':
    os.execv(os.environ['REAL_PYTHON'], [os.environ['REAL_PYTHON'], *args])
allowed = {
 'go': [['version'], ['env', '-json'], ['vet', './...'], ['test', '-short', './...'], ['run', './tools/pack-binaries', 'check']],
 'staticcheck': [['-version'], ['./...'], ['-checks=inherit,-SA4023', './...']],
 'sh': [['scripts/test-changelog-section.sh']],
 'python3': [[x] for x in ['scripts/test-check-userguide-closed-tree.py', 'scripts/test-check-site-output-dir.py', 'scripts/check-site-output-dir.py', 'scripts/test-site-base-href.py', 'scripts/test-vantage-check.py', 'scripts/test-completion-check.py']] + [['scripts/check-userguide-closed-tree.py', 'userguide']],
}
valid = (name == 'gofmt' and args and args[0] in ['-w', '-l']) or (name == 'uvx' and args and args[0] == 'vantage-check@latest') or args in allowed.get(name, [])
with open(os.environ['FAKE_LOG'], 'a') as f:
    f.write(json.dumps({'tool': name, 'argv': args, 'GOOS': os.environ.get('GOOS'), 'valid': valid}) + '\n')
if not valid:
    print('unexpected fake argv', name, args, file=sys.stderr); sys.exit(97)
if name == 'go' and args == ['version']: print('go version go1.26.7 linux/amd64')
if name == 'go' and args == ['env', '-json']: print('{}')
if name == 'staticcheck' and args == ['-version']: print('staticcheck 2026.1')
if name == 'uvx' and 'index' in args:
    print(json.dumps({'sections': {}, 'roadmap': {}}))
fail = os.environ.get('FAIL_TOOL')
if fail and fail == name: sys.exit(23)
'''
for name in ['go', 'gofmt', 'staticcheck', 'uvx', 'python3', 'sh']:
    p = BIN / name
    p.write_text('#!' + REAL_PYTHON + '\n' + FAKE)
    p.chmod(0o755)
shutil.copyfile(SOURCE / 'Justfile', CASE / 'Justfile')
if len(sys.argv) > 2 and sys.argv[2] == 'delete-caller':
    recipe = CASE / 'Justfile'
    text = recipe.read_text()
    assert text.count('done: check\n') == 1
    recipe.write_text(text.replace('done: check\n', 'done:\n', 1))
(CASE / 'scripts').mkdir()
shutil.copyfile(SOURCE / 'scripts/vantage-check.sh', CASE / 'scripts/vantage-check.sh')
(CASE / 'scripts/vantage-check.sh').chmod(0o755)
# Copy an authorized future helper only if present. Never replace a missing caller/helper.
if (SOURCE / 'scripts/completion-check.py').exists():
    shutil.copyfile(SOURCE / 'scripts/completion-check.py', CASE / 'scripts/completion-check.py')
p = CASE / 'docs/plans/test-suite-speed.md'
p.parent.mkdir(parents=True)
p.write_text('# Speed\n\nOrdinary prose.\n')
(CASE / 'probe.go').write_text('package probe\n')
ENV = dict(os.environ, PATH=str(BIN) + os.pathsep + os.environ['PATH'],
           REAL_PYTHON=REAL_PYTHON, FAKE_LOG=str(LOG), GIT_CONFIG_NOSYSTEM='1',
           GIT_CONFIG_GLOBAL='/dev/null')

def git(*args):
    # Every history mutation first verifies fixture ownership, not a worktree pointer.
    if args[0] != 'init':
        assert (CASE / '.git').is_dir() and not (CASE / '.git').is_symlink()
        root = subprocess.check_output(['git', '-C', str(CASE), 'rev-parse', '--show-toplevel'], env=ENV, text=True).strip()
        assert Path(root).resolve() == CASE.resolve()
    return subprocess.check_output(['git', '-C', str(CASE), *args], env=ENV, stderr=subprocess.STDOUT, text=True).strip()

git('init', '--quiet')
git('config', 'user.name', 'Completion Fixture')
git('config', 'user.email', 'fixture@example.invalid')
git('add', '.')
git('commit', '--quiet', '-m', 'synthetic initial inputs')

def done(label):
    LOG.write_text('')
    result = subprocess.run([JUST, '--justfile', str(CASE / 'Justfile'), 'done'], cwd=CASE, env=ENV, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (ROOT / (label + '.log')).write_text(result.stdout)
    events = [json.loads(x) for x in LOG.read_text().splitlines()]
    (ROOT / (label + '-argv.json')).write_text(json.dumps(events, indent=2) + '\n')
    assert result.returncode == 0, result.stdout
    assert all(x['valid'] for x in events), events
    return events

first = done('first-full')
required = [('go', ['vet', './...'], 'linux'), ('go', ['vet', './...'], 'darwin'),
 ('staticcheck', ['./...'], 'linux'), ('staticcheck', ['-checks=inherit,-SA4023', './...'], 'darwin'),
 ('go', ['test', '-short', './...'], None), ('go', ['run', './tools/pack-binaries', 'check'], None)]
for name, args, goos in required:
    assert any(x['tool'] == name and x['argv'] == args and x['GOOS'] == goos for x in first), (name, args, goos, first)
p.write_text('# Speed\n\nRevised ordinary prose.\n')
git('add', '.')
git('commit', '--quiet', '-m', 'synthetic listed prose modification')
prose = done('listed-prose')
forbidden = [x for x in prose if x['tool'] in ['go', 'gofmt', 'staticcheck']]
assert not forbidden, 'listed prose invoked forbidden quality tools: ' + json.dumps(forbidden)
assert any(x['tool'] == 'uvx' and '--strict' in x['argv'] for x in prose), prose
assert any(x['tool'] == 'uvx' and 'index' in x['argv'] and '--format' in x['argv'] for x in prose), prose
print('real done full control and prose contract passed')
```
