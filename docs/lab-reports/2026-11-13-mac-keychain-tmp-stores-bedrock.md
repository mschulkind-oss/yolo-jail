---
title: "Mac run of the 11-11 to 11-13 checks: Keychain W-KH1 to W-KH3, W-TMP1, W-SU14 and the W-BK2 follow-up; account-mode launches intermittently lose yolo from PATH"
date: 2026-11-13
tags: [lab-report, macos, macos-user, keychain, named-setups, bedrock-key, approvals]
summary: "At main b1d255ded, W-KH2, W-KH3, the W-BK2 follow-up and W-SU14's listing pass. W-KH1 stops at its own precondition (no Slack sign-in). W-TMP1's closed /tmp holds, but `yolo config unset macos_user_shared_tmp --user` is refused, and 3 of 5 account-mode launches in a fresh workspace stopped with `yolo: command not found` in provisioning. TestMacosUserStoresListsTheSandboxAccount fails on a row 11f removed. The capture runner is still blocked on a locked screen."
---

# Mac run of the 11-11 to 11-13 checks

**Run 2026-11-13 on the owner's Mac** (macOS 26.5, build 25F71, arm64; Apple Container CLI 1.1.0;
podman machine `applehv`, rootless with `pasta`), at main `b1d255ded`. Every yolo launch used a dev
bundle at that commit (`f5b6478adfa82ed5…/bin/yolo`, `0.17.1+492.gb1d255ded99d`), not a fresh
`just install`: none of these checks depends on the installed launcher, and the installed `yolo`
is still `0.17.1+197.g5a69dd0b4075`. Every launch set `YOLO_ALLOW_MISSING_PROGRAMS=1`. Nothing in
yolo was fixed.

The checks:

