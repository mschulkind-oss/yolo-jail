---
title: "Access introspection: build hand-off"
date: 2026-11-04
status: accepted
stage: DECIDED
next: "Await parent authorization to start Task 4 (own-jail readout and grant snapshot)"
depends-on:
  - access-introspection.md
tags: [plan, broker, dry-run, introspection]
summary: "Tree-grounded hand-off for yolo access, broker-owned decisions, pre-execution approval filing and a workspace-only readout."
---

# Access introspection: build hand-off

**Written against:** `1c6f83c1ed4723268e8e789baeace8e7cc154bf2`, 2026-11-04. Planning only;
no runtime changed by this document. **Design:** [access-introspection.md](access-introspection.md).
Precedence: the design wins on behavior; the tree wins on cosmetic facts; this plan is advice and
is first to be wrong. Never twist code to match an overtaken plan. Stop and report without
committing if a named seam disappears, a proposed red test is not red for the stated reason,
someone else's file is dirty, a fix crosses a task's fence, or an out-of-scope gate fails.

**Integrated implementation receipt:** GitHub tasks 1–2 provide shared act/evaluate decisions,
versioned status/evaluate/ask forwarding, workspace-scoped status, and atomic pre-ask. The actual
`ForwardAccess` production handler path is pinned through the published endpoint for all three
operations; its frames have only the versioned `access` envelope, and evaluate/ask carry nested
argv. The historical act-handler rotation regression and flattened-envelope negative controls
are included.

**Task 3 (Git ref-update prediction and pre-ask) is integrated in main at**
`bc5f0f7da23b806fce5c7c5ba38c4b602fac27e8`; its accepted frozen source lineage ends at
`0c4c09fdac9bab2e0ec3c765634663ec5e93b8a4`. The client derives selected updates through a
helper-mediated dry run; the host verifies proposed old refs against a fresh receive-pack
advertisement and reuses the act decision. Prediction and `--ask` send no ref-update or pack
bytes, file only the existing canonical request under the same atomic coverage check, and do not
spend a grant or wait. The typed `yolo.access.prediction/1` reply separates actual call/evidence
digests and offers only verified commands. Seven affected packages and focused race suites passed on the integration source commit. A complete census
at `045be7632a12738f6eae634ccbeb864ee5509632` measured 16,988 tests and generated the current reader
inventory: 979 reading tests in 35 packages, committed at `d22f8c6b7a2f5ff3cc3a87a2b3fc051b6e6cfb76`.
The later parser guard at `21ed3f2c044bec6078ef06ef6a2f9ad6633b709a` only preserves existing field-presence
checks and adds malformed-wire cases inside an already inventoried test; the census was not rerun after
that Go-only change. On that exact source commit, `just check-ci`, `just done`, the six matching
`TestGitBroker*` integration cases, and focused Git-package normal, race, and static checks passed.
The full slow integration suite remains for final combined landing; this slice does not establish native
macOS or rootless-only behavior.

Tasks 4 and 5 remain unbuilt: Task 4 is the launch snapshot, aggregate readout and briefing;
Task 5 is the equal-input prediction-divergence tripwire and shipped documentation. No GitHub
path is evidence for Git prediction, MCP status, aggregate snapshots, or divergence audit.
Starting Task 4 awaits explicit parent authorization; do not import its snapshot/runtime work,
MCP status, or resource-group code as part of Task 3. The earlier broad-run preparation readiness
and temp-directory removal failures remain unexplained; the prior Task 2 affected-package run and
`just check-ci` passed, which does not establish their cause or a flake-rate fix. Earlier
integration receipts and their limits remain recorded in their handoff.

## Map

