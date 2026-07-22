# Cache relocation — let a cache subdir live on other storage

**Status:** **Implemented 2026-07-21** — work items 1–10 landed. The host-gated
acceptance step is now **done**: a real cross-filesystem HuggingFace-cache move
to cold storage was verified on the maintainer's host (2026-07-22). Item 11
(`yolo cache relocate`) is **held pending a design question, not merely
deferred** — the maintainer flagged a worry that the whole `cache_relocations`
mechanism may sit at the wrong level of abstraction. Do not build item 11 until
that is resolved; see [Is `cache_relocations` the right
level?](#is-cache_relocations-the-right-level-held) under Open Questions.
**Filed:** 2026-07-21.

## The problem

`~/.local/share/yolo-jail/cache` is a single directory on whatever filesystem
`$HOME` sits on, and some of its subdirs get very large. On the machine that
prompted this, the cache had grown to **241 GiB**, of which **185 GiB was
`cache/huggingface`** — about fifteen diffusers model repos (FLUX.1-schnell
32G, FLUX.1-Kontext-dev 32G, SDXL-base 20G, SD-3.5-medium 16G, …). That was
the single largest consumer on a 948 GiB root filesystem that had hit 100%
full, while an 11 TB HDD sat at 53%.

These are exactly the bytes you want on cheap storage: enormous, write-once,
read-sequentially, and cold most of the time. Nothing about them wants to be on
NVMe. But today there is no supported way to say so — `GlobalCache()` is
`filepath.Join(GlobalStorage(), "cache")` with no override
(`internal/paths/paths.go:73`), and `GlobalStorage()` is a hardcoded
`$HOME/.local/share/yolo-jail` (`paths.go:64`).

## Why the two obvious workarounds don't work

### Symlinking the subdir dangles inside the jail

The whole cache dir is bind-mounted into the container as one unit:

```go
"-v", paths.GlobalCache()+":/home/agent/.cache",
```

— `internal/cli/run/assemble_parts.go:50` (podman) and `:23` (Apple Container).

Podman resolves the *source* path of that `-v`, not the symlinks inside it. So
if you replace `cache/huggingface` with a symlink to `/data/…`, the container
gets a symlink pointing at `/data/…` — a path that does not exist in the
container's mount namespace. Every in-jail HF download then fails on a dangling
path, and the failure mode is confusing because the same symlink works fine
when you `ls` it on the host.

**This makes the current `yolo prune` hint actively misleading**
(`internal/prune/prunecmd.go:194-197`):

> hint: cache/images holds 41.8 GiB of jail tarballs. They're streamed once at
> podman load then unused — consider symlinking this subdir to HDD storage if
> you have it.

The advice happens to be *correct for `cache/images` specifically* — that subdir
is only ever read host-side, by `internal/image/autoload.go:166` and
`internal/image/image.go:141`, before any container exists — but it reads as a
general technique. A user who follows it for `cache/huggingface` (the far bigger
prize, and one of the two `CachePurgeHeavySubdirs` in
`internal/prune/cachepurge.go:17`) breaks their jail. Work item 8 fixes the hint.

### `mounts` is read-only and project-scoped

`config.mounts` looks close, but it is hardcoded read-only —
`internal/cli/run/assemble.go:123` appends `":ro"` unconditionally — and the
README documents that as a security property ("Extra mounts are read-only by
default"). A HuggingFace cache needs write access even on a pure cache *hit*:
`huggingface_hub` takes lock files and writes `.no_exist` marker files.

Relaxing `mounts` to allow `:rw` would also be the wrong shape — see
[Threat model](#threat-model-why-user-scope-is-the-whole-design) below.

## Proven behavior (podman 5.8.4, 2026-07-21)

The design rests on nested bind mounts working under `--read-only`. That is no
longer an assumption — it was run before this plan was written:

```
podman run --rm --read-only --read-only-tmpfs=false --userns=host --net=host \
  --cgroups=disabled \
  -v $PWD/outer:/home/agent/.cache \
  -v $PWD/inner:/home/agent/.cache/huggingface \
  alpine sh -c 'ls /home/agent/.cache; cat .../huggingface/marker.txt;
                echo x > /home/agent/.cache/huggingface/new.txt'
```

| Question | Result |
|---|---|
| Nested mount under `--read-only`? | **Works.** Host file readable in-jail, in-jail write landed in the host target. |
| Is argv order load-bearing? | **No.** Reversing the two `-v` args behaves identically — podman sorts mounts by destination depth. |
| Who creates the mountpoint? | **Podman**, on the *host* side, inside the parent bind source (`outer/huggingface`, root-owned `drwxr-xr-t`). We create it ourselves instead, with known ownership and perms. |
| Missing target dir? | **Hard failure**, not a skip: `Error: statfs /…/typo/oops: no such file or directory`. The container never starts. |

Two consequences the earlier draft of this plan had backwards, now folded in:
ordering is *not* an invariant to pin in a golden test (work item 5), and a
missing target must be an **error**, never a warning (work item 2).

## Design

A **user-scope-only** `cache_relocations` map: cache subdir name → absolute host
path, mounted read-write nested inside the existing `.cache` mount.

```jsonc
// ~/.config/yolo-jail/config.jsonc — user scope ONLY
{
  "cache_relocations": {
    "huggingface": "/data/relocated/yolo-jail/cache/huggingface"
  }
}
```

Assembly appends, adjacent to the base `.cache` mount:

```
-v /home/matt/.local/share/yolo-jail/cache:/home/agent/.cache
-v /data/relocated/yolo-jail/cache/huggingface:/home/agent/.cache/huggingface
```

In-jail, `~/.cache/huggingface` is an ordinary writable directory. `HF_HOME`
and every other tool's cache path stay exactly as they are — nothing downstream
needs to know.

**Scope:** podman only. Apple Container warns and skips (work item 6); the
`macos-user` backend is unaffected — it has no container and no bind mounts (no
`GlobalCache()` reference outside `assemble_parts.go`), so a plain host symlink
already works there.

### Why keys are subdir names, not paths

The container destination is always `/home/agent/.cache/<key>` and is never
caller-supplied, so this cannot be used to mount over `/etc`, `/opt/yolo-jail`,
or the workspace. Validation additionally rejects any key that is not a single
non-`.`/`..` path segment.

### Threat model: why user-scope is the whole design

The cache directory is bind-mounted **read-write into the jail** — `~/.cache`
in the container *is* the host's `GlobalCache()`. So:

> **Anything derived from the cache dir's contents is agent-controlled.**

That single fact kills the tempting simpler design, and it should be recorded
here because it will be re-proposed: *"skip the config key — just scan
`GlobalCache()` for symlinked subdirs and mount each resolved target."* It needs
no config, no scope rules, and it would make the existing prune hint true. It is
also an **arbitrary-host-path read-write mount primitive**: an in-jail agent
writes `~/.cache/evil -> /etc`, and the next *host*-launched jail mounts host
`/etc` read-write at `~/.cache/evil`. Today that symlink is inert. Rejected.

The same reasoning constrains where the config may be read from. Three
locations could carry the key; two are jail-writable:

| Source | Jail-writable? | Verdict |
|---|---|---|
| Workspace `yolo-jail.jsonc` / `.local.jsonc` | **Yes** — `/workspace` is bind-mounted rw | Rejected as a validation error |
| `<workspace>/.yolo/config-snapshot.json` | **Yes** — same mount; read verbatim in-jail (`config/load.go:225-236`, `snapshot.go:19`) | Never consulted for this key |
| Host `~/.config/yolo-jail/config.jsonc` | **No** — mounted into the jail **read-only** (`userConfigMountArgs`, `assemble_parts.go`) | **The only source** |

Hence: **`cache_relocations` is read directly from `paths.UserConfigPath()` at
assemble time and never from the merged config.** Workspace scope becomes
inexpressible by construction; the validation error is defense-in-depth against
silent no-ops, not the security boundary. This is the *mechanism* the Lua
transform uses (fixed path, `internal/cli/config.go:224`) — note it is not the
same *policy*, since that loader deliberately reads both scopes.

**Correction (found in review, 2026-07-21).** An earlier draft of this table
claimed the jail's `~/.config` is purely a per-workspace overlay, so the host
user config was unreachable in-jail. That is wrong: `userConfigMountArgs`
(`internal/cli/run/assemble_parts.go`) additionally bind-mounts the host's
`config.jsonc` over that overlay, **read-only** — confirmed in a jail's
`/proc/self/mountinfo`. The security conclusion is unchanged and in fact
stronger (read-only, so never agent-writable), but the key *is* visible inside a
jail, and that had a sharp consequence: the validator's "target's parent must
exist" probe ran in-jail against host paths that are deliberately not in the
jail's mount namespace, turning a valid host config into a fatal
`Invalid jail config` on **every nested `yolo` run and every in-jail
`yolo check`**. Two independent routes carried the key in (this mount, and the
host-written `config-snapshot.json` that `LoadConfig` prefers in-jail).

Resolution: relocation is a **host-side-only** feature. `LoadCacheRelocations`
returns nothing in-jail (a nested container cannot mount a host path it cannot
see, and its `GlobalCache()` is its own per-workspace dir anyway), and
`validateCacheRelocations` gates only the *filesystem* probe on `inJail()` —
shape, scope and duplicate rules still apply everywhere, so host-side typo
protection is untouched.

## Work items

Phase 1 is the feature. Phase 2 is what stops the feature from making
`yolo prune` lie. Do not ship 1 without 2.

### Phase 1 — mount it

1. **`internal/config/relocations.go` (new).**
   `type CacheRelocation struct{ Subdir, Target string }` and
   `LoadCacheRelocations(warn Warn) ([]CacheRelocation, error)`, which loads
   *only* `paths.UserConfigPath()` via `LoadJSONCWithIncludes` (its
   `include_if_found` files are host-side too, so they are fine), validates, and
   returns the entries **sorted by `Subdir`** so argv is deterministic.
2. **Validation rules**, shared by the loader and `yolo check`:
   - key: single path segment, non-empty, no `/`, not `.` or `..`;
   - value: absolute after `~` expansion; no duplicates;
   - **the target's parent must exist**; the final segment is created if absent
     (work item 3). A missing parent is an error — auto-creating the full path
     turns a typo (`/data/relcoated/…`) into a silently-wrong empty dir on the
     root filesystem, which is the exact failure this feature exists to prevent.
     A missing target that reaches podman is an unexplained `statfs` crash.
3. **`storage.EnsureCacheRelocations([]CacheRelocation) error` (new,
   `internal/storage/ensure.go`).** `MkdirAll` the target's final segment and
   the `GlobalCache()/<subdir>` mountpoint, both `0o755`, so a fresh host with
   the config set Just Works and podman never invents the stub itself. Separate
   function — `EnsureGlobalStorage(migrate func())` stays config-free. Call it
   from the two existing `EnsureGlobalStorage` sites (`cli/run/run.go:82`,
   `cli/check/check.go:35`) once config is available.
4. **`internal/config/validate.go` — `validateCacheRelocations`.** Two jobs:
   shape-validate the entries visible in the merged map (rule set above), and
   **error if the key appears at workspace scope**. `ValidateConfig` only ever
   receives the merged map (`cli/run/preflight.go:30`, `cli/check/check.go:344`,
   merged at `config/load.go:249`) and carries no provenance, so the scope check
   re-reads `LoadWorkspaceConfig(workspace, false, nil)` — one extra file read in
   a cold path, and no new provenance plumbing. Message must name the fix:
   *"cache_relocations is user-scope only; move it to
   ~/.config/yolo-jail/config.jsonc"*. Append the new checks at the **end** of
   `ValidateConfig`'s sequence — its append order is a frozen golden contract.
5. **Assembly.** Add `cacheRelocations []config.CacheRelocation` to
   `assembleInput` (`cli/run/assemble.go:24`), populated in the run pipeline so
   `assembleRunCmd` stays a pure function of `(o, in)`. Emit
   `-v <target>:/home/agent/.cache/<subdir>` from `podmanBaseMounts`
   (`assemble_parts.go:39`), immediately after the `.cache` mount — adjacency is
   for readability, **not correctness** (proven above).
6. **Apple Container.** `appleContainerBaseMounts` (`assemble_parts.go:18`) is a
   separate path built around a "single writable /home/agent (device-limit
   workaround)" constraint. Skip relocations there with one clear warning,
   mirroring the existing `mounts`-under-`container` skip
   (`assemble.go:117-122`). Revisit if a Mac user asks; the motivating hardware
   is Linux.

### Phase 2 — keep the host tools honest

Relocation is **container-side only**: host-side `cache/<subdir>` stays an empty
stub. So prune does not over-report freed bytes — it goes **blind**.

7. **Prune accounting.** `DiskReport.CacheBreakdown` (`internal/prune/report.go:62-87`)
   walks the direct children of `GlobalCache()` and *skips symlinks* (`:75`), so
   after relocation the 185 GiB entry simply disappears from the `cache/ top 5`
   panel (`prunecmd.go:184-192`) — the largest consumer becomes invisible in the
   tool whose job is finding it. Add a `CacheRelocated map[string]int64` sourced
   by stat'ing the real targets, print it as its own labelled section with the
   backing filesystem, and keep it out of the primary `GlobalStorage` total
   (those bytes are on another device).
8. **Purge.** `PurgeCacheByAge` (`cachepurge.go:37`) joins `cacheRoot/sub` — and
   `huggingface` is in `CachePurgeHeavySubdirs` (`:17`). Post-relocation, the
   heavy purge silently no-ops on it while reporting success. Resolve each
   subdir through the relocation map before walking.
9. **Fix the hint** (`prunecmd.go:194-197`): point at `cache_relocations`, and
   stop implying the symlink trick generalizes.

### Phase 3 — docs

10. README config block + `yolo config-ref` + a `docs/guides/USER_GUIDE.md`
    section: when relocating is worth it (large cold caches yes; `uv`,
    `go-build`, `pip` no — those are hit on every build and are
    latency-sensitive), and the **manual migration procedure** — stop all jails,
    `rsync -aH --remove-source-files`, verify, then set the config.

### Deferred (held pending a design question — do not build yet)

11. `yolo cache relocate <subdir> <target>` — copy-verify-swap as one command,
    refusing to run while any jail is up and naming the offenders. The manual
    procedure has now been walked (host acceptance done 2026-07-22), which would
    normally clear this to build. **It is held instead:** the maintainer is not
    sure `cache_relocations` sits at the right level of abstraction, and does not
    want a command locked around it until that resolves — see [Is
    `cache_relocations` the right level?](#is-cache_relocations-the-right-level-held).
    (Design note for whenever it does land: a jail left running across a
    relocation keeps its old empty mount until restart — podman bind mounts are
    `rprivate` — which reads exactly like cache corruption, so the command must
    refuse while any jail is up.)

## Test plan

- **Golden argv** (`internal/cli/run/assemble_test.go`): relocation absent;
  one relocation present; two relocations emitted in sorted order. Assert the
  `-v` pair is **present**, not its position relative to the `.cache` mount —
  podman sorts by destination and pinning order would freeze a non-invariant.
- **Runtime gating:** `rt == "container"` emits the skip warning and no `-v`.
- **Validation** (`internal/config/validate_test.go`): keys `../etc`, `a/b`,
  `.`, `""`; relative target; duplicate targets; missing target parent; and the
  workspace-scope rejection (key in `yolo-jail.jsonc` → error naming the fix).
- **Prune** (`internal/prune`): a report with a relocated subdir shows it in the
  new section at its real size and excludes it from the primary total; the heavy
  purge walks the relocated target.
- **Integration** (`integration/`, `requireJail`): **NOT WRITTEN — deliberately
  omitted.** The test would have to place a `cache_relocations` key where the
  loader reads it, and the loader reads exactly one path: `paths.UserConfigPath()`
  = `$HOME/.config/yolo-jail/config.jsonc`. That is the whole point of the
  [threat model](#threat-model-why-user-scope-is-the-whole-design) — the key is
  unreachable from the workspace config a test can create — and the harness does
  not isolate `$HOME`: `runCommand` builds the child env as
  `append(os.Environ(), "TERM=dumb")` (`integration/harness_test.go`), so the
  spawned CLI sees the *real* user's home. A test that wrote that file would
  mutate the developer's own config. Overriding `HOME` for the child is not an
  escape either: `paths.GlobalStorage()` is `$HOME/.local/share/yolo-jail`, so
  moving `HOME` moves the whole storage root and the jail launch itself becomes a
  different (cold, unprovisioned) thing. Adding a test-only env seam to redirect
  the user-config path would widen the one surface this feature deliberately
  keeps narrow. Coverage instead comes from the argv-level unit tests
  (`internal/cli/run/assemble_test.go` asserts the emitted
  `-v <target>:/home/agent/.cache/<subdir>` pairs, the sorted order, the
  byte-identical no-relocation golden, and the Apple Container warn-and-skip),
  `internal/storage` (both ends provisioned, missing parent refused),
  `internal/config` (loader + validator), `internal/prune` (accounting + purge),
  and the manual host acceptance step below. Revisit if a hermetic
  `HOME`-isolated harness ever lands.
- **Manual host acceptance** (cannot be done in-jail): a real cross-filesystem
  relocation of `huggingface`, confirming `df` on the root filesystem drops and
  an in-jail HF download lands on the HDD. **Done 2026-07-22** — the maintainer
  moved a HuggingFace cache to cold storage on another machine successfully; the
  manual procedure (work items 1–10) is proven end to end.

## Answered

### Whether nested bind mounts work under `--read-only`

**Answer:**
> **Yes — proven 2026-07-21** on podman 5.8.4 with the real flag set. See
> [Proven behavior](#proven-behavior-podman-584-2026-07-21). The mountpoint is
> auto-created inside the writable parent bind mount and in-jail writes land in
> the host target. Two corrections fell out: argv order is not load-bearing, and
> a missing target is a hard `statfs` failure rather than a skip.

### Whether Apple Container supports nested bind mounts at all

_Leaning was:_ implement for podman, warn-and-skip on `container`.

**Answer:**
> **Skipped on `container`, but the question in the title is still open.**
> Review found the original rationale overstated: `appleContainerBaseMounts`
> *already* nests a bind mount at the required depth — it mounts `GlobalCache()`
> at `/home/agent/.cache` inside its `/home/agent` mount — so "cannot nest" was
> never established. The honest reason is that it is a separate mount path built
> around a device limit and **nobody has run a relocation on real Apple Container
> hardware**. Skipping loudly beats half-applying, so the skip stands (work item
> 6), but it is an untested-therefore-unimplemented, not a limitation. The
> warning text and `docs/guides/macos.md` were corrected to say so. `macos-user`
> genuinely needs nothing — it has no bind mounts, so a plain symlink works.

### Whether relocation should be per-subdir or a whole-cache root override

_Leaning was:_ per-subdir.

**Answer:**
> **Per-subdir.** The cache is heterogeneous: `huggingface` (185 GiB, cold,
> sequential) wants an HDD, while `uv` (32 GiB), `go-build`, and `pip` are hit on
> every build and want NVMe. A single `cache_root` forces one answer and would
> make builds measurably worse. A whole-cache move also already works by
> symlinking `cache` itself, since podman resolves that top-level source path.

### Whether the host-side migration needs to be safe against running jails

_Leaning was:_ refuse to relocate while jails are up.

**Answer:**
> **Yes, but documented before it is automated.** Podman bind mounts are
> `rprivate`, so a new relocation does not propagate into running containers —
> they keep the old, now-empty directory until restarted, which looks exactly
> like cache corruption. v1 ships the manual procedure with a "stop all jails"
> first step (work item 10); `yolo cache relocate` (work item 11) enforces it.

## Open Questions

### Is `cache_relocations` the right level? (held)

**This one gates item 11 — do not build `yolo cache relocate` until it is
answered.** The maintainer is not yet convinced the mechanism sits at the right
level of abstraction, and would rather sit with that than lock a command around
it.

The unease: `cache_relocations` is a yolo-specific, per-subdir map that only the
container knows about. It solves the immediate problem (get `huggingface` off the
root filesystem — done, verified 2026-07-22), but it does so by teaching *yolo*
about storage layout rather than letting the host's own storage layer express
it. Plausible "lower" levels the same need might be better served at:

- **Filesystem / mount level on the host.** The subdir is just a directory; a
  host-side bind mount or a dataset/subvolume (ZFS/btrfs) at
  `cache/<subdir>` would move the bytes with zero yolo involvement, and every
  host tool (`du`, backups, the container runtime) would see one truth. This is
  the same fork as [the host-side-reflection
  question](#whether-the-relocation-should-also-be-reflected-host-side) below —
  the two are really one decision. The blocker is the [threat
  model](#threat-model-why-user-scope-is-the-whole-design): a *yolo-managed*
  host symlink is the rejected arbitrary-path primitive, and a *yolo-managed*
  host mount unit is a lifecycle yolo would then own. But a symlink/mount the
  **human** creates and yolo merely consumes sidesteps both — which suggests the
  real primitive might be "yolo follows whatever `cache/<subdir>` already is"
  rather than "yolo relocates it."
- **Cache-tool level.** `HF_HOME` / `HUGGINGFACE_HUB_CACHE`, `UV_CACHE_DIR`,
  `GOCACHE`, `PIP_CACHE_DIR` are all env-addressable. Pointing the tool at cold
  storage directly (and mounting that path) would not need a bespoke relocation
  map at all — though it fragments across tools and loses the single `.cache`
  story.

Nothing here is decided. The immediate win is already banked (the manual move
works and is documented, work items 1–10), so there is no pressure to pick. The
open question is whether item 11 should exist *as designed* — a yolo command
that owns the copy-verify-swap — or whether the honest primitive is one level
down (host filesystem) or one level out (per-tool env), in which case yolo's job
shrinks to *consuming* a host-declared layout rather than *managing* one.

_Leaning:_ genuinely undecided — this is a real fork, not a formality. Revisit
alongside the host-side-reflection question; they resolve together.

**Answer:**
> _(empty — held for further consideration; see above)_

### Whether the relocation should also be reflected host-side

Today's design is container-only: host `cache/<subdir>` is an empty stub and
`yolo prune` learns about the real bytes from the config (work items 7–8). The
alternative is to make the host agree physically — a bind mount or symlink at
`cache/<subdir>` — so `du`, backup tools, and anything else that walks the cache
see one truth without knowing about yolo.

_Leaning:_ No. A symlink there is the primitive the [threat
model](#threat-model-why-user-scope-is-the-whole-design) rejects, and a host bind
mount means yolo owns a mount unit with a lifecycle (survive reboot? unmount on
uninstall?) — a large step up in responsibility for a cosmetic gain. Teaching
prune is ~30 lines. But it does mean third-party tools stay wrong, so this is a
real fork rather than an obvious call. **Now folded into the larger held
question** — [Is `cache_relocations` the right
level?](#is-cache_relocations-the-right-level-held) — because "yolo consumes a
host-declared layout" is exactly this reflection done by the human instead of by
yolo. They resolve together.

**Answer:**
> _(empty — held; resolves with the level-of-abstraction question above)_

### Whether `cache_relocations` should accept a per-workspace override for read-only sharing

A plausible follow-on: point several machines' jails at one NFS-mounted model
cache. That is a different feature (shared, possibly read-only, contention on
`huggingface_hub`'s lock files) and might argue for a `mode` field rather than a
bare path.

_Leaning:_ Out of scope for v1 — ship the bare `subdir → path` map. Adding a
value struct later is a compatible change (string or object); adding a second
top-level key is not. Worth confirming nobody wants NFS on day one.

**Answer:**
> _(empty — fill in when decided)_
