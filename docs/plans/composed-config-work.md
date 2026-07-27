# Composed-config work — the implementable list

**Status:** plan, 2026-07-26. **ROADMAP:** supersedes the sub-tables of items 3 and 4 as
the working list; ROADMAP keeps the one-line summaries and the lane/blocker call.

> **⚠ For picking up work, use [BACKLOG.md](BACKLOG.md) instead.** It is the single
> ordered list across the whole cluster (prism + packs + rip-out) and maps each item to
> its reasoning doc. This file remains the *per-item detail* for the prism items — the
> failure each one fixes, the file it touches, the verification it needs.

**What this is.** Every actionable item from the composed-config discussion, deduplicated
into one dependency-ordered list, each marked defect-or-improvement, ready-or-needs-
decision, and sized. Written because the work had scattered across ~8,900 lines of design
docs and the same item appeared in up to five places with different framing.

**What this is not.** The reasoning. That stays in
[composed-file-permissions.md](../design/composed-file-permissions.md) (postures, the
audit, the writer-class split), [host-file-staging.md](host-file-staging.md) (the shipped
`host_files` design), and [packs-and-the-prism.md](../design/packs-and-the-prism.md) (the
longer-term shape). This doc is the *what to do*, with a pointer per item.

**Verification note, read this first.** Bare `yolo` in a dev jail is frozen at the last
host `just load` and **does not have `config ls|diff|reset`**. Every item touching those
must be verified with `./dist-go/linux-$(go env GOARCH)/yolo`, per AGENTS.md. Flake
changes need a nested jail with `YOLO_REPO_ROOT=/workspace`, or `AutoLoadImage` silently
runs stale code.

---

## Order of play

Four tranches. The first is subtractive, the second is cheap and unblocked, the third
needs one decision, the fourth is genuine design work that should not be started by
accident.

| Tranche | Items | Gate |
|---|---|---|
| **0 — subtract first** | remove gemini | none |
| **1 — cheap + unblocked** | correctness cluster, then steering | none |
| **2 — the data-loss chain** | adopt-on-first-migration → de-compose credentials → sidecar location | **one decision** (§2.1) |
| **3 — parked design work** | modes 4→3, `:ro` per surface, capture timing | needs a per-surface pass |

**If the goal is the full pack rip-out**, tranches 0–2 are its prerequisites, not a detour —
see [packs-rip-out.md](packs-rip-out.md), which lists the three decisions still owed and
confirms none of them block this list.

**One item in tranche 3 is not like the others.** 3.9 ("where does composition run?") is a
*fork in the architecture*, not a task — it reprices 3.1, 3.2, 3.3 and the entire
pack-logic question. It is parked because it is a real port, not because it is minor; the
risk is deciding it **implicitly** by continuing to render in `entrypoint`.

---

## Tranche 0 — remove the `gemini` agent

**Improvement. Ready. Medium (touches ~8 files).**

Google is deprecating Gemini CLI. Do this first because it is *subtractive*: it removes
rows from the tables every other item edits, so doing it later means doing that work twice.

Blast radius, coupling analysis and the migration note are in
[ROADMAP item 0](ROADMAP.md). The one scary-looking dependency is settled: **`agy` already
runs with `gemini` unselected in this very jail** (probed — `~/.gemini/antigravity-cli` is
rw while its parent `~/.gemini` is ro), so removal is safe for it.

**Watch:** `agents.AllOverlayDirs` feeds `reservedHomeSegments`, so dropping `.gemini`
makes `~/.gemini/…` a legal `host_files` destination. Decide whether to keep it reserved
anyway (agy still writes there).

---

## Tranche 1 — cheap, unblocked, and mostly correctness

Each is independently shippable in roughly one sitting.

