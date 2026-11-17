---
title: "AWS broker: operation grammar"
status: accepted
stage: GRADUATED
verified: 2026-11-17
verified_commit: bde502e4a
covers: [internal/awsclassify]
tags: [aws, classifier, grammar]
---

# AWS broker: operation grammar

**Status:** Verified 2026-11-17 against `bde502e4a`. Pure classifier and built
subprocess/canonical-argv synthetic witnesses; installed AWS CLI acceptance is
not established by those tests.

`Classify` selects an exact table from the first argv word. Unknown service,
operation or form refuses; `Get*`/`Describe*` spelling is not evidence of safety.
No local command, including `aws --version`, is admitted. Every accepted value is
parsed, validated and re-emitted rather than passed through unchecked.

## Boundary and refusal order

Unknown `--` vocabulary refuses `credential` before shape, values and scope are
considered. CE's vocabulary is the union of its row flags; S3 and unknown-service
vocabulary remains the S3 set. A known flag on the wrong row, duplicate/missing
flag, stray positional word or invalid value refuses `unsupported`. Only then can
an otherwise valid call refuse `scope` for lacking its own service root.

Profile, region, endpoint, signing, debug, CA, file/JSON/YAML input, skeleton,
output, query and pagination globals from the jail never run. Refusals name host
configuration, supported forms or the root-edit/apply next step. They do not echo
jail operands, except a plain option name.

