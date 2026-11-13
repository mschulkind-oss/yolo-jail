---
title: "Pinned host helpers — a jail's host processes run the build that launched it"
status: accepted
stage: CURRENT
verified: 2026-11-12
verified_commit: 6c6045b2b
covers:
  - internal/helperpin/
  - internal/cli/run/pinnedexec.go
  - internal/cli/run/hostdaemonsupervise.go
  - internal/cli/run/macosuserservices.go
  - internal/cli/run/applybrokerskew.go
  - internal/cli/hostpin.go
  - internal/launchservice/
  - internal/openaiauthhost/privatedaemon.go
  - internal/macosuser/real.go
  - internal/flakebundle/
  - internal/cli/run/housekeeping.go
  - scripts/stage-source-bundle.sh
tags: [host-services, keeper, daemons, install, bundles, logging]
summary: "Every host-side process that serves one jail or one launch is exec'd from one pinned file, the host-OS yolo inside the bundle the launch resolved, checked unchanged before every exec, so it runs the launch's build for the jail's whole life or does not run at all. Each such daemon writes a start line naming its build, commit, pid, jail and executable."
---

# Pinned host helpers — a jail's host processes run the build that launched it

**Status:** Verified 2026-11-12 against `6c6045b2b`. MEASURED: the owner's Mac ran the five Mac
witnesses ([below](#mac-witnesses)) at main `5a69dd0b4` on darwin/arm64 (Apple Container CLI 1.1.0,
podman machine `applehv`), and all passed.

A **host helper** is any host-side process that serves one jail or one launch: the jail keeper,
every host daemon (`yolo internal daemon <name>`), launch-owned services, `yolo host`'s private
credential daemons, the scratch and discard removers, the context injector's child, and the
macos-user guest's staged `yolo`. Every one of them is the running yolo re-executed as a
subcommand. The **pinned helper**, or **pin** (terms this project coined), is the one executable
file a launch execs all of them from: the host-OS `yolo` inside the bundle that launch resolved,
`<bundle root>/bin/<host os>-<host arch>/yolo`, with every symlink resolved. It is not the launcher
on `PATH`, not the stable `flake-bundle` symlink, and not a copy yolo makes. Each helper daemon
writes a **start line** naming its build at start, so which build served a jail is read from a log,
not argued.

| Component | Lives in |
| :--- | :--- |
| Pin resolution, the unchanged-file check, the start-line format | `internal/helperpin` (`Pin`, `ResolveForBuild`, `Pin.Unchanged`, `StartLine`, `EnvJail`) |
| A container launch's pin and its handoff to the keeper | `internal/cli/run` (`pinnedexec.go`, `Run`'s pre-flight) |
| The supervised-restart give-up | `internal/cli/run` (`daemonSupervisor`), `internal/launchservice` (`Running.Supervise`) |
| `yolo host`'s pin | `internal/cli` (`hostpin.go`) |
| macos-user: services and the staged guest `yolo` | `internal/cli/run` (`macosuserservices.go`), `internal/macosuser` (`PinnedSelfExe`) |
| `yolo host`'s private OpenAI daemon | `internal/openaiauthhost` |
| Generation liveness from the process table | `internal/prune` (`ProcessExes`), `internal/flakebundle` (`ReapGuarded`), `internal/cli/run` (`housekeeping.go`) |
| The config-apply note about older brokers | `internal/cli/run` (`applybrokerskew.go`) |