| # | Item | Kind | Size | Where |
|---|---|---|---|---|
| 1.1 | **Reserve symlink targets** — `~/.config/git/config`, `~/.config/bashrc`, `~/.claude/claude.json` validate as `host_files` destinations while their aliases are rejected | defect | small | `internal/config/writablehome.go` reserved lists; [§4.5](../design/composed-file-permissions.md) |
| 1.2 | **Mark `claude/config` non-rendered** so `config render claude` stops printing a composition the jail never performs (it currently dumps `machineID`, the full `mcpServers` table, onboarding state) | defect | small | `internal/cli/config.go` render loop + a manifest flag; [§4.3](../design/composed-file-permissions.md) |
| 1.3 | **Fix the `writeInPlaceString` umask claim** — the comment asserts "0o644, umask-independent"; `os.WriteFile`'s perm is masked, so `umask 077` yields `0o600`. Set modes explicitly | defect (latent) | small | `internal/entrypoint/helpers.go`; [§2](../design/composed-file-permissions.md) |
| 1.4 | **Make `~/.gitconfig`'s unwritability legible** — the symlink is a decoy (target shadowed by a `:ro` bind), so `git config --global` fails "Device or resource busy" | defect | small | drop the decoy symlink or surface the reason; [§4.1](../design/composed-file-permissions.md) |
| 1.5 | **Fix `config-ref`'s `reset`-re-seeds-`once` promise** — it documents behavior that cannot happen. Either implement it or remove the claim | defect (docs lie) | small | `internal/cli/config_ref.txt`; this is exactly what host-file-staging's "Scope: the line" exists to prevent |
| 1.6 | **Feed `config render` the overlay + computed layers** (or relabel it a defaults+host preview). Today it omits both and reads "host" from its own destination, so it attributes mise's computed `tools` to `host` and prints a claude `model` present in no boot layer | defect | small–medium | `internal/cli/config.go`; [§4.4](../design/composed-file-permissions.md) |
| 1.7 | **Give `pi`/`codex`/`opencode` a skills dir** — they are `Skills: ""` and get `continue`d, so they have **no skills at all, including yolo's own built-in suite**, while their briefing tells them to "read configuring-the-jail" | defect | **two lines** | `internal/agents/agents.go`; the mount loop already handles it |
| 1.9 | **`host_files`' `transform` key is inert** — it is documented (`config_ref.txt:283` "path to a Lua hook; works on every codec"), in the schema allowlist (`hostfiles.go:72`), parsed and path-cleaned (`:539`), and copied onto the surface (`entrypoint/hostfiles.go:135`). But **nothing reads `Surface.Transform`**: `compose.go:255` passes `in.Script`, which every producer fills from the global `config.lua` pair only (`prism.go:124,275`; `cli/config.go:193`). Its sole readers are two `!= ""` display checks in `configls.go`. So a user's per-surface hook is silently ignored | defect (**documented key does nothing**) | small | wire `Surface.Transform` into the compose path, or remove the key + doc line |
| 1.8 | **Steer directed agents at composed surfaces** — extend `configuring-the-jail` to cover composed files, and add a generated-by-yolo header where the codec allows. Zero of the four built-in skills mention the prism, a composed surface, or `yolo config ls` | improvement, **highest value/effort in the set** | docs-only | [§8.4](../design/composed-file-permissions.md); depends on 1.7 to reach all agents |

**Do 1.7 + 1.8 together** — 1.8's guidance is worthless to three agents until 1.7 lands.
1.8 is also Phase 0 of the config-packs plan, so it pays twice.

---

## Tranche 2 — the data-loss chain

### 2.1 The decision that gates it

**`copilot/config` can wipe a live OAuth token** — the only ⚠ data-loss defect in the set.
It renders statefully with `Defaults: {"yolo": true}` and no host layer, so a
first-migration boot (absent/corrupt `last_render`) reduces a file holding
`copilot_tokens`, `logged_in_users` and `last_logged_in_user` to one key. Steady state
recovers, which is why it went unnoticed.

The fix is one branch — seed the overlay from `mergeDiff(pureRender, current)` instead of
empty — and it fixes **every** surface at once. Proved by probe on the real shape:

```
TODAY -> {"yolo": true}                                    <- token gone
ADOPT -> {"copilot_tokens":{…},"model":"x","yolo": true}    <- token preserved
```

