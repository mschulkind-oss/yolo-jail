# The four open rulings — context for a decision

**Status: ALL FOUR ANSWERED 2026-07-26.** Every stage in [BACKLOG.md](BACKLOG.md) is now
unblocked. This doc keeps the facts and options that led to each answer; the answers are in the
`DECIDED` blocks.

| # | Ruling | Answer |
|---|---|---|
| 1 | first-migration vs user-asked-to-discard | **(a)** `reset` also truncates the surface to the pure render |
| 2 | pack state scope | selection stays **user-level**; **shared-across-jail state becomes pack-declared** (new field, generalizing claude auth); removal leaves abandoned state in place, deliberately |
| 3 | where composition runs | **split by dependency**: image-build inputs and host-file reads on the host; **everything else stays in the container** |
| 4 | re-render while running | **no** — not supported; it was ruling 3's premise |
| 5 | config/pack generator failure | **fatal** — loud, halting, jail does not start. `genStep`'s fail-open behavior is removed (20 call sites) |

**Ruling 3 rejected my recommendation, and produced a better rule.** I argued host-side
composition for its error-timing benefit; the ruling points out that once re-render-while-running
is off the table, there is no reason to move composition at all. The line is *what needs the
host*, not *where we'd prefer things to run* — which deletes the largest port in stage D. The
error-timing benefit I wanted is recovered directly instead: **ruling 5 makes generator
failures fatal**, so in-jail composition now has the same error discipline I wanted from
host-side composition. Host-side validation stays worth building, as defense in depth.

**Scope facts, stated once, because I got this wrong in both directions.** There are **two**
tiers and both are real:

| Tier | Path | What lives there |
|---|---|---|
| **per-workspace** | `<workspace>/.yolo/home/<agent>/` | the bulk of agent state — `claude.json`, `history.jsonl`, `debug`, `cache` |
| **machine-global** | `GlobalHome/.claude-shared-credentials/` | **claude auth, deliberately shared across every workspace and jail** |

My first draft called all agent state machine-global (wrong — the `GlobalHome/.claude` entries
are empty mountpoints); my correction then over-swung to "state is per-workspace" and buried
the auth tier. **Both tiers exist by design.** The mechanism is a symlink *out* of the
per-workspace dir:

```
~/.claude/.credentials.json -> ../.claude-shared-credentials/.credentials.json
```

planted by `ensureCredentialsSymlink` (`entrypoint/claude.go:106-108`), with the target dir
bind-mounted from `GlobalHome` (`assemble.go:174-175`). `EnsureGlobalStorage`
(`storage/ensure.go:54,72`) creates it, the OAuth broker reads that exact path
(`oauthbroker/oauthbrokercmd.go:20`), and `yolo check` verifies it
(`check/sections_misc.go:20`). **Maintaining claude auth across jails is a designed feature
with five call sites, not an accident** — which is exactly why ruling 2 generalizes it.

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

### The real layout — two tiers, both by design

Verified in this jail (see also the summary at the top of this doc):

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

**And there is genuinely machine-global state: claude auth.**
`.claude-shared-credentials` is mounted from GlobalHome (`assemble.go:174-175`) when claude is
selected, and `~/.claude/.credentials.json` is a **symlink out** of the per-workspace dir into
it (`entrypoint/claude.go:106-108`). That is a designed feature — *we maintain claude auth
across workspaces and jails* — with five supporting call sites (`storage/ensure.go:54,72`
creates it, `oauthbroker/oauthbrokercmd.go:20` reads it, `check/sections_misc.go:20` verifies
it). Do not read the per-workspace default as "everything is per-workspace."

So the shape is: **per-workspace by default, machine-global where the state is
identity/credential-shaped.** Ruling 2 generalizes the second tier from one hardcoded dir into
a pack-declared field.

### The actual questions

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

**What we give up — and RULED 2026-07-26: nothing, because fail-open is being removed.**

*"A pack failure means a jail should not start. Failures should be loud and halting."*

**What "fail-open" means here**, since the term was jargon: `genStep`
(`boot.go:533-538`) is the wrapper every config generator runs through, and its entire body is

```go
func genStep(e *Env, label string, fn func() error) {
    if err := fn(); err != nil {
        e.warn("Warning: " + label + ": " + err.Error())
    }
}
```

The error is printed and **discarded** — boot continues to the next step and the jail starts
anyway. "Fail-open" = the failure does not close the gate. There are **20 `genStep` call sites**
in `boot.go`, so today *every* config generator can fail silently-ish and still yield a running
jail with a misconfigured agent.

**The ruling: a pack/config generator failure is fatal — print it loudly and refuse to start
the jail.** This is a new work item and it is not small, because it inverts a deliberate design
choice made 20 times over.

What has to be worked out while implementing it (not blockers, but they will come up):

- **Which failures are genuinely fatal vs absent-input.** "The pack's projector exited 1" and
  "this optional host file does not exist" are different. Today both flow through `genStep`.
  The fail-open behavior exists partly to tolerate the second kind, so those paths need to stop
  returning errors for non-errors rather than being made fatal.
- **Ordering.** A generator that fails after earlier ones have written files leaves a partially
  configured home. Halting is still correct — a partial config that *reports itself* beats a
  partial config that pretends success — but the message must say what was and wasn't done.
- **Host-side validation is still worth doing**, and now for a better reason: catching a bad
  pack at `yolo check` or run assembly turns a fatal boot failure into a pre-flight error the
  user can act on before the container starts. Precedent and location both exist
  (`checkHostFileLayer`, `checkHostFileDest`). This is now *defense in depth* rather than the
  only line of defense.

**Net effect of this ruling on ruling 3:** the one real cost I identified for keeping
composition in-jail is gone. In-jail composition with loud halting failures has the same error
discipline I wanted from host-side composition.
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
