---
title: "Plan: pinned host helpers and their start line"
date: 2026-11-10
status: accepted
stage: DECIDED
next: "Build task 1 (internal/helperpin) and task 2 (the darwin host yolo in the bundle) in parallel"
depends-on: [pinned-host-helpers.md]
tags: [plan, host-services, keeper, daemons, bundles, logging]
summary: "Build plan for pinned-host-helpers.md, by file: a helperpin package (resolve, identity check, start line), the darwin host yolo in bundles, the run pipeline's self-execs through the pin, launch services, yolo host and macos-user, the start line in internaldaemon and the keeper, process-exe liveness in the generation reaper, and one integration test."
---

# Plan: pinned host helpers and their start line

**Status:** 2026-11-10. Written against `e5a75f551`. Nothing built.

**Design:** [`pinned-host-helpers.md`](pinned-host-helpers.md). Precedence: the design wins on
behavior, the tree wins on fact (cosmetic drift only: follow it and say so in the commit), this file
is advice and is the first thing to be wrong. **Stop and report, do not commit** when a task's red
test does not fail as predicted, a named file is dirty with someone else's work, a named symbol is
gone, the fix needs a file outside the task's fence, or a gate fails outside the task's scope.

## Map

| Path | Change |
| :--- | :--- |
| `internal/helperpin/` | **new**: `Pin` (path + recorded identity), `Resolve`, `Unchanged`, `StartLine`, `EnvJail` |
| `scripts/stage-source-bundle.sh` | a darwin host list (`yolo`) beside `GUEST_BINARIES` (~`:123`) |
| `scripts/matched-bundle.py` | `assemble` (~`:199`) takes the darwin launcher from the bundle; `_binary_lists`, `_validate_bundle_contents` |
| `scripts/test_matched_bundle.py` | cases for the darwin host yolo |
| `internal/reporoot/reporoot.go` | `darwinBinaries` (~`:60`) gains `yolo`; `validateBundle` already refuses with `ReinstallSteps` |
| `tools/tap-install-check/judge.go` | `requiredPrefixBinaries` (~`:361`) expects the darwin host yolo |
| `internal/cli/run/scratchremoval.go` | `Options.selfExecArgv` (~`:131`) execs the pin when not in keeper mode on Linux |
| `internal/cli/run/keeperspawn.go` | `realSpawnKeeper` (~`:80`) execs the pin |
| `internal/cli/run/keeper_other.go` | `keeperSelfExe` returns the plan's pin path |
| `internal/cli/run/keeperplan.go` | `keeperPlan` (~`:38`) gains the pin (path + identity) |
| `internal/cli/run/run.go` | pre-flight resolution beside `o.RepoRoot()` (~`:177`) |
| `internal/cli/run/hostdaemonsupervise.go` | `daemonSupervisor.run` (~`:245`): `Unchanged` before `s.restart()`, give-up otherwise |
| `internal/cli/run/loopholesruntime.go` | `startExternalService` (~`:1400`) sets `YOLO_HELPER_JAIL` |
| `internal/cli/run/applybrokerskew.go` | the note drops "may still run it" |
| `internal/cli/run/keeper.go` | the keeper's first log line is the start line |
| `internal/cli/run/keeperpins_test.go` | extend the AST test to the non-keeper spawners |
| `internal/launchservice/launchservice.go` | `SelfExec` (~`:537`) execs the pin; `Supervise` checks before restart; env |
| `internal/cli/host.go`, `internal/cli/hostclaudeview.go`, `internal/openaiauthhost/privatedaemon.go` | `yolo host` resolves a pin when it will start a helper, and its private daemons exec it |
| `internal/macosuser/real.go`, `orchestrator.go`, `jaildaemon.go` | `SelfExec` becomes the pin; the guest `yolo` is copied from it (~`jaildaemon.go:310`) |
| `internal/internaldaemon/internaldaemon.go` | `Run` (~`:60`) writes the start line before dispatch |
| `internal/flakebundle/flakebundle.go`, `internal/cli/run/housekeeping.go` | process-exe liveness for generations (~`housekeeping.go:833`) |
| `integration/pinnedhelpers_test.go` | **new** |

## Reuse before you write

- **Bundle resolution:** `reporoot.Resolve` already returns the bundle root the jail mounts. Take the
  root from its result; never re-derive it from `os.Executable()` or from `paths.FlakeBundleDir`
  (that is the stable symlink, which an install moves).
- **Build stamp:** `keeperBuildStamp()` (`keeperplan.go:207`) is the existing "same build" string.
  Compare the bundle's stamp the way `reporoot.validateLauncherIdentity` does; do not invent a
  second comparison.
- **Version values:** `version.Baked()`, `version.CommitLabel()`, `version.ExecutablePath()`
  (`internal/version/identity.go`).
- **Timestamp:** the `logstamp` package's layout; every daemon log already uses it.
- **Stop remedy:** `keyedStopRemedy(rt, cname)` (`internal/cli/run/attachskew.go:71`) for the give-up text.
- **Process table:** `prune.readProcStarts` (`internal/prune/embeddedtrees.go:81`) and `ProcStart.Exe`;
  the dev-bundle reaper's `DevBundleLiveness` shape (`internal/prune/devbundles.go`) is the sibling to
  copy for "known" tri-state.
