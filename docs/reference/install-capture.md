---
status: current
stage: CURRENT
verified: 2026-10-16
verified_commit: 8e65f3444
covers:
  - internal/capture/
  - internal/treedigest/
  - internal/cli/capture.go
  - internal/cli/capturehost.go
  - internal/cli/capturematerialize.go
  - internal/cli/autocapture.go
  - internal/cli/capturelandlock_linux.go
  - internal/cli/run/autocapture.go
  - internal/cli/run/captures.go
  - internal/cli/run/macosctxtree.go
  - internal/entrypoint/capturereceipt.go
  - internal/macosuser/capture.go
  - internal/macosuser/seatbeltcapture.go
  - internal/prune/prunecmd.go
tags: [capture, installers, program-delivery, macos-user, reflink, relocation, prune]
---

# Install capture — recording a vendor installer once per machine

**Status:** Verified 2026-10-16 against `8e65f3444`. MEASURED on Linux: the store, the capture
driver, `yolo capture`, materialize, the reap and auto-capture, in nested jails (2026-09-04), and
a real claude capture coming out relocatable (2026-10-01). The relocation rewrite is MEASURED on
Linux only, against temp directories standing in for the two macOS homes. UNMEASURED: a
`macos-user` launch materializing from the staged store ([H4](#h4)), the Seatbelt denial probe
during a capture ([the Mac checks](#mac-checks)), and uid mapping on a real rootless host. Graduated
from `docs/plans/install-capture.md`, deleted in `dffa38006`, whose own status line called it "not a graduation candidate
until a Mac has run H4"; that run is carried here as an owed check rather than a gate.

**Install capture** runs a vendor installer once, in a throwaway jail, records what it left in
the home (the **delta**), and files that delta in a machine-wide content-addressed store. From then
on, a cold launcher in any workspace **materializes** the entry (places its files into the home)
instead of downloading. It exists for the one dependency class with no native lockfile: a
`program` contribution declared `via: "installer"`, whose installer is a URL that runs as a shell
script and writes wherever it likes. What it buys is a manifest of what the installer made, an
offline deterministic install, drift against an immutable reference, and one copy per machine
rather than per workspace. It is not the disk fix for the version axis; that is
[the V-axis prune](agent-cli-copies.md#the-v-axis-prune), and on ext4 a capture adds a copy
([`agent-cli-copies.md`](agent-cli-copies.md#the-ext4-inversion)).

The behavior was designed in [`program-delivery.md` §6.3](../design/program-delivery.md#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package),
whose rulings ([`OQ-PD17`](../design/program-delivery.md#decision-ledger),
[`OQ-PD18`](../design/program-delivery.md#decision-ledger),
[`OQ-PD25`](../design/program-delivery.md#decision-ledger),
[`OQ-PD26`](../design/program-delivery.md#decision-ledger)) resolve in that doc's Decision Ledger.

| Component | Lives in |
| :--- | :--- |
| Store: layout, admit, resolve, completion marker | `internal/capture` (`Store`, `Entry`, `Key`) |
| Canonical tree digest | `internal/treedigest` (shared with host skill composition) |
| Capture driver (baseline walk, delta, move) | `internal/capture` (`Run`, `Options`, `DefaultExcludes`), behind `yolo internal capture-run` (`internal/cli/capture.go`) |
| Delta manifest and relocation record | `internal/capture` (`Manifest`, `relocate.go`) |
| Selection rule, and the reap that is its complement | `internal/capture` (`Select`, `PruneSupersededCaptures`) |
| Materialize, the clone primitives, the relocation rewrite | `internal/capture` (`Materialize`, `clone_linux.go`, `clone_darwin.go`, `rewrite.go`) |
| `yolo capture <bin>`, the host act | `internal/cli` (`captureHost`, `capturehost.go`) |
| `yolo internal capture-materialize`, the receipt adapter | `internal/cli` (`capturematerialize.go`, `captureRecords`, `resolveCaptureFor`) |
| Auto-capture trigger and decision | `internal/cli/run` (`autocapture.go`), `internal/cli` (`autocapture.go`) |
| The `:ro` store mount for container jails | `internal/cli/run` (`captures.go`) |
| Capture receipt writer | `internal/entrypoint` (`CaptureReceipt`, `capturereceipt.go`) |
| The launcher's materialize branch | `internal/entrypoint` (`nativeLauncherTemplate`, `InstallOnlyEnv`, `CapturesDirEnv`) |
| `macos-user` capture plan, Seatbelt capture profile, staged store | `internal/macosuser` (`capture.go`, `SeatbeltCaptureProfile`, `StageCaptureCommands`, `StagedCapturesRoot`) |
| The `yolo prune` section | `internal/prune` (`prunecmd.go`, `Options.CaptureRecords`) |

**Reads with:** [`agent-cli-copies.md`](agent-cli-copies.md) (the two copy axes and reflink),
[`storage-and-config.md`](storage-and-config.md#machine-wide-storage) (where `captures/` sits in
machine storage), [`workspace-path-mirroring.md`](workspace-path-mirroring.md) (relocation's
sibling problem).

---

## Defined terms

- **Delta**: the paths an installer created or changed under the capture surfaces, between a
  baseline walk taken after boot and a walk taken after the install.
- **Capture surfaces**: the home subtrees where installed programs live, `paths.InstalledProgramSurfaces`
  (the per-workspace home binds plus codex's nested standalone payload).
- **Entry**: one admitted capture, `entries/<key>/`, holding `tree/` (the delta, unpacked), its
  `capture-manifest.json`, its `receipts.jsonl` and the completion marker.
- **Record** and **materialize**: [§6.3](../design/program-delivery.md#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package)'s
  two verbs, and the two `act` values of a capture receipt. Record is the capture; materialize is
  placing an entry into a home.
- **Program**: what an entry is selected by, `capture.Program`: the binary and the platform the
  capture observed, plus a fork's source, a patched fork's key, or an ordinary install's
  compatibility identity when another writer made the entry.
- **Relocatable**: whether an entry may be materialized into a home other than the one it was
  captured in, with every recorded absolute reference rewritten.
- **Proto-entry** *(coined in the plan)*: the entry-shaped out directory the driver fills
  (`tree/` plus the manifest) before admission renames it into `entries/`.

## Principles

1. <a id="blockers"></a>**Capture is additive, never mandatory.** Every path falls through to
   the vendor download when no entry resolves: a torn entry, an unreadable store, a refused
   relocation, a failed auto-capture. Making a capture required for the installer class is a
   behavior change [`OQ-PD7`](../design/program-delivery.md#decision-ledger) ("report first; gate
   later") does not license, and it would make a first run on a machine with no capture fail.
   Stop and ask before crossing it. Default-on auto-capture does not cross it: a launch tries to
   fill the store, and every way that can fail ends where a miss always did.
2. **One selection rule.** `capture.Select` is the only code that decides which entry answers
   "`<bin>` for `<platform>`?". The resolver is a lookup in its result, and the reap removes its
   complement, so the two cannot drift.
3. **The install is the launcher's.** A capture runs `env YOLO_INSTALL_ONLY=1 <bin>`, which is the
   generated native launcher's own `_do_install` path. A second implementation of
   download-then-run would capture bytes no launch would have produced.
4. **The platform is the jail's.** Only the driver, inside the capture jail, can observe it: a Mac
   running podman captures `linux/<arch>`, and a host-side `runtime.GOOS` would answer
   `darwin/arm64`, the one wrong answer that looks right. The host act copies `Manifest.Platform`
   into the receipt.
5. **No second record.** The receipt schema is extended, never forked: a capture's identity is
   the existing tuple (bin, declared URL, resolved key, manifest sha256, path, time) plus one new
   field, `platform`. The receipts live inside the entry they describe, so they cannot go stale
   relative to it.
6. **Captures stay on the machine that made them.** No signing, no publishing, no cross-machine
   key.

## Invariants

- **Admission is a rename within the store.** A capture stages under `<CapturesDir>/staging/<id>`
  and `Store.Admit` refuses a staged tree from anywhere else rather than copying gigabytes.
- **The completion marker is written last, and it is all `Resolve` reads.** A torn or in-flight
  entry reads as absent, and the next admit of the same content clears and redoes it.
- <a id="host-owned-metadata"></a>**Only the host writes an entry's receipts and marker.** A
  proto-entry is the out directory a capture or build jail filled, so `Store.AdmitEntry` (and its
  guarded and validated forms) refuses one that already holds `receipts.jsonl` or the completion
  marker, before anything is stored. A record counts only for the entry its key names: the reader
  (`captureRecords`) drops a line naming another key or none, and selection and the materialize's
  platform gate ignore a record keyed to another entry.
- **Resolve is strictly offline.** A miss is `ErrNotCaptured`, naming the act that would fix it.
- **Entry files are frozen read-only at admit, and nothing is deduplicated within the store.**
  Directories keep their write bit so a reap can unlink them.
- **The capture jail gets no store mount.** `run.Options.CapturesDir` returns `""` for it, so its
  launcher cannot materialize an old entry and record it as a fresh capture, and the same switch
  suppresses auto-capture there, so a capture never triggers a capture.
- **The store is mounted read-only into every container jail**, at `/ctx/captures`, named by
  `YOLO_CAPTURES_DIR`. Only the host act admits.
- **The store path is baked into each launcher at generation time**, never read by the generated
  script from the environment, because `macos-user` execs launchers under `env -i`. The receipts
  file follows the same rule.
- **A capture's manifest is target-independent.** It records references to the capture home and
  never a claim about the home it will be materialized into; length or destination checks belong
  to the rewrite.

> [!WARNING]
> <a id="the-hardlink-trap"></a>**A hardlinked store file is the running program's bytes.** On
> the hardlink arm, an installer or self-updater that opens a materialized file for write
> corrupts the store and every workspace at once. The admit-time freeze is what keeps that arm
> safe. On the reflink arm, which a healthy machine takes, the placed file is its own inode and a
> write copies on write, so the hazard is absent there. Do not drop the freeze because reflink
> made it look unnecessary, and never dedupe inside the store.

## The store

<a id="slice-1"></a>

The store is `paths.CapturesDir()` in machine storage, beside the pack store, created at boot by
`storage.EnsureGlobalStorage`. Its layout is a documented on-disk contract:

```text
<CapturesDir>/staging/<id>/              a capture in flight
<CapturesDir>/entries/<key>/
    tree/                                the delta, unpacked
    capture-manifest.json                beside the tree, never inside it
    receipts.jsonl                       record (and build) receipts for this entry
    .yolo-capture-complete               written last
<CapturesDir>/auto-capture-failures/     the auto-capture back-off memo
```

It copies `packsrc.Store`'s discipline: two stages, marker last, offline resolve. The key is the
repository's content-address convention, a short prefix of sha256 over the canonical tree digest
(`treedigest`: directories, symlinks by their unfollowed target, files by exec bit and content,
sorted). The tree is stored **unpacked**: a tar cannot be hardlinked or cloned from, and would be
unpacked per workspace, which is the copy the store exists to delete.

`Store.AdmitEntry` renames the whole proto-entry in one step, key computed from `tree/` alone and
marker still last, so an entry never reads complete while its manifest is missing.

## Recording: the capture driver

<a id="slice-2"></a>

`yolo internal capture-run` is backend-neutral: a process with a `HOME`. It walks the capture
surfaces (a baseline of kind, mode, size and mtime per path, never content hashes), runs the
installer, walks again, writes the delta manifest, and moves the delta into the out directory's
`tree/`.

- **A directory absent from the baseline moves whole**, in one rename; a **surface root** never
  does, because on the container backends it is a bind mountpoint and renaming it is `EBUSY`.
- **yolo's own state is excluded** (`capture.DefaultExcludes`: the jail's machine-storage
  directory and the in-jail daemons' log directory), and the guard covers the whole-directory
  move too, since a new `.local/share` moved whole would otherwise carry yolo's state with it.
  A capture jail that recorded its own daemon log handed every materializing jail a stale log in
  place of its own (MEASURED 2026-10-01), which is why that directory is excluded.
- **The boot writes into the surfaces before the installer does**, so the baseline has to be
  taken inside the jail, after boot, before install. A host-side diff cannot see the delta.
- **When the move crosses a mount**, the driver falls back to a copy, counts it and says so on
  stderr. `rename(2)` compares the mount, not the device: two binds of one btrfs with the same
  `st_dev` still return `EXDEV` (MEASURED 2026-09-04).
- **Absolute references are recorded from symlink targets always, and from file contents only
  with `--scan-content-refs`**, a read pass over the whole delta. A reference inside a file that
  sniffs binary (git's heuristic: a NUL in the first bytes) is recorded and makes the entry
  non-relocatable.

Not built, by decision: an installer that rewrites a file to the same size and mode within the
mtime granularity is invisible to the baseline, and writes outside the capture surfaces are left
alone rather than enumerated (that needs a whole-home walk).

## `yolo capture` on the container backends

<a id="slice-3"></a>

`yolo capture <bin>` resolves the declaring pack through `packload.HonoredInstalls` and the
launch's pack resolver (fetched packs included), stages under the store, runs the ordinary run
pipeline with that staging directory as the workspace and the driver as the command, admits the
proto-entry, and appends a `kind:"capture"`, `act:"record"` receipt beside the entry.
`yolo capture <pack>/<name>` is the same verb building a patched extension's tree now.

- <a id="slice-3a"></a>**The staging directory is the workspace, and the driver reaches the
  surfaces through the workspace bind.** Under podman each capture surface has two container
  paths, its own bind and `/workspace/.yolo/home/<subtree>`, and only the second shares a mount
  with the out directory, so `capture.Options.SurfaceRoot` is that second path. Recorded paths stay
  home-relative and `Manifest.Home` stays the real `/home/agent`.
- <a id="slice-3d"></a>**The installer a capture runs is the launcher**, under `YOLO_INSTALL_ONLY`,
  which stops it exec'ing the tool afterwards; otherwise the capture would record the tool's
  first-run state.
- **The capture lock is per program**, non-blocking: a second capture of the same bin refuses
  rather than waits.
- **Three gates stop a failed install from admitting an entry**: the launcher exits 1 under
  install-only when the binary is not executable afterwards; a non-zero installer is an error and
  writes no manifest; and `captureHost` refuses an empty delta by name.
- **The capture receipt is written from Go** (`entrypoint.CaptureReceipt`), on the same head as
  the shell-written receipts, escaped by Go, and its append can fail the capture, because the
  receipt is half the deliverable. A fork's build is recorded under a second kind, `kind:"build"`
  ([`FP-D8`](../design/forked-programs-as-packs.md#FP-D8)).

On a Linux machine with no container runtime, `yolo capture` runs the installer on the host
instead, confined by Landlock to a throwaway home under the same staging directory, and its
receipt's platform carries a `+host` origin mark
([`HP-D18`](host-agent-environment.md), `capturelandlock_linux.go`).

## Materialize

<a id="slice-4"></a>

The generated native launcher's cold branch (`if [ ! -x "$REAL_BIN" ]`) first tries
`yolo internal capture-materialize`: if an entry resolves for this bin and the jail's platform,
it is placed in the home and an `act:"materialize"` receipt is written; otherwise the launcher
falls through to the vendor download. It is in the launcher, not a boot step, so a tool never
invoked costs nothing.

- <a id="slice-4a"></a>**Placement is reflink, then hardlink, then copy, decided once per run.**
  Reflink compares the filesystem (every bind of one filesystem shares a superblock) where
  `link(2)` compares the mount, so a clone from the `:ro` store bind into a home bind works where a
  hardlink returns `EXDEV` (MEASURED 2026-09-04 in a real podman container). On Linux the clone is
  `FICLONE`; on macOS it is APFS's `clonefile(2)`, made with `CLONE_NOOWNERCOPY`, within one volume
  only. `EXDEV`, `ENOTSUP` and `EPERM` (Seatbelt's answer to a denied operation) retire the arm for
  the run rather than per file. The copy arm is loud and names both filesystems, because ext4 has
  no reflink and is common.
- **Mode comes from the manifest.** A cloned or copied file gets the installer's mode back; a
  hardlinked one keeps the store's frozen mode, since chmod-ing it would chmod the store. That is
  why materialize is driven by the manifest rather than by a walk of the tree.
- <a id="slice-4e"></a>**There is no index.** The lookup scans each entry's own receipts: one
  directory read plus one small file per entry, on a path that runs once per program per
  workspace. Newest record wins by receipt time, the greater key breaking a tie (the stamp has
  one-second resolution), and the winner is then fetched through `Store.Resolve`, so a receipt
  beside a torn tree cannot select it.
- <a id="record-platform"></a>**The platform is a record's, not the entry's.** The key is the
  tree's content alone, so the same bytes admitted for a second platform (a script, which no build
  changes) land on the existing entry: its manifest, which names the platform admitted first, is
  kept, and the second admission adds only a `record` receipt. `capture.Materialize` therefore
  takes the platform asked for when the manifest names it or, given a record reader
  (`MaterializeOptions.PlatformRecords`), when a record beside the entry names it exactly; with
  neither it refuses with `capture.ErrWrongPlatform` before writing. The fork-build launcher, the
  installer-capture launcher, the host floor and the cached-good check all judge by the record.
  The host floor selects a host capture's record (platform plus the `+host` mark) as well as a
  jail's, so its reader (`floorCaptureRecords`) reads that mark as the platform it was captured
  on; a jail's reader keeps it, since no jail selects a host capture.
- <a id="slice-4f"></a>**The capture jail is given no store** (see [Invariants](#invariants)).
- **Materialize is not transactional.** A failure partway leaves what it already placed, and the
  launcher falls through to the vendor installer.

> [!WARNING]
> One line is pinned only by the integration suite: the container arm's `capturesDir` in
> `run.Run`. Unit tests in `internal/cli/run` drive the `macos-user` arm, which builds no podman
> argv, so deleting it leaves them green while `integration/capturematerialize_test.go` goes red.

## Reaping

<a id="slice-5"></a>

`capture.PruneSupersededCaptures` reaps every entry `capture.Select` would not return: everything
that is not the newest for its program. An entry with no readable receipt is attributed to no
program, a miss for the resolver and reapable by the same rule. It is a section of `yolo prune`,
dry-run by default like every other, with its bytes folded into the reclaim total.

- **Reclaiming is never a correctness event.** A reflinked destination survives its source's
  unlink byte-identical (MEASURED 2026-09-04), and the other arms strand nothing either; the worst
  case of reaping a live entry is that the next cold install downloads. A reap racing a
  materialize is a miss, which falls through.
- **A reaped entry keeps its manifest and receipts** and reads as absent, so drift against a
  version no longer stored costs kilobytes.
- **The store takes a `Records` function rather than importing the receipt reader**, because the
  reader's package pulls the config graph that `internal/prune` deliberately avoids; the one
  adapter is `captureRecords` at the CLI boundary. A nil reader is refused, not defaulted, since
  the complement of an empty selection is the whole store, and the prune section prints that it
  skipped.

Two leaks are deliberate. A torn entry is invisible to the sweep (a torn entry and an in-flight
admit are the same observation), so an admit killed before its marker leaks a tree until the next
capture of the same content. A stale `staging/<id>` is never swept, because deleting a live one
breaks its capture and telling the two apart needs the liveness or age rule [`OQ-PD17`](../design/program-delivery.md#decision-ledger) declined.
Both are cleared by the next `Stage`/`Admit` of the same program.

## `macos-user`

<a id="slice-6"></a>

### Recording under Seatbelt

<a id="h1"></a>

`yolo capture` on `macos-user` runs `macosuser.RunCaptureAct`: the same driver, with
`HOME` set to a staging home and `--scan-content-refs` passed, under `SeatbeltCaptureProfile`,
which denies every file write and allows only the staging root, `/tmp` and `/var/folders`. The
shared sandbox home is denied for the duration.

- <a id="slice-6a"></a>**The profile allows the staging root**, holding both the home the
  installer runs against and the out directory, so "everything this capture touches is under one
  allow" is checkable, and the out directory stays outside the captured home.
- <a id="slice-6b"></a>**The capture stages on neutral ground**, `macosuser.CaptureRootDefault()`,
  not under `<CapturesDir>/staging`. The store is under the invoking user's home, which the
  sandbox account must not reach, and a writable subtree there would put every other captured
  program in reach of a first-run vendor installer. The host then moves the finished proto-entry
  into the store's staging directory, and that move **refuses on `EXDEV`** rather than copying.
  READ FROM CODE: on a stock Mac both sides are on the one APFS Data volume.
- <a id="slice-6c"></a>**The staging home gets a bootstrap of its own** (`buildBootstrapEnv`
  takes the home as a parameter), because the generated launcher exists only in a home the darwin
  bootstrap rendered. `TestCapturePlanRefusesTheSharedHome` fails if a capture plan points at the
  shared home.

### The relocation record and the rewrite

<a id="slice-6d"></a><a id="h2"></a>

The manifest carries `refScan` (which scan ran), `relocatable` and `notRelocatable` (the
reasons), because one bit cannot tell "looked and found nothing unrewritable" from "never looked".
The contract, which nothing else may read the fields against:

> A materialize whose destination home equals `Manifest.Home` ignores `relocatable`. A materialize
> into any other home refuses when it is false, printing `notRelocatable`, and rewrites every
> `absoluteRefs` entry from `Manifest.Home` to the destination when it is true.

So `relocatable:false` costs the container backends nothing, since their destination is always
the `/home/agent` they captured in, and a manifest from before these fields reads back false and
is still correct.

The rewrite is `internal/capture/rewrite.go`, called by `capture.Materialize`:

- **Every refusal happens before the home is written.** `planRelocation` checks the whole
  manifest first and collects every reason; each wraps `ErrNotRelocatable`, so the launcher falls
  through. It refuses: a record that says no; `relocatable:true` over a symlink-only scan; a
  reference naming a path the capture lacks, of the wrong kind, or with a prefix that is not
  `Manifest.Home`; a content reference in a file that sniffs binary (re-checked against the bytes
  being placed); an unknown reference kind; and an absolute link into the capture home that no
  reference lists.
- **A symlink is re-created with its target cleaned, then the prefix swapped**, because darwin
  resolves `..` physically and the destination reaches `.local` through a link.
- **A rewritten file is a new inode**, streamed through the substitution into a temp file beside
  the destination and renamed into place, never edited through a placed file (on the hardlink arm
  that would be the store's inode). `MaterializeResult.Rewritten` counts these apart from the
  chain's arms.
- <a id="h2-f"></a>**A partial write never lands at a destination path.** I/O errors on the
  destination, or a conflict with what the home holds, can still leave a partial tree; neither
  wraps `ErrNotRelocatable`, since recapturing would change neither.
- **The substitution is plain bytes, every occurrence**, matching the recording scan exactly; a
  capture-home string followed by more name characters is rewritten too, which the capture home's
  spelling makes moot. There is no length check, because every reference inside a binary refuses;
  a padding rewrite for binaries, if one is ever built, owns it.
- **A symlink that resolves to a directory is an existing directory**, so materialize never
  chmods through the `macos-user` home's links into the workspace sidecar.
- **No schema change.** `capture-materialize` prints one extra line when it relocated, naming the
  capture home and what it rewrote.

### The staged store a `macos-user` launch reads

<a id="h4"></a><a id="hand-offs--what-is-not-wired-and-the-exact-line-that-wires-it"></a>

A `macos-user` sandbox cannot read the store, which sits under the invoking user's home. So each
launch stages a **sealed copy of each selected program's current entry** under the backend's
host-owned staging root (`macosuser.StagedCapturesRoot`, under `macosuser.StagingRoot`), names it
to the bootstrap as `YOLO_CAPTURES_DIR`, and the bootstrap bakes it into every launcher. The
relocation rewrite then places it from the staging home into the sandbox home with no further
change.

- **The host picks; the plan builder stays pure.** `macosUserCaptures` asks `resolveCaptureFor`,
  the sandbox's own resolver, for each selected `via: installer` program at `darwin/<GOARCH>`,
  with no source or fork, so a fork's build is never picked. The pick crosses as
  `HostContext.Captures`.
- **The copy is a copy, never a link of the store's inodes.** A `macos-user` capture's files are
  owned by the sandbox account under its staging tree's shared-group ACL, so a link would let the
  sandbox rewrite bytes every workspace runs (INFERRED from that ownership). `StageCaptureCommands`
  copies into `staging/<key>`, drops group and other write, then moves into `entries/<key>`, and
  skips a key already staged, since a key is a content address. Plain `cp -R` carries no ACL
  (read from Apple's source); `chmod -R -N` is not used, because it follows symlinks.
- **The copy is best effort.** Its commands are `RunPlan.CaptureStageCommands`, run after the
  fatal stage commands; a failure warns once, names the program that installs the ordinary way,
  and the next launch tries again. `PlanInvariants` refuses a capture script among the fatal
  commands, a store outside the staging root or under any spelling of `/Users` homes, and a store
  named with nothing staged or the reverse.
- **The prune keeps every current entry at the platform** (`HostContext.CapturesKept`), not only
  this launch's, so alternating between two pack sets copies nothing twice.
- **The session Seatbelt profile is the same with and without captures**
  (`TestTheSessionProfileIsTheSameWithAndWithoutCaptures`): the staging root's read allow, which
  every staged tree needs, already covers it.
- **The fork launcher reads the same baked store**, but the copy carries installer captures only,
  so a fork's build still reaches no `macos-user` launch
  ([`FP-D3`](../design/forked-programs-as-packs.md#FP-D3)).

<a id="h3"></a>**A capture does not provide `packages:`.** `CaptureOptions.Darwin` exists and the
caller passes nil, so an installer that needs a `packages:`-declared tool fails inside the
capture. Paying a native nix build to run one shell script is the wrong default; the seam is
there for a real installer that needs it.

## Auto-capture

<a id="slice-7"></a>

Ruled default on ([`OQ-PD18`](../design/program-delivery.md#decision-ledger)). A **fresh** launch,
never an attach ([`OQ-PD25`](../design/program-delivery.md#decision-ledger)), enumerates the
selected packs' `via: "installer"` programs through `HonoredInstalls`, asks `resolveCaptureFor`
for each at the jail's platform, and captures each miss before its container starts: blocking, one
program at a time, with a line stating the cost. The container arm passes `linux/<arch>`
(`containerJailPlatform`); the `macos-user` arm passes darwin and runs that backend's capture act
(`autoCaptureActFor`), so a workspace choosing `macos-user` never records a linux entry.

- **The seam carries the bins and the platform, both decided by the pipeline**; `internal/cli`
  decides the miss and calls `captureHost`, which it owns because it launches jails.
- **The lock is `captureHost`'s.** The trigger takes none of its own, since a second open of the
  same flock in one process would fail its own non-blocking acquire. A launch that loses the lock
  skips, and that is not a failure.
- **Failure is never fatal, and it is remembered** ([`OQ-PD26`](../design/program-delivery.md#decision-ledger)):
  the launch warns once, naming the program, `yolo capture <bin>` and the hatch, and continues. A
  memo per program and platform backs the next launches off, doubling up to a cap; a different
  yolo version retries at once. Launches inside the back-off say nothing.
- **On `macos-user`, only for a launch the backend will not refuse at once.** The arm asks the
  backend's own preflight (`macosuser.PreflightLaunch`) first and skips silently when it would
  refuse, holds the account home's hold while the capture runs, and releases it before the backend
  takes its own.
- **`YOLO_NO_AUTO_CAPTURE`**, any non-empty value, turns the trigger off loudly, naming what it
  suppressed.
- **The integration suite sets the hatch** unless `YOLO_TEST_REAL_PACK_INSTALLS` is set, so a
  vendor download does not land on every push.

> [!WARNING]
> Auto-capture depends on [the V-axis prune](agent-cli-copies.md#the-v-axis-prune). Default-on
> makes a stale entry harmful: a workspace materializes it, evergreen updates past it, and the
> vendor updater's retained old build is left in the workspace. Do not remove the prune while
> auto-capture is on.

## Other writers of the store

The store also holds entries other acts admit, selected through the same `capture.Select` by the
extra fields of `capture.Program`:

- a fork's build (`kind:"build"` receipts with revision, recipe and toolchain;
  [`forked-programs-as-packs.md`](../design/forked-programs-as-packs.md#FP-D8));
- a patched fork's build, keyed by its fork ([`patched-forks.md`](patched-forks.md));
- an ordinary program's sealed preparation, keyed by a compatibility identity
  ([`launch-update-preparation-plan.md`](../design/launch-update-preparation-plan.md)).

Their reaps follow the same complement rule, so a fork's newest build per source is kept.

## Failure modes

| Case | What happens |
| :--- | :--- |
| No entry for the program | The launcher downloads, as before capture existed |
| Torn or in-flight entry | Reads as absent; the next admit of that content redoes it |
| Entry reaped mid-materialize | A miss; the launcher downloads |
| Relocation refused | Pre-write, `ErrNotRelocatable`; the launcher downloads |
| No manifest or record names this platform | Pre-write, `ErrWrongPlatform`; the fork-build launcher names the host act that records one: `yolo pack build <pack> --force` for a pack-tree program, `yolo capture <bin>` for a fork |
| Installer fails, or lands nothing | No manifest, or an empty delta refused; nothing is stored, the launch continues |
| Two workspaces capture one program | The loser skips and installs lazily |
| Auto-capture fails | Warns once, backs off per program and platform, the launch continues |
| The host floor's evergreen recapture fails | The installer's URL unreachable: the installed copy runs, with a could-not-check line. Otherwise `yolo host` stops with [the refusal block](host-agent-environment.md#a-failed-update-stops-the-launch) unless `YOLO_USE_INSTALLED` names the program ([PD-NF1](../design/program-delivery.md#PD-NF1)) |
| `macos-user` staged copy fails | Warns once; that program installs the ordinary way; the next launch retries |
| Move across a mount in the driver | Copies, counts, says so |
| `macos-user` proto-entry move hits `EXDEV` | Refuses rather than copying |

## What this does not do

<a id="non-goals"></a>

- **No image layer.** `macos-user` has no image, and it would tie every capture to the image
  rebuild cadence.
- **No distribution between machines.**
- **No `via` value for capture.** Capture is the installer resolver's record and materialize, not
  a delivery route; `source` is a separate route (the fork route), not a capture.
- **No `archive/tar`.** The entry is a tree.
- **No `DISABLE_AUTOUPDATER` change here**; it is separately fixable.
- **No negative space beyond the stated leaks**: no index, no age floor, no `K = 2`, no
  `--captures-keep` flag (see [Why it's this way](#why-its-this-way)).

## What is measured, and what is owed

<a id="build-order"></a>

- **Nested jails**, from a throwaway workspace, measured the store, the driver, `yolo capture`,
  materialize, the reap and auto-capture on 2026-09-04: an empty store captured on the first
  launch and materialized by reflink, a second workspace materialized the same key with no
  notice, the hatch fell through to the vendor installer, and an installer that failed stored
  nothing while the launch continued.
- **A real claude capture** (2026-10-01, Linux, no agent session) came out `relocatable:true` with
  one absolute reference, the launcher symlink, and no reference to the home in any file.
  INFERRED for the Mac: the darwin binary is also downloaded, so it cannot carry the staging home
  unless the installer writes it into a file.
- **Uid mapping is owed one confirmation on a real rootless host.** A nested jail runs
  `--userns=host`, so mode bits, `nlink` and the admit rename are unseen there (see AGENTS.md's
  carve-outs). No part of capture falls into the host-loopback carve-out: a capture needs egress,
  not loopback forwarding.

### <a id="mac-checks"></a>What a human with a Mac must run

Unit tests pin the capture profile's bytes, the argvs, the plan invariants, the relocation record
and the stage scripts (run by `check-macos` with macOS's own `cp`, `chmod` and `mv`); none of them
loads a profile or runs as the sandbox account. Items 1 and 2 ran on 2026-09-11 as
[`provisioner-sets.md` M4](../design/provisioner-sets.md#15-what-a-mac-session-should-measure); the
rest have no recorded run.

1. ✅ A dry run of `yolo capture claude` (the `macos-user` act forwards the run's dry-run): read the printed profile and both argvs.
2. ✅ `yolo capture claude`: the staging tree exists only for the run, the entry lands under the
   store, and the manifest is `relocatable:true` naming `.local/bin/claude`. (Whether the Mac
   manifest came out relocatable was not recorded; `cat` it.)
3. <a id="item-3"></a>**The confinement itself:** with the capture profile loaded, a write to the
   shared sandbox home must fail and `log show` must show the Sandbox denial. A capture that wrote
   to the shared home would look identical to one that did not.
   `TestMacosUserCaptureSeatbeltProfileDeniesTheSharedHome` is the case; no green run is recorded.
4. Whether `getpwuid`-based home resolution trips the `/Users` read deny for an installer's shell.
5. Whether the proto-entry move's `EXDEV` refusal ever fires.
6. A launch materializes item 2's entry into the sandbox home: `capture-materialize` prints its
   relocation line naming the capture home, `~/.local/bin/claude` links under the sandbox home,
   `claude --version` runs, and the line names the arm (`reflink` where `clonefile(2)` succeeds,
   else `hardlink` or `copy`). Also unmeasured: what `clonefile(2)` and `link(2)` answer from a
   store owned by the invoking user into the sandbox home under Seatbelt.

`integration/macosusercapture_test.go`'s two cases
(`TestMacosUserLaunchesMaterializeACapturedFixture`, `TestMacosUserAutoCapturesAFixtureOnFirstLaunch`)
and item 3's case wait on a dispatch of `macos-user.yml`.

## Why it's this way

Rulings and implementation decisions a maintainer would otherwise undo. The `slice-`, `H` ids are
the graduated plan's own labels (its build-order slices and hand-offs), kept because code
comments cite them.

| ID | Decision | Why |
| :--- | :--- | :--- |
| <a id="slice-3d-row"></a>slice-3d | The capture runs the generated launcher under `YOLO_INSTALL_ONLY`; there is no shared install-body constant | A second download-then-run would capture bytes no launch produces, and running the tool would capture its first-run state |
| <a id="slice-4a-row"></a>slice-4a | Materialize stays in the launcher, as reflink → hardlink → copy | Reflink crosses the bind-mount gap `link(2)` cannot, so there is no need to move materialize host-side and lose "a tool never invoked costs nothing" |
| <a id="slice-4e-row"></a>slice-4e | No index; scan the receipts beside each entry | An index is a second record admit and reap must keep true, and a stale one is a wrong answer |
| <a id="slice-4f-row"></a>slice-4f | The capture jail is suppressed at the mount (`CapturesDir` returns `""`), and the same switch suppresses auto-capture | A launcher exception would be a second rule; a capture of an already-captured program must record fresh installer bytes |
| <a id="record-platform-row"></a>record-platform | A [record beside the entry](#record-platform), not only the manifest, licenses the platform a materialize places for; the store key stays content-only. **Owner-approved direction, 2026-10-29** | Keying by platform too would re-key every existing entry. Bytes identical across platforms otherwise selected by one platform's record and were refused by the other platform's manifest on every relaunch (seen natively on a Mac: [lab report §4](../lab-reports/2026-10-29-mac-native-witnesses-apple-prune-and-copilot-probe.md#4-also-seen)). The record is the same evidence selection already trusts, so no integrity check beyond the platform label moves |
| <a id="slice-5-row"></a>slice-5 | The reap is the complement of `capture.Select`, `K = 1`, no age floor, no flag | Under reflink `st_nlink` says nothing about referrers, and no workspace enumeration is a safe oracle; deriving the reap from the reader makes them unable to drift. The marker already hides an in-flight entry, and the store is not a version history |
| <a id="slice-6b-row"></a>slice-6b | A `macos-user` capture stages on neutral ground, and the move into the store refuses on `EXDEV` | The sandbox account must not reach the invoking user's home or the rest of the store; a multi-gigabyte copy is not an honest fallback |
| <a id="slice-6e-row"></a>slice-6e | The file-content scan is opt-in (`--scan-content-refs`), passed by the `macos-user` capture, the host capture and the container auto-capture | It is a read pass over the whole delta; a capture without it records `relocatable:false` for want of the scan, never a wrong `true` |
| <a id="H2"></a>H2 | Plain-bytes substitution, a new inode per rewritten file, no length check | Matches the recording scan exactly; an in-place edit on the hardlink arm would rewrite the store; every binary reference already refuses |
| <a id="H4"></a>H4 (b) | A `macos-user` launch reads a sealed host-owned copy of the selected entries under the backend's staging root. **Implementation decision under the maintainer's 2026-10-04 delegation, reversible; the maintainer may revisit it** | No new Seatbelt rule; the backend's established staging pattern; the store is not a confidentiality boundary (every podman jail reads it whole). Reading the store in place (a) needs an allow under `/Users` homes; retiring materialize there (c) contradicts "macos-user too" |
| <a id="H3"></a>H3 | A capture does not provide `packages:` | A native nix build to run one shell script is the wrong default |
| <a id="schema"></a>schema | Additive manifest fields only, each defaulting to the fail-safe reading; `ManifestSchema` stays 1 | An older reader that ignores them materializes only into the capture's own home, which is what it did before. Stop and ask before adding per-entry metadata a later yolo must parse |

## Current values

Verified at `8e65f3444`. The prose above explains what each is for.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Store root | `~/.local/share/yolo-jail/captures` | `paths.CapturesDir`, `paths.CapturesDirUnder` |
| Completion marker | `.yolo-capture-complete` | `capture.completeMarker` |
| Entry key | first 16 hex of sha256 over the tree digest | `capture.Key` |
| Manifest schema | 1 | `capture.ManifestSchema` |
| Capture surfaces | `.npm-global`, `.local`, `go`, plus `.codex/packages/standalone` | `paths.HomeSurfaces`, `paths.InstalledProgramSurfaces` |
| Excluded from a delta | `.local/share/yolo-jail`, `.local/state/yolo-jail-daemons` | `capture.DefaultExcludes` |
| Binary sniff | a NUL in the first 8000 bytes | `capture.binarySniff` |
| Container store mount | `:ro` at `/ctx/captures`, named by `YOLO_CAPTURES_DIR` | `run` (`captures.go`), `entrypoint.CapturesDirEnv` |
| Install-only switch | `YOLO_INSTALL_ONLY=1` | `entrypoint.InstallOnlyEnv` |
| Auto-capture hatch | `YOLO_NO_AUTO_CAPTURE` (any non-empty value) | `cli.NoAutoCaptureEnv` |
| Auto-capture back-off | first retry after 24h, doubling, capped at 7 days, per yolo version | `cli.autoCaptureFirstBackoff`, `cli.autoCaptureMaxBackoff` |
| Back-off memo | `<CapturesDir>/auto-capture-failures/<bin>@<platform>.json` | `capture.autoFailuresLeaf` |
| Capture lock | `<global storage>/locks/capture-<bin>.lock` | `cli.captureLockPath` |
| Host-capture origin mark | `+host` suffix on the platform | `cli.hostOriginMark` |
| `macos-user` capture staging | `/Users/Shared/yolo-captures` | `macosuser.CaptureRootDefault` |
| `macos-user` staged store | `/Users/Shared/yolo-staged/captures` | `macosuser.StagedCapturesRoot`, `macosuser.StagingRoot` |
