---
title: "Where the GitHub line is drawn: which writes run unasked, per user and per workspace"
date: 2026-10-30
status: in-review
stage: DESIGN
next: "Rule OQ-GW7 to OQ-GW12; OQ-GW7 (does `all` reach working-set items?) shapes how a call resolves"
depends-on:
  - boundary-broker.md
tags: [design, github, gh, broker, grants, approvals, config, toml]
summary: "Today a PR or issue in the workspace's working set takes every write but a merge unasked, so on a colleague's PR a human added, the agent can approve it or mark it ready without asking. This doc replaces that one bit with two small tables of cells in the workspace's requests, `working_set` (the items the agent created or was handed by an approved request, one population) and `all` (every item of the repository), each cell a write class on PRs or issues (`comment_prs`, `edit_issues`, `merge_prs`, …) set to `true` or `\"ask\"`. The user's ceiling names the same cells by path; `\"ask\"` narrows without a human's y, and `true` needs the ceiling and the y. Repository-level grants (dispatch on more refs, fork-run approval, reruns, cancels, releases, labels, projects) join beside them, opt-in and never default."
---

# Where the GitHub line is drawn: which writes run unasked, per user and per workspace

**Status:** 2026-11-02. Nothing of the cells is built; `create_pr`, `create_issue`,
`dispatch_workflows` and `approve_runs` are ([§9](#9-migration-from-what-is-built)). Written from
the maintainer's ask: *"everybody's going to differ on where they want the line drawn. And it may be
on a per workspace basis."*

> **In short.** The line between "runs" and "asks" is a grid: what the write does (comment, edit,
> approve, merge), on what (PRs or issues), and over which items (the **working set**, or **all** of
> them). Each square is one key a learner can read cold, and no key changes what another means.

**Why it matters.** Today a working-set entry is one bit, so on a colleague's PR a human added, the
agent approves or readies it unasked ([§1](#1-the-line-today-and-where-it-leaks)).

**The shape.** Two tables under `[requests.github]`, `working_set` and `all`, holding six **cells**
([§4](#4-the-cells)); the user's ceiling names cells by path; `"ask"` narrows freely and `true`
widens only within the ceiling ([§5](#5-two-layers-ceiling-asks-and-the-narrowing)). An item joins
the working set by being created or by an approved hand-over request ([§6](#6-the-working-set)).

**Cost.** Approving a PR in the working set, which runs unasked today, asks until `all.approve_prs`
is granted. `yolo approve --add` goes.

**Start at [§4](#4-the-cells)**, the grid; [§5](#5-two-layers-ceiling-asks-and-the-narrowing) resolves a call.

**Needs your ruling:** [OQ-GW7](#OQ-GW7), [OQ-GW8](#OQ-GW8), [OQ-GW9](#OQ-GW9),
[OQ-GW10](#OQ-GW10), [OQ-GW11](#OQ-GW11), [OQ-GW12](#OQ-GW12).

**Reads with:** [`boundary-broker.md` §5.7](boundary-broker.md#57-permission-sets) (the grant
model this refines), [`config-user.md` §6](config-user.md#6-what-a-jail-may-do-with-your-logins)
(the `allow` and `requests` tables), [`github-write-scopes-plan.md`](github-write-scopes-plan.md)
(the implementation plan's seed; incomplete while any question here is open).

---

## 1. The line today, and where it leaks

The **working set** is the PRs and issues a workspace may write to unasked
([`boundary-broker.md` §1.3](boundary-broker.md#13-terms)). What a GitHub write does today, as built
in [BB-D111](boundary-broker.md#BB-D111) (`targetOf` and `coveredWrite` in `internal/ghbroker`):

| The write | Today |
| :--- | :--- |
| `gh pr create`, `gh issue create` | Runs unasked under a `create_pr` or `create_issue` grant the user allows and the project requests; the new item joins the working set |
| Any write to a working-set PR or issue but a merge: comment, edit, any review verdict, ready, close, reopen, reviewers, assignees | Runs unasked, for an item the agent created and one a human added with `yolo approve r-<id> --add` alike |
| `gh pr merge`, `PUT pulls/{n}/merge` | Always asks, per PR; a merge that runs ends the entry |
| Labels, projects, sub-issues, releases, settings, any other `gh api` write | Asks, as a write to the repository |
| Anything on an item outside the working set | Asks, per resource |

The leak is the second row. **A working-set entry is one bit: in or out**, and everything but a merge
rides on it, including the verdict GitHub treats as a reviewer's word on someone else's work. On a
PR the agent created that is harmless (GitHub refuses approving your own PR). On a colleague's PR a
human added, the agent approves it unasked, and an armed auto-merge then merges it.

People want different lines, and one person wants different lines in different repositories: some
want the agent to approve and merge freely, some want even a comment to ask, and a sensitive
repository wants the strict line while the rest of the same user's work does not.

## 2. Principles

- **P1. The line is drawn by class, read from the command.** Which cell a write falls in comes from
  its `gh` argv or REST method and path, plus whether the item is a PR or an issue, never from its
  effect at run time ([§4](#4-the-cells)).
- **P2. Every key reads cold.** A learner who sees one key, with its table header, knows what it
  does without knowing any other key exists, and **no key implies or changes another**. These are
  the owner's rules R1 and R2 ([GW-D37](#GW-D37), [GW-D38](#GW-D38)).
- **P3. Nothing new by default** ([BB-D104](boundary-broker.md#BB-D104),
  [BB-D105](boundary-broker.md#BB-D105)). An empty config grants nothing. The one exception is the
  working set's comment and edit cells, which ship `true` ([§6.3](#63-shipped-defaults)), because
  the working set holds only items the agent made or a human handed it.
- **P4. Narrowing is free; widening needs the ceiling and a human's y.** Making a write ask is
  honored from any file; making one run unasked needs the user's ceiling and the `y` at the
  config-change gate ([BB-D96](boundary-broker.md#BB-D96)).
- **P5. Every ask names the command that would have let it run**
  ([`happy-path-principle.md`](../reference/happy-path-principle.md)).

## 3. The axes

Four axes could carry the line. Three become the grid; the fourth is where a line is written.

| Axis | Values | Carried by |
| :--- | :--- | :--- |
| **What the write does** | comment, edit, approve, merge | The cell's verb ([§4.1](#41-the-grid)): a long list of commands folds into four |
| **What it is written to** | a PR, an issue | The cell's suffix, `_prs` or `_issues` |
| **Which items** | the working set; all items of the repository | The table: `[requests.github.working_set]` or `[requests.github.all]` |
| **Which workspaces** | every one; this project; this clone | The file: the user's config is every workspace, `yolo-jail.toml` one project for everyone who clones it, `yolo-jail.local.toml` one clone |

**The repository** is not an axis: a cell applies to every repository in the workspace's scope
([`boundary-broker.md` §5.6](boundary-broker.md#56-the-repository-scope)), as `create_pr` does. A
user who wants a different line in one repository has that repository's workspace file. A line per
PR number is what `yolo approve` draws, one resource at a time, in the store.

The table names, and the decision to stop naming the members of the working set, are the owner's:
*"if we stop trying to come up with a name for the things that go into the working set, we can just
like literally say working set permissions ... and then all. And then it's clear that the working set
has a special set, and then all is things that aren't in the working set."* Whether `all` excludes
the working set or overlays it is [OQ-GW7](#OQ-GW7).

## 4. The cells

A **cell** *(coined here)* is one key in `requests.github.working_set` or `requests.github.all`: one
verb on one kind of item over one set of items. It is not a repository-level grant
([§10](#10-repository-level-grants)) and not a creation grant (`create_pr`).

### 4.1 The grid

| Verb | `gh` commands | `gh api` writes | Cells |
| :--- | :--- | :--- | :--- |
| **comment**: says something, changes no field or state | `pr comment`, `issue comment`; `pr review` with `--comment` or no verdict flag | `POST pulls/{n}/comments`, `POST issues/{n}/comments`; `POST pulls/{n}/reviews` whose `event` is `COMMENT` or absent | `comment_prs`, `comment_issues` |
| **edit**: changes the item's fields, people, open or draft state, or blocks it | `pr edit`, `issue edit` (without `--add-project`/`--remove-project`); `pr ready`, with or without `--undo`; `pr close`, `pr reopen`, `issue close`, `issue reopen`; `pr review --request-changes` | `PATCH pulls/{n}`, `PATCH issues/{n}`; writes under `pulls/{n}/requested_reviewers` and `issues/{n}/assignees`; a review or review event with `REQUEST_CHANGES` | `edit_prs`, `edit_issues` |
| **approve**: says a PR may merge | `pr review --approve` | `POST pulls/{n}/reviews` or `…/reviews/{id}/events` with `event=APPROVE` | `approve_prs`, in `all` only |
| **merge**: merges a PR, or arms or disarms its auto-merge | `pr merge`, every flag (`--auto`, `--disable-auto`) but `--delete-branch` | `PUT pulls/{n}/merge` | `merge_prs` |

Each cell takes `true` (runs unasked, within the ceiling) or `"ask"` (asks here), or is absent
([§5](#5-two-layers-ceiling-asks-and-the-narrowing)); whether a third value `false` exists is
[OQ-GW8](#OQ-GW8). A write on no row is a write to the repository: it asks, unless a
repository-level grant ([§10](#10-repository-level-grants)) covers it.

Cold reads, the test every key passes: `[requests.github.all] merge_prs = "ask"` is "every merge
asks"; `[requests.github.working_set] edit_issues = true` is "edit issues in the working set". The
one defined term a learner needs is **working set**, which the header spells.

### 4.2 The rules the grid rests on

- **PR or issue is read from the item, not the command.** PRs and issues share one number sequence,
  and `gh issue comment 32` on PR #32 comments on a PR. The broker already reads the item's state
  before each covered write ([BB-D111](boundary-broker.md#BB-D111)); that read says which it is. A
  failed read asks.
- **A review's verdict is read from its flag or a plainly written `event` field**
  ([GW-D2](#GW-D2)). A body from `--input`, or `-F event=@file`, leaves it unreadable, and the write
  asks as a repository write.
- **Marking ready is an edit** ([GW-D3](#GW-D3)): it changes state as close and reopen do, and a
  user who wants "ready my own drafts, never approve anyone's" can say so.
- **Requesting changes is an edit** ([GW-D4](#GW-D4)): a comment changes nothing but adds words; a
  request-changes verdict changes the PR's review state.
- **Everything under `pr merge` is merge** ([GW-D5](#GW-D5)), `--disable-auto` included. The one
  exception is `--delete-branch` (and `pr close --delete-branch`): **no cell covers a delete**, so
  those ask, naming the merge without the flag and a git `push` grant for the branch delete
  ([GW-D56](#GW-D56)).
- **The sub-resource fences are [BB-D111](boundary-broker.md#BB-D111)'s.** Labels, projects,
  sub-issues, a percent-encoded or re-cased segment, a review dismissal and `update-branch` ask as
  repository writes; the grid moves no fence ([BB-P8](boundary-broker.md#BB-P8)).
- **There is no `working_set.approve_prs`.** `yolo check` refuses it naming `all.approve_prs`
  ([GW-D43](#GW-D43)). On a PR the agent created, GitHub refuses the approval; on one handed over, it
  is a verdict on someone else's work. One key would mean two behaviors.
- **No naked verb.** `comment`, `edit`, `approve` or `merge` alone, in `requests.github` or the
  ceiling, is a `yolo check` error naming the cells ([GW-D40](#GW-D40)).

### 4.3 An armed auto-merge: an approval stays an approval

**An approval, or a ready, on a PR whose author armed auto-merge is classified by its argv like any
other write**, not as a merge ([OQ-GW5](#decision-ledger), ruled B: *"it's less surprising
really"*). If GitHub then merges, that is the author's armed auto-merge completing.

- **The cell says so where it is granted.** The gate line for `all.approve_prs` states that an
  approval can complete an armed auto-merge and that, where branch protection requires a review,
  this approval is the review; the line for an `edit_prs` cell states that readying a draft can
  complete its armed auto-merge.
- **The agent cannot arm one itself** without a `merge_prs` cell: `pr merge --auto` is merge.
- **"A merge asks" is a statement about what the agent runs**, not what GitHub does next.

## 5. Two layers: ceiling, asks, and the narrowing

The cells use the two layers the config already has
([config-user.md §6](config-user.md#6-what-a-jail-may-do-with-your-logins),
[BB-D96](boundary-broker.md#BB-D96)). The **ceiling** is the user's `[allow.github]` table: what any
workspace may hold. The **gate** is the config-change prompt at launch or `yolo config apply`, where
a human answers `y` to a widening.

### 5.1 The ceiling names cells by path

A ceiling token is the cell's config path: `"all.merge_prs"`, `"working_set.merge_prs"`. A scope
alone names every cell of it: `"working_set"`, `"all"`. **`all.X` in the ceiling covers
`working_set.X`**, since an item in the working set is one of all items ([GW-D44](#GW-D44)).

| Where | Who writes it, and how | What it says |
| :--- | :--- | :--- |
| User config, `[allow.github] always = [...]` | The user, on the host: `yolo allow github all.comment_issues` | The cell runs unasked in every workspace with no project line; each workspace's gate still shows it ([OQ-GW6](#decision-ledger)) |
| User config, `[allow.github] on_request = [...]` | `yolo allow github on_request all.merge_prs` | A workspace may ask for the cell |
| Project config, `[requests.github.all] merge_prs = true` | Anyone who can edit `yolo-jail.toml`, the agent included: `yolo request github all.merge_prs` | This project asks for the cell; it runs once the ceiling has it and a human said `y`. Outside the ceiling it is reported and skipped, never an error |
| Project or local config, `merge_prs = "ask"` | Anyone: `yolo request github all.merge_prs --ask`, with `--local` for one clone | **Here, this cell asks, whatever the ceiling says.** Needs nobody's `y` ([GW-D45](#GW-D45)) |

The local file merges after the project file, so its value wins per cell ([GW-D7](#GW-D7)).
`yolo config set requests.github.all.merge_prs ask` is the same edit as `--ask`; advice names
`yolo request` ([GW-D46](#GW-D46)).

### 5.2 Resolving one call

For a write in cell C (say `merge_prs`) on item N, after the fences nothing here moves (refused
commands, the scope, the read floor), the pure decision is:

1. **If N is in the working set and `working_set.C` is present**, shipped default included, that
   value decides.
2. **Else if `all.C` is present**, that value decides. Under the strict reading of
   [OQ-GW7](#OQ-GW7), this step applies only to items outside the working set.
3. **Else the ceiling:** a token in the user's `always` that covers C for this item runs;
   otherwise it asks.

A value decides as follows: `"ask"` asks. `true` runs unasked when the ceiling holds a token
covering it (in `always` or `on_request`) and the human approved the request at the gate; a `true`
outside the ceiling was skipped at load and counts as absent, so the next step decides. A shipped
default needs no ceiling token ([§6.3](#63-shipped-defaults)).

The broker evaluates this against the live scope file under the store lock, as it does `create_pr`
today, and a timed grant or Allow once still covers what this leaves asking
([§6.4](#64-what-covers-a-write-in-order)).

### 5.3 Consequences

- **A workspace narrows below the user's `always`.** A sensitive repository's `yolo-jail.toml` sets
  `[requests.github.working_set] comment_prs = "ask"`, and comments on its PRs ask for everyone who
  clones it. Adding the line needs nobody's `y`; removing it is a widening and shows at the gate.
- **A workspace widens only to the ceiling.** `all.approve_prs = true` in a project whose user has
  no covering token is reported and skipped, naming `yolo allow github on_request all.approve_prs`.
- **"Merge mine, ask for others'" is two lines because it says two things:**
  `working_set.merge_prs = true` and `all.merge_prs = "ask"`.
- **`read` takes no narrowing.** `requests.github.read = "ask"` is a `yolo check` error naming
  `yolo pack remove github` ([GW-D8](#GW-D8)).

## 6. The working set

### 6.1 Membership

An item is in the working set when **the agent created it** through the broker, under a
`create_pr` or `create_issue` grant, or when **a human approved the agent's request to hand it
over**. The two are one population, treated identically by every cell ([GW-D39](#GW-D39)); the owner:
*"1 and 2 should be handled identically. otherwise we have 3 types of things, no?"*

Membership ends when the PR merges (as today), and, under [OQ-GW10](#OQ-GW10)'s leaning, when the
item closes or either side removes it. Entries a human added with `yolo approve --add` before its
removal are kept until revoked, listed with their origin, and ended by
`yolo approve --revoke w-<id>` ([GW-D34](#GW-D34)).

### 6.2 The hand-over request

> **Ruled 2026-11-02: `yolo approve --add` is removed.** The owner: *"I don't like this add flag
> because it's confusing. It rides along with a grant, or it rides along with an approval, and
> depending on what the approval is, it does different things, and you're trying to guess context.
> … Let's remove it. We can force the agent to take some other path here."*

The **hand-over request** *(coined here)* is the agent asking, by number, for an item to join the
working set; the owner: *"can't the agent request it to be added to the working set somehow?"*, then
*"yes that sounds good."* It is a broker request, like a held write, and not a config write, so it
needs no `yolo config apply`; a plain `yolo approve` answers it, and an approval of a write never adds
anything.

Kurt files bug #70; the agent fixed it and comments on it first:

```text
$ gh issue comment 70 --body "Fixed in #71"
gh: refused: comment on issue #70 (opened by kurt): not in this workspace's working set,
    and all.comment_issues is not granted (r-8f2).
  once:   yolo approve r-8f2                   (host) allows this comment only
  add:    yolo request github working-set 70   files a request; approved, #70 joins the working set
```

The agent runs the second line (its spelling is [OQ-GW9](#OQ-GW9)). Issue and PR numbers share one
sequence, so it takes no type word. **The approval line is computed from the project's
`working_set` cells**, so the human reads exactly what the item gets here:

```text
r-9a1  add issue #70 "flaky nightly on darwin" (opened by kurt) to the working set of yolo-jail
       there the agent may, unasked: comment, edit (close, reopen)
       yolo approve r-9a1        yolo approve r-9a1 --deny
```

**A handed-over PR gets merge when the project holds `working_set.merge_prs = true`**, since the two
populations are one; its approval line then reads `comment, edit, merge`. That consequence is
[OQ-GW12](#OQ-GW12). `yolo github working-set` lists the set with how each item entered;
`yolo github working-set remove 70` takes one out, a narrowing either side runs without approval.

### 6.3 Shipped defaults

A **shipped default** *(coined here)* is a cell yolo writes as present when no file sets it, which
`yolo config why` reports as `shipped default`. There are four, all in the working set:
`comment_prs`, `comment_issues`, `edit_prs` and `edit_issues` ship `true`. `merge_prs` ships absent,
and the `all` table ships empty.

They carry [OQ-GW3](#decision-ledger)'s ruling (*"agents should be able to comment on edit on PRs
they create. We can just specify that as part of create PR."*) as cells instead of as something
`create_pr` implies ([GW-D42](#GW-D42)):

- **No ceiling token is needed for them**: every item in the set entered under a creation grant the
  ceiling allowed or a hand-over a human approved, and that is the consent.
- **They are present cells**, so under the overlay reading an `all.comment_*` or `all.edit_*` value
  never reaches the set unless a project writes the `working_set` cell itself (R2 holds: writing
  `all.comment_issues = "ask"` changes no working-set item).
- **A project narrows them** like any cell: `working_set.edit_prs = "ask"`.

### 6.4 What covers a write, in order

1. **A creation under a user grant** (`create_pr`, `create_issue`): unchanged.
2. **A cell that resolves to "runs unasked"** ([§5.2](#52-resolving-one-call)), with the one state
   read finding N open: runs, audited with the cell's path and, for a working-set item, the
   entry's id ([GW-D13](#GW-D13)).
3. **A timed grant** from `yolo approve r-<id> --for <duration>`: the request's cell on its item,
   nothing wider, at most 24 hours ([GW-D10](#GW-D10)). A `--for 15m` on a comment does not cover
   an approval.
4. **Allow once**, as built.
5. Otherwise **a request**, whose message names the cell, the item, and the commands
   ([§8](#8-every-stop-names-its-next-step)).

## 7. What people write

The project file of each persona, with the verbs that write it in comments. Every user config holds
`read` (`yolo allow github read`), and each `true` needs its token in the user's `on_request`
(`yolo allow github on_request <path>`); an `"ask"` needs nothing.

```toml
# 1. Default: create PRs and tend them. Working-set comment and edit are shipped defaults.
[requests.github]                 # yolo request github create_pr
create_pr = true

# 2. Triage: every issue runs; a PR outside the working set asks, whatever the ceiling says.
[requests.github.all]             # yolo request github all.comment_issues; … all.edit_issues
comment_issues = true             # yolo request github all.comment_prs --ask; … all.edit_prs --ask
edit_issues = true
comment_prs = "ask"
edit_prs = "ask"

# 3. Reviewer: comment on and approve any PR (approving its own is GitHub's refusal).
[requests.github.all]             # yolo request github all.comment_prs; … all.approve_prs
comment_prs = true
approve_prs = true

# 4. Autonomous maintainer: merges every PR, its own included.
[requests.github]                 # yolo request github create_pr; … all.merge_prs
create_pr = true
[requests.github.all]
merge_prs = true

# 5. A sensitive repository: every merge asks, for everyone who clones it; nobody's y needed.
[requests.github.all]             # yolo request github all.merge_prs --ask
merge_prs = "ask"
```

Persona 3's gate line states the auto-merge reach
([§4.3](#43-an-armed-auto-merge-an-approval-stays-an-approval)). Under the strict reading of
[OQ-GW7](#OQ-GW7), persona 5 also needs `working_set.merge_prs = "ask"`.

**6. Hand-over of #70** writes no TOML; it is the flow of [§6.2](#62-the-hand-over-request):

```text
agent: gh issue comment 70 --body …        -> refused, files r-8f2
agent: yolo request github working-set 70  -> files r-9a1
host:  yolo approve r-9a1                  -> #70 joins the working set; the retried comment runs
```

## 8. Every stop names its next step

| The stop | What the agent reads, and the line it names |
| :--- | :--- |
| A cell not held, on a working-set item | `github-broker: approving PR o/r#32 asks here: all.approve_prs is not granted (r-7k2m). On the host: yolo approve r-7k2m once, or --for 15m; to grant it, yolo allow github on_request all.approve_prs, then yolo request github all.approve_prs here` |
| An item outside the working set | The refusal of [§6.2](#62-the-hand-over-request): once, or the hand-over request, plus the `all.<cell>` request |
| A cell set to `"ask"` | `… asks here: yolo-jail.toml sets requests.github.working_set.comment_prs = "ask" (r-7k2m). yolo approve r-7k2m once or --for 15m; to lift the line, yolo config unset requests.github.working_set.comment_prs --project, then yolo config apply` |
| A merge with `--delete-branch` | `… a merge that deletes its branch asks (r-7k2m): merge without --delete-branch, then git push origin --delete <head> under a git push grant, or yolo approve r-7k2m` |
| A review through `gh api` with an unreadable `event` | `… this review's verdict is not written on the command line (--input or @file), so it asks as a write to o/r (r-7k2m). Write -f event=COMMENT to have it classified, or yolo approve r-7k2m` |
| `yolo check`: a cell outside the ceiling | `yolo-jail.toml:4: this project requests all.approve_prs. Your config does not allow it on request, so this project runs without it. To allow it for every project: yolo allow github on_request all.approve_prs` |
| `yolo check`: a naked verb or `working_set.approve_prs` | `requests.github.approve: there is no approve on its own; to approve any PR, yolo request github all.approve_prs` (and the same naming `all.approve_prs` for the working-set spelling) |
| `yolo check`: `read = "ask"` | `requests.github.read: reads never ask. To keep your jails from reading GitHub: yolo pack remove github` |
| The launch disclosure | `github-broker: this workspace holds create_pr on acme/app; working set: comment, edit run (shipped default), merge asks; all: merge_prs asks; working set: 2 PRs, 1 issue` |

The verbs these lines name exist (`yolo allow`, `yolo request`, `yolo approve`, `yolo config unset`,
`yolo pack remove`) except the path form of `yolo request`'s argument, `--ask`, and the hand-over
request ([GW-D46](#GW-D46), [OQ-GW9](#OQ-GW9)); advice follows the format of the file it names
([RC-D40](readable-config.md#RC-D40)).

## 9. Migration from what is built

Of the GitHub grants, only `read`, `create_pr`, `create_issue`, `dispatch_workflows`
([GW-D14](#GW-D14)) and `approve_runs` ([GW-D15](#GW-D15)) are built, with the one-bit working set
and `yolo approve --add` and `--for`. No cell has a legacy spelling, so this is a mapping, not a
converter; the store needs none, since each entry records what added it and a timed entry its
resource.

| Built today ([BB-D111](boundary-broker.md#BB-D111)) | Under this design |
| :--- | :--- |
| `create_pr` / `create_issue` allowed and requested: creation runs unasked; the item joins the working set | Unchanged |
| Every non-merge write to a working-set item runs unasked | Comments and edits run under the shipped defaults; approvals ask until `all.approve_prs` is granted |
| A merge always asks | Asks unless a `merge_prs` cell covers it |
| `--for <duration>` covers every non-merge write to the item | Covers the request's cell on the item |
| `yolo approve --add` adds an item | Removed; the hand-over request replaces it. Earlier entries kept until revoked ([GW-D34](#GW-D34)) |
| A `gh api` review with an unreadable `event` runs unasked on a working-set PR | Asks, as a write to the repository |
| `grant_kinds` in a legacy user config | Already `on_request` ([config-user.md A.6](config-user.md#a6-the-grant-kinds-mapped)) |

## 10. Repository-level grants

Some writes are to the repository, not to a PR or issue, and never join the working set
([BB-D95](boundary-broker.md#BB-D95)). They ask unless one of these grants covers them. The owner's
rulings: *"approving/merging PRs and apprving a fork's runs shoul all be grantable if allowed. as
well as releases."*, and *"None of these wildcard everythings whatever should be grants by default,
but somebody's going to want them."* ([GW-D48](#GW-D48)). Approving and merging any PR are the cells
`all.approve_prs` and `all.merge_prs`; the rest are here.

Every grant follows the same rules: the user's `on_request` lists the word, the project's
`requests.github` names the resource, and it joins at the gate's `y` with a plain-words line. A
grant naming a resource is `on_request` only; a boolean one may sit in `always`, as `create_pr`
may. Each is checked under the store lock and audited under its own set.

| Project spelling | Covers | Never covers | Build |
| :--- | :--- | :--- | :--- |
| `dispatch_workflows = ["nightly.yml"]` | `gh workflow run <file>` and the dispatch endpoint, on the default branch, pinned to `refs/heads/<default>` | other refs, a workflow by id or display name | built ([GW-D14](#GW-D14)) |
| `dispatch_refs = ["release/*"]` or `["*"]` | a granted workflow on matching branches, pinned to `refs/heads/<name>` | a tag ref; alone it covers nothing, and `yolo check` names `dispatch_workflows` | [GW-D49](#GW-D49) |
| `approve_runs = ["github-actions[bot]"]` | approving a held run the named actor triggered from the repository itself | a fork's run | built ([GW-D15](#GW-D15)) |
| `approve_fork_runs = ["<login>"]` or `["*"]` | approving a held run a named actor triggered from a fork | reruns, cancels, deployment approvals | [GW-D50](#GW-D50) |
| `rerun_runs = true` | `gh run rerun <id>` (`--failed`, `--job`, `--debug`) and the three rerun endpoints, numeric id, stated method | a run with no id (the picker) | [GW-D51](#GW-D51) |
| `cancel_runs = true` | `gh run cancel <id>` and its endpoint | `run delete` | [GW-D51](#GW-D51) |
| `releases = ["draft"]` or `["draft", "publish"]` | creating and editing releases **on a tag that already exists**; a covered `release create` is sent with `--verify-tag` | a missing tag (asks, naming `git push origin <tag>` under `push_tags`), deletes, asset uploads | [GW-D52](#GW-D52) |
| `labels = true` | `gh label create`, `gh label edit` and their endpoints | `label delete`, `label clone` | [GW-D53](#GW-D53) |
| `projects = ["acme/5"]` | adding this repository's items to board `acme/5` and setting their fields | creating, closing, editing or deleting a board or field; draft items | [GW-D54](#GW-D54) |

Gate lines state the reach plainly. Three worth quoting:

- `dispatch_refs`: *"may run `nightly.yml` on branches matching `release/*` of github.com/o/r,
  unasked."* No secrets clause: a `push` grant on any branch already runs any workflow text with
  the repository's secrets through a push-triggered workflow, which is the owner's point: *"can't it
  already do this with perms to push to literally any branch?"*
- `approve_fork_runs`: *"… a fork's `pull_request` run gets a read-only token and no secrets, and
  runs the contributor's workflow changes on the repository's Actions minutes; a
  `pull_request_target` workflow runs the base branch's file with secrets."*
- `releases` with `publish`: *"a published release is public, notifies watchers and runs the
  repository's release workflows."*

**Still refused, under every grant** ([GW-D55](#GW-D55)): a repository's settings, secrets,
variables, deploy keys, rulesets, branch protection and collaborators; archive, transfer and
`repo delete`; any delete (`DELETE`, a `delete` verb, `--delete-last`, `--delete-branch`,
`item-delete`); raw GraphQL.

## 11. What this does not cover

- **Git.** Its working set has one kind of write, a push, and its line is the branch
  ([`git-broker.md` §5.1](git-broker.md#51-the-working-set-of-pushable-branches)).
- **The scope and the refusals** ([BB-P8](boundary-broker.md#BB-P8)). No cell or grant widens the
  repository scope or reaches a refused command.
- **Keeping a jail off GitHub entirely.** That is the user's pack selection.
- **Per-PR lines in config.** `yolo approve` draws those, in the store; the hand-over request adds
  an item, not a line.

## 12. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Keep one bit; make approve and ready always ask** | Rejected: one line for everyone, and the reviewer persona wants approvals unasked while a sensitive repository wants even comments asked |
| **Naked kinds**: `comment = true`, `approve = true` on the working set | Removed. The owner: *"I don't even understand what the naked approve and comment would even be. So I think that says we should remove them. We don't even have to have a migration path here."* |
| **`*_own` and `*_any` words, with `merge = true` as the one positive per-item word** | Rejected. The owner: *"I don't like that asymmetry."* |
| **`comment_others`** for items the agent did not create | Rejected (R1). The owner: *"problem with comment_others is you need to know that comment_own exists to know what it means."* |
| **Exact lists**: `comment_any = ["issues"]` grants issues and makes PRs ask | Rejected (R2). The owner: *"setting issues to ask shouldn't rely on you knowing the default and maintaining for other bits that are implied."* |
| **A per-class inline table** (`comment = { issues = true, prs = "ask" }`) | Rejected: one key holding several cells, so one line carries several lines; the suffix makes each cell a key that reads cold |
| **Naming the members** (`my_`, `agent_` prefixes) | Rejected: *"still confusing names."*; the owner's way out is to name the set, not its members ([§3](#3-the-axes)) |
| **A third population for handed-over items** | Rejected (R3): created and handed-over items are one population |
| **Catch the auto-merge case: an approval on an armed PR asks as a merge** | Not taken ([OQ-GW5](#decision-ledger), ruled B); the gate line states the reach |
| **A cell that names a repository** | Rejected ([§3](#3-the-axes)): the workspace file is where a repository's line goes |

## 13. What done looks like

- `gh pr review 32 --approve` on a handed-over PR, in a workspace without `all.approve_prs`, files a
  request naming `all.approve_prs` and the `yolo allow` line; `gh pr comment 32` on it runs, audited
  with `working_set.comment_prs`.
- With only `create_pr` allowed and requested, `gh pr comment 12` on the agent's own PR runs, and
  `yolo config why requests.github.working_set.comment_prs` says `shipped default`.
- `[requests.github.all] merge_prs = "ask"` in `yolo-jail.toml` makes `gh pr merge` ask, for every
  user, with the request naming the file and the `unset` line; deleting it shows at the gate and
  runs nothing until `y`.
- `yolo request github working-set 70`, approved, puts #70 in the set; the approval line listed
  exactly the cells the project holds; a retried comment runs.
- `yolo approve r-x --add` fails as an unknown flag naming `--for` and the hand-over request; an
  entry added with `--add` earlier still covers its item until `--revoke`.
- `working_set.approve_prs = true` and `comment = true` each fail `yolo check`, naming the cell.
- `gh release create v1.2.3` under `releases = ["draft", "publish"]` asks naming `git push origin
  v1.2.3` while the tag is missing, and runs with `--verify-tag` once it exists.

## 14. Open Questions

1. 💬 **OQ-GW7: Does "all" include the items in the working set?**

   Priya's project writes `[requests.github.all] merge_prs = "ask"`, meaning "every merge asks
   here". Her user config lets the working set merge in every workspace. The agent runs
   `gh pr merge 12` on a PR it created, which is in the working set.

   - **A — Overlay: `all` is every item.** The merge asks. A `working_set` cell that is set
     overrides `all` for set items; an absent one lets `all` decide. Cost: an `all` cell can change
     a set item while its `working_set` cell is absent (only merge has no shipped default).
   - **B — Strict: `all` is every item not in the working set.** The tables never meet, so #12
     merges unasked; "every merge asks" needs a second line, `working_set.merge_prs = "ask"`. This
     is the owner's own phrasing: "all is things that aren't in the working set."

   <!-- vantage: question id=OQ-GW7 leaning="Overlay: the word all means all, and a learner who sets all.merge_prs to ask and then sees a set PR merge unasked was misled by the key." -->

   _Leaning:_ Overlay: the word all means all, and a learner who sets all.merge_prs to ask and then sees a set PR merge unasked was misled by the key.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-GW8: Should a project be able to say "never" for a cell?**

   Sam maintains a repository where the agent must never approve a PR, not even after a human
   answers a request. His project can write `all.approve_prs = "ask"`, and then each approval asks
   and he can still say yes to one. There is no way to say "refuse it".

   - **A — Add `false`.** `all.approve_prs = false` refuses every approval here, whatever the
     ceiling, with no request filed; `yolo check` and the refusal name the line to remove. One more
     value, not a new shape.
   - **B — No `false`.** `"ask"` is the strongest line; a human who never wants it answers `--deny`.

   <!-- vantage: question id=OQ-GW8 leaning="Add false, as a refusal that beats the ceiling: it is one more value, and it is the only way to say never." -->

   _Leaning:_ Add false, as a refusal that beats the ceiling: it is one more value, and it is the only way to say never.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-GW9: What command does the agent run to ask for an item?**

   The agent wants Kurt's issue #70 added to the working set. It runs one command, which files a
   request the human answers with `yolo approve`.

   - **A — `yolo request github working-set 70`**, beside the commands that request grants, so the
     agent's side of every ask starts `yolo request`. Unlike them it writes no config.
   - **B — `yolo github working-set add 70`**, beside `yolo github working-set` (the list) and
     `remove 70`, so every working-set command is in one place.

   <!-- vantage: question id=OQ-GW9 leaning="yolo request github working-set 70: the agent's side of every grant is filing a request, and the host's is yolo approve." -->

   _Leaning:_ yolo request github working-set 70: the agent's side of every grant is filing a request, and the host's is yolo approve.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-GW10: When does an item leave the working set?**

   The agent closed issue #70, which Kurt handed over; a week later Kurt reopens it, and the agent
   comments. Today only a merge takes an item out of the working set.

   - **A — On merge or close, or an explicit `remove`.** The comment on the reopened #70 asks once,
     and the agent can ask to hand it over again. Finished work leaves the set.
   - **B — On merge or `remove` only, as today.** The comment runs; the set grows with every
     closed issue until someone removes it.
   - **C — An expiry** (say 30 days). Created items would expire too, since the two are one
     population.

   <!-- vantage: question id=OQ-GW10 leaning="A: on merge or close, or an explicit remove; no expiry, since a created item has none and a handed-over one is the same thing." -->

   _Leaning:_ A: on merge or close, or an explicit remove; no expiry, since a created item has none and a handed-over one is the same thing.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-GW11: Should the approve cell say that it reaches handed-over PRs?**

   Lee grants `all.approve_prs` to review colleagues' PRs. The PRs Lee's agent was handed are in the
   working set, and since there is no working-set approve cell, `all.approve_prs` is what covers
   them. Lee may not expect that from the key name.

   - **A — Say it once:** in the key's doc line and in `yolo config why`, not in the key name.
   - **B — Say nothing:** "all" already says every PR.

   <!-- vantage: question id=OQ-GW11 leaning="A: say it once in yolo config why and the key's doc line, not in the key name." -->

   _Leaning:_ A: say it once in yolo config why and the key's doc line, not in the key name.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-GW12: Should a handed-over PR be mergeable when the working set can merge?**

   Ana's project lets the agent merge its own PRs: `working_set.merge_prs = true`. The agent asks to
   be handed Bo's PR #40 to fix a typo, and Ana approves. Created and handed-over items are treated
   the same, so the agent can now merge Bo's PR unasked; the approval line Ana answered said
   `comment, edit, merge`.

   - **A — Yes, stated in the approval line.** One population, as ruled; Ana read "merge" before
     her `y`, and `--deny` or a project line `working_set.merge_prs = "ask"` are her ways out.
   - **B — A hand-over never carries merge.** Handed-over PRs merge only under `all.merge_prs`; this
     makes the two populations differ in one cell.

   <!-- vantage: question id=OQ-GW12 leaning="A: the two populations are one by ruling, and the approval line puts merge in front of the human before the y." -->

   _Leaning:_ A: the two populations are one by ruling, and the approval line puts merge in front of the human before the y.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="GW-D1"></a>GW-D1 | **Superseded by [GW-D36](#GW-D36) and [GW-D41](#GW-D41).** The work kinds are capabilities of the GitHub source, spelled `comment`, `edit`, `approve`, `merge` in `allow.github` and `requests.github`, the spelling `create_pr` already uses | 2026-10-30 | [§4.1](#41-the-grid) | — |
| <a id="GW-D2"></a>GW-D2 | A review's verdict is read only from a `pr review` flag or a plainly written `-f`/`-F` `event` field; `--input` or an `@file` value leaves it unreadable, and the write asks as a repository write | 2026-10-30 | [§4.2](#42-the-rules-the-grid-rests-on) | — |
| <a id="GW-D3"></a>GW-D3 | `pr ready`, with or without `--undo`, is an edit | 2026-10-30 | [§4.2](#42-the-rules-the-grid-rests-on) | — |
| <a id="GW-D4"></a>GW-D4 | `pr review --request-changes`, and the `REQUEST_CHANGES` event, are an edit; a comment changes nothing but adds words | 2026-10-30 | [§4.2](#42-the-rules-the-grid-rests-on) | — |
| <a id="GW-D5"></a>GW-D5 | Every `pr merge` invocation is merge, `--auto` and `--disable-auto` included; `--delete-branch` is excepted by [GW-D56](#GW-D56) | 2026-10-30 | [§4.2](#42-the-rules-the-grid-rests-on) | — |
| <a id="GW-D6"></a>GW-D6 | **Amended by [GW-D45](#GW-D45)** (the narrowing is per cell). A narrowing (`"ask"`) is honored from any file without the ceiling or a `y`; its removal is a widening and appears at the gate as a `+` line | 2026-10-30 | [§5.1](#51-the-ceiling-names-cells-by-path) | — |
| <a id="GW-D7"></a>GW-D7 | The local file merges after the project file for `requests.github`, as it does for every key; the last file wins per cell | 2026-10-30 | [§5.1](#51-the-ceiling-names-cells-by-path) | — |
| <a id="GW-D8"></a>GW-D8 | `requests.github.read = "ask"` is a `yolo check` error naming `yolo pack remove github`, the user-config verb; `packs` has no per-project form | 2026-10-30 | [§5.3](#53-consequences) | — |
| <a id="GW-D9"></a>GW-D9 | **Superseded by [OQ-GW3](#decision-ledger), now carried by [GW-D42](#GW-D42).** `yolo allow github [on_request] create_pr` offered to also allow `comment` and `edit`; there is nothing to pair | 2026-10-30 | [§6.3](#63-shipped-defaults) | — |
| <a id="GW-D10"></a>GW-D10 | A timed grant (`--for`) covers the request's cell on its item and no other cell | 2026-10-30 | [§6.4](#64-what-covers-a-write-in-order) | — |
| <a id="GW-D11"></a>GW-D11 | **Superseded by [GW-D41](#GW-D41)**, where approve and merge have no `_issues` cell. An issue's working-set entry never covers approve or merge | 2026-10-30 | [§4.1](#41-the-grid) | — |
| <a id="GW-D12"></a>GW-D12 | **Superseded by [GW-D46](#GW-D46).** `yolo request github <kind> --ask [--local]` wrote the narrowing on a kind key | 2026-10-30 | [§5.1](#51-the-ceiling-names-cells-by-path) | — |
| <a id="GW-D13"></a>GW-D13 | A covered write is audited with the covering cell's path (`working_set.comment_prs`, `all.merge_prs`) and, for a working-set item, the entry's id; the launch disclosure names the cells that run and the cells that ask, marking shipped defaults (amended for cells by [GW-D41](#GW-D41)) | 2026-10-30 | [§8](#8-every-stop-names-its-next-step) | — |
| <a id="GW-D14"></a>GW-D14 | **Maintainer's ask:** *"I want you to improve the broker so dispatch is possible. then we'll modify my config to allow it, relaunch, and have you dispatch it."* A user grant kind `dispatch-workflow` names a repository and a `workflow`, one workflow file name (`nightly-macos.yml`) or `*` for every workflow; a grant without one is refused at config load. Readable spelling: `allow.github.on_request = ["dispatch_workflows"]`, `requests.github.dispatch_workflows = ["<file>"]`. It covers only `gh workflow run <file>` and `gh api` POST `repos/{o}/{r}/actions/workflows/{file}/dispatches`, with no query or fragment on the endpoint, no `--input`, and exactly one field rooted at `ref` (a key cut at `[` or `]`, as gh reads it), which must be `ref` itself; a workflow named by id or display name, `run rerun`, `run cancel` and `workflow enable`/`disable` ask as before. It covers a run on the repository's **default branch** only: `--ref` omitted, the default branch's name, or `refs/heads/<name>`, read by the broker's own `gh api repos/{o}/{r}`. A covered run is sent pinned to the full ref: the argv that runs carries `--ref=refs/heads/<default>`, or `ref=refs/heads/<default>` for `gh api`, so what runs is exactly the branch checked, and neither gh's own default-branch lookup nor a tag of the same name can change it (review fix, 2026-10-31, replacing a tag-shadow read). The reason: a dispatch runs the workflow file as it is on the ref, with the repository's secrets, and a jail able to push a branch could push a changed workflow and dispatch it there. Any other ref asks (Allow once or a timed grant) unless [GW-D49](#GW-D49)'s `dispatch_refs` covers it. Inputs pass through unchanged. A covered run is authorized under the store's transaction lock with the grant re-read and audited under the set `dispatch-workflow`, as a `create-pr` creation is. Residual: the default-branch read happens just before the transaction, not inside it (a default branch renamed in between still runs the branch read), and a workspace that may push the default branch itself (a `branch` grant on it) can change the workflow it then dispatches | 2026-10-31 | [§10](#10-repository-level-grants) | built |
| <a id="GW-D15"></a>GW-D15 | **Maintainer's ask:** approve the workflow runs GitHub holds at `action_required` (the weekly `flake.lock` bump PR that `github-actions[bot]` opens from a branch of the same repository) from the jail without a per-call host approval. A user grant kind `approve-run`, modeled on `dispatch-workflow` ([GW-D14](#GW-D14)), names a repository and an `actor`: a GitHub login (letters, digits and hyphens, not starting with a hyphen, at most 39 characters, optionally followed by `[bot]`) or `*` for every actor; a grant without one is refused at config load. Readable spelling: `allow.github.on_request = ["approve_runs"]`, `requests.github.approve_runs = ["<login>"]`; `yolo request github approve_runs <login>` writes it. It covers exactly `gh api` with a stated `-X POST` (or `--method POST`) of `repos/{o}/{r}/actions/runs/{id}/approve`: a numeric id, no query or fragment, no field and no `--input`. Before running it, the broker reads `repos/{o}/{r}/actions/runs/{id}` through the host gh and covers the approval only when `triggering_actor.login` matches a grant's actor (case-insensitively, or `*`) **and** `head_repository.full_name` is the repository itself (case-insensitively). A fork's run is not covered by this grant, whatever the actor ([GW-D50](#GW-D50) is the fork's own word). A failed or unreadable read, an answer naming another run, a fork head or an actor no grant names asks, with a stderr line saying which; the pending request names `yolo request github approve_runs <actor>` then `yolo config apply`. Re-running, cancelling and `pending_deployments` approvals ask as before. A covered approval is authorized under the store's transaction lock with the grant re-read for the actor the broker read, and audited under the set `approve-run`. Residual: the run read happens just before the transaction, not inside it | 2026-11-02 | [§10](#10-repository-level-grants) | built |
| <a id="OQ-GW1"></a>[OQ-GW1](#OQ-GW1) | **Ruled A:** four work kinds, comment, edit, approve, merge. **Amended by [GW-D41](#GW-D41):** they survive as the verb half of each cell, not as keys | 2026-11-02 | [§4.1](#41-the-grid) | — |
| <a id="OQ-GW2"></a>[OQ-GW2](#OQ-GW2) | **Ruled:** `yolo approve --add` is removed; an item the agent did not create is reached by Allow once, by `--for`, or by the agent's own request to join the working set, which a plain `yolo approve` answers. Owner: *"can't the agetn request it to be added to the working set somehow?"*, then *"yes that sounds good"*. **Amended by [GW-D39](#GW-D39) and [GW-D47](#GW-D47):** a handed-over item gets what the working set's cells say, not "comment and edit only"; the standing `*_any` path is now the `all` table ([GW-D36](#GW-D36)) | 2026-11-02 | [§6.2](#62-the-hand-over-request) | — |
| <a id="GW-D33"></a>GW-D33 | **Superseded by [GW-D36](#GW-D36) and [GW-D40](#GW-D40)** (the removal of naked kinds stands; the `*_any` words and `merge = true` go). `comment`, `edit` and `approve` had no positive form; `merge = true` stayed as the one positive kind. Owner: *"I don't even understand what the naked approve and comment would even be. So I think that says we should remove them. We don't even have to have a migration path here."* | 2026-11-02 | [§4.2](#42-the-rules-the-grid-rests-on) | — |
| <a id="GW-D34"></a>GW-D34 | Working-set entries a human added with `--add` before its removal are kept until revoked, listed by `yolo approve` with their origin, and ended by `--revoke w-<id>`; the store records what was added, not how, so nothing migrates | 2026-11-02 | [§6.1](#61-membership) | — |
| <a id="GW-D35"></a>GW-D35 | **Superseded by [GW-D36](#GW-D36) and [GW-D38](#GW-D38).** The narrowing lived on the `*_any` keys (`comment_any`, `edit_any`, `approve_any`, `merge_any`), each taking a list of classes, `true`, or `"ask"`; `merge = true` stayed the one per-item positive word | 2026-11-02 | [§5](#5-two-layers-ceiling-asks-and-the-narrowing) | — |
| <a id="OQ-GW3"></a>[OQ-GW3](#OQ-GW3) | **Ruled B:** `create_pr` and `create_issue` carry comment and edit on what they create; a project narrows with `"ask"`. Owner: *"agents should be able to comment on edit on PRs they create. We can just specify that as part of create PR. I don't think it's really making one thing mean two things."* **Amended by [GW-D42](#GW-D42):** expressed as shipped defaults on the working set's comment and edit cells | 2026-11-02 | [§6.3](#63-shipped-defaults) | — |
| <a id="OQ-GW4"></a>[OQ-GW4](#OQ-GW4) | **Ruled A:** the narrowing is `"ask"` in `requests.github`; `true` requests, absent leaves it to the user. **Amended by [GW-D45](#GW-D45):** the value sits on a cell | 2026-11-02 | [§5.1](#51-the-ceiling-names-cells-by-path) | — |
| <a id="OQ-GW5"></a>[OQ-GW5](#OQ-GW5) | **Ruled B:** an approval or a ready on a PR with armed auto-merge is classified by its argv and is not a merge; the approve gate line states that an approval can complete an armed auto-merge. Owner: *"it's less surprising really"* | 2026-11-02 | [§4.3](#43-an-armed-auto-merge-an-approval-stays-an-approval) | — |
| <a id="OQ-GW6"></a>[OQ-GW6](#OQ-GW6) | **Ruled A:** the user's `always` may hold a work kind; each workspace's gate still shows it, and a project narrows with `"ask"`. **Amended by [GW-D44](#GW-D44):** the token is a cell path | 2026-11-02 | [§5.1](#51-the-ceiling-names-cells-by-path) | — |
| <a id="GW-D36"></a>GW-D36 | **Owner's ruling:** two tables of cells, `[requests.github.working_set]` and `[requests.github.all]`, named for the sets rather than their members. Owner: *"still confusing names."*, then *"if we stop trying to come up with a name for the things that go into the working set, we can just like literally say working set permissions ... and then all. And then it's clear that the working set has a special set, and then all is things that aren't in the working set."* Whether `all` overlays or excludes the working set is [OQ-GW7](#OQ-GW7). Supersedes [GW-D1](#GW-D1), [GW-D33](#GW-D33), [GW-D35](#GW-D35) | 2026-11-02 | [§3](#3-the-axes) | — |
| <a id="GW-D37"></a>GW-D37 | **Owner's ruling (R1):** every key reads cold, without knowing another key exists. Owner: *"problem with comment_others is you need to know that comment_own exists to know what it means."* | 2026-11-02 | [§2](#2-principles) | — |
| <a id="GW-D38"></a>GW-D38 | **Owner's ruling (R2):** no key implies or changes another; rejects the exact-list rule, where `comment_any = ["issues"]` made PRs ask. Owner: *"setting issues to ask shouldn't rely on you knowing the default and maintaining for other bits that are implied."* | 2026-11-02 | [§2](#2-principles) | — |
| <a id="GW-D39"></a>GW-D39 | **Owner's ruling (R3):** items the agent created and items handed to it by an approved request are one population, treated identically by every cell. Owner: *"1 and 2 should be handled identically. otherwise we have 3 types of things, no?"* | 2026-11-02 | [§6.1](#61-membership) | — |
| <a id="GW-D40"></a>GW-D40 | **Owner's rulings:** no naked verb and no `_own`/`_any` asymmetry. `comment`, `edit`, `approve`, `merge` alone, `*_any`, `*_own` and `merge = true`, in `requests.github` or the ceiling, are `yolo check` errors naming the cell; no migration, since none was built. Owner, on naked kinds: *"I don't even understand what the naked approve and comment would even be. So I think that says we should remove them. We don't even have to have a migration path here."*; on `*_own`/`*_any` and `merge = true`: *"I don't like that asymmetry."* | 2026-11-02 | [§4.2](#42-the-rules-the-grid-rests-on) | — |
| <a id="GW-D41"></a>GW-D41 | Six cells, verb plus suffix: `comment_prs`, `comment_issues`, `edit_prs`, `edit_issues`, `approve_prs`, `merge_prs`, classed by the argv of [§4.1](#41-the-grid) and the item's own PR-or-issue state; each takes `true` or `"ask"` or is absent; approve and merge have no `_issues` cell; no cell implies another | 2026-11-02 | [§4.1](#41-the-grid) | — |
| <a id="GW-D42"></a>GW-D42 | The working set's `comment_prs`, `comment_issues`, `edit_prs` and `edit_issues` ship `true` as present cells, reported `shipped default` by `yolo config why`, needing no ceiling token since every member entered under a ceiling-allowed creation or a human-approved hand-over; `merge_prs` ships absent and `all` ships empty. Carries [OQ-GW3](#decision-ledger) | 2026-11-02 | [§6.3](#63-shipped-defaults) | — |
| <a id="GW-D43"></a>GW-D43 | There is no `working_set.approve_prs`: `yolo check` refuses it, and the ceiling token, naming `all.approve_prs`; on created PRs GitHub refuses the approval and on handed-over ones it is a verdict on another's work | 2026-11-02 | [§4.2](#42-the-rules-the-grid-rests-on) | — |
| <a id="GW-D44"></a>GW-D44 | A ceiling token is a cell's config path (`"all.merge_prs"`), or a scope alone for every cell in it (`"working_set"`, `"all"`); `all.X` covers `working_set.X`; `yolo allow github [on_request] <path>` writes it | 2026-11-02 | [§5.1](#51-the-ceiling-names-cells-by-path) | — |
| <a id="GW-D45"></a>GW-D45 | Resolution per call, in order: the `working_set` cell for a set item when present; else the `all` cell; else the ceiling's `always`; else ask. `"ask"` asks, from any file, with no ceiling and no `y`; `true` runs only within the ceiling and after the gate's `y`, and a `true` outside the ceiling counts as absent | 2026-11-02 | [§5.2](#52-resolving-one-call) | — |
| <a id="GW-D46"></a>GW-D46 | `yolo request github <scope>.<cell> [--ask] [--local]` writes a cell (`true`, or `"ask"` with `--ask`); `yolo config set requests.github.<scope>.<cell> ask` is the same edit, and advice names `yolo request`. Supersedes [GW-D12](#GW-D12) | 2026-11-02 | [§5.1](#51-the-ceiling-names-cells-by-path) | — |
| <a id="GW-D47"></a>GW-D47 | The hand-over request is a broker request filed by the agent naming one item number, answered by a plain `yolo approve` or `--deny`, writing no config; its approval line lists the cells the item would get, computed from the project's `working_set` cells. Its spelling is [OQ-GW9](#OQ-GW9) | 2026-11-02 | [§6.2](#62-the-hand-over-request) | — |
| <a id="GW-D48"></a>GW-D48 | **Owner's rulings:** repository-level and wide grants are opt-in, never default: *"None of these wildcard everythings whatever should be grants by default, but somebody's going to want them."*; *"approving/merging PRs and apprving a fork's runs shoul all be grantable if allowed. as well as releases."* Each is two-layer, a resource-taking one `on_request` only, joining at the gate with a plain-words line, checked under the store lock, audited under its own set | 2026-11-02 | [§10](#10-repository-level-grants) | — |
| <a id="GW-D49"></a>GW-D49 | `dispatch_refs`, a list of branch names or patterns (`*` allowed), widens a `dispatch_workflows` grant from the default branch to matching branches, pinned to `refs/heads/<name>`; a tag ref is never covered; alone it covers nothing and `yolo check` says so; its gate line states the reach with no secrets clause. Owner: *"can't it already do this with perms to push to literally any branch?"* | 2026-11-02 | [§10](#10-repository-level-grants) | — |
| <a id="GW-D50"></a>GW-D50 | `approve_fork_runs = ["<login>" or "*"]` covers the run-approve endpoint when the run read shows a fork head and a matching actor; `approve_runs` keeps the repository's own heads; the gate line names the read-only token, the minutes and `pull_request_target` | 2026-11-02 | [§10](#10-repository-level-grants) | — |
| <a id="GW-D51"></a>GW-D51 | `rerun_runs = true` covers `gh run rerun <id>` and the `rerun`, `rerun-failed-jobs` and `jobs/{id}/rerun` endpoints with a numeric id, stated method, no field and no `--input`, with no state read; `cancel_runs = true` covers `gh run cancel <id>` and its endpoint; separate words, so a user can hold one without the other | 2026-11-02 | [§10](#10-repository-level-grants) | — |
| <a id="GW-D52"></a>GW-D52 | `releases = ["draft"]` or `["draft", "publish"]`, `publish` not implying `draft`, covers creating and editing releases only on a tag one read finds, with `--verify-tag` added to a covered `release create`; a missing tag asks naming `git push origin <tag>` under `push_tags`; deletes and asset uploads never | 2026-11-02 | [§10](#10-repository-level-grants) | — |
| <a id="GW-D53"></a>GW-D53 | `labels = true` covers creating and editing the repository's labels; never `label delete` or `label clone` | 2026-11-02 | [§10](#10-repository-level-grants) | — |
| <a id="GW-D54"></a>GW-D54 | **Owner's ruling:** offer projects. `projects = ["<owner>/<number>"]` covers adding this repository's items to a granted board, `item-edit` and `item-archive` on it, and `--add-project`/`--remove-project` after one read resolves the title to a granted board; those `gh project` item subcommands leave the account-wide refusal only when owner and number match; never board or field create, close, edit, delete, copy or link, nor draft items | 2026-11-02 | [§10](#10-repository-level-grants) | — |
| <a id="GW-D55"></a>GW-D55 | Still refused under every grant: settings, secrets, variables, deploy keys, rulesets, branch protection, collaborators, archive, transfer, `repo delete`, any delete, raw GraphQL | 2026-11-02 | [§10](#10-repository-level-grants) | — |
| <a id="GW-D56"></a>GW-D56 | No cell covers a delete: `pr merge --delete-branch` and `pr close --delete-branch` ask, naming the write without the flag and a git `push` grant for the branch delete | 2026-11-02 | [§4.2](#42-the-rules-the-grid-rests-on) | — |
