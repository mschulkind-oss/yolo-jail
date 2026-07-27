# The four open rulings — context for a decision

**Status:** 2026-07-26. Each ruling below blocks a stage in [BACKLOG.md](BACKLOG.md). This
doc gives the facts, the options, and a recommendation per ruling, so the answers can be
given in one pass and the work fired off.

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

**Recommendation: (a).** It is the only one that adds no new state. It also makes `reset`
honest: today `reset` is invisible until the next boot, which is itself confusing.

**Question for you:** *approve (a), or prefer (b)?*

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

1. **Do packs follow that pattern?** A pack's *content* is machine-wide (fetched once,
   `~/.local/share/yolo-jail/packs/`). Its *effects* — composed files, captured overlays —
   are per-workspace. **Recommendation: yes, follow it.** It matches the sidecars, which are
   already per-workspace, and it means two workspaces can enable different packs.
2. **Which state is credential-shaped and therefore machine-wide?** Today exactly one dir,
   claude's. If pack-shipped agents need the same, the pattern must generalize — an agent pack
   would declare "this dir is shared across workspaces," which is a new pack field.
3. **Does pack removal clean up?** Content is easy (delete the fetch). Per-workspace effects
   are the hard part: they are spread across every workspace that ever used the pack, and
   nothing enumerates workspaces. **Recommendation: don't try.** `yolo pack remove` deletes
   content and stops composing; stale files decay naturally because composition regenerates
   every boot and a dropped input simply stops appearing.

**Question for you:** *packs follow the per-workspace pattern with a machine-wide escape
hatch for credential dirs, and removal doesn't chase per-workspace effects — approve?*

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

### The cost, stated plainly

A running jail can no longer re-render itself. Today an agent edits a composed file and the
next boot reconciles from inside. Host-side means reconciliation happens at *assembly*, so
re-render requires a restart — unless `yolo config render` is made to write through in-jail,
which is possible (the files are in a host-backed bind) but is extra work.

**Recommendation: host-side, with the binary-install carve-out.** Compose config on the host;
keep agent-CLI / npm / LSP / mise installs in-jail where they already are. It is the only
option with one story across all three backends, and it converts the pack error surface from
fail-open-at-boot to pre-flight — which is the single biggest risk in the rip-out.

**Question for you:** *host-side (recommended), in-jail, or host-side-but-keep-in-jail-
re-render-as-a-later-item?*

---

## Ruling 4 — does a running jail need to re-render?

**Blocks:** nothing on its own; it is the follow-on to ruling 3 and only bites if that goes
host-side.

### Context

Today's flow: agent edits `~/.claude/settings.json` → next boot's `ComposeStateful` diffs it
against `last_render`, folds the delta into the overlay, re-renders. The sidecars are at
`<workspace>/.yolo/prism/` (`prism.go:41`) — **per-workspace, host-visible**, which is why
nothing is lost across `--rm`: both the edited file and its baseline survive in the
bind-mounted workspace.

Under host-side composition, that same reconcile happens at *next assembly* instead. Nothing
is lost; only the *timing* changes.

### Options

| | Approach | Notes |
|---|---|---|
| **a** | **Re-render only at run start.** In-jail edits reconcile next run | simplest. Matches the current mental model ("config regenerates every boot") |
| b | `yolo config render --write` invokable in-jail | the files are host-backed bind mounts, so an in-jail write works; needs the pack/projector inputs available in-jail too, which partly defeats ruling 3 |
| c | A `yolo config capture` verb, plus capture on terminate | already tracked as E; useful for *observability* regardless |

**Recommendation: (a), plus (c) later for visibility.** (b) reintroduces the thing ruling 3
removes.

**Question for you:** *approve (a) + defer (c)?*

---

## What is NOT blocked

**Stage A's 11 items need none of these answers.** A1 (remove gemini), A2–A7 (the correctness
cluster), A8+A10 (skills gap + steering), A9 (wire the inert `transform`), A11 (un-hardcode
`/workspace`). That is the bulk of the immediately-shippable work and it can start now, in
parallel with these rulings.
