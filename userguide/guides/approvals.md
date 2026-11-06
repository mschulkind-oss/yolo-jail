# Approving What the Agent Asks For

A jailed agent can ask you for two kinds of things, and you answer each in a different place.

| The agent asks for | How it asks | How you answer | Where it is explained |
|---|---|---|---|
| **Standing access** for the project: push to `main`, open pull requests, read your journal | `yolo request <source> <capability>`, which writes a line in the project's `yolo-jail.toml` | `yolo config apply` on your machine, in the project, and `y` at its prompt | [Standing access](#standing-access-the-ceiling-and-the-request) below |
| **One action now** that no standing access covers: a `git push` or a `gh` write | A **request** the broker files on your machine, with an id such as `r-3f9a…`; the agent is told the id | `yolo approve r-<id>` on your machine | [One action](#one-action-yolo-approve) below |

Standing access is a **grant**: a named thing a project may do, approved once and held from then
on. A one-action request is answered once, or turned into standing access on the spot. This page
covers both, and how to be told when a request is waiting.

## Standing access: the ceiling and the request

Access a project holds for good comes from two files, and each has a command that writes it:

1. **Your user config sets the ceiling.** `[allow.<source>]` in `~/.config/yolo-jail/config.toml`
   lists what projects may hold. `always = [...]` gives every project a capability; `on_request =
   [...]` lets a project ask for it. Write it on your machine:

   ```bash
   yolo allow git fetch                     # every project: fetch with your git credentials
   yolo allow git on_request push           # a project may ask to push a branch
   yolo allow github on_request create_pr   # a project may ask to open pull requests
   ```

   Without `always` or `on_request`, the bare form is `always`. A capability that carries values,
   such as process names or device patterns, can only be allowed `on_request`, since only a project
   knows which values it needs.

2. **The project asks.** `[requests.<source>]` in the project's `yolo-jail.toml` names what this
   project wants. Run it in the project, on your machine or in the jail; the agent may run it too:

   ```bash
   yolo request git push main
   yolo request github create_pr
   yolo request journal read
   ```

   It writes the line, runs `yolo check`, and names the step that approves it.

3. **You approve it on your machine**, in the project, where the request shows in the same
   prompt that approves any config change:

   ```bash
   yolo config apply
   ```

   A running jail's broker takes a git or GitHub grant at its next call. Loophole grants such as
   the journal's change at once too. With no jail running, the next launch starts with the grant.

A request is only a request. **A request outside your ceiling is skipped, never an error**, and
you are told where you look: `yolo check`, every launch, and the `yolo config apply` prompt each
show it, with the command that would allow it:

```text
[WARN] yolo-jail.toml:2: this project requests git push main.
  Your config does not allow push on request, so this project runs without it.
  To allow it for every project you work on (in your config, not this file), run on the host:
    yolo allow git on_request push
  then approve it for this workspace, if that command did not already ask:
    yolo config apply
```

Run the two commands it names, in that order, and the warning is gone. `yolo allow`, run in the
project, offers that approval itself where it can, which is why the message says "if that command
did not already ask".

To take standing access back, remove the request from the project's config and approve the
change, or remove the capability from your ceiling, which reaches every project:

```bash
yolo config unset requests.git.push --project    # in the project; then yolo config apply
```

The per-source pages list each capability: [Git](git.md), [GitHub](github.md) and
[Host Access and Loopholes](loopholes.md#giving-a-project-access).

## One action: `yolo approve`

Two brokers can ask you for one action: the git broker, for a `git push` that no grant or
working-set entry covers, and the GitHub broker, for a `gh` write that no grant covers (a merge
always asks). Loopholes such as the journal never ask this way: they have standing access or
nothing.

### What the agent sees

The push or `gh` command waits 30 seconds for your answer. Unanswered, it is refused with nothing
sent, and the refusal lists every way to allow it, each command on its own line, so the agent can
relay them to you. A git push names the request on each refused ref:

```text
! [remote rejected] refs/heads/main (needs approval: r-<id>)
```

The agent then runs `yolo wait r-<id>` in the jail, which waits until you answer, up to the
request's expiry, and exits saying what you decided, so the agent can run the command again once
you allowed it. A request nobody answers **expires after an hour** as refused.

### Answering on your machine

`yolo approve` with no arguments lists every pending request, every live grant and each project's
working set, from any terminal, one over SSH included. A request's line gives its id, its jail,
its set (`push`, or the kind of GitHub write), the project in brackets, the repository and the
command. Then answer it:

| Command | What it allows |
|---|---|
| `yolo approve r-<id>` | **Allow once**: that exact command, one use, within 15 minutes |
| `yolo approve r-<id> --for 15m` | For a git push: every push to the refs that needed approval, for that long (any duration up to 24 hours). For a GitHub write: that kind of write on its PR or issue (or, for a creation or a repository-level write, in its repository), up to 12 hours |
| `yolo approve r-<id> --deny` | Refuse it. The agent is told, and should not try it again without asking you |

The refusal the agent relays prints exactly these lines for its request, plus the project request
(`yolo request git push <branch>` then `yolo config apply`) and the command to run on your machine
instead. For a branch you will push again, the project request is the one to pick; a one-off
takes `yolo approve r-<id>` alone.

`--add` and `--remember` are gone. Either is refused, deciding nothing, with a message naming the
answers above. Branches, PRs and issues you added with `--add` before then stay in the working
set until you revoke them.

An approval belongs to the project, not to one jail: every jail of that project may use it, and
it lasts through broker and jail restarts until it expires, its uses run out, or you revoke it.
`yolo approve --revoke <id>` ends a grant (`g-…`) or a working-set entry (`w-…`); `--revoke all`
ends every live grant, and `--revoke all --workspace <path>` that project's grants and recorded
working set. A branch declared in the project's config (`c-…`) is not the broker's to revoke:
the command names the file to edit instead. `yolo approve --help` has every exit code.

### Getting notified

Nothing is printed at the agent's terminal when a request is filed: the agent owns that screen,
and a question drawn into it could be answered by the agent's own output. Without a notifier you
learn of a request from the agent, or from `yolo approve`. Two ways to be told at once:

**A desktop notification, with one line of config.** The `on_request` key names a command that
each broker on your machine runs, as you, with `sh -c`, from your home directory, once for every
request a jail files. It goes at the **top level** of your user config, and only there:

```toml
# ~/.config/yolo-jail/config.toml
on_request = 'notify-send -u critical "yolo: $YOLO_REQUEST_HEADLINE" "$YOLO_REQUEST_DESCRIPTION_MARKUP"'
```

Then run `yolo check`. It takes effect at the next fresh launch. The request's fields are in the
command's environment, as `YOLO_REQUEST_*` variables in three groups, beside `YOLO_SOURCE`
(`git` or `github`), `YOLO_EVENT` and `YOLO_REQUEST_WORKSPACE`:

- **Safe anywhere**, yolo's own words and validated parts, needing no escaping:
  `YOLO_REQUEST_ID`; `YOLO_REQUEST_ACTION` (`push`, `pr-create`, `pr-merge`, `pr-write`,
  `issue-create`, `issue-write`, `workflow-dispatch`, `repo-write` or `api`);
  `YOLO_REQUEST_TARGET` (such as `PR o/r#70`); `YOLO_REQUEST_HEADLINE`, a short phrase that
  needs no assembly (`merge PR o/r#70`, `push 2 refs to github.com/o/r`, `run a workflow on
  o/r`); `YOLO_REQUEST_SET`, `YOLO_REQUEST_JAIL`, `YOLO_REQUEST_EXPIRES`; and
  `YOLO_REQUEST_REPO` (for git, the remote's host and path as its approved URL names them).
- **Free text**, the agent's words, made safe for a terminal but nothing else:
  `YOLO_REQUEST_TITLE` (a `--title` or `--subject`, a workflow file, an API path),
  `YOLO_REQUEST_REFS` (one per line: a push's refs, a dispatch's ref),
  `YOLO_REQUEST_DESCRIPTION` (the headline, then `title: ...` and one `ref: ...` per ref, a
  line each), and `YOLO_REQUEST_SUMMARY` (one line, the complete record: the id, the set, the
  repository and the jail, then the command and its digests).
- **Escaped for notification markup**: `YOLO_REQUEST_DESCRIPTION_MARKUP` and
  `YOLO_REQUEST_SUMMARY_MARKUP`.

The same request arrives as one JSON object on the command's standard input, with every field
but the two markup ones.

The free text is the agent's words, so treat it as untrusted. A plain-text notifier (a terminal,
a log) shows `YOLO_REQUEST_DESCRIPTION`, one that renders markup shows
`YOLO_REQUEST_DESCRIPTION_MARKUP`, and `YOLO_REQUEST_HEADLINE` is safe in either; never run any
of them as a command. Desktop notification daemons such as swaync render the freedesktop
notification markup, which includes `<b>` and `<a href>`, so the plain text could hide or fake
text, or add a link, in the very notification you approve from. A markup field has `&`, `<`,
`>`, `"` and `'` replaced by XML entities, which suits a notifier that renders the freedesktop
notification (Pango) markup and nothing else: any other renderer, such as a Slack message,
needs its own escaping of the plain field. Your notifier may wrap a field in formatting of its
own, such as `"<b>$YOLO_REQUEST_HEADLINE</b>"`: only the values an agent writes need escaping.
An invisible format character, such as a zero-width space that would make a branch read as
`main` when it is not, shows escaped in every free-text field. Lookalike letters from other
scripts are not caught; the command SHA-256 at the end of the summary is the exact identity of
what you approve.

The command can answer, too. A plain `yolo` inside it is the yolo that started the broker,
whatever your `PATH` (`$YOLO_BIN` is its full path), so a notification with buttons is the whole
approval front end. The command does not inherit the pane identity of the terminal that started
the broker (`HERDR_PANE_ID`, `HERDR_TAB_ID`, `HERDR_WORKSPACE_ID`, `TMUX_PANE`,
`ZELLIJ_PANE_ID`, `WEZTERM_PANE`, `KITTY_WINDOW_ID`): the broker outlives that pane, so a hook
that drives a multiplexer names its pane itself. The session's identity (`HERDR_SOCKET_PATH`,
`HERDR_SESSION`, `TMUX` and the like) is kept, so the hook still reaches the right session. With `notify-send` from libnotify 0.7.10 or newer, which takes `-A` for a
button:

```toml
on_request = 'a=$(notify-send -u critical -A once="Allow once" -A deny=Deny "yolo: $YOLO_REQUEST_HEADLINE" "$YOLO_REQUEST_DESCRIPTION_MARKUP"); case "$a" in once) yolo approve "$YOLO_REQUEST_ID";; deny) yolo approve "$YOLO_REQUEST_ID" --deny;; esac'
```

A button does exactly what the command does, with the same checks and the same audit record. A
longer hook can live in a script the key names instead. Things to know:

- **It only notifies.** yolo reads nothing back from the command; the only decision is a
  `yolo approve` someone runs. A notification that is dismissed or times out leaves the request
  waiting for `yolo approve`.
- **It runs until the request is answered or expires**, so a notification can stay on screen
  for the hour. Several requests run several commands at once. Pass `-u critical` to
  `notify-send`: the desktop notification spec asks that a critical notification not expire
  on its own, so it waits for you. A toast timeout (`-t`) is optional: a notification you
  ignore until the request expires is not a failure.
- **When the request stops waiting, the command is stopped.** Answered (by its own button or a
  `yolo approve` anywhere), expired, or refused because its jail stopped: yolo sends `SIGINT`
  to the command and everything it started, then `SIGKILL` to whatever is left two seconds
  later. `notify-send` from libnotify 0.8.0 or newer, waiting on `-A` or `-w`, closes its
  notification on `SIGINT`, so the example above cleans up after itself. For a notifier that
  does not, a script can close it from a `trap ... INT`.
- **A failure is visible.** A command that exits non-zero, or cannot start, is recorded on the
  request: `yolo approve` shows `your on_request failed, so you may not have been notified: hook
  exited 127: sh: notify-send: not found`, `--json` carries the last of its output, and the
  agent is told your notifier failed.
- **The summary is the agent's words.** The command, the branch names and the repository come
  from the jail. Show them; never run them or paste them into a shell.
- **Over SSH**, the notification pops up on the desktop of the machine the broker runs on, which
  may be unattended.
- **Only your user config may set it.** A project's `yolo-jail.toml` naming it is a `yolo check`
  error that refuses the launch, because the command runs on your machine and can approve.

**A watcher, for a service or a UI of your own.** `yolo approve --watch --json` prints the
current state of every store as one JSON object, then one JSON object per event, each on its own
line, until interrupted: a request filed, decided or expired, a grant used, ended or revoked. Every
object carries `"schema": 1`. A UI built on it decides through `yolo approve`, like a button.
**It must never accept an approval from a surface a jail can reach**, such as a port your machine
listens on: a jail can connect to that port, and would approve its own request.

### What it records

Every request and every decision is in the broker's record on your machine. `yolo audit` lists
the calls each broker was sent, whether they ran, were refused or needed an approval; `yolo audit
--service git` or `--service github` narrows it to one. yolo mounts that record into no jail.

## See also

- [Git Without a Credential](git.md): the push grants and the working set of branches.
- [GitHub Without a Token](github.md): the `gh` grants and the working set of PRs and issues.
- [Host Access and Loopholes](loopholes.md): grants for the journal, host processes, serial
  devices and the rest.
- [Configuration](../reference/configuration.md#approving-config-changes): the config-change
  prompt that approves every change to a project's config.
