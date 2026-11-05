---
title: "Pack broker HTTP: bounded build hand-off"
date: 2026-11-05
status: accepted
stage: DECIDED
next: "Resolve [OQ-HTTP2](pack-brokers-http.md#OQ-HTTP2) before publishing a selected front on shared loopback; keep nonpublishing G4 work moving and custom execution fail-closed at OQ-HTTP1"
tags: [plan, broker, http]
summary: "Tree-grounded HTTP build hand-off; custom execution and shared-loopback publication remain open."
---

# Pack broker HTTP: bounded build hand-off

**Status:** 2026-11-06. Primary `d06a47707d87` integrates G3 core partially;
independent closure covers four findings only. The d06 matched bundle, host
`check --no-build`, and selective `just done` passed; `just done` is not whole-tree
CI. The rootful nested empty-packs smoke is for candidate `0dec2ce2f792`, not d06.
No primary selected-front launch/publication, full G3 acceptance, combined `check-ci`,
full integration, remote CI or native rootless/macOS proof is established. The
front-handler work can proceed without the custom executor ruling, but
[OQ-HTTP2](pack-brokers-http.md#OQ-HTTP2) gates shared-loopback URL publication;
[OQ-HTTP1](pack-brokers-http.md#OQ-HTTP1) gates only custom execution/signing. Neither
core checks nor this plan claim an OS sandbox for a host program. **Design:**
[HTTP contract](pack-brokers-http.md). The [broader plan](pack-brokers-plan.md)
remains a seed under [OQ-BX7](pack-brokers.md#OQ-BX7). Precedence: design wins on
behavior, the tree wins on cosmetic fact, this plan is advice and first to be wrong.
Stop without committing if a named symbol disappears, the test is not red for its
predicted reason, a file is dirty with another writer's work, a change escapes the
fence or an unrelated gate fails.

**Source prerequisite:** Reviewed F read/list and host-environment/XDG source and its scope-bind-failure cleanup repair are integrated. G1 preserves the independent MCP final-stop, confirmed-absence and harvest-failure checks; it does not weaken or replace them. F's read/list behavior does not prove cloud login. `ForAddRemember` and mixed-cell timed/Add stay disabled pending [OQ-BX7](pack-brokers.md#OQ-BX7) and safe persistence. No external credentials or account probes.

**G3 core integration checkpoint:** Primary `d06a47707d87` integrates reviewed
candidate `0dec2ce2f792`; the 17 nonoverlapping source paths match and
[`internal/callbroker/http.go`](../../internal/callbroker/http.go) preserves the primary's existing `allowed[key]`
canonical-header/staticcheck fix. The primary focused host test, five affected package
tests, four-package race tests, Linux/Darwin vet and staticcheck, format and matched build
passed. The clean-environment matched launcher reports `0.16.1+112.gd06a47707d87`;
the first `--version` inherited a `YOLO_VERSION` override and is not evidence. Host
`check --no-build` passed (22 checks, 3 warnings, 1 skipped); it did not launch a jail.
The selective `just done` passed. The independent
`G3_FOUR_FINDINGS_CLOSURE: OK` covers exact-schema, post-TLS-withdrawal, pinned
credential-parent and fresh-root findings only; it is not full G3 acceptance. The
candidate's rootful empty-packs smoke is evidence for `0dec…`, not d06; d06's
`check --no-build` was only host preflight. No combined `check-ci`, full integration,
native/rootless/macOS proof or external authentication is established. G4
selected-front/launch wiring remains absent from primary; an isolated front
adapter/dispatch component is partial, not a selected launch, published URL, ready
service or G4 acceptance. External host-state change after the final preflight remains
nonatomic with remote transmission. [OQ-HTTP1](pack-brokers-http.md#OQ-HTTP1) stays
open and custom execution remains blocked, but does not block G4.
[OQ-HTTP2](pack-brokers-http.md#OQ-HTTP2) separately gates agent-facing publication
on shared loopback; nonpublishing front work may proceed.

## Map

| Path | Change |
| :--- | :--- |
| `internal/loopholedecl/callbroker.go`, `callbroker_test.go`, `tomlform_test.go`; `internal/config/callbroker.go`, `scopeapproval.go`, `readabledeclarations.go`, config/gate tests; `internal/cli/run/launchinput.go`, `keeperplan.go`, `keeperspawn.go`, tests; `internal/callbrokerinput/launch.go` | G1: strict source-bound HTTP declaration, gate display/approved binding and selected private input; preserve CLI behavior (**integrated; focused findings closed; combined review remains**) |
| `internal/frameproto/frameproto.go`, `frameproto_test.go`; `internal/hostservice/hostservice.go`, tests; `internal/callbroker/classifier.go`, `http.go` (new), HTTP tests | G2: HTTP-only authenticated host-ingress preallocation bound and HTTP classifier/canonical bytes; leave generic CLI/Git/GH frame caps alone |
| `internal/callbroker/daemon.go`, `authority.go`, `handler.go`, `decision.go`, `http_handler.go`, `http_executor.go`, `http_native.go`, `http_write.go`, HTTP tests; `internal/brokerstore/store.go`, tests; `internal/brokeraudit/`, tests | G3: host HTTP decision, non-body pending metadata/act resubmission, exact spend/audit and pinned core executor (**partial core source integrated; four focused findings independently closed; custom signer/executor wire awaits [OQ-HTTP1](pack-brokers-http.md#OQ-HTTP1); G3 acceptance remains separate**) |
| `cmd/yolo-jaild/main.go`, `main_test.go`; `internal/entrypoint/requiredservice.go`; `internal/cli/run/launchinput.go`, `packservices.go`, `servedaddresses.go`, runtime tests; `internal/launchservice/reserve.go`; `internal/loopholedecl/tokens.go`; `internal/loopholes/runtime.go`; `internal/packload/served.go`, `credentialscope.go`; `internal/callbroker/httpfront.go` (new); `integration/packbroker_test.go` (new) | G4: selected in-jail front, client base-URL publication and real launch wiring, authenticated host identity, readiness and cross-jail caller boundary |
| `internal/callbroker/conformance.go` (new), `internal/callbroker/*_test.go`, `integration/packbroker_test.go` (new), `tools/broker-fixture/` (new), `integration/testdata/pack-broker-{cli,http}/` (new), `userguide/guides/pack-broker-demo.md` (new), overlapping reference docs | H: real CLI/HTTP conformance and host controls |

## Reuse and traps

- **Reuse:** `loopholedecl.Decode`/`LoadDir`, `parseCallBroker`, `config.validateBrokeredWorkspace`, `run.selectedCallBrokerLaunchInput`, `callbrokerinput.Launch` and `DeclarationHash` compose strict selected declaration → gate → private launch. `brokerstore.Store` and `approvalwait.Record` have distinct host versus published state; reuse exact filing/spend APIs, not a new approval RPC. `hostservice.handleOne` receives the host-asserted jail identity from a preamble. `callbroker.Classifier.invoke` enforces one JSON stdin, a 64 KiB answer and two seconds.
- **Trap:** selected defaults or `EnvFromHost` presence are not approved HTTP binding. Freeze the exact name/origin/header/source/prefix; recheck current selection, ceiling, grant and lease at dispatch. Neither jail nor classifier receives credentials. CLI `OriginalArgv` replay remains unchanged.
- **Trap:** bound only authenticated HTTP ingress before reading its body; generic `frameproto.ReadRequestBytes` allocates using a uint32 length and Git/GH/CLI must not inherit a new frame cap. A classifier-unit test cannot prove selected-front host effects.
- **Trap:** a disconnected fixture seeing zero requests cannot prove redirect protection; a first counted receiver must send a redirect to a second counted listener. Known-token redaction does not promise encoded or transformed-secret protection.

- **HTTP ingress:** `hostservice.handleOne` uses `frameproto.ReadRequestBytes`, which trusts a uint32 length before allocation. Bound the authenticated HTTP ingress **before** allocating; do not impose a new cap on other Git/GH/CLI request readers. `callbroker.Classifier.invoke` currently marshals one CLI JSON input, checks 64 KiB, then reads a 64 KiB answer; HTTP requires a separate 4,456,448-byte JSON input budget while keeping one JSON stdin, 64 KiB answer and 2 s deadline. An unbound classifier-only test does not establish HTTP execution.
- **Binding trap:** `loopholedecl.parseCallBroker` currently permits `front=http` but lacks an `http` declaration; its lookup fields are only method/path. `callbrokerinput.Launch` and `run.selectedCallBrokerLaunchInput` omit HTTP binding; `callbroker.Main` constructs only `newCLIHandler`, `ApprovalAuthority` requires CLI, and `cmd/yolo-jaild/main.go` has no HTTP front. The fixture/G tests must go through these production call sites. F's host-env source repair is CLI-scoped: HTTP `host_env` needs the **exact approved variable name** plus origin/source/header/prefix gate binding, not merely `EnvFromHost` presence. `fenced_file` is inside approved credential paths with current host-user owner, nonsymlink regular 0600, <=8 KiB and no empty/control value.
- **Custody trap:** `brokerstore.Request.OriginalArgv` and `ApprovalAuthority` replay the CLI classifier from stored argv/digest; HTTP must persist only nonsecret intent/digests/cells/native IDs/metadata, never original/canonical body or a replayable request, and reclassify the resubmitted original body at act. Preserve CLI storage and digest semantics. Native parents need fresh exact-read lookups, not stored chains or a fixture oracle; read standing is tested on **every** lookup and at act.

## Common gates and hand-off protocol

One commit per ordered G1 → G2 → G3 → G4 → H task, after the source prerequisite. On a clean tree write the consumer regression first and run its exact red command; missing symbols alone are not behavioral red. After green run task package gates and `just format`. For `internal/`/`cmd/` changes run `just build-dev-bundle` and its emitted absolute launcher from a throwaway workspace. The landing owner runs combined `just check-ci`, relevant integration, and one full slow suite; a nested rootful jail cannot prove rootless reachability or macOS behavior. If a test reads outside its package run `python3 scripts/completion-census.py` and commit its output. Report red/green commands, counted host effects, audit/spend and unsupported backends. Stop on unexpected drift; do not diagnose an unapproved behavior into code.

## Tasks

### G — HTTP front and core executor (after reviewed F source integration; G1 → G2 → G3 → G4)

**Scoped contract, do not re-open:** The [HTTP design](pack-brokers-http.md) controls settled behavior; [OQ-HTTP1](pack-brokers-http.md#OQ-HTTP1) gates only the custom HTTP executor. HTTP is not an elevated route around the selected pack, current ceiling, exact grant, native ancestry or audit. For all G tasks: begin on a clean integrated source tree with reviewed F, verify named symbols still exist and stop/report on a noncosmetic drift, unexpected red result, dirty file, fence escape or unrelated gate failure. Each task is one commit, ordered because the declaration, transport, host decision and real front share `callbroker`/launch input. Keep Git/GH and CLI limits and CLI `OriginalArgv` replay unchanged. Report red and green consumer results, effects and residuals, not only helper tests.

**G1 — declaration and approved binding** (first G commit).
**Tests first — compilable, consumer-path behavioral red:** add this complete test to `internal/loopholedecl/callbroker_test.go` (package `loopholedecl` already imports `strings` and `testing` and defines `callBrokerFixture` and `callBrokerManifest`). The declared block must load; the unknown nested key must refuse. This tests existing `Decode`, not a new type that only fails compilation. Preserve it alongside the gate test below.

```go
func TestCallBrokerHTTPDeclaration(t *testing.T) {
    valid := strings.Replace(callBrokerFixture,
        `"source":"local-tasks"`,
        `"source":"local-tasks","http":{"origin":"https://fixture.example","credential":{"source":"host_env","key":"TASKS_TOKEN","header":"Authorization","prefix":"Bearer "},"request_headers":["Content-Type"]}`,
        1)
    if _, err := callBrokerManifest(valid); err != nil {
        t.Fatalf("selected HTTP declaration must load: %v", err)
    }
    unknown := strings.Replace(valid, `"request_headers":["Content-Type"]`,
        `"request_headers":["Content-Type"],"unapproved_header":"X-Token"`, 1)
    if _, err := callBrokerManifest(unknown); err == nil || !strings.Contains(err.Error(), "unknown key") {
        t.Fatalf("unknown nested HTTP key: %v; want refusal", err)
    }
}
```

**Red:** `go test -short ./internal/loopholedecl -run '^TestCallBrokerHTTPDeclaration$' -count=1`: `callBrokerKeys` currently lacks `http`, so the valid declaration is rejected. This assertion must become green only after the actual manifest decoder accepts and validates the declared binding. **Additional consumer regression:** the selected TOML declaration, config gate and `run.selectedCallBrokerLaunchInput` must bind the exact variable name, origin, source/header/prefix and declared POST `/graphql`; do not accept `EnvFromHost` alone as that evidence.

- **Further cases:** extend `internal/loopholedecl/tomlform_test.go` with a selected TOML fixture declaring `front = "http"`, `[call_broker.http]` origin/credential/request headers and POST `/graphql`; via actual manifest decoder and `run.selectedCallBrokerLaunchInput` + config gate assert exact host-env variable, credential source/header/prefix, origin, declared POST path and selected pack appear in frozen gate input. Missing approval, duplicate/unknown nested key, userinfo/path/fragment/wildcard/non-HTTPS origin, invalid header or non-absolute fenced-file path refuse. Red: `go test -short ./internal/loopholedecl ./internal/config ./internal/cli/run -run 'TestCallBrokerHTTPDeclaration|TestCallBrokerHTTPApprovedBinding' -count=1` (add both consumer tests first; current declaration has no HTTP binding).
- **Change:** strict parse/schema validation in `internal/loopholedecl/callbroker.go`; existing selected-source gate in `internal/config/brokered.go` and run's scope/gate path, `internal/cli/run/launchinput.go` and `internal/callbrokerinput/launch.go`. Use the selected private producer/declaration hash; check the approved binding on each dispatch, not raw `EnvFromHost` or mutable jail/workspace settings. Bind `host_env` name to approved declaration (value host-only); validate fenced file as [HTTP binding](pack-brokers-http.md#http-binding-and-executor) at use. **Fence:** no new credential channel, host grant, global header/env policy or CLI change. **Gate:** red above, green same packages. **Commit:** `feat(broker): bind declared HTTP origin and credential at the gate`.

**G2 — bounded transport and typed HTTP classification** (after G1).
- **Tests first:** in `internal/hostservice`, drive the authenticated HTTP broker ingress with four prefix bytes `ff ff ff ff` and no body: immediate refusal before any body read/allocation or handler effect. In `internal/frameproto`, preserve an existing generic reader regression instead of asserting a global cap: CLI/Git/GH retain their prior behavior. In `internal/callbroker/http_test.go`, one original 3 MiB body traverses the classifier (not old CLI 64 KiB), a 3 MiB + 1 byte body and a 4,456,449-byte host JSON frame refuse before effects; CLI JSON input/answer remain 64 KiB and 2 s. Test original-body reference endorsed digest/presence, inline base64 rewrite bounded by 64 KiB answer, duplicate fields, absent vs empty body, original/canonical length-delimited digest mismatch, raw path+query and header aggregates >16 KiB, >64 headers/name >256. Red: `go test -short ./internal/frameproto ./internal/hostservice ./internal/callbroker -run 'TestHTTPHostIngressRejectsDeclaredLengthBeforeAllocation|TestHTTPClassifierBodyBudgetsAndCanonicalDigest' -count=1` (write a consumer test that reaches the actual ingress; a direct `ReadRequestBytes` cutoff would break unrelated transports).
- **Change:** HTTP-only bounded authenticated host read on `hostservice`'s `ReadRequestBytes` call path (not a global arbitrary cap); HTTP protocol/classifier in `internal/callbroker/http.go` (new) composing `classifier.go`'s staged identity/deadline/answer validation, not substituting an unbound classifier protocol for a host path. JSON classifier stdin is one value, with its own 4,456,448 budget; canonical bytes are what eventual executor receives. **Fence:** no generic transport frame cutoff, unbounded streaming or body side channel. **Gate:** red above, green affected packages plus CLI classifier regression. **Commit:** `feat(broker): bound HTTP ingress and canonical classification`.

**G3 — fresh facts, approvals and pinned core HTTP transport** (after G2).
- **Literal host-path fixtures for tests first:** selected POST path `/graphql`, original body `{"operationName":"Rename","query":"mutation Rename { renameWorkItem(id: \"a1\", title: \"ready\") { id } }"}` and changed-body retry with title `"changed"`; second project `beta`/item `b1` is outside approved `alpha`/`a1`. Use a declared GET lookup `/items/a1` returning `{"id":"a1","project":{"id":"alpha"}}` and JSON Pointers `/id` and `/project/id`; a returned `"b1"` for queried `"a1"` must refuse before dispatch. Count `requests` on the pinned TLS receiver, `writes` on its state log, and `requests` on an independently listening redirect target; denial/evaluate/canceled/stale read/body/parent each yield zero new writes, Allow once yields exactly one write and one spend, redirect yields exactly one first-server request and zero second-server requests. The body literals are fixture semantics, not an invented broker-wire JSON schema.
- **Tests first:** `internal/callbroker/http_test.go` through the host handler, actual approval store and counted `httptest.NewTLSServer` receiver: GET standing read is permitted; POST `/graphql` is read only when **both original and canonical** method/path are POST at the declared gate-shown path; PUT/DELETE read fail. A typed native-id/parent lookup via declared HTTP template must classify the instantiated **original and canonical** request as identical exact standing read before dispatch, select RFC 6901 scalar strings, verify returned ID equals queried ID, and fail on unknown/missing/conflicting/cyclic parent or revoked read standing after approved apply. Exercise up to three native-fact lookup rounds and refuse a fourth; verify every instantiated lookup gets its own exact standing-read check and that withdrawn read standing before dispatch yields no target request. Test alpha/beta multiwrite one request/no partial effect, denied/ASK/evaluate/canceled pre-effect 0, Allow once 1 effect/1 spend, changed body/presence/ancestry/binding or selection on act 0 effect, fresh reclassification on **resubmitted original body**. Counted second listener sees zero requests after 3xx; 32 MiB response bound, URL/query/Location and chunk-split exact token redaction, stripped caller Authorization/Set-Cookie are observed through the real host path. Red: `go test -short ./internal/callbroker ./internal/brokerstore -run 'TestPackHTTPExactReadLookupAndWithdrawal|TestPackHTTPApprovedActRechecksOriginalAndNativeChain|TestPackHTTPGraphQLMultiwriteIsAtomic|TestPackHTTPRedirectNeverLeaksCredential' -count=1`.
- **Change:** host daemon HTTP branch in `internal/callbroker/daemon.go` and handler/authority/decision with `brokerstore`'s exact file/spend APIs and required audit; HTTP request record contains nonsecret metadata and hashes, never original/canonical bytes or payload audit. Resolve host credential only after real decision (file owner/mode/size at use); pin HTTPS DNS origin, no client authority, zero redirect, cookie or jar; strip credential/Set-Cookie fields, redact exact known token across response chunks/headers and URL/Location (no encoded-secret promise). Count receiver and second-listener requests. Distinguish proven before-send from possibly-sent failures; never auto-retry/refund, tell caller to inspect host state. **Fence:** no `ForAddRemember`/timed/Add multiwrite while [OQ-BX7](pack-brokers.md#OQ-BX7) is open; no original HTTP body archive or new approval RPC. **Gate:** red above, green `go test -short ./internal/callbroker ./internal/brokerstore ./internal/brokeraudit`. **Commit:** `feat(broker): execute approved HTTP calls with fresh host facts`.

**Custom HTTP executor — blocked on [OQ-HTTP1](pack-brokers-http.md#OQ-HTTP1).** The parent design's pack-executor scope remains intended, not removed, but its HTTP transport authority is not settled. Do not implement a signer or full HTTP pack executor from either proposed option. After the owner rules the question and the design records it, add the chosen wire and its custody, transport, audit and verification gates here. Core-owned G3 and G4 work may proceed without it; neither core transport checks nor this plan claim an OS sandbox for a host program.

### G4 — selected front, launch and publication (after G3; handler work can proceed before [OQ-HTTP2](pack-brokers-http.md#OQ-HTTP2))

**Publication gate:** [OQ-HTTP2](pack-brokers-http.md#OQ-HTTP2) must be ruled before an
agent-facing HTTP URL is published on a shared loopback. Do not infer local caller
authentication from the host TLS preamble: it carries the front's `FrontJailID`, not the
identity of the jail that dialed its local URL. [OQ-HTTP1](pack-brokers-http.md#OQ-HTTP1)
still gates only custom executor/signing and does not block this core-front work.

- **Pack-to-client association:** use the selected pack's existing `kind = "env"`
  contribution, `served_by` naming the same selected front, and `http://{listen}` to set
  that client's existing base-URL variable. The producer must gate only selected, enabled
  and approved HTTP fronts; `loopholes.Set.JailDaemons` appends extra specs without applying
  that selection gate. The host TLS endpoint file is private host transport material, never
  the client-facing URL. Add no manifest field or global client environment name.
- **One usable address through launch and attach:** the core producer supplies the actual
  nonzero loopback `JailDaemonSpec.Listen` used both in daemon argv and the resolved
  `{listen}` environment pointer. Hold a unique address keyed by selected front name for
  private and shared network namespaces; attach adopts the running jail's name/address map,
  never a new port. Publish the selected client variable and ready name only after the local
  listener is bound, selected host endpoint/generation is verified, and required readiness
  passes. On allocation, bind, readiness or stale-generation failure, withhold the URL and
  fail the affected selected front with a next step; never fall back to a declared port or
  leave stale publication.
- **Current-source trap:** `ListenAddressProblem` rejects port `0`;
  `settleServedAddresses` reserves declared host:port entries only on shared namespaces and
  warns/falls back to declared addresses on reservation failure. Existing address state keys
  declared endpoints, not dynamic front names. Do not encode `127.0.0.1:0` in pack schema or
  treat the current relocation/attach channel as this allocator. Add only the core name-keyed
  held-address and attach/readiness path the contract requires; do not add a privileged pack
  daemon, binary or host grant. Local token delivery is separate engineering below; this
  plan does not authorize an actual origin credential, CLI TLS bearer, or generic host
  credential channel.
- **Local caller token and custody:** the owner prefers trying the selected client's existing
  ordinary configurable token/API-key field with a fresh jail-scoped replacement token, not
  a new special client header. A's front consumes and strips that local token before host
  dispatch; the host independently injects the actual origin credential. This is a preference,
  not an accepted wire, custody mechanism, compatibility guarantee, client-eligibility rule,
  or backend restriction. Never use an actual vendor credential or the CLI transport bearer
  as the local token.

  Do not reuse current `{caller_token}` delivery as proof of cross-jail secrecy. The selected
  value is recorded in the per-entry channel and rendered into selected-agent environment
  files and session snapshots under `paths.WorkspaceHomeState`; the workspace is also exposed
  to each jail. See [`callertokens.go`](../../internal/cli/run/callertokens.go),
  [`userenv.go`](../../internal/cli/run/userenv.go),
  [`agentenvfiles.go`](../../internal/cli/run/agentenvfiles.go),
  [`sessionsnapshot.go`](../../internal/cli/run/sessionsnapshot.go), and the workspace/home
  binds in [`assemble_parts.go`](../../internal/cli/run/assemble_parts.go). A same-owner
  peer jail sharing the workspace can read those aliases; `0600`, a read-only home overlay,
  and no-follow/root-walk protections do not establish peer secrecy. This is a source-based
  finding, not a reproduced two-jail runtime result. Engineer private delivery and protect the
  token-bearing consumer startup chain, including workspace-backed launcher/environment
  inputs. This is the next engineering action, not a blanket owner-approval hold. If it
  requires a changed shared-runtime trust boundary, return that concrete choice to the owner.
- **Consumer regression after a selected front is callable:** in
  `integration/packbroker_test.go`, use the actual selected client environment and two
  independently selected fixture jails A and B that share loopback and the same workspace.
  A has an approved write and succeeds once through its normal token field. B must attempt to
  acquire A's actual token through every published workspace alias and session snapshot, then
  replay any acquired value through the same client field; also attempt to tamper with the
  token-bearing consumer startup chain. The current-path red must show acquisition and replay
  reaching A's host handler; connection refusal or only absent/foreign-token rejection is not
  evidence. After private delivery and consumer integrity pass, B must neither acquire nor
  replay A's capability, enter A's host handler, cause a receiver request, or spend. Count
  A's handler entries, receiver effects and spend. A host-origin credential is independently
  injected by the host and never becomes the client's local token. For a concrete client path
  that cannot meet these conditions, record the specific compatibility/isolation boundary
  and return it to the owner; do not pre-emptively exclude a client or backend. This is not a
  helper-only test or a measured native-Mac/rootless claim. Red:
  `go test -run '^TestPackBrokerHTTPFrontCannotBeBorrowedByAnotherJail$' ./integration`.

- **Literal front-path assertions:** send a selected fixture client request `POST /graphql` with `Authorization: jail-forged` and an original body from G3; for ASK assert `403`, `Yolo-Verdict: asks`, an opaque `Yolo-Request`, a GraphQL `errors` array carrying the actionable menu, and zero receiver requests. Repeat with no selected pack, invalid host preamble and missing grant; each refuses before the counted receiver. A selected approved call instead reaches it once with the host-built header, never `jail-forged`. Do not mistake a disconnected test listener for successful refusal routing.
- **Tests first:** selected fixture HTTP pack's actual client path reaches `yolo-jaild` front → authenticated loopback-TLS → bound host handler → counted TLS receiver; absent selection, invalid preamble, unbound launch, missing grant, ASK and denied responses cause zero receiver requests. Assert HTTP 403 `Yolo-Verdict`/`Yolo-Request` + GraphQL `errors` menu; client Authorization is stripped and disclosed. Red: `go test -short ./cmd/yolo-jaild ./internal/entrypoint ./internal/cli/run ./internal/callbroker -run 'TestSelectedPackHTTPFrontBindsHostIdentity|TestPackHTTPFrontRefusalShape' -count=1` (production dispatch, not helper-only).
- **Change:** `cmd/yolo-jaild/main.go`, `internal/entrypoint/requiredservice.go`, run's
  existing `YOLO_JAIL_DAEMONS`/selected launch producer and endpoint path in
  `internal/cli/run/packservices.go` and `servedaddresses.go`,
  `internal/launchservice/reserve.go`, `internal/loopholedecl/tokens.go`,
  `internal/loopholes/runtime.go`, `internal/packload/served.go` and
  `credentialscope.go`, and `internal/callbroker/httpfront.go` (new). The existing
  preamble supplies front-to-host jail identity, not local caller identity; the front holds
  no host authority. **Fence:** no host credential, new global env/config, production TLS
  trust override or pack-owned privileged daemon. **Gate:** red consumer tests above, green
  affected packages, `just build-dev-bundle` + absolute emitted launcher in a throwaway
  workspace; rootful nested tests do not prove native Mac/rootless isolation. **Commit:**
  `feat(broker): connect selected HTTP front to host decision`.

### H — conformance, nested-jail proof and operator guide (after real G4 and C/F fixture integration)

- **Settled, do not re-open:** independent CLI/HTTP fixture packs use actual selected fronts, loopback-TLS, classifier, grant/store, `yolo approve`, `yolo wait` and audit; host-operated fixture state is synthetic. H does not license CircleCI writes whose real grammar is unverified; F's narrow read-only behavior does not establish HTTP or real cloud authentication. Timed/Add/Remember remains off pending BX7.
- **Tests first:** `integration/packbroker_test.go` `TestPackBrokerCLIEndToEnd`/`TestPackBrokerHTTPEndToEnd`/`TestPackBrokerConformance` under `requireJail`: real source-built jail, selected independently loadable packs, host `start/seed/inspect/change/reset/stop` and counted local TLS service. GraphQL named operation/aliases/read POST/multiple writes; predict, pre-ask, deny note, Allow once then act; zero side effects for disabled/no grant/ask/canceled/beta/stale body/native parent and exactly one alpha write/use after approved act; redirects to a second listener zero requests and header/body chunk redaction with synthetic token. Assert request ID, current read withdrawal, audit and state deltas, not merely 403 or unbound-server counts. A deterministic classifier run twice and real read-lookup classification belong to conformance. Red: `go test -run 'TestPackBroker(CLIEndToEnd|HTTPEndToEnd|Conformance)' ./integration` after tests exist, before G4 wiring; report a real call-path failure, not a missing test.
- **Change:** conformance command in `internal/callbroker/conformance.go` (new) plus CLI dispatch if needed for pack-author invocation; `integration/packbroker_test.go` (new), independent fixtures under `integration/testdata/pack-broker-{cli,http}/`, host controls under `tools/broker-fixture/`, `userguide/guides/pack-broker-demo.md` (new) with verified start/seed/inspect/change/reset/stop, direct vs brokered commands and next steps. Recheck overlapping `docs/reference/loophole-{grants,transport}.md`, `docs/reference/pack-system.md`, `userguide/reference/settings-per-setup.md`, and CLI/check/readout help against shipped behavior (not blanket rewrites); update generated matrix only if schema demands it. **Fence:** no external login, real token, CircleCI write-grammar invention, arbitrary host effects or agent interaction. **Gate:** targeted nested test, affected packages, `just build-dev-bundle` + absolute launcher, Vantage on changed docs; census when a new test reads outside its package. **Commit:** `test(broker): prove selected HTTP and CLI flows in nested jails`. **Report:** actual host state/effect counters and unsupported native backend cases.
- **Landing owner, not each worker:** after related F/G/H integration and independent review, `just check-ci`, final combined completion census, one full `go test -count=1 -timeout 0 ./integration`, then `just done` on clean committed tree. Native rootless/macOS reachability and actual external authentication remain unproved by nested rootful runs; report `podman info --format '{{.Host.Security.Rootless}}'` with any rootless claim. No approval-menu conformance for timed/Add until BX7 and per-resource all-or-none persistence are ruled and implemented.

## Ships with

- Synthetic host start/seed/inspect/change/reset/stop and selected-pack demo guide only after verified commands exist. Recheck [loophole grants](../reference/loophole-grants.md), [transport](../reference/loophole-transport.md), [pack system](../reference/pack-system.md), [setup matrix](../../userguide/reference/settings-per-setup.md) and `yolo check`/CLI refusal next steps against shipped behavior. No changelog entry.
- Fixture and nested proof do not establish cloud credentials, native rootless network, Apple Container or macOS account enforcement; report limits beside local green results.
