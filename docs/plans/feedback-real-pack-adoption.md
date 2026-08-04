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

## F3 — ✅ DISSOLVED 2026-08-04 — Briefings duplicated on first apply against an existing file

**Not fixed — dissolved**, by §6a's ruling that `briefing` is generated WHOLESALE. The duplication
was an artifact of the append-based first write; with no append nothing can double, so the
suggested fix below ("adopt the prose into the markers") is moot — the ruling claims that ownership
explicitly and up front, which is the honest version of the same move. The prose is MOVED into the
local pack rather than wrapped in markers, so it still reaches every agent.

Asserted by `TestHostBriefingFirstApplyDoesNotDuplicateProse` (a first apply against a destination
already holding the pack's prose verbatim) and by the CLI-level idempotency test.

**The original report, for the record:**

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

## F5 — ✅ FIXED 2026-08-04 — `pack lint` refused a pure-`files` pack, with wrong reasoning

A `files` + `config-overlay` pack now lints clean; see
[`outstanding-work.md`](outstanding-work.md) §7 for what shipped. The report below is the
original finding.

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
2. ✅ **FIXED 2026-08-04.** ~~**`yolo pack footprint` has no `--allow-exec`.**~~ `pack lint
   --allow-exec <dir>` accepted a pack shipping an executable while `pack footprint <dir>` on
   the same pack exited 1 with the exec-bit refusal. So a pack you *could* lint you *could
   not* inspect — and `footprint`'s own help advertises it as the way to inspect a pack you're
   authoring. `footprint` now takes the same flag; without it the refusal still stands, since
   the flag supplies the consumer's half of the decision rather than removing the gate.

---

## Triage and recommended order (added 2026-08-04)

Every finding above re-verified against the code before ranking. Two are re-ranked, one fix is
partly rejected, and F5 turns out to be the same rule an independent audit hit the same week.

**The unifying diagnosis is the one this doc's own last paragraph makes, and it is right:** all
three 🔴/🟠 first-contact findings are SILENT, in a codebase whose stated discipline is "never
silent." F1's note makes the sharpest form of it — the G1 fix made *declared-but-unimplemented*
kinds loud; these are the *undeclared* and *blocked* cases, and they are still mute. That is a
gap in a principle we thought was closed, not six unrelated papercuts.

| Order | Finding | Why here |
|---|---|---|
| ✅ 1 | **F2 SHIPPED** `8bc562e` — dangling symlinks | The only finding that BROKE a working machine. No design question: `Lstat` says symlink + `Stat` fails ⇒ absent. Cheapest real fix in the list |
| ✅ 2 | **F1 SHIPPED** `18695f5` — zero-ceremony host no-op | Silent, and the fix is already PROVEN by the jail — see below |
| 3 | ~~**F5 + the lint rewrite**~~ | ✅ **DONE 2026-08-04.** One rule, two reports — done together, as advised |
| ✅ 4 | **F4 SHIPPED** `bfd4a1f` — outranked overlay key | One line of output; turns a misleading warning into stated policy |
| ✅ 5 | **F6 SHIPPED** `8bc562e`+`d7478a0` — tense + `--allow-exec` | ✅ `--allow-exec` **DONE 2026-08-04** (shipped with F5, same file). F6.1's past-tense observe output is still open |
| — | **F3** briefing duplication | **DISSOLVED** by the Q4 briefing ruling: wholesale generation means no append, so nothing can double. The ownership decision it needed was made — yolo owns the file, and the user's prose MOVES to the local pack |

### F1 — take fix option 1, and here is why it is safe

Confirmed by probe 2026-08-04: a pack with `skills/` + `AGENTS.md` and no `pack.json` lints
`✓ pack ok — 2 file(s) stage` and renders **zero** files to a real `$HOME`.

**The mechanism, which the report inferred correctly and which the code confirms:**
`packload.SkillsSourceDirs` has an explicit zero-ceremony fallback —

```go
if !declared {
    // Zero-ceremony: no manifest (or none mentioning skills) still merges skills/.
    add(packdecl.Contribution{Kind: packdecl.KindSkills})
}
```

— while the host render iterates `p.Decl.Contributions()` only. So this is an **asymmetry
between notches, not a host-side policy**, which is exactly what makes option 1 the right
choice: the jail already proves the inference is well-defined and already ships the destination
list. The host is not being asked to invent anything.

Suggested fix 2 (warn instead) is the fallback, and suggested fix 3 (reword
`(no declared claims)`) should happen **regardless** — it is the lint rewrite below.

### F2 — the ranking bump, and why the message matters as much as the behavior

Promoted above F1: it is the only finding that caused breakage rather than absence, and it has
no open design question.

The refusal rule (never clobber what yolo cannot prove it wrote) is **correct and must stay**.
The defect is that a *dangling* symlink is indistinguishable to it from precious user content —
and by any reading, a link to a file that no longer exists is not user content. The report's
`skipped (yours) — left untouched` observation is the important half: it reads as a safe no-op
while meaning *"this pack will never work until you delete something by hand."*

The `--doctor` suggestion is worth taking further than proposed: "why is my pack doing nothing"
is the same question F1 raises, so ONE command should answer both — destinations blocked by
`skipped (yours)`, plus packs that declare no destinations at all.

### F3 — take the warning, REJECT the adopt-into-markers half

The warning is right and matches the `⚠ would overwrite your existing value` precedent that made
the settings half of this migration uneventful.

**Adoption is not safe, though**, and the reason is a rule this codebase already follows
everywhere else: *never claim ownership of content you cannot prove you wrote.* Wrapping the
user's hand-written prose in yolo's markers claims exactly that — and the retirement path
shipped 2026-08-03 would then **delete the user's original text** when the pack later drops that
prose or is removed from config. The user would experience "I removed a pack and it ate my
CLAUDE.md."

So: warn, name the duplication, tell the user to delete their hand-written copy if they want the
pack to own it. Let the human transfer ownership explicitly; do not infer it from a byte match.

### F4 — the output actively misleads, which is worse than silence

The mechanism is right (this is the autonomy-leak fix, verified 2026-08-03: a host apply writes
`defaultMode: "default"`, `additionalDirectories: []`, `skipDangerousModePermissionPrompt:
false`). The defect is purely legibility, and it is the sharp kind: the overlay is accepted,
LISTED as contributing, and then loses — while `⚠ overwrote your existing value` fires, so the
output reads as though the overlay won.

This is ruling R3's question ("which pack set that key?") one layer deeper: R3 made overlay
contributions visible, but not overlay contributions that were **outranked**. Name the loss and
its cause, as proposed.

### F5 — the same rule an independent audit hit, and the rule asks the wrong question

Reported independently as the `pack lint` finding in
[`outstanding-work.md`](outstanding-work.md) §7, where the full probe table and the
recommended rewrite live. Short version: the rule asks *"did this pack stage `skills/` or
`AGENTS.md`?"* as a proxy for *"does anything read this pack?"* — true when a pack could only
ship content, false now that a pack contributes any of 14 kinds. A pack with **zero
contributions** and a working config-only pack get the *identical* message, so the rule is
useless in the one case it exists for.

The fix is two honest checks in place of one bad one — warn on zero contributions (which
`pack footprint` already computes exactly), keep the declared-source-delivers-nothing check —
and the report's own wording complaint is the tell: *"would stage files nothing reads"* is
simply false for a `files` tree.

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
