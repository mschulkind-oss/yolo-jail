---
title: "Plan: the GitHub work kinds, after repository binding"
date: 2026-11-12
status: accepted
stage: DECIDED
next: "Task 2: the cell tables in config, the invariant at load and the ceiling's cell tokens"
depends-on:
  - github-write-scopes.md
  - github-repository-binding-plan.md
tags: [plan, github, broker, grants, config]
summary: "Hand-off for the work kinds of github-write-scopes.md, after the repository-binding slice: Task 1 (the cell classifier and the working set's shipped defaults in the broker) is built; Tasks 2 to 7 build the cell tables, their resolution, the verbs, the hand-over request, the per-cell timed grant and the disclosure; Task 8 the repository-level grants of §7. One question is open, OQ-GW16."
---

# Plan: the GitHub work kinds, after repository binding

**Status:** 2026-11-12. Completed against the tree from the seed, written against `8dc1bab44`.
Task 1 is built on the lane branch that carries this file; Tasks 2 to 8 are not. Built here means
the code and its tests are in a commit. It does not mean a release, a host install, or any grant
or config file changed.

**Design:** [`github-write-scopes.md`](github-write-scopes.md): the cells ([§1](github-write-scopes.md#1-the-cells)),
the ceiling and the ask ([§2](github-write-scopes.md#2-the-ceiling-and-the-ask)), the working set
([§3](github-write-scopes.md#3-the-working-set)), the resolution ([§4](github-write-scopes.md#4-how-one-call-resolves)),
the stops ([§5](github-write-scopes.md#5-every-stop-names-its-next-step)) and the repository-level grants
([§7](github-write-scopes.md#7-repository-level-grants-and-what-is-refused-outright)). Repository binding,
the first slice, is built from [`github-repository-binding-plan.md`](github-repository-binding-plan.md).

**Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only. This
file is advice and is the first thing to be wrong. **Stop and report, do not commit**, when: a red
test does not fail as the task predicts; a named file is dirty with someone else's work; a named
symbol is gone; the fix needs a file outside the task's fence; a gate fails outside the task's
scope; or the task meets [OQ-GW16](github-write-scopes.md#OQ-GW16) before it is ruled.

## What the seed got right and wrong

Kept: the classifier is `targetOf` in `internal/ghbroker/writetarget.go`; `prSubResource` and
`issueSubResource` stay the fence; `readIssue` gives PR-ness and `merged_at`; the four tests the
seed names still pin the old line. Dropped or corrected:

- **The seed's state-read note.** No second read is needed for Task 1: `issues/{n}` already says
  PR or issue. The `auto_merge` and `draft` read it mentions serves only the gate line's wording
  ([OQ-GW5](github-write-scopes.md#OQ-GW5) ruled an approval stays an approval), so no task reads
  `pulls/{n}`.
- **`yolo approve --add`** is removed (built 2026-11-06, [OQ-GW2](github-write-scopes.md#OQ-GW2)), so
  the seed's verb line about its output is void.
- **Pack call brokers already have cells.** `validateReadableCallBrokerRequests`,
  `validateReadableCallBrokerCeiling` and `readableCallBrokerCeilingAllows` in
  `internal/config/readableallow.go`, and `decideCall` in `internal/callbroker/decision.go`, hold
  the same `working_set`/`all` tables, `true`/`"ask"`/`false` values, cell-path ceiling tokens
  and a partial invariant. The seed did not know; it is the main reuse below.
- **Where the cells reach the broker** is decided here as [GW-D81](github-write-scopes.md#GW-D81):
  lowered into the scope file, beside the grants.

## Map

| Path | Task | Change |
| :--- | :--- | :--- |
| `internal/ghbroker/writetarget.go` | 1 (built) | `writeTarget.Verb`; `commandVerb`, `prAPIVerb`, `reviewVerdict`, `cellOf`; `--delete-branch` merge and `DELETE` are repository writes; approval's covering kinds |
| `internal/ghbroker/cells.go` | 1 (built), 3 | new: shipped working-set cells, `entryCovers`, the stop text; Task 3 replaces the shipped map with the resolved policy |
| `internal/ghbroker/evaluate.go`, `workingset.go` | 1 (built), 3 | the working-set search reads the cell; the audit carries it |
| `internal/ghbroker/approvalstore.go` | 1 (built), 5 | `RequestEntries` records a timed approval as its own kind; Task 5 per cell |
| `internal/brokerstore/workingresource.go` | 1 (built), 5 | `WorkingApprove`; `DispatchWorkingMatchContext` |
| `internal/brokeraudit/brokeraudit.go` | 1 (built) | `Event.Cell` |
| `internal/config/schema.go` | 2 | rows for `requests.github.working_set.*`, `requests.github.all.*` (mixed: boolean or string) and the same under `repos.*.requests.github` |
| `internal/config/readableallow.go` | 2 | the GitHub cells' validation, invariant and ceiling tokens, lowered as GW-D81 says |
| `internal/brokerscope/grant.go` | 2 | a `cell` grant: `Cell` (`working_set.merge_prs`) and `Value` (`true`, `ask`, `false`) |
| `packs/github/loopholes/github-broker/loophole.toml` | 2 | the cells declared, so a ceiling token validates |
| `internal/ghbroker/cells.go`, `evaluate.go`, `workingset.go`, `menu.go` | 3 | [§4](github-write-scopes.md#4-how-one-call-resolves)'s resolution from the live scope file, both tables; [§5](github-write-scopes.md#5-every-stop-names-its-next-step)'s stops |
| `internal/cli/request.go`, `internal/cli/allow.go` | 4 | `yolo request github <table>.<cell> [--ask] [--local]`; `yolo allow github [on_request] <path>` |
| `internal/brokerstore/workingresource.go`, `internal/ghbroker/approvalstore.go` | 5 | GW-D10 for every cell: a timed entry names its cell |
| `internal/cli/request.go`, new `internal/cli/githubworkingset.go`, `internal/ghbroker` | 6 | `yolo request github working-set <n>`, its broker request and approval line; `yolo github working-set [remove <n>]` |
| `internal/config/readableallow.go` (`ProduceReadableGateInput`), launch disclosure | 7 | the gate line and the launch line per repository (GW-D13, [§5](github-write-scopes.md#5-every-stop-names-its-next-step)'s last rows) |
| `internal/ghbroker/runs.go`, `approverun.go`, new files per grant | 8 | `dispatch_refs`, `approve_fork_runs`, `releases`, `labels`, `projects`, and GW-D55's refusals |

## Reuse before you write

- **The cell model:** `validateReadableCallBrokerRequests` and `validateReadableCallBrokerCeiling`
  (`internal/config/readableallow.go`): table names, the three values, the
  `"all.X covers working_set.X"` ceiling rule. Its invariant check covers only `all = true` over a
  `working_set` that is not; GW-D58 also refuses `false` under `"ask"` or absent. Extend it in
  one place for both sources, do not fork it.
- **The decision shape:** `decideCall` (`internal/callbroker/decision.go`) is the pure
  table-then-cell decision [§4](github-write-scopes.md#4-how-one-call-resolves) describes, timed answers included; mirror its shape for GitHub.
- **Timed entries per cell:** pack call brokers already key a timed entry by cell
  (`brokerstore.WorkingCallPrefix`, `IsCallKind`). Task 5 mirrors that.
- **Repository resolution:** the cells of a named repository come through the same lowering as
  its capability lines ([the binding plan](github-repository-binding-plan.md)); `BrokeredGrantKindsForRepo`
  is the scoped-ceiling read.
- **Broker fixtures:** `workingSetBroker`, `serveGH`, `pending`, `hostGHFake` (with `issue` for an
  issue read) in `internal/ghbroker/workingset_test.go`, and `lastAudit` in `daemon_test.go`.
- **Verbs:** `yolo request repos.<name>.github` (`internal/cli/requestrepos.go`) already writes
  under a named table; the cell verb takes the same path.

## Traps

- **Two decision paths.** A write is decided in `evaluateWrite` (the facts, which pick the
  covering entry) and dispatched in `coveredWrite`, whose `DispatchWorkingContext` picks the
  first live entry of the kinds again. Without a filter there the dispatch can pick an entry the
  facts rejected. Task 1 added `DispatchWorkingMatchContext`; every later coverage rule goes in
  `entryCovers`, which both paths call.
- **A timed answer folds into a lasting entry.** `planWorkingDecision`
  (`internal/brokerstore/workingcommit.go`) returns an existing live entry of the same kind and
  resource instead of a new timed one. A `--for` on an approval of a working-set PR recorded as
  `WorkingPR` therefore covers nothing: the request shows allowed and the retry is refused.
  Hence `WorkingApprove`, and Task 5's cell-keyed kinds.
- **`-F field=@file` never reaches the classifier.** `Authority.Classify` refuses it first ("reads
  a host file"); `reviewVerdict`'s `@` check is a second fence, not reachable from a test through
  the broker.
- **The legacy manifest fixture.** `TestEveryShippedLoopholeIsTOMLAndDecodesToWhatItAlwaysDid`
  compares `loophole.toml` to a frozen JSON; every change to the github broker's `description`
  must be named in its replacer, or it fails.
- **`targetOf` results are compared with `!=`** in `coveredWrite`'s gate: keep `writeTarget`
  comparable (no slices or maps).
- **The ruled behavior tightens existing state.** Lasting entries a human added with the old
  `--add` (GW-D34) are working-set members and get the cells like any other, so an approval on
  one asks from Task 1 on. That is the design, not a regression.

## Tasks

### Task 1 — the cell classifier and the working set's shipped defaults (built)

- **Settled, do not re-open:** a cell is a verb ([§1](github-write-scopes.md#1-the-cells)'s table) plus `_prs` or `_issues` by the
  item's own state; `pr review` takes its verdict from its flag and `gh api` from a plain
  `event` field, unreadable otherwise (GW-D2, GW-D4); ready is an edit (GW-D3); all of
  `pr merge` is merge (GW-D5); no cell covers a delete (GW-D56). The working set's comment and
  edit cells ship `true`; `approve_prs` and `merge_prs` ship absent, so they ask (GW-D42). A
  timed grant covers its cell on its item (GW-D10, built for the approval cell only). A covered
  write is audited with its cell (GW-D13).
- **Built:** `TestTargetOfNamesTheCellVerb`, `TestCellOfReadsPROrIssueFromTheItem`,
  `TestAWorkingSetPRRunsItsShippedCellsAndAsksForAnApproval`,
  `TestAWorkingSetIssueRunsItsIssueCells`,
  `TestAMergeThatDeletesItsBranchAsksEvenUnderATimedMerge`, and three rows in
  `TestTargetOfNamesKindAndResource` (`internal/ghbroker`).
- **Decided in the build:** a REST `DELETE` under a PR or issue asks as a repository write until
  [OQ-GW16](github-write-scopes.md#OQ-GW16) is ruled; a timed `WorkingPR`/`WorkingIssue` entry
  keeps covering comments and edits but no longer an approval.

### Task 2 — the cell tables in config (after 1)

- **Settled, do not re-open:** [§2](github-write-scopes.md#2-the-ceiling-and-the-ask)'s table of values; GW-D44's ceiling tokens (a cell path or a
  table, `all.X` covering `working_set.X`); GW-D40 (a naked verb, `*_any`, `*_own` is an error
  naming the cell); GW-D8 (`read = "ask"` is an error naming `yolo pack remove github`); GW-D58
  (the invariant: `false` < `"ask"` < `true`, absent read as `"ask"`, shipped defaults in, checked
  at load on the merged project and local tables, per repository's pair); GW-D7 (local over
  project, per cell); a `true` outside the ceiling is reported and counts as absent.
- **Change:** schema rows (mixed type) for both tables at top level and under
  `repos.*.requests.github`; the GitHub cells declared in `loophole.toml`; validation by the
  call-broker functions, extended for GW-D58's full order; lowering per GW-D81.
- **Tests first** (`internal/config`): each [§5](github-write-scopes.md#5-every-stop-names-its-next-step) `yolo check` row's text, one test per row; the
  invariant's five failing pairs and three passing ones; a `[repos.infra.requests.github.all]`
  cell lowered on that identity only; a `true` above the ceiling skipped with the reason.
- **Gates:** `go test ./internal/config/ ./internal/brokerscope/`, then `just check-ci`.
- **Fences:** no broker change; the broker keeps reading the shipped defaults until Task 3.
- **Commit:** `feat(config): GitHub cell tables, the working_set/all invariant and cell ceiling tokens`.

### Task 3 — [§4](github-write-scopes.md#4-how-one-call-resolves)'s resolution in the broker (after 2)

- **Settled, do not re-open:** [§4](github-write-scopes.md#4-how-one-call-resolves)'s six steps; GW-D57 (pick the table first, no fallback);
  `false` refuses with no request filed and names the line; `"ask"` asks; the ceiling's `always`
  decides an absent cell; a write on a named repository with no table asks.
- **Change:** `cells.go`'s shipped map becomes the effective cells read from the live scope file
  under the store lock (`b.authority.live()`); the `all` table covers writes on items outside the
  working set; `menu.go` and the stop texts follow [§5](github-write-scopes.md#5-every-stop-names-its-next-step)'s rows.
- **Tests first:** one broker test per [§4](github-write-scopes.md#4-how-one-call-resolves) step, through `serveGH`, with the scope file written as
  `grantCreatePR` writes it; the `false` stop files nothing (`pending` empty).
- **Commit:** `feat(ghbroker): resolve a GitHub write by its cell, working set first`.

### Task 4 — the verbs (after 2; independent of 3)

`yolo request github <table>.<cell> [--ask] [--local]` and `yolo allow github [on_request] <path>`,
with `repos.<name>.github` forms (GW-D46, GW-D77). Tests in `internal/cli/request_test.go` and
`allow_test.go` beside the existing `create_pr` cases.
**Commit:** `feat(cli): request and allow GitHub cells`.

### Task 5 — a timed grant per cell (after 3)

GW-D10 for every cell: `RequestEntries` records `cell:<cell>`-style kinds (mirror
`WorkingCallPrefix`), `entryCovers` reads them, and `WorkingApprove` from Task 1 becomes one of
them with its records still read. **Stop and ask** before rewriting stored records: the store's
records outlive a build, and Task 1's `WorkingApprove` records must stay readable.
**Commit:** `feat(ghbroker): a timed GitHub answer covers its cell on its item`.

### Task 6 — the hand-over request and the working-set verbs (after 3)

[OQ-GW9](github-write-scopes.md#OQ-GW9) and [OQ-GW10](github-write-scopes.md#OQ-GW10): the agent's
`yolo request github working-set <n>` (or `<owner>/<repo>#<n>`, GW-D65) files a broker request whose
approval line lists the `working_set` cells that would run (GW-D47); `yolo approve` adds the item
with `By: "human"`; `yolo github working-set` lists, `remove <n>` ends one; an item leaves on close
as well as merge. **Commit:** `feat(github): hand an item to the working set by request`.

### Task 7 — the gate and launch disclosure (after 3)

GW-D13's launch line per repository and the gate lines of [§1](github-write-scopes.md#1-the-cells) ([OQ-GW5](github-write-scopes.md#OQ-GW5)'s auto-merge sentence) and [§5](github-write-scopes.md#5-every-stop-names-its-next-step).
**Commit:** `feat(github): disclose the cells that run at launch and at the gate`.

### Task 8 — the repository-level grants (independent of 2 to 7)

One commit per row of [§7](github-write-scopes.md#7-repository-level-grants-and-what-is-refused-outright)'s table not yet built (`dispatch_refs`, `approve_fork_runs`, `releases`,
`labels`, `projects`), each mirroring `runs.go` and `approverun.go`, then GW-D55's refusals.

## Ships with

- **Docs that restate the line:** `packs/github/briefing/gh.md`, `userguide/guides/github.md`
  ("What an agent can run", "The working set", "Coming: finer write grants"), the github broker's
  `description` and comments in `loophole.toml`, and the design's Built column. Task 1 updated
  all four; each later task moves the "Coming" section's text into the guide proper.
  [BB-D111](boundary-broker.md#BB-D111)'s prose is the superseded design's and stays.
- **Tests that will move, and should:** the working-set coverage tests in
  `internal/ghbroker/workingset_test.go` when Task 3 lands (rewrite to the new resolution, do not
  relax); `TestApproveAddAndForCoverTheNextWrite` when Task 5 lands.
- **Integration:** `integration/githubbroker_test.go` drives reads and the repository scope
  through a launch, and no write through the working set, at the stamped commit; the broker
  tests drive `ServeContext`, the entry point a jail's `gh` reaches. Task 3 or 6 is the place to
  add one launch that creates a PR under a grant and comments on it.

## Don't

- Don't read a cell from the command's noun (`gh issue comment 32` on a PR is `comment_prs`).
- Don't make a lasting working-set entry carry a cell: membership is one population (GW-D39),
  and what it may do is the table's, read at each write.
- Don't add a second store read per entry: `readIssue` runs once per write (`facts.issueRead`).

## Blockers

[OQ-GW16](github-write-scopes.md#OQ-GW16): whether a REST `DELETE` that removes an assignee or a
requested reviewer is an edit or refused. Task 1 makes it ask; Tasks 3 and 8 need the ruling.
