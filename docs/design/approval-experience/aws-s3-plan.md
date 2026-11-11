---
title: "AWS S3 slice 1: build hand-off"
date: 2026-11-09
status: accepted
stage: BUILT
next: "Backlog only: prefix-scoped get-object and multipart; native-host verification of the local S3-D5 warning"
depends-on:
  - aws-s3.md
  - service-mediation.md
  - ../pack-brokers.md
tags: [plan, broker, packs, aws, s3]
summary: "Tree-grounded hand-off for the first mediated S3 slice: an aws-s3 pack on the existing CLI call-broker front, a deny-by-default classifier, and an account-free fake S3 service and host CLI."
---

# AWS S3 slice 1: build hand-off

**Status:** 2026-11-10. Tasks 1, 2 and 3 are built (classifier, dispatch and pack; fixture service and fake host CLI; in-process and nested-jail end-to-end tests). S3-D5's diagnostic is built and integrated in `main` at `b913b6b7d`, with the legacy-key and no-profile repair at `f83f75164` (based on `62b8bbda3`); the remaining [Ships with](#ships-with) backlog is prefix-scoped `get-object` and multipart. **MEASURED:** `TestCheckWarnsForAWSS3WithUnnarrowedAWSAuth` exercises the production full-check caller with paired un-narrowed, narrowed and absent selections; no AWS or API calls. Native-host verification of this warning is unperformed. The slice-1 hand-off was written against `04f300a9e`. **Design:** [`aws-s3.md`](aws-s3.md); recipe: [`service-mediation.md`](service-mediation.md).
Precedence: the design wins on behavior, the tree wins on cosmetic fact, this plan is advice and the first thing to be wrong. Stop and report, without committing, when a test does not fail as predicted, a named symbol is gone, a fix needs a file outside the task's fence, or a gate fails outside the task's scope.

**Hard rule for every task:** no real AWS. No account, no credential, no network call to an AWS endpoint. Every credential is a synthetic literal; every endpoint is `127.0.0.1`.

## Settled behavior (from the design; do not re-open)

