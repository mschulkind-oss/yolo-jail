---
title: "AWS operation broker: source and CLI fact packet"
date: 2026-11-16
status: accepted
tags: [research, aws, broker, source-grounding]
summary: "Verified source boundaries and published AWS CLI/API semantics for replacing aws-s3 with aws, without credentials or live AWS requests."
---

# AWS operation broker: source and CLI fact packet

**Verified:** 2026-11-16 against `b5a5fe32a326109371656162623b690a3c790427`.
The isolated planning checkout began clean at the same commit as the earlier source survey;
there was no affected-seam drift. This packet is source/documentation evidence, not runtime proof.
The [implemented reference](../reference/aws-operation-broker/README.md) owns behavior;
[execution evidence boundaries](../reference/aws-operation-broker/execution.md) separate
published documentation from actual provider/native verification.

## Baseline production path

Facts below remain as of `b5a5fe32a`; source links use successor paths after the direct
replacement. They are not claims that the successor still has the baseline identity or grammar.

| Boundary | Verified source fact |
| :--- | :--- |
| Pack and intercept | [successor AWS pack](../../packs/aws/pack.toml) owns the command `aws`, forwards to `aws-s3-broker`; it does not select `aws-auth` |
| Selected declaration | [successor manifest](../../packs/aws/loopholes/aws-broker/loophole.toml) joins source `aws-s3`, CLI front, built-in classifier, core executor, `read`, declared roots and two S3 write cells |
| Shim | `GenerateShims` calls `GenerateIntercepts` in [shims](../../internal/entrypoint/shims.go); [intercept writer](../../internal/entrypoint/interceptshims.go) emits a quoted argv prefix plus original arguments, ahead of installed programs |
| Built-in caller | `runInternal` in [dispatch](../../internal/cli/internal.go) calls `s3classify.Run` only for source word `aws-s3` |
| Pure classifier | `Run` and `Classify` in [successor classifier](../../internal/awsclassify/classifier.go) support exactly three S3 reads and two S3 writes, no `ce` branch, no local answer, no lookups |
| Protocol | `CLIInput` in [core classifier](../../internal/callbroker/classifier.go) contains only version, front, argv, stdin hash, roots, optional lookup facts; there are no service/profile/account/region/endpoint fields |
| Authority | `currentAuthority` in [authority](../../internal/callbroker/authority.go) rechecks selected declaration, live scope and literal pack `read` grant; arbitrary grant kinds do not provide service-specific runtime admission |
| Decision | `decideCall` in [decision](../../internal/callbroker/decision.go) immediately admits a reads-only classification as a standing read; cell policy governs writes, not reads |
| Effect boundary | `serveCLI` in [handler](../../internal/callbroker/handler.go) rechecks live authority before `pinnedCLI.run`; classified refusals never execute the host CLI |
| Chain guard | `validateChain` in [core classifier](../../internal/callbroker/classifier.go) requires a declared resource chain ending in a parentless type with id equal to one approved root, verbatim |
| Gate | `CollectScopeCheck` flow in [launch scope](../../internal/cli/run/brokeredscope.go) calls `WithCallBrokerDeclarations` in [config](../../internal/config/callbroker.go); workspace roots and selected declaration disclosures go to the config-change gate |
| Cell snapshot | `freezeCallBrokerCells` in [launch policy](../../internal/cli/run/callbrokerpolicy.go) calls `EffectiveCallBrokerCells` in [config policy](../../internal/config/callbrokerpolicy.go); producers consume the frozen result |
| Approval namespace | `callRequest` and `decideCall` bind source in [decision](../../internal/callbroker/decision.go); `revalidate` rejects a request whose Set/Repo differ from launch source in [authority](../../internal/callbroker/authority.go). Old source records cannot become `aws` authority |

## S3 facts that must survive

The baseline classifier (now [AWS classifier](../../internal/awsclassify/classifier.go)) admits:

| Row | Canonical argv | Resource/cell |
| :--- | :--- | :--- |
| R1 | `s3 ls s3://B/P [--recursive]` | `prefix` under `location` |
| R2 | `s3 cp s3://B/K -` | `object` under `location` |
| R3 | `s3api head-object --bucket B --key K` | `object` under `location` |
| W1 | `s3 cp - s3://B/K` | `put_objects`, stdin digest bound |
| W2 | `s3 rm s3://B/K` | `delete_objects` |

