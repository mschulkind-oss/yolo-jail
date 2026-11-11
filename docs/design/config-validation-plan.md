---
title: "Config validation implementation plan"
status: accepted
stage: DECIDED
next: "Implement the shared config-only validation path from the accepted design; refresh the source map against the integration base first"
depends-on:
  - config-validation.md
---

# Plan: `yolo config check`

**Completed:** 2026-11-11. Written against `3dbf49baa8d076ec4185526290ac2195e53f6c8b`.
**Design:** [Config validation](config-validation.md).
**Precedence:** the design wins on behavior; the tree wins on fact; this plan is advice and is the first thing to be stale. The latest wording worker touches `internal/cli/check/envoverrides.go` only; refresh this map from the integrated base before implementation. No other task starts from an old plan stamp.

**Do not re-open:** `config check` shares the validator with `yolo check`; it makes no persistent writes, network requests, runtime/Nix calls or credentials access. Only the shared validator's read-only `git rev-parse` / `git ls-tree` queries against an existing fetched-pack store are permitted. A cached fetched pack is validated fully; a missing store/revision or unavailable `git` is a per-pack SKIP with partial coverage disclosed, and unrelated config validation still runs. It never fetches. Exit 0 means no config FAIL, not readiness. `yolo check` remains broad except for retired disk accounting, and its in-jail default build changes as the design states.

## Map

| Path | Change |
| :--- | :--- |
| `internal/cli/config.go` | Add `config check` dispatch before target resolution; own its help/argument contract and call the config-only runner. |
| `internal/cli/configeditverbs.go` | Route the automatic post-write validation for successful public workspace `set/add/unset` through the same config-check entrypoint. Preserve written-file disclosure and non-zero propagation. Keep user-scope, no-op and `--dry-run` behavior unchanged. |
| `internal/cli/commands.go` | Update `check`/`doctor` help and usage; keep broad `check` semantics and flags except specified in-jail build default. |
| `internal/cli/check/check.go` | Extract/reuse the config-file, merged-validation and selected-pack stages as a config-only orchestration path; keep `Check` broad, remove disk section, add scope block, skip entrypoint dry-run and readiness sections. Never branch work selection on `Format`. |
| `internal/cli/check/checkcmd.go` | Add only the seams needed to prove production config-check exclusions; keep `Options.inJail()` as the single `YOLO_VERSION` detector. |
| `internal/cli/check/jsonreport.go`, `reporter.go` | Add additive `mode` and `scope` data and scope rendering through the existing single finding sink; report SKIP coverage accurately and never leak credentials. |
| `internal/cli/check/configcheck.go` | New config-only runner/report assembly if needed; it must call the shared config validator and selected-pack path, not copy their semantics. |
| `internal/config/validate.go`, `validateprofiles.go`, `profiles.go`, `packs.go`, `selectedpacks.go` | Extract a shared validator core that accepts the already-read config/provenance and read-only pack-resolution results. Preserve every existing validation and finding string; report unresolved fetched-pack coverage without aborting unrelated checks. |
| `internal/config/packresolve.go`, `internal/packload/packload.go`, `internal/packload/embedded.go` | Keep selected pack resolution at `ReadOnlyStore:true`; add a non-persistent selected-embedded-pack read/stage only if necessary, reusing the existing embedded loader/decoder. Do not invoke the shared persistent `packload.Embedded()` cache from this command. |
| `internal/packsrc/store.go` | Reuse `ResolveExisting` only. Pin its permitted local read-only `git rev-parse` / `git ls-tree` invocations and unavailable-git/missing-revision outcomes; no fetch/clone/store creation. |
| `internal/cli/check/packs.go`, `sections_loopholes.go`, `entrypoint.go`, `probes.go` | Split validity from liveness in place where needed. Keep `sectionPacks`/selected declarations shared; exclude executable lookup, broker/service liveness, runtime resolution, and the whole entrypoint dry-run from config check. |
| `internal/cli/check/sections_misc.go`, `golden_test.go`, `check_test.go` | Retire disk usage from `yolo check`; deliberately repin text output and exercise the new scope entry. Keep unrelated full-check sections intact. |
| `internal/cli/configeditdispatch_test.go`, `internal/cli/outputformat_test.go`, `internal/cli/check/configcheck_test.go` | Production-dispatch coverage for check syntax, side-effect exclusions, write-postcheck routing and status propagation, JSON/text/gating, temp cleanup, and cached/missing fetched packs. New test file marked new. |
| `internal/config/validate_test.go`, `internal/config/selectedpacks_test.go`, `internal/packload/embeddedlazy_test.go` | Pin shared-validator parity, per-pack partial outcomes, selected-only declaration resolution, and no cold persistent embedded-cache materialization. |
| `integration/cli_test.go` | Add real-CLI valid and invalid `yolo config check` workspace cases beside `TestYoloCheckValidConfig` / `TestYoloCheckInvalidConfigFails`. |
| `AGENTS.md`, `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md`, `internal/jailcontent/builtinskills/diagnosing-the-jail/SKILL.md` | Update canonical guidance only; never edit generated skill copies. |
| `userguide/reference/cli-reference.md`, `userguide/reference/configuration.md`, `userguide/getting-started.md`, `userguide/guides/providers-and-models.md`, `userguide/guides/packs-and-skills.md`, `userguide/guides/migrating-to-packs.md`, `userguide/guides/claude-plugins-and-mods.md` | Replace recommendations that present broad `yolo check`/`--no-build` as config validation; preserve a broad check when it is specifically for runtime/image readiness. |
| `docs/reference/config-safety.md`, `docs/reference/host-service-startup-diagnostics.md`, [docs/reference/composed-file-permissions.md](../reference/composed-file-permissions.md), `docs/reference/tool-provisioning.md`, `packs/github/briefing/gh.md` | Audit the located config-edit advice; update only the config-validity step, keeping approval, environment-readiness and image-build steps explicit. |
| `docs/design/config-validation.md`, `docs/design/config-validation-plan.md` | Source documents own behavior and build hand-off. No changelog entry. |
| `docs/plans/roadmap.md` | Minimal priority link to the design and completed plan; no copied status or task checklist. |

