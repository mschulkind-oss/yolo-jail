---
title: "AWS S3 vertical: five object operations through the host's own aws, and nothing else"
date: 2026-11-09
status: accepted
stage: BUILT
next: "Graduate this design into a system doc; backlog in aws-s3-plan.md"
depends-on:
  - ../pack-brokers.md
  - service-mediation.md
tags: [design, broker, packs, aws, s3, approvals]
summary: "The first mediated AWS service: an aws-s3 pack on the existing CLI call-broker front whose deny-by-default classifier admits three reads and two writes on declared bucket and prefix roots, executed by the host's own aws with the host's own login, proved against an account-free fake S3 service."
---

# AWS S3 vertical: five object operations through the host's own `aws`, and nothing else

**Status:** 2026-11-10. Every decision is ledgered. Slice 1 is built: `packs/aws-s3`, `internal/s3classify`, `internal/s3fixture` and the end-to-end tests. Slice-1 evidence checked against `04f300a9e`. S3-D5's full-check warning is built locally in an unpushed change based on `62b8bbda3`. **MEASURED:** `TestCheckWarnsForAWSS3WithUnnarrowedAWSAuth` drives the production `Check` caller with selected, narrowed and absent-pack cases, without AWS or an API call. Native-host verification remains unperformed. The build hand-off is [`aws-s3-plan.md`](aws-s3-plan.md).

> **In short.** S3 is mediated, not delegated: the jail's `aws` is a forwarder, a classifier
> with no credential admits five exact object operations on roots the human declared, and the
> host runs its own `aws` with its own login. It confines S3 only when no other S3-capable
> credential reaches the jail, and it says so.

