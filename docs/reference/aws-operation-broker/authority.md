---
title: "AWS broker: authority and roots"
status: accepted
stage: GRADUATED
verified: 2026-11-17
verified_commit: bde502e4a
covers: [internal/config/callroots.go, internal/config/callbroker.go, internal/config/gateread.go, internal/config/readableallow.go, internal/configmigrate/lower.go, packs/aws, internal/awsclassify]
tags: [aws, config, authority]
---

# AWS broker: authority and roots

**Status:** Verified 2026-11-17 against `bde502e4a`. Linux synthetic config,
mediation and live nested gate witnesses; no real account or native Mac claim.

A service is admitted by three independent conditions: a standing user `read`
grant, a gate-approved workspace root covering this service's call, and an exact
[classification row](grammar.md). Root context is not a grant or a write ceiling.
Write rows additionally pass the generic cell decision and approval transaction.

## Service isolation principles

**P1. Each service owns exactly one root scheme, and reads only roots in it.**
S3 uses `s3://`; Cost Explorer uses `ce://`. Other schemes are invisible, never
parsed or widened by that table.

**P2. The top of a chain always carries the scheme of the service the first argv
word named.** Neither service's root can stand under the other's call.

**P3. A new service ships with a new scheme.** An existing read grant and existing
roots do not admit it. The workspace must declare and approve its new root.

Core's `validateChain` checks the root id verbatim against approved scope. The
classifier's root grammar narrows that generic check; it does not replace it.

## Declared roots and authorship

Workspace TOML uses the generic `[call_roots]` table:

```toml
[call_roots]
aws = ["s3://demo-bucket/team/", "ce://host-login"]
```

Legacy workspace JSONC uses `brokered.aws.roots`. TOML `brokered`, legacy
`call_roots`, and `requests.aws.roots` are refused spellings. User-scope roots and
outside-workspace declaring files are refused. A source must be declared by a
selected pack in declared-root mode; remote-derived sources cannot use this key.

The readable loader moves values **and writers** to canonical
`brokered.<source>.roots`. Approval parts and root records remain canonical;
diagnostics and `yolo config why call_roots.aws` recover the public TOML key and
its declaring file/line. Equivalent TOML/JSONC roots have equal canonical values,
not equal filename bindings: the gate separately binds the literal declaring file.

Lists preserve readable union behavior; a local empty list resets the project
list. Duplicate effective roots fail existing validation. Writing both format
spellings for the same source refuses and names both writers. Different sources
can compose without a format-key residue in approval data.

> [!WARNING]
> Format migration safely refuses declared roots **before** touching originals,
> backups or new files. A filename change cannot preserve the old approval by
> rewriting provenance. Re-declare through the supported format and pass the gate;
> do not silently drop roots or relax filename binding.

Use the editing verbs rather than guessing a file spelling:

```console
yolo config add call_roots.aws ce://host-login --project
yolo config why call_roots.aws
yolo config check
yolo config apply
```

The last command is host-side approval. These commands do not grant user read
allowance. Planned edits resolve literal pack grant tokens through the owning
loaded declaration's existing `ResolveLoopholeGrants` reader on the original
input. Only table/cell tokens enter returned write policy lists. This makes edits
work with `always=["read"]` or `on_request=["read"]` without scoped READ or new
permission semantics. A custom non-AWS grant regression pins that separation.

## Coverage

| Root | Coverage |
| :--- | :--- |
| Valid S3 bucket root | Objects/listings in its own bucket |
| Valid S3 prefix root ending `/` | Keys/listing prefixes starting with that prefix |
| Exact `ce://host-login` | Only the admitted CE queries, as host IAM sees them |
| Any other CE root, or unsupported service scheme | No coverage |

S3 picks the longest covering root string as its parentless `location`. Bucket,
key and prefix acceptance is defined in [the grammar](grammar.md#s3-names).
A prefix root does not cover a shorter listing prefix without its slash.
No malformed root is repaired. Adding a longer root changes the write chain,
so a timed answer on a shorter root does not automatically cover it.

The CE root is deliberately host-login-wide. There is no injected account filter,
role pin or billing-view selection. User scope cannot separately allow S3 reads
while refusing CE reads: their shared grant admits reads, and workspace roots
choose the services. Remove the workspace CE root to stop the next CE call.

## Read grants and write cells

The `read` grant remains requestable by default, but is not granted implicitly.
Both services independently require current read authority. CE is standing read,
not a cell; it never asks for a write approval.

S3 writes name `put_objects` or `delete_objects`. A cell belongs to the service
whose rows name it. A later service's writes require new cell names, not reuse of
an existing put ceiling. Timed answers bind the full cell/resource chain; changed
stdin requires a new Allow-once answer, not a body-insensitive replay.

| Current value | Setting | Defined in |
| :--- | :--- | :--- |
| Public TOML declaration | `call_roots.<source>`; workspace only | `internal/config`, readable schema and `lowerReadableCallRoots` |
| Canonical/legacy declaration | `brokered.<source>.roots` | `internal/config`, `WorkspaceRead.CallRoots` |
| CE location | Exactly `ce://host-login` | `internal/awsclassify`, `classifyCE` |
| Put tables / timed cap | `working_set`, `all`; 1h | `packs/aws`, call-broker manifest |
| Delete table / default timed cap | `all`; 15m | `packs/aws`; generic broker policy default |

## Direct replacement

A configuration selecting `aws-s3` fails unknown-pack validation: select `aws`
with `yolo pack add aws`. Old declared roots have no selected source; re-declare
under the new public key and approve again. Old allowance tokens/grants and
pending/Allow-once/timed records do not authorize `aws`; there is no migration.

Removed-source history may be displayed as deny-only inventory. It is neither an
active request nor compatibility. The permanent caller
`TestAWSNeverConsultsRetiredSourceRequestsOrAnswers` pins no old execution, replay
or spending without deleting or hiding historical records.
