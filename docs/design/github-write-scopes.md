---
title: "Where the GitHub line is drawn: which writes run unasked, per user and per workspace"
date: 2026-10-30
status: accepted
stage: DECIDED
next: "Write the implementation plan from the ruled design; GW-D58's invariant is ruled"
depends-on:
  - boundary-broker.md
tags: [design, github, gh, broker, grants, approvals, config, toml]
summary: "Which of the agent's gh writes run unasked is a 2x2: the user's config is the ceiling (the most a workspace may hold) and the workspace's config is the ask, each with two tables of the same six cells, `working_set` (the PRs and issues the agent created or was handed) and `all` (every other item). A write on a working-set item reads the `working_set` table only; any other write reads `all` only. One invariant: `working_set` is never below `all` in any cell (false < ask < true), or the config is refused. Repository-level grants (dispatch on more refs, fork-run approval, reruns, cancels, releases, labels, projects) sit beside the cells, opt-in and never default. Every line is for the workspace's own repository, `origin`, unless it sits in a `[repos.<name>]` table, which names one repository for git and GitHub alike, by a git remote's name or a `repo` line, and holds its grants under `requests` (git's lines, with GitHub's under `requests.github`); a named repository with no lines is read only."
---

# Where the GitHub line is drawn: which writes run unasked, per user and per workspace

