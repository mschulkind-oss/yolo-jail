---
title: "Shared broker policy revision — observed contracts and design grounding"
status: accepted
tags: [research, brokers, policy, approvals, scopes]
summary: "Read-only evidence packet distinguishing existing machinery, parity gaps, owner requirements and recommended architecture."
---

# Research: Shared broker and loophole policy

## Summary and evidence boundary

Yolo manages the selected coding environment and its permissions; confinement is one setting of that environment. The revision should extend its existing host-owned approval authority, not introduce an AWS approval system. Four shipped sources actually classify operations: **git, github, aws and circleci**. Generic pack brokers already reuse the durable store, exact approvals and timed resource answers, but scoped reads and notification/shutdown parity are not complete.

**Source-read grounding, checked 2026-11-17**, against supplied baseline `a97a0e055f20de4524063b70729595ea8794bef8`. HEAD and cleanliness were supplied for the source-read round, not independently verified by that reader. Original source and manifest reads were supplemented by a read-only inventory. **No tests, builds, API calls, agents, installers, config commands or credential-store reads support this packet.** Test descriptions are inspected assertions, not fresh passing receipts.

**Reads with:** [Scoped broker policy](../design/scoped-broker-policy.md) (the proposed contract; this grounding does not grant build authority).

Evidence notation: **Observed** means source or manifest read; **Interpretation** means consequences of those explicit branches; **Proposal** means architecture for the new requirement. Code anchors are repository-relative filenames and symbols; selected line ranges were observed in source inventory. No web research was necessary for this repository-contract inventory.

## 1. Requirements versus the current system

**Owner-settled, not questions to reopen:** unconfigured access stays denied; pack selection alone grants no execution authority. Applicable brokers use consistent allow/ask/deny, standing permission, scoped read/write, notification, bounded wait, timeout and persistent-request lifecycle. Scope syntax must fit each source's resources. CE can be always-read while S3 generally asks, with bucket/prefix read-only or read/write exceptions. Host-controlled S3 routing selects profiles; the agent cannot pick arbitrary host profiles. Ordinary supported `aws` commands hit mediation. `aws-auth` remains the independent, unchanged credential service; directly delivered credentials can bypass mediation if IAM permits. No live grants, credentials or IAM are changed automatically.

**Compatibility requirement:** universal scoped-read support supersedes the earlier universal WRITE-only restriction. It does **not** mean turning existing broad grants or shipped defaults into ask-every-time policy. Existing explicitly granted broad reads, branch rights and working-set behavior need named compatibility treatment.

**Observed current layers:**

1. Selection/declaration and credential fences.
2. Host ceiling plus workspace request and approval gate.
3. Admitted roots.
4. Source classification.
5. Standing/resource/working-set/temporary decisions.
6. Durable request and approval transaction.
7. Canonical host dispatch and audit.

A root is an admission boundary, not a permission, and a request is not a grant.

## 2. Exhaustive shipped operation-source inventory

Membership was established by a shipped-manifest scan, then each owning manifest was read. Only git/github declare `[brokered]`; only aws/circleci declare `[call_broker]`. Synthetic packs under `integration/testdata/pack-broker-{cli,http}` are conformance fixtures, not shipped service sources.

