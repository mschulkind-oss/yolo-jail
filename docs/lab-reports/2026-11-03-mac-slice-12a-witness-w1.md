---
title: "Mac run: slice 12a's witness W1, the macos-user suite, and the short suite on darwin"
date: 2026-11-03
tags: [lab-report, macos, macos-user, testing]
summary: "Two account-mode workspaces run at once on the 12a build, but nine macos-user integration tests that pass on 12a's parent fail on it (five not predicted by W1), $TMPDIR is empty in the sandbox, nix warns on every call, and four short-suite tests fail on macOS 26.5 because kern.procargs2 hides a platform binary's environment. A later rerun (section 10) confirms the fixes for W1b, with the relaunch copy, a self-mode copy, the procargs2 skip's sleep gap and one load flake left."
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

**`just done` after this report's commit** (`90862d6ef`; no verified ancestor, so the full
`just check-ci`, 1216 s) failed. It repeated the four [§6](#6-kernprocargs2-hides-a-platform-binarys-environment)
failures and the brokerhook flake, and `internal/cli` hit `panic: test timed out after 20m0s` as
predicted above. Three failures were new:

- `scripts/test-go-packages.py`,
  `GoPackageSelection.test_completion_graph_and_census_use_the_shared_selector` (line 233): the
  graph's `rel` is `../../../../../../../private/var/folders/…/repo/pkg`, want `pkg`. **This fails
  every time on darwin**: rerun alone, it fails with the default `TMPDIR` (`/var/folders/…`, under
  the `/var` → `/private/var` link) and passes with `TMPDIR=/private/tmp`. This is the darwin
  path-resolution class AGENTS.md describes, in a Python test.
- `internal/cli` `TestHostPiLaunchGetsItsOwnSessionHome` (`hostsessionhome_test.go:179`, "the
  launch's session home lease is free, want held across the exec"): passes `-count=3` alone, so a
  flake under load.
- `internal/hostservice`
  `TestBoundedCommandClosesInheritedOutputWithoutWaitingForEscapedDescendant`
  (`boundedcommand_test.go:73`): passes `-count=5` alone, so a flake under load.

So `just done` is red on this Mac at `90862d6ef`, for reasons that are not this docs-only commit.

## 10. Rerun: witness W1b and the `just done` fixes witness

**Run 2026-11-03, later the same evening**, at `main` `fd702e9fb`, which carries the fixes for
[§2](#2-tmpdir-is-empty-in-account-mode) to [§9](#9-the-short-suite-on-this-mac) (`fd7ef428c` to `407f30c2f`). Run as a fresh dev bundle (`dev-bundles/a0ed3a40b9f26f56-…`).
This covers both witnesses that ask for a Mac run:
[W1b](../design/macos-user-model-plan.md#mac-checks) and the
[`just done` fixes witness](../plans/test-suite-speed.md#mac-witness-the-darwin-just-done-fixes-2026-11-03).
Nothing was fixed.

| # | W1b item | Result |
| :--- | :--- | :--- |
| 1 | The nine tests, by name | **As expected:** 8 PASS; only `TestMacosUserUnchangedRelaunchKeepsTheDeliveredFiles` FAILS (same two messages as [§3](#3-the-macos-user-suite-nine-failures-all-from-12a)) |
| 1a | `readlink ~/Library/Caches` in account mode | `/Users/_yolojail/Library/Caches` (from a session of my own: the home-tier test prints no such line) |
| 2 | The relaunch's boot-log line | `home overlay: ~/.claude/skills was delivered as a copy of 72 file(s), not a hard link: linking the staged file was refused (operation not permitted)`, and the same for `~/.claude/CLAUDE.md`, `bin`, `mods` and `~/.codex/AGENTS.md` |
| 2a | `sandbox-exec -p '(version 1)(allow default)(deny file-link)' /bin/ln a b` | **Compiles and refuses:** `ln: b: Operation not permitted`, rc 1, no `b`. The control without the deny links. `file-link*` does **not** compile (`unbound variable: file-link*`) |
| 2b | (added) the same link as `_yolojail` outside Seatbelt | `sudo -n -u _yolojail /usr/bin/env /bin/ln <staged SKILL.md> <ws>/.yolo/home/…` **succeeds** (link count 2). So the account mode's EPERM comes from the profile, not from file ownership or APFS |
| 2c | The relaunch test in self mode | **Also a copy:** a throwaway copy of the test with `"macos_user_mode": "self"` saw link count 1 on both launches and the inode move (128849359 → 128849591). The owner is `matt` there, so the test's owner check can't detect it |
| 3 | `echo $TMPDIR` in an account-mode session | `/private/var/folders/mr/t1nfdfgj1mn79jn_q0r6bpj40000kt/T`, and `touch "$TMPDIR/x"` succeeds. The XDG base dirs name the workspace home's `.config`, `.cache`, `.local/share`, `.local/state` |
| 3a | `getconf DARWIN_USER_TEMP_DIR` inside | **Now prints** `/private/var/folders/mr/t1nfdfgj1mn79jn_q0r6bpj40000kt/T` (rc 0), where in [§2](#2-tmpdir-is-empty-in-account-mode) it printed nothing. Not traced: plausibly confstr reads the `TMPDIR` MMP-D30 now sets |
| 3b | `nix --version` | `nix (Nix) 2.34.7`, no `$HOME … is not owned by you` line |
| 4 | The [§5](#5-an-apple-container-home-blocks-a-later-account-mode-launch) case | Fresh `w1b-c`: `YOLO_RUNTIME=container yolo -- true` (exit 0, 448 s of provisioning), then macos-user `yolo -- true` refuses, naming Apple Container and `mv <ws>/.yolo/home <ws>/.yolo/home.apple-container`, or `YOLO_RUNTIME=container`. In [§5](#5-an-apple-container-home-blocks-a-later-account-mode-launch)'s old `w1-a`, the same refusal; **after that `mv` the macos-user launch runs** with `HOME=<ws>/.yolo/home` |
| 5 | `go test -short ./integration -run '^(TestStopHomeProcesses\|TestProcessEnvironReadsALiveChildsHome)'` | **Skips, naming the install** (`nix profile install nixpkgs#bash`), as W1b allows. See the finding below |
| 6 | `run-checks.sh --mode account` while a `yolo -- sleep` runs elsewhere | **41 PASS, 0 FAIL, 4 SKIP**, g4 included. Also passed with a fresh launch started 3 s into the ~12 s run, which is when a launch sweeps dead sessions |

**Finding: the skip's own remedy leads to two failures.** The skip checks only that a
non-platform `sh` resolves. With a nix `bash-interactive` `sh` on `PATH` (the state
`nix profile install nixpkgs#bash` produces), two of the four pass and two fail:

- `TestStopHomeProcessesStopsEveryoneUnderTheHome` (`preparationworkers_test.go:1356`): "the
  stand-in the test's own child (pid …) never showed HOME=…"
- `TestStopHomeProcessesStopsWhatIsSpawnedDuringTheStop` (`preparationworkers_test.go:3791`):
  "revalidating pid … (sleep) HOME ownership: … pid … has an empty environment"

Both stand-ins are `sleep`, which still resolves to `/bin/sleep`, a platform binary. One is
`fakeHostDaemonProcess`'s `exec.Command("sleep", "600")` (`integration/homedaemons_test.go:161`).
The other is the `sleep 600 &` that `standInScript` (`preparationworkers_test.go:1283`) runs. With nix coreutils on `PATH` as well, all four
pass. So the guard should require a non-platform `sleep` too, or the stand-ins should not run the
system `sleep`.

**Decision needed:** the `file-link` grant for the staging root. 2a shows the operation name
compiles on this Mac, and 2b shows the profile is the only obstacle in account mode. 2c shows
self mode copies too, which the plan did not expect.

| # | `just done` fixes witness item | Result |
| :--- | :--- | :--- |
| 1 | `python3 scripts/test-go-packages.py`, default `TMPDIR` | 15 tests OK |
| 2 | `go test -short -count=3 -run '^TestHostPiLaunchGetsItsOwnSessionHome$' ./internal/cli` | ok |
| 3 | `go test -short -count=5 -run '^TestBoundedCommand' ./internal/hostservice`, then `pgrep -f hostservice.test` | ok; `pgrep` finds nothing (rc 1) |
| 4 | `go test -short -count=1 -run '^TestAHook' ./internal/brokerhook` | ok (49.7 s) |
| 5 | `python3 scripts/test-completion-check.py` | OK. It prints `census: wrote generated.json`, and the tree stayed clean |
| 6 | `just done` (full `just check-ci`; no verified ancestor) | **FAILED**, 26 m 36 s wall: one test, below. `internal/cli` took **1574 s** (26.2 min, inside the new 45 m limit), `internal/cli/run` 874 s, `internal/entrypoint` 966 s. The `integration` package passed, and the four `kern.procargs2` tests skip there silently: no nix `sh` is on this Mac's `PATH` |

The one failure: `internal/cli` `TestSharedDemandPublicHostProcessesProducerInterruptSecondRemains`
(`shareddemand_process_test.go`): `TempDir RemoveAll cleanup: unlinkat …/003/.local/share/yolo-jail:
directory not empty`. Something under the test's home was still writing when its cleanup ran.
**Passes `-count=10` alone** (27.8 s), so it's a flake under load, in a test from 2026-10-13
(`98fb2d3ce`). The load was this session's other launches only, lighter than
[§9](#9-the-short-suite-on-this-mac)'s.

**Still blocked:** `claude -p` still fails with "OAuth token revoked", and `log show` still prints
`Operation not permitted` from this session.

## 11. Mac witness: the fixes for the rerun's two test defects

**Run 2026-11-03 at `064706ada`: every item as expected**
([§12](#12-results-of-the-section-11-witness-and-witness-w-kc5)). This is the check for the owner's Mac agent, for the commit that fixes both
[§10](#10-rerun-witness-w1b-and-the-just-done-fixes-witness) findings. Run it from a checkout at that commit or later. Nothing here needs a container,
a launch, or the `_yolojail` account.

**What changed.**

- **The stand-ins no longer run the system `sleep`.** Each stand-in's `sleep` is now the
  integration test binary itself, run through a link named `sleep` that is first on the
  stand-in's `PATH`. The test binary is not a platform binary, so `kern.procargs2` shows its
  environment. `fakeHostDaemonProcess` runs that `sleep` directly. The skip's guard is
  unchanged: it still needs only a non-platform `sh`, so its remedy (`nix profile install
  nixpkgs#bash`) is now the whole fix. The new `TestAStandInAndItsSleepBothShowTheirHome`
  checks this: the stand-in shell and its `sleep` child must both show their `HOME`.
- **The flake was the second requester's worker.** Each requester starts its own detached
  worker, and no test waited for that process. The test waited only until `worker.lock` was
  free. A second worker that was slow to start (under load) took the lock after that check, then
  re-created the demand directory under the home while the `TempDir` cleanup was removing it.
  Now each worker inherits a liveness lock from its spawner, held from the fork on. The
  fixture's cleanup, and the interrupt test before its "no second child" check, wait until
  every such lock is free.

| # | Command (from the checkout root) | Expected |
| :--- | :--- | :--- |
| 1 | `command -v sh sleep` with no nix `sh` on `PATH` (this Mac's state in [§10](#10-rerun-witness-w1b-and-the-just-done-fixes-witness) item 6) | `/bin/sh`, `/bin/sleep` |
| 2 | `go test -short -count=1 -v -run '^(TestStopHomeProcesses(StopsEveryoneUnderTheHome\|WaitsForAScratchRemover\|StopsWhatIsSpawnedDuringTheStop)\|TestProcessEnvironReadsALiveChildsHome\|TestAStandInAndItsSleepBothShowTheirHome)$' ./integration` | All five `SKIP`, each naming `nix profile install nixpkgs#bash`. No `FAIL` |
| 3 | `nix profile install nixpkgs#bash`, then `command -v sh sleep` | `sh` resolves under `~/.nix-profile/bin` (or a nix store path). **`sleep` must still be `/bin/sleep`**: do not install coreutils, or this check proves nothing |
| 4 | Item 2's command again | All five **`PASS`**, `TestStopHomeProcessesStopsEveryoneUnderTheHome` and `TestStopHomeProcessesStopsWhatIsSpawnedDuringTheStop` included (the two [§10](#10-rerun-witness-w1b-and-the-just-done-fixes-witness) failures) |
| 5 | `go test -short -race -count=5 -run '^TestSharedDemand' ./internal/cli` | `ok` |
| 6 | `pgrep -fl TestSharedDemandProcessHelper` right after item 5 | Nothing (rc 1): no helper outlived its test |
| 7 | The next full `just done` or `just check-ci`, with this session's usual other launches running | `internal/cli` passes, and `TestSharedDemandPublicHostProcessesProducerInterruptSecondRemains` reports no `TempDir RemoveAll cleanup` error. A failure that says `helper processes still running 30s after the test` is a new finding: record which lock names it lists |

To undo item 3 afterwards: `nix profile remove bash` (`nix profile list` shows the entry's name).

## 12. Results of the section 11 witness, and witness W-KC5

**Run 2026-11-03 at `main` `064706ada`**, on the same Mac. Nothing was fixed.

| # | [§11](#11-mac-witness-the-fixes-for-the-reruns-two-test-defects) item | Result |
| :--- | :--- | :--- |
| 1 | `command -v sh sleep` | `/bin/sh`, `/bin/sleep` |
| 2 | The five tests, no nix `sh` | **All five SKIP**, each naming `nix profile install nixpkgs#bash`. No FAIL |
| 3 | A nix `sh` on `PATH`, `sleep` still the system's | **Departure:** I did not run `nix profile install`. Instead I prepended `/nix/store/a8r42jvrfindh7hcbg535mab0kylnfr6-bash-interactive-5.3p20/bin` to `PATH` for this one command ([§11](#11-mac-witness-the-fixes-for-the-reruns-two-test-defects) allows "or a nix store path"). That leaves the owner's profile untouched and needs no undo. `command -v sh` is that store `sh`; `command -v sleep` is still `/bin/sleep`. No coreutils on `PATH` |
| 4 | The five tests again | **All five PASS**, including `TestStopHomeProcessesStopsEveryoneUnderTheHome` (0.58 s) and `…StopsWhatIsSpawnedDuringTheStop` (0.29 s), the two [§10](#10-rerun-witness-w1b-and-the-just-done-fixes-witness) failures, and the new `TestAStandInAndItsSleepBothShowTheirHome` (0.13 s) |
| 5 | `go test -short -race -count=5 -run '^TestSharedDemand' ./internal/cli` | `ok` (259.6 s) |
| 6 | `pgrep -fl TestSharedDemandProcessHelper` right after | Nothing (rc 1) |
| 7 | Full `just done` | **Green:** `just done: verified 064706ada8fc (full); working tree clean`, 28 m 4 s wall. `internal/cli` `ok` in 1513 s, `internal/cli/run` 796 s, `internal/entrypoint` 897 s. No `TempDir RemoveAll cleanup` and no `helper processes still running` line anywhere in the log. **Caveat:** no launch ran beside it (W-KC5 stopped at its step 1, below), so this was lighter load than [§11](#11-mac-witness-the-fixes-for-the-reruns-two-test-defects) asks for and lighter than [§9](#9-the-short-suite-on-this-mac)'s |

This is the first green `just done` on this Mac in this report.

### Witness W-KC5: stopped at step 1

[W-KC5](../design/keychain-from-a-jail.md#W-KC5) checks the copilot pack's new `host_keychain_env`
route (`6707f0c72`). That route passes the host Copilot login into macos-user as
`COPILOT_GITHUB_TOKEN`.

- `copilot --version`: `GitHub Copilot CLI 1.0.105.` The witness's text assumes 1.0.89.
- `security find-generic-password -s copilot-cli` (attributes only, no `-w`):
  `SecKeychainSearchCopyNext: The specified item could not be found in the keychain.`

The witness says that, in this case, the service is not `copilot-cli`, so find the real one with
`security dump-keychain | grep -i copilot`, record it and stop. **That search was not run.** This
session's permission policy refused a keychain dump as credential exploration, and I didn't look
for another way in. There are two possible readings, and this run can't tell them apart: the host
Copilot is not logged in at all (W-KC5's precondition), or 1.0.105 stores its login under another
service name. Steps 2 and 3 (one Copilot prompt in a macos-user workspace, and the env-file mode
check) were not run. One more thing for whoever runs it: the witness's workspace
`~/kc5-witness` is under `$HOME`. Account mode, this Mac's default (no `macos_user_mode` key),
refuses a workspace inside a home: `HomeContaining(workspace)` runs only when the mode is not self
(`internal/macosuser/runplan.go:939`). So step 2 as written should be refused here and needs a
`/Users/Shared/…` workspace. I read that in the code and did not run the launch.

**Decision needed (the owner, at the Mac):** run `copilot login` on the host if it isn't logged
in, or run the keychain search yourself, then rerun W-KC5 from step 1.

## 13. 2026-11-04: W1c, W-SU5, `claude -p` on Bedrock, and `just done`

**Run 2026-11-04 at `main` `3ccc28def`** (the v0.16.1 notes), as a fresh dev bundle
(`yolo-jail 0.16.0+172.g3ccc28def766`). Nothing was fixed.

### W1c: passes

[W1c](../design/macos-user-model-plan.md#mac-checks) checks
[MMP-D35](../design/macos-user-model-plan.md#MMP-D35), copies kept while unchanged.

- `YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run
  '^TestMacosUserUnchangedRelaunchKeepsTheDeliveredFiles$' ./integration`: **PASS** (30.4 s),
  `/account` 11.2 s and `/self` 19.2 s. The harness also printed `stopped the processes left
  running under this test's HOME …: pid … (internal pack-tree-reap), …`
  (`detachedwriters_test.go:122`) for the self subtest. That's the harness cleaning up, not a
  failure; noted in case it shouldn't happen.
- The manual record: two `YOLO_RUNTIME=macos-user yolo -- true` relaunches of `w1b-a` (both exit
  0), then `rg -n 'delivered as a copy' .yolo/boot.log` **printed nothing** (rc 1) against a
  `boot.log` written by the second launch. `grep -c 'home overlay'` on it is 0.

### W-SU5: one expectation does not match

[W-SU5](../design/named-setups-plan.md) checks the `yolo yolo-account` verbs (slice 7,
`563a42895`). It ran in a throwaway `/Users/Shared/yolo/su5-share`, with stdin closed and a 60 s
alarm on each command so a password prompt could not hang it:

| Command | Expected | Observed |
| :--- | :--- | :--- |
| `yolo macos-setup` | the new name, non-zero | `yolo macos-setup is now yolo yolo-account create. Run:` / `yolo yolo-account create`, rc 2 ✓ |
| `yolo yolo-account share <dir>` | the ACL lists `_yolojail` | `✓ Applied shared-group ACLs`, rc 0; `ls -lde` shows two explicit `group:_yolojail allow …` entries ✓ |
| `yolo yolo-account create` (account exists) | `The yolo account _yolojail already exists.`, rc 0, no password | **✗** `• sudo needs your password to set up the yolo account and its rule (/etc/sudoers.d/yolo-jail).`, then `sudo: a password is required` and `✗ sudo did not run the check of /etc/sudoers.d/yolo-jail, so nothing was changed. Run yolo yolo-account create again and give your admin password when sudo asks.`, rc 1 |
| `yolo yolo-account unshare <dir>` | rc 0, no `_yolojail` entry | `✓ Stripped yolo-jail ACLs`, rc 0, and `ls -lde` lists **no entries at all** ✓ (see below) |
| `yolo yolo-account create --help` | usage, no sudo | `Usage: yolo yolo-account create` and its description, rc 0, no prompt ✓ |

**The `create` divergence is not new.** The installed `yolo` (`0.16.0+30.g5342558ff`, before
the rename) runs `yolo macos-setup` the same way: the same password line, the same `sudo did not
run the check of /etc/sudoers.d/yolo-jail`, rc 1. So on a Mac where the account exists,
`create` still re-checks the sudo rule through a password `sudo`. W-SU5's "already exists, exit
0, no password" describes behavior neither build has.

**Decision needed:** whether `create` should short-circuit when the account and its rule are in
place, or whether W-SU5's expectation should change.

**What `unshare` removed.** After `share`, `<dir>` carried two explicit `_yolojail` entries and
four inherited ones from `/Users/Shared/yolo`: two for `group:_yolojail`, and two for a principal
shown only as `0E7B72D7-D737-43C8-B1DB-5D8E8C7CA00F`. `dscl . -search` finds no user or group with
that GeneratedUID, so it's an orphan entry, and `/Users/Shared/yolo` itself carries it explicitly.
`unshare` left `<dir>` with no ACL entries at all, the inherited orphan included. Whether
stripping inherited and non-yolo entries is intended is not checked.

### `claude -p` answers on Bedrock

The `claude -p 'Reply with the single word: ok'` line in W1 (and W-SU3) failed in [§8](#8-blocked-and-not-run) and [§12](#12-results-of-the-section-11-witness-and-witness-w-kc5) with
`OAuth token revoked`. Those runs used plain `yolo -- claude -p …`, which takes the Anthropic
subscription. This Mac's user config also declares a `bedrock` profile for Claude
(`work.toml`, plus the `claude-bedrock-global` pack). With it, in account mode:

```
YOLO_RUNTIME=macos-user yolo -p claude=bedrock -- bash -c 'claude -p "Reply with the single word: ok"'
→ ok   (rc 0; the launch took 223 s, most of it a first install of Claude in that workspace)
```

So the jail's Claude works. What is revoked is the **subscription login that jails share**. The
owner switches between the subscription and Bedrock, and was asked to run `/login` once in a
subscription session to re-seed it. The subscription variant of the line stays unchecked until then.

### `just done`: green, and a Mac timing after levers A and B

`just done` ran the full `just check-ci`, because `.github/workflows/macos-user.yml` changed:
**`verified 3ccc28def766 (full); working tree clean`**, 24 m 33 s wall. No `TempDir RemoveAll
cleanup` and no `helper processes still running` line.

| Package | 2026-11-03 at `064706ada` ([§12](#12-results-of-the-section-11-witness-and-witness-w-kc5)) | 2026-11-04 at `3ccc28def` |
| :--- | ---: | ---: |
| `internal/cli` | 1513 s | **1289 s** |
| `internal/cli/run` | 796 s | 878 s |
| `internal/entrypoint` | 897 s | 959 s |
| `internal/packsrc` | — | 364 s |

`internal/cli` is 224 s (15%) faster, the package [levers A and B](../plans/test-suite-speed.md)
target. The other two are slower. That is **not a controlled comparison**: one run each, and
this one overlapped a macos-user launch (the Bedrock check above, about 4 minutes) where [§12](#12-results-of-the-section-11-witness-and-witness-w-kc5)'s had
none. The plan's inference for lever A on the Mac was 1.3 to 2.3 minutes saved. This result is
consistent with it, but doesn't measure it. I counted no git starts.

### Also checked

`yolo setup list` (slice 1, `ba6ac38c9`) lists the eight built-ins, marks `landlock-*` `not
available here`, and says `Selected: apple-container-self, this OS's default`. `yolo setup show`
prints each built-in's `[setups.<name>]` table. No other W-SU witness ran: their slices are not
built.

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
