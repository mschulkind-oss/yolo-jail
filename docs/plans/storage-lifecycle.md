# Storage / cache / image lifecycle — make GC safe at any moment

**Status:** §1–§4 IMPLEMENTED (2026-07-22); host-gated residuals remain (see
below). Anchored on a real incident: a host `nix-collect-garbage` reclaiming
~2.5 TiB swept the **running jail image's own store closure**, leaving 235 of
467 `/bin` symlinks pointing at dead targets (git, gh, curl, gcc, rg, fd, node,
yolo, …). The jail kept running but its tools were broken. Root cause: **the
running image's store closure is not a registered nix GC root**, so a GC is not
safe to run at an arbitrary moment. This plan sequenced the fix.

**Landed:**
- **§1** — the run path registers a durable per-image GC root
  (`build/roots/<sha16>`), retained across runs; `yolo prune` reaps roots no
  live jail needs (tri-state fail-safe). Mechanism verified in-jail; the
  security win for the maintainer's live jails is host-gated on `just load`.
- **§2** — `yolo check` warns when the host nix daemon's auto-GC is off
  (`min-free == 0`), the safety net that §1 makes safe to enable. Host-owns the
  actual nix.conf edit.
- **§3** — opt-in `yolo prune --nix-gc`: a bounded, rooting-aware
  `nix store gc --max N` that refuses in-jail and declines unless every loaded
  image closure has a durable §1 root. Never a blanket collect.
- **§4** — log/overlay lifecycle sweeps: dangling build out-links, orphaned
  agent-staging dirs, and age-purge of regenerable agent logs (Claude
  transcripts deliberately excluded).

**Host-gated residuals (need the maintainer):** `just load` to ship §1's run-path
rooting to live jails; the `min-free`/`max-free` nix.conf edit (§2); and the §3
end-to-end store-GC acceptance run against a real host store. See "What needs the
human / host" below.

---

## The core invariant

> **A garbage collection must be safe to run at any moment.**

Concretely: the store closure of *every currently-loaded jail image* — the
`stream-yolo-jail` script and everything it references (git, node, ripgrep, the
`yolo`/`yolo-entrypoint` binaries, the customisation layer) — MUST be reachable
from a registered nix GC root for as long as a jail is running against it. Today
it is not, and nothing else in this plan is safe until that holds. Auto-GC
(min-free/max-free) and any yolo-driven store GC are *defect amplifiers* until
rooting lands: they turn "a GC someday" into "a GC on a timer."

### Confirmed defect (measured in-jail, 2026-07-22)

- `/bin/git` resolves to `/nix/store/1k2lblqlj…-git-2.54.0/bin/git`. That exact
  path is a member of the running image's stream-script closure
  (`nix-store --query --requisites <stream-path>` lists it). So rooting the
  single `stream-yolo-jail` store path transitively protects every `/bin`
  binary — one root covers the whole image.
- `nix-store --query --roots <stream-path>` for the loaded image returns
  **empty** — the closure has **no GC root**. A `nix-collect-garbage` is free to
  delete it out from under the live jail.
- The image closure is **~3.09 GiB** (`path-info --closure-size` of a valid
  `stream-yolo-jail` = 3,313,506,048 B).

---

## How the image is built, loaded, and (not) rooted

The build+load pipeline is `internal/image/autoload.go` → `AutoLoadImage`:

1. `buildImageStorePath` runs `nix build .#ociImage --impure --out-link <outLink>`
   where `outLink = BuildDir()/run-result-<pid>` (`autoload.go:144`, built at
   `:290-293`). The out-link is nix's **only** GC root for that build.
2. It resolves the out-link to the `stream-yolo-jail` store path, streams the
   image tar to `cache/images/<sha16>.tar` (`materializeImage`), and
   `podman load`s it. `podman load` copies the layer blobs into podman's *own*
   image store — it does **not** create or hold any `/nix/store` reference.
3. **Every exit path then calls `os.Remove(outLink)`** — success at
   `autoload.go:266`, and each failure at `:236`, `:243`, `:259`. Removing the
   out-link destroys the sole GC root the instant the build finishes.

`internal/image/build.go` (`BuildOCIImage`, the `yolo check` preflight) is worse:
it builds to an `os.CreateTemp` out-link and `defer os.Remove`s it (`build.go:23-30`)
— rooted for the duration of `nix build`, unrooted forever after.

