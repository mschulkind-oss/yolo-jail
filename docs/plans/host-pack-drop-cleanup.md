# Retiring a pack's host output

**Status:** R1/R2/R4 SHIPPED 2026-08-03 (`internal/cli/applyhostprune.go`). R3 — the overlay
half, including the provenance laundering — is still open.

Three things the spec did not know, all found by running the lifecycle rather than reading it:

1. **A NAMESPACED (tier-A) subtree leaks the same way and the manifest cannot see it.** The
   spec says "the manifest is the whole source of truth here". It is not: `deliverNamespaced`
   records NOTHING in the manifest, because inside its own subtree "is this mine?" is answered
   by the path. So a dropped `tier: "namespaced"` pack leaves a whole loadable namespace behind
   with zero manifest entries, and a manifest-only scan misses it entirely. The subtree's own
   `x-yolo-managed-by` marker is the only evidence, so the scan reads it (`YoloPluginOwner`)
   for any dir the manifest does not already own — manifest first, because for a WRAPPED
   plugin the marker names the PLUGIN, not the pack that delivered it.
2. **Emptying `packs` retired nothing.** `applyHost` early-returns on `len(entries) == 0` with
   "nothing to apply to the host", so the most complete drop there is was the one case that
   cleaned up nothing.
3. **`active` is the wrong set to key on; `configured` is.** `PruneHostBriefings` keys on the
   RESOLVED set, so a fetched pack with an unreachable remote looks dropped. A briefing
   survives that mistake (the block re-renders from prose inside the pack the moment the
   remote is back); an archived skills tree does not come back without the user digging in the
   state dir. Same evidence, different cost of being wrong, so the retire pass keys on the set
   the config NAMES. The briefing prune's threshold was deliberately left alone.

`yolo apply --host` renders four kinds into a real `$HOME`. When a pack is DROPPED from
config, exactly one of them is cleaned up.

| Kind | Pack changed (still configured) | Pack DROPPED from config |
|---|---|---|
| `briefing` | replaced in its managed block | **removed** ✅ |
| `skills` | stale entries archived | **archived, confirm-gated** ✅ (was: left behind, still loadable) |
| `files` | stale entries archived | **archived, confirm-gated** ✅ (was: left behind) |
| `config-overlay` | keys re-asserted | **keys left, and provenance LAUNDERED** ❌ (R3, open) |

## Why only briefing worked

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
**SHIPPED.** One deviation worth naming: declining does NOT fail the apply (rc stays 0).
`confirmHostLosses` refuses the whole write because it gates a render the user asked for; here
nothing the user asked for was skipped, and a permanent non-zero exit would make every scripted
`apply --host --assert` look broken after any drop, with no non-interactive way to ever answer.

**R2 — Archive, never delete.** Retirement moves content under the archive root with the
apply's stamp (`hostskills.Archive`), reclaimed by `yolo prune`. Already promised by
`docs/design/pack-system.md:797`. **SHIPPED** — verified reclaimable through
`yolo prune`'s existing "Host-render archive" sweep, which needed no change.

**R3 — Overlay keys are a pure assertion; drop them with the same confirm.** An overlay key
is a value in a config file, not user content — but it is IN a file the user owns, so it
rides the same gate rather than a separate silent path. Provenance must stop laundering
regardless of whether the key is dropped: an unclaimed key that yolo previously attributed
to a pack is recorded as retired, never as `host`.

**R4 — Briefing keeps its current behavior.** It is already correct and already
unconditional. Do not put it behind the new prompt; removing a delimited managed block
restores the file's own bytes and loses nothing. **SHIPPED** — and pinned: a test asserts the
block is removed even on a DECLINED file retire, so the two halves cannot be quietly unified.

## Why confirm rather than silent-archive

Archiving is recoverable, so a silent archive is defensible. It is still wrong here: the
user's mental model is "I edited a config list", and the consequence is "files left my real
home". Those are far enough apart that the action needs to be named at the moment it
happens. This is the same reasoning as `confirmHostLosses` — policy is only policy once the
user has opted into it.
