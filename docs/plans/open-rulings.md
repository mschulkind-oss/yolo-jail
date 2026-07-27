# The four open rulings — context for a decision

**Status: ALL FOUR ANSWERED 2026-07-26.** Every stage in [BACKLOG.md](BACKLOG.md) is now
unblocked. This doc keeps the facts and options that led to each answer; the answers are in the
`DECIDED` blocks.

| # | Ruling | Answer |
|---|---|---|
| 1 | first-migration vs user-asked-to-discard | **(a)** `reset` also truncates the surface to the pure render |
| 2 | pack state scope | selection stays **user-level**; **shared-across-jail state becomes pack-declared** (new field); removal leaves abandoned state in place, deliberately |
| 3 | where composition runs | **split by dependency**: image-build inputs and host-file reads on the host; **everything else stays in the container** |
| 4 | re-render while running | **no** — not supported; it was ruling 3's premise |

**Ruling 3 rejected my recommendation, and produced a better rule.** I argued host-side
composition for its error-timing benefit; the ruling points out that once re-render-while-running
is off the table, there is no reason to move composition at all. The line is *what needs the
host*, not *where we'd prefer things to run* — which deletes the largest port in stage D. The
error-timing benefit I wanted has to be recovered a different way: **host-side validation of
pack contributions**, noted under ruling 3 as a required work item.

**One correction up front, because it changes ruling 2 substantially.** I previously said
agent state is "per-machine by accident of a shared `GlobalHome`." **That is wrong.** Agent
state is **per-workspace**; the `GlobalHome` entries I saw are empty *mountpoints*. Details
under ruling 2.

---

## Ruling 1 — how does the engine tell "first migration" from "user asked to discard"?

**Blocks:** all of stage B (the only ⚠ data-loss items in the plan).

### The situation

`copilot/config` can wipe a live OAuth token. It renders statefully with
`Defaults: {"yolo": true}` and no host layer, so on a boot where `last_render` is
absent or corrupt, a file holding `copilot_tokens`, `logged_in_users` and
`last_logged_in_user` is reduced to one key. Steady state recovers, which is why nobody
noticed.

The fix is one branch. `staterender.go:129-140`:

```go
lastRender, lastOK := decodeKind(c, kind, in.LastRenderBytes)
firstMigration := !in.LastRenderPresent || !lastOK

if firstMigration {
    overlay = emptyOverlay(kind)     // ← today: discard whatever is on disk
} else {
    overlay = parseOverlayKind(kind, in.OverlayJSON)
    ...
}
```

Changing that to seed from `mergeDiff(pureRender, current)` preserves the token — proved by
probe on the real shape:

```
TODAY -> {"yolo": true}                                     <- token gone
ADOPT -> {"copilot_tokens":{…},"model":"x","yolo": true}     <- token preserved
```

### Why it can't just ship

`yolo config reset` deletes **both** sidecars precisely to reach that empty-overlay path
(`configdiff.go:235` removes `overlayPath` and `prismLastRenderPath`). That *is* how the
discard takes effect. So if first migration adopts the on-disk file instead:

> reset → no baseline → adopt → **the edits the user just asked to discard come back.**
> `reset` becomes a no-op.

Both states are "no baseline." The engine cannot distinguish them today.

### Options

| | Approach | Cost |
|---|---|---|
| **a** | **`reset` also truncates the surface file to the pure render** | smallest; one extra write in `reset`. Matches what a user means by "reset" — the file visibly returns to yolo's version |
| b | `reset` writes a one-shot marker the next boot consumes | a new sidecar kind and a new lifecycle to get wrong |
| c | Infer from mtime (adopt only if the surface predates a yolo artifact) | heuristic; clock skew and touched files make it unreliable |

### DECIDED 2026-07-26: (a)

**`reset` also truncates the surface file to the pure render.** Nothing is left to adopt, so
first-migration can safely adopt the on-disk file and the copilot token wipe is fixed without
`reset` becoming a no-op. Adds no new state, and makes `reset` take effect immediately instead
of silently waiting for the next boot.

Implementation shape for B1 (stage B is now fully unblocked):

1. `staterender.go:132` — replace `overlay = emptyOverlay(kind)` on the `firstMigration` branch
   with a seed from `mergeDiff(pureRender, current)`.
2. `configdiff.go` `configReset` — after removing both sidecars, write the pure render to the
   surface path.
3. Regression tests for both halves, plus the specific copilot shape (a file holding
   `copilot_tokens` must survive an absent `last_render`).

