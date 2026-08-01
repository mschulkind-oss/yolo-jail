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

## Phase 0 — Stop the destructive host-side write

**Design:** BACKLOG G1/G2; `host-render-target.md` §6.1, §8 step 1.
**Depends on:** nothing. **Ship regardless of whether the rest happens.**

Host-side `yolo config reset`/`capture` already resolve `~` against the invoking
human's real home and write it — `reset mise` truncates a real `~/.config/mise/config.toml`
to `"\n"`. Fix: refuse (or require `--force`) when `surfacesAreLocal()` is false; the
predicate exists (`configls.go:341`) and is consulted only by `composedFileExists`.

- **0.1** Gate `configReset` (`configdiff.go`) on `surfacesAreLocal()`; refuse with a
  message naming the surface, `--force` to override. **(data-loss fix)**
- **0.2** Gate `configCapture` the same way (the api-key-hint privacy leak, G2).
- **0.3** Regression test: a host-side `reset`/`capture` in a non-jail home is a
  refusal, not a write.

**Done when:** a host-side `reset`/`capture` cannot mutate a real dotfile without
`--force`. This is a ~20-line change and its own shippable unit — do it first.

---

## Phase 1 — One renderer, several targets (`internal/render` + `Target`)

**Design:** BACKLOG G3/G4/G5; `host-render-target.md` §3 (the load-bearing section),
§4, §7.1.
**Depends on:** Phase 0 (so the refactor lands on non-destructive host paths).

This is the foundation. Today `agentcfg.Compose`/`ComposeStateful` have two independent
callers — the in-jail boot render (`entrypoint/prism.go`) and the host-side `config`
verbs (`cli/config.go`, `configdiff.go`) — and three code comments admit the second
mirrors the first. Collapse them into one `internal/render` package parameterized by an
explicit `Target{Home, Workspace, SidecarDir, HostLayer, Tables, Hooks, Fields, Posture}`.

- **1.1** Introduce `internal/render` with `Target` + the three constructors
  `Jail(e)` / `Preview(dir)` / `Host(home)` (`host-render-target.md` §3.3). Move the
  three surface writers out of `entrypoint/prism.go`, keyed on `Target`.
- **1.2** `entrypoint` calls `render.Jail(e)`; `cli` calls `render.Preview()`. Delete
  the mirrored helpers and the two hand-maintained maps (`surfaceHasHostLayer`,
  `surfaceHasComputedLayer`, `configls.go:197,204`) — they become derived from `Target`.
- **1.3** `FieldSet` (G5): a target declares which kinds apply; an inapplicable kind is
  refused **by name**, never silently skipped.
- **1.4** Fix macos-user as a target row (G3): it currently renders zero surfaces
  silently because the run path returns before `stagePacks`. This is **the cheapest
  test of the abstraction** — an existing backend that should render into a real home
  and renders none. If `Target` cannot express macos-user cleanly, it will not express
  `host` either.

**The one hard risk (from `host-render-target.md` §3.5):** this refactors the
A12-fatal boot path — a regression stops jails from *starting*, not just misconfigures
one. **Retire it with a byte-equality check of every shipped pack's rendered surfaces,
before and after** — the same method Stage D used to prove the Go surface literals
equalled their generated pack declarations. `entrypoint` must not gain a `cli`
dependency; `liveTables` and `genStep`'s A12 policy stay in the caller, not the renderer.

**Done when:** boot render, `config render`, and (via macos-user) a real-home render
all go through one `render.Render(target, surfaces)`, and the byte-equality gate is
green.

---

## Phase 2 — Name the dial: the `confinement` key

**Design:** design doc §4, §4.0.
**Depends on:** Phase 1 (so a notch is a `Target`, not a code branch).
**Before you start — decide:** the composable-primitive question (§4.0, and the open
question below). Phase 2 ships the *three named presets*; it must not foreclose the
primitive layer underneath.

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

## Phase 4 — `apply --host`: the host notch becomes real

**Design:** BACKLOG G6; `host-render-target.md` §6 (the whole section), §6.5 postures,
§7.2; design doc §4.1.
**Depends on:** Phase 1 (`render.Host`), Phase 3 (`apply`), Phase 2 (`--at host`).
**Before you start — decide:** OQ-A (host sidecar location) and OQ-B (retire the host
layer?) below. Both change what Phase 4 writes.

- **4.1** `render.Host(home)` renders the applicable kinds into the real `$HOME`;
  postures `observe` → `assert` → (maybe never) `own` (§6.5). Default `observe`
  (dry-run); `assert` writes only pack-`managed` keys and records a sidecar so
  `--revert` means something.
- **4.2** Every host-target surface is `rmw` (§6.3) — `stateful`/`capture` is meaningless
  where the editor and yolo are the same person. `${workspace}`-using surfaces are
  refused (no referent).
- **4.3** `program` (install) below `jail` is **confirm-gated, not refused** (§4.1, the
  reviewed position): TTY-only, command shown, curl-to-shell shows the script,
  permission-bounded. **This wants its own threat-model pass first** (OQ-C).
- **4.4** `check --at host` reports host-render drift and hands off to `apply --host`
  (§3.4) — the host-side twin of the shipped `config drift`. `check` never writes.

