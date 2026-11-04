---
title: "Pack brokers: implementation plan (seed)"
date: 2026-11-04
status: draft
stage: SKETCH
depends-on:
  - pack-brokers.md#OQ-BX1
  - pack-brokers.md#OQ-BX2
  - pack-brokers.md#OQ-BX3
tags: [plan, seed, broker, packs, extension-point]
summary: "Seed of the implementation plan for pack-brokers.md: notes the design work surfaced. Not a hand-off; the design wins on behavior."
---

# Pack brokers: implementation plan (seed)

**Status:** 2026-11-04 — seeded during design; not a hand-off, and incomplete until the design is
accepted.

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
- **Linear facts to confirm:** the header form for personal keys versus OAuth tokens; the upload
  flow's hosts; complexity limits per request.
- **The HTTP front in the jail** is a `yolo-jaild` daemon; check how `wire-bridge` publishes its
  endpoint file and reuse that shape.
- **Apple Container admission** (`internal/cli/run/loopholeinert.go`) lists admitted daemons by
  name; decide how a pack broker is reported there.
- **Classifier latency**: measure one subprocess per call against the 2 s deadline.
- Blocked on [OQ-BX1](pack-brokers.md#OQ-BX1) (executors), [OQ-BX2](pack-brokers.md#OQ-BX2)
  (classifier form) and [OQ-BX3](pack-brokers.md#OQ-BX3) (where the CircleCI pack lives).
