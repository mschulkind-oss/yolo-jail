# Retiring a pack's host output

**Status:** ALL FOUR RULINGS SHIPPED 2026-08-03. R1/R2/R4 in
`internal/cli/applyhostprune.go`; R3's second sentence (the laundering) in
`internal/entrypoint/prism.go`; R3's first sentence (dropping the keys) in
`internal/entrypoint/hostoverlayprune.go` + `internal/cli/applyhostoverlaykeys.go`, riding
R1's single prompt.

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

`yolo apply --host` renders four kinds into a real `$HOME`. When a pack was DROPPED from
config, exactly one of them was cleaned up. All four are now.

| Kind | Pack changed (still configured) | Pack DROPPED from config |
|---|---|---|
| `briefing` | replaced in its managed block | **removed** ✅ |
| `skills` | stale entries archived | **archived, confirm-gated** ✅ (was: left behind, still loadable) |
| `files` | stale entries archived | **archived, confirm-gated** ✅ (was: left behind) |
| `config-overlay` | keys re-asserted | **key removed, confirm-gated; provenance retired** ✅ (was: left, and LAUNDERED) |

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

**Status: FIXED** (`retired:<layer>`, see "How it was fixed" below). The record no longer
launders; whether the orphaned KEY is also dropped from the file remains open under R3.

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

### How it was fixed

A new layer token, `retired:<the layer that last claimed the key>`. The label carries the
previous layer verbatim (`retired:config-overlay:dropme`), because after the drop there is no
other source for the fact — nothing declares the key any more.

```
apply 1 (pack configured) fileSuggestion	config-overlay:dropme
apply 2 (pack dropped)    fileSuggestion	retired:config-overlay:dropme   ← was `host`
apply 3, 4, …             fileSuggestion	retired:config-overlay:dropme   ← sticky
```

Mechanically: `rmwProvenance` takes the PREVIOUS record (`readProvenanceRecord`) and ends with
`retireUnclaimed`, which rewrites an attribution only when all three hold — this render derived
`host`, the previous record attributed the key to a FORCE-WRITTEN layer
(`agentcfg.LayerAsserted`: `managed`, `computed`, `config-overlay:<pack>`), and the key is still
in the file. Retirement is sticky because a `retired:` label is itself asserted-through;
without that the fix would only DELAY the laundering by one apply.

Three properties worth stating, each of which is a way this could have gone wrong:

- **A prefix on the previous label, not a bare `retired` and not a second column.** The record
  stays one `key\tlayer` line, so every existing reader parses it unchanged. And the label is
  neither `host` (nothing mistakes the key for the user's) nor `OverlayLayer(pack)` (a reader
  asking "did MY pack win?" correctly answers no — a dropped pack is not setting the key).
- **`host` and `defaults` are NOT retirable.** Retiring the user's own key is the same
  laundering reversed, and it is the direction that COSTS something: a prune reading the record
  would delete a hand-written key. `defaults` is excluded for a different reason — it is
  fill-if-absent, so yolo writes it once and the value is the user's from then on.
- **Fail-safe is a closed set of exact tokens.** A missing, unreadable, or corrupt record
  proves NOTHING, which the existing code already treats as "every key is the user's". Garbage
  is rejected line by line rather than wholesale, so one bad byte cannot relaunder a surface.

The reader half is `yolo config diff`, which now reaches a surface whose ONLY finding is a
retired key — the case a reader keyed on live contributions structurally cannot see, since an
orphan's defining property is that no pack declares it.

**Parity was NOT touched.** `TestProvenanceParityAcrossBothDerivations` passes unchanged, and
the reason is structural rather than lucky: every corpus fixture is a FIRST render into a fresh
home, so `previous` is nil and the retirement pass is a no-op. Retirement has no counterpart in
`Compose` to be at parity WITH — a fold renders from the layers it has, so a layer that stops
claiming a key simply does not contribute it and the key is not in the rendered file. There is
nothing to launder in a fold. That boundary is now ASSERTED in `parityRecords` (no host record
may carry a retired label), so a retirement pass that ever fired on a first render would fail
the table rather than silently invalidate its premise. Ruling #8 stands: the two derivations
remain separate.

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
to a pack is recorded as retired, never as `host`. **Second sentence SHIPPED** (the
`retired:<layer>` token — see "How it was fixed"); the record no longer lies whether or not the
key is dropped, which is what makes the drop half independently sequenceable. **First sentence
SHIPPED** — `entrypoint.PruneHostOverlayKeys` finds the keys, `cli.overlayKeyRetirement` plans
and commits them, and they appear in R1's ONE prompt rather than a second.

Four things the key half turned out to need, none of them in the spec:

1. **The prune reads the record and CROSS-CHECKS the live layers.** The record holds one winner
   per key, so two packs contributing the same key leave only the last named. Drop that one
   while the other stays and the record alone calls a live key an orphan. An `--assert`
   self-corrects (its render rewrites the record before the prune reads it) but OBSERVE does
   not, so a record-only prune printed `would remove` for a key the very next assert keeps.
   `liveClaims` is the fix; it deliberately excludes `defaults`, which is fill-if-absent.
2. **Both spellings of the attribution are eligible**, `config-overlay:<pack>` and
   `retired:config-overlay:<pack>`. Which one is on disk depends only on whether a render has
   run since the drop, so accepting one made the prune work in exactly one posture.
   `retired:managed` / `retired:computed` stay OUT: those are the owner pack's own keys, a
   different axis.
3. **A key-only drop must be able to raise the prompt by itself.** A pack contributing an
   overlay and nothing else leaves no path to retire, so a gate keyed on "is there a path?"
   would remove the key silently — exactly what R3 forbids.
4. **A dynamic managed table needs no prune and must not get one.** An overlay contributing
   `mcpServers` entries folds into the wholesale table layer, so its key is attributed
   `computed`; dropping the contributor leaves that layer empty and `regenerateManagedTables`
   clears the block on the next apply. A second remover would race a mechanism that is already
   correct.

Not archived, unlike a path (R2), and the asymmetry is real: a delivered path may carry the
user's edits, while an overlay key is the pack's own assertion, reproduced exactly by putting
the pack back in `packs`. There is nothing to keep — what the user is owed is being TOLD, which
is the shared prompt's job.

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