Every CE raw option operand and emitted value word must avoid a leading dash or
URI scheme prefix (`[A-Za-z0-9+.-]+://`), including equals-form values and each
list operand. Nested JSON values are **data**, not argv words: filter Values,
Tags/CostCategories Keys and group Keys may contain `-x` or `file://x` under their
own grammar. Filter/group values are emitted as one compact JSON word beginning
`{` or `[`. Raw URI operands remain refused. S3's `-` stdin/stdout sentinel is
unchanged. Published-source boundary evidence is in the
[grounding packet](../../research/aws-operation-broker-grounding.md#parameter-file-loading-and-request-counts).

## S3 rows

`B`, `K` and `P` mean a bucket, object key and listing prefix accepted below.
`ROOT` is the longest covering S3 root. Leaf resources point to `location ROOT`.

| Jail form | Canonical argv | Verdict / leaf |
| :--- | :--- | :--- |
| `s3 ls s3://B[/P]`, optional `--recursive` before/after URL | `s3 ls s3://B/P`, then optional `--recursive` | read / `prefix s3://B/P` |
| `s3 cp s3://B/K -` | same | read / `object s3://B/K` |
| `s3api head-object --bucket B --key K`, either order, separate/equals flags once | fixed bucket then key flags | read / `object s3://B/K` |
| `s3 cp - s3://B/K` | same, stdin digest bound | write `put_objects` / `object s3://B/K` |
| `s3 rm s3://B/K` | same | write `delete_objects` / `object s3://B/K` |

Bucket-only listing canonicalizes to a trailing slash. Put describes creating or
replacing an object from standard input; delete describes deleting that object.
All S3 approval/body/timed behavior remains the generic broker's contract.

### S3 names

Bucket names match `^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`, excluding `..`, dotted
quads and ARNs. Key/prefix alphabets are ASCII `[A-Za-z0-9._=/-]`: spaces, `+`,
percent escapes and non-ASCII refuse. Keys have no leading/trailing slash, empty
segment, `.` or `..` segment. Listing prefixes may be empty or end in one slash,
otherwise retain those segment rules. Bounds are in [Current values](#current-values).

A bucket root is `s3://B`, not `s3://B/`. A prefix root has a nonempty valid
listing prefix ending `/`. Coverage is same bucket plus prefix matching; no root
normalization or repair widens it.

## Cost Explorer rows

Every row is read-only with `cost_query ce://host-login/<operation>` pointing to
exact `location ce://host-login`. Required then optional columns below are the
canonical flag order; only present optional flags are emitted.

| Operation | Required flags | Optional flags |
| :--- | :--- | :--- |
| `get-cost-and-usage` | time-period, granularity, metrics | group-by, filter, next-page-token |
| `get-cost-forecast` | time-period, granularity, metric | filter, prediction-interval-level |
| `get-dimension-values` | time-period, dimension | search-string, filter, max-results, next-page-token, context |
| `get-tags` | time-period | tag-key, search-string, filter, max-results, next-page-token |

Input flags may occur in any order, separately or as `--name=value`, each once.
Metrics and group-by consume lists; other options consume one word. Dimension
queries inject `--context COST_AND_USAGE` when absent. All rows end with
`--output json --no-paginate`. This requests one CE page, not one HTTP request;
[execution](execution.md) explains retries and costs.

Billing-view ARN, SortBy, dimension-key, product-attribute grouping/filtering,
hourly granularity, other contexts and every other CE operation are excluded.
MaxResults is accepted syntax, **not** a page-size promise without excluded SortBy.

## Parsed values and canonical JSON

Time-period accepts either `Start=D,End=D` in either input order or a JSON object
with exactly Start/End. Dates are real calendar dates in the allowed years, Start
before End; output always uses Start then End shorthand. Granularity is DAILY or
MONTHLY. Metrics and MatchOptions are distinct enumerated names emitted in their
definition order (`ceMetrics`, `ceForecastMetrics`, `ceMatchOptions`); unknown or
duplicate names refuse. Forecast metric and context are scalar exact enums.

Dimension names are an uppercase letter followed by uppercase letters, digits or
underscores. Labels are ASCII letters/digits/space and `_ . : / = + @ -`.
Decimal options have no leading zero. Tokens are printable ASCII without spaces
and remain boundary-guarded. AWS validates semantic dimensions, operation-specific
enum combinations, retention, forecast start and date-range policy; its errors
return without broadening admission.

Group-by is either words `Type=T,Key=K` in exactly that order or one JSON array
of exactly Type/Key objects, input keys either order. Types are DIMENSION, TAG or
COST_CATEGORY; dimension keys use dimension grammar, others label grammar. Word
keys contain no comma/equal. Mixed forms, multiple JSON words and duplicate
(Type, Key) pairs refuse. Output is one JSON array word, entries in given order,
object keys Type then Key.

A filter node is an object with exactly one of:

- And/Or: an array of at least two nodes, preserving child order;
- Not: one node object, never an array;
- Dimensions/Tags/CostCategories: exactly Key, Values and optional MatchOptions.

Leaf Key uses dimension/label grammar. Values are distinct valid UTF-8 strings,
no control characters; an empty string is allowed but empty/omitted Values is not,
even with ABSENT. Values retain order. Absent MatchOptions stays omitted; a
present list must be nonempty with distinct known names. Structurally equal
And/Or children are allowed; they are not deduplicated.

The token walker `ceJSON` rejects duplicate keys at every level, unknown schema
keys, wrong types, invalid UTF-8 and trailing data. Logical filter depth counts
nodes, not leaf inner arrays/objects. Leaves count across the entire tree, not
Values members. Compact JSON uses deterministic key order and round-trips strings
without insignificant whitespace; nested scheme/dash text is not recursively
loaded by the host's parameter-file expansion.

## Current values

Exact acceptance bounds, verified in `internal/awsclassify`:

| Value | Bound / setting | Anchor |
| :--- | :--- | :--- |
| S3 key | 1–1024 bytes | `checkKey` |
| S3 listing prefix | 0–1024 bytes | `checkKey` (listing mode) |
| Date years | 2000–2099; `YYYY-MM-DD` | `ceDate`, `cePeriod` |
| Metrics | 1–7 distinct usage metrics | `ceMetrics`, `ceEnumList` |
| Groups | 1–2 | `ceGroups` |
| Dimension | 1–64 characters | `ceDimension` |
| Label | 1–256 ASCII bytes | `ceLabel` |
| MaxResults | 1–1000, no leading zero | `ceValue` |
| Prediction interval | 51–99 | `ceValue` |
| Next-page token | 1–8192 bytes, ASCII 33–126 | `ceValue` |
| Received filter | ≤8192 bytes before parsing | `ceFilter` |
| Logical filter depth / leaves | ≤5 / ≤50 | `ceFilterNode` |
| Values per leaf / string bytes | 1–100 / 0–256 | `ceFilterNode` |
| MatchOptions | 1–8 | `ceMatchOptions`, `ceFilterNode` |
| JSON token-depth guard | ≤32 | `ceJSONValue` |
