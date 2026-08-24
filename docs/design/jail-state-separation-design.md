# Full host↔jail state separation — split mise store + neutral path + per-side venvs

**Status:** IMPLEMENTED (2026-07-03), **re-verified against the Go tree 2026-08-23.**
All three changes shipped, and so did every item of the Residual-issue leaning except
the upstream bug report. Durable reference.

> [!NOTE]
> **Postscript, 2026-08-23 — the design is live; every `.py` citation in it is dead.**
> This doc was written against the Python implementation, and yolo has since been
> ported to Go in full (`fd -e py` over the repo returns nothing, verified
> 2026-08-23). So **the mechanisms below are exactly what runs — the file:line
> anchors are not.** Do not go looking for `run_cmd.py:1163`; the table immediately
> after this note is the current map. §"The design in one view" onward is preserved
> in its original tense, as the argument for why the three changes only make sense
> together — which is still the thing worth reading here.
>
> | Doc says (Python) | Actually lives at (Go, verified 2026-08-23) |
> |---|---|
> | `run_cmd.py:1163` mount source | `internal/cli/run/assemble.go:566` (`-e MISE_DATA_DIR=/mise`); store dir is `paths.GlobalMise()` = `<GlobalStorage>/mise` (`internal/paths/paths.go:322-323`) |
> | `MISE_DATA_DIR` in the env block | `internal/cli/run/assemble.go:566`; check path `internal/cli/check/entrypoint.go:40` |
> | `YOLO_OUTER_MISE_PATH` deletion | **done** — zero occurrences in code; only these docs still name it |
> | nested-jail store re-resolution | `internal/storage/ensure.go:206-210` and `internal/cli/run/storagehelpers.go:22` (`/mise` at every nesting depth) |
> | venv shadow `ws_state/venv-<slug>` | `per_side_paths` is a real config key — `internal/config/validate.go:354-365`, `internal/config/config.go:62`, documented at `internal/cli/config_ref.txt:598-601` |
> | migration layout-version marker | `internal/storage/ensure.go:19-22` (`StorageLayoutVersion = 2`) + `MigrateStorageLayout` at `:246-273`, with `FindDanglingMiseSymlinks(HostMiseDir())` as step 2 |
> | `MISE_ENV=jail` | `internal/cli/run/assemble.go:571`; the jail-pair precedence is documented at `internal/entrypoint/shell.go:346` |
>
> One behavioral gap the port surfaced and this doc never named: **`per_side_paths`
> is not enforced on the `macos-user` backend**, which warns by name rather than
> degrading silently (`internal/macosuser/orchestrator.go:150-164`). That backend did
> not exist when this was written.

**Reference** — how host and jail keep their runtime state fully separate. This
is the live model (shipped 2026-07-03): the split mise store, the neutral `/mise`
path, and per-side venv shadows. Read it to understand *why* a jail never shares
the host's mise store or `.venv` and how the boundary is drawn. The original
per-incident investigation that motivated the mise-store split is recorded in
[mise-host-jail-path-mismatch.md](../research/mise-host-jail-path-mismatch.md).

## What shipped (vs. the original sketch)

Shipped as designed, with two deliberate deviations from the sketch:

- **Store dir reuses the existing `GLOBAL_MISE`** constant
  (`~/.local/share/yolo-jail/mise`, src/cli/paths.py) rather than the
  sketch's example `~/.local/share/yolo/mise-jail` — the dir already
  existed, was mkdir'ed on every run, and was wired into prune/storage
  accounting and the `yolo check` storage listing.
- **Venv shadow backing lives at `ws_state/venv-shadows/<slug>`**
  (`<workspace>/.yolo/home/venv-shadows/`, one dir per shadowed relative
  path with `/` replaced by `__`) rather than the sketch's bare
  `ws_state/venv` — one backing dir per entry in the shadow set
  (`.venv` ∪ parsed mise venv path ∪ `per_side_paths`).

## The design in one view

