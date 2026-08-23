# Active plans & designs

This directory holds the **active** work — plans and designs we're currently
implementing or still discussing. Reference docs (how live systems work) live in
[`../design/`](../design) and [`../research/`](../research); done/obsolete
working docs are archived in git history (see [`doc-triage.md`](doc-triage.md)
for the classification and `git log --follow` to recover any).

> **Where to start:** [`roadmap.md`](roadmap.md) — the living forward plan, and the
> only doc here that answers "what is left?". Everything else in this directory is
> either a design/handoff for one piece of work or a historical record.
>
> [`sequencing-2026-07.md`](sequencing-2026-07.md) is the retired predecessor: a
> 2026-07-22 snapshot of the dependency ordering, kept for "why was this done in
> that order" rather than "what is next".
>
> **For the composed-config / packs cluster specifically**, start at
> [`BACKLOG.md`](BACKLOG.md): that cluster's design spans 8 docs, and BACKLOG is the
> only place that lists the implementable items in order, with a pointer per item to
> the doc holding its reasoning.

## macOS revival + distribution

| Doc | What it is | Status |
|---|---|---|
| [roadmap.md](roadmap.md) | **THE forward plan.** Everything still to do and nothing else — grouped by state (💬 needs you / 📦 ready / 🔒 waiting / 🧊 icebox), counted from its own contents, and citing every open question by ID in the design doc that holds it. As of 2026-08-23 the 📦 queue is **empty**: the last item shipped, and eleven rulings are what stand between here and more. | **Start here for "what is left?"** |
| [further-roadmap-ideas.md](further-roadmap-ideas.md) | **Candidates, not a queue.** Eight ideas from the 2026-08-23 doc audit, each with a verdict — four build, two rule-first, two drop — plus §4, which argues two rows should leave the roadmap. Nothing here is committed. | **Read when the queue is empty** |
| [shipped-2026-08-pack-batch.md](shipped-2026-08-pack-batch.md) | HISTORY of the ten-item pack batch: the rulings, the notch-divergence audit, and the NINE defects that surfaced only by running the lifecycle. Kept for the reasoning, not for planning. | ✅ done 2026-08-04 |
| [handoff-guest-notch-macos.md](handoff-guest-notch-macos.md) | The `guest` notch (env-manager Phase 7) plus every other Mac-gated item, in one place so one trip to a Mac can close all of it. Starts with the still-live G3 bug: macos-user renders ZERO pack surfaces, silently. | **Handoff** — Phase 7 not built; host/Mac-gated, not design-blocked. |
| [macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md) | The macOS-backend revival + source-distribution roadmap (Tracks J/D/M). | **In progress** — J1.1–J1.4, D1, D2, D3, J2, J3 landed; Track M M0/M1/M2 verified on real HW 2026-07-21; only D4's human-gated push/download proof remains. |
| [handoff-cachix-cache.md](handoff-cachix-cache.md) | Procedure to publish the prebuilt OCI image to a Cachix binary cache (= revival plan **D4**). | **Human-gated** — Substituter enabled (2026-07-20, `flake.nix:13-16`); Cachix account + cache exist and CI has already pushed data; only one Mac download proof remains. |

## Post-Go-port backlog

The archived `go-port-post-transition.md` (git history) queued work for after the
Python→Go cutover. §2 distribution landed. The still-open items are now tracked
here:

| Doc | What it is | Status |
|---|---|---|
| [nix-ld-dynamic-linking.md](nix-ld-dynamic-linking.md) | Replace the `LD_LIBRARY_PATH=/lib:/usr/lib` whack-a-mole with nix-ld so the mise node + MCP servers link env-free (closes the custom-`mcp_servers` startup gap). | **Open** — decided, not started; flake change, nested-jail validatable (host `just load` only ships it). |
| [cli-color-audit.md](cli-color-audit.md) | Make `prune`/`builder`/`macos-*` render rich markup to ANSI instead of stripping it; consolidate the duplicated printers. | **Open** — bug class fixed: prune/builder/macosuser/broker + top-level cli commands route through the shared richtext renderer with a TTY gate; remaining: migrate `run/console.go` off its private duplicate + unify the TTY probe. |
| [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md) | Collapse the ~34 Python-mirroring `internal/*` packages into native-Go structure; drop parity machinery; §4 OSS-hygiene remnants. | **Done** (2026-07-21); package-merge declined. |

## Test-suite speed

