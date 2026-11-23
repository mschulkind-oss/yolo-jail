---
title: "Mac run at fe37424b3: Darwin trace publication passes; 11-13's config-edit and stores items are fixed; test-fast fails 18 tests, 16 from a `yolo host` caller's env; an mcpauth test fails under -count"
date: 2026-11-23
tags: [lab-report, macos, testing, perf, config-edit, mcp-auth]
summary: "At main fe37424b3, on the owner's Mac with the installed yolo at the same commit: the Darwin RENAME_EXCL trace publication passes natively; `yolo config unset` of macos_user_shared_tmp and of env.secrets now leaves a clean file; TestMacosUserStoresListsTheSandboxAccount passes. New: the config-edit diff prints a +/- marker on only the first line of a multi-line hunk, and `yolo config unset env.set.X` refuses and says to hand-edit. `just test-fast` fails 18 tests: 16 are deterministic when the caller's env carries a `yolo host` session's YOLO_* variables, TestOutcomeTwoBrokersAdoptOneSingleUseToken fails 39 of 40 repetitions under -count=20 but passed in the second full run, and 2 are load flakes. `just done`'s full gate adds a callbroker failure, a matched-bundle Exec format error and a pre-existing vantage link error. Round two: the callbroker failure is a load flake, the matched-bundle error is a darwin-only test-fixture defect with a one-line fix, W-PH1, W-PH5 and W-PH4's dry run still pass, and W-GB1 is blocked on a stopped podman machine. The account-mode provisioning re-check did not run."
---

# Mac run at `fe37424b3`

**Run 2026-11-23 on the owner's Mac** (darwin/arm64, Go 1.26.7), at main `fe37424b3`, from a
Claude Code session that runs under `yolo host`. The installed `yolo` is
`0.19.0+61.gfe37424b3605` (`build kind: installed`), the same commit as HEAD, so no dev bundle
was needed. Nothing in yolo was fixed. The owner's config was not touched: every config edit ran
against a scratch `HOME` or a scratch workspace, both deleted afterwards.

This session chose its own checks. Nothing was pending in `yolo approve`, and there were no
`host-run` requests. The checks were:

- the Darwin half of [startup tracing](../design/startup-tracing-plan.md)'s pending publication,
  which that plan says no native Mac run has yet claimed;
