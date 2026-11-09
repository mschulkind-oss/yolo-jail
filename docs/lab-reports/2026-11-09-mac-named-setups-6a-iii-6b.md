---
title: "Mac run of the named-setups checks for 6a-iii and 6b: Seatbelt workspaces from before 6a-iii refuse to launch"
date: 2026-11-09
tags: [lab-report, macos, macos-user, apple-container, named-setups, ci]
summary: "At main daf82c79a, W-SU1, the move in W-SU4 and the Apple Container half of W-SU12 pass. Every Seatbelt workspace last launched before 6a-iii refuses its next launch, because the user-config delivery record is named by the old jail name. W-SU4's symbolic-link case meets an earlier refusal. Four TestMacosUser tests fail, three of them on CI before 6a-iii too."
---

# Mac run of the named-setups checks for 6a-iii and 6b

**Run 2026-11-09 on the owner's Mac** (darwin/arm64, Apple Container CLI 1.1.0, podman machine
`applehv`). The new build was a dev bundle at main `daf82c79a`
(`2bf4a95a730e562e…/bin/yolo`), which contains `e67b3c81f`. The yolo from before 6a-iii was a
dev bundle at `bb10db358` (`f61ec75d3d6f2ef4…/bin/yolo`), the commit the plan names, instead of an
installed one. Every launch set `YOLO_ALLOW_MISSING_PROGRAMS=1`, because the owner's `pi` fork
build is pending. Nothing was fixed.

