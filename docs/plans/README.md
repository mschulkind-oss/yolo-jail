# Active plans & designs

**Status:** INDEX — **rebuilt against the tree 2026-08-23.** The rows for the macOS track, the
guest-notch handoff, `pack-system`, `environment-manager-plan`, `agent-config-packs`,
`antigravity-agy-support` and `cli-color-audit` were re-checked against the code that day, and
**four were wrong in the direction that matters** (D1 retired, D2 reverted, D3 superseded, and
`cli-color-audit` was finished — that row listed two remaining items, both of which had landed). The remaining rows carry their doc's own dated status,
unverified here. **If a row disagrees with the doc it points at, trust the doc and fix the row.**

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

## Keeping this corpus honest — the five checks, so they are re-runnable

The 2026-08-23 audit ran these by hand; four of the five found something, and the fifth is worth
keeping because it is now clean and would not stay that way silently. They are not wired into `just`
yet — that proposal, with the allowlists it needs, is
[`further-roadmap-ideas.md`](further-roadmap-ideas.md) §I1. Until then, run them when a sprint
closes; the drift clusters there rather than spreading evenly.

```console
# 1. Every relative doc link resolves.              (found: 5, now 0)
# 2. Every live open question is countable.         (found: 6 invisible to the first regex)
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )(<a id="[^"]*"></a> ?)?💬' docs/ --sort path
# 3. Every backticked SHA resolves in THIS repo.    (found: 3 phantoms cited as evidence)
$ git rev-parse --verify --quiet <sha>^{commit}
# 4. Every backticked code path exists.             (found: 1 real rot among 15 hits)
# 5. Every in-doc heading link resolves.            (clean — but only with a CORRECT slugger:
#    GitHub maps each space to its own hyphen, so an em-dash heading yields `--`. A naive
#    slugger collapses them and reports 67 false positives.)
```

**Checks 3 and 4 need an allowlist or they cry wolf**: upstream `flake.lock` revs and other
projects' source are legitimately unresolvable, and a doc *recording a deletion* is supposed to name
the thing it deleted. The signal is a path or SHA offered as **evidence**, not one named as history.

---

## macOS revival + distribution

| Doc | What it is | Status |
|---|---|---|
| [roadmap.md](roadmap.md) | **THE forward plan.** Everything still to do and nothing else — grouped by state (💬 needs you / 📦 ready / 🔒 waiting / 🧊 icebox), counted from its own contents, and citing every open question by ID in the design doc that holds it. As of 2026-08-25 the 📦 queue holds **two** items — minimal disk footprint and program delivery §10. C2 and C3 were queued and shipped the same day (2026-08-25), which is the file's own rule working: work that ships leaves immediately. **Fourteen** 💬 rows stand between here and more. | **Start here for "what is left?"** |
| [further-roadmap-ideas.md](further-roadmap-ideas.md) | **Candidates, not a queue.** Seven proposals from the 2026-08-23 doc audit — five build, two rule-first — plus **three rows that should LEAVE the roadmap** (§4, §4a) and §4b, which records what an adversarial re-check did to the file itself. Nothing here is committed. | **Read when the queue is empty** |
| [shipped-2026-08-pack-batch.md](shipped-2026-08-pack-batch.md) | HISTORY of the ten-item pack batch: the rulings, the notch-divergence audit, and the NINE defects that surfaced only by running the lifecycle. Kept for the reasoning, not for planning. | ✅ done 2026-08-04 |
| [handoff-guest-notch-macos.md](handoff-guest-notch-macos.md) | The `guest` notch (env-manager Phase 7) plus every other Mac-gated item, in one place so one trip to a Mac can close all of it. Its old lead — the G3 bug, macos-user rendering ZERO pack surfaces — was **fixed on 2026-08-12** (`2bb792ff`); the live lead is now the Mac's config still using the removed `agents` key, which refuses every launch there. | **Handoff** — Phase 7 not built; host/Mac-gated, not design-blocked. |
| [macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md) | The macOS-backend revival + source-distribution roadmap (Tracks J/D/M). | **In progress, and three of those letters do not mean what this row said** (re-checked 2026-08-23): **D1 landed and was RETIRED** (`20a8ce9f`), **D2 landed and was REVERTED** (`5d34dece` — a missing repo root is fatal again), **D3 was superseded** by the prebuilt-bundle cutover, and J1.3 shipped inside `internal/builder`, which is deleted. J1.1/J1.2/J1.4, J2 and J3 hold. Track M M0/M1/M2 passed on real HW 2026-07-21 but **M2 has lapsed** — that Mac is 531 commits stale on the removed `agents` key. D4 needs a first push and a Mac download. |
| [handoff-cachix-cache.md](handoff-cachix-cache.md) | Procedure to publish the prebuilt OCI image to a Cachix binary cache (= revival plan **D4**). | **Human-gated** — Substituter enabled (2026-07-20, `flake.nix:13-16`); Cachix account + cache exist and CI has already pushed data; only one Mac download proof remains. |

