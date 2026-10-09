---
title: "Completion should check the task, without rewriting it"
date: 2026-10-09
status: accepted
stage: DECIDED
next: "Close the supported context profile, renderer adapter and monitored-interval fixtures in the companion sketch before implementation hand-off"
tags: [testing, tooling, design]
summary: "A bounded documentation-only completion shortcut, with an automatic successful baseline and conservative read-only fallback; source landing policy remains unchanged."
---

# Completion should check the task, without rewriting it

**Status:** 2026-10-09 — behavior specified; nothing built or runtime-verified.

> **In short.** Completion should verify the work since a successful baseline and explain its coverage,
> not format committed code or run unrelated Go checks for proven ordinary prose.

**Why it matters.** Suite cost and unnecessary repetition are different problems.
**The shape.** One completion command collects changes, selects checks and records successful coverage.
**Cost.** A small exact allowlist buys a deliberately narrow shortcut; uncertain inputs still pay for the full gate.
**Start at [the baseline](#3-the-baseline-is-automatic-and-covers-earlier-commits)** — empty working diffs are not evidence.
**Needs your ruling:** None for the first slice; selective source landing is excluded.
**Reads with:** [input research](../research/completion-check-inputs.md) (source evidence),
[the companion sketch](change-aware-completion-plan.md) (not a build hand-off), and
[suite speed](../plans/test-suite-speed.md) (independent timing work).

---

## 1. Scope: completion is not a new landing policy

My recommendation is a small, read-only front door for `just done`, initially optimizing only
proven ordinary documentation changes. **Read-only** means it does not rewrite tracked files,
the index, refs or untracked task files. Tools may use their ordinary caches and temporary outputs;
the command may write its own verification record under the worktree's Git administrative directory.
It never stages, commits, formats, launches a jail or publishes anything. Provisioning is not a
completion step: the existing gate can resolve a checker or fetch its pinned Go toolchain, so
completion must preflight already-provisioned tools and refuse with the setup action rather than
promise that calling the existing recipe alone prevents installation.

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
log locations. It stores no secrets or repository copy. Only this command writes the successful
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
version, flags and effective non-secret verification environment used by its full run. A changed
context or inability to establish it selects full. Secrets are never logged; an input whose
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

For a listed modification, compare the ordered rendered target identities against the baseline
and **both sides of every collected history edge**. A set of slugs alone loses duplicate-heading
ownership and ordering. Any changed heading/explicit/question identity, source-citation target or
unclassifiable syntax selects full. Strict checker success is not anchor extraction: the installed
checker has different renderer and link-index pipelines and no public anchor-export command.
Do not port its heading slugger into a regex and call that renderer equivalence.

Source-citation preservation also compares the existing test's own target interpretation, which
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

Ordinary standalone `just done` must be supported without caller certification. It starts an
input-change observer before resolving/classifying inputs, establishes a registration barrier,
and keeps observing through gates and final record validation. The supported observer must cover
tracked file inodes, their parent directories, relevant Git administration and identified external
context inputs; atomic replacement and alias writes must not evade it. An event affecting an
input invalidates the attempt even when final bytes are restored. Overflow, watch loss, unreadable
inputs or unsupported filesystem behavior are unknown stability, not green.

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

The standalone-invocation requirement supersedes the coordinator-assertion mechanism in CAC-D4:
an assertion cannot stop an uncoordinated writer, and refusing every uncertified caller would make
ordinary `just done` unreachable. The underlying prohibition on accepting restored mutations
remains. None of these mechanisms is implemented by this document.

No owner questions are open in this design. The speed plan's answered
[OQ-TS1](../plans/test-suite-speed.md#OQ-TS1) and [OQ-TS3](../plans/test-suite-speed.md#OQ-TS3), and open
[OQ-TS2](../plans/test-suite-speed.md#OQ-TS2) and [OQ-TS4](../plans/test-suite-speed.md#OQ-TS4), stay in their
own source. None is duplicated or silently answered here.
