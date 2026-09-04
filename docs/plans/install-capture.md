# Plan: capture-and-repackage for the installer class

**Design:** [`program-delivery.md` §6.3](../design/program-delivery.md#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package)
(ruled [OQ-PD10](../design/program-delivery.md#decision-ledger), resequenced FIRST by
[OQ-PD15](../design/program-delivery.md#decision-ledger)) · **Status:** ready ·
Written against `839d0745`, 2026-09-03.

**Precedence:** the design wins on behavior; the tree wins on fact; this file is advice and is
the first thing to be wrong. Never twist code to match it — correct it in the commit.

**Sequencing:** this is §10 step six and lands **before** step seven,
[`evergreen-agent-updates.md`](evergreen-agent-updates.md) — evergreen multiplies the per-workspace
disk cost capture removes. Re-measured here
2026-09-03: `~/.local/share/claude/versions` holds **five** builds at **1.2 GB** (the design measured
four at 1019 MB the same morning), and `~/.local` is a per-workspace bind.

## Map

| Path | Change |
| :--- | :--- |
| `internal/treedigest/` | **new** — `treeDigest`/`treeDigestSkipping` lifted out of `internal/hostskills/compose.go:958,964` verbatim |
| `internal/hostskills/compose.go` | call the new package; delete the local copies |
| `internal/capture/` | **new** — `store.go` (layout, admit, resolve, completion marker), `manifest.go` (delta manifest + capture receipt), `materialize.go` (hardlink, EXDEV → copy), `inner.go` (the backend-neutral driver), `gc.go` (`PruneUnreferencedCaptures(root, keep, olderThan, apply, now)`) |
| `internal/paths/paths.go` | **new** `CapturesDir()` + `CapturesDirUnder(home)`, beside `PacksDir` (`:423`); **new** `HomeSurfaces()` — the capture/dedupe surface pair list, per slice 2's correction (a); **new** `GlobalStorageRel()` and `WorkspaceStateDir`/`WorkspaceHomeState`, per slice 3's corrections (a) and (b) |
| `internal/prune/prune.go` | derive `dedupeSubtrees` from `paths.HomeSurfaces()` rather than re-typing it |
| `internal/storage/ensure.go` | add `CapturesDir()` to the boot `MkdirAll` list (`:44`) |
| `internal/cli/capture.go` | **new** — `yolo internal capture-run`, the inner half (slice 2) |
| `internal/cli/capturehost.go` | **new** — `yolo capture <bin>`, the host act (slice 3). Split from the file above: one is a hidden in-jail driver entry, the other launches jails |
| `internal/cli/hostapplylock.go` | `tryFlockAt` factored out of `tryHostApplyLock` and shared with the per-program capture lock |
| `internal/cli/subhelp.go` | one `subcommandUsage` row — every registry key must answer `--help` |
| `internal/cli/dispatch.go` | register `capture` (`:15-35`) |
| `internal/cli/help.go` | one `commandHelp` row (`:10-31`) |
| `internal/cli/internal.go` | add `capture-run` to the hidden namespace switch (`:28`, `:32`) |
| `internal/entrypoint/shims.go` | ~~factor the `nativeLauncherTemplate` install body into a shared const~~ — **not needed, see slice 3(d)**: the capture RUNS the launcher. What landed instead is `InstallOnlyEnv` and its branch in the template. Slice 4 still adds the materialize-from-CAS branch |
| `internal/entrypoint/capturereceipt.go` | **new** (slice 3) — the `kind:"capture"` writer, the only receipt in this repo written from Go |
| `internal/entrypoint/receiptread.go` | extend the reader for `kind:"capture"`, `act:"record"`/`act:"materialize"`, and the new `platform` field |
| `internal/prune/prunecmd.go`, `pathsref.go` | new `Options.CapturesDir func() string` seam + one report section |
| `internal/cli/commands.go` | `pruneUsage` (`:117`) + the flag parse in `runPrune` (`:149`) |
| `internal/macosuser/seatbeltcapture.go` | **new** (slice 6) — `SeatbeltCaptureProfile(stagingRoot)`. ⚠ *Its own file, not `seatbelt.go`, and it takes the staging ROOT: see slice 6(a)* |
| `internal/macosuser/capture.go` | **new** (slice 6) — the capture plan, its invariants, and the executor over the existing `Deps` seams |
| `internal/macosuser/runplan.go`, `macosuser.go` | slice 6 lifted `buildBootstrapEnv` (home is now a parameter) and `sandboxEnvPairs` out; no behavior change |
| `internal/capture/relocate.go` | **new** (slice 6) — the file-content reference scan and the relocatable verdict |
| `docs/design/storage-and-config.md` | §2's `<gs>` table (line 112) — already missing 9 dirs; add `captures/` |
| `docs/design/program-delivery.md` | §10 step six → SHIPPED, per slice |

## Reuse

- **`packsrc.Store` is the CAS this copies** (`internal/packsrc/store.go:11-18,249-289`): two-stage
  `mirrors/`+`trees/<key>/`, **completion marker written LAST** (`.yolo-pack-complete`, `:260`) so a
  torn write is detectable, and `Resolve` is strictly offline with an error naming the fetch command.
  Same three properties, same shape.
- **`hostskills.treeDigestSkipping`** (`compose.go:964`) already emits the exact canonical form a
  capture needs: `d <rel>`, `l <rel> <target>` (readlink, never followed), `f <rel> <exec-octal>` +
  content, sorted. Lift it, don't rewrite it — hostskills' existing tests are the refactor gate.
- **Key convention:** `hex(sha256(x))[:16]`, per `image.ImageStoreKey` (`internal/image/gcroot.go:27`).
- **Receipt schema:** extend, never fork. Writer head `entrypoint.receiptPrefix` (`shims.go:471`),
  shell tail `receiptShellFns` (`:495-593`), reader `receiptread.go:42-75`. Round-trip is pinned by
  `TestReceiptRoundTripThroughAGeneratedLauncher` (`programreconcile_test.go:659`).
- **`prune` house shape:** every sweeper takes `apply bool` last, computes identical numbers on both
  paths, and `verb(apply, "would remove", "removed")` (`prunecmd.go:712`). Closest siblings:
  `PruneOrphanImageRoots` (`imageroots.go:38`) and `PruneHostArchive` (`hostarchive.go:80`).
- **`prune.inode()`** (`internal/prune/inode.go:12`) — the lstat behind the `st_nlink` GC oracle.
- **`dedupeSubtrees = []string{"npm-global","local","go"}`** (was `internal/prune/prune.go:24`) is
  already the exact capture surface set. Import that spelling rather than a fourth one.
  ⚠ *Corrected while building slice 2 — see build-order 2(a).* Those are the HOST spellings and the
  jail-side names differ; the constant was also unexported, and slice 5 makes `prune` import
  `capture`, so importing it the other way would have been a cycle. The pair list now lives in
  `paths.HomeSurfaces()` (`internal/paths/paths.go:383`) and `prune.dedupeSubtrees`
  (`prune.go:31`) derives from it; import THAT.
- **Locking:** `tryHostApplyLock` (`internal/cli/hostapplylock.go:80`) — non-blocking, never refuses,
  into `<gs>/locks/`. Two concurrent captures of one bin must not race the admit.

## Traps

- **A hardlinked CAS file is the running program's bytes.** An installer or self-updater that opens a
  materialized file **for write** corrupts the CAS and every other workspace at once, silently.
  **Constraint:** chmod CAS entry files to drop `w` at admit time, and never dedupe *within* the CAS.
  claude's updater writes new version dirs (new inodes) and is safe; this is not a general guarantee.
- **The scratch root must live inside the CAS dir** (`<CapturesDir>/staging/<id>`), not `/tmp`.
  Admission is `os.Rename`; a different filesystem turns it into a 1.2 GB copy, which is the cost
  this whole subsystem exists to delete. ⚠ *And "filesystem" is too weak — see build-order 2(b):
  `rename(2)` compares the MOUNT, so two bind mounts of one btrfs with identical `st_dev` still
  fail `EXDEV` (MEASURED 2026-09-04). Under podman every capture surface is its own bind.*
  ⚠ *Resolved in slice 3(a): the capture WORKSPACE is the scratch root, and the driver reaches the
  surfaces through the workspace bind — the one view that shares a mount with it.*
- **`FindYoloWorkspaces` (`internal/prune/probes.go:148`) is NOT a reference oracle.** It enumerates
  `podman ps -a`, so a workspace whose container was removed is invisible — GC keyed on it would
  delete bytes a live workspace still uses. Use `st_nlink` instead (build order 5).
- **`receiptsFile` is baked at generation time, never read from env** (`shims.go:416-446`) —
  `YOLO_WORKSPACE` is absent in a live container and macos-user execs launchers under `env -i`. The
  capture driver must bake its output path the same way.
- **Three names for one mechanism:** manifest `via:"installer"` → `packdecl.Install.Kind == "native"`
  → receipt `kind:"installer"`.
- **Apple Container has no per-dir binds** — `appleContainerBaseMounts` puts the whole `wsState` at
  `/home/agent` in one rw bind (`assemble_parts.go:60`). The delta surface there is the whole home, so
  the inner driver's baseline walk is what makes AC work at all; do not special-case the bind list.
- **The boot writes into the capture surfaces before the installer does** (bootstrap npm packages,
  LSP servers, `yolo-bin`). A host-side before/after diff is therefore impossible from outside — the
  baseline walk has to happen inside, after boot, before install.
- **A fetched pack's `installerUrl` is refused** (`packload.HonoredInstalls`, `packload.go:491-502`).
  `yolo capture` must call `HonoredInstalls`, not read the manifest — or it executes what the origin
  gate exists to refuse.
- The design's pipeline line says *"delta → tar+hash"*. **There is no tar code in this repo**
  (`archive/tar` is imported nowhere; the one tar op is a shell-out at `image/autoload.go:994`) and a
  tar cannot be hardlinked from. Store the entry **unpacked** and hash the canonical manifest — the
  design's own *materialize* verb reads "unpack/**hardlink**", so this is inside its option set, but
  the implementer will trip over the wording.

## Build order

1. **`internal/treedigest` + `paths.CapturesDir()` + the store skeleton. — LANDED 2026-09-04.** Lift
   the digest, add the path pair and the `ensure.go` mkdir, write `admit`/`resolve`/completion-marker
   with the torn-write redo. No CLI. → `go test ./internal/treedigest ./internal/hostskills ./internal/capture ./internal/paths`
   *Two corrections from building it.* `PacksDir` is a bare `func PacksDir() string`, **not** a
   `Dir()`/`DirUnder(home)` pair — the pair shape copied is `GeneratedBinDir`/`GeneratedBinDirUnder`
   (`paths.go:357-363`), sited beside `PacksDir` as the map says. And the entry is
   `entries/<key>/tree/` with the marker in the entry ROOT beside it, one level deeper than
   packsrc's `trees/<sha>/`: metadata (slice 2's manifest) then has a home that is not inside the
   tree materialize hardlinks wholesale.
2. **The inner driver, `yolo internal capture-run`. — LANDED 2026-09-04.** Baseline walk of the
   three capture surfaces → run the installer → delta manifest → move the delta paths into the out
   dir. Backend-neutral: it is a process with a `HOME`, nothing more. Drive it in tests with a
   fixture "installer" shell script that writes files and one absolute symlink. → `just test-fast`

   *Five corrections from building it.*

   **(a) `dedupeSubtrees` is not home-relative, and it could not have been imported anyway.** Those
   three strings are the HOST spelling, `<ws>/.yolo/home/<sub>`; the jail-side names differ and the
   mapping is not derivable (`npm-global` → `~/.npm-global`, but `go` → `~/go`). The jail spelling
   existed only as literals in the podman argv (`assemble_parts.go:108-110`), so there were already
   two lists. `dedupeSubtrees` is also unexported, and slice 5 puts `PruneUnreferencedCaptures` in
   `internal/capture` for `prunecmd.go` to call — so `capture` importing `prune` would have been an
   import cycle one slice later. **Resolution:** the pair moved to `paths.HomeSurfaces()`
   (`[]HomeSurface{{Subtree, HomeRel}}`, a leaf package both can import), `prune.dedupeSubtrees`
   derives from it, and a test in `internal/cli/run` asserts the podman argv binds each
   `Subtree` at its `HomeRel` — the only place in the tree where the two halves of the pair meet.

   **(b) "same filesystem, so rename" is the wrong predicate: it is the same MOUNT.** MEASURED
   2026-09-04 in this jail — `/tmp` → `/workspace`, both btrfs, identical `st_dev` (61) —
   `rename(2)` returns `EXDEV`, because the kernel compares `mnt` and not the device. Under podman
   every capture surface is its OWN bind mount, so a scratch dir outside them hits this by default
   and `st_dev` cannot be used to pre-check it. The driver therefore falls back to a copy, counts
   it (`Result.Copied`) and says so loudly on stderr; a capture is still correct, it merely costs
   the bytes twice. **Slice 3 should site the scratch dir on the same mount as the surfaces** — free
   on Apple Container, whose single `/home/agent` bind covers all three. `Store.Admit`'s rename is
   unaffected: staging and entries are both under `<CapturesDir>`.

   **(c) The out dir is ENTRY-SHAPED, not a bare tree.** The driver fills `<out>/tree/` and writes
   `<out>/capture-manifest.json`, which is exactly slice 1's `entries/<key>/` layout — so slice 3 is
   `Admit(capture.TreeDir(out))` plus moving one small file up beside the admitted tree, with
   nothing in between that could file a manifest against a tree it does not describe.

   **(d) Two move rules the plan's one-liner does not imply.** A directory absent from the baseline
   is moved WHOLE (one rename for a whole version dir — everything under a new dir is new by
   construction), but a SURFACE ROOT never is, even when new: on the container backends those are
   bind mountpoints and renaming one is `EBUSY`.

   **(e) Two things deliberately NOT built here, so slice 3/6 do not assume them.** The baseline
   records `(kind, perm, size, mtime)` per path, never content hashes — an installer that rewrote a
   file to the same size and mode within the mtime granularity would be invisible, which is the
   price of not reading a booted home's worth of bytes. And §6.3's *"a jail that writes outside its
   binds is a finding the capture run reports"* is unbuilt: stray writes are left alone and not
   enumerated (that needs a whole-home walk). Absolute references are gathered from SYMLINK TARGETS
   only; file-content references are slice 6's if it needs them.

   *One thing slice 3 must watch:* the jail's own `~/.local/share/yolo-jail` is INSIDE the `.local`
   surface, so anything yolo writes there between the baseline and the installer lands in the delta.
3. **`yolo capture <bin>` on the container backends. — LANDED 2026-09-04.** Host act:
   `HonoredInstalls` → scratch workspace under `<CapturesDir>/staging/<id>` → the ordinary run
   pipeline with the inner driver as the command → hash → rename into `<CapturesDir>/entries/<key>`
   → write the capture receipt. First user-visible benefit, and a **small** one: reconcile stops
   guessing (`reconcile.go:218` compares one file's digest today; a manifest replaces it). →
   `integration/capture_test.go` + a nested jail

   *Eight corrections from building it.*

   **(a) THE SCRATCH DIR IS THE WORKSPACE, and the driver reaches the surfaces through the
   WORKSPACE BIND.** This is 2(b)'s open question, answered by measurement. Under podman a
   capture surface has TWO container paths for one directory — `$HOME/.local` (its own bind) and
   `/workspace/.yolo/home/local` (inside the workspace bind) — and `rename(2)` compares the mount,
   so only the second shares one with a scratch dir under `/workspace`. MEASURED in a nested jail
   2026-09-04, same `st_dev` (61) and same `st_ino` for both paths:

   | driver reaches the surfaces via | renamed | copied | tree inode == source inode |
   | :--- | ---: | ---: | :--- |
   | `--surface-root=/workspace/.yolo/home` | 1 | 0 | yes |
   | `$HOME` (no surface root) | 0 | 1 | no |

   So `capture.Options.SurfaceRoot` is a SECOND PATH to the same directories, used for the walk
   and the move while every recorded path stays home-relative and `Manifest.Home` stays the real
   `/home/agent`. The pair it consumes is `paths.HomeSurface`'s: `<SurfaceRoot>/<Subtree>` is
   walked, `<HomeRel>/…` is reported — the second place in the tree where both halves are used at
   once. The `.yolo/home` spelling moved to `paths.WorkspaceHomeState` and `prepare.go:302` now
   calls it, so the bind's two ends cannot drift. Siting the workspace under
   `<CapturesDir>/staging/<id>` stays forced, by the OTHER rename: `Store.Admit` refuses a staged
   tree from anywhere else.

   **(b) The exclusion of yolo's own state needed a guard on the WHOLE-DIRECTORY MOVE, not only on
   the paths a descent reaches.** `capture.DefaultExcludes()` is `[paths.GlobalStorageRel()]` —
   `.local/share/yolo-jail` — recorded in the manifest's new `excluded` field. But a directory the
   baseline never saw is moved in ONE rename (2(d)), and on a booted home `.local/share` is exactly
   such a directory: moved whole, it takes the state dir with it and the per-path check never runs.
   `driver.containsExcluded` is the guard. Found by the test, not by reading.

   **(c) `Store.AdmitEntry`, because "admit the tree then move the manifest up" leaves a window.**
   2(c) said slice 3 is `Admit(TreeDir(out))` plus moving one small file; that ordering has the
   completion marker land BEFORE the manifest, so the entry reads complete while its manifest is
   missing. Harmless today (nothing on the materialize path reads it) and exactly what the
   two-stage discipline exists to not rely on. `AdmitEntry` renames the whole proto-entry in one
   go, key still computed from `tree/` alone, marker still last. `Admit` is unchanged and both
   share one body.

   **(d) The installer a capture runs is THE LAUNCHER, so the map's "factor the
   `nativeLauncherTemplate` install body into a shared const" was not needed — and would have been
   wrong.** The capture jail runs `env YOLO_INSTALL_ONLY=1 <bin>`, which resolves to the generated
   native launcher (`~/.yolo/bin/launch` is last on PATH and a fresh capture home has nothing else
   by that name) and takes its `_do_install` path verbatim. A second implementation of
   download-then-run would capture bytes a launch would never have produced, which is the one
   property slice 4 depends on. The new `entrypoint.InstallOnlyEnv` (native launchers only) is what
   stops the launcher exec'ing the tool afterwards — without it a capture would record the tool's
   FIRST-RUN state, which is §6.3's "personalizes at install time" hazard, created on purpose.

   **(e) The receipt needed ONE new field, not two, and it goes BESIDE THE ENTRY.** "Ships with"
   says the receipt gains `kind:"capture"`, `act:"materialize"`, and §6.3's two new tuple members
   (`file manifest`, `platform`). Only `platform` is new. The tuple maps onto existing fields:
   declaration → `bin`, installer URL → `declared`, capture hash → `resolved` (the store key),
   **file manifest → `sha256`** (the sha256 OF the canonical manifest, of which the key is the
   first 16 chars, so a reader can check one against the other), entry root → `path`, time →
   `time`. The manifest FILE is `path`'s sibling; a copy of it inside the line would be the
   parallel ledger §6 warns about. Slice 3's act is **`record`** — §6.3's own verb, the one paired
   with slice 4's `materialize`. And the file is `entries/<key>/receipts.jsonl`, per §6.3's *"a
   machine-local receipt beside the CAS entry"*: the capture workspace is thrown away and the
   invoking workspace merely happened to be where a human stood.

   **(f) This is the first receipt written from GO.** Every other one is appended by generated
   shell. `entrypoint.CaptureReceipt.Line()` builds on the same `receiptPrefix` head and mirrors
   `_yolo_receipt`'s field order; it diverges twice, deliberately — it escapes with
   `jsonStringLiteral` instead of `_yolo_scrub` (Go can escape; shell cannot), and its append CAN
   fail its caller, because here the receipt is half the deliverable rather than a note on the way
   past.

   **(g) `platform` is the JAIL's, and only the driver can observe it.** A capture made from a Mac
   through podman is a `linux/<arch>` capture; a host-side `runtime.GOOS` would answer
   `darwin/arm64` — the one wrong answer that looks right. So `Manifest.Platform` is set inside and
   the host act copies it into the receipt.

   **(h) Two limits worth knowing.** `resolveCaptureTarget` uses `packForCheckDeps`, the host-side
   resolver `internal/cli` already had, which resolves EMBEDDED and LOCAL packs but not a FETCHED
   one out of the packsrc store — so `yolo capture <bin>` for a git-sourced pack reports the pack
   as "not resolvable offline (run `yolo pack install`)" and names it, rather than resolving it. No
   shipped pack is fetched. And `dispatchNative` hands every handler the whole argv, so `runCapture`
   drops `args[0]` the way `runPack` does; the unit tests called the body directly and were all
   green with the token left in, which is the pinned-callee shape AGENTS.md names — there is now a
   test for the dispatch entry itself.
4. **Materialize, from the launcher.** `nativeLauncherTemplate`'s `_do_install` gains a branch: if a
   capture for this bin+platform resolves, hardlink it in (EXDEV → copy) and write an
   `act:"materialize"` receipt; otherwise fall through to today's download. **This is the slice that
   pays** — the second workspace stops downloading, and 1.2 GB stops being per-workspace.
   *Advice: materialize from the launcher, not a boot genStep* — §5.2 names "you pay nothing for a
   tool you never invoke" as a virtue any replacement must keep, and OQ-PD12a's design has no boot
   step at all. → an `integration/` test over two workspaces asserting one download

   ⚠ **Two things slice 3 found that this line does not account for. Read them before starting.**

   **`link(2)` compares the MOUNT too, so an IN-JAIL hardlink materialize may be structurally
   impossible.** MEASURED in this jail 2026-09-04: a hardlink from the workspace bind to the
   `/home/agent/.local` bind — one filesystem, one inode, two mounts — fails `EXDEV`, while a link
   within one bind succeeds. The store lives on the HOST at `<CapturesDir>` and is not mounted into
   a jail at all today; mounting it anywhere would make it one more mount, so an in-jail launcher
   could COPY an entry but not link one, which deletes the whole point. On the host both paths are
   ordinary directories on one filesystem and `link(2)` works. Slice 3's own answer — reach the two
   halves through the ONE bind that contains both — has no analogue here, because no host directory
   contains both `<CapturesDir>` and an arbitrary `<ws>/.yolo/home`. So the advice above is probably
   inverted and materialize wants to be host-side (the run pipeline, per workspace, before launch),
   which trades §5.2's "pay nothing for a tool you never invoke" for the property the subsystem
   exists to buy. **That is a behaviour question, not an implementation detail: stop and ask.**

   **There is no bin+platform → key INDEX.** The store is content-addressed, so nothing can compute
   an entry's key from a program name; slice 3 records `(bin, platform, key)` in each entry's
   `receipts.jsonl`, which answers the question only by scanning every entry. Whatever resolves a
   capture for materialize needs a real index, and it is slice 4's to design — `packsrc`'s lockfile
   beside the user config is the closest sibling shape.
5. **Remove + GC.** `st_nlink == 1` on the entry's files **is** the unreferenced oracle: a materialized
   hardlink keeps the count above 1 from a workspace `yolo` cannot enumerate, so it is fail-safe by
   construction, and it closes OQ-PD4's back door by construction too — dropping a pack leaves the
   materialized tree, so the entry stays referenced and GC reclaims nothing. Compose it with the two
   sibling idioms: an age floor for the in-flight window, keep-newest-N per (bin, platform).
   A cross-device copy does not bump nlink, so that workspace can see its entry reaped — which strands
   nothing (it has its own bytes) and only forces a re-capture. → `go test ./internal/prune ./internal/capture`, then `yolo prune` dry-run
6. **`macos-user`. — LANDED 2026-09-04, RECORDING HALF ONLY.** `SeatbeltCaptureProfile`:
   `deny file-write* /` then allow only the staging dir + `/tmp` + `/var/folders` — the shared
   `/Users/_yolojail` home denied for the duration. Same inner driver, `HOME=<staging>`. Adds
   **relocation**: the manifest records every absolute reference to the staging prefix (symlink
   targets first — `~/.local/bin/claude` is one, confirmed by `ls -l` in this jail) and materialize
   rewrites them, or the entry is admitted `relocatable:false` and refuses to materialize elsewhere.
   → `go test ./internal/macosuser ./internal/capture ./internal/cli` (the plan said the first two
   "only"; the `--scan-content-refs` flag parse lives in the third, and a test that skipped it would
   pin the callee with the call site unpinned)

   *Six corrections and two hand-offs from building it.*

   **(a) The profile takes the staging ROOT, not the staging home.** The capture writes two things
   — the home the installer runs against and the entry-shaped out dir the delta is moved into — and
   the profile allows exactly one subtree. Making that subtree the ROOT (`<root>/home`,
   `<root>/out`) covers both, keeps the out dir OUT of the home being captured, and makes "everything
   this capture touches is under one allow" a checkable invariant rather than an intention. It also
   lives in its own `seatbeltcapture.go`: the two profiles share `sbplStr` and `ancestorLiterals` and
   nothing else, and the file header is where the MEASURED/NOT-MEASURED split has to be stated.

   **(b) THE STAGING TREE CANNOT BE `<CapturesDir>/staging`, which the whole plan assumed.** Slice
   3's trick — the store's staging dir IS the capture workspace, so admission is a rename — is
   unavailable here twice over. `paths.CapturesDir()` is under the INVOKING user's home, and this
   backend exists to keep the sandbox uid out of that home (the same reason `StagePackCommands`
   copies packs to `/var` instead of pointing the sandbox at `~/.local/share/yolo-jail`); and a
   writable subtree there would put the machine-wide CAS — every other program's captured bytes —
   in reach of a vendor installer yolo is running for the first time. **Resolution:** a capture
   stages on NEUTRAL GROUND at `macosuser.CaptureRootDefault()` = `/Users/Shared/yolo-captures`,
   provisioned with the same `WorkspaceACLAces` a workspace gets, and the HOST moves the finished
   proto-entry into `<CapturesDir>/staging/<bin>/out` afterwards. That move **refuses on `EXDEV`
   rather than copying** — the stance `Store.Admit` already takes about the same rename, and the
   only honest one for a multi-gigabyte tree.
   *READ FROM CODE, not measured:* on a stock Mac `/Users/Shared` and `/Users/<admin>` are both on
   the one APFS Data volume, so the rename should succeed. Nothing here has run a Mac.

   **(c) The staging home needs a bootstrap of its own, so `buildBootstrapEnv` had to take the home
   as a parameter.** Slice 3(d) established that the capture runs THE GENERATED LAUNCHER, not a
   second implementation of download-then-run. On this backend that launcher exists only in a home
   `yolo internal darwin-bootstrap` has rendered into, and `BuildRunPlan` hard-coded `SandboxHome()`
   in three places. Pointed at the shared home, a capture would provision the machine's real agent
   home and then capture a delta of it. The extraction is a pure refactor (`48031ef7`); the
   invariant that fails if it is undone is `TestCapturePlanRefusesTheSharedHome`.

   **(d) `relocatable` needed a second field to be honest, and the contract belongs on the FIELD.**
   "The entry is admitted `relocatable:false`" is one bit, and one bit cannot distinguish *"we
   looked and found nothing unrewritable"* from *"we never looked."* Both produce an empty
   `absoluteRefs`, and only the first licenses moving the tree. So the manifest gains `refScan`
   (`symlink-targets` | `symlink-targets+file-content`), `relocatable`, and `notRelocatable` (the
   reasons). **The contract, which slice 4 or whoever builds materialize must implement:**

   > A materialize whose destination home EQUALS `Manifest.Home` ignores `relocatable` entirely. A
   > materialize into any OTHER home must REFUSE when it is false — printing `notRelocatable` —
   > and must rewrite every `absoluteRefs` entry from `Manifest.Home` to the destination when it is
   > true. Nothing else may read the field.

   That is what makes `relocatable:false` free on the container backends: their destination is
   always the `/home/agent` they were captured in. It is also why a manifest written before these
   fields existed (slice 3's) reads back false and is still correct.

   **(e) The content scan is OFF by default and the driver takes a flag for it.** Slice 2(e)
   deliberately left file-content references to this slice. They are needed here — claude's shim
   embeds its own path — and they cost a read pass over the whole delta, 1.2 GB for claude. So
   `capture.Options.ScanContentRefs` / `yolo internal capture-run --scan-content-refs`, passed by
   the macos-user driver argv and by nothing else. A reference in a file that is not text (git's
   NUL-in-the-first-8000-bytes heuristic) is RECORDED and makes the entry non-relocatable: a prefix
   substitution in a Mach-O binary is not a string edit, and Homebrew's answer on this same OS was
   to pad the prefix rather than rewrite it in place. Length checking against a specific
   destination is the REWRITE's job, not the record's — the manifest must stay target-independent
   or a content-addressed entry would carry a claim about one machine's home.

   **(f) Two design-doc citations about this backend were wrong** and are fixed in
   [`program-delivery.md`](../design/program-delivery.md) §6.3. `internal/cli/run/run.go:156-159`,
   cited for the machine-constant shared home, is the profile/provider channel composition and says
   nothing about a home; the refusal the next clause cites (`:235-250`) was the right range all
   along. And `macosuser.go:328-377` for `LaunchArgv` was correct until this slice's refactor
   shortened it, so it is now named rather than numbered. (This is the third wrong citation about
   this one paragraph — `ff4730d8` fixed `:188-203` the same week.)

   ### Hand-offs — what is NOT wired, and the exact line that wires it

   **H1. `yolo capture` still refuses on macos-user.** `runCaptureJail`'s `opts.MacosUserRun`
   closure (`internal/cli/capturehost.go`) prints slice 3's "cannot capture yet" message.
   `internal/cli/capturehost.go` was being edited by the concurrent materialize slice while this
   one landed, so the closure was left alone rather than risking a shared-worktree collision.
   Replacing it is the whole wiring:

   ```go
   opts.MacosUserRun = func(cfg *jsonx.OrderedMap, _ string, _, _ []string,
       _, packRoot string, dryRun bool, packEnv *jsonx.OrderedMap) int {
       deps := macosuser.RealDeps(nil, nil, color)
       deps.Out = out
       return macosuser.RunCaptureAct(deps, macosuser.CaptureOptions{
           Bin: bin, Config: cfg, HostPackRoot: packRoot, SandboxEnv: packEnv,
       }, filepath.Join(workspace, captureOutLeaf), dryRun)
   }
   ```

   `RunCaptureAct` pulls `SelfExe` and `HostUser` off `deps`, leaves the proto-entry at that path,
   and sweeps the staging tree — so everything after `runCaptureJail` in `captureHost` (read the
   manifest, refuse an empty delta, `AdmitEntry`, receipt) already works unchanged. Landing a
   mechanism ahead of its wiring has precedent in this package: `EndpointGrantCommands` has done
   exactly this since 2026-08.

   **H2. The REWRITE is not built.** `relocatable:true` entries carry a complete `absoluteRefs`
   list and nothing consumes it. Materialize into `/Users/_yolojail` from a capture whose
   `Manifest.Home` is `/Users/Shared/yolo-captures/<bin>/home` must apply the contract in (d) — the
   symlink refs by re-creating the link with a rewritten target, the file-content refs by
   substituting the prefix through the file's bytes — or refuse. Until it does, a macos-user
   capture is a recorded artifact nobody materializes.

   **H3. A capture does not materialize `packages:`.** `CaptureOptions.Darwin` exists and the
   caller passes nil, so an installer needing a `packages:`-declared tool fails inside the capture
   rather than finding it. Stated, not hidden: paying a native nix build to run one CDN shell
   script is the wrong default, and the seam is there when a real installer needs it.

   ### What a human with a Mac must run — nothing below is measured

   Unit tests pin the profile's BYTES, the argvs, the plan invariants and the relocation record.
   **No kernel has loaded this profile.** After landing H1, on a Mac with `yolo macos-setup` done:

   1. `yolo capture claude --dry-run`-equivalent (H1's closure forwards `dryRun`) — read the
      printed profile and both argvs before letting anything run.
   2. `yolo capture claude`, then check: the staging tree existed only for the run; the entry
      landed under `~/.local/share/yolo-jail/captures/entries/<key>`; the manifest's `relocatable`
      is `true` and `absoluteRefs` names `.local/bin/claude`.
   3. **The confinement itself, which is the only claim that matters:** with the capture profile
      loaded, `sandbox-exec -f <profile> -- /bin/sh -c 'touch /Users/_yolojail/PROBE'` must fail,
      and `log show --predicate 'sender == "Sandbox"' --last 5m` must show the denial. A capture
      that silently wrote to the shared home would look identical to a successful one.
   4. Whether `getpwuid`-based home resolution (as opposed to `$HOME`) trips the `/Users` read deny
      for a vendor installer's shell — the one failure mode designed around rather than observed.
   5. Whether the `EXDEV` refusal in (b) ever fires in practice.

## Verification, honestly

- **Nested jail is mandatory for 1–5** and runs from a throwaway workspace, never `/workspace` —
  `<ws>/.yolo/home` for `/workspace` *is* this session's live home (AGENTS.md, Testing):
  `mkdir -p /tmp/yolo-nested && cd /tmp/yolo-nested && YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -- bash`
- **No slice falls into the host-reachability carve-out.** A capture needs *egress* to a vendor CDN,
  not host-loopback forwarding, and podman-in-podman's forced `--net=host` supplies egress. The one
  thing a nested jail cannot represent is uid mapping: it runs `--userns=host`, so mode bits, `nlink`
  and the `os.Rename` admit want one confirmation on a real rootless host.
- **Slice 6 is design-against-read-code and cannot be tested from here.** macos-user's installer
  pipeline is itself unverified on hardware (`macos-user-nix-and-features.md`); unit tests pin the
  generated profile string and the relocation RECORD, and nothing pins that Seatbelt honors it.
  ⚠ *This line said "the relocation rewrite". There is no rewrite: slice 6 built the record and
  handed the rewrite on (slice 6 hand-off H2). Nothing in the tree substitutes a prefix.* The
  hardware checklist that would close the gap is in slice 6's own section.

## Ships with

- **Unit:** torn capture (marker absent → redo); admit is idempotent for an identical tree; digest is
  stable across a re-walk and moves on an exec-bit flip; materialize into a populated home; EXDEV
  falls back to copy; GC with `nlink>1` reaps nothing; GC dry-run and `--apply` report identical
  numbers; relocation rewrite of an absolute symlink; `relocatable:false` refusal.
  *Slice 6 shipped the record side of the last two:* an absolute symlink AND a file-content
  reference recorded with `relocatable:true`; a reference inside a non-text file recorded with
  `relocatable:false` naming the file; no scan → `relocatable:false` naming the scan; a
  pre-fields manifest reading back `false`. The REWRITE and its refusal are still owed (H2).
- **Integration:** `integration/capture_test.go` — capture once, materialize into **two** workspaces,
  assert the second performs no download and that the two files share an inode. That is the test that
  catches a regression to per-workspace refetch; no unit test can.
- **Tests that must be REWRITTEN, not repaired:** `programreconcile_test.go`'s installer-digest cases
  assert `(sha256, bytes)` of one landing file — that is the guess capture replaces. Rewrite to the
  manifest comparison. `TestUsageListsEveryParsedFlag` and `TestUsageListedCommandsAreRegistered` will
  fail until `pruneUsage` and `commandHelp` carry the new flag and the `capture` row.
- **Docs:** `../design/program-delivery.md` §10 step six status; `../design/storage-and-config.md`
  §2's `<gs>` table (line 112 — already 9 dirs stale, so add `captures/` and say the table was
  incomplete); `roadmap.md:550`'s program-delivery row; `../guides/USER_GUIDE.md` for the new verb.
- **Surfaces:** `yolo capture --help`; `yolo prune --captures-keep N` (per OQ-PD4's "autoprune is an
  option nobody gets by default"); `<CapturesDir>` layout is a documented on-disk contract; the receipt
  gains `kind:"capture"`, `act:"materialize"`, and §6.3's two new tuple members (`file manifest`,
  `platform`) — reader and writer move together or the round-trip goes red.
- **Invariants to satisfy, cite by ID:** `../design/minimal-disk-footprint.md` §5 **P2** (fail-safe on
  unknown liveness), **P3** (a reclaim never strands a running jail — hardlinks make this structural:
  unlinking one name frees nothing another name holds), **P7** (safe at an arbitrary moment).
- **Norms:** `just check-ci` before each commit (the pre-commit hook runs it); `just format`.

## Don't

- **Don't build an image layer.** Rejected three ways in §6.3 (macos-user has no image; it couples
  every capture to the 3.28 GiB rebuild/reload cadence §5.1 priced; it is container-shaped).
- **Don't distribute captures between machines.** Explicitly out of scope (§7) — a capture made here
  is used here. No signing, no publishing, no cross-machine key.
- **Don't invent a second record.** The receipt exists (`af46c9b4`); capture replaces its *guess*, not
  its file — a parallel ledger is the shape §6's warning says was killed once already.
- **Don't fix `DISABLE_AUTOUPDATER` here.** OQ-PD15's ruling names it as separately fixable today and
  gated on none of this.
- **Don't add a `via` value.** Capture is the installer resolver's `record`+`materialize`; `knownVias`
  (`packdecl/contributes.go:306`) stays a two-value set.
- **Don't reach for `archive/tar`.** See the last trap; the entry is a tree.

## Blockers

- **Stop and ask** before making a capture *mandatory* for the installer class. Every slice above is
  additive — the launcher falls through to today's download when no capture resolves. Removing that
  fallback is a behavior change [OQ-PD7](../design/program-delivery.md#decision-ledger) ("report
  first; gate later") does not license, and it makes a first run on a machine with no capture fail.
- **Stop and ask** before adding per-entry metadata a *later* yolo must parse. The receipt schema is
  versioned (`Schema: 1`) and unknown-higher is a hard error in the sibling lockfile
  (`packsrc/lock.go:30`); take the same discipline and say so in the type.
  *Slice 6 added three MANIFEST-level fields (`refScan`, `relocatable`, `notRelocatable`) and one
  new `absoluteRefs[].kind` value, and NO new per-entry field — deliberately, against this line.
  All four are additive, all default to the fail-safe reading, and `ManifestSchema` stays 1
  because an older reader that ignores them materializes only into the home the capture names,
  which is what it did before they existed.*
- No open questions in the design: §6.3 is ruled, and the Open Questions section reads *"None open."*
