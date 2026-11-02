# claude-oauth-broker loophole

A contribution of the official **`claude-auth` pack** — `packs/claude-auth/pack.toml` declares
`[[loophole]] from = "loopholes/claude-oauth-broker"`, and `packs/claude` needs
`claude-auth`, so selecting `packs: ["claude"]` is still what installs it. It moved from the `claude`
pack to `claude-auth` on 2026-10-29
([`provider-enablement.md` OQ-PE5](../../../../docs/design/provider-enablement.md#OQ-PE5)). Serializes Claude OAuth refreshes so multi-jail setups don't burn the single-use
refresh token. See [`docs/reference/claude-oauth-interposition.md`](../../../../docs/reference/claude-oauth-interposition.md) for design rationale, [the operator triage below](#repeated-login-401s) for repeated `/login` prompts, [`userguide/guides/loopholes.md`](../../../../userguide/guides/loopholes.md) for the loophole system.

It was `bundled_loopholes/claude-oauth-broker/` until 2026-08-19, and it was the **last** inhabitant
of that channel: this move is what emptied and retired it
([`loophole-system.md`](../../../../docs/reference/loophole-system.md#the-claude-oauth-broker-a-contribution-of-the-claude-pack)).
Nothing about the loophole's own paths changed — same name, same `{state}` dir, same endpoint file —
so an upgrading host keeps its CA and every jail keeps trusting it.

## Architecture — two daemons per jail, one shared credentials file, no privileged ports

- **Host daemon** (`yolo internal daemon claude-oauth-broker`) — **one per jail**, started by the
  launch and stopped when the jail ends
  ([`host-daemon-ownership.md`](../../../../docs/design/host-daemon-ownership.md), HD-R1; it was a
  host-wide singleton until then). Every copy reads and writes the one shared credentials file and
  takes the refresh flock beside it before it spends a refresh token, which is what keeps two jails
  from racing. Binds `/tmp/yolo-front-<jail>-claude-oauth-broker.sock`; that socket is never exposed
  to jails. Its manifest declares `detach`, so a jail's end lets a refresh in flight finish and
  write its replacement token rather than killing it.
- **The framework front** — not a process of this loophole's at all. Because the manifest declares
  `publishes: "socket"`, yolo runs a `svcendpoint` loopback-TLS front for the jail over that
  socket: a `127.0.0.1` listener on a kernel-assigned port, serving a throwaway certificate whose
  private key never leaves the launching `yolo` process, splicing each authenticated connection into
  the jail's broker.

  It is also the attribution point: the front prepends yolo's **connection preamble**, carrying a
  host-asserted `jail_id`, so the broker's audit line names the jail and that name cannot be forged
  from inside one.

  A **per-jail relay** (`yolo internal daemon broker-relay`) used to do all of this. It is deleted
  ([`loophole-system.md`](../../../../docs/reference/loophole-system.md#the-per-jail-relay-is-gone)); the front is a goroutine
  in the launching process and there is no second host daemon to supervise, reap or leak.
- **Jail daemon** (`yolo-jaild oauth-terminator`) — supervised inside the jail at boot. Binds
  `127.0.0.1:443` in the container network namespace (unprivileged there), terminates TLS for
  `platform.claude.com` with a CA-signed leaf cert, and forwards refresh/proxy requests to the front,
  which it finds via
  `YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT=/run/yolo-services/claude-oauth-broker.endpoint`.

## The endpoint file is a credential

That file is one line — `<host:port> <base64 cert> <token>` — written **0600** into this jail's own host-services dir (`/tmp/yolo-host-services-<hash>/`, mounted at `/run/yolo-services/`). It is re-read **fresh on every dial**, so a front on a new port with a new certificate and a new token is picked up with no jail relaunch.

- The client trusts **exactly** the certificate in that file, via a dedicated root pool — not a CA, and specifically not this loophole's own CA ([#33](https://github.com/mschulkind-oss/yolo-jail/issues/33) is why).
- The **token** is what `0600` means on a port: reachability is not authorization. It is minted in the front's memory, per jail and per service, and compared in constant time.
- **There is no token environment variable, deliberately.** An env var is inherited by every child process the terminator spawns; a file is read at the moment of use by the one process that needs it. `YOLO_SERVICE_CLAUDE_OAUTH_BROKER_TOKEN` does not exist and no fallback reads one.
- Never copy an endpoint file between jails, and never paste one into a log or a bug report.

Failure layers in the jail daemon's log. The word *relay* survives in these strings and now names
the **front** — the layer split they encode is unchanged, and renaming them is a separate change:

| Message | Meaning |
|---|---|
| `relay unreachable — …` | the endpoint file is not published, or nothing is listening at the address it names — this jail's front is down |
| `relay auth rejected — …` | the endpoint file's token does not match the running front: the file is stale (a predecessor's file was left behind) |
| `host broker endpoint …: malformed endpoint file` | the file exists but is truncated or was written by an older yolo |
| `host broker unreachable through the relay …` / `host broker exited N` | the front answered but the singleton behind it failed |

Any `yolo` invocation against the jail re-runs the front and republishes the file.

## Activation

**Selecting the pack is the activation**, as it is for every loophole, and this one declares nothing
to grant: a jail-only claude user who loses it is not merely without a feature, they are running
unserialized single-use refresh-token races against Anthropic.

The manifest's `requires.command_on_path: claude` predicate is **gone** as of the pack move. It was a
host-side `exec.LookPath`, and it read false for exactly the user yolo exists for: someone who
installs claude *inside* the jail and never on the host. Selecting `packs: ["claude"]` is the
dependency it was approximating.

To disable it while keeping the claude pack, override from `yolo-jail.jsonc`:

```jsonc
{
  "loopholes": {
    "claude-oauth-broker": { "enabled": false }
  }
}
```

## Files

| File | Location | Reaches a jail? | Purpose |
|---|---|---|---|
| [`loophole.toml`](loophole.toml) | the `claude` pack's staged tree (read-only) | yes (`/etc/yolo-jail/loopholes/…`) | Loophole definition |
| `ca.crt` | state dir | **yes** | Root CA generated by `--init-ca`, valid 10 years. Trusted inside jails via `NODE_EXTRA_CA_CERTS`. |
| `server.crt`, `server.key` | state dir | **yes** | Leaf cert for `platform.claude.com`, issued by the CA. Used by the in-jail TLS terminator. |
| `cert.lock`, `refresh.lock` | state dir | no | The certificate-mint lock and the refresh flock. |

The CA's private key is never written to disk: `--init-ca` signs the leaf in memory and discards
the key. `ca.key`, `ca.srl`, `leaf.cnf` and `server.csr` are files an older, `openssl`-based broker
left behind; the broker removes them when it finds them, and a surviving `ca.key` makes it mint a
new CA ([why](../../../../docs/reference/claude-oauth-interposition.md)).

State dir: `~/.local/share/yolo-jail/state/claude-oauth-broker/` on the host. **Only the three files marked above are bind-mounted read-only** into a jail, each as a single file under `/var/lib/yolo-jail/loopholes/claude-oauth-broker/` — declared by the manifest's `state_files` key.

The whole state directory used to cross instead, which put the then on-disk `ca.key` inside every jail ([#33](https://github.com/mschulkind-oss/yolo-jail/issues/33)). A jail's agent runs as UID 0 by design, so the key's `0600` mode was no barrier. The narrowing is least-privilege: it does not fix an auth escalation (the CA signs certs; it is not a credential for the broker, the host, or anything upstream) but it removes a lateral-movement rung — a jail can no longer mint a leaf that a *sibling* jail's TLS clients would trust.

## Operations

```bash
# Prime CA/leaf state (idempotent; run once by `just deploy`)
yolo internal daemon claude-oauth-broker --init-ca

# Regenerate everything (breaks existing jails until they restart)
yolo internal daemon claude-oauth-broker --force-init-ca

# Self-check (also runs automatically via `yolo doctor` → manifest.doctor_cmd)
yolo internal daemon claude-oauth-broker --self-check

# Singleton host daemon log — ONE file for every jail on the machine, which is
# what "host-wide singleton" means. There are no per-jail relay logs any more.
tail -F ~/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log

# Jail daemon logs (from inside a jail)
cat ~/.local/state/yolo-jail-daemons/claude-oauth-broker.log
```

### Repeated `/login` 401s

The entry point when a jail prompts `Please run /login · API Error: 401` more often than about
once a month. A login itself lasts about four weeks however often it is refreshed
([why](../../../../docs/reference/claude-oauth-interposition.md#two-logins-each-lived-about-four-weeks-however-often-they-were-refreshed)),
so one prompt a month is expected. Everything below runs on the host unless it says otherwise.

1. **`yolo check`** (alias `yolo doctor`). Its Loopholes section carries this loophole's
   `--self-check` lines:

   | Line | Means | Do |
   | :--- | :--- | :--- |
   | `ca.crt and server.crt not yet generated` (or one of them) | The broker's state was never primed | `just deploy`, or `yolo internal daemon claude-oauth-broker --init-ca` |
   | `shared creds expired … ago` | Refreshes are not landing | `/login` from inside a jail, then read the broker log (step 2) for why |
   | `shared creds expire in …` | Under an hour left with no refresh landed; a healthy cadence keeps it above an hour | Watch it; if it reaches zero, treat it as expired |
   | `shared creds valid for …` | The credential is fine | Look at steps 3 and 4 |
   | `… does not exist — run Claude and /login first` | No login yet | `/login` from inside a jail |

   On a backend that does not start this broker (Apple Container), the expired line names that
   instead: Claude in the jail refreshes for itself, so check the jail's outbound network, not the
   broker.

2. **The broker itself:** `yolo broker status`, then `yolo broker logs -n 50`. Restart a dead or
   unresponsive broker with `yolo broker restart`; it is safe under running jails, and a request
   in flight at that instant fails once and succeeds on retry. The log has one
   `action=refresh|proxy` line per jail request, and a failed upstream refresh logs as
   `upstream <status> for rt=<fingerprint>: <body>`. A background-refresh failure logs as
   `bg_refresh: refresh failed error=<code>`.

3. **Server-side revocation.** An `upstream 400` whose body names `invalid_grant` means Anthropic
   revoked the refresh token, even if `expiresAt` is still in the future. The only fix is `/login`
   from inside a jail; the broker mirrors the new login into the shared file.

4. **The refresh never reached the broker.** Inside the jail, the terminator log
   (`~/.local/state/yolo-jail-daemons/claude-oauth-broker.log`) has a `request:` line per request
   with `is_refresh=true|false`. If it shows only `is_refresh=false` (the `/login` exchange) and
   the broker log is quiet, Claude in the jail is not sending refresh grants through the broker.
   Check the interception itself:
   - `getent hosts platform.claude.com` in the jail should answer `127.0.0.1`; without that host
     entry Claude reaches Anthropic directly and the broker is bypassed.
   - `NODE_EXTRA_CA_CERTS` in the jail should name the broker's `ca.crt`; otherwise the TLS
     handshake to the terminator fails.
   - The terminator must be running; its log above says whether it started.
   - Interception runs on podman only, and only with `YOLO_CLAUDE_CREDENTIAL_VIEW` off
     ([backends carrying the interception](../../../../docs/reference/claude-oauth-interposition.md#current-values)).

5. **Right after a laptop wakes.** A 401 in the first seconds after resume, before the network is
   back, is the one window the broker cannot close: the background refresher retries every 5 s
   for about a minute once the network is up. Retry the request; `/login` only if it persists.

## Refresh: on demand, and proactively

**On demand.** When a jail POSTs to `/v1/oauth/token`, the host daemon takes the flock, checks on-disk expiry, and either returns the cached tokens (still valid, ≥ 90 s headroom) or calls Anthropic once and rewrites the shared file.

**Proactively.** The daemon also runs a background refresher **by default** (`RunBackgroundRefresher`, started in `oauthbrokercmd.go` unless `--no-background-refresh` is passed). It ticks every `BackgroundRefreshTickSeconds` = 60 s and refreshes when the shared token is within `BackgroundRefreshLeadSeconds` = 1800 s (thirty minutes) of expiry. A tick that fails with `upstream_unreachable` while still due fast-retries every 5 s, up to 12 consecutive times — added for suspend/resume, where the token expires during sleep and the first post-wake tick fires before DNS is up.

The proactive loop is architectural, not an optimization: Claude Code has **no** proactive refresh of its own for Pro/Max tokens — it refreshes reactively, after a 401, and gives up after a bounded number of retries. A jail that idles past expiry, or a host that suspends through it, would otherwise wake up logged out. Why the loop stays, and what closes the other failure paths: [the background refresh loop stays on](../../../../docs/reference/claude-oauth-interposition.md#the-background-refresh-loop-stays-on).

(The legacy standalone `claude-token-refresher` **is** gone — the broker became the single refresh authority and absorbed the job. Earlier revisions of this file read that as "there is no background timer", which was never true of the Go daemon.)

The broker operates on ONE credentials file — the shared jail file at `~/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json` — and never touches host Claude's `~/.claude/.credentials.json`. Host and in-jail Claude maintain independent OAuth identities (separate refresh tokens minted by separate `/login` flows). Anthropic's OAuth issuer supports multiple concurrent active refresh tokens per account, so this is cheap and safe.

The earlier "mirror-if-identity-matches" behavior caused the 2026-04-23 `invalid_grant` incident: host Claude refreshed out-of-band via native OAuth, upstream invalidated the shared file's now-stale refresh token, and every in-jail request started failing. Two independent clients cannot safely share a single-use refresh token. Separate identities eliminate the whole failure mode.

If you used to rely on the shared identity, expect to run `/login` **once** on host (to re-establish its independent identity) after the fix lands.
