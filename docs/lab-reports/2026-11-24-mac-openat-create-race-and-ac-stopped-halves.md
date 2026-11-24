---
title: "Mac run at 042a00ab2: concurrent openat(O_CREAT) of one new name returns ENOENT on macOS 26.5 APFS, os.Root included, which is the mcpauth two-broker failure; W-SU11 and W-SU14's stopped halves pass once Apple Container is booted out of gui/501"
date: 2026-11-24
tags: [lab-report, macos, mcp-auth, paths, named-setups, apple-container]
summary: "At main 042a00ab2 on the owner's Mac. TestOutcomeTwoBrokersAdoptOneSingleUseToken fails 10 of 10 with no data race left. At the failing openat the directory is intact and records.lock already exists, created by the other broker. A standalone probe shows two concurrent openat(dirfd, name, O_RDWR|O_CREAT) of a new name give one ENOENT in about 99.7% of racing pairs, os.Root.OpenFile included; open(2) by absolute path and a lone opener never do. `container system stop` boots out the user/501 domain while the services run in gui/501, so it never stopped them. Booted out of gui/501, the runtime is unreachable, and W-SU11 and W-SU14's stopped halves pass on their output. W-UH2 has no seatbelt-self home with agent sessions to read; slice 1 of the unified home has not landed."
---

# Mac run at `042a00ab2`

**Run 2026-11-24 on the owner's Mac** (macOS 26.5, build 25F71, darwin/arm64, APFS; Go 1.26.7;
Apple Container CLI 1.1.0; podman machine on applehv), at main `042a00ab2`. The installed `yolo`
was `0.19.0+148.g3264dd695740`, which is behind main by docs and tests only. Launches used it.

- **Owner's say.** The owner has cleared stopping Apple Container and stopping daemons.
- **CI runner.** The Mac's self-hosted Actions runner was idle throughout: `Runner.Listener` only,
  no `Runner.Worker`. It was not stopped.
