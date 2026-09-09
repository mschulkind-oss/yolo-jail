---
status: current
verified: 2026-09-09
verified_commit: 41dde711
covers:
  - internal/cli/run/assemble.go
  - internal/cli/run/mounts.go
  - internal/cli/run/storagehelpers.go
  - internal/storage/ensure.go
  - internal/paths/paths.go
  - internal/config/validate.go
  - internal/entrypoint/shell.go
  - internal/macosuser/orchestrator.go
tags: [mise, storage, venv, state, boundary, migration]
---

# Host↔jail state separation — the split mise store, the neutral path, and per-side venvs

**Status:** CURRENT as of 2026-09-09, verified against `41dde711`.

Host and jail keep their **runtime state** fully separate. A jail never shares the host's mise
store and never shares a workspace `.venv`; the only thing crossing the boundary is the workspace
source itself, plus the lockfiles in it. Three mechanisms deliver that, and they only work
together: a **jail-land-only mise store**, mounted at a **neutral path identical in every jail on
every machine**, plus **per-side shadow mounts** for the workspace paths that hold derived state.

| Component | Lives in |
| :--- | :--- |
| The store mount, its env var, and the `MISE_ENV` wiring | `internal/cli/run/assemble.go` |
| Per-side shadow mounts, and the venv-path parse | `internal/cli/run/mounts.go` |
| The store dir, and its re-resolution at every nesting depth | `internal/paths` (`GlobalMise`), `internal/cli/run/storagehelpers.go` |
| Layout version, migration, the host-store heal | `internal/storage` (`StorageLayoutVersion`, `MigrateStorageLayout`, `FindDanglingMiseSymlinks`, `HostMiseDir`) |
| The host-side prune grant | `internal/cli/run` (`storePruneOK`) |
| `per_side_paths` shape-checking | `internal/config/validate.go` |
| The `macos-user` refusal notice | `internal/macosuser/orchestrator.go` |

**Reads with:** [`storage-and-config.md`](storage-and-config.md) (the storage roots and what each
tier shares), [`jail-home.md`](jail-home.md) (the `:ro` machine base and the per-workspace writable
overlays this leans on),
[`mise-node-dynamic-linking.md`](mise-node-dynamic-linking.md) (what a mise-installed toolchain
needs at run time),
[`../research/mise-host-jail-path-mismatch.md`](../research/mise-host-jail-path-mismatch.md) (the
per-incident investigation that motivated the store split).

For `per_side_paths` as a config key, run `yolo config-ref`.

---

## The governing rule

> **Sources and lockfiles cross the boundary. Every derived artifact — a venv, a cache, a
> toolchain store — stays on its side.**

That is the whole mental model, and everything below is its mechanism. The corollary worth stating
because it reads as a loss: cross-boundary conveniences end. Running a host-created venv's
interpreter from inside a jail worked when versions happened to align, and under the split it never
does. Little is actually lost — each side has a complete venv of its own — but the model changes.

**Drift between two per-side venvs is bounded by the lockfile.** With a lockfile in the shared
workspace, the host venv and the jail venv are two materializations of the *same* resolution, and
the lockfile is the sync channel — already on the right side of the boundary. Only an unlocked
project can genuinely diverge, and that is a pre-existing property of unlocked projects, not
something the split introduces.

## Invariants

- **No jail state references a host path, and no host state references a jail path.** The neutral
  store path is what makes the first half true; the store being jail-land-only makes the second.

- **The store path is re-resolved at every nesting depth.** A nested jail mounts the same neutral
  path, so a store entry written at depth 1 means the same thing at depth 2.