**Done when:** `yolo apply --host` reasserts a pack's config surfaces into the real home
(behind `observe`/`assert`), `--revert` undoes exactly what it asserted, and inapplicable
kinds are refused by name.

---

## Phase 5 — `apply --sealed` and the input closure

**Design:** design doc §3.3 (the full-closure table + the sealing rule).
**Depends on:** Phase 3 (`apply`). Independent of the host notch — sealing is a
host-side check that needs no container (§3.3).
**Before you start — decide:** OQ-B (host layer) again, because it moves a row of the
closure table from Declared-impure to Declared.

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
**Before you start — decide:** OQ-D (checker-library boundary) and OQ-E (offer-to-run /
`sudo` confirm UX) below. §3.5 fixes the *shape*, not the schema.

- **6.1** Pack-authored `provides`/`install_hints` (per-system: `brew`/`apt`/`dnf`/`nix`),
  declared by the pack that *introduces* the dep (no re-declaring others' deps).
- **6.2** The shared **dep-checker**: probe "is this binary present, at what version,
  what installs it," used by `check`, by every `apply`, and standalone (`yolo
  check-deps`). Boundary leaning: a **declared schema** a third-party doctor can read,
  yolo shipping a reference checker over it (OQ-D).
- **6.3** The manifest as a **composed surface** at a fixed path (`~/.config/yolo/Brewfile`
  + apt/dnf/pacman kin), composed from all packs' hints, regenerated wholesale every
  `apply`, yolo-owned. Not a one-off in a random dir.
- **6.4** Offer-to-run: both no-elevation and `sudo` remedies are offer-to-run behind a
  per-step confirm; a `sudo` step runs `sudo <cmd>` and lets the OS password prompt show
  through (yolo never handles the credential). Never ambient; no-TTY = print-only; the
  manifest is always the floor (OQ-E).

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

These are the design doc's own unresolved points (its §8), pulled forward and tied to
the phase each blocks. **Everything above is decided enough to start; these are the
"decide before you start phase N" items.**

**OQ-A — Where does a host-target reconcile sidecar live? (blocks Phase 4)**
`host-render-target.md` §9.5. The jail sidecars are workspace-scoped; a host assertion is
machine-scoped (`~/.local/state/yolo-jail/host-render/`?), and if two workspaces both
`apply --host` they assert into one file — the sidecar is the only record of who put what
there. Last-writer-wins with a shared sidecar is probably right, but a `--revert` that
removes another workspace's keys is the failure to design against.

**OQ-B — Retire the host (`reads-host`) compose layer? (shapes Phase 4 and Phase 5)**
Design doc §3.3 "The `host` layer is the input we should retire." The lean is *yes* —
drop settings-inheritance, express personal settings as a local pack — because reading
settings *in* and asserting config *out* over the same file is an XOR, and it is the one
input whose meaning needs a container. Deciding yes collapses a Declared-impure closure
row into Declared (simpler Phase 5) and removes a Phase-4 fixpoint case. Deciding no
means Phase 4 must handle the §6.3 fixpoint. **This is the biggest single decision in the
plan** and should be made before either phase.

**OQ-C — Threat-model pass for confirm-gated `install` below `jail` (blocks Phase 4.3)**
Design doc §4.1, §8. Moving from "never" to "confirm-gated, TTY-only, command shown,
permission-bounded" is the right trust model (you already approved the pack), but the
confirm UX, what a curl-to-shell approval displays, and the category-(a)/(b) permission
split want to be pinned before code, not designed at the call site.

**OQ-D — The dep-checker library boundary (blocks Phase 6.2)**
Design doc §3.5, §8. A declared schema a third-party doctor reads (portable, leaning) vs.
an importable Go package (only if the probe logic gets subtle enough that a spec
under-specifies it). Getting it wrong reintroduces the duplication §3.5 exists to avoid.

**OQ-E — Offer-to-run confirm UX (blocks Phase 6.4)**
Design doc §3.5, §8. How a `sudo` step is presented; whether a multi-step manifest is
confirmed once or per-step. The design fixes the shape (offer-to-run, OS prompt shown,
never ambient, manifest is the floor); the UX detail is open.

**OQ-F — Composable-primitive model shape (shapes Phase 2.2)**
Design doc §4.0. The three notches are presets over (separate user / Seatbelt / bwrap /
namespace). The internal representation that lets a fourth combination be expressed
(and printed by `describe`) without becoming a policy vector the user hand-assembles
(`happy-path-principle.md`) is undesigned. Get it roughly right at Phase 2 so Phase 7's
Linux `guest` is a new preset, not a new special case.

---

## What this plan explicitly does not do

- **It does not re-list BACKLOG Stage G.** Phase 0 = G1/G2, Phase 1 = G3/G4/G5, Phase 4 =
  G6. BACKLOG stays the item-level tracker for those; this plan is the whole-vision
  sequence they sit inside.
- **It does not change the pack format.** Every phase renders through the shipped
  `contributes[]` substrate; `provides`/`install_hints` (Phase 6) is the one additive
  field, and it is additive.
- **It does not touch the happy path.** `yolo -- claude` in a fresh repo stays one
  command, jail, credentials-omitted, at every phase (design doc §7). A phase that costs
  the happy path anything is wrong.
