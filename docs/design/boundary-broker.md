# A stateful broker on the jail boundary — approvals and credential switching

**Status:** DESIGN SKETCH, 2026-08-05. Nothing built. Written in response to two use cases the
maintainer named together, plus a third the research surfaced. Where the answer is "this already
exists," it says so.

**The thesis, from the maintainer:**

> "I want to put some sort of service in between the contained area and the host, just like
> loopholes, but positioned in a place where it can hold tasks that are waiting for a human for
> approval or have other state associated with it… the agent inside can ask to use my GitHub
> credentials to post a comment to a PR, and then it will go to a queue and I can say yes or no…
> and I want to be able to switch out my Claude auth, basically a Claude account switcher between
> OAuth and Bedrock keys, dynamically at the jail layer."

**The short version.** The mechanism is 90% built — it is the loophole protocol plus what the
OAuth broker already does. What is missing is not a transport or a daemon framework; it is **two
things a loophole cannot express today: a request that OUTLIVES its connection, and a request
whose answer comes from a HUMAN rather than from the host filesystem.** Those two absences are
the whole design, and they are smaller than they sound.

The credential-switching case turns out to be a *different* feature that happens to want the same
front door — worth building, worth not conflating. See §5.

**Reads with:** [`loophole-protocol.md`](loophole-protocol.md) (the wire format this extends),
[`agent-credentials.md`](agent-credentials.md) (what crosses the boundary today and why),
[`../guides/loopholes.md`](../guides/loopholes.md) (the three shipped loopholes),
[`../plans/outstanding-work.md`](../plans/outstanding-work.md) (where this lands in priority).

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

## 5. Use case C: Claude auth switching — a DIFFERENT feature, same front door

**The ask:** switch between OAuth and Bedrock keys dynamically at the jail layer, like an account
switcher.

**This is not an approval problem and should not be built as one.** It is a *credential selection*
problem, and conflating them would give both a worse design. Approvals are per-action, ephemeral,
and human-answered; auth selection is per-jail (or per-session), persistent, and configuration.

**What it actually needs.** Today (`packs/claude/pack.json`) the credential arrives as a
`shared_credentials` hook symlinking `~/.claude/.credentials.json` into a machine-wide shared dir
— one credential, one shape, decided before the jail boots. And the Bedrock path is not modeled in
yolo at all: `rg -n 'bedrock' internal/` finds **nothing**. On this machine it works because
`AWS_*` + `CLAUDE_CODE_USE_BEDROCK` come through `env_sources`, which is invisible to yolo's
config model.

So the honest statement of the gap: **yolo has one credential channel per agent, and no notion
that an agent might have several mutually exclusive auth modes.** That is the thing to model.

**Where the broker helps, and where it does not.** It helps if switching must be *dynamic* — a
running jail changing modes without a restart — because then something host-side must hold "which
mode is jail X on" and hand out the right material on request. That is a state-holding boundary
service, and it is the same daemon.

But **ask whether dynamic is the requirement**, because the cheaper answer may be enough: a config
key (`claude_auth: oauth | bedrock`) plus the existing per-jail credential plumbing gets you an
account switcher at *launch* time, with no new daemon, no new protocol, and no new state. That
covers "I want this jail on Bedrock and that one on OAuth" — which may be the actual need. It does
not cover "switch this running jail," which is the only part that needs the broker.

**My recommendation: split it.** Model the auth modes first (config + credential channel), which
is useful alone and is a prerequisite either way; add broker-mediated *dynamic* switching only if
launch-time selection proves insufficient in practice. Building the daemon first would be designing
for a requirement not yet demonstrated.

**One real constraint to carry either way:** the OAuth broker exists because Anthropic's refresh
token is **single-use**, so concurrent refreshes across jails burn each other's token. Any auth
switcher must not reintroduce that race — e.g. two jails switching to OAuth simultaneously, or a
switch racing a refresh. The flock precedent (`RefreshLockPath`) is the mechanism, and this is the
argument for auth state living in the *same* daemon as the refresh serialization rather than beside
it.

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
2. **One allowlisted verb, synchronous approval.** `github.pr_comment`, human answers at a
   foreground `yolo approve` while the jail blocks with a timeout. Proves the preview-and-validate
   design without any durable queue. Fail-closed on timeout.
3. **The durable queue.** Only once (2) shows the synchronous version is genuinely too limiting —
   which it may not be, if the human is usually present.
4. **Auth modes as configuration** (§5), independent of 1–3 and useful on its own.
5. **Broker-mediated dynamic switching**, only if launch-time selection proves insufficient.

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
- **§5's auth-mode modeling is separable and arguably higher value than the broker**, because the
  Bedrock path being entirely unmodeled in yolo is a real gap today — it works on this machine only
  through `env_sources`, which yolo cannot see or reason about. That is worth its own row in the
  queue independent of any of this.

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
