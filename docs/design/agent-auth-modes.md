# Auth modes — one agent, several mutually exclusive ways to pay for it

**Status:** DESIGN, 2026-08-05. Nothing built. This is queue row **B3**, split out of
[`boundary-broker.md`](boundary-broker.md) because it shares that doc's front door and none of
its blockers: the broker waits on N3/OQ-1, this waits on nothing.

**The ask, from the maintainer:** a Claude **Teams subscription** is the account to prioritize
(it is bought, it carries the discount, it is already set up), with **Bedrock as overflow** —
"ideally dynamically" — working on the host, in a jail, and on a Mac.

**Reads with:** [`agent-credentials.md`](agent-credentials.md) (what crosses the boundary
today — and see §2, which corrects its §3), [`pack-system.md`](pack-system.md) (the `autonomy`
kind, which is the structural precedent), [`../plans/outstanding-work.md`](../plans/outstanding-work.md)
(row B3).

---

## 1. The shape of the gap, in one sentence

**yolo models one credential channel per agent, and has no notion that an agent might have
several mutually exclusive auth modes** — where a mode is not just a credential but a
credential *plus* the environment and the model names that only make sense with it.

That last clause is the part that is easy to miss and expensive to get wrong. See §4.

---

## 2. Measured state — including a manual switch performed mid-design

This section was measured twice, an hour apart, because the maintainer switched the machine from
Bedrock to Teams while the doc was being written. **The switch is the best evidence in this
document**, so both states are kept.

### 2.1 Before — Bedrock only

| Fact | Evidence |
|---|---|
| The subscription did not reach jails at all: `~/.claude-shared-credentials/.credentials.json` was **0 bytes** | `wc -c` in a live jail; the symlink was correctly wired and pointed at an empty file |
| **Bedrock served everything**, via `env_sources` | `~/.config/yolo-user-env.sh` exported `CLAUDE_CODE_USE_BEDROCK`, `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` |
| Model pinned to a **Bedrock-shaped ID** | `model: "us.anthropic.claude-opus-5[1m]"` in host `settings.json`, composed through to the jail |
| The `env` block of host `settings.json` was **empty** | `jq '.env'` → `{}` |

The empty credentials file was **expected state, not a defect** — never logged in to Teams *on
this machine*, though logged in on others. Credential state is **machine-scoped**
(`.claude-shared-credentials` is a machine-tier `state` contribution), so a login elsewhere does
not and should not arrive here. Worth recording because an empty file reads exactly like one of
the broker's three documented logout paths, and the next person to find one will assume a bug.

### 2.2 After — Teams, and the credential half works

The maintainer logged in and edited the jail config. Re-measured:

| Fact | Evidence |
|---|---|
| **The subscription path works end to end** | creds file is 555 bytes; `subscriptionType: "team"`, `rateLimitTier: "default_claude_max_5x"`, five `user:*` scopes, access token present |
| The `shared_credentials` hook did its job | `~/.claude/.credentials.json` → `../.claude-shared-credentials/.credentials.json`, intact, populated |
| Bedrock env is **gone** | `yolo-user-env.sh` now exports only `TAVILY_API_KEY` |

That retires §9's step 1: the symlink-plus-broker plumbing is **verified against a real
`/login`**, not assumed. Notably the metadata trio (`scopes` / `subscriptionType` /
`rateLimitTier`) is present and preserved — the property §3 depends on.

### 2.3 The residue — §4 predicted this exact miss

**The model pin is still `us.anthropic.claude-opus-5[1m]`** — the Bedrock-shaped, region-prefixed
ID — in **both** the host `settings.json` and the composed jail copy, after a switch to
first-party Teams.

The switch was made in two places (`/login`, and the `env_sources` Bedrock block) and **missed a
third**. That is precisely the failure §4 argues the design must prevent: the credential channel
and the env moved together, the model names did not, because nothing ties them into one unit.
A hand-performed switch missed the component a hand-performed switch is most likely to miss.

This is the strongest available argument that **a mode is a bundle**, and it arrived unprompted.