## Reuse

- `internal/cli/check/check.go:124–151, 702–795` — production config-file parsing, `config.ValidateConfig`, capability/profile checks, provenance annotations, and shared validity findings. `sectionMergedConfig` currently mixes these with `resolveRuntimeForCheck`, optional mounts, and acceptance-snapshot writing; split at the seam instead of copying the validator.
- `internal/config/validate.go:75–185` is the shared validation entry. Its current `validateConfigWithEditContext` is **not** a drop-in config-check mode: edit validation changes behavior and only skips some rereads. Extract a shared validation context/core, feed the already-read sources, and preserve the ordinary validator's errors/warnings and order.
- `internal/config/selectedpacks.go:27–101` returns resolved declarations and a completeness bit using `ResolvePack(ReadOnlyStore:true)`. Extend this shared resolver result with per-pack reason/coverage as needed; do not return early on one unresolved pack.
- `internal/config/packresolve.go:104–195` is the one resolver. `ResolveExisting` in `internal/packsrc/store.go:1215–1257` is the allowed fetched-pack path and is read-only/non-fetching but does execute local Git queries. Do not call `Resolve` or `ResolvePack` with default writable mode from the new path.
- `internal/packload/packload.go:1382–1450` has the embedded materializer and the shared `loadEmbeddedPack`; `packload.Embedded()` instead leases/materializes the persistent shared cache (`embedded.go:115–124`, `embeddedcache.go:191–205`). A private-temp selected-set reader must reuse the same decode/use-read behavior while leaving unselected packs and persistent cache untouched.
- `internal/cli/check/reporter.go` and `jsonreport.go` already unify one-pass text/JSON findings and count SKIP separately. Extend this reporter for mode/scope rather than adding a second JSON traversal.
- `Options.inJail()` (`checkcmd.go:372–378`) already provides the required `YOLO_VERSION` test seam. `Check` currently ensures storage before any section (`check.go:63–82`), so the new config-only dispatch must not enter `Check`'s full sequence.
- `reporoot.Resolve` (`internal/reporoot/reporoot.go:81–160`) reads local executable/bundle metadata and uses filesystem symlink resolution; it makes no subprocess, Nix or network call. At this base `sectionMergedConfig` does not call it. Do not add repo/bundle readiness to config-only validation unless it is a real input to the shared config validator.
- `configRunW` in `internal/cli/config.go:99–150` is the production config dispatcher. `runConfigPostWriteCheck` in `configeditverbs.go:282–291` currently selects `check --no-build`; replace that call with the same public config-check entry, not a parallel post-write validator.
- Existing callsite guards: `TestCheckOptionsWiring` (`internal/cli/outputformat_test.go`), `TestEverySectionIsWired` (`internal/cli/check/sectionwiring_test.go`), and `assertPublicPostWriteCheck` (`internal/cli/configeditdispatch_test.go:406–415`). Update expected behavior in place so the test fails if dispatch/post-write wiring disappears.
- Existing integration helpers and comparisons are in `integration/cli_test.go`; use `requireJail`, `writeProject`/`tempProject`, and `runYoloCLI`, not a helper binary or an agent process.