| Doc | What it is | Status |
|---|---|---|
| [integration-parallelism.md](integration-parallelism.md) | Bounded `t.Parallel()` for the container suite, after per-test GlobalStorage isolation unsticks the shared `last-load` sentinel race. | **Parked** — CI is free + the fast local loop skips these tests; the launch-merges (done 2026-07-20) were the cheaper win. Pick up only if the full local `just test` becomes a friction. |

## Other

| Doc | What it is | Status |
|---|---|---|
| [agent-settings-composition.md](agent-settings-composition.md) | Design of record: layered regeneration of any generated config (agent settings + MCP/LSP/mise/identity) + a Lua transform (format-agnostic, user-scope-only, no source mutation). | **Phase C complete 2026-07-22** — the prism is the unconditional config path at boot + check; the bespoke agent-config `Configure*` writers are deleted. mise/identity surfaces still deferred. |
| [cache-relocation.md](cache-relocation.md) | User-scope-only `cache_relocations` so a large cold cache subdir (`huggingface`, 185 GiB) can live on other storage, mounted read-write nested inside `.cache`. Read straight from the user config — never the merged config or the jail-writable snapshot. Also unblinds `prune`/`purge` and fixes the hint that recommends the symlink trick that dangles in-jail. | **Implemented 2026-07-21** — work items 1–10 landed and verified end to end in a nested jail; `yolo cache relocate` (item 11) deferred; one host-gated acceptance step (a real cross-filesystem move) outstanding. |
| [antigravity-agy-support.md](antigravity-agy-support.md) | Support Google Antigravity CLI (`agy`) as a native agent inside `yolo-jail`. | **✅ Done 2026-07-22** — born directly on the prism; all eight touchpoints landed (registry, `agySettings` surface, `AgyDir`, `ConfigureAgyPrism`, boot, preflight, docs, tests). |
| [agent-config-packs.md](agent-config-packs.md) | Proposal: share agent environment config (skills, AGENTS.md fragments, settings) between people by `(repo, path, branch)` with no PR — user-scope `packs`, host-side blobless fetch, content-addressed trees, pin/rollback, cross-agent projection. Includes the scope verdict (in yolo-jail, one extractable package) and the landscape research it rests on. | **Proposal** — ROADMAP open item 5. Phase 0 (`file://`-only) needs no network code and fixes the `pi`/`codex` no-skills gap standalone. |
| [../design/pack-system.md](../design/pack-system.md) | The pack system, whole: the `contributes[]` manifest, the twelve kinds + footprints + conflict rules, the one-writer rule, the compose engine + `derive`, selection/fetch/origin-gate. | **Shipped** — this is the current design of record for authoring/debugging/changing a pack (the reform that produced it is complete and its plan is retired). |
| [environment-manager-plan.md](environment-manager-plan.md) | Sequences [../design/yolo-as-environment-manager.md](../design/yolo-as-environment-manager.md) into buildable phases: the render-path collapse (= BACKLOG Stage G), the `confinement` dial, `apply`/`describe`/`--at`/`--sealed`, dep-provisioning, the `guest` notch, self-describing briefings. | **Plan** — nothing built beyond `config dump`/`config drift`; Phase 0 (data-loss fix) and Phase 1 (`internal/render`) are Stage G and ready; Phases 2–8 each gated by an open question (OQ-A…F). |

## Track M verification runbooks

[`runbooks/`](runbooks/) holds the Mac hardware verification procedures — they
are the revival plan's Track M gates, not user-facing reference (they moved here
from `docs/guides/runbooks/`). See the [sequencing-2026-07](sequencing-2026-07.md#runbooks) for their
status:

| Doc | What it is | Status |
|---|---|---|
| [runbooks/mac-macos-user-e2e.md](runbooks/mac-macos-user-e2e.md) | You-drive macos-user acceptance-bar test (the M1 anchor). | **Passed** (2026-07-21); M1 gate green; kept as repeatable procedure. |
| [runbooks/mac-ac-container-builder.md](runbooks/mac-ac-container-builder.md) | Zero-sudo Apple Container builder proof; Track-M/J3-adjacent. | **Passed** (2026-07-17) — kept as the repeatable procedure. |
| [runbooks/mac-go-port-verification.md](runbooks/mac-go-port-verification.md) | Go-vs-Python diff verification of the port. | **Stale** — recommended for `git rm` (its diff-against-Python method is dead post-wipe). |

Related live tracker: [`../research/macos-support-matrix.md`](../research/macos-support-matrix.md)
is the authoritative state-of-the-macOS-backend matrix.
