# Auth modes — one agent, several mutually exclusive ways to pay for it

**Status:** DESIGN, 2026-08-05. Nothing built. This is queue row **B3**, split out of
[`boundary-broker.md`](boundary-broker.md) because it shares that doc's front door and none of
its blockers: the broker waits on N3/OQ-1, this waits on nothing.

**The ask, from the maintainer:** a Claude **Teams subscription** is the account to prioritize
(it is bought, it carries the discount, it is already set up), with **Bedrock as overflow** —
"ideally dynamically" — working on the host, in a jail, and on a Mac.

**Reads with:** [`agent-credentials.md`](agent-credentials.md) (what crosses the boundary
today — and see §2, which corrects its §3), [`pack-system.md`](pack-system.md) (the `autonomy`
kind, which is the structural precedent), [`../plans/roadmap.md`](../plans/roadmap.md)
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

> **~~`agent-credentials.md` §3 documents a mechanism that is not the one in use.~~ FIXED — the
> passage now carries the correction (verified 2026-08-23, `agent-credentials.md:319-329`).** It
> used to say the Bedrock IAM keys live "under the `"env"` block of `~/.claude/settings.json` on the
> host", riding the `/ctx/host-claude/` mount. That block is `{}` and always was; the keys arrived
> via `env_sources`. **Kept here because the correction it points at is load-bearing in the other
> direction:** the `settings.json` `env` block is still the right *target* design (§11.2 has the
> Bedrock pack deliver exactly that), so a reader who sees the fix must not conclude the block is
> the wrong home — only that it was never the home yet.

---

## 3. What `apiKeyHelper` cannot do — closing an open question

An earlier sketch in this session proposed routing both accounts through Claude Code's
`apiKeyHelper` seam, so switching would be "the helper returns a different credential." **That
does not work, and the reason generalizes.**

`apiKeyHelper` returns an **API key**. An API key bills as pay-per-token API usage. A Teams
subscription is **not** a key — it is OAuth whose credential file carries
`subscriptionType` / `rateLimitTier`, and that metadata *is* the entitlement. (The repo already
depends on this, because Claude ≥ 2.1.200 treats a creds file carrying only the token trio as
*not logged in*. It used to depend on it twice — `oauthMetadataKeys` in the entrypoint's
`shared_credentials` harvest **and** `NormalizeOAuth` in the broker. The harvest was deleted
with the rest of the claude-shaped hook body, [`pack-code-separation.md`](pack-code-separation.md)
§5, so the broker's copy-previous-then-override is now the **only** guard — pinned by
`TestNormalizeOAuthPreservesEntitlementMetadata` and
`TestRefreshPreservesEntitlementMetadataOnDisk` in `internal/oauthbroker`, which exist precisely
because deleting one of two guards is how a property quietly becomes unheld.) Putting a
subscription through the key slot would mean paying for the seat and billing tokens separately.

**This answers a live open question.** `sequencing-2026-07.md` §4e asks: *"Keep the TLS-MITM architecture,
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
both. Neither is introduced here, and neither should be silently inherited — `sequencing-2026-07.md` §4e's
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
existing flock cannot reach — which is precisely the failure mode `sequencing-2026-07.md` documents from
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

**OQ-1 is the only one with reach**, and it is the only one in this doc that resolves by
**experiment rather than ruling** — see its `Resolved by`. It gates
[`boundary-broker.md`](boundary-broker.md) B2 as well as §6 here.

1. 💬 **OQ-1 — is per-jail selection enough?** If the subscription is primary and Bedrock is a
   deliberate fallback the human chooses, §5 is the whole feature and §6 never happens.

   _Leaning:_ **Yes, per-jail is enough for v1.** §6's dynamic overflow needs the daemon to hold
   auth state, which is the expensive half of B2; §5 needs none of it.

   **Resolved by:** an experiment, not a ruling — ~5 minutes. Point Claude Code at a non-Anthropic
   base URL with a subscription OAuth token in place and observe whether it sends the bearer. If it
   does, §6 is reachable without the broker; if it does not, §6 needs B2 and moves behind it.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-2 — does the Bedrock bundle stay in `env_sources`, or become a declared bundle?**
   `env_sources` works today and is invisible to yolo's config model — which is the gap. Moving
   it into a declared bundle is what makes `describe`/`check` able to report the active mode.

   _Leaning:_ **Declared bundle**, because "which mode am I in" is unanswerable while the answer
   lives in a key yolo does not model. Pairs with OQ-9, which asks where the *secret* half lands.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-3 — what happens on a mode switch mid-session?** Claude Code reads credentials at
   startup; a switch that requires a restart is honest, a switch that half-applies is not.

   _Leaning:_ **Require a restart and say so.** A half-applied switch is §7's whole trap list.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-4 — should `check` verify the selected mode's credential is live?** §7.2 argues yes. The
   cost is a network call in a command that is currently offline-safe.

   _Leaning:_ **Yes, behind a flag** — the offline-safe property of `check` is worth keeping as the
   default, and a dead credential is exactly what the user wants `check` for when they ask.

   **Answer:**
   > _(empty — fill in when decided)_