| Source | Actual intercepted path and admission | Read behavior | Write/approval behavior |
| --- | --- | --- | --- |
| **git** | `packs/git/loopholes/git-broker/loophole.toml`; remote rewriting invokes `git-remote-yolo` / `yolo git-remote`; exact approved SSH/HTTPS URLs, any remote host. Core implementation `internal/gitbroker`. | Host-credential fetch requires scope/read authority. Public HTTPS fetch first tries anonymously even without approved scope/read; this is a deliberate compatibility exception, not credential authority. | Whole push is inspected as old/new/ref commands plus push options. New branch grants, declared branch/tag grants, recorded working set and timed entries cover individual refs. Every ref must be covered; otherwise one exact whole-push request. |
| **github** | [`GitHub manifest`](../../packs/github/loopholes/github-broker/loophole.toml); `gh` interception invokes `yolo gh`; approved github.com repositories from remotes/named repos. `internal/ghbroker`. | Reads standing after broad user read grant and approved repository admission. Unknown grammar, credential/file escapes and account-wide operations are refused, not requestable. | Creation/runs/approve-run grants and working-set comment/edit coverage run; other supported writes ask. Approve and merge are separate. Exact once or timed resource answers; no current permanent Add UI. |
| **aws** | `packs/aws/pack.toml`: `bin = "aws"`, `forward = ["yolo", "internal", "call-broker-forward", "aws-broker", "--"]`; classifier dispatch `aws`. Declared service roots. | S3 listings/download/head and four CE queries are standing after shared read authority and their own roots. No per-operation read approvals. | S3 `put_objects` and `delete_objects` use generic write policy, exact stdin-bound once, timed whole-chain answers. CE has no write cell. |
| **circleci** | [`CircleCI manifest`](../../packs/circleci/loopholes/circleci-broker/loophole.toml); generic CLI front; remotes map github.com OWNER/REPO to `gh/{owner}/{repo}`. | Only explicit `run list --project gh/ORG/REPO` with bounded listing flags, exact root. | Manifest declares `rerun_workflows`, both tables default false, but classifier refuses all writes, API passthrough and other verbs. Declaration is not implemented operation support. |

**Exact default wording:** git manifest: “No switch, and no access by default”; github description: “with none, every call is refused”. AWS/CircleCI each declare `kind = "read"` and `requestable_by_default = true`: **requestable is not granted**. `config.ResolveLoopholeGrants` distinguishes candidate/requestable/not_allowed; only the gate record grants a candidate. `always` still passes the workspace approval gate. Explicit user `on_request` replaces requestable-by-default fallback.

Other shipped loopholes must not be miscounted as classified operation brokers: **aws-auth** mints/delegates SDK credentials; **mcp-auth-broker** shares declared MCP sign-ins; **claude-oauth-broker/openai-auth-broker** serialize OAuth refresh. **journal/macos-log** are live grant-gated log bridges (`read`, `read_all`); **host-processes** has valued `view` names; **serial** has valued `devices` globs and list/open transport; **audio** gates direct host socket/device crossing with `use`; **cgroup-delegate** only narrows its jail's own cgroup and needs no authority grant; **hello-daemon** is a jail executable-delivery experiment. **wire-bridge** is a pack service for API protocol adaptation, not a loophole call classifier. These share applicable declaration/fence/grant/lifecycle contracts, but currently have no per-operation brokerstore approvals. Making every serial byte/audio action an approval is not implied by this revision. A designer should explicitly classify which capability services need policy extension versus only shared terminology/introspection.

## 3. Generic schema and policy precedence already built

**Observed schema:** `internal/loopholedecl/callbroker.go`, `CallBroker`, strictly accepts source/front/program/classifier/executor/credential_paths/env_from_host/scope/resource/cell/lookup/read_over_post/joins_working_set/http. Resources are `{Type, Parent}`. Scope is `declared` or `remotes` with remote_host/root_template. Cells carry `Name`, `Tables`, boolean per-table `Default`, and `MaxFor`. Tables are only `working_set` and `all`. `parseCallBrokerCells` explicitly refuses cell name `read`; there is no resource-scoped read cell or host-profile route field.

The core executable classifier form is restricted to exactly `yolo internal call-broker-classify <name>`; external classifiers bind staged `{program:<bin>}` identities and hashes. Duplicate selected source owners are refused (`config.callBrokerInfoBySource`); CLI approval discovery only attaches native git/github authority to the matching **official** pack identity, not an arbitrary pack reusing its name (`cli.approvalStores`). Unknown/removed/incompletely discovered sources are inventory/deny-only.

`callbroker.Classification` is currently:

```go
Argv []string
StdinSHA256 string
Reads [][]Resource
Writes []Write // Cell, Chain, Description
```

Reads have **no cell, operation identifier, description or query-policy identity**. `CLIInput` exposes version/front/argv/stdin SHA/root strings/lookup facts; never host environment, credentials or stdin bytes. `validateChain` requires declared types, no repeated type, exact declared parent links, and a parentless final ID equal verbatim to an approved root. This is robust admission validation to reuse, not proof that arbitrary classifier IDs denote true provider ancestry.

