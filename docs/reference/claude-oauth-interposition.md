---
status: current
verified: 2026-09-21
verified_commit: 9018aa9c
covers:
  - internal/oauthterminator/
  - internal/oauthbroker/
  - internal/loopholes/runtime.go
  - internal/entrypoint/system.go
  - internal/entrypoint/packhooks.go
  - internal/cli/run/loopholesruntime.go
  - internal/cli/run/jaildaemondecline.go
  - internal/cli/run/loopholeinert.go
  - internal/cli/run/assemble_parts.go
  - packs/claude/loopholes/claude-oauth-broker/
  - packs/claude/pack.json
tags: [credentials, oauth, interception, broker, tls, claude]
summary: "What yolo interposes on for Claude OAuth and what it leaves alone: exactly one hostname is
  routed to loopback, exactly one grant on it is terminated, everything else on that host is proxied,
  and model traffic is never touched. Then the question the mechanism keeps provoking — why a
  credential FILE is involved at all — answered by enumerating every channel Claude Code 2.1.278 will
  accept a credential on and what each one cannot do."
---

# Claude OAuth interposition — one hostname, one grant, and why there is still a file

**Status:** CURRENT as of 2026-09-21, verified against `9018aa9c`.

> **In short.** yolo interposes on **one hostname** (`platform.claude.com`), and on that hostname it
> terminates **one grant** (`grant_type=refresh_token`) and proxies everything else. Model traffic to
> `api.anthropic.com` is never intercepted, proxied or rewritten — there is no man-in-the-middle on
> inference, and there never was one. The credential still lives in a **file** because the vendor owns
> the storage: Claude Code will only re-read a rotating, refreshable credential from its own
> credentials store, every other channel it accepts carries a bare frozen string, and an environment
> variable is fixed at exec time. The broker exists because yolo moved that file out from under the
> vendor's own locks, and it is **host-side** for backend independence rather than for one-ness.

Two things make this doc necessary rather than a restatement. First, the mechanism reads far more
invasive than it is, and the scope is checkable in one command. Second, "why is a file involved?" has a
long answer that lives in the vendor binary, not in this tree.

| Component | Lives in |
| :--- | :--- |
| The interception declaration | `packs/claude/loopholes/claude-oauth-broker/manifest.jsonc` (`intercepts`, `broker_ip`, `ca_cert`, `state_files`) |
| `--add-host` emission, state-file mounts, the CA env var | `internal/loopholes/runtime.go` (`runtimeArgsWith`) |
| The in-jail TLS terminator | `internal/oauthterminator` (`makeHandler`, `IsRefreshGrant`, `Refresh`, `ProxyUpstream`) |
| The host daemon: refresh, proxy, mirror | `internal/oauthbroker` (`DoRefresh`, `DoProxy`, `maybePropagateTokenResponse`) |
| The shared-credentials symlink | `internal/entrypoint/packhooks.go` (`linkSharedCredential`); `packs/claude/pack.json` |
| The OpenSSL-family CA bundle | `internal/entrypoint/system.go` (`GenerateCABundle`), `boot.go`, `shell.go` |
| Backend scope | `internal/loopholes/runtime.go` (`admitsJailSideEffects`); `internal/cli/run` (`startLoopholes`, `hostScopedEndpointIsUnpublishable`, `jaildaemondecline.go`) |

**Reads with:** [`agent-credentials.md`](agent-credentials.md) — the enumeration of every credential
channel across the jail boundary, of which this is one; it owns the broker's *rulings*, the 90/300
threshold defect and the per-backend credential table. This doc owns what is and is not on the wire.
[`../research/claude-oauth-refresh-mechanics.md`](../research/claude-oauth-refresh-mechanics.md) owns
the vendor's refresh state machine — the three failure paths, the dead-token set, why writing the disk
file is the right hook. This doc does not re-derive either; it records the *interposition surface* and
the channel enumeration behind "why a file".
[`loophole-transport.md`](loophole-transport.md) owns the endpoint file, the front and the wire format.

**Every vendor fact below is measured against Claude Code 2.1.278** and is marked where it appears.
Vendor internals are not a contract; a minor release can move any of them, and the byte offsets are
given so a re-measurement is cheap rather than as a claim that they are stable.

---

## What is intercepted, and what is not

**Model traffic is never intercepted.** Claude Code's inference endpoint is
`BASE_API_URL:"https://api.anthropic.com"` (2.1.278, bundle offset 189496381), and nothing in yolo
touches that hostname: no `--add-host`, no `/etc/hosts` entry, no proxy. Measured in this jail on
2026-09-21, `api.anthropic.com` resolves to the real internet (`160.79.104.10`), as do `claude.ai`,
`claude.com` and `console.anthropic.com`. The broker's effect on model calls is entirely indirect — it
keeps the access token in the credentials file fresh and un-burnt, and Claude reads the bearer from
that file itself.

yolo declares exactly **one** interception, and it is checkable in one command:

```console
$ rg -n '"intercepts"' packs/ internal/ --glob '!*_test.go'
```

That returns the manifest declaration at
[`manifest.jsonc:68-70`](../../packs/claude/loopholes/claude-oauth-broker/manifest.jsonc) —
`"intercepts": [{"host": "platform.claude.com"}]`, a one-element list — plus the schema key at
[`keys.go:30`](../../internal/loopholedecl/keys.go). No other shipped loophole manifest declares the
key at all. The routing is emitted from that list alone, with the manifest's `broker_ip`
(`manifest.jsonc:74`, `127.0.0.1`):

```go
// internal/loopholes/runtime.go:328-330
for _, intercept := range m.Intercepts {
    args = append(args, "--add-host", intercept.Host+":"+m.BrokerIP)
}
```

Live, in this jail: `/etc/hosts` carries `127.0.0.1 platform.claude.com` plus the ordinary
`localhost`/`host.containers.internal` lines and nothing else, and `getaddrinfo` confirms
`platform.claude.com -> 127.0.0.1` while the four hostnames above go to the internet.

> [!NOTE]
> **State the scope as a declaration count, not as a string absence.** The literal text
> `api.anthropic.com` *does* occur in this tree — in comments
> ([`packs/claude/derive.lua:167`](../../packs/claude/derive.lua)) and in test fixtures — so
> "it appears nowhere" is false and will drift further. The load-bearing and re-verifiable claim is
> the one above: **one `intercepts` entry, for `platform.claude.com`**, and the OAuth token endpoint
> is the only credential hop on that host.