Three changes that only make sense together:

| # | Change | Mechanism |
|---|---|---|
| 1 | **Split the mise store** — jails stop sharing the host's `~/.local/share/mise`; all jails share a jail-land-only store | Swap the Linux mount source (run_cmd.py:1163) from the host dir to a yolo-owned dir (or named volume). macOS already works this way (`yolo-mise-data` volume, run_cmd.py:1070) |
| 2 | **Neutral store path** — mount that store at a fixed path, identical in every jail on every machine | Mount at `/mise` (empty mount point already in the image, flake.nix:998); export `MISE_DATA_DIR=/mise`; delete the `YOLO_OUTER_MISE_PATH` nested-jail plumbing (run_cmd.py:1895 → storage.py:154) |
| 3 | **Per-side venvs** — the workspace `.venv` is no longer a shared artifact | Shadow mount: `-v {ws_state}/venv:/workspace/.venv` (plus any venv path parsed from `mise.toml`). Host sees its venv, jail sees its own, both at the idiomatic path |

Mount table, before → after (Linux):

```
before:  ~/.local/share/mise  ─────►  /home/matt/.local/share/mise   (host's real dir, host path string)
         (workspace .venv shared implicitly via /workspace bind)

after:   ~/.local/share/yolo/mise-jail  ─────►  /mise                (jail-land store, fixed path)
         {ws_state}/venv                ─────►  /workspace/.venv     (per-side venv shadow)
```

After this, **no jail state references a host path, no host state references
a jail path, and the only thing crossing the boundary is the workspace
source itself.**

## Why bundled (each part covers the others' exposure)

- Splitting the store (1) without the neutral path (2) keeps the host
  username baked into every jail and keeps the `YOLO_OUTER_MISE_PATH`
  plumbing alive for no remaining reason — the same-path mount existed
  *only* so host-written absolute paths resolve in-jail (run_cmd.py:1153-1156
  says exactly this), and (1) ends host writes.
- The neutral path (2) without per-side venvs (3) leaves host-created
  `.venv`s **broken in-jail** — worse, the venv pre-create hook
  (shell.py:291) skips when the dir exists, so the breakage is silent.
- Per-side venvs (3) make sense regardless, because venv "sharing" was
  always half-broken: console-script shebangs embed the venv's own absolute
  path, which already differs host↔jail (`~/code/<proj>/.venv` vs
  `/workspace/.venv`), and host (Arch) and jail (NixOS) are different
  userlands — a source-built C extension is only correct on the side that
  built it.

## Pros

**Correctness — whole bug classes die, not instances:**

- Host `mise install` can never again be broken by a jail, and vice versa
  (the original dangling-symlink incident becomes impossible, not just
  repaired).
- Host↔jail mise **version skew stops mattering** — no skew guard, no
  host-pinning story, no "how does the host update mise" question. The
  host's mise lifecycle is fully its own.
- Venvs are correct per side by construction: no dangling interpreters, no
  cross-OS native-extension hazard, no shebang mismatch. The pre-create
  hook flips from footgun (skips past a broken host venv) to mechanism
  (populates the empty jail view on first boot).

**Predictability — the "fully predictable jails" goal:**

- Identical mount tables on every machine: no host username/home layout
  leaks into the jail. A jail on gauss, a Mac, or CI is the same jail.
- One storage model across runtimes — Linux adopts what macOS already
  ships, deleting the platform fork at run_cmd.py:1163.
- Jail-land runs exactly one mise version per image (managed by `flake.lock` +
  the weekly `update-flake-lock` CI bump).

**Efficiency retained:**

- Jail↔jail install sharing survives — the store split is host-vs-jails,
  not per-jail. First jail installs node/python/go once for all of
  jail-land (the cost that killed the old per-jail-overlay idea stays
  dead).
