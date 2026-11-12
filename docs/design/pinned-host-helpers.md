---
title: "A jail's host helpers should run the build that launched it, and say which build that is"
date: 2026-11-10
status: accepted
stage: BUILT
next: "Graduate this built design into the system reference"
tags: [design, host-services, keeper, daemons, install, bundles, logging]
summary: "Every host-side process that serves one jail or one launch (keeper, host daemons, launch-owned services, yolo host's private daemons, removers) is exec'd from one pinned file, the host-OS yolo inside the bundle the launch resolved, checked unchanged before every exec, so it runs the launch's build for the jail's whole life or does not run at all. Each such daemon writes a start line naming its build, commit, pid, jail and executable. The user config stays shared and unpinned; the 3f58db1cc own-table read and the config-apply note cover that skew."
---

# A jail's host helpers should run the build that launched it, and say which build that is

**Status:** 2026-11-10. Built; source verified at `e5a75f551`. MEASURED: the owner's Mac ran W-PH1 to W-PH5 at main `5a69dd0b4` on darwin/arm64 with installed yolo `0.17.1+197.g5a69dd0b4075`, Apple Container CLI 1.1.0 and podman machine `applehv`; W-PH2 and W-PH3 passed on `podman-self`, W-PH4 passed with its explicit Seatbelt setup, and W-PH5 printed `ok`. Apple Container cannot run W-PH2's git-broker witness because git-broker is inert on that backend.

> **In short.** A host helper's build should be a property of the launch that started it, not of
> whatever file sits at a path when the helper happens to start or restart. One pinned file per
> launch, inside an immutable bundle, makes that true on every backend; one start line per process
> makes it provable.

**Why it matters.** On 2026-11-10 the shared git-broker log showed a broker coming back up at 10:55
that refused a `branch:main` push with the refusal text from before `3f58db1cc`. The log could not
say which build it was, or which jail it served, so the cause could only be argued, not read. Off
Linux, and at every launch without a keeper, a restarted helper today runs whatever binary an
install has since put at the launcher's path.

**The shape.** The launch resolves one **pinned helper** from its bundle; every self-exec that
serves the jail goes through it, guarded by an unchanged-file check; every helper daemon writes a
**start line**; the bundle reaper counts a running helper as a user.

**Cost.** A broker fix reaches a running jail only after it is stopped and launched again, on every
backend. macOS bundles grow by one binary. A restart after `brew upgrade` or an in-place archive
upgrade gives up instead of running the new build.