**Mechanism ([S3-D1](aws-s3.md#S3-D1)).** Mediation through the existing CLI front. The `aws-s3` pack intercepts `aws` in the jail; core's CLI executor runs the host `aws` program with the canonical argv, the host's own login (`HOME` plus the declared `env_from_host`). No credential enters the jail or the classifier.

**Pack (`packs/aws-s3`).**

- Pack `aws-s3`; loophole `aws-s3-broker` (`transport = "loopback-tls"`, `lifecycle = "spawned"`, one `read` grant kind, `requestable_by_default = true`); `[[intercept]] bin = "aws"`, `forward = ["yolo","internal","call-broker-forward","aws-s3-broker","--"]`.
- `[call_broker]`: `source = "aws-s3"`, `front = "cli"`, `program = "aws"`, `classifier = ["yolo","internal","call-broker-classify","aws-s3"]`, `executor = "core"`, `credential_paths = ["~/.aws"]`, `env_from_host = ["AWS_PROFILE","AWS_REGION","AWS_DEFAULT_REGION","AWS_CONFIG_FILE","AWS_SHARED_CREDENTIALS_FILE","AWS_ACCESS_KEY_ID","AWS_SECRET_ACCESS_KEY","AWS_SESSION_TOKEN","AWS_ENDPOINT_URL","AWS_ENDPOINT_URL_S3"]`.
- `[call_broker.scope] roots = "declared"` (the workspace's `brokered.aws-s3.roots`). Root grammar: `s3://<bucket>` or `s3://<bucket>/<prefix>/`.
- Resources: `location` (no parent; id is the approved root string verbatim), `prefix` (parent `location`; id `s3://<bucket>/<listing-prefix>`), `object` (parent `location`; id `s3://<bucket>/<key>`).
- Cells: `put_objects` (tables `working_set`, `all`; `max_for = "1h"`; no default), `delete_objects` (table `all`; no `max_for`, so core caps a timed answer at 15 minutes; no default).

**Names.** Bucket `^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`, no `..`, not a dotted quad; ARNs refused. Key: 1–1024 bytes of `[A-Za-z0-9._=/-]`, no leading `/`, no empty, `.` or `..` segment; an object key does not end in `/`. A listing prefix uses the same alphabet, may be empty and may end in `/`.

**Coverage (deterministic).** A root (bucket `B`, prefix `R`, empty for a bucket root) covers a path (bucket `b`, key-or-prefix `p`) iff `B == b` and `p` starts with `R`. The longest covering root string is the chain's `location`. No covering root is refused, class `scope`. A root that does not parse is ignored, never widened.

**Classification table** (argv after `aws`; everything else refused):

| # | Jail argv | Canonical argv | Verdict | Chain |
| :--- | :--- | :--- | :--- | :--- |
| R1 | `s3 ls s3://B[/P]` with optional `--recursive` before or after the URL; `s3://B` equals `s3://B/` | `s3 ls s3://B/P` then `--recursive` if given | read | `prefix s3://B/P` < `location ROOT` |
| R2 | `s3 cp s3://B/K -` | same | read | `object` < `location` |
| R3 | `s3api head-object --bucket B --key K`, any order, `--x v` or `--x=v`, each once | `s3api head-object --bucket B --key K` | read | `object` < `location` |
| W1 | `s3 cp - s3://B/K` | same; stdin digest bound | write `put_objects`: `create or replace object s3://B/K from standard input` | `object` < `location` |
| W2 | `s3 rm s3://B/K` | same | write `delete_objects`: `delete object s3://B/K` | `object` < `location` |

**Refusals.** The classifier never answers `local`: core's CLI front refuses every local answer (`internal/callbroker/handler.go` ~`:162`) and the pack owns the jail's `aws`, so `aws --version` is refused `unsupported`, naming the host. Any global option anywhere (`--endpoint-url`, `--profile`, `--region`, `--no-sign-request`, `--no-verify-ssl`, `--ca-bundle`, `--debug`, `--output`, `--query`, `--cli-*`, `--color`, any other `--` word not in the row's grammar): class `credential`, next step "set it in the host's AWS configuration or environment; the broker never takes an endpoint, profile, region or signing option from the jail". Any other service, verb, flag or argument shape (`rm --recursive`, `--include`, `sync`, `mv`, `cp` between two S3 URLs or with a local path, `presign`, `mb`, `get-object`, `put-object`, `delete-objects` …): class `unsupported`, next step naming the supported forms or the host. The line between the two classes: a `--` word that some `s3` or `s3api` command takes (`--recursive`, `--include`, `--page-size` …) but the row does not is `unsupported`; every other `--` word is `credential`. A `--bucket` or `--key` value starting with `-` is `unsupported`, because the host `aws` would read it as an option. A bad bucket or key: `unsupported`, naming the alphabet. No covering root: `scope`, next step "add the root to `brokered.aws-s3.roots` in this workspace's config, then run `yolo config apply` on the host".

## Map

| Path | Change |
| :--- | :--- |
| `internal/s3classify/classifier.go` | new — `Run(args, stdin, stdout, stderr) int` and `Classify(callbroker.CLIInput) callbroker.Answer` |
| `internal/s3classify/classifier_test.go` | new — table tests over every row and refusal |
| `internal/cli/internal.go` (`call-broker-classify`, ~`:57`) | dispatch on `args[1]`: `circleci` → `circleciclassify.Run`, `aws-s3` → `s3classify.Run` |
| `packs/aws-s3/pack.toml`, `packs/aws-s3/loopholes/aws-s3-broker/loophole.toml` | new |
| `packs/embed.go` (explicit embed list), shipped-pack tests | add the pack; follow whatever `TestEmbedMatchesTree` and the shipped-manifest tests demand |
| `internal/s3fixture/server.go`, `client.go`, `*_test.go` | new — fake S3 service and fake host `aws` client |
| `internal/s3fixture/cmd/s3fixture/main.go` | new — one binary: basename `aws` is the client; `s3fixture serve` is the host-operable demo service |
| `internal/callbroker/s3_e2e_test.go` | new — the AE-E4 path in process, through shim, front, handler, real classifier, store, `yolo approve` and executor |
| `integration/aws_s3_broker_test.go` | new — nested-jail proof mirroring `TestCircleCIFakeHostCLIUsesRealClassifier` |

## Reuse

- Classifier shape: mirror `internal/circleciclassify/classifier.go` exactly (strict decode with `DisallowUnknownFields`, 64 KiB cap, trailing-data refusal, `protocolVersion = "yolo.call-broker-classifier/1"`, `refused(...)` helper). Its tests are the template for `TestXClassifierUsesActualProtocolDispatch`.
- Answer types: `callbroker.CLIInput`, `Answer`, `Classification`, `Write`, `Resource`, `Refusal` (`internal/callbroker/classifier.go`). Core re-validates every chain in `Classifier.validateChain`: each type at most once, top element parentless and equal to a root. A chain that breaks that is refused by core, not the classifier.
- Signing: `internal/sigv4.Sign(req, body, creds, Options{Region, Service: "s3", SinglePathEncoding: true, ContentSHA256Header: true})`. The fake server verifies by re-signing a clone with the same `X-Amz-Date` and comparing `Authorization`. No AWS SDK: it is deliberately not vendored.
- In-process harness: `selectedOnceFixtureWith` and `TestSelectedCLIForwarderHostApproveOnceReportsCompletedOversizedWrite` in `internal/callbroker/once_test.go` already build a selected pack, scope file, grant file, store, authenticated front, generated shim and a built `yolo` for `yolo approve`. Copy their structure; do not refactor them.
- Nested proof: `integration/circleci_broker_test.go` (`packHome`, `writeProject`, `runCommand`, `withEnv`, `check --no-build --accept-config-changes`).

## Traps

- `internal/callbroker` tests cannot import `internal/s3classify` (it imports `callbroker`: an import cycle). Run the classifier as the built `yolo internal call-broker-classify aws-s3` subprocess, as production does.
- `internal/s3fixture` must import neither `callbroker` nor `s3classify`, and nothing shipped may import `internal/s3fixture`.
- The CLI executor gives the host program only `PATH`, `HOME` and the declared `env_from_host`; a fixture marker env var never arrives. Point the fake at its server with `AWS_ENDPOINT_URL_S3` and synthetic `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`, which are declared and therefore also redacted from output.
- Core redacts declared env values from output: the fake must not echo its secret, and a test may assert it never appears in jail-visible output.
- `aws s3 ls s3://b/p` lists keys starting with `p` (string prefix, not a directory); coverage is a string-prefix test, so `s3://b/p` is not covered by root `s3://b/p/`.
- Stdin over the forwarder is capped at 3 MiB; output at 32 MiB.

## Tasks

**Task 1 — classifier, dispatch and pack** (independent). Tests first in `internal/s3classify/classifier_test.go`: every table row allowed with its exact canonical argv and chain, `--x=v` normalization, longest-root selection (roots `s3://b`, `s3://b/team/`), a malformed root ignored, every refusal class above with its `Class`, and determinism (two calls, equal answers). Red: `go test ./internal/s3classify`. Then the dispatch and the pack; `go test ./internal/s3classify ./internal/cli ./internal/loopholedecl ./internal/loopholes ./packs ./internal/config`. Fence: `internal/s3fixture`, `internal/callbroker`, `integration`, docs. Commit: `feat(aws-s3): a deny-by-default S3 classifier and the aws-s3 call-broker pack`.

**Task 2 — fixture service and fake host CLI** (independent). Tests first: a signed PUT/GET/HEAD/LIST/DELETE round trip through the client against the server; an unsigned and a wrongly signed request refused 403 and logged with state unchanged; the client exits 252 on any argv outside the canonical grammar. Red: `go test ./internal/s3fixture/...`. Fence: everything outside `internal/s3fixture`. Commit: `test(s3fixture): an account-free fake S3 service and host aws client`.

**Task 3 — end to end** (after 1 and 2). In `internal/callbroker/s3_e2e_test.go`, with the shipped `packs/aws-s3` declaration, the built `yolo` as classifier and approver, and the built `s3fixture` installed as `aws`, cover these cases:

- (a) `aws s3 ls` and `aws s3 cp s3://b/k -` run and return fixture data;
- (b) `aws s3 cp - s3://b/new` asks (exit 75, names `yolo approve r-…`), fixture unchanged;
- (c) host `yolo approve r-…` then the same call runs once and the object exists with the exact stdin bytes;
- (d) the same call again asks (Allow once spent) and changes nothing;
- (e) different stdin asks again;
- (f) `aws s3 rm` of an object in another bucket, `aws --endpoint-url http://x s3 ls s3://b`, `aws s3 sync . s3://b` and `aws ec2 describe-instances` all refuse (exit 64) and the fixture's request log shows no request;
- (g) jail-visible output never contains the synthetic secret.

Then `integration/aws_s3_broker_test.go` covering (a), (b), (c) and one refusal in a nested jail. Gates: `go test -run S3 ./internal/callbroker`; the integration command under the lock. Commit: `test(aws-s3): the S3 slice end to end through real mediation against a local fixture`.

## Ships with

- [`aws-s3.md`](aws-s3.md) and [`service-mediation.md`](service-mediation.md) (the design and recipe).
- `yolo check` already reports a call-broker pack's host program presence.
- S3-D5 is built and integrated in `main` (`b913b6b7d`, with `f83f75164`): `internal/cli/check/awss3bypass.go`, called only from the full `Check` pipeline in `check.go`, warns for selected `aws-s3` plus explicitly un-narrowed `aws-auth`. `awss3bypass_test.go` drives that caller and verifies paired un-narrowed, narrowed-role/session-policy, false/unspecified and absent-pack cases, and that `ConfigCheck` does not emit it. It reads no credentials, inspects no IAM permissions and changes no auth route or grant; native-host verification is unperformed.
- Backlog: prefix-scoped `get-object` with output to stdout; multipart.

## Don't

- Don't touch `packs/aws-auth` or `internal/awsauth*`: S3 mediation must not change the credential route.
- Don't add the AWS SDK or any S3 library: the fake implements five operations.
- Don't accept a global option "because it is harmless" (`--output`, `--region`): every one is refused in slice 1, so nothing from the jail shapes the host invocation beyond the table.