> **Live item for the maintainer, not a design point:** that pin should become the bare
> first-party form. Left unchanged here because host `settings.json` is the user's own file.
> It is *not* obviously breaking today — the running session reports itself as bare
> `claude-opus-5[1m]`, so something is normalizing or overriding the pin — which makes it the
> worse kind of residue: inert until it isn't, and invisible when it bites.

> **`agent-credentials.md` §3 documents a mechanism that is not the one in use.** It says the
> Bedrock IAM keys live "under the `"env"` block of `~/.claude/settings.json` on the host",
> riding the `/ctx/host-claude/` mount. That block is `{}` and always was. The keys arrived via
> `env_sources`. The §3 narrative is otherwise accurate about *scoping* and blast radius — it is
> the delivery path that is stale. Fix that passage when implementing this.

---

## 3. What `apiKeyHelper` cannot do — closing an open question

An earlier sketch in this session proposed routing both accounts through Claude Code's
`apiKeyHelper` seam, so switching would be "the helper returns a different credential." **That
does not work, and the reason generalizes.**

`apiKeyHelper` returns an **API key**. An API key bills as pay-per-token API usage. A Teams
subscription is **not** a key — it is OAuth whose credential file carries
`subscriptionType` / `rateLimitTier`, and that metadata *is* the entitlement. (The repo already
depends on this: `oauthMetadataKeys` in `internal/entrypoint/claude.go` preserves those fields
deliberately, because Claude ≥ 2.1.200 treats a creds file carrying only the token trio as *not
logged in*.) Putting a subscription through the key slot would mean paying for the seat and
billing tokens separately.

**This answers a live open question.** `ROADMAP.md` §4e asks: *"Keep the TLS-MITM architecture,
or revisit `apiKeyHelper`?"* — noting a working `apiKeyHelper` broker exists on an abandoned
fork (−4406/+129 lines) with no recorded rejection rationale in `main`. The rationale is now
recorded: **`apiKeyHelper` cannot carry an OAuth subscription, so it is not a substitute for the
MITM path for the subscription mode.** It remains viable for a key-shaped credential. That
narrows the question from "which architecture" to "does any mode want a key at all", which is a
much smaller question.

---

## 4. A mode is a BUNDLE, and the model ID is why

The single most important design point, and the one that will produce a baffling bug if missed.

Flipping `CLAUDE_CODE_USE_BEDROCK` alone is **not** a mode switch. The model IDs differ between
the two paths:

| | credential | env | model ID |
|---|---|---|---|
| `subscription` | OAuth via `/login`, shared-creds symlink, broker-serialized refresh | no `AWS_*`, no `CLAUDE_CODE_USE_BEDROCK` | **bare** — `claude-opus-5` |
| `bedrock` | long-term IAM keys (`matt-bedrock`, invoke-only) | `CLAUDE_CODE_USE_BEDROCK=1`, `AWS_REGION`, `AWS_*` | **region-prefixed** — `us.anthropic.claude-opus-5[1m]` |

Today's pinned `model` is the Bedrock form. Switch the credential channel without switching the
model names and Claude Code issues a **first-party request for a model ID that does not exist
first-party** — which presents as a 404 on an unknown model, not as an auth error. Nothing in
the failure names the account.

So the unit that switches is `{credential channel, env vars, model IDs}`, atomically. Also in
the bundle: `ANTHROPIC_DEFAULT_OPUS_MODEL` (currently Bedrock-shaped) and any other
`ANTHROPIC_*_MODEL` pin.

**There is already a kind for this.** The `autonomy` contribution is structurally the same
thing: a pack declares **two complete postures** (`autonomous`, `guarded`), each folding config
keys into the pack's own surfaces and merging launch flags, and a **policy bit derived from the
notch** selects which one renders. Auth modes want exactly that — declare both bundles, select
one — differing only in what selects them (a config key, not the confinement profile). Building
this as a new mechanism when `autonomy` already demonstrates "notch-gated patch of the managed
layer, two named variants, either may be empty" would be inventing a second dialect of one
idea.

---

## 5. The recommendation: modes as configuration, `subscription` first

