# Claude Code OAuth refresh — mechanics

**Status:** research, in two layers, and the layers disagree.
[§1](#1-why-this-doc-exists)–[§9](#9-pointers) were gathered against **Claude
Code 2.1.143**, cross-references refreshed 2026-08-19 (broker moved into
`packs/claude`), stamped 2026-08-23.
**[§10](#10-prior-art-re-measured-mechanics-and-the-on-demand-question-2026-09-20)
was added 2026-09-20 and is the only layer measured against a current release
(2.1.278)** — it folds in a survey of how other tools solve
"N processes, one credential file, one rotating refresh token", re-derives the
refresh mechanics, and **supersedes several claims above**
([§10.8](#108-what-this-section-supersedes) is the list). Read that section
before acting on anything in [§3](#3-the-refresh-state-machine) through
[§6](#6-architecture-decisions).

The 2.1.143 layer's binary-level findings have **not** been re-derived wholesale
— treat every offset, string and minified identifier there as version-pinned, and
note that minified names are chunk-local and rotate between releases.
[§7](#7-reproducing-this-yourself) tells you how to redo the scan. The
`src/*.py` paths below are **Python-era names**, annotated inline with their Go
successors — the live broker is `internal/oauthbroker` (verified 2026-08-23).

This is a **research** document: it records what is known and measured, not what
will be built. Its one recommendation
([§10.7](#107-recommendation-on-the-proactive-loop)) is a recommendation.

Research-grade notes on **how Claude Code 2.1.143 actually manages its
OAuth tokens**, including the two distinct failure paths that surface
as "Please run /login" with zero broker activity. Companion to:

- [`claude-token-logouts.md`](claude-token-logouts.md) — user-facing
  operational triage.
- [`packs/claude/loopholes/claude-oauth-broker/README.md`](../../packs/claude/loopholes/claude-oauth-broker/README.md)
  — the live broker architecture + operator ops. It moved there from
  `bundled_loopholes/` on 2026-08-19, when the broker became a `loophole`
  contribution of the official `claude` pack and that channel was deleted.

Everything below is grounded in strings extracted from
`/home/agent/.local/share/claude/versions/2.1.143` (the 233 MB
Bun-compiled ELF that `claude` resolves to). Reproducer scripts and
extracted strings live in `.research/` (gitignored); the canonical
extracted scan is `.research/binary_scan.txt`. See
[§7](#7-reproducing-this-yourself) for how to redo this for any future
Claude release.

## 1. Why this doc exists

Two previous handoffs landed on the same user-visible symptom — "in-jail
Claude shows /login despite a fresh-looking creds file" — but described
different mechanisms (broker socket inode desync, "Claude doesn't even
try to refresh"). When we dug into the binary the actual picture turned
out to be richer than either handoff alone: there are at least **three
independent paths** to that symptom, and a single architectural fix
masks all three. The fix shipped in
`src/oauth_broker.py` *(Python era; the Go broker is `internal/oauthbroker`)* on 2026-05-17 (background
refresher daemon thread).

The architectural rationale doesn't fit in a code comment, and the
reverse-engineering was load-bearing on the decision, so it lives here.

## 2. Mental model — Claude's three token surfaces

For an in-jail Claude Code process, the OAuth access token exists in
three places at once. They drift, and the rules for resolving the drift
are non-obvious.

| Surface | What it holds | Who writes it | Who reads it |
|---|---|---|---|
| **In-memory `xq` cache** | Whatever Claude loaded last. Hot path for every API call. | `QYH()` clears it; `kZ()` repopulates from disk. | Every `api.anthropic.com` call attaches `Authorization: Bearer xq().accessToken`. |
| **On-disk creds file** (`~/.claude/.credentials.json`, which in our jails is a symlink into the shared creds dir) | The persisted JSON blob: `accessToken`, `refreshToken`, `expiresAt`, `scopes`, `subscriptionType`. | Claude itself after a successful refresh; our broker after a successful host-side refresh. | `kZ()` reads it; `Dk1()` watches its `mtimeMs`. |
| **`T86` dead-refresh-token set** | An in-process Set of refresh tokens that have ever produced an `invalid_grant` reply during this Claude lifetime. | `v86()` adds on `invalid_grant`. Never removed. | `v86()` early-returns false if the current refresh token is in here. |

**The on-disk shape is load-bearing (Claude ≥ 2.1.200).** The logged-in
check requires the metadata keys — a creds file carrying only the token
trio (`accessToken`, `refreshToken`, `expiresAt`) is treated as *not
logged in* even when the tokens are valid. A/B proven 2026-07-03 on
2.1.201: the same trio with `scopes`, `subscriptionType`,
`rateLimitTier` grafted on works. Consequence: **no writer of the file
may strip metadata down to the trio**. Our two writers divide the work:
the broker preserves whatever metadata the previous record carries and
falls back to the token response's `scope` string for `scopes`
(`_normalize_oauth`, `src/oauth_broker.py` *(Python era; the Go broker is `internal/oauthbroker`)*),
but it cannot invent `subscriptionType` / `rateLimitTier` it never saw
— a `/login` mirrored into a from-scratch shared file yields trio +
`scopes` only. The full shape is guaranteed only after the jail
entrypoint's harvest runs at the next launch and grafts the metadata
from the regular file Claude leaves behind after `/login`
(`_ensure_credentials_symlink`, `src/entrypoint/agent_configs.py`).
Until then, other running jails see a shared record without
`subscriptionType` / `rateLimitTier`; whether `scopes` alone satisfies
the ≥ 2.1.200 check is untested (the A/B grafted all three keys
together), so don't rule out the stripped-metadata mechanism in that
window.

`T86` is the surface that has been making people's lives mysterious.
It's not persisted — restart Claude and it's empty — and it's not
visible in any log or telemetry the broker can see. It also can never
*shrink*, so a single transient upstream blip permanently disables
refresh for the rest of the Claude process's lifetime.

## 3. The refresh state machine

Below is the decoded flow for the user-token (Claude Pro/Max) refresh
path, as it appears in the bundle. JS identifiers are the minified
names so you can grep for them; `binary_scan.txt` line / byte offsets
in parens.

### 3.1 Trigger: 401 from `/v1/messages`

```js
// Inside the api_request retry loop (binary pos ≈226536571)
if (Y instanceof Cq && Y.status === 401 || EW$(Y)) {
  if (!ML() && EE()) (await gxH().catch(() => null))?.invalidate();
  if (w) if (await Pu(w), xq()?.accessToken === w) {
    if (reH() !== null || !WD8(Y) && ++M >= rf5)
      throw mH("api_request", "api_request_oauth_refresh_exhausted"),
            new DS(Y, _);
  } else M = 0;
}
A = await H();
w = Bj() ? xq()?.accessToken : void 0;
```

Plain reading:

1. Real-API call returns 401.
2. Call `Pu(w)` where `w` is the access token Claude just got 401'd with.
3. After `Pu` resolves, check whether `xq().accessToken` is still equal
   to `w`. If yes → refresh did *not* land → after `rf5` retries (5)
   throw `api_request_oauth_refresh_exhausted` → user sees /login.
   If no → reset the retry counter and try the API call again.

There is **no proactive timer**. There is no "the access token will
expire in N minutes, let me refresh ahead of time" code path for the
user-token. Until a 401 fires, the access token in `xq()` is used as
opaque blob — Claude doesn't even examine `expiresAt`. This is the
reactive-only behavior the original handoff was confused about.

> [!NOTE]
> **Re-verified against 2.1.278 (2026-09-20): still true for the interactive
> path.** The bundle does now contain a proactive scheduler and a near-expiry
> predicate, but the scheduler belongs to Remote Control's daemon-auth worker and
> the predicate is only evaluated when something calls the refresh check — the
> interactive Pro/Max path is still 401-driven. Details, and the two lead-time
> constants that matter,
> in [§10.3](#103-claude-code-21278-is-not-the-claude-code-this-document-describes).

(A separate `cXH` class with proactive refresh exists in the bundle, but
it's for **MCP-OAuth** tokens — not the user's Pro/Max token. The
MCP-OAuth walkthrough in `.research/REPORT.md` *(never committed, so
there is nothing to open)* has the diff.)

### 3.2 `Pu` — single-flight wrapper

```js
function Pu(H) {
  let $ = Z86.get(H);
  if ($) return $;
  let q = jk1(H).finally(() => { Z86.delete(H) });
  return Z86.set(H, q), q;
}
```

Concurrent 401s for the same access token coalesce on one in-flight
promise — the auth2api "assign promise synchronously before any await"
pattern. The interesting work is in `jk1`.

### 3.3 `jk1` — the actual recovery decision tree (load-bearing)

```js
async function jk1(H) {
  QYH();                               // clear xq.cache + kZ.cache
  let $ = await kZ();                  // re-read .credentials.json
  if (!$?.refreshToken) {
    // … SDK-callback path for token-fd / env-var modes …
    if (process.env.CLAUDE_CODE_OAUTH_TOKEN || pc()) {
      let _ = (await d9().readAsync())?.claudeAiOauth;
      if (_?.accessToken && _.accessToken !== H) {
        // Disk has a different access token than the one we got 401'd with.
        // Adopt it.  NO HTTP REQUEST.
        return d("tengu_oauth_401_recovered_from_disk", {}), !0;
      }
    }
    return mH("oauth_401_recovery", /* … */), !1;
  }
  if ($.accessToken !== H) {
    // Same outcome, different telemetry event name.
    return d("tengu_oauth_401_recovered_from_keychain", {}), !0;
  }
  return wY(0, !0, H);                 // fall through to HTTP refresh
}
```

There are **two early returns before the HTTP path**, and *both* are
the same shape: "if disk has a different access token than the one we
just tried, use it." This is the disk-recovery hook the broker fix
exploits.

### 3.4 `wY` → `v86` — the HTTP refresh path

```js
async function v86(H, $, q) {
  await Dk1();                         // mtime-check creds file → maybe invalidate
  let _ = xq();
  if (!$) {                            // $ = force flag, true on 401 path
    if (!_?.refreshToken || !t6H(_.expiresAt)) return !1;
  }
  if (!_?.refreshToken || T86.has(_.refreshToken)) return !1;
  // …
  let M = await kZ();
  if (!M?.refreshToken) return !1;
  if (T86.has(M.refreshToken)) return !1;  // <-- THIS LINE
  // … take .oauth_refresh.lock flock …
  try {
    let w = await _4$(M.refreshToken, /* … */);
    await LmH(w);                      // write tokens, clear caches
    return !0;
  } catch (M) {
    // …
    if (cp$(M) && O) T86.add(O),       // <-- AND THIS LINE
        d("tengu_oauth_refresh_token_marked_dead_invalid_grant", {});
    return !1;
  }
}
```

`_4$` is the actual `POST platform.claude.com/v1/oauth/token` call;
that's the only outbound HTTP for refresh, and it's the one our in-jail
TLS terminator intercepts.

The two `T86`-related lines are the ones that produce the "zero
terminator hits" symptom. After any `invalid_grant` reply for refresh
token `O`, `T86.add(O)` permanently marks it dead for this Claude
process; subsequent calls into `v86` early-return *before* `_4$` runs.

### 3.5 `Dk1` — the mtime sentinel

```js
async function Dk1() {
  try {
    let { mtimeMs: H } = await Q2H.stat(
      N86.join(x8(), ".credentials.json")
    );
    if (H !== xwK) xwK = H, QYH();
  } catch {
    xq.cache?.clear?.(), kZ.cache?.clear?.();
  }
}
```

Plain reading: stat `.credentials.json`; if `mtimeMs` differs from the
cached value `xwK`, clear the in-memory token caches and re-read on
next `xq()` / `kZ()` call.

Crucially `Dk1` is only invoked at the top of `v86` — i.e. on the
HTTP-refresh path, *after* `jk1` has already decided to fall through.
There is **no per-request mtime check**. The implication: external
writes to the creds file are picked up at the next `Pu` invocation,
not on the next API call. That's fine for our purposes — the disk
read inside `jk1` (the `kZ()` call right after `QYH()`) already
re-reads fresh on every 401.

## 4. The three paths that look identical from outside

All three produce the same user-visible symptom: "zero entries in the
in-jail terminator log, then 'Please run /login' after some hours."
They have different mechanisms.

### Path A — Claude is idle

Plain reactive-only behavior. User stops typing; access token expires
at `expiresAt`; no API call happens, so no 401 happens, so no refresh
happens. User comes back hours later, types, gets a 401, *now* the
refresh attempts to fire — but by then the refresh token may also be
in trouble (see Path B/C). This is the original handoff's hypothesis #2.

### Path B — `T86` poisoning from a transient broker error

Path A's recovery attempt hits one of our broker failure modes
(2026-05-13 inode-desync, 2026-04-23 cross-file mirror race, Cloudflare
1010 mistaken for `invalid_grant`, …). The first failure marks the
refresh token dead in `T86`. Every subsequent 401 takes the
`jk1 → wY → v86` path, finds the same token in `T86`, returns false.
Zero new terminator entries because `_4$` is never called.

### Path C — Cross-jail single-use rotation race

Jail A calls refresh successfully; Anthropic rotates the refresh token
upstream and returns the new value, which Jail A's broker writes to the
shared creds file. Jail B's Claude, with the *old* refresh token still
in its in-memory `xq` cache, eventually 401s, falls through to `v86`,
calls `_4$` with the now-burnt refresh token, gets `invalid_grant`,
poisons `T86`. Same dead-end as Path B.

(Note: this race is already mitigated by the broker holding the
`REFRESH_LOCK` flock, but only for the **on-host** singleton. Two
brokers can't race; two jail Claudes' in-memory caches can.)

> [!WARNING]
> **Both halves of that note are wrong, in opposite directions** — corrected in
> [§10.5](#105-the-crux-note-confronted) (2026-09-20). The flock is **not** a
> property of the singleton: its path is derived from `$HOME` alone, so N brokers
> in one home take the same inode. And two jail Claudes' in-memory caches can
> **diverge** but cannot race upstream: the terminator routes every refresh grant
> to the broker, which reads the shared file and ignores the token the client
> presented, so a burnt in-memory refresh token never leaves the jail.
> **Paths B and C are both structurally closed as of 2.1.278** — for reasons that
> are not the background refresher ([§10.4](#104-three-structural-protections-yolo-has-and-did-not-know-it-had)).

## 5. Why writing the disk file is the right hook

The pre-fix architecture only refreshed when the in-jail TLS terminator
saw a request. That works for Path A only if Claude actually fires its
refresh — which we just saw it doesn't, reliably. And it can never
work for Paths B/C because `T86` short-circuits the request before
`_4$` runs.

The disk-recovery branch in `jk1` ([§3.3](#33-jk1--the-actual-recovery-decision-tree-load-bearing))
is the only refresh-adjacent code path that:

1. Runs on every 401 (not gated on idle-vs-active).
2. Does **not** consult `T86`.
3. Adopts whatever the disk has, regardless of how it got there.

So if a host-side process keeps the disk file ahead of expiry, all
three paths collapse:

- **Path A**: when the user's first post-expiry API call 401s, `jk1`
  reads disk → finds a fresh access token (written by the host
  refresher minutes before expiry) → adopts it. No HTTP refresh from
  Claude's side; never reaches `v86`.
- **Path B**: same flow as A. `T86` is irrelevant because we never call
  `v86`.
- **Path C**: same. The "burnt refresh token in Claude's in-memory
  cache" doesn't matter — `jk1`'s `kZ()` re-reads disk, finds the
  post-rotation tokens, adopts the access token directly.

This is why a 60-second proactive refresher on the host is an
architectural fix and not a band-aid.

> [!WARNING]
> **That last sentence no longer holds against 2.1.278.** Paths B and C are
> closed by the terminator's routing and the broker's reply shape, not by the
> refresher ([§10.4](#104-three-structural-protections-yolo-has-and-did-not-know-it-had),
> [§10.6](#106-the-regression-analysis-path-by-path)). The loop still masks
> **Path A**, which is a latency-and-availability job — real, measured at
> suspend/resume, and the reason the recommendation is still *keep it*
> ([§10.7](#107-recommendation-on-the-proactive-loop)) — but it is not what makes
> the design correct. The three things that do are undocumented and untested;
> that is the larger exposure.

## 6. Architecture decisions

### 6.1 Lead time: 5 minutes

Match `claude-oauth-proxy` (Go reference impl). Long enough that two
refresh ticks can both miss without exposing Claude to an expired
disk-token; short enough to not burn extra upstream refreshes.
`auth2api` (TS reference) uses 4 hours, which is fine for credit-card-
billed proxies aggressively keeping tokens warm but is overkill for
our single-user case.

### 6.2 Tick: 60 seconds

Same as both proactive references. Bounds the worst-case staleness
window to 60 s plus the upstream refresh round-trip. Coarser ticks
(5 min) would mean a Claude that wakes up at minute 4 of an interval
could still see a 60-s-stale token; finer ticks (every 5 s) burn
syscalls without measurable benefit.

**Exception (added 2026-07-03): fast retry on transient failure.**
Suspend/resume broke the 60-s bound in practice: the token expires
during sleep, the refresher fires within seconds of wake but DNS isn't
up yet (`upstream_unreachable`), and the full-tick wait left running
jails holding an expired token long enough to exhaust Claude's 401
retries. When a tick fails with `upstream_unreachable` while the token
is still due, the loop now waits `BACKGROUND_REFRESH_FAST_RETRY_SECONDS`
(5 s) instead, capped at `BACKGROUND_REFRESH_MAX_FAST_RETRIES` (12 ≈
one normal tick) consecutive fast retries so a long outage falls back
to the normal cadence. Non-transient errors (`invalid_grant` / any
upstream 4xx) never fast-retry — hammering a revoked refresh token buys
nothing and risks upstream rate limits. Residual race: Claude can still
401 in the first seconds after wake, before the NIC exists at all; that
window can't be closed from the broker side.

### 6.3 Where the loop lives

Inside the host singleton broker process, not a separate daemon.
Three reasons:

1. The singleton already holds `REFRESH_LOCK` (the on-disk flock that
   serializes refreshes across jails). Embedding the loop reuses that
   serialization for free — the background tick and an on-demand
   request will never collide.
   ⚠ The *reuse* argument holds; the **singleton** premise does not. The flock's
   path is a function of `$HOME` alone, so N broker processes in one home contend
   on the same inode — which is the measurement the ruling to retire
   `host_daemon.scope: "host"` rests on
   ([`host-daemon-ownership.md`](../design/host-daemon-ownership.md), RULED
   2026-09-20, built nowhere). Under per-jail helpers this reason is unchanged;
   see [§10.5](#105-the-crux-note-confronted).
2. The singleton already owns the upstream HTTP call, the
   Cloudflare-evading User-Agent, the atomic file write, and the
   `_describe_creds` log helpers. The loop is ~80 LOC because all the
   primitives existed.
3. Lifecycle is trivial: daemon thread dies with the singleton on
   SIGTERM via `host_service`'s existing signal handler.

### 6.4 Why we *also* kept the on-demand path

The in-jail TLS terminator and the broker's existing on-demand refresh
handler are kept as-is. Two reasons:

1. The `/login` flow still goes through it (authorization_code grant,
   not refresh).
2. If Claude *does* attempt a refresh — say, a fast-mode classifier
   request 401s before the user's foreground request hits the
   disk-recovery path — the on-demand handler still serves it
   correctly. Defense in depth.

## 7. Reproducing this yourself

When Claude Code releases a new version and you want to verify the
mechanics haven't changed:

```bash
# Locate the bundle
realpath "$(which claude)"
# → /home/agent/.local/share/claude/versions/<version>

CLAUDE=/home/agent/.local/share/claude/versions/2.1.143

# Confirm refresh endpoint hasn't moved
rg -oab 'platform\.claude\.com/v1/oauth/token' "$CLAUDE" | head -3

# Find the 401 recovery function (jk1 in 2.1.143; name may rotate)
rg -oab 'async function jk1\(' "$CLAUDE"
# … then dd the byte window to read 4 KB of context:
OFFSET=$(rg -oab 'async function jk1\(' "$CLAUDE" | head -1 | cut -d: -f1)
dd if="$CLAUDE" bs=1 skip=$((OFFSET - 1000)) count=8000 2>/dev/null

# Find T86 (the dead-refresh-token set; name will rotate but
# 'oauth_refresh_token_marked_dead' is a stable telemetry string)
rg -oa 'oauth_refresh_token_marked_dead[^"]*' "$CLAUDE" | head
rg -oab 'T86\.add\(' "$CLAUDE"

# Confirm Dk1 (mtime sentinel) still exists
rg -oab 'mtimeMs:H.*xwK' "$CLAUDE"
```

If any of those greps come back empty, the mechanics shifted in the
new release. Re-read the bundle around the `tengu_oauth_*` telemetry
strings — those are the most stable anchors, since they're shipped to
Anthropic's analytics and changing them silently is unusual.

## 8. What we still don't know

Open after this round of research. None of these block the implemented
fix, but they are worth investigating before any *next* round.

1. ~~**`ANTHROPIC_BASE_URL` for `/v1/messages` in prod.**~~ **ANSWERED 2026-09-02: the env var is
   honored in prod, and the saved subscription bearer follows it.** Measured with exactly the
   30-second test this item proposed (loopback listener + `claude -p`, claude-cli 2.1.220, team
   OAuth login, no `ANTHROPIC_AUTH_TOKEN`/`ANTHROPIC_API_KEY` set): every `/v1/messages?beta=true`
   request landed on the redirected base URL carrying `Authorization: Bearer sk-ant-oat0…`. So the
   reverse-proxy approach ("intercept `api.anthropic.com`", Option B of the archived
   `claude-oauth-mitm-proxy-plan.md`) is viable by base URL alone — and the same fact is a measured
   exfiltration channel, recorded with implications in
   [`agent-auth-modes.md` §8.1](../design/agent-auth-modes.md#81-measured-2026-09-02-the-subscription-bearer-follows-anthropic_base_url).
2. **Anthropic's server-side grace window past `expiresAt`.** The
   2026-05-17 incident showed Claude happy for 23 min past `expiresAt`
   client-side; could be Anthropic leniency, could be Claude idle. Test
   #1 in `.research/REPORT.md` *(never committed, so there is nothing to
   open)* resolves it. **Still open, and widened**: the related and more
   consequential question is whether the *refresh token* has a reuse/grace
   interval — see [§10.1](#101-the-grace-window-question-answered-first) for what
   the specs and six identity vendors do, why Anthropic's silence must not be
   read either way, and [§10.9](#109-open-questions-added-by-this-section) item 1
   for the experiment.
3. **Concurrency between proactive loop and Claude's own 401-driven
   refresh.** Both go through `do_refresh`'s flock, so they should
   compose. Worth a stress test with N=8 concurrent jails before any
   future scale increase.
4. **`CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR` as an alternative to
   bind-mount.** Less mtime-race-prone but requires holding an fd
   across Claude's lifetime. Could replace the file bind-mount
   entirely in a future version; investigated only at the existence
   level.
5. **`T86` observability.** Today there's no way for the broker to know
   a given jail's Claude has poisoned its in-memory `T86`. If we ever
   want a `yolo doctor` check that catches it pre-symptom, we'd need
   a probe (e.g., a synthetic 401 via a known-bad model call) — not
   free.

## 9. Pointers

- `src/oauth_broker.py` — host singleton broker; the `_refresh_due`,
  `_background_refresh_tick`, `_background_refresher_loop`,
  `start_background_refresher` family is the fix.
- `src/oauth_broker_jail.py` — in-jail TLS terminator, unchanged by the
  fix. Still required for `/login` and serves as defense-in-depth for
  any refresh Claude *does* attempt.
- `tests/test_oauth_broker.py` — search for `# Background refresher`
  section for the new coverage.
- `.research/REPORT.md` — the original research-agent report;
  prior-art survey of four OS Claude proxies and exact bundle quotes.
- `.research/binary_scan.txt` — pre-extracted 600-byte windows around
  each OAuth anchor; faster to grep than the raw 233 MB binary.

## 10. Prior art, re-measured mechanics, and the on-demand question (2026-09-20)

**Status of this section:** the only part of this document measured against a
**current** Claude release. Everything above is pinned to 2.1.143; everything
here is pinned to **2.1.278**, the build `claude` resolves to in this jail
(MEASURED — `realpath "$(which claude)"`). Where the two disagree, this section
says so and names the claim it supersedes
([§10.8](#108-what-this-section-supersedes)). Nothing here is a build decision:
this is a research doc, and [§10.7](#107-recommendation-on-the-proactive-loop)
is a recommendation, not a plan.

It exists because of one question — *"why do we ever need a refresher? can't we
just refresh on demand?"* — and one instruction: *fold the prior art in, and
avoid a regression.* [§4](#4-the-three-paths-that-look-identical-from-outside)'s
three paths are the regression list, and
[§10.6](#106-the-regression-analysis-path-by-path) works each one.

Evidence labels: **SPEC** (an RFC or draft), **VENDOR** (a vendor's own docs or
shipped code), **PRACTICE** (a shipped or proposed implementation, or a field
report) for external claims; **MEASURED** (ran it here), **READ** (read the code
or the page), **CLAIMED** (someone asserts it) for claims about this tree and
about Claude's binary.

### 10.1 The grace-window question, answered first

This is the finding that decides how bad a lost race is, so it goes first.

**The specs neither require nor forbid a grace window.**

- SPEC, READ: RFC 9700 [§2.2.2](https://www.rfc-editor.org/rfc/rfc9700.html#section-2.2.2) —
  *"Refresh tokens for public clients MUST be sender-constrained or use refresh
  token rotation"*. [§4.14.2](https://www.rfc-editor.org/rfc/rfc9700.html#section-4.14.2)
  describes rotation
  and its penalty: the AS *"cannot determine which party submitted the invalid
  refresh token, but it will revoke the active refresh token. This stops the
  attack at the cost of forcing the legitimate client to obtain a fresh
  authorization grant."* The BCP contains **no** text about grace periods, reuse
  intervals, or concurrent legitimate use — confirmed by two independent reads of
  the document. <https://www.rfc-editor.org/rfc/rfc9700.html>
- SPEC, READ: OAuth 2.1 (`draft-ietf-oauth-v2-1-15`) carries the same MUST and a
  harsher penalty — on replay the AS revokes *"the active refresh token as well as
  the access authorization grant associated with it"* — but
  [§4.3.3](https://www.ietf.org/archive/id/draft-ietf-oauth-v2-1-15.txt) says the AS
  **"MAY revoke the old refresh token after issuing a new refresh token"**. That
  `MAY` is load-bearing: **a grace window is spec-conformant.** Strict
  one-use-ever is permitted, not mandated.
  <https://www.ietf.org/archive/id/draft-ietf-oauth-v2-1-15.txt>

**Identity vendors ship one, at every default position.** All four numbers below
were read from the vendor's own documentation.

| Provider | Rotation | Grace / reuse window | Default |
|---|---|---|---|
| Okta | on when enabled | *"The default number of seconds for the **Grace period for token rotation** is set to 30 seconds. You can change the value to any number from 0 through 60 seconds."* — *"After the refresh token is rotated, the previous token remains valid for this amount of time to allow clients to get the new token."* | **grace ON (30 s)** |
| Auth0 | configurable | rotation overlap / leeway; *"helps to avoid concurrency issues when exchanging the rotating refresh token multiple times within a given timeframe"*; *"Only the previous token can be reused; if the second-to-last one is exchanged, breach detection will be triggered"* | **"By default leeway is disabled."** |
| Ory Hydra | configurable | `rotation_grace_period` + `rotation_grace_reuse_count`; Ory's own warning: enabling it *"effectively disables this security feature for the duration of the grace period"* | off |
| PingFederate | off by default | *"Refresh Token Rolling Grace Period"* — the time a rolled token stays valid *"in the event that the client failed to receive an updated one during a roll"* | rolling off |
| Microsoft Entra ID | rotates | *"The Microsoft identity platform doesn't revoke old refresh tokens when used to fetch new access tokens."* | **no race exists** |
| Google | does not rotate | n/a — *"continue to use them as long as they remain valid"* | **no race exists** |

Sources: <https://developer.okta.com/docs/guides/refresh-tokens/main/>,
<https://auth0.com/docs/secure/tokens/refresh-tokens/configure-refresh-token-rotation>,
<https://www.ory.com/docs/hydra/guides/graceful-token-refresh>,
<https://docs.pingidentity.com/pingfederate/12.2/administrators_reference_guide/help_authorizationserversettingstasklet_oauthauthorizationserversettingsstate.html>,
<https://learn.microsoft.com/en-us/entra/identity-platform/refresh-tokens>,
<https://developers.google.com/identity/protocols/oauth2>
(Okta and Auth0 fetched directly for this section; the other four are the
prior-art survey's reads, carried over with their URLs.)

**And now the part that matters for yolo, stated so it cannot be misread:**

> [!IMPORTANT]
> **Nobody has measured whether Anthropic has a grace window, and Anthropic
> documents nothing about refresh semantics at all.**
>
> VENDOR, READ — an *absence*: Anthropic's authentication documentation covers
> credential storage, precedence and `apiKeyHelper`, and says nothing about the
> token endpoint, the `refresh_token` grant, rotation, single-use semantics, or
> any reuse interval (<https://code.claude.com/docs/en/authentication>).
> PRACTICE, CLAIMED — every third-party report describes rotation as immediate
> and single-use, with the loser getting `invalid_grant`
> (<https://github.com/anthropics/claude-code/issues/43392>,
> <https://github.com/anthropics/claude-code/issues/48786>,
> <https://github.com/editor-code-assistant/eca/issues/462>). The one thing
> anybody *measured* points away from family revocation: oh-my-pi observed that
> *"minutes after the failure, a usage fetch still succeeded on the un-refreshed
> access token"* (<https://github.com/can1357/oh-my-pi/issues/5396>).
>
> **Read the absence as an absence.** It is not evidence that a lost race is
> survivable, and it is not evidence that it is fatal. It is unmeasured — and
> [§10.9](#109-open-questions-added-by-this-section) names the experiment that
> would settle it. Every grace window in the table above is an **authorization
> server** setting; yolo is a client. There is no knob here to turn even if the
> answer were favourable.

The closest thing to a related Anthropic statement is the rationale already
recorded in the broker README (CLAIMED, not re-verified here): the OAuth issuer
supports multiple concurrent active refresh tokens per account, which is what
makes host/jail identity separation cheap. That is about *independent grants*,
not about reusing *one* rotated token, and it must not be conflated with a grace
window.

### 10.2 What everyone else builds for "N processes, one credential file"

The survey is unanimous on the mechanism and split on everything else. All rows
are VENDOR or PRACTICE, READ from the source or the vendor's docs.

| Tool | Mechanism | Lock spans | On lock failure | Note |
|---|---|---|---|---|
| MSAL .NET `CrossPlatLock` | dedicated `<cache>.lockfile`, `FileShare.None`, `DeleteOnClose`, 100 ms × 600 spin | read → **POST** → write | **fails closed** (throws) | writes `pid processName` into the lock so a human can see the holder |
| msal-extensions (Python) | `portalocker` (`fcntl.flock`) + an `'x'`-mode sentinel | the **write only** | logs and **proceeds** | `os.remove`s the lock file on exit — the documented stale-lock bug class |
| Azure CLI | consumes the above | cache mutation | inherits | serializes the cache, not the exchange |
| GitHub CLI | `refresh.lock` + `syscall.Flock` + in-process mutex; reload-before-spend | refresh path only | **proceeds anyway** | ⚠ **PROPOSED, not shipped** — issue and PRs open, author says *"not merge-ready as-is"* |
| botocore / AWS CLI v2 SSO | in-process `threading.Lock` only | nothing | n/a | rotates refresh tokens and has **no** cross-process coordination |
| gcloud | SQLite, 5 s busy timeout | one statement | n/a | store integrity, not refresh serialization |
| ECR credential helper | atomic `CreateTemp`+`Rename` | nothing | n/a | its own comment: *"there is no out of process locking"* |
| Vault Agent auto-auth | one background renewer writing a sink file | n/a | n/a | the only surveyed precedent for a *proactive* refresher |
| ssh-agent / EC2 IMDS | the consumer never holds the renewable secret | n/a | n/a | removes the race instead of serializing it |

Sources, in row order:
<https://raw.githubusercontent.com/AzureAD/microsoft-authentication-library-for-dotnet/main/src/client/Microsoft.Identity.Client.Extensions.Msal/Shared/CrossPlatLock.cs>,
<https://github.com/AzureAD/microsoft-authentication-extensions-for-python/blob/dev/msal_extensions/cache_lock.py>,
<https://github.com/Azure/azure-cli/blob/dev/src/azure-cli-core/azure/cli/core/auth/persistence.py>,
<https://github.com/cli/cli/issues/14449>,
<https://github.com/boto/botocore/blob/develop/botocore/tokens.py>,
<https://github.com/twistedpair/google-cloud-sdk/blob/master/google-cloud-sdk/lib/googlecloudsdk/core/credentials/creds.py>,
<https://github.com/awslabs/amazon-ecr-credential-helper/blob/main/ecr-login/cache/file.go>,
<https://developer.hashicorp.com/vault/docs/agent-and-proxy/autoauth>,
<https://man.openbsd.org/ssh-agent>,
<https://docs.aws.amazon.com/AWSEc2/latest/UserGuide/instance-metadata-security-credentials.html>

**The four things every working implementation does**, and yolo does all four
(READ, `internal/oauthbroker/refresh.go`): a **dedicated** lock file, never the
credential file; an **advisory kernel lock** on it; a **fresh read of the
credential after acquiring** and before deciding to spend; and the loser
**adopting the winner's result** instead of refreshing again. In yolo those are
`RefreshLockPath` (a fixed rendezvous derived from `BrokerDir()`, itself a
function of `$HOME` alone), `syscall.Flock(LOCK_EX)`, `CachedTokens(credsPath)`
called *inside* the lock, and that cache hit being returned as an ordinary token
response.

**yolo is stricter than the field on exactly one axis, and it should stay that
way.** `withRefreshLock` treats a flock failure as a hard error — *"We must NOT
silently proceed unlocked (that would let concurrent jails burn the token)"*
(READ). Of everything surveyed only MSAL .NET also fails closed. GitHub CLI's
design explicitly runs the refresh anyway so *"a filesystem limitation never
blocks the user"*, and msal-extensions Python logs and continues. **That
fail-open is the named temptation**, and it is what a future change to "avoid a
hang" would reach for.

**Four decoys, each of which dominates the search results for this problem:**

1. **In-process single-flight is a different problem.** `golang.org/x/sync/singleflight`,
   AWS SDK Go v2's `CredentialsCache`, and Claude's own promise-coalescing
   wrapper ([§3.2](#32-pu--single-flight-wrapper)) coordinate goroutines or
   async callers inside **one** program. Both vendors say so themselves: MSAL's
   lock *"does not ensure thread safety, i.e. 2 threads from the same process
   will pass through this lock"*, and gh's in-process mutex comment says the
   flock *"guards against other gh processes but not against goroutines sharing
   this single AuthConfig instance"*. The two mechanisms are orthogonal and a
   tool with only the first still burns a single-use token N times. A doc
   sentence saying "the refresh is single-flighted" must say **which kind**.
2. **Atomic rename is not coordination.** `mkstemp`+`fsync`+`os.replace` and
   `CreateTemp`+`Rename` guarantee no reader sees a torn file. They do nothing
   about two processes each POSTing the same refresh token.
3. **A database is not a refresh lock.** gcloud's SQLite busy timeout serializes
   *statements*; a store that can never be corrupted can still spend a single-use
   token N times.
4. **A keychain is not an answer either.** It serializes item access, not the
   refresh, and races on its own — aws-vault's parallel-invocation work had to
   add serialization on top after hitting *"item already exists"* from the
   keychain (<https://github.com/ByteNess/aws-vault/pull/337>, an open PR on a
   fork — a field report, not shipped behaviour).

One idea from the prior art is genuinely on the other side of the wire and worth
naming only so nobody chases it: cloudflare/workers-oauth-provider's proposal for
**idempotent rotation** — cache the rotation result keyed by the consumed token,
so a replay within the window returns the same minted result instead of revoking
(<https://github.com/cloudflare/workers-oauth-provider/issues/214>). That is a
thing an authorization server can offer. It is the shape of a feature request to
Anthropic, not a lever for a client.

### 10.3 Claude Code 2.1.278 is not the Claude Code this document describes

All of the following is MEASURED — strings and byte windows extracted from
`/home/agent/.local/share/claude/versions/2.1.278` with the recipe in
[§7](#7-reproducing-this-yourself). Minified identifiers are chunk-local and
**will** rotate between releases; the exported names and the telemetry strings
are the stable anchors and are given alongside.

**The claude.ai refresh path now takes a real cross-process lock, and it fails
closed.** The successor to [`v86`](#34-wy--v86--the-http-refresh-path) acquires a
lock (exported as `acquireOAuthRefreshLock`) on the **config directory** before
refreshing:

- The lock is `proper-lockfile`-shaped — `{stale: 60000, update: 5000,
  onCompromised}` — i.e. a heartbeat lock with the lock-**theft** hazard flock
  does not have. `onCompromised` aborts an `AbortController` whose signal is
  passed into the token POST, so a stolen lock **cancels the in-flight refresh**
  rather than letting two grants go out. Outcome `lock_compromised`.
- A **second, legacy** lock is taken at a sibling path derived by resolving the
  same directory (`tengu_oauth_refresh_legacy_lock_contended`), for interop with
  older releases that spelled it differently. Both are derived from the **config
  directory**, so neither reaches the shared credentials directory.
- Contention: five retries at `1000 + random()*1000` ms, then the outcome
  `lock_timeout` or `lock_busy` and an `OAuthRefreshLockContendedError`. **It
  never proceeds unlocked.** Matching changelog entry, VENDOR/READ, v2.1.248:
  *"Fixed being sent to the login screen when another Claude Code process held
  the token refresh lock while the session token had expired; the request now
  fails with a retryable error instead."*
- The lock **spans the network POST**, as MSAL .NET's does and as yolo's does.

This resolves, for 2.1.278, a question the prior art left open: a decompilation
of 2.1.258/259 reported the claude.ai path as having only in-process
de-duplication (<https://github.com/anthropics/claude-code/issues/91708>).
Against 2.1.278 that is **not** what the bundle contains.

**The lock is per-config-directory, so it does not serialize across yolo jails.**
The lock path is derived from `CLAUDE_SECURESTORAGE_CONFIG_DIR ?? <config dir>`,
and in a jail that is the jail's own `~/.claude` — MEASURED here: only
`~/.claude/.credentials.json` is a symlink (`-> ../.claude-shared-credentials/.credentials.json`),
and `~/.claude-shared-credentials` is the machine-scoped shared directory. So
Claude's lock excludes *sibling Claude processes in the same jail* and nothing
else. It **composes with** yolo's broker flock — different resources, always
acquired in the same order (Claude's, then the broker's) — and replaces none of
it.

**Read-and-adopt now happens three times, and it is named as a race resolution.**
Before acquiring, inside the lock, and — this is the new one — **in the failure
handler**: after a refresh throws, Claude clears its cache, re-reads the
credential, and if the access token changed it returns `"refreshed"` with
`tengu_oauth_token_refresh_race_recovered`. That is exactly the "on failure,
re-read once before reporting a logout" remedy the prior art recommends, shipped
by the vendor.

**Writes are a compare-and-swap.** Claude writes the credential only if the file
still holds the refresh token it posted (or an empty one); otherwise it declines
to write and returns `adopted_sibling`
(`tengu_oauth_refresh_save_adopted_newer_write`), retrying three times with a
100 ms-scaled backoff. This is the structural absence of the clobber in
<https://github.com/anthropics/claude-code/issues/88583>, where the race
**loser** persisted its emptied state over the winner's rotated credential.

**`T86` survives — and it now has teeth.** The dead-refresh-token set is still
an in-process `Set` that production code never clears (a reset exists, exported
as `__resetKnownDeadRefreshTokensForTest`). What changed is what happens
alongside it: on a classified `invalid_grant`, Claude adds the token to the set
**and writes the credential file with `refreshToken: ""`, `accessToken: ""`,
`expiresAt: 0`** — guarded by "only if the file still holds the token that just
died" (`tengu_oauth_refresh_token_marked_dead_invalid_grant`,
`tengu_oauth_refresh_token_cleared_on_disk`). In a jail, that file is the
**shared** credential. [§10.4](#104-three-structural-protections-yolo-has-and-did-not-know-it-had)
is why that has never fired here, and how one change would let it.

**The classifier that gates all of that reads the response body's top-level
`error` field** — it requires HTTP 400 or 401 **and** a body whose `error` is
exactly `"invalid_grant"`. Remember this; it is the whole of
[§10.4](#104-three-structural-protections-yolo-has-and-did-not-know-it-had)'s
second protection.

**Claude's own near-expiry threshold is 300 seconds** — the predicate is
`Date.now() + 300000 >= expiresAt`. That is **exactly**
`BackgroundRefreshLeadSeconds`. There is no margin between yolo's lead and
Claude's notion of "due"; they are the same number.

> [!WARNING]
> **This paragraph used to end "the only reason yolo reliably moves first is
> that it *polls*". RETRACTED 2026-09-20 — it does not move first, and the
> comparison was against the wrong constant.** Claude evaluates the predicate
> before every API call, which in an interactive session is far more often than
> a 60-second tick, so Claude reaches the broker first essentially always. And
> what it reaches is **not** the refresher's 300s lead but `CachedTokens`'
> independent floor of **90 seconds** (`oauthbroker.go:162`), which serves the
> on-disk record unchanged above that mark. So between 300s and 90s the broker
> answers HTTP 200 with the caller's own `access_token`, and
> [§3.1](#31-trigger-401-from-v1messages)'s exhaustion counter increments on
> exactly that condition (`accessToken===De`, limit `Rmo` = **2**). MEASURED in
> one jail's terminator log: **364 of 380** successful refresh replies carried
> 90–299 seconds of life. yolo therefore disagrees with itself — the refresher
> agrees with Claude and the cache does not — and `force` is dropped at the
> terminator, so Claude cannot override it. **FIXED 2026-09-20** by splitting the one
> constant into the two questions it was answering — a liveness floor for the
> `cached` action, and a refresh floor DERIVED as `ConsumerRefreshDueMS + 60_000`
> so the inequality lives in the source. Recorded in
> [`agent-credentials.md`](../reference/agent-credentials.md#-the-90300-threshold-mismatch--a-live-defect).

**Still no proactive timer on the interactive path** —
[§3.1](#31-trigger-401-from-v1messages)'s core claim survives. The proactive
scheduler that *does* exist in the bundle belongs to the **daemon-auth worker**
(Remote Control's daemon, alongside `daemon-auth-status`, `attachWorker`,
`token_update`): it schedules at `expiresAt − 240000 ms`, clamped to
`[5000, 86400000]`, logs `auth: scheduling proactive refresh in <n>s`, re-checks
the keychain every 30 s when no token is found, and — note the wording —
treats a sibling having already refreshed as success: `auth: token still valid
(cross-process refresh or not yet due)`. MCP OAuth has had its own proactive
refresh since v1.0.110 (VENDOR/READ, changelog). Neither is the interactive
Pro/Max path a jail uses.

**The vendor has been fixing this exact class for a year.** VENDOR, READ, from
`anthropics/claude-code`'s own `CHANGELOG.md`
(<https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md>):

| Version | Entry |
|---|---|
| 2.1.59 | *"Fixed MCP OAuth token refresh race condition when running multiple Claude Code instances simultaneously"* |
| 2.1.81 | *"Fixed multiple concurrent Claude Code sessions requiring repeated re-authentication when one session refreshes its OAuth token"* |
| 2.1.118 | *"Fixed MCP OAuth refresh proceeding without its cross-process lock under contention"* |
| 2.1.126 | *"Fixed a rare race where a concurrent credential write could clear a valid OAuth refresh token"* |
| 2.1.129 | *"Fixed OAuth refresh race after wake-from-sleep that could log out all running sessions"* |
| 2.1.133 | *"Fixed parallel sessions all dead-ending at 401 after a refresh-token race wiped shared credentials"* |
| 2.1.136 | *"Fixed MCP OAuth refresh tokens being lost when multiple servers refresh concurrently"* |
| 2.1.221 | *"Fixed a rare wake-from-sleep race where two Claude Code processes could both refresh the same MCP connector or WIF OAuth token at once"* |
| 2.1.248 | *"Fixed being sent to the login screen when another Claude Code process held the token refresh lock…"* |

The direction of travel is unambiguous, and it is the same four-part pattern as
[§10.2](#102-what-everyone-else-builds-for-n-processes-one-credential-file).

> [!NOTE]
> A separate **workload-identity-federation** credential path in the same bundle
> ships its own lock over the WIF credentials *directory*, with retry budgets
> `{"fail-closed": 5, "fail-open": 15}`, an adopt path
> (`tengu_wif_user_oauth_refresh_race_resolved`) and an `invalid_grant` cleanup
> (`tengu_wif_user_oauth_refresh_token_cleared`). Its `user_oauth` authentication
> **type** is a WIF config value, not the claude.ai subscription credential — do
> not read its fail-open `oidc_federation` mode, or its *"refreshing without
> cross-process serialization"* message, as describing the path a jail uses.

### 10.4 Three structural protections yolo has, and did not know it had

This is the part that answers *"surely we can handle this better without racing
things"*: **an in-jail Claude is already structurally unable to participate in
the race** — for three reasons, none of which is the background refresher, and
all three of which are one small "improvement" away from being gone.

**P1 — the broker discards the refresh token the client presented.** MEASURED,
READ in-tree: the in-jail terminator routes every `POST /v1/oauth/token` whose
body has `grant_type == "refresh_token"` to `action=refresh`
(`IsRefreshGrant` → `Refresh`); everything else (the `authorization_code` of
`/login`) is proxied untouched. `action=refresh` calls `DoRefresh(credsPath)`,
which takes **only the creds path** and never looks at the request body. The
token the jail's Claude was holding — stale, burnt, or fine — **never leaves the
jail**. The broker reads the shared file under its own flock and refreshes from
*that*.

This is the ssh-agent / EC2-IMDS property — *the consumer never holds the
renewable secret* — **half** made. Half, because `AsOAuthResponse` still returns
the real `refresh_token` in the reply, so Claude writes a real refresh token into
its view of the world. It simply can never usefully spend one, because every
spend is routed to the broker's own file read.

**P2 — no broker reply can trigger Claude's dead-token wipe.** MEASURED,
READ in-tree: `Refresh()` maps any broker error dict to **HTTP 400** with the
dict as the body, whose top-level `error` is one of `upstream_http`,
`upstream_unreachable`, `upstream_bad_response`, `creds_unreadable`,
`no_refresh_token` — **never `invalid_grant`**. Claude's classifier
([§10.3](#103-claude-code-21278-is-not-the-claude-code-this-document-describes))
requires the body's top-level `error` to be exactly `"invalid_grant"`. A genuine
upstream `invalid_grant` arrives embedded in the dict's `body` string, one level
down, where the classifier does not look.

Consequence: in a jail, Claude's `invalid_grant` handler **cannot fire**. The
dead-token set is never poisoned by a broker reply, and — far more important —
**the shared credentials file is never blanked**. The failure yolo would
otherwise inherit is the one in
<https://github.com/anthropics/claude-code/issues/88583>, except worse, because
the file being emptied is shared by every jail on the machine and by the host
broker.

**P3 — the broker is the sole writer of the shared file, because its reply omits
one field.** MEASURED: `AsOAuthResponse` returns `{access_token, refresh_token,
expires_in, token_type}` and **no `scope`**. Claude parses scopes from the
response's `scope` string; absent, that is the empty list; with empty scopes both
its compare-and-swap save and its legacy save short-circuit
(`tengu_oauth_tokens_not_claude_ai`) and return success **without writing**.
Claude then clears its token cache and re-reads from disk — where the broker has
already written. One writer, by accident of a missing field.

> [!WARNING]
> **All three protections are accidental, undocumented, and individually
> reversible by a change that looks like an improvement.**
>
> - **P1** dies if anyone routes refresh grants through `action=proxy` "so the
>   upstream sees the real request", or makes `DoRefresh` honour a
>   client-supplied refresh token.
> - **P2** dies if anyone makes the terminator "pass the upstream error through
>   faithfully" — hoisting `invalid_grant` to the reply's top-level `error`, or
>   forwarding the upstream body verbatim. The cost of that change is one jail's
>   Claude blanking the shared credential for every jail and the host.
> - **P3** dies if anyone echoes `scope` in `AsOAuthResponse` for symmetry with
>   `NormalizeOAuth`, which already reads `scope` on the way in. Claude then
>   becomes a second writer of the shared file — one that takes its *own*
>   per-jail lock and not the broker's flock. The damage is bounded by Claude's
>   compare-and-swap, but the invariant "exactly one writer" is gone.
>
> These belong in tests before anything else in this area is touched. Each one
> fails silently: the tree stays green and the jail keeps working until two jails
> and an expiry line up.

P2 also has a **cost**. `Refresh()` flattens *every* broker failure to HTTP 400,
so a DNS failure after resume, a Cloudflare `429` from a datacenter IP
(PRACTICE, measured by a third party against this endpoint:
<https://github.com/earendil-works/pi/issues/9369>), a malformed body and a
genuinely dead grant are **indistinguishable to the client**: the status is
always 400 and the top-level `error` is always a broker-internal code.

Be precise about what that costs, because it is easy to overstate. **Diagnostic
loss is certain**: nothing downstream of the broker can tell "retry in a second"
from "this grant is gone", so neither Claude nor a human reading a jail log can.
**Behavioural loss is unverified.** The status-based transient classifier in the
bundle (`>= 500`, `429`, `408`, plus a list of socket error codes) is on the
**WIF** path (READ); on the claude.ai path the failure handler classifies by
*body content* — `invalid_grant`, account-on-hold — and returns a generic
refresh failure for everything else regardless of status, after which the
bounded api-request retry loop ([§3.1](#31-trigger-401-from-v1messages)) governs.
So the flattening may cost nothing behavioural there, or it may cost a transient
failure the same finite retry budget as a terminal one — which at suspend/resume
is the budget being spent before the NIC exists. **Measure before fixing**
([§10.9](#109-open-questions-added-by-this-section) item 2). Either way it
matters far more to a design that makes Claude refresh on every expiry than to
one that keeps the disk ahead.

### 10.5 The crux note, confronted

[§4](#4-the-three-paths-that-look-identical-from-outside)'s Path C carries this
parenthetical:

> *"this race is already mitigated by the broker holding the `REFRESH_LOCK`
> flock, but only for the on-host singleton. Two brokers can't race; two jail
> Claudes' in-memory caches can."*

**First half — unchanged, and for a reason the note gets wrong.** It is not the
singleton that makes two brokers safe. `RefreshLockPath` is `BrokerDir()/refresh.lock`
and `BrokerDir()` is a function of `$HOME` alone (READ) — it takes no daemon
name, no socket path, no jail id. **N brokers in one home contend on the same
inode however they are spelled**, and the second arrival re-reads the file inside
the lock and gets the first one's token back as a cache hit. That is the same
observation the maintainer's ruling to retire `host_daemon.scope: "host"` rests
on ([`host-daemon-ownership.md`](../design/host-daemon-ownership.md), **RULED
2026-09-20, built nowhere**). So under per-jail helpers this half is not merely
preserved — it never depended on the singleton.

**Second half — the in-memory cache race is real, and it is harmless.** Two jail
Claudes genuinely can hold divergent refresh tokens in memory, and no host-side
lock can see that. But **P1** ([§10.4](#104-three-structural-protections-yolo-has-and-did-not-know-it-had))
means neither of them can spend one: a refresh grant is routed to the broker,
which ignores the presented token entirely. The in-memory divergence costs one
extra 401 and one extra round trip that returns a cache hit. It does not cost the
grant, and it cannot produce an upstream `invalid_grant`.

So the honest form of the note is: *the flock serializes the host side; the
terminator's routing makes the jail side structurally unable to race at all.* And
**that second clause is untouched by the refresher question** — it is a property
of `IsRefreshGrant` → `action=refresh` → `DoRefresh(credsPath)`, not of any tick.

### 10.6 The regression analysis, path by path

The question: **if the proactive loop is dropped and refresh happens only on
demand, does each of [§4](#4-the-three-paths-that-look-identical-from-outside)'s
paths get more likely?** One path does. Two do not, for reasons that are *not*
the refresher — which is itself the finding.

#### Path A — Claude idles past expiry → 401 → recovery. **WORSE.**

On-demand works: the decision tree in
[§3.3](#33-jk1--the-actual-recovery-decision-tree-load-bearing) falls through to
the HTTP refresh, the `/etc/hosts` pin (MEASURED in this jail:
`127.0.0.1 platform.claude.com`) routes it to the in-jail terminator, and the
broker refreshes under its flock and replies. That is established, and it is why
"just refresh on demand" is a coherent proposal rather than a mistake.

What dropping the loop changes is **frequency and placement**. Today the disk
token is kept ahead of expiry, so the post-idle 401 is rare and, when it happens,
`kZ()` finds a fresh access token on disk and adopts it with **no HTTP at all**.
On-demand-only makes the full chain — Claude's own lock, the terminator, the
front, the broker's flock, Anthropic, the write, Claude's re-read — run on the
user's **first keystroke after a gap**, every time.

Three concrete costs, in descending confidence:

1. **Suspend/resume regresses, and it is already a measured incident.**
   [§6.2](#62-tick-60-seconds) records it: the token expires during sleep, the
   refresher fires within seconds of wake but DNS is not up, and the fix was the
   5 s × 12 fast retry so the **disk** is correct before Claude ever asks.
   On-demand-only removes that: the first post-wake request 401s, the refresh
   fails `upstream_unreachable`, and Claude spends its bounded retry budget
   against a network that is not up — with no way to tell a transient failure
   from a terminal one, because the broker flattens both to the same 400
   ([§10.4](#104-three-structural-protections-yolo-has-and-did-not-know-it-had)'s
   P2 cost). This is the single clearest regression and it is not hypothetical.
2. **The lead-time tie stops being comfortable.** Claude's own "due" threshold is
   300 s, identical to `BackgroundRefreshLeadSeconds`
   ([§10.3](#103-claude-code-21278-is-not-the-claude-code-this-document-describes)).
   Today yolo moves first only because it polls. That ordering is accidental and
   undocumented; whichever way the refresher question is settled, the inequality
   belongs in the code next to the constant, with the command that re-measures
   the vendor's number.
3. **Latency moves onto the interactive path.** Four processes and two locks, on
   a keystroke. Not a correctness matter, but it is what "keeping the disk file
   ahead" was buying.

Note what does **not** get worse: a failed refresh on this path cannot poison
anything, because the broker never emits `invalid_grant` (P2), and a network
failure is not classified as `invalid_grant` by Claude in any case.

#### Path B — dead-refresh-token poisoning. **UNCHANGED, and currently closed.**

This is the finding that inverts [§5](#5-why-writing-the-disk-file-is-the-right-hook)'s
argument. That section says the disk hook matters because *"`T86` short-circuits the request
before `_4$` runs"* and the proactive loop keeps Claude out of that code path.
Against 2.1.278 with the current terminator, **the poisoning cannot happen at
all**: the set is only populated by the `invalid_grant` classifier, and no broker
reply can satisfy it (P2). Dropping the loop multiplies the number of times
Claude *asks*, but the probability per ask is zero while the error-dict shape
holds.

So Path B is load-bearing on **P2**, not on the refresher. Two consequences:

- Keeping the refresher is not what protects Path B, and the doc should stop
  implying it is.
- **P2 is now a safety-critical invariant with no test.** That is a worse
  exposure than the refresher question itself.

Residual, honestly stated: if the in-jail terminator is down while the
`/etc/hosts` pin still points at `127.0.0.1`, Claude gets a connection refusal —
fails safe. If the **pin** is ever absent (a backend that does not add the host
entry, a jail whose entrypoint did not run), Claude reaches the real endpoint
with whatever token it holds, and **P1 and P2 both evaporate in the same
instant**. That, not the refresher, is the configuration worth a `yolo doctor`
check.

#### Path C — cross-jail single-use rotation race. **UNCHANGED, and structurally closed.**

Per [§10.5](#105-the-crux-note-confronted): the burnt in-memory refresh token
cannot reach Anthropic, because the terminator routes the grant to
`action=refresh` and the broker reads the file instead. The race resolves as an
extra round trip returning a cache hit. Dropping the loop makes that round trip
more frequent and changes nothing about its outcome.

#### A fourth hazard, and it is the one on-demand actually creates

Not in [§4](#4-the-three-paths-that-look-identical-from-outside), because it did
not exist in 2.1.143: **the in-jail Claude is now capable of writing the shared
credential file.** Today it does not, only because of **P3** — the reply omits
`scope`, so the save short-circuits. Under on-demand-only, Claude's write path is
exercised on **every** expiry instead of almost never, which means P3 stops being
a curiosity and becomes the thing standing between the design and a second,
unsynchronized writer of the shared file. The damage would still be bounded by
Claude's compare-and-swap
([§10.3](#103-claude-code-21278-is-not-the-claude-code-this-document-describes)),
which declines to write when the file has moved — but "bounded by the vendor's
CAS" is a much weaker guarantee than "never writes", and it is version-pinned to
a build that could change.

#### Summary table

| Path | Mechanism that closes it today | Effect of dropping the proactive loop |
|---|---|---|
| **A** — idle → expiry → 401 | the loop itself (disk kept ahead) + the 401 fall-through | **worse** — suspend/resume loses its fast retry; every expiry ends in a full chain on a keystroke; the 300 s lead tie stops being comfortable |
| **B** — dead-token poisoning | **P2** (no broker reply carries `invalid_grant`) | **unchanged** — more asks, zero probability per ask |
| **C** — cross-jail rotation race | **P1** (the presented token is discarded) | **unchanged** — more round trips, same outcome |
| **D** — a second writer of the shared file | **P3** (the reply omits `scope`) | **latent becomes live** — Claude's write path runs on every expiry |

### 10.7 Recommendation on the proactive loop

**KEEP it. Do not make it conditional. Rewrite its justification, and pin the
three protections it turns out not to be providing.**

The reasoning, stated so a future reader can disagree with the premise rather
than the conclusion:

- **Dropping it regresses exactly one thing, and that thing is already a
  recorded incident.** Suspend/resume ([§6.2](#62-tick-60-seconds)) is the
  measured case where the disk must be correct *before* Claude asks, and P2's
  400-flattening means a failed post-wake refresh reaches Claude as
  non-retryable.
- **It buys less than [§5](#5-why-writing-the-disk-file-is-the-right-hook)
  claims.** Against 2.1.278 the loop is *not* what masks Paths B and C; P1 and P2
  are. The loop is a **latency-and-availability** device, not a correctness one.
  That is a demotion, not a deletion — but the doc must stop calling it "an
  architectural fix and not a band-aid" without qualification.
- **Conditional is the worst of the three.** A loop that runs only under some
  predicate adds a second thing to get wrong (and a second thing to explain in a
  refusal message) for no measured benefit over always-on. The loop is one
  goroutine and one `stat` per minute.

With the loop kept, the work worth doing is **not** about the loop. In rough
order of how much regression each prevents:

1. **Pin P1, P2 and P3 with tests.** Specifically: `DoRefresh` must never return
   a top-level `error` of `"invalid_grant"` and the terminator must never hoist
   an upstream error code to the reply's top level (P2); refresh grants must
   route to `action=refresh` and never to `action=proxy` (P1); the refresh reply
   must not carry `scope` (P3) — or, if it ever should, that change must be made
   deliberately, with the second-writer consequence written down. Each of these
   is a cheap test protecting a silent, machine-wide failure.
2. **Stop flattening every failure to HTTP 400** — at minimum for
   diagnosability, and possibly for behaviour. Map `upstream_unreachable` and
   upstream `429`/`5xx` onto a status that carries the distinction, keeping `400`
   for the terminal cases. The diagnostic benefit is certain; the behavioural
   benefit on the claude.ai path is **unverified** and should be measured first
   ([§10.9](#109-open-questions-added-by-this-section) item 2). It is a
   precondition for anyone ever revisiting on-demand-only.
   ⚠ It interacts with P2: the mapping must change the **status only**, and must
   never hoist the upstream `error` string to the reply's top level.
3. **Bound the flock wait, and record the holder.** `LOCK_EX` is a blocking wait
   with no deadline and no deadlock detection (SPEC, READ:
   <https://man7.org/linux/man-pages/man2/flock.2.html>). yolo is immune to the
   *stale*-lock class — the kernel releases a flock when the holder dies,
   including on `SIGKILL`, and yolo never unlinks the lock file, which is the
   correct choice and must not be "tidied up". What remains is a **live** holder
   wedged in an upstream call; the 30 s HTTP timeout bounds the common case, but
   nothing bounds the pathological one. The non-regressing fix is a deadline that
   **reports** a distinct error, plus writing `pid argv0` into the lock file so
   the waiter can name the holder (MSAL .NET writes `"{ProcessId} {ProcessName}"`
   for exactly this reason; yolo opens the file `O_TRUNC` and writes nothing).
   ⚠ **A deadline must never become a fail-open.** The refusal is the feature.
4. **Write the lead-time inequality down** next to `BackgroundRefreshLeadSeconds`:
   yolo's lead must stay at or above Claude's own "due" threshold (300 s as of
   2.1.278) or the in-jail client starts initiating refreshes itself, with the
   re-measurement command beside it.
5. **Consider the custodian upgrade as a separate question, not as part of this
   one.** Making `AsOAuthResponse` withhold the real `refresh_token` would turn
   P1 from "cannot usefully spend it" into "never had it" — the ssh-agent / IMDS
   property, fully made. It is not needed for any path above, and it has real
   regression risk (Claude persists whatever it receives; yolo's own
   `no_refresh_token` guard keys on a non-empty refresh token; `/login` mirroring
   and the metadata-shape requirement in [§2](#2-mental-model--claudes-three-token-surfaces)
   both touch the same file). Verify against a live jail before changing
   anything.

**Named temptations, i.e. the changes to refuse:**

| Tempting change | Why it is a regression |
|---|---|
| Warn-and-proceed on a flock failure, to avoid a hang | Concurrent jails burn the token. Only MSAL .NET also fails closed; gh and msal-python both fail open and both say so in their source. The cost of guessing wrong is a revoked grant ([OAuth 2.1 §4.3.1](https://www.ietf.org/archive/id/draft-ietf-oauth-v2-1-15.txt)), i.e. `/login` in every jail. |
| Unlink the lock file on release, or move to a lock-file library | flock has no stale-lock class; lock-file artifacts do (msal-python's `os.remove`, and the portalocker/MSAL issues it produced), and heartbeat libraries import lock **theft** — which is why Claude's own `proper-lockfile` usage needs an `onCompromised` handler that aborts the in-flight POST. |
| Derive the lock path per-process, per-`TMPDIR`, or from a socket name | The rendezvous is an **inode**. Two spellings are two locks. |
| Assume Anthropic has a grace window | Nothing documents one and nobody has measured one ([§10.1](#101-the-grace-window-question-answered-first)). |
| "Pass the upstream error through faithfully" | Destroys **P2**; one jail can then blank the machine-wide credential. |
| Echo `scope` in the refresh reply "for symmetry" | Destroys **P3**; adds a second writer that does not hold the broker's flock. |

### 10.8 What this section supersedes

Claims above that are version-pinned to 2.1.143 and **no longer describe
2.1.278**. The earlier text is kept as written — it is the record of what was
true then — but do not act on these without re-measuring:

| Where | Claim | Status against 2.1.278 |
|---|---|---|
| [§2](#2-mental-model--claudes-three-token-surfaces) | `T86` is *"never removed"* and invisible | still in-process and never cleared in production, but it is now accompanied by a **disk wipe** of the credential, and it has a test-only reset |
| [§3.4](#34-wy--v86--the-http-refresh-path) | *"take `.oauth_refresh.lock` flock"* as an aside | the lock is now central: a heartbeat lock on the **config directory**, plus a legacy realpath sibling, five jittered retries, **fail-closed**, and an `onCompromised` handler that aborts the in-flight POST |
| [§4](#4-the-three-paths-that-look-identical-from-outside) Path B | poisoning from a transient broker error | **cannot occur** through the broker — no reply satisfies the classifier (**P2**) |
| [§4](#4-the-three-paths-that-look-identical-from-outside) Path C + its note | *"two jail Claudes' in-memory caches can [race]"* | they can diverge; they cannot race upstream (**P1**). Corrected in [§10.5](#105-the-crux-note-confronted) |
| [§5](#5-why-writing-the-disk-file-is-the-right-hook) | the disk hook *"is an architectural fix and not a band-aid"* because it masks all three paths | it masks **Path A**. B and C are closed by P1/P2 regardless. The loop is a latency-and-availability device |
| [§6.3](#63-where-the-loop-lives) | *"the singleton already holds `REFRESH_LOCK`"* as a reason to embed the loop | the reuse argument holds; the **singleton** premise does not — the flock is `$HOME`-derived and N brokers take the same inode ([`host-daemon-ownership.md`](../design/host-daemon-ownership.md)) |
| [§8](#8-what-we-still-dont-know) item 3 | *"worth a stress test with N=8 concurrent jails"* | still unrun, and now more interesting: the contenders are the broker's flock, Claude's per-jail lock, and the CAS save |

### 10.9 Open questions added by this section

1. **Does Anthropic have a reuse/grace interval?** Unmeasured by anyone. The test
   is small and destructive-if-wrong, so it wants a throwaway identity: refresh
   once, then replay the *previous* refresh token at T+1 s, T+15 s, T+45 s, and
   record whether each is accepted, and whether the winner's token survives.
   Until someone runs it, assume zero — and note that even a favourable answer
   changes nothing yolo can configure ([§10.1](#101-the-grace-window-question-answered-first)).
2. **Does the 400-flattening cause user-visible dead ends today?** The broker
   logs the real classification; Claude sees a 400. A soak that counts
   `upstream_unreachable` replies against Claude's retry exhaustion would settle
   whether item 2 of [§10.7](#107-recommendation-on-the-proactive-loop) is urgent
   or merely correct.
3. **Does Claude's own lock ever contend inside one jail?** It is scoped to the
   config directory, so it serializes sibling Claude processes in the same jail —
   which is a real configuration here. No measurement exists of how often it hits
   its five-retry budget, or what a jail does when it returns `lock_busy`.
4. **What happens on a backend without the `/etc/hosts` pin?** P1 and P2 both
   depend on the pin routing the refresh into the terminator. A jail without it
   reaches the real endpoint directly and inherits every failure this section
   says yolo does not have. Worth a `yolo doctor` check; worth confirming on
   `macos-user`, where the mechanism differs.
5. **Is the daemon-auth worker ever live in a jail?** The 240 s proactive
   scheduler belongs to Remote Control's daemon. If that daemon ever runs
   in-jail, there is a *third* refresh authority on one credential, with a lead
   shorter than both yolo's and the CLI's.
