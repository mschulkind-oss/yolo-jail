---
title: "Synced skill trees — implementation sketch"
date: 2026-09-18
status: draft
tags: [plan, sketch, skills, packs, host-notch, claude]
summary: "The parking lot for `synced-skill-trees.md`: the measurement transcript that doc cites, the seams a real implementation-plan will need, and the checks to re-run before building. Not a hand-off artifact — no design decision is made here, and four questions in the design are open."
vantage:
  status-chip: true
---

# Synced skill trees — implementation sketch

**Status:** SKETCH, 2026-09-18 — incomplete, and unstable while questions are open.

> [!WARNING]
> **Do not build from this.** It is a parking lot that keeps
> [`synced-skill-trees.md`](synced-skill-trees.md) at altitude. A real hand-off needs
> [`implementation-plan`](../plans/README.md) run against the tree by an agent that has just
> read it. **The design wins on behaviour**: where this file and
> [`synced-skill-trees.md`](synced-skill-trees.md) disagree, the design is right and this is
> stale.

## The measurement

The transcript [§3.2](synced-skill-trees.md#32-on-the-host-yolo-eats-it--measured) compresses.
Run on 2026-09-18 in this jail, against `yolo-jail 0.9.0+45.gd4c0e7e3`, with a throwaway home —
never the live one.

Fixture:

```console
$ FH=$SCRATCH/fakehome
$ B=de3d6732-aa3b-4500-a6ab-a20b5e5ca4c8_29eb2395-06b1-4ccc-91f6-7c9df9f218f1
$ mkdir -p $FH/.config/yolo-jail \
    $FH/.claude/skills/synced/$B/{pdf-tools,brand-voice} \
    $FH/.claude/skills/synced/.trash \
    $FH/.claude/skills/my-own-skill
$ # a SKILL.md in each of the three skill dirs
$ printf '{ "packs": ["claude"] }\n' > $FH/.config/yolo-jail/config.jsonc
```

Run 1, dry run — `synced` is listed as an adoption beside the hand-written skill:

```text
⚠ yolo COMPOSES these skills directories wholesale, and they currently hold 2 skill(s) yolo did not write:
  …/.claude/skills/my-own-skill
  …/.claude/skills/synced
  skills  my-own-skill  would move to your local pack
  skills  synced        would move to your local pack
  ⚠ 2 skills in your agent skill dirs are yours, not yolo's, and would move into your local pack: my-own-skill, synced
```

Run 2, `--assert` with `y` on the prompt — `Applied: 1 config file, 2 skills moved into your
local pack.` Afterwards `~/.config/yolo-jail/local/skills/synced/` holds the bucket **and**
`.trash`, and `~/.claude/skills/synced/` holds a composed copy. The two trees are
byte-identical, which is the whole reason this is invisible.

Run 3 — add `new-from-upstream/` to the bucket, rewrite `pdf-tools/SKILL.md`, apply again:

```text
Applied: 1 composed skill.
```

`new-from-upstream` is gone and `pdf-tools` is back to its pre-edit bytes, in both the
destination and the local pack. **That one output line is the entire notice.**

**To re-run this after a fix:** the assertion is that run 3 leaves both the new skill and the
edit intact, and that run 1 lists `my-own-skill` and *not* `synced`.

## The second measurement: an empty bucket, and the plugins-side twin

Run 2026-09-18 in this jail, after the maintainer reported that the directory came from
`claude plugin install` and regenerates when deleted. Both halves reconcile.

**In this jail's own `~/.claude`** (a per-workspace overlay, a different home from the
maintainer's, sharing only the OAuth identity):

```console
$ ls -a ~/.claude/plugins/synced/
.bucket-de3d6732-…_29eb2395-…      # a zero-byte FILE
de3d6732-…_29eb2395-…/             # an EMPTY directory
$ cat ~/.claude/plugins/installed_plugins.json
{ "version": 2, "plugins": {} }
$ ls ~/.claude/plugins/known_marketplaces.json   # claude-plugins-official, github, lastUpdated
```

Same bucket name as the maintainer reported, on a home that never saw his files, with nothing
ever installed into it. That is the identity-minting claim, measured rather than parsed.

**The exposure measurement**, against a throwaway home carrying exactly that shape under
`skills/` instead of `plugins/`:

```console
$ HOME=$FAKE yolo host apply
  ⚠ 1 skill in your agent skill dirs is yours, not yolo's, and would move into your local pack: synced
```

An **empty** sync root is adopted, because `Adoptions` tests only non-dot-and-directory. The
`.bucket-…` marker is excluded twice over (dot-prefixed, and not a directory).

> [!WARNING]
> **This jail cannot measure the skills-side timing, and an absent `~/.claude/skills/synced`
> here is not evidence.** `/home/agent/.claude/skills` is a `:ro` bind of
> `…/agents/<container>/skills-claude` — yolo's own staging dir — per `/proc/self/mountinfo`, so
> no vendor process could create `synced` under it. Anything about when the skills-side root
> first appears has to be measured on a real host.

## Where the claude.ai facts came from

All read out of `~/.local/share/claude/versions/2.1.275` (an ELF bundle, so `grep -a -o -E`
over it rather than a JS file), never from vendor docs — the lesson
[`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md#n6-new--copilot-reads-claudes-plugin-manifests-and-namespaces-plugin-skills)
records. The load-bearing extract is the bucket-name module: a `synced` constant beside
`.trash` and `.staging`, the UUID regex, a two-argument minter joining its arguments with `_`
and defaulting the second to `unbound`, a parser returning `{org, account}`, and a telemetry
redactor emitting the literal `"<org>_<account>"`.

**Re-verify on a version bump** — this is a private layout with no compatibility promise. The
cheapest probe is the redaction template: if `"<org>_<account>"` is still in the binary, the
naming has not moved.

## Seams a real plan will need

- **The fence's read point** is `hostskills.Adoptions` in
  [`../../internal/hostskills/compose.go`](../../internal/hostskills/compose.go) — the same
  function that already implements four exclusions, each with its reasoning in the doc comment.
  A fifth exclusion is the shape; it needs the destination's declaring pack in scope, which
  `Destination` does not carry today (it carries `Layers`, each with a `Pack`).
- **The declaration** belongs beside `SkillsTier` on the manifest
  ([`../../internal/packdecl/packdecl.go`](../../internal/packdecl/packdecl.go)) — per pack or
  per contribution is an open shape, but note tier went manifest-level for a reason that may
  apply here too.
- **The jail half** is
  [`../../internal/jailcontent/skills.go`](../../internal/jailcontent/skills.go)'s
  `copySkillSubdirs`. Today it is only reachable with a broken home behind it; after the fence
  it should stay unreachable, and a test that pins the CALL SITE rather than the helper is the
  one that would notice.
- **`yolo pack init --from-plugin`** already writes `skills_tier: namespaced` and a `skills`
  contribution into `.claude/skills`, and copies the tree rather than linking it (packstage
  refuses an escaping symlink, so a link would stage into no jail). That is the synced-plugin
  route and it needs no new code — only a pointer from the snapshot's output.
- **`packload.SkillsSourceDir`** is the one resolver for a contribution's `from`, used by all
  three former hardcoding call sites. Anything new that reads a pack's skills source goes
  through it.

## Checks to re-run before building

- `hostskills.Collisions` is enforced structurally before any write and exits non-zero — confirm
  a snapshot cannot slip a name past it ([§5](synced-skill-trees.md#5-naming-and-collisions)
  ruling 4 depends on this).
- `host_files` destination reservation lists do **not** name `.claude/skills`, so a user can
  already aim one there. Nothing cross-references `host_files` destinations against pack-declared
  skills mounts — worth measuring before the design's
  [§8](synced-skill-trees.md#8-alternatives-considered) `host_files` paragraph is relied on
  either way.
- The `:ro` skills mount is not guarded on `roBindsUnsupported`, unlike the pack host-grant
  path — on Apple Container below its read-only floor the staged skills dir is effectively
  writable. Irrelevant to the fence, relevant to any claim that a jail cannot edit its skills.

## Blocked entries

- The snapshot's destination layout, its record file and its command spelling are all blocked on
  [OQ-ST1](synced-skill-trees.md#OQ-ST1) — a dedicated pack and the local pack need different
  records and different removal stories, so writing either down now would be a guess.
- Whether the fence needs a user-scope spelling is blocked on
  [OQ-ST2](synced-skill-trees.md#OQ-ST2).
- Whether anything is emitted on the launch stream is blocked on
  [OQ-ST3](synced-skill-trees.md#OQ-ST3); a launch has no quiet mode, so this is not a line that
  can be added provisionally.