- [W-KH1 to W-KH3](../design/claude-keychain-at-host.md#mac-checks);
- [W-TMP1](../design/macos-user-model-plan.md#W-TMP1);
- [W-SU14](../design/named-setups-plan.md#mac-checks);
- the [W-BK2 follow-up](../design/bedrock-key-provider-plan.md#mac-checks);
- the [Mac capture runner](../design/approval-experience/mac-capture-runner.md#4-mac-checks), re-checked only for its blockers.

## Observed

| Check | Result |
| :--- | :--- |
| **Account-mode launches** | **Intermittently stop before the agent**: `yolo: command not found` in provisioning, see [§3](#3-account-mode-provisioning-loses-yolo-from-path) |
| Capture runner M-1 to M-13 | **Cannot run yet**: still locked, still no Screen Recording, and the owner's hook is unchanged, see [§6](#6-the-capture-runner) |
| W-KH1, the Keychain item | **Stops at its precondition**: the item holds no `mcpOAuth`, so host Claude is not signed in to Slack, see [§1](#1-w-kh1-to-w-kh3) |
| W-KH2, Claude's lock around a Keychain write | **Passes** on 2.1.325 |
| W-KH3, `security -i`'s stdin cap | **Measured**: the command line is cut at 4,095 characters, and the cut value is stored without error |
| W-TMP1, the closed `/tmp` | **Passes**, except that the closed-`/tmp` line is in the jail-mechanics topic file, not `CLAUDE.md`, see [§2](#2-w-tmp1) |
| W-TMP1, the hatch | **`tmp:` line passes**. The in-sandbox `touch` did not run, because of [§3](#3-account-mode-provisioning-loses-yolo-from-path). **`yolo config unset macos_user_shared_tmp --user` is refused** |
| W-TMP1's tests | Not run |
| W-BK2 follow-up | **Passes**, see [§4](#4-w-bk2-follow-up) |
| W-SU14, the listing and `--setup` | **Passes**, see [§5](#5-w-su14) |
| W-SU14, `container system stop` | **Not run**: it would also stop a 35-day-old Apple Container jail of the owner's |
| W-SU14, `TestMacosUserStores` | **Fails**: `TestMacosUserStoresListsTheSandboxAccount` wants an `images.macos-user` row that 11f removed |

### 1. W-KH1 to W-KH3

**W-KH1.**

- `security find-generic-password -s 'Claude Code-credentials'` printed `"acct"<blob>="matt"` and
  `"svce"<blob>="Claude Code-credentials"`, with no dialog.
- The item is **560 bytes**.
- Its top-level keys are `['claudeAiOauth']`, and it has no `mcpOAuth` (`[]`).

So host Claude is not signed in to Slack, and the witness stops here as it says to. That sign-in
is the owner's to do. No value was printed.

**W-KH2.** Claude is `2.1.325 (Claude Code)`. The `.storage-write` context in the real binary,
`…/host-floor/programs/claude/<gen>/home/.local/share/claude/versions/2.1.325`, reads:

```text
Be.getStore())return e();let r=er,n;er=new Promise((a)=>{n=a});try{if(await r,K())return await Be.run(!0,e);let a=H3();await se().mkdir(a);let s=await hc(En(a,".storage-write"),{realpath:!1,retries:{retries:10,minTimeout:100,maxTimeout:1000},stale:15000,onCompromised:(l)=>t(`[sec
```

So the `if(await r,K())return await` guard is there, just before the lock call. The credentials
variable is now `Gje` (2.1.323's `hzr`), and the storage chunk begins
`import{F,K}from"/$bunfs/root/chunk-m8nhymg4.js"`. A Linux agent still has to confirm that this
`K` is the diskless-launch check.

**Running W-KH2's commands as written does not work here:**

- `command -v claude` is yolo's host wrapper, `~/.local/share/yolo-jail/bin/wrap/claude`. Its
  `claude --version` ran a whole `yolo host` launch: a fork build, and a failed `board` build.
- `readlink -f` of the wrapper is a shell script.
- The `grep` on `PATH` is ugrep, which rejects the pattern (*"exceeds complexity limits"*), and
  macOS `/usr/bin/grep` rejects `{0,400}` (*"maximum repetition exceeds 255"*).

I read the binary with a Python byte search instead.

**W-KH3**, on the throwaway item `yolo-kh3-probe`, deleted afterwards (a later lookup returns 44):

| n | Command length | `security -i` exit | Read-back (`wc -c`, with a newline) |
| :--- | :--- | :--- | :--- |
| 1900 | 3,870 | 0 | 1901: stored whole |
| 2100 | 4,270 | 1 | 2020: **2,019 bytes stored, silently cut** |

At n=2100, `security -i` cut the line at 4,095 characters, stored what fit, and then ran the rest of
the hex as a second command: *"security: unknown command "6161…""*. So the cap is a 4,095-character
command line, about 2,019 bytes through `-X` hex. The cut is silent: the add itself succeeds.

### 2. W-TMP1

The workspace was a fresh `/Users/Shared/yolo/tmp1`, with `YOLO_SETUP=seatbelt-yolo-account`, and
there was no `macos_user_shared_tmp` key. The first launch failed as in
[§3](#3-account-mode-provisioning-loses-yolo-from-path). A later launch ran the four commands:

```text
1: /private/var/folders/mr/t1nfdfgj1mn79jn_q0r6bpj40000kt/T
2: OK
touch: /tmp/x: Operation not permitted
4: /private/var/folders/mr/t1nfdfgj1mn79jn_q0r6bpj40000kt/T /private/var/folders/mr/t1nfdfgj1mn79jn_q0r6bpj40000kt/T/zsh
```

**The briefing line.** The line starting "The shared `/tmp` (`/private/tmp`) is closed" is **not in
`~/.claude/CLAUDE.md`**. It is in `~/.claude/skills/jail-mechanics/topics/limits.md`, and in the
same file for copilot, codex and pi. `CLAUDE.md` points at the jail-mechanics skill. The witness
predates the briefing diet.

**The hatch.**

- `yolo config set macos_user_shared_tmp true --user` wrote `+macos_user_shared_tmp = true` and
  `Written.` into the owner's dotfiles `config.toml`.
- The next launch printed *"tmp: the shared /tmp is open to the sandbox, read and write, so it can
  read files your other programs leave there (macos_user_shared_tmp in your user config; remove it
  to close /tmp again)"*.
- Both launches with the hatch on then stopped as in [§3](#3-account-mode-provisioning-loses-yolo-from-path), so `touch /tmp/x` inside did not run.

**`yolo config unset macos_user_shared_tmp --user` is refused**:

```text
cannot unset config key "macos_user_shared_tmp" in selected file "/Users/matt/.config/yolo-jail/config.toml": this key has no scalar removal policy in the selected config schema; edit the value manually in that file, then run `yolo check` in this workspace
```

So the verb the witness names, and the step the `tmp:` line asks for ("remove it"), have no working
command. I restored the file with `git checkout` in the dotfiles, which are clean again.
`sudo -n --user=_yolojail /usr/bin/env /bin/rm -f /tmp/x` exited 0, and no `/tmp/x` remains.

The four Mac-only tests W-TMP1 lists were not run.

### 3. Account-mode provisioning loses yolo from PATH

Five account-mode launches with the dev bundle, all with the same `yolo-jail.toml`:

| Workspace | Launch | Result |
| :--- | :--- | :--- |
| `tmp1` (fresh) | 1st, `/tmp` closed | **Stopped** |
| `tmp1` | 2nd, `/tmp` open | **Stopped** |
| `w7-move` | `-- true` | Ran |
| `tmp1` | after a launch by the installed `0.17.1+197` yolo, `/tmp` closed | Ran |
| `tmp1` | `/tmp` open | **Stopped** |

`tmp1` also ran under `seatbelt-self`, and under the installed yolo in account mode.

Each stop printed, right after `yolo-jail macos-user bootstrap ok`:

```text
/bin/bash: line 1: yolo: command not found
Provisioning log could not be written: /Users/Shared/yolo/tmp1/.yolo/logs/startup.log — repair workspace .yolo permissions or free disk space, then retry yolo.
```

The launch exited 1. The provisioning stage pipes its output to `yolo internal log-append`. The
planned `PATH` (`yolo run --dry-run`) finds `yolo` only in
`/Users/Shared/yolo-staged/bin/session-<bundle identity>/`, which `StageGuestSessionCommands`
stages per session. After the runs, that directory no longer exists.

The workspace's permissions were fine:

- `.yolo` and `.yolo/logs` carry `_yolojail`'s inherited allow entries;
- the disk is 75% full;
- no Sandbox denial was logged.

So the refusal's remedy, "repair workspace .yolo permissions or free disk space", names neither the
cause nor a step that helps. No sequence that reliably reproduces it was found.

### 4. W-BK2 follow-up

The owner's machine-local `config.d/overrides.toml`, which lists their real key-pair file, was
moved aside for the check and restored afterwards. In `/Users/Shared/yolo/bedrock-key`:

- **Set.** Each `yolo config set env.secrets.set.<NAME> '"not-a-real-…"' --project` showed a
  redacted diff (`AWS_ACCESS_KEY_ID = "<redacted>"`) and *"Written. The host must run `yolo config
  apply` to approve the workspace change."*.
- **Launch.** `yolo --setup seatbelt-self -p claude=bedrock -- true` exited 1 with the override
  refusal:
  - `AWS_ACCESS_KEY_ID is delivered by TOML [env.secrets].set.`, and the same for the secret;
  - *"For this inline private value, run `yolo config unset env.secrets.set.<NAME>` with its scope
    (`--user`, `--project`, or `--local`), or select the declaring included config with `--file
    <path>`"*;
  - *"`yolo config set allow.aws-auth.profile "" --user`"* as the route that withholds the pointer.
- **Unset.** Both `yolo config unset … --project` showed `-… = "<redacted>"`, then `Written.`.
- **No leak.** No output contained `not-a-real`.
- **Leftover.** The file is left holding `[env.secrets.set]` and two lines of spaces. Each unset
  adds a whitespace-only line, and the empty table stays. `yolo check --no-build` still passes
  (83 passed).

### 5. W-SU14

One `ph2` jail ran under `podman-self` and one under `apple-container-self`. `yolo ps` listed both
with their setups.

**`yolo stores`:**

- It printed `Sources: Podman, Apple Container, Seatbelt, yolo host`.
- It printed both `container image store — Podman` and `container image store — Apple Container`,
  and one `ph2/.yolo/durable` row.
- In `--format json`: `"schema": "yolo.stores/1"`, `"complete": true`, no `runtime` key, and
  `confinement` on all six image rows (`podman`, `apple-container`).

**`yolo stores --setup apple-container-self`** printed `Sources: Apple Container` and no Podman
heading.

**`go test -count=1 -run TestMacosUserStores ./integration`** failed one test: *"the container
image store is not the not-applicable row on macos-user ("", listed false)"*. The test asks for an
`images.macos-user` row in state `absent`. Under
[SUP-D57](../design/named-setups-plan.md#SUP-D57) and
[SUP-D58](../design/named-setups-plan.md#SUP-D58), a macos-user run lists each listed container
source's image rows instead, so the test reads a row that no longer exists.

### 6. The capture runner

- The session dictionary still reports `CGSSessionScreenIsLocked = 1`.
- `screencapture -x` still fails with *"could not create image from display"*.
- `~/.local/bin/yolo-approve-toast` still calls `alerter … -closeLabel Ignore`.

M-1 to M-13 stay blocked on the same three things.

## Fixed and committed

Nothing in yolo. The owner's config was changed and restored as described in [§2](#2-w-tmp1) and [§4](#4-w-bk2-follow-up).

## Open

1. [§3](#3-account-mode-provisioning-loses-yolo-from-path): account-mode provisioning sometimes
   runs without `yolo` on its `PATH`, and the stop blames permissions or disk space.
2. [§2](#2-w-tmp1): `yolo config unset macos_user_shared_tmp --user` is refused. W-TMP1's briefing
   check should read `jail-mechanics/topics/limits.md`. W-TMP1's hatch `touch` and its four tests
   are still to run.
3. [§5](#5-w-su14): `TestMacosUserStoresListsTheSandboxAccount` needs updating for 11f. W-SU14's
   `container system stop` half needs the owner's go-ahead.
4. [§1](#1-w-kh1-to-w-kh3): W-KH1 needs the owner's Slack sign-in in host Claude. W-KH2's
   commands need the real binary and a byte search.
5. `yolo config unset` leaves whitespace-only lines and an empty table.

## Evidence

In `/Users/Shared/yolo/evidence/`: `kh2-1.txt`, `kh2-2.txt`, `tmp1-*.log`, `bk2f-*.log`,
`su14-*.log`, `su14-stores.txt`, `su14-stores.json`, `su14-stores-ac.txt` and `su14-test.log`.
