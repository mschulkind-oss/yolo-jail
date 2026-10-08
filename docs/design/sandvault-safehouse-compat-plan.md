---
status: draft
stage: SKETCH
next: "Wait for OQ-NB1, OQ-NB2, OQ-NB3 and OQ-NB5, then complete the jail-only sketch against the tree with the implementation-plan skill"
depends-on: [sandvault-safehouse-compat.md#OQ-NB1, sandvault-safehouse-compat.md#OQ-NB2, sandvault-safehouse-compat.md#OQ-NB3, sandvault-safehouse-compat.md#OQ-NB5]
tags: [sandvault, safehouse, import, compatibility]
---

# SandVault and Safehouse import — implementation sketch

**Status:** 2026-10-08 — incomplete, and unstable while questions are open. Do not build from it.
Rechecked after the second design review; no implementation or native measurements performed.

The design wins on behavior: [`sandvault-safehouse-compat.md`](sandvault-safehouse-compat.md).
This file only holds settled material that is below the design's level. The command surface
waits on [OQ-NB1](sandvault-safehouse-compat.md#OQ-NB1); SandVault import scope on
[OQ-NB2](sandvault-safehouse-compat.md#OQ-NB2); credential inputs on
[OQ-NB3](sandvault-safehouse-compat.md#OQ-NB3); and trial state/home placement on
[OQ-NB5](sandvault-safehouse-compat.md#OQ-NB5). Guest migration and host-notch consumption
are outside the bounded jail-only work, not extra behavior for the builder to choose.

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
  (`config.WorkspaceFilePath`) and its resolver. Use the common user-scope reader in
  [`userlayer.go`](../../internal/config/userlayer.go), with an explicit jail/inspection context
  for the imported layer ([NB-D14](sandvault-safehouse-compat.md#NB-D14)), not an ambient
  addition consumed by every host reader. Cover `config.LoadConfig`, `config.LoadPacks` and
  `config.LoadRWMounts` ([`mounts.go`](../../internal/config/mounts.go)): their existing
  user-scope reads otherwise see none of the imported packs or read-write mounts. Cover
  `validateMountScope`'s provenance (an imported read-write element is user scope), `yolo check`,
  `describe` and `config dump`. Each supported production call site gets a test that fails when
  it is deleted. `yolo host --` and `yolo host apply` must not consume this layer; test both
  exclusions. Nested inherited scope must leave it out, following the switch file's
  `LoadConfigWithoutWorkspaceFile` exclusion, without losing other user scope.
- **The trial's in-memory layer** uses the same jail/inspection-context readers as the import,
  at `--user-layer` precedence, not a second merge; it is absent from host-notch and nested
  inputs too.
- **Required source locks** ([NB-D17](sandvault-safehouse-compat.md#NB-D17)) come from the
  translator result or imported provenance, never the composed `workspace_readonly` alone.
  After all composition, union them into the effective list before validation and mount
  delivery. Today's [`mergeConfig`](../../internal/config/load.go#L175-L198) allows a `null`
  override to erase a list; [`validateWorkspaceReadonly`](../../internal/config/validate.go#L371-L375)
  accepts null, and [`workspaceReadonlyMountArgs`](../../internal/cli/run/mounts.go#L22-L25)
  then returns with no locks. Production-path cases must cover `null`, `[]` and extra locks
  from workspace configuration, its local file and includes, for trial, import preflight and
  later imported launches. Assert the delivered read-only paths, not merely translator output.
  A missing/escaping lock target or an unsupported/unknown read-only capability must refuse,
  not warn and skip. Apple Container uses the existing 1.1.0 floor; native enforcement remains
  a separate acceptance requirement.
- **Guest refusal** comes before writing or launching, even if `macos-user` preflight passes.
  Cover explicit `--at guest` and runtime/config selection. Do not add ACLs or accept a copied
  workspace outside the original SandVault shared area to make tests pass. Guest parity and a
  provenance-preserving migration need separate work, as
  [the design's backend gate](sandvault-safehouse-compat.md#7-per-backend-support) says.
- **Safehouse trust fixtures** must exercise the full
  [ordered resolution](sandvault-safehouse-compat.md#44-safehouse-trust-is-a-precedence-rule-not-an-or):
  explicit false over a stored trust entry; empty, mixed-case and whitespace-padded booleans;
  invalid effective environment values refusing; higher-priority CLI bypass; the conflicting
  true/false flags; and `--always-trust-workdir-config=false` removing this run's stored trust
  without writing Safehouse's file.
- **Policy-only argv**: upstream
  [`safehouse_main`](https://github.com/eugene1g/agent-safehouse/blob/398d67ff25c3d85daf2f7bc57ed34f59dd1dcacd/bin/lib/commands/main.sh#L26-L31)
  never executes with `--stdout` or without a wrapped command. Test copied `--stdout` before
  the command (also with `--explain` and `--output`) refusing before any launch/import write;
  a `--stdout` after the first positional command is the command's argument, not Safehouse's
  flag. A policy-only copied list cannot invoke a default trial command. Cover `--explain`
  alone separately: it does not mean no execution. The refusal points at the import's
  non-executing `--dry-run` report; no SBPL output is implemented here.
- **The empty-packs pointer** ([OQ-NB1](sandvault-safehouse-compat.md#OQ-NB1) B) lands in
  `(*Options).warnIfNoPacks` in `internal/cli/run/run.go`, and the `yolo check` Packs section
  shares its text (`config.NoPacksGuidance`).
- **Fixtures.** The detector's tests need fake trees for both tools (a marker file, a fake `dscl`
  answer, a `.safehouse`, a `trusted-workdirs`) and never a real account. P2 also means no test
  runs a real `sv` or `safehouse`.
