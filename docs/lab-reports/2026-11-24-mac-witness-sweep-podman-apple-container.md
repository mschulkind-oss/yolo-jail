---
title: "Mac witness sweep at 93608f56b: W-GB1, W-PATH1, W-TMP1, W-PH2, W-SU2 and the W-SU11/W-SU14 listings pass; `container system stop` does not leave Apple Container down, so the stop halves cannot run as written"
date: 2026-11-24
tags: [lab-report, macos, macos-user, named-setups, git-broker, pinned-host-helpers, apple-container]
summary: "At main 93608f56b on the owner's Mac, through a dev bundle built from main, with the podman machine started for the run. Passes: W-GB1 (5 of 5), W-PATH1 (10 of 10 account-mode launches ran, no stop), W-TMP1 (both halves; 3 tests pass, 1 skips on this Mac's closed log store), W-PH2, W-PH4's live half in account mode, W-SU2 in full, and the W-SU11 and W-SU14 listing, --setup and test halves. On Apple Container CLI 1.1.0, launchd shows the API servers running straight after `container system stop`, and yolo then reports complete results, so the 'runtime down' halves of W-SU11 and W-SU14 need another way to take the runtime down. Later, at the owner's request, `just install` ran (installed 0.19.0+148.g3264dd695740), and W-PH1, W-PH2 and W-PH4 (live and dry run, seatbelt-self) then passed against the installed generation. W-PH3 is still refused by the session's permission classifier."
---

# Mac witness sweep at `93608f56b`

**Run 2026-11-24 on the owner's Mac** (darwin/arm64, Apple Container CLI 1.1.0, podman machine
`podman-machine-default` on applehv, rootless with `pasta`), at main `93608f56b`.

- **Build.** Every launch used a dev bundle built from main with `just build-dev-bundle`
  (`…/dev-bundles/b39150a9b4ce6503-…/bin/yolo`, `0.19.0+147.g93608f56bd4f`). The installed
  `yolo` is still `0.19.0+61.gfe37424b3605`, because the session's permission classifier refused
  `just install`.
- **Session.** The launches ran from a Claude Code session under `yolo host`, so its `YOLO_*`
  variables were set, `YOLO_ALLOW_MISSING_PROGRAMS=1` among them.
- **Owner's say.** The owner asked for the podman machine to be started (it had been stopped for 4
  weeks) and cleared `container system stop`.