`config.EffectiveCallBrokerCells` snapshots gate-approved workspace requests and ceiling per table/cell. `CallBrokerTablePolicy` holds request `absent|true|false|ask`, ceiling `off|on_request|always`, decision `run|ask|refuse`. Explicit false refuses; ask overrides always; true runs only within ceiling; absent runs only with always. `all` ceiling tokens cover working_set. Invariant is working_set ≥ all after defaults and authored overrides. Out-of-ceiling true is warned/skipped, not granted. Runtime `decideCall` selects working_set when any non-call working entry matches a resource/ancestor; otherwise all. One refused write refuses whole call; any uncovered write asks once for the whole call. Timed answers do not override false.

**Important nuance:** pure decision can predict ask under an off ceiling, but bound revalidation refuses filing/execution unless frozen/current write ceiling admits it. Thus “absence asks” is not authority to file approvable requests for every undeclared privilege. Preserve the owner no-access floor while specifying how host-admissible asks differ from hard denies.

**Observed read deficiency:** `decideCall` returns `runs`, rule `standing read`, whenever there are reads and no writes. Every bound call first requires current broad read allowance/grant. `callRequest` records only writes; `revalidate`, `RequestEntries`, `ApprovalTimedLimit` all require classified writes. A read cannot become an exact/timed pending request merely by changing that one branch. Mixed calls' reads also have no separate decision beyond admission/read standing.

**Current repository constraint:** `config/readablenamed.go`, `scopedCeilingReadProblem` (around 290), refuses read in either scoped ceiling list: “a repository's ceiling lists write capabilities only; reading is for every workspace: yolo allow … always …”. `readableScopedCeiling` also rejects generic callbrokers and any-host git sources as repository ceiling owners. These are real high-level constraints, not just prose. Universal scoped reads must revise these paths and their caller/tests explicitly, while preserving broad grants.

## 4. Root, identity, admission and withdrawal machinery

`config.ReadWorkspaceForGate` strictly reads workspace config once, with writer provenance, and rejects requests/repos/canonical broker entries written outside the workspace. `WithCallBrokerDeclarations` adds selected declaration disclosures and roots to the same persisted gate. Public TOML `[call_roots] aws=[...]` lowers to canonical `brokered.aws.roots`; `WorkspaceRead.CallRoots` retains declaring filenames. AWS authority reference documents filename-bound approval and safe refusal of root-bearing format migration; this is not permission to rewrite approval provenance.

`brokerscope.File` retains source, random launch ID, launching PID, container/workspace, form, exact roots/repos, grants, broker start/PID, live-scope flag, selected call-input locator and pinned CLI path/hash. Scope files are 0600 in 0700 directories, atomic renames. `FormCallRoot` compares root IDs verbatim; Git exact URLs and GitHub normalized repository identities remain source-specific. `ReplaceScopeContext` serializes scope/grant replacement under the same source lock as decisions, retaining launch identity. Gate-approved roots can change live; raw workspace edits alone are not authority.

`callbrokerinput.Launch` version `yolo.call-broker-launch/2` freezes selected pack/loophole/source/declaration hash, classifier/executor identities, roots/resources/cells, EffectiveCells, host **environment names**, read-standing metadata, grant-file locator and authenticated front jail ID. Handler accepts only private regular 0600 input via O_NOFOLLOW, checks opened identity and exact unchanged bytes.

`currentAuthority` rechecks live lease/scope, exact CLI pin, fresh **user** selected declaration/ceiling, declaration hash/front/program/resources/cells/lookups/env names, unique owner, and this launch's grant snapshot identity. It does not reload mutable workspace config. Withdrawal diagnostics distinguish roots, grant and host read ceiling. `revalidate` replays original argv/stdin hash through current classifier, verifies canonical argv and ordered writes/chains/digest, live roots and frozen/current ceilings. Approval frontend derives scope path from validated launch ID, not agent-supplied path.