## Post-Go-port backlog

The archived `go-port-post-transition.md` (git history) queued work for after the
Python→Go cutover. §2 distribution landed. The still-open items are now tracked
here:

| Doc | What it is | Status |
|---|---|---|
| [nix-ld-dynamic-linking.md](nix-ld-dynamic-linking.md) | Replace the `LD_LIBRARY_PATH=/lib:/usr/lib` whack-a-mole with nix-ld so the mise node + MCP servers link env-free (closes the custom-`mcp_servers` startup gap). | **Open** — decided, not started; flake change, nested-jail validatable (host `just load` only ships it). |
| [cli-color-audit.md](cli-color-audit.md) | Make `prune`/`builder`/`macos-*` render rich markup to ANSI instead of stripping it; consolidate the duplicated printers. | ✅ **DONE** — and this row was two items stale (fixed 2026-08-23). Both things it listed as remaining have landed: `run/console.go` consolidated onto `internal/richtext` (`67454a8`; verified at `internal/cli/run/console.go:1-18`) and the TTY probe unified onto `internal/tty` (`b76b2ba`). The doc itself has said DONE since 2026-07-22. |
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
| [antigravity-agy-support.md](antigravity-agy-support.md) | Support Google Antigravity CLI (`agy`) as a native agent inside `yolo-jail`. | **✅ Done 2026-07-22**, and since RESHAPED — agy is a **pack** now and the agent registry it was added to is deleted (see the warning atop that doc). Born directly on the prism; all eight touchpoints landed (registry, `agySettings` surface, `AgyDir`, `ConfigureAgyPrism`, boot, preflight, docs, tests). |
| [agent-config-packs.md](agent-config-packs.md) | Proposal: share agent environment config (skills, AGENTS.md fragments, settings) between people by `(repo, path, branch)` with no PR — user-scope `packs`, host-side blobless fetch, content-addressed trees, pin/rollback, cross-agent projection. Includes the scope verdict (in yolo-jail, one extractable package) and the landscape research it rests on. | **Proposal** — largely OVERTAKEN: the `packs` key, host-side fetch, the lockfile and the origin gate all shipped in the 2026-07/08 pack work, so read this for the *landscape research* and the scope verdict rather than as a plan. (It used to cite "ROADMAP open item 5", a numbering the 2026-08-17 restructure retired.) |
| [../design/pack-system.md](../design/pack-system.md) | The pack system, whole: the `contributes[]` manifest, the **fifteen** kinds + footprints + conflict rules, the one-writer rule, the compose engine + `derive`, selection/fetch/origin-gate. | **Shipped** — this is the current design of record for authoring/debugging/changing a pack (the reform that produced it is complete and its plan is retired). |
| [environment-manager-plan.md](environment-manager-plan.md) | Sequences [../design/yolo-as-environment-manager.md](../design/yolo-as-environment-manager.md) into buildable phases: the render-path collapse (= BACKLOG Stage G), the `confinement` dial, `apply`/`describe`/`--at`/`--sealed`, dep-provisioning, the `guest` notch, self-describing briefings. | **Mostly BUILT** (re-checked 2026-08-23) — Phases 0–6, 8 and 9 have shipped: the data-loss fix, `internal/render` + `Target`, the confinement dial, `describe`/`apply`/`--at`/`--sealed`, `check-deps`, the notch briefing, and autonomy-as-a-notch-policy. **Phase 7 (the `guest` notch) is the only unbuilt phase** and is host-gated, not design-blocked. |

## Provider & profile machinery

Seven docs written 2026-08-29 → 2026-09-02 around one question: how does a provider (a z.ai key,
a Bedrock role) reach every selected agent. One parent design, the counter-design it answers, the
first consumer, two review docs that measured what actually shipped, and a plan apiece for those
two. The live questions are 💬 17/18/19 in [roadmap.md](roadmap.md), not here. Rows checked
against the tree 2026-09-02.

