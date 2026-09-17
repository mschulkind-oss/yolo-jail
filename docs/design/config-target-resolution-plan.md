---
title: "Config target resolution — implementation sketch"
date: 2026-09-16
status: draft
tags: [plan, sketch, config, cli]
summary: "The parking lot for implementation material belonging to config-target-resolution.md: where the code is, what to reuse, the seams tests depend on, and the traps. Not a hand-off artifact while it is stamped SKETCH."
vantage:
  status-chip: true
---

# Config target resolution — implementation sketch

**Status:** SKETCH, 2026-09-16 — incomplete, and unstable while questions are open.

**Reads with:** [`config-target-resolution.md`](config-target-resolution.md) — the design, which
**wins on behavior**. Nothing here decides anything; an entry that would choose behavior is an
`OQ-CR` there instead. No agent should build from this file while it carries this status: a real
implementation plan is written by someone who has just read the tree, and this is a parking lot.

---

## 1. Where the code is

| Thing | File |
| :--- | :--- |
| The two predicates, and the four hand-built store paths | [`internal/cli/configls.go`](../../internal/cli/configls.go) (`workspaceRoot`, `surfacesAreLocal`, `prismOverlayPath`, `prismLastRenderPath`, `prismProvenancePath`, `prismSidecarDir`, `hostProvenancePath`, `composedFileExists`, `overlayKeyCount`) |
| `diff` / `reset` / `capture`, the write guard, both truncations | [`internal/cli/configdiff.go`](../../internal/cli/configdiff.go) |
| `render`'s composition and `expandHome` | [`internal/cli/config.go`](../../internal/cli/config.go) |
| `promote`'s host-only refusal | [`internal/cli/configpromote.go`](../../internal/cli/configpromote.go) |
| The write-side target this mirrors | [`internal/render/target.go`](../../internal/render/target.go) (`Kind`, `Host`, `Jail`, `SidecarDir`, `OverlayPath`, `LastRenderPath`, `ProvenancePath`, `SidecarFileMode`, `ArchivePath`) |
| `--at` parsing to share | `yolo apply`'s notch flag, [`internal/cli/apply.go`](../../internal/cli/apply.go) |

## 2. What to reuse rather than rebuild

- **`render.Target` for every store path.** It already answers `SidecarDir`, the three file
  names, the mode and the provenance dir per notch, and `reset` already goes through it. The
  four `prism*` twins are the second implementation to delete.
- **`config.HostManagementDeclared`** for the ownership half — not a raw config read; it gives
  absent and unreadable deliberately different answers.
- **`paths.WorkspaceHomeState(ws)`** for a workspace's host-side home root. Do not spell
  `.yolo/home` again; its docstring says why.
- **`packload.WritableDirs(packs)` + `strings.TrimPrefix(dir, ".")`** is the `~/.claude` →
  `<ws>/.yolo/home/claude` mapping, and `prepareWsState`
  ([`internal/cli/run/prepare.go`](../../internal/cli/run/prepare.go)) is the writer that
  defines it. `paths.HomeFileRedirects()` covers the three home-ROOT files that live inside a
  per-workspace dir (`~/.claude.json` → `claude/claude.json`). Both are needed for a workspace
  target's home root, and neither is derivable — `paths.HomeSurface`'s docstring says so of its
  own three.
  *Blocked on [OQ-CR4](config-target-resolution.md#oq-cr4) — only a writing target needs the
  mapping; a reading one needs only the store.*
- **`Surface.HostSource` / `packload.SurfaceHostFile`** for the `host` layer, with
  `packload.ParseHostLayerReport` for the disposition. `entrypoint.hostSurfaceBytes` is the
  reference reader.
  *Blocked on [OQ-CR6](config-target-resolution.md#oq-cr6).*

## 3. The seams tests depend on

`surfacesAreLocal`, `prismSidecarDir`, `hostProvenancePath` and `hostCaptureDir` are package
vars **because tests stub them** — a bare runner has neither `YOLO_VERSION` nor a `/workspace`
mount, and without the seam an in-jail `go test` would read (and `reset` would delete) the real
`/workspace` sidecars. Four test files stub one of them today, plus `withLocalSurfaces`
([`configls_test.go:33`](../../internal/cli/configls_test.go)) used by three.

Whatever replaces them keeps an equivalent seam, or ~a dozen tests change shape in the same
commit as the refactor and stop pinning what they pinned.

## 4. Traps

- **The callee/call-site rule.** A test that resolves a target directly and asserts its fields
  passes with the resolution call deleted from every verb. The test that counts is the one that
  runs a verb and reads its disclosure line. [`AGENTS.md`](../../AGENTS.md) names five shipped
  instances of this shape.
- **`t.TempDir()` is a symlink on darwin** (`/var/folders/…` → `/private/var/…`), and this
  design walks the cwd upward. Resolve where the path is MINTED — a fixture that hands out
  `EvalSymlinks(t.TempDir())` — or the comparison passes on Linux and fails on
  `check-macos`. Reproduce locally with `TMPDIR=/tmp/link go test -short ./...`.
- **`expandHome` has callers that are not surface paths.** Do not rewrite it to take a target;
  give the surface-path call sites the target and leave the helper alone.
- **The disclosure must be teed like everything else.** The launcher's output goes to
  `<workspace>/.yolo/launch.log`; a config verb writes to the caller's stdout and has no such
  file. Nothing to do — noted so nobody invents one.
- **`config drift` and `config dump` are workspace-config verbs, not surface verbs.** They
  share `workspaceRoot()` and nothing else. Changing the marker test
  ([OQ-CR2](config-target-resolution.md#oq-cr2)) changes what they resolve too; decide whether
  they take the same refusal or keep the cwd walk.

## 5. Tests worth writing (shapes, not names)

- Two cwds, one jail, one assertion: the verb's disclosure line names a different workspace and
  the reports differ **for a stated reason**. This is [F2](config-target-resolution.md#23-the-four-failures)
  as a regression test.
- An owned host fixture (`hostResetFixture`'s shape,
  [`internal/cli/hostownedreset_test.go`](../../internal/cli/hostownedreset_test.go)) where
  `diff` must report the key `reset` would discard. Today's probe of this returns *"No captured
  in-jail edits"* — that string is the red state.
- `$HOME`-as-a-workspace: a fixture home containing `.yolo/bin` and nothing else must not
  resolve as a workspace.
- `--at guest` refused by name, at every verb, sharing `apply`'s vocabulary.
- A preview whose `host` layer is unreachable reports the layer, not silence
  (*blocked on [OQ-CR6](config-target-resolution.md#oq-cr6)*).

## 6. Sequencing notes below the design's altitude

- Step 1 of [§8](config-target-resolution.md#8-what-i-would-build-in-order) is a pure refactor
  and should land alone, with the existing tests unchanged except for the seam.
- The disclosure line's exact wording is not a design decision; match the launch's
  `Flake source: <path> (<what selected it>)` shape and let review word it.
- `yolo config-ref` documents config-FILE keys only, so `--at` on a read verb is documented in
  `yolo config --help` and nowhere else — the `YOLO_*`-style rule from
  [`AGENTS.md`](../../AGENTS.md): document it where it is enforced.
