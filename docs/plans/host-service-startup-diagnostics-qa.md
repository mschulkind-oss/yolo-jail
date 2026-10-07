---
title: "QA: host-service startup diagnostics candidate"
status: accepted
stage: CURRENT
tags: [qa, diagnostics, host-services]
summary: "Bounded candidate results and the remaining acceptance checks; none establishes a landed startup-diagnostics feature."
---

# QA: host-service startup diagnostics candidate

The implementation is outside main and stopped for an environment restart on 2026-10-07.
This record distinguishes completed bounded work from incomplete whole-feature acceptance.
The [design](../design/host-service-startup-diagnostics.md) owns behavior; the
[task checklist](host-service-startup-diagnostics-tasks.md) owns remaining implementation work.

## Completed bounded work

- The ordering/isolation worker reported a passing full CLI/run package suite after repairing
  seal-fixture isolation, plus related package suites and production-call ordering mutations.
- The lifecycle/channel worker reported passing focused package and race suites, per-jail
  interleaving and validator-input overwrite regressions, keeper snapshot consumption,
  real host-doorway refusal, singleton publication serialization, attempt attribution and
  bounded inherited-output tests. Its temporary mutations were reported restored.
- AWS fixture tests exercised the actual refusal producer and launch renderer with sentinel
  profile and policy values, without invoking AWS APIs, credentials or an agent CLI.

These reports cover bounded slices, not the final interrupted tree. Two earlier attempts
reported incomplete required criteria and were rejected. The final contract/documentation
worker stopped without its final report. Its reference edits, checklist marks and additional
claimed results require audit; they are not independent acceptance or reference graduation.

## Remaining acceptance

- Exercise full native launch output and an external keeper's frames/final client result;
  immutable plan consumption alone does not prove refusal propagation to the terminal.
- Complete dynamic validator admission, private-file permissions/cleanup, legacy stable-path,
  valid singleton replacement and existing-client/front preservation cases.
- Cover validator timeout/executable failure doctor skipping while unrelated doctors continue.
- Complete the outcome/readiness matrix for both ownership paths, including clean daemonizing
  wrappers, absent/late/malformed records, process exit, timeouts and transport faults.
- Preserve exact mutation patches and restored greens. A source-order deletion check is not
  a substitute for a behavior-level caller regression. Host-doorway refusal can remain green
  when its preflight is deleted because a lower-level preflight masks that deletion.
- Reconcile every candidate reference/changelog statement with accepted behavior, then obtain
  fresh independent correctness and safety review.
- Parent runs the final combined quality gate, Go build, fresh-binary isolated nested smoke
  and applicable full integration suite before integrating runtime source.

Linux fixtures and Darwin compilation do not establish native Mac behavior, real rootless
loopback forwarding, live AWS authentication or billing-tag propagation. No permission
widening, live configuration change or publication is authorized by these candidate results.