The vendor speaks to four hostnames, and yolo interposes on one of them. From the same constants block
(2.1.278, offset 189496381): `api.anthropic.com` carries inference plus
`/api/oauth/claude_cli/create_api_key`, `/api/oauth/claude_cli/roles` and the WebFetch domain-info
lookup; `claude.com` carries the subscription authorize URL; `claude.ai` is the origin;
`platform.claude.com` carries `TOKEN_URL`, the console authorize URL and the manual redirect
callback. Only the last is routed to loopback.

**Interception is DNS-wide; service is port-443-only.** The terminator binds one port —
`host` defaults to `127.0.0.1` and `port` to `443`
([`oauthterminatorcmd.go:36-37`](../../internal/oauthterminator/oauthterminatorcmd.go)) — so plain
HTTP to the intercepted name is a hard connection refusal inside a jail rather than a pass-through.
Measured 2026-09-21: `curl http://platform.claude.com/robots.txt` fails with
`Failed to connect to platform.claude.com:80`, and `ss -ltn` shows a single listener on
`127.0.0.1:443` for this loophole.

## What is terminated, and what is proxied

Within that one host, the split is one grant wide. The terminator classifies, then dispatches:

```go
// internal/oauthterminator/oauthterminatorcmd.go:109-110
isToken := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/oauth/token")
isRefresh := isToken && IsRefreshGrant(body)
```

`isRefresh` takes `Refresh(hostEndpoint)`; everything else takes
`ProxyUpstream(hostEndpoint, r.Method, r.URL.RequestURI(), flattenHeaders(r.Header), body)`
(`oauthterminatorcmd.go:120-127`).
[`IsRefreshGrant`](../../internal/oauthterminator/handler.go) (`handler.go:13-31`) returns true only
for a JSON **object** whose `grant_type` is exactly `"refresh_token"`; its docstring names the excluded
cases — *"Anything else (authorization_code from /login, unparseable, empty) is proxied untouched."*

**The refresh branch forwards nothing.** `Refresh` sends one frame and no body:

```go
// internal/oauthterminator/handler.go:107-108
func Refresh(endpointPath string) ProxyResult {
    resp, err := AskHostBroker(endpointPath, singleton("action", "refresh"))
```

The discard is visible at the call site: `body` is read once (`oauthterminatorcmd.go:106`) and passed
**only** on the non-refresh branch (`:124`). Host-side, `DoRefresh(credsPath)`
([`refresh.go:48`](../../internal/oauthbroker/refresh.go)) takes only a path and reads the refresh
token out of the shared file itself (`refresh.go:72`), under an exclusive `flock`
(`refresh.go:17-39`, taken at `:54`). So the refresh token Claude presents never leaves the jail edge,
and a jail cannot spend a stale one — the token it presents is not an input to anything.

**The proxy branch makes no outbound connection from the jail.** `ProxyUpstream`
(`handler.go:45-103`) builds a `{action:"proxy", method, path, headers, body_b64}` frame and hands it
to `AskHostBroker`, which dials **this jail's endpoint file** —
`svcendpoint.Dial(endpointPath, 30*time.Second)`
([`client.go:62-63`](../../internal/oauthterminator/client.go)) at the path named by
`BrokerEndpointEnv` (`client.go:41`; live value
`YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT=/run/yolo-services/claude-oauth-broker.endpoint`). Egress
happens host-side: the daemon routes `action=proxy` to `DoProxy`
([`handler.go:102-111`](../../internal/oauthbroker/handler.go)), which builds the destination as

```go
// internal/oauthbroker/http.go:141
url := "https://" + UpstreamHost + path
```

with `UpstreamHost` a constant — `platform.claude.com`
([`oauthbroker.go:39`](../../internal/oauthbroker/oauthbroker.go)). Headers are forwarded minus
`hopByHop` (`http.go:40-44`), `Accept-Encoding` is forced to `identity` (`:175`), the caller's UA is
preserved or replaced with `yolo-jail-oauth-broker` (`:144-155`, and `:29-35` for why a generic UA is
refused by Cloudflare), and the reply returns as `{status, headers, body_b64}`. On the way back out the
terminator drops `Content-Length`, writes header names verbatim and adds `Connection: close`
(`oauthterminatorcmd.go:144-167`).

> [!WARNING]
> **The listener is not name-scoped; the destination is fixed.** The terminator inspects neither SNI
> nor `Host` — anything that completes TLS on `127.0.0.1:443` is proxied to `platform.claude.com`.
> Measured 2026-09-21 from this jail: `curl https://localhost/robots.txt` returned HTTP 200 with
> platform.claude.com's robots.txt (the leaf's SAN includes `localhost` —
> [`cert.go:258-264`](../../internal/oauthbroker/cert.go)), and adding `-H 'Host: example.com'`
> returned the same bytes. So the loophole README's *"terminates TLS for `platform.claude.com`"*
> ([`README.md:35-36`](../../packs/claude/loopholes/claude-oauth-broker/README.md)) describes an
> intent, not a check: `intercepts` is a DNS-routing declaration and nothing enforces it at the
> listener. The upside is the shape this gives — a **fixed single-host egress**, not an open proxy.
> The `Host` header cannot redirect it: net/http moves `Host` out of `r.Header`, and `hopByHop`
> strips `host` anyway (`http.go:41`).

### How the handshake is trusted

Interception only works because the jail trusts a yolo-minted leaf, and that trust arrives through two
independent paths.

- **The anchor crosses as a narrowed per-file mount.** The manifest declares
  `"ca_cert": "{state}/ca.crt"` (`manifest.jsonc:80`) and
  `"state_files": ["ca.crt", "server.crt", "server.key"]` (`:86`); `runtime.go:341-358` emits one
  `-v <src>:/var/lib/yolo-jail/loopholes/<name>/<rel>:ro` per declared file and `:366-386` collects
  the CA path. The CA's **private** key stays host-side — signing is `cert.go:33-47`'s host-only job.
  Verified live: that directory holds `ca.crt`, `server.crt` and `server.key` (mode `0600`) and
  nothing else. The served leaf is `CN=platform.claude.com` with
  `SAN DNS:platform.claude.com, DNS:localhost`, issued by
  `O=yolo-jail, OU=local, CN=yolo-jail-claude-oauth-broker`.
- **Node/Bun clients** get it through `NODE_EXTRA_CA_CERTS` on the container argv
  (`runtime.go:419-421`). The vendor really reads it: 2.1.278's `node:tls` compat layer (offset
  22158845) does `if (process.env.NODE_EXTRA_CA_CERTS) { let extra = cacheExtraCACertificates(); … }`
  and pushes the result onto its bundled roots — which is why `axios/1.15.2` completes a handshake
  against a self-signed leaf.