Bucket and key grammars, longest-covering-root selection and malformed-root ignoring are
implemented by `checkBucket`, `checkKey`, `parseRoots`, `cover`. A bucket root ends at the
bucket name, not a slash. A prefix root ends in a slash; string-prefix similarity is not coverage.
The `-` stdin/stdout sentinel is intentionally supported; a blanket dash guard must not remove it.

The baseline manifest (now [AWS manifest](../../packs/aws/loopholes/aws-broker/loophole.toml)) keeps put tables
`working_set`/`all`, put cap `1h`, delete table `all`, no explicit delete cap or default answers.
[Timed approvals](../../internal/callbroker/timed.go) apply the default 15-minute delete cap,
use one entry per cell/full resource chain and offer no permanent Add. Allow once is command-
and stdin-bound; a timed answer instead covers its exact cell/resource during the window,
including another body. The [successor nested tests](../../integration/aws_broker_test.go) distinguish these.
The existing forwarder limits stdin to 3 MiB; this plan does not alter that boundary.

## Host login and bypass

`pinCLI` and `pinnedCLI.run` in the [executor](../../internal/callbroker/executor.go) resolve,
canonicalize and checksum host `aws`, run it at `/`, and use only host `PATH`, `HOME` and
manifest-listed host variables. No jail environment or CLI-input field chooses a host account,
profile, region, endpoint, signing mode or file. The unchanged list is:

```text
AWS_PROFILE AWS_REGION AWS_DEFAULT_REGION AWS_CONFIG_FILE
AWS_SHARED_CREDENTIALS_FILE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
AWS_SESSION_TOKEN AWS_ENDPOINT_URL AWS_ENDPOINT_URL_S3
```

The host `~/.aws` configuration and any configured credential helper are trusted. A global host
endpoint override reaches CE; no `AWS_ENDPOINT_URL_CE` is added. The executor redacts *every*
non-empty declared variable value, not only secrets, and bounds each output stream at 32 MiB.
Region/profile text can therefore become `[redacted]` inside returned financial JSON.

The intercept is not an account-wide AWS sandbox. Credentials deliberately supplied by
`aws-auth` or another route permit direct SDK/absolute-path calls. The existing
`YOLO_BYPASS_SHIMS` branch in the [intercept renderer](../../internal/entrypoint/interceptshims.go)
also reaches the installed CLI behind the shim, if one exists; it does not supply credentials.
No policy change to either route is proposed.

`Check` calls `reportAWSS3CredentialBypass` in [full check](../../internal/cli/check/check.go),
implemented in the [successor warning](../../internal/cli/check/awsbypass.go). It inspects selection and
an explicit un-narrowed `aws-auth` profile only, never IAM or credential contents. Config-only
check and launch do not emit that warning. Retargeting the selection/text requires no auth edit.

## AWS documentation check

Published documentation retrieved 2026-11-16; no AWS service endpoint was contacted.
The four operations and every admitted required/optional flag exist in both references:

