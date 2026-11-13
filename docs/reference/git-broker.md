---
status: accepted
stage: CURRENT
verified: 2026-11-12
verified_commit: 82dcd8725
covers:
  - internal/gitbroker/
  - internal/gitremote/
  - internal/gitproto/
  - internal/brokercore/
  - internal/brokerstore/working.go
  - internal/brokerstore/workingcommit.go
  - internal/brokerscope/grant.go
  - internal/cli/approve.go
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
credential helper is recorded. The answers to a push were re-verified 2026-11-12 against
`82dcd8725`, when the design graduated here: `yolo approve --add` and `--remember` are removed
([GB-D28](#GB-D28)). One Mac witness is still to run in full
([W-GB1](#a-mac-witness-still-to-run-w-gb1)).

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
| The human's answers | `internal/cli` (`approve.go`; `refuseRemovedApproveFlag` refuses the removed flags) |
| The pack: the rewrite, the helper's placement, the loophole manifest, the agent briefing | `packs/git` |

**Reads with:** [the rulings below](#why-its-this-way),
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

- **Broker core** *(coined in the git-broker design, 2026-10-23)*: the
  source-neutral half the GitHub and git brokers share: the daemon shell, the lifetime lease, the
  request store with its grants and working set, the scope and grant checks, the audit log and
  `yolo approve`. A source adds a classifier and an executor on top. Not a daemon of its own: each
  source runs its own daemon built from it.
- **Repository identity**: the form in which grants and working-set entries name a repository,
  `host/path`: the host lower-cased with any port kept, the user name dropped, a leading `/` or
  `~/` and a trailing `/` and `.git` stripped. So `git@github.com:o/r.git` and
  `https://github.com/o/r` are both `github.com/o/r`. Not the URL the broker connects to, which
  is the remote's own string, verbatim.
- **Working set** *(coined in the git-broker design, widened to every
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
- **A push is all or nothing** ([GB-D4](#GB-D4)). When any ref is not
  covered, no ref is forwarded, and a covered ref is refused "with the rest of this push".
- **Nothing is granted by default** ([GB-D15](#GB-D15)). Selecting the
  pack starts the broker and serves the rewrite in every workspace; without a read grant every
  push and every credentialed fetch is refused, naming the grant.
- **The live scope is re-read at every call.** A `yolo config apply` on the host that changes
  the scope or the grants, or an edit to the user's ceiling, reaches the next fetch or push of a
  running jail.
- **The broker's background log names no workspace, repository, remote, host path or ref.** It
  is shared by every jail on the machine; it carries request numbers, services, outcomes, jail
  ids and request ids only. The audit log holds the rest, and a refused request's line names the
  command that reads it: `yolo audit --service git --jail <jail>` on the host.
- **Every refusal names a next step that works from where the user stands**
  ([GB-D24](#GB-D24),
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
   `yolo config apply` on the host. For github.com, a project's named `[repos.<name>]` table
   (`yolo request repos <alias> github.com/<path>`) also widens git's read scope, to that
   repository's two URLs, and never adds a push.
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
([OQ-GB2](#OQ-GB2)):

- **The agent creates it through the broker** under a `create-branch` grant, an Allow once, or a
  timed entry, and the upstream's report says `ok`. The broker records an entry
  (`brokerstore.WorkingEntry`, a `w-` id) keyed by workspace, repository identity and ref, whose
  origin is `create-branch`, the Allow once's `g-<id>`, or the timed entry itself, which
  becomes lasting.
- **The workspace config declares it** with an approved branch grant, by name or pattern. The
  config holds it, never the store: a delete through the broker leaves it declared, and removing
  the grant ends it. `yolo approve` lists it with a `c-` id, marked `from config`, while a jail
  of that project runs.
- **A human added it before 2026-11-06** with `yolo approve <id> --add`, since removed
  ([GB-D28](#GB-D28)). Such an entry stays, and covers, until it is revoked or its branch is
  deleted.

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
([GB-D19](#GB-D19)).

**What a request binds.** Its command is `git-receive-pack` followed by one `<old> <new> <ref>`
line per command in the push's order, then one `push-option=<value>` line per push option, so an
Allow once covers that exact push, every ref and option of it, once
([GB-D17](#GB-D17)). Its display marks which refs needed approval, which
is outside the digest; `yolo approve` shows each ref's old and new ids with no force mark. A
request's jail is the scope file's container hash, never an id the connection claims.

**The wait.** The push waits a bounded time for the answer (half a minute; exact value in
[Current values](#current-values)). Allowed during the wait, it goes through. Otherwise each ref
that needed approval is refused `(needs approval: r-<id>)`, and the refusal lists every path
that answers it, each command on its own line. The pending request is written to the broker's
background log, and the user's `on_request` hook runs for it; with no notifier and no hook, the
agent's own refusal and the log are its only trace
([GB-D10](#GB-D10)). A broker that stops refuses its pending requests;
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
| `yolo approve r-<id> --deny [--note <text>]` | Refused; the same push files no new request for a cooldown |

`--for` records working-set entries and no command grant, and the broker consults the entries
before filing, so a push they cover never asks again while they last ([GB-D26](#GB-D26)).

**`--add` and `--remember` are removed** ([GB-D28](#GB-D28), carrying
[OQ-GW2](../design/github-write-scopes.md#OQ-GW2) to git). Either, in any spelling, exits 2,
decides nothing, and names the answers that replace it: Allow once, `--for 15m`, and, for a
branch the agent pushes again, `yolo request git push <branch>` (or `push_tags <tag>`) in the
jail, then `yolo config apply` on the host (`refuseRemovedApproveFlag` in
`internal/cli/approve.go`). Entries added with `--add` before the removal stay until
`yolo approve --revoke w-<id>`.

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
([GB-D20](#GB-D20)). An upstream that rejects a forwarded push (a
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
| Remote out of scope | Refused at once, naming the approved scope and the gate: `git remote add` (or, for github.com, `yolo request repos <alias> github.com/<path>`), then `yolo config apply` |
| Transport not brokered | Refused, naming the SSH or HTTPS URL to set with `git remote set-url` |
| No `ssh` on the host's launch PATH, or one the placement rule refuses | Refused, naming the host install or the reason |
| The host has no SSH key the server accepts | Refused, naming `ssh-add -l` and `ssh -T <dest>` on the host |
| `git credential fill` returns no password, or the server refuses it | Refused, naming the host's `git credential fill` to run; a refused credential is reported to the helper as `reject` |
| HTTPS server speaks only protocol v0 for a fetch | Refused, naming the remote's SSH URL |
| A key needs a touch or passphrase and nobody answers on the host | The SSH connection times out; git shows its own error; an Allow once is not spent |
| The working-set store cannot be read | No entry covers this push, said once; branches declared in config still cover |

**A known gap**, recorded by the build on 2026-10-24 and carried here unverified: a stalled
upstream can hang a call once the jail side has closed, over HTTPS as over SSH, because the
handler cancels only when the jail's connection errors.

### A Mac witness still to run: W-GB1

A nightly run on the owner's Mac failed in `TestGitBrokerConfigBranch`: the second workspace's
granted `git fetch` reported `fatal: bad object` although the host broker logged
`git-upload-pack` exit 0 ([run 42833734953, shard
5](https://github.com/mschulkind-oss/yolo-jail/actions/runs/42833734953/job/129892741730)). Linux
cannot settle the Mac Podman VM's jail-to-host stream.

| Step | What to do |
| :--- | :--- |
| 1 | On the Mac's rootless Podman machine, record `podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'`; expect `true pasta` |
| 2 | From a clean checkout at main `287479402` or later, with the normal HOME and Go caches: `YOLO_SETUP=podman-self go test -count=5 -timeout 0 -v -run '^TestGitBrokerConfigBranch$' ./integration` |
| 3 | Require five `--- PASS: TestGitBrokerConfigBranch` and no `--- SKIP:`. The test records fetch and push exit statuses separately, checks the fetched upstream commit and a client-side `git fsck`, and traces packets for the fixture fetch only |
| 4 | On any failure, keep the full Go output with the command and the recorded network mode |

**Status: partly measured.** On 2026-11-11 the owner's Mac (macOS 26.5, arm64, `true pasta`)
passed five runs at main `77df9ca0d`, which holds the reviewed baseline `3b1be0bc15d1` but not
`287479402`, so those runs lacked step 3's separate exit statuses, commit check, `git fsck` and
packet trace (logs on the Mac under `/Users/Shared/yolo/evidence/`). The failure did not recur in
five runs; that neither withdraws the nightly's observation nor names its cause. **Still to run:**
steps 1 to 4 at `287479402` or later.

## What this does not do

- **Forge extras.** Pull requests, issues and the rest of a forge's API are the GitHub broker's.
- **Git LFS, submodules, and `git clone` of a repository the workspace has no remote for** are
  not brokered.
- **No `push-branch` grant.** Long-lived access to an existing branch, such as `main` of the
  agent's own fork, is that branch declared in the working set by a branch grant
  ([GB-D12](#GB-D12)).
- **Read-only pushes nothing** ([GB-D7](#GB-D7)): a workspace with only a
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

The rulings a maintainer would otherwise undo, with the ids of the design this broker was built
from (`git-broker.md`, decided 2026-10-23, graduated here 2026-11-12 and since deleted) and of
its build plan. Code comments cite both series.

### The decision ledger

[OQ-GB1](#OQ-GB1), [OQ-GB2](#OQ-GB2) and every row marked a maintainer ruling are the owner's; the rest were the
designer's or builder's within them. Rows a later one supersedes are kept, marked, because the
reason a choice was undone is what stops it being redone.

| ID | Ruling / Decision | Date | Built |
| :--- | :--- | :--- | :--- |
| <a id="GB-D1"></a>GB-D1 | **Maintainer direction:** git is a brokered source with read and write support, after factoring the source-neutral broker machinery out of the github-broker; push is granted per branch and a branch the agent creates is pushable without asking. *"I think this is all the right approach in general"* | 2026-10-23 | — |
| <a id="GB-D2"></a>GB-D2 | *Implementation decision.* The git source is a second service of the github pack's broker: the same enablement, scope file and login, and its own store directory under the source name `git`. **Superseded in part 2026-10-23** by [OQ-GB1](#OQ-GB1) (no `gh` login; any git server) and [GB-D5](#GB-D5) (its own switch, off by default); its own store directory under `git` stands | 2026-10-23 | — |
| <a id="GB-D3"></a>GB-D3 | *Implementation decision.* Under either option the host never runs git with the workspace as its repository; a jail-writable checkout's config can run code | 2026-10-23 | — |
| <a id="GB-D4"></a>GB-D4 | *Implementation decision.* A push changing several refs files one request naming the refs not held; a grant covers what its request named | 2026-10-23 | — |
| <a id="OQ-GB1"></a>[OQ-GB1](#OQ-GB1) | **Maintainer ruling, B, broker the transport, refined:** every remote form (scp-style, `ssh://`, `https://`) is rewritten by `url.<broker>.insteadOf` to the host broker over a leg with no credential; the broker connects upstream with the host's SSH keys or agent (the launching session's `SSH_AUTH_SOCK`) or the host's `git credential fill`, needing no `gh` login; any git server, since policy reads git's protocol ref commands; pre-push hooks run in the jail; a refusal or pending request is `! [remote rejected] <ref> (<reason>)`, exit 1; a key needing a touch or passphrase prompts on the host. The broker speaks upstream over SSH as well as smart HTTP. Forge extras stay the gh broker's. *"This seems just like the right answer."* | 2026-10-23 | built: the git pack rewrites every remote form to the broker; pinned by `TestGitPackRewritesSSHFormsThroughTheBroker` |
| <a id="OQ-GB2"></a>[OQ-GB2](#OQ-GB2) | **Maintainer ruling, B, persisted:** a branch the agent creates joins the workspace's working set of pushable branches, with the same standing as one granted any other way, across launches, until the branch is deleted or the grant is revoked through `yolo approve`; no age cap. *"it should be persisted because it's a property of the workspace"*. **Revised in part 2026-10-23 by [GB-D7](#GB-D7):** creating a branch is no longer standing; it takes a user grant, and the working set and its persistence stand | 2026-10-23 | built as revised by [GB-D7](#GB-D7): a created branch persists in the workspace working set; pinned by `TestCreateBranchUnderGrantJoinsWorkingSet` and `TestWorkingSetSurvivesANewBroker` |
| <a id="GB-D5"></a>GB-D5 | *Implementation decision,* forced by [OQ-BB16](../design/boundary-broker.md#OQ-BB16)'s ruling. The git source has its own switch, off by default, set only on the user's side for one workspace (the per-workspace file, as for every brokered loophole); enabling the github-broker does not include it, and a workspace config's switch for it is refused. Which pack ships the git source is the implementer's choice | 2026-10-23 | pending |
| <a id="GB-D6"></a>GB-D6 | *Implementation decision,* reading [OQ-BB16](../design/boundary-broker.md#OQ-BB16)'s *"read-only or read-write"* onto the git source's sets: `read-only` holds `read` and `push-own`, the standing sets [GB-D1](#GB-D1) made free, and `read-write` adds `push` on every ref in scope. Reversible: if the maintainer meant `read-only` to forbid every push, `push-own` moves to `read-write`. **Superseded 2026-10-23 by [GB-D7](#GB-D7):** he did | 2026-10-23 | — |
| <a id="GB-D7"></a>GB-D7 | **Maintainer ruling, superseding [GB-D6](#GB-D6) and the standing half of [OQ-GB2](#OQ-GB2):** git read-only allows no push of any kind; an agent pushes even its own branches only under a grant that allows it, and creating a branch is a user grant whose created branches join the working set ([BB-D76](../design/boundary-broker.md#BB-D76), [BB-D78](../design/boundary-broker.md#BB-D78)). *"Read-only mode is read-only mode. It cannot create branches push... well, it can create branches in the local git repo. It cannot push them, that's for sure."* | 2026-10-23 | built: a push no grant covers is not forwarded, and a created branch joins the working set; pinned by `TestNoGrantPushesNothing`, `TestPushRefusedNothingForwarded` and `TestCreateBranchUnderGrantJoinsWorkingSet` |
| <a id="GB-D8"></a>GB-D8 | *Implementation decision,* carrying [BB-D76](../design/boundary-broker.md#BB-D76), [BB-D77](../design/boundary-broker.md#BB-D77) and [BB-D80](../design/boundary-broker.md#BB-D80) to git: a per-ref request is answered Allow once (the exact push), Allow push to `<ref>` for 15 minutes, Add `<ref>` to the working set, or Deny, from the user's buttons list; a ref push's resource is the ref, and a creation's is the repository's ability to create branches. Replaces the windowed per-ref `push` set. **Narrowed 2026-10-29 by [GB-D16](#GB-D16), widened back 2026-10-30 by [GB-D26](#GB-D26):** all four answers are built | 2026-10-23 | built: Allow once and Deny ([GB-D16](#GB-D16)); the 15-minute answer and Add, pinned by `TestApproveGitForRecordsTimedGrantAndPushWithinItGoesThrough`, `TestApproveGitAddJoinsWorkingSetAcrossRelaunch`, `TestTimedGrantExpiresAndRefilesRequest` ([GB-D26](#GB-D26)) |
| <a id="GB-D9"></a>GB-D9 | *Reconciliation,* carrying [BB-D96](../design/boundary-broker.md#BB-D96) to git and replacing this doc's earlier "a user grant in the user's own config for that workspace": the user's own config lists the grant kinds a workspace may hold (`create-branch` for git); the workspace config holds each `create-branch` grant naming its repository, or a wildcard written out as one; a grant joins only at the config-change approval and within the user's kinds, and `yolo check` refuses one outside them, naming the user-config key. `read` needs no entry. The loophole switch of [GB-D5](#GB-D5) stays in the per-workspace file | 2026-10-23 | built: a grant outside the user's kinds is refused naming `brokered.git.grant_kinds`; pinned by `TestGrantKindNotAllowedNamesUserKey` and `TestRefusedCreationNamesTheCreateBranchGrant` |
| <a id="GB-D10"></a>GB-D10 | *Reconciliation,* carrying [BB-D98](../design/boundary-broker.md#BB-D98) to git: a per-ref request on a host with no notifier and no `on_request` hook gets no terminal line and no notice file; it is written to the broker's background log, and the agent's pending line and wait timeout stay its only messages | 2026-10-23 | built 2026-10-29: the git broker hands the core its store, so the user's `on_request` runs for its requests, and logs each pending request with `yolo approve <id>` (`gitbroker.askForPush`, `TestHandlerRunsOnRequestAndRefusesPendingAtStop`) |
| <a id="GB-D11"></a>GB-D11 | *Reconciliation:* [BB-D97](../design/boundary-broker.md#BB-D97)'s merge rule is the GitHub source's; the git source's working set holds only branches, which leave it on deletion or revocation ([OQ-GB2](#OQ-GB2)) | 2026-10-23 | — |
| <a id="GB-D12"></a>GB-D12 | **Maintainer ruling, extending, for git, [OQ-BB19](../design/boundary-broker.md#OQ-BB19)'s list of user grants:** long-lived access to an existing branch, such as `main`, goes through the broker as a branch in the working set, declared in config. There is no `push-branch` and no separate push concept: the working set takes branches from create-branch, from config, and (later) from a human's Add. The kind is `branch`, granted exactly as `create-branch` is: the user's own config allows the kind (`brokered.git.grant_kinds`), the workspace's `yolo-jail.jsonc` names the grant, `{"kind": "branch", "repo": "github.com/owner/name", "branch": "main"}`, it joins at the config-change approval (`yolo config apply` for a running jail, or the launch prompt), and `yolo check` refuses a grant whose kind the user config does not allow, naming the key. *"I was expecting you to edit the jail config and then I run a config apply or something like that ... we can stick with main. And yes, I want to do this through the broker."* Then: *"Why do we need a specific push grant? I thought, is this not just writing to a branch? ... I said it should just be like a branch working set, and shouldn't that already cover what's needed here?"* | 2026-10-24 | built |
| <a id="GB-D13"></a>GB-D13 | *Implementation decision,* filling in [GB-D12](#GB-D12): a `branch` grant's `branch` is one branch's short name (`main`, meaning `refs/heads/main`), validated by git's ref-name rules (`git check-ref-format`); a full ref, a pattern or `"*"` is refused at config load with a next step, and tags and other refs are never covered. Its `repo` names one repository and is never `"*"`, unlike a create-branch grant's: with `"*"`, the branch on every approved remote, an upstream organization's remote and any remote approved later included, could be force-pushed or deleted, while the ruling's case was `main` of the agent's own fork. A `"*"` is refused at config load, naming the repository as the next step and one grant per repository, and the scope file refuses it too. This narrows access, so it is the coordinator's decision rather than a ruling. The declared branch is covered exactly as a working-set entry is: creating, updating, force-updating and deleting it, the remote's own protection being the backstop. It is never recorded in the host store: the config holds it, so a delete through the broker leaves it declared, and removing the grant takes it out. A store entry for the same ref is preferred, so a delete through the broker still ends that record. The broker re-reads the live scope file and the user config's `grant_kinds` at every push, so removing either refuses the next push. The audit's set is `working-set` and its `covered_by` names the grant (`branch:main`). `yolo approve` lists each declared branch of a running jail's workspace beside the store's entries, with a `c-` id, marked `from config` with the file that declares it; `--revoke` of one exits 5 and names that file and `yolo config apply`; a `c-` id no running jail declares exits 4, saying it is declared in config and listed only while a jail of its project runs. `--revoke all --workspace` leaves declared branches in place, and when they are all that workspace holds it names their config file instead of pointing back to the listing. `--json` carries each one as a working-set entry with `"by": "config"` and `"file"`. A declared branch whose kind the user config no longer lists says so only when the push is refused, not when a create-branch grant covers it | 2026-10-24 | built |
| <a id="GB-D14"></a>GB-D14 | *Reconciliation,* carrying [BB-D99](../design/boundary-broker.md#BB-D99) to git: every grant the git source records will be held by the workspace and kept across broker and jail restarts and relaunches until it expires, is used up or is revoked, as the GitHub source's are; a broker stopping ends none. Since 2026-10-29 the git source records Allow once grants ([GB-D16](#GB-D16)), held the same way; since 2026-10-30 its timed grants are working-set entries with an expiry ([GB-D26](#GB-D26)), held by the workspace the same way. Its working-set entries were already keyed by workspace, nothing ends them at broker or jail stop, and the integration test `TestGitBrokerCreateBranch` already pushes on the working set alone after a relaunch, with a new broker | 2026-10-24 | built in the shared store ([BB-D99](../design/boundary-broker.md#BB-D99)) |
| <a id="GB-D15"></a>GB-D15 | **Maintainer ruling, carried to git** ([BB-D104](../design/boundary-broker.md#BB-D104), [BB-D105](../design/boundary-broker.md#BB-D105)): the git-broker has no switch. Selecting the git pack starts it in every workspace and serves the remote rewrite, and it grants nothing: every fetch and push is refused until a `read` grant covers the remote. The usual grant is the user config's `"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}]}}`, shown at each workspace's config-change approval. *"a user config that gives all workspaces read-only access to their configured git repo"*. Supersedes [GB-D5](#GB-D5) and [GB-D9](#GB-D9)'s "read needs no grant entry"; `read` is now a grant kind like the others ([BB-D107](../design/boundary-broker.md#BB-D107)) | 2026-10-24 | built: as [BB-D106](../design/boundary-broker.md#BB-D106) to [BB-D109](../design/boundary-broker.md#BB-D109); the git side in `gitbroker` `serveRequest`, pinned by `TestNoReadGrantRefusesEveryCall`, `TestUserReadGrantAppliedLetsTheFetchThrough`, `TestUserReadGrantWithdrawnRefusesTheNextFetch` and `TestReadGrantOnAnotherRepoCoversNothing` |
| <a id="GB-D16"></a>GB-D16 | **Maintainer ruling (2026-10-29), accepting the proposed default:** a push no grant or working-set entry covers files a request, and the only answers are Allow once and Deny. `yolo approve <id> --for` and `--add` are refused for git, as for GitHub, exit 5, naming Allow once as the next step. **Superseded in part 2026-10-30 by [GB-D26](#GB-D26):** the owner ruled to build the other two answers, so `--for` and `--add` are no longer refused for git. The Allow once answer lasts 15 minutes, is used once, and is held by the workspace ([BB-D77](../design/boundary-broker.md#BB-D77), [BB-D99](../design/boundary-broker.md#BB-D99)). The push waits 30 seconds ([§3.2 there](../design/boundary-broker.md#32-a-write-and-the-bounded-wait)). Unanswered, the push is refused `(needs approval: r-<id>)`. Denied, it is refused `(needs approval: r-<id>, denied on the host)` | 2026-10-29 | built: `gitbroker.askForPush`, `gitbroker.ApprovalStore`, `allowOnceOnly` in `internal/cli/approve.go`; Allow once pinned by `TestPushToMainAllowedOnceGoesThroughSSH`, `TestPushToMainAllowedOnceGoesThroughHTTPS`, `TestPushAllowedDuringTheWaitGoesThrough`, `TestPushDeniedNothingForwarded`, `TestApproveGitRequestAllowOnceAndRefusesFor` and the integration test `TestGitBrokerAllowOncePush` |
| <a id="GB-D17"></a>GB-D17 | **Maintainer ruling (2026-10-29), accepting the proposed default:** an Allow once's digest covers every command in the push, covered refs included. The request's argv is `git-receive-pack` followed by one `<old> <new> <ref>` per command, in the push's order. Its stdin digest is that of empty input. The display marks which refs needed approval (`Request.NeedsApproval`, shown by `yolo approve` and carried as `needs_approval` in `--json`, and outside the digest). *Extended 2026-10-29 after review, an implementation decision:* the push options the push sends upstream are bound too, one `push-option=<value>` line each after the commands, in the push's order, so they are in the digest, in `--json`'s argv, and on their own lines in `yolo approve`'s listing. A server acts on options (GitLab merges a request or sets CI variables by them), so the same commands sent with other options, or none, spend nothing. The listing shows ids to 12 hex digits | 2026-10-29 | built: `gitbroker.pushArgv`, `gitbroker.Broker.pushRequest`, `printPushRefs` in `internal/cli/approve.go`; pinned by `TestAskForPushSpendsExactGrantOnce`, `TestApprovalAuthorityRefusesForeignRefsAndHolders`, `TestPushOptionsAreBoundIntoTheAllowOnce` and `TestApproveGitRequestAllowOnceAndRefusesFor` |
| <a id="GB-D18"></a>GB-D18 | **Maintainer ruling (2026-10-29), accepting the proposed default:** a tag push files a request too. The refused tag's line says every tag push asks for approval, or can be pushed from the host. A tag never joins the working set | 2026-10-29 | built; the refusal line's wording superseded by [GB-D25](#GB-D25), which names a tag grant; pinned by `TestTagPushAsksForApproval` and `TestRefusedTagNamesTheTagGrant` |
| <a id="GB-D19"></a>GB-D19 | **Maintainer ruling (2026-10-29), accepting the proposed default:** the Allow once is spent under the store's lock, and the push is forwarded upstream after that lock is released | 2026-10-29 | built: `brokerstore.Store.SpendExactOrFileContext` with no dispatch, called from `gitbroker.askForPush`, which spends a grant or else joins or files the request in one transaction, so an answer cannot land between the two; after the wait, `brokerstore.Store.ConsumeExactDispatchContext`. Pinned by `TestAskForPushSpendsAnAnswerLandingAfterItsFirstTransaction` |
| <a id="GB-D20"></a>GB-D20 | **Maintainer ruling (2026-10-29), accepting the proposed default:** the SSH upstream connection stays open during the wait, and a connection that drops after the spend is not refunded | 2026-10-29 | built |
| <a id="GB-D21"></a>GB-D21 | **Maintainer ruling (2026-10-29), accepting the proposed default:** a request shows each ref's old id and new id only, with no force mark. Supersedes [grants in two layers](#grants-in-two-layers)'s earlier `force` mark | 2026-10-29 | built: `printPushRefs` |
| <a id="GB-D22"></a>GB-D22 | **Maintainer ruling (2026-10-29), accepting the proposed default:** a branch creation forwarded under an Allow once joins the working set, its entry recorded with `By` set to the grant's `g-<id>` | 2026-10-29 | built: `gitbroker` `record`; pinned by `TestAllowOnceCreationJoinsTheWorkingSet` |
| <a id="GB-D23"></a>GB-D23 | *Implementation decision,* filling in [GB-D16](#GB-D16). **(a)** A request's jail is the short hash of the scope file's container (`paths.JailShortHash`), not the id the connection's preamble carries. **(b)** The store's authority, `gitbroker.ApprovalAuthority`, accepts a decision only while the filing broker's lifetime lease is live (`brokercore.Live`). It then re-checks the request's holder, set, stdin digest and argv. It also re-checks that the repository is still in the live scope and that a read grant still covers it. A grant's use is checked the same way against the launch spending it. **(c)** A push holding any ref outside `refs/heads/` and `refs/tags/` files no request. **(d)** A broker stopping refuses its jails' pending requests (`brokerstore.Store.End`). **(e)** A spend that finds the request allowed but the grant gone (another run of the same push spent it, it was revoked, or the scope changed) is refused `(needs approval: r-<id>, allowed but its grant no longer covers this push)`, and its next step is a new request, never `yolo approve` of the answered one | 2026-10-29 | built: `internal/gitbroker/approvalauthority.go`, `internal/gitbroker/approval.go`; pinned by the `TestApprovalAuthority*` tests, `TestPushWithARefOutsideHeadsAndTagsFilesNoRequest` and `TestHandlerRunsOnRequestAndRefusesPendingAtStop` |
| <a id="GB-D24"></a>GB-D24 | *Implementation decision, after review (2026-10-29),* carrying the Happy Path Principle ([`happy-path-principle.md`](happy-path-principle.md)) to every refusal of a push that asked: each ends in a step that works from where the user stands. A request no longer pending (allowed with its grant gone, denied, closed at broker stop, expired) never sends the user to `yolo approve <its id>`, which exits 3 for an answered request; the pending limit names `yolo approve` to list and answer the waiting requests; the denial cooldown names the denied request, when the quiet ends, and the host push; a store failure names the push again and `yolo approve`. Each refusal's ref line and its next step are built together (`refusal` in `internal/gitbroker/approval.go`) | 2026-10-29 | built: pinned by `TestEveryApprovalRefusalNamesAWorkingNextStep`, `TestPushAtThePendingLimitNamesYoloApprove`, and `TestGitRefusalNextStepsNameRealCommands`, which runs every `yolo` command a next step names through the CLI |
| <a id="GB-D25"></a>GB-D25 | **Maintainer ruling, superseding [GB-D13](#GB-D13)'s wildcard refusals and [GB-D18](#GB-D18)'s "every tag asks":** a `branch` grant may name a pattern and `repo: "*"`, and tags get the same grant. *"why can't we have a wildcard * branch grant? is there a good reason for that?"*; *"if someone wants to grant repo *, why should we stop them? that feels weird."*; *"allow."*; *"we should be able to wildcard tags as well"*. Shape: a pattern is git's own refspec pattern, one `*` anywhere in the short name, matching any run of characters including `/` (`*`, `patch/*`, `v*`), validated with `git check-ref-format --refspec-pattern`; whole-segment-only, as [WW-D31](../design/workspace-widening.md#WW-D31) does for repository names, was not taken because it would refuse `v*`, the tag case the ruling exists for (a coordinator decision). `repo` may be `"*"`. A new kind `tag` mirrors `branch` for `refs/tags/`, with the same pattern rule, the same two layers (the user's `grant_kinds`, then the config-change approval) and the same coverage (create, update, force-update, delete). The existing two layers are the whole gate, with no separate wildcard kind. **Built with it:** the grant's approval line says in plain words what a wildcard admits ("may create, update, force-push and delete `main` on every remote in scope now and any approved later"), colored as other widenings; and approving a new remote later names every standing wildcard grant that now covers it. The remote's own protection stays the backstop | 2026-10-29 | built: `brokerscope.ValidGrantRefPattern`, `brokerscope.RefPatternMatches` and `brokerscope.GrantNote`, the `tag` kind in the git pack's manifest (`push_tags` in the readable spelling); pinned by `TestPatternBranchGrantCoversMatchingBranches`, `TestAnyRepoBranchGrantCoversEveryRemote`, `TestTagGrantCoversMatchingTags`, `TestRefusedTagNamesTheTagGrant`, `TestWildcardGrantLineSaysWhatItAdmits` and `TestNewRemoteNamesTheWildcardGrantsCoveringIt` |
| <a id="GB-D26"></a>GB-D26 | **Maintainer ruling (2026-10-30), superseding [GB-D16](#GB-D16)'s narrowing to Allow once and Deny:** build the other two answers [GB-D8](#GB-D8) and [BB-D76](../design/boundary-broker.md#BB-D76) name. `yolo approve <id> --for <duration>` is **Allow push to `<ref>` for a time** (15 minutes unless given, at most 24 hours, never `session`) and `--add` is **Add `<ref>` to the working set**. Each records one working-set entry per ref the push needed approval for, held by the workspace across restarts and relaunches (a timed entry carries an `Expires`; `brokerstore.Store.DecideWorking`, the git store's `Entries` hook), and no command grant; the request becomes allowed, and the push waiting on it goes through when the broker finds every ref covered. The broker consults the entries before it files a request, so every push to the ref within the window goes through, unspent. *Implementation decisions:* a tag takes `--for` only (a tag never joins the working set, [GB-D11](#GB-D11), [GB-D18](#GB-D18)), and its refusal of `--add` names `--for 15m`; a branch creation takes `--for` only, since a lasting right to create is a user grant ([BB-D90](../design/boundary-broker.md#BB-D90)), but a branch the agent creates under a timed entry makes the entry lasting ([the working set](#the-working-set)); revoke is `yolo approve --revoke w-<id>`. GitHub keeps Allow once and Deny only (`allowOnceOnly` names only it now) | 2026-10-30 | built `c250da3f8`: `brokerstore.Store.DecideWorking`, `gitbroker.pushEntries`, the coverer's timed-entry cases and `askForPush`'s re-cover; pinned by `TestApproveGitForRecordsTimedGrantAndPushWithinItGoesThrough`, `TestApproveGitAddJoinsWorkingSetAcrossRelaunch`, `TestTimedGrantExpiresAndRefilesRequest`, two `--add` tests deleted with the flag by [GB-D28](#GB-D28), and the integration test `TestGitBrokerAllowPushForATime` |
| <a id="GB-D28"></a>GB-D28 | **Carrying the owner's removal of `yolo approve --add` (2026-11-02, [OQ-GW2](../design/github-write-scopes.md#OQ-GW2)) to git, for the owner's own reason** (*"It rides along with a grant, or it rides along with an approval, and depending on what the approval is, it does different things"*): `yolo approve` answers a git request once, `--for <duration>`, or `--deny`. `--add` and `--remember` ([RC-D39](config/rulings.md#RC-D39)) are removed and refused as unknown flags naming `--for` and the standing path, `yolo request git push <branch>` (or `push_tags <tag>`) then `yolo config apply` on the host, which the refusal of a push already names per ref ([GB-D25](#GB-D25)). Branches a human added before the removal are kept until revoked ([GW-D34](../design/github-write-scopes.md#GW-D34)). Supersedes [GB-D26](#GB-D26)'s `--add` and the "A human adds it" path of [the working set](#the-working-set) | 2026-11-02 | built 2026-11-06: `yolo approve` refuses `--add` and `--remember` (any spelling, with or without a value) with exit 2, deciding nothing, naming Allow once, `--for 15m` and `yolo request git push <branch>` then `yolo config apply` (`refuseRemovedApproveFlag` in `internal/cli/approve.go`); the push refusal's menu offers neither (`pushMenu.menu`), nor does the jail briefing's example menu or the git pack's briefing; entries added before the removal still cover. Pinned by `TestApproveRefusesTheRemovedAddAndRememberFlags`, `TestApproveUsageNamesNoRemovedFlag`, `TestGitRefusalNextStepsNameRealCommands`, `TestEveryApprovalRefusalNamesAWorkingNextStep`, `TestApproveGitForRecordsTimedGrantAndPushWithinItGoesThrough`, `TestTheJailBriefingCarriesTheHandOffRules`, `TestNoShippedPackOffersTheRemovedApproveFlags`, and for the kept entries `TestApproveGitForCLI` and `TestApproveGitAddJoinsWorkingSetAcrossRelaunch` |

### The build's implementation decisions

The planner's decisions for the first slice, `ID-1` to `ID-13`, each reversible within the
rulings above; the **Reverse by** clause says what changing one costs.

| ID | Decision |
| :--- | :--- |
| <a id="ID-1"></a>ID-1 | **Package layout.** The core is a new package, `internal/brokercore`; the git source is `internal/gitbroker`, its jail client `internal/gitremote`, and the wire-format code `internal/gitproto`, shared by both. *Why:* the client runs in the jail and must not import the daemon's upstream code; the protocol code is the one thing both halves need. *Reverse by:* merging packages, no behavior change. |
| <a id="ID-2"></a>ID-2 | **The git scope records the workspace's remote URLs verbatim.** A manifest declaring `"remote_host": "*"` makes core read every remote of the workspace on any host, `url` and `pushurl` alike, and put each URL in front of the human at the config-change gate, verbatim. The scope file records them with `"form": "remote-url"`. Requests match the exact URL by default. The Git broker also admits only these bounded SSH equivalences: SSH schemes with identical parsed destination, explicit port and path, and (only for `git@github.com` without a port) the two-segment relative scp path versus the corresponding absolute SSH URL path. It preserves the request URL for its SSH argv and audit; it does not strip `.git`, trailing slashes, case, `~/` or encoding, nor equate usernames, hosts, ports or generic relative/absolute paths. HTTPS and all other callers remain exact. An `https://` URL whose user-info carries a password is left out of the scope, with a gate line saying why. `brokered.git.repos` is refused by `yolo check`: a git remote is added with `git remote add`, which reaches the gate as a new remote. *Why:* the design says the upstream comes from the scope ([§7](#what-this-does-not-do)), and this narrow matcher gives the jail no choice of upstream endpoint. *Reverse by:* a pattern form later; the `form` field is what makes that additive. |
| <a id="ID-3"></a>ID-3 | **Repository identity.** Grants and working-set entries name a repository as `host/path`: the host lower-cased, a port kept as `host:port`, the user-info dropped, a leading `/` or `~/` stripped from the path, a trailing `/` then a trailing `.git` stripped. So `git@github.com:o/r.git`, `ssh://git@github.com/o/r` and `https://github.com/o/r.git` are all `github.com/o/r`. *Why:* one branch must not be two working-set entries because it has two remotes. *Reverse by:* re-keying the store, before any release carries it. |
| <a id="ID-4"></a>ID-4 | **The jail leg is a git remote helper.** The jail's git rewrites `ssh://`, `git+ssh://`, `ssh+git://`, `https://` and scp-style `git@` URLs (including bracketed IPv6 scp hosts) to `yolo::<original URL>`, by `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_<n>`/ `GIT_CONFIG_VALUE_<n>` from a pack `env` contribution. Git then runs `git-remote-yolo <remote> <url>`, placed by an `intercept` contribution forwarding to `yolo git-remote --`. The helper answers `capabilities` with `connect` and nothing else. Not rewritten, and documented: `http://`, `git://`, `file://`, local paths, and scp-style remotes that do not start with `git@` (such as `host:path` and `alice@host:path`). *Why:* the env keeps the jail's global git config, which yolo composes, untouched, and the `yolo::` prefix carries the original URL to the helper unchanged. *Reverse by:* moving the rewrite into the composed global config. |
| <a id="ID-5"></a>ID-5 | **The jail-to-host stream.** The helper sends one request frame `{"service":"git-upload-pack"\|"git-receive-pack","url":"<original URL>","protocol":"<GIT_PROTOCOL or empty>","agent":"<agent name or empty>"}`, then git's bytes as Stdout(0) frames; a Stdout frame with an empty payload means git closed its side. The broker answers with Stdout frames (git's protocol), Stderr frames (shown to the user) and one Exit frame. The helper prints git's blank "connected" line immediately before relaying the first host Stdout bytes; on a refusal that arrives before any, it prints the Stderr text and exits with the Exit code without the blank line, so git reports the helper's failure. `git-upload-archive` is refused. *Why:* this is the serial bridge's shape, already served by `hostservice`. *Reverse by:* wire change in two packages, both new. |
| <a id="ID-6"></a>ID-6 | **The rewrite exists only where the broker runs.** The env contribution is `served_by: "git-broker"`, and a new `loopholes.Set.FrontedHostNames(from, runtime)` lists the enabled loopholes whose `host_daemon` serves the jail over `loopback-tls`, gated by `admitsJailSideEffects`, and none on Apple Container (`runtime == "container"`), which runs no host services. Its names join the served set at the launch (`assemble.go`'s `servedDaemons`, recorded beside `o.jailBound` in `packservices.go`) and at `yolo check` (`check/served.go`). So with the loophole off, the jail's git is untouched, and `yolo host --` withholds the env as unserved. It is a config-level answer: a daemon that then fails to start leaves the rewrite in place, and the helper says the endpoint is missing and names `yolo check`, as [§6](#failure-paths) requires. *Why:* without the gate, enabling the pack would break every remote fetch in a jail with the loophole off, and every host git at `yolo host --`. *Reverse by:* deleting the method and its three call sites. *Built differently (Task 7):* `FrontedHostNames` also leaves out a loophole that declares a `jail_daemon` beside its `host_daemon`. Such a loophole (aws-auth's shape) serves its pointers through the jail daemon, whose address they name, and `loopholes.ServedJailDaemonNames` already decides where that one runs; listing it here too would deliver its pointers on a runtime whose jail daemon does not run there, such as the macos-user guest's declines. A withheld rewrite names its own reason (`loopholes.Set.FrontedHostWhy`, passed through `WithNotServedWhy` at the launch, at `yolo check` and at `yolo host --`): the loophole off names `yolo loopholes enable git-broker`, Apple Container says it runs no host services, and the host says its agent's git already reaches remotes with the host's own credentials. **Superseded in part by [GB-D15](#GB-D15):** the git-broker has no switch, so selecting the pack serves the rewrite; the Apple Container and `jail_daemon` exclusions stand. |
| <a id="ID-7"></a>ID-7 | **SSH upstream.** The broker runs the host's `ssh` (placement-checked) as `ssh -x -T -o ClearAllForwardings=yes -o PermitLocalCommand=no -o ConnectTimeout=30 [-p <port>] [-o SendEnv=GIT_PROTOCOL] -- <[user@]host> "<git-upload-pack\|git-receive-pack> '<path>'"`, the path single-quoted by `shquote`, as a stateful pipe. A host starting with `-` is refused. Its env is built from nothing: `PATH`, `HOME`, `USER`, `SSH_AUTH_SOCK`, and, when set, `DISPLAY`, `WAYLAND_DISPLAY`, `SSH_ASKPASS`, `SSH_ASKPASS_REQUIRE`, plus `GIT_PROTOCOL` when the request carried a valid one (`version=2` or `version=1`, nothing else). No `BatchMode`, so a key needing a touch or passphrase prompts on the host, as the design requires. *Why:* this is what `git` itself runs for an SSH remote, minus forwarding. *Reverse by:* local change in one file. |
| <a id="ID-8"></a>ID-8 | **HTTPS upstream, smart HTTP.** Credentials come from the host's `git credential fill` (`protocol`, `host`, `path` on stdin), run placement-checked, in an empty broker-owned directory, with `GIT_TERMINAL_PROMPT=0`, `GIT_CEILING_DIRECTORIES=<that dir>` and an env of `PATH`, `HOME`, `DBUS_SESSION_BUS_ADDRESS`. A fill that returns no password is the "host has no credential" refusal; `approve` or `reject` is reported back to the helper after the first authenticated response. Fetch always asks the server for protocol v2 (`Git-Protocol: version=2`): the broker relays the capability advertisement, then POSTs each client request up to its flush and relays the response. A server that answers v0 is refused: "the HTTPS server does not speak protocol v2; use its SSH remote". Push uses `info/refs?service=git-receive-pack`, relays the advertisement without the `# service` line and its flush, reads the commands to the flush and the pack to the client's end (no pack when every command is a delete), POSTs them, and relays the report. TLS uses the system roots, with no option to skip verification. *Why:* v2 fetch is request-response, which a stateful client stream maps onto stateless POSTs without a negotiation state machine. *Reverse by:* adding v0 stateless-rpc negotiation if a server needs it. |
| <a id="ID-9"></a>ID-9 | **Push policy reads the commands before forwarding anything** and is all-or-nothing ([GB-D4](#GB-D4)). A ref outside `refs/heads/` and `refs/tags/` is refused always. In this step every other ref no grant or working-set entry covers is refused with `needs approval, which this version cannot ask for` (no request is filed: requests turn on in step 3). Refusing answers the client with `unpack ok` and one `ng <ref> <reason>` per command, side-band wrapped if the client negotiated it. A creation (old id all zeros) under `refs/heads/` is covered by a `create-branch` grant on the repository identity or `*`; an update or delete is covered by a live working-set entry for that ref. No force detection in this step: a force push to a working-set ref is an update. *Why:* the design's [§4.2](#a-push) and [§5](#grants-in-two-layers). *Reverse by:* none needed; step 3 replaces the refusal with filing. |
| <a id="ID-10"></a>ID-10 | **Grant configuration** ([BB-D96](../design/boundary-broker.md#BB-D96)). The manifest's `brokered` block gains `grants`, the kinds the source defines (`["create-branch"]` for git; none for github in this plan). User config: `brokered.<source>.grant_kinds: ["create-branch"]`. Workspace config: `brokered.<source>.grants: [{"kind": "create-branch", "repo": "github.com/o/r"}]`, `"repo": "*"` being the written-out wildcard. `yolo check` refuses a workspace grant of a kind the source does not define, a kind the user config does not list (naming `brokered.<source>.grant_kinds` in the user config), a `repo` that is not a repository identity or `*`, `grant_kinds` at workspace scope and `grants` at user scope. `read` is implied by the loophole being on; writing it warns. Grants appear in the config-change gate as their own lines under the source's scope block, are recorded under the approval record key `<source>.grants` (a source name cannot contain `.`, so it collides with nothing), and are written to the scope file's `grants`. At every push the broker reads the live scope file's grants and re-reads the user config's `grant_kinds`, so removing the kind on the host takes effect at the next push. `yolo config apply` replaces a running jail's grants as it replaces its repositories. *Why:* the gate is the "config-change approval" BB-D96 names, and `config apply` is that gate run live. *Reverse by:* key renames before release; `yolo config-ref` is the authority once built. **Superseded in part by [GB-D15](#GB-D15):** `read` is a grant kind like the others, and every fetch is refused until one covers the remote. |
| <a id="ID-10a"></a>ID-10a | **A grant's `repo` needs a host with a `.` or a `:`.** *Implementation decision*, made in Task 8. A grant's `repo` is `*` or a repository identity (ID-3) whose host contains a `.` or a `:` (a port). So a scope entry on `git@gitserver:team/app.git`, whose identity is `gitserver/team/app`, is admittable, but a grant covering it must be `"*"`. *Why:* so the GitHub shorthand `o/r` written as a grant's `repo` is refused, rather than read as host `o` and path `r`. *Reverse by:* accepting a single-label host when the identity has two or more path segments after it. |
| <a id="ID-11"></a>ID-11 | **The working set's records.** A new record kind in the source's store, `working/w-<id>.json`: `ID`, `Workspace`, `Repo` (identity), `Kind` (`"branch"`), `Resource` (the ref), `Added`, `By` (`"create-branch"`, a grant id, or `"human"`), `Ended`, `EndReason` (`deleted`, `gone`, `revoked`). One live entry per `(Workspace, Repo, Resource)`; adding a duplicate is a no-op. A creation the upstream did not report `ok` records nothing. A creation on a ref that already has a live entry means the branch was deleted upstream: the entry ends `gone` and the push is judged as a creation. A delete reported `ok` ends the entry `deleted`. Writes go through the store's existing transaction lock. *Why:* [§5.1](#the-working-set) and [BB-D85](../design/boundary-broker.md#BB-D85). *Reverse by:* store migration, before any release carries it. |
| <a id="ID-12"></a>ID-12 | **The git source ships as its own pack**, `packs/git`, with loophole `git-broker`, `default_enabled: false`, enabled per workspace by `yolo loopholes enable git-broker` ([GB-D5](#GB-D5) left the pack to the implementer). *Why:* selecting the github pack must not include git ([OQ-BB16](../design/boundary-broker.md#OQ-BB16)), and a pack selected without its loophole enabled changes nothing (ID-6). Its `credential_paths` fence `~/.ssh`, `$SSH_AUTH_SOCK`, `~/.git-credentials` and `~/.config/git/credentials`, so with the pack selected a workspace `mounts` entry reaching one is refused and a user-scope one disclosed ([BB-D26](../design/boundary-broker.md#BB-D26)). *Reverse by:* moving the contributions into another pack; trimming the fence list. **Superseded in part by [GB-D15](#GB-D15):** there is no `default_enabled` switch and no `yolo loopholes enable git-broker`; selecting the pack starts the broker, which grants nothing until a `read` grant does. |
| <a id="ID-13"></a>ID-13 | **Audit.** Git events use `Service: "git"`, `Repo` the identity, `Remote` the URL, `Set` one of `read`, `create-branch`, `working-set`, `refused`, and `Refs`, one `{ref, old, new, covered_by}` per command, `covered_by` naming the grant kind or the `w-` id. `Argv` holds `[service]` alone. *Why:* `yolo audit --service git` must answer "what did it push with my access" ([§7](#what-this-does-not-do)). *Reverse by:* additive fields only. |
