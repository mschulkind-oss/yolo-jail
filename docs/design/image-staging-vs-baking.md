# Baking vs. staging — what the image must contain, and what a launch can deliver

**Status:** ANALYSIS + PROPOSAL — **all five questions RULED** (OQ-2 on 2026-08-15, the other four
on 2026-08-25) and **C2 + C3 SHIPPED on 2026-08-25**, the same day their rulings landed. **C4–C5
remain unbuilt and gated.** Written 2026-08-15, re-checked against the tree 2026-08-23, re-stamped
2026-08-25 when the maintainer ruled on OQ-1, OQ-3, OQ-4 and OQ-5 (§10.1), and re-anchored
2026-08-25 after C2+C3 landed. **Every `file:line` anchor below was re-derived against the tree**;
the C2/C3 implementation moved `internal/image/autoload.go` by up to +264 lines and deleted two
functions this doc used to cite, so §1.4, §1.5, §4 and §9 all changed anchors. Earlier drift is on
the same record: all 49 `flake.nix` citations were wrong by +25 to +142 lines until 2026-08-25 (that
file moved in `60376fed`, 2026-08-20), as were every `AGENTS.md` citation and the
`internal/entrypoint/{shims,shell,boot,env,packsurfaces}.go` and `internal/cli/run/assemble*.go`
ones. §7's anchors are the deliberate exception — they are pre-`7830f65` line numbers, and §7 says
so. All measurements taken in this development jail on 2026-08-15 unless dated otherwise; every
number below is labelled **MEASURED** or **NOT MEASURED**.

> [!IMPORTANT]
> **A ruling is not an implementation — but two of these are now both.** The 2026-08-25 rulings
> settled C2's mechanism, C3's verdict, C4/C5's shape, and the scope question under `packages:`.
> C2 and C3 were then BUILT the same day, so the sections describing them are records, not plans:
> the two defects §1.4 and §1.5 measure are fixed on podman. **C4 and C5 build nothing yet**, §11 is
> still the order for them, and C4 is still gated on the §11 step-5 re-measurement — which is now
> possible for the first time, because it was waiting on C2+C3.

**What is built, so the body's "nothing built" framing is not read too widely:**

| Item | State | Evidence, re-checked 2026-08-25 |
| :--- | :--- | :--- |
| **C1** — a failed image build fails as itself | ✅ shipped `7830f65`, 2026-08-15 | `internal/image/autoload.go:195-265` — the `buildFailed` flag splits the fallback branch, prints nix's own stderr (`:260`), returns `false` (`:263`); opt-out is `YOLO_ALLOW_STALE_IMAGE=1` (`:170`). This is OQ-2 in §10.1 |
| **`--accept-flake-config`** on the image `nix` invocations (§6 item 3) | ✅ shipped | `internal/image/nixflags.go:35` and `internal/darwinpkg/darwinpkg.go:91` (verified 2026-08-25). Note the consequence: the substituter surface it opens is now live, which is what [`macos-user-build-step-threat-model.md`](macos-user-build-step-threat-model.md) Q2 asks about |
| **C2** — address the image by content | ✅ shipped 2026-08-25 | `image.JailImageRef` (`internal/image/image.go:126-128`) is the ref a jail runs; the load decision is `image inspect <content ref>` (`internal/image/autoload.go:422-424`), and the ref is threaded to the argv through `assembleInput.imageRef` (`internal/cli/run/assemble.go:743-748`). This is OQ-3 in §10.1 |
| **C3** — stream, write no tar | ✅ shipped 2026-08-25 | `ImageLoadStdinCmd` (`internal/image/image.go:55-60`) is the decision point; `internal/image/streamload.go` is the pipe. On podman `cache/images` stays EMPTY, asserted on disk (`internal/image/streamload_test.go:60`). This is OQ-5 here and OQ-DF1 in [`minimal-disk-footprint.md`](minimal-disk-footprint.md) |
| **C4 · C5** | ❌ not built, and still **gated** | OQ-1 rules the *shape* for both — opt-in fast path, baked path retained — but the go/no-go still waits on the re-measurement after C2+C3 (§11 step 5), which is now *possible* rather than blocked. C5 reuses C4's mechanism and is ordered after it (§4 C5, §11 step 6) |
| **The retention rule (R3)** | ❌ not settled | C2 armed `yolo prune`'s old-image pass for the first time, so it had to be made SAFE in the same change — entries deduped by image ID, and a liveness veto from the load sentinel (`internal/prune/probes.go:211-254`, `ProtectedImageTags` in `internal/prune/imageroots_probe.go`). The *number* is still [`minimal-disk-footprint.md`](minimal-disk-footprint.md) OQ-DF3, still OPEN; `--keep-images` default 2 is untouched (`internal/prune/prunecmd.go:49`, `:138`) |

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