- **Restart seam:** the supervisor's existing give-up path in `daemonSupervisor.run`; add a cause,
  not a second path.

## Traps

- **Linux keeper must keep `/proc/self/exe`.** `TestAKeepersSelfExecsRunItsOwnInode` pins it; the pin
  only changes what the keeper is *spawned from*. Rewriting the keeper's own self-execs to the pin's
  path loses the survives-deletion property. **Constraint.**
- **Homebrew bytes differ from the bundle's.** `update-homebrew.sh` builds `bin/yolo` with `-s -w`;
  a byte or hash comparison between launcher and pin fails on every Homebrew install. Compare stamps.
- **`os.Executable()` on Linux returns the replaced path** after `just install`; a test that execs
  "the current binary" by path does not prove pinning. Tests use an injected pin pointing at a copy.
- **The integration run has its own state dir**: read the daemon log under it, not under `~`, which
  is the outer jail's live home.
- **`internaldaemon.Run` is also reached by `--self-check`** from `yolo check`; writing the start line
  there corrupts the doctor output tests compare.
- **The keeper's stdio is `/dev/null`**; a start line on stderr from the keeper is lost. Write it to
  the keeper log.
- **`git add` before a nested build**: the hermetic Go build sees tracked files only; a new
  `internal/helperpin` untracked is silently absent from the jail.

## Tasks

### Task 1 — `internal/helperpin` (independent)

- **Settled (do not re-open):** the pin is `<bundle root, EvalSymlinks>/bin/<GOOS>-<GOARCH>/yolo`;
  identity is device+inode, size, mtime; `Unchanged` is a `stat`, never a hash; the start line is
  `<logstamp> helper start: name=<n> build=<Baked or unstamped> commit=<CommitLabel> pid=<pid> jail=<YOLO_HELPER_JAIL or -> exe=<ExecutablePath or unknown>`,
  keys in that order, values with whitespace or quotes Go-quoted.
- **Tests first:** `internal/helperpin/helperpin_test.go`:
  - `TestResolveReadsTheHostBinaryFromTheResolvedRoot` — a temp bundle whose root is a symlink;
    the pin's path is under the target.
  - `TestResolveRefusesABundleWithoutAHostYolo` — error text names `reporoot.ReinstallSteps`.
  - `TestUnchangedSeesAReplacedFile` — write, resolve, rename a new file over it → false; remove → false.
  - `TestStartLineFormat` — fixed clock and values; exact string, including a quoted `exe=` with a space
    and `jail=-` when the env is empty.
  - Red: `go test ./internal/helperpin` (package does not exist).
- **Change:** new package; no callers yet.
- **Gates:** `go test ./internal/helperpin`; `just format`.
- **Fences:** nothing outside `internal/helperpin/`.
- **Commit:** `feat(helperpin): resolve a launch's pinned helper and format the start line`.
- **Report:** red, green, any helper you found already existing.

### Task 2 — the darwin host yolo in the bundle (independent)

- **Settled:** darwin bundle dirs carry `yolo` from a separate host list; guest list unchanged;
  `assemble` takes the darwin launcher from the bundle; reporoot requires it on darwin.
- **Tests first:** `scripts/test_matched_bundle.py` — a darwin `_binary_lists` case expecting `yolo`;
  `internal/reporoot` — `TestValidateBundleRequiresTheDarwinHostYolo` (GOOS injected the way existing
  darwin cases do). Red: `python3 scripts/test_matched_bundle.py` and `go test ./internal/reporoot`.
- **Change:** files in the map rows for the bundle; `tools/tap-install-check/judge.go`.
- **Gates:** both test commands; `go test ./tools/tap-install-check/...`; `just format`.
- **Fences:** no Go outside `internal/reporoot` and `tools/tap-install-check`; do not touch
  `GUEST_BINARIES`.
- **Commit:** `feat(bundle): ship the darwin host yolo in every bundle`.
- **Report:** red, green, and the darwin bundle size delta if you can measure it.

### Task 3 — the run pipeline (after 1)

- **Settled:** pre-flight resolution for any launch that will start a helper; a refusal names the
  reinstall step; every self-exec in `internal/cli/run` that serves the jail goes through the pin;
  the Linux keeper keeps `/proc/self/exe`; a changed or missing pin turns a restart into a give-up
  naming the stop step; `YOLO_HELPER_JAIL` is the container name.
- **Tests first** (`internal/cli/run`):
  - `TestAHostDaemonIsExecdFromThePin` — an `o.helperPin` seam pointing at a copy; drive
    `resolveDaemonArgv` through `startExternalService` and assert argv[0].
  - `TestARestartWithAChangedPinGivesUp` — supervisor with an injected exit channel; replace the pin
    file; the give-up line appears (wait on the log line, not a timer) and no restart happens.
  - `TestTheKeeperPlanCarriesThePin` and, off Linux, `keeperSelfExe` returns it.
  - `TestALaunchWithoutAPinRefusesBeforeSpawning`.
  - Extend `TestTheKeepersSelfExecsGoThroughItsOwnBinary` to fail if any call site in the package
    reaches `execx.SelfExecArgv` directly.
  - Rewrite `applybrokerskew` tests to the definite wording; do not relax them.
  - Red: `go test -short ./internal/cli/run -run 'Pin|GivesUp|SelfExecs|BrokerSkew'`.
