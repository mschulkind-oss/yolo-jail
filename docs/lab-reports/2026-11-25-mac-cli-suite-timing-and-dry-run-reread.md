---
title: "Mac at 033c405d1: internal/cli's short suite is slow on a Mac because process starts are, and a `yolo host` session's git blocker on PATH makes it 1.65× slower again; the macos-user dry-run lines read correctly"
date: 2026-11-25
tags: [lab-report, macos, test-speed, guardrails, macos-user, ci]
summary: "internal/cli and internal/cli/run have about 3,000 top-level tests each, none parallel, and no long pole. On this Mac every process start costs 8 to 10 ms (CrowdStrike Falcon is active) and every git call about 24 ms. Run from a `yolo host` session, the tests inherit the guardrails pack's git blocker, a 7.5 KB sh script first on PATH, which brings each git call to about 44 ms. internal/cli took 1,574 s with it and 956 s without it. A macos-user --dry-run declaring a /dev/cu.* node, a raw disk and tmpfs prints the right lines, and a control that declares none prints none. The darwin floor cache's x86_64 job fails because cachix-action installs from nixpkgs 26.11, which dropped x86_64-darwin."
---

# Mac at `033c405d1`: the `internal/cli` suite timing, and the macos-user dry-run reread

**Checked 2026-11-25 on the owner's Mac** (macOS 26.5, darwin/arm64, Go 1.26.7, 10 cores), at main
`033c405d1`. Nothing in yolo was changed. The runs covered:
- the backlog item [`cli-short-suite-slowdown`](../plans/BACKLOG.md#cli-short-suite-slowdown);
- the item *"Reread the macos-user launch lines that changed on 2026-10-04, on a Mac"*;
- one CI failure seen while looking.

The tests below were run like the previous report's `check-ci`:
- every `YOLO_*` variable and `PI_CODING_AGENT_DIR` unset;
- `yolo` off PATH;
- under `scripts/tmp-leak-gate.py`.

Every run passed, and the leak gate reported nothing.

## Observed

### 1. `cli-short-suite-slowdown`: no long pole; process starts are the cost

**The shape** (`go test -short -json`; the summaries are in `cli.summary.txt` and
`cli_run.summary.txt`):

| Package | Top-level tests | Parallel tests | Package time | Sum of test times | Tests ≥ 1 s (time share) | 20 slowest (share) |
| :--- | ---: | ---: | ---: | ---: | :--- | ---: |
| `internal/cli` | 3,249 | 0 | 1,574 s | 1,572 s | 440 (77%) | 15% |
| `internal/cli/run` | 3,037 | 0 | 919 s | 917 s | 159 (64%) | 34% |

No test in either package calls `t.Parallel()`, so the package time is the sum of its tests'
times. Most of the time is in hundreds of tests of 1 to 5 seconds each, not in a few outliers.

The 1 s-and-over tests in `internal/cli` cluster by name prefix (`cli.clusters.txt`):

| Prefix | Time | Tests |
| :--- | ---: | ---: |
| `TestPatchActor*` | 84 s | 26 |
| `TestPackUpdate*` | 64 s | 13 |
| `TestBackgroundAdvance*` | 48 s | 5 |
| `TestBackgroundPatch*` | 46 s | 2 |
| `TestProductionFloor*` | 44 s | 16 |

These are the patched-build and floor tests, which spawn `git` and builds. That is the backlog's
earlier long pole, now spread over many tests.

**Process starts on this Mac** (100 runs each, in a loop):

| Command | Cost each |
| :--- | ---: |
| `/bin/echo x`, `/usr/bin/true`, `/bin/ls /` | 8–10 ms |
| `/bin/sh -c true` | 21 ms |
| `/usr/bin/git --version`: the Command Line Tools stub, which runs the real git | 24 ms |
| `git --version` through the guardrails blocker (`~/.local/share/yolo-jail/bin/block/<hash>/git`, a 7,525-byte `#!/bin/sh` guard) | **44 ms** |
| `git init` plus an empty commit | 110 ms |

`systemextensionsctl list` shows CrowdStrike Falcon Sensor (`com.crowdstrike.falcon.Agent`)
activated and enabled. Endpoint scanning on exec is the likely cause of the 8–10 ms floor; that
was not measured further. It also means the "5–10× slower than the Linux jail" is mostly this
machine's process start cost, multiplied by thousands of serial tests that each start
processes.

**The git blocker from a `yolo host` session.** This agent runs inside a `yolo host` launch
with the `guardrails` pack selected. That launch puts its blocker directory first on PATH, and
the test binaries inherit it. `7fbcc8a32` cleans the caller's `YOLO_*` variables out of these
suites, but not that PATH entry. Measured with the same compiled test binary and only the PATH
changed (`block-vs-noblock.txt`, `block-vs-noblock-package.txt`):

| Run | With the blocker | Without it |
| :--- | ---: | ---: |
| `TestBackgroundPatchFailureRecoveryUsesCurrentCheckAuthority`, twice | 36.6 s, 36.9 s | 20.5 s, 20.6 s |
| `TestBackgroundAdvanceRepairTokenRefusesOtherPersistedFailureIdentityChanges`, twice | 24.4 s, 24.4 s | 16.3 s, 16.4 s |
| `TestPackUpdatePrintsWhichCopyRunsAndNeverTouchesTheClone`, twice | 27.3 s, 27.2 s | 22.6 s, 22.8 s |
| the whole `internal/cli` package | 1,574 s (load 5.2 at the start, 4.6 at the end) | 956 s (load 2.2 at the start, 3.7 at the end) |

The whole-package pair ran under somewhat different load, so its 1.65× is an upper bound. The
single-test pairs ran back to back and repeat within 0.3 s.

The blocker is also scoped to the launch's workspace: it names `/Users/Shared/yolo/yolo-jail/.yolo`.
So a test's `git` call inside this checkout passes through a guard written for the agent, not
for the test. No test was seen failing because of it.

### 2. The macos-user dry-run lines read correctly

Both runs used `YOLO_SETUP=seatbelt-yolo-account yolo run --dry-run </dev/null`, from two empty
throwaway workspaces:
- **declared:** `yolo-jail.toml` is `devices = ["/dev/cu.debug-console", "/dev/disk0"]` and
  `ephemeral_storage = "tmpfs"` (`dryrun-ws.toml`);
- **control:** no workspace config at all.

The host has `~/.config/nvim`. Both runs exited 0, ending `✓ all plan invariants hold`. Neither
asked for config approval. The two outputs differ only in these lines, after normalizing the
ports, hashes and paths (`dryrun.diff`):

| Declared | Line printed |
| :--- | :--- |
| `/dev/cu.debug-console` | `devices: the sandbox allows device control (ioctl) on /dev/cu.debug-console. The node opens under ordinary macOS permissions; nothing is attached.` The profile also gains `(allow file-read* file-write* file-ioctl (literal "/dev/cu.debug-console"))` under `#seatbelt-test-id:device-ioctl-allow#`. |
| `/dev/disk0` | `Warning: devices entry /dev/disk0 is skipped on macos-user — raw disks and packet capture stay denied on this backend; remove the entry, or run this work in a podman jail on Linux, which passes a raw device through.` |
| `ephemeral_storage = "tmpfs"` | `Warning: ephemeral_storage: "tmpfs" is not read on macos-user — … the sandbox writes this machine's own /tmp and /var/folders, on disk. Remove the key, or use Apple Container (yolo --setup apple-container-self), whose scratch is always RAM-backed.` |

The host nvim line prints in **both** runs, because it depends on the host having
`~/.config/nvim`, not on the config: `Host nvim config (~/.config/nvim) is not delivered on
macos-user: … an open design question ([OQ-ED2](../design/baked-editor-preference.md#OQ-ED2)), so there is nothing to change until it is
ruled.`

The control prints none of the device or tmpfs lines. Every line names its next step.

The item's second half is not done. It says to drop the user guide's *"not yet tried on a Mac"*
serial hedge (also in `config-ref`'s `devices` entry, `userguide/guides/macos.md`, `devices-and-gpus.md` and `reference/settings-per-setup.md`) once two things hold:
- **`declared_device_ioctl_allowed` passes on `macos-user.yml`.** It does: run `45530262435`
  at `e562dcbcb`, 2026-11-25.
- **A real USB serial adapter accepts `stty -f` from the sandbox.** This Mac has none attached.
  Its only `/dev/cu.*` nodes are `Bluetooth-Incoming-Port` and `debug-console`.

### 3. Seen in passing: the darwin floor cache's x86_64 job fails outside this repository's flake

Run `45642736635` at `033c405d1`, job `push the floor (x86_64-darwin)` on the hosted
`macos-26-intel` runner, failed in its **Set up Cachix** step (`cachix/cachix-action@v17`) with
`error: Nixpkgs 26.11 has dropped support for x86_64-darwin`. The step fails before
`flake.nix` is evaluated. The `aarch64-darwin` job passed. This is GitHub's runner, not this
Mac, and it is noted only because it is a darwin job.

## Fixed and committed

Nothing. This report only.

## Open

1. **The test suites inherit a `yolo host` session's blocker directory on PATH.** It doubles
   some tests' time on a Mac, and puts an agent guard in front of every test `git` call.
   *Decision needed:* whether the suites should drop `bin/block/` entries from PATH, as
   `7fbcc8a32` does for `YOLO_*`, or whether the gate should run from a shell without them.
2. **`cli-short-suite-slowdown`:** the cost is serial tests times this Mac's process start
   cost, not a long pole. *Decision needed:* whether the remedy is fewer process starts per
   test (shared git fixtures, fewer builds per test), `t.Parallel()` for tests that can take
   it, or accepting a Mac-specific time.
3. **The serial hedge:** it waits on a real USB serial adapter. *Decision needed:* whether the
   owner plugs one in for a `stty -f` check, or the hedge stays.
4. **The x86_64-darwin floor job:** *decision needed:* pin the action's nixpkgs, move the job
   to 26.05, or drop x86_64-darwin from the floor matrix.

## Evidence

All of it is under `/Users/Shared/yolo/evidence/2026-11-25b/`, captured at `033c405d1`
(`HEAD.txt`):

| File | Contents |
| :--- | :--- |
| `cli.json`, `cli_run.json`, `*.stderr`, `*.load-*.txt` | the two packages with the blocker on PATH, and the load before and after |
| `cli.summary.txt`, `cli_run.summary.txt`, `cli.clusters.txt` | the time buckets and the name-prefix clusters |
| `cli-noblock.json`, `path-noblock.txt` | the `internal/cli` run without the blocker, and its PATH |
| `block-vs-noblock.txt`, `block-vs-noblock-package.txt` | the paired runs |
| `dryrun-ws.toml`, `dryrun.txt`, `dryrun-control.txt`, `dryrun.diff` | the dry-run config, both outputs, and their diff |

The command for the package runs:

```sh
python3 scripts/tmp-leak-gate.py go test -short -count=1 -timeout=3h -json ./internal/<pkg>
```

The paired single tests ran a `go test -c` binary from `internal/cli`, with
`-test.short -test.count=1 -test.run '<the three names>'`.
