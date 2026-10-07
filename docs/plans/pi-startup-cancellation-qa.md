---
title: "QA: Pi startup cancellation candidate"
status: accepted
stage: CURRENT
tags: [qa, pi, cancellation, startup]
summary: "Preserved regression evidence, blocking teardown findings and the audit required for the interrupted actual-backend repair."
---

# QA: Pi startup cancellation candidate

The implementation is outside main and stopped for an environment restart on 2026-10-07.
The [design](../design/pi-startup-cancellation.md) owns the repair contract and the
[plan](../design/pi-startup-cancellation-plan.md) owns its next work.

## Preserved evidence

Earlier workers reported Linux controlling-terminal, compiler-child, bounded pipe-drain,
interrupt-status and targeted race greens. Cleanup/reuse regressions were subsequently run
against unchanged baseline production callers, using only the baseline-compatible new test.
Saved exact cleanup/reuse mutations produced reds and were followed by restored greens.
The first modified-draft red remains preserved but is not pristine-baseline proof.

Independent review blocked three candidates in sequence:

1. Child process-group death does not prove detached keeper/container teardown; unconditional
   staging cleanup can delete paths a live capture still uses.
2. Container absence does not establish keeper completion or workspace launch ownership;
   retry must preserve the original backend rather than probe a newly chosen one.
3. A backend predicted from environment/PATH is not the backend selected by the real capture
   launch, including confinement-only native selection and automatic container fallback.

The interrupted final repair contains resolver callback wiring and candidate tests/docs for
actual backend persistence. It has no final worker report or independent rereview. Its QA
claims and any new mutation/restoration artifacts must be checked against the final tree;
none supersedes the latest blocking review.

## Next acceptance checks

- Verify the final production source is restored, not a leftover mutation, and record its diff.
- Prove the actual ordinary capture resolver persists its selected backend before native or
  container work, including child wiring, confinement-only guest and automatic fallback.
- Require workspace ownership, keeper completion and original-backend known absence before
  cleanup or same-key staging reuse; missing/contradictory records fail closed.
- Preserve ordinary successful native builds. Unknown native teardown retains/fences state
  and requests verification of that specific capture, not account-wide process cleanup.
- Re-run the targeted caller/race/mutation matrix on the final tree, then obtain independent
  rereview before parent combined quality/build/nested-launch/full integration gates.

Linux terminal tests do not prove macOS terminal ordering, native capture completion,
rootless Podman or Apple Container teardown. A compiler that deliberately leaves its process
 group is not reaped by group signaling; bounded output drainage detects only escapes that
retain descriptors and fails that build. The detached capture keeper is a separate lifetime.