**Compatibility investigation:** generic write revalidation checks the `all` table for each write even when pure decision selected working_set; this may constrain working_set-only ceilings. This is observed branch structure, not a verified failing user scenario. Also EffectiveCells is immutable in original launch input while current roots/grant files are live: planner must trace cell-edit `config apply` behavior and state whether relaunch is required; do not assume new table values hot-reload.

## 5. Shared approval lifecycle and concurrency

Reuse `brokerstore`, not another pending directory. `Request` stores host launch/jail/start/workspace, canonical and original argv, repository/set/stdin/digest, ordered `CallWrites`, policy, timestamps/state, human About, hook failures/run state, answer/via/until/spent/lapsed, denial note. HTTP stores original/canonical/declaration hashes, **not original body**. `Grant.Workspace` is the holder; jail/start are history. Exact grants are one-use, normally 15m. Pending life is 1h. Three pending requests per jail and 10m denied/expired same-command cooldown are store-enforced (`limits.go`); dedup joins pending by jail/lifetime/set/digest.

Exact digest (`store.go:654`) hashes canonical argv, repo, stdin digest; generic CLI additionally binds ordered CallWrites and OriginalArgv; HTTP additionally binds intent/canonical/declaration hashes and writes. It does **not** bind any future host route/profile identity, or read resources. Legacy git/GitHub digest compatibility is deliberately preserved.

`FileIfStillAsksWithCoverageContext` revalidates policy and late standing/timed/exact coverage under one source transaction. `SpendExactOrFilePreparedContext` checks parent/invocation authority, preflights, durably spends then dispatches at most once, or atomically files/joins. No refund after possible transmission. `boundWrite` recalculates coverage under lock and dispatches there; a revoke landing first blocks, landing after dispatch waits. Store callbacks must not recursively acquire its lock. Timed pack answers use one `call:<cell>` entry per **full chain**, capped by smallest touched cell `max_for`, all-or-none through `DecideWorking`. Default cap 15m; AWS put 1h, delete 15m. Timed calls can reuse different bodies on the same cell/full chain; once cannot.

`yolo approve` is host-only; answers once/--for/deny, optional denial note ≤500 runes, revoke; --add/--remember are refused but historical entries remain. `cli/approvesources.go` discovers selected generic adapters and attaches revalidation/timed/audit hooks. Normal Store.End closes pending from its jail/start as refused but preserves workspace grants.

`brokerstore.publish` writes `.yolo/approvals` wake hints; publication failure is non-authoritative. `approvalwait.Wait` holds one waiter flock per request, polls authoritative-looking **hint state only for wake advice**, defaults to request expiry, returns allowed 0, pending timeout 75, denied 77, expired 78, closed 79, used/lapsed 72. Actual retried operation reauthorizes at host. UI/help must never treat these agent-writable records as grants.

**Observed parity gaps:** generic `callbroker.Main` has its own daemon loop and never calls `brokercore.Daemon.Main`/`StartOnRequest`; inspected production caller scan likewise has no generic hook runner. Git/GitHub Main use brokercore, whose Main calls StartOnRequest. Generic Main sets PublishRecords/Audit but does not install PublishFailed reporting or call Store.End in its own teardown. It closes lease, so stale approvals fail, but immediate closed-pending wake behavior is not implemented there. Audit-only failure/publication behavior needs explicit parity tests. This is a concrete missing integration path, not proof every conceivable launcher cleanup path is absent.

Git pushes wait up to 30s for an answer and can continue in-place (`gitbroker/approval.go`); GH has bounded broker wait/status machinery (`window.go`, `forward.go`); generic boundWrite immediately returns ask and Handle exits 75. Generic CLI has 30s call budget, 40s forwarding deadline, 5s dial. Persistent requests survive that client return, but uniform automatic wait semantics are not built.

