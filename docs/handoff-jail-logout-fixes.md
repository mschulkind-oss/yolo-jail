# Handoff: fix the residual jail-logout mechanisms

Audience: an agent running **inside a yolo-jail** with this repo mounted at
`/workspace`. All the host-side evidence below was gathered on the host on
2026-07-03 — you cannot re-verify it from inside the jail (broker logs,
`GLOBAL_HOME`, other workspaces' `.yolo/` dirs are host-only). Trust it;
your job is the code fix plus unit tests. Host-level verification steps for
Matt are at the end.

## Status

Three logout mechanisms have been diagnosed. Two are **fixed and shipped**;
the third was found the same evening the first two deployed (it masked
them) and is **the open work item**. A one-time host-side state repair for
mechanism 3 was applied on 2026-07-03, so the system is currently stable —
the code fixes below are the defense that keeps it from regressing.

| # | Mechanism | Status |
|---|---|---|
| 1 | New workspaces boot logged-out (empty `GLOBAL_HOME` claude.json seed) | Fixed — `8f7b550` (`_sync_claude_json_seed` back-propagation) |
| 2 | Suspend/resume DNS window (bg refresher waited a full 60s tick) | Fixed — `e0ebba5` (fast retry on `upstream_unreachable`) |
| 3 | Broker-written creds file lacks `scopes`/`subscriptionType`; claude ≥ 2.1.200 rejects it | Fixed in code 2026-07-04 (Fixes A/B + atomic `_write_tokens`) — live after broker restart + image rebuild |
| 4 | Broker restart orphans the socket inode mounted into running jails | Fixed in code 2026-07-04 (round 2: supervised per-jail relay, per-connection dial) — live on next `yolo` invocation per jail |

## Mechanism 3 — stripped creds metadata (the open one)

### Evidence (host, 2026-07-03 evening)

- Claude Code auto-updated on the host through 2.1.199 → 2.1.200 (13:04)
  → 2.1.201. Jails run the host's claude binary (native-install
  passthrough), so every jail picked this up on its next launch.
- After that, **every jail relaunch demanded `/login`**, even with valid
  shared creds, `oauthAccount`, and `hasCompletedOnboarding` all present.
  Jail-side terminator logs show four `is_refresh=False` token POSTs
  (real logins) that evening — including one 90 seconds after a relaunch
  of a workspace that had just logged in.
- **A/B proven on the host**: a scratch `HOME` with the exact shared creds
  file (3 keys: `accessToken`, `refreshToken`, `expiresAt`) →
  `Not logged in · Please run /login`. The **same tokens** plus `scopes`,
  `subscriptionType`, `rateLimitTier` grafted on → works. The missing
  metadata keys are the whole difference.

### The loop

1. Jail boots → `_ensure_credentials_symlink()`
   (`src/entrypoint/agent_configs.py`) points
   `~/.claude/.credentials.json` at the shared file. The shared file has
   only the 3 token keys — it is written exclusively by the broker
   (`_normalize_oauth` in `src/oauth_broker.py` preserves metadata from
   `previous`, but the file never had any to preserve). Claude ≥ 2.1.200
   reads it → "not logged in".
2. User runs `/login`. Claude's atomic write (tmp + rename) **replaces the
   symlink with a regular file** holding the full 6-key record (observed
   directly: regular file in the overlay, written 0.4s after the login's
   token response). The broker's proxy mirror
   (`_maybe_propagate_token_response`) simultaneously writes the shared
   file — stripped back to 3 keys.
3. Next relaunch → `_ensure_credentials_symlink()` **deletes Claude's full
   record**: its migrate-copy branch only fires when the shared file is
   missing or empty, which is never true because the mirror just wrote it.
   It re-links to the stripped shared file → goto 1.

### State repair already applied (host, 2026-07-03)

The full record from a post-login overlay regular file was merged into the
shared creds file (newest token trio wins; metadata keys grafted on) and
the overlay symlink restored. Verified: a scratch-HOME boot against the
repaired shared file is logged in. From here on, `_normalize_oauth`'s
`dict(previous)` carries the metadata forward on every refresh/mirror —
**unless** something recreates the shared file from scratch (fresh
install, state re-init, a future `/login` after the file is lost). That's
what the code fixes prevent.

### Fix A (required): `_normalize_oauth` must not produce metadata-less creds

File: `src/oauth_broker.py` (`_normalize_oauth`, ~line 392).

