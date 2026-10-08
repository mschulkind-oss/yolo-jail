---
title: "QA: host-service startup diagnostics"
status: accepted
stage: CURRENT
tags: [qa, diagnostics, host-services]
summary: "What the landed startup diagnostics were verified by, and what only a real Mac, rootless host or live AWS login can show."
---

# QA: host-service startup diagnostics

The [design](../design/host-service-startup-diagnostics.md) owns behavior; the
[task checklist](host-service-startup-diagnostics-tasks.md) names the test behind each row.
Status 2026-10-08: every task row is checked.

## Verified

- Whole launches through `Run()`, with the keeper running in-process, show a cooperative
  configuration refusal reaching the terminal through the container and macos-user keepers,
  a non-configuration refusal staying a warning printed before its derived symptom, and a
  settings-preflight refusal stopping both arms before keeper, image and daemon work. Deleting
  each forwarding call (the keeper's relay, either arm's preflight refusal, the start
  boundary's hand-back, `HostDoorways.Start`'s own preflight) fails a test.
- Lifecycle: private validator and daemon snapshots are `0600` and removed on pass, refusal,
  timeout, spawn failure and teardown; a legacy manifest keeps the stable settings path; a
  valid changed snapshot restarts the singleton onto exactly the validated bytes; a failed
  validator or preparation leaves a live singleton, its settings file, record and fronts
  unchanged; record-less exits and timeouts return within their bounds even when a grandchild
  holds the channel; a daemonizing wrapper stays ready.
- An accepted refusal ends the readiness wait, and a refusing daemon the attempt spawned is
  stopped. Text sanitizers drop control and format characters. `yolo check` skips validators
  of services its backend does not start and, for a refused, timed-out or unstartable
  validator, only that service's doctor.
- AWS fixtures use sentinel profile and policy values and never call AWS.

## Not shown by these tests

Linux fixtures and Darwin compilation do not establish native Mac behavior, real rootless
loopback forwarding, live AWS authentication or billing-tag propagation. A nested jail cannot
show the first two (see the carve-outs in the repository's agent guide).
