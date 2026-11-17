---
title: "AWS operation broker: tree-grounded build hand-off"
date: 2026-11-16
status: accepted
stage: DECIDED
next: "Review the isolated candidate and run the combined landing gates once"
depends-on:
  - aws-operation-broker.md
  - pack-brokers.md
  - approval-experience/service-mediation.md
tags: [plan, aws, broker, packs, cost-explorer]
summary: "Four serial, bounded implementation units: direct identity replacement, pure Cost Explorer classification, synthetic production-path proof, then nested proof and current documentation. No auth or core-policy changes."
---

# AWS operation broker: tree-grounded build hand-off

**Status:** 2026-11-17. All four units are implemented in an isolated candidate, not yet accepted or landed. Permanent dispatch and CE rows failed before their implementations; shipped shim→front→classifier→core→fake-host CE failed at the old fixture (252) before the independent CE fixture was added. Focused package gates and all three reached nested AWS tests pass. Dispatch and full-check callsite deletion controls fail and restoration passes. Combined census/check-ci/full integration/clean `just done`, native Mac/rootless-host and live AWS remain separate gates. Written against clean `b5a5fe32a326109371656162623b690a3c790427`; allocation was verified at clean `a85e2734a579e9eab2f7a340ba3200faa2a51f06`.
**Design:** [canonical contract](aws-operation-broker.md), including the request-count,
exact-grammar and nested-value amendments. **Evidence:** [source and CLI packet](../research/aws-operation-broker-grounding.md).
The original S3 contract is baseline evidence, not authority to retain the old pack name.

## Precedence, preflight and fences

**Constraint:** the canonical design wins on behavior; the tree wins on cosmetic fact;
this plan is advice and the first thing to be wrong. Do not twist code to fit a stale map.
Before each task, verify actual cwd/HEAD and clean state; compare its affected seams with the
written-against commit. Work in a new managed isolated worktree, never mutate the primary checkout.

**Stop and report without committing:** a named symbol is gone, an affected file is dirty
with someone else's work, a red test does not fail for the stated reason, an edit needs an
out-of-fence file, a source fact contradicts the policy, or a gate fails outside this work.
No reset or branch switch to recover an assumed tree. Tasks are serial; no parallel writers share these files.

**Tier-3 task fence (review-enforced):** source/docs/tests only. Do not edit any `aws-auth`
manifest/source/derive/settings, the separate workspace Git-skills feature or its candidate,
credential/IAM/live grant/config state, core broker policy/protocol/executor/redaction,
image/build delivery, global caches or the changelog. No real AWS request, STS/SSO/auth probe,
CLI install, deployment or live credential read. All test accounts, configs and keys are synthetic.
Core changes discovered to be necessary require a new decision, not a broadened task.

## Settled behavior — do not re-open

- Replace pack `aws-s3` with `aws`, loophole `aws-s3-broker` with `aws-broker`, source and
  classifier dispatch word with `aws`. No alias, `formerly`, migration, wrapper or compatibility.
  Pack `aws` still intercepts the whole command `aws`; there is no service-specific executable.
- Every admitted call requires pack `read` plus a covering declared root for its service plus
  one exact row. Roots live at workspace `brokered.aws.roots`, supported only in legacy JSONC in this tree; user TOML `[allow.aws]` works independently. Workspace TOML refuses `brokered` and has no root replacement. Do not invent `requests.aws.roots`, claim migration preserves roots or treat JSONC proof as TOML proof. This is a disclosed usability gap, pending author disposition; no generic schema change has been made. S3 sees only valid `s3://` roots, CE only the exact `ce://host-login`. Other schemes/malformed roots cover nothing.
  Core checks the top resource id against the gate-approved roots verbatim.
- `location` remains parentless; S3 leaf types remain `prefix`/`object`. CE adds `cost_query`
  under `location`, id `ce://host-login/<operation>`. CE is reads only, has no approval cell.
- S3's three reads, two writes, name grammar, longest root, descriptions, stdin binding,
  3 MiB stdin cap, Allow once, put `working_set`/`all` with `1h` cap and delete `all` with
  default 15-minute cap remain unchanged. Timed answers cover the cell/full resource, not a body.
- The pack `read` grant remains requestable by default, not implicitly granted. Write tokens
  now belong to `[allow.aws]`; old grants/tokens/requests/answers are never consulted for `aws`.
- Host `aws` remains pinned, running at `/` with host `PATH`, `HOME` and the ten existing
  AWS env names. No `AWS_ENDPOINT_URL_CE` or jail-supplied login/region/profile/account field.
  Account/billing visibility is host IAM's. Host configuration/credential helpers/plugins are trusted.
