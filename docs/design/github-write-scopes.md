---
title: "Where the GitHub line is drawn: which writes run unasked, per user and per workspace"
date: 2026-10-30
status: in-review
stage: DESIGN
next: "Build the kinds, the wide grants and the working-set request, then write the implementation plan's build record"
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
> kind asks here whatever the user allows. Approving and merging are two of the kinds; an
> approval is an approval even when the PR's auto-merge is armed, and the `approve` kind says so
> ([OQ-GW5](#OQ-GW5)).

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

**Cost.** A write the agent did not create covers less than today until the user allows a
kind for it; `create_pr` and `create_issue` keep today's comment and edit on what they create
([OQ-GW3](#OQ-GW3)).

**Start at [§4](#4-the-work-kinds)** — the kinds. [§5](#5-two-layers-ceiling-asks-and-the-narrowing) is how the two files compose; everything else follows.

**Every question is ruled** ([§14](#14-decision-ledger)). `yolo approve --add` is removed, and an
agent reaches an item it did not create by asking for it by name ([§6.2](#62-reaching-an-item-the-agent-did-not-create)).

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
  ([§4.2](#42-an-armed-auto-merge-an-approval-stays-an-approval)) and only ever moves a write
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
| **Whose item it is** | the agent created it; a human added it | **The working set already knows** (`By` on each entry: `create-pr`, `human`, or a grant id), so no word is added; the one place it changes a rule is the merge of an added PR ([§6.2](#62-reaching-an-item-the-agent-did-not-create), [OQ-GW2](#OQ-GW2)) |
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
  it can finish a merge is through an armed auto-merge, which [§4.2](#42-an-armed-auto-merge-an-approval-stays-an-approval)
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

### 4.2 An armed auto-merge: an approval stays an approval

**An `approve`, or an `edit` that marks a draft ready, on a PR whose auto-merge is armed is
classified by its argv like any other write: it is an `approve` or an `edit`, not a `merge`**
([OQ-GW5](#OQ-GW5), ruled B: *"it's less surprising really"*). If GitHub then merges the PR,
that merge is the author's armed auto-merge completing, not a merge the agent ran.

- **The `approve` kind says so where it is granted.** The config-change gate line for `approve`
  (and for a wide approval grant) states that an approval can complete a PR's armed auto-merge,
  so the reach is read where it is allowed rather than discovered afterwards.
- **The agent cannot arm one itself without `merge`:** `pr merge --auto` is `merge`
  ([GW-D5](#GW-D5)).
- **"A merge always asks" is a statement about what the agent runs**, not about what GitHub does
  next: a user who allows `approve` on a PR whose author armed auto-merge has allowed that
  approval to finish the merge.

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
requests it: PRs created unasked, and **comments and edits on the PRs it created unasked too**:
`create_pr` and `create_issue` carry `comment` and `edit` on what they create
([OQ-GW3](#OQ-GW3), ruled B). Approving and merging are never carried by creation.

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
   ([§5](#5-two-layers-ceiling-asks-and-the-narrowing)), or N was created by this workspace
   under `create_pr`/`create_issue` and K is `comment` or `edit` ([OQ-GW3](#OQ-GW3)), and the
   one state read finds N open: runs, audited `held: <kind>` with the entry's id.
3. **A timed grant** from `yolo approve r-<id> --for <duration>`: covers **the request's kind
   on its resource**, and nothing wider, for the time ([BB-D82](boundary-broker.md#BB-D82);
   [GW-D10](#GW-D10)). A `--for 15m` on a comment request does not cover an approval.
4. **Allow once**, as built.
5. Otherwise **a request**, whose message names the kind, the item, and the commands
   ([§8](#8-every-stop-names-its-next-step)).

### 6.2 Reaching an item the agent did not create

> **Ruled 2026-11-02: `yolo approve --add` is removed.** The owner: *"I don't like this add
> flag because it's confusing. It rides along with a grant, or it rides along with an approval,
> and depending on what the approval is, it does different things, and you're trying to guess
> context. … Let's remove it. We can force the agent to take some other path here."*

Kurt files bug #70; the agent fixes it and wants to comment on it and close it. Four paths reach
it, and each is one thing a human answers in one way ([OQ-GW2](#OQ-GW2)):

| Path | The agent runs | The human answers | It covers |
| :--- | :--- | :--- | :--- |
| **Allow once** | the write (`gh issue close 70`), which files `r-<id>` | `yolo approve r-<id>` | that exact command, once, within 15 minutes |
| **For a time** | the same write | `yolo approve r-<id> --for 1h` | the request's kind on that item, at most 24 hours ([GW-D10](#GW-D10)) |
| **Join the working set** | `yolo request github working-set issue 70` (or `pr 32`), which files `r-<id>` naming only that | `yolo approve r-<id>` | `comment` and `edit` on that item until it closes or merges, or `yolo approve --revoke w-<id>` ends it; never `approve` or `merge` |
| **Standing, per repository** | `yolo request github comment_any issues` (and `edit_any`, `approve_any`, `merge_any`) | `yolo config apply` on the host | that kind on every item of the repository |

The third path is the owner's: *"can't the agent request it to be added to the working set
somehow?"* It is what `--add` did, with the ambiguity taken out: the request says it is a request
to join, so a plain `yolo approve` means exactly one thing, and an approval of a write never adds
anything. An entry joined this way is recorded `By: human` as the Add's were, and an issue's
entry never covers `approve` or `merge` ([GW-D11](#GW-D11)). **A merge of a PR the agent did not
create is reached only by Allow once, `--for`, or `merge_any`**: joining the working set never
carries it.

The refusal of a write to such an item names all four, with the human's commands each alone on a
line ([§8](#8-every-stop-names-its-next-step)). Working-set entries a human added with `--add`
before its removal are kept until revoked ([GW-D34](#GW-D34)).

### 6.3 What the agent's own creation covers

A PR or issue the agent created under `create_pr` or `create_issue` is a working-set entry, and
**the creation grant carries `comment` and `edit` on it** ([OQ-GW3](#OQ-GW3), ruled B: *"agents
should be able to comment on edit on PRs they create. We can just specify that as part of create
PR."*). A project that wants even those to ask narrows with `comment = "ask"` or `edit = "ask"`
([§5](#5-two-layers-ceiling-asks-and-the-narrowing)). `approve` on its own PR is refused by GitHub
either way; `merge` on its own PR runs only under the `merge` kind.

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
project; its gate line says an approval can complete a PR's armed auto-merge ([§4.2](#42-an-armed-auto-merge-an-approval-stays-an-approval)).

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
| A review through `gh api` with an unreadable `event` | `… this review's verdict is not written on the command line (--input or @file), so it asks as a write to o/r (r-7k2m). Write the event as -f event=COMMENT to have it classified, or `yolo approve r-7k2m`.` |
| `yolo check` on a project requesting `approve` outside the ceiling | `yolo-jail.toml:4: this project requests approve. Your config does not allow approve on request, so this project runs without it. To allow it for every project you work on: yolo allow github on_request approve` (the built shape for `push`) |
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
| Every non-merge write to a working-set item runs unasked | Comments and edits on what the agent created run unasked under the creation grant ([OQ-GW3](#OQ-GW3)); other items run only the kinds the workspace holds; approvals, which ran unasked, ask until `approve` is allowed |
| A merge always asks | A merge of a PR the agent created runs unasked under `merge`; a merge of a PR the agent did not create asks ([OQ-GW2](#OQ-GW2)); an approval stays an approval when auto-merge is armed ([§4.2](#42-an-armed-auto-merge-an-approval-stays-an-approval)) |
| `--for <duration>` covers every non-merge write to the item for the time | Covers the request's kind on the item for the time |
| `--add` covers every non-merge write to the added item | Removed (2026-11-02); its replacement is [OQ-GW2](#OQ-GW2) |
| A `gh api` review with an unreadable `event` runs unasked on a working-set PR | Asks, as a write to the repository |
| `grant_kinds` in a legacy user config | Already `on_request` ([config-user.md A.6](config-user.md#a6-the-grant-kinds-mapped)); the work kinds have no legacy spelling, so no rename |

## 10. What this does not cover

- **Git.** The git working set has one kind of write, a push, and its line is the branch
  ([`git-broker.md` §5.1](git-broker.md#51-the-working-set-of-pushable-branches)). Nothing here
  reaches it.
- **Repository-level writes.** Labels, releases, workflow runs, settings and every write the
  table does not name keep asking per resource and never join the working set
  ([BB-D95](boundary-broker.md#BB-D95)). A `release` kind was not proposed: no user asked, and
  the ask path exists. Two exceptions are built, at the maintainer's ask: a run of a named
  workflow on the repository's default branch, under a `dispatch-workflow` grant
  ([GW-D14](#GW-D14)), and approving a held run a named actor triggered from the repository
  itself, under an `approve-run` grant ([GW-D15](#GW-D15)).
- **The scope and the refusals** ([BB-P8](boundary-broker.md#BB-P8)). A kind never widens the
  repository scope and never reaches a refused command.
- **Who may add a PR to the working set.** `yolo approve --add` is removed (2026-11-02,
  superseding [BB-D92](boundary-broker.md#BB-D92) for this source); what replaces it is
  [OQ-GW2](#OQ-GW2).
- **Keeping a jail off GitHub entirely.** That is the user's pack selection, not a kind.
- **Per-PR lines in config.** `yolo approve` draws those, one resource at a time, in the store.

## 11. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Keep one bit; take `approve` and `ready` out of the working set's coverage so they always ask.** Closes the trigger with no vocabulary | Rejected: it draws one line for everyone, and the ask is for everybody's line. The reviewer persona wants approvals unasked; the cautious persona wants comments asked |
| **Two kinds, `work` and `merge`, where `work` is everything but a merge and an approval** | Rejected: `approve` must be a word a user can allow or withhold on its own, since it is the one that differs between the reviewer and the default persona; and `comment` against `edit` is where a colleague's PR differs from your own. Four is the count at which every persona in [§7](#7-what-people-write) is one line ([OQ-GW1](#OQ-GW1), ruled A) |
| **Five or more kinds**: split `review` (request changes) from `comment`, or `close` from `edit`, or `ready` from both | Rejected for v1: no persona needed the split, and each word a reader must learn costs more than the line it draws. Add one when a real user asks |
| **Origin as a suffix or second list** (`merge_own`, or `[allow.github] added = [...]`) | Rejected: it doubles the vocabulary for a distinction that matters to one kind. The working set already knows who added each item; the one rule that turns on it is stated in words ([§6.2](#62-reaching-an-item-the-agent-did-not-create)) |
| **Choose the kinds at Add time** (`yolo approve r-<id> --add comment,edit`) | Rejected as the model, kept as a possible later flag: a per-resource choice does not give the user a line they can read in their config, and the human's Add already has a config to read the line from |
| **A separate workspace table for narrowing** (`[ask.github] kinds = [...]`) | Not taken ([OQ-GW4](#OQ-GW4), ruled A): one table, `requests.github`, holding `true` and `"ask"` keeps a kind's whole story on one line of one table |
| **Catch the auto-merge case: an approval on an armed PR asks as a merge** | Not taken ([OQ-GW5](#OQ-GW5), ruled B): the owner found it more surprising than an approval that stays an approval; the `approve` gate line states the reach instead |
| **A kind that names a repository** | Rejected ([§3](#3-the-axes-and-which-become-vocabulary)): the workspace file is where a repository's line goes |

## 12. What done looks like

- `gh pr review 32 --approve` on a PR a human added, in a workspace whose user allows
  `comment` and `edit` only, files a request whose message names `approve` and the `yolo allow`
  line; `gh pr comment 32` on the same PR runs and is audited `held: comment`.
- The same approval on a PR whose `auto_merge` is non-null, in a workspace that allows
  `approve`, runs as an approval; the `approve` gate line names the auto-merge reach.
- A project whose `yolo-jail.toml` sets `comment = "ask"` asks for a comment on the agent's own
  PR, for every user, and the request's message names the file and the `unset` line; deleting
  the line shows `+ github: comment` at the gate and runs nothing until `y`.
- `gh pr merge 32` on a PR the agent created runs under `merge`; on a PR a human added it asks,
  whatever the config.
- With only `create_pr` allowed and requested, `gh pr comment 12` on a PR the agent created
  runs unasked; `comment = "ask"` in the project makes it ask.
- `yolo approve r-<id> --for 15m` on a comment request leaves an approval on the same PR asking
  inside the window.
- The launch disclosure line names the kinds that run and the kinds that ask, and
  `yolo config why requests.github.comment` names the file and line that set `"ask"`.

## 13. Open Questions

1. ✅ **OQ-GW2: With `yolo approve --add` removed, how does the agent come to write to a PR or issue it did not create?**

   The Add flag is removed ([§6.2](#62-reaching-an-item-the-agent-did-not-create) quotes the ruling). Kurt
   files bug #70; the agent fixes it and wants to comment and close it.

   - Allow once per write, or a timed `--for`.
   - The repository-wide `comment_any` / `edit_any` grants.
   - A per-item grant spelled in config.

   Git's `--add` and `--remember` go too ([GB-D28](git-broker.md#GB-D28)).

   <!-- vantage: question id=OQ-GW2 leaning="Allow once, --for, the *_any grants, and a request by name to join the working set." -->

   _Leaning:_ Allow once, `--for`, the wide `*_any` grants, and a request by name to join the
   working set.

   **Answer:** Allow once, `--for`, the wide `*_any` grants, and a request by name to join the
   working set, answered by a plain `yolo approve`
   ([§6.2](#62-reaching-an-item-the-agent-did-not-create)). No per-item grant in config.

   > *"can't the agetn request it to be added to the working set somehow?"* … *"yes that sounds
   > good."* (owner, 2026-11-02)

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
| <a id="GW-D9"></a>GW-D9 | **Superseded by [OQ-GW3](#OQ-GW3) (2026-11-02): creation carries comment and edit, so there is no pairing to offer.** `yolo allow github [on_request] create_pr` on a terminal offers to also allow `comment` and `edit` in `always`; declined, or without a terminal, it prints the two commands, and `yolo check` repeats the pairing hint while `create_pr` is held with no work kind | 2026-10-30 | [§5](#5-two-layers-ceiling-asks-and-the-narrowing) | — |
| <a id="GW-D10"></a>GW-D10 | A timed grant (`--for`) covers the request's kind on its resource and no other kind | 2026-10-30 | [§6.1](#61-what-covers-a-write-in-order) | — |
| <a id="GW-D11"></a>GW-D11 | An issue's working-set entry never covers `approve` or `merge` | 2026-10-30 | [§6.2](#62-reaching-an-item-the-agent-did-not-create) | — |
| <a id="GW-D12"></a>GW-D12 | `yolo request github <kind> --ask [--local]` writes the narrowing; `yolo config set requests.github.<kind> ask` is the same edit, and advice names the former | 2026-10-30 | [§8](#8-every-stop-names-its-next-step) | — |
| <a id="GW-D13"></a>GW-D13 | A covered write is audited `held: <kind>` with the covering entry's id; the launch disclosure names the kinds that run and the kinds that ask | 2026-10-30 | [§8](#8-every-stop-names-its-next-step) | — |
| <a id="GW-D14"></a>GW-D14 | **Maintainer's ask:** *"I want you to improve the broker so dispatch is possible. then we'll modify my config to allow it, relaunch, and have you dispatch it."* A user grant kind `dispatch-workflow` names a repository and a `workflow`, one workflow file name (`nightly-macos.yml`) or `*` for every workflow; a grant without one is refused at config load. Readable spelling: `allow.github.on_request = ["dispatch_workflows"]`, `requests.github.dispatch_workflows = ["<file>"]`. It covers only `gh workflow run <file>` and `gh api` POST `repos/{o}/{r}/actions/workflows/{file}/dispatches`, with no query or fragment on the endpoint, no `--input`, and exactly one field rooted at `ref` (a key cut at `[` or `]`, as gh reads it), which must be `ref` itself; a workflow named by id or display name, `run rerun`, `run cancel` and `workflow enable`/`disable` ask as before. It covers a run on the repository's **default branch** only: `--ref` omitted, the default branch's name, or `refs/heads/<name>`, read by the broker's own `gh api repos/{o}/{r}`. A covered run is sent pinned to the full ref: the argv that runs carries `--ref=refs/heads/<default>`, or `ref=refs/heads/<default>` for `gh api`, so what runs is exactly the branch checked, and neither gh's own default-branch lookup nor a tag of the same name can change it (review fix, 2026-10-31, replacing a tag-shadow read). The reason: a dispatch runs the workflow file as it is on the ref, with the repository's secrets, and a jail able to push a branch could push a changed workflow and dispatch it there. Any other ref asks (Allow once or a timed grant). Inputs pass through unchanged. A covered run is authorized under the store's transaction lock with the grant re-read and audited under the set `dispatch-workflow`, as a `create-pr` creation is. Residual: the default-branch read happens just before the transaction, not inside it (a default branch renamed in between still runs the branch read), and a workspace that may push the default branch itself (a `branch` grant on it) can change the workflow it then dispatches | 2026-10-31 | [§10](#10-what-this-does-not-cover) | built |
| <a id="GW-D15"></a>GW-D15 | **Maintainer's ask:** approve the workflow runs GitHub holds at `action_required` (the weekly `flake.lock` bump PR that `github-actions[bot]` opens from a branch of the same repository) from the jail without a per-call host approval. A user grant kind `approve-run`, modeled on `dispatch-workflow` ([GW-D14](#GW-D14)), names a repository and an `actor`: a GitHub login (letters, digits and hyphens, not starting with a hyphen, at most 39 characters, optionally followed by `[bot]`) or `*` for every actor; a grant without one is refused at config load. Readable spelling: `allow.github.on_request = ["approve_runs"]`, `requests.github.approve_runs = ["<login>"]`; `yolo request github approve_runs <login>` writes it. It covers exactly `gh api` with a stated `-X POST` (or `--method POST`) of `repos/{o}/{r}/actions/runs/{id}/approve`: a numeric id, no query or fragment, no field and no `--input`. Before running it, the broker reads `repos/{o}/{r}/actions/runs/{id}` through the host gh and covers the approval only when `triggering_actor.login` matches a grant's actor (case-insensitively, or `*`) **and** `head_repository.full_name` is the repository itself (case-insensitively). A fork's run is never covered, whatever the actor: approving it runs code from outside the repository. A failed or unreadable read, an answer naming another run, a fork head or an actor no grant names asks, with a stderr line saying which; the pending request names `yolo request github approve_runs <actor>` then `yolo config apply`. Re-running, cancelling and `pending_deployments` approvals ask as before. A covered approval is authorized under the store's transaction lock with the grant re-read for the actor the broker read, and audited under the set `approve-run`. Residual: the run read happens just before the transaction, not inside it | 2026-11-02 | [§10](#10-what-this-does-not-cover) | built |
| <a id="OQ-GW1"></a>[OQ-GW1](#OQ-GW1) | **Ruled A:** four work kinds, `comment`, `edit`, `approve`, `merge`, as tabled in [§4.1](#41-the-table) | 2026-11-02 | [§4.1](#41-the-table) | — |
| <a id="OQ-GW2"></a>[OQ-GW2](#OQ-GW2) | **Ruled:** `yolo approve --add` is removed; an item the agent did not create is reached by Allow once, by `--for`, by a standing `*_any` grant, or by the agent's own request to join the working set (`yolo request github working-set issue <n>` or `pr <n>`), which a plain `yolo approve` answers and which covers `comment` and `edit` only. Owner: *"can't the agetn request it to be added to the working set somehow?"*, then *"yes that sounds good"* | 2026-11-02 | [§6.2](#62-reaching-an-item-the-agent-did-not-create) | — |
| <a id="GW-D33"></a>GW-D33 | **Owner's ruling (2026-11-02):** `comment`, `edit` and `approve` have no positive form; `true`, or a listing in `always`/`on_request`, is a `yolo check` error naming `create_pr`/`create_issue` (which carry comment and edit on the agent's own items), the working-set request, or the `*_any` word. `"ask"` stays for all four kinds. No migration: the positive forms were never built. Owner: *"I don't even understand what the naked approve and comment would even be. So I think that says we should remove them. We don't even have to have a migration path here."* `merge = true` (the agent's own PRs) stays as the one positive kind | 2026-11-02 | [§4.1](#41-the-table) | — |
| <a id="GW-D34"></a>GW-D34 | Working-set entries a human added with `--add` before its removal are kept until revoked, listed by `yolo approve` with their origin, and ended by `--revoke w-<id>`; the store records what was added, not how, so nothing migrates | 2026-11-02 | [§6.2](#62-reaching-an-item-the-agent-did-not-create) | — |
| <a id="GW-D35"></a>GW-D35 | **Owner's ruling (2026-11-02), refining [GW-D33](#GW-D33):** the narrowing lives on the `*_any` keys, so `comment`, `edit` and `approve` are not words at all. `comment_any`, `edit_any`, `approve_any` and `merge_any` each take their grant form (a list of `issues`/`pulls`, or `true`) or `"ask"`, and `"ask"` makes that kind ask in this workspace on every item, the agent's own included. Owner: *"so wouldn't we just make "ask" a value for the _any keys?"* `merge = true` (the agent's own PRs) stays the one per-item positive word; `merge_any = "ask"` narrows it too. `comment = …`, `edit = …` and `approve = …` in any form are `yolo check` errors naming the `*_any` key | 2026-11-02 | [§5](#5-two-layers-ceiling-asks-and-the-narrowing) | — |
| <a id="OQ-GW3"></a>[OQ-GW3](#OQ-GW3) | **Ruled B:** `create_pr` and `create_issue` carry `comment` and `edit` on what they create; a project narrows with `"ask"`. Owner: *"agents should be able to comment on edit on PRs they create. We can just specify that as part of create PR. I don't think it's really making one thing mean two things."* | 2026-11-02 | [§6.3](#63-what-the-agents-own-creation-covers) | — |
| <a id="OQ-GW4"></a>[OQ-GW4](#OQ-GW4) | **Ruled A:** the narrowing is `"ask"` in `requests.github`, written by `yolo request github <kind> --ask`; `true` requests the kind, absent leaves it to the user | 2026-11-02 | [§5](#5-two-layers-ceiling-asks-and-the-narrowing) | — |
| <a id="OQ-GW5"></a>[OQ-GW5](#OQ-GW5) | **Ruled B:** an approval or a ready on a PR with armed auto-merge is classified by its argv and is not a merge; the `approve` gate line states that an approval can complete an armed auto-merge. Owner: *"it's less surprising really"* | 2026-11-02 | [§4.2](#42-an-armed-auto-merge-an-approval-stays-an-approval) | — |
| <a id="OQ-GW6"></a>[OQ-GW6](#OQ-GW6) | **Ruled A:** the user's `always` may hold a work kind; each workspace's gate still shows it, and a project narrows with `"ask"` | 2026-11-02 | [§5](#5-two-layers-ceiling-asks-and-the-narrowing) | — |
