---
title: "Host-only config contributions — implementation sketch"
date: 2026-09-27
status: deprecated
stage: SUPERSEDED
next: "Nothing: the build is done, and the design's Decision Ledger (NS-D1 to NS-D26) is its record"
---

# Host-only config contributions — implementation sketch

**Status:** 2026-09-27 as a sketch, and superseded by the build. It was unstable while
[OQ-5](notch-scoped-config-contributions.md#OQ-5) was open; that question was ruled as leaned on
2026-09-28. Evidence verified at `8da7840d`. **[§2](#2-step-1--posture-lists) (rows 1–7), row 8
and [§4](#4-the-end-to-end-test-of-the-fifth-disposition) were BUILT on 2026-09-27** on
[OQ-5](notch-scoped-config-contributions.md#OQ-5)'s leaning (`bbe5c878`, `499a332f`,
`a6021d86`); the design's [ledger](notch-scoped-config-contributions.md#10-decision-ledger)
records the mechanism choices as `NS-D` rows (NS-D14 is a host-apply fix the review found outside
this build, and NS-D15 to NS-D18 correct it).
[§3](#3-only-if-the-rulings-are-amended--the-posture-modifier) is moot: it applied only if
[OQ-5](notch-scoped-config-contributions.md#OQ-5) amended the rulings, and the ruling kept them.
[OQ-3](notch-scoped-config-contributions.md#OQ-3)'s posture overlay was built on 2026-09-28
(`12032eb2`) with no section here; the design's ledger rows NS-D19 to NS-D26 are its record.

> **Precedence.** This is the implementation sketch beside
> [`notch-scoped-config-contributions.md`](notch-scoped-config-contributions.md). The design wins on
> every behavior, and nothing here makes a design decision. Do not build from this file: what it
> sketched is built, and the design's ledger is the record.

---

## 1. Codebase map

Symbols, not lines. Rows 1–7 follow the design's recommended shape
([§4.1](notch-scoped-config-contributions.md#41-recommended-posture-lists-inside-autonomy)) and rest
on [OQ-5](notch-scoped-config-contributions.md#OQ-5)'s leaning; rows 8–9 are needed whichever way
it is answered.

| # | Where | What changes |
| :--- | :--- | :--- |
| 1 | `packdecl.AutonomyPosture` (`internal/packdecl/contributes.go`) | A `Lists` field: `{surface, path, add}` entries, the same shape a `config-list` carries |
| 2 | `validateAutonomyPosture` | Each entry through `configListProblems` and `configListPathProblems`; a posture with only `lists` is valid |
| 3 | `packoverlay.Collect` (`internal/packoverlay/packoverlay.go`) | Take each pack's posture lists in the existing list pass: decode both postures' lists, then the gate on `autonomy`, then the owner check and `listsByTarget`. An ownerless one is an `OrphanOverlay` whose kind is `autonomy`. No signature change |
| 4 | `Collect`'s doc comment and `internal/packoverlay/autonomyinert_test.go` | Rewrite "its effect on this function's output is zero" to "it never changes surface identities or ownership, and it selects posture lists"; keep the identity pin, add the list assertion |
| 5 | `packload` footprint (`internal/packload/footprint.go`, the `KindAutonomy` case) | Name each posture's lists in the claim's detail, as the `profile` detail does |
| 6 | `surveyNotchFacts` (`internal/cli/hostapplynotch.go`) | `AutonomyFolds` true when the host posture declares `lists`, not only `config`. *Narrowed after review (NS-D12):* true only for a list `Collect` placed (`OverlaySet.PlacesPostureListFrom`), since an ownerless one folds nothing |
| 7 | `docs/reference/pack-system.md` | The `autonomy` and `config-list` sections: posture lists, their gate, their disclosure |
| 8 | `(*Options).buildMacosCtxTree` (`internal/cli/run/macosctxtree.go`), `macosuser.HostContext`, `macosuser.hostLayerWire` (`internal/macosuser/runplan.go`) | Render-mark parity: compute `Rendered` with `hostLayerIsRender`, carry it on the context, marshal `entrypoint.HostLayerWire` |
| 9 | `docs/reference/config-target-resolution.md` | Gap 1 closes when row 8 lands; gap 2 closes with [§4](#4-the-end-to-end-test-of-the-fifth-disposition) |

**No change at the callers.** Every production caller of `Collect` already passes its notch's bit:
`entrypoint.ConfigurePackSurfaces`, `entrypoint.ConfigurePackByName`, `applyHostSurveyed`,
`renderContributions`, `overlayContributionRows` and `loadPromoteFold`.

---

## 2. Step 1 — posture lists

Rests on [OQ-5](notch-scoped-config-contributions.md#OQ-5)'s leaning. If [OQ-5](notch-scoped-config-contributions.md#OQ-5) amends the rulings,
[§3](#3-only-if-the-rulings-are-amended--the-posture-modifier) replaces this section.

- **The gate's position** mirrors the `profile` gate in Pass 2: decode first (so a malformed entry
  is reported at every notch), gate second, owner check third (so an unselected entry is never an
  orphan).
- **Order.** Pack order, then declaration order
  ([config-list-order](../reference/pack-system.md#config-list-order)); a posture's lists stand at
  the `autonomy` contribution's position in `contributes`. Check that the existing
  `ConfigListContributions()` walk can keep that order, or walk `Contributions()` instead.
  *Answered in the build:* it cannot, so a new `ListContributions()` walks `Contributions()`
  (NS-D5).
- **`OrphanOverlay.Reason`** prints `kindName`, the kind as written, so an `autonomy`-kind orphan
  needs no new branch; check that the core-owned sentence still reads right with `autonomy` in it.
  *Answered in the build:* it did not ("autonomy contributes to a surface a pack owns" is false of
  the kind's own config patch), so that sentence says "a posture list" (NS-D7).
- **Tolerant decode.** `DecodeTolerant` uses `json.Unmarshal`, so an older entrypoint drops the
  nested `lists` field silently. That is the intended fail-closed behavior; nothing to add.

---

## 3. Only if the rulings are amended — the posture modifier

Was blocked on [OQ-5](notch-scoped-config-contributions.md#OQ-5), which kept the rulings on
2026-09-28, so this section was never built. The selector is the posture either
way ([OQ-1](notch-scoped-config-contributions.md#10-decision-ledger)).

- A `Posture` field on `Contribution`, validated in `validateContribution` by one helper for
  `config-list` and `config-overlay`, and refused on every other kind with the pattern the
  `profile` modifier uses (`c.Profile != "" && c.Kind != …`). There is no `configOverlayProblems`.
- The gate in Pass 2 and Pass 3, at the same position as [§2](#2-step-1--posture-lists)'s.
- ⚠ **Do not import `render` from `packdecl`**: `internal/render/fieldset.go` imports
  `internal/packdecl`, so that is a cycle. `render.IsValidNotch` does not exist; `KindForNotch`
  does. A posture value needs neither.
- ⚠ **Skew fails open for this shape**: an older in-jail reader ignores the modifier and renders
  the list unconditionally. The design's [§4.5](notch-scoped-config-contributions.md#45-failure-paths)
  states it; nothing in the code can close it.

---

## 4. The end-to-end test of the fifth disposition

Gap 2 of [config-target-resolution.md](../reference/config-target-resolution.md#where-this-does-not-reach):
one managed home, asserted into; the launcher's `hostLayerEnv` output fed to a boot render; assert
the surface composed without a host layer. The existing unit halves to reuse:
`TestHostSurfaceRenderedReadsTheProvenanceMark` (the mark),
`TestBootDoesNotComposeAHostLayerLabelledARender` (the read) and
`TestRenderDropsAHostLayerTheLaunchLabelledARender` (the host-side preview).

---

## 5. Test strategy

Every test here must fail when its production call site is deleted (AGENTS.md, Testing).

1. **`internal/packdecl`:** `guarded.lists` decodes; a malformed entry (null entry, root path,
   missing `add`) is refused with `config-list`'s wording; an unknown field is refused by the
   strict decoder.
2. **`internal/packoverlay`:** `Collect(…, false, …)` holds the guarded entry and not the
   autonomous one; `Collect(…, true, …)` the reverse; no orphan for the unselected posture; an
   ungated `config-list` is unchanged at both bits; the identity pin still holds.
3. **Jail boot:** through `ConfigurePackSurfaces`, the guarded entry is absent from the rendered
   `pi/settings`.
4. **Host:** `RenderHostPack` inserts the entry and writes the insert record
   (`internal/entrypoint/configlistrender_test.go` has the helpers).
5. **Inspection:** `yolo config ls` and `yolo config render` at `--at host` and `--at jail` differ.
6. **`macos-user`:** `hostLayerWire` carries `Rendered` for a marked home, and the boot read
   drops it.

---

## 6. Traps to check before building

- **`packrender_test_support.go` is production code.** `ConfigurePackByName` in it is
  `yolo check`'s dry-run probe (`internal/cli/check/entrypoint.go`), not only a test helper.
  *After review (NS-D13):* its body is `configureOnePack`, so a fixture pack reaches the probe's
  `Collect` call; no embedded pack declares a posture list.
- **`Collect`'s doc comment names `configdiff.go` as a caller.** At `8da7840d` it is not one; the
  callers are the six in [§1](#1-codebase-map). Fix the comment while rewriting it (row 4).
  *Fixed in `bbe5c878`:* the comment now names the six.
- **Run the in-jail suite with the jail's variables unset** (`YOLO_VERSION`, `YOLO_HOST_LAYERS`);
  both skew `go test` in here.
- **`git add` before any nested-jail verification**: the nested image build sees tracked files only.
- **`macos-user` parity cannot be verified in a nested Linux jail.** It needs the Mac runner or CI.