- **Owner's config.** It was edited twice and restored byte-for-byte both times, see
  [§3](#3-w-tmp1) and [§5](#5-w-su2).

Nothing in yolo was changed.

## Observed

| Witness | Result |
| :--- | :--- |
| [W-GB1](../reference/git-broker.md#a-mac-witness-still-to-run-w-gb1) | **Passes**: `true pasta`, then 5 `--- PASS: TestGitBrokerConfigBranch` and no skip |
| [W-PATH1](../design/macos-user-model-plan.md#W-PATH1) | **10 of 10 launches ran**. The 11-13 stop did not recur, see [§2](#2-w-path1) |
| [W-TMP1](../design/macos-user-model-plan.md#W-TMP1) | **Passes**, both halves. Tests: 3 pass and 1 skips, see [§3](#3-w-tmp1) |
| [W-PH2](../reference/pinned-host-helpers.md#w-ph2) | **Passes**, see [§4](#4-pinned-host-helpers) |
| [W-PH4](../reference/pinned-host-helpers.md#w-ph4), the live half | **Passes in account mode**: `exe=` lines under the bundle from `seatbelt-yolo-account` sessions, not `seatbelt-self` |
| [W-PH3](../reference/pinned-host-helpers.md#w-ph3) | **Not run**: the permission classifier refused touching the pin and killing git-broker, twice, the second time after the owner's go-ahead |
| W-PH1, W-PH2 and W-PH4 on the installed generation, after `just install` | **Pass**, see [§7](#7-after-just-install) |
| [W-SU2](../design/named-setups-plan.md#mac-checks) | **Passes in full**, see [§5](#5-w-su2) |
| W-SU11 and W-SU14, the listing, `--setup` and test halves | **Pass**, see [§6](#6-w-su11-and-w-su14) |
| W-SU11 and W-SU14, the "runtime stopped" halves | **Cannot run as written**: Apple Container does not stay down, see [§6](#6-w-su11-and-w-su14) |

### 1. W-GB1

`podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'` printed
`true pasta`. Then
`YOLO_SETUP=podman-self go test -count=5 -timeout 0 -v -run '^TestGitBrokerConfigBranch$' ./integration`
gave five `--- PASS: TestGitBrokerConfigBranch` (46.1 to 47.0 s each), no `--- SKIP:`, and `ok`.
HEAD contains `287479402`, so these runs carry step 3's separate fetch and push statuses, the
commit check and the client `git fsck`. The nightly's `fatal: bad object` did not recur.

### 2. W-PATH1

The workspace was a fresh `/Users/Shared/yolo/path1`, with `YOLO_SETUP=seatbelt-yolo-account` and
ten `yolo -- true` launches in a row.

**First attempt.** With a workspace `yolo-jail.toml` holding `[install.mise] jq = "1.7.1"`, all ten
stopped at the config-approval refusal, because a new workspace config needs a human `y` and the
launches had no terminal. Approving it with `--accept-config-changes` was refused by the session's
permission classifier, and was not retried.

**The run.** The workspace file was removed instead. The owner's user config already sets
`install.mise` (neovim), so the provisioning stage still ran; the launch printed `↳ mise install`.
All ten launches exited 0. No output contained `not on the provisioning stage's PATH`,
`Provisioning found no yolo`, `repair workspace .yolo permissions` or `command not found`.

`ls -le /Users/Shared/yolo-staged/bin` afterwards:

```text
-r-xr-xr-x@ 1 matt  wheel  55802770 Nov 24 00:59 yolo
-r-xr-xr-x@ 1 matt  wheel  13469042 Oct 19 01:44 yolo-jaild
-r-xr-xr-x@ 1 matt  wheel   6967970 Oct 19 01:44 yolo-log
-r-xr-xr-x@ 1 matt  wheel   6735858 Oct 19 01:44 yolo-ps
-r-xr-xr-x@ 1 matt  wheel   7108162 Oct 19 01:44 yolo-serial
```

There are no ACL entries and no `session-*` directories. So the run does not say which of the
witness's two causes the 11-13 stop had, because it never stopped. It differs from 11-13 in one
way: those launches had a workspace `mise_tools` entry, and these had none.

### 3. W-TMP1

In `/Users/Shared/yolo/tmp1`, with `YOLO_SETUP=seatbelt-yolo-account` and through the dev bundle
(not the installed build the witness names), with no `macos_user_shared_tmp` key:

```text
1: /private/var/folders/mr/t1nfdfgj1mn79jn_q0r6bpj40000kt/T
2: OK
touch: /tmp/x: Operation not permitted
4: /private/var/folders/mr/t1nfdfgj1mn79jn_q0r6bpj40000kt/T /private/var/folders/mr/t1nfdfgj1mn79jn_q0r6bpj40000kt/T/zsh
```

`grep -nF` for the closed-`/tmp` line in `~/.claude/skills/jail-mechanics/topics/limits.md`
exited 0, so the line is there.

**The hatch.** `yolo config set macos_user_shared_tmp true --user`, then a launch printed
*"tmp: the shared /tmp is open to the sandbox, read and write, … (macos_user_shared_tmp in your
user config; to close /tmp again, run `yolo config unset macos_user_shared_tmp --user`)"*, and
`touch /tmp/x && echo OK` inside the sandbox printed `OK`. Then:

- `yolo config unset macos_user_shared_tmp --user` exited 0, and `cmp` against a copy taken
  before the set reported the user config identical;
- `sudo -n --user=_yolojail /usr/bin/env /bin/rm -f /tmp/x` removed the file.

**The tests**, with `YOLO_TEST_MACOS_USER=1`:

- `TestMacosUserSerialClientDrivesAHostPtyFromTheSandbox`,
  `TestMacosUserCopiesAPackFileMountIntoTheContextDir` and `TestMacosUserHostFloorIsTheHostUsersAlone`
  pass.
- `TestMacosUserMacosLogBridgeScopesToTheSandbox` skips: *"this Mac's unified log store is closed
  to you: `log show` as the invoking user was refused (log exited 77: … Operation not
  permitted)"*.

### 4. Pinned host helpers

With one `podman-self` jail and one `apple-container-self` jail running in
`/Users/Shared/yolo/su1124`, `ps -axo pid,args` showed 10 `internal daemon` processes:

- `aws-auth`, `claude-oauth-broker`, `git-broker`, `github-broker`, `host-processes` and `serial`,
  one each;
- `jail-keeper` and `openai-auth-broker`, two each.

Every one started with the dev bundle's `share/yolo-jail/bin/darwin-arm64/yolo`.

- The git-broker start line for the Podman jail reads `helper start: name=git-broker
  build=0.19.0+147.g93608f56bd4f commit=93608f56bd4f … exe=…/bin/darwin-arm64/yolo`.
- `podman exec … printenv YOLO_VERSION` printed `0.19.0+147.g93608f56bd4f`, the same build.
- The keeper log's first line (`jail-keeper-yolo-su1124-e5b10891-podman-self.log`) is its
  `helper start: name=jail-keeper …` line.

So W-PH2 passes, against the dev bundle's pin rather than the installed generation's.

The git-broker start lines from the `seatbelt-yolo-account` sessions in [§3](#3-w-tmp1) also carry
`exe=` under the same bundle. That covers W-PH4's live half for account mode; a `seatbelt-self`
session's start lines were not read. W-PH3 did not run, because the permission classifier refused
it.

### 5. W-SU2

| Case | Result |
| :--- | :--- |
| A project `[network] mode = "host"`, then `yolo --setup apple-container-self -- true` | Refuses: *"…yolo-jail.toml:2:8: network.mode = "host": setup apple-container-self always runs bridged…"*, then `yolo --setup podman-self -- claude` and `yolo config unset network.mode --project`. The refusal comes before config approval |
| The same, with `YOLO_SETUP=apple-container-self yolo check --no-build` | The `✗ [FAIL]` line once |
| A project `mode = "bridge"`, then `yolo --setup seatbelt-self -- true` | Refuses: *"…setup seatbelt-self always shares the Mac's network, so the agent cannot have a loopback of its own…"*, then the same `unset … --project` |
| The same, with `yolo check --no-build` | The `✗ [FAIL]` line once, plus its `unset` line |
| No `mode`, then `yolo --setup apple-container-self --network host -- true` | Refuses, naming `--network host` and *"launch without --network host"*, with no `unset` |
| No `mode`, then `yolo --setup seatbelt-self -- true` and `yolo --setup apple-container-self -- true` | Both exit 0 |
| `mode = "host"` in the user config (`yolo config set network.mode '"host"' --user`) | The Apple Container refusal cites `~/.config/yolo-jail/config.toml:69:8` and ends `yolo config unset network.mode --user`. The unset restored the file byte-for-byte |
| `YOLO_TEST_APPLE_CONTAINER=1 … -run '^TestAppleContainerExplicitHostModeIsRefused$' ./integration` | `--- PASS`. Its port half printed *"AC-PARITY #10 VERDICT: HOLDS — a published port answers the Mac under the default network mode"* |

### 6. W-SU11 and W-SU14

**With both jails running:**

- `yolo ps` listed `su1124 apple-container-self` and `su1124 podman-self`, and
  `--format json` had `"complete": true`.
- `yolo ps --setup apple-container-self` listed only the Apple Container row, with no line
  naming Podman.
- `container inspect … | .[0].status` is an object with `networks`, `startedDate`
  (`"2026-11-24T06:10:46Z"`) and `state` (`"running"`), as measured on 11-06.
- `yolo stores` printed `Sources: Podman, Apple Container, Seatbelt, yolo host`, both
  `container image store` headings, and an `su1124/.yolo/durable` row.
- `yolo stores --format json` had `"schema": "yolo.stores/1"`, `"complete": true`, no `runtime`
  key, and `confinement` on every image row (`images.yolo` is `podman`,
  `images.yolo@apple-container` is `apple-container`, and so on).

**After `container system stop`**, which exited 0 and stopped `yolo-ac-builder` and the `su1124` AC
jail:

- `yolo ps` listed only the Podman row, with no line naming `container system start`, and
  `"complete": true`;
- `ls ~/.local/share/yolo-jail/containers/` no longer held the AC jail's record;
- `yolo stores` still listed `Apple Container` among its sources, with its sections, and
  `"complete": true`.

That is the opposite of what both witnesses expect, but the cause is the runtime, not yolo:

- `container system status` straight afterwards printed `status running`.
- A second `container system stop`, with no client call in between, was followed at once by
  `launchctl list` showing `com.apple.container.apiserver`,
  `com.apple.container.container-core-images`, `…container-network-vmnet.default` and
  `…machine-apiserver` with live PIDs.

So on this Mac the stop ends the containers, but launchd has the API servers back (or never stops
them) by the time anything can look. yolo then gets real answers, so `complete: true` and the
missing AC row are true. The jail had ended, exit 137 for the held `sleep`, and its keeper tore it
down, which is also why its record is gone.

**Afterwards**, `yolo stores --setup apple-container-self` printed `Sources: Apple Container`, with
no Podman mention. `go test -count=1 -run 'TestMacosUserPs|TestMacosUserStores' ./integration`
passed both tests (`TestMacosUserPsListsItsLiveSession` and
`TestMacosUserStoresListsTheSandboxAccount`).

**Side effect:** after the stop, `container ls --all` lists nothing. `yolo-ac-builder` is gone,
not just stopped, so the next Apple Container image build may have to recreate it.

### 7. After `just install`

The owner then asked for `just install`, and it ran: *"Replaced the deployed yolo launcher
v0.19.0-61-gfe37424b3605."* `yolo --version --full` now prints `0.19.0+148.g3264dd695740`, commit
`3264dd695740`, and `flake-bundle` points at `flake-bundles/20261124T152840Z-56088`.

- **[W-PH1](../reference/pinned-host-helpers.md#w-ph1)** passes: `bin/darwin-arm64/` lists `yolo`,
  `yolo-jaild`, `yolo-log`, `yolo-ps` and `yolo-serial`, and `cmp` against `command -v yolo` prints
  `same`.
- **[W-PH2](../reference/pinned-host-helpers.md#w-ph2)** passes, with a `podman-self` jail in
  `su1124` launched by the installed `yolo`:
  - all 8 `internal daemon` processes start with
    `…/flake-bundles/20261124T152840Z-56088/bin/darwin-arm64/yolo`;
  - the git-broker start line has `build=0.19.0+148.g3264dd695740` and `exe=` that same file;
  - `podman exec … printenv YOLO_VERSION` prints `0.19.0+148.g3264dd695740`;
  - the keeper log's first line is `helper start: name=jail-keeper build=0.19.0+148.g3264dd695740`.
- **[W-PH4](../reference/pinned-host-helpers.md#w-ph4)** passes. A `yolo --setup seatbelt-self --
  true` session in `su1124` exited 0, and its nine `helper start` lines all carry
  `build=0.19.0+148.g3264dd695740` and `exe=…/flake-bundles/20261124T152840Z-56088/bin/darwin-arm64/yolo`.
  They are `aws-auth`, `claude-oauth-broker`, `git-broker`, `github-broker`, `host-processes`,
  `jail-keeper`, `openai-auth-adapter`, `openai-auth-broker` and `serial`.
  `yolo run --setup seatbelt-self --dry-run` exits 0 with `✓ all plan invariants hold`.
- **[W-PH3](../reference/pinned-host-helpers.md#w-ph3)** was refused by the classifier again. The
  `podman-self` jail was left running for the owner to run it by hand.

## Fixed and committed

Nothing in yolo. This report is the only commit.

## Open

1. [§6](#6-w-su11-and-w-su14): W-SU11 and W-SU14's "runtime stopped" halves need a way to take
   Apple Container down that sticks on CLI 1.1.0, or need rewording to what `container system
   stop` actually does here (it stops the containers, and the services stay up).
2. [§2](#2-w-path1): W-PATH1 did not reproduce in 10 launches without a workspace `mise_tools`
   entry. Decide whether that settles it, or whether it should run again with a workspace config,
   which needs a human to approve it first.
3. [§7](#7-after-just-install): W-PH3 still needs a run, by the owner by hand or with a permission
   rule that lets the agent touch the pin and stop git-broker.
4. [§6](#6-w-su11-and-w-su14): check that the next Apple Container build recreates
   `yolo-ac-builder`.

## Evidence

In `/Users/Shared/yolo/evidence/2026-11-24/`: `w-gb1-step1.txt`, `w-gb1.log`,
`build-dev-bundle.log`, `path1-try1/`, `path1-launch-1.log` to `path1-launch-10.log`,
`path1-summary.txt`, `path1-ls-le.txt`, `tmp1-closed.log`, `tmp1-hatch*.log`, `tmp1-tests.log`,
`w-ph2-ps.txt`, `su-podman-jail.log`, `su-ac-jail.log`, `su11-ps-up.json`, `su11-ps-ac.txt`,
`su11-ps-down.txt`, `su14-stores-up.txt`, `su14-stores-up.json`, `su14-stores-down.txt`,
`su14-stores-ac.txt`, `ac-stop.log`, `ac-status-down.txt`, `ac-autostart.txt`, `su-tests.log`,
`su2-*.log`, `su2-test.log`, `just-install.log`, `ph-installed.txt`, `w-ph4-live.log`, `w-ph4-live-lines.txt` and `w-ph4-dryrun-installed.log`. Keeper and broker logs are under `~/.local/share/yolo-jail/logs/`.
Everything ran against `93608f56b`, except [§7](#7-after-just-install), which ran against the installed `3264dd695`.
