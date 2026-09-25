---
title: "Plan: shared OpenAI subscription authentication"
date: 2026-09-17
status: accepted
tags: [authentication, codex, pi, oauth, implementation]
summary: "What the machine-wide OpenAI credential service still needs: the macos-user refresh consumer and the checks no unit test reaches. Host-only import and logout and the Apple Container disclosure shipped 2026-09-18; the Codex version floor was recorded 2026-09-25."
---

# Plan: shared OpenAI subscription authentication

**Status:** DECIDED, 2026-09-24 — steps 1–8 are built or partial, steps 10 and 11 shipped
2026-09-18 (`36c47baa`, `4de78ac0`; step 10's endpoint-withholding half was declined there, with
its reason), step 9 waits on [OQ-OA6](openai-auth-broker.md#OQ-OA6), step 4's last clause waits
on [OQ-OA7](openai-auth-broker.md#OQ-OA7), and step 12 is the checks nothing automated reaches.

**Design:** [`openai-auth-broker.md`](openai-auth-broker.md)

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
**Written against** `365f0ecf` on 2026-09-17, re-checked step by step against the tree; the
2026-09-14 draft predates the entire implementation. Steps 10 and 11 landed after that check.

**Precedence:** the design wins on behavior, the tree wins on implementation facts, and
this file is advice — the first thing here to be wrong. Never twist the code to match it.

**What is left:** the `macos-user` refresh consumer (9, waiting on
[OQ-OA6](openai-auth-broker.md#OQ-OA6)) · the checks nothing automated reaches (12). The
measured Codex floor is recorded in
[`../research/openai-subscription-auth.md`](../research/openai-subscription-auth.md) (2026-09-25).
Apple Container's disclosure (10,
`36c47baa`) and host-only import and logout (11, `4de78ac0`, public as `yolo openai-auth` since
`fafb7493`) shipped. The rest is built, two steps differently from how the original hand-off
described them. **Read
[`jail-daemon-on-macos-user-plan.md`](jail-daemon-on-macos-user-plan.md) before step 9:**
it owns the generic in-jail-daemon half of this backend, already records this service's
dead `127.0.0.1:1460` override as one of its four measured cases, and is blocked on
[OQ-DP8](declaration-parity.md#OQ-DP8) and [OQ-DP9](declaration-parity.md#OQ-DP9).

## Where the original eight steps landed

| # | Step | State | Proof, or what is missing |
| :--- | :--- | :--- | :--- |
| 1 | Credential transaction | **done, differs** | `openaiauth.Broker`: `withLock` (`syscall.Flock`), reload under lock, `DecisionStale` on a caller-generation mismatch, `writeState`'s 0600-in-0700 atomic rename, `TokenFingerprint`, `context.WithoutCancel` around redemption. `TestConcurrentCallersRedeemExactlyOnce` is the race. **Differs:** a NEW package, not a generalization of `internal/oauthbroker`, whose `withRefreshLock` is still a second flock transaction — see Blockers. |
| 2 | Host service transport | **done, differs** | **Differs:** the service ships from `packs/openai-auth`, not from the Codex pack. `packs/codex/pack.json` and `packs/pi/pack.json` each carry an unconditional `needs` on it. `TestStagePacksJoinsOpenAIAuthForCodex` and `TestStageRunPacksPreservesNeededOpenAIAuthState` exercise the real selection call site. |
| 3 | Codex adapter | **done** | `internal/openaiauthadapter` serves the native token-endpoint shape, JSON and form bodies both (`readTokenRequest`, fixed 2026-09-22); the manifest's `jail_daemon` binds `127.0.0.1:1460`; `packs/codex/pack.json` sets `CODEX_REFRESH_TOKEN_URL_OVERRIDE` at that URL. The version floor is **measured** (0.56.0, the warning above), so a launch-time refusal would be dead code. The floor is **recorded** (2026-09-25) in [`../research/openai-subscription-auth.md`](../research/openai-subscription-auth.md) §1.2, beside its 0.154.0 provenance line. |
| 4 | Pi adapter | **done, one clause owed a ruling** | `packs/pi/extensions/yolo-openai-auth.js` registers the `openai-codex` provider (`login`/`refreshToken`/`getApiKey`), shells to `yolo internal openai-auth-client`, and puts `yolo-broker:<generation>` in Pi's `refresh` field — never the canonical token. The design's ask-once-more-after-unauthorized ([§2](openai-auth-broker.md#2-one-writer-and-two-views)) is **measured unbuildable** (the warning above): pi has no 401 refresh path and the extension API exposes no status. Whether the design drops it is [OQ-OA7](openai-auth-broker.md#OQ-OA7). |
| 5 | Callback relay and login | **partial, differs** | `openaiauthdaemon.StartLogin`: PKCE, exact-path `/auth/callback`, state compared before the code is taken, a second callback refused 409, `listenLoginPort` binding 1455 then 1457 with the redirect URI naming the port it got. **Differs:** no state registry and no routing to a jail — the host daemon owns the whole flow and the jail's `login` action only streams the URL back, which makes the design's relay unnecessary rather than unbuilt. **Missing:** a third concurrent login has no port. |
| 6 | Backend transport | **partial** | Podman: `hostServicesMountArgs` emits the services-dir bind plus `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT`, and the adapter joins `YOLO_JAIL_DAEMONS` through `runtimeArgsFor`. `macos-user`: the arm calls `startLoopholesDisclosed` with the whole pack set, refuses the launch when this service did not start, sets the variable to the **host** path, and `macosuser.EndpointGrantCommands` ACL-grants it (`PlanInvariants` refuses a plan that carries an endpoint without a grant). Apple Container reports the loophole inert (step 10). **Missing:** step 9. |
| 7 | Managed host use | **partial** | `openaiauthhost.Prepare`, reached from `internal/cli/host.go` through `prepareOpenAIAuthHost` (`TestHostExecUsesManagedOpenAIAuthLaunch` pins that call site): a managed `CODEX_HOME`, the ordinary config copied with this workspace marked trusted, `AGENTS.md`/`skills` symlinked, a dynamic adapter on `127.0.0.1:0` closed when Codex exits. `hostwrap.Body("codex")` routes through it. The one-shot import shipped as step 11. |
| 8 | Operations | **done** | `runProactive`; `status` returning fingerprints only (`statusView`); `selfCheck` wired as the manifest's `doctor_cmd`; `logout` reachable from `yolo openai-auth logout` on the host socket only (step 11). The docs half is listed under Ships with. |

## Map — remaining work only

| Path | Change |
| :--- | :--- |
| `internal/macosuser/runplan.go` | step 9 route (b) — carry a launch-owned refresh-adapter URL into the sandbox env |
| `internal/cli/run/run.go` (macos-user arm) | step 9 route (b) — start that adapter beside `startLoopholesDisclosed` and own its lifetime |
| `packs/codex/pack.json` | the refresh URL stops being a static pack `env` var if step 9 takes the dynamic-port route |

Steps 10 and 11's rows are spent: `import.go`, the second handler in `serveSockets`, the host-only
client verbs and the public `yolo openai-auth` verb landed, and the `withoutOpenAIAuthPack`
special case is deleted. Step 10's other row — withholding the endpoint variable on Apple
Container — was **declined** in `36c47baa`: the measurement is per backend, so every
loopback-TLS endpoint there is equally unreachable, and withholding the pointer while the adapter
stays in `YOLO_JAIL_DAEMONS` turns an unreachable front into no front at all.

## Reuse

- **`internal/openaiauthhost` already solves step 9.** A managed host Codex is the same
  problem as a `macos-user` Codex — a native process on the launcher's own loopback — and
  `prepare` solves it: `net.Listen("tcp", "127.0.0.1:0")`, `openaiauthadapter.Serve`, the
  URL written from `listener.Addr()`, and `Launch.Run` closing it when the agent exits.
  That is route (b) below, already written once.
- **The one-retry-after-401 exists in-tree**: `wirebridged.NewCodexResponsesHandler` sets
  `retryUnauthorized` around `openauthclient.RequestAccessToken`. It is **not** a template for
  step 4: that handler sees the upstream status, and pi's extension never does
  ([OQ-OA7](openai-auth-broker.md#OQ-OA7)).
- **Step 11's negative half still guards it.** `jailActionAllowed` excludes `import` and
  `logout` (`TestJailActionsExcludeMachineWideDestructiveOperations`), and `openauthclient.Run`
  refuses both from a jail (`TestRunRefusesMachineWideMutations`); the positive path lives on
  the private host socket only.
- **`browserOpen` is an injectable package var** and
  `TestBrowserLoginFallsBackValidatesStateAndExchangesPKCE` is the pattern for any login
  test: fake opener, `httptest` token server, no network and no agent process.
- `openauthclient.WriteCodexAuth` is the importer's mirror image — round-trip against it
  rather than inventing a second reading of Codex's schema.
- Prelaunch needs nothing new on any backend: `agentAuthPrelaunchShellFn` derives
  `YOLO_AUTH_PRELAUNCH_<BIN>_{FLAG,PATH}` from `$BIN`, and `GenerateAgentLaunchers` runs
  from `internal/entrypoint/boot.go` and `internal/entrypoint/darwin.go` both.

## Traps

- **A handler cannot tell which socket carried its bytes.** `hostservice`'s own doc states
  it, and `Session.JailID` falls back to the client's *self-asserted* `jail_id` on
  `ServeUnix`. Step 11 therefore built a **second handler** for the private 0600
  `HostSocketPath` rather than inspecting the session. **Constraint, still:** any later
  host-only action goes on that handler, never behind a `JailID` check — that is a
  jail-supplied string.
- **`macos-user` Codex is handed a refresh URL nothing binds.** `CODEX_REFRESH_TOKEN_URL_OVERRIDE`
  is a static pack `env` var and pack env reaches this backend (`packload.EnvVarsFor` →
  `packChannel.launchEnv` → `macosuser.Options.PackEnv` → the sandbox env file), while
  `internal/macosuser` has no `JailDaemon` reader and `YOLO_JAIL_DAEMONS` is emitted only
  into a container argv. Symptom: prelaunch seeds a good `auth.json`, the session works
  until expiry, then every refresh fails against a closed port. Step 9 fixes it. Note that
  1460 is the *machine's* real loopback on this backend, so a manifest-literal port is also
  a collision between two concurrent launches.
- **Apple Container's measurement is negative, and since step 10 the launch says so.**
  `backendInertReason("container")` records the result of
  `integration/applecontainer_test.go`'s `TestAppleContainerReachesHostLoopback` on
  `container` 1.1.0: a container→host connection completes its handshake and carries
  nothing, no bind address helps, and `host.containers.internal` does not resolve. AC still
  gets the endpoint variable and the mount — `hostScopedEndpointIsUnpublishable` withholds
  every host-scoped endpoint on AC **except** this one — and the disposition is `unknown`,
  which never escalates, so the fatal witness does not refuse it. What changed is that
  `notePackLoopholesInert` names the loophole and the measured reason at launch.
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
10. **SHIPPED 2026-09-18 (`36c47baa`), disclosure half.** The `withoutOpenAIAuthPack`
    special case is deleted, so the launch reports the loophole inert with
    `backendInertReason`'s measured reason. Withholding the endpoint variable was declined in
    the same commit (see the Map). Re-running `TestAppleContainerReachesHostLoopback` on the
    Mac runner is how the inert line *expires* later.
11. **SHIPPED 2026-09-18 (`4de78ac0`); public verb 2026-09-20 (`fafb7493`).** A second
    handler on the host socket; `import --from <file>` into `Broker.Replace`; `logout` calling
    `Broker.Logout`; `yolo openai-auth status|import|logout` states logout is machine-wide
    before it deletes anything.
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
- **Unit, steps 10 and 11: shipped with them** — `packhostdisclosure_test.go` for the AC
  inert line, `internal/openaiauthdaemon/hostactions_test.go` and
  `internal/openaiauthhost/operator_test.go` for import and logout.
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
  [`../reference/agent-credentials.md`](../reference/agent-credentials.md). The measured Codex
  version floor is recorded in
  [`../research/openai-subscription-auth.md`](../research/openai-subscription-auth.md) beside
  its 0.154.0 provenance line (2026-09-25).

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

- **Stop and ask: route (a) or (b) for step 9** — filed as [OQ-OA6](openai-auth-broker.md#OQ-OA6). Route (b) is the cheaper path and un-blocks
  this service from the two rulings
  [`jail-daemon-on-macos-user-plan.md`](jail-daemon-on-macos-user-plan.md) is waiting on
  ([OQ-DP8](declaration-parity.md#OQ-DP8), [OQ-DP9](declaration-parity.md#OQ-DP9)) —
  and it is consistent with what already ships, since the loopback-TLS front itself runs
  unconfined in the launcher process on this backend. But it is a second mechanism for one
  manifest key, and whether that is acceptable is exactly what that plan's confinement
  question decides. Do not pick it unilaterally.
- **Stop and ask before refusing an old Codex.** Recording the floor is documentation; a
  launch-time refusal or a `min_version` schema field is new behavior and belongs in the
  design doc first. `internal/packdecl` has no such field today — and the measured floor
  (0.56.0, about a year behind every installable Codex) makes such a refusal dead code.
- **Two flock transactions, by accident rather than by ruling.** The design's
  [§2](openai-auth-broker.md#2-one-writer-and-two-views) says the engine "should be
  generalized once … rather than a second independent implementation"; what shipped is
  `internal/openaiauth` beside `internal/oauthbroker`. Nothing remaining needs it resolved.
  Recorded so the next reader does not believe that sentence.
