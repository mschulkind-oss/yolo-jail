---
title: "One program, two builds — should the installed launcher and the jail's copy be the same bytes?"
stage: BUILT
status: accepted
next: "Verify on a real Linux and Mac host that `just install` installs the bundle's own yolo, then graduate into self-update.md"
tags: [install, self-update, builds, provenance]
summary: "The installed launcher and the copy a jail runs come from one frozen snapshot but differ by two linker stamps, and only the from-source update channel reads them."
---

# One program, two builds — should the installed launcher and the jail's copy be the same bytes?

**Status:** 2026-10-23, built. All three rulings in [§8](#8-open-questions) are implemented, and [§9](#9-decision-ledger) records them with the implementation decisions they needed. `version.SourceDir`, `version.SourceBranch` and the source channel are deleted, a `just install` binary classifies as a snapshot install, and an install compiles `yolo` once. Sections 1 to 7 describe the tree before the build. Their claims were verified against `d4d3ff0d2`, so their line anchors name code that no longer exists. The measurements in [§2](#2-reproduction-and-the-one-variable) were taken in this checkout. [§1](#1-the-reported-asymmetry) is a host report and is marked as one. Not yet verified on a real host: `matched-bundle.py install` refuses inside a jail, so the installed launcher's bytes were checked only by the assemble test.

> **In short.** `just install` builds `yolo` twice from one frozen snapshot, and the two builds differ by exactly two linker stamps that only the from-source update channel reads. The expectation that one snapshot should have one set of bytes is already true in the development channel, and would be true for an install the moment those two values stop living inside the artifact.

**Why it matters.** An install that reports two different digests for one commit cannot be checked by digest, so the natural staleness test — *is the jail running what I installed?* — reports a correct install as broken. The stronger concern is the principle: an install is supposed to be a snapshot, and a binary that still points at its checkout is not one.

**The shape.** Two artifacts from one snapshot — the **installed copy** on `PATH` and the **bundle copy** a jail mounts — differing in `SourceDir` and `SourceBranch` alone, which the from-source [update channel](../reference/self-update.md#install-channels) reads to find the checkout it pulls and redeploys.

**Cost.** Moving the pointer out of the artifact costs a new on-disk provenance contract and makes a binary copied away from its installation unable to name its checkout; dropping the pointer costs the automatic half of the from-source channel. Doing nothing leaves the asymmetry misread as a defect.

**Start at [§4](#4-the-collision)** — the collision between "an install is a snapshot" and "a from-source install tracks a checkout". Every option in [§6](#6-options) is an answer to it.

**Needs your ruling:** nothing open. **Ruled 2026-10-23:** [OQ-IP1](#OQ-IP1), [OQ-IP2](#OQ-IP2), [OQ-IP3](#OQ-IP3).

**Reads with:** [`matched-development-bundles.md`](../reference/matched-development-bundles.md) (owns the matched-artifact contract and the development wrapper), [`installed-launcher-provenance-plan.md`](installed-launcher-provenance-plan.md) (the implementation sketch, now built), [`self-update.md`](../reference/self-update.md) (owns the install channels and what `yolo update` does on a from-source install), [`host-execution-from-the-workspace.md`](../reference/host-execution-from-the-workspace.md) (rules what a checkout a jail can write may do on the host), [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md) (owns the mounted prefix).

---

## 1. The reported asymmetry

Two runs of the same command, same machine, minutes apart. Reported 2026-10-23 from a host shell in a `~/.dotfiles` workspace and from inside that workspace's jail:

| Where | Version | Build stamp | Commit | `binary sha256` |
| :--- | :--- | :--- | :--- | :--- |
| Host (`which yolo`) | `0.14.0+591.ga866b30b2d0c` | `v0.14.0-591-ga866b30b2d0c` | `a866b30b2d0c` | `788db5da…` |
| In jail (`yolo -- bash -lc 'yolo --version --full'`) | `0.14.0+591.ga866b30b2d0c` | `v0.14.0-591-ga866b30b2d0c` | `a866b30b2d0c` | `8caa07df…` |

Every identity field agrees. Only the digest differs, and the digest is the one line that invites the comparison: it sits directly under `commit:` and reads like a second, rougher identity. The owner's reading — *one commit, so it should be one binary; something is stale or wrong* — is the reading that line invites, and nothing in the output contradicts it.

The finding below is that the asymmetry is real, deliberate, and much narrower than it looks: the two files are the same program built from one frozen snapshot, differing by two `-X` values.

## 2. Reproduction, and the one variable

Measured in this checkout, each pair built from the same source with the same `VERSION`/`COMMIT`/`SNAPSHOT_IDENTITY`/`BUILD_KIND`:

| Fixture | `SourceDir` / `SourceBranch` | sha256 |
| :--- | :--- | :--- |
| Installed copy (as `just install` builds it) | `/home/matt/code/yolo-jail`, `main` | `2a012874…` |
| Bundle copy (as the bundle build gets it) | unset | `6a8f90a5…` |
| Control — identical flags, built twice | unset | `0649a0a3…`, then `0649a0a3…` |

So the two differ, the control says the build itself is deterministic, and the variable is exactly those two stamps. A real `matched-bundle.py build-dev` run confirms the shape end to end: for **`build kind: development`**, the launcher and the copy the jail mounts are **byte-identical** — `bin/yolo` and `share/yolo-jail/bin/linux-amd64/yolo` both hash to `877718db…`.

That is the load-bearing measurement: **the asymmetry belongs to the install channel alone.** The development channel — the one that is deliberately coupled to the checkout — already produces one binary.

One confirming check on the jail side: this session's `/opt/yolo-jail/bin/yolo` is `e324b5a0…` and contains no occurrence of `/home/matt/`, i.e. it is the bundle build with the stamps absent.

## 3. Where the two copies go

`matched-bundle.py install` assembles one output directory and then sends it two ways ([`matched-bundle.py:194-228`](../../scripts/matched-bundle.py), [`matched-bundle.py:464`](../../scripts/matched-bundle.py), [`matched-bundle.py:472`](../../scripts/matched-bundle.py)):

```
assemble(snapshot, …, build_kind="installed")
 │
 ├── build A: build-go.sh with SOURCE_DIR + SOURCE_BRANCH       ← set only for build_kind=installed
 │     output/bin/yolo ─────────────────────► $GOBIN/yolo
 │
 └── build B: stage-source-bundle.sh, which POPS both          ← matched-bundle.py:219-220
       output/share/yolo-jail/bin/linux-<arch>/yolo ─► published as the flake generation
                                                            ─► bind-mounted :ro at /opt/yolo-jail/bin
```

The bundle's layout is the published generation's root, so the copy a jail executes is the file the bundle shipped, mounted twice under different paths ([`jailprefix.go:72-79`](../../internal/cli/run/jailprefix.go), and the launch line it prints at [`jailprefix.go:100`](../../internal/cli/run/jailprefix.go) names the generation).

Four stamps are baked by [`build-go.sh`](../../scripts/build-go.sh), and the split is what matters here:

| Stamp | In the installed copy | In the bundle copy | Read by |
| :--- | :--- | :--- | :--- |
| `SnapshotIdentity` ([`version.go:44`](../../internal/version/version.go)) | yes | yes | the launch-time identity check |
| `BuildKind` ([`version.go:48`](../../internal/version/version.go)) | yes | yes | the launch-time identity check, and update-channel classification |
| `SourceDir` ([`version.go:31`](../../internal/version/version.go)) | **yes** | no | **the from-source update channel only** |
| `SourceBranch` ([`version.go:39`](../../internal/version/version.go)) | **yes** | no | **the from-source update channel only** |

`version.SourceDir` says this itself: stamped by that recipe "and by nothing else", because "a path on the build machine means nothing on the machine the binary lands on". Every distribution build leaves it empty — the release `.goreleaser.yaml` ldflags do not set it — so the installed copy from a from-source `just install` is the only artifact in the system that carries it.

The two host-only stamps are what the identity check does *not* cover: the check compares `SnapshotIdentity` and `BuildKind` against `yolo-build-stamp.json` ([`reporoot.go:76`](../../internal/reporoot/reporoot.go)), and the install-time pair validation compares the same two plus version and commit ([`matched-bundle.py:238-268`](../../scripts/matched-bundle.py)). Neither compares bytes, so two builds can be a valid matched pair while differing in the digest — which is exactly the observed state.

## 4. The collision

Two positions, both defensible, and they are what [OQ-IP1](#OQ-IP1) has to choose between.

**P1 — an install is a snapshot.** `just install` freezes the source and installs it; afterwards nothing in the checkout may affect the host. Checkout-coupled work has its own channel: the development wrapper, invoked by path, which builds from the live checkout on demand without touching the installed command ([`matched-development-bundles.md`](../reference/matched-development-bundles.md)).

**P2 — a from-source install is a channel that tracks a checkout.** It is the only install channel that can update itself in place: the source check reads the checkout's branch and upstream, and the apply runs `git pull --ff-only` and then `just deploy` there ([`self-update.md`](../reference/self-update.md#install-channels), [`self-update.md`](../reference/self-update.md#from-source-installs)). Every other channel is found by a marker the distribution left behind — a Cellar path, a pipx receipt, a bundle beside the binary. A `git clone` plus `just install` leaves no marker at all, so `SourceDir` *is* that channel's marker.

P2 is why the stamps exist, and it is not a small feature: without a marker, `selfupdate.Detect` cannot classify the binary as a source install at all, and reports the moved-checkout state instead ([`channel.go:177-183`](../../internal/selfupdate/channel.go)).

The collision is real, and it is settled today by putting the pointer in the *installed* copy and not in the *shipped* one. That is precisely the asymmetry in [§1](#1-the-reported-asymmetry) — so the two positions are not actually in conflict about the artifact that ships. They conflict about the artifact that stays.

**The separation worth noticing.** P2 needs provenance *discoverable at the install*. It does not need provenance *baked into the executable*. Those came apart the moment two artifacts were built from one snapshot, and every option in [§6](#6-options) is a different answer to where provenance lives.

> [!IMPORTANT]
> **The security half is already ruled, and this document does not reopen it.** A checkout that a jail can write is the product cell, and updating from it runs what the jail wrote, by design: "building a checkout runs its contents, and yolo adds no guard there" ([`host-execution-from-the-workspace.md`](../reference/host-execution-from-the-workspace.md)). Whatever [OQ-IP1](#OQ-IP1) decides, it decides where a pointer lives — not whether a from-source update is trusted.

## 5. What is guaranteed, and what is not

| Property | Status |
| :--- | :--- |
| One snapshot ⇒ one version, commit, snapshot identity and build kind in both copies | **Guaranteed and checked** — a mismatch refuses at launch with the install remedy ([`reporoot.go:76`](../../internal/reporoot/reporoot.go)) |
| The jail runs the bytes the bundle published | **Guaranteed and checked** — the integration test compares the jail's reported digest against `bin/linux-<arch>/yolo` in the selected bundle ([`versionstamp_test.go:179-186`](../../integration/versionstamp_test.go)) |
| The installed copy and the bundle copy are the same bytes | **Not guaranteed, not checked, and not required** |

The digest's own documented purpose is the last row: it "tells two binaries apart even when they were stamped from the same commit (a dirty build, or one built from another checkout)" ([`identity.go:18`](../../internal/version/identity.go)). The line exists to distinguish builds, not to confirm them — so treating a mismatch as evidence of a bad install inverts what it is for.

## 6. Options

| # | Option | What it changes | Verdict |
| :--- | :--- | :--- | :--- |
| A | Keep the two builds | nothing | Baseline. Leaves an install whose digest cannot be compared, and an output line that reads as a defect |
| B | Move provenance into the installation | the two copies become one build; the source channel reads provenance from the install instead of from its own bytes | **Viable** — satisfies P1 without deleting P2's channel; costs a new on-disk contract, and a binary copied out of its installation stops being self-describing |
| C | Drop the pointer — a from-source install is a snapshot | no automatic source check or apply; `yolo update --from <checkout>` remains as the explicit path | **Viable and smallest** — makes P1 exactly true by deleting the channel half that needs the pointer; its heaviest user is the maintainer's own loop |
| D | Bake the stamps into the shipped bundle too | both copies carry provenance | **Rejected** — puts a host path into every artifact that ships, and a stray value outranks every distribution marker in `Detect` ([`channel.go:177-183`](../../internal/selfupdate/channel.go)), so a Homebrew or pipx install would stop identifying itself |
| E | Keep the asymmetry, make it legible | print the source dir and branch in `--version --full`; say in the release notes that a digest identifies a build, not a snapshot | **Viable, cheap, and not an answer to P1** — the fallback if B and C are both refused, and worth doing alongside either |

**B in one paragraph.** Provenance moves to a per-install record keyed by the binary's own snapshot identity, in state yolo already keeps outside the jail (the install's stamp file, or a sibling of it). `assemble` stops setting `SOURCE_DIR`/`SOURCE_BRANCH`, so both destinations receive one build; `selfupdate` resolves provenance through the install rather than through its own bytes. The cost is a real behavior loss and should be stated as one: today the installed binary knows where it came from wherever it is, and afterwards only an installation's binary does — a copy taken elsewhere degrades to the message the code already has for exactly this case ([`update.go:420-422`](../../internal/cli/update.go)). The storage shape belongs to the sketch; the constraint that matters here is that provenance must not enter `share/yolo-jail`, which is both the shipping tree and what a jail mounts.

**C in one paragraph.** `SourceDir` and `SourceBranch` are deleted; the from-source channel keeps no marker, so a from-source install classifies as an installed snapshot. `yolo update` still works from a checkout you name explicitly, and the `--from` recovery already exists for exactly that shape. This is the option that makes the owner's model literal: `just install` installs a snapshot, and the only way a checkout reaches the host afterwards is the command that names it. Its cost is that a maintainer tracking `main` must now say `--from` every time, or drive the development wrapper.

**Why D is not merely aesthetically wrong.** `stamped` requires only a non-empty `SourceDir` and commit, and the `case stamped:` arm returns before the Cellar, pipx, uv and archive arms ([`channel.go:177-183`](../../internal/selfupdate/channel.go)). A shipped binary carrying the stamp would therefore be misclassified on every machine but the build machine — and *this* is the trap to preserve: the intuitive fix ("make them match by putting the stamp in both") is the one that breaks update-channel detection for everyone else.

## 7. What this does not license

- **Not a `YOLO_REPO_ROOT` revival.** That variable no longer selects a launch source ([`matched-development-bundles.md`](../reference/matched-development-bundles.md)), and provenance is not a route back to it.
- **Not a weaker identity check.** `reporoot` must keep refusing a launcher whose identity does not match the bundle it would pin.
- **Not a change to how a jail receives its helpers.** The `:ro` bind of the generation, and what the image does or does not bake, stay with [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md).
- **Not a new path from a jail to the checkout.** No option here gives a jail a writable route to the host's source tree, and none changes what a from-source update trusts.
- **Not a reaper or a retention change.** Nothing here touches when a generation or a development bundle is collected.

## 8. Open Questions

1. <a id="OQ-IP1"></a>✅ **[`OQ-IP1`](#OQ-IP1): Does a from-source install keep a pointer to its checkout, and may it live inside the executable?**

   This decides whether the two copies are one build, and so whether a digest can check an install.

   - **A — Keep it in the executable.** Today's shape. Self-describing wherever it is copied; the two copies stay different bytes.
   - **B — Move the pointer into the installation.** One build for both destinations; the channel resolves provenance through the install. A binary copied out of it can no longer name its checkout.
   - **C — Drop the pointer.** A from-source install is a snapshot: no automatic source check or apply, and `--from` names a checkout explicitly.

   _Leaning:_ B — it makes the owner's principle true without deleting the channel, and the only thing lost is a binary's self-description outside its installation, which already has a defined degraded message.

   **Answer:**
   > **Ruled in review 2026-10-23: C.** *"There's absolutely no way yolo update should do something
   > special if it was installed from a checkout and you use the just install command."* `just
   > install` installs a snapshot, the same as a release downloaded at that commit. Behavior that
   > follows a checkout belongs to the development wrapper, which knows its checkout because it is
   > symlinked there. `SourceDir` and `SourceBranch` are deleted with the source channel; `yolo
   > update --from <checkout>` stays as the explicit way to name one.

2. <a id="OQ-IP2"></a>✅ **[`OQ-IP2`](#OQ-IP2): Is byte equality between the two copies a checked invariant, or only an outcome?**

   If it is checked, a future build step that reintroduces a divergence fails at install time instead of surfacing as a mysterious digest mismatch months later. If it is not, the property holds only as long as nobody adds a stamp.

   - **A — Assert it at install time.** The pair validation compares the two files' bytes and refuses on a mismatch.
   - **B — Assert it in the test suite only.** The integration suite pins it; an install does not pay for the check.
   - **C — Do not check it.** Document the property and rely on review.

   _Leaning:_ A — the property is the entire point of the change, and an unasserted invariant is the one that silently stops holding.

   **Answer:**
   > **Ruled in review 2026-10-23, none of the options:** *"I don't think it should be checked,
   > because it should be structurally in such a way that it can never be false. Like, you would
   > only ever be checking the same inode against the same inode."* So an install builds `yolo`
   > once for the host's platform, and the installed launcher is that one artifact, the same file
   > the bundle ships for that platform. There is no second build to diverge, so there is nothing to
   > compare. Where the host and the jail differ in platform (a Mac host and a Linux container),
   > the two are different programs by construction, and no digest comparison between them means
   > anything.

3. <a id="OQ-IP3"></a>✅ **[`OQ-IP3`](#OQ-IP3): Does code keep honoring the stamps baked into already-installed binaries?**

   Installs in the field carry `SourceDir` today. A runnable answer is needed for the day the writer stops setting it, or the first such binary gets classified by whatever the reader happens to do with a value it can no longer see.

   - **A — Keep reading them, stop writing them.** Existing installs keep their source channel until they are reinstalled.
   - **B — Read them only when the install has no provenance record.** A new record wins; the baked value is a legacy fallback.
   - **C — Ignore them.** Every existing from-source install loses its source channel at the next update.

   _Leaning:_ A — the value costs nothing to read, and dropping it would silently strand the installs in the field.

   **Answer:**
   > **Ruled in review 2026-10-23: moot, closed.** *"I can't identify any reason why this needs to
   > be there."* No new binary carries the stamps, and an old binary runs its own old code, whose
   > update installs the new build. The stamp reader is deleted with no transition.

## 9. Decision ledger

| ID | Ruling / Decision | Date | Built |
| :--- | :--- | :--- | :--- |
| OQ-IP1 | **C: an install is a snapshot.** `SourceDir`, `SourceBranch`, their linker stamps and the source channel are deleted. `yolo update --from <checkout>` remains the explicit way to deploy a named checkout. | 2026-10-23 | `internal/selfupdate`, `internal/cli/update.go`, `scripts/build-go.sh`, `scripts/matched-bundle.py` |
| OQ-IP2 | **One build, structurally.** `assemble` stages the bundle first, and on Linux copies the bundle's `bin/linux-<arch>/yolo` to `bin/yolo`. On macOS the bundle's darwin set is the guest helpers only, so the launcher is the one darwin build of `yolo`, and the macos-user guest stages that same running launcher. Nothing compiles `yolo` twice, and no byte comparison is added. A Linux container on a Mac runs the bundle's Linux `yolo`, a different program by nature. | 2026-10-23 | `scripts/matched-bundle.py`; regression test `test_assemble_builds_yolo_once_and_the_launcher_is_the_bundles_copy` |
| OQ-IP3 | **Moot.** The stamp reader is deleted with no transition. | 2026-10-23 | `internal/selfupdate/channel.go` |
| IP-D1 | *(implementation decision, the owner's)* **What a snapshot install's `yolo update` does.** A binary whose `BuildKind` is `installed` and that has no other channel marker is a snapshot install. `yolo update` and `--check` on it change nothing: they print that there is no release to compare against, and the two next steps, `just install` in the checkout or `yolo update --from <checkout>`. `--check` exits 0, and a plain update exits 1 because it installed nothing. It gets no background check, notice or launch offer. It is never classified as `go install`, as an archive, or as unknown. `yolo check` shows the same steps. | 2026-10-23 | `selfupdate.KindSnapshot`, `updateMain`, `sectionUpdates` |
| IP-D2 | *(implementation decision, the builder's; reversible)* **What `--from` does now that no channel tracks a checkout.** `--from` is accepted only on a snapshot install. It runs `just deploy` in the named checkout exactly as it stands, pinned to the running binary's directory through `GOBIN`, with no fetch, pull, branch or upstream check. It refuses uncommitted changes unless `--autostash`, which stashes, deploys and restores. `--check --from` prints the deploy and changes nothing, and `--autostash` without `--from` is refused. | 2026-10-23 | `selfupdate.DeployCheckout`, `deployFrom` |

## 10. Terms

**Installed copy** *(coined here)* — the `yolo` executable a host install places on `PATH` (`$GOBIN/yolo`), which starts jails and is the copy that can update itself. Not the bundle copy, and not the development wrapper.

**Bundle copy** *(coined here)* — the same program as it exists inside a published bundle at `bin/linux-<arch>/yolo`, which is the file a jail binds read-only and executes. It is the bytes an install ships; the installed copy is the bytes an install runs.

**Provenance** *(coined here)* — which checkout a binary was built from, as an absolute path and a branch. Not identity: two builds with different provenance are the same snapshot, and two builds with the same provenance can be different snapshots. This document uses the word only for the `SourceDir`/`SourceBranch` pair.

**Snapshot identity** — the content hash of the frozen source a build came from, which distinguishes two builds of one commit. Defined in [`matched-development-bundles.md`](../reference/matched-development-bundles.md#the-source-snapshot-and-its-identity); the value itself is stamped at [`version.go:44`](../../internal/version/version.go).

**Matched bundle** — one immutable build output pairing the host launcher with the helpers and flake it selects. Coined in [`matched-development-bundles.md`](../reference/matched-development-bundles.md); this document adds nothing to it.
