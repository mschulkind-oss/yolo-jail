---
title: "CI run 45494660596 at e42052373: check-macos fails three groups of tests added 2026-11-24, all reproduced on the owner's Mac; check-go fails the instruction-files group too"
date: 2026-11-24
tags: [lab-report, macos, ci, resource-groups, entrypoint, paths]
summary: "CI at e42052373 fails check-go, check-macos (1) and integration-complete. On the owner's Mac, each check-macos failure reproduces: the four repo-instructions boot tests (mkdir /workspace is a read-only file system on macOS, and permission denied on check-go's Linux), TestPlacedResourceGroupIsRecordedForTheLaunch (resourcegroupplace.go:99 calls resourcegroups.CheckDelegatedIO with no seam, and the !linux stub always refuses), and TestRemovedDirectoryStillReportsENOENT (the APFS nlink=2 finding already filed). All three groups were added on 2026-11-24."
---

# CI run `45494660596` at `e42052373`

**Checked 2026-11-24 on the owner's Mac** (macOS 26.5, darwin/arm64, Go 1.26.7), at main
`e42052373`. There were no new commits since the previous Mac report, so this run looked at CI
instead. Nothing in yolo was changed.

CI run [`45494660596`](https://github.com/mschulkind-oss/yolo-jail/actions/runs/45494660596) failed
`check-go`, `check-macos (1)` and `integration-complete`. The earlier run at `9d002cafd`
([`45452376220`](https://github.com/mschulkind-oss/yolo-jail/actions/runs/45452376220)) failed
differently. Its failures were the `TestClassifierNegotiation…` tests, which do not fail at
`e42052373`; `abc9bfc0f` ("count classifier calls without trusting wc's padding") is their
apparent fix. Apple Container parity passed at both commits.

## Observed

| Failure | `check-macos (1)` | `check-go` (Linux) | On this Mac | Added |
| :--- | :--- | :--- | :--- | :--- |
| Four repo-instructions boot tests, see [§1](#1-the-repo-instructions-boot-tests) | fails | fails | **reproduces** | `94a6f14c8` and its series, 2026-11-24 |
| `TestPlacedResourceGroupIsRecordedForTheLaunch`, see [§2](#2-testplacedresourcegroupisrecordedforthelaunch) | fails | passes | **reproduces** | `5c04ce72e`, 2026-11-24 |
| `TestRemovedDirectoryStillReportsENOENT`, see [§3](#3-testremoveddirectorystillreportsenoent) | fails | passes | **reproduces** | `a314dc882`, 2026-11-24 |

### 1. The repo-instructions boot tests

These are `TestBootRendersClaudesExclusionsAnchoredAtTheWorkspaceBesideTheUsersOwn`,
`TestBootRendersNothingForAKeepLaunch`, `TestBootHandsPiItsFlagAndTheBriefingBackAndAgyNothing`
and `TestBootWritesCodexsProjectDocLimitAsAnInteger` (`internal/entrypoint/repoinstructions_test.go`).

On macOS each fails with:

```text
boot failed: [configure_claude_settings: mkdir /workspace: read-only file system configure_codex_config: mkdir /workspace: read-only file system]
```

`check-go` shows the same failure with `permission denied`. So the boot under test writes to the
literal `/workspace` rather than to a fixture directory. This group is not specific to the Mac.

### 2. `TestPlacedResourceGroupIsRecordedForTheLaunch`

The test is at `internal/cli/run/resourcegroups_test.go:321`. On this Mac it prints:

```text
a placeable mapping refused:
I/O child plan for the selected group:
8:0 rbps: finite assigned 101 agent=5 batch=96
…
Resource group "batch.slice" selected via default could not be placed; this launch was not started: delegated cgroup-v2 I/O is unsupported on this platform; run yolo check --no-build
```

The test sets `IsLinux: true` and injects `ResourceGroupIOPaths` and `ResourceGroupPlace`. But
`internal/cli/run/resourcegroupplace.go:99` calls `resourcegroups.CheckDelegatedIO(iopaths,
os.ReadFile)` directly. On a `!linux` build that is `internal/resourcegroups/authority_other.go`,
which always returns `errUnsupportedDelegation`. So off Linux the test cannot reach the placement
it sets up. Either the check needs a seam the test can fill, or the test belongs to Linux only.

### 3. `TestRemovedDirectoryStillReportsENOENT`

This is the same failure as the previous Mac report: `openat calls = 9, want 1`, because an
`rmdir`'d directory's descriptor keeps `nlink=2` on APFS.

## Fixed and committed

Nothing in yolo. This report is the only commit.

## Open

1. [§1](#1-the-repo-instructions-boot-tests): make the repo-instructions boot tests write under a
   fixture directory. This fails both CI jobs.
2. [§2](#2-testplacedresourcegroupisrecordedforthelaunch): inject the delegated-I/O check, or tag
   the test `linux`.
3. [§3](#3-testremoveddirectorystillreportsenoent): the darwin liveness check from the previous
   report.

## Evidence

In `/Users/Shared/yolo/evidence/2026-11-24d/`: `HEAD.txt`, `rg.log` and `repoinstr.log`. The CI
logs are in the run linked above. Everything ran against `e42052373`.