**Start at [§3](#3-the-pinned-helper).**

**Needs your ruling:** None.

**Reads with:** [`pinned-host-helpers-plan.md`](pinned-host-helpers-plan.md) (the build plan, by
file), [`jail-lifetime-last-session-wins.md`](jail-lifetime-last-session-wins.md) (the keeper, and
[JL-D5](jail-lifetime-last-session-wins.md#JL-D5), whose Linux inode rule this generalizes),
[`host-daemon-ownership.md`](host-daemon-ownership.md) (per-jail daemons and the restart policy,
[HD-D25](host-daemon-ownership.md#HD-D25)), [`installed-launcher-provenance.md`](installed-launcher-provenance.md)
(one snapshot, one build).

---

## 1. What runs on the host for a jail today

**Every host helper is the running yolo re-exec'd as a subcommand.** Each official host daemon and
the keeper are `yolo internal daemon <name>`; the removers and the context injector are
`yolo internal <verb>`. What differs is how the leading `yolo` is turned into a file:

| Spawner | How `yolo` becomes a file | After `just install` or `brew upgrade` |
| :--- | :--- | :--- |
| A fresh launch spawning its keeper, Linux | `/proc/self/exe` (`realSpawnKeeper`, [`keeperspawn.go`](../../internal/cli/run/keeperspawn.go)) | the launcher's own inode: same build |
| The keeper's own self-execs, Linux (daemons, their restarts, the scratch remover) | `/proc/self/exe` (`keeperSelfExe`, [`keeper_linux.go`](../../internal/cli/run/keeper_linux.go); [JL-D5](jail-lifetime-last-session-wins.md#JL-D5)) | same build |
| The keeper anywhere else (podman on macOS, Apple Container, macos-user) | `os.Executable()`'s path (`execx.SelfExecArgv`, [`execx.go`](../../internal/execx/execx.go)) | **whatever is at the path now**; the keeper's own spawn is guarded by the plan's build stamp ([JL-D20](jail-lifetime-last-session-wins.md#JL-D20)), its later restarts are not |
| A launch with no keeper: `yolo host` doorways and launch-owned services, its private daemons | `os.Executable()`'s path (`Options.selfExecArgv` in [`scratchremoval.go`](../../internal/cli/run/scratchremoval.go), `launchservice.SelfExec`, [`privatedaemon.go`](../../internal/openaiauthhost/privatedaemon.go), [`hostclaudeview.go`](../../internal/cli/hostclaudeview.go)) | **whatever is at the path now**, at every supervised restart |
| macos-user's guest `yolo` | a copy of `os.Executable()` staged per session ([`jaildaemon.go`](../../internal/macosuser/jaildaemon.go)) | a copy taken at launch: same build |

Two facts make "whatever is at the path now" a different build rather than a missing file:

- **`just install` replaces the launcher in place.** It copies the new `yolo` beside `$GOBIN/yolo`
  and renames it over the old one (`_atomic_install_launcher`, [`matched-bundle.py`](../../scripts/matched-bundle.py)).
  On Linux `os.Executable()` reads `/proc/self/exe` and returns that path, which now names the new file.
- **A supervised restart reuses the argv the first spawn resolved**
  ([HD-D28](host-daemon-ownership.md#HD-D28), and `Running.Supervise` in
  [`launchservice.go`](../../internal/launchservice/launchservice.go)), so a path in argv[0] is
  re-read at every restart.

**The jail side is already pinned.** `/opt/yolo-jail` is two read-only bind mounts of the bundle the
launch resolved ([`jailprefix.go`](../../internal/cli/run/jailprefix.go)). An installed bundle is a
**generation**, a directory under `~/.local/share/yolo-jail/flake-bundles/` that no install writes
again ([`flakebundle.go`](../../internal/flakebundle/flakebundle.go)); a development bundle under
`dev-bundles/` is published read-only. Each holds `bin/linux-<arch>/yolo`, byte-identical to the Linux
launcher it was installed with, because `assemble` copies the launcher out of it.

**Nothing logs which build a helper is.** The shared `host-service-git-broker.log` interleaves every
jail's broker and carries no version, pid or jail line at start. The keeper's log names its pid, not
its build.

## 2. Principles

- **P1. A process runs the build of the launch that started it, for its whole life.** A keeper's
  helpers run the fresh launch's build; a later launch's own children run that later launch's build.
- **P2. Never a different build, silently.** When the pinned file is gone or changed, the spawn does
  not happen and the reason is reported with a next step. No fallback to another binary.
- **P3. Every backend behaves the same.** Linux may keep a stronger mechanism where it has one; the
  observable outcome (same build, or a reported refusal) is identical.
- **P4. A running helper proves its own build.** The process writes it, at start, into the log a
  reader already opens; a spawner's intention is not evidence.

## 3. The pinned helper

**Pinned helper** *(coined here)*: the one executable file a launch execs every host helper from. It
is the host-OS `yolo` inside the bundle that launch resolved, at
`<bundle root>/bin/<host os>-<host arch>/yolo`, with every symlink resolved. It is **not** the
launcher on `PATH`, not the stable `flake-bundle` symlink, and not a copy yolo makes.

### 3.1 Where it comes from

- **The same resolution the jail is built from.** `reporoot.Resolve` already chooses one bundle per
  launch and proves it matches the launcher (snapshot identity for installed and development
  builds). The pin is read from that bundle's root after `EvalSymlinks`, so an install that swaps
  `flake-bundle` during the launch cannot move it.
- **Same build means the same build stamp.** The pin is accepted when the bundle's
  `yolo-build-stamp.json` names the launcher's version and commit, and, when the launcher carries a
  snapshot identity, that identity. This is the definition [JL-D20](jail-lifetime-last-session-wins.md#JL-D20)
  already uses for the keeper. It matters for Homebrew, whose `bin/yolo` is built separately from
  the bundle's copy ([`update-homebrew.sh`](../../tools/release-wiring/update-homebrew.sh) builds it
  with `-s -w`): same stamp, different bytes, same build.
- **macOS bundles gain a host `yolo`.** Before PH-D2 a darwin bundle carried only the guest set
  (`yolo-jaild yolo-serial yolo-ps yolo-log`, [`stage-source-bundle.sh`](../../scripts/stage-source-bundle.sh)).
  The darwin directories also get `yolo`, as a separate host list: the guest list is what a guest
  runs, and stays unchanged. `assemble` then takes the macOS launcher out of the bundle as it already
  does on Linux, so a from-source install compiles `yolo` once on both OSes.

| Install channel | Pin | Immutable for the jail's life? |
| :--- | :--- | :--- |
| `just install` | the generation's `bin/<os>-<arch>/yolo` | yes |
| `just dev`, `just deploy-dev`, a dev bundle by path | the dev bundle's `share/yolo-jail/bin/<os>-<arch>/yolo` | yes |
| Homebrew | the keg's `share/yolo-jail/bin/<os>-<arch>/yolo` | until `brew cleanup` removes the keg |
| Release archive | `share/yolo-jail/bin/<os>-<arch>/yolo` beside `yolo` | until the user unpacks a newer archive over it |
| `go install`, pipx, uvx | none: no bundle | not a launch channel today ([getting-started](../../userguide/getting-started.md)) |

### 3.2 When it is resolved

- **At pre-flight, once, by any launch that will start a host helper,** before anything is spawned.
  A container launch always starts a keeper, so it always resolves one. A `yolo host` launch that
  starts no doorway, launch-owned service or private daemon resolves nothing, so a binary-only
  `yolo host` without helpers keeps working.
- **What it records:** the path, and the file's identity at that moment (device and inode, size,
  modification time).
- **When it cannot be had, the launch refuses before starting anything.** The message names what was
  missing (no bundle, no host `yolo` in it, a stamp that differs) and the next step reporoot already
  gives for an incomplete bundle: reinstall from Homebrew or a release archive, or `just install`,
  or `just build-dev-bundle` and its printed launcher.

### 3.3 Who execs it

**Every self-exec that serves a jail or a launch** takes the pin in place of `execx.SelfExecArgv`:

- the keeper's spawn;
- every host daemon a launch or keeper starts, and every supervised restart of one;
- launch-owned services and their restarts (`yolo host`, macos-user);
- `yolo host`'s private Claude and OpenAI broker daemons;
- the scratch and discard removers, and the context injector's child;
- macos-user's per-session guest `yolo`, which is staged from the pin instead of `os.Executable()`.

**The keeper hands the pin on.** The launch writes the pin's path and recorded identity into the
keeper's plan. Off Linux the keeper execs that path for each self-exec. On Linux the keeper keeps
[JL-D5](jail-lifetime-last-session-wins.md#JL-D5)'s `/proc/self/exe`, which, because the keeper was
itself spawned from the pin, now names the pin's inode: the same build, and still runnable if the
file were ever wrongly deleted.

**Outside this rule** (they serve no jail, and run the yolo the user just invoked): `yolo check`'s
self-checks (`doctor_cmd`, the settings check), the preparation scheduler, background advance, shared
demand, and a patched fork's build child. A third-party pack's daemon that names its own program is
not a yolo self-exec and is untouched.

### 3.4 The check before every exec

Before any exec by path, the spawner compares the file at the pin's path with the recorded identity
(same device and inode, same size, same modification time). It is a `stat`, not a hash.

| Moment | Pin unchanged | Pin missing or changed |
| :--- | :--- | :--- |
| A first spawn in the launch | exec | the launch refuses as in [§3.2](#32-when-it-is-resolved) |
| A first spawn in the keeper (a later daemon, a remover) | exec | that spawn does not happen; the keeper logs one line naming the path and the next step |
| A supervised restart ([HD-D25](host-daemon-ownership.md#HD-D25), `Running.Supervise`) | restart | **a give-up**, through the supervisor's existing give-up path: one line in the daemon's log and in `launch.log` (or `host-launch.log`), recorded as down |

The give-up line names the cause and the step:

```text
host service 'git-broker' died (its process exited with status 1); not restarting it: the yolo
this jail was launched with is no longer at /opt/homebrew/Cellar/yolo-jail/0.17.1/share/yolo-jail/bin/darwin-arm64/yolo
(an upgrade removed or replaced it). It stays down until the jail is launched again: `yolo stop`
once its sessions are done, then launch.
```

The stop step is the one `keyedStopRemedy` already prints for the notch, so macos-user and `yolo host`
name their own. On Linux the keeper's execs go through `/proc/self/exe` and never reach the check.

> [!WARNING]
> Do not "repair" a failed check by falling back to `os.Executable()` or to the current
> `flake-bundle`. That fallback is exactly the different-build restart this design removes, and on
> a Mac it is what a `brew upgrade` mid-session would trigger.

### 3.5 Attach and join

**An attach or a join starts nothing the keeper holds**, so it can never restart a helper on its own
build ([§9.9.5 of the keeper design](jail-lifetime-last-session-wins.md#995-how-a-second-launch-joins);
a container attach never re-stages). What a later launch starts for its own session (the context
injector, a macos-user joiner's sandbox and guest supervisor, a `yolo host` joiner's own services)
runs from that launch's own pin, by [P1](#2-principles). A later launch of another build is told so
by the keeper line it already prints.

## 4. Keeping the bundle alive while a helper runs from it

**A generation is in use while a live process of this user executes a file inside it**, as well as
while a running jail mounts it. Today the generation reaper (`flakebundle.ReapGuarded`) counts mounts
only. That already covers a container jail's helpers while the container runs, but not a detached
credential daemon finishing a refresh after its jail ends ([HD-D6](host-daemon-ownership.md#HD-D6)),
nor a macos-user or `yolo host` launch, which mount nothing.

- **The evidence is the process table the development-bundle reaper already reads**
  (`readProcStarts`: `/proc/<pid>/exe` on Linux, `kern.procargs2` on macOS, this user's processes).
- **Unreadable means decline.** A sweep that cannot read the table removes no generation, as
  [AGENTS.md](../../AGENTS.md) requires of every reaper. On macOS this table is complete for
  generations, because the only processes that run a sandbox account's code from yolo's files run
  copies staged into the session's own directory, never the generation itself.
- **Development bundles change nothing.** Their reaper already counts executing processes on Linux
  and declines on macOS (`DevBundleOSWhy`).
- **Homebrew kegs and release archives** are not yolo's to reap; [§3.4](#34-the-check-before-every-exec)
  is what makes their removal safe.

## 5. The start line

Every `yolo internal daemon <name>` process (the keeper, every official host daemon, every doorway and
launch-owned service) writes one line at start, before it does anything else, into the log its
spawner already gives it:

```text
2026-11-10T10:55:26.301-05:00 helper start: name=git-broker build=0.17.1+39.gb36be7d53044 commit=b36be7d53044 pid=2050311 jail=yolo-yolo-jail-887995ca-podman-self exe=/home/matt/.local/share/yolo-jail/flake-bundles/20261109T211200Z-12345/bin/linux-amd64/yolo
```

- **Stamp:** the `logstamp` layout every daemon log already uses.
- **Keys, in this order, always present:** `name` (the daemon name), `build` (`version.Baked()`, the
  value a container's `YOLO_VERSION` carries, or `unstamped`), `commit` (`version.CommitLabel()`),
  `pid`, `jail` (the spawner's name for what it serves, or `-`), `exe` (the running executable,
  symlinks resolved, or `unknown`).
- **A value containing whitespace or a quote is Go-quoted**, so a line splits on spaces.
- **`jail` comes from the spawner.** Every spawner sets `YOLO_HELPER_JAIL` *(coined here: the
  helper's jail label)* in the helper's environment: the container name, the macos-user key, or
  `yolo host`'s workspace key. A process started without it writes `-`.
- **Where it goes:** stderr, which every spawner points at the daemon's log; the keeper, whose stdio
  is `/dev/null`, writes it as the first line of its own log.
- **Not written** for `--self-check` (a check, not a start) or a usage error before dispatch.

A reader answers "which build served this jail" with one search, for example
`rg 'helper start: name=git-broker' ~/.local/share/yolo-jail/logs/host-service-git-broker.log`, and
"which file is it running" with `ps -o args` or `/proc/<pid>/exe`, which now name the bundle.

## 6. What stays unpinned: the user config

**The user config (`~/.config/yolo-jail/`) is shared by every jail and every build, and a running
broker rereads it at every call.** Pinning makes a broker's build fixed, so a broker older than the
yolo that last wrote the config is now the expected case for a long-lived jail, not an accident of a
restart. The 2026-11-10 incident is that case: this jail was launched by `0.17.1+39.gb36be7d53044`,
older than `3f58db1cc`, and on Linux its keeper already restarted helpers from that build
([JL-D5](jail-lifetime-last-session-wins.md#JL-D5)). Pinning alone would not have changed the
refusal; the start line would have proven which build gave it.

What covers that skew, unchanged by this design:

- **A broker resolves only its own allow table** (`3f58db1cc`): a newer spelling in another source's
  table no longer withdraws this broker's grants. An unreadable own table still fails closed, and the
  refusal names the older-broker case.
- **`yolo config apply` names brokers started by an older yolo** and the stop-and-relaunch step
  ([`applybrokerskew.go`](../../internal/cli/run/applybrokerskew.go)). With pinning the hedge in that
  note ("may still run it", written because a restart could run the newer binary) is no longer true:
  the brokers do run it, and the note says so.

## 7. Cost and trade-off

- **A broker fix needs a relaunch, on every backend.** Container jails on Linux already worked this
  way. Off Linux and at `yolo host`, a crash-restart used to pick up a newer build by accident; it no
  longer does. This is the cost the owner accepted with "pin" on 2026-11-10.
- **A Homebrew or archive upgrade under a running jail turns a later helper crash into a give-up**
  rather than a restart on the new build.
- **One more darwin binary per bundle.** It replaces the separate darwin build `assemble` does
  today, so a from-source install compiles no more than before.

## 8. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| A content-addressed copy of the running `yolo` per launch, under yolo's state | Rejected: a second store and a second reaper for bytes the bundle already holds immutably, and a Homebrew pin would no longer be the jail's own bundle. |
| `/proc/self/exe` everywhere | Linux only; macOS has no equivalent, and parity requires the same outcome everywhere. Kept as the keeper's Linux mechanism. |
| Pin by path with no identity check | Rejected: a release archive unpacked in place, or a keg replaced, would silently run another build at a restart. |
| Hash the pin before every exec | Rejected for the `stat` identity: a generation is immutable by construction, and a hash costs a full read of a large binary at each spawn. |
| Fall back to `os.Executable()` when the pin is gone | Rejected by [P2](#2-principles); see the warning in [§3.4](#34-the-check-before-every-exec). |

## 9. Non-goals

- Pinning the user config, or making a broker refuse a config written by a newer yolo
  ([§6](#6-what-stays-unpinned-the-user-config)).
- Restarting a jail's helpers onto a newer build in place. Relaunch is the path.
- Reporting builds in `yolo host-daemon` or `yolo check`. The start line makes either a later,
  independent step.
- Machine-wide yolo processes that serve no jail ([§3.3](#33-who-execs-it)).

## 10. What done looks like

- On Linux and macOS, `ps -o pid,args` and `/proc/<pid>/exe` (Linux) for a jail's keeper and host
  daemons name a file inside the jail's bundle, never `$GOBIN/yolo`.
- Every host-service, launch-service and keeper log has a `helper start:` line per process start,
  whose `build` equals the jail's `YOLO_VERSION`.
- After `just install` under a running jail, killing one of its host daemons restarts it, and the new
  start line shows the jail's old build.
- With the pin removed under a running Mac jail, killing a daemon logs the give-up line of
  [§3.4](#34-the-check-before-every-exec) and does not restart it.
- A housekeeping sweep keeps a generation a detached daemon still runs from, and declines when it
  cannot read the process table.

## Mac checks

Linux runs the keeper's own self-execs through `/proc/self/exe`, so the nested jail and the
integration suite never reach the pin-path exec, its `stat` check, or the darwin bundle. These
witnesses settle them on a Mac, at main `2590e19cc` or later. Run each from a fresh workspace under
`/Users/Shared/yolo/<x>` with `yolo` installed from that main by `just install`. W-PH2 and W-PH3
require `podman-self` because Apple Container carries no container→host connection for git-broker;
W-PH4 uses `seatbelt-self`. Paste every output named under "report". Commands below use
`darwin-arm64`; on Intel read `darwin-amd64`.

### W-PH1: the darwin bundle carries the host yolo, and the launcher is that file (PH-D2)

1. `ls ~/.local/share/yolo-jail/flake-bundle/bin/darwin-arm64/`
   lists `yolo` beside `yolo-jaild`, `yolo-serial`, `yolo-ps` and `yolo-log`.
2. `cmp "$(command -v yolo)" ~/.local/share/yolo-jail/flake-bundle/bin/darwin-arm64/yolo && echo same`
   prints `same`.

Report: both outputs. Settles PH-D2 on macOS.

*Run 2026-11-10 at main `5a69dd0b4`* (darwin/arm64, installed build `0.17.1+197.g5a69dd0b4075`): **passes**. The directory contains the host `yolo` beside the guest binaries, and `cmp` printed `same`.

### W-PH2: a container jail's helpers run from the pin and say so (PH-D1, PH-D3, PH-D4, PH-D9)

1. Start a jail in the workspace with `yolo --setup podman-self -- sh -c 'echo $YOLO_VERSION; sleep 600'` and leave it running.
2. In another terminal: `ps -axo pid,args | grep 'internal daemon' | grep -v grep`
   Every line, the `jail-keeper` and `git-broker` included, starts with a path under
   `~/.local/share/yolo-jail/flake-bundles/<generation>/bin/darwin-arm64/yolo`, never `$GOBIN/yolo`
   or `/opt/homebrew/bin/yolo`.
3. `grep 'helper start: name=git-broker' ~/.local/share/yolo-jail/logs/host-service-git-broker.log | tail -1`
   shows `build=` equal to the `$YOLO_VERSION` the jail printed, `jail=` the container name, and
   `exe=` the same bundle path as step 2.
4. `head -1 ~/.local/share/yolo-jail/logs/jail-keeper-<container name>.log`
   contains `helper start: name=jail-keeper`.

Report: the outputs of steps 2–4 and the jail's `$YOLO_VERSION`. Settles PH-D3, PH-D4 and PH-D9 off Linux.

*Run 2026-11-10 at main `5a69dd0b4`* on `podman-self`: **passes**. The jail version and every listed helper (jail-keeper, host-processes, serial, git-broker, github-broker, aws-auth, openai-auth-broker and claude-oauth-broker) matched the darwin bundle path; the git-broker start line matched build, commit and jail. Apple Container was also tried, but git-broker is inert there, so steps 2–3 require podman.

### W-PH3: a restart after the pin changed gives up (PH-D6, PH-D18)

With the W-PH2 jail still running:

1. Change the pinned file's identity without changing its contents (its modification time):
   `touch <the bundle path from W-PH2 step 2>`
2. Kill the broker: `pkill -f 'internal daemon git-broker'`
3. `tail -3 ~/.local/share/yolo-jail/logs/host-service-git-broker.log`
   shows `host service 'git-broker' died (...); not restarting it: the yolo this jail was launched
   with is no longer at <that path> (an upgrade removed or replaced it). It stays down until the jail
   is launched again:` followed by the stop step, and no new `helper start: name=git-broker` line
   after it.
4. `ps -axo args | grep 'internal daemon git-broker' | grep -v grep` prints nothing.

Report: steps 3 and 4. A restart (a second start line) fails it. Settles PH-D6 and PH-D18 off Linux.
End with `yolo stop` in the workspace.

*Run 2026-11-10 at main `5a69dd0b4`* on `podman-self`: **passes**. After the pinned file's modification time changed, the broker logged the give-up reason and stop step; no second start line or broker process remained.

### W-PH4: macos-user helpers run from the pin, and a dry run still plans (PH-D4, PH-D21)

1. In a fresh workspace, start with `yolo --setup seatbelt-self -- sh -c 'sleep 300'`, and while it runs:
   `grep -h 'helper start:' ~/.local/share/yolo-jail/logs/launch-service-*.log ~/.local/share/yolo-jail/logs/host-service-*.log | tail -5`
   Lines from this session show `exe=` under the bundle's `bin/darwin-arm64/yolo` and `jail=` this
   session's key.
2. `yolo run --setup seatbelt-self --dry-run; echo "rc=$?"` in the same workspace prints the plan and `rc=0`.

Report: both outputs. Settles PH-D4 for macos-user and PH-D21.

*Run 2026-11-10 at main `5a69dd0b4`*: **passes**. The helper start lines used the pinned bundle path. In a workspace defaulting to `apple-container-self`, the dry run needed the explicit `--setup seatbelt-self`; that command printed the plan and exited 0.

### W-PH5: the generation reaper's process reader works on macOS (PH-D8, PH-D14)

1. In a checkout of that main: `go test -count=1 -run TestProcessExesNamesTheRunningBinary ./internal/prune`
   prints `ok`.

Report: the output. A failure means a running helper's generation could be reaped on macOS.

*Run 2026-11-10 at main `5a69dd0b4`*: **passes**; `go test -count=1 -run TestProcessExesNamesTheRunningBinary ./internal/prune` printed `ok`.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| PH-R1 | The owner's ruling: pin host helpers to the jail's build, and log each helper's version when it starts ("yes A doc and then implementation if no OQs") | 2026-11-10 | [§3](#3-the-pinned-helper), [§5](#5-the-start-line) | yes |
| PH-D1 | The pin is the host-OS `yolo` in the bundle the launch resolved, symlinks resolved; same build means same build stamp (and snapshot identity where the launcher has one) | 2026-11-10 | [§3.1](#31-where-it-comes-from) | yes |
| PH-D2 | macOS bundles carry a host `yolo` in `bin/darwin-<arch>/`, as a list separate from the guest set; `assemble` takes the launcher from it; reporoot requires it on macOS | 2026-11-10 | [§3.1](#31-where-it-comes-from) | yes |
| PH-D3 | Resolved once at pre-flight by any launch that will start a helper; a launch that cannot have one refuses before spawning, naming the reinstall step; a launch that starts none resolves none | 2026-11-10 | [§3.2](#32-when-it-is-resolved) | yes |
| PH-D4 | One rule for every self-exec that serves a jail or launch; check-time and machine-wide yolo processes are outside it | 2026-11-10 | [§3.3](#33-who-execs-it) | yes |
| PH-D5 | The keeper gets the pin through its plan; on Linux it keeps JL-D5's `/proc/self/exe`, which now names the pin's inode | 2026-11-10 | [§3.3](#33-who-execs-it) | yes |
| PH-D6 | A `stat` identity check before every exec by path; a failed check never falls back: a first spawn refuses, a keeper spawn is skipped and logged, a restart is a give-up naming the stop step | 2026-11-10 | [§3.4](#34-the-check-before-every-exec) | yes |
| PH-D7 | Attaches and joins start nothing the keeper holds; their own children use their own launch's pin | 2026-11-10 | [§3.5](#35-attach-and-join) | yes |
| PH-D8 | A generation is in use while a live process of this user executes from it; an unreadable process table declines the sweep | 2026-11-10 | [§4](#4-keeping-the-bundle-alive-while-a-helper-runs-from-it) | yes |
| PH-D9 | The start line: format, keys and order, `YOLO_HELPER_JAIL` from every spawner, stderr (the keeper: its own log), none for `--self-check` | 2026-11-10 | [§5](#5-the-start-line) | yes |
| PH-D10 | The user config stays unpinned; `3f58db1cc`'s own-table read and the config-apply note cover the skew, and the note drops its "may still run" hedge | 2026-11-10 | [§6](#6-what-stays-unpinned-the-user-config) | yes |
| PH-D11 | The pin's recorded identity is device and inode, size and modification time in nanoseconds, read through the Unix `stat`; the pin's path has every symlink resolved, the file's own included | 2026-11-10 | [§3.2](#32-when-it-is-resolved) | yes |
| PH-D12 | "Same build" at pin resolution compares the bundle stamp's version (normalized) and commit with the launcher's stamped version and commit; a field absent or `unknown` on either side is not compared, so an unstamped launcher or an older bundle is not refused on it | 2026-11-10 | [§3.1](#31-where-it-comes-from) | yes |
| PH-D13 | The install-check tool excuses from the darwin host `yolo` only a plain `X.Y.Z` at or below 0.17.1, the last release staged before PH-D2, so a published release from before PH-D2 still passes; every later release, a prerelease included, must carry it for both Mac arches. The version decides, not the darwin directories: a directory's presence cannot tell 0.17.1 from a later release that lost its host `yolo`, because 0.17.1 already ships both darwin directories, holding the guest set alone | 2026-11-10 | [§3.1](#31-where-it-comes-from) | yes: the candidate checker ran against the tap's published 0.17.1 on Apple silicon, [run 43125796991](https://github.com/mschulkind-oss/yolo-jail/actions/runs/43125796991) at main `61a2c73d1`, job "brew install from the tap" passed. Intel (`darwin-amd64`) was not run; the checker still requires both Darwin host copies for later or unknown versions |
| PH-D14 | The generation reaper reads the process table through `prune.ProcessExes`, which reads it on macOS too (the development-bundle reader declines there); a missing reader is "could not ask"; it is read once per sweep, at listing time | 2026-11-10 | [§4](#4-keeping-the-bundle-alive-while-a-helper-runs-from-it) | yes |
| PH-D15 | A daemon process writes its start line only after its name is known to dispatch, so a usage error writes none; `--settings-check` is a check like `--self-check` and writes none; the keeper writes its line through its own log writer | 2026-11-10 | [§5](#5-the-start-line) | yes |
| PH-D16 | Every non-dry-run launch whose bundle resolved resolves a pin, an attach included, because an attach that finds the jail gone continues into a fresh launch in the same run; `yolo config apply`'s context injector resolves its pin from its own bundle when it needs one | 2026-11-10 | [§3.2](#32-when-it-is-resolved), [§3.5](#35-attach-and-join) | yes |
| PH-D17 | `YOLO_HELPER_JAIL` is set after a pack manifest's own environment, so a manifest cannot rename it; under `yolo host` its value is the workspace path, the key `yolo host` already keeps its launch state by; the scratch and discard removers are not labeled, since they write no start line | 2026-11-10 | [§5](#5-the-start-line) | yes |
| PH-D18 | Both supervisors ask whether the pin changed again after the restart backoff, immediately before the restart, so a change during the wait gives up too | 2026-11-10 | [§3.4](#34-the-check-before-every-exec) | yes |
| PH-D19 | `yolo host` resolves its pin at the first helper it starts, once per launch, failures included. The Claude credential view keeps its own never-fatal rule: a pin failure there skips the view with its existing warning instead of refusing the launch; no other build runs, so [P2](#2-principles) holds | 2026-11-10 | [§3.2](#32-when-it-is-resolved) | yes |
| PH-D20 | Commands that serve no launch keep the yolo the user invoked: `yolo openai-auth status`, `import` and `logout`, the `yolo macos-*` verbs, capture, and the host floor | 2026-11-10 | [§3.3](#33-who-execs-it) | yes |
| PH-D21 | A macos-user `--dry-run` with no pin prints its plan with the staged source named as unresolved; it neither refuses nor falls back to the running binary, since a dry run copies nothing | 2026-11-10 | [§3.2](#32-when-it-is-resolved) | yes |
| PH-D22 | `launchservice.SelfExec`, the path-based seam, is deleted; tests start services from fixture bundles, and a source scan fails any unlisted direct `execx.SelfExecArgv` call in the packages that start helpers | 2026-11-10 | [§3.3](#33-who-execs-it) | yes |
| PH-D23 | The integration witness uses the git broker, a real `yolo internal daemon` process, because only those write a start line; the start line's reference lives in [`host-service-startup-diagnostics.md`](../reference/host-service-startup-diagnostics.md) | 2026-11-10 | [§5](#5-the-start-line) | yes |
