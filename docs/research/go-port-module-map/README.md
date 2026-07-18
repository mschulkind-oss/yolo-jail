# Go-port module maps

Contract-level maps of every module group, the test suite, the build/deploy
pipeline, and the applicable engineering standards — produced 2026-07-17 as the
research input to [`docs/plans/go-port-plan.md`](../../plans/go-port-plan.md).

**Read the relevant map before starting any port stage.** Each map lists the
module's externally observable surface (CLI flags, env vars, socket message
shapes, file formats, exit codes), seams, porting hazards, and test gaps, with
`file:line` references into the Python source as of commit `21183b3`.

Maps describe a moving codebase: line numbers drift as main advances, and two
entries in `cli-entry.json` carry inline `CORRECTED 2026-07-17` notes where the
original mapping was wrong. When a map disagrees with the source, the source
wins — port from code, never from these summaries.
