---
title: Config validation — a fast, read-only `yolo config check` beside the broad `yolo check`
status: accepted
stage: BUILT
next: "Graduate to a reference doc"
date: 2026-11-11
---

# Config validation

**Claim.** "Is my config edit valid?" and "is this machine ready to launch a jail?" are two questions, and today one command answers both. This design gives the first question its own read-only verb, `yolo config check`, and leaves `yolo check` as the broad readiness report. It also removes the disk-usage walk from `yolo check` and makes the build flags unnecessary in normal use.

**Status:** 2026-11-12 — built: `yolo config check` landed in `b4d4e267c` (2026-11-10), with the
integration cases `TestYoloConfigCheckValidWorkspace` and `TestYoloConfigCheckInvalidWorkspace`.
UNMEASURED by this re-check, which read the commits and the tree and ran nothing. The original evidence packet records its tree at `2c2c26f7f`; the planner verified the relevant live seams against `3dbf49baa` while completing the companion plan. The owner's "five minutes" is a report, not a measurement, and no measurement singles out any one section as dominant.

> **In short.** After any config change, run `yolo config check`. It parses, merges and validates the config and the selected packs' declarations with the same validator `yolo check` uses. It writes nothing persistent, never touches the network, a container runtime, Nix or credentials, and states what it did not look at. Its only subprocesses are the read-only `git` lookups that validator already makes to read a fetched pack from the local store. A fetched pack that is not in the local store is reported as not validated, not as a failure. `yolo check` stays the machine-and-workspace readiness report, minus disk accounting (that is `yolo stores` / `yolo prune`), and no longer needs `--no-build` inside a jail.

**Costs.** One new verb. `yolo check` goldens change (disk section retired, scope block added). The JSON report gains two additive fields. Help text, canonical skills, and config-edit guidance are updated. Successful public workspace `config set/add/unset` writes run the new config check instead of a second broad post-write readiness check. The passive disk-bloat WARN in `yolo check` is lost.

