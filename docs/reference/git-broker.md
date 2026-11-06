---
status: accepted
stage: CURRENT
verified: 2026-11-06
verified_commit: f75e64269
covers:
  - internal/gitbroker/
  - internal/gitremote/
  - internal/gitproto/
  - internal/brokercore/
  - internal/brokerstore/working.go
  - internal/brokerstore/workingcommit.go
  - internal/brokerscope/grant.go
  - internal/cli/approve.go
  - internal/cli/approveremember.go
  - internal/cli/gitremote.go
  - packs/git/
tags: [broker, git, credentials, approvals, loopholes, audit]
summary: "As-built reference for the git broker: a jail's git fetches and pushes through a host daemon that connects upstream with the host's own SSH keys or git credential helper, so no git credential enters the jail. Covers the remote rewrite, the scope and read-grant checks, the anonymous public HTTPS fetch, push judging per ref, grants in two layers, the per-workspace working set, per-push approval requests and their answers, and the audit trail."
---

# The git broker: git fetch and push through the host's credentials

**Status:** Verified 2026-11-06 against `f75e64269`. MEASURED: the SSH path end to end, in a
real jail running the image's own `git` against a fake host `ssh` and a bare repository on the
host (the `TestGitBroker*` tests in `integration/gitbroker_test.go`: fetch and refusal, no read
grant, branch creation and the working set across a relaunch, a branch declared in config,
Allow once, and Allow for a time). UNMEASURED: the HTTPS upstream runs only in package tests
against a local server, and no run against a real forge, a real SSH agent or a real host
credential helper is recorded.

The git broker lets an agent in a jail run `git fetch`, `git pull`, `git ls-remote` and
`git push` against the workspace's real remotes while the jail holds no git credential. The
jail's own, unmodified `git` does all the local work and speaks git's protocol to a daemon on the
host. That daemon checks the remote against what a human approved, connects upstream the way the
host's own git would (the host's SSH keys or agent, or the host's `git credential fill`), relays
a fetch, and reads every push's ref commands before forwarding a byte of them. A push goes
through only when a grant or the workspace's working set covers every ref. Otherwise the push
files one request that a human answers with `yolo approve` on the host. Every call is audited.

| Component | Lives in |
| :--- | :--- |
| The host daemon, one per jail: admission, upstream connections, push policy, approval requests | `internal/gitbroker` (`Broker`, `Main`) |
| The jail's remote helper, `git-remote-yolo`, reached as `yolo git-remote` | `internal/gitremote`, `internal/cli` (`gitremote.go`) |
| git's wire formats: pkt-line, side-band, receive-pack commands and report, remote URL to repository identity | `internal/gitproto` |
| The source-neutral daemon shell, lifetime lease and exit codes | `internal/brokercore` (`Daemon`, `BindLease`, `Live`) |
| Requests, grants and working-set entries on the host | `internal/brokerstore` (`Store`, `WorkingEntry`) |
| Scope files and grant matching | `internal/brokerscope` (`File`, `Grant`, `RefPatternMatches`) |
| The audit log | `internal/brokeraudit` (`Event`, `RefUpdate`) |
| The human's answers | `internal/cli` (`approve.go`, `approveremember.go`) |
| The pack: the rewrite, the helper's placement, the loophole manifest, the agent briefing | `packs/git` |

