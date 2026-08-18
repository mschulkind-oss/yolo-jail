# Baking vs. staging — what the image must contain, and what a launch can deliver

**Status:** ANALYSIS + PROPOSAL, 2026-08-15. Nothing built. All measurements taken in this
development jail on 2026-08-15 unless dated otherwise; every number below is labelled
**MEASURED** or **NOT MEASURED**.

**The question, from the maintainer:** *"what we can do to avoid cache rebuilds/reloads by changing
how we stage things — what can we copy into the image rather than bake into it for efficiency
basically."*

**The short version — and it is not the answer the question expects.** I went looking for baked
content to move out and found that moving content out is the *third*-best lever. The measurement
that decides it: between the two most recently loaded images on this machine, `nix store
diff-closures` reports **one** changed package — `yolo-jail-install`, a 180.2 KiB size delta —
and that delta cost a full **3.28 GiB** re-materialization and a full `podman load` (§1.4).
yolo's own content is **3.25 %** of the image closure and moves in **59.5 %** of commits; nixpkgs
content is **96.75 %** of the closure and moves in **0.5 %** of commits (§1.2, §1.3). The image
is therefore already almost perfectly stratified — the waste is not *what* is baked, it is that
the pipeline treats a 3 %-delta image as a brand-new artifact end to end. So the three ranked
candidates are: **(C1)** make a failed build fail as itself, because a staging change is worthless
if a failure to stage is invisible; **(C2)** address the image by content instead of by the single
`:latest` tag, which deletes the cross-workspace reload thrash on every container backend for very
little code; **(C3)** stop writing a 3.28 GiB tar for every load. Only then does real
staging-instead-of-baking (§4, C4–C5) earn its risk — and it earns it on **one of three backends**,
because `/nix/store` is mounted only on Linux + podman (§3.2).

**The most important section is §1** (the cost model). §5's table is the deliverable the question
asked for; §2 is why the numbers in §1 are what they are.

**Reads with:** [`storage-and-config.md`](storage-and-config.md) (where these bytes live),
[`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) (the 2026-07-22 baseline this
doc's growth numbers are measured against),
[`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) (the backend with no image at
all), [`../plans/handoff-cachix-cache.md`](../plans/handoff-cachix-cache.md) (the binary-cache
prior art, argued in §6).

---

## 1. The cost model

### 1.1 What triggers a rebuild, and how often

Every `yolo` launch runs `nix build .#ociImage --impure` before the container starts
(`internal/image/autoload.go:319-323`, called from `internal/cli/run/imageload.go:15-40`). The
derivation moves when anything in the `goSrc` fileset moves — `go.mod`, `go.sum`, `vendor/`,
`cmd/`, `internal/`, `bundled_loopholes/`, `packs/` (`flake.nix:61-83`) — or `flake.nix` /
`flake.lock` move, or `YOLO_EXTRA_PACKAGES` changes (§1.5).

**MEASURED**, over the 200 commits from `23cee7a` (2026-08-05) to `9bae9f3` (2026-08-15):

| Path set | Commits | Share |
|---|---:|---:|
| `goSrc` fileset (`cmd/ internal/ packs/ bundled_loopholes/ go.mod go.sum vendor/`) | 119 | **59.5 %** |
| `flake.nix` | 2 | 1.0 % |
| `flake.lock` | 1 | 0.5 % |
| `packs/` alone | 4 | 2.0 % |
| `vendor/ go.mod go.sum` alone | 0 | 0.0 % |
| **Union — any commit that forces a rebuild** | **121** | **60.5 %** |
| Neither — no rebuild | 79 | 39.5 % |
| (`docs/` — for scale) | 91 | 45.5 % |

**MEASURED**, over the 500 commits from `c937394` (2026-07-25): union **266 / 500 = 53.2 %**,
`flake.nix` **6 / 500 = 1.2 %**. The shape is stable across both windows, so the 200-commit figure
is not a doc-heavy-fortnight artifact.

**The finding that reorders everything:** the flake barely moves. The thing that forces ~60 % of
rebuilds is our own Go source, and our own Go source is a rounding error inside the image (§1.2).
Any proposal framed as "bake less nixpkgs" is aimed at the 1 % case.

### 1.2 What the image contains, by size

**MEASURED** with `nix path-info -S` / `-r` against the image derivation currently at the head of
the load sentinel (`/nix/store/q3hbzcn…-stream-yolo-jail`):

| Component | Bytes | Store paths | Share |
|---|---:|---:|---:|
| Whole image closure | 3,461,437,424 (3.22 GiB) | 577 | 100 % |
| `imageClosureRoot` — the nixpkgs half (`flake.nix:831-834`) | 3,349,065,480 (3.12 GiB) | 571 | **96.75 %** |
| Everything yolo builds (`installPrefix`, `binPathLinks`, `nix-ld`, metadata drvs) | 112,371,944 (107 MiB) | 6 | **3.25 %** |
| `installPrefix` closure alone (`flake.nix:681-709`) | 82,781,928 (79 MiB) | — | 2.39 % |
| The four shipped Go binaries | 39,943,902 (38 MiB): `yolo` 16.0 MB, `yolo-jaild` 9.6 MB, `yolo-entrypoint` 7.2 MB, `yolo-ps` 7.1 MB | — | 1.15 % |

`installPrefix` stores each binary **twice** — once at `/opt/yolo-jail/bin/` and once in the
`share/yolo-jail/bin/linux-<arch>/` flake bundle (`flake.nix:694-708`) — which is why 38 MB of
binaries occupy 79 MB of closure. That duplication is deliberate (`flake.nix:657-666`: a symlink
would break exe-relative bundle resolution) and is 2 % of the image; it is not worth attacking.

### 1.3 What a rebuild actually costs

The surprise here is that **the build is cheap and the delivery is not**.

**MEASURED**, `nix build --impure --dry-run .#ociImage` on a warm store with nothing else changed:

- baseline (no `packages:`): **5 derivations to build** — `yolo-jail-customisation-layer`,
  `excludePaths`, `layers.json`, `yolo-jail-conf.json`, `stream-yolo-jail`. All metadata. Nothing
  to fetch.
- `YOLO_EXTRA_PACKAGES=["zbar"]`: **6 derivations** — the same five plus `bin-path-links` (the
  `/lib` symlink farm, `flake.nix:429-637`).
- `YOLO_EXTRA_PACKAGES=["hello"]`: **5 derivations** — `bin-path-links` did *not* appear. I did
  not chase why; `hello` contributes no `lib/`, but the store path is still interpolated into the
  farm's builder script (`flake.nix:526`) so I expected 6. Reported as observed, not explained.

**MEASURED**, evaluation cost on a warm eval cache, three runs each:

| Operation | Time |
|---|---:|
| `nix eval --impure .#installPrefix.outPath` | **0.22 s** (confirms the "~0.3 s" claim at `AGENTS.md:154`) |
| `nix eval --impure .#ociImage.drvPath` | **1.28 s** |
| Materialize: stream the image derivation to `/dev/null` | **11.2 s** for 3,524,710,400 B — **299 MiB/s** |

**NOT MEASURED**, and I say so rather than guess:

- A cold `nix build` of the image (a Go rebuild plus the five metadata derivations). Would have
  required an actual build; the brief said to avoid one.
- `podman load -i <3.28 GiB tar>`. Would have consumed ~3 GiB in podman storage on a device already
  at 69 %.
- The disk-write half of materialization. The 11.2 s figure is stream-to-`/dev/null`; the real path
  writes 3.28 GiB through `os.Create` + `Rename` (`internal/image/autoload.go:408-454`).

Documented-but-not-independently-verified durations, for triangulation: **~12–13 s** for a
`packages:`-bearing `--impure` rebuild plus container cold start (`integration/packages_test.go:35-36`);
**~45 s** for a forced in-jail image rebuild + reload (`AGENTS.md:160`); **~2–5 min** for a first
build on Linux (`docs/research/platform-comparison.md:231-232`). The machine these were taken on has
32 cores and 125 GiB RAM (**MEASURED**) — a laptop will be materially slower, and macOS slower again.