- open items 1, 2, 3 and 5 of [the 11-13 report](2026-11-13-mac-keychain-tmp-stores-bedrock.md#open);
- a native `just test-fast` as a baseline.

## Observed

| Check | Result |
| :--- | :--- |
| Darwin trace publication (`rename_noreplace_darwin.go`) | **Passes**: all 26 `TestTraceStore*` tests, see [§1](#1-darwin-trace-publication) |
| `yolo trace` on the host | Lists nothing and says collection "is not yet wired in this build", as designed |
| 11-13 item 2: `yolo config unset macos_user_shared_tmp --user` | **Fixed**: it removes the line, and the file is back to its starting bytes |
| 11-13 item 5: `unset` leaves whitespace and an empty table | **Fixed**: two `env.secrets.set` unsets leave a 0-byte file. No secret value was printed |
| 11-13 item 3: `TestMacosUserStoresListsTheSandboxAccount` | **Passes** on this Mac (`executed=1 skipped=0`) |
| 11-13 item 1: account-mode provisioning loses `yolo` | **Not run**: the session's permission classifier refused the launch, see [§4](#4-account-mode-provisioning-not-re-checked) |
| **New:** the config-edit diff's markers | **Wrong**: only the first line of a multi-line hunk gets `+`/`-`, see [§2](#2-the-config-edit-diff-marks-only-a-hunks-first-line) |
| **New:** `yolo config unset env.set.<NAME>` | **Refused**, and it tells you to hand-edit the file, see [§3](#3-yolo-config-unset-envsetname-is-refused) |
| `just test-fast` | **Fails 18 tests in 6 packages**, see [§5](#5-just-test-fast-on-darwin) |

### 1. Darwin trace publication

`internal/perf/rename_noreplace_darwin.go` calls `unix.RenameatxNp(…, unix.RENAME_EXCL)`.
`PublishPending` uses it at `internal/perf/tracestore.go:419`, to expose `.meta-slot` as
`meta.json`. `go test -count=1 -v -run TestTraceStore ./internal/perf` passed 26 tests. The one
skip was `TestTraceStoreProcessHelper`, which is only a re-exec helper.

`TestTraceStorePendingPublicationOnceAndConflict` requires `TracePublished` and a 452-byte
`meta.json`. The other-target stub declines publication, so a pass means the Darwin rename itself
ran and worked here on APFS. It did not quietly fall back. This covers the Darwin compile and
execution half that U6b2 left unclaimed. It does not cover production collection, which the CLI
says is not yet wired.

### 2. The config-edit diff marks only a hunk's first line

Against an empty project file:

```text
$ yolo config set env.secrets.set.AWS_ACCESS_KEY_ID '"…"' --project
@@
+[env.secrets.set]
AWS_ACCESS_KEY_ID = "<redacted>"
…
$ yolo config unset env.secrets.set.AWS_SECRET_ACCESS_KEY --project   # the last key
@@
-[env.secrets.set]
AWS_SECRET_ACCESS_KEY = "<redacted>"
```

In the second edit, the key that was removed prints like an unchanged context line, although the
file ends empty. A plain `env.set.PUBLIC_A` shows the same thing (`+[env.set]` then an unmarked
`PUBLIC_A = "a"`), so redaction is not the cause.

**Cause, from reading the code:** `writeConfigEditHunks`
(`internal/cli/configeditverbs.go:413`) writes the `-` or `+` once, in front of the whole
multi-line `hunk.Before` or `hunk.After` (lines 417 and 423), not in front of each line.

### 3. `yolo config unset env.set.<NAME>` is refused

After `yolo config set env.set.PUBLIC_A '"a"' --project` succeeds, the matching unset refuses:

```text
cannot unset config key "env.set.PUBLIC_A" in selected file "…/yolo-jail.toml": this key's removal requires updating both env.set and env.unset; no multi-key edit is available; edit the value manually in that file, then run `yolo check` in this workspace; …
```

The refusal comes from `internal/cli/configeditverbs.go:550`. `env.secrets.set.<NAME>` unsets
fine. AGENTS.md says advice names a verb, not a file to hand-edit, and no ruling in
[`config/rulings.md`](../reference/config/rulings.md) accepts this exception.

### 4. Account-mode provisioning, not re-checked

The plan was to repeat 11-13's five `YOLO_SETUP=seatbelt-yolo-account yolo -- true` launches in a
fresh workspace. Since then, `b640bd144` has changed the stop to name the missing staged command
directory. The first launch was refused by the session's permission classifier as a destructive or
unwanted action, so none ran. The re-check needs the owner either to run it or to allow it.

### 5. `just test-fast` on darwin

`just test-fast` exited 1. 176 packages printed `ok`, and 18 tests failed
in 6 packages. Each failure was rerun alone, three ways: with every `YOLO_*` variable removed
(`env -u …`), with the session's env kept, and, for one test, 20 times each way.

**16 are deterministic, and the cause is the caller's env.** This session's shell carries a
`yolo host` launch's variables: `YOLO_JAIL=1`, `YOLO_USE_INSTALLED=copilot`,
`YOLO_ALLOW_OLDER_UPSTREAM=yolo-packs/pi-fork/pi`, `YOLO_ALLOW_MISSING_PROGRAMS=1`,
`YOLO_USE_PROFILES`, `YOLO_PROVIDERS` and `YOLO_PROFILES`. The tests inherit them, and their
expected refusal text no longer matches. For example, a refusal that prints
`YOLO_USE_INSTALLED=copilot,chrome-devtools-mcp` where the test expects
`YOLO_USE_INSTALLED=chrome-devtools-mcp`, or an extra `YOLO_ALLOW_OLDER_UPSTREAM` warning line.
All 16 pass without the variables and fail with them:

- `internal/cli` (6): `TestAHostAgentLaunchSaysWhenItsMCPServersProgramCannotBeInstalled`,
  `TestARefusedHostLaunchLeavesItsLineAndItsRefusal`,
  `TestAHostLaunchRefusesWhenANewerVersionOfADeclaredProgramFailsToInstall`,
  `TestTheApplyOffExtensionRefusalListsEveryHatch`,
  `TestAHostLaunchRefusesWhenADeclaredProgramCannotBeInstalled`,
  `TestAHostLaunchSaysItWillNotInstallOverANewerYolosCopy`;
- `internal/entrypoint` (7): `TestAFailedUpdateSaysWhatEachModeDoesNext`,
  `TestAFailedBackgroundRefreshRefusesWhenTheRetryFails`,
  `TestAStaleServerWhoseNewerVersionFailsRefuses`, `TestANewerNpmVersionThatFailsToInstallRefuses`,
  `TestAMovedPinThatFailsToInstallRefusesOfflineAndAHoldRunsIt`,
  `TestANativeVerbThatFailsIsJudgedByItsUpstream`,
  `TestAFailedPrelaunchRefreshWhoseRegistryAnswersRefuses`;
- `internal/cli/run` (2): `TestOrdinaryPreparationRefusalPrintsItsOwnBlock`,
  `TestTheMissingBuildRefusalPrintsTheAdvancesRecovery`.

CI and a fresh jail do not set these variables, so neither sees this. Any agent running the landing
gate from a `yolo host` session does. Whether that is a test defect (the tests should clear the
`YOLO_*` dials they read) or an unsupported caller has not been decided.

**1 fails under repetition, not because of env.** `TestOutcomeTwoBrokersAdoptOneSingleUseToken`
(`internal/mcpauth/outcome_concurrency_test.go:28`) fails with
`slack: openat records.lock: no such file or directory`. With `-count=20` in one process, it
failed 20 of 20 repetitions with the env and 19 of 20 without it. Single runs were mixed: it
failed in the first full run, passed alone twice, and passed in `just done`'s full run
([§6](#6-just-dones-full-gate)). So the trigger may be state shared across `-count` repetitions
in one process rather than, or as well as, a race between the two brokers. The lock is opened
by `paths.OpenLockFileBeneath`, which passes `O_RDWR|O_CREATE`, through `openNoFollowLeaf`
(`internal/paths/statefile_open_unix.go:38`). An `openat` with `O_CREAT` returns ENOENT when the
directory it is anchored on is gone. So one hypothesis is that something removes or replaces the
canonical directory while a broker opens its lock. This was not confirmed. Whether
Linux CI also fails was not checked.

**2 are load flakes.** Each failed once during the full run and passed alone, with the env and
without it:

- `TestGitPreflightEOFWaitsForOrdinaryExitStatus` (`internal/gitremote`): `context deadline
  exceeded (1.000812709s)`. Its pass condition is a 1-second `context.WithTimeout`, which is the
  "short budget the code has to beat on a loaded machine" that AGENTS.md's testing rules forbid.
- `TestDecisionInterruptsAnInFlightCLIRatherThanKillingIt` (`internal/approvalnotify`):
  `status=-1 out=""`, meaning the CLI was killed instead of interrupted. The test waits on events;
  whatever grace period `Decision` allows between interrupt and kill was not examined.

### 6. `just done`'s full gate

After the report was committed, `just done` found no verified ancestor and ran the full
`just check-ci` with `vantage-check` over 225 changed documents. It **failed**, and nothing was
recorded. All 16 env tests from [§5](#5-just-test-fast-on-darwin) failed again, and mcpauth
and both load flakes passed. It also showed three things the first run did not:

- `TestSelectedHTTPWriteRevokedReadOrChangedNativeParentCannotSpend/pack_deselected`
  (`internal/callbroker`, `http_host_test.go:1284`): `rc=64` and *"HTTP broker refused: This is a
  bug in the selected pack's HTTP classifier"*. Rerun alone in [§7](#7-round-two), it is a load
  flake.
- `scripts/matched-bundle.py` (`_run_fresh_executable`, line 297) raised
  `OSError: [Errno 8] Exec format error` running a staged `bundle/bin/yolo`. It is a darwin-only
  fixture defect, see [§7](#7-round-two).
- `lint-ci` failed on one vantage error, which this report did not cause:
  `docs/research/toml-scoped-pack-imports.md:69` links `#L695-L738` of
  `internal/hostskills/deliver.go`, which has 719 lines. The new report raised no finding.

### 7. Round two

The owner asked for a second pass. Everything still ran against `fe37424b3`.

**The callbroker failure is a load flake.**
`go test -short -count=3 -run '^TestSelectedHTTPWriteRevokedReadOrChangedNativeParentCannotSpend$' ./internal/callbroker`
passed 3 of 3 with the session's env and 3 of 3 without it.

**The matched-bundle failure is a darwin-only fixture defect, and it fails every time.**
`python3 -I scripts/test_matched_bundle.py SnapshotTests.test_copied_build_scripts_run_from_frozen_tree_after_live_edit`
fails alone.

- The fake stage script in that test (`scripts/test_matched_bundle.py`) writes the fake launcher
  script only to `bin/linux-<arch>/yolo`.
- `_fake_helper_dirs` (line 58) writes the bytes `helper` to every name in `bin/darwin-<arch>/` on
  a darwin host, and that list includes `yolo`.
- Since `0dc56882e` ("ship the darwin host yolo in every bundle"), the launcher on darwin is the
  darwin directory's `yolo`, so `validate_pair` runs `helper` and gets ENOEXEC.

As a check only, writing the launcher to `bin/{goos}-{goarch}/yolo` instead (one line; on Linux
`goos` is `linux`, so nothing changes there) makes the test pass and the whole file report `OK`.
Reverted, the file fails again. The change was reverted and is not committed, so it is left for
the agent that owns this.

**The pinned-host-helper witnesses still pass on the current install**
([pinned-host-helpers.md](../reference/pinned-host-helpers.md#mac-witnesses); they last passed on
11-10 at `5a69dd0b4`):

- [W-PH1](../reference/pinned-host-helpers.md#w-ph1): `bin/darwin-arm64/` lists `yolo`,
  `yolo-jaild`, `yolo-log`, `yolo-ps` and `yolo-serial`, and `cmp` against `~/.local/bin/yolo`
  prints `same`.
- [W-PH5](../reference/pinned-host-helpers.md#w-ph5): `TestProcessExesNamesTheRunningBinary`
  prints `ok`.
- [W-PH4](../reference/pinned-host-helpers.md#w-ph4), the dry-run half only:
  `yolo run --setup seatbelt-self --dry-run` in a scratch workspace printed the plan and
  `✓ all plan invariants hold`, and exited 0.

W-PH2 and W-PH3 need a running `podman-self` jail, and W-PH4's live half needs a `seatbelt-self`
session, so none of those ran.

**[W-GB1](../reference/git-broker.md#a-mac-witness-still-to-run-w-gb1) is blocked, because the
podman machine is stopped.** HEAD contains `287479402`, so it qualifies. But
`podman machine list` shows `podman-machine-default` (applehv) last up 4 weeks ago, and step 1's
`podman info` cannot connect (`dial tcp 127.0.0.1:61044: connect: connection refused`). Starting
the owner's VM was left to the owner.

## Fixed and committed

Nothing in yolo. This report and its updates are the only commits.

## Open

1. [§5](#5-just-test-fast-on-darwin), mcpauth: `TestOutcomeTwoBrokersAdoptOneSingleUseToken`
   fails nearly every repetition under `-count=20` on this Mac. Find the cause, and check Linux CI.
2. [§5](#5-just-test-fast-on-darwin), the env: decide whether tests must clear inherited `YOLO_*`
   dials, which makes 16 tests a defect, or whether the landing gate must not run from a `yolo
   host` session, which makes it a documented precondition.
3. [§2](#2-the-config-edit-diff-marks-only-a-hunks-first-line): mark each line of a multi-line
   hunk.
4. [§3](#3-yolo-config-unset-envsetname-is-refused): give `env.set` removal a verb, or rule the
   exception.
5. [§5](#5-just-test-fast-on-darwin), the flakes: make `TestGitPreflightEOFWaitsForOrdinaryExitStatus`
   event-driven, and look at `Decision`'s interrupt-to-kill grace.
6. [§7](#7-round-two): land the one-line darwin fix to
   `test_copied_build_scripts_run_from_frozen_tree_after_live_edit`'s fixture. Separately, fix the
   out-of-range line anchor in `docs/research/toml-scoped-pack-imports.md`
   ([§6](#6-just-dones-full-gate)).
7. [§4](#4-account-mode-provisioning-not-re-checked): 11-13 item 1 still needs a run, by the owner
   or with the owner's permission.
8. [§7](#7-round-two): W-GB1 steps 1 to 4, W-PH2, W-PH3 and W-PH4's live half need the podman
   machine started and a live launch.

## Evidence

In `/Users/Shared/yolo/evidence/2026-11-23/`: `perf-tracestore.log`, `config-unset.log`,
`secrets-unset-raw.log`, `public-env-diff.log`, `public-env-unset.log`, `su14-test.log`,
`test-fast.log`, `failed-by-pkg.txt`, `rerun-clean-env.log`, `rerun-dirty-env.log`,
`rerun-dirty-env-2.log`, `mcpauth-x20-dirty.log`, `mcpauth-x20-clean.log`, `just-done.log`, `callbroker-dirty.log`, `callbroker-clean.log`, `matched-bundle-patched.log`, `matched-bundle-patched-full.log`, `matched-bundle-unpatched-full.log`, `pinned-helpers.log`, `w-ph4-dryrun.log` and `w-gb1-step1.txt`. Everything ran
against `fe37424b3`.
