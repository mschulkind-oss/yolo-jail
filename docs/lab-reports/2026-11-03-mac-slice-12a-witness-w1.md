---
title: "Mac run: slice 12a's witness W1, the macos-user suite, and the short suite on darwin"
date: 2026-11-03
tags: [lab-report, macos, macos-user, testing]
summary: "Two account-mode workspaces run at once on the 12a build, but nine macos-user integration tests that pass on 12a's parent fail on it (five not predicted by W1), $TMPDIR is empty in the sandbox, nix warns on every call, and four short-suite tests fail on macOS 26.5 because kern.procargs2 hides a platform binary's environment."
---

# Mac run: slice 12a's witness W1, the macos-user suite, and the short suite on darwin

**Run 2026-11-03 on the maintainer's Mac** (macOS 26.5 build 25F71, arm64, SIP enabled). The
source is `main` at `90ff72f2c`, run as the development bundle `just build-dev-bundle` emitted
(`yolo-jail 0.16.0+60.g90ff72f2cc08`). The deployed `yolo` was older (`0.16.0+30.g5342558ff`)
and was not used. This session debugged and documented only: nothing here is fixed.

**Terms.**

- *Slice 12a* is the slice of [the macos-user model plan](../design/macos-user-model-plan.md)
  that landed as `610d94b42` ("two workspaces at once in account mode"): account mode now runs
  each workspace with `HOME = <ws>/.yolo/home` ([MMP-D25](../design/macos-user-model-plan.md#MMP-D25)).
- *Account mode* is macos-user's default, which runs the agent as the dedicated `_yolojail`
  account through `sudo -u` ([macOS backends](../../userguide/guides/macos.md)).
- *W1* is the plan's Mac witness that gates 12a's landing
  ([Mac checks](../design/macos-user-model-plan.md#mac-checks)). 12a is already on `main`, so
  this run is W1 after the fact.
- A *platform binary* is one Apple ships signed as part of the OS (`/bin/sleep`, `/bin/sh`,
  `/usr/bin/sandbox-exec`), as opposed to a nix-store or Homebrew build.

## Results

| # | Finding | Kind |
| :--- | :--- | :--- |
| 1 | Two account-mode workspaces run at once, each with its own `$HOME` | W1 passes ([§1](#1-w1-two-workspaces-at-once-passes-its-core)) |
| 2 | `$TMPDIR` is empty in the sandbox and `getconf DARWIN_USER_TEMP_DIR` prints nothing | W1 line fails ([§2](#2-tmpdir-is-empty-in-account-mode)) |
| 3 | Nine `TestMacosUser*` tests fail on 12a and pass on its parent; five were not predicted | Regression or stale test, per test ([§3](#3-the-macos-user-suite-nine-failures-all-from-12a)) |
| 4 | Every `nix` call in account mode warns that `$HOME` is not owned by the user | Product-visible ([§3](#3-the-macos-user-suite-nine-failures-all-from-12a)) |
| 5 | The briefing still says the home is one account home shared by every workspace | Stale text ([§4](#4-the-briefing-still-describes-the-shared-account-home)) |
| 6 | A workspace once launched on Apple Container refuses a later account-mode launch | Backend collision ([§5](#5-an-apple-container-home-blocks-a-later-account-mode-launch)) |
| 7 | `kern.procargs2` hides a platform binary's environment; four short-suite tests fail | Defect in tests, possibly in production ([§6](#6-kernprocargs2-hides-a-platform-binarys-environment)) |
| 8 | `run-checks.sh` g4 fails when a launch runs at the same time | Harness hazard ([§7](#7-run-checkssh-g4-and-a-concurrent-launch)) |
| 9 | `claude -p` fails in both workspaces: "OAuth token revoked" | Blocked on the owner ([§8](#8-blocked-and-not-run)) |

## 1. W1: two workspaces at once passes its core

Two fresh workspaces, `/Users/Shared/yolo/w1-c` (A) and `/Users/Shared/yolo/w1-d` (B). With no
`macos_user_mode` key, `YOLO_RUNTIME=macos-user` and `YOLO_ALLOW_MISSING_PROGRAMS=1` ([§8](#8-blocked-and-not-run) says
why), A ran `bash probe.sh 400` (it prints, sleeps 400 s, prints again), and B ran while A slept.

- **B was not refused.** A was still running when B started and when B exited 0.
- `$HOME` was `/Users/Shared/yolo/w1-c/.yolo/home` in A and `/Users/Shared/yolo/w1-d/.yolo/home`
  in B. `id -un` was `_yolojail` in both.
- **No link under `/Users/_yolojail` points into either workspace** (`find /Users/_yolojail
  -maxdepth 3 -type l` and `readlink`, grep `w1-`: no match). The pre-12a links into
  `/Users/Shared/yolo/issue66` (`.claude`, `.config`, `.local`, `.npm-global`, `.yolo/bin`, …)
  are unchanged, as [OQ-MM4](../design/macos-user-model.md#OQ-MM4) expects.
- In A, `ls -a ~/.claude` was identical before B started and after B exited.
- `ls ~/.config/yolo-agent-env` lists `provider-keys`.
- The briefing's **Home** line names the workspace home (see [§4](#4-the-briefing-still-describes-the-shared-account-home) for the rest of the line).
- `TestMacosUserTwoWorkspacesRunAtOnceAsTheAccount` **passes** (12.9 s).
- `./run-checks.sh --mode account --account _yolojail --profile-from-yolo <dev yolo>`, run
  alone: **41 PASS, 0 FAIL, 4 SKIP** (b6 no `SSH_AUTH_SOCK`, g8 no colima/Docker socket, e1
  self mode only, f1 needs `--with-claude`). A first run beside a launch failed g4 ([§7](#7-run-checkssh-g4-and-a-concurrent-launch)).

**Not checked:** "a workspace used before 12a keeps its undotted `claude` and `config` unused".
Both W1 workspaces were fresh. `log show --predicate 'sender == "Sandbox"'` printed
`log store: Operation not permitted` from this session, so the Sandbox-denial line is unchecked.

**Decision needed:** whether W1 counts as passed with [§2](#2-tmpdir-is-empty-in-account-mode), [§3](#3-the-macos-user-suite-nine-failures-all-from-12a) and the two unchecked lines open.

## 2. `$TMPDIR` is empty in account mode

W1 expects `echo $TMPDIR` to name the account's own `/var/folders/…/T/`
([MMP-D29](../design/macos-user-model-plan.md#MMP-D29)). Observed in both workspaces:

```
W1MARK TMPDIR=
W1MARK getconf=                      # getconf DARWIN_USER_TEMP_DIR, inside the sandbox
touch: /x: Operation not permitted   # touch "$TMPDIR/x", with $TMPDIR empty
```

The directory itself is granted. On the host,
`sudo -n --user=_yolojail /usr/bin/env /usr/bin/getconf DARWIN_USER_TEMP_DIR` prints
`/var/folders/mr/t1nfdfgj1mn79jn_q0r6bpj40000kt/T/` (rc 0), and inside B,
`touch /private/var/folders/mr/t1nfdfgj1mn79jn_q0r6bpj40000kt/T/w1x-$$` succeeded. `touch /tmp/x`
succeeded too, as MMP-D29's shared `/tmp` means it should. So the profile opens the directory, but
no tool is told about it: MMP-D29 says the account mode "keeps the TMPDIR its user's env layers
give it", and after the `sudo` crossing those layers give none. Inside the sandbox `getconf` cannot
find the directory either. A tool that reads `$TMPDIR` falls back to `/tmp`.

**Decision needed:** either set `TMPDIR` to the directory the plan already resolves, or change
W1's expectation to "the shared `/tmp`". This is the MMP-D29 ruling the plan's `next:` line asks for.

## 3. The macos-user suite: nine failures, all from 12a

`YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^TestMacosUser' ./integration` at
`90ff72f2c`: **153 PASS, 9 FAIL, 4 SKIP**, 837 s. To find out which failures 12a caused, the same
nine tests ran on 12a's parent (`467d785d9`, a detached worktree, with
`YOLO_TEST_IMAGE_SKEW=warn` because that parent predates today's `flake.lock` bump and no
macos-user test reads the container image). **All nine pass on the parent.**

Four are the stale premises W1 predicted, to be rewritten from what the run shows:

| Test | File | What it still assumes |
| :--- | :--- | :--- |
| `TestMacosUserHomeTierIsPerWorkspace` | `macosuserhometier_test.go:86` | `~/.local`, `~/.npm-global`, `~/go`, `~/.yolo/bin` are links in the account home |
| `TestMacosUserLayoutRefusesAnOccupiedSidecarMirror` | `macosuserlayoutrefusal_test.go:114` | the refusal's `rm -rf` names `.yolo/home` twice and not `.yolo/home/.claude-shared-credentials`, the path it refused over |
| `TestMacosUserHomeRootHostFilesArePerWorkspace` | `macosuserhomeroothostfiles_test.go:96` | reads the undotted `.yolo/home/config/yolo-home/…` |
| `TestMacosUserDeclaredToolsArrive` | `macosusertools_test.go:116`, `:133`, `:165` | the mise store at `/Users/_yolojail/.yolo/mise/installs`; the TIERS probe lines for `~/.yolo/bin` and `~/.npm-global` |

W1 also named `macosusercontent_test.go` and `macosuserseatbelt_test.go`. Both passed.

Five were **not predicted** by W1:

| Test | Message (abridged) | Reading |
| :--- | :--- | :--- |
| `TestMacosUserNixIsTheHostDaemonClientWithNoFlags` (`macosusernix_test.go:113`, `:127`) | `warning: $HOME ('<ws>/.yolo/home') is not owned by you, falling back to the one defined in the 'passwd' file ('/Users/_yolojail')` | **Product-visible.** `<ws>/.yolo/home` is `matt:_yolojail` and the sandbox runs as `_yolojail`, so every `nix` call warns and nix reads its user config from the account home. |
| `TestMacosUserCapturesConfigEditsWhenTheSessionEnds` (`macosuserconfigcapture_test.go:43`) | the session's edit to `$HOME/.claude/settings.json` is not in `<ws>/.yolo/prism/claude-settings.overlay.json`, which is `{}` | Likely a real regression: the edit is made at the new `$HOME`, and the end-of-session capture does not see it. Root cause not looked for. |
| `TestMacosUserFloorReachesTheSandboxPath` (`macosuserfloor_test.go:74`) | `node`, `npm`, `jq` resolve to `/Users/_yolojail/.yolo/mise/shims/…` instead of the nix profile | Either a test premise (it removes "the sandbox home's directories" from the lookup, and the account home is no longer `$HOME`) or account-home mise shims still on `PATH`. Unsettled. |
| `TestMacosUserUnchangedRelaunchKeepsTheDeliveredFiles` (`macosuseroverlayreuse_test.go:41`, `:45`) | an unchanged relaunch replaced `.yolo/home/.claude/skills/configuring-the-jail/SKILL.md` (new inode); `linkat` from the staging root was refused | The overlay re-delivers on every launch at the new home. Unsettled whether the reuse check or the test is wrong. |
| `TestMacosUserOverlayInstallKeepsTheAgentStateBesideItsSkills` (`macosuseroverlaystate_test.go:84`) | layout `NOT-A-SYMLINK` after the second launch, want `SYMLINK` (`~/.oh-omp/agent`) | Probably the links-in-the-account-home premise again; the test reads it as G36, the overlay replacing a directory above its destinations. Unsettled. |

**Decision needed:** for each of the five, whether it's a test to rewrite or a 12a defect. The
nix warning is visible to users whatever the answer.

## 4. The briefing still describes the shared account home

In the briefing of `w1-c` (`.yolo/home/.claude/CLAUDE.md`):

```
- **Home**: `/Users/Shared/yolo/w1-c/.yolo/home` (one account home, shared by every workspace on this
  machine; its per-workspace directories are links into this workspace's `.yolo/home`)
```

and its opening says "Your home is a real account's home … The account is shared by every
workspace on this machine; the state directories your packs declare at `scope: workspace` are
linked into THIS workspace's own sidecar, and anything else you write in the home is not yours
alone." Both come from `internal/jailcontent/briefing.go` (`:439` and `:743`) and describe the
pre-12a geometry. After 12a the home *is* the workspace's sidecar, so its per-workspace directories
are not links into it any more. What it does still link is machine-wide state: in `w1-c` those are
`.claude-shared-credentials`, `.pi-shared-credentials`, `.cache`, `.bashrc`, `.gitconfig`,
`.claude.json` and `ctx`. The path is right but the text around it is wrong.

**Decision needed:** the account-mode wording. The self mode's wording should be checked too.

## 5. An Apple Container home blocks a later account-mode launch

The user config on this Mac sets no runtime. The first launch in `/Users/Shared/yolo/w1-a`, made
without `YOLO_RUNTIME`, ran in a Linux container (mise reported `linux-arm64`, the home was
`/home/agent`, and the launch log names Apple Container throughout). It was stopped
part-way, after writing `<ws>/.yolo/home/.config/yolo-jail/config.toml` (dotted, the Apple Container
home layout). The next launch of the same workspace with `YOLO_RUNTIME=macos-user` refused:

```
refusing macos-user inherited preflight scope: existing user file
/Users/Shared/yolo/w1-a/.yolo/home/.config/yolo-jail/config.toml is in the way, and the
home-overlay installer cannot prove it is a file it delivered before, so installing the user
config there could overwrite it. Review it, preserving its contents and mode, then move it
aside (mv …
```

Before 12a the account mode's `$HOME` was `/Users/_yolojail`, so the two backends never shared a
home directory. Now both use `<ws>/.yolo/home`. The refusal does name a next step. Left in place
for inspection: `/Users/Shared/yolo/w1-a`.

**Decision needed:** whether switching one workspace between Apple Container and account mode is
supported. If it is, the overlay installer needs to recognise a file the other backend delivered.

## 6. `kern.procargs2` hides a platform binary's environment

`go test -short ./...` (the `just test-fast` package set, without its `-timeout=20m`) failed four tests in `integration`, and they
fail the same way when run alone:

- `TestStopHomeProcessesStopsEveryoneUnderTheHome`, `…WaitsForAScratchRemover`,
  `…StopsWhatIsSpawnedDuringTheStop` (`preparationworkers_test.go:1346`, `:1390`, `:3759`): "the
  stand-in sh … never showed HOME=…"
- `TestProcessEnvironReadsALiveChildsHome` (`procinfo_darwin_test.go:236`): "the child never
  showed HOME=…: environment []"

Measured with `unix.SysctlRaw("kern.procargs2", pid)` from a small Go program, as the same user:

| Child | Bytes returned | Environment |
| :--- | :--- | :--- |
| `/bin/sleep 20` (platform binary) | 29–34 | none: `"\x02\x00\x00\x00/bin/sleep\x00…sleep\x0030\x00"` |
| `/bin/sh -c 'sleep 20'` | 29 | none |
| `/nix/store/…-coreutils-9.11/bin/sleep 20` | 10252 | full |
| the test process itself | 10460 | full |

`ps -E` shows the same thing: no environment for `/bin/sleep` and the full one for the nix
`sleep`. So on macOS 26.5 the kernel withholds a platform binary's environment even from its
owner. The tests' stand-ins are `sleep` and `sh` from `/bin`, so the HOME match can never succeed
on this Mac. CI's macOS runner may run a different macOS version.

**Possible production impact, not measured.** `internal/macosuser/guestprocs_darwin.go:70` reads
the same sysctl in run-as-you mode to find the sandbox's processes by an environment marker.
`parseProcArgs2` returns an empty environment as *known* (`EnvKnown = true`), so `marked`
(`guestprocs.go:73`) reports a platform-binary process as unmarked rather than falling back to
"alive". When the marked process at the top of a session (a shell, `sandbox-exec`) is a platform
binary, it and every descendant would be classified as outside the sandbox. `internal/prune`
(`procstart_darwin.go`) reads only the exec path from this sysctl, which this hiding does not touch.

**Decision needed:** use a non-platform stand-in in the tests (or skip where the sysctl hides the
environment), and decide whether an empty environment from a platform binary should count as
unknown in `guestprocs`.

## 7. `run-checks.sh` g4 and a concurrent launch

The first `run-checks.sh --mode account` run, made while a W1 launch was running, failed g4 ("read
this session's endpoint file"): `cat …/demo.endpoint: No such file or directory`, with the
no-profile control also failing. Run alone, g4 passed. The script makes two fake
`/private/tmp/yolo-host-services-<hex>-rc<nonce>` dirs with a `.session.lock` it never holds.
`collectDeadServicesSessions` (`internal/cli/run/servicessession.go:178`) removes *every*
workspace's session dir whose lock is not held, so any launch made during the script deletes
them. This is a hazard in the harness, not a profile defect.

**Decision needed:** have the script hold its fake lock, or document "run it alone".

## 8. Blocked and not run

- **`claude -p 'Reply with the single word: ok'`** (a W1 line) ran in both workspaces and failed:
  `Failed to authenticate: OAuth token revoked. Please log in again or contact your
  administrator.` That is the shared Claude login on this Mac, not 12a's layout. Rerun after the
  owner signs in again.
- **The user config's pi fork and extensions** would not build on Apple Container:
  `left it pending because Apple Container has running yolo jail(s): yolo-ac-builder`. That
  container is the long-lived builder from `ghcr.io/mschulkind-oss/yolo-jail-builder:latest`, up
  since 2026-10-09, and `appleContainerBuildDecision` (`internal/cli/applebuildcapability.go`)
  counts it as a live jail. This is why every launch here set `YOLO_ALLOW_MISSING_PROGRAMS=1`. Not
  investigated further.
- **The first macos-user launch built nix's darwin tool floor from source.** The fork build log
  showed `mise` crates compiling under `/nix/var/nix/builds/…` for about 15 minutes, so that
  derivation missed the binary cache. Today's `flake.lock` bump (`7009671da`) is the likely cause.
  Not checked.
- **Not run:** W2 and W9 (both need `macos_user_mode = "self"` in the user config, which this
  session did not change), W3 (slice 12c is not built), and the keychain-session witnesses R1–R6
  and H1 in [`macos-keychain-session.md`](../research/macos-keychain-session.md) (they need test
  accounts, the owner's admin password, or a deliberate lock-all).

## 9. The short suite on this Mac

`go test -short ./...` at `90ff72f2c`, run while the dev bundle built and a launch ran:

- `integration`: the four failures in [§6](#6-kernprocargs2-hides-a-platform-binarys-environment).
- `internal/brokerhook`: `TestAHookIsKilledWhenItsRequestLeavesPending/timed_out/shell_waiting` and
  `/shell_exited` ("timed out waiting for the hook's pid file"). **Passes alone** (19 s), so this
  is a flake under load.
- `internal/cli`, `internal/cli/run`, `internal/entrypoint`: hit the default 10-minute `go test`
  timeout under that load. Rerun with `-timeout 25m` while the `^TestMacosUser` suite and another
  session's `integration.test` ran, **all three pass**: `internal/cli` 1359 s, `internal/cli/run`
  619 s, `internal/entrypoint` 766 s. `just test-fast` passes `-timeout=20m`, and `internal/cli`'s
  1359 s (22.6 min) is over that too, so under this load `just test-fast` on this Mac would fail on
  time, not on a test. Not measured on an idle machine.

## Evidence

All at `90ff72f2c` unless named. The logs were kept under `/tmp/macnotes/` on the Mac and are not
committed.

```
just build-dev-bundle                       # → dev-bundles/133933f3369e5824-…/bin/yolo
cd /Users/Shared/yolo/w1-c && YOLO_RUNTIME=macos-user YOLO_ALLOW_MISSING_PROGRAMS=1 <dev yolo> -- bash probe.sh 400 &
cd /Users/Shared/yolo/w1-d && YOLO_RUNTIME=macos-user YOLO_ALLOW_MISSING_PROGRAMS=1 <dev yolo> -- bash probe.sh 1
YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^TestMacosUserTwoWorkspacesRunAtOnceAsTheAccount$' ./integration   # PASS
YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^TestMacosUser' ./integration                                       # 153/9/4
git worktree add --detach <tmp> 610d94b42^ && YOLO_TEST_IMAGE_SKEW=warn YOLO_TEST_MACOS_USER=1 go test … -run '^TestMacosUser(<the nine>)$' ./integration  # 9 PASS
docs/plans/runbooks/macos-user-model-prototype/run-checks.sh --mode account --account _yolojail --profile-from-yolo <dev yolo>  # 41/0/4 alone
go test -short ./...                        # bare: the default 10 min timeout, not just test-fast's 20m
```
