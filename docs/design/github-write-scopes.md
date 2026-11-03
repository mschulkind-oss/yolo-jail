---
title: "Where the GitHub line is drawn: which writes run unasked, per user and per workspace"
date: 2026-10-30
status: in-review
stage: DESIGN
next: "Owner confirms the pick-one-set rule, the invariant and the working-set approve cell (GW-D57 to GW-D59; OQ-GW7, OQ-GW11, OQ-GW12 recommended closed), and rules OQ-GW13 to OQ-GW15 on which repository a line is for (§8); then the plan is built from it"
depends-on:
  - boundary-broker.md
tags: [design, github, gh, broker, grants, approvals, config, toml]
summary: "Which of the agent's gh writes run unasked is a 2x2: the user's config is the ceiling (the most a workspace may hold) and the workspace's config is the ask, each with two tables of the same six cells, `working_set` (the PRs and issues the agent created or was handed) and `all` (every other item). A write on a working-set item reads the `working_set` table only; any other write reads `all` only. One invariant: whatever `all` grants, `working_set` grants too, or the config is refused. Repository-level grants (dispatch on more refs, fork-run approval, reruns, cancels, releases, labels, projects) sit beside the cells, opt-in and never default. Every line is for the workspace's own repositories, the ones its git remotes name, unless it sits under `[requests.github.repository.<name>]`, which names one other repository, by a remote's name or an `OWNER/REPO`; a repository listed in `repos` is read only."
---

