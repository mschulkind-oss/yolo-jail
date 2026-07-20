# Active plans & designs

This directory holds the **active** work — plans and designs we're currently
implementing or still discussing. Reference docs (how live systems work) live in
[`../design/`](../design) and [`../research/`](../research); done/obsolete
working docs are archived in git history (see [`../DOC_TRIAGE.md`](../DOC_TRIAGE.md)
for the classification and `git log --follow` to recover any).

| Doc | What it is | Status |
|---|---|---|
| [macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md) | The macOS-backend revival + source-distribution roadmap (Tracks J/D/M). | **In progress** — J1.1–J1.4, D1, D3 landed 2026-07-20; J2, D2, D4, J3, Track M remain. |
| [handoff-cachix-cache.md](handoff-cachix-cache.md) | Procedure to publish the prebuilt OCI image to a Cachix binary cache (= revival plan **D4**). | **Human-gated** — wired; needs a Cachix account created, then uncomment `flake.nix` nixConfig + set the CI secret/var. |
| [agent-settings-composition.md](agent-settings-composition.md) | RFC for the "Prism" model — composing agent settings across host and jail layers. | **Proposed, unbuilt** — nothing implemented; its "what exists today" section predates the Go port and needs re-grounding in `internal/config` + `internal/entrypoint` before execution. |

Related live tracker: [`../research/macos-support-matrix.md`](../research/macos-support-matrix.md)
is the authoritative state-of-the-macOS-backend matrix.

## Broader post-Go-port backlog (not yet re-homed here)

Beyond the macOS revival plan, the archived `go-port-post-transition.md` (git
history) still lists open, non-macOS work: **nix-ld** (replace the
`LD_LIBRARY_PATH` workaround, an image change — see
[`../design/mise-node-dynamic-linking.md`](../design/mise-node-dynamic-linking.md)),
**module consolidation** (~60 `internal/*` packages mirror the old Python
layout), and an **OSS-hygiene sweep**. If any of these get picked up, add a plan
here.
