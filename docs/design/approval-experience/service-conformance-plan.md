---
title: "Service conformance: bounded implementation hand-off"
status: accepted
stage: BUILT
next: "Independent review and root combined census/landing gates"
depends-on:
  - service-conformance.md
tags: [plan, testing, broker, packs]
---

# Service conformance: bounded implementation hand-off

**Status:** 2026-11-10. Written against `62b8bbda375acda7caa3f675a4844aa020e8b060`;
source seams and baseline command below verified. Implemented locally on 2026-11-11
from `017ea6df9`; focused gates pass. Independent review and root landing gates remain.
**Design:** [service-conformance.md](service-conformance.md).

Precedence: design wins on behavior; tree wins on cosmetic fact; this plan is advice unless
marked **must**. Stop/report non-cosmetic disagreement, a dirty fenced file, a missing named
symbol, a required production edit, or an out-of-scope gate failure. Do not diagnose a new
service or invent a new prerequisite. This is one serial implementation task, not fan-out.

## Map and edit fence

| Path | Work |
| :--- | :--- |
| `internal/callbrokerconformance/kit.go` | New: test-only classification/step checkers; no callbroker import |
| `internal/callbrokerconformance/kit_test.go` | New: checker self-tests including deliberately broken adapters |
| `internal/callbroker/conformance_test.go` | New: shared lifecycle table and adapter observations/approval helper |
| `internal/callbroker/conformance_s3_test.go` | New: S3 table and real mediation adapter |
| `internal/callbroker/conformance_synthetic_test.go` | New: test-only tasks table and negative controls |
| These two conformance design/plan docs | Update bounded implementation/evidence status after tests |

**Must:** only these new source files and owned docs. No rewrite of existing S3/core tests,
private-cache fix, fixture, production code, manifests, dispatch or CLI. Do not edit the
umbrella, service-mediation/history/browser/native-gallery/Linux-capture/roadmap/S3-D5 docs.
Tier-3 edit fence: verify the final diff explicitly; root review is the independent backstop.
Report new cross-package file readers; root owns combined completion census and full gate.

## Reuse before writing

- `internal/callbroker/s3_e2e_test.go`: `newS3E2E(t)` already builds yolo/fake host aws,
  isolates execution HOME, loads shipped declarations through selection/pack loading, writes
  live scope/grants/input, binds the lease, generates the shim and starts the authenticated
  front. Reuse unchanged. `f.aws`, `f.host`, `f.pending`, `f.requests`, `f.server.Object`,
  `f.server.Writes`, `f.store.Snapshot` give observations. `f.h.classifier.ClassifyCLI` invokes
  the actual selected classifier subprocess for the classification table.
- `internal/callbroker/once_test.go`: `selectedOnceFixture(t)` creates the selected synthetic
  tasks pack, live handler/store/lease and host CLI. `f.h.serveCLI`/`f.call`, `f.effects`,
  `f.pending` (all requests, **filter State == pending**), `f.userPath` and `f.store` are enough.
  Use `update-pr` for a no-lookup write; `read` for the read. Existing `update a1` exercises
  declared read lookup; its read effects are legitimate and separate from write counts.
- `internal/callbroker/classifier_test.go`: `fixtureClassifier(t, body)` supplies a real
  checksum-pinned executable emitting a chosen protocol answer. `fixtureAnswer` shows the
  JSON shape. For malicious answers, copy the synthetic handler's Classifier, replace ONLY
  Argv/Identities with the fixture executable's, preserving the handler's schema/root/cells.
  Then exercise `serveCLI` as well as `ClassifyCLI`; never call private `validate` directly.
- `internal/callbroker/decision_handler_test.go`: `callBrokerTestGoTool`, `callBrokerTestBuild`
  and `callBrokerBuildEnv` retain original HOME/Go overrides. `YOLO_TEST_MATCHED_YOLO` is the
  existing optional supplied binary. Build a missing approver via `callBrokerTestBuild`, not
  bare `go build` after fixture HOME changes. Synthetic host approval is `exec.Command(yolo,
  "approve", pendingID)` with explicit HOME/XDG_CONFIG_HOME/PATH/USER and fixture cwd, matching
  `TestSelectedCLIForwarderHostApproveOnceReportsCompletedOversizedWrite`.
- `once_expiry_test.go`: inject `f.store.Now = func() time.Time { return grant.Expires.Add(time.Second) }`;
  `brokerstore.IsLapsedAllow` distinguishes expired refusal. No sleep.