**Replay discrepancy to settle:** GH exactGrantGate rebinds a stored parent's jail/start/launch to the current invoking launch of the same workspace before revalidation; `TestExactGrantOutlivesItsBrokerForItsWorkspaceOnly` asserts reuse after restart. Generic boundWrite gate calls `h.revalidate(held)` without rebinding, while revalidate requires current start/launch/jail. Interpretation: generic exact grants can be rejected across launches despite workspace-holder wording. Add a synthetic reproduction before calling it a confirmed runtime regression; timed full-chain entries have different checks.

## 6. Notifications and capability services

`config.OnRequest` is user-only shell command, default unset; workspace authorship is refused. `brokerhook.Runner` watches only its start's request-filed events; hook stdout is **never consent**. It supplies safe summaries, JSON on stdin, YOLO_REQUEST_* and an absolute YOLO_BIN plus private yolo PATH link, runs from user home, removes terminal-pane identity. Hook exits/timeouts/output tails are stored on request; hooks end when request leaves pending/expiry/broker cancellation, SIGINT then process-group kill. No duplicate request means no duplicate notification.

`approvalnotify` is the optional Linux actionable doorbell. It validates schema/pending ID/expiry, pins D-Bus server owner, checks actions support, offers once/15m/deny, and invokes host CLI as sole decision effect. It withdraws notifications without sending an old numeric ID to a replacement server. Browser `yolo approve --web` remains host-only, read-only; do not accidentally introduce browser mutation authority in this policy work.

Valued capability grants already distinguish host ceilings, workspace request values, actual granted snapshot and live withdrawal. `serial.devices`/`host-processes.view` have no default values and cannot be under always; audio crossing changes at restart; log bridges reread grants per call. **Interpretation:** reuse their grant vocabulary/liveness, but distinguish gate-time requestable capability from operation-time ask. Transport reachability is not authority, and credential-refresh services should not inherit unrelated operation policies.

## 7. AWS host execution and routing hazards

`awsclassify.Classify` admits exactly S3 `ls` (optional recursive), one-object cp to stdout or put from stdin, one-object rm, and s3api head-object. No local host path copies, sync or arbitrary SDK/CLI services. CE exact rows are get-cost-and-usage, get-cost-forecast, get-dimension-values, get-tags; canonical suffix `--output json --no-paginate`. CE resource ID presently distinguishes operation but not normalized query: `cost_query ce://host-login/<operation> < location ce://host-login`. Exact query approvals must additionally bind normalized query/canonical call, not claim this ID is sufficient.

S3 roots select longest covering bucket/prefix string; prefix boundary ends `/`. Root change can change chain identity, invalidating a timed answer. CE accepts only `ce://host-login`, deliberately host-login-wide, no account/role/billing-view pin. Shared AWS read grant cannot independently admit CE but deny S3: roots choose services, standing read covers both. This fails the requested service/resource read policy despite successful admission isolation.

`pinCLI` resolves/hash-pins host executable and captures exactly host PATH/HOME plus ten manifest names: AWS_PROFILE, AWS_REGION, AWS_DEFAULT_REGION, AWS_CONFIG_FILE, AWS_SHARED_CREDENTIALS_FILE, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN, AWS_ENDPOINT_URL, AWS_ENDPOINT_URL_S3. Executor runs cwd `/`, canonical argv and original digest-checked stdin, bounded owned process, ≤32MiB each stream. All nonempty declared variable values are redacted, including nonsecret region/profile strings, potentially changing returned financial data. Jail profile/region/endpoint/signing/debug/file flags refuse. Trusted host login/helpers/plugins/config and endpoint overrides still affect identity/output.

**No current bucket/prefix-to-profile routing:** one captured environment serves all admitted AWS calls. A proposed host profile selector must account for environment static credentials overriding selected profile, external config/credential filenames, endpoint routing, region and credential helpers. Merely appending `--profile` is not sufficient evidence of desired identity. Store/profile route binding must be nonsecret, host-controlled and revalidated before approval/spend; changed route must not silently reuse an approval or timed grant. Do not put host profiles/env values into classifier protocol or let workspace config select them unrestrictedly.

