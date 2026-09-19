---
title: "Synced skill trees — implementation sketch"
date: 2026-09-18
status: draft
tags: [plan, sketch, skills, packs, host-notch, claude]
summary: "The parking lot for `synced-skill-trees.md`: the measurement transcript that doc cites, the seams a real implementation-plan will need, and the checks to re-run before building. Not a hand-off artifact — no design decision is made here, and five questions in the design are open."
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

## The enabledPlugins measurement

The fixture behind [§8.2](synced-skill-trees.md#82-what-yolo-does-to-it-today--measured), run
2026-09-19 in this jail against the baked `yolo 0.9.0+45.gd4c0e7e3`, on throwaway homes under
the session scratchpad — **never a live home, and never `$HOME` itself**. Each run is a FRESH
home: the archive is one-per-surface-per-home and the provenance record makes the second apply a
different code path, so re-using a home silently measures something else.

```console
$ mkdir -p $FH/.config/yolo-jail $FH/.claude
$ printf '{ "packs": ["claude"] }\n' > $FH/.config/yolo-jail/config.jsonc
$ cat > $FH/.claude/settings.json <<'EOF'
{ "theme": "dark",
  "enabledPlugins": { "my-own-plugin@mkt": true, "another-plugin@mkt": true },
  "env": { "MY_HAND_WRITTEN": "keepme" } }
EOF
$ cd $FH && HOME=$FH yolo host apply            # dry run: names all three
$ printf 'y\n' | HOME=$FH yolo host apply --assert
```

The four runs, and what each one is for — the last two are the ones that are not obvious:

| Fixture | Posture | Result |
| :--- | :--- | :--- |
| Fresh home, three user entries | `assert` (default) | All three dropped. One-way-door prompt naming each. `enabledPlugins` and `env` both `{}` afterwards. **No archive directory exists** |
| Fresh home, same file | `own` | Same three dropped, same prompt, **plus** `archived your file as yolo found it: …/.local/share/yolo-jail/archive/config/claude-settings/settings.json`. The archive holds the original verbatim |
| A home already applied under `assert`, then switched to `own` with one `enabledPlugins` entry re-added | `assert` → `own` | Dropped with **no prompt at all** — `confirmHostLosses` is first-apply-only — named on the result line after the write. The archive taken here already has `env: {}`, because yolo's earlier `assert` run emptied it: *as yolo found it at adoption*, not *before yolo* |
| Clean `assert` home, no user entries, switched to `own` | `assert` → `own` | No value changed; the file was re-encoded with keys sorted (`theme` first → last). `config-ref`'s *"changes ZERO bytes"* holds at value granularity, not at byte granularity |

**The remedy fixture** — a `config-overlay` in the conventional local pack — is the one that
comes out green, under both postures, with no loss line at all:

```console
$ cat > $FH/.config/yolo-jail/local/pack.json <<'EOF'
{ "name": "local", "contributes": [
  { "kind": "config-overlay", "surface": "claude/settings",
    "config": { "managed": { "enabledPlugins": { "my-own-plugin@mkt": true } } } } ] }
EOF
$ printf 'y\n' | HOME=$FH yolo host apply --assert
Applied: 1 config file.
$ jq -c .enabledPlugins $FH/.claude/settings.json
{"my-own-plugin@mkt":true}
```

**To re-run after row `0b` lands:** the assertion is that run 1 drops NOTHING and prints no loss
line, while yolo's own LSP toggles are still regenerated from the derive — the second half is
what a fix that simply stops dropping would get wrong.

## Two defects this measurement found, neither of them row `0b`

Both are live on the host notch today, both are independent of every `OQ-ST`, and neither is a
design question — recorded here so they are not re-discovered.

- **The dropped-entry remedy names `mcp_servers` for every table.** `mcpEntryRemedy`
  (`internal/cli/hostapplyremedy.go`) is one string for the whole `entry_dropped` class, and the
  boot-side twin (`noteDroppedManagedEntries`, `internal/entrypoint/prism.go`) hardcodes the same
  key in its own sentence. For an `enabledPlugins` or `env` loss the advice is not merely
  unhelpful, it is impossible to follow. The working remedy is the `config-overlay` above, and
  the remedy is per-SURFACE-KEY rather than per-class — which is a declaration the owning pack
  could carry, rather than a switch in the reporter.
- **`yolo pack lint` passes an overlay body the render refuses BY NAME.** `manifest.DecodeOverlay`
  reports two problems for a body carrying `defaults` where `managed` belongs: `defaults` is one
  of `OverlayDTO`'s explicitly-refused fields (declared rather than left to
  `DisallowUnknownFields` precisely so the diagnostic names the rule), and the body then
  contributes no keys. Measured the same day, `yolo pack lint` printed `✓ pack ok` and
  `config-overlay claude/settings  contributes keys (owner still wins)` for exactly that body,
  which then contributed nothing and lost the entry at the next apply. Whatever lint is checking,
  it is not what the render decodes. This matters more than it looks because the overlay IS the
  remedy: a user following the advice above with the wrong spelling gets a green lint and the loss
  anyway.

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
- **The leaf-level record** ([OQ-ST5](synced-skill-trees.md#OQ-ST5), roadmap row `0b`) has two
  consumers and they are in different packages: `dropComputedTables`
  (`internal/agentcfg/staterender.go`) for the adopting branch, and `hostTableLayer` /
  `regenerateManagedTables` (`internal/entrypoint`) for the `rmw` one. A fix that lands in one
  leaves the other posture losing entries — the `assert` half is the DEFAULT and the half with no
  archive, so it is the one to do first, which is the opposite of where the function comment
  points.

## Checks to re-run before building

- `hostskills.Collisions` is enforced structurally before any write and exits non-zero — confirm
  a snapshot cannot slip a name past it ([§5](synced-skill-trees.md#5-naming-and-collisions)
  ruling 4 depends on this).
- `host_files` destination reservation lists do **not** name `.claude/skills`, so a user can
  already aim one there. Nothing cross-references `host_files` destinations against pack-declared
  skills mounts — worth measuring before the design's
  [§9](synced-skill-trees.md#9-alternatives-considered) `host_files` paragraph is relied on
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
