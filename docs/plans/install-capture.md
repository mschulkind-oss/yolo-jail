# Plan: capture-and-repackage for the installer class

**Design:** [`program-delivery.md` §6.3](../design/program-delivery.md#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package)
(ruled [OQ-PD10](../design/program-delivery.md#decision-ledger), resequenced FIRST by
[OQ-PD15](../design/program-delivery.md#decision-ledger)) · **Status:** ready ·
Written against `839d0745`, 2026-09-03.

**Precedence:** the design wins on behavior; the tree wins on fact; this file is advice and is
the first thing to be wrong. Never twist code to match it — correct it in the commit.

**Why first:** evergreen multiplies the per-workspace disk cost capture removes. Re-measured here
2026-09-03: `~/.local/share/claude/versions` holds **five** builds at **1.2 GB** (the design measured
four at 1019 MB the same morning), and `~/.local` is a per-workspace bind.

## Map

| Path | Change |
| :--- | :--- |
| `internal/treedigest/` | **new** — `treeDigest`/`treeDigestSkipping` lifted out of `internal/hostskills/compose.go:958,964` verbatim |
| `internal/hostskills/compose.go` | call the new package; delete the local copies |
| `internal/capture/` | **new** — `store.go` (layout, admit, resolve, completion marker), `manifest.go` (delta manifest + capture receipt), `materialize.go` (hardlink, EXDEV → copy), `inner.go` (the backend-neutral driver), `gc.go` (`PruneUnreferencedCaptures(root, keep, olderThan, apply, now)`) |
| `internal/paths/paths.go` | **new** `CapturesDir()` + `CapturesDirUnder(home)`, beside `PacksDir` (`:423`) |
| `internal/storage/ensure.go` | add `CapturesDir()` to the boot `MkdirAll` list (`:44`) |
| `internal/cli/capture.go` | **new** — `yolo capture <bin>` host act; `yolo internal capture-run` inner |
| `internal/cli/dispatch.go` | register `capture` (`:15-35`) |
| `internal/cli/help.go` | one `commandHelp` row (`:10-31`) |
| `internal/cli/internal.go` | add `capture-run` to the hidden namespace switch (`:28`, `:32`) |
| `internal/entrypoint/shims.go` | factor the `nativeLauncherTemplate` install body into a shared const; add the materialize-from-CAS branch |
| `internal/entrypoint/receiptread.go` | extend the reader for `kind:"capture"` + `act:"materialize"` |
| `internal/prune/prunecmd.go`, `pathsref.go` | new `Options.CapturesDir func() string` seam + one report section |
| `internal/cli/commands.go` | `pruneUsage` (`:117`) + the flag parse in `runPrune` (`:149`) |
| `internal/macosuser/seatbelt.go` | **new** `SeatbeltCaptureProfile(stagingHome)` — no workspace write allow |
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
- **`dedupeSubtrees = []string{"npm-global","local","go"}`** (`internal/prune/prune.go:24`) is
  already the exact capture surface set. Import that spelling rather than a fourth one.
- **Locking:** `tryHostApplyLock` (`internal/cli/hostapplylock.go:80`) — non-blocking, never refuses,
  into `<gs>/locks/`. Two concurrent captures of one bin must not race the admit.

## Traps

- **A hardlinked CAS file is the running program's bytes.** An installer or self-updater that opens a
  materialized file **for write** corrupts the CAS and every other workspace at once, silently.
  **Constraint:** chmod CAS entry files to drop `w` at admit time, and never dedupe *within* the CAS.
  claude's updater writes new version dirs (new inodes) and is safe; this is not a general guarantee.
- **The scratch root must live inside the CAS dir** (`<CapturesDir>/staging/<id>`), not `/tmp`.
  Admission is `os.Rename`; a different filesystem turns it into a 1.2 GB copy, which is the cost
  this whole subsystem exists to delete.
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

1. **`internal/treedigest` + `paths.CapturesDir()` + the store skeleton.** Lift the digest, add the
   path pair and the `ensure.go` mkdir, write `admit`/`resolve`/completion-marker with the torn-write
   redo. No CLI. → `go test ./internal/treedigest ./internal/hostskills ./internal/capture ./internal/paths`
2. **The inner driver, `yolo internal capture-run`.** Baseline walk of the three home-relative roots
   → run the installer → delta manifest → move the delta paths into the out dir (same filesystem, so
   rename). Backend-neutral: it is a process with a `HOME`, nothing more. Drive it in tests with a
   fixture "installer" shell script that writes files and one absolute symlink. → `just test-fast`
3. **`yolo capture <bin>` on the container backends.** Host act: `HonoredInstalls` → scratch workspace
   under `<CapturesDir>/staging/<id>` → the ordinary run pipeline with the inner driver as the command
   → hash → rename into `<CapturesDir>/entries/<key>` → write the capture receipt. First user-visible
   benefit, and a **small** one: reconcile stops guessing (`reconcile.go:218` compares one file's
   digest today; a manifest replaces it). → `integration/capture_test.go` + a nested jail
4. **Materialize, from the launcher.** `nativeLauncherTemplate`'s `_do_install` gains a branch: if a
   capture for this bin+platform resolves, hardlink it in (EXDEV → copy) and write an
   `act:"materialize"` receipt; otherwise fall through to today's download. **This is the slice that
   pays** — the second workspace stops downloading, and 1.2 GB stops being per-workspace.
   *Advice: materialize from the launcher, not a boot genStep* — §5.2 names "you pay nothing for a
   tool you never invoke" as a virtue any replacement must keep, and OQ-PD12a's design has no boot
   step at all. → an `integration/` test over two workspaces asserting one download
5. **Remove + GC.** `st_nlink == 1` on the entry's files **is** the unreferenced oracle: a materialized
   hardlink keeps the count above 1 from a workspace `yolo` cannot enumerate, so it is fail-safe by
   construction, and it closes OQ-PD4's back door by construction too — dropping a pack leaves the
   materialized tree, so the entry stays referenced and GC reclaims nothing. Compose it with the two
   sibling idioms: an age floor for the in-flight window, keep-newest-N per (bin, platform).
   A cross-device copy does not bump nlink, so that workspace can see its entry reaped — which strands
   nothing (it has its own bytes) and only forces a re-capture. → `go test ./internal/prune ./internal/capture`, then `yolo prune` dry-run
6. **`macos-user`.** `SeatbeltCaptureProfile(stagingHome)`: `deny file-write* /` then allow only the
   staging dir + `/tmp` + `/var/folders` — the shared `/Users/_yolojail` home denied for the duration.
   Same inner driver, `HOME=<staging>`. Adds **relocation**: the manifest records every absolute
   reference to the staging prefix (symlink targets first — `~/.local/bin/claude` is one, confirmed by
   `ls -l` in this jail) and materialize rewrites them, or the entry is admitted `relocatable:false`
   and refuses to materialize elsewhere. → `go test ./internal/macosuser ./internal/capture` only

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
  generated profile string and the relocation rewrite, and nothing pins that Seatbelt honors it.

## Ships with

- **Unit:** torn capture (marker absent → redo); admit is idempotent for an identical tree; digest is
  stable across a re-walk and moves on an exec-bit flip; materialize into a populated home; EXDEV
  falls back to copy; GC with `nlink>1` reaps nothing; GC dry-run and `--apply` report identical
  numbers; relocation rewrite of an absolute symlink; `relocatable:false` refusal.
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
- No open questions in the design: §6.3 is ruled, and the Open Questions section reads *"None open."*
