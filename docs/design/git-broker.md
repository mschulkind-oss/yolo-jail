---
title: "Git through the host's login, one ref at a time: the second brokered source"
date: 2026-10-23
status: accepted
stage: DECIDED
next: "Graduate into docs/reference/git-broker.md, first moving the plan's ID-1 to ID-13 there because source comments cite them, and removing the reference's rows that still document yolo approve --add and --remember"
depends-on: []
tags: [design, credentials, git, github, broker, approvals, loopholes, audit]
summary: "The second source behind the boundary broker, after gh: git fetch and push from a jail that holds no credential. Fetching is standing; read-only allows no push of any kind (ruled 2026-10-23). Creating a branch takes a create-branch grant: the user's own config allows the kind, the workspace config names the repository, and the grant joins at the config-change approval; a branch created under it joins the workspace's working set, where the agent then pushes freely. Long-lived access to an existing branch (such as main) is that branch in the working set, declared in config by a branch grant granted the same way (ruled 2026-10-24); there is no separate push grant. A push to any other ref asks a human per ref: Allow once, Allow push for 15 minutes, Add the ref to the working set, or Deny. Step 1 of the maintainer's 2026-10-23 path: it first factors the source-neutral broker core out of the github-broker, then adds git as a second source on it. Ruled 2026-10-23: git crosses as a brokered transport, every remote form rewritten to the host broker, which connects upstream the way the host already does (SSH keys or agent, or git's credential helper), for any git server; a working-set branch stays until it is deleted or revoked; the source is off until the user includes it. Nothing is open; the step-1 build is git-broker-plan.md."
vantage:
  status-chip: true
---

# Git through the host's login, one ref at a time: the second brokered source

