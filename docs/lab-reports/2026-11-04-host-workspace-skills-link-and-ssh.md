---
title: "Mac run: the untracked .claude/skills link, and what ssh can and cannot do"
date: 2026-11-04
tags: [lab-report, macos, host, skills, keychain]
summary: "yolo host -- claude links .claude/skills to the repository's .pi/skills and adds an ignore line once; when that line is reverted, by anyone, yolo never re-adds it, there is no opt-out, and the owner read the link as Claude Code's. Separately: over ssh only first-time keychain dialogs fail; log show needs admin, not the desktop."
---

# Mac run: the untracked `.claude/skills` link, and what ssh can and cannot do

**Run 2026-11-04 on the maintainer's Mac** (macOS 26.5, arm64), in this checkout at `main`
`f4619eca2`, with the installed `yolo` `0.16.1+35.g7f1210e630c9`. It follows the
[slice 12a witness report's §15](2026-11-03-mac-slice-12a-witness-w1.md#15-2026-11-04-at-the-macs-own-display-w-kc5-finished-and-copilot-cannot-make-https-requests-in-account-mode). Nothing was fixed.

**Terms.**

- *`yolo host`* runs an agent on the real machine with no confinement
  ([`host-agent-environment.md`](../reference/host-agent-environment.md)). This session runs as
  `yolo host -- claude`.
- The *host workspace skills link* is the one symlink `yolo host -- <agent>` writes into a
  workspace so that the agent reads the repository's project skills
  ([the host half](../reference/agent-briefings.md#the-host-half-one-link-per-launch),
  [`OQ-WS6`](../reference/agent-briefings.md#OQ-WS6)). It's the host's counterpart of the mirror a
  jail gets.
- *Aqua* and *Background* are what `launchctl managername` prints in a shell inside the desktop
  login session and in an ssh session.

## 1. Results

| # | Finding | Kind |
| :--- | :--- | :--- |
| 1 | `.claude/` in `git status` is yolo's host workspace skills link, not Claude Code's | Explained ([§2](#2-what-made-claude)) |
| 2 | Its ignore line, once reverted by anyone, is never re-added, and the launch line is all that says so | Gotcha ([§3](#3-the-ignore-line-is-added-once-and-a-revert-counts-as-the-owners)) |
| 3 | No switch turns the link off; deleting it brings it back | Missing opt-out ([§4](#4-there-is-no-opt-out)) |
| 4 | The owner could not tell where the folder came from | Disclosure gap ([§5](#5-the-owner-read-it-as-claude-codes)) |
| 5 | Over ssh, only a first-time keychain dialog fails; after *Always Allow*, yolo's read works | Measured ([§6](#6-ssh-against-the-macs-own-display)) |
| 6 | `log show` needs the admin group, not the desktop session | Measured ([§6](#6-ssh-against-the-macs-own-display)) |
| 7 | Copilot's commented `config.json` fix works; the new keychain message calls a measured result unmeasured | Verified, one stale clause ([§7](#7-later-the-same-day-712b4238c-on-the-mac-and-the-macos-guides-yolo-ps--yolo-stop)) |
| 8 | `yolo ps` and `yolo stop` miss a Seatbelt session under the default `apple-container-self` setup | Defect or doc gap ([§7](#7-later-the-same-day-712b4238c-on-the-mac-and-the-macos-guides-yolo-ps--yolo-stop)) |
| 9 | `yolo ps` puts Apple Container's whole row in STATUS, and lists the builder | Defect ([§7](#7-later-the-same-day-712b4238c-on-the-mac-and-the-macos-guides-yolo-ps--yolo-stop)) |
| 10 | W9's refusal is as specified, but two of its three routes don't run Copilot from that workspace | Witness passes, defects in its routes ([§8](#8-w9-and-w2s-cache-line-run-through-yolo_setupseatbelt-self)) |
| 11 | Run-as-you mode has no working pip; with Homebrew's, `pip download` fills the host's pip cache | W2's cache line passes, with a substitute ([§8](#8-w9-and-w2s-cache-line-run-through-yolo_setupseatbelt-self)) |
| 12 | After the owner's `/login`, `claude -p` answers on the subscription in both W1 workspaces and every W2 launch | W1 and W2 pass ([§9](#9-2026-11-05-the-claude-subscription-lines-of-w1-and-w2-pass)) |

## 2. What made `.claude/`

`git status` in this checkout listed `?? .claude/`, which holds one entry:
`skills -> ../.pi/skills`, created 2026-11-04 13:05, the moment this session resumed. That
untracked entry stops `just done` ("the working tree is not clean; no check ran: ?? .claude/skills").

The writer is `hostWorkspaceSkills` (`internal/cli/hostworkspaceskills.go:89`), which every
`yolo host -- <agent>` launch calls. Its rule, as `yolo config-ref` states it
(`internal/cli/config_ref.txt:2123`): when the repository has none of the launched agent's project
skill paths, yolo puts one link at the agent's first path (`.claude/skills` for claude, from
`packs/claude/pack.toml:93`). It points at the first other agent's skills directory the
repository does have, here `.pi/skills`, and adds the link's path to the workspace's `.gitignore`
once. It never writes where the repository already has any of those paths, in any form.

The link is doing real work. This session's skill list includes `cutting-releases` and
`context-first-design`, which are this repository's `.pi/skills`, reaching Claude Code through
it.

yolo's record of it, `~/.local/share/yolo-jail/host-workspace-skills/c028f44993c56f12.json`
(named by `hostSkillsRecordPath`, `:537`):

```json
{
  "workspace": "/Users/Shared/yolo/yolo-jail",
  "link": ".claude/skills",
  "target": "../.pi/skills",
  "created_dirs": [".claude"],
  "ignore_seen": true
}
```

## 3. The ignore line is added once, and a revert counts as the owner's

`ensureHostSkillsIgnoreLine` (`internal/cli/hostworkspaceskills.go:469`) appends `/.claude/skills`
to `.gitignore` unless the record already has `ignore_seen`. If the line stood there once and is
gone now, it does not add it back, and the launch says `` `/.claude/skills` was in .gitignore and
has been removed, so yolo leaves it out and `git status` will list the link; add it back
yourself to quiet it `` (`:487`). That is [`OQ-WS6`](../reference/agent-briefings.md#OQ-WS6)'s leaning (a): a user who removes the line is
not fought.

What happened here: at the start of the
[first Mac session](2026-11-03-mac-slice-12a-witness-w1.md) (2026-11-03), `git status` showed
`M .gitignore`. That was yolo's appended line. Moments later the tree was clean apart from
`?? .claude/`, so the line had been reverted. This checkout is worked on by several agents at
once, and which one reverted it is not known. Nothing in the git history records it:
`git log -- .gitignore` shows no commit adding or removing the line. Since then, every launch
leaves it out.

**The gotcha:** the rule cannot tell the owner removing the line from a `git checkout`, `git
stash` or `git reset` that an agent ran to clean its tree. The second is the common case in a
checkout shared by agents, so the "do not fight the user" rule mostly protects a decision no user
made.

**Decision needed:** whether a revert of the line by a tree-cleaning command should count as
removal, or whether yolo should re-add the line while it keeps the link (or offer to, at the
launch).

## 4. There is no opt-out

`hostWorkspaceSkills` returns early only inside a jail (`config.InJail()`), for a workspace
`paths.WorkspaceScopeBreach` refuses, and when no selected pack gives the agent a skills
destination with `project_dirs`. No config key, environment variable or flag skips it. A deleted
link is placed again by the next launch, and the code's own lines say so ("the next launch places
it again", `:233` and `:238`).

**Decision needed:** whether to offer an opt-out (per workspace, or a user-scope key).

## 5. The owner read it as Claude Code's

The owner asked: "where does this .claude/ stuff come from? … why does claude do this? I didn't
ask for it. can I turn it off?" The folder's name is Claude Code's, its only entry is a skills
link, and it appeared when a Claude session resumed. Nothing in the repository says yolo wrote it.
The launch does print a `yolo host: workspace skills: …` line (`hostWorkspaceSkills`, `:106`), but
that is one line among a launch's many, and it scrolled past.

**Decision needed:** whether the link should carry its own explanation. It's a symlink, so it
can't hold a comment. Options include a named `.gitignore` comment line above the entry, or
`yolo check` reporting the link and why it exists.

### What the owner can do today

- **This repository:** add `/.claude/skills` back to `.gitignore` and commit it. That's the
  state yolo intended, and it unblocks `just done`. Not done here: it's the owner's call.
- **Globally:** ignoring `.claude/skills` everywhere is not recommended, because some repositories
  commit real skills there, and an ignore would hide a new one until it is force-added. Claude
  Code's machine-local files, `.claude/settings.local.json` and `.claude/worktrees/`, are the safe
  global ignores.

## 6. ssh against the Mac's own display

The [slice 12a report](2026-11-03-mac-slice-12a-witness-w1.md) blamed several "could not" results
on ssh. Measured since:

| Check | Over ssh (`Background`) | At the display (`Aqua`) |
| :--- | :--- | :--- |
| `security find-generic-password -s copilot-cli -w` before *Always Allow* | `rc=36` (`errSecInteractionNotAllowed`), no dialog; the same in tmux and in a fresh ssh session without tmux | Dialog, `rc=0` after Allow |
| The same, after *Always Allow* | **`rc=0`** | `rc=0` |
| yolo's own read (a `YOLO_SETUP=seatbelt-yolo-account yolo -- true` launch in `kc5-witness`) after *Always Allow* | **`Host login passed in: your host copilot login (keychain service copilot-cli) is COPILOT_GITHUB_TOKEN …`** | the same |
| `log show --predicate 'sender == "Sandbox"'` | `Operation not permitted` | `Operation not permitted` unless the owner holds admin (Privileges.app); works with it |
| Tests, launches, `just done`, `run-checks.sh` | work | work |

So over ssh, only a keychain item yolo has not been allowed to read yet needs the Mac's display
(once, choosing *Always Allow*). The system log needs admin rights wherever it's read. An
earlier one-time *Allow* (the owner pressed Enter) was not remembered: the next ssh read failed
again.

The keychain-read message for `rc=36`, "the keychain is locked and macOS could not ask you to
unlock it" (`securityExitError` in `internal/cli/run/hostkeychainenv.go`), was wrong in every case
here: `security show-keychain-info` reported the login keychain unlocked throughout. Its decision
is already recorded in the [slice 12a report, W-KC5 after copilot login](2026-11-03-mac-slice-12a-witness-w1.md#w-kc5-after-copilot-login).

## 7. Later the same day: `712b4238c` on the Mac, and the macOS guide's `yolo ps` / `yolo stop`

**Run 2026-11-04 over ssh**, at `main` `bc09f809b`, as a dev bundle
(`dev-bundles/d191f50b93a4ba34-…`). Nothing was fixed.

### `712b4238c` ("integrate reviewed broker and Copilot fixes")

- **Copilot's commented `config.json`: fixed, measured.** `kc5-witness/.yolo/home/.copilot/config.json`
  still had Copilot 1.0.105's header (`// User settings belong in settings.json.` / `// This
  file is managed automatically.`). A `YOLO_SETUP=seatbelt-yolo-account` launch printed **no
  `copilot/config` warning**, rewrote the file (mtime 16:47:22), and kept both comment lines above
  the JSON (`splitJSONCommentHeader`, `internal/entrypoint/surfacecodec.go`).
- **The keychain message: reworded, not reproduced live.** `ErrHostKeychainNoDialog` now says
  `macOS refused to let security show its keychain access dialog to this process`. The next step
  names ssh when `SSH_CONNECTION` or `SSH_TTY` is set, and suggests Always Allow from a Terminal on
  the Mac. *Always Allow* is already set here, so exit 36 no longer happens and the line could not
  be seen. **One stale clause:** it says "whether Always Allow also enables later reads over SSH
  has not been measured". [§6](#6-ssh-against-the-macs-own-display) measured it (`rc=0` over ssh,
  and yolo's own read passed), but the commit came before that report.
- **Unit tests on darwin:** `go test -short` passes for `./internal/cli/run/ -run HostKeychain`,
  `./internal/entrypoint/ -run 'Copilot|Prism|Surface|Codec'`, `./internal/config/`,
  `./internal/loopholes/` and `./internal/paths/`.

**Decision needed:** drop the "has not been measured" clause, or reword it to the measured
result.

### `yolo ps` and `yolo stop` on Seatbelt: "Not yet tried on a Mac", now tried

`userguide/guides/macos.md` (around line 383) says that on `Seatbelt`, `yolo stop` from the
project folder "ends every session of the project and the background process that holds their
host services", and that `yolo ps` "lists each session that is still running and its project
folder". It also says "Not yet tried on a Mac". This run started `bash -c 'sleep 300'` in
`/Users/Shared/yolo/guide-check` under `YOLO_SETUP=seatbelt-yolo-account`, then:

| Command | Observed |
| :--- | :--- |
| `yolo ps` (this Mac's default setup, `apple-container-self`), from `/tmp` and from the project folder | Only the `yolo-ac-builder` container. **The Seatbelt session is not listed**, and nothing points at where it would be |
| `YOLO_SETUP=seatbelt-yolo-account yolo ps` | `yolo-guide-check-b1066db5  running (macos-user session)  /Users/Shared/yolo/guide-check` ✓ |
| `yolo stop` from the project folder, default setup | **`No jail running for this workspace (yolo-guide-check-b1066db5).`**, rc 0, while that workspace's session ran |
| `YOLO_SETUP=seatbelt-yolo-account yolo stop` from the project folder | `sent SIGTERM to the keeper (pid 56064), a session (pid 55648)` … `Stopped this workspace's macos-user sessions and their keeper.`, rc 0. The session ended (exit 143), and the next `yolo ps` printed `No running macos-user sessions.` ✓ |

So both verbs work as the guide says, **but only under the Seatbelt setup**. On a Mac whose
default is `apple-container-self`, a plain `yolo stop` reports nothing running for a workspace
whose Seatbelt session is live. The guide's sentence names no setup. The pointer exists in one
direction only: the Seatbelt `yolo ps` ends with `Container jails are listed under their own
runtime: YOLO_SETUP=apple-container-self yolo ps (or YOLO_SETUP=podman-self yolo ps).`, but the
container side has no line pointing to Seatbelt.

**Decision needed:** whether `yolo ps`/`yolo stop` should cover every setup, say which setup
they looked at and name the others, or whether the guide should say to pass the setup.

### `yolo ps` crams the whole Apple Container row into STATUS

Default-setup `yolo ps` printed:

```
CONTAINER        STATUS                                                                                                               WORKSPACE
yolo-ac-builder  ghcr.io/mschulkind-oss/yolo-jail-builder:latest linux arm64 running 192.168.64.2/24 8 12288 MB 2026-10-09T22:39:57Z  unknown
```

`ParseContainerLsRows` (`internal/runtime/probe.go:185`) takes the first field as the name and
joins every later field into the status. `container ls` now prints `ID IMAGE OS ARCH STATE IP
CPUS MEMORY STARTED`, so STATUS is the image, OS, arch, state, IP, CPUs, memory and start time.
The row is also not a jail: `yolo-ac-builder` is the long-lived builder (up since 2026-10-09),
listed because its name starts with `yolo-`. `internal/cli/applebuildcapability.go`'s listing
parser already reads this table by header, MEMORY's two tokens included.

**Decision needed:** parse by header here too, and decide whether the builder belongs in
`yolo ps`.

## 8. W9 and W2's cache line, run through `YOLO_SETUP=seatbelt-self`

Both witnesses needed `macos_user_mode = "self"` in the owner's user config, which these sessions
did not edit. Named setups now select run-as-you mode for one launch (`YOLO_SETUP=seatbelt-self`),
so both ran without a config change. Installed `yolo` `0.16.1+35.g7f1210e630c9`, over ssh, at
`main` `dca0520ea` (nothing new had landed). Nothing was fixed.

### W9 ([`copilot-keychain-boundary.md`](../research/copilot-keychain-boundary.md), `8a48c2cc7`)

**The refusal: as specified.** In a fresh `/Users/Shared/yolo/w9-self`, `YOLO_SETUP=seatbelt-self
yolo -- copilot -p 'hi'`, plain and with `YOLO_USE_INSTALLED=copilot`, printed in both cases,
before any agent output:

```
yolo: ERROR: copilot 1.0.105 is installed. Copilot CLI 1.0.93 and newer cannot make HTTPS requests under macos-user self's
required SecurityServer deny: its native TLS needs the macOS service com.apple.SecurityServer, which run-as-you mode denies
because it is also the way into your login keychain. YOLO_USE_INSTALLED does not get past it, since every request the
installed version makes fails the same way. Upstream: https://github.com/github/copilot-cli/issues/5085

Next steps, in a terminal outside this jail:
  yolo config set setup seatbelt-yolo-account --user  run as the yolo account (yolo yolo-account create creates it once)
  yolo --setup podman-self -- copilot               run this launch in a podman container (apple-container-self for Apple Container)
  yolo config set setup podman-self --user          run every launch in a podman container (apple-container-self for Apple Container)
```

Both logs: `Checking GitHub` 0 times, `Start anyway` 0 times, exit 1. The routes are now named by
setup, no longer by `macos_user_mode` and `YOLO_RUNTIME` as the witness text has them.

**Each route, with `copilot update` as the HTTPS check, from the same workspace:**

| Route | Result |
| :--- | :--- |
| `seatbelt-yolo-account` (the first one named) | **Fails**: account mode denies Copilot `com.apple.SecurityServer` since slice 12a, and `copilot update` gets `error sending request` to `api.github.com` (the [slice 12a report, why Copilot cannot reach GitHub](2026-11-03-mac-slice-12a-witness-w1.md#why-copilot-cannot-reach-github-the-account-mode-now-denies-securityserver), confirmed against 12a's parent) |
| `yolo --setup podman-self -- bash -c 'copilot update'` | **Works**: `GitHub Copilot CLI 1.0.98.`, then `Download complete. Installing...` / `Copilot CLI version 1.0.105 installed.`, rc 0 |
| `yolo --setup apple-container-self -- …` (the parenthetical) | **Refused before start**, because the self-mode launch had just laid out this workspace's home: `refusing Apple Container inherited preflight scope: existing user file /Users/Shared/yolo/w9-self/.yolo/home/.config/yolo-jail/config.toml is in the way, and the whole HOME bind would overwrite it. Review it, preserving its contents and mode, then move it aside (mv … config.toml.moved-aside) and relaunch` |

The Apple Container refusal is the reverse of the
[slice 12a report §5](2026-11-03-mac-slice-12a-witness-w1.md#5-an-apple-container-home-blocks-a-later-account-mode-launch)
collision: Seatbelt, then Apple Container, in one workspace. Unlike the Apple Container-then-Seatbelt
direction ([MMP-D32](../design/macos-user-model-plan.md#MMP-D32)), it gets the generic message,
which doesn't say that a Seatbelt launch laid out the home or how to keep using both. Named
setups' per-setup homes ([`named-setups-plan.md`](../design/named-setups-plan.md) slice 6a, not
built) are meant to remove the collision. Until then, the W9 refusal sends a user from a
run-as-you workspace to one route that fails (account mode) and one that refuses in the same
workspace (Apple Container). Only Podman worked.

**Decision needed:** drop or qualify the account-mode route while account mode denies
SecurityServer, and either qualify the Apple Container route or give the Seatbelt-then-Apple
Container collision its named refusal before slice 6a.

### W2's cache line ([MMP-D12](../design/macos-user-model-plan.md#MMP-D12), run-as-you mode shares host caches)

- **The link is there and disclosed.** In `/Users/Shared/yolo/w2-pip`, the launch printed `Host
  caches aliased (writable) — this jail shares these host stores instead of keeping second copies:`
  / `pip: /Users/matt/Library/Caches/pip → /Users/matt/.local/share/yolo-jail/macos-user/Library/Caches/pip`.
  Inside, `<ws>/.yolo/home/Library/Caches/pip -> /Users/matt/Library/Caches/pip`.
- **The sandbox has no working pip.** `pip` resolves to nothing. `pip3` is `/usr/bin/pip3`, Apple's
  stub, which printed `xcode-select: Failed to locate 'pip3', and no install could be requested
  (perhaps no UI is present).` (rc 72; the Command Line Tools are not installed). `python3` is
  yolo's tool-floor Python (`/nix/store/…-yolo-noncontainer-profile/bin/python3`), and it has `No
  module named pip`. So W2's `pip download requests` cannot run as written on this Mac.
- **With Homebrew's pip, it passes.** `/opt/homebrew/bin/python3 -m pip download --no-deps -q
  requests -d "$(mktemp -d)"` inside the sandbox: rc 0, `requests-2.34.2-py3-none-any.whl`. The
  host's `~/Library/Caches/pip` went from **3478 to 3482 files**. A count of files modified in the
  last 3 minutes read 0, presumably because pip keeps the original times on what it caches, so the
  count is the measurement.

**Decision needed:** whether W2 should name a pip the sandbox has, or whether the floor should
carry one.

W2's other lines (Claude logged in twice with no `/login`, and a second workspace at the same time)
were not run: they need the Claude subscription, which is out of credits until 2026-11-05.

## 9. 2026-11-05: the Claude subscription lines of W1 and W2 pass

The owner signed the shared Claude login in again with `/login` in a
`YOLO_SETUP=seatbelt-yolo-account yolo -- claude` session. Before that, the same probe still
failed with `Failed to authenticate: OAuth token revoked.` Then, over ssh, installed `yolo`
`0.16.1+35.g7f1210e630c9`, every probe being `claude -p "Reply with the single word: ok"` inside
the launch:

| Witness line | Setup, workspace | Result |
| :--- | :--- | :--- |
| W1: `claude -p` answers in both workspaces | `seatbelt-yolo-account`, `/Users/Shared/yolo/w1b-a` | `ok`, rc 0 |
| | `seatbelt-yolo-account`, `/Users/Shared/yolo/w1-d` | `ok`, rc 0 |
| W2: logged in on the first launch, no `/login` | `seatbelt-self`, fresh `~/code/alpha` (`HOME=/Users/matt/code/alpha/.yolo/home`) | `ok` |
| W2: a second workspace runs at the same time | `seatbelt-self`, fresh `~/code/beta`, launched while alpha's session slept | `ok`, exit 0; alpha's session was still running (its `A1 DONE` printed later) |
| W2: logged in on the second launch, no `/login` | `seatbelt-self`, `~/code/alpha` again, after the first session ended (exit 0) | `ok` |

With the pip-cache line of [§8](#8-w9-and-w2s-cache-line-run-through-yolo_setupseatbelt-self),
**W2 passes in full** (pip by Homebrew's, as [§8](#8-w9-and-w2s-cache-line-run-through-yolo_setupseatbelt-self) says), and **W1's last line passes**. That
leaves W1's own expectations met apart from the findings already recorded in the
[slice 12a report](2026-11-03-mac-slice-12a-witness-w1.md) (the overlay copy is ruled; the
account-mode SecurityServer deny is open). Nothing was fixed.

## Evidence

```
git status --short                                   # ?? .claude/
ls -la .claude/                                      # skills -> ../.pi/skills, 2026-11-04 13:05
cat ~/.local/share/yolo-jail/host-workspace-skills/c028f44993c56f12.json   # above
grep -n claude .gitignore                            # no match
just done                                            # refuses: ?? .claude/skills
launchctl managername                                # Background (ssh), Aqua (display)
security find-generic-password -s copilot-cli -w >/dev/null; echo rc=$?   # the table above
dseditgroup -o checkmember -m matt admin             # no matt is NOT a member of admin
```
