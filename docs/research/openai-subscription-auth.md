---
title: "OpenAI subscription authentication across Codex, Pi, the host, and jails"
date: 2026-09-14
status: accepted
tags: [authentication, codex, pi, oauth, research]
summary: "Source-level research into browser login, refresh-token rotation, shared state, and the safe boundary for sharing one ChatGPT subscription grant."
---

# OpenAI subscription authentication across Codex, Pi, the host, and jails

**Status:** findings gathered 2026-09-14 from Codex 0.154.0, Pi 0.85.1, current
upstream source, and public issue reports. Codex's version floor for the broker seam is
**0.56.0**, measured 2026-09-22 and re-checked 2026-09-25 ([§1.2](#12-codex-refresh-is-careful-inside-one-process-not-across-processes)).

> **In short.** Copying either agent's credential file is unsafe because OpenAI
> rotates refresh tokens. Sharing becomes reliable only when one machine-wide
> service owns refresh and every consumer, including an opted-in host Codex,
> obtains current tokens through it.

**Reads with:** [`../design/openai-auth-broker.md`](../design/openai-auth-broker.md)
(the resulting design), [`../reference/agent-credentials.md`](../reference/agent-credentials.md)
(the existing credential tiers), and
[`claude-oauth-refresh-mechanics.md`](claude-oauth-refresh-mechanics.md) (the
same failure class for Claude).

---

## 1. Findings

### 1.1 Codex browser login needs a callback into the jail

Codex's normal login starts an HTTP listener on jail loopback and asks the browser
to return to `http://localhost:1455/auth/callback`; current upstream can fall back
to port 1457 when 1455 is occupied. The browser runs on the host, where
`localhost` is different. `network.forward_host_ports` is the opposite direction
(jail to host), while a static `network.ports` publication would collide as soon
as two jails tried to use the same host port.

The useful prior art is a temporary callback relay. Container recipes commonly
publish port 1455 or carry it through an SSH tunnel, and RFC 8252 recommends a
loopback redirect for native applications. A yolo relay can improve on a static
publication: one host listener receives the callback and routes it to the jail
that registered the OAuth `state` value.

### 1.2 Codex refresh is careful inside one process, not across processes

Current Codex source has a process-local semaphore. Before refreshing it reloads
`auth.json`; if another writer already changed the credential, it adopts that
generation. On an unauthorized response it first reloads disk, then refreshes and
retries. These steps reduce races but do not close one between two processes:
both can read the same generation before either writes, then redeem the same
single-use refresh token.

Codex writes `auth.json` by truncating and rewriting it. It has no cross-process
file lock. Public Codex reports reproduce `refresh_token_reused` when credential
files are copied or independently refreshed, and OpenAI closed a request for a
separate shared-auth path as not planned. A symlink keeps one current file and is
better than copies, but it still does not serialize the authority request.

Current upstream exposes `CODEX_REFRESH_TOKEN_URL_OVERRIDE`, which is the clean
seam for a broker. Codex can retain its native file and recovery behavior while
the broker serializes the only operation that consumes a refresh token.

**The version floor is 0.56.0.** "Version floor" here means the oldest Codex release
that honors the override: one older than that posts its refresh straight to OpenAI and
never reaches the broker. The override arrived in 0.56.0, released 2025-11-07. The
`rust-v0.55.0` tree's `codex-rs/core/src/auth.rs` posts to the hardcoded
`https://auth.openai.com/oauth/token`. The `rust-v0.56.0` tree's copy reads
`CODEX_REFRESH_TOKEN_URL_OVERRIDE` (`REFRESH_TOKEN_URL_OVERRIDE_ENV_VAR`, through
`refresh_token_endpoint`) and sends the refresh as a JSON body (`Content-Type:
application/json`). That JSON shape is what yolo's Codex adapter decodes
(`readTokenRequest` in `internal/openaiauthadapter`).
The floor was first measured on 2026-09-22 across 13 release tags, which found the override
absent at 0.55.0 and earlier and present at every release since. That measurement is recorded in
the plan's [2026-09-22 warning](../design/openai-auth-broker-plan.md) (commit `a0527c48`).
The two boundary trees were re-read from GitHub's tag archives on 2026-09-25. Every Codex
installable today is about a year newer than the floor, so the plan judges a launch-time
refusal of older releases dead code and builds none.

### 1.3 Pi has good locking, within Pi's file format

Pi's `openai-codex` provider uses the same OpenAI client ID and token endpoint as
Codex. It stores a provider-scoped OAuth record in `~/.pi/agent/auth.json`, then
refreshes within five minutes of expiry.

Pi uses `proper-lockfile` around a read, refresh, and write transaction, including
a second credential check after acquiring the lock. That coordinates Pi
processes sharing one file. Codex uses another file shape and does not take that
lock, so the two agents can still race at the token endpoint.

Pi's Codex Responses adapter only needs an access-token JWT. It extracts the
ChatGPT account identifier from the token and sends both in request headers.
This lets a yolo provider extension give Pi a current access token without
giving it the canonical refresh token. Pi still requires a `refresh` string in
its OAuth record; a nonsecret broker marker can satisfy that local schema while
the extension's refresh method obtains the current generation. Pi's documented
`!command` API-key values are cached for the process lifetime, so that mechanism
alone is too stale for a long session.

### 1.4 `macos-user` changes the route, not credential ownership

`macos-user` runs the agent as a sandbox account directly on macOS. Its loopback
is the host browser's loopback, so browser callbacks already arrive without port
publication. The existing container loophole launcher skips external services
on this backend because a native process can reach host services directly. The
OpenAI service therefore needs an explicit `macos-user` publication path: start
the same host singleton, publish an authenticated loopback-TLS endpoint in
sandbox-visible launch state, and let both thin agent adapters call it. No DNS
override, system trust change, or TLS interception is required.

### 1.5 Pi selects the ChatGPT subscription as the `openai-codex` provider

Pi's current provider identifier for a ChatGPT Plus or Pro subscription is
`openai-codex`. After starting Pi, `/model` or Ctrl+L opens the model picker;
choosing any model under that provider makes Pi use the shared subscription.
The command-line equivalents are:

```bash
pi --provider openai-codex --model gpt-5.5
pi --model openai-codex/gpt-5.5
```

In yolo, the interactive path is `yolo -- pi`, then `/model`. A configured
Codex profile can make the selection at launch with `yolo -p codex -- pi`.
Pi's [`README`](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md#providers--models)
documents `/model` and Ctrl+L, and its
[`CLI reference`](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/cli/args.ts)
defines `--provider` plus both forms of `--model`. Verified from current upstream
source on 2026-09-14.

## 2. Prior-art verdicts

| Approach | Verdict | Reason |
| :--- | :--- | :--- |
| Copy host `auth.json` into each jail | Reject | Each copy eventually holds a consumed refresh token. |
| Symlink one Codex `auth.json` everywhere | Incomplete | Readers see rotation, but authority refresh and truncate-write remain cross-process races. |
| Log in independently in every Codex home | Safe but poor experience | Separate grants do not race, but login is repeated and Pi cannot share it. |
| Publish host port 1455 permanently | Reject | Fixed host-port collisions make concurrent jails mutually exclusive. |
| Temporary callback relay keyed by OAuth state | Adopt | One host listener can route concurrent callbacks without reserving one host port per jail. |
| One machine-wide refresh owner | Adopt | It serializes the single-use operation and gives Codex and Pi one current generation. |
| Share with ordinary, unmanaged host Codex | Reject | A host process that bypasses the broker can consume the broker's refresh token. Host sharing must be opt-in and broker-routed. |

## 3. Debugging requirements

The Claude incident was hard to diagnose because token bodies could not be logged
and the failing writer was ambiguous. The OpenAI service should record only:

- a one-way fingerprint of the refresh-token generation;
- expiry and remaining lifetime;
- caller class (`codex`, `pi`, host, or jail identity);
- whether a request returned the cached generation, adopted a newer generation,
  refreshed upstream, or failed;
- upstream status, OpenAI error code, request ID, and latency;
- last successful login, refresh, and self-check timestamps.

It must never log tokens, authorization codes, PKCE verifiers, or callback query
strings. A status command should distinguish “access token still usable” from
“refresh path proved usable”; only a controlled refresh self-check proves the
latter.

## 4. Sources and half-life

Primary implementation sources inspected on 2026-09-14:

- [Codex login server](https://github.com/openai/codex/blob/main/codex-rs/login/src/server.rs),
  [auth manager](https://github.com/openai/codex/blob/main/codex-rs/login/src/auth/manager.rs),
  and [file storage](https://github.com/openai/codex/blob/main/codex-rs/login/src/auth/storage.rs).
- [Pi OpenAI OAuth provider](https://github.com/earendil-works/pi/blob/main/packages/ai/src/auth/oauth/openai-codex.ts)
  and [Pi provider documentation](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/providers.md).
- [RFC 8252, OAuth for native apps](https://www.rfc-editor.org/rfc/rfc8252).

Observed failure reports and deployment prior art:

- [Codex issue #15410](https://github.com/openai/codex/issues/15410) documents
  copied homes and single-use refresh failures; OpenAI closed separate shared
  auth as not planned.
- [Codex issue #15502](https://github.com/openai/codex/issues/15502) reports the
  documented copied-cache flow remaining unreliable.
- [Paperclip issue #5707](https://github.com/paperclipai/paperclip/issues/5707)
  reports multiple agents consuming one Codex refresh token.
- [hotchpotch/openai-api-server-via-codex](https://github.com/hotchpotch/openai-api-server-via-codex/blob/main/docs/docker.md)
  demonstrates port publication and SSH tunneling for the callback.

This is fast-moving code. Re-check the Codex override, callback ports, Pi auth
extension surface, and both file formats immediately before implementation.
