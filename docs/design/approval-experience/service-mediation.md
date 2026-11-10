---
title: "Service mediation: adding a service should be filling in a table, and here is the table"
date: 2026-11-09
status: accepted
stage: DECIDED
next: "Walk the recipe once with the S3 slice (aws-s3-plan.md); revisit the shared-helper question after a second service"
depends-on:
  - ../pack-brokers.md
tags: [design, broker, packs, recipe, testing, approvals]
summary: "The repeatable recipe for mediating one more service through a pack call broker: the classification and refusal table format, the shared scope vocabulary, the front-selection rule, executor and credential requirements, the account-free local proof service pattern, and the test checklist, with AWS S3 as the worked example."
---

# Service mediation: adding a service should be filling in a table, and here is the table

**Status:** 2026-11-09. Every decision is ledgered; the first walk of the recipe, the
[S3 slice](aws-s3.md), is being implemented on this lane and nothing is claimed built here.
Evidence checked against `04f300a9e`.

> **In short.** The [pack call-broker contract](../pack-brokers.md) already owns decision,
> approval and execution, so a new service owes only three artifacts: a classification table, a
> local proof service, and a fixed test checklist. Everything else is a copy.

**Why it matters.** [AE-E5](README.md#10-observable-success-and-next-investigations) asks that a
second adapter be written mostly by filling in a table. Without a fixed format, each adapter
reinvents its refusals, scope rules and fixtures, and the twentieth service costs what the first
did.

**The shape.** One table format, one scope vocabulary, one fixture pattern, one checklist —
documents and conventions, not a new core component.

**Cost.** A service that does not fit the table (query languages, multi-resource writes the CLI
expands silently) still needs design work; the recipe makes that visible, not free.

**Start at [§4](#4-the-classification-table-format)** — the format the rest of the recipe fills.

**Needs your ruling:** None.

**Reads with:** [`aws-s3.md`](aws-s3.md) (the worked example),
[`pack-brokers.md`](../pack-brokers.md) (the contract this applies; it wins on any conflict),
[`pack-brokers-http.md`](../pack-brokers-http.md) (the HTTP front's status),
[the umbrella](README.md) (intent, [§6.3](README.md#63-complete-coverage-inventory)'s service
inventory).

---

## 1. Reader question and verdict

**What does it take to add one fine-grained mediated service, and can it be walked again cheaply?**

Verdict: walk five steps ([umbrella §6.1](README.md#61-reuse-the-pack-contract-produce-a-recipe)),
producing the artifacts in [§3](#3-the-recipe-step-by-step), on the CLI front, against a local
proof service, until the checklist in [§9](#9-the-test-checklist) passes. Build no shared
abstraction until a second service shows which parts are actually common ([SM-D5](#SM-D5)).

## 2. Terms

Call-broker terms — **call broker**, **pack broker**, **front** (**CLI front**, **HTTP front**),
**classifier**, **canonical form**, **lookup**, **resource type**, **scope root**, **executor** —
are defined in [`pack-brokers.md` §2](../pack-brokers.md#2-terms), with **cell**, **ask**,
**working set**, **Allow once** and **timed grant** linked from there. **Mediation** and
**credential delegation** are [the umbrella's](README.md#1-recommendation-and-scope).

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **recipe** | The repeatable procedure for adding one mediated service; from [the umbrella](README.md#61-reuse-the-pack-contract-produce-a-recipe) | Not a code framework or generator |
| **classification table** *(coined here)* | A service's list of admitted jail calls, each with canonical form, verdict, resource chain and description ([§4.1](#41-admitted-forms)) | Not the classifier program, which implements it |
| **refusal table** *(coined here)* | A service's list of refused call shapes, each with a refusal class and the next step it names ([§4.2](#42-refusals)) | Not an exhaustive denylist: anything absent from the classification table is refused anyway |
| **local proof service** | A small host-side fake of the service with a fake host client and synthetic credentials; from [`pack-brokers.md` §12.1](../pack-brokers.md#121-what-the-fixtures-prove) | Not a mock inside a unit test, and not evidence of live compatibility |
| **root grammar** *(coined here)* | The pack's rule for which declared or remote-derived strings are scope roots for this service | Not core's root-identifier check, which only refuses non-identifiers |

## 3. The recipe, step by step

| Step | Produces | Where it lives |
| :--- | :--- | :--- |
| 1. Inventory operations and fronts: verbs, alternate flags, endpoint overrides, SDK access, pagination, compound effects | A short list in the service's design doc | The service sibling |
| 2. Define resource types, roots and cells | The scope section and the classification table | The service sibling |
| 3. Define canonicalization, refusals, executor and credential requirements | The refusal table, the executor section, the bypass table | The service sibling |
| 4. Exercise the full local path against a local proof service | The fixture and the checklist's tests | `internal/<svc>fixture`, tests |
| 5. Record live compatibility separately from fixture proof | A dated compatibility note, or its absence stated | The service sibling's evidence section |

## 4. The classification table format

[`aws-s3.md` §3](aws-s3.md#3-the-classification-table) is the worked example; copy its shape.

### 4.1 Admitted forms

One row per admitted call shape. Columns, in this order:

| Column | Content |
| :--- | :--- |
| **Row** | A stable id: `R<n>` for reads and locals, `W<n>` for writes. Tests are named after rows |
| **Jail argv** (or request) | The exact grammar the jail may send: words, positional forms, flag spellings (`--x v` and `--x=v`), order freedom, repetition |
| **Canonical form** | What core executes: one fixed spelling per call. `—` for local |
| **Verdict** | `local`, `read`, or `write <cell>` |
| **Resource chain** | Leaf first, `<` reading "inside", ending at the root: `object s3://B/K < location ROOT` |
| **Description** | For writes: the one-line template the human sees at the ask, in the user's terms, with every resource id in it |

Rules for filling it:

- **Every write row binds what it consumes.** A write that reads stdin binds the stdin digest, so
  changed stdin asks again.
- **No row names a host file.** The executor runs on the host; a jail-named path would read or
  write host files. Stream through stdin and stdout instead.
- **A row needing a fact the argv lacks** (which project a workflow belongs to) declares a lookup
  ([`pack-brokers.md` §7.3](../pack-brokers.md#73-lookups)); say so in the chain column.
- **`local`** is only for `--help`, `--version` and `version` alone; core refuses any other local
  answer. On the CLI front a local answer runs nothing on the host and returns a refusal whose
  next step says to run it locally.

### 4.2 Refusals

| Class | Meaning | Next step shape |
| :--- | :--- | :--- |
| `credential` | The call carries anything that could choose or redirect the credential: endpoint, profile, account, region, server, token, signing, TLS, or any global option | Configure it on the host; the broker never takes it from the jail |
| `unsupported` | Any shape outside the classification table, or a name outside the service's grammar | The supported forms, or run it on the host |
| `scope` | A well-formed call on a resource no root covers | The config key to add the root to, then `yolo config apply` on the host |

Core requires a refusal to carry a non-empty class, reason and next step, each at most 512 bytes
and terminal-safe (`internal/callbroker/classifier.go:306`); the class string itself is not
checked, so these three are a recipe convention. A service adds a class only when a fourth kind of
next step exists.

**Fix the check order** so one call yields one class: `credential`, then `unsupported` shape, then
`unsupported` names, then `scope`.

## 5. Scope vocabulary

### 5.1 Roots

| Form | Source | When to use |
| :--- | :--- | :--- |
| `remotes` | The workspace's git remotes on `remote_host`, mapped through `root_template`, plus `brokered.<source>.repos` | The service's resources hang off a repository (CI projects) |
| `declared` | `brokered.<source>.roots` in workspace config, approved at the config-change gate | Anything else (buckets, teams, spaces) |

Core rejects at validation any root that is not an identifier (`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`,
no `.` or `..` segment). The service's **root grammar** is narrower, lives in its classifier, and
obeys one rule: **a root that does not parse is ignored, never widened.** It covers nothing; it is
never repaired into a root that would.

### 5.2 Resource types and chains

The pack declares types with parents. Core re-validates every chain a classifier returns
(`Classifier.validateChain`, `internal/callbroker/classifier.go:363`, checked 2026-11-09):

- each element's type is declared, its id is non-empty and has no NUL, CR or LF;
- **each type appears at most once**, and the chain is no longer than the declared type count;
- each element's declared parent is the next element's type;
- **the top element is parentless and its id equals an approved root**, verbatim.

A chain that breaks any rule is refused by core, whatever the classifier said. So the top type is
the root type (S3's `location`), and its id is the root string itself.

### 5.3 Coverage

A classifier maps a call to a root by a **deterministic** rule stated in its design: when several
roots cover, the longest root string wins. Two reasons: the answer must be a pure function of
argv and roots ([`pack-brokers.md` §7.2](../pack-brokers.md#72-the-classifier)), and the root is
part of a timed grant's identity ([BX-D19](../pack-brokers.md#BX-D19)), so a stable choice keeps
a timed answer meaning one thing.

### 5.4 Cells

| Field | Rule |
| :--- | :--- |
| `name` | Never `read`; reads use the loophole's read grant |
| `tables` | `working_set`, `all`, or both; a cell only in `all` can never be covered by the working set |
| `max_for` | The longest timed answer; absent means 15 min |
| `default` | Per table, whether it runs unasked; shown at the gate and capped by the user's ceiling ([`pack-brokers.md` §7.4](../pack-brokers.md#74-working-set-rules)) |

Start every write cell asking by default; a later slice may add a working-set default once a
`joins_working_set` rule gives the working set meaning.

## 6. Choosing a front

| Front | Status, 2026-11-09 | Use it when |
| :--- | :--- | :--- |
| **CLI** | Built and shipping; CircleCI uses it | The service has a host CLI whose admitted verbs can stream through stdin and stdout. **This is the default** |
| **HTTP** | Not published. G3 core is partial; the G4 front is built and starts in a selected launch, but publishes no URL, token or trust, and is unactivated ([`pack-brokers-http.md`](../pack-brokers-http.md), [plan](../pack-brokers-http-plan.md)). Custom signing is ruled header-only ([OQ-HTTP1](../pack-brokers-http.md#OQ-HTTP1)) but its wire is undesigned | Not yet. **An HTTP-fronted service cannot ship until G4 publishes** for its client and setup |

A service with no usable CLI waits for the HTTP front; it does not get a bespoke one.

## 7. Executor and credential requirements

Core's CLI executor ([`pack-brokers.md` §8.1](../pack-brokers.md#81-the-cli-executor)) runs the
host program with the canonical argv, an environment of exactly `PATH`, `HOME` and the declared
`env_from_host` read from the host broker's own environment, never the jail's, at most 3 MiB of
stdin and 32 MiB of output. The service's design states:

- **`env_from_host`**: every variable the host program needs, endpoint overrides included, so
  they are host-owned.
- **`credential_paths`**: the host paths holding the login, fenced from workspace `mounts`.
- **Redaction**: core redacts only declared environment values. A keyring or file login has no
  redaction backstop; the design names which admitted verbs could print one (ideally none).
- **Bypass table and claim** ([§10](#10-broker-policy-and-upstream-credential-reach)).

## 8. The local proof service pattern

One test-only binary per service, under `internal/<svc>fixture`, importing neither `callbroker`
nor the classifier, and imported by nothing shipped.

| Part | Behavior |
| :--- | :--- |
| **Fake service** | Implements only the admitted operations over loopback, with synthetic state and a **request log** of every request it received |
| **Fake host client** | Selected by the binary's basename (`aws`, `circleci`); implements only the canonical grammar and exits non-zero on anything else, so a canonicalization bug fails loudly |
| **Credentials** | Synthetic literals, delivered through declared `env_from_host` names so core also redacts them |
| **Credential check** | The fake service verifies the real signing or credential application (S3: SigV4 re-signing), so a pass proves the credential was applied host-side; unsigned or wrongly signed requests are refused and logged with state unchanged |
| **`serve` mode** | Starts the service for a human on the host, prints its address and state location, and supports seed, inspect and reset ([`pack-brokers.md` §12.3](../pack-brokers.md#123-host-operation-for-demos-and-learning)) |

The request log is what makes "a refused call reaches nothing" checkable.

## 9. The test checklist

A service is done when every item passes. Each test names its table row.

1. **Classifier unit table**: every admitted row with its exact canonical form and chain; every
   flag-spelling variant normalized; every refusal row with its class; root selection with
   overlapping roots; a malformed root ignored.
2. **Determinism**: the same input twice gives equal answers.
3. **Refusals reach nothing**: each refusal class, end to end, leaves the fixture's request log
   empty.
4. **In-process end to end**, through the generated shim, the authenticated front, the handler,
   the real classifier as a subprocess, the store, a real `yolo approve`, the executor and the
   fixture:
   - a read runs and returns fixture data;
   - a write asks and changes nothing;
   - Allow once runs it exactly once;
   - the same call again asks (spent);
   - changed stdin asks again;
   - an out-of-scope call refuses `scope`;
   - a redirecting flag refuses `credential`.
5. **Secret hygiene**: no jail-visible output contains the synthetic secret.
6. **Nested jail**: one read, one ask, one approve and one refusal through a real nested launch.
7. **Darwin build**: `GOOS=darwin go vet` on every new package.

The [conformance kit](../pack-brokers.md#19-what-done-looks-like) is not built (checked
2026-11-09); items 1 and 2 are what it would run.

## 10. Broker policy and upstream credential reach

Every service design carries a bypass table stating, separately:

- **Broker policy**: what the classification table admits through the intercepted command.
- **Upstream reach**: what any credential reaching the jail by another route can do — another
  pack's delegation, a user-granted file or mount, an SDK in the jail.

The confinement claim is then exactly: *the broker governs calls through the intercepted
command; it confines the service only when no other credential for it reaches the jail.*
[`aws-s3.md` §6](aws-s3.md#6-bypass-analysis-and-the-confinement-claim) is the example.

## 11. Walking it a second time

| File | Table-filling or new |
| :--- | :--- |
| `packs/<svc>/pack.toml`, its `loophole.toml` | Filling: copy the S3 pack, change names, cells, types, roots and env |
| `internal/<svc>classify/classifier_test.go` | Filling: one case per table row |
| `internal/<svc>classify/classifier.go` | Mostly filling: protocol envelope copied verbatim; the row matcher and name grammar are new per service |
| `internal/cli/internal.go` dispatch, `packs/embed.go` | One line each |
| `internal/<svc>fixture` | New per service: the fake must speak the service's protocol |
| End-to-end and nested tests | Filling: copy the S3 tests, change argv and fixture |
| The service's design doc | Filling: this document's sections in order |

So "mostly a table" is true for the manifest, tests and design, and only partly true for the
classifier and fixture. That is the input [SM-D5](#SM-D5) waits for.

> [!WARNING]
> Services that share a host CLI share its intercepted name, and intercepts are exclusive by name
> (`internal/packload/footprint.go:551`). A second AWS service cannot be a sibling pack that also
> intercepts `aws`; its sibling decides how AWS services share one name.

## 12. Next-service ordering (nonbinding)

Order by the recipe shape a service adds, not by wishlist position
([umbrella §6.3](README.md#63-complete-coverage-inventory)):

1. **Same shape as S3** (CLI, per-resource roots, simple writes): EC2's selected operations. Cheap;
   tests the shared-name problem.
2. **Query-shaped reads**: CloudWatch Logs. Tests query text, time ranges and sensitive results.
3. **REST with per-resource roots and no CLI**: issue trackers and wikis. Waits on the HTTP front.
4. **Posting as the user**: chat. Audience scope is a new resource kind.
5. **OAuth scope narrowing and query languages**: workspace suites, data warehouses. Hardest;
   last.

## 13. What this does not do

- **No shared table-driven helper package.** [`pack-brokers.md`](../pack-brokers.md)
  rules classifiers are programs ([OQ-BX2](../pack-brokers.md#decision-ledger)), which a pack may generate from a table. A shared Go
  helper is a candidate once a second service proves the common shape ([SM-D5](#SM-D5)).
- **No new core component, protocol or authority.**
- **No live compatibility claim** for any service.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="SM-D1"></a>SM-D1 | A service's design owes a classification table and a refusal table in the [§4](#4-the-classification-table-format) format, with the three refusal classes `credential`, `unsupported`, `scope` checked in that order | 2026-11-09 | [§4](#4-the-classification-table-format) | — |
| <a id="SM-D2"></a>SM-D2 | Root grammar is the classifier's; a root that does not parse is ignored, never widened; when several roots cover, the longest root string is the chain top | 2026-11-09 | [§5](#5-scope-vocabulary) | — |
| <a id="SM-D3"></a>SM-D3 | The CLI front is the default; an HTTP-fronted service does not ship until the HTTP front publishes for its client and setup | 2026-11-09 | [§6](#6-choosing-a-front) | — |
| <a id="SM-D4"></a>SM-D4 | Each service gets a local proof service under `internal/<svc>fixture`: fake service and fake host client in one test-only binary, synthetic credentials, real credential verification, a request log and a `serve` mode | 2026-11-09 | [§8](#8-the-local-proof-service-pattern) | — |
| <a id="SM-D5"></a>SM-D5 | No shared table-driven classifier helper until a second service proves the common shape; one example is not an abstraction | 2026-11-09 | [§13](#13-what-this-does-not-do) | — |
| <a id="SM-D6"></a>SM-D6 | Every service design states broker policy and upstream credential reach separately, and bounds its confinement claim accordingly | 2026-11-09 | [§10](#10-broker-policy-and-upstream-credential-reach) | — |
