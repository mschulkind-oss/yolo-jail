---
title: "Plan: shared OpenAI subscription authentication"
date: 2026-09-14
status: accepted
tags: [authentication, codex, pi, oauth, implementation]
summary: "Build order and verification for the machine-wide OpenAI credential service and agent adapters."
---

# Plan: shared OpenAI subscription authentication

**Design:** [`openai-auth-broker.md`](openai-auth-broker.md) · Written against
the repository on 2026-09-14.

**Status:** DECIDED, 2026-09-14 — ready to implement.

**Precedence:** the design wins on behavior, the tree wins on implementation
facts, and this file is build advice.

## Build order

1. **Credential state and refresh transaction.** Generalize the proven
   host-wide locking, stale-caller detection, atomic persistence, fingerprints,
   and logging patterns in `internal/oauthbroker`. Keep the OpenAI wire format
   separate from Claude's. Unit tests must race multiple stale callers and show
   one upstream redemption.
2. **Host service transport.** Ship the service from the Codex pack, and let the
   Pi pack depend on or contribute to the same service without two declarations.
   Exercise the real pack-selection call site: Codex-only, Pi-only, both, and
   neither.
3. **Codex adapter.** Add the shared machine credential tier and set
   `CODEX_REFRESH_TOKEN_URL_OVERRIDE` from the published endpoint. Pin the
   minimum Codex version that supports the variable. Tests must delete or bypass
   the production contribution and fail.
4. **Pi adapter.** Register a narrow `openai-codex` provider extension whose
   login and refresh methods obtain an agent-shaped access-token view from the
   service. Preserve Pi's native expiry scheduling and store a nonsecret broker
   marker where its schema requires a refresh string. Retry broker resolution
   once after an unauthorized response. Do not put the canonical refresh token
   in Pi's workspace state.
5. **Callback relay and login CLI.** Add state registration, exact-path routing,
   expiry, replay refusal, and 1455/1457 binding. Test with two simultaneous
   fake jail listeners and swapped completion order. Verify bridged-network
   reachability with bare rootless Podman; a nested yolo jail cannot prove it.
6. **Backend transport.** Reuse the authenticated loopback-TLS front on all
   backends. Container jails get the endpoint file through the existing mount;
   `macos-user` gets it in sandbox-visible launch state and needs no callback
   relay. Pin that no backend intercepts `auth.openai.com` or changes the host
   trust store.
7. **Managed host use.** Make `yolo host -- codex` and the generated Codex host
   wrapper select a yolo-managed Codex home, render the same pack-owned config
   and skills there, and point at the service. Add an explicit one-shot import
   from the ordinary host Codex credential. Verify direct `codex` and
   `~/.codex/auth.json` remain unchanged.
8. **Operations.** Add proactive refresh, status, fingerprint-only diagnostics, logout, and a
   refresh self-check. Update `agent-credentials.md`, config reference, pack
   reference, and loophole listings.

## Required verification

- `just format`
- `just test-fast`
- `just build-go`
- Launch from `/tmp/yolo-nested` with the freshly built binary and
  `YOLO_REPO_ROOT=/workspace`; this proves plumbing and first boot only.
- Run the callback relay and host-service integration on a real rootless host,
  reporting `podman info --format '{{.Host.Security.Rootless}}'` and
  `podman info --format '{{.Host.RootlessNetworkCmd}}'`.
- Run browser login and an expiry crossing on a real `macos-user` backend; the
  nested jail cannot exercise Seatbelt or the sandbox account's loopback access.
- Manual no-API-call probes may run `codex --version` and `pi --version` only.

## Traps

- Do not share all of `CODEX_HOME`; it also holds SQLite databases, sessions,
  cache, configuration, and skills.
- Do not treat a symlink as a refresh lock.
- Do not cancel after an upstream refresh request may have consumed the token.
- Do not log callback URLs: their query contains an authorization code.
- Codex's installed version may predate the refresh override; verify the release
  floor before enabling the contribution.
- Pi command-backed API keys are cached for a process lifetime and cannot alone
  supply refresh behavior.
- Pack loopholes are currently skipped on `macos-user`; starting the singleton
  and publishing its endpoint there must be an explicit supported service path,
  not an accidental relaxation of every loophole.
