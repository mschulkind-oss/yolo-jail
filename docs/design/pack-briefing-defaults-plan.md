---
title: "Pack briefing defaults — companion sketch"
date: 2026-09-22
status: draft
tags: [sketch, packs, briefing, skills, defaults]
summary: "File-level map and measured anchors for pack-briefing-defaults.md. A parking lot, not a hand-off: no design decision lives here, and it must not be built from while it is a SKETCH."
vantage:
  status-chip: true
---

# Pack briefing defaults — companion sketch

**Status:** SKETCH, 2026-09-22 — incomplete, and unstable while
[OQ-PB1](pack-briefing-defaults.md#OQ-PB1)–[OQ-PB4](pack-briefing-defaults.md#OQ-PB4) are open.

> [!IMPORTANT]
> **Not a hand-off artifact.** An agent must not build from this while it is stamped SKETCH. It
> becomes a plan through `implementation-plan`, against the tree, once the design's questions are
> ruled.

**Reads with:** [`pack-briefing-defaults.md`](pack-briefing-defaults.md) — **the design wins on
behaviour.** If an entry here contradicts it, this file is wrong.

---

## 1. The sites that carry each default

Measured at `3ac4e8b1`. Re-read before relying on any line number.

| Default | Site |
|---|---|
| The conventional briefing file list (`["AGENTS.md"]`) | `internal/packdecl/contributes.go` `DefaultBriefingFiles` |
| The `[from, AGENTS.md]` fallback chain | `internal/packdecl/contributes.go` `BriefingCandidates`; consumed by `internal/packload/briefingsource.go` `BriefingProseFor` |
| `into` required without `agents` | `internal/packdecl/contributes.go` `validateContribution`, the `KindSkills, KindBriefing, KindFiles` arm (`req("into", c.Into)`) |
| Jail briefing suppression (`if !declared`) | `internal/cli/run/packs.go` `packBriefingProses` |
| Jail skills suppression (`if !declared`) | `internal/packload/skillssource.go` `SkillsSources` |
| Host suppression (`len(out) == 0 && !p.declares(kind)`) | `internal/packload/mergedest.go` `borrowingSources`, `declares` |
| The false "precedence matches briefing's" claim | `internal/packload/skillssource.go` header |
| The false "ABSENT MEANS BROADCAST" claim | `internal/packdecl/contributes.go` `Contribution.Agents` doc |
| Scaffold text and advice | `internal/cli/pack.go` `packInit` |
| The "this line adds nothing" advisory | `internal/cli/pack.go`, lint's agent-pack-destination loop |
| Local-pack migration target (`local/AGENTS.md`) | `internal/cli/applyhostbriefings.go` `localPackBriefingPath`; `entrypoint.HostBriefingRequest.LocalPackAGENTS` |
| Pack guide text naming `AGENTS.md` | `internal/cli/pack.go` usage (`A zero-ceremony pack needs no manifest…`) |
| Reference prose | `docs/reference/pack-system.md` (`#### briefing`, `#### skills`), `docs/reference/agent-briefings.md` (`Audiences`) |

## 2. Notes

- **One governance predicate, three callers.** The three suppression sites are the reason the trap
  reaches every notch; the design's R5 wants them to become one "which files does this pack's
  declaration set govern, and what is implicitly broadcast" function in `packload`, called by all
  three.
- **Existing test that pins the old behaviour:** `internal/packload/briefingsource_test.go` pins
  `AGENTS.md` as "THE convention and the ONLY one" and the whitespace-stub fallback. Both invert.
- **`pack footprint` already has a "neither: the zero-ceremony broadcast" branch**
  (`internal/packload/footprint.go`), which is the natural home for the implicit-delivery line in
  lint's output.
- The rename touches `pack --help`, the scaffold, the reference docs, the migration writer, and the
  `yolo pack lint` "conventionally-read location (skills/, AGENTS.md)" message — grep
  `AGENTS\.md` under `internal/cli` before calling it done.
- **`DefaultBriefingFiles` stops being a file list.** Under [OQ-PB1](pack-briefing-defaults.md#OQ-PB1)'s leaning the
  convention is a directory read one level deep, so its return type and every `BriefingCandidates` caller change shape.

Blocked on [OQ-PB1](pack-briefing-defaults.md#OQ-PB1) — every spelling of the new location above.
