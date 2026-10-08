---
status: draft
stage: SKETCH
next: "Wait for OQ-NB1 and OQ-NB2, then complete this against the tree with the implementation-plan skill"
depends-on: [sandvault-safehouse-compat.md#OQ-NB1, sandvault-safehouse-compat.md#OQ-NB2]
tags: [sandvault, safehouse, import, compatibility]
---

# SandVault and Safehouse import — implementation sketch

**Status:** 2026-10-08 — incomplete, and unstable while questions are open. Do not build from it.

The design wins on behavior: [`sandvault-safehouse-compat.md`](sandvault-safehouse-compat.md).
This file only holds settled material that is below the design's level.

## Notes to check before relying on them

- **Single-file grants.** Safehouse's `--add-dirs-ro=~/.gitignore` grants a single file. Check
  whether a read-only string `mounts` entry naming a file binds on podman and Apple Container, and
  what `macos-user` does with one (the user guide says a *pack's* single-file `mount` is copied in
  at each launch). If it does not bind, the translator should emit a `host_files` entry for a file.
- **Version reading.** SandVault's `sv` holds `readonly VERSION="1.32.0"`. Under Homebrew, `sv`
  resolves through `/opt/homebrew/opt/sandvault/...`. Safehouse's `dist/safehouse.sh` carries
  its version in a bootstrap constant. Read both as text and never run either one
  ([NB-D3](sandvault-safehouse-compat.md#NB-D3)).
- **`SANDVAULT_ARGS` quoting.** `sv` splits it with `xargs -n1`. A Go port of that quoting must
  match `xargs` on single quotes, double quotes and backslashes; a table test from `sv`'s own
  README examples is the acceptance check.
- **Safehouse's `.safehouse` grammar** fails on a malformed line or unknown key. The translator
  should do the same: report the file as unreadable rather than translating part of it.
- **The loader for `.imported.jsonc`** reuses the per-workspace file's name stem
  (`config.WorkspaceFilePath`) and its resolver; the merge position is
  [NB-D14](sandvault-safehouse-compat.md#NB-D14). Every reader that composes user scope
  (`LoadConfig`, `yolo check`, `config dump`) must read it, and the in-jail inherited scope must
  leave it out, as `LoadConfigWithoutWorkspaceFile` does for the switch file.
- **The empty-packs pointer** ([OQ-NB1](sandvault-safehouse-compat.md#OQ-NB1) C) lands in
  `(*Options).warnIfNoPacks` in `internal/cli/run/run.go`, and the `yolo check` Packs section
  shares its text (`config.NoPacksGuidance`).
- **Fixtures.** The detector's tests need fake trees for both tools (a marker file, a fake `dscl`
  answer, a `.safehouse`, a `trusted-workdirs`) and never a real account. P2 also means no test
  runs a real `sv` or `safehouse`.