**Reads with:** [`minimal-disk-footprint.md`](minimal-disk-footprint.md) (where the OQ-5 ruling is
actually executed — this doc measured the 404 GiB and carries the ruling that it is a bug; that one
owns the fix),
[`storage-and-config.md`](storage-and-config.md) (where these bytes live),
[`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) (the 2026-07-22 baseline this
doc's growth numbers are measured against, and the GC work OQ-5 says is nowhere near enough),
[`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) (the backend with no image at
all), [`../plans/handoff-cachix-cache.md`](../plans/handoff-cachix-cache.md) (the binary-cache
prior art, argued in §6 — and the surface OQ-3's cachix caveat is about).

---

## 1. The cost model

### 1.1 What triggers a rebuild, and how often

Every `yolo` launch runs `nix build .#ociImage --impure` before the container starts (the argv is
`ociBuildArgv`, `internal/image/nixflags.go:47-56`, run at `internal/image/autoload.go:403` and
reached from `internal/cli/run/imageload.go:15-40`). The derivation moves when anything in the
`goSrc` fileset moves — `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/`, `packs/`
(`flake.nix:86-109`) — or `flake.nix` / `flake.lock` move, or `YOLO_EXTRA_PACKAGES` changes (§1.5).
`bundled_loopholes/` was the sixth entry of that fileset when the table below was measured; it left
on 2026-08-19 together with the directory, and the flake keeps the record at `flake.nix:102-106`.

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
| `imageClosureRoot` — the nixpkgs half (`flake.nix:973-976`) | 3,349,065,480 (3.12 GiB) | 571 | **96.75 %** |
| Everything yolo builds (`installPrefix`, `binPathLinks`, `nix-ld`, metadata drvs) | 112,371,944 (107 MiB) | 6 | **3.25 %** |
| `installPrefix` closure alone (`flake.nix:809-837`) | 82,781,928 (79 MiB) | — | 2.39 % |
| The four shipped Go binaries | 39,943,902 (38 MiB): `yolo` 16.0 MB, `yolo-jaild` 9.6 MB, `yolo-entrypoint` 7.2 MB, `yolo-ps` 7.1 MB | — | 1.15 % |

The last row says **four** because that is what `shippedBinaries` held when this was measured;
`02438f86` added `yolo-cglimit` and `yolo-journalctl` later the same day, so the list is six today
(`flake.nix:808`). The row is left as the dated measurement it is — the share is a rounding error
either way, which is the point it makes.

`installPrefix` stores each binary **twice** — once at `/opt/yolo-jail/bin/` and once in the
`share/yolo-jail/bin/linux-<arch>/` flake bundle (`flake.nix:822-836`) — which is why 38 MB of
binaries occupy 79 MB of closure. That duplication is deliberate (`flake.nix:768-777`: a symlink
would break exe-relative bundle resolution) and is 2 % of the image; it is not worth attacking.

### 1.3 What a rebuild actually costs

The surprise here is that **the build is cheap and the delivery is not**.

**MEASURED**, `nix build --impure --dry-run .#ociImage` on a warm store with nothing else changed:

- baseline (no `packages:`): **5 derivations to build** — `yolo-jail-customisation-layer`,
  `excludePaths`, `layers.json`, `yolo-jail-conf.json`, `stream-yolo-jail`. All metadata. Nothing
  to fetch.
- `YOLO_EXTRA_PACKAGES=["zbar"]`: **6 derivations** — the same five plus `bin-path-links` (the
  `/lib` symlink farm, `flake.nix:540-748`).
- `YOLO_EXTRA_PACKAGES=["hello"]`: **5 derivations** — `bin-path-links` did *not* appear. I did
  not chase why; `hello` contributes no `lib/`, but the store path is still interpolated into the
  farm's builder script (`flake.nix:637`) so I expected 6. Reported as observed, not explained.

**MEASURED**, evaluation cost on a warm eval cache, three runs each:

| Operation | Time |
|---|---:|
| `nix eval --impure .#installPrefix.outPath` | **0.22 s** (confirms the "~0.3s" claim at `AGENTS.md:192-193`) |
| `nix eval --impure .#ociImage.drvPath` | **1.28 s** |
| Materialize: stream the image derivation to `/dev/null` | **11.2 s** for 3,524,710,400 B — **299 MiB/s** |

**NOT MEASURED**, and I say so rather than guess:

- A cold `nix build` of the image (a Go rebuild plus the five metadata derivations). Would have
  required an actual build; the brief said to avoid one.
- `podman load -i <3.28 GiB tar>`. Would have consumed ~3 GiB in podman storage on a device already
  at 69 %.
- The disk-write half of materialization. The 11.2 s figure is stream-to-`/dev/null`; the real path
  writes 3.28 GiB through `os.Create` + `Rename` (`internal/image/autoload.go:488-530`).

Documented-but-not-independently-verified durations, for triangulation: **~12–13 s** for a
`packages:`-bearing `--impure` rebuild plus container cold start (`integration/packages_test.go:64-65`);
**~45 s** for a forced in-jail image rebuild + reload (`AGENTS.md:198`); **~2–5 min** for a first
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
**3.28 GiB** tar to `cache/images/<sha16>.tar`, and ran a full `podman load` reading that file back.

> [!NOTE]
> **Half of that sentence is now history — C3 shipped 2026-08-25.** On podman the tar is gone: the
> nix stream pipes straight into `podman load` (`internal/image/autoload.go:474-491`, the pipe
> itself in `internal/image/streamload.go`), and `cache/images` stays empty on a successful load
> — asserted on disk, not on which function ran
> (`internal/image/streamload_test.go:60`). The full load still happens; **that** is the half
> this measurement is really about, and it is what C4/C5 aim at. The file form survives on Apple
> Container alone, whose converters interpolate a path and cannot consume a stream
> (`autoload.go:492-513`, writing through `materializeImage`, `autoload.go:689` — `os.Create` at
> `:701`, `os.Rename` at `:743`).

For contrast, the same command between the *oldest* and *newest* entries in that ten-deep sentinel
— a `flake.lock` bump — reports chromium 150→151, gcc 15.2→15.3, icu4c 76→78, git 2.54→2.55, and
~60 more. **That** case genuinely needs a whole new image. It happens once per 200 commits (§1.1).

### 1.5 The multiplication factor: `packages:` and `--impure`

**Established definitively by measurement**, since the brief flagged it as the crux.

`nix build .#ociImage --impure` is run with `YOLO_EXTRA_PACKAGES` set from the config `packages:`
list (`internal/image/autoload.go:602-607`, via `config.EffectivePackages`,
`internal/cli/run/imageload.go:22`). The flake reads it through `builtins.getEnv`
(`flake.nix:166-169`, the `getEnv` itself at `:167`), which is why `--impure` exists at all
(`AGENTS.md:171-172`).

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
right staleness oracle for the integration suite (`AGENTS.md:195-197`).

**Is the resulting image shared across workspaces?** In *content*, no — it is a function of
`packages:`. In *name*, it USED to be: there was exactly one tag, `localhost/yolo-jail:latest`, and
exactly one load sentinel per runtime, `build/last-load-<runtime>` (`internal/image/autoload.go:256`).
That single tag is what OQ-3 ruled expendable, and **C2 shipped on 2026-08-25**: the ref is now
`<repo>:<sha16-of-store-path>` (`image.JailImageRef`, `internal/image/image.go:126-128`), one name
per config, so the answer is now "no" in both senses. The legacy tag survives for the two jobs with
no store path to hash (`internal/paths/paths.go:52-53`, `internal/image/image.go:95-100`); the
sentinel survives as prune's liveness ledger and as the human-readable load diagnosis (§4 C2). And
`packages:` is
**workspace-scope**: `validatePackages` (`internal/config/validate.go:204-215`, verified 2026-08-25)
imposes no user-scope restriction, unlike `packs` (`internal/config/packs.go:487-488`) or
`host_files` (`internal/config/hostfiles.go:937`).

**That workspace scope is settled, and it is not the lever.** The maintainer ruled on 2026-08-25:
`packages:` stays workspace-scope — *"yes, has to be"* (OQ-4, §10.1). The reasoning is the authority
test in [`gate-placement-principle.md`](gate-placement-principle.md) **Test 1**: `packs` and
`host_files` are user-scope-only because they grant **host access** — a pack stages skills and
briefing prose an agent then follows (`internal/config/packs.go:488`), and a source-bearing
`host_files` entry decides which host files cross the boundary (`internal/config/hostfiles.go:937`).
`packages:` grants a *tool*, and an agent inside the jail can already install tools. A scope
restriction there would be a gate placed where the authority already exists — aimed at the wrong
problem, and it would cost a repo the ability to declare its own toolchain, which is the whole point
of the key.

> [!WARNING]
> **The cost below was real and was never an argument for user-scoping.** Everything in the
> blockquote that follows was a genuine cross-workspace cost that one repo imposed on an unrelated
> one — and the ruling was to **fix the cost, never the scope**. C2 did (each config keeps its own
> loaded image); C4 would delete it at the root (the image stops being a function of `packages:` at
> all). If a future reader re-derives "just make `packages:` user-scope" from the paragraph below,
> this is the answer: it was proposed, argued, and refused.

Those three facts composed into a defect — **fixed by C2, 2026-08-25.** Stated in the past tense
because the mechanism it describes no longer exists:

> Two workspaces on one machine with different `packages:` lists **reloaded the whole image on
> every alternation, forever.** `alreadyLoaded` compared the current store path against the single
> most-recently-loaded sentinel entry; the ten-entry history existed (`AddLoadedPath`,
> `internal/image/image.go:261-284`) but was deliberately not consulted for the decision. Workspace
> A launches → path A loaded. Workspace B launches → mismatch → full reload. Back to A → mismatch →
> full reload. The tar was already cached so materialization was skipped, but the `podman load` was
> not.
> *(Those anchors are gone rather than moved: `alreadyLoaded` was DELETED. The load decision is now
> `image inspect <content ref>` — `internal/image/autoload.go:422-428` — and the comment that
> argued for equality-over-membership is preserved verbatim at `:393-407`, quoted at `:401-405`, as
> the argument FOR what replaced it. §4 C2's callout quotes it in full. The pre-C2 shape is in
> `git show 7830f65a^:internal/image/autoload.go` at `:226`/`:237`.)*

That was the multiplication factor the brief asked me to establish, and it was worse than "a package
costs a rebuild": it cost a reload *per launch, indefinitely*, to every other workspace on the
machine. It now costs one `image inspect` per launch —
`internal/image/contentref_test.go:221` asserts 2 loads for 2 configs and none on any alternation
thereafter.

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
`internal/prune/prunecmd.go:54`, `:138`), reachable only through `yolo prune --apply`
(`internal/prune/prunecmd.go:384`). Nothing calls it automatically. A 20 GiB hint fires at
`prunecmd.go:209`, `:274-296`. **The measured reality is that the hint did not cause a prune for
twenty-four days and 395 GiB.** (Anchors re-checked 2026-08-25; the numbers in the table above are
the 2026-08-15 measurement and are left as the dated evidence they are.)

Note also that a loaded image is stored **three times**: the store closure (~3.22 GiB, kept alive by
a durable GC root, `internal/image/gcroot.go:13-20`, `:38-77`), the cache tar (3.28 GiB), and
podman's own image store. `internal/prune/prunecmd.go:287-296` states the first two are separate
ledgers; podman storage was **NOT MEASURED** here (this jail has no loaded image — `podman images`
is empty).

**The ruling: this is a bug, not a configuration.** On 2026-08-25 the maintainer ruled on OQ-5, and
the ruling is stronger than this section's own leaning was. Verbatim: *"bug, for sure. I see no
reason to keep any of this around … we need to use minimal disk space … we've done some GC work,
but it's nowhere near enough."* Four things follow, and they are normative:

1. **404 GiB of cached tars is a defect in yolo, not a user's tuning mistake.** Nothing about the
   default `keep=3` is the problem; the problem is that a keep-3 retention rule which never runs is
   indistinguishable from no retention rule at all.
2. **The target is minimal disk, not bounded disk.** The leaning here was "an automatic keep-N sweep
   at materialize time". The ruling goes past it: there is no reason to keep *any* of this around.
   What survives of keep-N is an offline safety net (R4), not a retention policy.
3. **`yolo` may delete a user's cached tars without `--apply`.** That was OQ-5's sub-question and the
   answer is yes. The tar is a one-shot load artifact — the running jail depends on the store
   closure, not on the tar, and `internal/prune/prunecmd.go:287-296` already says so in the code.