- **String-uniform, backing-per-side.** A shadow mount gives host and jail the *same* path string
  over *different* backing. That is what lets both sides use the idiomatic path — `/workspace/.venv`
  in the jail, `<repo>/.venv` on the host — with neither seeing the other's content. The same trick
  occurs naturally in the shared store and is why the common collision case is a no-op; see
  [the residue](#the-jailjail-residue-in-the-shared-store).

- **A store prune is authorized host-side and executed in-jail.** The env-var grant looks like
  indirection; it is a liveness check crossing the boundary. See the warning below.

- **The jail-land store starts cold, by decision.** A neutral-path store structurally cannot reuse
  host content, because host shebangs embed the host user's home. The shared store means each tool
  installs once for all of jail-land, not once per jail.

## Why the three changes are one change

Each part covers the others' exposure, which is why none of them ships alone.

- **Splitting the store without the neutral path** keeps the host username baked into every jail
  and keeps the outer-store plumbing alive for no remaining reason. That same-path mount existed
  *only* so host-written absolute paths would resolve in-jail — and splitting the store ends host
  writes, so the reason is gone.
- **The neutral path without per-side venvs** leaves host-created venvs **broken in-jail** — and
  silently, because the venv pre-create step skips when the directory already exists.
- **Per-side venvs make sense regardless**, because venv "sharing" was always half-broken:
  console-script shebangs embed the venv's own absolute path, which already differs across the
  boundary, and the two sides are different userlands, so a source-built C extension is only
  correct on the side that built it.

The shadow set is **`.venv` ∪ the venv path parsed out of the project's mise config ∪ the
workspace's `per_side_paths`**, one shadow mount per entry, each backed by its own directory under
the workspace's state dir.

> [!WARNING]
> **`per_side_paths` is not enforced on `macos-user`, and that backend says so by name.** There is
> no mount layer there, so a shadow cannot be expressed at all. The launch prints a warning rather
> than degrading silently — which is the whole of the mitigation, and the reason a project relying
> on a shadowed path must not assume it on that backend.

Creating a shadow mountpoint materializes an empty directory in the host workspace when the host
side does not have one. Gitignored in practice; harmless, and worth knowing before it is reported
as a bug.

## The jail↔jail residue in the shared store

The split confines this class to jail-land — the host can no longer be hit — and makes the mise
version uniform across every consumer of the store. The collision itself survives, and its shape is
worth knowing before anyone "simplifies" the fix.

**What happens.** Some mise backends record absolute paths *derived from the project directory*
into the shared store. The concrete case: a project whose mise config sets
`CARGO_HOME = "{{ config_root }}/.cargo"`, where mise's rust backend symlinks
`installs/rust/<version> → $CARGO_HOME/bin`. In the jail, `config_root` is the workspace mount
point — and **every** jail names its workspace the same thing, so the entry means something
different in every jail.

Three facts determine every failure mode:

1. **The store entry is a pointer to a project-configured location.** The store is keyed by
   `(tool, version)`, but the entry's *value* is `$CARGO_HOME/bin` — project config leaking into a
   shared, project-agnostic key. That is the root defect, and it is upstream's.
2. **Symlink targets are strings, resolved in the reader's namespace at every traversal.** The
   target means "whatever that path is *in the jail doing the exec, right now*." Shims dispatch
   through the mise binary per exec, so there is no caching: a rewrite by one jail is visible to all
   others on their very next command.
3. **Jails are mutually invisible through the store.** No jail can tell whether an entry it
   considers dangling is live for a sibling.

The four cases follow:

| Case | What happens |
| :--- | :--- |
| **Same version, same in-jail string** (the common case) | Two `{{config_root}}`-style projects both write the same target string, each resolving per-jail to its own backing. Rewrites are byte-identical no-ops. **No conflict at all** — the string-uniform/backing-per-side trick occurring naturally. |
| **Same version, different strings** | A genuine fight. Last writer wins, and per fact 2 the loser's toolchain breaks **immediately mid-session**, then heals on its next install — which breaks the other side again. Requires that specific config mismatch at the same pinned version. |
| **Unrelated jails** (no such tool) | The entry dangles in their view. Warning noise on a tolerant mise version; on an intolerant one it made *every* install fatal — which is a mise-version property, uniform and pinnable within jail-land. |
| **A rust-less jail pruning "its" dangling entry** | Per fact 3 it deletes a link that is live for a running sibling, whose toolchain then vanishes mid-session. This is why the prune is gated. |

**The layered answer, as built:**

1. **Nothing for the common case** — same-version projects with the same in-jail config string
   coexist correctly by construction.
2. **A prune, host-side and gated.** The host CLI decides whether a prune is safe — it launches the
   containers, so it is the only thing that can — and grants it to the in-jail provisioning step
   through an environment variable. The entrypoint's prune loop is **inert without that grant.**
3. **A per-project escape hatch.** `MISE_ENV=jail` lets a project keep host-inert, jail-only
   overrides in a checked-in `mise.jail.toml` — moving the offending variable off the workspace so
   the store entry points at per-jail state. This eliminates the collision for that project rather
   than healing it.

> [!WARNING]
> **Do not "simplify" the prune into the entrypoint.** Fact 3 is the whole reason the grant is
> computed host-side. An unconditional in-jail prune deletes a symlink that is live for a running
> sibling, and per fact 2 the sibling's toolchain vanishes *mid-session*, not at its next restart.
> The same fail-safe shape guards the one-time migration: it **defers rather than prunes** when it
> cannot establish that reclaiming is safe.

> [!WARNING]
> **Do not reach for a same-path workspace mount to fix this.** Mounting each workspace at its real
> host path — so `config_root` is globally unique — is the obvious move and is **actively worse
> here**. Uniqueness only helps when workspaces differ; two workspaces pinning the *same* version
> still fight over the single `installs/<tool>/<version>` entry, because the store is keyed by
> tool+version and one symlink cannot point into two projects. Worse, real host paths make every
> project's string unique, which **destroys** the no-conflict common case above: any two
> same-version projects would then conflict unconditionally. Its original benefit — host↔jail
> resolution — is already delivered by the split.

## Package caches stay per side

Asked and refused: could the uv and pip caches be shared across the boundary to avoid double
downloads? **No, and the reason is durable rather than a matter of taste.**

They are not shared today: the jail's cache dir is a yolo-owned directory shared across *jails*,
never with the host's. So this would be a new coupling, not the preservation of an existing one.

**Why it is unsound.** Those caches mix two kinds of content in one tree: portable downloads (wheel
archives, index metadata) and **locally-built wheels from sdists**. Built wheels are cached by
package plus interpreter, *not* by the userland that built them — so a source-built wheel from the
host would be silently reused inside the jail, and a jail-built one on the host. That is the
cross-userland venv hazard again, but as persistent, invisible cache poisoning. Sharing only the
safe buckets would mean binding uv-internal, version-suffixed directory names, an implementation
detail that shifts under every uv release, and it would add cross-boundary lock contention on cache
writes.

**What sharing would save**: one duplicate download-and-unpack per wheel version, once ever per
side — jail-land already amortizes across every jail through the shared cache. Seconds of network
per package, against a silent correctness hazard and a new host coupling in the design whose entire
point is removing host couplings.

## Migration

The `yolo` CLI runs on the host on every invocation, so the move to this layout is a one-time,
**versioned, host-side** routine rather than an instruction list. The global storage root carries a
layout-version marker; on the first invocation where the marker is below the current version:

1. **Create the jail-land store.** It starts cold by decision; provisioning fills it per workspace
   as usual.
2. **Heal the host store.** Scan the *host's* installs tree for symlinks whose target does not
   exist, and remove them, logging each. That deletes exactly the jail-written debris of the old
   shared-mount model and nothing else: a resolving symlink is never touched, and regular files and
   directories are never touched. No-op on the platform where the host store was never shared.
3. **Retire jail-made workspace venvs, lazily per workspace.** On the first post-upgrade launch for
   a workspace: if its venv's `pyvenv.cfg` `home =` points into a store path that does not resolve
   on the host, the venv was materialized by a jail under the old model and is broken derived state
   on the host — delete it before mounting the shadow. **A venv whose interpreter resolves
   host-side is host-owned and is left strictly alone.** Deletion is safe by construction: a venv is
   a disposable materialization of the lockfile, and this one is already unusable on the only side
   that can see it.
4. **Stamp the marker.** Every step is idempotent, so a crashed migration simply re-runs.

**Rollback is free.** The old host store is never moved or rewritten — step 2 only removes
provably-dangling symlinks, the same links mise's own rebuild pass tries to remove — so downgrading
restores the previous behavior with no data loss. Nothing in-jail needs migrating: per-jail home
state is untouched, and the shadow backing starts empty and is populated by the existing boot hook.

## What this does not cover

- **Which toolchains a workspace installs.** That is the project's mise config; this is only about
  where the store lives and who may write it.
- **The dynamic-linking story for a mise-installed toolchain.** See
  [`mise-node-dynamic-linking.md`](mise-node-dynamic-linking.md).
- **The upstream defect itself.** Layers 1–3 above **heal** the symptom; nothing here **fixes**
  fact 1, so every future backend that records a side-specific path reintroduces the class. That is
  tracked as `SS-6` — which lives in the **stub design doc**, deliberately kept at
  [`docs/design/jail-state-separation-design.md`](../design/jail-state-separation-design.md) under
  this file's own basename, because a reference doc may not carry a live `💬`. ⚠ **That target is
  the `docs/design/` twin, not this file** — a basename-driven link sweep has already "corrected" it
  to point here once.
- **Sharing anything else across the boundary.** The governing rule is the scope: sources and
  lockfiles, nothing derived.

## Why it's this way

Rulings a future change would otherwise undo, kept with their original IDs.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="ss-1"></a>[**SS-1**](#ss-1) — the store path is **`/mise`** | Short, already an empty mount point in the image, and *not* under the jail home — the `.local` there is a per-workspace overlay, so nesting a shared store inside it invites exactly the confusion the split exists to end. `/var/lib/mise` and `/opt/mise` were the rejected FHS-flavored alternatives. |
| <a id="ss-2"></a>[**SS-2**](#ss-2) — the shadow set is **both**: `.venv` ∪ the parsed venv path ∪ `per_side_paths` | Parsing catches a custom venv path with no user action; the config key covers non-python derived state — a workspace `.cargo` — that no parser would find. Neither alone is sufficient. |
| <a id="ss-3"></a>[**SS-3**](#ss-3) — the shadow backing lives in the workspace state dir, registered with prune and storage accounting | It persists across restarts, so there is no per-boot rebuild. The registration is the price, and it is a one-time cost against a per-boot one. |
| <a id="ss-4"></a>[**SS-4**](#ss-4) — the jail-land store **starts cold** | A neutral-path store structurally cannot reuse host content, so pre-warming would buy nothing but a tool-list coupling. |
| <a id="ss-5"></a>[**SS-5**](#ss-5) — wire **`MISE_ENV=jail`** | Cheap, independent of the other two changes, and it gives any project a checked-in, host-inert place for jail-only overrides — which is what makes layer 3 of the residue answer available at all. |

## Current values

Verified at `41dde711`. The prose above explains what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Neutral store path | `/mise`, exported as `MISE_DATA_DIR` | `internal/cli/run/assemble.go`; checked by `internal/cli/check/entrypoint.go` |
| Jail-land store dir, host side | `<global storage>/mise` | `paths.GlobalMise` |
| Host store dir (named for doctor and prune only, never a mount target) | — | `storage.HostMiseDir` |
| Storage layout version | 2 — the split store | `storage.StorageLayoutVersion` |
| Shadow backing | one dir per shadowed relative path, under the workspace state dir's `venv-shadows/`, with `/` replaced by `__` | `internal/cli/run/mounts.go`; `yolo config-ref` |
| Prune grant | `YOLO_STORE_PRUNE_OK=1`, computed by `storePruneOK` | `internal/cli/run/assemble.go`, `internal/cli/run/command.go` |
| Per-project override env | `MISE_ENV=jail`, reading `mise.jail.toml` | `internal/cli/run/assemble.go`; precedence noted in `internal/entrypoint/shell.go` |
| Config key | `per_side_paths` | `internal/config/validate.go`; `yolo config-ref` |
