---
title: "Instruction files from the repository: build seed"
date: 2026-11-24
status: draft
stage: SKETCH
tags: [instructions, packs, plan-seed]
summary: "What the design work surfaced for the build of repository instruction-file modes; not a hand-off, and incomplete until the design is accepted."
depends-on: [workspace-instruction-files.md#OQ-IF1, workspace-instruction-files.md#OQ-IF2]
next: "Wait for the design's two rulings, then a planner completes this against the tree"
---

# Instruction files from the repository: build seed

**Status:** 2026-11-24 — seeded during design; not a hand-off, and incomplete until the design is accepted. The
design ([workspace-instruction-files.md](workspace-instruction-files.md)) wins on behavior.

Blocked on [OQ-IF1](workspace-instruction-files.md#OQ-IF1) (what the mask covers) and
[OQ-IF2](workspace-instruction-files.md#OQ-IF2) (refuse or disclose).

## What surfaced

- Check whether `internal/packdecl` can take a new kind without the strict host decoder refusing older
  entrypoints; the `autonomy` `lists` note says an older entrypoint drops an unknown nested field, so install the
  host yolo first.
- The claude entries need `${workspace}` substitution in a list entry; check that `config-list` bodies take it
  the way `config` map keys do.
- `yolo host -- claude --settings '<json>'`: nothing measured shows claude merges `claudeMdExcludes` from that
  layer; the design marks it UNMEASURED, so the first build slice should record a Mac and Linux witness.
- Codex's `-c` handoff already exists as the `launch_selection` `each` form; reuse its argv placement rules.
- pi's redelivery needs a second `[[briefing]]` destination used only while ignoring; check how
  `agent-briefings.md` composes two destinations for one agent.
- Copilot: a measurement that its personal file survives `--no-custom-instructions` would promote it from
  fallback-only; it needs a session-free probe, if one exists.
- Add the `repo_instructions` row to `userguide/reference/settings-per-setup.md` and the config reference text.
