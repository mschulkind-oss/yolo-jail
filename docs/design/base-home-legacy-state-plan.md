---
title: "Base-home legacy state — implementation sketch"
date: 2026-09-20
status: draft
tags: [plan, sketch, base-home, storage, migration]
summary: "Parking lot for the base-home legacy-state quarantine: archive directory naming, the marker code sketch, the seedAgentDir allowlist shape, and test ideas. No design decision lives here — the design is base-home-legacy-state.md and it wins on behavior."
---

# Base-home legacy state — implementation sketch

**Status:** SKETCH, 2026-09-20 — incomplete, and unstable while questions are open.

**Design:** [`base-home-legacy-state.md`](base-home-legacy-state.md). **Precedence:** the
design wins on behavior; this file is the first thing here to be wrong.

**Reads with:** [`base-home-legacy-state.md`](base-home-legacy-state.md) (the design this
sketches), [`jail-home.md`](../reference/jail-home.md) (the home layout it operates on).

---

## What this file is

A parking lot for material that surfaced while writing the design and is worth keeping, but
is below the design's altitude and needs no ruling. **No design decision is made here.** If an
entry rests on an unruled question, it carries the link and the work waits.

## Archive directory naming

- Root: `filepath.Join(paths.GlobalStorage(), "archive", "base-home")`.
- **Generation keyed by state dir, NOT by a render stamp.** `PruneHostArchive` deletes any
  generation whose name `looksLikeArchiveStamp` parses — `YYYYMMDD-HHMMSS`
  (`internal/prune/hostarchive.go:122-125`) — keeping the newest 3
  (`internal/prune/prunecmd.go:268,548-549`). A stamp-shaped key would be silently pruned by
  the 4th `yolo prune --apply`. Key on the state dir the way the `config` bucket keys on
  `<agent>-<name>` (`internal/render/target.go:600`), which prune's own rule leaves alone.
- Per entry: `<root>/<state-dir>/<basename>`, with collision suffixing; a re-appearing
  identical file is reconciled through the manifest, not re-archived as `.2`.
