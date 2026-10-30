---
title: "Where the GitHub line is drawn: which writes run unasked, per user and per workspace"
date: 2026-10-30
status: in-review
stage: DESIGN
next: "Rule OQ-GW1 to OQ-GW6; the vocabulary waits on OQ-GW1 and the Add rule on OQ-GW2"
depends-on:
  - boundary-broker.md
tags: [design, github, gh, broker, grants, approvals, config, toml]
summary: "Today a PR or issue in the workspace's working set takes every write but a merge unasked, so on a colleague's PR a human added, the agent can approve it or mark it ready without asking, and an armed auto-merge then merges it. This doc splits a write to a working-set item into four kinds a user can read and allow (comment, edit, approve, merge), composes them through the existing two config layers (the user's allow ceiling, the workspace's requests), adds a per-workspace narrowing that makes any kind ask, and treats an approval that would complete an armed auto-merge as the merge it is."
---

# Where the GitHub line is drawn: which writes run unasked, per user and per workspace

**Status:** 2026-10-30. Nothing built. Written against the tree at the landing of the GitHub
working set ([BB-D111](boundary-broker.md#BB-D111)), the same day, from the maintainer's ask:
*"some users are going to want this. So we need a clear way to scope this at both the user
config and workspace config level that will make everybody happy. … everybody's going to differ
on where they want the line drawn. And it may be on a per workspace basis."*

> **In short.** The line between "runs" and "asks" is drawn by the *kind* of write, and the
> same six kinds appear in both config layers: the user's config says which kinds any workspace
> may hold, a workspace asks for them within that ceiling, and a workspace can also say that a
> kind asks here whatever the user allows. Approving and merging are two of the kinds, and an
> approval that would complete an armed auto-merge counts as a merge.

**Why it matters.** The working set as built covers every write to a PR but its merge
([§1](#1-the-line-today-and-where-it-leaks)). A review found that on a PR a human added with
`yolo approve r-<id> --add`, `gh pr review --approve` and `gh pr ready` run unasked, and if the
author armed auto-merge the approval merges the PR: "a merge always asks" is true of the command
and false of the outcome.

**The shape.** Seven capabilities for the GitHub source, `read`, `create_pr`, `create_issue`,
and four **work kinds** *(coined here: the kinds of write to an item already in the working set,
as against creating one; not a grant on the repository)*: `comment`, `edit`, `approve`,
`merge`. The classifier assigns one from the `gh` argv alone ([§4](#4-the-work-kinds)). The two
config layers compose exactly as they do for `create_pr` today, plus one narrowing word
([§5](#5-two-layers-ceiling-asks-and-the-narrowing)).

**Cost.** A PR a human added covers less than today until the user allows kinds; an existing
`create_pr` user sees a one-time hint naming the two commands that restore today's feel
([§9](#9-migration-from-the-working-set-as-built)).

**Start at [§4](#4-the-work-kinds)** — the kinds. [§5](#5-two-layers-ceiling-asks-and-the-narrowing) is how the two files compose; everything else follows.

**Needs your ruling:** [OQ-GW1](#OQ-GW1), [OQ-GW2](#OQ-GW2), [OQ-GW3](#OQ-GW3),
[OQ-GW4](#OQ-GW4), [OQ-GW5](#OQ-GW5), [OQ-GW6](#OQ-GW6).

**Reads with:** [`boundary-broker.md`](boundary-broker.md) (the grant model this refines:
[§5.7](boundary-broker.md#57-permission-sets), [§5.8](boundary-broker.md#58-long-lived-access-and-temporary-access)
and its ledger), [`config-user.md` §6](config-user.md#6-what-a-jail-may-do-with-your-logins)
(the `allow` and `requests` tables these kinds live in),
[`git-broker.md`](git-broker.md) (the git working set, which has one kind and needs none of this),
[`github-write-scopes-plan.md`](github-write-scopes-plan.md) (the implementation plan's seed;
incomplete while any question here is open).

---

## 1. The line today, and where it leaks

What a GitHub write does today, as built and ledgered in
[BB-D111](boundary-broker.md#BB-D111) (verified in the tree the same day: `targetOf` and
`coveredWrite` in `internal/ghbroker`):

| The write | Today |
| :--- | :--- |
| `gh pr create`, `gh issue create` | Runs unasked under a `create_pr` or `create_issue` grant the user allows and the project requests; the new item joins the working set |
| Any write to a working-set PR or issue but a merge: comment, edit, review with any verdict, ready, close, reopen, requested reviewers, assignees | Runs unasked, for an item the agent created and for one a human added alike |
| `gh pr merge`, `PUT pulls/{n}/merge` | Always asks, per PR |
| Labels, projects, sub-issues, releases, settings, any other `gh api` write | Asks, as a write to the repository |
| Anything on an item outside the working set | Asks, per resource |

The leak is in the second row. **A working-set entry is one bit: in or out.** Everything but
a merge rides on it, including the two writes GitHub itself treats as a reviewer's verdict on
someone else's work: approving, and marking a draft ready. On a PR the agent created the bit is
harmless: GitHub refuses a review that approves the reviewer's own PR, and readying your own
draft is working your own PR. On a PR a human **added** — a colleague's, taken into the working
set with `yolo approve r-<id> --add` so the agent can review it — the same bit lets the agent
approve it unasked, and when the author has armed auto-merge, GitHub merges on that approval.
The merge the design made always ask happened without a merge command.

Two facts fix the frame:

- **The maintainer's ruling that a working set takes someone else's resource stands**
  ([BB-D92](boundary-broker.md#BB-D92)): *"you should also be able to widen your working set
  for a branch that somebody else made."* The question is not whether a human can add a
  colleague's PR; it is what the agent may then do to it without asking.
- **People want different lines, and one person wants different lines in different
  repositories.** Some want the agent to approve and merge freely; some want even a comment to
  ask; a sensitive repository wants the strict line while the rest of the same user's work does
  not. One bit cannot carry that. A short vocabulary can.

## 2. Principles

- **P1. The line is drawn by kind, read from the command.** Which kind a write is comes from
  its `gh` argv or REST method and path, never from its effect at run time, so the same command
  is always the same kind and a user can predict it from the table in
  [§4](#4-the-work-kinds). The one exception is named as an exception
  ([§4.2](#42-an-armed-auto-merge-makes-an-approval-a-merge)) and only ever moves a write
  toward asking.
- **P2. Few words, each one a line a user would draw.** A kind exists only where real users
  differ on it. Nobody splits "comment on a PR" from "comment on an issue"; many split
  "comment" from "approve". Seven capabilities in all, and a reader of either config file knows
  where the line is without opening a second document.
- **P3. The same words in both layers, and nothing new by default**
  ([BB-D104](boundary-broker.md#BB-D104), [BB-D105](boundary-broker.md#BB-D105)). A kind is
  spelled the same in the user's `allow.github` lists and the workspace's `requests.github`
  table, the ceiling is the user's, a workspace asks within it, and an empty config grants
  nothing; the kinds give a user something to allow, never something they already have.
- **P4. A workspace may narrow anything and widen only within the ceiling.** Making a write ask
  is always honored, from any file, by anyone who can edit it; making one run unasked needs the
  user's ceiling and the human's `y` at the config-change gate, as every request does today
  ([BB-D96](boundary-broker.md#BB-D96)).
- **P5. Every ask names the command that would have let it run.** The happy-path principle
  ([`happy-path-principle.md`](../reference/happy-path-principle.md)): a pending request's
  message, a `yolo check` finding and the launch disclosure each name the `yolo allow`,
  `yolo request` or `yolo approve` line that is the next step.

## 3. The axes, and which become vocabulary

Four axes could carry the line. Only two become words a user writes; the other two are already
carried by where a line is written.

| Axis | Values | Carried by |
| :--- | :--- | :--- |
| **What the write does** | comment, edit, request changes, approve, mark ready, close, reopen, merge, arm auto-merge, label, release, … | **The work kinds** ([§4](#4-the-work-kinds)): the long list folds into four words |
| **Whose item it is** | the agent created it; a human added it | **The working set already knows** (`By` on each entry: `create-pr`, `human`, or a grant id), so no word is added; the one place it changes a rule is the merge of an added PR ([§6.2](#62-what-a-humans-add-covers), [OQ-GW2](#OQ-GW2)) |
| **Which repository** | the workspace's own remotes; a listed `repos` entry | **The scope** ([§5.6 there](boundary-broker.md#56-the-repository-scope)): a kind never names a repository; it applies to every repository in the workspace's scope, as `create_pr` does |
| **Which workspace** | this project; this clone; all of them | **Which file holds the line**: the user's config is every workspace, the project's `yolo-jail.toml` is one project for everyone who clones it, the uncommitted `yolo-jail.local.toml` is one clone ([§5](#5-two-layers-ceiling-asks-and-the-narrowing)) |

A kind that named a repository (`approve` only in `o/r`) was considered and dropped: the
repository axis is the workspace's, and a user who wants a different line in one repository has
a workspace file for exactly that. Making the line per PR number is what `yolo approve` already
does, one resource at a time, and belongs in the store, not in config.

## 4. The work kinds

### 4.1 The table

The GitHub source defines seven capabilities. `read`, `create_pr` and `create_issue` are the
three that exist ([config-user.md appendix A.6](config-user.md#a6-the-grant-kinds-mapped)).
The four work kinds are new. Each is assigned from the canonical argv the broker runs, by the
rules below; a write on none of the rows is a write to the repository and asks, as today.

| Kind | `gh` commands | `gh api` writes on this repository | What it says about the item |
| :--- | :--- | :--- | :--- |
| `comment` | `pr comment`, `issue comment`; `pr review` with `--comment` or with no verdict flag | `POST pulls/{n}/comments`, `POST issues/{n}/comments`; `POST pulls/{n}/reviews` whose `event` field is `COMMENT` or absent | Says something. Changes no field and no state |
| `edit` | `pr edit`, `issue edit` (without `--add-project`/`--remove-project`); `pr ready`, with or without `--undo`; `pr close` (without `--delete-branch`), `pr reopen`, `issue close`, `issue reopen`; `pr review --request-changes` | `PATCH pulls/{n}`, `PATCH issues/{n}`; writes under `pulls/{n}/requested_reviewers` and `issues/{n}/assignees`; `POST pulls/{n}/reviews` with `event=REQUEST_CHANGES`; `POST pulls/{n}/reviews/{id}/events` with `event` `COMMENT` or `REQUEST_CHANGES` | Changes the item: its fields, who is on it, whether it is open or a draft, or a verdict that can only block |
| `approve` | `pr review --approve` | `POST pulls/{n}/reviews` with `event=APPROVE`; `POST pulls/{n}/reviews/{id}/events` with `event=APPROVE` | Says it may merge |
| `merge` | `pr merge`, every flag included (`--auto` arms a later merge, `--disable-auto` disarms one) | `PUT pulls/{n}/merge` | Merges it, or arms the merge |

Rules the table rests on, each a decision ledgered below:

- **A review's verdict is read from its flag or its `event` field.** `gh pr review` takes one
  of `--approve`, `--request-changes`, `--comment`; the broker reads which. For `gh api`, the
  `event` is read from a `-f`/`-F` field written plainly on the command line; a body from
  `--input`, or a field value read from a file (`-F event=@file`), is unseen, so the write has
  no readable verdict and asks as a write to the repository, exactly as a `pr create` carrying
  `--input` does today ([BB-D111](boundary-broker.md#BB-D111)). This narrows one row of
  today's behavior: a review posted through `gh api` with an unreadable event ran unasked on a
  working-set PR; now it asks. Fail closed is the direction every fence here moves
  ([GW-D2](#GW-D2)).
- **Marking ready is an `edit`, not an `approve`** ([GW-D3](#GW-D3)). It changes the item's
  state the way close and reopen do, it is routine on the agent's own drafts, and the one way
  it can finish a merge is through an armed auto-merge, which [§4.2](#42-an-armed-auto-merge-makes-an-approval-a-merge)
  catches by name. Putting it under `approve` would make a user who wants "ready my own drafts,
  never approve anyone's" unable to say so.
- **Requesting changes is an `edit`, not a `comment`** ([GW-D4](#GW-D4)). A `comment` must
  change nothing but add words; a request-changes verdict changes the PR's review state, even
  though it can only block. A user who allowed only `comment` on a colleague's PR would be
  surprised to find the agent had blocked it.
- **Everything under `pr merge` is `merge`** ([GW-D5](#GW-D5)), `--disable-auto` included,
  though disarming is harmless: "anything spelled `pr merge` is the merge kind" is a sentence a
  user can hold, and a finer rule buys nothing.
- **The sub-resource lists are [BB-D111](boundary-broker.md#BB-D111)'s.** What is written
  plainly in a path, what asks as a repository write (labels, projects, sub-issues, a
  percent-encoded or re-cased segment, a review dismissal, `update-branch`), and the one state
  read before each covered write all stand. This doc sorts the covered writes into kinds; it
  moves no fence ([BB-P8](boundary-broker.md#BB-P8)).

### 4.2 An armed auto-merge makes an approval a merge

The trigger case, resolved: **an `approve`, or an `edit` that marks a draft ready, on a PR
whose auto-merge is armed is treated as a `merge` of that PR.** It runs unasked only where a
merge of that PR would ([§6.2](#62-what-a-humans-add-covers)); otherwise it asks as a merge,
and the message says why:

```console
$ gh pr review 32 --approve
github-broker: approving PR o/r#32 would complete its armed auto-merge, so it asks as a merge:
  the request is r-7k2m. On the host: `yolo approve r-7k2m` allows it once, `--for 15m` for 15 minutes.
```

- **The read is the one the broker already makes.** Before each covered write it reads the
  item's state once ([BB-D111](boundary-broker.md#BB-D111)) to learn whether a PR is merged;
  for an `approve` or a ready the read is of `pulls/{n}`, whose `auto_merge` field says whether
  one is armed, and a failed read asks, as today.
- **It only ever moves a write toward asking** (P1's exception). A write classified `approve`
  from its argv is never made `comment` by state; it is made `merge` by one named fact.
- **The race is the author's, not the agent's.** Auto-merge armed between the read and the
  approval merges the PR; the agent cannot arm it itself without the `merge` kind, since
  `pr merge --auto` is `merge`.
- Whether GitHub lets auto-merge be armed on a draft is not verified here, and the rule does
  not depend on it: a ready that finds no armed auto-merge is an `edit`, as the table says.

The alternative, documenting the hazard beside the `approve` kind and letting users who allow
`approve` accept it, leaves "a merge always asks" false of the outcome for every reviewer
persona in [§7](#7-what-people-write) ([OQ-GW5](#OQ-GW5) asks the owner to confirm the rule).

## 5. Two layers: ceiling, asks, and the narrowing

The kinds use the two layers the config already has
([config-user.md §6](config-user.md#6-what-a-jail-may-do-with-your-logins),
[BB-D96](boundary-broker.md#BB-D96)), with one word added for narrowing.

| Where | Who writes it, and how | What it says | What it needs |
| :--- | :--- | :--- | :--- |
| User config, `[allow.github] always = [...]` | The user, on the host: `yolo allow github <kind>` | Every workspace's jail gets this kind on its working set | Nothing more. Shown in each workspace's config-change diff as `+ github: comment` ([BB-D105](boundary-broker.md#BB-D105)) |
| User config, `[allow.github] on_request = [...]` | `yolo allow github on_request <kind>` | A workspace may ask for this kind | The workspace's request, and the human's `y` |
| Project config, `[requests.github] <kind> = true` | Anyone who can edit `yolo-jail.toml`, the agent included: `yolo request github <kind>` | This project asks for the kind | The kind in the user's `on_request` (or `always`, where the line is redundant and said so), and the human's `y` at the gate. Outside the ceiling it is reported and skipped, never an error |
| Project or local config, `[requests.github] <kind> = "ask"` | Anyone: `yolo request github <kind> --ask`, with `--local` for this clone only | **Here, this kind asks a human, whatever the user's config allows** | Nothing. A narrowing is always honored ([GW-D6](#GW-D6)) |
| Local config, `yolo-jail.local.toml`, same keys | The user, for one clone: the same verbs with `--local` | A per-clone line, uncommitted | Merged after the project file, so its line wins ([GW-D7](#GW-D7)) |

**Resolution, per kind, per workspace** (the pure decision; the broker evaluates it against the
live scope file under the store lock, as it does `create_pr` today):

1. If the kind is `"ask"` in the effective workspace config (the project file, then the local
   file, last wins), **it asks** here. Nothing else is consulted.
2. Else if the kind is in the user's `always`, **it runs unasked** on the working set.
3. Else if the kind is in the user's `on_request` and the workspace requests it `true` and a
   human approved that at the gate, **it runs unasked** on the working set.
4. Else **it asks**, per resource, with `yolo approve`'s answers.

Three consequences worth stating:

- **A workspace narrows below the user's `always`.** A sensitive repository's `yolo-jail.toml`
  says `comment = "ask"`, and in that project even a comment asks, for everyone who clones it,
  whatever their own config. Removing that line is a widening back to the ceiling and shows at
  the gate as `+ github: comment` for the human's `y`; adding it shows as `– github: comment
  asks here` and needs nobody's `y` ([GW-D6](#GW-D6)).
- **A workspace widens only to the ceiling.** `approve = true` in a project whose user has no
  `approve` in `on_request` is reported and skipped with the `yolo allow github on_request
  approve` line, as `push` is for git today ([config-user.md §6](config-user.md#6-what-a-jail-may-do-with-your-logins)).
- **`read` takes no narrowing.** `read = "ask"` is a `yolo check` error: reads never ask, and
  the way to keep a jail from reading GitHub at all is the user's pack selection
  (`yolo pack remove github`), not this table; `packs` is one list in the user's config, with no
  per-project form ([GW-D8](#GW-D8)).

**Defaults.** Nothing. `[allow.github]` absent means every write asks and `read` itself is not
granted ([BB-D104](boundary-broker.md#BB-D104)). A user who runs only
`yolo allow github on_request create_pr` (after `yolo allow github read`) gets, in a project that
requests it: PRs created unasked, and **every comment or edit on them asking**, because no work
kind is allowed. That is what the config says, and the verb says it too: `yolo allow github
on_request create_pr` offers the usual pairing in the same run, *"Also allow `comment` and `edit`
on every workspace's working set, so the jail can tend the PRs it creates? [Y/n]"*, writing both
on `y` and printing the two commands on `n` or without a terminal ([GW-D9](#GW-D9);
[OQ-GW3](#OQ-GW3) asks whether creation should instead carry the two kinds by itself).

## 6. The working set under kinds

The working set stays what it is ([§1.3 there](boundary-broker.md#13-terms)): the PRs and
issues a workspace may write to unasked, joined by creation under a user grant or by a human's
Add, left on merge or revocation. What changes is that **an entry covers the kinds the workspace
holds, not every write.**

### 6.1 What covers a write, in order

For a write of kind K on item N of repository R, after the fences nothing here moves (refused
commands, the scope, the read floor):

1. **A creation under a user grant** (`create_pr`, `create_issue`): unchanged.
2. **A working-set entry on N, and K resolves to "runs unasked" in this workspace**
   ([§5](#5-two-layers-ceiling-asks-and-the-narrowing)), and the one state read finds N open
   and, for an `approve` or a ready, no armed auto-merge ([§4.2](#42-an-armed-auto-merge-makes-an-approval-a-merge)):
   runs, audited `held: <kind>` with the entry's id.
3. **A timed grant** from `yolo approve r-<id> --for <duration>`: covers **the request's kind
   on its resource**, and nothing wider, for the time ([BB-D82](boundary-broker.md#BB-D82);
   [GW-D10](#GW-D10)). A `--for 15m` on a comment request does not cover an approval.
4. **Allow once**, as built.
5. Otherwise **a request**, whose message names the kind, the item, and the commands
   ([§8](#8-every-stop-names-its-next-step)).

### 6.2 What a human's Add covers

`yolo approve r-<id> --add` puts a colleague's PR or issue into the working set. Under this
design the entry covers **the work kinds the workspace holds** — comment and edit for most
users, approve where allowed — and **a merge of an added PR asks per PR, from config or from
nowhere**: the `merge` kind covers PRs the agent created, and a colleague's PR is merged only on
a `yolo approve` answer (`--for` for a window, or once). This is [OQ-GW2](#OQ-GW2); the
reasoning:

- Merging a PR you did not open is the one act every persona in [§7](#7-what-people-write)
  wants a human for, and the one the auto-merge leak was about.
- An issue entry never covers `approve` or `merge`: neither exists on an issue
  ([GW-D11](#GW-D11)).
- The Add still happens when the workspace holds no work kind; the entry is a fact about the
  workspace, and coverage follows the config later. The command says so:

```console
$ yolo approve r-7k2m --add
Allowed r-7k2m: added PR acme/app#32 to the working set of /home/m/acme, across restarts.
  Your config grants no kind on the working set yet, so every write to it still asks;
  `yolo allow github comment` and `yolo allow github edit` cover comments and edits in every
  workspace, or `yolo request github comment` in this one.
```

### 6.3 What the agent's own creation covers

A PR or issue the agent created under `create_pr` or `create_issue` is a working-set entry like
any other: it covers the kinds the workspace holds. Whether the creation grant itself should
carry `comment` and `edit` on what it creates, so that `create_pr` alone restores today's feel on
the agent's own PRs, is [OQ-GW3](#OQ-GW3). `approve` on its own PR is refused by GitHub either
way; `merge` on its own PR runs only under the `merge` kind.

## 7. What people write

Each persona's files and the verbs that write them. `read` is in every one because a write
request takes effect only once the source's read is allowed (built: the request is skipped
otherwise, naming the line).

**Cautious: even comments ask.** Nothing but reading. Creation asks per PR; every write asks per
resource; an Add covers nothing until a kind is allowed.

```toml
# ~/.config/yolo-jail/config.toml        yolo allow github read
[allow.github]
always = ["read"]
```

**Default: tend its own work, never judge anyone's.** The agent creates PRs where a project
asks, comments on and edits what is in the working set, and asks before approving or merging.

```toml
# ~/.config/yolo-jail/config.toml        yolo allow github read; yolo allow github comment;
[allow.github]                           # yolo allow github edit; yolo allow github on_request create_pr
always = ["read", "comment", "edit"]
on_request = ["create_pr", "create_issue"]

# acme/yolo-jail.toml                    yolo request github create_pr
[requests.github]
create_pr = true
```

**Let it merge its own PRs.** The default plus `merge`, on request. Approving stays out, so a
colleague's PR a human adds is commented on and edited, and its merge or approval asks.

```toml
# ~/.config/yolo-jail/config.toml        yolo allow github on_request merge
[allow.github]
always = ["read", "comment", "edit"]
on_request = ["create_pr", "create_issue", "merge"]

# acme/yolo-jail.toml                    yolo request github merge
[requests.github]
create_pr = true
merge = true
```

**Reviewer's helper, in one workspace.** The user adds colleagues' PRs and wants approvals
unasked there, and nowhere else. `approve` is allowed on request and asked for in the one
project; an approval that would complete an armed auto-merge still asks as a merge.

```toml
# ~/.config/yolo-jail/config.toml        yolo allow github on_request approve
[allow.github]
always = ["read", "comment", "edit"]
on_request = ["create_pr", "approve"]

# team/yolo-jail.toml                    yolo request github approve
[requests.github]
approve = true
```

**A sensitive repository: the strict line for everyone who clones it.** The project narrows
below every contributor's `always`. Nobody's `y` is needed to add these lines; removing one shows
at the gate.

```toml
# secrets-service/yolo-jail.toml         yolo request github comment --ask; yolo request github edit --ask
[requests.github]
comment = "ask"
edit = "ask"
```

**A per-clone exception.** One person, one checkout, within their own ceiling, uncommitted.

```toml
# acme/yolo-jail.local.toml              yolo request github merge --local
[requests.github]
merge = true
```

## 8. Every stop names its next step

| The stop | What the agent reads, and the line it names |
| :--- | :--- |
| A kind the workspace does not hold, on a working-set item | `github-broker: a comment on PR o/r#32 asks here: this workspace holds no `comment` on its working set (r-7k2m). On the host: `yolo approve r-7k2m` once, `--for 15m`, or for every workspace `yolo allow github comment`.` |
| A kind narrowed to `"ask"` | `… asks here: yolo-jail.toml sets requests.github.comment = "ask" (r-7k2m). `yolo approve r-7k2m` once or `--for 15m`; to lift the line, `yolo config unset requests.github.comment --project`, then `yolo config apply`.` |
| A merge of a PR a human added | `… merging PR o/r#32, which a human added, asks per PR (r-7k2m): `yolo approve r-7k2m` once or `--for 15m`.` |
| An approval or ready on a PR with auto-merge armed | [§4.2](#42-an-armed-auto-merge-makes-an-approval-a-merge)'s message |
| A review through `gh api` with an unreadable `event` | `… this review's verdict is not written on the command line (--input or @file), so it asks as a write to o/r (r-7k2m). Write the event as -f event=COMMENT to have it classified, or `yolo approve r-7k2m`.` |
| `yolo check` on a project requesting `approve` outside the ceiling | `yolo-jail.toml:4: this project requests approve. Your config does not allow approve on request, so this project runs without it. To allow it for every project you work on: yolo allow github on_request approve` (the built shape for `push`) |
| `yolo check` on `create_pr` held with no work kind | `allow.github allows create_pr, and no kind lets a jail tend the PRs it creates: every comment and edit on them will ask. The usual pairing: yolo allow github comment; yolo allow github edit` |
| `yolo check` on `read = "ask"` | `requests.github.read: reads never ask. To keep your jails from reading GitHub, take github out of packs in your config: yolo pack remove github` |
| The launch disclosure | `github-broker: this workspace holds create_pr on acme/app; on its working set comment and edit run unasked, approve and merge ask; working set: 2 PRs` |

The verbs these lines name all exist (`yolo allow`, `yolo request`, `yolo approve`,
`yolo config unset`, `yolo pack remove`, each verified in `internal/cli`); `--ask` on `yolo request` is the one new flag
([GW-D12](#GW-D12)), and the advice follows the format of the file it names
([RC-D40](readable-config.md#RC-D40)).

## 9. Migration from the working set as built

Nothing has shipped in a release that carries today's behavior, so this is a mapping, not a
converter. The store needs no migration: each entry already records what added it (`By`), and a
timed entry already carries its kind.

| Today ([BB-D111](boundary-broker.md#BB-D111)) | Under this design |
| :--- | :--- |
| `create_pr` / `create_issue` allowed and requested: creation runs unasked; the new item joins the working set | Unchanged |
| Every non-merge write to a working-set item runs unasked | Only the kinds the workspace holds run unasked. A user with `create_pr` and no work kind sees the `yolo check` pairing hint once ([§8](#8-every-stop-names-its-next-step)) and runs two commands to have comments and edits on the working set again; approvals, which ran unasked, ask until `approve` is allowed |
| A merge always asks | A merge of a PR the agent created runs unasked under `merge`; a merge of a PR a human added asks ([OQ-GW2](#OQ-GW2)); an approval or ready that would complete an armed auto-merge asks as a merge |
| `--for <duration>` covers every non-merge write to the item for the time | Covers the request's kind on the item for the time |
| `--add` covers every non-merge write to the added item | Covers the kinds the workspace holds, and says so |
| A `gh api` review with an unreadable `event` runs unasked on a working-set PR | Asks, as a write to the repository |
| `grant_kinds` in a legacy user config | Already `on_request` ([config-user.md A.6](config-user.md#a6-the-grant-kinds-mapped)); the work kinds have no legacy spelling, so no rename |

## 10. What this does not cover

- **Git.** The git working set has one kind of write, a push, and its line is the branch
  ([`git-broker.md` §5.1](git-broker.md#51-the-working-set-of-pushable-branches)). Nothing here
  reaches it.
- **Repository-level writes.** Labels, releases, workflow runs, settings and every write the
  table does not name keep asking per resource and never join the working set
  ([BB-D95](boundary-broker.md#BB-D95)). A `release` kind was not proposed: no user asked, and
  the ask path exists.
- **The scope and the refusals** ([BB-P8](boundary-broker.md#BB-P8)). A kind never widens the
  repository scope and never reaches a refused command.
- **Who may add a PR to the working set.** A human, with `yolo approve --add`, as ruled
  ([BB-D92](boundary-broker.md#BB-D92)). This doc changes what the Add covers, not who makes it.
- **Keeping a jail off GitHub entirely.** That is the user's pack selection, not a kind.
- **Per-PR lines in config.** `yolo approve` draws those, one resource at a time, in the store.

## 11. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Keep one bit; take `approve` and `ready` out of the working set's coverage so they always ask.** Closes the trigger with no vocabulary | Rejected: it draws one line for everyone, and the ask is for everybody's line. The reviewer persona wants approvals unasked; the cautious persona wants comments asked |
| **Two kinds, `work` and `merge`, where `work` is everything but a merge and an approval** | Rejected: `approve` must be a word a user can allow or withhold on its own, since it is the one that differs between the reviewer and the default persona; and `comment` against `edit` is where a colleague's PR differs from your own. Four is the count at which every persona in [§7](#7-what-people-write) is one line; [OQ-GW1](#OQ-GW1) confirms it |
| **Five or more kinds**: split `review` (request changes) from `comment`, or `close` from `edit`, or `ready` from both | Rejected for v1: no persona needed the split, and each word a reader must learn costs more than the line it draws. Add one when a real user asks |
| **Origin as a suffix or second list** (`merge_own`, or `[allow.github] added = [...]`) | Rejected: it doubles the vocabulary for a distinction that matters to one kind. The working set already knows who added each item; the one rule that turns on it is stated in words ([§6.2](#62-what-a-humans-add-covers)) |
| **Choose the kinds at Add time** (`yolo approve r-<id> --add comment,edit`) | Rejected as the model, kept as a possible later flag: a per-resource choice does not give the user a line they can read in their config, and the human's Add already has a config to read the line from |
| **A separate workspace table for narrowing** (`[ask.github] kinds = [...]`) | Not taken, pending [OQ-GW4](#OQ-GW4): one table, `requests.github`, holding `true` and `"ask"` keeps a kind's whole story on one line of one table |
| **Document the auto-merge hazard instead of catching it** | Rejected, pending [OQ-GW5](#OQ-GW5): it leaves "a merge always asks" false of the outcome for every user who allows `approve` |
| **A kind that names a repository** | Rejected ([§3](#3-the-axes-and-which-become-vocabulary)): the workspace file is where a repository's line goes |

## 12. What done looks like

- `gh pr review 32 --approve` on a PR a human added, in a workspace whose user allows
  `comment` and `edit` only, files a request whose message names `approve` and the `yolo allow`
  line; `gh pr comment 32` on the same PR runs and is audited `held: comment`.
- The same approval on a PR whose `auto_merge` is non-null, in a workspace that allows
  `approve`, asks as a merge and says why; with auto-merge disarmed it runs.
- A project whose `yolo-jail.toml` sets `comment = "ask"` asks for a comment on the agent's own
  PR, for every user, and the request's message names the file and the `unset` line; deleting
  the line shows `+ github: comment` at the gate and runs nothing until `y`.
- `gh pr merge 32` on a PR the agent created runs under `merge`; on a PR a human added it asks,
  whatever the config.
- `yolo allow github on_request create_pr` on a terminal offers the pairing; declined, the user
  config holds exactly `create_pr`, and `yolo check` prints the pairing hint once per run.
- `yolo approve r-<id> --for 15m` on a comment request leaves an approval on the same PR asking
  inside the window.
- The launch disclosure line names the kinds that run and the kinds that ask, and
  `yolo config why requests.github.comment` names the file and line that set `"ask"`.

## 13. Open Questions

1. 💬 **OQ-GW1: Are comment, edit, approve and merge the right four words for what the agent may do to a PR without asking?**

   Priya allows the jail to create PRs. It opens one, then runs `gh pr comment 12`,
   `gh pr ready 12`, and on a teammate's PR she added, `gh pr review 40 --approve`. Today all
   three run; she cannot say "comment yes, approve no". The table in [§4.1](#41-the-table)
   gives her four words: `comment` (says something), `edit` (changes fields, open/draft state, or
   requests changes), `approve`, `merge`. Fewer words draw fewer lines; more words are more to
   learn before reading your own config.

   - **A — Four, as tabled.** Her config reads `always = ["read", "comment", "edit"]`; approve
     and merge ask. Requesting changes counts as `edit`, marking ready as `edit`.
   - **B — Three: `work`, `approve`, `merge`**, where `work` is comment and edit together. One
     word fewer; a colleague's PR she adds can be edited by anyone who may comment on it.
   - **C — Five or more**, splitting request-changes or ready into their own words. Each persona
     gets a finer line and a longer table.

   <!-- vantage: question id=OQ-GW1 leaning="A — four words: every persona in §7 is one line at four, and the split of comment from edit is where a colleague's PR differs from your own." -->

   _Leaning:_ A. Every persona in [§7](#7-what-people-write) is one line at four, and the split
   of `comment` from `edit` is exactly where a colleague's PR differs from your own.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-GW2: When a human adds a colleague's PR to the working set, what may the agent do to it without asking, and may it ever merge it?**

   Matt adds a teammate's PR with `yolo approve r-7k2m --add` so the agent can review it. Today
   the agent may then do anything but merge it, approval included; a merge asks. He wants the
   agent to comment and edit, and he has `merge` allowed so it merges its own PRs. The question
   is what the Add hands over.

   - **A — The kinds his config allows, and a merge of an added PR always asks.** The agent
     comments and edits; approving asks unless he allowed `approve`; merging the teammate's PR
     asks per PR, answered `yolo approve r-… --for 15m`, whatever `merge` says.
   - **B — The same as a PR the agent created**, today's rule. With `merge` allowed, the agent
     may merge the teammate's PR unasked; with `approve` allowed, approve it.
   - **C — A, plus a `--add` flag naming the kinds** for this one item.

   <!-- vantage: question id=OQ-GW2 leaning="A — the kinds the config allows, and merging a PR someone else opened always asks per PR: it is the one act every persona wants a human for." -->

   _Leaning:_ A. Merging a PR someone else opened is the one act every persona wants a human
   for, and `--for` keeps it one command when he does want it.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-GW3: Does allowing the jail to create PRs still let it comment on and edit the PRs it creates, or must those be allowed in words?**

   Dana runs `yolo allow github read` and `yolo allow github on_request create_pr`, and her
   project requests `create_pr`. The agent opens a PR, then runs `gh pr comment 12` to post the
   CI result. Today it runs: creating implied working ([OQ-BB19](boundary-broker.md#15-decision-ledger)).
   Under explicit kinds it asks until she allows `comment`.

   - **A — Explicit.** Her config says exactly what runs. The verb offers the pairing when she
     allows `create_pr` (*"Also allow comment and edit? [Y/n]"*), and `yolo check` names the
     two commands when she declines. A cautious user writes `always = ["read"]` and reads it as
     "everything else asks", which is true.
   - **B — Creation carries comment and edit on what it creates.** `create_pr` alone gives
     today's feel on the agent's own PRs. The words `comment` and `edit` then matter only for PRs
     a human adds, and a cautious user needs `comment = "ask"` in a workspace to stop comments
     on the agent's own PRs.

   <!-- vantage: question id=OQ-GW3 leaning="A — explicit: what the config says is what runs, and the verb's one-question pairing makes the common path one command anyway." -->

   _Leaning:_ A. What the config says is what runs, and the verb's one-question pairing makes
   the common path one command anyway. B keeps the earlier ruling's convenience at the cost of
   one word meaning two things.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-GW4: How does a project say "even comments ask here"?**

   A maintainer of a sensitive service wants every GitHub write from any contributor's jail to
   ask, whatever that contributor's own config allows. Today there is no such line: a user's
   `always` reaches every project. The line is a narrowing, so it needs nobody's approval.

   - **A — A value in the table that already holds requests:** `[requests.github] comment =
     "ask"`, written by `yolo request github comment --ask`. One table tells a kind's whole
     story: `true` asks for it, `"ask"` turns it off here, absent leaves it to the user's config.
   - **B — A table of its own:** `[ask.github] kinds = ["comment", "edit"]`. Reads as a list of
     what asks; a second table and a second verb to learn.
   - **C — `false` in the requests table.** No new word, but `comment = false` reads as "not
     requested", which is what absent already means, so the reader cannot tell a decline from a
     default.

   <!-- vantage: question id=OQ-GW4 leaning="A — one table, one verb: true asks for the kind, the ask value makes it ask here, absent leaves it to the user." -->

   _Leaning:_ A. One table, one verb; C is ruled out because a reader cannot tell it from
   absent.

   **Answer:**

   > _(empty — fill in when decided)_

5. 💬 **OQ-GW5: Should approving, or marking ready, a PR whose auto-merge is armed count as merging it?**

   Sam allows `approve` so the agent can approve teammates' PRs he adds. A teammate opens a PR
   and arms auto-merge; the agent runs `gh pr review 40 --approve`. Today that runs, and GitHub
   merges the PR. The design reads the PR's state before the write anyway.

   - **A — Yes.** The broker reads `auto_merge` in that read; armed, the approval asks as a
     merge, and the message says *"approving PR #40 would complete its armed auto-merge"*. Sam
     answers `yolo approve r-… --for 15m` when he means it. One more field read, no new command.
   - **B — No; say so in the `approve` kind's description.** Sam allowed approvals; a merge that
     follows is GitHub's doing. Simpler, and "a merge always asks" stops being true of the
     outcome for anyone who allows `approve`.

   <!-- vantage: question id=OQ-GW5 leaning="A — yes: it is the trigger case itself, costs one field in a read the broker already makes, and keeps 'a merge asks' true of what happens." -->

   _Leaning:_ A. It is the trigger case itself, it costs one field in a read the broker already
   makes, and it keeps "a merge asks" true of what happens, not only of what was typed.

   **Answer:**

   > _(empty — fill in when decided)_

6. 💬 **OQ-GW6: May the user's `always` list hold a work kind, so comments run unasked in every workspace?**

   Lee wants the agent to comment on and edit working-set PRs in every project without touching
   each project's file, so he runs `yolo allow github comment`, which writes `always =
   ["read", "comment"]`. The two lists have the same shape for every capability
   ([config-user.md §6](config-user.md#6-what-a-jail-may-do-with-your-logins)), but
   [OQ-BB23](boundary-broker.md#OQ-BB23) is still open on whether a user grant of a write kind
   may reach every workspace at once.

   - **A — Yes, for the work kinds.** `always` means every workspace's working set; each
     workspace's gate still shows it as `+ github: comment`. A sensitive project narrows with
     `"ask"`.
   - **B — No: work kinds only in `on_request`.** Lee writes `comment = true` in every project
     he works in, and approves each at its gate.

   <!-- vantage: question id=OQ-GW6 leaning="A — yes: the personas are one line only if always may hold them, each gate still shows it, and a project can narrow it." -->

   _Leaning:_ A. The personas in [§7](#7-what-people-write) are one line only if `always` may
   hold them, each workspace's gate still shows it, and a project can narrow it.

   **Answer:**

   > _(empty — fill in when decided)_

## 14. Decision Ledger

Implementation decisions this doc makes, each with one obvious answer. A ruling on a question
above lands here when it arrives.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="GW-D1"></a>GW-D1 | The work kinds are capabilities of the GitHub source, spelled `comment`, `edit`, `approve`, `merge` in `allow.github` and `requests.github`, the spelling `create_pr` already uses; the pack manifest declares them as it declares `create_pr` | 2026-10-30 | [§4.1](#41-the-table) | — |
| <a id="GW-D2"></a>GW-D2 | A review's verdict is read only from a `pr review` flag or a plainly written `-f`/`-F` `event` field; `--input` or an `@file` value leaves it unreadable, and the write asks as a repository write | 2026-10-30 | [§4.1](#41-the-table) | — |
| <a id="GW-D3"></a>GW-D3 | `pr ready`, with or without `--undo`, is `edit` | 2026-10-30 | [§4.1](#41-the-table) | — |
| <a id="GW-D4"></a>GW-D4 | `pr review --request-changes`, and the `REQUEST_CHANGES` event, are `edit`; a `comment` changes nothing but adds words | 2026-10-30 | [§4.1](#41-the-table) | — |
| <a id="GW-D5"></a>GW-D5 | Every `pr merge` invocation is `merge`, `--auto` and `--disable-auto` included | 2026-10-30 | [§4.1](#41-the-table) | — |
| <a id="GW-D6"></a>GW-D6 | A narrowing (`"ask"`) is honored from any file without the ceiling or a `y`; its removal is a widening and appears at the gate as a `+` line | 2026-10-30 | [§5](#5-two-layers-ceiling-asks-and-the-narrowing) | — |
| <a id="GW-D7"></a>GW-D7 | The local file merges after the project file for `requests.github`, as it does for every key; the last file wins per kind | 2026-10-30 | [§5](#5-two-layers-ceiling-asks-and-the-narrowing) | — |
| <a id="GW-D8"></a>GW-D8 | `requests.github.read = "ask"` is a `yolo check` error naming `yolo pack remove github`, the user-config verb; `packs` has no per-project form | 2026-10-30 | [§5](#5-two-layers-ceiling-asks-and-the-narrowing) | — |
| <a id="GW-D9"></a>GW-D9 | `yolo allow github [on_request] create_pr` on a terminal offers to also allow `comment` and `edit` in `always`; declined, or without a terminal, it prints the two commands, and `yolo check` repeats the pairing hint while `create_pr` is held with no work kind | 2026-10-30 | [§5](#5-two-layers-ceiling-asks-and-the-narrowing) | — |
| <a id="GW-D10"></a>GW-D10 | A timed grant (`--for`) covers the request's kind on its resource and no other kind | 2026-10-30 | [§6.1](#61-what-covers-a-write-in-order) | — |
| <a id="GW-D11"></a>GW-D11 | An issue's working-set entry never covers `approve` or `merge` | 2026-10-30 | [§6.2](#62-what-a-humans-add-covers) | — |
| <a id="GW-D12"></a>GW-D12 | `yolo request github <kind> --ask [--local]` writes the narrowing; `yolo config set requests.github.<kind> ask` is the same edit, and advice names the former | 2026-10-30 | [§8](#8-every-stop-names-its-next-step) | — |
| <a id="GW-D13"></a>GW-D13 | A covered write is audited `held: <kind>` with the covering entry's id; the launch disclosure names the kinds that run and the kinds that ask | 2026-10-30 | [§8](#8-every-stop-names-its-next-step) | — |