- `once_identity_test.go`, `once_test.go`: changed input cannot spend a grant; current host
  permission withdrawal refuses before execution; completed oversized write spends its grant
  and must not expose `OVER_CAP_PRIVATE_SENTINEL` or partial response bytes.

## Shared package API (must)

These types are test observations, not a new broker protocol. Export the following in
`internal/callbrokerconformance`; use stdlib only. Implementation of comparisons is cheap
and the worker's; JSON answer comparison must preserve array order and reject invalid JSON.

```go
type Call struct { Argv []string; Stdin []byte }
type ClassificationCase struct {
    Name string
    Call Call
    Want json.RawMessage
    ErrorContains string
}
type Classify func(context.Context, Call) (json.RawMessage, error)
func CheckClassifications(context.Context, Classify, []ClassificationCase) error

type Counts struct { Writes, Pending, GrantUses int }
type Observation struct { Verdict, Output string; Counts Counts }
type Step struct {
    Name string
    Run func(context.Context) (Observation, error)
    WantVerdict string
    WantCounts Counts
    ErrorContains string
    OutputContains, OutputExcludes []string
}
func CheckSteps(context.Context, []Step) error
```

**Must:** error includes row/step name; nil action/classifier, empty table/script/name,
malformed expectation and ambiguous success/error expectations fail closed. Classification
runs twice, matches expected JSON/error and determinism; steps execute once in order and
stop on first mismatch. Success requires nil error; expected failure requires a nonnil error
containing the specified text. Counts compare all fields exactly. Output uses required and
forbidden substrings; no implicit normalization of credential-containing output.

## Task — implement and demonstrate (one coherent code commit)