- Manifest: `<root>/manifest.json`, versioned, updated atomically **per entry** before the
  next move ([design §5.3](base-home-legacy-state.md#53-disposition-archive-layout-and-manifest)).
- **Do not reuse `hostskills.Archive` as-is:** its `EXDEV` fallback materializes symlinks and
  reads whole files into memory (`internal/hostskills/archive.go:96-137`), contradicting the
  migration's `Lstat`-only rule and risking an OOM on a large session store. Either grow the
  helper with a link-preserving streaming mode or write the copy here.

Blocked on [OQ-BH1](base-home-legacy-state.md#OQ-BH1) for the bucket's retention and the
opt-in delete verb.

## Marker code sketch

```go
// A SEPARATE marker from StorageLayoutVersion. That one is written unconditionally
// (internal/storage/ensure.go:277) and is already 2 on every host, so sharing it
// stamps-without-apply.
//
// Detection is always-on and read-only; the marker gates only the apply.
//   func DetectLegacyBaseHome(packs []*packload.Pack, cfg *jsonx.OrderedMap) []LegacyEntry
//   func ApplyLegacyBaseHome(entries []LegacyEntry, stamp string) error
//
// Detection runs in the host-only path (insideJail short-circuit, ensure.go:257) on
// every host command; apply runs where a TTY and the console exist, under an
// unconditional non-blocking flock in GlobalStorage()/locks/. The lock is NOT held
// across the prompt.
//
// The marker is written only after ApplyLegacyBaseHome returns success. A
// zero-candidate apply discharges the debt and stamps; a transient failure leaves it
// unstamped and retried; a deterministic per-entry failure is recorded as skipped and
// does not block the marker (always-on detection re-reports it).
```

Blocked on [OQ-BH2](base-home-legacy-state.md#OQ-BH2) (marker mechanism and where apply
runs) and [OQ-BH3](base-home-legacy-state.md#OQ-BH3) (whether apply prompts).

## Classification predicate sketch

```go
// Derived at runtime from declarations that already exist — no new packdecl field
// for v1.
//
//  0. skip/refuse any root that is not a real directory, and any path under a
//     declared machine-scope shared dir (packload.EmbeddedSharedDirs) -> CREDENTIAL
//  1. basename in the credential set derived from packs' shared_credentials hooks
//     (from-path basenames) + {.credentials.json, auth.json, oauth_creds.json}
//        -> CREDENTIAL
//  2. relPath matches a declared config surface, joined through
//     paths.HomeFileRedirects so "~/.claude.json" maps to ".claude/claude.json"
//        -> CONFIG   (NB: .claude/claude.json still carries projects/mcpServers;
//                     those keys need a separate reduction via claudeJSONSeedKeys)
//  3. relPath under a declared content destination — the resolved skills/briefing/
//     files destinations from packload.ResolveDestinations
//        -> CONTENT
//  4. otherwise -> RUNTIME (archive)
//
// The join is home-relative: a surface.Path like "~/.copilot/config.json" trims "~/"
// and compares against the state dir's path. A directory-valued surface owns its
// subtree. Symlinks are Lstat'd, never followed.
```

Blocked on [OQ-BH4](base-home-legacy-state.md#OQ-BH4) (core list vs packdecl field vs
derive).

## seedAgentDir allowlist shape

```go
// internal/cli/run/storagehelpers.go:42 — replace the "every top-level regular file"
// loop with a predicate call. Lstat, NOT Stat: the current body follows symlinks
// (storagehelpers.go:59) and would seed a link named like a credential whose target
// is runtime.
//
//   for _, e := range entries {
//       if e.IsDir() { continue }
//       if !seedable(subdir, e.Name()) { continue }
//       ... copyFile2 ...
//   }
//
// seedable(subdir, name) = credentialName(name) || configSurface(subdir, name)
//
// NOTE: seedAgentDir already skips directories (storagehelpers.go:52), so nested
// config surfaces (~/.pi/agent/*, ~/.gemini/antigravity-cli/*, ~/.oh-omp/agent/
// models.yml) are not seeded by this predicate either. That is a known gap, not a
// regression.
//
// Ships in the SAME change as the move: the seed runs later in the launch than
// ensureStorage (run.go:129 vs run.go:1046 -> prepare.go:409), so a deferred move
// would otherwise re-infect every workspace.
```

Blocked on [OQ-BH5](base-home-legacy-state.md#OQ-BH5) (how far prevention goes).

## Shadow-layer sketch (if ruled)

```go
// podman: for each packload.EmbeddedWritableDirs() NOT in packload.WritableDirs(selected),
// emit a nested mount over the base dir, e.g.:
//   -v <wsState>/shadow/<dir>:/home/agent/<dir>
// where <wsState>/shadow/<dir> is an empty per-workspace dir. Never over a selected pack's
// real overlay (assemble.go:376-379).
```

Blocked on [OQ-BH6](base-home-legacy-state.md#OQ-BH6).

## Test ideas

- A fixture base home with a `.copilot/session-store.db` (+ `-wal`/`-shm`), a
  `.copilot/config.json`, a `session-state/events.jsonl`, and a machine-scope credential dir;
  assert only the runtime leaves move and the credential dir is untouched.
- Assert the SQLite sibling set stays together (all three moved, or none).
- Assert the top-level state dirs still exist after a move (the `:ro` mountpoint constraint).
- Assert the marker is **not** written when a transient move fails, **not** written when the
  apply is deferred (no TTY), **is** written on success, and is written on a zero-candidate run.
- Assert a second run is a no-op (idempotence) and a resumed run continues past a moved entry.
- Assert a deterministic per-entry failure (dangling symlink) is skipped and disclosed, and the
  rest of the migration proceeds.
- Assert `seedAgentDir` no longer copies a runtime file into `wsState`, and does not follow a
  symlink whose target is runtime.
- Assert the in-jail resolver (`insideJail`) short-circuits the apply.
- Assert `hostskills.Archive`'s cross-device fallback is not exercised by this migration
  unmodified (it materializes symlinks / reads whole files).
- Assert the `base-home` archive bucket survives `yolo prune --apply` (non-stamp key).
- A test that fails if the apply call site is deleted — pinning the callee while the caller is
  unpinned is not a test.
- A negative test for [§8](base-home-legacy-state.md#8-what-this-does-not-cover):
  `PruneShadowedHome`'s registry does not gain a pack state dir.

## Open threads

- The 34 MB instance is host-only; no in-jail test can assert on it. The fixture is synthetic.
- `.gemini/antigravity-cli/` is the mixed-directory test case; keep it in the fixture.
- `.claude/claude.json` is CONFIG and is not moved, but its `projects`/`mcpServers` keys still
  leak; the companion reduction is via `claudeJSONSeedKeys`
  (`internal/storage/claudejson.go:10-13`).
- Retired/unknown pack state dirs are a walk-set question, not just a classification one
  ([design §1](base-home-legacy-state.md#1-the-base-home-is-a-union-and-the-union-is-the-defect)).
