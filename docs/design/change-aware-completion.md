---
title: "Completion should check the task, without rewriting it"
date: 2026-10-09
status: accepted
stage: BUILT
next: "Keep scripts/completion-readers.json current"
tags: [testing, tooling, design]
summary: "Read-only, change-aware `just done`: an automatic verified baseline shared by worktrees, package-impact and recorded-reader test selection, and the full check-ci when anything is unproven; landing policy unchanged."
---

# Completion should check the task, without rewriting it

**Status:** 2026-10-09 — built. [`scripts/completion-check.py`](../../scripts/completion-check.py)
is `just done`; [What shipped](#what-shipped) describes it, and the
[ledger](#decision-ledger) records where it departs from the sections below, which are kept as the
reasoning that led there. Sections 1 to 8 describe the narrower first slice proposed before the
owner's 2026-10-09 request; where they disagree with [What shipped](#what-shipped), it wins.

The earlier bounded experiment stays in
[`tools/completion-experiment/`](../../tools/completion-experiment/README.md), unconnected.

> **In short.** Completion should verify the work since a successful baseline and explain its coverage,
> not format committed code or run unrelated Go checks for proven ordinary prose.

**Why it matters.** Suite cost and unnecessary repetition are different problems.
**The shape.** One completion command collects changes, selects checks and records successful coverage.
**Cost.** A small exact allowlist buys a deliberately narrow shortcut; uncertain inputs still pay for the full gate.
**Start at [the baseline](#3-the-baseline-is-automatic-and-covers-earlier-commits)** — empty working diffs are not evidence.
**Ruled:** the owner confirmed [CAC-D7](#CAC-D7) on 2026-10-09: `just done` selects Go checks by package impact.
Landing (`just check-ci`) is unchanged.
**Reads with:** [input research](../research/completion-check-inputs.md) (source evidence),
[the companion sketch](change-aware-completion-plan.md) (not a build hand-off), and
[suite speed](../plans/test-suite-speed.md) (independent timing work).

---

## What shipped

The owner asked on 2026-10-09 for `just done` to run "only the right tests", as fast as possible,
because agents run it after every task ([CAC-D7](#CAC-D7)). Measured on this machine before the
change: the old recipe ran the whole short suite, and a fresh worktree paid it cold (about 158 s
of tests and 32 s of lint); a single package, `internal/cli`, took about 130 s of that.

`just done` now runs [`scripts/completion-check.py`](../../scripts/completion-check.py):

1. **Refuse a dirty tree** before any check: staged, unstaged and untracked paths are listed, with
   the next step (commit; `just format` first if gofmt is why). It never formats or stages.
2. **Find the baseline**: the nearest ancestor of HEAD with a verification record whose tree and
   context (the effective `go env` values and the staticcheck binary and version) match. Records
   live in the repository's common Git directory, keyed by commit, so every worktree shares them
   ([CAC-D8](#CAC-D8)). No baseline runs the full gate.
3. **Collect the change**: `git diff --raw --no-renames` from the baseline's tree to HEAD's, so a
   rename is a deletion plus an addition ([CAC-D9](#CAC-D9)). HEAD's tree equal to a verified tree
   runs nothing ([CAC-D13](#CAC-D13)).
4. **Plan**. Any of these runs the full `just check-ci`, plus the Markdown check on changed
   documents: a gate or whole-tree input (`go.mod`, `go.sum`, `vendor/`, the `Justfile`, `mise`
   files, the flake, `.github/`, `scripts/`, release and site config), a non-regular file, a Go
   file in no package, an error from `go list`, an unreadable readers file, or a path no check and
   no recorded reader covers. Otherwise only:
   - `go vet` and staticcheck for both GOOS values, and `go test -short`, on the changed packages
     and every package whose build or tests import them (each GOOS's own graph for lint);
   - the individual tests recorded as **reading** a changed path, or listing a directory that
     gained or lost an entry ([CAC-D10](#CAC-D10)), and on every change every **scanning** test,
     one that reads all files of a kind in a directory or lists a whole tree
     ([CAC-D15](#CAC-D15));
   - `gofmt -l` on the changed Go files, and the official-binary pin check when Go or pack sources
     moved;
   - for documents: the [Vantage wrapper](../../scripts/vantage-check.sh), at a pinned release, on
     changed Markdown ([CAC-D14](#CAC-D14), [CAC-D16](#CAC-D16)), the guide's closed-tree and whole-guide checks when `userguide/`
     changed, and the changelog extraction tests when `CHANGELOG.md` changed.
5. **Run** the selected checks in parallel, keep the full output in the worktree's own Git
   directory (`yolo-completion/last.log`), and on any failure record nothing.
6. **Re-check** that HEAD is unchanged and the tree still clean ([CAC-D12](#CAC-D12)), then record
   the verification atomically. The report names what ran, what was inherited from the baseline,
   and what `just done` never covers (integration, nested jail, native macOS).

The reader evidence is [`scripts/completion-readers.json`](../../scripts/completion-readers.json),
written by [`scripts/completion-census.py`](../../scripts/completion-census.py): it runs every test
once, alone, with Go's `-test.testlogfile` — the log `go test` itself uses to invalidate its cache —
and keeps each test's reads of files that belong to no package or to another package. On
2026-10-09 it took 145 to 200 s for 12,403 tests and found 578 reading tests in 34 packages. Changing the file is a `scripts/`
change, so the commit that refreshes it runs the full gate.
[`scripts/test-completion-check.py`](../../scripts/test-completion-check.py), run by `lint-ci`,
covers the routes on a synthetic repository and pins both call sites.

### Measured on 2026-10-09

One machine (32 CPUs), the shared Go build cache warm from earlier runs:

| Change since the baseline | What ran | Wall time |
| :--- | :--- | :--- |
| None: first run in this worktree | Full `just check-ci` | 229 s |
| One line of `docs/reference/jail-home.md` | Its Markdown check, and the one citation test that reads it | 4.4 s |
| The same line, in a new checkout path holding only the shared record | The same two checks | 4.0 s |
| A comment in `tools/build-wheels`, a package nothing imports | Lint and tests for that package, one reader test, gofmt, pins | 1.1 s |
| One line of `internal/cli` code | Lint and tests for `internal/cli` and `cmd/yolo`, 14 reader tests | 211 s, 210 s of it `internal/cli`'s own suite |

The old recipe (`check`, then the tree check), on the commit before this change: 241 s in a new
checkout path, and 21.2 s after the same one-line document commit in a checkout whose test cache
was warm.

`internal/cli` and `internal/cli/run` import nearly every package in the module, so most Go changes
still pay for those two suites. That cost is the [suite-speed](../plans/test-suite-speed.md) work,
not something selection can remove.

### Known limits

- **Reads by a test's subprocesses** (git, `go build`) are not in Go's test log; Go's own test
  cache has the same blind spot.
- **The readers file goes stale with ordinary edits.** A test added in a commit runs there anyway,
  its package having changed. But what an existing test reads can move without its package
  changing: a citation added to Go source makes the citation test read one more document. Scanning
  tests are therefore run on every change ([CAC-D15](#CAC-D15)), leaving Go's own test cache to
  decide whether they need to execute. A test that reads a fixed list of named files is still
  selected from the recorded list, so a change to that list is missed until the census is rerun.
  Rerun it before landing any change to a test that reads outside its package; `just check-ci` at
  landing and CI catch what it misses.
- **Ambient inputs** — files under HOME, environment variables other than the recorded `go env`
  values — are not part of the context. Landing and CI run the whole gate.
- **A write that is undone before the checks finish** is not detected ([CAC-D12](#CAC-D12)).

## 1. Scope: completion is not a new landing policy

My recommendation is a small, read-only front door for `just done`, initially optimizing only
proven ordinary documentation changes. **Read-only** means it does not rewrite tracked files,
the index, refs or untracked task files. Tools may use their ordinary caches and temporary outputs;
the command may write its own verification record under the worktree's Git administrative directory.
It never stages, commits, formats, launches a jail or publishes anything. The existing full gate's
provisioning policy remains: a first full may resolve its checker or fetch the pinned Go toolchain
and capture effective inputs automatically. Missing verification tools name their existing setup
step; no manual preinstallation or no-bootstrap certificate establishes a baseline.

The first slice includes automatic baseline discovery, exact prose classification, cleanliness
before and after verification, explainable routing, and failure-safe baseline advancement.
It excludes package-selective source checks and reuse of unrelated old command output.

Existing obligations remain distinct:

| Obligation | First-slice disposition |
| :--- | :--- |
| Interactive `check` | Still explicitly mutating; a developer chooses it before committing |
| Completion for uncertain/source inputs | Read-only full `check-ci`, plus applicable changed-document checks |
| Local mandatory source landing | Whole `check-ci` unchanged; no selective source exemption |
| Changed-path real integration and nested fresh-binary verification | Unchanged; not performed by completion |
| Launch-path full integration once on combined landing tree | Unchanged; not replaced by short units |
| Whole-tree CI quality, native macOS and Linux architecture jobs | Unchanged |

The current [recipe graph](../../Justfile#L409-L498) makes `done` depend on `check`, which formats,
lints Linux and Darwin, runs all short units and checks official binary pins. It does **not** run
full integration. `check-ci` adds read-only formatting and changelog/guide/site-contract checks;
it does not build a rendered site. [The research](../research/completion-check-inputs.md#the-recipe-names-describe-different-coverage)
records those differences. The full fallback intentionally uses the existing stronger read-only gate.

### What this does not license

- No pre-commit hook or slow check on every commit; [the landing/commit separation](../../AGENTS.md#workflow) holds.
- No replacement of native execution with cross-GOOS lint or Linux integration.
- No weakening of [rootless networking and ID-mapping carve-outs](../../AGENTS.md#testing).
- No dependency on a remote CI service, network-aware selector or publication credentials.
- No repository snapshots, per-file hash manifests, approval of bytes or signed-receipt system.
- No suite-wide scheduling limit; [the suite-speed ledger](../plans/test-suite-speed.md#decision-ledger) remains authoritative.

## 2. Principles and observable outcomes

1. **P1 — The task range counts.** Earlier committed work cannot disappear behind the final commit or a clean status.
2. **P2 — Unknown means full.** A route is a proof of irrelevance, not a guess based on filename extension.
3. **P3 — Cleanliness is separate.** Green checks cannot excuse staged, unstaged or relevant untracked work.
4. **P4 — Completion never repairs silently.** Formatting or repair happens explicitly, before a new clean verification.
5. **P5 — Coverage is named.** Every selected, excluded and inherited check has a reason in the report.
6. **P6 — Failure retains evidence.** A failed attempt does not advance the baseline or erase its diagnostic log.

A successful completion reports the baseline and current Git identities, complete changed-path
set, chosen checks, command outcomes, exclusions and before/after cleanliness. It says **completion
coverage satisfied**, not **all runtime checks passed**. Landing and native/runtime evidence are
reported separately, with unrun and skipped coverage explicit.

No successful baseline exists today for this proposed mechanism. A new checkout starts on the
full route automatically; no operator must remember to mark a task's starting point.

## 3. The baseline is automatic and covers earlier commits

A **baseline** here is the Git commit/tree covered by this completion command's last successful
clean verification in this worktree, together with the checks that actually covered it. It is not
HEAD's parent, the last commit of a task, an arbitrary user-supplied commit or a printed PASS line.

### Automatic establishment and advancement

1. On every `just done`, resolve the current worktree and its own Git administrative directory.
2. Read the command's own success and latest-attempt records. Missing, malformed, unsupported or
   unverifiable records select the full route; do not search logs for an older green sentence.
3. A first clean full success records HEAD, its tree, full completion coverage and the relevant
   verification context. It requires no manual task-start flag or baseline import.
4. A later ordinary-prose success advances to the new HEAD only after all selected checks pass
   and unchanged source/gate inputs justify retaining the prior full coverage.
5. A full success replaces the full-coverage reference. Any failure leaves the last success alone.
   A subsequent attempt must inspect the separate failed-attempt record and reverify; a failure
   at the same tree is never excused by a success that predates it.

The record carries a schema version, producer version, commit/tree identities, verification
context, named coverage with its originating successful full run, actual outcomes and diagnostic
log locations. Validate object/field types and require the complete full-origin command outcomes;
nonobject JSON or missing outcomes selects full, never a crash or vacuous pass. It stores no secrets
or repository copy. Only this command writes the successful
pointer; replacement is atomic after final validation. Existing ad hoc logs are ignored.

A per-worktree completion lock refuses a second simultaneous completion with the next action:
wait for the first attempt to finish, then retry. It serializes record writers, not every editor.
The one-writer limitation is stated in [the stability rule](#6-cleanliness-and-a-stable-verification-interval).

### Complete change collection

A valid baseline must exist locally and be an ancestor of current HEAD. Collect the union of:

- changes from the baseline tree to HEAD;
- each intervening commit's changes against **each parent**, including merge parents;
- staged differences against HEAD, unstaged differences against the index and non-ignored untracked paths;
- both old and new names for renames/copies, deleted paths, file-type and executable-mode changes.

Use Git's NUL-delimited output, not whitespace splitting. Include modified-then-reverted source
from an intervening commit: a net docs-only diff must not hide source work from the task range.
Do not infer a rename solely by its new suffix. Ambiguous history, shallow/unavailable parent
objects, unmerged entries, path decoding or Git errors select full or refuse if the tree cannot
be read at all. A deleted package cannot be classified only from files that still exist.

An empty collected range **does not select a no-check route** in this slice. It selects full.
Exact same-input result reuse is later work. This also keeps the ordinary bare `just done` safe
when nothing is staged and the user already committed everything.

### Context cannot be silently inherited

The successful record identifies the actual tool releases, host OS/architecture, recipe/selector
version, flags and effective non-secret verification environment used by its full run. A changed context for inherited outcomes or inability to establish it selects full. Fresh documentation
outcomes are never inherited: [the latest checker](../../scripts/vantage-check.sh) and its effective
inputs are captured for current verification, not required to match the old Go baseline unless an
actual additional reader makes them Go inputs. Secrets are never logged; an input whose
relevance cannot be established without storing secret material makes inheritance unavailable.

Context identification has two phases. The full route may query Go while actually running the
Go gates. A later prose route compares file-resolved tool identities and effective configuration
against that record **without executing any Go, gofmt or staticcheck argv**, including version,
environment, list or help probes. A version string alone is not an identity; changed executable,
compiler/standard-library tree, configuration or PATH resolution invalidates inheritance.

The supported profile is discovered automatically, not enabled by an environment switch or a
caller certificate. Direct executables and the project's declared tool-manager layout are the
initial resolution cases to prove. Opaque shims, custom workspaces, flags, C toolchains and
unaccounted ambient readers are full-route cases, not silently normalized into another gate.
Neither a full-environment hash nor a hand-maintained list of variable names proves that external
files are unchanged. The [source preparation](change-aware-completion-plan.md#verification-context)
names the remaining reader/profile work; promotion needs an executable positive default-profile
fixture, not a design that permanently disables inheritance.

## 4. The first shortcut is an exact allowlist

**Ordinary prose** in this slice means a modified regular tracked file at one of the five exact
paths below, whose body is not executable/test/pack input and whose structural targets are preserved.
These paths are selected from the [actual reader survey](../research/completion-check-inputs.md#non-go-readers-rule-out-a-broad-prose-category),
not from a proposed all-documentation convention:

| Exact path | Why it is eligible for investigation |
| :--- | :--- |
| [The speed plan](../plans/test-suite-speed.md) | Measurement and planning prose; source citations must survive |
| [The roadmap](../plans/roadmap.md) | Priority/planning prose; source citations and planning routing must survive |
| [Completion input research](../research/completion-check-inputs.md) | Source findings, not embedded instructions |
| [This behavior design](change-aware-completion.md) | Planning behavior, not a runtime configuration |
| [The companion sketch](change-aware-completion-plan.md) | Unbuilt implementation notes, not an executable fixture |

This is a small initial set, not a claim that the rest of the corpus is sourceful. New allowlist
entries require a reader survey and regression evidence. Unlisted paths always select full.
Additions, deletions, renames, copies, mode changes, symlinks and other file types select full in
the first slice, even at listed names. Thus the initial addition of these documents is not itself
an example of the proposed shortcut.

### Anchors and incoming references are real inputs

For a listed modification, preserve target identities on **both sides of every collected history
edge**, either with the ordered renderer/source comparisons below or with a sufficient inert-text
proof. The bounded proof admits one isolated, unindented ASCII-letter sentence, outside closed
frontmatter, changed only by equal-length letter substitutions. Every other byte, including spaces,
periods and newlines, stays fixed. Documents containing raw HTML, source-ID attribute text, fences,
math blocks, tabs or CR
are excluded. This preserves heading/question/explicit-ID ownership and source/line targets without
extracting them; it is not a replacement renderer or a claim of regex equivalence.

The restriction matters: a blank-surrounded text line can still change a multiline HTML ID or lie
inside a fence/math block. Setext adjacency, heading edits, question IDs and frontmatter also
force full. [Bounded counterexamples and positives](../research/completion-check-inputs.md#bounded-positive-diagnostic)
were executed; broader ordinary prose still requires the ordered comparisons.

For that broader route, a set of slugs alone loses duplicate-heading ownership and ordering.
Any changed heading/explicit/question identity, source-citation target or unclassifiable syntax
selects full. Strict checker success is not anchor extraction: the installed checker has different
renderer and link-index pipelines and no public anchor-export command. Do not port its heading
slugger into a regex and call that renderer equivalence.

Broader analysis also compares the existing test's own target interpretation, which
is not identical to the renderer. Keeping one renderer's anchors cannot silently break the Go
citation test's distinct ATX/explicit-ID matching. The [anchor/referrer preparation](change-aware-completion-plan.md#anchors-and-incoming-references)
records both mechanisms and the required adapter boundary.

Strict rendering/link checks run on every actual changed Markdown file and affected incoming
Markdown referrers, including line anchors, definitions and images. A conservative referrer
superset is acceptable; an unreadable or incomplete graph selects full and cannot produce a
partial green. Deletion/rename/anchor changes also check surviving referrers on the full route.
Code-claim review remains source-first; the command cannot prove that a sentence accurately
describes a behavior or numbered section.

[The current source-citation test](../../internal/paths/doccitations_test.go) checks paths and
fragments from source directories and root files, with explicit fixture/history exceptions.
It runs on the full route through short units. It is not a standalone lightweight checker today.
A future non-Go citation checker must preserve those exceptions, scan incoming Go and other
source references, and turn deletion/rename/anchor defects red before widening the shortcut.

### Explicit exclusions

| Class | First-slice reason for full routing |
| :--- | :--- |
| Pack docs, briefing/skills prose and built-in agent instructions | Embedded or staged runtime content |
| Root README and guides | Wheel/install/census readers; publishing checks are also applicable |
| Example packs and fixtures/golden documentation | Direct test inputs, regardless of extension |
| Source comments, generated shell content and config/schema | Source/runtime/test contracts, even when prose-only |
| Toolchains, dependencies, recipes, workflows, flake and shipping lists | Global/cross-language verification inputs |
| Any unlisted or uncertain path | No proven reader classification |

The bounded proof avoids a new parser dependency, not the unfinished production-context audit.
It does not weaken strict changed/referrer checks or any landing/runtime obligation.

## 5. Each route prints what it does and what it excludes

| Route | Selected work | Excluded or inherited work |
| :--- | :--- | :--- |
| Ordinary listed prose | Strict changed-Markdown and incoming-link checks; planning-index review; independent clean/stability checks | Go formatting/lint/short units/pins inherit the named full baseline because their inputs/context are unchanged |
| Full fallback | Existing `check-ci`; applicable changed-Markdown/incoming checks and planning review | No source-quality omission; full integration/native/nested remain independent obligations |
| Dirty or unstable | Collect and print intended route; refuse success before executing gates if initially dirty | Nothing is covered; repair/finish the writer, then retry clean |

The report names every lint configuration and its existing Darwin SA4023 exclusion, short-unit
scope, official-pin check, formatting mode, applicable changelog/guide/site checks and documentation
checks. “Skipped” never conflates irrelevant, inherited, unavailable and not run.

For CHANGELOG changes, full routing includes the existing extraction tests and strict changed-doc
checks. Guide changes also retain closed-tree and census checks. Site-builder changes retain the
site contract/base-href tests. A site-content change still needs separately authorized rendered
export validation; completion never invokes the mutating deployment builder or claims a site build.

Missing baseline/context/classification evidence automatically schedules full verification, not
an owner pause. A tool failure preserves its exit status/log and ends the attempt without success.
Small deterministic failures go to the ordinary repair queue; repairs do not run inside completion.
A retry never drops the failing check to obtain green.

## 6. Cleanliness and a stable verification interval

Cleanliness is checked before gates and again after all selected gates, independently of their
exit codes. Record HEAD/tree/index identities and Git status with staged, unstaged, untracked,
rename/delete, unmerged and submodule state visible. Relevant ignored inputs cannot be silently
accepted: if a verifier reads an ignored file not accounted for by the context, inheritance is
unavailable. Logs/caches live outside task inputs.

Initial dirtiness refuses completion, even when it is only an untracked ordinary document.
The report still identifies those paths and the conservative route they require. The next action
is to finish/commit the intended edits and retry; if formatting is the cause, explicitly run the
existing developer formatting command before committing. Completion does not do it afterwards.

A changed HEAD/index/status or relevant input during gates invalidates the attempt. Preserve the
log, do not advance success, and retry only after the writer is finished. Check results do not
turn a concurrent-change failure into a pass.

> [!WARNING]
> Before/after equality cannot detect a writer temporarily changing a file and restoring it.
> The completion lock serializes completion records only. No caller assertion or orchestration
> token turns that advisory lock into a fence against editors, agents or Git commands.

Ordinary standalone `just done` needs no caller certification. Initial discovery is provisional:
discard its identities and decisions. Register tracked/Git/external directory/inode watches and
missing-input parents, then re-resolve/read the complete set under the established barrier. Only
those reads may justify routing or coverage; a changed set restarts registration before gates.
The protected interval ends at the final observer drain/context/clean validation immediately before
atomic publication, not at initial discovery. Alias/replacement writes within it invalidate even
restored bytes; double reads alone are not a fence. Bounded observation is not enforced immutability.
Unsupported discovery/types or unavailable/lost observation select full with a named limitation
and no reusable pointer, not a mutation refusal. Actual mutation refuses; verifier failure stays red.
That fallback preserves normal gate provisioning; unavailable prerequisites name their setup action.
Provisioning outputs and selected verification read inputs remain distinct.

Unsupported observation still permits the conservative read-only full gate, reported with its
stability limitation, but cannot establish reusable coverage. An observed mutation refuses
completion and preserves the logs. A fresh attempt after the writer finishes must run green.
The final observer drain and clean/context comparison precede atomic record publication; this is
bounded observation, not OS-enforced immutability against a hostile writer.

The Linux file-event diagnostic catches mutate/restore, but does not establish a complete
portable monitor. [The companion sketch](change-aware-completion-plan.md#stable-interval)
keeps startup, replacement, overflow, external-input and native-platform controls as explicit
promotion gates. The suite-wide scheduling ruling is unchanged.

## 7. Later work must earn broader coverage

### Source-impact selection is not in the first slice

A future local selector would cover changed Go packages and their transitive reverse importers:
packages whose builds or tests depend on a changed package, including internal and external test
imports. It needs current and baseline-side information for deleted/moved packages, Linux/Darwin
build configurations, architecture constraints, tags and cgo mode. Graph errors or missing history
select full; nearest-directory tests are insufficient.

Imports do not explain arbitrary reads. Explicit external-input routing must cover embeds, pack
JSON/Lua/JavaScript/prose, fixture/golden data, config/schema, generated scripts, flake/shipping
lists and gate/toolchain/workflow changes. Dependency changes must reach official binary pins.
Unknown input ownership selects full, not an empty package set.

Changing **selective source landing** would revise the whole-tree local rule and requires a future
policy decision. That optional revision is not an unanswered first-slice question and does not
hold ordinary-prose/read-only completion. Full CI remains a backstop in either case.

### Exact result reuse is a separate later slice

Reuse means accepting an actual successful command result for exactly the relevant Git-identifiable
inputs and verification context, not asking Go to use its own cache. A later protocol must record:

- actual green outcomes, named gate coverage, clean stable interval and producer/log provenance;
- Git-native source/gate identities, tool versions, flags, non-secret environment and platform;
- integration/runtime mode and which tests executed, skipped or were unavailable when relevant.

Only clean, identifiable inputs qualify initially. Dirty or changing inputs, failed/missing records,
partial command coverage, native skips, restored mutation reds and opaque environment differences
cannot cover omitted checks. No repository copies or per-path approval manifests are required.

A full `check-ci` green can cover its shared completion obligations; a completion green cannot
cover CI-only obligations it excluded. Integration cannot substitute for lint/pins, and Linux
integration cannot establish macOS execution. The first slice imports none of these old results.

## 8. Costs, alternatives and acceptance

| Alternative | Verdict |
| :--- | :--- |
| Treat every Markdown file as ordinary prose | Rejected: embeds, fixtures and guide/README readers contradict it |
| Use working diff or HEAD's parent as baseline | Rejected: misses already-committed multi-commit work |
| Require a manual task-start flag to make `done` safe | Rejected as sole mechanism: ordinary invocation must fail conservatively |
| Always run mutating `check` at completion | Rejected: can rewrite committed work while verifying it |
| Add a repository snapshot/build-system/cryptographic receipt layer | Rejected as disproportionate to local coverage records |
| Start with package selection and old-log reuse | Deferred: broader input and provenance obligations than this first slice |

| Risk | Consequence and mitigation |
| :--- | :--- |
| A new reader starts consuming allowlisted prose | Shortcut becomes unsound; re-audit at promotion and pin reader/classifier wiring |
| Context identification is incomplete | Full without reusable coverage; promotion requires a reachable default-profile positive case |
| Renderer or incoming-link analysis is incomplete | Full fallback; do not guess anchors or silently omit referrers |
| Input observation is unsupported or loses events | Full without reusable coverage; observed changes refuse completion |
| Missing tool or failed gate | Preserve red/unknown evidence and repair queue; never record success |

Acceptance requires the real `just done` entry point to demonstrate:

- ordinary allowlisted modifications invoke no Go/gofmt/staticcheck tools;
- source, sourceful Markdown, unknown, empty range and invalid baselines invoke full coverage;
- multiple commits, reverted intermediate source, merge parents, untracked files and renames count;
- dirty input, command failure and concurrent/transient mutation cannot advance success;
- recipe/selector call-site deletion produces red regression evidence, then restoration produces green;
- every route explains inherited/excluded work and never claims native/runtime coverage it did not run.

Build order is read-only completion and automatic baseline first, then the proven prose shortcut.
Only after those are independently accepted should package impact or exact result reuse be designed.
The [companion sketch](change-aware-completion-plan.md#before-promotion) owns remaining tree investigation;
engineering gaps go there, not into invented owner questions.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| CAC-D1 | Authorized first slice: ordinary-prose/read-only completion only; source landing and CI rules unchanged | 2026-10-09 | [Scope](#1-scope-completion-is-not-a-new-landing-policy) | — |
| CAC-D2 | Proposed engineering mechanism: automatic last-success baseline; missing/invalid/context-unknown evidence selects full | 2026-10-09 | [Baseline](#3-the-baseline-is-automatic-and-covers-earlier-commits) | — |
| CAC-D3 | Proposed engineering boundary: five exact modified-file paths; structural changes and all unknown/sourceful inputs select full | 2026-10-09 | [Allowlist](#4-the-first-shortcut-is-an-exact-allowlist) | — |
| CAC-D4 | Proposed stability contract: coordinated one-writer interval; no acceptance from endpoint equality alone | 2026-10-09 | [Stability](#6-cleanliness-and-a-stable-verification-interval) | — |
| CAC-D5 | Source-preparation amendment: standalone invocation uses bounded input observation, not caller certification; positive context identification executes no Go argv | 2026-10-09 | [Context](#context-cannot-be-silently-inherited), [stability](#6-cleanliness-and-a-stable-verification-interval) | — |
| CAC-D6 | Bounded diagnostic amendment: a sufficient inert-paragraph proof may preserve targets without a renderer adapter; all unsupported syntax still selects full | 2026-10-09 | [Targets](#anchors-and-incoming-references-are-real-inputs) | — |
| <a id="CAC-D7"></a>CAC-D7 | **Scope, confirmed by the owner 2026-10-09 ("yes to D7"), from the owner's request of 2026-10-09** (relayed by the coordinating agent): *"we just need only the right tests to run. We want this to be as fast as possible. It gets run a lot. And we want it to be intelligent about what's run."* `just done` selects Go checks by package impact and documents by recorded reader, which supersedes CAC-D1's prose-only slice and CAC-D3's five-path allowlist. Landing (`just check-ci`), CI and the integration and nested-jail obligations are unchanged. **Needs the owner's confirmation**, because section 7 called selective source checking a future policy decision | 2026-10-09 | [What shipped](#what-shipped) | ✅ `scripts/completion-check.py` |
| <a id="CAC-D8"></a>CAC-D8 | *Implementation decision.* Verification records live in the common Git directory (`yolo-completion/verified/<commit>.json`), keyed by commit and checked against that commit's tree and the current context, so every worktree shares them; a per-worktree lock and log stay in the worktree's own Git directory. **Why:** agents start each task in a new worktree, and per-worktree records (CAC-D2) would make every first `just done` a full run. A record says one tree passed under one context, which no worktree path changes. Reversible | 2026-10-09 | [What shipped](#what-shipped) | ✅ |
| <a id="CAC-D9"></a>CAC-D9 | *Implementation decision.* The change is the baseline-tree-to-HEAD-tree diff, not the union of every intervening commit's edges. **Why:** the check verifies HEAD's tree; a change made and reverted inside the range leaves no difference for any check to see, and every path that does differ is in the net diff. `--no-renames` still yields both names of a rename. Reversible | 2026-10-09 | [What shipped](#what-shipped) | ✅ |
| <a id="CAC-D10"></a>CAC-D10 | *Implementation decision.* Which tests read which non-imported files comes from a census of Go's own test log (`scripts/completion-census.py`), per test, replacing the renderer-adapter and inert-paragraph proofs of section 4. A directory listing counts only when a direct entry appears or goes. **Why:** it is the evidence `go test` itself uses to invalidate cached results, it covers arbitrary file reads that no import graph shows, and it is per test, so a doc change runs one citation test, not `internal/cli`'s 130 s suite. Reversible | 2026-10-09 | [What shipped](#what-shipped) | ✅ |
| <a id="CAC-D11"></a>CAC-D11 | *Implementation decision.* The context is the effective `go env` values (version, GOOS, GOARCH, GOROOT, GOFLAGS, CGO, toolchain, workspace, CC) and staticcheck's resolved path and version. Reading them runs `go env` and `staticcheck -version`, which CAC-D5 forbade on the prose route. **Why:** both take milliseconds and run no quality check; resolving tool identity from files without them was the unbuilt half of the old plan. Reversible | 2026-10-09 | [What shipped](#what-shipped) | ✅ |
| <a id="CAC-D12"></a>CAC-D12 | *Implementation decision.* Stability is the before-and-after check (clean tree, same HEAD) plus a per-worktree lock; there is no file-event observer, replacing CAC-D4 and CAC-D5's bounded observation. **Why:** the observer was unbuilt, Linux-only and could still not fence a writer. Cost, stated plainly: a write undone before the checks finish goes unseen; landing and CI rerun everything. Reversible | 2026-10-09 | [What shipped](#what-shipped) | ✅ |
| <a id="CAC-D13"></a>CAC-D13 | *Implementation decision.* A HEAD whose tree equals a verified tree under the same context runs no check, where section 3 sent an empty range to the full gate. **Why:** that record is an actual green for exactly these inputs, which is the reuse section 7 allowed once identities are recorded. Reversible | 2026-10-09 | [What shipped](#what-shipped) | ✅ |
| <a id="CAC-D14"></a>CAC-D14 | *Implementation decision.* Every changed Markdown file outside a Go package directory gets the Vantage check, including `docs/`, which `check-ci` does not check today. The ten `docs/` files with existing findings were fixed on 2026-10-09; the eleventh, `docs/research/vantage-check-0.5.9-findings.md`, is exempt by name, because its findings are specimens that its own opening note says must never be linked away. **Why:** section 4 requires the strict check on every changed document, and a check that skipped known-bad files would hide new findings in them. Reversible | 2026-10-09 | [What shipped](#what-shipped) | ✅ |
| <a id="CAC-D15"></a>CAC-D15 | *Implementation decision, from review.* Every scanning test — one whose recorded reads include a `D/*` or `D/*.E` pattern, or whose listings include a `D/**` tree — runs on every selective check, 43 tests in 18 packages on 2026-10-09. **Why:** what they read moves with ordinary edits, so a recorded set went stale between census runs: deleting a cited document selected none of the citation test, and `just done` would have recorded a tree `check-ci` rejects. Run uncached, the 42 of the earlier census took 14 s together; Go's test cache skips them when nothing they read changed. Reversible | 2026-10-09 | [Known limits](#known-limits) | ✅ |
| <a id="CAC-D16"></a>CAC-D16 | *Implementation decision, from review.* The Vantage wrapper runs a pinned release (0.10.0), not `@latest`. **Why:** with `@latest`, an upstream release could turn a document nobody changed red, and a document's check must depend only on the document. Raising the pin changes `scripts/`, so that commit runs the full gate. Reversible | 2026-10-09 | [What shipped](#what-shipped) | ✅ |

The standalone-invocation requirement supersedes the coordinator-assertion mechanism in CAC-D4:
an assertion cannot stop an uncoordinated writer, and refusing every uncertified caller would make
ordinary `just done` unreachable. CAC-D7 to CAC-D16 record what was built and where it departs
from CAC-D1 to CAC-D6.

The owner confirmed [CAC-D7](#CAC-D7) on 2026-10-09 ("yes to D7"). The speed plan's answered
[OQ-TS1](../plans/test-suite-speed.md#OQ-TS1) and [OQ-TS3](../plans/test-suite-speed.md#OQ-TS3), and open
[OQ-TS2](../plans/test-suite-speed.md#OQ-TS2) and [OQ-TS4](../plans/test-suite-speed.md#OQ-TS4), stay in their
own source. None is duplicated or silently answered here.