- **Changes.** Nothing in yolo was changed. A temporary diagnostic in
  `internal/paths/statefile_open_unix.go` was added, run and reverted (`git checkout`), see
  [§1](#1-the-mcpauth-two-broker-failure-is-a-darwin-openat-race).

## Observed

| Check | Result |
| :--- | :--- |
| [`mcpauth-records-lock-enoent-darwin`](../plans/BACKLOG.md#mcpauth-records-lock-enoent-darwin) | **Still fails, 10 of 10.** The cause is a macOS `openat(O_CREAT)` race, see [§1](#1-the-mcpauth-two-broker-failure-is-a-darwin-openat-race) |
| W-SU11 and W-SU14, the "runtime stopped" halves | **Pass**, with the runtime taken down by `launchctl bootout gui/501/…`, see [§2](#2-w-su11-and-w-su14-the-stopped-halves) |
| Why `container system stop` never stopped Apple Container | **Found**: it boots out `user/501`, and the services run in `gui/501` |
| `yolo-ac-builder` after the earlier stop | **Recreated** by a later Apple Container launch (started 2026-11-24T06:34:03Z) |
| [W-UH2](../design/unified-workspace-home.md#mac-checks) | **Cannot run yet**: no `seatbelt-self` home on this Mac holds agent sessions |
| W-UH1, W-UH5 and W-UH6 | **Not due**: slice 1 has not landed (there is no `internal/unifiedhome`) |
| W-UH3 | By hand, with a live `claude`, so it is the owner's |

### 1. The mcpauth two-broker failure is a darwin `openat` race

Built from main (after `2eb6aeb7f`, which gave each broker its own locked clock), and run as 10
separate processes with `-test.count=1`:

| Build | Failed | `WARNING: DATA RACE` |
| :--- | :--- | :--- |
| plain | 10 of 10 | 0 |
| `-race` | 10 of 10 | 0 |

Each failure is `outcome_concurrency_test.go:37: slack: openat records.lock: no such file or
directory`. The data race is gone, and the ENOENT is not.

**A temporary diagnostic** in `openNoFollowLeaf` (`internal/paths/statefile_open_unix.go`) recorded
what the failing `openat` saw. It printed the flags, `fstat` of the anchor directory, and
`fstatat(AT_SYMLINK_NOFOLLOW)` of the leaf at the moment of failure:

```text
DIAG openNoFollowLeaf: name="records.lock" root=".../TestOutcomeTwoBrokersAdoptOneSingleUseToken1131677994/002/once" err=no such file or directory flag=0x1000306 anchor_fstat_err=<nil> nlink=3 ino=178734131 mode=40700 leaf_lstat_err=<nil> leaf_mode=100600
```

So the directory is intact, and `records.lock` already exists as a regular `0600` file, created by
the other broker. The flags are `O_RDWR|O_NONBLOCK|O_NOFOLLOW|O_CREAT|O_CLOEXEC`. The diagnostic was
reverted.

**A standalone probe, with no yolo code**, ran on the same Mac. It is
`/Users/Shared/yolo/evidence/2026-11-24b/creatprobe/main.go`, built against `golang.org/x/sys`
v0.46.0 from the module cache. For each of 2,000 trials it makes a fresh directory, and two
goroutines released together each open `x.lock` in it once:

| Open | ENOENT |
| :--- | :--- |
| `openat(dirfd, …, O_RDWR\|O_CREAT\|O_NOFOLLOW\|O_NONBLOCK\|O_CLOEXEC)`, yolo's flags | 1994 to 1997 of 2000 pairs |
| `openat(dirfd, …, O_RDWR\|O_CREAT\|O_NOFOLLOW)` | 1993 to 1999 |
| `openat(dirfd, …, O_RDWR\|O_CREAT\|O_NONBLOCK)` | 1997 to 1998 |
| `openat(dirfd, …, O_RDWR\|O_CREAT)` | 1994 to 1997, and 1968 under `$TMPDIR` |
| Go's `os.Root.OpenFile(…, O_RDWR\|O_CREATE)` | 1976 |
| control: one opener, `openat(…, O_RDWR\|O_CREAT)` | **0** of 2000 |
| control: two racing `open(2)` by absolute path, `O_RDWR\|O_CREAT` | **0** of 2000 |

Moving `d.Fd()` out of the goroutines made no difference. So on this macOS, when two threads
create-if-missing the same new name **relative to a directory descriptor**, one of them nearly
always gets ENOENT. Neither flag matters, and Go's `os.Root` is affected too. The same race
through a path is clean. This is backlog hypothesis (1), now measured.

It reaches past this test. Any concurrent first open of a lock or state file through
`paths.OpenLockFileBeneath`, `openRegularBeneath` or an `os.Root.OpenFile` with `O_CREATE` can fail
this way on a Mac, for example two brokers or two launches meeting a lock that does not exist yet.
Production callers were not counted. In this probe the file always exists once the ENOENT is
returned, so one retry would succeed. That suggests a retry on ENOENT while the anchor directory
still exists, but nothing here proves a single retry is always enough.

### 2. W-SU11 and W-SU14, the stopped halves

**Why `container system stop` never stopped anything.**

- `ps -o lstart` shows `container-apiserver` (PID 2258) and the three plugin services running since
  **Tue Nov 17 18:49:05**, through both of the earlier stops.
- `launchctl print gui/501` lists them in `gui/501`. Their plist
  (`~/Library/Application Support/com.apple.container/apiserver/apiserver.plist`) has
  `LimitLoadToSessionType = Aqua` and `RunAtLoad`.
- The owner's `~/Library/LaunchAgents/local.container-system-start.plist` runs
  `container system start` once at login (`RunAtLoad`, no `KeepAlive`, `runs = 1`). That puts the
  services in the GUI session.
- `container system stop` logs `label=user/501/com.apple.container.apiserver … stopping service`,
  which is the `user/501` domain, so it misses them. It does stop containers.

**The run**, with one `podman-self` and one `apple-container-self` jail running in
`/Users/Shared/yolo/su1124`. The services were booted out with
`launchctl bootout gui/501/<label>` for `com.apple.container.apiserver`,
`…container-core-images`, `…machine-apiserver` and `…container-network-vmnet.default`. Then:

- `container system status` printed *"apiserver is not running and not registered with launchd"*.
- `yolo ps` listed only the Podman row, then *"Apple Container is installed but not answering
  (Error: internalError: "failed to list containers" (cause: "interrupted: "XPC connection error:
  Connection invalid"")); no yolo jail is recorded on it."* followed by `container system start`.
- `yolo ps --format json` had `"schema": "yolo.ps/1"` and `"complete": false`. The
  `apple-container` source had `"state": "unreachable"` with that detail, and the `podman` source
  had `"state": "listed"`.
- `yolo stores` printed `Sources: Podman, Seatbelt, yolo host`, then *"Apple Container did not
  answer (unreachable: …); its sections are not listed, …"*, with no `— Apple Container` section.
  `--format json` had `"complete": false`.
- **One difference from the witness:** `~/.local/share/yolo-jail/containers/` no longer held the
  AC jail's record. Booting the services out ended that jail's VM (its launch exited 1), and its
  keeper tore it down (`session.keeper_teardown took 20.656s`). So the jail was really gone. That
  is not a record lost while the jail lived.

**Restored** with `container system start`, which exited 0 and gave `status running`, with a new
API-server PID (84940). After that:

- `yolo stores --setup apple-container-self` printed `Sources: Apple Container`, with no Podman
  mention;
- `go test -count=1 -run 'TestMacosUserPs|TestMacosUserStores' ./integration` passed
  `TestMacosUserPsListsItsLiveSession` and `TestMacosUserStoresListsTheSandboxAccount`.

### 3. The unified-home witnesses

[W-UH2](../design/unified-workspace-home.md#mac-checks) reads the names in a `seatbelt-self` home
"where agents were used". A `find` under `/Users/Shared` and `/Users/matt`, to depth 7, found no
`.yolo/setups/seatbelt-self/home/.claude/projects` or `.pi/agent/sessions` directory with any entry,
and every scratch workspace's `seatbelt-self` home was empty of sessions. The witness runs no agent,
so it waits until the owner has used one under `seatbelt-self`.

## Fixed and committed

Nothing in yolo. This report is the only commit.

## Open

1. [§1](#1-the-mcpauth-two-broker-failure-is-a-darwin-openat-race): handle ENOENT from a concurrent
   `openat(O_CREAT)` on darwin in `openNoFollowLeaf`, and in any `os.Root.OpenFile(O_CREATE)` that
   can race. Then rerun the two-broker test on a Mac as 10 separate processes.
2. [§2](#2-w-su11-and-w-su14-the-stopped-halves): change W-SU11 and W-SU14 to take the runtime down
   with `launchctl bootout gui/$(id -u)/com.apple.container.apiserver` (and its three plugin
   services) and restore with `container system start`. Also change the record expectation: the
   jail ends with its runtime, so its record goes.
3. [§3](#3-the-unified-home-witnesses): W-UH2 needs a `seatbelt-self` home where agents have run.
   W-UH3 is the owner's by hand. W-UH1, W-UH5 and W-UH6 wait for slice 1.

## Evidence

In `/Users/Shared/yolo/evidence/2026-11-24b/`: `HEAD.txt`, `mcpauth-plain.log`, `mcpauth-race.log`,
`mcpauth-diag.log`, `creatprobe/` (the probe source and its `go.mod`), `creatprobe.txt`,
`creatprobe-fd-hoisted.txt`, `ac-launchagent.txt`, `ac-apiserver-plist.txt`, `ac-service-pids.txt`,
`stop-podman-jail.log`, `stop-ac-jail.log`, `su-down.txt`, `ps-down.json`, `stores-down.json`,
`ac-restart.log` and `su-tests.log`. Everything ran against `042a00ab2`.