**Settled, do not re-open:** shared assertions only; S3 plus test-only tasks; production
classify/approval/executor callers; no real AWS; partial [§19](../pack-brokers.md#19-what-done-looks-like) coverage only.

**Preconditions:** clean tree after the plan commit; baseline test below passes; optional
matched yolo must be built from this candidate. Existing helpers are read-only.

**Tests first:** the following is an exact minimal red test in `kit_test.go` before writing
`kit.go`. Add checker tests for changed canonical JSON, changed array order, negative errors,
wrong counts/verdict, error-with-success, output leaks and empty/malformed input, plus a valid
success. This catches a runner that compares two equally wrong answers or skips callbacks.

```go
package callbrokerconformance

import (
    "context"
    "encoding/json"
    "strings"
    "testing"
)

func TestCheckClassificationsRejectsNondeterminism(t *testing.T) {
    calls := 0
    classify := func(context.Context, Call) (json.RawMessage, error) {
        calls++
        if calls == 1 { return json.RawMessage(`{"argv":["read","a"]}`), nil }
        return json.RawMessage(`{"argv":["read","b"]}`), nil
    }
    err := CheckClassifications(context.Background(), classify, []ClassificationCase{
        {Name: "R1", Call: Call{Argv: []string{"read", "a"}}, Want: json.RawMessage(`{"argv":["read","a"]}`)},
    })
    if err == nil || !strings.Contains(err.Error(), "R1") || calls != 2 {
        t.Fatalf("drift accepted: err=%v calls=%d", err, calls)
    }
}
```

Red command: `go test ./internal/callbrokerconformance` (undefined kit symbols).

**Classification coverage through actual core subprocess caller:** service tables supply
explicit expected `Answer` JSON (marshal manually authored `Answer` values; never derive
expectations from a classifier invocation). S3 R1/R2/R3/W1/W2; R1 recursive-order variant;
R3 `--key=team/a.txt --bucket=bkt` normalization; scope/unsupported/credential refusals;
longest covering root using a copied Classifier with roots `s3://bkt` and `s3://bkt/team/`;
malformed root `s3://bkt/team` alone covers nothing. Synthetic `read` and `update-pr` have
`project team:alpha` and `pr #1 < project team:alpha`, cell `change`. Exercise `update a1`
with real `lookupWithClassifier` to prove this declared lookup is admitted as a read; do not
claim exhaustive arbitrary lookup coverage.

**Shared lifecycle table:** implement once in `conformance_test.go` with small callback
adapter, not separate copy/pasted sequences. It is run for S3 and tasks. Exact ordering:
read act runs; write evaluate asks without pending/effect; ask creates one pending; duplicate
ask remains one; built host approve grants one use without effect; evaluate runs without
spending; changed stdin act asks without spending the original use; original exact act runs,
writes once and consumes use; replay original act asks without another write. The expected
`Counts{Writes, Pending, GrantUses}` sequence is `{0,0,0}` (read/evaluate), `{0,1,0}`
(ask/duplicate ask), `{0,0,1}` (approve/approved evaluate), `{0,1,1}` (changed stdin),
`{1,1,0}` (exact act), `{1,2,0}` (replay). Snapshot counts must come from stored records:
filter pending state and sum recorded exact-grant Uses; never fill counters from this table.
The approval step's observation verdict is `approved` only after the CLI exits successfully.
Handler observations combine stdout/stderr and `Verdict.Next` for diagnostics; shim
observations map actual rc 0/75/64 to runs/asks/refused and retain actual output. Any other
exit/error must fail rather than be coerced to the expected verdict. Use W1
`hello\n`/`different\n` and synthetic `update-pr` with the same bodies. Read output is fixture
alpha for S3 and `{"ok":true}` for tasks. Verify exact stdin bytes/object and no synthetic
secret in all returned output. S3 act goes through `f.aws` shim; evaluate/ask can use the
selected handler's `serveCLI` directly. Synthetic acts use `serveCLI`; host approval still
uses the actual built CLI. Report this path difference, never call it a second front proof.

**Controls (fresh fixture per destructive condition):**

| Case | Caller/expected evidence |
| :--- | :--- |
| Global `--endpoint-url http://127.0.0.1:1`, unsupported `s3 sync`, uncovered bucket, malformed name | S3 shim rc 64; request log, writes and pending count unchanged; class checked by classification table |
| Empty canonical argv / canonical NUL / changed returned stdin digest | Synthetic malicious subprocess answer through `ClassifyCLI` twice and handler act; named core error; no write/pending/grant |
| Undeclared resource type / repeated type / wrong parent / uncovered top root `team:gamma` | Same actual caller; core-validation refusal and no effects; preserve valid base chain for each independent mutation |
| Expired exact approval | Actual host approve first; advance only store clock; act refused with lapsed-allow diagnostic; original grant not spent; no writes |
| Withdraw write ceiling after approval | Change only fixture user config `all.change` to `all.blocked` (tasks), or remove `all.put_objects` (S3); handler/shim act refuses, use retained, no writes; also host approve on a pending call after withdrawal must fail |
| Oversized classifier response | Pinned synthetic subprocess emits 65537 bytes; actual ClassifyCLI and handler refuse; no writes/pending |
| Completed oversized executor response | Tasks body `oversized-response\n`; host approve then handler act invokes real executor; completed-write error, writes 1, uses 0, no partial/sentinel output; next step contains inspect-host-state guidance; replay asks, writes still 1 |

For completed oversized response also assert audit outcome `ran`, reason
`host-cli-response-rejected`, and no original stdin/sentinel in audit. Inspect the actual
returned error (`completedWriteResponse`) rather than expecting an ordinary safe refusal.
S3 refusal actions **must** compare fixture request-log count before/after; writes alone are
insufficient. Synthetic negative outputs exercise schema safety, not S3 signing compatibility.

**Gates:**

```sh
gofmt -w internal/callbrokerconformance/*.go internal/callbroker/conformance*_test.go
go test ./internal/callbrokerconformance
go test -short -run 'TestConformance' ./internal/callbroker
go test -short -run 'TestS3ShippedPackThroughRealMediation|TestS3FixtureBuildsOutsideCredentialHome|TestCallBrokerTestBuildPreservesOwnerGoOverrides|TestSelectedCLIExactOnceExpiresOrIsRevokedWithoutHostEffect' ./internal/callbroker
GOOS=darwin go vet ./internal/callbrokerconformance ./internal/callbroker
git diff --check
git diff --cached --name-only
```

No full gate/census here. No new nested launch needed for test-only additions; do not claim
nested verification of the kit. Test adapters read shipped S3 files and fixture-created
selected-pack config/declarations through existing helpers: report these cross-package
readers to root for the combined census. Do not stage unrelated files. Commit intended files
then leave no staged/dirty files; root handles review and landing.

**Commit:** `test(callbroker): reusable bounded service conformance kit`.
**Report:** SHA, files/stat, red and green command results, per-case coverage and actual
consumer paths, remaining [§19](../pack-brokers.md#19-what-done-looks-like) limitations, cross-package readers, no-staged-files evidence.

## Baseline evidence

Passed at the written-against commit:

```sh
go test -short -run 'TestPackCLIClassifierValidatesCanonicalResourceAndDoesNotExecute|TestSelectedCLIExactOnceExpiresOrIsRevokedWithoutHostEffect|TestSelectedCLIExactOnceFilesOnceAndSpendsOnOnlyOneActualCall|TestS3ShippedPackThroughRealMediation' ./internal/callbroker
```

Result: `ok github.com/mschulkind-oss/yolo-jail/internal/callbroker 11.848s`.
This establishes existing contracts only, not proof of the unbuilt kit.


## Implementation evidence

The five fenced Go files implement the exact kit API, shared lifecycle and two adapters.
Existing helpers, S3/core fixtures, production code and declarations are unchanged.

| Check | Actual result / consumer path |
| :--- | :--- |
| Checker red | `go test ./internal/callbrokerconformance` failed on undefined kit symbols before `kit.go` existed |
| Additional checker red | Missing step verdict and success-plus-error expectations ran callbacks; exact large-number comparison accepted two different JSON numbers; regressions failed before fixes |
| Checker green | `go test ./internal/callbrokerconformance` passed: JSON value/array ordering, exact numbers, determinism, negative errors, missing/malformed expectations, step counts/verdict/errors/leaks/order |
| S3 rows | `TestConformanceS3Classifications` passed R1/R2/R3/W1/W2, recursive/equals normalization, four refusal answers, longest/malformed roots through actual `ClassifyCLI` subprocesses |
| Shared lifecycle | `TestConformanceS3Lifecycle` and `TestConformanceSyntheticLifecycle` passed one shared nine-step table; counts come from pending-state records and grant uses, effects from actual fixtures |
| S3 boundary | `TestConformanceS3ShimRefusals` passed endpoint/sync/uncovered-bucket/malformed-name controls through generated shim/front; request log, writes and pending stay unchanged |
| Tasks boundary | `TestConformanceSyntheticClassifications` passed read, update-pr and update-a1 with actual declared read lookup and read audit; no write or request filed |
| Malicious classifier | `TestConformanceSyntheticInvalidAnswers` passed empty argv/NUL/digest/resource/repeated-type/parent/uncovered-root/schema/65537-byte controls through pinned subprocess twice, then handler act; zero effects/requests/grants |
| Stale approval | `TestConformanceSyntheticStaleApproval` passed actual host approval then fake-clock expiry/ceiling withdrawal, retaining one use; approval after withdrawal fails |
| Completed response | `TestConformanceSyntheticCompletedOversizedResponse` passed actual executor write, completed-write error, spent grant/request, withheld partial output, inspect-host-state guidance, `ran` audit and replay asking |

The focused consumer command `go test -short -run 'TestConformance' ./internal/callbroker`
passed. The planned existing S3/build-environment/expiry regression selection passed;
`GOOS=darwin go vet ./internal/callbrokerconformance ./internal/callbroker` passed.
`just format` and `git diff --check` passed. No full gate, completion census, nested
launch, native-host run or live AWS call was performed in this task.

**Actual path difference:** S3 acts use the generated shim and authenticated front;
S3 evaluate/ask use the selected handler. Tasks use the actual handler entry, not a
second front, and both approval adapters invoke the built host `yolo approve`.
Neither adapter infers an observation from expected counts. Completed output rejection
is evidence of a write that occurred, not a safe no-effect refusal.

**Approved factual correction:** the existing tasks fixture covers both `team:alpha`
and `team:beta`. The uncovered-root control therefore uses `team:gamma`, with an
explicit fixture-root precondition, preserving the fixture unchanged. The repeated-type
chain is rejected first by the parent-chain check; it is not independent proof of a
later duplicate-type branch. Unknown schema fields produce the actual strict-JSON
error. These two error-substring assertions initially failed, then passed with the
actual caller diagnostics; no production behavior was changed to make them green.

**New cross-package readers for root's combined census:** the S3 conformance tests reuse
`newS3E2E`, which reads `packs/aws-s3` to generate the shim and loads its selected
loophole declaration, and builds `cmd/yolo` and `internal/s3fixture/cmd/s3fixture`.
The tasks conformance tests reuse the selected fixture's generated pack/config/input
and `internal/callbroker/testdata/classifier`; their approver helper builds `cmd/yolo`
through unchanged `callBrokerTestBuild` and its original build environment/shared caches.
No new production reader or arbitrary pack-discovery mechanism was added.

**Remaining [§19](../pack-brokers.md#19-what-done-looks-like) limits:** no standalone author command, arbitrary-pack discovery,
every-pack block-loading report, exhaustive lookup proof, second real service,
HTTP-front proof, new nested/native-host proof or native AWS compatibility evidence.