| Doc | What it is | Status |
|---|---|---|
| [../design/profiles-as-pack-variants.md](../design/profiles-as-pack-variants.md) | The parent design. `kind: "profile"` is a named variant of **one pack's own** declarations — the shipped `autonomy` contribution given an open selector, not a new merge engine — `providers` stays a config key with a stricter schema, and the missing process-env channel is what had Claude's Bedrock case hardcoded in Go. §2 measures what already shipped instead of assuming it. | **DECIDED 2026-09-01** (ledger, §14) and **built** — the implementation shipped through `980aed71`, including §12 step 6's `config-overlay` `profile` gate. Its own follow-up note reports the re-measurement clean and files the edge defects as OQ-PT1–PT5 in [provider-table-fidelity.md](../design/provider-table-fidelity.md). |
| [../design/pack-profiles.md](../design/pack-profiles.md) | The counter-design the parent answers: a dual layer of `kind: "provider"` + `kind: "pack-fragment"` under RFC-7386 merging, plus `env_sources`/`api_key_env` secret hygiene. | **DRAFT 2026-08-29, nothing built, superseded as a direction.** Read §2's diagnosis and §4's credential architecture (adopted by the parent as recommendation plus mechanism), not the schemas — its own note says the doc is unchanged and points at the parent's §9 point-by-point diff. |
| [../design/zai-plumbing.md](../design/zai-plumbing.md) | The first real consumer: both routes to "one provider, every agent" — name the protocol and fill the values (§3), or ship a layered zai pack the user drops a key into (§4) — and the endpoint-by-protocol resolution behind `-p zai` (§5). | **DECIDED 2026-09-01** (ledger, §8); nothing built beyond the shipped `providers` key and the three derives it inherits. §5's resolution table now speaks the canonical vocabulary (`db6aff96`), and its follow-up note hands the codex dialect question to [provider-table-fidelity.md](../design/provider-table-fidelity.md) §3. |
| [../design/provider-table-fidelity.md](../design/provider-table-fidelity.md) | The defect report on that shipped machinery: **eleven** defects, D1–D11, of which three share one cause — a value validated against a set yolo owns and handed verbatim to consumers that own different sets. D1 is the only one that puts a wrong value in a file an agent reads (`wire_api`, the protocol field, was four borrowed spellings naming three protocols). D9 outgrew the doc to [`trust-paths.md`](../design/trust-paths.md)'s census. | **DECIDED 2026-09-01** (ledger, §11), and the plan is **landing**: D1's three-name vocabulary + per-agent dialect maps, D2's composer refusal (`5d8bd1fe`), D5's `--profile`/`-p` split (`886a9191`) and D6's census (`67f87f36`) are in the tree, with `integration/providers_test.go` pinning D1. D10/D11 were filed 2026-09-02 while paying §3.0a's verification debt. |
| [../design/provider-catalog-and-selection.md](../design/provider-catalog-and-selection.md) | Splits the knot the two above left tangled: **catalog** (the agent's directory of providers it *could* use) and **selection** (which one it *does* use) are two features, and one table drives both — measured in a live jail, `-p zai` changes the behaviour of one agent in four. Also dissolves disable-without-deleting. | **DECIDED 2026-09-01** (ledger, §10; a tenth question was withdrawn as never having been a design question). §3's empty pi row was filled from source 2026-09-02 — which closed the research blocker its own plan still cites. |
| [../design/provider-table-fidelity-plan.md](../design/provider-table-fidelity-plan.md) | Implementation plan for the defect list: the `wire_api` dialect translation, the composer's composition bugs, the `--profile` split, the census. Written against `578c7e5f`. | **ready** as written, and its map is nearly all in the tree (2026-09-02 — see the design row); read the map as the checklist for what is left. |
| [../design/provider-catalog-and-selection-plan.md](../design/provider-catalog-and-selection-plan.md) | Sibling plan: env-emitting derives, deleting the `env_shape` placeholder vocabulary, the `pack_profiles` → `use_profiles` rename, the `options` block. | **ready, blocked at step 1 as written** — and the env half has landed (`f55f2109`, `3144fbed`, `43d24e9e`, with the profile declarations in `internal/config/packs.go`); the codex/pi/opencode selection key and `options` were not in the tree at this row's check (2026-09-02). |

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