- **The OpenSSL family** gets a concatenated bundle. `GenerateCABundle`
  ([`system.go:16-62`](../../internal/entrypoint/system.go)) joins the image baseline `$SSL_CERT_FILE`
  ([`flake.nix:1542`](../../flake.nix), nixpkgs cacert) with every de-duplicated
  `NODE_EXTRA_CA_CERTS` path into `$HOME/.yolo-ca-bundle.crt`; `boot.go:601-608` exports
  `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE` and `GIT_SSL_CAINFO` at it before any child
  spawns, and `shell.go:231-236` re-exports the same four from `.bashrc`. The file is per-workspace
  state, bound from the workspace ([`assemble_parts.go:154`](../../internal/cli/run/assemble_parts.go)).

> [!WARNING]
> **`NODE_EXTRA_CA_CERTS` is a single path, not a list — and yolo joins it like a list.**
> `runtime.go:420` joins multiple CA paths with `os.PathListSeparator`, but the consumer treats the
> whole value as one filename: 2.1.278's `maybeWarnAboutExtraCACerts` (offset 22159500) calls
> `accessSync(extraPath)` on the raw variable and, on failure, prints
> ``Warning: Ignoring extra certs from `${extraPath}`, load failed: …``. The moment a second selected
> loophole declares a `ca_cert`, the joined value becomes unopenable and **both** CAs are silently
> dropped for every Node/Bun client, leaving only the OpenSSL-family bundle (which does concatenate
> correctly) working. Only one shipped manifest declares a `ca_cert` today, so the bug is unreachable
> and untested.

Two further notes for a threat model. The anchor is read **once per process** —
`NODE_EXTRA_CA_CERTS` is in 2.1.278's startup-env list and `cacheExtraCACertificates` memoizes — so a
CA re-minted while Claude runs is not picked up until restart. And **DNS-plus-a-CA is not a stylistic
choice**: the vendor offers no supported way to repoint `TOKEN_URL`. `CLAUDE_CODE_CUSTOM_OAUTH_URL` is
checked against a hardcoded allowlist and a non-member throws
`CLAUDE_CODE_CUSTOM_OAUTH_URL is not an approved endpoint.` (offset 189498422), and the
`CLAUDE_LOCAL_OAUTH_*` variables only select a localhost dev config.

### The `/login` flow is not terminated — it is the enrollment path

A login spans four hosts and only one is intercepted. The authorize hop goes to
`CLAUDE_AI_AUTHORIZE_URL` on `claude.com` for a subscription login (2.1.278, offset 192044025) —
unintercepted, not even in `/etc/hosts`. The **token exchange** posts
`{grant_type:"authorization_code", code, redirect_uri, client_id, code_verifier, state}` to
`TOKEN_URL` (offset 192044595), which *is* intercepted — and `IsRefreshGrant` returns false for it,
so it is **proxied**. Measured twice in one jail's terminator log, 2026-08-05 and 2026-09-04, both as
`POST /v1/oauth/token body_len=325 is_refresh=false ua='axios/1.15.2'` → `-> 200 body_len=695`. The
tail (`create_api_key`, `roles`) is on `api.anthropic.com` and unintercepted.