## Traps

- `config.ValidateConfig` normal mode currently reaches `UseProfileCLINames` and selected pack declaration validation; one route uses writable `ResolvePack` and can fetch, and the embedded reader can materialize a persistent cache. Both must become a single shared read-only path, not silently bypassed.
- Missing a selected fetched pack must be a pack-local SKIP; an all-or-nothing early return would hide an unrelated invalid key and fail AC3a.
- Use `SelectedPackDeclarations`' selection/closure semantics; do not load every pack to reserve a name or infer agent membership. Preserve local-pack symlink behavior already ruled by [`OQ-NC9`](../plans/notch-convergence.md#7-decision-ledger); do not add path canonicalization or symlink guessing.
- The config-edit automatic check is observable behavior: keep its write-before-check message, final error line, exit code, and host approval requirement; do not run it for no-op, dry-run or user edit. Rewrite `configeditdispatch_test.go` assertions rather than weakening them.
- `Options.Format` changes rendering only. `mode` is output data, never the switch that admits stages.
- `sectionInlineLoopholes` calls `LookPath`; `resolveRuntimeForCheck` calls `LookPath`/`Exec`; entrypoint preflight uses a temporary home but renders all embedded pack surfaces. Exclude these probes from config-only validation; temporary output alone does not make an environment probe config-determined.
- Ordinary `packload.Embedded()` is not harmless for this verb: on a cold process it can write the persistent embedded-pack cache. A temp pack tree must be removed on success, FAIL, SKIP and signals as best effort.
- Existing text goldens pin exact strings and summaries. Retire disk and add scope in one intentional re-pin; do not regenerate unrelated sections.
- Do not treat the broad check's `SkipEnsureStorage` test seam as production purity: `config check` must not call storage ensure at all.
- When auditing guidance, change only statements that say the broad check validates a config edit. Keep `yolo check` for runtime, machine, image-build and service diagnosis.

## Tasks

### Task 1 — shared safe validation and partial pack coverage

**Settled:** preserve the existing validator's semantics and strings; cached fetched packs validate fully; missing cache/revision/Git produces a per-pack SKIP and partial-coverage count, while unrelated config checks continue. Do not re-open.

- **Preconditions:** refresh the base as noted above. Read current `validate.go`, `validateprofiles.go`, `profiles.go`, `packs.go`, `selectedpacks.go`, `packresolve.go`, `packsrc/store.go`, and the embedded loader. If any path/symbol moved, update this map; if preserving full `ValidateConfig` findings requires a second validator or accepting fetches/cache writes, stop and report rather than weaken scope.
- **Tests first:** add table-driven cases in `internal/config/selectedpacks_test.go` or a source-named `validationconfigcheck_test.go`: cached valid fetched pack; cached invalid declaration; cached capability/name-reservation conflict; missing store; missing pinned revision; unusable Git; and an unrelated invalid config key combined with each unavailable-pack case. Assert same normal validator error string for readable failures, per-pack reason, partial count, continued unrelated FAIL, no store creation/write, and no persistent embedded-cache materialization. Use a recording fake `Store.Git` executable or a narrow store-Git invocation seam to assert exact allowed argv; the broad runtime `Options.Exec` seam does not currently own `packsrc.Store` commands. Red: `go test ./internal/config -run 'Test.*(ConfigCheck|SelectedPack|ReadOnly)' -count=1`.
- **Change:** extract a shared `ValidateConfig` core that accepts the already-read config/provenance and pack-resolution results; both existing `yolo check` and new config check call it. Parameterize profile/pack namespace resolution in place so there is no writable default resolver on the config-check path. Use the only admitted Git operations (`ResolveExisting`) for an existing store. Add a selected embedded pack reader/stager under the runner's private temp root, reusing packload's existing loader, and never call `packload.Embedded()` from config check. Keep cross-pack checks over readable declarations and explicitly mark their partial coverage when any pack was skipped.
- **Gate:** `go test ./internal/config ./internal/packload ./internal/packsrc`.
- **Fence:** no parallel validator; no pack fetch, clone, store creation, persistent cache, general workspace Git, runtime/Nix/executable probing, credential reads, or unselected-pack declaration scan. No agent-membership switch or config schema expansion.
- **Report:** red/green commands, cases, exact permitted Git argv, and proof fixed store fixture remains unchanged (fixture stat/bytes; no snapshot/hash manifest).