**Status:** 2026-11-12 — every question is ruled. The repository-binding slice is built (commit
`28757f5a0` records it); the work-kind cells and Git lines under a repository name are not.
[Current support](#current-support-and-the-repository-only-first-slice) has the detail.

The GitHub broker runs some of the agent's `gh` writes unasked and holds the rest for a human.
Where that line sits is four small tables: two files, two sets of items.

| | `working_set`: the PRs and issues the agent created or was handed | `all`: every other PR and issue of the repository |
| :--- | :--- | :--- |
| **User config, the ceiling** (host, `~/.config/yolo-jail/config.toml`): the most any workspace may hold | `[allow.github] on_request = ["working_set.merge_prs"]` | `[allow.github] on_request = ["all.merge_prs"]` |
| **Workspace config, the ask** (`yolo-jail.toml`, or `yolo-jail.local.toml` for one clone) | `[requests.github.working_set] merge_prs = true` | `[requests.github.all] merge_prs = "ask"` |

**The cells**, the same six in each table: `comment_prs`, `comment_issues`, `edit_prs`, `edit_issues`,
`approve_prs`, `merge_prs`. A cell is `true` (runs unasked, once the ceiling allows it and a human
said `y`), `"ask"` (asks here, nobody's `y` needed), `false` (refused here, no request filed), or
absent (the user's `always` list decides, else it asks).

**The rule.** A write on an item in the working set reads the `working_set` table and nothing else.
A write on any other item reads `all` and nothing else.

**The invariant.** Whatever `all` grants, `working_set` grants too: `all.X = true` without
`working_set.X = true` is a config error, because an agent that may merge everyone's PRs but not its
own is a mistake. `"ask"` and `false` are not grants, so `all.X` set to either never trips it.

```toml
[allow.github]                      # user config, host: the ceiling
on_request = ["all.comment_issues", "all.merge_prs"]   # all.X allows working_set.X too

[requests.github]                   # yolo-jail.toml: the ask
create_pr = true
[requests.github.working_set]       # comment_* and edit_* ship true here
merge_prs = true                    # merges what it created or was handed
[requests.github.all]
comment_issues = true               # comments on anyone's issue
merge_prs = "ask"                   # a merge of anyone else's PR asks
```

**Reads with:** [`boundary-broker.md` §5.7](boundary-broker.md#57-permission-sets) (the grant
model this refines), [`config-user.md` §6](config-user.md#6-what-a-jail-may-do-with-your-logins)
(the `allow` and `requests` tables), [`github-write-scopes-plan.md`](github-write-scopes-plan.md)
(the plan's seed).

---

## 1. The cells

A **cell** *(coined here)* is one key in `requests.github.working_set` or `requests.github.all`: one
verb on one kind of item. The cell is read from the `gh` argv or REST method and path, never from the
write's effect at run time.

| Verb | `gh` commands | `gh api` writes | Cells |
| :--- | :--- | :--- | :--- |
| **comment**: says something, changes no field or state | `pr comment`, `issue comment`; `pr review` with `--comment` or no verdict flag | `POST pulls/{n}/comments`, `POST issues/{n}/comments`; `POST pulls/{n}/reviews` whose `event` is `COMMENT` or absent | `comment_prs`, `comment_issues` |
| **edit**: changes fields, people, open or draft state, or blocks the item | `pr edit`, `issue edit` (without `--add-project`/`--remove-project`); `pr ready`, with or without `--undo`; `pr close`, `pr reopen`, `issue close`, `issue reopen`; `pr review --request-changes` | `PATCH pulls/{n}`, `PATCH issues/{n}`; writes under `pulls/{n}/requested_reviewers` and `issues/{n}/assignees`; a review or review event with `REQUEST_CHANGES` | `edit_prs`, `edit_issues` |
| **approve**: says a PR may merge | `pr review --approve` | `POST pulls/{n}/reviews` or `…/reviews/{id}/events` with `event=APPROVE` | `approve_prs` |
| **merge**: merges a PR, or arms or disarms its auto-merge | `pr merge`, every flag (`--auto`, `--disable-auto`) but `--delete-branch` | `PUT pulls/{n}/merge` | `merge_prs` |

- **PR or issue is read from the item**, not the command: the two share one number sequence, and the
  broker's state read before each write says which. A failed read asks.
- **A review's verdict is read from its flag or a plainly written `event` field** ([GW-D2](#GW-D2));
  `--input` or `-F event=@file` leaves it unreadable, and the write asks as a repository write.
- **Ready and request-changes are edits** ([GW-D3](#GW-D3), [GW-D4](#GW-D4)); **all of `pr merge` is
  merge** ([GW-D5](#GW-D5)). **No cell covers a delete** ([GW-D56](#GW-D56)): `--delete-branch` asks.
- **Approve and merge have no `_issues` cell** ([GW-D41](#GW-D41)); **`working_set.approve_prs` exists**
  ([GW-D59](#GW-D59)): on the agent's own PR GitHub refuses the approval; on a handed-over one the
  hand-over line said `approve` before the human's `y`. **No naked verb**: `comment = true` alone is a
  `yolo check` error naming the cells ([GW-D40](#GW-D40)).
- **An approval stays an approval on a PR with armed auto-merge** ([OQ-GW5](#decision-ledger)). The
  `approve_prs` gate line says an approval can complete an armed auto-merge and, where protection
  requires a review, is that review; the `edit_prs` line says readying a draft can complete one. The
  agent cannot arm one without `merge_prs`: `pr merge --auto` is merge.
- **Every other write is a write to the repository** (labels, projects, sub-issues, review dismissals,
  `update-branch`): it asks unless a repository-level grant covers it ([§7](#7-repository-level-grants-and-what-is-refused-outright)).

## 2. The ceiling and the ask

The **ceiling** is the user's `[allow.github]` table: the most any workspace of this user may hold.
Its tokens are cell paths, `"all.merge_prs"`, or a whole table, `"working_set"` ([GW-D44](#GW-D44));
`all.X` allows `working_set.X` too, which the invariant needs. `on_request` lists what a workspace may
ask for; `always` lists what runs in every workspace with no project line, each workspace's gate still
showing it ([OQ-GW6](#decision-ledger)). The **ask** is the workspace's `[requests.github]` tables;
the local file merges after the project file, last value per cell wins ([GW-D7](#GW-D7)). Both
tables, and every grant of [§7](#7-repository-level-grants-and-what-is-refused-outright), are for
the workspace's own repositories; a `[repos.<name>.requests.github]` table holds the same
keys for one other repository ([§8](#8-which-repository-a-line-is-for)).

| Value in the ask | Means | Needs |
| :--- | :--- | :--- |
| `true` | the cell runs unasked here | a covering ceiling token and a human's `y` at the config-change gate; outside the ceiling it is reported at load and counts as absent, never an error |
| `"ask"` | the cell asks here, whatever the ceiling says | nobody's `y`; removing it is a widening and shows at the gate |
| `false` | the cell is refused here, whatever the ceiling or `always` says, with no request filed; the refusal names the line to remove ([OQ-GW8](#decision-ledger)) | nobody's `y`; removing it is a widening and shows at the gate |
| absent | the user's `always` decides; else it asks | — |

**Where the invariant is checked** ([GW-D58](#GW-D58), confirmed 2026-11-03): at config load, so
`yolo check`, a launch and `yolo config apply` all refuse the same file, on the workspace's two tables
after the project and local files merge, shipped defaults in. The values order `false`, then `"ask"`,
then `true`, with an absent line read as `"ask"` (both tables meet the same `always`). For every cell X,
`working_set.X` below `all.X` in that order is the error, *"in any way"*: `"ask"` or `false` under
`all.X = true`, and `false` under `all.X = "ask"` or an absent `all.X`. The ceiling needs no check: an
`all.X` token covers `working_set.X`.
For a grant, `all` therefore means all. For `"ask"` it means the rest: `all.merge_prs = "ask"` leaves
working-set merges to `working_set.merge_prs`, and "every merge asks" is both lines ([§6](#6-what-people-write)).

The verbs: `yolo allow github [on_request] <path>` writes the ceiling, on the host;
`yolo request github <table>.<cell> [--ask] [--local]` writes the ask, in a jail too ([GW-D46](#GW-D46)).
`read` takes no `"ask"`: that is a `yolo check` error naming `yolo pack remove github` ([GW-D8](#GW-D8)).

## 3. The working set

The **working set** is the PRs and issues the agent created through the broker under a `create_pr`
or `create_issue` grant, plus the ones a human handed it. The two are one population, treated the same
by every cell ([GW-D39](#GW-D39)). An item leaves when it merges or closes, or on
`yolo github working-set remove 70`; nothing expires, since a created item has no expiry and a
handed-over one is the same population ([OQ-GW10](#decision-ledger)). A reopened item is outside
the set: its next write asks once, and the agent may ask for it again.

**Hand-over.** The **hand-over request** *(coined here)* is the agent asking, by number, for an item to
join the set: `yolo request github working-set 70` ([OQ-GW9](#decision-ledger)), so the agent's side
of every grant is a `yolo request`. It is a broker request, not a config write, so no
`yolo config apply`; the human answers `yolo approve r-<id>` or `--deny`. The approval line is computed from the project's `working_set` cells, so the human reads
exactly what #70 gets here, `merge` and `approve` included when the project holds them
([GW-D47](#GW-D47), [OQ-GW12](#decision-ledger)):

```text
r-9a1  add issue #70 "flaky nightly on darwin" (opened by kurt) to the working set of yolo-jail
       there the agent may, unasked: comment, edit (close, reopen)
       yolo approve r-9a1        yolo approve r-9a1 --deny
```

`yolo approve` answers once, `--for <duration>` (the request's cell on its item, at most 24 hours,
[GW-D10](#GW-D10)) or `--deny`; `--add` and `--remember` are gone ([OQ-GW2](#decision-ledger)), and
entries added with `--add` earlier stay until `yolo approve --revoke w-<id>` ([GW-D34](#GW-D34)).
`yolo github working-set` lists the set with how each item entered; `remove 70` takes one out, unasked.

**Shipped defaults.** A **shipped default** *(coined here)* is a cell yolo writes as present when no
file sets it, reported `shipped default` by `yolo config why`. The working set's four comment and edit
cells ship `true` and need no ceiling token: every member entered under a creation the ceiling allowed
or a hand-over a human approved ([GW-D42](#GW-D42)). They are also the invariant's partner for every
`all.comment_*` or `all.edit_*` grant, so only approve and merge ever take two lines. `approve_prs` and
`merge_prs` ship absent; `all` ships empty. A project narrows a default like any cell.

## 4. How one call resolves

For a `gh` write on item N of repository R, after the fences nothing here moves (refused commands,
the repository scope, the read floor), the tables read are R's: `requests.github` when a git remote
of the workspace names R, the `[repos.<name>]` tables whose name resolves to R otherwise, and
none when R is a named repository with no table, in scope for reading only, in which case every write asks
([§8](#8-which-repository-a-line-is-for)). Then:

1. **Classify.** The argv gives the verb; one state read gives PR or issue and whether N is open. Together
   they name the cell C. Unreadable, or on no row: asks as a repository write.
2. **Pick the table.** N in the working set: `working_set`. Otherwise: `all`. Call it T.
3. **Read T.C in the ask** (local over project, shipped defaults in). `false`: refused, no request
   filed. `"ask"`: asks. `true`: runs, since it passed the ceiling and the gate at load. Absent: step 4.
4. **Read the ceiling's `always`.** A token covering T.C: runs. Otherwise: asks.
5. **A run** is audited with the cell's path and, for a working-set item, the entry's id ([GW-D13](#GW-D13)).
6. **An ask** files a request naming the cell, the item and the commands ([§5](#5-every-stop-names-its-next-step)); a
   timed grant or Allow once on exactly that cell and item covers the retry.

The broker evaluates this under the store lock against the live scope file, as it does `create_pr`.

## 5. Every stop names its next step

Every stop ends with the command that lifts it, alone on its own line, so the agent can relay it.
The last column is that line.

| The stop | What it says | The line it ends with |
| :--- | :--- | :--- |
| The invariant is broken (`yolo check`, launch, `yolo config apply`) | `yolo-jail.toml:9: requests.github.all.merge_prs = true but working_set.merge_prs is not true: the agent would merge everyone's PRs but its own. Grant it, or make all ask:` | `yolo request github working_set.merge_prs`, or `yolo request github all.merge_prs --ask` |
| A cell not held, on a working-set item | `github-broker: approving PR o/r#32 asks here: working_set.approve_prs is not granted (r-7k2m). Once: yolo approve r-7k2m, or --for 15m. To grant it, on the host:` | `yolo allow github on_request working_set.approve_prs`, then here `yolo request github working_set.approve_prs` |
| An item outside the working set | `comment on issue #70 (opened by kurt): not in this workspace's working set, and all.comment_issues is not granted (r-8f2). Once: yolo approve r-8f2. To add it:` | `yolo request github working-set 70` |
| A cell set to `"ask"` | `… asks here: yolo-jail.toml sets requests.github.working_set.comment_prs = "ask" (r-7k2m). Once: yolo approve r-7k2m. To lift the line:` | `yolo config unset requests.github.working_set.comment_prs --project` |
| A cell set to `false` | `approving PR o/r#32 is refused here: yolo-jail.toml sets requests.github.all.approve_prs = false. To allow asking:` | `yolo config unset requests.github.all.approve_prs --project` |
| `yolo check`: a `true` outside the ceiling | `yolo-jail.toml:4: this project requests all.approve_prs; your config does not allow it on request, so this project runs without it. To allow it:` | `yolo allow github on_request all.approve_prs` |
| `yolo check`: a naked verb | `requests.github.approve: there is no approve on its own. To approve any PR:` | `yolo request github all.approve_prs` |
| `yolo check`: `read = "ask"` | `requests.github.read: reads never ask. To keep your jails off GitHub:` | `yolo pack remove github` |
| A merge with `--delete-branch` | `a merge that deletes its branch asks (r-7k2m): merge without --delete-branch, then delete the branch under a git push grant:` | `git push origin --delete <head>` |
| A `gh api` review with an unreadable `event` | `this review's verdict is not on the command line (--input or @file), so it asks as a write to o/r (r-7k2m). Write the verdict plainly:` | `-f event=COMMENT` |
| The launch disclosure, one line per repository | `github-broker: acme/app (origin): create_pr; working set: comment, edit run (shipped default), merge runs; all: comment_issues runs, merge asks; working set: 2 PRs, 1 issue` then `github-broker: acme/infra (repos.infra): dispatch deploy.yml runs; every other write asks` then `github-broker: mschulkind/dotfiles: read only (repos.dotfiles)` | — |
| A write on a repository in scope for reading only | `dispatching sync.yml on mschulkind/dotfiles asks here (r-4c1): yolo-jail.local.toml names it as repos.dotfiles and grants no write on it. Once: yolo approve r-4c1. To grant this one:` | `yolo request repos.dotfiles.github runs sync.yml --local` |
| `yolo check`: a name that is neither a remote nor declared | `yolo-jail.toml:14: [repos.infra.requests.github]: infra is no git remote of this workspace (origin, upstream) and its table has no repo line. Name the repository:` | `yolo request repos infra github.com/acme/infra` |
| `yolo check`: a `repo` with no host | `yolo-jail.toml:3: repos.infra.repo = "acme/infra" names no host; git and GitHub read the same line, so it is written as git names a repository:` | `yolo request repos infra github.com/acme/infra` |
| `yolo check`: a name that reads as every repository | `yolo-jail.toml:3: [repos.all] would read as every repository, so a repository cannot be called that. Pick another name:` | `yolo request repos infra github.com/acme/infra` |
| `yolo check`: `origin`'s GitHub lines under a name | `yolo-jail.toml:7: [repos.origin.requests.github]: GitHub lines for origin live in [requests.github], which is for origin already. Write them there, as:` | `yolo request github create_pr` |
| `yolo check`, a launch: a removed spelling | `yolo-jail.local.toml:8: requests.github.repos is gone: a repository is a [repos.<name>] table with repo = "github.com/OWNER/REPO", named once for git and GitHub alike, and naming it is the request to read it. Convert this file:` | `yolo config migrate` |
| `yolo check`: a request under a name, outside the ceiling | `yolo-jail.toml:16: this project requests runs on acme/infra (repos.infra); your config allows it on request for no repository that admits acme/infra, so this project runs without it. To allow it for every repository, or for this one:` | `yolo allow github on_request runs`, or `yolo allow repos.infra.github on_request runs` after `yolo allow repos infra github.com/acme/infra` |

Advice follows the format of the file it names ([RC-D40](readable-config.md#RC-D40)).

## 6. What people write

Each `true` needs its token in the user's `on_request` (`yolo allow github on_request <path>`), and
every user holds `read`; an `"ask"` needs nothing. The verb that writes a line is in its comment.

```toml
# 1. Default: create PRs and tend them. Working-set comment and edit are shipped defaults.
[requests.github]                 # yolo request github create_pr
create_pr = true

# 2. Triage: every issue runs; a PR outside the working set asks. The shipped defaults are the
#    invariant's partner for comment_issues and edit_issues, so these four lines are all of it.
[requests.github.all]             # yolo request github all.comment_issues; … all.edit_issues
comment_issues = true             # yolo request github all.comment_prs --ask; … all.edit_prs --ask
edit_issues = true
comment_prs = "ask"
edit_prs = "ask"

# 3. Reviewer: comment on and approve any PR. Two approve lines: the invariant's.
[requests.github.working_set]     # yolo request github working_set.approve_prs
approve_prs = true
[requests.github.all]             # yolo request github all.comment_prs; … all.approve_prs
comment_prs = true
approve_prs = true

# 4. Merge mine, ask for others'. With all.merge_prs = true instead, the agent merges every PR,
#    its own included; with working_set.merge_prs = "ask" too, every merge asks, for everyone who
#    clones the project, nobody's y needed; with false, no merge ever runs here. all = true beside
#    working_set = "ask" or false is the error of §5.
[requests.github.working_set]     # yolo request github working_set.merge_prs
merge_prs = true
[requests.github.all]             # yolo request github all.merge_prs --ask
merge_prs = "ask"
```

Persona 3's gate line states the auto-merge reach ([§1](#1-the-cells)). **5. Hand-over of #70** writes
no TOML: the refused comment names `yolo request github working-set 70`, the agent runs it, the host
answers `yolo approve r-9a1`, and the retried comment runs ([§3](#3-the-working-set)).

## 7. Repository-level grants, and what is refused outright

Writes to the repository rather than to a PR or issue never join the working set
([BB-D95](boundary-broker.md#BB-D95)). They ask unless one of these covers them; none is a default
([GW-D48](#GW-D48)). Each is two-layer like a cell: the user's `on_request` lists the word, the project
names the resource, it joins at the gate's `y` with a plain-words line, and it is checked under the
store lock and audited under its own set. A grant that names a resource is `on_request` only. Written
in `[requests.github]` it is for the workspace's own repositories; written in
`[repos.<name>.requests.github]` it is for that one repository and no other
([§8](#8-which-repository-a-line-is-for)).

| Project spelling | Covers | Never covers | Build |
| :--- | :--- | :--- | :--- |
| `runs = ["nightly.yml"]` | `gh workflow run <file>` and the dispatch endpoint, on the default branch, pinned to `refs/heads/<default>`; `gh run rerun <id>` (`--failed`, `--job <id>`, `--debug`) of a run of that workflow whose head is the default branch of the repository itself, read by the broker; `gh run cancel <id>` of any run in the repository | other refs and branches, a fork's run, a workflow by id or display name, a run with no id (the picker), the `gh api` spellings of rerun and cancel | built ([GW-D14](#GW-D14), [GW-D78](#GW-D78)) |
| `dispatch_refs = ["release/*"]` or `["*"]` | a granted workflow on matching branches, pinned to `refs/heads/<name>` | a tag ref; alone it covers nothing, and `yolo check` names `runs` | [GW-D49](#GW-D49) |
| `approve_runs = ["github-actions[bot]"]` | approving a held run the named actor triggered from the repository itself | a fork's run | built ([GW-D15](#GW-D15)) |
| `approve_fork_runs = ["<login>"]` or `["*"]` | approving a held run a named actor triggered from a fork | reruns, cancels, deployment approvals | [GW-D50](#GW-D50) |
| `releases = ["draft"]` or `["draft", "publish"]` | creating and editing releases **on a tag that already exists**; a covered `release create` is sent with `--verify-tag` | a missing tag (asks, naming `git push origin <tag>` under `push_tags`), deletes, asset uploads | [GW-D52](#GW-D52) |
| `labels = true` | `gh label create`, `gh label edit` and their endpoints | `label delete`, `label clone` | [GW-D53](#GW-D53) |
| `projects = ["acme/5"]` | adding this repository's items to board `acme/5` and setting their fields | creating, closing, editing or deleting a board or field; draft items | [GW-D54](#GW-D54) |

Gate lines state the reach plainly: `dispatch_refs` names the branches and no secrets clause (a push
grant already runs any workflow text with the repository's secrets); `approve_fork_runs` names the fork's
read-only token, the Actions minutes and `pull_request_target`; `releases` with `publish` says a
published release is public, notifies watchers and runs the release workflows.

**Still refused under every grant** ([GW-D55](#GW-D55)): a repository's settings, secrets, variables,
deploy keys, rulesets, branch protection and collaborators; archive, transfer and `repo delete`; any
delete (`DELETE`, a `delete` verb, `--delete-last`, `--delete-branch`, `item-delete`); raw GraphQL.

Not covered here: git's working set, whose line is the branch
([`git-broker.md` §5.1](../reference/git-broker.md#the-working-set)), though git takes the
same `[repos.<name>]` tables ([GW-D76](#GW-D76)); the refused commands
([BB-P8](boundary-broker.md#BB-P8)), which no cell or grant widens; the repository scope, which no cell
widens and which a named repository joins only at the gate's `y`, as a remote does
([GW-D71](#GW-D71)); a line per PR number, which `yolo approve` draws in the store.

## 8. Which repository a line is for

Every line in `[requests.github]`, cells and repository-level grants alike, is for the
**workspace's own repositories** *(a plain phrase, used here as [`config-user.md` §6](config-user.md#6-what-a-jail-may-do-with-your-logins)
uses "its own repositories")*: the repositories the workspace's git remotes name on github.com,
and of those, `origin` only ([OQ-GW13](#OQ-GW13)), a row the human approved at the gate. A PR the agent opens on another remote from a branch on `origin`, such as an upstream PR from a fork, counts as `origin`'s `create_pr` and joins the working set, but only its comment and edit lines apply there: a merge on it asks whatever `working_set.merge_prs` says, and a dispatch there asks. To grant more on that repository, the project writes lines for it by name (below). Unqualified `[requests.git]` lines stay what they are today, every remote of the workspace on any host.

Two rules from the owner shape the rest ([GW-D69](#GW-D69), [GW-D70](#GW-D70)): **one way to say
one thing**, and **a repository is named the same way for git and for GitHub**. The owner ruled
the spelling ([OQ-GW15](#OQ-GW15)): a repository is a `[repos.<name>]` table, and its grants live
in it.

**A repository is named once, for every broker** ([GW-D70](#GW-D70), [GW-D76](#GW-D76)). A
repository other than the workspace's own is a top-level `[repos.<name>]` table, and the git broker
and the GitHub broker both read it:

```toml
[repos.infra]                       # yolo request repos infra github.com/acme/infra
repo = "github.com/acme/infra"
```

`repo` is written as git identifies a repository, `<host>/<path>` (the form `gitproto.Identity`
gives), so one line means the same repository to both brokers. A name declared there is a **named
repository** *(coined here)*. A git remote's name, `origin` or `upstream`, is a name too: a
`[repos.<name>]` table with no `repo` line means the repository that remote points at, read from
this clone's `.git/config`. A forge broker yolo adds later reads the same table.

**Naming a repository is asking to read it** ([GW-D71](#GW-D71)). A named repository is in the
workspace's scope for reading, for every broker that serves its host, and for nothing more: on
github.com, `gh` reads through the GitHub broker and `git fetch` through the two exact URLs of
[RC-D26](readable-config.md#RC-D26); on any other host it must also be a remote of the clone,
since a remote is the only way git reaches it, and `yolo check` otherwise names `git remote add`. A
write on it asks until a line grants one. This is what `requests.github.repos` said, so that key
is gone, for every broker: there is no separate read line, because nothing is written on a
repository nobody can read. The top-level `repos` table takes its place, and its name. A yolo
older than the first slice reads only the `requests.github.repos` key for this. Under
[GW-D79](#GW-D79), a repository the project should not reach at all gets no table.

**A repository's grants live in its table** ([GW-D76](#GW-D76)). Under `[repos.<name>]`, a
project writes `requests` and a user config writes `allow`, the same two words as the top-level
tables, each accepted only where its top-level table is (`allow` is user-scope only, `requests`
project and local). Git is the base, since every repository a forge serves is a git repository,
so git's lines sit in the table itself and a forge's lines in a subtable named for its broker:

| Lines | Workspace config, the ask | User config, the ceiling |
| :--- | :--- | :--- |
| git's: `push`, `push_tags`, `new_branches` | `[repos.<name>.requests]` | `[repos.<name>.allow]` (`always`, `on_request`) |
| GitHub's: the cells and the grants of [§7](#7-repository-level-grants-and-what-is-refused-outright) | `[repos.<name>.requests.github]`, with `.working_set` and `.all` below it | `[repos.<name>.allow.github]` (`always`, `on_request`) |

No line applies to both brokers but reading, which the table's name already says, and no line is
git's alone beyond the three above, so there is no `.git` subtable. A key under `requests` or
`allow` is a git word or a broker's name, and yolo keeps the two sets apart: a pack whose broker
would be called `push`, `push_tags`, `new_branches`, `always` or `on_request` is refused. A name
is a TOML bare key (letters, digits, `-` and `_`), so no path ever needs quoting, and it sits one
level below `repos`, so it can collide with no key yolo has or adds. `all` is refused as a name,
because `repos.all` reads as every repository.

This answers the worry the earlier option B raised, that one table would be an ask in a project
file and the ceiling in the user config: the word under the name says which, `requests` or
`allow`, and neither file accepts the other's.

| | Before (GW-D61 to GW-D67) | Now |
| :--- | :--- | :--- |
| A repository to read | `[requests.github]` `repos = ["mschulkind/dotfiles"]` | `[repos.dotfiles]` `repo = "github.com/mschulkind/dotfiles"` |
| A name, for both brokers | `[requests.github.repository.infra]` `repo = "acme/infra"`, and again `[requests.git.repository.infra]` `repo = "github.com/acme/infra"` | `[repos.infra]` `repo = "github.com/acme/infra"`, once |
| A GitHub line on it | `requests.github.repository.infra.runs` | `repos.infra.requests.github.runs` |
| A git line on it | `requests.git.repository.infra.push` | `repos.infra.requests.push` |
| The ceiling for it | `[allow.github.repository.mine]` `repo = "mschulkind/*"` | `[repos.mine]` `repo = "github.com/mschulkind/*"` and `[repos.mine.allow.github]`, in the user config |

**What a name resolves to** ([GW-D70](#GW-D70), [GW-D73](#GW-D73)):

- **A git remote's name** resolves per clone, and each person approves the repository it resolves
  to at their own prompt ([OQ-GW14](#OQ-GW14)). No remote of that name is a `yolo check` error
  naming the remotes and `yolo request repos`; GitHub lines on a remote off github.com are one too.
- **`origin`'s GitHub lines have one home**, the unqualified `[requests.github]`, which is for
  `origin` already ([OQ-GW13](#OQ-GW13)), so `[repos.origin.requests.github]` is refused naming
  it ([GW-D77](#GW-D77)). Git lines under `[repos.origin.requests]` say something the unqualified
  `[requests.git]` cannot, `origin` and no other remote, and are accepted.
- **A declared name** resolves to its `repo`, which always names its host: `repo = "acme/infra"` is
  refused, naming `github.com/acme/infra`. The owner and repository segments may be whole-segment
  wildcards (`github.com/acme/*`, `github.com/*/*`, [WW-D31](workspace-widening.md#WW-D31)), carried
  by a gate line that says in plain words what it admits; the host never is.
- **A declared name that is also a remote's name pins it**: its lines and its read scope go to the
  declared repository, which is how someone whose remote is wrong for the project fixes it in their
  uncommitted `yolo-jail.local.toml` ([OQ-GW14](#OQ-GW14)). The gate row names both repositories
  when they differ. Unqualified lines stay the remote's own.
- **Names belong to the file scope that declares them.** A workspace's names (its project and local
  files merged, the local `repo` winning) serve its requests. A user config's names serve its
  `allow` lines only and put nothing in any workspace's scope, which stays the workspace's ask
  ([OQ-RC16](readable-config.md#decision-ledger)). A workspace line never resolves a user's name, so
  a committed file means the same in every clone.

**The verbs** ([GW-D77](#GW-D77)). The first word after `yolo request` is the key path the line
lands under, with `requests` left out, as `yolo request github` already writes `requests.github`:

| Verb | Writes |
| :--- | :--- |
| `yolo request repos infra github.com/acme/infra [--local]` | `[repos.infra]` `repo = "github.com/acme/infra"` |
| `yolo request repos.infra push main` | `[repos.infra.requests]` `push = ["main"]` |
| `yolo request repos.infra.github runs deploy.yml` | `[repos.infra.requests.github]` `runs = ["deploy.yml"]` |
| `yolo request repos.infra.github all.comment_issues` | `[repos.infra.requests.github.all]` `comment_issues = true` |
| `yolo allow repos mine 'github.com/mschulkind/*'` (host) | the user config's `[repos.mine]` `repo = "github.com/mschulkind/*"` |
| `yolo allow repos.mine.github on_request runs` (host) | the user config's `[repos.mine.allow.github]` `on_request = ["runs"]` |

A line under a name that is neither declared nor a remote is refused, naming
`yolo request repos <name> <host>/<path>`. `repos` is a word of `yolo request` and `yolo allow`
ahead of every source, so a pack cannot be named it.

```toml
# yolo-jail.toml: this project (origin and, in a fork clone, upstream) and one other repository
[requests.github]
create_pr = true                                     # on the workspace's own repository, origin
runs = ["release-request.yml"]         # on it too, default branch

[repos.infra]                                        # yolo request repos infra github.com/acme/infra
repo = "github.com/acme/infra"
[repos.infra.requests]
push = ["main"]                                      # yolo request repos.infra push main
[repos.infra.requests.github]
runs = ["deploy.yml"]                  # yolo request repos.infra.github runs deploy.yml
[repos.infra.requests.github.all]
comment_issues = true                                # the same cells, the same invariant, on acme/infra

[repos.upstream.requests.github]                     # no repo line: this clone's remote "upstream"
labels = true

# yolo-jail.local.toml: read only, since nothing is granted on it
[repos.dotfiles]                                     # yolo request repos dotfiles github.com/mschulkind/dotfiles --local
repo = "github.com/mschulkind/dotfiles"
```

**The ceiling is per repository the same way** ([GW-D64](#GW-D64), spelled by
[GW-D76](#GW-D76)). `[allow.github]` is the ceiling for every repository;
`[repos.<name>.allow.github]`, with its own `always` and `on_request`, adds a ceiling for the
repositories that user-config name admits, and `[repos.<name>.allow]` does the same for git. A
project's request on repository R is within the ceiling when the unqualified lists or any `repos`
table admitting R lists the token; a `repos` table never narrows the unqualified one, so a user
who wants "dispatch on mine, never elsewhere" moves the word from `[allow.github] on_request` to
`[repos.mine.allow.github]`. Names are matched by the repository they admit, never by spelling: a
project's `infra` and the user's `mine` meet only at R.

```toml
# ~/.config/yolo-jail/config.toml
[allow.github]
always = ["read"]
on_request = ["create_pr", "create_issue"]           # any repository a project names

[repos.mine]                                         # yolo allow repos mine 'github.com/mschulkind/*'
repo = "github.com/mschulkind/*"
[repos.mine.allow.github]                            # yolo allow repos.mine.github on_request runs
on_request = ["runs", "approve_runs", "all.merge_prs"]   # only under my own account
```

**Scope** ([GW-D63](#GW-D63), [GW-D71](#GW-D71)). A named repository is in the approved scope in
its own right, for each broker that serves its host: a row in each broker's scope block under
`repos.<name>` and its file, joining at the `y` like a remote. Its grants are the ones its table
holds, fanned out over the repositories its name resolves to, and over nothing else; a remote's
repository gets the unqualified lines and, when a `repos` table names that remote, that table's
lines too.

**The cells and the working set** ([GW-D65](#GW-D65)). `[repos.<name>.requests.github.working_set]`
and `.all` are the same six cells, and the invariant ([GW-D58](#GW-D58)) holds within each
repository's pair. Items are already keyed by repository, so the working set changes only in
spelling: `yolo request github working-set acme/infra#70` names the item's repository, and a bare
`working-set 70` is accepted only when the workspace's own repositories are one. A covered write is
audited with the full key, `repos.infra.requests.github.all.comment_issues`.

**Git on a named github.com repository** ([GW-D70](#GW-D70)). A named repository no remote names
joins git's scope as [RC-D26](readable-config.md#RC-D26)'s two URLs; a `[repos.<name>.requests]`
line on it is a push grant there, because the project named the repository and wrote the line. A
named repository with no git line stays read only in git, as RC-D26 holds today.

**Today's files** ([GW-D75](#GW-D75), spelled by [GW-D77](#GW-D77)). Built for the `github`
source in the first slice, within [GW-D80](#GW-D80)'s bounds; a yolo older than that slice still
accepts `requests.github.repos` and reads nothing else. From that build, the
removed spellings are refused, each naming its replacement, and none is kept as an alias, as the owner
ruled for renames
([MT-R3](named-setups.md#decision-ledger)). `requests.github.repos` (and `repos` in any
source's table) is refused naming `[repos.<name>]` `repo = "github.com/OWNER/REPO"` and
`yolo config migrate`; `yolo request github repos` is refused naming `yolo request repos`. The
`repository` segment, `[repositories]` and `for`, spellings this doc proposed and no build
shipped, need no refusal. `yolo config migrate` converts a file already in TOML too: each `repos`
entry becomes a `[repos.<name>]` table in the same file, the name the repository's last segment
(`owner-repo` on a tie, as pack namespaces do). A legacy `brokered.<source>.grants` entry converts
to the unqualified key when its `repo` is the workspace's sole remote, as today, otherwise to the
`repos.<name>` table's line with the name declared; `"*"` becomes the unqualified line plus the
same line under each name the workspace declares, exactly the repositories it covered. The grant
set the brokers resolve is proved equal before and after, as [RC-D55](readable-config.md#RC-D55)
proves. This repository's own `yolo-jail.local.toml` keeps `requests.github.repos` until the host's
yolo is one that reads the tables; changing that file is the owner's step, and no build does it.

**What the gate prints** ([GW-D68](#GW-D68)). Every grant line ends with where it came from, the
key and the file, so the provenance a human reads is never a guess:

```text
github-broker repository scope, read from this workspace's .git/config, yolo-jail.toml and yolo-jail.local.toml:
    github.com/mschulkind-oss/yolo-jail   remote origin                        unchanged
  + github.com/acme/infra                 repos.infra (yolo-jail.toml)           added
    github.com/mschulkind/dotfiles        repos.dotfiles (yolo-jail.local.toml)  unchanged, read only
  + grant runs github.com/acme/infra deploy.yml  added (yolo-jail.toml: repos.infra.requests.github.runs)
    grant runs github.com/mschulkind-oss/yolo-jail release-request.yml  unchanged (yolo-jail.toml: requests.github.runs, on remote origin)
    grant read *  unchanged (~/.config/yolo-jail/config.toml: allow.github.always, for every workspace)
git-broker repository scope, read from the same files:
  + github.com/acme/infra                 repos.infra (yolo-jail.toml)           added
  + grant branch github.com/acme/infra main  added (yolo-jail.toml: repos.infra.requests.push)
    github.com/mschulkind/dotfiles        repos.dotfiles (yolo-jail.local.toml)  unchanged, read only
```

### Current support and the repository-only first slice

**Status.** The section above this subsection is the ruled target contract ([OQ-GW13](#OQ-GW13),
[OQ-GW15](#OQ-GW15), [GW-D76](#GW-D76), [GW-D77](#GW-D77)). The slice below is built
(2026-11-12, the series ending `0121997f1`): `[repos.<name>]` tables and their GitHub lines,
origin-only lowering, the scoped ceiling, the verbs, the refusal of `requests.<source>.repos` and
its migration. Not built: the work-kind cells, and Git lines under a name, which are refused.
MEASURED: the unit and integration suites, and one nested launch that accepted a named table.
UNMEASURED: native macOS, rootless reachability, and a GitHub call under live authority.
[The plan's verification record](github-repository-binding-plan.md#verification-record) has the
commands and limits. Built is a statement about the source: a release, a host install, and any
change to a grant or a config file are separate steps. Other items keep the status the Decision
Ledger's Built column gives; the `runs` operation set ([GW-D78](#GW-D78)) is built.

**Before the slice.** This is what a yolo older than the slice does. The clone's GitHub scope is
every git remote that names github.com, plus each repository
listed in `requests.github.repos`. Every other `[requests.github]` line, `runs = ["*"]` and
`approve_runs` included, is requested for *every* repository in that scope. Scope narrows only by
removing a remote or by not listing an extra repository; a remote stays in scope whether or not
anything lists it. So two repositories in scope cannot get different lines, and a project that
wants writes on one repository only must, today, keep the other out of scope.

**The slice.** Build the GitHub side of repository binding first, ahead of the work-kind cells and
the Git tables. The repository declaration stays one shared `[repos.<name>]` table for both brokers
([GW-D70](#GW-D70)): this slice adds no GitHub-only naming, hard-codes no pack, grants no Git push
implicitly and adds no Git per-repository write table. Git's read behavior for named github.com
repositories stays [RC-D26](readable-config.md#RC-D26). Git per-repository write tables and the
work-kind cells wait.

1. **Origin-only lowering** ([OQ-GW13](#OQ-GW13)). Each unqualified `[requests.github]` line,
   list or boolean, lowers to grants on one *concrete identity*, the `<host>/<path>` that `origin`
   resolves to, and on no other target.
2. **Named tables** ([OQ-GW15](#OQ-GW15)). `[repos.<name>]` with `repo` puts a repository in scope
   and requests read, as [RC-D26](readable-config.md#RC-D26) defines it. Writes there come only
   from lines in its own `[repos.<name>.requests.github]`.
3. **Scoped ceiling.** User `[repos.<name>.allow.github]` meets requests by concrete identity and
   adds to the broad `[allow.github]`.
4. **Migration for `requests.github.repos`**, as [GW-D75](#GW-D75) rules it.

Unqualified `requests.git`, repository wildcard patterns and the `runs` operation set
([GW-D78](#GW-D78)) are unchanged.

**Case: every workflow on origin, nothing elsewhere.** The project file needs no `repos` table:

```toml
# yolo-jail.toml: origin only, by rule rather than by omission
[requests.github]
runs = ["*"]
approve_runs = ["github-actions[bot]"]
```

Those lines read the same as in the current format. What changes is their reach: a repository
added later, in any layer, cannot inherit them. To make the host ceiling exact as well, remove the
capability from the broad ceiling and state it for one identity:

```toml
# user config: remove runs and approve_runs from [allow.github], then
[repos.jail]
repo = "github.com/mschulkind-oss/yolo-jail"

[repos.jail.allow.github]
on_request = ["runs", "approve_runs"]
```

**General example: other repositories.** This is not this repository's configuration. `notes` is
read-only by default. `tools` asks for one workflow, which still passes the host gate and needs a
ceiling that covers `github.com/example/tools`:

```toml
[repos.notes]
repo = "github.com/example/notes"

[repos.tools]
repo = "github.com/example/tools"

[repos.tools.requests.github]
runs = ["lint.yml"]
```

**Behavior.**

- **Wildcard containment.** `runs = ["*"]` means every workflow within the one identity its table
  lowers to. It never selects repositories.
- **Layers.** Lists still merge by union, per table. A local file can add a named table, which
  adds a read-only target, or add lines to a table. Adding a table or lines never changes which
  repository another table's lines reach. Only a scalar `repo` does, as the next item says.
- **Retargeting.** A scalar `repo` in any layer that retargets a name, or an `origin` remote that
  now resolves elsewhere, is a new identity. The gate shows old and new identity and asks again;
  earlier approval does not follow the name.
- **Provenance.** The gate lists each lowered grant with its identity and the layer and table that
  asked for it.
- **Gate and ceiling.** No project line grants anything by itself. A request above the ceiling is
  skipped and reported, as now.

**Acceptance checks.**

| Check | Expected |
|---|---|
| A second repository is in scope and origin has `runs = ["*"]` and `approve_runs` | No `runs` or `approve_runs` grant is lowered for the second identity |
| An unqualified GitHub line where `origin` is absent or not on `github.com` | Error, not a skip. It names the next step: correct or add an `origin` remote on `github.com`, or, if no origin target is meant, move the lines to a `[repos.<name>]` table with its own `repo` and qualified requests. Pinning `[repos.origin] repo` does not help, since unqualified lines follow the `origin` remote. An unqualified line never falls back to another repository |
| A local layer adds a named table | A read-only target is added; origin's grants are unchanged, and the new target has no writes |
| `origin` or a named `repo` is retargeted | The gate shows the identity difference and asks for approval again |
| A file with `requests.github.repos` is migrated | Each grant that today reaches the listed repository is written as an explicit line in its named table and printed; the before and after grant sets are equal |
| An unknown key under any `repos.<name>` table | Validation refuses it |
| A request above the ceiling | Skipped and reported; other grants are unaffected |

**Migration note.** Today's fan-out means an equal migration writes the origin lines again under
each listed repository. That follows [GW-D75](#GW-D75): nothing is widened and nothing is dropped
silently. The migration prints every retained named write with its identity and the file and key
it came from, so the owner can narrow it on purpose. Before approval, the owner uses the
configuration verbs to remove retained write requests, or unwanted repository scope. This slice
does not make narrowing the default; that would reverse a ruling. A legacy `"*"` grant covers
repositories not yet declared, so copying it to the currently declared names is not proved equal
to it, and this design claims no equality for it. A legacy pattern `brokered.<source>.repos`
entry, and a raw grant whose `repo` is a pattern, are in the same case: an older gate recorded
them on a pattern that matched no command, and a table would match one, so no table is equal to
them. The build bounds the case ([GW-D80](#GW-D80)): such a grant or entry is not converted, and
its old file is kept and still read, with its reason and next step. This workspace's `requests.github.repos` is not migrated by this design,
and no migration is performed.

**Stored approvals.** Checked against the tree for the plan
([Task 4](github-repository-binding-plan.md#task-4-stored-authority-regression-tests-after-3-test-only)):
the approval record is only the gate's comparison baseline and is replaced at each approval, so a
grant no longer lowered for an identity never reaches a broker from it. A running jail keeps the
scope it was approved with until a fresh launch or `yolo config apply`; editing a file revokes
nothing live. A human's stored per-command and per-resource answers are keyed by concrete
identity and stay subject to the live scope and the read grant.

**Bounds of the slice** ([GW-D80](#GW-D80)), each fail-closed and none a new grant:

- Origin-only lowering applies to a broker whose declaration opts in; the shipped `github`
  broker does. Another named-host source keeps today's reach for unqualified lines; the
  `[repos.<name>]` tables serve every named-host source.
- A pattern `repo` lowers one grant per line with the pattern as its repository, marked as a pattern, and the broker matches a marked grant by whole segments behind the scope and read floor. A grant an older gate recorded on a pattern matched no command and still matches none; a pattern grant covers only when the gate approved it as one, from a named table, and a new one shows at the gate as added and asks. A user `repo` is required.
- A scoped ceiling lists write capabilities. Read stays in `[allow.<broker>] always`, and is
  refused under a name, naming that line.
- Git lines under a name are refused, naming `yolo request git`.
- The migration preserves the writes on listed repositories and on remotes other than `origin`,
  the latter as lines of a table named for the remote. A workspace with no old spelling is not
  rewritten; there the other remotes' loss shows at the gate as removed ([OQ-GW13](#OQ-GW13)).

## Open Questions

None open. The ruled questions are in the [Decision Ledger](#decision-ledger).

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="GW-D2"></a>GW-D2 | A review's verdict is read only from a `pr review` flag or a plainly written `-f`/`-F` `event` field; `--input` or an `@file` value leaves it unreadable, and the write asks as a repository write | 2026-10-30 | [§1](#1-the-cells) | — |
| <a id="GW-D3"></a>GW-D3 | `pr ready`, with or without `--undo`, is an edit | 2026-10-30 | [§1](#1-the-cells) | — |
| <a id="GW-D4"></a>GW-D4 | `pr review --request-changes`, and the `REQUEST_CHANGES` event, are an edit; a comment changes nothing but adds words | 2026-10-30 | [§1](#1-the-cells) | — |
| <a id="GW-D5"></a>GW-D5 | Every `pr merge` invocation is merge, `--auto` and `--disable-auto` included; `--delete-branch` is excepted by [GW-D56](#GW-D56) | 2026-10-30 | [§1](#1-the-cells) | — |
| <a id="GW-D7"></a>GW-D7 | The local file merges after the project file for `requests.github`, as for every key; the last file wins per cell | 2026-10-30 | [§2](#2-the-ceiling-and-the-ask) | — |
| <a id="GW-D8"></a>GW-D8 | `requests.github.read = "ask"` is a `yolo check` error naming `yolo pack remove github`; `packs` has no per-project form | 2026-10-30 | [§2](#2-the-ceiling-and-the-ask) | — |
| <a id="GW-D10"></a>GW-D10 | A timed grant (`--for`, at most 24 hours) covers the request's cell on its item and no other cell | 2026-10-30 | [§3](#3-the-working-set) | — |
| <a id="GW-D13"></a>GW-D13 | A covered write is audited with the covering cell's path and, for a working-set item, the entry's id; the launch disclosure names the cells that run and the cells that ask, marking shipped defaults | 2026-10-30 | [§5](#5-every-stop-names-its-next-step) | — |
| <a id="GW-D14"></a>GW-D14 | **Renamed and widened by [GW-D78](#GW-D78):** the kind is `runs`, which also covers reruns and cancels. **Maintainer's ask:** *"I want you to improve the broker so dispatch is possible. then we'll modify my config to allow it, relaunch, and have you dispatch it."* The `dispatch-workflow` grant names a repository and one workflow file name (or `*`); readable spelling `allow.github.on_request = ["dispatch_workflows"]`, `requests.github.dispatch_workflows = ["<file>"]`. It covers only `gh workflow run <file>` and `gh api` POST `repos/{o}/{r}/actions/workflows/{file}/dispatches` with no query or fragment, no `--input`, and exactly one field rooted at `ref`, on the repository's default branch only (`--ref` omitted, the branch's name, or `refs/heads/<name>`, read by the broker's own `gh api repos/{o}/{r}`); the argv that runs is pinned to `refs/heads/<default>`, so neither gh's own lookup nor a tag of the same name can change it. A workflow by id or display name, other refs (unless [GW-D49](#GW-D49)), `run rerun`, `run cancel`, `workflow enable`/`disable` ask. Authorized under the store's transaction lock with the grant re-read, audited under `dispatch-workflow`. Residual: the default-branch read is just before the transaction, and a workspace that may push the default branch can change the workflow it dispatches | 2026-10-31 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | built |
| <a id="GW-D15"></a>GW-D15 | **Maintainer's ask:** approve the runs GitHub holds at `action_required` (the weekly `flake.lock` bump PR `github-actions[bot]` opens from the repository's own branch) without a per-call host approval. The `approve-run` grant names a repository and an actor, a GitHub login (optionally `[bot]`) or `*`; readable spelling `allow.github.on_request = ["approve_runs"]`, `requests.github.approve_runs = ["<login>"]`, written by `yolo request github approve_runs <login>`. It covers exactly `gh api` with a stated POST of `repos/{o}/{r}/actions/runs/{id}/approve`, numeric id, no query, field or `--input`; before running it the broker reads the run and covers it only when `triggering_actor.login` matches (case-insensitively, or `*`) **and** `head_repository.full_name` is the repository itself. A fork's run, a failed read, another run or an unnamed actor asks, saying which, and names `yolo request github approve_runs <actor>` then `yolo config apply`. Authorized under the store lock with the grant re-read, audited under `approve-run`. Residual: the run read is just before the transaction | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | built |
| <a id="OQ-GW1"></a>[OQ-GW1](#OQ-GW1) | **Ruled A:** four work kinds, comment, edit, approve, merge; they survive as the verb half of each cell ([GW-D41](#GW-D41)) | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="OQ-GW2"></a>[OQ-GW2](#OQ-GW2) | **Ruled:** `yolo approve --add` is removed; an item the agent did not create is reached by Allow once, by `--for`, or by the agent's own request to join the working set, which a plain `yolo approve` answers. Owner: *"can't the agetn request it to be added to the working set somehow?"*, then *"yes that sounds good"*; on `--add`: *"It rides along with a grant, or it rides along with an approval, and depending on what the approval is, it does different things, and you're trying to guess context. … Let's remove it."* A handed-over item gets what the `working_set` cells say ([GW-D39](#GW-D39), [GW-D47](#GW-D47)) | 2026-11-02 | [§3](#3-the-working-set) | removal built 2026-11-06 ([GB-D28](../reference/git-broker.md#GB-D28) carries the refusal): the pending write's menu (`ghbroker.pendingMenu`), the github pack's briefing and the github-broker's description no longer offer `--add`, and `yolo approve <id> --add` is refused naming Allow once and `--for`. Pinned by `TestApproveRefusesTheRemovedAddAndRememberFlags`, `TestPendingMessageListsEveryAnswerOnItsOwnLine`, `TestApproveAddAndForCoverTheNextWrite`, `TestNoShippedPackOffersTheRemovedApproveFlags`. The hand-over request that replaces it, `yolo request github working-set <n>` ([OQ-GW9](#OQ-GW9)), is not built |
| <a id="GW-D34"></a>GW-D34 | Working-set entries a human added with `--add` before its removal are kept until revoked, listed by `yolo approve` with their origin, and ended by `--revoke w-<id>`; the store records what was added, not how, so nothing migrates | 2026-11-02 | [§3](#3-the-working-set) | built 2026-11-06: entries a human added are kept and listed with their origin, and `--revoke w-<id>` ends one; pinned by `TestApproveGitHubForCLI` (a lasting entry written as the old `--add` did, listed and revoked) |
| <a id="OQ-GW3"></a>[OQ-GW3](#OQ-GW3) | **Ruled B:** `create_pr` and `create_issue` carry comment and edit on what they create; a project narrows with `"ask"`. Owner: *"agents should be able to comment on edit on PRs they create. We can just specify that as part of create PR."* Expressed as the shipped defaults of [GW-D42](#GW-D42) | 2026-11-02 | [§3](#3-the-working-set) | — |
| <a id="OQ-GW4"></a>[OQ-GW4](#OQ-GW4) | **Ruled A:** the narrowing is `"ask"` in `requests.github`; `true` requests, absent leaves it to the user; the value sits on a cell ([GW-D45](#GW-D45)) | 2026-11-02 | [§2](#2-the-ceiling-and-the-ask) | — |
| <a id="OQ-GW5"></a>[OQ-GW5](#OQ-GW5) | **Ruled B:** an approval or a ready on a PR with armed auto-merge is classified by its argv and is not a merge; the approve gate line states that an approval can complete an armed auto-merge. Owner: *"it's less surprising really"* | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="OQ-GW6"></a>[OQ-GW6](#OQ-GW6) | **Ruled A:** the user's `always` may hold a cell; each workspace's gate still shows it, and a project narrows with `"ask"`; the token is a cell path ([GW-D44](#GW-D44)) | 2026-11-02 | [§2](#2-the-ceiling-and-the-ask) | — |
| <a id="GW-D36"></a>GW-D36 | **Owner's ruling:** two tables of cells, `[requests.github.working_set]` and `[requests.github.all]`, named for the sets rather than their members. Owner: *"if we stop trying to come up with a name for the things that go into the working set, we can just like literally say working set permissions ... and then all. And then it's clear that the working set has a special set, and then all is things that aren't in the working set."* | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="GW-D37"></a>GW-D37 | **Owner's ruling (R1):** every key reads cold, without knowing another key exists. Owner: *"problem with comment_others is you need to know that comment_own exists to know what it means."* | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="GW-D38"></a>GW-D38 | **Owner's ruling (R2):** no key implies or changes another. Owner: *"setting issues to ask shouldn't rely on you knowing the default and maintaining for other bits that are implied."* The invariant ([GW-D58](#GW-D58)) refuses a file rather than implying the missing line, to keep R2 | 2026-11-02 | [§2](#2-the-ceiling-and-the-ask) | — |
| <a id="GW-D39"></a>GW-D39 | **Owner's ruling (R3):** items the agent created and items handed to it by an approved request are one population, treated identically by every cell. Owner: *"1 and 2 should be handled identically. otherwise we have 3 types of things, no?"* | 2026-11-02 | [§3](#3-the-working-set) | — |
| <a id="GW-D40"></a>GW-D40 | **Owner's rulings:** no naked verb and no `_own`/`_any` asymmetry; `comment`, `edit`, `approve`, `merge` alone, `*_any`, `*_own` and `merge = true` are `yolo check` errors naming the cell, with no migration. Owner: *"I don't even understand what the naked approve and comment would even be. So I think that says we should remove them."*; *"I don't like that asymmetry."* | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="GW-D41"></a>GW-D41 | Six cells, verb plus suffix: `comment_prs`, `comment_issues`, `edit_prs`, `edit_issues`, `approve_prs`, `merge_prs`, classed by argv and the item's own PR-or-issue state; each takes `true`, `"ask"` or `false` ([OQ-GW8](#OQ-GW8)) or is absent; approve and merge have no `_issues` cell; no cell implies another. Amended by [GW-D59](#GW-D59): `approve_prs` sits in both tables | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="GW-D42"></a>GW-D42 | The working set's `comment_prs`, `comment_issues`, `edit_prs` and `edit_issues` ship `true` as present cells, reported `shipped default`, needing no ceiling token since every member entered under a ceiling-allowed creation or a human-approved hand-over; `approve_prs` and `merge_prs` ship absent and `all` ships empty. Carries [OQ-GW3](#decision-ledger) | 2026-11-02 | [§3](#3-the-working-set) | — |
| <a id="GW-D43"></a>GW-D43 | **Superseded by [GW-D59](#GW-D59).** There was no `working_set.approve_prs`, on the ground that one key would mean two behaviors (GitHub refuses the agent's own; a handed-over one is a verdict on another's work) | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="GW-D44"></a>GW-D44 | A ceiling token is a cell's config path (`"all.merge_prs"`), or a table alone for every cell in it (`"working_set"`, `"all"`); `all.X` covers `working_set.X`, which [GW-D58](#GW-D58) requires; `yolo allow github [on_request] <path>` writes it | 2026-11-02 | [§2](#2-the-ceiling-and-the-ask) | — |
| <a id="GW-D45"></a>GW-D45 | **Amended by [GW-D57](#GW-D57)**, which picks the table first. Within the picked table: the ask's cell when present, else the ceiling's `always`, else ask. `"ask"` asks, from any file, with no ceiling and no `y`; `true` runs only within the ceiling and after the gate's `y`, and a `true` outside the ceiling counts as absent | 2026-11-02 | [§4](#4-how-one-call-resolves) | — |
| <a id="GW-D46"></a>GW-D46 | `yolo request github <table>.<cell> [--ask] [--local]` writes a cell; `yolo config set requests.github.<table>.<cell> ask` is the same edit, and advice names `yolo request` | 2026-11-02 | [§2](#2-the-ceiling-and-the-ask) | — |
| <a id="GW-D47"></a>GW-D47 | The hand-over request is a broker request filed by the agent naming one item number, answered by a plain `yolo approve` or `--deny`, writing no config; its approval line lists the cells the item would get, computed from the project's `working_set` cells. Spelling: [OQ-GW9](#OQ-GW9) | 2026-11-02 | [§3](#3-the-working-set) | — |
| <a id="GW-D48"></a>GW-D48 | **Owner's rulings:** repository-level and wide grants are opt-in, never default: *"None of these wildcard everythings whatever should be grants by default, but somebody's going to want them."*; *"approving/merging PRs and apprving a fork's runs shoul all be grantable if allowed. as well as releases."* Each is two-layer, a resource-taking one `on_request` only, joining at the gate with a plain-words line, checked under the store lock, audited under its own set | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D49"></a>GW-D49 | `dispatch_refs`, a list of branch names or patterns, widens a `runs` grant from the default branch to matching branches, pinned to `refs/heads/<name>`; never a tag; alone it covers nothing; its gate line has no secrets clause. Owner: *"can't it already do this with perms to push to literally any branch?"* | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D50"></a>GW-D50 | `approve_fork_runs = ["<login>" or "*"]` covers the run-approve endpoint when the run read shows a fork head and a matching actor; `approve_runs` keeps the repository's own heads; the gate line names the read-only token, the minutes and `pull_request_target` | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D51"></a>GW-D51 | **Superseded by [GW-D78](#GW-D78):** reruns and cancels are part of the `runs` grant, not words of their own. `rerun_runs = true` covers `gh run rerun <id>` and the `rerun`, `rerun-failed-jobs` and `jobs/{id}/rerun` endpoints with a numeric id, stated method, no field and no `--input`; `cancel_runs = true` covers `gh run cancel <id>` and its endpoint; separate words | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D52"></a>GW-D52 | `releases = ["draft"]` or `["draft", "publish"]`, `publish` not implying `draft`, covers creating and editing releases only on a tag one read finds, with `--verify-tag` added to a covered `release create`; a missing tag asks naming `git push origin <tag>` under `push_tags`; deletes and asset uploads never | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D53"></a>GW-D53 | `labels = true` covers creating and editing the repository's labels; never `label delete` or `label clone` | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D54"></a>GW-D54 | **Owner's ruling:** offer projects. `projects = ["<owner>/<number>"]` covers adding this repository's items to a granted board, `item-edit` and `item-archive` on it, and `--add-project`/`--remove-project` after one read resolves the title to a granted board; never board or field create, close, edit, delete, copy or link, nor draft items | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D55"></a>GW-D55 | Still refused under every grant: settings, secrets, variables, deploy keys, rulesets, branch protection, collaborators, archive, transfer, `repo delete`, any delete, raw GraphQL | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D56"></a>GW-D56 | No cell covers a delete: `pr merge --delete-branch` and `pr close --delete-branch` ask, naming the write without the flag and a git `push` grant for the branch delete | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="GW-D57"></a>GW-D57 | **Owner's ruling (confirmed 2026-11-03 through [OQ-GW7](#OQ-GW7)), from the owner's model of 2026-11-02:** pick the table first. A write on a working-set item reads `working_set` only; a write on any other item reads `all` only; neither table is a fallback for the other. Amends [GW-D45](#GW-D45) | 2026-11-02 | [§4](#4-how-one-call-resolves) | — |
| <a id="OQ-GW7"></a>[OQ-GW7](#OQ-GW7) | **Owner's ruling: B.** Only the working-set table decides for a working-set item; `all` decides for every other item; neither falls back to the other ([GW-D57](#GW-D57)). Owner: *"the more specific governs. … Working set is clearly a subset of all, so working set covers working set. All covers things that aren't working set."* So making every merge ask, the agent's own PRs included, takes both `all.merge_prs = "ask"` and `working_set.merge_prs = "ask"` | 2026-11-03 | [§4](#4-how-one-call-resolves) | — |
| <a id="GW-D58"></a>GW-D58 | **Owner's ruling, confirmed and widened:** the invariant. The values order `false` < `"ask"` < `true`, an absent line read as `"ask"`, and for every cell X `working_set.X` below `all.X` is a fatal config error: `"ask"` or `false` under `all.X = true`, and `false` under `all.X = "ask"` or an absent `all.X` (a shipped default counts). Checked at config load on the workspace's merged tables, so `yolo check`, a launch and `yolo config apply` refuse the same file, naming both fixes; the ceiling satisfies it by construction ([GW-D44](#GW-D44)). Owner, 2026-11-02: *"a check to make sure that at least the minimum of the all permissions are specified in the working set permissions. Otherwise ... that's probably a fatal error because somebody probably made a mistake there."* 2026-11-03: *"yes I want it. error if working_set < all in any way."* | 2026-11-03 | [§2](#2-the-ceiling-and-the-ask) | — |
| <a id="GW-D59"></a>GW-D59 | `working_set.approve_prs` exists (ruled with [OQ-GW11](#decision-ledger)), so the two tables hold the same six cells and the invariant needs no special case; it ships absent. On the agent's own PR GitHub refuses the approval; on a handed-over PR the hand-over line named `approve` before the `y`. Supersedes [GW-D43](#GW-D43) | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="OQ-GW11"></a>[OQ-GW11](#OQ-GW11) | **Ruled A:** the working set has its own `approve_prs` line, absent by default, so both tables hold the same six cells and the invariant needs no special case; the hand-over prompt shows `approve` before the human's `y` ([GW-D59](#GW-D59)). Owner, taking the leaning: *"the hand-over prompt shows approve before the y, and same six lines in both tables means no special case."* | 2026-11-02 | [§3](#3-the-working-set) | no |
| <a id="OQ-GW12"></a>[OQ-GW12](#OQ-GW12) | **Ruled A:** a handed-over PR merges unasked when the project holds `working_set.merge_prs = true`, the same as the agent's own; `--deny` at the hand-over or `working_set.merge_prs = "ask"` are the ways out. Owner, taking the leaning: *"the hand-over prompt already shows merge before the y, so a second kind of item buys nothing."* | 2026-11-02 | [§3](#3-the-working-set) | no |
| <a id="OQ-GW8"></a>[OQ-GW8](#OQ-GW8) | **Ruled A:** a cell may be `false`, a refusal that beats the ceiling and `always`, filing no request and naming the line to remove; it orders below `"ask"`, so `all.X = true` with `working_set.X = false` breaks the invariant. Owner: *"Add false, as a refusal that beats the ceiling: it is one more value, and it is the only way to say never."* | 2026-11-02 | [§2](#2-the-ceiling-and-the-ask) | no |
| <a id="OQ-GW9"></a>[OQ-GW9](#OQ-GW9) | **Ruled A:** the hand-over request is `yolo request github working-set <n>`. Owner: *"yolo request github working-set 70: the agent's side of every grant is filing a request, and the host's is yolo approve."* | 2026-11-02 | [§3](#3-the-working-set) | no |
| <a id="OQ-GW10"></a>[OQ-GW10](#OQ-GW10) | **Ruled A:** an item leaves the working set on merge or close, or on `yolo github working-set remove <n>`; nothing expires; a reopened item asks once and may be requested again. Owner: *"A: on merge or close, or an explicit remove; no expiry, since a created item has none and a handed-over one is the same thing."* | 2026-11-02 | [§3](#3-the-working-set) | no |
| <a id="GW-D60"></a>GW-D60 | **Superseded in part by [GW-D71](#GW-D71):** the unqualified line's reach stands; read scope is now a named repository, and `repos` is gone. **An unqualified `requests.github` line is for the workspace's own repositories only: the repositories its git remotes name on github.com, never a `requests.github.repos` entry.** A `repos` entry is read scope, as its spelling in this repository's local file promises (*"Pushes are never widened by a requested repository"*) and as git already holds ([RC-D26](readable-config.md#RC-D26)); a write on one asks, naming `yolo request github repository <name> <OWNER/REPO>`. Replaces the lowering that fanned every request over every target (`ProduceReadableGateInput`), which gave `mschulkind/dotfiles` four dispatch grants on 2026-11-02. Which of the remotes the unqualified line covers is [OQ-GW13](#OQ-GW13) | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D61"></a>GW-D61 | **Superseded by [GW-D76](#GW-D76):** a repository's lines live in its `[repos.<name>]` table, under `requests`, and `requests.github.repos` is gone. **Owner's ask, shaped:** *"somebody's gonna want to grant things across multiple repositories … you'll have some name you can define somewhere. And then that name will be part of the path to these grants."* The path is `requests.github.repository.<name>.<key>`, the fixed segment `repository` then the name, never the name alone as the owner feared (*"that does create collision risks"*): a bare name would collide with `all`, `working_set`, `repos`, every capability word and every one yolo adds later, which would silently change a user's name's meaning; a fixed segment makes the collision impossible instead of reserved against. `repository`, not `repo`, because the repo treats a singular spelling beside a plural key as a typo (`singularRootKeys`) and `repos` stays. A named table takes exactly the keys `requests.github` takes, `repos` and `repository` excepted, each meaning on that repository what it means unqualified ([GW-D37](#GW-D37): every key reads cold at either depth). A name is a TOML bare key (letters, digits, `-`, `_`) so no path is ever quoted, and may not be a key `requests.github` takes (`all`, `working_set`, `repos`, `repository`, a capability word), which `yolo check` refuses naming another name | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D62"></a>GW-D62 | **Superseded by [GW-D70](#GW-D70), [GW-D73](#GW-D73) and [GW-D74](#GW-D74):** a name is declared once in `[repos.<name>]` with `repo = "<host>/<path>"`, a declaration with a remote's name pins it rather than being refused, and the verbs changed. A name is a git remote's name or a declared one. A remote's name (`origin`, `upstream`) is a name with nothing written: it means the repository that remote points at on github.com, read from this clone's `.git/config`; a remote on another host or no remote of that name is a `yolo check` error naming the remotes and the `repo` line. A declared name has `repo = "OWNER/REPO"`, or a whole-segment wildcard in [WW-D31](workspace-widening.md#WW-D31)'s forms, whose gate line says in plain words what it admits, as [GB-D25](../reference/git-broker.md#GB-D25) does for a `*` ref grant; the owner on wildcards: *"if someone wants to grant repo \*, why should we stop them?"*. A declared name equal to a remote's name is accepted when both name the same repository and refused when they differ, naming the rename or the `repo` line to drop. `yolo request github repository <name> <OWNER/REPO>` declares; `yolo request github repository.<name>.<key> [resource]` writes under it, the dotted form of [GW-D46](#GW-D46) | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D63"></a>GW-D63 | **Superseded in part by [GW-D71](#GW-D71):** the scope row is `repos.<name>`, in each broker's block, and there is no `repos` entry. A named repository is a scope entry in its own right: a row in the gate's scope block under `requests.github.repository.<name>` and its file, joining at the `y` as a remote or a `repos` entry does, with no second `repos` line needed or implied (the scope is computed from remotes, `repos` and names; no key changes another, [GW-D38](#GW-D38)). Its grants are its table's, fanned out over the repositories the name resolves to and nothing else; a remote's repository gets the unqualified lines plus any table naming that remote. [OQ-BB18](boundary-broker.md#OQ-BB18) is unchanged: which repositories stays a workspace request, the access level stays the user's ceiling | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D64"></a>GW-D64 | **Spelling superseded by [GW-D76](#GW-D76) and [GW-D77](#GW-D77):** `[repos.<name>.allow]` and `[repos.<name>.allow.github]`, the name declared in the user config's `[repos.<name>]`; the ceiling's rule stands. The ceiling is per repository by the same table: `[allow.github.repository.<name>]` with `repo` (always declared, since a user config has no remotes) and its own `always` and `on_request`, written by `yolo allow github repository <name> <OWNER/REPO or wildcard>` and `yolo allow github repository.<name> [always|on_request] <token>`. The unqualified `[allow.github]` is the ceiling for every repository; a qualified table adds for the repositories its `repo` admits and never narrows. A request on R is within the ceiling when either lists the token. A skipped request names both commands | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D65"></a>GW-D65 | **Spelling superseded by [GW-D76](#GW-D76):** `[repos.<name>.requests.github.working_set]`, audited as `repos.infra.requests.github.all.comment_issues`; the rule stands. The cells live under a name too, `[requests.github.repository.<name>.working_set]` and `.all`, and [GW-D58](#GW-D58) holds within each repository's pair, checked at load. The hand-over request names the item's repository, `yolo request github working-set acme/infra#70`; a bare number is accepted only when the workspace's own repositories are one, else refused naming the form. Audit and `yolo config why` carry the full key, `requests.github.repository.infra.all.comment_issues`; the launch disclosure is one line per repository, a read-only one saying so | 2026-11-02 | [§3](#3-the-working-set), [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D66"></a>GW-D66 | **Superseded by [GW-D70](#GW-D70) and [GW-D76](#GW-D76):** git reads the same `[repos.<name>]` tables as GitHub, its lines are `[repos.<name>.requests]`, and `requests.<source>.repos` is gone for git too. Git takes the same qualification: `[requests.git.repository.<name>]` with `push`, `push_tags`, `new_branches`; a name is a remote's (any host) or declared with `repo = "<host>/<path>"` (git's identity, `gitproto.Identity`) or `"*"` ([GB-D25](../reference/git-broker.md#GB-D25)). Unqualified `[requests.git]` stays every remote of the workspace, on any host, as today. [RC-D26](readable-config.md#RC-D26) is unchanged for `repos`; a named github.com repository no remote names joins git's scope as RC-D26's two exact URLs and keeps its writes, since it was named for them; any other host must be a remote. `yolo request git repository …` writes it; `yolo request git repos` stays refused | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D67"></a>GW-D67 | **Superseded by [GW-D75](#GW-D75).** Migration. No TOML key changes spelling: an unqualified line keeps its text, its meaning narrows to the workspace's own repositories, and the grants it loses on `repos` repositories show at the next gate as removed. `yolo config migrate` converts a legacy workspace `brokered.<source>.grants` entry to the unqualified key when its `repo` is the sole remote (today's `soleRemote`), otherwise to `repository.<name>` with `repo` written, the name being the remote that points at it, else the repository's last segment (`owner-repo` on a tie, as pack namespaces do), `any` for `"*"`; the grant set the lowering resolves is proved equal before and after, as [RC-D55](readable-config.md#RC-D55) proves. This retires `lower.go`'s "keep the old file for now" for several remotes, a `repos`-only repository and `"*"`; a legacy grant on a `repos` repository therefore keeps its writes through the name while the `repos` line stays | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D68"></a>GW-D68 | **Superseded in part by [GW-D71](#GW-D71):** a read-only row names `repos.<name>`, not `requests.github.repos`. What the gate prints: every grant line ends with its key and file, `(yolo-jail.toml: requests.github.repository.infra.runs)`, `(yolo-jail.toml: requests.github.runs, on remote origin)`, `(~/.config/yolo-jail/config.toml: allow.github.always, for every workspace)`; a repository row names its origin the same way, and a `repos` row says `read only`. The label `(from your user config, for every workspace)` is then true only of lines whose key is under `allow` | 2026-11-02 | [§5](#5-every-stop-names-its-next-step), [§8](#8-which-repository-a-line-is-for) | — |
| <a id="OQ-GW13"></a>[OQ-GW13](#OQ-GW13) | **Owner's ruling: A.** A `requests.github` line that names no repository is for `origin` only. A PR opened on another remote from a branch on `origin` (an upstream PR from a fork) counts as `origin`'s `create_pr` and joins the working set with its comment and edit lines only; a merge there asks per `working_set.merge_prs`, and a dispatch there asks; more needs a `[requests.github.repository.<name>]` table. Owner: *"the line grants what the human pictured, their own repository, and the upstream PR gets exactly what its author can do there anyway"* | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="OQ-GW14"></a>[OQ-GW14](#OQ-GW14) | **Owner's ruling: A.** A committed config may name a repository by a git remote's name; it resolves per clone, each person approves the repository it resolves to at their own prompt, and a wrong remote is pinned with `repo = "OWNER/REPO"` in the uncommitted local file. Owner: *"each person sees and approves the repository it resolves to, so nothing is granted unseen"* | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D69"></a>GW-D69 | **Implementation decision on the owner's comment on [OQ-GW15](#OQ-GW15), taken as a principle: one way to say one thing.** The owner: *"I don't want to have two ways to specify the same thing. That's right."* and *"I feel like the designer should have a better idea about this than me."* So a repository's name is declared once for every broker ([GW-D70](#GW-D70)), reading is that declaration and nothing else ([GW-D71](#GW-D71)), `requests.<source>.repos` is removed for every source, and a removed spelling is refused naming its replacement with no alias, as [MT-R3](named-setups.md#decision-ledger) ruled for renames | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D70"></a>GW-D70 | **Spelling superseded in part by [GW-D76](#GW-D76):** the table is `[repos.<name>]`, and git's lines are `[repos.<name>.requests]`. **Implementation decision on the owner's comment on [OQ-GW15](#OQ-GW15): a repository is named the same way for git and GitHub.** The owner: *"why is this GitHub specific? Why doesn't this apply to Git also?"* A name is declared once, in the top-level `[repositories.<name>]` table, with `repo = "<host>/<path>"` (git's identity, `gitproto.Identity`), and every broker reads it, a forge broker added later included; a git remote's name is a name with nothing written. This replaces the two declarations [GW-D62](#GW-D62) and [GW-D66](#GW-D66) allowed, one per broker. A `requests.git.for.<name>` line on a named github.com repository no remote names is a push grant there, on [RC-D26](readable-config.md#RC-D26)'s two URLs; a named repository off github.com must also be a remote | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D71"></a>GW-D71 | **Spelling superseded in part by [GW-D76](#GW-D76):** the scope row is `repos.<name>`, and the top-level `repos` table replaces `requests.<source>.repos`. **Naming a repository in a workspace is the request to read it**, for every broker serving its host (on github.com, `gh` reads and `git fetch` on RC-D26's two URLs), joining at the gate's `y` as a row of each broker's scope block under `repositories.<name>` and its file. It grants nothing else; a write on it asks. There is no separate read line, so `repos` has nothing left to say and goes ([GW-D69](#GW-D69)). A user config's names put nothing in any workspace's scope ([OQ-RC16](readable-config.md#decision-ledger)) | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D72"></a>GW-D72 | **Superseded by [GW-D76](#GW-D76)** (the owner's ruling of [OQ-GW15](#OQ-GW15)): there is no `for` segment; a repository's lines live in its own table. **The lines for a name are `requests.<source>.for.<name>.<key>` and `allow.<source>.for.<name>`**, for git and GitHub alike. The owner: *"request.github.blahblahblah is a lot to type out."* The fixed segment stays, as [GW-D61](#GW-D61) argued, so no name collides with a key yolo adds later; `for` is that segment at its shortest, it reads as English (`requests.github.for.infra`), and it is reserved in every source's table, in the place `repos` held. The name moves out of the path's declaration (`repo` lives in `[repositories]`), so a `for` table holds exactly its source's keys. `all` and `working_set` are refused as names, since `for.all` reads as every repository. The alternative, the lines under the repository (`[repositories.<name>.github]`), is [OQ-GW15](#OQ-GW15) | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D73"></a>GW-D73 | **Superseded in part by [GW-D76](#GW-D76) and [GW-D77](#GW-D77):** its `for` lines are now its `[repos.<name>]` table's lines; only `all` is refused as a name; `origin`'s GitHub lines stay in `[requests.github]`. **A declared name that is also a remote's name pins it**: its `for` lines and read scope go to the declared repository, and the gate row names both when they differ; unqualified lines stay the remote's. This carries out [OQ-GW14](#OQ-GW14)'s pin in the local file, which [GW-D62](#GW-D62)'s refusal of a disagreeing declaration contradicted. A `repo` with no host is refused naming the `github.com/` form; wildcards cover the owner and repository segments only, never the host. Names belong to the file scope that declares them: a workspace line never resolves a user config's name, and ceiling and request meet only at the repository both admit | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D74"></a>GW-D74 | **Superseded by [GW-D77](#GW-D77).** The verbs: `yolo request repository <name> <host>/<path> [--local]` declares; `yolo request <source> for.<name>.<key> [resource]` writes under it and refuses an undeclared name that is no remote, naming the first; `yolo allow repository <name> <host>/<path>` and `yolo allow <source> for.<name> on_request <token>` (or `always`) on the host. `repository` is a word of both verbs ahead of every source, so no pack may be named it. `yolo request <source> repos` is refused naming `yolo request repository` | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D75"></a>GW-D75 | **Spelling superseded by [GW-D77](#GW-D77):** entries become `[repos.<name>]` tables and grants their lines; the conversion's rules stand. Migration, replacing [GW-D67](#GW-D67)'s "no TOML key changes spelling": `requests.<source>.repos` and the never-built `repository` segment are refused at load, each naming its replacement. `yolo config migrate` converts a TOML file too: each `repos` entry becomes `[repositories.<last segment>]` in the same file (`owner-repo` on a tie); a legacy `brokered.<source>.grants` entry becomes the unqualified key when its `repo` is the sole remote, else `for.<name>` with the name declared, and `"*"` becomes the unqualified line plus the same line under each declared name. The grant set the brokers resolve is proved equal before and after ([RC-D55](readable-config.md#RC-D55)) | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | built 2026-11-12 (`0121997f1`), within [GW-D80](#GW-D80)'s bounds: the key is refused at load, and `yolo config migrate` converts a TOML or JSONC file with the grant sets compared (`TestRealReadersSeeTheSameGrantsAfterTheTOMLReposMigration`). A `"*"` or pattern grant is kept in its old file, not copied under each name |
| <a id="OQ-GW15"></a>[OQ-GW15](#OQ-GW15) | **Owner's ruling, a third option:** a repository is a top-level `[repos.<name>]` table with `repo = "<host>/<path>"`, and its grants live in it under the word `requests`: git's lines and any that apply to both in `[repos.<name>.requests]`, GitHub-only lines in `[repos.<name>.requests.github]`. Owner: *"we can use the word requests like repos dot infra dot requests and then list them there if we have to split between git and github I would say put the git ones the ones that apply to both and then for ones that only apply to github you put them under github"*; on the table's name: *"I feel like repos is just as clear to anybody and repositories is long"* | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D76"></a>GW-D76 | **Carrying out [OQ-GW15](#OQ-GW15)'s ruling.** `[repos.<name>]` holds `repo` (absent for a remote's name), `requests` in a workspace file and `allow` in the user config, each accepted only where its top-level table is (`allow` is user-scope only), so a table is never an ask in one file and the ceiling in another: the word under the name says which, which answers the objection the earlier option B carried. Git is the base: `[repos.<name>.requests]` holds `push`, `push_tags` and `new_branches`, `[repos.<name>.allow]` git's `always` and `on_request`; `.github` below either holds GitHub's lines, the cells as `.github.working_set` and `.github.all`. No line other than reading applies to both brokers, and reading is the table itself ([GW-D71](#GW-D71)); no git line lives anywhere but the base, so there is no `.git` subtable. A key under `requests` or `allow` is a git word or a broker's name, and a pack whose broker would take a git word is refused. A name sits one level below `repos`, so it collides with no key; `all` is refused because `repos.all` reads as every repository. The top-level `repos` table replaces `requests.<source>.repos`. Audit and `yolo config why` carry the full key, `repos.infra.requests.github.all.comment_issues`. Supersedes [GW-D72](#GW-D72) | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | partly, 2026-11-12 (`0121997f1`): `repo`, `[repos.<name>.requests.github]` capability lines and the user-only `[repos.<name>.allow.github]` are built. Not built: Git lines under a name (refused), and the `working_set` and `all` cells |
| <a id="GW-D77"></a>GW-D77 | **The verbs and the refusals for [GW-D76](#GW-D76).** The first word after `yolo request` or `yolo allow` is the key path, `requests` or `allow` left out: `yolo request repos <name> <host>/<path> [--local]` declares; `yolo request repos.<name> <git key> [resource]` writes `[repos.<name>.requests]`; `yolo request repos.<name>.github <key> [resource]` writes `[repos.<name>.requests.github]`; `yolo allow repos <name> <host>/<path>` and `yolo allow repos.<name>.github on_request <token>` (or `always`, and `repos.<name>` for git) on the host. A line under a name neither declared nor a remote is refused naming `yolo request repos`; `repos` is a word of both verbs ahead of every source, so no pack may be named it. `requests.<source>.repos` is refused at load naming `[repos.<name>]` `repo = "github.com/OWNER/REPO"` and `yolo config migrate`, and `yolo request github repos` naming `yolo request repos`, through the rename table (`internal/config/renames.go`); the `repository`, `[repositories]` and `for` spellings were never built and need none. **`origin`'s GitHub lines have one home**, applying [GW-D69](#GW-D69): `[repos.origin.requests.github]` says what the unqualified `[requests.github]` says ([OQ-GW13](#OQ-GW13)) and is refused naming it; `[repos.origin.requests]` git lines mean `origin` alone, which unqualified `[requests.git]` (every remote) cannot say, and are accepted. Supersedes [GW-D74](#GW-D74) | 2026-11-03 | [§8](#8-which-repository-a-line-is-for) | partly, 2026-11-12 (`0121997f1`): `yolo request repos`, `yolo request repos.<name>.github`, `yolo allow repos`, `yolo allow repos.<name>.github` and the refusals are built (`TestRequestReposDeclaresANamedRepository`, `TestAllowReposDeclaresAUserNameAndItsScopedCeiling`). Not built: the Git-key forms |
| <a id="GW-D78"></a>GW-D78 | **Owner, 2026-11-06:** *"Do we really need 'Cancel Run'? Is that really so different than starting a run? Can we just have 'Runs' be a category?"* The `dispatch-workflow` kind becomes **`runs`** (readable `allow.github.on_request = ["runs"]`, `requests.github.runs = ["<file>"]`, `yolo request github runs <file>`), and one grant naming a workflow file or `*` on a repository covers three things unasked: a dispatch on the default branch exactly as [GW-D14](#GW-D14) rules it; `gh run rerun <id>` (with `--failed`, `--debug`, or `--job <id>` in place of the id) of a run the broker reads (`repos/{o}/{r}/actions/runs/{id}`, through `actions/jobs/{id}` for `--job`) to have `head_repository` the repository itself, `head_branch` its default branch and `path` `.github/workflows/<file>` with a file the grant names, since a rerun executes its own commit's workflow file and that is the default branch's; and `gh run cancel <id>` of any run in the repository, under any `runs` grant on it, since cancelling executes no code. A rerun of another branch or a fork, `workflow enable`/`disable`, `run delete` and the `gh api` spellings of rerun and cancel still ask; `approve-run` stays its own kind. The former spellings `dispatch-workflow` and `dispatch_workflows` are read as `runs` wherever a file, a scope file or a verb writes them, so an existing grant keeps working and now also reruns and cancels, the widening the owner's question rules; nothing is rewritten. Supersedes [GW-D51](#GW-D51) | 2026-11-06 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | built |
| <a id="GW-D79"></a>GW-D79 | **Implementation decision: repository binding is the first buildable slice of the ruled contract.** Build the GitHub side of repository binding first, ahead of the work-kind cells and the Git tables: unqualified `[requests.github]` lines lower to origin's concrete identity only; other repositories enter by `[repos.<name>]`, read-only unless their own table asks; the user's `[repos.<name>.allow.github]` is additive; a scalar `repo` retargets a name behind a fresh identity gate; `requests.github.repos` migrates with grant equality, printing retained writes. Engineering sequencing of [OQ-GW13](#OQ-GW13), [OQ-GW15](#OQ-GW15) and [GW-D75](#GW-D75); no new policy. This workspace's boundary is `mschulkind-oss/yolo-jail` only (owner, 2026-11-12); the design changes no active grant or configuration file. | 2026-11-12 | [§8](#8-which-repository-a-line-is-for), [first slice](#current-support-and-the-repository-only-first-slice) | built 2026-11-12 (`0121997f1`); pinned by `TestUnqualifiedGitHubRequestsLowerToOriginOnly` and `TestANamedRepositoryIsApprovedAtTheGateAndReachesTheBroker`. This workspace's own files are unchanged |
| <a id="GW-D80"></a>GW-D80 | **Implementation decision: the first slice's bounds**, found completing [the plan](github-repository-binding-plan.md) against the tree; each refuses or keeps today's behavior, and none widens. Origin-only lowering applies to a broker whose declaration opts in (`unqualified_requests = "origin"`, declared by the shipped `github` broker), so another named-host source keeps today's reach; a pattern `repo` lowers one grant per line with the pattern as its repository, marked as a pattern (`RepoPattern`) and matched by whole segments in the broker only when marked, since a grant on a pattern matched nothing before and a recorded one stays inert, so the gate asks once for a named table's pattern; a user `repo` is required; a scoped ceiling lists write capabilities only, read staying in `[allow.<broker>] always`; Git lines under a name are refused until the Git tables are built; the migration preserves the writes on listed repositories and on remotes other than `origin`, and leaves a workspace with no old spelling unwritten; a legacy `"*"` repository grant, and a legacy pattern entry or pattern grant, is not converted, its old file kept and still read (a correction of the premise that a pattern entry converts as read scope as before: the readable path refuses that read, and the record cannot make an inert grant equal to a live one). A stored approval is not effective once its request is no longer lowered: the record is a comparison baseline, and a running jail's scope changes only at a fresh launch or `yolo config apply` | 2026-11-12 | [first slice](#current-support-and-the-repository-only-first-slice) | built 2026-11-12 (`0121997f1`) |