- Venv recreation is cheap: uv's wheel cache is already jail-shared via
  the `GLOBAL_CACHE` mount (run_cmd.py:1062/1108), and the shadow backing
  in `ws_state` persists across restarts. Same-filesystem placement lets
  uv hardlink instead of copy.

**Less code:**

- `YOLO_OUTER_MISE_PATH` propagation deleted; nested jails just re-mount
  the constant path.
- Everything in-jail already derives from `MISE_DATA_DIR`
  (entrypoint/__init__.py:67 and friends) — the neutral path is a value
  change, not a refactor.

## Cons / costs

- **Cold start for jail-land.** Host-store contents can't seed a
  neutral-path store (host shebangs embed `/home/matt/…`), so every
  tool@version installs once more, in-jail, on first use. One-time,
  amortized across all jails; downloads hit the shared cache thereafter.
- **Disk duplication.** Host copy + jail-land copy of overlapping
  toolchains, plus one venv per side per workspace. Modest (single-digit
  GB for typical toolsets); `ws_state/venv` must be registered with
  prune/storage accounting.
- **Cross-boundary conveniences end.** `python -m …` against a
  host-created venv worked in-jail when versions aligned; under the split
  it never does (each side has a complete venv of its own, so little is
  actually lost — but the mental model changes: *derived state never
  crosses*).
- **"Drift" between the two venvs is bounded by the lockfile.** With
  `uv.lock`/pinned requirements (in the shared workspace), the host and
  jail venvs are two materializations of the *same* resolution — the
  lockfile is the sync channel, and it's already on the right side of the
  boundary. Only unlocked projects can genuinely diverge, and that's a
  preexisting property of unlocked projects (two machines, two checkouts —
  same story), not something the split introduces.
- **Mountpoint artifacts.** If the host workspace lacks `.venv`, creating
  the shadow mountpoint materializes an empty `.venv/` dir in the host
  workspace (gitignored in practice; can be created proactively).
- **Doesn't fix the jail↔jail residue** — the shared jail store can still
  hold workspace-derived paths that are wrong for other workspaces. Full
  description and fix options below (§ "Residual issue").
- **Verification burden.** Nested jails (inner mount of `/mise` and a
  second venv shadow), Apple Container mount quirks
  (apple/container#1089 single-file mount bug — the venv shadow is a
  directory mount, which is the safe kind), and at least one real project
  (songtv/polyclav) need an end-to-end pass.
- **Migration.** Handled automatically on first post-upgrade `yolo`
  invocation — no manual steps; see § "Migration" below.

## Residual issue: jail↔jail workspace-path collisions in the shared store

**What happens.** Some mise backends record absolute paths derived from the
project directory into the shared store. Concretely: polyclav's `mise.toml`
sets `CARGO_HOME = "{{ config_root }}/.cargo"`, and mise's rust backend
symlinks `installs/rust/<version> → $CARGO_HOME/bin`. Inside polyclav's
jail, `config_root` is `/workspace`, so the *shared jail store* gains
`installs/rust/1.95.0 → /workspace/.cargo/bin`. Every jail names its
workspace `/workspace`, so that entry means something different in every
jail: in polyclav's it resolves correctly; in a workspace that has no
`.cargo`, it dangles; in an unrelated rust workspace it silently points at
the *wrong* toolchain. Depending on mise version, a dangling entry ranges
from harmless (2026.2.1 tolerates it) to fatal for every `mise install`
(observed on 2026.6.11 — the original incident).

The bundle confines this to jail-land (the host can no longer be hit) and
makes the mise version uniform across all consumers of the store, but the
collision itself survives. Fix options:

### Mechanics: when does it actually break? (added 2026-07-03)

Three facts determine the failure modes:

1. **The store entry is a pointer to a project-configured location.** The
   store is keyed by (tool, version), but the rust backend's entry *value*
   is `$CARGO_HOME/bin` — project config leaking into a shared,
   project-agnostic key. That's the root defect (and the upstream issue:
   the key should include the target, or the rebuild should tolerate
   dangling entries).
