---
status: current
stage: CURRENT
verified: 2026-10-26
verified_commit: 6365c0d9
covers:
  - scripts/completion-check.py
  - scripts/test-completion-check.py
  - scripts/completion-census.py
  - scripts/completion-readers.json
  - scripts/go_packages.py
  - scripts/test-go-packages.py
  - scripts/vantage-check.sh
tags: [testing, tooling, completion]
summary: "How `just done` checks a finished task without rewriting it: it refuses a dirty tree, finds the nearest verified ancestor shared by every worktree, and runs only what the change since then can reach — lint for changed Go packages and their importers, short tests for the changed packages, the tests recorded as reading a changed file, and the document checks — or the full `just check-ci` when anything is unproven. The landing gate is unchanged."
---

# Change-aware completion — what `just done` checks, and why it never rewrites

**Status:** Verified 2026-10-26 against `6365c0d9`. Graduated from the design doc
`docs/reference/change-aware-completion.md`, which git history keeps. MEASURED: every route on a
synthetic repository (`scripts/test-completion-check.py`, run by `lint-ci`), and the wall times in
[Measured on 2026-10-09](#measured-on-2026-10-09).

`just done` is the command an agent runs at the end of every task. It runs
[`scripts/completion-check.py`](../../scripts/completion-check.py), which verifies the work since the
last verified commit and says what it covered. It is **read-only**: it never formats, stages,
commits, launches a jail or publishes anything; tools may use their ordinary caches, and the
command writes only its own records under Git's administrative directories. It is **not the landing
gate**: landing still runs `just check-ci`, the integration tests the change reaches and, for a
launch-path change, the full integration suite ([AGENTS.md, Workflow](../../AGENTS.md#workflow)).

| Component | Lives in |
| :--- | :--- |
| The command `just done` runs | `scripts/completion-check.py` (`Store`, `GLOBAL_FILES`, `GLOBAL_PREFIXES`, `GO_ENV_KEYS`, `LINT_PASSES`) |
| Its route tests, on a synthetic repository | `scripts/test-completion-check.py` |
| Shared Git-aware package enumeration | `scripts/go_packages.py` (recipes, completion graph and census) |
| Package-selection regression tests | `scripts/test-go-packages.py` |
| The reader census, and its output | `scripts/completion-census.py` → `scripts/completion-readers.json` |
| The pinned Markdown checker | `scripts/vantage-check.sh` |
| The recipe | `done` in the `Justfile` |

**Reads with:** [suite speed](../plans/test-suite-speed.md) (independent timing work, whose own
questions stay in that source).

---

## Principles

1. **P1 — The task range counts.** Earlier committed work cannot disappear behind the final commit
   or a clean status.
2. **P2 — Unknown means full.** A route is a proof of irrelevance, not a guess from a filename
   extension.
3. **P3 — Cleanliness is separate.** Green checks cannot excuse staged, unstaged or untracked work.
4. **P4 — Completion never repairs silently.** Formatting or repair happens explicitly, before a new
   clean verification.
5. **P5 — Coverage is named.** Every selected, excluded and inherited check has a reason in the
   report.
6. **P6 — Failure retains evidence.** A failed attempt does not advance the baseline or erase its
   diagnostic log.

A success says **completion coverage satisfied**, never "all runtime checks passed": the report
names what ran, what was inherited from the baseline, and what `just done` never covers
(integration, a nested jail, native macOS).

## How a run proceeds

1. **Refuse a dirty tree** before any check: staged, unstaged and untracked paths are listed, with
   the next step (commit; `just format` first if gofmt is why). It never formats or stages, which is
   why `done` no longer depends on `check`, whose `gofmt -w` could rewrite the work it was
   verifying.
2. **Find the baseline.** A **baseline** is the nearest ancestor of HEAD with a verification record
   whose tree and **context** match. The context is the effective `go env` values and the
   staticcheck binary's resolved path and version ([CAC-D11](#CAC-D11)). Records live in the
   repository's common Git directory, keyed by commit, so every worktree shares them
   ([CAC-D8](#CAC-D8)). No baseline runs the full gate.
3. **Collect the change**: `git diff --raw --no-renames` from the baseline's tree to HEAD's, so a
   rename is a deletion plus an addition ([CAC-D9](#CAC-D9)). A HEAD whose tree equals a verified
   tree under the same context runs nothing ([CAC-D13](#CAC-D13)).
4. **Plan.** Any of these runs the full `just check-ci`, plus the Markdown check on changed
   documents: a gate or whole-tree input (`GLOBAL_FILES` and `GLOBAL_PREFIXES`: `go.mod`,
   `go.sum`, `vendor/`, the `Justfile`, `mise` files, the flake, `.github/`, `scripts/`, release and
   site config), a non-regular file, a Go file in no package, an error from `go list`, an unreadable
   readers file, or a path no check and no recorded reader covers. The one exemption is
   `scripts/completion-readers.json` itself ([CAC-D17](#CAC-D17)). Otherwise only:
   - `go vet` and staticcheck, for both GOOS values, on the changed packages and every package
     whose build or tests import them (each GOOS's own import graph), and `go test -short` on the
     changed packages alone ([CAC-D18](#CAC-D18)); package directories come from Git-tracked Go
     files (even if ignored) and non-ignored untracked Go files, with Go applying build
     constraints. Ignored files still participate inside a selected real package, but an
     ignored-only package does not select a directory;
   - the individual tests recorded as **reading** a changed path, or listing a directory that
     gained or lost a direct entry ([CAC-D10](#CAC-D10)), and on every change every **scanning**
     test ([CAC-D15](#CAC-D15));
   - `gofmt -l` on the changed Go files;
   - for documents: the [Vantage wrapper](../../scripts/vantage-check.sh), at its latest release, on
     changed Markdown ([CAC-D14](#CAC-D14), [CAC-D16](#CAC-D16)); the guide's closed-tree and
     whole-guide checks when `userguide/` changed; and the changelog extraction tests when
     `CHANGELOG.md` changed.
5. **Run** the selected checks in parallel, keep the full output in the worktree's own Git directory
   (`yolo-completion/last.log`), and on any failure record nothing.
6. **Re-check** that HEAD is unchanged and the tree still clean ([CAC-D12](#CAC-D12)), then record
   the verification atomically.

A per-worktree lock refuses a second simultaneous `just done` in the same worktree; it serializes
record writers, not editors.

## The reader census

Imports do not explain a test that reads a file: a citation test reads documents, a golden test
reads fixtures, and no import graph shows either. So the reader evidence is
[`scripts/completion-readers.json`](../../scripts/completion-readers.json), written by
[`scripts/completion-census.py`](../../scripts/completion-census.py). The Justfile's Linux/Darwin
lint and Go test recipes, the selector's Go graph and the census use `scripts/go_packages.py` to
start from Git-visible Go source directories, so an ignored extra package cannot introduce an
unrelated missing dependency into the gate. This is a directory-selection boundary, not a file
filter: Go still compiles ignored extra Go files that share a selected package directory. The
census runs every test once, alone, with Go's `-test.testlogfile` — the log `go test` itself uses
to invalidate its cache — and keeps each test's reads of files that belong to no package or to
another package. Git-aware package selection is used in a checkout; the Go-only lint and test recipes
retain standard `./...` behavior in a source archive. Change-aware completion and the reader census
still require Git metadata for their change and read selection.

A **scanning test** *(a term this design coined)* is one whose recorded reads include a whole-kind
pattern (`D/*`, `D/*.E`) or whose listings include a whole tree (`D/**`). What it reads moves with
ordinary edits, so scanning tests run on every selective check, and Go's own test cache decides
whether they need to execute.

The census is selection data the run reads fresh from the working tree, not a gate input, so a range
whose only global-matching path is the census routes selective: refreshing it selects checks
instead of forcing the whole gate.

> [!IMPORTANT]
> **Rerun `python3 scripts/completion-census.py` and commit its output before landing a change to a
> test that reads files outside its package.** A test that reads a fixed list of named files is
> selected from the recorded list, so a change to that list is missed until the census is rerun.
> `scripts/test-completion-check.py` checks that every package and test the file names still exists.

## Known limits

- **Reads by a test's subprocesses** (git, `go build`) are not in Go's test log; Go's own test cache
  has the same blind spot.
- **The readers file goes stale with ordinary edits.** A test added in a commit runs anyway, its
  package having changed; but what an existing test reads can move without its package changing.
  Scanning tests cover the common case; [the census rule](#the-reader-census) covers the rest, and
  `just check-ci` at landing and CI catch what it misses.
- **Ambient inputs** — files under HOME, environment variables other than the recorded `go env`
  values — are not part of the context. Landing and CI run the whole gate.
- **A write that is undone before the checks finish** is not detected ([CAC-D12](#CAC-D12)). The
  lock fences only other `just done` runs, never an editor, an agent or a Git command.
- **Importers' tests are not run locally.** A change to a core package lints its whole importer
  closure but tests only itself; CI runs the closure's tests on every push ([CAC-D18](#CAC-D18)).

## Measured on 2026-10-09

One machine (32 CPUs), the shared Go build cache warm from earlier runs. These are the numbers
[CAC-D18](#CAC-D18) and [CAC-D10](#CAC-D10) rest on.

| Change since the baseline | What ran | Wall time |
| :--- | :--- | :--- |
| None: first run in this worktree | Full `just check-ci` | 229 s |
| One line of `docs/reference/jail-home.md` | Its Markdown check, and the one citation test that reads it | 4.4 s |
| The same line, in a new checkout path holding only the shared record | The same two checks | 4.0 s |
| A comment in `tools/build-wheels`, a package nothing imports | Lint and tests for that package, one reader test, gofmt, pins | 1.1 s |
| One line of `internal/cli` code | Lint and tests for `internal/cli` and `cmd/yolo`, 14 reader tests | 211 s, 210 s of it `internal/cli`'s own suite |

The old recipe (`check`, then the tree check) took 241 s in a new checkout path. A single comment
in one importable package used to select a third to a half of the module's packages for testing,
because Go's test cache invalidates for every package a change reaches. `internal/cli` and
`internal/cli/run` import nearly every package, so most Go changes still pay to lint those two; that
remaining cost is the [suite-speed](../plans/test-suite-speed.md) work, not something selection can
remove.

## What this does not license

- No pre-commit hook or slow check on every commit; [the landing/commit
  separation](../../AGENTS.md#workflow) holds.
- No replacement of native execution with cross-GOOS lint or Linux integration.
- No weakening of [the rootless networking and ID-mapping carve-outs](../../AGENTS.md#testing).
- No dependency on a remote CI service, a network-aware selector or publication credentials.
- No repository snapshots, per-file hash manifests, approval of bytes or signed receipts.
- No suite-wide scheduling limit; [the suite-speed ledger](../plans/test-suite-speed.md#decision-ledger)
  remains authoritative.

> [!WARNING]
> **Do not take the baseline from the working diff or from HEAD's parent.** Both miss work already
> committed earlier in the task (P1). And **do not run the mutating `check` at completion**: it can
> rewrite committed work while verifying it (P4).

## Why it's this way

Rulings and decisions a maintainer would otherwise undo. Each is reversible unless it says
otherwise. An earlier, narrower first slice — a five-path prose allowlist, an inert-text proof of
preserved anchors, a file-event observer and caller certification — was proposed and superseded by
the rows below; git history keeps it.

| ID | Ruling |
| :--- | :--- |
| <a id="CAC-D7"></a>CAC-D7 | **Scope, confirmed by the owner 2026-10-09 ("yes to D7"):** *"we just need only the right tests to run. We want this to be as fast as possible. It gets run a lot. And we want it to be intelligent about what's run."* `just done` selects Go checks by package impact and documents by recorded reader. Landing (`just check-ci`), CI and the integration and nested-jail obligations are unchanged |
| <a id="CAC-D8"></a>CAC-D8 | **Verification records live in the common Git directory** (`yolo-completion/verified/<commit>.json`), keyed by commit and checked against that commit's tree and the current context, so every worktree shares them; the lock and log stay in each worktree's own Git directory. Agents start each task in a new worktree, so per-worktree records would make every first `just done` a full run, and a record says one tree passed under one context, which no worktree path changes |
| <a id="CAC-D9"></a>CAC-D9 | **The change is the baseline-tree-to-HEAD-tree diff**, not the union of every intervening commit's edges. The check verifies HEAD's tree; a change made and reverted inside the range leaves nothing for any check to see, and every path that does differ is in the net diff |
| <a id="CAC-D10"></a>CAC-D10 | **Which tests read which non-imported files comes from a per-test census of Go's own test log.** It is the evidence `go test` itself uses to invalidate cached results, it covers arbitrary file reads that no import graph shows, and it is per test, so a document change runs one citation test, not a package's whole suite |
| <a id="CAC-D11"></a>CAC-D11 | **The context is the effective `go env` values and staticcheck's resolved path and version**, read by running `go env` and `staticcheck -version`. Both take milliseconds and run no quality check |
| <a id="CAC-D12"></a>CAC-D12 | **Stability is a before-and-after check (clean tree, same HEAD) plus a per-worktree lock**, with no file-event observer. The observer could not fence a writer either, and was Linux-only. The cost, stated plainly: a write undone before the checks finish goes unseen; landing and CI rerun everything |
| <a id="CAC-D13"></a>CAC-D13 | **A HEAD whose tree equals a verified tree under the same context runs no check.** That record is an actual green for exactly these inputs |
| <a id="CAC-D14"></a>CAC-D14 | **Every changed Markdown file outside a Go package directory gets the Vantage check**, `docs/` included, which `check-ci` does not check. A check that skipped known-bad files would hide new findings in them |
| <a id="CAC-D15"></a>CAC-D15 | **Every scanning test runs on every selective check.** A recorded set went stale between census runs: deleting a cited document selected none of the citation test, and `just done` would have recorded a tree `check-ci` rejects. Go's test cache skips them when nothing they read changed |
| <a id="CAC-D16"></a>CAC-D16 | **The Vantage wrapper runs `vantage-check@latest`, with no pin.** The owner, 2026-11-10: *"remove the vantage check pin entirely"*. It reverses the earlier pin, which held the gate on 0.10.0 after 0.11.0 dropped a warning this project ignores. The cost is accepted: a new upstream release can turn a document nobody changed red, and `just done` then reports it (`test_the_markdown_checker_runs_its_latest_release`) |
| <a id="CAC-D17"></a>CAC-D17 | **`scripts/completion-readers.json` is exempt from the global-input rule.** It is reader-selection data read fresh, not a gate input; treating it as global forced a nine-minute full gate on every census refresh. Every other `scripts/` path and every genuine global input still runs the full gate |
| <a id="CAC-D18"></a>CAC-D18 | **The local route tests the changed packages and leaves the importer closure's tests to CI**, authorized by the owner 2026-10-12. Lint still covers the closure. Go's test cache invalidates for every package a change reaches, so running the closure's suite made `just done` cost what the gate it narrows costs |

## Current values

Verified at `8e65f344`. The prose above explains what each of these is for; this table is the only
place the exact values are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Shared verification records | `<git-common-dir>/yolo-completion/verified/<commit>.json` | `Store` in `scripts/completion-check.py` |
| Per-worktree lock and last log | `<git-dir>/yolo-completion/lock`, `…/last.log` | `scripts/completion-check.py` |
| Record schema version | 1 | `SCHEMA` |
| Context keys | `GOVERSION`, `GOOS`, `GOARCH`, `GOROOT`, `GOFLAGS`, `CGO_ENABLED`, `GOEXPERIMENT`, `GOAMD64`, `GOARM64`, `GOTOOLCHAIN`, `GOWORK`, `CC`, plus staticcheck's path and version | `GO_ENV_KEYS` |
| Lint passes | `go vet` and staticcheck for linux and darwin; darwin staticcheck excludes SA4023 | `LINT_PASSES` |
| Global inputs | `GLOBAL_FILES`, and the prefixes `vendor/`, `.github/`, `scripts/` | `scripts/completion-check.py` |
| Vantage release | `vantage-check@latest`, unpinned | `scripts/vantage-check.sh` |
