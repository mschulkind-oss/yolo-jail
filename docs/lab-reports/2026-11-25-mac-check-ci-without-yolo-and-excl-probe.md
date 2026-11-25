---
title: "Mac at 898222bc6: check-ci with no yolo on PATH fails only the release-decode test, because the clone's newest tag lagged; O_EXCL never loses the APFS race; MMP-D37's semaphore cases pass"
date: 2026-11-25
tags: [lab-report, macos, check-ci, apfs, seatbelt, release-decode]
summary: "On the owner's Mac at main 898222bc6, `just check-ci` with no yolo on PATH and every YOLO_* variable unset failed one test, TestShippedPacksDecodeUnderTheLastRelease. The clone had not fetched v0.20.0 yet, so the test compared against v0.19.0, and the new per-setup-state kind is listed only under v0.20.0. Once the tag was fetched the test passed. None of the contributor's #79 failures reproduced, and the TMPDIR gate found no leak with podman on PATH. An O_EXCL create never got ENOENT in 6,000 racing pairs. The MMP-D37 macos-user semaphore and shared-memory cases pass."
---

# Mac at `898222bc6`: `check-ci` with no `yolo` on PATH, the `O_EXCL` probe, and MMP-D37

**Checked 2026-11-24 and 2026-11-25 on the owner's Mac** (macOS 26.5, darwin/arm64, Go 1.26.7),
at main `898222bc6`. Nothing in yolo was changed. This report answers the Mac next steps of two
backlog items:
`mac-local-check-ci-failures` and `excl-create-enoent-darwin`, both since closed and deleted from
the backlog (see Open).

## Observed

### 1. `just check-ci` with no `yolo` on PATH

How it was run:
- `PATH` held every entry of the owner's normal PATH, except that `~/.local/bin` was replaced
  by a copy without `yolo`.
- Every `YOLO_*` variable was unset.
- `podman` stayed on PATH (`/opt/homebrew/bin/podman`).

Results:
- `lint-ci` passed, `scripts/test_matched_bundle.py` included.
- In `test-fast`, 241 packages passed and **one failed**: `packs`, in
  `TestShippedPacksDecodeUnderTheLastRelease`.

Every test the contributor saw fail in [#79](https://github.com/mschulkind-oss/yolo-jail/pull/79)
passed:
- `scripts/test_matched_bundle.py`;
- the packages holding the flaky failures: `internal/callbroker` (587 s), `internal/mcpauth`,
  `internal/gitremote` and `internal/hostprocesses`;
- `internal/config`, which holds `TestEveryKeyAdviceNamesForUnsetIsRemovable`.

Native confirmations, from the same run:
- `8fcf990f0`: `internal/paths` passed. `TestRemovedDirectoryStillReportsENOENT` passes alone
  too; it runs rather than skips.
- `765b1e6e9`: `internal/entrypoint` (the repo-instructions boot tests) and `internal/cli/run`
  (the resource-group placement test) passed.

**The one failure is not a macOS failure. It happens because a clone's newest tag can lag the
tree.** `releasebaseline.Latest` picks the highest release tag the local clone has. When the run
started, this clone's newest tag was `v0.19.0`. Tag `v0.20.0` (commit
`3fcba8c74`, an ancestor of `898222bc6`) was fetched only by the `git pull` after the run. Its
ref file is dated after the log finished.

So the test ran v0.19.0's reader. That reader skips the `per-setup-state` kind (added in
`95c0d7684`) for `copilot`, `omp` and `pi`, but `knownReleaseBreaks` lists that skip only under
`release: "v0.20.0"` (`packs/releasedecode_test.go:184-190`). That produced six `skipped an
unlisted contribution` errors. Once the tag was fetched, the test, run alone with `-v`, picked
v0.20.0 and passed.

So any clone that has the release commit but not yet its tag goes red here. That is every
developer for the window between a release being tagged and their next tag fetch. The test's
header says only that a clone *without* tags skips; a clone with an older tag fails instead.
CI does not see this, because its checkout fetches tags.

### 2. The TMPDIR leak

There was no leak:
- The gate (`scripts/tmp-leak-gate.py`) checks for leftovers whether or not the command passes.
  It printed no `TMPDIR LEAK` line, though `podman` was on PATH.
- The host's own `$TMPDIR/storage-run-501/libpod` is dated 2026-11-17 18:50, a week before this
  run, so this run did not make it. It is what a podman command run with the host's normal
  TMPDIR leaves.