**But it cannot ship as specified**, and this is the gate:

> **`yolo config reset` deletes *both* sidecars precisely to reach the empty-overlay
> path** (`internal/cli/configdiff.go:234-235`) — that is how the discard takes effect. If
> first migration instead adopts the on-disk file, reset → no baseline → adopt → **the
> edits the user just asked to discard come back.** Reset becomes a no-op.

Both states are "no baseline", so the engine cannot distinguish them today.

**Question for the maintainer: how does the engine tell "first migration" from "the user
asked to discard"?** Options, cheapest first:

1. **`reset` also truncates the surface file to the pure render** — nothing left to adopt.
   Smallest, and it matches what a user means by "reset".
2. `reset` writes a one-shot marker the next boot consumes.
3. Infer migration from mtime (adopt only if the surface predates a yolo artifact).

### 2.2 Then, in order

| # | Item | Kind | Size |
|---|---|---|---|
| 2.2a | **Adopt-on-first-migration** — the branch above, once 2.1 is answered | defect fix | one branch in `internal/agentcfg/staterender.go:132-140` + unit test + nested-jail check |
| 2.2b | **De-compose the credential surfaces** — move `copilot/config` onto the `writeClaudeJSON` read-modify-write pattern so tokens leave the capture path entirely | defect fix | ~20 lines per surface; precedent is `internal/entrypoint/claude.go:54-72` |
| 2.2c | **Separate durable state from the pending-edit baseline** — the overlay is state; `last_render` is a one-boot write-ahead baseline (deleting it silently discards uncaptured edits — proved). Naming them differently is the prerequisite for any location change | improvement | small |
| 2.2d | **Sidecar location / scope** — becomes easy after 2.2b, because the token is no longer in the overlay | improvement | needs one ruling: **is a captured edit per-workspace or per-machine?** |

**Known regression to handle in 2.2b:** de-composing removes those surfaces from
`yolo config ls`. Fix by having `ls` list *non-rendered but yolo-touched* files too —
needed anyway, since `claude/config` is already in that category and currently mislabeled.

---

## Tranche 3 — parked design work

Do not start these by accident; each needs a decision pass, and #3.2 appears in five
places which makes it look smaller than it is.

| # | Item | Why parked |
|---|---|---|
| 3.1 | **Collapse `host_files`' four modes to three** (`copy` merges into `readonly`) | behavior change on a shipped key, and blocked on 3.2 |
| 3.2 | **`readonly` as a real `:ro` mount instead of `0o444`** — `0o444` is *asymmetric*: root ignores it, a non-root agent gets EACCES and the surface silently stops re-rendering | **a per-surface design pass**, not a code change: you cannot compose *into* a `:ro` mount, so each candidate surface must be shown to afford losing `managed`/`defaults`/`transform`. **Cheaper if 3.9 lands first** — host-side composition finishes the file before the mount exists, so `:ro` costs nothing |
| 3.3 | **Capture timing** — a `yolo config capture` subcommand, then capture in the existing `onTerminate` hook | small, but **not urgent**: nothing is lost today (every surface is under a host-backed rw bind; the edit and its baseline both survive `--rm`), only *observability* lags. An inotify watcher is **not** justified |
| 3.4 | **Comment preservation on `json`/`toml` surfaces** | needs a decision; the sub-questions (staleness → drop-on-override via existing provenance, in-jail additions → one-way host→jail, attachment → the usual convention) are already answered in host-file-staging.md, so this starts from decisions rather than blank |
| 3.5 | **`managed`/`defaults` array-append pinning** | no user surface has needed it |
| 3.6 | ~~**Non-agent prism ports**~~ — **RESOLVED 2026-07-27: not a gap.** MCP and LSP are already ported as the **computed layer** of per-agent surfaces (`copilot/mcp`, `copilot/lsp`, `agy/mcp`) — the right model, since neither has a file of its own to write. `identity` is host-composed and `:ro`-mounted by design (`gitIdentityMountArgs`). So `config render mcp` reporting "no surfaces" is CORRECT: there is no such surface, by design | — |
| 3.7 | **Rename the recovered state** — four terms for one concept; "captured edits" is the proposed umbrella, **not** "managed" (already taken) | mechanical, wants one pass |
| 3.9 | **Decide where composition runs** — host-side (before the container exists) vs in-jail (today). Available because composition **never probes the container**: every `computed` producer reads config/env/paths only, with no `os.Stat`/`exec.Command`/`LookPath` in layer construction (`prism.go:448`, `agent_configs.go:167`), and `yolo config render` already composes host-side. Moving it makes pack failures **pre-flight instead of fail-open `genStep` warnings**, lets `readonly` be a real `:ro` mount (unblocks **3.2**), and keeps pack logic outside the boundary entirely. Costs: in-jail re-render/capture timing must move host-side (**3.3** by another route), and macos-user has no mount step, so it needs a separate answer | **design decision, gates 3.1/3.2/3.3 and the pack-logic mechanism** | one decision + a real port; see [what-yolo-is.md](../design/what-yolo-is.md) |
| 3.8 | **Parameterize `/workspace` out of `builtin.go`** — claude's `defaults`/`managed` hardcode the literal path (`internal/agentcfg/builtin.go:104`), so surface data is jail-shaped even though the engine is not. Wants a `${workspace}` substitution at compose time. Small, but it is a **prerequisite** for surfaces-as-pack-data and for reusing the engine outside a jail — see [what-yolo-is.md](../design/what-yolo-is.md) | small; parked only because it has no standalone payoff yet |