The checks are the [named-setups plan's Mac checks](../design/named-setups-plan.md#mac-checks).

## Observed

| Witness | Result |
| :--- | :--- |
| **A Seatbelt workspace from before 6a-iii, relaunched** | **Fails**: the launch refuses on its own user-config copy, see [§1](#1-seatbelt-workspaces-from-before-6a-iii-refuse-to-launch) |
| W-SU1, both Seatbelt setups side by side | **The launch half passes**. The `TestMacosUser` half has 4 failures in 159 passes, see [§4](#4-the-testmacosuser-suite) |
| W-SU4 (a), a jail of the project running | **Passes** |
| W-SU4 (b), `.yolo/home` a symbolic link | **Does not match**: the launch refuses, see [§2](#2-w-su4-b-meets-the-linked-state-refusal) |
| W-SU4, the move | **Passes** |
| W-SU4, `TestAppleContainer` | **Passes**, run later at `b36be7d53`, see [§5](#5-later-the-same-day-w5-and-the-testapplecontainer-suite) |
| W-SU12, Apple Container | **Passes**, except that the refusal names only `yolo stop` |
| W-SU12, Seatbelt | **The refusal and stop pass, and the relaunch fails** as in [§1](#1-seatbelt-workspaces-from-before-6a-iii-refuse-to-launch) |
| W-SU12, `yolo prune` | **Passes** |
| Nightly 42482019048 (`310130d79`) | One failure, `TestCircleCIFakeHostCLIUsesRealClassifier`, the test defect the [11-07 report](2026-11-07-mac-session-homes-and-nightly-42372802408.md#3-nightly-42372802408) explained |
| Triage [W6](2026-11-06-mac-ci-triage-witnesses.md#4-the-60-minute-step-cap) | **Both readings are in**: 59 min on 11-07, with a 20-minute hang, and 52 min on 11-08 |
| Triage W5 | **Passes** on the on-tag form, run locally at the `v0.17.1` tag, see [§5](#5-later-the-same-day-w5-and-the-testapplecontainer-suite) |

### 1. Seatbelt workspaces from before 6a-iii refuse to launch

A Seatbelt workspace last launched by a yolo from before 6a-iii refuses its next launch on the new
build:

```text
refusing macos-user inherited preflight scope: existing user file
/Users/Shared/yolo/su12-d/.yolo/setups/seatbelt-self/home/.config/yolo-jail/config.toml is in the
way, and the home-overlay installer cannot prove it is a file it delivered before, so installing
the user config there could overwrite it. Review it, preserving its contents and mode, then move it
aside (mv …) and relaunch
```

It happened to two workspaces:

- `su12-d`, during W-SU12's Seatbelt half (`seatbelt-self`), right after `yolo stop` ended the old jail.
- `w7-move`, laid on 2026-11-06 at `c5990cf51` under `seatbelt-yolo-account` and untouched
  since.

**The cause.** The file is yolo's own copy (`-r--r--r-- matt:_yolojail`, first line `# GENERATED
by yolo at jail launch`). A Seatbelt launch proves that through a delivery record under
`/Users/Shared/yolo-staged/inherited-delivered/`, named after the staged overlay, which is named
after the jail (`entrypoint.InheritedDeliveryRecordPath`,
`internal/entrypoint/inheriteddelivery.go:45`). The only records for these workspaces are
`yolo-su12-d-fe3a8973.json` and `yolo-w7-move-0e9df7a2.json`, the old jail names. 6a-iii names
the jails `yolo-su12-d-fe3a8973-seatbelt-self` and so on, so the new launch finds no record and
refuses.

**Effect.** Every Seatbelt workspace used before 6a-iii, `seatbelt-self` or
`seatbelt-yolo-account`, stops at its first launch on the new build. Getting past it takes a
hand `mv` of a file yolo wrote. The owner's own workspaces were not launched to check.

**Decision needed:** none on whether this is a defect. The fix is the owner's to choose: carry
the record across the rename, or also accept a record under the legacy name.

### 2. W-SU4 (b) meets the linked-state refusal

With `.yolo/home` replaced by `home-real` plus a link to it, `yolo --setup apple-container-self --
true` exited 1 before the move ran:

```text
Refusing to launch: /Users/Shared/yolo/su4-b/.yolo/home is a symbolic link to
/Users/Shared/yolo/su4-b/.yolo/home-real. The jail can write this workspace's .yolo, so a link
there would carry the launcher's writes, and the container's binds, to wherever it points.
If the jail left that link, remove it (rm …/.yolo/home); … If you moved the directory to
…/.yolo/home-real yourself, move it back instead, …: rm …/.yolo/home && mv …/.yolo/home-real
…/.yolo/home. There is no override for this one.
```

The link and its target were unchanged, and no `.yolo/setups` was made. The refusal is the
linked-state check (`internal/cli/run/wsstatebeneath.go:47`). Its comment says `Run` refuses a
linked `.yolo` or legacy home "before the launch log". W-SU4 (b) expects the launch to ask
nothing and print the move's "left in place" line, which is
[SUP-D20](../design/named-setups-plan.md#SUP-D20)'s case (d) and *"never refuses the launch"*. The
earlier guard stops the launch before that case can run.

**Decision needed (owner):** whether W-SU4 (b)'s expectation, or SUP-D20's "never refuses",
should give way to the existing refusal. The refusal already names the next step.

### 3. The other witnesses

**W-SU1.** The workspace was `/Users/Shared/yolo/su1-a`. `/Users/Shared/yolo/a` was not used,
so as not to disturb anything already there.

- **Launches.** `yolo --setup seatbelt-self` printed
  `HOME=/Users/Shared/yolo/su1-a/.yolo/setups/seatbelt-self/home` and `matt`, and
  `--setup seatbelt-yolo-account` printed `…/seatbelt-yolo-account/home` and `_yolojail`.
- **`yolo setup list`** marked both `running`.
- **`yolo stop`** printed `stopped seatbelt-self` and `stopped seatbelt-yolo-account`, and both
  sessions ended with 143.
- **`env | grep YOLO_RUNTIME`** printed nothing.
- **A stray line.** Its first line was *"No jail running for this workspace
  (yolo-su1-a-ee4f8cdf-apple-container-self)."* That names this OS's default setup, which ran
  no jail, before the two stops that did. The same line opened both `yolo stop` runs in W-SU12.

**W-SU4.** The legacy home was laid by the installed `0.16.1+35.g7f1210e630c9` under
`apple-container-self` in `su4-base`, then copied to `su4-a`, `su4-b` and `su4-c`.

- **(a)** A `seatbelt-self` jail was running. The Apple Container launch asked nothing and
  printed *"This project's home …/su4-a/.yolo/home moves to
  …/.yolo/setups/apple-container-self/home once no jail of the project runs; a jail of this
  project is running, so the apple-container-self setup keeps using it until then. Run `yolo
  stop` and launch again to move it."* `.yolo/home/.claude.json` was still there.
  `.yolo/setups/apple-container-self` held only `config-boot.json`,
  `config-assembled.json` and `config-assembled-origins.json`, and no `home`, as
  [SUP-D21](../design/named-setups-plan.md#SUP-D21) requires.
- **The Seatbelt launch in (a)** printed *"This project's home … was laid by an Apple Container
  setup, so the seatbelt-self setup starts with its own home and leaves that one in place."*
- **The move.** It asked nothing and printed *"Moved this project's home
  /Users/Shared/yolo/su4-c/.yolo/home to
  /Users/Shared/yolo/su4-c/.yolo/setups/apple-container-self/home: each setup keeps its own home
  now."* Afterwards `ls .yolo/home` reported no such file, and
  `.yolo/setups/apple-container-self/home/.claude.json` exists.
- `YOLO_SETUP=apple-container-self go test -run '^TestAppleContainer' ./integration` was not run.

**W-SU12, Apple Container**, in `/Users/Shared/yolo/su12-c`:

- **The refusal.** With the old jail `yolo-su12-c-72ac2cea` running, the new launch refused: *"Refusing to
  launch: a jail an earlier yolo started for this workspace (yolo-su12-c-72ac2cea) is still
  running. It holds this workspace's home, and this yolo names each setup's jail apart, so it
  would start a second jail beside it."* The next line was *"Stop it, then launch again: `yolo
  stop` from this workspace."* It does not also name `container stop yolo-su12-c-…`, which the
  witness expects.
- **`ls .yolo/setups`** showed no new directory. `apple-container-self` was already there from
  the old bundle, which carries 6a-ii.
- **`yolo stop`** printed *"Stopping the jail an earlier yolo started for this workspace
  (yolo-su12-c-72ac2cea)."*, and the old shell ended with 143.
- **The relaunch** ran, and `container ls` names it `yolo-su12-c-72ac2cea-apple-container-self`.
- **Prune.** With that jail stopped, `yolo prune` listed *"Apple Container tool disks … none"*.
  `container volume ls` still shows `yolo-su12-c-72ac2cea-apple-container-self.mise`, and also
  the old jail's `yolo-su12-c-72ac2cea.mise`.

**W-SU12, Seatbelt**, in `/Users/Shared/yolo/su12-d`:

- The refusal was the same, and named `yolo stop` alone, as expected.
- `yolo stop` added *"This workspace's jail was launched on macos-user, so this stop asks
  macos-user, not container."*, and the old shell ended.
- The relaunch failed as in [§1](#1-seatbelt-workspaces-from-before-6a-iii-refuse-to-launch).

### 4. The TestMacosUser suite

`YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^TestMacosUser' ./integration`
took 1049 s: 159 passed, 4 failed and 4 skipped. The skipped ones were
`TestMacosUserMacosLogBridgeScopesToTheSandbox`, `TestMacosUserPasswordlessStartAndExit` and the
two `…RelocationOnAVolumeMeasurement` tests.

| Test | What it said | Also on CI? |
| :--- | :--- | :--- |
| `TestMacosUserCapturesConfigEditsWhenTheSessionEnds` | *"no overlay sidecar after the session ended (open …/.yolo/setups/seatbelt-yolo-account/prism/claude-settings.overlay.json: no such file or directory)"* | Yes, in macos-user run 42525693335 at `310130d79` |
| `TestMacosUserJailDaemonRunsConfinedInTheGuest` | *"the host cannot read the supervisor's log: open /Users/Shared/yolo/yolo-it-…/.local/state/yolo-jail-daemons/supervisor.log"*. The launch names the log under `…/.yolo/setups/seatbelt-yolo-account/home/.local/state/…`, so the test reads a path without the setup's home | Yes |
| `TestMacosUserOverlayInstallKeepsTheAgentStateBesideItsSkills` | *"generated = "GONE" after the second launch, want "PRESENT""* (its G36 wording) | Yes |
| `TestMacosUserMemoryGuard` | *"HOG: no guard line naming resources.memory"*, with `ps` inside the sandbox giving `/bin/ps: Operation not permitted` | No, only here |

`310130d79` predates 6a-iii, and the run at `8987ad733` on 11-06 passed. So the first three began
between those commits, not with 6a-iii.

### 5. Later the same day: W5 and the TestAppleContainer suite

**W5.** At a detached worktree of the `v0.17.1` tag (commit `7f62a455c`), `go test -count=1
-timeout 0 -v -run '^TestTheJailsYoloCarriesItsMatchedBundleStamp$' ./integration` passed in
9.76 s. A dev bundle built at that worktree reports `yolo-jail 0.17.1+g7f62a455cf58` and build
stamp `v0.17.1-0-g7f62a455cf58`: the on-tag form, with no commit count. W5 asked for this on a
nightly, but no nightly has run a tag commit, so this was run locally instead. The scheduled
nightly 42643319301, created 2026-11-09, ran `310130d79` again, the same commit as 11-08's, so it
does not cover 6a-iii either. Its shard 4 had failed while shards 15 to 18 were still running,
and its logs could not yet be fetched.

**`TestAppleContainer`.** At main `b36be7d53`, `YOLO_SETUP=apple-container-self go test -count=1
-timeout 0 -v -run '^TestAppleContainer' ./integration` took 334 s: all 23 tests passed, none
skipped.

## Fixed and committed

Nothing.

## Open

1. [§1](#1-seatbelt-workspaces-from-before-6a-iii-refuse-to-launch): Seatbelt workspaces from
   before 6a-iii refuse to launch. This is the one to fix first.
2. [§2](#2-w-su4-b-meets-the-linked-state-refusal): W-SU4 (b) against the linked-state refusal. (Owner.)
3. The W-SU12 Apple Container refusal names only `yolo stop`, and `yolo stop` opens with a "No jail
   running" line about the default setup.
4. The four suite failures in [§4](#4-the-testmacosuser-suite).
5. ~~`TestAppleContainer` and W5~~: both pass, see
   [§5](#5-later-the-same-day-w5-and-the-testapplecontainer-suite).
6. `TestCircleCIFakeHostCLIUsesRealClassifier` still fails nightly.

## Evidence

- Workspaces `/Users/Shared/yolo/{su1-a,su4-base,su4-a,su4-b,su4-c,su12-c,su12-d,w7-move}`,
  left in place.
- `ls /Users/Shared/yolo-staged/inherited-delivered | grep -E 'su12-d|w7-move'`, and `ls -leO` and
  `head -1` of each refused `config.toml`.
- `gh api …/actions/runs/42482019048/jobs` and `…/actions/runs/42525693335/jobs`, plus each failed
  job's log.
