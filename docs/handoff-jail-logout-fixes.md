# Handoff: fix the two residual jail-logout mechanisms

Audience: an agent running **inside a yolo-jail** with this repo mounted at
`/workspace`. All the host-side evidence below was gathered on the host on
2026-07-03 — you cannot re-verify it from inside the jail (broker logs,
`GLOBAL_HOME`, journalctl are host-only). Trust it; your job is the code fix
plus unit tests. Host-level verification steps for Matt are at the end.

## Context

Despite the 2026-05-17 broker fix (proactive background refresher — see
[`claude-oauth-refresh-mechanics.md`](claude-oauth-refresh-mechanics.md)),
jails still occasionally demand `/login`. Diagnosis on 2026-07-03 found the
refresher itself is working (the shared creds file stays fresh; the broker
log shows zero Claude-initiated refreshes ever needed). Two *other*
mechanisms produce the logouts. Both need fixes in this repo.

## Mechanism 1 — new workspaces boot logged-out (seed file never exists)

**Evidence (host, 2026-07-03):** shared creds were valid (refreshed
11:58:58), a first-ever `yolo run` in a workspace created `.yolo/home` at
12:11, and Claude ran the full `/login` flow at 12:13 anyway.

**Cause:** Claude decides "am I logged in" partly from `~/.claude.json`
(`oauthAccount`, `hasCompletedOnboarding`), not just `.credentials.json`.
New jails are supposed to inherit that state via the merge in
`src/cli/run_cmd.py` (search for "Seed claude.json onboarding", around line
1006): it merges `GLOBAL_HOME / ".claude" / "claude.json"` into the
per-workspace `ws_state / "claude" / "claude.json"`.

The seed source **has never existed** on this install:
`~/.local/share/yolo-jail/home/.claude/` is an empty directory (created
empty at state-init on 2026-06-27). The `if src_claude_json.is_file():`
guard makes the whole merge a silent no-op. Jails write login state only to
their per-workspace overlay and nothing propagates it back, so the seed can
never self-heal. Historically this worked only because the seed file was a
leftover from the pre-read-only-refactor era when jails wrote directly into
`GLOBAL_HOME`.

### Fix 1 (required): back-propagate auth keys to the seed

In the same seeding block of `src/cli/run_cmd.py` (runs on the **host**
during `yolo run`, before the container starts):