### 1.4 The amplification factor — the number this doc exists for

**MEASURED**, `nix store diff-closures` between the two most recently loaded images recorded in
`~/.local/share/yolo-jail/build/last-load-podman`:

```
yolo-jail-install: 180.2 KiB
```

That is the entire output. One package changed. Every other one of the 577 store paths is
byte-identical. For that, the pipeline: built a new `stream-yolo-jail` derivation, wrote a fresh
**3.28 GiB** tar to `cache/images/<sha16>.tar` (`internal/image/autoload.go:252-266`), and ran a
full `podman load` (`:267-273`).

For contrast, the same command between the *oldest* and *newest* entries in that ten-deep sentinel
— a `flake.lock` bump — reports chromium 150→151, gcc 15.2→15.3, icu4c 76→78, git 2.54→2.55, and
~60 more. **That** case genuinely needs a whole new image. It happens once per 200 commits (§1.1).

### 1.5 The multiplication factor: `packages:` and `--impure`

**Established definitively by measurement**, since the brief flagged it as the crux.

`nix build .#ociImage --impure` is run with `YOLO_EXTRA_PACKAGES` set from the config `packages:`
list (`internal/image/autoload.go:312-323`, via `config.EffectivePackages`,
`internal/cli/run/imageload.go:16`). The flake reads it through `builtins.getEnv`
(`flake.nix:140-143`), which is why `--impure` exists at all (`AGENTS.md:133-134`).

Derivation paths, **MEASURED** by `nix eval --impure`:

| Attr | no `packages:` | `["hello"]` | verdict |
|---|---|---|---|
| `ociImage.drvPath` | `4wm5csvm…` | `fzvb9xyd…` | **changes** |
| `binPathLinks.drvPath` | `nmbdb0nq…` | `6222wgbl…` | **changes** |
| `installPrefix.drvPath` | `hw7r9820…` | `hw7r9820…` | invariant |
| `installPrefix.outPath` | `7d2payjy…` | `7d2payjy…` | invariant |
| `goBinaries.drvPath` | `6smyba51…` | `6smyba51…` | invariant |

Adding two packages (`["hello","cowsay"]`) yields a third distinct image path. So: **one package
added to `packages:` produces a distinct image, a distinct 3.28 GiB tar, and a distinct
`podman load`.** The `installPrefix` invariance also confirms, by measurement, that it is the
right staleness oracle for the integration suite (`AGENTS.md:157-159`).

**Is the resulting image shared across workspaces?** In *content*, no — it is a function of
`packages:`. In *name*, yes — there is exactly one tag, `localhost/yolo-jail:latest`
(`internal/paths/paths.go:35-36`, `internal/image/image.go:37-42`), and exactly one load sentinel
per runtime, `build/last-load-<runtime>` (`internal/image/autoload.go:157`). And `packages:` is
**workspace-scope**: `internal/config/validate.go:184-215` imposes no user-scope restriction, unlike
`packs` (`internal/config/packs.go:488`) or `host_files` (`internal/config/hostfiles.go:938`).

Those three facts compose into a defect:

> Two workspaces on one machine with different `packages:` lists **reload the whole image on every
> alternation, forever.** `alreadyLoaded` compares the current store path against the single
> most-recently-loaded path (`internal/image/autoload.go:232-237`); the ten-entry history exists
> (`internal/image/image.go:142-159`) but is deliberately not consulted for the decision
> (`autoload.go:226-231`). Workspace A launches → path A loaded. Workspace B launches → mismatch →
> full reload. Back to A → mismatch → full reload. The tar is already cached so materialization is
> skipped (`autoload.go:258`), but the `podman load` is not.

This is the multiplication factor the brief asked me to establish, and it is worse than "a package
costs a rebuild": it costs a reload *per launch, indefinitely*, to every other workspace on the
machine.

### 1.6 What it has actually cost, on disk

**MEASURED**, this jail's state dir, 2026-08-15:

| Thing | Value |
|---|---|
| `~/.local/share/yolo-jail/cache/images` | **125 tars, 404.4 GiB**, mean 3.24 GiB |
| `/nix/store` | **209 GB** |
| Root device (`/dev/mapper/root`, shared by store, home, `/tmp`, `/workspace`) | 3.7 T, **2.5 T used, 69 %** |
| Realized `*-stream-yolo-jail` store paths | **212** |
| Realized `*-yolo-jail-install-prefix` store paths | **152** |
| Realized `*-yolo-jail-go-0-dev` store paths | **177** |
| Busiest single day of tar creation | **40 tars on 2026-07-27** (~130 GiB in one day); 37 on 2026-08-02 |

Against the in-repo baseline of 2026-07-22 (`docs/plans/storage-lifecycle.md:127`, `:134`, `:148-152`):
`cache/images` was **9.5 GiB / 3 tars** and the device was **1.6 TiB used (45 %)**. Twenty-four days
later it is 404.4 GiB and 2.5 TiB (69 %) — roughly **+16 GiB/day of image tar**, on top of the store
closures.

Retention exists and is opt-in: `PruneImageCache` keeps the newest 3 by mtime
(`internal/prune/imagecache.go:9-83`, default `ImageCacheKeep: 3` at
`internal/prune/prunecmd.go:54`, `:136`), reachable only through `yolo prune --apply`
(`internal/prune/prunecmd.go:378-389`). Nothing calls it automatically. A 20 GiB hint fires at
`prunecmd.go:207`, `:272-295`. **The measured reality is that the hint did not cause a prune for
twenty-four days and 395 GiB.**

Note also that a loaded image is stored **three times**: the store closure (~3.22 GiB, kept alive by
a durable GC root, `internal/image/gcroot.go:13-45`), the cache tar (3.28 GiB), and podman's own
image store. `internal/prune/prunecmd.go:285-294` states the first two are separate ledgers; podman
storage was **NOT MEASURED** here (this jail has no loaded image — `podman images` is empty).

### 1.7 The cost model in one paragraph

Sixty percent of commits force a rebuild. The rebuild itself is five metadata derivations plus a Go
build — cheap. The *delivery* is 3.28 GiB written and 3.28 GiB loaded, every time, to ship a delta
measured at 180 KiB. A `packages:` entry multiplies that by the number of distinct package lists on
the machine, per launch, forever. The accumulated artifacts are 404 GiB and unbounded in practice.
**Nothing in that paragraph is fixed by moving content out of the image.** That is the argument
§4 has to answer.

---

## 2. What the image contains, and what invalidates each part

Read `flake.nix` as four strata. The strata already correspond almost exactly to the size/frequency
split in §1.

**(a) Package sets — 96.75 % of the closure, invalidated by `flake.lock`.**
`corePackagesFromNixpkgs` (`flake.nix:720-761`) is everything the integration suite touches plus
POSIX essentials; `fullPackages` (`:767-795`) is the bulk the suite does *not* touch — chromium,
gcc, binutils, nix, podman, tmux, bat, eza, delta, fzf. The minimal variant drops the second set
and is documented as **~1.6–2 GB smaller** (`Justfile:175-176`, `.github/workflows/ci.yml:129-131`)
— NOT independently measured here.

**(b) Our own Go build — 2.4 %, invalidated by any `goSrc` file.** `goBinaries`
(`flake.nix:96-129`) compiles every `cmd/*` in one derivation; `installPrefix` (`:681-709`) copies
four of the five into `/opt/yolo-jail/bin/` plus the flake bundle, and symlinks `/bin/<name>` at the
**absolute store path** rather than through the `/opt/yolo-jail` mountpoint (`:667-679` — a
bind-mount over `/opt/yolo-jail` once bricked pid1). `goprobe` is deliberately excluded (`:664-666`).
The `goSrc` fileset trap is real and documented in the flake itself for both
`bundled_loopholes/` (`:69-74`) and `packs/` (`:75-81`): a top-level package outside the fileset
vanishes from the image while `go build ./...` stays green.

