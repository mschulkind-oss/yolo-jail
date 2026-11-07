---
title: "Mac run of the CI triage witnesses W1–W4, plus two named-setups measurements"
date: 2026-11-06
tags: [lab-report, ci, macos, macos-user, apple-container, named-setups]
summary: "On a real Mac at 9e1e9d8d3: the hello image builds, the config-migration tests pass under podman-self with no image build but fail under apple-container-self, and the macos-user setup-name and concurrency tests pass. Also measured: Apple Container's startedDate encoding, and who owns a seatbelt-yolo-account home."
---

# Mac run of the CI triage witnesses W1–W4

**Run 2026-11-06 on the owner's Mac** (darwin/arm64, Darwin 25.5.0, podman machine `applehv`,
Apple Container CLI 1.1.0). Everything ran at main `9e1e9d8d3`, from the dev bundle
`3742f6d811d691a0…/bin/yolo`, which reports `yolo-jail 0.16.1+222.g9e1e9d8d3a32`. Nothing was
fixed: this report records results only.

The witnesses are the ones the
[Mac CI triage report](2026-11-06-mac-ci-triage-witnesses.md#open) asks for. The setup
names, such as `podman-self`, are the built-in setups of
[named setups](../design/named-setups.md).

## Observed

| Witness | Result |
| :--- | :--- |
| Triage W1, the `hello` image build | **Passes.** It built natively, without the Linux-builder offload, so there was no failure report to read |
| Triage W2, the two config-migration tests | **Pass under `podman-self`**, CI's setup, with no image build. **Both fail under `apple-container-self`**, see [§2](#2-the-config-migration-tests-fail-under-apple-container-self) |
| Triage W3, the DP-B19 header | **Passes**: `TestMacosUserFooterSaysItsSetup` and `TestMacosUserBriefingAndLaunchLinesDescribeThisBackend` |
| Triage W4, two concurrent launches | **Passes**: "A joined \| B spawned", one broker |
| Triage W5, the version stamp on a tag commit | **Cannot run yet**: origin has no `v0.17.0` tag, though `3f131dfe2` is `chore(release): 0.17.0` |
| Triage W6, the shard step times | **Cannot run yet**: the newest scheduled nightly, 42228358413, tested `11e21ce18`, which predates the fixes |
| W-SU11's `startedDate` paste | **Measured**: an RFC 3339 UTC string, see [§3](#3-apple-containers-starteddate-for-w-su11) |
| The named-setups plan's question on who owns a `seatbelt-yolo-account` home | **Measured**: the home directory is the user's and nearly everything in it is `_yolojail`'s, see [§4](#4-who-owns-a-seatbelt-yolo-account-home) |

### 1. W1: the hello image builds

The workspace was `/Users/Shared/yolo/tri-w1`, holding a `yolo-jail.toml` with
`[install]` / `nix = ["hello"]`. (`packages = ["hello"]` is refused in TOML: *"use install.nix
instead"*.) It launched with
`YOLO_SETUP=podman-self YOLO_ALLOW_MISSING_PROGRAMS=1 <dev yolo> --accept-config-changes -- hello`.

- Result: `rc=0` in 2 min 59 s, and the jail printed `Hello, world!`.
- The image build was the plain one: "Building the jail image with nix… (17s)", then done. No
  offload ran, so no `containerbuilder.Session.Failure` reason was printed.
- Most of the time went to the owner's `pi` fork build: `preparation.build` took 80 s and
  `launch.fork_builds` 85 s. That build failed in `fork-build.mjs`, which is the existing
  pending pi build and unrelated to this witness. `YOLO_ALLOW_MISSING_PROGRAMS=1` let the
  launch go on.
- **Not as CI has it.** This Mac's nix daemon shows `substituters = https://cache.nixos.org/`,
  an empty `trusted-substituters` and `accept-flake-config = false`. That means the flake's
  `yolo-jail.cachix.org` substituter is not used here, while CI uses it through
  `cachix-action` and `--accept-flake-config`. The base image was already in the local store.

So this Mac cannot reproduce the 11-05 build failure. The decision the triage report asks for,
whether the cause is a builder defect or the runner's podman machine, still has no witness.

### 2. The config-migration tests fail under apple-container-self

The first run had no `YOLO_SETUP`, so it took this Mac's default, `apple-container-self`. Both
tests failed there (`66.8 s`):

```text
--- FAIL: TestOfferAnsweredNoLaunchesOnTheOldFiles (19.61s)
    configmigration_test.go:272: answering n must still launch: rc 1
      refusing Apple Container inherited preflight scope: existing user file
      <tmp>/003/.yolo/home/.config/yolo-jail/config.toml is in the way, and the whole HOME
      bind would overwrite it. Review it, preserving its contents and mode, then move it aside
      (mv … config.toml.moved-aside) and relaunch
--- FAIL: TestConvertLaterSilencesTheOffer (29.93s)
    configmigration_test.go:367: the silenced launch should run on the old files: rc 1
      (the same refusal, for <tmp>/005/.yolo/home/.config/yolo-jail/config.toml)
```

The second launch of each test fails. Each test's first launch succeeds and leaves
`.yolo/home/.config/yolo-jail/config.toml` behind, and the next launch of the same workspace
refuses because of that file. The refusal is the Apple Container branch of
`userConfigMountArgsForAssembly` (`internal/cli/run/inheritscope.go:510`). It calls
`refuseExistingScopeDestination` (`:316`), which refuses for any file at the destination,
whether yolo or the user wrote it.

The likely trigger is a JSONC user config. These tests give the user scope a
`config.jsonc`, and this refusal sits inside the `selectLegacy` callback for the legacy
format. An ordinary Apple Container workspace with this Mac's TOML user config relaunched fine
(`/Users/Shared/yolo/su11-ac`, two launches, both `rc=0`, `config.toml` present before the
second). That points at the format. It was not narrowed further, so the trigger is unconfirmed.

Under `YOLO_SETUP=podman-self`, CI's setup, both pass (`41.6 s`) and neither prints "Building the
jail image with nix: done". That is triage W2 as written. Under `apple-container-self` each
launch does print an image line, a cached evaluation of 4–6 s.

**Decision needed (owner):** whether an Apple Container user whose user config is still JSONC
can relaunch a workspace at all. If not, that is a real defect with no test on CI, because the
nightly sets `YOLO_SETUP: podman-self` (`.github/workflows/nightly-macos.yml:870`). Separately,
the triage report's statement that "none of them builds an image" holds for podman only.

### 3. Apple Container's startedDate, for W-SU11

[W-SU11](../design/named-setups-plan.md#mac-checks) asks for
`container inspect <name> | jq '.[0].status'` from a running jail. 11d is not built, so only
this paste of the witness could run. The jail was
`YOLO_SETUP=apple-container-self <dev yolo> -- sleep 240` in `/Users/Shared/yolo/su11-ac`:

```text
$ container inspect yolo-su11-ac-f113f9bc | jq '.[0].status | {startedDate,state}'
{
  "startedDate": "2026-11-06T19:57:12Z",
  "state": "running"
}
$ date -u +%FT%TZ
2026-11-06T19:57:12Z
```

- `status` is an **object**, not a string. Its keys are `networks`, `startedDate` and `state`.
- `startedDate` is an RFC 3339 timestamp in UTC with second precision, and it matched the
  wall clock at start.
- `container ls --all --format json` carries the same `status` object. Its top-level keys are
  `configuration`, `id` and `status`.
- `yolo-ac-builder` shows the same shape: `"startedDate": "2026-10-09T22:39:57Z"`.

While that jail ran, `yolo ps` from the dev bundle printed the whole `container ls` row as
STATUS and listed `yolo-ac-builder` with workspace `unknown`. That is the `ParseContainerLsRows`
finding of the
[earlier report's §7](2026-11-04-host-workspace-skills-link-and-ssh.md#7-later-the-same-day-712b4238c-on-the-mac-and-the-macos-guides-yolo-ps--yolo-stop),
unchanged at `9e1e9d8d3`. `YOLO_SETUP=podman-self yolo ps` printed "No running jails."

### 4. Who owns a seatbelt-yolo-account home

The [6a-ii entry conditions](../design/named-setups-plan.md#slice-6a--one-home-and-run-state-per-setup-after-4b-5-and-9-launch-path)
ask this. The answer comes from three homes that `seatbelt-yolo-account` launches laid at
`0.16.1+35.g7f1210e630c9`: `w1-d`, `w1b-a` and `kc5-witness`, all under `/Users/Shared/yolo/`.

| Path | Owner:group | Mode |
| :--- | :--- | :--- |
| `<ws>/.yolo/home` | `matt:_yolojail` | `drwxr-xr-x`, plus four inherited ACL entries: `group:_yolojail` and one UUID, each with add/delete/chown, file and directory inherit |
| entries directly inside it | 24 of 25, 26 of 27 and 25 of 26 are `_yolojail`'s | — |
| the one user-owned entry in each | `.config`, `matt:_yolojail` | — |
| `<ws>/.yolo/boot.log` | `_yolojail` | — |

Under [SUP-D20](../design/named-setups-plan.md#SUP-D20), the move checks "the source's type,
its owner…". The source is the `.yolo/home` directory, and that is owned by the invoking user,
so it passes the ordinary owner rule even without
[SUP-D31](../design/named-setups-plan.md#SUP-D31). SUP-D31's premise, *"That setup lays its home
as `_yolojail`"*, is true of the home's contents but not of the directory. A rename of the
directory needs write access to `.yolo` only, so the `_yolojail`-owned contents do not stop it.

**Decision needed:** none for the move. Whoever builds 6a-ii should know the directory is the
user's, in case a check reads ownership below the top.

### 5. Smaller things seen

- The concurrency test's keeper line still reads *"this workspace's guest-notch macos-user host
  services"*. Slice 5 deleted the notch code, but this wording is still printed, and
  `7fbca26d3` made the test read it.
- An integration launch under macos-user warns `YOLO_JAIL is not a recognized launch setting`
  and `YOLO_TEST_MACOS_USER is not a recognized launch setting`. These come from the harness's
  own environment.

Both are gone at `c5990cf51` ([§6](#6-later-the-same-day-w7w9-at-c5990cf51)).

### 6. Later the same day: W7–W9 at c5990cf51

The triage report then gained W7, W8 and W9
(its [§5](2026-11-06-mac-ci-triage-witnesses.md#5-the-legacy-homes-move-under-the-yolo-account) and [§6](2026-11-06-mac-ci-triage-witnesses.md#6-the-two-fixes-from-the-mac-run)).
They ran at main `c5990cf51`, from dev bundle `9eb3b90524ec6bef…`, which reports
`yolo-jail 0.17.0+67.gc5990cf5197e`. All three pass.

| Witness | Result |
| :--- | :--- |
| W7, the legacy home's move under the yolo account | **Passes.** Prints "Moved this project's home" with no `EACCES` |
| W8, the Apple Container relaunch | **Passes**, all four parts |
| W9, `yolo ps` on Apple Container | **Passes** |
| W4 again, for `b1daae49e` | **Passes**, and both [§5](#5-smaller-things-seen) items are gone |

**W7.** The workspace was `/Users/Shared/yolo/w7-move`.

- **First launch**, with the installed `0.16.1+35.g7f1210e630c9`, which predates 6a-ii:
  `YOLO_SETUP=seatbelt-yolo-account yolo -- id -un` printed `_yolojail`. It laid `.yolo/home` as
  `matt:_yolojail drwxr-xr-x`, with the four inherited ACL entries of
  [§4](#4-who-owns-a-seatbelt-yolo-account-home). Of 25 entries inside, 24 were `_yolojail`'s.
  There was no `.yolo/setups`.
- **Second launch**, on the dev bundle (`rc=0`), printed:

  ```text
  Moved this project's home /Users/Shared/yolo/w7-move/.yolo/home to /Users/Shared/yolo/w7-move/.yolo/setups/seatbelt-yolo-account/home: each setup keeps its own home now.
  _yolojail
  HOME=/Users/Shared/yolo/w7-move/.yolo/setups/seatbelt-yolo-account/home
  ```

- **After the move**, `.yolo/home` is gone, and
  `.yolo/setups/seatbelt-yolo-account/home` is `matt:_yolojail drwxr-xr-x`. Inside it, 24
  entries are `_yolojail`'s and one is `matt`'s, the same counts as before the move.
- **Two adoption lines on the moving launch.** That launch also printed `…/home/.config/mcp/mcp.json:
  adopted; the file as yolo found it is archived at …/.yolo/archive/config/pi-subagents-mcp/mcp.json`,
  and the same for `.codex/config.toml`. The first launch printed none, and a third launch
  printed none either. The likely cause is that the files' recorded path changed with the move.
  This was not checked in code.
- **Cost.** The moving launch took 40 s in `launch.build_home_overlay`.

**W8.** The checks ran with no `YOLO_SETUP`, so under `apple-container-self`.

- `TestOfferAnsweredNoLaunchesOnTheOldFiles` passed (18.0 s), and so did
  `TestConvertLaterSilencesTheOffer` (39.5 s), for `ok … 75.548s`.
- **Relaunch of a pre-fix workspace.** `/Users/Shared/yolo/su11-ac` was laid at `9e1e9d8d3` and
  launched with `rc=0`. The same launch moved its legacy home: "Moved this project's home
  …/su11-ac/.yolo/home to …/su11-ac/.yolo/setups/apple-container-self/home".
- **The copy after a launch.** `config.toml` is `matt 1 links -rw-r--r--`, and its first
  line is `# GENERATED by yolo at jail launch — do not edit; your edits are`.
- **An edited copy is refused.** One appended line, then a relaunch with `rc=1`:
  *"refusing Apple Container inherited preflight scope: existing user file
  …/apple-container-self/home/.config/yolo-jail/config.toml is in the way, and the whole HOME
  bind would overwrite it. … move it aside (mv … config.toml.moved-aside) and relaunch"*.
- **The named step works.** After that `mv`, the relaunch ran with `rc=0` and wrote a fresh
  `config.toml` beside the `.moved-aside` one.
- **A wording mismatch.** The copy's own header continues *"your edits are overwritten on the next
  launch"*, but an edit makes the next launch refuse instead.

**W9.** The listings were taken while `yolo-su11-ac-f113f9bc` ran `sleep 150`, with
`yolo-ac-builder` up.

- `container ls --format json`, without `--all`, has the same shape as with it. The top-level
  keys are `configuration`, `id` and `status`; the `status` keys are `networks`, `startedDate`
  and `state`.
- `yolo ps`:

  ```text
  CONTAINER              STATUS                              WORKSPACE
  yolo-ac-builder        running since 2026-10-09T22:39:57Z  unknown
  yolo-su11-ac-f113f9bc  running since 2026-11-07T03:54:26Z  /Users/Shared/yolo/su11-ac
  ```

- `yolo ps --format json` gave `"runtime": "container"`, `"enumerated": true` and the same two
  rows. The banner goes to stderr, so `2>/dev/null | jq` parses the JSON.

**W4 again.** After `b1daae49e`, the test passed (15.7 s), with "A spawned | B joined". The
keeper line now reads *"this workspace's Seatbelt host services"*. No `guest-notch` and no
`not a recognized launch setting` line appears in the log.

## Fixed and committed

Nothing. This report is the only commit.

## Open

1. The Apple Container JSONC relaunch refusal in [§2](#2-the-config-migration-tests-fail-under-apple-container-self):
   is it a defect, and should a test run under `apple-container-self`? (Owner.) Fixed by
   `45ad8d15b`, and W8 passes on the Mac ([§6](#6-later-the-same-day-w7w9-at-c5990cf51)).
   Still open: whether CI should run any test under `apple-container-self`, and the copy's
   header, which says edits are overwritten when they are in fact refused.
2. `yolo ps` under Apple Container prints the whole `container ls` row as STATUS
   ([§3](#3-apple-containers-starteddate-for-w-su11)). Fixed by `189f01062`, and W9 passes on
   the Mac.
3. Triage W1's build failure is not reproduced on this Mac, whose nix does not trust the Cachix
   substituter. A rerun on a CI runner is the only witness left.
4. Triage W5 waits on a nightly at or after the `v0.17.0` tag, which now exists at `6ac121c04`.
   W6 waits on the next two scheduled nightlies; none has run since `11e21ce18`.
5. W-SU1, W-SU4 and the rest of W-SU11 wait on slices 6a-iii, 6b and 11d. 6a-ii is on main,
   and W7 shows its move working on the Mac.
6. The MCP one-sign-in [Mac checks](../design/mcp-auth-sharing.md#mac-checks) need the owner.
   They need an interactive Slack sign-in through `/mcp` and the Slack plugin installed.

## Evidence

- Commit `9e1e9d8d3`; dev bundle `3742f6d811d691a0-62ac992691334affb4d2f65aef1a8db3`.
- `YOLO_TEST_JAIL_TIMEOUT=900 go test -count=1 -timeout 0 -v -run '^(TestConvertLaterSilencesTheOffer|TestOfferAnsweredNoLaunchesOnTheOldFiles)$' ./integration`
  failed both tests with no `YOLO_SETUP` (the default, `apple-container-self`), and passed
  with `YOLO_SETUP=podman-self`.
- `YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^(TestMacosUserFooterSaysItsSetup|TestMacosUserBriefingAndLaunchLinesDescribeThisBackend|TestMacosUserTwoConcurrentLaunchesOfOneWorkspace)$' ./integration`:
  `ok … 47.349s`.
- `env | grep YOLO_RUNTIME` printed nothing.
- `nix config show`, `container --version`, `podman machine list`,
  `gh run list --workflow nightly-macos.yml --limit 5`, `git ls-remote --tags origin 'v0.17*'`
  (empty).
- `stat -f %Su` and `ls -lde` over the three homes in [§4](#4-who-owns-a-seatbelt-yolo-account-home).