| Path | Change |
| :--- | :--- |
| `internal/ghbroker/authority.go`, `workingset.go`, `dispatch.go`, `approverun.go`, `window.go`, `daemon.go` | Separate host scope/sets and remote fact reads from decision; use one result in act, evaluate and pre-ask; retain transaction-time revalidation before effects. |
| `internal/ghbroker/forward.go`, `internal/ghbroker/*_test.go` | Versioned status/evaluate/ask frames, exact stdin relay; consumer-path fake-host tests. |
| `internal/gitbroker/broker.go`, `policy.go`, `push.go`, `https.go`, `approval.go`, `approvalauthority.go`, `*_test.go` | Decision over classified ref updates, host-verified remote refs; no push or credentialed write in evaluate/ask; exact request binding. |
| `internal/gitremote/helper.go`, `internal/gitremote/*_test.go` | Reuse helper's connect transport for git's dry-run preflight, not its act push stream for prediction. |
| `internal/brokerstore/store.go`, `*_test.go` | Read-only workspace projection and atomic *file-if-still-asks* operation; never call spend-on-evaluate or `SpendExactOrFileContext` for `--ask`. |
| `internal/cli/access.go` (new), `internal/cli/access_test.go` (new), `internal/cli/dispatch.go`, `subhelp.go` | `yolo access` source routing, json/text rendering, host-without-jail refusal, usage and exit codes. |
| `internal/brokeraudit/brokeraudit.go`, `internal/cli/audit.go`, `audit_test.go`, `internal/cli/check/*` | Evaluate and divergence records; `--event` host filter and defect finding. |
| `internal/cli/run/loopholesettings.go`, `loopholegrants.go`, `liveapplygrants.go`, `loopholesruntime.go`, relevant `*_test.go` | Publish sanitized per-launch access snapshot from approved grant block and rewrite on mediated live apply; absent/stale launch handling. |
| `internal/cli/run/brokeredscope.go`, `internal/ghbroker/daemon.go`, `internal/gitbroker/broker.go` | Scope provenance in broker readouts from live approved scope, not a jail copy. |
| `packs/github/pack.toml`, `packs/git/pack.toml`, `internal/jailcontent/*` | Broker briefing line at the existing pack briefing contribution seam; only touch jailcontent if the current composer requires it. |
| `integration/*access*_test.go` (new), `userguide/guides/loopholes.md`, `docs/reference/loophole-grants.md`, `docs/reference/loophole-transport.md` | Consumer end-to-end fixture and overlapping reference claims; no release changelog. |

## Reuse and engineering choices

- **Constraint:** `ghbroker.Forward` (`internal/ghbroker/forward.go`) reads stdin only for `readsDash`; `requestFrom` (`daemon.go`) decodes it. Route both prediction and pre-ask through that exact byte collection, repository resolution and broker classifier, not shell-string parsing. Digest remains `brokerstore.CommandDigest` over canonical argv/repo/stdin digest (the set is a separate request field); preserve `StdinSent` and distinguish absent/empty stdin in audited facts.
- **Constraint:** `ghbroker.Authority.Classify` reads live scope and host sets; `coveredWrite` reads issue state, `approveRunCovered` reads run actor and `onDefaultBranch` reads default branch and pins dispatch argv. Move *all* such lookups into predecision facts, including read failures and branch pinning; effectful `EndWorkingResource` on merged PR goes only to act after the verdict, never evaluate. Keep existing live revalidation and required audit inside dispatch transaction.
- **Constraint:** `gitbroker.readPush` parses packet commands and options via `gitproto.ReadCommands`; `judge`/`coverFor` judge every ref and `askForPush` builds `pushRequest` digest. Its canonical argv is `git-receive-pack`, each `old new ref` in order, then each `push-option=value` in order (`approvalauthority.go`); the user-facing `git push` argv itself is **not** the request digest. Extract reusable classification/facts/standing/decision for act, evaluate and pre-ask without changing whole-push atomic refusal. Verify the remote's old OIDs on the HOST at decision time; jail-proposed ref updates are never remote facts.
- **Confirmed offline:** git 2.56 `git push --dry-run --porcelain probe::foo HEAD:refs/heads/probe` against a local `connect git-receive-pack` helper produced `* HEAD:refs/heads/probe [new branch]`; packet trace showed advertisement followed by `push> 0000` and remote had no refs. **Constraint:** dry-run does **not** send ref-update commands to the helper. Client must derive candidate updates from porcelain/refspec selection and local object IDs, send a separate versioned evaluate/ask request, and host must compare them with its own remote advertisement. Recover effective push options (including `push.pushOption` config) exactly in wire order too; if that cannot be established, refuse pre-ask rather than file an unspendable digest. Ambiguous refspec, rejection, unresolved local OID or mismatch fails closed with a specific next step; never interpret a `0000` dry-run as an empty proposed push. Extend offline probe to deletion, wildcard, force, tags, up-to-date and HTTPS; keep routing through the broker (no direct remote login).
- **Constraint:** `brokerstore.SpendExactOrFileContext` first spends a live exact grant even with nil dispatch. `FileContext` dedups pending requests, enforces pending cap/cooldown and calls revalidation, but alone does not test a newly arrived grant. Add one atomic store transaction which rechecks the same live classification/facts/standing, returns `runs` if now covered *without spending*, otherwise joins/files via `fileIn`; the caller delivers once and returns immediately without `waitStatus`, dispatch or `afterWrite`. Recheck at later real act; no reservation. Keep `NeedsApproval`, `About`, policy and digest identical to real filing.
- **Constraint:** the GH and git framing is length-prefixed JSON request (`frameproto.WriteRequest`) and stream frames, with no version field today (`ghbroker.forward.go`, `gitremote/helper.go`). Add explicit `op`/version for new messages with strict broker-side allowlist before the act branch; keep legacy act frame compatible. Unknown version/op must be unsupported, not interpreted as act; old broker's non-JSON or act-shaped reply must be rejected by new client. Status uses framework `{"op":"status"}`; collision with GH's existing `operation=status` for request IDs is prohibited.
- **Constraint:** `brokerstore.List` reads request and grant records in a transaction; working entries are loaded there but not returned. Add a read-only single-transaction workspace projection including working entries and limits, filter by the **host-asserted** workspace, sanitize hook output, host paths, settings, tokens and other workspace records before serialization. Scope uses broker's live scope file; non-broker snapshot never authorizes a call.
- **Constraint:** launch writes grant block through `run.publishLoopholeSettings` → `loopholeGrantBlock`; mediated live changes use `loopholegrant.LiveFiles`/`Replace` from `run/liveapplygrants.go`. They are distinct paths, not a common `writeScopeFiles` call. Derive sanitized snapshot from the exact approved block at each path, keyed by launch ID, fail closed on missing identity and never read new config after gate; stage/replace atomically with the corresponding block. Do not copy settings or unmediated secret values.
- **Advice:** use the existing `hostGHFake` (`workingset_test.go`) and git fixtures (`broker_test.go`, `https_test.go`), and `approvalwait.Record`/`RequestView` for menus; they observe writes and request IDs without a login. The CLI registry is `internal/cli/dispatch.go`, not `cmd/`.