**(c) Generated-into-the-image content.** `mkBinPathLinks` (`flake.nix:429-637`) is one
`runCommand` producing: FHS symlinks (`/usr/bin/env`, `/bin/bash`, `/bin/sh`, `/bin/awk`, `/bin/sed`,
`/bin/grep`, `/bin/find`); the nix-ld ELF interpreter at `/lib/` and `/lib64/` (`:470-472`); the
`/lib` + `/usr/lib` symlink farm for the core trio and the chromium graphics stack (`:489-501`,
`:539-560`); the **user-package** half of that farm from `extraLibPackages` (`:525-535`);
`/etc/subuid`, `/etc/subgid`, `/etc/containers/{storage,containers,policy,registries}.conf`
(`:571-604`); `/etc/ld.so.conf`; and `/etc/localtime` → `/run/localtime`, `/etc/timezone` →
`/run/timezone`, `/etc/ld.so.cache` → `/run/ld.so.cache` (`:452-453`, `:636`). `fakeRootCommands`
(`:850-872`) adds mountpoint dirs and `/etc/passwd` + `/etc/group`.

The three `/run` symlinks are the pattern this whole doc is about, already in production:
**the image bakes a stable name and the boot path supplies the content.** `flake.nix:622-630`
explains why for `ld.so.cache` specifically — the cache is generated at container startup because
the derivation builds natively on darwin, where the Linux `ldconfig` cannot run, so a build-time
cache was silently empty on every macOS-built image.

**(d) `config.Env`** (`flake.nix:880-897`) — `PATH=/bin:/usr/bin`, `SSL_CERT_FILE`,
`LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/<multilib>`, `PKG_CONFIG_PATH`, `FONTCONFIG_*`, `TZDIR`.
Two of these (`SSL_CERT_FILE`, `TZDIR`) are literal nix store paths burned into the image config,
which is a constraint on §4: moving `cacert` or `tzdata` out of the image means the env must move too.

**What `installPrefix` covers, and what it does not.** It covers exactly the `goSrc` fileset plus
`flake.nix`/`flake.lock`, and is invariant across the full/minimal variants and across
`packages:` (§1.5, MEASURED). It does **not** cover: the package sets' *content* (a `flake.lock`
bump moves the image without moving `installPrefix`), `binPathLinks`, or anything generated in
`fakeRootCommands`. That is precisely the property that makes it a good staleness oracle for
"is the loaded image built from this tree's Go code" and a *bad* one for "is the loaded image
current".

---

## 3. Delivery mechanisms that already exist

This is the palette. It is deliberately not a list of things to invent — every row below ships
today, and the design in §4 is an extension of the pattern, not a new one.

### 3.1 The boot-written anchors

`~/.yolo-shims` (blockers: `grep`, `find` → refuse and `exit 127`) and `~/.yolo-launchers` (lazy
installers: `claude`, `pnpm` → install on first use, then `exec`) are **generated at boot by
`internal/entrypoint`, not baked** — `flake.nix:876-879` says so explicitly: *"Blocked-tool shims
are generated at boot by the entrypoint into `$HOME/.yolo-shims` (config-driven) and prepended to
PATH there — there is no baked shim layer any more."* `GenerateShims`
(`internal/entrypoint/shims.go:39`), `GenerateAgentLaunchers` (`:185`) and
`GeneratePackageManagerLaunchers` (`:263`) run on **every boot**, unconditionally, from
`internal/entrypoint/boot.go:428-432`. Both dirs are bind-mount anchors backed by
`<ws>/.yolo/home/{yolo-shims,yolo-launchers}` (`internal/cli/run/assemble_parts.go:73`, `:79`) under
a `:ro` `/home/agent` (`:69`), and both are cleared contents-only — `resetAnchorDir`,
`internal/entrypoint/shims.go:24-29`, because `RemoveAll` on the anchor fails `EROFS` on the `:ro`
parent and leaves stale children in place (`:13-23`).

PATH is built by `BootPath` (`internal/entrypoint/boot.go:356-361`) and mirrored into `.bashrc`
(`internal/entrypoint/shell.go:128-132`). **A finding worth recording while we are here:** the
pre-exec re-set at `boot.go:515-518` omits `e.LocalBin()`, which `BootPath` includes — so anything
the entrypoint spawns between those two points sees a PATH the agent does not. Not caused by
anything in this doc, but any candidate that adds a dir to the delivery PATH has to add it in both
places, and today the two lists already disagree.

**A property of the boot path that C4 needs and gets for free:** those generators are wrapped in
`genStep`, so a failure is **fatal** and aborts the boot with every failure collected
(`internal/entrypoint/boot.go:525-527`, `:564-569`). A delivery step added there fails loudly by
construction — which is exactly the property §7 says the *build* path lacks.

**The ordering is the whole design and it constrains §4.** `~/.yolo-launchers` is ordered *last*,
after `/bin`, specifically so a pack-declared `program fzf` cannot shadow the image's `/bin/fzf` —
"the failure is unrepresentable rather than handled" (`AGENTS.md:210-219`,
`internal/entrypoint/env.go:213-237`). Any proposal that
delivers a package via a boot-written PATH dir inherits that ordering, and therefore **cannot
shadow anything the image bakes**. A candidate that moves a package *out* of the image and into a
launch-time dir is safe on that axis; a candidate that leaves it baked *and* stages it is not — the
baked one silently wins.

### 3.2 The mounted nix store — the key lever, and its hard limit

`internal/cli/run/assemble.go:243-250` mounts, when gated:

```go
nixSocket := "/nix/var/nix/daemon-socket"
nixStore  := "/nix/store"
if shouldMountHostNix(rt, o.PathExists(nixSocket), o.PathExists(nixStore), o.IsMacOS, o.Getenv("YOLO_NIX_HOST_DAEMON")) {
    runCmd = append(runCmd,
        "-v", nixSocket+":"+nixSocket,
        "-v", nixStore+":"+nixStore+":ro",
        "-e", "NIX_REMOTE=daemon")
}
```

So **the entire host store is already visible read-only inside the jail**, and any store path is
already *runnable* by absolute path without being in the image. That is the lever the brief asked
about, and the answer to "is baking ever necessary for something that only needs to be RUNNABLE
rather than on the default PATH" is: **no — on the backends where this mount happens.**

**And the pattern is already live in yolo's own code.** `streamImageCommand` returns the bare store
path as argv (`internal/image/autoload.go:526-528`) and `materializeImage` execs it
(`:399`). On the nested-jail dev loop, that store path was produced by a `nix build` delegated to
the host daemon over this socket, and it is executable **only** because `/nix/store` is bind-mounted.
Same shape at `internal/image/gcroot.go:67`. Nothing about it is in the image. Whatever else is
uncertain about §4, "a store path that is not in the image can be run" is not — it happens every
time a nested jail starts.

Note also that the socket mount is **read-write** (`assemble.go:247` — no `:ro`) and, on Linux, gated
on nothing but path existence (`hostprobes.go:22-24`). Anything in §4 that leans harder on the store
mount leans on that too; see R8.

The gate is the problem (`internal/cli/run/hostprobes.go:15-30`):

| Condition | Store mounted? |
|---|---|
| socket or store missing on host | **no** (`:16-18`) |
| `rt == "container"` (Apple Container) | **no** (`:19-21`) |
| Linux + podman + host daemon | **yes** (`:22-24`) |
| macOS + podman | **no** unless `YOLO_NIX_HOST_DAEMON=1` (`:25-29`) |
| `macos-user` | n/a — never reaches image load (`internal/cli/run/run.go:113-131`) |

**Every candidate in §4 that depends on the mounted store is Linux + podman only, and additionally
requires the user to run a nix daemon.** I could not find a fallback path that would let Apple
Container reach a store path. This is the single largest constraint in the design and I have not
found a way around it.

### 3.3 The other three backends' shapes

- **podman** — the full picture above. `/home/agent` is a `:ro` bind of `GlobalHome()` with rw
  anchors nested inside it (`internal/cli/run/assemble_parts.go:64-121`).
