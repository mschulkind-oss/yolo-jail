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