## Traps and gates common to every implementation task

- The design's abstract ordered decision is not yet code: GH standing sets and git public HTTPS anonymous fetch have existing precedence. Preserve their current outcomes with paired act/evaluate fixtures; never silently replace them with a generic GitHub cell parser. GitHub and git are independent current sources; the future pack classifier has no decided implementation contract.
- Fact reads may contact upstream under read authority, but `git-receive-pack` advertisement must never carry command/pack bytes in evaluate; GH `gh api` reads must never pass an unclassified write method. Prediction's `runs` means the broker would forward, not upstream success.
- `--ask` is a third *effect* after evaluate, not an act mode with forwarding disabled: no request for `runs`/`refused`; pending cap/cooldown/duplicate still apply to `asks`. If an allowed exact grant arrives between evaluation and filing, answer `runs` without consuming it; a later act can spend it. `--ask` must not block for an answer.
- For equal inputs digest, hash the fully classified call, all fact values *and failures*, the live standing/limit state and time-sensitive coverage relevant to the verdict in stable canonical order; keep command digest separately. Never put arbitrary host config, token, secret, or `on_request` command in prediction/audit. Keep last 15 minutes by workspace+command digest; compare only equal input digests and report divergent verdicts without changing act outcome.
- Pre-effect boundary for GH is before `runner.Run` of a write, store mutation, and `afterWrite`; for git before any receive-pack command bytes (`push.go`, `https.go`), credentialed POST or store spend. Required evaluate audit is the sole permitted host-side effect of plain prediction, plus its prediction record.
- Each implementation commit: add caller-driven red test; run its targeted `go test -short`; `just format`; `just build-dev-bundle` and nested smoke from a throwaway workspace for `internal/`/`cmd/` logic; `just check-ci` at combined-tree landing, relevant integration test, full suite once for changed launch path at landing, not once per task. No interactive agent/API or real credential tests. Test HOME isolated with `t.Setenv`/temp paths, preserve shared Go cache and original HOME; never replace host configuration.

## Tasks (one commit per task; paths shared across tasks land in this order)

