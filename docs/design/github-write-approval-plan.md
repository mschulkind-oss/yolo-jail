---
title: "Implementation plan: production GitHub Allow once"
status: accepted
stage: DECIDED
next: "Root to inspect the private candidate; do not land without separate authority"
tags: [implementation-plan, github, approvals, security]
summary: "A source-grounded hand-off to wire one exact host-approved GitHub write without changing other broker sources or enabling standing writes."
---

# Implementation plan: production GitHub Allow once

**Written against:** `b8a32ac0ff0a06d417ed5725a1e62239afd505e0` (2026-10-28), the verified private
worktree base. The parent `main` had the same base at admission; Stage 2 stays private and preserves
history.

This is the implementation hand-off for [`github-write-approval.md`](github-write-approval.md).
The policy authority remains [`boundary-broker.md`](boundary-broker.md), especially BB-D76/77/79,
BB-D98 and BB-D99. Its timed-resource and working-set Add answers stay unfinished backlog. The
[research note](../research/github-write-approval.md) records current production wiring and the
measured-red caller test. [Stage 1 QA evidence](../research/github-write-approval-qa.md) records
commands, outcomes and environment limits. The lead has accepted the bounded Stage 2 direction;
implement only the listed source changes.

## Contract and fence

- Wire only exact, one-use, 15-minute Allow once for accepted GitHub writes, using the existing
  host request store and `yolo approve <id>`. Preserve the authenticated forward/front, classifier,
  read behavior, scope checks, runner isolation and all host-only GitHub credentials.
- Put an exact-grant selector in the shared broker store and opt the GitHub path into it. A legacy
  empty-digest GitHub grant must never match. Do not globally change set-wide store semantics or
  the git broker's independent grant model.
- Reject a configured standing legacy `read-write` policy for writes before request filing or
  dispatch. Name the `brokered.github.sets` repair step; do not edit or migrate user config and do
  not add another grant/config mechanism. Keep standing reads unchanged.
- Preserve the exact argv/repository/stdin-byte-digest binding from classification through request,
  decision, spend, audit and execution. Audit digests and IDs, never stdin contents or credential
  material; required request/decision/spend audit remains before dispatch.
- Reuse `brokercore.Daemon.Main`'s existing `StartOnRequest`/`on_request` path and host broker log;
  a configured host hook is optional and may call `yolo approve`, but its output/exit status alone
  is never consent. The host CLI/log path supports manual approval without a hook. No HTTP approval
  endpoint, terminal-startup notice, launcher warning, host-run fallback or interactive test.
- A pending request is visible in host `yolo approve` / `--json` / `--watch --json`. Deny, silence,
  delivery/audit failure and lost responses never run or automatically repeat a write. After an
  unanswered bounded wait, the agent may reissue only the exact action if the host still wants it.

## Stage 1 RED evidence (complete)

The current in-tree consumer-path tests are `TestProductionGitHubWriteApprovalFlow` and
`TestProductionStandingWritePolicyRefusesWritesAndKeepsReads` in
[`internal/cli/ghbrokerapproval_test.go`](../../internal/cli/ghbrokerapproval_test.go). They start
production `ghbroker.Main` in a helper process, route `ghbroker.Forward` through
`svcendpoint.ServeFront`, and run only recording fake host `gh`/`on_request` commands. The Allow-once
flow configures a hook that logs the request ID but only prints `approve`; it then proves the request
stays pending until the actual host `cli.Main` `yolo approve` command is called. No private delivery
seam is installed. The flow expects these exact actions and host receipts:

| Case | Forward argv | Expected fake-host receipt |
| :--- | :--- | :--- |
| PR comment | `pr comment 41 --repo o/r --body approved-comment` | `pr comment --repo=o/r --body=approved-comment 41` |
| Issue close | `issue close 7 --repo o/r` | `issue close --repo=o/r 7` |
| Standing-policy control read | `pr view 41 --repo o/r` | ordinary standing read returns `fake GitHub receipt` |