4. **The shipped GC work does not close this.** [`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md)
   §1–§4 shipped 2026-07-22 and made a GC *safe* — durable per-image roots, fail-safe reaping, a
   bounded opt-in `nix store gc`. It made nothing *automatic* and lowered no retention default.
   Verified 2026-08-25: every reclaimer in `internal/prune` is still reached only from
   `internal/prune/prunecmd.go`, i.e. from a human typing `yolo prune` — no timer, hook, launch path
   or `just` recipe calls any of them.

**The fix is not designed here.** [`minimal-disk-footprint.md`](minimal-disk-footprint.md) owns it —
what gets deleted, when, by whom, and what the offline fallback becomes. This section's job is the
measurement and the verdict; that one's job is the mechanism.

> [!NOTE]
> **The table above is the 2026-08-15 measurement and stays that way.** It is the dated evidence the
> ruling was made on and re-running it in place would destroy the growth series it is half of. The
> curve has not flattened since — the fresher numbers, and the growth rate argued from them, live in
> [`minimal-disk-footprint.md`](minimal-disk-footprint.md). R7's caveat applies to both: every figure
> comes from one machine, and the *ratios* are what the ranking rests on.

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
`corePackagesFromNixpkgs` (`flake.nix:848-903`) is everything the integration suite touches plus
POSIX essentials; `fullPackages` (`:909-937`) is the bulk the suite does *not* touch — chromium,
gcc, binutils, nix, podman, tmux, bat, eza, delta, fzf. The minimal variant drops the second set
and is documented as **~1.6–2 GB smaller** (`Justfile:175-176`, `.github/workflows/ci.yml:129-131`)
— NOT independently measured here.

**(b) Our own Go build — 2.4 %, invalidated by any `goSrc` file.** `goBinaries`
(`flake.nix:122-155`) compiles every `cmd/*` in one derivation; `installPrefix` (`:809-837`) copies
six of the seven into `/opt/yolo-jail/bin/` plus the flake bundle, and symlinks `/bin/<name>` at the
**absolute store path** rather than through the `/opt/yolo-jail` mountpoint (`:779-790` — a
bind-mount over `/opt/yolo-jail` once bricked pid1). `goprobe` is deliberately excluded — it is the
one `cmd/` dir absent from `shippedBinaries` (`:808`), and `:792-798` says why an accidental
omission looks identical to it. The `goSrc` fileset trap is real and documented in the flake itself
at `:94-107`: a top-level package outside the fileset vanishes from the image while
`go build ./...` stays green. (That comment now covers `packs/` alone; `bundled_loopholes/` was the
other entry of the shape until 2026-08-19, and `:102-106` is the record of its removal.)

**(c) Generated-into-the-image content.** `mkBinPathLinks` (`flake.nix:540-748`) is one
`runCommand` producing: FHS symlinks (`/usr/bin/env`, `/bin/bash`, `/bin/sh`, `/bin/awk`, `/bin/sed`,
`/bin/grep`, `/bin/find`) (`:543-549`); the nix-ld ELF interpreter at `/lib/` and `/lib64/`
(`:581-583`); the `/lib` + `/usr/lib` symlink farm for the core trio and the chromium graphics stack
(`:600-612`, `:650-671`); the **user-package** half of that farm from `extraLibPackages`
(`:636-646`); `/etc/subuid`, `/etc/subgid`, `/etc/containers/{storage,containers,policy,registries}.conf`
(`:682-715`); `/etc/ld.so.conf` (`:742-746`); and `/etc/localtime` → `/run/localtime`,
`/etc/timezone` → `/run/timezone` (`:563-564`), `/etc/ld.so.cache` → `/run/ld.so.cache` (`:747`).
`fakeRootCommands` (`:992-1014`) adds mountpoint dirs and `/etc/passwd` + `/etc/group`.

The three `/run` symlinks are the pattern this whole doc is about, already in production:
**the image bakes a stable name and the boot path supplies the content.** `flake.nix:734-741`
explains why for `ld.so.cache` specifically — the cache is generated at container startup because
the derivation builds natively on darwin, where the Linux `ldconfig` cannot run, so a build-time
cache was silently empty on every macOS-built image.

**(d) `config.Env`** (`flake.nix:1022-1039`) — `PATH=/bin:/usr/bin`, `SSL_CERT_FILE`,
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
`internal/entrypoint`, not baked** — `flake.nix:1018-1021` says so explicitly: *"Blocked-tool shims
are generated at boot by the entrypoint into `$HOME/.yolo-shims` (config-driven) and prepended to
PATH there — there is no baked shim layer any more."* `GenerateShims`
(`internal/entrypoint/shims.go:41`), `GenerateAgentLaunchers` (`:187`) and
`GeneratePackageManagerLaunchers` (`:298`) run on **every boot**, unconditionally, from
`internal/entrypoint/boot.go:432-436`. Both dirs are bind-mount anchors backed by
`<ws>/.yolo/home/{yolo-shims,yolo-launchers}` (`internal/cli/run/assemble_parts.go:111`, `:117`)
under a `:ro` `/home/agent` (`:107`), and both are cleared contents-only — `resetAnchorDir`,
`internal/entrypoint/shims.go:26-31`, because `RemoveAll` on the anchor fails `EROFS` on the `:ro`
parent and leaves stale children in place (`:15-25`).

PATH is built by `BootPath` (`internal/entrypoint/boot.go:356-361`) and mirrored into `.bashrc`
(`internal/entrypoint/shell.go:125-134`). **A finding worth recording while we are here:** the
pre-exec re-set at `boot.go:529-532` omits `e.LocalBin()`, which `BootPath` includes — so anything
the entrypoint spawns between those two points sees a PATH the agent does not. Not caused by
anything in this doc, but any candidate that adds a dir to the delivery PATH has to add it in both
places, and today the two lists already disagree.

**A property of the boot path that C4 needs and gets for free:** those generators are wrapped in
`genStep`, so a failure is **fatal** and aborts the boot with every failure collected
(`internal/entrypoint/boot.go:576-599`, collected into one error at `:563-575` and raised at
`:551`). A delivery step added there fails loudly by construction — which is exactly the property
§7 says the *build* path lacks.

**The ordering is the whole design and it constrains §4.** `~/.yolo-launchers` is ordered *last*,
after `/bin`, specifically so a pack-declared `program fzf` cannot shadow the image's `/bin/fzf` —
"the failure is unrepresentable rather than handled" (`AGENTS.md:321-337`,
`internal/entrypoint/boot.go:343-361`, `internal/entrypoint/env.go:265-289`). Any proposal that
delivers a package via a boot-written PATH dir inherits that ordering, and therefore **cannot
shadow anything the image bakes**. A candidate that moves a package *out* of the image and into a
launch-time dir is safe on that axis; a candidate that leaves it baked *and* stages it is not — the
baked one silently wins.

### 3.2 The mounted nix store — the key lever, and its hard limit

`internal/cli/run/assemble.go:299-306` mounts, when gated:

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
path as argv (`internal/image/autoload.go:605-607`) and `materializeImage` execs it
(`:477-478`). On the nested-jail dev loop, that store path was produced by a `nix build` delegated to
the host daemon over this socket, and it is executable **only** because `/nix/store` is bind-mounted.
Same shape at `internal/image/gcroot.go:67`. Nothing about it is in the image. Whatever else is
uncertain about §4, "a store path that is not in the image can be run" is not — it happens every
time a nested jail starts.

Note also that the socket mount is **read-write** (`assemble.go:303` — no `:ro`) and, on Linux, gated
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
  anchors nested inside it (`internal/cli/run/assemble_parts.go:102-161`, the `:ro` base at `:107`).
- **Apple Container (`"container"`)** — no store mount. Its base mounts are a different shape
  entirely: **one writable `/home/agent`** over the whole workspace state dir, no `:ro` base
  (`internal/cli/run/assemble_parts.go:49-95`, the bind at `:60`). It **cannot bind-mount a single
  file** (apple/container#1089), which is why `acMaterialize` copies instead
  (`internal/cli/run/helpers.go:105`, called from `internal/cli/run/packfiles.go:97`,
  `internal/cli/run/assemble.go:260` and `:624`; documented at
  `docs/design/pack-system.md:332`, `docs/design/agent-credentials.md:100`) — and it **silently
  ignores `:ro`** (apple/container#889, `internal/cli/run/mounts.go:16-32`), which is why config
  `mounts` are skipped on it wholesale (`assemble.go:186-206`). Pack staging does not even cross as a
  mount here: `YOLO_PACK_ROOT` is set to the *host* path and AC reads it directly
  (`assemble.go:545-554`). Anything staged as a *file* has to be copied on this backend.
- **`macos-user`** — no container, no image, no bind mounts of any kind
  (`docs/design/macos-user-nix-and-features.md:24-31`, `:213-231`;
  `internal/entrypoint/darwin.go:54` — "macos-user bakes no image at all"). It already solves the
  whole problem the other way round: `packages:` is materialized as a **`buildEnv` profile whose
  `bin` is prepended to the agent's PATH** (`flake.nix:1204-1209`,
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
(`internal/cli/run/assemble_parts.go:102-161`); the nix socket + store (§3.2); scratch mounts for the
`--read-only` rootfs (`internal/cli/run/runmount.go:20-41`); `/ctx/packs` for pack staging
(`assemble.go:552-553`), `/ctx/host-*` for pack host-file grants (`internal/cli/run/packhostgrants.go`)
and `/ctx/host-user/<slug>` for user `host_files` (`internal/cli/run/hostfiles.go:68-90`); `/mise`
(`assemble_parts.go:155-160`, always a mount, never image content); git identity and gitignore,
host-composed and `:ro`-mounted (`assemble_parts.go:254-306`); and a dozen single-file binds for
logs, locks and sentinels (`assemble_parts.go:132-142`).

`flake.nix:999-1006` records that podman creates a `/ctx` mountpoint on demand even under
`--read-only` — so **a new `/ctx` consumer needs no flake edit at all.** That is the cheapest
extension point in the whole system and it is already proven by `/ctx/packs`.

Boot-time generation, all of it on **every boot** (`internal/entrypoint/boot.go:406-552`), carries:
`/run/localtime` + `/run/timezone` (`internal/entrypoint/system_boot.go:20`, called `boot.go:422`);
`/run/ld.so.cache` (`system_boot.go:58`, called `boot.go:426`); the two anchor dirs (§3.1); the CA
bundle (`internal/entrypoint/system.go:16`, `boot.go:456`); `.bashrc`
(`internal/entrypoint/shell.go:64`, `boot.go:466`); the bootstrap and venv-precreate scripts
(`shell.go:163`, `:395`; `boot.go:468`, `:470`); the mise config surface
(`internal/entrypoint/prism_mise.go:55`, `boot.go:472`); the MCP node/npx/chrome wrappers
(`internal/entrypoint/mcp_wrappers.go:7`, `boot.go:481`); every pack surface including MCP config, in
one loop with no switch on any tool name (`internal/entrypoint/packsurfaces.go:145`, `boot.go:497`);
and user `host_files` staging (`boot.go:504`). Sentinel-gating is the exception, not the rule, and
lives in the *generated scripts*: the LSP install/uninstall keyed on `~/.yolo-installed-lsps`
(`shell.go:295-383`), and the agent-CLI update stamps under `~/.cache/yolo-agent-stamps`
(`shims.go:192`).

> [!NOTE]
> **`yolo-cglimit` and `yolo-journalctl` are no longer on that list, and the direction of travel is
> the opposite of this section's.** They used to be generated in-jail; they are now baked binaries
> in `shippedBinaries` (`flake.nix:808`), and `internal/entrypoint/scripts.go:24-29` exists only to
> *unlink* the scripts an older entrypoint wrote, because `~/.local/bin` precedes `/bin` and a
> surviving script would shadow the baked binary forever (`scripts.go:11-21`, `:40-45`). A staging
> proposal should know that one class of content went the other way on purpose — see
> [`loophole-transport.md`](loophole-transport.md) §8.4.

Agent CLIs install lazily into `$NPM_CONFIG_PREFIX/bin` = `/home/agent/.npm-global`
(`shims.go:555`, `:560`), which is itself the rw `wsState/npm-global` bind
(`assemble_parts.go:108`) — so an installed agent CLI persists per workspace on the host and never
touches the image. mise tools install into `/mise` (`MISE_DATA_DIR=/mise`, `assemble.go:663`), a
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

### C2 — Address the loaded image by content, not by the `:latest` tag. **Rank 2. Mechanism RULED and SHIPPED 2026-08-25.**

**Mechanism — settled, and built.** The loaded image is named `yolo-jail:<sha16-of-store-path>`,
reusing the key `keyFor` (`internal/image/image.go:286-293`, exported as `ImageStoreKey` in `gcroot.go:27`) already
computes for the cache tar and the GC root; `image.JailImageRef` composes it
(`internal/image/image.go:126-128`). `alreadyLoaded` is GONE — it was the
single-most-recent-path comparison, and the question is now "is *this* ref present in the runtime",
asked by `image inspect` at `internal/image/autoload.go:422-424` and answered by the runtime's own
store. OQ-3 asked whether to do this or to take the cheaper variant — keep `:latest` and make
`alreadyLoaded` check membership in the ten-entry LRU. **Content-addressed tags won.**

**The image is named ON THE WAY IN, not retagged afterwards, and that distinction is load-bearing.**
The flake bakes `name = "yolo-jail"; tag = "latest"` (and `ci-minimal` on the minimal variant), so an
un-overridden stream cannot produce a content-addressed name. The first implementation therefore
loaded and then ran `podman tag :latest <content ref>` — which reads a SHARED, MUTABLE name a second
time, with nothing serializing image loads across workspaces (the run lock is per-container-name).
A concurrent load landing in that window bound this config's ref to another config's image, and
because a tag is PERMANENT the next launch found the ref present, skipped the load, and ran the
wrong image *forever*. nixpkgs' `streamLayeredImage` script takes `--repo_tag/-t` ("Override the
RepoTags from the configuration"), so the name goes into the archive instead: `StreamRepoTag`
(`internal/image/image.go:152-154`) → `streamImageArgv` (`internal/image/autoload.go:835-842`) →
`podman load`, which creates the image under exactly that name. Verified end-to-end 2026-08-25
against the live stream script and a real podman: `--repo_tag yolo-jail:<key>` yields
`Loaded image: localhost/yolo-jail:<key>`. `:latest` is then pointed at the new image DOWNSTREAM
(`pointLatestAt`, `internal/image/autoload.go:579-587`), best-effort, so the degraded fallback branch
still has something to ask about and `podman images` still reads sensibly.

> [!WARNING]
> **Do not "simplify" C2 back into an LRU-membership test on `:latest`.** The comment is PRESERVED
> in the shipped code at `internal/image/autoload.go:393-407` (the quoted lines at `:401-405`),
> deliberately kept rather than deleted. It records why equality was chosen over membership, and it
> is an argument **for** C2, not against it:
>
> > *"Comparing against the most-recently-loaded path (not mere map/set membership across the last-10
> > history) matters because nix builds are content-addressed: reverting a config change can
> > reproduce a store path that's still in the history from an earlier load, even though a
> > different, newer path has since become `:latest`."*
>
> That comment is describing the failure mode of **not knowing what `:latest` is**. Equality is the
> least-wrong answer available while one tag names every image; LRU membership reintroduces exactly
> the bug it warns about. Content addressing dissolves the question instead of answering it — when
> the ref *is* the store-path hash, "is this ref present" cannot be ambiguous, and the comment's
> whole scenario stops being representable.

**The tag is not a public surface.** The maintainer's ruling, verbatim, on whether anyone depends on
`localhost/yolo-jail:latest` by name: *"for container images? definitely not."* So C2 is free to stop
producing that name. Nothing in or out of this repo may depend on the container image tag, and any
future code that hardcodes it is a bug rather than a compatibility constraint.

> [!NOTE]
> **The cachix caveat is about a different surface — do not conflate them.** The same ruling added
> *"although we have plans on making cachix useful."* That is the **nix binary cache** — the
> substituter `yolo-jail.cachix.org` declared in `flake.nix` and now actually consulted since
> `--accept-flake-config` shipped (`internal/image/nixflags.go:35`), argued in §6 and tracked in
> [`../plans/handoff-cachix-cache.md`](../plans/handoff-cachix-cache.md). A cachix-published closure
> is addressed by **store path**, not by a podman tag; the two surfaces do not touch. "The image tag
> is not public" is **not** licence to break a cachix artifact name, a flake attr name
> (`.#ociImage`, `.#ociImageMinimal`, built by `.github/workflows/publish.yml`), or the substituter
> config. It is licence to stop using one podman tag for every image.

**What it bought.** The §1.5 cross-workspace thrash is gone: each distinct `packages:` list keeps
its own loaded image, and alternating between workspaces costs an `image inspect`, not a 3.28 GiB
load. It also removed a whole class of confusion in which `:latest` names an image built from
someone else's config.

**What it touched** (anchors re-derived 2026-08-25, after the change).
`paths.JailImage` / `JailImageShort` (`internal/paths/paths.go:52-53`) are no longer the ref a jail
runs. The assembler's `jailImageRef(rt)` helper was DELETED; the ref is now INPUT, carried on
`assembleInput.imageRef` (`internal/cli/run/assemble.go:45`) and read by
`assembleInput.jailImage()` (`:743-748`, with the `unsetImageRef` sentinel at `:737`) where the
container argv is built (`:662`). The same field feeds `insertHostServiceEnv`
(`internal/cli/run/run.go`), which finds its insert point by searching the argv for that exact
value — two readers of ONE field, because with per-config refs there is no constant two call sites
could independently arrive at. The checker asks the sharper question when it has a store path
(`internal/cli/check/check.go:510`) and falls back to a REPOSITORY probe when it does not (`:523`,
`:529` for Apple Container; section signature at `:502`). `image.JailImage` survives for the two
jobs with no store path (`internal/image/image.go:95-100`), read at `autoload.go:351` (the degraded
fallback) and `:580` (the `:latest` alias). The Apple Container conversion cluster still reads
`paths.JailImage` at `autoload.go:924` (`podman tag`); skopeo's converter is `:894-895` and the
export `:929`.

> [!NOTE]
> **The pruner WAS on that list after all, and the original note here was wrong — read this before
> touching `PruneOldImages` again.** It said a content-addressed tag "leaves it working exactly as
> it does today", reasoning that the filter is a REPOSITORY name
> (`run([]string{rt, "images", "--format", …, "yolo-jail"})`, `internal/prune/probes.go:255`) and
> the parse discards the `repo:tag` field. Both halves are true and the conclusion did not follow.
> What changed is the SHAPE of the result: `podman images` prints one row per NAME, so the newest
> image appears twice (content tag + `:latest`), and every config now keeps a permanent row of its
> own. Measured on the maintainer's host the day C2 landed — three rows, two images — `keep=2`
> selected the second workspace's live image, and removal is `rmi -f`, which destroys the containers
> using it. The pass had been a no-op for years *because* the query returned one row; C2 armed it.
> Both defects were fixed in the same change (`internal/prune/probes.go:211-254`): dedup by image
> ID, and a liveness veto sourced from the load sentinel (`ProtectedImageTags`,
> `internal/prune/imageroots_probe.go`). The RULE — the number — is still
> [`minimal-disk-footprint.md`](minimal-disk-footprint.md) OQ-DF3's, and was not touched.

Runtime image count grows — bounded by `yolo prune --keep-images` (default 2,
`internal/prune/prunecmd.go:49`, `:138`), whose retention rule still wants revisiting because
"newest 2" sorts by CreatedAt, i.e. most recently BUILT, which is the wrong axis when a revisited
workspace deliberately does not reload (R3, OQ-DF3). Layers dedup in podman's store, so the
incremental disk cost per extra tag should be the changed layer only — **NOT MEASURED**.

**Verification, as shipped.** `internal/image/contentref_test.go` drives `AutoLoadImage` against a
fake runtime whose image store is keyed by REF and maps each ref to an image IDENTITY — the fixture
shape that makes "the ref names the wrong image" a failure a test can see:
`TestAlternatingStorePathsEachStayLoaded` (`:221`) asserts 2 loads for 2 configs and none on any
alternation after; `TestTheImageIsNamedOnTheWayIn` asserts the archive carries the content name and
that nothing reads `:latest` as a tag SOURCE; `TestAConcurrentLoadCannotStealTheContentRef` seeds a
foreign image on `:latest` and asserts the content ref does not end up naming it.

**Backends: podman and Apple Container** (both have a tagged image store). Not applicable to
`macos-user`.

### C3 — Stop writing a 3.28 GiB tar on the load path. **Rank 3. Verdict RULED and SHIPPED 2026-08-25.**

**Mechanism — built.** `materializeImage` used to stream the nix image to `cache/images/<key>.tar`
and then hand `podman load -i` the file it had just written. `just load` had demonstrated the pipe
form all along (`./result | {{runtime}} load`, `Justfile:181-182`); C3 is that, in Go, with the
failure detection a shell pipeline does not give you.

The decision point is `ImageLoadStdinCmd` (`internal/image/image.go:55-60`) — `podman load` reads a
tar from stdin when given no `-i`, and Apple Container is *unrepresentable* there rather than
handled, because its CONVERTERS interpolate a path. The pipe itself is
`internal/image/streamload.go`, reached through the `StreamLoad` seam at
`internal/image/autoload.go:474-491`. `materializeImage` survives at `autoload.go:689`, reached only
from the Apple Container arm (`:492-513`), and `ImageLoadCmd` (`internal/image/image.go:28-33`)
survives for the build-failure fallback, its one remaining call at `autoload.go:368`.

The cached-tar SHORTCUT is gone from the podman branch and the code says why (`autoload.go:447-473`):
on podman nothing writes a tar any more, so a file at that name is a legacy artifact of a path that
no longer runs, and preferring an unverified file to a verified stream would let one truncated
leftover brick a workspace. It survives on Apple Container (`:504`), where `materializeImage` is
still what puts the file there.

**What it buys.** Directly deletes the largest measured artifact in §1.6 — **404 GiB as measured
2026-08-15, ~16 GiB/day over that window**; the pre-C3 model was a ~7 GiB/day floor plus ~125 GiB
spike days ([`minimal-disk-footprint.md`](minimal-disk-footprint.md) §2.2). Removes one full
3.28 GiB disk write per rebuild (60 % of commits). **The podman growth term is now zero.**

**What it had to keep working.** The cached-tar fallback (`newestTars`, the loop at
`autoload.go:359-373`, the function at `:944`) is the only thing that lets a jail start when the
build fails and no image is loaded, and C3 removed the CREATION of tars, not the ability to consume
one — pinned by `TestBuildFailureFallbackStillLoadsAnExistingTar`
(`internal/image/streamload_test.go`). The byte-progress UI needed a source of truth other than the
file it was writing: the count now happens IN TRANSIT (`copyCounting`, `streamload.go:195`), and
`progressLine` (`autoload.go:765-797`) took a prefix parameter because there are two callers saying
different things — "Caching image... " for the file form (`autoload.go:717`) and "Streaming
image... " for the pipe (`streamload.go:140`).

**The verdict OQ-5 hands C3.** This section originally hedged: *"the honest form is 'keep N tars,
stream the rest', not 'never write a tar'."* The 2026-08-25 ruling reverses the burden of proof.
The artifact class is a **bug** (§1.6), the goal is **minimal disk**, and there is *"no reason to
keep any of this around."* So:

- **The target is zero retained tars: a tar that exists after a successful load is something a
  specific fallback has to justify**, in the ruling's words rather than in a retention constant.
  The actual floor — keep-zero, keep-one, or an opt-in keep-N — is
  [`minimal-disk-footprint.md`](minimal-disk-footprint.md) **OQ-DF1**, still open as of 2026-08-25.
  Do not implement a default from this section: the ruling reverses the burden of proof, and the doc
  that owns the fix picks the number.
- **The offline safety net survives as a mechanism, not as a retention policy.** `newestTars` exists
  for one job: a jail that must start when the build failed and nothing is loaded. That job needs
  *at most one* tar — the one matching the currently loaded image — and it does not need a keep-N
  window of every image this machine ever built. Reconciling "minimal disk" with "a jail can still
  start offline" is the design [`minimal-disk-footprint.md`](minimal-disk-footprint.md) owns; see
  R4, where the two answers that used to stand side by side are resolved.
- **Apple Container keeps a file path and therefore keeps a tar.** That is not an exemption from the
  ruling; it is a constraint [`minimal-disk-footprint.md`](minimal-disk-footprint.md) has to price.
  State it precisely, though, because the sharper version of it is wrong: `convertViaSkopeo` does
  write a *second* full-size `<key>.tar.oci.tar` beside the first, in the same `cache/images` dir
  (`autoload.go:900-901`), but it removes that one the moment the load returns (`:906`;
  `convertViaDaemon` likewise at `:928-934`). The second tar is therefore **peak** disk during the
  conversion, not accrued disk — and OQ-5 is about what *stays*. What stays is the first tar, and
  this backend cannot do without it, because C3's pipe form is unavailable where a file path is
  required. (Anchors re-derived 2026-08-25, after the change.)

**Verification, as shipped.** The disk claim is asserted ON DISK rather than on which function ran,
because a stream that also wrote a tar on the side would satisfy a code-path assertion:
`TestPodmanHappyPathStreamsAndNeverWritesATar` (`internal/image/streamload_test.go:60`) fails the
`Materialize` seam outright and then asserts `cache/images` is EMPTY, under both spellings (this
store path's tar name, and the directory as a whole). The four pipe failure classes — a stream that
dies after a plausible prefix, a loader that rejects a complete archive, a loader that dies
mid-stream, a stream that cannot start — are driven through the real pipe with shell one-liners and
asserted to be told APART, not merely both detected. No container-behavior change to verify: the
loaded image is identical.

**Backends: podman only** for the pipe form; Apple Container keeps the file path.

### C4 — Deliver `packages:` from the mounted store instead of baking it. **Rank 4. Shape RULED 2026-08-25; go/no-go still gated.**

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
  (`flake.nix:1025`) and the farm links user packages' `.so` into `/lib` and `/usr/lib`
  (`flake.nix:636-646`). Those dirs are image content on a `--read-only` root. Delivery requires a
  writable dir *appended* to `LD_LIBRARY_PATH`, and `flake.nix:730-732` warns that "a consumer that
  scrubs `LD_LIBRARY_PATH` cannot be rescued" — the nix-ld fallback dir
  (`/usr/share/nix-ld/lib`, `flake.nix:592-599`) is also baked and is the *only* search path under a
  scrubbed environment.
- **`/etc/ld.so.cache`.** Already generated at boot into `/run` (`flake.nix:734-747`), so this part
  is fine — it is the existing pattern.
- **The two integration tests in §7** assert `/lib/libzbar.so.0` and `/lib/libsodium.so.*` by exact
  path (`integration/packages_test.go:87`, `:139-141`). They encode the baked-farm layout and would
  have to move to whatever dir C4 writes.
- **Backends.** Podman-on-Linux-with-a-nix-daemon only (§3.2). Apple Container and macOS-podman get
  nothing; the code would have to keep the baking path for them, which means maintaining two
  package-delivery mechanisms. `macos-user` already has C4's mechanism and does not need it. **OQ-1
  ruled that cost acceptable** — see below; it is no longer a reason not to build C4, only a reason
  the build has to be shaped a particular way.

**Verification.** The existing two `packages:` integration tests, retargeted; plus a new one that
asserts the image store path is *unchanged* by adding a `packages:` entry — which is the whole point
and is a one-line `nix eval` assertion.

**Verdict: worth designing, not worth building until C1–C3 land.** The ceiling is the highest of any
candidate; the work is smaller than it first looks because the host half is written; and the risk is
still the highest, concentrated entirely in `LD_LIBRARY_PATH` and the backend asymmetry. C2 captures
most of the *frequency* benefit for a fraction of the risk, which is why it outranks this.

**The OQ-1 ruling — read both halves or misreport it.** On 2026-08-25 the maintainer answered the
question this section's backend asymmetry raises. The ruling has two halves and they are equally
binding:

1. **The shape is settled: if C4 or C5 ship at all, they ship as an OPT-IN FAST PATH with the baked
   path retained as the fallback.** The alternative branch — accept a documented, unmitigated
   backend asymmetry and make store-delivery *the* mechanism — is **no longer live**. R1 used to
   offer both ("Or accept a documented backend asymmetry, explicitly"); it does not any more.
   Concretely: Apple Container and macOS-podman keep getting their packages baked, forever, and
   `flake.nix`'s `extraPackages` path is not deleted by C4. Two package-delivery mechanisms are the
   accepted cost, not an open question.

   > [!WARNING]
   > **"Retained as fallback" is per LAUNCH, never per package — and R2 is why.** §3.1's PATH
   > ordering puts a boot-written delivery dir *after* `/bin`, so a package that is both baked and
   > staged silently runs the **baked** copy (R2). If "keep the baked path" were read as "leave
   > `extraPackages` in the image and add a staged copy alongside", C4 would deliver nothing at all
   > and look like it worked. The only shape that satisfies both the ruling and R2: **a launch that
   > opts in builds the stock image with no `YOLO_EXTRA_PACKAGES` and gets its packages from the
   > store; a launch that does not opt in — every Apple Container and macOS-podman launch, always —
   > builds the baked image and gets them from `/bin`.** Exactly one mechanism is live in any given
   > jail. R2's "all-or-nothing" survives the ruling; what changes is that the unit is the launch,
   > not the package.
2. **The go/no-go is NOT settled.** It remains gated on the re-measurement in §11 step 5, after C2
   and C3 land. The reasoning that gate encodes is unchanged and still load-bearing: C2 mitigates
   the §1.5 thrash and C3 deletes the tar, so C4's *marginal* benefit after both is exactly the
   number nobody has yet. If the curve is flat, an opt-in second mechanism may not be worth
   maintaining even though its shape is now agreed.

> [!WARNING]
> **A ruling on shape is not approval to build.** "OQ-1 is answered" does not license starting C4.
> The measurement in §11 step 5 is the gate, and it cannot be run before C2 and C3 exist.

**What the ruling does buy immediately:** C5 inherits the shape for free. C5 reuses C4's mechanism
wholesale, so "opt-in fast path, baked fallback" is C5's shape too, and the PATH-ordering hazard
below (`/bin/fzf` vs a pack's `program fzf`) has to be resolved *inside* an opt-in path rather than
by a wholesale move out of `/bin` — which is a materially smaller blast radius than the original
framing assumed.

### C5 — Move `fullPackages` out of the run-path image. **Rank 5. Shape RULED 2026-08-25 with C4.**

**Mechanism.** Same as C4 (symlink farm from the mounted store into a boot-written PATH dir), applied
to `flake.nix:909-937` — chromium, gcc, binutils, tmux, bat, eza, delta, fzf, nix, podman, htop.
The run path then builds the minimal variant.

**What it buys.** ~1.6–2 GB off the streamed tar (`Justfile:175-176` — documented, not measured
here), multiplied by *every* rebuild, i.e. 60 % of commits — a bigger frequency base than C4's.

**What breaks.** The PATH-ordering invariant (§3.1) inverts: today `/bin/fzf` beats a pack's
`program fzf` *by position*, and `AGENTS.md:335-337` explicitly flags that as a property to
re-check before baking a name a pack also claims. Moving `fzf` out of `/bin` and into a
launch-time dir changes which wins, silently, for any pack that declares it. Chromium additionally
drags the whole `withChromium` branch of `mkBinPathLinks` (`flake.nix:565-569`, `:647-681`) —
font links and `/etc/fonts` — which is baked content, not a PATH entry.

**Verdict: the best size-per-risk ratio *after* C4 exists, because it reuses C4's mechanism
wholesale.** Building it first would mean building that mechanism for the lower-value case.
Podman-on-Linux only, same as C4 — and, per the OQ-1 ruling, **opt-in with the baked path retained**,
same as C4. That is what makes the PATH-ordering hazard above survivable: on the backends and
launches that do not opt in, `/bin/fzf` is still baked and still wins by position, so the invariant
`AGENTS.md:329-337` guards ("re-check it before baking a package whose name a pack also claims") is
not silently inverted for everyone at once.

### C6 — Layer the image so the delta is the unit of transfer. **Considered, rejected for now.**

`streamLayeredImage` with `maxLayers = 100` (`flake.nix:979-983`) already gives each popular store path
its own layer, and `podman load` skips layers it already has. Using `fromImage` to build a thin
top image over a stable base would make the *streamed tar* small too. **Rejected as subsumed by C3
for podman** (a pipe makes the tar cost zero regardless of layering) and **unproven for Apple
Container**, where the skopeo conversion (`convertViaSkopeo`, `internal/image/autoload.go:640-662`,
re-verified 2026-08-25) may not preserve the dedup. Worth revisiting if C3 turns out to be blocked.

### C7 — Skip the build when nothing moved. **Considered, rejected.**

A cheap `nix eval .#installPrefix.outPath` gate (0.22 s vs 1.28 s, MEASURED) before the full build.
**Rejected:** it saves ~1 s on a path that costs seconds-to-minutes elsewhere, and `installPrefix`
is invariant under `flake.lock` (§2), so the gate would be wrong exactly when it mattered. Test 1
of [`gate-placement-principle.md`](gate-placement-principle.md) applies by analogy: a check that
cannot see the case it is guarding against is worse than none.

### Ranking summary

| # | Candidate | Frequency | Cost avoided | Risk | Work | Backends | State, 2026-08-25 |
|---|---|---|---|---|---|---|---|
| C1 | Honest build failure | every failed build | hours of wrong-layer debugging | very low | small | all | ✅ shipped `7830f65` |
| C2 | Content-addressed image tag | every cross-workspace alternation | 3.28 GiB load | low | small–medium | podman, Apple Container | ready — mechanism ruled (OQ-3) |
| C3 | Stream to the runtime, tar stops being the default artifact | 60 % of commits | 3.28 GiB write; 404 GiB accrued | low–medium | small | podman | ready — artifact class ruled a bug (OQ-5) |
| C4 | `packages:` from the mounted store | every `packages:` user, every launch | the whole `--impure` axis | **high** | medium — the host half is `internal/darwinpkg`, already written | **podman + Linux + nix daemon only** | shape ruled (OQ-1); go/no-go gated on §11 step 5 |
| C5 | `fullPackages` from the mounted store | 60 % of commits | ~1.6–2 GB per rebuild | high | small, *after* C4 (same mechanism) | **podman + Linux + nix daemon only** | shape ruled with C4; ordered after it |

---

## 5. The central table: must bake / could move / already delivered

**MUST BAKE** — needed before any yolo code runs, or must resolve on the image's own PATH
(`PATH=/bin:/usr/bin`, `flake.nix:1023`).

| Content | Why it cannot move |
|---|---|
| `/bin/yolo-entrypoint` (`flake.nix:809-837`, the `/bin/<name>` symlink at `:835`) | The container argv is `<image-ref> yolo-entrypoint` (`internal/cli/run/assemble.go:650`, re-verified 2026-08-25) — resolved on the image's PATH by the runtime, before one line of yolo code has run. Nothing yolo does at launch can supply it. |
| `/bin/bash`, `/bin/sh`, `/usr/bin/env`, coreutils (`flake.nix:543-549`) | The entrypoint's generated scripts and the runtime's own exec path need a shell that exists in the rootfs. |
| nix-ld at `/lib/ld-*` and `/lib64/ld-*` (`flake.nix:581-583`) | It is a `PT_INTERP` — an absolute path burned into every FHS binary, not a PATH entry. A binary cannot be told to look elsewhere. |
| `/usr/share/nix-ld/lib` core trio (`flake.nix:592-612`) | The *only* library search path an FHS binary gets under a fully scrubbed environment (`flake.nix:592-599`). By construction it cannot depend on env the jail sets. |
| `/etc/passwd`, `/etc/group` (`flake.nix:1010-1013`) | Read by podman and sshd before/independently of yolo. |
| `/etc/containers/*.conf`, `/etc/subuid`, `/etc/subgid` (`flake.nix:682-715`) | Nested-podman config; consulted by podman inside the jail, and the root fs is `--read-only`. |
| `config.Env` incl. `SSL_CERT_FILE`, `TZDIR` (`flake.nix:1022-1039`) | Literal store paths in the image config. Moving `cacert`/`tzdata` means moving these too — a coupled change, not a free one. |
| `/etc/ld.so.conf`, and the `/run/*` **symlinks** (`flake.nix:742-747`, `:563-564`) | The *link* must be baked because `/etc` is read-only at runtime. The *target* is already staged — this row is the pattern, not an exception. |

**COULD MOVE** — mechanism and cost.

| Content | Mechanism | Cost / what breaks |
|---|---|---|
| `extraPackages` from `packages:` (`flake.nix:343-344`, landing in the image's `contents` at `:985-989`) | C4: host-side `buildEnv`, symlink `bin`/`lib`/`lib/pkgconfig` into boot-written dirs on the mounted store | Podman+Linux+daemon only; `LD_LIBRARY_PATH` and the `/lib` farm are baked; two integration tests assert baked paths. **Per OQ-1 (2026-08-25) this row is an *addition*, not a move: the baked path stays as the fallback.** `packages:` itself stays workspace-scope (OQ-4) — the cost is what moves, never the scope |
| `extraLibPackages` — the user half of the `/lib` farm (`flake.nix:636-646`) | Same as above; append a boot-written dir to `LD_LIBRARY_PATH` | Scrubbed-env consumers lose it (`flake.nix:730-732`); nix-ld's compiled-in fallback dir stays baked |
| `fullPackages` (`flake.nix:909-937`) | C5, same mechanism | Inverts the PATH-ordering invariant that lets `/bin/fzf` beat a pack's `program fzf` (`AGENTS.md:329-337`, `internal/entrypoint/env.go:265-289`); chromium drags baked font links. **OQ-1's opt-in shape contains that inversion** — it only applies on launches that opt in, not machine-wide |
| The `share/yolo-jail/bin/linux-<arch>/` duplicate of the binaries (`flake.nix:827-828`) | Nothing — deliberate (`flake.nix:768-777`) | 2 % of the image. Not worth it. |

**ALREADY DELIVERED** — the largest column, and the reason this design is an extension rather than
an invention.

| Content | How |
|---|---|
| Blocked-tool shims (`~/.yolo-shims`) | `GenerateShims`, every boot (`internal/entrypoint/shims.go:41`, `boot.go:432`); `flake.nix:1018-1021` records that the baked shim layer was *removed* |
| Lazy agent/package-manager launchers (`~/.yolo-launchers`) | `GenerateAgentLaunchers` / `GeneratePackageManagerLaunchers`, every boot (`shims.go:187`, `:298`; `boot.go:434-436`); ordered last on PATH (`internal/entrypoint/boot.go:343-361`, rationale at `internal/entrypoint/env.go:265-289`) |
| `/etc/ld.so.cache` | Boot-generated into `/run` (`internal/entrypoint/system_boot.go:58`, `boot.go:426`); rationale `flake.nix:734-747` |
| `/etc/localtime`, `/etc/timezone` | Boot-populated `/run` targets (`internal/entrypoint/system_boot.go:20`, `boot.go:422`) |
| CA bundle, `.bashrc`, bootstrap + venv scripts, MCP wrappers | Boot-generated (`internal/entrypoint/system.go:16`, `shell.go:64`, `:163`, `:395`, `mcp_wrappers.go:7`). **Not** `yolo-cglimit` / `yolo-journalctl` any more — those are baked binaries and the entrypoint only unlinks the scripts an older one wrote (`scripts.go:24-29`, `:40-45`) |
| The whole nix store | `-v /nix/store:/nix/store:ro` (`internal/cli/run/assemble.go:303-305`) — gated per §3.2 |
| Workspace, home + rw anchors, scratch mounts, `/ctx/*`, `/mise` | `internal/cli/run/assemble_parts.go:49-161`, `runmount.go:20-41`, `assemble.go:186-206`, `:552-553` |
| Pack content | Staged host-side by `internal/packstage`, mounted `:ro` at `/ctx/packs` (`internal/cli/run/packs.go:55-197`, `assemble.go:552-553`); on Apple Container, read directly from the host path via `YOLO_PACK_ROOT` (`assemble.go:545-550`) |
| Every pack surface, incl. MCP config | `ConfigurePackSurfaces`, one loop, every boot (`internal/entrypoint/packsurfaces.go:145`, `boot.go:497`) |
| mise tools | `mise` is baked (`flake.nix:856`); the tools install into the `/mise` mount (`assemble.go:663`, `assemble_parts.go:155-160`) |
| Agent CLIs (claude/copilot/codex/…) | Lazily npm-installed into `/home/agent/.npm-global` (`shims.go:555`, `:560`), itself the rw `wsState/npm-global` bind (`assemble_parts.go:108`) |
| LSP servers | Sentinel-tracked install *and uninstall* (`internal/entrypoint/shell.go:295-383`) |
| Git identity + global gitignore | Host-composed, `:ro`-mounted (`internal/cli/run/assemble_parts.go:254-306`) |
| User `host_files` | Staged to `/ctx/host-user/<slug>` (`internal/cli/run/hostfiles.go:68-90`), consumed at `internal/entrypoint/hostfiles.go:35` |
| `packages:` on `macos-user` | Already a store `buildEnv` PATH-prepend (`flake.nix:1204-1209`, `internal/darwinpkg/darwinpkg.go:174-193`) — C4's shape, shipped |

---

## 6. The binary-cache alternative, argued fairly

A binary cache changes the economics entirely: a "rebuild" becomes a download. If
`yolo-jail.cachix.org` served the image, §1's rebuild frequency would matter far less on a fresh
machine, and macOS users would not need a Linux builder at all — which is the reason the cache was
set up (`docs/plans/handoff-cachix-cache.md:8-16`).

**Where it genuinely helps.** First-run cost on a new machine or a CI runner. A `flake.lock` bump —
the one case where the whole 3.12 GiB nixpkgs half really does move (§1.4) — is exactly the case a
cache turns into a download, and it is why `imageClosureRoot` was factored out to be substitutable
from `cache.nixos.org` (`flake.nix:953-957`).

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
   unset, `extraPackageSpecs = []` (`flake.nix:166-169`). So **every user with a `packages:` entry
   is a guaranteed cache miss, by construction.** That makes C4 a *prerequisite* for the cache
   being useful to anyone who uses `packages:` — the two are complements, not alternatives.
3. ~~**The image build path does not opt into the flake's substituter.**~~ **⚠ RETRACTED — fixed
   2026-08-15, re-verified 2026-08-25.** As written on 2026-08-15 this said `--accept-flake-config`
   appeared in exactly two lines repo-wide, both in `internal/darwinpkg/darwinpkg.go`, and that the
   three image invocations did not pass it. **That is no longer true.** `NixFlakeFlags()`
   (`internal/image/nixflags.go:32-37`) now returns it for every invocation that *evaluates* this
   flake — the run-path image build, `yolo check`'s preflight build, and check's `--dry-run` cache
   probe — with the whole rationale at `internal/image/nixflags.go:9-20`, including the original
   symptom: nix printing *"ignoring untrusted flake configuration setting 'extra-substituters'"* and
   never consulting the cache. **The item number is kept because `internal/image/nixflags.go:17`
   cites "§6 item 3" by number** — renumbering §6 breaks a code comment. The consequence of the fix
   is live and is the substituter surface
   [`macos-user-build-step-threat-model.md`](macos-user-build-step-threat-model.md) Q2 asks about.
   Note the deliberate non-coupling recorded in the same comment: `nix store gc`, `nix path-info`
   and `nix copy` take a store path rather than a flake ref, so they do **not** get the flag.
4. **It serves at most two of three backends.** `macos-user` has no image
   (`docs/design/macos-user-nix-and-features.md:47-50`).

**Verdict: worth finishing, and cheap to finish — but it is not an alternative to §4.** The cache
attacks first-run and `flake.lock` cost; C1–C3 attack the inner loop. Item 3 was the two-character
fix and it has shipped, which leaves items 1 and 2 as the whole of the remaining work.

**This is the surface OQ-3's cachix caveat is about.** The maintainer's 2026-08-25 ruling that the
container image tag is not a public surface came with *"although we have plans on making cachix
useful"* — and "cachix" here means item 1 and item 2 above, not `localhost/yolo-jail:latest`. Item 2
is the sharper one: it is the link from "make cachix useful" straight back to C4. While the image
derivation reads `YOLO_EXTRA_PACKAGES`, the published cache can only ever hold the stock image, so
**every `packages:` user is a guaranteed miss no matter how well the cache is populated.** Finishing
cachix and building C4 are complements — the cache does not become useful to that population until
the image stops being a function of `packages:`. See §4 C2's callout for why the two surfaces must
not be conflated in the other direction.

---

## 7. The silent-fallback defect — why staging is worthless without honest failure

**This section exists because the question surfaced from a wrong-layer diagnosis.** On the morning
of 2026-08-15, two macOS integration tests — `TestExtraPackageLibFarm`
(`integration/packages_test.go:82`) and `TestDevPackageLinksRuntimeLib` (`:135`) — failed with a
**lib-farm assertion**: `libzbar.so.0 not linked into /lib //usr/lib resolving to /nix/store`
(`:101-102`) and *"the `.dev` request did not link the runtime lib into the farm"* (`:143-145`). The
actual cause was a failed image build.

> [!NOTE]
> **Every `autoload.go` anchor in the rest of this section is a PRE-`7830f65` line number, on
> purpose.** This is the code as it was on the morning it misdiagnosed, and C1 rewrote exactly that
> region. Read them against `git show 7830f65a^:internal/image/autoload.go` — verified 2026-08-25,
> all five still land on the lines described. The post-fix shape is in the header table and §10.1.

**The mechanism, exactly.** Both tests write a `packages:` config and launch a jail
(`integration/packages_test.go:84-86`, `:137-138`). The launch runs the `--impure` build. When that
build fails, `buildImageStorePath` returns `("", tail)` (`internal/image/autoload.go:353-355`), and
control reaches:

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
is fatal by default, with `YOLO_ALLOW_STALE_IMAGE=1` as the opt-in. See §10.1 OQ-2 for the ruling
and the WARNING under the ledger for where it differs from this document's leaning.

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
  [`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) own that; §1.6 only cites its
  baseline.
- **Disk lifecycle, as of the OQ-5 ruling.** This doc measured the 404 GiB (§1.6) and carries the
  maintainer's verdict that it is a **bug** — but it does not design the fix, and after 2026-08-25 it
  is not the place to. [`minimal-disk-footprint.md`](minimal-disk-footprint.md) owns what gets
  deleted, when, on whose authority, what replaces the keep-N offline fallback, and the reclaimers
  that are not image tars at all (podman's untagged image store, and the cache subdirs it
  classifies). The host `/nix/store` beyond yolo's own roots is **neither doc's** — it stays with
  [`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) §2's host-owned `min-free`/
  `max-free` residual, which `minimal-disk-footprint.md` §8 declines by name.
  Where this doc and that one disagree about a retention number, that one wins. **What
  stays here:** C3 as a *staging* change — the pipe form that stops the tar being written in the
  first place — because that is a question about how an image is delivered, not about how long an
  artifact is kept.
- **Anything requiring a measured `nix build` of the image or a measured `podman load`.** Both are
  flagged NOT MEASURED in §1.3 and neither ranking above depends on a guessed value for them.

---

## 9. Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | **C4/C5 are podman-on-Linux-only**, so shipping either means maintaining two package-delivery mechanisms indefinitely — the exact "fill the matrix" failure [`happy-path-principle.md`](happy-path-principle.md) warns about. | **Settled by OQ-1, 2026-08-25.** Do not ship C4 as *the* mechanism; ship it as an **opt-in fast path with the baked path retained as the fallback**, and only after C1–C3. The second branch this row used to offer — *"or accept a documented backend asymmetry, explicitly"* — **is no longer live**: the maintainer chose the first. Two mechanisms are the accepted, priced cost. What is still open is not the shape but the **go/no-go**, gated on §11 step 5's re-measurement. |
| R2 | **Delivering a package by PATH dir cannot shadow a baked one** (§3.1) — so a half-migration where a package is both baked and staged silently runs the baked version. | Migration must be all-or-nothing. **Sharpened 2026-08-25 by OQ-1:** the unit is the **launch**, not the package. A launch that opts into store delivery builds the stock image with no `YOLO_EXTRA_PACKAGES`; a launch that does not opt in bakes them. Exactly one mechanism is live per jail, and "keep the baked path as fallback" must never be implemented as "bake *and* stage in the same image" — that is precisely the half-state this row describes, and it fails silently. A test that asserts `which <pkg>` resolves to the staged dir catches it; see §4 C4's callout. |
| R3 | **C2 multiplies loaded images in the runtime store**, and `--keep-images 2` is the wrong retention rule for per-config tags. | **PARTLY DISCHARGED 2026-08-25, and the un-discharged half is named.** C2 did not merely multiply images — it *armed a pass that had never fired*, and shipping it without that would have been the change's worst defect. `PruneOldImages` filters by REPOSITORY and removes with `rmi -f`, which also destroys the containers using the image; while one `:latest` tag named everything the query returned one row and `keep=2` could not select anything. Under per-config tags it returns one row per NAME (so the newest image appears twice) and "everything past the newest 2" is "every config but the most recently BUILT one" — measured on the maintainer's host the day C2 landed: three rows, two images, and `yolo prune --apply` selecting the second workspace's live image. Two fixes landed with C2, both in `internal/prune/probes.go:211-254`: entries are deduped by image ID, and a liveness VETO drops any image whose content tag (or `:latest`) is in the load sentinel — `ProtectedImageTags`, `internal/prune/imageroots_probe.go`, reading the same ledger as `PruneOrphanImageRoots`' guard #2. **What is NOT discharged is the retention NUMBER**: `--keep-images` default 2 (`internal/prune/prunecmd.go:49`, `:138`) is untouched, and belongs to [`minimal-disk-footprint.md`](minimal-disk-footprint.md) OQ-DF3, still open. Podman's incremental per-tag cost is still NOT MEASURED. C2 consumed that doc's rule; it did not mint one. |
| R4 | **C3 removes the offline safety net** if taken to "never write a tar", and §7 shows build failures were already under-reported. | ~~Keep-N, not zero.~~ **Superseded by OQ-5, 2026-08-25**, and the tension is resolved rather than left standing. The ruling is that keeping tars around is a **bug**, so "keep-N" is no longer an acceptable default answer: the target is **zero tars on disk after a successful load**. What the safety net actually needs is *at most one* tar — the one matching the currently loaded image, so a jail can start when a build fails and nothing is loaded (`newestTars`, the loop at `internal/image/autoload.go:360-373`, the function at `:944`) — not a keep-N window over every image this machine has ever built. That is a **fallback mechanism**, not a retention policy, and its full design (does it survive at all once C3's pipe form exists? does it become a single pinned tar? does Apple Container, which needs a file, get a different answer?) belongs to [`minimal-disk-footprint.md`](minimal-disk-footprint.md). C1 has shipped, so the precondition this row asked for — a build failure being visible before the fallback's usefulness is reduced — is already met (`7830f65`). |
| R5 | **Scrubbed-environment breakage.** `flake.nix:730-732` records that a consumer scrubbing `LD_LIBRARY_PATH` cannot be rescued; C4 moves user libs from a baked dir to an env-dependent one, widening that class. | Keep the nix-ld fallback dir (`/usr/share/nix-ld/lib`) baked and consider extending it — it is the one search path that survives a scrub. Requires an explicit call on how large that "shadow surface" may grow (`flake.nix:596-599` says keep it to the trio). |
| R6 | **The `goSrc` fileset trap bites any new package** added under a new top-level dir, and it fails silently in the image while `go build ./...` stays green (`flake.nix:94-107`). | Not made worse by anything here, but any C4/C5 implementation that adds a Go package outside `cmd/`/`internal/` must add it to the fileset in the same commit. |
| R7 | **Every number in §1.6 comes from one machine — this jail.** Growth rates on a laptop, and podman-storage costs, may differ by an order of magnitude. | The *ratios* (3.25 % changing content, 180 KiB delta → 3.28 GiB transfer) are machine-independent and are what the ranking rests on. The absolute GiB figures are illustrative. |
| R8 | **C4/C5 make the jail structurally dependent on the host nix daemon.** The socket is mounted **read-write** with no `:ro` and, on Linux, no gate beyond path existence (`internal/cli/run/assemble.go:311`, `internal/cli/run/hostprobes.go:22-24`) — so a jail already has full nix-client access to the host store. Today that is incidental; after C4 the agent's toolchain does not exist without it. | This is a pre-existing property, not one C4 introduces — but C4 turns "convenient" into "load-bearing", which changes what a daemon outage looks like (a jail with no `packages:` tools instead of a jail that cannot `nix build`). Worth a deliberate decision rather than an inherited one. Blast-radius reasoning per [`gate-placement-principle.md`](gate-placement-principle.md) Test 2. |

---

## 10. Decisions

**Five questions were asked here. All five are now ruled** — OQ-2 on 2026-08-15, and OQ-1, OQ-3,
OQ-4 and OQ-5 by the maintainer on 2026-08-25. **Nothing in this doc is waiting on a ruling**, and
this section is no longer a queue: it is the ledger, and every ruling's reasoning lives in the body
section that governs it. **The IDs are retained exactly as they were spelled, and must never be
renumbered** — they are an API. Checked 2026-08-25: [`program-delivery.md`](program-delivery.md) §7
cites OQ-4 by ID, [`gate-placement-principle.md`](gate-placement-principle.md) cites OQ-2 by ID,
[`minimal-disk-footprint.md`](minimal-disk-footprint.md) §12 inherits all four 2026-08-25 rulings by
ID, and
[`../plans/roadmap.md`](../plans/roadmap.md)'s 2026-08-25 header callout cites all four by ID — its
💬 6 row, which used to group them, was retired the same day.

> [!IMPORTANT]
> **What remains open here is a MEASUREMENT, not a question.** §11 step 5 — re-run §1.6 and §1.4
> after C2 and C3 land — is the gate on C4/C5's go/no-go. OQ-1 settled the shape and deliberately
> left that gate standing. Nobody needs to answer anything; somebody needs to build C2 and C3 and
> then measure.

### 10.1 Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-1 | **Shape only, and conditional.** If C4/C5 ship, they ship as an **opt-in fast path with the baked path retained as fallback** — two package-delivery mechanisms, accepted deliberately. The rejected branch was "accept an undocumented-fallback backend asymmetry". **This is NOT approval to build C4:** the go/no-go stays gated on the §11 step 5 re-measurement after C2+C3 | 2026-08-25 | §4 C4, §4 C5, §9 R1, §11 step 6 |
| OQ-2 | A build that **ran and failed** is FATAL — `autoload.go` returns `false`, prints the classification and nix's own stderr. Opt-out is `YOLO_ALLOW_STALE_IMAGE=1`, not a TTY test. Shipped `7830f65` | 2026-08-15 | §7 |
| OQ-3 | **Content-addressed image tags win** (C2); the LRU-membership variant is refused. `localhost/yolo-jail:latest` is **not a public surface** — nothing may depend on the container image tag by name. Separately: the *"plans on making cachix useful"* caveat is about the **nix binary cache / substituter**, a different surface, and confers no licence over it | 2026-08-25 | §4 C2, §6 items 2–3, §9 R3 |
| OQ-4 | **`packages:` stays workspace-scope** — *"yes, has to be."* Per `gate-placement-principle.md` Test 1, the `packs`/`host_files` restriction exists because those grant host access; `packages:` grants a tool an agent could install anyway. **Fix the cost, never the scope** | 2026-08-25 | §1.5 |
| OQ-5 | **404 GiB of cached tars is a BUG, not a configuration.** Stronger than this doc's leaning of an automatic keep-N sweep: *"I see no reason to keep any of this around … we need to use minimal disk space."* The shipped GC work (`../plans/storage-lifecycle.md` §1–§4) is *"nowhere near enough."* Sub-question **answered YES**: `yolo` may delete a user's cached tars without `--apply`. **Executed in** [`minimal-disk-footprint.md`](minimal-disk-footprint.md) | 2026-08-25 | §1.6, §4 C3, §8, §9 R4 |

> [!WARNING]
> **OQ-2's ruling deliberately contradicts [`gate-placement-principle.md`](gate-placement-principle.md)'s
> "tell a human from a pipe", and that divergence was argued rather than overlooked.** What makes a
> stale run safe is not *who* is running but that somebody **SAID** the image may be stale — precisely
> the knowledge whose absence caused the bug. The asymmetry: refusing costs a rerun with one env var;
> continuing costs an investigation at the wrong layer, two layers from the cause. The full
> three-option argument lives on `internal/image/autoload.go` at the `currentPath == ""` comment.
>
> **`SkipBuild` is untouched, and its silence is deliberate.** No build was attempted, so nothing
> failed; warning there would train the reader to ignore the warning. Do not "fix" that asymmetry.

---

## 11. What to do first — dependency-ordered

Re-stated 2026-08-25, after the rulings, and re-stamped the same day after C2 and C3 landed.
**Steps 1 through 4 are done**; they stay in the list because the order is the argument and deleting
a discharged precondition makes the rest read as arbitrary. Neither 3 nor 4 could be *finished*
without a ruling elsewhere, and they resolved differently: step 4's offline-fallback floor,
[`minimal-disk-footprint.md`](minimal-disk-footprint.md) **OQ-DF1**, was **ruled the same day**
(*"stream, keep zero tars"*), so C3 shipped complete. Step 3's retention half is that doc's
**OQ-DF3**, **still open**, so C2 shipped the addressing plus the SAFETY the new tag shape forced,
and invented no number. **Step 5 is now the live one** — it was gated on 3 and 4, and is not gated
on anything else.

1. ~~**C1 — make a failed image build fail as itself**~~ (§7). **SHIPPED `7830f65`, 2026-08-15.**
   Everything else in this doc makes the pre-container phase do more work; until a failure there was
   legible, every later change would have been debugged at the wrong layer. The shipped form went
   further than this doc proposed — fatal by default, `YOLO_ALLOW_STALE_IMAGE=1` as the opt-out
   (OQ-2, §10.1).
2. ~~**Pass `--accept-flake-config` on the three image `nix` invocations**~~ (§6 item 3).
   **SHIPPED** — `NixFlakeFlags()`, `internal/image/nixflags.go:32-37`, verified 2026-08-25. It was
   the two-character change it looked like, and it is the difference between the flake's declared
   binary cache being consulted and being ignored. The substituter surface it opened is now live
   (`macos-user-build-step-threat-model.md` Q2).
3. ~~**C2 — content-addressed image ref**~~ (§4). **SHIPPED 2026-08-25.** OQ-3 settled the
   mechanism; the code deleted the cross-workspace reload thrash on both container backends without
   touching the flake. Both constraints that rode along were honoured, and one of them turned out to
   be sharper than this step predicted: R3 said the `--keep-images` retention rule *"must be
   revisited in the same change"*, and revisiting it showed that per-config tags ARM a pass which had
   never fired — one row per NAME, `rmi -f`, no liveness gate. The SAFETY half landed with C2 (dedup
   by image ID plus a liveness veto from the load sentinel); the retention NUMBER did not, because
   that rule is [`minimal-disk-footprint.md`](minimal-disk-footprint.md) OQ-DF3's to set. C2 consumed
   it; it did not mint one.
4. ~~**C3 — stream to the runtime; the tar stops being the default artifact**~~ (§4).
   **SHIPPED 2026-08-25**, and OQ-5 had raised its priority: the artifact class it deletes is a ruled
   **bug**, not a tuning opportunity. It depended on C1 (shipped) and was indeed cleaner after C2,
   which decides what a "current" image is. Its offline-fallback question was R4's and R4 handed the
   design to the sibling doc, which is exactly how it was built: no retention number was invented
   here — [`minimal-disk-footprint.md`](minimal-disk-footprint.md) **OQ-DF1** was ruled the same day
   (*"stream, keep zero tars"*) and C3 implements that ruling.
5. **Re-measure.** Repeat §1.6 and §1.4 now that 3 and 4 have landed. **This is the only thing still
   holding C4**, and OQ-1 deliberately left it standing when it ruled on C4's shape. **It is now
   possible rather than blocked**, and it is the next thing this sequence owes: if C2 + C3 flatten
   the cost curve, C4's second mechanism may not be worth its backend asymmetry even though the
   asymmetry is now a priced, accepted one.
6. **C4, then C5** — only if step 5 says so. The OQ-1 ruling is in hand and fixes the *shape*
   (opt-in fast path, baked path retained as fallback); it is not a go-ahead. C5 reuses C4's
   mechanism, so their order is fixed.

**What is not in this list, and deliberately.** The disk work OQ-5 licenses is bigger than C3 and
does not belong in an image-staging sequence: podman's untagged image store, the shared cache
subdirs, and whether any reclaimer ever runs without a human typing `yolo prune`
are all [`minimal-disk-footprint.md`](minimal-disk-footprint.md)'s sequencing to own; the host
`/nix/store` beyond yolo's roots stays with
[`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) §2, which
[`minimal-disk-footprint.md`](minimal-disk-footprint.md) §8 declines by name. C3 is the one
piece that sits here because it is a change to how an image is *delivered*.