- **Apple Container (`"container"`)** — no store mount. Its base mounts are a different shape
  entirely: **one writable `/home/agent`** over the whole workspace state dir, no `:ro` base
  (`internal/cli/run/assemble_parts.go:36-49`). It **cannot bind-mount a single file**
  (apple/container#1089), which is why `acMaterialize` copies instead
  (`internal/cli/run/helpers.go:101-108`, `internal/cli/run/packfiles.go:78-83`,
  `internal/cli/run/assemble.go:196-204`, `:427-431`; documented at
  `docs/design/pack-system.md:332`, `docs/design/agent-credentials.md:100`) — and it **silently
  ignores `:ro`** (apple/container#889, `internal/cli/run/mounts.go:16-32`), which is why config
  `mounts` are skipped on it wholesale (`assemble.go:140-145`). Pack staging does not even cross as a
  mount here: `YOLO_PACK_ROOT` is set to the *host* path and AC reads it directly
  (`assemble.go:384-392`). Anything staged as a *file* has to be copied on this backend.
- **`macos-user`** — no container, no image, no bind mounts of any kind
  (`docs/design/macos-user-nix-and-features.md:24-31`, `:213-231`;
  `internal/entrypoint/darwin.go:54` — "macos-user bakes no image at all"). It already solves the
  whole problem the other way round: `packages:` is materialized as a **`buildEnv` profile whose
  `bin` is prepended to the agent's PATH** (`flake.nix:1062-1067`,
  `macos-user-nix-and-features.md:42-45`).

**`macos-user` is not merely an analogy — it is C4's mechanism, already written as a reusable Go
package.** `internal/darwinpkg` builds `.#yoloNoncontainerPackages` with `YOLO_EXTRA_PACKAGES`
(`internal/darwinpkg/darwinpkg.go:100-115`, `:132-150`), realizes it with `--out-link` as the GC root
(`internal/darwinpkg/materialize.go:100-102`), and returns `<out>/bin` as a PATH prefix and
`<out>/lib/pkgconfig` as `PKG_CONFIG_PATH` (`darwinpkg.go:174-193`). Its own doc comment states it is
platform-neutral — *"the exact same code resolves `x86_64-linux`"*, and names Linux `guest` as the
next consumer (`darwinpkg.go:2-3`, `:8-14`). **This materially lowers C4's cost estimate**: the
host-side half exists and is tested; what is missing is the jail-side wiring.

### 3.4 Everything else already delivered at launch

Bind mounts carry, today: `/workspace` and `/home/agent` with its rw anchors
(`internal/cli/run/assemble_parts.go:64-121`); the nix socket + store (§3.2); scratch mounts for the
`--read-only` rootfs (`internal/cli/run/runmount.go:20-41`); `/ctx/packs` for pack staging
(`assemble.go:390`), `/ctx/host-*` for pack host-file grants (`internal/cli/run/packhostgrants.go`)
and `/ctx/host-user/<slug>` for user `host_files` (`internal/cli/run/hostfiles.go:68-90`); `/mise`
(`assemble_parts.go:118-121`, always a mount, never image content); git identity and gitignore,
host-composed and `:ro`-mounted (`assemble_parts.go:238-262`); and a dozen single-file binds for
logs, locks and sentinels (`assemble_parts.go:95-103`).

`flake.nix:858-864` records that podman creates a `/ctx` mountpoint on demand even under
`--read-only` — so **a new `/ctx` consumer needs no flake edit at all.** That is the cheapest
extension point in the whole system and it is already proven by `/ctx/packs`.

Boot-time generation, all of it on **every boot** (`internal/entrypoint/boot.go:400-530`), carries:
`/run/localtime` + `/run/timezone` (`internal/entrypoint/system_boot.go:20`, called `boot.go:418`);
`/run/ld.so.cache` (`system_boot.go:58`, called `boot.go:422`); the two anchor dirs (§3.1); the CA
bundle (`internal/entrypoint/system.go:16`, `boot.go:443`); `.bashrc`
(`internal/entrypoint/shell.go:62`, `boot.go:453`); the bootstrap and venv-precreate scripts
(`shell.go:161`, `:324`); the mise config surface (`internal/entrypoint/prism_mise.go:55`,
`boot.go:459`); the MCP node/npx/chrome wrappers (`internal/entrypoint/mcp_wrappers.go:7`,
`boot.go:468`); every pack surface including MCP config, in one loop with no switch on any tool name
(`internal/entrypoint/packsurfaces.go:110`, `boot.go:484`); user `host_files` staging
(`boot.go:491`); and `yolo-cglimit` / `yolo-journalctl` (`internal/entrypoint/scripts.go:10`, `:15`).
Sentinel-gating is the exception, not the rule, and lives in the *generated scripts*: the LSP
install/uninstall keyed on `~/.yolo-installed-lsps` (`shell.go:244-312`), and the agent-CLI update
stamps under `~/.cache/yolo-agent-stamps` (`shims.go:190`).

Agent CLIs install lazily into `$NPM_CONFIG_PREFIX/bin` = `/home/agent/.npm-global`
(`shims.go:294-298`), which is itself the rw `wsState/npm-global` bind
(`assemble_parts.go:70`) — so an installed agent CLI persists per workspace on the host and never
touches the image. mise tools install into `/mise` (`MISE_DATA_DIR=/mise`, `assemble.go:497`), a
mount; only `mise` itself is baked.

**Be honest about the shape of the table in §5: the third column is the largest one.** Almost
everything mutable is already staged. What remains baked is baked because it is either (a) needed
before yolo code runs, or (b) a nixpkgs package, and nixpkgs packages are the 96.75 % that almost
never changes.

---

## 4. Candidates, ranked

Ranked by (frequency × cost) ÷ (risk + work), using §1's measured frequencies. Backend coverage is
stated for each because two of the three most attractive levers are podman-only.

### C1 — Make a failed image build fail as itself. **Rank 1. Not a staging change; a precondition.**

Frequency: every failed build. Cost when it bites: a wrong-layer diagnosis. Risk: near zero.
Work: small. **Backends: all.** See §7 — this has its own section because it is why the question
surfaced.

### C2 — Address the loaded image by content, not by the `:latest` tag. **Rank 2.**

**Mechanism.** Tag the loaded image `yolo-jail:<sha16-of-store-path>` — the key
`internal/image/image.go:161-168` already computes for the cache tar and the GC root — and run that
ref. `alreadyLoaded` becomes "is *this* ref present in the runtime", replacing the
single-most-recent-path comparison at `internal/image/autoload.go:232-237`.

**What it buys.** The §1.5 cross-workspace thrash disappears: each distinct `packages:` list keeps
its own loaded image, and alternating between workspaces costs an `image inspect`, not a 3.28 GiB
load. It also removes a whole class of confusion in which `:latest` names an image built from
someone else's config.

**What breaks.** `paths.JailImage` / `JailImageShort` are referenced from the run assembler
(`internal/cli/run/assemble.go:484`), the checker (`internal/cli/check/check.go:497`), the pruner
(`internal/prune/probes.go:218`), and the Apple Container conversion path
(`internal/image/autoload.go:568`, `:592`). Runtime image count grows — bounded by
`yolo prune --keep-images` (default 2, `internal/prune/prunecmd.go:49`), which would need its
retention rule revisited since "newest 2" is the wrong policy when tags are per-config. Layers
dedup in podman's store, so the incremental disk cost per extra tag should be the changed layer
only — **NOT MEASURED**.

**Verification.** Two workspaces with different `packages:`, alternating launches; assert the second
and subsequent launches emit no "Image load needed" line. An integration test can assert this
without a rebuild by launching the same workspace twice.

**Backends: podman and Apple Container** (both have a tagged image store). Not applicable to
`macos-user`.

### C3 — Stop writing a 3.28 GiB tar on the load path. **Rank 3.**

**Mechanism.** `materializeImage` streams the nix image to `cache/images/<key>.tar` and then
`podman load -i` reads it back (`internal/image/autoload.go:252-273`, `:397-459`). `just load`
already demonstrates the pipe form: `./result | {{runtime}} load` (`Justfile:181-182`). Make the tar
an *option*, not the only path: on podman, stream the derivation straight into `podman load`; keep
the tar only when the runtime needs a file (Apple Container's skopeo conversion,
`autoload.go:561-584`) or when an explicit offline-fallback flag asks for it.

**What it buys.** Directly deletes the largest measured artifact in §1.6 — 404 GiB, growing at
~16 GiB/day. Removes one full 3.28 GiB disk write per rebuild (60 % of commits).

**What breaks.** The cached-tar fallback at `autoload.go:192-207` is the only thing that lets a jail
start when the build fails and no image is loaded. Removing tars unconditionally would remove that
safety net, which interacts badly with C1's finding that build failures are already under-reported.
The honest form is "keep N tars, stream the rest", not "never write a tar". The byte-progress UI
(`autoload.go:461-499`) would need a source of truth other than the file it is writing.

**Verification.** Compare `du -s cache/images` across ten launches with a changing `internal/`
file, before and after. No container-behavior change to verify — the loaded image is identical.

**Backends: podman only** for the pipe form; Apple Container keeps the file path.

### C4 — Deliver `packages:` from the mounted store instead of baking it. **Rank 4. The question's actual answer.**

**Mechanism.** Stop threading `YOLO_EXTRA_PACKAGES` into the image build. Instead, on the host,
realize a `buildEnv` of the config's `packages:` and at boot symlink its `bin` into a boot-written
PATH dir, its `lib/*.so*` into a boot-written `LD_LIBRARY_PATH` dir, and its `lib/pkgconfig` into a
`PKG_CONFIG_PATH` dir. The store paths resolve because `/nix/store` is mounted `:ro` (§3.2).

**Half of this already exists and is tested.** `internal/darwinpkg` is the host side, verbatim:
build `.#yoloNoncontainerPackages` with `YOLO_EXTRA_PACKAGES` (`darwinpkg.go:100-115`, `:132-150`),
GC-root it with `--out-link` (`materialize.go:100-102`), return `<out>/bin` as a PATH prefix and
`<out>/lib/pkgconfig` as `PKG_CONFIG_PATH` (`darwinpkg.go:174-193`) — and its doc comment already
declares itself platform-neutral with a Linux consumer in mind (`darwinpkg.go:2-3`, `:8-14`). The
jail side is a `genStep` in `internal/entrypoint` alongside the existing anchor generators, which
gets fatal-on-failure for free (§3.1). **What is unbuilt is the wiring and the lib-farm story, not
the mechanism.**

**What it buys — and it is the largest ceiling of any candidate.** The image derivation stops
depending on `builtins.getEnv`, so:

1. `--impure` leaves the run path, and the image becomes **one artifact for every workspace on the
   machine**. The §1.5 thrash is deleted at the root rather than mitigated (C2 mitigates it).
2. The image becomes cacheable — the binary cache currently only ever holds the stock,
   zero-`packages:` image (§6), so today *any* user with a `packages:` entry is guaranteed a cache
   miss. After C4, there is only the stock image.
3. Adding a package stops costing a reload at all; it costs a `nix build` of a small `buildEnv`.

**What breaks — and it is a lot.**

- **`LD_LIBRARY_PATH` and the lib farm.** The image bakes `LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/<multilib>`
  (`flake.nix:883`) and the farm links user packages' `.so` into `/lib` and `/usr/lib`
  (`flake.nix:509-535`). Those dirs are image content on a `--read-only` root. Delivery requires a
  writable dir *appended* to `LD_LIBRARY_PATH`, and `flake.nix:616-621` warns that "a consumer that
  scrubs `LD_LIBRARY_PATH` cannot be rescued" — the nix-ld fallback dir
  (`/usr/share/nix-ld/lib`, `flake.nix:482-488`) is also baked and is the *only* search path under a
  scrubbed environment.
- **`/etc/ld.so.cache`.** Already generated at boot into `/run` (`flake.nix:622-636`), so this part
  is fine — it is the existing pattern.
- **The two integration tests in §7** assert `/lib/libzbar.so.0` and `/lib/libsodium.so.*` by exact
  path (`integration/packages_test.go:58`, `:110-112`). They encode the baked-farm layout and would
  have to move to whatever dir C4 writes.
- **Backends.** Podman-on-Linux-with-a-nix-daemon only (§3.2). Apple Container and macOS-podman get
  nothing; the code would have to keep the baking path for them, which means maintaining two
  package-delivery mechanisms. `macos-user` already has C4's mechanism and does not need it.

**Verification.** The existing two `packages:` integration tests, retargeted; plus a new one that
asserts the image store path is *unchanged* by adding a `packages:` entry — which is the whole point
and is a one-line `nix eval` assertion.

**Verdict: worth designing, not worth building until C1–C3 land.** The ceiling is the highest of any
candidate; the work is smaller than it first looks because the host half is written; and the risk is
still the highest, concentrated entirely in `LD_LIBRARY_PATH` and the backend asymmetry. C2 captures
most of the *frequency* benefit for a fraction of the risk, which is why it outranks this.

### C5 — Move `fullPackages` out of the run-path image. **Rank 5.**

**Mechanism.** Same as C4 (symlink farm from the mounted store into a boot-written PATH dir), applied
to `flake.nix:767-795` — chromium, gcc, binutils, tmux, bat, eza, delta, fzf, nix, podman, htop.
The run path then builds the minimal variant.

**What it buys.** ~1.6–2 GB off the streamed tar (`Justfile:175-176` — documented, not measured
here), multiplied by *every* rebuild, i.e. 60 % of commits — a bigger frequency base than C4's.

**What breaks.** The PATH-ordering invariant (§3.1) inverts: today `/bin/fzf` beats a pack's
`program fzf` *by position*, and `AGENTS.md:224-226` explicitly flags that as a property to
re-check before baking a name a pack also claims. Moving `fzf` out of `/bin` and into a
launch-time dir changes which wins, silently, for any pack that declares it. Chromium additionally
drags the whole `withChromium` branch of `mkBinPathLinks` (`flake.nix:454-458`, `:536-570`) —
font links and `/etc/fonts` — which is baked content, not a PATH entry.

**Verdict: the best size-per-risk ratio *after* C4 exists, because it reuses C4's mechanism
wholesale.** Building it first would mean building that mechanism for the lower-value case.
Podman-on-Linux only, same as C4.

### C6 — Layer the image so the delta is the unit of transfer. **Considered, rejected for now.**

`streamLayeredImage` with `maxLayers = 100` (`flake.nix:841`) already gives each popular store path
its own layer, and `podman load` skips layers it already has. Using `fromImage` to build a thin
top image over a stable base would make the *streamed tar* small too. **Rejected as subsumed by C3
for podman** (a pipe makes the tar cost zero regardless of layering) and **unproven for Apple
Container**, where the skopeo conversion (`internal/image/autoload.go:561-584`) may not preserve the
dedup. Worth revisiting if C3 turns out to be blocked.

### C7 — Skip the build when nothing moved. **Considered, rejected.**

A cheap `nix eval .#installPrefix.outPath` gate (0.22 s vs 1.28 s, MEASURED) before the full build.
**Rejected:** it saves ~1 s on a path that costs seconds-to-minutes elsewhere, and `installPrefix`
is invariant under `flake.lock` (§2), so the gate would be wrong exactly when it mattered. Test 1
of [`gate-placement-principle.md`](gate-placement-principle.md) applies by analogy: a check that
cannot see the case it is guarding against is worse than none.

### Ranking summary

| # | Candidate | Frequency | Cost avoided | Risk | Work | Backends |
|---|---|---|---|---|---|---|
| C1 | Honest build failure | every failed build | hours of wrong-layer debugging | very low | small | all |
| C2 | Content-addressed image tag | every cross-workspace alternation | 3.28 GiB load | low | small–medium | podman, Apple Container |
| C3 | Stream to the runtime, tar optional | 60 % of commits | 3.28 GiB write; 404 GiB accrued | low–medium | small | podman |
| C4 | `packages:` from the mounted store | every `packages:` user, every launch | the whole `--impure` axis | **high** | medium — the host half is `internal/darwinpkg`, already written | **podman + Linux + nix daemon only** |
| C5 | `fullPackages` from the mounted store | 60 % of commits | ~1.6–2 GB per rebuild | high | small, *after* C4 (same mechanism) | **podman + Linux + nix daemon only** |

---

## 5. The central table: must bake / could move / already delivered

**MUST BAKE** — needed before any yolo code runs, or must resolve on the image's own PATH
(`PATH=/bin:/usr/bin`, `flake.nix:881`).

| Content | Why it cannot move |
|---|---|
| `/bin/yolo-entrypoint` (`flake.nix:694-708`) | The container argv is `<image-ref> yolo-entrypoint` (`internal/cli/run/assemble.go:484`) — resolved on the image's PATH by the runtime, before one line of yolo code has run. Nothing yolo does at launch can supply it. |
| `/bin/bash`, `/bin/sh`, `/usr/bin/env`, coreutils (`flake.nix:432-438`) | The entrypoint's generated scripts and the runtime's own exec path need a shell that exists in the rootfs. |
| nix-ld at `/lib/ld-*` and `/lib64/ld-*` (`flake.nix:470-472`) | It is a `PT_INTERP` — an absolute path burned into every FHS binary, not a PATH entry. A binary cannot be told to look elsewhere. |
| `/usr/share/nix-ld/lib` core trio (`flake.nix:482-501`) | The *only* library search path an FHS binary gets under a fully scrubbed environment (`flake.nix:483-488`). By construction it cannot depend on env the jail sets. |
| `/etc/passwd`, `/etc/group` (`flake.nix:868-871`) | Read by podman and sshd before/independently of yolo. |
| `/etc/containers/*.conf`, `/etc/subuid`, `/etc/subgid` (`flake.nix:571-604`) | Nested-podman config; consulted by podman inside the jail, and the root fs is `--read-only`. |
| `config.Env` incl. `SSL_CERT_FILE`, `TZDIR` (`flake.nix:880-897`) | Literal store paths in the image config. Moving `cacert`/`tzdata` means moving these too — a coupled change, not a free one. |
| `/etc/ld.so.conf`, and the `/run/*` **symlinks** (`flake.nix:631-636`, `:452-453`) | The *link* must be baked because `/etc` is read-only at runtime. The *target* is already staged — this row is the pattern, not an exception. |

**COULD MOVE** — mechanism and cost.

| Content | Mechanism | Cost / what breaks |
|---|---|---|
| `extraPackages` from `packages:` (`flake.nix:241-242`, `:843-847`) | C4: host-side `buildEnv`, symlink `bin`/`lib`/`lib/pkgconfig` into boot-written dirs on the mounted store | Podman+Linux+daemon only; `LD_LIBRARY_PATH` and the `/lib` farm are baked; two integration tests assert baked paths |
| `extraLibPackages` — the user half of the `/lib` farm (`flake.nix:509-535`) | Same as above; append a boot-written dir to `LD_LIBRARY_PATH` | Scrubbed-env consumers lose it (`flake.nix:616-621`); nix-ld's compiled-in fallback dir stays baked |
| `fullPackages` (`flake.nix:767-795`) | C5, same mechanism | Inverts the PATH-ordering invariant that lets `/bin/fzf` beat a pack's `program fzf` (`AGENTS.md:210-226`, `internal/entrypoint/env.go:213-237`); chromium drags baked font links |
| The `share/yolo-jail/bin/linux-<arch>/` duplicate of the binaries (`flake.nix:699-700`) | Nothing — deliberate (`flake.nix:657-666`) | 2 % of the image. Not worth it. |

**ALREADY DELIVERED** — the largest column, and the reason this design is an extension rather than
an invention.

| Content | How |
|---|---|
| Blocked-tool shims (`~/.yolo-shims`) | `GenerateShims`, every boot (`internal/entrypoint/shims.go:39`, `boot.go:428`); `flake.nix:876-879` records that the baked shim layer was *removed* |
| Lazy agent/package-manager launchers (`~/.yolo-launchers`) | `GenerateAgentLaunchers` / `GeneratePackageManagerLaunchers`, every boot (`shims.go:185`, `:263`; `boot.go:430-432`); ordered last on PATH (`internal/entrypoint/env.go:213-237`) |
| `/etc/ld.so.cache` | Boot-generated into `/run` (`internal/entrypoint/system_boot.go:58`, `boot.go:422`); rationale `flake.nix:622-636` |
| `/etc/localtime`, `/etc/timezone` | Boot-populated `/run` targets (`internal/entrypoint/system_boot.go:20`, `boot.go:418`) |
| CA bundle, `.bashrc`, bootstrap + venv scripts, `yolo-cglimit`, `yolo-journalctl`, MCP wrappers | Boot-generated (`internal/entrypoint/system.go:16`, `shell.go:62`, `:161`, `:324`, `scripts.go:10`, `:15`, `mcp_wrappers.go:7`) |
| The whole nix store | `-v /nix/store:/nix/store:ro` (`internal/cli/run/assemble.go:246-249`) — gated per §3.2 |
| Workspace, home + rw anchors, scratch mounts, `/ctx/*`, `/mise` | `internal/cli/run/assemble_parts.go:36-121`, `runmount.go:20-41`, `assemble.go:126-147`, `:390` |
| Pack content | Staged host-side by `internal/packstage`, mounted `:ro` at `/ctx/packs` (`internal/cli/run/packs.go:55-197`, `assemble.go:390`); on Apple Container, read directly from the host path via `YOLO_PACK_ROOT` (`assemble.go:384-388`) |
| Every pack surface, incl. MCP config | `ConfigurePackSurfaces`, one loop, every boot (`internal/entrypoint/packsurfaces.go:110`, `boot.go:484`) |
| mise tools | `mise` is baked (`flake.nix:728`); the tools install into the `/mise` mount (`assemble.go:497`, `assemble_parts.go:118-121`) |
| Agent CLIs (claude/copilot/codex/…) | Lazily npm-installed into `/home/agent/.npm-global` (`shims.go:294-298`), itself the rw `wsState/npm-global` bind (`assemble_parts.go:70`) |
| LSP servers | Sentinel-tracked install *and uninstall* (`internal/entrypoint/shell.go:244-312`) |
| Git identity + global gitignore | Host-composed, `:ro`-mounted (`internal/cli/run/assemble_parts.go:238-262`) |
| User `host_files` | Staged to `/ctx/host-user/<slug>` (`internal/cli/run/hostfiles.go:68-90`), consumed at `internal/entrypoint/hostfiles.go:35` |
| `packages:` on `macos-user` | Already a store `buildEnv` PATH-prepend (`flake.nix:1062-1067`, `internal/darwinpkg/darwinpkg.go:174-193`) — C4's shape, shipped |

---

## 6. The binary-cache alternative, argued fairly

A binary cache changes the economics entirely: a "rebuild" becomes a download. If
`yolo-jail.cachix.org` served the image, §1's rebuild frequency would matter far less on a fresh
machine, and macOS users would not need a Linux builder at all — which is the reason the cache was
set up (`docs/plans/handoff-cachix-cache.md:8-16`).

**Where it genuinely helps.** First-run cost on a new machine or a CI runner. A `flake.lock` bump —
the one case where the whole 3.12 GiB nixpkgs half really does move (§1.4) — is exactly the case a
cache turns into a download, and it is why `imageClosureRoot` was factored out to be substitutable
from `cache.nixos.org` (`flake.nix:811-815`).

**Where it does not help, and this is the developer's inner loop.** A from-source Go build of
uncommitted local code can never be in any cache. Sixty percent of commits change `goSrc` (§1.1);
those images have never existed anywhere before and never will again. **The cache is structurally
incapable of touching the dominant case**, and the dominant case is the one the maintainer feels.

Four further limits, all of which I verified rather than assumed:

1. **The cache has, by the repo's own record, never been pushed to.** `handoff-cachix-cache.md:3-7`
   marks the wiring ENABLED but the first push and the Mac-side download proof as outstanding
   (`:58-64`, `:71-88`).
2. **Even when populated it holds only the stock image.** `.github/workflows/publish.yml:140-145`
   builds `.#ociImage` and `.#ociImageMinimal` with no `YOLO_EXTRA_PACKAGES` set. With the var
   unset, `extraPackageSpecs = []` (`flake.nix:140-143`). So **every user with a `packages:` entry
   is a guaranteed cache miss, by construction.** That makes C4 a *prerequisite* for the cache
   being useful to anyone who uses `packages:` — the two are complements, not alternatives.
3. **The image build path does not opt into the flake's substituter.** `--accept-flake-config`
   appears in exactly two lines repo-wide, both in `internal/darwinpkg/darwinpkg.go` (`:80`, `:91`),
   whose own comment explains that without it nix prints *"ignoring untrusted flake configuration
   setting 'extra-substituters'"* and never consults the cache. The three invocations that build or
   dry-run the **image** do not pass it: `internal/image/autoload.go:319-323`,
   `internal/image/build.go:44-49`, `internal/cli/check/sections_nix.go:19-20`. I observed exactly
   that warning on every probe run for this doc. So today the flake's cache is reachable only by a
   nix *trusted user* who has added it to their own `nix.conf`.
4. **It serves at most two of three backends.** `macos-user` has no image
   (`docs/design/macos-user-nix-and-features.md:47-50`).

**Verdict: worth finishing, and cheap to finish — but it is not an alternative to §4.** The cache
attacks first-run and `flake.lock` cost; C1–C3 attack the inner loop. Item 3 above is a
two-character fix with a real payoff and belongs in §9 regardless of anything else in this doc.

---

## 7. The silent-fallback defect — why staging is worthless without honest failure

**This section exists because the question surfaced from a wrong-layer diagnosis.** On the morning
of 2026-08-15, two macOS integration tests — `TestExtraPackageLibFarm`
(`integration/packages_test.go:53`) and `TestDevPackageLinksRuntimeLib` (`:106`) — failed with a
**lib-farm assertion**: `libzbar.so.0 not linked into /lib //usr/lib resolving to /nix/store`
(`:72-74`) and *"the `.dev` request did not link the runtime lib into the farm"* (`:113-117`). The
actual cause was a failed image build.

**The mechanism, exactly.** Both tests write a `packages:` config and launch a jail
(`:55`, `:108`). The launch runs the `--impure` build. When that build fails,
`buildImageStorePath` returns `("", tail)` (`internal/image/autoload.go:353-355`), and control
reaches:

```go
if currentPath == "" {
    imageName := JailImage(o.Runtime)
    if rc, ran := o.Run(ImageInspectCmd(o.Runtime, imageName)); ran && rc == 0 {
        fmt.Fprintln(out, "Using existing "+imageName+" image.")
        return true
    }
```
— `internal/image/autoload.go:184-191`.

Three things go wrong at once:

1. It returns **true**, so the jail launches on the *previous* image — which has no zbar in its lib
   farm, because zbar was in the build that failed.
2. `buildTail` — the last 30 lines of nix stderr, already captured at `:169` — is **dropped on the
   floor**. It is only ever surfaced through `DiagnoseFailure` at `:218`, reachable solely when
   there is *also* no existing image and no cached tar.
3. The message says "Using existing … image", which reads as a normal cache hit, not a fallback
   from a failure.

The result is a diagnosis two layers from its cause, pointing at the lib farm — code that is
working correctly.

**The framing that matters for this whole document: a staging change is worthless if a failure to
stage is invisible.** Every candidate in §4 makes the launch path do *more* work before the
container starts. C4 in particular replaces "the package is baked in, or the build failed loudly"
with "the package is symlinked at boot, or … something". If a failure to deliver degrades to a
silent stale-content launch the way a failed build does today, C4 converts a build error into a
mysterious missing library. **C1 is a precondition for C2–C5, not a parallel nicety.**

**The minimal honest fix — proposed here, SHIPPED since as `7830f65`** (2026-08-15; this list was
written as a proposal, deliberately not implemented, because another lane owns `internal/image/`).
The shipped fix does all four bullets and takes the fourth further than "consider": a failed build
is fatal by default, with `YOLO_ALLOW_STALE_IMAGE=1` as the opt-in. See §10 OQ-2 for the ruling and
for where it differs from this document's leaning.

- On the `currentPath == ""` fallback branch, distinguish the two reasons it was taken. `SkipBuild`
  (a degraded launch, already handled at `:211-217`) is legitimate. A **failed build** is not the
  same event and must not print the same word.
- When a build was attempted and failed, print the `DiagnoseFailure(buildTail)` output **before**
  falling back — the data is already in hand at `:169`; only the call site is missing.
- Say what the fallback means: not "Using existing image" but that the build failed and the jail is
  starting on a **previously built** image, which may not match this config.
- Consider making the fallback opt-in when a build was attempted and failed — a flag or an env var —
  so an automated caller (the integration suite) fails at the build rather than at an assertion.
  That last one is a behavior change with real blast radius and belongs to whoever owns the file.

The smallest version of this — one extra `fmt.Fprintln` of the tail plus honest wording — costs
nothing and would have turned this morning's two-hour lib-farm hunt into a one-line read.

---

## 8. What this does NOT cover

- **The build's own speed.** Nothing here proposes making `go build` or nix evaluation faster. §1.3
  measures both and finds them small relative to delivery.
- **Image content policy.** Whether `chromium` or `gcc` *should* be in a jail is a product question;
  §4's C5 only addresses where they are delivered from.
- **`macos-user`.** It has no image and is out of scope except as the existence proof in §3.3.
- **The GC-root / store-lifecycle ledger.** `internal/image/gcroot.go` and
  `docs/plans/storage-lifecycle.md` own that; §1.6 only cites its baseline.
- **Anything requiring a measured `nix build` of the image or a measured `podman load`.** Both are
  flagged NOT MEASURED in §1.3 and neither ranking above depends on a guessed value for them.

---

## 9. Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | **C4/C5 are podman-on-Linux-only**, so shipping either means maintaining two package-delivery mechanisms indefinitely — the exact "fill the matrix" failure [`happy-path-principle.md`](happy-path-principle.md) warns about. | Do not ship C4 as *the* mechanism; ship it as an opt-in fast path with the baked path as the fallback, and only after C1–C3. Or accept a documented backend asymmetry, explicitly. |
| R2 | **Delivering a package by PATH dir cannot shadow a baked one** (§3.1) — so a half-migration where a package is both baked and staged silently runs the baked version. | Migration must be all-or-nothing per package: remove from `flake.nix` in the same change that adds the staging. A test that asserts `which <pkg>` resolves to the staged dir catches the half-state. |
| R3 | **C2 multiplies loaded images in the runtime store**, and `--keep-images 2` is the wrong retention rule for per-config tags. | Revisit `internal/prune/prunecmd.go:49` retention as part of C2, not after. Measure podman's incremental per-tag cost first — it is currently NOT MEASURED. |
| R4 | **C3 removes the offline safety net** if taken to "never write a tar", and §7 shows build failures are already under-reported. | Keep-N, not zero. Land C1 first so a build failure is visible before the fallback's usefulness is reduced. |
| R5 | **Scrubbed-environment breakage.** `flake.nix:616-621` records that a consumer scrubbing `LD_LIBRARY_PATH` cannot be rescued; C4 moves user libs from a baked dir to an env-dependent one, widening that class. | Keep the nix-ld fallback dir (`/usr/share/nix-ld/lib`) baked and consider extending it — it is the one search path that survives a scrub. Requires an explicit call on how large that "shadow surface" may grow (`flake.nix:483-488` says keep it to the trio). |
| R6 | **The `goSrc` fileset trap bites any new package** added under a new top-level dir, and it fails silently in the image while `go build ./...` stays green (`flake.nix:69-81`). | Not made worse by anything here, but any C4/C5 implementation that adds a Go package outside `cmd/`/`internal/` must add it to the fileset in the same commit. |
| R7 | **Every number in §1.6 comes from one machine — this jail.** Growth rates on a laptop, and podman-storage costs, may differ by an order of magnitude. | The *ratios* (3.25 % changing content, 180 KiB delta → 3.28 GiB transfer) are machine-independent and are what the ranking rests on. The absolute GiB figures are illustrative. |
| R8 | **C4/C5 make the jail structurally dependent on the host nix daemon.** The socket is mounted **read-write** with no `:ro` and, on Linux, no gate beyond path existence (`internal/cli/run/assemble.go:247`, `internal/cli/run/hostprobes.go:22-24`) — so a jail already has full nix-client access to the host store. Today that is incidental; after C4 the agent's toolchain does not exist without it. | This is a pre-existing property, not one C4 introduces — but C4 turns "convenient" into "load-bearing", which changes what a daemon outage looks like (a jail with no `packages:` tools instead of a jail that cannot `nix build`). Worth a deliberate decision rather than an inherited one. Blast-radius reasoning per [`gate-placement-principle.md`](gate-placement-principle.md) Test 2. |

---

## 10. Open Questions

1. **Is a backend asymmetry acceptable for package delivery?** C4 and C5 work only on
   podman + Linux + a host nix daemon (§3.2). Shipping them means either an opt-in fast path with
   the baked path retained as fallback (two mechanisms, forever) or an explicit "this optimization
   is Linux-only" in the docs. This is the question that decides whether C4 is a design or a
   curiosity.

   _Leaning:_ Opt-in fast path with the baked path retained — but only if C2 and C3 have already
   landed, because if they have, C4's remaining marginal benefit may not justify the second
   mechanism at all. I would want to re-measure after C2/C3 before committing.

   **Resolved by:** landing C2 + C3 and re-running §1.6's measurement.

   **Answer:**
   > _(empty — fill in when decided)_

2. **✅ Should the fallback in `autoload.go` still return `true` after a failed build? — ANSWERED
   BY SHIPPED CODE (`7830f65`, 2026-08-15).** §7's minimal
   fix makes the failure *visible*. It does not decide whether a jail should still start on a stale
   image. Starting is friendlier for a human with a transient network failure; refusing is correct
   for the integration suite and for anyone whose config demands content the stale image lacks.

   _Leaning:_ Print honestly and still start **for an interactive human**, refuse for a
   non-interactive caller — the same "a gate that cannot tell a human from a pipe is not asking a
   human" reasoning as [`gate-placement-principle.md`](gate-placement-principle.md). But this is a
   behavior change in a file another lane owns.

   **Resolved by:** a maintainer ruling, then whoever owns `internal/image/`.

   **Answer:**
   > **No — a build that ran and failed is FATAL, and the shipped answer is NOT the leaning above.**
   > `7830f65` (*"make a failed image build fail as itself, not a silent stale launch"*) splits the
   > `currentPath == ""` branch on a new `buildFailed` flag, prints the classification **and nix's
   > own stderr**, and returns `false`. The opt-out is **`YOLO_ALLOW_STALE_IMAGE=1`** — one env var,
   > which still prints the whole report — not a TTY test.
   >
   > **The divergence is the point, and it was argued rather than overlooked.** The leaning wanted
   > the gate to tell a human from a pipe; the shipped code deliberately does not, because what
   > makes a stale run safe is not *who* is running but that someone **SAID** the image is stale —
   > "precisely the knowledge whose absence caused the bug". The asymmetry it leans on: refusing
   > costs a rerun with one env var, continuing costs an investigation at the wrong layer. The full
   > three-option argument (loud-but-continuing / fatal-with-no-way-past / this) lives on the branch
   > itself, `internal/image/autoload.go` at the `currentPath == ""` comment.
   >
   > **`SkipBuild` is untouched** — no build was attempted, so the pre-existing degraded path
   > (D2's) runs exactly as before, and that silence is deliberate: warning there would train the
   > reader to ignore the warning.

3. **Do we want the image tag to be content-addressed (C2), or the sentinel to consult its full
   history?** C2 tags by store-path hash. A cheaper variant keeps `:latest` and makes
   `alreadyLoaded` check membership in the ten-entry LRU rather than equality with the most recent —
   but `internal/image/autoload.go:226-231` documents precisely why equality was chosen (a reverted
   config can reproduce a store path still in the history while a *different* path is what `:latest`
   actually points to). Content-addressed tags dissolve that ambiguity; LRU membership reintroduces
   the bug the comment describes.

   _Leaning:_ Content-addressed tags. The comment at `:226-231` is an argument *for* C2, not
   against it — it is describing the failure mode of not knowing what `:latest` is.

   **Resolved by:** a maintainer ruling on whether `localhost/yolo-jail:latest` is a public surface
   anyone depends on by name.

   **Answer:**
   > _(empty — fill in when decided)_

4. **Should `packages:` remain workspace-scope?** §1.5 shows a workspace-scope `packages:` list —
   agent-editable, travelling with a repo — mints a distinct 3.28 GiB image and imposes a reload on
   every other workspace on the machine. That is a real cost one repo can impose on an unrelated
   one. C4 removes the cost; user-scoping would remove it differently, and more bluntly.

   _Leaning:_ Keep it workspace-scope. Per
   [`gate-placement-principle.md`](gate-placement-principle.md) the restriction on `packs` and
   `host_files` exists because those grant *host access*; `packages:` grants only a tool, so a scope
   restriction here would be aimed at the wrong problem. Fix the cost, not the scope.

   **Resolved by:** C4 or C2 landing; if neither does, revisit.

   **Answer:**
   > _(empty — fill in when decided)_

5. **Is 404 GiB of cached tars a bug or a configuration?** Retention exists (keep 3) and is manual
   (§1.6). The 20 GiB hint fired for twenty-four days without effect. Options: leave manual, prune
   automatically at materialize time, or make C3 remove the artifact class entirely.

   _Leaning:_ C3 makes the question mostly moot on podman, and an automatic keep-N sweep at
   materialize time is a five-line change that fixes it everywhere else. I would do both.

   **Resolved by:** a maintainer ruling on whether `yolo` may delete a user's cached tars without
   `--apply`.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 11. What to do first — dependency-ordered

1. **C1 — make a failed image build fail as itself** (§7). Everything else in this doc makes the
   pre-container phase do more work; until a failure there is legible, every later change is
   debugged at the wrong layer. Smallest useful version: print `DiagnoseFailure(buildTail)` on the
   existing-image fallback and reword the message. No behavior change, no backend risk.
2. **Pass `--accept-flake-config` on the three image `nix` invocations** (§6 item 3). Independent of
   everything else, two-character change, and it is the difference between the flake's declared
   binary cache being consulted and being ignored. It also makes C1's failure reports meaningful —
   today a "build failed" on macOS may just be a cache that was never asked.
3. **C2 — content-addressed image ref** (§4). Deletes the cross-workspace reload thrash on both
   container backends without touching the flake. Requires the `--keep-images` retention rule to be
   revisited in the same change (R3).
4. **C3 — stream to the runtime, tar becomes keep-N** (§4). Reclaims the largest measured artifact.
   Depends on C1 (the tar is the fallback C1 makes legible) and is cleaner after C2 (which decides
   what a "current" image is).
5. **Re-measure.** Repeat §1.6 and §1.4 after 3 and 4. OQ-1 explicitly turns on this measurement:
   if C2 + C3 flatten the cost curve, C4's second mechanism may not be worth its backend asymmetry.
6. **C4, then C5** — only if step 5 says so, and only with the OQ-1 ruling in hand. C5 reuses C4's
   mechanism, so their order is fixed.
