---
title: "Plan: shared OpenAI subscription authentication"
date: 2026-09-17
status: accepted
tags: [authentication, codex, pi, oauth, implementation]
summary: "What the machine-wide OpenAI credential service still needs: the macos-user refresh consumer, host-only import and logout, and the checks no unit test reaches."
---

# Plan: shared OpenAI subscription authentication

**Design:** [`openai-auth-broker.md`](openai-auth-broker.md) · **Status:** DESIGN,

> [!WARNING]
> **MEASURED 2026-09-22, and two of this plan's steps change shape. Read before building either.**
>
> **Step 3's blocker is dead, and a bigger defect was underneath it.** The "version floor is pinned
> nowhere" blocker assumed a floor worth refusing on. Measured across 13 Codex release tags by
> downloading each source tree: the refresh handling arrived in **0.56.0** (2025-11-07), absent at
> 0.55.0 and earlier, present at every release since. Today's `latest` is 0.155.1 and this jail has
> 0.145.0 — so **every installable Codex clears the floor by about a year**, and a launch-time
> refusal would be dead code.
>
> ✅ **What was underneath it is FIXED (2026-09-22): the adapter could not serve Codex at all.**
> Codex POSTs a JSON body — `.header("Content-Type","application/json").json(&refresh_request)`,
> true at 0.56.0, at 0.145.0 and at 0.155.1 — while
> [`openaiauthadapter.Handler`](../../internal/openaiauthadapter/handler.go) read the request with
> `r.ParseForm()`, which for `application/json` reads **nothing**. Every Codex refresh was answered
> `unsupported_grant_type` (400). ⚠ **No test caught it because every fixture was form-encoded** —
> the handler and its tests agreed with each other and with nothing else. `readTokenRequest` now
> decodes both encodings, and the Codex-shaped test is verified to fail against the old behaviour.
>
> **Step 4 is a ruling plus a rewrite, not a retry.** "Whether Pi's own 401 path calls
> `refreshToken`" is measured: pi 0.87.0 has **no such path**. Both call sites of the composed
> `oauth.refresh(...)` are **expiry-gated**, and 401 appears in none of its three retry classifiers
> (408/409/429/5xx only). The Codex API resolves the bearer **once** and its generic catch replays
> the same headers, so a retry there can never present a new token. Every 401→refresh in the bundle
> belongs to a vendored third-party SDK acting on its own cache. So
> `packs/pi/extensions/yolo-openai-auth.js` having no retry is **correct, not partial** — the
> extension API exposes no status to hook.
>
> **What these static reads cannot show:** no exit codes, no network failure paths, and nothing about
> whether a refreshed token is actually accepted upstream.
2026-09-17 — steps 1–8 are built or partial, steps 10–12 are buildable cold, step 9 owes
one ruling. **Written against** `365f0ecf`, re-checked step by step against the tree; the
2026-09-14 draft predates the entire implementation.

**Precedence:** the design wins on behavior, the tree wins on implementation facts, and
this file is advice — the first thing here to be wrong. Never twist the code to match it.

**What is left:** the `macos-user` refresh consumer (9) · Apple Container, which is a
*disclosure* job and not a transport job (10) · host-only import and logout (11) · the
checks nothing automated reaches (12). The rest is built, two steps differently from how
the original hand-off described them. **Read
[`jail-daemon-on-macos-user-plan.md`](jail-daemon-on-macos-user-plan.md) before step 9:**
it owns the generic in-jail-daemon half of this backend, already records this service's
dead `127.0.0.1:1460` override as one of its four measured cases, and is blocked on two
unfiled rulings.

## Where the original eight steps landed

