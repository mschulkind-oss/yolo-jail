# Feedback — adopting packs for a real, pre-existing config

**Audience:** an agent or maintainer working on yolo-jail's pack/host-render surface.
**Written:** 2026-08-04, against `0.7.1+380.ga3d6d4e`.
**What this is:** field notes from converting a real hand-tuned agent config into three
packs and applying them to a real `$HOME` — not a jail, not a `/tmp` fixture. Everything
below was hit in practice, in order, and every claim was verified by running the binary.

The migration **succeeded**: 3 packs, 157 files rendered, idempotent on re-apply, and the
headline win landed (5 personal skills went from reaching *one* agent to reaching *all
four*). This doc is about the friction on the way, ranked by how much damage it can do to
someone who doesn't already know the answer.

Prior handoffs from the same effort:
[`handoff-pack-host-management-gaps.md`](handoff-pack-host-management-gaps.md) (five gaps,
all now closed) and [`handoff-host-mcp-servers.md`](handoff-host-mcp-servers.md).

---

## What was adopted

| Pack | Contents | Claims |
|---|---|---|
| `matt-core` | 5 shared skills + house rules | 4 `skills` + 4 `briefing` (one per agent) |
| `matt-fzf` | an `fd\|fzf` `@`-file finder | 3 `requires`, `files`, `config-overlay`, `briefing` |
| `matt-local` | pi's custom provider, token minter, 6 themes | `requires`, 4 `files`, `briefing` |

The pre-existing config was deployed by **rcm** (`rcup` symlinks `~/.dotfiles/**` into
`~`), which turns out to be the single most important fact about the migration — see F1.

---

## F1 — 🔴 A zero-ceremony pack silently renders NOTHING to the host

**The worst finding, because it fails silently and the docs actively recommend the broken
path.**

`yolo pack --help` and the migration guide both promote zero-ceremony as *the* starting
point: "a skills dir and an AGENTS.md at the pack root are staged as-is", "it works with no
`pack.json` at all." That is true **in a jail**. On the host it renders **zero files**, with
no error, no warning, and a `✓ pack ok` lint.

```console
$ ls ~/.dotfiles/packs/matt-core        # AGENTS.md, README.md, skills/  — no pack.json
$ yolo pack lint ~/.dotfiles/packs/matt-core
✓ pack ok — 27 file(s) stage            # ← looks completely fine

$ yolo pack footprint ~/.dotfiles/packs/matt-core
matt-core
  (no declared claims)                  # ← the only hint, and it reads as benign

$ yolo apply --host --assert
  skills     claude ships none (its contribution names the destination other packs merge into)
  skills     pi ships none  …           # ← nothing from matt-core AT ALL
$ ls ~/.claude/skills
ls: cannot access: No such file or directory
```

Adding an explicit manifest fixed it — 8 claims, 5 skills × 4 agents:

```jsonc
{ "name": "matt-core", "contributes": [
  { "kind": "skills",   "from": "skills",    "into": ".claude/skills" },
  { "kind": "skills",   "from": "skills",    "into": ".pi/agent/skills" },
  { "kind": "skills",   "from": "skills",    "into": ".codex/skills" },
  { "kind": "skills",   "from": "skills",    "into": ".gemini/antigravity-cli/skills" },
  { "kind": "briefing", "from": "AGENTS.md", "into": ".claude/CLAUDE.md" },
  … one briefing per agent … ] }
```

**Why it happens:** in a jail, staging walks the pack tree and merges `skills/` into every
selected agent's staging dir, so a destination is *inferred*. The host renderer iterates
**declared contributions** only, so a pack with none contributes nothing.

**Suggested fixes, in preference order:**
1. **Make zero-ceremony work at both notches** — infer the same per-agent destinations the
   jail already infers. This is the fix that matches the documented promise.
2. If (1) is rejected, **say so at the point of failure**: `apply --host` should print
   `matt-core  skipped: zero-ceremony pack declares no destinations, which the host render
   requires — add a skills/briefing contribution to pack.json`. A pack that contributes
   nothing anywhere is *always* a mistake; it should never be silent.