**Start at:** [2. Decisions](#2-decisions) · **Needs your ruling:** None. · **Reads with:** [`docs/design/config-validation-plan.md`](config-validation-plan.md) (companion implementation plan, written by the planner).

## 1. Problem

| # | Observation (packet, `2c2c26f7f`) | Consequence |
|---|---|---|
| P1 | `yolo check` is one body: storage ensure/migrate, runtime, Nix, config, pack nudge, pack staging, cache dirs, entrypoint dry-run, device probes, image build, delivery, loopholes, services, disk, durable, wrappers. | A config edit pays for the whole machine. |
| P2 | `--no-build` skips only the image build, yet help and `AGENTS.md` call it "the fast preflight to run after editing". | Agents cannot tell which form to run, and the "fast" form is still broad. |
| P3 | The disk section runs `ps -a`, inspects every `yolo-*` container, and recursively sizes each `.yolo` and global storage. It grades hygiene, not validity. | Unbounded-by-size work sits inside a validity check. |
| P4 | `check` writes: `EnsureGlobalStorage` and migration, the pack nudge (`PackAdd`), cache relocation dirs, the approval snapshot. | The body cannot be called a pure preflight, though `host-service-startup-diagnostics.md` requires the validation phase to be pure. |
| P5 | In a jail the config is a generated snapshot, so host mount paths cannot be judged. | A fast check must say so rather than pass them. |

**Existing mechanisms surveyed (from the packet, not re-searched by this seat):**

| Existing mechanism | Why it does not already solve this |
|---|---|
| `yolo check --no-build` | Broad report minus the build (P2). |
| `yolo config set/add/unset/migrate`, `pack add/remove` | They validate their own write. No evidence shows this equals full merged validation, and hand or agent edits bypass them. |
| `yolo config why/ls/render/diff` | They inspect; they do not grade. |
| `yolo config drift` | It compares pending against applied; it does not judge validity. |
| `yolo stores`, `yolo prune` | They already own disk inventory and reclaim, so nothing new is needed for P3. |

The packet reports that no standalone config-only check was found. The option shortlist in the brief was checked against this table.

## 2. Decisions

| ID | Decision | By |
|---|---|---|
| CV-D1 | Add `yolo config check`: config validation only, read-only, same exit contract as `check`. | designer |
| CV-D2 | A probe belongs in `config check` only if it is *config-determined* (defined in [3.1](#31-the-membership-rule)). | designer |
| CV-D3 | `config check` calls the same validator (`config.ValidateConfig` and the same section functions) as `yolo check`, with no second validator and no subprocess-free reimplementation. Where a section function mixes config validity with environment probing (runtime resolution, executable-path probes), the function is parameterised or split in place so both commands share the validity half; it is not copied. Any FAIL `config check` raises, `yolo check` raises on the same inputs. Because the validator resolves fetched packs with `ResolvePack(ReadOnlyStore:true)`, `config check` inherits its read-only `git rev-parse` / `git ls-tree` probes of the existing pack store and adds none of its own. | designer |
| CV-D4 | Every report from either command ends with a *scope block* naming what was not examined and the verb that examines it. | designer |
| CV-D5 | `yolo check` keeps its breadth and meaning. `--no-build` is not narrowed. `doctor` stays an alias. | designer |
| CV-D6 | The disk-usage section leaves `yolo check`. The scope block points to `yolo stores` and `yolo prune`. | designer |
| CV-D7 | Inside a jail, `yolo check` with neither build flag does not build. Explicit `--build` is still honoured. | designer |
| CV-D8 | `config check` takes no `--build`, `--no-build` or `--accept-config-changes`. It never approves, nudges, migrates, or runs a `doctor_cmd`. | designer |
| CV-D9 | Guidance becomes one rule on every surface (see [6. Guidance](#6-guidance)). | designer |
| CV-D10 | After a public workspace `config set/add/unset` writes a change, its automatic post-write validation uses the same `yolo config check` entrypoint. Preserve write-before-check disclosure and failure/exit propagation; do not run it for a dry run, no-op, or user-scope edit. | designer |

The owner's authorising statement (asking for better guidance and a better-shaped check) is the basis for making these as engineering choices. No decision above is an owner quote.

## 3. `yolo config check`

### 3.1 The membership rule

*Config-determined* (coined here): a probe whose verdict is a function only of these inputs:

- the user and workspace config file contents;
- the declarations of the packs that config selects;
- on the host only, no-follow existence and type of paths the config names.

Such a probe needs no network, container runtime, Nix daemon, credential, prompt, build, install, storage walk or persistent write. Its only permitted subprocess is a read-only `git` query against a pack store that already exists, issued by the shared validator's `ReadOnlyStore` resolution. It never fetches, clones or writes a store. Anything else is environment readiness and stays in `yolo check`.

| Area (current section) | In `config check`? | Note |
|---|---|---|
| Config files: parse and load user and workspace (TOML; legacy JSONC still accepted) | Yes | Same findings and strings as today. |
| Merged config: merge, provenance, resource groups, schema, scopes, paths, providers, profiles, models, pack relationships | Yes | Repo-flake resolution is included only in a form that needs no Nix or network (A3). Runtime resolution and executable-path probes are excluded and named in the scope block. |
| Selected packs: resolution, staging plus declaration, contribution, audience, capability and name-reservation validation | Yes | Selected packs only, resolved with `ReadOnlyStore:true`. Local packs are read from disk. Fetched packs are read from the existing store through the validator's read-only Git probes. Needs bounded temporary staging (see [3.4](#34-effects-and-concurrency)). A fetched pack absent from the local store is SKIP, per the case table. |
| Inline loophole declarations | Yes, declaration validity only (A2) | Executable-path probes and broker or service liveness are environment readiness. They stay in `yolo check` and are listed in the scope block. |
| Runtime resolution (`resolveRuntimeForCheck`, `LookPath`) inside the merged-config section | No | The configured runtime *value* is validated. Whether that runtime is on this machine is readiness. Scope-block entry. |
| Entrypoint dry-run | No | It renders every embedded pack surface, not just the selected declarations; it is broader than config/declaration validation. It stays in `yolo check` whole and is listed in the scope block. |
| Storage ensure/migrate, pack nudge, cache relocation, approval snapshot | No | These are writes. |
| Runtime, Nix, macOS, devices, image, delivery, running jails, host loopholes and brokers, services, durable, wrappers, PATH, updates | No | Environment readiness. |
| `doctor_cmd` | Never, in either command's validation phase | It may fetch or mint credentials. |

### 3.2 Invocation and output

| Aspect | Behaviour |
|---|---|
| Forms | `yolo config check`, `yolo config check --json`, `yolo config check --format json`, `yolo config check --help`. |
| Help | Printed before any work. Its first line says what the verb is not: "Validates config only; does not test the runtime, image, or services — that is `yolo check`." |
| Unknown flag or invalid format | Exit 2 before any read. This includes `--build`, `--no-build` and `--accept-config-changes`; the message names `yolo check`. |
| Exit codes | 0 if no FAIL, 1 if any FAIL, 2 for usage. WARN and SKIP do not fail. |
| Text shape | A mode banner, then the admitted sections with the existing badges and counts, then the scope block. |
| JSON | Rendering-only, as for `check`: same work, same graded findings, remedies and counts, plus `mode` and `scope` (see [3.3](#33-the-scope-block)). No secrets, as today. |

Exit 0 means "no config FAIL". The banner, the scope block and the help all say it does not mean "ready to launch".

**Staged gating** follows `check`: parse, then merge/validate, then packs. There is no entrypoint stage in `config check`. A FAIL in one stage ends the run with exit 1. Later stages are printed as *not reached* (coined here: in scope but gated by an earlier FAIL). They are never printed as OK and never counted as SKIP. A fetched pack that is not in the local store is a SKIP, not a FAIL, and does not gate: the remaining packs and checks still run. The report is therefore not exhaustive after a failure, and it says so in one line: "Fix the above and re-run `yolo config check`."

**Degenerate cases:**

| Case | Result |
|---|---|
| No workspace config | Validates the user config alone; the banner says "no workspace config". Exit by findings. |
| No config at all | One OK finding, "no config files; defaults apply". Exit 0. |
| File present but unreadable | FAIL naming the path. Exit 1. |
| Legacy JSONC | Today's finding is unchanged; the remedy names `yolo config migrate`. |
| No packs selected | Pack section reports "none selected". No staging happens. |
| Local pack declaration unreadable or invalid | FAIL, same id and string as `yolo check`. |
| Fetched pack present in the local store at the pinned revision | Verified by the read-only Git probes, then validated exactly like a local pack. An invalid declaration is FAIL. |
| Fetched pack not available locally (store missing, pinned revision absent, or `git` cannot be run) | Not attempted further: no fetch, no store creation. That pack's row is SKIP, with the reason and the command that fetches it. All validation that does not need that pack's declaration still runs. Cross-pack checks (capabilities, name reservations) run over the packs that were read, and the report says they did not cover the skipped pack. The summary line states how many selected packs were not validated, so the run can never read as an unqualified "config valid". SKIP follows the existing SKIP rule for exit status. |
| Anything that would need network or Nix | Not attempted; listed in the scope block. |

### 3.3 The scope block

*Scope block* (coined here): the closing part of every report, listing each area not examined as area, reason and next verb. It is not a section and carries no badge or count. An omission can therefore never be read as a pass, and it does not inflate SKIP.

- **Text.** A "Not checked" list.
- **JSON.** `mode` is `"config"` or `"full"`. `scope.not_checked` is an array of `{area, reason, verb}`, and `scope.not_reached` is an array of area names. This is additive: existing fields are unchanged.

| Context | Entries always present |
|---|---|
| `config check`, host | Runtime availability and executable resolution, Nix, image, services and loophole liveness → `yolo check`. Entrypoint dry-run → `yolo check`. Config-change approval → asked at launch, or `yolo check --accept-config-changes`. Disk → `yolo stores`. |
| `config check`, in jail | The host entries above, plus "host paths and mounts not verified — this is a generated snapshot; run `yolo config check` on the host". The banner reads "config only · in jail · host facts unverified". |
| `yolo check` (any) | Disk usage → `yolo stores` (inventory, size, ownership), `yolo prune` (preview), `yolo prune --apply` (reclaim). |

In a jail, path-existence findings for host paths are not emitted at all, neither OK nor FAIL. The scope line replaces them.

### 3.4 Effects and concurrency

| Property | Rule |
|---|---|
| Persistent writes | None. No storage ensure or migration, no cache dirs, no snapshot, no pack add, no config rewrite. The existing immutable embedded-pack cache must not be materialized by this command; use the shared validator's embedded declaration source without creating it. |
| Temporary writes | Selected-pack staging goes into a per-process private temp directory. It is removed on every exit path (success, FAIL, usage error after start, interrupt as best effort), using the cleanup ownership `Check` already has. This is the one qualification to "read-only", and help states it. |
| Subprocesses, network, credentials | No network, no credentials, no prompt. No `nix`, no runtime, no `doctor_cmd`, no agent install, no build, no API call, no `LookPath` for a runtime or loophole executable. One exception: the read-only `git rev-parse` / `git ls-tree` calls that `packsrc.Store.ResolveExisting` makes for a selected fetched pack. They run against the existing local store only, with no fetch and no store write. A config with no fetched pack runs no subprocess at all. |
| File safety | Existing rules stay: `.yolo` opened no-follow, no workspace `git` on the host. |
| Prompts | None. It never blocks on stdin. |
| Concurrency | No locks are taken. Each file is read once. A concurrent writer can at worst produce a parse FAIL, which a re-run clears. Parallel runs do not collide because temp dirs are unique. |
| Permissions | Nothing is widened. It reads exactly what `check`'s config phase reads today. |

## 4. Changes to `yolo check`

| Aspect | Before | After |
|---|---|---|
| Breadth | All sections | All sections except disk usage (CV-D6). The durable section keeps its existing 10-second budget and is untouched. |
| Disk section | `ps -a`, per-container inspect, recursive sizing, WARN/OK; in-jail skip text | Retired on host and in jail alike; replaced by a scope-block entry. The hygiene WARN no longer appears in `check`. |
| Build default | Build if missing or stale | Host: unchanged. In jail: no build. A SKIP finding reads "image build skipped inside a jail — run `yolo check` on the host" (CV-D7). |
| `--build` / `--no-build` | Last wins | Unchanged. Explicit `--build` in a jail is honoured. `--no-build` on the host still means "everything but the build". |
| `--no-build` help text | "…the fast preflight to run after editing `yolo-jail.jsonc`, and the one to use inside a jail." | "Skip the image build; every other section still runs. To validate a config edit, use `yolo config check`." |
| Usage text | Lists areas | Adds: "After a config change, `yolo config check` is the fast, read-only form." Drops any mention of disk. |
| `--accept-config-changes`, `--json`, exit codes, early help, exit 2 on bad flags, `doctor` alias | — | Unchanged. JSON gains `mode` and `scope`. |
| Writes (storage ensure, nudge, cache dirs) | Present | Unchanged in this design. `check` is not claimed to be pure; only `config check` is. |
| Config FAIL remedies | Various | Each names `yolo config check` as the re-run verb, and the relevant `yolo config …` or `yolo pack …` verb as the fix. No remedy suggests generic hand-editing. |
| Public workspace config edits | `set/add/unset` currently run `yolo check --no-build` after a write | Run the same `yolo config check` entrypoint automatically. A failed check still discloses that the file was written and propagates the check's non-zero status. Dry runs and no-ops still do no post-write validation; user-scope edits retain their existing behavior. |

## 5. Alternatives

| Option | Verdict | Reason |
|---|---|---|
| Narrow `--no-build` to config only | Rejected | It silently changes an existing flag's meaning; scripts would exit 0 on less coverage. |
| Make `yolo check` config-only and `yolo doctor` the broad report | Rejected | Every existing `yolo check` caller would silently lose readiness coverage. `--build` and `--accept-config-changes` would become nonsense on `check`. It breaks a documented alias. |
| `yolo check --scope config\|env\|all` or `--fast` | Rejected | It creates a flag matrix beside the build flags, which is the ambiguity being removed. |
| Keep disk in `check` behind `--disk` | Rejected | `yolo stores` and `yolo prune` already own this; a second door adds a choice. |
| Parallelise or background the disk walk | Rejected | It masks unbounded work in a validity report. |
| Rely on config mutation verbs' own validation | Rejected as sufficient | No evidence of equivalence to merged validation; it does not cover hand or agent edits. It may be revisited once equivalence is proven. |
| New verb in the `config` family (chosen) | Accepted | It sits with `config set/why/drift`, breaks nothing, and has one obvious name. |

## 6. Guidance

One rule, with identical wording on every surface:

> Changed config (by `yolo config …`, `yolo pack …`, or any edit)? Run `yolo config check`: fast, read-only, works on the host and in a jail. Jail won't start or the machine is suspect? Run `yolo check`. Disk? `yolo stores`, then `yolo prune`.

| Surface | Change |
|---|---|
| `AGENTS.md` | Replace "Run `yolo check` after every edit… `--no-build` is the fast in-jail preflight" with the rule. |
| `configuring-the-jail` skill | "then run `yolo check --no-build`" becomes "then run `yolo config check`". Keep the note that in-jail runs cannot judge host mounts. Keep the preference for config verbs over file edits. |
| `diagnosing-the-jail` skill | Step 1 stays `yolo check` (no flag needed in a jail after CV-D7). Its description no longer claims it is the config-edit check. |
| CLI help and user guidance | `check`, `config`, and `config check`, plus config-edit instructions in the user guide, as in sections 3 and 4. Public config edits also run the same check automatically after a successful write. Keep `yolo check` wherever the advice is about machine/runtime readiness or image build rather than config validity. |

The skill files in the workspace are generated copies. The edit goes in their canonical templates, which the planner locates. No surface tells an agent to choose between `--build` and `--no-build`.

## 7. Assumptions and unverified facts

| ID | Assumption | If false |
|---|---|---|
| A1 | A missing fetched pack is reported as a per-pack SKIP through the shared validation path while unrelated config validation continues; cross-pack checks state their partial coverage. No fallback may abort validation of unrelated settings. | The same validator path must expose the missing-pack outcome while continuing unaffected checks; never fetch or claim the pack is valid. |
| A2 | Inline-loophole declaration validity is separable, in place, from executable-path probes and from broker or service liveness. | If not separable, the whole section stays in `yolo check` only, with a scope-block entry. |
| A3 | Repo-flake resolution inside merged-config validation has a form that needs no Nix and no network. | That sub-probe leaves `config check` and becomes a scope-block entry. CV-D2 decides; no new ruling is needed. |
| A4 | Selected-pack staging is local and bounded: no network, no Nix, and no subprocess beyond the read-only Git probes of CV-D3. | If a pack is not available in the local store, report SKIP for that pack and continue unrelated validation; do not omit readable declarations or fail the whole command. |
| A5 | The in-jail condition is detectable by the mechanism the disk section already uses for its skip. | The plan names the detector. Behaviour is unchanged. |
| A6 | The read-only Git probes keep `config check` near one second for a config with fetched packs. Unmeasured. | Record the sample for evidence. Do not claim an SLO, add a timeout that turns into a pass, or weaken pack validation based solely on the measurement. |

These are implementation constraints and evidence items, not owner questions. The completed plan records the live call sites and the exact adjustments needed to meet CV-D2 without a second validator or an all-or-nothing fallback.

## 8. Risks

| Risk | Mitigation |
|---|---|
| Exit 0 from `config check` is read as "ready". | Banner, scope block, the JSON `mode` field, and the first line of help. |
| `yolo check` is still slow (runtime readiness up to a minute, Nix probes, staging). Disk was never measured as the cause. | Not claimed fixed. The build should capture one per-section timing sample on host and in jail as evidence, and open a follow-up if another section dominates. |
| The two commands drift apart. | CV-D3: shared section functions, plus the subset test in section 9. |
| The disk-bloat nudge is lost. | The scope block names `yolo stores` on every `check` run. |
| A golden or JSON consumer breaks. | JSON is additive only. Text goldens are re-pinned deliberately in one change. |
| A script inside a jail relied on the default build. | Explicit `--build` still works. The SKIP finding is visible and not counted as a pass. |

## 9. Acceptance

These are observable and structural. Wall time is recorded as evidence only (AC1a), not an acceptance gate.

| ID | Criterion |
|---|---|
| AC1 | Through production dispatch (the `TestCheckOptionsWiring` style, not a fake callee), `yolo config check` makes zero calls on the injected `BuildImage`, runtime and storage-ensure seams, zero `LookPath` calls for a runtime or loophole executable, and zero filesystem writes outside its temp dir. On the `Exec` seam, a config with no fetched pack makes zero calls. A config with a fetched pack makes only `git rev-parse` / `git ls-tree` calls against the existing store, and no `fetch`, `clone` or other writing verb. Assert the fixed store fixture has no new entries and its pinned files' sizes and bytes remain unchanged. The temp dir is gone after success, FAIL, SKIP and gated exits. |
| AC1a | Wall time of `config check` is recorded for (a) no fetched pack and (b) one cached fetched pack, on the same machine, and reported with the change. It is evidence for A6, not a pass/fail gate. |
| AC2 | Pack nudge, cache relocation, approval snapshot and `doctor_cmd` call sites are unreachable from `config check`, asserted at the call site. |
| AC3 | Subset property: for a table of invalid configs (parse, schema, scope, provider/profile/model, pack relationship, and an invalid declaration inside a cached fetched pack), every FAIL from `config check` appears with the same id and string in `yolo check --no-build`. |
| AC3a | Fetched-pack fixtures. (a) Cached and valid: pack rows are OK. (b) Cached with a capability or name-reservation conflict: FAIL. (c) Store missing, (d) pinned revision absent, (e) `git` not runnable: that pack is SKIP with a reason and the fetch command, there is no FAIL for it, no store is created, an unrelated invalid setting in the same config still FAILs, and the summary names the count of packs not validated. |
| AC4 | Exit codes: 0, 1 and 2 as specified. `--build`, `--no-build` and `--accept-config-changes` on `config check` exit 2 before any read. `--help` does no work. |
| AC5 | Goldens pin the banner, sections, not-reached rendering and scope block for host, in-jail, no-config and gated-failure cases. JSON carries `mode` and `scope` and is otherwise equal in findings to text. |
| AC6 | In-jail `config check` emits no host-path verdicts and does emit the "host facts unverified" scope entry. |
| AC7 | `yolo check`: the disk section's container enumeration and recursive sizing are not invoked (call-site assertion). The scope block names `yolo stores` and `yolo prune`. All other sections' goldens are unchanged. |
| AC8 | `yolo check` in a jail with no build flag does not call `BuildImage` and prints the SKIP finding. With `--build` it does call it. Host default is unchanged. |
| AC9 | A real-CLI smoke on the built binary runs `yolo config check` against a valid and an invalid workspace. The existing full gate (`just check-ci`, targeted integration, matched dev-bundle smoke) passes. |
| AC10 | No guidance surface or help string recommends `--no-build` for config edits. The canonical skill templates and the rendered copies agree. |

Native macOS staged reachability cannot be proven from a nested jail. AC9 on a macOS host is for the Mac agent to run.

## Appendix: seed material for the plan

- The canonical sources of the two skills are under `internal/jailcontent` or a pack. Locate them before editing; never edit the rendered copies.
- Resolve A1–A6 from the live tree, including the exact selected-pack, runtime, entrypoint, and cache boundaries; they fix the final section list and scope-block entries.
- `Check` currently calls `EnsureGlobalStorage` before any section. `config check` needs an entry that does not pass through it.
- The check goldens pin exact strings, so the disk retirement and scope block land as one deliberate re-pin.
- Trap: `Format` is rendering-only. Do not let `mode` leak into work selection through the format path.
- Trap: normal `packload.Embedded()` materialization uses the persistent shared cache. Do not use it for `config check`; selected embedded declarations need a private temporary read that preserves the same loader semantics and does not inspect unselected packs.
- Trap: host `git` must not run in the workspace; the existing durable-section rule applies to any reused helper.
- Route this design into `docs/plans/roadmap.md` (integrator's task).