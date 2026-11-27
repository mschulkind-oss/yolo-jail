---
title: "Managed MCP sign-in: synthetic acceptance coverage"
date: 2026-11-18
status: accepted
stage: DECIDED
next: "Complete candidate gates and independent review, then obtain separate native/rootless/Mac/provider witnesses before claiming runtime success"
depends-on: [managed-mcp-sign-in.md, managed-mcp-sign-in-plan.md]
tags: [mcp, testing, acceptance, verification]
summary: "A1–A32 executable caller inventory for the landed synthetic implementation, separating source/fixture evidence from native plugin, callback and provider behavior still unverified."
---

# Managed MCP sign-in: synthetic acceptance coverage

**Boundary:** this is an implementation inventory, not a native success claim. The candidate landed in squash `492dab7fa` (2026-11-18).
The [accepted scenarios](managed-mcp-sign-in.md#12-acceptance-scenarios) remain authoritative.
Tasks 1–8 are serial commits through `99130ff312b064b31f78fb46a907519d40037ec4`; Task 9 adds
real account-free jail regressions and repairs the caller gaps they exposed. Final command outcomes,
source-at-probe stamp and retained failures belong to the Task 9 continuation report and ignored
candidate evidence directory `.yolo/durable/managed-mcp-evidence`. Compile/short skips are not
container acceptance; later greens do not erase earlier failures. An independent review of
the landed code and the native witnesses below remain owed.

## A1–A32 caller inventory

Each row names permanent executable drivers, not comment/text assertions. Temporary controls
were restored before gates. Control references name numbered retained receipts, not filename-based
success assumptions; the Task 6/7 reports retain their mislabelled and ordinary fixture failures.

| Scenario | Permanent production-caller evidence | Control / limitation |
| :--- | :--- | :--- |
| A1 | `TestRunResolvesOnlyUserSelectedDeliveredSignInPoints`; `TestManagedMCPHostApplyResolvesOnlyActualWholeTreePlacement`; real integration `TestManagedMCPSignInLaunchPlacesAndDerivesWithoutExecutingAgents` | Tasks 2–3 delivery/selection controls; actual namespace identity is synthetic, not loaded-plugin proof. |
| A2 | Same Run producer matrix; `TestManagedMCPCallbackDisabledOrUndeliveredNeverAcquires`; Task 9 workspace override relaunch | Existing workspace user-scope refusal remains; no illegal full-Run fixture substituted. |
| A3 | `TestMCPProducerCarriesExpectedClientNotJustItsDigest`; `TestServeRefusesExpectedClientBeforeAnyHTTP`; `TestOutcomeExpectedClientRequiresTheNativeClientField` | Task 4 client guard/control and counted synthetic transport. |
| A4 | `TestOutcomeTwoBrokersAdoptOneSingleUseToken`; real framed helper drivers | Synthetic issuer and atomic store only; no Slack/native first login. |
| A5 | `TestRunManagedMCPRouteReachesProductionBootConsumer`; `TestClaudeManagedMCPRouteThroughConfigurePack`; Task 9 actual render integration | Task 7 derive/decoder/consumer controls; actual shipped Lua/surfaces through inert program adapters. |
| A6 | `TestApplyHostManagedMCPRouteUsesDeliveredBundleAndRecordFreshness`; ConfigurePack source/usable-record matrix | Strict canonical read and synthetic storage only. |
| A7 | Task 7 Run/boot backend matrix; `TestHeaderRouteUsesFrozenSourceFactsAndCapabilities` | Static backend seams; no native Apple/Podman-Mac reachability claim. |
| A8 | `TestMCPAuthJSONHeaderThroughMain`; `TestMCPAuthHeaderGrammarRefusesBeforeIssuance`; Task 9 header-only real jail | Bare and JSON privately compared; no token output. |
| A9 | `TestUnexpiredHostAndJailHeadersIgnoreRefreshLock`; `TestMCPAuthHeaderCarriesOneDeadlineThroughDialAndFraming` | Task 5 owner/framing controls and event-driven injected guards. |
| A10 | `TestMCPAuthStatusThroughMainHasNoEffectsOnMissingState`; `TestStatusHandlerStrictLayersAndNoEffects` | Task 6 strict-reader/dispatch controls; no staging, repair, OAuth or provider calls. |
| A11 | `TestStatusHandlerIncludesHeaderOnlyServers`; `TestManagedMCPSignInHeaderOnlyStatusReadsSyntheticRecord` | Task 9 found/fixed real frontend principal mismatch, not a fabricated row. |
| A12 | `TestDiagnosisClosedVerdictsAndNativeRecovery`; `TestMCPAuthDiagnosisWordingThroughMain`; native-recovery framed tests | Task 6 diagnosis/advice controls; ordinary agent sign-in named, no yolo login verb. |
| A13 | `TestOutcomeMalformedUnsafeAndMissingSourcesNeverSignOut`; `TestStatusOutcomeUnreadableAndUnknownVersionAreNotRepaired` | Torn/unsafe source never proves sign-out. |
| A14 | `TestOutcomeTwoBrokersAdoptOneSingleUseToken`; `TestFramedExpiredHeadersCoalesceInTheExistingPoll` | Single-use synthetic refresh, locked reread; no provider concurrency witness. |
| A15 | `TestOutcomeRotatedNotSavedIsObservedNotReconstructed`; `TestOutcomeNoteFailureCannotFailCommittedAdoption` | Persist/note controls; spent token refusal after restart is genuinely observed. |
| A16 | `TestOutcomeLatestNativeSignInAdvancesGeneration` | Synthetic entries/generations only. |
| A17 | `TestManagedMCPCallbackIsOptionalExclusiveAndLoopback`; entrypoint callback DNAT driver | Task 8 controls18 assembly/19 published-port merge; exact IPv4 loopback TCP. Nested cannot prove rootless forwarding. |
| A18 | Task 9 placement→workspace override→bundle withdrawal relaunch; Task 3 removal/record matrix; clean-source sign-out drivers | Withdrawal does not revoke/rewrite retained canonical state. Actual plugin reconnect remains unverified. |
| A19 | Existing host-resident/keeper/Apple/macos-user callers retained; reached five existing integration tests named in plan | Only selected reached tests and fast callers are this checkpoint; full slow/native suite remains parent-owned. |
| A20 | `TestManagedMCPCallbackAttachNeverAcquiresOrMovesExistingLease`; `TestManagedMCPCallbackAcquireCannotStealPendingLaunchBeforeRuntimeCreate`; unsafe/own-cname acquisition drivers | Acquisition holds the previous owner's lifecycle exclusion through absence proof/replacement; a pending runtime-absent Run retains its lease. No attach acquisition; synthetic keeper, not live bridge. |
| A21 | Foreign bind race/synchronous/multiport drivers; `TestManagedMCPCallbackOwnPartialCreateIsStoppedConfirmedGoneThenRetried`; unsafe cleanup negatives | Task 8 controls20 early retry/39 owned-partial caller/40 teardown fence. Known-own cleanup precedes retry; unknown/foreign/failed cleanup preserves evidence. |
| A22 | Confirmed-gone reclaim/independent forget/lifecycle/replacement race drivers | Task 8 controls21/23 release; generation fence, unknown retention, no broad reaper change. |
| A23 | Callback planning/backend matrix; real `TestManagedMCPCallbackLaunchPlanRetiresLease` | Real nested branch is **no publication**; no live rootless/browser witness. |
| A24 | `TestClaudePluginInventoryIdentityShapes`; linked-component/declaration/config-check drivers; actual Run inventory | Unsupported shapes/duplicates/path escapes refuse points; no upstream original executed. |
| A25 | Whole-tree host/jail placement drivers; actual integration byte/licence comparison | Delivered `.mcp.json`/LICENSE compare inside real jail; placement is not loading. |
| A26 | Actual Run secure-storage/view matrix; `TestManagedMCPJailLayerUsesDeclaredIdentityGuard`; ConfigurePack matrix | Source/capture floor preserved, no Mac Keychain adoption added. |
| A27 | `TestExpiredHeaderPollCommitsAfterFramedRequesterCancellation`; framed coalescing; host refusal/Main framing drivers | Task 5 wake/owner controls; durable worker settles independently of cancelled waiter. |
| A28 | Host Main effect-free status/read spies; absent staged tree/strict existing descriptor drivers | Task 6 controls16–18; no resolver materialization/source repair/new poll. |
| A29 | `TestOutcomeIsWrittenUnderTheRecordLockAndReadAfterRestart`; strict outcome reader and status projection drivers | Closed token-free code, no token/hash/provider text; Task 4 note/observer controls. |
| A30 | `TestMCPAuthStatusProvisioningAdviceThroughMain`; `TestStatusHandlerDerivesAdviceOnlyForNoSignInPoint`; `TestFrozenLaunchProducerAdviceThroughAuthenticatedStatus` | Task 6 diagnosis→advice controls; immutable embedded originals only, synthetic non-Slack provisioning fixture. |
| A31 | Main/pure advice capability/client/URL/transport/shadow/order/malformed matrices | No candidate chosen or selected; metadata does not create authority. |
| A32 | Main and framed no-point-only counting filesystem matrices | Unselected user trees never read; other verdicts do not read embedded originals. |

## Independent-review repair checkpoint

- The pending-launch acquisition regression pauses actual Run at the keeper-spawn event after lease
  registration, with its lifecycle gate held and runtime absent. A competing Run must retain the
  original bytes and report the original owner, without acquiring a lease or publishing its port.
  Acquisition uses nonblocking cross-owner lifecycle exclusion under the port lock, retained through
  confirmed absence and replacement. Same-cname reuse requires the actual safe already-held descriptor;
  unsafe gates fail closed. Retirement/attach/partial-cleanup rules are unchanged.
- `TestManagedMCPQualifiedSourceThroughTOMLShareAdoptionAndHostHeader` uses actual qualified TOML
  selection, inert host apply, validated private share, exact shipped Claude Lua match, a private
  synthetic issuer and actual host Main header issuance. `Source.Pack` stays logical provenance/script
  key; its additive internal `Adapter` is the selected program's exact Lua registration. Old absent
  adapters retain leaf-only behavior; `Replica.Pack` stays a filesystem leaf. No record/digest/source-key
  migration or unselected built-in fallback is introduced. Unsupported qualified adapters are not
  capturable and stay plain without a separately usable authorized canonical record.
- `TestOnceQualifiedSameLeafSourcesKeepScriptsAndAdoptionDistinct` exercises separate selected logical
  names/scripts/private roots with the same native key and copied grant, including a nil match,
  different URL/client/issuer, genuinely different grant, and mutation at source-lock/refresh events.
  Current source match, exact adoption URL semantics and issuer/client fences precede source-derived
  refresh or native writeback. A mismatched source neither receives tokens nor revokes canonical state;
  canonical-only refresh and legitimate compatible same-grant writeback remain available.
- Repair receipts under `.yolo/durable/managed-mcp-evidence/review-repairs` retain actual REDs and restored
  independent deletion controls. Earlier fixture/compile failures and the incorrect byte-normalization
  hypothesis remain failures, not acceptance REDs. These are synthetic repair checks; an independent
  review of the landed code and native/rootless/plugin/provider witnesses remain owed.

## Task 9 caller repairs and controls

- Real jail status/header exposed two different identities: container cname in `Share.Jail`, and the
  authenticated frontend publication principal. The backend socket is deliberately outside the
  endpoint directory. Core overwrites a host-child-only publication locator after inherited/manifest
  env; Main binds its exact existing frontend derivation and trusted container name to the validated
  private share. Share version/meaning, authentication, private ownership and UID/path checks stay intact.
- `TestMCPAuthShareSessionThroughProducerMainAndAuthenticatedFront` uses the actual share producer,
  `startExternalService`, native Main through production self-exec and real authenticated framing.
  It tests inherited/manifest override attempts, correct bare/status, foreign/recreated principals,
  wrong share, spoofed payload, wrong token and nonfronted refusal. Controls17–19 delete exact principal
  check, publication producer and Main binding separately; each executed red before native restoration.
- Valid frozen eligibility now fences nonsource helper rendering. Matching server/effective agent
  suffices without point/capture/bundle; valid-empty/wrong-agent/wrong-server remains plain. Truly absent
  legacy input preserves baseline. Supplied-invalid input remains distinguishable and fails closed.
  `TestPiManagedMCPFrozenEligibilityAndSuppliedInvalidFacts` reaches the real ConfigurePack decoder/derive;
  controls24–26 delete eligibility, derive caller and invalid/absent distinction independently.
- Integration adapters preserve exact shipped consumer Lua and metadata but replace vendor program
  recipes with inert local declarations. New fixtures prove the effective graph is synthetic-only
  before invocation; readiness and auto-capture are disabled. Internal synthetic core provisioning ran;
  external/vendor hooks, agents, installers and auto-capture did not.
- Existing legacy leaf-name selection is used privately because readable custom handles containing `/`
  fail the unchanged MCP source identity validator. Retained receipt07 is a production compatibility gap,
  not fixture green or authorization for identity migration; combined TOML/MCP review must assess it.

## Witnesses still owed

Native plugin loading/resolved-name visibility, first OAuth sign-in/paste, reconnect/401 helper rerun,
no own-OAuth flow, source behavior after refresh-token removal, writeback and public-client/provider
acceptance remain unmeasured. Real rootless bridge callback (including IPv4/IPv6 browser preference),
Mac file/Keychain storage and native backend lifecycle witnesses cannot be supplied by nested host-net
Podman or static seams. Actual observed nested runtime is rootful; it is not a rootless witness.
Codex helper/version and native-origin proof remain blocked independently. Failed Claude native helper
measurement withdraws only its header declaration for next-launch baseline; it does not erase placement,
status/outcome evidence or trust disclosure.
