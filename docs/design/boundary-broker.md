# A stateful broker on the jail boundary — approvals for what crosses

**Status:** DESIGN SKETCH, 2026-08-05. Nothing built. Where the answer is "this already exists,"
it says so.

**The thesis, from the maintainer:**

> "I want to put some sort of service in between the contained area and the host, just like
> loopholes, but positioned in a place where it can hold tasks that are waiting for a human for
> approval or have other state associated with it… the agent inside can ask to use my GitHub
> credentials to post a comment to a PR, and then it will go to a queue and I can say yes or no."

**The short version.** The mechanism is 90% built — it is the loophole protocol plus what the
OAuth broker already does. What is missing is not a transport or a daemon framework; it is **two
things a loophole cannot express today: a request that OUTLIVES its connection, and a request
whose answer comes from a HUMAN rather than from the host filesystem.** Those two absences are
the whole design, and they are smaller than they sound.

**The most important thing in this doc is §5:** for the motivating GitHub case, an approval queue
is probably the *wrong* tier. There are three tiers, not two, and the middle one needs no human.

**Before building any of it, read §10.** [unyolo.io](https://unyolo.io/) is an MIT-licensed
credential broker that converged on this design independently and solves several problems this doc
had not thought of. **§10 was rewritten 2026-08-12 from the source rather than the website, and the
verdict flipped: B1b is a BUILD, not an adoption** — but a smaller build than it looked, because
the transport half already exists in this repo. §10.6 says which of unYOLO's ideas to take.

**Scope note.** Claude auth switching was originally sketched here as a third use case. It is a
different feature that wants the same front door, and it is now
[`agent-auth-modes.md`](agent-auth-modes.md) — split out because it shares this doc's front door
and none of its blockers (this waits on N3/OQ-1; that waits on nothing). §6 keeps only the
constraint the two share.

**Reads with:** [`loophole-protocol.md`](loophole-protocol.md) (the wire format this extends),
[`agent-credentials.md`](agent-credentials.md) (what crosses the boundary today and why),
[`agent-auth-modes.md`](agent-auth-modes.md) (the split-out sibling),
[`../guides/loopholes.md`](../guides/loopholes.md) (the three shipped loopholes),
[`../plans/outstanding-work.md`](../plans/outstanding-work.md) (rows B1/B2).

---

## 1. What already exists, stated precisely

This matters because the instinct "build a new service" is usually wrong here — three of the four
pieces are shipped.

| Piece | Where | State |
|---|---|---|
| A framed request/response protocol across the boundary | `internal/frameproto`, [`loophole-protocol.md`](loophole-protocol.md) | **shipped**, versioned, documented for external authors |
| A host-side daemon framework with per-jail identity | `internal/hostservice`, `yolo internal daemon <name>` | **shipped** — 4 daemons ride it |
| A host daemon holding CROSS-JAIL state behind a lock | `internal/oauthbroker` — `RefreshLockPath`, an flock every broker instance agrees on | **shipped**, and it is the closest precedent |
| Argv safety for host-side execution | `Session.exec_allowlisted(argv, allowlist=…)` — argv positions validated against a server-owned allowlist | **shipped**, enforced by construction |
| A request that outlives its connection | — | **MISSING** |
| A human in the answer path | — | **MISSING** |

**The OAuth broker is the existence proof.** It already is a stateful boundary service: it
serializes refreshes across multiple jails because Anthropic's refresh token is single-use, so two
jails refreshing concurrently burn each other's token. That is *exactly* the shape of "hold state
on the boundary that no single jail can hold correctly." The proposed broker is that pattern with
a second consumer.

**What the loophole protocol says that constrains this** (§Security posture): *"The socket file is
the authentication. A daemon trusts whoever can connect() — which is the jail (and anything else
running as the same user on the host)."* So the boundary is **not** a security boundary against a
malicious jail occupant; it is a boundary against *accident and unaudited action*. That framing
decides several things below, and getting it wrong would oversell the feature.

> **The quote is the OLD wording, 2026-08-13; the constraint it names is unchanged.**
> [`loophole-protocol.md`](loophole-protocol.md) §Security posture now says the boundary is
> *"whatever runs as your user"* and names the per-jail token as what enforces it on a port — because
> there is no socket file on the jail-facing hop any more (see
> [`loophole-transport.md`](loophole-transport.md)). Every conclusion in this document that rests on
> the sentence survives verbatim: the same-user set is the specification, so this is still a boundary
> against accident and unaudited action, not against a hostile occupant. Quoted here as history so
> the argument's premise is traceable — do not re-derive the old wording from it.

---

## 2. The two absences, and why they are the whole design

### 2.1 A request that outlives its connection

Every loophole today is synchronous: *"A client opens the Unix socket, sends one length-prefixed
JSON request, and reads framed response data until the server closes the connection."* The request
lives exactly as long as the connection.

An approval cannot work that way. The human may be at lunch. The agent may want to do something
else meanwhile. The jail may restart. So the request needs an **identity that outlives the
socket** — which means:

- a **queue** with durable entries (id, jail, ask, created-at, state),
- a **poll or wait** call the jail can re-issue after a disconnect,
- and a **decision record** that survives the answering.

This is the part that is genuinely new. Everything else is plumbing that exists.

### 2.2 A human in the answer path

Today a host daemon answers from the host filesystem or from a subprocess it controls. Nothing in
the boundary asks a person. The nearest thing is `promptYesNo` in `internal/cli` — used by
`apply --host`'s confirm gates — but that is a **foreground CLI prompt on the human's own
terminal**, in a process the human started. An approval queue is the inverse: the *agent* starts
it, and the human answers later, possibly from a different terminal.

Two properties from those existing gates should carry over verbatim, because they were learned the
hard way and are the reason `--assert` on a real `$HOME` is trustworthy:

1. **FAIL-CLOSED on absent input.** `promptYesNo` reads nil/EOF stdin as **no**, so a scripted run
   aborts rather than silently proceeding. An approval queue with no human attached must **deny**,
   not block forever and not default yes.
2. **Only ask when something is actually at stake.** A confirmation that fires on every run trains
   people to hit `y` without reading. This is the single biggest design risk of the whole feature
   — see §6.

---

## 3. Use case A: approval-gated host credentials (the GitHub case)

**The ask:** the agent wants to post a PR comment using the human's GitHub credentials. It asks;
the request queues; the human approves; the action happens.

**Why this is the strong case.** It closes a real hole in the current model, which the jail's own
docs state plainly: *"Host credentials are not propagated into the jail: the host's ~/.ssh,
~/.gitconfig, and cloud/gh tokens are invisible here. This is a credential boundary."* Today the
only two options are **no access** (the default, and why this jail reads `origin` via a
workspace-local `GH_TOKEN`) or **hand the jail a token**, which makes the boundary a fiction for
that credential's whole lifetime. An approval queue is the missing middle: **the credential never
crosses, the ACTION crosses.**

That distinction is the design's most important property and should be a hard rule:

> The broker performs the action host-side using host credentials and returns the RESULT. It never
> hands the jail a credential, not even scoped, not even short-lived.

Because the moment a token crosses, every later use is unaudited and un-revokable — and the
`exec_allowlisted` precedent already shows the shape for "the jail names an intent, the host owns
the argv."

**What an ask should carry** (sketch, not a schema):

| Field | Why |
|---|---|
| `action` | a server-owned **allowlisted** verb (`github.pr_comment`), never argv |
| `args` | validated per verb against a server-owned schema |
| `why` | free text FROM THE AGENT — the human is approving intent, and intent is not derivable from argv |
| `jail`, `workspace` | who is asking; the protocol already stamps `jail_id` |
| `preview` | the exact effect, rendered host-side (see below) |

**The preview is what makes approval meaningful.** A human approving `github.pr_comment` is not
approving anything they can evaluate. A human approving *"post this 400-character comment to
PR #32 on mschulkind-oss/yolo-jail"* is. Render the preview from the **validated** args on the
host, never from a string the jail supplied — otherwise the jail can describe one action and
request another, which is the boundary-service equivalent of a confused deputy.

**Scope, deliberately narrow to start:** one verb. `github.pr_comment` is a good first one — it is
useful immediately (this session has wanted it), it is low-blast-radius, and it is *irreversible
enough to be worth a prompt* without being dangerous enough that a mistake is a crisis.

---

## 4. Use case B: the approval prompts at the jail boundary

The maintainer's framing: *"I want to be able to do the agent approval prompts the same way Claude
and everyone does them, but I want them to be on the jail boundary."*

**This is the more radical idea in the pair, and the more interesting one.** Today the jail runs
`--dangerously-skip-permissions` with `IS_SANDBOX=1` and `permissions.allow: []` — i.e. **the
agent's own prompts are turned OFF entirely**, and the container is the safety net. That is
coherent, and it is why `autonomy` is a confinement policy: prompts off at `jail`/`guest`, on at
`host`.

Moving prompts to the boundary would be a **third posture**, not a replacement for either:

| | who decides | what is protected |
|---|---|---|
| agent-internal prompts (`host` notch today) | the agent asks, human answers in-band | the whole machine |
| no prompts (`jail`/`guest` today) | nobody; the container contains | everything outside the jail |
| **boundary prompts (proposed)** | the human answers OUT of band | the specific things that cross |

**Why the boundary is arguably the right place.** An in-agent prompt is only as trustworthy as the
agent: it is the thing being restrained, and it renders its own prompt text. A boundary prompt is
rendered by yolo from validated arguments, outside the agent's control — which is a materially
stronger property, and the same argument as the preview above.

**Why it is not a drop-in replacement.** In-agent prompts cover *everything the agent does*
(editing a file, running a command). A boundary broker only ever sees what crosses the boundary.
So this is not "move the prompts"; it is **"gate the crossings, and keep the container for the
rest."** Framing it as a replacement would oversell it and, worse, invite turning the container's
guarantees down to compensate.

---

## 5. Three tiers, not two — and git wants the middle one

The framing above ("no access, or hand over a token") is a false binary, and the missing option
is the one the GitHub case most likely wants. **Approval is the heaviest of three tiers, and the
maintainer's own instinct — "it's possible for the git thing that we'll want to just filter access
instead" — is right more often than this doc's §3 implies.**

| Tier | Credential lives | Human in the loop | Right when |
|---|---|---|---|
| **filter** | in the jail, scoped | no | the permission system can *express* the limit |
| **proxy** | host-side, injected after egress | no | it cannot, but the action is still mechanically checkable |
| **approve** | host-side, action gated | **yes** | the judgment is genuinely human |

**Filter** is what this workspace already does: a fine-grained PAT with `Contents: Read` on one
repo, delivered by `env_sources`, is how this jail reads `origin`. It needs no queue, no daemon, no
UI, and no human. It is the correct answer whenever the scope you want is a scope the provider can
express.

It stops working at two points. First, expressiveness: a GitHub PAT cannot say "may comment, may
not push," or "may push only to `agent/*`". Second, and more important, **a token inside the jail
is unauditable and un-revokable per-action for its whole lifetime** — which is the boundary
becoming a fiction for that credential, exactly as §3 says.

**Proxy is the tier this doc was missing.** Route the operation through a host-side proxy that
injects the credential *after the request leaves the jail*. The jail holds nothing; the host holds
the secret and sees every request. This is not speculative — it is the shape Anthropic's own
managed-agents git path uses: the repo token is never placed in the sandbox, and `git pull` /
`git push` / GitHub REST calls route through a proxy that injects it on the way out, so code
running in the sandbox cannot read or exfiltrate it.

For git specifically that is a credential helper or an `http.extraHeader` pointing at a host-side
endpoint, and it delivers **this doc's own central rule — the action crosses, the credential does
not — with no human in the loop at all.** It also gets the audit log (§7 step 1) for free, since
every request passes through one place.

**And yolo already ships this mechanism.** `claude-oauth-broker` is a credential-injecting
TLS-interception proxy today — an `intercepts` list, an in-jail terminator on
`127.0.0.1:443`, a per-jail host relay that stamps `jail_id` host-side, a host singleton holding the
credential. B1b is that pattern re-aimed from `platform.claude.com` at `github.com`, which is why
[§10.6](#106-recommendation--build-b1b-vendor-the-policy-engine-do-not-adopt-gh-broker) concludes
it is a build rather than an adoption.

**So the recommendation for the GitHub case changes:** reach for **proxy** first, and reserve
**approve** for the operations where a human would actually say no. Posting a PR comment is
probably proxy-tier. Force-pushing to `main` is approve-tier. That distinction is the same
forcing question as §6.1, applied earlier.

**What this does not change.** The approval tier still needs to exist for the class of action
where the judgment is irreducibly human, and the two absences in §2 are still what it costs.
Nothing below is retracted — it is re-scoped to a smaller set of verbs than "anything using host
credentials."

---

## 6. The risks worth naming before any code

**6.1 Prompt fatigue is the design-killer.** If this asks too often, the human becomes a rubber
stamp and the feature is worse than not having it — it manufactures the appearance of oversight.
The existing confirm gates got this right by firing *only* when something is actually lost, and
that discipline is not optional here. A good forcing question: **would the human ever say no?** If
not, it should not be a prompt; it should be an allowlist entry or a log line.

**6.2 The boundary is not a security boundary against the jail occupant.** Per the protocol's own
security posture — since 2026-08-13 worded as *the boundary is whatever runs as your user*, with a
per-jail token enforcing it where there is no socket file to permit — anything running as the same
user can connect. So this protects against **accident and unaudited action**, not against a hostile agent
that could simply ask again, or ask for something innocuous-looking. Documenting it as a security
control would be a lie, and the doc should say so where a user reads it.

**6.3 A queue is durable state, which yolo has been careful about.** Where does it live, who reaps
it, what happens to a pending ask when the jail dies, and does an approval granted at 09:00 still
apply at 17:00? **Expiry should be mandatory, not optional** — a stale approval is worse than no
approval because it looks current. `yolo prune`'s existing sweeps are the precedent for reaping,
and the `PruneOrphanImageRoots` lesson applies: put this queue in its **own** directory, because a
sweep that reaps "everything under X that no live jail needs" will happily reap a pending human
decision.

**6.4 Auditability is the actual product.** Even before approvals, a *log* of every boundary
crossing ("jail X asked to post a PR comment") has most of the value and none of the risk. The
loophole framework already logs one structured line per request (`jail=… keys=… rc=… elapsed_ms=…`)
and deliberately does **not** log bodies. An approval broker wants the opposite for the ask itself
— the human needs the body to decide — which is a real tension worth resolving deliberately rather
than by default.

**6.5 Do not let this become a general RPC.** The moment `action` accepts arbitrary argv, the
boundary is gone and yolo has shipped a remote-execution service that happens to prompt. The
`exec_allowlisted` precedent is the guardrail: **server-owned allowlist, validated args, no argv
from the jail, ever.** This is the invariant most likely to erode under "just one more verb."

---

## 7. What I would build, in order

Each step is independently useful, which is the property that makes this safe to start.

1. **Audit-only crossing log.** Every boundary request logged with enough context for the human to
   review after the fact. No queue, no prompt, no new protocol — it is mostly already there.
   Establishes what people actually ask for, which should inform every later decision. **Cheapest
   thing with real value.**
2. **The injecting proxy for git** (§5). No human, no queue, no new protocol beyond a credential
   helper — and it satisfies the "action crosses, credential does not" rule outright. This is the
   step that most likely *removes* the need for step 3 in the motivating case.
3. **One allowlisted verb, synchronous approval.** Human answers at a foreground `yolo approve`
   while the jail blocks with a timeout. Proves the preview-and-validate design without any
   durable queue. Fail-closed on timeout. Pick the verb from what step 1 shows people ask for and
   step 2 cannot cover — deliberately *not* pre-chosen here any more, because §5 makes
   `github.pr_comment` a poor first candidate: it is proxy-tier.
4. **The durable queue.** Only once (3) shows the synchronous version is genuinely too limiting —
   which it may not be, if the human is usually present.

### 7.1 One front door, several front-ends

The maintainer wants "a web app locally hosted, ideally one for all jails, or similarly control
things with a tui/cli through the same protocol." That grain is already right: daemons are
host-side and per-user, jails are clients, and the OAuth broker is already one singleton serving
every jail. Multiple front-ends over one store is the natural shape.

The discipline that makes it work: **the state is the API and the front-ends are dumb.**
`yolo approve`, a TUI, and a web app are all clients of the same daemon. That also dissolves OQ-E
— "where does the human answer" stops being a state-design question and becomes a packaging one.

**One caution that is not cosmetic.** The protocol's security posture is *"the socket file is the
authentication."* A TCP port is not that. In bridge mode a jail reaches the host at
`host.containers.internal`, so **an HTTP approval UI is reachable by the very thing whose requests
it approves** — loopback-binding does not exclude the jail. Keep authority in the unix socket and
make HTTP a thin local proxy, or the approver and the approved share a channel.

---

## 8. Where this sits against the rest of the queue — my priority read

Asked for explicitly, so stated plainly rather than hedged.

**Below everything currently in [`outstanding-work.md`](../plans/outstanding-work.md), except
possibly step 1.** The reasons:

- **The queue's remaining items are defects and rulings; this is a new capability.** S1 (skills
  collisions are silently lost), S3 (the jail reads its own output as the user's tree), P7 (the
  middle notch does not work) are all *things that are wrong*. This is a thing that does not exist
  yet. Wrong-things-first is the right default when the wrong things include silent data loss.
- **It has an unanswered design question upstream of it:** OQ-1 / N3 — whether `host` is a place
  agents *run* or only get *configured*. A boundary approval service is much more compelling in a
  world where yolo launches processes at multiple notches, and its shape differs. Deciding N3
  first costs nothing and de-risks this.
- **Step 1 (the audit log) is the exception** and could be done any time: it is small, it is
  strictly additive, it has no design risk, and it produces the evidence that would tell us which
  verbs are worth gating. If any part of this jumps the queue, that is the part.
- **Step 2 (the injecting proxy) is the second exception**, and it is not blocked on N3/OQ-1 the
  way the approval tier is — it has no human in it, so it does not care whether `host` is a place
  agents run. It is also the step that makes the motivating use case work, which is a better
  reason to do it than its position here suggests.
- **Auth-mode modeling was split out** to [`agent-auth-modes.md`](agent-auth-modes.md) (row B3)
  and is **higher value than anything in this doc**, because it is a real gap today rather than a
  new capability. It waits on nothing.

**What would move it up:** a concrete instance of wanting it that the current model blocks. The
GitHub-comment case is close — this session has already wanted it — and if that recurs, step 2
becomes cheap to justify.

---

## 9. Open questions for the maintainer

- **OQ-A. Is the synchronous version enough?** Most of the complexity here is durability. If the
  human is usually at the keyboard, a blocking ask with a timeout may cover the real need — and
  step 3 never has to happen. **Resolved by:** trying step 2.
- **OQ-B. Should an approval be reusable?** "Yes, and don't ask again for this verb in this
  session" is the difference between a usable feature and prompt fatigue — but it is also how a
  gate quietly becomes an allowlist. **Resolved by:** deciding whether the grant is per-action or
  per-(verb, jail, session), and if the latter, what expires it. **→ §10 has a worked answer.**
- **OQ-C. Does the jail see the RESULT or just success?** A PR comment returns a URL, which is
  useful; a credential-bearing response would defeat the "action crosses, credential does not"
  rule. **Resolved by:** a per-verb response schema, server-owned.
- **OQ-D. Is dynamic auth switching a real requirement or a nice-to-have?** Moved with the auth
  content to [`agent-auth-modes.md`](agent-auth-modes.md) §6 / OQ-1; kept here as a pointer
  because the answer decides whether this daemon ever holds auth state (§6).
- **OQ-E. Where does the human answer?** A foreground `yolo approve`, a TUI, a notification, the
  existing `yolo ps`-style view? This is a UX decision that constrains the state design, so it is
  worth answering before step 3 rather than after. **→ §10 answers the security half of this.**

---

## 10. Prior art — unYOLO (re-analyzed FROM SOURCE 2026-08-12)

[unyolo.io](https://unyolo.io/) is an MIT-licensed **access-control framework for coding agents**,
by Onur Solmaz ([@osolmaz](https://github.com/osolmaz)). Its thesis is this doc's §3 rule almost
verbatim: the agent never receives a credential; the broker holds it and executes the operation. It
ships three brokers — **gh-broker** (GitHub), **hf-broker** (Hugging Face), **sudo-broker** (Unix
commands) — plus an approvals UI as a plugin for OpenClaw (a different agent host, not Claude Code).

**This section was rewritten from the code.** The first pass (same date) was written from the
project's website and threat-model page; this one is based on the repository at commit `eaee5fe`
(2026-08-10) — 893 Go files, 440 commits — plus the GitHub API for maturity signals and the HN
thread, which was finally retrieved. **Every §10.1 claim below was checked against a file.** The
headline correction is in §10.6: the earlier "probably an adoption" is wrong.

**Two premises the queue carried were wrong**, and both mattered:

- **It is Go, not Python.** One module, `github.com/osolmaz/unyolo`, `go 1.25.8`. There is no
  language-boundary cost at all — which removes the argument that was doing most of the work
  against adoption, and forces the decision onto better evidence.
- **`gh-broker` is not quite "row B1b's entire scope."** It is B1b *plus* a policy engine, an
  approval queue (B2), a grant store, and an operator inbox. It is closer to B1b+B2 than to B1b.

The name collision with this project is unfortunate and unrelated — different authors, adjacent
problems.

### 10.1 The six claims from the website pass, checked against code

All six are real. Three are stronger than the website suggested; two need correcting.

| Claim (website pass) | Verdict | Where |
|---|---|---|
| Content-addressed immutable execution plans | **confirmed, smaller than it sounds** | `operation/digest/digest.go` is 20 lines — sha256 over canonical bytes. The binding is elsewhere: `internal/storage/state/plans.go:65`, `internal/storage/state/grants.go:149`, re-checked at execution in `brokers/sudo/internal/executorserver/server.go:191` |
| Grants bounded by duration AND use count | **confirmed** | `authorization/policy/types.go:64-71` — `mode`, `default_minutes`, `max_minutes`, `default_max_uses`, `max_uses` |
| …with operator narrowing at approval time | **confirmed, but NARROW-ONLY** | `authorization/grants/v1_decisions.go:269-280`. See correction below |
| `expected_revision` optimistic concurrency | **confirmed** | enforced at `authorization/grants/v1_decisions.go:112`, alongside a required `IdempotencyKey` |
| A deny floor no grant can lift | **confirmed, and it is two mechanisms** | see correction below |
| One-time decision tokens | **confirmed** | only a verifier is stored (`grants.go:110`), rotated on re-request (`grants.go:546`), mismatch → `ErrInvalidDecisionToken` (`v1_decisions.go:152`) |
| Operators on a separate listener, distinct credentials | **confirmed, and stronger** | three listeners, and in production the agent and operator sockets carry **distinct Unix groups** (`gh-broker-agent`, `gh-broker-operator`) as well as distinct secrets |

Also confirmed: the `presentation` projection is a fixed vocabulary, not free text — `Risk` is
`unknown|low|medium|high|critical` with `Title`/`Summary`/`Target`/`Facts`/`Warnings`/`PlanHash`
(`approval/view/presentation.go:68-102`).

**Correction 1 — the operator can only narrow, never widen.** §10 previously said the human could
"widen it to a bounded window they choose." Wrong: `validApprovalConstraints` rejects any constraint
exceeding what was requested (`constraints.Duration > duration` → false), and the request was itself
bounded by policy. The chain is **policy ceiling ≥ request ≥ operator's grant**, monotonically
narrowing. That is a better answer to OQ-B than the one recorded, not a worse one.

**Correction 2 — the real deny floor is a code-owned flag, not a rule.** The quoted *"Deletion is
never delegated, even under an approved grant"* is a `description` string in an **example policy**
(`web/src/content/docs/reference/policy-schema.md:230`) — a convention, not an invariant. The
actual floor is two things, both stronger:

1. **Deny is evaluated before grants** (`authorization/policy/decide.go:23-25`) — a `deny` rule
   structurally overrides every active grant, which is a property of the evaluator, not of the
   policy file.
2. **`OperationSpec.Grantable`** (`authorization/policy/registry.go:51`) — a per-operation boolean
   owned by the broker's **Go code**, not by the policy file. A non-grantable operation cannot be
   made grantable by any policy, and declaring grant settings on one is a **parse-time error**
   (`registry.go:171-188`). This is the cheap, strong safety property, and it is the single best
   idea in the project.

### 10.2 What `gh-broker` actually does — the B1b question, answered

The mechanism is **not** an HTTP proxy, not `http.extraHeader`, not `/etc/hosts`. It is a gitconfig
install with two halves (`git/client/gitclient.go:358-390`):

```ini
[url "http://127.0.0.1:38471/"]
    insteadOf = https://github.com/
    insteadOf = ssh://git@github.com/
    insteadOf = git@github.com:
[credential "http://127.0.0.1:38471"]
    helper =                       ; clears inherited helpers
    helper = unyolo --provider github
```

So the README's *"remotes stay ordinary GitHub URLs and contain no credential"* is literally true —
`remote.origin.url` is untouched — but the **resolved** URL is rewritten at transport time to a
loopback TCP listener. The helper supplies the **broker-client secret**, never a GitHub credential.
Facts that follow from this and matter for yolo:

- **The git listener cannot be a unix socket** — stock git cannot dial one — so it is loopback TCP
  by construction, and the code refuses anything else (`gitclient.go:313-325`).
- **SSH remotes are silently rewritten to loopback HTTP.** `git@github.com:` is in the rewrite set;
  an SSH key is bypassed entirely.
- **Every git invocation by that user is routed through the broker**, including tools that shell out
  to git unaware of unYOLO. That is the containment property — and it means a down broker breaks
  all git for the account. `insteadOf` has no fallback.
- **Injection point:** `brokers/github/internal/githubauth/types.go:81-93` sets
  `Authorization: Basic base64("x-access-token:" + token)` and zeroes the plaintext after use; the
  `Credential` type has no readback API. The inbound client secret is stripped before upstreaming.
- **The credential is a GitHub App installation token**, minted per `(repo, permission-shape)` with
  `repository_ids` of exactly one repo and a minimal permission map — `git.fetch` →
  `contents:read`, any `git.push.*` → `contents:write` (`installation.go:459-474`) — cached and
  refreshed 2 minutes before its ~1h expiry.
- **Classification parses the packfile, not just the ref-update lines.** The broker strips
  `thin-pack` from GitHub's capability advertisement so the pack is self-contained, then walks the
  commit DAG inside it. **`git.push.force` is the default**; fast-forward must be affirmatively
  proven (`server.go:841-856`).

**One nuance worth carrying into any yolo design:** fast-forward is proven against the *client's
claimed* `oldOID` and the pack's internal ancestry — **the broker never asks GitHub for the current
ref value** (`git_pack_classification.go:49-51`). This is safe, because GitHub independently
compare-and-swaps `oldOID` when the pack lands, and a lying client can only make its push look
*more* dangerous. But "fast_forward" is a statement about the pack, not about the remote. Two
consequences the docs do not mention: **SHA-256 repositories can never classify as fast-forward**
(a hardcoded `len(oid) == 40` guard), and a subagent tracing the code could not find the
deny→`[remote rejected]` rendering that the README advertises, though hf-broker has it — so a
denied push may surface as a raw HTTP 403. *Both are unconfirmed with the maintainer; treat as
observations, not established defects.*

**Agent-side cost is genuinely low:** two binaries (`gh-broker`, `git-credential-unyolo`), one
`0600` `client.json`, **no daemon, no port, no service**. The floor for one developer is a
foreground `go run ./cmd/gh-broker` with a scope file and a dev token.

**Production cost is not low:** it wants a **GitHub App** — App id, private key, webhook secret —
and *"inline PAT configuration is rejected"* in production; the fine-grained-token path is
explicitly `--dev-token-fallback`, "for local development only."

### 10.3 Fit against yolo, concretely

**a. yolo already owns the transport half of B1b.** This is the most important fact in the section
and it was not visible from the website. `claude-oauth-broker` is *already* a
credential-injecting TLS-interception proxy: an `intercepts` list (the transport field is
`loopback-tls`, which is a different axis), an in-jail terminator
binding `127.0.0.1:443` in the container netns with a CA-signed leaf, a per-jail host relay that
stamps `jail_id` **host-side** (so attribution is not an in-jail self-report), and a host singleton
that holds the credential. B1b is that pattern aimed at `github.com` instead of
`platform.claude.com` — including the `ca.key`-must-not-cross lesson already learned the hard way
(#33). **The mechanism §5 called "not speculative" is not merely not speculative; it is shipped, in
this repo, and debugged.**

**b. The dependency asymmetry rules out wholesale adoption.** yolo has **2 direct dependencies**
(`gopher-lua`, `x/sys`) and a 7.9 MB `vendor/` under a hermetic offline `-mod=vendor` nix build
whose `goSrc` fileset sees only `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/`,
`bundled_loopholes/`. unYOLO has **16 direct + 57 indirect**: embedded SQLite
(`modernc.org/sqlite` + `modernc.org/libc`, a transpiled C runtime), goose migrations, echo,
Prometheus, the charm TUI stack, go-git, go-github v88. Vendoring that is a category change in
yolo's build, for a feature whose transport we already have.

**c. But the policy engine is separable and stdlib-only — verified, not assumed.**
`go list -deps ./authorization/policy` resolves to **stdlib plus two in-repo packages**
(`authorization/budget`, `internal/copyx`). ~2,100 non-test lines, ~3,850 with tests (a 1,456-line
`policy_test.go`). **Zero third-party closure.** 98 of unYOLO's 191 packages are in that state; the
separation is deliberate and CI-enforced (`scripts/check-architecture.sh`). `authorization/grants`
is the opposite — SQLite is welded in at `authorization/grants/sqlite.go` with no storage interface,
so the *grant store* is not separable even though the *grant model* is.

**d. Versioning is thin, but it IS importable — corrected 2026-08-12.** An earlier draft of this
section said there are "no Go-importable versions". That is wrong, and re-checked against the
GitHub API: the repo has **exactly one `go.mod`, at the root**, with module path
`github.com/osolmaz/unyolo` — so the bare root tags `v0.1.0` / `v0.2.0` are precisely the
go-gettable form, and `go get github.com/osolmaz/unyolo@v0.2.0` resolves. The per-component tags
(`gh-broker/v0.6.0`, `unyolo/v0.8.0`) are release-tooling artifacts for a layout that does not
exist in Go terms, not a barrier.

What survives is weaker and still real: **the importable tags are `v0.x` and lag the component
tags badly**, against a project whose written policy is *"no legacy routes, no old-state readers,
fresh-state coordinated cutover"*. So a pin is possible but buys little stability — the tagged
surface is not where the work is.

**e. License is MIT and clean — with one bundling caveat.** MIT at the root and in each of
`brokers/{github,huggingface,sudo}/`. But `brokers/github/internal/upstream/snapshots/` carries
**~19 MB of vendored GitHub API metadata** under **CC BY 4.0** (`LICENSE.github-docs`) plus MIT
(`LICENSE.rest-api-description.md`). CC BY 4.0 carries a real attribution obligation, and 19 MB is
real weight in a hermetic image. Vendoring `authorization/policy` alone touches none of it and needs
only the MIT notice.

**f. No nix packaging exists** — a `buildGoModule` would be ours to write. Signed release tarballs
(linux/darwin × amd64/arm64) with SBOM and build provenance do exist, so a fetch-the-binary loophole
is the cheaper shape if adoption were chosen.

**g. Running it behind a loophole is architecturally plausible.** Its threat model demands the
client cannot read the broker's files or inspect its process — which the jail boundary already
provides more strongly than the separate-Unix-user setup it ships with. This is the one place the
two projects fit together cleanly, and it is why "ignore" is the wrong verdict.

### 10.4 Maturity — the decisive negative

Engineering quality and project maturity diverge sharply here, and both readings are honest.

**Quality is high, and better than most funded Go projects.** 382 test files / 2,363 tests, a
0.63:1 test:code ratio, an **enforced 85% coverage floor**, `go test -race` on Linux *and* macOS,
`govulncheck`, `gitleaks`, a CI-gated architecture check, SBOM + `attest-build-provenance` on every
release artifact, SHA-pinned actions, ADRs, a written threat model, and reusable conformance suites
for downstream consumers.

**Maturity is low, and this is what decides the question:**

- **Bus factor 1.** 414 of 440 commits are the maintainer's (94%). The only other author
  contributed 26 commits over four days in early July and has not committed since; the identity
  reads as an agent, not a second maintainer. **Zero external PRs; exactly one external issue ever.**
- **5 weeks old** (created 2026-07-08; an earlier draft said 11 — recomputed from the API), with a project rename mid-flight, and 20 stars.
- **An explicit, repeatedly-stated policy of zero backward compatibility.** From its `AGENTS.md`:
  *"Do not add legacy routes, old-state readers, aliases, converters, dual reads, or dual writes.
  This repository uses a fresh-state coordinated cutover."* Breaking changes land as **in-place v1
  replacements that discard existing state**, and migrations are actively forbidden. No CHANGELOG.
- **76 commits (17%) carry an explicit model co-author trailer** across four different models, and
  the maintainer's own `slophammer` gate exists to bound LLM-generated duplication and complexity.
  The gates are good and are doing work human review would normally do — but they are the
  maintainer's own tool, so it is a self-consistent bar rather than an independent one.

Taking a runtime dependency on this for yolo's git path means depending on a five-week-public,
single-maintainer, pre-1.0 project that has promised to break its formats in place. **Vendoring a
stdlib-only leaf package at a pinned SHA carries none of that risk**, which is exactly why the
recommendation splits the two.

### 10.5 The Hacker News thread — retrieved, and nearly empty

The earlier pass recorded a 429. It still 429s on direct fetch; the **Algolia API**
(`hn.algolia.com/api/v1/items/49232548`) returned it. **18 points, 5 comments.** Two are
substantive:

- **`torm115`** raises the sharper critique: *"Credential scoping handles the 'what can the agent do'
  part, but after running a few agents on my own data for a while I think the harder problem is what
  they can see. Prompts turned out to be pretty much useless as a boundary there, so I ended up
  pushing all of it server-side."* The author's reply conflates read-gating with action-gating and
  does not engage the exfiltration framing. **This is the same gap §10.7 names for both projects.**
- **`TZubiri`** juxtaposes *"product about access control"* against
  `curl -fsSL https://unyolo.io/install.sh | sh` with no further comment; a third user filed issue
  #140 over it. The author says the installer is intentional.

**Assessment: essentially no external critique of this design exists.** That is not evidence of
quality in either direction, and the earlier "treat as investigate, not decided" caveat is now
discharged by reading the code rather than by the thread.

### 10.6 RECOMMENDATION — build B1b, vendor the policy engine, do not adopt gh-broker

**Verdict: reimplement the ideas, with one narrow vendoring option. Not adopt, not vendor
wholesale, not ignore.** The four decisive facts, in order of weight:

1. **B1b's transport already exists in this repo** (§10.3a). Adopting `gh-broker` would replace a
   mechanism yolo owns, has shipped, and has already debugged with an external 73-module daemon.
   That is the argument that settles it; everything below is confirmation.
2. **The GitHub App requirement** (§10.2). Today this jail reads `origin` with a fine-grained PAT
   from `.env`. gh-broker's production path wants a registered GitHub App with a private key and a
   webhook secret, and explicitly rejects inline PATs outside development. For a
   single-developer tool that is a large step change in setup cost — and yolo does not need it,
   because a PAT plus a policy engine gives the same per-operation control.
3. **Maturity** (§10.4): bus factor 1, 5 weeks old, importable tags that lag the real work, and a written
   promise to break formats in place.
4. **Build shape** (§10.3b/e): 73 modules vs yolo's 3, no nix packaging, 19 MB of CC BY 4.0
   snapshots if `brokers/github` is bundled.

**Which ideas earn their complexity — take these:**

- **The four-effect policy evaluation, deny-before-grant.** `allow` / `deny` / `request` /
  `no_match` as outcomes of one evaluation, with deny checked first. This collapses §5's three
  *architectures* into one policy *file*, and it is the single highest-value idea here. Already
  recorded in §10.1 of the earlier pass; now verified as ~110 lines of evaluator
  (`authorization/policy/decide.go`).
- **`Grantable` as a code-owned per-operation flag.** The real deny floor. One bool, validated at
  parse time, and it makes "approval can never unlock this verb" *unrepresentable* rather than
  merely unwritten — the same shape as the launchers-ordered-last invariant yolo already likes.
- **A server-owned operation registry** (operation → target kinds → permitted attrs, validated
  before evaluation). This is §6.5's *do not let this become a general RPC* made concrete and
  mechanical instead of aspirational.
- **Both bounds on a grant — duration AND uses — narrowing-only.** The correct answer to **OQ-B**,
  and stronger than the one currently recorded there (§10.1, correction 1).

**Which do NOT earn it yet — defer, with the trigger that would change the answer:**

- **Content-addressed plans.** The digest is 20 lines, but its *value* requires a durable queue, a
  separate executor process, and a re-check at execution — unYOLO has all three; yolo's §7 step 3
  deliberately has none. Ceremony until then. **Trigger:** B2 step 4 (the durable queue).
- **`expected_revision` + idempotency keys.** These exist to stop two operators double-approving.
  A single foreground `yolo approve` cannot race itself. **Trigger:** the second front-end in §7.1
  actually being built — at which point take it rather than re-deriving it.
- **One-time decision tokens.** Their purpose is to make an *out-of-band* channel (Telegram
  callback buttons) unforgeable. yolo's approval path is a unix socket whose posture is already
  "the socket file is the authentication." **Trigger:** approvals ever leaving the socket.
- **The grant store** (SQLite + goose). Not separable from `authorization/grants` in any case, and
  §7 step 3 is synchronous by design.
- **A separate operator listener with distinct credentials.** Still the right answer to §7.1's
  caution — but that caution only bites if the approval UI is HTTP. Keep authority in the unix
  socket and the problem does not arise. **Trigger:** the web UI in §7.1.

**The one vendoring option, if the policy model is wanted verbatim:** `authorization/policy` +
`authorization/budget` + `internal/copyx` are MIT, **stdlib-only**, ~2,100 lines with a 1,456-line
test file, and drop into `vendor/` with **no new module requirements** and no change to the `goSrc`
fileset. Given §10.4's no-compatibility policy, copying at a pinned SHA is strictly safer than a
module dependency, and it is the one piece where copying plausibly beats re-deriving. **This is a
genuine fork in the road and it is the maintainer's call — see the B1b row in
[`outstanding-work.md`](../plans/outstanding-work.md).**

**What survives from the website pass unchanged:** the convergence itself. Two designs reached the
same shape without contact, and that is still the most useful signal in this section.

### 10.7 Where it does not help

- **It does nothing for Claude auth.** unYOLO brokers *third-party service* credentials. There is no
  OAuth-subscription handling, no provider switching, no Bedrock. Row **B3** and
  [`agent-auth-modes.md`](agent-auth-modes.md) are untouched — still a mildly interesting negative
  result, since the nearest prior art declines the hardest part of our version.
- **Its threat model assumes what yolo provides, and vice versa.** Verified at
  `docs/security/THREAT_MODEL.md:124-128`: it does *not* sandbox provider code, validate arbitrary
  shell strings, proxy arbitrary provider APIs, or replace host hardening. yolo *is* the sandbox and
  does none of the credential brokering. Complementary layers — which is why §10.3g holds even
  though the verdict is "build."
- **Its non-protections list names our §6.5 risk.** "Does not validate arbitrary shell strings" is
  the same admission as *do not let this become a general RPC* — and `sudo-broker` is exactly the
  product shaped like that risk. Still worth reading before writing any yolo verb that shells out.
- **The threat model does not address prompt injection** or distinguish a malicious agent from a
  confused one — the same gap `ROADMAP.md` §4e names for yolo. **The HN thread's one real critique
  (§10.5) is precisely this**, and it went unanswered. Neither project has an answer.

### 10.8 What this changes in the plan

- **B1b is a BUILD, not an adoption** — but a *smaller* build than the row implied, because the
  transport is `claude-oauth-broker`'s pattern re-aimed. The row's "possibly an ADOPTION" note is
  retired.
- **B1b now carries one decision:** vendor the stdlib-only policy engine, or re-derive the model?
  §10.6 recommends vendoring it.
- **B2 should take** the four-effect evaluation, `Grantable`, and two-bound narrowing-only grants —
  and should **defer** content-addressed plans, `expected_revision`, and decision tokens until the
  triggers in §10.6 fire.
- **§5's three tiers should still be re-expressed as one policy file with three effects.**
- **OQ-B is answered** — per-action by default, operator may only narrow. Better than the earlier
  reading, which had the human widening.
- **OQ-E's security half stands** (§10.3, §10.6): keep authority in the unix socket, and the
  separate-listener problem never arises.
