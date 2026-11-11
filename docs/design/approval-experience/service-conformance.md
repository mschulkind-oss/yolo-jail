---
title: "Service conformance: reuse the proof, not the classifier"
status: accepted
stage: BUILT
next: "Independent review and combined-tree gates; retain partial §19 scope"
depends-on:
  - service-mediation.md
  - ../pack-brokers.md
tags: [design, testing, broker, packs]
---

# Service conformance: reuse the proof, not the classifier

**Status:** 2026-11-11. Bounded kit implemented from `017ea6df9`, with focused
checks passing. The new assertion package and S3/tasks adapters are test-only;
independent review and combined-tree landing gates remain. Evidence and fixture
corrections are recorded in [the plan](service-conformance-plan.md#implementation-evidence).

> **In short.** A reusable test kit checks service-supplied examples against the existing
> classifier and approval/execution path. It supplies assertions, never classification,
> scope, credentials or approval authority.

**The shape.** One test-only assertion package; two adapters in call-broker tests: shipped
S3 and synthetic tasks. The adapters own fixtures; the kit owns repeated comparisons.

**Cost.** This slice is a Go test command, not the standalone pack-author command promised
by [pack-brokers §19](../pack-brokers.md#19-what-done-looks-like).

**Needs your ruling:** None for this bounded slice.

**Reads with:** [implementation plan](service-conformance-plan.md),
[service recipe](service-mediation.md), [S3 contract](aws-s3.md).

---

## 1. Scope and precedence

The accepted pack contract and service tables remain authoritative. This design records
only the requested bounded kit: executable examples and common checks, demonstrated through
an existing real-service adapter and a **test-only** synthetic adapter. It does not relax
SM-D5: there is no shared table-driven production classifier helper and no second real service.

Do not add a CLI verb, generated classifier language, EC2/third service, HTTP-front
activation, private integration, credential route, grant, default scope or approval authority.
Do not change S3's existing tests or core code to accommodate the kit.

## 2. Two assertion surfaces

**Classification cases** carry a stable service row id, original argv/stdin, and either the
exact expected protocol answer or an expected error substring. The kit invokes the adapter
twice with the same input and checks determinism and expectation. Answers are compared as
JSON values, not raw formatting; array order remains significant. Positive answers include
canonical argv, stdin digest, complete ordered chains/cells/descriptions and refusals.
An empty table, missing case name, invalid expected JSON, or missing expectation is a kit
error, not a green test. Negative core-validation cases must fail both invocations.

**Execution steps** carry a stable name, an action callback, exact expected verdict and
counts (writes, pending requests, remaining exact-grant uses), required/forbidden output
fragments, and an optional expected error substring. The kit runs them in listed order,
failing at the first mismatch with the step name. A callback returns observations from
actual fixtures/store snapshots; it cannot supply an expected outcome as its observation.
Execution-step callbacks, rather than a generic service factory, keep process/credential
ownership with the adapter. Empty scripts and missing actions/names/verdicts are kit errors. An error expectation
cannot also expect the success verdict `runs` or `approved`; `refused` with an expected
error remains valid.

Counts and output assertions are shared; fixture-specific object-byte, upstream request-log,
audit and credential assertions stay with the adapter. A request log is not inferred from
write count: a refused S3 call must leave **both** unchanged. Prediction and asking cannot
write, but a service with declared lookups may legitimately perform read lookups.

The package returns named errors, so unit tests can prove that a deliberately broken adapter
is rejected without launching services. It does not import callbroker or any classifier;
callbroker's package-local test adapters can therefore import it without a cycle.

## 3. Consumer paths and negative controls

S3 uses the shipped declaration, actual subprocess classifier, generated shim,
authenticated front, selected live handler/store, built host `yolo approve`, core CLI executor,
and the existing SigV4-verifying loopback fixture. Classification checks use core's
`Classifier.ClassifyCLI`, not a direct classifier function.

Synthetic tasks reuse the existing selected test pack and subprocess classifier, plus
package-local handler entry for evaluate/ask/act and the built `yolo approve` for answers.
They supply a different resource vocabulary and argv table; no production dispatch is added.
Injected malformed answers are executable classifier outputs fed through core's caller,
not direct calls to its private validator. The synthetic adapter can exercise executor output
limits without changing S3 or making a real upstream request.

The shared lifecycle checks: read runs; evaluate predicts without filing/writing; ask files
without writing; host approval does not execute; evaluate after approval predicts runs
without spending; changed stdin asks and cannot spend the original grant; exact act writes
once; replay asks. Fresh fixtures isolate expiry, stale permissions and oversized-output
controls. Every stale/invalid case must retain the exact grant when execution never started.

Expiry uses the store's injected clock beyond the recorded grant expiry, not sleeping.
A completed write with an oversized response is **not** described as a safe refusal: the
write occurred, the grant is spent, partial output is withheld, the next step says inspect
host state, and replay needs a new approval. This is the existing completed-response contract.

## 4. Evidence and exclusions

Report each covered case by row/control name, command result and actual consumer path.
Do not claim full [§19](../pack-brokers.md#19-what-done-looks-like): this slice does not provide a general author command, automatic
arbitrary-pack discovery, a block-loading report for every pack, exhaustive lookup checks,
an HTTP proof, a new nested-launch proof or live AWS compatibility. Existing nested S3
coverage remains separate evidence, not a test the new kit ran.

Build fixture binaries before changing execution HOME, using the original effective Go
build environment and shared caches, preserving owner overrides. Only execution uses the
isolated credential home. All service endpoints and credentials are local/synthetic.

## Decision ledger

| ID | Decision | Authority |
| :--- | :--- | :--- |
| SC-D1 | Reuse assertions over callbacks; no production classifier abstraction | Bounded task; SM-D5 remains intact |
| SC-D2 | Demonstrate with shipped S3 and test-only synthetic tasks | Bounded task; no second real service |
| SC-D3 | Exercise actual classifier, host approval and executor callers | [Service recipe §9](service-mediation.md#9-the-test-checklist); [pack-brokers §19](../pack-brokers.md#19-what-done-looks-like) |
| SC-D4 | Report partial coverage, not completion of the general author kit | Bounded task; [pack-brokers §19](../pack-brokers.md#19-what-done-looks-like) |