Net effect: **every image build leaves a ~3.1 GiB unrooted closure.** A nested
`yolo -- bash` rebuilds the flake whenever it changes (documented in AGENTS.md),
so ordinary in-jail development manufactures these dead closures continuously.
The podman-loaded image works only because nix hasn't GC'd yet — it is stale-code
territory the moment a GC runs.

### Why `podman load` doesn't save us

podman's image store lives under its own graphroot (in-jail: nested podman;
host: rootless podman storage), entirely separate from `/nix/store`. The nix
store paths the image's `/bin` symlinks point *into* are the host store, mounted
`:ro` at `/nix/store` (`assemble.go:232-238`, gated by `shouldMountHostNix`). So
the loaded, running container depends on live `/nix/store` paths that nix
believes are garbage. That split is the whole bug.

### The HOME-divergence subtlety (rooting must be host-side)

From inside a jail, nix delegates to the host daemon: `/nix/var/nix/daemon-socket`
+ `/nix/store:ro` are mounted and `NIX_REMOTE=daemon` is set (`assemble.go:232-238`).
**`/nix/var/nix/gcroots/` is NOT mounted into the jail** (confirmed: `ls
/nix/var/nix/gcroots` → "No such file or directory" in-jail; only `daemon-socket`
is present under `/nix/var/nix`). Two consequences:

- An **indirect GC root** (`nix-store --add-root`, or a symlink placed under
  `/nix/var/nix/gcroots/auto/`) cannot be created from inside a jail — the store
  and its gcroots dir are read-only / absent there.
- An out-link created in-jail at `BuildDir()/run-result-<pid>` lives at
  `/home/agent/.local/share/yolo-jail/build/...`. That is the *nested* jail's
  home, which the **host daemon does not treat as a valid indirect-root
  location** the same way the host's own path would. Indirect gcroots are the
  daemon following a symlink from `gcroots/auto/<hash>` back to the out-link;
  when the build ran under `NIX_REMOTE=daemon` the daemon registers the auto-root
  against the out-link path as seen by the *client*, but the client's home tree
  is not the host's — so rooting reliability differs host vs. in-jail. **The
  durable GC root must be established on the host side, by the process that owns
  the loaded image's lifecycle.**

This means the fix has a host-gated component: the maintainer's day-to-day jails
only become GC-safe after a host `just load` ships the change, AND the host-side
`yolo run` path must register the root. In-jail we can *verify the mechanism*
(the out-link is retained, `--query --roots` is non-empty for a host-side build)
but the security win for the maintainer's running jails is host-gated.

---

## Every storage consumer (the full map)

Measured baseline below each. Root device: `/dev/mapper/root`, 3.7 TB, **45%
used (1.6 TiB)** — shared by `/nix/store`, `/home/agent`, `/tmp`, `/workspace`,
and the host.

