---
title: "Mac run at 65a532431: the openat ENOENT retry fixes the mcpauth two-broker test; TestRemovedDirectoryStillReportsENOENT fails because a removed directory's descriptor keeps nlink=2 on APFS; RC-D74 passes"
date: 2026-11-24
tags: [lab-report, macos, paths, mcp-auth, config-edit]
summary: "At main 65a532431 on the owner's Mac. After a314dc882, TestOutcomeTwoBrokersAdoptOneSingleUseToken passes 10 of 10 as separate processes, 10 of 10 under -race, and 20 of 20 under -count=20. But `go test ./internal/paths` fails TestRemovedDirectoryStillReportsENOENT (9 openat calls, want 1). On macOS 26.5 APFS, fstat of an rmdir'd directory's descriptor still reports nlink=2, so fdIsLiveDir is true for a removed directory and the retry runs its full count. `yolo config unset env.set.NAME` now deletes the key (RC-D74). The updated Apple Container builder script reuses the running builder."
---

# Mac run at `65a532431`

**Run 2026-11-24 on the owner's Mac** (macOS 26.5, darwin/arm64, APFS, Go 1.26.7), at main
`65a532431`. Go tests and a `yolo` built with `go build ./cmd/yolo` ran from the checkout. Nothing
in yolo was changed.

## Observed

| Check | Result |
| :--- | :--- |
| [`mcpauth-records-lock-enoent-darwin`](../plans/BACKLOG.md#mcpauth-records-lock-enoent-darwin), after `a314dc882` | **Fixed on the Mac**, see [§1](#1-the-openat-retry) |
| `go test ./internal/paths` | **Fails** `TestRemovedDirectoryStillReportsENOENT`, see [§2](#2-a-removed-directorys-descriptor-keeps-nlink2) |
| [RC-D74](../reference/config/rulings.md#RC-D74), `yolo config unset env.set.<NAME>` | **Passes**: the key and its empty table are deleted, and the file ends at 0 bytes |
| `scripts/mac-ac-linux-builder.sh` after `eef03bb0b` | **Runs**: it reuses the running `yolo-ac-builder` (`builder at 192.168.65.3`) and prints the spec. Its new stopped-leftover path was not exercised |
| `claudeMdExcludes` through `yolo host -- claude --settings` ([plan](../design/workspace-instruction-files-plan.md)) | **Not run**: it needs a live `claude` session, which is the owner's |

### 1. The openat retry

`TestOutcomeTwoBrokersAdoptOneSingleUseToken` (`internal/mcpauth`), built from main:

| Run | Failed |
| :--- | :--- |
| a compiled binary, 10 separate processes, `-test.count=1` | 0 of 10 (it was 10 of 10 at `042a00ab2`) |
| the same with `-race` | 0 of 10, and no `WARNING: DATA RACE` |
| `-test.count=20` in one process | 0 of 20 |

`go test -count=1 ./internal/mcpauth` passes.

### 2. A removed directory's descriptor keeps `nlink=2`

`go test -count=1 ./internal/paths` fails:

```text
--- FAIL: TestRemovedDirectoryStillReportsENOENT (0.00s)
    createrace_unix_test.go:92: openat calls = 9, want 1 (a removed directory is not retried)
```

The retry in `openNoFollowLeaf` (`internal/paths/statefile_open_unix.go:59`) runs only while
`fdIsLiveDir` holds. That check (`statefile_open_unix.go:26`) is
`st.Mode&unix.S_IFMT == unix.S_IFDIR && st.Nlink > 0`. A direct measurement on this Mac, in Python
against a fresh directory:

```text
before rmdir: nlink=2
after  rmdir: nlink=2 mode=40755
openat(O_CREAT) in removed dir: No such file or directory
```

So on APFS, `fstat` of a descriptor on an `rmdir`'d directory still reports `nlink=2`, while an
`openat` with `O_CREAT` through it returns ENOENT. `Nlink > 0` therefore never tells a removed
directory from a live one on darwin. The retry runs its full `createRetries` against a directory
that is gone, which is bounded, so it does not hang, and the test fails.

`paths.OpenRootCreate` (`internal/paths/rootcreate.go`) uses a different check: it retries while
`r.Stat(".")` is a directory. The measurement above shows a removed directory's descriptor still
reporting a directory mode (`40755`), so that check would also pass for a removed directory on
darwin. `internal/toolskills/snapshot.go` has its own ENOENT loop, which was not read. Neither was
run separately.

## Fixed and committed

Nothing in yolo. This report is the only commit.

## Open

1. [§2](#2-a-removed-directorys-descriptor-keeps-nlink2): darwin needs another liveness test for
   the anchor directory. One possibility, untested here: compare the descriptor's `fstat`
   `(dev, ino)` with an `lstat` of the root's path.
2. The `claudeMdExcludes` witness of the instruction-files plan needs a live `claude` session, so
   it waits for the owner.

## Evidence

In `/Users/Shared/yolo/evidence/2026-11-24c/`: `HEAD.txt`, `mcpauth-plain.log`, `mcpauth-race.log`,
`mcpauth-x20.log`, `paths-mcpauth.log`, `rmdir-nlink.txt`, `rcd74.log`, `builder-spec.txt` and
`builder-stderr.txt`. Everything ran against `65a532431`.
