# Active plans & designs

This directory holds the **active** work — plans and designs we're currently
implementing or still discussing. Reference docs (how live systems work) live in
[`../design/`](../design) and [`../research/`](../research); done/obsolete
working docs are archived in git history (see [`doc-triage.md`](doc-triage.md)
for the classification and `git log --follow` to recover any).

## macOS revival + distribution

| Doc | What it is | Status |
|---|---|---|
| [macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md) | The macOS-backend revival + source-distribution roadmap (Tracks J/D/M). | **In progress** — J1.1–J1.4, D1, D3 landed 2026-07-20; J2, D2, D4, J3, Track M remain. |
| [handoff-cachix-cache.md](handoff-cachix-cache.md) | Procedure to publish the prebuilt OCI image to a Cachix binary cache (= revival plan **D4**). | **Human-gated** — wired; needs a Cachix account created, then uncomment `flake.nix` nixConfig + set the CI secret/var. |

## Post-Go-port backlog

The archived `go-port-post-transition.md` (git history) queued work for after the
Python→Go cutover. §2 distribution landed. The still-open items are now tracked
here:

| Doc | What it is | Status |
|---|---|---|
| [nix-ld-dynamic-linking.md](nix-ld-dynamic-linking.md) | Replace the `LD_LIBRARY_PATH=/lib:/usr/lib` whack-a-mole with nix-ld so the mise node + MCP servers link env-free (closes the custom-`mcp_servers` startup gap). | **Open** — decided, not started; host-gated image change. |
| [cli-color-audit.md](cli-color-audit.md) | Make `prune`/`builder`/`macos-*` render rich markup to ANSI instead of stripping it; consolidate the duplicated printers. | **Open** — partially fixed (`run` done); jail-testable. |
| [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md) | Collapse the ~34 Python-mirroring `internal/*` packages into native-Go structure; drop parity machinery; §4 OSS-hygiene remnants. | **Open** — lowest priority; post-cutover endgame. |

## Other

| Doc | What it is | Status |
|---|---|---|
| [agent-settings-composition.md](agent-settings-composition.md) | RFC for the "Prism" model — composing agent settings across host and jail layers. | **Proposed, unbuilt** — nothing implemented; its "what exists today" predates the Go port and needs re-grounding in `internal/config` + `internal/entrypoint` before execution. |

Related live tracker: [`../research/macos-support-matrix.md`](../research/macos-support-matrix.md)
is the authoritative state-of-the-macOS-backend matrix.
