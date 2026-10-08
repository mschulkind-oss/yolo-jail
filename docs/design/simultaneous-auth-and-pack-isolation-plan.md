---
title: "Implementation handoff: Pi active-provider request policy"
date: 2026-10-07
status: accepted
stage: DECIDED
next: "Pin/replay the declared Pi source and patch series, then add failing supported-runtime denial tests before the policy seam"
tags: [pi, implementation-plan, providers]
---

# Implementation handoff: Pi active-provider request policy

**Status:** 2026-10-07. Policy ruled; runtime enforcement unbuilt. Yolo map checked at
`931489400b4ce7334a0731a47a47c116f6e44ab2`; installed Pi 1.0.4 docs/declarations/dispatch inspected read-only.
**Design:** [simultaneous auth and pack isolation](simultaneous-auth-and-pack-isolation.md).
Precedence: design wins on behavior, source tree wins on fact, this plan is advice and the first to rot.

## Repository and deliverable ownership

The configured **pi-fork** source pack declares
`git+https://github.com/earendil-works/pi?ref=main`, `fork_of: pi`, a `patches` directory, and
`npm ci --ignore-scripts --no-audit --no-fund && node scripts/fork-build.mjs`.
It is **upstream plus a patch series**, not evidence of a distinct GitHub fork repository.
The inspected series' first patch declares base
[`7c10bd4337495ee613f2224843ecdf349b80d1df`](https://github.com/earendil-works/pi/commit/7c10bd4337495ee613f2224843ecdf349b80d1df);
that is patch metadata, **not the installed build's established upstream revision**.

The Pi API writer works in a **parent-allocated writable durable upstream checkout**, with the
configured series replayed and exact upstream/series hashes recorded by a source scout. Deliver
new pack-relative format patches in a separate writable candidate. Never edit installed Pi or the
read-only deployment pack. Host promotion/install/restart is a later explicit operational step.
The ordinary yolo Pi pack remains responsible for profiles/settings/launch wiring in this repository.

## Exact seams, checked in installed source

Source paths below are in [earendil-works/pi](https://github.com/earendil-works/pi), not yolo paths.
Installed `dist` counterparts and exported declarations were inspected; recheck mapped source on replay.

| Source | Grounding / proposed work |
| :--- | :--- |
| `packages/coding-agent/src/core/model-runtime.ts` | `prepareRequest` gets provider then calls `getAuth`, then dispatches. Add the final provider check **before its request auth**, plus guard earlier request-auth entry through runtime `getAuth`; dispatch-only denial is too late for preflights. `stream`, `streamSimple`, deferred, image and classifier dispatch converge here |
| `packages/coding-agent/src/core/model-registry.ts` | Supported extension facade delegates to the runtime; expose a dedicated policy-registration contract, not method monkeypatches |
| `packages/coding-agent/src/core/extensions/types.ts`, `runner.ts` | Current `before_provider_request` is payload-only; runner catches errors and continues. New policy must carry actual model/provider/operation and **deny on handler error**, separate from notification events |
| `packages/coding-agent/src/core/sdk.ts`, `agent-session.ts` | SDK dispatch wraps runtime `streamSimple`, but session `_getRequiredRequestAuth`/`_getSummarizationRequestAuth` call `getAuth` beforehand. Gate those preflights before credential work; the summary auth-error catch must rethrow policy denial. Main agent, summaries/compaction and session replacement share invocation policy; retries/continuations cannot reset it |
| `packages/coding-agent/src/core/cache-warmer.ts` | Warming uses the runtime; verify its real caller against the common gate, not merely a helper test |
| `packages/coding-agent/src/core/virtual-models.ts` | Check final physical dispatch and router auxiliary model calls. Logical selected provider is insufficient |
| `packages/ai/src/api/lazy.ts`, `packages/ai/src/utils/model-operations.ts` | Installed `pi-ai` source maps identify package-relative `src/api/lazy.ts` and `src/utils/model-operations.ts`; verify these monorepo paths during pinned replay. Existing lazy chat/deferred setup and image/classifier result conversions preserve error text but erase typed denial code/retryability. Define supported stream/result policy-error propagation, not just a thrown exception |
| `packages/coding-agent/src/core/agent-session.ts` and actual retry/error consumers | Installed session passes assistant messages to retry classification. Trace and verify every supported retry/error consumer during replay; preserve non-retryable denial through conversion and consumption, while keeping native/no-profile ordinary-error compatibility |
| `packages/coding-agent/src/index.ts` and public docs | Export the supported capability/API and document scope/version; metadata observation is not a deny API |

The inspected `CreateModelRuntimeOptions` and `ExtensionAPI` declare no request-policy registration
hook. Existing provider registration can wrap named providers/APIs but restores native built-ins on
unregister and cannot cover newly registered providers or all operation kinds. Do not ship that as isolation.

## Yolo seams after the Pi contract exists

| Yolo file / fixture | Reuse and constraint |
| :--- | :--- |
| [`packs/pi/derive.lua`](../../packs/pi/derive.lua) | `ctx.selected_provider`, `ctx.active_set`, `piProfileFor` and `piSetHas` already distinguish selection from catalog. Emit an explicit normalized Pi-ID allow set, not all dependency providers |
| [`packs/pi/pack.json`](../../packs/pi/pack.json) | `launch_selection.surfaces` already transports per-invocation model-list data for host overrides. Add policy data through declarations; core gets no Pi-name branch |
| [`yolo-model-lists.js`](../../packs/pi/extensions/yolo-model-lists.js), [`yolo-openai-auth.js`](../../packs/pi/extensions/yolo-openai-auth.js) | Existing model-list enforcement remains independent. New policy extension/data reader can use the dedicated API, never clear native credentials or omit an override to imply denial |
| [`internal/cli/host.go`](../../internal/cli/host.go), [`internal/entrypoint`](../../internal/entrypoint) | Verify managed launch/capability transport through real existing declaration callers; don't hardcode provider authority into core |
| [`internal/packload`](../../internal/packload), [`integration/providers_test.go`](../../integration/providers_test.go) | Reuse derive/launch fixtures and source-call integration patterns; enumerate actual affected tests before edits |

Advice: represent policy as a versioned tri-state document: native/no-profile, active+nonempty IDs,
or invalid. Explicit active-empty/malformed must never become unrestricted. Numeric limits/schema
spelling are engineering choices; record/validate them in the dedicated API contract.

## Bounded next Luna contracts

1. **PAS-A — Pi fail-closed API/common boundary.** Scout resolves exact writable source/replay first.
   Add fake providers and an in-memory credential store; genuine baseline red through actual runtime
   dispatch. Add policy capability, install it before any managed request, and test unregister/reload/
   SDK session replacement without clearing the invocation requirement. A policy extension being
   disabled must produce managed refusal, not bypass; implementation must prove how CLI/child SDK
   runtimes retain this requirement independently of discovery. Choose the smallest source API shape
   that satisfies the design; do not change existing notification semantics to invent a gate.
2. **PAS-B — yolo projection/transport.** Only after PAS-A's tested contract is fixed. Add derive/
   launch tests first for one/many/duplicate providers, built-in ID mapping, no profile, invalid policy,
   host `-p` override, nested child and explicit outside-set selection. Real production launch callers
   must pass the exact invocation data without persistent settings/auth writes. Managed capability
   failure names the compatible Pi build/restart step.
3. **Parent integration/host rollout.** Independently review both source and patch export; rebuild
   from declared upstream+series and verify installed capability. No API call or interactive agent
   test is needed for local denial proof. Native/rootless/live auth acceptance remains separate.

## Required proof and mutation matrix

- Fake saved OAuth/API credentials and refresh-command sentinels: outside-set calls yield a typed,
  non-retryable denial; resolver/refresh/transport counters remain zero; stored fixture bytes unchanged.
- Allow-set calls retain each provider's normal auth/options, models and error behavior. No profile
  matches native behavior. Duplicate profiles deduplicate; invalid/empty active policy fails closed.
- Main assistant, continuation, retry, compaction, branch summary, cache warming, facade nested chat,
  codemode image/classifier, deferred fetch/cancel and virtual routes each exercise the **actual caller**.
- Test policy-denial propagation through actual runtime lazy streams, image/classifier results and
  session/auxiliary retry/error consumers. Installed-source-informed conversion evidence is only a
  handoff obligation: no runtime gate exists yet, and converter-helper greens alone are not proof.
  Mutate erased denial metadata and retry classification separately; native/no-profile ordinary
  errors must retain compatible behavior.
- Mutate dispatch gate deletion, preflight-auth guard deletion, summary-denial fallback, pre/post-auth
  ordering, final-route check, malformed fallback, child transport and capability refusal separately. Preserve exact patches/red logs/restored greens, not assertions
  of picker contents or source-order greps as request-denial evidence.
- Focused source tests use the replayed package's own test command for named new files; the scout
  records it before handoff. Focused yolo tests run only affected packages/patterns. Parent owns full
  combined landing gates. Do not run native/AWS/model-account checks from Linux and call them proof.

## Don't

No installed-file edits, login deletion, token import, broker grant widening, live settings, guessed
fork revision, publication or host promotion. Deliberate raw `pi-ai`/HTTP bypasses are outside this
supported-runtime policy; do not sell it as same-user confinement or hide them with a fake menu filter.