- **Change:** the run-package rows of the map.
- **Gates:** `go test -short ./internal/cli/run`; `rg -n 'host-service|keeper' integration/` and run
  those tests; `just format`.
- **Fences:** not `keeper.go`'s log writer (task 6); not `internal/launchservice` (task 4).
- **Commit:** `feat(run): exec every jail helper from the launch's pinned yolo`.
- **Report:** red, green, the integration tests run.

### Task 4 — launch-owned services (after 1)

- **Settled:** `SelfExec` execs the pin; `Running.Supervise` checks `Unchanged` before a restart and
  gives up with the stop step otherwise; services carry `YOLO_HELPER_JAIL`.
- **Tests first:** `internal/launchservice` — `TestAServiceIsExecdFromThePin`,
  `TestSuperviseGivesUpWhenThePinChanged` (event-driven on the exit channel).
  Red: `go test ./internal/launchservice -run Pin`.
- **Gates:** `go test ./internal/launchservice`; `just format`.
- **Fences:** `internal/launchservice/` only.
- **Commit:** `feat(launchservice): run launch-owned services from the pinned yolo`.

### Task 5 — `yolo host` and macos-user (after 1 and 4)

- **Settled:** `yolo host` resolves a pin only when it will start a helper; its private daemons and
  macos-user's services exec the pin; the guest `yolo` is copied from the pin.
- **Tests first:** `internal/macosuser` — the staged guest `yolo` is the pin's file (compare contents
  against the injected pin, not `os.Executable()`); `internal/openaiauthhost` and
  `internal/cli` — the private daemon argv[0] is the pin. Red: `go test ./internal/macosuser ./internal/openaiauthhost ./internal/cli -run Pin`.
- **Gates:** those packages; `GOOS=darwin go vet ./internal/macosuser/...`; `just format`.
- **Fences:** the files in the map rows; nothing in `internal/cli/run`.
- **Commit:** `feat(host,macosuser): run helpers from the pinned yolo on every notch`.

### Task 6 — the start line (after 1)

- **Settled:** `internaldaemon.Run` writes it to stderr before dispatch, except jail-keeper and
  `--self-check`; the keeper writes it as its log's first line.
- **Tests first:** `internal/internaldaemon` — with an injectable stderr, `Run([]string{"<name>", ...})`
  writes exactly one start line first; `--self-check` writes none. `internal/cli/run` —
  the keeper log's first line matches `helper start: name=jail-keeper`.
  Red: `go test ./internal/internaldaemon -run StartLine`.
- **Gates:** both packages; `go test ./internal/loopholes -run SelfCheck` (doctor output unchanged).
- **Fences:** `internaldaemon.go` and the keeper log open in `keeper.go`.
- **Commit:** `feat(daemons): every host helper logs its build at start`.

### Task 7 — generation liveness (independent)

- **Settled:** a generation is in use while a live process of this user executes a file inside it;
  an unreadable process table declines the sweep.
- **Tests first:** `internal/flakebundle` — `TestAGenerationAProcessRunsFromIsKept`,
  `TestAnUnreadableProcessTableReapsNothing`. Red: `go test ./internal/flakebundle -run Process`.
- **Gates:** `go test ./internal/flakebundle ./internal/cli/run -run Housekeeping`.
- **Fences:** `flakebundle.go`, `housekeeping.go`; do not change `PruneDevBundles`.
- **Commit:** `fix(flakebundle): keep a generation a running helper executes from`.

### Task 8 — integration and docs (after all)

- **Test:** `integration/pinnedhelpers_test.go` — launch a jail with one host daemon; its log's start
  line has `exe=` inside the run's bundle and `build=` equal to the jail's `YOLO_VERSION`; kill the
  daemon and wait for the second start line, same build. `requireJail(t)`, no `t.Parallel()`.
- **Docs:** the JL-D5 row's Built column, HD-D28's give-up causes,
  `docs/reference/image-staging-vs-baking.md` (the host side is now pinned too), and graduate the
  start line into the host-service reference.
- **Gates:** `just check-ci`; `go test -count=1 -timeout 0 ./integration` (launch-path change).
- **Commit:** `test(integration): a host daemon runs and logs the launch's build`.

## Ships with

- The start line is the observable surface; no new verb.
- Error text: the pre-flight refusal and the give-up line, both naming a next step.
- Darwin: Mac verification of tasks 2, 5 and the give-up path goes to a design-doc witness list for
  the owner's Mac agent; the nested jail cannot check darwin.

## Don't

- Don't fall back to `os.Executable()` when the pin is gone; that is the behavior the design removes.
- Don't hash the pin before each exec; `stat` identity is decided.
- Don't pin `yolo check`'s self-checks or machine-wide jobs.
- Don't copy the binary into a new store under yolo's state.