**Reads with:** [`host-service-startup-diagnostics.md`](host-service-startup-diagnostics.md) (the
start line among the other startup records),
[`jail-lifetime-last-session-wins.md`](../design/jail-lifetime-last-session-wins.md) (the keeper and
[JL-D5](../design/jail-lifetime-last-session-wins.md#JL-D5), whose Linux inode rule this
generalizes), [`host-daemon-ownership.md`](../design/host-daemon-ownership.md) (the restart policy,
[HD-D25](../design/host-daemon-ownership.md#HD-D25) and
[HD-D28](../design/host-daemon-ownership.md#HD-D28)),
[`matched-development-bundles.md`](matched-development-bundles.md) (bundle discovery),
[`image-staging-vs-baking.md`](image-staging-vs-baking.md) (the jail side's mounted prefix, pinned
the same way).

---

## Principles

- <a id="p1"></a>**P1. A process runs the build of the launch that started it, for its whole
  life.** A keeper's helpers run the fresh launch's build; a later launch's own children run that
  later launch's build.
- <a id="p2"></a>**P2. Never a different build, silently.** When the pinned file is gone or changed,
  the spawn does not happen and the reason is reported with a next step. No fallback to another
  binary.
- <a id="p3"></a>**P3. Every backend behaves the same.** Linux keeps a stronger mechanism where it
  has one; the observable outcome (same build, or a reported refusal) is identical.
- <a id="p4"></a>**P4. A running helper proves its own build.** The process writes it, at start, into
  the log a reader already opens; a spawner's intention is not evidence.

Without the pin, a helper started or restarted by path runs whatever file sits at that path now:
`just install` renames a new launcher over the old one, and a supervised restart reuses the argv
the first spawn resolved, so it re-reads the path. The jail side was already pinned, because
`/opt/yolo-jail` is two read-only bind mounts of the resolved bundle; the pin gives the host side
the same property.

## Where the pin comes from

- **The same resolution the jail is built from.** `reporoot.Resolve` chooses one bundle per launch
  and proves it matches the launcher (snapshot identity, see
  [matched development bundles](matched-development-bundles.md#invariants)). The pin is read from
  that bundle's root after symlinks are resolved, so an install that swaps `flake-bundle` during the
  launch cannot move it. The root is never re-derived from `os.Executable()` or from the stable
  symlink.
- **Same build means the same build stamp.** `helperpin.ResolveForBuild` accepts the pin only when
  the bundle's `yolo-build-stamp.json` names the launcher's normalized version and commit. A field
  either side lacks (empty or `unknown`) is not compared, so an unstamped launcher or an older
  bundle is not refused on that field alone. This lets Homebrew work: its `bin/yolo` is built
  separately from the bundle's copy, so the bytes differ but the stamp, and therefore the build, is
  the same.
- **macOS bundles carry a host `yolo`.** The darwin directory holds `yolo` beside the guest set
  (`yolo-jaild`, `yolo-serial`, `yolo-ps`, `yolo-log`), as a separate host list in
  `scripts/stage-source-bundle.sh`; the guest list is what a guest runs and does not include it.
  Discovery requires it on macOS, and `just install` takes the launcher out of the bundle on both
  OSes, so `yolo` is compiled once.

| Install channel | Pin | Immutable for the jail's life? |
| :--- | :--- | :--- |
| `just install` | the generation's `bin/<os>-<arch>/yolo` | yes |
| `just dev`, `just deploy-dev`, a dev bundle by path | the dev bundle's `share/yolo-jail/bin/<os>-<arch>/yolo` | yes |
| Homebrew | the keg's `share/yolo-jail/bin/<os>-<arch>/yolo` | until `brew cleanup` removes the keg |
| Release archive | `share/yolo-jail/bin/<os>-<arch>/yolo` beside `yolo` | until a newer archive is unpacked over it |

## When it is resolved

- **At pre-flight, once, by every non-dry-run launch whose bundle resolved**, before anything is
  spawned, an attach included, because an attach that finds the jail gone continues into a fresh
  launch in the same run. `yolo config apply`'s context injector resolves its pin from its own
  bundle when it needs one.
- **`yolo host` resolves at the first helper it starts**, once per launch, failures included; a
  `yolo host` launch that starts no doorway, launch-owned service or private daemon resolves
  nothing, so a binary-only `yolo host` without helpers keeps working.
- **What it records:** the path, and the file's identity then: device and inode, size, and
  modification time in nanoseconds, read through the Unix `stat`.
- **When it cannot be had, the launch refuses before starting anything**, naming what was missing
  (no bundle, no host `yolo` in it, a stamp that differs) and the reinstall step: Homebrew or a
  release archive, `just install`, or `just build-dev-bundle` and its printed launcher.
- **Two exceptions keep their own failure rule.** `yolo host`'s Claude credential view, which is
  never fatal, skips the view with its existing warning on a pin failure. A macos-user `--dry-run`
  with no pin prints its plan with the staged source named as unresolved, since a dry run copies
  nothing. Neither runs another build, so [P2](#p2) holds.

## Who execs it

**Every self-exec that serves a jail or a launch** goes through the pin:

- the keeper's spawn;
- every host daemon a launch or keeper starts, and every supervised restart of one;
- launch-owned services and their restarts (`yolo host`, macos-user);
- `yolo host`'s private Claude and OpenAI broker daemons;
- the scratch and discard removers, and the context injector's child;
- macos-user's per-session guest `yolo`, staged from the pin rather than from `os.Executable()`.

**The keeper receives the pin through its plan.** Off Linux the keeper execs that path for each
self-exec. On Linux the keeper keeps [JL-D5](../design/jail-lifetime-last-session-wins.md#JL-D5)'s
`/proc/self/exe`, which, because the keeper was itself spawned from the pin, names the pin's inode:
the same build, and still runnable if the file were deleted.

**Outside the rule**, because they serve no launch and run the yolo the user invoked: `yolo check`'s
self-checks, the preparation scheduler, background advance, shared demand, a patched fork's build
child, `yolo openai-auth status`, `import` and `logout`, the `yolo macos-*` verbs, capture, and the
host floor. A third-party pack's daemon that names its own program is not a yolo self-exec and is
untouched.

## The check before every exec

Before any exec by path, the spawner compares the file at the pin's path with the recorded identity
(`Pin.Unchanged`): a `stat`, never a hash.

| Moment | Pin unchanged | Pin missing or changed |
| :--- | :--- | :--- |
| A first spawn in the launch | exec | the launch refuses, as above |
| A first spawn in the keeper (a later daemon, a remover) | exec | the spawn does not happen; the keeper logs one line naming the path and the next step |
| A supervised restart | restart | **a give-up** on the supervisor's existing give-up path: one line in the daemon's log and in `launch.log` (or `host-launch.log`), recorded as down |

Both supervisors ask again after the restart backoff, immediately before the restart, so a change
during the wait gives up too. The give-up names the cause and the stop step the notch already
prints:

```text
host service 'git-broker' died (its process exited with status 1); not restarting it: the yolo
this jail was launched with is no longer at /opt/homebrew/Cellar/yolo-jail/0.17.1/share/yolo-jail/bin/darwin-arm64/yolo
(an upgrade removed or replaced it). It stays down until the jail is launched again: `yolo stop`
once its sessions are done, then launch.
```

On Linux the keeper's execs go through `/proc/self/exe` and never reach the check.

> [!WARNING]
> Do not "repair" a failed check by falling back to `os.Executable()` or to the current
> `flake-bundle`. That fallback is exactly the different-build restart the pin removes, and on a
> Mac it is what a `brew upgrade` mid-session would trigger. Nor drop the identity check and pin by
> path alone: an archive unpacked in place or a replaced keg would then silently run another build
> at a restart.

## Attach and join

**An attach or a join starts nothing the keeper holds**, so it can never restart a helper on its own
build (a container attach never re-stages). What a later launch starts for its own session (the
context injector, a macos-user joiner's sandbox and guest supervisor, a `yolo host` joiner's own
services) runs from that launch's own pin, by [P1](#p1). A later launch of another build is told so
by the keeper line it already prints.

## Keeping a bundle alive while a helper runs from it

**A generation is in use while a live process of this user executes a file inside it**, as well as
while a running jail mounts it. Without the process count, a detached credential daemon finishing a
refresh after its jail ends, or a macos-user or `yolo host` launch, which mount nothing, could lose
its generation.

- **The evidence is the process table**, read once per sweep at listing time through
  `prune.ProcessExes`: `/proc/<pid>/exe` on Linux, `kern.procargs2` on macOS, this user's processes.
- **Unreadable means decline.** A sweep that cannot read the table removes no generation; a missing
  reader is "could not ask".
- **On macOS this table is complete for generations**, because a sandbox account's processes run
  copies staged into the session's own directory, never the generation itself.
- **Development bundles have their own reaper**, which counts executing processes on Linux and
  declines on macOS ([matched development bundles](matched-development-bundles.md#collecting-old-development-bundles)).
- **Homebrew kegs and release archives** are not yolo's to reap; the check before every exec is what
  makes their removal safe.

## The start line

Every `yolo internal daemon <name>` process (the keeper, every official host daemon, every doorway
and launch-owned service) writes one line at start, once dispatch knows its name and before it does
anything else, into the log its spawner already gives it:

```text
2026-11-10T10:55:26.301-05:00 helper start: name=git-broker build=0.17.1+39.gb36be7d53044 commit=b36be7d53044 pid=2050311 jail=yolo-yolo-jail-887995ca-podman-self exe=/home/matt/.local/share/yolo-jail/flake-bundles/20261109T211200Z-12345/bin/linux-amd64/yolo
```

- **Keys, in this order, always present:** `name`, `build` (the baked version, the value a
  container's `YOLO_VERSION` carries, or `unstamped`), `commit`, `pid`, `jail` (or `-`), `exe` (the
  running executable with symlinks resolved, or `unknown`). The stamp is the `logstamp` layout every
  daemon log uses. A value containing whitespace or a quote is Go-quoted, so a line splits on spaces.
- **`jail` comes from the spawner** through `YOLO_HELPER_JAIL`, the helper's jail label: the
  container name, the macos-user key, or under `yolo host` the workspace path. It is set after a
  pack manifest's own environment, so a manifest cannot rename it. The removers write no start line
  and are not labeled.
- **Where it goes:** stderr, which every spawner points at the daemon's log; the keeper, whose stdio
  is `/dev/null`, writes it as the first line of its own log.
- **Not written** for a usage error before dispatch, `--self-check` or `--settings-check`. Those are
  reached from `yolo check`, whose output tests compare.

A reader answers "which build served this jail" with one search, for example
`rg 'helper start: name=git-broker' ~/.local/share/yolo-jail/logs/host-service-git-broker.log`, and
"which file is it running" with `ps -o args` or `/proc/<pid>/exe`, which name the bundle.

## What stays unpinned: the user config

**The user config (`~/.config/yolo-jail/`) is shared by every jail and every build, and a running
broker rereads it at every call.** With pinning, a broker older than the yolo that last wrote the
config is the expected case for a long-lived jail. Two things cover that skew:

- **A broker resolves only its own allow table**, so a newer spelling in another source's table
  does not withdraw this broker's grants. An unreadable own table still fails closed, and the
  refusal names the older-broker case.
- **`yolo config apply` names brokers started by an older yolo** and the stop-and-relaunch step
  (`applybrokerskew.go`); the brokers do run the older build, and the note says so.

## Costs

- **A broker fix reaches a running jail only after it is stopped and launched again**, on every
  backend.
- **A Homebrew or archive upgrade under a running jail turns a later helper crash into a give-up**
  rather than a restart on the new build.
- **macOS bundles carry one more binary**, which replaces the separate darwin build of the launcher,
  so a from-source install compiles no more than before.

## Not in scope

- Pinning the user config, or making a broker refuse a config written by a newer yolo.
- Restarting a jail's helpers onto a newer build in place. Relaunch is the path.
- Reporting builds in `yolo host-daemon` or `yolo check`.
- Machine-wide yolo processes that serve no jail.
- A content-addressed per-launch copy of `yolo`: a second store and reaper for bytes the bundle
  already holds immutably.

## Traps

> [!WARNING]
> **The Linux keeper keeps its own inode.** The pin changes only what the keeper is spawned *from*.
> Rewriting the keeper's own self-execs to the pin's path would lose the survives-deletion property
> of JL-D5; `TestAKeepersSelfExecsRunItsOwnInode` pins it.

> [!WARNING]
> **A test proves pinning only through an injected pin.** On Linux `os.Executable()` returns the
> replaced path after `just install`, so a test that execs "the current binary" by path proves
> nothing; tests point the pin at a copy. An integration test reads the daemon log under its run's
> own state directory, never under `~`, which in a jail is the outer jail's live home.

> [!WARNING]
> **The give-up is a cause on the existing path**, in `daemonSupervisor` and `Running.Supervise`,
> never a second give-up path.

What pins the behavior: `internal/helperpin`'s tests hold resolution, the identity check and the
start-line format. `TestTheKeepersSelfExecsGoThroughItsOwnBinary` is a source scan that fails any
unlisted direct `execx.SelfExecArgv` call in the packages that start helpers.
`TestAGenerationAProcessRunsFromIsKept` and `TestAnUnreadableProcessTableReapsNothing` pin the
process-table liveness, and `TestProcessExesNamesTheRunningBinary` its macOS reader.
`TestAHostDaemonRunsAndLogsThePinnedBuild` in `integration/pinnedhelpers_test.go` is the end-to-end
witness.

## Mac witnesses

Linux runs the keeper's self-execs through `/proc/self/exe`, so a nested jail and the integration
suite never reach the pin-path exec, its `stat` check, or the darwin bundle. These witnesses settle
them on a Mac. All five passed on 2026-11-10 at main `5a69dd0b4` (darwin/arm64, installed build
`0.17.1+197.g5a69dd0b4075`). W-PH2 and W-PH3 need `podman-self`, because git-broker is inert on
Apple Container; W-PH4 uses `seatbelt-self`. Re-run them after a change to the pin's macOS path.

| ID | What it shows | How |
| :--- | :--- | :--- |
| <a id="w-ph1"></a>W-PH1 | The darwin bundle carries the host `yolo`, and the installed launcher is that file (PH-D2) | `ls ~/.local/share/yolo-jail/flake-bundle/bin/darwin-arm64/` lists `yolo`; `cmp` against `command -v yolo` prints `same` |
| <a id="w-ph2"></a>W-PH2 | A container jail's helpers run from the pin and say so (PH-D1, PH-D3, PH-D4, PH-D9) | with a `podman-self` jail running, every `internal daemon` line in `ps -axo pid,args` starts with the generation's `bin/darwin-arm64/yolo`; the git-broker start line's `build` equals the jail's `$YOLO_VERSION`; the keeper log's first line is its start line |
| <a id="w-ph3"></a>W-PH3 | A restart after the pin changed gives up (PH-D6, PH-D18) | `touch` the pinned file, `pkill -f 'internal daemon git-broker'`; the broker log shows the give-up and stop step and no second start line, and no broker process remains |
| <a id="w-ph4"></a>W-PH4 | macos-user helpers run from the pin, and a dry run still plans (PH-D4, PH-D21) | a `seatbelt-self` session's start lines show `exe=` under the bundle; `yolo run --setup seatbelt-self --dry-run` prints the plan and exits 0 |
| <a id="w-ph5"></a>W-PH5 | The generation reaper's process reader works on macOS (PH-D8, PH-D14) | `go test -count=1 -run TestProcessExesNamesTheRunningBinary ./internal/prune` prints `ok` |

## Why it's this way

Rulings of 2026-11-10, kept with their original IDs because code comments cite them.

| ID | Ruling | Why it holds |
| :--- | :--- | :--- |
| PH-R1 | The owner's ruling: pin host helpers to the jail's build, and log each helper's version when it starts | A helper whose build depends on what sits at a path when it restarts makes a running jail's behavior unknowable; the owner accepted the relaunch cost. |
| PH-D1 | The pin is the host-OS `yolo` in the bundle the launch resolved, symlinks resolved; same build means same build stamp | The bundle is the one immutable artifact the launch already proved matches the launcher ([where it comes from](#where-the-pin-comes-from)). |
| PH-D2 | macOS bundles carry a host `yolo` in `bin/darwin-<arch>/`, separate from the guest list; install takes the launcher from it; discovery requires it on macOS | Without it a Mac has no immutable host `yolo` to pin. |
| PH-D3 | Resolved once at pre-flight by any launch that will start a helper; a launch that cannot have one refuses before spawning; one that starts none resolves none | Refusing before anything starts is the only point where no helper can already be running another build. |
| PH-D4 | One rule for every self-exec that serves a jail or launch; check-time and machine-wide processes are outside it | [Who execs it](#who-execs-it). |
| PH-D5 | The keeper gets the pin through its plan; on Linux it keeps JL-D5's `/proc/self/exe`, which names the pin's inode | Keeps the survives-deletion property on Linux while the outcome matches every backend ([P3](#p3)). |
| PH-D6 | A `stat` identity check before every exec by path; a failed check never falls back | [P2](#p2). A hash would read a large binary at each spawn for no gain over an immutable generation. |
| PH-D7 | Attaches and joins start nothing the keeper holds; their own children use their own launch's pin | [Attach and join](#attach-and-join). |
| PH-D8 | A generation is in use while a live process of this user executes from it; an unreadable process table declines the sweep | A reaper that cannot ask never treats "unknown" as "unused". |
| PH-D9 | The start line: format, keys and order, `YOLO_HELPER_JAIL` from every spawner, stderr (the keeper: its own log), none for `--self-check` | [P4](#p4). |
| PH-D10 | The user config stays unpinned; a broker's own-table read and the config-apply note cover the skew | [What stays unpinned](#what-stays-unpinned-the-user-config). |
| PH-D11 | The recorded identity is device and inode, size and modification time in nanoseconds; the path has every symlink resolved | Cheap, and changes whenever an install or upgrade replaces the file. |
| PH-D12 | "Same build" compares the bundle stamp's normalized version and commit with the launcher's; a field absent or `unknown` on either side is not compared | Homebrew's launcher and bundle copy differ in bytes but not in build; an older bundle without a field is not refused on it. |
| PH-D13 | The install-check tool excuses the missing darwin host `yolo` only for a plain `X.Y.Z` at or below 0.17.1, the last release staged before PH-D2 | A directory's presence cannot tell 0.17.1 from a later release that lost its host `yolo`; the version can. |
| PH-D14 | The generation reaper reads the process table through `prune.ProcessExes`, which reads it on macOS too; read once per sweep, at listing time | The development-bundle reader declines on macOS, which would leave generations unprotected there. |
| PH-D15 | A daemon writes its start line only after dispatch knows its name; `--self-check` and `--settings-check` write none; the keeper writes through its own log writer | Keeps `yolo check`'s compared output stable. |
| PH-D16 | Every non-dry-run launch whose bundle resolved resolves a pin, an attach included; `yolo config apply`'s context injector resolves its own | An attach can turn into a fresh launch in the same run. |
| PH-D17 | `YOLO_HELPER_JAIL` is set after a pack manifest's environment; under `yolo host` it is the workspace path; the removers are not labeled | A manifest cannot rename the jail a line claims to serve. |
| PH-D18 | Both supervisors check the pin again after the restart backoff, immediately before the restart | A change during the wait gives up too. |
| PH-D19 | `yolo host` resolves its pin at the first helper it starts, once per launch; the Claude credential view skips itself on a pin failure | The view was already never fatal; no other build runs, so [P2](#p2) holds. |
| PH-D20 | Commands that serve no launch keep the yolo the user invoked | They are not a jail's helpers ([who execs it](#who-execs-it)). |
| PH-D21 | A macos-user `--dry-run` with no pin prints its plan with the staged source unresolved | A dry run copies nothing, so neither a refusal nor a fallback is needed. |
| PH-D22 | `launchservice.SelfExec`, the path-based seam, is deleted; a source scan fails any unlisted direct `execx.SelfExecArgv` call in the packages that start helpers | A seam that execs by path is how a different build comes back. |
| PH-D23 | The integration witness uses the git broker, a real `yolo internal daemon` process | Only those processes write a start line. |