3. Change `(no declared claims)` to something that reads as a problem, and have `pack lint`
   warn when a pack stages files but declares nothing.

Note this interacts badly with the "never silent" discipline the G1 fix established for
`skills`/`briefing`. That fix made *declared-but-unimplemented* kinds loud. This is the
*undeclared* case, and it's still mute.

---

## F2 — 🔴 Migrating an rcm-managed config breaks the live home, and packs stay inert

This is the one that actually broke a working machine mid-migration, and the interaction is
subtle enough to deserve a documented recipe.

Sequence, all reasonable, ending in a broken state:

1. `git mv claude/skills packs/matt-core/skills` — the pack must own real files, since a
   pack **refuses symlinks pointing outside itself** (correct rule).
2. That instantly leaves **34 dangling symlinks** in the live home (`~/.claude/skills/**`,
   9 × `~/.pi/agent/*`), because rcm had deployed the old paths. One of them was
   `fileSuggestion`'s target, so Claude Code's `@`-completion was **broken** from this
   moment until the fix.
3. `yolo apply --host --assert` does **not** repair it:

```
skills  agent-standards  skipped (yours)  exists and yolo has no record of writing it — left untouched
```

**The refusal is right** — never clobber a user's file — but a *dangling symlink* is
indistinguishable to it from precious user content, so the pack is **permanently inert**
until someone deletes the stale tree by hand. The message says "left untouched", which
reads like a safe no-op rather than "your pack will never work."

**Suggested fixes:**
- **Detect a dangling symlink specifically** and treat it as absent (or offer to replace
  it): `skills  agent-standards  replaced a DANGLING symlink (was → …/claude/skills/…,
  which no longer exists)`. A broken link is not user content by any reading.
- Document the rcm/stow/chezmoi migration recipe: *move the files, delete the old deployed
  symlinks, then apply.* Any dotfile manager that symlinks into `$HOME` hits this, which is
  most of them.
- Consider a `yolo apply --host --doctor` that reports destinations blocked by
  `skipped (yours)`, so "why is my pack doing nothing" is one command.

---

## F3 — 🟠 Briefings duplicate on first apply against an existing file

`briefing` appends a delimited managed block. If the user's briefing **already contains**
the prose they just moved into a pack — the overwhelmingly likely case when migrating
existing config — the first apply produces it twice:

```console
$ grep -c '^# Agent Developer Guide' ~/.claude/CLAUDE.md
2
```

The block itself is correctly delimited and idempotent on re-apply, so this is a *first
contact* problem, not a drift problem. But the user has to notice, and the natural
assumption is that the pack somehow ran twice.

**Suggested fix:** on the first render into a non-empty briefing, if the pack's prose is
already present verbatim outside the markers, either adopt it (wrap it in the markers) or
warn: `⚠ your briefing already contains this pack's prose — it now appears twice; delete
the hand-written copy, the pack owns it now`. Same class as the `⚠ would overwrite your
existing value` warning that already exists for config keys, and that warning is exactly
what made the settings side of this migration painless by comparison.

---

## F4 — 🟠 `permissions.defaultMode` cannot be set at the host, and the docs imply it can

The guide's Part 2 story is "declare what you want, render it wherever." For this one key
that isn't true, and it's a key many people will reach for first.

Verified: with `claude` plus **two** overlay packs (one setting
`permissions.defaultMode: acceptEdits`), the host render lands `default`:

```console
$ yolo apply --host --assert
  claude/settings  rendered
    config-overlay keys from: p1, p2 (below this surface's own managed layer, which still wins a conflict)
$ # → permissions.defaultMode: "default"     (NOT acceptEdits)
```

The mechanism is right — the guarded `autonomy` posture *should* own the jail-bypass keys,
and prompts-on is correct for a machine with no container boundary. The problem is
discoverability: the overlay is accepted, listed as contributing, and then silently loses.
The `⚠ overwrote your existing value for: permissions.defaultMode` warning fires, which
makes it look like the overlay *won*.