---

## Ruling 2 — is agent/pack state per-machine or per-jail?

**Blocks:** B4 (sidecar location), D6, and pack-removal cleanup.

### ⚠ My earlier claim was wrong — here is the real layout

Verified in this jail:

| Path | What it actually is |
|---|---|
| `<workspace>/.yolo/home/claude/` | **the real state** — 532 bytes of entries: `claude.json`, `history.jsonl`, `debug`, `cache`, … |
| `~/.local/share/yolo-jail/home/.claude/` | **an empty mountpoint** (`total 0`, no entries) |
| `~/.local/share/yolo-jail/home/.claude-shared-credentials/` | **genuinely machine-wide** — holds `.credentials.json` |
| `<workspace>/.yolo/prism/` | the capture sidecars — already per-workspace |

`prepareWsState` (`cli/run/prepare.go:135-142`) creates `<workspace>/.yolo/home/<subdir>`
per selected agent, and `assemble.go:169-171` bind-mounts each over
`/home/agent/.<subdir>`, nesting inside the `:ro` GlobalHome base. So **agent state is
per-workspace already**, and the GlobalHome dirs exist only so the OCI runtime has a
mountpoint to bind onto (it cannot `mkdirat` inside a `:ro` bind).

The one deliberate exception is `.claude-shared-credentials`, mounted from GlobalHome
(`assemble.go:173-176`) **only when claude is selected** — that is what lets a login survive
across workspaces.

### So the real question is narrower than I framed it

Not "should state be per-workspace?" — it already is, and it's the right default. The actual
questions:

**A distinction my first draft muddled.** "Are packs per-workspace?" is two questions, and
they have opposite answers:

- **Pack *selection* — which packs exist and are enabled — is USER-LEVEL, full stop.** Already
  settled: there is no workspace-scope pack, because a repo can lay out whatever it wants in
  its own tree and needs no distribution mechanism to reach files it owns. Nothing below
  reopens this.
- **Pack *effects* — the files a pack causes yolo to write — land wherever that kind of file
  already lands.** Composed agent config is per-workspace today
  (`<workspace>/.yolo/home/<agent>/`), so a pack-composed file goes there too. That is not a
  pack scope decision; it is just "packs don't invent a new location."

So: **user-level packs, existing per-workspace effects.** No conflict.

### DECIDED 2026-07-26

1. **Pack selection is user-level only** — reaffirmed, unchanged.
2. **Shared-across-jail state is generalized and pack-declared.** ✅ *"We need to generalize
   shared-across-jail state, to be specified by the pack."* Today exactly one dir is
   machine-wide (`.claude-shared-credentials`, mounted from GlobalHome only when claude is
   selected, `assemble.go:173-176`) and it is hardcoded. A pack declares which of its dirs are
   shared across jails; yolo mounts those from GlobalHome and everything else per-workspace.
   **This is a new pack field and a real work item** — see B4/D-stage in
   [BACKLOG.md](BACKLOG.md). Note it is also a prerequisite for agents-as-packs: claude's
   credential dir cannot become pack data without it.
3. **Removal does not clean up, and abandoned state stays in place.** ✅ *"Don't try, but leave
   the abandoned state in place."* `yolo pack remove` deletes the fetched content and stops
   composing. Per-workspace effects are left alone — not chased, not deleted. Two reasons this
   is right rather than merely easy: nothing enumerates workspaces, and composition regenerates
   every boot, so a dropped input simply stops appearing. Leaving the files also means a
   re-enabled pack finds its state intact, and a user who wants them gone can delete
   `<workspace>/.yolo/` themselves.

   **Implementation note:** "leave it in place" must be *deliberate*, not incidental. Removal
   should say what it left behind rather than silently orphaning files.

---

## Ruling 3 — where does composition run?

**Blocks:** D1, and reprices E's `:ro` work and capture timing.

**This is the big one.** Full argument in
[../design/what-yolo-is.md](../design/what-yolo-is.md) and
[../design/three-decisions.md](../design/three-decisions.md); here is the decision-shaped
summary.

### What is true

- Composition **never probes the running container**. Every `computed`-layer producer reads
  config, env and computed paths only — no `os.Stat`, `exec.Command` or `LookPath` in layer
  construction (`prism.go:448`, `agent_configs.go:167`).
- `yolo config render` already composes **host-side**, today, with no container.
- So where it runs is a **free choice**, not a constraint.