- At most one host process, no classifier lookups and no broker retry. `--no-paginate` requests
  one CE page, not one HTTP request: host retries and preserved S3 pagination remain untouched.
- Sensitive financial data and actual billable AWS requests are disclosed in pack description,
  loophole access and `read` text. No fixed price, cost meter, cache or retry override.
- Retarget only the full-check warning to selected `aws` plus explicit un-narrowed `aws-auth`
  with a profile; name S3 and CE, retain TOML/JSONC inspect-key behavior. No launch warning.
- Existing audit remains source `aws` with read/write/outcome/reason/digest, not service/operation.
  Existing 32 MiB per-stream cap and all-declared-variable redaction remain, including data distortion.
- Unknown service/operation is denied. Never infer safety from a `Get*` or `Describe*` prefix.
  No version/local command; no hourly/resource-level/billing-view/sort/product-attribute widening.

### Exact CE grammar to implement

Flags can arrive in any order, as `--x v` or `--x=v`, each at most once. Scalar options
consume one word; metrics/group-by consume one or more operands. No stray positional word.
Emit only present flags in the table's required-then-optional order; R6 always has context
once (last before the trailing globals). Append `--output json --no-paginate` to every CE row.

| Operation | Required, in canonical order | Optional, in canonical order |
| :--- | :--- | :--- |
| `get-cost-and-usage` | time-period, granularity, metrics | group-by, filter, next-page-token |
| `get-cost-forecast` | time-period, granularity, metric | filter, prediction-interval-level |
| `get-dimension-values` | time-period, dimension | search-string, filter, max-results, next-page-token, context |
| `get-tags` | time-period | tag-key, search-string, filter, max-results, next-page-token |

| Value | Fixed acceptance and canonical form |
| :--- | :--- |
| time-period | `Start=D,End=D` either input key order, or one JSON object exactly Start/End; real dates `YYYY-MM-DD`, years 2000–2099, Start < End; emit Start then End shorthand |
| granularity | `DAILY`, `MONTHLY` only |
| metrics | 1–7 distinct `AmortizedCost`, `BlendedCost`, `NetAmortizedCost`, `NetUnblendedCost`, `NormalizedUsageAmount`, `UnblendedCost`, `UsageQuantity`; emit this list order |
| metric | One of `AMORTIZED_COST`, `BLENDED_COST`, `NET_AMORTIZED_COST`, `NET_UNBLENDED_COST`, `UNBLENDED_COST` |
| dimension | Uppercase letter followed by 0–63 uppercase letters/digits/underscore; AWS handles actual dimension validity |
| group-by | 1–2 entries; word `Type=T,Key=K` in exactly that order, or one JSON array of exactly Type/Key objects, keys either order. No mixed forms/second JSON word/duplicate Type+Key pair. Type DIMENSION/TAG/COST_CATEGORY; dimension Key uses dimension grammar, other Key label; word Key has no comma/equal. Emit one JSON word, given entry order, Type then Key |
| label | 1–256 bytes of ASCII letters/digits/space and `_ . : / = + @ -`; no non-ASCII |
| search-string/tag-key | A label; raw and host value words are boundary-guarded |
| max-results | Decimal 1–1000, no leading zero; admitted but no effective cap promised without excluded SortBy |
| prediction-interval-level | Decimal 51–99 |
| next-page-token | 1–8192 bytes printable ASCII without spaces; boundary-guarded |
| context | `COST_AND_USAGE` only; injected for R6 when absent, never duplicated |