**1 — GitHub decision seam** (independent of pack-broker rulings; unblocks 2 and 3).
- **Settled, do not re-open:** one parse/scope/fact/standing/pure decision pipeline; equal inputs in act/evaluate mean equal verdict; no changed act policy.
- **Tests first:** add `TestServeEvaluateAndActUseSameDecision` in `internal/ghbroker/daemon_test.go`: fake host runner returns open PR/default branch, approved scope, uncovered merge, assert evaluate `asks`, no writes/request/spend, act files identical request and never merges until approved; table for no read, out of scope, covered user/working/timed grant, merged item, failed fact read. Red: `go test -short ./internal/ghbroker -run TestServeEvaluateAndActUseSameDecision -count=1` (new op initially unsupported).
- **Change:** `Authority.Classify`, `coveredWrite`, `readIssue`, `approveRunCovered`, `onDefaultBranch`, `window`, `ServeContext`; classify canonical request once, capture fact failures, pure outcome; keep act's transaction-time recheck and checked pinned argv.
- **Gates:** red above, then `go test -short ./internal/ghbroker ./internal/brokerstore` plus common gates.
- **Fence:** no [`pack-brokers.md`](pack-brokers.md), pack classifier/core executor or MCP-plan edits. **Commit:** `refactor(access): share GitHub pre-effect decisions`. **Report:** red/green results and any act fixture outcome changed (stop if so).

**2 — GitHub protocol, readout, pre-ask and CLI** (after 1; unblocks 4 and 5).
- **Settled, do not re-open:** `yolo access`, plain prediction files nothing, `--ask` files the real request on `asks` without executing proposed call; exact bytes, pending cap/cooldown/dedup and subsequent Allow once binding remain intact.
- **Tests first:** `TestAccessAskFilesWithoutMerging` in `internal/cli/access_test.go`: temp fake broker, `gh pr merge 40 --squash`, assert plain JSON `yolo.access.prediction/1`, `asks`, no store request/write; `--ask` returns same command digest plus real `r-` ID and one pending request; repeat joins same ID; approve once and real act spends exactly once. Pair stdin test `gh api --input -` with two different byte bodies; second cannot spend first's grant. Red: `go test -short ./internal/cli -run 'TestAccessAskFilesWithoutMerging|TestAccessStdinBinding' -count=1` (verb absent).
- **Change:** `ghbroker.forward.go` and request decoder op/version; broker `status` workspace readout; new `brokerstore` atomic file-if-asks and read-only projection; `cli/access.go`, registry/help and sanitized rendering. Timeout readout source at 5s, continue other sources; usage 64, unavailable 69, any verdict answer 0. Host with no jail names `yolo approve --json`.
- **Gates:** red above, then `go test -short ./internal/cli ./internal/ghbroker ./internal/brokerstore ./internal/frameproto` plus common gates.
- **Fence:** no git broker implementation before task 3; no host cross-workspace readout. **Commit:** `feat(access): expose GitHub readout prediction and pre-ask`. **Report:** red/green, exact digest and duplicate evidence.

**3 — Git ref-update prediction and pre-ask** (after 2; independent of future pack brokers).
- **Settled, do not re-open:** git's own dry-run obtains selected refs only; host verifies old refs; same decision function as act per ref, whole push asks when any ref asks; no bytes forwarded to receive-pack beyond advertisement on evaluate or pre-ask.
- **Tests first:** `TestGitAccessPushAskNeverSendsCommands` in `internal/gitbroker/broker_test.go` with local bare upstream and fake receive-pack logger: dry-run selection, evaluate `asks`, pre-ask same request digest as real push, no remote change/pack bytes, duplicate request joins, approved exact later act succeeds; include deletion/force/unknown option/changed remote OID and HTTPS fake (`https_test.go`). Red: `go test -short ./internal/gitbroker -run 'TestGitAccessPushAskNeverSendsCommands|TestGitAccessPushRefBinding' -count=1` (evaluate frame unsupported).
- **Change:** `gitremote/helper.go` client-side dry-run adapter, `gitbroker/broker.go` status/evaluate/ask frame handling, `policy.go` pure per-ref decision, `push.go` act reuse, `https.go` no POST in evaluate, `approval.go` same request constructor and atomic file-if-asks. Host checks new/local OID is the proposed exact call's selected update; stale advertisement refuses or retries preflight, never silently approves a different ref. Fetch/pull/ls-remote use URL read decision only.
- **Gates:** red above, then `go test -short ./internal/gitbroker ./internal/gitremote ./internal/gitproto ./internal/brokerstore` plus common gates.
- **Fence:** no invented pack broker transport; no live remote credentials. **Commit:** `feat(access): predict and pre-ask git broker calls`. **Report:** red/green, offline packet evidence for all ref forms and HTTPS.