- The upstream token response (both grant types) may carry a `scope` field
  (space-separated string, per OAuth spec). When present and the record
  would otherwise lack `scopes`, map it: `out["scopes"] = scope.split()`.
  Check what Claude's own file stores (a JSON list — see the fixture in
  `test_normalize_oauth_preserves_subscription`,
  `tests/test_oauth_broker.py` ~line 829) and match that shape exactly.
- Do not guess `subscriptionType`/`rateLimitTier` — they are not in the
  token response. Preserving them from `previous` (already the behavior)
  plus Fix B's harvest is how they arrive.
- Never let a write *remove* metadata keys that `previous` had (already
  true via `dict(previous)` — add a regression test for it).

### Fix B (required): entrypoint must harvest, not discard, the regular file

File: `src/entrypoint/agent_configs.py` (`_ensure_credentials_symlink`,
~line 464).

When the link path is a **regular file** (what Claude leaves behind after
`/login`), it is the freshest and fullest record of the *same* identity.
Instead of the current "copy only if shared missing/empty, else discard":

1. Parse it. If it has a `claudeAiOauth` dict:
   - Merge into the shared file: metadata keys (`scopes`,
     `subscriptionType`, `rateLimitTier`) always; the token trio
     (`accessToken`, `refreshToken`, `expiresAt`) only if its `expiresAt`
     is newer than the shared file's. Preserve unrelated keys already in
     the shared record.
   - Write atomically with restrictive mode (0600), matching
     `_write_tokens` in the broker.
2. Unparseable/empty regular file → keep today's behavior (replace with
   symlink; if the shared file is missing/empty, still attempt the copy
   first, as now).
3. Then replace with the symlink as today.

Note the concurrency caveat: the entrypoint runs at jail boot while the
host broker may refresh concurrently. The broker serializes its writes
with a flock on the host — the entrypoint (in-jail) cannot take that lock.
The expiresAt-newer guard makes a lost race benign (worst case the
entrypoint's token trio is stale and skipped; metadata merge is
idempotent). Don't try to add cross-boundary locking.

## Tests

- Fix A: extend the `_normalize_oauth` tests in `tests/test_oauth_broker.py`
  (~line 829). Cover: `scope` string → `scopes` list when previous lacks
  it; previous `scopes` win over response `scope` (or pick one rule and
  test it); no `scope` in response + none in previous → key absent (not
  crashed); metadata keys from previous always survive.
- Fix B: extend the credentials-symlink tests in
  `tests/test_entrypoint.py` (`test_credentials_symlink_created` /
  `test_credentials_symlink_migrates_existing_file`, ~line 1468). Cover:
  regular file with full record + non-empty shared file → metadata merged
  into shared, symlink restored; regular file with *newer* expiresAt →
  token trio adopted; *older* → shared tokens kept; corrupt regular file →
  no crash, symlink restored; shared missing → existing migrate behavior
  intact.
- Run `pytest` and `ruff format --check` (CI enforces formatting).

## Docs to update

- `docs/claude-token-logouts.md` — add a Step 1b row: "every jail relaunch
  demands /login after a claude-cli update" → creds file missing
  `scopes`/`subscriptionType`; fixed by broker scope-mapping + entrypoint
  harvest; note the one-time 2026-07-03 state repair.
- `docs/claude-oauth-refresh-mechanics.md` — the on-disk shape table (§ on
  the creds file) already lists `scopes`/`subscriptionType`; add that
  claude ≥ 2.1.200's logged-in check **requires** them (A/B proven
  2026-07-03 on 2.1.201), so any writer of that file must produce the full
  shape.

## Round 2 — relay unification (stale broker socket after restart)

Do this as a **separate change after** Fixes A/B land. It stands alone.

### Problem

On Linux, `yolo run` bind-mounts the broker's unix-socket **file**
(`run_cmd.py`, ~line 1835: `BROKER_SINGLETON_SOCKET.resolve()` →
`/run/yolo-services/claude-oauth-broker.sock`). A file bind-mount pins an
inode; when the broker restarts, `host_service.py` unlinks and re-binds
the path — new inode — and every already-running jail keeps the dead one.
All proxied auth traffic from those jails then fails `Connection refused`
(502) until the jail is relaunched. Latent since `e7b7073` (2026-04-24);
first bit on 2026-07-03 when a deploy restarted the broker under a
long-running jail and a `/login` code-exchange 502'd mid-flow.

### Decision: unify on the relay (macOS approach), not a dir mount

