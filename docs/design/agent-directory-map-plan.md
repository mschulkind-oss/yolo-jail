---
title: "Agent directory map — implementation sketch"
date: 2026-09-28
status: draft
tags: [plan, sketch, packs, agent-directories, pi]
summary: "The parking lot for the agent directory map's implementation-level material: where the kind, the evaluator, the reporting and pi's map probably land, what each derived status reads, and the traps to check before relying on anything here. Not a hand-off; nobody builds from it while it is a sketch."
stage: SKETCH
next: "Nothing to hand off yet: §4, the jail-launch line, is built (the design's AM-D19); the rest waits on the design's rulings"
depends-on:
  - agent-directory-map.md
---

# Agent directory map — implementation sketch

**Status:** 2026-09-28 — incomplete, and unstable while questions are open.

**The design wins on behavior.** This file holds material that surfaced while writing
[`agent-directory-map.md`](agent-directory-map.md) and that needs no ruling. Where it disagrees with
the design, the design is right and this file is stale. No design decision is made here. An entry
that rests on an open question says so, and is revisited when that question is ruled.

The file locations below were read at `daac6eb4`. **Re-read the tree before relying on any of
them**, because an `implementation-plan` pass owns turning this into a hand-off.

---

## 1. The kind

- The kind lands in `internal/packdecl`: add `directory` to the `footprints` map in `kinds.go`, which
  is the authority `KnownKinds()` derives from, and add its fields to `Contribution` in
  `contributes.go`. Candidate spellings are `at` (the root, reusing the `state` field),
  `entries` (a map from a root-relative path to a class string or an object), `measured_against`,
  and `adds_to` for a contribution that adds entries under another pack's root instead of owning
  one (the design's [§5.4](agent-directory-map.md#54-more-than-one-pack-under-one-root)).
- **Combine rule.** Nothing in the existing `Combine` enum means "exclusive per root, merged per
  entry". Either add a value, or model the root and the entries as two claim targets. Check how
  `internal/packload/footprint.go`'s collision loop groups on `(kind, target)` before choosing.
  *Blocked on [OQ-AM1](agent-directory-map.md#OQ-AM1)* for whether non-owner packs may add entries
  at all.
- **Validation** goes in `validateContribution`. It needs: the closed class set (`state`, `cache`,
  `yours`), the closed marks (*blocked on [OQ-AM2](agent-directory-map.md#OQ-AM2)*), one-segment
  globs only, the existing traversal guard for path-bearing fields, and refusal of a root that is
  `.`, `.config`, `.local`, `.cache` or `.yolo` whole.
- **"Declared twice"** is one check: an entry's `credential: true` at a path the **same pack's**
  `shared_credentials` hook names in `from` (the design's
  [§5.2](agent-directory-map.md#52-what-the-map-derives-and-from-where)). Both sit in one manifest,
  so it runs in `packdecl`'s manifest-level validation (`Manifest.Validate` in `packdecl.go`, which
  sees every contribution; `validateContribution` sees one) and needs no surfaces. `composed`, `laid`
  and `retired` need no check of their own: the closed class set already refuses them, and the
  error text should say they are derived. An entry **at** a surface, content or hook path is
  expected; never compare entry paths against those.
- The tolerant decode (`DecodeTolerant`) must skip an unknown **field** of the new kind and report
  it, which is not only about an unknown kind. Check that the tolerant path handles per-field skew
  for a kind it knows.

## 2. The evaluator

- **Candidate home**: a new package beside `internal/basehome`. It takes its declarations **as a
  value**, the way `basehome.Decls` does, so the classification table is testable against a
  fixture that states what it assumes. One constructor reads the real selected packs.
- **Share `DeclsFromPacks`' projection, not its result** (the design's
  [AM-D14](agent-directory-map.md#AM-D14)). `internal/basehome/decls.go` already walks
  `HookContributions()` (with the load-bearing `shared_credentials` name filter), the content
  `into` values and `Surfaces()` paths. Factor that loop so it takes the pack set as an argument;
  `basehome` keeps passing the shipped set, the map passes the selected set and filters by the
  notch's field set. Do not reuse `CredentialFiles`' basename match.
- **The derived-status inputs**, each with the symbol that owns it today:

  | Status | Read from |
  | :--- | :--- |
  | composed: surfaces | `agentcfg.ManifestWith` over the selected packs, filtered by the notch's `render.FieldSet` (`internal/render/fieldset.go`) |
  | composed: content | the `files`, `skills` and `briefing` `into` values; `Destination.IsReserved` for the exclusions |
  | laid: hooks | `packdecl.KnownHooks` and the hook `from`/`at` fields. Recognize by the **exact** link string the hook writes: relative for `shared_*`, absolute for `per_jail_history`. `unshareDirectory` in `internal/entrypoint/packhooks.go` already matches a link by its target |
  | laid: macos-user | `entrypoint.DeriveDarwinHomeLayout` (links and mirrors), and `darwinoverlay.go`'s `overlayStagedSuffix` and `overlayAsideSuffix` |
  | laid: redirects | `paths.HomeFileRedirects` |
  | laid: bookkeeping | `packdecl.StoreBookkeepingPrefix` |
  | retired | `manifest.Surface.RetireOnFirstRender`, jail notch only, and only for a surface whose mode is not `stateful` (a stateful surface retires only on its migrating write, `packsurfaces.go`). The `unshare_directory` hook's `at` link |
  | credential mark | the `shared_credentials` hooks' `from`, exact path |
  | merged | the profile-gated `env` pair naming the prelaunch credential path (`YOLO_AUTH_PRELAUNCH_*_PATH` in `packs/pi/pack.json`). Check whether a structured declaration exists before parsing an env value |

- **Dangling links.** The map's predicate is **not** `FindBrokenLink` as-is: its
  destination-itself exemption (`dirExists(filepath.Dir(end))`) applies only to a composed file at
  the host notch ([AM-D5](agent-directory-map.md#AM-D5)). Share the chain walk (`linkChainEnd`,
  `firstHop` in `internal/entrypoint/hostbrokenlink.go`) rather than copying it; it lives in
  `internal/entrypoint`, so calling it from a host-CLI package may create an import cycle. If so,
  move the walker to a leaf package and keep **one** copy. Wrong type needs an `os.Stat` at the
  chain's end.
- **Root resolution** follows a link at the root only. On darwin `t.TempDir()` is itself behind a
  symlink, so test fixtures must be minted with `EvalSymlinks`, and the suite should be run under
  `TMPDIR=/tmp/link` as [AGENTS.md](../../AGENTS.md#testing) describes.
- **Relocation** checks the environment the **agent** will see. At `yolo host --` that is the
  composed launch environment, not the caller's. See how `host.go` builds it. A variable the launch
  itself set (`launch.vars["CODEX_HOME"]` in `internal/openaiauthhost/host.go`) is not a finding:
  its value is a derived root ([AM-D13](agent-directory-map.md#AM-D13)), so the evaluator needs to
  know which variables yolo set, not only the final environment.

## 3. Reporting

- **`yolo check`**: a new section following the `internal/cli/check/sections_*.go` pattern, with
  JSON rows through `jsonreport.go`. An in-jail check reads the jail's own home.
  *Blocked on [OQ-AM6](agent-directory-map.md#OQ-AM6).*
- **`yolo host apply`**: a group rendered after the verdict block. This amends
  [`report-tiers.md`](../reference/report-tiers.md#the-tiers), because a map finding renders like
  tier 3 and is excluded from the verdict. The candidate code is `hostapplyverdict.go` and
  `hostapplydetail.go`.
- **`yolo host --` preflight**: runs before the launch gate (`hostapplygate.go`), and nothing it
  finds is an input to the gate. Its budget is a package var, as `hostApplyGateBudget` is, and it
  must never run in the gate's refusal path.
- **`yolo pack map`**: a new verb in `packMain` (`internal/cli/pack.go`), plus `subhelp.go`.
- **`yolo config ls` footer**: `configls.go`.
- **The seen record**: at the host only, a file beside `host-reserved-trees.json`, which is
  [ST-N2](../reference/pack-system.md#st-n2)'s record and the pattern to copy. Only host apply and the
  `yolo host --` preflight read or write it; `yolo check` and `yolo pack map` must not
  ([§4.5](agent-directory-map.md#45-print-when-new)). There is no in-jail record.

## 4. The jail-launch line (independent of the map)

✅ **Built** 2026-09-30, as the design's [AM-D19](agent-directory-map.md#AM-D19) records. The
import-cycle worry in [§2](#2-the-evaluator) did not arise: `internal/cli/run` already imports
`internal/entrypoint`, so the read-side predicate, `entrypoint.FindDanglingLink`, sits beside
`FindBrokenLink` on the one chain walk. The bullets below are what it was built from.

- `hostFileArgs` in `internal/cli/run/packhostgrants.go`: when `isFile` is false, ask whether the
  source is a dangling link (the map's predicate, not `FindBrokenLink`'s: a read through a dangling
  link fails whether or not the target's directory exists). If it is, emit one launch line (a run
  fact with a remedy) naming the link and its target. The skip itself stays.
- The briefing's `after: host:` goes through `briefingHostOverlay` (`prepare.go`) and then
  `jailcontent.PrependHostBriefing`, which swallows any read error. It needs the same check at the
  caller.

## 5. Pi's map

- It goes in `packs/pi/pack.json` as a `directory` contribution, transcribed from the design's
  [§6.2](agent-directory-map.md#62-the-map). *Blocked on [OQ-AM9](agent-directory-map.md#OQ-AM9)*
  for the root.
- The ecosystem entries (pi-mcp-adapter, pi-subagents, pi-web-access, pi-automode,
  pi-dynamic-workflows) go in the maintainer's personal content pack, outside this repository.
  *Blocked on [OQ-AM1](agent-directory-map.md#OQ-AM1).*
- Before transcribing, re-read pi's `dist/` for the version then current, and update
  `measured_against`.

## 6. Tests

- **A fixture tree** recorded from the measured layout: the incident's three dangling links, the
  partial move (`npm -> dot/npm` with `dot/` present), a link at `npm/` that ends at a regular file,
  `mantle/`, a 0-byte `APPEND_SYSTEM.md`, the laid `npm` link, and lock directories.
- **Call-site tests** that fail when the preflight, check or apply call is deleted, not only unit
  tests of the evaluator ([AGENTS.md](../../AGENTS.md#testing)).
- The "declared twice" refusal (a same-pack `credential` restating a `shared_credentials` hook),
  and that the shipped pi map, whose entries sit at composed and laid paths, loads clean. A
  cross-pack conflict that is skipped, not fatal. A root collision.
- The seen record: a `yolo check` between two preflights marks nothing seen.
- The preflight budget, tested the way `TestHostApplyGateExecsWhenTheBudgetExpires` avoids a racy
  nanosecond budget (read that test's comment first).
- In-jail `yolo check` needs a nested-jail verification (`cd /tmp/yolo-nested`, per AGENTS.md), and
  possibly an integration test gated by `requireJail`.

## 7. Docs on graduation

- [`pack-system.md`](../reference/pack-system.md#the-per-kind-rules-worth-knowing) gets a
  `directory` subsection. [`report-tiers.md`](../reference/report-tiers.md) gets the new group.
- A `CHANGELOG.md` line: users can now see dangling links and unknown files in their agent
  directories, and ask what deleting one would lose.
- [`pack-declared-file-diagnostics.md`](pack-declared-file-diagnostics.md) is superseded if
  [OQ-AM8](agent-directory-map.md#OQ-AM8) rules A.

## 8. Check before relying on it

- Whether claude's `skills/.trash` and `skills/.staging` are siblings of `synced/` or inside it.
  The bundle and [`synced-skill-trees.md` §2.3](synced-skill-trees.md#23-reserved-siblings)
  disagree. This must be measured before the claude slice.
- Whether copilot 1.0.48's `config.json` migration loops against yolo's strict JSON `rmw` decode.
  Measure it in a scratch home before the copilot slice, and possibly before anything else.
- Whether agy writes its OAuth token **through** the laid link or replaces the link with a rename.