2. **Symlink targets are strings, resolved in the reader's namespace at
   every traversal.** `→ /workspace/.cargo/bin` means "whatever
   `/workspace/.cargo/bin` is *in the jail doing the exec, right now*."
   Shims dispatch through the mise binary per exec, so there is no
   caching: a rewrite by one jail is visible to all others on their very
   next command.
3. **Jails are mutually invisible through the store.** No jail can tell
   whether an entry it considers dangling is live for a sibling.

Consequences, by case:

- **Same version, same in-jail string (the common case).** Two
  `{{config_root}}/.cargo`-style projects both write
  `→ /workspace/.cargo/bin`; default-CARGO_HOME projects both write
  `→ /home/agent/.cargo/bin`. Identical strings, each resolving per-jail
  to that jail's own backing — rewrites are byte-identical no-ops.
  **No conflict at all.** This is the same string-uniform/per-side-backing
  trick the venv shadow uses, occurring naturally. (Corollary: option A
  would *destroy* this — real host paths make every project's string
  unique, so any two same-version projects would conflict
  unconditionally. A is not just weaker here; it's actively worse.)
- **Same version, different strings** (workspace-cargo project vs
  default-cargo project): genuine fight. Last writer wins; per fact 2 the
  loser's `cargo` breaks **immediately mid-session** (not at restart) and
  heals without restart on its next `mise install`/relink — which breaks
  the other side again. This is the true ping-pong, and it requires that
  specific config mismatch at the same pinned version.
- **Unrelated jails** (no rust): the entry dangles in their view. On
  tolerant mise (2026.2.1) that's warning noise; on 2026.6.11-style
  behavior it made every `mise install` fatal — which is a *mise version*
  property, uniform and pinnable in jail-land.
- **Naive boot-time prune is not safe.** Per fact 3, a rust-less jail
  pruning "its" dangling entry deletes a link that is live for a running
  sibling — per fact 2 the sibling's toolchain vanishes mid-session. A
  prune is only safe when no other jail is running, which the host-side
  `yolo` CLI *can* determine (it launches the containers) — so the prune
  belongs in the CLI at jail-start, gated on zero live jails, not in the
  entrypoint unconditionally.

- **Same-path workspace mount (option A in the mismatch doc)** — mount each
  workspace at its real host path, keep `/workspace` as a symlink, so
  `config_root` is globally unique.
  - \+ Store entries become unambiguous — no two workspaces can write the
    same path string meaning different things.
  - ‒ **Weaker than it looks under this bundle:** uniqueness only helps
    when workspaces differ. Two workspaces pinning the *same* rust version
    still fight over the single `installs/rust/1.95.0` entry — the store
    is keyed by tool+version, not by workspace, so one symlink cannot
    point into two projects. A dangling/wrong entry for one of them is
    structural, not a path accident.
  - ‒ Changes a documented invariant (`/workspace` everywhere), needs a
    canonicalization test pass, and its *original* main benefit —
    host↔jail resolution — is already delivered by the split.
- **Boot-time prune (option C)** — at jail start, remove store symlinks
  whose targets don't exist in this jail's view; mise reinstalls/relinks on
  demand.
  - \+ Cheap, version-independent insurance; also self-heals any future
    backend that writes side-specific paths, not just rust.
  - \+ Relink is fast (the toolchain files live in the workspace's
    `.cargo`, which is intact — only mise's pointer is rebuilt).
  - ‒ Ping-pong churn: two rust workspaces alternate ownership of the
    entry on every boot. Bounded cost, but visible.
- **Per-project escape hatch (`MISE_ENV=jail` + `mise.jail.toml`)** — move
  `CARGO_HOME` off the workspace (e.g. under the jail home) for projects
  that trip this, so the store entry points at per-jail state.
  - \+ Actually eliminates the collision for that project instead of
    healing it; host-inert.
  - ‒ Per-project opt-in, doesn't protect the store from the next
    offending project.
- **Upstream (option E)** — mise could key such entries per-target or
  tolerate dangling ones during symlink rebuild. Worth filing; not a fix
  to wait on.

_Leaning (revised 2026-07-03 after the mechanics analysis):_ layered —

1. **Do nothing for the common case**: same-version projects with the same
   in-jail config string coexist correctly by construction; document it.
2. **Prune, but host-side and gated**: `yolo` prunes dangling store
   symlinks at jail launch *only when no other jail is running* (the CLI
   can check) — never unconditionally in the entrypoint, which can break
   a live sibling mid-session.
3. **`mise.jail.toml` normalization** for the rare same-version,
   different-string pair that genuinely ping-pongs.
4. **Upstream issue** for the root defect (project config leaking into a
   project-agnostic store key).

Option A is rejected outright for this problem: beyond not earning its
invariant change, unique real paths would make *every* same-version pair
conflict instead of almost none (see Mechanics).

> **Layers 1–3 shipped; verified 2026-08-23.** The gate in layer 2 is the
> load-bearing part and it is built exactly as specified: the **host** CLI decides
> whether a prune is safe (`storePruneOK`, `internal/cli/run/run.go:450-454`,
> `internal/cli/run/assemble.go:47-77`) and grants it to the in-jail provisioning
> step with `-e YOLO_STORE_PRUNE_OK=1`; the entrypoint's prune loop is inert
> without that grant (`internal/cli/run/command.go:9-10` — `if [ "${YOLO_STORE_PRUNE_OK:-0}" = "1" ]`).
> The same fail-safe shape guards the one-time migration:
> `MigrateStorageLayout(insideJail, canReclaim, warnf)` **defers rather than prunes**
> when `canReclaim` is nil or false, i.e. on unknown/live siblings
> (`internal/storage/ensure.go:246-273`). Layer 3's escape hatch is wired
> (`MISE_ENV=jail`, `internal/cli/run/assemble.go:571`).

> [!WARNING]
> **Do not "simplify" the prune into the entrypoint.** Fact 3 above — jails are
> mutually invisible through the store — is the whole reason the grant is computed
> host-side. An unconditional in-jail prune deletes a symlink that is live for a
> running sibling, and per fact 2 the sibling's toolchain vanishes *mid-session*,
> not at its next restart. The env-var grant looks like indirection; it is the
> liveness check crossing the boundary.

## Evaluated and rejected: sharing uv/pip caches host↔jail

Asked 2026-07-03: could the package caches be shared across the boundary to
avoid double downloads?

**Baseline first:** they aren't shared today. The jail's `~/.cache` comes
from `GLOBAL_CACHE` (paths.py:28), a yolo-owned directory — shared across
*jails*, never with the host's `~/.cache`. So this would be a new coupling,
not the preservation of an existing one.

**Why it's unsound:** uv and pip caches mix two kinds of content in one
tree — portable downloads (wheel archives, index metadata: `wheels-v*`,
`simple-v*`) and **locally-built wheels from sdists** (`builds-v*`,
`sdists-v*`). Built wheels are cached by package + interpreter, *not* by
the userland that built them. A source-built wheel from the Arch host
(linking `/usr/lib/…`) would be silently reused inside NixOS jails, and a
jail-built one (referencing `/nix/store/…`) on the host — the venv
cross-OS hazard again, but as persistent, invisible cache poisoning.
Sharing only the safe buckets would mean binding uv-internal,
version-suffixed directory names (the live cache currently holds both
`simple-v20` *and* `simple-v21`) — an implementation detail that shifts
under every uv release. Plus cross-boundary lock contention on cache
writes.

**What sharing would actually save:** one duplicate download-and-unpack per
wheel version, once ever per side — jail-land already amortizes across all
jails via `GLOBAL_CACHE`. Seconds of network per package, against a silent
correctness hazard and a new host coupling in the design whose entire point
is removing host couplings.

**Verdict: keep caches per side.** Sources and lockfiles cross the
boundary; every derived artifact (venv, cache, store) stays on its side.

## Implementation sketch

1. `run_cmd.py:1163` — mount source becomes a yolo-owned dir (e.g.
   `~/.local/share/yolo/mise-jail`), target becomes `/mise`; same change
   collapses the macOS branch (volume → `/mise`) into one line.
2. `MISE_DATA_DIR=/mise` in the env block (run_cmd.py:1183) and the check
   path (run_cmd.py:550).
3. Delete `YOLO_OUTER_MISE_PATH` (run_cmd.py:1895, storage.py:154);
   `_host_mise_dir()` becomes host-only (it still names the *host's* dir
   for doctor/prune purposes, never a mount target).
4. Venv shadow: at mount assembly, union `[".venv"]` with the
   `env._.python.venv.path` parsed from `mise.toml`/`.mise.toml` (parser
   logic already exists in the pre-create script) and any
   `per_side_paths` from `yolo-jail.jsonc`; emit one
   `-v {ws_state}/venv-<slug>:{workspace_path}` per entry.
5. Register `ws_state/venv*` and the jail-land store with prune/storage
   accounting; update `storage-and-config.md` (the mount-rationale comment
   at run_cmd.py:1153 and doc line 82/274 describe the old invariant).
6. Migration routine per § "Migration": layout-version marker in
   `GLOBAL_STORAGE`, host-store dangling-symlink prune, lazy per-workspace
   retirement of jail-made venvs.
7. Verify: nested jail boot, songtv provisioning (with the independent
   `.mise.toml` trust fixes), polyclav rust flow, macOS/Apple Container
   run.

## Migration (automatic — no manual steps)

The `yolo` CLI runs on the host on every invocation, so migration is a
one-time, versioned, host-side routine — not an instruction list for the
user. `GLOBAL_STORAGE` gains a layout-version marker file; on the first
`yolo run`/`yolo check` where the marker is below the current version:

1. **Create the jail-land store** (`~/.local/share/yolo/mise-jail`) —
   starts cold by decision (see Open Questions); provisioning fills it per
   workspace as usual.
2. **Heal the host store.** Scan the *host's*
   `~/.local/share/mise/installs/*/*` for symlinks whose target does not
   exist and remove them, logging each. This deletes exactly the
   jail-written debris of the old shared-mount model (the
   `→ /workspace/…` entries that break host `mise install`) and nothing
   else — a resolving symlink is never touched, and regular files/dirs are
   never touched. It subsumes the previously-manual `rm` of the two
   dangling rust entries. (No-op on macOS, where the host store was never
   shared.)
3. **Retire jail-made workspace venvs, lazily per workspace.** On the
   first post-upgrade `yolo run` for a workspace: if `<ws>/.venv` exists
   and its `pyvenv.cfg` `home =` points into a mise store path that does
   not resolve on the host, the venv was materialized by a jail under the
   old model and is broken derived state on the host — delete it before
   mounting the shadow. A venv whose interpreter resolves host-side is
   host-owned and is left strictly alone. (Deletion is safe by
   construction: venvs are disposable materializations of the lockfile;
   this one is *already unusable* on the only side that can see it.)
4. **Stamp the marker.** Every step is idempotent, so a crashed migration
   simply re-runs.

Rollback is free: the old host store is never moved or rewritten (step 2
only removes provably-dangling symlinks — the same links mise's own
rebuild pass tries to `rm`), so downgrading yolo restores the previous
behavior with no data loss. Nothing in-jail needs migrating: per-jail home
state is untouched, `ws_state/venv` starts empty and is populated by the
existing boot hook.

Out of scope but adjacent (tracked in the source docs): the three
filename-gated `mise trust` hooks, the no-op `MISE_TRUST=1`, the
provisioning-failure handling (decided: pause with continue/abort prompt,
persist the startup log, breadcrumb its path + an error flag into the
generated agents file — see the mismatch doc's Open Questions), weekly
`flake.lock` CI.

## Decision Ledger

All five were the design's open questions; all were settled 2026-07-03 and all are
live in the code. IDs (`SS-*`) are **new as of the 2026-08-23 compaction** — nothing
outside this doc cited these decisions, so no cross-reference was broken by naming
them. The "why" column is the load-bearing half: it is the reason the code looks the
way it does, and it is the part that would be re-derived expensively if deleted.

| ID | Ruling | Why (kept, not archaeology) | Date | Live at (verified 2026-08-23) |
| :--- | :--- | :--- | :--- | :--- |
| SS-1 | Store path is **`/mise`** | Short, already an empty mount point in the image, and *not* under `/home/agent` — `.local` there is a per-workspace overlay, so nesting a shared store inside it invites exactly the confusion the split exists to end. `/var/lib/mise` and `/opt/mise` were the rejected FHS-flavored alternatives. | 2026-07-03 | `internal/cli/run/assemble.go:566`; `internal/storage/ensure.go:206-210` re-resolves it at every nesting depth |
| SS-2 | Shadow list is **both**: `[".venv"]` ∪ parsed `_.python.venv` ∪ `per_side_paths` | Parsing catches custom venv paths with no user action; the config key covers non-python derived state (a workspace `.cargo`) that no parser would find. Neither alone is sufficient. | 2026-07-03 | `internal/config/validate.go:354-365`; `internal/cli/config_ref.txt:598-601` |
| SS-3 | Venv shadow backing lives at **`ws_state/venv…`**, registered with prune/storage accounting | Persists across restarts, so no per-boot rebuild; the registration is the price, and it is a one-time cost against a per-boot one. | 2026-07-03 | `<workspace>/.yolo/home/venv-shadows/` per `internal/cli/config_ref.txt:601` |
| SS-4 | The jail-land store **starts cold** | A neutral-path store structurally cannot reuse host content (host shebangs embed `/home/<user>/…`), so pre-warming would buy nothing but a tool-list coupling. The shared store means each tool installs once for all of jail-land, not once per jail. | 2026-07-03 | `MigrateStorageLayout` creates and stamps; no seeding step exists (`internal/storage/ensure.go:246-273`) |
| SS-5 | **Wire `MISE_ENV=jail`** | Cheap, independent, and it replaces the old option-D idea: any project can keep host-inert jail-only overrides in a checked-in `mise.jail.toml`. Precedence was to be verified against mise docs during implementation. | 2026-07-03 | `internal/cli/run/assemble.go:571`; precedence documented `internal/entrypoint/shell.go:346` |

## Open Questions

1. 💬 **SS-6: File the upstream mise issue for the project-agnostic store key.**
   Layer 4 of the Residual-issue leaning — the root defect is that mise's rust
   backend writes a *project-configured* value (`$CARGO_HOME/bin`) into a store entry
   keyed only by (tool, version). Layers 1–3 shipped and **heal** the symptom; nothing
   yet **fixes** it, so every future backend that records a side-specific path
   reintroduces the class. This decides whether yolo carries the gated-prune
   machinery indefinitely or eventually deletes it.

   _Leaning:_ File it, expect nothing, keep the prune. The leaning already said "worth
   filing; not a fix to wait on," and 2026-08-23 changes nothing about that — but the
   filing itself is still unrecorded, which is the only reason this is open rather
   than settled. I could not verify from inside the repo whether an issue exists.

   **Answer:**
   > _(empty — fill in when decided)_

<!-- changelog -->
- [ebdc38c1] Broke the bullet out into a "Residual issue" section: concrete failure walk-through, four fix options with pros/cons, and a revised leaning (prune over option A, whose same-version collision gap the bundle exposes)
- [935888b2] Replaced the debris bullet with a "Migration" section: automatic one-time routine (layout-version marker, host-store dangling-symlink prune, lazy retirement of jail-made venvs), idempotent, free rollback, zero manual steps