| Consumer | Path | Who creates | Who cleans | Bound? |
|---|---|---|---|---|
| **Host nix store** | `/nix/store` (`:ro` in-jail) | every `nix build` | nothing yolo-owned; only host `nix-collect-garbage` | **UNBOUNDED** — no min-free/max-free (host `nix config show`: `min-free = 0`, `max-free = MAX`), no yolo GC root, no yolo GC |
| Image build out-links | `build/run-result-<pid>`, check tempfile | `autoload.go`, `build.go` | removed immediately (destroys the root) | leaks a ~3.1 GiB unrooted closure per build |
| Image cache tars | `cache/images/*.tar` | `materializeImage` | `PruneImageCache(keep=3)` via `yolo prune` (manual) | keep-newest-3; **9.49 GiB now** (3 × ~3.14 GiB) |
| Orphan `.tmp` (crashed materialize) | `cache/images/*.tmp` | `materializeImage` temp | `PruneImageCache` always sweeps | swept every prune |
| Build-root generations (legacy) | `nix-build-root*` | pre-cutover source-bundle staging (removed) | `PruneLegacyBuildRoots` (one-shot legacy cleanup) | staging is gone; sweeps stragglers from pre-cutover installs |
| Load sentinels | `build/last-load-<runtime>` | `AddLoadedPath` (LRU 10) | self-capping at 10 entries | tiny; but see "stale sentinel" below |
| Per-workspace overlays | `<ws>/.yolo/home/{npm-global,local,go,…}` | entrypoint bootstrap | `yolo prune` hardlink-dedup (cross-ws) | dedup only; grows per workspace |
| Shared download caches | `cache/{npm,go-build,uv,pip,node-gyp,…}` | in-jail tools | `PurgeCacheByAge(age>30d)` via `yolo prune` | age-purge; **npm 2.0 GiB, go-build 256 MiB, node-gyp 63 MiB** |
| Global home seed | `home/` (`:ro` mount) | bootstrap/migrate | `PruneShadowedHome` (overlay-masked subtrees) | small (12 KiB now) |
| Agent logs / staging | `agents/` (briefings), `logs/`, `locks/` | boot, per-run | nothing periodic | agents **30 MiB**, logs 1.3 MiB; slow leak |
| mise store | `mise/` | mise | nothing yolo-owned | **1.2 GiB**; shared CAS |
| In-jail agent logs | `~/.claude/projects/`, `~/.copilot/logs/`, `~/.cache/gemini-cli/logs/` | agents | nothing | unbounded, per-workspace |
| Nested podman store | nested graphroot | nested `yolo -- bash` | nothing yolo-owned | can hold duplicate loaded images |
| tmpfs | `/tmp`, `/var/tmp` | runtime | ephemeral (tmpfs) | RAM-bound, resets on stop |

**Baseline totals (2026-07-22, this host):**
- `/nix/store` device: 1.6 TiB used / 3.7 TB (45%).
- `~/.local/share/yolo-jail` total: **~14 GiB** — `cache` 13 GiB (of which
  `cache/images` **9.5 GiB**, `cache/npm` 2.0 GiB), `mise` 1.2 GiB, `agents`
  30 MiB, `build` 28 KiB.
- Image closure: **~3.09 GiB** per loaded image; cache tar **~3.14 GiB** each.
- `build/`: 4 out-link symlinks, **3 of 4 already dangle** (`run-result-113179`,
  `run-result-133037`, `run-result-222284` → deleted store paths) — visible
  evidence of the out-link-removal leak. One manual `restore-result` symlink
  (the incident recovery root) still resolves.
- `last-load-podman` sentinel lists 10 store paths; the top ones are already
  invalid store paths (post-incident) — the sentinel is a stale, non-rooting
  record, not a GC root.

---

## The `yolo prune` surface (what exists, what it won't touch)

`internal/prune/` + `internal/cli/commands.go:runPrune`. Default **dry-run**;
`--apply` reclaims. Sections, in order (`prune.go:Run`):

1. Hardlink dedup across workspace overlays (`prune.go`, atomic link-to-tmp-rename).
2. Stopped `yolo-*` containers.
3. Orphaned broker relays (tri-state live-gated).
4. Old `yolo-jail` images in the runtime (`--keep-images 2`).
5. **Cached image tarballs** (`imagecache.go` `PruneImageCache`, `--image-cache-keep 3`;
   always sweeps `.tmp`).
6. Orphaned build-root generations (`sweep.go`, live-gated + 1h floor, fail-safe).
7. Shadowed seed subtrees (`shadowed.go`).
8. Age-based cache purge (`cachepurge.go` `PurgeCacheByAge`, `--cache-age 30`;
   forbidden browser-profile subdirs hard-excluded; relocation-aware).

**What prune deliberately does NOT touch — keep it that way:**
- The host `/nix/store`. There is **no** `nix-collect-garbage` anywhere in the
  codebase (confirmed: only match is `flake.nix:799`, the builder image's
  `mkdir gcroots`). The user's hard rule stands: **never carelessly GC the host
  store.** Any store GC this plan adds must be rooting-aware and jail-live-gated,
  never a blanket collect.
- Host symlinks/mounts as an arbitrary rw primitive. The `cache-relocation.md`
  threat model is settled law here: yolo does not manage host symlinks/mounts;
  a *human*-declared layout yolo merely consumes is the only acceptable shape.
  A store-GC feature must not become a backdoor to that.

