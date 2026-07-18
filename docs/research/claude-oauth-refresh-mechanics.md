# Claude Code OAuth refresh — mechanics

Research-grade notes on **how Claude Code 2.1.143 actually manages its
OAuth tokens**, including the two distinct failure paths that surface
as "Please run /login" with zero broker activity. Companion to:

- [`claude-token-logouts.md`](claude-token-logouts.md) — user-facing
  operational triage.
- [`claude-oauth-mitm-proxy-plan.md`](../plans/claude-oauth-mitm-proxy-plan.md)
  — historical design notes for the broker.

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
[`src/oauth_broker.py`](../src/oauth_broker.py) on 2026-05-17 (background
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
(`_normalize_oauth`, [`src/oauth_broker.py`](../src/oauth_broker.py)),
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

(A separate `cXH` class with proactive refresh exists in the bundle, but
it's for **MCP-OAuth** tokens — not the user's Pro/Max token. See
[`.research/REPORT.md`](../../.research/REPORT.md) §2.6 if you want the
diff.)

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

1. **`ANTHROPIC_BASE_URL` for `/v1/messages` in prod.** Bundle resolves
   `BASE_API_URL` from a hardcoded constant in prod mode; would unlock
   the cleaner "intercept `api.anthropic.com` reverse-proxy" Option B
   in [`claude-oauth-mitm-proxy-plan.md`](../plans/claude-oauth-mitm-proxy-plan.md).
   30-second test: set the env var, `claude -p hi`, tcpdump for outbound
   :443 to `api.anthropic.com`.
2. **Anthropic's server-side grace window past `expiresAt`.** The
   2026-05-17 incident showed Claude happy for 23 min past `expiresAt`
   client-side; could be Anthropic leniency, could be Claude idle. Test
   #1 in [`.research/REPORT.md`](../../.research/REPORT.md) §5 resolves it.
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