The standing-write test sets only its temporary user's `brokered.github.sets` to
`read-only: standing` and `read-write: standing`. It requires the read above to run, but the write
`pr comment 41 --repo o/r --body must-not-run` to return `ghbroker.ExitNoPerm` with both
`brokered.github.sets` and `remove` in the explanation; no request is filed and no comment reaches
fake `gh`. These temporary policy fixtures do not alter any user config.

Exact RED command:

```sh
go test ./internal/cli -run '^(TestProductionGitHubWriteApprovalFlow|TestProductionStandingWritePolicyRefusesWritesAndKeepsReads)$' -count=1
```

The command's expected current non-zero result is captured outside the checkout at
`/workspace/.yolo/durable/github-write-approval-item5/stage1-red-regression-05.txt`; do not treat it
as a green feature test. The writes presently exit 77 before filing. The read control succeeds,
while a standing-policy write receives the generic missing-delivery refusal rather than the
policy-specific refusal. The production test proves the real approval caller and fails if production
`RequiredDelivery` wiring is removed.

## Stage 2 tasks (ordered)

### 1. Close the standing-write escape before enabling delivery

In [`internal/ghbroker/authority.go`](../../internal/ghbroker/authority.go), reject a write that
resolves from the legacy `read-write` set to `OutcomeStanding`, with an actionable refusal naming
`brokered.github.sets` and telling the host user to remove or make that entry non-standing. Make the
initial classification fail closed, so `ServeContext` cannot route it into `window` and let its
second classification take the standing dispatch branch. Remove/update stale comments in
[`internal/ghbroker/daemon.go`](../../internal/ghbroker/daemon.go) that describe a standing write as
safe. Change `TestHandlerSerializesScopeRemovalThroughStandingAndWindowedDispatch` in
[`internal/ghbroker/dispatch_transaction_test.go`](../../internal/ghbroker/dispatch_transaction_test.go)
from proving a standing write dispatch into proving it is refused, no fake executor call occurs,
and ordinary standing reads remain covered by existing classifier tests and the production control
above. Do not refactor or weaken base read classification.

### 2. Add an opt-in exact-grant spend selector

In [`internal/brokerstore/store.go`](../../internal/brokerstore/store.go), preserve the current
default spend semantics for other sources but add an explicit exact-grant consume path. It must
ignore empty-digest set grants, match the grant digest to the invocation digest and require the
approved parent request to carry that same digest and set/workspace. Revalidate the parent and
actual invocation as today; a CLI-only filter is insufficient because persisted rows are consumed
here. Do not clamp or rewrite parent `Policy`, grant expiry or other metadata to force once/15m:
the existing exact-Allow-once issuance already provides that validity, and existing BB-D99 exact
rows remain live across restart. Exclude only empty-digest GitHub set-wide rows; retain the shared
store's default selector for other sources. Keep lock ordering, durable spend and required audit
before dispatch. Add source-named tests in [`internal/brokerstore/store_test.go`](../../internal/brokerstore/store_test.go)
for exact/non-exact selection, parent/invocation mismatch, concurrent spend and expiry/restart; keep
existing tests proving non-GitHub set grants still work. Rewrite
[`internal/ghbroker/window_test.go`](../../internal/ghbroker/window_test.go)'s broad timed-grant cases
for GitHub exact-only spend; preserve generic timed-grant coverage in brokerstore for other sources.

### 3. Bind required store audit to the exact action and use

Update [`internal/brokercore/audit.go`](../../internal/brokercore/audit.go) and the narrow
[`internal/brokeraudit`](../../internal/brokeraudit) event shape only as required so request,
decision and `grant-used` records carry canonical argv/repository, command digest, stdin SHA-256,
request ID, grant ID and outcome. Never persist stdin bytes, tokens, auth headers or a substitute
secret. Keep transaction-required audit failure fail-closed and do not refund a spent use. Add tests
that show a consumed grant-use record links to its exact approved request/action and that stdin
mutation changes the digest without logging the body.

### 4. Wire actual GitHub request delivery and decision surfaces