**Where a safe store-GC fits:** a new, opt-in prune section that (a) enumerates
live jails' loaded image closures (the same tri-state liveness prune already
uses for containers/relays/build-roots), (b) confirms each is a registered GC
root, and (c) only then invokes a **bounded** `nix-collect-garbage` (or
`nix store gc`) — never touching anything a live root protects. This is
**host-gated and last in priority**; it must not ship before rooting (§1).

---

## Prioritized work list (ordered by dependency)

### 1. Root the running image's closure — FIRST, everything depends on it

- [x] **Stop destroying the out-link on success** — `internal/image/autoload.go`.
  Remove the `os.Remove(outLink)` at `:266` (and reconsider `:236/:243/:259` —
  on failure there is no loaded image to protect, so removing is fine, but the
  success path must retain a root). The out-link name is per-PID
  (`run-result-<pid>`, `:144`), which is wrong for a *durable* root: the PID is
  the build process, not the jail lifetime.
- [x] **Introduce a per-loaded-image stable GC root** keyed by the store path,
  not the PID. Shape: after a successful load, `nix build --out-link
  BuildDir()/roots/<sha16>` (reusing `ImageCachePath`'s sha16 of the store path
  as the key) OR `nix-store --add-root BuildDir()/roots/<sha16> -r <store-path>`,
  so the root name is stable across runs of the same image and multiple distinct
  images each keep their own root. Register it **from the host-side `yolo run`
  path** (`internal/cli/run/imageload.go` / `autoLoadImage`), because in-jail the
  gcroots dir is unreachable (see HOME-divergence above).
- [x] **Reap the root when no live jail uses the image** — extend the existing
  live-image enumeration (`FindReferencedBuildRoots` pattern in `sweep.go`, which
  is already tri-state-safe) so `yolo prune` removes `roots/<sha16>` entries whose
  store path no live jail depends on. Mirror the fail-safe: liveness unknown →
  delete nothing.
- [x] **Fix `BuildOCIImage`** (`internal/image/build.go:23-30`) — the check
  preflight's `defer os.Remove(outPath)` is acceptable *only* because check
  doesn't load an image to run; leave it, but add a code comment tying it to this
  invariant so a future refactor doesn't copy the pattern into a load path.
- **Verification:** in-jail we can prove the *mechanism* — build, then
  `nix-store --query --roots <store-path>` is non-empty and points at the new
  root; a subsequent `nix-collect-garbage --dry-run` (read-only) does NOT list
  the image closure. **Host-gated:** the maintainer's live jails only become
  GC-safe after `just load` ships the run-path change AND a host `yolo run`
  registers the root.

### 2. Auto-GC safety net (min-free/max-free) — ONLY after §1

- [ ] **HOST-OWNED — Configure host nix `min-free`/`max-free`** so the store
  self-limits instead of relying on a manual blanket GC. Today `min-free = 0`,
  `max-free = MAX` (measured) — auto-GC is effectively off. Setting e.g.
  `min-free = 50 GiB`, `max-free = 200 GiB` makes the daemon free *unrooted*
  paths automatically when space runs low. **This is safe if and only if §1
  holds** — auto-GC honors GC roots, so a rooted image closure survives; an
  unrooted one (today) would be the first casualty, on a timer. Ordering is not
  optional. **Not shipped in code — the maintainer must make this nix.conf edit.**
- [x] **DONE (detect + warn)** — This is **host nix.conf**, not a yolo artifact:
  a `/etc/nix/nix.conf` (or Determinate `nix.custom.conf`) change the **human**
  must make. yolo does not edit it; instead `yolo check` now reads the daemon's
  effective `min-free` and warns with the exact remediation when it is 0 (the
  §2 code that landed). The nix.conf edit itself remains host-owned (bullet
  above).
- **Verification:** host-only. In-jail, `nix config show` reads the daemon's
  effective config (already works) so `yolo check` can *observe* the values, but
  changing them is host-side.

### 3. Bounded, rooting-aware store GC in `yolo prune` — after §1 and §2

- [x] Add an **opt-in** prune section (e.g. `--nix-gc`, default OFF) that runs a
  bounded `nix store gc --max <N>` after confirming every live jail's image
  closure is rooted (§1). Target file: `internal/prune/` (new `nixgc.go`) wired
  into `prune.go:Run` and `commands.go:runPrune`. Reuse the tri-state liveness
  probe; **fail-safe**: liveness unknown → skip GC entirely.