**Status:** 2026-10-23; built in part 2026-10-29. Built: the fetch and push path with the working set and the
`create-branch` grant; the Allow once and Deny answers to a push that asks ([GB-D16](#GB-D16), commits
`5c66007c6`, `b53b5a4ca` and `c2674694a`); and wildcard branch grants and tag grants ([GB-D25](#GB-D25), commit
`b1d59272d`); and, ruled and built 2026-10-30, the answers "Allow push to `<ref>` for 15 minutes" and "Add `<ref>` to the
working set" through `yolo approve <id> --for <duration>` and `--add` ([GB-D26](#GB-D26)).
Both questions here were ruled in review the same day,
and the broker's grant model was re-ruled later that day around resources
([BB-D76](boundary-broker.md#BB-D76)), down to which file holds a grant
([BB-D96](boundary-broker.md#BB-D96)); this doc follows all of it, and the build is
[`git-broker-plan.md`](git-broker-plan.md).
Split out of [`boundary-broker.md`](boundary-broker.md) the day the maintainer laid out his
five-step path ([§1.5 there](boundary-broker.md#15-the-maintainers-path-2026-10-23)), whose step 1
this doc designs. Evidence verified against `9f77afa19`.
**How the built system works now** is in the reference
[`docs/reference/git-broker.md`](../reference/git-broker.md), verified 2026-11-06.
[GB-D28](#GB-D28), the removal of `yolo approve --add` and `--remember` for git, was built
2026-11-06 (commit `86c0666cb`): both flags are now refused, so this doc is ready to graduate.

> **In short.** A jail's git should fetch and push the way a person's does, through the host's
> own keys or credential helper, without the credential ever entering the jail. Fetching is free;
> pushing is never free: creating a branch takes a grant the user approves, what the agent creates
> under it is then its to push, and anything else is one ref, shown to a human.

**Why it matters.** A jail has no git credential, so today `git fetch` and `git push` against a
private repository fail in every jail, and agents waste turns trying to work around it.
A colleague who built his own approval flow found the same thing and wrote a git broker of his
own; the maintainer called extending the broker to git *"definitely worth it"* (2026-10-23).

**The shape.** The **broker core** *(coined here)*: the source-neutral half of today's
github-broker (store, grants, audit, sets, scope, approval front-ends), factored out so a source
is a classifier plus an executor. **git** is the second source on it, reached through git's own
transport. Fetching is standing; branch creation is a **user grant**; pushes to the **working
set** run unasked; every other ref is asked for per ref ([§5](#5-the-git-sources-sets); both terms
are [defined in the broker's doc](boundary-broker.md#13-terms)).

**Cost.** A refactor of `internal/ghbroker` with no behavior change, a git transport endpoint on
the host that speaks upstream over both SSH and smart HTTP, a few lines of jail git configuration,
and a host-side record of the branches a workspace created.

**Start at [§4](#4-how-git-crosses)**: the one architecture choice. The rest follows from it.

**Needs your ruling:** None. [OQ-GB1](#OQ-GB1) and [OQ-GB2](#OQ-GB2) were ruled 2026-10-23,
as were a workspace requesting remotes ([OQ-BB18](boundary-broker.md#OQ-BB18)), the user
grants, git's `create-branch` among them ([OQ-BB19](boundary-broker.md#OQ-BB19); still no
`push-branch`: the working set takes branches from create-branch, from config
([GB-D12](#GB-D12), 2026-10-24), and later from a human's Add), which file holds a grant
([OQ-BB22](boundary-broker.md#OQ-BB22)) and a host with no notifier
([OQ-BB10](boundary-broker.md#OQ-BB10)). On 2026-10-24 the broker lost its switch: selecting the
git pack starts it, and it grants nothing until a `read` grant in config does ([GB-D15](#GB-D15)).

**Reads with:** [`boundary-broker.md`](boundary-broker.md) (the design this extends: the request
store, grants, the notification, `yolo approve`, the audit log and where long-lived access lives
are all its), [`workspace-widening.md`](workspace-widening.md) (how the GitHub source's scope
is widened today, which [OQ-BB18](boundary-broker.md#OQ-BB18) kept), and
[`git-broker-plan.md`](git-broker-plan.md) (the implementation plan for step 1; this doc wins on
behavior).

---

## 1. What the maintainer ruled, 2026-10-23

From a Slack thread with a colleague, which the maintainer pasted as the brief for this work.
The colleague's build proxies git: his host runs a fetch on request, a push is a separate request
he approves, and he grants push per branch, a branch the agent created being pushable without
asking. The maintainer's words on it:

> *"extending that github broker to support git over ssh is definitely worth it. how does that
> work though without allowing write access as well?"*

> *"interesting, so proxying the git CLI. I guess that's basically what I do with gh. maybe even
> worth making this a generic mechanism then. I think this is all the right approach in general"*

> *"add support for whatever loopholes you need/want with rw and ro support, maybe factoring out
> the CLI wrapping/proxy stuff"* (step 1 of his five)

| He says | Rules | Lands |
| :--- | :--- | :--- |
| *"support git"*, *"rw and ro support"* | git is a brokered source with a read set and a write set | [§5](#5-the-git-sources-sets) |
| *"factoring out the CLI wrapping/proxy stuff"*, *"a generic mechanism"* | The source-neutral half of the github-broker is factored out first | [§3](#3-the-broker-core-what-step-1-factors-out) |
| *"all the right approach"*, of per-branch grants with created branches free | Push is granted per ref, and a branch the workspace created is pushable without asking | [§5](#5-the-git-sources-sets) |
| *"proxying the git CLI"* | Read as the mechanism he had in mind, not a ruling on it; [OQ-GB1](#OQ-GB1) then ruled the transport instead | [§4](#4-how-git-crosses) |

The same day's review ruled the three questions this design raised: how git crosses
([OQ-GB1](#OQ-GB1)), how long a created branch stays pushable ([OQ-GB2](#OQ-GB2)), and where
long-lived access lives ([OQ-BB16](boundary-broker.md#OQ-BB16)). Later that day he re-ruled the
broker's grants around resources ([§1.6 there](boundary-broker.md#16-the-grant-model-2026-10-23)),
and two of those rulings reach git directly:

| He says | Rules | Lands |
| :--- | :--- | :--- |
| *"Read-only mode is read-only mode. It cannot create branches push... well, it can create branches in the local git repo. It cannot push them, that's for sure."* | Read-only allows no push of any kind, an agent's own branch included ([GB-D7](#GB-D7)) | [§5](#5-the-git-sources-sets) |
| *"Pushing to a branch it didn't create, I think is the same thing: it asks for access to that branch, and if it gets that access, it has that access. These should be able to be time-limited as well"* | Any other ref is asked for per ref, and can be added to the working set or granted for a time ([BB-D76](boundary-broker.md#BB-D76)) | [§5](#5-the-git-sources-sets) |
| *"Maybe we don't want to have read-only versus read-write. We just want to have these specific grants. You can tell me how many there are."* | Four user grants exist, git's `create-branch` among them ([OQ-BB19](boundary-broker.md#OQ-BB19)); for git, extended on 2026-10-24 by `branch`, a working-set branch declared in config ([GB-D12](#GB-D12)) | [§5](#5-the-git-sources-sets) |

On 2026-10-24 he ruled how long-lived push access to an existing branch is granted. It is not a
push concept: the branch is declared in config as a working-set entry.

| He says | Rules | Lands |
| :--- | :--- | :--- |
| *"I was expecting you to edit the jail config and then I run a config apply or something like that ... we can stick with main. And yes, I want to do this through the broker."* | Long-lived access to an existing branch such as `main` is granted in the workspace config, joins at the config-change approval, and goes through the broker ([GB-D12](#GB-D12)) | [§5](#5-the-git-sources-sets) |
| *"Why do we need a specific push grant? I thought, is this not just writing to a branch? ... I said it should just be like a branch working set, and shouldn't that already cover what's needed here?"* | There is no `push-branch`: a `branch` grant declares a branch in the working set, and it is covered exactly as a working-set entry is ([GB-D12](#GB-D12), [GB-D13](#GB-D13)) | [§5.1](#51-the-working-set-of-pushable-branches) |

Three more of that day's broker rulings reach git:

- **Where a grant lives** ([BB-D96](boundary-broker.md#BB-D96)): the user's own config lists
  which kinds of grant may be held, and the workspace config holds the specific grants, each
  naming its resource, joining only at the config-change approval. For git that is a
  `create-branch` grant naming a repository ([§5](#5-the-git-sources-sets), [GB-D9](#GB-D9)).
- **A PR leaving the working set on merge** ([BB-D97](boundary-broker.md#BB-D97)) is the
  GitHub source's rule; git's working set holds only branches ([GB-D11](#GB-D11)).
- **No notifier, no affordance** ([BB-D98](boundary-broker.md#BB-D98)): a per-ref request on a
  host with no notifier and no `on_request` hook is only logged ([§6](#6-failure-paths),
  [GB-D10](#GB-D10)).

The standing rule is unchanged: the credential never crosses
([BB-P1](boundary-broker.md#BB-P1)).

## 2. What exists today

Verified against `9f77afa19` on 2026-10-23.

| Piece | Where | State |
| :--- | :--- | :--- |
| A jail git credential | — | **none**: the jail has no SSH key and no token unless the user delivers one |
| The jail's global git config | composed on the host and mounted read-only on the container backends, written in place on macos-user (`internal/entrypoint/identity.go`) | **shipped**; a place yolo already owns where a URL rewrite can go |
| GitHub remotes reduced to `owner/repo` | `brokerscope.RepoFromRemoteURL`, for the https, `ssh://` and `git@host:` forms | **shipped**, used by the github-broker's scope; GitHub only |
| The approved repository scope | the config-change gate's approval record plus the per-launch scope file, re-read per request, widened live by `yolo config apply` ([`live-config-apply.md`](live-config-apply.md)) | **shipped** |
| The per-workspace file, user scope for one workspace | `~/.config/yolo-jail/workspaces/`, written on the host by `yolo loopholes enable` ([`storage-and-config.md`](../reference/storage-and-config.md#the-per-workspace-file)) | **shipped**; holds only loophole switches today |
| A request store with grants | `internal/brokerstore`, one directory per source (`paths.BrokerSourceDir`) | **built**; production files no request yet ([§7 there](boundary-broker.md#7-grants-and-the-request-store)) |
| An audit log with a `service` field | `internal/brokeraudit`, filtered by `yolo audit --service` | **shipped**; only `github` writes it |
| Jail-side interception of a command name | the `intercept` contribution kind (`packdecl.KindIntercept`) | **shipped**, used for `gh` |
| Anything for git | — | **missing**: no git source, transport or upstream connector in `internal/` or `packs/` |

The github-broker is one package, `internal/ghbroker`, holding both the source-neutral machinery
and everything gh-specific. That is what step 1 factors.

## 3. The broker core: what step 1 factors out

**The factoring is a refactor with no behavior change**, and it comes first because the git
source sits on it.

| Broker core (source-neutral) | Per source |
| :--- | :--- |
| The source registry: a name, a store directory, its default sets | The jail doorway: an intercept forwarder (gh), or a transport endpoint (git, [§4](#4-how-git-crosses)) |
| The classifier contract: an action goes in; out come refused, out of scope, or a set, plus the repositories it touches, a canonical rendering for the human, and a digest for Allow once | The classifier itself |
| Set resolution, user grants, the working set, timed grants, Allow once, each held by the workspace and kept across restarts ([BB-D99](boundary-broker.md#BB-D99)), revocation ([§7 there](boundary-broker.md#7-grants-and-the-request-store)) | The executor, and each write's kind and resource: for git, a ref |
| The repository-scope check against the approved scope | Which scope it reads: gh reads the workspace's GitHub remotes; git reads every remote of the workspace, on any host, as host and path |
| User grants in two layers ([BB-D96](boundary-broker.md#BB-D96)): the kinds the user's own config allows, and the specific grants the workspace config names, approved at the config-change gate and handed to the broker in the launch's scope file | Which grant kinds the source defines, and what resource each names |
| Request filing, duplicate joining, the bounded wait, status and wait | The exit codes or protocol errors the jail sees |
| Audit events, with `service` set to the source | Source-specific audit fields |
| The approval front-ends: notification text from the classifier's rendering, `yolo approve` across every source, the CLI surface ([§6.6 there](boundary-broker.md#66-one-store-several-front-ends-and-a-stable-surface-for-the-ones-yolo-does-not-ship)); `yolo audit --service` already filters by source | — |

**Done looks like:** the github-broker's tests pass unchanged on the core plus a gh source, and a
second, fake source in a test files, grants and audits through the same core.

## 4. How git crosses

Git differs from gh in one way that decides the design: **a push carries objects that exist
only in the jail.** Whatever runs the push on the host has to get them. So git crosses as its own
transport: the jail's git does all the local work and talks git's protocol to a host endpoint,
which applies policy and connects upstream ([OQ-GB1](#OQ-GB1), ruled 2026-10-23). The maintainer:
*"I guess I don't need to know how this works if it works … This seems just like the right
answer."*

### 4.1 The jail's leg

- **The configured SSH and HTTPS remotes are rewritten to the broker.** The jail's git
  configuration carries `url.<broker>.insteadOf` rules for scp-style `git@host:…`, `ssh://`,
  `git+ssh://`, `ssh+git://` and `https://` remotes. The SSH aliases and bracketed IPv6 scp form
  use the same broker transport; the broker checks the original URL against approved scope,
  allowing SSH-scheme spellings only when parsed destination, explicit port and path match, plus
  GitHub's `git@github.com:owner/repo` scp path versus `/owner/repo` SSH URL path, and for that
  same no-port GitHub user a single trailing `.git` on the repository segment, in either spelling.
  The request URL is preserved for upstream and audit; matching does not equate host aliases,
  usernames, ports, generic relative/absolute paths, HTTPS, or other path suffix variants. Other valid Git syntax,
  including `host:path` and `alice@host:path`, and other remote forms are not implicitly brokered.
  The rewritten URL carries the original remote, so the broker knows the upstream host, path and
  form; how it is encoded is the implementer's.
- **That leg carries no git credential.** The jail reaches the endpoint with the loophole
  transport's own token, which reaches only the broker
  ([`loophole-transport.md`](../reference/loophole-transport.md)).
- **The jail runs the real git, unchanged.** Nothing intercepts the `git` that every tool calls,
  IDEs and agents' own git included, and every local command behaves as without the broker.
  **Pre-push hooks run in the jail**, since the jail's git runs the push.

### 4.2 Policy, read from the protocol

1. **A fetch** (`git-upload-pack`) in scope is a `read`: forwarded, response streamed back.
2. **A push** (`git-receive-pack`) opens with one command line per ref: old id, new id, ref
   name. The broker reads those lines before forwarding anything and assigns each ref its write
   kind ([§5](#5-the-git-sources-sets)). If a grant covers every ref, the push goes through. If
   not, nothing is forwarded; the broker files a request and answers in git's own report format.
3. **A refusal or a pending request is git's own line**,
   `! [remote rejected] <ref> (<reason>)`, so `git push` exits 1, for example
   `! [remote rejected] main -> main (needs approval: r-3f9a0c21 …)`. The reason carries the
   request id and the `yolo` command, which is what an agent reads; a script cannot tell
   "pending" from "denied" by exit code, and does not need to.

Policy reads git's protocol ref commands, which are exact, rather than a forge's API or git's
push grammar. **So the source works for any git server**: GitHub, GitLab, Bitbucket or a
self-hosted one.

### 4.3 The upstream leg: the host's own credentials

The broker connects upstream **the way the host already does**, and requires no `gh` login:

| The remote | The broker connects with | What the user does |
| :--- | :--- | :--- |
| SSH (`git@host:…`, `ssh://`) | The host's SSH keys or agent: the launching session's `SSH_AUTH_SOCK`, and the host user's own SSH configuration and known hosts | Nothing. A user who has always used SSH migrates nothing |
| HTTPS | The host's `git credential fill`, so whatever credential helper the host's git uses | Nothing, if the host's git can already push |

- **A key that needs a touch or a passphrase prompts on the host**, at upstream time, as it
  would for a push typed on the host.
- **The SSH upstream is part of this design, not a later option.**
- **Implementation note:** the broker must speak upstream over SSH (`git-upload-pack` and
  `git-receive-pack` run over `ssh`) as well as over smart HTTP.

[GB-D3](#GB-D3) holds on this leg too: the host never runs git with the workspace as its
repository, and `git credential fill` runs in a broker-owned empty directory.

## 5. The git source's sets

A grant covers a resource, never a set for a window
([BB-D76](boundary-broker.md#BB-D76)); for git the resource is a ref, or the repository's ability
to create branches. **Read-only is read-only** ([GB-D7](#GB-D7)): it fetches and pushes nothing.

| Write kind | Admits | Runs unasked when |
| :--- | :--- | :--- |
| read (standing) | fetch, `ls-remote`, the fetch half of `pull`, on any repository in scope | always, once the source is included |
| branch creation | creating a branch under `refs/heads/` | the workspace holds an approved `create-branch` grant naming the repository, or a wildcard; the new branch joins the working set ([§5.1](#51-the-working-set-of-pushable-branches)) |
| branch push | updating, force-updating or deleting an existing branch, or creating one the config declares | the branch is in the working set: created under `create-branch`, declared by an approved `branch` grant in the workspace config, by name or by pattern, on the repository or on `*` ([GB-D12](#GB-D12), [GB-D13](#GB-D13), [GB-D25](#GB-D25)), or someone else's branch a human added ([OQ-BB19](boundary-broker.md#OQ-BB19), [BB-D92](boundary-broker.md#BB-D92)) |
| tag push | creating, updating, force-updating or deleting a tag under `refs/tags/` | an approved `tag` grant names the tag, by name or by pattern, on the repository or on `*` ([GB-D25](#GB-D25)); the tag never joins the working set |
| any other tag or branch push | a push under `refs/tags/` or `refs/heads/` not covered above | never: asked for per ref |

**Refused, under every set** ([BB-P8](boundary-broker.md#BB-P8)): a ref outside `refs/heads/`
and `refs/tags/` (`refs/pull/…`, `refs/notes/…`, and the like), and a push to a repository out
of scope.

**The source grants nothing until the user grants it** ([OQ-BB16](boundary-broker.md#OQ-BB16)):
*"we have default nothing for Git, and you even have to include that"*. Selecting the git pack
starts the broker and grants nothing; every call is refused until a `read` grant covers the
remote, usually the user config's read grant for every workspace, which each workspace's
config-change approval shows ([GB-D15](#GB-D15)). The broker has no switch.

**A per-ref request names its refs.** A push that no grant or working-set entry fully covers
files one request ([GB-D4](#GB-D4)). `yolo approve` lists every ref the push would change, old id
to new id, and marks the ones that need approval; the rest were covered already. It then lists
each push option (`git push -o`) the push sends, which the approval covers too
([GB-D17](#GB-D17)). No `force` mark is shown ([GB-D21](#GB-D21)). The push waits 30 seconds for
an answer. Unanswered, it is refused naming the request,
`! [remote rejected] <ref> (needs approval: r-<id>)`, and the same push run again within 15
minutes of an Allow goes through. **Built: four answers** ([§6.4 there](boundary-broker.md#64-choosing-a-duration)).
Allow once covers that exact push, every ref of it, once ([GB-D16](#GB-D16), 2026-10-29); Deny
refuses it. **Allow push to `<ref>` for 15 minutes** is `yolo approve <id> --for <duration>`
(15 minutes unless the duration says otherwise, at most 24 hours), and **Add `<ref>` to the working
set** is `yolo approve <id> --add` (built 2026-10-30, [GB-D26](#GB-D26)). Both record one entry per
ref the push needed approval for, in the workspace's working-set store, and the broker consults
them before it files a request, so every push to those refs goes through, unspent, until the
window ends or the entry is revoked. Add does not apply to a tag (the working set holds branches,
[GB-D11](#GB-D11), [GB-D18](#GB-D18)) nor to a new branch (a lasting right to create is a user
grant, [BB-D90](boundary-broker.md#BB-D90)); `yolo approve` refuses it for those, exit 5, naming
`--for 15m`. GitHub requests keep Allow once and Deny only. A tag push no `tag` grant covers asks too ([GB-D18](#GB-D18)), and the refused tag's line
names the `tag` grant that would cover it ([GB-D25](#GB-D25)). A branch created under an Allow once joins the
working set ([GB-D22](#GB-D22), [BB-D90](boundary-broker.md#BB-D90)). A push holding a ref
outside `refs/heads/` and `refs/tags/` files no request, since no answer could cover that ref.

**Long-lived access**, such as an agent that always pushes to `main` of its own fork, is that
branch in the working set, declared in config by a `branch` grant ([GB-D12](#GB-D12)); a release
agent that tags is a `tag` grant ([GB-D25](#GB-D25)). **Creating branches, declaring branches in
the working set and pushing tags are user grants in two layers**
([BB-D96](boundary-broker.md#BB-D96), [GB-D9](#GB-D9), [GB-D12](#GB-D12), [GB-D25](#GB-D25)):

1. **The user's own config allows the kind.** Without `create-branch`, `branch` or `tag` listed
   there, no workspace can hold it, and `yolo check` refuses a workspace grant of that kind,
   naming the user-config key that would allow it.
2. **The workspace config names the grant and its repository**: `create-branch` on one
   repository, or a wildcard written out as one; `branch` on one repository or `"*"` and one
   branch or pattern, `{"kind": "branch", "repo": "github.com/owner/name", "branch": "main"}`;
   `tag` the same way with a `tag` key, `{"kind": "tag", "repo": "*", "tag": "v*"}`. A pattern is
   git's refspec pattern: one `*` anywhere in the short name, matching any run of characters,
   `/` included (`*`, `patch/*`, `v*`); two are refused at config load, as is anything
   `git check-ref-format --refspec-pattern` refuses.
3. **The grant joins only at the config-change approval**, the same human y that admits a
   repository to the scope ([OQ-BB18](boundary-broker.md#OQ-BB18)). The workspace requests; the
   user gates. A workspace config alone holds nothing. A wildcard grant's line at the approval
   says in plain words what it admits ("may create, update, force-push and delete `main` on every
   remote in scope now and any approved later"), and a remote approved later names, on its own
   line, each standing grant on `"*"` that now covers it.

The levels `read-only` and `read-write` that [GB-D6](#GB-D6) mapped onto git are superseded by
specific user grants, `read`, `create-branch`, `create-pr` and `create-issue`
([OQ-BB19](boundary-broker.md#OQ-BB19)); git defines `read`, `create-branch`, since
[GB-D12](#GB-D12) `branch`, and since [GB-D25](#GB-D25) `tag`. `read` is a grant too, the floor every fetch and push needs
([GB-D15](#GB-D15)).

### 5.1 The working set of pushable branches

The **working set** *(coined here; the broker's doc widened it to PRs and issues,
[§1.3 there](boundary-broker.md#13-terms))* is the set of refs a workspace may push to unasked.
A ref joins it three ways, with the same standing each way ([OQ-GB2](#OQ-GB2), ruled
2026-10-23; the config way, [GB-D12](#GB-D12), 2026-10-24):

- **The agent creates it** under the `create-branch` user grant, or under an Allow once or a
  15-minute grant on a creation request. A ref update whose old id is all zeros creates the
  branch. When the broker forwards one and the upstream reports it accepted, the broker records
  `(workspace, repository, ref)` in the git source's store. The record names what allowed the
  creation: `create-branch`, or the `g-<id>` of the Allow once ([GB-D22](#GB-D22)).
- **The workspace config declares it** with an approved `branch` grant, by name or by pattern
  ([GB-D25](#GB-D25)). The config holds it,
  not the store: it stays until the grant leaves the config, and a delete does not end it
  ([GB-D13](#GB-D13)).
- **A human adds it** *(removed 2026-11-02, [GB-D28](#GB-D28); entries added before then stay until revoked)*: `yolo approve <id> --add` on a per-ref request puts the existing branches the
  push needed approval for in the store with no expiry ([BB-D86](boundary-broker.md#BB-D86),
  [GB-D26](#GB-D26)). `--for <duration>` records the same entry with an expiry instead, and a
  branch the agent then creates under it becomes lasting.

The maintainer: *"if it creates a branch and pushes it, it should have permissions to it, just
like if it was added through any other method that we can add it to this working set, and it
should be persisted because it's a property of the workspace."*

- **It persists across launches.** A record lasts until the branch is deleted (by a push through
  the broker, or found gone upstream on the next push) or the entry is revoked through
  `yolo approve --revoke`, which lists the records too. There is no age cap.
- **Only a creation through the broker counts.** A branch the user pushed from the host, or one
  another workspace created, is not this workspace's.
- **A creation the push lost** (the upstream rejected it, or the connection dropped before the
  report) records nothing; the next push is a creation again if the branch does not exist.
- **Two jails of one workspace** share the record, since it is keyed by workspace.

## 6. Failure paths

| What fails | What happens |
| :--- | :--- |
| The broker is not running | git reports the endpoint unreachable, and the briefing names `yolo check` |
| No read grant covers the remote | Refused at once in git's own error line, naming the read grant, `yolo config apply` and that nothing ran ([GB-D15](#GB-D15)) |
| The repository is out of scope | Refused at once, naming the scope, as for gh ([§5.6 there](boundary-broker.md#56-the-repository-scope)); audited, no request |
| The host has no credential for the remote (no SSH key or agent, or `git credential fill` returns nothing) | Refused naming what the host lacks and the host command that fixes it; the broker never starts a login and never prompts for a username or password |
| An SSH key needs a touch or passphrase and nobody answers on the host | The upstream connection times out; the agent sees git's own error, and an Allow once covering the push is not spent |
| A push needs a ref the jail does not hold | One request is filed for the whole push, and the push waits 30 seconds for an answer. Allowed during the wait, the push goes through. Otherwise it is refused with `! [remote rejected] <ref> (needs approval: r-<id>)`. A line names `yolo approve r-<id>` on the host and says to run the same push again within 15 minutes. Each refused branch or tag also gets a line naming the one grant that would cover it, spelled as the workspace config spells it ([GB-D25](#GB-D25)). The upstream receives no commands ([GB-D16](#GB-D16) to [GB-D23](#GB-D23)) |
| The host denies the request | `(needs approval: r-<id>, denied on the host)`, and a line saying to ask the user before pushing it again, or to push it from the host. The same push filed again within 10 minutes is refused at once, without a new request, naming the denied request and the time the quiet ends (the store's denial cooldown) |
| The request was allowed, but its grant no longer covers the push | `(needs approval: r-<id>, allowed but its grant no longer covers this push)`: another run of the same push spent it, it was revoked, or the scope changed. The next step is the same push again, to file a new request |
| The request was closed when its broker stopped, or expired | `(needs approval: r-<id>, closed when its broker stopped)`, whose next step is the same push again; or `(…, expired unanswered)`, which files nothing new for 10 minutes and names the host push |
| The jail already has 3 requests pending | Refused at once without a new request, listing the 3 and naming `yolo approve` on the host, which lists them and answers each with `yolo approve <id>` or `--deny`, then the push again |
| The request store fails while filing or spending | Refused naming the error; the next step is the push again, then `yolo approve` on the host, which reads the same store and reports what is wrong, or the host push |
| A push holds a ref outside `refs/heads/` and `refs/tags/` | Refused per ref, files no request ([BB-P8](boundary-broker.md#BB-P8)) |
| An Allow once was spent and the upstream connection then drops | The use is not refunded ([GB-D20](#GB-D20)): the next push files a new request |
| A per-ref request on a host with no notifier and no `on_request` hook | No affordance ([BB-D98](boundary-broker.md#BB-D98), [GB-D10](#GB-D10)): the request goes only to the broker's background log, and the agent's own pending line and wait timeout are its only messages. Creating and working its own branches under `create-branch` needs no notifier |
| The upstream rejects a forwarded push (a protected branch, a non-fast-forward) | The upstream's own report reaches the agent unchanged; an Allow once covering it is spent, as for any call that ran |
| A fetch exceeds the transfer cap | Not capped: a fetch is as large as the repository. The broker streams and holds no copy |

## 7. Security

- **No credential in the jail.** The jail holds the endpoint's transport token, which reaches
  only the broker, as every loophole's does
  ([`loophole-transport.md`](../reference/loophole-transport.md)). The host's SSH agent socket and
  credential helper are used by the broker and are never forwarded into the jail.
- **The upstream path comes from the scope, never from the jail.** The broker connects only to a
  host and path the approved scope holds, so the jail cannot steer the host's SSH or credential
  helper at a target of its choosing.
- **A pushed branch runs the repository's push-triggered workflows**, from the branch's own
  workflow files, with whatever secrets those workflows receive. A working-set push does not inspect what
  a push contains. This is the same exposure as a pull request the agent's branch would open, and
  intent is out of scope by [the misguided-agent principle](../reference/misguided-agent-principle.md);
  the audit log records every push with its refs and ids.
- **The audit line** for a push carries each ref, its old and new id, and the user grant,
  working-set entry or grant that covered it, so *"what exactly did it push with my access"* is one
  `yolo audit --service git` away.

## 8. What this does not cover

- **Forge extras.** Pull requests, issues and the rest of a forge's API stay the gh broker's
  job. A `glab` broker for GitLab merge requests is future work, another source on the same core.
- **Git LFS, submodules, `git clone` of a repository the workspace does not have.** Each needs
  its own design once someone asks.

## 9. The first slice

1. **The broker-core factoring** ([§3](#3-the-broker-core-what-step-1-factors-out)), with no
   behavior change. Waits on nothing.
2. **The git source's `read` set**: fetch, `pull` and `ls-remote` from the jail, in scope,
   through the brokered transport and the host's own SSH or credential helper, audited. Every
   push the working set does not cover answered "needs approval, which this version cannot ask
   for" until 2026-10-29, when such a push began to file a request ([GB-D16](#GB-D16)).
3. **The `create-branch` user grant**, in its two layers ([§5](#5-the-git-sources-sets)):
   creating a branch and pushing to it again, with the working set persisted per workspace and
   listed and revoked through `yolo approve`, so an agent whose workspace holds an approved grant
   can publish its own branch with no human at push time and no notifier
   ([OQ-BB19](boundary-broker.md#OQ-BB19), [BB-D96](boundary-broker.md#BB-D96)).

Per-ref requests needed the request path, from steps 2 and 3 of
[the path](boundary-broker.md#11-the-build-in-the-maintainers-five-steps). Built 2026-10-29 with
Allow once and Deny ([GB-D16](#GB-D16)), and with `--for` and `--add` since 2026-10-30
([GB-D26](#GB-D26)).
The slice reads the workspace's remotes and puts them in front of the human at the
config-change gate, as gh's scope does today; a workspace may request more, and they join only
at that y ([OQ-BB18](boundary-broker.md#OQ-BB18)).

## 10. What done looks like

1. In a jail whose workspace the user included and no credential, `git fetch origin` against a
   private in-scope repository brings its refs in, over an SSH remote and over an HTTPS one, and
   `yolo audit --service git` shows one `read` line for each.
2. A user who has only ever used SSH, with no `gh` login, fetches and pushes with no change on
   the host.
3. In a workspace holding an approved `create-branch` grant on the repository,
   `git push origin HEAD:refs/heads/agent/x`, a new branch, succeeds with no human; a second push
   and a force-push to it succeed too, each audited with the grant or the working-set entry that
   covered it, and still do after the jail restarts.
4. In a workspace holding no user grant, every push, a new branch included, prints
   `! [remote rejected]` and the upstream receives nothing: read-only pushes nothing.
5. `git push origin main` prints `! [remote rejected]` naming the request path, exits 1, and the
   upstream receives nothing.
6. A workspace the user did not include gets a refusal naming the switch, whatever its own
   config says; and a workspace config naming `create-branch` when the user's config does not
   allow the kind fails `yolo check`, naming the user-config key.
7. A fetch from a repository outside the scope is refused naming the scope.
8. `git status`, `git commit`, pre-push hooks and every other local command behave exactly as
   without the broker.

## 11. Open questions

None open. [OQ-GB1](#OQ-GB1) (how git crosses: the brokered transport, over a proxied CLI with
a host mirror) and [OQ-GB2](#OQ-GB2) (how long a created branch stays pushable: until deleted or
revoked, over the launch or an age cap) are ruled and recorded in the
[Decision Ledger](#12-decision-ledger).

## 12. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="GB-D1"></a>GB-D1 | **Maintainer direction:** git is a brokered source with read and write support, after factoring the source-neutral broker machinery out of the github-broker; push is granted per branch and a branch the agent creates is pushable without asking. *"I think this is all the right approach in general"* | 2026-10-23 | [§1](#1-what-the-maintainer-ruled-2026-10-23) | — |
| <a id="GB-D2"></a>GB-D2 | *Implementation decision.* The git source is a second service of the github pack's broker: the same enablement, scope file and login, and its own store directory under the source name `git`. **Superseded in part 2026-10-23** by [OQ-GB1](#OQ-GB1) (no `gh` login; any git server) and [GB-D5](#GB-D5) (its own switch, off by default); its own store directory under `git` stands | 2026-10-23 | [§3](#3-the-broker-core-what-step-1-factors-out) | — |
| <a id="GB-D3"></a>GB-D3 | *Implementation decision.* Under either option the host never runs git with the workspace as its repository; a jail-writable checkout's config can run code | 2026-10-23 | [§4](#4-how-git-crosses) | — |
| <a id="GB-D4"></a>GB-D4 | *Implementation decision.* A push changing several refs files one request naming the refs not held; a grant covers what its request named | 2026-10-23 | [§5](#5-the-git-sources-sets) | — |
| <a id="OQ-GB1"></a>[OQ-GB1](#OQ-GB1) | **Maintainer ruling, B, broker the transport, refined:** every remote form (scp-style, `ssh://`, `https://`) is rewritten by `url.<broker>.insteadOf` to the host broker over a leg with no credential; the broker connects upstream with the host's SSH keys or agent (the launching session's `SSH_AUTH_SOCK`) or the host's `git credential fill`, needing no `gh` login; any git server, since policy reads git's protocol ref commands; pre-push hooks run in the jail; a refusal or pending request is `! [remote rejected] <ref> (<reason>)`, exit 1; a key needing a touch or passphrase prompts on the host. The broker speaks upstream over SSH as well as smart HTTP. Forge extras stay the gh broker's. *"This seems just like the right answer."* | 2026-10-23 | [§4](#4-how-git-crosses) | built: the git pack rewrites every remote form to the broker; pinned by `TestGitPackRewritesSSHFormsThroughTheBroker` |
| <a id="OQ-GB2"></a>[OQ-GB2](#OQ-GB2) | **Maintainer ruling, B, persisted:** a branch the agent creates joins the workspace's working set of pushable branches, with the same standing as one granted any other way, across launches, until the branch is deleted or the grant is revoked through `yolo approve`; no age cap. *"it should be persisted because it's a property of the workspace"*. **Revised in part 2026-10-23 by [GB-D7](#GB-D7):** creating a branch is no longer standing; it takes a user grant, and the working set and its persistence stand | 2026-10-23 | [§5.1](#51-the-working-set-of-pushable-branches) | built as revised by [GB-D7](#GB-D7): a created branch persists in the workspace working set; pinned by `TestCreateBranchUnderGrantJoinsWorkingSet` and `TestWorkingSetSurvivesANewBroker` |
| <a id="GB-D5"></a>GB-D5 | *Implementation decision,* forced by [OQ-BB16](boundary-broker.md#OQ-BB16)'s ruling. The git source has its own switch, off by default, set only on the user's side for one workspace (the per-workspace file, as for every brokered loophole); enabling the github-broker does not include it, and a workspace config's switch for it is refused. Which pack ships the git source is the implementer's choice | 2026-10-23 | [§5](#5-the-git-sources-sets) | pending |
| <a id="GB-D6"></a>GB-D6 | *Implementation decision,* reading [OQ-BB16](boundary-broker.md#OQ-BB16)'s *"read-only or read-write"* onto the git source's sets: `read-only` holds `read` and `push-own`, the standing sets [GB-D1](#GB-D1) made free, and `read-write` adds `push` on every ref in scope. Reversible: if the maintainer meant `read-only` to forbid every push, `push-own` moves to `read-write`. **Superseded 2026-10-23 by [GB-D7](#GB-D7):** he did | 2026-10-23 | [§5](#5-the-git-sources-sets) | — |
| <a id="GB-D7"></a>GB-D7 | **Maintainer ruling, superseding [GB-D6](#GB-D6) and the standing half of [OQ-GB2](#OQ-GB2):** git read-only allows no push of any kind; an agent pushes even its own branches only under a grant that allows it, and creating a branch is a user grant whose created branches join the working set ([BB-D76](boundary-broker.md#BB-D76), [BB-D78](boundary-broker.md#BB-D78)). *"Read-only mode is read-only mode. It cannot create branches push... well, it can create branches in the local git repo. It cannot push them, that's for sure."* | 2026-10-23 | [§5](#5-the-git-sources-sets), [§5.1](#51-the-working-set-of-pushable-branches) | built: a push no grant covers is not forwarded, and a created branch joins the working set; pinned by `TestNoGrantPushesNothing`, `TestPushRefusedNothingForwarded` and `TestCreateBranchUnderGrantJoinsWorkingSet` |
| <a id="GB-D8"></a>GB-D8 | *Implementation decision,* carrying [BB-D76](boundary-broker.md#BB-D76), [BB-D77](boundary-broker.md#BB-D77) and [BB-D80](boundary-broker.md#BB-D80) to git: a per-ref request is answered Allow once (the exact push), Allow push to `<ref>` for 15 minutes, Add `<ref>` to the working set, or Deny, from the user's buttons list; a ref push's resource is the ref, and a creation's is the repository's ability to create branches. Replaces the windowed per-ref `push` set. **Narrowed 2026-10-29 by [GB-D16](#GB-D16), widened back 2026-10-30 by [GB-D26](#GB-D26):** all four answers are built | 2026-10-23 | [§5](#5-the-git-sources-sets) | built: Allow once and Deny ([GB-D16](#GB-D16)); the 15-minute answer and Add, pinned by `TestApproveGitForRecordsTimedGrantAndPushWithinItGoesThrough`, `TestApproveGitAddJoinsWorkingSetAcrossRelaunch`, `TestTimedGrantExpiresAndRefilesRequest` ([GB-D26](#GB-D26)) |
| <a id="GB-D9"></a>GB-D9 | *Reconciliation,* carrying [BB-D96](boundary-broker.md#BB-D96) to git and replacing this doc's earlier "a user grant in the user's own config for that workspace": the user's own config lists the grant kinds a workspace may hold (`create-branch` for git); the workspace config holds each `create-branch` grant naming its repository, or a wildcard written out as one; a grant joins only at the config-change approval and within the user's kinds, and `yolo check` refuses one outside them, naming the user-config key. `read` needs no entry. The loophole switch of [GB-D5](#GB-D5) stays in the per-workspace file | 2026-10-23 | [§3](#3-the-broker-core-what-step-1-factors-out), [§5](#5-the-git-sources-sets) | built: a grant outside the user's kinds is refused naming `brokered.git.grant_kinds`; pinned by `TestGrantKindNotAllowedNamesUserKey` and `TestRefusedCreationNamesTheCreateBranchGrant` |
| <a id="GB-D10"></a>GB-D10 | *Reconciliation,* carrying [BB-D98](boundary-broker.md#BB-D98) to git: a per-ref request on a host with no notifier and no `on_request` hook gets no terminal line and no notice file; it is written to the broker's background log, and the agent's pending line and wait timeout stay its only messages | 2026-10-23 | [§6](#6-failure-paths) | built 2026-10-29: the git broker hands the core its store, so the user's `on_request` runs for its requests, and logs each pending request with `yolo approve <id>` (`gitbroker.askForPush`, `TestHandlerRunsOnRequestAndRefusesPendingAtStop`) |
| <a id="GB-D11"></a>GB-D11 | *Reconciliation:* [BB-D97](boundary-broker.md#BB-D97)'s merge rule is the GitHub source's; the git source's working set holds only branches, which leave it on deletion or revocation ([OQ-GB2](#OQ-GB2)) | 2026-10-23 | [§5.1](#51-the-working-set-of-pushable-branches) | — |
| <a id="GB-D12"></a>GB-D12 | **Maintainer ruling, extending, for git, [OQ-BB19](boundary-broker.md#OQ-BB19)'s list of user grants:** long-lived access to an existing branch, such as `main`, goes through the broker as a branch in the working set, declared in config. There is no `push-branch` and no separate push concept: the working set takes branches from create-branch, from config, and (later) from a human's Add. The kind is `branch`, granted exactly as `create-branch` is: the user's own config allows the kind (`brokered.git.grant_kinds`), the workspace's `yolo-jail.jsonc` names the grant, `{"kind": "branch", "repo": "github.com/owner/name", "branch": "main"}`, it joins at the config-change approval (`yolo config apply` for a running jail, or the launch prompt), and `yolo check` refuses a grant whose kind the user config does not allow, naming the key. *"I was expecting you to edit the jail config and then I run a config apply or something like that ... we can stick with main. And yes, I want to do this through the broker."* Then: *"Why do we need a specific push grant? I thought, is this not just writing to a branch? ... I said it should just be like a branch working set, and shouldn't that already cover what's needed here?"* | 2026-10-24 | [§1](#1-what-the-maintainer-ruled-2026-10-23), [§5](#5-the-git-sources-sets) | built |
| <a id="GB-D13"></a>GB-D13 | *Implementation decision,* filling in [GB-D12](#GB-D12): a `branch` grant's `branch` is one branch's short name (`main`, meaning `refs/heads/main`), validated by git's ref-name rules (`git check-ref-format`); a full ref, a pattern or `"*"` is refused at config load with a next step, and tags and other refs are never covered. Its `repo` names one repository and is never `"*"`, unlike a create-branch grant's: with `"*"`, the branch on every approved remote, an upstream organization's remote and any remote approved later included, could be force-pushed or deleted, while the ruling's case was `main` of the agent's own fork. A `"*"` is refused at config load, naming the repository as the next step and one grant per repository, and the scope file refuses it too. This narrows access, so it is the coordinator's decision rather than a ruling. The declared branch is covered exactly as a working-set entry is: creating, updating, force-updating and deleting it, the remote's own protection being the backstop. It is never recorded in the host store: the config holds it, so a delete through the broker leaves it declared, and removing the grant takes it out. A store entry for the same ref is preferred, so a delete through the broker still ends that record. The broker re-reads the live scope file and the user config's `grant_kinds` at every push, so removing either refuses the next push. The audit's set is `working-set` and its `covered_by` names the grant (`branch:main`). `yolo approve` lists each declared branch of a running jail's workspace beside the store's entries, with a `c-` id, marked `from config` with the file that declares it; `--revoke` of one exits 5 and names that file and `yolo config apply`; a `c-` id no running jail declares exits 4, saying it is declared in config and listed only while a jail of its project runs. `--revoke all --workspace` leaves declared branches in place, and when they are all that workspace holds it names their config file instead of pointing back to the listing. `--json` carries each one as a working-set entry with `"by": "config"` and `"file"`. A declared branch whose kind the user config no longer lists says so only when the push is refused, not when a create-branch grant covers it | 2026-10-24 | [§5](#5-the-git-sources-sets) | built |
| <a id="GB-D14"></a>GB-D14 | *Reconciliation,* carrying [BB-D99](boundary-broker.md#BB-D99) to git: every grant the git source records will be held by the workspace and kept across broker and jail restarts and relaunches until it expires, is used up or is revoked, as the GitHub source's are; a broker stopping ends none. Since 2026-10-29 the git source records Allow once grants ([GB-D16](#GB-D16)), held the same way; since 2026-10-30 its timed grants are working-set entries with an expiry ([GB-D26](#GB-D26)), held by the workspace the same way. Its working-set entries were already keyed by workspace, nothing ends them at broker or jail stop, and the integration test `TestGitBrokerCreateBranch` already pushes on the working set alone after a relaunch, with a new broker | 2026-10-24 | [§5.1](#51-the-working-set-of-pushable-branches) | built in the shared store ([BB-D99](boundary-broker.md#BB-D99)) |
| <a id="GB-D15"></a>GB-D15 | **Maintainer ruling, carried to git** ([BB-D104](boundary-broker.md#BB-D104), [BB-D105](boundary-broker.md#BB-D105)): the git-broker has no switch. Selecting the git pack starts it in every workspace and serves the remote rewrite, and it grants nothing: every fetch and push is refused until a `read` grant covers the remote. The usual grant is the user config's `"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}]}}`, shown at each workspace's config-change approval. *"a user config that gives all workspaces read-only access to their configured git repo"*. Supersedes [GB-D5](#GB-D5) and [GB-D9](#GB-D9)'s "read needs no grant entry"; `read` is now a grant kind like the others ([BB-D107](boundary-broker.md#BB-D107)) | 2026-10-24 | [§5](#5-the-git-sources-sets), [§6](#6-failure-paths) | built: as [BB-D106](boundary-broker.md#BB-D106) to [BB-D109](boundary-broker.md#BB-D109); the git side in `gitbroker` `serveRequest`, pinned by `TestNoReadGrantRefusesEveryCall`, `TestUserReadGrantAppliedLetsTheFetchThrough`, `TestUserReadGrantWithdrawnRefusesTheNextFetch` and `TestReadGrantOnAnotherRepoCoversNothing` |
| <a id="GB-D16"></a>GB-D16 | **Maintainer ruling (2026-10-29), accepting the proposed default:** a push no grant or working-set entry covers files a request, and the only answers are Allow once and Deny. `yolo approve <id> --for` and `--add` are refused for git, as for GitHub, exit 5, naming Allow once as the next step. **Superseded in part 2026-10-30 by [GB-D26](#GB-D26):** the owner ruled to build the other two answers, so `--for` and `--add` are no longer refused for git. The Allow once answer lasts 15 minutes, is used once, and is held by the workspace ([BB-D77](boundary-broker.md#BB-D77), [BB-D99](boundary-broker.md#BB-D99)). The push waits 30 seconds ([§3.2 there](boundary-broker.md#32-a-write-and-the-bounded-wait)). Unanswered, the push is refused `(needs approval: r-<id>)`. Denied, it is refused `(needs approval: r-<id>, denied on the host)` | 2026-10-29 | [§5](#5-the-git-sources-sets), [§6](#6-failure-paths) | built: `gitbroker.askForPush`, `gitbroker.ApprovalStore`, `allowOnceOnly` in `internal/cli/approve.go`; Allow once pinned by `TestPushToMainAllowedOnceGoesThroughSSH`, `TestPushToMainAllowedOnceGoesThroughHTTPS`, `TestPushAllowedDuringTheWaitGoesThrough`, `TestPushDeniedNothingForwarded`, `TestApproveGitRequestAllowOnceAndRefusesFor` and the integration test `TestGitBrokerAllowOncePush` |
| <a id="GB-D17"></a>GB-D17 | **Maintainer ruling (2026-10-29), accepting the proposed default:** an Allow once's digest covers every command in the push, covered refs included. The request's argv is `git-receive-pack` followed by one `<old> <new> <ref>` per command, in the push's order. Its stdin digest is that of empty input. The display marks which refs needed approval (`Request.NeedsApproval`, shown by `yolo approve` and carried as `needs_approval` in `--json`, and outside the digest). *Extended 2026-10-29 after review, an implementation decision:* the push options the push sends upstream are bound too, one `push-option=<value>` line each after the commands, in the push's order, so they are in the digest, in `--json`'s argv, and on their own lines in `yolo approve`'s listing. A server acts on options (GitLab merges a request or sets CI variables by them), so the same commands sent with other options, or none, spend nothing. The listing shows ids to 12 hex digits | 2026-10-29 | [§5](#5-the-git-sources-sets) | built: `gitbroker.pushArgv`, `gitbroker.Broker.pushRequest`, `printPushRefs` in `internal/cli/approve.go`; pinned by `TestAskForPushSpendsExactGrantOnce`, `TestApprovalAuthorityRefusesForeignRefsAndHolders`, `TestPushOptionsAreBoundIntoTheAllowOnce` and `TestApproveGitRequestAllowOnceAndRefusesFor` |
| <a id="GB-D18"></a>GB-D18 | **Maintainer ruling (2026-10-29), accepting the proposed default:** a tag push files a request too. The refused tag's line says every tag push asks for approval, or can be pushed from the host. A tag never joins the working set | 2026-10-29 | [§5](#5-the-git-sources-sets) | built; the refusal line's wording superseded by [GB-D25](#GB-D25), which names a tag grant; pinned by `TestTagPushAsksForApproval` and `TestRefusedTagNamesTheTagGrant` |
| <a id="GB-D19"></a>GB-D19 | **Maintainer ruling (2026-10-29), accepting the proposed default:** the Allow once is spent under the store's lock, and the push is forwarded upstream after that lock is released | 2026-10-29 | [§5](#5-the-git-sources-sets) | built: `brokerstore.Store.SpendExactOrFileContext` with no dispatch, called from `gitbroker.askForPush`, which spends a grant or else joins or files the request in one transaction, so an answer cannot land between the two; after the wait, `brokerstore.Store.ConsumeExactDispatchContext`. Pinned by `TestAskForPushSpendsAnAnswerLandingAfterItsFirstTransaction` |
| <a id="GB-D20"></a>GB-D20 | **Maintainer ruling (2026-10-29), accepting the proposed default:** the SSH upstream connection stays open during the wait, and a connection that drops after the spend is not refunded | 2026-10-29 | [§6](#6-failure-paths) | built |
| <a id="GB-D21"></a>GB-D21 | **Maintainer ruling (2026-10-29), accepting the proposed default:** a request shows each ref's old id and new id only, with no force mark. Supersedes [§5](#5-the-git-sources-sets)'s earlier `force` mark | 2026-10-29 | [§5](#5-the-git-sources-sets) | built: `printPushRefs` |
| <a id="GB-D22"></a>GB-D22 | **Maintainer ruling (2026-10-29), accepting the proposed default:** a branch creation forwarded under an Allow once joins the working set, its entry recorded with `By` set to the grant's `g-<id>` | 2026-10-29 | [§5.1](#51-the-working-set-of-pushable-branches) | built: `gitbroker` `record`; pinned by `TestAllowOnceCreationJoinsTheWorkingSet` |
| <a id="GB-D23"></a>GB-D23 | *Implementation decision,* filling in [GB-D16](#GB-D16). **(a)** A request's jail is the short hash of the scope file's container (`paths.JailShortHash`), not the id the connection's preamble carries. **(b)** The store's authority, `gitbroker.ApprovalAuthority`, accepts a decision only while the filing broker's lifetime lease is live (`brokercore.Live`). It then re-checks the request's holder, set, stdin digest and argv. It also re-checks that the repository is still in the live scope and that a read grant still covers it. A grant's use is checked the same way against the launch spending it. **(c)** A push holding any ref outside `refs/heads/` and `refs/tags/` files no request. **(d)** A broker stopping refuses its jails' pending requests (`brokerstore.Store.End`). **(e)** A spend that finds the request allowed but the grant gone (another run of the same push spent it, it was revoked, or the scope changed) is refused `(needs approval: r-<id>, allowed but its grant no longer covers this push)`, and its next step is a new request, never `yolo approve` of the answered one | 2026-10-29 | [§6](#6-failure-paths) | built: `internal/gitbroker/approvalauthority.go`, `internal/gitbroker/approval.go`; pinned by the `TestApprovalAuthority*` tests, `TestPushWithARefOutsideHeadsAndTagsFilesNoRequest` and `TestHandlerRunsOnRequestAndRefusesPendingAtStop` |
| <a id="GB-D24"></a>GB-D24 | *Implementation decision, after review (2026-10-29),* carrying the Happy Path Principle ([`happy-path-principle.md`](../reference/happy-path-principle.md)) to every refusal of a push that asked: each ends in a step that works from where the user stands. A request no longer pending (allowed with its grant gone, denied, closed at broker stop, expired) never sends the user to `yolo approve <its id>`, which exits 3 for an answered request; the pending limit names `yolo approve` to list and answer the waiting requests; the denial cooldown names the denied request, when the quiet ends, and the host push; a store failure names the push again and `yolo approve`. Each refusal's ref line and its next step are built together (`refusal` in `internal/gitbroker/approval.go`) | 2026-10-29 | [§6](#6-failure-paths) | built: pinned by `TestEveryApprovalRefusalNamesAWorkingNextStep`, `TestPushAtThePendingLimitNamesYoloApprove`, and `TestGitRefusalNextStepsNameRealCommands`, which runs every `yolo` command a next step names through the CLI |
| <a id="GB-D25"></a>GB-D25 | **Maintainer ruling, superseding [GB-D13](#GB-D13)'s wildcard refusals and [GB-D18](#GB-D18)'s "every tag asks":** a `branch` grant may name a pattern and `repo: "*"`, and tags get the same grant. *"why can't we have a wildcard * branch grant? is there a good reason for that?"*; *"if someone wants to grant repo *, why should we stop them? that feels weird."*; *"allow."*; *"we should be able to wildcard tags as well"*. Shape: a pattern is git's own refspec pattern, one `*` anywhere in the short name, matching any run of characters including `/` (`*`, `patch/*`, `v*`), validated with `git check-ref-format --refspec-pattern`; whole-segment-only, as [WW-D31](workspace-widening.md#WW-D31) does for repository names, was not taken because it would refuse `v*`, the tag case the ruling exists for (a coordinator decision). `repo` may be `"*"`. A new kind `tag` mirrors `branch` for `refs/tags/`, with the same pattern rule, the same two layers (the user's `grant_kinds`, then the config-change approval) and the same coverage (create, update, force-update, delete). The existing two layers are the whole gate, with no separate wildcard kind. **Built with it:** the grant's approval line says in plain words what a wildcard admits ("may create, update, force-push and delete `main` on every remote in scope now and any approved later"), colored as other widenings; and approving a new remote later names every standing wildcard grant that now covers it. The remote's own protection stays the backstop | 2026-10-29 | [§5](#5-the-git-sources-sets) | built: `brokerscope.ValidGrantRefPattern`, `brokerscope.RefPatternMatches` and `brokerscope.GrantNote`, the `tag` kind in the git pack's manifest (`push_tags` in the readable spelling); pinned by `TestPatternBranchGrantCoversMatchingBranches`, `TestAnyRepoBranchGrantCoversEveryRemote`, `TestTagGrantCoversMatchingTags`, `TestRefusedTagNamesTheTagGrant`, `TestWildcardGrantLineSaysWhatItAdmits` and `TestNewRemoteNamesTheWildcardGrantsCoveringIt` |
| <a id="GB-D26"></a>GB-D26 | **Maintainer ruling (2026-10-30), superseding [GB-D16](#GB-D16)'s narrowing to Allow once and Deny:** build the other two answers [GB-D8](#GB-D8) and [BB-D76](boundary-broker.md#BB-D76) name. `yolo approve <id> --for <duration>` is **Allow push to `<ref>` for a time** (15 minutes unless given, at most 24 hours, never `session`) and `--add` is **Add `<ref>` to the working set**. Each records one working-set entry per ref the push needed approval for, held by the workspace across restarts and relaunches (a timed entry carries an `Expires`; `brokerstore.Store.DecideWorking`, the git store's `Entries` hook), and no command grant; the request becomes allowed, and the push waiting on it goes through when the broker finds every ref covered. The broker consults the entries before it files a request, so every push to the ref within the window goes through, unspent. *Implementation decisions:* a tag takes `--for` only (a tag never joins the working set, [GB-D11](#GB-D11), [GB-D18](#GB-D18)), and its refusal of `--add` names `--for 15m`; a branch creation takes `--for` only, since a lasting right to create is a user grant ([BB-D90](boundary-broker.md#BB-D90)), but a branch the agent creates under a timed entry makes the entry lasting ([§5.1](#51-the-working-set-of-pushable-branches)); revoke is `yolo approve --revoke w-<id>`. GitHub keeps Allow once and Deny only (`allowOnceOnly` names only it now) | 2026-10-30 | [§5](#5-the-git-sources-sets), [§5.1](#51-the-working-set-of-pushable-branches) | built `c250da3f8`: `brokerstore.Store.DecideWorking`, `gitbroker.pushEntries`, the coverer's timed-entry cases and `askForPush`'s re-cover; pinned by `TestApproveGitForRecordsTimedGrantAndPushWithinItGoesThrough`, `TestApproveGitAddJoinsWorkingSetAcrossRelaunch`, `TestTimedGrantExpiresAndRefilesRequest`, `TestAddIsRefusedForATagAndANewBranchNamingFor`, `TestApproveGitForAndAddCLI` and the integration test `TestGitBrokerAllowPushForATime` |
| <a id="GB-D28"></a>GB-D28 | **Carrying the owner's removal of `yolo approve --add` (2026-11-02, [OQ-GW2](github-write-scopes.md#OQ-GW2)) to git, for the owner's own reason** (*"It rides along with a grant, or it rides along with an approval, and depending on what the approval is, it does different things"*): `yolo approve` answers a git request once, `--for <duration>`, or `--deny`. `--add` and `--remember` ([RC-D39](readable-config.md#RC-D39)) are removed and refused as unknown flags naming `--for` and the standing path, `yolo request git push <branch>` (or `push_tags <tag>`) then `yolo config apply` on the host, which the refusal of a push already names per ref ([GB-D25](#GB-D25)). Branches a human added before the removal are kept until revoked ([GW-D34](github-write-scopes.md#GW-D34)). Supersedes [GB-D26](#GB-D26)'s `--add` and the "A human adds it" path of [§5.1](#51-the-working-set-of-pushable-branches) | 2026-11-02 | [§5.1](#51-the-working-set-of-pushable-branches) | built 2026-11-06: `yolo approve` refuses `--add` and `--remember` (any spelling, with or without a value) with exit 2, deciding nothing, naming Allow once, `--for 15m` and `yolo request git push <branch>` then `yolo config apply` (`refuseRemovedApproveFlag` in `internal/cli/approve.go`); the push refusal's menu offers neither (`pushMenu.menu`), nor does the jail briefing's example menu or the git pack's briefing; entries added before the removal still cover. Pinned by `TestApproveRefusesTheRemovedAddAndRememberFlags`, `TestApproveUsageNamesNoRemovedFlag`, `TestGitRefusalNextStepsNameRealCommands`, `TestEveryApprovalRefusalNamesAWorkingNextStep`, `TestApproveGitForRecordsTimedGrantAndPushWithinItGoesThrough`, `TestTheJailBriefingCarriesTheHandOffRules`, `TestNoShippedPackOffersTheRemovedApproveFlags`, and for the kept entries `TestApproveGitForCLI` and `TestApproveGitAddJoinsWorkingSetAcrossRelaunch` |