---

## Doc consolidation

The reasoning docs sprawled to ~8,900 lines across 14 files with the same items restated.
`doc-triage.md` already established the policy — **A** reference (`design/`), **B** active
plan (`plans/`), **C** archive (`git rm`, history preserves it) — but it predates most of
this cluster.

Proposed, following that policy:

| Doc | Verdict |
|---|---|
| `design/composed-file-permissions.md` | **keep (A)** — the reference for postures + the audit. The one doc to read. |
| `design/packs-and-the-prism.md` | **keep (A)** — the conceptual frame; distinct audience (deciding an architecture, not implementing) |
| `design/what-yolo-is.md` | **keep (A)** — the boundaries question (what is separable from the sandbox) + how pack *logic* would ship. Answers the two questions the packs sketch left open |
| `plans/composed-config-work.md` *(this)* | **keep (B)** — the single work list |
| `plans/packs-rip-out.md` | **keep (B)** — what is left to *design* before the full rip-out, and what can start today (answer: everything in this doc's tranches) |
| `plans/host-file-staging.md` | **keep (B)**, already marked SHIPPED and closed to new scope; its "Scope: the line" is the authority on `host_files` in/out |
| `plans/agent-settings-composition.md` | **keep (A-hybrid)** — the engine design of record. Stop adding status to it |
| `plans/ROADMAP.md` | **keep (B)** — sequencing only; item 3/4 sub-tables now point here |
| `design/config-migration-to-prism.md` | **candidate C** — the cutover it describes completed 2026-07-22. Keep only if the §3.2/§3.3 sidecar state machine is not documented elsewhere |
| `design/agent-credentials.md`, `design/jail-home.md` | **keep (A)** — different questions (what crosses the boundary; how the home is built) |

**Rule going forward, to stop the sprawl recurring:** a *posture or mechanism* goes in
`composed-file-permissions.md`; a *work item* goes here; a *sequencing decision* goes in
ROADMAP. Nothing gets three homes.

---

## Corrections to fold in while touching these docs

Found during the audit; small but they are the kind of thing that makes a doc untrustworthy.

- `composed-file-permissions.md` §9 item 6 is **stale** — the `EnsureSymlink` home-root
  staging shipped 2026-07-25.
- §4.3 **overstates** the `claude/config` defect: the `ls`/`diff`/`reset` half already
  skips it; only `render` renders it.
- ~~ROADMAP + agent-settings-composition claiming `config render mise` → "no surfaces"~~ —
  **fixed 2026-07-26** (`22f7f2b`); mise is ported.