### Task 2 — config-only command and reporter

**Settled:** `config check` accepts only its documented text/JSON/help forms; usage errors exit 2 before reads. FAIL exits 1, otherwise 0; SKIP does not fail but partial pack coverage is explicit. The same work produces text and JSON.

- **Tests first:** production dispatch tests in new `internal/cli/configcheck_test.go` through `configRunW`, not a direct runner call. Cover valid/no-config, parse and semantic FAIL, host/in-jail banners and host-path skip, JSON (`mode`, `scope`, findings equal text), flags `--build`, `--no-build`, `--accept-config-changes` rejected before config reads, and `--help` no work. Inject the runtime `Options.Exec`, `LookPath`, and `BuildImage` seams and assert they are untouched; assert storage ensure is unreachable through production dispatch (add a narrow seam only if the callsite cannot be observed). Separately record `packsrc.Store` Git via its `Git` executable seam and assert only the two allowed read-only Git forms run against an existing store. Assert temp cleanup on success, FAIL, SKIP and gated exits. Red: `go test ./internal/cli -run '^TestConfigCheck' -count=1`.
- **Change:** add `config check` in `configRunW` before config target disclosure/resolution and route through a config-only orchestrator in `internal/cli/check`. Reuse the source-reading and shared validator from Task 1, then the selected-pack stage; do not call full `Check`. Add scope rendering/JSON fields via reporter/`Report`; preserve existing finding fields and secret-safe formatting. `config check --format json` changes output only. Add help text saying not a readiness check and disclose the temporary private staging exception.
- **Callsite proof:** the test must fail if `configRunW` loses the route, if it enters `Check`/storage initialization, or if format starts choosing work. Add a structural/callsite assertion for nudge, cache relocation, approval snapshot and `doctor_cmd` remaining unreachable.
- **Gate:** `go test ./internal/cli ./internal/cli/check`; run targeted new tests after any later caller refactor.
- **Fence:** no flags beyond `--json`, `--format json`, and `--help`; no broad check side effects or readiness status; no secret values in text or JSON.
- **Report:** all status/exit combinations and counters, including partial selected-pack count and the reason/next fetch command.

### Task 3 — broad `yolo check`, automatic post-write validation, and guidance

**Settled:** `yolo check` keeps its breadth and `--no-build` meaning, except disk retirement and the no-flag in-jail build skip. A public successful workspace edit runs the new fast config check after writing. No-op/dry-run/user-scope behavior and approval remain unchanged. Do not re-open.

- **Tests first:** update `internal/cli/configeditdispatch_test.go`'s `assertPublicPostWriteCheck` expectations from `YOLO Jail Check` to the config-only banner and add a bad-config write proving `checkRC`/post-write disclosure propagation. Assert no automatic check on no-op/dry-run/user-scope. Add a callsite test proving post-write goes through the same public config check and has zero runtime/build/storage calls. In `internal/cli/check`, update full-report goldens for the retired disk section/scope block and pin in-jail build default plus explicit `--build`. Red: `go test ./internal/cli -run 'TestConfigRunW|TestConfigEdit|TestPublicPostWrite' -count=1` and `go test ./internal/cli/check -run 'TestCheck|TestDisk|Test.*Build' -count=1`.
- **Change:** use `runConfigPostWriteCheck` to call the same config-check entrypoint; update `projectWrittenLine(false)` and post-write docs to name `yolo config check`. Remove disk usage from `Check`, add its `yolo stores` / `yolo prune` scope entry, and preserve every other section. Apply CV-D7 only to the in-jail default; explicit `--build` still invokes image build. Rewrite config-edit advice in canonical skill templates and located user/reference guidance. Preserve readiness/image-build `yolo check` instructions as separate steps where required.
- **Guidance sources already found:** `AGENTS.md`; both built-in skill templates; `userguide/reference/cli-reference.md`, `configuration.md`, `userguide/getting-started.md`, `userguide/guides/providers-and-models.md`, `packs-and-skills.md`, `migrating-to-packs.md`, `claude-plugins-and-mods.md`; `docs/reference/config-safety.md`, `host-service-startup-diagnostics.md`, [docs/reference/composed-file-permissions.md](../reference/composed-file-permissions.md), `tool-provisioning.md`; and `packs/github/briefing/gh.md`. Search again for config-specific `--no-build`/“run `yolo check` after edit” guidance and classify each: update config-validity advice, retain genuine machine/runtime/image/service diagnosis. Never edit generated skill copies.
- **Gate:** `go test ./internal/cli ./internal/cli/check`; `go test -run 'TestYoloConfigCheck' ./integration` after Task 4 is added.
- **Fence:** no disk reclaim implementation, no narrowed `--no-build`, no config-edit behavior change to write/approval/dry-run semantics, no global replacement of readiness recommendations.
- **Report:** which advice changed and which remained readiness instructions, golden update list, post-write failure behavior.