**§11 and §12 add OQ-5 through OQ-9.** The live ones are in §12.4; the settled ones are in §12.5.

---

## 11. Packaging — two packs, and what pack composition can and cannot express

**The maintainer's shape:** a *shareable* Bedrock auth pack, plus a *personal* pack that adds
Tavily MCP whenever Bedrock is the active mode, "swapped manually for now" — and **all of it has
to work on the host as well as in a jail.**

All findings below verified against the code 2026-08-12.

### 11.1 Packs cannot depend on, or include, other packs 🔴

**There is no dependency mechanism.** The nearest thing, the `requires` kind, asserts that a
**binary** must exist on `PATH` and carries install hints (`RequiredBins`, `DepRequirements`) —
it takes a `bin`, not a pack name. Nothing in `packdecl` expresses pack→pack.

So the composition available today is **the `packs` list itself**, which is flat, ordered, and
user-scope:

```jsonc
"packs": ["claude", "claude-bedrock", "matt-bedrock-extras"]
```

That works, and for manual swapping it is arguably the *right* amount of machinery: the list is
the mode selector, and reading it tells you the whole story. What it cannot express is the
dependency — nothing stops `matt-bedrock-extras` being selected without `claude-bedrock`, in which
case the personal pack contributes Tavily to a jail that is on Teams. That is not harmful here
(an MCP server is additive), but the general shape — *a pack that is only meaningful alongside
another* — has no way to say so.

**A `requires_pack` contribution would be the minimal fix**, and it composes with the `conflicts`
idea A2 needs: one names packs that must be present, the other names packs that must not be.
Both are load-time checks over a list yolo already has, with no runtime cost. **OQ-5** below.

> **⛔ SUPERSEDED — `requires_pack` was RETIRED, and this paragraph's "minimal fix" is no longer
> the plan.** See [`retired-decisions.md`](retired-decisions.md) Thread A, *"Also retired:
> `requires_pack` / pack→pack composition"*: its motivating case was two auth packs excluding each
> other, and Thread A's ruling left **one** pack (`claude-bedrock`), so there is nothing to
> exclude. A personal pack selected without it is additive and harmless — the exact case described
> two paragraphs above — because an MCP server whose key is absent is already inert via
> `requires_env`. **Build it when something breaks without it.** The finding above (there is no
> pack→pack mechanism) is still true and still worth keeping; only the prescription is withdrawn.
> Tracked as answered in **OQ-5**, §12.5 (Decision Ledger).

### 11.2 The host-notch mechanism: use `config-overlay`, NOT the `env` kind

This corrects Thread A's earlier claim that `env` is "refused at the host notch." The truth is
more precise and much more useful.

`HostFields()` **includes** `KindEnv` — the host target *can* express it. What refuses it is
`hostUnimplemented`, and the recorded reason is scoped deliberately:

> *"env vars apply to a process yolo starts, and `apply --host` only configures your tools — it
> never runs them… A notch where yolo does the launching can honor them."*

The code comment goes out of its way to say this is **a limit of the `apply --host` COMMAND, not
of the notch** — precisely so a future `guest` target does not inherit a refusal it could honor.

**So the design answer is to not need the verb at all.** Claude Code reads an `env` block out of
`settings.json`. A Bedrock pack can therefore put `CLAUDE_CODE_USE_BEDROCK` and `AWS_REGION` into
`claude/settings` via **`config-overlay`** — a kind that renders at **both** notches — instead of
the `env` kind, which is jail-only until yolo owns the launch.

| | `env` kind | `config-overlay` → `settings.json` `env` block |
|---|---|---|
| in a jail | works | works |
| `apply --host` | unbuilt (no argv to inject into) | **works** |
| carries secrets | forbidden by the kind's contract | also must not — same discipline |

This is worth noticing: `agent-credentials.md` §3 *describes* Bedrock keys arriving through the
`settings.json` `env` block. That is stale as a description of the current machine (§2 measured the
block as `{}`), but it is **the correct target design** — the doc described the right mechanism
before anything used it.

**It also removes Thread A's dependency on N3.** Auth-as-packs becomes host-complete now, rather
than after `yolo --at host -- <cmd>` lands.

### 11.3 `hook` IS refused at the host — and that is correct

Unlike `env`, the `hook` kind is refused as a matter of policy, not left unbuilt:

> *"hooks are jail provisioning steps (credential symlinks, per-jail history, plugin
> reconciliation) — `apply --host` does not run them against your real home."*

