---
title: "Environment-manager implementation plan"
status: deprecated
stage: SUPERSEDED
next: "Nothing owed here: Phase 7 and the confinement key were replaced by named setups (docs/design/named-setups.md); the Linux guest is its reserved landlock-yolo-account setup (MT-R2) and user-stories Q7"
depends-on:
  - ../design/environment-manager-user-stories.md
---

# Environment-manager implementation plan

**Status:** 2026-11-12 — superseded in part by
[named setups](../design/named-setups.md), and kept for its phase record and its
[ledger](#decision-ledger), which [`what-yolo-is.md`](../reference/what-yolo-is.md) cites. Build
status was re-verified against the tree 2026-08-23, rows corrected 2026-09-24, re-checked
2026-09-30. Phases 0–6 and 9 are built and 8 is built but for 8.2, which is moot. **Phase 7, the
`guest` notch, no longer exists as a launch input.** 7.1, macOS `guest`, was built 2026-10-04 and
measured on a Mac (`TestMacosUserGuestNotchLaunchesTheSandbox` passed on the scheduled
`macos-user` job, run 38143926912, 2026-10-11); then on 2026-11-04 `f678594a5` refused `--at`
and `8b83df228` the `confinement` key, each naming setups instead, so the Mac half is now the `seatbelt-self` and
`seatbelt-yolo-account` setups and [EMP-D1](#EMP-D1) to [EMP-D5](#EMP-D5) describe retired
spellings. 7.2, Linux `guest`, has no code; named setups reserve it as `landlock-yolo-account`
([MT-R2](../design/named-setups.md#decision-ledger)), and it still waits on
[user-stories Q7](../design/environment-manager-user-stories.md#Q7), whether a Linux guest stays a
promise. The body below is the record as of 2026-10-04 and is not re-verified against named setups. Sequences the environment-manager design (the vision — finalized, the maintainer is happy with it;
graduated 2026-10-16 into [`what-yolo-is.md`](../reference/what-yolo-is.md)) into buildable phases.

> **Build status — verified against the tree 2026-08-23; rows 1, 5 and 6 corrected 2026-09-24.**
> Read this table, not the 2026-08-01 summary it replaces: that summary said "Phases 0–6, 8, 9
> SHIPPED" flat, and three of those were partial in ways that misdirect real work. Each phase
> heading below repeats its verdict.
>
> | Phase | Verdict, 2026-08-23 | The residue |
> |---|---|---|
> | **0** — data-loss fix (G1/G2) | ✅ **SHIPPED** `1220ac55` 2026-08-01, hardened `2b317dba` 2026-08-02 | none |
> | **1** — one renderer (`internal/render`) | ✅ **SHIPPED**, but **1.4 landed 2026-08-12, not 08-01** (`a39628ad`) | none — macOS staging was measured on a Mac on 2026-09-10 and again on 2026-09-12 ([runbook item 4](runbooks/macos-user-manual-checks.md#4-content-actually-reached-the-agent)) |
> | **2** — the `confinement` key | ✅ **SHIPPED** (`internal/config/confinement.go`, `internal/render/confinement.go`) | none |
> | **3** — `apply` + `describe` | ✅ **SHIPPED** (`internal/cli/apply.go`, `describe.go`) | none |
> | **4** — `yolo host apply` | ✅ **SHIPPED**, 4.3 included | `internal/cli/applyhostdeps.go` resolves a pack's `program` AND `requires` into the host's real dep state and offers the install; its own package comment records that it **replaces** the static "not run by `apply --host` yet" line this row used to cite, which was *"true and useless: the gate is real, but the line never said WHICH binary was missing"* |
> | **5** — `--sealed` + closure | ✅ **SHIPPED** — 5.1/5.2/5.4, and **5.3 on 2026-09-12** (`4223ca95`, `yolo config promote`) | none — `applySealed`'s refusal now names `yolo config promote` as a command beside `yolo config reset` |
> | **6** — dep provisioning | ✅ **SHIPPED** — 6.1/6.2/6.3 (`internal/depcheck/`, `yolo check-deps`), and **6.4's offer by 2026-09-12** (`f94b2c97`, the `yolo host apply --assert` dependency gate) | the offer is ONE prompt, not batched by elevation class; that batching was [`OQ-EM1`](../reference/what-yolo-is.md#why-its-this-way)'s, answered 2026-09-30: not owed |
> | **7** — the `guest` notch | ⚠️ **SUPERSEDED** — 7.1 (macOS) built 2026-10-04 and measured on a Mac 2026-10-11, then replaced by named setups (`f678594a5`, `8b83df228`); 7.2 (Linux) not built, reserved as `landlock-yolo-account` | see below |
> | **8** — self-describing briefing | ⚠️ **PARTIAL** — 8.1 shipped (`confinementHeader`, `internal/jailcontent/briefing.go`); **8.2 is MOOT** | the `jail-startup` built-in no longer exists |
> | **9** — agent autonomy | ✅ **SHIPPED** 2026-08-01 | none |
>
> **Phase 7 (the `guest` backend — macOS Seatbelt + Linux bwrap/Landlock) is half built.**
> 7.1 landed 2026-10-04 as a routing and policy decision on a backend that already shipped
> ([EMP-D1](#EMP-D1), [EMP-D2](#EMP-D2)), unit-tested on Linux and unmeasured on a Mac. The rest of
> this paragraph is the 2026-08-23 record, when neither half was built: it needed host
> capabilities a nested Linux jail cannot exercise, so it was host/Mac-gated and deferred. Two
> things sharpened since 2026-08-01. **7.1's precondition landed**: the
> zero-surfaces bug (1.4/G3) was fixed 2026-08-12, so macOS `guest` stages packs, and that
> staging was measured on a Mac on 2026-09-10 and 2026-09-12 ([runbook item 4](runbooks/macos-user-manual-checks.md#4-content-actually-reached-the-agent)).
> **7.2 has no code at all**: `bwrap`/`Landlock` appear in Go only as a profile constant
> (`GuestProfileLinux`, `internal/render/confinement.go`) and a label; and the notch's render policy is
> explicitly unstated — `KindGuest: UndecidedModes("… Phase 7's to state")`
> (`internal/render/modes.go`). The three-notch **vocabulary** shipped ahead of the notch,
> which is what user-stories Q7 now asks about.
>
> What shipped is behavior-neutral for the default `jail` notch — `yolo -- claude` is unchanged.
>
> **The `yolo host apply` bypass-leak is FIXED (Phase 9, 2026-08-01):** the shipped agent
> packs' jail-bypass keys live in the `autonomy` kind's `autonomous` posture, rendered only
> at the contained notches; `yolo host apply` renders the `guarded` posture (permission prompts
> on) and warns before overwriting any existing user value.

**What this doc is.** The design doc says *what yolo becomes* — "yolo describes an
environment; confinement is a dial (`jail`/`guest`/`host`); the verbs are `apply` /
`describe` / `check` / `--at` / `apply --sealed`." This doc says *in what order to
build that*, what each phase depends on, and what has to be decided before each phase
starts. It does not restate the design; read it beside the design doc, section for
section.

**What this doc is NOT.** It is not a second copy of the host-render work.
[`BACKLOG.md`](BACKLOG.md) **Stage G** already sequences the render-path refactor
(`internal/render` + `Target` + `FieldSet` + host render) in implementable detail, with
`host-render-target.md` as its reasoning. That IS Phase 1 below — this plan points at
it rather than re-listing G1–G6. Where a phase's design already lives in a doc, the
phase cites it; where it does not, the phase says "design first" and names the open
questions.

> [!WARNING]
> **⚠ Retracted: "Nothing here is built."** That sentence was true when written and is now the
> most misleading line in the doc, so it is preserved rather than deleted — a reader who finds it
> quoted elsewhere should know it lapsed. **As verified 2026-07-31** it read: *"no `confinement`
> config key, no `apply`/`describe`/`--at`/`--sealed` verb, no `guest` notch (`bwrap`/Landlock),
> no `internal/render` package exist in the code."* **As verified 2026-08-23**, four of those
> five exist: `internal/config/confinement.go`, `internal/cli/apply.go` (`--at` at `:54`,
> `--sealed` at `:69`), `internal/cli/describe.go`, and `internal/render/`. Only the `guest`
> notch's `bwrap`/Landlock half is still absent. See the build-status table above.

The two shipped pieces the design leaned on at the outset
are `yolo config dump` (the canonical computed-config dump `describe` absorbed as
`describe --json`) and `yolo config drift` (the in-jail cousin of the drift/sealing story). The pack system —
`contributes[]`, the twelve kinds, the compose engine, fetched-pack install-time
approval — is shipped and is the substrate every phase renders through
([`../reference/pack-system.md`](../reference/pack-system.md#selection-and-the-load-path)).

**Reads with:** the design doc (the spec); [`BACKLOG.md`](BACKLOG.md) Stage G +
[`../design/host-render-target.md`](../design/host-render-target.md) (Phase 1's
detailed design); [`../reference/pack-system.md`](../reference/pack-system.md#selection-and-the-load-path) (the pack
substrate); [`fill-the-matrix-principle.md`](../reference/fill-the-matrix-principle.md) (the
constraint on how many knobs each phase may expose).

---

## The dependency spine

Every phase after 0 depends on the render-path collapse, because the whole vision is
"one renderer, several targets" and today there are two hand-copied renderers. So the
order is not the *narrative* order of the design doc — it is foundation-first:

```
Phase 0  data-loss fix        (Stage G1/G2)      ← ship regardless, waits on nothing
Phase 1  one renderer         (Stage G3/G4/G5)   ← THE foundation; everything below needs it
Phase 2  confinement key      (§4)               ← names the dial; no behavior change yet
Phase 3  apply + describe     (§3.1, §3.2)       ← the verbs, jail-only first
Phase 4  yolo host apply      (Stage G6, §4.1)   ← the host notch becomes real
Phase 5  --sealed + closure   (§3.3)             ← the reproducibility guarantee
Phase 6  dep provisioning     (§3.4, §3.5)       ← check-at-notch + the manifest surface
Phase 7  guest notch          (§4, §4.0)         ← the middle notch actually works
Phase 8  self-describing      (§6)               ← the briefing states the notch
Phase 9  agent autonomy       (§4.2)             ← bypass is a notch policy, not baked config
```

Phases 0 and 1 are already scoped in BACKLOG Stage G. Phases 2–9 are this plan's
addition, and each is gated by the decisions in its "Before you start" note. Phase 9 was
added 2026-08-01 (the `yolo host apply` bypass-leak); it depends on 1/2/4 but not on 7.

---

## Phase 0 — Stop the destructive host-side write  *(was BACKLOG G1 + G2)*  ✅ **SHIPPED 2026-08-01**

**Design/reasoning:** `host-render-target.md` [§6.1](../design/host-render-target.md#61-finding-yolo-already-does-this-and-it-is-destructive) (the probes), [§8](../design/host-render-target.md#8-what-i-would-actually-do-in-order) step 1.
**Depends on:** nothing. **Ship regardless of whether the rest happens.**

> [!NOTE]
> **Verified 2026-08-23.** Commit `1220ac55`, hardened by `2b317dba` (2026-08-02). The predicate
> the items below prescribe is now a shared guard, `refuseHostSideWrite`
> (`internal/cli/configdiff.go`), wired at **both** verbs before any enumeration: `configReset`
> (closing G1) and `configCapture` (closing G2). Item 0.3's regression test was
> `TestResetCaptureRefuseHostSideWithoutForce`; the host-side reset cases are now pinned in
> `internal/cli/hostownedreset_test.go`. **The predicates the items below name are retired:**
> `surfacesAreLocal()` and `composedFileExists` gave way to the one resolved `configTarget`, and
> `TestTheRetiredPredicatesHaveNoOtherCallers` keeps them gone. `truncateSurfaceToPureRender`'s
> one caller, in `configReset`, sits downstream of the guard. `--force` is the escape hatch, as
> specified.

Host-side `yolo config` verbs already resolve `~` against the invoking human's *real*
home (`expandHome` → `paths.Home()`) and two of them write — a destructive posture
yolo is already in by accident. This is the one live data-loss bug in the whole
cluster, it is independent of the architecture below it, and it is ~20 lines.

- **0.1 — `config reset` destroys real user config. *(G1, ⚠ data-loss)*** The bug:
  `truncateSurfaceToPureRender` (`cli/configdiff.go`) composes with **no computed
  layer** against the real `$HOME`. Probed:
  - `reset mise` truncated a real `~/.config/mise/config.toml` (20 bytes → `"\n"`, the
    user's `[tools]` gone) — its content is *entirely* the computed layer.
  - `reset codex`/`opencode` replaced real files with yolo's managed keys only.
  - `reset claude` merged yolo's managed layer into the user's own file.

  **Fix:** refuse (or require `--force`) in `configReset` (`configdiff.go`) when
  `surfacesAreLocal()` (then in `configls.go`) is false — the predicate already exists and
  is currently consulted only by `composedFileExists`. `configCapture`'s own
  docstring already says a host-side re-render is wrong for exactly this
  reason; the reasoning is one function away, just not applied.
- **0.2 — `config capture` leaks host config into the workspace. *(G2, privacy)***
  `configCapture` copies real host config into `<workspace>/.yolo/prism/`; probed, a
  `~/.codex/config.toml` `api_key_hint` landed in the overlay sidecar. `.yolo/` is
  gitignored so it is not a commit leak, but it is host content crossing into the
  agent-readable workspace tree unasked. **Same `surfacesAreLocal()` predicate fixes
  it.**
- **0.3** Regression test: a host-side `reset`/`capture` in a non-jail home is a
  refusal, not a write.

**Done when:** a host-side `reset`/`capture` cannot mutate a real dotfile (0.1) or copy
host config into the workspace (0.2) without `--force`. Its own shippable unit — do it
first.

---

## Phase 1 — One renderer, several targets (`internal/render` + `Target`)  *(was BACKLOG G4 + G5 + G3)*  ✅ **SHIPPED** (1.1–1.3 2026-08-01; **1.4 2026-08-12**)

**Design/reasoning:** `host-render-target.md` [§3](../design/host-render-target.md#3-the-design-inside-yolo) (the load-bearing section), [§4](../design/host-render-target.md#4-the-four-couplings-under-a-target), [§7.1](../design/host-render-target.md#71-yolo-launches-a-jail-nothing-observable-changes).
**Depends on:** Phase 0 (so the refactor lands on non-destructive host paths).

> [!NOTE]
> **Verified 2026-08-23.** `internal/render/` exists with `target.go`, `fieldset.go`, `modes.go`,
> `confinement.go`. `FieldSet.Refuse` (1.3) names the kind and gives the census's reason
> (`internal/render/fieldset.go`) — including a hand-written inverse reason for `loophole`,
> because the generic line reads as obviously wrong for a host daemon.
>
> **Correction to the 2026-08-01 status: 1.4 was NOT shipped then.** G3 was fixed six weeks after
> the plan declared Phase 1 done, by `a39628ad` (2026-08-12, *"fix(run): stage packs BEFORE the
> backend dispatch — macos-user rendered none"*). That matters because 1.4 is described below as
> "the cheapest test of 1.1–1.3" and the proof that `Target` can express a real home — so for six
> weeks Phase 4 was building on an abstraction whose stated proof had not run.

This is the foundation — every phase below needs it. Today `agentcfg.Compose` /
`ComposeStateful` have **two independent callers**: the in-jail boot render
(`entrypoint/prism.go`) and the host-side `config` verbs (`cli/config.go`,
`cli/configdiff.go`). The code already admits the second is a hand-copy of the
first, in three comments — two in `prism.go` ("Mirrors `internal/cli.loadTransformScript`",
and "Mirrors `internal/cli.expandHome` … keyed on the Env rather than the
process `$HOME`"), and the `cli/config.go` header promising it runs "the SAME engine."
**Every Phase-0 defect is a drift between these two copies.** Collapse them into one
`internal/render` package parameterized by an explicit `Target`.

- **1.1 — introduce `internal/render` with `Target` + three constructors. *(G4 core)***
  ```go
  type Target struct { Home, Workspace, SidecarDir string; HostLayer HostLayer
                       Tables Tables; Hooks map[string]HookFunc; Fields FieldSet; Posture Posture }
  func Jail(e *entrypoint.Env) Target   // the boot render, behavior-identical to today
  func Preview(dir string) Target       // `config render` — writes nothing outside dir
  func Host(home string) Target         // Phase 4
  ```
  Move the three surface writers out of `entrypoint/prism.go`, keyed on `Target` instead
  of an implicit `*Env`/`paths.Home()`. `entrypoint` calls `render.Jail(e)`; `cli` calls
  `render.Preview()`.
- **1.2 — delete the duplication G4 exists to kill.** The two mirrored helpers above, and
  the two hand-maintained maps `surfaceHasHostLayer` / `surfaceHasComputedLayer`
  (then in `configls.go`), become *derived from `Target`* — `config render` stops being an
  approximation of the boot render and *is* the boot render against a temp home.
- **1.3 — `FieldSet`. *(G5)*** A target declares which kinds apply; an inapplicable kind
  gets a refusal **naming the kind**, never a silent skip. The census
  (`host-render-target.md` [§2.1](../design/host-render-target.md#21-but-measure-how-much-of-a-pack-the-host-actually-wants)): only `config` is target-independent; `program` must be
  refused, `mount`/`reads-host` are unavailable off-container and must be refused rather
  than emulated (a copy goes silently stale). G3 below is what a silent skip looks like
  after a year in production.
- **1.4 — fix macos-user as a target row. *(G3, and the cheapest test of 1.1–1.3)***
  macos-user renders **zero pack surfaces every launch, silently**: `RunDarwinBootstrap`
  calls `LoadJailPacks`/`ConfigurePackSurfaces`/`RunPackHooks` (`entrypoint/darwin.go`),
  but the run path returns *before* `stagePacks`, so
  `YOLO_PACK_ROOT` is never set and the loop runs over an empty list.
  (`macos-user-nix-and-features.md` still wrongly claims selection works.) This is an
  existing backend that *should* render into a real home and renders none — **if `Target`
  cannot express macos-user cleanly it will not express `host` either**, so fixing it here
  proves the abstraction before Phase 4 bets on it.

  > [!NOTE]
  > **1.4 DONE 2026-08-12 (`a39628ad`), verified 2026-08-23.** Staging moved above the backend
  > dispatch — `o.stageRunPacks(cname)` in `Run` (`internal/cli/run/run.go`), ahead of the
  > macos-user branch, with `staged.root` passed through. `YOLO_PACK_ROOT` is set by
  > `macosuser.BuildRunPlan` and deliberately left unset when nothing staged, so "no packs" is
  > stated by absence. `PlanInvariants` refuses the silent shapes. Pinned by
  > `TestPacksAreStagedBeforeBackendDispatch` (the handler receives a pack root that exists on
  > disk) and `TestPackRootIsEmptyWhenNoPacksAreConfigured`, plus
  > `internal/macosuser/packroot_test.go`. The early return the defect cited is now a
  > container-only repo-root gate macos-user skips. **The doc line was also corrected** (commit
  > `2bb792ff`); that reference has since been rewritten and no longer carries the row.
  > **Residue, closed:** the `sudo -u _yolojail` staging step and the sandbox-uid read were
  > measured on a Mac on 2026-09-10 and 2026-09-12 ([runbook item 4](runbooks/macos-user-manual-checks.md#4-content-actually-reached-the-agent)).

**The one hard risk (`host-render-target.md` [§3.5](../design/host-render-target.md#35-the-two-places-this-could-go-wrong)):** this refactors the **A12-fatal boot
path** — a regression does not misconfigure an agent, it stops jails from *starting*,
including the one you are reading this in. **Retire it with a byte-equality check of every
shipped pack's rendered surfaces, before and after** — the same method Stage D used to
prove the ten Go surface literals equalled their generated pack declarations. Two
constraints: `entrypoint` must **not** gain a `cli` dependency (the edge runs
`cli`→`entrypoint` today, and `internal/render` sits below both); and `liveTables` +
`genStep`'s A12 fail-closed policy stay in the *caller*, not the renderer — that split is
what lets a host target's refusal be a message while the jail stays loud-and-halting.

**Extraction is settled: no** (`host-render-target.md` [§2.3](../design/host-render-target.md#23-extraction-settled-and-the-answer-is-no), decided 2026-07-27). This
lives in yolo as `internal/render`, not a separate util — the field census puts the
boundary through the middle of a single manifest, and G4's value is in the *deletion* of
the duplicate renderer, host target or not.

**Done when:** boot render, `config render`, and (via macos-user, 1.4) a real-home render
all go through one `render.Render(target, surfaces)`; inapplicable kinds refuse by name;
and the byte-equality gate is green.

---

## Phase 2 — Name the dial: the `confinement` key  ✅ **SHIPPED 2026-08-01**

> [!NOTE]
> **Verified 2026-08-23.** `internal/config/confinement.go` — `ResolveConfinement`,
> `validateConfinement`, and the load-bearing fallback pinned by
> `TestConfinementValidateAndResolve` (`internal/config/validate_test.go`): **an unknown value resolves to `jail`, never
> `host`.** 2.2's primitive layer is `internal/render/confinement.go` (`Primitive`, the presets
> `JailProfile`/`GuestProfileMacOS`/`GuestProfileLinux`/`HostProfile`, and `AgentAutonomy` as a
> policy knob beside them). 2.3's mechanism-beside-notch printing is
> `printConfinementVector` (`internal/cli/describe.go`).

**Design:** [the confinement dial](../reference/what-yolo-is.md#the-confinement-dial), [why the middle notch is `guest`](../reference/what-yolo-is.md#why-guest-not-sandbox).
**Depends on:** Phase 1 (so a notch is a `Target`, not a code branch).
**Before you start — decide:** [OQ-10](#decision-ledger) (the composable-primitive model shape) — but it is a
"decide at this phase" item, not a blocker you owe an answer up front. Phase 2 ships the
*three named presets*; it must not foreclose the primitive layer underneath.

- **2.1** Add `confinement: jail|guest|host` (default `jail`), and demote `runtime` to a
  mechanism hint inside `jail` (`podman`/`container`/`auto`). `runtime: "macos-user"`
  maps to `confinement: guest` + its mechanism. Keep `runtime` accepted (deprecation, not
  removal) so no config breaks.
- **2.2** Internally, model confinement as a **composable primitive set** (separate user
  / Seatbelt / bwrap+Landlock / namespace) that the three notches are presets over — so a
  future fourth combination (Linux `guest`, seatbelt-without-user) is not a bolted-on
  special case. Only the three presets are user-selectable (`fill-the-matrix-principle.md`);
  hand-assembly is an advanced opt-in, not the front door.
- **2.3** `describe` prints the mechanism next to the notch (the dial is ordinal within a
  platform, not absolute across — a VM `jail` is stronger than a namespace `jail`).

**Done when:** `confinement` selects a notch, `runtime` still works as a mechanism hint,
and the primitive model exists internally even though only three presets are exposed.
**No behavior change for the default** — `yolo -- claude` is still a podman jail.

---

## Phase 3 — The verbs: `apply` and `describe` (jail-first)  ✅ **SHIPPED 2026-08-01**

> [!NOTE]
> **Verified 2026-08-23.** Both verbs are in the dispatch table
> (`registry`, `internal/cli/dispatch.go`) and the help index (`commandHelp`, `internal/cli/help.go`).
> `describe --json` is the canonical config (superseding `config dump`) and `--hash` prints
> **marked unsealed** as 3.2's caveat required — `internal/cli/describe.go`, pinned by
> `TestDescribeVerb`. `--at` parses in `runApply` (`internal/cli/apply.go`) and rejects
> an unknown notch with rc 2 (`TestApplyVerbRouting`). The earlier status note listed "the no-exec
> jail provision" as a deferred within-phase increment; `apply` provisions without launching
> today.

**Design:** [the verbs](../reference/what-yolo-is.md#the-verbs), [`apply`](../reference/what-yolo-is.md#apply), [`describe`](../reference/what-yolo-is.md#describe).
**Depends on:** Phase 1 (apply renders through the one renderer), Phase 2 (a notch to
apply at). Phase 3 does jail level only; the host notch is Phase 4.

- **3.1** `yolo apply`: split provision from launch. At `jail`, builds image + stages
  packs + renders config and exits. `yolo -- <cmd>` becomes "apply, then exec" — same
  behavior, now with a name. The manual-apply cases (set-up-don't-launch, CI provision
  step) are the [`apply`](../reference/what-yolo-is.md#apply) enumeration.
- **3.2** `yolo describe`: print the resolved description; **absorb the shipped `config
  dump`** (canonical form) as `describe --json`. Add the human table ([`describe`](../reference/what-yolo-is.md#describe)) and
  `describe --hash`. **`--hash` over an unsealed environment prints marked or refuses**
  ([`describe`](../reference/what-yolo-is.md#describe) caveat) — it is not authoritative until Phase 5's sealing exists, so gate the
  bare hash on `--sealed` or mark it.
- **3.3** `--at <level>` plumbing on `yolo`/`apply`, so a verb can target a notch other
  than the configured default (the mechanism the escape valve [`program` below `jail`](../reference/what-yolo-is.md#program-below-jail) uses).

**Done when:** `apply` provisions-without-launch at `jail`, `describe` prints the
description (and `--json` supersedes `config dump`), and `--at` selects a notch.

---

## Phase 4 — `yolo host apply`: the host notch becomes real  *(was BACKLOG G6)*  ✅ **SHIPPED — 4.3 by 2026-09-12**

> [!WARNING]
> **Corrected 2026-09-24: 4.3 is built.** `HostFields` honors `program` confirm-gated, and the
> `yolo host apply --assert` dependency gate (`internal/cli/applyhostdepgate.go`) offers the
> install. The rest of this note is the 2026-08-23 finding.
>
> **Verified 2026-08-23 — the previous flat "SHIPPED" hid an unbuilt item.** 4.1/4.2 are real:
> `apply --host` (now `yolo host apply`, `internal/cli/host.go`), `--assert`, default observe/dry-run, pure
> `rmw` over the managed keys, inapplicable kinds refused by name via `FieldSet`
> (`internal/render/fieldset.go`). 4.4's host-dep report is `internal/cli/applyhostdeps.go`.
>
> **4.3 (confirm-gated install) is NOT built, and the shipped behaviour is the OPPOSITE
> position.** `FieldSet` refuses `program` outright below `jail` — *"install is refused below
> jail (a pack must not mutate a real toolchain unprompted)"*
> (`internal/render/fieldset.go`) — which is the design's **original** rule, not the revised
> confirm-gated one that [`program` below `jail`](../reference/what-yolo-is.md#program-below-jail) and item 4.3 below both specify. `yolo host apply`
> prints a static pointer instead: *"apply --host reports host deps; it installs nothing. The
> confirm-gated install is env-manager plan Phase 4.3"*
> (`internal/cli/applyhostdeps.go` — ⚠ the static line this passage describes is GONE; the file now
> resolves deps and offers the install). So [OQ-6](#decision-ledger)/[OQ-7](#decision-ledger)'s resolutions are recorded but
> unconsumed. This is the single largest gap between the design doc and the tree.

**Design/reasoning:** `host-render-target.md` [§6](../design/host-render-target.md#6-the-host-as-a-reduced-target) (the whole section), [§6.5](../design/host-render-target.md#65-the-posture-stated-as-a-table) postures,
[§7.2](../design/host-render-target.md#72-the-human-manages-their-own-machine); [`program` below `jail`](../reference/what-yolo-is.md#program-below-jail).
**Depends on:** Phase 1 (`render.Host`), Phase 3 (`apply`), Phase 2 (`--at host`).
**Before you start — decide:** nothing outstanding. [OQ-1](#decision-ledger)..[OQ-9](#decision-ledger) are all RESOLVED ([OQ-5](#decision-ledger)
moot); the host-render shape (pure `rmw`, user-scoped, no read-in layer, no capture) and
the confirm-gated-install detail ([OQ-6](#decision-ledger)/7) are settled and assumed below.

- **4.1** `render.Host(home)` renders the applicable kinds into the real `$HOME`;
  postures `observe` → `assert` → (maybe never) `own` ([§6.5](../design/host-render-target.md#65-the-posture-stated-as-a-table)). Default `observe`
  (dry-run); `assert` **regenerates only the pack's `managed` keys** and leaves the
  user's own keys — the shipped "regenerate, don't reconcile" model applied to a real
  home. **No `--revert` verb** ([OQ-1](#decision-ledger), resolved): dropping yolo's management is "stop
  declaring the key and re-apply," which removes it with a notice exactly as an unset MCP
  server is dropped in a jail today; there is no restore-to-pre-yolo-state, which would
  need a before-snapshot nothing takes. **User-scoped, workspace contributes nothing**
  ([OQ-2](#decision-ledger), resolved).
- **4.2** Every host config surface is **pure `rmw`** ([OQ-4](#decision-ledger), resolved): `apply` rewrites
  only yolo's own declared keys (`managed` + dynamic tables), fills absent `defaults`, and
  leaves every key the agent wrote untouched — no whole-file compose, no capture overlay.
  A yolo-managed key the agent edits is overwritten on the next `apply` (yolo owns it,
  [OQ-1](#decision-ledger)). `${workspace}`-using surfaces are refused (no referent).
  ⚠ **As shipped this is the `host_management: assert` contract, not the whole notch**
  (2026-09-12): the reversal below made the mechanism a function of a declared key, so under
  `own` the same surfaces render `stateful` with a capture store. `assert` is the default and
  the unset state, so the sentence above still describes what an unconfigured machine does.
- **4.3** `program` (install) below `jail` is **confirm-gated, not refused** ([`program` below `jail`](../reference/what-yolo-is.md#program-below-jail), the
  reviewed position): TTY-only, permission-bounded. Per [OQ-6](#decision-ledger) the confirm shows the resolved
  **URL only** (not the fetched script); per [OQ-7](#decision-ledger)/[OQ-9](#decision-ledger) confirmations are **batched by
  elevation class** — one approval for all no-elevation remedies, one for all `sudo` ones
  (sudo first, so the OS password prompt comes up once at the front).
- **4.4** `check --at host` reports host-render drift and hands off to `apply --at host`
  ([`check` at a notch](../reference/what-yolo-is.md#check-at-a-notch)) — the host-side twin of the shipped `config drift`. `check` never writes.

**Done when:** `yolo host apply` regenerates a pack's `managed` config keys into the
real home (behind `observe`/`assert`) without clobbering the user's own keys, and
inapplicable kinds are refused by name.

---

## Phase 5 — `apply --sealed` and the input closure  ✅ **SHIPPED — 5.3 on 2026-09-12**

> [!WARNING]
> **Corrected 2026-09-24: 5.3 shipped on 2026-09-12** (`4223ca95`). `yolo config promote`
> exists (`internal/cli/configpromote.go`), and `applySealed`'s refusal names it as a command
> beside `yolo config reset`. The rest of this note is the 2026-08-23 finding it replaces.
>
> **Verified 2026-08-23.** `applySealed` (`internal/cli/apply.go`) enumerates and refuses
> the Undeclared tier: `yolo-jail.local.jsonc` and any surface with outstanding
> overlay keys. Tests: `TestApplySealedClosure` (`internal/cli/describe_test.go`).
>
> **5.3 is NOT built and it is the item the rest of the phase leans on.** There is no `yolo config
> promote`: the `config` subverbs are `ls, render, diff, reset, capture, drift, dump`
> (the `config` usage text, `internal/cli/config.go`) and `promote` appeared nowhere in the dispatch table. Capture is
> therefore **still a winning layer, not a staging area** — the refusal in `applySealed` told
> the user to *"promote them into a pack"* — English prose, not a named command (the refusal names
> only `yolo config reset`, which exists) — so the remedy the leaning wants is unavailable, leaving `yolo
> config reset` (discard) as the only shipped remedy. Two facts worth carrying forward: the
> Declared-impure `host`-layer row is **reported by nothing** (`describe` never names a host
> layer), and the capture overlay does **not** outrank every declared layer — it loses to
> `computed`/`managed` ([`compose.go`](../../internal/agentcfg/compose.go); the `transform`
> layer this note also listed was removed 2026-09-11,
> [`OQ-LT1`](../reference/pack-system.md#oq-lt1)), which is why it is a
> closure problem rather than a correctness one. **This is
> user-stories Q1's unbuilt half.**

**Design:** [the input closure and sealing](../reference/what-yolo-is.md#the-input-closure-and-sealing) (the full-closure table + the sealing rule).
**Depends on:** Phase 3 (`apply`). Independent of the host notch — sealing is a
host-side check that needs no container ([the input closure and sealing](../reference/what-yolo-is.md#the-input-closure-and-sealing)).
**Note:** [OQ-3](#decision-ledger) (retire the host read-in layer) is RESOLVED yes, so the closure table's
`host`-layer row has moved from Declared-impure to Declared — one fewer impure input for
sealing to report.

- **5.1** Enumerate the closure at apply time against [the input closure and sealing](../reference/what-yolo-is.md#the-input-closure-and-sealing) four-tier table
  (Locked / Declared / Declared-impure / Undeclared). This is the machine-readable
  version of `describe`.
- **5.2** `apply --sealed`: refuse the Undeclared tier (`yolo-jail.local.jsonc`, an
  outstanding capture overlay), report the Declared-impure tier, proceed only on the
  Locked+Declared definition. No-TTY treats it as it does other gates.
- **5.3** Capture becomes a **staging area**, not a silently-winning layer: recorded,
  reported, promotable (`yolo config promote` into a pack or the workspace config), and
  `--sealed` refuses while any are outstanding. This is the piece that makes "captured,
  and I meant it" expressible.
- **5.4** `describe --hash` becomes authoritative over a sealed definition (closes the
  Phase 3.2 caveat).

**Done when:** `apply --sealed` binds — an environment that passes it was assembled only
from declared inputs, and its `--hash` is a reproducibility pin.

---

## Phase 6 — Dependency provisioning below `jail`  ✅ **SHIPPED — 6.4's offer by 2026-09-12, unbatched**

> [!NOTE]
> **Verified 2026-08-23.** 6.1: packs declare per-manager `install_hints`
> (`internal/depcheck/depcheck.go`, `internal/entrypoint/requires.go`). 6.2: the shared checker
> is `internal/depcheck/` with a standalone entry point `yolo check-deps`
> (`internal/cli/checkdeps.go`, dispatched from `registry` in `internal/cli/dispatch.go`), reused rather than
> re-implemented by `yolo host apply` (`internal/cli/applyhostdeps.go`). The honesty rule holds:
> `noRemedyReason` (`applyhostdeps.go`) distinguishes *"hints cover brew/dnf but not this host's manager"*
> from *"no hints at all"* rather than reporting either as present. 6.3: the manifest is written
> at `~/.config/yolo/Brewfile` and kin (`depManifestDir` in `internal/cli/checkdeps.go`,
> `TestManifestBrewfile` in `internal/depcheck/depcheck_test.go`).
>
> **As of 2026-08-23, 6.4 (offer-to-run, batched by elevation class) was NOT built** and was deferred by name in the
> code: *"It NEVER installs anything… The offer-to-run (behind a batched, sudo-shown-through
> confirm, [OQ-9](#decision-ledger)) belongs to `apply` at a lower notch"* (`internal/cli/checkdeps.go`'s file comment). Since
> `apply`'s half of that is Phase 4.3, also unbuilt, **[OQ-9](#decision-ledger)'s resolution has no consumer at all
> today.** The manifest floor is shipped; only the offer on top of it is missing.
>
> **Corrected 2026-09-12: there IS a consumer now, and it reverses half of [OQ-9](#decision-ledger).**
> `yolo host apply --assert` grew a dependency gate
> ([`applyhostdepgate.go`](../../internal/cli/applyhostdepgate.go)) that offers to run a missing
> `program`'s install — but behind **one** prompt listing every missing dependency, not one per
> elevation class, and **a decline is FATAL at the prompt** where [OQ-9](#decision-ledger) made the manifest a
> floor the run continued over. The gate's own docstring names the reversal and
> [`report-tiers.md`](../reference/report-tiers.md#why-its-this-way)'s [`OQ-RO6`](../reference/report-tiers.md#why-its-this-way)/[`OQ-RO7`](../reference/report-tiers.md#why-its-this-way) carry the
> reasoning: an `--assert`'s promise is a ready environment, so a posture that exits 0 having
> left it unready has stated a result it did not achieve. **The elevation-class batching is
> still unbuilt** — what shipped is the offer, not [OQ-9](#decision-ledger)'s shape for it. `yolo check-deps`
> still installs nothing.

**Design:** [`check` at a notch](../reference/what-yolo-is.md#check-at-a-notch), [dependency provisioning](../reference/what-yolo-is.md#dependency-provisioning).
**Depends on:** Phase 1 (`FieldSet`/notch-aware render), Phase 4 (host apply is where a
missing dep bites). At `jail` this is a near-formality (the toolchain is in the sealed
image); below `jail` it is the real work.
**Before you start — decide:** nothing outstanding — [OQ-8](#decision-ledger) (checker boundary) and [OQ-9](#decision-ledger)
(confirm UX) are RESOLVED. [dependency provisioning](../reference/what-yolo-is.md#dependency-provisioning) fixes the *shape*, not the schema.

- **6.1** Pack-authored `provides`/`install_hints` (per-system: `brew`/`apt`/`dnf`/`nix`),
  declared by the pack that *introduces* the dep (no re-declaring others' deps).
- **6.2** The shared **dep-checker**: probe "is this binary present, at what version,
  what installs it," used by `check`, by every `apply`, and standalone (`yolo
  check-deps`). Boundary ([OQ-8](#decision-ledger), resolved): a **declared schema** a third-party doctor can
  read, yolo shipping a reference checker over it — evolvable to a Go helper later.
- **6.3** The manifest as a **composed surface** at a fixed path (`~/.config/yolo/Brewfile`
  + apt/dnf/pacman kin), composed from all packs' hints, regenerated wholesale every
  `apply`, yolo-owned. Not a one-off in a random dir.
- **6.4** Offer-to-run ([OQ-9](#decision-ledger), resolved): **confirm everything, batched by elevation class**
  — one approval for all no-elevation remedies, one for all `sudo` ones, `sudo` first so its
  OS password prompt (shown through, never captured) comes up once at the front. Not
  per-command, not one blind confirm. Never ambient; no-TTY = print-only; the manifest is
  always the floor.

**Done when:** `check --at host`/`apply --at host` report missing deps, write the
manifest surface, and (behind a confirm) can run the remedies including `sudo`.

---

## Phase 7 — Make the `guest` notch actually work  ⚠️ **PARTIAL — 7.1 built 2026-10-04, 7.2 not built**

> [!NOTE]
> **7.1 is built, and unmeasured on a Mac (2026-10-04).** On macOS `confinement: "guest"` and
> `--at guest` launch the macos-user backend with no `runtime` key (`config.NotchRuntime`, read by
> the launch, `yolo check`, `yolo describe` and `yolo ps`/`prune`), and the backend renders what it
> renders at `confinement: jail`, through `render.Jail` ([EMP-D1](#EMP-D1), [EMP-D2](#EMP-D2)). An
> explicit `runtime` or YOLO_RUNTIME naming a container contradicts the notch and is refused, naming
> both. `yolo apply --at guest` there points at the launch, as the jail notch's apply does. The
> agent's briefing carries the `guest` header, with the shared-account sentence the macos-user
> header already had. A Linux guest launch, and `yolo apply --at guest` on Linux, refuse with
> `render.NotchUnbuilt`, which now says the notch launches only on macOS and names the next step
> ([EMP-D3](#EMP-D3)). Unit tests drive the launch with the platform injected
> (`internal/cli/run/notchgate_test.go`); the Mac check is
> `TestMacosUserGuestNotchLaunchesTheSandbox` (`integration/macosuserguestnotch_test.go`), run by
> `macos-user.yml`. The session is told its notch (`YOLO_CONFINEMENT=guest`, [EMP-D4](#EMP-D4)), so
> the agent footer says `guest`, and every macos-user message whose next step is a container
> runtime names the jail notch with it there ([EMP-D5](#EMP-D5)). **Not done by 7.1:** `yolo config
> --at guest` still refuses on both platforms, naming `--at jail` and `--at host` instead
> (`internal/cli/configtarget.go`), and `describe` prints the composed primitives it already
> printed for macos-user.
>
> **The record below is 2026-08-23's**, when neither half was built.

> [!WARNING]
> **Verified 2026-08-23. 7.1's stated precondition landed; 7.2 has no code.** 7.1: the
> zero-surfaces bug it points at (1.4/G3) was fixed 2026-08-12, so macOS `guest` does stage
> packs — and that staging was measured on a Mac on 2026-09-10 and 2026-09-12
> ([runbook item 4](runbooks/macos-user-manual-checks.md#4-content-actually-reached-the-agent)), so "renders surfaces" is measured, not just wired. 7.2: `bwrap` and
> `Landlock` exist in Go **only as names** — the profile constant `GuestProfileLinux()`
> (`internal/render/confinement.go`), a primitive label, and a briefing test.
> There is no bwrap invocation anywhere. The notch's own render policy is explicitly unstated:
> `KindGuest: UndecidedModes("the guest notch's mode policy is Phase 7's to state")`
> (`internal/render/modes.go`) — a deliberate visible hole rather than an inherited default.
>
> **The ordering risk this phase now carries:** the three-notch *vocabulary* shipped in Phase 2
> ahead of the notch — `confinement: guest` validates, `apply --at guest` parses, `describe`
> prints it, and the briefing has a `guest` body (`confinementHeader`, `internal/jailcontent/briefing.go`). That
> is exactly what [what it costs](../reference/what-yolo-is.md#what-it-costs) warns against (*"a three-notch story with a broken middle"*)
> and what user-stories **Q7** asks a ruling on.

**Design:** [the confinement dial](../reference/what-yolo-is.md#the-confinement-dial), [why the middle notch is `guest`](../reference/what-yolo-is.md#why-guest-not-sandbox); `host-render-target.md` [§9.7](../design/host-render-target.md#9-open-questions--the-discussion-part), [§9.8](../design/host-render-target.md#9-open-questions--the-discussion-part).
**Depends on:** Phase 1 (guest is a `Target`), Phase 2 (the notch exists). This is where
"three-notch story with a broken middle" ([what it costs](../reference/what-yolo-is.md#what-it-costs)) gets fixed.

- **7.1** macOS `guest`: the existing macos-user backend, but rendering surfaces (Phase
  1.4 already fixes the zero-surfaces bug) — separate user + Seatbelt as composed
  primitives (Phase 2.2). **Built 2026-10-04** as [EMP-D1](#EMP-D1)–[EMP-D3](#EMP-D3);
  unmeasured on a Mac.
- **7.2** Linux `guest`: bwrap + Landlock, a real home, no image — "a weaker container,
  no separate user." This is the missing fourth composition the primitive layer (2.2)
  was built to express; it needs no new concept.

**Done when:** `confinement: guest` renders a pack's full portable surface set into a
real, LSM-confined home on both platforms, and `describe` prints the composed primitives.

---

## Phase 8 — The environment describes itself to its agent  ⚠️ **PARTIAL — 8.1 shipped, 8.2 MOOT**

> [!NOTE]
> **Verified 2026-08-23.** 8.1 shipped: `confinementHeader`
> (`internal/jailcontent/briefing.go`, called from `BriefingContent`) emits a per-notch opening
> block with a dedicated `host` body and a `guest` body, plus a derived `enforcementLines` tail. The earlier status note listed "the per-notch body of the
> briefing" as deferred to the guest/host backends; it landed without them.
>
> **8.2 is MOOT: there is no `jail-startup` built-in skill to fix.** yolo's built-in suite is
> `configuring-the-jail`, `developing-yolo-jail`, `diagnosing-the-jail`
> (`internal/jailcontent/builtinskills/`). The startup-ritual skill (`n`) was deleted and the
> one-time handoff became a conditional **Handoff** section in the briefing
> (`BriefingInput.Handoff`, `internal/jailcontent/briefing.go`; see
> [`../reference/host-to-jail-handoff.md`](../reference/host-to-jail-handoff.md)).
>
> **What 8.2 was really for is still owed, and now has no home.** Nothing stamps a rendered
> briefing with the notch it was made for, and nothing asserts that stamp against observable
> reality at startup — so a `host` briefing left on disk and read inside a jail is unremarked.
> Deleting the skill removed the place the check was going to live. That is user-stories **Q4**.

**Design:** [the briefing states the notch](../reference/what-yolo-is.md#the-briefing-states-the-notch).
**Depends on:** Phase 2 (a notch to name). Small, high-value, can land any time after
Phase 2.

- **8.1** The generated briefing states the confinement notch, the grants, and the
  absences ("Confinement: host — changes are not disposable; you have real credentials;
  there is no jail to restart"). Same generator, one accurate paragraph on top.
- **8.2** The built-in `jail-startup` skill stops assuming a container at every notch.

**Done when:** an agent at `guest`/`host` is told it is not disposable, so it does not
take a disposable agent's risks.

---

## Phase 9 — Agent autonomy as a confinement policy  *(SHIPPED 2026-08-01)*

**Design:** [autonomy as a confinement policy](../reference/what-yolo-is.md#autonomy-is-a-confinement-policy) (the whole subsection). **Depends on:** Phase 1
(`render.Profile`/`Target`), Phase 2 (the notch), Phase 4 (`yolo host apply` is where the
defect bites). **Motivated by:** a host agent following the migration guide would
`apply --host --assert` the `claude` pack's jail-bypass keys (`acceptEdits`,
`skipDangerousModePermissionPrompt`, `additionalDirectories:["/"]`,
`--dangerously-skip-permissions`) onto the *real* machine, stripping the prompts that are
the only protection when there is no jail. Today those keys are unconditional pack config
with nothing marking them jail-only.

**Decided 2026-08-01 ([OQ-11](#decision-ledger)): a dedicated `autonomy` contribution kind
(Encoding A below).**

The maintainer delegated the choice ("do the sketch now, I'm not sure I care").
The sketch ([§9.0](#90-the-sketch-that-resolved-oq-11-two-encodings-vs-the-real-packs)) settles it: the discriminator-field encoding forces the `claude`
settings surface to be *split* into a conditional half and an always-on half, which is
exactly the way a jail-bypass key gets left in the unconditional part by accident. The
dedicated kind keeps each posture whole and keeps the always-safe keys in the ordinary
`config` kind, untouched.

### 9.0 The sketch that resolved [OQ-11](#90-the-sketch-that-resolved-oq-11-two-encodings-vs-the-real-packs) (two encodings vs the real packs)

The hard pack is `claude`: its autonomy recipe spans a `config` surface (`~/.claude/settings.json`)
*and* a `launch` flag, and that same settings surface also carries *benign* always-safe keys
(`preferences.autoUpdaterStatus`). `codex` is similar (a `config.toml` surface +
`--dangerously-bypass-approvals-and-sandbox`); `agy`/`opencode` are config-only; `pi` has
*no* autonomy config at all and needs a *guarded* posture invented for the `host` notch.

**Encoding A — a dedicated `autonomy` kind (CHOSEN).** One contribution names both
postures; each posture is a set of config patches (per agent/surface) + launch flags. The
benign keys stay in the pack's ordinary `config` kind and are never touched by the selector.

```jsonc
// claude/pack.json — the confinement-conditional recipe, lifted OUT of the plain config kind
{ "kind": "autonomy",
  "autonomous": {                                  // rendered when the notch's autonomy policy is ON
    "config": [ { "agent": "claude", "name": "settings", "managed": {
        "permissions": { "defaultMode": "acceptEdits", "additionalDirectories": ["/"],
                         "allow": [], "deny": [] },
        "skipDangerousModePermissionPrompt": true } } ],
    "launch": [ { "bin": "claude", "flags": ["--dangerously-skip-permissions"] } ] },
  "guarded": {                                     // rendered when it is OFF (the host default)
    "config": [ { "agent": "claude", "name": "settings", "managed": {
        "permissions": { "defaultMode": "default" } } } ],   // prompts on; NO allow/deny clobber
    "launch": [] } }
// pi/pack.json — permissive by default, so 'autonomous' is empty and only 'guarded' has content
{ "kind": "autonomy",
  "autonomous": {},
  "guarded": { "config": [ { "agent": "pi", "name": "settings", "managed": { /* pi's prompt-on keys */ } } ] } }
```

**Encoding B — a `when: autonomous|guarded` discriminator on existing entries (REJECTED).**
Each `config`/`launch` entry gains an optional `when`. Because the bypass keys are nested
in the *same* `managed` block as the benign `autoUpdaterStatus`, the claude settings surface
must be broken into **three** entries — one `when:autonomous` (the bypass keys), one
`when:guarded` (prompts on), one unconditional (the benign keys) — and the recipe scatters
across the file:

```jsonc
{ "kind": "config", "when": "autonomous", "config": [ { "agent": "claude", "name": "settings",
    "managed": { "permissions": { "defaultMode": "acceptEdits", … }, "skipDangerousModePermissionPrompt": true } } ] },
{ "kind": "config", "when": "guarded",    "config": [ { "agent": "claude", "name": "settings",
    "managed": { "permissions": { "defaultMode": "default" } } } ] },
{ "kind": "config",                       "config": [ { "agent": "claude", "name": "settings",
    "managed": { "preferences": { "autoUpdaterStatus": "disabled" } } } ] },   // must stay unconditional
{ "kind": "launch", "when": "autonomous", "bin": "claude", "flags": ["--dangerously-skip-permissions"] }
```

**Why A wins.**

1. *Safety by construction:* in B, a bypass key mistakenly left in the
   unconditional `config` entry silently ships to `host`; in A the confinement-conditional keys
   physically live in the `autonomy` kind and nowhere else, so "unconditional config" cannot
   contain a bypass.
2. *Legibility:* `describe`/`pack footprint` print one `autonomy` block
   ("autonomous → these keys + flags; guarded → these") instead of reconstructing intent from
   `when` tags scattered over N entries.
3. *Bidirectional reads cleanly:* `autonomous` vs
   `guarded` are two named siblings; `pi`'s empty-autonomous / full-guarded shape is obvious.
4. *Implementation reuse:* an `autonomy` posture's config half **selects which keys enter
   the `managed` layer** of the pack's own surface — it is NOT a `config-overlay` (that kind is
   defined as a contribution to a surface owned by *another* pack, and it folds in *below*
   `managed`, so it could neither target the pack's own surface nor make the `guarded` value win
   over the jail-default managed keys). Concretely 9.3 is "select the posture, fold its config
   patch into the surface's managed map before compose, and merge its launch flags" — reusing
   the existing managed-layer + `InjectLaunchFlags` machinery, not the overlay path and not a new
   renderer.

The cost of A is one new kind (13 → the closed set grows by one) and the schema for a
posture block. Accepted.

- **9.1** Add an `agent-autonomy` **policy** to `render.Profile` (composes beside the
  [why the middle notch is `guest`](../reference/what-yolo-is.md#why-guest-not-sandbox) enforcement primitives; it is a policy knob, not an enforcement one). Preset
  defaults: `jail` → on, `guest` → on, `host` → off. `describe` prints it. A composed
  custom confinement can set it explicitly (the [autonomy as a confinement policy](../reference/what-yolo-is.md#autonomy-is-a-confinement-policy) composability requirement).
- **9.2** Packs declare autonomy postures as data, **bidirectionally** (the subtle half):
  a pack states *both* the `autonomous` posture (config keys + launch flags meaning "no
  prompts") and the `guarded` posture (meaning "prompt"). Guarded-by-default agents
  (`claude`/`codex`/`agy`/`opencode`) get *loosened* by confinement; the permissive-by-
  default agent (`pi`) gets *tightened* at `host`. One selector, run in whichever
  direction the pack's default sits — NOT "always add a bypass."
- **9.3** At render: `profile.agentAutonomy ? pack.autonomous : pack.guarded`. Benign
  always-safe `managed` keys (auto-updater off, trust-dialog) are untouched by this — only
  the confinement-conditional keys move under the selector.
- **9.4** Migrate the shipped packs to the `autonomy` kind ([§9.0](#90-the-sketch-that-resolved-oq-11-two-encodings-vs-the-real-packs) Encoding A): move each
  agent's confinement-conditional keys + `--dangerously-*` launch flag into an `autonomy`
  contribution's `autonomous` block, leave the benign always-safe keys in the ordinary
  `config` kind, and author a `guarded` block (prompts on, no allow/deny clobber) for each —
  including `pi`, whose block is guarded-only. **Invariant:** the `jail`-on path must render
  byte-identical to today — `renderfingerprint_test.go` stays green — so only the host/guest
  paths change behavior.

**Done when:** `yolo host apply` renders an agent with its permission prompts intact (no
jail-bypass keys, no `--dangerously-skip-permissions`), `pi` is tightened at `host`, a
jail boot is byte-identical to today, and `describe` names the autonomy policy in force.

---

## Open questions to resolve before their phase

**None open.** Every question this plan raised, [OQ-1](#decision-ledger) to
[OQ-11](#decision-ledger), is ruled, and each is a row of the [Decision Ledger](#decision-ledger).
The ids are cited from sibling docs and [`BACKLOG.md`](BACKLOG.md), so the ledger keeps them.

> [!WARNING]
> **Four of those rulings were REVERSED on 2026-09-11 by
> [`config-ownership.md`](../reference/config-ownership.md).** That design
> re-argued this ground from scratch, because a ledger is per-document and nothing pointed across;
> the reversals cost four review rounds.
>
> | Ruled here (2026-08-01) | Reversed by | Now |
> | :--- | :--- | :--- |
> | **[OQ-1](#decision-ledger)** — no `--revert` on the host target | [`--revert`](../reference/config-ownership.md#revert) | `--revert` under `assert`, consuming the provenance record that now exists |
> | **[OQ-3](#decision-ledger)** — retire the `reads-host` read-*in* layer | [`OQ-CO10`](../reference/config-ownership.md#oq-co10) | The layer STAYS. Ruling its binding, failure direction and coverage decided that it exists |
> | **[OQ-4](#decision-ledger)** / **[OQ-5](#decision-ledger)** — pure `rmw`; whole-file `stateful`+capture rejected, "capture buys nothing" | [`OQ-CO3`](../reference/config-ownership.md#oq-co3) | `own` renders `stateful` with a host capture store, which is what makes adoption byte-identical. **SHIPPED 2026-09-12**: the store is `<home>/.local/share/yolo-jail/host-capture/`, and [OQ-5](#decision-ledger) is its answer |
>
> Before opening a question, search sibling ledgers for a prior ruling on the same subject. A
> cross-document ledger collision is invisible by construction.

**The decisions this plan does NOT hold, and must not be read as holding:** whether capture may
keep outranking the definition (user-stories **Q1**, the unbuilt half of Phase 5.3), whether
`apply` must reconcile declared-count against rendered-count (**Q2** — no such guard exists),
whether a rendered briefing carries a notch stamp (**Q4**), and whether Linux `guest` stays a
promise (**Q7**). Those live in
[`../design/environment-manager-user-stories.md`](../design/environment-manager-user-stories.md)
as [Q1](../design/environment-manager-user-stories.md#Q1),
[Q2](../design/environment-manager-user-stories.md#Q2),
[Q4](../design/environment-manager-user-stories.md#Q4) and
[Q7](../design/environment-manager-user-stories.md#Q7).

---

## Decision Ledger

The `EMP-D<n>` ids are this plan's own, coined 2026-10-04 for the decisions Phase 7.1 needed
("EMP" for environment-manager plan, "D" for decision). The plan's `next` had asked for the
first two as a proposal with a leaning; each was taken on that leaning.

| ID | Decision | Date | Status |
| :--- | :--- | :--- | :--- |
| OQ-1 | **No `--revert` verb on the host target.** Undo is "stop declaring the key and re-apply", which drops it with a notice (regenerate, don't reconcile). ⚠ **REVERSED 2026-09-11** (table above): `yolo host apply --revert` shipped 2026-09-12, consuming the per-key host provenance record that did not exist when this was ruled; the host-render doc keeps its `--revert` design | 2026-08-01; reversed 2026-09-11 | ⚠ reversed; `--revert` built |
| OQ-2 | **Host management is user-scoped; the workspace contributes nothing.** What `yolo host apply` asserts is a function of the user config and the packs the user installed, never of the repo it ran from ([`host-render-target.md` §6.6](../design/host-render-target.md#66-a-host-target-is-user-scoped-not-workspace-scoped)). So two workspaces cannot collide, `${workspace}` surfaces have no referent on the host, and any host capture overlay is user- or machine-scoped, keyed by target file | 2026-08-01 | ✅ the host render takes no workspace input and prunes `${workspace}` keys (`internal/entrypoint/hostoverlayprune.go`) |
| OQ-3 | **Retire the `reads-host` read-*in* layer; personal settings become a local pack.** ⚠ **REVERSED 2026-09-11** by [`OQ-CO10`](../reference/config-ownership.md#oq-co10): the layer stays, declared per surface and failing closed. Never implemented before the reversal | 2026-08-01; reversed 2026-09-11 | ⚠ reversed; the layer stays |
| OQ-4 | **On the host notch, pure `rmw`:** yolo rewrites only the keys it declares (its `managed` block and dynamic tables), fills `defaults` where a key is absent, and touches nothing else, so a key yolo owns is overwritten on the next apply and a key the agent owns is never touched. ⚠ **REVERSED 2026-09-11** by [`OQ-CO3`](../reference/config-ownership.md#oq-co3): `own` composes the whole file with a host capture store | 2026-08-01; reversed 2026-09-11 | ⚠ reversed; the reversal built 2026-09-12 |
| OQ-5 | **Where a host capture overlay lives:** moot under [OQ-4](#decision-ledger)'s pure `rmw`. Revived by the [OQ-4](#decision-ledger) reversal, and answered by the store `<home>/.local/share/yolo-jail/host-capture/` | 2026-08-01 | ✅ the host capture store |
| OQ-6 | **The curl-to-shell install confirm shows the resolved install URL only**, not the fetched script or a hash: the confirm approves running *this command*, it is not a code review of the payload | 2026-08-01 | ✅ the `--assert` dependency gate prints each exact install command above its one prompt (`internal/cli/applyhostdepgate.go`) |
| OQ-7 | **The elevation line per remedy:** category (a) writes only under the user's own tree (user `brew`, `pip --user`, `~`); category (b) is anything else (a system `apt install`). Used only to batch confirmations by elevation class ([OQ-9](#decision-ledger)) | 2026-08-01 | — no consumer; the batching is not owed since 2026-09-30 ([`OQ-EM1`](../reference/what-yolo-is.md#why-its-this-way)) |
| OQ-8 | **The dep-checker boundary is a declared schema** a third-party doctor can read, evolvable into a Go helper if a spec proves too weak ([dependency provisioning](../reference/what-yolo-is.md#dependency-provisioning)) | 2026-08-01 | ✅ `install_hints` + `internal/depcheck/` |
| OQ-9 | **Offer-to-run confirms are batched by elevation class**: one approval per class, `sudo` first so its password prompt comes once at the front; declining a batch leaves the manifest written but not run. ⚠ **Half-reversed 2026-09-12** by [`report-tiers.md`](../reference/report-tiers.md#why-its-this-way) for `yolo host apply --assert`: one prompt, and a decline is FATAL | 2026-08-01; half-reversed 2026-09-12 | ⚠ one unbatched prompt (`internal/cli/applyhostdepgate.go`); the batching is not owed ([`OQ-EM1`](../reference/what-yolo-is.md#why-its-this-way)) |
| OQ-10 | **The composable-primitive model shape** (separate user, Seatbelt, bwrap, namespace as independent knobs), decided at Phase 2 so the three notches are presets rather than monoliths ([why the middle notch is `guest`](../reference/what-yolo-is.md#why-guest-not-sandbox)). Answered in the build | 2026-08-01 | ✅ `internal/render/confinement.go`; `describe` prints it (`printConfinementVector`) |
| OQ-11 | **A pack encodes its autonomy postures as a dedicated `autonomy` kind** (Encoding A of [§9.0](#90-the-sketch-that-resolved-oq-11-two-encodings-vs-the-real-packs)). Delegated by the maintainer (*"do the sketch now, I'm not sure I care"*); the discriminator-field encoding would split the `claude` settings surface into a conditional and an always-on half, which is how a jail-bypass key gets left in the unconditional half by accident | 2026-08-01 | ✅ Phase 9 |
| <a id="EMP-D1"></a>EMP-D1 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **Which backend a `confinement: guest` launch runs.** On macOS the guest notch selects the macos-user runtime with no `runtime` key: `config.NotchRuntime` returns it, and every runtime reader weighs it after YOLO_RUNTIME and the `runtime` key and before the platform probe (`config.SelectedRuntime`: the launch's `resolveRuntime`, `yolo check`, `yolo describe`, `yolo ps`/`prune`). An explicit YOLO_RUNTIME or `runtime` naming podman or Apple Container together with guest is REFUSED as a contradiction, naming both inputs and how to keep either (`config.NotchRuntimeConflict`; the launch's notch gate, and a `yolo check` [FAIL]), rather than letting one input silently win. `runtime: "macos-user"` at the jail notch keeps working, and `runtime: "macos-user"` with guest is one launch said twice. `--at guest` is the same launch, and the briefing names the guest notch for it too. | 2026-10-04 | ✅ built, unmeasured on a Mac |
| <a id="EMP-D2"></a>EMP-D2 | *Implementation decision, taken under the same delegation; reversible.* **The guest notch's mode policy on macOS.** The macOS guest renders exactly what macos-user renders at `confinement: jail`: the bootstrap's `render.Jail` target and its census (`(*Env).renderTarget`, `internal/entrypoint/env.go`), so the render fingerprint is unchanged. No `KindGuest` Target is built for it: `HostFields` and `UndecidedModes` would cut the surfaces the backend renders today. The `KindGuest` row in `internal/render/modes.go` stays undecided and is now the Linux guest's alone (Phase 7.2), its reason saying so. The launch-flag posture follows (`jailLaunchAutonomy`, autonomy on), as does the profile (`render.GuestProfileMacOS`). | 2026-10-04 | ✅ built |
| <a id="EMP-D3"></a>EMP-D3 | *Implementation decision, taken under the same delegation; reversible.* **A Linux guest keeps refusing**, at the launch and at `yolo apply --at guest`, with `render.NotchUnbuilt`'s one sentence reworded to say the notch launches only on macOS (as the macos-user backend) and that its Linux backend is unwritten. Each caller names its own next step: the default jail, or `yolo host -- <cmd>` for no sandbox. Where the config itself says guest, the jail step is the config edit or `--at jail`, since a bare `yolo -- <cmd>` reads that config and refuses too; `yolo config --at guest` names `--at jail` and `--at host`. | 2026-10-04 | ✅ built |
| <a id="EMP-D4"></a>EMP-D4 | *Implementation decision, taken under the same delegation; reversible.* **A guest session is told its notch.** The macOS guest launch sets `YOLO_CONFINEMENT=guest` (`config.NotchEnv`) in the launch env the backend writes into the session env file, beside the `YOLO_VERSION` every launch sets; a jail-notch launch sets nothing, and unset reads as the jail. The agent footer prints `guest` for it, as [agent-footer.md](../reference/agent-footer.md#the-notch) requires of this phase's launcher, and only inside a launched session (`config.InJail`), so a stray variable can never claim a sandbox. Chosen over leaving the footer at `jail` under [OQ-FT13](../reference/agent-footer.md#OQ-FT13), which was ruled while macos-user ran only at the jail notch, because [OQ-FT4](../reference/agent-footer.md#OQ-FT4) is the maintainer's: *"I do want to know if I'm in a jail/guest or on the host"*. The backend's own messages read the same marker (EMP-D5). | 2026-10-04 | ✅ built, unmeasured on a Mac |
| <a id="EMP-D5"></a>EMP-D5 | *Implementation decision, taken under the same delegation; reversible.* **At the guest notch, a next step that names a container runtime names the jail notch too.** EMP-D1 refuses a container runtime beside a macOS guest, so the macos-user backend's "use a container runtime" steps (context mounts it cannot deliver, forks, patched extensions, directory `host_files`, `ephemeral_storage`, jail daemons the sandbox declines, the nix materialization failures, the per-side disclosure) would each walk into that refusal. One clause, `config.ContainerStepClause`, follows each of them at a guest: *"the guest notch runs only on macos-user, so a container runtime also needs the jail notch (`--at jail`, or `confinement` set to "jail")"*; the jail notch's wording is unchanged. The run pipeline reads the notch it recorded at the notch gate; the backend reads EMP-D4's marker. Chosen over one guest-only preamble line, which every guest launch would print whether or not a step followed. The contradiction refusal's own step for keeping the runtime from `--at guest` is `--at jail` in place of the flag, never "drop the flag", which falls back to a config that may say guest too. | 2026-10-04 | ✅ built |

---

## What this plan explicitly does not do

- **It absorbs BACKLOG Stage G rather than pointing at it.** Phase 0 = G1/G2, Phase 1 =
  G4/G5/G3, Phase 4 = G6, folded in with their full evidence and build order; BACKLOG's
  Stage G is now just a G-number→phase map, so each item lives once (here). The rest of
  BACKLOG (Stages A–F, E) stays the tracker for the shipped/parked composed-config work.
- **It does not change the pack format.** Every phase renders through the shipped
  `contributes[]` substrate; `provides`/`install_hints` (Phase 6) is the one additive
  field, and it is additive.
- **It does not touch the happy path.** `yolo -- claude` in a fresh repo stays one
  command, jail, credentials-omitted, at every phase ([what does not change](../reference/what-yolo-is.md#what-does-not-change)). A phase that costs
  the happy path anything is wrong.