### What each option buys

| | Host-side | In-jail (today) |
|---|---|---|
| pack failure | **pre-flight error** at `yolo check`/assembly | fail-open `genStep` warning; agent boots misconfigured |
| pack logic trust | never crosses the boundary — no jail exists yet | runs next to the agent |
| `readonly` as real `:ro` | **works** — file is finished before the mount | impossible; can't compose into a `:ro` mount |
| projector subprocess | ordinary host subprocess | needs the binary in-jail |
| macos-user | **the only coherent story** (no mounts, no `/ctx`; "compose then write" works) | mounts don't exist to compose into |
| in-jail re-render | **lost** — reconcile moves to next assembly | works today |

### DECIDED 2026-07-26 — split by *what needs the host*, not by preference

**Ruling:** *"Re-render while running is not important to support — so let's not, and simplify
things. The image-build input stuff must run host side obviously; after that, the rest should
run in the container for the stuff that doesn't need host influence."*

This **rejects my host-side-everything recommendation**, and it draws a better line than
either option I offered. The rule is a *dependency* test, not a location preference:

| Runs where | What | Why it must |
|---|---|---|
| **host — mandatory** | image-build inputs: pack `provision` contributions, `packages`, capability requirements | they feed the nix derivation, which is built before any container exists |
| **host — mandatory** | anything reading a host file or host credential: the `host` layer, pack fetch, lockfile/approval | the jail has no host filesystem and no git credentials |
| **jail — default** | composing every surface that needs no host influence, overlay capture, sidecar writes | it already works there; moving it buys nothing once re-render-while-running is off the table |

**What this simplifies, concretely:**

- **No port.** Composition stays in `entrypoint` where it is. The largest single piece of work
  in stage D disappears.
- **macos-user needs no special case.** My "host-side is the only coherent macos-user story"
  argument assumed a mount step to compose into. With composition staying in-jail and
  macos-user having no jail/host filesystem split at all, it is *already* the degenerate case
  that works.
- **In-jail re-render keeps working**, so ruling 4 is answered by construction rather than
  traded away.

**What we give up, honestly** — and this is the part to keep an eye on:

- **Pack failures stay fail-open.** `genStep` (`boot.go:534-538`) downgrades any generator error
  to a warning so boot never aborts, so a malformed pack means an agent boots silently
  misconfigured. **Mitigation, and it must be a real work item, not a hope:** validate pack
  contributions *host-side at `yolo check` and at run assembly*, where erroring is normal. That
  is where the `host_files` validators already live (`checkHostFileLayer`,
  `checkHostFileDest`), so the precedent and the location both exist. **This is the single
  most important consequence of this ruling** — the error-surface risk does not go away, it
  moves to validation.
- **`readonly` as a real `:ro` mount stays hard** (stage E). You cannot compose into a `:ro`
  mount, so that item keeps its per-surface design pass. Unchanged from today.
- **The projector runs in-jail** for compose-phase projections, so a third-party projector
  binary must be present in the jail. The in-pack-script tier (`python3`, already baked) and the
  nix-package tier both cover this; the prebuilt-binary tier now needs the artifact staged into
  the jail, which is one more reason to discourage it.

---

## Ruling 4 — does a running jail need to re-render?

### DECIDED 2026-07-26: no — and ruling 3 answers it

**Re-render while a jail is running is explicitly not supported.** That was the *premise* of
ruling 3's split, not a consequence of it: because nothing needs to re-render mid-session,
composition has no reason to move host-side, so it stays in the container.

The behavior that remains, unchanged from today: an agent edits `~/.claude/settings.json` →
the **next boot**'s `ComposeStateful` diffs it against `last_render`, folds the delta into the
overlay, and re-renders. Sidecars live at `<workspace>/.yolo/prism/` (`prism.go:41`), so both
the edited file and its baseline survive `--rm` in the bind-mounted workspace — nothing is
lost, only *observability* lags until the next run.

Still worth doing eventually, for visibility rather than correctness: a `yolo config capture`
verb plus capture in the existing `onTerminate` hook. Stays in **stage E**; an inotify watcher
remains unjustified.

---

## What is NOT blocked

**Stage A's 11 items need none of these answers.** A1 (remove gemini), A2–A7 (the correctness
cluster), A8+A10 (skills gap + steering), A9 (wire the inert `transform`), A11 (un-hardcode
`/workspace`). That is the bulk of the immediately-shippable work and it can start now, in
parallel with these rulings.