**Suggested fix:** when an overlay key is outranked by the owner's managed layer, name it:
`config-overlay keys from: p2 — permissions.defaultMode IGNORED (owned by claude's guarded
autonomy posture)`. Cheap, and it turns a silent loss into a documented policy.

---

## F5 — 🟡 `pack lint` refuses a pure-`files` pack, with wrong reasoning

```
✗ pack has neither a skills/ dir nor an AGENTS.md — it would stage files nothing reads
```

Hit while building `matt-local`, whose entire purpose is a `files` tree plus a
`config-overlay`. The gate (`internal/cli/pack.go:323`) keys strictly on a staged `skills/`
prefix or a physical root `AGENTS.md`/`CLAUDE.md`; a `files` + `briefing` manifest doesn't
satisfy it, and a `briefing` *declaration* doesn't either — the root file must exist.

"would stage files nothing reads" is simply false for a `files` tree: pi reads
`models.json` and the themes; Claude Code execs the finder script. The check should account
for `files` / `config` / `config-overlay` contributions, or at minimum reword to name what
it actually requires.

(Workaround was fine — both packs got a genuinely useful `AGENTS.md` warning the agent
those paths are read-only mounts. But that was luck, not design.)

---

## F6 — 🟡 Two smaller inconsistencies

1. **Observe mode reports skills in the past tense.** Every other kind uses future tense in
   a dry-run (`would render`, `⚠ would overwrite`); skills say
   `skills  agent-standards  rendered  invoke as /agent-standards`. Verified it writes
   nothing — but it reads as though the dry-run mutated the home, which is precisely the
   fear a dry-run exists to allay.
2. **`yolo pack footprint` has no `--allow-exec`.** `pack lint --allow-exec <dir>` accepts a
   pack shipping an executable; `pack footprint <dir>` on the same pack exits 1 with the
   exec-bit refusal. So a pack you *can* lint you *cannot* inspect — and `footprint`'s own
   help advertises it as the way to inspect a pack you're authoring.

---

## What worked well (so it doesn't get refactored away)

Genuinely good, and load-bearing for trusting `--assert` on a real home:

- **Per-item reporting with archival.** `rendered` / `unchanged` / `skipped (yours)` /
  `archived to <path>` per skill. Overwritten content goes to
  `~/.local/share/yolo-jail/archive/skills/<timestamp>/<pack>/<name>` rather than being
  destroyed. This is what made applying to a real home feel safe rather than reckless.
- **Dropping a skill from a pack Just Works.** Remove it from the pack, re-apply, and yolo
  archives and removes the host copy, reporting it by name:
  `skills  goaway  archived  moved to …/archive/skills/…`. No stale file, no manual
  cleanup. Verified directly — this is the single biggest reason the pack model beats a
  symlink farm for content that churns.
- **`requires` is the right kind.** Adopting it for `fd`/`fzf`/`jq` replaced a workaround
  with a declaration: asserts presence, generates nothing that can shadow a baked binary,
  and feeds `check-deps` (which now exits 0 on a host that has them).
- **The `⚠ overwrote your existing value for: <key>` warning**, in observe *and* assert. It
  is why the settings half of this migration was uneventful, and it's the model F3/F4 above
  should follow.
- **`config-overlay` + one-owner-per-surface.** Two packs contributed to `claude/settings`
  with no collision and clear provenance. The mechanical refusal of a second `config`
  declaration means the old silent-mode-flip hazard is now unreachable rather than merely
  discouraged.

---

## The one-paragraph version

The pack model is sound and the host render is genuinely usable on a real machine. The
friction is concentrated in **first contact with a pre-existing config**: zero-ceremony
silently no-ops on the host (F1), migrating from a symlink-based dotfile manager leaves
dangling links that make packs permanently inert while reporting a reassuring
`skipped (yours)` (F2), and briefings double up (F3). All three are *first-run* problems
that a one-time user hits exactly when they have the least context — and all three are
already-silent cases in a codebase whose stated discipline is "never silent."