**Reads with:** [`git-broker.md`](../design/git-broker.md) (the design and its ruling ledger,
still open for the removal of `--add` and `--remember`, see [The answers](#the-answers)),
[`boundary-broker.md`](../design/boundary-broker.md) (the broker model this source sits on and
its [terms](../design/boundary-broker.md#13-terms)),
[`loophole-transport.md`](loophole-transport.md) (the jail-to-host leg), and the user guides
[Git Without a Credential in the Jail](../../userguide/guides/git.md) and
[Approving What the Agent Asks For](../../userguide/guides/approvals.md).

---

## Terms

Terms the broker model already defines, **source**, **request**, **grant**, **Allow once**,
**user grant**, **approved scope** and **audit log**, are used as
[`boundary-broker.md` §1.3](../design/boundary-broker.md#13-terms) defines them. This doc adds or
narrows these:

- **Broker core** *(coined in [`git-broker.md`](../design/git-broker.md), 2026-10-23)*: the
  source-neutral half the GitHub and git brokers share: the daemon shell, the lifetime lease, the
  request store with its grants and working set, the scope and grant checks, the audit log and
  `yolo approve`. A source adds a classifier and an executor on top. Not a daemon of its own: each
  source runs its own daemon built from it.
- **Repository identity**: the form in which grants and working-set entries name a repository,
  `host/path`: the host lower-cased with any port kept, the user name dropped, a leading `/` or
  `~/` and a trailing `/` and `.git` stripped. So `git@github.com:o/r.git` and
  `https://github.com/o/r` are both `github.com/o/r`. Not the URL the broker connects to, which
  is the remote's own string, verbatim.
- **Working set** *(coined in [`git-broker.md`](../design/git-broker.md), widened to every
  resource in [`boundary-broker.md`](../design/boundary-broker.md#13-terms))*: for git, the
  branches one workspace may create, update, force-push and delete with no further asking. Not
  every branch the jail has pushed: only those joined in one of the ways
  [The working set](#the-working-set) lists. Never a tag.
- **Read grant**: a grant of kind `read` covering a repository identity or `*`. Every push and
  every call that could use a host credential needs one. Not implied by selecting the pack.
- **Ceiling** and **project request**: the two layers of a user grant. The ceiling is what the
  user's own config allows any workspace to hold; the project request is what one workspace's
  config asks for within it. Neither alone grants anything
  ([Grants in two layers](#grants-in-two-layers)).

## Principles and invariants

These are what a maintainer breaks by accident. Each holds for SSH and HTTPS alike.

- **The credential never crosses; the protocol does**
  ([BB-P1](../design/boundary-broker.md#BB-P1)). The jail reaches the broker with the loophole
  transport's own token, which reaches only the broker. The host's SSH agent socket and
  credential helper are used by the broker and never forwarded into the jail.
- **The jail runs the real git.** Nothing intercepts `git`; only the remote helper
  `git-remote-yolo` is placed. Every local command, IDE and agent git included, behaves as without
  the broker, and **pre-push hooks run in the jail**.
- **The host never runs git with the workspace as its repository.** A jail-writable checkout's
  config can run code. The broker reads the workspace's remotes as text (`brokerscope`), runs the
  host's `ssh` directly, and runs `git credential` in an empty broker-owned directory with git's
  ceiling set to it.
- **The upstream comes from the scope, never from the jail.** The broker connects only to a URL
  the approved scope holds, so the jail cannot point the host's SSH or credential helper at a
  target of its choosing. The one exception, a public HTTPS fetch, carries no credential
  ([Admission](#admission-scope-and-read-grant)).
- **Policy reads git's protocol, not a forge.** A push is judged from the ref commands
  receive-pack receives, which are exact, so the source works with any git server.
- **A push is all or nothing** ([GB-D4](../design/git-broker.md#GB-D4)). When any ref is not
  covered, no ref is forwarded, and a covered ref is refused "with the rest of this push".
- **Nothing is granted by default** ([GB-D15](../design/git-broker.md#GB-D15)). Selecting the
  pack starts the broker and serves the rewrite in every workspace; without a read grant every
  push and every credentialed fetch is refused, naming the grant.
- **The live scope is re-read at every call.** A `yolo config apply` on the host that changes
  the scope or the grants, or an edit to the user's ceiling, reaches the next fetch or push of a
  running jail.
- **The broker's background log names no workspace, repository, remote, host path or ref.** It
  is shared by every jail on the machine; it carries request numbers, services, outcomes and
  request ids only. The audit log holds the rest.
- **Every refusal names a next step that works from where the user stands**
  ([GB-D24](../design/git-broker.md#GB-D24),
  [`happy-path-principle.md`](happy-path-principle.md)). A refusal of an answered request never
  sends the user to `yolo approve` of that request, which only answers pending ones.

## How git reaches the broker

The `git` pack's `env` contribution sets `GIT_CONFIG_COUNT` and its numbered key and value
variables so that git rewrites `ssh://`, `git+ssh://`, `ssh+git://`, `https://` and scp-style
`git@…` remotes (bracketed IPv6 hosts included) to `yolo::<original URL>`. git then runs
`git-remote-yolo <remote> <url>`, which the pack's `intercept` contribution places as a
forwarder to `yolo git-remote --`. The helper answers git's `capabilities` with `connect` alone.
On `connect <service>` it sends the broker one request frame naming the service and the original
URL, then relays git's bytes both ways over the loophole's loopback-TLS endpoint.

Not rewritten, and so following the jail git's ordinary behavior: `http://`, `git://`,
`file://`, local paths, and scp-style remotes that do not start with `git@` (`host:path`,
`alice@host:path`).

**The rewrite exists only where the broker runs.** The `env` contribution is `served_by` the
`git-broker` loophole, and a fronted host loophole is served only where the launch runs its host
daemon (`loopholes.Set.FrontedHostNames`). Apple Container runs no host services, so it gets no
rewrite; `yolo host --` withholds it because the host's agent already reaches remotes with the
host's own credentials. Each withheld rewrite names its reason (`loopholes.Set.FrontedHostWhy`).

> [!WARNING]
> **An exported `GIT_CONFIG_COUNT` in the agent's own shell replaces the pack's rewrite.** That
> is git's own precedence and is not defended. The pack's briefing tells the agent never to set
> it.

> [!WARNING]
> **git ignores a remote helper's exit code once the helper has printed its "connected" line.**
> A failure after the first upstream bytes reach git must be said inside git's protocol (an
> `ERR` packet, or an `ng` line in the push report), never only as an exit code, or `git fetch`
> exits 0 having seen an empty remote. The helper prints the connected line only immediately
> before relaying the broker's first protocol bytes, so a refusal that arrives before any is
> reported by git as the helper's failure.

## Admission: scope and read grant

Each connection is checked in this order before any upstream is contacted:

1. **A well-formed request for a brokered service.** Only fetch (`git-upload-pack`) and push
   (`git-receive-pack`) cross; anything else, `git-upload-archive` included, is refused.
2. **The launch's live scope.** `brokercore.Live` re-reads the scope file and returns it only
   while this broker's lifetime lease is still the one bound to it. The git scope holds the
   workspace's remote URLs verbatim (`brokerscope.FormRemoteURL`): every `url` and `pushurl` of
   every remote, on any host, as shown to the human at the config-change gate. A remote joins
   only at that gate: `git remote add` in the workspace, then the launch prompt or
   `yolo config apply` on the host. For github.com, a project's `yolo request github repos`
   entry also widens git's read scope.
3. **The read grant and the scope match.** A push, any SSH call, and an HTTPS fetch that will
   need a credential are refused unless a read grant covers the remote's repository identity and
   the URL is in scope. Matching is exact, with only these equivalences
   (`gitbroker` `equivalentEndpoint`): SSH-scheme spellings whose parsed destination, explicit
   port and path agree; for `git@github.com` with no port, the relative scp path against the
   absolute SSH path and one trailing `.git`; and `https://github.com` URLs differing by one
   trailing slash or `.git`. Host aliases, other user names, ports and path forms are never
   equated.
4. **A transport the broker speaks:** SSH, or HTTPS.

**A public HTTPS fetch is tried anonymously first, even with no read grant and no approved
URL.** The broker never acquires a credential partway through such a request. Only when the
server answers 401, and the live scope still approves that URL and a read grant covers it
(re-checked at that moment), does the broker fill the host's credential and retry. An in-scope
URL still passes the current-scope check before its anonymous attempt; an out-of-scope URL can
be fetched anonymously but never becomes approved by it.

## The upstream leg: the host's own credentials

**SSH.** The broker runs the host's `ssh`, found on the PATH the jail was launched with and
checked by the broker core's placement rule (`brokercore.PlacementRefusal`), with forwarding and
local commands off, a connect timeout, the port if the URL has one, and the remote command
`git-upload-pack` or `git-receive-pack` with the path single-quoted as git quotes it. Its
environment is built from nothing: `PATH`, `HOME`, `USER`, the launching session's
`SSH_AUTH_SOCK`, and the display and askpass variables when set. There is no `BatchMode`, so a
key needing a touch or a passphrase prompts on the host, as a push typed there would. The broker
never prompts for a username or password.

**HTTPS (smart HTTP).** Credentials come from the host's `git credential fill`, given the
protocol, host and path, run with prompting off and bounded in time, so whatever helper the
host's git uses (a keychain, a token store) serves the broker too. After the first authenticated
response the broker reports `approve` to the helper, and `reject` on a 401, as git does. TLS
uses the system roots; nothing turns verification off, and a redirect is answered, not
followed. A fetch always asks for protocol v2 and maps the client's stream onto one POST per
client request; a server that answers v0 is refused, naming its SSH remote. A push relays the
`info/refs` advertisement, then POSTs the judged commands and the pack. A small push body is
buffered and sent with a length, and replayed with the credential after a 401; a large one
streams chunked, as git's own does.

## A fetch

A fetch in scope with a read grant is relayed both ways and audited in the `read` set. It is not
capped: the broker streams and holds no copy.

## A push

receive-pack speaks first, so the broker opens the upstream before it can read the push. It
relays the upstream's advertisement, reads the client's ref commands without forwarding any of
them, and judges every ref. A covered push is forwarded as the exact bytes read, followed by the
rest of the client's stream; the upstream's report is relayed and read to update the working
set. A refused push sends the upstream an empty command list over SSH (receive-pack treats it as
nothing to do) and no POST at all over HTTPS, and answers the client itself: `unpack ok` and one
`ng <ref> <reason>` per command, which git prints as `! [remote rejected] <ref> (<reason>)` and
exits 1.

What covers each ref (`Broker.coverForSnapshot` in `internal/gitbroker`):

| Ref | Covered, unasked, when |
| :--- | :--- |
| A new branch under `refs/heads/` (old id all zeros) | A `create-branch` grant on the repository or `*`, or a branch grant declaring it, while the ceiling allows the kind |
| An update, force-update or delete under `refs/heads/` | A live working-set entry for that workspace, repository and ref, or a branch grant declaring it by name or pattern, on the repository or `*`, while the ceiling allows `branch` |
| Any ref under `refs/tags/` | A tag grant naming or matching it, on the repository or `*`, while the ceiling allows `tag`. A tag never joins the working set |
| A branch or tag a human allowed for a time | That timed entry, until it expires |
| Anything else under `refs/heads/` or `refs/tags/` | Nothing: the push asks ([Approval requests](#approval-requests)) |
| A ref outside `refs/heads/` and `refs/tags/` (`refs/notes/…`, `refs/pull/…`) | Never. Refused per ref, and the push files no request, since no answer could cover it |

There is no force detection: a force-push to a covered ref is an update. A grant naming the ref
outright is preferred to a pattern, so the audit names the narrowest grant. A pattern is git's
refspec pattern with one `*` matching any run of characters, `/` included (`patch/*`, `v*`);
two are refused at config load.

A refused ref with no grant that could cover it gets a line naming the one project request that
would (`yolo request git push <branch>`, `push_tags <tag>`, or `new_branches`), and, when the
ceiling does not allow that kind yet, the `yolo allow` step on the host. The push's refusal lists
those lasting paths together with the request's own answers, each command on its own line.

## Grants in two layers

Every write kind is a user grant in two layers ([BB-D96](../design/boundary-broker.md#BB-D96)):

1. **The ceiling, in the user's own config**, lists the capabilities any workspace may hold:
   `fetch` (the read grant), `new_branches` (`create-branch`), `push` (`branch`) and
   `push_tags` (`tag`), as `[allow.git]` `always` or `on_request` entries written by
   `yolo allow git …` on the host. A legacy JSONC user config spells the read grant under
   `brokered.git.grants` and the ceiling as `brokered.git.grant_kinds`.
2. **The project request, in the workspace config**, names the grant and its resource:
   `[requests.git]` `new_branches`, `push = ["main"]`, `push_tags = ["v*"]`, written by
   `yolo request git …`. A legacy JSONC workspace spells it `brokered.git.grants`, each entry a
   kind, a `repo` (a repository identity or `*`) and a `branch` or `tag` name or pattern.
3. **It joins only at the config-change gate**, the same human answer that admits a remote to
   the scope: the launch prompt, or `yolo config apply` for a running jail. A wildcard grant's
   line at the gate says in plain words what it admits, and a remote approved later names each
   standing `*` grant that now covers it (`brokerscope.GrantNote`).

A project request of a kind the ceiling does not allow grants nothing. A TOML project's request
is reported and skipped; a legacy JSONC workspace grant fails `yolo check` on the host, naming
the user-config key. The broker re-reads the ceiling at every push, so withdrawing a kind
refuses the next push it alone would have covered. `yolo config-ref` is the authority for the
keys.

## The working set

A branch joins a workspace's working set in one of these ways, each with the same standing
([OQ-GB2](../design/git-broker.md#OQ-GB2)):

- **The agent creates it through the broker** under a `create-branch` grant, an Allow once, or a
  timed entry, and the upstream's report says `ok`. The broker records an entry
  (`brokerstore.WorkingEntry`, a `w-` id) keyed by workspace, repository identity and ref, whose
  origin is `create-branch`, the Allow once's `g-<id>`, or the timed entry itself, which
  becomes lasting.
- **The workspace config declares it** with an approved branch grant, by name or pattern. The
  config holds it, never the store: a delete through the broker leaves it declared, and removing
  the grant ends it. `yolo approve` lists it with a `c-` id, marked `from config`, while a jail
  of that project runs.
- **A human adds it** with `yolo approve <id> --add` on a push request: the existing branches the
  push needed approval for join with no expiry.

An entry lasts across jails and relaunches, with no age cap, until the branch is deleted by a
push through the broker that the upstream accepted (`deleted`), is found gone upstream when the
next push to it is a creation (`gone`), or is revoked with `yolo approve --revoke w-<id>`
(`revoked`). Two jails of one workspace share its entries. Only a creation through the broker
counts: a branch pushed from the host, or created by another workspace, is not this
workspace's. A creation the push lost (rejected upstream, or a report that could not be read)
records nothing. When a store entry and a config declaration both cover a ref, the store entry
is used, so a delete still ends the record.

> [!WARNING]
> **A creation on a ref with a live entry ends that entry as `gone` while the push is judged**,
> before the upstream answers, even if another ref then refuses the push whole. It only ever
> narrows what the workspace may push.

> [!WARNING]
> **Repository identity can merge two repositories** on a host where the path is relative to
> the login's home: it drops the user name and a leading `~/`, so `alice@host:r.git` and
> `bob@host:r.git` share one identity, and a grant or entry for one covers the other. On the
> forges it was designed for, where the path names the repository whoever logs in, those forms
> are the same repository.

## Approval requests

A push that no grant or working-set entry fully covers, and whose every ref lies under
`refs/heads/` or `refs/tags/`, either spends an Allow once the host already gave for exactly
this push or files one request for the whole push (`Broker.askForPush`). Spending and filing are
one store transaction, so an answer cannot land between them; the use is spent under the store's
lock, and the push is forwarded after the lock is released
([GB-D19](../design/git-broker.md#GB-D19)).

**What a request binds.** Its command is `git-receive-pack` followed by one `<old> <new> <ref>`
line per command in the push's order, then one `push-option=<value>` line per push option, so an
Allow once covers that exact push, every ref and option of it, once
([GB-D17](../design/git-broker.md#GB-D17)). Its display marks which refs needed approval, which
is outside the digest; `yolo approve` shows each ref's old and new ids with no force mark. A
request's jail is the scope file's container hash, never an id the connection claims.

**The wait.** The push waits a bounded time for the answer (half a minute; exact value in
[Current values](#current-values)). Allowed during the wait, it goes through. Otherwise each ref
that needed approval is refused `(needs approval: r-<id>)`, and the refusal lists every path
that answers it, each command on its own line. The pending request is written to the broker's
background log, and the user's `on_request` hook runs for it; with no notifier and no hook, the
agent's own refusal and the log are its only trace
([GB-D10](../design/git-broker.md#GB-D10)). A broker that stops refuses its pending requests;
grants it recorded stay with the workspace ([BB-D99](../design/boundary-broker.md#BB-D99)).

**Store authority.** `gitbroker.ApprovalAuthority` lets `yolo approve` decide a git request only
while the filing broker's lifetime lease is live, and re-checks the request's holder, set, digest
and argv, that the repository is still in the live scope, and that a read grant still covers it.
A grant's spend is re-checked the same way against the launch spending it.

### The answers

| Answer on the host | Effect |
| :--- | :--- |
| `yolo approve r-<id>` | **Allow once**: that exact push, once, within a short window. A branch it creates joins the working set |
| `yolo approve r-<id> --for <duration>` | One timed working-set entry per ref that needed approval; every push to those refs goes through, unspent, until it expires. Bounded; `session` is refused for git |
| `yolo approve r-<id> --add` | The existing branches that needed approval join the working set with no expiry. Refused, exit 5 naming `--for`, for a tag and for a new branch |
| `yolo approve r-<id> --remember` | Allow once, and write each ref that needed approval into the project's config as `yolo request git push <branch>` (or `push_tags <tag>`) would, effective after `yolo config apply`. Refused, writing nothing, while the project's config is legacy JSONC |
| `yolo approve r-<id> --deny [--note <text>]` | Refused; the same push files no new request for a cooldown |

`--for` and `--add` record working-set entries and no command grant, and the broker consults
the entries before filing, so a push they cover never asks again while they last
([GB-D26](../design/git-broker.md#GB-D26)).

> [!IMPORTANT]
> **The code and a ruling disagree here.** [GB-D28](../design/git-broker.md#GB-D28) (2026-11-02)
> ruled `--add` and `--remember` removed, refused as unknown flags naming `--for` and the
> project request, carrying [OQ-GW2](../design/github-write-scopes.md#OQ-GW2) to git and
> superseding [RC-D39](../design/readable-config.md#RC-D39). Both flags are still parsed and
> answered as above. The removal is unbuilt and stays with the design doc; entries added with
> `--add` stay until revoked either way.

### When a push that asked is refused

| Situation | The ref's reason | Next step named |
| :--- | :--- | :--- |
| Unanswered within the wait | `needs approval: r-<id>` | The answers above, the lasting project request, and the host push |
| Denied | `…, denied on the host` | Ask the user before pushing again; the same push files nothing during the cooldown; or push from the host |
| Allowed, but its grant no longer covers the push (spent by another run, revoked, scope changed) | `…, allowed but its grant no longer covers this push` | Run the same push again to file a new request |
| Allowed once, but its window closed before the push ran | `…, allowed but its window closed` | Tell the human; the next run files a new request; no request is filed by this one |
| Closed when its broker stopped | `…, closed when its broker stopped` | Run the same push again |
| Expired unanswered | `…, expired unanswered` | Wait out the cooldown, or push from the host |
| The jail already has the maximum of pending requests | `needs approval; this jail already has … requests waiting` | `yolo approve` on the host lists and answers them |
| The store failed | `needs approval; …` with the error | Push again; `yolo approve` reports what is wrong; or push from the host |

An Allow once spent on a push whose upstream connection then drops is not refunded
([GB-D20](../design/git-broker.md#GB-D20)). An upstream that rejects a forwarded push (a
protected branch, a non-fast-forward) reaches the agent unchanged, and an Allow once covering it
is spent.

## Asking before pushing: `yolo access`

The jail can ask the broker for its standing (`yolo access git`) or for the broker's own verdict
on a push before running it (`yolo access -- git push <remote> <source>:<destination>`), and can
file the request ahead of the push. The helper derives the candidate commands from git's dry
run, accepting only explicit destination refs, and treats their old ids as untrusted. The broker
opens only the upstream's receive-pack advertisement, binds every old id to what the host sees
there, sends no ref command or pack, evaluates the push with the same policy the push itself
uses, and audits the evaluation. The design is
[`access-introspection.md`](../design/access-introspection.md).

## Audit

Every call appends one line to the shared broker audit log with `service: git`, read with
`yolo audit --service git`: the remote URL verbatim, the repository identity, the set (`read`,
`create-branch`, `working-set`, `timed-grant`, `tag`, `out-of-scope` or `refused`), the outcome
and reason, the bytes sent and the elapsed time, and for a push one
`{ref, old, new, covered_by}` per command, `covered_by` naming the grant (`create-branch`,
`branch:<name>`, `tag:<name>`), the `w-` entry, or the Allow once. The store audits requests,
decisions and spent grants into the same log. Revoking a working-set entry is not audited, and
`yolo approve --watch` reports request and grant events only, not working-set changes.

## Failure paths

| What fails | What the agent sees |
| :--- | :--- |
| No broker endpoint in the jail | The helper says the endpoint is missing and names `yolo check` on the host |
| No read grant | Refused at once, saying nothing ran and naming how to add the read grant on the host, and whether this jail's broker takes it live or needs a relaunch |
| Remote out of scope | Refused at once, naming the approved scope and the gate: `git remote add` (or, for github.com, `yolo request github repos`), then `yolo config apply` |
| Transport not brokered | Refused, naming the SSH or HTTPS URL to set with `git remote set-url` |
| No `ssh` on the host's launch PATH, or one the placement rule refuses | Refused, naming the host install or the reason |
| The host has no SSH key the server accepts | Refused, naming `ssh-add -l` and `ssh -T <dest>` on the host |
| `git credential fill` returns no password, or the server refuses it | Refused, naming the host's `git credential fill` to run; a refused credential is reported to the helper as `reject` |
| HTTPS server speaks only protocol v0 for a fetch | Refused, naming the remote's SSH URL |
| A key needs a touch or passphrase and nobody answers on the host | The SSH connection times out; git shows its own error; an Allow once is not spent |
| The working-set store cannot be read | No entry covers this push, said once; branches declared in config still cover |

## What this does not do

- **Forge extras.** Pull requests, issues and the rest of a forge's API are the GitHub broker's.
- **Git LFS, submodules, and `git clone` of a repository the workspace has no remote for** are
  not brokered.
- **No `push-branch` grant.** Long-lived access to an existing branch, such as `main` of the
  agent's own fork, is that branch declared in the working set by a branch grant
  ([GB-D12](../design/git-broker.md#GB-D12)).
- **Read-only pushes nothing** ([GB-D7](../design/git-broker.md#GB-D7)): a workspace with only a
  read grant cannot push even a branch it created locally.
- **No inspection of what a push contains.** A pushed branch runs the repository's
  push-triggered workflows from its own workflow files; intent is out of scope by
  [the misguided-agent principle](misguided-agent-principle.md), and the audit log records every
  push with its refs and ids.
- **No fetch-size cap, no TLS-skip, no test-only environment hatch** in the shipped binary. The
  HTTPS trust roots and the push wait have unexported test seams only.
- **No credential mount.** With the pack selected, a workspace `mounts` entry reaching a
  credential path the loophole manifest fences (the SSH directory, the agent socket, git's
  credential stores) is refused.

## Current values

Verified at `f75e64269`. The prose above explains what each is for; this table is the only place
the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Rewritten remote prefixes | `ssh://`, `git+ssh://`, `ssh+git://`, `https://`, `git@` → `yolo::<url>` | `packs/git/pack.toml` (`[[env]]`) |
| Helper placement | `git-remote-yolo` → `yolo git-remote --` | `packs/git/pack.toml` (`[[intercept]]`) |
| Daemon | `yolo internal daemon git-broker --socket … --scope-file …`, loopback-TLS, one per jail | `packs/git/loopholes/git-broker/loophole.toml` |
| Self-check run by `yolo check` | `yolo internal daemon git-broker --self-check` | same manifest (`doctor_cmd`), `gitbroker.SelfCheck` |
| Grant kinds and their capabilities | `read` → `fetch`; `create-branch` → `new_branches`; `branch` → `push`; `tag` → `push_tags` | same manifest (`kind_capabilities`), `gitbroker.KindCapabilities` |
| Fenced credential paths | `~/.ssh`, `$SSH_AUTH_SOCK`, `~/.git-credentials`, `~/.config/git/credentials` | same manifest (`credential_paths`) |
| Jail endpoint variable | `YOLO_SERVICE_<slug>_ENDPOINT` for `git-broker` | `gitremote.EndpointEnv` |
| Store directory | `<global storage>/broker/git/` (`working/w-<id>.json` entries) | `paths.BrokerSourceDir`, `brokerstore` |
| Audit log | `<global storage>/broker/audit.jsonl` | `paths.BrokerAuditLog` |
| SSH options | `-x -T -o ClearAllForwardings=yes -o PermitLocalCommand=no -o ConnectTimeout=30` | `gitbroker` `sshArgv` |
| Push wait for an answer | 30 s | `gitbroker` `pushWait` |
| Allow once window, uses | 15 min, 1 use | `gitbroker` `pushPolicy` |
| `--for` default and ceiling | 15 min default, at most 24 h; `session` refused | `brokerstore.TimedGrantDefault`, `gitbroker` `pushPolicy` |
| Pending requests per jail | 3 | `brokerstore` `filingLimit` |
| Cooldown after a denial or expiry | 10 min | `brokerstore` `filingLimit` |
| Pending request lifetime | 1 h | `brokerstore.PendingLife` |
| Ended records kept | 7 days | `brokerstore` store pruning |
| `git credential` run limit | 2 min | `gitbroker` `credentialTimeout` |
| HTTPS push body sent with a length | up to 1 MiB; larger streams chunked | `gitbroker` `pushBufferMax` |
| HTTPS user agent | `git/yolo-git-broker` | `gitbroker` `userAgent` |
| `yolo access` budgets | status 3 s in the jail, 5 s on the host; evaluate 30 s in the jail, 25 s on the host | `gitremote` `ForwardAccess`, `gitbroker` `Broker.Handle` |
| Exit codes to git | 64 refused or out of scope; 69 unavailable; 77 no permission | `brokercore` (`ExitUsage`, `ExitUnavailable`, `ExitNoPerm`) |
| `yolo approve` exit codes | 1 store failure, 2 usage, 3 already decided, 4 unknown id, 5 refused by policy, 6 not confirmable | `internal/cli` `approve.go` |

## Why it's this way

The rulings a maintainer would otherwise undo. Each id resolves in the design doc's ledger,
which stays until [GB-D28](../design/git-broker.md#GB-D28) is built.

| ID | Ruling |
| :--- | :--- |
| [OQ-GB1](../design/git-broker.md#OQ-GB1) | git crosses as a brokered transport, every SSH and HTTPS remote rewritten to the host broker, not as a proxied git CLI or a host mirror: a push carries objects that exist only in the jail |
| [OQ-GB2](../design/git-broker.md#OQ-GB2) | A branch the agent creates stays in the working set until deleted or revoked, with no age cap: it is a property of the workspace |
| [GB-D3](../design/git-broker.md#GB-D3) | The host never runs git with the workspace as its repository |
| [GB-D4](../design/git-broker.md#GB-D4) | One request per push, and a push goes through whole or not at all |
| [GB-D7](../design/git-broker.md#GB-D7) | Read-only pushes nothing, not even the agent's own branch |
| [GB-D12](../design/git-broker.md#GB-D12) | Long-lived access to an existing branch is a branch grant declaring it in the working set; there is no separate push grant |
| [GB-D13](../design/git-broker.md#GB-D13) | A declared branch lives in config, never in the store, so a delete leaves it declared |
| [GB-D15](../design/git-broker.md#GB-D15) | No switch: selecting the pack starts the broker, and it grants nothing until a read grant does |
| [GB-D17](../design/git-broker.md#GB-D17) | An Allow once binds every command and every push option of the push |
| [GB-D19](../design/git-broker.md#GB-D19) | Spend and file are one store transaction; the push is forwarded after the lock is released |
| [GB-D20](../design/git-broker.md#GB-D20) | A spent Allow once is not refunded when the upstream connection drops |
| [GB-D25](../design/git-broker.md#GB-D25) | Branch and tag grants take one-`*` refspec patterns and `repo = "*"`; the two layers are the whole gate |
| [GB-D26](../design/git-broker.md#GB-D26) | `--for` and `--add` record working-set entries, not command grants; `--add` never applies to a tag or a new branch |
| [GB-D28](../design/git-broker.md#GB-D28) | Ruled: `--add` and `--remember` are removed for git. Not built; see [The answers](#the-answers) |
