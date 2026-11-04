---
title: "Pack brokers: implementation plan (seed)"
date: 2026-11-04
status: draft
stage: SKETCH
next: "Complete the tree-grounded hand-off for built-in CircleCI, local proof services and host demo controls"
depends-on:
  - pack-brokers.md#OQ-BX1
  - pack-brokers.md#OQ-BX2
  - pack-brokers.md#OQ-BX3
tags: [plan, seed, broker, packs, extension-point]
summary: "Seed of the implementation plan for pack-brokers.md: notes the design work surfaced. Not a hand-off; the design wins on behavior."
---

# Pack brokers: implementation plan (seed)

**Status:** 2026-11-04 — the design is accepted; this seed still needs a tree-grounded build
hand-off before implementation.

This seeds the plan for [`pack-brokers.md`](pack-brokers.md). Where the two disagree on behavior,
the design wins. Do not build from this file while it is a sketch.

## Notes the design surfaced

- **Source-neutral approvals.** `internal/cli/approve.go` lists the GitHub and git stores through
  their `ApprovalStore()` and has GitHub-only branches (`allowOnceOnly`); find every such branch in
  `approve`, `wait`, `history` and the config validator before replacing them with declared sources.
- **Manifest spellings** in the design's `call_broker` sketch are the plan's to settle against
  `internal/loopholedecl` conventions (`brokered.go`'s census, token rules).
- **The cells validator** for `[requests.<pack>]` today knows GitHub's cells; it must learn declared
  cells, and the working-set invariant check must run on them too.
- **CircleCI CLI facts to confirm at the current release** (from documentation, not run):
  each verb's exact name and flags with `circleci <verb> --help`; whether `pipeline create` defines
  or triggers; whether `job artifact` can stream to stdout; whether `config validate` reads stdin;
  whether API v3 masks environment variable values; how a `circleci/<org>/<project>` project names
  its repository.
- **Local proof services:** plan a test CLI client and HTTP service with isolated synthetic
  state/credentials, a finite GraphQL route, host start/seed/inspect/change/reset/stop controls
  and a short demo guide. Use independently loadable fixture packs to prove extension without
  core service-name edits. Nested-jail tests drive real broker paths and inspect service state,
  write logs and approval use; no Linear or CircleCI account is needed.
- **Built-in CircleCI:** ship `packs/circleci` with a yolo classifier subcommand, not a manifest
  binary; exercise its actual classifier using a fake host CLI. Treat upstream CLI/login/cloud
  compatibility as separate evidence, not as a result of the synthetic tests.
- **The HTTP front in the jail** is a `yolo-jaild` daemon; check how `wire-bridge` publishes its
  endpoint file and reuse that shape.
- **Apple Container admission** (`internal/cli/run/loopholeinert.go`) lists admitted daemons by
  name; decide how a pack broker is reported there.
- **Classifier latency**: measure one subprocess per call against the 2 s deadline.
- **Settled behavior:** core executors by default plus a pack executor where needed; one
  program classifier interface; built-in CircleCI; local human-operated proofs; optional denial
  notes across sources; gate-shown working-set defaults capped by the user ceiling
  ([owner rulings](pack-brokers.md#decision-ledger)). Map independent tasks against the access
  hand-off without stalling source-neutral or fixture work on unrelated implementation.
