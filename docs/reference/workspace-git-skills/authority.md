---
title: Skill import authority
status: accepted
stage: CURRENT
verified: 2026-11-16
verified_commit: 6b8879611
covers:
  - internal/gitbroker/importdecision.go
  - internal/gitbroker/importfetch.go
  - internal/skillimports/resolver.go
  - internal/skillimports/store.go
  - internal/cli/skills.go
  - internal/cli/run/workspaceskills.go
summary: Exact approved URLs and current broad read grants bound both fetches and cache delivery.
---

# Skill import authority

**Status:** Verified 2026-11-16 against `6b8879611`; focused cached-reader and
public-command regressions passed. This is not native host/rootless-network evidence.

[Imports and access classes](README.md#vocabulary) separate content selection from
permission. A workspace declaration names content; the existing Git broker decides
whether that URL may be fetched. Running the resolver on the host adds no authority.

## Request inputs

A fresh launch resolves only after its config gate succeeds. It supplies the approved
Git remote URLs and read grants, narrowed against current remote URLs and the current
user read grant. A host command uses this workspace's last approved scope/grants,
also narrowed by current state. Failed current-remote reads yield empty current scope.
`AuthorityFromWorkspace` in `internal/skillimports` assembles these read-only inputs.

Current state can **remove** access but cannot add it before a gate records approval.
The import URL is never inserted into the remote list; declaring it creates no grant,
scope row or approval file. Commands do not approve their own workspace changes.

A command snapshots approved authority once. Concurrent changes to that record are
not observed mid-command, but every later launch/attach re-decides before delivery.

## URL and grant are separate

The broker compares the **exact URL** with approved/current URLs. Repository identity
is a normalized host/repository name used for grants; identity equality does not make
another URL approved. See [broker admission](../git-broker.md#admission-scope-and-read-grant).

| URL and current authority | Fetch decision |
| :--- | :--- |
| Exact approved/current URL plus approved/current broad read coverage | Authenticated route allowed |
| HTTPS without that coverage | Anonymous attempt only; no credential acquisition |
| SSH/scp without that coverage | Refused |
| Unsupported transport or URL credentials | Refused during validation |

Repository-scoped ceilings are write-only. No scoped-read interpretation or new
credential channel exists here. The broad Git read grant remains the read authority.
A host command with no approval gets empty authenticated authority: public HTTPS can
still be tried anonymously, while SSH/scp is refused and names the fresh-launch step.

Anonymous HTTPS is existing broker policy, not a fallback after a credentialed failure.
An import cannot retry outside the broker or use user-scope pack fetching after refusal.
A URL outside scope can cause a host anonymous request to that server; the URL in a
human-mediated host-run line therefore deserves inspection before the human runs it.

## Transport and executable placement

`FetchImport` uses the existing remote-helper protocol and broker upstream checks for
fetch. It adds no jail protocol operation. The host process carries the bytes; its
transport route is not a grant. Credential acquisition remains on the broker's
credentialed path and rechecks authority as that path already requires.

Git runs in controlled import scratch/object storage, not in the checkout. Workspace
Git settings cannot supply `credential.helper`, `core.sshCommand`, URL rewriting,
hooks, filters or submodule behavior. Only committed objects are read.

`TrustedImportGit` applies the placement guard to **cached readers too**: object
existence checks, inspection, projection and fetching may not execute an agent-writable
workspace Git. Fetch resolves and pins that executable through its operations.
An offline cache does not justify relaxing executable trust.

## Cached bytes are not authority

The record's access class describes how its commit was obtained. Delivery requires
a current decision at least as strong:

| Recorded access | Current decision | Reader behavior |
| :--- | :--- | :--- |
| Anonymous | Authenticated or anonymous | Eligible, if record and objects remain valid |
| Authenticated | Authenticated | Eligible, if record and objects remain valid |
| Authenticated | Anonymous only | Withheld until fresh resolution obtains it anonymously |
| Either | Refused | Withheld, regardless of objects on disk |

A fresh launch can fetch under anonymous access and replace the record's class.
Attach/list are readers and cannot perform that downgrade themselves. Failure to obtain
eligible content withholds it; no wider route or credential retry is available.
Revoking a remote/read grant therefore stops delivery of credential-only cached content.

Record validity also ties it to configured source/ref or pin. Damaged, inaccessible,
stale or malformed records count as absent, not permission. A pin may reuse an existing
matching moving record when its commit/source/ref match; pinning needs no refetch.
Objects are partitioned by workspace identity, so another workspace's access cannot
supply this workspace's content.

## Failure and observation

Malformed entries fail ordinary config validation before resolution. Runtime access
failures withhold the affected import and allow launch to continue with actionable
warning text. [Projection observations](projection.md#observations) distinguish missing
state, refused objects, shadowing and actual writer events.

The host `yolo skills list` gives the recorded access and current delivery eligibility
without fetching, staging, writing state or reading manifests. Jail list cannot see
host records. A cache entry or list row never claims a running jail changed its skills.

## Where the code lives

| Responsibility | Anchor |
| :--- | :--- |
| Current/approved intersection | `DecideImportAccess` in `internal/gitbroker`; `AuthorityFromWorkspace` in `internal/skillimports` |
| Filtered object fetch | `ImportFetchRequest`, `FetchImport` in `internal/gitbroker` |
| Trusted executable | `TrustedImportGit` in `internal/gitbroker` |
| Record eligibility and access comparison | `Resolution`, `ResolveLaunch`, `cacheAccessAllows` in `internal/skillimports` |

No configurable access-class bypass or cache-authority hatch exists. This page owns
policy shape, not a duplicate list of broker capabilities or config editing verbs.
