# claude-oauth-broker module

Reference implementation of a yolo-jail host-side module. MITM proxy that terminates TLS for `platform.claude.com` on the host, serializes every OAuth refresh through a flock, and hands jails a cached access token when one is still valid. See [`docs/claude-oauth-mitm-proxy-plan.md`](../../docs/claude-oauth-mitm-proxy-plan.md) for the full design and [`docs/claude-token-logouts.md`](../../docs/claude-token-logouts.md) for how this fits in the overall logout triage.

## Files

| File | Purpose |
|---|---|
| `manifest.jsonc` | Module manifest. Installed under `~/.local/share/yolo-jail/modules/claude-oauth-broker/` by `just deploy`. |
| `claude-oauth-broker.service` | systemd user unit template. `@@BROKER_BIN@@` is substituted at deploy time with the console script's absolute path. |
| `ca.crt`, `ca.key` | Generated on first run by `yolo-claude-oauth-broker --init-ca`. Root CA is valid 10 years; jails trust it via `NODE_EXTRA_CA_CERTS`. Never checked into git. |
| `server.crt`, `server.key` | Leaf cert for `platform.claude.com`, issued by the CA. Also generated on first run. |

## Install

`just deploy` handles everything:

1. Installs the yolo-jail wheel (gives you `yolo-claude-oauth-broker` on PATH).
2. Copies this directory to `~/.local/share/yolo-jail/modules/claude-oauth-broker/`.
3. Runs `yolo-claude-oauth-broker --init-ca` to generate the CA/leaf pair.
4. Templates the systemd unit and starts `claude-oauth-broker.service`.

## Port 443 requirement

Claude Code inside jails opens TLS to `platform.claude.com` on port 443 — we can't redirect that to a different port without patching the binary. Options:

- **`AmbientCapabilities=CAP_NET_BIND_SERVICE`** in the systemd unit (default). Works on most modern systemd setups; some restrictive user-namespace configurations disallow ambient caps and you'll need one of the fallbacks.
- **`sysctl net.ipv4.ip_unprivileged_port_start=0`** — global, lets any user bind any port. Minimal privilege increase (port numbers have no real meaning today), but requires a one-time sudo.
- **DNAT on the container bridge** — redirect 169.254.1.2:443 → 169.254.1.2:8443 via iptables/nftables, then run the broker on 8443. Requires sudo at deploy time.

If the default fails, the systemd journal will say `Failed to bind to port 443 (Permission denied)`. Switch to `--port 8443` in the unit's `ExecStart` and add the DNAT rule.

## Operations

```bash
# Status
systemctl --user status claude-oauth-broker
journalctl --user -u claude-oauth-broker -n 50 --no-pager

# Health check (also wired into `yolo doctor` via manifest.doctor_cmd)
yolo-claude-oauth-broker --self-check

# Regenerate CA/leaf (breaks all existing jails until they restart and re-read NODE_EXTRA_CA_CERTS)
yolo-claude-oauth-broker --force-init-ca
systemctl --user restart claude-oauth-broker
```

## Disable

Set `"enabled": false` in `~/.local/share/yolo-jail/modules/claude-oauth-broker/manifest.jsonc` (or `yolo modules disable claude-oauth-broker`) and stop the service:

```bash
systemctl --user disable --now claude-oauth-broker
```

The refresher (`claude-token-refresher`) remains a valid fallback — with the broker disabled, jails fall back to the single-writer refresher story and occasionally race.

## Interaction with the refresher

The broker and the refresher can coexist:

- Broker handles **real-time** refresh requests from jails (synchronous).
- Refresher runs on a timer and proactively keeps the shared file ahead of expiry (eager).

Running both is safe — the broker's flock serializes against itself, and the refresher's flock is separate but refreshes are idempotent at the file level. If you want broker-only, set `claude_token_refresher: false` in `~/.config/yolo-jail/config.jsonc`.

## Writing your own module

The schema lives in [`src/modules.py`](../../src/modules.py) — docstring at the top. A new module is a directory with:

- `manifest.jsonc` (required)
- `ca.crt` (optional, auto-trusted if present)
- systemd unit / launchd plist / whatever the daemon needs (the module owns its own lifecycle)

Drop it under `~/.local/share/yolo-jail/modules/<name>/`, make sure the manifest's `name` field matches the directory name, and it gets picked up at next `yolo run`. No core changes required.