### Task 4 — real CLI proof and landing

**Settled:** a built binary must validate a valid and invalid workspace. Run the existing CI gate, targeted integration, and a matched development-bundle smoke; do not test against an installed/stale launcher.

- **Tests first:** add `TestYoloConfigCheckValidWorkspace` and `TestYoloConfigCheckInvalidWorkspace` to `integration/cli_test.go`, using `requireJail`, existing scratch-project helpers, and `runYoloCLI`. Assert real exit codes and the semantic finding, not merely the helper. Red: `go test -run 'TestYoloConfigCheck(Valid|Invalid)Workspace' ./integration`.
- **Change:** only add integration fixture/setup required by the tests. Do not start an agent or call external APIs.
- **Gates:** `just test-fast`; `go test -run 'TestYoloConfigCheck(Valid|Invalid)Workspace' ./integration`; `just check-ci`; `just build-dev-bundle`, then invoke its emitted launcher by absolute path from a throwaway workspace for valid and invalid config. Run `just format` before commit. This is an `internal/` logic change, so perform the nested-jail verification from a throwaway workspace; it cannot prove rootless-only/reachability behavior, which this change does not claim. If a macOS-host check is unavailable in CI, name a Mac agent to run the documented valid/invalid CLI smoke; do not fabricate a host result or timing.
- **Commit:** `feat(config): add read-only config check`.
- **Report:** red and green commands, complete gate result, emitted bundle launcher path, valid/invalid smoke outcomes, recorded no-pack/cached-pack timings (evidence only), Mac-agent witness if required, and `git diff --stat`.

## Ships with

- Unit: validator parity, no persistent write/cold embedded cache, cached/missing fetched pack statuses, continuation after missing pack, JSON/text parity, option/exit/gating matrix, temp cleanup, host/in-jail scope, disk retirement and build-default cases.
- Callsite: `configRunW` and public config edit post-write; tests fail if either route is removed or if the old full check still runs after an edit.
- Integration: `integration/cli_test.go` valid/invalid public CLI runs; selected cached fetched-pack behavior remains covered by deterministic unit fixtures and must not hit network.
- User-visible surfaces: `yolo config check` help; broad `yolo check`/`doctor` help and scope; JSON `mode`/`scope`; automatic post-write output and error line; canonical skills, config CLI/user guide and relevant references. No generated outputs edited manually.
- Existing `yolo check` JSON finding fields remain unchanged; only the design's additive mode/scope and disk/scope behavior ship.

## Don't

- Do not implement a second semantic validator or skip readable declarations to preserve a simplistic “offline” shortcut.
- Do not make a missing pack gate the rest of validation, turn it into a FAIL/OK, or claim cross-pack checks covered it.
- Do not call `packload.Embedded()` in the config-only path, load all shipped declarations as a reservation universe, or write outside the private temporary pack tree.
- Do not let runtime / loophole `LookPath`, `doctor_cmd`, runtime connectivity, Nix, entrypoint dry-run, disk traversal, storage ensure, cache relocation, pack nudge or approval snapshot run in the config-only or post-write check.
- Do not change `doctor`, `--no-build` breadth, host build default, explicit in-jail `--build`, config-write disclosure, approval flow or image readiness advice beyond the accepted design.
- Do not add a changelog entry, agent test, network test, or Mac-host claim without the actual witness.
