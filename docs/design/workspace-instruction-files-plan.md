---
title: "Instruction files from the repository: build seed"
date: 2026-11-24
status: draft
stage: SKETCH
tags: [instructions, packs, plan-seed]
summary: "What the design work surfaced for the build of repository instruction-file modes; not a hand-off, and incomplete until the design is accepted."
next: "Build the design; this seed records what the build surfaced"
---

# Instruction files from the repository: build seed

**Status:** 2026-11-24 — seeded during design; not a hand-off, and incomplete until the design is accepted. The
design ([workspace-instruction-files.md](workspace-instruction-files.md)) wins on behavior.

Both rulings are in: no mask ([IF-D14](workspace-instruction-files.md#10-decision-ledger)) and a launch that
continues and discloses ([IF-D15](workspace-instruction-files.md#10-decision-ledger)).

## What surfaced

- Check whether `internal/packdecl` can take a new kind without the strict host decoder refusing older
  entrypoints; the `autonomy` `lists` note says an older entrypoint drops an unknown nested field, so install the
  host yolo first.
- The claude entries need `${workspace}` substitution in a list entry; check that `config-list` bodies take it
  the way `config` map keys do.
- `yolo host -- claude --settings '<json>'`: the 2.1.331 binary reads `claudeMdExcludes` from the merged settings,
  which include the `--settings` source (design Appendix A); a Mac witness is still unrecorded and needs a live `claude` session, so it waits for the owner (a Mac pass on 2026-11-24 could not run it).
- Codex's `-c` handoff already exists as the `launch_selection` `each` form; reuse its argv placement rules.
- pi's redelivery is the briefing file handed back through `--append-system-prompt`, so no second destination
  is composed.
- Copilot is unsupported ([design §4](workspace-instruction-files.md#4-the-per-agent-mechanisms)); a measured
  session showing its personal file survives `--no-custom-instructions` would promote it.
- Add the `repo_instructions` row to `userguide/reference/settings-per-setup.md` and the config reference text.
