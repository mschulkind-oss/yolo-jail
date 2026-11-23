---
title: "Implementation plan: scoped operation-broker policy"
status: accepted
stage: DECIDED
next: "Candidate admitted on branch broker-candidate-admit and U1 reds recorded (U2 and the Appendix C holder/selector reds green there, never code-reviewed); next unit is U4 completion (startup placement test, timed-read caps), then U7 and U8"
depends-on:
  - scoped-broker-policy.md
  - pack-brokers.md
  - github-write-scopes.md
  - boundary-broker.md
tags: [implementation-plan, brokers, approvals, scopes]
summary: "Source-grounded implementation units for the common Git, GitHub, AWS and CircleCI operation policy; no implementation or live authority changes performed."
---

# Implementation plan: scoped operation-broker policy

**Written against:** `62c2f84aa6b294cae0272e73007b2871a83de5db`, 2026-11-17. The publication baseline has the same runtime trees as the planner's `15da1299ffe8c65db4a3f2beef9f8421dabbb99c` source-read baseline.
**Design:** [Scoped broker policy](scoped-broker-policy.md), accepted but unbuilt; [SBP-D1–D37](scoped-broker-policy.md#decision-ledger) and closed [OQ-SBP1=B](scoped-broker-policy.md#OQ-SBP1) are binding. **Designer:** the original design author. **Planner:** this source-read/artifact-only pass. **Implementer:** a future independently materialized candidate writer, not this session.

**Readiness:** complete, focused-reviewed build hand-off; accepted and unbuilt. This revision incorporates all six major findings and R1–R7 of the accepted author repair, its fixed V6/V7 fallbacks, all 144 acceptance IDs and the five focused-review publication corrections. Candidate admission and U1 remain the first implementation steps. Publication is not code authorization, runtime acceptance or reference graduation; fresh independent code review and applicable final-tree gates remain required.

**Provenance:** contract was read from fixed design commit `7883c182dd61aaf53d0b3e0c934f39ddfc569bff` plus the accepted author review-repair fragment; the changing primary design was not used. The revival baseline was docs-only `2ce7f706a33836f98e0ee3dbac85eeb6893d369c`; the parent subsequently published the accepted repair at `192c5aac5122f33907a352d8f10c67f437095023`. The publisher read the complete repaired hand-off, accepted repair and focused confirmation against the fixed publication baseline. Runtime tree identities remain `internal=5e36cf997de30ff0b8be5182555f2eba210709e7`, `packs=ee6eb4f5fc7f3bc6cff6f39b6e2dd8476a9e5bcc`, `cmd=f68ff884b6701d2e8ac752eb86e4328bb861709c`. Detailed private receipts and fragment provenance remain in the coordinator's ignored evidence report, not this public handoff.

## Precedence and fences

1. The accepted design wins on behavior. Its corrective overlay supersedes earlier fact-read and gate wording; the research packet is evidence, not authority.
2. The tree wins on cosmetic facts only. This plan is the first thing to become stale; do not bend production to fit it.
3. Retain failure receipts, diagnose ordinary deterministic failures and repair proportionately within the approved fence; an ordinary gate failure is not a new approval checkpoint. Stop and report a real external/runtime blocker, another writer's task-path changes, a missing required source identity or authority conflict, or a necessary change outside the approved boundary. Never weaken a gate or widen authority to obtain green.
4. **Constraint, tier 1:** the future writer works in its own independently materialized candidate. It must not touch or duplicate the independently owned TOML scoped-pack work or its candidate. **Tier 3 operational fence:** no push, PR, publication, release, deployment, CHANGELOG, live host config/grant/profile/credential/IAM mutation, real agent/installer, authenticated provider/API call, or child fanout. Commits belong to the future authorized candidate writer, not this read-only planning pass.
5. **Constraint:** new configuration is TOML only. Preserve legacy reading and old authority exactly; do not teach JSONC new scopes, `never`, scoped read tables or contexts. Internal JSON records/protocols are not legacy user configuration.
6. **Constraint:** pack selection, scope labels, routes, declarations and requestable defaults never grant operation authority. Unknown/removed/duplicate sources remain inventory-and-deny only. Trust read/exec disclosures remain mandatory; no quiet mode.
7. **Constraint:** preserve caller HOME, TMPDIR, GOENV and shared Go caches. Test fixtures may isolate their own synthetic HOME through existing helpers; compilers retain the original Go environment. Manual evidence/scratch is owner-created, durable and ignored, never a hand-created temporary-system directory. Private evidence paths are reported separately, not published here.

## Baseline, grounding and actual verification

The accepted design, both author closures, [source grounding](../research/scoped-broker-policy-grounding.md), the original complete plan and its six-major independent review were read. Only affected source seams/probes were revisited for this bounded repair. This does not re-inventory or implement the subsystem.

| Fact / control | Recorded evidence and fixed outcome |
| :--- | :--- |
| V1 / Appendix A | Revised registered editing compiles/red: scopes.reports.allow.never has no readable schema target. The fixture now adds `scopes.reports.allow.never all`, not a token already in that scope's other list; its working_set ask is seeded so adding all.put_objects remains valid. Never conflicts remain load errors. |
| V2 | Retained deadline spy proves acknowledgement deadline clears; preserve 30 s fetch hold and immediate-pending fallback if an actual helper differs. |
| V3 | Retained real GitHub ask sends six facts in one invocation; implement shared invocation facts or refuse before fact five. Never increase four. |
| V4 | Retained no-root generic call starts classifier once before refusing. Implement pre-classifier guard. |
| V5 | Retained finite approval fronts refuse rather than clamp oversize durations. Known-write-only native messages/maxima stay on their shipped path; new read/withheld roles always refuse above the new minimum. |
| V6 | Actual shipped git manifest projected through the production readable-declaration adapter, with actual parsed TOML: fetch lists/ask key accepted; literal read and fetch+read refused. Same fetch in both always/on_request remains accepted. Thus new alias collision rule applies; fallback is not needed here. If another shipped native adapter accepted both words, preserve its no-never pair and error only when never is involved. |
| V7 | Actual unchanged store on git/github source fixtures rereads committed timed call:read and call:merge entries plus native legacy eleven-hour entries; snapshot/list retain all records and expiry. Accepted kinds are available, so native timed reads need not fall back to once-only. If another old shared store cannot accept them, refuse its native read --for and offer once. |
| A2a/A2b | Real AWS declaration and production EffectiveCallBrokerCells/CallBrokerCurrentCeiling freeze same-cell always+on_request and broad always+cell ask as always/run. Valid fixtures explicitly keep working_set≥all. Git native same-word list pair is valid. |
| Audio baseline (A2c) | Audio's old same-word lists remain valid; never remains an unknown schema key. No generic grant-list rewrite. |
| GitHub validator (C7m) | Shipped GitHub declaration + production validator pins canonical tokens exactly read, create_pr, create_issue, runs, approve_runs; all, working_set and cell paths are not declared. Existing legacy alias handling remains separate/unchanged. |
| Holder/selector reds | Exact same-workspace fresh-holder regression: effects=0, uses=1; working-set-only prediction runs, transaction refuses with effects=0. Their retained full sources are unchanged in Appendix C. |

The initial public config-check probe had **no selected-pack coverage**, so its misleading successful token checks prove no authority and are not V6/token evidence. The selected-declaration resolver probes above are the correct source-grounded parser/algorithm witnesses; permanent public tests must also assert actual selected-source coverage. Earlier failed probes and corrected-fixture runs are preserved separately. No whole-tree, integration, census, native-runtime or rendered-document check is claimed. Structural/traceability/link checks are not a rendered Vantage gate.

**First permanent reds:** copy Appendix A/C to their named candidate test files and run the exact named tests. Keep the pre-fix receipt, then land each with the green fixing slice; no deliberately red commit. Baseline code identities above let independent review distinguish retained reds from feature evidence.

## Interface and ownership map

Existing paths below were inspected by source read; new paths are explicitly marked. File grouping is one ownership seam, not permission to rewrite the package. Permanent test files listed under tasks are new unless specifically identified as existing fixtures.

| Owner / paths | Existing contract and planned change | Unit |
| :--- | :--- | :--- |
| [`internal/loopholedecl/callbroker.go`](../../internal/loopholedecl/callbroker.go), [`internal/loopholedecl/callbroker_json.go`](../../internal/loopholedecl/callbroker_json.go); [`internal/config/readabledeclarations.go`](../../internal/config/readabledeclarations.go) | Strict declaration keys, Type/Parent resources, write cells/defaults/max_for, lookups, hashes. Add read-cell kind, exact/segment matching metadata and declaration-owned nonsecret context/route_anchors metadata; clone/hash/disclose every new field. Preserve legacy manifest reading without new legacy user-config features. | U2, U3 |
| `internal/brokerpolicy/policy.go`, `internal/brokerpolicy/resource.go` **new** | Immutable rules/operations, authored exact/segment matcher/order, one pure decision/table selector. No config/store/env/provider dependency; one matcher serves config, runtime, route and check. | U2, U3 |
| [`internal/loopholedecl/brokered.go`](../../internal/loopholedecl/brokered.go); selected native declaration readers in config/approvesources | TOML-only optional read_max_for parsed/cloned/hashed/disclosed; missing legacy/native key uses the existing shared 15 m constant. Official native provenance remains required. Shipped git/github manifests need no cap line. | U2, U4 |
| [`internal/config/schema.go`](../../internal/config/schema.go), [`internal/config/newreadableconfig.go`](../../internal/config/newreadableconfig.go), [`internal/config/readablevalidate.go`](../../internal/config/readablevalidate.go), [`internal/config/readableallow.go`](../../internal/config/readableallow.go), [`internal/config/readablenamed.go`](../../internal/config/readablenamed.go), [`internal/config/readablemerge.go`](../../internal/config/readablemerge.go), [`internal/config/readablescope.go`](../../internal/config/readablescope.go), [`internal/config/callbroker.go`](../../internal/config/callbroker.go), [`internal/config/callbrokerpolicy.go`](../../internal/config/callbrokerpolicy.go); `internal/config/scopedbrokerpolicy.go` **new** | TOML extraction, source/resource-alias duplicate detection, provenance, host-only contexts/routes, explicit `never`, read aliases and read tables, shared resolver inputs; retain legacy broad/default adapters. | U2, U3 |
| [`internal/config/gateread.go`](../../internal/config/gateread.go), [`internal/config/scopeapproval.go`](../../internal/config/scopeapproval.go); `internal/config/brokerpolicymarker.go` **new** | Gate reads workspace-authored asks once; roots retain literal source filenames. Add model marker and held-back write additions; separate inactive/no-root from active declaration diff. | U5 |
| [`internal/callbrokerinput/launch.go`](../../internal/callbrokerinput/launch.go); [`internal/cli/run/launchinput.go`](../../internal/cli/run/launchinput.go), [`internal/cli/run/callbrokerpolicy.go`](../../internal/cli/run/callbrokerpolicy.go), [`internal/cli/run/privatehttpclients.go`](../../internal/cli/run/privatehttpclients.go) | Step `yolo.call-broker-launch/2` to `/3`; freeze only declaration/program/classifier/env-name contracts. Publish live policy locator rather than EffectiveCells workspace authority. Update all version checks together. | U6 |
| [`internal/brokerscope/live.go`](../../internal/brokerscope/live.go); `internal/brokerscope/policy.go`, `internal/brokerscope/consumer.go` **new**, corresponding scope-file definition in [`internal/brokerscope/file.go`](../../internal/brokerscope/file.go) | Same source-lock namespace; exclusive all-ask snapshot, atomic roots locator, sequence-aware refusal and daemon-produced live consumer proof. Changed asks block nonconsuming launches before every effect. Keep native scope form and exact URLs. | U6 |
| [`internal/cli/run/liveapply.go`](../../internal/cli/run/liveapply.go), [`internal/cli/run/liveapplygrants.go`](../../internal/cli/run/liveapplygrants.go), [`internal/cli/run/liveapplyplan.go`](../../internal/cli/run/liveapplyplan.go); `internal/cli/run/brokerpolicyapply.go` **new** | Gate-approved cell/scoped-ask publication and truthful status, including source≠pack; install/marker failures. Raw workspace changes never mutate live authority. | U6 |
| [`internal/brokerstore/store.go`](../../internal/brokerstore/store.go), [`internal/brokerstore/working.go`](../../internal/brokerstore/working.go); `internal/brokerstore/operation.go` **new** | Extend requests with optional ordered reads, unresolved candidates/fact flag and nondefault context identity. Extend existing timed entry encoding and locked callback view; digest extensions only when new fields are present. No second store. | U4 |
| [`internal/callbroker/classifier.go`](../../internal/callbroker/classifier.go), [`internal/callbroker/http.go`](../../internal/callbroker/http.go), [`internal/callbroker/http_schema.go`](../../internal/callbroker/http_schema.go), [`internal/callbroker/http_native.go`](../../internal/callbroker/http_native.go) | Versioned typed reads/descriptions, mixed bundles, zero/one/many legacy read adapter; retain bound lookup/native ancestry verification, strict JSON and HTTP method floors. | U3, U7 |
| [`internal/callbroker/decision.go`](../../internal/callbroker/decision.go), [`internal/callbroker/handler.go`](../../internal/callbroker/handler.go), [`internal/callbroker/authority.go`](../../internal/callbroker/authority.go), [`internal/callbroker/boundwrite.go`](../../internal/callbroker/boundwrite.go), [`internal/callbroker/http_handler.go`](../../internal/callbroker/http_handler.go), [`internal/callbroker/http_write.go`](../../internal/callbroker/http_write.go), [`internal/callbroker/timed.go`](../../internal/callbroker/timed.go) | Drive all operations through common policy and one transaction. Replay under caller budget; rebound parent copy; current working-set selector at every boundary; read/mixed exact/timed answers. | U4, U7 |
| [`internal/callbroker/daemon.go`](../../internal/callbroker/daemon.go), [`internal/callbroker/forward.go`](../../internal/callbroker/forward.go), [`internal/callbroker/httpfront.go`](../../internal/callbroker/httpfront.go); `internal/callbroker/hold.go` **new** | Real Main hook/publication/End parity; 30 s hold outside lock, cancellation, separate execute budget and 100 s generic connection deadline. | U8 |
| [`internal/callbroker/executor.go`](../../internal/callbroker/executor.go); `internal/callbroker/context.go` **new** | Resolved native placement check, copy captured environment per call; generic declared context set/unset and value redaction. No AWS-specific core branch. | U4, U9 |
| [`internal/brokeraudit/brokeraudit.go`](../../internal/brokeraudit/brokeraudit.go); [`internal/callbroker/audit.go`](../../internal/callbroker/audit.go) | Optional unresolved-candidate/context-name evidence, required authorization-before-effect events and source-neutral redaction; no profile fields/provider facts in stored requests. Preserve MCP event fields and avoid cross-feature rewrite. | U4, U7, U11 |
| [`internal/cli/approvesources.go`](../../internal/cli/approvesources.go), [`internal/cli/approve.go`](../../internal/cli/approve.go), [`internal/cli/configwhy.go`](../../internal/cli/configwhy.go), [`internal/cli/configref.go`](../../internal/cli/configref.go), `config_ref.txt` | Existing selected-source adapters, read/mixed summary and timed limits, context name only, policy/refusal inspection and editing advice. Unknown/native-impostor sources never gain an adapter. | U4, U9, U12 |
| [`internal/gitbroker/broker.go`](../../internal/gitbroker/broker.go), [`internal/gitbroker/https.go`](../../internal/gitbroker/https.go), [`internal/gitbroker/upstream.go`](../../internal/gitbroker/upstream.go), [`internal/gitbroker/approval.go`](../../internal/gitbroker/approval.go), [`internal/gitbroker/approvalauthority.go`](../../internal/gitbroker/approvalauthority.go), [`internal/gitbroker/policy.go`](../../internal/gitbroker/policy.go), [`internal/gitbroker/importdecision.go`](../../internal/gitbroker/importdecision.go), [`internal/gitbroker/importfetch.go`](../../internal/gitbroker/importfetch.go); `internal/gitbroker/fetchpolicy.go` **new** | Preserve native transport/push grammar; decide fetch at every credential boundary, SSH before resolution/spawn, hold credential asks, imports never file. Retire read on-switch only after marked gate approval. | U10 |
| [`internal/skillimports/resolver.go`](../../internal/skillimports/resolver.go), [`internal/skillimports/project.go`](../../internal/skillimports/project.go); [`internal/cli/run/workspaceskills.go`](../../internal/cli/run/workspaceskills.go) | Carry the approved/current Git fetch decision into import/cache use without removing the separate skill-registration gate or authenticated-cache class fence. | U10 |
| [`internal/ghbroker/authority.go`](../../internal/ghbroker/authority.go), [`internal/ghbroker/daemon.go`](../../internal/ghbroker/daemon.go), [`internal/ghbroker/access.go`](../../internal/ghbroker/access.go), [`internal/ghbroker/evaluate.go`](../../internal/ghbroker/evaluate.go), [`internal/ghbroker/window.go`](../../internal/ghbroker/window.go), [`internal/ghbroker/workingset.go`](../../internal/ghbroker/workingset.go), [`internal/ghbroker/dispatch.go`](../../internal/ghbroker/dispatch.go), [`internal/ghbroker/runs.go`](../../internal/ghbroker/runs.go), [`internal/ghbroker/approverun.go`](../../internal/ghbroker/approverun.go), [`internal/ghbroker/cells.go`](../../internal/ghbroker/cells.go), [`internal/ghbroker/approvalstore.go`](../../internal/ghbroker/approvalstore.go); `internal/ghbroker/factpolicy.go` **new** | Native adapter: one repository read cell, existing shipped write capability/default mapping, bounded candidate-aware facts and 30 s fresh post-answer execution. No unbuilt GitHub tables/verbs become implemented. | U11 |
| [`internal/awsclassify/classifier.go`](../../internal/awsclassify/classifier.go), [`internal/awsclassify/ce.go`](../../internal/awsclassify/ce.go); [`internal/circleciclassify/classifier.go`](../../internal/circleciclassify/classifier.go); [`packs/aws/loopholes/aws-broker/loophole.toml`](../../packs/aws/loopholes/aws-broker/loophole.toml), [`packs/circleci/loopholes/circleci-broker/loophole.toml`](../../packs/circleci/loopholes/circleci-broker/loophole.toml) | Label supported reads; declare AWS segment scope anchors, context profile/env removals and S3 route_anchors; CircleCI listing read cell, reruns still classifier-refused. Keep exact CE/S3 grammar. | U3, U9 |
| [`internal/changetiming/changetiming.go`](../../internal/changetiming/changetiming.go); `internal/cli/check/` existing reporting entry points | Scoped asks/cells classified as live only when actually applied; inert/out-of-root scopes and declared-unimplemented cells inspectable, writes-remaining-after-read-withdrawal report. Do not alter `awsbypass.go`. | U6, U12 |
| [`integration/aws_broker_test.go`](../../integration/aws_broker_test.go), [`integration/aws_ce_broker_test.go`](../../integration/aws_ce_broker_test.go), [`integration/circleci_broker_test.go`](../../integration/circleci_broker_test.go), [`integration/gitbroker_test.go`](../../integration/gitbroker_test.go), [`integration/githubbroker_test.go`](../../integration/githubbroker_test.go); `integration/scoped_broker_policy_test.go` **new** | Fake-executable launch/apply/front coverage. Existing network/rootless/Mac blind spots remain explicit. | U12 |
| Canonical overlapping references; [`scripts/completion-readers.json`](../../scripts/completion-readers.json) | Correct claims after behavior ships; regenerate census once on the final candidate feature tree. No CHANGELOG. | U12 |

[`internal/brokerscope/file.go`](../../internal/brokerscope/file.go) owns lenient FileVersion=1 and source/form identities. An old broker **does** ignore unknown snapshot fields; never assume it fails closed. New readers fail closed on invalid binding, while U6 proves consumption before publishing changed asks. Legacy nonconsumers with roots-only changes keep the shipped file/apply behavior. Do not casually bump shared native schemas. [`internal/callbrokerinput/publication.go`](../../internal/callbrokerinput/publication.go) currently has an empty published HTTP-client cell list: this feature must not change it or publish a new client. Existing sealing/acquisition/source-origin/native-verification gates are retained. Do not add a second declaration registry or reserve all embedded source names.


### Shared implementation shapes (mechanics, not new policy)

- **Advice:** keep the pure evaluator in a small dependency-leaf package so config, native adapters and generic runtime cannot drift by copying truth tables. Its inputs are immutable typed rules, operations, admitted chains, working membership, timed entries, context identity and supplied `now`; its output contains per-operation governing-line provenance and the strictest bundle decision.
- **Contract shape:** `Operation = kind + cell + full chain + description + optional candidates/fact-read marker`. Write table is selected once per operation from live non-`call:` membership; read has no table. The classifier never sees rules, context fields, profile names, host environment or authority-bearing snapshot paths. At an effect boundary use one fresh immutable user-config read for ceiling, routes and contexts together, not separate loads per operation that could assemble a mixed-generation bundle.
- **Contract shape:** native adapters retain transport/canonical intent. Git branch/tag/push rights remain native; GitHub maps only implemented cells/grants/defaults. Core generic classification is not a replacement for native grammar or provider-specific ancestry checks.
- **Contract shape:** existing transaction callbacks obtain a read-only working-entry/clock view while the store already owns the source lock. Supply this view to both revalidation and coverage. Never call `Inspect`, `Working` or another locking API from that callback. Existing `Revalidate func(Request)` callers retain adapters; new bounded paths close over caller context and the locked view instead of `context.Background()`.
- **Advice:** a call-local assessment value carries verified facts, candidate decisions and a counter; discard it at return. This prevents duplicate `evaluateWrite`/`fileAsk` fact reads without creating a reusable authorization cache. A source can still hit the fifth-read fallback; no fact counter reset at approval/dispatch.
- **Cheap implementer choices:** private helper names, enum representation, slice-vs-map indexing and test-file subdivisions. **Not cheap:** canonical matching, digest bytes, any authority default, record migration, callback lock ownership, source provenance, context identity or fact-read reachability. Do not improvise those.

## Lifetimes, versioning and compatibility

| Object | Binding / write authority | Lifetime / invalidation / compatibility |
| :--- | :--- | :--- |
| Source declaration | Selected owner, staged identities, full hash of authored optional metadata; derived implicit read is never included | Immutable for launch. Active changed declaration requires gate `y`; until then refuse old and new. Inactive declaration alone records/prompts nothing. Duplicate owners refuse. |
| Launch input `/3` | Private regular 0600, O_NOFOLLOW read, exact original bytes; source/pack/launch and program/classifier/env-name identities | No workspace asks or ceiling/context values. `/2` and `/3` never pair across versions; name relaunch. Update HTTP front/input validators as well as CLI. No silent root-bearing format migration. |
| Live policy snapshot | Gate-owned every workspace ask, source-scope read included, model marker and current roots; workspace/source/launch/declaration hash, monotonically increasing per-launch sequence | Read afresh under source lock at decide, file, approve, late coverage, spend and dispatch. A raw edit changes none of it. Ceiling/routes/contexts share one fresh immutable user read; snapshot is the only workspace-ask source. Separate grant files are never consulted for snapshot-held asks. |
| Snapshot publication | Immutable private sidecar containing complete roots+asks, then one atomic scope-file commit of its locator/digest/sequence and mirrored roots under the source lock | Readers accept only matching complete snapshots and matching scope roots. Unreferenced preparation file is not authority. Never expose separately published roots and asks. Ancillary grant writes precede scope commit; every source apply failure writes refusal under the same lock. Marker-failure output distinguishes old/new/unknown committed authority, never falsely old after install; name jail stop. Cleanup only files this launch/publication owns. |
| Scope/refusal state | Existing random launch identity/PID/workspace/form and same source lock | Foreign binding, missing sidecar, corrupt sequence/digest, malformed policy, lost lease or refusal newer than committed sequence refuses before any effect. No cached old policy fallback. |
| Request | Existing brokerstore record; canonical/original intent, ordered write bundle; optional read bundle/candidates/context only when applicable | Pending 1 h, 3 per jail, dedup joins, denied/expired cooldown 10 min; End closes pending. Stores no HTTP body, provider fact data, credential or profile value. Unresolved candidates are named in audit. |
| Exact digest | Preserve existing `digest` prefix and old optional writes/HTTP encoding byte-for-byte | Append domain-separated optional read/candidate/nondefault-context sections only when present. A legacy/default-context write-only call must match fixed baseline digest vectors. If no read asks, do not blindly turn every existing standing read into new recorded read authority. An old answer never covers a read or routed context. |
| Exact grant | One use, normally 15 min, workspace holder; immutable filed launch history | Same source/workspace later launch may rebind a temporary parent copy, revalidate stored intent and current invocation under current authority, then spend once. Another workspace/source/query/resource/context cannot. Pending-ended-launch requests are not approvable. No refund after possible transmission. |
| Timed entry | Accepted old-store call:<cell> for new roles; Resource embeds role/nondefault context in the actual merge key | Bound cell/chain/context until expiry; one limit is the smallest role-derived cap (generic undeclared cap 15 min; AWS put 1 h, delete 15 min; native historical write caps unchanged, native read uses read_max_for/default 15 min; native withheld-fact write, candidate or known nonmember, min(15 min, shipped native write maximum)). All-or-none for all required cells. Does not count as working-set membership. Old entries stay readable and cover only former writes/default context. |
| Facts | Known admitted repository/scope, read cell, canonical fact intent, candidate set; one call-local counter | No durable/provider facts cache or preapproval lookup. Fact read must run before sending it; fifth refuses. Cached invocation facts are usable only with current scope/policy/context checks at effect boundaries; do not preserve them across a retry or relaunch. |
| Context identity | Source, declaration hash including set/unset metadata, context name, exact declared field names/values, matched scope | Only nondefault identity enters digest/timed key. Fresh route selection at answer and dispatch; any identity mismatch prevents spend/coverage. Host profile contents are neither read nor verified. Captured environment itself stays pin-time fixed. |
| Agent wake/publication records | Existing `.yolo/approvals` hints and native notification/web view | Non-authoritative. Wait exit codes preserved; publication failure reported. Never authorize from browser, hook output or agent-writable hints. |
| Classifier protocol | Step CLI/HTTP classifier version together; typed ordered read cells/descriptions | Zero declared read cells use the derived implicit read only if the existing read grant kind exists; one absorbs an uncelled read; many require a named declared read cell. Zero with no grant kind has no read authority. Cell/description additions are optional; old protocol remains valid except withheld lookup must refuse. Native Git/GH wire versions are not rewritten; generic classifier /1 normal compatibility, the observed-advertisement /2 upgrade for typed reads, and /2 withheld fact behavior are explicit below ([SBP-D37](scoped-broker-policy.md#SBP-D37)). Reject unknown/duplicate JSON and incompatible frontend/input versions. |

**Compatibility assertions, do not reopen:** broad read grants, native branch/tag/repository rights, working-set defaults and historical Add entries keep their meanings. With no new form, decisions/digests stay baseline except the specifically accepted hold/deadline, fresh-holder, placement, selector and cell-apply corrections. A legacy record without a source read grant retains the old all-call refusal; its latent writes become explicitly added gate lines before the new model marker is approved. A marked record with later read removal retains independent write rights, and inspection names `[allow.<pack>] off = true` as the whole-source stop.

## Build units

U1 is a preflight/evidence checkpoint; U2–U12 are coherent candidate commits ending at their named green gates, not permission for parallel writers to share source files. Dependencies below permit independent worktree scheduling only after the parent approves it. This plan itself launches nothing. All tests use fake host tools/loopback fixtures and event-driven synchronization.

### U1 — pin the current defects and admission invariants (first; 1–2 h)

**Settled:** test the actual caller before changing behavior. Start with the fastest permanent red controls, not a broad rewrite.

**Preconditions:** stamp candidate HEAD; code diff against the stamp is empty or separately approved; no task paths are dirty; confirm all map paths/symbols. Preserve the retained private baseline red receipts; their source/effect counters are summarized here and full sources follow.

**Tests first:** promote `TestClosureExactAnswerSurvivesFreshSameWorkspaceLaunch` and `TestClosureWorkingSetOnlyCeilingRunsCoveredWrite` as `TestSelectedCLIExactAnswerSurvivesFreshLaunch` and `TestSelectedCLIWorkingSetOnlyCeilingRunsThroughTransaction` in `internal/callbroker/scoped_policy_regression_test.go`. Their full sources are in Appendix C and their observed red conditions are exact: effects=0/uses=1 holder mismatch; pure runs/transaction refuses. Promote the V1 test in Appendix A. Add the inactive source test through real `serveCLI`/HTTP Handle and public `ScopeChanges`, not solely `decideCall`.

**Placement refinement:** the old `pinCLI` direct test is a gap witness, not sufficient acceptance. Add `TestSelectedCLIWorkspaceProgramRefusedAtStartup` driving `newCLIHandler`/real daemon startup with workspace outside HOME, direct and symlink executable, plus native exemption fixture. It must fail if the production call to PlacementRefusal is deleted.

**Gates:** run the supplied exact permanent test sources as a test-only overlay if needed, then run them first in their owning U2/U4/U5 change. Retain the red receipt, fix the caller, and commit each test with its green production slice; no skipped placeholder or deliberately red landing commit. U1 itself leaves evidence only and makes no repository commit.

**Fence:** no authority behavior edits during preflight. **Commit ownership:** the supplied regressions belong to their fixing unit's commit. **Report:** failing assertion, zero-effect counters, exact scenario IDs and environmental baseline.

### U2 — TOML vocabulary, validation, editor and provenance (after U1; 4–7 h)

**Settled:** design [§3.6 spelling](scoped-broker-policy.md#36-spelling), [§3.2 precedence](scoped-broker-policy.md#32-which-rule-governs), [§7.1 field bounds](scoped-broker-policy.md#71-model); new features TOML-only.

**Tests first:** `internal/config/scopedbrokerpolicy_test.go` loads actual TOML via `LoadReadableConfig` and `ReadWorkspaceForGate`; `internal/cli/scopedbrokeredit_test.go` runs registered `config` dispatch with post-write checks. Appendix A is the first red. Table fixtures must assert both filename writers and canonical values, not reconstructed jsonx maps alone. Appendix A also supplies a real registered-dispatch undeclared-never-token negative, with selected AWS coverage in a fixture isolated from the positive edits.

**Change:** first add declaration parsing/types/clone/hash for cell kind (omitted means write; read refuses tables/defaults), exact/segment metadata, context/route_anchors and native read_max_for. Pass parsed declarations into config; no classifier or authority activation is added in this unit. Then add explicit typed schema targets for `allow.*.never` list/elements, generic `scopes.*` source/resource/allow/requests/read/tables (never only for operation brokers, never audio or other grant services), `contexts.*` source/declared fields and user-only route context. Parse/validate against selected declarations; alias is a bare local key, matching is source+resource. Preserve native `[repos]` forms, allow scoped read there, but keep unbuilt Git lines refused. Implement the declaration matcher in resource.go now, with parsed matching metadata, selected-source-only string validation and shared inert reporting; U3 adds operation policy over that leaf. No classifier runs for config load/check/why. Native fetch/read normalize at every source/repository scope; distinct spellings in one scope/layer name both positioned lines and refuse on the verified V6 path. The shipped fetch token in both lists remains valid; preserve the author's no-never fallback for a native adapter already accepting both words. Native read.fetch is refused naming fetch; generic named read cells are not renamed by this native compatibility adapter.

**Checkable cases:** duplicate aliases resolving one resource, same token in never and another list; same always/on_request token stays valid, ask-file keys merge last-file-wins as shipped, read scalar plus read table, invalid declared-mode scope string, `[scopes]` git/github refusal naming `[repos]`, workspace/user scope ownership violations, inherited/outside-workspace request authorship, out-of-ceiling true warns/counts absent, inert unmatched scope, independent layers, per-scope working_set≥all including inherited effective values, narrow `never` versus absence.

**No legacy expansion:** add a public JSONC refusal fixture for every new user-config form; read old JSONC broad grants unchanged. Pass the existing manifest decoder legacy bit to new declaration-key handling: new matching/read_max_for keys stay TOML-only; retain legacy strict/tolerant unknown-key treatment and readable legacy exact/15 m defaults. Hash/clone/disclosure tests mutate each field individually. Do not alter concurrent pack-import spelling or dependency resolution.

**Gates:** `go test -short ./internal/config ./internal/configedit ./internal/cli`; `go test -run '^TestConfigRef' ./internal/cli`; fixture-only `yolo config check` via matching built CLI for authored TOML. **Commit:** `feat(config): read scoped broker policy in TOML`. **Report:** V1 green through real registered dispatch and canonical/public provenance tests.

### U3 — declarations, typed operations and one pure decision (after U2; 4–6 h)

**Settled:** read/write independent; each governing line final within its layer, stricter layer wins; any refused operation refuses the bundle, any ask makes one request. No read defaults.

**Tests first:** `internal/brokerpolicy/policy_test.go` and public `internal/callbroker/scopedclassification_test.go` cover the full rule state matrix below. Drive built-in AWS/CircleCI classifier Run boundaries and generic CLI/HTTP protocol decoders. Pin the zero/one/many matrix and read-grant/default behavior in H26–H32; malformed named read, forbidden read defaults/tables, reserved cell read and many-cell ambiguity refuse before effects. [Negotiation cases N1–N12, with N5a–N5c](#planned-classifier-negotiation-controls) (plan-level, under H26–H32 and C7n; no new scenario IDs) drive the real process boundary with counting fake classifiers.

**Change:** use U2's parsed cell Kind and add typed read descriptors to operation protocols/classifiers; write cell omission retains the existing write kind. Read cells have no working-set table/default. No shipped pack is required to declare read cells. Its zero-cell implicit read suffices for scoped AWS/CircleCI policy without a declaration gate change **only while the manifest is unchanged**. AWS segment/anchors/context metadata or explicit AWS/CircleCI read cells change the hash and require one active-source declaration prompt; H26 covers unchanged declarations only. Explicit per-operation AWS read cells (list_objects/get_objects/head_objects/four CE queries) and CircleCI listing are an additive TOML declaration change in this unit, never a prerequisite for the shared model. Native Git fetch/GitHub read remain native single subjects. Keep AWS original canonical argv and grammar; naming a read is not a new AWS operation. Add read-cell metadata without adding program/credential authority.

**Checkable tasks:** implement layer rule selection, the authored four-step never-only exception over the shipped ceiling resolver, exact `never` coverage, strict bundle aggregation and the single live table selector. H1–H14 pin exact/segment byte containment, chain-position/length order, broad anchor rules within admission, selected-source validation and rule-versus-route independence. Retain AWS cover under H9 parity; do not turn route matching into URI parsing. Resolve source containment exclusively through the authored contract. Adapt zero cells to an implicit read without mutating the declaration, one to its declared cell, and many only to named cells. Do not insert implicit metadata into hash/disclosure or grant anything to a zero-cell source lacking read grant kind. The AWS classifier answers a /1 request with its shipped uncelled reads labelled /2, and labels every read with one of the seven declared cells only on a /2 request; it adds no grammar row. The CircleCI classifier may advertise and name its optional listing cell the same way; an unadvertising classifier with zero read cells is unchanged. The decoder is version-dispatched and source-neutral: no source-name switch, registry, declaration capability field or cell metadata in classifier input. Keep `validateChain` repeated-type/parent/verbatim-approved-root checks, HTTP method/read_over_post floor and native resolver contracts before policy.

**Gates:** `go test -short ./internal/brokerpolicy ./internal/loopholedecl ./internal/awsclassify ./internal/circleciclassify ./internal/callbroker ./internal/config`; fuzz malformed/duplicate classifier fields using existing fuzz boundaries. **Commit:** `feat(brokers): resolve typed read and write cells consistently`.

#### Planned classifier negotiation controls

These are **protocol-testcase IDs**, grouped under existing H26–H32 and C7n, not new acceptance
scenario IDs. **Predicted outcomes; not executed.** Implement them through U3's real process
boundary with counting fake classifiers; permission activation, provider execution and approval
filing remain U7 work, not added to U3.

**Request-owned formats:** the answer body follows the request version, not its advertisement
label. CLI `/1` and `/2` requests retain `version`, `front`, `argv`, `stdin_sha256`, `roots` and
`facts`; no cell metadata or authority input is added. HTTP keeps its own request shape and uses
the same source-neutral negotiation/validation algorithm; the closure author did not read its
schema, and this publication does not claim those six CLI fields are HTTP fields.

| Request / answer label | Required body and outcome |
| :--- | :--- |
| `/1`→`/1` | Strict shipped body: bare read chains, single-cell writes. Old classifier; no successor request. |
| `/1`→`/2` | Strict shipped body, not typed: advertisement only. All `/1`-request answers in one classification keep this label. |
| `/2`→`/2` | Strict typed body: `reads: [{cell?, chain, description?}]`; every read passes the zero/one/many matrix. |
| `/2`→`/1` | Pack bug; refuse before lookup, decision or effect. |

Unknown and duplicate keys refuse in both decoders. A `/1` fact keeps `name`, `arguments`,
`value`; a `/2` fact carries exactly one of `value` or `status: "withheld"`. A `/2` write carries
chain/description and exactly one of cell or at least two unique declared write candidates;
candidates are legal only if the request held a withheld fact. A withheld continuation never
invents provider data. Zero declared read cells reject any named read, including in a withheld
`/2` answer; an existing read grant kind alone supplies the unnamed implicit read. One cell
absorbs an uncelled read or accepts only its own name. Many cells require names; a `/1`-only
classifier's reads refuse with the next step to update the pack's classifier or declare at most
one read cell. No supplied cell name is stripped, and core never supplies one.

**Validation order:** strict request-version shape and label; canonical argv/stdin hash, exactly
one answer kind and declared lookups; admitted chains and applicable HTTP floors; declared
write/candidate names; then read cardinality. Only the first advertising read/lookup answer under
explicit read cells is provisional and skips the final cardinality check. All earlier checks
still run; the entire provisional answer is discarded before any lookup, decision, request or
effect. The typed replacement passes every check. Withheld continuation defers nothing.

| Bound | Required count / budget |
| :--- | :--- |
| Local classifier processes per classification | Baseline at most 4; at most 5 only with the one discarded typed-read upgrade. A withheld continuation adds no process beyond the existing round loop. |
| Lookup rounds | At most 3; provisional lookups are never performed and do not consume a round. |
| Actual fact sends per call | At most 4; the fifth refuses. The extra local negotiation process is not a fact send. |
| Per-process and whole classification budget | 2 s and 30 s unchanged, including the upgrade. No new timeout or cap cache. |

Each replay and nested lookup classification starts `/1` and negotiates afresh. New core remains
compatible with old classifiers; this does not promise that an advertising new classifier works
with baseline core. Baseline core already ran the local classifier before refusing its version;
no broker/provider operation or approval request follows. Shipped commands are source-confirmed
as `yolo internal call-broker-classify aws` and `yolo internal call-broker-classify circleci` in
the [AWS manifest](../../packs/aws/loopholes/aws-broker/loophole.toml) and
[CircleCI manifest](../../packs/circleci/loopholes/circleci-broker/loophole.toml), not proof of an
always-matching generation. Required label/shape checks apply even to shipped executables.
No runner emission, AWS grammar-row correspondence or HTTP schema was verified by this publication.

| Case | Setup | Predicted result |
| :--- | :--- | :--- |
| N1 | Unchanged AWS declaration (zero read cells), shipped-shape classifier labelled `/1` | One process; reads are the implicit read; identical to baseline. |
| N2 | Zero read cells, advertising classifier, read-only call | One process; no `/2` request is ever written to the classifier. |
| N3 | Zero read cells; a `/2`-shape body in answer to a `/1` request | Refused by the strict `/1` decoder. |
| N4 | One read cell, `/1`-only classifier | One process; uncelled read absorbed. |
| N5 | Seven read cells, advertising classifier, S3 list | Two processes. The first answer's uncelled read is not refused: cardinality is deferred for it and it is discarded. The second request is `/2` with identical argv, hash, roots and empty facts. The second answer's read is named and passes cardinality. |
| N5a | N5, but the first answer has an unadmitted chain, an undeclared write cell or a wrong argv echo | Refused on the first answer; no `/2` request is sent. |
| N5b | N5, but the `/2` answer's read is uncelled or names an undeclared cell | Refused; no lookup, no decision. The deferral did not carry over. |
| N5c | N5's first answer, with the upgrade path disabled in the test | No decision, request or effect is ever produced from the provisional answer. |
| N6 | Seven read cells, `/1`-only classifier, read call | One process; refused with the classifier-update next step; no `/2` sent. |
| N7 | Seven read cells, advertising classifier, first answer refused or write-only | One process; final. |
| N8 | Advertising classifier, first answer asks a lookup, explicit read cells | The provisional lookup request is discarded. Lookup reader call count is 0 until a `/2` answer has passed all five checks. |
| N9 | `/2` answer naming an undeclared cell, an uncelled read with many cells, or labelled `/1` | Refused; no lookup, no decision. |
| N10 | Label flips between two `/1`-request answers | Refused. |
| N11 | Withheld lookup, `/1`-only classifier | Refused naming the read line (C7n); no `/2` sent. |
| N12 | Typed upgrade followed by three lookup rounds | Five processes, three lookup rounds, within the 30 s budget; a sixth process is refused. |

### U4 — store binding, selector parity, holder and placement (after U3; 5–7 h)

**Settled:** one brokerstore; optional digest extensions; one selected table throughout; workspace-held exact answers; no grant/route/root authority fabrication.

**Tests first:** U1 holder/working-set tests become green; add `internal/brokerstore/operation_test.go`, `internal/callbroker/scoped_approval_test.go` and CLI/HTTP holder tests. Freeze baseline native and generic digest literals before production edits. New tests mutate one argv/query/body/resource/cell/candidate/context at a time and expect no spend/effect. Mixed read+write produces one durable request with both ordered subjects; a refused operation produces none.

**Change:** extend existing records with optional read/candidate role fields, common timed expansion and atomic role-derived cap validation. One TimedLimit function feeds both front maximum/prompt and --for validation for new read/withheld roles; known-write-only native answers retain the old validation/message branch even if a hook is installed; it rereads selected current declarations and native policies at answer time, checks request state first, and names the limiting cell/role. Missing native read_max_for reuses brokerstore.TimedGrantDefault, never another literal. No cap/profile/provider data is persisted in a request or cache. Extend callback state rather than recursively acquiring locks to obtain membership. Revalidate invocation and a temporary rebound stored intent with unchanged history. Refuse approve of ended pending launch. Apply `brokercore.PlacementRefusal` after path/symlink resolution with approved workspace at actual startup, preserving exemptions.

**Checkable tasks:** retain exact digest omission for legacy/default calls; read+context additions bounded and length/domain-separated; legacy read absent means no new read coverage. H15–H21 pin the single current role-derived timed limit, expiry precedence, zero-write refusal and identical expiry across all entries. Old write entries retain broad native coverage/original expiry even after cap changes; they gain no read/candidate role. `--for` creates all touched eligible read/write/candidate entries or none. A timed answer never overrides `never` or workspace false; call entries never qualify as working membership. CLI replay uses original argv/stdin hash and caller's bounded context, HTTP reclassifies resubmitted body, never archives it. Preserve no-refund after possible transmission and required audit-before-effect.

**Gates:** `go test -short ./internal/brokerstore ./internal/brokerscope ./internal/callbroker ./internal/cli`; focused race tests described below. **Commit:** `feat(approvals): bind scoped operation bundles and workspace holders`.

### U5 — on-switch compatibility and truthful declaration gate (after U2/U3; 3–4 h)

**Settled:** design [§3.7](scoped-broker-policy.md#37-read-and-write-are-independent) and SBP-D26, including no prompt/record/code for inactive rootless sources; active changed declarations require `y` and refuse until approved. Required trust disclosures unchanged.

**Tests first:** `internal/config/scopedbroker_gate_test.go` drives `ScopeChanges`/`RecordApproval` from positioned workspace TOML, then real launch/apply gate consumers in `internal/cli/run/scopedbrokergate_test.go`. Assert prompt blocks and resulting record/authority, not a comment or private `compareScope` alone.

**Change:** record an optional policy-model marker without rewriting old records opportunistically. Old absent-read records still refuse latent writes; the next actual gate approval lists them as added before marker publication. Existing broad-read legacy records produce no extra prompt. Separate declaration-only diff from scope block: inactive no roots skips/records nothing; first root shows both; active declaration-only shows removed/added disclosures, no origin/count; combined change shows both. Cancel preserves pending refusal and next prompt.

**Checkable tasks:** declared-root filename origin versus remote `.git/config`, source≠pack, empty rendered blocks never prompt, no root skips pack code, native trust banner bytes remain. Include mode/separator/anchors/route_anchors/read_max_for hash mutations in the same selected-source declaration gate; canonical new native Brokered declaration hash is carried alongside the existing CallBrokerDeclarationHash, not a second registry. Gate cancellation must not let either declaration start executing.

**Gates:** `go test -short ./internal/config ./internal/cli/run`; existing gate/provenance tests plus new G1–G3/G21–G25 cases. **Commit:** `feat(brokers): gate policy-model and active declaration changes`.

### U6 — exclusive live asks, consumer proof and atomic apply (after U4/U5; 6–8 h)

**Settled:** every workspace ask, **source-scope read included**, and roots are one source authority replacement. A consuming daemon never reads the separate grant file for asks, including a legacy-model source-read bit; user ceilings/routes remain one fresh host read. A raw workspace edit is not live authority.

**Tests first:** `internal/brokerscope/policy_test.go`, `internal/cli/run/scopedbrokerapply_test.go`, `internal/callbroker/livepolicy_test.go`. Public apply plus actual next handler call proves G5/G6/G6a–G6e, not only a status line. Stale grant read=true beside snapshot read=false must refuse with grant-reader count zero. Source read true→false plus scoped read false→true must never expose an old/new mixture. Permanent failure points cover each old scope/snapshot/grant/proof read, sidecar preparation/write/readback, ancillary grant write, authoritative scope write/rename, refusal-marker write and postcommit publication.

**Consumer proof (generic and native daemon startup, not just the generic launcher):** `internal/brokerscope/consumer.go` (new) stores a private regular 0600 `snapshot-consumer` record under that source's protected host launch state. **Only real daemon Main**, after binding its live lease and successfully validating its initial snapshot reader, writes it under the source lock. It binds source/workspace/launch/declaration hash, live PID/process-start/lease, input identity and supported snapshot-reader version. Apply validates it and liveness before any effect; absence/mismatch means nonconsuming. The launcher never asserts consumption. Shutdown/replacement makes it invalid; foreign/copy/agent-written/symlink/old-daemon unknown fields cannot count as proof. Proof publication failure stops/refuses startup with next step. Real-Main test and deletion control must fail if daemon proof writing is removed.

**Older launch rule, before any write:** compare **all** old/new asks (source read, cells, scoped asks, model) using a trusted running delivered/gate-bound ask view reconciled with the old private launch input's frozen cells and source grant state, plus proposed gate input. The latest approved record alone may already be ahead of what the old daemon runs and is insufficient. An unreadable or unreconciled old ask is unknown, never proof of roots-only. No proof + any ask change => change nothing for this source, roots/grants included, nonzero, old-policy disclosure and concrete jail stop/relaunch command. An inability to read enough to prove no ask changed is not roots-only. No proof + no ask change => run the shipped root/grant apply and older-broker skew handling unchanged.

**Real older fixture:** add G6c/G6d beside [`internal/cli/run/liveapplyolderbrokers_test.go`](../../internal/cli/run/liveapplyolderbrokers_test.go). Build the actual pre-revision hidden broker from read-only fixed `7883c182` source in an owned fixture, with fake host program/classifier. Do not emulate old behavior by setting a new flag on a new daemon. A read-only git archive into fixture scratch changes no checkout/ref; unavailable fixed source is an explicit fixture blocker, not a skip-green. Freeze its roots-only baseline result/warnings/effects first, then compare feature apply. Changed asks leave the actual old frozen asks, root bytes and grant bytes unchanged. An unknown locator key in its lenient v1 file is ignored and must not be treated as rejection or consumption.

**Commit/write order under the single source lock:**

1. Read and validate existing scope, its complete snapshot (or legitimate legacy approved state), current ancillary grant file, consumer proof, declaration/lease and one immutable current host bundle. Compare asks and finish older-launch precheck. Any read error refuses before effects; attempt the refusal marker for a confirmed consuming launch.
2. Prepare and validate the complete immutable **roots + every ask** sidecar in protected state; read it back and verify binding/digest/sequence. It is not authority yet.
3. Perform every ancillary write required by this apply (grant-file projection for other readers included). The consuming operation daemon ignores those files for asks. Any failure uses the source refusal path; never report next restart while installing the rest.
4. **Last authority write:** atomic scope replacement commits the sidecar locator/digest/sequence and mirrored roots together. There is no required grant/policy/marker-clear write after this commit. Include supersession of an existing refusal in this committed state/sequence rather than requiring a fallible later marker deletion. Readers reject mismatch and refusal newer than the committed sequence. No cached fallback.
5. Release the lock only after recording the truthful commit disposition. Publication/hints follow and are non-authoritative; their failure is nonzero/visible but cannot be called an old-policy install failure.

**Failure truth:** before the scope rename, install failure + successful refusal marker => source refuses; marker failure => old committed snapshot remains the only workspace authority, disclose it as still live and name the jail to stop. A scope writer error after rename (if an existing helper can report one), failed readback, or uncertain outcome must be classified under the lock as old/new/unknown using available validated bytes. Never say old live if the new locator is installed. New-committed + marker failure explicitly says **new policy installed, apply failed, refusal not confirmed**; uncertain says **installation/refusal unconfirmed; stop jail**. Every such path returns nonzero. Inject both precommit and committed/uncertain error positions. A corrupt/unreadable old snapshot does not justify claiming old permission is live. No speculative fsync promise is added.

**Other bindings:** `/3` removes frozen authority Roots/EffectiveCells; immutable original bytes still bind the program/declaration/input. Standalone approve resolves current scope and current snapshot under the source lock. G6e files under a newly live root and answers through public approve, proving no `in.Roots == sf.Repos` comparison remains. Attach uses the running tree; version guards update together. Apply reports cell/scoped/read changes live only after actual commit.

**Gates:** `go test -short ./internal/brokerscope ./internal/callbrokerinput ./internal/callbroker ./internal/cli/run ./internal/changetiming`; focused G6 failure/older/consumer-proof races and U12 fake-provider integration. **Commit:** `feat(brokers): publish all live asks with daemon-proven consumption`. **Report:** proof producer, every failure disposition, real old-broker roots-only baseline and new-root approval witness.

### U7 — generic CLI/HTTP read, mixed and fact transactions (after U4/U6; 3–5 h)

**Settled:** admission first; prediction has no effect and never reads a non-run fact; exact/timed does not override refused candidates or facts.

**Tests first:** `internal/callbroker/scoped_call_test.go`, `scoped_http_test.go`, `fact_policy_test.go` drive `serveCLI`/Handle and actual HTTP front. Synthetic mixed classifier returns ordered read+write chains; synthetic lookups count sends. Cross-check evaluator output with act and stored request, not only pure function tables.

**Change:** remove unconditional read run and blanket read-as-on-switch after the marked-model gate. Route read-only and mixed calls through common transaction; current lease/selection/declaration/root/policy validation occurs before classifier when no authority root exists. Use the implicit or declared fact read subject and known bound scope with finite candidates; no classifier-requested lookup can start under ask/refuse outside the combined contract.

**Boundaries:** generic HTTP typed-native lookup ancestry remains required, credentials stay sealed, read-over-POST exception remains declaration-owned, query/body/URL/header validation unchanged. The final approved root ID remains verbatim, not replaced with a normalized sibling. Any adapter unable to establish bound fact scope/candidates must refuse rather than execute an exploratory provider lookup. The real generic daemon witness uses two write cells with differing true/ask/false values, correctly paired working_set≥all, a declared lookup under an admitted root and a counted fake provider. A withheld read sends no provider lookup; new Answer protocol reports candidate writes and safe core text. Old Answer protocol with a withheld lookup refuses naming the governing read line, never fabricates an empty provider result. A source-scope prediction is not this producer witness.

**Gates:** `go test -short ./internal/callbroker ./internal/callbrokerinput ./internal/brokerstore`; existing HTTP withdrawal/preflight/no-refund/selected-front tests. **Commit:** `feat(callbroker): mediate read and mixed operation bundles`.

### U8 — lifecycle parity, waits, deadlines and real Main (after U7; 3–5 h)

**Settled:** design [§6 lifecycle](scoped-broker-policy.md#6-one-lifecycle-for-every-broker-proposed) / [§6.1 budgets](scoped-broker-policy.md#61-budgets); generic hook, publication failure and Store.End must be wired in production Main, not only a tested helper.

**Tests first:** `internal/callbroker/lifecycle_test.go` launches the real daemon Main in an owned helper subprocess, uses an actual front/session and fake classifier/host CLI/hook. `hold_test.go` drives response via request-filed events, store answer events, disconnect and shutdown channels; injected timer/clock advances 29 and 30 seconds. No sleep or elapsed-time pass condition.

**Change:** wire `brokercore.StartOnRequest`/real Runner, PublishFailed and launch cleanup Store.End, and join hook teardown. Hold outside source lock; reacquire and revalidate after answer. Generic front 5 s dial + 100 s call connection. Current HTTP front has its own 30 s request timeout/forwarding deadline (`HTTPFront.ServeHTTP`, forward connection and seal-created front default), unlike CLI's 40 s connection; change the request/forward budgets to the fixed 100 s contract as well, without widening seal/acquisition/credential publication gates. Asking generic gets decide 30 s, hold 30 s, execute fresh 30 s including replay/program. Nonasking generic still one shared 30 s decide+execute. Native GitHub deadline update belongs in U11; Git transport retains no overall transfer deadline.

**Checkable tasks:** request filed once/join no notification; hook output `approved` changes no authority; failure recorded; End closes/publishes/wakes pending, retains workspace grant; answered at fake second 29 still has full execute budget; no answer gives 75/ID/pending; disconnect leaves pending/no effect; joined exact races give one dispatch/one 72; second call progresses during hold. Wait codes 0/72/75/77/78/79 preserved. Publication failure visible but not a decision.

**Gates:** `go test -short ./internal/callbroker ./internal/brokercore ./internal/brokerhook ./internal/approvalwait ./internal/cli`; production Main deletion controls required. **Commit:** `feat(callbroker): match native approval lifecycle and hold budgets`.

### U9 — declaration-owned contexts and AWS routes (after U3/U4/U6; 3–5 h)

**Settled:** design [§7](scoped-broker-policy.md#7-execution-contexts-and-routing-proposed); routing is not permission, one context per bundle, no default fallback, profile selector is not account verification.

**Tests first:** `internal/callbroker/context_test.go` and public TOML route loading/call tests; fake AWS executable records only synthetic declared variables and supplied argv. Synthetic non-AWS source sets another variable to prove core genericity. No credential file, `sts`, provider SDK or real AWS CLI is involved.

**Change:** declaration metadata sets exact named field env vars, unsets exact list, restricts segment routes by route_anchors (exact mode takes no route_anchors and all its valid scopes route); hash/clone/disclose this metadata. An HTTP-front context block refuses declaration before startup; context metadata is CLI-only. Context validates declared fields, nonempty valid UTF-8 ≤1024 bytes, no U+0000–001F/007F–009F, no trim. Select the most specific applying route by first contained chain position, then byte length, with source scope least specific; host-only matching/source validation. Bound context name visible, field values/profile never classifier-visible or request/audit-visible.

**AWS declaration:** profile→AWS_PROFILE, unset AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY/AWS_SESSION_TOKEN only on routed calls; route_anchors=["s3://"], including the s3:// anchor-wide scope; CE routes load-refused. A shared broad route does not widen approved roots. Copy baseline pinned PATH/HOME and other captured host names; default context is byte-for-byte unchanged, including static credential precedence. Add context values to redaction; do not mutate `pinnedCLI.env` globally. No per-call profile/credential/IAM discovery.

**Checkable tasks:** route edit pending/allowed/timed invalidates without spending; conflicting operation contexts refuse; missing/broken matched context fails without default retry; jail --profile/env override refused/ignored unchanged; 1024-byte/space/non-ASCII reaches fake env verbatim; invalid values fail field-named load. `aws-auth` and `reportAWSCredentialBypass` stay untouched and retained tests pass.

**Gates:** `go test -short ./internal/config ./internal/loopholedecl ./internal/callbroker ./internal/awsclassify ./internal/cli/check`; F1–F15 and unchanged aws-auth source diff. **Commit:** `feat(brokers): bind declaration-owned execution contexts`.

### U10 — native Git credentialed fetch and independent push (after U4/U5/U6/U8; 4–6 h)

**Settled:** [OQ-SBP1](scoped-broker-policy.md#OQ-SBP1)=B; a read refusal withholds host credentials only for supported public HTTPS fetch/import. No anonymous arbitrary write, gh/AWS bypass or host credential fallback.

**Tests first:** `internal/gitbroker/scopedfetch_test.go` using existing local TLS Git fixtures/fake credential helper and SSH recorder; drive helper/front through actual transport. `internal/skillimports/scopedaccess_test.go` drives ResolveLaunch/Inspect/Project using local immutable objects/fake fetcher. Test SSH zero spawn before answer; HTTPS 401 zero helper fill before answer, and revalidation after fill/before send/host git; no-request imports under ask.

**Change:** fetch cell at every existing `credentialPolicy` boundary, before resolving/spawning SSH and in DecideImportAccess/FetchImport. Hold first credential-needed ask 30 s after acknowledgement; source lock not held. Each recheck can withdraw credentials. Marked gate push grants are evaluated independently of fetch; legacy record old on-switch maintained. Preserve whole-push/ref/options/canonical intent and native exact matching.

**Integration boundary:** current `AuthorityFromWorkspace` intersects approved/current exact remote URLs and broad read grants; extend it to carry approved/current scoped fetch policy rather than inventing import authority. `resolveFreshSkillImports` runs only after its separate successful registration/config gate; preserve that callsite. `cacheAccessAllows` blocks using formerly authenticated cached bytes under now-anonymous authority. Preserve it: a fresh public anonymous fetch is allowed, previously credentialed bytes are not silently relabeled public. This is not a contradiction with [OQ-SBP1](scoped-broker-policy.md#OQ-SBP1). If implementation discovers registration/delivery forbids even newly anonymously obtained public content solely because fetch is denied, flag that exact callsite to the author; do not remove registration authority.

**Checkable tasks:** normalized repository identity alone cannot admit credential fill/send; use exact approved URL/equivalent endpoint matcher plus selected read rule. HTTPS public no-line/never/false succeeds anonymously including import; private 401 fails normally with zero helper/send; SSH never/false zero spawn; import ask HTTPS anonymous/SSH refusal, no pending. Timed fetch answer matches workspace/resource/context and never overrides refusal. G15a drives actual `DecideImportAccess`→`FetchImport`→skill resolver and fake HTTPS credential-helper path with a live timed fetch entry: it is credentialed, then expired/withdrawn authority becomes anonymous or refuses SSH with no request. Do not count a direct coverage helper as the import witness. Raw import declaration never adds a root/grant.

**Gates:** `go test -short ./internal/gitbroker ./internal/gitremote ./internal/skillimports ./internal/cli/run`; reached Git integration names in U12. **Commit:** `feat(gitbroker): scope credentialed fetch without blocking public Git`.

### U11 — native GitHub read adapter and bound fact matrix (after U4/U5/U6/U8; 4–6 h)

**Settled:** exactly one repository read cell; no new GitHub cell tables/verbs; bounded facts and candidate matrix from [§4.1](scoped-broker-policy.md#41-fact-reads); fresh 30 s execution after answered hold.

**Tests first:** `internal/ghbroker/scopedread_test.go`, `factpolicy_test.go`, `operationholder_test.go`, `holdbudget_test.go`. Drive `ServeContext` including public Mode=ask/evaluate and fronted requests. Fake `hostRead`/runner records every API path; consumer requests must name known repository before provider read. Preserve `TestExactGrantOutlivesItsBrokerForItsWorkspaceOnly` and source/holder/fact guards.

**Change:** route all supported read dispatch and every fact callsite through common policy. The current paths are `evaluateWrite`, `fileAsk`, `coveredWrite`, and their `readIssue`, `readRun`, `readRunHead` (job then run), `defaultBranch`, `runsCovered` and `onDefaultBranch` helpers. One assessment/counter flows through the invocation; no direct fact bypass remains. A working-set item needs its provider state read; when read is ask/refuse, treat it as nonmember, ask and send no provider read before or after a once answer. Such withheld-write answers use the new ≤15 m role, even when one write is known. Baseline nonmember writes with read run keep their known-write cap/message. Defaults/actor/job grants needing unreadable facts cover nothing. Keep actor/repository/default-branch validation and dispatch pinning only when facts legitimately run.

**Checkable tasks:** GitHub producer coverage is limited to built rights: C7c/C7i/C7j/C7l/C7m and H18a/H18b. C6/C7/C7a/C7b/C7d–C7h/H18 differing-candidate cases are real **generic daemon** witnesses; for GitHub they are injected adapter-boundary checks only, explicitly not reachable or producer-verified. No GitHub cell keys are a prerequisite. Pin never tokens exactly read/create_pr/create_issue/runs/approve_runs from the shipped validator; all/working_set/cell paths refuse as in other lists. Include old exact and timed entries; candidate-only request text with no fetched data; spend at first fact/no refund when refused real cell appears; audit unresolved candidates when no fact sent. Approval-time/timed expansion honors each cap and writes all-or-none. Native nonasking behavior and legacy digest remain; remove nested/remainder deadline for asking execution, not the decide budget. Ordinary `yolo access` prediction files/spends/dispatches nothing and only run-authorized facts may be read.

**Remaining native limits:** this implements the one GitHub read cell and existing write rights/defaults only. GitHub cell tables previously marked unbuilt stay unbuilt. Git named-repository policy is read-only; other named Git lines still refuse. CircleCI rerun remains declared/inert and classifier-refused, with no new program/auth surface. AWS operation grammar/auth pack remain unchanged; generic matcher metadata is not provider execution authority.

**Gates:** `go test -short ./internal/ghbroker ./internal/brokerstore ./internal/cli`; counted real public ask regression must either reuse invocation facts to ≤4 sends or refuse at fact five, never raise cap. **Commit:** `feat(ghbroker): mediate reads and bounded decision facts`.

### U12 — common conformance, integrations, inspection and final gates (after all units; 5–9 h plus gates)

**Settled:** all four sources implement the common contract; source-specific unsupported grammar stays refused. Observable surfaces ship with behavior, not later.

**Tests first:** extend `internal/callbrokerconformance` fixtures/CheckSteps and add native adapter fixtures so the same state assertions run against git/github/aws/circleci. Each matrix row below becomes a named permanent test/subtest linked to a real consumer. Run source-specific unsupported/inert cases, not merely generic success paths. Update previous unconditional-read/on-switch assertions to legacy-vs-marked expectations; do not weaken grammar, root, credential, secrecy or no-refund tests.

**Ships with:** selected-source coverage must be asserted in public config/editor fixtures; empty/skipped declaration coverage is never semantic policy-green. `config why` distinguishes actual source/rule/ceiling/ask/read/write/context name and withdrawal; `check` reports inert scopes/declared unimplemented cells/writes remaining after read removal; approve summaries include read/candidate subjects/context name without profile or provider data. Errors name exact existing editing verb or apply/relaunch/stop next step. Request/history/wait hint views stay non-authoritative. Update `config_ref.txt` and schema coverage examples for TOML only.

**Documentation claims to revise:** [AWS authority](../reference/aws-operation-broker/authority.md) presently requires broad read as on-switch; [execution](../reference/aws-operation-broker/execution.md) presently describes one host environment; [Git broker](../reference/git-broker.md), [pack brokers](pack-brokers.md), [GitHub write scopes](github-write-scopes.md), [live config apply](live-config-apply.md), [approval experience](approval-experience/README.md), and pack author service recipe/read-never-asks guidance overlap. Correct old `only`/`always` claims as well as links; keep unbuilt GitHub tables explicitly unbuilt. Preserve AWS bypass report/no routine warning and mandatory trust banners. Do not graduate the author-owned design without its required built/evidence pass.

**Gates:** run package/full gate, nested wiring and reached integration protocol below; full integration suite once on combined final launch-path tree, not per worker. Run FULL census once on final feature candidate—not the quick generator—and commit its generated output with feature instrumentation. **Commit:** `test(brokers): verify scoped policy across native and generic fronts`. **Report:** all 144 IDs accounted for with producer-versus-adapter labels, actual test inventory/no empty selection, failing controls, full final-tree gate receipts, platform evidence limitations and owned cleanup.

## Exhaustive state and fact matrices

### Rule matrix (per source, not AWS-only)

Run all 16 pairs: ask `false`, `ask`, `true`, absent × ceiling absent, `never`, on_request, always. Expected rows are respectively `RRRR`, `RRAA`, `RRUU`, `RRAU` (`R=refuse`, `A=ask`, `U=run`; true is gate-approved). Out-of-ceiling true additionally emits the load warning/counts absent. Repeat with source/root/narrower scope selection, the four never exception steps versus baseline covering-token resolution, source/alias duplicates, inheritance, both write tables, timed present/expired/wrong chain/wrong context, matching/nonmatching working membership and old/new model marker. Test the official native working-set comment/edit defaults separately: absent ceiling does not remove those shipped defaults. GitHub has no declared writable `working_set` or cell token; its shipped defaults remain unchanged. Any hypothetical explicit-never suppression of that default is an injected adapter-boundary case only, never public GitHub config or producer evidence. A generic pack's authored default=true under an absent ceiling still warns/refuses; it cannot acquire that native exemption by source name, table value or manifest claim. No read default exists. Explicit `[allow.<pack>] off = true` refuses the whole source even if a narrower scope or route otherwise matches. A source selected with roots but no permission still cannot file an approvable operation.

Resource tests use exact literals: `s3://a/team/` does not cover `s3://a/team-other/`, an object-parent listing does not inherit an object-only decision, Cost Explorer operation does not admit S3, CE query identity changes with canonical query even on same resource ID, CircleCI `gh/o/r` does not cover `gh/o/other`, Git normalized identity cannot replace exact/equivalent-endpoint matching. **Important source fact:** existing AWS bucket admission root is `s3://a` (no trailing slash); both `s3://a` and `s3://a/` are valid, distinct scope strings, with the second inside the first. Do not normalize the approved root or silently accept `s3://a/` as an old root: `awsclassify.parseRoots` currently rejects that bucket-root form. The accepted exact/segment matcher bridges scopes without changing a single admitted root byte. No canonical scope URI parser is added.

### Bound candidate/fact matrix (U7/U11)

| Candidate decisions | Fact run | Fact ask | Fact refuse |
| :--- | :--- | :--- | :--- |
| All refuse | Refuse, no read/request | Refuse, no read/request | Refuse, no read/request |
| All run | Read under standing authority, then run | Run without fact | Run without fact |
| All ask or run+ask, no refuse | Read then actual-cell policy | One ask, no fact before/after answer | One ask, no fact ever |
| Some refuse, some non-refuse | Read, then actual-cell policy | One bound fact+write ask; no provider fact before answer | Refuse, no request; name both read and refusing cell lines |

Every candidate decision includes live timed coverage but never overrides hard refusal. Third-row once binds canonical argv whichever approvable candidate; timed expands every candidate all-or-none, minimum role-derived cap. Fourth-row once spends at first authorized fact send, then sends write only if resolved cell is not refused; no refund. Fourth-row timed includes read plus only non-refused candidates, all-or-none; actual refused cell still refuses. A refused fact stays unread even with an old exact/timed grant. A write depending on default branch/actor/job facts loses that grant's coverage when fact is not run; it asks as uncovered, without provider data. Fifth fact send is impossible: it refuses first.

### Event-driven concurrency and failure controls

- Barriers at request filed, lock acquired, fresh authority read, audit prepared, grant durable-spend, first provider transmission and exit observed. Timeouts are generous failure guards only; timer transitions use injected clock/channel factories.
- E1/D11: two callers join; answer once; count one durable spend and one executable/provider send, loser 72. E2: synchronize two approvers, assert first answer persists and second reports answered. No sleeps to order races.
- E3/G9/F6: suspend before dispatch, withdraw root/ceiling/membership/route through the supported gate/user-config reader, resume; no spend/effect. E4: suspend after possible send, withdraw; no refund/retry. E5: fail one timed cap/entry persistence; neither cell authoritative.
- D9: another same-source call reaches its effect while first is held; completion event proves lock release. D10: close actual client transport; pending remains and no host execution. D3: signal actual Main; observe Store.End publication/wait 79 before owned subprocess exit.
- G6: fail policy sidecar install, marker install and publication independently; assert returned status and actual next-call authority. A publish-record error does not revoke/allow; required authorization audit error blocks before effects. Hook failure/output never consent.
- Native resolver/source-provenance controls: fake classifier with valid-looking chains cannot bypass native HTTP ancestry verifier; spoofed selected native pack/source cannot install git/GH approval authority; withdrawn/faulted selector/pack load refuses; alias value with wrong filename writer cannot gain approval.
- **Callsite deletion controls:** in a disposable worker-owned candidate only, temporarily remove (one at a time) the Main StartOnRequest, PublishFailed, Store.End, pin placement, early root guard, snapshot read at spend, daemon consumer-proof writer/validator, exclusive source-read snapshot selection, shared selector at revalidation, each Git credentialPolicy check, each GH fact-policy dispatch, context env application and explicit native-origin check. Run their permanent consumer tests and retain specific reds, restore exact bytes immediately. Never mutate primary/reviewer trees or use deletion controls as the submitted implementation. If removing a production call does not fail a test, add the missing caller assertion before green.

## Final validation protocol and estimates

The package test gate is cheap and belongs to each unit. The full applicable gate belongs once to the final candidate feature tree. Budget estimates are planning ranges, not measured completion claims: U1–U12 sum roughly 45–70 engineering hours; package iteration is seconds/minutes with shared warm caches; final census/CI/integration budget is an additional 2–6 h depending on available runtime/image. The estimate includes the closed H-series contract; it does not hide an unresolved architecture gap.

```sh
# Candidate only; before final feature commit. Standard environment/caches.
just format
go test -short ./internal/brokerpolicy ./internal/config ./internal/configedit ./internal/loopholedecl ./internal/brokerstore ./internal/brokerscope ./internal/callbrokerinput ./internal/callbroker ./internal/gitbroker ./internal/gitremote ./internal/ghbroker ./internal/skillimports ./internal/awsclassify ./internal/circleciclassify ./internal/cli ./internal/cli/run ./internal/brokercore ./internal/brokerhook ./internal/approvalwait
# Focused races are fixture/account-free; name actual tests as they land.
go test -race -count=1 -timeout=15m -run 'Scoped|FactPolicy|FreshLaunch|WorkingSetOnly|LivePolicy|Hold|Context' ./internal/brokerstore ./internal/brokerscope ./internal/callbroker ./internal/ghbroker ./internal/gitbroker
# Whole-tree landing gate once, after final feature and census output are committed.
just check-ci
```

**FULL census, no shared environment overrides:** source [`scripts/completion-census.py`](../../scripts/completion-census.py) compiles all package tests and executes each short test with Go test instrumentation; it is not the quick completion generator. Run only on final feature candidate, retaining the receipt and actual emitted package/test counts (the requested ~18k corpus is a budget description, not a count measured here). If collector binaries/logs must stay in ignored candidate durable scratch, use only Python's collector temporary-directory setting; leave child HOME/TMPDIR/GOENV/cache env unchanged. The wrapper below restores the default before removing the shared test cache, and keeps the collector's own per-test leak scanner:

```sh
# From final candidate root. Manual scratch must already be ignored and owner-created.
python3 - <<'PY'
import os, pathlib, runpy, tempfile
scratch = pathlib.Path(os.environ['EVIDENCE_DIR']).resolve() / 'final-census'
scratch.mkdir(parents=True, exist_ok=True)
module = runpy.run_path('scripts/completion-census.py', run_name='completion_census_collector')
gate = module['load_leak_gate']()
previous = tempfile.tempdir
default_tmp = tempfile.gettempdir()
try:
    tempfile.tempdir = str(scratch)
    module['collect'](gate)
finally:
    tempfile.tempdir = previous
    module['remove_shared_test_cache'](gate, default_tmp)
PY
# Inspect generated scripts/completion-readers.json, retain failed receipts, commit final output.
```

**Reached integration tests:** first inventory exact names (`go test -short -list ... ./integration` plus source search); then run fake-provider tests reading touched contract. Existing names: `TestAWSBrokerMediatesReadsAndAsksForWrites`, `TestAWSBrokerTimedAnswerIsCappedAndCoversItsResource`, `TestAWSBrokerCostExplorerRequiresItsOwnRoot`, `TestCircleCIFakeHostCLIUsesRealClassifier`, `TestGitBrokerFetchAndRefusal`, `TestGitBrokerWithNoGrantRefusesTheFetch`, `TestGitBrokerCreateBranch`, `TestGitBrokerConfigBranch`, `TestGitBrokerAllowOncePush`, `TestGitBrokerAllowPushForATime`, `TestGitHubBrokerRunsAReadAgainstTheHostGH`, `TestFakeGHDispatchedChecksExactCommandAndArguments`, `TestGitHubBrokerScopeChangeIsApprovedAtTheNextFreshLaunch`, `TestGitHubBrokerAnAttachKeepsTheRunningScope`. Add `TestScopedBrokerPolicyLiveApplyAndMixedAnswer` to the serial requireJail suite; no `t.Parallel`. Preserve `YOLO_TEST_JAIL_TIMEOUT` guards.

```sh
go test -count=1 -timeout=0 -run '^(TestAWSBroker.*|TestCircleCIFakeHostCLIUsesRealClassifier|TestGitBroker.*|TestGitHubBroker.*|TestFakeGHDispatchedChecksExactCommandAndArguments|TestScopedBrokerPolicy.*)$' ./integration
# Launch path changes require the full suite once on the combined final tree:
go test -count=1 -timeout=0 ./integration
# After final authorized candidate commits and clean tree:
just done
```

Before integration execution, confirm selected names use fake host tools/fixtures; never run an authenticated provider probe. Nested-jail verification uses `just build-dev-bundle` and its emitted absolute launcher from an owned throwaway workspace. Synthetic emitted probes must disable **both** `YOLO_NO_PROGRAM_READINESS=1` and `YOLO_NO_AUTO_CAPTURE=1`, plus `YOLO_NO_UPDATE_CHECK=1` and `YOLO_NO_AUTO_IMAGE_REAP=1`; source-verified gates, not guessed spellings. Use fake selected tools only, inventory before/after for installation/capture markers, own and remove all children/workspace/state. Do not override the compiler's HOME or caches; any fixture HOME is a scoped child-only environment under durable scratch. No `just install`/deploy.

Nested/root-UID fixtures prove wiring only: they are not real rootless reachability/store evidence or Mac placement/runtime evidence. No network-mode/rootless claim is being made by this change; if one becomes necessary, retain the required real-host `podman info --format '{{.Host.Security.Rootless}}'` / `{{.Host.RootlessNetworkCmd}}` receipts or CI and a Mac handoff separately. Do not claim an unrun native gate passed. A stale-image/runtime external blocker is reported with its next step, not worked around with live host edits.

## Accepted author closure and complete engineering contract

The accepted [scope matching](scoped-broker-policy.md#38-scope-matching), [zero/one/many reads](scoped-broker-policy.md#39-read-cells-zero-one-or-many), [timed caps](scoped-broker-policy.md#51-timed-caps) and [authority-state contract](scoped-broker-policy.md#8-authority-state-concurrency-withdrawal-failure), with D27–D36, are the binding engineering closure. This plan chooses mechanics only; the six-major review's false baseline/producer claims are corrected below. Detailed private source attribution is retained outside the public plan.

### Matcher contract, ownership and interfaces (U2/U3/U9)

| Declaration / data | Exact required interpretation |
| :--- | :--- |
| `[call_broker.scope] nesting` | `exact` or `segment`; absent and legacy manifests are exact. Unknown mode refuses declaration. TOML-only new keys. |
| `separator` | Required only for segment; one printable ASCII byte, not a letter/digit. Refused with exact. |
| `anchors` | Required nonempty literal-string list with segment; no anchor prefixes another. Refused with exact. No source-name branch or glob. |
| Scope string for a selected source | Nonempty valid UTF-8, ≤1024 bytes, no NUL/control character; segment additionally starts with exactly one anchor. Anchor alone is valid. Do not add URI/canonical-grammar checks, normalization, decoding, trimming, case folding or dot-segment semantics. |
| Scope source not selected | Do not validate its source-specific scope string or apply it; report inert. Structural TOML types/ownership and byte-identical alias duplicates retain their load rules. Shared user config must load across workspaces. |
| Exact containment | Scope S contains chain ID B only when byte-identical. A classifier-declared ancestor supplies ancestry, not a slash in an opaque ID. |
| Segment containment | B == S, or B starts with S and S ends in separator or B's next byte is separator. Compare bytes only. |
| Scope application/order | Applies if it contains at least one already-admitted chain ID. Sort by first contained chain position, leaf position zero first, then longer byte string; source scope last. Equal position/length implies the same scope, whose duplicate in one layer is already refused. |
| Rules/routes/check | One shared matcher/order. Each selects among scopes carrying its own kind of line: rule and route may come from different scopes. Segment inert reporting tests overlap in both directions with approved roots; exact reports non-root scopes as classifier-conditional, not guessed descendants. No classifier/host program/provider/read grant for load, check or why. |
| Admission | Unchanged declared type/parent/verbatim chain-to-approved-root proof first. Matching admits nothing. An anchor-wide rule/route applies only to the operations the existing roots admitted. |
| Routing | Segment context block requires nonempty `route_anchors` subset of its source anchors; a context scope under another anchor is a load error. Exact context block refuses route_anchors; every valid exact scope is routable. Context identity records the winning authored scope bytes. |
| Declaration identity | Include nesting, separator, anchors, route_anchors and optional native read_max_for in clone/hash/disclosure and immutable launch contracts. A mode/anchor/cap change reaches the same declaration gate, not a mutable daemon config shortcut. |

**Concrete leaf:** `internal/brokerpolicy/resource.go` owns immutable `ScopeMatcher`, `ScopeMatch` (scope ID, first chain position, byte length), `ValidateScope`, `Contains`, `ApplyingScopes` and pure root-overlap/report inputs. Config converts selected parsed declarations to these values. Runtime passes a chain whose admission is already proved. Routes and `yolo check` call the same functions, never a copied prefix predicate. `policy.go` consumes their ordered matches. No file/environment/store/provider I/O or dependency on callbroker in this leaf. `awsclassify` already imports callbroker, so it can import this dependency leaf without a cycle.

**AWS mechanics choice:** retain the classifier's current `cover` implementation and prove parity under H9 rather than rewriting admission. [`packs/aws/loopholes/aws-broker/loophole.toml`](../../packs/aws/loopholes/aws-broker/loophole.toml) gains segment `/`, anchors `["s3://", "ce://"]`, and context `route_anchors=["s3://"]`. CircleCI stays exact and needs no scope-mode change. H9's table includes bucket roots without `/`, prefix roots ending `/`, empty/root key, exact prefix, prefix siblings, nested prefixes and bucket-name siblings. Through actual AWS Run classification assert addressed canonical IDs and approved root bytes; compare matcher containment with the classifier cover witness for the same valid root/key pairs. Broader/new scope-only spellings are separately tested, not fed to parseRoots as newly accepted roots. In particular `s3://b` and `s3://b/` remain distinct scopes and only the former is the shipped bucket-root spelling.

**Native exception is existing admission, not generic matching:** git/github take no nesting. First prove exact approved remote/equivalent endpoint or approved GitHub repository with the shipped admission; only then select named-repository policy by shipped identity. An unapproved remote never acquires authority from normalized identity. Git's `read_capability="fetch"` and literal `read` lower to one subject at all source/repository lists and ask keys. Both words in one scope/layer (including different lists) refuse with both positioned writers on verified V6; preserve the fixed old-adapter no-never fallback if applicable; `read.fetch` refuses naming `fetch`. Shipped words/verbs keep spelling fetch; no named Git verb is added. This paragraph does not rename arbitrary generic declared read cells or remove their specific-cell rules.

### Timed roles, one limit and record compatibility (U4/U10/U11)

| Entry role | Generic cap | Native cap |
| :--- | :--- | :--- |
| Known write | Its cell max_for or existing 15 m default | Existing source policy maximum, unchanged: Git 24 h, GitHub 12 h at this baseline |
| Read | Its read cell max_for or same existing 15 m default | Selected official declaration read_max_for, absent/legacy 15 m |
| Write whose fact was withheld, candidate or single nonmember | Candidate cell max_for or same existing 15 m default | min(existing 15 m default, shipped native write maximum) |

`brokerstore.TimedGrantDefault` is the existing single default. Keep `callbroker.defaultCellMaxFor` as its existing alias; native adapters obtain that value rather than a second constant. Pure limit calculation receives already-resolved duration values from adapters, avoiding an I/O package dependency in brokerpolicy. Native `read_max_for` is TOML-only and reuses generic max_for's duration syntax/validation bounds. Shipped git/github manifest files gain no line: missing metadata deliberately yields 15 m. Legacy manifests do not recognize the new override; retain their strict/tolerant unknown-key behavior, and readable legacy declarations keep the default, never the supplied override. New user config carries no cap metadata.

**One function:** `brokerpolicy.TimedAnswerLimit` takes the asking entries and their explicit role/cell/cap values, returns the minimum plus limiting cell/role provenance, and is used by prompt/help maximum and answer validation. Source adapters resolve fresh current selected declarations/native policy inside existing approval authority at the answer boundary. It includes every asking read/write and every non-refused withheld-fact write (candidate or one known nonmember). The cap is not copied into a request/cache or derived from a provider. Expired/closed/answered request state is rejected before inspecting --for; then zero/negative/unparseable duration refuses; above-limit duration refuses naming the cell/role, showing the limit and next shorter command. No clamping for new read/withheld roles; known-write-only native requests stay on their existing validation/message path. One accepted duration gives every entry one `now + duration` expiry, or no entries at all. The existing locked store's all-or-none write path remains the effect boundary.

**Preserve history:** known-write-only native requests retain existing maxima, coverage, prompts/help and V5 front behavior. Do not shrink a broad legacy native PR/branch write entry to a new cell merely to reuse generic call entries. Old stored entries retain their recorded expiry, even after a cap change; they never cover a new read/candidate/routed identity. New roles are explicit optional record/digest discriminators; do not infer candidate from a missing old field or insert numeric caps into history. Candidate eligibility still follows the fact table; a resolved refused write does not execute or refund already-spent fact authority.

**Fixtures and deletion controls:** H15–H17/H18a/H18b/H19 use native public approve plus real selected declarations; H18 is generic real-daemon candidate authority (1h/15m), with GH differing-candidate injection labeled adapter-only. H19's private synthetic official-pack test bundle changes only the read cap to 1 h and is built from the candidate under owned scratch with caller Go environment intact; do not give a third-party pack named git/github an official exemption or edit repository/installed manifests. Assert prompt 1 h, 1 h accepted, >1 h refused, and default bundle still 15 m. Delete the native selected-declaration-to-limit call site and H19 must fail. Delete the shared prompt/check limiter call and H15/H16 and generic H18 must fail. H20 checks an explicit old eleven-hour entry remains write-covered but not read-covered. H21 combines expiry with an invalid/oversized duration and must report expiry first. V5 receipts prove finite CLI/generic fronts; browser/notification paths still receive permanent H-series tests, not an unearned baseline-green claim.

### Glossary, authoritative delta and independent review

Use the already-published glossary definitions for [Scope nesting](../glossary.md#scope-nesting), [Anchor](../glossary.md#anchor-broker-scope), [Timed limit](../glossary.md#timed-limit), [Read word](../glossary.md#read-word) and [Implicit read](../glossary.md#implicit-read). U12 checks their references against shipped behavior rather than promising a second publication or duplicating the algorithm. Update canonical reference claims only after behavior ships; the parent has separately published the exact author supplement before implementation admission. U2's declaration tests include raw TOML/legacy decode, key refusal, clone/hash mutation vectors and selected provenance. U5's public gate tests vary each new matcher/cap/route field, with unchanged trust disclosure behavior. H1–H25 are fully mapped in Appendix B; no new owner decision or invented grammar remains.

### Shipped ceiling resolution, not a replacement (U2/U3)

Within a ceiling scope compute the cell in four ordered steps (D31):

1. Its own token is in never: never.
2. No never token covers it: invoke the **existing shipped resolver**. Any covering always token wins over any covering on_request token, cell/table/kind alike. Same token in both lists is valid. Do not duplicate that algorithm in brokerpolicy; adapters supply the shipped per-cell result to the pure evaluator for this branch.
3. A table/kind never covers it and its own token is in always/on_request: higher of those two lists; cell exception wins.
4. Otherwise never.

Only exact same token in never and another list conflicts. Repetition within a list retains its shipped check. Ask-file keys remain last-file-wins, not duplicate-effective-key errors. Resource-alias byte duplicates still refuse. Native fetch in both old lists remains the same-token case; fetch+literal read is the new alias case subject to verified V6/fallback. Never is added only to selected operation-broker tables: audio and every other grant-gated service keep their old unknown-key and list behavior. Freeze A2a–A2c from actual pre-change source before implementing A2d; test both prediction and actual transaction where the old broad vector is runnable. No new compatibility-mode bit is invented.

### Zero/one/many reads and unchanged hash (U2/U3/U5/U7)

| Declared read cells | Effective read | Uncelled classifier read | Named classifier read | Config / authority |
| :--- | :--- | :--- | :--- | :--- |
| Zero, existing read grant kind | Derived implicit read, unnamed | Accepted; core derives safe chain-based description if absent | Refuse | read only; read.x refuses. Authority is existing grant kind, never declaration absence. |
| Zero, no read grant kind | None | Refuse | Refuse | read line is a load error; selection grants nothing. |
| One | That declared read cell | Adapter selects it | Must name it | read alias or read.cell; no table/default. |
| Many | Those declared cells | Refuse; no request/effect | Must name declared read cell | read alias or specific cell. |

All old declarations, TOML/legacy alike, have zero explicit read cells. The derived subject exists **outside** the parsed declaration and hash input. New optional metadata hashes omit absent fields; omitted cell kind remains its old serialized omission, not a newly emitted write string. Capture full old hash/disclosure vectors, compare bytes on the actual unmodified declarations after upgrade, and assert no declaration-only gate prompt. Explicit kind=read is TOML-only; absent means write; reserved cell name read remains forbidden permanently. A legacy supplied kind is an unknown call-broker cell key and strictly refuses the declaration, as every shipped format's cell decoder does; it is not parsed as a write. Omitted kind retains the existing default-write meaning. No legacy key/parser/grant is added and no tolerant acceptance or read-table exemption is introduced.

Requestable-by-default retains only its old source-scope read-kind gate meaning; it never supplies a scoped read or a read.cell ask. Implicit read cap is the same 15-minute constant and timed kind call:read; this cannot collide with a declared cell named read. Explicit per-operation AWS/CircleCI read-cell metadata is an additive gate-approved enhancement, not a prerequisite for source/prefix read policy or legacy operation compatibility.

### Producer reachability and withheld lookups (U7/U11/U12)

**Real generic producer:** the real Main/frontend/classifier/lookup/transaction path witnesses C6/C7/C7a/C7b/C7d–C7h/H18 with a fake source declaring write cells a/b, correctly paired all/working_set asks, and a bound declared lookup. Host read never or workspace read false with a=false/b=ask must produce no provider call and no request (C7e), including a preexisting b timed entry (C7f). With read changed to run, the counted lookup resolves the actual cell and sends only the allowed write (C7g). Fact ask + mixed refuse/ask follows the spent-at-first-fact fourth row, including denied resolved write/no refund. All-ask/non-refused candidates accept one once answer or all-or-none timed entries; H18 caps are 1h and15m, not GitHub native maxima.

**Protocol mechanics:** retain shipped `yolo.call-broker-classifier/1` request/answer normal reads; introduce its `/2` successor for typed optional reads and withheld facts/candidates. Begin every classification with the existing /1 request so old classifiers remain runnable. A /2-capable classifier declares support by labelling its answer to a /1 request `/2`; that answer's body stays /1 shape (uncelled reads, single-cell writes), and all answers to /1 requests in one classification carry one label. Core sends /2 only to a producer observed advertising in this classification, and only in two cases. (a) Typed reads: the selected declaration has >=1 explicit read cell and the first answer is a classification with >=1 read or a lookup request; core treats that answer as provisional: it validates strict shape, version label, argv/stdin hash, admitted chains, declared lookups, HTTP floors and declared write cells, defers ONLY uncelled-read cardinality, discards the answer, uses nothing from it (no lookup, decision, request or effect), and resends identical argv/stdin hash/roots with empty facts as /2; every later request in that classification is /2, and each /2 answer passes full read-cell cardinality and name validation before any lookup, provider call or decision. Cardinality is never deferred for a /1-labelled answer, a zero-read-cell declaration, a refused/local/read-free first answer, or any /2 answer; no cell name is stripped or supplied by core. (b) After a withheld bound lookup the next request is /2 with an explicit withheld status and no Value/provider data; this does not discard the prior answer or defer cardinality. /2 answers use `reads: [{cell?, chain, description?}]`; a /1-labelled answer to a /2 request is a pack bug. A refusal, local answer or read-free classification on the first answer is final with no upgrade. No capability cache: replays and nested lookup classifications renegotiate. Upgrade adds at most one classifier process (max 5), no lookup round and no fact send. A never-advertising classifier is never sent /2; with many read cells its reads refuse naming the classifier update. Core/classifier generation match is never assumed, including for shipped packs: label and shape checks run on every answer and refuse on skew before any lookup or effect. A baseline core refuses an advertising classifier at its version check, so third-party packs advertise only for this revision's core. Its write response has either one Cell or >=2 unique declared write Candidates plus the same admitted chain/canonical intent; never both. A /1 answer asking a lookup that cannot run is refused naming the read line (C7n), without treating missing facts as empty real data. Version-dispatched strict decoding preserves old uncelled/description-less reads and rejects new ambiguity/unknown fields. No rule, route/profile, authority path or host secret is classifier input. Actual lookup sends remain capped at four for the invocation and admission is unchanged.

**GitHub:** only implemented canonical ceiling words read/create_pr/create_issue/runs/approve_runs enter never; no all/working_set/cell tokens or new ask tables. Existing legacy alias transformations are preserved by their old path, not advertised as newly implemented cell words. Producer tests use built grants/defaults only. C7c/C7i/C7j/C7l/C7m are built-rights witnesses; differing candidate values for C6/C7/C7a/C7b/C7d–C7h/H18 are injected adapter-boundary checks, **not GitHub config or producer evidence**. The generic producer is the acceptance witness for those cases.

For a working-set comment/edit/approve/merge, the item's open/kind state is a fact where the native adapter reads it. If read is not run, membership covers nothing; act as nonmember, ask and send zero provider reads both before and after once. Dispatch/approve-run/rerun grants needing unreadable provider values likewise cover nothing. A withheld-write role (including a single known nonmember write) receives min(15m, native write max). H18a 30m refuses; <=15m works without fact. Baseline nonmember under run read files its original known-write request without a fact and retains its historical12h cap/message (H18b). These requests must not be conflated by role/digest or UI text. Old native broad write entries retain their lifetime/coverage where the shipped fact dependencies legitimately run; they acquire no new read/candidate authority.

### Shared-store kind and merge key (U4/U9/U10/U11)

Use accepted timed call:<cell> kinds for every new native/generic read/withheld entry; implicit read uses call:read. Keep existing known-native-write kinds/keys unchanged. V7 actually wrote committed entries and reopened the unchanged store on both native sources, checking the old entry stayed eleven hours and Snapshot/List did not fail. No once-only fallback is needed for this baseline; the fixed fallback remains refusal of native read --for if a different supported old store cannot read accepted kinds.

`WorkingEntry.Resource`, not a side field, carries role/context identity because `upsertWorkingIn` merges workspace+repo+kind+resource. Keep legacy/default-context **known-write** Resource byte-identical. Every new read/withheld role, even default-context, and every routed known write gets a domain-separated resource string: `broker-op/1:` followed by deterministic JSON `[role, legacyResourceKey, contextIdentityDigestOrEmpty]`. Roles are read / withheld-write / known-write; only the last/default case bypasses the wrapper. This prefix cannot be a valid old native ref/PR key or old CallResourceKey type prefix. The opaque context digest binds exact matched scope/name/declaration/fields; raw profile/field values are neither stored in this key/request nor printed. Same helper creates and checks keys. Candidate cell stays in call:<cell>; candidates of different cells cannot merge. New-role resource wrappers prevent an old cell-name write entry from covering a later read or candidate with that name. No old broker must understand the wrapper to parse its store.

Permanent tests reopen committed AnswerFor records with the real unchanged old store reader, not only AddWorking, and exercise default/routed/read/withheld variants under the same cell kind. Extending routed changes only that entry; default neither satisfies nor extends it, different roles cannot satisfy one another, and old11h coverage remains. New roles are optional request/digest fields; no old request is reinterpreted. Known-write-only over-limit validation/help/message stays on the shipped branch even after TimedLimit hooks exist. New-role errors name minimum/cell/role and next shorter command. HTTP-front context blocks refuse declaration (they cannot mutate an HTTP executor's host program environment); a CLI synthetic non-AWS context proves source neutrality.

### Major-by-major closure and required acceptance surfaces

| Review finding | Closed plan obligation | Real witness / limit |
| :--- | :--- | :--- |
| 1 impossible Appendix A | Nonconflicting scopes.reports.never all plus valid working_set scaffold | Exact revised public editor source compiled/red; future semantic selected-pack assertions required. |
| 2 baseline lists | Existing resolver wins without a covering never; never conflicts operation brokers only | Frozen AWS/native/audio vectors, then A2d never exception. No unearned public empty-coverage pass. |
| 3 zero read cells | Derived implicit grant-bound read; zero/one/many matrix, old hash unchanged | Actual legacy/TOML old-protocol reads and H26–H32; no mandatory pack declaration update. |
| 4 GitHub reachability | Mixed values real generic daemon; GitHub built-rights producer vs injected adapter separated | C7l/H18a15m withheld state; H18b known-write baseline maximum/message; C7m token set. |
| 5 split source read | Snapshot only source of all asks; grant projection any failure is source install failure | G6a/b actual call, each read/grant/scope/marker failure and truthful current snapshot disposition. |
| 6 old consumer ignored keys | Daemon-produced launch-bound proof; no proof+changed ask has zero writes/roots | Real old broker G6c versus shipped roots-only G6d beside existing older-broker fixture tests. |
| R7 bounded backlog | Merge-key roles/context, accepted old-store kinds, HTTP context refusal, G6e current roots, G15a timed import | Actual old store, actual public approve after apply, actual import fetch caller—not helper-only claims. |

Focused review and canonical publication complete this hand-off; independently materialized candidate admission still precedes implementation. This table is repair accounting, not a runtime acceptance claim. No owner question remains.


## Appendix A — first permanent RED → GREEN editing test (complete source)

Place in `internal/cli/scopedbrokeredit_test.go`, then run `go test -count=1 -run '^TestScopedBrokerEditingVerbsReachNewTOMLPaths$' -v ./internal/cli`. Baseline fails at the first unsupported scoped-policy schema boundary. The supplied body drives registered config dispatch and its public post-write check, with complete source/resource/root/ceiling scaffolding rather than expecting an invalid partial scope to load. It neither approves the fixture nor invokes the broker:

```go
package cli

import (
    "os"
    "reflect"
    "strings"
    "testing"

    "github.com/mschulkind-oss/yolo-jail/internal/config"
    "github.com/mschulkind-oss/yolo-jail/internal/configdoc"
    "github.com/mschulkind-oss/yolo-jail/internal/jsonx"
    "github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func assertScopedEditingAWSSelected(t *testing.T, userPath string) {
    t.Helper()
    raw, err := os.ReadFile(userPath)
    if err != nil { t.Fatal(err) }
    doc, err := configdoc.Parse(raw, configdoc.TOML)
    if err != nil { t.Fatal(err) }
    selected, ok := doc.Values.Get("packs")
    if !ok || !reflect.DeepEqual(selected, []any{"aws"}) { t.Fatalf("fixture packs=%#v", selected) }
    // Resolve this asserted bare shipped selection without asking the old config
    // decoder to understand the deliberately unsupported scopes fixture first.
    pack, err := resolveConfiguredPack(config.PackEntry{Name:"aws", IsEmbedded:true, EmbeddedName:"aws"})
    if err != nil { t.Fatal(err) }
    brokers, _, err := config.ReadableDeclarationsForPacks([]*packload.Pack{pack})
    if err != nil { t.Fatal(err) }
    for _, broker := range brokers {
        if broker.CallBroker != nil && broker.CallBroker.Source == "aws" && len(broker.CallBroker.Cells) > 0 { return }
    }
    t.Fatal("selected aws has no actual call-broker declaration/cells")
}

func TestScopedBrokerEditingRejectsUndeclaredNeverToken(t *testing.T) {
    user, _ := genericFixture(t, `packs = ["aws"]
[allow.aws]
on_request = ["read"]
`, configdoc.TOML, `[call_roots]
aws = ["s3://b"]
`)
    assertScopedEditingAWSSelected(t, user.Path)
    rc, out, errs := runConfigVerb(t, "add", "allow.aws.never", "not_declared", "--user")
    if rc == 0 || (!strings.Contains(out+errs, "not_declared") && !strings.Contains(errs, "no value type in the readable config schema")) {
        t.Fatalf("undeclared token accepted or not identified: rc=%d stdout=%q stderr=%q", rc, out, errs)
    }
}

func TestScopedBrokerEditingVerbsReachNewTOMLPaths(t *testing.T) {
    user, ws := genericFixture(t, `# fixture only
packs = ["aws"]
[allow.aws]
on_request = ["read", "all.put_objects", "all.delete_objects"]
[scopes.reports]
source = "aws"
resource = "s3://b/old/"
`, configdoc.TOML, `# fixture only
[call_roots]
aws = ["s3://a", "s3://b"]
[scopes.team]
source = "aws"
resource = "s3://a/team/"
[scopes.team.requests.working_set]
put_objects = true
`)
    assertScopedEditingAWSSelected(t, user.Path)
    for _, args := range [][]string{
        {"add", "scopes.reports.allow.never", "all", "--user"},
        {"set", "scopes.reports.resource", "s3://b/reports/", "--user"},
        {"set", "scopes.team.requests.all.put_objects", "true", "--project"},
    } {
        rc, out, errs := runConfigVerb(t, args...)
        if rc != 0 {
            t.Fatalf("args=%q rc=%d stdout=%q stderr=%q", args, rc, out, errs)
        }
    }
    for _, tc := range []struct{ path string; keys []string; want any }{
        {user.Path, []string{"scopes", "reports", "allow", "never"}, []any{"all"}},
        {user.Path, []string{"scopes", "reports", "resource"}, "s3://b/reports/"},
        {ws.Path, []string{"scopes", "team", "requests", "all", "put_objects"}, true},
        {ws.Path, []string{"scopes", "team", "requests", "working_set", "put_objects"}, true},
    } {
        data, err := os.ReadFile(tc.path)
        if err != nil { t.Fatal(err) }
        doc, err := configdoc.Parse(data, configdoc.TOML)
        if err != nil { t.Fatal(err) }
        var value any = doc.Values
        for _, key := range tc.keys {
            table, ok := value.(*jsonx.OrderedMap)
            if !ok { t.Fatalf("path=%v reached %T before %s", tc.keys, value, key) }
            value, ok = table.Get(key)
            if !ok { t.Fatalf("path=%v missing %s", tc.keys, key) }
        }
        if !reflect.DeepEqual(value, tc.want) { t.Fatalf("path=%v got=%#v want=%#v",tc.keys,value,tc.want) }
    }
}
```

The negative control and positive body have separate fixtures: a failed edit may leave changed bytes and must not contaminate the positive setup. Both assert real selected AWS declaration/cell coverage using existing resolver helpers before registered dispatch. The negative's undeclared token must refuse and identify the token once `never` has a schema target; the baseline's unsupported-schema refusal is also accepted by this compilable control, not proof of future token semantics. The positive retains the actual unsupported scoped-schema RED (`config key scopes.reports.allow.never has no readable schema target`) as an owned test-only overlay, never a deliberately red repository commit. U2 must make the positive green while retaining the negative. No fixture text or successful config check is a live grant: these tests never answer a gate or invoke a provider. Add resulting-policy/provenance assertions in U2 alongside the exact serialized values.

## Appendix B — acceptance traceability (all 144 design scenarios)

The table maps every source scenario to an owning unit and named permanent consumer test/subtest. Differing-candidate rows explicitly identify the generic producer and label GitHub's injected adapter-only coverage; no unsupported native config is counted as producer evidence. A test name here is a required addition, not a claim that it exists or passed. Keep IDs in subtest names and link the authoritative scenario table rather than duplicating its policy argument.

| ID | Unit | Permanent consumer test/subtest | Required witness |
| :--- | :--- | :--- | :--- |
| A1 | U4/U5/U12 | `internal/callbrokerconformance: TestScopedBrokerCompatibility/A1` | all four adapters; decisions, digest vectors, no effect without authority |
| A2 | U4/U5/U12 | `internal/callbrokerconformance: TestScopedBrokerCompatibility/A2` | all four adapters; decisions, digest vectors, no effect without authority |
| A2a | U2/U3/U12 | `internal/config/scopedbrokerpolicy_test.go: TestFrozenBrokerListCompatibility/A2a` | call/native/read same token two lists loads; always wins; real source vectors |
| A2b | U2/U3/U12 | `internal/callbroker/scoped_call_test.go: TestFrozenBrokerListCompatibility/A2b` | broad all always plus cell ask preserves run/digest under valid paired asks |
| A2c | U2/U12 | `internal/config/scopedbrokerpolicy_test.go: TestFrozenBrokerListCompatibility/A2c` | audio same lists unchanged; never unknown as shipped |
| A2d | U2/U3/U7/U12 | `internal/callbroker/scoped_call_test.go: TestNeverException/A2d` | never all + always all.put_objects: put runs, other writes refuse |
| A3 | U4/U5/U12 | `internal/callbrokerconformance: TestScopedBrokerCompatibility/A3` | all four adapters; decisions, digest vectors, no effect without authority |
| A4 | U4/U5/U12 | `internal/callbrokerconformance: TestScopedBrokerCompatibility/A4` | all four adapters; decisions, digest vectors, no effect without authority |
| B1 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B1` | positioned TOML, actual classified call and request/effect count |
| B2 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B2` | positioned TOML, actual classified call and request/effect count |
| B3 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B3` | positioned TOML, actual classified call and request/effect count |
| B4 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B4` | positioned TOML, actual classified call and request/effect count |
| B4a | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B4a` | positioned TOML, actual classified call and request/effect count |
| B4b | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B4b` | positioned TOML, actual classified call and request/effect count |
| B4c | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B4c` | positioned TOML, actual classified call and request/effect count |
| B5 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B5` | positioned TOML, actual classified call and request/effect count |
| B6 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B6` | positioned TOML, actual classified call and request/effect count |
| B7 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B7` | positioned TOML, actual classified call and request/effect count |
| B8 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B8` | positioned TOML, actual classified call and request/effect count |
| B9 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B9` | positioned TOML, actual classified call and request/effect count |
| B10 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B10` | positioned TOML, actual classified call and request/effect count |
| B11 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B11` | positioned TOML, actual classified call and request/effect count |
| B12 | U2/U3/U12 | `internal/config + brokerpolicy + source conformance: TestScopedBrokerRules/B12` | positioned TOML, actual classified call and request/effect count |
| C1 | U4/U7 | `internal/callbroker: TestScopedOperationApprovals/C1` | real CLI/HTTP bundle; durable reads/writes/digest/uses/entries |
| C2 | U4/U7 | `internal/callbroker: TestScopedOperationApprovals/C2` | real CLI/HTTP bundle; durable reads/writes/digest/uses/entries |
| C3 | U4/U7 | `internal/callbroker: TestScopedOperationApprovals/C3` | real CLI/HTTP bundle; durable reads/writes/digest/uses/entries |
| C4 | U4/U7 | `internal/callbroker: TestScopedOperationApprovals/C4` | real CLI/HTTP bundle; durable reads/writes/digest/uses/entries |
| C5 | U4/U7 | `internal/callbroker: TestScopedOperationApprovals/C5` | real CLI/HTTP bundle; durable reads/writes/digest/uses/entries |
| C6 | U4/U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/C6` | real Main/front, differing write values, bound withheld lookup, request/spend/provider/effect counters; GH injected adapter-boundary only, not producer/config reachable |
| C7 | U4/U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/C7` | real Main/front, differing write values, bound withheld lookup, request/spend/provider/effect counters; GH injected adapter-boundary only, not producer/config reachable |
| C7a | U4/U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/C7a` | real Main/front, differing write values, bound withheld lookup, request/spend/provider/effect counters; GH injected adapter-boundary only, not producer/config reachable |
| C7b | U4/U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/C7b` | real Main/front, differing write values, bound withheld lookup, request/spend/provider/effect counters; GH injected adapter-boundary only, not producer/config reachable |
| C7c | U11/U7 | `internal/ghbroker: TestBoundFactPolicy/C7c` | GitHub built-rights producer plus generic witness; no unsupported GitHub config values |
| C7d | U4/U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/C7d` | real Main/front, differing write values, bound withheld lookup, request/spend/provider/effect counters; GH injected adapter-boundary only, not producer/config reachable |
| C7e | U4/U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/C7e` | real Main/front, differing write values, bound withheld lookup, request/spend/provider/effect counters; GH injected adapter-boundary only, not producer/config reachable |
| C7f | U4/U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/C7f` | real Main/front, differing write values, bound withheld lookup, request/spend/provider/effect counters; GH injected adapter-boundary only, not producer/config reachable |
| C7g | U4/U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/C7g` | real Main/front, differing write values, bound withheld lookup, request/spend/provider/effect counters; GH injected adapter-boundary only, not producer/config reachable |
| C7h | U4/U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/C7h` | real Main/front, differing write values, bound withheld lookup, request/spend/provider/effect counters; GH injected adapter-boundary only, not producer/config reachable |
| C7i | U11/U7 | `internal/ghbroker: TestBoundFactPolicy/C7i` | GitHub built-rights producer plus generic witness; no unsupported GitHub config values |
| C7j | U11/U7 | `internal/ghbroker: TestBoundFactPolicy/C7j` | GitHub built-rights producer plus generic witness; no unsupported GitHub config values |
| C7k | U11/U7 | `internal/ghbroker: TestBoundFactPolicy/C7k` | public ServeContext; ordered fact/write sends and unchanged/refused authority |
| C7l | U11/U12 | `internal/ghbroker/scopedread_test.go: TestGitHubBuiltRights/C7l` | working member read ask/never becomes nonmember ask; once sends write and zero provider reads |
| C7m | U2/U11/U12 | `internal/config/scopedbrokerpolicy_test.go: TestGitHubBuiltRights/C7m` | never accepts five implemented canonical tokens only; tables/cell paths refuse |
| C7n | U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/C7n` | actual old /1 classifier lookup withheld refuses naming read line, zero lookup/request/write |
| C8 | U7/U11 | `internal/callbroker + ghbroker: TestScopedPredictionHasNoEffects/C8` | zero file/spend/send; fact policy checked |
| D1 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D1` | actual source fronts; filed/answered/disconnected/stopped events |
| D2 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D2` | actual source fronts; filed/answered/disconnected/stopped events |
| D3 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D3` | actual source fronts; filed/answered/disconnected/stopped events |
| D4 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D4` | actual source fronts; filed/answered/disconnected/stopped events |
| D5 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D5` | actual source fronts; filed/answered/disconnected/stopped events |
| D6 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D6` | actual source fronts; filed/answered/disconnected/stopped events |
| D7 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D7` | actual source fronts; filed/answered/disconnected/stopped events |
| D8 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D8` | actual source fronts; filed/answered/disconnected/stopped events |
| D9 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D9` | actual source fronts; filed/answered/disconnected/stopped events |
| D10 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D10` | actual source fronts; filed/answered/disconnected/stopped events |
| D11 | U8/U12 | `internal/callbrokerconformance: TestScopedApprovalLifecycle/D11` | actual source fronts; filed/answered/disconnected/stopped events |
| D12 | U11 | `internal/ghbroker: TestScopedHoldExecutionBudget/D12` | fake late-answer event; fresh execution deadline |
| D13 | U8 | `internal/callbroker: TestScopedMainLifecycle/D13` | real Main subprocess; hook/join/End/PublishFailed; deletion reds |
| E1 | U4/U6 | `internal/callbroker + brokerstore: TestScopedAtomicAuthority/E1` | barrier-ordered caller/approver/revoke races; durable uses and exact sends |
| E2 | U4/U6 | `internal/callbroker + brokerstore: TestScopedAtomicAuthority/E2` | barrier-ordered caller/approver/revoke races; durable uses and exact sends |
| E3 | U4/U6 | `internal/callbroker + brokerstore: TestScopedAtomicAuthority/E3` | barrier-ordered caller/approver/revoke races; durable uses and exact sends |
| E4 | U4/U6 | `internal/callbroker + brokerstore: TestScopedAtomicAuthority/E4` | barrier-ordered caller/approver/revoke races; durable uses and exact sends |
| E5 | U4/U6 | `internal/callbroker + brokerstore: TestScopedAtomicAuthority/E5` | barrier-ordered caller/approver/revoke races; durable uses and exact sends |
| E6 | U4/U6 | `internal/callbroker + brokerstore: TestScopedAtomicAuthority/E6` | barrier-ordered caller/approver/revoke races; durable uses and exact sends |
| F1 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F1` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F2 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F2` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F3 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F3` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F4 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F4` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F5 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F5` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F6 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F6` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F7 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F7` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F8 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F8` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F9 | U12 | `internal/cli/check: TestScopedPolicyLeavesAWSAuthUnchanged/F9` | unchanged aws-auth paths/report; synthetic config-only check |
| F10 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F10` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F11 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F11` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F12 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F12` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F13 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F13` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F14 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F14` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| F15 | U9 | `internal/callbroker + config: TestDeclaredExecutionContexts/F15` | user-only positioned TOML, fake env recorder, identity/request/uses checks |
| G1 | U5/U10/U11 | `internal/cli/run: TestScopedPolicyModelMigration/G1` | real gate record/read grant transition and subsequent source call |
| G2 | U5/U10/U11 | `internal/cli/run: TestScopedPolicyModelMigration/G2` | real gate record/read grant transition and subsequent source call |
| G3 | U5/U10/U11 | `internal/cli/run: TestScopedPolicyModelMigration/G3` | real gate record/read grant transition and subsequent source call |
| G4 | U5/U10/U11 | `internal/cli/run: TestScopedPolicyModelMigration/G4` | real gate record/read grant transition and subsequent source call |
| G5 | U6 | `internal/cli/run + callbroker: TestScopedLivePolicyApply/G5` | actual apply plus next-call outcome; failure injection/binding |
| G6 | U6/U12 | `internal/cli/run/scopedbrokerapply_test.go: TestScopedPolicyFailure/G6` | sidecar/grant/scope install errors nonzero; marker success refuses actual call; failed marker old/new/unknown disposition is truthful |
| G6a | U6/U12 | `internal/cli/run/scopedbrokerapply_test.go: TestScopedPolicyFailure/G6a` | source read true->false + scoped false->true; each read/write failure cannot mix old grant/new ask |
| G6b | U6/U7/U12 | `internal/callbroker/livepolicy_test.go: TestExclusiveSnapshotAsk/G6b` | stale grant true ignored when snapshot false; grant read count0, actual read refuses |
| G6c | U6/U12 | `internal/cli/run/liveapplyolderbrokers_test.go: TestSnapshotApplyRealOldBroker/G6c` | real fixed old broker has no daemon proof; changed asks leaves all source files/roots unchanged, nonzero stop advice |
| G6d | U6/U12 | `internal/cli/run/liveapplyolderbrokers_test.go: TestSnapshotApplyRealOldBroker/G6d` | same real older fixture roots-only equals its frozen shipped result and skew note |
| G6e | U6/U4/U12 | `internal/cli/scopedbrokerapprove_test.go: TestApproveCurrentLiveRoot/G6e` | file pending under newly applied root then public approve succeeds on current scope/snapshot |
| G7 | U6 | `internal/cli/run + callbroker: TestScopedLivePolicyApply/G7` | actual apply plus next-call outcome; failure injection/binding |
| G8 | U4 | `internal/callbroker: TestScopedTableSelectionAtDispatch/G8` | pure prediction vs real transaction; membership withdrawal barrier |
| G9 | U4 | `internal/callbroker: TestScopedTableSelectionAtDispatch/G9` | pure prediction vs real transaction; membership withdrawal barrier |
| G10 | U4 | `internal/callbroker: TestScopedExactFreshLaunchCLIAndHTTP/G10` | one spend; stored holder bytes unchanged; foreign holders zero effect |
| G11 | U4/U8 | `internal/cli: TestScopedApprovalRefusesEndedPendingLaunch/G11` | real Main End then public approve; no grant |
| G12 | U4 | `internal/callbroker: TestSelectedCLIWorkspaceProgramRefusedAtStartup/G12` | real startup direct/symlink plus native exemption; zero serve/effect |
| G13 | U10 | `internal/gitbroker + skillimports: TestScopedFetchCredentialBoundary/G13` | native helper/TLS/SSH/fake credential recorder and import cache class |
| G14 | U10 | `internal/gitbroker + skillimports: TestScopedFetchCredentialBoundary/G14` | native helper/TLS/SSH/fake credential recorder and import cache class |
| G15 | U10 | `internal/gitbroker + skillimports: TestScopedFetchCredentialBoundary/G15` | native helper/TLS/SSH/fake credential recorder and import cache class |
| G15a | U10/U12 | `internal/skillimports/scopedfetch_test.go: TestTimedFetchImport/G15a` | actual resolver->FetchImport fake credentialed HTTPS when timed fetch live; expiry/withdrawal anonymous/SSH refusal, no request |
| G16 | U10 | `internal/gitbroker + skillimports: TestScopedFetchCredentialBoundary/G16` | native helper/TLS/SSH/fake credential recorder and import cache class |
| G17 | U10 | `internal/gitbroker + skillimports: TestScopedFetchCredentialBoundary/G17` | native helper/TLS/SSH/fake credential recorder and import cache class |
| G18 | U10 | `internal/gitbroker + skillimports: TestScopedFetchCredentialBoundary/G18` | native helper/TLS/SSH/fake credential recorder and import cache class |
| G19 | U10 | `internal/gitbroker + skillimports: TestScopedFetchCredentialBoundary/G19` | native helper/TLS/SSH/fake credential recorder and import cache class |
| G20 | U7/U11 | `internal/callbroker + ghbroker: TestScopedRefusalHasNoAnonymousFallback/G20` | no fake provider/credential send or request |
| G21 | U5/U6 | `internal/config + cli/run: TestScopedDeclarationGate/G21` | public gate/record/real next call; prompt origin/count/trust blocks |
| G22 | U5/U6 | `internal/config + cli/run: TestScopedDeclarationGate/G22` | public gate/record/real next call; prompt origin/count/trust blocks |
| G23 | U5/U6 | `internal/config + cli/run: TestScopedDeclarationGate/G23` | public gate/record/real next call; prompt origin/count/trust blocks |
| G24 | U5/U6 | `internal/config + cli/run: TestScopedDeclarationGate/G24` | public gate/record/real next call; prompt origin/count/trust blocks |
| G25 | U5/U6 | `internal/config + cli/run: TestScopedDeclarationGate/G25` | public gate/record/real next call; prompt origin/count/trust blocks |
| H1 | U2/U3/U7/U12 | `internal/brokerpolicy/resource_test.go + internal/callbroker/scoped_call_test.go: TestScopedBrokerMatching/H1` | shared matcher plus real classified call; sibling/root/case bytes, governing line, admission and effect counters |
| H2 | U2/U3/U7/U12 | `internal/brokerpolicy/resource_test.go + internal/callbroker/scoped_call_test.go: TestScopedBrokerMatching/H2` | shared matcher plus real classified call; sibling/root/case bytes, governing line, admission and effect counters |
| H3 | U2/U3/U7/U12 | `internal/brokerpolicy/resource_test.go + internal/callbroker/scoped_call_test.go: TestScopedBrokerMatching/H3` | shared matcher plus real classified call; sibling/root/case bytes, governing line, admission and effect counters |
| H4 | U2/U3/U7/U12 | `internal/brokerpolicy/resource_test.go + internal/callbroker/scoped_call_test.go: TestScopedBrokerMatching/H4` | shared matcher plus real classified call; sibling/root/case bytes, governing line, admission and effect counters |
| H5 | U2/U3/U7/U12 | `internal/brokerpolicy/resource_test.go + internal/callbroker/scoped_call_test.go: TestScopedBrokerMatching/H5` | shared matcher plus real classified call; sibling/root/case bytes, governing line, admission and effect counters |
| H6 | U2/U5/U12 | `internal/config/scopedbrokerpolicy_test.go + internal/cli/scopedbrokerinspect_test.go: TestScopedBrokerMatching/H6` | actual TOML/declaration load and public check/why; positioned error or inert report, classifier/host starts=0 |
| H7 | U2/U5/U12 | `internal/config/scopedbrokerpolicy_test.go + internal/cli/scopedbrokerinspect_test.go: TestScopedBrokerMatching/H7` | actual TOML/declaration load and public check/why; positioned error or inert report, classifier/host starts=0 |
| H8 | U2/U3/U7/U12 | `internal/brokerpolicy/resource_test.go + internal/callbroker/scoped_call_test.go: TestScopedBrokerMatching/H8` | shared matcher plus real classified call; sibling/root/case bytes, governing line, admission and effect counters |
| H9 | U2/U3/U12 | `internal/awsclassify/scopeparity_test.go: TestAWSDeclaredScopeCoverParity/H9` | actual Run canonical IDs/admitted root plus unchanged classifier cover vs shared Contains on each valid root/key pair |
| H10 | U2/U5/U12 | `internal/config/scopedbrokerpolicy_test.go + internal/cli/scopedbrokerinspect_test.go: TestScopedBrokerMatching/H10` | actual TOML/declaration load and public check/why; positioned error or inert report, classifier/host starts=0 |
| H11 | U2/U5/U12 | `internal/config/scopedbrokerpolicy_test.go + internal/cli/scopedbrokerinspect_test.go: TestScopedBrokerMatching/H11` | actual TOML/declaration load and public check/why; positioned error or inert report, classifier/host starts=0 |
| H12 | U2/U3/U7/U12 | `internal/brokerpolicy/resource_test.go + internal/callbroker/scoped_call_test.go: TestScopedBrokerMatching/H12` | shared matcher plus real classified call; sibling/root/case bytes, governing line, admission and effect counters |
| H13 | U9/U12 | `internal/callbroker/context_test.go: TestScopedBrokerMatching/H13` | rule prefix differs from winning bucket route; context identity records authored bucket scope, no profile value leak |
| H14 | U10/U12 | `internal/gitbroker/fetchpolicy_test.go: TestScopedBrokerMatching/H14` | identity-equivalent unapproved remote; zero credential fill/send/spawn, no rule applied |
| H15 | U4/U11/U12 | `internal/cli/scopedbrokertimed_test.go + internal/ghbroker/scopedpolicy_test.go: TestScopedBrokerTimedCaps/H15` | 20m read answer refuses at15m with limiting cell;15m accepts |
| H16 | U4/U11/U12 | `internal/cli/scopedbrokertimed_test.go + internal/ghbroker/scopedpolicy_test.go: TestScopedBrokerTimedCaps/H16` | known write + asking read min15m;1h refusal leaves zero new entries |
| H17 | U4/U11/U12 | `internal/cli/scopedbrokertimed_test.go + internal/ghbroker/scopedpolicy_test.go: TestScopedBrokerTimedCaps/H17` | known-write-only shipped12h maximum accepted, old coverage unchanged |
| H18 | U4/U7/U12 | `internal/callbroker/scoped_fact_daemon_test.go: TestGenericWithheldFactCandidates/H18` | real generic candidate caps1h/15m;30m refusal;10m both entries share expiry; GH differing candidates adapter-only |
| H18a | U4/U11/U12 | `internal/cli/scopedbrokertimed_test.go: TestGitHubBuiltTimedRoles/H18a` | withheld-read working item nonmember ask30m refuses at15m; zero facts |
| H18b | U4/U11/U12 | `internal/cli/scopedbrokertimed_test.go: TestGitHubBuiltTimedRoles/H18b` | baseline nonmember/read-run known-write12h accepts; old over-limit message intact |
| H19 | U4/U11/U12 | `internal/cli/scopedbrokertimed_test.go + internal/ghbroker/scopedpolicy_test.go: TestScopedBrokerTimedCaps/H19` | parsed1h selected official native test declaration; prompt/check agree,1h accepted; no custom-pack exemption |
| H20 | U4/U11/U12 | `internal/cli/scopedbrokertimed_test.go + internal/ghbroker/scopedpolicy_test.go: TestScopedBrokerTimedCaps/H20` | pre-revision eleven-hour entry remains write-only covered with original expiry |
| H21 | U4/U11/U12 | `internal/cli/scopedbrokertimed_test.go + internal/ghbroker/scopedpolicy_test.go: TestScopedBrokerTimedCaps/H21` | expired request takes precedence over invalid/oversized --for; nothing written |
| H22 | U2/U4/U10/U12 | `internal/config/scopedbrokerpolicy_test.go + internal/loopholedecl/brokered_test.go + internal/gitbroker/fetchpolicy_test.go: TestNativeReadCompatibility/H22` | shipped fetch source spelling still runs under same admitted remote |
| H23 | U2/U10/U12 | `internal/config/scopedbrokerpolicy_test.go: TestNativeReadCompatibility/H23` | new fetch+read collision names both lines; fetch alone in always/on_request loads as always; V6 fallback only where old adapter accepted both |
| H24 | U2/U4/U10/U12 | `internal/config/scopedbrokerpolicy_test.go + internal/loopholedecl/brokered_test.go + internal/gitbroker/fetchpolicy_test.go: TestNativeReadCompatibility/H24` | native read.fetch refuses naming fetch; no generic-cell rename |
| H25 | U2/U4/U10/U12 | `internal/config/scopedbrokerpolicy_test.go + internal/loopholedecl/brokered_test.go + internal/gitbroker/fetchpolicy_test.go: TestNativeReadCompatibility/H25` | new cap not recognized in legacy; existing unknown-key treatment/default15m, no legacy override |
| H26 | U2/U3/U5/U7/U12 | `internal/callbroker/scopedclassification_test.go: TestReadCellCardinality/H26` | actual unchanged TOML/legacy old reads run; byte-identical hash/disclosure, zero gate prompt |
| H27 | U2/U3/U7/U12 | `internal/callbroker/scopedclassification_test.go: TestReadCellCardinality/H27` | scoped read governs derived implicit; read.x load refuses |
| H28 | U2/U3/U7/U12 | `internal/callbroker/scopedclassification_test.go: TestReadCellCardinality/H28` | no read grant/cell: read line error, every read refuses; selection grants nothing |
| H29 | U3/U7/U12 | `internal/callbroker/scopedclassification_test.go: TestReadCellCardinality/H29` | one declared read absorbs old uncelled read |
| H30 | U3/U7/U12 | `internal/callbroker/scopedclassification_test.go: TestReadCellCardinality/H30` | many cells require named valid read; uncelled/unknown refuse with no request |
| H31 | U2/U3/U12 | `internal/loopholedecl/callbroker_test.go: TestReadCellCardinality/H31` | read tables/defaults/name read refused; legacy kind is an unknown cell key and strictly refuses, no new legacy feature |
| H32 | U4/U7/U12 | `internal/cli/scopedbrokertimed_test.go: TestReadCellCardinality/H32` | implicit read timed call:read resource-role key15m cap; old-store reader still loads |

**Traceability check:** 144 distinct IDs (initial 122 plus 22 accepted repair IDs), each mapped exactly once. Cross-package unit and integration witnesses are additive; a shared evaluator test is never the only evidence for a production caller.

## Appendix C — permanent holder and selector reds (complete source)

Place in `internal/callbroker/scoped_policy_regression_test.go` and run `go test -count=1 -timeout=8m -run '^(TestSelectedCLIExactAnswerSurvivesFreshLaunch|TestSelectedCLIWorkingSetOnlyCeilingRunsThroughTransaction)$' -v ./internal/callbroker`. These bodies are the retained synthetic reproductions promoted onto the existing production handler fixtures; they were rerun against the stamped baseline. When U6 replaces frozen asks with live snapshots, update only fixture publication to the gate-owned snapshot helper, retaining the real handler call and all holder/history/effect assertions. Do not remove these tests in favor of a pure evaluator green.

```go
package callbroker

import (
 "context"
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/mschulkind-oss/yolo-jail/internal/brokercore"
 "github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
 "github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
 "github.com/mschulkind-oss/yolo-jail/internal/config"
 "github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

func republishScopedRegressionInput(t *testing.T, f *onceFixture) {
 t.Helper()
 raw, err := json.Marshal(f.h.input); if err != nil { t.Fatal(err) }
 if err := os.WriteFile(f.inputPath, raw, 0600); err != nil { t.Fatal(err) }
 f.h.original = raw
}

func TestSelectedCLIExactAnswerSurvivesFreshLaunch(t *testing.T) {
 f := selectedOnceFixture(t)
 if v, _, err := f.call(t, "ask", "update", "a1"); err != nil || v.Verdict != "asks" { t.Fatalf("file: %+v %v", v, err) }
 if _, err := f.store.Decide(f.pending(t)[0].ID, "pending", brokerstore.Decision{}); err != nil { t.Fatal(err) }
 sf := f.scope
 var err error
 sf.LaunchID, err = brokerscope.NewLaunchID(); if err != nil { t.Fatal(err) }
 sf.BrokerStart, sf.BrokerPID, sf.CallCLIPath, sf.CallCLIHash = "", 0, "", ""
 sf.CallInput = ""
 path, err := brokerscope.Write(sf); if err != nil { t.Fatal(err) }
 in := f.h.input
 in.ScopeFile, in.FrontJailID = path, "fresh-launch"
 _, block, err := loopholegrant.Read(in.GrantFile); if err != nil || block == nil { t.Fatalf("read fixture grant: %v", err) }
 block.LaunchID = sf.LaunchID
 grantRaw, err := loopholegrant.Merge([]byte("{}"), *block); if err != nil { t.Fatal(err) }
 in.GrantFile = filepath.Join(f.home, "fresh-grant.json")
 if err := os.WriteFile(in.GrantFile, grantRaw, 0600); err != nil { t.Fatal(err) }
 inputPath := filepath.Join(f.home, "fresh-selected.json")
 raw, err := json.Marshal(in); if err != nil { t.Fatal(err) }
 if err := os.WriteFile(inputPath, raw, 0600); err != nil { t.Fatal(err) }
 if err := brokerscope.BindCallInput(path, sf, inputPath); err != nil { t.Fatal(err) }
 sf, err = brokerscope.ReadFile(path); if err != nil { t.Fatal(err) }
 h, err := newCLIHandler(inputPath, f.home, f.store, f.h.audit); if err != nil { t.Fatal(err) }
 h.classifier.deadline = fixtureClassifierDeadline
 start, err := brokerstore.NewStartID(); if err != nil { t.Fatal(err) }
 bound, lease, err := brokercore.BindLease(in.Source, path, sf, start); if err != nil { t.Fatal(err) }
 t.Cleanup(func() { if err := brokercore.CloseLease(in.Source, lease); err != nil { t.Error(err) } })
 if err := brokerscope.BindCallCLI(path, bound, h.program.path, h.program.sha256); err != nil { t.Fatal(err) }
 bound.CallCLIPath, bound.CallCLIHash = h.program.path, h.program.sha256
 h.scopePath, h.bound, h.start = path, bound, start
 f.h, f.store.Revalidate = h, h.revalidate
 if _, _, err := h.currentAuthority(); err != nil { t.Fatalf("fresh authority: %v", err) }
 v, _, err := f.call(t, "act", "update", "a1")
 snap, snapErr := f.store.Snapshot(); if snapErr != nil { t.Fatal(snapErr) }
 t.Logf("fresh result=%+v error=%v effects=%d uses=%d", v, err, f.effects(t), snap.Grants[0].Uses)
 if err != nil || v.Verdict != "runs" || f.effects(t) != 1 || snap.Grants[0].Uses != 0 { t.Fatalf("workspace-held exact answer rejected by fresh live holder") }
}

func TestSelectedCLIWorkingSetOnlyCeilingRunsThroughTransaction(t *testing.T) {
 f := selectedOnceFixtureWithStanding(t, true)
 raw, err := os.ReadFile(f.userPath); if err != nil { t.Fatal(err) }
 raw = []byte(strings.Replace(string(raw), `"all.change"`, `"working_set.change"`, 1))
 if err := os.WriteFile(f.userPath, raw, 0600); err != nil { t.Fatal(err) }
 p := f.h.input.EffectiveCells["change"]
 p.Tables["all"] = config.CallBrokerTablePolicy{Request:"false", Ceiling:"off", Decision:"refuse"}
 f.h.input.EffectiveCells["change"] = p
 republishScopedRegressionInput(t, f)
 if _, _, err := f.store.AddWorking(brokerstore.WorkingEntry{Workspace:f.home, Repo:f.h.input.Source, Kind:brokerstore.WorkingPR, Resource:"#1", By:"fixture"}); err != nil { t.Fatal(err) }
 answer, err := f.h.classifier.ClassifyCLI(context.Background(), []string{"update-pr", "1"}, nil, nil); if err != nil || answer.Classification == nil { t.Fatalf("classify: %v", err) }
 _, _, working, now, err := f.store.InspectContext(context.Background()); if err != nil { t.Fatal(err) }
 pure := decideCall(*answer.Classification, f.h.input.EffectiveCells, working, f.home, f.h.input.Source, now)
 if pure.Verdict != "runs" { t.Fatalf("fixture pure verdict: %+v", pure) }
 v, _, err := f.call(t, "act", "update-pr", "1")
 t.Logf("pure=%+v actual=%+v error=%v effects=%d", pure, v, err, f.effects(t))
 if err != nil || v.Verdict != "runs" || f.effects(t) != 1 { t.Fatal("working-set-only authority rejected by all-table revalidation") }
}

```