That proxied 200 is what **seeds** the shared credentials file. `maybePropagateTokenResponse`
(`oauthbroker/handler.go:177-197`) gates on `POST`, the `/v1/oauth/token` path prefix, no `{error}`,
status 200, the Claude Code client id (`:194-196`; the constant at `oauthbroker.go:41` is byte-identical
to the vendor's `CLIENT_ID`) and inference scopes (`:221-229`), then `propagate` (`:241-263`) writes
under the same `flock` a refresh takes. Both logins are visible host-side as
`proxy mirror: wrote shared creds` — the 2026-08-05 line records the first-ever seeding
(`rt (none) -> …`). So `/login` is not merely tolerated by the interception; it is how a machine
enrolls, and the 2026-09-04 pair shows the intended recovery end to end: a refresh fails with
`invalid_grant`, a human re-runs `/login`, the authorization-code exchange is proxied, and the mirror
re-seeds the shared file.

One consequence for anyone debugging a jail login: the browser hop **cannot close inside a jail**. The
vendor's `redirect_uri` is `http://localhost:${port}/callback`, which names the *jail's* loopback — a
host browser in the default bridge mode cannot reach it — so a jail login must take the manual-paste
path through `platform.claude.com/oauth/code/callback` in the human's host browser, where `/etc/hosts`
is unmodified and nothing is intercepted.

### The documentation collision

`platform.claude.com` serves both the token endpoint and the public docs site, so yolo cannot
intercept the credential hop without interposing on ordinary doc reads. This is routine, not exotic —
the vendor bundle points Claude at `platform.claude.com/llms.txt` and several `/docs/en/…` pages. One
jail's terminator log has `GET /docs/en/agent-sdk/overview` and siblings proxied at 200 with the
WebFetch UA (`Claude-User (claude-code/2.1.220; …)`), and a live `curl
https://platform.claude.com/robots.txt` on 2026-09-21 returned 200 with genuine Cloudflare response
headers. Four consequences, each measured or code-certain:

- **Doc fetches are logged by path**, jail-side and host-side, and the host line carries a
  host-asserted `jail_id`.
- **They are coupled to broker health.** When the front was down, `GET /api/oauth/whoami` returned
  `502` with `proxy failed: relay unreachable` (jail log, 2026-08-17) — a dead front breaks unrelated
  reads of that host.
- **Whole bodies are base64'd through a framed exchange** with a 30s deadline on each side
  (`client.go:93`, `http.go:22-27`). A ~300 KB doc fetch is fine; the ceiling is real.
- **Duplicate response headers collapse to the last value** (`http.go:190-194`, which names multiple
  `Set-Cookie` explicitly), so a cookie-driven flow on that host would break in a way a single
  robots.txt fetch cannot reveal.

Non-Claude clients are caught too: the same log has `GET /v2/` probes with
`ua='containers/5.39.2 (github.com/containers/image)'` — podman registry lookups against the
intercepted name, answered 404.

### Two fragilities in the split, and one missing check

> [!WARNING]
> **`IsRefreshGrant` parses JSON only.** 2.1.278's first-party refresh sends JSON —
> `O={grant_type:"refresh_token",refresh_token:e,client_id:…}` with
> `headers:{"Content-Type":"application/json"}` (offset 192045344) — but the same binary already has a
> **form-encoded** refresh path for gateway auth (offset 192069853,
> `new URLSearchParams({grant_type:"refresh_token",…})` with `application/x-www-form-urlencoded`). If
> the first-party refresh ever switched encodings, `IsRefreshGrant` returns false, the grant is
> **proxied**, and the jail's own refresh token goes upstream unserialized — the exact race the
> loophole exists to prevent, with no error anywhere. The proxy mirror would still write the result,
> so it would half-work.

The path test is a **prefix** and not an equality (`oauthterminatorcmd.go:109`), so
`/v1/oauth/tokenfoo` also counts as the token endpoint. Harmless today; worth knowing it is not a
match on the endpoint.

> [!WARNING]
> **The refresh endpoint authenticates nothing.** The presented token is discarded and no caller check
> exists (`oauthterminatorcmd.go:104-130` reads the body and dispatches; nothing inspects identity), so
> any process in the jail can POST `{"grant_type":"refresh_token"}` to
> `https://localhost/v1/oauth/token` and receive the machine-wide `access_token` **and**
> `refresh_token` in the 200 body. In the default configuration this grants nothing new — the agent
> runs as UID 0 and `~/.claude/.credentials.json` is a live symlink to the shared file — but it is a
> second, **file-independent** read of the credential that would survive un-mounting the file. "The
> file is the only channel" is a fact about the **vendor**, not about yolo's own surface.

## Why there is a file on disk at all

This is the question the mechanism keeps provoking, and the short answer is that **the vendor owns the
storage**. Claude Code will accept a credential on several channels, but the credential yolo needs to
share is a *rotating* one, and an environment variable is fixed at exec time. The table below is every
channel 2.1.278 will take a credential on, and what each one cannot do.

| Channel (2.1.278) | What it carries | Why it cannot be yolo's channel |
| :--- | :--- | :--- |
| `.credentials.json` — the credentials store | `accessToken`, `refreshToken`, `expiresAt`, `scopes` | Nothing: **this is the one.** It is re-read after an external change, and the check follows a symlink |
| `CLAUDE_CODE_OAUTH_TOKEN` | one static access token, `refreshToken:null` | Frozen at exec, and it **vetoes** the file's rotation for that session |
| `CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR` and its three siblings | one string, read once | An inherited descriptor is drained to EOF, the read is memoized, and the variable is then deleted |
| `/home/claude/.claude/remote/.oauth_token` | one string | Anthropic's own remote-runner handoff, read through the same memo; its rotation story is process replacement |
| `CLAUDE_BG_AUTH_SNAPSHOT_PATH` | one credential snapshot | Consumed once and `unlink`ed; and it **refuses a symlink** unless the session is host-managed |
| SDK control protocol (`oauth_token_refresh`) | a fresh access token, on demand | The only genuine in-process rotation channel — and it requires yolo to *become* the agent host |
| `CLAUDE_CODE_HOST_CREDS_FILE` | an env map with expiry and writer liveness | The vendor's own host-owned rotating channel — **and it is still a file**; two blockers below |
| `apiKeyHelper` | a bare string, re-invoked on a TTL | It *does* rotate. The objection is **shape and precedence**, not rotation |
| `CLAUDE_CODE_OAUTH_REFRESH_TOKEN` | a refresh token, at `claude login` only | A **bootstrap** channel that terminates in the file |
| `ANTHROPIC_UNIX_SOCKET` + a placeholder token | no credential at all | Works, and is the rejected alternative — see [below](#the-rejected-alternative-no-credential-in-the-jail-at-all) |

Read down that column and two narrow claims survive, both of which hold:

1. **The credentials file is the only channel that re-delivers a credential to a plain interactive
   `claude`** — one that is not an SDK subprocess with a control-protocol host on stdio, and whose
   provider is not host-managed.
2. **The credentials store is the only channel that can carry a *refreshable* credential** —
   `accessToken` + `refreshToken` + `expiresAt` + `scopes` together. Every other channel carries a
   bare string, so the process holding it can be handed a replacement but can never mint one.

The mechanical confirmation is on the write path: the vendor **refuses to persist a credential it
cannot refresh**. `if(!e.refreshToken||!e.expiresAt) return i("tengu_oauth_tokens_inference_only",{}),
{success:!0}` (offset 192105507). So an injected static token can never bootstrap the shared file, no
matter how it arrives.

The detail worth the most is the one that answers the design question directly: **Anthropic, solving
exactly yolo's problem for its own host-managed sessions, also chose a file.**
`CLAUDE_CODE_HOST_CREDS_FILE` is rejected unless absolute, capped at 65536 bytes, refused for
group/other-readable mode or the wrong owner, schema'd as `{env, expiresAt, pid, procStart}`, and
re-read on a 401 through an installed refresh callback — a `0600`, uid-checked, expiry-stamped,
liveness-stamped **file**, with a re-read bolted onto 401. Two things block reusing it from a jail:
`isProcessRunning(pid)` plus a ±2s start-time match are evaluated in the **reader's** PID namespace,
so a host pid cannot be vouched for from inside a container; and it demands
`CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST`, which also strips the session's settings-level provider
routing.

Three channels deserve their corrections spelled out, because each has been described wrongly:

- **`CLAUDE_CODE_OAUTH_TOKEN` does not merely fail to rotate — it actively vetoes rotation.** On a
  401, `Boolean(a.CLAUDE_CODE_OAUTH_TOKEN) && !…` makes the CLI *refuse* to adopt the stored
  credential: *"OAuth 401: keeping the user-supplied CLAUDE_CODE_OAUTH_TOKEN instead of adopting the
  stored credential. Mint a fresh token with `claude setup-token` and restart with it, or unset the
  variable and run /login."* A jail launched with that variable would ignore every refresh the broker
  writes, and the only exit is a process restart. The vendor even warns about it at `/login` time.
- **`apiKeyHelper` rotates.** Its invocation is TTL-cached per host with JWT-expiry awareness, and its
  output becomes a **bearer**, not only an api-key header. So "it cannot carry a rotating credential"
  is wrong. The real objection is that a helper returns a bare string — no `refreshToken`,
  `expiresAt` or `scopes` travel with it and nothing persists it — and that naming one changes which
  auth source **wins**, routing the session around the claude.ai OAuth branch entirely. A helper can
  keep a jail's bearer fresh only by taking the session off the stored credential, which is the thing
  yolo is sharing. One experiment is unrun and would settle whether that door is really closed:
  whether a helper-supplied claude.ai access token is accepted on `/v1/messages`, since the OAuth
  branch pairs the bearer with `anthropic-beta: oauth-2025-04-20` and the helper branch does not.
- **The FD channel degenerates into a file by design.** Having drained the descriptor, the vendor
  *persists* the value to a well-known path for children — *"Persisted ${r} to ${e} for subprocess
  access"*. The vendor's own answer to "the child needs this too" is a file.

### The SDK control protocol — the one genuine alternative, and what it costs

There *is* an in-process rotation channel, and it is out of reach for a plain jail. It is gated on
`Boolean(a.CLAUDE_CODE_SDK_HAS_OAUTH_REFRESH) && pio.has(a.CLAUDE_CODE_ENTRYPOINT ?? "")` where `pio`
is `new Set(["claude-desktop","local-agent","claude-vscode"])` (offset 192081833). The variable is set
by the SDK host, not by a user, and the host must supply a `getOAuthToken` callback; delivery is a
`{subtype:"oauth_token_refresh"}` message on the stdio control channel, and the CLI installs the answer
by writing its own `process.env`. To use it, yolo would have to **become the agent host** — run
`claude` as an SDK subprocess speaking stream-json on stdio and claim one of three entrypoint
identities — instead of being the environment a normal interactive `claude` runs in. That is a
different product, and it would cost the interactive TUI.

### The rejected alternative: no credential in the jail at all

The sharpest answer to *"why don't we just proxy everything?"* is that **the vendor already built that
door.** `ANTHROPIC_UNIX_SOCKET` routes Anthropic API traffic over a unix socket
(`if(e.forAnthropicAPI){let i=a.ANTHROPIC_UNIX_SOCKET;if(i)return{...o,unix:i}}`, offset 190912537),
and the login-required check is satisfied by a literal placeholder — `var z_e="ssh-placeholder"`
(offset 190970207) — which the env scrubber deliberately passes to children. Such a session is
classed as host-managed. It needs **no TLS interception and no CA**: one unix socket, a placeholder
token, and zero credential bytes inside the jail.

It is recorded here as a **rejected alternative with a real cost**, not as something the vendor
forbids: every model request, streaming included, would traverse a host proxy that yolo would then own
and have to keep correct — which is precisely the man-in-the-middle on inference that today's design
does not have.

### Where the file actually is, and how Claude finds it

The canonical location is `CLAUDE_SECURESTORAGE_CONFIG_DIR ?? CLAUDE_CONFIG_DIR ?? ~/.claude`, joined
with `.credentials.json` (`function Wb()`, offset 191037919). **yolo sets neither variable** and
reaches the shared file by symlink instead: `packs/claude/pack.json:159-175` declares `.claude` as
`{"kind":"state","scope":"workspace"}` and `.claude-shared-credentials` as `scope: "machine"`, joined
by the `shared_credentials` hook, which
[`packhooks.go:106-139`](../../internal/entrypoint/packhooks.go) renders as a **relative** symlink
(`filepath.Rel` at `:132`) into a directory the pack declared shared (`:111`). Live in this jail:
`~/.claude/.credentials.json -> ../.claude-shared-credentials/.credentials.json`. The broker's own
default target is the same file under the global home
([`oauthbrokercmd.go:15-21`](../../internal/oauthbroker/oauthbrokercmd.go)).

The re-read that makes an external write visible is an **mtime sentinel** that resolves symlinks:
`let{mtimeMs:r}=await iE(lE(Wb(),".credentials.json")); if(r!==e.lastCredentialsMtimeMs) …` (offset
192110644), where `iE` is `fs/promises.stat` — not `lstat`. On a stat failure it falls back to
comparing the stored `accessToken`. This is the same sentinel
[`claude-oauth-refresh-mechanics.md:236-262`](../research/claude-oauth-refresh-mechanics.md) documents
under its old minified name; it survived into 2.1.278 unchanged in substance.

> [!NOTE]
> **One correction to that research doc.** Its
> [§5](../research/claude-oauth-refresh-mechanics.md#5-why-writing-the-disk-file-is-the-right-hook)
> (`:310-352`) says the disk-adopting branch of the
> 401 handler *"Runs on every 401"* and *"Adopts whatever the disk has"*. In 2.1.278 there are **two**
> branches: the `CLAUDE_CODE_OAUTH_TOKEN`/FD disk-adoption block sits inside `if(!h?.refreshToken)`
> and therefore only fires for injected static tokens (telemetry
> `tengu_oauth_401_recovered_from_disk`), while a normal claude.ai login adopts disk through an
> `h.accessToken !== e` comparison instead (`tengu_oauth_401_recovered_from_keychain`). The doc's
> conclusion holds; its mechanism sentence no longer does.

> [!WARNING]
> **The standing risk: a symlink-refusing store is already shipped, just not constructed.** The live
> backend reads the credential with a plain `readFileSync`, so yolo's symlink is safe today. But the
> same binary contains a store that opens with `O_RDONLY|O_NOFOLLOW`, maps `ELOOP` to
> `{kind:"refused-symlink"}`, and `lstat`s the path — returning `{state:"read-failed",code:"ELOOP"}`
> for a symlink. It is unreachable only because nothing constructs it:
> `function Qkn(){if(!F())return;return}` — exported as `tryCreateV5Backend` — returns `undefined`
> unconditionally (offset 200212086), behind a server-side gate. **The day that returns a real
> backend, a symlinked `.credentials.json` reads as absent and every jail sees "please run /login"**
> — flipped by a vendor gate, not by a yolo change. The mitigation is already identified: point
> `CLAUDE_SECURESTORAGE_CONFIG_DIR` at the machine-scope directory so the real file sits where Claude
> opens it, with no symlink in the credential path. The trade is that it moves the whole
> secure-storage directory rather than that one file, and the keychain service name is derived from
> that directory too.

## Why a broker exists

**We shared the file and not the vendor's locks** — and there are two of them, not one.

The vendor **co-locates** the lock and the credential in one directory. The credential is
`join(Wb(), ".credentials.json")`; the refresh lock is

```js
// Claude Code 2.1.278, offset 192117058
function Jar(e,n){return{lockfilePath:lE(e,".oauth_refresh.lock"),realpath:!1,stale:60000,update:5000,…}}
```

with the directory argument being `Wb()` — the config dir. yolo redirects only the **leaf** out of
that directory, which is exactly what splits the pair: the credentials file becomes machine-scope while
both locks stay per-workspace. The broker is what puts serialization back, host-side.

> [!IMPORTANT]
> **Two corrections to [`agent-credentials.md`](agent-credentials.md)'s account of that lock**, both
> about characterization rather than conclusion.
>
> First, `realpath:!1` is **not** the load-bearing character
> ([`agent-credentials.md:245-246`](agent-credentials.md)). The bundled locker is proper-lockfile, and
> two of its functions settle it: `function ne(e,r){return r.lockfilePath||`${e}.lock`}` — an explicit
> `lockfilePath` short-circuits, so the option cannot influence the lock path — and
> `function Pe(e,r,n){if(!r.realpath)return n(null,bt.resolve(e));r.fs.realpath(e,n)}`, where
> `realpath` resolves the **target** argument, here the config *directory*. With the default
> `realpath:!0` the lock path would be byte-identical. A third, independent reason: the credentials
> path is not an input to the lock path at all — yolo symlinks the leaf while the lock is a *sibling*
> named `.oauth_refresh.lock`, so there is no symlink on the lock path to follow. What is load-bearing
> is `lockfilePath: lE(e, ".oauth_refresh.lock")` — **the lock is derived from the config directory**,
> which `agent-credentials.md:236-237` already says correctly one sentence earlier.
>
> Second, the vendor does **not** "disable symlink resolution outright"
> ([`agent-credentials.md:237-238`](agent-credentials.md)). It takes **two** locks, and the second one
> applies `realpath`: `y=`${await RTe(e).catch(()=>e)}.lock`` (offset 192117521), where `RTe` is
> `fs/promises.realpath`. `ELOCKED` on that legacy lock throws after releasing the first, under
> telemetry `tengu_oauth_refresh_legacy_lock_contended`. The conclusion is unaffected — it realpaths
> the *config dir*, which yolo keeps per-workspace, so that lock is per-jail too — but both locks
> derive from the config directory, one by `join` and one by `realpath`.

There is also a **second per-jail lock on the write path**, which the broker does not replace. Every
read-modify-write of the credential goes through `<configDir>/.storage-write` (offset 191041435,
`realpath:!1`, ten retries, 15s stale), derived from the same `Wb()` and therefore equally
per-workspace under yolo's scoping, while the file it protects is the machine-scoped one.

> [!WARNING]
> **A residual, not a measured failure: two writer classes, one of which takes no yolo lock.** The
> broker serializes its own writes against each other — `refresh.go:54` and
> `oauthbroker/handler.go:249` are the only non-test `WriteTokens` callers and both are inside
> `withRefreshLock` — and `WriteTokens` uses tmp+rename precisely because *"in-jail readers take no
> flock; an in-place O_TRUNC would expose a torn file"*
> ([`oauthbroker.go:295-300`](../../internal/oauthbroker/oauthbroker.go)). That prevents a **torn
> read**. It does not prevent a **lost update** between a broker write and an in-jail Claude write
> taken under `.storage-write` — a `/login` write racing the proxy mirror, for instance. No resulting
> failure has been measured; it is recorded because "we shared the file and not the lock" is true of
> *two* vendor locks and the broker replaces one.

Finally, the reason the broker is needed at all is upstream, not local. The vendor's post-refresh save
is a **compare-and-swap** with up to three attempts, and *"a sibling won"* is a designed, named
outcome (`tengu_oauth_refresh_save_adopted_newer_write`, returning `"adopted_sibling"`). So the file's
*consistency* is already the vendor's own cross-process business. What the CAS cannot undo is that each
losing jail has already **spent** a single-use refresh token upstream before the CAS runs. That is the
race, and it is a property of Anthropic's service rather than of yolo's architecture.

## Why it is host-side

The property the broker is bought for is **backend independence**, not one-ness — and not a credential
boundary.

**The flock does not need a singleton.** `RefreshLockPath` is set once, to `refresh.lock` under
`BrokerDir()` ([`oauthbrokercmd.go:80`](../../internal/oauthbroker/oauthbrokercmd.go)), and
`BrokerDir()` ([`cert.go:22-31`](../../internal/oauthbroker/cert.go)) takes **no argument** — not the
loophole name, not the socket path, not the pid, not the creds file. So N brokers in one home contend
on the same inode however their sockets are spelled. Two supporting properties, checked rather than
assumed: every broker-side write is inside that lock (above), and the **loser re-reads from disk with
no memoization** — `cachedAbove` calls `oauthFromCreds`, which is `os.ReadFile`
(`oauthbroker.go:207-219` → `:122-127`) — so it returns the winner's token. The lock inode is stable:
`withRefreshLock` opens `O_WRONLY|O_CREATE|O_TRUNC` and never unlinks, and `WriteTokens`' tmp+rename
targets a different directory.

> [!NOTE]
> **Two source comments overstate this.** `cert.go:51-53` and
> [`singletondeps_test.go:32-34`](../../internal/broker/singletondeps_test.go) both say `BrokerDir()`
> *"is a function of `$HOME` alone"*; `cert.go:23` checks `YOLO_BROKER_STATE_DIR` first. That
> override has no production writer, so the conclusion holds —
> [`host-daemon-ownership.md:121`](../design/host-daemon-ownership.md) already carries the right
> phrasing, *"(or the test-only `YOLO_BROKER_STATE_DIR`)"*, and copying it into those two comments is
> the fix. One residual caveat the phrase "one home" quietly assumes: `flock` over a network home is
> not reliably coherent.

**Backend dependence is what host-side buys, and it rests on one unverified backend.**
[`agent-credentials.md:250-256`](agent-credentials.md) argues that machine-scoping the lock directory
would work "on podman … and only there", citing Apple Container *and* `macos-user`. The `macos-user`
half does not follow: that backend has **no bind mounts at all**
([`macos.md:556`](../guides/macos.md)), which means every sandbox writes the **real** host filesystem
under the **host** kernel — `HOME` never moves, so the `scope: machine` directory never moves either
and the hook's output is byte-identical there
([`macos-user-nix-and-features.md:545`](macos-user-nix-and-features.md)), while each `scope: workspace`
directory is a symlink into `<workspace>/.yolo/home` (`:305-310`). Every sandbox would therefore open
the same inode and `flock` would contend correctly — arguably more reliably than podman, with no mount
in between; `macos.md:562` independently notes that reaching these needs no bind mount, *"which is why
this backend can carry them at all"*. The Apple Container half stands — one lightweight VM per
container ([`macos.md:52`](../guides/macos.md)), so a lock is guest kernel state over virtiofs — and
that doc already marks the cross-VM reasoning architectural and **UNVERIFIED**. So the conclusion
survives with one backend behind it rather than two, and it should be stated that way.

### Where the interposition exists at all

**Interception is a podman/Linux mechanism, and both macOS backends are uncovered.**

- **Apple Container** drops the record **whole**: `admitsJailSideEffects` ends with
  `return !(runtime == "container" && len(m.Intercepts) > 0)`
  ([`runtime.go:281`](../../internal/loopholes/runtime.go)), so there is no `--add-host`, no `:ro`
  module mount, no `state_files` and no jail-daemon payload entry. `startLoopholes` narrows the
  host-daemon allow list to the OpenAI service on that runtime
  ([`loopholesruntime.go:170-175`](../../internal/cli/run/loopholesruntime.go)) and
  `hostScopedEndpointIsUnpublishable` suppresses the endpoint variable for every other host-scoped
  loophole ([`assemble_parts.go:612-617`](../../internal/cli/run/assemble_parts.go)).
- **`macos-user`** declines the terminator **permanently**, by name, at every launch: its default port
  is 443 and its interception needs an `--add-host` this backend cannot emit, and `yolo-jaild` is not
  built for darwin at all ([`jaildaemondecline.go:36-43`](../../internal/cli/run/jaildaemondecline.go)).

On both, the agent talks straight to `platform.claude.com` with its own file-based token and gets no
serialization. `macos-user` does not need the file *sharing* — one real home means one real credentials
file — but it does lose the *refresh serialization* across concurrent sessions.

> [!NOTE]
> **One stale summary to fix while you are here.**
> [`loopholeinert.go:10-15` and `:58-62`](../../internal/cli/run/loopholeinert.go) both assert that
> `startLoopholes` *"returns nil for rt == \"container\" BEFORE any external service starts, so EVERY
> pack-shipped host daemon is skipped there."* That is no longer true —
> `loopholesruntime.go:172-174` starts `openai-auth-broker` on that backend, and
> `assemble_parts.go:606-611` calls its own allow-list check "the second spelling" of the same fact.
> The `backendInertReason` body itself (`:74-95`) is correct and well-measured; only the two summary
> sentences above it overstate the skip.

## What it does not buy

Three beliefs about the broker were measured false.
[`agent-credentials.md:365-380`](agent-credentials.md) states them; what this doc adds is the
precision each one needed.

- **It is not what stops a jail spending a stale token — and the reason is structural, not
  defensive.** The terminator does that, independently: `Refresh` sends one frame with no body, so the
  presented refresh token is **not an input to anything**. This would hold with zero serialization and
  no flock at all.
- **It does not need to be a singleton**, per the trace above: the flock derives from `$HOME`, every
  broker-side write is inside it, and the loser re-reads disk. Two source comments overstate the
  derivation; the conclusion is unaffected.
- **It does not trigger the vendor's dead-token disk clear, and cannot surface a real one either — and
  the margin is one string.** The classifier is
  `function _ne(e){… let n=e.response.status; if(n!==400&&n!==401)return!1; return
  jd(e.response.data).code==="invalid_grant" && ZEt(e.response.data)===null}` (offset 192054308), and
  it gates the clear at both call sites; the clear itself blanks `refreshToken`, `accessToken` and
  `expiresAt` in the file. Two refinements to how that has been described. It is **not** "by the
  top-level `error` string, not the status code" ([`agent-credentials.md:375`](agent-credentials.md)):
  the status **is** checked — it must be 400 or 401 — and `ZEt(data)===null` is required too; the
  status is necessary but not sufficient. And `jd` accepts an **object** form as well
  (`error.type === "invalid_grant"` matches), so "the error string" understates the surface. yolo is
  safe only because `errResult` always sets `error` to one of five plain strings —
  `creds_unreadable`, `no_refresh_token`, `upstream_http`, `upstream_bad_response`,
  `upstream_unreachable` (`refresh.go:28`, `:35`, `:67`, `:75`, `:91`, `:100`, `:103`, `:111`) — and a
  genuine upstream `invalid_grant` is wrapped at `refresh.go:91` with the real text buried in `body`
  (truncated at `:83-86`).

> [!WARNING]
> **The terminator returns broker errors at HTTP 400** (`oauthterminator/handler.go:118`), which
> **satisfies** the classifier's status gate. The string value of `error` is therefore the only thing
> standing between yolo and a clear that would blank the machine-wide shared credentials file for
> every workspace. Anyone "improving" the broker to pass upstream's error through verbatim fires it.

## Open question

### <a id="oq-ci1"></a>[`OQ-CI1`](#oq-ci1) — should the credential be shared at all?

**Not decided. Recorded with the measurements on both sides**, because the whole stack above exists to
serve one choice: that every jail on a machine share one Claude login.

**If the credential stopped being shared**, the interception, the CA, the terminator, the host daemon
and the flock all become unnecessary, and several live risks evaporate with them: the vendor's
co-located lock and file are correct again (both `.oauth_refresh.lock` and `.storage-write` would be
per-workspace over a per-workspace file, which is what the vendor built); the symlink-refusal risk
above disappears because there is no symlink in the credential path; the doc-fetch interposition and
its two logs disappear; and the unauthenticated refresh endpoint disappears. The vendor already
handles the multi-process case correctly on a plain host — the CAS and both locks are its own
machinery, and it is only yolo's scoping split that defeats them.

**The cost is one `/login` per workspace**, and that cost is larger than it sounds for a reason
measured above: a jail login cannot close in the browser, because the vendor's `redirect_uri` names
the jail's loopback, so it takes the manual-paste path through the human's host browser. It is also a
cost yolo has already ruled against once, in the strongest terms the corpus has for this:
[`packhooks.go:102-105`](../../internal/entrypoint/packhooks.go) records that re-authenticating in
every workspace is *"wrong behavior, not an inconvenience"*, which is why the machine tier exists at
all.

**One thing is unmeasured and could change the price.**
`CLAUDE_CODE_OAUTH_REFRESH_TOKEN` is read in exactly one place — the `claude login` subcommand — where
it exchanges a handed-in refresh token and saves the result to the credentials store, requiring
`CLAUDE_CODE_OAUTH_SCOPES` alongside it (offset 213747814). It is useless as a way to *avoid* having a
file, but it is a **scripted enrollment** channel. Whether a yolo verb could drive it to seed a fresh
workspace from an existing machine grant — turning "one `/login` per workspace" into "one `/login` per
machine plus a scripted seed per workspace" — has not been tried. If it works, the fork is cheaper
than it currently looks; if it does not, the cost stands as stated.

## Why it's this way

| Ruling | Why it holds |
| :--- | :--- |
| **Exactly one hostname is intercepted, and it carries no inference traffic** | The credential hop and the model hop are on different hosts, so the narrow interception is available at no cost. Widening it would create the man-in-the-middle on inference that this design does not have. |
| **Exactly one grant is terminated; everything else on that host is proxied** | The `/login` exchange must reach upstream to *mint* a credential, and the proxied 200 is what seeds the shared file. Terminating more would break enrollment; terminating less would leak the refresh race. |
| **DNS plus a private CA, rather than an endpoint override** | Not a stylistic choice: `CLAUDE_CODE_CUSTOM_OAUTH_URL` is allowlisted to three vendor endpoints and throws otherwise (2.1.278), so this is the only channel the vendor leaves open for moving the token endpoint. |
| **The credential lives in a file** | The vendor owns the storage. Its store is the only channel that carries a refreshable credential and the only one a plain interactive `claude` re-reads — and the vendor's own host-managed channel is *also* a file. |
| **The refresh token a jail presents is discarded at the jail edge** | Structural rather than defensive: `Refresh` forwards no body and `DoRefresh` takes only a path, so a jail cannot spend a stale token even with serialization switched off entirely. |
| **The flock is taken host-side, not in a singleton** | Host-side is where every backend agrees on the inode. The daemon being one process is not required — the lock derives from `$HOME` — and a shared-file lock would be backend-dependent. |
| **yolo's broker errors are never the string `invalid_grant`** | The vendor's dead-token classifier fires on that string at status 400/401, and the terminator already answers 400. Passing the upstream error through verbatim would blank the machine-wide credential file. |

## Current values

Every row names where its value is defined, so a row is checkable against that file rather than
against a commit stamp. Vendor rows are measured against **Claude Code 2.1.278** and carry a byte
offset for re-measurement.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Intercepted host | `platform.claude.com`, mapped to `broker_ip` `127.0.0.1` via `--add-host` | `packs/claude/loopholes/claude-oauth-broker/manifest.jsonc` (`intercepts`, `broker_ip`); emitted by `internal/loopholes/runtime.go` (`runtimeArgsWith`) |
| Terminator listener | `127.0.0.1:443`, HTTP/1.1 pinned, keep-alives disabled | `internal/oauthterminator/oauthterminatorcmd.go` (flag defaults, `srv`) |
| Terminated request shape | `POST` + path prefix `/v1/oauth/token` + JSON `grant_type == "refresh_token"` | `internal/oauthterminator` (`makeHandler`, `IsRefreshGrant`) |
| Proxy destination | `https://` + `UpstreamHost` + path — a constant, not the request's `Host` | `internal/oauthbroker/oauthbroker.go` (`UpstreamHost`); `internal/oauthbroker/http.go` (`DoProxy`) |
| Jail→host hop | the `0600` endpoint file named by `YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT`, re-read on every dial | `internal/oauthterminator/client.go` (`BrokerEndpointEnv`, `AskHostBroker`); `internal/svcendpoint` |
| Per-hop deadline | 30s on each side | `internal/oauthterminator/client.go`; `internal/oauthbroker/http.go` (`httpClient`) |
| Jail-side trust files | `ca.crt`, `server.crt`, `server.key` under `/var/lib/yolo-jail/loopholes/<name>/`, each `:ro` per file; the CA **key** never crosses | `manifest.jsonc` (`state_files`, `ca_cert`); `internal/loopholes/runtime.go`; `internal/oauthbroker/cert.go` |
| Served leaf | `CN=platform.claude.com`, `SAN DNS:platform.claude.com, DNS:localhost` | `internal/oauthbroker/cert.go` (leaf template) |
| Node/Bun trust var | `NODE_EXTRA_CA_CERTS`, one path — ⚠ joined as a list by yolo, read as a single filename by the consumer ([why that matters](#how-the-handshake-is-trusted)) | `internal/loopholes/runtime.go`; Claude Code 2.1.278 (`node:tls` compat, offset 22158845) |
| OpenSSL-family trust vars | `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO` → `$HOME/.yolo-ca-bundle.crt` | `internal/entrypoint/system.go` (`GenerateCABundle`), `boot.go`, `shell.go`; bound from the workspace in `internal/cli/run/assemble_parts.go` |
| Vendor credential path | `CLAUDE_SECURESTORAGE_CONFIG_DIR ?? CLAUDE_CONFIG_DIR ?? ~/.claude` + `.credentials.json`; yolo sets **neither** variable | Claude Code 2.1.278 (`Wb()`, offset 191037919); `packs/claude/pack.json`, `internal/entrypoint/packhooks.go` |
| Shared-credentials join | relative symlink from `.claude/.credentials.json` into the `scope: machine` `.claude-shared-credentials` | `packs/claude/pack.json`; `internal/entrypoint/packhooks.go` (`linkSharedCredential`) |
| Vendor refresh lock | `<configDir>/.oauth_refresh.lock`, `realpath:false`, stale `60000`, update `5000` — per-jail | Claude Code 2.1.278 (offset 192117058) |
| Vendor **legacy** refresh lock | `realpath(<configDir>) + ".lock"` — a second lock, per-jail, taken after the first | Claude Code 2.1.278 (offset 192117521) |
| Vendor credential **write** lock | `<configDir>/.storage-write`, `realpath:false`, 10 retries, stale `15000` — per-jail, and yolo takes no equivalent | Claude Code 2.1.278 (offset 191041435) |
| Broker refresh lock | `refresh.lock` under `BrokerDir()`, which takes no name, socket or pid | `internal/oauthbroker/oauthbrokercmd.go`, `internal/oauthbroker/cert.go` (`BrokerDir`) |
| Broker error codes | `creds_unreadable`, `no_refresh_token`, `upstream_http`, `upstream_bad_response`, `upstream_unreachable` — **never** `invalid_grant` | `internal/oauthbroker/refresh.go` (`DoRefresh`) |
| Client id, beta header | `9d1c250a-e61b-44d9-88ed-5944d1962f5e`; `oauth-2025-04-20` — byte-identical to the vendor's | `internal/oauthbroker/oauthbroker.go` (`ClientID`, `OAuthBetaHeader`); Claude Code 2.1.278 (offset 189496381) |
| Backends carrying the interception | podman only — Apple Container drops the record whole, `macos-user` declines the terminator by name | `internal/loopholes/runtime.go` (`admitsJailSideEffects`); `internal/cli/run/jaildaemondecline.go` |
| Refresh floors and cadence | the two-floor pair and the background refresher | owned by [`agent-credentials.md`](agent-credentials.md#current-values), not restated here |