**4 — Snapshot, aggregate readout and briefing** (after 2; git rows after 3).
- **Settled, do not re-open:** brokers report their own live workspace state; only non-broker loopholes use launch-written snapshot, rewritten on live apply. Missing snapshot/endpoint/old broker have per-source next steps, not fabricated standing.
- **Tests first:** `TestAccessReadoutUsesLiveBrokerAndApprovedSnapshot` in `internal/cli/access_test.go`, fake two sources + temp snapshot: only own workspace grants/working entries/recent requests; absent snapshot says relaunch; timed-out broker leaves other source usable. `TestAccessSnapshotChangesWithLiveGrant` in `internal/cli/run/liveapplygrants_test.go` checks real live-apply call site, valued kinds and no settings/secret. Red: `go test -short ./internal/cli ./internal/cli/run -run 'TestAccessReadoutUsesLiveBrokerAndApprovedSnapshot|TestAccessSnapshotChangesWithLiveGrant' -count=1`.
- **Change:** snapshot publication and live apply as mapped; aggregate readout includes inert/unreachable. When an MCP sign-in daemon implements its own `{"op":"status"}` contract, aggregate its live answer; show unavailable rather than inventing MCP records now (no MCP status daemon is present in this tree). Update broker pack briefings.
- **Gates:** red above, then `go test -short ./internal/cli ./internal/cli/run ./internal/loopholegrant` plus common gates.
- **Fence:** no MCP sign-in plan sibling edits, no secret-bearing settings. **Commit:** `feat(access): publish own-jail readout and grant snapshot`. **Report:** red/green, launch/live-apply parity and absent-source output.

**5 — Truthfulness tripwire and shipped documentation** (after 2 and 3; last access slice).
- **Settled, do not re-open:** equal inputs + divergent verdict produces `prediction-diverged` audit and `yolo check` finding; no block. `--event evaluate` filters host audit, 15-minute prediction window.
- **Tests first:** `TestPredictionDivergenceOnlyForEqualInputs` in `internal/ghbroker/daemon_test.go` with fake clock, different/equal inputs and expiry; `TestAuditEventFilter` in `internal/cli/audit_test.go`; check test drives actual finding call site. Red: `go test -short ./internal/ghbroker ./internal/cli ./internal/cli/check -run 'TestPredictionDivergenceOnlyForEqualInputs|TestAuditEventFilter|TestPredictionDivergenceFinding' -count=1`.
- **Change:** audit events and CLI filter/help; check finding with actionable `yolo audit --event prediction-diverged`; text/JSON docs and relevant integration fixture in map. Rewrite old tests that assert previous refusal/menu shape, never make them green by reverting new behavior.
- **Gates:** red above, then `go test -short ./internal/ghbroker ./internal/gitbroker ./internal/cli ./internal/cli/check`; `go test -run TestAccess ./integration` using offline broker fixture; `uvx vantage-check` exact changed docs; common gates. **Commit:** `feat(access): audit prediction parity and document access`. **Report:** red/green, integration and any unverified platform reachability.

## Ships with / boundaries

- JSON schemas `yolo.access/1` and `yolo.access.prediction/1`, text with agent/human next steps and expiry; no user hook command, other workspace, or settings value in either. Existing `yolo wait`/`yolo history` records remain distinct from MCP sign-in status.
- Test both modes at production handler call sites, including malformed op/version, no endpoint, absent snapshot, source timeout, request limit/cooldown, allow-once not consumed by `--ask`, same/different stdin, stale remote refs, whole-push mixed refs and audit divergence. Source-specific fake credentials only; never run an agent or use a real HOME credential.
- Cheap implementer choices: Go result struct field layout and JSON subfield names not fixed by the design, bounded cache representation; maintain fixed schema names/verdict/rule/covered_by/next/relies_on_until. No new config keys or migrations.
- **Later, independent slice:** the [pack-broker owner rulings](pack-brokers.md#decision-ledger) are settled; its tree-grounded hand-off still precedes implementation. Wire its classifiers to the status/evaluate/ask contract once the corresponding access slice is built; this is **not** a prerequisite for tasks 1–5 and must not pretend a pack broker exists now. Preserve the pack design without reopening its decisions. MCP sign-in status aggregation waits for that daemon's separately planned implementation, not for a fabricated core record.
- No blockers for GitHub/git/snapshot implementation. A newly discovered product question must be filed in the owning design, routed by its roadmap and raised to the owner; do not hide it as an implementer choice. No broad broker framework rewrite or upstream verdict prediction.