- [x] Never a blanket `nix-collect-garbage -d`. The section's contract: "free
  unrooted store paths up to N bytes; never touch a path a live jail's rooted
  image needs." Document it next to the existing "never carelessly GC host store"
  rule.
- **Verification:** host-gated (needs a real host store + daemon). Dry-run
  accounting is unit-testable with an injected `nix store gc --dry-run` seam.

### 4. Log / overlay / cache lifecycle — independent, lower priority

- [x] **In-jail agent logs** (`~/.claude/projects/`, `~/.copilot/logs/`,
  `~/.cache/gemini-cli/logs/`) have no reaper. Add age-based purge, ideally by
  folding the log dirs into `CachePurgeDefaultSubdirs`-style handling in
  `cachepurge.go` (they are under `~/.cache` for gemini; claude/copilot are under
  home overlays, so a separate walker keyed off `GlobalHome()` per-workspace).
  Respect the `cachePurgeForbidden` discipline — never delete live profile state.
- [x] **`agents/` staging** (30 MiB, per-container briefings) accumulates one dir
  per container name forever. Add a sweep tied to the same live-container
  enumeration prune already does (`FindYoloWorkspaces`): drop `agents/<name>` for
  names with no tracking file. Target: `internal/prune/`, new section.
- [x] **Stale out-link symlinks** in `build/` (3 of 4 dangle today). Add a sweep
  of `build/run-result-*` symlinks whose target no longer exists — pure cleanup,
  no liveness needed (a dangling symlink protects nothing). Target:
  `internal/prune/`, small helper; safe in-jail.
- [x] **`cache/images` size hint** already exists (`prunecmd.go`); once §1 lands,
  update the mental model note there — the tars are streamed once then unused,
  but the *store closure* behind the loaded image is the real 3 GiB that must
  stay rooted, distinct from the tar.
- **Verification:** all in-jail-verifiable (pure host-storage manipulation via a
  temp root, the pattern `prune_test.go` already uses).

---

## What needs the human / host (gated)

1. **Host nix.conf `min-free`/`max-free`** (§2) — a `/etc/nix/nix.conf` edit yolo
   must not make. yolo may detect + warn only.
2. **Shipping the rooting fix to the maintainer's live jails** (§1) requires a
   host `just load` (image + run-path change) — in-jail nested runs validate the
   mechanism but do not re-root the host's already-running jails.
3. **Any host-side gcroots registration** happens on the host `yolo run` path;
   the in-jail path cannot write `/nix/var/nix/gcroots` (read-only / unmounted).
4. **Bounded store GC** (§3) can only be exercised end-to-end against a real host
   store + daemon.

---

## Test plan

- **Unit (in-jail):** `internal/image` — `AutoLoadImage` retains the out-link /
  registers the sha16-keyed root on success (inject the build + a fake
  `add-root`); failure paths still clean up. `internal/prune` — the new
  root-reaper drops only unreferenced `roots/<sha16>` (tri-state fail-safe), the
  dangling-out-link sweep, the `agents/<name>` sweep, all against a temp storage
  root like the existing `prune_test.go` / `imagecache` tests.
- **Nested-jail (mechanism, in-jail):** `yolo -- bash` builds the flake; after,
  `nix-store --query --roots <stream-path>` is non-empty and `nix-collect-garbage
  --dry-run` does not list the image closure.
- **Host acceptance (gated, cannot run in-jail):** with `min-free`/`max-free`
  set, force a low-space GC while a jail is up; confirm `/bin/git` et al. stay
  live (the incident, now non-reproducible). This is the acceptance bar for §1+§2
  together.

---

## Open questions

- **Root granularity:** one root per distinct loaded image (sha16-keyed) vs. one
  root per live jail (container-name-keyed). The former dedupes when many jails
  share one image (the common case); the latter maps 1:1 to lifetime. Leaning:
  sha16-keyed root + a reaper that checks *any* live jail references it.
- **min-free/max-free values:** need the maintainer's real headroom numbers on
  the 3.7 TB shared device; 50/200 GiB is a placeholder.
- **`restore-result`** in `build/` is a manual recovery root, not created by any
  yolo code (confirmed: no repo reference). Once §1 gives durable auto-roots, the
  manual root can be retired — worth a note so it isn't mistaken for a yolo
  artifact.
