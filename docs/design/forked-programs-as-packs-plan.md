---
title: "Forked programs as packs — implementation plan"
date: 2026-09-21
status: accepted
tags: [plan, packs, programs, capture, forks]
summary: "Build hand-off for the source-built program route: a fork declared as a program with via source, pinned in its own lock by the explicit pack verbs, built once per platform in a sealed capture jail, recorded under a new receipt kind, and delivered to the jail by an entry key the host hands over. Promoted against the tree 2026-09-30; nothing built. The design wins on behavior."
vantage:
  status-chip: true
---

# Forked programs as packs — implementation plan

**Status:** DECIDED, 2026-09-30 — promoted against the tree at `4c3d6a85`. Steps 1–6, the jail
notch, were built on 2026-09-30. Step 7, the host notch, stopped at its measurement, which needs
what the building jail could not do: [Step 7 needs](#step-7-needs) records exactly what. The
implementation choices this promotion made are
[FP-D4](forked-programs-as-packs.md#FP-D4)–[FP-D9](forked-programs-as-packs.md#FP-D9) in the
design's ledger, and this file assumes them.

**Design:** [`forked-programs-as-packs.md`](forked-programs-as-packs.md). **Precedence:** the
design wins on behavior, the tree wins on fact, and this file is advice and the first thing here
to be wrong. Never twist the code to match it; correct it in the commit.

**Reads with:** [`install-capture.md`](../plans/install-capture.md) (the capture store as built,
and its traps), [`host-tool-provisioning.md` §4](host-tool-provisioning.md#4-when-provisioning-runs)
(the lock [FP-D1](forked-programs-as-packs.md#FP-D1) copies).

## What the promotion corrected

Every sketch line was re-read against the tree on 2026-09-30. These were wrong or stale:

- `knownVias` is at `contributes.go:811`, not `:496`, and a third value reaches every
  `Install.Kind` switch in the [Map](#map), not three places.
- The capture jail's store suppression is `capturehost.go:337` (`opts.CapturesDir`), not `:313`.
- **The capture jail does receive `env_sources`, `host_files` and loopholes today.** The sketch
  listed their absence as a property to pin; it is work, step 4. This file calls that work **the
  seal** *(coined here)*: [FP-D9](forked-programs-as-packs.md#FP-D9)'s rule that a build launch
  hands the jail no credential and nothing that writes outside its own workspace and home. A
  **crossing site** *(coined here)* is any place in the run pipeline that hands a jail something
  of the host's: an env pair, a bind, a started host service.
- *"The loser adopts"* was wrong: `captureHost`'s lock refuses. The launch path waits
  ([FP-D1](forked-programs-as-packs.md#FP-D1)); `yolo capture` keeps refusing.
- `packsrc.Parse` takes a bare remote (`git+https://host/org/fork?ref=main`, empty `Path`) and
  `git+file://` (`parseGit`); `file://` is refused as a fork source
  ([FP-D5](forked-programs-as-packs.md#FP-D5)).
- `packsrc` no longer keeps resolution off the launch (`refresh.go`, 2026-09-25), so fork sources
  stay off its refresh ([FP-D7](forked-programs-as-packs.md#FP-D7)), which also settles the
  sketch's open lock question.
- The relocation measurements are still unmade, and they now gate only the host notch (step 7):
  the jail notch never relocates ([FP-D4](forked-programs-as-packs.md#FP-D4)).

## Map

| Path | Change |
| :--- | :--- |
| [`packdecl/contributes.go`](../../internal/packdecl/contributes.go#L811) | a `source` row in `knownVias`; `fork_of`, `source`, `build`, `produces` on `Contribution`; the program arm of `validateContribution` (`:3257`); `InstallContributions` (`:942`) projects the four and skips a `fork_of` contribution; `selfInstallCommand` (`:1035`) returns `""` for it |
| [`packdecl/packdecl.go`](../../internal/packdecl/packdecl.go#L193) | `Install` gains the four, plus `ForkedBy`, naming the fork pack for provenance |
| `packload/forks.go` | **new**: the set-level rewrite and its refusals ([FP-D5](forked-programs-as-packs.md#FP-D5), [FP-D6](forked-programs-as-packs.md#FP-D6)) |
| [`packload/needs.go`](../../internal/packload/needs.go#L61) | `ResolveNeeds` joins a shipped base that `fork_of` names |
| [`packload/footprint.go`](../../internal/packload/footprint.go#L417) | program arms of `FootprintOf` (`:425`) and `DisclosureSentence` (`:135`); `agentNameClaims` (`:1136`) skips `fork_of` |
| [`config/packselection.go`](../../internal/config/packselection.go#L114) | `SelectPacks` runs the rewrite after the closure; a refusal is `ClosureErr` |
| [`entrypoint/packsurfaces.go`](../../internal/entrypoint/packsurfaces.go#L66) | `LoadJailPacks` runs the same rewrite over the staged set |
| `packsrc/forklock.go` | **new**: `forks.lock.json` ([FP-D7](forked-programs-as-packs.md#FP-D7)) |
| [`cli/pack.go`](../../internal/cli/pack.go#L1432), [`cli/packupdate.go`](../../internal/cli/packupdate.go#L101) | `packInstall` pins, `packUpdate` re-resolves, `packStatus` (`:1580`) reports pin, drift and what is built |
| `cli/forkbuild.go` | **new**: the build act — lock, stage, checkout, sealed jail, `produces` check, admit, receipt |
| [`cli/capturehost.go`](../../internal/cli/capturehost.go#L318) | `runCaptureJail` takes the argv and the seal; `resolveCaptureTarget` (`:230`) hands a forked bin to the build act |
| [`cli/capturematerialize.go`](../../internal/cli/capturematerialize.go#L99) | a `--key=` mode; `captureRecords` (`:214`) reads both receipt kinds |
| [`entrypoint/capturereceipt.go`](../../internal/entrypoint/capturereceipt.go#L81), [`receiptread.go`](../../internal/entrypoint/receiptread.go#L162) | kind `build` and fields `revision`, `recipe`, `toolchain` in the writer, in `parseReceiptLine`, and in a reader beside `ReadCaptureReceipts` |
| [`capture/select.go`](../../internal/capture/select.go#L41) | `Program` and `Record` gain the source address ([FP-D8](forked-programs-as-packs.md#FP-D8)); [`gc.go`](../../internal/capture/gc.go#L157) needs no rule of its own |
| `cli/run/forkbuild.go` | **new**: which forks a launch needs, the `BuildForks` call, the fork argv pair, the `macos-user` line |
| [`cli/run/run.go`](../../internal/cli/run/run.go#L805) | the call beside `autoCaptureInstallerPrograms`; the `macos-user` line above that arm's return (`:767`) |
| [`cli/run/runcmd.go`](../../internal/cli/run/runcmd.go#L461) | `Options.BuildForks` beside `AutoCapture`, and the seal switch |
| [`cli/run/assemble.go`](../../internal/cli/run/assemble.go#L868) and every crossing site | the seal; the fork pair beside `capturesArgs` |
| [`cli/commands.go`](../../internal/cli/commands.go#L1177) | inject `BuildForks` beside `AutoCapture` |
| [`entrypoint/shims.go`](../../internal/entrypoint/shims.go#L437) | a `source` arm and launcher template; the env name beside `CapturesDirEnv` (`:1447`) |
| [`entrypoint/catalog.go`](../../internal/entrypoint/catalog.go#L228) | a fork's outputs join both declared sets (`:237`, `:366`) |
| [`hostfloor/floor.go`](../../internal/hostfloor/floor.go#L296), [`ensure.go`](../../internal/hostfloor/ensure.go#L211) | step 1: `noEntryReason`'s `source` arm; step 7: the `declared`, `via`, install and `newerThan` arms |
| [`hostfloor/lock.go`](../../internal/hostfloor/lock.go#L33) | `acquire` becomes a helper both callers share, with an optional deadline |
| [`cli/hostfloor.go`](../../internal/cli/hostfloor.go#L357) | `noCopyWhere` names the fork case |
| `integration/forkbuild_test.go` | **new** |

## Reuse

- **`captureHost`** ([`capturehost.go:97`](../../internal/cli/capturehost.go#L97)) is the whole
  act: stage inside the store, run the jail, read the manifest, refuse an empty delta,
  `AdmitEntry`, append the receipt. The build act is that sequence with another target, argv and
  receipt. Lift the middle into one function both call, and pass `runCaptureJail` its argv.
  *Advice:* a copy is the two-implementations drift AGENTS.md keeps deleting.
- **`captureJailArgv`** (`:303`): keep `--out`, `--surface-root` and `--scan-content-refs`, and
  change only what follows `--` to `env YOLO_BYPASS_SHIMS=1 bash -c '<cd into the source && build>'`,
  built with `shquote.Join`. The full scan is what the host notch reads
  ([FP-D4](forked-programs-as-packs.md#FP-D4)).
- **`packsrc.Store.Sync`** ([`store.go:329`](../../internal/packsrc/store.go#L329)) is the pin:
  fetch, then resolve a commit. Its doc says no production path calls it; the fork pin is that
  caller. **`Store.Materialize`** (`:482`) checks the commit out; copy the tree into the staging
  workspace, because a tree in the pack store is shared and never written again.
- **`packsrc.Lock`**: `LoadLock`, `Save` and `WithLock` are the discipline for the fork lock — a
  newer schema refused, temp file and rename, a flock around the rewrite.
- **`hostfloor.acquire`** ([`lock.go:33`](../../internal/hostfloor/lock.go#L33)) is
  [FP-D1](forked-programs-as-packs.md#FP-D1)'s shape: a flock, the holder's pid in the file, an
  `onWait` line. Lift it rather than write another: flock helpers already exist in several
  packages (`cli.tryFlockAt` and `packsrc.flockPath` among them).
- **`autoCaptureInstallerPrograms`** ([`run/autocapture.go:56`](../../internal/cli/run/autocapture.go#L56)):
  the slot, the `CapturesDir() == ""` switch that keeps a capture jail from triggering itself, and
  `containerJailPlatform`. **`autoCapture`** ([`cli/autocapture.go:87`](../../internal/cli/autocapture.go#L87)):
  the cost line, and a launch that never fails over it.
- **`capturesArgs`** ([`run/captures.go:55`](../../internal/cli/run/captures.go#L55)): the argv
  pair's shape, and the Apple Container arm's warning.
- **`nativeAgentLauncher`** and `_try_materialize` ([`shims.go:1678`](../../internal/entrypoint/shims.go#L1678))
  are the template to start the source launcher from; `npmAgentLauncher`'s `node_floor`
  interpreter resolution is what a Node fork execs through.
- **`installFromCapture`** in `hostfloor` is step 7's install arm.
- **`noteMacosUserPlatformGaps`** ([`run/loopholeinert.go:425`](../../internal/cli/run/loopholeinert.go#L425))
  is the shape of [FP-D3](forked-programs-as-packs.md#FP-D3)'s line. `backendparity_test.go`
  requires its runtime branch to carry a `// parity: Warned` marker.
- **Fixtures.** `captureFixtureHome`, `fakeCaptureJail` and `withFakeCaptureJail`
  ([`cli/capturehost_test.go:36`](../../internal/cli/capturehost_test.go#L36)) drive the whole act
  with no container. `TestALaunchWiresTheAutoCaptureTrigger`
  ([`cli/autocapture_test.go:272`](../../internal/cli/autocapture_test.go#L272)) invokes an
  injected closure through `launchRunPipeline`. `goldenOptions`
  ([`run/assemble_test.go:68`](../../internal/cli/run/assemble_test.go#L68)) builds a
  deterministic argv. `stubDeclaredBins` ([`cli/hostdepstub_test.go:127`](../../internal/cli/hostdepstub_test.go#L127))
  stubs any bin a test probes. [`integration/capture_test.go`](../../integration/capture_test.go)'s
  hermetic fixture pack is the integration test's model.

**House style.** Every `Install.Kind` switch gets an explicit `source` arm, never a default: a
declined program says why (`GenerateAgentLaunchers`' default arm warns because a silent drop cost
once). Spell the mechanism's three names once, as `packdeclNativeKind` does for the installer:
`via: "source"` → `Install.Kind` `"source"` → receipt `kind: "build"`. Each step's test must fail
when its production call site is deleted (AGENTS.md, Testing).

## Traps

- **The embedded packs are shared.** `packload.Embedded()` hands every caller one `*Pack`, so a
  rewrite in place leaks the fork into later selections in the same process. Copy the `Pack` and
  its `Contributes`. Symptom: a verb that selects twice shows the fork after the config dropped it.
- **A need may not name an unshipped pack, even a selected one** (WB-D9,
  [`needs.go:45`](../../internal/packload/needs.go#L45)). A synthesized `needs` entry for a
  configured base refuses every such fork: join a shipped base inside `ResolveNeeds`, and require
  a configured one in `packs`.
- **The receipt reader and the GC change in the same commit as the first `build` receipt.**
  `captureRecords` keeps only kind `capture`, and `PruneSupersededCaptures` reaps an entry
  nothing attributes. Symptom: `yolo prune --apply` deletes every fork entry, and the next launch
  rebuilds.
- **Without the source address in `Program`, an installer query can select a fork entry** of the
  same bin — a fork of `claude` served to `claude`'s materialize. An installer capture's source
  address is empty.
- **The Node floor installs into the machine-shared `/mise`** (`mise install node@<floor>`,
  `shell.go`), which [`assemble_parts.go`](../../internal/cli/run/assemble_parts.go#L62) binds
  read-write into every jail, `~/.cache` beside it. Sealing either store away leaves the build no
  Node; leaving it shared lets a build plant a Node every later jail runs. The seal binds a
  private directory of the staging workspace at each ([FP-D9](forked-programs-as-packs.md#FP-D9)).
- **The toolchain is not a manifest field** ([§6](forked-programs-as-packs.md#6-identity-what-keys-a-fork-entry)).
  Have the build jail write its image identity (`/etc/yolo-jail-image-identity`) to a file in the
  staging workspace outside `out/`, and read it before `AdmitEntry` renames `out/`.
- **The slot must check Apple Container's read-only floor.** `autoCaptureInstallerPrograms` checks
  only `CapturesDir()`. Below the floor `capturesArgs` mounts no store, so a build there makes an
  entry no jail can read: skip it, and hand the jail that reason.
- **A forked bin has no update mode.** `yolo pack update` runs launchers with `YOLO_PACK_UPDATE`
  (`packupdate.go`); the source launcher treats it as a no-op that says the pin moves the fork, or
  the verb reports a failure for it.
- **`catalog.go`'s autoprune removes what it cannot account for.** A fork under `~/.local/bin` or
  `~/.npm-global/lib/node_modules` is an orphan unless the declared sets include it, and with
  `programs.autoprune` on it is deleted at every boot.
- **An entrypoint that predates this route skips `via: "source"`** (`unknownViaSkip`) and never
  rewrites the base, so the base's upstream program installs in that jail. `version.SourceSkew`
  refuses that launch from source, and a release ships both halves together; know the symptom.
- **The two locks differ.** `captureHost`'s is per bin and refuses; the fork's is per build
  (source address, revision, recipe and platform, the key
  [§6](forked-programs-as-packs.md#6-identity-what-keys-a-fork-entry) names) and waits on the launch
  path. Shared, an installer capture of `claude` and a build of a `claude`
  fork refuse each other.

## What building corrected

Building steps 1–6 found these wrong or incomplete. The choices it made are ledgered in the
design as [FP-D10](forked-programs-as-packs.md#FP-D10) onward.

- Step 1: the fork's claim targets `<bin> (fork of <base>)`, not `<bin> (fork, <pack>)`. The
  reviewer is told which program the fork replaces, and the target still sits outside the
  exclusive loop.
- Step 2: [FP-D6](forked-programs-as-packs.md#FP-D6) names neither `model_catalog`,
  `capabilities`, `platform_regions` nor `unlisted_background_models`, and lets both the base and
  the fork declare `node_floor` ([FP-D10](forked-programs-as-packs.md#FP-D10)).
- Step 4: the seal reaches more crossing sites than FP-D9 lists: ports and forwards, devices, the
  nvim config, the user config copy, the host-services mount, the host briefing and the store-prune
  grant ([FP-D11](forked-programs-as-packs.md#FP-D11)).
- Step 5: the lock moved into a package of its own, `internal/pidlock`, with the bounded wait;
  `hostfloor/lock.go` now wraps it. `runCaptureJail` keeps its name, which AST tests pin, and gains
  the argv and the seal as parameters. `yolo capture` of a fork with no pin refuses, printing the
  launch's pin line and the command that pins it.
- Step 6: the catalog's `go/bin` reader takes the selected packs (`catalogGoBinOrphans(e, packs)`),
  to count a fork's `go/bin` output as delivered. The pin line is not printed in a capture or build
  jail (`noteForkPins` returns when `CapturesDir()` is ""), because the build jail's own run
  pipeline repeated the line its launch had printed. The source launcher takes a `node_floor` the
  way the npm launcher does, including the floor-pending record.
- Step 6: the build line step 1's examples used, `npm install -g .`, leaves the program as a link
  into the build's workspace, which is deleted when the build ends. Such a build now stores nothing
  ([FP-D12](forked-programs-as-packs.md#FP-D12)). The examples in the pack reference, the user
  guide and the `packdecl` test use `npm install -g "$(npm pack --silent)"` instead, and the two
  docs list the package's directory in `produces`.

## Build order

Steps 1–6 are the jail notch, [§11](forked-programs-as-packs.md#11-sequencing) steps 1–2; step 7 is
its step 3. Step 2 unblocks everything after it, because every later reader keys on the rewritten
program. Step 4 lands before step 5's first real build: a build without the seal is the credential
gap [§7](forked-programs-as-packs.md#7-trust-and-what-the-build-may-touch) names. From step 2 until
step 6 a selected fork leaves its bin undelivered, saying so by name; nothing between them is a
release, and the changelog line waits for step 6.

1. **Vocabulary.** The `source` row and its four fields, with validation: `source` through
   `packsrc.Parse` and git transports only; each `produces` entry home-relative, under
   `paths.InstalledProgramSurfaces()`, one of them `<surface>/bin/<bin>` on PATH; beside them only
   `platforms` and `node_floor` ([FP-D6](forked-programs-as-packs.md#FP-D6)). The footprint claim
   targets `<bin> (fork, <pack>)`, so the exclusive loop stays inert, and is review-worthy. Its
   disclosure sentence opens with a direction verb (`RUNS`) and says whose machine, which
   `TestDisclosureSentenceNamesDirectionAndWhoseMachine` checks of every claim in its fixture; add
   a fork claim there. No name claim. Every switch in the [Map](#map) gets a `source` arm that
   declines by name. →
   `go test -short ./internal/packdecl ./internal/packload ./internal/hostfloor ./internal/entrypoint ./internal/cli/...`
2. **Resolution.** `packload/forks.go`, the `ResolveNeeds` join, and the two calls. →
   `go test -short ./internal/packload ./internal/config ./internal/entrypoint`
3. **The pin** — [§11](forked-programs-as-packs.md#11-sequencing) step 1 is done when this lands.
   The fork lock; `pack install`, `update` and `status`; the launch reads the lock only, and prints
   the revision line [OQ-FP6](forked-programs-as-packs.md#14-decision-ledger) rules on and what it
   would build. → `go test -short ./internal/packsrc ./internal/cli ./internal/cli/run`
4. **The seal** ([FP-D9](forked-programs-as-packs.md#FP-D9)): the switch at every crossing site,
   and the narrowed selection. → `go test -short ./internal/cli/run ./internal/cli`
5. **The build act**: `cli/forkbuild.go`, the receipts, `Select`'s source component, the lock with
   its bounded wait, and `yolo capture <forked bin>` as the explicit rebuild. →
   `go test -short ./internal/cli ./internal/capture ./internal/entrypoint ./internal/hostfloor`
6. **Jail delivery** — [§11](forked-programs-as-packs.md#11-sequencing) step 2. The trigger, the
   seam, the argv pair, the source launcher, `capture-materialize --key`, the catalog, the
   `macos-user` line. → the unit packages above, then
   `go test -count=1 -run TestFork ./integration`
7. **The host notch** — [§11](forked-programs-as-packs.md#11-sequencing) step 3. **Measure first**:
   build the motivating fork through step 6, then read its `capture-manifest.json` (`relocatable`,
   `notRelocatable`) and search the tree for `/nix/store` and `/lib`, which the scan does not
   report. Then the `hostfloor` arms. → `go test -short ./internal/hostfloor ./internal/cli`

## Step 7 needs
<a id="step-7-needs"></a>

Stopped 2026-09-30, after step 6, because the measurement that starts step 7 needs three things
the building jail did not have:

1. **The motivating fork's address.** The design's motivating case is "a forked `pi`"
   ([§3](forked-programs-as-packs.md#3-why-this-is-not-an-agent-feature)), and no file in this
   repository names that fork's repository, a ref, its `build` line or its `produces`. The
   measurement needs a fork pack a maintainer actually uses: `source` (`git+https://…?ref=<branch or tag>`), `build`, and
   `produces`, including the package's directory under `.npm-global/lib/node_modules`.
2. **A network fetch.** `yolo pack install` fetches that repository into the pack store's mirror to
   pin it, and a Node fork's build (`npm ci`) fetches its dependencies from the npm registry.
3. **A real build.** A sealed capture jail on a host with podman. From inside a jail that is a
   nested launch, and it writes the machine's capture store under the home.

With those, on a Linux host with podman:

1. Select the fork pack, then run `yolo pack install` and `yolo capture pi`. The last line names the
   entry's root, under `~/.local/share/yolo-jail/captures/entries/`.
2. Read that entry's `capture-manifest.json`: `relocatable`, and every reason under
   `notRelocatable`.
3. Search the entry's `tree/` for `/nix/store` and `/lib` (`rg -l -a -F /nix/store tree`), which
   the reference scan does not report ([FP-D4](forked-programs-as-packs.md#FP-D4)'s warning).
   Native addons under `node_modules` (`*.node`) are where either would appear.
4. If the tree references the image's own paths, stop and ask, as [Ships with](#ships-with) says:
   FP-D4's host notch cannot ship as written.
5. If it does not, build the `hostfloor` arms: `declared`, `via`, `describeRecipe`, `install` and
   `newerThan` in `internal/hostfloor` gain a source arm that materializes the pinned build's entry
   into the floor through the relocating materialize (a moved pin is `Pending`, and no refresh poll
   runs); `noEntryReason`'s source arm, which today refuses by name, becomes the not-relocatable
   reason naming the jail's home; `noCopyWhere` in `cli/hostfloor.go` follows; and
   [`host-tool-provisioning.md`](host-tool-provisioning.md)'s floor table gains the source-built
   row. The tests are [Ships with](#ships-with)'s step 7.

## Ships with

**Tests, unit, by step.**

1. Each required field missing; a `file://` source; a `produces` entry absolute, with `..`,
   outside the surfaces, or none at `bin/<bin>`; `package`, `url`, `update` or `refresh` on a fork;
   `fork_of` without `via: "source"` and the reverse. `AgentNameCollisions` of base and fork is
   empty. `TestViaVocabularyIsOneSet` covers the tolerant skip once the row exists.
2. The rewrite keeps and replaces exactly [FP-D6](forked-programs-as-packs.md#FP-D6)'s lists; each
   refusal (no base, no such program, two forks of one); the embedded base untouched after a fork
   selection; the fork pack's `InstallBins` empty. Call-site pins: a `SelectPacks` test and a
   `LoadJailPacks` test over a staged-tree fixture, each red with its call deleted.
3. Lock round trip, and a newer schema refused. `packInstall` pins once and leaves a pinned fork
   alone when its branch moves (a `git+file://` fixture repo given a second commit); `packUpdate`
   moves it; a launch leaves the lock and the mirror untouched; drift named by `packStatus` and by
   the launch.
4. A `goldenOptions` launch with the seal, under a user config declaring `env_sources`,
   `host_files`, `mounts`, `env`, a loophole pack and a machine-scope shared dir: its `-v`/`-e`
   set equals an allowlist, `/mise` and `~/.cache` are bound from under the staging workspace, and
   the host-service start is never reached. *Advice:* write it as an
   allowlist, so a crossing added later fails it. Plus the call site: the build act's options
   carry the seal (a `fakeCaptureJail` test).
5. Admit plus the `build` receipt's fields; exit 0 with an empty delta, and a delta missing a
   `produces` path, each store nothing; an installer query never selects a fork entry, nor the
   reverse; prune keeps the newest fork entry per source and reaps the older; `ReadCaptureReceipts`
   still skips a `build` line; a launch-path loser waits, then uses the winner's entry (contention
   through the lock's `flock` var); a wait past its bound is that launch's failed build;
   `yolo capture <forked bin>` refuses on contention.
6. `TestALaunchWiresTheForkBuildTrigger`, invoking the closure as its auto-capture twin does; the
   trigger fires on a miss and not on a hit, never in a capture or build jail, and not below the
   Apple Container floor; the argv carries the pair; the source launcher materializes the key, and
   with none prints the reason and exits non-zero without the base's delivery;
   `capture-materialize --key` refuses a key whose record names another bin or platform; the
   catalog lists no fork file; the `macos-user` arm prints its line.
7. With `internal/hostfloor/floortest`: a fork entry materialized into the floor; one not
   relocatable is no entry, naming the jail's home; a moved pin is pending; no refresh poll runs.

**Integration**, step 6: `integration/forkbuild_test.go`. The test makes a `git+file://` fork repo
whose `build` writes a marker script to `~/.local/bin/<bin>`, and a configured fixture base pack
declaring that bin through a `file://` installer; both are selected. First launch: one build, and
the jail runs the fork's marker, not the base's. Second launch: no build. A new commit and
`yolo pack update`: a new entry. The test's entry removed: one rebuild. No real fork, no network.
⚠ In an integration run the capture store is the developer's real one (`capture_test.go` says
why), so the test removes only the entries it added, never the store.

**Rewrite, don't repair:** `TestKnownViasCoversTheShippedMechanisms`
([`viaenum_test.go:82`](../../internal/packdecl/viaenum_test.go#L82)) pins
`viaList() == "npm or installer"`; it moves to the three-value set.

**Docs that describe the old thing**, landing with the step that changes them:

- Step 1: [`install-capture.md`](../plans/install-capture.md#dont)'s *"Don't add a `via` value …
  stays a two-value set"* now points at [OQ-FP3](forked-programs-as-packs.md#14-decision-ledger);
  the `Contribution.Via`, `Install.Kind` and `knownVias` doc comments;
  [`pack-system.md`](../reference/pack-system.md)'s `program` section.
- Step 3: `packUsage` (`yolo pack --help`) for install, update and status;
  [`storage-and-config.md`](../reference/storage-and-config.md) for the new file.
- Step 5: `captureUsage` (`yolo capture --help`), whose *"an npm-declared program … needs no
  capture"* gains the fork case; install-capture's store description gains the second receipt
  kind; the comment above the slot in `run.go` still calls slice 6's rewrite unbuilt, stale since
  H2.
- Step 6: `capturesArgs`' Apple Container line says *"Vendor-installer captures"* and now covers
  forks; the `[^capture]` note in
  [`settings-per-setup.md`](../../userguide/reference/settings-per-setup.md) gains the `macos-user`
  gap; one `CHANGELOG.md` `[Unreleased]` `### Added` paragraph, the first slice a user can notice.
- Step 7: [`host-tool-provisioning.md`](host-tool-provisioning.md)'s floor table gains the
  source-built row.
- The design's Built cells and status line, as each step lands.

**Surfaces:** `forks.lock.json` under `~/.config/yolo-jail`; receipt kind `build`; the jail env
pair, a host↔jail contract `version.SourceSkew` covers, so say so in that commit. No config key.

**Cheap, and yours:** the env var's name and encoding (read once at boot); the recipe hash's
canonical form, as long as it covers `build`, `produces` and the source subdirectory; the staging
id, the lock file name and the wait bound (a named constant with its reason); a `--dir` flag on
`capture-run` or a `cd` in the argv; whether `produces` is validated in `packdecl` or `packload`.

**Stop and ask** if a fork needs a build toolchain the image, `packages`, `mise_tools` and the
base's `node_floor` cannot supply: a pack-declared toolchain is a new contribution the design does
not have. Stop and ask if step 7's measurement finds the motivating fork referencing the image's
own paths: [FP-D4](forked-programs-as-packs.md#FP-D4)'s host notch cannot then ship as written.
Stop and ask before a launch refuses over a fork's pin or build
([§9](forked-programs-as-packs.md#9-failure-modes): a broken fork is one missing tool); the
selection refusals [FP-D5](forked-programs-as-packs.md#FP-D5) names are the only ones.

## Don't

- **Don't add fork sources to the launch's pack refresh** (`run/packrefresh.go`): it re-fetches a
  branch ref at most hourly, the rebuild on a timer
  [§9](forked-programs-as-packs.md#9-failure-modes) forbids.
- **Don't put the toolchain or the revision in the capture manifest**
  ([§6](forked-programs-as-packs.md#6-identity-what-keys-a-fork-entry)).
- **Don't fall back to the base's delivery** when the source launcher has no entry. The installer
  route's download fallback is not this route's: a failed build leaves the program unavailable.
- **Don't build on the host**, on any backend or in any test
  ([§12](forked-programs-as-packs.md#12-what-this-does-not-license)); unit tests stand the jail in
  with `fakeCaptureJail`.
- **Don't seal `yolo capture` of an installer** in this work
  ([FP-D9](forked-programs-as-packs.md#FP-D9)), and don't add a notch component to the key
  ([FP-D4](forked-programs-as-packs.md#FP-D4)).
- **Don't wire `macos-user` past [FP-D3](forked-programs-as-packs.md#FP-D3)'s line**: the slot sits
  below that arm's return, and the store's reach there is a pending ruling.

## Blockers

None for steps 1–6. Step 7 waits on its measurement ([Step 7 needs](#step-7-needs)). Past it,
[§11](forked-programs-as-packs.md#11-sequencing) step 4
(`macos-user`) waits on hand-off H4, a ruling
[`install-capture.md`](../plans/install-capture.md#hand-offs--what-is-not-wired-and-the-exact-line-that-wires-it)
holds, and on the macOS host capture ([HP-D2](host-tool-provisioning.md#HP-D2)); step 5 (`guest`)
waits on env-manager Phase 7.