In [`internal/ghbroker/daemon.go`](../../internal/ghbroker/daemon.go) / `newBroker`, install the
production `RequiredDelivery`. Its readiness requires the real background log and host CLI path,
consistent with BB-D98; an `on_request` hook is optional. Use the existing host command as one request
affordance when configured. It may call host `yolo approve`; only the store decision written through
that CLI is consent, never hook output or exit status. Keep `brokercore.StartOnRequest` as the
execution path and report hook failure; request/log failure never dispatches. Update the inaccurate
[`internal/ghbroker/onrequest_test.go`](../../internal/ghbroker/onrequest_test.go) claim that hooks
only notify: the configured trusted command may invoke `yolo approve`, but hook output alone never
changes stored status. Do not edit only the fake fixture.

In [`internal/cli/approve.go`](../../internal/cli/approve.go), reject `--for` and `--add` for pending
GitHub writes with the plain `yolo approve <id>` Allow-once next step. Do not alter these flags for
other broker sources, fabricate a timed/Add grant or silently coerce them. Refresh stale usage,
empty-state and decision messages so a user can distinguish a pending request from an old
set-wide grant. Tests must pin source-specific refusal and unchanged other-source behavior.

### 5. Prove the complete production caller and approval behavior

Finish the new CLI tests so the real front/daemon/host CLI flow exercises the optional configured
hook, proves hook output alone is not approval, then approves both actions via `cli.Main`, checks the
exact fake-`gh` argv/receipt and correlated request/grant/action audit. Add production-path refusal
checks for no approval, deny, expiry/revocation, changed argv/repository/stdin, legacy empty-digest
GitHub grants, standing `read-write` policy, required audit failure and concurrent one-use calls.
Verify an existing unspent exact grant remains valid across restart and spend prevents replay. Do
not use a private executor or delivery mock to claim production wiring; callee-only unit tests
complement, never replace, this caller proof.

### 6. Update source-facing explanations without changing policy authority

Refresh [`internal/cli/gh.go`](../../internal/cli/gh.go),
[`internal/cli/help.go`](../../internal/cli/help.go),
[`packs/github/briefing/gh.md`](../../packs/github/briefing/gh.md),
[`packs/github/README.md`](../../packs/github/README.md),
[`userguide/guides/github.md`](../../userguide/guides/github.md),
[`packs/github/loopholes/github-broker/manifest.jsonc`](../../packs/github/loopholes/github-broker/loophole.toml),
[`internal/ghbroker/daemon_test.go`](../../internal/ghbroker/daemon_test.go),
[`internal/ghbroker/onrequest_test.go`](../../internal/ghbroker/onrequest_test.go), and the existing
read-only integration fixture [`integration/githubbroker_test.go`](../../integration/githubbroker_test.go):
refresh the intro/table in `userguide/guides/github.md` and the intro/exit-77 row in
`packs/github/README.md`; remove the claim that all writes immediately exit 77 or that `host-run` is
the approval path; do
not make the read-only regression test wait 30 seconds for an unapproved write. The integration
fixture may retain read/refusal coverage; production approved writes are proven by the actual
consumer-path test. Update only the implementation/status table and stale shipping-state paragraphs
in [`docs/design/boundary-broker.md`](boundary-broker.md), not its settled policy or open BB-D76/79
questions. Review every source-named statement with `rg` and the implemented path.

### 7. Validate the shipped caller and scoped change

Run `just format`, the affected package tests (`go test -short ./internal/ghbroker ./internal/brokerstore ./internal/brokercore ./internal/cli`), and `just check-ci`. Because the repaired path includes daemon startup/launch behavior, run the **full** integration suite once on the final combined tree (`just test`, which includes `go test -count=1 -timeout 0 ./integration`), not only a symbol-filtered subset. If changed repo-reader tests read outside their package, rerun `python3 scripts/completion-census.py` and commit its output.

For the mandatory nested check of the `internal/` change, run `just build-dev-bundle` and invoke its emitted launcher by absolute path from a fresh workspace under `/workspace/.yolo/durable/github-write-approval-item5/stage2-nested-workspace`. The current host preflight is `podman info --format '{{.Host.Security.Rootless}}'` = `false` and `podman info --format '{{.Host.RootlessNetworkCmd}}'` = `slirp4netns`; this nested jail proves launcher/production wiring only, not a rootless-only or reachability path. Use fake host `gh`, inspect but do not override the owner Go cache, and never edit or write main.

