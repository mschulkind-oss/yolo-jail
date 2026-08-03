# Retiring a pack's host output

**Status:** spec, ready to implement (maintainer ruled 2026-08-03).

`yolo apply --host` renders four kinds into a real `$HOME`. When a pack is DROPPED from
config, exactly one of them is cleaned up.

| Kind | Pack changed (still configured) | Pack DROPPED from config |
|---|---|---|
| `briefing` | replaced in its managed block | **removed** ✅ |
| `skills` | stale entries archived | **left behind, still loadable** ❌ |
| `files` | stale entries archived | **left behind** ❌ |
| `config-overlay` | keys re-asserted | **keys left, and provenance LAUNDERED** ❌ |

## Why only briefing works

The apply loop is `for _, p := range loaded`. A dropped pack is not in `loaded`, so nothing
ever asks about its output. `PruneHostBriefings` is the ONLY path that reasons over the
inactive set — it takes `append(loaded, embeddedPacksForPrune()...)` plus an `active` map
precisely so it can see packs that left.

The per-pack retire machinery we shipped (`hostskills.Manifest.EntriesFor(pack, skillsDir)`
= "what I wrote last time minus what this pack ships now") is keyed on the pack being
ITERATED. It solves *the pack changed*. It cannot see *the pack left*.

So this is not a duplicate of the manifest/archive work — it is the other axis.

## Verified behavior (throwaway `$HOME`, 2026-08-03)

A test pack declaring all four kinds, applied alongside `claude`, then dropped:

```
dropme/briefing      removed (pack no longer configured)      ← only this
~/.claude/skills/demo                                          ← still there, still invocable
~/.claude/bin/pick.sh                                          ← still there
settings.json: "fileSuggestion": {...}                         ← still there
```

Two corrections to the original report, both of which change the fix:

1. **Skills leak too.** The report named only `files` and the overlay. `.claude/skills/demo`
   survived with the manifest still naming `dropme` as its owner. A live orphaned skill an
   agent will actually load is sharper than an orphaned script.
2. **The "broken hook" claim is wrong.** `files` COPIES the tree into `$HOME`. Deleting the
   pack source dir entirely left `~/.claude/bin/pick.sh` present and intact, so
   `fileSuggestion` keeps working. This is not a dangling-script state; it is an orphan that
   silently keeps FUNCTIONING — a stale-config problem, not a breakage. It is why the fix
   does not need to be urgent-destructive.

## The real defect: provenance laundering

While the pack is active, `~/.local/share/yolo-jail/host-provenance/claude-settings.provenance`
says:

```
fileSuggestion	config-overlay:dropme
```

The next apply AFTER the drop rewrites it to:

```
fileSuggestion	host
```

`rmwProvenance` (`internal/entrypoint/prism.go:611`) records every key the existing file has
as `LayerHost`, then upgrades the ones a live layer claims. With the owning pack gone, no
layer claims `fileSuggestion`, so it stays `host` — "the user set this".

This is worse than the leak it accompanies. The record we built so yolo could tell its own
output from the user's now converts yolo's own key into something the system will forever
defend as user content. It is also the cheapest half to fix: the correct attribution existed
one apply earlier.

## Rulings

**R1 — Confirm before removing host files.** *"I want to confirm before removing host
files."* Removing a dropped pack's `skills`/`files` output requires an explicit
confirmation, in the shape of `confirmHostLosses`: only when something is actually removed,
never in observe, fail-closed on EOF stdin. Declining leaves everything and reports it.

**R2 — Archive, never delete.** Retirement moves content under the archive root with the
apply's stamp (`hostskills.Archive`), reclaimed by `yolo prune`. Already promised by
`docs/design/pack-system.md:797`.

**R3 — Overlay keys are a pure assertion; drop them with the same confirm.** An overlay key
is a value in a config file, not user content — but it is IN a file the user owns, so it
rides the same gate rather than a separate silent path. Provenance must stop laundering
regardless of whether the key is dropped: an unclaimed key that yolo previously attributed
to a pack is recorded as retired, never as `host`.

**R4 — Briefing keeps its current behavior.** It is already correct and already
unconditional. Do not put it behind the new prompt; removing a delimited managed block
restores the file's own bytes and loses nothing.

## Why confirm rather than silent-archive

Archiving is recoverable, so a silent archive is defensible. It is still wrong here: the
user's mental model is "I edited a config list", and the consequence is "files left my real
home". Those are far enough apart that the action needs to be named at the moment it
happens. This is the same reasoning as `confirmHostLosses` — policy is only policy once the
user has opted into it.
