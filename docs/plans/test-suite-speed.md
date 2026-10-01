---
title: "Test suite speed"
date: 2026-09-27
status: in-review
stage: DECIDED
next: "Cut the waits the 2026-10-01 retake names (a 15 s and two 5 s tests in internal/cli/run, six refresh-timeout tests in internal/packsrc), then retake recipe 1 and three idle runs of recipe 3"
tags: [testing, ci, performance, plan]
summary: "Why the unit gate doubled and the integration suite grew by half in three weeks, what is being cut, the targets, and four questions for the maintainer, two of them answered."
---

# The suite got slower because tests were added, and the suite got run more often

**Status:** 2026-09-27. Evidence measured 2026-09-27 against `71114ecc` and its parents.
Five work items are ruled and being built, and some have landed, among them the **integration**
partition's two isolations ([OQ-TS1](#OQ-TS1)'s answer names the commits). Read in the tree on
2026-09-30, not re-measured: most of the other four partitions' fixes are in too
(`startExternalServiceHarness` takes a readiness deadline, the bounded-wait test fakes its clock,
the bootstrap tests carry a fake `fc-cache`, `TestASocketPathThatExistsAccepts` runs 500 rounds,
and `check-ci` is `[parallel]`), while `perf.SlowSpanThreshold` is still a fixed constant.
**Retaken 2026-10-01** at `d4e435a3`, on a machine other agents were using
([the retake](#retaken-2026-10-01-and-which-targets-hold)): the warm `check-ci` target holds on a
test-cache hit (6.4 s); the unit target does not (69.0 s, `internal/cli/run` 67.5 s), nor the
integration one (498.0 s, one run); and nothing of the five items as written is left, the misses
coming from tests added since. Four questions each
asked for one more lever. [OQ-TS1](#OQ-TS1) and [OQ-TS3](#OQ-TS3) were answered 2026-09-29, both as
leaned and both adding no lever ([Decision Ledger](#decision-ledger)). [OQ-TS2](#OQ-TS2) and
[OQ-TS4](#OQ-TS4) are still open, and none of the five work items waits on them. This follows the
precedent [`install-capture.md`](install-capture.md) set for a plan that owes work with an
additive question still open.

> **In short.** No existing test got slower. The time went into tests added since mid-August,
> several of which wait on real clocks, and into running the full suite 28 to 34 times a day.
> Cutting about 18 s of waits, isolating two integration tests from a concurrent run's state, and
> running the full suite once per landing brings the warm gate back under 30 s.

**Why it matters.** On 09-27, 5 h 45 m of a 21 h 36 m working day was spent stalled on usage limits,
and about 5 h went to the integration suite. Measured the same day.

**Start at [Where the time goes](#where-the-time-goes).** Each work item fixes one row there.

**Needs your ruling:** [OQ-TS2](#OQ-TS2), [OQ-TS4](#OQ-TS4).

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

## Measurement recipe

Run these from a clean checkout of the commit being measured. For a past tree, use
`git worktree add "$S/tree" "$(git rev-list -1 --before=2026-09-03T23:59 main)"`. Set
`S=<scratch dir>` first. The two `env -u` flags remove the in-jail variables that change `go test`
results.

```console
# 1. Unit wall time, per-package time, and the slowest tests. Run twice and keep the second (warm) run.
$ time env -u YOLO_VERSION -u YOLO_HOST_LAYERS go test -short -count=1 -json ./... > "$S/unit.json"
$ jq -r 'select(.Action=="pass" and .Test==null) | "\(.Elapsed)\t\(.Package)"' "$S/unit.json" | sort -rn | head
$ jq -r 'select(.Action=="pass" and .Test!=null) | "\(.Elapsed)\t\(.Package)\t\(.Test)"' "$S/unit.json" | sort -rn | head -20
$ go test -short -list . ./internal/cli/run | rg -c '^Test'

# 2. The gate: warm (second of two runs) and cold (fresh caches).
$ time just check-ci
$ time env GOCACHE="$(mktemp -d)" STATICCHECK_CACHE="$(mktemp -d)" just check-ci

# 3. The integration suite, and its slowest tests (median of three runs, with no other suite running).
$ time go test -count=1 -timeout 0 -json ./integration > "$S/integ.json"
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

## Open Questions

1. ✅ <a id="OQ-TS1"></a>**OQ-TS1: Should a machine-wide lock let only one full integration suite run at
   a time?** A lock would stop overlapping runs from failing, and it would do that without fixing each
   test. It would also make every other agent that reaches its landing wait for the current run to
   finish.

   <!-- vantage: oq id=OQ-TS1 -->

   _Leaning:_ No. A suite-wide lock works against the maintainer's standing preference not to limit
   parallelism ([TS-D1](#TS-D1)). Isolating the two tests that share state (the **integration**
   partition) fixes the cause, and the lock would only hide it.

   **Answer:**
   > **Answered 2026-09-29 by [TS-D1](#TS-D1)**, the maintainer's standing preference of
   > 2026-09-27, as leaned: no lock lets only one suite run at a time. A lock that makes every other
   > landing wait for the current run limits how much runs at once, which is what that preference
   > rules out.
   >
   > Isolating the two tests that share state stays the fix. It is the **integration** partition's
   > work item, and it has landed:
   >
   > - `bef9fc90` makes `TestYoloCheckValidConfig` set aside a failure row that names another run's
   >   jail. Any other failure still fails the test.
   > - `2a50babc` isolates `TestImageCopyLockSerializesConcurrentLaunches`. It adds a cross-run lock
   >   of its own, and that lock is not the one this question asked about. Every integration test
   >   holds it shared, so two runs' ordinary tests still interleave. Only the tests that take
   >   machine-wide state over hold it exclusively and run alone: the image-copy lock test, and the
   >   openai-auth and aws-auth broker tests, which own a host daemon whose socket and port are fixed
   >   per machine.

2. 💬 <a id="OQ-TS2"></a>**OQ-TS2: Should `ci.yml` set `concurrency: cancel-in-progress`?** Today
   `ci.yml` has no `concurrency` block, so each push to a pull request runs the whole workflow again
   while the earlier run keeps going. At 1085 s for the x64 integration job, that adds up.

   <!-- vantage: oq id=OQ-TS2 leaning="Yes for pull-request branches only, keyed on the branch. No for main, where every push's result is a record of that commit and a cancelled run would leave a commit that was never checked." -->

   _Leaning:_ Yes, but for pull-request branches only, with the concurrency group keyed on the branch.
   No for `main`: there, each push's result is the record for that commit, and a cancelled run would
   leave a landed commit that was never checked.

   **Answer:**
   > _(empty — fill in when decided)_

3. ✅ <a id="OQ-TS3"></a>**OQ-TS3: Should the integration tests that change the shared image store be
   skipped unless a variable turns them on, for runs that are not a landing?** Tests such as the
   image-copy lock and archive-delta tests are among the slowest in the suite, and they touch state
   that other runs share. The repository already uses this pattern once: `YOLO_TEST_REAL_PACK_INSTALLS`,
   which the `test` recipe in the [`Justfile`](../../Justfile) sets.

   <!-- vantage: oq id=OQ-TS3 -->

   _Leaning:_ No. The full suite now runs only at landing, so a run that is not a landing already
   executes only the tests its change reaches. A second switch would add another way to land with
   those tests skipped.

   **Answer:**
   > **Answered 2026-09-29 by the once-per-landing rule
   > ([AGENTS.md Testing](../../AGENTS.md#testing)), as leaned:** no variable, because the question
   > is moot. The full suite runs only at landing. A run that is not a landing already runs only the
   > integration tests that read what its change touched, picked with `go test -run`. So a
   > store-changing test runs there only when the change reaches it, and then it is a test that run
   > needs. The switch would save nothing on the runs it was meant for. Its one new effect would be
   > a way to land with those tests skipped.

4. 💬 <a id="OQ-TS4"></a>**OQ-TS4: Should the darwin lint pass run in CI only, and not in the local
   `just check-ci`?** That pass has cost 43 s since 09-13. Taking it out of the local gate would cut
   that time for everyone, but a darwin-only finding would then first appear after a push.

   <!-- vantage: oq id=OQ-TS4 leaning="No. The Justfile comment rules that the landing gate is the one that must not be blind to darwin-only files, and the pass is close to free on a warm cache. Parallelizing recovers much of the cold cost instead." -->

   _Leaning:_ No. The comment above the `lint` recipe's darwin pass in the [`Justfile`](../../Justfile) rules against it: the
   landing gate is the one that must not miss darwin-only files. On a warm cache the pass costs close
   to nothing. Running the lint passes in parallel gets much of the cold cost back
   without dropping it.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| <a id="TS-D1"></a>TS-D1 | **Maintainer ruling, a standing preference.** Slow agent work is answered by cutting duplicated work and slow tests, never by limiting how much runs at once. Given when a workflow that took hours drew the offer to run one workflow at a time: *"I don't want you to limit parallelism, I just want you to optimize things."* | 2026-09-27 | [OQ-TS1](#OQ-TS1) |
| [OQ-TS1](#OQ-TS1) | **Answered by [TS-D1](#TS-D1), as leaned.** No machine-wide lock lets only one full integration suite run at a time. Isolating the two tests that share state is the fix instead, the **integration** partition's work item, landed in `bef9fc90` and `2a50babc` | 2026-09-29 | [OQ-TS1](#OQ-TS1), [Work items](#work-items-being-built-now) |
| [OQ-TS3](#OQ-TS3) | **Answered by the once-per-landing rule ([AGENTS.md Testing](../../AGENTS.md#testing)), as leaned: moot.** No variable skips the store-changing integration tests. A run that is not a landing already runs only the tests its change reaches, so the switch's one new effect would be a way to land with those tests skipped | 2026-09-29 | [OQ-TS3](#OQ-TS3) |