Before final combined-tree gates, merge the exact root-authorized docs-only commit `ed4aed903415bb4e67c61203dc742d6181384bd6` (or a later root-provided docs ref) into the private branch by normal ancestry, without rebasing or rewriting Stage 1. After all coherent commits, ensure the worktree is clean and run `just done`.

## Proof matrix

| Claim | Proof required |
| :--- | :--- |
| Removing production delivery fails the caller test | Delete only production `RequiredDelivery` wiring in a disposable verification copy or prove the test fails at readiness; retain no destructive mutation in this worktree. |
| Approval authorizes only this action | Real `Forward` through front and `ghbroker.Main`; host `cli.Main` allows it; fake `gh` records byte-exact canonical argv and stdin digest; required audit includes request/grant/action linkage. |
| Standing writes cannot take the standing branch | Actual daemon + temporary host policy has `read-write: standing`; read passes; write gets explicit actionable refusal, no request/dispatch; existing unsafe scope-removal transaction test remains meaningful for approved windowed write. |
| Shared store is safe without regressions | Empty digest does not cover GitHub exact path; existing exact-grant rows remain valid under unchanged parent metadata after restart; other sources still consume intended set grants; concurrent one-use spend is serialized. |
| Failure, replay and restart are truthful | Required failure never executes; an approved unspent exact grant retries after restart once; spent, denied, expired and revoked states never execute. |

## Current proof-matrix status

This bounded final refinement starts from the exact private successor candidate
`417dc70de05fa26ecccfcee3585172c9b67b2b2e` and changes only tests and evidence. The frozen independent
review of original candidate `f94aa9b13534b998aa3d77200a4488369b3adf74`
remains **BLOCK** as historical evidence; its RED report and findings remain preserved. The confirmed
findings were repaired in descendants and checked by the lead against the production diff and actual
normal/race tests. The only later production delta was bounded request-summary formatting/redaction;
Root policy treated that as minor, so this test/documentation-only successor does not request another
independent reviewer. No new independent review is claimed. Root retains private-candidate inspection
and landing authority.

- Production `Forward`/front/daemon/host-CLI coverage remains the exact PR comment and issue-close
  path, with NUL-delimited argv receipts, actual stdin bytes/digests, pending no-dispatch, host approval,
  required audit links, body/target mismatch without spending, expiry/revoke, restart and workspace / repo
  fences, audit-append failure, and concurrent one-use callers. The final test refinement adds live
  `approve --watch` output for two real forwarded `--body-file -` requests and strengthens applicable
  lifecycle receipt, stdin-hash and linked-audit assertions. Fake host `gh` is the only receiver; no
  real GitHub operation or credential use occurred.
- Targeted normal/race verification for this final test delta and `just check-ci` are captured in the
  [QA evidence](../research/github-write-approval-qa.md). The final full integration suite passed on
  source/test candidate 417 in 766.416s; the runtime production source is unchanged by this refinement.
  The matching actual nested jail launch passed earlier at 398, whose only difference from 417 was
  documentation. No full integration or nested runtime rerun is needed for this test/docs-only delta.
- The nested run was rootful podman (`Rootless=false`, `slirp4netns`) and proves neither rootless-only
  nor host-loopback reachability behavior. Native macOS and live GitHub remain untested. Timed-resource
  and working-set Add remain unfinished; this is not a claim that the whole boundary broker is complete.

## Stage boundary

Stage 1 established the source audit, complete plan and caller-path RED baseline. The original `f94aa9b`
review BLOCK remains preserved, while the successor repairs are separately tied to the production path
and verified tests. The current work adds only caller-level assertions and evidence updates; it does not
change production behavior or policy. Keep Issue 65 open as an upstream wait. Do not change user config,
credentials, repository grants or main. Root inspects the private candidate and controls any eventual
Primary landing.
