---
title: "AWS broker: execution and observations"
status: accepted
stage: GRADUATED
verified: 2026-11-17
verified_commit: bde502e4a
covers: [packs/aws, internal/awsclassify, internal/callbroker, internal/s3fixture, internal/cli/check/awsbypass.go, integration/aws_broker_test.go, integration/aws_ce_broker_test.go]
tags: [aws, execution, verification]
---

# AWS broker: execution and observations

**Status:** Verified 2026-11-17 against `bde502e4a`. MEASURED: controlled Linux
host/service production mediation and nested Podman config-gate interaction.
UNMEASURED: installed AWS CLI v1/v2 acceptance, native macOS, real rootless-host
reachability and real AWS/IAM/account/billing behavior.

The generic executor runs the pinned host AWS CLI with canonical argv, working
directory `/`, host PATH/HOME and only the manifest-declared host variables.
Account, profile, region and endpoints follow host configuration. No jail value
can substitute credentials or configuration. A trusted host-wide endpoint override
also applies to CE just as it would to a host-typed command.

## Process, pagination and charges

An admitted jail call starts at most one host AWS CLI process. The classifier
requests no lookup, and the broker neither restarts nor retries that process.
Cost Explorer appends JSON output and disables automatic pagination; agents page
by resubmitting the returned token, one brokered call per page.

This is not a production HTTP-request cap. Host CLI transport retries remain
host-configured, and S3 listings, especially recursive ones, can page inside one
process. Sensitive cost data follows the host login's IAM visibility. AWS charges
by requests it actually receives, not broker call count. No fixed price, spend
meter, response cache or retry override is promised.

> [!WARNING]
> A loop can issue many billable reads. Removing the CE root stops the next call,
> not a completed request or its charge. Independent fake request counts prove
> the controlled fake, never the actual provider's billing behavior.

## Credentials and bypass

| Route | Boundary |
| :--- | :--- |
| Jail profile/region/endpoint/signing/debug/file input | Refused before host execution |
| Raw URI/dash-leading query operand | Refused by argv-boundary grammar |
| Nested canonical filter/group strings | Data, not host file/URL expansion |
| Unlisted service or operation | Denied by exact table dispatch |
| Host config, credential helpers and plugins | Trusted host configuration |
| Other credentials deliberately exposed into the jail | May bypass the broker |

The command shim mediates its own route, not all possible AWS access. An SDK or
an absolute-path AWS executable can use credentials supplied through `aws-auth`
or another route. The separate credential service remains unchanged.

Full `yolo check` reports the configured bypass when selected `aws` coexists with
explicit unnarrowed `aws-auth` and a profile. It inspects config only, names both
S3 and CE, and retains format-aware inspect keys. It adds **no routine launch
warning** and does not change credential permission or other trust disclosures.

## Output and failure

Stdout, stderr and exit status pass through generic bounded execution and
redaction. CE emits the CLI's JSON; the broker does not filter or reshape it.

> [!WARNING]
> Core redacts every nonempty declared host-variable value, not only secrets.
> Exported region/profile names can therefore become `[redacted]` in cost data or
> tags. This visibly alters results. Do not treat the returned financial data as
> unmodified merely because credentials were not leaked.

| Situation | Behavior and next step |
| :--- | :--- |
| Pinned host AWS CLI missing/changed | Existing refusal; relaunch with a stable host CLI |
| Login expired or IAM denies request | Host error/status returned; repair the host login/policy |
| AWS rejects date/option combination | Host error/status returned; revise within admitted grammar or run on host |
| Output exceeds cap | Existing refusal after execution; reduce the query |
| Classifier fails or times out | Existing pack-bug refusal; repair the pack/classifier |
| Root, read grant or user read ceiling withdrawn | Next call refuses its actual live cause |
| Unsupported token issued by AWS | Refuses unsupported; narrow the query or propose a reviewed grammar fix |

Reads share no mutable service state and can run concurrently. Writes retain the
existing approval-store transaction. Every call is a fresh host invocation, not a
response-cache read. A cache would retain sensitive financial data and stale
results; it is deliberately absent.

## Audit and evidence boundaries

Generic audit records source `aws`, read/write, outcome, reason and command digest.
It does not record service/operation as separate fields, so the audit alone is not
a CE-call counter or spend estimate. No core audit extension is part of this pack.

Permanent source-named tests reach the actual CLI dispatch, built classifier,
shipped pack/shim, authenticated front, handler, approval store, executor and
independent fake host/service. The fake independently parses canonical rows and
rejects unknown forms before endpoint/environment effects; it does not import the
production classifier as its validator.

The proof surface includes:

- every CE row's canonical argv/JSON and one controlled fake invocation;
- root-scheme isolation, unknown-service/operation denial and global/value escapes
  with no host invocation;
- distinct cause assertions for root/read-grant/read-ceiling withdrawal;
- actual classifier protocol capture showing argv/digest/roots, not host secrets,
  host environment, endpoint or raw stdin;
- S3 reads, asks, changed stdin, spent Allow once, timed caps and resource coverage;
- retired-source inventory, no authority/replay/spending and inert historical data;
- real config check/edit/why with read allowance held first, provenance and safe
  migration refusal;
- one live nested broker admitting/withdrawing CE roots through TOML config apply.

Deleting production dispatch/full-check/classifier/lowering seams makes permanent
caller tests fail. Restored controls and fake calls establish wiring, not native
provider correctness. Published CLI v1/v2 source/reference checks are distinct
from execution. [Grounding](../../research/aws-operation-broker-grounding.md)
records that dated external evidence without upgrading it to a real AWS claim.

Native checks and unmeasured compatibility have a live next step in the
[verification backlog](../../plans/BACKLOG.md#git-sourced-skills-and-aws-verification).
They run on main after Linux landing; no Mac branch or installed/live account
probe is required for Linux graduation.

## Current values

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Host AWS env set | Ten names; no CE-specific addition | `packs/aws`, loophole manifest |
| Output cap | 32 MiB per stream | `internal/callbroker`, executor |
| Stdin cap | 3 MiB; CE adds no stdin row | `internal/callbroker`, CLI front |
| Canonical CE suffix | `--output json --no-paginate` | `internal/awsclassify`, `classifyCE` |
| Execution identity | Pinned host `aws`, cwd `/` | `internal/callbroker`, `pinCLI` |
