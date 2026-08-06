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
security posture, *the socket file is the authentication* — anything running as the same user can
connect. So this protects against **accident and unaudited action**, not against a hostile agent
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
  per-(verb, jail, session), and if the latter, what expires it.
- **OQ-C. Does the jail see the RESULT or just success?** A PR comment returns a URL, which is
  useful; a credential-bearing response would defeat the "action crosses, credential does not"
  rule. **Resolved by:** a per-verb response schema, server-owned.
- **OQ-D. Is dynamic auth switching a real requirement or a nice-to-have?** §5 argues launch-time
  selection covers most of it. **Resolved by:** naming the case where restarting the jail is not
  acceptable.
- **OQ-E. Where does the human answer?** A foreground `yolo approve`, a TUI, a notification, the
  existing `yolo ps`-style view? This is a UX decision that constrains the state design, so it is
  worth answering before step 3 rather than after.