**Boundary rule (V-1 resolved):** reject raw option operand words beginning `-` or a URI
scheme (letters/digits/`+.-`, then `://`), including equals value and every list operand.
Canonical host value words satisfy the same guard. Do not mistake an option flag for its value.
Nested filter Values, Tags/CostCategories Key and group Key are data and may begin `-`/scheme.
Word `Type=TAG,Key=file://x` is accepted only because the host receives JSON, never shorthand.
Both-major published CLI source confirms this non-recursive expansion boundary; see
[V-1 proof](../research/aws-operation-broker-grounding.md#parameter-file-loading-and-request-counts).
The recursive fallback is not activated; the implementer is not choosing a policy. S3's `-`
stdin/stdout sentinel is not an option operand and remains supported.

**Filter:** one JSON object with exactly one node key: And/Or (array of ≥2 nodes), Not (one
node object), Dimensions/Tags/CostCategories (leaf object exactly Key/Values/optional MatchOptions).
Key uses dimension grammar for Dimensions, label otherwise. Values: 1–100 strings, each
0–256 bytes valid UTF-8 without control characters, empty string accepted, byte-identical
duplicates refused, preserve order. Empty or omitted Values refuses even with ABSENT (PC-1 closed).
MatchOptions absent stays omitted; present requires 1–8 distinct names from EQUALS, ABSENT,
STARTS_WITH, ENDS_WITH, CONTAINS, CASE_SENSITIVE, CASE_INSENSITIVE, GREATER_THAN_OR_EQUAL.
Emit this enum order; empty/duplicate/unknown refuses. Emit leaf keys Key/Values/MatchOptions.
And/Or child order stays; structurally equal children are accepted without deduplication.

**Strictness/bounds:** case-sensitive keys, correct JSON types, no unknown/duplicate key at any
object level, no trailing data in any JSON form. Received filter size 8192 bytes inclusive.
Logical depth root=1, each node child +1, leaf inner object/arrays do not count; max depth 5.
Count leaf nodes across the entire tree, max 50 inclusive, not Values length. All violations
refuse `unsupported`. JSON escaping alone is cheap/delegated: deterministic compact output,
round-trip-identical strings, no insignificant whitespace. Do not delegate any acceptance choice.

**Refusal precedence:** scan unknown `--` vocabulary first (`credential`), then row shape,
then value grammar, then scope. CE vocabulary is the union of the four rows; a known flag
on the wrong row is `unsupported`. Non-CE/unknown-service vocabulary stays the current S3 set.
Global endpoint/profile/region/signing/debug/output/query/file-input/pagination flags from the
jail refuse `credential`, never run. Malformed values refuse without echoing them; only safe
option names may be echoed. Each refusal names supported forms/host, host AWS settings or
`brokered.aws.roots` plus host `yolo config apply`, respectively.

## Map

The map retains the original replacement work, but its links now resolve to the candidate's successor paths. Package/file renames preserve one source spelling and the existing S3 functions/tests.

| Existing path | Work |
| :--- | :--- |
| [AWS pack](../../packs/aws/pack.toml) | Direct replacement of `packs/aws-s3`; new forwarding, CE/bypass/redaction disclosures |
| [AWS manifest](../../packs/aws/loopholes/aws-broker/loophole.toml) | New identity/text/classifier word and cost_query; unchanged env/path/cells |
| [Embedding](../../packs/embed.go) | Replace explicit `all:aws-s3` with `all:aws` |
| [Classifier](../../internal/awsclassify/classifier.go), [tests](../../internal/awsclassify/classifier_test.go) | Renamed package; same exported Run/Classify contracts and S3 helpers; CE grammar in `ce.go`, `ce_values.go`, `ce_filter.go`, source-named tests |
| [Internal CLI](../../internal/cli/internal.go), [caller tests](../../internal/cli/aws_internal_test.go) | Import/dispatch awsclassify on `aws`, remove old source; rename caller test file `aws_internal_test.go` |
| [Warning](../../internal/cli/check/awsbypass.go), [tests](../../internal/cli/check/awsbypass_test.go), [caller](../../internal/cli/check/check.go) | Rename files/symbol to AWS, retarget selection/text/call; preserve check-only and auth mode semantics |
| [Manifest table](../../internal/loopholedecl/shipped_test.go), [loader table](../../internal/loopholes/shipped_test.go) | Replace pack/loophole IDs |
| [Historical manifest fixture](../../internal/loopholedecl/testdata/legacy-shipped/aws-s3__aws-s3-broker.json), [equivalence test](../../internal/loopholedecl/shippedtoml_test.go) | Keep frozen JSON untouched. Test-only pairing maps this historical fixture to the new shipped module and names expected identity/text/cost_query deltas; no production alias or old module |
| [Grant comments](../../internal/config/loopholegrants.go), [readable comments](../../internal/config/readablescope.go), [cell tests](../../internal/config/loopholegrants_callbrokercell_test.go), [approval tests](../../internal/cli/approvepack_test.go) | Retarget examples/current fixtures; add old source isolation/rejection tests, no parser special case |
| [Config tests](../../internal/config/callbroker_test.go) | Current selected-source roots acceptance/new root gate and old source rejection; core readers unchanged |
| [S3 E2E](../../internal/callbroker/s3_e2e_test.go), [conformance](../../internal/callbroker/conformance_s3_test.go), [build env proof](../../internal/callbroker/s3_buildenv_test.go) | Retarget identity, preserve S3 lifecycle; share existing setup for new `ce_e2e_test.go` and `conformance_ce_test.go`; protect original build env |
| [Fake host CLI](../../internal/s3fixture/client.go), [service](../../internal/s3fixture/server.go), [fixture tests](../../internal/s3fixture/s3fixture_test.go), [fixture entry](../../internal/s3fixture/cmd/s3fixture/main.go) | Advice: extend existing test-only fixture with `ce_client.go`, `ce_server.go`, source-named tests and argv/invocation observations; keep S3 implementation/entry and independent parsers |
| [Nested proof](../../integration/aws_broker_test.go) | Rename pack-facing file/tests to AWS; retain S3 tests; add CE read/own-root refusal in `integration/aws_ce_broker_test.go` |
| [Current docs index](approval-experience/README.md), [S3 design](approval-experience/aws-s3.md), [S3 plan](approval-experience/aws-s3-plan.md) | Retire old current pack advice with a superseded status/link to canonical AWS contract; preserve S3 rows as baseline until incorporated; no product compatibility |
| [Recipe](approval-experience/service-mediation.md), [conformance](approval-experience/service-conformance.md), [conformance plan](approval-experience/service-conformance-plan.md), [broker design](pack-brokers.md), [broker plan](pack-brokers-plan.md) | Replace current pack/source/test names and false S3-only admission claims, repair inbound links |
| [Design](aws-operation-broker.md), this plan | Keep status evidence honest after builds; no BUILT stamp until measured |
| [Generated readers](../../scripts/completion-readers.json) | Combined landing regenerates after reader-test changes; do not hand-edit |

## Reuse before writing

- **Constraint:** retain `callbroker.CLIInput`, Answer/Classification/Resource types in
  [protocol](../../internal/callbroker/classifier.go). There is no parser field for region,
  profile, account or service; choose the table from argv[0], not a new protocol key.
- **Advice:** keep current `Run`, `read`, `write`, `refused`, `safe`, `checkBucket`, `checkKey`,
  `parseRoots`, `cover` from the [classifier](../../internal/awsclassify/classifier.go); they
  already bind stdin/resource chains and preserve S3 behavior. CE needs a separate exact-root check.
- **Advice:** mirror existing command option parsing for `--x=v` normalization but use explicit
  CE row vocabularies/list widths, not the broad S3 `commandFlags` as CE permission.
- **Constraint:** [jsonx.Decode](../../internal/jsonx/decode.go) overwrites duplicate keys;
  `encoding/json` map/struct decode and DisallowUnknownFields do not detect duplicates.
  Use a bounded token walk with per-object key tracking before typed interpretation of every
  JSON form. Advice: keep it local to awsclassify, because this is classifier policy, not config repair.
- **Advice:** reuse `newS3E2E`/`newS3E2EWithBuild`, `aws`, `host`, `pending`, `requests` in
  [E2E](../../internal/callbroker/s3_e2e_test.go): shipped selected pack, real shim/front/handler,
  built classifier, real approval store/host approve, pinned fake host aws. A generalized setup
  may add synthetic roots/fixture options; never substitute a handwritten manifest or classifier.
- **Constraint:** keep `callBrokerTestBuild` and `callBrokerTestGoTool` via the existing setup;
  [build-env proof](../../internal/callbroker/s3_buildenv_test.go) pins inherited shared Go caches
  before HOME is replaced. No task-private GOCACHE/GOMODCACHE or `/tmp` scratch.
- **Advice:** reuse `kit.CheckClassifications`, `checkConformanceLifecycle`, `conformanceClassify`
  from [S3 conformance](../../internal/callbroker/conformance_s3_test.go). CE conformance asserts
  no writes/requests filed and the core's chain validation, not a duplicated validator.
- **Advice:** reuse `newS3BrokerJail`, `hostYolo`, `runCommand`, `ghScript` in
  [nested proof](../../integration/aws_broker_test.go), adjusting only synthetic fixture roots.
  `requireJail(t)` remains; no t.Parallel. Nested proof does not prove native/rootless reachability.
- **Constraint:** keep the Check caller, not a direct warning-helper-only test. Existing
  [warning tests](../../internal/cli/check/awsbypass_test.go) ensure deleted callsites turn red.

## Traps

- The pack and source are different namespaces even when spelled the same: update selected
  pack, loophole forwarding/endpoint slug, source directory/store, grants and classifier word atomically.
- Adding a shared read grant cannot admit CE under an S3 chain. CE must end exactly at
  `ce://host-login`; `ce://other`, `ce://host-login/` and S3 roots are invisible to that table.
- A canonical list operand starting `[` must remain one argv word. Do not forward raw group
  shorthand, quote into a shell command, reparse a string with shell evaluation, or forward jail JSON bytes.
- Unknown global options must lose before shape/value/scope. A raw unknown `--` word cannot
  be absorbed as a metrics value or ignored by a failed service dispatch.
- Do not recursively reject `file://x` inside JSON after V-1 passed. Conversely, `--filter=file://x`
  is still raw URI loading, not JSON data; inspect the equals value before any host invocation.
- Date validity/range order is broker grammar; AWS retention/forecast limits and expression
  combinations are not new broker policy. Return errors/exit status through existing core redaction.
- MaxResults syntax exists on R6/R7 but does not promise a cap without denied SortBy. No repair
  by injecting a sort or accepting billing-view/product-attribute/dimension-key options.
- Synthetic request count proves the controlled fake only. It cannot assert one production HTTP
  request or a cost-per-call ceiling. Capture host process count and canonical argv separately.
- Existing S3 fixture readiness contains polling sleeps. New/changed waits must be event-driven;
  deadlines are generous failure guards, never elapsed-time pass criteria.
- A fake that imports the production classifier can share its escape bug. Fixture parsers stay
  independent, loopback-only and deny unknown forms before endpoint/environment access.
- Old requests/Allow once/timed answers must remain inert, not moved or cleaned automatically.
  Updating historical changelog entries or adding a rename migration violates the contract.
- The [TOML equivalence test](../../internal/loopholedecl/shippedtoml_test.go) requires one
  frozen JSON filename per current shipped module and exact decode equality. Merely renaming
  the pack breaks its key lookup/count even if all loader registry tests pass. Preserve the
  frozen fixture; scope the expected historical identity/declaration deltas to this test only.

## Task 1 — direct identity replacement (independent; one commit)

**Settled:** S3 behavior remains, but only the new pack/source/loophole identity exists. No compatibility.
**Preconditions:** shared preflight; current `runInternal`, S3 tests, manifests and registry rows exist.
**Tests first:** add this caller test to the current internal CLI test package (using existing helpers/imports):

```go
func TestRunInternalDispatchesAWSAndRejectsOldSource(t *testing.T) {
    input := callbroker.CLIInput{
        Version: "yolo.call-broker-classifier/1", Front: "cli",
        Argv: []string{"s3", "ls", "s3://bkt/x"},
        StdinSHA256: strings.Repeat("a", 64), Roots: []string{"s3://bkt"},
    }
    data, err := json.Marshal(input)
    if err != nil { t.Fatal(err) }
    pipeStdin(t, data)
    rc, out, errs := runMainCaptured(t, "internal", "call-broker-classify", "aws")
    if rc != 0 { t.Fatalf("aws dispatch rc=%d stderr=%q", rc, errs) }
    var a callbroker.Answer
    if err := json.Unmarshal([]byte(out), &a); err != nil { t.Fatal(err) }
    if a.Classification == nil || len(a.Classification.Reads) != 1 ||
        a.Classification.Reads[0][0].ID != "s3://bkt/x" { t.Fatalf("answer=%+v", a) }
    pipeStdin(t, data)
    rc, out, errs = runMainCaptured(t, "internal", "call-broker-classify", "aws-s3")
    if rc != 64 || out != "" || errs == "" {
        t.Fatalf("old source remains active: rc=%d out=%q err=%q", rc, out, errs)
    }
}
```

Red: `go test -short ./internal/cli -run '^TestRunInternalDispatchesAWSAndRejectsOldSource$'`
currently fails at new `aws` dispatch (64). Record the actual failure; no software test was run by the planner.
Also retarget current selection/embedding tests and assert old pack selection is unknown and old
`brokered.aws-s3.roots` has no selected declaration when only `aws` is selected. Reuse config
validation fixtures, no new special-case refusal or formerly entry. Put old approval records
in a synthetic old source store and prove selecting only `aws` never activates or spends them; do not delete records. **Source-fact correction:** generic `yolo approve --json` inventories historical stores as `removed`/`deny-only`; it may list inert old grant and timed-answer IDs, but these are not active `aws` requests or authority. `TestAWSNeverConsultsRetiredSourceRequestsOrAnswers` drives that actual caller and proves the selected/removed distinction, failed approval, no execution/replay and no old-grant spending. The original whole-inventory absence clause contradicted core's intentional removed-source inventory; no core/UI change or alias was introduced.

**Change:** atomic pack/loophole/package rename, embed, CLI caller/help, warning symbol/caller,
registry rows, current S3/grant/approval test fixtures. In the shipped TOML equivalence test,
map only the historical fixture key to `aws__aws-broker` before selecting its module; normalize
its expected pack-independent manifest name/source/classifier/text and add exactly the new
cost_query resource. Keep the historical file unchanged; assert other host env/path/cell fields
still match. This follows the existing explicit GitHub historical-delta precedent, not a loader
alias. Add CE-sensitive text and `cost_query`
resource declaration without admitting any CE row yet. The two write cells/env list stay byte-equivalent.
**Gates:** `just format`; `go test -short ./packs ./internal/awsclassify ./internal/cli ./internal/cli/check ./internal/config ./internal/loopholedecl ./internal/loopholes ./internal/callbroker`.
**Fences:** shared fence; no core semantic edits, no S3 grammar changes. Only identity/text/fixtures here.
**Commit:** `refactor(aws): replace aws-s3 pack and source directly`.
**Report:** exact red failure/new dispatch green, old selection/source/store rejection, S3 regression and warning results; `git diff --check` and no staged residue.

## Task 2 — pure CE table and canonical values (after 1; one commit)

**Settled:** the exact four rows/grammar above; CE requires its own root and is a read only.
**Preconditions:** Task 1 green; new package's Run/Classify/input/testDigest helpers are preserved.
**Tests first:** add the full production-Classify test below to the renamed classifier tests:

```go
func TestCECanonicalRowsRequireTheirOwnRoot(t *testing.T) {
    rows := []struct { op string; args, want []string }{
        {"get-cost-and-usage",
         []string{"--metrics", "UsageQuantity", "UnblendedCost", "--granularity=MONTHLY", "--time-period", "End=2026-02-01,Start=2026-01-01"},
         []string{"--time-period", "Start=2026-01-01,End=2026-02-01", "--granularity", "MONTHLY", "--metrics", "UnblendedCost", "UsageQuantity"}},
        {"get-cost-forecast",
         []string{"--metric", "UNBLENDED_COST", "--time-period", "Start=2026-01-01,End=2026-02-01", "--granularity", "DAILY"},
         []string{"--time-period", "Start=2026-01-01,End=2026-02-01", "--granularity", "DAILY", "--metric", "UNBLENDED_COST"}},
        {"get-dimension-values",
         []string{"--dimension", "SERVICE", "--time-period", `{"End":"2026-02-01","Start":"2026-01-01"}`},
         []string{"--time-period", "Start=2026-01-01,End=2026-02-01", "--dimension", "SERVICE", "--context", "COST_AND_USAGE"}},
        {"get-tags",
         []string{"--time-period=Start=2026-01-01,End=2026-02-01"},
         []string{"--time-period", "Start=2026-01-01,End=2026-02-01"}},
    }
    for _, row := range rows { t.Run(row.op, func(t *testing.T) {
        argv := append([]string{"ce", row.op}, row.args...)
        a := Classify(input([]string{"ce://host-login", "s3://bkt"}, argv...))
        if a.Classification == nil || a.Refused != nil || a.Local || len(a.Lookups) != 0 {
            t.Fatalf("not classified: %+v", a)
        }
        want := append(append([]string{"ce", row.op}, row.want...), "--output", "json", "--no-paginate")
        if !reflect.DeepEqual(a.Classification.Argv, want) { t.Fatalf("argv=%q want=%q", a.Classification.Argv, want) }
        chain := [][]callbroker.Resource{{res("cost_query", "ce://host-login/"+row.op), res("location", "ce://host-login")}}
        if !reflect.DeepEqual(a.Classification.Reads, chain) || len(a.Classification.Writes) != 0 || a.Classification.StdinSHA256 != testDigest {
            t.Fatalf("classification=%+v", a.Classification)
        }
        for _, roots := range [][]string{nil, {"s3://bkt"}, {"ce://other"}, {"ce://host-login/"}} {
            denied := Classify(input(roots, argv...))
            if denied.Classification != nil || denied.Refused == nil || denied.Refused.Class != "scope" {
                t.Fatalf("roots=%q answer=%+v", roots, denied)
            }
        }
    }) }
}
```

Red: `go test -short ./internal/awsclassify -run '^TestCECanonicalRowsRequireTheirOwnRoot$'`
after Task 1 fails because CE has no admitted table, not an API/network error.
Add source-named table tests for every grammar boundary in the settled section, especially
all JSON duplicates/wrong types/trailing data, byte/depth/leaf inclusivity, absent versus
empty MatchOptions, empty/duplicate Values, mixed/duplicate groups, arbitrary service/Get*/
CE write refusal, credential-before-shape-before-value-before-scope and safe refusal text.
Add subprocess caller coverage of a CE row to the CLI package; deleting its dispatch must turn red.

**Change:** service-first vocabulary/dispatch in Classify; separate pure CE option/value/filter
parsers; exact CE root/read builder. No host-side effects/lookup/account filter or input fields.
**Gates:** `just format`; `go test -short ./internal/awsclassify ./internal/cli ./internal/callbroker`;
`git diff --check`. Inspect every admitted row's canonical argv and chain, not just success.
**Fences:** shared fence; manifest change only if needed to finish the declared cost_query type;
no unrelated new AWS rows or S3 helper behavior changes.
**Commit:** `feat(aws): classify scoped read-only Cost Explorer queries`.
**Report:** red failure, canonical row/grammar/core-chain/caller green; V-1 boundary cases and PC-1 refusal remain explicit.

## Task 3 — independent synthetic proof through production (after 2; one commit)

**Settled:** one host invocation of canonical argv, root/grant isolation, no escapes or secret leak;
S3 approval/Allow once/stdin/timed lifecycle unchanged. Real AWS HTTP count is not asserted.
**Preconditions:** Tasks 1–2 green; existing E2E uses shipped selection and built classifier.
**Tests first:** in the callbroker test package, use the existing setup and scope APIs:

```go
func TestCEReadThroughShippedAWSMediation(t *testing.T) {
    f := newS3E2E(t)
    scope, err := brokerscope.ReadFile(f.h.scopePath)
    if err != nil { t.Fatal(err) }
    scope.Repos = append(scope.Repos, "ce://host-login")
    if _, err := brokerscope.Write(scope); err != nil { t.Fatal(err) }
    before := f.requests()
    rc, out := f.aws("", "ce", "get-tags", "--time-period", "Start=2026-01-01,End=2026-02-01")
    if rc != 0 || out != "{\"Tags\":[\"fixture\"]}\n" {
        t.Fatalf("CE mediation rc=%d output=%q", rc, out)
    }
    if f.requests() != before+1 { t.Fatal("controlled fake did not make one request") }
    if len(f.pending()) != 0 { t.Fatal("read filed an approval request") }
}
```

Red: `go test -short ./internal/callbroker -run '^TestCEReadThroughShippedAWSMediation$'`
after Task 2 fails because the existing fake host aws has no CE implementation. This synthetic
scope write is a test fixture adjustment, not config-apply production proof; Task 4 supplies that.
If it instead fails authority/classifier before reaching the fake, stop and report the assumption.

**Change:** extend independent test fixture CLI/service for exactly R4–R7 canonical argv,
fixed synthetic JSON responses (get-tags returns the literal above), explicit next-token page,
no real network/non-loopback endpoint or credential-file access. Advice: separate CE source
files in the existing fixture package avoid changing the S3 service/client grammar. Use a
host-wide synthetic `AWS_ENDPOINT_URL` in E2E setup alongside unchanged S3-specific override;
this is already in the production host-env declaration. Set it before pinCLI captures env.
Do not add a production CE endpoint variable. Keep fixture parsing independent from awsclassify.

Add deterministic host-invocation/argv observations to the fixture (new test-only shape;
no assumed existing LoggedRequest argv/body fields). Record canonical argv, never synthetic
secret headers. Assert invocation count separately from service request log. Serve locally,
reject unknown forms before effects; use event notifications/channel/connection readiness,
including touched readiness waits. Preserve cache/build-env proof and cleanup.

**Additional tests:** all four reads/exact JSON/canonical args; S3-only roots deny every CE row;
CE-only root denies S3; withdrawn read grant denies both; live root removal denies next CE;
request/approval/timed records under old source authorize nothing; no automatic pending CE
request. Raw profile/region/endpoint/debug/signing/file/JSON-input globals refuse, raw URI/dash
filter/list/direct-string values refuse; fixture logs stay empty. Nested `file://x`/`-x`/`https://x`
data and group word/JSON forms are accepted only as one canonical JSON argv word. Test host
output/exit preservation with synthetic errors, output redaction, classifier input containing
no synthetic secret, and unchanged S3 lifecycle/timed caps. Use conformance kit against built
classifier to validate chains and refusal classes; never substitute a handwritten classifier.

**Gates:** `just format`; `go test -short ./internal/s3fixture ./internal/callbroker ./internal/cli ./internal/config`;
`git diff --check`. Run source-reader census only at the combined landing owner's request.
**Fences:** shared fence; fixture code never imported by shipped production code and no shipped binary added.
**Commit:** `test(aws): prove Cost Explorer through synthetic production mediation`.
**Report:** actual red, green full-path proof, exact canonical argv/process counts, no-execution logs,
secret/cleanup/build-env observations; mark real CLI/IAM/billing and native Mac unverified.

## Task 4 — nested proof and current guidance (after 3; one commit)

**Settled:** nested CE read/own-root refusal and preserved S3 write/timed cases; current guidance
uses aws; historical changelog/release evidence is untouched.
**Preconditions:** Tasks 1–3 green; new managed workspace can run matched emitted launcher.
**Tests first:** add `TestAWSBrokerCostExplorerRequiresItsOwnRoot` to the integration package,
using the current requireJail/runCommand/newS3BrokerJail setup, synthetic host fake, approved
workspace config and no agent/program readiness. First with only S3 roots run exactly:

```bash
aws ce get-tags --time-period Start=2026-01-01,End=2026-02-01
```

Expect exit 64 and zero new fixture invocations. Apply a synthetic workspace config declaring
both roots under `brokered.aws.roots` through host config check/apply, then run the same command:
expect exit 0, exactly `{"Tags":["fixture"]}`, canonical host argv and one fake invocation.
Remove CE root through the same gate and expect refusal next call. No manual production scope
file rewrite in this test. First red command (test added against pre-feature baseline) is
`go test -count=1 -run '^TestAWSBrokerCostExplorerRequiresItsOwnRoot$' ./integration`:
positive CE run must fail without the feature. Against Tasks 1–3 the test is wiring proof and
may already be green; do not manufacture a red by weakening a caller or running a stale artifact.

Preserve/rewrite the current pack-facing nested names to `TestAWSBrokerMediatesReadsAndAsksForWrites`
and `TestAWSBrokerTimedAnswerIsCappedAndCoversItsResource`, not repaired old-name compatibility.
`aws-auth` may appear only as synthetic selection/settings in warning tests, never a real auth setup.

**Change:** nested helper roots/host endpoint options with synthetic fixtures; current docs
and inbound links in the map. Retire current aws-s3 advice instead of inventing a transition.
Carry precise S3 baseline grammar to the live AWS doc before retiring old linked guidance.
No source reader census hand-edit, no changelog alteration, no false CLI/API proof claim.
**Gates:** `just format`; docs/link checks on every changed document;
`go test -count=1 -run '^TestAWSBroker(CostExplorerRequiresItsOwnRoot|MediatesReadsAndAsksForWrites|TimedAnswerIsCappedAndCoversItsResource)$' ./integration`;
`just build-dev-bundle`, then absolute emitted launcher from a disposable repository-local
Git-ignored durable workspace for CE read/refusal and preserved S3 cases; `git diff --check`.
Every launcher probe sets `YOLO_NO_PROGRAM_READINESS=1`, `YOLO_NO_AUTO_CAPTURE=1` and
`YOLO_NO_UPDATE_CHECK=1`; assert no installer/capture markers and complete fixture cleanup.
This path verifies broker wiring only, not rootless reachability or native macOS.
**Fences:** shared fence; no native-host/API smoke/deployment is authorized.
**Commit:** `test(aws): verify nested scope and replace current S3-only guidance`.
**Report:** exact artifact/cwd/HEAD, nested outcome and scratch cleanup, docs checks, residual platform/provider limits.

## Ships with / combined landing gates

- Pack description/access/read text explains sensitive financial data, actual billable requests,
  trusted host login, root admission, bypass and visible redaction. No price promise.
- Source refusal text names a next step; new CE forms appear in pack help/current documentation,
  not a generic pass-through `Get*` list. Full-check warning stays warning-only/config-inspection-only.
- Unit: every CE row plus all exact value/shape/root/credential/nested-boundary edge cases.
  Subprocess caller tests make dispatch deletion fail. Rewritten S3 tests preserve old behavior.
- In-process: emitted shim/authenticated front/built classifier/store/executor/fake host/local
  service, plus old-store isolation and live withdrawal. Core flow is reused, not reimplemented.
- Nested: one CE read, own-root denial and S3 approval/timed cases with no agents/install/capture.
  Documentation/source evidence is not real CLI/provider or native Mac proof.
- **Landing owner runs once on the combined tree:** `python3 scripts/completion-census.py`
  after source-reader tests/new build paths/renames; commit generated result, then `just check-ci`,
  affected integration and full integration suite because the launch path is reached. Planner
  does not run or claim those gates. Candidate completion commits cleanly, leaves no staged files;
  landing owner handles change-aware `just done` from the final combined commit.

## Don't

No generic AWS API tunnel, service plugin registry, new grant kinds, CE approval/write cells,
account pin/filter, region/endpoint/profile parser fields, credentials in protocol, recursive
nested guard after verified V-1, implicit new scheme admission, file/URL payload forwarding,
JSON config parser repair, response cache, spend meter, retry override or core redaction fix.
No transition/migration/alias/`formerly`/wrapper, old-state reaper or changelog rewrite.

## Blockers and evidence limits

No owner ruling remains. The grammar/request-count and nested-value amendments are incorporated
in the canonical design. PC-1 stays narrow (Values 1–100). V-1 passed against published sources
for both CLI major versions; no recursive fallback is active. All flags/enums checked against
published CLI/API references; MaxResults/no-SortBy remains a documented cap limitation.
No installed AWS CLI, credentials, IAM, live billing or native Mac behavior was exercised.
An implementer encountering contrary current evidence stops rather than selecting a new policy.
