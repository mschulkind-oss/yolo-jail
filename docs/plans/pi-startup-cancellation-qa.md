---
title: "QA: Pi startup cancellation candidate"
status: accepted
stage: CURRENT
tags: [qa, pi, cancellation, startup]
summary: "Regression evidence, the review findings and their fixes, and the checks left for the parent."
---

# QA: Pi startup cancellation candidate

The implementation is on the local branch `lane/pi-cancel`, not on main (2026-10-08).
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

## Review and lane verification, 2026-10-08

One independent review of the repair commit found one blocker and two majors, all fixed
with tests that fail when the fix is removed:

- A Ctrl-C before the child recorded a runtime fenced that build key for good. Now a child
  whose process group is confirmed gone with no runtime record is not retained.
- A retained workspace was recorded as a failed build with back-off. It is now its own
  error, which a patched advance records nothing for.
- A good build failed when container removal trailed the keeper. Admission now polls the
  original backend for up to 30 seconds.

Two minors are fixed (records survive a partial cleanup; a wrong "retained" message). The
partial-cleanup fix has no test, because the suite runs as root, where `RemoveAll` cannot be
made to fail. A corrupt runtime record still falls back to a valid keeper record, as a
deliberate existing test pins. macos-user's retry ignoring its run-returned record is open.

Green on Linux after rebasing onto main: `go test -short` for `internal/cli` and
`internal/cli/run`, `just lint-ci`, `GOOS=darwin go vet ./internal/cli/`, and the four
integration tests `TestForkBuildDeliversTheForkInPlaceOfItsBase`,
`TestPatchedForkFollowsItsUpstreamAndHoldsAtAConflict`,
`TestPatchedExtensionIsBuiltMountedReadOnlyAndHeldAtAConflict` and
`TestAnUnmodifiedNpmExtensionIsBuiltOnTheHostAndMountedReadOnly`. Those four had failed
in-jail before main's change running the suite with `YOLO_VERSION` unset.

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
