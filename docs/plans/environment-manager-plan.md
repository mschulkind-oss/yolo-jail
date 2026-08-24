# Environment-manager implementation plan

**Status:** plan, written 2026-07-31; **build status re-verified against the tree 2026-08-23.**
Sequences [`../design/yolo-as-environment-manager.md`](../design/yolo-as-environment-manager.md)
(the vision — finalized, the maintainer is happy with it) into buildable phases.

> **Build status — verified against the tree 2026-08-23.** Read this table, not the 2026-08-01
> summary it replaces: that summary said "Phases 0–6, 8, 9 SHIPPED" flat, and three of those are
> partial in ways that misdirect real work. Each phase heading below repeats its verdict.
>
> | Phase | Verdict, 2026-08-23 | The residue |
> |---|---|---|
> | **0** — data-loss fix (G1/G2) | ✅ **SHIPPED** `1220ac55` 2026-08-01, hardened `2b317dba` 2026-08-02 | none |
> | **1** — one renderer (`internal/render`) | ✅ **SHIPPED**, but **1.4 landed 2026-08-12, not 08-01** (`a39628ad`) | macOS staging UNVERIFIED on real hardware |
> | **2** — the `confinement` key | ✅ **SHIPPED** (`internal/config/confinement.go`, `internal/render/confinement.go`) | none |
> | **3** — `apply` + `describe` | ✅ **SHIPPED** (`internal/cli/apply.go`, `describe.go`) | none |
> | **4** — `apply --host` | ⚠️ **PARTIAL** — 4.1/4.2/4.4 shipped; **4.3 (confirm-gated install) NOT built** | `internal/cli/applyhostdeps.go:113-116` prints a static "Phase 4.3" note instead |
> | **5** — `--sealed` + closure | ⚠️ **PARTIAL** — 5.1/5.2/5.4 shipped; **5.3 (capture as staging area, `yolo config promote`) NOT built** | the refusal at `apply.go:635` uses the word "promote" as English prose (the only command it names is `yolo config reset`, which exists) — but the verb the LEANING wants still does not |
> | **6** — dep provisioning | ⚠️ **PARTIAL** — 6.1/6.2/6.3 shipped (`internal/depcheck/`, `yolo check-deps`); **6.4 (offer-to-run) NOT built** | `internal/cli/checkdeps.go:9-12` defers it by name |
> | **7** — the `guest` notch | ❌ **NOT BUILT** — as previously stated | see below |
> | **8** — self-describing briefing | ⚠️ **PARTIAL** — 8.1 shipped (`internal/jailcontent/briefing.go:86-170`); **8.2 is MOOT** | the `jail-startup` built-in no longer exists |
> | **9** — agent autonomy | ✅ **SHIPPED** 2026-08-01 | none |
>
> **Phase 7 (the `guest` backend — macOS Seatbelt + Linux bwrap/Landlock) is NOT built**: it
> needs host capabilities a nested Linux jail cannot exercise, so it is host/Mac-gated and
> deferred. Two things sharpened since 2026-08-01. **7.1's precondition landed**: the
> zero-surfaces bug (1.4/G3) was fixed 2026-08-12, so macOS `guest` stages packs — though
> `docs/design/macos-user-nix-and-features.md:174` records the row as ⚠️ *UNVERIFIED on a Mac*.
> **7.2 has no code at all**: `bwrap`/`Landlock` appear in Go only as a profile constant
> (`internal/render/confinement.go:132-136`) and a label; and the notch's render policy is
> explicitly unstated — `KindGuest: UndecidedModes("… Phase 7's to state")`
> (`internal/render/modes.go:185`). The three-notch **vocabulary** shipped ahead of the notch,
> which is what user-stories Q7 now asks about.
>
> What shipped is behavior-neutral for the default `jail` notch — `yolo -- claude` is unchanged.
>
> **The `apply --host` bypass-leak is FIXED (Phase 9, 2026-08-01):** the shipped agent
> packs' jail-bypass keys live in the `autonomy` kind's `autonomous` posture, rendered only
> at the contained notches; `apply --host` renders the `guarded` posture (permission prompts
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
([`../design/pack-system.md`](../design/pack-system.md)).

**Reads with:** the design doc (the spec); [`BACKLOG.md`](BACKLOG.md) Stage G +
[`../design/host-render-target.md`](../design/host-render-target.md) (Phase 1's
detailed design); [`../design/pack-system.md`](../design/pack-system.md) (the pack
substrate); [`happy-path-principle.md`](../design/happy-path-principle.md) (the
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
Phase 4  apply --host         (Stage G6, §4.1)   ← the host notch becomes real
Phase 5  --sealed + closure   (§3.3)             ← the reproducibility guarantee
Phase 6  dep provisioning     (§3.4, §3.5)       ← check-at-notch + the manifest surface
Phase 7  guest notch          (§4, §4.0)         ← the middle notch actually works
Phase 8  self-describing      (§6)               ← the briefing states the notch
Phase 9  agent autonomy       (§4.2)             ← bypass is a notch policy, not baked config
```

Phases 0 and 1 are already scoped in BACKLOG Stage G. Phases 2–9 are this plan's
addition, and each is gated by the decisions in its "Before you start" note. Phase 9 was
added 2026-08-01 (the `apply --host` bypass-leak); it depends on 1/2/4 but not on 7.

---

## Phase 0 — Stop the destructive host-side write  *(was BACKLOG G1 + G2)*  ✅ **SHIPPED 2026-08-01**

**Design/reasoning:** `host-render-target.md` §6.1 (the probes), §8 step 1.
**Depends on:** nothing. **Ship regardless of whether the rest happens.**

> [!NOTE]
> **Verified 2026-08-23.** Commit `1220ac55`, hardened by `2b317dba` (2026-08-02). The predicate
> the items below prescribe is now a shared guard, `refuseHostSideWrite`
> (`internal/cli/configdiff.go:84-93`), wired at **both** verbs before any enumeration:
> `:645` (`configReset`, closing G1) and `:848` (`configCapture`, closing G2). Item 0.3's
> regression test exists: `internal/cli/configdiff_test.go:172`
> `TestResetCaptureRefuseHostSideWithoutForce`. **The line numbers below have drifted** —
> `truncateSurfaceToPureRender` is at `configdiff.go:804-827` (was `:381`), `surfacesAreLocal()`
> at `internal/cli/configls.go:385-393` (was `:341`), `composedFileExists` at `:370` (was `:330`).
> `truncateSurfaceToPureRender` still resolves `~` through `expandHome`; it is simply unreachable
> host-side, its only caller sitting at `:684` downstream of the guard. `--force` is the escape
> hatch, as specified.

Host-side `yolo config` verbs already resolve `~` against the invoking human's *real*
home (`expandHome` → `paths.Home()`) and two of them write — a destructive posture
yolo is already in by accident. This is the one live data-loss bug in the whole
cluster, it is independent of the architecture below it, and it is ~20 lines.

- **0.1 — `config reset` destroys real user config. *(G1, ⚠ data-loss)*** The bug:
  `truncateSurfaceToPureRender` (`cli/configdiff.go:381`) composes with **no computed
  layer** against the real `$HOME`. Probed:
  - `reset mise` truncated a real `~/.config/mise/config.toml` (20 bytes → `"\n"`, the
    user's `[tools]` gone) — its content is *entirely* the computed layer.
  - `reset codex`/`opencode` replaced real files with yolo's managed keys only.
  - `reset claude` merged yolo's managed layer into the user's own file.

  **Fix:** refuse (or require `--force`) in `configReset` (`configdiff.go`) when
  `surfacesAreLocal()` (`configls.go:341`) is false — the predicate already exists and
  is currently consulted only by `composedFileExists` (`:330`). `configCapture`'s own
  docstring (`:415-419`) already says a host-side re-render is wrong for exactly this
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

**Design/reasoning:** `host-render-target.md` §3 (the load-bearing section), §4, §7.1.
**Depends on:** Phase 0 (so the refactor lands on non-destructive host paths).

> [!NOTE]
> **Verified 2026-08-23.** `internal/render/` exists with `target.go`, `fieldset.go`, `modes.go`,
> `confinement.go`. `FieldSet.Refuse` (1.3) names the kind and gives the census's reason
> (`internal/render/fieldset.go:23-63`) — including a hand-written inverse reason for `loophole`,
> because the generic line reads as obviously wrong for a host daemon.
>
> **Correction to the 2026-08-01 status: 1.4 was NOT shipped then.** G3 was fixed six weeks after
> the plan declared Phase 1 done, by `a39628ad` (2026-08-12, *"fix(run): stage packs BEFORE the
> backend dispatch — macos-user rendered none"*). That matters because 1.4 is described below as
> "the cheapest test of 1.1–1.3" and the proof that `Target` can express a real home — so for six
> weeks Phase 4 was building on an abstraction whose stated proof had not run.

This is the foundation — every phase below needs it. Today `agentcfg.Compose` /
`ComposeStateful` have **two independent callers**: the in-jail boot render
(`entrypoint/prism.go:167,322`) and the host-side `config` verbs (`cli/config.go:253`,
`cli/configdiff.go:394,476`). The code already admits the second is a hand-copy of the
first, in three comments — `prism.go:61` ("Mirrors `internal/cli.loadTransformScript`"),
`prism.go:351-353` ("Mirrors `internal/cli.expandHome` … keyed on the Env rather than the
process `$HOME`"), and the `cli/config.go:3-4` header promising it runs "the SAME engine."
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
  (`configls.go:197,204`), become *derived from `Target`* — `config render` stops being an
  approximation of the boot render and *is* the boot render against a temp home.
- **1.3 — `FieldSet`. *(G5)*** A target declares which kinds apply; an inapplicable kind
  gets a refusal **naming the kind**, never a silent skip. The census
  (`host-render-target.md` §2.1): only `config` is target-independent; `program` must be
  refused, `mount`/`reads-host` are unavailable off-container and must be refused rather
  than emulated (a copy goes silently stale). G3 below is what a silent skip looks like
  after a year in production.
- **1.4 — fix macos-user as a target row. *(G3, and the cheapest test of 1.1–1.3)***
  macos-user renders **zero pack surfaces every launch, silently**: `RunDarwinBootstrap`
  calls `LoadJailPacks`/`ConfigurePackSurfaces`/`RunPackHooks` (`entrypoint/darwin.go:57-62`),
  but the run path returns at `cli/run/run.go:73` *before* `stagePacks`, so
  `YOLO_PACK_ROOT` is never set and the loop runs over an empty list.
  (`macos-user-nix-and-features.md:174` still wrongly claims selection works.) This is an
  existing backend that *should* render into a real home and renders none — **if `Target`
  cannot express macos-user cleanly it will not express `host` either**, so fixing it here
  proves the abstraction before Phase 4 bets on it.

  > [!NOTE]
  > **1.4 DONE 2026-08-12 (`a39628ad`), verified 2026-08-23.** Staging moved above the backend
  > dispatch — `o.stageRunPacks(cname)` at `internal/cli/run/run.go:103`, macos-user branch at
  > `:112` returning at `:155-156` with `staged.root` passed through. `YOLO_PACK_ROOT` is set at
  > `internal/macosuser/runplan.go:208-211` and deliberately left unset when nothing staged, so
  > "no packs" is stated by absence. Three plan invariants at `runplan.go:302-320` refuse the
  > silent shapes. Pinned by `internal/cli/run/packstagedispatch_test.go:87` (the handler receives
  > a pack root that exists on disk) and `:131`, plus `internal/macosuser/packroot_test.go`. The
  > cited `run.go:73` is now a container-only repo-root gate macos-user skips;
  > `entrypoint/darwin.go:57-62` is now `:59-62`. **The doc line is also corrected**:
  > `macos-user-nix-and-features.md:174` reads ⚠️ *"Wired 2026-08-12 (B-0); UNVERIFIED on a Mac"*
  > (commit `2bb792ff`), with a retained blockquote at `:178-195` recording that the old ✅ row
  > had never been true. **Residue:** no Mac has exercised the `sudo -u _yolojail` staging step or
  > the sandbox-uid read — a verification gap, not the defect.

**The one hard risk (`host-render-target.md` §3.5):** this refactors the **A12-fatal boot
path** — a regression does not misconfigure an agent, it stops jails from *starting*,
including the one you are reading this in. **Retire it with a byte-equality check of every
shipped pack's rendered surfaces, before and after** — the same method Stage D used to
prove the ten Go surface literals equalled their generated pack declarations. Two
constraints: `entrypoint` must **not** gain a `cli` dependency (the edge runs
`cli`→`entrypoint` today, and `internal/render` sits below both); and `liveTables` +
`genStep`'s A12 fail-closed policy stay in the *caller*, not the renderer — that split is
what lets a host target's refusal be a message while the jail stays loud-and-halting.

**Extraction is settled: no** (`host-render-target.md` §2.3, decided 2026-07-27). This
lives in yolo as `internal/render`, not a separate util — the field census puts the
boundary through the middle of a single manifest, and G4's value is in the *deletion* of
the duplicate renderer, host target or not.

**Done when:** boot render, `config render`, and (via macos-user, 1.4) a real-home render
all go through one `render.Render(target, surfaces)`; inapplicable kinds refuse by name;
and the byte-equality gate is green.

---

## Phase 2 — Name the dial: the `confinement` key  ✅ **SHIPPED 2026-08-01**

> [!NOTE]
> **Verified 2026-08-23.** `internal/config/confinement.go` — `ResolveConfinement` (`:40`),
> `validateConfinement` (`:61-79`), and the load-bearing fallback pinned at
> `internal/config/validate_test.go:468-481`: **an unknown value resolves to `jail`, never
> `host`.** 2.2's primitive layer is `internal/render/confinement.go` (`Primitive`, the presets
> `JailProfile`/`GuestProfileMacOS`/`GuestProfileLinux`/`HostProfile`, and `AgentAutonomy` as a
> policy knob beside them). 2.3's mechanism-beside-notch printing is
> `internal/cli/describe.go:145-149`.

**Design:** design doc §4, §4.0.
**Depends on:** Phase 1 (so a notch is a `Target`, not a code branch).
**Before you start — decide:** OQ-10 (the composable-primitive model shape) — but it is a
"decide at this phase" item, not a blocker you owe an answer up front. Phase 2 ships the
*three named presets*; it must not foreclose the primitive layer underneath.

- **2.1** Add `confinement: jail|guest|host` (default `jail`), and demote `runtime` to a
  mechanism hint inside `jail` (`podman`/`container`/`auto`). `runtime: "macos-user"`
  maps to `confinement: guest` + its mechanism. Keep `runtime` accepted (deprecation, not
  removal) so no config breaks.
- **2.2** Internally, model confinement as a **composable primitive set** (separate user
  / Seatbelt / bwrap+Landlock / namespace) that the three notches are presets over — so a
  future fourth combination (Linux `guest`, seatbelt-without-user) is not a bolted-on
  special case. Only the three presets are user-selectable (`happy-path-principle.md`);
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
> (`internal/cli/dispatch.go:22-23`) and the help index (`internal/cli/help.go:20-21`).
> `describe --json` is the canonical config (superseding `config dump`) and `--hash` prints
> **marked unsealed** as 3.2's caveat required — `internal/cli/describe.go:105`, pinned by
> `internal/cli/describe_test.go:38-44`. `--at` parses at `internal/cli/apply.go:54` and rejects
> an unknown notch with rc 2 (`describe_test.go:256`). The earlier status note listed "the no-exec
> jail provision" as a deferred within-phase increment; `apply` provisions without launching
> today.

**Design:** design doc §3, §3.1, §3.2.
**Depends on:** Phase 1 (apply renders through the one renderer), Phase 2 (a notch to
apply at). Phase 3 does jail level only; the host notch is Phase 4.

- **3.1** `yolo apply`: split provision from launch. At `jail`, builds image + stages
  packs + renders config and exits. `yolo -- <cmd>` becomes "apply, then exec" — same
  behavior, now with a name. The manual-apply cases (set-up-don't-launch, CI provision
  step) are the §3.1 enumeration.
- **3.2** `yolo describe`: print the resolved description; **absorb the shipped `config
  dump`** (canonical form) as `describe --json`. Add the human table (§3.2) and
  `describe --hash`. **`--hash` over an unsealed environment prints marked or refuses**
  (§3.2 caveat) — it is not authoritative until Phase 5's sealing exists, so gate the
  bare hash on `--sealed` or mark it.
- **3.3** `--at <level>` plumbing on `yolo`/`apply`, so a verb can target a notch other
  than the configured default (the mechanism the escape valve §4.1 uses).

**Done when:** `apply` provisions-without-launch at `jail`, `describe` prints the
description (and `--json` supersedes `config dump`), and `--at` selects a notch.

---

## Phase 4 — `apply --host`: the host notch becomes real  *(was BACKLOG G6)*  ⚠️ **PARTIAL — 4.1/4.2/4.4 shipped, 4.3 NOT built**

> [!WARNING]
> **Verified 2026-08-23 — the previous flat "SHIPPED" hid an unbuilt item.** 4.1/4.2 are real:
> `apply --host` at `internal/cli/apply.go:63`, `--assert` at `:47`, default observe/dry-run, pure
> `rmw` over the managed keys, inapplicable kinds refused by name via `FieldSet`
> (`internal/render/fieldset.go`). 4.4's host-dep report is `internal/cli/applyhostdeps.go`.
>
> **4.3 (confirm-gated install) is NOT built, and the shipped behaviour is the OPPOSITE
> position.** `FieldSet` refuses `program` outright below `jail` — *"install is refused below
> jail (a pack must not mutate a real toolchain unprompted)"*
> (`internal/render/fieldset.go:38`) — which is the design's **original** rule, not the revised
> confirm-gated one that §4.1 of the design doc and item 4.3 below both specify. `apply --host`
> prints a static pointer instead: *"apply --host reports host deps; it installs nothing. The
> confirm-gated install is env-manager plan Phase 4.3"*
> (`internal/cli/applyhostdeps.go:113-116`). So OQ-6/OQ-7's resolutions are recorded but
> unconsumed. This is the single largest gap between the design doc and the tree.

**Design/reasoning:** `host-render-target.md` §6 (the whole section), §6.5 postures,
§7.2; design doc §4.1.
**Depends on:** Phase 1 (`render.Host`), Phase 3 (`apply`), Phase 2 (`--at host`).
**Before you start — decide:** nothing outstanding. OQ-1..OQ-9 are all RESOLVED (OQ-5
moot); the host-render shape (pure `rmw`, user-scoped, no read-in layer, no capture) and
the confirm-gated-install detail (OQ-6/7) are settled and assumed below.

- **4.1** `render.Host(home)` renders the applicable kinds into the real `$HOME`;
  postures `observe` → `assert` → (maybe never) `own` (§6.5). Default `observe`
  (dry-run); `assert` **regenerates only the pack's `managed` keys** and leaves the
  user's own keys — the shipped "regenerate, don't reconcile" model applied to a real
  home. **No `--revert` verb** (OQ-1, resolved): dropping yolo's management is "stop
  declaring the key and re-apply," which removes it with a notice exactly as an unset MCP
  server is dropped in a jail today; there is no restore-to-pre-yolo-state, which would
  need a before-snapshot nothing takes. **User-scoped, workspace contributes nothing**
  (OQ-2, resolved).
- **4.2** Every host config surface is **pure `rmw`** (OQ-4, resolved): `apply` rewrites
  only yolo's own declared keys (`managed` + dynamic tables), fills absent `defaults`, and
  leaves every key the agent wrote untouched — no whole-file compose, no capture overlay.
  A yolo-managed key the agent edits is overwritten on the next `apply` (yolo owns it,
  OQ-1). `${workspace}`-using surfaces are refused (no referent).
- **4.3** `program` (install) below `jail` is **confirm-gated, not refused** (§4.1, the
  reviewed position): TTY-only, permission-bounded. Per OQ-6 the confirm shows the resolved
  **URL only** (not the fetched script); per OQ-7/OQ-9 confirmations are **batched by
  elevation class** — one approval for all no-elevation remedies, one for all `sudo` ones
  (sudo first, so the OS password prompt comes up once at the front).
- **4.4** `check --at host` reports host-render drift and hands off to `apply --host`
  (§3.4) — the host-side twin of the shipped `config drift`. `check` never writes.

**Done when:** `yolo apply --host` regenerates a pack's `managed` config keys into the
real home (behind `observe`/`assert`) without clobbering the user's own keys, and
inapplicable kinds are refused by name.

---

## Phase 5 — `apply --sealed` and the input closure  ⚠️ **PARTIAL — 5.1/5.2/5.4 shipped, 5.3 NOT built**

> [!WARNING]
> **Verified 2026-08-23.** `applySealed` (`internal/cli/apply.go:617-651`) enumerates and refuses
> the Undeclared tier: `yolo-jail.local.jsonc` (`:623-630`) and any surface with outstanding
> overlay keys (`:631-638`). Tests at `internal/cli/describe_test.go:264-287`.
>
> **5.3 is NOT built and it is the item the rest of the phase leans on.** There is no `yolo config
> promote`: the `config` subverbs are `ls, render, diff, reset, capture, drift, dump`
> (`internal/cli/config.go:33-60`) and `promote` appears nowhere in the dispatch table. Capture is
> therefore **still a winning layer, not a staging area** — the refusal at `apply.go:635` tells
> the user to *"promote them into a pack"* — English prose, not a named command (the refusal names
> only `yolo config reset`, which exists) — so the remedy the leaning wants is unavailable, leaving `yolo
> config reset` (discard) as the only shipped remedy. Two facts worth carrying forward: the
> Declared-impure `host`-layer row is **reported by nothing** (`describe` never names a host
> layer), and the capture overlay does **not** outrank every declared layer — it loses to
> `computed`/`transform`/`managed` (`internal/agentcfg/compose.go:357-379`,
> `:65-77`), which is why it is a closure problem rather than a correctness one. **This is
> user-stories Q1's unbuilt half.**

**Design:** design doc §3.3 (the full-closure table + the sealing rule).
**Depends on:** Phase 3 (`apply`). Independent of the host notch — sealing is a
host-side check that needs no container (§3.3).
**Note:** OQ-3 (retire the host read-in layer) is RESOLVED yes, so the closure table's
`host`-layer row has moved from Declared-impure to Declared — one fewer impure input for
sealing to report.

- **5.1** Enumerate the closure at apply time against the §3.3 four-tier table
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

## Phase 6 — Dependency provisioning below `jail`  ⚠️ **PARTIAL — 6.1/6.2/6.3 shipped, 6.4 NOT built**

> [!NOTE]
> **Verified 2026-08-23.** 6.1: packs declare per-manager `install_hints`
> (`internal/depcheck/depcheck.go:48`, `internal/entrypoint/requires.go`). 6.2: the shared checker
> is `internal/depcheck/` with a standalone entry point `yolo check-deps`
> (`internal/cli/checkdeps.go`, dispatch at `internal/cli/dispatch.go:24`), reused rather than
> re-implemented by `apply --host` (`internal/cli/applyhostdeps.go:16`). The honesty rule holds:
> `applyhostdeps.go:183-191` distinguishes *"hints cover brew/dnf but not this host's manager"*
> from *"no hints at all"* rather than reporting either as present. 6.3: the manifest is written
> at `~/.config/yolo/Brewfile` and kin (`internal/cli/checkdeps.go:145-156`,
> `internal/depcheck/depcheck_test.go:54`).
>
> **6.4 (offer-to-run, batched by elevation class) is NOT built** and is deferred by name in the
> code: *"It NEVER installs anything… The offer-to-run (behind a batched, sudo-shown-through
> confirm, OQ-9) belongs to `apply` at a lower notch"* (`internal/cli/checkdeps.go:9-12`). Since
> `apply`'s half of that is Phase 4.3, also unbuilt, **OQ-9's resolution has no consumer at all
> today.** The manifest floor is shipped; only the offer on top of it is missing.

**Design:** design doc §3.4, §3.5.
**Depends on:** Phase 1 (`FieldSet`/notch-aware render), Phase 4 (host apply is where a
missing dep bites). At `jail` this is a near-formality (the toolchain is in the sealed
image); below `jail` it is the real work.
**Before you start — decide:** nothing outstanding — OQ-8 (checker boundary) and OQ-9
(confirm UX) are RESOLVED. §3.5 fixes the *shape*, not the schema.

- **6.1** Pack-authored `provides`/`install_hints` (per-system: `brew`/`apt`/`dnf`/`nix`),
  declared by the pack that *introduces* the dep (no re-declaring others' deps).
- **6.2** The shared **dep-checker**: probe "is this binary present, at what version,
  what installs it," used by `check`, by every `apply`, and standalone (`yolo
  check-deps`). Boundary (OQ-8, resolved): a **declared schema** a third-party doctor can
  read, yolo shipping a reference checker over it — evolvable to a Go helper later.
- **6.3** The manifest as a **composed surface** at a fixed path (`~/.config/yolo/Brewfile`
  + apt/dnf/pacman kin), composed from all packs' hints, regenerated wholesale every
  `apply`, yolo-owned. Not a one-off in a random dir.
- **6.4** Offer-to-run (OQ-9, resolved): **confirm everything, batched by elevation class**
  — one approval for all no-elevation remedies, one for all `sudo` ones, `sudo` first so its
  OS password prompt (shown through, never captured) comes up once at the front. Not
  per-command, not one blind confirm. Never ambient; no-TTY = print-only; the manifest is
  always the floor.

**Done when:** `check --at host`/`apply --at host` report missing deps, write the
manifest surface, and (behind a confirm) can run the remedies including `sudo`.

---

## Phase 7 — Make the `guest` notch actually work  ❌ **NOT BUILT**

> [!WARNING]
> **Verified 2026-08-23. 7.1's stated precondition landed; 7.2 has no code.** 7.1: the
> zero-surfaces bug it points at (1.4/G3) was fixed 2026-08-12, so macOS `guest` does stage
> packs — but `docs/design/macos-user-nix-and-features.md:174` records that row as ⚠️
> **UNVERIFIED on a Mac**, so "renders surfaces" is wired, not measured. 7.2: `bwrap` and
> `Landlock` exist in Go **only as names** — the profile constant `GuestProfileLinux()`
> (`internal/render/confinement.go:132-136`), a primitive label (`:69`), and a briefing test.
> There is no bwrap invocation anywhere. The notch's own render policy is explicitly unstated:
> `KindGuest: UndecidedModes("the guest notch's mode policy is Phase 7's to state")`
> (`internal/render/modes.go:185`) — a deliberate visible hole rather than an inherited default.
>
> **The ordering risk this phase now carries:** the three-notch *vocabulary* shipped in Phase 2
> ahead of the notch — `confinement: guest` validates, `apply --at guest` parses, `describe`
> prints it, and the briefing has a `guest` body (`internal/jailcontent/briefing.go:133`). That
> is exactly what the design doc §8 warned against (*"a three-notch story with a broken middle"*)
> and what user-stories **Q7** asks a ruling on.

**Design:** design doc §4, §4.0; `host-render-target.md` §9.7, §9.8.
**Depends on:** Phase 1 (guest is a `Target`), Phase 2 (the notch exists). This is where
"three-notch story with a broken middle" (§8) gets fixed.

- **7.1** macOS `guest`: the existing macos-user backend, but rendering surfaces (Phase
  1.4 already fixes the zero-surfaces bug) — separate user + Seatbelt as composed
  primitives (Phase 2.2).
- **7.2** Linux `guest`: bwrap + Landlock, a real home, no image — "a weaker container,
  no separate user." This is the missing fourth composition the primitive layer (2.2)
  was built to express; it needs no new concept.

**Done when:** `confinement: guest` renders a pack's full portable surface set into a
real, LSM-confined home on both platforms, and `describe` prints the composed primitives.

---

## Phase 8 — The environment describes itself to its agent  ⚠️ **PARTIAL — 8.1 shipped, 8.2 MOOT**

> [!NOTE]
> **Verified 2026-08-23.** 8.1 shipped: `confinementHeader`
> (`internal/jailcontent/briefing.go:86-170`, called at `:287`) emits a per-notch opening block
> with a dedicated `host` body at `:124` and `guest` body at `:133`, plus a derived
> `enforcementLines` tail at `:171`. The earlier status note listed "the per-notch body of the
> briefing" as deferred to the guest/host backends; it landed without them.
>
> **8.2 is MOOT: there is no `jail-startup` built-in skill to fix.** yolo's built-in suite is
> `configuring-the-jail`, `developing-yolo-jail`, `diagnosing-the-jail`
> (`internal/jailcontent/builtinskills/`). The startup-ritual skill (`n`) was deleted and the
> one-time handoff became a conditional **Handoff** section in the briefing
> (`internal/jailcontent/briefing.go:69`; see
> [`../design/host-to-jail-handoff.md`](../design/host-to-jail-handoff.md)).
>
> **What 8.2 was really for is still owed, and now has no home.** Nothing stamps a rendered
> briefing with the notch it was made for, and nothing asserts that stamp against observable
> reality at startup — so a `host` briefing left on disk and read inside a jail is unremarked.
> Deleting the skill removed the place the check was going to live. That is user-stories **Q4**.

**Design:** design doc §6.
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

**Design:** design doc §4.2 (the whole subsection). **Depends on:** Phase 1
(`render.Profile`/`Target`), Phase 2 (the notch), Phase 4 (`apply --host` is where the
defect bites). **Motivated by:** a host agent following the migration guide would
`apply --host --assert` the `claude` pack's jail-bypass keys (`acceptEdits`,
`skipDangerousModePermissionPrompt`, `additionalDirectories:["/"]`,
`--dangerously-skip-permissions`) onto the *real* machine, stripping the prompts that are
the only protection when there is no jail. Today those keys are unconditional pack config
with nothing marking them jail-only.

**OQ-11 — RESOLVED (2026-08-01): a dedicated `autonomy` contribution kind (Encoding A
below).** The maintainer delegated the choice ("do the sketch now, I'm not sure I care").
The sketch (§9.0) settles it: the discriminator-field encoding forces the `claude`
settings surface to be *split* into a conditional half and an always-on half, which is
exactly the way a jail-bypass key gets left in the unconditional part by accident. The
dedicated kind keeps each posture whole and keeps the always-safe keys in the ordinary
`config` kind, untouched.

### 9.0 The sketch that resolved OQ-11 (two encodings vs the real packs)

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

**Why A wins.** (1) *Safety by construction:* in B, a bypass key mistakenly left in the
unconditional `config` entry silently ships to `host`; in A the confinement-conditional keys
physically live in the `autonomy` kind and nowhere else, so "unconditional config" cannot
contain a bypass. (2) *Legibility:* `describe`/`pack footprint` print one `autonomy` block
("autonomous → these keys + flags; guarded → these") instead of reconstructing intent from
`when` tags scattered over N entries. (3) *Bidirectional reads cleanly:* `autonomous` vs
`guarded` are two named siblings; `pi`'s empty-autonomous / full-guarded shape is obvious.
(4) *Implementation reuse:* an `autonomy` posture's config half **selects which keys enter
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
  §4.0 enforcement primitives; it is a policy knob, not an enforcement one). Preset
  defaults: `jail` → on, `guest` → on, `host` → off. `describe` prints it. A composed
  custom confinement can set it explicitly (the §4.2 composability requirement).
- **9.2** Packs declare autonomy postures as data, **bidirectionally** (the subtle half):
  a pack states *both* the `autonomous` posture (config keys + launch flags meaning "no
  prompts") and the `guarded` posture (meaning "prompt"). Guarded-by-default agents
  (`claude`/`codex`/`agy`/`opencode`) get *loosened* by confinement; the permissive-by-
  default agent (`pi`) gets *tightened* at `host`. One selector, run in whichever
  direction the pack's default sits — NOT "always add a bypass."
- **9.3** At render: `profile.agentAutonomy ? pack.autonomous : pack.guarded`. Benign
  always-safe `managed` keys (auto-updater off, trust-dialog) are untouched by this — only
  the confinement-conditional keys move under the selector.
- **9.4** Migrate the shipped packs to the `autonomy` kind (§9.0 Encoding A): move each
  agent's confinement-conditional keys + `--dangerously-*` launch flag into an `autonomy`
  contribution's `autonomous` block, leave the benign always-safe keys in the ordinary
  `config` kind, and author a `guarded` block (prompts on, no allow/deny clobber) for each —
  including `pi`, whose block is guarded-only. **Invariant:** the `jail`-on path must render
  byte-identical to today — `renderfingerprint_test.go` stays green — so only the host/guest
  paths change behavior.

**Done when:** `apply --host` renders an agent with its permission prompts intact (no
jail-bypass keys, no `--dangerously-skip-permissions`), `pi` is tightened at `host`, a
jail boot is byte-identical to today, and `describe` names the autonomy policy in force.

---

## Open questions to resolve before their phase

**Each OQ below is ONE decision** — a single yes/no or a pick-from-two, with a leaning
and a "if no preference, I build X" fallback — so you can answer them one at a time
rather than untangle a bundle. They are grouped by the phase they block, and marked
**RESOLVED** once you have answered. A short **Context** line precedes each cluster; the
long reasoning lives in the cited design section, not here.

**Status (2026-08-01): OQ-1 through OQ-9 are all RESOLVED** (OQ-5 dropped as moot). OQ-10
is a "decide at Phase 2" internal-representation choice that needs no answer up front.
**OQ-11 (2026-08-01) is RESOLVED** — the Phase 9 pack-encoding choice is the dedicated
`autonomy` kind (Encoding A; the sketch is in §9.0). Nothing is outstanding to decide before
implementing any phase.

> [!NOTE]
> **Consumption check, 2026-08-23 — resolved is not the same as built.** Four of these rulings
> are recorded and have **no code consuming them**, which is worth knowing before treating this
> section as done. **OQ-3** (retire the read-in `host` layer): not implemented — `HostSource`
> still exists (`internal/agentcfg/manifest/manifest.go:142`) and the `host` layer still composes
> (`internal/agentcfg/compose.go:357-379`). **OQ-6 / OQ-7** (what the install confirm shows, the
> elevation-class line): Phase 4.3 is unbuilt, and the shipped behaviour is a flat refusal
> (`internal/render/fieldset.go:38`), so there is no confirm to configure. **OQ-9** (batched
> offer-to-run): both its callers — Phase 4.3 and Phase 6.4 — are unbuilt. **OQ-10** was answered
> in the build rather than in this doc: the primitive model is `internal/render/confinement.go`
> and `describe` prints it (`internal/cli/describe.go:145-149`).
>
> **The decisions this plan does NOT hold, and must not be read as holding:** whether capture may
> keep outranking the definition (user-stories **Q1**, the unbuilt half of Phase 5.3), whether
> `apply` must reconcile declared-count against rendered-count (**Q2** — no such guard exists),
> whether a rendered briefing carries a notch stamp (**Q4**), and whether Linux `guest` stays a
> promise (**Q7**). Those live in
> [`../design/environment-manager-user-stories.md`](../design/environment-manager-user-stories.md)
> and are cited from `roadmap.md` 💬 7.

### Resolved

- **OQ-1 — Is there a `--revert` verb on the host target? → RESOLVED: NO (2026-08-01).**
  Undo is "stop declaring the key and re-apply," which drops it with a notice, the shipped
  "regenerate, don't reconcile" model (`prism.go:397-436`, OQ12(d)). A `--revert` to a
  pre-yolo state would need a before-snapshot nothing takes. *Consequence:* no revert verb,
  no per-file reconcile sidecar; the host-render doc's `--revert` design (§6.5/§7.2/§9.5) is
  **superseded** — strike it there when that doc is next touched.
- **OQ-2 — Is host management user-scoped, with the workspace contributing nothing? →
  RESOLVED: YES (2026-08-01).** What `apply --host` asserts is a function of your *user*
  config + the packs *you* installed, never of the repo you ran it from — the same
  user-scope rule packs already enforce (`pack-system.md` §8), written up as
  `host-render-target.md` §6.6. *Consequence:* the "two workspaces collide" question is
  void (one description, one owner); `${workspace}` surfaces are refused on host; any host
  capture overlay is user/machine-scoped, keyed by target file, never by workspace.

### Blocks Phase 4 (host render)

**Context — how `apply --host` touches a file the agent also writes.** Two calls, both now
resolved; the reviewer's push on OQ-4 corrected an over-complication I had introduced.

- **OQ-3 — Retire the `reads-host` read-*in* layer? → RESOLVED: YES (2026-08-01).** Drop
  settings-inheritance (yolo reading your real `~/.claude/settings.json` *into* a jail as a
  compose layer); express personal settings as a **local pack** instead — declared, locked,
  portable to every notch. This collapses a Declared-impure closure row into Declared
  (simpler Phase 5) and removes the read-in/write-out XOR. Design doc §3.3. *Consequence:*
  the `host` compose layer and the `reads-host` kind's compose role go away; credentials are
  unaffected (they cross as mounts, not a layer).

- **OQ-4 — On the host notch, `rmw` (surgical) or whole-file compose? → RESOLVED: pure
  `rmw` (2026-08-01).** The reviewer is right that **overwrite is the only workable option
  for a key yolo manages**, and once you see why, `rmw` is not just workable — it is the
  *simpler* and *complete* answer, so my earlier "keep capture" lean was wrong. The two
  models differ only in blast radius:

  - **`rmw` (the answer):** yolo rewrites **only the keys it declares** — its `managed`
    block and dynamic tables (e.g. `mcpServers`) — fills `defaults` where a key is absent,
    and **touches nothing else**. Every key the agent wrote (`theme`, a `/config` toggle,
    anything not in yolo's declaration) is left byte-for-byte, because yolo never reads or
    rewrites it. This is exactly the shipped `~/.claude.json` behavior (`prism.go`
    `regenerateManagedTables` + `applyRMWLayer`), just pointed at a real home.
  - **whole-file `stateful`+capture (rejected):** yolo composes the *entire* file from its
    layer stack and writes all of it — which would blow away the agent's own keys, so it
    would need a **capture** sidecar to first snapshot the agent's edits and re-inject them
    as a layer. That machinery exists in a jail *only because* a jail whole-file-composes
    some surfaces; on the host there is no reason to whole-file-compose (esp. with OQ-3
    retiring the read-in layer), so **capture buys nothing** — it is solving a problem `rmw`
    does not have.

  So the answer to your question — "what happens to a pack-managed key the agent then
  edits?" — is: **yolo overwrites it on the next `apply`**, deliberately, because yolo owns
  that key (regenerate-don't-reconcile, OQ-1). A key the agent owns is never touched.
  Capture was never about protecting a *managed* key (nothing can — overwrite is correct
  there); it was only about surviving a *whole-file* rewrite, which `rmw` doesn't do.
  `host-render-target.md` §6.3 already concluded pure `rmw`; only its *justification* ("same
  person") was loose — the real reason is "`rmw` only ever rewrites yolo's own keys, so the
  agent's are safe without capture." I will tighten §6.3 to say that.

- **~~OQ-5 — where does a host capture overlay live?~~ → MOOT.** It only existed if OQ-4
  chose capture. With pure `rmw` there is no host capture overlay, so there is no new
  storage location to decide. (Removed from the count.)

### Blocks Phase 4.3 (confirm-gated install)

**Context — install below `jail` is confirm-gated (policy decided, §4.1); two threat-model
details are open.** I will draft a short note for your sign-off rather than improvise at
the call site.

- **OQ-6 — What does the curl-to-shell install confirm display? → RESOLVED: URL only
  (2026-08-01).** Show the resolved install URL; do not fetch-and-display the script or a
  hash. (Simplest, and consistent with the confirm being "approve running *this command*,"
  not a code review of the payload.)
- **OQ-7 — Where is the category-(a) *no-elevation* / category-(b) *needs-`sudo`* line drawn
  per remedy? → RESOLVED (2026-08-01).** (a) = writes only under the user's own tree (user
  `brew`, `pip --user`, `~`); (b) = anything else (a system `apt install`, anything outside
  the user's tree). The split is now *only* used to **batch confirmations by elevation
  class** — see OQ-9, which the reviewer answered together with this.

### Blocks Phase 6 (dep provisioning)

- **OQ-8 — Dep-checker boundary: a declared schema, or an importable Go package? → RESOLVED:
  schema, evolvable (2026-08-01).** Start with a declared schema a third-party doctor can
  read; this can grow a Go helper later if a spec proves too weak. No lock-in either way.
  Design doc §3.5.
- **OQ-9 — Offer-to-run confirm UX → RESOLVED: batch by elevation class, minimize
  interaction (2026-08-01).** Not per-command (my earlier split was too interactive) and not
  one blind confirm. **Confirm everything, batched:** group the remedies by elevation class
  (OQ-7's a/b line) and ask **once per class** — show all category-(a) commands and confirm
  them, show all category-(b)/`sudo` commands and confirm them — so there are two approvals,
  not N. **Order `sudo` first** where possible, so the single `sudo` password prompt (shown
  through, OS-native, §3.5) comes up once at the front rather than interleaved. The manifest
  is still the floor — decline either batch and it is only written, not run. Design doc §3.5.

### Decide at its phase, not up front

- **OQ-10 — The composable-primitive model shape (Phase 2).** How confinement is represented
  internally (separate user / Seatbelt / bwrap / namespace as independent knobs) so a fourth
  combination is expressible and `describe`-printable without exposing a hand-assembled
  policy vector (`happy-path-principle.md`). Design doc §4.0. **No call needed from you now** —
  I will propose it as part of Phase 2 and you review then; flagged here only so Phase 2 does
  not hard-code three monoliths and foreclose it.

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
  command, jail, credentials-omitted, at every phase (design doc §7). A phase that costs
  the happy path anything is wrong.