That means a Teams pack's `shared_credentials` hook **will never fire at the host notch.** This is
right rather than limiting: the hook exists because a jail has a disposable home and *several*
jails share one credential file, so it symlinks into a machine-global dir. A host has one real home
and one credential file already in the right place. **On the host the hook is meaningless**, which
is exactly what the refusal says.

Consequence for the pack split: **the Teams pack is nearly a no-op at the host** — its host-side
contribution is the bare model IDs, and the credential arrives because you ran `/login`. That is a
feature. It also means "does auth work outside a jail?" has different answers per mode, and both
are fine:

| mode | in a jail | on the host |
|---|---|---|
| Teams | hook + broker + machine-shared creds | `/login` writes `~/.claude/.credentials.json` directly; pack contributes model IDs only |
| Bedrock | `config-overlay` env block + `env_sources` keys | `config-overlay` env block; keys from the user's own shell or `settings.json` |

### 11.4 The personal-pack case (Tavily) is already expressible

MCP servers are config, not a dedicated kind: they live under `mcpServers` in `~/.claude.json`
(the `claude/config` surface), and the delivery path already supports `requires_env` gating so a
server that needs `TAVILY_API_KEY` stays inert without it. So `matt-bedrock-extras` is a
`config-overlay` on `claude/config` adding one `mcpServers` entry, plus the key via `env_sources`.

No new mechanism. The only thing missing is §11.1's "and I only want this when Bedrock is active",
which today is expressed by *selecting both packs together*.

### 11.5 A shipped auth pack breaks the "six shipped packs" tests

