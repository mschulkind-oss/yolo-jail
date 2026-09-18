---
title: "Sketch: workspace skills"
date: 2026-09-17
status: draft
tags: [plan, sketch, skills, packs, workspace, implementation]
summary: "Implementation material parked beside workspace-skills.md while its six questions are open: the declaration a pack would carry, the change points in the skills staging, the symlink walk that must not reuse the existing copier, the links mechanism's git plumbing, the tests that pin call sites, and the corpus edits. Not a hand-off; nothing here decides behaviour."
vantage:
  status-chip: true
---

# Sketch: workspace skills

**Status:** SKETCH, 2026-09-17 — incomplete, and unstable while questions are open. Nothing is
built and nobody should build from this.

**Design:** [`workspace-skills.md`](workspace-skills.md). The design wins on behaviour; this
file holds the settled-but-boring material and the tree facts an implementer will want, and
every entry that rests on an unruled question names it. It becomes a hand-off only through
the `implementation-plan` skill, against the tree, after the rulings land.

**Precedence.** The design wins on behaviour. The tree wins on fact — a symbol named below that
has moved is followed, and the commit says so.

## The declaration

Blocked on [OQ-WS3](workspace-skills.md#OQ-WS3) — if the source set is one conventional dir,
none of this is needed.

- **Where it lives:** the agent pack's existing `skills` contribution
  ([`internal/packdecl/contributes.go`](../../internal/packdecl/contributes.go)), beside
  `into` and `agent`. A new list field naming project-relative directories, in the agent's own
  precedence order. Name to be chosen at build time; the design fixes only the semantics.
- **The strict-decoder trap.** The pack manifest decoder refuses unknown fields, and the
  entrypoint reads the same `pack.json` in-jail. Adding `tier` to `skills` once made every jail
  refuse to start when a newer staged tree met an older baked entrypoint
  ([`internal/packdecl/packdecl.go`](../../internal/packdecl/packdecl.go), the `Tier`
  tombstone's comment). The entrypoint is mounted from the same tree since 2026-09-06, so the
  skew class is smaller than it was — check the source-skew gate still covers `packs/` before
  relying on that.
- **What each shipped pack would declare**, from the binaries installed in this jail on
  2026-09-17 ([design §2.1](workspace-skills.md#21-where-each-agent-reads-skills--measured)):

  | Pack | Declares | Confidence |
  | :--- | :--- | :--- |
  | `claude` | `.claude/skills` | verified in 2.1.275 |
  | `copilot` | `.github/skills`, `.agents/skills`, `.claude/skills` | verified in 1.0.48, and matches its in-app help text |
  | `codex` | `.codex/skills` (+ `.agents/skills` if confirmed) | `.codex/skills` verified in 0.145.0; `.agents/skills` unconfirmed |
  | `opencode` | `.opencode/skills`, `.claude/skills`, `.agents/skills` | strings in 1.18.31 |
  | `pi` | `.pi/skills` | verified in 0.85.1 (`CONFIG_DIR_NAME` + `skills`) |
  | `agy` | `.agents/skills` | strings in 1.1.7 |
  | `omp` | unknown — not installed here | needs a bundle |

- **The probe test** pins the strings only, never runs an agent (the repo's "no agent tests"
  rule: `--version` probes at most).

## Mechanism A — change points

Blocked on [OQ-WS1](workspace-skills.md#OQ-WS1), [OQ-WS2](workspace-skills.md#OQ-WS2) (layer
position) and [OQ-WS3](workspace-skills.md#OQ-WS3) (source set).

- **Where the sources are set:** [`internal/cli/run/packs.go`](../../internal/cli/run/packs.go)
  calls `jailcontent.SetPackSkillDirs` with the per-pack sources;
  [`internal/cli/run/prepare.go`](../../internal/cli/run/prepare.go) sets the targets. The
  workspace source joins the same list — but see the walk below, because `PackSkillSource`
  today means "copy with dereference", which the workspace source must not.
- **Where the layers are copied:** `PrepareSkills` in
  [`internal/jailcontent/skills.go`](../../internal/jailcontent/skills.go): built-ins first,
  then `packSkillDirs` in order. A lowest-layer workspace source (the leaning) is written
  *before* the built-ins; positions (b) and (c) of the question land at other points of that
  loop. The audience filter (`sourceAddressesAgent`) is where the skip rule attaches: a
  destination whose pack declares the source dir among its own project paths is skipped.
- **The walk (P5) is new code, not `copySkillSubdirs`.** `copySkillSubdirs` stats through a
  symlinked source and `copyFileDeref` opens the link target; both are correct for the local
  pack and wrong for a clone. The rule to copy is
  [`internal/packstage/packstage.go`](../../internal/packstage/packstage.go)'s rule 1 — *"NO
  ESCAPE. A symlink pointing outside the pack root is refused"* — resolved with `EvalSymlinks`
  so a chain cannot smuggle an escape past a single check. Root = the workspace root as the
  launcher resolved it. A refused entry is skipped and named; it never fails the launch (a
  clone must not be able to refuse a jail — the design states this; it is not a knob).
- **Inode preservation** is already the contract: clear *inside* the staging dir, never
  recreate it. Unchanged; just do not break it.
- **Dedup by destination** in [`internal/cli/run/assemble.go`](../../internal/cli/run/assemble.go)
  is unaffected — the workspace source is merged into existing staging dirs, and no new mount
  is emitted.
- **`macos-user`:** the composed content tree the sidecar receives is built from the same
  `PrepareSkills` output; nothing backend-specific should be needed. Verify on a Mac; a Linux
  jail cannot see that backend run.
- **The disclosure line** (one per shadowed or refused entry, once per run) goes through the
  report-tier vocabulary the launch already uses
  ([`../reference/report-tiers.md`](../reference/report-tiers.md)); the existing
  `*disclosure.go` files under `internal/cli/run/` are the shape to copy. Wording is the
  implementer's.

## Mechanism B — change points

Blocked on [OQ-WS4](workspace-skills.md#OQ-WS4) (is B built at all in containers),
[OQ-WS5](workspace-skills.md#OQ-WS5) (host in scope) and [OQ-WS6](workspace-skills.md#OQ-WS6)
(the ignore route).

- **Where it runs:** launcher-side, before the container starts, and on the
  `yolo host -- <agent>` exec path in [`internal/cli/host.go`](../../internal/cli/host.go).
  Never on `yolo host apply`.
- **Link target:** relative, from the link's parent to the source dir, so a committed copy
  would still resolve for another reader.
- **The four "already there" cases** and the ownership record: the record lives under
  `<ws>/.yolo/` (already `*`-ignored), maps link path → target yolo wrote; a link not in the
  record is never touched. Removal when the declaring pack leaves `packs` reads the record.
- **`.gitignore` append precedent:** `appendFile` and the contains-check in
  [`internal/cli/init.go`](../../internal/cli/init.go); the write-once rule and its test in
  [`internal/paths/workspacestateignore_test.go`](../../internal/paths/workspacestateignore_test.go)
  ("A .gitignore the user has edited is theirs. yolo writes once and never again").
- **`.git/info/exclude` path:** never spelled; `git rev-parse --git-path info/exclude` handles
  worktrees and submodules (where `.git` is a file and the exclude lives in the common dir).
  If `workspace_readonly` has locked `.git/info`, the write fails and the fallback is whatever
  [OQ-WS6](workspace-skills.md#OQ-WS6) names.
- **Symlink precedent in the tree:** [`internal/openaiauthhost/host.go`](../../internal/openaiauthhost/host.go)
  symlinks `AGENTS.md` and `skills` from the ordinary `~/.codex` into a managed `CODEX_HOME` —
  the same shape (link a dir the agent reads at a dir it does not), a different root.

## Tests — the call-site rule

`AGENTS.md`'s standing warning applies here word for word: a test that pins the helper while
the call site is unpinned is not a test. Each of these fails if the call site is deleted:

- **P5:** a workspace fixture carrying `.agents/skills/x/SKILL.md → <file under $HOME>`; drive
  the real staging pass; assert the staging dir contains no `x`, and that the launch output
  names the refused link. Also a chain (`link → link → outside`) and a dangling link.
- **The skip rule:** `packs: ["claude", "pi"]`, a fixture with only `.claude/skills/review/`;
  assert `pi`'s staging dir has `review` and `claude`'s does not.
- **The layer position:** a fixture shadowing a built-in by name; assert the built-in's bytes
  win under the leaning and the shadowed name is disclosed.
- **Attach refresh:** the second invocation against the same jail re-stages a changed workspace
  skill (the existing inode-preserving contract, extended to the new source).
- **B, if built:** `git status --porcelain` after a launch equals what the ruled ignore route
  predicts; a repo whose `.codex/skills` is a real dir is untouched; a link yolo wrote is
  removed when the pack leaves `packs`, and a user-committed link is not.
- **Darwin path class:** any fixture path compared against `EvalSymlinks` output must itself
  be minted through `EvalSymlinks(t.TempDir())` — `/var/folders` is a symlink on macOS.

## Corpus edits the build owes

- [`../reference/agent-briefings.md`](../reference/agent-briefings.md#skills) — the Skills
  section gains the workspace layer and the skip rule; its warning about the deleted third
  layer stays, and should say why a workspace source is not that layer.
- [`../reference/pack-system.md`](../reference/pack-system.md#skills) — the `skills` kind's
  first sentence still reads *"built-in < pack < the user's own tree"*, which predates the
  local-pack ruling; fix that drift while adding the new field.
- [`../reference/jail-home.md`](../reference/jail-home.md) — the skills bullet still says the
  merge has "the user's own host skills" as its third source; same drift.
- [`../plans/setup-support-gaps.md`](../plans/setup-support-gaps.md) — G32's row and G14's
  row both touch this; re-read both.
- `AGENTS.md` — the skill-priority sentence ("built-in < shared packs < the conventional local
  pack") gains the workspace layer at whatever position [OQ-WS2](workspace-skills.md#OQ-WS2)
  rules.
- [`../plans/agent-config-packs.md`](../plans/agent-config-packs.md#-oq-acp2--whether-opencodes-skills-gap-should-be-closed-by-writing-into-workspace)
  — [`OQ-ACP2`](../plans/agent-config-packs.md#-oq-acp2--whether-opencodes-skills-gap-should-be-closed-by-writing-into-workspace) is closed by [OQ-WS4](workspace-skills.md#OQ-WS4) and
  [OQ-WS5](workspace-skills.md#OQ-WS5); its premise (opencode has no home-scope skills dir) is
  already false and the row should say so.
- The roadmap row is the maintainer's to apply.
