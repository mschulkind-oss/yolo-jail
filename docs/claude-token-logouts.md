# Claude token logouts — diagnosis & fix

Entry point when a jail (or host) prompts `Please run /login · API Error: 401 {"type":"authentication_error"}` more often than once a month.

This doc is user-facing and operational. Background:

- [`HANDOFF-credentials-logout-2026-04-28.md`](../HANDOFF-credentials-logout-2026-04-28.md) — current investigation notes (broker era).
- [`HANDOFF-credentials-logout.md`](../HANDOFF-credentials-logout.md) — earlier handoff, still useful for binary offsets and ruled-out hypotheses.
- [`src/bundled_loopholes/claude-oauth-broker/README.md`](../src/bundled_loopholes/claude-oauth-broker/README.md) — broker architecture + operator ops.
- [`docs/claude-oauth-mitm-proxy-plan.md`](claude-oauth-mitm-proxy-plan.md) — historical design notes for the broker split.

## Architecture (post-`cb6e850`, post-`e7b7073`)

Host and jail are **independent identities** by design.

- **Host** Claude reads/writes `~/.claude/.credentials.json` and talks to Anthropic directly.
- **Jails** share `~/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json`. Each jail's `~/.claude/.credentials.json` is a relative symlink into the shared dir (resolves only inside the jail).
- A **singleton** `yolo-claude-oauth-broker-host` daemon serves every jail. PID file at `/tmp/yolo-claude-oauth-broker.pid`, socket at `/tmp/yolo-claude-oauth-broker.sock`, log at `~/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log`.

Implication for triage: **divergence between host and shared creds is the design**, not a bug. Don't try to "re-converge" them — that re-introduces the refresh-token race the split was designed to eliminate.

## TL;DR

Repeated logouts almost always mean one of:

1. **Broker singleton not running** — never spawned, or crashed.
2. **Shared creds expired without a refresh landing** — the symptom the new doctor check surfaces.
3. **Refresh token was server-side revoked** — even with `expiresAt` in the future, Anthropic can invalidate. Re-`/login` from inside a jail is the only fix.

All three are diagnosable in seconds with `yolo doctor` and `yolo broker status` on the host.

## Symptoms

- A jail session returns `API Error: 401 ... Please run /login` mid-task.
- Host `claude` outside a jail prompts for `/login` after you've just logged in recently. (Host and jail are independent — re-`/login` on the host does **not** revive the jail.)
- `stat ~/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json` shows `expiresAt` has passed and the file hasn't been touched in hours.

## Step 1 — run `yolo doctor` on the host

```bash
yolo doctor
```

Scan the Loopholes section for the `claude-oauth-broker` lines.

| Symptom | What it means | Fix |
|---|---|---|
| `claude-oauth-broker: inactive — requires.command_on_path 'claude' not met` | `claude` isn't on the host PATH. Broker never activates. | Install Claude Code, or set `loopholes.claude-oauth-broker.enabled: false` if intentional. |
| `NOTE: ca.crt not yet generated` | Fresh install, state dir is empty. | `just deploy` (or `yolo-claude-oauth-broker-host --init-ca` directly). |
| `loophole claude-oauth-broker: daemon not running` | Singleton hasn't been started. | `yolo broker restart` (or just run a jail — first `yolo run` spawns it). |
| `loophole claude-oauth-broker: stale PID file …` | Previous singleton crashed. | `yolo broker restart`. |
| `loophole claude-oauth-broker: daemon unresponsive …` | Process exists but doesn't answer ping. | `yolo broker restart` — typical after a wheel upgrade left old code in memory. |
| `shared creds expired Nm ago` | Refreshes are not landing. | Re-`/login` from inside a jail; tail the broker log to see what's happening. |
| `shared creds expire in Nm` | Approaching expiry without a refresh. | Watch — if it ticks down without a refresh landing, escalate. |
| `loophole claude-oauth-broker: daemon live (pid=…, ping ok)` and `shared creds valid for Xh Ym` | All good. | — |

## Step 2 — broker status + log

```bash
yolo broker status
yolo broker logs -n 50
```

The log shows every `POST /v1/oauth/token` proxied from a jail. Healthy refresh cadence is one `is_refresh=True` request every ~7–8h per active jail. If the log shows only `is_refresh=False` (PKCE `authorization_code`, i.e. `/login`) entries — Claude inside the jail is not sending refresh-token grants. That's the open architectural question; see the handoff doc.

```bash
# Last 20 entries with grant type + status
grep -E 'is_refresh=|status=' \
  ~/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log \
  | tail -20
```

## Step 3 — server-side revocation

Even with `expiresAt` in the future, Anthropic can invalidate a refresh token. Symptom: broker log shows `400 invalid_grant` on a refresh attempt. Only fix: re-`/login` from inside a jail. The new creds will replace the shared file via the broker's mirror-on-/login path.

## Step 4 — broker bypassed (jail reaching Anthropic directly)

Possible causes:

- `NODE_EXTRA_CA_CERTS` not set in the jail (TLS to intercepted `platform.claude.com` fails, Claude falls back).
- `--add-host platform.claude.com:127.0.0.1` missing from the container runtime invocation.
- The in-jail `oauth-broker-jail` daemon crashed — `cat ~/.local/state/yolo-jail-daemons/claude-oauth-broker.log` inside a jail.

Watch shared file mtime while a jail makes an authed request:

```bash
# Terminal 1: inside a running jail
claude -p 'reply OK'

# Terminal 2: on the host
watch -n 1 'stat -c "mtime=%y" \
  ~/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json'
```

If the mtime advances but the broker log has nothing around that timestamp, the jail wrote the file by another path (host-side script, cb6e850-era bandaid). Investigate before relying on the broker again.

## Manual checks cheat sheet

```bash
# Broker state — CA, leaf, lock
ls ~/.local/share/yolo-jail/state/claude-oauth-broker/

# Broker self-check (singleton ping + parseable creds + …)
yolo-claude-oauth-broker-host --self-check

# Singleton log (one file, all jails)
tail -F ~/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log

# In-jail TLS terminator log (run inside a jail)
cat ~/.local/state/yolo-jail-daemons/claude-oauth-broker.log

# Shared creds state
stat ~/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json
python3 - <<'PY'
import json, os, datetime
p = os.path.expanduser(
    "~/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json"
)
d = json.load(open(p))["claudeAiOauth"]
exp = datetime.datetime.fromtimestamp(d["expiresAt"] / 1000, tz=datetime.timezone.utc)
now = datetime.datetime.now(datetime.timezone.utc)
print(f"refreshToken[:16] = {d['refreshToken'][:16]}")
print(f"expiresAt         = {exp.isoformat()} ({(exp - now).total_seconds() / 3600:+.2f}h)")
PY
```

## When to update this doc

- A new Claude Code version moves the token endpoint → update `TOKEN_URL` in `src/oauth_broker.py` and note it here.
- A failure mode shows up that doesn't map to Step 1–4 → add it as a new row in Step 1 or a subsection here.
- Singleton paths or the symptom-check thresholds change in `cli.py` → mirror them here.