1. Keep the existing seed→workspace merge.
2. Add the reverse direction: if the workspace's `claude.json` contains
   `oauthAccount` and the `GLOBAL_HOME` seed is missing or lacks it, write
   an allowlisted subset of keys up into
   `GLOBAL_HOME / ".claude" / "claude.json"`:
   - `oauthAccount`
   - `hasCompletedOnboarding`

   Allowlist only — do **not** copy `mcpServers`, `projects`, or anything
   workspace-specific into the shared seed. Merge into whatever is already
   in the seed file (don't clobber unrelated keys if the file exists).
3. Note `GLOBAL_HOME / ".claude.json"` is a symlink to
   `.claude/claude.json` (see `src/cli/storage.py`, `_ensure_symlink`
   calls) — write through the real path `".claude" / "claude.json"`, and
   create parent dirs defensively.

Effect: the first `yolo run` of any already-logged-in workspace repairs the
seed; every workspace created afterwards inherits login state and boots
logged-in. A truly fresh install still needs exactly one `/login` — that's
correct and expected.

**Optional / stretch:** the broker already mirrors successful
`authorization_code` token responses into the shared creds file
(`_maybe_propagate_token_response` in `src/oauth_broker.py`). If the token
response body carries account info, the broker could also write the seed's
`oauthAccount` on login, removing the "needs one run of a logged-in
workspace" dependency. Only attempt if you can confirm the response schema
from an existing fixture/test — don't guess fields.

## Mechanism 2 — suspend/resume window (running jails hit /login)

**Evidence (host broker log, 2026-07-02):** machine slept ~27h
(suspend-then-hibernate; hourly wake blips are shorter than the 60s tick so
the refresher never ran). Token expired 19h into the sleep. On real wake at
16:33:34 the refresher fired within 2s but failed —
`error=upstream_unreachable` / `Temporary failure in name resolution` (DNS
not up yet) — and only succeeded on the *next* tick at 16:34:36. A running
in-jail Claude that 401s inside that ~60s window exhausts its 5 retries in
seconds and shows "Please run /login" (happened in polyclav, re-login at
16:41). The same wake-DNS-failure pattern appears in the log on 06-29,
06-30, 07-01, and 07-03.

### Fix 2 (required): fast retry when refresh fails transiently at/past expiry

Files: `src/oauth_broker.py` — `_background_refresh_tick` (~line 511) and
`_background_refresher_loop` (~line 542).

Recommended shape (keeps sleeping in the loop, keeps the tick unit-testable
without sleeps):

1. Change `_background_refresh_tick` to **return** a signal instead of
   `None` — e.g. `True` when it failed *transiently* while the token is
   due (i.e. `do_refresh` returned `error == "upstream_unreachable"` and
   `_refresh_due(...)` is still true), `False` otherwise.
2. In `_background_refresher_loop`, when the tick returns that signal, use
   a short wait (e.g. `FAST_RETRY_SECONDS = 5`) for the next
   `stop_event.wait(...)` instead of the full `tick_seconds`. Optionally
   cap consecutive fast retries (e.g. 12 ≈ one normal tick's worth) before
   falling back to the normal cadence, so a long outage doesn't hammer.
3. Only treat network-unreachable as transient. `invalid_grant` (or any
   4xx from upstream) must **not** fast-retry — retrying a revoked refresh
   token buys nothing and risks upstream rate limits.
4. Add module-level constants next to `BACKGROUND_REFRESH_TICK_SECONDS` /
   `BACKGROUND_REFRESH_LEAD_SECONDS` rather than inline literals.

This shrinks the stale-creds window at wake from ~62s to ~5–10s. It cannot
fully close the race (Claude can 401 before the NIC is up at all) — that's
acceptable; note it in the doc update below.

## Tests

- Fix 2: extend the `# Background refresher` section in
  `tests/test_oauth_broker.py` (~line 975). `_refresh_due` and the tick are
  already directly unit-tested there — follow that style. Cover: tick
  returns fast-retry signal on `upstream_unreachable` while due; no signal
  on success; no signal on `invalid_grant`; loop uses the short wait when
  signaled (inject `tick_seconds`/stop event as the existing tests do).
- Fix 1: seeding tests live around the run-command tests
  (`tests/test_cli_commands.py` has existing seed coverage — grep `seed`).
  Cover: ws-has-oauthAccount + missing seed → seed created with only
  allowlisted keys; seed exists without oauthAccount → keys merged in,
  unrelated seed keys preserved; neither side has oauthAccount → no write;
  corrupt seed JSON → doesn't crash `yolo run`.
- Run `pytest` and `ruff format --check` (CI enforces formatting — see
  commit `1eab9e2`).

## Docs to update

`docs/claude-token-logouts.md`:

- Add a Step 1 table row / subsection for each mechanism: "fresh workspace
  prompts /login despite valid shared creds" (seed repair — fixed, note the
  back-propagation) and "logout right after laptop wake" (fast-retry
  window, with the residual-race caveat).
- The doc's "When to update this doc" section explicitly asks for this.

Also update `docs/claude-oauth-refresh-mechanics.md` §8 if you close or
change any of its open questions.

## Constraints (you are in a jail)

- You can edit `/workspace` (this repo) and run its test suite. You cannot
  see host state: `~/.local/share/yolo-jail/` (real one), broker logs,
  `journalctl`, or other workspaces' `.yolo/` dirs.
- `~/.claude/skills/` is read-only in-jail; irrelevant to this task —
  don't touch it.
- Do not add AI attribution trailers to commits.

## Host verification for Matt (after merge + `just deploy` / wheel upgrade)

```bash
# 1. Broker must be restarted to pick up new code
yolo broker restart && yolo broker status

# 2. Seed repair: run a jail in an already-logged-in workspace, then
python3 -c "import json;d=json.load(open('/home/matt/.local/share/yolo-jail/home/.claude/claude.json'));print(bool(d.get('oauthAccount')), d.get('hasCompletedOnboarding'))"
# expect: True True

# 3. New-workspace check: `yolo run` in a never-jailed repo — Claude should
#    start logged-in, no /login prompt.

# 4. Wake behavior: after next suspend cycle,
grep -E "bg_refresh|fast" ~/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log | tail -20
# expect: a failed tick followed by a ~5s retry success, not a 60s gap.
```
