---
title: "Last session wins: the keeper at the container backends, implementation plan"
date: 2026-09-30
status: accepted
stage: BUILT
next: "Delete this file, as it asks once its work lands: step 3 landed at e343542d, and the design names its code and tests in its section 7"
tags: [plan, lifecycle, attach, sessions, keeper]
summary: "The build hand-off for step 3 of jail-lifetime-last-session-wins.md: the keeper at podman and Apple Container. The map, what to reuse, the traps, the build order and what ships with it, written against f596a969. The keeper at yolo host and macos-user (§9.9) and the Mac measurements (step 4) are out of scope."
vantage:
  status-chip: true
---

# Last session wins: the keeper at the container backends, implementation plan

**Status:** 2026-09-30 (`e343542d`). Written against `f596a969` the same day, as the hand-off
step 3's build followed. MEASURED in nested jails on Linux podman, by the integration tests the design's
[§7](jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order) step 3 entry names.
UNMEASURED: on either Mac backend and on a real rootless systemd host.

**Design:** [`jail-lifetime-last-session-wins.md`](jail-lifetime-last-session-wins.md), step 3 of
its [§7](jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order) and all of
[§9](jail-lifetime-last-session-wins.md#9-the-keeper-design-2026-09-29) up to
[§9.9](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user).

**Built** at `e343542d`, 2026-09-30 (the design's [§7](jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order) step 3 entry names the code and the tests).
Delete this file when the work lands. What the build had to rediscover, which the next plan should
say: the spawn derives the host-services dir from the container name, not from the plan; the
package's end-to-end tests need an in-process keeper spawner (`defaultKeeperSpawner`, swapped in
`TestMain`); the running event must reach the launch before ready, or the relay ends first and the
housekeeping slot never starts; and the same-cycle CHANGELOG entries of steps 1 and 2 fold into
step 3's. Nothing here went unused.

**Precedence.** The design wins on behavior, the tree wins on fact, and this file is advice and
the first thing to be wrong. The terms (keeper, hold process, session lock, liveness lock, lifeline,
unkept jail, draining, Window A) are the design's
([§1.1](jail-lifetime-last-session-wins.md#11-terms)).

**Scope.** The container backends only: podman on Linux and macOS, and Apple Container. The keeper
at `yolo host` and macos-user ([§9.9](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user))
waits on [OQ-JL9](jail-lifetime-last-session-wins.md#OQ-JL9) and step 5. Step 4's Mac measurements are
not buildable here.

## Map

| Path | Change |
| :--- | :--- |
| `internal/cli/run/keeperplan.go` | new: the plan (JSON, `0600` in a `0700` dir, read once and removed) and its build stamp |
| `internal/cli/run/keeperframe.go` | new: the progress pipe's frames, the launch-side relay, the keeper's switchable writer and log |
| `internal/cli/run/keeperstate.go` | new: the liveness lock, the start record, the keeper's log path, and every probe an arrival, a quit, the reaper and `yolo stop` make of them |
| `internal/cli/run/keeper.go` | new: `KeeperMain` and the keeper's life: services, the container, ready, the drain, the chain |
| `internal/cli/run/keeper_linux.go`, `keeper_other.go` | new: `/proc/self/exe` spawn, the kernel's death signal on children, the scope move |
| `internal/cli/run/keeperspawn.go` | new: the fresh launch's side: disclosure line, spawn, relay until ready, the first session, its quit |
| `internal/cli/run/run.go` | `runContainer` (`run.go:1099`) splits at the service boundary (`run.go:1688` to `run.go:1945`); the attach decision loops on a drain; `attachExisting` (`run.go:2196`) quits through the shared session quit |
| `internal/cli/run/jailmain.go` | `awaitJailMainEnd` and `firstSessionStatus` go; `launchSignalArm` stays (both arms use it) |
| `internal/cli/run/stopreason.go` | the first-session-end records go (`stopreason.go:118` to `:146`); the keeper's reasons join |
| `internal/cli/run/sessionlock.go` | a take that returns at once when a keeper is draining |
| `internal/cli/run/lifecycle.go` | the reaper's liveness half (`lifecycle.go:231`), Apple Container included; `clearOwnerPID` becomes a comparison; `noteGoneOwner` goes |
| `internal/cli/run/contracttags.go` | `restartJailForAttach` (`contracttags.go:515`) waits for the old keeper, holding the launch lock |
| `internal/cli/run/network.go`, `loopholesruntime.go` | children get the death signal in keeper mode; the fwd dir is removed whole; `stopLoopholes`' probe is bounded (`loopholesruntime.go:587`) |
| `internal/cli/run/packloopholes.go` | `startLoopholesDisclosed` splits into its disclosure and its start, so the launch can disclose and the keeper start |
| `internal/cli/run/scratchremoval.go` | the keeper's scratch remover self-execs its own binary, not `execx.SelfExecArgv` (`scratchremoval.go:116`) |
| `internal/entrypoint/jailmain.go` | the hold no longer follows the first session: `followFirstSession` (`jailmain.go:291`) goes |
| `internal/internaldaemon/internaldaemon.go`, `internal/cli/internal.go` | the `jail-keeper` member, wired by the CLI |
| `internal/cli/stop.go` | waits for the keeper and streams it, or runs the chain for an unkept jail; help text |
| `integration/keeper_test.go` | new; `jailmain_test.go` and `stop_test.go` rewritten to step 3 |

## Reuse before you write

- **The spawn shape:** `startDetached` (`scratchremoval.go`): Setsid, nil stdio, `ExtraFiles`.
  The keeper's spawn is that plus three descriptors and a `cmd.Wait` the launch keeps.
- **The lifeline:** `launchservice.Lifeline`'s mechanism, a pipe whose write end only the launch holds.
- **The relay:** `startJailMain` and `readyRelay` (`jailmain.go`) already relay pid 1 and detect
  `entrypoint.BootReadyLine`; the keeper calls `startJailMain` with frame writers.
- **The chain:** `teardownAfterExit` (`run.go:1953`) is the keeper's chain unchanged, after its stop.
  `stopJail`, `stopLoopholes`, `forgetGoneContainer`, `startScratchRemoval`, `captureConfigOnTerminate`.
- **The session's arm:** `attachSignalArm` / `hangUpAttachSession` (`sessionhangup.go`). The first
  session takes it with an id of its own, since every keeper-era jail freezes the session-hangup tag.
- **Why a session ended:** `noteJailEnded` (`stopreason.go`). The first session's quit is an attach's.
- **Pack records:** `loadPackTree` + `adoptPackRecords` (`packtree.go`) rebuild the converged
  loophole set from the staged tree; that is how the keeper resolves its packs (JL-D35).
- **The call-order tests:** `funcDecl`, `skelCallee`, `callsIn` in the package's tests.

## Traps

- **`workspaceLock.Close` unlocks every duplicate** (`flock.go`). Handing the launch lock to the
  keeper closes the launch's copy *without* `LOCK_UN`, or the keeper holds nothing.
- **`internaldaemon` cannot import `run`**: `run`'s tests dispatch through `internaldaemon.Run`
  (`journalbridge_test.go`'s `TestMain`). The CLI sets the member's entry.
- **A unit test must never self-exec the keeper**: the test binary would rerun its suite in a detached
  child (the `errTestBinarySelfExec` guard in `startDetached` is the precedent).
- **`TestEverySpawnEntryDisclosesHostExecFirst`** fails on any `startLoopholes` caller that does not call
  `notePackHostExec` first. The keeper cannot disclose; give its start its own pin, never an exemption.
- **Every loophole's front is a goroutine**: a keeper that exits early takes the jail's credentials
  with it. `os.Exit` in a keeper path skips the chain; return through `run()`.
- **Go exits on SIGPIPE to fd 1 or 2** without `signal.Notify`. The keeper's stdio is `/dev/null` and
  the pipe an `ExtraFiles` descriptor, and it still notifies for SIGPIPE. Never `signal.Ignore`.
- **Pdeathsig is Linux-only** (`syscall.SysProcAttr` has no field on darwin); keep it in `_linux.go`.
- **An arrival never waits holding the launch lock**, or the keeper's non-blocking teardown guards
  back off and leak the host-services dir, the tracking file, the skeleton and the pack tree.
- **`GOOS=darwin staticcheck` runs in `just lint`**: every new helper needs a caller on both GOOS.

## Build order

1. **The keeper process and its state**, reachable through `yolo internal daemon jail-keeper`, with unit
   tests of the plan, the frames, the liveness lock and the keeper's life against a fake main process.
   → `go test ./internal/cli/run -run 'Keeper' ./internal/internaldaemon ./internal/cli`
2. **The fresh launch spawns it**, the hold stops following the first session, and the first session
   quits as an ordinary session. The step-2 couplings (JL-D46, JL-D49's launch-wide arm, the first-session
   records) go in the same commit. → `just test-fast`, then
   `go test -run 'TestTheMainProcessIsAHold|TestStopEnds|TestQuittingTheFirst' ./integration`
3. **Arrivals, unkept jails and the attach-skew restart**: JL-D28's wait, JL-D13's refusal, JL-D30's reap
   on the last quit, JL-D26. → the unit suite, then `go test -run 'Keeper|Orphan' ./integration`
4. **The reaper's liveness half, `yolo stop`, children that end without the keeper.**
   → `just test-fast`, `go test -run 'Stop|Orphan|Keeper' ./integration`
5. **Docs**, gated by `uvx vantage-check@0.7.0` on each changed file.

## Ships with

- **Unit, `internal/cli/run`:** the plan's mode, removal and build-stamp refusal; a frame round trip; the
  relay's routing (keeper lines to the tees, pid 1's to the raw streams, the events); a second liveness
  take fails; the keeper drains on the exclusive session lock, records why, stops, runs the chain and
  frees both locks; a lifeline EOF before ready unwinds; a SIGTERM ends the jail in order; a quit reads
  last, not last, and unkept; the arrival's drain wait; the unkept refusal; the reaper declines a live
  keeper's jail and a jail whose session lock it cannot take; `yolo stop` waits for a live keeper.
- **Call sites (AST):** `runContainer` discloses before it spawns, takes its session lock before it
  spawns, and hands the first session the session arm; **no session's arm calls `stopJail`, the first
  session's included**; the keeper's start refuses an undisclosed daemon before it starts one.
- **Entrypoint:** the hold ends only on SIGTERM, with a first session registered and gone.
- **Integration:** two sessions, quit the first: it returns at once with the line, the second still
  runs, and the last quit leaves no container and no keeper; `kill -9` the keeper: the session runs on,
  an arrival is refused, and the last quit reaps the jail; a hung-up first session ends only itself.
- **Rewrite, do not repair:** `TestTheHoldFollowsTheFirstSession`,
  `TestTheJailEndsWithTheFirstSessionEvenWhenTheHoldDoesNotFollow`,
  `TestTheFreshLaunchRunsTheJailAsAHoldAndItsFirstSessionByExec`,
  `TestAStopRecordIsReplacedByACauseAndNotByTheFirstSessionsEnd`,
  `TestAnAttachIntoAJailWhoseLauncherIsGoneSaysSo`, and in `integration/jailmain_test.go`
  `TestAnOrphanSweepSparesAJailWithASessionInIt` and `TestAHangupWhileTheMainProcessLingersStillEndsTheJail`.
  Each asserts the step-2 shape step 3 replaces.
- **Docs:** the design's status, [§7](jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order) step 3, the ledger's Built cells and new rows; `jail-home.md`'s
  lifecycle table; `perf-logging.md`'s Window A and Ctrl-C rows; `ctrl-z-and-the-tty-proxy.md`;
  `userguide/guides/troubleshooting.md`'s "My other terminal's agent stopped"; `yolo stop --help`
  (`stopUsage`); one CHANGELOG `[Unreleased]` entry.
- **No config key, no new binary**: the keeper is a member of `yolo internal daemon`, so `flake.nix`
  and `stage-source-bundle.sh` do not change.
- **Cheap and yours:** frame encoding, the plan's field names, the bound values (each must exist).
  **Stop and ask:** anything that changes what a session sees beyond
  [§4.5](jail-lifetime-last-session-wins.md#45-what-the-user-sees).

## Don't

- Don't build the death pipe (`podman exec --preserve-fds`): optional under JL-D4, local podman only.
- Don't give the keeper a timer to end on (JL-D17), and don't restart a dead keeper (JL-D18).
- Don't move housekeeping into the keeper (JL-D10).
- Don't touch the macos-user or `yolo host` arms of `Run`.

## Blockers

None for the container backends. [OQ-JL9](jail-lifetime-last-session-wins.md#OQ-JL9) blocks
[§9.9](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user) only.