Several tests hardcode the official set — `packload_test.go` ("The six official packs must be
EMBEDDED"), `packconfigexclusive_test.go` (`["claude","copilot","opencode","pi","codex","agy"]`),
plus briefing/skills source tests that assert "all six". Adding shipped auth packs means updating
those, which is mechanical but should be a deliberate commit rather than a surprise in a larger
one.

**This is also an argument for shipping the auth packs as a separate fetched/public repo rather
than embedding them** — which matches "shareable" better anyway, and exercises the fetched-pack
approval path that already exists. **OQ-6.**

---

## 12. Relationship to PR #32 (macOS broker transport)

> **⛔ OVERTAKEN BY EVENTS, 2026-08-13.** This section was written while #32 was open and
> mergeable. It no longer is: the maintainer ruled that yolo **builds the unified transport
> instead of merging #32**, and that `loopback-tls` becomes the loophole framework's only
> transport (`unix-socket` retired). See
> [`loophole-transport.md`](loophole-transport.md) §7.3 (OQ-T8) and §7.4 (OQ-T9); §7.1 of that doc
> states **"All of §7 is now settled."** The analysis below is kept as the record of why the
> generalization was the right call — §12.2's list of what #32 got right is the spec
> `loophole-transport.md` §7.3 re-derives against. Only its framing of the choice as still open is
> stale. **OQ-8 is answered accordingly — §12.5 (Decision Ledger).**

[PR #32](https://github.com/mschulkind-oss/yolo-jail/pull/32) — *"oauth broker: loopback TCP
transport for macOS+podman"*, +1064/−13, open — fixes
[#31](https://github.com/mschulkind-oss/yolo-jail/issues/31): on macOS+podman the in-jail
`oauth-terminator` cannot `connect()` the relay's unix socket, because virtiofs shares the inode
but not the socket endpoint across the podman-machine VM boundary. Every `platform.claude.com`
request 502s and Claude Code will not start. Its fix is a loopback **TLS** front on the relay with
an **ephemeral host-only-key cert** (pinned by the terminator, never mounted into a jail) plus a
**per-jail bearer token** sent inside TLS.

### 12.1 It is not subsumed by auth modes — but Bedrock routes around it

The broker exists only for the **OAuth refresh** race (Anthropic's refresh token is single-use).
**Bedrock mode has no OAuth, no refresh, and therefore no broker** — so on macOS a Bedrock-mode
jail never touches the path #31 breaks.

That lowers #32's *urgency* without replacing it: it makes "Claude Code will not start on macOS"
conditional on the mode rather than absolute. **Teams mode on macOS+podman still needs #32 or
something like it.** Anyone reading "subsume" as "we can close it" would be wrong.

### 12.2 The better move is to PROMOTE it, not replace it

Two reasons it is worth more than its own PR description claims:

1. **Its transport is the one every host service needs on macOS.** The virtiofs-socket problem is
   not specific to the OAuth broker — it is a property of the boundary. `macos.md:575` already
   documents `host.containers.internal` as the workaround for exactly this. Any future host-side
   service — the audit log (B1), the git proxy (B1b), an approval queue (B2) — hits the same wall
   on macOS. #32 solves it once, inside `brokerrelay`. **Generalizing it into the loophole
   framework would make every host service macOS-capable in one move.**
2. **Its per-jail bearer token is the client-auth upgrade the boundary work independently
   arrived at.** [`boundary-broker.md`](boundary-broker.md) §10.3 recommends adopting unYOLO's
   *named broker-client secret*, because yolo's current posture is "the socket file is the
   authentication — a daemon trusts whoever can `connect()`." #32 implements precisely that
   upgrade, for one service, on one platform. Third independent convergence on the same idea.

So the relationship is the inverse of subsumption: **#32 is a down payment on the boundary-service
architecture, currently scoped to one consumer.** The work that "replaces" it is generalizing it.

### 12.3 What would genuinely replace it

**macos-user** — no container, no VM boundary, so no virtiofs problem at all. But per Thread B it
renders zero pack surfaces, has a dangling credentials symlink, and has the broker **unwired**
(`EndpointGrantCommands`, zero call sites). So "use macos-user instead" is not available today
and is a larger project than merging #32.

**Also worth carrying:** #32's own "why not reuse the broker CA" section documents that the broker
CA's **private key is mounted `:ro` into every jail**, so a malicious jail could sign a relay cert
and MITM a sibling. It works around that rather than fixing it — and it is the same confirmed
defect `sequencing-2026-07.md` §4d records. Merging #32 does not fix it and should not be read as having done
so.

### 12.4 Open questions from §11–§12

**OQ-6 is the one with reach** — it gates building `claude-bedrock` at all.

5. 💬 **OQ-6 — shipped, or a separate public pack repo?** §11.5 — shipping breaks the "six packs"
   tests and embeds a personal auth choice in the binary; a fetched repo matches "shareable" and
   exercises the approval path.

   _Leaning:_ **Fetched.** It is the only option that exercises the approval path this pack's whole
   point depends on, and a personal auth choice does not belong in everyone's binary.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-7 — does the auth pack own the model IDs, or does the base `claude` pack?**

   > [!NOTE]
   > **Restated 2026-08-23.** This question was written as *"does the **Teams** pack own the model
   > IDs"* and that framing is dead: Thread A collapsed the two-auth-pack shape into ONE
   > `claude-bedrock` pack ([`retired-decisions.md`](retired-decisions.md)), so there is no Teams
   > pack to own anything. The question survives the collapse unchanged in substance — it is about
   > where a pin lives, not about how many packs there are.

   If the base pack pins nothing, a jail with **no auth pack at all** has no model pin — which may
   be correct (Claude Code's own defaults, which move when Anthropic moves them) or may be a silent
   hole that changes a jail's model under the user without a config change.

   _Leaning:_ **The base `claude` pack pins nothing; `claude-bedrock` pins the Bedrock IDs.** A
   model ID is a property of the credential path, and Bedrock's are the only ones yolo has a reason
   to know. The "silent hole" reading is really a request for `describe` to *report* the effective
   model, which is OQ-2's job rather than a pin.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 **OQ-9 — is `env_sources` still the right home for the AWS keys?** It works and is invisible
   to yolo's config model (§1). §11.2 moves the *non-secret* half into a pack; the secret half has
   to live somewhere, and `env_sources` puts it cleartext at 0644 in several files (§7.4).

   _Leaning:_ **Keep `env_sources` until something better than "a file on disk" exists**, and fix
   the mode rather than the mechanism. Answering OQ-2 makes the *bundle* visible to the config
   model without moving the secret.

   **Answer:**
   > _(empty — fill in when decided)_

### 12.5 Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-5 | **NO pack→pack composition.** `requires_pack` (and A2's `conflicts`) retired — the flat, ordered `packs` list is the whole composition story. Build it when something breaks without it | 2026-08-13 | §11.1, [`retired-decisions.md`](retired-decisions.md) Thread A |
| OQ-8 | **Generalize** #32's transport into the loophole framework — and #32 is not merged at all. `loopback-tls` becomes the framework's only transport; `unix-socket` retired | 2026-08-13 | §12.2, [`loophole-transport.md`](loophole-transport.md) §7.3–§7.4 |

> [!WARNING]
> **OQ-8 was decided against the recommendation in §12.2, and the cost was accepted knowingly:**
> macOS + podman stays broken until the unification ships, and #32's 1064 tested lines are
> re-derived rather than reused. Do not re-propose "merge #32 now, migrate later" — it lives in
> `brokerrelay`, where the framework cannot own it, which is the reason the fast path was refused.
>
> **And do not read OQ-5 as "packs cannot depend on anything".** What was removed is a *pack→pack*
> edge; `requires_env` still makes a keyless MCP server inert, which is what made the motivating
> case harmless once Thread A collapsed the two auth packs into one.
