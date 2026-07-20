# Plan: module consolidation + "always-Go" cleanup

**Status:** OPEN — post-cutover endgame, not started. Pulled out of the archived
`go-port-post-transition.md` §3 (+ §4 OSS-hygiene remnants). Now actionable (the
Python wipe landed). Jail-testable; cosmetic/structural, no behavior change.

## Goal

The Go port is split across ~34 `internal/*` packages + several `cmd/*` binaries
that **mirror the old Python module boundaries**. Those seams were transition
scaffolding; consolidate to a structure that reads as native Go, and strip the
last Python-era residue.

## The work

- [ ] **Consolidate packages.** Collapse the Python-mirroring `internal/*` split
  into a structure that reads as native Go. Decide whether the `cmd/yolo-*`
  daemons fold into one multi-call binary — this also affects the goreleaser
  `builds:` set, so do it together with distribution if that changes.
- [ ] **Drop parity/divergence machinery.** The live-Python oracles, drift dump,
  and `divergences.md` are historical now — keep the regression *tests*, drop
  the *comparisons*. Rename `*_parity_test.go` files that are now plain unit
  tests. (Verify none remain load-bearing first.)
- [ ] **Strip "ports X" docstrings** and other "this mirrors the Python" comments
  across `internal/` so the code reads as the spec, not a translation.
- [ ] **Shared rich→ANSI renderer.** Lift the color-aware renderer into one
  helper as part of this consolidation — the same duplication the
  [cli-color-audit.md](cli-color-audit.md) pass targets. Landing it here fixes
  the lost-color bug everywhere at once and removes the four near-duplicate
  `richTagRe` printers. **Do these two plans together.**

## §4 OSS-hygiene remnants (mostly done — verify + close)

The bulk of the OSS-hygiene sweep landed with the distribution work. Verified
2026-07-20:

- ✅ README `## Install` — the standard 4-channel block is present (brew / go
  install / pipx / source).
- ✅ `.github/workflows/` — the 3-workflow set (ci/release/publish) exists.
- ✅ `dependabot.yml` — has `gomod`; no `pip` ecosystem.
- ✅ Versioning — git tags (no `setuptools-scm`, no tracked `pyproject.toml`).

Remaining:

- [ ] Update the maintainer's project dossier
  (`references/projects/yolo-jail.md` in the backplane) from "Python CLI +
  container" → "Go CLI + container", and the distribution.md channel matrix.
  (Lives outside this repo.)
- [ ] Run the `open-source-project` skill audit against the Go-only repo once
  the consolidation settles, to catch anything the manual check missed.

## Sequencing

Lowest priority of the open backlog; do it after the higher-value items
([nix-ld-dynamic-linking.md](nix-ld-dynamic-linking.md),
[cli-color-audit.md](cli-color-audit.md), and the macOS revival plan's J2/J3)
land, so it consolidates a settled tree rather than a moving one. The color-audit
renderer is the one piece worth pulling in early if the audit runs first.