`aws-auth` settings profile/role_arn/session_policy/unnarrowed are user-only; absent profile serves nothing, unnarrowed default false. SDK-delivered credentials are an independent bypass. Full-check `reportAWSCredentialBypass` inspects config only, flags selected aws + configured unnarrowed aws-auth, names S3/CE and administrator next step, and explicitly says narrowed role is not proof bypass is absent. It performs no IAM inspection and adds no routine launch warning. Preserve that boundary unchanged.

## 8. Smallest complete architecture (proposal)

1. **One operation policy model and evaluator**, with source adapters producing typed read/write operations, canonical intent, resource chains and optional facts. Keep classifiers ignorant of permission/credentials. Extend existing schema rather than making AWS exceptions or a second store. Retain native Git transport and GH grammar/fact readers; do not rewrite their wire protocols just for uniform policy.
2. **Separate admission from authority:** selected source + gate-approved root is required; host ceiling bounds eligible standing/requestable operations; workspace explicit deny/ask can narrow; absent rules preserve existing accepted broad/default behavior. Hard classifier refusals/out-of-root/missing execution authority never become approvable through a notification. Resource-specific rules need declared matching/containment and deterministic specificity, plus explicit tie/conflict semantics. No arbitrary strings/prefix matching across sources.
3. **Make reads first-class approval subjects**, including operation/query identity, descriptions and ordered resources. Reuse exact digest/once/timed transaction machinery, extending it compatibly for read and mixed bundles. Internal fact reads must meet policy too; specify how approval-required discovery proceeds without recursively authorizing unseen provider reads. An evaluator may not leak CE/S3 data through a fact lookup before the outer ask.
4. **One lifecycle shell:** reuse hook runner, request visibility, Store.End, PublishFailed, wait/status/timeout semantics and safe summaries across native/generic adapters. Wire production callers, not only helper tests. No approval UI becomes authority.
5. **Host-only execution routing** is separate from permission but bound to intent/approved authority. S3 resource matcher chooses one admitted route; default preserves current host-login behavior. Route identity/version enters digest/live revalidation and timed coverage as needed; classifier never chooses credentials. Keep aws-auth unchanged and visibly separate.
6. **Versioned compatibility:** broad explicit grants remain broad; existing working-set behavior persists unless an authored narrower rule applies. Legacy records remain readable; new scoped read/route authority is never fabricated from old answers. Removed sources stay deny-only. Uniformity means shared semantics, not flattening branch pushes, CE queries, serial device admission and credential refresh into identical resource kinds.

## 9. Acceptance scenarios and remaining investigations

Concrete account-free acceptance outcomes:

- Select each source with no grants: no credentialed read/write, no execution; preserve documented anonymous public git fetch exception. Add explicit broad grant: prior supported behavior unchanged.
- Same request policy semantics across git/github/aws/circleci: deny files no request; ask never dispatches before approval; standing exception runs; narrower deny overrides broader grant; ambiguous rules fail clearly.
- AWS CE always-read plus generally asking S3; bucket A/prefix RW, bucket B/prefix R-only; neighboring prefixes, list parent, put/delete and changed query/body never widen. Unknown service/operation still refuses.
- Scoped Git/GH read accepted by revised config/runtime, other repo denied/asks as specified; explicit broad read remains broad. CircleCI admitted project listings follow the same lifecycle without activating declared-but-unimplemented reruns.
- Ask persists after client timeout, one notification per filed request, wait wakes on once/deny/expiry/shutdown; duplicate joins, three-pending/cooldown retained. Generic daemon uses actual production runner and closes pending on shutdown.
- Two simultaneous retries spend exactly once; two approvers first-decision wins; scope/ceiling/route withdrawal before dispatch prevents spend/effect; withdrawal after possible transmission gives no refund/retry; multi-operation answer commits all-or-none.
- Route selects configured host profile for S3 resource, never jail profile/env injection; overlapping route specificity deterministic; route change invalidates incompatible stored answer; synthetic environment precedence checks prove static credentials cannot silently select wrong identity.
- Cross-jail/relaunch same-workspace exact/timed behavior follows the declared compatibility contract; another workspace/source/resource/query cannot reuse. Predictions inspect but never file/spend/write and do not execute unauthorized facts.