# Where the GitHub line is drawn: which writes run unasked, per user and per workspace

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
the workspace's own repositories; a `[requests.github.repository.<name>]` table holds the same
keys for one other repository ([§8](#8-which-repository-a-line-is-for)).

| Value in the ask | Means | Needs |
| :--- | :--- | :--- |
| `true` | the cell runs unasked here | a covering ceiling token and a human's `y` at the config-change gate; outside the ceiling it is reported at load and counts as absent, never an error |
| `"ask"` | the cell asks here, whatever the ceiling says | nobody's `y`; removing it is a widening and shows at the gate |
| `false` | the cell is refused here, whatever the ceiling or `always` says, with no request filed; the refusal names the line to remove ([OQ-GW8](#decision-ledger)) | nobody's `y`; removing it is a widening and shows at the gate |
| absent | the user's `always` decides; else it asks | — |

**Where the invariant is checked** ([GW-D58](#GW-D58)): at config load, so `yolo check`, a launch and
`yolo config apply` all refuse the same file, on the workspace's two tables after the project and local
files merge, shipped defaults in. The values order `true`, then `"ask"`, then `false`: `working_set.X`
set to `"ask"` or `false` under `all.X = true` is the error. The ceiling needs no check: an `all.X`
token covers `working_set.X`.
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
([GW-D47](#GW-D47), [OQ-GW12](#OQ-GW12)):

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
of the workspace names R, the `repository.<name>` table whose name resolves to R otherwise, and
none when R is in scope for reading only, in which case every write asks
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
| The launch disclosure, one line per repository | `github-broker: acme/app (origin): create_pr; working set: comment, edit run (shipped default), merge runs; all: comment_issues runs, merge asks; working set: 2 PRs, 1 issue` then `github-broker: acme/infra (repository.infra): dispatch deploy.yml runs; every other write asks` then `github-broker: acme/docs: read only (requests.github.repos)` | — |
| A write on a repository in scope for reading only | `dispatching sync.yml on mschulkind/dotfiles asks here (r-4c1): yolo-jail.local.toml lists it under requests.github.repos, which is read only. Once: yolo approve r-4c1. To grant writes on it, name it:` | `yolo request github repository dotfiles mschulkind/dotfiles`, then `yolo request github repository.dotfiles.dispatch_workflows sync.yml` |
| `yolo check`: a name that is neither a remote nor declared | `yolo-jail.toml:14: [requests.github.repository.infra] names no git remote of this workspace (origin, upstream) and declares no repo. Name the repository:` | `yolo request github repository infra acme/infra` |
| `yolo check`: a declared name that disagrees with a remote | `yolo-jail.toml:14: requests.github.repository.upstream.repo = "acme/infra", but this workspace's remote upstream is acme/app. Rename the table, or drop the repo line to mean the remote:` | `yolo config unset requests.github.repository.upstream.repo --project` |
| `yolo check`: a name that is a word the table takes | `yolo-jail.toml:14: [requests.github.repository.all]: "all" is a key of requests.github, so a repository cannot be called that. Pick another name:` | `yolo request github repository infra acme/infra` |
| `yolo check`: a request under a name, outside the ceiling | `yolo-jail.toml:16: this project requests dispatch_workflows on acme/infra (repository.infra); your config allows it on request for no repository that admits acme/infra, so this project runs without it. To allow it for every repository, or for this one:` | `yolo allow github on_request dispatch_workflows`, or `yolo allow github repository.infra on_request dispatch_workflows` after `yolo allow github repository infra acme/infra` |

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
`[requests.github.repository.<name>]` it is for that one repository and no other
([§8](#8-which-repository-a-line-is-for)).

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

Gate lines state the reach plainly: `dispatch_refs` names the branches and no secrets clause (a push
grant already runs any workflow text with the repository's secrets); `approve_fork_runs` names the fork's
read-only token, the Actions minutes and `pull_request_target`; `releases` with `publish` says a
published release is public, notifies watchers and runs the release workflows.

**Still refused under every grant** ([GW-D55](#GW-D55)): a repository's settings, secrets, variables,
deploy keys, rulesets, branch protection and collaborators; archive, transfer and `repo delete`; any
delete (`DELETE`, a `delete` verb, `--delete-last`, `--delete-branch`, `item-delete`); raw GraphQL.

Not covered here: git's working set, whose line is the branch
([`git-broker.md` §5.1](git-broker.md#51-the-working-set-of-pushable-branches)), though git takes the
same `repository.<name>` qualification ([GW-D66](#GW-D66)); the refused commands
([BB-P8](boundary-broker.md#BB-P8)), which no cell or grant widens; the repository scope, which no cell
widens and which a `repository.<name>` table joins only at the gate's `y`, as a remote or a `repos`
entry does ([GW-D63](#GW-D63)); a line per PR number, which `yolo approve` draws in the store.

## 8. Which repository a line is for

Every line in `[requests.github]`, cells and repository-level grants alike, is for the
**workspace's own repositories** *(a plain phrase, used here as [`config-user.md` §6](config-user.md#6-what-a-jail-may-do-with-your-logins)
uses "its own repositories")*: the repositories the workspace's git remotes name on github.com,
every one of them ([OQ-GW13](#OQ-GW13)), each a row the human approved at the gate. A
repository the project lists in `requests.github.repos` is in scope for **reading only**: no
unqualified line reaches it, and a write on it asks ([GW-D60](#GW-D60)). That is what this
repository's own local file says of `mschulkind/dotfiles`, and what the lowering did not honor.

To write on any other repository, the project names it. A **named repository** *(coined here)*
is one table, `[requests.github.repository.<name>]`, holding exactly the keys `[requests.github]`
takes, `repos` and `repository` excepted, each meaning on that repository what it means on the
workspace's own ([GW-D61](#GW-D61)). The segment is the fixed word `repository`, never the name
alone, because a bare name would collide with `all`, `working_set`, `repos` and every capability
word, today's and the ones yolo adds later, which would silently change what a user's name means.

**A name is one of two things** ([GW-D62](#GW-D62)):

- **A git remote's name**, `origin`, `upstream`, read from this clone's `.git/config` and nothing
  else written. It means the repository that remote points at on github.com. In a committed file it
  resolves per clone ([OQ-GW14](#OQ-GW14)); a remote on another host, or no remote of that
  name, is a `yolo check` error naming the remotes and the `repo` line.
- **A declared name**, with `repo = "OWNER/REPO"`, or a whole-segment wildcard
  (`"acme/*"`, `"*/*"`, [WW-D31](workspace-widening.md#WW-D31)), carried by the gate line that
  says in plain words what it admits. A declared name that is also a remote's name is fine when
  both name the same repository and an error when they do not.

A name is a TOML bare key, letters, digits, `-` and `_`, so no path ever needs quoting, and it may
not be a word `requests.github` takes. `yolo request github repository <name> <OWNER/REPO>` declares
one; `yolo request github repository.<name>.<key> [resource]` writes a line under it, as
`yolo request github working_set.merge_prs` writes a cell ([GW-D46](#GW-D46)).

```toml
# yolo-jail.toml: this project (origin and, in a fork clone, upstream) and two other repositories
[requests.github]
create_pr = true                                     # on the workspace's own repositories
dispatch_workflows = ["release-request.yml"]         # on them too, default branch

[requests.github.repository.infra]                   # yolo request github repository infra acme/infra
repo = "acme/infra"
dispatch_workflows = ["deploy.yml"]                  # yolo request github repository.infra.dispatch_workflows deploy.yml
[requests.github.repository.infra.all]
comment_issues = true                                # the same cells, the same invariant, on acme/infra

[requests.github.repository.upstream]                # no repo line: this clone's remote "upstream"
labels = true

# yolo-jail.local.toml: read only, as its comment says
[requests.github]
repos = ["mschulkind/dotfiles"]
```

**The ceiling is per repository the same way** ([GW-D64](#GW-D64)). `[allow.github]` is the
ceiling for every repository; `[allow.github.repository.<name>]`, with a `repo` line (a user config
has no remotes, so a name there is always declared) and its own `always` and `on_request`, adds a
ceiling for the repositories its `repo` admits. A project's request on repository R is within the
ceiling when the unqualified lists or any qualified table admitting R lists the token; a qualified
table never narrows the unqualified one, so a user who wants "dispatch on mine, never elsewhere"
moves the word from `[allow.github] on_request` to `[allow.github.repository.mine]`.

```toml
# ~/.config/yolo-jail/config.toml
[allow.github]
always = ["read"]
on_request = ["create_pr", "create_issue"]           # any repository a project names

[allow.github.repository.mine]                       # yolo allow github repository mine 'mschulkind/*'
repo = "mschulkind/*"
on_request = ["dispatch_workflows", "approve_runs", "all.merge_prs"]   # only under my own account
```

**Scope** ([GW-D63](#GW-D63)). A named repository is in the approved scope in its own right: a row
in the gate's scope block under its key and file, joining at the `y` like a remote or a `repos`
entry; no second line in `repos` is needed or implied. Its grants are the ones its table holds,
fanned out over the repositories its name resolves to, and over nothing else; a remote's repository
gets the unqualified lines and, when a table names that remote, that table's lines too.

**The cells and the working set** ([GW-D65](#GW-D65)). `[requests.github.repository.<name>.working_set]`
and `.all` are the same six cells, and the invariant ([GW-D58](#GW-D58)) holds within each
repository's pair. Items are already keyed by repository, so the working set changes only in
spelling: `yolo request github working-set acme/infra#70` names the item's repository, and a bare
`working-set 70` is accepted only when the workspace's own repositories are one. A covered write is
audited with the full key, `requests.github.repository.infra.all.comment_issues`.

**Git takes the same qualification** ([GW-D66](#GW-D66)). `[requests.git]` lines (`push`,
`push_tags`, `new_branches`) are for every remote of the workspace, on any host, as today;
`[requests.git.repository.<name>]` names one, by a remote's name or `repo = "<host>/<path>"`
(git's identity, `github.com/acme/infra`) or `"*"` ([GB-D25](git-broker.md#GB-D25)). A requested
github.com repository stays read only in git ([RC-D26](readable-config.md#RC-D26)); a named one
on github.com joins the git scope as RC-D26's two URLs and keeps its writes, because it was named
for them.

**Today's files** ([GW-D67](#GW-D67)). No TOML key changes spelling: an unqualified line keeps its
text and loses the `repos` repositories it was fanned out over, which the next gate shows as
`- grant … removed`. `yolo config migrate` converts a legacy `brokered.<source>.grants` entry to the
unqualified key when its `repo` is the workspace's sole remote, as today, and otherwise to
`repository.<name>` with the `repo` line written, so the cases `lower.go` leaves as "keep the old
file for now" (several remotes, a `repos`-only repository, `"*"`) convert exactly.

**What the gate prints** ([GW-D68](#GW-D68)). Every grant line ends with where it came from, the
key and the file, so the provenance a human reads is never a guess:

```text
github-broker repository scope, read from this workspace's .git/config, yolo-jail.toml and yolo-jail.local.toml:
    github.com/mschulkind-oss/yolo-jail   remote origin                                    unchanged
  + github.com/acme/infra                 requests.github.repository.infra (yolo-jail.toml)  added
    github.com/mschulkind/dotfiles        requests.github.repos (yolo-jail.local.toml)       unchanged, read only
  + grant dispatch-workflow github.com/acme/infra deploy.yml  added (yolo-jail.toml: requests.github.repository.infra.dispatch_workflows)
    grant dispatch-workflow github.com/mschulkind-oss/yolo-jail release-request.yml  unchanged (yolo-jail.toml: requests.github.dispatch_workflows, on remote origin)
    grant read *  unchanged (~/.config/yolo-jail/config.toml: allow.github.always, for every workspace)
```

## Open Questions

1. 💬 **<a id="OQ-GW7"></a>[OQ-GW7](#OQ-GW7): When the agent merges its own PR, should only the working-set line decide, or should an `all` line count too?**

   The defaults, when no file sets a line ([§3](#3-the-working-set), *Shipped defaults*): the
   working set's comment and edit lines are on, its approve and merge lines are absent, and `all`
   is empty. An absent line runs only if Kurt's user config lists it under `always`; otherwise it
   asks. Kurt's user config lists nothing under `always` but `read`.

   Kurt's `yolo-jail.toml` says `[requests.github.all] merge_prs = "ask"`, meaning "every merge asks
   me". The agent opened PR #41 itself, so #41 is in the working set, whose `merge_prs` line is
   absent by default. The agent runs `gh pr merge 41`. Under the rule as drafted, a write on a
   working-set item reads only the working-set lines, so `all` does not reach #41. The absent line
   falls through to Kurt's `always`, which has no merge, so the merge asks anyway: the defaults
   give Kurt what he meant. The split only shows when his user config holds
   `always = ["working_set.merge_prs"]`: then #41 merges unasked despite his `all` line, and making
   every merge ask takes a second line, `working_set.merge_prs = "ask"`. A `true` in `all` never
   splits this way, because the check that refuses an `all` granting more than the working set
   copies it in.

   - **A — Each table falls back to the other, line by line.** Kurt's one `all` line makes #41's
     merge ask too. There's a second rule to keep in mind when reading any config: which table wins
     on a given line.
   - **B — Only the working-set table decides for a working-set item.** One rule. Kurt needs two lines to make
     every merge ask, and `yolo config why requests.github.working_set.merge_prs` shows him that the line is
     absent.

   <!-- vantage: question id=OQ-GW7 leaning="B: one table per call is the rule you can hold in your head, and the check already carries every all grant into the working set." -->

   _Leaning:_ **B.** Reading one table per call is the rule you can keep in your head, and the check
   already copies every `all` grant into the working set, so only "ask" ever takes two lines.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **<a id="OQ-GW11"></a>[OQ-GW11](#OQ-GW11): Should the working set have its own "approve a PR" line?**

   Ana hands the agent PR #70, which a teammate opened, with `yolo request github working-set 70`,
   and approves the hand-over. She wants the agent to approve #70 once its review passes, so it can
   merge under branch protection. The agent runs `gh pr review 70 --approve`. #70 is now in the
   working set, so only that table's lines decide, and if the working set has no approve line, the
   write asks every time, whatever Ana's `all` table says. On a PR the agent opened itself, GitHub
   refuses the approval anyway, since an author cannot approve their own PR.

   - **A — Give the working set an approve line, off by default.** Ana writes
     `working_set.approve_prs = true`. The hand-over prompt then shows `approve` among what the agent
     may do with #70, before her `y`. Both tables then hold the same six lines.
   - **B — No approve line in the working set.** Approving a handed-over PR always asks. The tables
     differ by one line, and the check needs a special case for it.

   <!-- vantage: question id=OQ-GW11 leaning="A: the hand-over prompt shows approve before the y, and same six lines in both tables means no special case." -->

   _Leaning:_ **A.** The hand-over prompt shows `approve` before Ana says yes, and the same six lines in both
   tables leave the check without a special case.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **<a id="OQ-GW12"></a>[OQ-GW12](#OQ-GW12): Should a PR you hand to the agent merge unasked when the project lets the agent merge its own PRs?**

   The project's `yolo-jail.toml` holds `working_set.merge_prs = true`, so the agent merges PRs it
   opened without asking. Ana hands it #70, a teammate's PR, and the hand-over prompt reads `there the agent may, unasked: comment, edit, merge`. She answers `y`. Later the agent runs
   `gh pr merge 70`. Today, a handed-over PR and one the agent created are treated the same, so the merge runs.

   - **A — Yes, the same as the agent's own PRs.** The prompt showed `merge` before Ana's `y`. To keep
     merges asking for one PR, she answers `--deny` and hands it over later. To keep them asking for
     every PR, she sets `working_set.merge_prs = "ask"`.
   - **B — A handed-over PR always asks before it merges.** Ana approves each merge as it happens.
     The working set then holds two kinds of item that behave differently, and every line has to
     say which kind it means.

   <!-- vantage: question id=OQ-GW12 leaning="A: the hand-over prompt already shows merge before the y, so a second kind of item buys nothing." -->

   _Leaning:_ **A.** The hand-over prompt already puts `merge` in front of Ana before her `y`, so a
   second kind of item buys nothing.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **<a id="OQ-GW13"></a>[OQ-GW13](#OQ-GW13): When a project grants a write without naming a repository, does it cover every GitHub remote or only `origin`?**

   Kurt works in a fork: his `origin` is `kurt/app` and his `upstream` is `acme/app`. The
   project's committed `yolo-jail.toml` says `create_pr = true` and
   `dispatch_workflows = ["nightly.yml"]`, with no repository named. The agent runs
   `gh pr create`, which from a fork opens the PR on `acme/app`. Later it runs
   `gh workflow run nightly.yml --repo acme/app`. Today, both reach `acme/app`, because each
   remote is a repository Kurt approved at his own prompt.

   - **A — Every GitHub remote.** The fork's PR flow works with no extra line. The nightly
     dispatch also runs on `acme/app`, where GitHub's own permissions on Kurt's account are the
     backstop. A project that means one remote writes
     `[requests.github.repository.origin]`.
   - **B — `origin` only.** `gh pr create` from a fork asks every time unless the project adds a
     `[requests.github.repository.upstream]` table. Kurt's dispatch to `acme/app` asks.

   <!-- vantage: question id=OQ-GW13 leaning="A: origin-only breaks the fork PR flow, the case upstream matters most, and a project that means one remote can name it." -->

   _Leaning:_ **A.** Origin-only breaks the fork PR flow, which is where `upstream` matters most,
   and a project that means one remote can name it.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **<a id="OQ-GW14"></a>[OQ-GW14](#OQ-GW14): May a committed config name a repository by a remote's name, when each clone's remote can point somewhere different?**

   The project's committed `yolo-jail.toml` holds `[requests.github.repository.upstream]
   labels = true`. In Kurt's clone `upstream` is `acme/app`, and in Ana's it is
   `ana/app-experiments`. Each of them launches, and their prompts show
   `+ grant labels github.com/acme/app` and `+ grant labels github.com/ana/app-experiments`. So one
   committed line grants label writes on two different repositories, depending on whose clone it is.

   - **A — Allow it.** This is exactly what a fork workflow wants to say. Each person approves the
     repository it resolves to at their own prompt. Someone whose `upstream` is wrong for the
     project puts `repo = "acme/app"` in their uncommitted `yolo-jail.local.toml`.
   - **B — Remote names only in the uncommitted local file.** The committed file always means the same
     repositories. Every fork contributor adds their own table by hand before the line does anything.

   <!-- vantage: question id=OQ-GW14 leaning="A: each person sees and approves the repository it resolves to, so nothing is granted unseen." -->

   _Leaning:_ **A.** Each person sees and approves the repository the name resolves to, so nothing
   is granted unseen.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 🤷 **<a id="OQ-GW15"></a>[OQ-GW15](#OQ-GW15): Should `repos`, the read-only list of extra repositories, stay beside named repositories?**

   This repository's uncommitted `yolo-jail.local.toml` says `repos = ["mschulkind/dotfiles"]`, so the agent can
   read your yolo configs. Named repositories give a second way to say the same thing: a
   `[requests.github.repository.dotfiles]` table holding only `repo = "mschulkind/dotfiles"`, which
   is read-only because it grants nothing. Two spellings for one thing is what this project usually
   avoids.

   - **A — Keep `repos`.** "Read these" stays one short line. `yolo config why` shows which
     spelling put a repository in scope.
   - **B — Retire `repos` into named tables.** One way to say it. Every existing `repos` line
     needs rewriting, which `yolo config migrate` does not do today for a file already in TOML.

   <!-- vantage: question id=OQ-GW15 leaning="A: weak, mostly taste; repos is the short spelling for the common read-only case." -->

   _Leaning:_ **A**, weakly. This is mostly taste. `repos` is the short spelling for the common
   read-only case.

   **Answer:**
   > _(empty — fill in when decided)_

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
| <a id="GW-D14"></a>GW-D14 | **Maintainer's ask:** *"I want you to improve the broker so dispatch is possible. then we'll modify my config to allow it, relaunch, and have you dispatch it."* The `dispatch-workflow` grant names a repository and one workflow file name (or `*`); readable spelling `allow.github.on_request = ["dispatch_workflows"]`, `requests.github.dispatch_workflows = ["<file>"]`. It covers only `gh workflow run <file>` and `gh api` POST `repos/{o}/{r}/actions/workflows/{file}/dispatches` with no query or fragment, no `--input`, and exactly one field rooted at `ref`, on the repository's default branch only (`--ref` omitted, the branch's name, or `refs/heads/<name>`, read by the broker's own `gh api repos/{o}/{r}`); the argv that runs is pinned to `refs/heads/<default>`, so neither gh's own lookup nor a tag of the same name can change it. A workflow by id or display name, other refs (unless [GW-D49](#GW-D49)), `run rerun`, `run cancel`, `workflow enable`/`disable` ask. Authorized under the store's transaction lock with the grant re-read, audited under `dispatch-workflow`. Residual: the default-branch read is just before the transaction, and a workspace that may push the default branch can change the workflow it dispatches | 2026-10-31 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | built |
| <a id="GW-D15"></a>GW-D15 | **Maintainer's ask:** approve the runs GitHub holds at `action_required` (the weekly `flake.lock` bump PR `github-actions[bot]` opens from the repository's own branch) without a per-call host approval. The `approve-run` grant names a repository and an actor, a GitHub login (optionally `[bot]`) or `*`; readable spelling `allow.github.on_request = ["approve_runs"]`, `requests.github.approve_runs = ["<login>"]`, written by `yolo request github approve_runs <login>`. It covers exactly `gh api` with a stated POST of `repos/{o}/{r}/actions/runs/{id}/approve`, numeric id, no query, field or `--input`; before running it the broker reads the run and covers it only when `triggering_actor.login` matches (case-insensitively, or `*`) **and** `head_repository.full_name` is the repository itself. A fork's run, a failed read, another run or an unnamed actor asks, saying which, and names `yolo request github approve_runs <actor>` then `yolo config apply`. Authorized under the store lock with the grant re-read, audited under `approve-run`. Residual: the run read is just before the transaction | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | built |
| <a id="OQ-GW1"></a>[OQ-GW1](#OQ-GW1) | **Ruled A:** four work kinds, comment, edit, approve, merge; they survive as the verb half of each cell ([GW-D41](#GW-D41)) | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="OQ-GW2"></a>[OQ-GW2](#OQ-GW2) | **Ruled:** `yolo approve --add` is removed; an item the agent did not create is reached by Allow once, by `--for`, or by the agent's own request to join the working set, which a plain `yolo approve` answers. Owner: *"can't the agetn request it to be added to the working set somehow?"*, then *"yes that sounds good"*; on `--add`: *"It rides along with a grant, or it rides along with an approval, and depending on what the approval is, it does different things, and you're trying to guess context. … Let's remove it."* A handed-over item gets what the `working_set` cells say ([GW-D39](#GW-D39), [GW-D47](#GW-D47)) | 2026-11-02 | [§3](#3-the-working-set) | — |
| <a id="GW-D34"></a>GW-D34 | Working-set entries a human added with `--add` before its removal are kept until revoked, listed by `yolo approve` with their origin, and ended by `--revoke w-<id>`; the store records what was added, not how, so nothing migrates | 2026-11-02 | [§3](#3-the-working-set) | — |
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
| <a id="GW-D49"></a>GW-D49 | `dispatch_refs`, a list of branch names or patterns, widens a `dispatch_workflows` grant from the default branch to matching branches, pinned to `refs/heads/<name>`; never a tag; alone it covers nothing; its gate line has no secrets clause. Owner: *"can't it already do this with perms to push to literally any branch?"* | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D50"></a>GW-D50 | `approve_fork_runs = ["<login>" or "*"]` covers the run-approve endpoint when the run read shows a fork head and a matching actor; `approve_runs` keeps the repository's own heads; the gate line names the read-only token, the minutes and `pull_request_target` | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D51"></a>GW-D51 | `rerun_runs = true` covers `gh run rerun <id>` and the `rerun`, `rerun-failed-jobs` and `jobs/{id}/rerun` endpoints with a numeric id, stated method, no field and no `--input`; `cancel_runs = true` covers `gh run cancel <id>` and its endpoint; separate words | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D52"></a>GW-D52 | `releases = ["draft"]` or `["draft", "publish"]`, `publish` not implying `draft`, covers creating and editing releases only on a tag one read finds, with `--verify-tag` added to a covered `release create`; a missing tag asks naming `git push origin <tag>` under `push_tags`; deletes and asset uploads never | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D53"></a>GW-D53 | `labels = true` covers creating and editing the repository's labels; never `label delete` or `label clone` | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D54"></a>GW-D54 | **Owner's ruling:** offer projects. `projects = ["<owner>/<number>"]` covers adding this repository's items to a granted board, `item-edit` and `item-archive` on it, and `--add-project`/`--remove-project` after one read resolves the title to a granted board; never board or field create, close, edit, delete, copy or link, nor draft items | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D55"></a>GW-D55 | Still refused under every grant: settings, secrets, variables, deploy keys, rulesets, branch protection, collaborators, archive, transfer, `repo delete`, any delete, raw GraphQL | 2026-11-02 | [§7](#7-repository-level-grants-and-what-is-refused-outright) | — |
| <a id="GW-D56"></a>GW-D56 | No cell covers a delete: `pr merge --delete-branch` and `pr close --delete-branch` ask, naming the write without the flag and a git `push` grant for the branch delete | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="GW-D57"></a>GW-D57 | **Recommended (pending the owner's confirmation), from the owner's model of 2026-11-02:** pick the table first. A write on a working-set item reads `working_set` only; a write on any other item reads `all` only; neither table is a fallback for the other. Amends [GW-D45](#GW-D45) | 2026-11-02 | [§4](#4-how-one-call-resolves) | — |
| <a id="GW-D58"></a>GW-D58 | **Recommended (pending the owner's confirmation):** the invariant. For every cell X, `all.X = true` requires `working_set.X = true` (a shipped default counts); `"ask"` is never a grant, so `all.X = "ask"` constrains nothing and `working_set.X = "ask"` under `all.X = true` is the error. Checked at config load on the workspace's merged tables, so `yolo check`, a launch and `yolo config apply` refuse the same file, naming both fixes; the ceiling satisfies it by construction ([GW-D44](#GW-D44)). Owner: *"a check to make sure that at least the minimum of the all permissions are specified in the working set permissions. Otherwise ... that's probably a fatal error because somebody probably made a mistake there."* | 2026-11-02 | [§2](#2-the-ceiling-and-the-ask) | — |
| <a id="GW-D59"></a>GW-D59 | **Recommended (pending the owner's confirmation):** `working_set.approve_prs` exists, so the two tables hold the same six cells and the invariant needs no special case; it ships absent. On the agent's own PR GitHub refuses the approval; on a handed-over PR the hand-over line named `approve` before the `y`. Supersedes [GW-D43](#GW-D43) | 2026-11-02 | [§1](#1-the-cells) | — |
| <a id="OQ-GW8"></a>[OQ-GW8](#OQ-GW8) | **Ruled A:** a cell may be `false`, a refusal that beats the ceiling and `always`, filing no request and naming the line to remove; it orders below `"ask"`, so `all.X = true` with `working_set.X = false` breaks the invariant. Owner: *"Add false, as a refusal that beats the ceiling: it is one more value, and it is the only way to say never."* | 2026-11-02 | [§2](#2-the-ceiling-and-the-ask) | no |
| <a id="OQ-GW9"></a>[OQ-GW9](#OQ-GW9) | **Ruled A:** the hand-over request is `yolo request github working-set <n>`. Owner: *"yolo request github working-set 70: the agent's side of every grant is filing a request, and the host's is yolo approve."* | 2026-11-02 | [§3](#3-the-working-set) | no |
| <a id="OQ-GW10"></a>[OQ-GW10](#OQ-GW10) | **Ruled A:** an item leaves the working set on merge or close, or on `yolo github working-set remove <n>`; nothing expires; a reopened item asks once and may be requested again. Owner: *"A: on merge or close, or an explicit remove; no expiry, since a created item has none and a handed-over one is the same thing."* | 2026-11-02 | [§3](#3-the-working-set) | no |
| <a id="GW-D60"></a>GW-D60 | **An unqualified `requests.github` line is for the workspace's own repositories only: the repositories its git remotes name on github.com, never a `requests.github.repos` entry.** A `repos` entry is read scope, as its spelling in this repository's local file promises (*"Pushes are never widened by a requested repository"*) and as git already holds ([RC-D26](readable-config.md#RC-D26)); a write on one asks, naming `yolo request github repository <name> <OWNER/REPO>`. Replaces the lowering that fanned every request over every target (`ProduceReadableGateInput`), which gave `mschulkind/dotfiles` four dispatch grants on 2026-11-02. Which of the remotes the unqualified line covers is [OQ-GW13](#OQ-GW13) | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D61"></a>GW-D61 | **Owner's ask, shaped:** *"somebody's gonna want to grant things across multiple repositories … you'll have some name you can define somewhere. And then that name will be part of the path to these grants."* The path is `requests.github.repository.<name>.<key>`, the fixed segment `repository` then the name, never the name alone as the owner feared (*"that does create collision risks"*): a bare name would collide with `all`, `working_set`, `repos`, every capability word and every one yolo adds later, which would silently change a user's name's meaning; a fixed segment makes the collision impossible instead of reserved against. `repository`, not `repo`, because the repo treats a singular spelling beside a plural key as a typo (`singularRootKeys`) and `repos` stays. A named table takes exactly the keys `requests.github` takes, `repos` and `repository` excepted, each meaning on that repository what it means unqualified ([GW-D37](#GW-D37): every key reads cold at either depth). A name is a TOML bare key (letters, digits, `-`, `_`) so no path is ever quoted, and may not be a key `requests.github` takes (`all`, `working_set`, `repos`, `repository`, a capability word), which `yolo check` refuses naming another name | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D62"></a>GW-D62 | A name is a git remote's name or a declared one. A remote's name (`origin`, `upstream`) is a name with nothing written: it means the repository that remote points at on github.com, read from this clone's `.git/config`; a remote on another host or no remote of that name is a `yolo check` error naming the remotes and the `repo` line. A declared name has `repo = "OWNER/REPO"`, or a whole-segment wildcard in [WW-D31](workspace-widening.md#WW-D31)'s forms, whose gate line says in plain words what it admits, as [GB-D25](git-broker.md#GB-D25) does for a `*` ref grant; the owner on wildcards: *"if someone wants to grant repo \*, why should we stop them?"*. A declared name equal to a remote's name is accepted when both name the same repository and refused when they differ, naming the rename or the `repo` line to drop. `yolo request github repository <name> <OWNER/REPO>` declares; `yolo request github repository.<name>.<key> [resource]` writes under it, the dotted form of [GW-D46](#GW-D46) | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D63"></a>GW-D63 | A named repository is a scope entry in its own right: a row in the gate's scope block under `requests.github.repository.<name>` and its file, joining at the `y` as a remote or a `repos` entry does, with no second `repos` line needed or implied (the scope is computed from remotes, `repos` and names; no key changes another, [GW-D38](#GW-D38)). Its grants are its table's, fanned out over the repositories the name resolves to and nothing else; a remote's repository gets the unqualified lines plus any table naming that remote. [OQ-BB18](boundary-broker.md#OQ-BB18) is unchanged: which repositories stays a workspace request, the access level stays the user's ceiling | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D64"></a>GW-D64 | The ceiling is per repository by the same table: `[allow.github.repository.<name>]` with `repo` (always declared, since a user config has no remotes) and its own `always` and `on_request`, written by `yolo allow github repository <name> <OWNER/REPO or wildcard>` and `yolo allow github repository.<name> [always|on_request] <token>`. The unqualified `[allow.github]` is the ceiling for every repository; a qualified table adds for the repositories its `repo` admits and never narrows. A request on R is within the ceiling when either lists the token. A skipped request names both commands | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D65"></a>GW-D65 | The cells live under a name too, `[requests.github.repository.<name>.working_set]` and `.all`, and [GW-D58](#GW-D58) holds within each repository's pair, checked at load. The hand-over request names the item's repository, `yolo request github working-set acme/infra#70`; a bare number is accepted only when the workspace's own repositories are one, else refused naming the form. Audit and `yolo config why` carry the full key, `requests.github.repository.infra.all.comment_issues`; the launch disclosure is one line per repository, a read-only one saying so | 2026-11-02 | [§3](#3-the-working-set), [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D66"></a>GW-D66 | Git takes the same qualification: `[requests.git.repository.<name>]` with `push`, `push_tags`, `new_branches`; a name is a remote's (any host) or declared with `repo = "<host>/<path>"` (git's identity, `gitproto.Identity`) or `"*"` ([GB-D25](git-broker.md#GB-D25)). Unqualified `[requests.git]` stays every remote of the workspace, on any host, as today. [RC-D26](readable-config.md#RC-D26) is unchanged for `repos`; a named github.com repository no remote names joins git's scope as RC-D26's two exact URLs and keeps its writes, since it was named for them; any other host must be a remote. `yolo request git repository …` writes it; `yolo request git repos` stays refused | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D67"></a>GW-D67 | Migration. No TOML key changes spelling: an unqualified line keeps its text, its meaning narrows to the workspace's own repositories, and the grants it loses on `repos` repositories show at the next gate as removed. `yolo config migrate` converts a legacy workspace `brokered.<source>.grants` entry to the unqualified key when its `repo` is the sole remote (today's `soleRemote`), otherwise to `repository.<name>` with `repo` written, the name being the remote that points at it, else the repository's last segment (`owner-repo` on a tie, as pack namespaces do), `any` for `"*"`; the grant set the lowering resolves is proved equal before and after, as [RC-D55](readable-config.md#RC-D55) proves. This retires `lower.go`'s "keep the old file for now" for several remotes, a `repos`-only repository and `"*"`; a legacy grant on a `repos` repository therefore keeps its writes through the name while the `repos` line stays | 2026-11-02 | [§8](#8-which-repository-a-line-is-for) | — |
| <a id="GW-D68"></a>GW-D68 | What the gate prints: every grant line ends with its key and file, `(yolo-jail.toml: requests.github.repository.infra.dispatch_workflows)`, `(yolo-jail.toml: requests.github.dispatch_workflows, on remote origin)`, `(~/.config/yolo-jail/config.toml: allow.github.always, for every workspace)`; a repository row names its origin the same way, and a `repos` row says `read only`. The label `(from your user config, for every workspace)` is then true only of lines whose key is under `allow` | 2026-11-02 | [§5](#5-every-stop-names-its-next-step), [§8](#8-which-repository-a-line-is-for) | — |
