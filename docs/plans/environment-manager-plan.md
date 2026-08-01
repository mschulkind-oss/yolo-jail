# Environment-manager implementation plan

**Status:** plan, 2026-07-31. Sequences [`../design/yolo-as-environment-manager.md`](../design/yolo-as-environment-manager.md)
(the vision — finalized, the maintainer is happy with it) into buildable phases.

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

**Nothing here is built.** Verified 2026-07-31: no `confinement` config key, no
`apply`/`describe`/`--at`/`--sealed` verb, no `guest` notch (`bwrap`/Landlock), no
`internal/render` package exist in the code. The two shipped pieces the design leans on
are `yolo config dump` (the canonical computed-config dump `describe` will absorb) and
`yolo config drift` (the in-jail cousin of the drift/sealing story). The pack system —
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
```

Phases 0 and 1 are already scoped in BACKLOG Stage G. Phases 2–8 are this plan's
addition, and each is gated by the decisions in its "Before you start" note.

---

## Phase 0 — Stop the destructive host-side write  *(was BACKLOG G1 + G2)*

**Design/reasoning:** `host-render-target.md` §6.1 (the probes), §8 step 1.
**Depends on:** nothing. **Ship regardless of whether the rest happens.**

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

## Phase 1 — One renderer, several targets (`internal/render` + `Target`)  *(was BACKLOG G4 + G5 + G3)*

**Design/reasoning:** `host-render-target.md` §3 (the load-bearing section), §4, §7.1.
**Depends on:** Phase 0 (so the refactor lands on non-destructive host paths).

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

## Phase 2 — Name the dial: the `confinement` key

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

## Phase 3 — The verbs: `apply` and `describe` (jail-first)

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

## Phase 4 — `apply --host`: the host notch becomes real  *(was BACKLOG G6)*

**Design/reasoning:** `host-render-target.md` §6 (the whole section), §6.5 postures,
§7.2; design doc §4.1.
**Depends on:** Phase 1 (`render.Host`), Phase 3 (`apply`), Phase 2 (`--at host`).
**Before you start — decide:** OQ-3, OQ-4, OQ-5 (host layer / capture / capture-storage)
and OQ-6, OQ-7 (the confirm-gated-install detail, for 4.3). OQ-1 and OQ-2 are RESOLVED
and already assumed below.

- **4.1** `render.Host(home)` renders the applicable kinds into the real `$HOME`;
  postures `observe` → `assert` → (maybe never) `own` (§6.5). Default `observe`
  (dry-run); `assert` **regenerates only the pack's `managed` keys** and leaves the
  user's own keys — the shipped "regenerate, don't reconcile" model applied to a real
  home. **No `--revert` verb** (OQ-1, resolved): dropping yolo's management is "stop
  declaring the key and re-apply," which removes it with a notice exactly as an unset MCP
  server is dropped in a jail today; there is no restore-to-pre-yolo-state, which would
  need a before-snapshot nothing takes. **User-scoped, workspace contributes nothing**
  (OQ-2, resolved).
- **4.2** Whether a host surface stays `stateful`+capture or is pure `rmw` is **OQ-4**,
  not settled: §6.3 currently says pure `rmw`, but a host agent editing its own config
  between applies is the case that model gets wrong, so the leaning is to keep capture.
  Either way, `${workspace}`-using surfaces are refused (no referent).
- **4.3** `program` (install) below `jail` is **confirm-gated, not refused** (§4.1, the
  reviewed position): TTY-only, command shown, curl-to-shell shows the script,
  permission-bounded. **The two confirm details are OQ-6 and OQ-7.**
- **4.4** `check --at host` reports host-render drift and hands off to `apply --host`
  (§3.4) — the host-side twin of the shipped `config drift`. `check` never writes.

**Done when:** `yolo apply --host` regenerates a pack's `managed` config keys into the
real home (behind `observe`/`assert`) without clobbering the user's own keys, and
inapplicable kinds are refused by name.

---

## Phase 5 — `apply --sealed` and the input closure

**Design:** design doc §3.3 (the full-closure table + the sealing rule).
**Depends on:** Phase 3 (`apply`). Independent of the host notch — sealing is a
host-side check that needs no container (§3.3).
**Before you start — decide:** OQ-3 (retire the host layer) again, because it moves a row
of the closure table from Declared-impure to Declared.

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

## Phase 6 — Dependency provisioning below `jail`

**Design:** design doc §3.4, §3.5.
**Depends on:** Phase 1 (`FieldSet`/notch-aware render), Phase 4 (host apply is where a
missing dep bites). At `jail` this is a near-formality (the toolchain is in the sealed
image); below `jail` it is the real work.
**Before you start — decide:** OQ-8 (checker-library boundary) and OQ-9 (offer-to-run /
`sudo` confirm UX) below. §3.5 fixes the *shape*, not the schema.

- **6.1** Pack-authored `provides`/`install_hints` (per-system: `brew`/`apt`/`dnf`/`nix`),
  declared by the pack that *introduces* the dep (no re-declaring others' deps).
- **6.2** The shared **dep-checker**: probe "is this binary present, at what version,
  what installs it," used by `check`, by every `apply`, and standalone (`yolo
  check-deps`). Boundary leaning: a **declared schema** a third-party doctor can read,
  yolo shipping a reference checker over it (OQ-8).
- **6.3** The manifest as a **composed surface** at a fixed path (`~/.config/yolo/Brewfile`
  + apt/dnf/pacman kin), composed from all packs' hints, regenerated wholesale every
  `apply`, yolo-owned. Not a one-off in a random dir.
- **6.4** Offer-to-run: both no-elevation and `sudo` remedies are offer-to-run behind a
  per-step confirm; a `sudo` step runs `sudo <cmd>` and lets the OS password prompt show
  through (yolo never handles the credential). Never ambient; no-TTY = print-only; the
  manifest is always the floor (OQ-9).

**Done when:** `check --at host`/`apply --at host` report missing deps, write the
manifest surface, and (behind a confirm) can run the remedies including `sudo`.

---

## Phase 7 — Make the `guest` notch actually work

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

## Phase 8 — The environment describes itself to its agent

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

## Open questions to resolve before their phase

**Each OQ below is ONE decision** — a single yes/no or a pick-from-two, with a leaning
and a "if no preference, I build X" fallback — so you can answer them one at a time
rather than untangle a bundle. They are grouped by the phase they block, and marked
**RESOLVED** once you have answered. A short **Context** line precedes each cluster; the
long reasoning lives in the cited design section, not here.

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

**Context — the host `host` layer and how a host agent keeps editing its own config.**
Design doc §3.3 wants to *retire* the `reads-host`/`host` compose layer (reading your real
`~/.claude/settings.json` *into* a jail), because reading-in and asserting-out over one
file is an XOR. Separately, `host-render-target.md` §6.3 claims "on host every surface is
`rmw`, capture is meaningless — same person edits both." That claim uses the wrong axis: a
host `claude` writes its own settings constantly, and the real split is **apply-time (yolo
asserting) vs. between-applies (the agent working)** — exactly what the jail's
`stateful`+capture model already draws. These are now three separate calls:

- **OQ-3 — Retire the `reads-host` read-*in* layer? (yes / no / defer)** *Leaning: yes* —
  express personal settings as a local pack instead (declared, locked, portable), which
  collapses a Declared-impure closure row into Declared and removes a Phase-4 fixpoint.
  Design doc §3.3. **If no preference, I build "yes, retired."**
- **OQ-4 — On the host notch, does a config surface stay `stateful`+capture, or is it pure
  `rmw`?** *Leaning: keep `stateful`+capture* — so the agent edits its own config freely
  and `apply` re-asserts only `managed` keys over it, rather than pure `rmw` overwriting.
  Answering "capture" **reopens `host-render-target.md` §6.3**; I will draft that revision
  once you pick, since it sets what Phase 4.2 builds. **If no preference, I build
  "capture."** *(This is the biggest single behavior call in the plan.)*
- **OQ-5 — Where does a host capture overlay live?** Only live if OQ-4 = capture. *Leaning:*
  `~/.local/state/yolo-jail/host-render/`, keyed by target file (user/machine-scoped per
  OQ-2). `host-render-target.md` §4.4, §6.6. **If no preference, I build that path.**

### Blocks Phase 4.3 (confirm-gated install)

**Context — install below `jail` is confirm-gated (policy decided, §4.1); two threat-model
details are open.** I will draft a short note for your sign-off rather than improvise at
the call site.

- **OQ-6 — What does the curl-to-shell install confirm display?** URL only / URL + fetched
  script / URL + a hash. *Leaning: show the resolved URL and the fetched script* (approve a
  specific thing, not a category). **If no preference, I build "URL + script."**
- **OQ-7 — Where is the category-(a) *no-elevation* / category-(b) *needs-`sudo`* line drawn
  per remedy?** *Leaning:* (a) = writes only under the user's own tree (user `brew`, `pip
  --user`, `~`); (b) = anything else. **If no preference, I build that rule.**

### Blocks Phase 6 (dep provisioning)

- **OQ-8 — Dep-checker boundary: a declared schema, or an importable Go package?** *Leaning:
  schema* — portable to a third-party doctor; add a Go helper only if a spec proves too
  weak. Design doc §3.5. **If no preference, I build the schema.**
- **OQ-9 — Offer-to-run confirm: once for the whole manifest, or per command?** *Leaning:
  per-command for `sudo`/category-(b) steps, once for category-(a).* Design doc §3.5. A UX
  call, not a blocker. **If no preference, I build the split.**

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