**Why it matters.** It is the first service walked through the
[service-mediation recipe](service-mediation.md), so its table, refusals and fixture are the
worked example every later service copies ([AE-E4](README.md#10-observable-success-and-next-investigations)).

**The shape.** A new `aws-s3` pack declares a CLI-front call broker over `aws`; core's CLI
executor runs the host program; a fake S3 service and fake host `aws`, in one test-only binary,
prove the path without an account.

**Cost.** In a jail that selects `aws-s3`, every `aws` invocation that is not one of the five
forms is refused, including every other AWS service.

**Start at [§3](#3-the-classification-table)** — the table is the design; the rest bounds it.

**Needs your ruling:** None.

**Reads with:** [`service-mediation.md`](service-mediation.md) (the recipe this instantiates),
[`aws-s3-plan.md`](aws-s3-plan.md) (the build hand-off; the design wins on behavior),
[`pack-brokers.md`](../pack-brokers.md) (the call-broker contract every term here comes from),
[the umbrella](README.md) (intent and boundaries).

---

## 1. Goal and non-goals

**Goal.** An agent in the jail lists, reads, writes and deletes individual S3 objects under
buckets and prefixes the human declared for this workspace, with every write asking first, and no
AWS credential entering the jail or the classifier.

**What slice 1 does not claim:**

- **Live AWS compatibility.** The fake proves policy and wiring; whether the real `aws` CLI and S3
  behave identically is recorded separately ([§7](#7-evidence-and-acceptance)).
- **SDK clients in the jail.** A program using an AWS SDK does not run `aws`, so it is not
  mediated; it reaches S3 only with a credential, which is the bypass in [§6](#6-bypass-analysis-and-the-confinement-claim).
- **Multipart, versions, presigning, `sync`, `mv`, recursive delete, bucket operations,
  object-to-object copy, or local-file transfer.** All refused ([§3.2](#32-refusals)).
- **Any other AWS service.** EC2 and a third service follow the recipe in their own sibling
  ([§8](#8-handoff-to-ec2-and-a-third-service)).
- **A change to `aws-auth`.** Its credential route is untouched ([S3-D2](#S3-D2)).

## 2. Terms

Call-broker terms — **front**, **classifier**, **canonical form**, **scope root**, **resource
type**, **executor** — are [`pack-brokers.md` §2](../pack-brokers.md#2-terms)'s; **cell**,
**ask** and **working set** are linked from there. **Mediation** and **credential delegation** are
[the umbrella's](README.md#1-recommendation-and-scope). **Classification table**, **refusal table**
and **local proof service** are defined in [`service-mediation.md` §2](service-mediation.md#2-terms).

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **bucket root** *(coined here)* | A declared root naming a whole bucket: `s3://B` | Not a bucket the jail may create or configure |
| **prefix root** *(coined here)* | A declared root naming a key prefix ending in `/`: `s3://B/team/` | Not a directory: S3 has none, and coverage is a string-prefix test |
| **covering root** *(coined here)* | A parsed root whose bucket equals the call's and whose prefix starts the call's key or listing prefix | Not merely a root in the same bucket |

## 3. The classification table

### 3.1 Admitted forms

The argv is everything after `aws`. Anything not in this table is refused. `B` is a bucket, `K` an
object key, `P` a listing prefix (grammar in [§4.1](#41-names)); `ROOT` is the longest covering
root ([§4.2](#42-roots-and-coverage)).

| Row | Jail argv | Canonical argv | Verdict | Resource chain | Description |
| :--- | :--- | :--- | :--- | :--- | :--- |
| R1 | `s3 ls s3://B[/P]`, optional `--recursive` before or after the URL; `s3://B` equals `s3://B/` | `s3 ls s3://B/P`, then `--recursive` if given | read | `prefix s3://B/P` < `location ROOT` | — |
| R2 | `s3 cp s3://B/K -` | same | read | `object s3://B/K` < `location ROOT` | — |
| R3 | `s3api head-object --bucket B --key K`, either order, `--x v` or `--x=v`, each once | `s3api head-object --bucket B --key K` | read | `object s3://B/K` < `location ROOT` | — |
| W1 | `s3 cp - s3://B/K` | same; stdin digest bound | write `put_objects` | `object s3://B/K` < `location ROOT` | `create or replace object s3://B/K from standard input` |
| W2 | `s3 rm s3://B/K` | same | write `delete_objects` | `object s3://B/K` < `location ROOT` | `delete object s3://B/K` |

A chain is written leaf first, `<` reading "inside". Every chain is two elements; no row needs a
lookup, because bucket and key are in the argv.

**No `local` answer.** The classifier never answers `local`. Core's CLI front turns a `local`
answer into a refusal telling the caller to run the command in the jail
(`internal/callbroker/handler.go`, checked 2026-11-09), and the pack owns the jail's `aws`
([S3-D8](#S3-D8)), so there is no other `aws` to run. `aws --version` is therefore refused
`unsupported`, naming the host, like every other argv outside the table: nothing reaches the host.

### 3.2 Refusals

| Class | What triggers it | Next step the refusal names |
| :--- | :--- | :--- |
| `credential` | Any global option anywhere in the argv: `--endpoint-url`, `--profile`, `--region`, `--no-sign-request`, `--no-verify-ssl`, `--ca-bundle`, `--debug`, `--output`, `--query`, `--cli-*`, `--color`, and any other `--` word not in the row's grammar | Set it in the host's AWS configuration or environment; the broker never takes an endpoint, profile, region or signing option from the jail |
| `unsupported` | Any other service, verb, flag or shape: `rm --recursive`, `--include`, `sync`, `mv`, `cp` between two S3 URLs or with a local path, `presign`, `mb`, `get-object`, `put-object`, `delete-objects`, an ARN for a bucket | The supported forms, or run it on the host |
| `unsupported` | A bucket or key outside [§4.1](#41-names)'s grammar | The allowed alphabet |
| `scope` | No covering root | Add the root to `brokered.aws-s3.roots` in this workspace's config, then run `yolo config apply` on the host |

**Order of checks**, so one argv always yields one class: global options first (`credential`),
then shape (`unsupported`), then names (`unsupported`), then coverage (`scope`). An argv carrying
both `--profile` and an unsupported verb is `credential`.

**Refusals run nothing.** No host process starts and the fixture's request log records nothing;
the end-to-end test asserts that ([§7](#7-evidence-and-acceptance)).

## 4. Scope

### 4.1 Names

| Name | Grammar |
| :--- | :--- |
| Bucket | `^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`; no `..`; not a dotted quad. ARNs refused |
| Object key | 1–1024 bytes of `[A-Za-z0-9._=/-]`; no leading `/`; no empty, `.` or `..` segment; does not end in `/` |
| Listing prefix | The key alphabet; may be empty; may end in `/` |

The alphabet is deliberately narrower than S3's: spaces, `+`, `%` and non-ASCII keys are refused
in slice 1 rather than canonicalized, because a key that needs encoding is a key whose canonical
and jail forms can disagree.

### 4.2 Roots and coverage

- **Source.** Roots are `declared`: the workspace's `brokered.aws-s3.roots`, approved at the
  config-change gate ([`pack-brokers.md` §7.1](../pack-brokers.md#71-the-block)). Core already
  refuses a value that is not a root identifier at all.
- **Grammar.** `s3://B` is a bucket root; `s3://B/R` with non-empty `R` ending in
  `/` and drawn from the key alphabet is a prefix root.
- **Malformed roots are ignored, never widened.** A root core accepts but this grammar does not
  (`team:ENG`, `s3://Bad_Bucket`, `s3://b/team` without the slash, `s3://b/` with an empty
  prefix) covers nothing. It is never repaired into a root that would.
- **Coverage.** A root with bucket `B'` and prefix `R` (empty for a bucket root) covers a call on
  bucket `b` with key or listing prefix `p` iff `B' == b` and `p` starts with `R`.
- **Longest wins.** The chain's `location` is the longest covering root string, verbatim. Roots
  `s3://b` and `s3://b/team/` both cover `s3://b/team/x`; the location is `s3://b/team/`.
  Two covering roots of equal length are the same string, so there is never a tie. The answer is
  a pure function of argv and roots.
- **String prefix, not directory.** `aws s3 ls s3://b/p` lists keys starting with `p`, so root
  `s3://b/p/` does not cover it, and root `s3://b/te` covers `s3://b/team/x`. Write prefix roots
  with their trailing slash.
- **No covering root** is class `scope`.

> [!WARNING]
> The chosen location is part of a timed grant's identity: a timed answer covers its cell on the
> write's whole chain ([BX-D19](../pack-brokers.md#BX-D19)). Adding a longer root later moves new
> calls to a new chain, so an earlier timed answer stops covering them and they ask again. This
> fails closed, and it is why the longest root, not the first, is the rule.

### 4.3 Cells and grants

| Cell | Tables | `max_for` | Default | Rows |
| :--- | :--- | :--- | :--- | :--- |
| `read` (the grant kind) | — | — | Needs the workspace's read grant | R1, R2, R3 |
| `put_objects` | `working_set`, `all` | 1 h | asks | W1 |
| `delete_objects` | `all` | none, so core caps a timed answer at 15 min | asks | W2 |

No default runs unasked in slice 1. The pack declares no `joins_working_set` rule, so its working
set holds only what a human hands over ([`pack-brokers.md` §7.4](../pack-brokers.md#74-working-set-rules)).
The gate shows both cells, their tables, `max_for` and the absent defaults before the human
approves the selection. An answer to a write is Allow once (bound to the canonical argv and, for
W1, the stdin digest), a timed `--for` within the cell's cap, or Deny
([OQ-BX7](../pack-brokers.md#OQ-BX7), [BX-D17](../pack-brokers.md#BX-D17)).

## 5. Execution and credentials

The pack declares `executor = "core"`. Core's CLI executor runs the **host's** `aws` with the
canonical argv, the jail's stdin (W1 only), and an environment of `PATH`, `HOME` and the declared
`env_from_host` taken from the host broker's own environment, never the jail's
([`pack-brokers.md` §8.1](../pack-brokers.md#81-the-cli-executor)):

`AWS_PROFILE`, `AWS_REGION`, `AWS_DEFAULT_REGION`, `AWS_CONFIG_FILE`,
`AWS_SHARED_CREDENTIALS_FILE`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`,
`AWS_ENDPOINT_URL`, `AWS_ENDPOINT_URL_S3`.

- **The host's `aws` signs** with whatever login the host has: SSO cache, `~/.aws/credentials`,
  or the declared environment. Region, profile and endpoint come from the host's configuration.
- **`credential_paths = ["~/.aws"]`** fences a workspace `mounts` entry reaching `~/.aws` while
  the pack is selected.
- **Limits.** Stdin over the forwarder is capped at 3 MiB, so W1 cannot write a larger object in
  slice 1; the forwarder refuses before anything is filed. Executor output is capped at 32 MiB, so
  R2 of a larger object fails after the host read, naming the cap.
- **Redaction** covers only declared environment values. An SSO-cache or credentials-file login
  has no redaction backstop; the five admitted verbs do not print credentials.

## 6. Bypass analysis and the confinement claim

**The claim.** The broker governs calls made through the jail's `aws` command. It confines S3
only when no other S3-capable credential reaches the jail. Nothing else is claimed.

| Route around the broker | Status |
| :--- | :--- |
| `aws-auth` credentials with S3 reach: both packs selected, and `aws-auth` serves `unnarrowed` or a `role_arn` whose permissions include S3 | **Open.** The container-credentials pointer hands any jail process a credential an SDK, or an `aws` reached by absolute path, uses directly. Narrow `aws-auth` to a role without S3 |
| A credential the user deliberately grants into the jail (a user-scope mount or a `host_files` grant of `~/.aws`, or a key in the jail's environment) | **Open, and the user's own authority.** A workspace `mounts` entry is fenced; user-scope config is not |
| Jail-supplied endpoint, profile, region or signing option | **Closed.** Every global option is refused ([§3.2](#32-refusals)); jail environment is never forwarded |
| Host `~/.aws/config` settings such as `endpoint_url` | **Trusted.** Host-owned; the jail cannot write them |
| Credential in output | **Bounded.** Declared env values are redacted; SSO and file credentials are not redactable, and the admitted verbs do not print them |
| Unauthenticated network access to S3 | **Not a bypass.** It reaches only what is public |

**Diagnostic ([S3-D5](#S3-D5)).** Full `yolo check` warns when the selected packs include
both `aws-s3` and `aws-auth`, and the supplied `aws-auth` setting explicitly selects
`unnarrowed = true`. It reports a **possible** bypass, not proof of S3 permission: directly
delivered credentials let an SDK or an `aws` binary reached by absolute path bypass the wrapped
CLI if the profile permits S3. Run `yolo config why allow.aws-auth.unnarrowed` on the host to
review the selected mode; if broker-only S3 access is wanted, ask the AWS administrator for a
profile or role without S3 permissions. Keeping the intended auth route is valid.

The warning runs only in the full diagnostic, not `yolo config check` or routine launches. It
reads configuration, not credentials or IAM permissions, and changes no auth route or grant.
A narrowed role is outside this warning's trigger, **not** evidence that its permissions exclude
S3. This warning is built locally and unpushed; native-host verification is still unperformed.

## 7. Evidence and acceptance

**[AE-E4](README.md#10-observable-success-and-next-investigations) is met when**, with no AWS
account, credential or endpoint involved:

1. `aws s3 ls` and `aws s3 cp s3://b/k -` in the jail return fixture data.
2. `aws s3 cp - s3://b/new` asks (exit 75, naming `yolo approve r-…`); fixture state is unchanged.
3. After host `yolo approve r-…`, the same call runs once and the object holds exactly the stdin
   bytes.
4. The same call again asks (Allow once spent) and changes nothing; different stdin asks again.
5. A delete in an uncovered bucket, `aws --endpoint-url http://x s3 ls s3://b`,
   `aws s3 sync . s3://b` and `aws ec2 describe-instances` refuse (exit 64), and the fixture's
   request log shows no request.
6. No jail-visible output contains the synthetic secret.
7. Cases 1–3 and one refusal pass in a nested jail.

**What the fixture proves.** The fake S3 service verifies SigV4 signatures, so a passing run shows
the host-side credential was applied on the host, not in the jail ([S3-D7](#S3-D7)). It is not
evidence that real S3 or the real `aws` CLI behave the same; live compatibility is a separate
record ([umbrella §6.1](README.md#61-reuse-the-pack-contract-produce-a-recipe), step 5).

## 8. Handoff to EC2 and a third service

They follow [the recipe](service-mediation.md) in their own sibling. Two facts from this slice bind
them:

- **One `aws` name.** Intercepts are exclusive by name (`internal/packload/footprint.go:551`), so
  a second pack intercepting `aws` cannot be selected beside `aws-s3`. The EC2 sibling decides
  whether AWS services share one pack and classifier with per-service tables, or something else;
  pack renames have a `formerly` path.
- **A third service.** CloudWatch Logs exercises query-shaped reads (query text, time ranges,
  sensitive results) that S3 does not. This is a nonbinding note, not a selection.

## 9. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Narrow the `aws-auth` SSO route to S3 | Rejected: delegation, not mediation ([S3-D1](#S3-D1)) |
| Pack or core HTTP executor signing with SigV4 behind the HTTP front | Rejected for now; revisit when the HTTP front publishes ([S3-D1](#S3-D1)) |
| Extend `aws-auth` with S3 cells | Rejected: different trust model ([S3-D2](#S3-D2)) |
| Accept local paths for `cp` | Rejected: the executor runs on the host ([S3-D3](#S3-D3)) |
| Accept harmless global options such as `--output` | Rejected: nothing from the jail shapes the host invocation ([S3-D3](#S3-D3)) |
| A different intercepted name, such as `aws-s3` | Rejected ([S3-D8](#S3-D8)) |

## Decision Ledger

Each row is an implementer decision under the umbrella's settled intent
([AE-R4](README.md#11-settled-owner-intent)): one-answer mechanism choices are ledgered with
their reasons rather than filed as questions.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="S3-D1"></a>S3-D1 | **Mediate through the existing CLI front; core's CLI executor runs the host `aws`, which signs with the host login; no credential crosses.** Rejected: (a) narrowing the `aws-auth` SSO route, which is delegation — the jail holds credentials and can call S3 directly, so there is no per-operation approval to give, and wrapping a CLI over delegated credentials is bypassable ([umbrella §9](README.md#9-alternatives-costs-and-risks), "Wrap `aws` while preserving unrestricted delegated credentials"); (b) a pack executor or core HTTP executor signing with `internal/sigv4` behind the HTTP front: the front is unpublished (G4), [OQ-HTTP1](../pack-brokers-http.md#OQ-HTTP1)'s signer wire is undesigned, and it would put a host credential reader in core for one service. Revisit (b) when the HTTP front publishes, because it would also cover SDK clients in the jail | 2026-11-09 | [§5](#5-execution-and-credentials) | — |
| <a id="S3-D2"></a>S3-D2 | **A new pack `aws-s3`, not an extension of `aws-auth`.** Different trust model (mediation versus delegation); `aws-auth` is unchanged; selecting one never selects the other | 2026-11-09 | [§1](#1-goal-and-non-goals) | — |
| <a id="S3-D3"></a>S3-D3 | **The front grammar is the stream forms of `aws s3` plus `s3api head-object`.** Host-file paths are never accepted, because the executor runs on the host and a jail-named path would read or write host files, so put and get go through stdin and stdout. Every global option is refused, so nothing from the jail shapes endpoint, profile, region or signing | 2026-11-09 | [§3](#3-the-classification-table) | — |
| <a id="S3-D4"></a>S3-D4 | **Roots are declared** (`s3://B` or `s3://B/R/`; `s3://B/` is not a root); the longest covering root string is the chain top; coverage is a string-prefix test; a root that does not parse covers nothing | 2026-11-09 | [§4.2](#42-roots-and-coverage) | — |
| <a id="S3-D5"></a>S3-D5 | **Bypasses are listed and the claim is bounded:** the broker governs calls through the jail's `aws`, and confines S3 only when no other S3-capable credential reaches the jail. Full `yolo check` warns for selected `aws-s3` plus explicitly un-narrowed `aws-auth`; it does not inspect IAM permissions, read credentials, alter auth or grants, or run on routine launches | 2026-11-09 | [§6](#6-bypass-analysis-and-the-confinement-claim) | Built locally, unpushed; production caller test measured, native-host verification unperformed |
| <a id="S3-D6"></a>S3-D6 | **Cells:** `put_objects` (`working_set` and `all`, `max_for` 1 h, asks), `delete_objects` (`all` only, 15 min cap, asks). No default runs unasked in slice 1; reads need the read grant | 2026-11-09 | [§4.3](#43-cells-and-grants) | — |
| <a id="S3-D7"></a>S3-D7 | **The fake is `internal/s3fixture` and verifies SigV4**, so the fixture proves the host credential is applied host-side. It is not live AWS compatibility evidence | 2026-11-09 | [§7](#7-evidence-and-acceptance) | — |
| <a id="S3-D8"></a>S3-D8 | **Selecting `aws-s3` owns the jail's whole `aws` command:** every non-S3 `aws` invocation is refused. Mediated tools have no unmediated twin of the same name, as `gh` and `circleci` already work; a different name (`aws-s3 …`) would leave the agent's habitual `aws` free to reach a credential directly. A user who wants an unmediated in-jail `aws` with their own credentials does not select this pack | 2026-11-09 | [§3.1](#31-admitted-forms) | — |
| <a id="S3-D9"></a>S3-D9 | **Checks run in a fixed order** (global options, shape, names, coverage) so each argv yields one refusal class | 2026-11-09 | [§3.2](#32-refusals), [§4.2](#42-roots-and-coverage) | — |