macOS already avoids this by accident: `_start_broker_relay`
(`src/cli/loopholes_runtime.py`, ~line 466) listens on a per-jail socket
inside the already-mounted `host_services_sockets_dir` and dials the real
broker path **per connection** — so a restarted broker is picked up on the
next connect. Chosen direction: use the relay on Linux too and drop the
`IS_MACOS` branch + the socket-file `-v` mount entirely. Rationale over
the alternative (bind-mounting a dedicated socket *directory*): one
codepath on both platforms, the broker's real socket is never exposed to
jails, and the relay is the natural place to finally fix `jail=unknown`
in the broker log (per-jail attribution).

### Requirements

1. `run_cmd.py` (~lines 1820–1847): always take the relay path; delete the
   Linux socket-file mount. The relay socket already lands at the expected
   `{JAIL_HOST_SERVICES_DIR}/claude-oauth-broker.sock` via the existing
   directory mount, and `YOLO_SERVICE_CLAUDE_OAUTH_BROKER_SOCKET` wiring
   is unchanged.
2. **Lifecycle** — the current relay is a daemon *thread* inside the
   `yolo run` host process, so it lives exactly as long as that process.
   Verify every flow where a container outlives its `yolo run` process
   (detached runs, exec/attach paths, host process crash); if any exist,
   the relay must move to a supervised standalone process (see
   `host_processes.py` machinery) instead of a thread. If `yolo run`
   always outlives the container, the thread is fine — document that
   invariant next to `_start_broker_relay`.
3. **Never silent** — a dead relay reproduces exactly the symptom this doc
   exists for (one jail 502s while `yolo doctor` says the broker is
   healthy), so:
   - `yolo doctor` must enumerate running jails and check each jail's
     relay socket answers (a connect + broker ping through it).
   - The in-jail terminator's error message
     (`src/oauth_broker_jail.py`) must distinguish "relay socket
     absent/connection refused" (relay layer) from an upstream broker
     error passed through, so the log names the failing layer.
4. **Attribution (follow-on, same round if cheap)** — the broker log
   prints `jail=unknown` on every request. The relay knows which jail it
   serves; if the host_service JSON protocol allows injecting/overriding
   the jail field on the first message, do it relay-side (trustworthy).
   If that makes the relay protocol-aware in an ugly way, fall back to
   the terminator self-reporting its jail name from env (spoofable
   in-jail, fine for logging) and note the difference in a comment.

### Tests (round 2)

- Relay: connect-per-dial behavior — kill/re-bind a fake broker socket
  between two connections through one relay; second connect must succeed
  (this is the regression test for the whole round).
- Relay survives a client that connects and immediately drops; fds don't
  leak across many connections (the `_pipe` close paths, ~line 493).
- Doctor: healthy relay → ok line; missing/dead relay socket → distinct
  failure line naming the jail.

### Docs (round 2)

- `claude-token-logouts.md`: add a Step 1 row — "one jail 502s /
  `Connection refused` in its terminator log while doctor says broker
  healthy" → stale relay (post-fix) / stale socket inode (pre-fix,
  relaunch the jail). Remove/amend the operational rule about relaunching
  jails after `yolo broker restart` once the relay lands.
- `src/bundled_loopholes/claude-oauth-broker/README.md`: architecture
  section gains the relay hop.

## Constraints (you are in a jail)

- You can edit `/workspace` (this repo) and run its test suite. You cannot
  see host state: `~/.local/share/yolo-jail/` (the real one), broker logs,
  or other workspaces' `.yolo/` dirs.
- `~/.claude/skills/` is read-only in-jail; irrelevant to this task —
  don't touch it.
- Do not add AI attribution trailers to commits.

## Host verification for Matt (after merge + deploy)

```bash
# 1. Broker must be restarted to pick up new code (note: restarting kills
#    broker access for currently-running jails — relaunch them after)
yolo broker restart && yolo broker status

# 2. Shared creds keep the full shape across a refresh cycle
python3 -c "import json;d=json.load(open('/home/matt/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json'))['claudeAiOauth'];print(sorted(d.keys()))"
# expect: accessToken, expiresAt, rateLimitTier, refreshToken, scopes, subscriptionType

# 3. Relaunch loop is dead: stop and re-run a jail twice in a row —
#    Claude must start logged-in both times, no /login prompt.

# 4. After the next /login anywhere (if one ever happens), re-run check 2 —
#    the mirror write must not strip the metadata keys.

# 5. (round 2, once the relay lands) restart the broker UNDER a running
#    jail, then make an authed request from inside it — must succeed
#    without relaunching the jail:
yolo broker restart
# inside the running jail:
claude -p 'reply OK'
```