Reuse observed test assertions: `integration/TestAWSBrokerMediatesReadsAndAsksForWrites`, `TestAWSBrokerTimedAnswerIsCappedAndCoversItsResource`, `TestAWSBrokerCostExplorerRequiresItsOwnRoot` (same running jail, FIFO-driven apply/withdraw), `TestCircleCIFakeHostCLIUsesRealClassifier`; generic conformance synthetic lifecycle/invalid-answer/stale-approval; brokerstore atomic answer failure/fresh-reader tests and scope-writer/consume serialization. Test-name inventory additionally identifies `TestSelectedCLITwoWriteAllowOnceBindsFullOrderedBundle`, `TestSelectedCLIReadAndEvaluateRefuseCurrentHostReadWithdrawal`, HTTP withdrawal/preflight and no-refund tests. Name-only discoveries are pointers, not inspected proof. Nested tests establish wiring, not rootless-host reachability or real AWS CLI/IAM compatibility.

Bounded engineering follow-up: reproduce generic cross-launch exact reuse and working_set-only ceilings; trace all config-apply cell/route publication; trace external launcher cleanup before stating global absence of End; assess generic pinned program placement (pinCLI lacks native PlacementRefusal call, but launch validation may enforce it); verify full CLI allow/request/edit/refusal schemas after introducing read operations; map GH fact-read prerequisites and Git authenticated fetch granularity; design typed CE query identity and S3 containment; add production notification/wait parity witnesses. Genuine precedence/routing/temporary-scope policy choices not settled above belong in the owning design, not silently chosen by an implementer.

## Contradictions and sources

- [Pack brokers, §3](../design/pack-brokers.md#3-what-exists-today) says approve hard-lists two sources; **current approvesources.go already discovers generic adapters**. Reuse it.
- Service recipe says cells never read/reads never ask; current schema agrees, but **owner's latest universal scoped-read requirement supersedes that constraint**.
- `github-write-scopes.md` describes six cells/tables/verbs as normative design; its status and `ghbroker/cells.go` show only shipped working-set comment/edit defaults and repository binding are built. Do not advertise full GitHub table policy as current.
- Workspace-held-grant prose versus generic parent-holder check needs reproduction, not silent reconciliation.
- Nonsecret host-env redaction is an explicit observable trade-off, not a promise of unmodified financial output.

| Source anchor | Why kept |
| :--- | :--- |
| [Call-broker declaration](../../internal/loopholedecl/callbroker.go) | Current cell/schema constraints |
| [Call broker](../../internal/callbroker/) | Classification, policy and production lifecycle |
| [Broker store](../../internal/brokerstore/), [scope](../../internal/brokerscope/), [daemon core](../../internal/brokercore/) | Authority, transactions and notification reuse |
| [Native Git](../../internal/gitbroker/), [GitHub](../../internal/ghbroker/) | Existing transport and approval adapters |
| [Scoped ceiling reader](../../internal/config/readablenamed.go) | Shipped WRITE-only restriction |
| [AWS classifier](../../internal/awsclassify/), [AWS manifest](../../packs/aws/pack.toml) | Admitted service grammar and captured environment names |
| [Git manifest](../../packs/git/loopholes/git-broker/loophole.toml), [GitHub manifest](../../packs/github/loopholes/github-broker/loophole.toml), [CircleCI manifest](../../packs/circleci/loopholes/circleci-broker/loophole.toml) | Shipped source defaults and admission boundaries |
| [AWS authority](../reference/aws-operation-broker/authority.md), [execution](../reference/aws-operation-broker/execution.md) | Service invariants and measured limits |
| [Pack brokers](../design/pack-brokers.md), [GitHub write scopes](../design/github-write-scopes.md), [approval experience](../design/approval-experience/README.md) | Existing contracts, reuse and incomplete slices |
| [Integration tests](../../integration/) | Inspected assertions and future regression targets, not fresh passing receipts |

Deprioritized stale status prose, historical aws-s3 fixtures and prior memory as behavioral evidence. No external source was used. [Execution context](../glossary.md#execution-context) and [route](../glossary.md#route-broker-policy) name the proposal, not current authority.