The cheapest thing that satisfies the stated need, in full:

- **A key — `claude_auth: subscription | bedrock`** — workspace-scoped, like `confinement`.
  **`subscription` is the default**, because it is the paid primary. (Note this *changes* today's
  effective behavior on this machine, where Bedrock is the only wired path — so the first apply
  after this lands must be expected to switch accounts, and that should be said out loud at
  launch rather than discovered.)
- **Two declared bundles in the pack**, `autonomy`-shaped: each names its config patch (model
  pins), its env vars, and its credential channel. Either may be partially empty.
- **The existing plumbing does the work.** `subscription` → the `shared_credentials` hook and
  broker, already built. `bedrock` → `env_sources`, already how it works today.

No daemon, no protocol, no queue, no new state. And it is **notch-portable for free**, which is
the "host, jail, and Mac" requirement: both halves already work at every notch that works at all
(with the macos-user caveats in §7).

**This covers "this jail on Bedrock, that one on the subscription."** It does not cover
switching a *running* jail — see §6.

---

## 6. Dynamic overflow: what is reachable and what is not

The ask says "ideally dynamically." Stating the limit precisely, because it is a hard one:

**yolo cannot see the rate limit.** The 429 and its `retry-after` are seen by **Claude Code**,
inside the jail. Nothing on the boundary observes rate-limit state, and nothing in the
credential path is called on failure — so "subscription until exhausted, then Bedrock" is not
reachable by configuration alone. It needs a signal that does not exist.

Compounding it: **rate limits are per-model-bucket.** Opus 5 draws from a pool separate from
Opus 4.x. A single global "the subscription is exhausted" flag would therefore be wrong more
often than right, and a switch driven by it would move traffic off a subscription that still has
headroom on the model actually in use.

| Want | Reachable? |
|---|---|
| Different modes per jail / per workspace | **Yes** — §5, config only |
| Manual `yolo auth use bedrock` on a running jail | Needs host-side "which mode is jail X on" state — the broker (§8) |
| Automatic failover on exhaustion | **No** — needs a signal from inside the jail; see below |

If automatic failover is genuinely wanted, the honest options are all somewhat ugly: the agent
reports its own 429 (in-jail self-report, and the relay's `jail_id` injection exists precisely
because self-reports are not trustworthy); a host-side prober burns quota to measure it; or a
shared counter approximates it and drifts. **Recommendation: do not build this until a manual
switch has proven insufficient.** Per-jail selection plus a manual switch is most of the value
and none of the guessing.

---

## 7. Traps that will make a correct switcher look broken

**7.1 Never export a blank `ANTHROPIC_API_KEY`.** An **empty** `ANTHROPIC_API_KEY=""` still wins
its precedence slot in the credential chain and authenticates with an empty key. A mode switch
that "clears" the other mode by setting variables empty fails exactly this way, and the error
will point at the API, not at yolo. Unset, never blank. The same applies to
`ANTHROPIC_AUTH_TOKEN` — and if both it and `ANTHROPIC_API_KEY` are set, the SDKs send both
headers and the API rejects the request outright.

**7.2 Refresh tokens hard-expire; they do not slide with use.** A subscription left idle while
Bedrock serves everything will eventually fail to refresh — and the failure surfaces at the
moment you switch to it, which is the worst time to debug it. A mode switch should verify
liveness as part of selection, not leave it to be discovered. This is also why §2's empty
credentials file is worth re-checking after the first real `/login`: an empty file and an expired
token look similar from the outside and have different fixes.

**7.3 Claude Code warns about conflicting credentials.** An `ant` profile and Claude Code's own
`/login` credential can conflict, and Claude Code says so. Pick one story for the subscription
mode — the shared-creds file is the one already built — and do not have a mode that half-uses
both.

**7.4 `env_sources` secrets are already exposed in two ways this makes worse.** They land
cleartext at 0644 in five agent config files and a prism `last_render` sidecar; on macos-user
they are placed **on the process argv** (`env -i K=V…`), visible in `ps` to every user on the
Mac. A design that leans harder on `env_sources` for the Bedrock bundle raises the stakes on
both. Neither is introduced here, and neither should be silently inherited — `ROADMAP.md` §4e's
"should env_sources secrets be redactable?" is upstream of this row.