| Row | CLI v2 | CLI v1 |
| :--- | :--- | :--- |
| R4 | [get-cost-and-usage](https://docs.aws.amazon.com/cli/latest/reference/ce/get-cost-and-usage.html) | [get-cost-and-usage](https://docs.aws.amazon.com/cli/v1/reference/ce/get-cost-and-usage.html) |
| R5 | [get-cost-forecast](https://docs.aws.amazon.com/cli/latest/reference/ce/get-cost-forecast.html) | [get-cost-forecast](https://docs.aws.amazon.com/cli/v1/reference/ce/get-cost-forecast.html) |
| R6 | [get-dimension-values](https://docs.aws.amazon.com/cli/latest/reference/ce/get-dimension-values.html) | [get-dimension-values](https://docs.aws.amazon.com/cli/v1/reference/ce/get-dimension-values.html) |
| R7 | [get-tags](https://docs.aws.amazon.com/cli/latest/reference/ce/get-tags.html) | [get-tags](https://docs.aws.amazon.com/cli/v1/reference/ce/get-tags.html) |

- R4 documents all seven selected metric names and at most two groups; the selected group
  types `DIMENSION`, `TAG`, `COST_CATEGORY` exist. Additional `PRODUCT_ATTRIBUTE` is not admitted.
- R5's operation prose lists the five selected forecast metrics and only `DAILY`/`MONTHLY`;
  its general model enum is wider. Prediction interval is explicitly 51–99.
- R6 documents `COST_AND_USAGE` context; R6/R7 document explicit `--next-page-token`.
- Both R6 and R7 expose `--max-results`. [GetTags API](https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_GetTags.html)
  also confirms MaxResults and a 1000 upper quota. The CLI/API prose says MaxResults is only
  used when SortBy is supplied. This design admits the flag but excludes SortBy, so it promises
  no effective page-size limit and does not inject sorting.
- `--output json` and `--no-paginate` are global options in both versions. They are emitted by
  the classifier only, never accepted from the jail. No-paginate stops automatic CE page traversal.
- The CLI leaf models document all eight selected MatchOptions. [Expression](https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_Expression.html)
  warns that operation-specific expression/match combinations vary; those errors stay AWS's.
- **PC-1 resolved without widening:** Expression does not explicitly confirm empty `Values`
  with `ABSENT`. [TagValues](https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_TagValues.html)
  describes *omitted* Values as ABSENT, not explicit `[]`. The accepted conditional amendment
  is therefore not activated: every leaf still requires 1–100 Values strings.
- Dates, token/label alphabets and filter limits in the design are intentionally narrower
  broker policy, not a claim to implement the entire AWS model.
- Billing view, sort, dimension-key, product attributes, other CE operations and arbitrary
  `Get*` names remain denied even when new AWS documentation exposes them.

### Parameter-file loading and request counts

**V-1 verified for both major versions**, using published release sources AWS CLI
1.42.0 and 2.31.0, not an installed CLI:

| Check | CLI v1 pinned source | CLI v2 pinned source |
| :--- | :--- | :--- |
| Raw load precedes parsing | [driver](https://github.com/aws/aws-cli/blob/1.42.0/awscli/clidriver.py#L599-L617): `_unpack_arg` before `add_to_params`; [load event](https://github.com/aws/aws-cli/blob/1.42.0/awscli/argprocess.py#L67-L91) | [driver](https://github.com/aws/aws-cli/blob/2.31.0/awscli/clidriver.py#L909-L927); [load event](https://github.com/aws/aws-cli/blob/2.31.0/awscli/argprocess.py#L65-L89) |
| Expansion checks whole string only | [URI handler](https://github.com/aws/aws-cli/blob/1.42.0/awscli/paramfile.py#L159-L218): registered on `load-cli-arg`; unwraps one-word list, checks only raw string prefixes | [URI handler](https://github.com/aws/aws-cli/blob/2.31.0/awscli/paramfile.py#L30-L76): same behavior |
| Parsed strings are not re-expanded | [argument conversion](https://github.com/aws/aws-cli/blob/1.42.0/awscli/arguments.py#L482-L496) invokes `process-cli-arg`, not `load-cli-arg`; [JSON path](https://github.com/aws/aws-cli/blob/1.42.0/awscli/argprocess.py#L178-L219) uses `json.loads` without recursive URI events | [argument conversion](https://github.com/aws/aws-cli/blob/2.31.0/awscli/arguments.py#L482-L496); [JSON path](https://github.com/aws/aws-cli/blob/2.31.0/awscli/argprocess.py#L176-L217) |
| `{`/`[` is JSON, not shorthand | [shorthand exclusion](https://github.com/aws/aws-cli/blob/1.42.0/awscli/argprocess.py#L418-L435) | [shorthand exclusion](https://github.com/aws/aws-cli/blob/2.31.0/awscli/argprocess.py#L416-L433) |

All three conditional facts hold in these two supported-major source exemplars. The promoted
rule is therefore **argv-boundary-only**: raw option operands (including the part after `=`
and every list operand) and canonical host value words cannot start with `-` or a URI scheme.
Nested filter Values/Tags/CostCategories Keys and group-by Keys remain data under their own
grammars, including `-x` and `file://x`. The host receives reserialized JSON starting `{`/`[`,
never a raw URI or the jail's group shorthand. There is no fallback choice left to the builder.
This is published-source evidence, not a guarantee about every historical minor release or
arbitrary host executable/plugin; host-owned CLI configuration remains trusted.

The official [retry guide](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-retries.html)
documents automatic transport retries and host `max_attempts`. Therefore one process is not
one AWS HTTP request. Existing S3 pagination remains; the broker adds no retry/lookup or retry override.
Published [Cost Explorer pricing](https://aws.amazon.com/aws-cost-management/aws-cost-explorer/pricing/)
states USD 0.01 per primary-view API request and USD 0.01 per source for custom-view requests.
This is a dated pricing observation, not a promised price or cost-per-broker-call limit;
the pack disclosure should name sensitive financial data and actual billable requests without a price.

## Direct-replacement inventory

Tracked literal search at the verified commit found these consumers:

| Category | Existing paths / consequence |
| :--- | :--- |
| Declaration, embedding, dispatch | [Successor pack](../../packs/aws/pack.toml), [loophole](../../packs/aws/loopholes/aws-broker/loophole.toml), [embed](../../packs/embed.go), [classifier](../../internal/awsclassify/classifier.go), [CLI](../../internal/cli/internal.go) |
| Registry fixtures | [TOML equivalence](../../internal/loopholedecl/shippedtoml_test.go) pairs frozen JSON filenames with shipped modules and exact decoded content (explicit historical GitHub delta precedent); [Manifest table](../../internal/loopholedecl/shipped_test.go), [loader table](../../internal/loopholes/shipped_test.go), [historical JSON fixture](../../internal/loopholedecl/testdata/legacy-shipped/aws-s3__aws-s3-broker.json) |
| Config/approval tests and comments | [Cell reader test](../../internal/config/loopholegrants_callbrokercell_test.go), [grant reader](../../internal/config/loopholegrants.go), [readable scope](../../internal/config/readablescope.go), [approval tests](../../internal/cli/approvepack_test.go) |
| Caller, classifier, warning tests | [Successor dispatch tests](../../internal/cli/aws_internal_test.go), [classifier tests](../../internal/awsclassify/classifier_test.go), [warning](../../internal/cli/check/awsbypass.go), [warning tests](../../internal/cli/check/awsbypass_test.go) |
| Production-path proofs | [E2E](../../internal/callbroker/s3_e2e_test.go), [conformance](../../internal/callbroker/conformance_s3_test.go), [successor nested](../../integration/aws_broker_test.go) |
| Current docs | [S3 design](../design/approval-experience/aws-s3.md), [S3 plan](../design/approval-experience/aws-s3-plan.md), [approval index](../design/approval-experience/README.md), [recipe](../design/approval-experience/service-mediation.md), [conformance](../design/approval-experience/service-conformance.md), [conformance plan](../design/approval-experience/service-conformance-plan.md), [broker design](../design/pack-brokers.md), [broker plan](../design/pack-brokers-plan.md) |
| Generated census | [Completion readers](../../scripts/completion-readers.json): regenerate at combined landing after source-reader test edits; never hand-edit |
| Historical release evidence | [Changelog](../../CHANGELOG.md): untouched |

Generic config readers are source-parameterized. Root syntax is workspace-only and unique;
core accepts safe root identifiers but the classifier owns each service's grammar. Old root
entries fail selected-source validation. Old grants/cell tokens/store records are not consulted
for the new pack/source. No migration, alias, wrapper, `formerly`, deletion of user state or
new special-case registry is needed.

## Evidence limits

No tests, deployment, live grants, IAM changes, credential reads, auth probes or AWS API calls
were made. CLI documentation/upstream-source reads prove published spelling and parser shape,
not acceptance by an installed CLI. Synthetic proof services cannot establish real AWS billing,
IAM/account visibility, pagination-token compatibility or native macOS behavior. Those remain
explicit limits at build acceptance; none authorizes a live provider test.
