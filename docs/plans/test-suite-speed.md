---
title: "Test suite speed"
date: 2026-09-27
status: in-review
stage: DESIGN
next: "Audit the remaining timer tests and the slow package setups recorded by the latest retake before choosing the next bounded source change; then repeat the target measurements only when host-wide idleness can be verified"
tags: [testing, ci, performance, plan]
summary: "The suite-speed investigation, changes built so far, measurement recipes and retakes, and the remaining questions about the landing gate and darwin lint."
---

# The suite got slower because tests were added, and the suite got run more often

**Current status, 2026-10-09.** The refreshed recipe-1 and recipe-3 measurements are recorded at [the 2026-10-08 retake](#retaken-2026-10-08-on-the-clean-source-tree). All five commands passed, but the warm unit time and the three-run integration median both exceed their targets. Host-wide idleness was not verified, so this is not a valid target-acceptance check. The plan remains partly built: [OQ-TS4](#OQ-TS4) remains open ([OQ-TS2](#OQ-TS2) was ruled and built 2026-11-03), and the next source work is a bounded audit of the remaining waits and dominant package setups.

**Status:** 2026-09-27. Evidence measured 2026-09-27 against `71114ecc` and its parents.
Five work items are ruled and being built, and some have landed, among them the **integration**
partition's two isolations ([OQ-TS1](#decision-ledger)'s answer names the commits). Read in the tree on
2026-09-30, not re-measured: most of the other four partitions' fixes are in too
(`startExternalServiceHarness` takes a readiness deadline, the bounded-wait test fakes its clock,
the bootstrap tests carry a fake `fc-cache`, `TestASocketPathThatExistsAccepts` runs 500 rounds,
and `check-ci` is `[parallel]`), while `perf.SlowSpanThreshold` is still a fixed constant.
**Retaken 2026-10-01** at `d4e435a3`, on a machine other agents were using
([the retake](#retaken-2026-10-01-and-which-targets-hold)): the warm `check-ci` target holds on a
test-cache hit (6.4 s); the unit target does not (69.0 s, `internal/cli/run` 67.5 s), nor the
integration one (498.0 s, one run); and nothing of the five items as written is left, the misses
coming from tests added since. Four questions each
asked for one more lever. [OQ-TS1](#decision-ledger) and [OQ-TS3](#decision-ledger) were answered 2026-09-29, both as
leaned and both adding no lever ([Decision Ledger](#decision-ledger)). [OQ-TS2](#OQ-TS2) was
ruled by the owner on 2026-11-03, not as leaned, and built in `101f766b8`.
[OQ-TS4](#OQ-TS4) is still open, and none of the five work items waits on it.

> **In short (the 09-27 baseline).** No existing test got slower. The time went into tests added since mid-August,
> several of which wait on real clocks, and into running the full suite 28 to 34 times a day.
> Cutting about 18 s of waits, isolating two integration tests from a concurrent run's state, and
> running the full suite once per landing brings the warm gate back under 30 s.

**Why it matters.** On 09-27, 5 h 45 m of a 21 h 36 m working day was spent stalled on usage limits,
and about 5 h went to the integration suite. Measured the same day.

**Start at [Where the time goes](#where-the-time-goes).** Each work item fixes one row there.

**Needs your ruling:** [OQ-TS4](#OQ-TS4).

**Reads with:** [`integration-parallelism.md`](integration-parallelism.md) (the parked plan to run
tests in parallel inside the suite, which this plan does not reopen),
[AGENTS.md Workflow step 4](../../AGENTS.md#workflow) (the rule that committing and landing are
separate, which this plan's orchestration rules build on).

---

## Baselines

All figures are wall-clock seconds, measured on the maintainer's machine unless the row says CI.
[The measurement recipe](#measurement-recipe) retakes every row.

| What | Then | Now (09-27) | Source |
| :--- | :--- | :--- | :--- |
| Unit `go test -short ./...` | 19.0 s (the 09-03 tree), 30.1 s (09-19) | 41.2 s (`71114ecc`) | local, recipe 1 |
| `just check-ci`, local median | 17–20 s (09-03 to 09-10) | 54 s | local, recipe 2 |
| `just check-ci`, CI `check-go` quality step | 16 s (late July) | 126–160 s | GitHub Actions, recipe 4 |
| Integration suite, local | 315–374 s (09-03 to 09-17) | 518–553 s | local, recipe 3 |
| Integration suite, CI x64 | 646 s | 1085 s | GitHub Actions, recipe 4 |
| Full integration runs per day | 0–2 (09-10 to 09-25) | 7–8 (09-26), 28–34 (09-27) | session transcripts, recipe 5 |

The 28–34 range comes from where the UTC day boundary falls. Transcript timestamps are in UTC, so the
maintainer's evening of 09-27 is logged as 09-28.

### Retaken 2026-10-01, and which targets hold

**MEASURED** at `d4e435a3`, in a jail on the maintainer's machine, with recipes 1 to 3 below and
the four in-jail variables unset (`YOLO_VERSION`, `YOLO_HOST_LAYERS`, `AWS_CONTAINER_*`,
`YOLO_SERVICE_AWS_AUTH_ENDPOINT`). The machine was **not idle**: other agents' workflows ran
beside these, and the one-minute load average is given with each row, so read every figure as an
upper bound rather than a target check.

| What | 09-27 | 10-01 | Load | Target | Holds |
| :--- | :--- | :--- | :--- | :--- | :--- |
| Unit `go test -short -count=1 ./...`, second warm run | 41.2 s | **69.0 s** (the first run took 76 s) | 2–3 | ≤ 25 s | **No** |
| `internal/cli/run`, the package that sets that time | 40.8 s, 1064 tests | 67.5 s, 1471 tests | 2–3 | — | — |
| `just check-ci`, the second of two runs | 54 s | **6.4 s**: 110 of 112 packages were `(cached)` | 8 | ≤ 30 s | **Yes**, on a test-cache hit |
| `just check-ci`, compile caches warm and every test run | — | 76.5 s | 1–8 | — | — |
| `just check-ci`, fresh `GOCACHE` and `STATICCHECK_CACHE` | — | 91.0 s | 7–14 | — | — |
| Integration suite, `go test -count=1 -timeout 0 -json ./integration` | 518–553 s | **498.0 s**, one run: 246 passed, 45 skipped | 9–28 | ≤ 450 s | **No**, and not a valid check: one run, not the median of three on an idle machine |

The 54 s of 09-27 re-ran every test, because the in-tree `housekeeping.log` invalidated the cache
on every run. That log is gone (no `internal/cli/run/.yolo` existed after the runs above), so the
second run of an unchanged tree now replays the cache, and the warm target holds on that reading.
A change that touches `internal/cli/run` re-runs it, and then the gate costs what the 76.5 s row
says, since `internal/cli/run` alone took 70.7 s of it.

**What is left of the five work items**, read in the tree and in the timings above:

- **cli-run.** Done: `TestExternalServiceWaitsForCompleteEndpoint` takes 1.0 s and
  `TestExternalServiceRemovesStaleEndpoint` 0.3 s, against 5.01 s each, and
  `TestWaitForRunningContainerGivesUpBounded` 0.4 s. The slow-span tests no longer wait,
  recording a span past the threshold directly (`slowSpanEnd`,
  [`timingspans_test.go:285`](../../internal/cli/run/timingspans_test.go#L285)), while
  `perf.SlowSpanThreshold` is still a fixed 1 s constant. **What doubled the package instead:**
  six tests added from 09-26 to 09-29 (`git log -S` on each name) take 31.4 s together.
  `TestATreeTheTeardownCannotProveGoneOutlivesRun` takes 15.0 s;
  `TestASpawnWithNoApprovedScopeFailsClosed` and
  `TestAFreshLaunchHandsTheApprovedScopeToTheBroker` take 5.0 s each, the length of
  `serviceReadyTimeoutDefault` (INFERRED from the match, not traced); then
  `TestAConcurrentSweepNeverTakesASessionThatIsStartingUp` 2.7 s,
  `TestStartDetachedDoesNotWaitAndDetaches` 2.1 s and
  `TestWaitForScratchRemoversWaitsForTheSpawnedChild` 1.5 s. The other 1463 tests that ran take
  about 35 s.
- **entrypoint.** Done: the package takes 22.0 s, against 28.6 s, and no bootstrap test is among
  its slowest; the longest is `TestADeadLaunchersLockStillAges`, 2.0 s.
- **misc-unit.** `TestASocketPathThatExistsAccepts` takes 1.4 s. **New:** `internal/packsrc`
  takes 26.5 s, almost all of it six refresh-timeout tests (6.0, 6.0, 3.1, 3.0, 3.0 and 2.0 s), and
  `internal/svcendpoint`'s `TestReadAckTimeoutIsNotARejection` 5.0 s.
- **integration.** Landed (`bef9fc90`, `2a50babc`).
- **gate-ci.** Landed: `check-ci` is `[parallel]` in the [`Justfile`](../../Justfile).

So the unit target now waits on a new round of the cli-run and misc-unit items, over the tests
named above; nothing in the five items as written is left to build.

**Cut 2026-10-08:** the 15 s and both 5 s `internal/cli/run` tests, and
`TestReadAckTimeoutIsNotARejection`, each now take under half a second, with no assertion changed.
The 15 s was the keeper's `confirmGone` polling its whole bound (`keeperGoneAttempts`, 75 polls
200 ms apart) against a fixture runtime that always lists the container; the two 5 s tests were
`serviceReadyTimeoutDefault`, as inferred above; the svcendpoint test waited out
`handshakeTimeout`, now a variable. The six `internal/packsrc` refresh-timeout tests remain.

**Bounded refresh-wait first slice, 2026-10-08 (source/test work, not a measurement):** three existing
fetch/helper cases now cancel an existing `Store.Ctx` only after the wrapper or its pipe-inheriting
helper acknowledges the real fetch invocation. Each publishes the complete PID through a sibling
temporary file followed by an atomic rename; the tests include a controlled publication-gap
regression. Their 3 s store timeout remains a local-git safety
budget; cancellation is not described as a deadline. The non-detached case still waits for the
production 3 s `WaitDelay` backstop to actually fire. The other three deliberate timer proofs remain:
the exact-ref-lookup timeout (3 s), the real stalled-HTTP deadline (2 s), and the shared clone/fetch
budget (6 s). The lookup was not converted: canceling the store parent also cancels the later cached
ref resolution, so that path cannot preserve the existing cached-commit/Warning contract without a
production context-policy change, which is out of scope. The HTTP and shared-budget cases likewise
remain unchanged.

### Retaken 2026-10-08 on the clean source tree

**MEASURED** against `b2eeffc12b631d5cda108238e77a6c4cb5bb777c`, with the source tree clean before and after each command. The two recipe-1 runs were sequential; recipe 3 was run three times sequentially. Every command exited 0. The measurement ran in a Linux rootful jail with 32 CPUs visible. No other heavy command or suite was visible from the jail during the runs, but host-wide idleness could not be verified. The process umask was `022`. The sampled one-minute load averages were 12.05 and 14.17 at the two unit starts, then 11.57, 10.34 and 4.52 at the three integration starts.

| Measurement | Run 1 | Warm run / runs 2–3 | Median or warm result | Target | Result |
| :--- | ---: | ---: | ---: | ---: | :--- |
| Unit `go test -count=1 -json -short ./...` wall time | 124.156 s | 123.287 s | 123.287 s (warm) | ≤ 25 s | Not met |
| Full integration `go test -count=1 -json -timeout 0 ./integration` wall time | 676.432 s | 631.301 s; 606.978 s | 631.301 s (median of three) | ≤ 450 s | Not met |

The package `Elapsed` values in Go's JSON output are not command wall times; the table uses the external wall measurements. The Linux integration runs did not exercise native macOS coverage: 0 `macos-user` tests executed and 91 were skipped. This limits what the green integration result establishes.

The warm unit run's largest package `Elapsed` values provide an audit order, not an attribution to a single test and not a sum of wall time:

| Package | Elapsed in warm unit run |
| :--- | ---: |
| `internal/cli/run` | 121.213 s |
| `internal/cli` | 96.053 s |
| `internal/entrypoint` | 72.050 s |
| `internal/packsrc` | 31.145 s |
| `integration` under `-short` | 30.989 s |

Neither numeric target was reached, and the run cannot establish an idle-target check because host-wide idleness was not observable. These timings make no claim of improvement or causality relative to earlier source trees or different machine loads. The next source step is to audit the dominant package test setup and the remaining deliberate timer waits described above, then select the smallest safe follow-on; do not introduce a new timing framework or a global parallelism limit based on this data.

> [!NOTE]
> An earlier attempt is excluded from these measurements: two capture tests failed because the runner's restrictive inherited umask made copied test fixtures mode `0700` instead of the intended `0755`. The runner was corrected to set the test process umask to `022`, and both focused capture tests passed before this completed retake. This was a fixture-permission measurement-harness defect, not evidence of a product regression.

### `internal/cli` on the maintainer's Mac is git-spawn-bound (2026-10-16)

The maintainer's Mac (Apple Git 2.50.1, no Homebrew git) timed `internal/cli` out at the 20-minute
package limit (the lab report `docs/lab-reports/2026-10-16-macos-user-pi-refusal.md`, written on
the Mac), while CI's macOS runner passes under it.

**MEASURED on Linux, 2026-10-16**: `go test -short -count=1 ./internal/cli` takes 170 s for 2347
tests. No test takes more than 4.1 s, and only 22 take 1 s or more (39 s together). With `git` on
`PATH` replaced by a logging wrapper, the package started **48,536 `git` processes**, spread across
572 tests: the 100 heaviest hold 45% of them, so no handful of tests is the cost. Most are the
product's own patched-fork replay (`rev-parse`, `rev-list`, `am`, `merge-tree`, `commit-tree`), not
fixture setup.

**MEASURED on that Mac, 2026-10-19 at `8d8e73653`, and it is 41 seconds:** `go test -short
-count=1 -timeout 60m ./...` reports `ok … /internal/cli 1241.180s`. The package **passes** — it
is not hanging, and no test in it fails — but 1241 s is 41 s past the gate's own
`-timeout=20m` of the time (the Justfile's `test-fast`), so `just check-ci` and `just done` could not go
green on that host while every other package passed. ⚠ **This supersedes the ~25-minute estimate
below as the number to plan against.** It also changes what the fix has to be worth: the levers
in this section are each a ruling and a build, and the gap they must close is 3.4% of one
package's wall time. `internal/entrypoint` is second at 722.8 s, so nothing else is near the
ceiling. A per-package timeout, or the first lever that saves a minute, settles it.

**MEASURED on that Mac, 2026-10-23 at `83ac7bb41`: the `/usr/bin/git` shim is a quarter of the
package.** 100 runs of `/usr/bin/git --version` take 20.0–21.1 ms each, against 10.1–11.2 ms for
the Command Line Tools binary it forwards to (`xcrun --find git`,
`/Library/Developer/CommandLineTools/usr/bin/git`), three interleaved rounds. `go test -short
-count=1 -timeout 0 ./internal/cli` then took **1127 s** with the shim (load average 2.3 at start)
and **872 s** with the CLT directory first on `PATH` (load average 5.2 at start), every test
passing both times. That is 255 s saved, 23%, under more load. The two heaviest tests went from
30.4 s to 21.6 s and from 22.2 s to 16.4 s. So the cheaper `git` on darwin, the second lever
below, is now measured; how yolo or its tests would pick that binary is still a ruling.
`internal/entrypoint` took 655 s the same day, so it does not approach the gate.

**The gate's bound, 2026-11-03: the per-package timeout settles it, at 45 minutes.** The Mac
timed `internal/cli` out again under `just done` (1359 s alone with `-timeout 25m` while the
macos-user and integration suites ran, the lab report
[`2026-11-03-mac-slice-12a-witness-w1.md`](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#9-the-short-suite-on-this-mac)),
and check-macos's three `internal/cli` shards summed to about 9 minutes on an idle runner, so the
work is legitimately that long. `SHORT_TEST_TIMEOUT` in
[`scripts/go_packages.py`](../../scripts/go_packages.py) is now the one bound, twice the slowest
run, and both `just test-fast` and `just done`'s changed-package `go test` take it; the latter
took go test's default of 10 minutes before. The three levers below were ruled 2026-11-03 (all three, fewer starts first); the bound only stops the gate from failing on time when no test hung.

**INFERRED from the lab report's two timings, superseded by the measurement above:** the two tests it names take 45.9 s and 29.8 s on
the Mac, and start 1468 and 837 `git` processes on Linux, so each start costs about 31 to 36 ms
there. 48,536 starts at that cost is about 25 minutes, past the limit. Unmeasured: whether the cost
is the `/usr/bin/git` xcrun shim, which CI's Homebrew git skips, or per-exec scanning. To tell them
apart on the Mac, time 100 runs of `/usr/bin/git --version` against 100 of
`"$(xcrun --find git)" --version`.

So shortening real waits cannot fix this: the background-advance tests are not waiting, they are
starting `git`. The levers are fewer `git` starts per replay (a launch on the Mac pays the same
cost), a cheaper `git` on darwin, or running `git`-heavy tests in parallel. **Ruled 2026-11-03:**
all three, in that priority. Owner: *"A, and C if possible. B if it's easy."* Fewer starts per replay
comes first, because a real launch on the Mac pays the same cost; parallel tests second, where tests
can be made independent; the cheaper darwin `git` only if it is a small change.
The one wait cut made: `TestAppleListingQueryIsBounded` sets the `container ls` bound
(`appleListingBound`) to 100 ms instead of waiting out 2 s.

**Levers A and B built 2026-11-03; C not built.** A *replay* here is one run of a patched fork's
series onto a candidate commit (`WalkSeries` in [`replay.go`](../../internal/packsrc/replay.go));
a launch that builds pays two, the walk's and the build's copy into its source directory.

- **A, fewer starts per replay.** Three starts are gone, each with identical answers. The commits
  one `git am` made are read from the scratch repository's own `.git/HEAD` and loose commit
  objects, which replaces a `rev-list --reverse` (and the `rev-parse HEAD` after each member's
  `git am`). Any shape those files do not answer for falls back to git
  ([`scratchread.go`](../../internal/packsrc/scratchread.go)). With git 2.45 or later, whose
  `merge-tree` merges trees as well as commits, no pick is committed (`commit-tree`): the next
  pick merges onto the previous pick's tree. The base's and the first entry's object listing is
  one `rev-list` when both are complete (`probeComplete`). When anything is missing, the old
  one-at-a-time probe and fetch run unchanged. A filled-mirror replay of a two-member series onto
  a moved upstream now starts 7 gits, where it started 10, and
  `TestAFilledWalkStartsSevenGits` pins that count by subcommand.
- **B, the darwin `git`.** When `PATH` resolves `git` to Apple's `/usr/bin/git` shim, a store
  with no `Git` of its own runs `xcrun --find git`'s answer directly, asked once per process
  and developer directory ([`gitbin.go`](../../internal/packsrc/gitbin.go)). This reaches the
  product's patched-fork git only; test fixtures still call `git` from `PATH`. It has not been
  timed on the Mac.
- **C, parallel tests, is not built.** The `git`-heavy `internal/cli` tests are whole launches:
  their fixture (`newPatchedFixture`) calls `t.Setenv` for `HOME` and `XDG_CONFIG_HOME`, which
  the product reads through `paths`, and it swaps package-level functions
  (`programRefresh`, `hostApplyFromPackUpdate`). `t.Setenv` panics under `t.Parallel`, so
  making these tests independent means threading a home directory through the launch path.
  That is not a small change.
- **Not built: the build reusing the walk's replay.** A launch that builds replays the series
  twice, and handing the build the walk's finished tree would save the second replay. It would also
  skip what the second replay checks: that the walk's answer is not stale, the mirror refetch, and
  the lock that keeps the tree from changing between the two. Keeping those checks costs more than
  the replay it saves, so both replays stay.

**MEASURED on Linux, 2026-11-03** (this jail, 32 cores, other agents' load on the machine).
`internal/cli` started **23,852** `git` processes at `b2f9a0811`. Earlier work had already cut
that from 48,536, and the cut is not part of this change. After A it starts **20,017** (−16%):
`rev-list` went from 4,892 to 2,375 and `commit-tree` from 1,347 to 36. Wall time, with interleaved
runs of the two test binaries and no counting wrapper: before 415.5 s and 402.2 s, after 221.9 s and
354.6 s, every run passing. Load averages at the runs' starts were 12 to 37, from other agents. That
spread is the machine's load, not this change: on Linux a `git` start costs a few milliseconds, so
3,835 starts are seconds. These runs show no regression and measure no speedup. On the Mac, at 20–36 ms a start,
3,835 fewer starts are about 1.3 to 2.3 minutes (INFERRED from the per-start costs above, not
measured there).

```console
# A counting git first on PATH (each run's arguments, one line a run), then per-subcommand counts.
$ D=$YOLO_DURABLE_DIR/scratch/spawnlog; mkdir -p "$D/bin"
$ printf '#!/bin/bash\nprintf "%%s\\t%%s\\n" "$EPOCHREALTIME" "$*" >> "$GITCOUNT_LOG"\nexec /bin/git "$@"\n' > "$D/bin/git"; chmod +x "$D/bin/git"
$ GITCOUNT_LOG=$D/after.runs env -u YOLO_ALLOW_MISSING_PROGRAMS PATH="$D/bin:$PATH" \
    go test -short -count=1 -timeout 0 -json ./internal/cli > "$D/after.json"
$ wc -l < "$D/after.runs"
# Wall time: build both trees' test binaries (go test -c ./internal/cli) and run them alternately,
# each from its own internal/cli directory, with -test.short -test.count=1 -test.timeout=0.
```

**A flake seen once in four unit runs**, the second:
`TestEnvOverrideRefusesTheMacosUserLaunch` failed with `Refusing the macos-user launch: the
"openai-auth-broker" service (pack "openai-auth") did not start: listen tcp 127.0.0.1:44791:
bind: address already in use`, after the claude broker's front had logged `listening on
127.0.0.1:44791`. READ FROM CODE, the cause is production's: a served address is chosen by
listening on port 0 and closing the listener (`servedaddresses.go`), and a later listener in the
same launch can be handed that port before the daemon binds it. UNMEASURED: how often a real
launch is refused this way. **Fixed 2026-10-01:** a picked port is now held until the process that
serves it has it ([NC-D69](notch-convergence.md#NC-D69),
[HS-D26](../design/host-notch-services.md#HS-D26)), and
`TestAMacosUserDoorwaysPickedPortIsStillItsOwnWhenAnotherListenerAsksFirst` reproduces this flake
deterministically, by asking for the doorway's exact port before it opens.

## Where the time goes

### Unit tests: one package sets the wall time, and five of its tests are waiting on clocks

`go test` runs packages in parallel, so `internal/cli/run` sets the wall time. That package grew from
14.3 s and 457 tests on 09-03 to 40.8 s and 1064 tests now. Its tests run one at a time, and no test in
it calls `t.Parallel()`. The 15 slowest unit tests in the tree were all added between 08-13 and 09-25.

| Test(s) | Cost | Why | Evidence |
| :--- | :--- | :--- | :--- |
| `TestExternalServiceWaitsForCompleteEndpoint`, `TestExternalServiceRemovesStaleEndpoint` | 5.01 s each | `startExternalServiceHarness` never sets `o.ServiceReadyTimeout`, so these two tests wait for the production default. Other tests in the file set it to 300 ms. | [`hostservices_test.go:156`](../../internal/cli/run/hostservices_test.go#L156), [`:323`](../../internal/cli/run/hostservices_test.go#L323) |
| `TestWaitForRunningContainerGivesUpBounded` | 5.0 s | The loop checks its deadline with `o.Now()`, which the test can fake, but it sleeps with `time.Sleep`, which the test cannot. A faked clock therefore saves nothing. | [`lifecycle.go:310`](../../internal/cli/run/lifecycle.go#L310), [`stalecollision_test.go:106`](../../internal/cli/run/stalecollision_test.go#L106) |
| Two slow-span tests | ~3.1 s | The slow-span threshold is a fixed 1 s constant, so each test has to wait past it for real. | [`perf.go:85`](../../internal/perf/perf.go#L85) |
| Four bootstrap tests in `internal/entrypoint` | ~8 s | The fake tool directory has no `fc-cache`, so these tests run the real `fc-cache -f`. | [`shell.go:399`](../../internal/entrypoint/shell.go#L399) |
| Four refresh-lock tests in `internal/entrypoint` (added 09-25) | 11.2 s | They sleep. | measured, recipe 1 |

Together that is about 18.2 s of `internal/cli/run`'s 40.8 s, and about 19 s of
`internal/entrypoint`'s 28.6 s.

### The gate: slower tests, a second lint pass, and a cache that kept getting invalidated

- **Lint gained 43 s on 09-13**, when the darwin `go vet` and `staticcheck` pass was added
  (the `lint` recipe in the [`Justfile`](../../Justfile)). The Justfile comment gives the reason and puts the cost
  at "roughly doubles a COLD `just lint`".
- **`lint-ci` and `test-fast` run one after the other.** Running them concurrently with `just`'s
  `[parallel]` attribute saved about 7.5 s in a local trial (54.0 s to 46.5 s).
- **The Go test cache was rarely reused, for two reasons.**
  1. Git puts `libexec/git-core` first on `PATH` when it runs a hook, and many tests read `PATH`, so a
     hook run never reused the cache from a hand run. That hook was retired in `eb0af5ee`.
  2. Tests in `internal/cli/run` append to `internal/cli/run/.yolo/housekeeping.log`, a file inside
     the source tree. Its size was 269 kB on 09-27. Any change to it invalidates that package's cached
     results.
- **A fresh worktree pays the cold cost**: about 65 s for its first gate, against about 10 s warm.
- **`-trimpath` saves 13–14 s cold on `vet` and `staticcheck`, and is not used.** It cannot be used
  with `go test`, where 42 tests fail under it; and on the lint passes a cached finding replays the
  file path of whichever checkout produced it, so it can name a sibling worktree (found in review,
  2026-09-28). The gate's measured gain comes from `[parallel]` instead.

### Integration: the tests that already existed did not slow down

The 44 integration tests added since 09-24 cost 228 s. Six of them account for 209 s:

| Test | Cost |
| :--- | :--- |
| `TestImageCopyLockSerializesConcurrentLaunches` | 97.9 s |
| `TestAnAttachKeepsThePackTreeTheJailBootedWith` | 33.5 s |
| `TestArchiveDeltaRetryRecoversAnOverClaim` | 29.2 s |
| `TestAttachRefusesAJailThatCannotReceiveTheSelection` | 20.3 s |
| `TestPodmanHomeIsAPerJailReadOnlySkeleton` | 19.3 s |
| `TestAttachRestartsAnOlderJailAtATerminal` | 8.6 s |

On CI, the tests that build nix image variants take 104–147 s each, against 2–29 s locally. The likely
cause is a cold nix store on the CI runner, but that is inferred and has not been measured.

### Concurrency: overlapping runs fail, and the failures cost more than the runs

On 09-27, 6 of 16 full runs that overlapped another full run failed. Of 21 runs with nothing
overlapping, 1 failed. The failed runs cost about 3,173 s, and re-running them cost another 26 min.
Two tests failed:

- **`TestYoloCheckValidConfig`** ([`cli_test.go:119`](../../integration/cli_test.go#L119)).
  `yolo check` probes every running jail on the machine
  ([`sections_loopholes.go:405`](../../internal/cli/check/sections_loopholes.go#L405)), so this test
  also sees the jails a concurrent run started.
- **`TestImageCopyLockSerializesConcurrentLaunches`**
  ([`concurrentlaunch_test.go:498`](../../integration/concurrentlaunch_test.go#L498)). It holds the
  machine-wide image-copy lock and removes the jail image's names from the shared podman store, so a
  concurrent run both waits on that lock and loses the image it expected.

Both are isolated, and no suite-wide lock serializes runs ([OQ-TS1](#decision-ledger)):

- `bef9fc90` makes `TestYoloCheckValidConfig` set aside a failure row that names another run's
  jail. Any other failure still fails the test.
- `2a50babc` isolates `TestImageCopyLockSerializesConcurrentLaunches` with a cross-run lock of its
  own, which is not a suite-wide lock. Every integration test holds it shared, so two runs'
  ordinary tests still interleave. Only the tests that take machine-wide state over hold it
  exclusively and run alone: the image-copy lock test, and the openai-auth and aws-auth broker
  tests, which own a host daemon whose socket and port are fixed per machine.

### Frequency: the rule that raised it has been replaced

A memory rule written on 09-26 told every builder to run the integration suite. After that, each code
workflow ran it 4–6 times. [AGENTS.md](../../AGENTS.md#testing) now says to run the full suite once per
landing (`eb0af5ee`).

The pre-commit hook was in place from 09-20 to 09-27. It raised the cost of a commit from 0.1 s to
34–52 s. Across 558 commits that ran it, that came to 4.05 agent-hours, and 136 of those runs were on
a tree that had not changed since the previous run. The hook is now retired.

### Usage-limit stalls: larger turns, not slower ones

On 09-27, 5 h 45 m of 21 h 36 m was spent waiting on usage limits. The median model turn latency did
not change (5.0–5.3 s). What changed is the size of each turn: context grew from about 160k tokens to
about 255k, and effort went from high to xhigh. Spending less of the quota each hour is what reduces
these stalls, and the [orchestration rules](#orchestration-rules) are how.

## Targets

| Measure | Target | How it is reached |
| :--- | :--- | :--- |
| Unit `-short` wall time | ≤ 25 s | Removing 18.2 s of waits brings `internal/cli/run` to about 22.6 s, which then sets the wall time. |
| `just check-ci`, warm | ≤ 30 s | 54 s, minus about 18 s of faster tests, minus 7.5 s from `[parallel]`, is about 28.5 s. |
| Integration suite, local | ≤ 450 s | The concurrency fixes do not speed up a run that has nothing overlapping it. Getting here requires cutting the six new tests, and the 97.9 s test first. |
| Full integration runs | One per landing | Already the rule ([AGENTS.md](../../AGENTS.md#workflow) step 4). This plan makes that one run reliable when another is running. |

A target is met when [the measurement recipe](#measurement-recipe) reproduces it on a warm cache.
For the integration suite, that means the median of three runs with nothing else running.

## Work items being built now

Each item is owned by one partition, meaning one agent's set of files, so no two agents edit the same
file. The fixes are to tests and harnesses. None of them changes production behavior.

| Partition | Files it owns | What it fixes |
| :--- | :--- | :--- |
| **cli-run** | `internal/cli/run` | Sets a short `ServiceReadyTimeout` in `startExternalServiceHarness`. Makes `waitForRunningContainer`'s sleep go through the same seam as its clock, or gives the test a short deadline. Lets the slow-span tests use a lower threshold. Stops the package's tests writing `housekeeping.log` into the source tree. |
| **entrypoint** | `internal/entrypoint` | Adds a fake `fc-cache` to the bootstrap tests' tool directory. Shortens the four refresh-lock tests' sleeps, using the seams the code already has or a fake clock. |
| **misc-unit** | slow tests outside the two packages above | Points tests that list `/tmp` or `/dev/shm` directly (in `paths`, `loopholes` and `capture`) at a `t.TempDir()` root, where the code already has a way to pass one in. Reduces `hostservice`'s `TestASocketPathThatExistsAccepts`, which runs 2000 rounds with a tight `os.Stat` loop ([`bindready_test.go:18`](../../internal/hostservice/bindready_test.go#L18)). |
| **integration** | `integration/` | Makes `TestYoloCheckValidConfig` check only its own workspace's jail. Isolates the image-copy lock test from state a concurrent run shares. Cuts the six new tests where their own setup allows. |
| **gate-ci** | `Justfile`, `.github/workflows/ci.yml` | Puts `[parallel]` on `check-ci`, after confirming that the `just` CI installs supports the attribute (CI downloads the latest `casey/just` release, [`ci.yml:78`](../../.github/workflows/ci.yml#L78)) and that a failure still ends the recipe with a non-zero exit. Optionally runs the four lint passes in parallel. Tried `-trimpath` for `vet` and `staticcheck`, then dropped it (above). |

These items must not do three things:

- Add `t.Parallel()` inside `integration/`. That package is serial by rule
  ([AGENTS.md](../../AGENTS.md#testing)), and [`integration-parallelism.md`](integration-parallelism.md)
  explains why.
- Drop the darwin lint pass. [OQ-TS4](#OQ-TS4) asks about moving it, and until that is ruled it stays.
- Weaken what any sped-up test asserts. Shortening a wait is allowed. Removing the check that followed
  the wait is not.

## Orchestration rules

These rules come from the frequency, hook and usage-limit findings above. They change how agents are
run, not how the code is written.

1. **Run the full integration suite once per landing**, on the combined tree. It is not run once per
   builder or once per reviewer ([AGENTS.md](../../AGENTS.md#workflow) step 4).
2. **Land from one long-lived landing worktree that stays warm.** A fresh worktree's first gate costs
   about 65 s, against about 10 s warm.
3. **Do not poll.** Wait on the process, or use a background task that reports when it exits. Do not
   run a loop that re-checks status.
4. **Give each agent its own scratch log.** Concurrent agents must never tee into the same file.
5. **Run review agents at high effort, not xhigh.** Turn latency did not change on 09-27, but the
   quota each turn used did.
6. **Run mutation testing only on the packages the diff touches**, not on the whole tree.

## Out of scope

- Running tests in parallel inside `integration/`. [`integration-parallelism.md`](integration-parallelism.md)
  covers that, and it stays parked.
- The CI runners' cold nix store. Its cause is inferred, not measured, so there is nothing to fix yet.
- Model turn latency. It did not change.
- A variable that skips the store-changing integration tests outside a landing
  ([OQ-TS3](#decision-ledger)). A run that is not a landing already runs only the integration
  tests its change reaches, so the switch would save nothing and add a way to land with those
  tests skipped.

## Measurement recipe

Run these from a clean checkout of the commit being measured. For a past tree, use
`git worktree add "$S/tree" "$(git rev-list -1 --before=2026-09-03T23:59 main)"`. Set
`S=<scratch dir>` first. **Measurement recipe:** The recorded 2026-10-08 runner removed five environment variables before each command: `YOLO_VERSION`, `YOLO_HOST_LAYERS`, `YOLO_SERVICE_AWS_AUTH_ENDPOINT`, `AWS_CONTAINER_AUTHORIZATION_TOKEN` and `AWS_CONTAINER_CREDENTIALS_FULL_URI`. Recipe commands below spell out the same isolation rather than implying that only the two previously listed flags were unset.


```console
# 1. Unit wall time, per-package time, and the slowest tests. Run twice and keep the second (warm) run.
$ time env -u YOLO_VERSION -u YOLO_HOST_LAYERS \
    -u YOLO_SERVICE_AWS_AUTH_ENDPOINT \
    -u AWS_CONTAINER_AUTHORIZATION_TOKEN \
    -u AWS_CONTAINER_CREDENTIALS_FULL_URI \
    go test -short -count=1 -json ./... > "$S/unit.json"
$ jq -r 'select(.Action=="pass" and .Test==null) | "\(.Elapsed)\t\(.Package)"' "$S/unit.json" | sort -rn | head
$ jq -r 'select(.Action=="pass" and .Test!=null) | "\(.Elapsed)\t\(.Package)\t\(.Test)"' "$S/unit.json" | sort -rn | head -20
$ go test -short -list . ./internal/cli/run | rg -c '^Test'

# 2. The gate: warm (second of two runs) and cold (fresh caches).
$ time just check-ci
$ time env GOCACHE="$(mktemp -d)" STATICCHECK_CACHE="$(mktemp -d)" just check-ci

# 3. The integration suite, and its slowest tests (median of three runs, with no other suite running).
$ time env -u YOLO_VERSION -u YOLO_HOST_LAYERS \
    -u YOLO_SERVICE_AWS_AUTH_ENDPOINT \
    -u AWS_CONTAINER_AUTHORIZATION_TOKEN \
    -u AWS_CONTAINER_CREDENTIALS_FULL_URI \
    go test -count=1 -timeout 0 -json ./integration > "$S/integ.json"
$ jq -r 'select(.Action=="pass" or .Action=="fail") | select(.Test!=null) | "\(.Elapsed)\t\(.Action)\t\(.Test)"' "$S/integ.json" | sort -rn | head -20

# 4. CI step and job durations (needs a token that can read the repository).
$ gh run list --workflow ci.yml --branch main --limit 20 --json databaseId,createdAt,conclusion
$ gh run view <id> --json jobs --jq '.jobs[] | .name as $j | .steps[] | [$j, .name, .startedAt, .completedAt] | @tsv'

# 5. Full integration runs per day, counted from session transcripts (UTC dates).
$ fd -e jsonl . ~/.claude/projects/-workspace | xargs -d '\n' jq -r '
    select(.type=="assistant") | .timestamp as $t | .message.content[]?
    | select(.type=="tool_use" and .name=="Bash") | .input.command
    | select(test("(go test[^|;&]*\\./integration|just test( |$))") and (test(" -(run|list|short|c) ")|not))
    | $t[0:10]' 2>/dev/null | sort | uniq -c
```

The hook's cost, the usage-limit stalls, and the concurrency failure counts were each worked out once
from 09-27's session transcripts. They have no recipe here: the hook has been retired, and the other
two depend on how a particular day's agents were scheduled.

## Mac witness: the darwin `just done` fixes (2026-11-03)

The [W1 lab report's](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md) short-suite failures
were fixed on Linux (merged as `b208e4f51`): the symlinked-root bug in `scripts/completion-check.py`'s
`go_graph`, the GC-closed lease in `TestHostPiLaunchGetsItsOwnSessionHome`, the sleep in the bounded
command test's helper, the brokerhook start-up allowance, and one `SHORT_TEST_TIMEOUT` (45m) for
`test-fast` and `just done`, which had used Go's default 10m. The owner's Mac agent re-runs, on `main`,
and appends the results to that lab report:

1. `python3 scripts/test-go-packages.py` with the default `TMPDIR` (under `/var/folders`): all pass.
2. `go test -short -count=3 -run '^TestHostPiLaunchGetsItsOwnSessionHome$' ./internal/cli`: passes; it
   exercises the new `/dev/fd` close-on-exec check on darwin.
3. `go test -short -count=5 -run '^TestBoundedCommand' ./internal/hostservice`, then
   `pgrep -f hostservice.test`: passes, and no leaf process is left.
4. `go test -short -count=1 -run '^TestAHook' ./internal/brokerhook`: passes.
5. `python3 scripts/test-completion-check.py`: passes.
6. `just done` (the full `just check-ci`), under the same load as the report's [§9](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#9-the-short-suite-on-this-mac) if practical: the
   only expected failures are none (the four `kern.procargs2` tests were fixed in `f25911eb3`); report
   the `internal/cli` package's time.

*Run 2026-11-03 at `fd702e9fb`* ([lab report §10](../lab-reports/2026-11-03-mac-slice-12a-witness-w1.md#10-rerun-witness-w1b-and-the-just-done-fixes-witness)):
items 1–5 pass. `just done` failed on one load flake,
`TestSharedDemandPublicHostProcessesProducerInterruptSecondRemains` (a TempDir cleanup race; it
passes `-count=10` alone). `internal/cli` took 1574 s.

## Open Questions

1. <a id="OQ-TS2"></a>**OQ-TS2: Should `ci.yml` set `concurrency: cancel-in-progress`?** Today
   `ci.yml` has no `concurrency` block, so each push to a pull request runs the whole workflow again
   while the earlier run keeps going. At 1085 s for the x64 integration job, that adds up.

   <!-- vantage: question id=OQ-TS2 leaning="Yes for pull-request branches only, keyed on the branch. No for main, where every push's result is a record of that commit and a cancelled run would leave a commit that was never checked." -->

   _Leaning:_ Yes, but for pull-request branches only, with the concurrency group keyed on the branch.
   No for `main`: there, each push's result is the record for that commit, and a cancelled run would
   leave a landed commit that was never checked.

   **Answer:**
   > Ruled by the owner on 2026-11-03, not as leaned: one run per workflow and ref, `main`
   > included, so a newer push cancels the older run. Built in `101f766b8`; the comment above
   > `ci.yml`'s `concurrency` block records the ruling.

2. 💬 <a id="OQ-TS4"></a>**OQ-TS4: Should the darwin lint pass run in CI only, and not in the local
   `just check-ci`?** That pass has cost 43 s since 09-13. Taking it out of the local gate would cut
   that time for everyone, but a darwin-only finding would then first appear after a push.

   <!-- vantage: question id=OQ-TS4 leaning="No. The Justfile comment rules that the landing gate is the one that must not be blind to darwin-only files, and the pass is close to free on a warm cache. Parallelizing recovers much of the cold cost instead." -->

   _Leaning:_ No. The comment above the `lint` recipe's darwin pass in the [`Justfile`](../../Justfile) rules against it: the
   landing gate is the one that must not miss darwin-only files. On a warm cache the pass costs close
   to nothing. Running the lint passes in parallel gets much of the cold cost back
   without dropping it.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| <a id="TS-D1"></a>TS-D1 | **Maintainer ruling, a standing preference.** Slow agent work is answered by cutting duplicated work and slow tests, never by limiting how much runs at once. Given when a workflow that took hours drew the offer to run one workflow at a time: *"I don't want you to limit parallelism, I just want you to optimize things."* | 2026-09-27 | [Concurrency](#concurrency-overlapping-runs-fail-and-the-failures-cost-more-than-the-runs) |
| OQ-TS1 | **Answered by [TS-D1](#TS-D1), as leaned.** No machine-wide lock lets only one full integration suite run at a time: a lock that makes every other landing wait limits how much runs at once. Isolating the two tests that share state is the fix instead, the **integration** partition's work item, landed in `bef9fc90` and `2a50babc` | 2026-09-29 | [Concurrency](#concurrency-overlapping-runs-fail-and-the-failures-cost-more-than-the-runs), [Work items](#work-items-being-built-now) |
| OQ-TS3 | **Answered by the once-per-landing rule ([AGENTS.md Testing](../../AGENTS.md#testing)), as leaned: moot.** No variable skips the store-changing integration tests. A run that is not a landing already runs only the tests its change reaches, so the switch's one new effect would be a way to land with those tests skipped | 2026-09-29 | [Out of scope](#out-of-scope) |
