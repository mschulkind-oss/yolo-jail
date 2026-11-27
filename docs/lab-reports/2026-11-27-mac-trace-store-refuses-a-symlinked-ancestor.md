---
title: "CI check-macos at 447f52051: the two new invocation-trace tests fail because the trace store declines any root whose ancestor is a symlink, and every darwin t.TempDir() is under /var"
date: 2026-11-27
tags: [lab-report, macos, ci, tracing, darwin-paths]
summary: "check-macos (1) and (2) fail TestRunRunInvocationTraceFinalizesWithNoIndicator and TestRunRunInvocationTraceOutlivesTitleRestore ('the launch left no launcher lane'). Both fail on the owner's Mac too. The launch prints 'trace recording declined: storage ownership, locking, accounting, or room is unavailable'. A temporary caller report traced it to openStoreRoot (internal/perf/tracestore.go:482-484), which Lstats each ancestor of the traces root and declines one that is not a directory. On macOS /var is a symlink, so a HOME under $TMPDIR (/var/folders/…) records no trace. The same HOME through /private/var, or under /Users/Shared, records one."
---

# CI `check-macos` at `447f52051`: the trace store declines a symlinked ancestor

**Checked 2026-11-27 on the owner's Mac** (macOS 26.5, darwin/arm64, Go 1.26.7), at main
`447f52051`. Nothing in yolo was changed. One temporary edit, a caller report in
`traceDeclined`, was built into a scratch binary and reverted before anything else ran.
`git status` was clean after.

## Observed

### 1. The CI failure, reproduced

CI run [`46049903881`](https://github.com/mschulkind-oss/yolo-jail/actions/runs/46049903881)
fails `check-macos (1)` and `check-macos (2)`. Each fails one of two tests in
`internal/cli/invocationtracewiring_test.go`, added by `449c16ab6` ("own the launcher's startup
trace"):
- `TestRunRunInvocationTraceFinalizesWithNoIndicator`: `invocationtracewiring_test.go:142: the
  launch left no launcher lane: file does not exist`;
- `TestRunRunInvocationTraceOutlivesTitleRestore`: `:102`, the same message.

**On this Mac both fail, run alone** (`go test -short -count=1 -run '^TestRunRunInvocationTrace'
./internal/cli`, `trace-tests.txt`).

### 2. The cause: `openStoreRoot` refuses a symlinked ancestor

A temporary copy of `TestRunRunInvocationTraceFinalizesWithNoIndicator`, printing what the
launch wrote (`diag.txt`), shows:
- the test's `HOME` is `/var/folders/j6/…/T/tt…/TestZZDiagTrace…/001`;
- the launch's stderr is `yolo: trace recording declined: storage ownership, locking,
  accounting, or room is unavailable; keep the legacy timing log and ask the yolo maintainer to
  inspect the host trace store`;
- no file exists under `logs/traces`.

`launcherLane` returns `os.ErrNotExist` whenever its glob does not match exactly one file,
which is where the test's "file does not exist" comes from.

**The same launch by hand,** `yolo -- true` built at `447f52051`, with `env -i`, an empty PATH
and `YOLO_TIMING=1` (`replay-paths.txt`):

| HOME | Launcher lanes written |
| :--- | ---: |
| `$TMPDIR/trc.XXXX/home`, which is `/var/folders/…/T//trc.…/home` | **0**, and the decline line printed |
| the same directory resolved, `/private/var/folders/…/home` | 1 |
| `/Users/Shared/yolo/evidence/2026-11-27a/trc.…/home` | 1 |

**Which check declines.** The temporary caller report (`diag-caller.txt`), with the patch's
five-line offset removed, points at:
- `OpenTraceStore` → `openStoreRoot(root)`, `internal/perf/tracestore.go:516`;
- the ancestor walk in `openStoreRoot`, lines 476–485, which is commented *"No symlink
  guessing, including ancestors of the explicitly supplied private root"*:

```go
for p := path; ; p = filepath.Dir(p) {
	i, err := os.Lstat(p)
	...
	if err == nil && !i.IsDir() {   // :482
		return nil, nil, traceDeclined()
	}
```

`ls -ld` on this Mac: `/var -> private/var`, `/tmp -> private/tmp`, `/etc -> private/etc`.
So `Lstat("/var")` is a symlink, not a directory. Every `t.TempDir()` on darwin is under `/var`,
which is the darwin path-resolution class AGENTS.md describes. This walk landed in `8f733e00e`
(2026-11-20, "admit and retain bounded traces"). The tests that expose it landed on 2026-11-27.

**Reach beyond the tests (not measured).** A real macOS home, `/Users/<name>`, has no symlinked
ancestor, so ordinary launches record traces. Any HOME, or an `XDG_*`-relocated logs directory,
reached through a symlink records none, and prints a line asking for the maintainer. On Linux
this is the class `TMPDIR=/tmp/link go test -short ./...` reproduces, per AGENTS.md. That gives
a Linux-side failing test.

### 3. Also seen

- The **Nightly macOS Integration** run `46031665455` at `fd75c97c4` was still `queued` at
  this check: created 16:12 UTC, with its jobs listed as `completed` without a conclusion. The
  self-hosted runner's `Runner.Listener` was up, and no `Runner.Worker` was running. It was
  not investigated.
- Everything else at `447f52051` is green: CI's Linux jobs, Apple Container parity, and Pack
  Installs.

## Fixed and committed

Nothing. This report only.

## Open

1. **`openStoreRoot`'s ancestor walk declines every root under a system symlink.** *Decision
   needed:* either resolve the explicitly supplied root once where it is minted (`paths` or the
   caller) and keep the leaf-level no-follow checks, or permit symlinked ancestors above the
   store and refuse only a link at or below it. Then decide whether the decline line should
   name the path it judged.
   - The two tests can mint `HOME` as `EvalSymlinks(t.TempDir())`, as AGENTS.md says. That
     alone would hide the production case, though.
2. *Decision needed:* whether the queued nightly needs a look.

## Evidence

All of it is under `/Users/Shared/yolo/evidence/2026-11-27a/`, captured at `447f52051`
(`HEAD.txt`):

| File | Contents |
| :--- | :--- |
| `ci-46049903881.log` | the failed-jobs log of the CI run |
| `trace-tests.txt` | the two tests run on this Mac: both fail |
| `diag.txt` | the temporary diagnostic test: the test's HOME, the decline line, the empty glob |
| `replay-paths.txt` | the three hand replays |
| `diag-caller.txt`, `tracestore.go.orig` | the caller report, and the unpatched file it was built from |
| `yolo-head`, `yolo-diag` | the two scratch binaries |