| # | Step | State | Proof, or what is missing |
| :--- | :--- | :--- | :--- |
| 1 | Credential transaction | **done, differs** | `openaiauth.Broker`: `withLock` (`syscall.Flock`), reload under lock, `DecisionStale` on a caller-generation mismatch, `writeState`'s 0600-in-0700 atomic rename, `TokenFingerprint`, `context.WithoutCancel` around redemption. `TestConcurrentCallersRedeemExactlyOnce` is the race. **Differs:** a NEW package, not a generalization of `internal/oauthbroker`, whose `withRefreshLock` is still a second flock transaction — see Blockers. |
| 2 | Host service transport | **done, differs** | **Differs:** the service ships from `packs/openai-auth`, not from the Codex pack. `packs/codex/pack.json` and `packs/pi/pack.json` each carry an unconditional `needs` on it. `TestStagePacksJoinsOpenAIAuthForCodex` and `TestStageRunPacksPreservesNeededOpenAIAuthState` exercise the real selection call site. |
| 3 | Codex adapter | **partial** | `internal/openaiauthadapter` serves the native token-endpoint shape; the manifest's `jail_daemon` binds `127.0.0.1:1460`; `packs/codex/pack.json` sets `CODEX_REFRESH_TOKEN_URL_OVERRIDE` at that URL. **Missing:** the version floor is pinned nowhere — not in the pack, not in `internal/packdecl` (which has no such field), and [`../research/openai-subscription-auth.md`](../research/openai-subscription-auth.md) records only that it read Codex 0.154.0. |
| 4 | Pi adapter | **partial** | `packs/pi/extensions/yolo-openai-auth.js` registers the `openai-codex` provider (`login`/`refreshToken`/`getApiKey`), shells to `yolo internal openai-auth-client`, and puts `yolo-broker:<generation>` in Pi's `refresh` field — never the canonical token. **Missing:** the design's ask-once-more-after-unauthorized ([§2](openai-auth-broker.md#2-one-writer-and-two-views)). The file has no status inspection and no retry; whether Pi's own 401 path calls `refreshToken` and covers it is unverified, and Pi is not in this tree. |
| 5 | Callback relay and login | **partial, differs** | `openaiauthdaemon.StartLogin`: PKCE, exact-path `/auth/callback`, state compared before the code is taken, a second callback refused 409, `listenLoginPort` binding 1455 then 1457 with the redirect URI naming the port it got. **Differs:** no state registry and no routing to a jail — the host daemon owns the whole flow and the jail's `login` action only streams the URL back, which makes the design's relay unnecessary rather than unbuilt. **Missing:** a third concurrent login has no port. |
| 6 | Backend transport | **partial** | Podman: `hostServicesMountArgs` emits the services-dir bind plus `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT`, and the adapter joins `YOLO_JAIL_DAEMONS` through `runtimeArgsFor`. `macos-user`: the arm calls `startLoopholesDisclosed` with the whole pack set, refuses the launch when this service did not start, sets the variable to the **host** path, and `macosuser.EndpointGrantCommands` ACL-grants it (`PlanInvariants` refuses a plan that carries an endpoint without a grant). **Missing:** steps 9 and 10. |
| 7 | Managed host use | **partial** | `openaiauthhost.Prepare`, reached from `internal/cli/host.go` through `prepareOpenAIAuthHost` (`TestHostExecUsesManagedOpenAIAuthLaunch` pins that call site): a managed `CODEX_HOME`, the ordinary config copied with this workspace marked trusted, `AGENTS.md`/`skills` symlinked, a dynamic adapter on `127.0.0.1:0` closed when Codex exits. `hostwrap.Body("codex")` routes through it. **Missing:** the one-shot import (step 11). |
| 8 | Operations | **partial** | `runProactive`; `status` returning fingerprints only (`statusView`); `selfCheck` wired as the manifest's `doctor_cmd`. **Missing:** logout is **unreachable** — `Broker.Logout` has no production caller at all, only `TestReplaceAndLogoutUseCanonicalGeneration`. The docs half is listed under Ships with. |

## Map — remaining work only

| Path | Change |
| :--- | :--- |
| `internal/openaiauthdaemon/handler.go` | `import` and `logout` actions; `jailActionAllowed` keeps refusing both |
| `internal/openaiauthdaemon/main.go` | `serveSockets` builds **two** handlers, one per socket — see Traps |
| `internal/openaiauthdaemon/import.go` | new — decode Codex's `auth.json` into `openaiauth.Tokens` for `Broker.Replace` |
| `internal/openauthclient/main.go` | `import` and `logout` become real verbs, host-socket only |
| `internal/cli/openaiauth.go` | new — the user-facing verb; `internal/cli/internal.go` is a hidden path and logout is a thing users type |
| `internal/macosuser/runplan.go` | step 9 route (b) — carry a launch-owned refresh-adapter URL into the sandbox env |
| `internal/cli/run/run.go` (macos-user arm) | step 9 route (b) — start that adapter beside `startLoopholesDisclosed` and own its lifetime |
| `internal/cli/run/assemble_parts.go` | stop emitting the endpoint variable for `rt == "container"` |
| `internal/cli/run/packloopholes.go` | drop the `withoutOpenAIAuthPack` special case so AC reports this loophole inert |
| `packs/codex/pack.json` | the refresh URL stops being a static pack `env` var if step 9 takes the dynamic-port route |

## Reuse

- **`internal/openaiauthhost` already solves step 9.** A managed host Codex is the same
  problem as a `macos-user` Codex — a native process on the launcher's own loopback — and
  `prepare` solves it: `net.Listen("tcp", "127.0.0.1:0")`, `openaiauthadapter.Serve`, the
  URL written from `listener.Addr()`, and `Launch.Run` closing it when the agent exits.
  That is route (b) below, already written once.
- **The one-retry-after-401 exists in-tree**: `wirebridged.NewCodexResponsesHandler` sets
  `retryUnauthorized` around `openauthclient.RequestAccessToken`. Step 4's gap is the same
  behavior one layer out.
- **The negative half of step 11 is already written and passing.** `jailActionAllowed`
  excludes `import` and `logout` (`TestJailActionsExcludeMachineWideDestructiveOperations`),
  and `openauthclient.Run` refuses both (`TestRunRefusesMachineWideMutations`). Both stay
  green; step 11 adds the positive path on the private host socket only. The wire key is
  already anticipated — that second test asserts the client never sends `path`.
- **`browserOpen` is an injectable package var** and
  `TestBrowserLoginFallsBackValidatesStateAndExchangesPKCE` is the pattern for any login
  test: fake opener, `httptest` token server, no network and no agent process.
- `openauthclient.WriteCodexAuth` is the importer's mirror image — round-trip against it
  rather than inventing a second reading of Codex's schema.
- Prelaunch needs nothing new on any backend: `agentAuthPrelaunchShellFn` derives
  `YOLO_AUTH_PRELAUNCH_<BIN>_{FLAG,PATH}` from `$BIN`, and `GenerateAgentLaunchers` runs
  from `internal/entrypoint/boot.go` and `internal/entrypoint/darwin.go` both.

## Traps

- **One handler serves both sockets, and it cannot tell them apart.** `serveSockets` hands
  the same `hostservice.Handler` to `ServeFrontedUnix` (jail-facing, behind yolo's front)
  and `ServeUnix` (the private 0600 `HostSocketPath`). `hostservice`'s own doc states the
  handler never learns which socket carried its bytes, and `Session.JailID` falls back to
  the client's *self-asserted* `jail_id` on `ServeUnix`. **Constraint:** gate host-only
  actions by building a second handler for the host socket, never by inspecting the
  session. A `JailID`-based check is a jail-supplied string.
- **`macos-user` Codex is handed a refresh URL nothing binds.** `CODEX_REFRESH_TOKEN_URL_OVERRIDE`
  is a static pack `env` var and pack env reaches this backend (`packload.EnvVarsFor` →
  `packChannel.launchEnv` → `macosuser.Options.PackEnv` → the sandbox env file), while
  `internal/macosuser` has no `JailDaemon` reader and `YOLO_JAIL_DAEMONS` is emitted only
  into a container argv. Symptom: prelaunch seeds a good `auth.json`, the session works
  until expiry, then every refresh fails against a closed port. Step 9 fixes it. Note that
  1460 is the *machine's* real loopback on this backend, so a manifest-literal port is also
  a collision between two concurrent launches.
- **Apple Container's measurement is negative, and the launch does not say so.**
  `backendInertReason("container")` records the result of
  `integration/applecontainer_test.go`'s `TestAppleContainerReachesHostLoopback` on
  `container` 1.1.0: a container→host connection completes its handshake and carries
  nothing, no bind address helps, and `host.containers.internal` does not resolve. AC
  nevertheless gets the endpoint variable and the mount (`brokerEndpointIsUnpublishable`
  suppresses only the *Claude* broker's variable), `startLoopholesDisclosed` passes
  `withoutOpenAIAuthPack` so the inert line is withheld, and the disposition is `unknown`
  (`jailLoopbackEnvArgs`'s default), which never escalates — so the fatal witness does not
  refuse it either. **Step 10 is a disclosure change, not a transport one.**
- **Do not mount the state directory anywhere.** The nonempty `state_files` list is the
  fail-closed boundary that keeps `credentials.json` out of the jail; an empty or absent
  list mounts the whole directory. `prepareOpenAIAuthMountSentinel` exists to give that
  boundary one harmless always-present source.
- Carried forward, still true: do not share all of `CODEX_HOME`; do not treat a symlink as
  a refresh lock; do not cancel after redemption may have started; do not log callback URLs
  (their query holds an authorization code).

## Build order

Each step ends green and committable. **Class** names the instrument that can actually
prove it — read *Instruments* below before believing a green.

9. **`macos-user` Codex refresh consumer.** Two routes, and the choice is a ruling — see
   Blockers. **(a)** Wait for
   [`jail-daemon-on-macos-user-plan.md`](jail-daemon-on-macos-user-plan.md) steps 3 and 4,
   which start the declared `yolo-jaild openai-auth-adapter` natively; both are blocked.
   **(b)** Give this one service a launch-owned adapter instead: `net.Listen` on
   `127.0.0.1:0` beside `startLoopholesDisclosed`, carry its URL into the sandbox env so it
   overrides the pack's static value, close it when the agent exits — the `openaiauthhost`
   shape, which needs no native `yolo-jaild` and adds no confined child, so it is subject to
   neither of that plan's blockers. Either way the deliverable is the same: a Codex session
   that outlives its first access token. **Class:** Linux `go test` for the plan (`macosuser.BuildRunPlan`
   is ordinary Go — `internal/macosuser` is not build-tagged darwin-only, and
   `internal/macosuser/openaiauth_endpoint_test.go` already runs on Linux); the **hosted Mac
   nightly** for the launch (`.github/workflows/macos-user.yml`, `-run '^TestMacosUser'`). →
   `go test ./internal/macosuser ./internal/cli/run`
10. **Tell the truth on Apple Container.** Withhold the endpoint variable for
    `rt == "container"` and delete the `withoutOpenAIAuthPack` special case so the launch
    reports the loophole inert with `backendInertReason`'s measured reason. **Class:**
    Linux `go test` alone — this is argv and report content. Re-running
    `TestAppleContainerReachesHostLoopback` on the Mac runner is how the skip *expires*
    later, not how this step is proved. →
    `go test ./internal/cli/run`
11. **Host-only import and logout.** Two handlers in `serveSockets`; an `import` action
    reading a named Codex `auth.json` into `Broker.Replace`; a `logout` action calling
    `Broker.Logout`; both wired to a user-facing verb that states logout is machine-wide
    before it deletes anything. **Class:** Linux `go test`. →
    `go test ./internal/openaiauthdaemon ./internal/openauthclient ./internal/cli`
12. **The checks nothing automated reaches.** A rootless jail completing a brokered
    refresh; a browser login end to end; two agents crossing one expiry boundary with one
    upstream redemption. **Class:** the podman `integration` job in `.github/workflows/ci.yml` (rootless on
    both arches) for reachability; a **human at a real machine** for the browser and the
    expiry crossing, because both need a ChatGPT account and real wall-clock time.

## Instruments — what each one cannot prove

- **A nested jail proves nothing here.** Podman-in-podman forces `--net=host`, so the
  jail's loopback *is* the launcher's and the reachability class cannot fail; and it runs
  as root, so every rootless-only path is untested (AGENTS.md's two carve-outs).
- **Linux `go test -short`** proves the transaction, the adapter, the client, every argv
  and every `macos-user` run plan — not `sandbox-exec`, `container`, or rootless podman.
- **`.github/workflows/ci.yml`'s `integration` job** is a real rootless host on both arches: that, not a
  human, is the cheap instrument for step 12's reachability half. Report
  `podman info --format '{{.Host.Security.Rootless}}'` with any such claim.
- **`.github/workflows/macos-user.yml`** (hosted `macos-latest`, nightly, `^TestMacosUser`) is the macos-user
  instrument. **`.github/workflows/apple-container.yml`** (self-hosted arm64 Mac, `workflow_dispatch` plus a
  launchd dispatcher, `^TestAppleContainer`) is the only machine with the AC backend.
- **No automated test may start an agent CLI interactively or call an API.** `--version`
  probes only; fake the upstream with `httptest` and the opener with `browserOpen`.

## Ships with

- **Unit, step 9:** the run plan carries the launch-owned URL and it *overrides* the pack's
  static value (the override is the assertion — a test that only checks presence passes
  against today's dead port); the adapter is closed on exit.
- **Unit, step 10:** an AC launch emits no `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT` and
  *does* print the inert line for `openai-auth`. **Rewrite, do not repair** — all three of
  `TestAppleContainerMountsOnlyActiveOpenAIAuthEndpoint`,
  `TestAppleContainerWithholdsUnapprovedOpenAIAuthEndpoint` and
  `internal/cli/run/acbrokerendpoint_test.go` assert the behavior step 10 removes.
- **Unit, step 11:** `import` and `logout` succeed on the host socket and are refused on the
  fronted one — the refusal must reach the daemon *through* `ServeFrontedUnix`, or it pins a
  callee whose call site is unpinned. Import refuses a malformed or expired file, refuses a
  symlink, and leaves `~/.codex/auth.json` untouched; logout is idempotent.
- **Integration: there is nothing today.** `integration/` contains no OpenAI-broker test at
  all. Step 12's first artifact is one: a jail whose `packs` selects `codex`, asserting a
  brokered `token` round-trip through the published endpoint. That is the test that would
  catch this breaking end to end, and it is the one the whole unit suite cannot represent.
- **Docs that now describe the old thing** — all written before the macos-user lifecycle was
  generalized, all claiming the two macOS backends carry exactly one loophole, and all still
  correct about Apple Container: [`../reference/agent-credentials.md`](../reference/agent-credentials.md)
  (the warning under *The OpenAI subscription credential service*, and the `macos-user` cell
  of *Per-backend differences*), [`../guides/macos.md`](../guides/macos.md),
  [`../guides/USER_GUIDE.md`](../guides/USER_GUIDE.md),
  [`../research/macos-support-matrix.md`](../research/macos-support-matrix.md),
  [`../plans/macos-revival-and-distribution-plan.md`](../plans/macos-revival-and-distribution-plan.md).
- **Docs that never covered it:** [`../reference/loophole-protocol.md`](../reference/loophole-protocol.md)
  does not mention this loophole, and `internal/cli/config_ref.txt` names no key or variable
  of it — correctly, since AGENTS.md documents a `YOLO_*` dial where it is enforced. Treat
  step 8's docs line as satisfied by [`../guides/loopholes.md`](../guides/loopholes.md) plus
  [`../reference/agent-credentials.md`](../reference/agent-credentials.md), and record the
  measured Codex version floor in
  [`../research/openai-subscription-auth.md`](../research/openai-subscription-auth.md) beside
  its 0.154.0 provenance line.

## Don't

- Don't build the design's state-routed callback relay ([§3](openai-auth-broker.md#3-browser-callback-relay)).
  The daemon owns the flow and the jail never listens, so there is nothing to route. The
  real gap is the third concurrent login, and it is a port-allocation question.
- Don't try to start `yolo-jaild` natively yourself: it is not built for darwin and
  `macosuser.StageBinaryCommands` stages `yolo` alone. Resolving that is
  [`jail-daemon-on-macos-user-plan.md`](jail-daemon-on-macos-user-plan.md)'s step 3, not
  this one's.
- Don't reach for `Session.JailID` to identify a host caller. See Traps.
- Don't widen `state_files`, and don't make it empty.
- Don't try to fix Apple Container's transport: nothing crosses container→host there and
  the cause is upstream. Step 10 is about saying so.

## Blockers

- **Stop and ask: route (a) or (b) for step 9.** Route (b) is the cheaper path and un-blocks
  this service from the two unfiled rulings
  [`jail-daemon-on-macos-user-plan.md`](jail-daemon-on-macos-user-plan.md) is waiting on —
  and it is consistent with what already ships, since the loopback-TLS front itself runs
  unconfined in the launcher process on this backend. But it is a second mechanism for one
  manifest key, and whether that is acceptable is exactly what that plan's confinement
  question decides. Do not pick it unilaterally.
- **Stop and ask before refusing an old Codex.** Recording the floor is documentation; a
  launch-time refusal or a `min_version` schema field is new behavior and belongs in the
  design doc first. `internal/packdecl` has no such field today.
- **Two flock transactions, by accident rather than by ruling.** The design's
  [§2](openai-auth-broker.md#2-one-writer-and-two-views) says the engine "should be
  generalized once … rather than a second independent implementation"; what shipped is
  `internal/openaiauth` beside `internal/oauthbroker`. Nothing remaining needs it resolved.
  Recorded so the next reader does not believe that sentence.