So podman on PATH did not, by itself, make the suite leak `storage-run-501` here.

### 3. Does the APFS create race reach an `O_EXCL` create? No

The probe runs two goroutines that create the same new name in a fresh directory at the same
moment, 2,000 pairs per case. It is standalone and uses no yolo code.

| Case | Succeeded | EEXIST | ENOENT |
| :--- | ---: | ---: | ---: |
| `openat` `O_RDWR\|O_CREAT\|O_EXCL\|O_NOFOLLOW\|O_CLOEXEC` | 2000 | 2000 | **0** |
| `openat` `O_WRONLY\|O_CREAT\|O_EXCL` | 2000 | 2000 | **0** |
| control: `open(2)` by absolute path, `O_WRONLY\|O_CREAT\|O_EXCL` | 2000 | 2000 | 0 |
| reference: `openat` `O_RDWR\|O_CREAT`, no `O_EXCL` | 3024 | 0 | 976 |

Every `O_EXCL` pair gave exactly one success and one EEXIST. The reference row shows the race
was still live on this machine during the same run.

### 4. MMP-D37's semaphore runtime cases (macos-user Seatbelt profile)

All three tests passed with `YOLO_TEST_MACOS_USER=1`:
- `TestMacosUserDenyDefaultProfileEnforcesItsRules`: 76 of its 79 cases passed and 3 were
  skipped. They include:
  - `posix_semaphore_lmdb_lock_sequence`
  - `posix_semaphore_of_yours_opens_by_name`
  - `posix_shared_memory_create_refused` (`shm_open(O_CREAT)` stays refused)
- `TestMacosUserDenyDefaultCasesAreWellFormed`
- `TestMacosUserDenyDefaultRegistryMatchesTheProfile`

The three skips are this Mac's own conditions, each named by the test:
- `xcode_shim_make_runs`: there is no accepted Xcode license.
- `b4_keychain_item_unreadable`: the login keychain refused a non-interactive add.
- `macos_log_store_read_refused`: `log show` is refused to this user, so the test's control
  cannot run.

## Fixed and committed

Nothing. This report only.

## Open

Each item was decided by the orchestrator on 2026-11-25:

1. **The release-decode test goes red in a clone whose newest tag lags the tree.** Fixed as a
   test defect in commit `f38d3b41c`: a break listed under release R now applies to every
   baseline up to and including R, a break under an older release never excuses a newer
   baseline, and the failure message says to run `git fetch --tags` when the baseline is older
   than a release the table names.
2. **`mac-local-check-ci-failures`:** closed. None of #79's failures reproduced here at
   `898222bc6` and podman on PATH did not leak, so the item had nothing left to track; its
   backlog entry is deleted (git history is the archive) and this report is the evidence.
3. **`excl-create-enoent-darwin`:** closed. `O_EXCL` creates never got ENOENT in 6,000 racing
   pairs, so the non-retried `O_EXCL` claims need no retry; its backlog entry is deleted and this
   report is the evidence.

## Evidence

All of it is under `/Users/Shared/yolo/evidence/2026-11-24e/`, captured at `898222bc6`
(`HEAD.txt`):

| File | Contents |
| :--- | :--- |
| `check-ci.log` | the full `just check-ci` run, ending `exit=1` |
| `path.txt`, `shadow-bin/` | the PATH used, and the `~/.local/bin` copy without `yolo` |
| `releasedecode-alone-newhead.txt` | the failing test run alone after the tag fetch: `ok` |
| `releasedecode-alone-v.txt` | the same run with `-v`: it names v0.20.0 as the last release |
| `exclprobe/main.go`, `exclprobe.txt` | the probe and its output |
| `semaphore-integration.txt` | the MMP-D37 integration run, ending `exit=0` |

The command each file came from:
- `check-ci.log`: `env -u YOLO_… PATH="$(cat path.txt)" just check-ci`
- `releasedecode-alone-*.txt`:
  `go test -short -count=1 [-v] -run '^TestShippedPacksDecodeUnderTheLastRelease$' ./packs`
- `semaphore-integration.txt`: `YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run
  '^(TestMacosUserDenyDefaultProfileEnforcesItsRules|TestMacosUserDenyDefaultCasesAreWellFormed|TestMacosUserDenyDefaultRegistryMatchesTheProfile)$'
  ./integration`