**7.5 macos-user's credential symlink dangles.** A confirmed defect: `ensureCredentialsSymlink`
runs in `RunDarwinBootstrap`, but the target dir is provisioned only by
`storage.EnsureGlobalStorage` plus the container bind mount, neither of which runs there. So the
**subscription mode cannot work on macos-user until that is fixed** — which matters because
"working on a Mac" is part of the ask. Bedrock's `env_sources` path also fails there per §2's
mechanism correction (the `/ctx/host-claude` mount does not exist on macos-user). Both modes are
therefore Mac-gated for verification even though the config work is not.

---

### 7.6 The nearest prior art declines this problem

[unyolo.io](https://unyolo.io/) — analyzed in [`boundary-broker.md`](boundary-broker.md) §10 — is
an MIT-licensed agent credential broker that independently converged on that doc's whole design.
It brokers **third-party service credentials** (GitHub, Hugging Face, Google Workspace, sudo) and
has **no OAuth-subscription handling, no provider switching, and no Bedrock**.

That is a mildly useful negative result rather than an omission on their part: the hard thing here
is not "hold a credential on the agent's behalf", which is well-trodden, but that **an LLM provider
credential selects a whole backend** — endpoint, wire dialect, feature set, and model namespace —
in a way a GitHub token does not. Brokering a GitHub token changes who is authorized; switching to
Bedrock changes what the API *is*. §4's bundle exists because of that difference, and it is why an
off-the-shelf credential broker does not shorten this row.

---

## 8. What the broker would add, and why it is not required

[`boundary-broker.md`](boundary-broker.md) §5 argued auth switching is a different feature that
wants the same front door. That still holds, and §5 above is the "different feature" half.

The broker earns its place only for **dynamic** switching: something host-side must hold "which
mode is jail X on" and hand out the right material on request. If that gets built, the auth
state belongs in the **same** daemon as the refresh serialization rather than beside it — because
Anthropic's refresh token is single-use, and two jails switching to `subscription` concurrently,
or a switch racing a refresh, is the same token-burning race the broker already exists to
prevent (`RefreshLockPath`). Putting auth state in a second daemon would create a racer the
existing flock cannot reach — which is precisely the failure mode `ROADMAP.md` documents from
the host-Claude case, where a native refresher never took the lock.

---

## 9. Order of work

1. ~~**A real subscription `/login` in a jail**, verified end to end.~~ **DONE 2026-08-05**, by
   the maintainer's own switch — see §2.2. The credential half of `subscription` is proven.
2. **Fix the residual Bedrock model pin** (§2.3) — a one-line user-side change, and the concrete
   instance of the bug §4 describes.
3. **Correct `agent-credentials.md` §3** to describe `env_sources`, not the `env` block.
4. **`claude_auth` as config** (§5) — the two bundles, `subscription` default, model IDs carried
   in the bundle. Now the first real build step, and better specified than it was an hour ago:
   the manual switch enumerated exactly what a bundle must contain.
5. **Manual switch on a running jail**, only if launch-time selection proves insufficient.
6. **Automatic failover** — only with a named, trustworthy signal. Not before.

---

## 10. Open questions

- **OQ-1. Is per-jail selection enough?** If the subscription is primary and Bedrock is a
  deliberate fallback the human chooses, §5 is the whole feature and §6 never happens.
  **Resolved by:** using §5 for a while.
- **OQ-2. Does the Bedrock bundle stay in `env_sources`, or become a declared bundle?**
  `env_sources` works today and is invisible to yolo's config model — which is the gap. Moving
  it into a declared bundle is what makes `describe`/`check` able to report the active mode.
- **OQ-3. What happens on a mode switch mid-session?** Claude Code reads credentials at startup;
  a switch that requires a restart is honest, a switch that half-applies is not.
- **OQ-4. Should `check` verify the selected mode's credential is live?** §7.2 argues yes. The
  cost is a network call in a command that is currently offline-safe.
