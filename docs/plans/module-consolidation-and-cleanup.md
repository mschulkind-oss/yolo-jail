# Plan: module consolidation + "always-Go" cleanup

**Status:** DONE (2026-07-21) — the cleanup landed (comment sweep, native-Go
filenames, parity-cruft removal, shared renderer). Package-merging + daemon-fold
were assessed and deliberately declined as churn-without-value (see "The work").
Pulled out of the archived `go-port-post-transition.md` §3 (+ §4 OSS-hygiene).
Jail-tested; no behavior change. Remaining §4 items are out-of-repo (dossier
update) or a final `open-source-project` skill audit.

## Goal

The Go port is split across ~34 `internal/*` packages + several `cmd/*` binaries
that **mirror the old Python module boundaries**. Those seams were transition
scaffolding; consolidate to a structure that reads as native Go, and strip the
last Python-era residue.

## The work

**Done 2026-07-21 — cleanup, not consolidation-for-its-own-sake** (maintainer
steer: "reorg to how it would be if built in a direct path; I don't want
consolidation for consolidation's sake, I want clean up"):

- [x] **Strip "ports X" docstrings** — 11 packages / 137 files swept
  (91 stripped, 395 rewritten into spec sentences, 31 frozen-contract markers
  preserved+reframed). AST-diff verified comment-only. (`743e053`)
- [x] **Native-Go filenames** — 6 library packages' `main.go` (a Python
  `__main__.py` artifact) → `<pkg>cmd.go` (the repo's own `*cmd.go` convention);
  `run/helpers2.go`+`helpers3.go` (numbered = split-Python tell) → `helpers.go`
  + `sliceutil.go`. Pure renames. (`d2b2db7`)
- [x] **Drop parity/divergence machinery** — verified already retired: no
  `*_parity_test.go` files, no `divergences.md`, no live oracle/drift refs in
  Justfile/scripts; removed the stale `tools/parity/` pycache remnant.
- [x] **Shared rich→ANSI renderer** — done with cli-color-audit
  (`internal/richtext`; prune/builder/macosuser/broker (and the top-level cli
  commands) route through it; run itself still carries the private
  `richToANSI`/`stripRich` that richtext was extracted from).

**Deliberately NOT done — would be churn that loses meaning:**

- **Package "consolidation."** Assessed all 43 `internal/*` packages: none are
  Python-boundary shims. They're cohesive Go packages split by concept (the
  37-file `cli/run` is split by-topic — assemble/mounts/network/lsp/identity/… —
  exactly as a large Go package built directly would be; single-file packages
  like `shquote`/`pytext`/`paths`/`version` are legit utilities, à la stdlib
  `path/filepath`). Package/dir names all match; no mismatch smells. Merging them
  is consolidation-for-its-own-sake — explicitly out of scope.
- **`cmd/yolo-*` daemon folding.** Already effectively done: goreleaser ships
  ONE binary (`yolo`); the daemons are `yolo internal daemon <…>` subcommands
  and the `cmd/yolo-*` mains are thin image-side entry points. No goreleaser
  `builds:` change needed.
- **Python-semantic identifiers** (`pyTruthy`/`pyStr`/`pyEqual`/`pyReprValue`,
  package `pytext`) are KEPT: they name Python's *value semantics* the config
  validator must reproduce for format-behavior parity — renaming would lose the
  critical "this is Python's rules, not Go's" signal.

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
